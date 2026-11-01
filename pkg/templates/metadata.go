// Package templates reads published development container Template metadata.
// relationships:
//
//	references: [../oci/oci, ../../reference/cli/src/spec-node/templatesCLI/metadata]
package templates

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

// ErrUnavailable means the reference, artifact type or metadata is unavailable.
// The upstream command emits an empty object for this class of failure.
var ErrUnavailable = errors.New("Template metadata unavailable")

var collectionPath = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)
var collectionTag = regexp.MustCompile(`^[a-z0-9_][a-z0-9._-]{0,127}$`)

// Metadata fetches the dev.containers.metadata annotation of a published
// Template. It returns the annotation's JSON value, including scalar or null
// values, without fetching layers. A nil client uses Docker registry credentials.
// References follow upstream collection syntax, with an explicit registry,
// lowercase normalization, latest by default, and optional sha256 digest.
func Metadata(client *oci.Client, identifier string) (json.RawMessage, error) {
	ref, err := metadataReference(identifier)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if client == nil {
		client = oci.NewClient()
	}
	manifest, body, err := client.FetchArtifactManifest(ref)
	if err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if ref.Digest != "" {
		digest := sha256.Sum256(body)
		if "sha256:"+hex.EncodeToString(digest[:]) != ref.Digest {
			return nil, fmt.Errorf("Template manifest does not match its digest")
		}
	}
	if manifest.Config.MediaType != "application/vnd.devcontainers" {
		return nil, fmt.Errorf("%w: unexpected manifest config media type %q", ErrUnavailable, manifest.Config.MediaType)
	}
	metadata := manifest.Annotations["dev.containers.metadata"]
	if metadata == "" {
		return nil, fmt.Errorf("%w: manifest has no metadata; ask its owner to republish it", ErrUnavailable)
	}
	if !json.Valid([]byte(metadata)) {
		return nil, fmt.Errorf("Template manifest metadata is not valid JSON")
	}
	return json.RawMessage(metadata), nil
}

func metadataReference(identifier string) (oci.Reference, error) {
	input := strings.ToLower(identifier)
	invalid := func() (oci.Reference, error) {
		return oci.Reference{}, fmt.Errorf("invalid Template reference %q", identifier)
	}
	if strings.HasPrefix(input, ".") {
		return invalid()
	}
	resource, tag, digest := input, "latest", ""
	if at := strings.LastIndex(input, "@"); at >= 0 {
		resource, digest = input[:at], input[at+1:]
		parts := strings.Split(digest, ":")
		if len(parts) != 2 || parts[0] != "sha256" {
			return invalid()
		}
	} else if colon := strings.LastIndex(input, ":"); colon > strings.LastIndex(input, "/") {
		resource, tag = input[:colon], input[colon+1:]
	}
	if tag != "" && !collectionTag.MatchString(tag) {
		return invalid()
	}
	parts := strings.Split(resource, "/")
	if len(parts) < 2 {
		return invalid()
	}
	if parts[1] == "devcontainers-contrib" {
		parts[1] = "devcontainers-extra"
	}
	path := strings.Join(parts[1:], "/")
	if !collectionPath.MatchString(path) || (!strings.Contains(parts[0], ".") && !strings.HasPrefix(parts[0], "localhost")) {
		return invalid()
	}
	if tag == "" {
		tag = "latest"
	}
	return oci.Reference{Registry: parts[0], Repository: path, Tag: tag, Digest: digest}, nil
}
