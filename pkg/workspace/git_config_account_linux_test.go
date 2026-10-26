// relationships:
//   references: [git_config_include, git_account_lookup_passwd, git_account_lookup_system, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// accountHelperResult is what TestGitRootNamedAccountHelper reports.
type accountHelperResult struct {
	Root, RootErr, Source, Target, Folder, ResolveErr string
}

// TestGitRootNamedAccountInclude includes ~name/ paths from a global config
// file, with a controlled libc account lookup that knows fixtureacct as an
// account service other than /etc/passwd would. The host Git and a helper
// process of this test binary both run with the lookup preloaded: a cgo build
// asks it, as Git does, and a build whose lookup reads /etc/passwd only, such
// as the release build, cannot. That build skips an include whose account it
// cannot find, the approved difference, so it finds the Git root where Git
// rejects an unknown name or the included file of a service-only account.
// Known accounts and ~/ follow Git in every build.
func TestGitRootNamedAccountInclude(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("no C compiler for the account lookup fixture")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs(filepath.Join("testdata", "account-service.c"))
	if err != nil {
		t.Fatal(err)
	}
	preload := filepath.Join(base, "account-service.so")
	if out, err := exec.Command(cc, "-shared", "-fPIC", "-o", preload, source, "-ldl").CombinedOutput(); err != nil {
		t.Fatalf("cc: %v %s", err, out)
	}
	accountHome := mkdir(t, filepath.Join(base, "account"))
	writeFile(t, filepath.Join(accountHome, "valid.cfg"), globalConfigValid)
	writeFile(t, filepath.Join(accountHome, "broken.cfg"), globalConfigBroken)
	home := mkdir(t, filepath.Join(base, "home"))
	writeFile(t, filepath.Join(home, "valid.cfg"), globalConfigValid)
	writeFile(t, filepath.Join(home, "broken.cfg"), globalConfigBroken)
	// A known account's include reaches the fixture through its home folder,
	// so the test writes nothing there.
	known := ""
	if current, err := user.Current(); err == nil && current.Username != "" {
		if physical, err := filepath.EvalSymlinks(current.HomeDir); err == nil {
			if rel, err := filepath.Rel(physical, accountHome); err == nil {
				if _, err := os.Stat(current.HomeDir + "/" + rel + "/valid.cfg"); err == nil {
					known = "~" + current.Username + "/" + filepath.ToSlash(rel) + "/"
				}
			}
		}
	}
	for _, c := range []struct {
		name, include string
		// git is true when Git with the fixture finds the Git root.
		git bool
		// passwdOnly is true when a build whose lookup reads /etc/passwd
		// only finds the Git root: the approved difference where git is
		// false.
		passwdOnly bool
		known      bool
	}{
		{name: "service-only-valid", include: "~fixtureacct/valid.cfg", git: true, passwdOnly: true},
		{name: "service-only-syntax", include: "~fixtureacct/broken.cfg", passwdOnly: true},
		{name: "service-only-missing", include: "~fixtureacct/missing.cfg", git: true, passwdOnly: true},
		{name: "unknown-valid", include: "~no-such-user-for-dcb-tests/valid.cfg", passwdOnly: true},
		{name: "unknown-syntax", include: "~no-such-user-for-dcb-tests/broken.cfg", passwdOnly: true},
		{name: "known-valid", include: "valid.cfg", git: true, passwdOnly: true, known: true},
		{name: "known-syntax", include: "broken.cfg", known: true},
		{name: "home-valid", include: "~/valid.cfg", git: true, passwdOnly: true},
		{name: "home-syntax", include: "~/broken.cfg"},
	} {
		t.Run(c.name, func(t *testing.T) {
			include := c.include
			if c.known {
				if known == "" {
					t.Skip("the current account's home folder does not reach the fixture")
				}
				include = known + include
			}
			tree := filepath.Join(base, c.name, "tree")
			makeRepo(t, tree)
			folder := mkdir(t, filepath.Join(tree, "app"))
			writeFile(t, filepath.Join(tree, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n")
			global := filepath.Join(base, c.name, "global.cfg")
			writeFile(t, global, includeLine(include))
			caseBase := filepath.Join(base, c.name)
			env := applyGlobalEnv(t, testgit.Env(), includeCaseVars(caseBase, map[string]*string{
				"GIT_CONFIG_GLOBAL": &global, "HOME": &home, "XDG_CONFIG_HOME": nil,
			}))
			gitWant, nativeWant := "", ""
			if c.git {
				gitWant = tree
			}
			if c.git || c.passwdOnly && gitAccountLookupPasswdOnly {
				nativeWant = tree
			}
			fixtureEnv := append(env, "LD_PRELOAD="+preload, "FIXTURE_ACCOUNT_HOME="+accountHome)
			if measured := globalSetupGit(t, git, folder, fixtureEnv); measured != gitWant {
				t.Fatalf("Git root with the fixture = %q; want %q", measured, gitWant)
			}
			if c.name == "service-only-valid" {
				// Without the fixture, Git does not know the account.
				if measured := globalSetupGit(t, git, folder, env); measured != "" {
					t.Fatalf("Git root without the fixture = %q; want none", measured)
				}
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestGitRootNamedAccountHelper$")
			cmd.Dir, cmd.Env = folder, append(fixtureEnv, "DCB_TEST_ACCOUNT_HELPER=1")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("helper: %v %s", err, out)
			}
			got := accountHelperOutput(t, out)
			if got.Root != nativeWant || got.RootErr != "" {
				t.Fatalf("GitRoot = %q, %s; want %q (Git %q)", got.Root, got.RootErr, nativeWant, gitWant)
			}
			mount := nativeWant
			if mount == "" {
				mount = folder
			}
			rel, err := filepath.Rel(mount, folder)
			if err != nil {
				t.Fatal(err)
			}
			target := path.Join("/workspaces", filepath.Base(mount))
			if got.ResolveErr != "" || got.Source != mount || got.Target != target || got.Folder != path.Join(target, filepath.ToSlash(rel)) {
				t.Fatalf("Resolve = %+v; want source %q target %q", got, mount, target)
			}
		})
	}
}

// accountHelperPrefix starts the line that carries the helper's result, among
// the test runner's own output.
const accountHelperPrefix = "account-helper-result: "

// accountHelperOutput reads the helper's result from its output.
func accountHelperOutput(t *testing.T, out []byte) accountHelperResult {
	t.Helper()
	for line := range strings.Lines(string(out)) {
		if rest, found := strings.CutPrefix(line, accountHelperPrefix); found {
			var result accountHelperResult
			if err := json.Unmarshal([]byte(rest), &result); err != nil {
				t.Fatalf("helper result %q: %v", rest, err)
			}
			return result
		}
	}
	t.Fatalf("helper wrote no result: %s", out)
	return accountHelperResult{}
}

// TestGitRootNamedAccountHelper finds the Git root and the workspace mount of
// its working folder, for TestGitRootNamedAccountInclude, in a process that
// the account lookup fixture is preloaded into.
func TestGitRootNamedAccountHelper(t *testing.T) {
	if os.Getenv("DCB_TEST_ACCOUNT_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	folder, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var result accountHelperResult
	result.Root, err = GitRoot(folder)
	if err != nil {
		result.RootErr = err.Error()
	}
	resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
	switch {
	case err != nil:
		result.ResolveErr = err.Error()
	case resolved.WorkspaceMount == nil:
		result.ResolveErr = "no workspace mount"
	default:
		result.Source, result.Target, result.Folder = resolved.WorkspaceMount.Source, resolved.WorkspaceMount.Target, resolved.WorkspaceFolder
	}
	line, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\n%s%s\n", accountHelperPrefix, line)
}
