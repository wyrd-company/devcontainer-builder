// relationships:
//
//	references: [lockfile, registry_test]
package features

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

func TestLockfileCreationRespectsUmask(t *testing.T) {
	if os.Getenv("DCB_LOCKFILE_UMASK_HELPER") == "1" {
		config := filepath.Join(os.Getenv("DCB_LOCKFILE_UMASK_DIR"), "devcontainer.json")
		state, err := ReadLockfile(config, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := state.Prepare(lockedSample()); err != nil {
			t.Fatal(err)
		}
		if err := state.Publish(context.Background()); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(LockfilePath(config))
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%03o", info.Mode().Perm()); got != os.Getenv("DCB_LOCKFILE_UMASK_EXPECTED") {
			t.Fatalf("mode=%s", got)
		}
		return
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("shell is unavailable")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ mask, mode string }{{"077", "600"}, {"002", "664"}} {
		t.Run(tc.mask, func(t *testing.T) {
			cmd := exec.Command(sh, "-c", `umask "$1"; exec "$2" -test.run '^TestLockfileCreationRespectsUmask$'`, "sh", tc.mask, binary)
			cmd.Env = append(os.Environ(), "DCB_LOCKFILE_UMASK_HELPER=1", "DCB_LOCKFILE_UMASK_DIR="+t.TempDir(), "DCB_LOCKFILE_UMASK_EXPECTED="+tc.mode)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("umask %s: %v\n%s", tc.mask, err, output)
			}
		})
	}
}

func TestLockfileDanglingSymlinkPublication(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "devcontainer.json")
	target := filepath.Join(root, "target.json")
	if err := os.Symlink("target.json", LockfilePath(config)); err != nil {
		t.Fatal(err)
	}
	state, err := ReadLockfile(config, true, false)
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
	if data, err := os.ReadFile(target); err != nil || !strings.Contains(string(data), "sample:1") {
		t.Fatalf("target=%s err=%v", data, err)
	}
}

func TestLockfileSiblingCollisionPreservesOtherWriter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".devcontainer-lock-existing")
	if err := os.WriteFile(path, []byte("other generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f, err := createLockfileSibling(dir, func() string {
		calls++
		if calls == 1 {
			return "existing"
		}
		return "next"
	})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("our generation")); err != nil {
		t.Fatal(err)
	}
	if f.Name() != filepath.Join(dir, ".devcontainer-lock-next") {
		t.Fatalf("adopted another writer's sibling: %s", f.Name())
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "other generation" {
		t.Fatalf("other generation=%q err=%v", data, err)
	}
}

func TestLockfileInterleavedPublication(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("held_cancelled=", fail), func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "devcontainer.json")
			prepare := func(version string) *LockfileState {
				t.Helper()
				state, err := ReadLockfile(config, true, false)
				if err != nil {
					t.Fatal(err)
				}
				candidate := lockedSample()
				candidate[0].Metadata.Version = version
				if err := state.Prepare(candidate); err != nil {
					t.Fatal(err)
				}
				return state
			}
			held := prepare("1.0.0")
			release := make(chan struct{})
			result := make(chan error)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				<-release
				result <- held.Publish(ctx)
			}()
			other := prepare("2.0.0")
			err := other.Publish(context.Background())
			if fail || err != nil {
				cancel()
			}
			close(release)
			heldErr := <-result
			if err != nil {
				t.Fatal(err)
			}
			if fail {
				if heldErr != context.Canceled {
					t.Fatalf("held error=%v", heldErr)
				}
				data, err := os.ReadFile(LockfilePath(config))
				if err != nil || !strings.Contains(string(data), "2.0.0") {
					t.Fatalf("cancelled writer changed publication: %s %v", data, err)
				}
				if err := held.Publish(context.Background()); err != nil {
					t.Fatalf("retry: %v", err)
				}
			} else if heldErr != nil {
				t.Fatal(heldErr)
			}
			data, err := os.ReadFile(LockfilePath(config))
			if err != nil || !strings.Contains(string(data), "1.0.0") {
				t.Fatalf("successful last writer: %s %v", data, err)
			}
		})
	}
}

func TestLockfileDirectorySymlinkRetainsOrdinaryWriteFailure(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "devcontainer.json")
	path := LockfilePath(config)
	if err := os.Symlink("missing/", path); err != nil {
		t.Fatal(err)
	}
	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o666); err == nil {
		f.Close()
		t.Fatal("ordinary write unexpectedly succeeded")
	}
	state, err := ReadLockfile(config, true, false)
	if err != nil {
		return
	}
	if err := state.Prepare(lockedSample()); err != nil {
		t.Fatal(err)
	}
	if err := state.Publish(context.Background()); err == nil {
		t.Fatal("publication through a missing directory target succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Fatalf("unexpected target created: %v", err)
	}
}
