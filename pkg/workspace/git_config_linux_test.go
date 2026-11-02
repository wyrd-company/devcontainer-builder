// relationships:
//   references: [git_config, git_config_fifo_linux_test, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
	"golang.org/x/sys/unix"
)

// repositoryFIFOFixture puts a FIFO at the format check's common config or
// config.worktree, on each discovery route. Each probe gets its own producer.
func repositoryFIFOFixture(t *testing.T, git, route, name string, enabled bool) (tree, folder, fifo string, env []string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree = filepath.Join(base, "tree")
	env = testgit.Env()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run(base, "init", "-q", "-b", "main", tree)
	admin := filepath.Join(tree, ".git")
	worktree := admin
	vars := map[string]*string{"GIT_CEILING_DIRECTORIES": &base, "GIT_CONFIG_GLOBAL": globalValue(os.DevNull), "GIT_CONFIG_NOSYSTEM": globalValue("1")}
	folder = filepath.Join(tree, "app")
	switch route {
	case "gitfile":
		moved := filepath.Join(base, "admin")
		if err := os.Rename(admin, moved); err != nil {
			t.Fatal(err)
		}
		writeFile(t, admin, "gitdir: "+moved+"\n")
		admin, worktree = moved, moved
	case "explicit":
		vars["GIT_DIR"] = &admin
	case "self":
		folder = admin
	case "linked":
		run(tree, "commit", "-q", "--allow-empty", "-m", "initial")
		linked := filepath.Join(base, "linked")
		run(tree, "worktree", "add", "-q", linked)
		tree = linked
		folder = filepath.Join(tree, "app")
		worktree = filepath.Join(admin, "worktrees", "linked")
	case "common-env":
		vars["GIT_COMMON_DIR"] = &admin
	}
	mkdir(t, folder)
	config := filepath.Join(admin, "config")
	if name == "config" {
		if err := os.Remove(config); err != nil {
			t.Fatal(err)
		}
		fifo = config
	} else {
		common := "[core]\nrepositoryformatversion=1\n"
		if enabled {
			common += "[extensions]\nworktreeConfig=true\n"
		}
		writeFile(t, config, common)
		fifo = filepath.Join(worktree, name)
	}
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	env = applyGlobalEnv(t, env, vars)
	return
}

// repositoryFIFOResolve asserts the actual bind source, target and folder.
// A failed Git probe selects folder; a successful probe selects root.
func repositoryFIFOResolve(t *testing.T, folder, root string) {
	t.Helper()
	resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	source := root
	if source == "" {
		source = folder
	}
	target := "/workspaces/" + filepath.Base(source)
	rel, err := filepath.Rel(source, folder)
	if err != nil {
		t.Fatal(err)
	}
	wantFolder := filepath.ToSlash(filepath.Join(target, rel))
	if resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Source != source || resolved.WorkspaceMount.Target != target || resolved.WorkspaceFolder != wantFolder {
		t.Fatalf("Resolve mount = %+v, folder = %q; want source %q target %q folder %q", resolved.WorkspaceMount, resolved.WorkspaceFolder, source, target, wantFolder)
	}
}

// The format check and the full read after setup each open the config FIFO.
// Only the first applies repository settings; only the second follows includes.
// Both stop at a syntax error. GitRoot and Resolve must match real Git and the
// fixed expected root and number of reads on all six discovery routes.
func TestGitRootRepositoryConfigFIFOStream(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	const valid = "[core]\nrepositoryformatversion=0\n"
	const bare = valid + "bare=true\n"
	for _, name := range []string{"config", "config.worktree"} {
		for _, route := range []string{"discovered", "gitfile", "explicit", "self", "linked", "common-env"} {
			for _, c := range []struct {
				name      string
				streams   []string
				ok        bool
				reads     int
				firstBare bool
			}{
				{"valid", []string{valid, valid}, true, 2, false},
				{"empty", nil, true, 2, false},
				{"syntax", []string{globalConfigBroken}, false, 1, false},
				{"second-syntax", []string{valid, globalConfigBroken}, false, 2, false},
				{"large", []string{globalConfigLargeStream, globalConfigLargeStream}, true, 2, false},
				{"large-syntax", []string{globalConfigLargeStream + globalConfigBroken}, false, 1, false},
				{"first-bare", []string{bare, valid}, true, 2, true},
				{"second-bare", []string{valid, bare}, true, 2, false},
				{"typed-version", []string{"[core]\nrepositoryformatversion=bad\n"}, name == "config.worktree", 1, false},
				{"typed-bare", []string{valid + "bare=bad\n"}, false, 1, false},
				{"typed-worktree", []string{valid + "worktree\n"}, false, 1, false},
				{"typed-extension", []string{valid + "[extensions]\nworktreeConfig=bad\n"}, name == "config.worktree", 1, false},
				{"first-include", []string{valid + "[include]\npath\n", valid}, true, 2, false},
				{"second-include", []string{valid, valid + "[include]\npath\n"}, false, 2, false},
			} {
				t.Run(name+"/"+route+"/"+c.name, func(t *testing.T) {
					tree, folder, fifo, env := repositoryFIFOFixture(t, git, route, name, true)
					want := ""
					if c.ok {
						want = tree
						if route == "explicit" || route == "self" || (c.firstBare && (name == "config.worktree" || (route != "linked" && route != "common-env"))) {
							want = folder
						}
					}
					reads := c.reads
					if (c.name == "typed-version" || c.name == "typed-extension") && name == "config.worktree" {
						reads = 2
					}
					for _, probe := range []string{"git", "GitRoot", "Resolve"} {
						t.Run(probe, func(t *testing.T) {
							finish := serveConfigFIFO(t, fifo, c.streams...)
							switch probe {
							case "git":
								if got := globalSetupGit(t, git, folder, env); got != want {
									t.Errorf("Git root = %q; want %q", got, want)
								}
							case "GitRoot":
								if got, err := GitRoot(folder); err != nil || got != want {
									t.Errorf("GitRoot = %q, %v; want %q", got, err, want)
								}
							case "Resolve":
								repositoryFIFOResolve(t, folder, want)
							}
							if got := finish(); got != reads {
								t.Errorf("FIFO reads = %d; want %d", got, reads)
							}
						})
					}
				})
			}
		}
	}
}

// An inactive config.worktree must not be opened, even when it is a FIFO.
func TestGitRootRepositoryConfigFIFOInactive(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	tree, folder, fifo, env := repositoryFIFOFixture(t, git, "discovered", "config.worktree", false)
	finish := serveConfigFIFO(t, fifo, globalConfigBroken)
	if got := globalSetupGit(t, git, folder, env); got != tree {
		t.Fatalf("Git root = %q; want %q", got, tree)
	}
	if got, err := GitRoot(folder); err != nil || got != tree {
		t.Fatalf("GitRoot = %q, %v; want %q", got, err, tree)
	}
	repositoryFIFOResolve(t, folder, tree)
	if got := finish(); got != 0 {
		t.Fatalf("inactive FIFO reads = %d; want 0", got)
	}
}

// Each probe waits for a writer on both opens, without an application timeout.
// The 200 ms hold and 10 s completion bounds are the existing FIFO test bounds.
func TestGitRootRepositoryConfigFIFOWaits(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	for _, name := range []string{"config", "config.worktree"} {
		for _, probe := range []string{"git", "GitRoot", "Resolve"} {
			t.Run(name+"/"+probe, func(t *testing.T) {
				tree, folder, fifo, env := repositoryFIFOFixture(t, git, "discovered", name, true)
				result := make(chan string, 1)
				go func() {
					switch probe {
					case "git":
						result <- globalSetupGit(t, git, folder, env)
					case "GitRoot":
						got, _ := GitRoot(folder)
						result <- got
					case "Resolve":
						result <- configFIFOResolved(tree, folder)
					}
				}()
				premature := false
				select {
				case got := <-result:
					premature = true
					t.Errorf("root = %q before a writer opened the FIFO", got)
				case <-time.After(200 * time.Millisecond):
				}
				finish := serveConfigFIFO(t, fifo, globalConfigValid, globalConfigValid)
				if !premature {
					select {
					case got := <-result:
						if got != tree {
							t.Errorf("root = %q; want %q", got, tree)
						}
					case <-time.After(10 * time.Second):
						t.Error("probe did not settle after writer started")
					}
				}
				if got := finish(); got != 2 {
					t.Errorf("FIFO reads = %d; want 2", got)
				}
			})
		}
	}
}

// Keep the writer open after an invalid prefix: Git reports the parse error
// before EOF, so the format reader must do so too. Cleanup closes the writer
// and settles the probe even when that assertion fails.
func TestGitRootRepositoryConfigFIFOStopsAtError(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	for _, name := range []string{"config", "config.worktree"} {
		for _, c := range []struct{ name, content string }{
			{"syntax", globalConfigBroken},
			{"bare", "[core]\nbare=bad\n"},
			{"worktree", "[core]\nworktree\n"},
			{"version", "[core]\nrepositoryformatversion=bad\n"},
			{"extension", "[extensions]\nworktreeConfig=bad\n"},
		} {
			if name == "config.worktree" && (c.name == "version" || c.name == "extension") {
				continue
			}
			for _, probe := range []string{"git", "GitRoot", "Resolve"} {
				t.Run(name+"/"+c.name+"/"+probe, func(t *testing.T) {
					tree, folder, fifo, env := repositoryFIFOFixture(t, git, "discovered", name, true)
					result := make(chan string, 1)
					go func() {
						switch probe {
						case "git":
							result <- globalSetupGit(t, git, folder, env)
						case "GitRoot":
							got, _ := GitRoot(folder)
							result <- got
						case "Resolve":
							result <- configFIFOResolved(tree, folder)
						}
					}()
					// A nonblocking writer lets cleanup settle a probe that rejects the
					// FIFO before opening it. Its startup bound is test-only.
					var fd int
					var openErr error
					deadline := time.Now().Add(10 * time.Second)
					for {
						fd, openErr = unix.Open(fifo, unix.O_WRONLY|unix.O_NONBLOCK, 0)
						if openErr == nil || time.Now().After(deadline) {
							break
						}
						time.Sleep(time.Millisecond)
					}
					if openErr != nil {
						t.Fatal(openErr)
					}
					writer := os.NewFile(uintptr(fd), fifo)
					defer writer.Close()
					if _, err := fmt.Fprint(writer, c.content); err != nil {
						t.Fatal(err)
					}
					settled := false
					select {
					case got := <-result:
						settled = true
						if got != "" {
							t.Errorf("root = %q; want no root", got)
						}
					case <-time.After(200 * time.Millisecond):
						t.Error("probe waited for EOF after invalid config prefix")
					}
					writer.Close()
					if !settled {
						select {
						case <-result:
						case <-time.After(10 * time.Second):
							t.Error("probe did not settle after writer closed")
						}
					}
				})
			}
		}
	}
}

// A repository config symlink to /dev/null is an empty device stream. The existing directory and permission cases keep their refusal
// assertions in TestGitRootRepositoryFormatMatchesGit.
func TestGitRootRepositoryConfigDevice(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	for _, name := range []string{"config", "config.worktree"} {
		t.Run(name, func(t *testing.T) {
			tree, folder, fifo, env := repositoryFIFOFixture(t, git, "discovered", name, true)
			if err := os.Remove(fifo); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(os.DevNull, fifo); err != nil {
				t.Fatal(err)
			}
			if got := globalSetupGit(t, git, folder, env); got != tree {
				t.Fatalf("Git root = %q; want %q", got, tree)
			}
			if got, err := GitRoot(folder); err != nil || got != tree {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, tree)
			}
			repositoryFIFOResolve(t, folder, tree)
		})
	}
}
