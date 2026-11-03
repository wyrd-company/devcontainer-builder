// relationships:
//
//	references: [build_options, builder, ../buildkit/build_hosts]
package builder

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildExtraHostsOptions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args, want []string
		invalid    bool
	}{
		{name: "absent"},
		{name: "separate", args: []string{"--add-host", "sample:127.0.0.1"}, want: []string{"sample:127.0.0.1"}},
		{name: "repeat-csv", args: []string{"--add-host=sample:127.0.0.1,other:[::1]", "--add-host=sample:127.0.0.2"}, want: []string{"sample:127.0.0.1", "other:[::1]", "sample:127.0.0.2"}},
		{name: "quoted-multi-ip", args: []string{"--add-host=\"sample=127.0.0.1,::1\""}, want: []string{"sample=127.0.0.1,::1"}},
		{name: "empty", args: []string{"--add-host="}, want: []string{}},
		{name: "missing", args: []string{"--add-host"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Path: filepath.Join(t.TempDir(), "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Options: tc.args}}
			_, df, err := prepareBaseImage(dc, Options{})
			if (err != nil) != tc.invalid {
				t.Fatalf("err=%v invalid=%v", err, tc.invalid)
			}
			if !tc.invalid && !reflect.DeepEqual(df.ExtraHosts, tc.want) {
				t.Fatalf("hosts=%v want=%v", df.ExtraHosts, tc.want)
			}
		})
	}
}
