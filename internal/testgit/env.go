// relationships:
//   references: [CONTRIBUTING]

// Package testgit isolates Git commands used by test fixtures.
package testgit

import (
	"os"
	"strings"
)

// Env preserves the process environment except for Git overrides, then supplies
// a fixed identity and disables host Git configuration for fixture commands.
// Callers can append deliberate fixture settings after this baseline.
func Env() []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	return append(env,
		"GIT_AUTHOR_NAME=Example", "GIT_AUTHOR_EMAIL=example@example.invalid",
		"GIT_COMMITTER_NAME=Example", "GIT_COMMITTER_EMAIL=example@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
}
