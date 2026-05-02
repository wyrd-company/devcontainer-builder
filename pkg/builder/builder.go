// Package builder is the top-level orchestrator. It loads a
// devcontainer.json, optionally builds the upstream Dockerfile, resolves
// devcontainer features, downloads the VS Code server, generates a final
// Dockerfile that wraps the base image, and dispatches the build through
// buildx.
package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildx"
	"github.com/wyrd-company/devcontainer-builder/pkg/dockerfile"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

// Options drive a single Build invocation.
type Options struct {
	// SpecPath is an explicit path to a devcontainer.json. When empty,
	// Workspace is searched for the conventional locations.
	SpecPath string
	// Workspace is the workspace folder the spec was authored for. It
	// is used as the default build context root and to discover the
	// spec when SpecPath is empty.
	Workspace string

	// Tags applied to the produced image.
	Tags []string
	// Platforms (e.g., "linux/amd64", "linux/arm64").
	Platforms []string
	// Builder buildx instance name. Empty uses the current default.
	Builder string
	// BuildArgs / Labels passed to buildx.
	BuildArgs map[string]string
	Labels    map[string]string
	// CacheFrom / CacheTo specifications.
	CacheFrom []string
	CacheTo   []string

	// Push uploads the result to the registry described by Tags.
	Push bool
	// Load loads the result into the local docker image store.
	Load bool
	// NoCache disables build cache.
	NoCache bool
	// Pull always re-pulls referenced images.
	Pull bool
	// Progress one of "auto" | "plain" | "tty" | "rawjson".
	Progress string

	// SkipFeatures disables feature resolution and installation.
	SkipFeatures bool
	// SkipVSCodeServer disables VS Code server staging.
	SkipVSCodeServer bool

	// VSCode controls server resolution. Zero value = stable, current
	// platform, latest commit.
	VSCode vscode.Spec

	// WorkDir is where intermediate artifacts (the generated build
	// context) are written. When empty, a directory under os.TempDir()
	// is used.
	WorkDir string
	// KeepWorkDir keeps the working directory for inspection after the
	// build. Default removes it.
	KeepWorkDir bool

	// Logger receives informational messages.
	Logger func(format string, args ...any)
	// Stdout / Stderr are forwarded to buildx.
	Stdout io.Writer
	Stderr io.Writer
}

// Result describes the outcome of a successful build.
type Result struct {
	// FinalDockerfile is the path to the wrapper Dockerfile that was
	// fed to buildx.
	FinalDockerfile string
	// ContextDir is the build context that buildx was given.
	ContextDir string
	// VSCodeServer reports details on the staged server, if any.
	VSCodeServer *vscode.Result
	// Features is the resolved feature list in install order.
	Features []features.Resolved
	// BuildxArgs is the rendered argv given to buildx (for logging).
	BuildxArgs []string
}

// Builder is reusable across builds.
type Builder struct {
	Resolver   *features.Resolver
	Downloader *vscode.Downloader
}

// New constructs a Builder with default helpers.
func New() *Builder {
	return &Builder{
		Resolver:   features.New(),
		Downloader: vscode.NewDownloader(),
	}
}

// Build executes a full devcontainer build.
//
// High-level steps:
//
//  1. Load devcontainer.json.
//  2. Determine the base image: either spec.image directly, or build the
//     upstream Dockerfile (spec.dockerFile / spec.build.dockerfile) into
//     a temporary tag and use that as the base.
//  3. Resolve and stage features into a temp directory.
//  4. Download VS Code server tarball into the temp directory.
//  5. Generate the wrapper Dockerfile.
//  6. Run `docker buildx build` against the wrapper.
//
// The temp directory is removed at the end unless KeepWorkDir is set.
func (b *Builder) Build(ctx context.Context, opts Options) (*Result, error) {
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
		_ = dc
	}
	dc, err := spec.Load(specPath)
	if err != nil {
		return nil, err
	}
	logf("loaded spec from %s", specPath)

	workDir := opts.WorkDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "devcontainer-builder-")
		if err != nil {
			return nil, err
		}
	} else {
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return nil, err
		}
	}
	logf("staging in %s", workDir)
	cleanup := func() {
		if !opts.KeepWorkDir {
			_ = os.RemoveAll(workDir)
		}
	}

	// Stage features alongside the upstream context. We use a single
	// "context" subdir that gets passed to buildx as the build context.
	ctxDir := filepath.Join(workDir, "context")
	if err := os.MkdirAll(ctxDir, 0o755); err != nil {
		cleanup()
		return nil, err
	}

	// 1. Determine base image. If the spec uses a Dockerfile, build it
	//    first into an intermediate tag.
	baseImage, err := b.prepareBaseImage(ctx, dc, opts, ctxDir, logf)
	if err != nil {
		cleanup()
		return nil, err
	}
	logf("base image: %s", baseImage)

	// 2. Resolve features into <workDir>/features/<safe-id>.
	var resolvedFeatures []features.Resolved
	if !opts.SkipFeatures && dc.Features.Len() > 0 {
		featStaging := filepath.Join(ctxDir, "_features")
		b.Resolver.Logger = logf
		resolvedFeatures, err = b.Resolver.Resolve(dc.Features, dc.OverrideFeature, featStaging)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("features: %w", err)
		}
		logf("resolved %d feature(s)", len(resolvedFeatures))
	}

	// 3. Download VS Code server.
	var serverResult *vscode.Result
	if !opts.SkipVSCodeServer {
		spec := opts.VSCode
		if spec.Platform.OS == "" {
			spec.Platform = primaryPlatform(opts.Platforms)
		}
		b.Downloader.Logger = logf
		serverResult, err = b.Downloader.Fetch(ctx, spec, filepath.Join(ctxDir, "_vscode"))
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("vscode server: %w", err)
		}
	}

	// 4. Generate wrapper Dockerfile.
	var serverCtxPath string
	if serverResult != nil {
		serverCtxPath = relPath(ctxDir, serverResult.TarballPath)
	}
	df, err := dockerfile.Generate(dockerfile.Inputs{
		Spec:                    dc,
		BaseImage:               baseImage,
		Features:                resolvedFeatures,
		FeaturesContextDir:      "_features",
		VSCodeServer:            serverResult,
		VSCodeServerContextPath: serverCtxPath,
		RemoteUser:              dc.RemoteUser,
	})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("dockerfile: %w", err)
	}
	dfPath := filepath.Join(workDir, "Dockerfile.devcontainer-builder")
	if err := os.WriteFile(dfPath, df, 0o644); err != nil {
		cleanup()
		return nil, err
	}
	logf("wrote dockerfile %s (%d bytes)", dfPath, len(df))

	// 5. Build via buildx.
	build := buildx.Build{
		ContextDir: ctxDir,
		Dockerfile: dfPath,
		Tags:       opts.Tags,
		BuildArgs:  opts.BuildArgs,
		Labels:     opts.Labels,
		Platforms:  opts.Platforms,
		CacheFrom:  opts.CacheFrom,
		CacheTo:    opts.CacheTo,
		Builder:    opts.Builder,
		Push:       opts.Push,
		Load:       opts.Load,
		NoCache:    opts.NoCache,
		Pull:       opts.Pull,
		Progress:   opts.Progress,
		Stdout:     opts.Stdout,
		Stderr:     opts.Stderr,
	}
	logf("running: docker %s", strings.Join(build.Args(), " "))
	if err := build.Run(ctx); err != nil {
		// Don't clean up on failure; let users inspect the work dir.
		return &Result{
			FinalDockerfile: dfPath,
			ContextDir:      ctxDir,
			VSCodeServer:    serverResult,
			Features:        resolvedFeatures,
			BuildxArgs:      build.Args(),
		}, err
	}
	cleanup()
	return &Result{
		FinalDockerfile: dfPath,
		ContextDir:      ctxDir,
		VSCodeServer:    serverResult,
		Features:        resolvedFeatures,
		BuildxArgs:      build.Args(),
	}, nil
}

// prepareBaseImage either returns spec.Image, or builds the upstream
// Dockerfile into a temporary tag that we then use as FROM.
func (b *Builder) prepareBaseImage(
	ctx context.Context, dc *spec.DevContainer, opts Options, ctxDir string,
	logf func(format string, args ...any),
) (string, error) {
	if dc.Image != "" && dc.DockerfilePath() == "" {
		return dc.Image, nil
	}
	dfPath := dc.DockerfilePath()
	if dfPath == "" {
		return "", fmt.Errorf("spec must specify either image or dockerFile")
	}

	upstreamCtx, err := dc.BuildContextDir()
	if err != nil {
		return "", err
	}

	// Build the upstream Dockerfile into a deterministic local tag.
	tag := fmt.Sprintf("devcontainer-builder/base:%d", time.Now().UnixNano())
	logf("building upstream dockerfile %s -> %s", dfPath, tag)

	upstreamDf := dfPath
	if !filepath.IsAbs(upstreamDf) {
		upstreamDf = filepath.Join(upstreamCtx, dfPath)
	}

	build := buildx.Build{
		ContextDir: upstreamCtx,
		Dockerfile: upstreamDf,
		Tags:       []string{tag},
		BuildArgs:  mergeArgs(buildArgsFromSpec(dc), opts.BuildArgs),
		Builder:    opts.Builder,
		Load:       true,
		Pull:       opts.Pull,
		NoCache:    opts.NoCache,
		Progress:   opts.Progress,
		Platforms:  opts.Platforms,
		Stdout:     opts.Stdout,
		Stderr:     opts.Stderr,
	}
	if dc.Build != nil && dc.Build.Target != "" {
		build.Target = dc.Build.Target
	}
	if err := build.Run(ctx); err != nil {
		return "", err
	}
	return tag, nil
}

func buildArgsFromSpec(dc *spec.DevContainer) map[string]string {
	if dc.Build == nil {
		return nil
	}
	return dc.Build.Args
}

func mergeArgs(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func primaryPlatform(p []string) platform.Platform {
	if len(p) == 0 {
		return platform.Current()
	}
	parsed, err := platform.Parse(p[0])
	if err != nil {
		return platform.Current()
	}
	return parsed
}

// relPath returns p relative to base, defaulting to a forward-slash form
// suitable for embedding in a Dockerfile.
func relPath(base, p string) string {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}
