// relationships:
//
//	references: [mapping, registry_test]
package features

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the reference's actual mapping function without its TypeScript-only
// parameter annotations. No upstream dependencies or transpiler are required.
func TestFeatureMappingDifferential(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("optional upstream differential test requires Node")
	}
	source, err := os.ReadFile("../../reference/cli/src/spec-configuration/containerFeaturesConfiguration.ts")
	if os.IsNotExist(err) {
		t.Skip("reference submodule is not initialized")
	}
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "export function getBackwardCompatibleFeatureId(")
	if start < 0 {
		t.Fatal("upstream mapping function missing")
	}
	text = strings.TrimPrefix(text[start:], "export ")
	end := strings.Index(text, "\n}\n")
	if end < 0 {
		t.Fatal("upstream mapping function end missing")
	}
	fn := strings.Replace(text[:end+2], "output: Log, id: string", "output, id", 1)
	inputs := []string{"aws-cli", "azure-cli", "desktop-lite", "docker-in-docker", "docker-from-docker", "dotnet", "git", "git-lfs", "github-cli", "java", "kubectl-helm-minikube", "node", "powershell", "python", "ruby", "rust", "sshd", "terraform", "golang", "common", "gradle", "maven", "jupyterlab", "fish", "./common", "node:1", "ghcr.io/devcontainers/features/node:2", "registry.example/collection/sample:1"}
	raw, _ := json.Marshal(inputs)
	script := "const LogLevel={Warning:0};\n" + fn + "\nconsole.log(JSON.stringify(" + string(raw) + ".map(id=>getBackwardCompatibleFeatureId({write(){}},id))))"
	output, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("reference mapping: %v: %s", err, output)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	r := New()
	for i, id := range inputs {
		if got := r.featureIdentifier(id); got != want[i] {
			t.Errorf("%s: got %s want %s", id, got, want[i])
		}
		r.SkipAutoMapping = true
		if got := r.featureIdentifier(id); got != id {
			t.Errorf("skip %s: got %s", id, got)
		}
		r.SkipAutoMapping = false
	}
}

type mappingTransport struct {
	target string
	next   http.RoundTripper
}

func (m mappingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Host = m.target
	clone.Host = m.target
	return m.next.RoundTrip(clone)
}

func TestResolveFeatureAutoMapping(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "mapped", true: "skipped"}[skip], func(t *testing.T) {
			reg := newFakeRegistry(t)
			reg.publish("devcontainers/features/common-utils", "1", `{"id":"sample","version":"1.0.0"}`, "")
			reg.publish("library/common", "latest", `{"id":"sample","version":"1.0.0"}`, "")
			r := reg.resolver()
			r.SkipAutoMapping = skip
			r.OCI.Auth = nil
			r.OCI.HTTP.Transport = mappingTransport{target: reg.host(), next: r.OCI.HTTP.Transport}
			got, err := r.Resolve(userFeatures("common", `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir())
			if skip {
				if err == nil || !strings.Contains(err.Error(), "legacy feature") || len(reg.requested()) != 0 {
					t.Fatalf("unmapped legacy feature err=%v requests=%v", err, reg.requested())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "ghcr.io/devcontainers/features/common-utils:1"
			if len(got) != 1 || got[0].Source != "common" || got[0].Reference.String() != want {
				t.Fatalf("resolved=%+v, want reference %s and original source", got, want)
			}
			if len(reg.requested()) == 0 {
				t.Fatal("registry was not reached")
			}
		})
	}
}

func TestResolveRejectsBareLegacyIdentifiers(t *testing.T) {
	for _, id := range []string{"sample", "node:1", "gradle:1", "Maven"} {
		t.Run(id, func(t *testing.T) {
			reg := newFakeRegistry(t)
			r := reg.resolver()
			r.OCI.Auth = nil
			r.OCI.HTTP.Transport = mappingTransport{target: reg.host(), next: r.OCI.HTTP.Transport}
			_, err := r.Resolve(userFeatures(id, `{}`), nil, t.TempDir(), t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "legacy feature") || len(reg.requested()) != 0 {
				t.Fatalf("legacy reference err=%v requests=%v", err, reg.requested())
			}
		})
	}
}
