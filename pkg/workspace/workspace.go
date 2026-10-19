// relationships:
//   references: [git, ../../reference/cli/src/spec-node/utils]

// Package workspace decides where a local workspace folder is mounted in a
// single-container devcontainer and which container folder is its workspace
// folder. It follows upstream's getWorkspaceConfiguration
// (src/spec-node/utils.ts) and reads the host file system only; it never runs
// git. Compose configurations own their mounts and do not use this package.
package workspace

import (
	"path"
	"path/filepath"
	"strings"
)

// Consistency values accepted for bind mounts on Docker Desktop hosts.
const (
	ConsistencyConsistent = "consistent"
	ConsistencyCached     = "cached"
	ConsistencyDelegated  = "delegated"
)

// Options select how the workspace is mounted.
type Options struct {
	// MountGitRoot mounts the top folder of the workspace's Git working tree
	// in place of the workspace folder (upstream default true).
	MountGitRoot bool
	// MountGitWorktreeCommonDir also mounts the common Git folder of a linked
	// worktree whose .git file holds a relative gitdir, so Git works in the
	// container. It applies only with MountGitRoot.
	MountGitWorktreeCommonDir bool
	// Consistency is set on the bind mounts on hosts other than Linux. Empty
	// means consistent.
	Consistency string
	// Platform is the host's GOOS. Linux mounts carry no consistency option,
	// as upstream omits it there.
	Platform string
}

// Mount is a bind mount from a host folder to a container folder.
type Mount struct {
	Source, Target, Consistency string
}

// String renders the mount in upstream's devcontainer mount syntax, quoting a
// source or target that contains a comma.
func (m Mount) String() string {
	quote := func(key, value string) string {
		if strings.Contains(value, ",") {
			return `"` + key + "=" + value + `"`
		}
		return key + "=" + value
	}
	out := "type=bind," + quote("source", m.Source) + "," + quote("target", m.Target)
	if m.Consistency != "" {
		out += ",consistency=" + m.Consistency
	}
	return out
}

// Configuration is the resolved workspace placement.
type Configuration struct {
	// WorkspaceFolder is the container workspace folder, or "" when the
	// configuration sets workspaceFolder itself.
	WorkspaceFolder string
	// WorkspaceMount is the workspace bind mount, or nil when the
	// configuration declares workspaceMount.
	WorkspaceMount *Mount
	// AdditionalMount is the linked worktree's common Git folder, if mounted.
	AdditionalMount *Mount
}

// Resolve places localWorkspace (an absolute host folder). hasWorkspaceFolder
// and hasWorkspaceMount report whether the configuration declares those
// properties; when it declares both, nothing is derived.
func Resolve(localWorkspace string, hasWorkspaceFolder, hasWorkspaceMount bool, opts Options) (Configuration, error) {
	var out Configuration
	if hasWorkspaceFolder && hasWorkspaceMount {
		return out, nil
	}
	hostMountFolder := localWorkspace
	if opts.MountGitRoot {
		root, err := GitRoot(localWorkspace)
		if err != nil {
			return out, err
		}
		if root != "" {
			hostMountFolder = root
		}
	}
	consistency := ""
	if opts.Platform != "linux" {
		consistency = opts.Consistency
		if consistency == "" {
			consistency = ConsistencyConsistent
		}
	}

	containerMountFolder := path.Join("/workspaces", filepath.Base(hostMountFolder))
	if opts.MountGitRoot && opts.MountGitWorktreeCommonDir {
		folder, common, err := worktreeCommonDir(hostMountFolder, consistency)
		if err != nil {
			return out, err
		}
		if common != nil {
			containerMountFolder, out.AdditionalMount = folder, common
		}
	}
	if !hasWorkspaceFolder {
		rel, err := filepath.Rel(hostMountFolder, localWorkspace)
		if err != nil {
			return out, err
		}
		out.WorkspaceFolder = path.Join(containerMountFolder, filepath.ToSlash(rel))
	}
	if !hasWorkspaceMount {
		out.WorkspaceMount = &Mount{Source: hostMountFolder, Target: containerMountFolder, Consistency: consistency}
	}
	return out, nil
}

// worktreeCommonDir places a linked worktree beside its common Git folder:
// the container keeps the host path segments from the folder that contains
// both, so the relative gitdir resolves inside the container too. It returns
// a nil mount unless hostMountFolder/.git is a file with a relative gitdir.
func worktreeCommonDir(hostMountFolder, consistency string) (string, *Mount, error) {
	dotGit := filepath.Join(hostMountFolder, ".git")
	if !isFile(dotGit) {
		return "", nil, nil
	}
	gitdir, err := gitfilePointer(dotGit)
	if err != nil || gitdir == "" || filepath.IsAbs(gitdir) {
		return "", nil, err
	}
	// gitdir names .git/worktrees/<name>; the common folder is two levels up.
	common := filepath.Join(hostMountFolder, gitdir, "..", "..")
	var segments []string
	for current := hostMountFolder; !strings.HasPrefix(common, current+string(filepath.Separator)) && current != filepath.Dir(current); current = filepath.Dir(current) {
		segments = append([]string{filepath.Base(current)}, segments...)
	}
	folder := path.Join(append([]string{"/workspaces"}, segments...)...)
	target := path.Join(folder, filepath.ToSlash(gitdir), "..", "..")
	return folder, &Mount{Source: common, Target: target, Consistency: consistency}, nil
}
