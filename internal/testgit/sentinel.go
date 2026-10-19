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
	// Background maintenance creates transient lock files after commit returns.
	// The sentinel must stay idle while its complete contents are compared.
	run("config", "maintenance.auto", "false")
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
		after := snapshot()
		if !reflect.DeepEqual(before, after) {
			for path, body := range before {
				if got, ok := after[path]; !ok || body != got {
					t.Errorf("sentinel repository changed existing file: %s", path)
				}
			}
			for path := range after {
				if _, ok := before[path]; !ok {
					t.Errorf("sentinel repository added file: %s", path)
				}
			}
		}
	})
	t.Setenv("GIT_DIR", filepath.Join(root, ".git"))
	t.Setenv("GIT_WORK_TREE", root)
	fixture()
}
