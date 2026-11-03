// relationships:
//
//	references: [build_options, builder, ../../reference/cli/src/spec-node/singleContainer]
package builder

import (
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"path/filepath"
	"testing"
)

func TestBuildCgroupOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		want    string
		invalid bool
	}{
		{name: "absent"},
		{name: "separate", args: []string{"--cgroup-parent", "/sample"}, want: "/sample"},
		{name: "equals", args: []string{"--cgroup-parent=sample.slice"}, want: "sample.slice"},
		{name: "last", args: []string{"--cgroup-parent=first", "--cgroup-parent=second"}, want: "second"},
		{name: "empty-reset", args: []string{"--cgroup-parent=first", "--cgroup-parent="}},
		{name: "literal", args: []string{"--cgroup-parent=sample:scope/value with spaces"}, want: "sample:scope/value with spaces"},
		{name: "missing", args: []string{"--cgroup-parent"}, invalid: true},
		{name: "position", args: []string{"--cgroup-parent=sample", "extra"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Path: filepath.Join(t.TempDir(), "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Options: tc.args}}
			if err := validateBuildSupport(dc); (err != nil) != tc.invalid {
				t.Fatalf("validation=%v invalid=%v", err, tc.invalid)
			}
			_, df, err := prepareBaseImage(dc, Options{})
			if (err != nil) != tc.invalid {
				t.Fatalf("prepare=%v invalid=%v", err, tc.invalid)
			}
			if !tc.invalid && df.CgroupParent != tc.want {
				t.Fatalf("parent=%q want=%q", df.CgroupParent, tc.want)
			}
		})
	}
}
