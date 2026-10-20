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
	"path/filepath"
	"runtime"

	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func newDockerClient() (*dockerclient.Client, error) {
	return dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
}

func (a *application) dockerHost(cli *dockerclient.Client) *hostdocker.Host {
	return hostdocker.New(cli, hostdocker.Options{TerminalSize: a.terminalSize, Logger: a.logf, Stdout: a.logs, Stderr: a.logs, RegistryAuth: registryauth.FromEnvironment()})
}
func (a *application) composeHost(cli *dockerclient.Client) *dockercompose.Host {
	return dockercompose.New(cli, dockercompose.Options{RegistryAuth: registryauth.FromEnvironment(), UserDataFolder: a.userDataFolder, Logger: a.logf, Stdout: a.logs, Stderr: a.logs})
}

func (a *application) upCommand() *cobra.Command {
	var opts buildOptions
	var skip skipOptions
	var controls lifecycleOptions
	var dotfiles dotfilesOptions
	var configuration configurationOptions
	var environment environmentOptions
	var mounts mountOptions
	var remove, allowProjectConflict bool
	cmd := &cobra.Command{Use: "up", Short: "Build, create, start, and run lifecycle commands", Args: cobra.NoArgs}
	opts.flags(cmd, false)
	mounts.flags(cmd)
	skip.flags(cmd)
	controls.flags(cmd)
	gpuAvailabilityFlag(cmd)
	dotfiles.flags(cmd)
	configuration.flags(cmd)
	environment.flags(cmd, true)
	cmd.Flags().BoolVar(&remove, "remove-existing-container", false, "Remove the workspace container before creating it")
	cmd.Flags().BoolVar(&allowProjectConflict, "allow-project-conflict", false, "Allow Compose up to reuse or recreate another workspace's project containers")
	cmd.Flags().BoolVar(&opts.options.NoCache, "build-no-cache", false, "Disable build cache (upstream up flag)")
	cmd.Flags().String("update-remote-user-uid-default", "on", "Remote user UID update default: on, off, or never (Linux default on; macOS requires explicit on; single-container mode)")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		remoteEnv, secrets, err := environment.load(a)
		if err != nil {
			return err
		}
		if err := mounts.prepare(a); err != nil {
			return err
		}
		gpu, err := resolveGPUAvailability(cmd)
		if err != nil {
			return err
		}
		dc, workspace, err := opts.cfg.load()
		if err != nil {
			return err
		}
		if err := a.placeWorkspace(dc, workspace); err != nil {
			return err
		}
		uid, err := resolveUIDUpdate(cmd, runtime.GOOS)
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
		if err := controls.requireExisting(cmd.Context(), cli, dc, workspace, a.idLabels); err != nil {
			return err
		}
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
		if remove {
			if err := a.remove(cmd.Context(), cli, dc, workspace); err != nil {
				return err
			}
		}
		var id string
		if dc.Mode() == spec.ModeCompose {
			req := composeUpRequest(dc, workspace, image, built, skip)
			req.GPUAvailability = gpu
			req.OmitConfigRemoteEnvFromMetadata = bo.Metadata.OmitConfigRemoteEnv
			req.AllowProjectConflict = allowProjectConflict
			req.ContainerDataFolder = folderOption(cmd, "container-data-folder")
			req.ContainerSystemDataFolder = folderOption(cmd, "container-system-data-folder")
			req.Dotfiles = dotfiles.Dotfiles
			req.Lifecycle = controls.LifecycleControls
			req.RemoteEnv, req.Secrets = remoteEnv, secrets
			req.DefaultUserEnvProbe = environment.probe.mode
			req.Mounts = a.composeMounts()
			req.IDLabels = a.idLabels
			host := dockercompose.New(cli, dockercompose.Options{RegistryAuth: registryauth.FromEnvironment(), UserDataFolder: a.userDataFolder, TerminalSize: a.terminalSize, Logger: a.logf, Stdout: a.logs, Stderr: a.logs, NoRecreate: controls.expectExisting})
			res, err := host.Up(cmd.Context(), req)
			if err != nil {
				return err
			}
			id = res.PrimaryContainerID
			a.result["composeProjectName"] = res.ProjectName
		} else {
			req := hostdocker.UpRequest{OmitConfigRemoteEnvFromMetadata: bo.Metadata.OmitConfigRemoteEnv, Spec: dc, SpecPath: dc.Path, LocalWorkspace: workspace, Image: image, ImageBuilt: built != nil, SkipLifecycle: skip.postCreate, SkipPostAttach: skip.postAttach, Features: resolved, LocalEnv: envMap(), RemoteUserUIDUpdate: uid, GPUAvailability: gpu, ContainerDataFolder: folderOption(cmd, "container-data-folder"), ContainerSystemDataFolder: folderOption(cmd, "container-system-data-folder"), Dotfiles: dotfiles.Dotfiles, Lifecycle: controls.LifecycleControls, DefaultUserEnvProbe: environment.probe.mode, RemoteEnv: remoteEnv, Secrets: secrets, IDLabels: a.idLabels}
			if a.placement != nil {
				req.ContainerWorkspace = a.placement.WorkspaceFolder
			}
			req.WorkspaceMount, req.Mounts, err = a.dockerMounts()
			if err != nil {
				return err
			}
			res, err := a.dockerHost(cli).Up(cmd.Context(), req)
			if err != nil {
				return err
			}
			id = res.ContainerID
		}
		if err := configuration.result(a, cmd, cli, dc, workspace, id); err != nil {
			return err
		}
		return a.containerResult(cmd.Context(), cli, dc, workspace, id)
	}
	return cmd
}

// composeUpRequest distinguishes a complete derived image from an unchanged or
// Compose-built base so the host collects current contributions exactly once.
func composeUpRequest(dc *spec.DevContainer, workspace, image string, built *builder.Result, skip skipOptions) dockercompose.UpRequest {
	req := dockercompose.UpRequest{Spec: dc, SpecPath: dc.Path, LocalWorkspace: workspace, PrimaryImage: image, ImageBuilt: built != nil, SkipLifecycle: skip.postCreate, SkipPostAttach: skip.postAttach, LocalEnv: envMap()}
	if built != nil {
		req.Features = built.Features
	}
	return req
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
	user := substitute.Substitute(a.substitutionContext(dc, workspace, labels), dc.RemoteUser)
	if user == "" && info.Config != nil {
		user = info.Config.User
	}
	if user == "" {
		user = "root"
	}
	a.result["containerId"] = id
	a.result["remoteUser"] = user
	a.result["remoteWorkspaceFolder"] = a.remoteWorkspaceFolder(dc, workspace, info)
	return nil
}

// resolveUIDUpdate gates the spec/default decision on the host platform.
// On macOS only an explicit on flag opts into UID remapping.
func resolveUIDUpdate(cmd *cobra.Command, platform string) (*hostdocker.RemoteUserUIDUpdate, error) {
	const flag = "update-remote-user-uid-default"
	mode, err := cmd.Flags().GetString(flag)
	if err != nil {
		return nil, err
	}
	switch mode {
	case "never":
		return nil, nil
	case "on", "off":
		if platform != "linux" && !(platform == "darwin" && mode == "on" && cmd.Flags().Changed(flag)) {
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
	if len(a.idLabels) > 0 && dc.Mode() != spec.ModeCompose {
		return a.removeByIDLabels(ctx, cli)
	}
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
	if len(a.idLabels) > 0 {
		return a.findByIDLabels(ctx, cli)
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
	var selection mountOptions
	name, short := "down", "Stop and remove the workspace devcontainer"
	if inspect {
		name, short = "inspect", "Inspect the workspace devcontainer"
	}
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs}
	cfg.flags(cmd)
	selection.idLabelFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := selection.prepare(a); err != nil {
			return err
		}
		load := cfg.optionalLoad
		if a.selectsByLabels(cmd) {
			load = a.labelSelectedConfig
		}
		// Runtime ownership must be consulted before mutable configuration. This
		// also retries object cleanup after all containers have already disappeared.
		runtimeRemoved := false
		if !inspect && len(a.idLabels) == 0 {
			workspace, err := filepath.Abs(cfg.workspace)
			if err != nil {
				return err
			}
			cli, err := newDockerClient()
			if err != nil {
				return err
			}
			defer cli.Close()
			err = a.composeHost(cli).RemoveRuntime(cmd.Context(), workspace)
			if err != nil && !errors.Is(err, dockercompose.ErrProjectNotFound) {
				return err
			}
			runtimeRemoved = err == nil
		}
		dc, workspace, err := load()
		if err != nil {
			if runtimeRemoved {
				return nil // recorded state was removed despite unavailable configuration
			}
			return err
		}
		cli, err := newDockerClient()
		if err != nil {
			return err
		}
		defer cli.Close()

		// Container labels still identify Compose mode if the source config is gone.
		if dc.Path == "" {
			id, lookupErr := a.findContainer(cmd.Context(), cli, dc, workspace, "")
			if lookupErr != nil {
				if !inspect && errors.Is(lookupErr, hostdocker.ErrContainerNotFound) {
					return nil
				}
				return lookupErr
			}
			info, err := cli.ContainerInspect(cmd.Context(), id)
			if err != nil {
				return err
			}
			if info.Config != nil && info.Config.Labels[dockercompose.LabelProject] != "" {
				dc.Service = info.Config.Labels[dockercompose.LabelService]
				if !inspect && len(a.idLabels) > 0 {
					// A label-selected primary owns its project identity. The
					// current folder is not a teardown selector.
					return a.composeHost(cli).RemoveProject(cmd.Context(), info.Config.Labels[dockercompose.LabelProject], info.Config.Labels[dockercompose.LabelLocalFolder])
				}
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
