// relationships:
//   references: [substitute]

package dockercompose

import (
	"encoding/json"
	"path"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// substituteSpec returns a shallow copy of dc with the string-y fields
// resolved against ctx. Same shape as the docker host's helper of the
// same name; duplicated here so the two host packages stay decoupled.
func substituteSpec(dc *spec.DevContainer, ctx substitute.Context) *spec.DevContainer {
	if dc == nil {
		return nil
	}
	out := *dc
	out.WorkspaceFolder = substitute.Substitute(ctx, dc.WorkspaceFolder)
	out.WorkspaceMount = substitute.Substitute(ctx, dc.WorkspaceMount)
	out.RemoteUser = substitute.Substitute(ctx, dc.RemoteUser)
	out.ContainerUser = substitute.Substitute(ctx, dc.ContainerUser)

	if dc.UpdateRemoteEnv != nil {
		newEnv := make(map[string]any, len(dc.UpdateRemoteEnv))
		for k, v := range dc.UpdateRemoteEnv {
			if s, ok := v.(string); ok {
				newEnv[k] = substitute.Substitute(ctx, s)
			} else {
				newEnv[k] = v
			}
		}
		out.UpdateRemoteEnv = newEnv
	}
	out.OnCreate = substituteCommand(ctx, dc.OnCreate)
	out.UpdateContent = substituteCommand(ctx, dc.UpdateContent)
	out.PostCreate = substituteCommand(ctx, dc.PostCreate)
	out.PostStart = substituteCommand(ctx, dc.PostStart)
	out.PostAttach = substituteCommand(ctx, dc.PostAttach)
	return &out
}

func substituteCommand(ctx substitute.Context, c spec.Command) spec.Command {
	if c.IsZero() {
		return c
	}
	out := spec.Command{}
	if c.Shell != "" {
		out.Shell = substitute.Substitute(ctx, c.Shell)
	}
	if len(c.Args) > 0 {
		out.Args = substitute.Apply(ctx, c.Args)
	}
	if len(c.Parallel) > 0 {
		out.Parallel = make(map[string]spec.Command, len(c.Parallel))
		for k, v := range c.Parallel {
			out.Parallel[k] = substituteCommand(ctx, v)
		}
	}
	return out
}

// substituteContainerEnv resolves ${containerEnv:VAR} on the spec
// fields that legitimately reference it (remoteEnv values + lifecycle
// commands). See the docker host's helper for the rationale.
func substituteContainerEnv(dc *spec.DevContainer, env map[string]string) *spec.DevContainer {
	if dc == nil {
		return nil
	}
	cctx := substitute.ContainerContext{ContainerEnv: env}
	out := *dc
	if dc.UpdateRemoteEnv != nil {
		newEnv := make(map[string]any, len(dc.UpdateRemoteEnv))
		for k, v := range dc.UpdateRemoteEnv {
			if s, ok := v.(string); ok {
				newEnv[k] = substitute.ContainerSubstitute(cctx, s)
			} else {
				newEnv[k] = v
			}
		}
		out.UpdateRemoteEnv = newEnv
	}
	out.OnCreate = containerSubstituteCommand(cctx, dc.OnCreate)
	out.UpdateContent = containerSubstituteCommand(cctx, dc.UpdateContent)
	out.PostCreate = containerSubstituteCommand(cctx, dc.PostCreate)
	out.PostStart = containerSubstituteCommand(cctx, dc.PostStart)
	out.PostAttach = containerSubstituteCommand(cctx, dc.PostAttach)
	return &out
}

func containerSubstituteCommand(ctx substitute.ContainerContext, c spec.Command) spec.Command {
	if c.IsZero() {
		return c
	}
	out := spec.Command{}
	if c.Shell != "" {
		out.Shell = substitute.ContainerSubstitute(ctx, c.Shell)
	}
	if len(c.Args) > 0 {
		out.Args = make([]string, len(c.Args))
		for i, a := range c.Args {
			out.Args[i] = substitute.ContainerSubstitute(ctx, a)
		}
	}
	if len(c.Parallel) > 0 {
		out.Parallel = make(map[string]spec.Command, len(c.Parallel))
		for k, v := range c.Parallel {
			out.Parallel[k] = containerSubstituteCommand(ctx, v)
		}
	}
	return out
}

// stringMap unwraps a map[string]any whose values are strings.
// Anything non-stringly is JSON-encoded.
func stringMap(in map[string]any) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		default:
			b, _ := json.Marshal(t)
			out[k] = string(b)
		}
	}
	return out
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func ensurePosixPath(p string) string {
	return path.Clean("/" + strings.TrimLeft(strings.ReplaceAll(p, "\\", "/"), "/"))
}
