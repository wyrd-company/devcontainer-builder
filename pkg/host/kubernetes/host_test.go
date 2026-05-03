package kubernetes

import (
	"regexp"
	"testing"
)

func TestLocalFolderHash_DeterministicAndDistinct(t *testing.T) {
	h1 := LocalFolderHash("/host/proj")
	h2 := LocalFolderHash("/host/proj")
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %q vs %q", h1, h2)
	}
	if other := LocalFolderHash("/host/other"); other == h1 {
		t.Fatalf("distinct paths must hash differently, both got %q", other)
	}
}

func TestLocalFolderHash_EmptyIsEmpty(t *testing.T) {
	if got := LocalFolderHash(""); got != "" {
		t.Errorf("empty path hash = %q, want empty", got)
	}
}

// Kubernetes label values must match (([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?
// and be at most 63 chars. The hash is fed straight into a label
// selector, so it has to satisfy this regex.
func TestLocalFolderHash_ValidLabelValue(t *testing.T) {
	re := regexp.MustCompile(`^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$`)
	for _, p := range []string{"/host/proj", "/", "/path/with spaces/and-dashes", "/c:/winpath"} {
		got := LocalFolderHash(p)
		if len(got) > 63 {
			t.Errorf("%q -> hash %q exceeds 63 chars", p, got)
		}
		if !re.MatchString(got) {
			t.Errorf("%q -> hash %q is not a valid label value", p, got)
		}
	}
}

func TestNew_DefaultsReadyTimeout(t *testing.T) {
	h := New(nil, nil, Options{})
	if h.opts.ReadyTimeout == 0 {
		t.Error("expected ReadyTimeout to default to non-zero")
	}
}

func TestNamespaceOr(t *testing.T) {
	cases := []struct {
		opt, req, want string
	}{
		{"", "", "default"},
		{"team-a", "", "team-a"},
		{"team-a", "override", "override"},
		{"", "override", "override"},
	}
	for _, c := range cases {
		h := New(nil, nil, Options{Namespace: c.opt})
		if got := h.namespaceOr(c.req); got != c.want {
			t.Errorf("namespaceOr(opt=%q, req=%q) = %q, want %q", c.opt, c.req, got, c.want)
		}
	}
}
