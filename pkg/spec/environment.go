// relationships:
//
//	references: [spec, ../../reference/spec/schemas/devContainer.base.schema.json]
package spec

import (
	"encoding/json"
	"fmt"
)

// ValidateEnvironment checks the schema types without coercing extension values.
// It also applies to configurations constructed by library callers.
func (d *DevContainer) ValidateEnvironment() error {
	var raw map[string]json.RawMessage
	if len(d.Raw) > 0 {
		if err := json.Unmarshal(d.Raw, &raw); err != nil {
			return fmt.Errorf("configuration: %w", err)
		}
	}
	for _, field := range []struct {
		name     string
		values   map[string]any
		nullable bool
	}{
		{"containerEnv", d.ContainerEnv, false}, {"remoteEnv", d.UpdateRemoteEnv, true},
	} {
		if value, exists := raw[field.name]; exists {
			var object map[string]any
			if err := json.Unmarshal(value, &object); err != nil || object == nil {
				return fmt.Errorf("%s must be an object", field.name)
			}
		}
		for key, value := range field.values {
			if _, ok := value.(string); !ok && !(field.nullable && value == nil) {
				return fmt.Errorf("%s[%q] must be %s", field.name, key, map[bool]string{true: "a string or null", false: "a string"}[field.nullable])
			}
		}
	}
	return nil
}

// ValidateSource checks the additional source requirement for build and up.
// Validate deliberately accepts common-only documents for read operations.
func (d *DevContainer) ValidateSource() error {
	if d.Mode() == ModeCompose || d.Image != "" || d.DockerfilePath() != "" {
		return nil
	}
	return fmt.Errorf("configuration has no image, build Dockerfile, dockerFile, or dockerComposeFile source")
}
