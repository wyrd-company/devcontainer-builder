package spec

import "encoding/json"

// MigrateLegacyVSCode moves top-level extensions and settings into
// customizations.vscode, as upstream's updateFromOldProperties does. Extensions
// append; settings already in customizations win. With devPort, a top-level
// devPort moves too unless customizations.vscode already has one, as for
// configurations. It reports whether data changed.
func MigrateLegacyVSCode(data []byte, devPort bool) ([]byte, bool, error) {
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, false, err
	}
	_, hasDevPort := obj["devPort"]
	hasDevPort = devPort && hasDevPort
	if obj["extensions"] == nil && obj["settings"] == nil && !hasDevPort {
		return data, false, nil
	}
	customizations, _ := obj["customizations"].(map[string]any)
	if customizations == nil {
		customizations = map[string]any{}
		obj["customizations"] = customizations
	}
	vscode, _ := customizations["vscode"].(map[string]any)
	if vscode == nil {
		vscode = map[string]any{}
		customizations["vscode"] = vscode
	}
	if legacy := obj["extensions"]; legacy != nil {
		existing, _ := vscode["extensions"].([]any)
		if list, ok := legacy.([]any); ok {
			vscode["extensions"] = append(existing, list...)
		} else {
			vscode["extensions"] = append(existing, legacy)
		}
		delete(obj, "extensions")
	}
	if legacy, ok := obj["settings"].(map[string]any); ok {
		existing, _ := vscode["settings"].(map[string]any)
		for key, value := range existing {
			legacy[key] = value
		}
		vscode["settings"] = legacy
		delete(obj, "settings")
	}
	if _, set := vscode["devPort"]; hasDevPort && !set {
		vscode["devPort"] = obj["devPort"]
		delete(obj, "devPort")
	}
	out, err := json.Marshal(obj)
	return out, true, err
}
