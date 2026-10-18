// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package dockercompose

import (
	"encoding/json"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildPrimaryPreservesEachCustomization(t *testing.T) {
	plain := &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{"compose.yml"}, Service: "app"}
	if needsDerivedImage(BuildPrimaryInputs{Spec: plain}) {
		t.Fatal("plain Compose image should be reused")
	}
	for _, tc := range []struct {
		name    string
		options builder.Options
	}{
		{"tags", builder.Options{Tags: []string{"example:dev"}}},
		{"server", builder.Options{InstallVSCodeServer: true}},
		{"push", builder.Options{Push: true}},
		{"output", builder.Options{OutputDir: "output"}},
		{"labels", builder.Options{Labels: map[string]string{"example": "value"}}},
		{"platforms", builder.Options{Platforms: []string{"linux/arm64"}}},
		{"pull", builder.Options{Pull: true}},
		{"no-cache", builder.Options{NoCache: true}},
		{"cache-from", builder.Options{CacheFrom: []string{"type=registry,ref=example:cache"}}},
		{"cache-to", builder.Options{CacheTo: []string{"type=inline"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !needsDerivedImage(BuildPrimaryInputs{Spec: plain, BuilderOptions: tc.options}) {
				t.Fatalf("%s customization discarded", tc.name)
			}
		})
	}
	if !needsDerivedImage(BuildPrimaryInputs{Spec: plain, Tag: "example:dev"}) {
		t.Fatal("explicit tag discarded")
	}
	withFeatures := *plain
	withFeatures.Features = spec.OrderedMap{Keys: []string{"./feature"}, Values: map[string]json.RawMessage{"./feature": json.RawMessage(`{}`)}}
	if !needsDerivedImage(BuildPrimaryInputs{Spec: &withFeatures}) {
		t.Fatal("features discarded")
	}
}
