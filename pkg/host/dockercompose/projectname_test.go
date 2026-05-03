package dockercompose

import (
	"strings"
	"testing"
)

func TestProjectName_StableForSameWorkspace(t *testing.T) {
	a := ProjectName("/home/me/work/proj")
	b := ProjectName("/home/me/work/proj")
	if a != b {
		t.Errorf("ProjectName not stable: %s != %s", a, b)
	}
}

func TestProjectName_DiffersForDifferentWorkspaces(t *testing.T) {
	a := ProjectName("/home/me/work/proj-a")
	b := ProjectName("/home/me/work/proj-b")
	if a == b {
		t.Errorf("ProjectName collided: both = %s", a)
	}
}

func TestProjectName_SanitizesBase(t *testing.T) {
	cases := map[string]string{
		"/tmp/My.Repo":      "myrepo",
		"/tmp/123digits":    "digits",
		"/tmp/hello-world!": "hello-world",
	}
	for in, wantPrefix := range cases {
		got := ProjectName(in)
		if !strings.HasPrefix(got, wantPrefix+"_") {
			t.Errorf("ProjectName(%q) = %q; want prefix %q_", in, got, wantPrefix)
		}
	}
}

func TestProjectName_FallbackBase(t *testing.T) {
	got := ProjectName("/")
	if !strings.HasPrefix(got, "devcontainer_") {
		t.Errorf("ProjectName for unsanitizable base: %q; want devcontainer_-prefixed", got)
	}
}
