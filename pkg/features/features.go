// relationships:
//   references: [spec, metadata]

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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Metadata is the parsed devcontainer-feature.json bundled inside each
// feature artifact.
type Metadata struct {
	Customizations    map[string]any    `json:"customizations,omitempty"`
	ID                string            `json:"id"`
	Version           string            `json:"version"`
	Name              string            `json:"name,omitempty"`
	Description       string            `json:"description,omitempty"`
	DocumentationURL  string            `json:"documentationURL,omitempty"`
	Options           map[string]Option `json:"options,omitempty"`
	ContainerEnv      map[string]string `json:"containerEnv,omitempty"`
	Mounts            []any             `json:"mounts,omitempty"`
	Init              *bool             `json:"init,omitempty"`
	Privileged        *bool             `json:"privileged,omitempty"`
	CapAdd            []string          `json:"capAdd,omitempty"`
	SecurityOpt       []string          `json:"securityOpt,omitempty"`
	Entrypoint        string            `json:"entrypoint,omitempty"`
	InstallsAfter     []string          `json:"installsAfter,omitempty"`
	CurrentID         string            `json:"currentId,omitempty"`
	DependsOn         spec.OrderedMap   `json:"dependsOn,omitempty"`
	LegacyIDs         []string          `json:"legacyIds,omitempty"`
	Deprecated        bool              `json:"deprecated,omitempty"`
	OnCreateCommand   spec.Command      `json:"onCreateCommand,omitempty"`
	UpdateContent     spec.Command      `json:"updateContentCommand,omitempty"`
	PostCreateCommand spec.Command      `json:"postCreateCommand,omitempty"`
	PostStartCommand  spec.Command      `json:"postStartCommand,omitempty"`
	PostAttachCommand spec.Command      `json:"postAttachCommand,omitempty"`
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
	// Digest is the manifest digest of an OCI feature. Empty for local
	// features.
	Digest string

	// localPath is the absolute source path of a local feature.
	localPath string
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

// Resolve fetches all features from the given spec into stagingDir. Local
// feature references are resolved relative to localBaseDir when they are not
// absolute. The returned slice is in install order.
func (r *Resolver) Resolve(specFeatures spec.OrderedMap, override []string, stagingDir, localBaseDir string) ([]Resolved, error) {
	if specFeatures.Len() == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, err
	}

	type pending struct {
		source string
		raw    json.RawMessage
		parent *node
	}
	worklist := make([]pending, 0, specFeatures.Len())
	for _, source := range specFeatures.Keys {
		worklist = append(worklist, pending{source: source, raw: specFeatures.Values[source]})
	}

	// Resolve user features, then their dependsOn features recursively.
	// A feature equal to one already resolved (same feature and options)
	// is installed once and satisfies every dependency on it.
	var nodes []*node
	link := func(parent, n *node) {
		if parent != nil {
			parent.dependsOn = append(parent.dependsOn, n)
		}
	}
	for len(worklist) > 0 {
		item := worklist[0]
		worklist = worklist[1:]
		options, optionsValue := decodeOptions(item.raw)

		if existing := findNode(nodes, func(n *node) bool {
			return n.res.Source == item.source && n.options == options
		}); existing != nil {
			link(item.parent, existing)
			continue
		}
		opts, scalar, err := parseOptions(item.raw)
		if err != nil {
			return nil, fmt.Errorf("feature %s: %w", item.source, err)
		}
		resolved, err := r.resolveOne(item.source, opts, options, stagingDir, localBaseDir)
		if err != nil {
			return nil, fmt.Errorf("feature %s: %w", item.source, err)
		}
		if _, ok := resolved.Metadata.Options["version"]; scalar && !ok {
			// Like upstream, a scalar sets "version" only when the
			// feature declares that option.
			resolved.Options = nil
		}
		n := &node{res: *resolved, options: options, optionsValue: optionsValue}
		if existing := findNode(nodes, n.equals); existing != nil {
			link(item.parent, existing)
			continue
		}
		nodes = append(nodes, n)
		link(item.parent, n)
		deps := n.res.Metadata.DependsOn
		for _, source := range deps.Keys {
			worklist = append(worklist, pending{source: source, raw: deps.Values[source], parent: n})
		}
	}

	aliases := map[string][]string{}
	lookup := func(id string) ([]string, error) {
		if a, ok := aliases[id]; ok {
			return a, nil
		}
		a, err := r.featureAliases(id)
		if err != nil {
			return nil, err
		}
		aliases[id] = a
		return a, nil
	}
	out, err := installOrder(nodes, override, localBaseDir, lookup)
	if err != nil {
		return nil, fmt.Errorf("feature install order: %w", err)
	}
	return out, nil
}

func findNode(nodes []*node, match func(*node) bool) *node {
	for _, n := range nodes {
		if match(n) {
			return n
		}
	}
	return nil
}

func (r *Resolver) resolveOne(source string, opts map[string]any, options, stagingDir, localBaseDir string) (*Resolved, error) {
	// The same feature may be installed more than once with different
	// options (via dependsOn), so the staging directory is keyed by both.
	safeID := safeIdent(source, options)
	dir := filepath.Join(stagingDir, safeID)

	if isLocalSource(source) {
		abs, err := localSourcePath(source, localBaseDir)
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
			Source: source, Options: opts, Metadata: meta, Dir: dir, SafeID: safeID, localPath: abs,
		}, nil
	}

	ref, err := oci.ParseReference(source)
	if err != nil {
		return nil, fmt.Errorf("not a local path and not a valid OCI ref: %w", err)
	}
	r.logf("pulling feature %s", ref)
	manifest, rawManifest, err := r.OCI.FetchManifest(ref)
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
	sum := sha256.Sum256(rawManifest)
	return &Resolved{
		Source: source, Reference: &ref, Options: opts, Metadata: meta, Dir: dir, SafeID: safeID,
		Digest: "sha256:" + hex.EncodeToString(sum[:]),
	}, nil
}

func (r *Resolver) fetchAndExtractLayer(ref oci.Reference, layer oci.Descriptor, dir string) error {
	var buf bytes.Buffer
	if err := r.OCI.FetchBlob(ref, layer.Digest, &buf); err != nil {
		return err
	}
	return extractTarMaybeGz(buf.Bytes(), dir)
}

// aliases returns the feature's current id (currentId, else id)
// followed by its legacy ids.
func (m Metadata) aliases() []string {
	var out []string
	switch {
	case m.CurrentID != "":
		out = append(out, m.CurrentID)
	case m.ID != "":
		out = append(out, m.ID)
	}
	return append(out, m.LegacyIDs...)
}

// featureAliases reads the aliases of the OCI feature id references, from
// the manifest's dev.containers.metadata annotation when present, else
// from the feature's devcontainer-feature.json.
func (r *Resolver) featureAliases(id string) ([]string, error) {
	ref, err := oci.ParseReference(id)
	if err != nil {
		return nil, err
	}
	r.logf("reading feature metadata %s", ref)
	manifest, _, err := r.OCI.FetchManifest(ref)
	if err != nil {
		return nil, err
	}
	if annotation := manifest.Annotations["dev.containers.metadata"]; annotation != "" {
		var meta Metadata
		if err := json.Unmarshal(spec.StripJSONC([]byte(annotation)), &meta); err != nil {
			return nil, fmt.Errorf("parse dev.containers.metadata: %w", err)
		}
		return meta.aliases(), nil
	}
	dir, err := os.MkdirTemp("", "devcontainer-feature-metadata-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for _, layer := range manifest.Layers {
		if err := r.fetchAndExtractLayer(ref, layer, dir); err != nil {
			return nil, err
		}
	}
	meta, err := readMetadata(dir)
	if err != nil {
		return nil, err
	}
	return meta.aliases(), nil
}

func readMetadata(dir string) (Metadata, error) {
	path := filepath.Join(dir, "devcontainer-feature.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("read %s: %w", path, err)
	}
	data, _, err = spec.MigrateLegacyVSCode(spec.StripJSONC(data), false)
	if err != nil {
		return Metadata{}, fmt.Errorf("parse %s: %w", path, err)
	}
	var meta Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
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

func localSourcePath(source, baseDir string) (string, error) {
	var p string
	if strings.HasPrefix(source, "file://") {
		u, err := url.Parse(source)
		if err != nil {
			return "", fmt.Errorf("parse file URL: %w", err)
		}
		if u.Scheme != "file" {
			return "", fmt.Errorf("unsupported local feature URL scheme %q", u.Scheme)
		}
		if u.Host != "" && u.Host != "localhost" {
			return "", fmt.Errorf("file URL host %q is not supported", u.Host)
		}
		if u.Path == "" {
			return "", fmt.Errorf("file URL has no path")
		}
		p = u.Path
	} else {
		p = source
	}
	if !filepath.IsAbs(p) {
		if baseDir == "" {
			var err error
			baseDir, err = os.Getwd()
			if err != nil {
				return "", err
			}
		}
		p = filepath.Join(baseDir, p)
	}
	return filepath.Abs(p)
}
