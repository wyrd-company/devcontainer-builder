// relationships:
//
//	references: [info, registry_test]
package features

import (
	"encoding/json"
	"errors"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInfoRequiresManifestInEveryMode(t *testing.T) {
	r := newFakeRegistry(t)
	for _, mode := range []string{"manifest", "tags", "dependencies", "verbose"} {
		for _, format := range []string{"text", "json"} {
			t.Run(mode+"/"+format, func(t *testing.T) {
				got, err := Info(r.resolver().OCI, mode, r.host()+"/collection/absent", format)
				if !errors.Is(err, ErrInfoUnavailable) {
					t.Fatalf("got %q, %v", got, err)
				}
				if format == "json" && got != "{}\n" {
					t.Fatalf("got %q", got)
				}
			})
		}
	}
}
func TestInfoDependenciesJSONDoesNotReadMetadata(t *testing.T) {
	r := newFakeRegistry(t)
	r.publish("collection/sample", "1", `{"id":"sample","dependsOn":{"invalid":{}}}`, "")
	got, err := Info(r.resolver().OCI, "dependencies", r.host()+"/collection/sample:1", "json")
	if err != nil || got != "{}\n" || len(r.requested()) != 1 {
		t.Fatalf("got %q, %v, requests %v", got, err, r.requested())
	}
}
func TestInfoRejectsArtifactType(t *testing.T) {
	r := newFakeRegistry(t)
	r.manifests["collection/sample/manifests/latest"] = []byte(`{"config":{"mediaType":"wrong"}}`)
	got, err := Info(r.resolver().OCI, "manifest", r.host()+"/collection/sample", "json")
	if !errors.Is(err, ErrInfoUnavailable) || got != "{}\n" {
		t.Fatalf("%s %v", got, err)
	}
}
func TestInfoRegistryTagsRetainOrderAndDuplicates(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "tags/list") {
			_, _ = w.Write([]byte(`{"tags":["latest","2","1.0.0","beta","1.0.0"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"config":{"mediaType":"application/vnd.devcontainers"}}`))
	}))
	defer s.Close()
	c := oci.NewClient()
	c.HTTP = s.Client()
	got, err := Info(c, "tags", strings.TrimPrefix(s.URL, "https://")+"/collection/sample", "json")
	if err != nil || got != "{\n    \"publishedTags\": [\n        \"latest\",\n        \"2\",\n        \"1.0.0\",\n        \"beta\",\n        \"1.0.0\"\n    ]\n}\n" {
		t.Fatalf("%s %v", got, err)
	}
}
func TestInfoManifestPreservesPropertiesAndCanonicalHeader(t *testing.T) {
	body := `{"config":{"mediaType":"application/vnd.devcontainers"},"unknown":{"z":1,"a":2}}`
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Docker-Content-Digest", "sha256:sample")
		_, _ = w.Write([]byte(body))
	}))
	defer s.Close()
	c := oci.NewClient()
	c.HTTP = s.Client()
	host := strings.TrimPrefix(s.URL, "https://")
	got, err := Info(c, "manifest", host+"/collection/sample", "json")
	if err != nil || !strings.Contains(got, host+"/collection/sample@sha256:sample") || !strings.Contains(got, `"unknown"`) {
		t.Fatalf("%s %v", got, err)
	}
}
func TestInfoDependencyGraphHardSoftAndLayerMetadata(t *testing.T) {
	r := newFakeRegistry(t)
	host := r.host()
	root := `{"id":"sample","version":"1.0.0","dependsOn":{"` + host + `/collection/leaf:1":{}},"installsAfter":["` + host + `/collection/leaf:1"]}`
	r.publish("collection/sample", "1", root, root)
	r.publish("collection/leaf", "1", `{"id":"leaf","version":"1.0.0"}`, "")
	got, err := Info(r.resolver().OCI, "dependencies", host+"/collection/sample:1", "text")
	if err != nil || !strings.Contains(got, " --> ") || !strings.Contains(got, " -.-> ") || !strings.Contains(got, "aliases: leaf") {
		t.Fatalf("%s %v", got, err)
	}
}
func TestInfoDigestMismatchFails(t *testing.T) {
	r := newFakeRegistry(t)
	digest := "sha256:" + strings.Repeat("1", 64)
	r.manifests["collection/sample/manifests/"+digest] = []byte(`{"config":{"mediaType":"application/vnd.devcontainers"}}`)
	_, err := Info(r.resolver().OCI, "manifest", r.host()+"/collection/sample@"+digest, "json")
	if err == nil || !strings.Contains(err.Error(), "Digest did not match") {
		t.Fatal(err)
	}
}
func TestInfoManifestJSONHasFourSpaceIndent(t *testing.T) {
	r := newFakeRegistry(t)
	r.publish("collection/sample", "1", `{"id":"sample"}`, "")
	got, err := Info(r.resolver().OCI, "manifest", r.host()+"/collection/sample:1", "json")
	if err != nil || !json.Valid([]byte(got)) || !strings.HasPrefix(got, "{\n    \"manifest\": {") {
		t.Fatalf("%s %v", got, err)
	}
}

func TestInfoEmptyTagsFailAfterManifest(t *testing.T) {
	r := newFakeRegistry(t)
	r.publish("collection/sample", "1", `{"id":"sample"}`, "")
	got, err := Info(r.resolver().OCI, "tags", r.host()+"/collection/sample:1", "json")
	if !errors.Is(err, ErrInfoUnavailable) || got != "{}\n" {
		t.Fatalf("%s %v", got, err)
	}
	if len(r.requested()) != 2 {
		t.Fatal(r.requested())
	}
}

func TestInfoNonArrayTagsPreserveJSONAndTextHeading(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "tags/list") {
			_, _ = w.Write([]byte(`{"tags":{"a":1}}`))
			return
		}
		_, _ = w.Write([]byte(`{"config":{"mediaType":"application/vnd.devcontainers"}}`))
	}))
	defer s.Close()
	c := oci.NewClient()
	c.HTTP = s.Client()
	id := strings.TrimPrefix(s.URL, "https://") + "/collection/sample"
	got, err := Info(c, "tags", id, "json")
	if err != nil || !strings.Contains(got, `"a": 1`) {
		t.Fatalf("%s %v", got, err)
	}
	got, err = Info(c, "tags", id, "text")
	if err == nil || got != infoBox("Published Tags")+"\n" {
		t.Fatalf("%s %v", got, err)
	}
}
