package dockercompose

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestApplyProfiles_KeepsPrimaryFiltersOthers(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	body := `services:
  app:
    image: alpine
  perftools:
    image: alpine
    profiles: ["debug"]
  metrics:
    image: alpine
    profiles: ["metrics"]
`
	if err := os.WriteFile(composePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(specPath, []byte(`{"dockerComposeFile": "docker-compose.yml", "service": "app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dc, err := spec.Load(specPath)
	if err != nil {
		t.Fatalf("spec.Load: %v", err)
	}
	proj, err := LoadProject(context.Background(), LoadInputs{Spec: dc, Workspace: dir})
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	// No profiles requested → only services without `profiles:` are
	// active. The two profile-gated services drop out.
	out, err := applyProfiles(proj, nil, []string{"app"})
	if err != nil {
		t.Fatalf("applyProfiles: %v", err)
	}
	if got := out.ServiceNames(); !equalStrings(got, []string{"app"}) {
		t.Errorf("no profiles: got %v want [app]", got)
	}

	// Activate "debug" → app + perftools come up.
	out, err = applyProfiles(proj, []string{"debug"}, []string{"app"})
	if err != nil {
		t.Fatalf("applyProfiles debug: %v", err)
	}
	got := out.ServiceNames()
	sort.Strings(got)
	if !equalStrings(got, []string{"app", "perftools"}) {
		t.Errorf("debug profile: got %v want [app perftools]", got)
	}

	// "*" wildcard pulls every service in.
	out, err = applyProfiles(proj, []string{"*"}, []string{"app"})
	if err != nil {
		t.Fatalf("applyProfiles wildcard: %v", err)
	}
	got = out.ServiceNames()
	sort.Strings(got)
	if !equalStrings(got, []string{"app", "metrics", "perftools"}) {
		t.Errorf("wildcard: got %v want [app metrics perftools]", got)
	}
}

func TestApplyProfiles_KeepsRunServicesEvenIfProfileGated(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	body := `services:
  app:
    image: alpine
  worker:
    image: alpine
    profiles: ["batch"]
`
	if err := os.WriteFile(composePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(specPath, []byte(`{"dockerComposeFile": "docker-compose.yml", "service": "app", "runServices": ["worker"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dc, err := spec.Load(specPath)
	if err != nil {
		t.Fatalf("spec.Load: %v", err)
	}
	proj, err := LoadProject(context.Background(), LoadInputs{Spec: dc, Workspace: dir})
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	// keepEnabled covers app + worker — worker's "batch" profile is
	// activated transitively even though Profiles is empty.
	out, err := applyProfiles(proj, nil, []string{"app", "worker"})
	if err != nil {
		t.Fatalf("applyProfiles: %v", err)
	}
	got := out.ServiceNames()
	sort.Strings(got)
	if !equalStrings(got, []string{"app", "worker"}) {
		t.Errorf("keepEnabled with profile: got %v want [app worker]", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
