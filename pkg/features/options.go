package features

import (
	"encoding/json"
	"fmt"
	"sort"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// compareOptions mirrors upstream's optionsCompareTo: strings and
// booleans compare by value, objects by key count, then keys, then
// string or boolean values; other value types do not take part.
// Mismatched types compare by their JavaScript type name.
func compareOptions(a, b any) int {
	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			return compareFeatureStrings(av, bv)
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
	return compareFeatureStrings(jsTypeOf(a), jsTypeOf(b))
}

func compareOptionObjects(a, b map[string]any) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	aKeys, bKeys := sortedKeys(a), sortedKeys(b)
	for i := range aKeys {
		if aKeys[i] != bKeys[i] {
			return compareFeatureStrings(aKeys[i], bKeys[i])
		}
		switch av := a[aKeys[i]].(type) {
		case string:
			if bv, ok := b[bKeys[i]].(string); ok {
				if c := compareFeatureStrings(av, bv); c != 0 {
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

// parseOptions decodes a feature's options value. A scalar is shorthand
// for the "version" option; scalar reports that form.
func parseOptions(raw json.RawMessage) (opts map[string]any, scalar bool, err error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	if err := json.Unmarshal(raw, &opts); err != nil {
		var v any
		if err2 := json.Unmarshal(raw, &v); err2 != nil {
			return nil, false, fmt.Errorf("parse options: %w", err)
		}
		return map[string]any{"version": v}, true, nil
	}
	return opts, false, nil
}

// compareFeatureStrings follows the upstream CLI default English collation.
func compareFeatureStrings(a, b string) int { return collate.New(language.English).CompareString(a, b) }
