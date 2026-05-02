package buildkit

import (
	"context"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

// marshalGraph runs the LLB marshaller and returns a flattened text
// rendering of every Op in the definition. Useful to assert that the
// expected operations are present without depending on the exact
// digest layout.
func marshalGraph(t *testing.T, in GraphInputs) string {
	t.Helper()
	state, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	def, err := state.Marshal(context.Background())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var b strings.Builder
	for _, dt := range def.Def {
		b.Write(dt)
		b.WriteString("\n")
	}
	return b.String()
}

func TestBuild_BareImage(t *testing.T) {
	got := marshalGraph(t, GraphInputs{
		Spec:               &spec.DevContainer{Image: "alpine:3.20"},
		BaseImage:          "alpine:3.20",
		ResolveImageConfig: false,
	})
	if !strings.Contains(got, "alpine:3.20") {
		t.Errorf("expected alpine reference in graph, got:\n%s", got)
	}
}

func TestBuild_FeatureRunsAsRoot(t *testing.T) {
	got := marshalGraph(t, GraphInputs{
		Spec:                  &spec.DevContainer{Image: "alpine"},
		BaseImage:             "alpine",
		LocalContextName:      "context",
		FeaturesContextSubdir: "_features",
		Features: []features.Resolved{{
			Source: "ghcr.io/devcontainers/features/node:1",
			SafeID: "abc-node",
			Metadata: features.Metadata{
				ID:      "node",
				Version: "1.0.0",
			},
		}},
		ResolveImageConfig: false,
	})
	for _, want := range []string{
		"/tmp/build-features/abc-node",
		"install.sh",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in graph", want)
		}
	}
}

func TestBuild_VSCodeServer(t *testing.T) {
	got := marshalGraph(t, GraphInputs{
		Spec:               &spec.DevContainer{Image: "alpine", RemoteUser: "vscode"},
		BaseImage:          "alpine",
		VSCodeServer:       &vscode.Result{Commit: "deadbeef", Quality: vscode.QualityStable, URL: "https://example.invalid/x.tar.gz", SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		ResolveImageConfig: false,
	})
	for _, want := range []string{
		"/home/vscode/.vscode-server/bin/deadbeef",
		"vscode-server.tar.gz",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in graph", want)
		}
	}
}

func TestBuild_RequiresBaseImage(t *testing.T) {
	_, err := Build(GraphInputs{Spec: &spec.DevContainer{}})
	if err == nil {
		t.Fatal("expected error for missing base image")
	}
}
