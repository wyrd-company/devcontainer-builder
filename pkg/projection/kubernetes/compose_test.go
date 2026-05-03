package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	composeloader "github.com/compose-spec/compose-go/v2/loader"
	composetypes "github.com/compose-spec/compose-go/v2/types"
	corev1 "k8s.io/api/core/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// loadProjectForTest writes a docker-compose.yml + devcontainer.json
// pair into a temp dir and returns the loaded *types.Project plus the
// parsed spec. Doesn't depend on the dockercompose host package — that
// would create an import cycle.
func loadProjectForTest(t *testing.T, composeBody, specBody string) (*spec.DevContainer, *composetypes.Project, string) {
	t.Helper()
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte(composeBody), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(specPath, []byte(specBody), 0o644); err != nil {
		t.Fatal(err)
	}
	dc, err := spec.Load(specPath)
	if err != nil {
		t.Fatalf("spec.Load: %v", err)
	}
	details := composetypes.ConfigDetails{
		WorkingDir:  dir,
		ConfigFiles: []composetypes.ConfigFile{{Filename: composePath}},
	}
	proj, err := composeloader.LoadWithContext(context.Background(), details, func(o *composeloader.Options) {
		o.SetProjectName("test_proj", true)
	})
	if err != nil {
		t.Fatalf("loader.LoadWithContext: %v", err)
	}
	return dc, proj, dir
}

func TestProjectCompose_PrimaryAndSidecar(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine:3.20
  worker:
    image: redis:7
    command: ["redis-server", "--appendonly", "yes"]
    environment:
      LOG_LEVEL: info
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
		Namespace:      "dev",
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	pod := out.Pod
	if pod.Name != "alice-dev" || pod.Namespace != "dev" {
		t.Errorf("metadata: got %s/%s want alice-dev/dev", pod.Namespace, pod.Name)
	}
	if len(pod.Spec.Containers) != 2 {
		t.Fatalf("containers: got %d want 2", len(pod.Spec.Containers))
	}
	primary := pod.Spec.Containers[0]
	if primary.Name != "app" {
		t.Errorf("primary name: got %s want app", primary.Name)
	}
	if primary.Image != "alpine:3.20" {
		t.Errorf("primary image: got %s want alpine:3.20", primary.Image)
	}
	if primary.Command[0] != "/bin/sh" || primary.Args[0] != "-c" {
		t.Errorf("primary should use keepalive /bin/sh -c; got %v %v", primary.Command, primary.Args)
	}

	side := pod.Spec.Containers[1]
	if side.Name != "worker" {
		t.Errorf("sidecar name: got %s want worker", side.Name)
	}
	if side.Image != "redis:7" {
		t.Errorf("sidecar image: got %s want redis:7", side.Image)
	}
	if len(side.Args) != 3 || side.Args[0] != "redis-server" {
		t.Errorf("sidecar should keep its compose command; got %v", side.Args)
	}
	if len(side.Env) != 1 || side.Env[0].Name != "LOG_LEVEL" || side.Env[0].Value != "info" {
		t.Errorf("sidecar env: got %v", side.Env)
	}

	// Pod-level annotations carry compose origin.
	if pod.Annotations[AnnotationComposeProject] != "test_proj" {
		t.Errorf("compose project annotation: got %q", pod.Annotations[AnnotationComposeProject])
	}
	if pod.Annotations[AnnotationComposeService] != "app" {
		t.Errorf("compose service annotation: got %q", pod.Annotations[AnnotationComposeService])
	}
}

func TestProjectCompose_NamedVolumeBecomesPVC(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
    volumes:
      - data:/var/data
  worker:
    image: alpine
    volumes:
      - data:/srv/data
volumes:
  data:
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	if len(out.PersistentVolumeClaims) != 1 {
		t.Fatalf("PVCs: got %d want 1", len(out.PersistentVolumeClaims))
	}
	pvc := out.PersistentVolumeClaims[0]
	if !strings.Contains(pvc.Name, "data") {
		t.Errorf("pvc name should reference volume key: got %q", pvc.Name)
	}
	if got := pvc.Spec.AccessModes; len(got) != 1 || got[0] != corev1.ReadWriteOnce {
		t.Errorf("access modes: got %v want [ReadWriteOnce]", got)
	}
	if pvc.Spec.Resources.Requests.Storage().IsZero() {
		t.Errorf("default storage request: should not be zero")
	}

	// The Pod refers to the same PVC from both containers, sharing
	// one volume.
	pod := out.Pod
	pvcVols := 0
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == pvc.Name {
			pvcVols++
		}
	}
	if pvcVols != 1 {
		t.Errorf("pod should have one Volume referencing the PVC, got %d", pvcVols)
	}

	// Both containers mount it.
	mountsFor := func(name string) []corev1.VolumeMount {
		for _, c := range pod.Spec.Containers {
			if c.Name == name {
				return c.VolumeMounts
			}
		}
		return nil
	}
	pmounts := mountsFor("app")
	wmounts := mountsFor("worker")
	gotPrimary := false
	for _, m := range pmounts {
		if m.MountPath == "/var/data" {
			gotPrimary = true
		}
	}
	gotSide := false
	for _, m := range wmounts {
		if m.MountPath == "/srv/data" {
			gotSide = true
		}
	}
	if !gotPrimary || !gotSide {
		t.Errorf("named volume should mount in both containers; primary=%v sidecar=%v", gotPrimary, gotSide)
	}
}

func TestProjectCompose_ExternalNamedVolumeReferencesExistingPVC(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
    volumes:
      - shared:/data
volumes:
  shared:
    external: true
    name: my-existing-pvc
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	if len(out.PersistentVolumeClaims) != 0 {
		t.Errorf("external volume should not generate a PVC; got %d", len(out.PersistentVolumeClaims))
	}
	primary := out.Pod.Spec.Containers[0]
	gotMount := false
	for _, m := range primary.VolumeMounts {
		if m.MountPath == "/data" {
			gotMount = true
			vol := findVolume(out.Pod, m.Name)
			if vol == nil || vol.PersistentVolumeClaim == nil || vol.PersistentVolumeClaim.ClaimName != "my-existing-pvc" {
				t.Errorf("external volume should reference existing PVC name; got %v", vol)
			}
		}
	}
	if !gotMount {
		t.Error("external volume mount not found on primary container")
	}
}

func TestProjectCompose_BindBecomesHostPath(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
    volumes:
      - /host/path:/in/container
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	primary := out.Pod.Spec.Containers[0]
	gotMount := false
	for _, m := range primary.VolumeMounts {
		if m.MountPath == "/in/container" {
			gotMount = true
			vol := findVolume(out.Pod, m.Name)
			if vol == nil || vol.HostPath == nil || vol.HostPath.Path != "/host/path" {
				t.Errorf("expected HostPath:/host/path; got %v", vol)
			}
		}
	}
	if !gotMount {
		t.Error("bind mount not present on primary container")
	}
}

func TestProjectCompose_RejectsCommodeRunAsSidecarWithoutImage(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
  builder:
    build:
      context: .
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	_, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
	})
	if err == nil {
		t.Fatal("expected error for sidecar without image; got nil")
	}
	if !strings.Contains(err.Error(), "build") || !strings.Contains(err.Error(), "out of scope") {
		t.Errorf("error should call out the compose build scope cut; got %q", err.Error())
	}
}

func TestProjectCompose_PrimaryImageOverride(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine:3.20
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		PrimaryImage:   "registry/derived:tag",
		Name:           "alice-dev",
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	if got := out.Pod.Spec.Containers[0].Image; got != "registry/derived:tag" {
		t.Errorf("primary image override: got %q want registry/derived:tag", got)
	}
}

func TestProjectCompose_HostAliasesForServiceNameDNS(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
    networks:
      default:
        aliases: ["api"]
  db:
    image: postgres:16
  cache:
    image: redis:7
    profiles: ["full"]
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	if len(out.Pod.Spec.HostAliases) != 1 {
		t.Fatalf("HostAliases: got %d entries, want 1", len(out.Pod.Spec.HostAliases))
	}
	got := out.Pod.Spec.HostAliases[0]
	if got.IP != "127.0.0.1" {
		t.Errorf("IP: got %q want 127.0.0.1", got.IP)
	}
	hosts := map[string]bool{}
	for _, h := range got.Hostnames {
		hosts[h] = true
	}
	if !hosts["app"] || !hosts["db"] || !hosts["api"] {
		t.Errorf("hostnames should include app + db + api alias; got %v", got.Hostnames)
	}
	// "cache" is profile-gated and not active, so it must not appear.
	if hosts["cache"] {
		t.Errorf("profile-gated service should not appear in hostAliases; got %v", got.Hostnames)
	}
}

func TestProjectCompose_HostAliasesIncludeProfileEnabledServices(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
  cache:
    image: redis:7
    profiles: ["full"]
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
		Profiles:       []string{"full"},
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	hosts := map[string]bool{}
	for _, h := range out.Pod.Spec.HostAliases[0].Hostnames {
		hosts[h] = true
	}
	if !hosts["cache"] {
		t.Errorf("activated profile-gated service should appear in hostAliases; got %v", out.Pod.Spec.HostAliases[0].Hostnames)
	}
}

func findVolume(pod *corev1.Pod, name string) *corev1.Volume {
	for i := range pod.Spec.Volumes {
		if pod.Spec.Volumes[i].Name == name {
			return &pod.Spec.Volumes[i]
		}
	}
	return nil
}
