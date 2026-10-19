// relationships:
//
// references: [staging]
package datafolder

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPrivateStagingNormalizedConcurrentAndRetry(t *testing.T) {
	base := t.TempDir()
	parent := filepath.Join(base, "state")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(parent, "existing")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{parent + "/", parent + "/../state/./", relative} {
		var wg sync.WaitGroup
		dirs := make(chan string, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				dir, e := NewStaging(input, "run-")
				if e != nil {
					t.Error(e)
					return
				}
				dirs <- dir
			}()
		}
		wg.Wait()
		close(dirs)
		seen := map[string]bool{}
		for dir := range dirs {
			if filepath.Dir(dir) != parent || seen[dir] {
				t.Fatalf("not a private normalized child: %s", dir)
			}
			seen[dir] = true
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
		if len(seen) != 2 {
			t.Fatal("missing staging directory")
		}
	}
	if _, err := NewStaging(sentinel, "run-"); err == nil {
		t.Fatal("regular file accepted as staging parent")
	}
	retry, err := NewStaging(parent, "retry-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(retry); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || string(content) != "keep" {
		t.Fatal("existing content changed")
	}
}

func TestStagingCreatesSelectedParent(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "missing", "state")
	dir, err := NewStaging(parent+"/./", "run-")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != parent {
		t.Fatalf("staged outside selected parent: %s", dir)
	}
}
