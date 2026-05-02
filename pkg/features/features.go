// Package features resolves and stages devcontainer features so they can be
// copied into a build context and executed during image build.
//
// A feature is a small artifact (typically published as an OCI image) that
// contributes an install script plus metadata describing dependencies, the
// installation order, and any options. See
// https://containers.dev/implementors/features/ for the spec.
//
// This package implements just enough of the spec to:
//
//   - Resolve features from OCI references like
//     ghcr.io/devcontainers/features/node:1.
//   - Resolve features from local filesystem paths (./local-feature) or
//     git tarballs delivered as direct file paths.
//   - Extract them into a staging directory.
//   - Determine installation order, honoring `installsAfter` and
//     `overrideFeatureInstallOrder`.
package features

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Metadata is the parsed devcontainer-feature.json bundled inside each
// feature artifact.
type Metadata struct {
	ID                 string            `json:"id"`
	Version            string            `json:"version"`
	Name               string            `json:"name,omitempty"`
	Description        string            `json:"description,omitempty"`
	DocumentationURL   string            `json:"documentationURL,omitempty"`
	Options            map[string]Option `json:"options,omitempty"`
	ContainerEnv       map[string]string `json:"containerEnv,omitempty"`
	Mounts             []any             `json:"mounts,omitempty"`
	Init               *bool             `json:"init,omitempty"`
	Privileged         *bool             `json:"privileged,omitempty"`
	CapAdd             []string          `json:"capAdd,omitempty"`
	SecurityOpt        []string          `json:"securityOpt,omitempty"`
	Entrypoint         string            `json:"entrypoint,omitempty"`
	InstallsAfter      []string          `json:"installsAfter,omitempty"`
	LegacyIDs          []string          `json:"legacyIds,omitempty"`
	Deprecated         bool              `json:"deprecated,omitempty"`
	OnCreateCommand    spec.Command `json:"onCreateCommand,omitempty"`
	UpdateContent      spec.Command `json:"updateContentCommand,omitempty"`
	PostCreateCommand  spec.Command `json:"postCreateCommand,omitempty"`
	PostStartCommand   spec.Command `json:"postStartCommand,omitempty"`
	PostAttachCommand  spec.Command `json:"postAttachCommand,omitempty"`
}

// Option models a single configurable feature option.
type Option struct {
	Type        string   `json:"type,omitempty"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	Proposals   []string `json:"proposals,omitempty"`
}

// Resolved is a single feature ready for inclusion in a build.
type Resolved struct {
	// Source is the original spec key (e.g. ghcr.io/devcontainers/features/node:1).
	Source string
	// Reference is the parsed OCI reference, if Source was an OCI ref.
	Reference *oci.Reference
	// Options as supplied in devcontainer.json (raw JSON values).
	Options map[string]any
	// Metadata loaded from the feature's devcontainer-feature.json.
	Metadata Metadata
	// Dir is the on-disk directory containing the feature contents
	// (devcontainer-feature.json, install.sh, etc.).
	Dir string
	// SafeID is a path-safe identifier used to namespace the feature
	// inside the staging directory.
	SafeID string
}

// Resolver pulls and stages features.
type Resolver struct {
	// CacheDir, if non-empty, is used to cache pulled OCI artifacts
	// keyed by digest. Otherwise resolution always re-fetches.
	CacheDir string
	// OCI is the registry client used for OCI-based features.
	OCI *oci.Client
	// Logger receives progress messages. May be nil.
	Logger func(format string, args ...any)
}

// New constructs a Resolver with default configuration.
func New() *Resolver {
	return &Resolver{
		OCI: oci.NewClient(),
	}
}

func (r *Resolver) logf(format string, args ...any) {
	if r.Logger != nil {
		r.Logger(format, args...)
	}
}

// Resolve fetches all features from the given spec into stagingDir. The
// returned slice is in install order.
func (r *Resolver) Resolve(specFeatures spec.OrderedMap, override []string, stagingDir string) ([]Resolved, error) {
	if specFeatures.Len() == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, err
	}

	out := make([]Resolved, 0, specFeatures.Len())
	for _, source := range specFeatures.Keys {
		raw := specFeatures.Values[source]
		var opts map[string]any
		if len(raw) > 0 && string(raw) != "null" {
			// Options may be a bool/string scalar shorthand for "version".
			if err := json.Unmarshal(raw, &opts); err != nil {
				var scalar any
				if err2 := json.Unmarshal(raw, &scalar); err2 != nil {
					return nil, fmt.Errorf("feature %s: parse options: %w", source, err)
				}
				opts = map[string]any{"version": scalar}
			}
		}
		resolved, err := r.resolveOne(source, opts, stagingDir)
		if err != nil {
			return nil, fmt.Errorf("feature %s: %w", source, err)
		}
		out = append(out, *resolved)
	}

	return order(out, override), nil
}

func (r *Resolver) resolveOne(source string, opts map[string]any, stagingDir string) (*Resolved, error) {
	safeID := safeIdent(source)
	dir := filepath.Join(stagingDir, safeID)

	if isLocalSource(source) {
		abs, err := filepath.Abs(source)
		if err != nil {
			return nil, err
		}
		if err := copyTree(abs, dir); err != nil {
			return nil, err
		}
		meta, err := readMetadata(dir)
		if err != nil {
			return nil, err
		}
		return &Resolved{
			Source: source, Options: opts, Metadata: meta, Dir: dir, SafeID: safeID,
		}, nil
	}

	ref, err := oci.ParseReference(source)
	if err != nil {
		return nil, fmt.Errorf("not a local path and not a valid OCI ref: %w", err)
	}
	r.logf("pulling feature %s", ref)
	manifest, _, err := r.OCI.FetchManifest(ref)
	if err != nil {
		return nil, err
	}
	if len(manifest.Layers) == 0 {
		return nil, fmt.Errorf("feature manifest has no layers")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for _, layer := range manifest.Layers {
		if err := r.fetchAndExtractLayer(ref, layer, dir); err != nil {
			return nil, err
		}
	}
	meta, err := readMetadata(dir)
	if err != nil {
		return nil, err
	}
	return &Resolved{
		Source: source, Reference: &ref, Options: opts, Metadata: meta, Dir: dir, SafeID: safeID,
	}, nil
}

func (r *Resolver) fetchAndExtractLayer(ref oci.Reference, layer oci.Descriptor, dir string) error {
	var buf bytes.Buffer
	if err := r.OCI.FetchBlob(ref, layer.Digest, &buf); err != nil {
		return err
	}
	return extractTarMaybeGz(buf.Bytes(), dir)
}

func extractTarMaybeGz(data []byte, dir string) error {
	var rdr io.Reader = bytes.NewReader(data)
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		rdr = gz
	}
	tr := tar.NewReader(rdr)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Sanitize: refuse paths that escape the destination.
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || strings.HasPrefix(clean, "/") {
			return fmt.Errorf("tar: unsafe path %q", hdr.Name)
		}
		target := filepath.Join(dir, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, fs.FileMode(hdr.Mode)|0o111); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

func readMetadata(dir string) (Metadata, error) {
	path := filepath.Join(dir, "devcontainer-feature.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("read %s: %w", path, err)
	}
	var meta Metadata
	if err := json.Unmarshal(spec.StripJSONC(data), &meta); err != nil {
		return Metadata{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if meta.ID == "" {
		return Metadata{}, fmt.Errorf("%s: missing id", path)
	}
	return meta, nil
}

// isLocalSource returns true for spec sources that should be loaded from
// the local filesystem (./, ../, or /absolute paths).
func isLocalSource(s string) bool {
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "/") {
		return true
	}
	if strings.HasPrefix(s, "file://") {
		return true
	}
	return false
}

// safeIdent transforms an arbitrary source string into a path-safe
// identifier. It uses a sha256 prefix plus the trailing path component to
// stay deterministic across invocations.
func safeIdent(s string) string {
	sum := sha256.Sum256([]byte(s))
	prefix := hex.EncodeToString(sum[:6])
	tail := s
	if i := strings.LastIndexAny(tail, "/:@"); i >= 0 {
		tail = tail[i+1:]
	}
	tail = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			return r
		}
		return '-'
	}, tail)
	if tail == "" {
		tail = "feature"
	}
	return prefix + "-" + tail
}

func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			ln, err := os.Readlink(path)
			if err != nil {
				return err
			}
			_ = os.Remove(target)
			return os.Symlink(ln, target)
		default:
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode())
		}
	})
}
