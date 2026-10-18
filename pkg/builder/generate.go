package builder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wyrd-company/devcontainer-builder/pkg/dockerfile"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

// Generate runs every step of Build except the BuildKit solve. It is
// the dry-run companion to Build, useful for inspecting the generated
// Dockerfile or for callers that want to drive a build themselves.
//
// When the spec uses an upstream Dockerfile (rather than spec.image),
// this method does NOT pre-build it — instead, the wrapper Dockerfile's
// FROM line is left as a placeholder string. Callers that want to
// resolve that themselves should use Build directly.
func (b *Builder) Generate(ctx context.Context, opts Options) (*Result, error) {
	logf := opts.Logger
	if logf == nil {
		logf = func(string, ...any) {}
	}

	specPath := opts.SpecPath
	if specPath == "" {
		dc, err := spec.LoadFromWorkspace(opts.Workspace)
		if err != nil {
			return nil, err
		}
		specPath = dc.Path
	}
	dc, err := spec.Load(specPath)
	if err != nil {
		return nil, err
	}

	workDir := opts.WorkDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "devcontainer-builder-gen-")
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	ctxDir := filepath.Join(workDir, "context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		return nil, err
	}

	baseImage := dc.Image
	if baseImage == "" {
		baseImage = "<unresolved-base-build-required>"
	}

	var resolved []features.Resolved
	if !opts.SkipFeatures && dc.Features.Len() > 0 {
		b.Resolver.Logger = logf
		resolved, err = b.Resolver.Resolve(dc.Features, dc.OverrideFeature, filepath.Join(ctxDir, "_features"), featureBaseDir(dc, opts))
		if err != nil {
			return nil, fmt.Errorf("features: %w", err)
		}
	}

	var sr *vscode.Result
	if opts.InstallVSCodeServer {
		spec := opts.VSCode
		if spec.Platform.OS == "" {
			spec.Platform = primaryPlatform(opts.Platforms)
			if spec.Platform.OS == "" {
				spec.Platform = platform.Current()
			}
		}
		b.Downloader.Logger = logf
		sr, err = b.Downloader.Fetch(ctx, spec, filepath.Join(ctxDir, "_vscode"))
		if err != nil {
			return nil, fmt.Errorf("vscode server: %w", err)
		}
	}

	var serverCtx string
	if sr != nil {
		serverCtx = relPath(ctxDir, sr.TarballPath)
	}
	df, err := dockerfile.Generate(dockerfile.Inputs{
		Spec:                    dc,
		BaseImage:               baseImage,
		Features:                resolved,
		FeaturesContextDir:      "_features",
		VSCodeServer:            sr,
		VSCodeServerContextPath: serverCtx,
		RemoteUser:              dc.RemoteUser,
		ImageUser:               b.baseImageUser(dc, opts, resolved, logf),
	})
	if err != nil {
		return nil, err
	}
	dfPath := filepath.Join(workDir, "Dockerfile.devcontainer-builder")
	if err := os.WriteFile(dfPath, df, 0o644); err != nil {
		return nil, err
	}
	logf("wrote dockerfile %s", dfPath)
	return &Result{
		FinalDockerfile: dfPath,
		ContextDir:      ctxDir,
		VSCodeServer:    sr,
		Features:        resolved,
	}, nil
}
