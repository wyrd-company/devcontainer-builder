// relationships:
//
//	references: [up, manage, ../../cli/cli]
package dockercompose

import (
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
)

// ProjectConflict identifies a project container owned by another workspace.
// ID is the full Docker ID; LocalFolder is devcontainer.local_folder.
type ProjectConflict struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	LocalFolder string `json:"localFolder"`
}

// ProjectConflictError carries all conflicting containers so callers can offer
// both text diagnostics and structured error results.
type ProjectConflictError struct {
	Project   string
	Workspace string
	Conflicts []ProjectConflict
}

func (e *ProjectConflictError) Error() string {
	parts := make([]string, 0, len(e.Conflicts))
	for _, c := range e.Conflicts {
		parts = append(parts, fmt.Sprintf("container %q (%s) belongs to workspace %q", c.Name, c.ID, c.LocalFolder))
	}
	return fmt.Sprintf("compose project %q has conflicting containers for workspace %q: %s", e.Project, e.Workspace, strings.Join(parts, "; "))
}

func projectConflict(project, workspace string, containers []container.Summary) *ProjectConflictError {
	var conflicts []ProjectConflict
	for _, c := range containers {
		if owner := c.Labels[LabelLocalFolder]; owner != "" && owner != workspace {
			name := ""
			if len(c.Names) > 0 {
				name = strings.TrimPrefix(c.Names[0], "/")
			}
			conflicts = append(conflicts, ProjectConflict{Name: name, ID: c.ID, LocalFolder: owner})
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Name != conflicts[j].Name {
			return conflicts[i].Name < conflicts[j].Name
		}
		return conflicts[i].ID < conflicts[j].ID
	})
	return &ProjectConflictError{Project: project, Workspace: workspace, Conflicts: conflicts}
}
