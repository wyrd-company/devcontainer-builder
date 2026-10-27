// relationships:
//   references: [git, git_config_include, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// TestGitRootConfigIncludeFIFOStream includes a FIFO from a global config
// file, repository config and command-line config. Git opens an included FIFO
// each time it reads the including file, and waits for a writer: twice for a global file,
// when it starts and after setup, and once for the repository config, in its
// full read after setup. Command-line config is also read once after setup.
// Native discovery and the host Git must both give each
// case's root, and take the same number of streams.
func TestGitRootConfigIncludeFIFOStream(t *testing.T) {
	git, _ := exec.LookPath("git")
	for _, from := range []string{"global", "repository", "command-count", "command-parameters"} {
		for _, c := range []struct {
			name     string
			segments []string
			ok       bool
			reads    map[string]int
		}{
			{name: "valid", segments: []string{globalConfigValid, globalConfigValid}, ok: true, reads: map[string]int{"global": 2, "repository": 1, "command-count": 1, "command-parameters": 1}},
			{name: "syntax", segments: []string{globalConfigBroken}, reads: map[string]int{"global": 1, "repository": 1, "command-count": 1, "command-parameters": 1}},
			{name: "second-syntax", segments: []string{globalConfigValid, globalConfigBroken}, ok: from != "global", reads: map[string]int{"global": 2, "repository": 1, "command-count": 1, "command-parameters": 1}},
		} {
			t.Run(from+"/"+c.name, func(t *testing.T) {
				base, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				tree := filepath.Join(base, "tree")
				makeRepo(t, tree)
				folder := mkdir(t, filepath.Join(tree, "app"))
				fifo := filepath.Join(base, "include.fifo")
				if err := unix.Mkfifo(fifo, 0o600); err != nil {
					t.Fatal(err)
				}
				global := os.DevNull
				config := "[core]\n\trepositoryformatversion = 0\n"
				if from == "global" {
					global = filepath.Join(base, "global.cfg")
					writeFile(t, global, includeLine(fifo))
				} else if from == "repository" {
					config += includeLine(fifo)
				}
				writeFile(t, filepath.Join(tree, ".git", "config"), config)
				vars := map[string]*string{"GIT_CONFIG_GLOBAL": &global}
				if from == "command-count" {
					vars["GIT_CONFIG_COUNT"] = globalValue("1")
					vars["GIT_CONFIG_KEY_0"] = globalValue("include.path")
					vars["GIT_CONFIG_VALUE_0"] = &fifo
				} else if from == "command-parameters" {
					vars["GIT_CONFIG_PARAMETERS"] = globalValue("'include.path'=" + commandQuote(fifo))
				}
				env := applyGlobalEnv(t, testgit.Env(), includeCaseVars(base, vars))
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
					if reads := finish(); got != want || reads != c.reads[from] {
						t.Fatalf("%s root = %q after %d reads; want %q after %d", route.name, got, reads, want, c.reads[from])
					}
				}
			})
		}
	}
}

// Git names a relative config file, and a relative include in it, from its
// working folder, as given, so each name has its own path length limit. The
// caller folder's path takes more than half of PATH_MAX, and each long
// relative name takes most of the rest, so the folder's path joined to it does
// not fit in PATH_MAX. Both of Git's reads find the files, from the caller
// folder and then from the working tree. Native discovery and the host Git
// must both give each case's result.
func TestGitRootConfigRelativePathMax(t *testing.T) {
	testConfigRelativePathMax(t, false)
}

// Git's chdir to its working folder needs only search permission on it, so
// the cases of TestGitRootConfigRelativePathMax give the same results when the
// process cannot read the caller folder.
func TestGitRootConfigSearchOnlyPathMax(t *testing.T) {
	testConfigRelativePathMax(t, true)
}

// testConfigRelativePathMax runs the cases of TestGitRootConfigRelativePathMax,
// with a caller folder that the process can only search if searchOnly is set.
func testConfigRelativePathMax(t *testing.T, searchOnly bool) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	long := strings.Repeat("./", 1050)
	for _, c := range []struct {
		name, global, include, target string
		ok                            bool
	}{
		{name: "include-short-valid", global: "global.cfg", include: "inc.cfg", target: globalConfigValid, ok: true},
		{name: "include-short-syntax", global: "global.cfg", include: "inc.cfg", target: globalConfigBroken},
		{name: "include-long-valid", global: "global.cfg", include: long + "inc.cfg", target: globalConfigValid, ok: true},
		{name: "include-long-syntax", global: "global.cfg", include: long + "inc.cfg", target: globalConfigBroken},
		{name: "global-long-valid", global: long + "global.cfg", include: "inc.cfg", target: globalConfigValid, ok: true},
		{name: "global-long-syntax", global: long + "global.cfg", include: "inc.cfg", target: globalConfigBroken},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			for i := 0; len(tree) < 2200; i++ {
				tree = filepath.Join(tree, strings.Repeat("s", 150))
			}
			env := testgit.Env()
			cmd := exec.Command(git, "init", "-q", "-b", "main", tree)
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			folder := mkdir(t, filepath.Join(tree, "app"))
			env = applyGlobalEnv(t, env, includeCaseVars(base, nil))
			if control := globalSetupGit(t, git, folder, env); control != tree {
				t.Fatalf("Git root before the case = %q; want %q", control, tree)
			}
			// A long case's joined path does not fit in PATH_MAX, and a short
			// case's does.
			if joined := len(folder) + 1 + max(len(c.global), len(c.include)); joined >= 4096 != strings.Contains(c.name, "long") {
				t.Fatalf("joined path length %d for case %s", joined, c.name)
			}
			for _, dir := range []string{folder, tree} {
				writeFile(t, filepath.Join(dir, "global.cfg"), includeLine(c.include))
				writeFile(t, filepath.Join(dir, "inc.cfg"), c.target)
			}
			if searchOnly {
				searchOnlyFolder(t, folder)
			}
			env = applyGlobalEnv(t, env, includeCaseVars(base, map[string]*string{"GIT_CONFIG_GLOBAL": &c.global}))
			want := ""
			if c.ok {
				want = tree
			}
			if measured := globalSetupGit(t, git, folder, env); measured != want {
				t.Fatalf("Git root = %q; want %q", measured, want)
			}
			if got, err := GitRoot(folder); err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
			}
			assertIncludeResolve(t, folder, want)
		})
	}
}

// searchOnlyFolder lets the process search folder but not read it, until the
// test ends. The case is skipped for root, which reads it anyway.
func searchOnlyFolder(t *testing.T, folder string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission the case denies")
	}
	if err := os.Chmod(folder, 0o111); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(folder, 0o755); err != nil {
			t.Error(err)
		}
	})
	if fd, err := unix.Open(folder, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0); !errors.Is(err, unix.EACCES) {
		if err == nil {
			unix.Close(fd)
		}
		t.Fatalf("open %s for reading = %v; want EACCES", folder, err)
	}
}

// Git's full read after setup names the common folder's config from its
// working folder, the working tree, with a relative GIT_COMMON_DIR as given.
// The working tree's physical path takes more than half of PATH_MAX and the
// value takes most of the rest. The caller folder is reached through a short
// symbolic link, from which the format check finds the value's folder within
// PATH_MAX: from the caller folder it names the repository folder, and from
// the working tree a folder whose config only the full read reads.
func TestGitRootRelativeCommonDirFullReadPathMax(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	for _, c := range []struct {
		name, config string
		ok           bool
	}{
		{name: "valid", config: globalConfigValid, ok: true},
		{name: "syntax", config: globalConfigBroken},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			for i := 0; len(tree) < 2200; i++ {
				tree = filepath.Join(tree, strings.Repeat("s", 150))
			}
			env := testgit.Env()
			cmd := exec.Command(git, "init", "-q", "-b", "main", tree)
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			mkdir(t, filepath.Join(tree, "app"))
			symlink(t, tree, filepath.Join(base, "alias"))
			folder := filepath.Join(base, "alias", "app")
			value := strings.Repeat("./", 1000) + "c"
			symlink(t, filepath.Join(tree, ".git"), filepath.Join(tree, "app", "c"))
			writeFile(t, filepath.Join(tree, "c", "config"), c.config)
			if joined := len(tree) + 1 + len(value) + len("/config"); joined < 4096 {
				t.Fatalf("joined path length %d fits in PATH_MAX", joined)
			}
			env = applyGlobalEnv(t, env, includeCaseVars(base, map[string]*string{
				"GIT_DIR":        globalValue(filepath.Join(tree, ".git")),
				"GIT_WORK_TREE":  &tree,
				"GIT_COMMON_DIR": &value,
			}))
			measured := globalSetupGit(t, git, folder, env)
			if (measured != "") != c.ok {
				t.Fatalf("Git root = %q; want a root %v", measured, c.ok)
			}
			if got, err := GitRoot(folder); err != nil || got != measured {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, measured)
			}
			assertIncludeResolve(t, folder, measured)
		})
	}
}
