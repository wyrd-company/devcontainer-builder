// relationships:
//
//	references: [options_feature_controls, stage]
package cli

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestFeatureControlRegistration(t *testing.T) {
	root := (&application{}).command()
	pairs := 0
	for _, cmd := range root.Commands() {
		for _, flag := range []string{"skip-feature-auto-mapping", "skip-persisting-customizations-from-features", "omit-config-remote-env-from-metadata", "omit-syntax-directive"} {
			want := flag == "skip-feature-auto-mapping" && (cmd.Name() == "build" || cmd.Name() == "up" || cmd.Name() == "exec" || cmd.Name() == "read-configuration" || cmd.Name() == "run-user-commands") || flag == "skip-persisting-customizations-from-features" && cmd.Name() == "build" || flag == "omit-config-remote-env-from-metadata" && cmd.Name() == "up" || flag == "omit-syntax-directive" && (cmd.Name() == "build" || cmd.Name() == "up")
			f := cmd.Flags().Lookup(flag)
			if (f != nil) != want {
				t.Errorf("%s --%s present=%v want=%v", cmd.Name(), flag, f != nil, want)
			}
			if want && f != nil {
				pairs++
				if f.Value.Type() != "bool" || f.DefValue != "false" || !f.Hidden {
					t.Errorf("%s --%s = %+v", cmd.Name(), flag, f)
				}
			}
		}
	}
	if pairs != 9 {
		t.Fatalf("option pairs=%d want 9", pairs)
	}
}

func TestBuildFeatureControlBinding(t *testing.T) {
	for _, name := range []string{"build", "up"} {
		t.Run(name, func(t *testing.T) {
			var opts buildOptions
			cmd := &cobra.Command{Use: name}
			opts.flags(cmd, false)
			flags := []string{"--skip-feature-auto-mapping", "--omit-syntax-directive"}
			if name == "build" {
				flags = append(flags, "--skip-persisting-customizations-from-features")
			} else {
				flags = append(flags, "--omit-config-remote-env-from-metadata")
			}
			if err := cmd.ParseFlags(flags); err != nil {
				t.Fatal(err)
			}
			got, err := opts.prepare(&application{}, &spec.DevContainer{Image: "alpine"}, t.TempDir(), false)
			if err != nil {
				t.Fatal(err)
			}
			if !got.SkipFeatureAutoMapping || !got.OmitSyntaxDirective || got.Metadata.SkipFeatureCustomizations != (name == "build") || got.Metadata.OmitConfigRemoteEnv != (name == "up") {
				t.Fatalf("builder options=%+v", got)
			}
		})
	}
}

type captureFeatureRequest struct{ host, path string }

func (c *captureFeatureRequest) RoundTrip(req *http.Request) (*http.Response, error) {
	c.host, c.path = req.URL.Host, req.URL.Path
	return nil, errors.New("test stopped feature acquisition")
}

func TestNonBuildFeatureMappingBinding(t *testing.T) {
	// The real resolver's default HTTP boundary sees the requested identifier.
	// No external registry or credentials are used.
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	for _, name := range []string{"read-configuration", "run-user-commands", "exec"} {
		for _, skip := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "-default", true: "-skip"}[skip], func(t *testing.T) {
				a := &application{}
				root := a.command()
				cmd, _, err := root.Find([]string{name})
				if err != nil {
					t.Fatal(err)
				}
				if skip {
					if err := cmd.ParseFlags([]string{"--skip-feature-auto-mapping"}); err != nil {
						t.Fatal(err)
					}
				}
				capture := &captureFeatureRequest{}
				http.DefaultTransport = capture
				features, err := spec.ParseFeatures([]byte(`{"common":{}}`))
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = a.resolveFeatures(&spec.DevContainer{Features: features, Path: filepath.Join(t.TempDir(), "devcontainer.json")})
				if err == nil {
					t.Fatal("feature resolution unexpectedly succeeded")
				}
				wantHost, wantPath := "ghcr.io", "/v2/devcontainers/features/common-utils/manifests/1"
				if skip {
					wantHost, wantPath = "", ""
					if !strings.Contains(err.Error(), "legacy feature") {
						t.Fatalf("expected upstream legacy rejection: %v", err)
					}
				}
				if capture.host != wantHost || capture.path != wantPath {
					t.Fatalf("request=%s%s want=%s%s", capture.host, capture.path, wantHost, wantPath)
				}
			})
		}
	}
}
