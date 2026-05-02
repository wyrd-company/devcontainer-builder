package features

import (
	"strings"
)

// order returns the features in the order they should be installed,
// honoring `installsAfter` (a soft dependency hint from the feature's own
// metadata) and `overrideFeatureInstallOrder` (an explicit user-supplied
// override from devcontainer.json).
//
// The override list takes precedence: any feature whose source matches an
// override entry (by full source or by feature ID) is installed in the
// override order, before the remaining features. The remaining features
// are sorted via a deterministic stable topological sort that respects
// installsAfter.
func order(in []Resolved, override []string) []Resolved {
	if len(in) <= 1 {
		return in
	}

	if len(override) > 0 {
		ordered, leftover := applyOverride(in, override)
		topo := topoSort(leftover)
		return append(ordered, topo...)
	}
	return topoSort(in)
}

func applyOverride(in []Resolved, override []string) (ordered []Resolved, rest []Resolved) {
	matched := make(map[int]bool)
	for _, want := range override {
		for i, f := range in {
			if matched[i] {
				continue
			}
			if matches(f, want) {
				ordered = append(ordered, f)
				matched[i] = true
				break
			}
		}
	}
	for i, f := range in {
		if !matched[i] {
			rest = append(rest, f)
		}
	}
	return ordered, rest
}

func matches(r Resolved, want string) bool {
	if r.Source == want {
		return true
	}
	if r.Metadata.ID == want {
		return true
	}
	// Allow matching by source without tag (e.g., "ghcr.io/.../node").
	if i := strings.LastIndex(r.Source, ":"); i > 0 {
		if r.Source[:i] == want {
			return true
		}
	}
	return false
}

// topoSort returns features in installation order respecting installsAfter
// while preserving the input order for unrelated features.
func topoSort(in []Resolved) []Resolved {
	n := len(in)
	indexBy := make(map[string]int, n*2)
	for i, f := range in {
		indexBy[f.Source] = i
		indexBy[f.Metadata.ID] = i
	}

	deps := make([][]int, n)
	for i, f := range in {
		for _, after := range f.Metadata.InstallsAfter {
			if j, ok := indexBy[after]; ok {
				deps[i] = append(deps[i], j)
			}
		}
	}

	visited := make([]int, n) // 0 unvisited, 1 visiting, 2 done
	out := make([]Resolved, 0, n)
	var visit func(int)
	visit = func(i int) {
		if visited[i] == 2 {
			return
		}
		if visited[i] == 1 {
			// Cycle: bail out without re-recursing; we'll still emit
			// later, but order is not guaranteed for cyclic groups.
			return
		}
		visited[i] = 1
		for _, d := range deps[i] {
			visit(d)
		}
		visited[i] = 2
		out = append(out, in[i])
	}

	for i := range in {
		visit(i)
	}
	return out
}
