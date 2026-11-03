//go:build docker_integration

// relationships:
//
//	references: [docker_build_integration_test]
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerBuildSubstitutionIntegration(t *testing.T) {
	requireDocker(t)
	bin := buildBinary(t)
	dir := t.TempDir()
	dest := filepath.Join(t.TempDir(), "out")
	for name, content := range map[string]string{
		"devcontainer.json": `{"build":{"dockerfile":"${localEnv:SAMPLE_FILE:Dockerfile}","context":"${localWorkspaceFolder}","args":{"VALUE":"${localEnv:SAMPLE_BUILD_VALUE}","FALLBACK":"${localEnv:SAMPLE_ABSENT:fallback}","WORKSPACE":"${localWorkspaceFolder}"}}}`,
		"Dockerfile":        "FROM alpine\nARG VALUE\nARG FALLBACK\nARG WORKSPACE\nRUN printf '%s\\n%s\\n%s\\n' \"$VALUE\" \"$FALLBACK\" \"$WORKSPACE\" > /sample\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("SAMPLE_BUILD_VALUE", "selected")

	// Supply an explicitly absent variable independently of the host environment.
	cmd := exec.Command(bin, "build", "--workspace-folder", dir, "--config", filepath.Join(dir, "devcontainer.json"), "--output", "type=local,dest="+dest)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "SAMPLE_ABSENT=") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dest, "sample"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "selected\nfallback\n"+dir+"\n" {
		t.Fatalf("Dockerfile ARG output = %q", got)
	}
}
