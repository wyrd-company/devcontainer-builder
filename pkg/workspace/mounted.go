// relationships:
//   references: [workspace]

package workspace

import (
	"path"
	"path/filepath"
	"strings"
)

// Bind is an existing container's bind mount: the host source and the
// container destination, as the Engine reports them.
type Bind struct {
	Source, Destination string
}

// MountedFolder returns the container folder at which an existing container
// shows localWorkspace: the destination of the bind mount whose source is the
// workspace or its nearest mounted ancestor, with the rest of the workspace
// path below it. That is where the container placed the workspace, whatever
// options created it. It returns "" when no bind mount contains the workspace.
func MountedFolder(localWorkspace string, binds []Bind) string {
	local := filepath.Clean(localWorkspace)
	best, folder := -1, ""
	for _, b := range binds {
		if b.Source == "" || b.Destination == "" {
			continue
		}
		source := filepath.Clean(b.Source)
		rel, err := filepath.Rel(source, local)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if len(source) > best {
			best, folder = len(source), path.Join(b.Destination, filepath.ToSlash(rel))
		}
	}
	return folder
}
