// relationships:
//
// references: [objects, up, manage, ../../../internal/datafolder/staging]
package dockercompose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/volume"
	"github.com/wyrd-company/devcontainer-builder/internal/datafolder"
)

const labelObjectRoot = "devcontainer.compose.objects"

// allocateObjects records ownership before writing any secret. A failed or
// interrupted Up leaves a discoverable record, including before ContainerCreate.
// The volume is an unmounted ledger; its labels contain paths, never payloads.
func (h *Host) allocateObjects(ctx context.Context, project, workspace string) (string, error) {
	parent := h.opts.UserDataFolder
	if parent == "" {
		parent = filepath.Join(os.TempDir(), "dcb-compose")
	}
	root, err := datafolder.NewStaging(parent, "objects-")
	if err != nil {
		return "", fmt.Errorf("allocate compose objects: %w", err)
	}
	err = h.publishObjectOwnership(ctx, root, project, workspace)
	if err != nil {
		// A lost response can still mean the daemon recorded ownership. Keep that
		// record for retry; no payload has been written into the directory yet.
		_ = os.Remove(root)
		return "", err
	}
	return root, nil
}

// VolumeCreate can return an existing volume. Do not write into a new root
// unless the daemon returned the exact ownership we requested.
func (h *Host) publishObjectOwnership(ctx context.Context, root, project, workspace string) error {
	record, err := h.client.VolumeCreate(ctx, volume.CreateOptions{
		Name:   "dcb-" + filepath.Base(root),
		Labels: map[string]string{labelObjectRoot: root, LabelLocalFolder: workspace, LabelProject: project},
	})
	if err != nil {
		return errorf("record materialized object ownership: %w", err)
	}
	if record.Name != "dcb-"+filepath.Base(root) || record.Labels[labelObjectRoot] != root || record.Labels[LabelLocalFolder] != workspace || record.Labels[LabelProject] != project {
		return errorf("materialized object ownership collision")
	}
	return nil
}

func (h *Host) objectRecords(ctx context.Context, workspace string) ([]*volume.Volume, error) {
	result, err := h.client.VolumeList(ctx, volume.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", labelObjectRoot), filters.Arg("label", LabelLocalFolder+"="+workspace),
	)})
	if err != nil {
		return nil, errorf("list materialized object ownership: %w", err)
	}
	return result.Volumes, nil
}

func (h *Host) removeObjectRecords(ctx context.Context, records []*volume.Volume) error {
	for _, record := range records {
		root := record.Labels[labelObjectRoot]
		// Only allocator-shaped, absolute private roots are deletion targets.
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || !strings.HasPrefix(filepath.Base(root), "objects-") || record.Name != "dcb-"+filepath.Base(root) {
			return errorf("invalid materialized object ownership record")
		}
		if err := os.RemoveAll(root); err != nil {
			return errorf("remove materialized objects: %w", err)
		}
		if err := h.client.VolumeRemove(ctx, record.Name, true); err != nil {
			return errorf("remove materialized object ownership: %w", err)
		}
	}
	return nil
}
