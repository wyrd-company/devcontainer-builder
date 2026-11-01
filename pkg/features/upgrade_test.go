// relationships:
//
//	references: [upgrade, registry_test, lockfile]
package features

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpgradePinsDependenciesAndUpdatesRequirement(t *testing.T) {
	reg := newFakeRegistry(t)
	dep := reg.host() + "/collection/beta:1"
	id := reg.host() + "/collection/alpha:1"
	reg.publish("collection/beta", "1", `{"id":"beta","version":"1.0.0"}`, "")
	reg.publish("collection/alpha", "1", `{"id":"alpha","version":"1.0.0","dependsOn":{"`+dep+`":{}}}`, "")
	reg.publish("collection/alpha", "2", `{"id":"alpha","version":"2.0.0","dependsOn":{"`+dep+`":{}}}`, "")
	w := t.TempDir()
	p := filepath.Join(w, ".devcontainer.json")
	body := `// keep this comment` + "\n" + `{"image":"alpine","features":{"` + id + `":{}},"name":"` + id + `"}`
	if err := os.WriteFile(p, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	r := reg.resolver()
	out, err := r.Upgrade(context.Background(), UpgradeOptions{Workspace: w, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(LockfilePath(p)); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote absent lock: %v", err)
	}
	var first Lockfile
	if err := json.Unmarshal(out, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Features) != 2 || first.Features[id].Version != "1.0.0" {
		t.Fatalf("lock=%s", out)
	}
	if _, err = r.Upgrade(context.Background(), UpgradeOptions{Workspace: w}); err != nil {
		t.Fatal(err)
	}
	// Save original manifests under digest handles, then move tags. Existing pins
	// must keep both the root and transitive dependency unchanged.
	for _, key := range []string{id, dep} {
		ref, _ := versionReference(key)
		reg.manifests[ref.Repository+"/manifests/"+first.Features[key].Integrity] = reg.manifests[ref.Repository+"/manifests/1"]
	}
	reg.publish("collection/alpha", "1", `{"id":"alpha","version":"9.0.0"}`, "")
	reg.publish("collection/beta", "1", `{"id":"beta","version":"9.0.0"}`, "")
	out, err = r.Upgrade(context.Background(), UpgradeOptions{Workspace: w, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var pinned Lockfile
	_ = json.Unmarshal(out, &pinned)
	if pinned.Features[id].Version != "1.0.0" || pinned.Features[dep].Version != "1.0.0" {
		t.Fatalf("lost pins: %s", out)
	}
	out, err = r.Upgrade(context.Background(), UpgradeOptions{Workspace: w, DryRun: true, Feature: id, TargetVersion: "2"})
	if err != nil {
		t.Fatal(err)
	}
	var next Lockfile
	_ = json.Unmarshal(out, &next)
	if next.Features[strings.TrimSuffix(id, ":1")+":2"].Version != "2.0.0" || next.Features[dep].Version != "1.0.0" {
		t.Fatalf("updated lock=%s", out)
	}
	updated, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(updated), "// keep this comment\n") || strings.Count(string(updated), strings.TrimSuffix(id, ":1")+":2") != 2 {
		t.Fatalf("config=%s", updated)
	}
	written, _ := os.ReadFile(LockfilePath(p))
	if string(written) != string(out) {
		t.Fatalf("existing lock not refreshed in dry run: %s != %s", written, out)
	}
}

func TestUpgradeValidationAndFailureWrites(t *testing.T) {
	w := t.TempDir()
	p := filepath.Join(w, ".devcontainer.json")
	original := `{"features":{"./sample:1":{}}}`
	if err := os.WriteFile(p, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for _, o := range []UpgradeOptions{{Feature: "./sample"}, {TargetVersion: "1"}, {Feature: "./sample", TargetVersion: "latest"}, {Feature: "./sample", TargetVersion: "1.2.3.4"}} {
		o.Workspace = w
		if _, err := New().Upgrade(context.Background(), o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
		b, _ := os.ReadFile(p)
		if string(b) != original {
			t.Fatalf("validation changed config: %s", b)
		}
	}
	// Upstream changes the config before attempting resolution, even on failure.
	if _, err := New().Upgrade(context.Background(), UpgradeOptions{Workspace: w, Feature: "./sample", TargetVersion: "2", DryRun: true}); err == nil {
		t.Fatal("missing local source accepted")
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "./sample:2") {
		t.Fatalf("config update missing: %s", b)
	}
	if _, err := os.Stat(LockfilePath(p)); !os.IsNotExist(err) {
		t.Fatalf("failed resolution wrote lock: %v", err)
	}
}

// Run complete pinned upstream invocations against the same TLS registry and
// compare their output and file effects. Set DCB_UPSTREAM_CLI to an installed
// build of the read-only pinned reference CLI.
func TestUpgradeUpstreamRegistryDifferential(t *testing.T) {
	upstream := os.Getenv("DCB_UPSTREAM_CLI")
	if upstream == "" {
		t.Skip("DCB_UPSTREAM_CLI not supplied")
	}
	reg := newFakeRegistry(t)
	dep := reg.host() + "/collection/beta:1"
	id := reg.host() + "/collection/alpha:1"
	reg.publish("collection/beta", "1", `{"id":"beta","version":"1.0.0"}`, "")
	reg.publish("collection/alpha", "1", `{"id":"alpha","version":"1.0.0","dependsOn":{"`+dep+`":{}}}`, "")
	reg.publish("collection/alpha", "2", `{"id":"alpha","version":"2.0.0","dependsOn":{"`+dep+`":{}}}`, "")
	cert := filepath.Join(t.TempDir(), "registry-ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: reg.srv.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{false, true} {
		for _, old := range []string{"absent", "empty", "pinned"} {
			for _, target := range []string{"", "2"} {
				t.Run(fmt.Sprintf("dry=%v/old=%s/target=%s", dry, old, target), func(t *testing.T) {
					w := t.TempDir()
					config := filepath.Join(w, ".devcontainer.json")
					lock := LockfilePath(config)
					original := []byte("// retain\n" + `{"image":"alpine","features":{"` + id + `":{}},"name":"` + id + `"}`)
					if err := os.WriteFile(config, original, 0600); err != nil {
						t.Fatal(err)
					}
					var initial []byte
					if old == "empty" {
						initial = []byte{}
					} else if old == "pinned" {
						out, err := reg.resolver().Upgrade(context.Background(), UpgradeOptions{Workspace: w, DryRun: true})
						if err != nil {
							t.Fatal(err)
						}
						initial = out
						var l Lockfile
						_ = json.Unmarshal(out, &l)
						for k, v := range l.Features {
							ref, _ := versionReference(k)
							reg.manifests[ref.Repository+"/manifests/"+v.Integrity] = reg.manifests[ref.Repository+"/manifests/1"]
						}
					}
					if initial != nil {
						if err := os.WriteFile(lock, initial, 0600); err != nil {
							t.Fatal(err)
						}
					}
					args := []string{upstream, "upgrade", "--workspace-folder", w}
					if dry {
						args = append(args, "--dry-run")
					}
					if target != "" {
						args = append(args, "-f", id, "-v", target)
					}
					cmd := exec.Command("node", args...)
					cmd.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+cert)
					var logs bytes.Buffer
					cmd.Stderr = &logs
					want, err := cmd.Output()
					if err != nil {
						t.Fatalf("upstream: %v %s", err, &logs)
					}
					wantConfig, _ := os.ReadFile(config)
					wantLock, wantErr := os.ReadFile(lock)
					if err := os.WriteFile(config, original, 0600); err != nil {
						t.Fatal(err)
					}
					_ = os.Remove(lock)
					if initial != nil {
						if err := os.WriteFile(lock, initial, 0600); err != nil {
							t.Fatal(err)
						}
					}
					o := UpgradeOptions{Workspace: w, DryRun: dry}
					if target != "" {
						o.Feature = id
						o.TargetVersion = target
					}
					got, err := reg.resolver().Upgrade(context.Background(), o)
					if err != nil {
						t.Fatal(err)
					}
					if !dry {
						got = nil
					}
					gotConfig, _ := os.ReadFile(config)
					gotLock, gotErr := os.ReadFile(lock)
					if !bytes.Equal(want, got) || !bytes.Equal(wantConfig, gotConfig) || !bytes.Equal(wantLock, gotLock) || os.IsNotExist(wantErr) != os.IsNotExist(gotErr) {
						t.Fatalf("upstream out=%s config=%s lock=%s err=%v; native out=%s config=%s lock=%s err=%v", want, wantConfig, wantLock, wantErr, got, gotConfig, gotLock, gotErr)
					}
				})
			}
		}
	}
}
