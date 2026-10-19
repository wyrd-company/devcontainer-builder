// relationships:
//
//	references: [builder, metadata, spec]
package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// BuildPrimaryInputs feeds BuildPrimaryImage.
type BuildPrimaryInputs struct {
	// SkipDefaultLoad leaves BuilderOptions.Load unchanged instead of enabling
	// local loading by default. Push and filesystem exports retain their meaning.
	SkipDefaultLoad bool

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
	// Built is true when a Compose service or derived image was built.
	Built bool
	// BuildResult is non-nil only for a derived build. Its image label owns
	// the full current config/features; pass ImageBuilt: BuildResult != nil
	// to Up. A Compose service build alone does not have that provenance.
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
			Project:             proj,
			OmitSyntaxDirective: in.BuilderOptions.OmitSyntaxDirective,
			Services:            []string{in.Spec.Service},
			BuildKitAddress:     in.BuilderOptions.BuildKitAddress,
			Docker:              in.BuilderOptions.Docker,
			Platforms:           in.BuilderOptions.Platforms,
			NoCache:             in.BuilderOptions.NoCache,
			Pull:                in.BuilderOptions.Pull,
			Progress:            in.BuilderOptions.Progress,
			Stdout:              in.BuilderOptions.Stdout,
			Stderr:              in.BuilderOptions.Stderr,
			Logger:              in.BuilderOptions.Logger,
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

	synthetic, err := primaryBuildSpec(in.Spec, baseImage)
	if err != nil {
		return nil, err
	}

	opts := in.BuilderOptions
	opts.Spec, opts.SpecPath = synthetic, in.Spec.Path
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
	if !in.SkipDefaultLoad && !opts.Push && opts.OutputDir == "" && opts.Output == "" {
		opts.Load = true
	}

	b := builder.New()
	res, err := b.Build(ctx, opts)
	if err != nil {
		return &BuildPrimaryResult{Image: tag, BuildResult: res}, fmt.Errorf("build derived image: %w", err)
	}
	return &BuildPrimaryResult{Image: tag, Built: true, BuildResult: res}, nil
}

func needsDerivedImage(in BuildPrimaryInputs) bool {
	o := in.BuilderOptions
	return in.Spec.Features.Len() > 0 || o.InstallVSCodeServer || len(o.Tags) > 0 || in.Tag != "" || o.Push || o.OutputDir != "" || o.Output != "" || len(o.Labels) > 0 || len(o.Platforms) > 0 || o.Pull || o.NoCache || len(o.CacheFrom) > 0 || len(o.CacheTo) > 0
}

// primaryBuildSpec retains all persistable config properties while selecting
// the Compose service image as the derived build base.
func primaryBuildSpec(dc *spec.DevContainer, baseImage string) (*spec.DevContainer, error) {
	// A derived image owns the full current config contribution. Runtime callers
	// can then use ImageBuilt without losing hooks or duplicating feature entries.
	configBody, err := json.Marshal(metadata.FromConfig(dc))
	if err != nil {
		return nil, fmt.Errorf("synthetic config marshal: %w", err)
	}
	syntheticSpec := map[string]any{}
	if err := json.Unmarshal(configBody, &syntheticSpec); err != nil {
		return nil, err
	}
	syntheticSpec["image"] = baseImage
	syntheticSpec["workspaceFolder"] = dc.WorkspaceFolder
	if dc.Features.Len() > 0 {
		featuresOut := map[string]json.RawMessage{}
		for _, k := range dc.Features.Keys {
			featuresOut[k] = dc.Features.Values[k]
		}
		syntheticSpec["features"] = featuresOut
	}
	if len(dc.OverrideFeature) > 0 {
		syntheticSpec["overrideFeatureInstallOrder"] = dc.OverrideFeature
	}
	body, err := json.MarshalIndent(syntheticSpec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("synthetic spec marshal: %w", err)
	}

	var synthetic spec.DevContainer
	if err := json.Unmarshal(body, &synthetic); err != nil {
		return nil, fmt.Errorf("synthetic spec parse: %w", err)
	}
	synthetic.Path, synthetic.Raw = dc.Path, body
	return &synthetic, nil
}
