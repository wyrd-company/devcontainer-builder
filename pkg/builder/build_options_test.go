// relationships:
//
//	references: [build_options, builder]
package builder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildOptionsNetwork(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    string
		invalid bool
	}{
		{"absent", nil, "", false},
		{"host", []string{"--network", "host"}, "host", false},
		{"none", []string{"--network=none"}, "none", false},
		{"default", []string{"--network=default"}, "sandbox", false},
		{"empty", []string{"--network="}, "sandbox", false},
		{"last", []string{"--network=host", "--network=none"}, "none", false},
		{"invalid overwritten", []string{"--network=sample", "--network=host"}, "host", false},
		{"custom", []string{"--network=sample"}, "", true},
		{"frontend internal mode", []string{"--network=sandbox"}, "", true},
		{"case", []string{"--network=HOST"}, "", true},
		{"missing", []string{"--network"}, "", true},
		{"positional", []string{"sample"}, "", true},
		{"terminated", []string{"--", "--network=host"}, "", true},
		{"unmapped", []string{"--ulimit", "nofile=1024:1024"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Build: &spec.Build{Dockerfile: "Dockerfile", Options: tc.args}, Path: filepath.Join(t.TempDir(), "devcontainer.json")}
			got, err := buildNetworkMode(dc)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("mode=%q err=%v want=%q invalid=%v", got, err, tc.want, tc.invalid)
			}
			_, df, err := prepareBaseImage(dc, Options{})
			if (err != nil) != tc.invalid {
				t.Fatalf("prepareBaseImage err=%v", err)
			}
			if !tc.invalid && df.NetworkMode != tc.want {
				t.Fatalf("Dockerfile network=%q want=%q", df.NetworkMode, tc.want)
			}
		})
	}
}

func TestBuildOptionsNetworkRejectsBeforeStaging(t *testing.T) {
	work := filepath.Join(t.TempDir(), "unallocated")
	opts := Options{Spec: &spec.DevContainer{Build: &spec.Build{Dockerfile: "Dockerfile", Options: []string{"--network=sample"}}}, WorkDir: work}
	_, allocated, _, _, _, _, _, _, _, err := New().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
	if allocated != "" {
		t.Fatalf("staging allocated: %s", allocated)
	}
	if _, statErr := os.Stat(work); !os.IsNotExist(statErr) {
		t.Fatalf("staging allocated: %v", statErr)
	}
	if err == nil || !strings.Contains(err.Error(), "network mode") {
		t.Fatalf("err=%v", err)
	}
}
