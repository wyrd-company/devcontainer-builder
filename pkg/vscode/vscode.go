// Package vscode resolves and downloads the VS Code Server tarball that
// matches a given platform. The result is staged on disk so it can be
// COPY'd into the build context and unpacked at the canonical location
// VS Code expects (~/.vscode-server/bin/<commit>/).
//
// Background: when VS Code's "Remote - Containers" extension attaches to a
// container, it tries to download and extract its server bin into
// ~/.vscode-server/bin/<commit>/. Pre-injecting that directory at build
// time makes the first-attach experience instant and works in offline
// environments.
package vscode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
)

// Quality is "stable" or "insider".
type Quality string

const (
	QualityStable  Quality = "stable"
	QualityInsider Quality = "insider"
)

// ServerKind selects between vanilla VS Code Server and the open-source
// VSCodium-style "code-server" build. We default to VS Code Server since
// the devcontainer experience is most commonly used with VS Code.
type ServerKind string

const (
	KindVSCodeServer ServerKind = "vscode-server"
)

// Spec describes the server variant to download.
type Spec struct {
	Quality  Quality
	Kind     ServerKind
	Platform platform.Platform
	// Commit, when non-empty, pins the download. When empty, the latest
	// is queried from the update API.
	Commit string
	// AlpineLibc selects musl-libc-compatible binaries.
	AlpineLibc bool
}

// DefaultSpec returns a sane default spec for the current platform.
func DefaultSpec() Spec {
	return Spec{
		Quality:  QualityStable,
		Kind:     KindVSCodeServer,
		Platform: platform.Current(),
	}
}

// Result describes a staged VS Code server download.
type Result struct {
	// TarballPath is the path to the downloaded tarball on disk.
	TarballPath string
	// Commit is the resolved commit hash used in the canonical install
	// path (~/.vscode-server/bin/<Commit>/).
	Commit string
	// Quality is the resolved quality channel.
	Quality Quality
	// SHA256 is the hex digest of the tarball contents.
	SHA256 string
	// Bytes is the size of the tarball.
	Bytes int64
	// URL is the source URL that was used.
	URL string
}

// Downloader resolves and fetches VS Code server tarballs.
type Downloader struct {
	HTTP   *http.Client
	Logger func(format string, args ...any)
}

// NewDownloader constructs a Downloader with sane defaults.
func NewDownloader() *Downloader {
	return &Downloader{
		HTTP: &http.Client{Timeout: 10 * time.Minute},
	}
}

func (d *Downloader) logf(format string, args ...any) {
	if d.Logger != nil {
		d.Logger(format, args...)
	}
}

// Fetch downloads the server tarball for the given Spec into stagingDir.
// The returned Result contains the on-disk path plus metadata.
func (d *Downloader) Fetch(ctx context.Context, spec Spec, stagingDir string) (*Result, error) {
	if spec.Quality == "" {
		spec.Quality = QualityStable
	}
	if spec.Kind == "" {
		spec.Kind = KindVSCodeServer
	}
	arch, err := spec.Platform.VSCodeServerArch()
	if err != nil {
		return nil, err
	}

	commit := spec.Commit
	if commit == "" {
		commit, err = d.resolveLatestCommit(ctx, spec.Quality, arch, spec.AlpineLibc)
		if err != nil {
			return nil, fmt.Errorf("resolve latest commit: %w", err)
		}
	}

	url := DownloadURL(spec.Quality, arch, commit, spec.AlpineLibc)
	d.logf("downloading vscode server %s for %s @ %s", spec.Quality, spec.Platform, commit[:min(12, len(commit))])

	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, err
	}
	out := filepath.Join(stagingDir, fmt.Sprintf("vscode-server-%s-%s-%s.tar.gz", spec.Quality, arch, commit))

	tmp, err := os.CreateTemp(stagingDir, ".dl-*.tar.gz")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		tmp.Close()
		return nil, err
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		tmp.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("vscode download %s: %s: %s", url, resp.Status, string(body))
	}

	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	if err := os.Rename(tmpPath, out); err != nil {
		return nil, err
	}
	return &Result{
		TarballPath: out,
		Commit:      commit,
		Quality:     spec.Quality,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
		Bytes:       n,
		URL:         url,
	}, nil
}

// DownloadURL builds the official VS Code update URL for a given commit.
//
// Pattern: https://update.code.visualstudio.com/commit:<commit>/server-linux-<arch>[-alpine]/<quality>
func DownloadURL(quality Quality, arch, commit string, alpine bool) string {
	suffix := ""
	if alpine {
		suffix = "-alpine"
	}
	return fmt.Sprintf("https://update.code.visualstudio.com/commit:%s/server-linux-%s%s/%s",
		commit, arch, suffix, quality)
}

// resolveLatestCommit asks update.code.visualstudio.com for the current
// commit on the requested channel.
func (d *Downloader) resolveLatestCommit(ctx context.Context, quality Quality, arch string, alpine bool) (string, error) {
	suffix := ""
	if alpine {
		suffix = "-alpine"
	}
	// "0000000000000000000000000000000000000000" is the documented
	// sentinel commit that asks the API for the latest update info.
	url := fmt.Sprintf("https://update.code.visualstudio.com/api/update/server-linux-%s%s/%s/0000000000000000000000000000000000000000",
		arch, suffix, quality)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("update api %s: %s: %s", url, resp.Status, string(body))
	}
	var payload struct {
		Version    string `json:"version"`
		ProductVersion string `json:"productVersion"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	commit := strings.TrimSpace(payload.Version)
	if commit == "" {
		return "", fmt.Errorf("update api returned empty version")
	}
	return commit, nil
}

// CanonicalInstallDir returns the path inside a container where VS Code
// expects its server binary to live for a given user.
//
// Example: CanonicalInstallDir("vscode", "abc123") =>
//
//	"/home/vscode/.vscode-server/bin/abc123"
//
// For root, the path is /root/.vscode-server/...
func CanonicalInstallDir(user, commit string) string {
	if user == "" || user == "root" {
		return filepath.Join("/root", ".vscode-server", "bin", commit)
	}
	return filepath.Join("/home", user, ".vscode-server", "bin", commit)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
