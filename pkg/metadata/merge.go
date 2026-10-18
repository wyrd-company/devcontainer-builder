// relationships:
//
//	references: [metadata, lifecycle]
package metadata

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Merged keeps the ordinary config shape for Go callers and exposes upstream's
// collected lifecycle/customization fields in JSON.
type Merged struct {
	*spec.DevContainer
	Customizations        map[string][]any
	Entrypoints           []string
	OnCreateCommands      []spec.Command
	UpdateContentCommands []spec.Command
	PostCreateCommands    []spec.Command
	PostStartCommands     []spec.Command
	PostAttachCommands    []spec.Command
}

// MarshalJSON uses the merged configuration names from the upstream CLI.
func (m Merged) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(m.DevContainer)
	if err != nil {
		return nil, err
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	out := map[string]any{}
	if len(m.Raw) > 0 {
		if err := json.Unmarshal(m.Raw, &out); err != nil {
			return nil, err
		}
	}
	for _, key := range []string{"init", "privileged", "capAdd", "securityOpt", "mounts", "remoteUser", "containerUser", "waitFor", "userEnvProbe", "shutdownAction", "overrideCommand", "updateRemoteUserUID", "remoteEnv", "containerEnv", "portsAttributes", "otherPortsAttributes", "forwardPorts", "hostRequirements"} {
		delete(out, key)
	}
	for k, v := range fields {
		out[k] = v
	}
	delete(out, "features")
	if m.Features.Len() > 0 {
		out["features"] = m.Features.Values
	}
	for _, key := range []string{"customizations", "entrypoint", "onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand"} {
		delete(out, key)
	}
	for key, value := range map[string]any{
		"remoteEnv": m.UpdateRemoteEnv, "containerEnv": m.ContainerEnv, "portsAttributes": m.PortsAttributes,
		"onCreateCommands": m.OnCreateCommands, "updateContentCommands": m.UpdateContentCommands,
		"postCreateCommands": m.PostCreateCommands, "postStartCommands": m.PostStartCommands,
		"postAttachCommands": m.PostAttachCommands,
	} {
		out[key] = value
	}
	if len(m.Customizations) > 0 {
		out["customizations"] = m.Customizations
	}
	if len(m.Entrypoints) > 0 {
		out["entrypoints"] = m.Entrypoints
	}
	return json.Marshal(out)
}

// Merge combines an already-collected list; it does not append config again.
// The config supplies fields that are not part of image metadata (e.g. image,
// build, workspace and runArgs). Neither config nor entries is modified.
func Merge(config *spec.DevContainer, entries []Entry) Merged {
	dc := spec.DevContainer{}
	if config != nil {
		dc = *config
	}
	m := Merged{DevContainer: &dc, Customizations: map[string][]any{},
		OnCreateCommands: []spec.Command{}, UpdateContentCommands: []spec.Command{},
		PostCreateCommands: []spec.Command{}, PostStartCommands: []spec.Command{}, PostAttachCommands: []spec.Command{}}
	init, privileged := false, false
	dc.CapAdd, dc.SecurityOpt, dc.Mounts, dc.ForwardPorts = nil, nil, nil, nil
	dc.RemoteUser, dc.ContainerUser, dc.WaitFor, dc.UserEnvProbe, dc.ShutdownAction = "", "", "", "", ""
	dc.OverrideCommand, dc.UpdateRemoteUserUID = nil, nil
	dc.UpdateRemoteEnv, dc.ContainerEnv, dc.PortsAttributes = map[string]any{}, map[string]any{}, map[string]any{}
	dc.OtherPortsAttributes, dc.HostRequirements, dc.Customizations = nil, nil, nil
	dc.OnCreate, dc.UpdateContent, dc.PostCreate, dc.PostStart, dc.PostAttach = spec.Command{}, spec.Command{}, spec.Command{}, spec.Command{}, spec.Command{}
	var mounts []any
	for _, e := range entries {
		init = init || e.Init != nil && *e.Init
		privileged = privileged || e.Privileged != nil && *e.Privileged
		dc.CapAdd = union(dc.CapAdd, e.CapAdd)
		dc.SecurityOpt = union(dc.SecurityOpt, e.SecurityOpt)
		if e.Entrypoint != "" {
			m.Entrypoints = append(m.Entrypoints, e.Entrypoint)
		}
		mounts = append(mounts, e.Mounts...)
		for k, v := range e.Customizations {
			m.Customizations[k] = append(m.Customizations[k], v)
		}
		m.OnCreateCommands = collectCommand(m.OnCreateCommands, e.OnCreate)
		m.UpdateContentCommands = collectCommand(m.UpdateContentCommands, e.UpdateContent)
		m.PostCreateCommands = collectCommand(m.PostCreateCommands, e.PostCreate)
		m.PostStartCommands = collectCommand(m.PostStartCommands, e.PostStart)
		m.PostAttachCommands = collectCommand(m.PostAttachCommands, e.PostAttach)
		lastString(&dc.WaitFor, e.WaitFor)
		lastString(&dc.RemoteUser, e.RemoteUser)
		lastString(&dc.ContainerUser, e.ContainerUser)
		lastString(&dc.UserEnvProbe, e.UserEnvProbe)
		lastString(&dc.ShutdownAction, e.ShutdownAction)
		if e.OverrideCommand != nil {
			dc.OverrideCommand = e.OverrideCommand
		}
		if e.UpdateRemoteUserUID != nil {
			dc.UpdateRemoteUserUID = e.UpdateRemoteUserUID
		}
		for k, v := range e.RemoteEnv {
			dc.UpdateRemoteEnv[k] = v
		}
		for k, v := range e.ContainerEnv {
			dc.ContainerEnv[k] = v
		}
		for k, v := range e.PortsAttributes {
			dc.PortsAttributes[k] = v
		}
		if e.OtherPortsAttributes != nil {
			dc.OtherPortsAttributes = e.OtherPortsAttributes
		}
		for _, port := range e.ForwardPorts {
			normalized := fmt.Sprint(port)
			if strings.HasPrefix(normalized, "localhost:") {
				normalized = strings.TrimPrefix(normalized, "localhost:")
			}
			var value any = port
			if n, err := strconv.Atoi(normalized); err == nil {
				value = n
			}
			if !slices.ContainsFunc(dc.ForwardPorts, func(p any) bool { return fmt.Sprint(p) == fmt.Sprint(value) }) {
				dc.ForwardPorts = append(dc.ForwardPorts, value)
			}
		}
		dc.HostRequirements = mergeHostRequirements(dc.HostRequirements, e.HostRequirements)
	}
	dc.Init, dc.Privileged = &init, &privileged
	seen := map[string]bool{}
	for i := len(mounts) - 1; i >= 0; i-- {
		target := mountTarget(mounts[i])
		if !seen[target] {
			dc.Mounts = append(dc.Mounts, mounts[i])
			seen[target] = true
		}
	}
	slices.Reverse(dc.Mounts)
	return m
}

func union(dst, src []string) []string {
	for _, v := range src {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}
func lastString(dst *string, value string) {
	if value != "" {
		*dst = value
	}
}
func collectCommand(dst []spec.Command, c spec.Command) []spec.Command {
	if !c.IsZero() {
		dst = append(dst, c)
	}
	return dst
}
func mountTarget(m any) string {
	switch v := m.(type) {
	case map[string]any:
		target, _ := v["target"].(string)
		return target
	case string:
		var target string
		for _, part := range strings.Split(v, ",") {
			k, value, _ := strings.Cut(part, "=")
			switch strings.TrimSpace(k) {
			case "target", "dst", "destination":
				target = value
			}
		}
		return target
	}
	return ""
}

// Plan retains entry origins and command forms, grouped in lifecycle phase order.
func Plan(entries []Entry) []lifecycle.Phase {
	var phases []lifecycle.Phase
	hooks := []struct {
		hook    lifecycle.Hook
		when    lifecycle.When
		command func(Entry) spec.Command
	}{
		{lifecycle.HookOnCreate, lifecycle.WhenOnce, func(e Entry) spec.Command { return e.OnCreate }},
		{lifecycle.HookUpdateContent, lifecycle.WhenOnce, func(e Entry) spec.Command { return e.UpdateContent }},
		{lifecycle.HookPostCreate, lifecycle.WhenOnce, func(e Entry) spec.Command { return e.PostCreate }},
		{lifecycle.HookPostStart, lifecycle.WhenStart, func(e Entry) spec.Command { return e.PostStart }},
		{lifecycle.HookPostAttach, lifecycle.WhenAttach, func(e Entry) spec.Command { return e.PostAttach }},
	}
	for _, h := range hooks {
		for _, e := range entries {
			c := h.command(e)
			if c.IsZero() {
				continue
			}
			origin := e.ID
			if origin == "" {
				origin = "devcontainer.json"
			}
			phases = append(phases, lifecycle.Phase{Hook: h.hook, When: h.when, Origin: lifecycle.Origin{Source: origin}, Command: c})
		}
	}
	return phases
}
