// relationships:
//
//	references: [features, lockfile, tarball, registry_test]
package features

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLockfilePinsRootAndDependencies(t *testing.T) {
	reg := newFakeRegistry(t)
	root := reg.host() + "/collection/alpha:1"
	dep := reg.host() + "/collection/beta:1"
	reg.publish("collection/alpha", "1", `{"id":"alpha","version":"1.0.0","dependsOn":{"`+dep+`":{}}}`, "")
	reg.publish("collection/beta", "1", `{"id":"beta","version":"1.0.0"}`, "")
	first, err := reg.resolver().Resolve(userFeatures(root, `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lock := GenerateLockfile(first)
	for _, f := range first {
		reg.manifests[f.Reference.Repository+"/manifests/"+f.Digest] = reg.manifests[f.Reference.Repository+"/manifests/1"]
	}
	reg.publish("collection/alpha", "1", `{"id":"alpha","version":"2.0.0"}`, "")
	reg.publish("collection/beta", "1", `{"id":"beta","version":"2.0.0"}`, "")
	// Upstream pins by integrity on the original repository, not resolved.
	entry := lock.Features[root]
	entry.Resolved = "registry.example/elsewhere/feature@" + entry.Integrity
	lock.Features[root] = entry
	pinned, err := reg.resolver().ResolveWithLockfile(context.Background(), userFeatures(root, `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir(), lock)
	if err != nil {
		t.Fatal(err)
	}
	if len(pinned) != 2 || pinned[0].Metadata.Version != "1.0.0" || pinned[1].Metadata.Version != "1.0.0" {
		t.Fatalf("not pinned: %+v", pinned)
	}
	if pinned[1].Reference.Tag != "1" || pinned[1].Source != root {
		t.Fatalf("original reference changed: %+v", pinned[1])
	}
	if got := lock.Features[root].DependsOn; len(got) != 1 || got[0] != dep {
		t.Fatalf("dependsOn=%v", got)
	}
}

func TestLockfileManifestIntegrity(t *testing.T) {
	for _, mode := range []string{"lockfile", "digest-reference"} {
		t.Run(mode, func(t *testing.T) {
			reg := newFakeRegistry(t)
			reg.publish("collection/sample", "1", `{"id":"sample","version":"1.0.0"}`, "")
			wrong := "sha256:" + strings.Repeat("0", 64)
			reg.manifests["collection/sample/manifests/"+wrong] = reg.manifests["collection/sample/manifests/1"]
			source := reg.host() + "/collection/sample:1"
			lock := &Lockfile{Features: map[string]LockedFeature{source: {Integrity: wrong}}}
			if mode == "digest-reference" {
				source = reg.host() + "/collection/sample@" + wrong
				lock = nil
			}
			_, err := reg.resolver().ResolveWithLockfile(context.Background(), userFeatures(source, `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir(), lock)
			if err == nil || !strings.Contains(err.Error(), "does not match its digest") {
				t.Fatalf("integrity err=%v", err)
			}
		})
	}
}

func TestLockfileLayerIntegrity(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.publish("collection/sample", "1", `{"id":"sample","version":"1.0.0"}`, "")
	for digest := range reg.blobs {
		reg.blobs[digest] = []byte("corrupt archive")
	}
	_, err := reg.resolver().Resolve(userFeatures(reg.host()+"/collection/sample:1", `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "does not match its digest") {
		t.Fatalf("layer err=%v", err)
	}
}

func sampleTarball(t *testing.T, version string) []byte {
	t.Helper()
	return tarGz(t, []tar.Header{{Name: "devcontainer-feature.json", Typeflag: tar.TypeReg, Mode: 0o644}, {Name: "install.sh", Typeflag: tar.TypeReg, Mode: 0o755}}, map[string]string{"devcontainer-feature.json": `{"id":"sample","version":"` + version + `"}`, "install.sh": "#!/bin/sh\n"})
}

func TestLockfileTarballIntegrity(t *testing.T) {
	data := sampleTarball(t, "1.0.0")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	defer server.Close()
	source := server.URL + "/devcontainer-feature-sample.tgz"
	r := New()
	r.OCI.HTTP = server.Client()
	got, err := r.Resolve(userFeatures(source, `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lock := GenerateLockfile(got)
	entry := lock.Features[source]
	if entry.Resolved != source || entry.Integrity != fmt.Sprintf("sha256:%x", sha256.Sum256(data)) || entry.Version != "1.0.0" {
		t.Fatalf("entry=%+v", entry)
	}
	data = sampleTarball(t, "2.0.0")
	_, err = r.ResolveWithLockfile(context.Background(), userFeatures(source, `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir(), lock)
	if err == nil || !strings.Contains(err.Error(), "tarball does not match its digest") {
		t.Fatalf("tarball err=%v", err)
	}
}

func TestTarballNamesAndInstallOrder(t *testing.T) {
	data := sampleTarball(t, "1.0.0")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	defer server.Close()
	r := New()
	r.OCI.HTTP = server.Client()
	alpha, beta := server.URL+"/devcontainer-feature-alpha.tgz", server.URL+"/devcontainer-feature-beta.tgz"
	got, err := r.Resolve(userFeatures(alpha, `{}`, beta, `{}`), []string{beta}, filepath.Join(t.TempDir(), "stage"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Metadata.ID != "beta" || got[1].Metadata.ID != "alpha" {
		t.Fatalf("order=%+v", got)
	}
	_, err = r.Resolve(userFeatures(server.URL+"/invalid.tgz", `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "expected tarball name") {
		t.Fatalf("invalid filename err=%v", err)
	}
}

func TestLockfileConcurrentPublicationAndRetry(t *testing.T) {
	for _, failFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("held-run-fails=%v", failFirst), func(t *testing.T) {
			firstData, secondData := sampleTarball(t, "1.0.0"), sampleTarball(t, "2.0.0")
			entered, release := make(chan struct{}), make(chan struct{})
			var count atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if count.Add(1) == 1 {
					close(entered)
					<-release
					if failFirst {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					_, _ = w.Write(firstData)
					return
				}
				_, _ = w.Write(secondData)
			}))
			defer server.Close()
			// Cleanup runs before server.Close even after an assertion failure.
			defer close(release)
			config := filepath.Join(t.TempDir(), "folder", "..", "devcontainer.json")
			source := server.URL + "/devcontainer-feature-sample.tgz"
			first, err := ReadLockfile(config, true, false)
			if err != nil {
				t.Fatal(err)
			}
			second, err := ReadLockfile(config, true, false)
			if err != nil {
				t.Fatal(err)
			}
			run := func(state *LockfileState) error {
				r := New()
				r.OCI.HTTP = server.Client()
				stage, err := os.MkdirTemp(filepath.Dir(LockfilePath(config)), "stage-")
				if err != nil {
					return err
				}
				defer os.RemoveAll(stage)
				resolved, err := r.ResolveWithLockfile(context.Background(), userFeatures(source, `{}`), nil, stage, filepath.Dir(config), state.Lockfile)
				if err != nil {
					return err
				}
				if err := state.Prepare(resolved); err != nil {
					return err
				}
				return state.Publish(context.Background())
			}
			done := make(chan error, 1)
			go func() { done <- run(first) }()
			<-entered
			if _, err := os.Stat(LockfilePath(config)); !os.IsNotExist(err) {
				t.Fatalf("held run published: %v", err)
			}
			if err := run(second); err != nil {
				t.Fatal(err)
			}
			published, err := os.ReadFile(LockfilePath(config))
			if err != nil || !strings.Contains(string(published), "2.0.0") {
				t.Fatalf("second result=%s err=%v", published, err)
			}
			release <- struct{}{}
			firstErr := <-done
			if (firstErr != nil) != failFirst {
				t.Fatalf("held run error=%v", firstErr)
			}
			published, err = os.ReadFile(LockfilePath(config))
			want := "1.0.0"
			if failFirst {
				want = "2.0.0"
			}
			if err != nil || !strings.Contains(string(published), want) {
				t.Fatalf("final result=%s want=%s err=%v", published, want, err)
			}
			// Retry through a freshly read snapshot: the server now supplies v2.
			if failFirst {
				retry, err := ReadLockfile(config, false, false)
				if err != nil {
					t.Fatal(err)
				}
				if err := run(retry); err != nil {
					t.Fatalf("retry=%v", err)
				}
			}
		})
	}
}
