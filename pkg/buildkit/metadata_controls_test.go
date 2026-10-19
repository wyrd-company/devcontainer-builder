// relationships:
//
//	references: [gateway, ../metadata/metadata]
package buildkit

import (
	"encoding/json"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestMergeImageConfigMetadataControls(t *testing.T) {
	for _, omit := range []bool{false, true} {
		g := GraphInputs{Spec: &spec.DevContainer{UpdateRemoteEnv: map[string]any{"VALUE": "config"}, Customizations: map[string]any{"editor": "config"}}, Features: []features.Resolved{{Source: "./sample", Metadata: features.Metadata{Customizations: map[string]any{"editor": "feature"}}}}, Metadata: metadata.CollectOptions{SkipFeatureCustomizations: omit, OmitConfigRemoteEnv: omit}}
		var img dockerspec.DockerOCIImage
		baseLabel, _ := metadata.Encode([]metadata.Entry{{ID: "base", RemoteEnv: map[string]any{"BASE": "inherited"}, Customizations: map[string]any{"editor": "base"}}})
		img.Config.Labels = map[string]string{metadata.Label: baseLabel}
		base, err := json.Marshal(img)
		if err != nil {
			t.Fatal(err)
		}
		out, err := mergeImageConfig(base, g, nil, amd64)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(out, &img); err != nil {
			t.Fatal(err)
		}
		entries, err := metadata.Parse(img.Config.Labels[metadata.Label])
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 || entries[0].RemoteEnv["BASE"] != "inherited" || entries[0].Customizations["editor"] != "base" || (len(entries[1].Customizations) == 0) != omit || (len(entries[2].RemoteEnv) == 0) != omit || entries[2].Customizations["editor"] != "config" {
			t.Fatalf("omit=%t entries=%+v", omit, entries)
		}
	}
}
