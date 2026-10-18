package builder

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
)

// dockerfileUser returns the user the target stage (the last stage when
// target is empty) runs as. It follows BuildKit's dispatch semantics
// (frontend/dockerfile/dockerfile2llb):
//
//   - Global ARGs expand in order over the automatic platform args, a
//     build arg replacing the default.
//   - A stage starts from its parent stage's environment and user (FROM
//     another stage), or from the image's Config.Env and Config.User.
//   - In a stage, ARG takes the build arg, else its default expanded in
//     the current environment, else (with neither) the global value; ENV
//     values expand in the current environment; USER expands likewise.
//
// The image config is fetched only when an expansion needs a variable
// that only the image can define, or when no stage sets USER.
func dockerfileUser(path, target string, buildArgs map[string]string, p platform.Platform, imageConfig func(string) (*oci.ImageConfig, error)) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	ast, err := parser.Parse(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	stages, metaArgs, err := instructions.Parse(ast.AST, nil)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if len(stages) == 0 {
		return "", fmt.Errorf("%s has no stages", path)
	}
	e := &stageEvaluator{
		lex:         shell.NewLex(ast.EscapeToken),
		buildArgs:   buildArgs,
		global:      platformArgs(p),
		byName:      map[string]*instructions.Stage{},
		imageConfig: imageConfig,
	}
	for _, a := range metaArgs {
		for _, kv := range a.Args {
			if v, ok := buildArgs[kv.Key]; ok {
				e.global[kv.Key] = v
			} else if kv.Value != nil {
				e.global[kv.Key] = e.expand(*kv.Value, &env{vars: e.global})
			}
		}
	}
	for i := range stages {
		if stages[i].Name != "" {
			e.byName[stages[i].Name] = &stages[i]
		}
	}
	stage := &stages[len(stages)-1]
	if target != "" {
		if stage = e.byName[target]; stage == nil {
			return "", fmt.Errorf("%s has no stage %q", path, target)
		}
	}
	st, err := e.eval(stage, map[*instructions.Stage]bool{})
	if err != nil {
		return "", err
	}
	user, err := st.user()
	if err != nil {
		return "", err
	}
	if st.root != nil && st.root.err != nil {
		return "", st.root.err
	}
	return user, nil
}

type stageEvaluator struct {
	lex         *shell.Lex
	buildArgs   map[string]string
	global      map[string]string
	byName      map[string]*instructions.Stage
	imageConfig func(string) (*oci.ImageConfig, error)
}

// rootImage loads the config of the image a stage chain starts from on
// first use.
type rootImage struct {
	name   string
	load   func(string) (*oci.ImageConfig, error)
	loaded bool
	cfg    *oci.ImageConfig
	err    error
}

func (r *rootImage) config() (*oci.ImageConfig, error) {
	if !r.loaded {
		r.loaded = true
		if r.name == "scratch" {
			r.cfg = &oci.ImageConfig{}
		} else {
			r.cfg, r.err = r.load(r.name)
		}
	}
	return r.cfg, r.err
}

// env is a stage's environment: variables set in the stage chain over
// the root image's Config.Env. It implements shell.EnvGetter.
type env struct {
	vars map[string]string
	root *rootImage
}

func (e *env) Get(key string) (string, bool) {
	if v, ok := e.vars[key]; ok {
		return v, true
	}
	if e.root == nil {
		return "", false
	}
	cfg, err := e.root.config()
	if err != nil {
		return "", false
	}
	for _, kv := range cfg.Config.Env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v, true
		}
	}
	return "", false
}

func (e *env) Keys() []string {
	keys := make([]string, 0, len(e.vars))
	for k := range e.vars {
		keys = append(keys, k)
	}
	if e.root != nil {
		if cfg, err := e.root.config(); err == nil {
			for _, kv := range cfg.Config.Env {
				k, _, _ := strings.Cut(kv, "=")
				keys = append(keys, k)
			}
		}
	}
	return keys
}

// stageState is the environment and USER at the end of a stage.
type stageState struct {
	env
	userSet bool
	userVal string
}

// user returns the stage's USER, else the root image's.
func (s *stageState) user() (string, error) {
	if s.userSet || s.root == nil {
		return s.userVal, nil
	}
	cfg, err := s.root.config()
	if err != nil {
		return "", err
	}
	return cfg.Config.User, nil
}

func (e *stageEvaluator) eval(stage *instructions.Stage, seen map[*instructions.Stage]bool) (*stageState, error) {
	if seen[stage] {
		return nil, fmt.Errorf("stage %q depends on itself", stage.Name)
	}
	seen[stage] = true
	base := e.expand(stage.BaseName, &env{vars: e.global})
	var st *stageState
	if parent := e.byName[base]; parent != nil {
		ps, err := e.eval(parent, seen)
		if err != nil {
			return nil, err
		}
		st = &stageState{env: env{vars: copyMap(ps.vars), root: ps.root}, userSet: ps.userSet, userVal: ps.userVal}
	} else {
		st = &stageState{env: env{vars: map[string]string{}, root: &rootImage{name: base, load: e.imageConfig}}}
	}
	for _, cmd := range stage.Commands {
		switch c := cmd.(type) {
		case *instructions.ArgCommand:
			for _, kv := range c.Args {
				if v, ok := e.buildArgs[kv.Key]; ok {
					st.vars[kv.Key] = v
				} else if kv.Value != nil {
					st.vars[kv.Key] = e.expand(*kv.Value, &st.env)
				} else if v, ok := e.global[kv.Key]; ok {
					st.vars[kv.Key] = v
				}
			}
		case *instructions.EnvCommand:
			for _, kv := range c.Env {
				st.vars[kv.Key] = e.expand(kv.Value, &st.env)
			}
		case *instructions.UserCommand:
			st.userSet, st.userVal = true, e.expand(c.User, &st.env)
		}
	}
	return st, nil
}

func (e *stageEvaluator) expand(word string, vars shell.EnvGetter) string {
	out, _, err := e.lex.ProcessWord(word, vars)
	if err != nil {
		return word
	}
	return out
}

// platformArgs returns BuildKit's automatic platform ARGs for the target
// platform p, built on the current host.
func platformArgs(p platform.Platform) map[string]string {
	build := platform.Current()
	return map[string]string{
		"BUILDPLATFORM":  build.String(),
		"BUILDOS":        build.OS,
		"BUILDARCH":      build.Arch,
		"BUILDVARIANT":   build.Variant,
		"TARGETPLATFORM": p.String(),
		"TARGETOS":       p.OS,
		"TARGETARCH":     p.Arch,
		"TARGETVARIANT":  p.Variant,
	}
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
