// relationships:
//   references: [git, git_config_include, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// TestGitRootConfigIncludeFIFOStream includes a FIFO from a global config
// file and from the repository config. Git opens an included FIFO each time it
// reads the including file, and waits for a writer: twice for a global file,
// when it starts and after setup, and once for the repository config, in its
// full read after setup. Native discovery and the host Git must both give each
// case's root, and take the same number of streams.
func TestGitRootConfigIncludeFIFOStream(t *testing.T) {
	git, _ := exec.LookPath("git")
	for _, from := range []string{"global", "repository"} {
		for _, c := range []struct {
			name     string
			segments []string
			ok       bool
			reads    map[string]int
		}{
			{name: "valid", segments: []string{globalConfigValid, globalConfigValid}, ok: true, reads: map[string]int{"global": 2, "repository": 1}},
			{name: "syntax", segments: []string{globalConfigBroken}, reads: map[string]int{"global": 1, "repository": 1}},
			{name: "second-syntax", segments: []string{globalConfigValid, globalConfigBroken}, ok: from == "repository", reads: map[string]int{"global": 2, "repository": 1}},
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
				} else {
					config += includeLine(fifo)
				}
				writeFile(t, filepath.Join(tree, ".git", "config"), config)
				env := applyGlobalEnv(t, testgit.Env(), includeCaseVars(base, map[string]*string{"GIT_CONFIG_GLOBAL": &global}))
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
