// relationships:
//   references: [metadata]

// Package oci is a small read-only OCI Distribution v2 client. It pulls
// manifests and blobs anonymously or with the registry credentials of the
// Docker client configuration, through Basic or Bearer-token challenges,
// which is sufficient for fetching devcontainer features published as OCI
// artifacts (the "ghcr.io/devcontainers/features/*" namespace, for
// instance).
//
// This client does not do content verification beyond digest matching, and
// is intentionally minimal — it's not a substitute for go-containerregistry
// or oras-go in production-grade tooling.
package oci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

const (
	mediaTypeOCIManifest      = "application/vnd.oci.image.manifest.v1+json"
	mediaTypeOCIIndex         = "application/vnd.oci.image.index.v1+json"
	mediaTypeDockerManifest   = "application/vnd.docker.distribution.manifest.v2+json"
	mediaTypeDockerManifestL  = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaTypeDevcontainerFeat = "application/vnd.devcontainers.layer.v1+tar"
)

// Reference is a parsed image reference of the form
// registry/repository:tag (or @digest).
type Reference struct {
	Registry   string
	Repository string
	Tag        string
	Digest     string
}

func (r Reference) String() string {
	suffix := ":" + r.Tag
	if r.Digest != "" {
		suffix = "@" + r.Digest
	}
	return r.Registry + "/" + r.Repository + suffix
}

// ParseReference parses an OCI reference. If no registry is specified,
// docker.io is assumed (with an "library/" prefix for single-segment names,
// matching docker conventions).
func ParseReference(ref string) (Reference, error) {
	r := Reference{Tag: "latest"}
	if i := strings.Index(ref, "@"); i >= 0 {
		r.Digest = ref[i+1:]
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i >= 0 {
		// Make sure the colon is in the tag segment (not a port number).
		if !strings.Contains(ref[i:], "/") {
			r.Tag = ref[i+1:]
			ref = ref[:i]
		}
	}
	parts := strings.SplitN(ref, "/", 2)
	switch {
	case len(parts) == 2 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost"):
		r.Registry = parts[0]
		r.Repository = parts[1]
	case len(parts) == 2:
		r.Registry = "registry-1.docker.io"
		r.Repository = ref
	default:
		r.Registry = "registry-1.docker.io"
		r.Repository = "library/" + ref
	}
	if r.Registry == "" || r.Repository == "" {
		return Reference{}, fmt.Errorf("invalid reference %q", ref)
	}
	return r, nil
}

// Client performs read operations against an OCI distribution registry.
type Client struct {
	HTTP *http.Client
	// Auth supplies registry credentials. When nil, every registry is
	// accessed anonymously.
	Auth          CredentialSource
	authorization map[string]string // registry-host -> Authorization header
}

// NewClient constructs a Client using sensible defaults. It reads
// registry credentials from the Docker client configuration.
func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			Timeout: 5 * time.Minute,
		},
		Auth:          registryauth.FromEnvironment(),
		authorization: map[string]string{},
	}
}

// WithAuth returns a client that reads registry credentials from auth
// and shares c's HTTP client. Its authorizations start empty, so that a
// token c obtained with its own credentials is never sent for auth. A nil
// c gives NewClient's HTTP client.
func (c *Client) WithAuth(auth CredentialSource) *Client {
	out := NewClient()
	if c != nil && c.HTTP != nil {
		out.HTTP = c.HTTP
	}
	out.Auth = auth
	return out
}

// Manifest is a thin model of an OCI image manifest.
type Manifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	MediaType     string        `json:"mediaType"`
	ArtifactType  string        `json:"artifactType,omitempty"`
	Config        Descriptor    `json:"config"`
	Layers        []Descriptor  `json:"layers"`
	Annotations   ManifestAnnot `json:"annotations,omitempty"`
}

// Index is a thin model of an OCI index / Docker manifest list.
type Index struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []Descriptor `json:"manifests"`
}

// Descriptor is the OCI content descriptor used by manifests.
type Descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Platform    *Platform         `json:"platform,omitempty"`
}

// Platform is the OCI platform object found on index descriptors.
type Platform struct {
	OS           string `json:"os,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Variant      string `json:"variant,omitempty"`
}

// ManifestAnnot is the typed map for manifest-level annotations.
type ManifestAnnot map[string]string

// FetchManifest fetches a manifest by reference, following indexes if
// needed by selecting the first child manifest. (For features, indexes are
// rare; when present we just take whatever is first.)
func (c *Client) FetchManifest(ref Reference) (*Manifest, []byte, error) {
	return c.fetchManifest(ref, ref.String(), 0, firstManifest)
}

// childSelector picks the manifest to follow from an index.
type childSelector func([]Descriptor) (Descriptor, error)

func firstManifest(ds []Descriptor) (Descriptor, error) {
	return ds[0], nil
}

// platformManifest selects the manifest for p. An empty variant matches
// any variant.
func platformManifest(p Platform) childSelector {
	return func(ds []Descriptor) (Descriptor, error) {
		for _, d := range ds {
			if d.Platform == nil || d.Platform.OS != p.OS || d.Platform.Architecture != p.Architecture {
				continue
			}
			if p.Variant == "" || d.Platform.Variant == p.Variant {
				return d, nil
			}
		}
		return Descriptor{}, fmt.Errorf("manifest index has no %s/%s manifest", p.OS, p.Architecture)
	}
}

// ImageConfig is the subset of an OCI image configuration this package
// reads.
type ImageConfig struct {
	Config struct {
		Labels     map[string]string `json:"Labels,omitempty"`
		Entrypoint []string          `json:"Entrypoint,omitempty"`
		Cmd        []string          `json:"Cmd,omitempty"`
		User       string            `json:"User,omitempty"`
		Env        []string          `json:"Env,omitempty"`
	} `json:"config"`
}

// FetchImageConfig returns the configuration of the image ref for
// platform p, following a multi-platform index to p's manifest.
func (c *Client) FetchImageConfig(ref Reference, p Platform) (*ImageConfig, error) {
	m, _, err := c.fetchManifest(ref, ref.String(), 0, platformManifest(p))
	if err != nil {
		return nil, err
	}
	if m.Config.Digest == "" {
		return nil, fmt.Errorf("manifest %s has no config", ref)
	}
	var buf strings.Builder
	if err := c.FetchBlob(ref, m.Config.Digest, &buf); err != nil {
		return nil, err
	}
	var cfg ImageConfig
	if err := json.Unmarshal([]byte(buf.String()), &cfg); err != nil {
		return nil, fmt.Errorf("image config of %s is malformed", ref)
	}
	return &cfg, nil
}

// fetchManifest fetches ref's manifest, following an index. Errors name
// the reference as name, the reference the caller asked for, so that no
// server-supplied digest reaches them.
func (c *Client) fetchManifest(ref Reference, name string, depth int, choose childSelector) (*Manifest, []byte, error) {
	if depth > 4 {
		return nil, nil, fmt.Errorf("manifest %s: index nesting too deep", name)
	}
	req, err := http.NewRequest(http.MethodGet, manifestURL(ref), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("manifest %s: invalid reference", name)
	}
	req.Header.Set("Accept", strings.Join([]string{
		mediaTypeOCIManifest,
		mediaTypeOCIIndex,
		mediaTypeDockerManifest,
		mediaTypeDockerManifestL,
	}, ", "))
	resp, err := c.do(req, ref)
	if err != nil {
		return nil, nil, fmt.Errorf("manifest %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("manifest %s: %s", name, statusText(resp.StatusCode))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("manifest %s: reading the response failed", name)
	}
	mediaType := strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0])
	var envelope struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, nil, fmt.Errorf("manifest %s is malformed", name)
	}
	if envelope.MediaType != "" {
		mediaType = envelope.MediaType
	}
	switch mediaType {
	case mediaTypeOCIIndex, mediaTypeDockerManifestL:
		var idx Index
		if err := json.Unmarshal(body, &idx); err != nil {
			return nil, nil, fmt.Errorf("manifest index %s is malformed", name)
		}
		if len(idx.Manifests) == 0 {
			return nil, nil, fmt.Errorf("manifest index %s has no manifests", name)
		}
		child, err := choose(idx.Manifests)
		if err != nil {
			return nil, nil, fmt.Errorf("manifest index %s: %w", name, err)
		}
		if !validDigest(child.Digest) {
			return nil, nil, fmt.Errorf("manifest index %s lists an invalid digest", name)
		}
		childRef := ref
		childRef.Tag = ""
		childRef.Digest = child.Digest
		m, childBody, err := c.fetchManifest(childRef, name, depth+1, choose)
		if err != nil {
			return nil, nil, err
		}
		if !matchesDigest(child.Digest, childBody) {
			return nil, nil, fmt.Errorf("manifest index %s: child manifest does not match its digest", name)
		}
		return m, childBody, nil
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, nil, fmt.Errorf("manifest %s is malformed", name)
	}
	return &m, body, nil
}

// FetchBlob streams a blob to the supplied writer. It verifies the digest
// before returning success. Errors name ref, not the digest, which can
// come from a manifest.
func (c *Client) FetchBlob(ref Reference, digest string, w io.Writer) error {
	if !validDigest(digest) {
		return fmt.Errorf("blob of %s: invalid digest", ref)
	}
	req, err := http.NewRequest(http.MethodGet, blobURL(ref, digest), nil)
	if err != nil {
		return fmt.Errorf("blob of %s: invalid reference", ref)
	}
	resp, err := c.do(req, ref)
	if err != nil {
		return fmt.Errorf("blob of %s: %w", ref, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("blob of %s: %s", ref, statusText(resp.StatusCode))
	}
	hasher := sha256.New()
	body := &readErrRecorder{r: resp.Body}
	if _, err := io.Copy(io.MultiWriter(w, hasher), body); err != nil {
		if body.err != nil {
			return fmt.Errorf("blob of %s: reading the response failed", ref)
		}
		return fmt.Errorf("blob of %s: %w", ref, err)
	}
	got := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(got, digest) {
		return fmt.Errorf("blob of %s does not match its digest", ref)
	}
	return nil
}

// readErrRecorder records a read error, so that it can be told apart from
// an error of the destination writer.
type readErrRecorder struct {
	r   io.Reader
	err error
}

func (r *readErrRecorder) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

// digestPattern is a sha256 digest, the only algorithm the client
// verifies.
var digestPattern = regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`)

func validDigest(d string) bool { return digestPattern.MatchString(d) }

func matchesDigest(want string, body []byte) bool {
	sum := sha256.Sum256(body)
	return strings.EqualFold("sha256:"+hex.EncodeToString(sum[:]), want)
}

func manifestURL(ref Reference) string {
	target := ref.Tag
	if ref.Digest != "" {
		target = ref.Digest
	}
	return fmt.Sprintf("https://%s/v2/%s/manifests/%s", ref.Registry, ref.Repository, target)
}

func blobURL(ref Reference, digest string) string {
	return fmt.Sprintf("https://%s/v2/%s/blobs/%s", ref.Registry, ref.Repository, digest)
}
