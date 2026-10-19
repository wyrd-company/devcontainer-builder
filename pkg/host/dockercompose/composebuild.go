package dockercompose

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
)

// BuildServiceImagesInputs feeds BuildServiceImages.
type BuildServiceImagesInputs struct {
	OmitSyntaxDirective bool
	// Project is the loaded compose project. Required.
	Project *types.Project
	// Services restricts the build to the named services. When empty,
	// every service in the project that has a `build:` block is built.
	// Names that don't exist in the project are an error.
	Services []string
	// Skip lists service names to leave alone even if they have a
	// `build:` block. Useful when an embedder is baking a particular
	// service through a different path (e.g. BuildPrimaryImage for the
	// devcontainer primary).
	Skip []string
	// BuildKitAddress selects a separate BuildKit. Empty falls back to
	// BUILDKIT_HOST, then to the Docker daemon's built-in BuildKit.
	BuildKitAddress string
	// Docker is the Engine API client the built images are loaded
	// through. When nil, one is created from the DOCKER_* environment.
	Docker *dockerclient.Client
	// Platforms are the target platforms. Empty means the daemon's
	// default. Per-service compose `build.platforms` takes precedence
	// over this when set.
	Platforms []string
	// NoCache disables the build cache.
	NoCache bool
	// Pull always re-pulls referenced images.
	Pull bool
	// Progress one of "auto" | "plain" | "tty" | "rawjson".
	Progress string
	// Stdout / Stderr receive build output. nil means os.Stdout /
	// os.Stderr; pass io.Discard to silence.
	Stdout io.Writer
	Stderr io.Writer
	// Logger receives informational messages.
	Logger func(format string, args ...any)
}

// BuildServiceImagesResult records the per-service image after building.
type BuildServiceImagesResult struct {
	// Images maps service name → built image reference. Only contains
	// services whose `build:` was honored.
	Images map[string]string
	// Built lists service names actually built (vs skipped because
	// they had no `build:`).
	Built []string
}

// BuildServiceImages walks every service of proj that declares a
// `build:` block, builds it with BuildKit's dockerfile.v0 frontend, and
// loads the result into the Docker daemon. Returns service-name →
// resulting tag.
//
// Tag resolution per service:
//
//   - When the service's `build:` declares its own `tags:`, the first
//     entry is used (additional tags are still applied via -t).
//   - Else when the service declares `image:`, that's the tag.
//   - Else a deterministic
//     `devcontainer-builder/compose-svc-<project>-<service>:<unix-nano>`
//     is generated.
//
// The function is safe to call without a Host: it connects to BuildKit
// itself. The Host's Up wraps it for convenience but embedders that own
// the project lifecycle can call it directly.
func BuildServiceImages(ctx context.Context, in BuildServiceImagesInputs) (*BuildServiceImagesResult, error) {
	if in.Project == nil {
		return nil, fmt.Errorf("BuildServiceImagesInputs.Project is required")
	}
	skip := map[string]bool{}
	for _, n := range in.Skip {
		skip[n] = true
	}
	candidates, err := selectBuildableServices(in.Project, in.Services, skip)
	if err != nil {
		return nil, err
	}

	out := &BuildServiceImagesResult{Images: map[string]string{}}
	logf := in.Logger
	if logf == nil {
		logf = func(string, ...any) {}
	}

	for _, name := range candidates {
		svc, _ := in.Project.GetService(name)
		tag, extraTags := resolveBuildTag(in.Project.Name, svc)
		build, opt, err := serviceBuild(in.Project, svc, tag, extraTags, in)
		if err != nil {
			return out, fmt.Errorf("service %q build: %w", name, err)
		}
		logf("building service %q -> %s", name, tag)
		if _, err := buildkit.SolveDockerfile(ctx, build, opt); err != nil {
			return out, fmt.Errorf("service %q build: %w", name, err)
		}
		out.Images[name] = tag
		out.Built = append(out.Built, name)
	}
	sort.Strings(out.Built)
	return out, nil
}

// selectBuildableServices filters proj.Services to those that have a
// non-nil Build, honoring the Services allowlist and Skip blocklist.
func selectBuildableServices(proj *types.Project, allow []string, skip map[string]bool) ([]string, error) {
	var allowSet map[string]bool
	if len(allow) > 0 {
		allowSet = map[string]bool{}
		for _, n := range allow {
			if _, err := proj.GetService(n); err != nil {
				return nil, fmt.Errorf("BuildServiceImages: unknown service %q", n)
			}
			allowSet[n] = true
		}
	}
	out := make([]string, 0, len(proj.Services))
	for _, svc := range proj.Services {
		if svc.Build == nil {
			continue
		}
		if skip[svc.Name] {
			continue
		}
		if allowSet != nil && !allowSet[svc.Name] {
			continue
		}
		out = append(out, svc.Name)
	}
	sort.Strings(out)
	return out, nil
}

// resolveBuildTag returns the primary tag for a service (the one we
// pass via -t and the one we'll mutate svc.Image to point at) plus any
// additional tags from compose's `build.tags`.
func resolveBuildTag(projectName string, svc types.ServiceConfig) (string, []string) {
	if svc.Build != nil && len(svc.Build.Tags) > 0 {
		extras := append([]string(nil), svc.Build.Tags[1:]...)
		// Honor the user's explicit build.tags first; if the service
		// also declares image:, append it so the produced image is
		// also addressable as that.
		if svc.Image != "" {
			extras = append(extras, svc.Image)
		}
		return svc.Build.Tags[0], extras
	}
	if svc.Image != "" {
		return svc.Image, nil
	}
	return fmt.Sprintf("devcontainer-builder/compose-svc-%s-%s:%d", projectName, svc.Name, time.Now().UnixNano()), nil
}

// serviceBuild translates a compose ServiceConfig.Build into a
// Dockerfile build and the options that load its result into the
// Docker daemon.
func serviceBuild(
	proj *types.Project, svc types.ServiceConfig,
	tag string, extraTags []string, in BuildServiceImagesInputs,
) (buildkit.DockerfileBuild, buildkit.SolveOptions, error) {
	bc := svc.Build
	if bc == nil {
		return buildkit.DockerfileBuild{}, buildkit.SolveOptions{}, fmt.Errorf("service %q has no build config", svc.Name)
	}
	if bc.DockerfileInline != "" {
		return buildkit.DockerfileBuild{}, buildkit.SolveOptions{}, fmt.Errorf("service %q: dockerfile_inline is not supported", svc.Name)
	}

	contextDir := bc.Context
	if contextDir == "" {
		contextDir = proj.WorkingDir
	}
	if !filepath.IsAbs(contextDir) {
		contextDir = filepath.Clean(filepath.Join(proj.WorkingDir, contextDir))
	}

	df := bc.Dockerfile
	if df == "" {
		df = "Dockerfile"
	}
	if !filepath.IsAbs(df) {
		df = filepath.Clean(filepath.Join(contextDir, df))
	}

	args := map[string]string{}
	for k, v := range bc.Args {
		if v == nil {
			continue
		}
		args[k] = *v
	}

	labels := map[string]string{}
	for k, v := range bc.Labels {
		labels[k] = v
	}

	platforms := append([]string(nil), in.Platforms...)
	if len(bc.Platforms) > 0 {
		platforms = append(platforms[:0], bc.Platforms...)
	}

	build := buildkit.DockerfileBuild{
		OmitSyntaxDirective: in.OmitSyntaxDirective,
		ContextDir:          contextDir,
		DockerfileDir:       filepath.Dir(df),
		Filename:            filepath.Base(df),
		Target:              bc.Target,
		BuildArgs:           args,
		Labels:              labels,
	}
	opt := buildkit.SolveOptions{
		Address:      in.BuildKitAddress,
		Docker:       in.Docker,
		Tags:         append([]string{tag}, extraTags...),
		Load:         true,
		Platforms:    platforms,
		CacheImports: buildkit.ParseCacheRefs(bc.CacheFrom),
		CacheExports: buildkit.ParseCacheRefs(bc.CacheTo),
		NoCache:      in.NoCache || bc.NoCache,
		Pull:         in.Pull || bc.Pull,
		Progress:     in.Progress,
		Stdout:       in.Stdout,
		Stderr:       in.Stderr,
	}
	return build, opt, nil
}
