// relationships:
//
//	references: [value]
package templates

import "testing"

func TestMetadataValue(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{`{"a":9007199254740993,"b":1e400,"c":-0}`, `{"a":9007199254740992,"b":null,"c":0}`},
		{`{"id":"first","id":"last"}`, `{"id":"last"}`},
		{`{"b":0,"2":2,"1":1,"a":3}`, `{"1":1,"2":2,"b":0,"a":3}`},
		{`{"a":{"id":1,"id":2},"b":[1e400,-1e400,1e-400]}`, `{"a":{"id":2},"b":[null,null,0]}`},
		{`{"\ud800":1,"\ud801":2,"\ud800":3}`, `{"\ud800":3,"\ud801":2}`},
		{`{"😀":1,"\ud83d\ude00":2}`, `{"😀":2}`},
		{`{"a":1,"\u0061":2}`, `{"a":2}`},
		{`null`, `null`}, {`[]`, `[]`}, {`{}`, `{}`}, {`"sample"`, `"sample"`},
	} {
		if got := string(metadataValue([]byte(tt.input))); got != tt.want {
			t.Errorf("%s: got %s want %s", tt.input, got, tt.want)
		}
	}
}
