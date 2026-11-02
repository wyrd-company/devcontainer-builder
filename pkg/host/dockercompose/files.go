// relationships:
//
//	references: [project, ../../../../reference/cli/src/spec-configuration/configuration.ts]
package dockercompose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

var composeFileEnvLine = regexp.MustCompile(`(?m)^COMPOSE_FILE=(.+)$`)

// ResolveComposeFiles follows the pinned CLI discovery for an empty file array.
// Explicit files resolve from the configuration directory; discovery uses the
// caller's workspace and environment, never the library process environment.
func ResolveComposeFiles(dc *spec.DevContainer, workspace string, env map[string]string) ([]string, error) {
	if dc == nil || dc.Mode() != spec.ModeCompose {
		return nil, fmt.Errorf("compose configuration is required")
	}
	if dc.DockerComposeFile == nil {
		return nil, fmt.Errorf("dockerComposeFile is missing")
	}
	if len(dc.DockerComposeFile) > 0 {
		return dc.ComposeFiles(), nil
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	resolve := func(value string) []string {
		parts := strings.Split(value, string(os.PathListSeparator))
		for i, part := range parts {
			if filepath.IsAbs(part) {
				parts[i] = filepath.Clean(part)
			} else {
				parts[i] = filepath.Join(workspace, part)
			}
		}
		return parts
	}
	if value := env["COMPOSE_FILE"]; value != "" {
		return resolve(value), nil
	}
	body, err := os.ReadFile(filepath.Join(workspace, ".env"))
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.EISDIR) {
		return nil, fmt.Errorf("compose .env: %w", err)
	}
	if match := composeFileEnvLine.FindSubmatch(body); match != nil {
		if value := strings.TrimSpace(string(match[1])); value != "" {
			return resolve(value), nil
		}
	}
	files := []string{filepath.Join(workspace, "docker-compose.yml")}
	override := filepath.Join(workspace, "docker-compose.override.yml")
	if info, err := os.Stat(override); err == nil && info.Mode().IsRegular() {
		files = append(files, override)
	}
	return files, nil
}
