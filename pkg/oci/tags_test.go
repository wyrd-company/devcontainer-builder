// relationships:
//
//	references: [tags, auth]
package oci

import (
	"fmt"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishedTagsAuthenticatedAndFailures(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Accept") != "application/json" {
			t.Error("wrong Accept")
		}
		if user, password, ok := r.BasicAuth(); !ok || user != "sample" || password != "sample" {
			w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "missing"):
			w.WriteHeader(404)
		case strings.Contains(r.URL.Path, "failed"):
			w.WriteHeader(500)
		case strings.Contains(r.URL.Path, "malformed"):
			fmt.Fprint(w, `{`)
		default:
			fmt.Fprint(w, `{"tags":["latest","1.0.0"]}`)
		}
	}))
	defer server.Close()
	client := NewClient()
	client.HTTP = server.Client()
	client.Auth = &staticAuth{creds: registryauth.Credentials{Username: "sample", Password: "sample"}}
	host := strings.TrimPrefix(server.URL, "https://")
	for _, name := range []string{"sample", "missing", "failed", "malformed"} {
		tags, err := client.PublishedTags(Reference{Registry: host, Repository: "collection/" + name})
		if name == "sample" && (err != nil || len(tags) != 2 || tags[0] != "latest") {
			t.Fatalf("%v %v", tags, err)
		}
		if name == "missing" && (err != nil || len(tags) != 0) {
			t.Fatalf("404: %v %v", tags, err)
		}
		if (name == "failed" || name == "malformed") && err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	if requests != 5 {
		t.Fatalf("requests=%d", requests)
	}
}
