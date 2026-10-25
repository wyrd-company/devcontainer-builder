// relationships:
//   references: [git_config]

package workspace

import "strings"

// gitParseBool follows git_parse_maybe_bool: a key without a value is true,
// an empty value is false, and other values are words or integers.
func gitParseBool(value *string) (bool, bool) {
	if value == nil {
		return true, true
	}
	switch {
	case *value == "":
		return false, true
	case asciiEqualFold(*value, "true"), asciiEqualFold(*value, "yes"), asciiEqualFold(*value, "on"):
		return true, true
	case asciiEqualFold(*value, "false"), asciiEqualFold(*value, "no"), asciiEqualFold(*value, "off"):
		return false, true
	}
	number, ok := gitParseInt(value)
	return number != 0, ok
}

// gitParseInt follows git_parse_int: C strtoimax with base 0, then an optional
// k, m or g unit, within the range of a 32-bit int.
func gitParseInt(value *string) (int, bool) {
	const limit = 1<<31 - 1
	if value == nil || *value == "" {
		return 0, false
	}
	s := *value
	i := 0
	for i < len(s) && strings.IndexByte(" \t\n\v\f\r", s[i]) >= 0 {
		i++
	}
	negative := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		negative = s[i] == '-'
		i++
	}
	base := uint64(10)
	switch {
	case i+2 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') && digitValue(s[i+2]) < 16:
		base = 16
		i += 2
	case i < len(s) && s[i] == '0':
		base = 8
	}
	start := i
	var magnitude uint64
	for ; i < len(s) && digitValue(s[i]) < base; i++ {
		// Any value past 32 bits fails the int range check below.
		if magnitude <= 1<<32 {
			magnitude = magnitude*base + digitValue(s[i])
		}
	}
	if i == start {
		return 0, false
	}
	var factor uint64
	switch unit := s[i:]; {
	case unit == "":
		factor = 1
	case asciiEqualFold(unit, "k"):
		factor = 1 << 10
	case asciiEqualFold(unit, "m"):
		factor = 1 << 20
	case asciiEqualFold(unit, "g"):
		factor = 1 << 30
	default:
		return 0, false
	}
	if negative {
		if magnitude > (limit+1)/factor {
			return 0, false
		}
		return -int(magnitude * factor), true
	}
	if magnitude > limit/factor {
		return 0, false
	}
	return int(magnitude * factor), true
}

func digitValue(c byte) uint64 {
	switch {
	case c >= '0' && c <= '9':
		return uint64(c - '0')
	case c >= 'a' && c <= 'z':
		return uint64(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return uint64(c-'A') + 10
	}
	return 36
}

// asciiEqualFold is C strcasecmp equality. strings.EqualFold also folds
// non-ASCII letters such as U+017F into ASCII ones.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if gitLower(a[i]) != gitLower(b[i]) {
			return false
		}
	}
	return true
}
