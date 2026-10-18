// relationships:
//   references: [imageuser, metadata]

package builder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// imageRegistry serves one single-platform image whose config sets User.
type imageRegistry struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests int
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// newImageRegistry serves repo:tag. With one user the tag is a single
// manifest; with "amd64=..." and "arm64=..." users it is a two-platform
// index.
func newImageRegistry(t *testing.T, repo, tag string, users ...string) *imageRegistry {
	t.Helper()
	paths := map[string][]byte{}
	manifestFor := func(arch, user string) []byte {
		cfg := []byte(fmt.Sprintf(`{"architecture":%q,"os":"linux","config":{"User":%q}}`, arch, user))
		paths["/v2/"+repo+"/blobs/"+sha(cfg)] = cfg
		return []byte(fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d},"layers":[]}`, sha(cfg), len(cfg)))
	}
	indexType := ""
	if len(users) == 1 {
		paths["/v2/"+repo+"/manifests/"+tag] = manifestFor("amd64", users[0])
	} else {
		var children []string
		for _, u := range users {
			arch, user, _ := strings.Cut(u, "=")
			m := manifestFor(arch, user)
			paths["/v2/"+repo+"/manifests/"+sha(m)] = m
			children = append(children, fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":%q,"size":%d,"platform":{"os":"linux","architecture":%q}}`, sha(m), len(m), arch))
		}
		paths["/v2/"+repo+"/manifests/"+tag] = []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[` + strings.Join(children, ",") + `]}`)
		indexType = "application/vnd.oci.image.index.v1+json"
	}
	r := &imageRegistry{}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests++
		r.mu.Unlock()
		body, ok := paths[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		if req.URL.Path == "/v2/"+repo+"/manifests/"+tag && indexType != "" {
			w.Header().Set("Content-Type", indexType)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *imageRegistry) host() string { return strings.TrimPrefix(r.srv.URL, "https://") }

func (r *imageRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests
}

func (r *imageRegistry) builder() *Builder {
	b := New()
	b.Resolver.OCI = oci.NewClient()
	b.Resolver.OCI.HTTP = r.srv.Client()
	return b
}

// writeWorkspace writes a devcontainer.json (with base being either an
// "image" or a "build" member) and a local feature.
func writeWorkspace(t *testing.T, base string, extra map[string]string) string {
	t.Helper()
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, ".devcontainer", "devcontainer.json"),
		`{`+base+`,"features":{"./marker":{}}}`)
	writeFile(t, filepath.Join(ws, ".devcontainer", "marker", "devcontainer-feature.json"), `{"id":"marker","version":"1.0.0"}`)
	writeFile(t, filepath.Join(ws, ".devcontainer", "marker", "install.sh"), "#!/bin/sh\n")
	for rel, content := range extra {
		writeFile(t, filepath.Join(ws, ".devcontainer", rel), content)
	}
	return ws
}

func generate(t *testing.T, b *Builder, ws string, mutate ...func(*Options)) (string, []string) {
	t.Helper()
	var logs []string
	opts := Options{
		Workspace: ws,
		WorkDir:   t.TempDir(),
		Logger:    func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) },
	}
	for _, m := range mutate {
		m(&opts)
	}
	res, err := b.Generate(context.Background(), opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	df, err := os.ReadFile(res.FinalDockerfile)
	if err != nil {
		t.Fatal(err)
	}
	return string(df), logs
}

func wantBuiltinUser(t *testing.T, dockerfile, user string) {
	t.Helper()
	want := features.BuiltinEnvContent(user, user)
	if !strings.Contains(dockerfile, want) {
		t.Fatalf("Dockerfile builtins do not name %q:\n%s", user, dockerfile)
	}
}

func TestGenerate_FeaturesSeeImageSpecBaseUser(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"image":"`+reg.host()+`/library/base:1"`, nil)
	df, _ := generate(t, reg.builder(), ws)
	wantBuiltinUser(t, df, "image-user")
}

func TestGenerate_FeaturesSeeDockerfileFromImageUser(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"build":{"dockerfile":"Dockerfile"}`, map[string]string{
		"Dockerfile": "FROM " + reg.host() + "/library/base:1\nRUN true\n",
	})
	df, _ := generate(t, reg.builder(), ws)
	wantBuiltinUser(t, df, "image-user")
}

func TestGenerate_FeaturesSeeDockerfileUser(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"build":{"dockerfile":"Dockerfile","args":{"NAME":"arg-user"}}`, map[string]string{
		"Dockerfile": "FROM " + reg.host() + "/library/base:1\nARG NAME\nUSER $NAME\n",
	})
	df, _ := generate(t, reg.builder(), ws)
	wantBuiltinUser(t, df, "arg-user")
	if n := reg.count(); n != 2 {
		t.Fatalf("registry requests = %d, want only the inherited label manifest/config read", n)
	}
}

func TestGenerate_BaseUserLookupFailureFallsBackToRoot(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"image":"`+reg.host()+`/library/missing:1"`, nil)
	df, logs := generate(t, reg.builder(), ws, func(o *Options) { o.Docker = metadataEngine(t, nil) })
	wantBuiltinUser(t, df, "root")
	if !strings.Contains(strings.Join(logs, "\n"), "base image user unknown") {
		t.Fatalf("missing warning in logs: %v", logs)
	}
}

func TestGenerate_SkipImageMetaResolveSkipsBaseUserLookup(t *testing.T) {
	t.Setenv("DOCKER_HOST", "invalid-default-engine")
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"image":"`+reg.host()+`/library/base:1"`, nil)
	df, _ := generate(t, reg.builder(), ws, func(o *Options) { o.SkipImageMetaResolve = true })
	wantBuiltinUser(t, df, "root")
	if n := reg.count(); n != 0 {
		t.Fatalf("registry requests = %d, want none", n)
	}
}

// Build writes the same Dockerfile through prepareBuildContext.
func TestPrepareBuildContext_FeaturesSeeImageSpecBaseUser(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"image":"`+reg.host()+`/library/base:1"`, nil)
	_, _, _, _, _, _, _, dfPath, _, err := reg.builder().prepareBuildContext(context.Background(), Options{
		Workspace: ws,
		WorkDir:   t.TempDir(),
	}, func(string, ...any) {})
	if err != nil {
		t.Fatalf("prepareBuildContext: %v", err)
	}
	df, err := os.ReadFile(dfPath)
	if err != nil {
		t.Fatal(err)
	}
	wantBuiltinUser(t, string(df), "image-user")
}

func TestGenerate_BaseUserForPrimaryPlatform(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "amd64=amd-user", "arm64=arm-user")
	ws := writeWorkspace(t, `"image":"`+reg.host()+`/library/base:1"`, nil)
	for platform, user := range map[string]string{"linux/arm64": "arm-user", "linux/amd64": "amd-user"} {
		df, _ := generate(t, reg.builder(), ws, func(o *Options) { o.Platforms = []string{platform, "linux/s390x"} })
		wantBuiltinUser(t, df, user)
	}
}

func TestGenerate_RestoresBaseUserWithoutFeatures(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"image":"`+reg.host()+`/library/base:1"`, nil)
	df, _ := generate(t, reg.builder(), ws, func(o *Options) { o.SkipFeatures = true })
	if !strings.Contains(df, "USER image-user\n") {
		t.Fatalf("Dockerfile did not restore base user without features:\n%s", df)
	}
}

func TestGenerate_FeaturesSeeUserFromInheritedStageEnv(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"build":{"dockerfile":"Dockerfile"}`, map[string]string{
		"Dockerfile": "FROM " + reg.host() + "/library/base:1 AS parent\nENV ACCOUNT=nobody\nFROM parent\nUSER ${ACCOUNT}\n",
	})
	df, _ := generate(t, reg.builder(), ws)
	wantBuiltinUser(t, df, "nobody")
}

func TestGenerate_FeaturesSeeUserFromDependentArgDefault(t *testing.T) {
	reg := newImageRegistry(t, "library/base", "1", "image-user")
	ws := writeWorkspace(t, `"build":{"dockerfile":"Dockerfile"}`, map[string]string{
		"Dockerfile": "FROM " + reg.host() + "/library/base:1\nARG ACCOUNT=nobody\nARG CHOSEN=${ACCOUNT}\nUSER ${CHOSEN}\n",
	})
	df, _ := generate(t, reg.builder(), ws)
	wantBuiltinUser(t, df, "nobody")
}

func TestGeneratedDockerfilesRestoreBaseImageUser(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepare=%v", prepare), func(t *testing.T) {
			reg := newImageRegistry(t, "library/base", "1", "image-user")
			opts := Options{
				Spec:    &spec.DevContainer{Image: reg.host() + "/library/base:1", RemoteUser: "remote-sample", ContainerUser: "root"},
				WorkDir: t.TempDir(),
			}
			var path string
			if prepare {
				_, _, _, _, _, _, _, dfPath, _, err := reg.builder().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
				if err != nil {
					t.Fatal(err)
				}
				path = dfPath
			} else {
				res, err := reg.builder().Generate(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
				path = res.FinalDockerfile
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "USER image-user\n") {
				t.Fatalf("generated Dockerfile did not restore base image user:\n%s", body)
			}
		})
	}
}
