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

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestFetchImageConfig_SelectsPlatformManifest(t *testing.T) {
	blobs := map[string][]byte{}
	manifests := map[string][]byte{}
	var children []string
	for _, p := range []struct{ arch, user string }{{"amd64", "amd-user"}, {"arm64", "arm-user"}} {
		cfg := []byte(fmt.Sprintf(`{"architecture":%q,"os":"linux","config":{"User":%q}}`, p.arch, p.user))
		blobs[digestOf(cfg)] = cfg
		m := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`,
			mediaTypeOCIManifest, digestOf(cfg), len(cfg)))
		manifests[digestOf(m)] = m
		children = append(children, fmt.Sprintf(`{"mediaType":%q,"digest":%q,"size":%d,"platform":{"os":"linux","architecture":%q}}`,
			mediaTypeOCIManifest, digestOf(m), len(m), p.arch))
	}
	index := []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":%q,"manifests":[%s]}`, mediaTypeOCIIndex, strings.Join(children, ",")))

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/1"):
			w.Header().Set("Content-Type", mediaTypeOCIIndex)
			_, _ = w.Write(index)
		case strings.Contains(r.URL.Path, "/manifests/"):
			_, _ = w.Write(manifests[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]])
		case strings.Contains(r.URL.Path, "/blobs/"):
			_, _ = w.Write(blobs[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ref := Reference{Registry: strings.TrimPrefix(srv.URL, "https://"), Repository: "library/base", Tag: "1"}
	c := NewClient()
	c.HTTP = srv.Client()

	for arch, want := range map[string]string{"amd64": "amd-user", "arm64": "arm-user"} {
		cfg, err := c.FetchImageConfig(ref, Platform{OS: "linux", Architecture: arch})
		if err != nil {
			t.Fatalf("%s: %v", arch, err)
		}
		if cfg.Config.User != want {
			t.Errorf("%s user = %q, want %q", arch, cfg.Config.User, want)
		}
	}
	if _, err := c.FetchImageConfig(ref, Platform{OS: "linux", Architecture: "s390x"}); err == nil {
		t.Fatal("expected error for a platform the index lacks")
	}
}
