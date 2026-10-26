// relationships:
//   references: [git, git_global_config, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// globalConfigTerminal opens a pseudo-terminal, a character device whose
// reader receives each segment and then an end of file, and returns the
// path of its reading side. Its input stays queued until a reader takes it.
func globalConfigTerminal(t *testing.T, segments ...string) string {
	t.Helper()
	// A non-blocking descriptor lets Close end a write that no reader takes.
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "/dev/ptmx")
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetUint32(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	name := "/dev/pts/" + strconv.FormatUint(uint64(number), 10)
	slave, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Line input without echo: ^D at the start of a line ends one read.
	termios, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	termios.Lflag = termios.Lflag&^(unix.ECHO|unix.ECHONL) | unix.ICANON
	if err := unix.IoctlSetTermios(int(slave.Fd()), unix.TCSETS, termios); err != nil {
		t.Fatal(err)
	}
	written := make(chan struct{})
	go func() {
		defer close(written)
		for _, segment := range segments {
			if _, err := master.Write([]byte(segment + "\x04")); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		master.Close()
		<-written
		slave.Close()
	})
	return name
}

// globalConfigLargeStream is valid config of more than 1 MiB.
var globalConfigLargeStream = strings.Repeat("# "+strings.Repeat("x", 97)+"\n", 11000) + globalConfigValid

// TestGitRootGlobalConfigStream reads global config files from character
// devices as Git does: through a finite stream of any length, once when Git
// starts and once more after setup, and to the end of the file when a read
// fails.
func TestGitRootGlobalConfigStream(t *testing.T) {
	git, _ := exec.LookPath("git")
	for _, c := range []struct {
		name     string
		relative bool
		segments []string
		ok       bool
	}{
		// The relative path names the device only from the caller folder;
		// Git's second read, from the working tree, finds no file.
		{name: "relative-large", relative: true, segments: []string{globalConfigLargeStream}, ok: true},
		{name: "relative-large-syntax", relative: true, segments: []string{globalConfigLargeStream + globalConfigBroken}},
		// An absolute path names the device in both reads.
		{name: "absolute-large", segments: []string{globalConfigLargeStream, globalConfigLargeStream}, ok: true},
		{name: "absolute-second-syntax", segments: []string{globalConfigValid, globalConfigBroken}},
		{name: "absolute-second-large-syntax", segments: []string{globalConfigValid, globalConfigLargeStream + globalConfigBroken}},
		{name: "read-error", ok: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			folder := mkdir(t, filepath.Join(tree, "app"))
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			// Each probe takes a new stream.
			config := func() string {
				if c.segments == nil {
					// Reading this from offset 0 fails with EIO, in Git's
					// process and in this one.
					return "/proc/self/mem"
				}
				device := globalConfigTerminal(t, c.segments...)
				if !c.relative {
					return device
				}
				link := filepath.Join(folder, "stream.cfg")
				os.Remove(link)
				symlink(t, device, link)
				return "stream.cfg"
			}
			want, source, target, workspaceFolder := "", folder, "/workspaces/app", "/workspaces/app"
			if c.ok {
				want, source, target, workspaceFolder = tree, tree, "/workspaces/tree", "/workspaces/tree/app"
			}
			if git != "" {
				if got := globalSetupGit(t, git, folder, append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base, "GIT_CONFIG_GLOBAL="+config())); got != want {
					t.Fatalf("Git root = %q; want %q", got, want)
				}
			}
			t.Setenv("GIT_CONFIG_GLOBAL", config())
			if got, err := GitRoot(folder); err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
			}
			t.Setenv("GIT_CONFIG_GLOBAL", config())
			resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
			if err != nil || resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Source != source ||
				resolved.WorkspaceMount.Target != target || resolved.WorkspaceFolder != workspaceFolder {
				t.Fatalf("Resolve = %+v %+v, %v; want source %q target %q folder %q", resolved, resolved.WorkspaceMount, err, source, target, workspaceFolder)
			}
		})
	}
}
