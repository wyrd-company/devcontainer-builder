// relationships:
//
//	references: [taskfile, ci-integration]
package compat_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Execute the actual CI wrapper with recording tools so that selection,
// environment isolation, failure propagation, and cleanup are observable.
func TestCIIntegrationRunner(t *testing.T) {
	script, err := filepath.Abs("../../scripts/ci-integration.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, suite := range []string{"all", "remote", "classic"} {
		for _, scenario := range []string{"success", "failure", "wrong-store"} {
			if scenario == "wrong-store" && suite != "classic" {
				continue
			}
			fail := scenario == "failure"
			name := suite + "/" + scenario
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				bin := filepath.Join(dir, "bin")
				if err := os.Mkdir(bin, 0o755); err != nil {
					t.Fatal(err)
				}
				write := func(name, body string) {
					t.Helper()
					if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				write("docker", `#!/bin/sh
printf '%s\n' "$*" >> "$RECORD_DIR/docker"
printf '%s\n' "${DOCKER_HOST-unset}" >> "$RECORD_DIR/docker-endpoints"
case "$1" in
run) echo fixture-container ;;
port) echo 127.0.0.1:1234 ;;
logs) echo 'running server'; echo 'API listen on [::]:2375' ;;
exec) echo "$TEST_DRIVER" ;;
esac
`)
				write("go", `#!/bin/sh
printf '%s\n' "$*" > "$RECORD_DIR/go"
printf '%s\n' "${BUILDKIT_HOST-unset}" > "$RECORD_DIR/host"
printf '%s\n' "$DOCKER_CONFIG" > "$RECORD_DIR/config-path"
printf '%s\n' "${DOCKER_HOST-unset}|${DOCKER_TLS_VERIFY-unset}|${DOCKER_CERT_PATH-unset}" > "$RECORD_DIR/engine-env"
cat "$DOCKER_CONFIG/config.json" > "$RECORD_DIR/config"
exit "$GO_EXIT"
`)
				originalConfig := filepath.Join(dir, "operator-config")
				if err := os.Mkdir(originalConfig, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(originalConfig, "config.json"), []byte(`{"credsStore":"fixture-helper"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				// Populate actual process overrides; the child inherits them before setup.
				t.Setenv("DOCKER_CONFIG", originalConfig)
				t.Setenv("BUILDKIT_HOST", "tcp://operator-buildkit:4321")
				t.Setenv("DOCKER_HOST", "tcp://parent-daemon:2375")
				t.Setenv("DOCKER_TLS_VERIFY", "1")
				t.Setenv("DOCKER_CERT_PATH", originalConfig)
				driver := "overlay2"
				if scenario == "wrong-store" {
					driver = "overlayfs"
				}
				t.Setenv("TEST_DRIVER", driver)
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("RECORD_DIR", dir)
				t.Setenv("TMPDIR", dir)
				code := "0"
				if fail {
					code = "23"
				}
				t.Setenv("GO_EXIT", code)
				cmd := exec.Command("bash", script, suite)
				cmd.Dir = dir
				out, runErr := cmd.CombinedOutput()
				if scenario == "wrong-store" {
					if exit, ok := runErr.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
						t.Fatalf("wrong store accepted: %v\n%s", runErr, out)
					}
				} else if fail {
					if exit, ok := runErr.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
						t.Fatalf("failure propagation: %v\n%s", runErr, out)
					}
				} else if runErr != nil {
					t.Fatalf("runner: %v\n%s", runErr, out)
				}
				read := func(name string) string {
					t.Helper()
					body, err := os.ReadFile(filepath.Join(dir, name))
					if err != nil {
						t.Fatal(err)
					}
					return strings.TrimSpace(string(body))
				}
				if scenario == "wrong-store" {
					if _, err := os.Stat(filepath.Join(dir, "go")); !os.IsNotExist(err) {
						t.Fatal("tests ran on wrong store")
					}
					if !strings.Contains(string(out), "want overlay2") {
						t.Fatalf("missing store diagnostic: %s", out)
					}
					if got := read("docker"); !strings.HasSuffix(got, "rm -fv fixture-container") {
						t.Fatalf("owned helper not cleaned: %q", got)
					}
					entries, err := os.ReadDir(dir)
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), "tmp.") {
							t.Fatalf("temporary config not cleaned: %s", entry.Name())
						}
					}
					return
				}
				if got := read("config"); got != "{}" {
					t.Fatalf("isolated config = %q", got)
				}
				configPath := read("config-path")
				if configPath == originalConfig {
					t.Fatal("operator config reused")
				}
				if _, err := os.Stat(configPath); !os.IsNotExist(err) {
					t.Fatalf("temporary config not cleaned: %v", err)
				}
				if _, err := os.Stat(filepath.Join(originalConfig, "config.json")); err != nil {
					t.Fatalf("operator config removed: %v", err)
				}
				if suite == "all" {
					if got := read("go"); got != "test -v -tags docker_integration ./... -count=1" {
						t.Fatalf("all-package invocation = %q", got)
					}
					if got := read("host"); got != "unset" {
						t.Fatalf("stock Docker inherited BUILDKIT_HOST = %q", got)
					}
					if _, err := os.Stat(filepath.Join(dir, "docker")); !os.IsNotExist(err) {
						t.Fatalf("stock Docker launched a helper: %v", err)
					}
				} else if suite == "classic" {
					if got := read("go"); got != "test -v -tags docker_integration ./pkg/buildkit ./pkg/builder -run TestDaemon.*Integration|TestEngineBuildIntegration -count=1" {
						t.Fatalf("classic invocation = %q", got)
					}
					if got := read("host"); got != "unset" {
						t.Fatalf("classic inherited BUILDKIT_HOST = %q", got)
					}
					if got := read("engine-env"); got != "tcp://127.0.0.1:1234|unset|unset" {
						t.Fatalf("classic engine environment = %q", got)
					}
					commands := read("docker")
					if !strings.Contains(commands, "docker:dind --storage-driver=overlay2 --feature containerd-snapshotter=false") {
						t.Fatalf("classic store not selected: %q", commands)
					}
					if !strings.HasSuffix(commands, "rm -fv fixture-container") {
						t.Fatalf("owned helper not cleaned: %q", commands)
					}
					for _, endpoint := range strings.Split(read("docker-endpoints"), "\n") {
						if endpoint != "tcp://parent-daemon:2375" {
							t.Fatalf("container management changed endpoints: %q", endpoint)
						}
					}
				} else {
					if got := read("go"); got != "test -v -tags docker_integration ./pkg/builder -run TestEngineBuildIntegration -count=1" {
						t.Fatalf("remote invocation = %q", got)
					}
					if got := read("host"); got != "tcp://127.0.0.1:1234" {
						t.Fatalf("remote host = %q", got)
					}
					if got := read("docker"); !strings.HasSuffix(got, "rm -fv fixture-container") {
						t.Fatalf("owned helper not cleaned: %q", got)
					}
				}
			})
		}
	}
}
