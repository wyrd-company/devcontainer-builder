package dockercompose

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/mount"
)

func TestObjectMaterializer_FileSourceReturnedVerbatim(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(dir, "creds")
	if err := os.WriteFile(host, []byte("api-key"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := testObjectMaterializer(t, nil)
	got, err := m.resolveSecret("creds", types.SecretConfig{File: host})
	if err != nil {
		t.Fatalf("resolveSecret: %v", err)
	}
	if got != host {
		t.Errorf("file source: got %q want %q", got, host)
	}
}

func TestObjectMaterializer_EnvSourceWritesToTemp(t *testing.T) {
	m := testObjectMaterializer(t, map[string]string{"API_KEY": "s3kret"})
	got, err := m.resolveSecret("api", types.SecretConfig{Environment: "API_KEY"})
	if err != nil {
		t.Fatalf("resolveSecret: %v", err)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read materialized: %v", err)
	}
	if string(body) != "s3kret" {
		t.Errorf("content: got %q want %q", string(body), "s3kret")
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o400 {
		t.Errorf("permissions: got %v want 0400", info.Mode().Perm())
	}
}

func TestObjectMaterializer_EnvMissingReturnsError(t *testing.T) {
	m := testObjectMaterializer(t, map[string]string{})
	_, err := m.resolveSecret("api", types.SecretConfig{Environment: "MISSING"})
	if err == nil {
		t.Fatal("expected error for missing env")
	}
	if !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("error should mention env name; got %q", err.Error())
	}
}

func TestObjectMaterializer_ContentSourceWritesToTemp(t *testing.T) {
	m := testObjectMaterializer(t, nil)
	got, err := m.resolveConfig("inline", types.ConfigObjConfig{Content: "literal"})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read materialized: %v", err)
	}
	if string(body) != "literal" {
		t.Errorf("content: got %q want %q", string(body), "literal")
	}
}

func TestObjectMaterializer_RejectsExternal(t *testing.T) {
	m := testObjectMaterializer(t, nil)
	_, err := m.resolveSecret("api", types.SecretConfig{External: types.External(true)})
	if err == nil {
		t.Fatal("expected error for external secret")
	}
	if !strings.Contains(err.Error(), "external") {
		t.Errorf("error should mention external; got %q", err.Error())
	}
}

func TestTranslateConfigsAndSecrets_DefaultTargets(t *testing.T) {
	dir := t.TempDir()
	configHost := filepath.Join(dir, "config.txt")
	secretHost := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(configHost, []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretHost, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}

	proj := &types.Project{
		Name: "proj_abc",
		Configs: types.Configs{
			"app-config": types.ConfigObjConfig{File: configHost},
		},
		Secrets: types.Secrets{
			"api-key": types.SecretConfig{File: secretHost},
		},
	}
	svc := types.ServiceConfig{
		Name: "app",
		ContainerSpec: types.ContainerSpec{
			Configs: []types.ServiceConfigObjConfig{
				{Source: "app-config"},
			},
			Secrets: []types.ServiceSecretConfig{
				{Source: "api-key"},
			},
		},
	}

	m := testObjectMaterializer(t, nil)
	got, err := translateConfigsAndSecrets(proj, svc, m)
	if err != nil {
		t.Fatalf("translateConfigsAndSecrets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("mounts: got %d want 2", len(got))
	}
	want := map[string]mount.Mount{
		"/app-config":          {Type: mount.TypeBind, Source: configHost, Target: "/app-config", ReadOnly: true},
		"/run/secrets/api-key": {Type: mount.TypeBind, Source: secretHost, Target: "/run/secrets/api-key", ReadOnly: true},
	}
	for _, m := range got {
		w, ok := want[m.Target]
		if !ok {
			t.Errorf("unexpected target %q", m.Target)
			continue
		}
		if w.Source != m.Source || w.Type != m.Type || w.ReadOnly != m.ReadOnly {
			t.Errorf("mount %s: got %+v want %+v", m.Target, m, w)
		}
	}
}

func TestTranslateConfigsAndSecrets_HonorsExplicitTarget(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(dir, "f")
	if err := os.WriteFile(host, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	proj := &types.Project{
		Name:    "p",
		Configs: types.Configs{"c": types.ConfigObjConfig{File: host}},
	}
	svc := types.ServiceConfig{
		Name: "app",
		ContainerSpec: types.ContainerSpec{
			Configs: []types.ServiceConfigObjConfig{{Source: "c", Target: "/etc/myconfig"}},
		},
	}
	m := testObjectMaterializer(t, nil)
	got, err := translateConfigsAndSecrets(proj, svc, m)
	if err != nil {
		t.Fatalf("translateConfigsAndSecrets: %v", err)
	}
	if len(got) != 1 || got[0].Target != "/etc/myconfig" {
		t.Errorf("explicit target: got %+v", got)
	}
}

func TestTranslateConfigsAndSecrets_UndeclaredReferenceFails(t *testing.T) {
	proj := &types.Project{Name: "p"}
	svc := types.ServiceConfig{
		Name: "app",
		ContainerSpec: types.ContainerSpec{
			Secrets: []types.ServiceSecretConfig{{Source: "missing"}},
		},
	}
	m := testObjectMaterializer(t, nil)
	_, err := translateConfigsAndSecrets(proj, svc, m)
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}

func testObjectMaterializer(t *testing.T, env map[string]string) *objectMaterializer {
	t.Helper()
	return newObjectMaterializer(func() (string, error) { return os.MkdirTemp(t.TempDir(), "objects-") }, env)
}

func TestObjectMaterializerPrivatePublication(t *testing.T) {
	for _, name := range []string{"ordinary", ".", "..", "../../outside"} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			m := newObjectMaterializer(func() (string, error) { return os.MkdirTemp(parent, "objects-") }, nil)
			path, err := m.resolveSecret(name, types.SecretConfig{Content: "synthetic-value"})
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(path) != filepath.Join(m.root, "secrets") {
				t.Fatal("object escaped its owned directory")
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary output residue: %v %v", entries, err)
			}
			// A failed replacement leaves the previous complete object intact.
			if err := os.Mkdir(filepath.Join(m.root, "configs"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(m.root, "configs", "blocked"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := m.resolveConfig("blocked", types.ConfigObjConfig{Content: "synthetic-value"}); err == nil {
				t.Fatal("expected publication failure")
			}
			entries, err = os.ReadDir(filepath.Join(m.root, "configs"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("failed publication residue: %v %v", entries, err)
			}
		})
	}
}

func TestObjectMaterializerErrorsDoNotDisclosePayload(t *testing.T) {
	raw := "synthetic +/\"\\ value"
	quoted, _ := json.Marshal(raw)
	spellings := []string{raw, string(quoted), base64.StdEncoding.EncodeToString([]byte(raw)), base64.RawStdEncoding.EncodeToString([]byte(raw)), base64.URLEncoding.EncodeToString([]byte(raw)), base64.RawURLEncoding.EncodeToString([]byte(raw)), url.QueryEscape(raw), url.PathEscape(raw)}
	for i, payload := range spellings {
		for _, kind := range []string{"configs", "secrets"} {
			for _, source := range []string{"content", "environment"} {
				// TempDir includes the test name on some Go versions. Keep payloads
				// out of paths so filesystem diagnostics cannot echo the fixture name.
				t.Run(fmt.Sprintf("%s/%s/spelling-%d", kind, source, i), func(t *testing.T) {
					parent := t.TempDir()
					blocked := filepath.Join(parent, "blocked")
					for _, spelling := range spellings {
						if strings.Contains(blocked, spelling) {
							t.Fatal("test path contains payload")
						}
					}
					if err := os.WriteFile(blocked, []byte("sentinel"), 0600); err != nil {
						t.Fatal(err)
					}
					m := newObjectMaterializer(func() (string, error) { return blocked, nil }, map[string]string{"SAMPLE_VALUE": payload})
					obj := types.FileObjectConfig{Content: payload}
					if source == "environment" {
						obj = types.FileObjectConfig{Environment: "SAMPLE_VALUE"}
					}
					_, err := m.resolveFileObject(kind, "sample", obj)
					if err == nil {
						t.Fatal("expected real filesystem failure")
					}
					for _, spelling := range spellings {
						if strings.Contains(err.Error(), spelling) {
							t.Fatal("filesystem error disclosed payload")
						}
					}
				})
			}
		}
	}
}
