// relationships:
//   references: [generate, metadata, buildkit]

// Package builder is the top-level orchestrator. It loads a
// devcontainer.json, resolves devcontainer features, downloads the VS
// Code server when asked, and builds the image with BuildKit: an
// upstream Dockerfile through the dockerfile.v0 frontend, with the
// devcontainer overlays authored directly in LLB on top.
//
// The wrapper Dockerfile is always written to the work directory for
// inspection and for the `generate` subcommand; it is not what BuildKit
// builds.
package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/internal/datafolder"
	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/dockerfile"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/vscode"
)

// Options drive a single Build invocation.
type Options struct {
	// Spec supplies an already loaded configuration. When set, Build and Generate
	// use this snapshot instead of reading SpecPath or discovering another file.
	// The caller must keep it immutable for the duration of the call. Path retains
	// the original configuration location for relative build and feature paths.
	Spec *spec.DevContainer
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
	// FeatureLockfile is the invocation's lockfile snapshot. The caller publishes
	// it after the complete command succeeds, including up's lifecycle work.
	FeatureLockfile *features.LockfileState

	// Tags applied to the produced image.
	Tags []string
	// Platforms (e.g., "linux/amd64", "linux/arm64"). The BuildKit
	// engine builds a manifest-list image when more than one is given.
	// --load is incompatible with multi-platform.
	Platforms []string
	// BuildKitAddress selects a separate BuildKit. Empty falls back to
	// BUILDKIT_HOST, then to the Docker daemon's built-in BuildKit.
	BuildKitAddress string
	// Docker is the Engine API client used to reach the daemon. When
	// nil, one is created from the standard DOCKER_* environment.
	Docker *dockerclient.Client
	// BuildArgs / Labels.
	BuildArgs map[string]string
	Labels    map[string]string
	// CacheFrom / CacheTo cache specs in `docker build` form, e.g.
	// "type=registry,ref=...".
	CacheFrom []string
	CacheTo   []string

	// Push uploads the result to the registry described by Tags.
	Push bool
	// Load loads the result into the Docker daemon's image store.
	Load bool
	// NoCache disables the build cache.
	NoCache bool
	// Pull always re-pulls referenced images.
	Pull bool
	// Progress one of "auto" | "plain" | "tty" | "rawjson".
	Progress string
	// Output is a buildx exporter specification or a local destination path.
	Output string
	// OutputWriter receives archive output when dest is "-" or omitted.
	OutputWriter io.Writer
	// OutputDir, when non-empty, requests filesystem export to this
	// path.
	OutputDir string

	// SkipFeatures disables feature resolution and installation.
	SkipFeatures bool
	// SkipFeatureAutoMapping disables upstream's legacy feature ID mapping.
	SkipFeatureAutoMapping bool
	// Metadata controls only newly persisted feature/config contributions.
	Metadata metadata.CollectOptions
	// OmitSyntaxDirective ignores authored Dockerfile syntax directives and
	// omits the directive from the generated inspection Dockerfile.
	OmitSyntaxDirective bool
	// InstallVSCodeServer stages the VS Code server into the image.
	// Off by default: editors that attach install their own server.
	InstallVSCodeServer bool
	// SkipImageMetaResolve disables the registry roundtrip that
	// inherits ENV/USER/WORKDIR and metadata from the base image.
	// A locally supplied Docker image is still inspected for metadata labels.
	SkipImageMetaResolve bool

	// VSCode controls server resolution. Zero value = stable, current
	// platform, latest commit.
	VSCode vscode.Spec

	// WorkDir is where intermediate artifacts (the generated build
	// context) are written. When empty, a directory under os.TempDir()
	// is used.
	WorkDir string
	// UserDataFolder contains private staging folders when WorkDir is unset.
	UserDataFolder string
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
	// FinalDockerfile is the path to the inspection-only wrapper
	// Dockerfile.
	FinalDockerfile string
	// ContextDir is the on-disk build context (features + vscode
	// tarball staging directory).
	ContextDir string
	// VSCodeServer reports details on the staged server, if any.
	VSCodeServer *vscode.Result
	// Features is the resolved feature list in install order.
	Features []features.Resolved
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

	dc, workDir, ctxDir, baseImage, upstream, resolved, sr, dfPath, df, err := b.prepareBuildContext(ctx, opts, logf)
	// Preparation can fail after allocating staging. Its returned workDir is
	// set only after allocation succeeds, so cleanup never targets a failed
	// allocation or a caller's unallocated path.
	defer func() {
		if workDir != "" && !opts.KeepWorkDir {
			_ = os.RemoveAll(workDir)
		}
	}()
	_ = df // already written; retained in case future engines want it inline

	result := &Result{
		FinalDockerfile: dfPath,
		ContextDir:      ctxDir,
		VSCodeServer:    sr,
		Features:        resolved,
	}

	if err != nil {
		if workDir == "" {
			return nil, err
		}
		return result, err
	}

	if err := b.runBuildKit(ctx, opts, ctxDir, dc, baseImage, upstream, resolved, sr, logf); err != nil {
		return result, err
	}

	return result, nil
}

func (b *Builder) prepareBuildContext(ctx context.Context, opts Options, logf func(string, ...any)) (
	dc *spec.DevContainer, workDir, ctxDir, baseImage string,
	upstream *buildkit.DockerfileBuild,
	resolved []features.Resolved, sr *vscode.Result,
	dfPath string, df []byte, err error,
) {

	dc, err = loadBuildSpec(opts)
	if err != nil {
		return
	}
	if err = validateBuildSupport(dc); err != nil {
		return
	}
	logf("loaded spec from %s", dc.Path)

	stagingDir := opts.WorkDir
	if stagingDir == "" {
		stagingDir, err = datafolder.NewStaging(opts.UserDataFolder, "devcontainer-builder-")
		if err != nil {
			return
		}
	} else if err = os.MkdirAll(stagingDir, 0o755); err != nil {
		return
	}
	workDir = stagingDir
	logf("staging in %s", workDir)

	contextDir := filepath.Join(workDir, "context")
	if err = os.MkdirAll(contextDir, 0o755); err != nil {
		return
	}
	ctxDir = contextDir

	// Determine the base image: the spec's image, or its Dockerfile,
	// chained in-process through the dockerfile.v0 frontend.
	baseImage, upstream, err = prepareBaseImage(dc, opts)
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
		b.Resolver.SkipAutoMapping = opts.SkipFeatureAutoMapping
		var lock *features.Lockfile
		if opts.FeatureLockfile != nil {
			lock = opts.FeatureLockfile.Lockfile
		}
		resolved, err = b.Resolver.ResolveWithLockfile(ctx, dc.Features, dc.OverrideFeature, featStaging, featureBaseDir(dc, opts), lock)
		if err != nil {
			err = fmt.Errorf("features: %w", err)
			return
		}
		logf("resolved %d feature(s)", len(resolved))
		if err = opts.FeatureLockfile.Prepare(resolved); err != nil {
			return
		}
	}

	if opts.InstallVSCodeServer {
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
	baseMetadata, metadataErr := b.BaseMetadata(ctx, dc, opts)
	if metadataErr != nil {
		err = metadataErr
		return
	}
	df, err = dockerfile.Generate(dockerfile.Inputs{
		Spec:                    dc,
		BaseMetadata:            baseMetadata,
		Metadata:                opts.Metadata,
		OmitSyntaxDirective:     opts.OmitSyntaxDirective,
		BaseImage:               wrapperBase,
		Features:                resolved,
		FeaturesContextDir:      "_features",
		VSCodeServer:            sr,
		VSCodeServerContextPath: serverCtxPath,
		RemoteUser:              dc.RemoteUser,
		ImageUser:               b.baseImageUser(ctx, dc, opts, logf),
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

// featuresLocalName is the session local that exposes the staging
// context (features and the VS Code server). It must differ from the
// "context" local, from which a Dockerfile base reads its build context.
const featuresLocalName = "devcontainer-features"

func (b *Builder) runBuildKit(ctx context.Context, opts Options, ctxDir string,
	dc *spec.DevContainer, baseImage string, upstream *buildkit.DockerfileBuild,
	resolved []features.Resolved, sr *vscode.Result,
	logf func(string, ...any),
) error {
	solveOpts := buildkit.SolveOptions{
		Address:          opts.BuildKitAddress,
		Docker:           opts.Docker,
		LocalContextName: featuresLocalName,
		LocalContextDir:  ctxDir,
		Tags:             opts.Tags,
		Push:             opts.Push,
		Load:             opts.Load,
		Output:           opts.Output,
		OutputWriter:     opts.OutputWriter,
		OutputDir:        opts.OutputDir,
		CacheImports:     buildkit.ParseCacheRefs(mergeSlices(buildCacheFrom(dc), opts.CacheFrom)),
		CacheExports:     buildkit.ParseCacheRefs(opts.CacheTo),
		Platforms:        opts.Platforms,
		NoCache:          opts.NoCache,
		Pull:             opts.Pull,
		Progress:         opts.Progress,
		Stdout:           opts.Stdout,
		Stderr:           opts.Stderr,
	}
	if opts.Push && len(opts.Tags) == 0 {
		return fmt.Errorf("--push requires at least one --tag")
	}
	logf("solving via buildkit (%s, platforms=%d)", buildKitEndpoint(solveOpts), max(1, len(opts.Platforms)))
	bspec := buildkit.BuildSpec{
		Graph: buildkit.GraphInputs{
			Spec:                  dc,
			Metadata:              opts.Metadata,
			BaseImage:             baseImage,
			Features:              resolved,
			VSCodeServer:          sr,
			LocalContextName:      featuresLocalName,
			FeaturesContextSubdir: "_features",
			RemoteUser:            dc.RemoteUser,
			ResolveImageConfig:    !opts.SkipImageMetaResolve,
		},
		Upstream: upstream,
		Labels:   opts.Labels,
	}
	_, err := buildkit.SolveDevcontainer(ctx, bspec, solveOpts)
	return err
}

// buildKitEndpoint names the BuildKit a solve will use, for logging.
func buildKitEndpoint(opt buildkit.SolveOptions) string {
	switch {
	case opt.Address != "":
		return "addr=" + opt.Address
	case buildkit.DefaultAddress() != "":
		return "addr=" + buildkit.DefaultAddress() + " (BUILDKIT_HOST)"
	default:
		return "docker daemon"
	}
}

// prepareBaseImage resolves what should appear "above" the devcontainer
// overlays: the spec's image, or a *buildkit.DockerfileBuild for its
// Dockerfile so the gateway path can chain the dockerfile.v0 frontend.
func prepareBaseImage(dc *spec.DevContainer, opts Options) (string, *buildkit.DockerfileBuild, error) {
	if dc.Image != "" && dc.DockerfilePath() == "" {
		return dc.Image, nil, nil
	}
	dfPath := dc.DockerfileFile()
	if dfPath == "" {
		return "", nil, fmt.Errorf("spec must specify either image or dockerFile")
	}

	upstreamCtx, err := dc.BuildContextDir()
	if err != nil {
		return "", nil, err
	}

	upstream := &buildkit.DockerfileBuild{
		OmitSyntaxDirective: opts.OmitSyntaxDirective,
		ContextDir:          upstreamCtx,
		DockerfileDir:       filepath.Dir(dfPath),
		Filename:            filepath.Base(dfPath),
		BuildArgs:           mergeArgs(buildArgsFromSpec(dc), opts.BuildArgs),
	}
	if dc.Build != nil && dc.Build.Target != "" {
		upstream.Target = dc.Build.Target
	}
	return "", upstream, nil
}

// validateBuildSupport rejects spec fields the BuildKit build cannot
// honor. build.options are `docker build` command-line flags, which
// have no BuildKit equivalent without the docker CLI.
func validateBuildSupport(dc *spec.DevContainer) error {
	if dc == nil || dc.Build == nil || len(dc.Build.Options) == 0 {
		return nil
	}
	return fmt.Errorf("build.options is not supported: dcb builds with BuildKit directly, not the docker CLI; remove build.options")
}

func buildArgsFromSpec(dc *spec.DevContainer) map[string]string {
	if dc.Build == nil {
		return nil
	}
	return dc.Build.Args
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
