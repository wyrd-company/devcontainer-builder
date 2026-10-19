// relationships:
//
//	references: [builder, generate, ../features/mapping]
package builder

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuilderFeatureMappingControls(t *testing.T) {
	for _, mode := range []string{"generate", "build"} {
		for _, skip := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "-default", true: "-skip"}[skip], func(t *testing.T) {
				features, err := spec.ParseFeatures([]byte(`{"common":{}}`))
				if err != nil {
					t.Fatal(err)
				}
				b := New()
				tr := &featureCapture{}
				b.Resolver.OCI.Auth = nil
				b.Resolver.OCI.HTTP = &http.Client{Transport: tr}
				opts := Options{Spec: &spec.DevContainer{Image: "alpine", Features: features}, WorkDir: t.TempDir(), SkipFeatureAutoMapping: skip}
				if mode == "generate" {
					_, err = b.Generate(context.Background(), opts)
				} else {
					_, _, _, _, _, _, _, _, _, err = b.prepareBuildContext(context.Background(), opts, t.Logf)
				}
				if err == nil {
					t.Fatal("feature transport was not reached")
				}
				want := "https://ghcr.io/v2/devcontainers/features/common-utils/manifests/1"
				if skip {
					want = "https://registry-1.docker.io/v2/library/common/manifests/latest"
				}
				if tr.url != want {
					t.Fatalf("request=%s want=%s", tr.url, want)
				}
			})
		}
	}
}

type featureCapture struct{ url string }

func (c *featureCapture) RoundTrip(req *http.Request) (*http.Response, error) {
	c.url = req.URL.String()
	return nil, os.ErrNotExist
}

func TestBuilderGeneratedMetadataControls(t *testing.T) {
	for _, mode := range []string{"generate", "build"} {
		for _, omit := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "-default", true: "-omit"}[omit], func(t *testing.T) {
				dir := t.TempDir()
				feature := filepath.Join(dir, "sample")
				if err := os.Mkdir(feature, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(feature, "devcontainer-feature.json"), []byte(`{"id":"sample","version":"1.0.0","customizations":{"editor":{"sample":"feature"}}}`), 0644); err != nil {
					t.Fatal(err)
				}
				feats, err := spec.ParseFeatures([]byte(`{"./sample":{}}`))
				if err != nil {
					t.Fatal(err)
				}
				opts := Options{Spec: &spec.DevContainer{Image: "alpine", Path: filepath.Join(dir, "devcontainer.json"), Features: feats, UpdateRemoteEnv: map[string]any{"VALUE": "config"}, Customizations: map[string]any{"editor": map[string]any{"sample": "config"}}}, WorkDir: t.TempDir(), SkipImageMetaResolve: true, OmitSyntaxDirective: omit, Metadata: metadata.CollectOptions{SkipFeatureCustomizations: omit, OmitConfigRemoteEnv: omit}}
				b := New()
				var path string
				if mode == "generate" {
					res, err := b.Generate(context.Background(), opts)
					if err != nil {
						t.Fatal(err)
					}
					path = res.FinalDockerfile
				} else {
					_, _, _, _, _, _, _, p, _, err := b.prepareBuildContext(context.Background(), opts, t.Logf)
					if err != nil {
						t.Fatal(err)
					}
					path = p
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				body := string(data)
				if strings.Contains(body, "# syntax=") == omit {
					t.Fatalf("syntax omission=%v: %s", omit, body)
				}
				_, label, ok := strings.Cut(body, "LABEL "+metadata.Label+"=")
				if !ok {
					t.Fatal("metadata label missing")
				}
				value, err := strconv.Unquote(strings.TrimSpace(label))
				if err != nil {
					t.Fatal(err)
				}
				entries, err := metadata.Parse(value)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 2 || (len(entries[0].Customizations) == 0) != omit || (len(entries[1].RemoteEnv) == 0) != omit || entries[1].Customizations["editor"].(map[string]any)["sample"] != "config" {
					t.Fatalf("metadata omission=%v entries=%+v", omit, entries)
				}

			})
		}
	}
}

func TestPrepareBaseImageSyntaxControl(t *testing.T) {
	dc := &spec.DevContainer{Path: filepath.Join(t.TempDir(), "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Context: "."}}
	for _, omit := range []bool{false, true} {
		_, upstream, err := prepareBaseImage(dc, Options{OmitSyntaxDirective: omit})
		if err != nil {
			t.Fatal(err)
		}
		if upstream.OmitSyntaxDirective != omit {
			t.Fatalf("frontend omit=%t want=%t", upstream.OmitSyntaxDirective, omit)
		}
	}
}
