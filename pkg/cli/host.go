// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"context"
	"errors"
	"os"
	"runtime"

	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func newDockerClient() (*dockerclient.Client, error) {
	return dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
}

func (a *application) dockerHost(cli *dockerclient.Client) *hostdocker.Host {
	return hostdocker.New(cli, hostdocker.Options{Logger: a.logf, Stdout: a.logs, Stderr: a.logs})
}
func (a *application) composeHost(cli *dockerclient.Client) *dockercompose.Host {
	return dockercompose.New(cli, dockercompose.Options{Logger: a.logf, Stdout: a.logs, Stderr: a.logs})
}

func (a *application) upCommand() *cobra.Command {
	var opts buildOptions
	var skip skipOptions
	var remove bool
	var uidMode string
	cmd := &cobra.Command{Use: "up", Short: "Build, create, start, and run lifecycle commands", Args: cobra.NoArgs}
	opts.flags(cmd, false)
	skip.flags(cmd)
	cmd.Flags().BoolVar(&remove, "remove-existing-container", false, "Remove the workspace container before creating it")
	cmd.Flags().BoolVar(&opts.options.NoCache, "build-no-cache", false, "Disable build cache (upstream up flag)")
	cmd.Flags().StringVar(&uidMode, "update-remote-user-uid-default", "on", "Remote user UID update default: on, off, or never (single-container mode)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		dc, workspace, err := opts.cfg.load()
		if err != nil {
			return err
		}
		uid, err := resolveUIDUpdate(uidMode)
		if err != nil {
			return err
		}

		bo, err := opts.prepare(a, dc, workspace, false)
		if err != nil {
			return err
		}

		cli, err := newDockerClient()
		if err != nil {
			return err
		}
		defer cli.Close()
		bo.Docker = cli
		if dc.Mode() == spec.ModeCompose && len(opts.options.Tags) == 0 {
			bo.Tags = nil
		}
		image := dc.Image
		var built *builder.Result
		// An image-only config needs no derived build. Features, image overlays,
		// Dockerfiles and server staging use the shared builder path.
		if dc.Mode() == spec.ModeCompose || needsBuild(dc, bo) || len(opts.options.Tags) > 0 {
			var images []string
			built, images, err = a.buildImage(cmd.Context(), dc, workspace, bo)
			if err != nil {
				return err
			}
			image = images[0]
		}
		var resolved []features.Resolved
		if built != nil {
			resolved = built.Features
		}
		dc, resolved = skip.apply(dc, resolved)
		if remove {
			if err := a.remove(cmd.Context(), cli, dc, workspace); err != nil {
				return err
			}
		}
		var id string
		if dc.Mode() == spec.ModeCompose {
			res, err := a.composeHost(cli).Up(cmd.Context(), dockercompose.UpRequest{Spec: dc, SpecPath: dc.Path, LocalWorkspace: workspace, PrimaryImage: image, Features: resolved, LocalEnv: envMap()})
			if err != nil {
				return err
			}
			id = res.PrimaryContainerID
			a.result["composeProjectName"] = res.ProjectName
		} else {
			res, err := a.dockerHost(cli).Up(cmd.Context(), hostdocker.UpRequest{Spec: dc, SpecPath: dc.Path, LocalWorkspace: workspace, Image: image, ImageBuilt: built != nil, SkipLifecycle: skip.postCreate, SkipPostAttach: skip.postAttach, Features: resolved, LocalEnv: envMap(), RemoteUserUIDUpdate: uid})
			if err != nil {
				return err
			}
			id = res.ContainerID
		}
		return a.containerResult(cmd.Context(), cli, dc, workspace, id)
	}
	return cmd
}

func needsBuild(dc *spec.DevContainer, bo builder.Options) bool {
	return dc.Build != nil || dc.Dockerfile != "" || dc.Features.Len() > 0 || bo.InstallVSCodeServer || len(bo.Platforms) > 0 || len(bo.Labels) > 0 || bo.Pull || bo.NoCache || len(bo.CacheFrom) > 0 || len(bo.CacheTo) > 0
}

func (a *application) containerResult(ctx context.Context, cli *dockerclient.Client, dc *spec.DevContainer, workspace, id string) error {
	info, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	labels := map[string]string{}
	if info.Config != nil {
		labels = info.Config.Labels
	}
	dc = effectiveContainerConfig(dc, labels)
	user := substitute.Substitute(substitutionContext(dc, workspace, labels), dc.RemoteUser)
	if user == "" && info.Config != nil {
		user = info.Config.User
	}
	if user == "" {
		user = "root"
	}
	a.result["containerId"] = id
	a.result["remoteUser"] = user
	folder := containerWorkspace(dc, workspace)
	if dc.WorkspaceFolder == "" && info.Config != nil && info.Config.WorkingDir != "" {
		folder = info.Config.WorkingDir
	}
	a.result["remoteWorkspaceFolder"] = folder
	return nil
}

func resolveUIDUpdate(mode string) (*hostdocker.RemoteUserUIDUpdate, error) {
	switch mode {
	case "never":
		return nil, nil
	case "on", "off":
		if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
			return nil, nil
		}
	default:
		return nil, usageError("--update-remote-user-uid-default must be on, off, or never")
	}
	uid, gid := os.Getuid(), os.Getgid()
	if uid < 0 || gid < 0 {
		return nil, nil
	}
	return &hostdocker.RemoteUserUIDUpdate{HostUID: uid, HostGID: gid, DefaultOff: mode == "off"}, nil
}

func (a *application) remove(ctx context.Context, cli *dockerclient.Client, dc *spec.DevContainer, workspace string) error {
	var err error
	if dc.Mode() == spec.ModeCompose {
		err = a.composeHost(cli).Remove(ctx, workspace)
	} else {
		err = a.dockerHost(cli).Remove(ctx, workspace)
	}
	if errors.Is(err, hostdocker.ErrContainerNotFound) || errors.Is(err, dockercompose.ErrProjectNotFound) {
		return nil
	}
	return err
}

func (a *application) findContainer(ctx context.Context, cli *dockerclient.Client, dc *spec.DevContainer, workspace, id string) (string, error) {
	if id != "" {
		return id, nil
	}
	if dc.Mode() == spec.ModeCompose {
		info, err := a.composeHost(cli).InspectPrimary(ctx, workspace)
		if err != nil {
			return "", err
		}
		return info.ID, nil
	}
	info, err := a.dockerHost(cli).Inspect(ctx, workspace)
	if err != nil {
		return "", err
	}
	return info.ID, nil
}

func (a *application) manageCommand(inspect bool) *cobra.Command {
	var cfg configOptions
	name, short := "down", "Stop and remove the workspace devcontainer"
	if inspect {
		name, short = "inspect", "Inspect the workspace devcontainer"
	}
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs}
	cfg.flags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		dc, workspace, err := cfg.optionalLoad()
		if err != nil {
			return err
		}
		cli, err := newDockerClient()
		if err != nil {
			return err
		}
		defer cli.Close()

		// Container labels still identify Compose mode if the source config is gone.
		if dc.Path == "" {
			info, lookupErr := a.dockerHost(cli).Inspect(cmd.Context(), workspace)
			if lookupErr != nil {
				if !inspect && errors.Is(lookupErr, hostdocker.ErrContainerNotFound) {
					return nil
				}
				return lookupErr
			}
			if info.Labels[dockercompose.LabelProject] != "" {
				dc.Service = info.Labels[dockercompose.LabelService]
			}
		}
		if !inspect {
			return a.remove(cmd.Context(), cli, dc, workspace)
		}

		id, err := a.findContainer(cmd.Context(), cli, dc, workspace, "")
		if err != nil {
			return err
		}
		info, err := cli.ContainerInspect(cmd.Context(), id)
		if err != nil {
			return err
		}
		a.result["container"] = info
		return a.containerResult(cmd.Context(), cli, dc, workspace, id)
	}
	return cmd
}

func (a *application) execer(cli *dockerclient.Client, dc *spec.DevContainer, id string) runner.Execer {
	if dc.Mode() == spec.ModeCompose {
		return a.composeHost(cli).Execer(id)
	}
	return a.dockerHost(cli).Execer(id)
}

func (a *application) execute(ctx context.Context, cli *dockerclient.Client, dc *spec.DevContainer, req hostdocker.ExecRequest) (*hostdocker.ExecResult, error) {
	if dc.Mode() == spec.ModeCompose {
		res, err := a.composeHost(cli).Exec(ctx, dockercompose.ExecRequest(req))
		if err != nil {
			return nil, err
		}
		converted := hostdocker.ExecResult(*res)
		return &converted, nil
	}
	return a.dockerHost(cli).Exec(ctx, req)
}
