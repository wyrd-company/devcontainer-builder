package features

import "testing"

func TestCompareOptions(t *testing.T) {
	obj := func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		name string
		a, b any
		want int
	}{
		{"equal strings", "1", "1", 0},
		{"strings", "1", "2", -1},
		{"false before true", false, true, -1},
		{"fewer keys first", obj(), obj("a", "x"), -1},
		{"key names", obj("a", "x"), obj("b", "x"), -1},
		{"string values", obj("a", "x"), obj("a", "y"), -1},
		{"bool values", obj("a", true), obj("a", false), 1},
		// Upstream ignores values that are neither strings nor booleans.
		{"numbers ignored", obj("a", float64(1)), obj("a", float64(2)), 0},
		{"mismatched value types ignored", obj("a", "x"), obj("a", true), 0},
		{"type names", true, "x", -1},        // "boolean" < "string"
		{"object vs string", obj(), "x", -1}, // "object" < "string"
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := compareOptions(c.a, c.b)
			if sign(got) != c.want {
				t.Fatalf("compareOptions(%v, %v) = %d, want sign %d", c.a, c.b, got, c.want)
			}
		})
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
