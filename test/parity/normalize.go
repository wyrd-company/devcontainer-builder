// relationships:
//
//	references: README
package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// Normalizer replaces only identities learned from this observation. Ordered
// arrays (commands and metadata install order) remain ordered.
type Normalizer struct{ Replacements map[string]string }

func (n Normalizer) Value(v any) any {
	switch x := v.(type) {
	case string:
		keys := make([]string, 0, len(n.Replacements))
		for k := range n.Replacements {
			if k != "" {
				keys = append(keys, k)
			}
		}
		sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
		for _, k := range keys {
			x = strings.ReplaceAll(x, k, n.Replacements[k])
		}
		return x
	case map[string]any:
		out := map[string]any{}
		for k, value := range x {
			out[k] = n.Value(value)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			out[i] = n.Value(value)
		}
		return out
	default:
		return v
	}
}

type Observation struct {
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}
type Dimension struct {
	Pass     bool        `json:"pass"`
	Upstream Observation `json:"upstream"`
	Go       Observation `json:"go"`
}
type FixtureResult struct {
	Name       string               `json:"name"`
	Pass       bool                 `json:"pass"`
	Dimensions map[string]Dimension `json:"dimensions"`
	Passed     int                  `json:"passed"`
	Total      int                  `json:"total"`
}
type Report struct {
	Version       string          `json:"upstreamVersion"`
	Adapter       string          `json:"adapter"`
	Fixtures      []FixtureResult `json:"fixtures"`
	Passed        int             `json:"passed"`
	Total         int             `json:"total"`
	Score         float64         `json:"score"`
	Skipped       string          `json:"skipped,omitempty"`
	CleanupErrors []string        `json:"cleanupErrors,omitempty"`
}

var dimensions = []string{"result", "metadata", "env", "labels", "mounts", "user", "workdir", "lifecycle", "features"}

func compare(name string, a, b map[string]Observation) FixtureResult {
	out := FixtureResult{Name: name, Dimensions: map[string]Dimension{}}
	for _, key := range dimensions {
		av, aok := a[key]
		bv, bok := b[key]
		pass := aok && bok && av.Error == "" && bv.Error == "" && reflect.DeepEqual(av.Value, bv.Value)
		out.Dimensions[key] = Dimension{pass, av, bv}
		out.Total++
		if pass {
			out.Passed++
		}
	}
	out.Pass = out.Passed == out.Total
	return out
}

func jsonValue(b []byte) (any, error) { var v any; err := json.Unmarshal(b, &v); return v, err }

// Result normalizes protocol identity and timestamp fields only at the JSON
// result boundary, so an environment variable with the same name stays visible.
func (n Normalizer) Result(v any) any {
	normalized := n.Value(v)
	if result, ok := normalized.(map[string]any); ok {
		for _, key := range []string{"containerId", "imageId"} {
			if _, exists := result[key]; exists {
				result[key] = "<id>"
			}
		}
		for _, key := range []string{"created", "Created", "timestamp"} {
			if _, exists := result[key]; exists {
				result[key] = "<timestamp>"
			}
		}
	}
	return normalized
}
