// relationships:
//   references: [builder, metadata, generate]

package builder

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// privateRegistry requires Basic testuser:testpassword on every request
// and serves a base image (team/base:1) and two features: alpha, which
// dependsOn beta.
type privateRegistry struct {
	srv   *httptest.Server
	paths map[string][]byte
	types map[string]string

	mu     sync.Mutex
	served []string
}

const privateBasic = "testuser:testpassword"

func newPrivateRegistry(t *testing.T) *privateRegistry {
	t.Helper()
	r := &privateRegistry{paths: map[string][]byte{}, types: map[string]string{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.srv.Close)
	host := r.host()

	cfg := []byte(fmt.Sprintf(`{"architecture":%q,"os":"linux","config":{"User":"baseuser","Labels":{"devcontainer.metadata":"[{\"remoteUser\":\"baseuser\"}]"}}}`, runtime.GOARCH))
	r.paths["/v2/team/base/blobs/"+sha(cfg)] = cfg
	r.manifest("team/base", "1", fmt.Sprintf(`{"mediaType":"application/vnd.oci.image.config.v1+json","digest":%q,"size":%d}`, sha(cfg), len(cfg)), "")

	r.feature(t, "team/features/beta", `{"id":"beta","version":"1.0.0"}`)
	r.feature(t, "team/features/alpha", `{"id":"alpha","version":"1.0.0","dependsOn":{"`+host+`/team/features/beta:1":{}}}`)
	return r
}

func (r *privateRegistry) feature(t *testing.T, repo, metadata string) {
	t.Helper()
	layer := featureLayer(t, metadata)
	r.paths["/v2/"+repo+"/blobs/"+sha(layer)] = layer
	r.manifest(repo, "1", `{"mediaType":"application/vnd.devcontainers","digest":"sha256:`+strings.Repeat("0", 64)+`","size":0}`,
		fmt.Sprintf(`{"mediaType":"application/vnd.devcontainers.layer.v1+tar","digest":%q,"size":%d}`, sha(layer), len(layer)))
}

func (r *privateRegistry) manifest(repo, tag, config, layer string) {
	body := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":` + config + `,"layers":[` + layer + `]}`)
	r.paths["/v2/"+repo+"/manifests/"+tag] = body
	r.types["/v2/"+repo+"/manifests/"+tag] = "application/vnd.oci.image.manifest.v1+json"
}

func (r *privateRegistry) serve(w http.ResponseWriter, req *http.Request) {
	if req.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(privateBasic)) {
		w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, ok := r.paths[req.URL.Path]
	if !ok {
		http.NotFound(w, req)
		return
	}
	r.mu.Lock()
	r.served = append(r.served, req.URL.Path)
	r.mu.Unlock()
	if ct := r.types[req.URL.Path]; ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	_, _ = w.Write(body)
}

func (r *privateRegistry) host() string { return strings.TrimPrefix(r.srv.URL, "https://") }

// servedManifests are the repositories whose manifest was served with
// credentials.
func (r *privateRegistry) servedManifests() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := map[string]bool{}
	for _, p := range r.served {
		if repo, _, ok := strings.Cut(strings.TrimPrefix(p, "/v2/"), "/manifests/"); ok {
			out[repo] = true
		}
	}
	return out
}

func (r *privateRegistry) static() registryauth.Static {
	user, pass, _ := strings.Cut(privateBasic, ":")
	return registryauth.Static{r.host(): {Username: user, Password: pass}}
}

func (r *privateRegistry) builder() *Builder {
	b := New()
	b.Resolver.OCI.HTTP = r.srv.Client()
	return b
}

func featureLayer(t *testing.T, metadata string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
		mode       int64
	}{{"./devcontainer-feature.json", metadata, 0o644}, {"./install.sh", "#!/bin/sh\n", 0o755}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Typeflag: tar.TypeReg, Mode: f.mode, Size: int64(len(f.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// markerHelperConfig points DOCKER_CONFIG at a synthetic configuration
// whose credsStore names a marker helper on PATH. The helper answers
// with the registry's credentials and appends each request to the
// returned log, so a test can tell whether the Docker configuration was
// consulted.
func markerHelperConfig(t *testing.T, host string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the marker credential helper is a shell script")
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "requests.log")
	user, pass, _ := strings.Cut(privateBasic, ":")
	script := `#!/bin/sh
[ "$1" = get ] || exit 2
read -r url
printf '%s\n' "$url" >> '` + log + `'
if [ "$url" = '` + host + `' ]; then
  printf '{"Username":"` + user + `","Secret":"` + pass + `"}'
  exit 0
fi
printf 'credentials not found in native keychain'
exit 1
`
	writeFile(t, filepath.Join(bin, "docker-credential-dcbmarker"), script)
	if err := os.Chmod(filepath.Join(bin, "docker-credential-dcbmarker"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.json"), `{"credsStore":"dcbmarker"}`)
	t.Setenv("DOCKER_CONFIG", dir)
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	return log
}

func helperRequests(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func privateWorkspace(t *testing.T, host string) string {
	t.Helper()
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, ".devcontainer", "devcontainer.json"),
		`{"image":"`+host+`/team/base:1","features":{"`+host+`/team/features/alpha:1":{}}}`)
	return ws
}

func generateWith(t *testing.T, b *Builder, ws string, auth func(*Options)) (*Result, error) {
	t.Helper()
	opts := Options{Workspace: ws, WorkDir: t.TempDir()}
	auth(&opts)
	return b.Generate(t.Context(), opts)
}

// The default (nil) source reads the Docker configuration, so the marker
// helper answers. This is the control that lets the injected-source
// tests fail.
func TestGenerate_DefaultRegistryAuthUsesTheDockerConfiguration(t *testing.T) {
	reg := newPrivateRegistry(t)
	log := markerHelperConfig(t, reg.host())
	if _, err := generateWith(t, reg.builder(), privateWorkspace(t, reg.host()), func(*Options) {}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(helperRequests(t, log)) == 0 {
		t.Fatal("the marker helper was not run for the default source")
	}
}

func TestGenerate_RegistryAuthReachesFeaturesDependenciesAndBaseImage(t *testing.T) {
	reg := newPrivateRegistry(t)
	log := markerHelperConfig(t, reg.host())
	res, err := generateWith(t, reg.builder(), privateWorkspace(t, reg.host()), func(o *Options) { o.RegistryAuth = reg.static() })
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if r := helperRequests(t, log); len(r) != 0 {
		t.Fatalf("the marker helper was run for %v", r)
	}
	served := reg.servedManifests()
	for _, repo := range []string{"team/features/alpha", "team/features/beta", "team/base"} {
		if !served[repo] {
			t.Errorf("%s manifest not fetched with the injected credentials; served %v", repo, served)
		}
	}
	df, err := os.ReadFile(res.FinalDockerfile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(df), "baseuser") {
		t.Errorf("generated Dockerfile does not carry the private base image user:\n%s", df)
	}
}

func TestGenerate_EmptyStaticIsAnonymousWithoutFallback(t *testing.T) {
	reg := newPrivateRegistry(t)
	log := markerHelperConfig(t, reg.host())
	_, err := generateWith(t, reg.builder(), privateWorkspace(t, reg.host()), func(o *Options) { o.RegistryAuth = registryauth.Static{} })
	if err == nil || !strings.Contains(err.Error(), "requires credentials") {
		t.Fatalf("Generate with no credentials: err = %v, want a missing-credentials error", err)
	}
	if r := helperRequests(t, log); len(r) != 0 {
		t.Fatalf("the marker helper was run for %v", r)
	}
}

func TestBaseMetadata_UsesRegistryAuth(t *testing.T) {
	reg := newPrivateRegistry(t)
	log := markerHelperConfig(t, reg.host())
	dc := &spec.DevContainer{Image: reg.host() + "/team/base:1"}
	entries, err := reg.builder().BaseMetadata(t.Context(), dc, Options{RegistryAuth: reg.static()})
	if err != nil {
		t.Fatalf("BaseMetadata: %v", err)
	}
	if len(entries) != 1 || entries[0].RemoteUser != "baseuser" {
		t.Fatalf("BaseMetadata = %+v, want the private image's entry", entries)
	}
	if r := helperRequests(t, log); len(r) != 0 {
		t.Fatalf("the marker helper was run for %v", r)
	}
}

// Concurrent calls on one Builder each keep their own source and token
// cache, and leave the Builder's Resolver unchanged.
func TestGenerate_RegistryAuthIsPerCall(t *testing.T) {
	reg := newPrivateRegistry(t)
	markerHelperConfig(t, reg.host())
	b := reg.builder()
	shared, sharedAuth := b.Resolver.OCI, b.Resolver.OCI.Auth
	ws := privateWorkspace(t, reg.host())

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			auth := registryauth.Static{}
			if i%2 == 0 {
				auth = reg.static()
			}
			_, errs[i] = b.Generate(t.Context(), Options{Workspace: ws, WorkDir: t.TempDir(), RegistryAuth: auth,
				Logger: func(string, ...any) {}})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if i%2 == 0 && err != nil {
			t.Errorf("call %d with credentials: %v", i, err)
		}
		if i%2 == 1 && (err == nil || !strings.Contains(err.Error(), "requires credentials")) {
			t.Errorf("call %d without credentials: err = %v, want a missing-credentials error", i, err)
		}
	}
	if b.Resolver.OCI != shared || b.Resolver.OCI.Auth != sharedAuth || b.Resolver.Logger != nil {
		t.Fatal("Generate changed the Builder's Resolver")
	}
}

func TestSolveOptions_ForwardsRegistryAuth(t *testing.T) {
	auth := registryauth.Static{"registry.example.test": {Username: "testuser"}}
	got := solveOptions(Options{RegistryAuth: auth}, t.TempDir(), &spec.DevContainer{})
	if s, ok := got.RegistryAuth.(registryauth.Static); !ok || s["registry.example.test"].Username != "testuser" {
		t.Fatalf("SolveOptions.RegistryAuth = %#v, want the injected source", got.RegistryAuth)
	}
	if got := solveOptions(Options{}, t.TempDir(), &spec.DevContainer{}); got.RegistryAuth != nil {
		t.Fatalf("SolveOptions.RegistryAuth = %#v, want nil for the BuildKit default", got.RegistryAuth)
	}
}
