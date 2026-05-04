package kubernetes

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

var updateManifestFiles = flag.Bool("update-manifests", false, "rewrite Kubernetes manifest fixtures")

func TestProjectYAMLDocuments(t *testing.T) {
	cases := []struct {
		name    string
		objects []runtime.Object
	}{
		{
			name: "image",
			objects: mustProject(t, ProjectRequest{
				Spec: &spec.DevContainer{
					Image:           "alpine:3.20",
					WorkspaceFolder: "/workspaces/app",
					ContainerEnv: map[string]any{
						"APP_ENV": "dev",
					},
					ForwardPorts: []any{3000},
				},
				SpecPath:       "/repo/.devcontainer/devcontainer.json",
				LocalWorkspace: "/repo",
				Image:          "alpine:3.20",
				Name:           "image-workspace",
				Namespace:      "dev",
				Labels: map[string]string{
					"app.kubernetes.io/name": "devcontainer",
				},
			}).Objects(),
		},
		{
			name: "dockerfile-image",
			objects: mustProject(t, ProjectRequest{
				Spec: &spec.DevContainer{
					Build:           &spec.Build{Dockerfile: "Dockerfile", Context: ".."},
					WorkspaceFolder: "/workspace/source",
					ContainerUser:   "1000:1000",
				},
				SpecPath:       "/repo/.devcontainer/devcontainer.json",
				LocalWorkspace: "/repo",
				Image:          "registry.example.com/devcontainers/app:build-output",
				Name:           "dockerfile-workspace",
				Namespace:      "dev",
			}).Objects(),
		},
		{
			name: "runargs",
			objects: mustProject(t, ProjectRequest{
				Spec: &spec.DevContainer{
					Image:           "ubuntu:24.04",
					WorkspaceFolder: "/workspace/project",
					RunArgs: []string{
						"--name", "runargs-workspace",
						"--hostname", "devbox",
						"--domainname", "cluster.local",
						"--user", "1001:1002",
						"--workdir", "/workspace/project",
						"--network", "host",
						"--ipc", "shareable",
						"--pid", "container",
						"--runtime", "gvisor",
						"--restart", "on-failure",
						"--stop-timeout", "20",
						"--memory", "512m",
						"--memory-reservation", "256m",
						"--cpus", "1.5",
						"--cap-add", "SYS_PTRACE",
						"--cap-drop", "MKNOD",
						"--security-opt", "no-new-privileges:true",
						"--label", "app.kubernetes.io/component=workspace",
						"--env", "EXTRA=1",
						"--add-host", "host.docker.internal:127.0.0.1",
						"--dns", "1.1.1.1",
						"--dns-search", "svc.local",
						"--dns-option", "ndots:2",
						"--group-add", "1003",
						"--expose", "3000/tcp",
						"--publish", "8080:80/tcp",
						"--sysctl", "net.ipv4.ip_unprivileged_port_start=0",
						"--tmpfs", "/tmp",
						"--mount", "type=bind,source=/var/cache/dev,target=/cache,readonly",
						"--gpus", "1",
						"--annotation", "example.com/owner=devtools",
					},
				},
				SpecPath:       "/repo/.devcontainer/devcontainer.json",
				LocalWorkspace: "/repo",
				Image:          "ubuntu:24.04",
				Namespace:      "dev",
			}).Objects(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertYAMLFile(t, tc.name+".yaml", tc.objects)
		})
	}
}

func TestProjectComposeYAMLDocuments(t *testing.T) {
	composeBody := `services:
  app:
    image: ghcr.io/acme/app:dev
    environment:
      APP_ENV: dev
    ports:
      - "3000:3000"
    volumes:
      - data:/workspace/.cache
      - /var/run/docker.sock:/var/run/docker.sock:ro
    configs:
      - app-config
    secrets:
      - api-token
  db:
    image: postgres:16
    command: ["postgres", "-c", "fsync=off"]
    environment:
      POSTGRES_PASSWORD: example
    volumes:
      - data:/var/lib/postgresql/data
volumes:
  data:
configs:
  app-config:
    content: |
      mode=dev
secrets:
  api-token:
    environment: API_TOKEN
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app", "workspaceFolder": "/workspace/project"}`
	dc, proj, _ := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		SpecPath:       "/repo/.devcontainer/devcontainer.json",
		LocalWorkspace: "/repo",
		Project:        proj,
		Name:           "compose-workspace",
		Namespace:      "dev",
		Labels: map[string]string{
			"app.kubernetes.io/name": "devcontainer",
		},
		LocalEnv: map[string]string{"API_TOKEN": "secret-value"},
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	assertYAMLFile(t, "compose.yaml", out.Objects())
}

func TestYAMLRoundTripPreservesResourceFields(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
    volumes:
      - data:/data
    secrets:
      - api-key
  worker:
    image: redis:7
volumes:
  data:
secrets:
  api-key:
    environment: API_KEY
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, _ := loadProjectForTest(t, composeBody, specBody)
	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: "/repo",
		Project:        proj,
		Name:           "roundtrip-workspace",
		Namespace:      "dev",
		LocalEnv:       map[string]string{"API_KEY": "abc123"},
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}

	for _, obj := range out.Objects() {
		data := encodeObject(t, obj)
		decoded := decodeObject(t, data)
		again := encodeObject(t, decoded)
		if !bytes.Equal(data, again) {
			t.Fatalf("%T did not round trip stably\nfirst:\n%s\nsecond:\n%s", obj, data, again)
		}
		assertDecodedResource(t, decoded)
	}
}

func mustProject(t *testing.T, req ProjectRequest) *Projection {
	t.Helper()
	out, err := Project(req)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	return out
}

func assertYAMLFile(t *testing.T, name string, objects []runtime.Object) {
	t.Helper()
	got, err := EncodeYAMLDocuments(objects)
	if err != nil {
		t.Fatalf("EncodeYAMLDocuments: %v", err)
	}
	path := filepath.Join("testdata", "manifests", name)
	if *updateManifestFiles {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s changed\nwant:\n%s\ngot:\n%s", path, want, got)
	}
}

func encodeObject(t *testing.T, obj runtime.Object) []byte {
	t.Helper()
	prepared, err := objectWithKind(obj)
	if err != nil {
		t.Fatal(err)
	}
	ser := json.NewYAMLSerializer(json.DefaultMetaFactory, manifestScheme, manifestScheme)
	var b bytes.Buffer
	if err := ser.Encode(prepared, &b); err != nil {
		t.Fatalf("Encode %T: %v", obj, err)
	}
	return b.Bytes()
}

func decodeObject(t *testing.T, data []byte) runtime.Object {
	t.Helper()
	ser := json.NewYAMLSerializer(json.DefaultMetaFactory, manifestScheme, manifestScheme)
	obj, _, err := ser.Decode(data, nil, nil)
	if err != nil {
		t.Fatalf("Decode: %v\n%s", err, data)
	}
	return obj
}

func assertDecodedResource(t *testing.T, obj runtime.Object) {
	t.Helper()
	switch v := obj.(type) {
	case *corev1.Pod:
		if v.Name == "" || v.Namespace != "dev" {
			t.Fatalf("pod metadata = %s/%s", v.Namespace, v.Name)
		}
		if len(v.Spec.Containers) == 0 {
			t.Fatal("pod has no containers")
		}
	case *corev1.PersistentVolumeClaim:
		if v.Spec.Resources.Requests.Storage().IsZero() {
			t.Fatal("pvc has no storage request")
		}
	case *corev1.ConfigMap:
		if len(v.BinaryData)+len(v.Data) == 0 {
			t.Fatal("configmap has no data")
		}
	case *corev1.Secret:
		if v.Type != corev1.SecretTypeOpaque {
			t.Fatalf("secret type = %q", v.Type)
		}
		if len(v.Data) == 0 {
			t.Fatal("secret has no data")
		}
	default:
		t.Fatalf("unexpected object type %T", obj)
	}
}
