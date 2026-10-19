// relationships:
//
//	references: [config, host, ../workspace/workspace, ../../reference/cli/src/spec-node/devContainersSpecCLI]
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/workspace"
)

// Upstream's argument checks (devContainersSpecCLI.ts). The id-label pattern
// is unanchored there too.
var (
	idLabelPattern = regexp.MustCompile(`.+=.+`)
	mountPattern   = regexp.MustCompile(`^type=(bind|volume),source=([^,]+),target=([^,]+)(?:,external=(true|false))?$`)
)

// hostOS selects the consistency rule for workspace mounts; tests replace it.
var hostOS = runtime.GOOS

// mountOptions are upstream's workspace placement and container selection
// flags on up, exec, read-configuration and run-user-commands. Commands
// without them (build, set-up) mount the workspace folder alone, as upstream.
type mountOptions struct {
	gitRoot, worktreeCommonDir bool
	idLabels                   []string
	// Only on up.
	consistency string
	mounts      []string
}

func (o *mountOptions) flags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.gitRoot, "mount-workspace-git-root", true, "Mount the workspace using its Git root (=false mounts the workspace folder alone)")
	cmd.Flags().BoolVar(&o.worktreeCommonDir, "mount-git-worktree-common-dir", false, "Mount the Git worktree common folder so Git works in the container; needs a worktree added with --relative-paths")
	o.idLabelFlags(cmd)
	if cmd.Name() == "up" {
		cmd.Flags().StringVar(&o.consistency, "workspace-mount-consistency", workspace.ConsistencyCached, "Workspace bind mount consistency on Docker Desktop: consistent, cached, or delegated")
		cmd.Flags().StringArrayVar(&o.mounts, "mount", nil, "Additional mount type=<bind|volume>,source=<source>,target=<target>[,external=<true|false>] (repeat)")
	}
}

// idLabelFlags also serves management commands, which do not place mounts.
func (o *mountOptions) idLabelFlags(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&o.idLabels, "id-label", nil, "Container id label NAME=VALUE (repeat): select a container carrying every label")
}

// prepare validates the values and makes them the invocation's selection.
func (o *mountOptions) prepare(a *application) error {
	switch o.consistency {
	case "", workspace.ConsistencyConsistent, workspace.ConsistencyCached, workspace.ConsistencyDelegated:
	default:
		return usageError("--workspace-mount-consistency must be consistent, cached, or delegated")
	}
	for _, m := range o.mounts {
		if !mountPattern.MatchString(m) {
			return usageError("Unmatched argument format: mount must match type=<bind|volume>,source=<source>,target=<target>[,external=<true|false>]")
		}
	}
	labels := map[string]string{}
	for _, label := range o.idLabels {
		if !idLabelPattern.MatchString(label) {
			return usageError("Unmatched argument format: id-label must match <name>=<value>")
		}
		// Split at the first "=", as upstream's envListToObj does.
		key, value, _ := strings.Cut(label, "=")
		labels[key] = value
	}
	a.mounts = o
	if len(labels) > 0 {
		a.idLabels = labels
	}
	return nil
}

// selectsByLabels reports whether --id-label replaces the workspace as the
// container selector and, with no workspace or configuration flag, the
// configuration source: upstream then reads no configuration, so that a
// configuration in the current folder cannot supply commands or environment.
func (a *application) selectsByLabels(cmd *cobra.Command) bool {
	if len(a.idLabels) == 0 {
		return false
	}
	for _, name := range []string{"workspace-folder", "config", "override-config"} {
		if cmd.Flags().Changed(name) {
			return false
		}
	}
	return true
}

// labelSelectedConfig is the empty configuration of a label-only selection,
// which has no workspace folder.
func (a *application) labelSelectedConfig() (*spec.DevContainer, string, error) {
	a.noWorkspace = true
	workspace, err := filepath.Abs(".")
	return &spec.DevContainer{Raw: json.RawMessage(`{}`)}, workspace, err
}

// placeWorkspace resolves the single-container workspace mount and folder for
// this invocation. Compose configurations and configuration-less selections
// own no workspace mount.
func (a *application) placeWorkspace(dc *spec.DevContainer, local string) error {
	a.placement = nil
	if dc.Path == "" || dc.Mode() == spec.ModeCompose {
		return nil
	}
	opts := workspace.Options{Platform: hostOS}
	if a.mounts != nil {
		opts.MountGitRoot = a.mounts.gitRoot
		opts.MountGitWorktreeCommonDir = a.mounts.worktreeCommonDir
		opts.Consistency = a.mounts.consistency
	}
	hasMount := declaresWorkspaceMount(dc)
	placement, err := workspace.Resolve(local, dc.WorkspaceFolder != "", hasMount, opts)
	if err != nil {
		return err
	}
	a.placement = &placement
	return nil
}

// declaresWorkspaceMount follows upstream's `'workspaceMount' in config`.
func declaresWorkspaceMount(dc *spec.DevContainer) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(dc.Raw, &keys) != nil {
		return dc.WorkspaceMount != ""
	}
	_, ok := keys["workspaceMount"]
	return ok
}

// dockerMounts converts the placement and --mount values for the Docker host.
// External applies to Compose volume declarations only, as upstream. A
// relative bind source resolves against the current directory, as the Docker
// CLI that upstream calls resolves it.
func (a *application) dockerMounts() (*mount.Mount, []mount.Mount, error) {
	var ws *mount.Mount
	var out []mount.Mount
	bind := func(m *workspace.Mount) mount.Mount {
		return mount.Mount{Type: mount.TypeBind, Source: m.Source, Target: m.Target, Consistency: mount.Consistency(m.Consistency)}
	}
	if a.placement != nil {
		if a.placement.WorkspaceMount != nil {
			m := bind(a.placement.WorkspaceMount)
			ws = &m
		}
		if a.placement.AdditionalMount != nil {
			out = append(out, bind(a.placement.AdditionalMount))
		}
	}
	if a.mounts != nil {
		for _, raw := range a.mounts.mounts {
			parts := mountPattern.FindStringSubmatch(raw)
			m := mount.Mount{Type: mount.Type(parts[1]), Source: parts[2], Target: parts[3]}
			if m.Type == mount.TypeBind && !filepath.IsAbs(m.Source) {
				abs, err := filepath.Abs(m.Source)
				if err != nil {
					return nil, nil, err
				}
				m.Source = abs
			}
			out = append(out, m)
		}
	}
	return ws, out, nil
}

// composeMounts returns the --mount values for the Compose host.
func (a *application) composeMounts() []string {
	if a.mounts == nil {
		return nil
	}
	return a.mounts.mounts
}

// findByIDLabels selects the container that carries every id label.
func (a *application) findByIDLabels(ctx context.Context, cli *dockerclient.Client) (string, error) {
	return a.dockerHost(cli).FindByLabels(ctx, a.idLabels)
}

// removeByIDLabels removes only a container that carries the full id-label
// set; a container matching some of the labels is not removed.
func (a *application) removeByIDLabels(ctx context.Context, cli *dockerclient.Client) error {
	id, err := a.findByIDLabels(ctx, cli)
	if errors.Is(err, hostdocker.ErrContainerNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return a.dockerHost(cli).RemoveByID(ctx, id)
}

// workspaceResult is read-configuration's workspace object, in upstream's
// shape: the workspace folder, its mount and the worktree common-folder mount.
func (a *application) workspaceResult(dc *spec.DevContainer, local string) map[string]string {
	out := map[string]string{"workspaceFolder": a.containerWorkspace(dc, local)}
	switch {
	case a.placement != nil && a.placement.WorkspaceMount != nil:
		out["workspaceMount"] = a.placement.WorkspaceMount.String()
	case dc.Mode() != spec.ModeCompose && dc.WorkspaceMount != "":
		out["workspaceMount"] = dc.WorkspaceMount
	}
	if a.placement != nil && a.placement.AdditionalMount != nil {
		out["additionalMountString"] = a.placement.AdditionalMount.String()
	}
	return out
}

// mountedWorkspace returns the folder at which an existing container mounts
// the workspace, read from its bind mounts and workspace label, so commands
// that attach to the container use the placement it was created with, not one
// recomputed from this invocation's flags. A configured workspaceFolder wins,
// a configured Compose service keeps upstream's folder, and a label-only
// selection has no workspace.
func (a *application) mountedWorkspace(dc *spec.DevContainer, local string, info container.InspectResponse) (string, bool) {
	if a.noWorkspace || dc.WorkspaceFolder != "" || dc.Path != "" && dc.Mode() == spec.ModeCompose {
		return "", false
	}
	if info.Config != nil && info.Config.Labels[hostdocker.LabelLocalFolder] != "" {
		local = info.Config.Labels[hostdocker.LabelLocalFolder]
	}
	var binds []workspace.Bind
	for _, m := range info.Mounts {
		if m.Type == mount.TypeBind {
			binds = append(binds, workspace.Bind{Source: m.Source, Destination: m.Destination})
		}
	}
	folder := workspace.MountedFolder(local, binds)
	return folder, folder != ""
}

// attachWorkspace makes an existing container's workspace folder this
// invocation's, for lifecycle working folders and substitution.
func (a *application) attachWorkspace(dc *spec.DevContainer, local string, info container.InspectResponse) {
	if folder, ok := a.mountedWorkspace(dc, local, info); ok {
		a.placement = &workspace.Configuration{WorkspaceFolder: folder}
	}
}
