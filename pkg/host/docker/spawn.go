package docker

import (
	"context"
	"encoding/json"
	"path"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// keepaliveCmd is the entrypoint upstream uses to keep the container
// running after it's been created. It traps SIGTERM so `docker stop`
// exits cleanly, and uses the `sleep & wait $!` pattern so the trap
// actually fires (a bare `sleep infinity` would block trap delivery
// until it returns).
const keepaliveCmd = `echo Container started
trap "exit 0" 15
exec "$@"
while sleep 1 & wait $!; do :; done`

// findExisting returns the container ID of a previously-created
// devcontainer for localFolder, or "" if none exists. Honors only the
// devcontainer.local_folder label (the same key upstream uses), so a
// container created by the upstream CLI is reusable here and vice
// versa.
func (h *Host) findExisting(ctx context.Context, localFolder string) (string, error) {
	args := filters.NewArgs(filters.Arg("label", LabelLocalFolder+"="+localFolder))
	list, err := h.client.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return "", errorf("list containers: %w", err)
	}
	if len(list) == 0 {
		return "", nil
	}
	// If there are multiple, prefer the most recently created. The list
	// is normally already ordered newest-first by the daemon.
	return list[0].ID, nil
}

// buildContainerConfig assembles the container.Config and HostConfig
// from a substituted spec. Variable substitution is the caller's
// responsibility (UpRequest already does it), so the strings here are
// final.
func buildContainerConfig(
	dc *spec.DevContainer,
	resolved []features.Resolved,
	image, containerWorkspaceFolder, localFolder, configFilePath string,
) (*container.Config, *container.HostConfig, error) {
	envSlice := mapToEnvSlice(stringMap(dc.ContainerEnv))
	for _, f := range resolved {
		for k, v := range f.Metadata.ContainerEnv {
			envSlice = upsertEnv(envSlice, k, v)
		}
	}

	labels := map[string]string{
		LabelLocalFolder: localFolder,
	}
	if configFilePath != "" {
		labels[LabelConfigFile] = configFilePath
	}

	cfg := &container.Config{
		Image:      image,
		Env:        envSlice,
		Labels:     labels,
		WorkingDir: containerWorkspaceFolder,
		// Match upstream: shell entrypoint that keeps the container
		// alive until SIGTERM.
		Entrypoint: strslice.StrSlice{"/bin/sh"},
		Cmd:        strslice.StrSlice{"-c", keepaliveCmd, "-"},
		StopSignal: "SIGTERM",
	}
	if dc.ContainerUser != "" {
		cfg.User = dc.ContainerUser
	}

	host := &container.HostConfig{
		AutoRemove: false,
	}
	if dc.Init != nil && *dc.Init {
		t := true
		host.Init = &t
	}
	if dc.Privileged != nil && *dc.Privileged {
		host.Privileged = true
	}
	if len(dc.CapAdd) > 0 {
		host.CapAdd = append(host.CapAdd, dc.CapAdd...)
	}
	if len(dc.SecurityOpt) > 0 {
		host.SecurityOpt = append(host.SecurityOpt, dc.SecurityOpt...)
	}

	mounts := []mount.Mount{}
	if m := workspaceMount(dc, localFolder, containerWorkspaceFolder); m != nil {
		mounts = append(mounts, *m)
	}
	for _, raw := range dc.Mounts {
		if m := parseMount(raw); m != nil {
			mounts = append(mounts, *m)
		}
	}
	host.Mounts = mounts

	if len(dc.RunArgs) > 0 {
		if err := applyRunArgs(dc.RunArgs, cfg, host); err != nil {
			return nil, nil, err
		}
	}

	return cfg, host, nil
}

// workspaceMount returns the bind mount for the workspace, honoring
// dc.WorkspaceMount when set. Returns nil for an empty workspace
// (allowed: the user may not need one for container-image-only specs).
func workspaceMount(dc *spec.DevContainer, localFolder, containerFolder string) *mount.Mount {
	if dc.WorkspaceMount != "" {
		if m := parseMountString(dc.WorkspaceMount); m != nil {
			return m
		}
	}
	if localFolder == "" || containerFolder == "" {
		return nil
	}
	return &mount.Mount{
		Type:   mount.TypeBind,
		Source: localFolder,
		Target: containerFolder,
	}
}

// parseMount accepts either a string ("type=bind,source=/x,target=/y")
// or an object form ({"type": "bind", "source": "/x", "target": "/y"})
// — both are valid in devcontainer.json.
func parseMount(raw any) *mount.Mount {
	switch v := raw.(type) {
	case string:
		return parseMountString(v)
	case map[string]any:
		return parseMountObject(v)
	}
	return nil
}

func parseMountString(s string) *mount.Mount {
	m := &mount.Mount{}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		k, v := kv, ""
		if eq := strings.IndexByte(kv, '='); eq >= 0 {
			k = strings.TrimSpace(kv[:eq])
			v = strings.TrimSpace(kv[eq+1:])
		}
		switch k {
		case "type":
			m.Type = mount.Type(v)
		case "source", "src":
			m.Source = v
		case "target", "dst", "destination":
			m.Target = v
		case "readonly", "ro":
			m.ReadOnly = v == "" || v == "true" || v == "1"
		case "consistency":
			m.Consistency = mount.Consistency(v)
		}
	}
	if m.Type == "" {
		m.Type = mount.TypeBind
	}
	if m.Target == "" {
		return nil
	}
	return m
}

func parseMountObject(o map[string]any) *mount.Mount {
	m := &mount.Mount{}
	for k, raw := range o {
		s, ok := raw.(string)
		if !ok {
			continue
		}
		switch k {
		case "type":
			m.Type = mount.Type(s)
		case "source":
			m.Source = s
		case "target", "destination":
			m.Target = s
		case "consistency":
			m.Consistency = mount.Consistency(s)
		}
		if k == "readonly" {
			if b, ok := raw.(bool); ok {
				m.ReadOnly = b
			}
		}
	}
	if m.Type == "" {
		m.Type = mount.TypeBind
	}
	if m.Target == "" {
		return nil
	}
	return m
}

func mapToEnvSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func upsertEnv(env []string, k, v string) []string {
	prefix := k + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + v
			return env
		}
	}
	return append(env, prefix+v)
}

// stringMap unwraps a map[string]any whose values are strings. Anything
// non-stringly is JSON-encoded so the user at least sees a meaningful
// value rather than "<map[string]interface {}>".
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

// substituteSpec returns a shallow copy of dc with the string-y fields
// resolved against ctx. Mutating the returned spec doesn't touch the
// original; callers can persist it for re-runs.
func substituteSpec(dc *spec.DevContainer, ctx substitute.Context) *spec.DevContainer {
	if dc == nil {
		return nil
	}
	out := *dc

	out.WorkspaceFolder = substitute.Substitute(ctx, dc.WorkspaceFolder)
	out.WorkspaceMount = substitute.Substitute(ctx, dc.WorkspaceMount)
	out.RemoteUser = substitute.Substitute(ctx, dc.RemoteUser)
	out.ContainerUser = substitute.Substitute(ctx, dc.ContainerUser)

	if dc.ContainerEnv != nil {
		newEnv := make(map[string]any, len(dc.ContainerEnv))
		for k, v := range dc.ContainerEnv {
			if s, ok := v.(string); ok {
				newEnv[k] = substitute.Substitute(ctx, s)
			} else {
				newEnv[k] = v
			}
		}
		out.ContainerEnv = newEnv
	}
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
	if len(dc.Mounts) > 0 {
		newMounts := make([]any, len(dc.Mounts))
		for i, m := range dc.Mounts {
			if s, ok := m.(string); ok {
				newMounts[i] = substitute.Substitute(ctx, s)
			} else {
				newMounts[i] = m
			}
		}
		out.Mounts = newMounts
	}
	if len(dc.RunArgs) > 0 {
		out.RunArgs = substitute.Apply(ctx, dc.RunArgs)
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

// ensurePosixPath canonicalizes a container-side path. Container paths
// are always posix; the caller may have passed a windows-style host
// path by mistake.
func ensurePosixPath(p string) string {
	return path.Clean("/" + strings.TrimLeft(strings.ReplaceAll(p, "\\", "/"), "/"))
}
