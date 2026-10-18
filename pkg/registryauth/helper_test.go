package registryauth

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// scriptHelper puts docker-credential-<name> with body on PATH and
// configures it as the credsStore.
func scriptHelper(t *testing.T, name, body string) *Store {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake credential helpers are shell scripts")
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker-credential-"+name), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return New(writeConfig(t, `{"credsStore":"`+name+`"}`))
}

// captureStderr returns what fn writes to the process's stderr.
func captureStderr(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	func() {
		defer func() { os.Stderr = old }()
		fn()
	}()
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// Helper stderr never reaches the process's stderr, whether the helper
// fails or succeeds.
func TestCredentials_HelperStderrDoesNotReachProcessStderr(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		wantErr      bool
	}{
		{"failing", "#!/bin/sh\nprintf 'synthetic-stderr-marker' >&2\nprintf 'helper failed'\nexit 3\n", true},
		{"succeeding", "#!/bin/sh\nprintf 'synthetic-stderr-marker' >&2\nprintf '{\"Username\":\"testuser\",\"Secret\":\"testpassword\"}'\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := scriptHelper(t, "dcbstderr", tc.script)
			var err error
			out := captureStderr(t, func() { _, err = s.Credentials("registry.example.test") })
			if (err != nil) != tc.wantErr {
				t.Fatalf("Credentials err = %v, want error %v", err, tc.wantErr)
			}
			if bytes.Contains(out, []byte("synthetic-stderr-marker")) {
				t.Fatalf("helper stderr reached process stderr: %q", out)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-stderr-marker") {
				t.Fatalf("error carries helper stderr: %v", err)
			}
		})
	}
}

// Helper stdout is read up to a bound; a larger answer fails without
// credentials and without its content in the error.
func TestCredentials_HelperOutputIsBounded(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"succeeding", "#!/bin/sh\nprintf '{\"Username\":\"testuser\",\"Secret\":\"'\nhead -c 8388608 /dev/zero | tr '\\000' x\nprintf '\"}'\n"},
		{"failing", "#!/bin/sh\nhead -c 8388608 /dev/zero | tr '\\000' x\nexit 1\n"},
		{"endless", "#!/bin/sh\nwhile :; do printf xxxxxxxxxxxxxxxx; done\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := scriptHelper(t, "dcblarge", tc.script)
			c, err := s.Credentials("registry.example.test")
			if err == nil {
				t.Fatalf("Credentials accepted %d bytes of helper output", len(c.Password))
			}
			if !strings.Contains(err.Error(), "exceeds") || strings.Contains(err.Error(), "xxxx") {
				t.Fatalf("error = %.200v, want a size-limit failure without the output", err)
			}
		})
	}
}

// A credsStore that is not a plain helper name is refused before
// anything runs, so it cannot name an executable outside PATH.
func TestCredentials_HelperPathMustResolveOnPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake credential helpers are shell scripts")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	if err := os.Mkdir(filepath.Join(dir, "docker-credential-probe"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside"), []byte("#!/bin/sh\ntouch '"+marker+"'\nprintf '{\"Username\":\"testuser\",\"Secret\":\"testpassword\"}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, `{"credsStore":"probe/../outside"}`)
	t.Chdir(dir)
	t.Setenv("PATH", t.TempDir())
	c, err := New(cfg).Credentials("registry.example.test")
	if err == nil || !strings.Contains(err.Error(), "invalid credential helper name") {
		t.Fatalf("Credentials = %+v, %v; want an invalid helper name error", c, err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("an executable outside PATH ran")
	}
}

func TestCredentials_HelperNamesAreValidated(t *testing.T) {
	for _, name := range []string{"probe/../outside", "../outside", "/bin/sh", "sub/helper", `dir\helper`, "..", ".hidden", "-flag", "has space", "semi;colon"} {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(name)
			if err != nil {
				t.Fatal(err)
			}
			dir := writeConfig(t, `{"credHelpers":{"registry.example.test":`+string(b)+`}}`)
			if _, err := New(dir).Credentials("registry.example.test"); err == nil || !strings.Contains(err.Error(), "invalid credential helper name") {
				t.Fatalf("credHelpers %q: err = %v, want an invalid helper name error", name, err)
			}
		})
	}
	for _, name := range []string{"osxkeychain", "ecr-login", "desktop", "dev-containers-58408eb4-cd1c-4152-b631-62a11a5423c9", "gcloud", "my_helper.v2"} {
		if err := validHelperName(name); err != nil {
			t.Errorf("validHelperName(%q) = %v, want nil", name, err)
		}
	}
}

// A helper in the current directory is not found through PATH lookup.
func TestCredentials_HelperInCurrentDirectoryIsNotRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake credential helpers are shell scripts")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	if err := os.WriteFile(filepath.Join(dir, "docker-credential-dcbcwd"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := writeConfig(t, `{"credsStore":"dcbcwd"}`)
	t.Chdir(dir)
	t.Setenv("PATH", ".")
	if _, err := New(cfg).Credentials("registry.example.test"); err == nil {
		t.Fatal("Credentials ran a helper from the current directory")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a helper in the current directory ran")
	}
}
