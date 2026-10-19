// relationships:
//
//	references: [lockfile, ../../reference/cli/src/spec-configuration/lockfile]
package features

import (
	"bytes"
	"encoding/json"
	"fmt"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// MarshalJSON keeps upstream's localeCompare order instead of Go's byte order.
// The CLI's default English collation sorts lowercase before uppercase.
func (l Lockfile) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(l.Features))
	for key := range l.Features {
		keys = append(keys, key)
	}
	collate.New(language.English).SortStrings(keys)
	var out bytes.Buffer
	out.WriteString(`{"features":{`)
	for i, key := range keys {
		if i != 0 {
			out.WriteByte(',')
		}
		if err := encodeJSON(&out, key); err != nil {
			return nil, err
		}
		out.WriteByte(':')
		if err := encodeJSON(&out, l.Features[key]); err != nil {
			return nil, err
		}
	}
	out.WriteString("}}")
	return out.Bytes(), nil
}

func encodeJSON(out *bytes.Buffer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

// Normalize as JSON.parse -> JSON.stringify does: decode string escapes and
// numbers, remove whitespace, and preserve member order. Lockfile fields are
// strings and arrays, but retaining unknown fields makes frozen comparison fail
// on extra data as upstream does.
func normalizeLockfileJSON(data []byte) []byte {
	d := json.NewDecoder(bytes.NewReader(data))
	var out bytes.Buffer
	if err := normalizeJSONValue(d, &out); err != nil {
		return nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, out.Bytes()); err != nil {
		return nil
	}
	return compact.Bytes()
}

func normalizeJSONValue(d *json.Decoder, out *bytes.Buffer) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	opening, container := token.(json.Delim)
	if !container {
		return encodeJSON(out, token)
	}
	if opening != '{' && opening != '[' {
		return fmt.Errorf("unexpected JSON delimiter")
	}
	out.WriteRune(rune(opening))
	for i := 0; d.More(); i++ {
		if i != 0 {
			out.WriteByte(',')
		}
		if opening == '{' {
			if err := normalizeJSONValue(d, out); err != nil {
				return err
			}
			out.WriteByte(':')
		}
		if err := normalizeJSONValue(d, out); err != nil {
			return err
		}
	}
	closing, err := d.Token()
	if err != nil {
		return err
	}
	out.WriteRune(rune(closing.(json.Delim)))
	return nil
}
