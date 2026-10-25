package dockercompose

import (
	"errors"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/registry"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// engineOnly is an Engine API credential source with no Credentials
// method. It records the references it is asked about.
type engineOnly struct {
	value string
	err   error
	refs  *[]string
}

func (a engineOnly) EngineAuth(ref string) (string, error) {
	if a.refs != nil {
		*a.refs = append(*a.refs, ref)
	}
	return a.value, a.err
}

// adapt is buildAuth for an Engine-only source, failing the test rather
// than panicking when it is not adapted.
func adapt(t *testing.T, src engineOnly) buildkit.RegistryAuth {
	t.Helper()
	a := buildAuth(src)
	if a == nil {
		t.Fatal("buildAuth returned nil for an Engine source")
	}
	return a
}

func encodeAuth(t *testing.T, ac registry.AuthConfig) string {
	t.Helper()
	v, err := registry.EncodeAuthConfig(ac)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBuildAuth_NilKeepsTheBuildKitDefault(t *testing.T) {
	if got := buildAuth(nil); got != nil {
		t.Fatalf("buildAuth(nil) = %#v, want nil", got)
	}
}

func TestBuildAuth_UsesABuildKitSourceDirectly(t *testing.T) {
	static := registryauth.Static{"registry.example.test": {Username: "testuser"}}
	got, ok := buildAuth(static).(registryauth.Static)
	if !ok || got["registry.example.test"].Username != "testuser" {
		t.Fatalf("buildAuth(Static) = %#v, want the Static itself", got)
	}
	store := registryauth.New(t.TempDir())
	if buildAuth(store) != buildkit.RegistryAuth(store) {
		t.Fatal("buildAuth(*Store) did not return the Store")
	}
}

func TestBuildAuth_AdaptsAnEngineSource(t *testing.T) {
	var refs []string
	src := engineOnly{value: encodeAuth(t, registry.AuthConfig{Username: "testuser", Password: "testpassword", ServerAddress: "registry.example.test"}), refs: &refs}
	adapted := buildAuth(src)
	if adapted == nil {
		t.Fatal("buildAuth returned nil for an Engine source")
	}
	got, err := adapted.Credentials("registry.example.test")
	if err != nil || got.Username != "testuser" || got.Password != "testpassword" {
		t.Fatalf("Credentials = %+v, %v; want the Engine source's credentials", got, err)
	}
	if len(refs) != 1 || !strings.HasPrefix(refs[0], "registry.example.test/") {
		t.Fatalf("EngineAuth asked for %v, want one reference on registry.example.test", refs)
	}
	enc, err := adapted.EngineAuth("registry.example.test/team/app:1")
	if err != nil || enc != src.value {
		t.Fatalf("EngineAuth = %q, %v; want the source's value", enc, err)
	}
}

func TestBuildAuth_AdaptsTokens(t *testing.T) {
	for name, ac := range map[string]registry.AuthConfig{
		"identity token": {IdentityToken: "synthetic-refresh-token"},
		"registry token": {RegistryToken: "synthetic-registry-token"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := adapt(t, engineOnly{value: encodeAuth(t, ac)}).Credentials("registry.example.test")
			if err != nil || got.IdentityToken != ac.IdentityToken || got.RegistryToken != ac.RegistryToken {
				t.Fatalf("Credentials = %+v, %v; want %+v", got, err, ac)
			}
		})
	}
}

func TestBuildAuth_EmptyEngineValueIsAnonymousWithoutFallback(t *testing.T) {
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example.test":{"auth":"ZW52dXNlcjplbnZwYXNzd29yZA=="}}}`)
	got, err := adapt(t, engineOnly{}).Credentials("registry.example.test")
	if err != nil || !got.Anonymous() {
		t.Fatalf("Credentials = %+v, %v; want anonymous", got, err)
	}
}

func TestBuildAuth_EngineErrorIsReturned(t *testing.T) {
	want := errors.New("synthetic source failure")
	if _, err := adapt(t, engineOnly{err: want}).Credentials("registry.example.test"); !errors.Is(err, want) {
		t.Fatalf("err = %v, want the source's error", err)
	}
}

// A malformed value fails with a fixed reason. The decoder's own error
// is not reported.
func TestBuildAuth_MalformedEngineValueHasAFixedReason(t *testing.T) {
	const value = "synthetic-malformed-secret"
	_, err := adapt(t, engineOnly{value: value}).Credentials("registry.example.test")
	const want = "registry credentials for registry.example.test: the X-Registry-Auth value is malformed"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestServiceBuild_ForwardsRegistryAuth(t *testing.T) {
	proj := &types.Project{Name: "proj", WorkingDir: "/w"}
	svc := types.ServiceConfig{Name: "x", Build: &types.BuildConfig{Context: "."}}
	auth := registryauth.Static{"registry.example.test": {Username: "testuser"}}
	_, opt, err := serviceBuild(proj, svc, "x:1", nil, BuildServiceImagesInputs{RegistryAuth: auth})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := opt.RegistryAuth.(registryauth.Static); !ok || s["registry.example.test"].Username != "testuser" {
		t.Fatalf("SolveOptions.RegistryAuth = %#v, want the injected source", opt.RegistryAuth)
	}
}

func TestPrimaryServiceBuild_ForwardsBuilderRegistryAuth(t *testing.T) {
	auth := registryauth.Static{"registry.example.test": {Username: "testuser"}}
	in := BuildPrimaryInputs{Spec: &spec.DevContainer{Service: "app"}, BuilderOptions: builder.Options{RegistryAuth: auth}}
	got := primaryServiceBuild(&types.Project{Name: "proj"}, in)
	if s, ok := got.RegistryAuth.(registryauth.Static); !ok || s["registry.example.test"].Username != "testuser" {
		t.Fatalf("RegistryAuth = %#v, want the builder options' source", got.RegistryAuth)
	}
}

func TestServiceBuilds_UseTheHostRegistryAuth(t *testing.T) {
	auth := registryauth.Static{"registry.example.test": {Username: "testuser"}}
	h := New(nil, Options{RegistryAuth: auth})
	got := h.serviceBuilds(&types.Project{Name: "proj"}, "app")
	if s, ok := got.RegistryAuth.(registryauth.Static); !ok || s["registry.example.test"].Username != "testuser" {
		t.Fatalf("RegistryAuth = %#v, want the host's source", got.RegistryAuth)
	}
	if _, ok := New(nil, Options{RegistryAuth: engineOnly{}}).serviceBuilds(&types.Project{}, "app").RegistryAuth.(engineCredentials); !ok {
		t.Fatal("an Engine-only host source was not adapted for service builds")
	}
}
