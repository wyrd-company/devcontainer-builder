// relationships:
//
//	references: [README, runner]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This adapter creates a real running container from the prepared runtime
// arguments. Observation still crosses the real Docker discovery/inspect boundary.
type discoveryAdapter struct {
	t       *testing.T
	folder  string
	value   string
	id      string
	failUp  bool
	user    string
	stopped bool
}

func (*discoveryAdapter) Name() string { return "discovery regression" }
func (a *discoveryAdapter) Up(ctx context.Context, r *Runner, workspace string, _ []string) ([]byte, error) {
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
		if a.user != "" {
			args = append(args, "--user", a.user)
		}
		args = append(args, "--env", "VALUE="+a.value, "--env", "WORKSPACE_FOLDER=/workspaces/"+filepath.Base(workspace), "alpine:3.20", "sleep", "infinity")
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
		if !a.stopped {
			if _, err := r.command(ctx, "docker", "start", id); err != nil {
				return nil, err
			}
		}
	}
	if a.failUp {
		return nil, fmt.Errorf("simulated lifecycle failure after container creation")
	}
	return json.Marshal(map[string]string{"outcome": "success", "containerId": a.id})
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
