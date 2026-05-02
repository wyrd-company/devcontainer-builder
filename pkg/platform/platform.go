// Package platform translates between Go's runtime platform names, OCI
// platform strings, Docker buildx platform strings, and the labels used by
// the VS Code server downloads.
package platform

import (
	"fmt"
	"runtime"
	"strings"
)

// Platform identifies a target build platform.
type Platform struct {
	OS      string // e.g. "linux"
	Arch    string // e.g. "amd64", "arm64", "arm"
	Variant string // e.g. "v7" for armhf
}

// String returns the canonical OCI form (os/arch[/variant]).
func (p Platform) String() string {
	if p.Variant != "" {
		return fmt.Sprintf("%s/%s/%s", p.OS, p.Arch, p.Variant)
	}
	return fmt.Sprintf("%s/%s", p.OS, p.Arch)
}

// Parse parses an OCI-style platform string like "linux/amd64" or
// "linux/arm/v7".
func Parse(s string) (Platform, error) {
	parts := strings.Split(s, "/")
	switch len(parts) {
	case 2:
		return Platform{OS: parts[0], Arch: parts[1]}, nil
	case 3:
		return Platform{OS: parts[0], Arch: parts[1], Variant: parts[2]}, nil
	default:
		return Platform{}, fmt.Errorf("invalid platform %q", s)
	}
}

// Current returns the platform the current process is running on.
func Current() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// VSCodeServerArch maps a Platform to the architecture label VS Code uses
// for server downloads.
//
// Reference: https://update.code.visualstudio.com/api/update/server-linux-<arch>/stable/<commit>
func (p Platform) VSCodeServerArch() (string, error) {
	if p.OS != "linux" {
		return "", fmt.Errorf("vscode server only supported on linux, got %q", p.OS)
	}
	switch p.Arch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		return "armhf", nil
	}
	return "", fmt.Errorf("vscode server: unsupported arch %q", p.Arch)
}

// AlpineSuffix is "-alpine" when the platform string carries that suffix,
// allowing distinct download variants. The devcontainer spec doesn't model
// libc explicitly, so callers typically derive this elsewhere.
func (p Platform) AlpineSuffix(alpine bool) string {
	if alpine {
		return "-alpine"
	}
	return ""
}
