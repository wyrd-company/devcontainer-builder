package spec

import (
	"encoding/json"
	"fmt"
)

// ParseFeatures parses a JSONC object in the form of the devcontainer.json
// features property, such as the value of upstream's --additional-features.
func ParseFeatures(data []byte) (OrderedMap, error) {
	var m OrderedMap
	if err := json.Unmarshal(StripJSONC(data), &m); err != nil {
		return OrderedMap{}, fmt.Errorf("parse features: %w", err)
	}
	return m, nil
}

// WithAdditional returns m followed by each feature of extra whose key m does
// not declare. For an equal key the options in m win, as in upstream
// userFeaturesToArray. Neither m nor extra is modified.
func (m OrderedMap) WithAdditional(extra OrderedMap) OrderedMap {
	out := OrderedMap{Keys: append([]string(nil), m.Keys...), Values: make(map[string]json.RawMessage, len(m.Keys)+len(extra.Keys))}
	for _, key := range m.Keys {
		out.Values[key] = m.Values[key]
	}
	for _, key := range extra.Keys {
		if _, declared := out.Values[key]; declared {
			continue
		}
		out.Keys = append(out.Keys, key)
		out.Values[key] = extra.Values[key]
	}
	return out
}
