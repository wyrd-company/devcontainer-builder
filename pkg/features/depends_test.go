package features

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// writeLocalFeature writes a minimal feature under base/<id>. extra is
// spliced into devcontainer-feature.json.
func writeLocalFeature(t *testing.T, base, id, extra string) {
	t.Helper()
	dir := filepath.Join(base, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"` + id + `","version":"1.0.0"` + extra + `}`
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func userFeatures(pairs ...string) spec.OrderedMap {
	om := spec.OrderedMap{Values: map[string]json.RawMessage{}}
	for i := 0; i < len(pairs); i += 2 {
		om.Keys = append(om.Keys, pairs[i])
		om.Values[pairs[i]] = json.RawMessage(pairs[i+1])
	}
	return om
}

func resolveLocal(t *testing.T, base string, om spec.OrderedMap) ([]Resolved, error) {
	t.Helper()
	return New().Resolve(om, nil, filepath.Join(t.TempDir(), "stage"), base)
}

type installed struct {
	id      string
	options string
}

func summarize(rs []Resolved) []installed {
	out := make([]installed, len(rs))
	for i, r := range rs {
		b, _ := json.Marshal(r.Options)
		out[i] = installed{r.Metadata.ID, string(b)}
	}
	return out
}

func TestResolve_DependsOnFetchesAndInstallsDependencyFirst(t *testing.T) {
	base := t.TempDir()
	writeLocalFeature(t, base, "alpha", `,"dependsOn":{"./beta":{"level":"high"}}`)
	writeLocalFeature(t, base, "beta", "")

	got, err := resolveLocal(t, base, userFeatures("./alpha", `{}`))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := []installed{{"beta", `{"level":"high"}`}, {"alpha", `{}`}}
	if g := summarize(got); len(g) != 2 || g[0] != want[0] || g[1] != want[1] {
		t.Fatalf("installed = %v, want %v", g, want)
	}
}

func TestResolve_DependsOnRecursive(t *testing.T) {
	base := t.TempDir()
	writeLocalFeature(t, base, "alpha", `,"dependsOn":{"./beta":{}}`)
	writeLocalFeature(t, base, "beta", `,"dependsOn":{"./gamma":{}}`)
	writeLocalFeature(t, base, "gamma", "")

	got, err := resolveLocal(t, base, userFeatures("./alpha", `{}`))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if g := strings.Join(ids(got), ","); g != "gamma,beta,alpha" {
		t.Fatalf("order = %s, want gamma,beta,alpha", g)
	}
}

func TestResolve_DependsOnSatisfiedByEqualUserFeature(t *testing.T) {
	base := t.TempDir()
	writeLocalFeature(t, base, "alpha", `,"dependsOn":{"./beta":{"level":"high"}}`)
	writeLocalFeature(t, base, "beta", "")

	got, err := resolveLocal(t, base, userFeatures("./alpha", `{}`, "./beta", `{"level":"high"}`))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if g := strings.Join(ids(got), ","); g != "beta,alpha" {
		t.Fatalf("order = %s, want beta installed once before alpha", g)
	}
}

// Upstream installs a feature once per distinct options value: a user
// entry with other options does not satisfy the dependency.
func TestResolve_DependsOnWithOtherOptionsInstallsTwice(t *testing.T) {
	base := t.TempDir()
	writeLocalFeature(t, base, "alpha", `,"dependsOn":{"./beta":{"level":"high"}}`)
	writeLocalFeature(t, base, "beta", "")

	got, err := resolveLocal(t, base, userFeatures("./alpha", `{}`, "./beta", `{}`))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	g := summarize(got)
	want := []installed{{"beta", `{}`}, {"beta", `{"level":"high"}`}, {"alpha", `{}`}}
	if len(g) != 3 || g[0] != want[0] || g[1] != want[1] || g[2] != want[2] {
		t.Fatalf("installed = %v, want %v", g, want)
	}
	if got[0].SafeID == got[1].SafeID {
		t.Fatalf("both beta installs share staging dir %s", got[0].SafeID)
	}
}

func TestResolve_DependsOnCycleIsError(t *testing.T) {
	base := t.TempDir()
	writeLocalFeature(t, base, "alpha", `,"dependsOn":{"./beta":{}}`)
	writeLocalFeature(t, base, "beta", `,"dependsOn":{"./alpha":{}}`)

	_, err := resolveLocal(t, base, userFeatures("./alpha", `{}`))
	if err == nil || !strings.Contains(err.Error(), "circular") {
		t.Fatalf("expected circular dependency error, got %v", err)
	}
}

func TestResolve_DependsOnOverridesUserOrder(t *testing.T) {
	base := t.TempDir()
	writeLocalFeature(t, base, "alpha", `,"dependsOn":{"./beta":{}}`)
	writeLocalFeature(t, base, "beta", "")

	// overrideFeatureInstallOrder cannot pull alpha ahead of its
	// dependency.
	got, err := New().Resolve(userFeatures("./alpha", `{}`, "./beta", `{}`), []string{"./alpha"}, filepath.Join(t.TempDir(), "stage"), base)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if g := strings.Join(ids(got), ","); g != "beta,alpha" {
		t.Fatalf("order = %s, want beta,alpha", g)
	}
}
