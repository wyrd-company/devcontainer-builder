package features

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// ContainerEnvKeys returns the keys of m.ContainerEnv in the order the
// reference CLI applies them: ECMAScript Object.keys order over the
// devcontainer-feature.json object. Keys that are array indices come first in
// ascending numeric order, then the other keys in declaration order. The
// reference emits one ENV instruction per key in this order, so a value can
// reference a key applied before it. A key with no recorded position, as in a
// Metadata built in code, follows the other keys in sorted order.
func (m Metadata) ContainerEnvKeys() []string {
	var indices, named, rest []string
	seen := make(map[string]bool, len(m.ContainerEnv))
	for _, k := range m.containerEnvOrder {
		if _, ok := m.ContainerEnv[k]; !ok || seen[k] {
			continue
		}
		seen[k] = true
		if isArrayIndex(k) {
			indices = append(indices, k)
		} else {
			named = append(named, k)
		}
	}
	for k := range m.ContainerEnv {
		if seen[k] {
			continue
		}
		if isArrayIndex(k) {
			indices = append(indices, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(indices, func(i, j int) bool {
		a, _ := strconv.ParseUint(indices[i], 10, 32)
		b, _ := strconv.ParseUint(indices[j], 10, 32)
		return a < b
	})
	sort.Strings(rest)
	return append(append(indices, named...), rest...)
}

// isArrayIndex reports whether key is an ECMAScript array index: the
// canonical decimal form of an integer from 0 to 2^32-2. "01", "-1", "1.0"
// and "4294967295" are not array indices.
func isArrayIndex(key string) bool {
	n, err := strconv.ParseUint(key, 10, 32)
	return err == nil && n != 1<<32-1 && strconv.FormatUint(n, 10) == key
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
