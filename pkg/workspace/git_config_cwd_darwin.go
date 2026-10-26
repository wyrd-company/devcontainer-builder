// relationships:
//   references: [git_config_cwd_unix]

package workspace

import "golang.org/x/sys/unix"

// gitConfigCwdFlags opens Git's working folder to look up names from it. This
// needs read permission on the folder, which Git's working folder does not;
// without it, relative names are joined to the folder's path.
const gitConfigCwdFlags = unix.O_RDONLY
