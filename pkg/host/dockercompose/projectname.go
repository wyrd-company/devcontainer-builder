package dockercompose

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// ProjectName derives a stable compose project name from the workspace
// path. Mirrors the devcontainers CLI's approach: a short hash of the
// absolute workspace path so the same workspace always reuses the
// same project, and unrelated workspaces never collide.
//
// The result is a compose-legal identifier: lowercase, leading
// alphanumeric, non-empty.
func ProjectName(workspace string) string {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		abs = workspace
	}
	abs = filepath.Clean(abs)

	sum := sha256.Sum256([]byte(abs))
	hash := hex.EncodeToString(sum[:])[:8]

	base := strings.ToLower(filepath.Base(abs))
	base = sanitizeProjectBase(base)
	if base == "" {
		base = "devcontainer"
	}
	return base + "_" + hash
}

// sanitizeProjectBase keeps only [a-z0-9_-], collapses runs, and
// strips leading non-letters. Compose accepts [a-z0-9_-]+ but rejects
// names starting with a digit or punctuation.
func sanitizeProjectBase(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		}
	}
	out := b.String()
	for len(out) > 0 && !(out[0] >= 'a' && out[0] <= 'z') {
		out = out[1:]
	}
	return out
}
