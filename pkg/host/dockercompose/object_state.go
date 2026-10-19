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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
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

// previousObjectRecords snapshots roots already consumed by this project before
// Up writes anything. Unmounted records can belong to an in-flight producer and
// remain for explicit teardown, rather than being swept during recreation.
func (h *Host) previousObjectRecords(ctx context.Context, workspace, project string, containers []container.Summary) ([]*volume.Volume, error) {
	if len(containers) == 0 {
		return nil, nil
	}
	records, err := h.objectRecords(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var previous []*volume.Volume
	for _, record := range records {
		if record.Labels[LabelProject] == project && objectRootMounted(record.Labels[labelObjectRoot], containers) {
			previous = append(previous, record)
		}
	}
	return previous, nil
}

// removeRetiredObjects deletes only prior roots with no remaining consumer. Check
// all containers, including stopped and unrelated containers, before deleting any
// root; a reused service can still mount a root shared with a recreated service.
func (h *Host) removeRetiredObjects(ctx context.Context, previous []*volume.Volume) error {
	if len(previous) == 0 {
		return nil
	}
	containers, err := h.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return errorf("find materialized object consumers: %w", err)
	}
	var retired []*volume.Volume
	for _, record := range previous {
		if !objectRootMounted(record.Labels[labelObjectRoot], containers) {
			retired = append(retired, record)
		}
	}
	return h.removeObjectRecords(ctx, retired)
}

func objectRootMounted(root string, containers []container.Summary) bool {
	for _, c := range containers {
		for _, m := range c.Mounts {
			source := filepath.Clean(m.Source)
			if m.Type == mount.TypeBind && (source == root || strings.HasPrefix(source, root+string(filepath.Separator))) {
				return true
			}
		}
	}
	return false
}
