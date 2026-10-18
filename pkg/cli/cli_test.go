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
		help, logs := invokeHelp(t, args...)
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
	for _, text := range []string{"name: dcb", "description:", "--workspace-folder", "--log-format", "stderr", "run-user-commands", "dcb exec", "Help is plain text on stdout", "Invocations except help and exec", "Capture\nstdout", "phasesRun", "type: diagnostic", "type: output"} {
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
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil || entry["text"] != want || entry["type"] != "output" || entry["level"] != "" {
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

func invokeHelp(t *testing.T, args ...string) (string, string) {
	t.Helper()
	var out, logs bytes.Buffer
	code := Run(context.Background(), args, "test", nil, &out, &logs)
	if code != 0 || json.Valid(out.Bytes()) || strings.Contains(out.String(), `"outcome":`) || !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("help: code=%d stdout=%q stderr=%q", code, out.String(), logs.String())
	}
	return out.String(), logs.String()
}

func TestCommandHelpPlainText(t *testing.T) {
	for _, command := range []string{"up", "build", "exec", "generate", "read-configuration", "run-user-commands", "set-up", "inspect", "down", "features", "server", "skills", "version"} {
		t.Run(command, func(t *testing.T) {
			help, logs := invokeHelp(t, command, "--help")
			if !strings.Contains(help, "dcb "+command) || logs != "" {
				t.Fatalf("help=%q logs=%q", help, logs)
			}
		})
	}
}

func TestExecOperationError(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	for _, format := range []string{"text", "json"} {
		for _, level := range []string{"default", "off"} {
			t.Run(format+"/"+level, func(t *testing.T) {
				var out, logs bytes.Buffer
				missing := filepath.Join(t.TempDir(), "missing")
				args := []string{"exec", "echo", "--config", missing, "--log-format", format}
				if level != "default" {
					args = append(args, "--log-level", level)
				}
				code := Run(context.Background(), args, "test", nil, &out, &logs)
				if code != 1 || out.Len() != 0 || !strings.Contains(logs.String(), missing) {
					t.Fatalf("code=%d out=%q logs=%q", code, out.String(), logs.String())
				}
				if format == "text" {
					if !strings.HasPrefix(logs.String(), "dcb: error: ") {
						t.Fatalf("unmarked error diagnostic=%q", logs.String())
					}
				} else {
					var record map[string]string
					if err := json.Unmarshal(logs.Bytes(), &record); err != nil || record["type"] != "diagnostic" || record["level"] != "error" {
						t.Fatalf("error diagnostic=%q (%v)", logs.String(), err)
					}
					if !strings.Contains(record["text"], missing) || strings.Contains(record["text"], "dcb: error: ") {
						t.Fatalf("error message=%q", record["text"])
					}
				}
			})
		}
	}
}

func TestDiagnosticLevels(t *testing.T) {
	for _, level := range []string{"trace", "debug", "info", "warn", "error", "off"} {
		var logs bytes.Buffer
		a := &application{logLevel: level, logs: &logWriter{dst: &logs, json: true}}
		a.diagnostic("trace", "trace detail")
		a.diagnostic("debug", "debug detail")
		a.logf("info detail")
		a.logf("%s", "warning: detail")
		a.diagnostic("error", "error detail")
		ranks := []string{"trace", "debug", "info", "warn", "error", "off"}
		start := 0
		for ranks[start] != level {
			start++
		}
		var got []string
		dec := json.NewDecoder(&logs)
		for dec.More() {
			var record map[string]string
			if err := dec.Decode(&record); err != nil {
				t.Fatal(err)
			}
			if record["type"] != "diagnostic" {
				t.Fatalf("record=%v", record)
			}
			got = append(got, record["level"])
		}
		if strings.Join(got, ",") != strings.Join(ranks[start:5], ",") {
			t.Fatalf("%s: levels=%v", level, got)
		}
	}
}

func TestCommandOutputIgnoresDiagnosticLevel(t *testing.T) {
	for _, format := range []bool{false, true} {
		var logs bytes.Buffer
		a := &application{logLevel: "off", logs: &logWriter{dst: &logs, json: format}}
		a.logf("hidden")
		_, _ = a.logs.Write([]byte("command output"))
		_ = a.logs.Flush()
		if !strings.Contains(logs.String(), "command output") || strings.Contains(logs.String(), "hidden") {
			t.Fatalf("logs=%q", logs.String())
		}
	}
}

func TestVerboseDiagnostics(t *testing.T) {
	for _, level := range []string{"info", "debug", "trace"} {
		_, logs := invoke(t, 0, "version", "--log-level", level)
		if strings.Contains(logs, "running dcb version") != (level != "info") {
			t.Fatalf("%s start diagnostics=%q", level, logs)
		}
		if strings.Contains(logs, "arguments:") != (level == "trace") {
			t.Fatalf("%s trace diagnostics=%q", level, logs)
		}
	}
}
