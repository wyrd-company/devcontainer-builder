// relationships:
//
//	references: [project, manage, ../../../../reference/cli/src/spec-node/dockerCompose.ts]
package dockercompose

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// ProjectName supplies the directory-based default used by the devcontainers
// CLI: <workspace>_devcontainer for a first Compose file in <workspace>/.devcontainer,
// otherwise the basename of that file's directory. With no file it assumes the
// conventional .devcontainer directory. LoadProject applies environment, .env,
// and authored Compose names ahead of this default. Names are lowercased and
// retain only [-_a-z0-9]; they contain no workspace hash.
func ProjectName(workspace string, composeFiles ...string) string {
	workingDir := filepath.Join(workspace, ".devcontainer")
	if len(composeFiles) > 0 {
		workingDir = filepath.Dir(composeFiles[0])
	}
	if filepath.Clean(workingDir) == filepath.Clean(filepath.Join(workspace, ".devcontainer")) {
		return toProjectName(filepath.Base(filepath.Clean(workspace)) + "_devcontainer")
	}
	return toProjectName(filepath.Base(workingDir))
}

func toProjectName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var projectNameEnvLine = regexp.MustCompile(`(?m)^COMPOSE_PROJECT_NAME=(.+)$`)

// Upstream up sets cliHost.cwd to --workspace-folder (or process.cwd when
// omitted). Its .env name lookup reads the first matching assignment literally,
// trims whitespace, then sanitizes it; this is not dotenv interpolation.
func projectNameOverride(workspace string, env map[string]string) (string, error) {
	if name := toProjectName(env["COMPOSE_PROJECT_NAME"]); name != "" {
		return name, nil
	}
	body, err := os.ReadFile(filepath.Join(workspace, ".env"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.EISDIR) {
			return "", nil
		}
		return "", err
	}
	if match := projectNameEnvLine.FindSubmatch(body); match != nil {
		return toProjectName(strings.TrimSpace(string(match[1]))), nil
	}
	return "", nil
}
