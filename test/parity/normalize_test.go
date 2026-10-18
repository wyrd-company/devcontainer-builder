// relationships:
//
//	references: README
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizerEquivalentAndDifferentEnv(t *testing.T) {
	a := Normalizer{map[string]string{"/tmp/run-a/workspace": "<workspace>", "aaa": "<container>"}}
	b := Normalizer{map[string]string{"/tmp/run-b/workspace": "<workspace>", "bbb": "<container>"}}
	av, _ := jsonValue([]byte(`{"path":"/tmp/run-a/workspace/file","id":"aaa","env":{"VALUE":"one"},"commands":["first","second"]}`))
	bv, _ := jsonValue([]byte(`{"id":"bbb","commands":["first","second"],"env":{"VALUE":"one"},"path":"/tmp/run-b/workspace/file"}`))
	if !reflect.DeepEqual(a.Value(av), b.Value(bv)) {
		t.Fatal("identity, path and key order should normalize")
	}
	bv.(map[string]any)["env"].(map[string]any)["VALUE"] = "two"
	if reflect.DeepEqual(a.Value(av), b.Value(bv)) {
		t.Fatal("genuine env difference hidden")
	}
	bv.(map[string]any)["env"].(map[string]any)["VALUE"] = "one"
	bv.(map[string]any)["commands"] = []any{"second", "first"}
	if reflect.DeepEqual(a.Value(av), b.Value(bv)) {
		t.Fatal("command order hidden")
	}
}
func TestCompareUnavailableNeverPasses(t *testing.T) {
	a := failedObservations(os.ErrNotExist)
	result := compare("fixture", a, a)
	if result.Passed != 0 || result.Total != len(dimensions) {
		t.Fatalf("failed observations scored: %+v", result)
	}
	if compare("empty", map[string]Observation{}, map[string]Observation{}).Passed != 0 {
		t.Fatal("missing observations scored")
	}
	a["env"] = Observation{Value: map[string]any{"VALUE": "one"}}
	if compare("equal", a, a).Passed != 1 {
		t.Fatal("real equality did not score")
	}
}
func TestUpstreamVersionUsesSubmodule(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "reference/cli")
	os.MkdirAll(path, 0755)
	os.WriteFile(filepath.Join(path, "package.json"), []byte(`{"version":"0.86.0"}`), 0644)
	got, err := upstreamVersion(root)
	if err != nil || got != "0.86.0" {
		t.Fatalf("version = %q, %v", got, err)
	}
	os.WriteFile(filepath.Join(path, "package.json"), []byte(`{"version":"0.87.0"}`), 0644)
	got, err = upstreamVersion(root)
	if err != nil || got != "0.87.0" {
		t.Fatalf("version did not follow pin: %q, %v", got, err)
	}
}
func TestRealDockerConfigIsolation(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI absent")
	}
	t.Setenv("DOCKER_CONTEXT", "")
	host := t.TempDir()
	os.WriteFile(filepath.Join(host, "config.json"), []byte(`{"currentContext":"parity-untrusted","credsStore":"untrusted-helper"}`), 0600)
	t.Setenv("DOCKER_CONFIG", host)
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"example.invalid":{"auth":"sample"}}}`)
	before, err := exec.Command("docker", "context", "show").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(before)) != "parity-untrusted" {
		t.Fatalf("adversarial config inactive: %s", before)
	}
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, ".tmp"), 0755)
	log, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	r, err := newRunner(root, log)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(r.temp)
	after, err := r.command(context.Background(), "docker", "context", "show")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(after)) != "default" {
		t.Fatalf("host config leaked: %s", after)
	}
	for _, v := range r.env {
		if strings.HasPrefix(v, "DOCKER_AUTH_CONFIG=") {
			t.Fatal("auth override leaked")
		}
	}
	config, err := os.ReadFile(filepath.Join(r.temp, "docker/config.json"))
	if err != nil || strings.TrimSpace(string(config)) != "{}" {
		t.Fatalf("isolated config: %s %v", config, err)
	}
}
func TestMissingToolsSkipWithReport(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	root := t.TempDir()
	output := filepath.Join(root, "report")
	if err := run(context.Background(), root, output); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(output, "score.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r Report
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Skipped, "node") || r.Total != 0 {
		t.Fatalf("skip report: %+v", r)
	}
}

func TestResultIdentityAndTimestampBoundary(t *testing.T) {
	n := Normalizer{}
	a := map[string]any{"containerId": "aaa", "timestamp": "2026-01-01T00:00:00Z", "outcome": "success"}
	b := map[string]any{"containerId": "bbb", "timestamp": "2026-02-01T00:00:00Z", "outcome": "success"}
	if !reflect.DeepEqual(n.Result(a), n.Result(b)) {
		t.Fatal("result identities/timestamps differ")
	}
	aenv := map[string]any{"timestamp": "one"}
	benv := map[string]any{"timestamp": "two"}
	if reflect.DeepEqual(n.Value(aenv), n.Value(benv)) {
		t.Fatal("env timestamp value hidden")
	}
}

func TestUpstreamInvocationBindsVersion(t *testing.T) {
	bin := t.TempDir()
	path := filepath.Join(bin, "npx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	log, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	r := &Runner{root: t.TempDir(), runID: "sample", env: os.Environ(), log: log}
	body, err := (UpstreamAdapter{Version: "0.87.0"}).Up(context.Background(), r, "/workspace", "tag")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "--yes\n@devcontainers/cli@0.87.0\nup\n") {
		t.Fatalf("unbound invocation: %s", body)
	}
}

func TestCleanupRequiresExactOwnership(t *testing.T) {
	for _, ownership := range []string{"sample", "other", "wrong-id"} {
		t.Run(ownership, func(t *testing.T) {
			bin := t.TempDir()
			calls := filepath.Join(bin, "calls")
			script := `#!/bin/sh
printf '%s\n' "$*" >> "$CALLS"
case "$*" in
 'ps '*) echo full-container-id ;;
 'container inspect '*) if [ "$OWNER" = wrong-id ]; then echo "different-id sample"; else echo "full-container-id $OWNER"; fi ;;
 'image ls '*) echo sha256:full-image-id ;;
 'image inspect '*) if [ "$OWNER" = wrong-id ]; then echo "sha256:different-id sample"; else echo "sha256:full-image-id $OWNER"; fi ;;
esac
`
			os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755)
			t.Setenv("PATH", bin)
			t.Setenv("CALLS", calls)
			t.Setenv("OWNER", ownership)
			log, err := os.CreateTemp(t.TempDir(), "log")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			r := &Runner{root: t.TempDir(), runID: "sample", env: os.Environ(), log: log}
			errors := r.cleanup(context.Background())
			body, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			removed := strings.Contains(string(body), "rm -f")
			if ownership == "sample" {
				if len(errors) != 0 || !removed {
					t.Fatalf("owned cleanup: %v %s", errors, body)
				}
			} else {
				if len(errors) != 2 || removed {
					t.Fatalf("foreign resource deleted: %v %s", errors, body)
				}
			}
		})
	}
}

func TestPreparePreservesFixtureAndAddsOwnership(t *testing.T) {
	root := t.TempDir()
	f := Fixture{Name: "sample", Path: "src/test/configs/sample", Config: ".devcontainer.json"}
	source := filepath.Join(root, "reference/cli", f.Path)
	os.MkdirAll(source, 0755)
	original := []byte(`{"build":{"dockerfile":"Dockerfile","options":["--pull"]},"runArgs":["-e","VALUE=one"]}`)
	os.WriteFile(filepath.Join(source, f.Config), original, 0644)
	os.WriteFile(filepath.Join(source, "Dockerfile"), []byte("FROM alpine\n"), 0644)
	temp := t.TempDir()
	r := &Runner{root: root, temp: temp, runID: "sample"}
	workspace, err := r.prepare(f, "upstream")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(workspace, f.Config))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal(err)
	}
	args := config["runArgs"].([]any)
	options := config["build"].(map[string]any)["options"].([]any)
	if !reflect.DeepEqual(args, []any{"-e", "VALUE=one", "--label", r.label()}) {
		t.Fatalf("runtime ownership: %v", args)
	}
	if !reflect.DeepEqual(options, []any{"--pull", "--label", r.label()}) {
		t.Fatalf("build ownership: %v", options)
	}
	unchanged, _ := os.ReadFile(filepath.Join(source, f.Config))
	if !bytes.Equal(original, unchanged) {
		t.Fatal("upstream fixture changed")
	}
}

func TestPrerequisiteSkips(t *testing.T) {
	for _, missing := range []string{"node", "npx", "docker", "submodule"} {
		t.Run(missing, func(t *testing.T) {
			bin := t.TempDir()
			for _, name := range []string{"node", "npx", "docker"} {
				if name != missing {
					os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0755)
				}
			}
			t.Setenv("PATH", bin)
			_, reason := prerequisites(t.TempDir())
			expected := missing
			if missing == "submodule" {
				expected = "submodule update"
			}
			if !strings.Contains(reason, expected) {
				t.Fatalf("skip reason %q lacks %q", reason, expected)
			}
		})
	}
}
