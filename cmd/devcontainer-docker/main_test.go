package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCLI_Version(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "version")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run . version: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "devcontainer-docker dev" {
		t.Fatalf("version output = %q, want devcontainer-docker version", out)
	}
}
