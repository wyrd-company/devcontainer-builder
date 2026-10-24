package registryauth

import (
	"errors"
	"testing"
)

func TestStatic_HeldHost(t *testing.T) {
	s := Static{"registry.example.test": {Username: "testuser", Password: "testpassword"}}
	got, err := s.Credentials("registry.example.test")
	if err != nil || got.Username != "testuser" || got.Password != "testpassword" {
		t.Fatalf("Credentials = %+v, %v; want the held credentials", got, err)
	}
}

func TestStatic_OtherHostIsAnonymous(t *testing.T) {
	s := Static{"registry.example.test": {Username: "testuser", Password: "testpassword"}}
	for _, host := range []string{"other.example.test", "registry.example.test:5000", "registry-1.docker.io"} {
		if got, err := s.Credentials(host); err != nil || !got.Anonymous() {
			t.Fatalf("Credentials(%s) = %+v, %v; want anonymous", host, got, err)
		}
	}
}

func TestStatic_EmptyIsAnonymousWithAHelperConfigured(t *testing.T) {
	log := fakeHelper(t, "marker", "registry.example.test", `{"Username":"helperuser","Secret":"helpersecret"}`)
	t.Setenv("DOCKER_CONFIG", writeConfig(t, `{"credsStore":"marker"}`))
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example.test":{"auth":"`+basicAuth("envuser", "envpassword")+`"}}}`)
	got, err := Static{}.Credentials("registry.example.test")
	if err != nil || !got.Anonymous() {
		t.Fatalf("Credentials = %+v, %v; want anonymous", got, err)
	}
	if enc, err := (Static{}).EngineAuth("registry.example.test/team/app:1"); err != nil || enc != "" {
		t.Fatalf("EngineAuth = %q, %v; want empty", enc, err)
	}
	if r := requests(t, log); len(r) != 0 {
		t.Fatalf("helper was run for %v", r)
	}
}

func TestStatic_DockerHubAliases(t *testing.T) {
	for _, key := range dockerHubHosts {
		s := Static{key: {Username: "hubuser", Password: "hubpassword"}}
		for _, host := range dockerHubHosts {
			if got, err := s.Credentials(host); err != nil || got.Username != "hubuser" {
				t.Fatalf("key %s: Credentials(%s) = %+v, %v; want the Docker Hub credentials", key, host, got, err)
			}
		}
	}
}

func TestStatic_ExactKeyWinsOverDockerHubAlias(t *testing.T) {
	s := Static{
		dockerHubKey: {Username: "indexuser"},
		"docker.io":  {Username: "dockeriouser"},
	}
	if got, _ := s.Credentials("docker.io"); got.Username != "dockeriouser" {
		t.Fatalf("Credentials(docker.io) = %+v, want the exact key", got)
	}
	if got, _ := s.Credentials("registry-1.docker.io"); got.Username != "indexuser" {
		t.Fatalf("Credentials(registry-1.docker.io) = %+v, want the index key first", got)
	}
}

func TestStatic_EngineAuth(t *testing.T) {
	s := Static{"docker.io": {Username: "hubuser", Password: "hubpassword"}, "127.0.0.1:5000": {RegistryToken: "synthetic-registry-token"}}
	got, err := s.EngineAuth("team/app:1")
	if err != nil {
		t.Fatal(err)
	}
	if ac := decodeEngineAuth(t, got); ac.Username != "hubuser" || ac.Password != "hubpassword" || ac.ServerAddress != dockerHubKey {
		t.Fatalf("EngineAuth = %+v, want the Docker Hub credentials", ac)
	}
	got, err = s.EngineAuth("127.0.0.1:5000/team/app:1")
	if err != nil {
		t.Fatal(err)
	}
	if ac := decodeEngineAuth(t, got); ac.RegistryToken != "synthetic-registry-token" || ac.ServerAddress != "127.0.0.1:5000" {
		t.Fatalf("EngineAuth = %+v, want the registry token", ac)
	}
}

func TestNewWithoutHelpers_ReadsStaticAuths(t *testing.T) {
	dir := writeConfig(t, `{"auths":{"registry.example.test":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	got, err := NewWithoutHelpers(dir).Credentials("registry.example.test")
	if err != nil || got.Username != "testuser" || got.Password != "testpassword" {
		t.Fatalf("Credentials = %+v, %v; want the static credentials", got, err)
	}
}

func TestNewWithoutHelpers_ReadsDockerAuthConfig(t *testing.T) {
	log := fakeHelper(t, "marker", "registry.example.test", `{"Username":"helperuser","Secret":"helpersecret"}`)
	dir := writeConfig(t, `{"credsStore":"marker"}`)
	t.Setenv("DOCKER_AUTH_CONFIG", `{"auths":{"registry.example.test":{"auth":"`+basicAuth("envuser", "envpassword")+`"}}}`)
	got, err := NewWithoutHelpers(dir).Credentials("registry.example.test")
	if err != nil || got.Username != "envuser" {
		t.Fatalf("Credentials = %+v, %v; want the DOCKER_AUTH_CONFIG credentials", got, err)
	}
	if r := requests(t, log); len(r) != 0 {
		t.Fatalf("helper was run for %v", r)
	}
}

func TestNewWithoutHelpers_HelperHostFailsWithoutRunningIt(t *testing.T) {
	for name, cfg := range map[string]string{
		"credsStore":  `{"credsStore":"marker","auths":{"registry.example.test":{"auth":"` + basicAuth("fileuser", "filepassword") + `"}}}`,
		"credHelpers": `{"credHelpers":{"registry.example.test":"marker"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			log := fakeHelper(t, "marker", "registry.example.test", `{"Username":"helperuser","Secret":"helpersecret"}`)
			s := NewWithoutHelpers(writeConfig(t, cfg))
			got, err := s.Credentials("registry.example.test")
			if !errors.Is(err, ErrHelpersDisabled) || !got.Anonymous() {
				t.Fatalf("Credentials = %+v, %v; want ErrHelpersDisabled", got, err)
			}
			if _, err := s.EngineAuth("registry.example.test/team/app:1"); !errors.Is(err, ErrHelpersDisabled) {
				t.Fatalf("EngineAuth err = %v, want ErrHelpersDisabled", err)
			}
			if r := requests(t, log); len(r) != 0 {
				t.Fatalf("helper was run for %v", r)
			}
		})
	}
}

func TestNewWithoutHelpers_OtherHostsUnaffectedByCredHelpers(t *testing.T) {
	dir := writeConfig(t, `{"credHelpers":{"helper.example.test":"marker"},"auths":{"registry.example.test":{"auth":"`+basicAuth("testuser", "testpassword")+`"}}}`)
	got, err := NewWithoutHelpers(dir).Credentials("registry.example.test")
	if err != nil || got.Username != "testuser" {
		t.Fatalf("Credentials = %+v, %v; want the static credentials", got, err)
	}
}

func TestNewWithoutHelpers_EmptyDirFollowsDockerConfig(t *testing.T) {
	dir := writeConfig(t, `{"credsStore":"marker"}`)
	t.Setenv("DOCKER_CONFIG", dir)
	if _, err := NewWithoutHelpers("").Credentials("registry.example.test"); !errors.Is(err, ErrHelpersDisabled) {
		t.Fatalf("err = %v, want ErrHelpersDisabled from $DOCKER_CONFIG", err)
	}
}
