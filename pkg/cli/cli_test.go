// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func invoke(t *testing.T, wantCode int, args ...string) (map[string]any, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, "test-version", nil, &stdout, &stderr)
	if code != wantCode {
		t.Fatalf("%v: exit %d, want %d; stdout=%s stderr=%s", args, code, wantCode, stdout.String(), stderr.String())
	}
	if bytes.Count(stdout.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("stdout must contain exactly one result line: %q", stdout.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid result %q: %v", stdout.String(), err)
	}
	wantOutcome := "success"
	if wantCode != 0 {
		wantOutcome = "error"
	}
	if result["outcome"] != wantOutcome {
		t.Fatalf("result=%v, want outcome %s", result, wantOutcome)
	}
	if wantCode != 0 {
		for _, key := range []string{"message", "description"} {
			if text, ok := result[key].(string); !ok || text == "" {
				t.Errorf("missing %s: %v", key, result)
			}
		}
	}
	return result, stderr.String()
}

func TestHelpContract(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"help"}, {}} {
		result, logs := invoke(t, 0, args...)
		help := result["help"].(string)
		for _, command := range []string{"up", "build", "exec", "read-configuration", "run-user-commands", "set-up", "down", "inspect", "generate", "features", "server", "version", "skills"} {
			if !strings.Contains(help, "  "+command+" ") {
				t.Errorf("help does not describe %s:\n%s", command, help)
			}
		}
		for _, text := range []string{"--workspace-folder", "--config", "--log-format", "--log-level", "dcb up", "dcb exec", "Exit codes:"} {
			if !strings.Contains(help, text) {
				t.Errorf("help missing %q", text)
			}
		}
		if logs != "" {
			t.Errorf("help logs=%q", logs)
		}
	}
}

func TestErrorResults(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	missing := filepath.Join(t.TempDir(), "missing")
	cases := []struct {
		name string
		code int
		args []string
	}{
		{"up", 1, []string{"up", "--workspace-folder", missing}},
		{"build", 1, []string{"build", "--workspace-folder", missing}},
		{"exec", 1, []string{"exec", "echo", "--config", missing}},
		{"read", 1, []string{"read-configuration", "--workspace-folder", missing}},
		{"unknown-command", 2, []string{"unknown-command"}},
		{"legacy-flag", 2, []string{"build", "--workspace", missing}},
		{"unknown-flag", 2, []string{"up", "--not-a-flag"}},
		{"exec-needs-command", 2, []string{"exec"}},
		{"extra-positional", 2, []string{"version", "extra"}},
		{"log-format", 2, []string{"version", "--log-format", "xml"}},
		{"log-level", 2, []string{"version", "--log-level", ""}},
		{"combined-log-levels", 2, []string{"version", "--log-level", "info|warn"}},
		{"skill-name", 2, []string{"skills", "missing"}},
		{"skill-dir", 2, []string{"skills", "install", "--dir="}},
		{"exec-container", 2, []string{"exec", "--container-id=", "echo"}},
		{"set-up-missing-container", 2, []string{"set-up"}},
		{"features-missing-out", 2, []string{"features"}},
		{"set-up-container", 2, []string{"set-up", "--container-id="}},
		{"features-out", 2, []string{"features", "--out="}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { invoke(t, tc.code, tc.args...) })
	}
	result, _ := invoke(t, 1, "exec", "echo", "--config", missing)
	if !strings.Contains(result["message"].(string), missing) {
		t.Fatalf("flag after positional did not reach config loader: %v", result)
	}
}

func TestReadConfiguration(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("EXAMPLE_VALUE", `value "with quotes"`)
	config := filepath.Join(workspace, "devcontainer.json")
	body := `{// JSONC is supported
 "image":"alpine:3.20",
 "workspaceFolder":"/workspaces/${localWorkspaceFolderBasename}",
 "customField":{"values":["${localEnv:EXAMPLE_VALUE}",true,42]},
 "postCreateCommand":"echo ready"
 }`
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	result, logs := invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--config", config)
	dc := result["configuration"].(map[string]any)
	if dc["image"] != "alpine:3.20" || dc["configFilePath"] != config || dc["postCreateCommand"] != "echo ready" {
		t.Fatalf("configuration=%v", dc)
	}
	values := dc["customField"].(map[string]any)["values"].([]any)
	if values[0] != `value "with quotes"` || values[1] != true || values[2] != float64(42) {
		t.Fatalf("unknown config fields did not survive: %v", values)
	}
	if result["workspace"].(map[string]any)["workspaceFolder"] != "/workspaces/"+filepath.Base(workspace) {
		t.Fatalf("workspace=%v", result["workspace"])
	}
	if logs != "" {
		t.Errorf("unexpected logs=%q", logs)
	}
}

func TestBundledSkills(t *testing.T) {
	result, _ := invoke(t, 0, "skills")
	skill := result["skills"].([]any)[0].(map[string]any)
	content := skill["content"].(string)
	for _, text := range []string{"name: dcb", "description:", "--workspace-folder", "--log-format", "stderr", "run-user-commands", "dcb exec"} {
		if !strings.Contains(content, text) {
			t.Errorf("skill missing %q", text)
		}
	}
	dir := t.TempDir()
	result, _ = invoke(t, 0, "skills", "install", "--dir", dir)
	path := filepath.Join(dir, "dcb", "SKILL.md")
	installed, err := os.ReadFile(path)
	if err != nil || string(installed) != content {
		t.Fatalf("installed skill differs: %v", err)
	}
	if result["installed"].([]any)[0] != path {
		t.Fatalf("installed=%v", result)
	}
	result, _ = invoke(t, 0, "skills", "dcb", "--list")
	skill = result["skills"].([]any)[0].(map[string]any)
	if skill["name"] != "dcb" || skill["content"] != nil {
		t.Fatalf("list=%v", result)
	}
}

func TestVersion(t *testing.T) {
	result, logs := invoke(t, 0, "version")
	if result["version"] != "test-version" || logs != "" {
		t.Fatalf("result=%v logs=%q", result, logs)
	}
}

func TestJSONDiagnosticStream(t *testing.T) {
	var logs bytes.Buffer
	writer := &logWriter{dst: &logs, json: true}
	for _, chunk := range []string{"first\nsec", "ond\nunterminated"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(logs.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("logs=%q", logs.String())
	}
	for i, want := range []string{"first", "second", "unterminated"} {
		var entry map[string]string
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil || entry["text"] != want || entry["level"] != "info" {
			t.Fatalf("log line %q: %v", lines[i], err)
		}
	}
}

func TestGenerateAndJSONLogs(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(`{"image":"alpine:3.20"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, logs := invoke(t, 0, "generate", "--workspace-folder", workspace, "--work-dir", t.TempDir(), "--log-format", "json")
	if _, err := os.Stat(result["dockerfile"].(string)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result["contextDir"].(string)); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("non-JSON diagnostic %q: %v", line, err)
		}
	}
	_, quiet := invoke(t, 0, "generate", "--workspace-folder", workspace, "--work-dir", t.TempDir(), "--log-level", "off")
	if quiet != "" {
		t.Fatalf("log-level off: %q", quiet)
	}
}
