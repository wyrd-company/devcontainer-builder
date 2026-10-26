// relationships:
//   references: [git_config]

package workspace

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// gitConfigEntry is one config variable. The name has Git's lower-case section
// and key, with the subsection unchanged. A nil value is a key without "=".
type gitConfigEntry struct {
	name  string
	value *string
}

// gitConfigSource follows get_next_char in Git's config.c: CRLF reads as LF,
// and the end of the input reads as LF with eof set. Git reads a config file
// with getc, so a read error also ends the input.
type gitConfigSource struct {
	reader *bufio.Reader
	eof    bool
}

func (s *gitConfigSource) next() byte {
	c, err := s.reader.ReadByte()
	if err != nil {
		s.eof = true
		return '\n'
	}
	if c == '\r' {
		if next, err := s.reader.Peek(1); err == nil && next[0] == '\n' {
			s.reader.ReadByte()
			return '\n'
		}
	}
	return c
}

// parseGitConfig parses config data and returns its entries.
func parseGitConfig(data []byte) ([]gitConfigEntry, bool) {
	var entries []gitConfigEntry
	if !parseGitConfigReader(bytes.NewReader(data), func(entry gitConfigEntry) {
		entries = append(entries, entry)
	}) {
		return nil, false
	}
	return entries, true
}

// parseGitConfigReader follows git_parse_source in Git's config.c, reading
// input as it parses, as Git does, so it stops at the first syntax error
// without an input size limit. Git uses its own ASCII character classes, and C
// strings for names and values. It reports false for a syntax error, which Git
// treats as fatal for a config file, and gives each entry to entry when it is
// not nil.
// Reference: https://github.com/git/git/blob/master/config.c
func parseGitConfigReader(input io.Reader, entry func(gitConfigEntry)) bool {
	const bom = "\xef\xbb\xbf"
	source := &gitConfigSource{reader: bufio.NewReader(input)}
	if first, err := source.reader.Peek(1); err == nil && first[0] == bom[0] {
		// Git skips a complete UTF-8 byte order mark and rejects a partial one.
		if prefix, _ := source.reader.Peek(len(bom)); string(prefix) != bom {
			return false
		}
		source.reader.Discard(len(bom))
	}
	var name []byte
	section := 0
	comment := false
	for {
		c := source.next()
		switch {
		case c == '\n':
			if source.eof {
				return true
			}
			comment = false
		case comment, gitSpace(c):
		case c == '#' || c == ';':
			comment = true
		case c == '[':
			name = name[:0]
			if !source.section(&name) || len(name) < 1 {
				return false
			}
			name = append(name, '.')
			section = len(name)
		case gitAlpha(c):
			name = append(name[:section], gitLower(c))
			key, ok := source.entry(name)
			if !ok {
				return false
			}
			name = key.key
			if entry != nil {
				entry(gitConfigEntry{name: cString(string(name)), value: key.value})
			}
		default:
			return false
		}
	}
}

// section follows get_base_var and get_extended_base_var.
func (s *gitConfigSource) section(name *[]byte) bool {
	for {
		c := s.next()
		if s.eof {
			return false
		}
		if c == ']' {
			return true
		}
		if gitSpace(c) {
			return s.subsection(name, c)
		}
		if !gitKeyChar(c) && c != '.' {
			return false
		}
		*name = append(*name, gitLower(c))
	}
}

func (s *gitConfigSource) subsection(name *[]byte, c byte) bool {
	for {
		if c == '\n' {
			return false
		}
		c = s.next()
		if !gitSpace(c) {
			break
		}
	}
	if c != '"' {
		return false
	}
	*name = append(*name, '.')
	for {
		c := s.next()
		if c == '\n' {
			return false
		}
		if c == '"' {
			break
		}
		if c == '\\' {
			c = s.next()
			if c == '\n' {
				return false
			}
		}
		*name = append(*name, c)
	}
	return s.next() == ']'
}

type gitConfigKey struct {
	key   []byte
	value *string
}

// entry follows get_value: the rest of the key name, then an optional value.
func (s *gitConfigSource) entry(name []byte) (gitConfigKey, bool) {
	var c byte
	for {
		c = s.next()
		if s.eof || !gitKeyChar(c) {
			break
		}
		name = append(name, gitLower(c))
	}
	for c == ' ' || c == '\t' {
		c = s.next()
	}
	if c == '\n' {
		return gitConfigKey{key: name}, true
	}
	if c != '=' {
		return gitConfigKey{}, false
	}
	value, ok := s.value()
	if !ok {
		return gitConfigKey{}, false
	}
	value = cString(value)
	return gitConfigKey{key: name, value: &value}, true
}

// value follows parse_value: unquoted whitespace is trimmed at both ends,
// comments end the value, and only \t, \b, \n, \\, \" and a line continuation
// are valid escapes.
func (s *gitConfigSource) value() (string, bool) {
	var value []byte
	quote, comment := false, false
	trim := 0
	for {
		c := s.next()
		if c == '\n' {
			if quote {
				return "", false
			}
			if trim > 0 {
				value = value[:trim]
			}
			return string(value), true
		}
		if comment {
			continue
		}
		if gitSpace(c) && !quote {
			if trim == 0 {
				trim = len(value)
			}
			if len(value) > 0 {
				value = append(value, c)
			}
			continue
		}
		if !quote && (c == ';' || c == '#') {
			comment = true
			continue
		}
		trim = 0
		switch c {
		case '\\':
			switch c = s.next(); c {
			case '\n':
				continue
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'n':
				c = '\n'
			case '\\', '"':
			default:
				return "", false
			}
			value = append(value, c)
		case '"':
			quote = !quote
		default:
			value = append(value, c)
		}
	}
}

// cString keeps the part of a Git name or value before its first NUL byte.
func cString(s string) string {
	if end := strings.IndexByte(s, 0); end >= 0 {
		return s[:end]
	}
	return s
}

// Git's sane_ctype classes: whitespace is space, tab, LF and CR, and letters
// are ASCII only.
func gitSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func gitAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func gitKeyChar(c byte) bool { return gitAlpha(c) || c >= '0' && c <= '9' || c == '-' }

func gitLower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
