// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
)

// secretMaskText replaces secret values, as upstream masks its log.
const secretMaskText = "********"

// secretMask replaces secret values in stderr and in the result. It masks
// the raw value and its Go-quoted and JSON-escaped spellings, so that a
// value quoted into an error is masked too.
type secretMask struct{ spellings [][]byte }

// newSecretMask returns nil when no value is nonempty. An empty value is
// not masked: it would match between every byte.
func newSecretMask(secrets map[string]string) *secretMask {
	seen := map[string]bool{}
	var spellings []string
	for _, value := range secrets {
		for _, s := range secretSpellings(value) {
			if s != "" && !seen[s] {
				seen[s] = true
				spellings = append(spellings, s)
			}
		}
	}
	if len(spellings) == 0 {
		return nil
	}
	// Longest first, so that a value containing another is masked whole.
	sort.Slice(spellings, func(i, j int) bool {
		if len(spellings[i]) != len(spellings[j]) {
			return len(spellings[i]) > len(spellings[j])
		}
		return spellings[i] < spellings[j]
	})
	m := &secretMask{}
	for _, s := range spellings {
		m.spellings = append(m.spellings, []byte(s))
	}
	return m
}

func secretSpellings(value string) []string {
	quoted := strconv.Quote(value)
	encoded, _ := json.Marshal(value)
	return []string{value, quoted[1 : len(quoted)-1], string(encoded[1 : len(encoded)-1])}
}

// String masks a complete text.
func (m *secretMask) String(s string) string {
	if m == nil {
		return s
	}
	out, _ := m.scan([]byte(s), true)
	return string(out)
}

// scan masks b. Unless final, it holds back a tail that a later write could
// complete into a secret, so that a value split across writes is masked.
func (m *secretMask) scan(b []byte, final bool) (out, held []byte) {
	out = make([]byte, 0, len(b))
	for i := 0; i < len(b); {
		rest := b[i:]
		if !final && m.couldComplete(rest) {
			return out, append([]byte(nil), rest...)
		}
		if n := m.match(rest); n > 0 {
			out = append(out, secretMaskText...)
			i += n
			continue
		}
		out = append(out, b[i])
		i++
	}
	return out, nil
}

func (m *secretMask) match(rest []byte) int {
	for _, s := range m.spellings {
		if bytes.HasPrefix(rest, s) {
			return len(s)
		}
	}
	return 0
}

// couldComplete reports whether rest is a proper prefix of a spelling.
func (m *secretMask) couldComplete(rest []byte) bool {
	for _, s := range m.spellings {
		if len(s) > len(rest) && bytes.HasPrefix(s, rest) {
			return true
		}
	}
	return false
}

// result masks every string in a result object. It fails closed: a result
// that cannot be re-read is replaced by an error without its content.
func (m *secretMask) result(result map[string]any) map[string]any {
	if m == nil {
		return result
	}
	data, err := json.Marshal(result)
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var generic map[string]any
		if err = dec.Decode(&generic); err == nil {
			return m.value(generic).(map[string]any)
		}
	}
	return map[string]any{"outcome": "error", "message": "dcb could not mask secret values in the result", "description": "dcb could not complete the requested command"}
}

func (m *secretMask) value(v any) any {
	switch v := v.(type) {
	case string:
		return m.String(v)
	case []any:
		for i := range v {
			v[i] = m.value(v[i])
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[m.String(k)] = m.value(item)
		}
		return out
	}
	return v
}

// maskSecrets adds the values to the mask of stderr and the result.
func (a *application) maskSecrets(secrets map[string]string) {
	if len(secrets) == 0 {
		return
	}
	if a.secretValues == nil {
		a.secretValues = map[string]string{}
	}
	for _, value := range secrets {
		a.secretValues[value] = value
	}
	a.mask = newSecretMask(a.secretValues)
	if a.logs != nil {
		a.logs.setMask(a.mask)
	}
}
