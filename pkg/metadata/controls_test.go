// relationships:
//
//	references: [metadata]
package metadata

import (
	"reflect"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestCollectMetadataControls(t *testing.T) {
	inherited := Entry{ID: "base", Customizations: map[string]any{"editor": "base"}, RemoteEnv: map[string]any{"BASE": "base"}}
	feature := features.Resolved{Source: "./sample", Metadata: features.Metadata{Customizations: map[string]any{"editor": "feature"}, PostCreateCommand: spec.Command{Shell: "echo feature"}}}
	dc := &spec.DevContainer{Customizations: map[string]any{"editor": "config"}, UpdateRemoteEnv: map[string]any{"VALUE": "config"}}
	for _, mode := range []string{"default", "skip-customizations", "omit-remote-env", "both"} {
		t.Run(mode, func(t *testing.T) {
			opts := CollectOptions{SkipFeatureCustomizations: mode == "skip-customizations" || mode == "both", OmitConfigRemoteEnv: mode == "omit-remote-env" || mode == "both"}
			entries := CollectWithOptions([]Entry{inherited}, []features.Resolved{feature}, dc, opts)
			encoded, err := Encode(entries)
			if err != nil {
				t.Fatal(err)
			}
			entries, err = Parse(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 3 || !reflect.DeepEqual(entries[0], inherited) {
				t.Fatalf("inherited entry changed: %+v", entries)
			}
			if (entries[1].Customizations == nil) != opts.SkipFeatureCustomizations || entries[1].PostCreate.Shell != "echo feature" {
				t.Fatalf("feature entry=%+v", entries[1])
			}
			if !reflect.DeepEqual(entries[2].Customizations, dc.Customizations) || (len(entries[2].RemoteEnv) == 0) != opts.OmitConfigRemoteEnv {
				t.Fatalf("config entry=%+v", entries[2])
			}
			if feature.Metadata.Customizations == nil || len(dc.UpdateRemoteEnv) != 1 {
				t.Fatal("source metadata was mutated")
			}
		})
	}
	if got := CollectWithOptions(nil, nil, &spec.DevContainer{UpdateRemoteEnv: dc.UpdateRemoteEnv}, CollectOptions{OmitConfigRemoteEnv: true}); len(got) != 0 {
		t.Fatalf("empty contribution retained: %+v", got)
	}
}
