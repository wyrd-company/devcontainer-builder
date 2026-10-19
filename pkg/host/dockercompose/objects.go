// relationships:
//
//	references: [manage, projectname]
package dockercompose

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/mount"
)

// objectMaterializer writes complete files in a private run directory. The
// allocator publishes durable ownership before the first payload write.
type objectMaterializer struct {
	localEnv map[string]string
	allocate func() (string, error)
	root     string
}

func newObjectMaterializer(allocate func() (string, error), localEnv map[string]string) *objectMaterializer {
	return &objectMaterializer{allocate: allocate, localEnv: localEnv}
}

// resolveConfig returns an absolute host path the daemon can bind into
// a container for the supplied top-level config entry.
func (m *objectMaterializer) resolveConfig(name string, c types.ConfigObjConfig) (string, error) {
	return m.resolveFileObject("configs", name, types.FileObjectConfig(c))
}

// resolveSecret returns an absolute host path the daemon can bind into
// a container for the supplied top-level secret entry.
func (m *objectMaterializer) resolveSecret(name string, s types.SecretConfig) (string, error) {
	return m.resolveFileObject("secrets", name, types.FileObjectConfig(s))
}

func (m *objectMaterializer) resolveFileObject(kind, name string, obj types.FileObjectConfig) (string, error) {
	if bool(obj.External) {
		return "", fmt.Errorf("%s %q is declared external; this host doesn't speak swarm/external %s", kind, name, kind)
	}
	if obj.Driver != "" {
		return "", fmt.Errorf("%s %q sets driver %q; only file/environment/content sources are supported", kind, name, obj.Driver)
	}
	if obj.File != "" {
		return obj.File, nil
	}
	if obj.Environment != "" {
		val, ok := m.localEnv[obj.Environment]
		if !ok {
			return "", fmt.Errorf("%s %q references env %q which is not in LocalEnv", kind, name, obj.Environment)
		}
		return m.writeMaterialized(kind, name, []byte(val))
	}
	if obj.Content != "" {
		return m.writeMaterialized(kind, name, []byte(obj.Content))
	}
	return "", fmt.Errorf("%s %q has no source (file/environment/content)", kind, name)
}

// writeMaterialized writes content to a project-scoped path, replacing
// any prior contents atomically. The resulting file is mode 0400 so
// the on-host file mirrors the read-only-secret semantic; the bind
// mount itself is also ReadOnly.
func (m *objectMaterializer) writeMaterialized(kind, name string, content []byte) (string, error) {
	if m.root == "" {
		root, err := m.allocate()
		if err != nil {
			return "", err
		}
		m.root = root
	}
	dir := filepath.Join(m.root, kind)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, sanitizeObjectName(name))
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp under %s: %w", dir, err)
	}
	cleanup := func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}
	if _, err := tmp.Write(content); err != nil {
		cleanup()
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Chmod(0o400); err != nil {
		cleanup()
		return "", fmt.Errorf("chmod %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("rename %s: %w", path, err)
	}
	return path, nil
}

// sanitizeObjectName keeps a config/secret filename predictable on
// disk: the compose key is only validated as a YAML map key, so it can
// contain characters that are awkward in filenames. Replace anything
// not alnum / dot / dash / underscore with "_".
func sanitizeObjectName(s string) string {
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '.', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// translateConfigsAndSecrets returns mount.Mount entries for every
// service-level configs/secrets ref. Targets default to /<source> for
// configs and /run/secrets/<source> for secrets — matching `docker
// compose` standalone mode (the daemon's swarm-mode secret/config
// objects aren't used). UID/GID/Mode are not enforced; the on-host
// file's ownership is what the container sees.
func translateConfigsAndSecrets(
	proj *types.Project, svc types.ServiceConfig, m *objectMaterializer,
) ([]mount.Mount, error) {
	if len(svc.Configs) == 0 && len(svc.Secrets) == 0 {
		return nil, nil
	}
	if m == nil {
		return nil, fmt.Errorf("service %q has configs/secrets but no materializer was supplied", svc.Name)
	}
	var out []mount.Mount
	for _, ref := range svc.Configs {
		entry, ok := proj.Configs[ref.Source]
		if !ok {
			return nil, fmt.Errorf("service %q references config %q which is not declared at top level", svc.Name, ref.Source)
		}
		host, err := m.resolveConfig(ref.Source, entry)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", svc.Name, err)
		}
		target := ref.Target
		if target == "" {
			target = "/" + ref.Source
		}
		out = append(out, mount.Mount{
			Type:     mount.TypeBind,
			Source:   host,
			Target:   target,
			ReadOnly: true,
		})
	}
	for _, ref := range svc.Secrets {
		entry, ok := proj.Secrets[ref.Source]
		if !ok {
			return nil, fmt.Errorf("service %q references secret %q which is not declared at top level", svc.Name, ref.Source)
		}
		host, err := m.resolveSecret(ref.Source, entry)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", svc.Name, err)
		}
		target := ref.Target
		if target == "" {
			target = "/run/secrets/" + ref.Source
		}
		out = append(out, mount.Mount{
			Type:     mount.TypeBind,
			Source:   host,
			Target:   target,
			ReadOnly: true,
		})
	}
	return out, nil
}
