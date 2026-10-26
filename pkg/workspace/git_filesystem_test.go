// relationships:
//
//	references: [git_filesystem, git, ../../reference/cli/src/spec-common/git]
package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

const acrossFilesystem = "GIT_DISCOVERY_ACROSS_FILESYSTEM"

// discoveryValues are GIT_DISCOVERY_ACROSS_FILESYSTEM settings: nil is unset.
// Git reads the variable as a boolean and rejects other values.
var discoveryValues = []*string{nil, ptr(""), ptr("0"), ptr("false"), ptr("off"), ptr("1"), ptr("true"), ptr("yes"), ptr("2"), ptr(" 1"), ptr("0x10"), ptr("maybe"), ptr("tru")}

func ptr(s string) *string { return &s }

func discoveryName(value *string) string {
	if value == nil {
		return "unset"
	}
	return "value=" + strings.ReplaceAll(*value, " ", "_")
}

// requireDiscoveryMatchesGit compares GitRoot and the Resolve mount source
// for folder with upstream's probe, `git rev-parse --show-cdup`, under the
// ceiling and the GIT_DISCOVERY_ACROSS_FILESYSTEM value, plus env.
func requireDiscoveryMatchesGit(t *testing.T, git, ceiling, folder string, value *string, env ...string) {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", ceiling)
	t.Setenv(acrossFilesystem, "")
	gitEnv := append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+ceiling)
	if value == nil {
		os.Unsetenv(acrossFilesystem)
	} else {
		t.Setenv(acrossFilesystem, *value)
		gitEnv = append(gitEnv, acrossFilesystem+"="+*value)
	}
	for _, entry := range env {
		name, setting, _ := strings.Cut(entry, "=")
		t.Setenv(name, setting)
	}
	cmd := exec.Command(git, "rev-parse", "--show-cdup")
	cmd.Dir = folder
	cmd.Env = append(gitEnv, env...)
	out, probeErr := cmd.CombinedOutput()
	want := ""
	if probeErr == nil {
		want = filepath.Clean(filepath.Join(folder, strings.TrimSpace(string(out))))
	}
	got, err := GitRoot(folder)
	if err != nil || got != want {
		t.Fatalf("GitRoot(%s) = %q, %v; git says %q (%v: %s)", folder, got, err, want, probeErr, out)
	}
	source := want
	if source == "" {
		// Upstream mounts the caller folder when the probe fails.
		source = folder
	}
	resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
	if err != nil || resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Source != source {
		t.Fatalf("Resolve(%s) = %+v, mount %+v, %v; want source %q", folder, resolved, resolved.WorkspaceMount, err, source)
	}
}

// Git reads GIT_DISCOVERY_ACROSS_FILESYSTEM before it tests any folder, so
// a value it rejects gives no root even without a file system boundary.
// GIT_DIR skips discovery, and the variable with it.
func TestGitRootDiscoveryAcrossFilesystemValuesMatchGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := mkdir(t, filepath.Join(base, "repo"))
	makeRepo(t, repo)
	app := mkdir(t, filepath.Join(repo, "app"))
	admin := filepath.Join(base, "admin")
	makeRepo(t, admin)
	tree := mkdir(t, filepath.Join(base, "tree"))
	writeFile(t, filepath.Join(tree, ".git"), "gitdir: ../admin/.git\n")
	treeApp := mkdir(t, filepath.Join(tree, "app"))
	bare := filepath.Join(base, "bare")
	makeRepo(t, bare)
	bare = filepath.Join(bare, ".git")
	plain := mkdir(t, filepath.Join(base, "plain"))
	for _, folder := range []string{repo, app, tree, treeApp, bare, plain} {
		for _, value := range discoveryValues {
			t.Run(strings.TrimPrefix(folder, base)+"/"+discoveryName(value), func(t *testing.T) {
				requireDiscoveryMatchesGit(t, git, base, folder, value)
			})
		}
	}
	for _, value := range []*string{nil, ptr("maybe")} {
		t.Run("GIT_DIR/"+discoveryName(value), func(t *testing.T) {
			requireDiscoveryMatchesGit(t, git, base, app, value, "GIT_DIR="+filepath.Join(repo, ".git"))
		})
	}
}

// Git stats the caller folder before discovery; a folder it cannot stat
// cannot start the probe either, and upstream then mounts the caller folder.
// Git cannot change to a missing folder even when it may cross file systems.
func TestGitRootUnreadableFolderDevice(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	makeRepo(t, repo)
	missing := filepath.Join(repo, "missing", "app")
	t.Setenv(acrossFilesystem, "")
	os.Unsetenv(acrossFilesystem)
	if got, err := GitRoot(missing); got != "" || err != nil {
		t.Fatalf("GitRoot(%s) = %q, %v; want no root", missing, got, err)
	}
	t.Setenv(acrossFilesystem, "true")
	if got, err := GitRoot(missing); got != "" || err != nil {
		t.Fatalf("GitRoot(%s) across file systems = %q, %v; want no root", missing, got, err)
	}
	os.Unsetenv(acrossFilesystem)
	present := mkdir(t, filepath.Join(repo, "present", "app"))
	if got, err := GitRoot(present); got != repo || err != nil {
		t.Fatalf("GitRoot(%s) = %q, %v; want %q", present, got, err, repo)
	}
}
