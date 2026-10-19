package runner

import (
	"bytes"
	"context"
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

// As upstream, an existing target is used as it is: no clone, no staging,
// and its own installer runs once. The first case is the home folder with an
// explicit install command (zone-pond's regression). Real git and shell.
func TestDotfilesExistingTargetUsedAsIs(t *testing.T) {
	for _, tc := range []struct{ target, folder string }{
		{"$HOME", ""},
		{"~", ""},
		{"~/", ""},
		{"~/mine/", "mine"},
		{"./mine/./", "mine"},
	} {
		t.Run(strings.ReplaceAll(tc.target, "$", "var-"), func(t *testing.T) {
			f := newGitFixture(t, "serve")
			dir := filepath.Join(f.home, tc.folder)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(dir, "install.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran >> \"$HOME/installer-runs\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			rt := f.runtime()
			rt.Dotfiles.TargetPath, rt.Dotfiles.InstallCommand = tc.target, "install.sh"
			var out bytes.Buffer
			result, err := RunWithResult(context.Background(), f.execer(), nil, rt, Options{Stdout: &out, Stderr: &out})
			if err != nil || result.Dotfiles != DotfilesInstalled || f.installerRuns() != 1 {
				t.Fatalf("result=%+v err=%v runs=%d output:\n%s", result, err, f.installerRuns(), out.String())
			}
			if _, err := os.Stat(script); err != nil {
				t.Fatalf("existing script damaged: %v", err)
			}
			if count, _ := os.ReadFile(filepath.Join(filepath.Dir(f.home), "plan-count")); len(count) != 0 {
				t.Fatalf("git transport ran %s time(s)", count)
			}
			if staged, _ := filepath.Glob(filepath.Join(f.home, "*.clone.*")); len(staged) != 0 {
				t.Fatalf("staging folders: %v", staged)
			}
		})
	}
}
