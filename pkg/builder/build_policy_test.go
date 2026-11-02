// relationships:
//   references: [builder, build_options, build_cache]

package builder

import (
	"reflect"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildPolicyOptions(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		invocation, libraryPull bool
		args                    []string
		cache, pull             bool
		invalid                 bool
	}{
		{name: "default"},
		{name: "invocation", invocation: true, cache: true, pull: true},
		{name: "library-pull", libraryPull: true, pull: true},
		{name: "bare", args: []string{"--no-cache", "--pull"}, cache: true, pull: true},
		{name: "override-generated", invocation: true, args: []string{"--no-cache=false", "--pull=false"}},
		{name: "override-library-pull", libraryPull: true, args: []string{"--pull=false"}},
		{name: "cache-alone", args: []string{"--no-cache=true"}, cache: true},
		{name: "pull-alone", args: []string{"--pull=1"}, pull: true},
		{name: "last", args: []string{"--no-cache", "--no-cache=0", "--pull=false", "--pull=T"}, pull: true},
		{name: "invalid-cache", args: []string{"--no-cache=bad", "--no-cache=true"}, invalid: true},
		{name: "invalid-pull", args: []string{"--pull=bad", "--pull=true"}, invalid: true},
		{name: "separate-bool", args: []string{"--pull", "false"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{Build: &spec.Build{Dockerfile: "Dockerfile", Options: tc.args, CacheFrom: spec.StringOrSlice{"configured"}}}
			got, err := solveOptions(Options{NoCache: tc.invocation, Pull: tc.libraryPull, CacheFrom: []string{"library"}}, t.TempDir(), dc)
			if (err != nil) != tc.invalid {
				t.Fatalf("err=%v", err)
			}
			if tc.invalid {
				return
			}
			if got.NoCache != tc.cache || got.Pull != tc.pull {
				t.Fatalf("cache=%v pull=%v want=%v/%v", got.NoCache, got.Pull, tc.cache, tc.pull)
			}
			var imports []string
			for _, entry := range got.CacheImports {
				imports = append(imports, entry.Attrs["ref"])
			}
			var want []string
			if !tc.invocation {
				want = []string{"library", "configured"}
			}
			if !reflect.DeepEqual(imports, want) {
				t.Fatalf("imports=%v want=%v", imports, want)
			}
		})
	}
}

func TestImageNoCacheDoesNotImplyPull(t *testing.T) {
	got, err := solveOptions(Options{NoCache: true}, t.TempDir(), &spec.DevContainer{Image: "alpine"})
	if err != nil || !got.NoCache || got.Pull {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}
