// relationships:
//
// references: [documentation, ../../reference/cli/src/spec-node/collectionCommonUtils/generateDocsCommandImpl]
package features

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type documentationMissing struct{}

var documentationUndefined = documentationMissing{}

type documentationObject struct {
	keys   []string
	values map[string]any
	proto  any
}

// The upstream docs reader discards jsonc-parser's error list. Retain values
// recovered before/after syntax errors, rather than imposing strict JSON admission.
// This reader follows that parser's object/array separator recovery, scoped to
// documentation; configuration and packaging keep their existing strict readers.
func documentationJSON(data []byte) any {
	p := documentationParser{data: string([]rune(string(data)))}
	p.next()
	value, _ := p.value()
	return value
}

type documentationParser struct {
	data    string
	at      int
	token   byte
	literal any
}

func (p *documentationParser) next() {
	p.token = 0
	for p.at < len(p.data) {
		ch := p.data[p.at]
		p.at++
		switch ch {
		case ' ', '\t', '\r', '\n':
			continue
		case '/':
			if p.at < len(p.data) && p.data[p.at] == '/' {
				for p.at < len(p.data) && p.data[p.at] != '\r' && p.data[p.at] != '\n' {
					p.at++
				}
				continue
			}
			if p.at < len(p.data) && p.data[p.at] == '*' {
				p.at++
				end := strings.Index(p.data[p.at:], "*/")
				if end < 0 {
					p.at = len(p.data)
				} else {
					p.at += end + 2
				}
				continue
			}
		case '{', '}', '[', ']', ',', ':':
			p.token = ch
			return
		case '"':
			p.token = 's'
			p.literal = p.str()
			return
		default:
			start := p.at - 1
			if ch == '-' || ch >= '0' && ch <= '9' {
				// JSON scanner numbers keep the valid prefix when a fractional/exponent
				// suffix is incomplete, and do not consume adjacent symbols.
				if ch == '-' && (p.at == len(p.data) || p.data[p.at] < '0' || p.data[p.at] > '9') {
					continue
				}
				if ch == '-' {
					ch = p.data[p.at]
					p.at++
				}
				if ch != '0' {
					for p.at < len(p.data) && p.data[p.at] >= '0' && p.data[p.at] <= '9' {
						p.at++
					}
				}
				end := p.at
				if p.at < len(p.data) && p.data[p.at] == '.' {
					p.at++
					digits := p.at
					for p.at < len(p.data) && p.data[p.at] >= '0' && p.data[p.at] <= '9' {
						p.at++
					}
					if p.at > digits {
						end = p.at
					}
				}
				if p.at < len(p.data) && (p.data[p.at] == 'e' || p.data[p.at] == 'E') {
					p.at++
					if p.at < len(p.data) && (p.data[p.at] == '+' || p.data[p.at] == '-') {
						p.at++
					}
					digits := p.at
					for p.at < len(p.data) && p.data[p.at] >= '0' && p.data[p.at] <= '9' {
						p.at++
					}
					if p.at > digits {
						end = p.at
					}
				}
				f, _ := strconv.ParseFloat(p.data[start:end], 64)
				p.token = 'v'
				p.literal = f
				return
			}
			// Unknown identifiers are skipped by jsonc-parser's scanNext, rather than
			// becoming property names or values.
			for p.at < len(p.data) && !strings.ContainsRune("{}[],: \t\r\n\"/", rune(p.data[p.at])) {
				p.at++
			}
			word := p.data[start:p.at]
			switch word {
			case "true":
				p.literal = true
			case "false":
				p.literal = false
			case "null":
				p.literal = nil
			default:
				continue
			}
			p.token = 'v'
			return
		}
	}
}
func (p *documentationParser) str() string {
	var b strings.Builder
	for p.at < len(p.data) {
		ch := p.data[p.at]
		p.at++
		if ch == '"' {
			break
		}
		if ch == '\r' || ch == '\n' {
			p.at--
			break
		}
		if ch != '\\' {
			b.WriteByte(ch)
			continue
		}
		if p.at == len(p.data) {
			break
		}
		ch = p.data[p.at]
		p.at++
		switch ch {
		case '"', '\\', '/':
			b.WriteByte(ch)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			start := p.at
			for p.at < len(p.data) && p.at < start+4 && strings.ContainsRune("0123456789abcdefABCDEF", rune(p.data[p.at])) {
				p.at++
			}
			if p.at-start == 4 {
				n, _ := strconv.ParseUint(p.data[start:p.at], 16, 16)
				r := rune(n)
				if r >= 0xd800 && r <= 0xdbff && p.at+6 <= len(p.data) && p.data[p.at:p.at+2] == "\\u" {
					low, err := strconv.ParseUint(p.data[p.at+2:p.at+6], 16, 16)
					if err == nil && low >= 0xdc00 && low <= 0xdfff {
						r = utf16.DecodeRune(r, rune(low))
						p.at += 6
					}
				}
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
func (p *documentationParser) recover(end byte) {
	for p.token != 0 && p.token != end && p.token != ',' {
		p.next()
	}
}
func (p *documentationParser) value() (any, bool) {
	switch p.token {
	case 's', 'v':
		v := p.literal
		p.next()
		return v, true
	case '{':
		o := &documentationObject{values: map[string]any{}}
		p.next()
		for p.token != 0 && p.token != '}' {
			if p.token == ',' {
				p.next()
			}
			if p.token != 's' {
				p.recover('}')
				continue
			}
			key := p.literal.(string)
			p.next()
			if p.token != ':' {
				p.recover('}')
				continue
			}
			p.next()
			v, ok := p.value()
			if !ok {
				p.recover('}')
				continue
			}
			if key == "__proto__" {
				if _, ok := v.(*documentationObject); ok || v == nil {
					o.proto = v
				}
				continue
			}
			if _, exists := o.values[key]; !exists {
				o.keys = append(o.keys, key)
			}
			o.values[key] = v
		}
		if p.token == '}' {
			p.next()
		}
		return o, true
	case '[':
		a := []any{}
		p.next()
		for p.token != 0 && p.token != ']' {
			if p.token == ',' {
				p.next()
			}
			v, ok := p.value()
			if !ok {
				p.recover(']')
				continue
			}
			a = append(a, v)
		}
		if p.token == ']' {
			p.next()
		}
		return a, true
	}
	return documentationUndefined, false
}
func documentationGet(value any, key string) any {
	switch v := value.(type) {
	case *documentationObject:
		if x, ok := v.values[key]; ok {
			return x
		}
		if v.proto != nil {
			return documentationGet(v.proto, key)
		}
	case []any:
		if key == "length" {
			return float64(len(v))
		}
		if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(v) {
			return v[i]
		}
	case string:
		if key == "length" {
			return float64(len(utf16.Encode([]rune(v))))
		}
	}
	return documentationUndefined
}
func documentationKeys(value any) []string {
	keys := []string{}
	switch v := value.(type) {
	case *documentationObject:
		keys = append(keys, v.keys...)
	case []any:
		for i := range v {
			keys = append(keys, strconv.Itoa(i))
		}
	case string:
		for i := 0; i < len(utf16.Encode([]rune(v))); i++ {
			keys = append(keys, strconv.Itoa(i))
		}
	}
	// ECMAScript enumerates canonical uint32 array-index keys before other keys.
	index := func(s string) (uint64, bool) {
		n, e := strconv.ParseUint(s, 10, 32)
		return n, e == nil && n < math.MaxUint32 && strconv.FormatUint(n, 10) == s
	}
	sort.SliceStable(keys, func(i, j int) bool {
		a, ai := index(keys[i])
		b, bi := index(keys[j])
		if ai != bi {
			return ai
		}
		return ai && a < b
	})
	return keys
}
func documentationTruthy(value any) bool {
	switch v := value.(type) {
	case documentationMissing:
		return false
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0 && !math.IsNaN(v)
	}
	return true
}
func documentationLength(value any) int {
	switch v := value.(type) {
	case []any:
		return len(v)
	case string:
		return utf8.RuneCountInString(v)
	}
	return 0
}
func documentationString(value any) string {
	switch v := value.(type) {
	case documentationMissing:
		return "undefined"
	case nil:
		return "null"
	case string:
		return v
	case *documentationObject:
		return "[object Object]"
	case []any:
		parts := make([]string, len(v))
		for i, x := range v {
			if x != nil && x != documentationUndefined {
				parts[i] = documentationString(x)
			}
		}
		return strings.Join(parts, ",")
	case float64:
		if math.IsInf(v, 1) {
			return "Infinity"
		}
		if math.IsInf(v, -1) {
			return "-Infinity"
		}
		if math.IsNaN(v) {
			return "NaN"
		}
		if v == 0 {
			return "0"
		}
		data, _ := json.Marshal(v)
		return string(data)
	default:
		return fmt.Sprint(v)
	}
}
