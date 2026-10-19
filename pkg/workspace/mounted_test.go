package workspace

import "testing"

func TestMountedFolder(t *testing.T) {
	root := Bind{Source: "/src/repo", Destination: "/workspaces/repo"}
	for _, tc := range []struct {
		name  string
		local string
		binds []Bind
		want  string
	}{
		{"Git root", "/src/repo/app", []Bind{root}, "/workspaces/repo/app"},
		{"workspace folder", "/src/repo/app", []Bind{{Source: "/src/repo/app", Destination: "/workspaces/app"}}, "/workspaces/app"},
		{"configured target", "/src/repo/app/", []Bind{{Source: "/src/repo/app", Destination: "/workspace"}}, "/workspace"},
		{"nearest ancestor wins", "/src/repo/app", []Bind{{Source: "/", Destination: "/host"}, root, {Source: "/src/repo/.git", Destination: "/workspaces/repo/.git"}}, "/workspaces/repo/app"},
		{"sibling prefix is not an ancestor", "/src/repository/app", []Bind{root}, ""},
		{"common Git folder only", "/src/trees/topic", []Bind{{Source: "/src/repo/.git", Destination: "/workspaces/repo/.git"}}, ""},
		{"no binds", "/src/repo/app", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MountedFolder(tc.local, tc.binds); got != tc.want {
				t.Fatalf("MountedFolder(%s) = %q, want %q", tc.local, got, tc.want)
			}
		})
	}
}
