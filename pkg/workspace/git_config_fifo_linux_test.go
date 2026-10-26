// relationships:
//   references: [git, git_global_config, git_system_config, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// serveConfigFIFO starts a producer for the FIFO at path, as a shell writer
// such as `printf ... > fifo` does for each read. Each reader that opens the
// FIFO takes the next segment and then an end of file; a reader after the last
// segment takes only an end of file. The returned function stops the producer
// and reports the number of readers that it served.
func serveConfigFIFO(t *testing.T, path string, segments ...string) func() int {
	t.Helper()
	watch, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unix.InotifyAddWatch(watch, path, unix.IN_CLOSE_NOWRITE); err != nil {
		unix.Close(watch)
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	wait := func() bool {
		select {
		case <-stop:
			return false
		case <-time.After(time.Millisecond):
			return true
		}
	}
	served := 0
	var failure error
	go func() {
		defer close(done)
		for {
			// A non-blocking open finds a reader without waiting for one,
			// so that the producer can stop.
			fd, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if errors.Is(err, unix.ENXIO) {
				if !wait() {
					return
				}
				continue
			}
			if err != nil {
				failure = err
				return
			}
			writer := os.NewFile(uintptr(fd), path)
			if served < len(segments) {
				// A reader that stops at a syntax error closes the FIFO
				// before it takes the rest.
				if _, err := writer.WriteString(segments[served]); err != nil && !errors.Is(err, unix.EPIPE) {
					failure = err
				}
			}
			writer.Close()
			served++
			// The next open must find the next reader, so wait until this
			// reader closes the FIFO.
			for closed := false; !closed; {
				event := make([]byte, unix.SizeofInotifyEvent)
				n, err := unix.Read(watch, event)
				switch {
				case err == nil && n == len(event):
					closed = binary.NativeEndian.Uint32(event[4:8])&unix.IN_CLOSE_NOWRITE != 0
				case errors.Is(err, unix.EAGAIN):
					if !wait() {
						return
					}
				default:
					failure = err
					return
				}
			}
		}
	}()
	var once sync.Once
	finish := func() int {
		once.Do(func() {
			close(stop)
			<-done
			unix.Close(watch)
			if failure != nil {
				t.Errorf("FIFO producer: %v", failure)
			}
		})
		return served
	}
	t.Cleanup(func() { finish() })
	return finish
}

// configFIFOVars names fifo as the config file that variable selects, with no
// other system or global file.
func configFIFOVars(variable, fifo string) map[string]*string {
	if variable == "GIT_CONFIG_SYSTEM" {
		return map[string]*string{"GIT_CONFIG_NOSYSTEM": nil, "GIT_CONFIG_SYSTEM": &fifo, "GIT_CONFIG_GLOBAL": globalValue(os.DevNull)}
	}
	return map[string]*string{"GIT_CONFIG_NOSYSTEM": globalValue("1"), "GIT_CONFIG_SYSTEM": nil, "GIT_CONFIG_GLOBAL": &fifo}
}

// configFIFOFixture makes a repository with a caller folder inside it and a
// FIFO that variable names. It returns the tree, the folder, the FIFO and the
// environment for Git.
func configFIFOFixture(t *testing.T, variable string) (tree, folder, fifo string, env []string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree = filepath.Join(base, "tree")
	makeRepo(t, tree)
	folder = mkdir(t, filepath.Join(tree, "app"))
	fifo = filepath.Join(base, "config.fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	vars := configFIFOVars(variable, fifo)
	vars["GIT_CEILING_DIRECTORIES"] = &base
	return tree, folder, fifo, applyGlobalEnv(t, testgit.Env(), vars)
}

// configFIFOResolved returns the Git root that Resolve mounts from folder, ""
// when it mounts folder, or a description of another result. It does not stop
// the test, so that a probe can run it in another goroutine.
func configFIFOResolved(tree, folder string) string {
	resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
	switch {
	case err != nil || resolved.WorkspaceMount == nil:
	case resolved.WorkspaceMount.Source == tree && resolved.WorkspaceMount.Target == "/workspaces/tree" && resolved.WorkspaceFolder == "/workspaces/tree/app":
		return tree
	case resolved.WorkspaceMount.Source == folder && resolved.WorkspaceMount.Target == "/workspaces/app" && resolved.WorkspaceFolder == "/workspaces/app":
		return ""
	}
	return fmt.Sprintf("Resolve = %+v %+v, %v", resolved, resolved.WorkspaceMount, err)
}

// TestGitRootConfigFIFOStream reads a system or global config file that is a
// FIFO as Git does: Git opens it once when it starts and once more after
// setup, and parses each finite stream to its end of file. A syntax error stops
// the probe in the read that takes it. Native discovery and the host Git must
// both give each case's root, and take the same number of streams.
func TestGitRootConfigFIFOStream(t *testing.T) {
	git, _ := exec.LookPath("git")
	for _, variable := range []string{"GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL"} {
		for _, c := range []struct {
			name     string
			segments []string
			ok       bool
			reads    int
		}{
			{name: "valid", segments: []string{globalConfigValid, globalConfigValid}, ok: true, reads: 2},
			{name: "empty", ok: true, reads: 2},
			{name: "syntax", segments: []string{globalConfigBroken}, reads: 1},
			{name: "second-syntax", segments: []string{globalConfigValid, globalConfigBroken}, reads: 2},
			{name: "large", segments: []string{globalConfigLargeStream, globalConfigLargeStream}, ok: true, reads: 2},
			{name: "large-syntax", segments: []string{globalConfigLargeStream + globalConfigBroken}, reads: 1},
		} {
			t.Run(variable+"/"+c.name, func(t *testing.T) {
				tree, folder, fifo, env := configFIFOFixture(t, variable)
				want := ""
				if c.ok {
					want = tree
				}
				for _, route := range []struct {
					name  string
					probe func() string
				}{
					{"git", func() string { return globalSetupGit(t, git, folder, env) }},
					{"GitRoot", func() string {
						got, err := GitRoot(folder)
						if err != nil {
							t.Fatalf("GitRoot: %v", err)
						}
						return got
					}},
					{"Resolve", func() string { return configFIFOResolved(tree, folder) }},
				} {
					if route.name == "git" && git == "" {
						continue
					}
					finish := serveConfigFIFO(t, fifo, c.segments...)
					got := route.probe()
					if reads := finish(); got != want || reads != c.reads {
						t.Fatalf("%s root = %q after %d reads; want %q after %d", route.name, got, reads, want, c.reads)
					}
				}
			})
		}
	}
}

// configFIFOWaits asserts that a probe waits while no writer opens the FIFO
// that variable names, as Git does, with no limit, and then takes both reads
// from a producer that starts later.
func configFIFOWaits(t *testing.T, variable string) {
	git, _ := exec.LookPath("git")
	tree, folder, fifo, env := configFIFOFixture(t, variable)
	for _, route := range []struct {
		name  string
		probe func() string
	}{
		{"git", func() string { return globalSetupGit(t, git, folder, env) }},
		{"GitRoot", func() string {
			got, _ := GitRoot(folder)
			return got
		}},
		{"Resolve", func() string { return configFIFOResolved(tree, folder) }},
	} {
		if route.name == "git" && git == "" {
			continue
		}
		result := make(chan string, 1)
		go func() { result <- route.probe() }()
		select {
		case got := <-result:
			t.Fatalf("%s root = %q before a writer opened the FIFO", route.name, got)
		case <-time.After(200 * time.Millisecond):
		}
		finish := serveConfigFIFO(t, fifo, globalConfigValid, globalConfigValid)
		select {
		case got := <-result:
			if reads := finish(); got != tree || reads != 2 {
				t.Fatalf("%s root = %q after %d reads; want %q after 2", route.name, got, reads, tree)
			}
		case <-time.After(10 * time.Second):
			// A test bound only; the producer keeps serving until cleanup.
			t.Fatalf("%s did not finish after the producer started", route.name)
		}
	}
}

// Git opens a FIFO named as the system config file and waits for a writer.
func TestGitRootSystemConfigFIFO(t *testing.T) {
	configFIFOWaits(t, "GIT_CONFIG_SYSTEM")
}

// Git opens a FIFO named as a global config file and waits for a writer.
func TestGitRootGlobalConfigFIFO(t *testing.T) {
	configFIFOWaits(t, "GIT_CONFIG_GLOBAL")
}
