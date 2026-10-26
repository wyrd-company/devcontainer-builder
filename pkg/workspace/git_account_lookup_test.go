// relationships:
//   validates: [git_account_lookup_passwd, git_account_lookup_system]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// goBuildLine is the //go:build line of the Go file at path.
func goBuildLine(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(content)) {
		if expr, found := strings.CutPrefix(strings.TrimSpace(line), "//go:build "); found {
			return expr
		}
	}
	t.Fatalf("%s has no //go:build line", path)
	return ""
}

// gitAccountLookupPasswdOnly must be true exactly in the builds where os/user
// looks accounts up in /etc/passwd only: the build constraint of the Go
// toolchain's os/user/lookup_unix.go.
func TestGitAccountLookupBuildConstraint(t *testing.T) {
	out, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	passwd := goBuildLine(t, filepath.Join(strings.TrimSpace(string(out)), "src", "os", "user", "lookup_unix.go"))
	if got := goBuildLine(t, "git_account_lookup_passwd.go"); got != passwd {
		t.Errorf("git_account_lookup_passwd.go builds for %q; os/user reads /etc/passwd only for %q", got, passwd)
	}
	if got, want := goBuildLine(t, "git_account_lookup_system.go"), "!("+passwd+")"; got != want {
		t.Errorf("git_account_lookup_system.go builds for %q; want %q", got, want)
	}
}
