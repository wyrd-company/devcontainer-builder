package features

import (
	"fmt"
	"path"
	"slices"
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
// digest. A soft dependency also matches through its own aliases: the
// current id and `legacyIds` that the referenced feature's metadata
// declares, under the reference's registry/namespace. Override entries
// carry no aliases, as upstream.

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
	// aliases are the ids the referenced feature declares for itself
	// (current id, then legacy ids). Only soft dependencies carry them.
	aliases []string
}

// aliasLookup returns the aliases declared by the metadata of the OCI
// feature that a soft dependency id references.
type aliasLookup func(id string) ([]string, error)

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
	return n.res.Metadata.aliases()
}

// satisfies reports whether n is the feature a soft dependency names:
// the same resource, or a resource the dependency's aliases name under
// its registry/namespace (upstream satisfiesSoftDependency).
func (n *node) satisfies(want sourceRef) bool {
	have := n.ref()
	if have.local != want.local {
		return false
	}
	if have.resource == want.resource {
		return true
	}
	if want.local {
		return false
	}
	prefix := path.Dir(want.resource)
	for _, alias := range want.aliases {
		if prefix+"/"+alias == have.resource {
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

// linkSoftDependencies records each node's installsAfter entries that
// name a feature in the install set. A referenced feature's aliases are
// looked up only when they could decide the match: no feature matches
// the resource and some feature shares its registry/namespace.
func linkSoftDependencies(nodes []*node, localBaseDir string, lookup aliasLookup) error {
	for _, n := range nodes {
		n.installsAfter = nil
		for _, id := range n.res.Metadata.InstallsAfter {
			ref, ok := parseSourceRef(id, localBaseDir)
			if !ok {
				continue
			}
			if !ref.local && !otherSatisfies(nodes, n, ref) && sharesNamespace(nodes, n, ref) {
				aliases, err := lookup(id)
				if err != nil {
					return fmt.Errorf("installsAfter dependency %q of feature %s could not be processed: %w", id, n.res.Source, err)
				}
				ref.aliases = aliases
			}
			if otherSatisfies(nodes, n, ref) {
				n.installsAfter = append(n.installsAfter, ref)
			}
		}
	}
	return nil
}

func otherSatisfies(nodes []*node, self *node, ref sourceRef) bool {
	return slices.ContainsFunc(nodes, func(o *node) bool { return o != self && o.satisfies(ref) })
}

// sharesNamespace reports whether another OCI feature lives under the
// registry/namespace of ref, the only place an alias can point.
func sharesNamespace(nodes []*node, self *node, ref sourceRef) bool {
	return slices.ContainsFunc(nodes, func(o *node) bool {
		have := o.ref()
		return o != self && !have.local && path.Dir(have.resource) == path.Dir(ref.resource)
	})
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
func installOrder(nodes []*node, override []string, localBaseDir string, lookup aliasLookup) ([]Resolved, error) {
	if err := linkSoftDependencies(nodes, localBaseDir, lookup); err != nil {
		return nil, err
	}
	if err := applyOverride(nodes, override, localBaseDir); err != nil {
		return nil, err
	}

	remaining := append([]*node(nil), nodes...)
	var installed []*node
	for len(remaining) > 0 {
		round := nextRound(remaining, installed)
		if len(round) == 0 {
			ids := make([]string, 0, len(remaining))
			for _, n := range remaining {
				ids = append(ids, n.res.Source)
			}
			return nil, fmt.Errorf("circular feature dependency among: %s", strings.Join(ids, ", "))
		}
		inRound := map[*node]bool{}
		for _, n := range round {
			inRound[n] = true
		}
		next := remaining[:0]
		for _, n := range remaining {
			if !inRound[n] {
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

// nextRound returns the features to install next: those whose
// dependencies are installed, limited to the highest round priority and
// sorted.
func nextRound(remaining, installed []*node) []*node {
	var round []*node
	maxPriority := 0
	for _, n := range remaining {
		if n.ready(installed) {
			round = append(round, n)
			maxPriority = max(maxPriority, n.priority)
		}
	}
	filtered := round[:0]
	for _, n := range round {
		if n.priority == maxPriority {
			filtered = append(filtered, n)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool { return compareNodes(filtered[i], filtered[j]) < 0 })
	return filtered
}

// ready reports whether every hard and soft dependency of n is
// installed.
func (n *node) ready(installed []*node) bool {
	for _, dep := range n.dependsOn {
		if !slices.Contains(installed, dep) {
			return false
		}
	}
	for _, dep := range n.installsAfter {
		if !slices.ContainsFunc(installed, func(done *node) bool { return done.satisfies(dep) }) {
			return false
		}
	}
	return true
}
