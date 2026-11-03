// relationships:
//
//	references: [create_only, create_only_test]
package docker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/volume/mounts"
)

// Keep the portable projection tied to the Engine's Linux grammar. The daemon
// package is test-only so Darwin release builds do not import its mount setup.
func TestStrictBindMatchesEngineParser(t *testing.T) {
	for _, raw := range []string{
		"/sample/source:/sample/target", "/sample/source:/sample/target:",
		"/sample/source:/sample/target:ro", "/sample/source:/sample/target:rw",
		"/sample/source:/sample/target:ro,rslave,cached",
		"/sample/source:/sample/target:rw,private,consistent",
		"/sample/source:/sample/target:shared,delegated",
		"/sample/source:/sample/target:rprivate", "/sample/source:/sample/target:rshared",
		"/sample/source:/sample/target:slave", "/sample/source:/sample/target:ro,ro",
		"/sample/source:/sample/target:ro,rw", "/sample/source:/sample/target:rslave,private",
		"/sample/source:/sample/target:cached,delegated", "/sample/source:/sample/target:unknown",
		"/sample/source:/sample/target:nocopy", "/sample/source:/sample/target:ro,",
		"/sample/source:/", "/sample/source:/sample/..", "/sample/source:relative",
		"/sample/source:", "/sample/source:/sample/target:ro:extra",
	} {
		t.Run(raw, func(t *testing.T) {
			parsed, wantErr := mounts.NewLinuxParser().ParseMountRaw(raw, "")
			host := &container.HostConfig{Binds: []string{raw}}
			gotErr := strictCreateMounts(host)
			if (gotErr != nil) != (wantErr != nil) {
				t.Fatalf("portable error %v; Engine error %v", gotErr, wantErr)
			}
			if wantErr != nil {
				return
			}
			want := parsed.Spec
			for _, mode := range strings.Split(parsed.Mode, ",") {
				switch mode {
				case "consistent", "cached", "delegated":
					want.Consistency = mount.Consistency(mode)
				}
			}
			if len(host.Binds) != 0 || !reflect.DeepEqual(host.Mounts, []mount.Mount{want}) {
				t.Fatalf("portable %+v; Engine %+v", host, want)
			}
		})
	}
}
