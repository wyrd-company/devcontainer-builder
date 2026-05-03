// Package substitute resolves the ${variable} placeholders that
// devcontainer.json and feature configs use. The behavior is intended
// to match upstream's src/spec-common/variableSubstitution.ts so a spec
// that works under devcontainers/cli works here too.
//
// Two phases are supported:
//
//   - Pre-container substitution (Substitute / Apply): resolves
//     ${env:VAR}, ${localEnv:VAR}, ${localWorkspaceFolder},
//     ${containerWorkspaceFolder}, and ${devcontainerId}. Run before the
//     container is created.
//   - Post-container substitution (ContainerSubstitute /
//     ApplyContainer): resolves ${containerEnv:VAR}. Run once the
//     container is created and its real env is known.
//
// Anything that doesn't resolve is left in place so the caller can
// surface a clearer error than "blank value at ENV[FOO]=".
package substitute

import (
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"path"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Context drives Substitute. Any field left zero/nil simply means "no
// value available for variables that reference it".
type Context struct {
	// Platform is the host OS, used to keep env-var lookups
	// case-insensitive on Windows. Empty defaults to runtime.GOOS.
	Platform string
	// LocalWorkspaceFolder is the absolute host path of the workspace.
	LocalWorkspaceFolder string
	// ContainerWorkspaceFolder is the path inside the container where
	// the workspace will be mounted. Used by
	// ${containerWorkspaceFolder}.
	ContainerWorkspaceFolder string
	// LocalEnv is the host environment. Populate from os.Environ() in
	// CLI use; tests can pass an explicit map.
	LocalEnv map[string]string
	// IDLabels are the labels we'll set on the container so it can be
	// rediscovered later. ${devcontainerId} is the sha256 of this map
	// (sorted, base32, 52 chars) — same scheme as upstream.
	IDLabels map[string]string
}

// ContainerContext is the second-phase context, available once the
// container exists and its environment can be observed.
type ContainerContext struct {
	Platform     string
	ContainerEnv map[string]string
}

var variableRegexp = regexp.MustCompile(`\$\{(.*?)\}`)

// Substitute replaces every ${...} occurrence in s using ctx.
// Unrecognized variables are left intact (the regex match is
// preserved).
func Substitute(ctx Context, s string) string {
	plat := ctx.Platform
	if plat == "" {
		plat = runtime.GOOS
	}
	winLookup := plat == "windows"
	env := normalizeEnv(winLookup, ctx.LocalEnv)
	// devcontainerId is computed lazily — most strings don't reference
	// it, and the hash is the most expensive part of substitution.
	var devcontainerID string
	idResolver := func() string {
		if devcontainerID == "" && len(ctx.IDLabels) > 0 {
			devcontainerID = devcontainerIDForLabels(ctx.IDLabels)
		}
		return devcontainerID
	}
	return variableRegexp.ReplaceAllStringFunc(s, func(match string) string {
		variable, args := splitVariable(match)
		switch variable {
		case "env", "localEnv":
			return lookupEnv(winLookup, env, args, match)
		case "localWorkspaceFolder":
			if ctx.LocalWorkspaceFolder == "" {
				return match
			}
			return ctx.LocalWorkspaceFolder
		case "localWorkspaceFolderBasename":
			if ctx.LocalWorkspaceFolder == "" {
				return match
			}
			return basename(plat, ctx.LocalWorkspaceFolder)
		case "containerWorkspaceFolder":
			if ctx.ContainerWorkspaceFolder == "" {
				return match
			}
			return ctx.ContainerWorkspaceFolder
		case "containerWorkspaceFolderBasename":
			if ctx.ContainerWorkspaceFolder == "" {
				return match
			}
			return path.Base(ctx.ContainerWorkspaceFolder)
		case "devcontainerId":
			id := idResolver()
			if id == "" {
				return match
			}
			return id
		}
		return match
	})
}

// ContainerSubstitute applies the second-phase substitution (only
// ${containerEnv:...}). Other variables are left alone — they should
// already have been resolved in the first pass.
func ContainerSubstitute(ctx ContainerContext, s string) string {
	plat := ctx.Platform
	if plat == "" {
		plat = runtime.GOOS
	}
	winLookup := plat == "windows"
	env := normalizeEnv(winLookup, ctx.ContainerEnv)
	return variableRegexp.ReplaceAllStringFunc(s, func(match string) string {
		variable, args := splitVariable(match)
		if variable == "containerEnv" {
			return lookupEnv(winLookup, env, args, match)
		}
		return match
	})
}

// Apply runs Substitute over each string in a slice, returning a new
// slice. Convenience for the common case of substituting mounts /
// runArgs / etc.
func Apply(ctx Context, in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = Substitute(ctx, v)
	}
	return out
}

// ApplyMap returns a new map with values substituted. Keys are not
// substituted (matches upstream — env keys and label keys are
// configuration identifiers, not user values).
func ApplyMap(ctx Context, in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = Substitute(ctx, v)
	}
	return out
}

// splitVariable splits the inside of a ${...} match into a variable
// name and zero-or-more colon-separated args. e.g. "env:FOO:default"
// returns ("env", []string{"FOO", "default"}).
func splitVariable(match string) (string, []string) {
	inside := match[2 : len(match)-1]
	parts := strings.Split(inside, ":")
	if len(parts) == 1 {
		return parts[0], nil
	}
	return parts[0], parts[1:]
}

func normalizeEnv(winLookup bool, in map[string]string) map[string]string {
	if !winLookup {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[strings.ToLower(k)] = v
	}
	return out
}

// lookupEnv mirrors the reference's lookupValue: missing var -> empty
// string (so shell-like behavior); missing var name -> leave the match
// in place (caller can detect and surface).
func lookupEnv(winLookup bool, env map[string]string, args []string, match string) string {
	if len(args) == 0 {
		return match
	}
	name := args[0]
	if winLookup {
		name = strings.ToLower(name)
	}
	if v, ok := env[name]; ok {
		return v
	}
	if len(args) > 1 {
		return args[1]
	}
	return ""
}

// devcontainerIDForLabels mirrors upstream's hash-of-sorted-labels:
// sha256 the JSON of the labels map with keys in sorted order, then
// base32-encode the digest (using BigInt's "base32" — really radix 32
// over 0-9 a-v, not RFC 4648), padded to 52 characters.
func devcontainerIDForLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Reproduce JSON.stringify with a key order: build manually.
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kBytes, _ := json.Marshal(k)
		vBytes, _ := json.Marshal(labels[k])
		b.Write(kBytes)
		b.WriteByte(':')
		b.Write(vBytes)
	}
	b.WriteByte('}')
	sum := sha256.Sum256([]byte(b.String()))
	n := new(big.Int).SetBytes(sum[:])
	encoded := n.Text(32)
	if len(encoded) < 52 {
		encoded = strings.Repeat("0", 52-len(encoded)) + encoded
	}
	return encoded
}

func basename(platform, p string) string {
	// On windows, paths are typically backslash-separated. Mirror
	// upstream's split-on-either-separator behavior.
	if platform == "windows" {
		p = strings.ReplaceAll(p, "\\", "/")
	}
	return path.Base(p)
}
