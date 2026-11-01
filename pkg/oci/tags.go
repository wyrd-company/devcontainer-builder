// relationships:
//
//	references: [oci, ../../reference/cli/src/spec-configuration/containerCollectionsOCI]
package oci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrTagListShape identifies a truthy non-array tags value which upstream
// cannot filter as a version list. Syntax and transport errors remain unavailable.
var ErrTagListShape = errors.New("published tags must be an array")

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
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	value := result["tags"]
	if value == nil || value == false || value == "" || value == float64(0) {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, ErrTagListShape
	}
	tags := []string{}
	for _, item := range items {
		if tag, ok := item.(string); ok {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}
