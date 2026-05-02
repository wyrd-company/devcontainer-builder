// Package lifecycle plans the post-build / post-start commands a
// devcontainer expects to run, in the order required by the spec, with
// the marker-file scheme that the upstream devcontainer CLI uses.
//
// The package is pure: it does not exec anything itself. Instead, Plan
// returns a slice of Phase records, and a host-specific package (e.g.
// pkg/host/docker) decides how to execute each phase against the
// container at runtime — running it imperatively for docker, projecting
// it into Pod fields for kubernetes.
//
// Compatibility is intentional. Marker file names, marker contents,
// command-form semantics (string→shell, array→exec, object→parallel),
// and run-once vs run-on-start vs run-on-attach mirror upstream's
// src/spec-common/injectHeadless.ts.
package lifecycle

import (
	"path"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// When describes how often a Phase runs across container lifetimes.
type When int

const (
	// WhenOnce runs at most once per container creation. Marker file's
	// content is the container's createdAt; if the marker is missing or
	// stale, the phase runs and the marker is updated.
	WhenOnce When = iota
	// WhenStart runs once per container start, including initial
	// creation. Marker content is startedAt.
	WhenStart
	// WhenAttach runs every time a client attaches. No marker —
	// upstream re-runs unconditionally on every attach.
	WhenAttach
)

func (w When) String() string {
	switch w {
	case WhenOnce:
		return "once"
	case WhenStart:
		return "start"
	case WhenAttach:
		return "attach"
	}
	return "unknown"
}

// Hook names mirror the spec's command field names so log output and
// marker filenames stay legible / compatible.
type Hook string

const (
	HookOnCreate      Hook = "onCreateCommand"
	HookUpdateContent Hook = "updateContentCommand"
	HookPostCreate    Hook = "postCreateCommand"
	HookPostStart     Hook = "postStartCommand"
	HookPostAttach    Hook = "postAttachCommand"
)

// Origin records where a command came from, so executors can label log
// output the way upstream does ("Running postCreateCommand from
// devcontainer.json..." vs "...from Feature 'ghcr.io/.../node:1'...").
type Origin struct {
	// Source is "devcontainer.json" for top-level commands, or the
	// feature's source ref (e.g. "ghcr.io/devcontainers/features/node:1")
	// for feature-supplied commands.
	Source string
}

// Phase is one logical run of a single command (or set of parallel
// named commands) for a particular hook.
type Phase struct {
	Hook    Hook
	When    When
	Origin  Origin
	Command spec.Command
}

// MarkerPath returns the path inside the container that the executor
// should use to record successful completion. WhenAttach phases have no
// marker; the empty string is returned in that case.
//
// userDataFolder is the user's devcontainer data folder; pass
// UserDataFolder(homeDir, "") to use the default.
func (p Phase) MarkerPath(userDataFolder string) string {
	switch p.When {
	case WhenOnce, WhenStart:
		return path.Join(userDataFolder, "."+string(p.Hook)+"Marker")
	}
	return ""
}

// UserDataFolder returns the per-user devcontainer state folder inside
// the container. The default is "${homeDir}/.devcontainer", overridable
// by passing a non-empty containerDataFolder, mirroring upstream's
// containerDataFolder option.
func UserDataFolder(homeDir, containerDataFolder string) string {
	if containerDataFolder != "" {
		if path.IsAbs(containerDataFolder) {
			return containerDataFolder
		}
		return path.Join(homeDir, containerDataFolder)
	}
	return path.Join(homeDir, ".devcontainer")
}

// SystemVarFolder is the system-wide devcontainer state folder used for
// marker files that need root (e.g. patching /etc/environment).
// Defaults to "/var/devcontainer" per upstream.
func SystemVarFolder(override string) string {
	if override != "" {
		return override
	}
	return "/var/devcontainer"
}

// Plan returns the ordered phases that should run for the given spec
// and resolved features, with the standard execution order:
//
//	onCreate (spec) → onCreate (features…)
//	updateContent (spec) → updateContent (features…)
//	postCreate (spec) → postCreate (features…)
//	postStart (spec) → postStart (features…)
//	postAttach (spec) → postAttach (features…)
//
// Within each hook, the spec's own command runs first, then each
// feature's command in install order. Phases whose command is empty are
// omitted, so callers can simply iterate the returned slice.
func Plan(dc *spec.DevContainer, resolved []features.Resolved) []Phase {
	if dc == nil {
		return nil
	}
	specOrigin := Origin{Source: "devcontainer.json"}
	hooks := []struct {
		hook       Hook
		when       When
		spec       spec.Command
		fromFeat   func(features.Metadata) spec.Command
	}{
		{HookOnCreate, WhenOnce, dc.OnCreate, func(m features.Metadata) spec.Command { return m.OnCreateCommand }},
		{HookUpdateContent, WhenOnce, dc.UpdateContent, func(m features.Metadata) spec.Command { return m.UpdateContent }},
		{HookPostCreate, WhenOnce, dc.PostCreate, func(m features.Metadata) spec.Command { return m.PostCreateCommand }},
		{HookPostStart, WhenStart, dc.PostStart, func(m features.Metadata) spec.Command { return m.PostStartCommand }},
		{HookPostAttach, WhenAttach, dc.PostAttach, func(m features.Metadata) spec.Command { return m.PostAttachCommand }},
	}
	var out []Phase
	for _, h := range hooks {
		if !h.spec.IsZero() {
			out = append(out, Phase{Hook: h.hook, When: h.when, Origin: specOrigin, Command: h.spec})
		}
		for _, f := range resolved {
			cmd := h.fromFeat(f.Metadata)
			if cmd.IsZero() {
				continue
			}
			out = append(out, Phase{
				Hook:    h.hook,
				When:    h.when,
				Origin:  Origin{Source: f.Source},
				Command: cmd,
			})
		}
	}
	return out
}
