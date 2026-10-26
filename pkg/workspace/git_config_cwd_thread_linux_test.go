//go:build gitthreadcwd

// relationships:
//   references: [git_config_cwd_thread, git_chdir_thread_linux]

package workspace

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestGitConfigCwdThread reads a config file by a relative name from a folder
// that the process can only search. The folder's own thread must look the
// name up, and end when the read closes the folder.
func TestGitConfigCwdThread(t *testing.T) {
	folder := t.TempDir()
	writeFile(t, filepath.Join(folder, "global.cfg"), globalConfigValid)
	searchOnlyFolder(t, folder)
	for range 3 {
		cwd := newGitConfigCwd(folder)
		if err := cwd.access("global.cfg"); err != nil {
			t.Fatalf("access: %v", err)
		}
		file, err := cwd.open("global.cfg")
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		file.Close()
		if cwd.calls == nil {
			t.Fatal("the relative name was not looked up from the folder's thread")
		}
		cwd.close()
	}
	// An ended goroutine leaves the scheduler at its next turn.
	for turn := 0; configCwdThreads() > 0; turn++ {
		if turn == 1000 {
			t.Fatalf("%d folder threads still run after close", configCwdThreads())
		}
		runtime.Gosched()
	}
}

// configCwdThreads counts the goroutines of folder threads.
func configCwdThreads() int {
	buf := make([]byte, 1<<20)
	return strings.Count(string(buf[:runtime.Stack(buf, true)]), "workspace.gitConfigCwdThread.func1(")
}
