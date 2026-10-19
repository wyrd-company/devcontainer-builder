// relationships:
//
//	references: [env]
package testgit

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// AssertIsolated runs a fixture with inherited Git paths aimed at a populated
// sentinel repository. It checks every repository file, including config, HEAD,
// refs and index, even when the fixture aborts the test.
func AssertIsolated(t *testing.T, fixture func()) {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = Env()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sentinel git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "example.txt"), []byte("sentinel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "Sentinel")
	snapshot := func() map[string]string {
		t.Helper()
		files := make(map[string]string)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(body)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()
	t.Cleanup(func() {
		if !reflect.DeepEqual(before, snapshot()) {
			t.Error("sentinel repository changed (config, HEAD, refs, index or worktree)")
		}
	})
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	t.Setenv("GIT_WORK_TREE", root)
	fixture()
}
