// relationships:
//
//	references: [build_options, builder, ../../reference/cli/src/spec-node/singleContainer]
package builder

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildOptionsLabels(t *testing.T) {
	t.Setenv("SAMPLE_LABEL", "must-not-inherit")
	for _, tc := range []struct {
		name    string
		args    []string
		want    map[string]string
		invalid bool
	}{
		{"separate", []string{"--label", "sample=value"}, map[string]string{"sample": "value"}, false},
		{"assigned", []string{"--label=sample=a=b,c"}, map[string]string{"sample": "a=b,c"}, false},
		{"last", []string{"--label=sample=first", "--label=other=keep", "--label=sample=last"}, map[string]string{"sample": "last", "other": "keep"}, false},
		{"bare", []string{"--label=SAMPLE_LABEL"}, map[string]string{"SAMPLE_LABEL": ""}, false},
		{"empty-value", []string{"--label=sample="}, map[string]string{"sample": ""}, false},
		{"whitespace", []string{"--label= sample key = value "}, map[string]string{" sample key ": " value "}, false},
		{"empty-key", []string{"--label==value"}, nil, true},
		{"empty-label", []string{"--label="}, nil, true},
		{"earlier-empty-key", []string{"--label==bad", "--label=sample=valid"}, nil, true},
		{"missing", []string{"--label"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Build: &spec.Build{Options: tc.args}}
			got, err := buildImageLabels(dc, nil)
			if (err != nil) != tc.invalid {
				t.Fatalf("labels=%v err=%v", got, err)
			}
			if !tc.invalid && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("labels=%v want=%v", got, tc.want)
			}
			if err := validateBuildSupport(dc); (err != nil) != tc.invalid {
				t.Fatalf("validation err=%v", err)
			}
		})
	}
}

func TestBuildOptionsLabelsExplicitPrecedence(t *testing.T) {
	dc := &spec.DevContainer{Build: &spec.Build{Options: []string{"--network=host", "--label=sample=config", "--label=keep=config"}}}
	explicit := map[string]string{"sample": "explicit", "extra": "explicit"}
	got, err := buildImageLabels(dc, explicit)
	want := map[string]string{"sample": "explicit", "keep": "config", "extra": "explicit"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("labels=%v err=%v", got, err)
	}
	got["sample"] = "changed"
	if explicit["sample"] != "explicit" {
		t.Fatal("mutated explicit labels")
	}
	network, err := buildNetworkMode(dc)
	if err != nil || network != "host" {
		t.Fatalf("network=%s err=%v", network, err)
	}
}

func TestBuildOptionsLabelsRejectBeforeStaging(t *testing.T) {
	work := filepath.Join(t.TempDir(), "unallocated")
	opts := Options{Spec: &spec.DevContainer{Build: &spec.Build{Dockerfile: "Dockerfile", Options: []string{"--label==value"}}}, WorkDir: work}
	_, allocated, _, _, _, _, _, _, _, err := New().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
	if err == nil {
		t.Fatal("accepted empty label key")
	}
	if allocated != "" {
		t.Fatal("allocated staging")
	}
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("staging: %v", err)
	}
}
