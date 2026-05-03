package dockercompose

import (
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestServiceConfigHash_StableForSameInput(t *testing.T) {
	val := "v"
	build := func() types.ServiceConfig {
		return types.ServiceConfig{
			Name:        "app",
			Image:       "alpine:3.20",
			Command:     types.ShellCommand{"sh", "-c", "exec /entrypoint"},
			WorkingDir:  "/srv",
			User:        "1000:1000",
			Environment: types.MappingWithEquals{"A": &val},
			Volumes: []types.ServiceVolumeConfig{
				{Type: types.VolumeTypeBind, Source: "/host", Target: "/in"},
			},
			Labels: types.Labels{
				"team": "infra",
				// Ignored:
				"com.docker.compose.project": "should-be-stripped",
				"devcontainer.local_folder":  "/should/be/stripped",
			},
		}
	}
	a := serviceConfigHash(build())
	b := serviceConfigHash(build())
	if a != b {
		t.Fatalf("hash unstable across runs: %s vs %s", a, b)
	}
	if a == "" {
		t.Fatal("hash is empty")
	}
}

func TestServiceConfigHash_ChangesWhenImageChanges(t *testing.T) {
	a := serviceConfigHash(types.ServiceConfig{Name: "app", Image: "alpine:3.20"})
	b := serviceConfigHash(types.ServiceConfig{Name: "app", Image: "alpine:3.21"})
	if a == b {
		t.Fatal("hash didn't change when image bumped")
	}
}

func TestServiceConfigHash_ChangesWhenEnvChanges(t *testing.T) {
	v1, v2 := "1", "2"
	a := serviceConfigHash(types.ServiceConfig{
		Image:       "alpine",
		Environment: types.MappingWithEquals{"FOO": &v1},
	})
	b := serviceConfigHash(types.ServiceConfig{
		Image:       "alpine",
		Environment: types.MappingWithEquals{"FOO": &v2},
	})
	if a == b {
		t.Fatal("hash didn't change when env changed")
	}
}

func TestServiceConfigHash_IgnoresHostManagedLabels(t *testing.T) {
	bare := serviceConfigHash(types.ServiceConfig{Image: "alpine"})
	// Adding only labels we strip should not change the hash.
	withManaged := serviceConfigHash(types.ServiceConfig{
		Image: "alpine",
		Labels: types.Labels{
			"com.docker.compose.project": "anything",
			"devcontainer.local_folder":  "/anything",
		},
	})
	if bare != withManaged {
		t.Fatalf("hash should ignore host-managed labels: %s vs %s", bare, withManaged)
	}
}

func TestServiceConfigHash_VolumeOrderMatters(t *testing.T) {
	a := serviceConfigHash(types.ServiceConfig{
		Image: "alpine",
		Volumes: []types.ServiceVolumeConfig{
			{Type: types.VolumeTypeBind, Source: "/a", Target: "/x"},
			{Type: types.VolumeTypeBind, Source: "/b", Target: "/y"},
		},
	})
	b := serviceConfigHash(types.ServiceConfig{
		Image: "alpine",
		Volumes: []types.ServiceVolumeConfig{
			{Type: types.VolumeTypeBind, Source: "/b", Target: "/y"},
			{Type: types.VolumeTypeBind, Source: "/a", Target: "/x"},
		},
	})
	if a == b {
		t.Fatal("hash should reflect volume order")
	}
}
