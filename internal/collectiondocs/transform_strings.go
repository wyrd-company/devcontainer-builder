// relationships:
//
//	references: [jsonc_value, ../../pkg/features/dependencies]
package collectiondocs

import "encoding/json"

// TransformJSONStrings maps values without losing JavaScript string code units.
// Object keys and non-string values are preserved.
func TransformJSONStrings(data []byte, transform func(string) string) json.RawMessage {
	p := documentationParser{data: documentationUTF8(data), ownProperties: true}
	p.next()
	value, _ := p.value()
	var walk func(any) any
	walk = func(value any) any {
		switch v := value.(type) {
		case string:
			return transform(v)
		case *documentationObject:
			for _, key := range documentationKeys(v) {
				v.values[key] = walk(v.values[key])
			}
		case []any:
			for i, item := range v {
				v[i] = walk(item)
			}
		}
		return value
	}
	return stringifyValue(walk(value))
}
