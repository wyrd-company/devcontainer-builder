// relationships:
//
//	references: [package, ../../reference/cli/src/spec-node/collectionCommonUtils/packageCommandImpl]
package features

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func packageFixture(t *testing.T, dir, metadata string, install bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	if install {
		if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o751); err != nil {
			t.Fatal(err)
		}
	}
}

const packageMetadata = `{"id":"sample","version":"1.2.3","name":"Sample tool"}`

type packageMember struct {
	header tar.Header
	data   []byte
}

func packageMembers(t *testing.T, path string) map[string]packageMember {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		t.Fatal("upstream writes plain tar, despite the tgz suffix")
	}
	tr := tar.NewReader(bytes.NewReader(data))
	members := map[string]packageMember{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		members[strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")] = packageMember{*h, data}
	}
	return members
}

func TestPackageSingle(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged-metadata", true: "legacy-current-id"}[legacy], func(t *testing.T) {
			source, out := filepath.Join(t.TempDir(), "input"), filepath.Join(t.TempDir(), "output")
			metadata := "// original comment\n" + strings.TrimSuffix(packageMetadata, "}") + `,"custom":{"opaque":[1,true]}`
			if legacy {
				metadata += `,"legacyIds":["old-sample"],"currentId":"stale"`
			}
			metadata += "}"
			packageFixture(t, source, metadata, true)
			payload := []byte{0, 255, 10, 42}
			if err := os.WriteFile(filepath.Join(source, ".payload"), payload, 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(".payload", filepath.Join(source, "alias")); err != nil {
				t.Fatal(err)
			}
			collection, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: out})
			if err != nil {
				t.Fatal(err)
			}
			if collection.SourceInformation.Source != "devcontainer-cli" || len(collection.Features) != 1 {
				t.Fatalf("collection = %+v", collection)
			}
			members := packageMembers(t, filepath.Join(out, "devcontainer-feature-sample.tgz"))
			if !bytes.Equal(members[".payload"].data, payload) || members[".payload"].header.Mode != 0o640 {
				t.Fatal("payload bytes or mode changed")
			}
			if members["alias"].header.Typeflag != tar.TypeSymlink || members["alias"].header.Linkname != ".payload" {
				t.Fatal("payload symlink was not preserved")
			}
			if members["install.sh"].header.Mode != 0o751 {
				t.Fatal("install script lost its mode")
			}
			var archived, listed map[string]any
			if err := json.Unmarshal(collection.Features[0], &listed); err != nil {
				t.Fatal(err)
			}
			if legacy {
				if err := json.Unmarshal(members["devcontainer-feature.json"].data, &archived); err != nil {
					t.Fatal(err)
				}
				if archived["currentId"] != "sample" || !reflect.DeepEqual(archived, listed) {
					t.Fatalf("archive/collection metadata disagree: %v / %v", archived, listed)
				}
			} else if string(members["devcontainer-feature.json"].data) != metadata {
				t.Fatal("metadata without legacy IDs must retain original bytes")
			}
			if !reflect.DeepEqual(listed["custom"], map[string]any{"opaque": []any{float64(1), true}}) {
				t.Fatal("unknown metadata dropped")
			}
			original, err := os.ReadFile(filepath.Join(source, "devcontainer-feature.json"))
			if err != nil || string(original) != metadata {
				t.Fatalf("source changed: %s %v", original, err)
			}
			var file Collection
			data, err := os.ReadFile(filepath.Join(out, "devcontainer-collection.json"))
			if err != nil || json.Unmarshal(data, &file) != nil || !reflect.DeepEqual(file, *collection) {
				// RawMessage whitespace differs after collection pretty printing.
				var a, b any
				encoded, _ := json.Marshal(collection)
				if json.Unmarshal(data, &a) != nil || json.Unmarshal(encoded, &b) != nil || !reflect.DeepEqual(a, b) {
					t.Fatalf("collection file differs: %s %v", data, err)
				}
			}
		})
	}
}

func TestPackageCollection(t *testing.T) {
	source, out := filepath.Join(t.TempDir(), "input"), filepath.Join(t.TempDir(), "output")
	packageFixture(t, filepath.Join(source, "zeta"), strings.Replace(packageMetadata, "sample", "different-id", 1), true)
	packageFixture(t, filepath.Join(source, "alpha"), packageMetadata, true)
	packageFixture(t, filepath.Join(source, ".hidden"), packageMetadata, false)
	if err := os.Mkdir(filepath.Join(source, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	collection, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if len(collection.Features) != 2 || !bytes.Contains(collection.Features[0], []byte(`"sample"`)) {
		t.Fatalf("collection entries or order = %s", collection.Features)
	}
	for _, name := range []string{"alpha", "zeta"} {
		packageMembers(t, filepath.Join(out, "devcontainer-feature-"+name+".tgz"))
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 3 {
		t.Fatalf("hidden/non-feature entries packaged: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(out, "devcontainer-feature-different-id.tgz")); !os.IsNotExist(err) {
		t.Fatal("collection archive must use directory name")
	}
}

func TestPackageInstallRule(t *testing.T) {
	source := filepath.Join(t.TempDir(), "input")
	packageFixture(t, source, packageMetadata, false)
	if _, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: filepath.Join(t.TempDir(), "single")}); err != nil {
		t.Fatalf("single Feature without install.sh is accepted upstream: %v", err)
	}
	_, err := Package(t.Context(), PackageOptions{Target: filepath.Dir(source), OutputDir: filepath.Join(t.TempDir(), "collection")})
	if err == nil || !strings.Contains(err.Error(), "install.sh") {
		t.Fatalf("collection without install.sh: %v", err)
	}
}

func TestPackageLegacyMetadata(t *testing.T) {
	for _, ids := range []string{`[]`, `["old-sample"]`} {
		t.Run(ids, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "input")
			metadata := strings.TrimSuffix(packageMetadata, "}") + `,"legacyIds":` + ids + "}"
			packageFixture(t, source, metadata, true)
			out := filepath.Join(t.TempDir(), "output")
			collection, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: out})
			if err != nil {
				t.Fatal(err)
			}
			var listed map[string]any
			if err := json.Unmarshal(collection.Features[0], &listed); err != nil {
				t.Fatal(err)
			}
			if ids == `[]` {
				if _, ok := listed["currentId"]; ok {
					t.Fatal("empty legacyIds added currentId")
				}
			} else if listed["currentId"] != "sample" {
				t.Fatal("legacyIds did not add currentId")
			}
			members := packageMembers(t, filepath.Join(out, "devcontainer-feature-sample.tgz"))
			if ids != `[]` && !bytes.Contains(members["devcontainer-feature.json"].data, []byte(`"currentId": "sample"`)) {
				t.Fatal("archive omitted currentId")
			}
		})
	}
}

func TestPackageSourceUnchanged(t *testing.T) {
	source := filepath.Join(t.TempDir(), "input")
	metadata := strings.TrimSuffix(packageMetadata, "}") + `,"legacyIds":["old-sample"],"currentId":"stale"}`
	packageFixture(t, source, metadata, true)
	if _, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: filepath.Join(t.TempDir(), "output")}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(source, "devcontainer-feature.json"))
	if err != nil || string(data) != metadata {
		t.Fatal("packaging changed source metadata")
	}
}

func TestPackageSourceSymlinkUnchanged(t *testing.T) {
	source := filepath.Join(t.TempDir(), "input")
	metadata := strings.TrimSuffix(packageMetadata, "}") + `,"legacyIds":["old-sample"]}`
	packageFixture(t, source, metadata, true)
	if err := os.Rename(filepath.Join(source, "devcontainer-feature.json"), filepath.Join(source, "metadata.json")); err != nil {
		t.Fatal(err)
	}
	// An absolute link remains meaningful in a private source snapshot.
	if err := os.Symlink(filepath.Join(source, "metadata.json"), filepath.Join(source, "devcontainer-feature.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: filepath.Join(t.TempDir(), "output")}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(source, "metadata.json"))
	if err != nil || string(data) != metadata {
		t.Fatal("packaging followed the metadata symlink back into source")
	}
}

func TestPackageArchiveCanBeResolved(t *testing.T) {
	source := filepath.Join(t.TempDir(), "input")
	packageFixture(t, source, packageMetadata, true)
	out := filepath.Join(t.TempDir(), "output")
	if _, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: out}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(out)))
	defer server.Close()
	resolved, err := New().resolveTarball(t.Context(), server.URL+"/devcontainer-feature-sample.tgz", nil, filepath.Join(t.TempDir(), "resolved"), "sample", "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Metadata.ID != "sample" || resolved.Metadata.Version != "1.2.3" {
		t.Fatalf("resolved metadata = %+v", resolved.Metadata)
	}
	data, err := os.ReadFile(filepath.Join(resolved.Dir, "install.sh"))
	if err != nil || string(data) != "#!/bin/sh\nexit 0\n" {
		t.Fatal("packaged Feature cannot be consumed")
	}
}

func TestPackageCollectionFailureKeepsEarlierArchives(t *testing.T) {
	source := filepath.Join(t.TempDir(), "input")
	packageFixture(t, filepath.Join(source, "alpha"), packageMetadata, true)
	packageFixture(t, filepath.Join(source, "beta"), `{"id":"second","version":"1"}`, true)
	out := filepath.Join(t.TempDir(), "output")
	if _, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: out}); err == nil {
		t.Fatal("invalid collection metadata accepted")
	}
	for _, name := range []string{"alpha", "beta"} {
		packageMembers(t, filepath.Join(out, "devcontainer-feature-"+name+".tgz"))
	}
	if _, err := os.Stat(filepath.Join(out, "devcontainer-collection.json")); !os.IsNotExist(err) {
		t.Fatal("failed collection published metadata")
	}
}

func TestPackageRejectsArchiveTraversal(t *testing.T) {
	root := t.TempDir()
	source, out, outside := filepath.Join(root, "input"), filepath.Join(root, "output"), filepath.Join(root, "outside.tgz")
	packageFixture(t, source, strings.Replace(packageMetadata, "sample", "../../../outside", 1), true)
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: out})
	if err == nil || !strings.Contains(err.Error(), "outside output directory") {
		t.Fatalf("traversal error = %v", err)
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "keep" {
		t.Fatal("archive escaped the selected output directory")
	}
	if _, err := os.Stat(filepath.Join(out, "devcontainer-collection.json")); !os.IsNotExist(err) {
		t.Fatal("invalid archive path published collection metadata")
	}
}

func TestPackageOutput(t *testing.T) {
	t.Chdir(t.TempDir())
	packageFixture(t, ".", packageMetadata, true)
	if _, err := Package(t.Context(), PackageOptions{}); err != nil {
		t.Fatal(err)
	}
	packageMembers(t, "output/devcontainer-feature-sample.tgz")
	if err := os.WriteFile("output/sentinel", []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Package(t.Context(), PackageOptions{}); err == nil {
		t.Fatal("existing output was accepted without force")
	}
	data, err := os.ReadFile("output/sentinel")
	if err != nil || string(data) != "keep" {
		t.Fatal("refusal changed existing output")
	}
	if _, err := Package(t.Context(), PackageOptions{ForceCleanOutputDir: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("output/sentinel"); !os.IsNotExist(err) {
		t.Fatal("forced output was not cleaned")
	}
	packageMembers(t, "output/devcontainer-feature-sample.tgz")
}

func TestPackageFailures(t *testing.T) {
	for _, key := range []string{"id", "version", "name"} {
		t.Run("missing-"+key, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "input")
			var metadata map[string]string
			_ = json.Unmarshal([]byte(packageMetadata), &metadata)
			delete(metadata, key)
			data, _ := json.Marshal(metadata)
			packageFixture(t, source, string(data), true)
			if _, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: filepath.Join(t.TempDir(), "output")}); err == nil {
				t.Fatalf("missing %s accepted", key)
			}
		})
	}
	t.Run("missing-target", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "output")
		if _, err := Package(t.Context(), PackageOptions{Target: filepath.Join(t.TempDir(), "absent"), OutputDir: out}); err == nil {
			t.Fatal("absent target accepted")
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("missing target created output")
		}
	})
	t.Run("empty-collection", func(t *testing.T) {
		if _, err := Package(t.Context(), PackageOptions{Target: t.TempDir(), OutputDir: filepath.Join(t.TempDir(), "output")}); err == nil {
			t.Fatal("empty collection accepted")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "input")
		packageFixture(t, source, packageMetadata, true)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		out := filepath.Join(t.TempDir(), "output")
		_, err := Package(ctx, PackageOptions{Target: source, OutputDir: out})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatal("cancelled call created output")
		}
	})
}

func TestPackagePreservesStickyDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "sample")
	packageFixture(t, source, packageMetadata, true)
	directory := filepath.Join(source, "shared")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o755|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSticky == 0 {
		t.Fatal("fixture sticky mode unavailable")
	}
	out := filepath.Join(t.TempDir(), "output")
	if _, err := Package(t.Context(), PackageOptions{Target: source, OutputDir: out}); err != nil {
		t.Fatal(err)
	}
	member := packageMembers(t, filepath.Join(out, "devcontainer-feature-sample.tgz"))["shared"]
	if member.header.Mode != 0o1755 {
		t.Fatalf("directory mode = %#o; want upstream %#o", member.header.Mode, 0o1755)
	}
}
