// relationships:
//
// references: [jsonc_value, documentation_jsonc]
package collectiondocs

import "testing"

func TestStringifyJSONCValuePreservesStringCodeUnits(t *testing.T) {
	input := `{"s":"\\u003c\u003c\u2028\ud800\udc00\ud800","\udfff":"\u0026","n":-0,"2":true,"1":null}`
	want := "{\"1\":null,\"2\":true,\"s\":\"\\\\u003c<\u2028𐀀\\ud800\",\"\\udfff\":\"&\",\"n\":0}"
	if got := string(StringifyJSONCValue([]byte(input))); got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestStringifyJSONValuePreservesOwnPrototypeProperty(t *testing.T) {
	input := []byte(`{"custom":{"__proto__":{"sample":true},"value":1}}`)
	if got := string(StringifyJSONValue(input)); got != string(input) {
		t.Fatalf("lost own property: %s", got)
	}
	if got := string(StringifyJSONCValue(input)); got != `{"custom":{"value":1}}` {
		t.Fatalf("JSONC metadata semantics changed: %s", got)
	}
}
