package kubernetes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestProjectCompose_ConfigBecomesConfigMap(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "app.conf")
	if err := os.WriteFile(configFile, []byte("hello-config"), 0o644); err != nil {
		t.Fatal(err)
	}
	composeBody := `services:
  app:
    image: alpine
    configs:
      - source: app-config
configs:
  app-config:
    file: ` + configFile + `
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
	if len(out.ConfigMaps) != 1 {
		t.Fatalf("ConfigMaps: got %d want 1", len(out.ConfigMaps))
	}
	cm := out.ConfigMaps[0]
	if cm.Namespace != "dev" {
		t.Errorf("namespace: got %q", cm.Namespace)
	}
	gotKey := ""
	for k := range cm.BinaryData {
		gotKey = k
	}
	if gotKey == "" {
		t.Fatal("ConfigMap has no data")
	}
	if string(cm.BinaryData[gotKey]) != "hello-config" {
		t.Errorf("ConfigMap data: got %q want hello-config", string(cm.BinaryData[gotKey]))
	}
	primary := out.Pod.Spec.Containers[0]
	gotMount := false
	for _, m := range primary.VolumeMounts {
		if m.MountPath == "/app-config" {
			gotMount = true
			if !m.ReadOnly {
				t.Errorf("config mount should be ReadOnly")
			}
		}
	}
	if !gotMount {
		t.Error("primary container should mount /app-config")
	}
}

func TestProjectCompose_SecretBecomesSecret(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
    secrets:
      - api-key
secrets:
  api-key:
    environment: API_KEY
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
		Namespace:      "dev",
		LocalEnv:       map[string]string{"API_KEY": "abc123"},
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	if len(out.Secrets) != 1 {
		t.Fatalf("Secrets: got %d want 1", len(out.Secrets))
	}
	s := out.Secrets[0]
	if s.Type != corev1.SecretTypeOpaque {
		t.Errorf("Secret type: got %q want Opaque", s.Type)
	}
	gotKey := ""
	for k := range s.Data {
		gotKey = k
	}
	if string(s.Data[gotKey]) != "abc123" {
		t.Errorf("Secret data: got %q want abc123", string(s.Data[gotKey]))
	}
	primary := out.Pod.Spec.Containers[0]
	gotMount := false
	for _, m := range primary.VolumeMounts {
		if m.MountPath == "/run/secrets/api-key" {
			gotMount = true
		}
	}
	if !gotMount {
		t.Error("primary container should mount /run/secrets/api-key")
	}
}

func TestProjectCompose_ExternalSecretReferencesExistingResource(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
    secrets:
      - api-key
secrets:
  api-key:
    external: true
    name: my-existing-secret
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
	if len(out.Secrets) != 0 {
		t.Errorf("external secret should not generate a Secret; got %d", len(out.Secrets))
	}
	primary := out.Pod.Spec.Containers[0]
	for _, m := range primary.VolumeMounts {
		if m.MountPath == "/run/secrets/api-key" {
			vol := findVolume(out.Pod, m.Name)
			if vol == nil || vol.Secret == nil || vol.Secret.SecretName != "my-existing-secret" {
				t.Errorf("expected reference to my-existing-secret, got %v", vol)
			}
		}
	}
}

func TestProjectCompose_ProfilesFilter(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
  perftools:
    image: alpine
    profiles: ["debug"]
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	// Without profiles, only app comes through.
	out, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	if len(out.Pod.Spec.Containers) != 1 || out.Pod.Spec.Containers[0].Name != "app" {
		t.Errorf("no profiles: got %d containers, want 1 (app)", len(out.Pod.Spec.Containers))
	}

	// With debug profile, perftools comes too.
	out, err = ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
		Profiles:       []string{"debug"},
	})
	if err != nil {
		t.Fatalf("ProjectCompose: %v", err)
	}
	if len(out.Pod.Spec.Containers) != 2 {
		t.Errorf("debug profile: got %d containers, want 2", len(out.Pod.Spec.Containers))
	}
}

func TestProjectCompose_RejectsExternalSecretEnvLookupMissing(t *testing.T) {
	composeBody := `services:
  app:
    image: alpine
    secrets:
      - api-key
secrets:
  api-key:
    environment: API_KEY
`
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	dc, proj, workspace := loadProjectForTest(t, composeBody, specBody)

	_, err := ProjectCompose(ComposeProjectRequest{
		Spec:           dc,
		LocalWorkspace: workspace,
		Project:        proj,
		Name:           "alice-dev",
		// LocalEnv intentionally omits API_KEY.
	})
	if err == nil {
		t.Fatal("expected error for missing env lookup")
	}
	if !strings.Contains(err.Error(), "API_KEY") {
		t.Errorf("error should mention env name; got %q", err.Error())
	}
}
