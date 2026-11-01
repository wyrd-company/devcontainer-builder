// relationships:
//
//	references: [outdated]
package features

import (
	"fmt"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReportNamespaceFilter(t *testing.T) {
	for _, id := range []string{"registry.example/sample:1", "registry.example/sample", "registry.example/sample:latest"} {
		if _, ok := versionReference(id); ok {
			t.Fatalf("accepted %s", id)
		}
	}
	if _, ok := versionReference("registry.example/collection/sample:1"); !ok {
		t.Fatal("rejected namespaced reference")
	}
}

func TestReportNumericPrereleaseRounding(t *testing.T) {
	tags := []string{"1.0.0-999999999999999999999999", "1.0.0-999999999999999999999998"}
	if got := StrictSortedVersions(tags); !reflect.DeepEqual(got, tags) {
		t.Fatalf("numeric ties reordered: %v", got)
	}
	small := []string{"1.0.0-11", "1.0.0-9", "1.0.0-a", "1.0.0"}
	want := []string{"1.0.0-9", "1.0.0-11", "1.0.0-a", "1.0.0"}
	if got := StrictSortedVersions(small); !reflect.DeepEqual(got, want) {
		t.Fatalf("normal precedence: %v", got)
	}
}

func TestReportLockfilePropertySemantics(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "devcontainer.json")
	for _, tc := range []struct {
		name, body string
		want       any
		fail       bool
	}{
		{"unrelated fields", `{"features":{"ID":{"version":"1.0.0","resolved":{},"dependsOn":{}}}}`, "1.0.0", false},
		{"missing features", `{}`, nil, true}, {"null features", `{"features":null}`, nil, true},
		{"null lock", `null`, nil, false}, {"empty", ``, nil, false},
		{"number", `{"features":{"ID":{"version":7}}}`, float64(7), false},
		{"null entry", `{"features":{"ID":null}}`, nil, false},
		{"empty version", `{"features":{"ID":{"version":""}}}`, nil, false},
		{"false version", `{"features":{"ID":{"version":false}}}`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(LockfilePath(path), []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			lock, err := readReportLockfile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := reportLockedVersion(lock, "ID")
			if (err != nil) != tc.fail || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v %v want %v failure=%v", got, err, tc.want, tc.fail)
			}
		})
	}
}

func TestReportTagResponseSemantics(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "devcontainer.json")
	for _, tc := range []struct {
		name, body, want string
		fail             bool
	}{
		{"trailing", `{"tags":["1.0.0"]} trailing`, "", false},
		{"string", `{"tags":"1.0.0"}`, "", true},
		{"object", `{"tags":{}}`, "", true},
		{"mixed", `{"tags":[1,null,"1.0.0",{}]}`, "1.0.0", false},
		{"null", `{"tags":null}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			client := oci.NewClient()
			client.Auth = nil
			client.HTTP = server.Client()
			id := strings.TrimPrefix(server.URL, "https://") + "/collection/sample:1"
			if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"features":{%q:{}}}`, id)), 0600); err != nil {
				t.Fatal(err)
			}
			dc, err := ReadVersionConfig(root, path)
			if err != nil {
				t.Fatal(err)
			}
			report, err := LoadVersionInfo(client, dc)
			if (err != nil) != tc.fail {
				t.Fatalf("error %v failure=%v", err, tc.fail)
			}
			if !tc.fail && report.Features[id].Wanted != tc.want {
				t.Fatalf("wanted %q expected %q", report.Features[id].Wanted, tc.want)
			}
		})
	}
}

func TestReportLockedCurrentValue(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "devcontainer.json")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"tags":["1.2.0"]}`) }))
	defer server.Close()
	client := oci.NewClient()
	client.Auth = nil
	client.HTTP = server.Client()
	id := strings.TrimPrefix(server.URL, "https://") + "/collection/sample:1"
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"features":{%q:{}}}`, id)), 0600); err != nil {
		t.Fatal(err)
	}
	dc, err := ReadVersionConfig(root, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		value string
		want  any
	}{
		{`7`, float64(7)}, {`true`, true}, {`false`, "1.2.0"}, {`0`, "1.2.0"}, {`""`, "1.2.0"}, {`null`, "1.2.0"}, {`"1.0.0"`, "1.0.0"},
	} {
		if err := os.WriteFile(LockfilePath(path), []byte(fmt.Sprintf(`{"features":{%q:{"version":%s,"resolved":{},"dependsOn":{}}}}`, id, tc.value)), 0600); err != nil {
			t.Fatal(err)
		}
		report, err := LoadVersionInfo(client, dc)
		if err != nil {
			t.Fatal(err)
		}
		if report.Features[id].Current != tc.want || report.Features[id].Wanted != "1.2.0" {
			t.Fatalf("%s: %+v", tc.value, report.Features[id])
		}
	}
}
