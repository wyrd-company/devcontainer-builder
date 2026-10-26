// relationships:
//
//	references: [git_filesystem, git, git_chdir_root_linux_test]
package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
	"golang.org/x/sys/unix"
)

// Git's discovery stops at the first parent on another device unless
// GIT_DISCOVERY_ACROSS_FILESYSTEM is true. The test mounts file systems in
// its own user and mount namespace and compares GitRoot and the Resolve mount
// source with real Git at each boundary.
func TestGitRootFilesystemBoundaryMatchesGit(t *testing.T) {
	runAsRoot(t, "", "TestGitRootFilesystemBoundaryMatchesGitAsRoot")
}

func TestGitRootFilesystemBoundaryMatchesGitAsRoot(t *testing.T) {
	skipUnlessRoot(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		cmd.Env = append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	tmpfs := func(dir string) string {
		t.Helper()
		mkdir(t, dir)
		if err := unix.Mount("tmpfs", dir, "tmpfs", 0, ""); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { unix.Unmount(dir, unix.MNT_DETACH) })
		return dir
	}
	device := func(path string) uint64 {
		t.Helper()
		var stat unix.Stat_t
		if err := unix.Stat(path, &stat); err != nil {
			t.Fatal(err)
		}
		return stat.Dev
	}

	// A working tree whose folder sv is another file system.
	dotgit := mkdir(t, filepath.Join(base, "dotgit"))
	run(dotgit, "init", "-q")
	mkdir(t, filepath.Join(tmpfs(filepath.Join(dotgit, "sv")), "app"))
	plain := mkdir(t, filepath.Join(dotgit, "plain", "app"))
	if err := os.Symlink(filepath.Join(dotgit, "sv", "app"), filepath.Join(dotgit, "into")); err != nil {
		t.Fatal(err)
	}
	// A gitfile working tree, and a bare repository, above a boundary.
	run(base, "init", "-q", "--bare", "admin")
	run(base, "config", "--file", "admin/config", "core.bare", "false")
	writeFile(t, filepath.Join(base, "gitfile", ".git"), "gitdir: ../admin\n")
	mkdir(t, filepath.Join(tmpfs(filepath.Join(base, "gitfile", "sv")), "app"))
	run(base, "init", "-q", "--bare", "bare")
	mkdir(t, filepath.Join(tmpfs(filepath.Join(base, "bare", "sv")), "app"))
	// A repository at the root of another file system.
	outer := mkdir(t, filepath.Join(base, "outer"))
	run(outer, "init", "-q")
	inner := tmpfs(filepath.Join(outer, "sv"))
	run(inner, "init", "-q")
	mkdir(t, filepath.Join(inner, "app"))
	// A caller that is itself a symbolic link into that file system: Git
	// takes the device of the link's target, finds inner one physical step
	// up, and upstream resolves that cdup against the link's path.
	if err := os.Symlink(filepath.Join(inner, "app"), filepath.Join(base, "toinner")); err != nil {
		t.Fatal(err)
	}
	// Two nested boundaries.
	nested := mkdir(t, filepath.Join(base, "nested"))
	run(nested, "init", "-q")
	mkdir(t, filepath.Join(tmpfs(filepath.Join(tmpfs(filepath.Join(nested, "s1")), "s2")), "app"))
	// A linked worktree on another file system; its gitdir is on the first.
	mainRepo := mkdir(t, filepath.Join(base, "main"))
	run(mainRepo, "init", "-q", "-b", "main")
	run(mainRepo, "commit", "-q", "--allow-empty", "-m", "initial")
	run(mainRepo, "worktree", "add", "-q", filepath.Join(tmpfs(filepath.Join(base, "wtfs")), "wt"), "-b", "topic")
	mkdir(t, filepath.Join(base, "wtfs", "wt", "app"))
	// A bind mount of the same file system keeps its device: no boundary.
	bound := mkdir(t, filepath.Join(base, "bound"))
	run(bound, "init", "-q")
	mkdir(t, filepath.Join(base, "elsewhere", "x"))
	bindMount(t, filepath.Join(base, "elsewhere"), mkdir(t, filepath.Join(bound, "b")))
	// A folder back on the caller's device below another file system still
	// stops at that file system.
	back := mkdir(t, filepath.Join(base, "back"))
	run(back, "init", "-q")
	mkdir(t, filepath.Join(base, "return", "app"))
	bindMount(t, filepath.Join(base, "return"), mkdir(t, filepath.Join(tmpfs(filepath.Join(back, "mid")), "x")))

	if device(filepath.Join(dotgit, "sv")) == device(dotgit) || device(filepath.Join(bound, "b", "x")) != device(bound) ||
		device(filepath.Join(back, "mid", "x")) != device(back) || device(filepath.Join(back, "mid")) == device(back) {
		t.Fatal("fixture devices do not form the intended boundaries")
	}
	stops := []*string{nil, ptr(""), ptr("0"), ptr("false"), ptr("maybe")}
	all := append([]*string{ptr("1"), ptr("true")}, stops...)
	for _, c := range []struct {
		folder string
		values []*string
	}{
		{filepath.Join(dotgit, "sv"), all},
		{filepath.Join(dotgit, "sv", "app"), all},
		{plain, all},
		// A symbolic link into sv has the device of sv. Git walks the
		// physical parents, so it meets the boundary at dotgit, and upstream
		// resolves the cdup against the link's path.
		{filepath.Join(dotgit, "into"), all},
		{filepath.Join(base, "gitfile", "sv", "app"), all},
		{filepath.Join(base, "bare", "sv", "app"), all},
		{filepath.Join(inner, "app"), all},
		{filepath.Join(base, "toinner"), all},
		{filepath.Join(nested, "s1", "s2", "app"), all},
		{filepath.Join(base, "wtfs", "wt", "app"), all},
		{filepath.Join(bound, "b", "x"), all},
		{filepath.Join(back, "mid", "x", "app"), all},
	} {
		for _, value := range c.values {
			t.Run(strings.TrimPrefix(c.folder, base)+"/"+discoveryName(value), func(t *testing.T) {
				requireDiscoveryMatchesGit(t, git, base, c.folder, value)
			})
		}
	}
}

// Git checks the device of each physical parent, not of the caller's lexical
// parents. Here the caller's path leaves the file system of its physical
// folder at a lexical parent that Git never visits: Git finds the repository
// at the root of that file system, two physical steps up, and upstream
// resolves that cdup against the caller's path to the lexical parent.
func TestGitRootSymlinkFilesystemDeviceMatchesGit(t *testing.T) {
	runAsRoot(t, "", "TestGitRootSymlinkFilesystemDeviceMatchesGitAsRoot")
}

func TestGitRootSymlinkFilesystemDeviceMatchesGitAsRoot(t *testing.T) {
	skipUnlessRoot(t)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lexical := mkdir(t, filepath.Join(base, "lexical"))
	physical := mkdir(t, filepath.Join(lexical, "physical"))
	if err := unix.Mount("tmpfs", physical, "tmpfs", 0, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Unmount(physical, unix.MNT_DETACH) })
	for _, dir := range []string{lexical, physical} {
		cmd := exec.Command(git, "init", "-q", dir)
		cmd.Env = testgit.Env()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init %s: %v\n%s", dir, err, out)
		}
	}
	mkdir(t, filepath.Join(physical, "sub", "app"))
	if err := os.Symlink("physical/sub", filepath.Join(lexical, "link")); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(lexical, "link", "app")
	var lexicalStat, physicalStat unix.Stat_t
	if unix.Stat(lexical, &lexicalStat) != nil || unix.Stat(physical, &physicalStat) != nil || lexicalStat.Dev == physicalStat.Dev {
		t.Fatal("fixture devices do not form the intended boundary")
	}
	for _, value := range []*string{nil, ptr(""), ptr("0"), ptr("false"), ptr("1"), ptr("true")} {
		t.Run(discoveryName(value), func(t *testing.T) {
			requireDiscoveryMatchesGit(t, git, base, folder, value)
			if got, _ := GitRoot(folder); got != lexical {
				t.Fatalf("GitRoot(%s) = %q; want the lexical parent %q", folder, got, lexical)
			}
			resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
			if err != nil || resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Target != "/workspaces/lexical" ||
				resolved.WorkspaceFolder != "/workspaces/lexical/link/app" {
				t.Fatalf("Resolve(%s) = %+v, %v; want target /workspaces/lexical, folder /workspaces/lexical/link/app", folder, resolved, err)
			}
		})
	}
}
