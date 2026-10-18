package features

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestResolve_LocalFeatureRelativeToSpecDir(t *testing.T) {
	root := t.TempDir()
	specDir := filepath.Join(root, ".devcontainer")
	featDir := filepath.Join(specDir, "features", "demo")
	if err := os.MkdirAll(featDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featDir, "devcontainer-feature.json"), []byte(`{"id":"demo","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(featDir, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	other := filepath.Join(root, "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}

	om := spec.OrderedMap{Keys: []string{"./features/demo"}, Values: map[string]json.RawMessage{"./features/demo": []byte(`{}`)}}
	got, err := New().Resolve(om, nil, filepath.Join(root, "stage"), specDir)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].Metadata.ID != "demo" {
		t.Fatalf("resolved = %+v", got)
	}
}

func TestLocalSourcePath_FileURL(t *testing.T) {
	dir := t.TempDir()
	got, err := localSourcePath("file://"+dir, "")
	if err != nil {
		t.Fatalf("localSourcePath: %v", err)
	}
	if got != dir {
		t.Fatalf("got %q want %q", got, dir)
	}
	if _, err := localSourcePath("file://example.com/tmp/feature", ""); err == nil {
		t.Fatal("expected non-local file URL host to be rejected")
	}
}

func TestExtractTarMaybeGz_RejectsSymlink(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "escape", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	err := extractTarMaybeGz(buf.Bytes(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "symlinks are not supported") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func tarGz(t *testing.T, entries []tar.Header, bodies map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		body := bodies[h.Name]
		h.Size = int64(len(body))
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := tw.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Published features (e.g. ghcr.io/devcontainers/features/*) are packed
// with a leading "./" directory entry.
func TestExtractTarMaybeGz_AcceptsCurrentDirEntry(t *testing.T) {
	data := tarGz(t, []tar.Header{
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: ".", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./install.sh", Typeflag: tar.TypeReg, Mode: 0o755},
		{Name: "./scripts/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./scripts/helper.sh", Typeflag: tar.TypeReg, Mode: 0o644},
	}, map[string]string{"./install.sh": "#!/bin/sh\n", "./scripts/helper.sh": "echo ok\n"})
	dir := t.TempDir()
	if err := extractTarMaybeGz(data, dir); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, p := range []string{"install.sh", "scripts/helper.sh"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
}

func TestExtractTarMaybeGz_RejectsEscapes(t *testing.T) {
	for _, name := range []string{"../escape", "./../escape", "a/../../escape", "/abs/escape"} {
		t.Run(name, func(t *testing.T) {
			data := tarGz(t, []tar.Header{{Name: name, Typeflag: tar.TypeReg, Mode: 0o644}}, map[string]string{name: "x"})
			err := extractTarMaybeGz(data, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "unsafe path") {
				t.Fatalf("expected unsafe path rejection, got %v", err)
			}
		})
	}
}

func TestExtractTarMaybeGz_AcceptsDotDotPrefixedName(t *testing.T) {
	data := tarGz(t, []tar.Header{{Name: "..hidden", Typeflag: tar.TypeReg, Mode: 0o644}}, map[string]string{"..hidden": "x"})
	dir := t.TempDir()
	if err := extractTarMaybeGz(data, dir); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..hidden")); err != nil {
		t.Fatal(err)
	}
}
