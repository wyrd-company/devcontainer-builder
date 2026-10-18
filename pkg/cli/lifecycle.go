// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

type skipOptions struct{ postCreate, postAttach bool }

func (o *skipOptions) flags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.postCreate, "skip-post-create", false, "Skip all lifecycle commands")
	cmd.Flags().BoolVar(&o.postAttach, "skip-post-attach", false, "Skip postAttachCommand")
}

func (o skipOptions) apply(dc *spec.DevContainer, resolved []features.Resolved) (*spec.DevContainer, []features.Resolved) {
	copy := *dc
	featuresCopy := append([]features.Resolved(nil), resolved...)
	if o.postCreate {
		copy.OnCreate, copy.UpdateContent, copy.PostCreate, copy.PostStart = spec.Command{}, spec.Command{}, spec.Command{}, spec.Command{}
		for i := range featuresCopy {
			m := &featuresCopy[i].Metadata
			m.OnCreateCommand, m.UpdateContent, m.PostCreateCommand, m.PostStartCommand = spec.Command{}, spec.Command{}, spec.Command{}, spec.Command{}
		}
	}
	if o.postCreate || o.postAttach {
		copy.PostAttach = spec.Command{}
		for i := range featuresCopy {
			featuresCopy[i].Metadata.PostAttachCommand = spec.Command{}
		}
	}
	return &copy, featuresCopy
}

func (a *application) lifecycleCommand(name string) *cobra.Command {
	var cfg configOptions
	var id string
	var skip skipOptions
	short := "Run lifecycle commands in an existing devcontainer"
	if name == "set-up" {
		short = "Set up an existing container with devcontainer lifecycle commands"
	}
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs}
	cfg.flags(cmd)
	skip.flags(cmd)
	cmd.Flags().StringVar(&id, "container-id", "", "Target an existing container by ID")
	if name == "set-up" {
		_ = cmd.MarkFlagRequired("container-id")
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("container-id") && id == "" {
			return usageError("--container-id must not be empty")
		}
		dc, workspace, err := cfg.load()
		if err != nil {
			return err
		}
		cli, err := newDockerClient()
		if err != nil {
			return err
		}
		defer cli.Close()
		containerID, err := a.findContainer(cmd.Context(), cli, dc, workspace, id)
		if err != nil {
			return err
		}
		info, err := cli.ContainerInspect(cmd.Context(), containerID)
		if err != nil {
			return err
		}
		execer := a.execer(cli, dc, containerID)
		env, err := runner.DetectContainerEnv(cmd.Context(), execer)
		if err != nil {
			// Match host up: a non-root PID 1 can deny this probe. Lifecycle
			// commands can still use their configured remote environment.
			a.logf("warning: detect container env: %v", err)
			env = map[string]string{}
		}
		home, err := runner.DetectHome(cmd.Context(), execer, dc.RemoteUser)
		if err != nil {
			return err
		}
		// Resolve feature metadata for feature-supplied lifecycle commands. Staging
		// is temporary: the running image already holds the installed feature files.
		resolved, cleanup, err := a.resolveFeatures(dc)
		if err != nil {
			return err
		}
		defer cleanup()
		dc, resolved = skip.apply(dc, resolved)
		plan := lifecycle.Plan(dc, resolved)
		sub := substitutionContext(dc, workspace, info.Config.Labels)
		for i := range plan {
			plan[i].Command = resolveCommand(plan[i].Command, sub, env)
		}
		rt := runner.Runtime{User: dc.RemoteUser, Workdir: containerWorkspace(dc, workspace), Env: resolveRemoteEnv(dc.UpdateRemoteEnv, sub, env), CreatedAt: info.Created, StartedAt: info.State.StartedAt, UserDataFolder: lifecycle.UserDataFolder(home, "")}
		result, err := runner.RunWithResult(cmd.Context(), execer, plan, rt, runner.Options{Logger: a.logf, Stdout: a.logs, Stderr: a.logs})
		if err != nil {
			return err
		}
		a.result["phasesRun"] = phaseResults(result.RanPhases())
		return a.containerResult(cmd.Context(), cli, dc, workspace, containerID)
	}
	return cmd
}

func resolveCommand(c spec.Command, ctx substitute.Context, env map[string]string) spec.Command {
	resolve := func(s string) string {
		return substitute.ContainerSubstitute(substitute.ContainerContext{ContainerEnv: env}, substitute.Substitute(ctx, s))
	}
	out := c
	out.Shell = resolve(c.Shell)
	out.Args = append([]string(nil), c.Args...)
	for i, arg := range out.Args {
		out.Args[i] = resolve(arg)
	}
	if c.Parallel != nil {
		out.Parallel = map[string]spec.Command{}
		for k, sub := range c.Parallel {
			out.Parallel[k] = resolveCommand(sub, ctx, env)
		}
	}
	return out
}

type phaseResult struct {
	Hook   lifecycle.Hook `json:"hook"`
	Origin string         `json:"origin"`
}

func phaseResults(phases []lifecycle.Phase) []phaseResult {
	results := make([]phaseResult, 0, len(phases))
	for _, phase := range phases {
		results = append(results, phaseResult{Hook: phase.Hook, Origin: phase.Origin.Source})
	}
	return results
}
