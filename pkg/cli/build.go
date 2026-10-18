// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

type buildOptions struct {
	cfg               configOptions
	options           builder.Options
	buildArgs, labels []string
}

func (o *buildOptions) flags(cmd *cobra.Command, generate bool) {
	o.cfg.flags(cmd)
	f := cmd.Flags()

	if !generate {
		f.StringArrayVar(&o.options.Tags, "image-name", nil, "Image name (repeat for multiple names)")
		f.StringVar(&o.options.BuildKitAddress, "buildkit-addr", "", "Separate BuildKit address (default: BUILDKIT_HOST, then Docker Engine)")
		f.StringVar(&o.options.Progress, "progress", "plain", "Build progress: auto, plain, tty, rawjson")
		f.BoolVar(&o.options.KeepWorkDir, "keep", false, "Keep staged build artifacts")
	}
	f.StringSliceVar(&o.options.Platforms, "platform", nil, "Target platform(s), for example linux/amd64")
	f.StringVar(&o.options.WorkDir, "work-dir", "", "Staged build directory (default: temporary)")
	f.BoolVar(&o.options.SkipFeatures, "skip-features", false, "Skip feature resolution and installation")
	f.BoolVar(&o.options.InstallVSCodeServer, "vscode-server", false, "Stage VS Code server in the image")
	f.StringVar((*string)(&o.options.VSCode.Quality), "vscode-quality", "stable", "VS Code server channel: stable or insider")
	f.StringVar(&o.options.VSCode.Commit, "vscode-commit", "", "VS Code server commit (default: latest)")
	f.BoolVar(&o.options.VSCode.AlpineLibc, "vscode-alpine", false, "Use the musl VS Code server variant")
	f.Var(&platformValue{target: &o.options.VSCode.Platform}, "vscode-platform", "VS Code server platform override")
	if !generate {
		f.BoolVar(&o.options.Push, "push", false, "Push the resulting image")
		f.BoolVar(&o.options.Load, "load", false, "Load the image into Docker (default unless --push or --output)")
		f.BoolVar(&o.options.NoCache, "no-cache", false, "Disable build cache")
		f.BoolVar(&o.options.Pull, "pull", false, "Always pull referenced images")
		f.StringVar(&o.options.OutputDir, "output", "", "Export image filesystem to a local directory")
		f.StringArrayVar(&o.options.CacheFrom, "cache-from", nil, "Cache import spec (repeat)")
		f.StringArrayVar(&o.options.CacheTo, "cache-to", nil, "Cache export spec (repeat)")
		f.StringArrayVar(&o.buildArgs, "build-arg", nil, "Build argument KEY=VALUE (repeat)")
		f.StringArrayVar(&o.labels, "label", nil, "Image label KEY=VALUE (repeat)")
	}
}

type platformValue struct{ target *platform.Platform }

func (p *platformValue) String() string {
	if p.target.OS == "" {
		return ""
	}
	return p.target.String()
}
func (p *platformValue) Type() string { return "platform" }
func (p *platformValue) Set(s string) error {
	parsed, err := platform.Parse(s)
	if err == nil {
		*p.target = parsed
	}
	return err
}

func (a *application) buildCommand(generate bool) *cobra.Command {
	var opts buildOptions
	name, short := "build", "Build a devcontainer image"
	if generate {
		name, short = "generate", "Generate a Dockerfile and staged build context"
	}
	cmd := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs}
	opts.flags(cmd, generate)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		dc, workspace, err := opts.cfg.load()
		if err != nil {
			return err
		}
		bo, err := opts.prepare(a, dc, workspace, cmd.Flags().Changed("load"))
		if err != nil {
			return err
		}
		if generate {
			res, err := builder.New().Generate(cmd.Context(), bo)
			if err != nil {
				return err
			}
			a.result["dockerfile"] = res.FinalDockerfile
			a.result["contextDir"] = res.ContextDir
			return nil
		}
		_, images, err := a.buildImage(cmd.Context(), dc, workspace, bo)
		if err != nil {
			return err
		}
		a.buildResult(bo, images)
		return nil
	}
	return cmd
}

func (o *buildOptions) prepare(a *application, dc *spec.DevContainer, workspace string, loadExplicit bool) (builder.Options, error) {
	bo := o.options
	if bo.Push && bo.Load {
		return bo, usageError("--push and --load are mutually exclusive")
	}
	if bo.OutputDir != "" && (bo.Push || bo.Load) {
		return bo, usageError("--output cannot be combined with --push or --load")
	}

	var err error
	if bo.BuildArgs, err = parsePairs(o.buildArgs); err != nil {
		return bo, err
	}
	if bo.Labels, err = parsePairs(o.labels); err != nil {
		return bo, err
	}
	bo.Spec, bo.SpecPath, bo.Workspace = dc, dc.Path, workspace
	bo.Logger, bo.Stdout, bo.Stderr = a.logf, a.logs, a.logs
	if len(bo.Tags) == 0 {
		bo.Tags = []string{defaultImage(workspace)}
	}
	if !loadExplicit && !bo.Push && bo.OutputDir == "" {
		bo.Load = true
	}
	return bo, nil
}

// buildImage keeps the Engine API builder and Compose primary-image path in one
// place. Up passes resolved feature metadata from the result to the host.
func (a *application) buildImage(ctx context.Context, dc *spec.DevContainer, workspace string, bo builder.Options) (*builder.Result, []string, error) {
	var result *builder.Result
	var err error
	images := bo.Tags
	if dc.Mode() == spec.ModeCompose {
		var res *dockercompose.BuildPrimaryResult
		res, err = dockercompose.BuildPrimaryImage(ctx, dockercompose.BuildPrimaryInputs{Spec: dc, Workspace: workspace, LocalEnv: envMap(), Tag: firstImage(bo.Tags), BuilderOptions: bo, SkipDefaultLoad: !bo.Load})
		if res != nil {
			result = res.BuildResult
			if len(images) == 0 {
				images = []string{res.Image}
			}
		}
	} else {
		result, err = builder.New().Build(ctx, bo)
	}
	if err != nil {
		if result != nil {
			a.result["contextDir"] = result.ContextDir
		}
		return result, images, err
	}

	return result, images, nil
}

func (a *application) buildResult(bo builder.Options, images []string) {
	switch {
	case bo.OutputDir != "":
		a.result["outputDir"] = bo.OutputDir
	case !bo.Load && !bo.Push:
		a.result["cacheOnly"] = true
	default:
		a.result["imageName"] = images
	}
}

func firstImage(images []string) string {
	if len(images) > 0 {
		return images[0]
	}
	return ""
}
