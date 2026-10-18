package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// BuildPrimaryInputs feeds BuildPrimaryImage.
type BuildPrimaryInputs struct {
	// Spec is the parsed devcontainer.json (compose mode).
	Spec *spec.DevContainer
	// Workspace is the host workspace path; used as the build context.
	Workspace string
	// LocalEnv is forwarded to compose interpolation.
	LocalEnv map[string]string
	// Tag is the image tag to assign. When empty, a timestamped tag
	// under "devcontainer-builder/compose-primary" is generated.
	Tag string
	// BuilderOptions tunes the underlying builder. Spec, SpecPath and Workspace
	// are derived from the primary service. Tag overrides the first image name;
	// additional Tags are retained. Load defaults on unless pushing or exporting.
	BuilderOptions builder.Options
}

// BuildPrimaryResult reports the outcome.
type BuildPrimaryResult struct {
	// Image is the image reference compose should use for the primary
	// service. Equal to the primary service's compose `image:` when
	// no feature, server, or export customization requires a derived image.
	Image string
	// Built is true when a derived image was actually built.
	Built bool
	// BuildResult is the underlying builder result, if a build ran.
	BuildResult *builder.Result
}

// BuildPrimaryImage produces the image the primary compose service
// should run with.
//
//   - When the primary declares only `image:` and no feature, server, or
//     build customization requires a derived image, the image is unchanged.
//   - When the primary declares `build:`, that compose-side build is
//     built with BuildKit first to produce a base image. When the
//     caller requests features or build customizations, a derived image is
//     layered on top; otherwise the build's output tag is returned directly.
//   - When features are present, a derived image is built FROM the
//     base (compose `image:` or compose `build:` output) with the
//     features (and the VS Code server, when requested) layered on, and
//     the derived tag is returned.
func BuildPrimaryImage(ctx context.Context, in BuildPrimaryInputs) (*BuildPrimaryResult, error) {
	if in.Spec == nil {
		return nil, fmt.Errorf("BuildPrimaryInputs.Spec is required")
	}
	if in.Spec.Mode() != spec.ModeCompose {
		return nil, fmt.Errorf("spec is not in compose mode")
	}

	proj, err := LoadProject(ctx, LoadInputs{
		Spec:      in.Spec,
		Workspace: in.Workspace,
		LocalEnv:  in.LocalEnv,
	})
	if err != nil {
		return nil, err
	}
	primary, err := proj.GetService(in.Spec.Service)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrPrimaryNotFound, in.Spec.Service)
	}

	if primary.Build != nil {
		copied := *primary.Build
		copied.Args = map[string]*string{}
		for key, value := range primary.Build.Args {
			copied.Args[key] = value
		}
		for key, value := range in.BuilderOptions.BuildArgs {
			v := value
			copied.Args[key] = &v
		}
		copied.Labels = map[string]string{}
		for key, value := range primary.Build.Labels {
			copied.Labels[key] = value
		}
		for key, value := range in.BuilderOptions.Labels {
			copied.Labels[key] = value
		}
		copied.CacheFrom = append(append([]string(nil), copied.CacheFrom...), in.BuilderOptions.CacheFrom...)
		copied.CacheTo = append(append([]string(nil), copied.CacheTo...), in.BuilderOptions.CacheTo...)
		primary.Build = &copied
		proj.Services[in.Spec.Service] = primary
	}
	baseImage := primary.Image
	composeBuilt := false
	if primary.Build != nil {
		bsi, err := BuildServiceImages(ctx, BuildServiceImagesInputs{
			Project:         proj,
			Services:        []string{in.Spec.Service},
			BuildKitAddress: in.BuilderOptions.BuildKitAddress,
			Docker:          in.BuilderOptions.Docker,
			Platforms:       in.BuilderOptions.Platforms,
			NoCache:         in.BuilderOptions.NoCache,
			Pull:            in.BuilderOptions.Pull,
			Progress:        in.BuilderOptions.Progress,
			Stdout:          in.BuilderOptions.Stdout,
			Stderr:          in.BuilderOptions.Stderr,
			Logger:          in.BuilderOptions.Logger,
		})
		if err != nil {
			return nil, err
		}
		baseImage = bsi.Images[in.Spec.Service]
		composeBuilt = true
	}
	if baseImage == "" {
		return nil, fmt.Errorf("primary service %q has neither image nor build", in.Spec.Service)
	}

	if !needsDerivedImage(in) {
		return &BuildPrimaryResult{Image: baseImage, Built: composeBuilt}, nil
	}

	tag := in.Tag
	if tag == "" && len(in.BuilderOptions.Tags) > 0 {
		tag = in.BuilderOptions.Tags[0]
	}
	if tag == "" {
		tag = fmt.Sprintf("devcontainer-builder/compose-primary:%d", time.Now().UnixNano())
	}

	syntheticSpec := map[string]any{
		"image":           baseImage,
		"remoteUser":      in.Spec.RemoteUser,
		"containerUser":   in.Spec.ContainerUser,
		"containerEnv":    in.Spec.ContainerEnv,
		"remoteEnv":       in.Spec.UpdateRemoteEnv,
		"workspaceFolder": in.Spec.WorkspaceFolder,
	}
	if in.Spec.Features.Len() > 0 {
		featuresOut := map[string]json.RawMessage{}
		for _, k := range in.Spec.Features.Keys {
			featuresOut[k] = in.Spec.Features.Values[k]
		}
		syntheticSpec["features"] = featuresOut
	}
	if len(in.Spec.OverrideFeature) > 0 {
		syntheticSpec["overrideFeatureInstallOrder"] = in.Spec.OverrideFeature
	}
	body, err := json.MarshalIndent(syntheticSpec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("synthetic spec marshal: %w", err)
	}

	var synthetic spec.DevContainer
	if err := json.Unmarshal(body, &synthetic); err != nil {
		return nil, fmt.Errorf("synthetic spec parse: %w", err)
	}
	synthetic.Path, synthetic.Raw = in.Spec.Path, body

	opts := in.BuilderOptions
	opts.Spec, opts.SpecPath = &synthetic, in.Spec.Path
	opts.Workspace = in.Workspace
	if in.Spec.Path != "" && opts.FeatureBaseDir == "" {
		opts.FeatureBaseDir = filepath.Dir(in.Spec.Path)
	}
	if len(opts.Tags) == 0 {
		opts.Tags = []string{tag}
	} else {
		opts.Tags = append([]string(nil), opts.Tags...)
		opts.Tags[0] = tag
	}
	if !opts.Push && opts.OutputDir == "" {
		opts.Load = true
	}

	b := builder.New()
	res, err := b.Build(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("build derived image: %w", err)
	}
	return &BuildPrimaryResult{Image: tag, Built: true, BuildResult: res}, nil
}

func needsDerivedImage(in BuildPrimaryInputs) bool {
	o := in.BuilderOptions
	return in.Spec.Features.Len() > 0 || o.InstallVSCodeServer || len(o.Tags) > 0 || in.Tag != "" || o.Push || o.OutputDir != "" || len(o.Labels) > 0 || len(o.Platforms) > 0 || o.Pull || o.NoCache || len(o.CacheFrom) > 0 || len(o.CacheTo) > 0
}
