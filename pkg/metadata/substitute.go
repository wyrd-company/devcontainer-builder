// relationships:
//
//	references: [metadata]
package metadata

import "encoding/json"

// Substitute applies a string substitution to every metadata value, including
// object mount fields and all lifecycle command forms. Keys stay unchanged.
func Substitute(entries []Entry, replace func(string) string) ([]Entry, error) {
	data, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	value = replaceStrings(value, replace)
	data, err = json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out []Entry
	err = json.Unmarshal(data, &out)
	return out, err
}
func replaceStrings(value any, replace func(string) string) any {
	switch v := value.(type) {
	case string:
		return replace(v)
	case []any:
		for i, item := range v {
			v[i] = replaceStrings(item, replace)
		}
	case map[string]any:
		for key, item := range v {
			v[key] = replaceStrings(item, replace)
		}
	}
	return value
}
