package docker

import (
	"strings"
	"testing"
)

func TestIsAllDigits(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", true},
		{"1000", true},
		{"vscode", false},
		{"u1000", false},
		{"1000a", false},
		{"-1", false},
	}
	for _, c := range cases {
		if got := isAllDigits(c.in); got != c.want {
			t.Errorf("isAllDigits(%q) = %v want %v", c.in, got, c.want)
		}
	}
}

// TestApplyUIDUpdate_SkipsNonHumanRemoteUser exercises the early-return
// branch — applyUIDUpdate must return nil without any docker calls
// when remoteUser is empty, "root", or numeric. The Host's nil docker
// client would panic if execution reached the exec path.
func TestApplyUIDUpdate_SkipsNonHumanRemoteUser(t *testing.T) {
	h := &Host{}
	upd := RemoteUserUIDUpdate{HostUID: 1000, HostGID: 1000}
	for _, ru := range []string{"", "root", "0", "1000"} {
		if err := h.applyUIDUpdate(nil, "", ru, upd); err != nil {
			t.Errorf("remoteUser=%q: expected nil, got %v", ru, err)
		}
	}
}

// TestUIDUpdateScript_Shape locks in the structural elements of the
// runtime script so changes that drift from upstream's
// scripts/updateUID.Dockerfile are caught here.
func TestUIDUpdateScript_Shape(t *testing.T) {
	for _, want := range []string{
		`REMOTE_USER=`,
		`NEW_UID=`,
		`NEW_GID=`,
		`/etc/passwd`,
		`/etc/group`,
		`OLD_UID=`,
		`OLD_GID=`,
		`HOME_FOLDER=`,
		`EXISTING_USER=`,
		`EXISTING_GROUP=`,
		`chown -R`,
	} {
		if !strings.Contains(uidUpdateScript, want) {
			t.Errorf("uidUpdateScript missing %q", want)
		}
	}
}
