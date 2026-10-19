//go:build docker_integration

// relationships:
//
//	references: [environment_integration_test, ../../pkg/host/runner/probe]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Alpine's /etc/profile supplies these variables, the measured upstream gap.
func TestUserEnvironmentProbe(t *testing.T) {
	ctx := context.Background()
	cli := hostRequireDocker(t, ctx)
	defer cli.Close()
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	want := "less|C|C.UTF-8|UTF-8"
	script := `printf '%s|%s|%s|%s' "$PAGER" "$LC_COLLATE" "$LANG" "$CHARSET"`
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			f := newEnvironmentFixture(t, bin, mode == "compose")
			configPath := filepath.Join(f.workspace, ".devcontainer", "devcontainer.json")
			data, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			var config map[string]any
			if err := json.Unmarshal(data, &config); err != nil {
				t.Fatal(err)
			}
			delete(config, "features")
			delete(config, "remoteEnv")
			config["postAttachCommand"] = script + " > /tmp/sample-profile"
			save := func() {
				t.Helper()
				body, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(configPath, body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			save()
			f.run(t, "up", "up", "--workspace-folder", f.workspace)
			assertHook := func() {
				t.Helper()
				got := strings.TrimSpace(string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--default-user-env-probe", "none", "--", "cat", "/tmp/sample-profile")))
				if got != want {
					t.Fatalf("profile env = %q want %q", got, want)
				}
			}
			assertHook()
			for _, command := range []string{"run-user-commands", "set-up"} {
				args := []string{command, "--workspace-folder", f.workspace}
				if command == "set-up" {
					result, _ := f.run(t, "inspect", "inspect", "--workspace-folder", f.workspace)
					args = append(args, "--container-id", result["containerId"].(string))
				}
				f.run(t, command, args...)
				assertHook()
			}
			for _, probe := range []string{"loginInteractiveShell", "loginShell", "interactiveShell", "none"} {
				got := string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--default-user-env-probe", probe, "--", "/bin/sh", "-c", script))
				if strings.HasPrefix(probe, "login") {
					if got != want {
						t.Fatalf("%s profile=%q", probe, got)
					}
				} else if got == want {
					t.Fatalf("%s unexpectedly read profile", probe)
				}
			}
			// Config wins over the command default; remoteEnv wins over the probe.
			config["userEnvProbe"] = "loginShell"
			config["remoteEnv"] = map[string]string{"PAGER": "configured"}
			save()
			got := string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--default-user-env-probe", "none", "--remote-env", "PAGER=caller", "--", "/bin/sh", "-c", script))
			if got != "configured|C|C.UTF-8|UTF-8" {
				t.Fatalf("config/remote precedence=%q", got)
			}
			// A real slow startup must continue without using late profile values.
			hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--default-user-env-probe", "none", "--", "/bin/sh", "-c", "printf 'sleep 12; export SAMPLE_PROBE=late\n' > /etc/profile.d/sample-delay.sh")
			// Config chooses loginShell, so --none cannot bypass it until the config is cleared.
			delete(config, "userEnvProbe")
			delete(config, "remoteEnv")
			save()
			command := exec.Command(bin, "exec", "--workspace-folder", f.workspace, "--default-user-env-probe", "loginShell", "--", "/bin/sh", "-c", `test -z "$SAMPLE_PROBE" && echo continued`)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("timeout exec: %v %s", err, stderr.String())
			}
			if strings.TrimSpace(stdout.String()) != "continued" || !strings.Contains(stderr.String(), "userEnvProbe is taking longer than 10 seconds. Avoid waiting for user input in your shell's startup scripts. Continuing.") {
				t.Fatalf("timeout result: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			t.Logf("timeout warning: %s", stderr.String())
			hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--default-user-env-probe", "none", "--", "rm", "/etc/profile.d/sample-delay.sh")

		})
	}
}
