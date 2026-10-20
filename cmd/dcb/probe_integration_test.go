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
	"time"
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
			started := time.Now()
			if err := command.Run(); err != nil {
				t.Fatalf("timeout exec: %v %s", err, stderr.String())
			}
			if time.Since(started) < 10*time.Second {
				t.Fatal("probe timeout shorter than upstream 10 s")
			}
			if strings.TrimSpace(stdout.String()) != "continued" || !strings.Contains(stderr.String(), "userEnvProbe is taking longer than 10 seconds. Avoid waiting for user input in your shell's startup scripts. Continuing.") {
				t.Fatalf("timeout result: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			t.Logf("timeout warning: %s", stderr.String())
			hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--default-user-env-probe", "none", "--", "rm", "/etc/profile.d/sample-delay.sh")

		})
	}
}

// Debian's non-root login PATH is the regression that distinguishes the system
// patches from the mergePaths fallback. Exercise setup and skip gating as well.
func TestUserEnvironmentSystemPatches(t *testing.T) {
	ctx := context.Background()
	cli := hostRequireDocker(t, ctx)
	defer cli.Close()
	hostEnsureImage(t, ctx, cli, "node:24-bookworm-slim")
	bin := hostBuildBinary(t)
	const wantPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			f := newEnvironmentFixture(t, bin, mode == "compose")
			configPath := filepath.Join(f.workspace, ".devcontainer", "devcontainer.json")
			config := map[string]any{"remoteUser": "node", "updateRemoteUserUID": false, "containerEnv": map[string]string{"SAMPLE_SYSTEM": "container-value"}, "postAttachCommand": `test "$PATH" = '` + wantPath + `'`}
			if mode == "image" {
				config["image"] = "node:24-bookworm-slim"
			} else {
				config["dockerComposeFile"], config["service"], config["workspaceFolder"] = "compose.yml", "app", "/workspace"
				if err := os.WriteFile(filepath.Join(f.workspace, ".devcontainer", "compose.yml"), []byte("services:\n  app:\n    image: node:24-bookworm-slim\n    command: [sleep, infinity]\n    volumes: [\"..:/workspace\"]\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, body, 0600); err != nil {
				t.Fatal(err)
			}
			systemFolder := "/var/devcontainer"
			if mode == "compose" {
				systemFolder = "/tmp/sample-up-system"
			}
			result, _ := f.run(t, "skip-up", "up", "--workspace-folder", f.workspace, "--skip-post-create", "--container-system-data-folder", systemFolder)
			id := result["containerId"].(string)
			root := func(script string) string {
				t.Helper()
				return string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--default-user-env-probe", "none", "--user", "root", "--", "/bin/sh", "-c", script))
			}
			check := `test -f ` + systemFolder + `/.patchEtcEnvironmentMarker && test -f ` + systemFolder + `/.patchEtcProfileMarker && test "$(grep -c '^SAMPLE_SYSTEM="container-value"$' /etc/environment)" = 1 && grep -q 'PATH=${PATH:-' /etc/profile`
			root(check + ` && ! grep -q '^HOSTNAME=' /etc/environment`)
			path := string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--", "/bin/sh", "-c", `printf %s "$PATH"`))
			if path != wantPath {
				t.Fatalf("non-root PATH=%q want upstream %q", path, wantPath)
			}
			// Install a profile side effect. A skipped lifecycle must never source it.
			root(`printf 'echo probed >> /tmp/sample-probe-calls\n' > /etc/profile.d/sample-probe.sh; rm -f /tmp/sample-probe-calls`)
			for _, command := range []string{"up", "set-up", "run-user-commands"} {
				args := []string{command, "--workspace-folder", f.workspace, "--skip-post-create", "--container-system-data-folder", systemFolder}
				if command == "set-up" {
					args = append(args, "--container-id", id)
				}
				_, stderr := f.run(t, "skip-"+command, args...)
				if strings.Contains(stderr, "userEnvProbe") {
					t.Fatalf("skipped probe logged: %s", stderr)
				}
				root(`test ! -f /tmp/sample-probe-calls`)
			}
			root(check)
			// set-up also patches an existing container when lifecycle is skipped, and
			// honors a custom system folder. Restore a literal PATH to prove the edit.
			root(`rm -f /etc/profile.d/sample-probe.sh; printf '\nPATH=/sample/bin\n' >> /etc/profile`)
			f.run(t, "custom-setup", "set-up", "--workspace-folder", f.workspace, "--container-id", id, "--skip-post-create", "--container-system-data-folder", "/tmp/sample-system")
			root(`test -f /tmp/sample-system/.patchEtcEnvironmentMarker && test -f /tmp/sample-system/.patchEtcProfileMarker && grep -Fq 'PATH=${PATH:-/sample/bin}' /etc/profile`)
			// sed's failure is intentionally ignored after its marker is written.
			root(`printf '#!/bin/sh\nexit 23\n' > /usr/local/bin/sed; chmod +x /usr/local/bin/sed`)
			f.run(t, "sed-failure", "set-up", "--workspace-folder", f.workspace, "--container-id", id, "--skip-post-create", "--container-system-data-folder", "/tmp/sample-sed-failure")
			root(`test -f /tmp/sample-sed-failure/.patchEtcProfileMarker`)
			root(`rm -f /usr/local/bin/sed`)
			// A marker path that cannot be created skips both edits without failing.
			f.run(t, "marker-failure", "set-up", "--workspace-folder", f.workspace, "--container-id", id, "--skip-post-create", "--container-system-data-folder", "/dev/null/sample")
		})
	}
}
