package builder

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// baseImageUser returns the base image's user for the generated
// Dockerfile's feature builtins, or "" (root) when there are no features
// or the lookup is skipped (SkipImageMetaResolve). When the lookup fails
// (for example, a registry that needs credentials) it logs a warning and
// features see root, so the build still runs.
func (b *Builder) baseImageUser(dc *spec.DevContainer, opts Options, resolved []features.Resolved, logf func(string, ...any)) string {
	if len(resolved) == 0 || opts.SkipImageMetaResolve {
		return ""
	}
	user, err := b.imageUser(dc, opts)
	if err != nil {
		logf("warning: base image user unknown, features see root: %v", err)
		return ""
	}
	return user
}

// imageUser returns the user the devcontainer's base image runs as,
// which features see as the container user when the spec sets no
// containerUser. Like upstream, a Dockerfile-based spec uses the last
// USER of the target stage (following FROM through earlier stages) and
// otherwise the user of the image the stages start from; an image-based
// spec uses that image's user. The image config is read from its
// registry for the build's primary platform.
func (b *Builder) imageUser(dc *spec.DevContainer, opts Options) (string, error) {
	image := dc.Image
	if dfPath := dc.DockerfilePath(); dfPath != "" {
		ctxDir, err := dc.BuildContextDir()
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(dfPath) {
			dfPath = filepath.Join(ctxDir, dfPath)
		}
		target := ""
		if dc.Build != nil {
			target = dc.Build.Target
		}
		user, base, err := dockerfileUser(dfPath, target, mergeArgs(buildArgsFromSpec(dc), opts.BuildArgs))
		if err != nil {
			return "", err
		}
		if user != "" || base == "" {
			return user, nil
		}
		image = base
	}
	if image == "" {
		return "", nil
	}
	ref, err := oci.ParseReference(image)
	if err != nil {
		return "", fmt.Errorf("base image %s: %w", image, err)
	}
	p := primaryPlatform(opts.Platforms)
	cfg, err := b.Resolver.OCI.FetchImageConfig(ref, oci.Platform{OS: p.OS, Architecture: p.Arch, Variant: p.Variant})
	if err != nil {
		return "", fmt.Errorf("base image %s config: %w", image, err)
	}
	return cfg.Config.User, nil
}

// dockerfileUser returns the expanded last USER of the target stage (the
// last stage when target is empty), following FROM through earlier
// stages. When no stage in the chain sets USER it returns the image the
// chain starts from instead.
func dockerfileUser(path, target string, buildArgs map[string]string) (user, baseImage string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	ast, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		return "", "", fmt.Errorf("parse %s: %w", path, err)
	}
	stages, metaArgs, err := instructions.Parse(ast.AST, nil)
	if err != nil {
		return "", "", fmt.Errorf("parse %s: %w", path, err)
	}
	if len(stages) == 0 {
		return "", "", fmt.Errorf("%s has no stages", path)
	}

	lex := shell.NewLex(ast.EscapeToken)
	global := map[string]string{}
	for _, a := range metaArgs {
		for _, kv := range a.Args {
			global[kv.Key] = argValue(kv, buildArgs, global)
		}
	}
	byName := map[string]*instructions.Stage{}
	for i := range stages {
		if stages[i].Name != "" {
			byName[stages[i].Name] = &stages[i]
		}
	}
	stage := &stages[len(stages)-1]
	if target != "" {
		if stage = byName[target]; stage == nil {
			return "", "", fmt.Errorf("%s has no stage %q", path, target)
		}
	}

	seen := map[*instructions.Stage]bool{}
	for stage != nil && !seen[stage] {
		seen[stage] = true
		env := copyMap(global)
		user := ""
		for _, cmd := range stage.Commands {
			switch c := cmd.(type) {
			case *instructions.ArgCommand:
				for _, kv := range c.Args {
					env[kv.Key] = argValue(kv, buildArgs, global)
				}
			case *instructions.EnvCommand:
				for _, kv := range c.Env {
					env[kv.Key] = expand(lex, kv.Value, env)
				}
			case *instructions.UserCommand:
				user = expand(lex, c.User, env)
			}
		}
		if user != "" {
			return user, "", nil
		}
		base := expand(lex, stage.BaseName, global)
		if next := byName[base]; next != nil {
			stage = next
			continue
		}
		return "", base, nil
	}
	return "", "", nil
}

// argValue resolves an ARG: a build arg wins, then the declared
// default, then the global (pre-FROM) value of the same name.
func argValue(kv instructions.KeyValuePairOptional, buildArgs, global map[string]string) string {
	if v, ok := buildArgs[kv.Key]; ok {
		return v
	}
	if kv.Value != nil {
		return *kv.Value
	}
	return global[kv.Key]
}

func expand(lex *shell.Lex, word string, env map[string]string) string {
	pairs := make([]string, 0, len(env))
	for k, v := range env {
		pairs = append(pairs, k+"="+v)
	}
	out, _, err := lex.ProcessWord(word, shell.EnvsFromSlice(pairs))
	if err != nil {
		return word
	}
	return out
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
