package features

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

// fakeRegistry serves feature artifacts over TLS. Each artifact is one
// gzip tar layer holding devcontainer-feature.json and install.sh.
type fakeRegistry struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	manifests map[string][]byte // "<repo>/manifests/<tag>"
	blobs     map[string][]byte // digest
	requests  []string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	r := &fakeRegistry{t: t, manifests: map[string][]byte{}, blobs: map[string][]byte{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRegistry) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

func (r *fakeRegistry) host() string { return strings.TrimPrefix(r.srv.URL, "https://") }

func (r *fakeRegistry) serve(w http.ResponseWriter, req *http.Request) {
	path := strings.TrimPrefix(req.URL.Path, "/v2/")
	r.mu.Lock()
	r.requests = append(r.requests, path)
	r.mu.Unlock()
	if body, ok := r.manifests[path]; ok {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write(body)
		return
	}
	if i := strings.Index(path, "/blobs/"); i >= 0 {
		if body, ok := r.blobs[path[i+len("/blobs/"):]]; ok {
			_, _ = w.Write(body)
			return
		}
	}
	http.NotFound(w, req)
}

// publish adds repo:tag with the given feature metadata. When annotate
// is set the metadata is also carried in the dev.containers.metadata
// manifest annotation.
func (r *fakeRegistry) publish(repo, tag, metadata string, annotate bool) {
	r.t.Helper()
	layer := tarGz(r.t, []tar.Header{
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./devcontainer-feature.json", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "./install.sh", Typeflag: tar.TypeReg, Mode: 0o755},
	}, map[string]string{"./devcontainer-feature.json": metadata, "./install.sh": "#!/bin/sh\n"})
	sum := sha256.Sum256(layer)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	r.blobs[digest] = layer
	manifest := map[string]any{
		"schemaVersion": 2,
		"mediaType":     "application/vnd.oci.image.manifest.v1+json",
		"config":        map[string]any{"mediaType": "application/vnd.devcontainers", "digest": "sha256:" + strings.Repeat("0", 64), "size": 0},
		"layers":        []any{map[string]any{"mediaType": "application/vnd.devcontainers.layer.v1+tar", "digest": digest, "size": len(layer)}},
	}
	if annotate {
		manifest["annotations"] = map[string]string{"dev.containers.metadata": metadata}
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		r.t.Fatal(err)
	}
	r.manifests[repo+"/manifests/"+tag] = body
}

func (r *fakeRegistry) resolver() *Resolver {
	res := New()
	res.OCI = oci.NewClient()
	res.OCI.HTTP = r.srv.Client()
	return res
}

// The renamed-reference case: alpha installsAfter collection/beta, the
// published beta declares legacyIds [old-beta], and the installed
// feature is collection/old-beta:1, whose own metadata predates the
// rename. Upstream reads beta's aliases, so old-beta installs first.
func TestResolve_InstallsAfterRenamedReferenceUsesReferencedMetadata(t *testing.T) {
	for _, annotate := range []bool{true, false} {
		t.Run(fmt.Sprintf("annotation=%v", annotate), func(t *testing.T) {
			reg := newFakeRegistry(t)
			ns := reg.host() + "/collection"
			reg.publish("collection/alpha", "1", `{"id":"alpha","version":"1.0.0","installsAfter":["`+ns+`/beta"]}`, annotate)
			reg.publish("collection/old-beta", "1", `{"id":"old-beta","version":"1.0.0"}`, annotate)
			reg.publish("collection/beta", "latest", `{"id":"beta","version":"2.0.0","legacyIds":["old-beta"]}`, annotate)

			got, err := reg.resolver().Resolve(userFeatures(ns+"/alpha:1", `{}`, ns+"/old-beta:1", `{}`), nil,
				filepath.Join(t.TempDir(), "stage"), t.TempDir())
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if g := strings.Join(ids(got), ","); g != "old-beta,alpha" {
				t.Fatalf("order = %s, want old-beta,alpha", g)
			}
			wantFetch := "collection/beta/manifests/latest"
			if !strings.Contains(strings.Join(reg.requested(), "\n"), wantFetch) {
				t.Fatalf("beta metadata not read; requests: %v", reg.requested())
			}
		})
	}
}

// A soft dependency that cannot share a namespace with any installed
// feature cannot match through aliases, so it is not fetched.
func TestResolve_InstallsAfterOtherNamespaceIsNotFetched(t *testing.T) {
	reg := newFakeRegistry(t)
	ns := reg.host() + "/collection"
	reg.publish("collection/alpha", "1", `{"id":"alpha","version":"1.0.0","installsAfter":["`+reg.host()+`/elsewhere/beta"]}`, true)

	got, err := reg.resolver().Resolve(userFeatures(ns+"/alpha:1", `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("resolved %d features", len(got))
	}
	for _, p := range reg.requested() {
		if strings.Contains(p, "elsewhere") {
			t.Fatalf("fetched unrelated soft dependency: %v", reg.requested())
		}
	}
}
