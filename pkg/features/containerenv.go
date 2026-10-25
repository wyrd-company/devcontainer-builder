package features

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// ContainerEnvKeys returns the keys of m.ContainerEnv in the order the
// feature's devcontainer-feature.json declares them. The reference CLI emits
// one ENV instruction per key in this order, so a value can reference a key
// declared before it. A key with no recorded position, as in a Metadata built
// in code, follows the declared keys in sorted order.
func (m Metadata) ContainerEnvKeys() []string {
	keys := make([]string, 0, len(m.ContainerEnv))
	seen := make(map[string]bool, len(m.ContainerEnv))
	for _, k := range m.containerEnvOrder {
		if _, ok := m.ContainerEnv[k]; ok && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	var rest []string
	for k := range m.ContainerEnv {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// containerEnvOrder reads the declaration order of the containerEnv keys in
// a devcontainer-feature.json document. It decodes the field the same way
// encoding/json fills Metadata.ContainerEnv, so both see the same object.
func containerEnvOrder(data []byte) ([]string, error) {
	var doc struct {
		ContainerEnv spec.OrderedMap `json:"containerEnv"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.ContainerEnv.Keys, nil
}

// QuoteContainerEnv quotes a feature's containerEnv value as the reference
// CLI writes it into its Dockerfile ENV instruction: double quotes, with `"`
// and `\` escaped and `$` left for the Dockerfile to expand.
func QuoteContainerEnv(v string) string {
	return `"` + containerEnvEscaper.Replace(v) + `"`
}

var containerEnvEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)
