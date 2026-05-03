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
// should run with. When the spec has no features, returns the
// primary service's compose `image:` unchanged. When features are
// present, builds a derived image FROM that base with the features
// (and the VS Code server, by default) layered on, and returns the
// derived tag.
//
// The primary service must declare an `image:` in compose; building
// from a compose-side `build:` for the primary is out of scope for
// this iteration (see package doc).
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
	if primary.Image == "" {
		return nil, fmt.Errorf("primary service %q has no image: building from compose-side `build:` for the primary service is out of scope", in.Spec.Service)
	}

	if in.Spec.Features.Len() == 0 {
		return &BuildPrimaryResult{Image: primary.Image}, nil
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
		"image":          primary.Image,
		"remoteUser":     in.Spec.RemoteUser,
		"containerUser":  in.Spec.ContainerUser,
		"containerEnv":   in.Spec.ContainerEnv,
		"remoteEnv":      in.Spec.UpdateRemoteEnv,
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
