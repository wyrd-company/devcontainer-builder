// relationships:
//   references: [git, git_chdir_linux, git_chdir_credentials_linux_test]

package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// credentialsEnv names the credential state that
// TestGitRootCallerCredentialsAsRoot gives its process.
const credentialsEnv = "DCB_WORKSPACE_TEST_CREDENTIALS"

// credentialState is a set of credentials a root process can give itself,
// and whether this process and a Git it starts can read files that only
// capabilities allow.
type credentialState struct {
	securebits     uintptr
	clearEffective bool
	// clearPermitted also clears the inheritable set; the bounding set
	// stays full.
	clearPermitted bool
	native         bool
	git            bool
	// gitNoNewPrivs is git for a process with no_new_privs, which keeps
	// a started Git's permitted set within the process's own.
	gitNoNewPrivs bool
}

// credentialStates are the states the tests give a root process. Git,
// started as root without SECBIT_NOROOT, regains its capabilities, from the
// bounding set unless no_new_privs is set; with SECBIT_NOROOT, Git starts
// without them.
var credentialStates = map[string]credentialState{
	"retained":          {native: true, git: true, gitNoNewPrivs: true},
	"retained-noroot":   {securebits: secbitNoroot | secbitNorootLocked, native: true},
	"dropped":           {clearEffective: true, git: true, gitNoNewPrivs: true},
	"dropped-noroot":    {securebits: secbitNoroot | secbitNorootLocked, clearEffective: true},
	"permitted-cleared": {clearEffective: true, clearPermitted: true, git: true},
}

// Native discovery reads with the credentials the process has. It does not
// regain capabilities that the process dropped, and it keeps capabilities
// that a Git it starts would lose. Where starting Git changes the
// credentials, Git's result differs; where it does not, the results agree.
// The thread tier's seccomp filter sets no_new_privs.
func TestGitRootCallerCredentials(t *testing.T) {
	for _, name := range []string{"retained", "retained-noroot", "dropped", "dropped-noroot", "permitted-cleared"} {
		t.Run(name, func(t *testing.T) {
			for _, without := range []string{"", "proc", "proc thread"} {
				t.Run("without-"+strings.ReplaceAll(without, " ", "-"), func(t *testing.T) {
					t.Setenv(credentialsEnv, name)
					runAsRoot(t, without, "TestGitRootCallerCredentialsAsRoot")
				})
			}
		})
	}
}

// credentialLayout is a repository whose discovery needs a capability to
// read the locked path.
type credentialLayout struct {
	name, folder, root, locked string
}

func TestGitRootCallerCredentialsAsRoot(t *testing.T) {
	skipUnlessRoot(t)
	state, ok := credentialStates[os.Getenv(credentialsEnv)]
	if !ok {
		t.Fatalf("%s = %q; want a known state", credentialsEnv, os.Getenv(credentialsEnv))
	}
	// Go documents that a binary that uses cgo, as a race build does,
	// cannot make a system call on every thread.
	if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_GETPID, 0, 0, 0); errno == unix.ENOTSUP {
		t.Skip("syscall.AllThreadsSyscall is unavailable in this binary")
	}
	layouts := credentialLayouts(t)
	before := setCredentials(t, state)
	git, gitErr := exec.LookPath("git")
	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			want := layout.root
			if layout.locked != "" && !state.native {
				want = ""
			}
			if got, err := GitRoot(layout.folder); got != want || err != nil {
				t.Errorf("GitRoot = %q, %v; want %q", got, err, want)
			}
			mount := want
			if mount == "" {
				mount = layout.folder
			}
			resolved, err := Resolve(layout.folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
			if err != nil || resolved.WorkspaceMount.Source != mount {
				t.Errorf("Resolve mount = %q, %v; want %q", resolved.WorkspaceMount.Source, err, mount)
			}
			if after := processCredentials(t); after != before {
				t.Errorf("credentials after discovery = %+v; want %+v", after, before)
			}
			if gitErr != nil {
				t.Skipf("git: %v", gitErr)
			}
			regains := state.git
			if before.noNewPrivs {
				regains = state.gitNoNewPrivs
			}
			gitWant := layout.root
			if layout.locked != "" && !regains {
				gitWant = ""
			}
			requireGitRoot(t, git, layout.folder, gitWant)
		})
	}
}

// credentialLayouts makes, with full capabilities, a repository with no
// locked path and one for each path that discovery reads.
func credentialLayouts(t *testing.T) []credentialLayout {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	var layouts []credentialLayout
	for _, name := range []string{"control", "git-folder", "config", "head", "core-worktree"} {
		dir := mkdir(t, filepath.Join(base, name))
		tree := filepath.Join(dir, "tree")
		makeRepo(t, tree)
		layout := credentialLayout{name: name, folder: mkdir(t, filepath.Join(tree, "app")), root: tree}
		switch name {
		case "git-folder":
			layout.locked = filepath.Join(tree, ".git")
		case "config":
			layout.locked = filepath.Join(tree, ".git", "config")
			writeFile(t, layout.locked, "[core]\n\trepositoryformatversion = 0\n")
		case "head":
			layout.locked = filepath.Join(tree, ".git", "HEAD")
		case "core-worktree":
			layout.locked = mkdir(t, filepath.Join(dir, "locked"))
			layout.root = layout.locked
			writeFile(t, filepath.Join(tree, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n\tworktree = ../../locked\n")
		}
		if layout.locked != "" {
			if err := os.Chmod(layout.locked, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(layout.locked, 0o700) })
		}
		layouts = append(layouts, layout)
	}
	return layouts
}

// credentials are the capability sets, securebits and no_new_privs of the
// process.
type credentials struct {
	effective, permitted, inheritable [2]uint32
	securebits                        int
	noNewPrivs                        bool
}

func processCredentials(t *testing.T) credentials {
	t.Helper()
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&header, &data[0]); err != nil {
		t.Fatal(err)
	}
	securebits, err := unix.PrctlRetInt(unix.PR_GET_SECUREBITS, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	noNewPrivs, err := unix.PrctlRetInt(unix.PR_GET_NO_NEW_PRIVS, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return credentials{
		effective:   [2]uint32{data[0].Effective, data[1].Effective},
		permitted:   [2]uint32{data[0].Permitted, data[1].Permitted},
		inheritable: [2]uint32{data[0].Inheritable, data[1].Inheritable},
		securebits:  securebits,
		noNewPrivs:  noNewPrivs == 1,
	}
}

// setCredentials gives every thread of the process state's credentials,
// with no ambient capabilities, and returns them.
func setCredentials(t *testing.T, state credentialState) credentials {
	t.Helper()
	if state.securebits != 0 {
		allThreads(t, unix.SYS_PRCTL, unix.PR_SET_SECUREBITS, state.securebits, 0)
	}
	allThreads(t, unix.SYS_PRCTL, unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0)
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
	var data [2]unix.CapUserData
	if err := unix.Capget(&header, &data[0]); err != nil {
		t.Fatal(err)
	}
	for i := range data {
		if state.clearEffective {
			data[i].Effective = 0
		}
		if state.clearPermitted {
			data[i].Permitted, data[i].Inheritable = 0, 0
		}
	}
	allThreads(t, unix.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0)
	runtime.KeepAlive(&header)
	runtime.KeepAlive(&data)
	got := processCredentials(t)
	full := got.effective != [2]uint32{}
	if full == state.clearEffective || (got.permitted == [2]uint32{}) != state.clearPermitted || got.securebits != int(state.securebits) {
		t.Fatalf("credentials = %+v; want state %+v", got, state)
	}
	return got
}

// requireGitRoot checks the Git root that git rev-parse --show-cdup gives
// from folder, as upstream reads it, or that Git fails when want is empty.
func requireGitRoot(t *testing.T, git, folder, want string) {
	t.Helper()
	cmd := exec.Command(git, "rev-parse", "--show-cdup")
	cmd.Dir = folder
	out, err := cmd.CombinedOutput()
	// Upstream resolves the trimmed cdup from the workspace folder.
	cdup := strings.TrimSpace(string(out))
	if !filepath.IsAbs(cdup) {
		cdup = filepath.Join(folder, cdup)
	}
	var exit *exec.ExitError
	switch {
	case want == "" && (!errors.As(err, &exit) || exit.ExitCode() != 128):
		t.Errorf("git rev-parse = %v, %q; want exit 128", err, out)
	case want != "" && (err != nil || filepath.Clean(cdup) != want):
		t.Errorf("git rev-parse = %v, %q; want %q", err, out, want)
	}
}
