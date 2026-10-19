// relationships:
//
//	references: [controls, runner, dotfiles_test]
package runner

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func controlPlan() []lifecycle.Phase {
	return lifecycle.Plan(&spec.DevContainer{
		OnCreate: spec.Command{Shell: "create"}, UpdateContent: spec.Command{Shell: "update"},
		PostCreate: spec.Command{Shell: "post-create"}, PostStart: spec.Command{Shell: "start"}, PostAttach: spec.Command{Shell: "attach"},
	}, nil)
}

func TestLifecycleStopBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		controls LifecycleControls
		want     int
		dotfiles bool
	}{
		{"default", LifecycleControls{}, 5, true},
		{"wait-ignored-without-flag", LifecycleControls{WaitFor: "initializeCommand"}, 5, true},
		{"skip-default", LifecycleControls{SkipNonBlocking: true}, 2, false},
		{"skip-initialize", LifecycleControls{SkipNonBlocking: true, WaitFor: "initializeCommand"}, 0, false},
		{"skip-create", LifecycleControls{SkipNonBlocking: true, WaitFor: "onCreateCommand"}, 1, false},
		{"skip-update", LifecycleControls{SkipNonBlocking: true, WaitFor: "updateContentCommand"}, 2, false},
		{"skip-post-create", LifecycleControls{SkipNonBlocking: true, WaitFor: "postCreateCommand"}, 3, false},
		{"skip-start", LifecycleControls{SkipNonBlocking: true, WaitFor: "postStartCommand"}, 4, true},
		{"prebuild", LifecycleControls{Prebuild: true}, 2, false},
		{"personalization", LifecycleControls{StopForPersonalization: true}, 3, true},
		{"prebuild-skip-create", LifecycleControls{Prebuild: true, SkipNonBlocking: true, WaitFor: "onCreateCommand"}, 1, false},
		{"prebuild-personalization", LifecycleControls{Prebuild: true, StopForPersonalization: true}, 2, false},
		{"personalization-skip-post-create", LifecycleControls{StopForPersonalization: true, SkipNonBlocking: true, WaitFor: "postCreateCommand"}, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, sparse := range []bool{false, true} {
				plan := controlPlan()
				want := plan[:tc.want]
				if sparse {
					// No command at the stop point must still stop later hooks.
					if tc.want > 0 {
						plan = append(append([]lifecycle.Phase{}, plan[:tc.want-1]...), plan[tc.want:]...)
						want = want[:tc.want-1]
					}
				}
				f := noMarker(nil)
				rt := dotfilesRuntime()
				rt.Lifecycle = tc.controls
				result, err := RunWithResult(context.Background(), f, plan, rt, Options{})
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(result.RanPhases(), append([]lifecycle.Phase{}, want...)) {
					t.Fatalf("sparse=%v got=%v want=%v", sparse, result.RanPhases(), want)
				}
				if (result.Dotfiles == DotfilesInstalled) != tc.dotfiles {
					t.Fatalf("sparse=%v dotfiles=%v want=%v", sparse, result.Dotfiles, tc.dotfiles)
				}
			}
		})
	}
}

func TestPrebuildRerunsUpdateContentWithCurrentMarker(t *testing.T) {
	for _, prebuild := range []bool{false, true} {
		f := &fakeExecer{handler: func(req ExecRequest) (*ExecResult, error) {
			if len(req.Cmd) == 3 && strings.Contains(req.Cmd[2], "ENOENT") {
				return &ExecResult{ExitCode: 1}, nil
			}
			return &ExecResult{}, nil
		}}
		rt := dotfilesRuntime()
		rt.Dotfiles = Dotfiles{}
		rt.Lifecycle.Prebuild = prebuild
		plan := controlPlan()
		// All feature and configuration commands at updateContent rerun.
		plan = append(plan[:2:2], append([]lifecycle.Phase{plan[1]}, plan[2:]...)...)
		result, err := RunWithResult(context.Background(), f, plan, rt, Options{})
		if err != nil {
			t.Fatal(err)
		}
		want := 1 // Only postAttach runs with current markers by default.
		if prebuild {
			want = 2
		}
		if got := len(result.RanPhases()); got != want {
			t.Fatalf("prebuild=%v ran=%v", prebuild, result.RanPhases())
		}
		if prebuild {
			for _, p := range result.RanPhases() {
				if p.Hook != lifecycle.HookUpdateContent {
					t.Fatalf("unexpected rerun: %v", p)
				}
			}
		}
	}
}

func TestLifecycleFailureHasNoStopReason(t *testing.T) {
	f := &fakeExecer{handler: func(req ExecRequest) (*ExecResult, error) {
		if len(req.Cmd) == 3 && req.Cmd[2] == "create" {
			return &ExecResult{ExitCode: 23}, nil
		}
		return &ExecResult{}, nil
	}}
	result, err := RunWithResult(context.Background(), f, controlPlan(), dotfilesRuntime(), Options{})
	if err == nil || result.Result != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
