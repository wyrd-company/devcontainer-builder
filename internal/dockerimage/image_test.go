// relationships:
//
//	references: [image]
package dockerimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"
)

func TestInspectConfig(t *testing.T) {
	for _, mode := range []string{"local", "registry", "pull", "pull-error", "inspect-error"} {
		t.Run(mode, func(t *testing.T) {
			config := `{"config":{"Labels":{"sample":"metadata"},"Entrypoint":["/entry"],"Cmd":["run"]}}`
			sum := sha256.Sum256([]byte(config))
			digest := "sha256:" + hex.EncodeToString(sum[:])
			registryRequests := 0
			registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				registryRequests++
				if mode != "registry" {
					http.Error(w, "unavailable", 500)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
				if strings.Contains(r.URL.Path, "/blobs/") {
					fmt.Fprint(w, config)
					return
				}
				fmt.Fprintf(w, `{"schemaVersion":2,"config":{"digest":%q}}`, digest)
			}))
			defer registry.Close()
			// OCI uses the default transport; trust only this test server.
			previous := http.DefaultTransport
			http.DefaultTransport = registry.Client().Transport
			defer func() { http.DefaultTransport = previous }()
			inspections, pulls := 0, 0
			engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/info"):
					fmt.Fprint(w, `{"OSType":"linux","Architecture":"amd64"}`)
				case strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
					inspections++
					if mode == "local" || (pulls > 0 && mode == "pull") {
						fmt.Fprint(w, `{"Config":{"Labels":{"sample":"metadata"},"Entrypoint":["/entry"],"Cmd":["run"]}}`)
					} else {
						w.WriteHeader(404)
						fmt.Fprint(w, `{"message":"missing"}`)
					}
				case strings.HasSuffix(r.URL.Path, "/images/create"):
					pulls++
					if r.Header.Get("X-Registry-Auth") != "test-auth" {
						t.Error("missing pull credentials")
					}
					if mode == "pull-error" {
						fmt.Fprintln(w, `{"errorDetail":{"message":"pull failed"},"error":"pull failed"}`)
					} else {
						fmt.Fprintln(w, `{"status":"done"}`)
					}
				default:
					t.Errorf("unexpected engine request: %s", r.URL.Path)
				}
			}))
			defer engine.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+strings.TrimPrefix(engine.URL, "http://")), dockerclient.WithVersion("1.45"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			cfg, err := InspectConfig(context.Background(), cli, strings.TrimPrefix(registry.URL, "https://")+"/sample:latest", "linux/amd64", testAuth{}, nil)
			if mode == "pull-error" || mode == "inspect-error" {
				if err == nil {
					t.Fatal("expected failure")
				}
			} else if err != nil || cfg == nil || cfg.Labels["sample"] != "metadata" || len(cfg.Entrypoint) != 1 || len(cfg.Cmd) != 1 {
				t.Fatalf("config = %+v, err = %v", cfg, err)
			}
			wantPulls, wantInspections := 0, 1
			if mode == "pull" || mode == "inspect-error" {
				wantPulls, wantInspections = 1, 2
			}
			if mode == "pull-error" {
				wantPulls = 5
			}
			if pulls != wantPulls || inspections != wantInspections {
				t.Fatalf("pulls=%d inspections=%d; want %d %d", pulls, inspections, wantPulls, wantInspections)
			}
			if mode == "local" && registryRequests != 0 {
				t.Fatal("local inspect contacted registry")
			}
		})
	}
}

type testAuth struct{}

func (testAuth) EngineAuth(string) (string, error) { return "test-auth", nil }

func TestPull_KeepsTheCredentialErrorTyped(t *testing.T) {
	sentinel := errors.New("synthetic credential source failure")
	err := Pull(context.Background(), nil, "registry.example.test/team/app:1", failingEngineAuth{sentinel}, nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Pull: err = %v, want the credential source's error", err)
	}
}

type failingEngineAuth struct{ err error }

func (a failingEngineAuth) EngineAuth(string) (string, error) { return "", a.err }
