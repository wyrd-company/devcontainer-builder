// relationships:
//
// references: [README, runner, discovery_test]
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestObserveEffectiveUser(t *testing.T) {
	for _, tc := range []struct {
		name, upstream, local, uid, gid string
		pass                            bool
	}{
		{"default-vs-root", "", "root", "0", "0", true},
		{"root-vs-nonroot", "root", "nobody", "65534", "65534", false},
		{"named-vs-numeric", "nobody", "65534", "65534", "65534", true},
		{"different-primary-group", "65534:0", "65534:65534", "65534", "65534", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, f := discoveryRunner(t)
			ctx := context.Background()
			upstream := r.observe(ctx, &discoveryAdapter{t: t, user: tc.upstream}, f, "upstream")
			local := r.observe(ctx, &discoveryAdapter{t: t, user: tc.local}, f, "go")
			for side, observations := range map[string]map[string]Observation{"upstream": upstream, "go": local} {
				if observations["user"].Error != "" {
					t.Fatalf("%s probe: %+v", side, observations["user"])
				}
			}
			want := map[string]string{"uid": tc.uid, "gid": tc.gid}
			if !reflect.DeepEqual(local["user"].Value, want) {
				t.Fatalf("effective user = %#v, want %#v", local["user"].Value, want)
			}
			result := compare(f.Name, upstream, local)
			if result.Dimensions["user"].Pass != tc.pass {
				t.Fatalf("user comparison: %+v", result.Dimensions["user"])
			}
			if result.Total != len(dimensions) {
				t.Fatalf("information changed denominator: %d", result.Total)
			}
			output := t.TempDir()
			if err := writeReport(output, Report{Fixtures: []FixtureResult{result}}); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(filepath.Join(output, "score.json"))
			if err != nil {
				t.Fatal(err)
			}
			var report Report
			if err := json.Unmarshal(body, &report); err != nil {
				t.Fatal(err)
			}
			d := report.Fixtures[0].Dimensions["user"]
			for side, obs := range map[string]Observation{"upstream": d.Upstream, "go": d.Go} {
				raw := tc.upstream
				if side == "go" {
					raw = tc.local
				}
				if got, exists := obs.Info["rawUser"]; !exists || got != raw {
					t.Fatalf("%s raw USER not retained: %+v", side, obs)
				}
			}
			if d.Pass != tc.pass || !reflect.DeepEqual(d.Go.Value, map[string]any{"uid": tc.uid, "gid": tc.gid}) {
				t.Fatalf("report lost effective user: %+v", d)
			}
		})
	}
}

func TestObserveEffectiveUserUnavailable(t *testing.T) {
	r, f := discoveryRunner(t)
	observations := r.observe(context.Background(), &discoveryAdapter{t: t, user: "root", stopped: true}, f, "go")
	user := observations["user"]
	if user.Error == "" || user.Value != nil {
		t.Fatalf("stopped container user probe passed: %+v", user)
	}
	if user.Info["rawUser"] != "root" {
		t.Fatalf("failed probe lost raw USER: %+v", user)
	}
	if compare(f.Name, observations, observations).Dimensions["user"].Pass {
		t.Fatal("unavailable users counted as a match")
	}
}

// The wrapper injects one failed probe; discovery, inspection and the other
// probe still cross Docker so either failed component must reject the user.
func TestObserveEffectiveUserProbeFailure(t *testing.T) {
	for _, flag := range []string{"-u", "-g"} {
		t.Run(flag, func(t *testing.T) {
			r, f := discoveryRunner(t)
			docker, err := exec.LookPath("docker")
			if err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := `#!/bin/sh
if [ "$1" = exec ] && [ "$3" = id ] && [ "$4" = "$PARITY_FAILED_PROBE" ]; then
 exit 1
fi
exec "$PARITY_REAL_DOCKER" "$@"
`
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			r.env = append(r.env, "PARITY_REAL_DOCKER="+docker, "PARITY_FAILED_PROBE="+flag)
			observations := r.observe(context.Background(), &discoveryAdapter{t: t, user: "root"}, f, "go")
			if user := observations["user"]; user.Error == "" || user.Value != nil || user.Info["rawUser"] != "root" {
				t.Fatalf("failed %s probe accepted or raw USER lost: %+v", flag, user)
			}
		})
	}
}
