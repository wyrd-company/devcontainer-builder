// relationships:
//
//	references: [features, lockfile, ../oci/tags, ../../reference/cli/src/spec-configuration/containerFeaturesConfiguration]
package features

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/wyrd-company/devcontainer-builder/internal/collectiondocs"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// FeatureVersionInfo omits unavailable values, as upstream JSON.stringify does.
type FeatureVersionInfo struct {
	Current     string `json:"current,omitempty"`
	Wanted      string `json:"wanted,omitempty"`
	WantedMajor string `json:"wantedMajor,omitempty"`
	Latest      string `json:"latest,omitempty"`
	LatestMajor string `json:"latestMajor,omitempty"`
}

// VersionReport preserves configuration order in both JSON and text consumers.
type VersionReport struct {
	Features map[string]FeatureVersionInfo
	Order    []string
}

func (r VersionReport) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"features":{`)
	for i, id := range r.Order {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(id)
		value, err := json.Marshal(r.Features[id])
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteString(`}}`)
	return b.Bytes(), nil
}

var featurePath = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)
var featureTag = regexp.MustCompile(`^[a-z0-9_][a-z0-9._-]{0,127}$`)
var reportingVariable = regexp.MustCompile(`\$\{(.*?)\}`)
var versionDelimiter = regexp.MustCompile(`[:@][^/]*$`)

// FeatureIDWithoutVersion removes the final tag or digest, preserving ports.
func FeatureIDWithoutVersion(id string) string {
	if loc := versionDelimiter.FindStringIndex(id); loc != nil {
		return id[:loc[0]]
	}
	return id
}

// versionReference follows upstream getRef rather than Docker image shorthand.
func versionReference(id string) (oci.Reference, bool) {
	input := strings.ToLower(id)
	if strings.HasPrefix(input, ".") {
		return oci.Reference{}, false
	}
	resource, tag, digest := input, "latest", ""
	if at := strings.LastIndex(input, "@"); at >= 0 {
		resource, digest, tag = input[:at], input[at+1:], ""
		p := strings.Split(digest, ":")
		if len(p) != 2 || p[0] != "sha256" {
			return oci.Reference{}, false
		}
	} else if colon := strings.LastIndex(input, ":"); colon > strings.LastIndex(input, "/") {
		resource, tag = input[:colon], input[colon+1:]
	}
	if tag != "" && !featureTag.MatchString(tag) {
		return oci.Reference{}, false
	}
	parts := strings.Split(resource, "/")
	if len(parts) < 2 {
		return oci.Reference{}, false
	}
	if parts[1] == "devcontainers-contrib" {
		parts[1] = "devcontainers-extra"
	}
	path := strings.Join(parts[1:], "/")
	if !featurePath.MatchString(path) {
		return oci.Reference{}, false
	}
	if tag == "" && digest == "" {
		tag = "latest"
	}
	return oci.Reference{Registry: parts[0], Repository: path, Tag: tag, Digest: digest}, true
}

func strictVersion(tag string) (*semver.Version, error) {
	if len(tag) > 256 {
		return nil, fmt.Errorf("version is too long")
	}
	version, err := semver.StrictNewVersion(strings.TrimPrefix(strings.TrimSpace(tag), "v"))
	if err == nil && (version.Major() > 9007199254740991 || version.Minor() > 9007199254740991 || version.Patch() > 9007199254740991) {
		return nil, fmt.Errorf("version exceeds JavaScript integer range")
	}
	return version, err
}

// StrictSortedVersions retains full valid semver tags (including prereleases
// and v prefixes), in ascending stable precedence order, like node-semver.
func StrictSortedVersions(tags []string) []string {
	out := []string{}
	for _, tag := range tags {
		if _, err := strictVersion(tag); err == nil {
			out = append(out, tag)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := strictVersion(out[i])
		b, _ := strictVersion(out[j])
		return a.LessThan(b)
	})
	return out
}

// LoadVersionInfo reports declared OCI Features without resolving dependencies,
// installing Features, or writing the lockfile. Tag-list failures leave fields
// unavailable. Digest metadata uses the annotation, then the first OCI layer.
// A nil client uses the existing Docker registry credential integration.
func LoadVersionInfo(client *oci.Client, config *spec.DevContainer) (VersionReport, error) {
	result := VersionReport{Features: map[string]FeatureVersionInfo{}}
	if config.Features.Len() == 0 {
		return result, nil
	}
	state, err := ReadLockfile(config.Path, false, false)
	if err != nil {
		return result, err
	}
	if client == nil {
		client = oci.NewClient()
	}
	for _, id := range config.Features.Keys {
		ref, ok := versionReference(id)
		if !ok {
			continue
		}
		tags, _ := client.PublishedTags(ref)
		versions := StrictSortedVersions(tags)
		v := FeatureVersionInfo{}
		if state.Lockfile != nil {
			v.Current = state.Lockfile.Features[id].Version
		}
		if len(versions) > 0 {
			v.Latest = versions[len(versions)-1]
		}
		if ref.Digest != "" {
			v.Wanted = v.Current
			if v.Wanted == "" {
				v.Wanted, err = digestVersion(client, ref)
				if err != nil {
					return result, err
				}
			}
		} else if ref.Tag == "latest" {
			v.Wanted = v.Latest
		} else {
			constraint, e := semver.NewConstraint(ref.Tag)
			if e == nil {
				for i := len(versions) - 1; i >= 0; i-- {
					candidate, _ := strictVersion(versions[i])
					if constraint.Check(candidate) {
						v.Wanted = versions[i]
						break
					}
				}
			}
		}
		if v.Current == "" {
			v.Current = v.Wanted
		}
		for _, field := range []struct {
			value  string
			target *string
		}{{v.Wanted, &v.WantedMajor}, {v.Latest, &v.LatestMajor}} {
			if field.value != "" {
				parsed, e := strictVersion(field.value)
				if e != nil {
					return result, fmt.Errorf("invalid Feature version %q: %w", field.value, e)
				}
				*field.target = fmt.Sprint(parsed.Major())
			}
		}
		result.Order = append(result.Order, id)
		result.Features[id] = v
	}
	return result, nil
}

func digestVersion(client *oci.Client, ref oci.Reference) (string, error) {
	manifest, body, err := client.FetchArtifactManifest(ref)
	if err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return "", err
		}
		return "", nil
	} // Upstream falls back to a non-OCI source.
	if expected := fmt.Sprintf("sha256:%x", sha256.Sum256(body)); expected != ref.Digest {
		return "", fmt.Errorf("Digest did not match for %s/%s", ref.Registry, ref.Repository)
	}
	var shape map[string]json.RawMessage
	_ = json.Unmarshal(body, &shape)
	if value, ok := shape["config"]; !ok || string(value) == "null" {
		return "", fmt.Errorf("Feature manifest has no config object")
	}
	if manifest.Config.MediaType != "application/vnd.devcontainers" {
		return "", nil
	}
	data := []byte(manifest.Annotations["dev.containers.metadata"])
	if len(data) == 0 {
		if len(manifest.Layers) == 0 {
			return "", fmt.Errorf("Feature manifest has no layer")
		}
		dir, err := os.MkdirTemp("", "dcb-outdated-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(dir)
		resolver := Resolver{OCI: client}
		if err := resolver.fetchAndExtractLayer(ref, manifest.Layers[0], dir); err != nil {
			return "", err
		}
		data, err = os.ReadFile(filepath.Join(dir, "devcontainer-feature.json"))
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
	}
	var version string
	raw := collectiondocs.ReadJSONCProperty(data, "version")
	if err := json.Unmarshal(raw, &version); err != nil {
		return "", err
	}
	return version, nil
}

// ReadVersionConfig discovers and reads a reporting configuration. Unlike a
// build, reporting does not require image/build/Compose discriminators. Feature
// identifiers are object keys: upstream substitutes values, never these keys.
// Feature options and other substituted values do not affect version reporting.
func ReadVersionConfig(workspace, configPath string) (*spec.DevContainer, error) {
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if configPath == "" {
		configPath = filepath.Join(workspace, ".devcontainer", "devcontainer.json")
		for _, candidate := range []string{configPath, filepath.Join(workspace, ".devcontainer.json")} {
			if _, e := os.Stat(candidate); e == nil {
				configPath = candidate
				break
			}
		}
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	data = collectiondocs.ReadJSONCValue(data)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("config must contain a JSON object literal")
	}
	// Versioning does not consume substituted values, but upstream still
	// rejects an environment substitution without a variable name anywhere
	// in the configuration. Inspect values only: keys are never substituted.
	var document any
	_ = json.Unmarshal(data, &document)
	if err := validateReportSubstitutions(document); err != nil {
		return nil, err
	}
	dc := &spec.DevContainer{Path: configPath}
	if value, ok := raw["features"]; ok && bytes.HasPrefix(bytes.TrimSpace(value), []byte("{")) {
		if err := json.Unmarshal(value, &dc.Features); err != nil {
			return nil, err
		}
	}
	// JavaScript objects keep only the last duplicate key, at its first position.
	seen := map[string]bool{}
	keys := []string{}
	for _, id := range dc.Features.Keys {
		if !seen[id] {
			keys = append(keys, id)
			seen[id] = true
		}
	}
	dc.Features.Keys = keys
	return dc, nil
}

func validateReportSubstitutions(value any) error {
	switch v := value.(type) {
	case string:
		for _, match := range reportingVariable.FindAllString(v, -1) {
			if match == "${env}" || match == "${localEnv}" {
				return fmt.Errorf("environment substitution can not be resolved because no environment variable name is given")
			}
		}
	case []any:
		for _, item := range v {
			if err := validateReportSubstitutions(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range v {
			if err := validateReportSubstitutions(item); err != nil {
				return err
			}
		}
	}
	return nil
}
