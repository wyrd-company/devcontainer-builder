package dockerfile

import (
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

func TestGenerate_BareImage(t *testing.T) {
	out, err := Generate(Inputs{
		Spec: &spec.DevContainer{
			Image:           "ubuntu:22.04",
			RemoteUser:      "vscode",
			WorkspaceFolder: "/workspaces/demo",
		},
		BaseImage: "ubuntu:22.04",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"FROM ubuntu:22.04",
		"WORKDIR /workspaces/demo",
		"USER vscode",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestGenerate_DoesNotBakeRemoteEnv(t *testing.T) {
	out, err := Generate(Inputs{
		Spec: &spec.DevContainer{
			Image:           "alpine",
			ContainerEnv:    map[string]any{"CONTAINER_ONLY": "yes"},
			UpdateRemoteEnv: map[string]any{"REMOTE_ONLY": "secret"},
		},
		BaseImage: "alpine",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "ENV CONTAINER_ONLY=yes") {
		t.Fatalf("containerEnv missing from Dockerfile:\n%s", got)
	}
	if strings.Contains(got, "REMOTE_ONLY") {
		t.Fatalf("remoteEnv should not be emitted into Dockerfile:\n%s", got)
	}
}

func TestGenerate_VSCodeServer_AsRoot(t *testing.T) {
	out, err := Generate(Inputs{
		Spec:                    &spec.DevContainer{Image: "alpine"},
		BaseImage:               "alpine",
		VSCodeServer:            &vscode.Result{Commit: "abc123", Quality: vscode.QualityStable, URL: "u", SHA256: "deadbeef", Bytes: 1},
		VSCodeServerContextPath: "_vscode/server.tar.gz",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "/root/.vscode-server/bin/abc123") {
		t.Errorf("expected canonical install dir for root, got:\n%s", got)
	}
}

func TestGenerate_VSCodeServer_AsUser_ChownsHome(t *testing.T) {
	out, err := Generate(Inputs{
		Spec:                    &spec.DevContainer{Image: "alpine", RemoteUser: "vscode"},
		BaseImage:               "alpine",
		VSCodeServer:            &vscode.Result{Commit: "abc123", Quality: vscode.QualityStable, URL: "u", SHA256: "deadbeef", Bytes: 1},
		VSCodeServerContextPath: "_vscode/server.tar.gz",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "/home/vscode/.vscode-server/bin/abc123") {
		t.Errorf("expected vscode user install dir, got:\n%s", got)
	}
	if !strings.Contains(got, "chown -R") {
		t.Errorf("expected chown step, got:\n%s", got)
	}
}

func TestGenerate_FeatureInstallScript(t *testing.T) {
	feat := features.Resolved{
		Source: "ghcr.io/devcontainers/features/node:1",
		SafeID: "abc-node",
		Options: map[string]any{
			"version": "20",
		},
		Metadata: features.Metadata{
			ID:      "node",
			Version: "1.0.0",
			Options: map[string]features.Option{
				"version": {Type: "string", Default: "lts"},
			},
		},
	}
	out, err := Generate(Inputs{
		Spec:               &spec.DevContainer{Image: "alpine"},
		BaseImage:          "alpine",
		Features:           []features.Resolved{feat},
		FeaturesContextDir: "_features",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, want := range []string{
		"COPY _features/abc-node /tmp/build-features/abc-node",
		"VERSION=20",
		"./install.sh",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
