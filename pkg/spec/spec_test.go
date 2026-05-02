package spec

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUnmarshal_AppPortVariants(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want any
	}{
		{"scalar number", `{"appPort": 3000}`, float64(3000)},
		{"scalar string", `{"appPort": "3000"}`, "3000"},
		{"array", `{"appPort": [3000, "4000"]}`, []any{float64(3000), "4000"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dc DevContainer
			if err := json.Unmarshal([]byte(c.in), &dc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(dc.AppPort, c.want) {
				t.Errorf("appPort: got %#v want %#v", dc.AppPort, c.want)
			}
		})
	}
}

func TestUnmarshal_UpdateRemoteUserUID(t *testing.T) {
	cases := []struct {
		in   string
		want *bool
	}{
		{`{"updateRemoteUserUID": true}`, ptrBool(true)},
		{`{"updateRemoteUserUID": false}`, ptrBool(false)},
		{`{}`, nil},
	}
	for _, c := range cases {
		var dc DevContainer
		if err := json.Unmarshal([]byte(c.in), &dc); err != nil {
			t.Fatalf("unmarshal %q: %v", c.in, err)
		}
		switch {
		case c.want == nil && dc.UpdateRemoteUserUID != nil:
			t.Errorf("%q: want nil, got %v", c.in, *dc.UpdateRemoteUserUID)
		case c.want != nil && dc.UpdateRemoteUserUID == nil:
			t.Errorf("%q: want %v, got nil", c.in, *c.want)
		case c.want != nil && *c.want != *dc.UpdateRemoteUserUID:
			t.Errorf("%q: want %v, got %v", c.in, *c.want, *dc.UpdateRemoteUserUID)
		}
	}
}

func TestUnmarshal_ForwardPorts(t *testing.T) {
	var dc DevContainer
	if err := json.Unmarshal([]byte(`{"forwardPorts": [3000, "8080:80"]}`), &dc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []any{float64(3000), "8080:80"}
	if !reflect.DeepEqual(dc.ForwardPorts, want) {
		t.Errorf("forwardPorts: got %#v want %#v", dc.ForwardPorts, want)
	}
}

func ptrBool(b bool) *bool { return &b }
