package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	composeloader "github.com/compose-spec/compose-go/v2/loader"
	composetypes "github.com/compose-spec/compose-go/v2/types"
	"k8s.io/apimachinery/pkg/runtime"

	k8sprojection "github.com/wyrd-company/devcontainer-builder/pkg/projection/kubernetes"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func main() {
	objects, err := buildObjects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "render Kubernetes manifests: %v\n", err)
		os.Exit(1)
	}
	data, err := k8sprojection.EncodeYAMLDocuments(objects)
	if err != nil {
		fmt.Fprintf(os.Stderr, "render Kubernetes manifests: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "write Kubernetes manifests: %v\n", err)
		os.Exit(1)
	}
}

func buildObjects() ([]runtime.Object, error) {
	var out []runtime.Object
	addProject := func(req k8sprojection.ProjectRequest) error {
		pj, err := k8sprojection.Project(req)
		if err != nil {
			return err
		}
		out = append(out, pj.Objects()...)
		return nil
	}

	if err := addProject(k8sprojection.ProjectRequest{
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
	}); err != nil {
		return nil, err
	}

	if err := addProject(k8sprojection.ProjectRequest{
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
	}); err != nil {
		return nil, err
	}

	if err := addProject(k8sprojection.ProjectRequest{
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
	}); err != nil {
		return nil, err
	}

	compose, err := loadComposeProject()
	if err != nil {
		return nil, err
	}
	cp, err := k8sprojection.ProjectCompose(k8sprojection.ComposeProjectRequest{
		Spec: &spec.DevContainer{
			DockerComposeFile: spec.StringOrSlice{"docker-compose.yml"},
			Service:           "app",
			WorkspaceFolder:   "/workspace/project",
		},
		SpecPath:       "/repo/.devcontainer/devcontainer.json",
		LocalWorkspace: "/repo",
		Project:        compose,
		Name:           "compose-workspace",
		Namespace:      "dev",
		Labels: map[string]string{
			"app.kubernetes.io/name": "devcontainer",
		},
		LocalEnv: map[string]string{"API_TOKEN": "secret-value"},
	})
	if err != nil {
		return nil, err
	}
	out = append(out, cp.Objects()...)
	return out, nil
}

func loadComposeProject() (*composetypes.Project, error) {
	dir, err := os.MkdirTemp("", "dcb-k8s-render-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	composePath := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(composeBody), 0o644); err != nil {
		return nil, err
	}
	details := composetypes.ConfigDetails{
		WorkingDir:  dir,
		ConfigFiles: []composetypes.ConfigFile{{Filename: composePath}},
	}
	return composeloader.LoadWithContext(context.Background(), details, func(o *composeloader.Options) {
		o.SetProjectName("test_proj", true)
	})
}

const composeBody = `services:
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
