package spec

import (
	"strings"
	"testing"
)

func TestStripJSONC_LineComments(t *testing.T) {
	in := []byte(`{
  // this is a comment
  "name": "demo", // trailing
  "image": "alpine"
}`)
	got := string(StripJSONC(in))
	if strings.Contains(got, "comment") {
		t.Fatalf("comment leaked: %s", got)
	}
	if !strings.Contains(got, `"name": "demo"`) {
		t.Fatalf("missing name field: %s", got)
	}
}

func TestStripJSONC_BlockComments(t *testing.T) {
	in := []byte(`{
  /* block
     comment */
  "image": "alpine" /* inline */
}`)
	got := string(StripJSONC(in))
	if strings.Contains(got, "block") || strings.Contains(got, "inline") {
		t.Fatalf("block comment leaked: %s", got)
	}
}

func TestStripJSONC_PreservesStrings(t *testing.T) {
	in := []byte(`{"x": "a // b /* c */"}`)
	got := string(StripJSONC(in))
	want := `{"x": "a // b /* c */"}`
	if got != want {
		t.Fatalf("strings altered: got %q want %q", got, want)
	}
}

func TestStripJSONC_TrailingCommas(t *testing.T) {
	in := []byte(`{
  "list": [1, 2, 3,],
  "x": "y",
}`)
	got := string(StripJSONC(in))
	if strings.Contains(got, ",]") || strings.Contains(got, ",}") {
		t.Fatalf("trailing comma left: %s", got)
	}
}

func TestStripJSONC_StringWithEscapedQuote(t *testing.T) {
	in := []byte(`{"x": "a\"b // c"}`)
	got := string(StripJSONC(in))
	if got != `{"x": "a\"b // c"}` {
		t.Fatalf("escaped quote string altered: %s", got)
	}
}
