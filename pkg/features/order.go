package features

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

// The install order mirrors upstream's round-based algorithm
// (reference/cli/src/spec-configuration/containerFeaturesOrder.ts):
//
//   - Each round installs every feature whose `dependsOn` hard
//     dependencies and `installsAfter` soft dependencies are already
//     installed. Soft dependencies that name a feature outside the
//     install set are dropped.
//   - `overrideFeatureInstallOrder` assigns a round priority (first entry
//     highest); within a round only the highest-priority candidates are
//     installed, so the override never breaks a dependency.
//   - Candidates within a round are sorted by registry/namespace, id, tag,
//     options and digest (OCI) or by path and options (local).
//   - A round with no candidates is a cycle and fails resolution.
//
// Soft dependencies and override entries match a feature by its
// registry/repository (or its resolved local path), ignoring tag and
// digest, and honoring the feature's `legacyIds`.

// node is one feature in the install graph.
type node struct {
	res Resolved
	// options is the canonical JSON of the options value as written in
	// devcontainer.json (or in a dependsOn entry).
	options string
	// optionsValue is the decoded options value, compared like upstream.
	optionsValue any
	// dependsOn holds the hard dependencies, each the node that installs
	// it.
	dependsOn []*node
	// installsAfter holds the soft dependencies that name a feature in
	// the install set.
	installsAfter []sourceRef
	// priority is the round priority from overrideFeatureInstallOrder.
	priority int
}

// sourceRef identifies a feature location independent of its version.
type sourceRef struct {
	local bool
	// resource is registry/repository for OCI features and the absolute
	// path for local features.
	resource string
}

// parseSourceRef converts a user-supplied feature id (as found in
// installsAfter or overrideFeatureInstallOrder) to a sourceRef. Ids that
// are neither local paths nor registry references (legacy bare ids) match
// nothing, so ok is false.
func parseSourceRef(id, localBaseDir string) (sourceRef, bool) {
	if isLocalSource(id) {
		p, err := localSourcePath(id, localBaseDir)
		if err != nil {
			return sourceRef{}, false
		}
		return sourceRef{local: true, resource: p}, true
	}
	if !strings.Contains(id, "/") {
		return sourceRef{}, false
	}
	ref, err := oci.ParseReference(id)
	if err != nil {
		return sourceRef{}, false
	}
	return sourceRef{resource: ref.Registry + "/" + ref.Repository}, true
}

func (n *node) ref() sourceRef {
	if n.res.Reference != nil {
		return sourceRef{resource: n.res.Reference.Registry + "/" + n.res.Reference.Repository}
	}
	return sourceRef{local: true, resource: n.res.localPath}
}

// aliases returns the feature's current id followed by its legacy ids.
func (n *node) aliases() []string {
	out := []string{}
	if n.res.Metadata.ID != "" {
		out = append(out, n.res.Metadata.ID)
	}
	return append(out, n.res.Metadata.LegacyIDs...)
}

// satisfies reports whether n is the feature a soft dependency names.
func (n *node) satisfies(want sourceRef) bool {
	have := n.ref()
	if have.local != want.local {
		return false
	}
	if have.resource == want.resource {
		return true
	}
	if have.local {
		return false
	}
	prefix := path.Dir(have.resource)
	for _, alias := range n.aliases() {
		if prefix+"/"+alias == want.resource {
			return true
		}
	}
	return false
}

// equals reports whether a and b are the same feature with the same
// options, so that one install satisfies both.
func (a *node) equals(b *node) bool {
	return compareNodes(a, b) == 0
}

// compareNodes orders two features within a round. It returns 0 when the
// features are equal.
func compareNodes(a, b *node) int {
	ar, br := a.ref(), b.ref()
	if ar.local != br.local {
		return strings.Compare(a.res.Source, b.res.Source)
	}
	if ar.local {
		if c := strings.Compare(ar.resource, br.resource); c != 0 {
			return c
		}
		return compareOptions(a.optionsValue, b.optionsValue)
	}
	ad, bd := a.res.Digest, b.res.Digest
	if ad != "" && ad == bd && compareOptions(a.optionsValue, b.optionsValue) == 0 {
		return 0
	}
	if c := compareOCIResource(a, b); c != 0 {
		return c
	}
	at, bt := a.res.Reference.Tag, b.res.Reference.Tag
	if at != "" && bt != "" && at != bt {
		return strings.Compare(at, bt)
	}
	if c := compareOptions(a.optionsValue, b.optionsValue); c != 0 {
		return c
	}
	return strings.Compare(ad, bd)
}

func compareOCIResource(a, b *node) int {
	ar, br := a.ref().resource, b.ref().resource
	if c := strings.Compare(path.Dir(ar), path.Dir(br)); c != 0 {
		return c
	}
	aIDs, bIDs := a.aliases(), b.aliases()
	if len(aIDs) == 0 {
		aIDs = []string{path.Base(ar)}
	}
	if len(bIDs) == 0 {
		bIDs = []string{path.Base(br)}
	}
	for _, x := range aIDs {
		for _, y := range bIDs {
			if x == y {
				return 0
			}
		}
	}
	return strings.Compare(path.Base(ar), path.Base(br))
}

// compareOptions mirrors upstream's optionsCompareTo: strings and
// booleans compare by value, objects by key count, then keys, then
// string or boolean values; other value types do not take part.
// Mismatched types compare by their JavaScript type name.
func compareOptions(a, b any) int {
	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			return strings.Compare(av, bv)
		}
	case bool:
		if bv, ok := b.(bool); ok {
			return compareBool(av, bv)
		}
	case map[string]any:
		if bv, ok := b.(map[string]any); ok {
			return compareOptionObjects(av, bv)
		}
	}
	return strings.Compare(jsTypeOf(a), jsTypeOf(b))
}

func compareOptionObjects(a, b map[string]any) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	aKeys, bKeys := sortedKeys(a), sortedKeys(b)
	for i := range aKeys {
		if aKeys[i] != bKeys[i] {
			return strings.Compare(aKeys[i], bKeys[i])
		}
		switch av := a[aKeys[i]].(type) {
		case string:
			if bv, ok := b[bKeys[i]].(string); ok {
				if c := strings.Compare(av, bv); c != 0 {
					return c
				}
			}
		case bool:
			if bv, ok := b[bKeys[i]].(bool); ok {
				if c := compareBool(av, bv); c != 0 {
					return c
				}
			}
		}
	}
	return 0
}

func compareBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case a:
		return 1
	}
	return -1
}

func jsTypeOf(v any) string {
	switch v.(type) {
	case nil:
		return "undefined"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64:
		return "number"
	}
	return "object"
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// decodeOptions returns the canonical JSON and the decoded value of an
// options value.
func decodeOptions(raw json.RawMessage) (string, any) {
	if len(raw) == 0 || string(raw) == "null" {
		return "null", nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw), nil
	}
	out, err := json.Marshal(v) // map keys are sorted
	if err != nil {
		return string(raw), v
	}
	return string(out), v
}

// linkSoftDependencies records each node's installsAfter entries that
// name a feature in the install set.
func linkSoftDependencies(nodes []*node, localBaseDir string) {
	for _, n := range nodes {
		n.installsAfter = nil
		for _, id := range n.res.Metadata.InstallsAfter {
			ref, ok := parseSourceRef(id, localBaseDir)
			if !ok {
				continue
			}
			for _, other := range nodes {
				if other != n && other.satisfies(ref) {
					n.installsAfter = append(n.installsAfter, ref)
					break
				}
			}
		}
	}
}

// applyOverride sets the round priority from overrideFeatureInstallOrder:
// the first entry gets the highest priority.
func applyOverride(nodes []*node, override []string, localBaseDir string) error {
	for i, id := range override {
		ref, ok := parseSourceRef(id, localBaseDir)
		if !ok {
			return fmt.Errorf("feature %q in overrideFeatureInstallOrder could not be processed", id)
		}
		priority := len(override) - i
		for _, n := range nodes {
			if n.satisfies(ref) && priority > n.priority {
				n.priority = priority
			}
		}
	}
	return nil
}

// installOrder returns the features in install order.
func installOrder(nodes []*node, override []string, localBaseDir string) ([]Resolved, error) {
	linkSoftDependencies(nodes, localBaseDir)
	if err := applyOverride(nodes, override, localBaseDir); err != nil {
		return nil, err
	}

	remaining := append([]*node(nil), nodes...)
	installed := make([]*node, 0, len(nodes))
	ready := func(n *node) bool {
		for _, dep := range n.dependsOn {
			found := false
			for _, done := range installed {
				if done == dep {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		for _, dep := range n.installsAfter {
			found := false
			for _, done := range installed {
				if done.satisfies(dep) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}

	for len(remaining) > 0 {
		var round []*node
		maxPriority := 0
		for _, n := range remaining {
			if ready(n) {
				round = append(round, n)
				if n.priority > maxPriority {
					maxPriority = n.priority
				}
			}
		}
		if len(round) == 0 {
			ids := make([]string, 0, len(remaining))
			for _, n := range remaining {
				ids = append(ids, n.res.Source)
			}
			return nil, fmt.Errorf("circular feature dependency among: %s", strings.Join(ids, ", "))
		}
		filtered := round[:0]
		for _, n := range round {
			if n.priority == maxPriority {
				filtered = append(filtered, n)
			}
		}
		round = filtered
		sort.SliceStable(round, func(i, j int) bool { return compareNodes(round[i], round[j]) < 0 })

		next := remaining[:0]
		for _, n := range remaining {
			inRound := false
			for _, r := range round {
				if r == n {
					inRound = true
					break
				}
			}
			if !inRound {
				next = append(next, n)
			}
		}
		remaining = next
		installed = append(installed, round...)
	}

	out := make([]Resolved, len(installed))
	for i, n := range installed {
		out[i] = n.res
	}
	return out, nil
}
