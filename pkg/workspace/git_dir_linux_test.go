// relationships:
//   references: [git, git_dir_test]

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

func TestGitDirFIFORejectsWithoutReading(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	cmd := exec.Command(gitBin, "init", "-q", repo)
	cmd.Env = testgit.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	folder := mkdir(t, filepath.Join(repo, "app"))
	fifo := filepath.Join(repo, "pipe")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	env := append(testgit.Env(), "GIT_DIR="+fifo, "DCB_TEST_GIT_DIR_FIFO_HELPER=1")
	// Review measured a blocked FIFO at three seconds. Bound only the test
	// subprocess so a missing node-type guard cannot leave an owned producer.
	run := func(bin string, args ...string) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = folder
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if ctx.Err() != nil {
			t.Fatalf("%s blocked on FIFO: %v; %s", bin, ctx.Err(), out)
		}
		return out, err
	}
	out, err := run(gitBin, "rev-parse", "--show-cdup")
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 128 {
		t.Fatalf("Git = %s, %v; want exit128", out, err)
	}
	if out, err := run(os.Args[0], "-test.run=^TestGitDirFIFOHelper$"); err != nil {
		t.Fatalf("native = %s, %v", out, err)
	}
}

func TestGitDirFIFOHelper(t *testing.T) {
	if os.Getenv("DCB_TEST_GIT_DIR_FIFO_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	folder, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := GitRoot(folder)
	if root != "" || err != nil {
		t.Fatalf("GitRoot = %q, %v; want no root", root, err)
	}
}
