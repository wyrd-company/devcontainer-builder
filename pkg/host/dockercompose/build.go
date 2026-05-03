package dockercompose

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	// BuilderOptions tunes the underlying builder. Engine,
	// SpecPath, Workspace, Tags, Load are overridden by this helper —
	// the caller's values are ignored for those fields.
	BuilderOptions builder.Options
}

// BuildPrimaryResult reports the outcome.
type BuildPrimaryResult struct {
	// Image is the image reference compose should use for the primary
	// service. Equal to the primary service's compose `image:` when
	// no features are present (no build was needed).
	Image string
	// Built is true when a derived image was actually built.
	Built bool
	// BuildResult is the underlying builder result, if a build ran.
	BuildResult *builder.Result
}

// BuildPrimaryImage produces the image the primary compose service
// should run with.
//
//   - When the primary declares only `image:` and the spec has no
//     features, the compose `image:` is returned unchanged.
//   - When the primary declares `build:`, that compose-side build is
//     run through pkg/buildx first to produce a base image. When the
//     spec has features, a derived image is layered on top; otherwise
//     the build's output tag is returned directly.
//   - When features are present, a derived image is built FROM the
//     base (compose `image:` or compose `build:` output) with the
//     features (and the VS Code server, by default) layered on, and
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

	baseImage := primary.Image
	composeBuilt := false
	if primary.Build != nil {
		bsi, err := BuildServiceImages(ctx, BuildServiceImagesInputs{
			Project:  proj,
			Services: []string{in.Spec.Service},
			Builder:  in.BuilderOptions.Builder,
			NoCache:  in.BuilderOptions.NoCache,
			Pull:     in.BuilderOptions.Pull,
			Progress: in.BuilderOptions.Progress,
			Stdout:   in.BuilderOptions.Stdout,
			Stderr:   in.BuilderOptions.Stderr,
			Logger:   in.BuilderOptions.Logger,
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

	if in.Spec.Features.Len() == 0 {
		return &BuildPrimaryResult{Image: baseImage, Built: composeBuilt}, nil
	}

	tag := in.Tag
	if tag == "" {
		tag = fmt.Sprintf("devcontainer-builder/compose-primary:%d", time.Now().UnixNano())
	}

	syntheticDir, err := os.MkdirTemp("", "dcb-compose-primary-")
	if err != nil {
		return nil, fmt.Errorf("synthetic spec dir: %w", err)
	}
	defer os.RemoveAll(syntheticDir)

	syntheticSpec := map[string]any{
		"image":         baseImage,
		"remoteUser":    in.Spec.RemoteUser,
		"containerUser": in.Spec.ContainerUser,
		"containerEnv":  in.Spec.ContainerEnv,
		"remoteEnv":     in.Spec.UpdateRemoteEnv,
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
	specPath := filepath.Join(syntheticDir, "devcontainer.json")
	if err := os.WriteFile(specPath, body, 0o644); err != nil {
		return nil, fmt.Errorf("write synthetic spec: %w", err)
	}

	opts := in.BuilderOptions
	opts.SpecPath = specPath
	opts.Workspace = in.Workspace
	if in.Spec.Path != "" && opts.FeatureBaseDir == "" {
		opts.FeatureBaseDir = filepath.Dir(in.Spec.Path)
	}
	opts.Tags = []string{tag}
	if !opts.Push {
		opts.Load = true
	}

	b := builder.New()
	res, err := b.Build(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("build derived image: %w", err)
	}
	return &BuildPrimaryResult{Image: tag, Built: true, BuildResult: res}, nil
}
