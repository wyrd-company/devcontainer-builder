// relationships:
//
//	references: [composebuild, metadata]
package dockercompose

import (
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestComposeServiceSyntaxControl(t *testing.T) {
	for _, omit := range []bool{false, true} {
		build, _, err := serviceBuild(&types.Project{WorkingDir: t.TempDir()}, types.ServiceConfig{Name: "app", WorkloadSpec: types.WorkloadSpec{Build: &types.BuildConfig{}}}, "example:latest", nil, BuildServiceImagesInputs{OmitSyntaxDirective: omit})
		if err != nil {
			t.Fatal(err)
		}
		if build.OmitSyntaxDirective != omit {
			t.Fatalf("frontend omit=%t want=%t", build.OmitSyntaxDirective, omit)
		}
	}
}

func TestComposeImageMetadataRemoteEnvControl(t *testing.T) {
	dc := &spec.DevContainer{UpdateRemoteEnv: map[string]any{"VALUE": "config"}, RemoteUser: "sample"}
	base, _ := metadata.Encode([]metadata.Entry{{ID: "base", RemoteEnv: map[string]any{"BASE": "inherited"}}})
	for _, omit := range []bool{false, true} {
		entries := imageMetadataEntries(UpRequest{Spec: dc, OmitConfigRemoteEnvFromMetadata: omit}, map[string]string{metadata.Label: base}, nil)
		if len(entries) != 2 || entries[0].RemoteEnv["BASE"] != "inherited" || (len(entries[1].RemoteEnv) == 0) != omit || entries[1].RemoteUser != "sample" {
			t.Fatalf("omit=%t entries=%+v", omit, entries)
		}
	}
}
