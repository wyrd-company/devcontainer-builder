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
