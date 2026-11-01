// relationships:
//
//	references: [documentation_jsonc, ../../pkg/features/outdated]
package collectiondocs

import (
	"bytes"
	"encoding/json"
	"math"
)

// ReadJSONCValue exposes the collection reader's tolerant JavaScript JSONC
// semantics to reporting readers. It returns normalized JSON, retaining object
// enumeration order and recovered properties. Undefined values become null.
// Build admission and packaging continue to use their strict readers.
func ReadJSONCValue(data []byte) json.RawMessage {
	return jsoncValue(documentationJSON(data))
}

// ReadJSONCProperty reads a property with JavaScript lookup semantics, including
// inherited metadata properties. Missing properties normalize to null.
func ReadJSONCProperty(data []byte, name string) json.RawMessage {
	return jsoncValue(documentationGet(documentationJSON(data), name))
}

func jsoncValue(value any) json.RawMessage {
	var b bytes.Buffer
	switch v := value.(type) {
	case *documentationObject:
		b.WriteByte('{')
		for i, key := range documentationKeys(v) {
			if i > 0 {
				b.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			b.Write(encoded)
			b.WriteByte(':')
			b.Write(jsoncValue(v.values[key]))
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(jsoncValue(item))
		}
		b.WriteByte(']')
	case documentationMissing:
		b.WriteString("null")
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
		} else {
			encoded, _ := json.Marshal(v)
			b.Write(encoded)
		}
	default:
		encoded, _ := json.Marshal(v)
		b.Write(encoded)
	}
	return b.Bytes()
}
