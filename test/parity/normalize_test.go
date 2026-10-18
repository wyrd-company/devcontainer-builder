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
func TestCompareRealEnvDifference(t *testing.T) {
	a := map[string]Observation{"env": {Value: map[string]any{"VALUE": "one"}}}
	b := map[string]Observation{"env": {Value: map[string]any{"VALUE": "two"}}}
	if compare("different", a, b).Dimensions["env"].Pass {
		t.Fatal("genuine environment difference scored equal")
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
	if !strings.Contains(r.Skipped, "node") || r.Total != 0 || r.Adapter != (DCBAdapter{}).Name() {
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

func TestFixtureVerdict(t *testing.T) {
	all := map[string]Observation{}
	for _, name := range dimensions {
		all[name] = Observation{Value: "equal"}
	}
	if result := compare("equal", all, all); !result.Pass || result.Passed != len(dimensions) {
		t.Fatalf("fixture verdict: %+v", result)
	}
	if result := compare("missing", all, map[string]Observation{}); result.Pass {
		t.Fatal("missing fixture passed")
	}
}

func TestUnorderedMounts(t *testing.T) {
	a := []any{map[string]any{"source": "/first", "rw": true}, map[string]any{"source": "/second", "rw": false}}
	b := []any{map[string]any{"source": "/second", "rw": false}, map[string]any{"source": "/first", "rw": true}}
	if !reflect.DeepEqual(unordered(a), unordered(b)) {
		t.Fatal("mount order should not matter")
	}
	b[0].(map[string]any)["rw"] = !b[0].(map[string]any)["rw"].(bool)
	if reflect.DeepEqual(unordered(a), unordered(b)) {
		t.Fatal("mount permissions differ")
	}
}

func TestPrepareGoPreservesNativeBuildOptions(t *testing.T) {
	root := t.TempDir()
	f := Fixture{Name: "sample", Path: "src/test/configs/sample", Config: ".devcontainer.json"}
	source := filepath.Join(root, "reference/cli", f.Path)
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, f.Config), []byte(`{"build":{"dockerfile":"Dockerfile"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{root: root, temp: t.TempDir(), runID: "sample"}
	workspace, err := r.prepare(f, "go")
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
	if _, exists := config["build"].(map[string]any)["options"]; exists {
		t.Fatal("harness injected unsupported Docker CLI build options into native build")
	}
}

func TestDCBAdapterNativeInvocation(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	script := filepath.Join(bin, "cli")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$PARITY_ADAPTER_CALLS\"\nprintf '{\"outcome\":\"success\",\"containerId\":\"example\"}\\n'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	log, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	r := &Runner{root: t.TempDir(), runID: "sample", env: append(os.Environ(), "PARITY_ADAPTER_CALLS="+calls), log: log}
	if _, err := (DCBAdapter{Binary: script}).Up(context.Background(), r, "/workspace", "sample:latest"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	args := string(body)
	if strings.Contains(args, "--engine\n") || strings.Contains(args, "--skip-vscode-server\n") || strings.Contains(args, "--vscode-server\n") {
		t.Fatal("obsolete flag or opt-in server flag sent to native CLI")
	}
	if !strings.Contains(args, "--label\n"+r.label()+"\n") {
		t.Fatal("native output image ownership label omitted")
	}
	if !strings.Contains(args, "up\n--workspace-folder\n/workspace\n--image-name\nsample:latest\n") {
		t.Fatalf("dcb did not request the labelled native image: %s", args)
	}
}

func TestDCBAdapterResultBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, body, exit string
		fail             bool
	}{
		{"success", "{\"outcome\":\"success\",\"containerId\":\"example\"}\n", "0", false},
		{"invalid-json", "not-json\n", "0", true},
		{"extra-stdout", "diagnostic\n{\"outcome\":\"success\"}\n", "0", true},
		{"blank-stdout-line", "{\"outcome\":\"success\"}\n\n", "0", true},
		{"missing-outcome", "{}\n", "0", true},
		{"error-outcome", "{\"outcome\":\"error\"}\n", "0", true},
		{"process-exit", "{\"outcome\":\"success\"}\n", "7", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "dcb")
			if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' \"$PARITY_RESULT\"\nexit \"$PARITY_EXIT\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			log, err := os.CreateTemp(t.TempDir(), "log")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			r := &Runner{root: dir, runID: "sample", env: append(os.Environ(), "PARITY_RESULT="+tc.body, "PARITY_EXIT="+tc.exit), log: log}
			body, err := (DCBAdapter{Binary: script}).Up(context.Background(), r, "/workspace", "sample:latest")
			if (err != nil) != tc.fail {
				t.Fatalf("adapter error=%v, want failure=%v", err, tc.fail)
			}
			if string(body) != tc.body {
				t.Fatalf("adapter changed real CLI output: %q", body)
			}
		})
	}
}

func TestBuildAdapterUsesOnlyDCB(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$PARITY_ADAPTER_CALLS\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	log, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	r := &Runner{root: t.TempDir(), temp: t.TempDir(), env: append(os.Environ(), "PARITY_ADAPTER_CALLS="+calls), log: log}
	adapter, err := buildAdapter(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := adapter.(DCBAdapter)
	if !ok || got.Binary != filepath.Join(r.temp, "dcb") {
		t.Fatalf("adapter=%+v", adapter)
	}
	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	expected := "build\n-o\n" + got.Binary + "\n./cmd/dcb\n"
	if string(body) != expected {
		t.Fatalf("binary build calls=%q, want %q", body, expected)
	}
}
