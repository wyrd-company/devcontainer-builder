package spec

import (
	"reflect"
	"testing"
)

func TestParseFeatures(t *testing.T) {
	m, err := ParseFeatures([]byte(`{
		// JSONC as upstream accepts
		"example.invalid/one:1": {"flag": true},
		"./local": "latest",
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Keys, []string{"example.invalid/one:1", "./local"}) || string(m.Values["./local"]) != `"latest"` {
		t.Fatalf("parsed %+v", m)
	}
	for _, bad := range []string{`{`, `[]`, `"x"`, `{"a":1} trailing`, ``} {
		if _, err := ParseFeatures([]byte(bad)); err == nil {
			t.Errorf("ParseFeatures(%q) accepted invalid input", bad)
		}
	}
}

// Upstream userFeaturesToArray keeps configuration order, then appends each
// additional feature whose key the configuration does not declare.
func TestWithAdditionalConfigurationWins(t *testing.T) {
	config, _ := ParseFeatures([]byte(`{"b":{"v":"config"},"a":true}`))
	extra, _ := ParseFeatures([]byte(`{"c":{"v":"extra"},"b":{"v":"extra"},"d":"1"}`))
	got := config.WithAdditional(extra)
	if !reflect.DeepEqual(got.Keys, []string{"b", "a", "c", "d"}) {
		t.Fatalf("keys=%v", got.Keys)
	}
	if string(got.Values["b"]) != `{"v":"config"}` || string(got.Values["c"]) != `{"v":"extra"}` {
		t.Fatalf("values=%s %s", got.Values["b"], got.Values["c"])
	}
	if len(config.Keys) != 2 || len(config.Values) != 2 {
		t.Fatalf("receiver modified: %+v", config)
	}
	if empty := (OrderedMap{}).WithAdditional(extra); !reflect.DeepEqual(empty.Keys, extra.Keys) {
		t.Fatalf("empty configuration: %v", empty.Keys)
	}
}
