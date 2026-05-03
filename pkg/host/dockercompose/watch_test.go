package dockercompose

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
)

func TestMatchPath_Globs(t *testing.T) {
	cases := []struct {
		pattern string
		rel     string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "sub/main.go", false},
		{"**/main.go", "main.go", true},
		{"**/main.go", "deep/dir/main.go", true},
		{"src/**", "src/x.go", true},
		{"src/**", "src/sub/x.go", true},
		{"src/**", "lib/x.go", false},
		{"foo?.txt", "fooA.txt", true},
		{"foo?.txt", "fooAB.txt", false},
	}
	for _, c := range cases {
		got := matchPath(c.pattern, c.rel)
		if got != c.want {
			t.Errorf("matchPath(%q, %q) = %v, want %v", c.pattern, c.rel, got, c.want)
		}
	}
}

func TestMatchesTrigger_IncludeIgnore(t *testing.T) {
	tr := types.Trigger{
		Include: []string{"src/**"},
		Ignore:  []string{"**/*.tmp"},
	}
	cases := []struct {
		rel  string
		want bool
	}{
		{"src/main.go", true},
		{"src/sub/main.go", true},
		{"src/main.tmp", false},
		{"docs/x.md", false},
	}
	for _, c := range cases {
		got := matchesTrigger(tr, c.rel)
		if got != c.want {
			t.Errorf("matchesTrigger(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

func TestScanTriggerPath_PicksUpFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "sub", "x.go"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "ignore.tmp"), []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}

	tr := types.Trigger{Path: dir, Include: []string{"src/**"}, Ignore: []string{"**/*.tmp"}}
	out, err := scanTriggerPath(tr)
	if err != nil {
		t.Fatalf("scanTriggerPath: %v", err)
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, filepath.ToSlash(k))
	}
	sort.Strings(keys)
	want := []string{"src/main.go", "src/sub/x.go"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("snapshot keys: got %v want %v", keys, want)
	}
}

func TestDiffSnapshots_DetectsChangeAndDelete(t *testing.T) {
	now := time.Now()
	prev := map[string]time.Time{
		"a.txt": now,
		"b.txt": now,
	}
	cur := map[string]time.Time{
		"a.txt": now.Add(time.Second), // changed
		"c.txt": now,                  // new
		// b.txt removed
	}
	changed, deleted := diffSnapshots(prev, cur)
	if !reflect.DeepEqual(changed, []string{"a.txt", "c.txt"}) {
		t.Errorf("changed: got %v", changed)
	}
	if !reflect.DeepEqual(deleted, []string{"b.txt"}) {
		t.Errorf("deleted: got %v", deleted)
	}
}

func TestBuildTar_PreservesRelpathLayout(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "x.go"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, err := buildTar(dir, []string{"main.go", "sub/x.go"})
	if err != nil {
		t.Fatalf("buildTar: %v", err)
	}
	got := map[string]string{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, tr); err != nil {
			t.Fatal(err)
		}
		got[hdr.Name] = buf.String()
	}
	want := map[string]string{
		"main.go":  "hello",
		"sub/x.go": "world",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tar contents: got %v want %v", got, want)
	}
}
