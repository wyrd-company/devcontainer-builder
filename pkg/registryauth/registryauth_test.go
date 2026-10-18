package registryauth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/registry"
)

// writeConfig writes config.json into a new directory and returns it.
// It clears DOCKER_AUTH_CONFIG so that only the written file applies.
func writeConfig(t *testing.T, cfg string) string {
	t.Helper()
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// fakeHelper puts docker-credential-<name> on PATH. The helper answers
// "get" with body for the server URL want, and with the protocol's
// not-found reply for any other URL. It appends each server URL it is
// asked for to the returned log file.
func fakeHelper(t *testing.T, name, want, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake credential helpers are shell scripts")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "requests.log")
	script := `#!/bin/sh
[ "$1" = get ] || exit 2
read -r url
printf '%s\n' "$url" >> '` + log + `'
if [ "$url" = '` + want + `' ]; then
  printf '%s' '` + body + `'
  exit 0
fi
printf 'credentials not found in native keychain'
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "docker-credential-"+name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func requests(t *testing.T, log string) []string {
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

func TestCredentials_StaticAuths(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"registry.example.test":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	got, err := New(dir).Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Credentials{Username: "testuser", Password: "testpassword"}); got != want {
		t.Fatalf("Credentials = %+v, want %+v", got, want)
	}
}

func TestCredentials_StaticAuthsMatchURLKeys(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"https://registry.example.test/v1/":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	got, err := New(dir).Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "testuser" || got.Password != "testpassword" {
		t.Fatalf("Credentials = %+v, want the URL-keyed entry", got)
	}
}

func TestCredentials_IdentityToken(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"registry.example.test":{"auth":"`+basicAuth("testuser", "")+`","identitytoken":"test-refresh-token"}}}`)
	got, err := New(dir).Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.IdentityToken != "test-refresh-token" {
		t.Fatalf("IdentityToken = %q, want the configured token", got.IdentityToken)
	}
}

func TestCredentials_DockerHubHostsUseTheIndexKey(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"https://index.docker.io/v1/":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	s := New(dir)
	for _, host := range []string{"docker.io", "index.docker.io", "registry-1.docker.io"} {
		got, err := s.Credentials(host)
		if err != nil {
			t.Fatal(err)
		}
		if got.Username != "testuser" {
			t.Errorf("Credentials(%s) = %+v, want the Docker Hub entry", host, got)
		}
	}
}

func TestCredentials_UnconfiguredHostIsAnonymous(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"registry.example.test":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	got, err := New(dir).Credentials("other.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Anonymous() {
		t.Fatalf("Credentials = %+v, want anonymous", got)
	}
}

func TestCredentials_MissingConfigIsAnonymous(t *testing.T) {
	got, err := New(t.TempDir()).Credentials("registry.example.test")
	if err != nil || !got.Anonymous() {
		t.Fatalf("Credentials = %+v, %v; want anonymous", got, err)
	}
}

func TestCredentials_NilStoreIsAnonymous(t *testing.T) {
	var s *Store
	got, err := s.Credentials("registry.example.test")
	if err != nil || !got.Anonymous() {
		t.Fatalf("Credentials = %+v, %v; want anonymous", got, err)
	}
}

func TestCredentials_MalformedConfigFails(t *testing.T) {
	dir := writeConfig(t, `{"auths":`)
	if _, err := New(dir).Credentials("registry.example.test"); err == nil {
		t.Fatal("Credentials with a malformed config.json succeeded")
	}
}

func TestFromEnvironment_FollowsDockerConfig(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"registry.example.test":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	t.Setenv("DOCKER_CONFIG", dir)
	got, err := FromEnvironment().Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "testuser" {
		t.Fatalf("Credentials = %+v, want the DOCKER_CONFIG entry", got)
	}
}

func TestCredentials_DockerAuthConfigEnvironment(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"registry.example.test":{"auth":"`+basicAuth("fileuser", "filepassword")+`"}}}`)
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example.test":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	got, err := New(dir).Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "testuser" || got.Password != "testpassword" {
		t.Fatalf("Credentials = %+v, want the DOCKER_AUTH_CONFIG entry", got)
	}
}

// Each Store reads DOCKER_CONFIG when first used, so that a change of
// DOCKER_CONFIG applies to the next Store.
func TestFromEnvironment_ReadsDockerConfigPerStore(t *testing.T) {
	for _, user := range []string{"firstuser", "seconduser"} {
		t.Setenv("DOCKER_CONFIG", writeConfig(t, `{"auths":{"registry.example.test":{"auth":"`+basicAuth(user, "testpassword")+`"}}}`))
		got, err := FromEnvironment().Credentials("registry.example.test")
		if err != nil {
			t.Fatal(err)
		}
		if got.Username != user {
			t.Fatalf("Credentials = %+v, want the current DOCKER_CONFIG entry for %s", got, user)
		}
	}
}

func TestCredentials_CredsStoreHelper(t *testing.T) {
	log := fakeHelper(t, "dcbtest", "registry.example.test", `{"ServerURL":"registry.example.test","Username":"testuser","Secret":"testpassword"}`)
	dir := writeConfig(t, `{"credsStore":"dcbtest"}`)
	got, err := New(dir).Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Credentials{Username: "testuser", Password: "testpassword"}); got != want {
		t.Fatalf("Credentials = %+v, want %+v", got, want)
	}
	if got := requests(t, log); len(got) != 1 || got[0] != "registry.example.test" {
		t.Fatalf("helper requests = %q, want one for registry.example.test", got)
	}
}

func TestCredentials_HelperTokenUsernameIsIdentityToken(t *testing.T) {
	fakeHelper(t, "dcbtest", "registry.example.test", `{"ServerURL":"registry.example.test","Username":"<token>","Secret":"test-refresh-token"}`)
	dir := writeConfig(t, `{"credsStore":"dcbtest"}`)
	got, err := New(dir).Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Credentials{IdentityToken: "test-refresh-token"}); got != want {
		t.Fatalf("Credentials = %+v, want %+v", got, want)
	}
}

func TestCredentials_CredHelpersTakePrecedence(t *testing.T) {
	fakeHelper(t, "dcbtest", "registry.example.test", `{"ServerURL":"registry.example.test","Username":"helperuser","Secret":"helperpassword"}`)
	dir := writeConfig(t, `{"credsStore":"absent","credHelpers":{"registry.example.test":"dcbtest"},"auths":{"registry.example.test":{"auth":"`+basicAuth("fileuser", "filepassword")+`"}}}`)
	got, err := New(dir).Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "helperuser" || got.Password != "helperpassword" {
		t.Fatalf("Credentials = %+v, want the credHelpers entry", got)
	}
}

func TestCredentials_HelperNotFoundIsAnonymous(t *testing.T) {
	fakeHelper(t, "dcbtest", "registry.example.test", `{}`)
	dir := writeConfig(t, `{"credsStore":"dcbtest"}`)
	got, err := New(dir).Credentials("other.example.test")
	if err != nil || !got.Anonymous() {
		t.Fatalf("Credentials = %+v, %v; want anonymous", got, err)
	}
}

func TestCredentials_CachesHelperAnswers(t *testing.T) {
	log := fakeHelper(t, "dcbtest", "registry.example.test", `{"ServerURL":"registry.example.test","Username":"testuser","Secret":"testpassword"}`)
	s := New(writeConfig(t, `{"credsStore":"dcbtest"}`))
	for i := 0; i < 3; i++ {
		if _, err := s.Credentials("registry.example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if got := requests(t, log); len(got) != 1 {
		t.Fatalf("helper ran %d times, want 1", len(got))
	}
}

func TestCredentials_HelperErrorsOmitHelperOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake credential helpers are shell scripts")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'leaked-test-secret'\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "docker-credential-dcbfail"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := writeConfig(t, `{"credsStore":"dcbfail"}`)
	_, err := New(dir).Credentials("registry.example.test")
	if err == nil {
		t.Fatal("Credentials with a failing helper succeeded")
	}
	if strings.Contains(err.Error(), "leaked-test-secret") {
		t.Fatalf("error carries helper output: %v", err)
	}
	if !strings.Contains(err.Error(), "docker-credential-dcbfail") || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("error = %v, want the helper name and its exit status", err)
	}
}

func TestCredentials_MissingHelperFails(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := writeConfig(t, `{"credsStore":"dcbabsent"}`)
	_, err := New(dir).Credentials("registry.example.test")
	if err == nil || !strings.Contains(err.Error(), "docker-credential-dcbabsent") {
		t.Fatalf("Credentials with a missing helper: err = %v, want a helper error", err)
	}
}

func TestEngineAuth_EncodesRegistryCredentials(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"127.0.0.1:5000":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	got, err := New(dir).EngineAuth("127.0.0.1:5000/team/app:1")
	if err != nil {
		t.Fatal(err)
	}
	ac := decodeEngineAuth(t, got)
	if ac.Username != "testuser" || ac.Password != "testpassword" || ac.ServerAddress != "127.0.0.1:5000" {
		t.Fatalf("EngineAuth = %+v, want the 127.0.0.1:5000 credentials", ac)
	}
}

func TestEngineAuth_DockerHubReference(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"https://index.docker.io/v1/":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	got, err := New(dir).EngineAuth("team/app:1")
	if err != nil {
		t.Fatal(err)
	}
	if ac := decodeEngineAuth(t, got); ac.Username != "testuser" || ac.ServerAddress != "https://index.docker.io/v1/" {
		t.Fatalf("EngineAuth = %+v, want the Docker Hub credentials", ac)
	}
}

func TestEngineAuth_AnonymousIsEmpty(t *testing.T) {
	got, err := New(t.TempDir()).EngineAuth("127.0.0.1:5000/team/app:1")
	if err != nil || got != "" {
		t.Fatalf("EngineAuth = %q, %v; want empty", got, err)
	}
}

func decodeEngineAuth(t *testing.T, v string) registry.AuthConfig {
	t.Helper()
	b, err := base64.URLEncoding.DecodeString(v)
	if err != nil {
		t.Fatalf("decode %q: %v", v, err)
	}
	var ac registry.AuthConfig
	if err := json.Unmarshal(b, &ac); err != nil {
		t.Fatal(err)
	}
	return ac
}
