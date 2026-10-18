package buildkit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/containerd/platforms"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	fsutil "github.com/tonistiigi/fsutil"
)

// DockerfileBuild describes a Dockerfile built through the builtin
// "dockerfile.v0" frontend: either the base of a devcontainer image
// (BuildSpec.Upstream), solved inside the same gateway build invocation
// as the rest of the LLB graph so no docker round-trip is required, or
// a standalone image (SolveDockerfile).
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
	// Labels become "label:KEY=VAL" frontend opts.
	Labels map[string]string
}

// dockerfileContextLocal / dockerfileDockerfileLocal are the local mount
// names SolveDockerfile uses; they are the dockerfile frontend defaults.
const (
	dockerfileContextLocal    = "context"
	dockerfileDockerfileLocal = "dockerfile"
)

// SolveDockerfile builds a plain Dockerfile through the dockerfile.v0
// frontend and exports the result as opt describes. All of
// opt.Platforms are built; more than one produces a manifest-list image.
func SolveDockerfile(ctx context.Context, b DockerfileBuild, opt SolveOptions) (*Result, error) {
	localMounts, err := localContextMounts(opt)
	if err != nil {
		return nil, err
	}
	if err := addDockerfileMounts(localMounts, &b, dockerfileContextLocal, dockerfileDockerfileLocal); err != nil {
		return nil, err
	}
	return runSolve(ctx, opt, len(opt.Platforms) > 1, localMounts, func(ctx context.Context, conn *Connection, so client.SolveOpt, ch chan *client.SolveStatus) (*client.SolveResponse, error) {
		bs := buildSettings{resolveMode: resolveMode(opt, conn), noCache: opt.NoCache}
		attrs := dockerfileFrontendAttrs(&b, bs, dockerfileContextLocal, dockerfileDockerfileLocal)
		if len(opt.Platforms) > 0 {
			attrs["platform"] = strings.Join(opt.Platforms, ",")
		}
		for k, v := range so.FrontendAttrs {
			attrs[k] = v
		}
		so.Frontend = "dockerfile.v0"
		so.FrontendAttrs = attrs
		return conn.BuildKit.Solve(ctx, nil, so, ch)
	})
}

// addDockerfileMounts registers b's context and Dockerfile directories
// under the given local mount names.
func addDockerfileMounts(mounts map[string]fsutil.FS, b *DockerfileBuild, contextName, dockerfileName string) error {
	if b.ContextDir == "" {
		return errors.New("dockerfile build: ContextDir is required")
	}
	dfDir := b.DockerfileDir
	if dfDir == "" {
		dfDir = b.ContextDir
	}
	ctxFS, err := fsutil.NewFS(b.ContextDir)
	if err != nil {
		return fmt.Errorf("dockerfile context %s: %w", b.ContextDir, err)
	}
	dfFS, err := fsutil.NewFS(dfDir)
	if err != nil {
		return fmt.Errorf("dockerfile dir %s: %w", dfDir, err)
	}
	mounts[contextName] = ctxFS
	mounts[dockerfileName] = dfFS
	return nil
}

// solveUpstreamDockerfile invokes the builtin dockerfile.v0 frontend.
// The local mounts named upstreamContextLocal and upstreamDockerfileLocal
// must be registered on the outer SolveOpt.
func solveUpstreamDockerfile(
	ctx context.Context, gc gateway.Client, b *DockerfileBuild, p ocispecs.Platform, bs buildSettings,
) (gateway.Reference, []byte, error) {
	attrs := dockerfileFrontendAttrs(b, bs, upstreamContextLocal, upstreamDockerfileLocal)
	attrs["platform"] = platforms.Format(p)
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

// dockerfileFrontendAttrs renders b as dockerfile.v0 frontend options
// reading its context and Dockerfile from the named local mounts.
func dockerfileFrontendAttrs(b *DockerfileBuild, bs buildSettings, contextName, dockerfileName string) map[string]string {
	filename := b.Filename
	if filename == "" {
		filename = "Dockerfile"
	}
	attrs := map[string]string{
		"filename":           filename,
		"contextkey":         contextName,
		"dockerfilekey":      dockerfileName,
		"image-resolve-mode": bs.resolveMode,
	}
	if b.Target != "" {
		attrs["target"] = b.Target
	}
	if bs.noCache {
		attrs["no-cache"] = ""
	}
	for _, k := range sortedStringMapKeys(b.BuildArgs) {
		attrs["build-arg:"+k] = b.BuildArgs[k]
	}
	for _, k := range sortedStringMapKeys(b.Labels) {
		attrs["label:"+k] = b.Labels[k]
	}
	return attrs
}
