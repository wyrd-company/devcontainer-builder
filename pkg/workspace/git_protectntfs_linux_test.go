// relationships:
//   references: [git_config_full, git_config_fifo_linux_test, git_protectntfs_test]

package workspace

import (
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// Git's very-early and format reads ignore core.protectntfs's type. Its full
// configset read finishes every file and command input before typed callbacks
// reject an invalid value. Finite FIFOs prove the reached reads, independently
// of the final root, with all producers stopped by serveConfigFIFO's cleanup.
func TestGitRootProtectntfsReadPhasesMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	valid := "[core]\nprotectntfs=true\n"
	invalid := "[core]\nprotectntfs=invalid\n"
	for _, name := range []string{"early-only-invalid", "format-only-invalid", "worktree-format-only-invalid", "global-invalid-before-repository", "invalid-before-include", "count-invalid-before-include", "count-invalid-before-parameters"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			folder := mkdir(t, filepath.Join(tree, "app"))
			admin := filepath.Join(tree, ".git")
			config := filepath.Join(admin, "config")
			writeFile(t, config, "[core]\nrepositoryformatversion=0\n")
			fifo := filepath.Join(base, "stream.cfg")
			segments := []string{valid}
			reads := 1
			ok := false
			vars := map[string]*string{}
			switch name {
			case "early-only-invalid":
				vars["GIT_CONFIG_GLOBAL"] = &fifo
				segments = []string{invalid, valid}
				reads = 2
				ok = true
			case "format-only-invalid":
				fifo = config
				segments = []string{invalid, valid}
				reads = 2
				ok = true
			case "worktree-format-only-invalid":
				writeFile(t, config, "[core]\nrepositoryformatversion=1\n[extensions]\nworktreeConfig=true\n")
				fifo = filepath.Join(admin, "config.worktree")
				segments = []string{invalid, valid}
				reads = 2
				ok = true
			case "global-invalid-before-repository":
				global := filepath.Join(base, "global.cfg")
				writeFile(t, global, invalid)
				vars["GIT_CONFIG_GLOBAL"] = &global
				fifo = config
				segments = []string{valid, valid}
				reads = 2
			case "invalid-before-include":
				global := filepath.Join(base, "global.cfg")
				writeFile(t, global, invalid+includeLine(fifo))
				vars["GIT_CONFIG_GLOBAL"] = &global
				segments = []string{valid, valid}
				reads = 2
			case "count-invalid-before-include", "count-invalid-before-parameters":
				vars = commandPairs("core.protectntfs", "invalid", "include.path", fifo)(t, includeFixture{})
				if name == "count-invalid-before-parameters" {
					vars = commandPairs("core.protectntfs", "invalid")(t, includeFixture{})
					vars["GIT_CONFIG_PARAMETERS"] = globalValue("'include.path'=" + commandQuote(fifo))
				}
			}
			if fifo == config {
				if err := unix.Unlink(config); err != nil {
					t.Fatal(err)
				}
			}
			if err := unix.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			env := applyGlobalEnv(t, testgit.Env(), includeCaseVars(base, vars))
			want := ""
			if ok {
				want = tree
			}
			for _, route := range []struct {
				name string
				run  func() string
			}{
				{"Git", func() string { return globalSetupGit(t, git, folder, env) }},
				{"GitRoot", func() string {
					root, err := GitRoot(folder)
					if err != nil {
						t.Fatal(err)
					}
					return root
				}},
				{"Options.GitRoot", func() string {
					root, err := (Options{}).GitRoot(folder)
					if err != nil {
						t.Fatal(err)
					}
					return root
				}},
				{"Resolve", func() string { return configFIFOResolved(tree, folder) }},
			} {
				t.Run(route.name, func(t *testing.T) {
					finish := serveConfigFIFO(t, fifo, segments...)
					got := route.run()
					count := finish()
					if got != want {
						t.Errorf("root = %q; want %q", got, want)
					}
					if count != reads {
						t.Errorf("config reads = %d; want %d", count, reads)
					}
				})
			}
		})
	}
}
