package dockercompose

import (
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
	m := newObjectMaterializer("proj", nil)
	got, err := m.resolveSecret("creds", types.SecretConfig{File: host})
	if err != nil {
		t.Fatalf("resolveSecret: %v", err)
	}
	if got != host {
		t.Errorf("file source: got %q want %q", got, host)
	}
}

func TestObjectMaterializer_EnvSourceWritesToTemp(t *testing.T) {
	defer removeMaterialized("proj-env-test")
	m := newObjectMaterializer("proj-env-test", map[string]string{"API_KEY": "s3kret"})
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
	m := newObjectMaterializer("proj-env-missing", map[string]string{})
	_, err := m.resolveSecret("api", types.SecretConfig{Environment: "MISSING"})
	if err == nil {
		t.Fatal("expected error for missing env")
	}
	if !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("error should mention env name; got %q", err.Error())
	}
}

func TestObjectMaterializer_ContentSourceWritesToTemp(t *testing.T) {
	defer removeMaterialized("proj-content-test")
	m := newObjectMaterializer("proj-content-test", nil)
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
	m := newObjectMaterializer("proj", nil)
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
		Configs: []types.ServiceConfigObjConfig{
			{Source: "app-config"},
		},
		Secrets: []types.ServiceSecretConfig{
			{Source: "api-key"},
		},
	}

	m := newObjectMaterializer("proj_abc", nil)
	got, err := translateConfigsAndSecrets(proj, svc, m)
	if err != nil {
		t.Fatalf("translateConfigsAndSecrets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("mounts: got %d want 2", len(got))
	}
	want := map[string]mount.Mount{
		"/app-config":         {Type: mount.TypeBind, Source: configHost, Target: "/app-config", ReadOnly: true},
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
		Name:    "app",
		Configs: []types.ServiceConfigObjConfig{{Source: "c", Target: "/etc/myconfig"}},
	}
	m := newObjectMaterializer("p", nil)
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
		Name:    "app",
		Secrets: []types.ServiceSecretConfig{{Source: "missing"}},
	}
	m := newObjectMaterializer("p", nil)
	_, err := translateConfigsAndSecrets(proj, svc, m)
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}
