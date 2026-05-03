package dockercompose

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"time"

	"github.com/compose-spec/compose-go/v2/types"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildx"
)

// BuildServiceImagesInputs feeds BuildServiceImages.
type BuildServiceImagesInputs struct {
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
	// Builder is the buildx builder name; empty uses the daemon's
	// default builder.
	Builder string
	// Platforms forwarded to buildx (--platform). Empty means the
	// daemon's default. Per-service compose `build.platforms` takes
	// precedence over this when set.
	Platforms []string
	// NoCache disables the build cache.
	NoCache bool
	// Pull always re-pulls referenced images.
	Pull bool
	// Progress one of "auto" | "plain" | "tty" | "rawjson".
	Progress string
	// Stdout / Stderr forwarded to buildx. nil means discard (buildx's
	// own default goes to os.Stdout / os.Stderr; pass io.Discard to
	// silence).
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
	// Built lists service names actually sent through buildx (vs
	// skipped because they had no `build:`).
	Built []string
}

// BuildServiceImages walks every service of proj that declares a
// `build:` block and builds it through `pkg/buildx` (i.e. shells out to
// `docker buildx build`). Returns service-name → resulting tag.
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
// The function is safe to call without a Host: it does its own buildx
// invocation against the local daemon. The Host's Up wraps it for
// convenience but embedders that own the project lifecycle can call
// it directly.
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
		build, err := buildxFromService(in.Project, svc, tag, extraTags, in)
		if err != nil {
			return out, fmt.Errorf("service %q build: %w", name, err)
		}
		logf("building service %q -> %s", name, tag)
		if err := build.Run(ctx); err != nil {
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

// buildxFromService translates a compose ServiceConfig.Build into a
// buildx invocation.
func buildxFromService(
	proj *types.Project, svc types.ServiceConfig,
	tag string, extraTags []string, in BuildServiceImagesInputs,
) (*buildx.Build, error) {
	bc := svc.Build
	if bc == nil {
		return nil, fmt.Errorf("service %q has no build config", svc.Name)
	}
	if bc.DockerfileInline != "" {
		return nil, fmt.Errorf("service %q: dockerfile_inline is not supported", svc.Name)
	}

	contextDir := bc.Context
	if contextDir == "" {
		contextDir = proj.WorkingDir
	}
	if !filepath.IsAbs(contextDir) {
		contextDir = filepath.Clean(filepath.Join(proj.WorkingDir, contextDir))
	}

	df := bc.Dockerfile
	if df != "" && !filepath.IsAbs(df) {
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

	build := &buildx.Build{
		ContextDir: contextDir,
		Dockerfile: df,
		Tags:       append([]string{tag}, extraTags...),
		BuildArgs:  args,
		Labels:     labels,
		Platforms:  platforms,
		Target:     bc.Target,
		CacheFrom:  bc.CacheFrom,
		CacheTo:    bc.CacheTo,
		Builder:    in.Builder,
		Load:       true,
		NoCache:    in.NoCache || bc.NoCache,
		Pull:       in.Pull || bc.Pull,
		Progress:   in.Progress,
		Stdout:     in.Stdout,
		Stderr:     in.Stderr,
	}
	return build, nil
}
