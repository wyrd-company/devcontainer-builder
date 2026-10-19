package runner

import (
	"reflect"
	"testing"
)

// Upstream probeRemoteEnv spreads --remote-env before the configuration's
// remoteEnv, and runLifecycleCommand spreads secrets after both.
func TestLifecycleEnvPrecedence(t *testing.T) {
	got := LifecycleEnv(
		map[string]string{"ONLY_REMOTE": "remote", "REMOTE_CONFIG": "remote", "ALL": "remote"},
		map[string]string{"REMOTE_CONFIG": "config", "CONFIG_SECRET": "config", "ALL": "config"},
		map[string]string{"CONFIG_SECRET": "secret", "ALL": "secret"},
	)
	want := map[string]string{"ONLY_REMOTE": "remote", "REMOTE_CONFIG": "config", "CONFIG_SECRET": "secret", "ALL": "secret"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LifecycleEnv = %v, want %v", got, want)
	}
	if got := LifecycleEnv(nil, nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("LifecycleEnv(nil...) = %#v, want an empty map", got)
	}
}
