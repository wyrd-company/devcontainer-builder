// relationships:
//   references: [git_config_cwd_unix]

package workspace

import "golang.org/x/sys/unix"

// gitConfigCwdFlags opens Git's working folder only to look up names from it,
// so that, as for Git's working folder, no read permission on it is needed.
const gitConfigCwdFlags = unix.O_PATH
