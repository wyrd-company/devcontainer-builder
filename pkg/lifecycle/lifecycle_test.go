package lifecycle

import (
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestPlan_OrdersFeaturesBeforeSpecPerHook(t *testing.T) {
	dc := &spec.DevContainer{
		OnCreate:   spec.Command{Shell: "spec-onCreate"},
		PostCreate: spec.Command{Shell: "spec-postCreate"},
	}
	feats := []features.Resolved{
		{Source: "feat-a", Metadata: features.Metadata{
			OnCreateCommand:   spec.Command{Shell: "a-onCreate"},
			PostCreateCommand: spec.Command{Shell: "a-postCreate"},
		}},
		{Source: "feat-b", Metadata: features.Metadata{
			OnCreateCommand: spec.Command{Shell: "b-onCreate"},
		}},
	}
	got := Plan(dc, feats)
	want := []struct {
		hook   Hook
		shell  string
		source string
	}{
		{HookOnCreate, "a-onCreate", "feat-a"},
		{HookOnCreate, "b-onCreate", "feat-b"},
		{HookOnCreate, "spec-onCreate", "devcontainer.json"},
		{HookPostCreate, "a-postCreate", "feat-a"},
		{HookPostCreate, "spec-postCreate", "devcontainer.json"},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d phases, got %d (%+v)", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].Hook != w.hook || got[i].Command.Shell != w.shell || got[i].Origin.Source != w.source {
			t.Errorf("phase %d: got %+v, want hook=%s shell=%s source=%s",
				i, got[i], w.hook, w.shell, w.source)
		}
	}
}

func TestPlan_SkipsEmptyCommands(t *testing.T) {
	dc := &spec.DevContainer{} // no commands at all
	feats := []features.Resolved{{Source: "x", Metadata: features.Metadata{}}}
	if got := Plan(dc, feats); len(got) != 0 {
		t.Errorf("expected empty plan, got %+v", got)
	}
}

func TestPlan_When(t *testing.T) {
	dc := &spec.DevContainer{
		OnCreate:   spec.Command{Shell: "x"},
		PostStart:  spec.Command{Shell: "y"},
		PostAttach: spec.Command{Shell: "z"},
	}
	plan := Plan(dc, nil)
	whens := map[Hook]When{}
	for _, p := range plan {
		whens[p.Hook] = p.When
	}
	if whens[HookOnCreate] != WhenOnce {
		t.Errorf("onCreate expected WhenOnce, got %v", whens[HookOnCreate])
	}
	if whens[HookPostStart] != WhenStart {
		t.Errorf("postStart expected WhenStart, got %v", whens[HookPostStart])
	}
	if whens[HookPostAttach] != WhenAttach {
		t.Errorf("postAttach expected WhenAttach, got %v", whens[HookPostAttach])
	}
}

func TestMarkerPath(t *testing.T) {
	udf := UserDataFolder("/home/vscode", "")
	if udf != "/home/vscode/.devcontainer" {
		t.Fatalf("default user data folder: got %q", udf)
	}
	cases := []struct {
		when     When
		hook     Hook
		expected string
	}{
		{WhenOnce, HookOnCreate, "/home/vscode/.devcontainer/.onCreateCommandMarker"},
		{WhenOnce, HookPostCreate, "/home/vscode/.devcontainer/.postCreateCommandMarker"},
		{WhenStart, HookPostStart, "/home/vscode/.devcontainer/.postStartCommandMarker"},
		{WhenAttach, HookPostAttach, ""},
	}
	for _, c := range cases {
		p := Phase{Hook: c.hook, When: c.when}
		if got := p.MarkerPath(udf); got != c.expected {
			t.Errorf("MarkerPath(%s,%s) = %q, want %q", c.when, c.hook, got, c.expected)
		}
	}
}

func TestUserDataFolder_Override(t *testing.T) {
	if got := UserDataFolder("/home/u", "custom"); got != "/home/u/custom" {
		t.Errorf("relative override should join with home, got %q", got)
	}
	if got := UserDataFolder("/home/u", "/abs/path"); got != "/abs/path" {
		t.Errorf("absolute override should be returned as-is, got %q", got)
	}
}

func TestPlan_NilSpec(t *testing.T) {
	if Plan(nil, nil) != nil {
		t.Error("nil spec should produce nil plan")
	}
}
