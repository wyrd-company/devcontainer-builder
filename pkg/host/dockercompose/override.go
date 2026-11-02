// relationships:
//
//	references: [up, configuration]
package dockercompose

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// OverrideInputs feeds OverrideYAML.
type OverrideInputs struct {
	// Spec is the parsed devcontainer.json (must be compose mode).
	Spec *spec.DevContainer
	// PrimaryImage, when non-empty, replaces the primary service's
	// `image:` (used after a derived feature build).
	PrimaryImage string
	// ContainerEnv is layered onto the primary service environment.
	ContainerEnv map[string]string
	// RemoteEnv is retained for source compatibility. It is exec-only and
	// never added to a container environment.
	RemoteEnv map[string]string
	// OverrideCommand defaults to false in Compose mode. True replaces the
	// original command with the Feature wrapper and keepalive.
	OverrideCommand bool
	// Entrypoints are the ordered Feature contributions used by the wrapper.
	Entrypoints []string
	// Labels merged onto the primary service.
	Labels map[string]string
}

// keepaliveScript matches the docker host's keepalive script: trap
// SIGTERM cleanly so `docker stop` returns quickly, run the features'
// entrypoints, and use `sleep & wait` so the trap actually fires (a bare
// `sleep infinity` would block trap delivery until it returned).
func keepaliveScript(entrypoints []string) string {
	lines := append([]string{"echo Container started", `trap "exit 0" 15`}, entrypoints...)
	lines = append(lines, `exec "$@"`, `while sleep 1 & wait $!; do :; done`)
	return strings.Join(lines, "\n")
}

// OverrideYAML returns a YAML document suitable for appending to the
// compose merge chain. Only the primary service is touched; other
// services pass through unchanged.
func OverrideYAML(in OverrideInputs) ([]byte, error) {
	if in.Spec == nil {
		return nil, fmt.Errorf("OverrideInputs.Spec is required")
	}
	if in.Spec.Service == "" {
		return nil, fmt.Errorf("OverrideInputs.Spec.Service is required")
	}

	svc := map[string]any{}
	if in.PrimaryImage != "" {
		svc["image"] = in.PrimaryImage
	}
	if len(in.ContainerEnv) > 0 {
		svc["environment"] = stringMapToSortedSlice(in.ContainerEnv)
	}
	if in.OverrideCommand {
		// `/bin/sh -c <script> -` — the trailing "-" sets $0 so the
		// script's `exec "$@"` sees an empty $@ and the trap is what
		// actually keeps the process alive.
		svc["entrypoint"] = []any{"/bin/sh", "-c"}
		svc["command"] = []any{keepaliveScript(in.Entrypoints), "-"}

	}
	if len(in.Labels) > 0 {
		svc["labels"] = stringMapToSortedSlice(in.Labels)
	}

	doc := map[string]any{
		"services": map[string]any{
			in.Spec.Service: svc,
		},
	}
	// This document is read by compose-go interpolation. Runtime assignments
	// after loading use literal single dollars instead.
	escapeComposeDollars(doc)
	out, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal override: %w", err)
	}
	return out, nil
}

// stringMapToSortedSlice returns "k=v" pairs, sorted, so generated YAML
// is byte-stable across runs.
func stringMapToSortedSlice(m map[string]string) []any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

func escapeComposeDollars(m map[string]any) {
	for k, value := range m {
		switch v := value.(type) {
		case string:
			m[k] = strings.ReplaceAll(v, "$", "$$")
		case map[string]any:
			escapeComposeDollars(v)
		case []any:
			for i, item := range v {
				if text, ok := item.(string); ok {
					v[i] = strings.ReplaceAll(text, "$", "$$")
				}
			}
		}
	}
}
