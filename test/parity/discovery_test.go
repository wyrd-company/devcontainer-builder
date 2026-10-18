// relationships:
//
//	references: [README, runner]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This adapter creates a real stopped container from the prepared runtime
// arguments. Observation still crosses the real Docker discovery/inspect boundary.
type discoveryAdapter struct {
	t      *testing.T
	folder string
	value  string
	id     string
	failUp bool
}

func (*discoveryAdapter) Name() string { return "discovery regression" }
func (a *discoveryAdapter) Up(ctx context.Context, r *Runner, workspace string) ([]byte, error) {
	if a.id == "" {
		body, err := os.ReadFile(filepath.Join(workspace, ".devcontainer.json"))
		if err != nil {
			return nil, err
		}
		var config struct{ RunArgs []string }
		if err := json.Unmarshal(body, &config); err != nil {
			return nil, err
		}
		args := append([]string{"create"}, config.RunArgs...)
		if a.folder != "missing" {
			folder := workspace
			if a.folder == "different" {
				folder = "/different/workspace"
			}
			args = append(args, "--label", "devcontainer.local_folder="+folder)
		}
		args = append(args, "--env", "VALUE="+a.value, "--env", "WORKSPACE_FOLDER=/workspaces/"+filepath.Base(workspace), "alpine:3.20", "true")
		body, err = r.command(ctx, "docker", args...)
		if err != nil {
			return nil, err
		}
		a.id = strings.TrimSpace(string(body))
		id := a.id
		// Register exact-handle fallback cleanup before any assertion or mutation.
		a.t.Cleanup(func() {
			body, err := r.command(context.Background(), "docker", "container", "inspect", id, "--format", "{{.Id}} {{index .Config.Labels \""+ownershipLabel+"\"}}")
			if err != nil { // Normal run cleanup may already have removed it.
				return
			}
			if strings.TrimSpace(string(body)) != id+" "+r.runID {
				a.t.Errorf("fallback ownership mismatch: %s", body)
				return
			}
			if _, err := r.command(context.Background(), "docker", "rm", "-f", id); err != nil {
				a.t.Error(err)
			}
		})
	}
	if a.failUp {
		return nil, fmt.Errorf("simulated lifecycle failure after container creation")
	}
	return json.Marshal(map[string]string{"outcome": "success", "containerId": a.id})
}

func discoveryRunner(t *testing.T) (*Runner, Fixture) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI absent")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".tmp"), 0755); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(root, "run.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	r, err := newRunner(root, log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.command(context.Background(), "docker", "info"); err != nil {
		t.Skip("Docker daemon unavailable")
	}
	if _, err := r.command(context.Background(), "docker", "image", "inspect", "alpine:3.20"); err != nil {
		if _, err := r.command(context.Background(), "docker", "pull", "alpine:3.20"); err != nil {
			t.Fatal(err)
		}
	}
	f := Fixture{Name: "sample", Path: "sample", Config: ".devcontainer.json"}
	path := filepath.Join(root, "reference/cli", f.Path)
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, f.Config), []byte(`{"image":"alpine:3.20"}`), 0644); err != nil {
		t.Fatal(err)
	}
	return r, f
}

func requireContainerObservations(t *testing.T, observations map[string]Observation, value string) {
	t.Helper()
	for _, dimension := range dimensions {
		if dimension != "result" && observations[dimension].Error != "" {
			t.Fatalf("%s unavailable: %+v", dimension, observations[dimension])
		}
	}
	env := observations["env"].Value.(map[string]any)
	if got := env["VALUE"]; got != value {
		t.Fatalf("observed another fixture/side: VALUE=%v, want %s", got, value)
	}
	if got := env["WORKSPACE_FOLDER"]; got != "/workspaces/<workspace-name>" {
		t.Fatalf("workspace name not normalized: %v", got)
	}
	labels := observations["labels"].Value.(map[string]any)
	for _, label := range []string{ownershipLabel, discoveryLabel} {
		if _, ok := labels[label]; ok {
			t.Fatalf("harness label scored: %s", label)
		}
	}
}

func TestObserveComparedLocalFolderDivergence(t *testing.T) {
	for _, folder := range []string{"missing", "different"} {
		for _, failUp := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/failure=%t", folder, failUp), func(t *testing.T) {
				r, f := discoveryRunner(t)
				ctx := context.Background()
				upstream := r.observe(ctx, &discoveryAdapter{t: t, value: "same"}, f, "upstream")
				local := r.observe(ctx, &discoveryAdapter{t: t, folder: folder, value: "same", failUp: failUp}, f, "go")
				requireContainerObservations(t, upstream, "same")
				requireContainerObservations(t, local, "same")
				if (local["result"].Error != "") != failUp {
					t.Fatalf("CLI result failure lost: %+v", local["result"])
				}
				result := compare(f.Name, upstream, local)
				if result.Dimensions["labels"].Pass {
					t.Fatal("local_folder divergence hidden")
				}
				labels := local["labels"].Value.(map[string]any)
				if folder == "missing" {
					if _, exists := labels["devcontainer.local_folder"]; exists {
						t.Fatal("missing local_folder manufactured")
					}
				} else if labels["devcontainer.local_folder"] != "/different/workspace" {
					t.Fatalf("different local_folder hidden: %v", labels)
				}
				output := t.TempDir()
				if err := writeReport(output, Report{Fixtures: []FixtureResult{result}}); err != nil {
					t.Fatal(err)
				}
				body, err := os.ReadFile(filepath.Join(output, "score.json"))
				if err != nil {
					t.Fatal(err)
				}
				var report Report
				if err := json.Unmarshal(body, &report); err != nil {
					t.Fatal(err)
				}
				d := report.Fixtures[0].Dimensions["labels"]
				if d.Pass || d.Upstream.Error != "" || d.Go.Error != "" {
					t.Fatalf("report lost observable divergence: %+v", d)
				}
			})
		}
	}
}

func TestObserveFixtureSideIsolation(t *testing.T) {
	r, f := discoveryRunner(t)
	ctx := context.Background()
	otherFixture := f
	otherFixture.Name = "another"
	adapters := []*discoveryAdapter{{t: t, value: "first"}, {t: t, value: "second"}, {t: t, value: "third"}}
	fixtures := []Fixture{f, f, otherFixture}
	sides := []string{"upstream", "go", "upstream"}
	// Populate all identities before asserting each observation again.
	for i, a := range adapters {
		r.observe(ctx, a, fixtures[i], sides[i])
	}
	// Same discovery identity but foreign run ownership must never be selected
	// or removed by this run's cleanup.
	foreign := *r
	foreign.runID += "-foreign"
	foreign.temp = filepath.Join(r.temp, "foreign")
	foreignAdapter := &discoveryAdapter{t: t, value: "foreign"}
	rForeign := foreign.observe(ctx, foreignAdapter, f, "upstream")
	requireContainerObservations(t, rForeign, "foreign")
	for i, a := range adapters {
		got := r.observe(ctx, a, fixtures[i], sides[i])
		requireContainerObservations(t, got, a.value)
	}
	if errors := r.cleanup(ctx); len(errors) != 0 {
		t.Fatalf("cleanup: %v", errors)
	}
	for _, a := range adapters {
		if _, err := r.command(ctx, "docker", "container", "inspect", a.id); err == nil {
			t.Fatalf("owned container not removed: %s", a.id)
		}
	}
	if _, err := r.command(ctx, "docker", "container", "inspect", foreignAdapter.id); err != nil {
		t.Fatal("foreign container removed", err)
	}
}

func TestObserveRejectsAmbiguousIdentity(t *testing.T) {
	r, f := discoveryRunner(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		a := &discoveryAdapter{t: t, value: "same"}
		got := r.observe(ctx, a, f, "go")
		if i == 0 {
			requireContainerObservations(t, got, "same")
		} else if got["env"].Error == "" {
			t.Fatal("ambiguous identity selected a container")
		}
	}
}

func TestObserveVerifiesInspectedIdentity(t *testing.T) {
	for _, changed := range []string{"id", "owner", "fixture-side"} {
		t.Run(changed, func(t *testing.T) {
			root := t.TempDir()
			f := Fixture{Name: "sample", Path: "sample", Config: ".devcontainer.json"}
			source := filepath.Join(root, "reference/cli", f.Path)
			if err := os.MkdirAll(source, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, f.Config), []byte(`{}`), 0644); err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("a", 64)
			inspectedID, owner, identity := id, "sample-run", "sample/go"
			switch changed {
			case "id":
				inspectedID = strings.Repeat("b", 64)
			case "owner":
				owner = "foreign-run"
			case "fixture-side":
				identity = "sample/upstream"
			}
			inspect, err := json.Marshal([]any{map[string]any{
				"Id": inspectedID, "Image": "sha256:sample",
				"Config": map[string]any{"Labels": map[string]string{ownershipLabel: owner, discoveryLabel: identity}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := "#!/bin/sh\ncase \"$1\" in\nps) printf '%s\\n' \"$PARITY_ID\" ;;\ninspect) printf '%s\\n' \"$PARITY_INSPECT\" ;;\nesac\n"
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			log, err := os.Create(filepath.Join(root, "run.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			r := &Runner{root: root, temp: t.TempDir(), runID: "sample-run", log: log, env: append(os.Environ(), "PARITY_ID="+id, "PARITY_INSPECT="+string(inspect))}
			// Reuse a completed adapter result so this test controls only discovery.
			a := &discoveryAdapter{t: t, id: id}
			got := r.observe(context.Background(), a, f, "go")
			if got["env"].Error == "" {
				t.Fatalf("accepted mismatched inspected %s", changed)
			}
		})
	}
}

// Derived names carry the run's workspace name; a run whose ID is a prefix of
// another's, or a name that only contains the ID mid-word, is foreign.
func TestCleanupRemovesOnlyRunNamedResources(t *testing.T) {
	r, _ := discoveryRunner(t)
	ctx := context.Background()
	owned := r.runID + "-sample-go"
	foreign := []string{r.runID + "9-sample-go", "x" + r.runID + "-sample-go"}
	type resource struct{ kind, name string }
	var created []resource
	create := func(kind, project string) string {
		name := "vsc-" + project
		var args []string
		switch kind {
		case "image":
			name += ":latest"
			args = []string{"image", "tag", "alpine:3.20", name}
		default:
			args = []string{kind, "create", "--label", composeProjectLabel + "=" + project + "_devcontainer", name}
		}
		if _, err := r.command(ctx, "docker", args...); err != nil {
			t.Fatal(err)
		}
		created = append(created, resource{kind, name})
		return name
	}
	t.Cleanup(func() {
		for _, c := range created {
			args := []string{c.kind, "rm", c.name}
			if c.kind == "image" {
				args = []string{"image", "rm", c.name}
			}
			r.command(context.Background(), "docker", args...)
		}
	})
	// dcb names a derived image from the workspace path, not the folder name.
	r2, f := discoveryRunner(t)
	workspace, err := r.prepare(f, "go")
	if err != nil {
		t.Fatal(err)
	}
	foreignWorkspace, err := r2.prepare(f, "go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{dcbImage(workspace), dcbImage(foreignWorkspace)} {
		if _, err := r.command(ctx, "docker", "image", "tag", "alpine:3.20", name); err != nil {
			t.Fatal(err)
		}
		created = append(created, resource{"image", name})
	}
	kinds := []string{"image", "network", "volume"}
	for _, kind := range kinds {
		create(kind, owned)
		for _, project := range foreign {
			create(kind, project)
		}
	}
	if errors := r.cleanup(ctx); len(errors) != 0 {
		t.Fatalf("cleanup: %v", errors)
	}
	for _, c := range created {
		_, err := r.command(ctx, "docker", c.kind, "inspect", c.name)
		if strings.Contains(c.name, owned) && !strings.Contains(c.name, "x"+owned) || c.name == dcbImage(workspace) {
			if err == nil {
				t.Errorf("owned %s not removed: %s", c.kind, c.name)
			}
		} else if err != nil {
			t.Errorf("foreign %s removed: %s", c.kind, c.name)
		}
	}
	if _, err := r.command(ctx, "docker", "image", "inspect", "alpine:3.20"); err != nil {
		t.Fatal("shared base image removed")
	}
}
