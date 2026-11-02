// relationships:
//
//	references: [build_options, builder, ../../reference/cli/src/spec-node/singleContainer]
package builder

import (
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"path/filepath"
	"testing"
)

func TestBuildResourceOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		args    []string
		shm     int64
		limits  string
		invalid bool
	}{
		{"absent", nil, 0, "", false},
		{"size", []string{"--shm-size", "128m"}, 134217728, "", false},
		{"last size", []string{"--shm-size=128m", "--shm-size=2k"}, 2048, "", false},
		{"zero", []string{"--shm-size=128m", "--shm-size=0"}, 0, "", false},
		{"fraction", []string{"--shm-size=1.5m"}, 1572864, "", false},
		{"limit", []string{"--ulimit", "nofile=1024:2048"}, 0, "nofile=1024:2048", false},
		{"single", []string{"--ulimit=nofile=1024"}, 0, "nofile=1024:1024", false},
		{"unlimited", []string{"--ulimit=core=-1:-1"}, 0, "core=-1:-1", false},
		{"last limit", []string{"--ulimit=nofile=1024:2048", "--ulimit=core=0", "--ulimit=nofile=512"}, 0, "core=0:0,nofile=512:512", false},
		{"both", []string{"--ulimit=nofile=1024", "--shm-size=2m"}, 2097152, "nofile=1024:1024", false},
		{"bad size", []string{"--shm-size=bad", "--shm-size=2m"}, 0, "", true},
		{"negative size", []string{"--shm-size=-1"}, 0, "", true},
		{"size missing", []string{"--shm-size"}, 0, "", true},
		{"bad limit", []string{"--ulimit=sample=2"}, 0, "", true},
		{"soft exceeds hard", []string{"--ulimit=nofile=2048:1024"}, 0, "", true},
		{"bad earlier limit", []string{"--ulimit=nofile=bad", "--ulimit=nofile=1024"}, 0, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Build: &spec.Build{Dockerfile: "Dockerfile", Options: tc.args}, Path: filepath.Join(t.TempDir(), "devcontainer.json")}
			if err := validateBuildSupport(dc); (err != nil) != tc.invalid {
				t.Fatalf("validation err=%v invalid=%v", err, tc.invalid)
			}
			_, df, err := prepareBaseImage(dc, Options{})
			if (err != nil) != tc.invalid {
				t.Fatalf("err=%v invalid=%v", err, tc.invalid)
			}
			if !tc.invalid && (df.ShmSize != tc.shm || df.Ulimits != tc.limits) {
				t.Fatalf("resources=%d %q want=%d %q", df.ShmSize, df.Ulimits, tc.shm, tc.limits)
			}
		})
	}
}
