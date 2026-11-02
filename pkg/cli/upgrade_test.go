// relationships:
//
//	references: [upgrade, cli_test]
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradeCommand(t *testing.T) {
	w := t.TempDir()
	d := filepath.Join(w, ".devcontainer")
	if err := os.MkdirAll(filepath.Join(d, "sample"), 0755); err != nil {
		t.Fatal(err)
	}
	for p, b := range map[string]string{filepath.Join(d, "devcontainer.json"): `{"features":{"./sample":true}}`, filepath.Join(d, "sample", "devcontainer-feature.json"): `{"id":"sample","version":"1.0.0"}`, filepath.Join(d, "sample", "install.sh"): "#!/bin/sh\n"} {
		if err := os.WriteFile(p, []byte(b), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, dry := range []bool{true, false} {
		args := []string{"upgrade", "--workspace-folder", w}
		if dry {
			args = append(args, "--dry-run")
		}
		var out, logs bytes.Buffer
		code := Run(context.Background(), args, "test", nil, &out, &logs)
		want := ""
		if dry {
			want = "{\n  \"features\": {}\n}\n"
		}
		if code != 0 || out.String() != want {
			t.Fatalf("code=%d out=%s logs=%s", code, &out, &logs)
		}
		_, err := os.Stat(filepath.Join(d, "devcontainer-lock.json"))
		if dry && !os.IsNotExist(err) || !dry && err != nil {
			t.Fatalf("lockfile err=%v dry=%v", err, dry)
		}
	}
	lock := filepath.Join(d, "devcontainer-lock.json")
	if err := os.WriteFile(lock, []byte(`{"features":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	var canonicalOut, canonicalLogs bytes.Buffer
	if code := Run(context.Background(), []string{"upgrade", "--workspace-folder", w}, "test", nil, &canonicalOut, &canonicalLogs); code != 0 {
		t.Fatalf("canonical rewrite: %d %s", code, &canonicalLogs)
	}
	if b, _ := os.ReadFile(lock); string(b) != "{\n  \"features\": {}\n}\n" {
		t.Fatalf("lock not canonical: %s", b)
	}
	for _, args := range [][]string{{"upgrade", "extra"}, {"upgrade", "-f", "sample"}, {"upgrade", "-v", "1"}, {"upgrade", "-f", "sample", "-v", "latest"}, {"upgrade", "--log-level", "off"}} {
		var out, logs bytes.Buffer
		code := Run(context.Background(), args, "test", nil, &out, &logs)
		if code != 2 || !strings.Contains(out.String(), `"outcome":"error"`) {
			t.Fatalf("args=%v code=%d out=%s", args, code, &out)
		}
	}
	var out, logs bytes.Buffer
	code := Run(context.Background(), []string{"upgrade", "--config", filepath.Join(w, "missing.json")}, "test", nil, &out, &logs)
	if code != 1 || out.Len() != 0 || logs.Len() == 0 {
		t.Fatalf("failure code=%d out=%s logs=%s", code, &out, &logs)
	}
}

func TestUpgradeErrorLogSuppressesProgress(t *testing.T) {
	w := t.TempDir()
	p := filepath.Join(w, ".devcontainer.json")
	if err := os.WriteFile(p, []byte(`{"features":{"127.0.0.1:1/collection/sample:1":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"error", "info"} {
		var out, logs bytes.Buffer
		code := Run(context.Background(), []string{"upgrade", "--workspace-folder", w, "--dry-run", "--log-level", level}, "test", nil, &out, &logs)
		if code != 1 || out.Len() != 0 || logs.Len() == 0 {
			t.Fatalf("level=%s code=%d out=%s logs=%s", level, code, &out, &logs)
		}
		if strings.Contains(logs.String(), "pulling feature") != (level == "info") {
			t.Fatalf("level=%s progress filter: %s", level, &logs)
		}
	}
}
