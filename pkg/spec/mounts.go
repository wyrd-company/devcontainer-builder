// relationships:
//
//	references: [spec, ../../reference/spec/schemas/devContainer.base.schema]
package spec

import (
	"fmt"
	"sort"
)

// ValidateMounts checks the devcontainer mount object schema. Strings use the
// container runtime's mount syntax and are left to the host to interpret.
// It also serves callers that construct a DevContainer in memory.
func (d *DevContainer) ValidateMounts() error {
	for i, raw := range d.Mounts {
		if _, ok := raw.(string); ok {
			continue
		}
		obj, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("mounts[%d]: expected a mount string or object", i)
		}
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			switch key {
			case "type", "source", "target":
				if _, ok := obj[key].(string); !ok {
					return fmt.Errorf("mounts[%d].%s: expected a string", i, key)
				}
			default:
				return fmt.Errorf("mounts[%d]: unsupported object property %q; mount objects allow only type, source, and target; for Docker read-only mounts use target \"/data,readonly\" or a mount string ending in ,readonly", i, key)
			}
		}
		typ, ok := obj["type"].(string)
		if !ok || (typ != "bind" && typ != "volume") {
			return fmt.Errorf("mounts[%d].type: required and must be bind or volume", i)
		}
		if _, ok := obj["target"].(string); !ok {
			return fmt.Errorf("mounts[%d].target: required string property", i)
		}
	}
	return nil
}
