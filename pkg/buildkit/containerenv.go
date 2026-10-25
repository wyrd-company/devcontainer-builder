package buildkit

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/moby/buildkit/util/system"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

// envVar is one containerEnv entry after expansion.
type envVar struct{ key, value string }

// expandFeatureEnv returns the feature's containerEnv entries, in
// declaration order, with each value expanded as the reference CLI's
// Dockerfile applies it: one `ENV KEY="value"` instruction per key, with
// only `"` and `\` escaped, so `$NAME`, `${NAME}` and the Dockerfile
// modifiers read the environment as built so far. That environment is env
// (the base image's ENV and every earlier feature's entries), then this
// feature's earlier keys. As the Dockerfile frontend does, PATH reads as
// the platform default when env has none. An unset name expands to the
// empty string; a substitution the Dockerfile rejects is an error.
func expandFeatureEnv(env []string, f features.Metadata, osName string) ([]envVar, error) {
	envList := &llb.EnvList{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		envList = envList.AddOrReplace(k, v)
	}
	if _, ok := envList.Get("PATH"); !ok {
		envList = envList.AddOrReplace("PATH", system.DefaultPathEnv(osName))
	}
	lex := shell.NewLex(parser.DefaultEscapeToken)
	keys := f.ContainerEnvKeys()
	out := make([]envVar, 0, len(keys))
	for _, k := range keys {
		value, _, err := lex.ProcessWord(features.QuoteContainerEnv(f.ContainerEnv[k]), envList)
		if err != nil {
			return nil, fmt.Errorf("feature %s: containerEnv %s: %w", f.ID, k, err)
		}
		envList = envList.AddOrReplace(k, value)
		out = append(out, envVar{key: k, value: value})
	}
	return out, nil
}

// withFeatureEnv sets the feature's containerEnv on state, each value
// expanded against the state's environment as expandFeatureEnv describes.
// The environment is read when the graph is marshalled, after a base image
// config resolved through the meta resolver is known.
func withFeatureEnv(state llb.State, f features.Metadata) llb.State {
	if len(f.ContainerEnv) == 0 {
		return state
	}
	return state.Async(func(ctx context.Context, st llb.State, c *llb.Constraints) (llb.State, error) {
		var opts []llb.ConstraintsOpt
		osName := "linux"
		if c != nil && c.Platform != nil {
			opts = append(opts, llb.Platform(*c.Platform))
			osName = c.Platform.OS
		}
		env, err := st.Env(ctx, opts...)
		if err != nil {
			return llb.State{}, err
		}
		vars, err := expandFeatureEnv(env.ToArray(), f, osName)
		if err != nil {
			return llb.State{}, err
		}
		for _, v := range vars {
			st = st.AddEnv(v.key, v.value)
		}
		return st, nil
	})
}
