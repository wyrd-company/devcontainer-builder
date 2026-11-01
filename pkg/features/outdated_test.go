// relationships:
//
//	references: [outdated]
package features

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

func TestVersionReportRegistry(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	metadata := []byte(`{"id":"sample","version":"1.3.0"}`)
	_ = tw.WriteHeader(&tar.Header{Name: "devcontainer-feature.json", Mode: 0644, Size: int64(len(metadata))})
	_, _ = tw.Write(metadata)
	_ = tw.Close()
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive.Bytes()))
	requests := []string{}
	manifestBody := func(fallback bool) []byte {
		annotation := `{"version":"1.2.0"}`
		if fallback {
			annotation = ""
		}
		body, _ := json.Marshal(map[string]any{"schemaVersion": 2, "config": map[string]any{"mediaType": "application/vnd.devcontainers"}, "annotations": map[string]string{"dev.containers.metadata": annotation}, "layers": []any{map[string]any{"digest": digest, "size": archive.Len()}}})
		return body
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "tags/list") {
			if strings.Contains(r.URL.Path, "failed") {
				w.WriteHeader(500)
				return
			}
			if strings.Contains(r.URL.Path, "malformed") {
				fmt.Fprint(w, `{`)
				return
			}
			fmt.Fprint(w, `{"tags":["2.0.0","1.1.0","latest","1","1.3.0","3.0.0-beta.1","v1.2.0","01.2.3"]}`)
			return
		}
		if strings.Contains(r.URL.Path, "blobs/") {
			_, _ = w.Write(archive.Bytes())
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write(manifestBody(strings.Contains(r.URL.Path, "fallback")))
	}))
	defer server.Close()
	client := oci.NewClient()
	client.HTTP = server.Client()
	root := t.TempDir()
	host := strings.TrimPrefix(server.URL, "https://")
	ids := []string{host + "/collection/sample:1", host + "/collection/sample:latest", host + fmt.Sprintf("/collection/digest@sha256:%x", sha256.Sum256(manifestBody(false))), host + fmt.Sprintf("/collection/fallback@sha256:%x", sha256.Sum256(manifestBody(true))), host + "/collection/failed:1", host + "/collection/malformed:1"}
	var config bytes.Buffer
	config.WriteString(`{"features":{`)
	for i, id := range ids {
		if i > 0 {
			config.WriteByte(',')
		}
		fmt.Fprintf(&config, "%q:{}", id)
	}
	config.WriteString(`,"./local":{},"deprecated":{},"https://example.invalid/sample.tgz":{}}}`)
	path := filepath.Join(root, "devcontainer.json")
	_ = os.WriteFile(path, config.Bytes(), 0600)
	lock := fmt.Sprintf(`{"features":{%q:{"version":"1.0.0"}}}`, ids[0])
	_ = os.WriteFile(LockfilePath(path), []byte(lock), 0600)
	dc, err := ReadVersionConfig(root, path)
	if err != nil {
		t.Fatal(err)
	}
	report, err := LoadVersionInfo(client, dc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Order, ids) {
		t.Fatalf("order: %v", report.Order)
	}
	expected := []FeatureVersionInfo{{"1.0.0", "1.3.0", "1", "3.0.0-beta.1", "3"}, {"3.0.0-beta.1", "3.0.0-beta.1", "3", "3.0.0-beta.1", "3"}, {"1.2.0", "1.2.0", "1", "3.0.0-beta.1", "3"}, {"1.3.0", "1.3.0", "1", "3.0.0-beta.1", "3"}, {}, {}}
	for i, id := range ids {
		if got := report.Features[id]; got != expected[i] {
			t.Errorf("%s: %+v want %+v", id, got, expected[i])
		}
	}
	after, _ := os.ReadFile(LockfilePath(path))
	if string(after) != lock {
		t.Fatal("lockfile changed")
	}
	body, _ := json.Marshal(report)
	if !strings.HasSuffix(string(body), `"`+ids[5]+`":{}}}`) {
		t.Fatalf("JSON order/omission %s", body)
	}
	if len(requests) != 9 {
		t.Fatalf("unexpected registry requests: %v", requests)
	}
}

func TestStrictSortedVersions(t *testing.T) {
	got := StrictSortedVersions([]string{"1", "1.2", "latest", "01.0.0", "1.0.0", "v1.0.0", "1.0.0+build", "1.0.0-alpha.1", "2.0.0"})
	want := []string{"1.0.0-alpha.1", "1.0.0", "v1.0.0", "1.0.0+build", "2.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestVersionConfigDiscoveryAndErrors(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".devcontainer.json")
	_ = os.WriteFile(path, []byte(`{"features":{"./local":{},"./local":false}}`), 0600)
	dc, err := ReadVersionConfig(root, "")
	if err != nil || dc.Features.Len() != 1 {
		t.Fatalf("%+v %v", dc, err)
	}
	_ = os.WriteFile(LockfilePath(path), []byte(`bad`), 0600)
	if _, err = LoadVersionInfo(nil, dc); err == nil {
		t.Fatal("accepted malformed lockfile")
	}
	for _, body := range []string{`{"features":[]}`, `{"features":false}`, `{"features":"sample"}`} {
		_ = os.WriteFile(path, []byte(body), 0600)
		dc, err := ReadVersionConfig(root, "")
		if err != nil || dc.Features.Len() != 0 {
			t.Fatalf("%s: %v %+v", body, err, dc)
		}
	}
	for _, body := range []string{`null`, `[]`} {
		_ = os.WriteFile(path, []byte(body), 0600)
		if _, err := ReadVersionConfig(root, ""); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}

func TestDigestVersionFailuresAndMetadataRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, annotation  string
		layers, badDigest bool
		want              string
		fail              bool
	}{
		{name: "annotation", annotation: `{"version":"1.2.0"}`, want: "1.2.0"},
		{name: "missing version", annotation: `{}`},
		{name: "null", annotation: `null`},
		{name: "array", annotation: `[]`},
		{name: "broken", annotation: `{`},
		{name: "broken suffix", annotation: `{"version":"1.2.0",`, want: "1.2.0"},
		{name: "bad digest", annotation: `{"version":"1.2.0"}`, badDigest: true, fail: true},
		{name: "missing layer", fail: true},
		{name: "failed blob", layers: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := map[string]any{"schemaVersion": 2, "config": map[string]string{"mediaType": "application/vnd.devcontainers"}, "annotations": map[string]string{"dev.containers.metadata": tc.annotation}}
			if tc.layers {
				obj["layers"] = []any{map[string]any{"digest": "sha256:" + strings.Repeat("a", 64)}}
			}
			body, _ := json.Marshal(obj)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "blobs/") {
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
				_, _ = w.Write(body)
			}))
			defer server.Close()
			client := oci.NewClient()
			client.HTTP = server.Client()
			client.Auth = nil
			digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
			if tc.badDigest {
				digest = "sha256:" + strings.Repeat("a", 64)
			}
			got, err := digestVersion(client, oci.Reference{Registry: strings.TrimPrefix(server.URL, "https://"), Repository: "collection/sample", Digest: digest})
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("got %q %v want %q failure=%v", got, err, tc.want, tc.fail)
			}
		})
	}
}

func TestVersionConfigSubstitutionValuesOnly(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "devcontainer.json")
	for _, tc := range []struct {
		body string
		fail bool
	}{
		{`{"features":{"${localEnv}":{}}}`, false},
		{`{"name":"${localEnv:UNSET:sample}","features":{}}`, false},
		{`{"features":{"./local":{}},`, false},
		{`{"name":"${localEnv}","features":{}}`, true},
		{`{"features":{"registry.example/collection/sample:1":{"option":"${env}"}}}`, true},
	} {
		_ = os.WriteFile(path, []byte(tc.body), 0600)
		_, err := ReadVersionConfig(root, path)
		if (err != nil) != tc.fail {
			t.Fatalf("%s: %v", tc.body, err)
		}
	}
}
