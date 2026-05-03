// Package builder is the top-level orchestrator. It loads a
// devcontainer.json, optionally builds the upstream Dockerfile, resolves
// devcontainer features, downloads the VS Code server, and dispatches
// the final image build to either BuildKit (LLB-direct, default) or
// buildx (shell wrapper, fallback).
//
// The wrapper Dockerfile is always written to the work directory for
// inspection and for the `generate` subcommand, even when BuildKit is
// the engine actually performing the build.
package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/buildx"
	"github.com/wyrd-company/devcontainer-builder/pkg/dockerfile"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

// Engine selects which build backend to use.
type Engine string

const (
	// EngineBuildKit drives builds through the BuildKit Go client
	// using LLB authored directly (the default).
	EngineBuildKit Engine = "buildkit"
	// EngineBuildx shells out to `docker buildx build` against the
	// generated wrapper Dockerfile.
	EngineBuildx Engine = "buildx"
)

// Options drive a single Build invocation.
type Options struct {
	// Engine selects the build backend. Defaults to BuildKit.
	Engine Engine

	// SpecPath is an explicit path to a devcontainer.json. When empty,
	// Workspace is searched for the conventional locations.
	SpecPath string
	// Workspace is the workspace folder the spec was authored for. It
	// is used as the default build context root and to discover the
	// spec when SpecPath is empty.
	Workspace string
	// FeatureBaseDir overrides the directory used to resolve relative
	// local feature references. Empty means the devcontainer.json
	// directory.
	FeatureBaseDir string

	// Tags applied to the produced image.
	Tags []string
	// Platforms (e.g., "linux/amd64", "linux/arm64"). The BuildKit
	// engine builds a manifest-list image when more than one is given.
	// --load is incompatible with multi-platform.
	Platforms []string
	// Builder is the buildx instance name (buildx engine only).
	Builder string
	// BuildKitAddress overrides the buildkitd endpoint. Empty falls
	// back to BUILDKIT_HOST and then a unix-socket default.
	BuildKitAddress string
	// BuildArgs / Labels.
	BuildArgs map[string]string
	Labels    map[string]string
	// CacheFrom / CacheTo cache specs (in buildx string form, e.g.
	// "type=registry,ref=...").
	CacheFrom []string
	CacheTo   []string

	// Push uploads the result to the registry described by Tags.
	Push bool
	// Load loads the result into the local docker image store. With
	// the BuildKit engine, this writes a docker tarball under WorkDir
	// and then `docker load`s it.
	Load bool
	// NoCache disables build cache (buildx engine only).
	NoCache bool
	// Pull always re-pulls referenced images (buildx engine only).
	Pull bool
	// Progress one of "auto" | "plain" | "tty" | "rawjson".
	Progress string
	// OutputDir, when non-empty, requests filesystem export to this
	// path (BuildKit engine only).
	OutputDir string

	// SkipFeatures disables feature resolution and installation.
	SkipFeatures bool
	// SkipVSCodeServer disables VS Code server staging.
	SkipVSCodeServer bool
	// SkipImageMetaResolve disables the registry roundtrip that
	// inherits ENV/USER/WORKDIR from the base image (BuildKit engine).
	SkipImageMetaResolve bool

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
	// Stdout / Stderr are forwarded to the build engine.
	Stdout io.Writer
	Stderr io.Writer
}

// Result describes the outcome of a successful build.
type Result struct {
	Engine Engine
	// FinalDockerfile is the path to the wrapper Dockerfile. Always
	// written, regardless of engine.
	FinalDockerfile string
	// ContextDir is the on-disk build context (features + vscode
	// tarball staging directory).
	ContextDir string
	// VSCodeServer reports details on the staged server, if any.
	VSCodeServer *vscode.Result
	// Features is the resolved feature list in install order.
	Features []features.Resolved
	// BuildxArgs is set when Engine=buildx, for logging.
	BuildxArgs []string
	// LoadTarball is set when Engine=buildkit and Load=true, pointing
	// at the docker-format tarball (which has already been loaded into
	// docker by the time Build returns).
	LoadTarball string
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
func (b *Builder) Build(ctx context.Context, opts Options) (*Result, error) {
	logf := opts.Logger
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opts.Engine == "" {
		opts.Engine = EngineBuildKit
	}

	dc, workDir, ctxDir, baseImage, upstream, resolved, sr, dfPath, df, err := b.prepareBuildContext(ctx, opts, logf)
	if err != nil {
		return nil, err
	}
	cleanup := func() {
		if !opts.KeepWorkDir {
			_ = os.RemoveAll(workDir)
		}
	}
	_ = df // already written; retained in case future engines want it inline

	result := &Result{
		Engine:          opts.Engine,
		FinalDockerfile: dfPath,
		ContextDir:      ctxDir,
		VSCodeServer:    sr,
		Features:        resolved,
	}

	switch opts.Engine {
	case EngineBuildx:
		if err := b.runBuildx(ctx, opts, dfPath, ctxDir, dc, baseImage, result, logf); err != nil {
			return result, err
		}
	case EngineBuildKit:
		if err := b.runBuildKit(ctx, opts, ctxDir, dc, baseImage, upstream, resolved, sr, result, logf); err != nil {
			return result, err
		}
	default:
		cleanup()
		return nil, fmt.Errorf("unknown engine %q", opts.Engine)
	}

	cleanup()
	return result, nil
}

func (b *Builder) prepareBuildContext(ctx context.Context, opts Options, logf func(string, ...any)) (
	dc *spec.DevContainer, workDir, ctxDir, baseImage string,
	upstream *buildkit.DockerfileBuild,
	resolved []features.Resolved, sr *vscode.Result,
	dfPath string, df []byte, err error,
) {
	specPath := opts.SpecPath
	if specPath == "" {
		var dctmp *spec.DevContainer
		dctmp, err = spec.LoadFromWorkspace(opts.Workspace)
		if err != nil {
			return
		}
		specPath = dctmp.Path
	}
	dc, err = spec.Load(specPath)
	if err != nil {
		return
	}
	if err = validateBuildSupport(dc, opts.Engine); err != nil {
		return
	}
	logf("loaded spec from %s", specPath)

	workDir = opts.WorkDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "devcontainer-builder-")
		if err != nil {
			return
		}
	} else if err = os.MkdirAll(workDir, 0o755); err != nil {
		return
	}
	logf("staging in %s", workDir)

	ctxDir = filepath.Join(workDir, "context")
	if err = os.MkdirAll(ctxDir, 0o755); err != nil {
		return
	}

	// Determine the base image. The BuildKit engine prefers chaining the
	// upstream dockerfile.v0 frontend in-process (no docker round-trip);
	// the buildx engine still needs a concrete tagged image to FROM.
	baseImage, upstream, err = b.prepareBaseImage(ctx, dc, opts, logf)
	if err != nil {
		return
	}
	switch {
	case upstream != nil:
		logf("base image: dockerfile %s (built in-process via dockerfile.v0)", upstream.Filename)
	default:
		logf("base image: %s", baseImage)
	}

	if !opts.SkipFeatures && dc.Features.Len() > 0 {
		featStaging := filepath.Join(ctxDir, "_features")
		b.Resolver.Logger = logf
		resolved, err = b.Resolver.Resolve(dc.Features, dc.OverrideFeature, featStaging, featureBaseDir(dc, opts))
		if err != nil {
			err = fmt.Errorf("features: %w", err)
			return
		}
		logf("resolved %d feature(s)", len(resolved))
	}

	if !opts.SkipVSCodeServer {
		vs := opts.VSCode
		if vs.Platform.OS == "" {
			vs.Platform = primaryPlatform(opts.Platforms)
		}
		b.Downloader.Logger = logf
		sr, err = b.Downloader.Fetch(ctx, vs, filepath.Join(ctxDir, "_vscode"))
		if err != nil {
			err = fmt.Errorf("vscode server: %w", err)
			return
		}
	}

	var serverCtxPath string
	if sr != nil {
		serverCtxPath = relPath(ctxDir, sr.TarballPath)
	}
	wrapperBase := baseImage
	if wrapperBase == "" && upstream != nil {
		wrapperBase = "<upstream-dockerfile>"
	}
	df, err = dockerfile.Generate(dockerfile.Inputs{
		Spec:                    dc,
		BaseImage:               wrapperBase,
		Features:                resolved,
		FeaturesContextDir:      "_features",
		VSCodeServer:            sr,
		VSCodeServerContextPath: serverCtxPath,
		RemoteUser:              dc.RemoteUser,
	})
	if err != nil {
		err = fmt.Errorf("dockerfile: %w", err)
		return
	}
	dfPath = filepath.Join(workDir, "Dockerfile.devcontainer-builder")
	if err = os.WriteFile(dfPath, df, 0o644); err != nil {
		return
	}
	logf("wrote dockerfile %s (%d bytes)", dfPath, len(df))
	return
}

func (b *Builder) runBuildx(ctx context.Context, opts Options, dfPath, ctxDir string,
	dc *spec.DevContainer, baseImage string, result *Result, logf func(string, ...any),
) error {
	_ = baseImage
	build := buildx.Build{
		ContextDir: ctxDir,
		Dockerfile: dfPath,
		Tags:       opts.Tags,
		BuildArgs:  opts.BuildArgs,
		Labels:     opts.Labels,
		Platforms:  opts.Platforms,
		CacheFrom:  mergeSlices(buildCacheFrom(dc), opts.CacheFrom),
		CacheTo:    opts.CacheTo,
		ExtraArgs:  buildOptions(dc),
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
	result.BuildxArgs = build.Args()
	return build.Run(ctx)
}

func (b *Builder) runBuildKit(ctx context.Context, opts Options, ctxDir string,
	dc *spec.DevContainer, baseImage string, upstream *buildkit.DockerfileBuild,
	resolved []features.Resolved, sr *vscode.Result, result *Result,
	logf func(string, ...any),
) error {
	cacheImports, cacheExports := translateCache(mergeSlices(buildCacheFrom(dc), opts.CacheFrom), opts.CacheTo)
	solveOpts := buildkit.SolveOptions{
		Address:          opts.BuildKitAddress,
		LocalContextName: "context",
		LocalContextDir:  ctxDir,
		Tags:             opts.Tags,
		Push:             opts.Push,
		Load:             opts.Load,
		OutputDir:        opts.OutputDir,
		CacheImports:     cacheImports,
		CacheExports:     cacheExports,
		Platforms:        opts.Platforms,
		Progress:         opts.Progress,
		Stdout:           opts.Stdout,
		Stderr:           opts.Stderr,
		WorkDir:          filepath.Dir(ctxDir),
	}
	if opts.Push && len(opts.Tags) == 0 {
		return fmt.Errorf("--push requires at least one --tag")
	}
	logf("solving via buildkit (addr=%s, platforms=%d)", solveOptsAddr(solveOpts), max(1, len(opts.Platforms)))
	bspec := buildkit.BuildSpec{
		Graph: buildkit.GraphInputs{
			Spec:                  dc,
			BaseImage:             baseImage,
			Features:              resolved,
			VSCodeServer:          sr,
			LocalContextName:      "context",
			FeaturesContextSubdir: "_features",
			RemoteUser:            dc.RemoteUser,
			ResolveImageConfig:    !opts.SkipImageMetaResolve,
		},
		Upstream: upstream,
	}
	res, err := buildkit.SolveDevcontainer(ctx, bspec, solveOpts)
	if err != nil {
		return err
	}
	if res.LoadTarball != "" {
		result.LoadTarball = res.LoadTarball
		logf("loading docker tarball %s", res.LoadTarball)
		if err := dockerLoad(ctx, res.LoadTarball, opts.Stdout, opts.Stderr); err != nil {
			return fmt.Errorf("docker load: %w", err)
		}
	}
	return nil
}

// solveOptsAddr returns a friendly representation of the resolved
// buildkit address for logging.
func solveOptsAddr(opt buildkit.SolveOptions) string {
	if opt.Address != "" {
		return opt.Address
	}
	if v := buildkit.DefaultAddress(); v != "" {
		return v + " (default)"
	}
	return "<unset>"
}

// dockerLoad invokes `docker load -i path`.
func dockerLoad(ctx context.Context, path string, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	c := exec.CommandContext(ctx, "docker", "load", "-i", path)
	c.Stdout = stdout
	c.Stderr = stderr
	return c.Run()
}

// translateCache parses buildx-style "type=...,ref=..." strings into
// CacheRef entries for the BuildKit client.
func translateCache(from, to []string) ([]buildkit.CacheRef, []buildkit.CacheRef) {
	parse := func(s []string) []buildkit.CacheRef {
		out := make([]buildkit.CacheRef, 0, len(s))
		for _, raw := range s {
			ref := buildkit.CacheRef{Attrs: map[string]string{}}
			for _, kv := range strings.Split(raw, ",") {
				eq := strings.IndexByte(kv, '=')
				if eq < 0 {
					continue
				}
				k, v := kv[:eq], kv[eq+1:]
				if k == "type" {
					ref.Type = v
					continue
				}
				ref.Attrs[k] = v
			}
			if ref.Type != "" {
				out = append(out, ref)
			}
		}
		return out
	}
	return parse(from), parse(to)
}

// prepareBaseImage resolves what should appear "above" the devcontainer
// overlays. There are three cases:
//
//   - The spec uses `image` directly: return it as the base.
//   - The spec uses `dockerFile` AND we're on the BuildKit engine: return
//     a *buildkit.DockerfileBuild so the gateway path can chain the
//     dockerfile.v0 frontend with no docker round-trip.
//   - The spec uses `dockerFile` AND we're on the buildx engine: pre-build
//     it via `docker buildx`, load it into the local docker store, and
//     return the temporary tag.
func (b *Builder) prepareBaseImage(
	ctx context.Context, dc *spec.DevContainer, opts Options,
	logf func(format string, args ...any),
) (string, *buildkit.DockerfileBuild, error) {
	if dc.Image != "" && dc.DockerfilePath() == "" {
		return dc.Image, nil, nil
	}
	dfPath := dc.DockerfilePath()
	if dfPath == "" {
		return "", nil, fmt.Errorf("spec must specify either image or dockerFile")
	}

	upstreamCtx, err := dc.BuildContextDir()
	if err != nil {
		return "", nil, err
	}

	if opts.Engine == EngineBuildKit {
		dfFilename := dfPath
		dfDir := upstreamCtx
		if filepath.IsAbs(dfPath) {
			dfDir = filepath.Dir(dfPath)
			dfFilename = filepath.Base(dfPath)
		} else {
			dfFilename = filepath.Base(dfPath)
			if rel := filepath.Dir(dfPath); rel != "." && rel != "" {
				dfDir = filepath.Join(upstreamCtx, rel)
			}
		}
		upstream := &buildkit.DockerfileBuild{
			ContextDir:    upstreamCtx,
			DockerfileDir: dfDir,
			Filename:      dfFilename,
			BuildArgs:     mergeArgs(buildArgsFromSpec(dc), opts.BuildArgs),
		}
		if dc.Build != nil && dc.Build.Target != "" {
			upstream.Target = dc.Build.Target
		}
		return "", upstream, nil
	}

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
		CacheFrom:  mergeSlices(buildCacheFrom(dc), opts.CacheFrom),
		ExtraArgs:  buildOptions(dc),
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
		return "", nil, err
	}
	return tag, nil, nil
}

func validateBuildSupport(dc *spec.DevContainer, engine Engine) error {
	if dc == nil || dc.Build == nil || len(dc.Build.Options) == 0 {
		return nil
	}
	if engine == EngineBuildKit {
		return fmt.Errorf("build.options is not supported by the buildkit engine; use engine=buildx or remove build.options")
	}
	return nil
}

func buildArgsFromSpec(dc *spec.DevContainer) map[string]string {
	if dc.Build == nil {
		return nil
	}
	return dc.Build.Args
}

func buildOptions(dc *spec.DevContainer) []string {
	if dc == nil || dc.Build == nil || len(dc.Build.Options) == 0 {
		return nil
	}
	return append([]string(nil), dc.Build.Options...)
}

func buildCacheFrom(dc *spec.DevContainer) []string {
	if dc == nil || dc.Build == nil || len(dc.Build.CacheFrom) == 0 {
		return nil
	}
	return append([]string(nil), dc.Build.CacheFrom...)
}

func mergeSlices(a, b []string) []string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

func featureBaseDir(dc *spec.DevContainer, opts Options) string {
	if opts.FeatureBaseDir != "" {
		return opts.FeatureBaseDir
	}
	if dc == nil || dc.Path == "" {
		return ""
	}
	return filepath.Dir(dc.Path)
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
