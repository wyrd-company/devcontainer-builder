package features

import "testing"

func r(source, id string, after ...string) Resolved {
	return Resolved{
		Source: source,
		Metadata: Metadata{
			ID:            id,
			InstallsAfter: after,
		},
	}
}

func TestOrder_NoOverride_TopoSort(t *testing.T) {
	in := []Resolved{
		r("a", "a"),
		r("b", "b", "a"),
		r("c", "c", "b"),
	}
	out := order(in, nil)
	if got, want := []string{out[0].ID(), out[1].ID(), out[2].ID()},
		[]string{"a", "b", "c"}; !equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestOrder_OverrideTakesPrecedence(t *testing.T) {
	in := []Resolved{
		r("a", "a"),
		r("b", "b"),
		r("c", "c"),
	}
	out := order(in, []string{"c", "a"})
	want := []string{"c", "a", "b"}
	got := []string{out[0].ID(), out[1].ID(), out[2].ID()}
	if !equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestOrder_InstallsAfterUnknownIsIgnored(t *testing.T) {
	in := []Resolved{
		r("a", "a", "missing"),
		r("b", "b"),
	}
	out := order(in, nil)
	if len(out) != 2 {
		t.Fatalf("expected 2 items, got %d", len(out))
	}
}

func TestOrder_CycleDoesNotInfiniteLoop(t *testing.T) {
	in := []Resolved{
		r("a", "a", "b"),
		r("b", "b", "a"),
	}
	out := order(in, nil)
	if len(out) != 2 {
		t.Fatalf("expected 2 items even for cycle, got %d", len(out))
	}
}

func (r Resolved) ID() string { return r.Metadata.ID }

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
