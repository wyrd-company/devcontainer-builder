package buildkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/util/progress/progressui"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	fsutil "github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"

	dcplatform "github.com/wyrd-company/devcontainer-builder/pkg/platform"
)

// DockerfileBuild describes an upstream Dockerfile that should be built
// via the builtin "dockerfile.v0" frontend and used as the base of the
// devcontainer image. It is solved inside the same gateway build
// invocation as the rest of the LLB graph, so no docker round-trip
// (build-then-pull) is required.
type DockerfileBuild struct {
	// ContextDir is the host directory exposed to the dockerfile
	// frontend as its build context.
	ContextDir string
	// DockerfileDir is the host directory containing the Dockerfile.
	// Empty means use ContextDir.
	DockerfileDir string
	// Filename is the Dockerfile name (default "Dockerfile").
	Filename string
	// Target selects a specific build stage.
	Target string
	// BuildArgs become "build-arg:KEY=VAL" frontend opts.
	BuildArgs map[string]string
}

// BuildSpec is the input to SolveDevcontainer. It bundles the LLB graph
// inputs together with an optional upstream Dockerfile that should be
// built first and chained as the base image.
type BuildSpec struct {
	// Graph is the devcontainer LLB graph description. Its Platform
	// field is overridden per target platform when SolveOptions
	// requests more than one.
	Graph GraphInputs
	// Upstream, when non-nil, is built via the dockerfile.v0 frontend
	// and used as the base for Graph (Graph.BaseImage / BaseState are
	// ignored in that case).
	Upstream *DockerfileBuild
}

// upstreamContextLocal / upstreamDockerfileLocal are local mount names
// reserved for the upstream Dockerfile build. They're independent of the
// devcontainer features local mount so the two contexts don't collide.
const (
	upstreamContextLocal    = "upstream-context"
	upstreamDockerfileLocal = "upstream-dockerfile"
)

// SolveDevcontainer drives a devcontainer build through the BuildKit
// gateway. Compared to Solve, it can:
//
//   - Build for multiple target platforms in a single invocation,
//     producing a manifest-list image.
//   - Chain an upstream Dockerfile build directly (via the dockerfile.v0
//     frontend) so the base image never has to round-trip through the
//     local docker store.
//
// Single-platform, image-spec builds without an upstream Dockerfile are
// still cheaper through Solve, but SolveDevcontainer handles them too.
func SolveDevcontainer(ctx context.Context, spec BuildSpec, opt SolveOptions) (*Result, error) {
	addr := opt.Address
	if addr == "" {
		addr = DefaultAddress()
	}
	if addr == "" {
		return nil, errors.New("buildkit: no address provided and no default could be determined " +
			"(set BUILDKIT_HOST or pass --buildkit-addr)")
	}

	targetPlatforms, err := parseTargetPlatforms(opt.Platforms, spec.Graph.Platform)
	if err != nil {
		return nil, err
	}
	multi := len(targetPlatforms) > 1

	if opt.Load && multi {
		return nil, errors.New("--load is incompatible with multi-platform builds " +
			"(docker's image store only stores single-platform images)")
	}
	if spec.Upstream == nil && spec.Graph.BaseImage == "" {
		return nil, errors.New("buildkit: BuildSpec needs either Upstream or Graph.BaseImage")
	}

	c, err := client.New(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer c.Close()

	stderr := opt.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	exports, loadTarball, err := buildExportsForGateway(opt, multi)
	if err != nil {
		return nil, err
	}

	localMounts := map[string]fsutil.FS{}
	if opt.LocalContextName != "" && opt.LocalContextDir != "" {
		fs, err := fsutil.NewFS(opt.LocalContextDir)
		if err != nil {
			return nil, fmt.Errorf("local mount %s: %w", opt.LocalContextDir, err)
		}
		localMounts[opt.LocalContextName] = fs
	}
	if spec.Upstream != nil {
		ctxDir := spec.Upstream.ContextDir
		if ctxDir == "" {
			return nil, errors.New("upstream dockerfile: ContextDir is required")
		}
		dfDir := spec.Upstream.DockerfileDir
		if dfDir == "" {
			dfDir = ctxDir
		}
		ctxFS, err := fsutil.NewFS(ctxDir)
		if err != nil {
			return nil, fmt.Errorf("upstream context %s: %w", ctxDir, err)
		}
		dfFS, err := fsutil.NewFS(dfDir)
		if err != nil {
			return nil, fmt.Errorf("upstream dockerfile dir %s: %w", dfDir, err)
		}
		localMounts[upstreamContextLocal] = ctxFS
		localMounts[upstreamDockerfileLocal] = dfFS
	}

	cacheImports := make([]client.CacheOptionsEntry, 0, len(opt.CacheImports))
	for _, ci := range opt.CacheImports {
		cacheImports = append(cacheImports, client.CacheOptionsEntry{Type: ci.Type, Attrs: ci.Attrs})
	}
	cacheExports := make([]client.CacheOptionsEntry, 0, len(opt.CacheExports))
	for _, ce := range opt.CacheExports {
		cacheExports = append(cacheExports, client.CacheOptionsEntry{Type: ce.Type, Attrs: ce.Attrs})
	}

	statusCh := make(chan *client.SolveStatus)
	display, err := progressui.NewDisplay(stderr, displayMode(opt.Progress))
	if err != nil {
		return nil, fmt.Errorf("progress display: %w", err)
	}

	buildFunc := func(ctx context.Context, gc gateway.Client) (*gateway.Result, error) {
		return runBuildFunc(ctx, gc, spec, targetPlatforms, multi)
	}

	g, gctx := errgroup.WithContext(ctx)
	var resp *client.SolveResponse
	g.Go(func() error {
		var solveErr error
		resp, solveErr = c.Build(gctx, client.SolveOpt{
			Exports:       exports,
			LocalMounts:   localMounts,
			CacheImports:  cacheImports,
			CacheExports:  cacheExports,
			FrontendAttrs: opt.FrontendAttrs,
		}, "devcontainer-builder", buildFunc, statusCh)
		return solveErr
	})
	g.Go(func() error {
		_, derr := display.UpdateFrom(ctx, statusCh)
		return derr
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return &Result{LoadTarball: loadTarball, SolveResponse: resp}, nil
}

// parseTargetPlatforms converts the SolveOptions list into ocispecs
// platforms. When the list is empty, the GraphInputs Platform is used
// if set, falling back to a single nil entry meaning "daemon default".
func parseTargetPlatforms(in []string, fallback dcplatform.Platform) ([]ocispecs.Platform, error) {
	if len(in) > 0 {
		out := make([]ocispecs.Platform, 0, len(in))
		for _, s := range in {
			p, err := platforms.Parse(s)
			if err != nil {
				return nil, fmt.Errorf("parse platform %q: %w", s, err)
			}
			out = append(out, platforms.Normalize(p))
		}
		return out, nil
	}
	if fallback.OS != "" && fallback.Arch != "" {
		return []ocispecs.Platform{platforms.Normalize(ocispecs.Platform{
			OS:           fallback.OS,
			Architecture: fallback.Arch,
			Variant:      fallback.Variant,
		})}, nil
	}
	return []ocispecs.Platform{platforms.DefaultSpec()}, nil
}

// runBuildFunc executes once per Build invocation. It builds the per-
// platform refs in parallel and assembles a (possibly multi-platform)
// gateway.Result that the chosen exporter then turns into the final
// image artifact.
func runBuildFunc(
	ctx context.Context, gc gateway.Client, spec BuildSpec,
	targets []ocispecs.Platform, multi bool,
) (*gateway.Result, error) {
	final := gateway.NewResult()
	platformsList := make([]exptypes.Platform, len(targets))
	refs := make([]gateway.Reference, len(targets))
	configs := make([][]byte, len(targets))

	eg, ctx := errgroup.WithContext(ctx)
	for i, tp := range targets {
		i, tp := i, tp
		eg.Go(func() error {
			ref, cfg, err := buildOnePlatform(ctx, gc, spec, tp)
			if err != nil {
				return fmt.Errorf("build %s: %w", platforms.Format(tp), err)
			}
			refs[i] = ref
			configs[i] = cfg
			platformsList[i] = exptypes.Platform{
				ID:       platforms.Format(tp),
				Platform: tp,
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}

	if multi {
		for i, p := range platformsList {
			final.AddRef(p.ID, refs[i])
			final.AddMeta(fmt.Sprintf("%s/%s", exptypes.ExporterImageConfigKey, p.ID), configs[i])
		}
		dt, err := json.Marshal(&exptypes.Platforms{Platforms: platformsList})
		if err != nil {
			return nil, fmt.Errorf("marshal platforms metadata: %w", err)
		}
		final.AddMeta(exptypes.ExporterPlatformsKey, dt)
	} else {
		final.SetRef(refs[0])
		final.AddMeta(exptypes.ExporterImageConfigKey, configs[0])
	}
	return final, nil
}

// buildOnePlatform produces a Reference plus a serialized image config
// for a single target platform. When BuildSpec.Upstream is set, the
// dockerfile.v0 frontend is solved first and its result becomes the
// base; otherwise the spec's BaseImage is resolved through the gateway
// (so each platform sees the right manifest entry).
func buildOnePlatform(
	ctx context.Context, gc gateway.Client, spec BuildSpec, p ocispecs.Platform,
) (gateway.Reference, []byte, error) {
	var baseState llb.State
	var baseConfig []byte

	if spec.Upstream != nil {
		ref, cfg, err := solveUpstreamDockerfile(ctx, gc, spec.Upstream, p)
		if err != nil {
			return nil, nil, err
		}
		st, err := ref.ToState()
		if err != nil {
			return nil, nil, fmt.Errorf("upstream ref to state: %w", err)
		}
		baseState = st
		baseConfig = cfg
	} else if spec.Graph.ResolveImageConfig {
		_, _, cfg, err := gc.ResolveImageConfig(ctx, spec.Graph.BaseImage, sourceresolver.Opt{
			Platform: &p,
			ImageOpt: &sourceresolver.ResolveImageOpt{},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("resolve base image %s: %w", spec.Graph.BaseImage, err)
		}
		baseConfig = cfg
	}

	graph := spec.Graph
	graph.Platform = dcplatform.Platform{OS: p.OS, Arch: p.Architecture, Variant: p.Variant}
	if spec.Upstream != nil {
		graph.BaseState = &baseState
	}

	finalState, err := Build(graph)
	if err != nil {
		return nil, nil, fmt.Errorf("author llb: %w", err)
	}

	def, err := finalState.Marshal(ctx, llb.Platform(p))
	if err != nil {
		return nil, nil, fmt.Errorf("marshal llb: %w", err)
	}

	res, err := gc.Solve(ctx, gateway.SolveRequest{
		Definition: def.ToPB(),
		Evaluate:   true,
	})
	if err != nil {
		return nil, nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, nil, err
	}

	cfg, err := mergeImageConfig(baseConfig, graph, p)
	if err != nil {
		return nil, nil, fmt.Errorf("image config: %w", err)
	}
	return ref, cfg, nil
}

// solveUpstreamDockerfile invokes the builtin dockerfile.v0 frontend.
// The local mounts named upstreamContextLocal and upstreamDockerfileLocal
// must be registered on the outer SolveOpt.
func solveUpstreamDockerfile(
	ctx context.Context, gc gateway.Client, b *DockerfileBuild, p ocispecs.Platform,
) (gateway.Reference, []byte, error) {
	filename := b.Filename
	if filename == "" {
		filename = "Dockerfile"
	}
	attrs := map[string]string{
		"filename":      filename,
		"contextkey":    upstreamContextLocal,
		"dockerfilekey": upstreamDockerfileLocal,
		"platform":      platforms.Format(p),
	}
	if b.Target != "" {
		attrs["target"] = b.Target
	}
	for _, k := range sortedStringMapKeys(b.BuildArgs) {
		attrs["build-arg:"+k] = b.BuildArgs[k]
	}
	res, err := gc.Solve(ctx, gateway.SolveRequest{
		Frontend:    "dockerfile.v0",
		FrontendOpt: attrs,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("dockerfile.v0 solve: %w", err)
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, nil, err
	}
	cfg := res.Metadata[exptypes.ExporterImageConfigKey]
	return ref, cfg, nil
}

// mergeImageConfig produces the serialized image config for the final
// image. It clones whatever the base resolver/upstream returned (so
// ENV / USER / WORKDIR / ENTRYPOINT carry through) and then layers in
// the devcontainer's own contributions: containerEnv, remoteEnv,
// workspaceFolder, remoteUser, plus the ENV from each feature.
func mergeImageConfig(base []byte, g GraphInputs, p ocispecs.Platform) ([]byte, error) {
	var img dockerspec.DockerOCIImage
	if len(base) > 0 {
		if err := json.Unmarshal(base, &img); err != nil {
			return nil, fmt.Errorf("decode base image config: %w", err)
		}
	}
	img.OS = p.OS
	img.Architecture = p.Architecture
	if p.Variant != "" {
		img.Variant = p.Variant
	}

	envIndex := envMap(img.Config.Env)
	for _, k := range sortedKeys(toStringMap(g.Spec.ContainerEnv)) {
		envIndex[k] = toStringMap(g.Spec.ContainerEnv)[k]
	}
	for _, f := range g.Features {
		for _, k := range sortedKeys(f.Metadata.ContainerEnv) {
			envIndex[k] = f.Metadata.ContainerEnv[k]
		}
	}
	for _, k := range sortedKeys(toStringMap(g.Spec.UpdateRemoteEnv)) {
		envIndex[k] = toStringMap(g.Spec.UpdateRemoteEnv)[k]
	}
	img.Config.Env = envSlice(envIndex)

	if g.Spec.WorkspaceFolder != "" {
		img.Config.WorkingDir = g.Spec.WorkspaceFolder
	}
	if u := remoteUser(g); u != "" && u != "root" {
		img.Config.User = u
	}
	return json.Marshal(img)
}

// envMap parses an existing ENV slice ("KEY=VAL") into a map preserving
// the last value seen for any duplicate key.
func envMap(in []string) map[string]string {
	out := map[string]string{}
	for _, kv := range in {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out[kv] = ""
			continue
		}
		out[kv[:eq]] = kv[eq+1:]
	}
	return out
}

func envSlice(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildExportsForGateway is buildExports with multi-platform aware
// tweaks: the image exporter is allowed to omit a tag when push is
// false (caller may want the gateway-cache export to still happen),
// and the multi-platform image exporter sets `name` only when push
// is true (otherwise BuildKit emits a meaningful warning).
func buildExportsForGateway(opt SolveOptions, multi bool) ([]client.ExportEntry, string, error) {
	if opt.Push && opt.Load {
		return nil, "", errors.New("--push and --load are mutually exclusive")
	}
	var exports []client.ExportEntry
	var loadPath string
	switch {
	case opt.Push:
		if len(opt.Tags) == 0 {
			return nil, "", errors.New("--push requires at least one tag")
		}
		attrs := map[string]string{
			"name":           joinTags(opt.Tags),
			"push":           "true",
			"name-canonical": "true",
		}
		exports = append(exports, client.ExportEntry{
			Type:  client.ExporterImage,
			Attrs: attrs,
		})
	case opt.Load:
		if multi {
			return nil, "", errors.New("--load cannot be combined with multi-platform")
		}
		if len(opt.Tags) == 0 {
			return nil, "", errors.New("--load requires at least one tag")
		}
		dest := opt.LoadWriter
		if dest == nil {
			if opt.WorkDir == "" {
				return nil, "", errors.New("--load needs a WorkDir or LoadWriter")
			}
			loadPath = filepath.Join(opt.WorkDir, "image.tar")
			f, err := os.Create(loadPath)
			if err != nil {
				return nil, "", err
			}
			dest = f
		}
		exports = append(exports, client.ExportEntry{
			Type:   client.ExporterDocker,
			Attrs:  map[string]string{"name": joinTags(opt.Tags)},
			Output: outputFunc(dest),
		})
	case opt.OutputDir != "":
		if err := os.MkdirAll(opt.OutputDir, 0o755); err != nil {
			return nil, "", err
		}
		exports = append(exports, client.ExportEntry{
			Type:      client.ExporterLocal,
			OutputDir: opt.OutputDir,
		})
	}
	return exports, loadPath, nil
}

