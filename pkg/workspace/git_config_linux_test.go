// relationships:
//   references: [git_config, git_dir_linux_test]

package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
	"golang.org/x/sys/unix"
)

// Git blocks reading a config FIFO, so this has no Git oracle. Native discovery
// rejects the special file without reading it. Reuse the established test-only
// subprocess bound so a regression cannot leave a blocked producer.
func TestGitConfigFIFORejectsWithoutReading(t *testing.T) {
	for _, name := range []string{"config", "config.worktree"} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			makeRepo(t, repo)
			folder := mkdir(t, filepath.Join(repo, "app"))
			admin := filepath.Join(repo, ".git")
			if name == "config.worktree" {
				writeFile(t, filepath.Join(admin, "config"), "[core]\n\trepositoryformatversion = 1\n[extensions]\n\tworktreeConfig\n")
			}
			if err := unix.Mkfifo(filepath.Join(admin, name), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGitDirFIFOHelper$")
			cmd.Dir = folder
			cmd.Env = append(testgit.Env(), "DCB_TEST_GIT_DIR_FIFO_HELPER=1")
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("native blocked on %s FIFO: %v; %s", name, ctx.Err(), out)
			}
			if err != nil {
				t.Fatalf("native = %s, %v", out, err)
			}
		})
	}
}
