// relationships:
//
//	references: [documentation_jsonc, ../../pkg/features/outdated]
package collectiondocs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode/utf16"
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

// StringifyJSONCValue uses JavaScript JSON.stringify string and number spelling
// while retaining the reporting reader's recovered property enumeration order.
func StringifyJSONCValue(data []byte) json.RawMessage {
	return stringifyValue(documentationJSON(data))
}

func stringifyString(s string) []byte {
	var b strings.Builder
	b.WriteByte('"')
	units := documentationUnits(s)
	for i := 0; i < len(units); i++ {
		u := units[i]
		switch u {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteByte(byte(u))
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if u < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, u)
			} else if u >= 0xd800 && u <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
				b.WriteRune(utf16.DecodeRune(rune(u), rune(units[i+1])))
				i++
			} else if u >= 0xd800 && u <= 0xdfff {
				fmt.Fprintf(&b, `\u%04x`, u)
			} else {
				b.WriteRune(rune(u))
			}
		}
	}
	b.WriteByte('"')
	return []byte(b.String())
}

func stringifyValue(value any) json.RawMessage {
	var b bytes.Buffer
	switch v := value.(type) {
	case *documentationObject:
		b.WriteByte('{')
		for i, key := range documentationKeys(v) {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(stringifyString(key))
			b.WriteByte(':')
			b.Write(stringifyValue(v.values[key]))
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			b.Write(stringifyValue(item))
		}
		b.WriteByte(']')
	case string:
		b.Write(stringifyString(v))
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			b.WriteString("null")
		} else if v == 0 {
			b.WriteByte('0')
		} else {
			encoded, _ := json.Marshal(v)
			b.Write(encoded)
		}
	case documentationMissing:
		b.WriteString("null")
	default:
		encoded, _ := json.Marshal(v)
		b.Write(encoded)
	}
	return b.Bytes()
}
