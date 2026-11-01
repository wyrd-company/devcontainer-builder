// relationships:
//
//	references: [oci, ../../reference/cli/src/spec-configuration/containerCollectionsOCI]
package oci

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// PublishedTags lists the registry's tags as-is using the client's registry
// authentication. A missing repository has no tags. Pagination is intentionally
// not followed, matching upstream collection discovery.
func (c *Client) PublishedTags(ref Reference) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://"+ref.Registry+"/v2/"+ref.Repository+"/tags/list", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.do(req, ref)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode > 299 {
		return nil, c.refused(ref, fmt.Errorf("published tags: %s", resp.Status))
	}
	var result struct {
		Tags []string `json:"tags"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	return result.Tags, err
}
