// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

type configOptions struct{ workspace, path, override string }

func (o *configOptions) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.workspace, "workspace-folder", ".", "Local workspace folder")
	cmd.Flags().StringVar(&o.path, "config", "", "Path to devcontainer.json (default: workspace discovery)")
	cmd.Flags().StringVar(&o.override, "override-config", "", "Read configuration content from this file; resolve relative paths against --config")
}

func (o configOptions) load() (*spec.DevContainer, string, error) {
	workspace, err := filepath.Abs(o.workspace)
	if err != nil {
		return nil, "", err
	}

	var dc *spec.DevContainer
	if o.override != "" {
		dc, err = spec.Load(o.override)
		original := o.path
		if original == "" {
			original = filepath.Join(workspace, ".devcontainer", "devcontainer.json")
			if discovered, discoverErr := spec.LoadFromWorkspace(workspace); discoverErr == nil {
				original = discovered.Path
			}
		}
		if err == nil {
			dc.Path, err = filepath.Abs(original)
		}
	} else if o.path == "" {
		dc, err = spec.LoadFromWorkspace(workspace)
	} else {
		p, absErr := filepath.Abs(o.path)
		if absErr != nil {
			return nil, "", absErr
		}
		dc, err = spec.Load(p)
	}

	return dc, workspace, err
}

// optionalLoad lets management and exec discover existing containers even after
// their configuration is removed. An explicit or malformed config still errors.
func (o configOptions) optionalLoad() (*spec.DevContainer, string, error) {
	dc, workspace, err := o.load()
	if err == nil || o.path != "" || o.override != "" {
		return dc, workspace, err
	}
	for _, p := range []string{filepath.Join(workspace, ".devcontainer", "devcontainer.json"), filepath.Join(workspace, ".devcontainer.json")} {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			return nil, workspace, err
		}
	}
	return &spec.DevContainer{}, workspace, nil
}

func (a *application) readCommand() *cobra.Command {
	var cfg configOptions
	cmd := &cobra.Command{Use: "read-configuration", Short: "Read and substitute devcontainer configuration", Args: cobra.NoArgs}
	cfg.flags(cmd)
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		dc, workspace, err := cfg.load()
		if err != nil {
			return err
		}
		configuration, err := readConfiguration(dc, workspace)
		if err != nil {
			return err
		}
		a.result["configuration"] = configuration
		a.result["workspace"] = map[string]string{"workspaceFolder": containerWorkspace(dc, workspace)}
		return nil
	}
	return cmd
}

// readConfiguration is the seam for metadata's mergedConfiguration extension.
// Keep the raw map so fields unknown to the Go model survive read-configuration.
func readConfiguration(dc *spec.DevContainer, workspace string) (map[string]any, error) {
	var raw map[string]any
	if err := json.Unmarshal(dc.Raw, &raw); err != nil {
		return nil, err
	}
	ctx := substitutionContext(dc, workspace, nil)
	result := substituteValues(raw, ctx).(map[string]any)
	result["configFilePath"] = dc.Path
	return result, nil
}

func substituteValues(value any, ctx substitute.Context) any {
	switch v := value.(type) {
	case string:
		return substitute.Substitute(ctx, v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = substituteValues(item, ctx)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = substituteValues(item, ctx)
		}
		return out
	default:
		return value
	}
}

func containerWorkspace(dc *spec.DevContainer, workspace string) string {
	folder := dc.WorkspaceFolder
	if folder == "" {
		folder = "/workspaces/" + filepath.Base(workspace)
	}
	return substitute.Substitute(substitute.Context{LocalWorkspaceFolder: workspace, LocalEnv: envMap()}, folder)
}

func substitutionContext(dc *spec.DevContainer, workspace string, labels map[string]string) substitute.Context {
	if labels == nil {
		labels = map[string]string{"devcontainer.local_folder": workspace, "devcontainer.config_file": dc.Path}
	}
	return substitute.Context{LocalWorkspaceFolder: workspace, ContainerWorkspaceFolder: containerWorkspace(dc, workspace), LocalEnv: envMap(), IDLabels: labels}
}

func envMap() map[string]string {
	out := map[string]string{}
	for _, pair := range os.Environ() {
		k, v, ok := strings.Cut(pair, "=")
		if ok {
			out[k] = v
		}
	}
	return out
}

func resolveRemoteEnv(in map[string]any, ctx substitute.Context, containerEnv map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		s, ok := v.(string)
		if !ok && v != nil {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		out[k] = substitute.ContainerSubstitute(substitute.ContainerContext{ContainerEnv: containerEnv}, substitute.Substitute(ctx, s))
	}
	return out
}

func parsePairs(values []string) (map[string]string, error) {
	out := map[string]string{}
	for _, value := range values {
		key, val, ok := strings.Cut(value, "=")
		if !ok || key == "" {
			return nil, usageError("expected KEY=VALUE, got %q", value)
		}
		out[key] = val
	}
	return out, nil
}

func defaultImage(workspace string) string {
	// A workspace hash avoids collisions between equal basenames.
	id := sha256.Sum256([]byte(workspace))
	return fmt.Sprintf("dcb-%x:latest", id[:8])
}
