// Package buildx wraps invocations of `docker buildx build` and friends.
//
// We shell out to docker rather than embedding BuildKit clients directly:
// the CLI is universally available, supports remote and kubernetes
// builders out of the box, and avoids pulling in a heavy dependency tree.
package buildx

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Build describes a single buildx invocation.
type Build struct {
	// ContextDir is the build context (the directory passed as the
	// final positional argument to buildx).
	ContextDir string
	// Dockerfile is the path to the Dockerfile, relative to ContextDir
	// or absolute. Empty means the default ContextDir/Dockerfile.
	Dockerfile string
	// Tags applied to the resulting image.
	Tags []string
	// BuildArgs (--build-arg KEY=VAL).
	BuildArgs map[string]string
	// Labels (--label KEY=VAL).
	Labels map[string]string
	// Platforms (--platform). Empty means the local default.
	Platforms []string
	// Target is the multi-stage target (--target).
	Target string
	// Output is the output mode (--output type=...). When empty and
	// Load is true, --load is used. When empty and Push is true,
	// --push is used. Otherwise the BuildKit default applies.
	Output string
	// CacheFrom / CacheTo entries.
	CacheFrom []string
	CacheTo   []string
	// Builder is the buildx instance name (--builder).
	Builder string
	// Push indicates --push.
	Push bool
	// Load indicates --load (push to local docker image store).
	Load bool
	// NoCache disables the build cache.
	NoCache bool
	// Pull always re-pulls referenced images.
	Pull bool
	// Progress is one of "auto", "plain", "tty", "rawjson". Defaults
	// to "auto" when empty.
	Progress string
	// Secrets passed via --secret.
	Secrets []string
	// SSH passes through --ssh entries.
	SSH []string
	// ExtraArgs are appended verbatim, after the structured options
	// and before the context. Use sparingly.
	ExtraArgs []string

	// Stdout / Stderr default to os.Stdout / os.Stderr when nil.
	Stdout io.Writer
	Stderr io.Writer
	// DockerBin overrides the docker binary path. Defaults to "docker".
	DockerBin string
	// Env is appended to the inherited environment for the docker
	// invocation.
	Env []string
}

// Args renders the buildx command line as an argv slice. Useful for
// logging and for callers that want to spawn the process themselves.
func (b Build) Args() []string {
	args := []string{"buildx", "build"}
	if b.Builder != "" {
		args = append(args, "--builder", b.Builder)
	}
	if b.Dockerfile != "" {
		args = append(args, "-f", b.Dockerfile)
	}
	for _, t := range b.Tags {
		args = append(args, "-t", t)
	}
	for k, v := range b.BuildArgs {
		args = append(args, "--build-arg", fmt.Sprintf("%s=%s", k, v))
	}
	for k, v := range b.Labels {
		args = append(args, "--label", fmt.Sprintf("%s=%s", k, v))
	}
	for _, p := range b.Platforms {
		args = append(args, "--platform", p)
	}
	if b.Target != "" {
		args = append(args, "--target", b.Target)
	}
	for _, c := range b.CacheFrom {
		args = append(args, "--cache-from", c)
	}
	for _, c := range b.CacheTo {
		args = append(args, "--cache-to", c)
	}
	for _, s := range b.Secrets {
		args = append(args, "--secret", s)
	}
	for _, s := range b.SSH {
		args = append(args, "--ssh", s)
	}
	if b.Output != "" {
		args = append(args, "--output", b.Output)
	} else if b.Load {
		args = append(args, "--load")
	} else if b.Push {
		args = append(args, "--push")
	}
	if b.NoCache {
		args = append(args, "--no-cache")
	}
	if b.Pull {
		args = append(args, "--pull")
	}
	if b.Progress != "" {
		args = append(args, "--progress", b.Progress)
	}
	args = append(args, b.ExtraArgs...)
	args = append(args, b.ContextDir)
	return args
}

// Run executes the build.
func (b Build) Run(ctx context.Context) error {
	if b.ContextDir == "" {
		return fmt.Errorf("buildx: ContextDir is required")
	}
	bin := b.DockerBin
	if bin == "" {
		bin = "docker"
	}
	args := b.Args()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = b.Stdout
	cmd.Stderr = b.Stderr
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if len(b.Env) > 0 {
		cmd.Env = append(os.Environ(), b.Env...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// EnsureBuilder returns nil if a buildx builder of the given name exists,
// or creates one if not. An empty name targets the default builder.
func EnsureBuilder(ctx context.Context, name string) error {
	if name == "" {
		// Default builder always exists.
		return nil
	}
	out, err := exec.CommandContext(ctx, "docker", "buildx", "inspect", name).CombinedOutput()
	if err == nil {
		return nil
	}
	if !strings.Contains(string(out), "not found") && !strings.Contains(strings.ToLower(string(out)), "no such") {
		return fmt.Errorf("inspect builder %s: %w: %s", name, err, string(out))
	}
	cmd := exec.CommandContext(ctx, "docker", "buildx", "create", "--name", name, "--use")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Version returns the installed buildx version string.
func Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "buildx", "version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
