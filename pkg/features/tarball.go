// relationships:
//
//	references: [features, lockfile, stage]
package features

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
)

var tarballName = regexp.MustCompile(`devcontainer-feature-([a-zA-Z0-9_-]+)\.tgz`)

func (r *Resolver) resolveTarball(ctx context.Context, source string, opts map[string]any, dir, safeID, integrity string) (*Resolved, error) {
	u, err := url.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("invalid feature tarball URL")
	}
	match := tarballName.FindStringSubmatch(path.Base(u.Path))
	if len(match) != 2 {
		return nil, fmt.Errorf("expected tarball name devcontainer-feature-<feature-id>.tgz")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid feature tarball URL")
	}
	resp, err := r.OCI.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching feature tarball failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feature tarball: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading feature tarball failed")
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if integrity != "" && digest != integrity {
		return nil, fmt.Errorf("feature tarball does not match its digest")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := extractTarMaybeGz(data, dir); err != nil {
		return nil, err
	}
	meta, err := readMetadata(dir)
	if err != nil {
		return nil, err
	}
	meta.ID = match[1]
	return &Resolved{Source: source, Tarball: source, Digest: digest, Metadata: meta, Options: opts, SafeID: safeID, Dir: dir, localPath: source}, nil
}
