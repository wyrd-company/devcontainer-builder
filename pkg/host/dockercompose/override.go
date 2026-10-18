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
	// RemoteEnv is layered onto the primary service's environment.
	// Values flow from devcontainer.json's `remoteEnv`.
	RemoteEnv map[string]string
	// OverrideCommand: when true (matching the spec's default in compose
	// mode), the primary service's command is replaced by a long-running
	// keepalive so lifecycle commands and exec attach have a live
	// container regardless of the image's ENTRYPOINT/CMD. When false,
	// the original entrypoint/command are preserved.
	OverrideCommand bool
	// Entrypoints are the features' entrypoint contributions, run by the
	// keepalive command when OverrideCommand is true.
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
	if len(in.RemoteEnv) > 0 {
		svc["environment"] = stringMapToSortedSlice(in.RemoteEnv)
	}
	if in.OverrideCommand {
		// `/bin/sh -c <script> -` — the trailing "-" sets $0 so the
		// script's `exec "$@"` sees an empty $@ and the trap is what
		// actually keeps the process alive.
		svc["entrypoint"] = []any{"/bin/sh", "-c"}
		svc["command"] = []any{keepaliveScript(in.Entrypoints), "-"}
		svc["stdin_open"] = true
		svc["tty"] = true
	}
	if len(in.Labels) > 0 {
		svc["labels"] = stringMapToSortedSlice(in.Labels)
	}

	doc := map[string]any{
		"services": map[string]any{
			in.Spec.Service: svc,
		},
	}
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
