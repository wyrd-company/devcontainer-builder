// relationships:
//   references: [git, git_global_config, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// globalSetupLayout is a repository layout and the folder that Git's setup
// leaves as its working folder, where Git reads a relative global config path
// a second time.
type globalSetupLayout struct {
	name string
	// build writes the layout under base and returns the caller folder, the
	// Git root, Git's working folder cwd, other folders where a config file
	// must not change the result, and the variables that select the route.
	build func(t *testing.T, base string) (folder, root, cwd string, others []string, env map[string]string)
}

// makeGitAdmin writes the minimum Git accepts as a repository folder.
func makeGitAdmin(t *testing.T, dir string, config string) {
	t.Helper()
	mkdir(t, filepath.Join(dir, "objects"))
	mkdir(t, filepath.Join(dir, "refs"))
	writeFile(t, filepath.Join(dir, "HEAD"), "ref: refs/heads/main\n")
	if config != "" {
		writeFile(t, filepath.Join(dir, "config"), config)
	}
}

var globalSetupLayouts = []globalSetupLayout{
	{name: "discovered", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		// Git changes to the folder where it found .git.
		tree := filepath.Join(base, "tree")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "")
		return mkdir(t, filepath.Join(tree, "app", "deep")), tree, tree, []string{filepath.Join(tree, "app")}, nil
	}},
	{name: "discovered-at-root", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		tree := filepath.Join(base, "tree")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "")
		return tree, tree, tree, []string{filepath.Join(tree, ".git")}, nil
	}},
	{name: "symlinked-parent", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		// Git walks the physical parents and changes to the physical folder
		// where it found .git; the root keeps the caller's lexical names, a
		// different folder.
		physical := filepath.Join(base, "physical")
		makeGitAdmin(t, filepath.Join(physical, ".git"), "")
		mkdir(t, filepath.Join(physical, "sub", "app"))
		lexical := mkdir(t, filepath.Join(base, "lexical"))
		symlink(t, filepath.Join(physical, "sub"), filepath.Join(lexical, "link"))
		return filepath.Join(lexical, "link", "app"), lexical, physical, []string{lexical, filepath.Join(physical, "sub")}, nil
	}},
	{name: "gitfile", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		tree := filepath.Join(base, "tree")
		admin := filepath.Join(base, "admin")
		makeGitAdmin(t, admin, "")
		writeFile(t, filepath.Join(tree, ".git"), "gitdir: "+admin+"\n")
		return mkdir(t, filepath.Join(tree, "app")), tree, tree, []string{admin}, nil
	}},
	{name: "explicit", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		// GIT_DIR sets the working tree to the caller folder, where Git stays.
		tree := filepath.Join(base, "tree")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "")
		folder := mkdir(t, filepath.Join(tree, "app"))
		return folder, folder, folder, []string{tree}, map[string]string{"GIT_DIR": filepath.Join(tree, ".git")}
	}},
	{name: "work-tree-inside", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		// Git changes to a working tree that contains the caller folder.
		tree := filepath.Join(base, "tree")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "")
		app := filepath.Join(tree, "app")
		return mkdir(t, filepath.Join(app, "deep")), app, app, []string{tree}, map[string]string{"GIT_WORK_TREE": app}
	}},
	{name: "explicit-work-tree-inside", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		tree := filepath.Join(base, "tree")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "")
		return mkdir(t, filepath.Join(tree, "app")), tree, tree, []string{filepath.Join(tree, ".git")},
			map[string]string{"GIT_DIR": filepath.Join(tree, ".git"), "GIT_WORK_TREE": tree}
	}},
	{name: "explicit-work-tree-outside", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		// Git stays in a caller folder outside the working tree.
		tree := filepath.Join(base, "tree")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "")
		other := mkdir(t, filepath.Join(base, "other"))
		folder := mkdir(t, filepath.Join(tree, "app"))
		return folder, other, folder, []string{other, tree},
			map[string]string{"GIT_DIR": filepath.Join(tree, ".git"), "GIT_WORK_TREE": other}
	}},
	{name: "core-worktree-inside", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		tree := filepath.Join(base, "tree")
		workTree := filepath.Join(base, "work")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "[core]\n\trepositoryformatversion = 0\n\tworktree = "+workTree+"\n")
		return mkdir(t, filepath.Join(workTree, "app")), workTree, workTree, []string{tree},
			map[string]string{"GIT_DIR": filepath.Join(tree, ".git")}
	}},
	{name: "core-worktree-outside", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		tree := filepath.Join(base, "tree")
		workTree := mkdir(t, filepath.Join(base, "work"))
		makeGitAdmin(t, filepath.Join(tree, ".git"), "[core]\n\trepositoryformatversion = 0\n\tworktree = "+workTree+"\n")
		folder := mkdir(t, filepath.Join(tree, "app"))
		return folder, workTree, folder, []string{tree, workTree}, nil
	}},
	{name: "core-bare", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		// Without a working tree, Git returns to the caller folder.
		tree := filepath.Join(base, "tree")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "[core]\n\trepositoryformatversion = 0\n\tbare = true\n")
		folder := mkdir(t, filepath.Join(tree, "app"))
		return folder, folder, folder, []string{tree}, nil
	}},
	{name: "bare-repository", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		bare := filepath.Join(base, "bare.git")
		makeGitAdmin(t, bare, "[core]\n\trepositoryformatversion = 0\n\tbare = true\n")
		folder := mkdir(t, filepath.Join(bare, "info"))
		return folder, folder, folder, []string{bare}, nil
	}},
	{name: "self", build: func(t *testing.T, base string) (string, string, string, []string, map[string]string) {
		tree := filepath.Join(base, "tree")
		makeGitAdmin(t, filepath.Join(tree, ".git"), "")
		admin := filepath.Join(tree, ".git")
		return admin, admin, admin, []string{tree}, nil
	}},
}

// globalSetupKinds are the files placed as the relative global config path.
var globalSetupKinds = []struct {
	name         string
	unprivileged bool
	write        func(t *testing.T, path string)
}{
	{name: "syntax", write: func(t *testing.T, path string) { writeFile(t, path, globalConfigBroken) }},
	{name: "directory", write: func(t *testing.T, path string) { mkdir(t, path) }},
	{name: "valid", write: func(t *testing.T, path string) { writeFile(t, path, globalConfigValid) }},
	{name: "unreadable", unprivileged: true, write: func(t *testing.T, path string) {
		writeFile(t, path, globalConfigBroken)
		chmod(t, path, 0)
	}},
}

// globalSetupWant is the Git root for a kind of file at place: Git's first
// read, from the caller folder, rejects a syntax error; its second read, from
// its working folder cwd, also rejects a file that it cannot open, such as a
// folder. Git skips a file that access(2) denies.
func globalSetupWant(t *testing.T, kind, place, folder, cwd, root string) string {
	t.Helper()
	same := func(a, b string) bool {
		first, err := os.Stat(a)
		if err != nil {
			t.Fatal(err)
		}
		second, err := os.Stat(b)
		if err != nil {
			t.Fatal(err)
		}
		return os.SameFile(first, second)
	}
	switch {
	case kind == "syntax" && (same(place, folder) || same(place, cwd)):
		return ""
	case kind == "directory" && same(place, cwd):
		return ""
	}
	return root
}

// TestGitRootGlobalConfigSecondRead asserts that a relative global config
// path is read again from Git's working folder after setup, not from the Git
// root or the caller folder, on each setup route.
func TestGitRootGlobalConfigSecondRead(t *testing.T) {
	runGlobalSetupLayouts(t, "")
}

// TestGitRootGlobalConfigSecondReadMatchesGit asserts that the host Git gives
// each second-read result.
func TestGitRootGlobalConfigSecondReadMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	runGlobalSetupLayouts(t, git)
}

// runGlobalSetupLayouts places each kind of file at each layout's working
// folder and other folders. It asserts the native root and mount, and, when
// git is not empty, the host Git root.
func runGlobalSetupLayouts(t *testing.T, git string) {
	for _, layout := range globalSetupLayouts {
		for _, kind := range globalSetupKinds {
			for index := 0; ; index++ {
				name := layout.name + "/" + kind.name + "/cwd"
				if index > 0 {
					name = layout.name + "/" + kind.name + "/other-" + string(rune('0'+index))
				}
				var more bool
				t.Run(name, func(t *testing.T) {
					base, err := filepath.EvalSymlinks(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					folder, root, cwd, others, vars := layout.build(t, base)
					places := append([]string{cwd}, others...)
					more = index+1 < len(places)
					place := places[index]
					if kind.unprivileged && os.Geteuid() == 0 {
						t.Skip("root bypasses the permission the case denies")
					}
					env := append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base, "GIT_CONFIG_GLOBAL=post.cfg")
					t.Setenv("GIT_CEILING_DIRECTORIES", base)
					t.Setenv("GIT_CONFIG_GLOBAL", "post.cfg")
					for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY"} {
						t.Setenv(name, "")
						os.Unsetenv(name)
					}
					for name, value := range vars {
						env = append(env, name+"="+value)
						t.Setenv(name, value)
					}
					if git != "" {
						// The layout gives the root before the file is placed.
						if got := globalSetupGit(t, git, folder, env); got != root {
							t.Fatalf("Git root without the file = %q; want %q", got, root)
						}
					}
					kind.write(t, filepath.Join(place, "post.cfg"))
					want := globalSetupWant(t, kind.name, place, folder, cwd, root)
					if git != "" {
						if got := globalSetupGit(t, git, folder, env); got != want {
							t.Fatalf("Git root = %q; want %q", got, want)
						}
					}
					got, err := GitRoot(folder)
					if err != nil || got != want {
						t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
					}
					source := want
					if source == "" {
						source = folder
					}
					target := path.Join("/workspaces", filepath.Base(source))
					rel, err := filepath.Rel(source, folder)
					if err != nil {
						t.Fatal(err)
					}
					workspaceFolder := path.Join(target, filepath.ToSlash(rel))
					resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
					if err != nil || resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Source != source ||
						resolved.WorkspaceMount.Target != target || resolved.WorkspaceFolder != workspaceFolder {
						t.Fatalf("Resolve = %+v %+v, %v; want source %q target %q folder %q", resolved, resolved.WorkspaceMount, err, source, target, workspaceFolder)
					}
				})
				if !more {
					break
				}
			}
		}
	}
}

// globalSetupGit returns the Git root that upstream resolves from
// `git rev-parse --show-cdup` in folder, or "" when the probe fails.
func globalSetupGit(t *testing.T, git, folder string, env []string) string {
	t.Helper()
	cmd := exec.Command(git, "rev-parse", "--show-cdup")
	cmd.Dir, cmd.Env = folder, env
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	cdup := strings.TrimSpace(string(out))
	if filepath.IsAbs(cdup) {
		return filepath.Clean(cdup)
	}
	return filepath.Join(folder, cdup)
}
