// Package spec contains types and parsers for the devcontainer specification
// as documented at https://containers.dev/implementors/json_reference/.
package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// StripJSONC removes comments and trailing commas from a JSON-with-comments
// document, producing a strictly valid JSON byte slice.
//
// Supported JSONC features:
//   - Line comments (// ...)
//   - Block comments (/* ... */)
//   - Trailing commas in objects and arrays
//
// Strings, including escape sequences, are preserved verbatim.
func StripJSONC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	i := 0
	n := len(in)
	for i < n {
		c := in[i]
		switch {
		case c == '"':
			// Copy string literal as-is, honoring escapes.
			start := i
			i++
			for i < n {
				if in[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if in[i] == '"' {
					i++
					break
				}
				i++
			}
			out = append(out, in[start:i]...)
		case c == '/' && i+1 < n && in[i+1] == '/':
			// Line comment: skip until newline.
			i += 2
			for i < n && in[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && in[i+1] == '*':
			// Block comment: skip until */.
			i += 2
			for i+1 < n && !(in[i] == '*' && in[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			}
		default:
			out = append(out, c)
			i++
		}
	}
	return removeTrailingCommas(out)
}

// removeTrailingCommas walks the (already comment-stripped) byte slice and
// removes commas that immediately precede a closing brace or bracket,
// optionally with whitespace in between.
func removeTrailingCommas(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		c := in[i]
		if c == '"' {
			start := i
			i++
			for i < len(in) {
				if in[i] == '\\' && i+1 < len(in) {
					i += 2
					continue
				}
				if in[i] == '"' {
					break
				}
				i++
			}
			// An unterminated string runs to the end of the input.
			out = append(out, in[start:min(i+1, len(in))]...)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(in) && (in[j] == ' ' || in[j] == '\t' || in[j] == '\n' || in[j] == '\r') {
				j++
			}
			if j < len(in) && (in[j] == '}' || in[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// UnmarshalJSONC parses a JSON-with-comments document into v.
func UnmarshalJSONC(data []byte, v any) error {
	stripped := StripJSONC(data)
	dec := json.NewDecoder(bytes.NewReader(stripped))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode jsonc: %w", err)
	}
	return nil
}
