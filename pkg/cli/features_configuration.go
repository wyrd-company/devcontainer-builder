// relationships:
//
//	references: [options_configuration, stage, features]
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// featuresConfiguration projects the resolved feature graph into upstream's
// featureSets shape, preserving metadata fields unknown to the Go model.
func (a *application) featuresConfiguration(dc *spec.DevContainer, configuration map[string]any, extra spec.OrderedMap) (map[string]any, error) {
	raw, err := json.Marshal(configuration)
	if err != nil {
		return nil, err
	}
	var snapshot spec.DevContainer
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	snapshot.Path = dc.Path
	snapshot.Features = snapshot.Features.WithAdditional(extra)
	resolved, cleanup, err := a.resolveFeatures(&snapshot)
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, nil
	}
	sets := make([]any, 0, len(resolved))
	for i, f := range resolved {
		id := filepath.Base(f.Source)
		if f.Tarball != "" {
			id = f.Metadata.ID
		}
		if f.Reference != nil {
			id = filepath.Base(f.Reference.Repository)
		}
		target := filepath.Join(filepath.Dir(f.Dir), fmt.Sprintf("%s_%d", id, i))
		if err := os.Rename(f.Dir, target); err != nil {
			cleanup()
			return nil, err
		}
		f.Dir = target
		set, err := resolvedFeatureSet(f, i, filepath.Dir(dc.Path))
		if err != nil {
			cleanup()
			return nil, err
		}
		sets = append(sets, set)
	}
	return map[string]any{"featureSets": sets, "dstFolder": filepath.Dir(resolved[0].Dir)}, nil
}

func resolvedFeatureSet(f features.Resolved, i int, configDir string) (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(f.Dir, "devcontainer-feature.json"))
	if err != nil {
		return nil, err
	}
	var feature map[string]any
	if err := json.Unmarshal(spec.StripJSONC(raw), &feature); err != nil {
		return nil, err
	}
	id := filepath.Base(f.Source)
	source := map[string]any{"type": "file-path", "userFeatureId": f.Source}
	if f.Tarball != "" {
		id = f.Metadata.ID
		source["type"], source["tarballUri"] = "direct-tarball", f.Tarball
		feature["name"] = f.Source
	} else if f.Reference == nil {
		path := f.Source
		if !filepath.IsAbs(path) {
			path = filepath.Join(configDir, path)
		}
		source["resolvedFilePath"] = filepath.Clean(path)
		feature["name"] = f.Source
	} else {
		ref := f.Reference
		id = filepath.Base(ref.Repository)
		resource := ref.Registry + "/" + ref.Repository
		version := ref.Tag
		refFields := map[string]any{"registry": ref.Registry, "owner": strings.Split(ref.Repository, "/")[0], "namespace": filepath.Dir(ref.Repository), "path": ref.Repository, "resource": resource, "id": id}
		if ref.Digest != "" {
			version = ref.Digest
			refFields["digest"] = ref.Digest
		} else {
			refFields["tag"] = ref.Tag
		}
		refFields["version"] = version
		source["type"], source["featureRef"], source["manifest"], source["manifestDigest"], source["userFeatureIdWithoutVersion"] = "oci", refFields, f.Manifest, f.Digest, resource
	}
	value := f.Value
	feature["id"], feature["value"], feature["included"], feature["consecutiveId"], feature["cachePath"] = id, value, true, fmt.Sprintf("%s_%d", id, i), f.Dir
	set := map[string]any{"features": []any{feature}, "sourceInformation": source, "internalVersion": "2"}
	if f.Digest != "" {
		set["computedDigest"] = f.Digest
	}
	return set, nil
}

// featureConfigurationSnapshot applies existing-container substitutions before
// feature options are resolved, as upstream read-configuration does.
func (a *application) featureConfigurationSnapshot(cmd *cobra.Command, dc *spec.DevContainer, workspace, id string, configuration map[string]any) (map[string]any, error) {
	cli, err := newDockerClient()
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	containerID, err := a.findContainer(cmd.Context(), cli, dc, workspace, id)
	if id == "" && (errors.Is(err, hostdocker.ErrContainerNotFound) || errors.Is(err, dockercompose.ErrProjectNotFound)) {
		return configuration, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := cli.ContainerInspect(cmd.Context(), containerID)
	if err != nil {
		return nil, err
	}
	labels, env := map[string]string{}, map[string]string{}
	if info.Config != nil {
		labels = info.Config.Labels
		for _, pair := range info.Config.Env {
			k, v, ok := strings.Cut(pair, "=")
			if ok {
				env[k] = v
			}
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(dc.Raw, &raw); err != nil {
		return nil, err
	}
	sub := a.substitutionContext(dc, workspace, labels)
	result := transformConfiguration(raw, func(s string) string {
		return substitute.ContainerSubstitute(substitute.ContainerContext{ContainerEnv: env}, substitute.Substitute(sub, s))
	}).(map[string]any)
	result["configFilePath"] = dc.Path
	return result, nil
}
