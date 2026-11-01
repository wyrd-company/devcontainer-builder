// relationships:
//
//	references: [oci, ../../reference/cli/src/spec-configuration/containerCollectionsOCI]
package oci

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ReadArtifact returns the original manifest bytes and canonical registry digest.
// A digest reference verifies the bytes even when the registry supplies a header.
func (c *Client) ReadArtifact(ref Reference) ([]byte, string, error) {
	target := manifestURL(ref)
	if strings.HasPrefix(ref.Registry, "localhost:") || ref.Registry == "localhost" {
		target = strings.Replace(target, "https:", "http:", 1)
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", mediaTypeOCIManifest)
	resp, err := c.do(req, ref)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode > 299 {
		return nil, "", c.refused(ref, fmt.Errorf("manifest: %s", resp.Status))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" || ref.Digest != "" {
		digest = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	}
	if ref.Digest != "" && digest != ref.Digest {
		return nil, "", fmt.Errorf("Digest did not match for %s/%s.", ref.Registry, ref.Repository)
	}
	return body, digest, nil
}
