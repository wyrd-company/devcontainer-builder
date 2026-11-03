// relationships:
//
//	references: [up, spawn]
package dockercompose

import (
	"github.com/compose-spec/compose-go/v2/types"
	"reflect"
	"strings"
	"testing"
)

func TestCreateOnlySELinuxBinds(t *testing.T) {
	for _, option := range []string{"z", "Z"} {
		t.Run(option, func(t *testing.T) {
			vol := types.ServiceVolumeConfig{Type: types.VolumeTypeBind, Source: "/sample/source", Target: "/sample/target", Bind: &types.ServiceVolumeBind{CreateHostPath: true, SELinux: option}}
			svc := types.ServiceConfig{Name: "sample", Volumes: []types.ServiceVolumeConfig{vol}}
			hash := serviceConfigHash(svc)
			proj := &types.Project{Services: types.Services{"sample": svc}}
			err := validateCreateOnlyBinds(proj, nil)
			for _, part := range []string{"service \"sample\"", "/sample/source", "/sample/target", option, "ordinary up still supports it"} {
				if err == nil || !strings.Contains(err.Error(), part) {
					t.Fatalf("missing %q in %v", part, err)
				}
			}
			got := proj.Services["sample"]
			if !reflect.DeepEqual(got, svc) || serviceConfigHash(got) != hash {
				t.Fatal("validation changed authored service/hash")
			}
			binds, mounts, err := translateVolumes(got.Volumes, nil)
			if err != nil || len(mounts) != 0 || len(binds) != 1 || binds[0] != "/sample/source:/sample/target:rw,"+option {
				t.Fatalf("ordinary up relabel lost: %v %v %v", binds, mounts, err)
			}
		})
	}
}
