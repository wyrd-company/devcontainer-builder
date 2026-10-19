// relationships:
//
//	references: [features, mapping, ../../reference/cli/src/spec-configuration/containerFeaturesConfiguration]
package features

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func deprecatedFeatureOption(id string) (feature, option string) {
	switch id {
	case "gradle":
		return "java", "installGradle"
	case "maven":
		return "java", "installMaven"
	case "jupyterlab":
		return "python", "installJupyterlab"
	default:
		return "", ""
	}
}

// upgradeDeprecatedFeatureOptions follows upstream's preprocessing before ID
// mapping. It applies even when SkipAutoMapping is set and leaves input intact.
func (r *Resolver) upgradeDeprecatedFeatureOptions(input spec.OrderedMap) (spec.OrderedMap, error) {
	out := input.WithAdditional(spec.OrderedMap{})
	for _, id := range input.Keys {
		feature, option := deprecatedFeatureOption(id)
		if feature == "" {
			continue
		}
		qualified := "ghcr.io/devcontainers/features/" + feature
		target := ""
		for _, key := range out.Keys {
			if key == feature || key == qualified || strings.HasPrefix(key, qualified+":") {
				target = key
				break
			}
		}
		if target == "" {
			target = qualified + ":1"
			out.Keys = append(out.Keys, target)
		}
		var value any
		if raw, ok := out.Values[target]; ok {
			if err := json.Unmarshal(raw, &value); err != nil {
				return spec.OrderedMap{}, fmt.Errorf("feature %s: %w", target, err)
			}
		}
		options := map[string]any{}
		switch v := value.(type) {
		case map[string]any:
			options = v
		case string:
			options["version"] = v
		}
		options[option] = true
		raw, err := json.Marshal(options)
		if err != nil {
			return spec.OrderedMap{}, err
		}
		out.Values[target] = raw
		r.logf("warning: deprecated feature %s is an option of %s", id, target)
	}
	keys := out.Keys[:0]
	for _, id := range out.Keys {
		feature, _ := deprecatedFeatureOption(id)
		if feature == "" {
			keys = append(keys, id)
		} else {
			delete(out.Values, id)
		}
	}
	out.Keys = keys
	return out, nil
}
