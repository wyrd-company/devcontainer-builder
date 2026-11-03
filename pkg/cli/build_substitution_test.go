// relationships:
//
//	references: [build, config]
package cli

import (
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"testing"
)

func TestBuildOptionsSubstitutionContext(t *testing.T) {
	t.Setenv("SAMPLE_BUILD_VALUE", "selected")
	a := &application{idLabels: map[string]string{"sample": "value"}}
	opts := buildOptions{}
	got, err := opts.prepare(a, &spec.DevContainer{Image: "alpine", WorkspaceFolder: "/workspace"}, "/host/workspace", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalEnv["SAMPLE_BUILD_VALUE"] != "selected" || got.Substitution.ContainerWorkspaceFolder != "/workspace" || got.Substitution.IDLabels["sample"] != "value" {
		t.Fatalf("context = %#v", got.Substitution)
	}
}
