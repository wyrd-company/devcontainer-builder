// relationships:
//   references: [oci, buildkit]

// Package registryauth reads registry credentials from the Docker client
// configuration, as the docker CLI does: static "auths" entries in
// config.json, and the credential-helper protocol
// (docker-credential-<name> get) for "credsStore" and "credHelpers".
//
// Credentials are read on demand and held in memory only. A credential
// helper's output never reaches the process output or an error, so that a
// misbehaving helper cannot put a secret into a log.
package registryauth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/distribution/reference"
	"github.com/docker/cli/cli/config"
	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/credentials"
	"github.com/docker/docker/api/types/registry"
)

// dockerHubKey is the config.json key under which the docker CLI stores
// Docker Hub credentials.
const dockerHubKey = "https://index.docker.io/v1/"

// Credentials authenticate to one registry. The zero value is anonymous.
type Credentials struct {
	Username string
	Password string
	// IdentityToken is an OAuth2 refresh token, used instead of
	// Username and Password to obtain registry tokens.
	IdentityToken string
	// RegistryToken is a bearer token sent to the registry as is.
	RegistryToken string
}

// Anonymous reports whether c holds no credentials.
func (c Credentials) Anonymous() bool {
	return c == Credentials{}
}

// Store resolves credentials from one Docker client configuration
// directory. The configuration is read on first use. A nil Store is
// anonymous for every registry.
type Store struct {
	dir string

	once sync.Once
	file *configfile.ConfigFile
	err  error

	mu    sync.Mutex
	cache map[string]Credentials
}

// FromEnvironment returns the Store for the Docker client configuration
// the docker CLI uses: $DOCKER_CONFIG/config.json, or
// ~/.docker/config.json when DOCKER_CONFIG is unset.
func FromEnvironment() *Store {
	return New("")
}

// New returns the Store for the config.json in dir. An empty dir selects
// the directory FromEnvironment uses, read when the Store is first used.
func New(dir string) *Store {
	return &Store{dir: dir, cache: map[string]Credentials{}}
}

// Credentials returns the credentials for the registry host (for
// example "ghcr.io", "127.0.0.1:5000" or "registry-1.docker.io"). A host
// with no configured credentials is anonymous.
func (s *Store) Credentials(host string) (Credentials, error) {
	if s == nil {
		return Credentials{}, nil
	}
	key := configKey(host)
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cache[key]; ok {
		return c, nil
	}
	file, err := s.load()
	if err != nil {
		return Credentials{}, err
	}
	c, err := lookup(file, key)
	if err != nil {
		return Credentials{}, fmt.Errorf("registry credentials for %s: %w", host, err)
	}
	s.cache[key] = c
	return c, nil
}

// lookup resolves key as the docker CLI does: DOCKER_AUTH_CONFIG first;
// then the credential helper that credHelpers or credsStore names, which
// alone answers when one is configured; otherwise the static auths.
func lookup(file *configfile.ConfigFile, key string) (Credentials, error) {
	if env := os.Getenv(envAuthConfig); env != "" {
		envFile := configfile.New("")
		if err := envFile.LoadFromReader(strings.NewReader(env)); err != nil {
			return Credentials{}, fmt.Errorf("%s is not valid", envAuthConfig)
		}
		if c := staticCredentials(envFile, key); !c.Anonymous() {
			return c, nil
		}
	}
	if helper := helperFor(file, key); helper != "" {
		return helperCredentials(helper, key)
	}
	return staticCredentials(file, key), nil
}

// envAuthConfig holds auths entries in the environment, as the docker CLI
// reads them.
const envAuthConfig = "DOCKER_AUTH_CONFIG"

func staticCredentials(file *configfile.ConfigFile, key string) Credentials {
	ac, _ := credentials.NewFileStore(file).Get(key)
	return Credentials{
		Username:      ac.Username,
		Password:      ac.Password,
		IdentityToken: ac.IdentityToken,
		RegistryToken: ac.RegistryToken,
	}
}

// helperFor is the credential helper configured for key, or "".
func helperFor(file *configfile.ConfigFile, key string) string {
	if helper, ok := file.CredentialHelpers[key]; ok {
		return helper
	}
	return file.CredentialsStore
}

// EngineAuth returns the X-Registry-Auth value for an Engine API pull or
// push of the image ref, or "" when its registry has no credentials.
func (s *Store) EngineAuth(ref string) (string, error) {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return "", fmt.Errorf("registry credentials for %s: %w", ref, err)
	}
	host := reference.Domain(named)
	c, err := s.Credentials(host)
	if err != nil || c.Anonymous() {
		return "", err
	}
	return registry.EncodeAuthConfig(registry.AuthConfig{
		Username:      c.Username,
		Password:      c.Password,
		IdentityToken: c.IdentityToken,
		RegistryToken: c.RegistryToken,
		ServerAddress: configKey(host),
	})
}

func (s *Store) load() (*configfile.ConfigFile, error) {
	s.once.Do(func() {
		dir := s.dir
		if dir == "" {
			dir = environmentDir()
		}
		s.file, s.err = config.Load(dir)
		if s.err != nil {
			s.err = fmt.Errorf("registry credentials: %w", s.err)
		}
	})
	return s.file, s.err
}

// environmentDir is the docker CLI's configuration directory. It is read
// on each call, unlike config.Dir, so that it follows DOCKER_CONFIG.
func environmentDir() string {
	if dir := os.Getenv(config.EnvOverrideConfigDir); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".docker"
	}
	return filepath.Join(home, ".docker")
}

// configKey is the config.json key the docker CLI uses for host.
func configKey(host string) string {
	switch host {
	case "docker.io", "index.docker.io", "registry-1.docker.io", dockerHubKey:
		return dockerHubKey
	}
	return host
}
