// relationships:
//
//	references: [features, lockfile, outdated, ../../reference/cli/src/spec-node/upgradeCommand]
package features

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/internal/collectiondocs"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// UpgradeOptions selects a workspace and optional Feature requirement update.
// DryRun prints the generated lockfile through the caller instead of forcing a
// lockfile write. Like upstream it still updates a requested config requirement
// and refreshes an existing lockfile during resolution.
type UpgradeOptions struct {
	Workspace, ConfigPath  string
	Feature, TargetVersion string
	DryRun                 bool
}

// ValidateUpgradeOptions checks the paired version flags before any file writes.
func ValidateUpgradeOptions(o UpgradeOptions) error {
	if (o.Feature == "") != (o.TargetVersion == "") {
		return fmt.Errorf("the '--target-version' and '--feature' flag must be used together")
	}
	if o.TargetVersion != "" && !regexp.MustCompile(`^[0-9]+(\.[0-9]+(\.[0-9]+)?)?$`).MatchString(o.TargetVersion) {
		return fmt.Errorf("invalid version %q: must be in the form of x, x.y, or x.y.z", o.TargetVersion)
	}
	return nil
}

// Upgrade generates a lockfile using the existing immutable pins and resolves
// new requirements and their dependencies. It does not build or install anything.
// The returned bytes are pretty JSON with a trailing newline for dry-run output.
func (r *Resolver) Upgrade(ctx context.Context, o UpgradeOptions) ([]byte, error) {
	if err := ValidateUpgradeOptions(o); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if o.Workspace == "" {
		o.Workspace = "."
	}
	workspace, err := filepath.Abs(o.Workspace)
	if err != nil {
		return nil, err
	}
	dc, err := ReadVersionConfig(workspace, o.ConfigPath)
	if err != nil {
		return nil, err
	}
	if o.Feature != "" {
		text, err := os.ReadFile(dc.Path)
		if err != nil {
			return nil, err
		}
		for _, id := range dc.Features.Keys {
			if FeatureIDWithoutVersion(id) != FeatureIDWithoutVersion(o.Feature) {
				continue
			}
			// Upstream uses an unescaped global regular expression over the config text.
			pattern, err := regexp.Compile(id)
			if err != nil {
				return nil, err
			}
			updated := pattern.ReplaceAllLiteral(text, []byte(FeatureIDWithoutVersion(o.Feature)+":"+o.TargetVersion))
			if string(updated) != string(text) {
				if err := os.WriteFile(dc.Path, updated, 0o666); err != nil {
					return nil, err
				}
			}
			break
		}
		dc, err = ReadVersionConfig(workspace, dc.Path)
		if err != nil {
			return nil, err
		}
	}
	if dc.Features.Len() == 0 {
		return nil, fmt.Errorf("Failed to update lockfile")
	}
	// Configuration values are substituted; Feature identifier keys are literal.
	text, err := os.ReadFile(dc.Path)
	if err != nil {
		return nil, err
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(collectiondocs.ReadJSONCValue(text), &document); err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			env[k] = v
		}
	}
	sub := substitute.Context{LocalWorkspaceFolder: workspace, LocalEnv: env}
	var folder string
	_ = json.Unmarshal(document["workspaceFolder"], &folder)
	if folder == "" {
		if _, ok := document["dockerComposeFile"]; ok {
			folder = "/"
		} else {
			folder = "/workspaces/" + filepath.Base(workspace)
		}
	}
	sub.ContainerWorkspaceFolder = substitute.Substitute(sub, folder)
	transform := func(raw json.RawMessage) json.RawMessage {
		return collectiondocs.TransformJSONStrings(raw, func(s string) string {
			if sub.ContainerWorkspaceFolder == "/" {
				s = strings.ReplaceAll(s, "${containerWorkspaceFolderBasename}", "")
			}
			return substitute.Substitute(sub, s)
		})
	}
	for _, id := range dc.Features.Keys {
		dc.Features.Values[id] = transform(dc.Features.Values[id])
	}
	var order []string
	if raw := document["overrideFeatureInstallOrder"]; raw != nil {
		if err := json.Unmarshal(transform(raw), &order); err != nil {
			return nil, err
		}
	}
	state, err := ReadLockfile(dc.Path, !o.DryRun, false)
	if err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp("", "dcb-upgrade-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	resolved, err := r.ResolveWithLockfile(ctx, dc.Features, order, stage, filepath.Dir(dc.Path), state.Lockfile)
	if err != nil {
		return nil, err
	}
	if err := state.Prepare(resolved); err != nil {
		return nil, err
	}
	var b strings.Builder
	encoder := json.NewEncoder(&b)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(GenerateLockfile(resolved)); err != nil {
		return nil, err
	}
	if !o.DryRun {
		// Upgrade forces a canonical rewrite after resolution, even when the old
		// JSON differs only in whitespace. Dry-run retains normal refresh rules.
		state.prepared = []byte(b.String())
	}
	if err := state.Publish(ctx); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}
