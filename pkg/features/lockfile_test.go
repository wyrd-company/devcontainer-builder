// relationships:
//
//	references: [lockfile, registry_test]
package features

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

func lockedSample() []Resolved {
	return []Resolved{{Source: "registry.example/collection/sample:1", Reference: &oci.Reference{Registry: "registry.example", Repository: "collection/sample", Tag: "1"}, Digest: "sha256:" + strings.Repeat("a", 64), Metadata: Metadata{Version: "1.0.0"}}}
}

func TestLockfilePath(t *testing.T) {
	for _, tc := range []struct{ config, want string }{
		{".devcontainer/devcontainer.json", ".devcontainer/devcontainer-lock.json"},
		{".devcontainer.json", ".devcontainer-lock.json"},
		{"configs/../configs/.custom.json", "configs/.devcontainer-lock.json"},
	} {
		if got := LockfilePath(tc.config); got != tc.want {
			t.Fatalf("LockfilePath(%q)=%q want=%q", tc.config, got, tc.want)
		}
	}
}

func TestLockfileWriteModes(t *testing.T) {
	canonical, err := json.MarshalIndent(GenerateLockfile(lockedSample()), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, old, error               string
		exists, create, frozen, writes bool
	}{
		{name: "default missing"},
		{name: "create", create: true, writes: true},
		{name: "default update", exists: true, old: `{"features":{}}`, writes: true},
		{name: "empty marker", exists: true, writes: true},
		{name: "whitespace marker", exists: true, old: " \n", writes: true},
		{name: "frozen missing", frozen: true, error: "Lockfile does not exist."},
		{name: "frozen empty", exists: true, frozen: true, error: "Lockfile does not match."},
		{name: "frozen drift", exists: true, frozen: true, old: `{"features":{}}`, error: "Lockfile does not match."},
		{name: "frozen match", exists: true, frozen: true, old: string(canonical)},
		{name: "both frozen wins", exists: true, create: true, frozen: true, old: string(canonical)},
		{name: "cosmetic whitespace", exists: true, old: string(canonical) + "\n\t "},
		{name: "frozen cosmetic whitespace", exists: true, frozen: true, old: string(canonical) + "\n\t "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "devcontainer.json")
			path := LockfilePath(config)
			if tc.exists {
				if err := os.WriteFile(path, []byte(tc.old), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			state, err := ReadLockfile(config, tc.create, tc.frozen)
			if err != nil {
				t.Fatal(err)
			}
			err = state.Prepare(lockedSample())
			if tc.error != "" {
				if err == nil || err.Error() != tc.error {
					t.Fatalf("err=%v want=%s", err, tc.error)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(path)
			if string(before) != tc.old {
				t.Fatal("Prepare changed disk")
			}
			if err == nil {
				if err := state.Publish(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			got, err := os.ReadFile(path)
			if !tc.exists && !tc.writes {
				if !os.IsNotExist(err) {
					t.Fatalf("unexpected lockfile: %s, %v", got, err)
				}
			} else if tc.writes {
				if string(got) != string(canonical)+"\n" {
					t.Fatalf("content=%s", got)
				}
			} else if string(got) != tc.old {
				t.Fatalf("lockfile modified: %s", got)
			}
		})
	}
}

func TestLockfileInvalidRead(t *testing.T) {
	for _, body := range []string{"{", "[]", `{"features":[]}`} {
		config := filepath.Join(t.TempDir(), "devcontainer.json")
		if err := os.WriteFile(LockfilePath(config), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadLockfile(config, true, false); err == nil {
			t.Fatalf("accepted invalid lockfile %s", body)
		}
	}
	config := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := os.Mkdir(LockfilePath(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLockfile(config, true, false); err == nil {
		t.Fatal("ignored read error")
	}
}

func TestLockfileAtomicPublication(t *testing.T) {
	config := filepath.Join(t.TempDir(), "devcontainer.json")
	path := LockfilePath(config)
	old := `{"features":{}}`
	if err := os.WriteFile(path, []byte(old), 0o640); err != nil {
		t.Fatal(err)
	}
	previous := path + ".previous"
	if err := os.Link(path, previous); err != nil {
		t.Fatal(err)
	}
	state, err := ReadLockfile(config, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Prepare(lockedSample()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := state.Publish(ctx); err != context.Canceled {
		t.Fatalf("cancel err=%v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != old {
		t.Fatal("cancelled run published")
	}
	if err := state.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(previous); string(got) != old {
		t.Fatal("publication modified the original inode")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestLockfileSymlinkPublication(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config", "devcontainer.json")
	if err := os.Mkdir(filepath.Dir(config), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "shared-lock.json")
	if err := os.WriteFile(target, []byte(`{"features":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, LockfilePath(config)); err != nil {
		t.Fatal(err)
	}
	state, err := ReadLockfile(config, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Prepare(lockedSample()); err != nil {
		t.Fatal(err)
	}
	if err := state.Publish(context.Background()); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(LockfilePath(config)); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v %v", info, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(got), "sample:1") {
		t.Fatalf("target=%s err=%v", got, err)
	}
}

func TestLockfilePublicationFailureCleansStaging(t *testing.T) {
	config := filepath.Join(t.TempDir(), "devcontainer.json")
	state, err := ReadLockfile(config, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Prepare(lockedSample()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(LockfilePath(config), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := state.Publish(context.Background()); err == nil {
		t.Fatal("publication over a directory succeeded")
	}
	entries, err := os.ReadDir(filepath.Dir(config))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("failed publication leaked staging: %v", entries)
	}
}
