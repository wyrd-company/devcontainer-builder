package registryauth

// Static holds fixed credentials keyed by registry host, for example
// "ghcr.io" or "127.0.0.1:5000". It reads no configuration and runs no
// program. A host it does not hold is anonymous, so an empty Static is
// anonymous for every registry. "docker.io", "index.docker.io",
// "registry-1.docker.io" and "https://index.docker.io/v1/" all name Docker
// Hub, as they do for a Store.
type Static map[string]Credentials

// Credentials returns the credentials held for the registry host. An
// exact key wins; otherwise a Docker Hub host takes the first Docker Hub
// key present, in the order of dockerHubHosts.
func (s Static) Credentials(host string) (Credentials, error) {
	if c, ok := s[host]; ok {
		return c, nil
	}
	if configKey(host) == dockerHubKey {
		for _, alias := range dockerHubHosts {
			if c, ok := s[alias]; ok {
				return c, nil
			}
		}
	}
	return Credentials{}, nil
}

// EngineAuth returns the X-Registry-Auth value for an Engine API pull or
// push of the image ref, or "" when its registry has no credentials.
func (s Static) EngineAuth(ref string) (string, error) {
	return engineAuth(s.Credentials, ref)
}

// dockerHubHosts are the keys that name Docker Hub, in lookup order.
var dockerHubHosts = []string{dockerHubKey, "docker.io", "index.docker.io", "registry-1.docker.io"}
