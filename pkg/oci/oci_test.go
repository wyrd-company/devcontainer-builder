package oci

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchManifest_FollowsIndexAndVerifiesChildDigest(t *testing.T) {
	child := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":2},"layers":[{"mediaType":"application/vnd.devcontainers.layer.v1+tar","digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","size":1}]}`)
	sum := sha256.Sum256(child)
	childDigest := "sha256:" + hex.EncodeToString(sum[:])
	index := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":%d,"platform":{"os":"linux","architecture":"amd64"}}]}`, childDigest, len(child)))

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/latest"):
			w.Header().Set("Content-Type", mediaTypeOCIIndex)
			_, _ = w.Write(index)
		case strings.HasSuffix(r.URL.Path, "/manifests/"+childDigest):
			w.Header().Set("Content-Type", mediaTypeOCIManifest)
			_, _ = w.Write(child)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ref := Reference{
		Registry:   strings.TrimPrefix(srv.URL, "https://"),
		Repository: "features/demo",
		Tag:        "latest",
	}
	c := NewClient()
	c.HTTP = srv.Client()
	m, body, err := c.FetchManifest(ref)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if len(m.Layers) != 1 || string(body) != string(child) {
		t.Fatalf("FetchManifest returned wrong child manifest: manifest=%+v body=%s", m, string(body))
	}
}
