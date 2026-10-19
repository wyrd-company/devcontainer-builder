package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every target form resolves to one clean absolute path, and the staging
// folder is a sibling of it: never inside the target. The shell runs in the
// home folder, so relative targets resolve against it. Real git and shell.
func TestDotfilesTargetPathForms(t *testing.T) {
	for _, tc := range []struct{ target, want string }{
		{"~/dotfiles/", "home/dotfiles"},
		{"~/dotfiles//", "home/dotfiles"},
		{"./dotfiles", "home/dotfiles"},
		{"dotfiles/.", "home/dotfiles"},
		{"../outside", "outside"},
		{"~/nested/./skip/../dotfiles/", "home/nested/dotfiles"},
		{"$HOME/dotfiles", "home/dotfiles"},
	} {
		t.Run(strings.ReplaceAll(tc.target, "$", "var-"), func(t *testing.T) {
			f := newGitFixture(t, "serve")
			root := filepath.Dir(f.home)
			rt := f.runtime()
			rt.Dotfiles.TargetPath = tc.target
			var out bytes.Buffer
			result, err := RunWithResult(context.Background(), f.execer(), nil, rt, Options{Stdout: &out, Stderr: &out})
			if err != nil || result.Dotfiles != DotfilesInstalled || f.installerRuns() != 1 {
				t.Fatalf("result=%+v err=%v runs=%d output:\n%s", result, err, f.installerRuns(), out.String())
			}
			entries, err := os.ReadDir(filepath.Join(root, tc.want))
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			if err != nil || strings.Join(names, ",") != ".git,install.sh" {
				t.Fatalf("clone at %s: %v %v", tc.want, names, err)
			}
			var staged []string
			_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
				if err == nil && strings.Contains(d.Name(), ".clone.") {
					staged = append(staged, p)
				}
				return nil
			})
			if len(staged) != 0 {
				t.Fatalf("staging folders remain: %v", staged)
			}
		})
	}
}

// A target that resolves to "/" or to the home folder is refused before any
// clone: a failed install with a message, no marker, and nothing changed.
func TestDotfilesTargetPathRejections(t *testing.T) {
	for _, target := range []string{"~", "~/", "~/dotfiles/..", "$HOME", "/", "/..", "../../../../../../../../.."} {
		t.Run(strings.ReplaceAll(target, "$", "var-"), func(t *testing.T) {
			f := newGitFixture(t, "serve")
			rt := f.runtime()
			rt.Dotfiles.TargetPath = target
			var out bytes.Buffer
			var logs []string
			opts := Options{Stdout: &out, Stderr: &out, Logger: func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }}
			result, err := RunWithResult(context.Background(), f.execer(), nil, rt, opts)
			if err != nil || result.Dotfiles != DotfilesFailed || f.installerRuns() != 0 {
				t.Fatalf("result=%+v err=%v runs=%d output:\n%s", result, err, f.installerRuns(), out.String())
			}
			if !strings.Contains(out.String(), "is / or the home folder; use a folder inside it.") || !hasLog(logs, "warning: dotfiles install from ") {
				t.Fatalf("no refusal message:\n%s\nlogs=%q", out.String(), logs)
			}
			entries, err := os.ReadDir(f.home)
			if err != nil || len(entries) != 0 {
				t.Fatalf("home changed: %v %v", entries, err)
			}
			if count, _ := os.ReadFile(filepath.Join(filepath.Dir(f.home), "plan-count")); len(count) != 0 {
				t.Fatalf("git transport ran %s time(s)", count)
			}
		})
	}
}
