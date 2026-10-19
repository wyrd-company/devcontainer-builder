// relationships:
//
//	validates: [env]
package testgit

import (
	"os"
	"strings"
	"testing"
)

func TestEnvRemovesEveryGitOverride(t *testing.T) {
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_NAMESPACE", "GIT_FUTURE_OVERRIDE", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(name, "inherited")
	}
	t.Setenv("EXAMPLE_KEEP", "value=with=equals")
	got := make(map[string]string)
	for _, entry := range Env() {
		name, value, _ := strings.Cut(entry, "=")
		if _, duplicate := got[name]; duplicate {
			t.Fatalf("duplicate variable %s", name)
		}
		got[name] = value
		if strings.HasPrefix(name, "GIT_") && value == "inherited" {
			t.Errorf("inherited override %s", name)
		}
	}
	for name, want := range map[string]string{"GIT_AUTHOR_NAME": "Example", "GIT_AUTHOR_EMAIL": "example@example.invalid", "GIT_COMMITTER_NAME": "Example", "GIT_COMMITTER_EMAIL": "example@example.invalid", "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1", "EXAMPLE_KEEP": os.Getenv("EXAMPLE_KEEP")} {
		if got[name] != want {
			t.Errorf("%s = %q, want %q", name, got[name], want)
		}
	}
}
