// relationships:
//
//	references: [build_target, metadata, imageuser]
package builder

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildOptionsTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Dockerfile"), "FROM scratch AS configured\nUSER config\nFROM scratch AS other\nUSER other\n")
	for _, tc := range []struct {
		name    string
		args    []string
		target  string
		stage   buildkit.DevcontainerStage
		invalid bool
	}{
		{"default", nil, "configured", buildkit.FinalStage, false},
		{"separate", []string{"--target", "other"}, "other", buildkit.AuthoredStage, false},
		{"last", []string{"--target=other", "--target=configured"}, "configured", buildkit.AuthoredStage, false},
		{"generated", []string{"--target=DEV_CONTAINERS_TARGET_STAGE"}, "configured", buildkit.FinalStage, false},
		{"empty", []string{"--target=other", "--target="}, "configured", buildkit.FinalStage, false},
		{"missing", []string{"--target"}, "", 0, true},
		{"unknown", []string{"--target=absent"}, "absent", buildkit.AuthoredStage, false},
		{"normalize-without-features", []string{"--target=dev_containers_feature_content_normalize"}, "dev_containers_feature_content_normalize", buildkit.AuthoredStage, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Target: "configured", Options: tc.args}}
			_, df, err := prepareBaseImage(dc, Options{})
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted missing target")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if df.Target != tc.target || df.Stage != tc.stage {
				t.Fatalf("target=%q stage=%v", df.Target, df.Stage)
			}
			got, err := offlineMetadataBuilder().imageUser(context.Background(), dc, Options{SkipImageMetaResolve: true})
			if err != nil || got != "config" {
				t.Fatalf("inference=%q err=%v", got, err)
			}
		})
	}
}

func TestBuildOptionsTargetAutoStage(t *testing.T) {
	for _, named := range []bool{false, true} {
		dir := t.TempDir()
		df := "FROM scratch\n"
		if named {
			df = "FROM scratch AS named\n"
		}
		writeFile(t, filepath.Join(dir, "Dockerfile"), df)
		dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Options: []string{"--target=dev_container_auto_added_stage_label"}}}
		_, got, err := prepareBaseImage(dc, Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		if named {
			want = "dev_container_auto_added_stage_label"
		}
		if got.Target != want || got.Stage != buildkit.AuthoredStage {
			t.Fatalf("target=%q stage=%v", got.Target, got.Stage)
		}
	}
}

func TestBuildOptionsTargetNormalizeSelection(t *testing.T) {
	var dc spec.DevContainer
	dc.Build = &spec.Build{Dockerfile: "Dockerfile", Target: "base", Options: []string{"--target=dev_containers_feature_content_normalize"}}
	if err := dc.Features.UnmarshalJSON([]byte(`{"./sample":{}}`)); err != nil {
		t.Fatal(err)
	}
	_, df, err := prepareBaseImage(&dc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if df.Stage != buildkit.NormalizeStage || df.Target != "base" {
		t.Fatalf("stage=%v target=%q", df.Stage, df.Target)
	}
}
