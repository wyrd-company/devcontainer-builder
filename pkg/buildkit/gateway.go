// relationships:
//   references: [llb, metadata]

package buildkit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/errgroup"

	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	dcplatform "github.com/wyrd-company/devcontainer-builder/pkg/platform"
)

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
	// Labels are set on the image config, over any the base image
	// carries.
	Labels map[string]string
}

// SolveDevcontainer drives a devcontainer build through the BuildKit
// gateway. Compared to Solve, it can:
//
//   - Build for multiple target platforms in a single invocation,
//     producing a manifest-list image.
//   - Chain an upstream Dockerfile build directly (via the dockerfile.v0
//     frontend) so the base image never has to round-trip through the
//     local docker store.
func SolveDevcontainer(ctx context.Context, spec BuildSpec, opt SolveOptions) (*Result, error) {
	targetPlatforms, err := parseTargetPlatforms(opt.Platforms, spec.Graph.Platform)
	if err != nil {
		return nil, err
	}
	multi := len(targetPlatforms) > 1
	if spec.Upstream == nil && spec.Graph.BaseImage == "" {
		return nil, errors.New("buildkit: BuildSpec needs either Upstream or Graph.BaseImage")
	}

	localMounts, err := localContextMounts(opt)
	if err != nil {
		return nil, err
	}
	if spec.Upstream != nil {
		if err := addDockerfileMounts(localMounts, spec.Upstream); err != nil {
			return nil, fmt.Errorf("upstream dockerfile: %w", err)
		}
	}

	return runSolve(ctx, opt, multi, localMounts, func(ctx context.Context, conn *Connection, so client.SolveOpt, ch chan *client.SolveStatus) (*client.SolveResponse, error) {
		bs := buildSettings{resolveMode: resolveMode(opt, conn), noCache: opt.NoCache}
		buildFunc := func(ctx context.Context, gc gateway.Client) (*gateway.Result, error) {
			return runBuildFunc(ctx, gc, spec, targetPlatforms, multi, bs)
		}
		return conn.BuildKit.Build(ctx, so, "devcontainer-builder", buildFunc, ch)
	})
}

// buildSettings carries per-solve choices that depend on the BuildKit
// the solve runs against.
type buildSettings struct {
	// resolveMode is a pb.AttrImageResolveMode* value.
	resolveMode string
	noCache     bool
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
	targets []ocispecs.Platform, multi bool, bs buildSettings,
) (*gateway.Result, error) {
	final := gateway.NewResult()
	platformsList := make([]exptypes.Platform, len(targets))
	refs := make([]gateway.Reference, len(targets))
	configs := make([][]byte, len(targets))

	eg, ctx := errgroup.WithContext(ctx)
	for i, tp := range targets {
		i, tp := i, tp
		eg.Go(func() error {
			ref, cfg, err := buildOnePlatform(ctx, gc, spec, tp, bs)
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
// (so each platform sees the right manifest entry, and the daemon's
// BuildKit can use images from its local store).
func buildOnePlatform(
	ctx context.Context, gc gateway.Client, spec BuildSpec, p ocispecs.Platform, bs buildSettings,
) (gateway.Reference, []byte, error) {
	var baseState llb.State
	var baseConfig []byte

	if spec.Upstream != nil {
		ref, cfg, err := solveUpstreamDockerfile(ctx, gc, spec.Upstream, p, bs)
		if err != nil {
			return nil, nil, err
		}
		st, err := ref.ToState()
		if err != nil {
			return nil, nil, fmt.Errorf("upstream ref to state: %w", err)
		}
		baseState = st
		baseConfig = cfg
	} else {
		imageRef, err := normalizeImageRef(spec.Graph.BaseImage)
		if err != nil {
			return nil, nil, err
		}
		if spec.Graph.ResolveImageConfig {
			_, _, cfg, err := gc.ResolveImageConfig(ctx, imageRef, sourceresolver.Opt{
				Platform: &p,
				ImageOpt: &sourceresolver.ResolveImageOpt{ResolveMode: bs.resolveMode},
			})
			if err != nil {
				return nil, nil, fmt.Errorf("resolve base image %s: %w", imageRef, err)
			}
			baseConfig = cfg
		}
		baseState = llb.Image(imageRef,
			llb.WithCustomNamef("FROM %s", imageRef),
			llb.Platform(p),
			llbResolveMode(bs.resolveMode),
		)
	}
	if len(baseConfig) > 0 {
		// Carry the base image's ENV / WORKDIR / USER into the build
		// steps, as a Dockerfile FROM does.
		st, err := baseState.WithImageConfig(baseConfig)
		if err != nil {
			return nil, nil, fmt.Errorf("apply base image config: %w", err)
		}
		baseState = st
	}

	graph := spec.Graph
	graph.Platform = dcplatform.Platform{OS: p.OS, Arch: p.Architecture, Variant: p.Variant}
	if graph.ImageUser == "" {
		graph.ImageUser = imageUser(baseConfig)
	}
	graph.BaseState = &baseState

	// Feature builtins use the metadata's effective users. Keep graph.Spec
	// unchanged for label collection so inherited entries are not appended twice.
	buildGraph := graph
	var baseImage dockerspec.DockerOCIImage
	if len(baseConfig) > 0 {
		if err := json.Unmarshal(baseConfig, &baseImage); err != nil {
			return nil, nil, err
		}
	}
	effective := metadata.Merge(graph.Spec, metadata.Collect(metadata.Read(baseImage.Config.Labels, nil), nil, graph.Spec))
	buildGraph.Spec = effective.DevContainer
	finalState, err := Build(buildGraph)
	if err != nil {
		return nil, nil, fmt.Errorf("author llb: %w", err)
	}

	marshal := []llb.ConstraintsOpt{llb.Platform(p)}
	if bs.noCache {
		marshal = append(marshal, llb.IgnoreCache)
	}
	def, err := finalState.Marshal(ctx, marshal...)
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

	cfg, err := mergeImageConfig(baseConfig, graph, spec.Labels, p)
	if err != nil {
		return nil, nil, fmt.Errorf("image config: %w", err)
	}
	return ref, cfg, nil
}

// mergeImageConfig produces the serialized image config for the final
// image. It clones whatever the base resolver/upstream returned (so
// ENV / USER / WORKDIR / ENTRYPOINT carry through) and then layers in
// the devcontainer's own image-time contributions: containerEnv,
// the ENV from each feature, and labels. Like upstream, the base USER and
// WORKDIR are kept; remoteUser and remoteEnv apply at runtime to exec/attach.
func mergeImageConfig(base []byte, g GraphInputs, labels map[string]string, p ocispecs.Platform) ([]byte, error) {
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

	// Features first, then the spec, so the spec wins (upstream order).
	envIndex := envMap(img.Config.Env)
	for _, f := range g.Features {
		for _, k := range sortedKeys(f.Metadata.ContainerEnv) {
			envIndex[k] = f.Metadata.ContainerEnv[k]
		}
	}
	for _, k := range sortedKeys(toStringMap(g.Spec.ContainerEnv)) {
		envIndex[k] = toStringMap(g.Spec.ContainerEnv)[k]
	}
	img.Config.Env = envSlice(envIndex)

	entries := metadata.CollectWithOptions(metadata.Read(img.Config.Labels, nil), g.Features, g.Spec, g.Metadata)
	value, err := metadata.Encode(entries)
	if err != nil {
		return nil, err
	}
	if img.Config.Labels == nil {
		img.Config.Labels = map[string]string{}
	}
	img.Config.Labels[metadata.Label] = value
	for k, v := range labels {
		img.Config.Labels[k] = v
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
