package dockertest

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"golang.org/x/crypto/bcrypt"
)

// Generic credentials the authenticated test registry accepts.
const (
	RegistryUsername = "testuser"
	RegistryPassword = "testpassword"
)

// AuthRegistry is a test-owned registry that serves TLS with a
// self-signed certificate and requires Basic credentials.
type AuthRegistry struct {
	// Addr is the registry as the daemon reaches it, "127.0.0.1:<port>".
	// The daemon treats it as an insecure registry, so it accepts the
	// certificate.
	Addr string
	// Client is an HTTP client that trusts the registry certificate.
	Client *http.Client
}

// StartAuthRegistry runs an AuthRegistry container on the daemon. The
// container is removed when the test ends.
func StartAuthRegistry(t testing.TB, ctx context.Context, cli *dockerclient.Client) *AuthRegistry {
	t.Helper()
	EnsureImage(t, ctx, cli, RegistryImage)
	certPEM, keyPEM := selfSignedCert(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(RegistryPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	files := tarFiles(t, map[string][]byte{
		"certs/registry.crt": certPEM,
		"certs/registry.key": keyPEM,
		"auth/htpasswd":      []byte(RegistryUsername + ":" + string(hash) + "\n"),
	})

	name := fmt.Sprintf("dcb-test-auth-registry-%d", time.Now().UnixNano())
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{
			Image:        RegistryImage,
			ExposedPorts: nat.PortSet{"5000/tcp": {}},
			Env: []string{
				"REGISTRY_AUTH=htpasswd",
				"REGISTRY_AUTH_HTPASSWD_REALM=dcb-test",
				"REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd",
				"REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.crt",
				"REGISTRY_HTTP_TLS_KEY=/certs/registry.key",
			},
		},
		&container.HostConfig{PortBindings: nat.PortMap{"5000/tcp": {{HostIP: "127.0.0.1"}}}},
		nil, nil, name)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	t.Cleanup(func() {
		if err := cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
			t.Logf("remove registry %s: %v", name, err)
		}
	})
	if err := cli.CopyToContainer(ctx, resp.ID, "/", bytes.NewReader(files), container.CopyToContainerOptions{}); err != nil {
		t.Fatalf("copy registry files: %v", err)
	}
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("start registry: %v", err)
	}
	inspect, err := cli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		t.Fatalf("inspect registry: %v", err)
	}
	bindings := inspect.NetworkSettings.Ports["5000/tcp"]
	if len(bindings) == 0 || bindings[0].HostPort == "" {
		t.Fatalf("registry port not published: %+v", inspect.NetworkSettings.Ports)
	}
	waitForLog(t, ctx, cli, resp.ID, "listening on")

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	return &AuthRegistry{
		Addr: "127.0.0.1:" + bindings[0].HostPort,
		Client: &http.Client{
			Timeout:   time.Minute,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		},
	}
}

// DockerConfig writes a Docker client configuration directory whose
// config.json holds cfg, and returns it. It clears DOCKER_AUTH_CONFIG so
// that only the written file applies.
func DockerConfig(t testing.TB, cfg string) string {
	t.Helper()
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// StaticAuthConfig writes a Docker client configuration with a static
// auths entry for the registry, and returns its directory.
func (r *AuthRegistry) StaticAuthConfig(t testing.TB) string {
	t.Helper()
	auth := base64.StdEncoding.EncodeToString([]byte(RegistryUsername + ":" + RegistryPassword))
	return DockerConfig(t, `{"auths":{"`+r.Addr+`":{"auth":"`+auth+`"}}}`)
}

// HelperStderrMarker is what the fake credential helper writes to its
// stderr on every call, so that tests can check it never surfaces.
const HelperStderrMarker = "synthetic-helper-stderr-marker"

// HelperConfig puts a fake docker-credential-<name> on PATH that answers
// "get" for the registry with its credentials, and writes a Docker client
// configuration whose credHelpers name it for the registry. It returns
// the configuration directory and the file the helper appends each
// requested server URL to.
func (r *AuthRegistry) HelperConfig(t testing.TB, name string) (dir, log string) {
	t.Helper()
	bin := t.TempDir()
	log = filepath.Join(bin, "requests.log")
	script := `#!/bin/sh
[ "$1" = get ] || exit 2
read -r url
printf '%s\n' "$url" >> '` + log + `'
printf '` + HelperStderrMarker + `' >&2
if [ "$url" = '` + r.Addr + `' ]; then
  printf '{"ServerURL":"%s","Username":"` + RegistryUsername + `","Secret":"` + RegistryPassword + `"}' "$url"
  exit 0
fi
printf 'credentials not found in native keychain'
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "docker-credential-"+name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return DockerConfig(t, `{"credHelpers":{"`+r.Addr+`":"`+name+`"}}`), log
}

// PushArtifact uploads blobs and the manifest that references them to
// repo:tag with the registry's credentials. config is the manifest's
// config blob; layers are its layer blobs with the given media type.
func (r *AuthRegistry) PushArtifact(t testing.TB, repo, tag, configType string, config []byte, layerType string, layers ...[]byte) {
	t.Helper()
	desc := func(mediaType string, b []byte) string {
		return fmt.Sprintf(`{"mediaType":%q,"digest":%q,"size":%d}`, mediaType, digestOf(b), len(b))
	}
	r.pushBlob(t, repo, config)
	layerDescs := ""
	for i, l := range layers {
		r.pushBlob(t, repo, l)
		if i > 0 {
			layerDescs += ","
		}
		layerDescs += desc(layerType, l)
	}
	manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":%s,"layers":[%s]}`,
		desc(configType, config), layerDescs)
	r.do(t, http.MethodPut, "https://"+r.Addr+"/v2/"+repo+"/manifests/"+tag,
		"application/vnd.oci.image.manifest.v1+json", []byte(manifest), http.StatusCreated)
}

func (r *AuthRegistry) pushBlob(t testing.TB, repo string, blob []byte) {
	t.Helper()
	resp := r.do(t, http.MethodPost, "https://"+r.Addr+"/v2/"+repo+"/blobs/uploads/", "", nil, http.StatusAccepted)
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("blob upload location: %v", err)
	}
	q := loc.Query()
	q.Set("digest", digestOf(blob))
	loc.RawQuery = q.Encode()
	r.do(t, http.MethodPut, loc.String(), "application/octet-stream", blob, http.StatusCreated)
}

func (r *AuthRegistry) do(t testing.TB, method, url, contentType string, body []byte, want int) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.SetBasicAuth(RegistryUsername, RegistryPassword)
	resp, err := r.Client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("%s %s: %s: %s", method, url, resp.Status, b)
	}
	return resp
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// selfSignedCert returns a PEM certificate and key for 127.0.0.1 and
// localhost.
func selfSignedCert(t testing.TB) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: "dcb test registry"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// tarFiles returns a tar stream of files, with their parent directories.
func tarFiles(t testing.TB, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	dirs := map[string]bool{}
	for name, body := range files {
		if dir := filepath.Dir(name); !dirs[dir] {
			dirs[dir] = true
			if err := tw.WriteHeader(&tar.Header{Name: dir + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
