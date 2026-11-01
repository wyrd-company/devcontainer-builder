// relationships:
//
//	references: [metadata, ../../reference/cli/src/spec-node/templatesCLI/metadata]
package templates

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// metadataValue implements JSON.parse followed by JSON.stringify for validated
// JSON. Keep strings raw so isolated UTF-16 surrogates survive. Object properties
// retain insertion order, with integer indexes first and the last duplicate value.
func metadataValue(data []byte) json.RawMessage {
	p := metadataParser{data: data}
	return p.value()
}

type metadataParser struct {
	data []byte
	at   int
}

func (p *metadataParser) space() {
	for p.at < len(p.data) && strings.ContainsRune(" \t\r\n", rune(p.data[p.at])) {
		p.at++
	}
}
func (p *metadataParser) quoted() []byte {
	start := p.at
	p.at++
	for p.at < len(p.data) {
		c := p.data[p.at]
		p.at++
		if c == '\\' {
			p.at++
		} else if c == '"' {
			break
		}
	}
	return p.data[start:p.at]
}
func (p *metadataParser) value() []byte {
	p.space()
	switch p.data[p.at] {
	case '"':
		return p.quoted()
	case '{':
		p.at++
		p.space()
		type member struct {
			key, value []byte
			index      uint64
			numeric    bool
		}
		var members []member
		seen := map[string]int{}
		for p.data[p.at] != '}' {
			key := p.quoted()
			p.space()
			p.at++
			value := p.value()
			identity := metadataKey(key)
			if i, ok := seen[identity]; ok {
				members[i].value = value
			} else {
				var decoded string
				_ = json.Unmarshal(key, &decoded)
				n, e := strconv.ParseUint(decoded, 10, 32)
				numeric := e == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == decoded
				seen[identity] = len(members)
				members = append(members, member{key, value, n, numeric})
			}
			p.space()
			if p.data[p.at] == ',' {
				p.at++
				p.space()
			} else {
				break
			}
		}
		p.at++
		sort.SliceStable(members, func(i, j int) bool {
			a, b := members[i], members[j]
			if a.numeric != b.numeric {
				return a.numeric
			}
			return a.numeric && a.index < b.index
		})
		var out bytes.Buffer
		out.WriteByte('{')
		for i, m := range members {
			if i > 0 {
				out.WriteByte(',')
			}
			out.Write(m.key)
			out.WriteByte(':')
			out.Write(m.value)
		}
		out.WriteByte('}')
		return out.Bytes()
	case '[':
		p.at++
		p.space()
		var out bytes.Buffer
		out.WriteByte('[')
		for p.data[p.at] != ']' {
			if out.Len() > 1 {
				out.WriteByte(',')
			}
			out.Write(p.value())
			p.space()
			if p.data[p.at] == ',' {
				p.at++
				p.space()
			} else {
				break
			}
		}
		p.at++
		out.WriteByte(']')
		return out.Bytes()
	default:
		start := p.at
		for p.at < len(p.data) && !strings.ContainsRune(" \t\r\n,]}", rune(p.data[p.at])) {
			p.at++
		}
		raw := p.data[start:p.at]
		if raw[0] == 't' || raw[0] == 'f' || raw[0] == 'n' {
			return raw
		}
		number, _ := strconv.ParseFloat(string(raw), 64)
		if math.IsInf(number, 0) || math.IsNaN(number) {
			return []byte("null")
		}
		if number == 0 {
			return []byte("0")
		}
		encoded, _ := json.Marshal(number)
		return encoded
	}
}

// Compare property names as UTF-16 units, including escaped lone surrogates.
func metadataKey(raw []byte) string {
	raw = raw[1 : len(raw)-1]
	var out bytes.Buffer
	unit := func(u uint16) { out.WriteByte(byte(u >> 8)); out.WriteByte(byte(u)) }
	for len(raw) > 0 {
		if raw[0] == '\\' {
			if raw[1] == 'u' {
				n, _ := strconv.ParseUint(string(raw[2:6]), 16, 16)
				unit(uint16(n))
				raw = raw[6:]
				continue
			}
			escapes := map[byte]uint16{'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}
			unit(escapes[raw[1]])
			raw = raw[2:]
			continue
		}
		r, n := utf8.DecodeRune(raw)
		raw = raw[n:]
		for _, u := range utf16.Encode([]rune{r}) {
			unit(u)
		}
	}
	return out.String()
}
