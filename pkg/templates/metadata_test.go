// relationships:
//
//	references: [metadata, ../oci/oci]
package templates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

func TestMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, annotation, config string
		status                   int
		fail                     bool
	}{
		{"object", `{"id":"sample","extra":[true,7]}`, "application/vnd.devcontainers", 200, false},
		{"null", `null`, "application/vnd.devcontainers", 200, false},
		{"array", `["sample",1]`, "application/vnd.devcontainers", 200, false},
		{"scalar", `"sample"`, "application/vnd.devcontainers", 200, false},
		{"missing", "", "application/vnd.devcontainers", 200, true},
		{"malformed", `{`, "application/vnd.devcontainers", 200, true},
		{"wrong config", `{}`, "application/vnd.oci.image.config.v1+json", 200, true},
		{"not found", `{}`, "application/vnd.devcontainers", 404, true},
		{"refused", `{}`, "application/vnd.devcontainers", 403, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v2/collection/sample/manifests/latest" {
					t.Errorf("unexpected fetch %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
				w.WriteHeader(tt.status)
				_ = json.NewEncoder(w).Encode(oci.Manifest{SchemaVersion: 2, Config: oci.Descriptor{MediaType: tt.config}, Annotations: oci.ManifestAnnot{"dev.containers.metadata": tt.annotation}})
			}))
			defer server.Close()
			client := &oci.Client{HTTP: server.Client()}
			got, err := Metadata(client, strings.TrimPrefix(server.URL, "https://")+"/collection/sample")
			if (err != nil) != tt.fail {
				t.Fatalf("got %s, %v", got, err)
			}
			if !tt.fail && string(got) != tt.annotation {
				t.Fatalf("got %s", got)
			}
			if requests != 1 {
				t.Fatalf("fetch count %d", requests)
			}
		})
	}
}

func TestMetadataDigestAndAuthentication(t *testing.T) {
	body := []byte(`{"schemaVersion":2,"config":{"mediaType":"application/vnd.devcontainers"},"annotations":{"dev.containers.metadata":"{\"id\":\"sample\"}"}}`)
	hash := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic dXNlcjpwYXNz" {
			w.Header().Set("WWW-Authenticate", `Basic realm="sample"`)
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client := &oci.Client{HTTP: server.Client(), Auth: sampleCredentials{}}
	ref := strings.TrimPrefix(server.URL, "https://") + "/collection/sample@"
	if _, err := Metadata(client, ref+digest); err != nil {
		t.Fatal(err)
	}
	if _, err := Metadata(client, ref+"sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("accepted digest mismatch")
	}
}

func TestMetadataReference(t *testing.T) {
	for _, tt := range []struct{ input, output string }{
		{"REGISTRY.EXAMPLE.TEST/Collection/Sample:STABLE", "registry.example.test/collection/sample:stable"},
		{"localhost:5000/sample", "localhost:5000/sample:latest"},
		{"registry.example.test/devcontainers-contrib/sample:", "registry.example.test/devcontainers-extra/sample:latest"},
		{"registry.example.test/sample@sha256:ABC", "registry.example.test/sample@sha256:abc"},
	} {
		r, e := metadataReference(tt.input)
		if e != nil || r.String() != tt.output {
			t.Errorf("%s: %s %v", tt.input, r, e)
		}
	}
	for _, input := range []string{"sample", "owner/sample", ".registry/sample", "https://registry.example.test/a", "registry.example.test/a--b", "registry.example.test/a:bad!", "registry.example.test/a@sha512:abc"} {
		if _, err := metadataReference(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestMetadataLocalhostHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"mediaType":"application/vnd.devcontainers"},"annotations":{"dev.containers.metadata":"{}"}}`))
	}))
	defer server.Close()
	host := "localhost:" + strings.Split(strings.TrimPrefix(server.URL, "http://"), ":")[1]
	if _, err := Metadata(&oci.Client{HTTP: server.Client()}, host+"/sample"); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataDoesNotFollowIndex(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		_, _ = w.Write([]byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`))
	}))
	defer server.Close()
	if _, err := Metadata(&oci.Client{HTTP: server.Client()}, strings.TrimPrefix(server.URL, "https://")+"/sample"); err == nil {
		t.Fatal("accepted index")
	}
	if requests != 1 {
		t.Fatalf("followed index: %d requests", requests)
	}
}
