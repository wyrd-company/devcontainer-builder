// relationships:
//
//	references: [features, order, ../oci/artifact, ../../reference/cli/src/spec-node/featuresCLI/info]
package features

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/internal/collectiondocs"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// ErrInfoUnavailable marks an unavailable Feature report, with its upstream output.
var ErrInfoUnavailable = errors.New("Feature information unavailable")

// Info queries a published Feature. JSON dependencies deliberately returns an
// empty object after manifest admission, without fetching dependency metadata.
// A nil client uses the existing registry credential integration.
func Info(client *oci.Client, mode, identifier, format string) (string, error) {
	if mode != "manifest" && mode != "tags" && mode != "dependencies" && mode != "verbose" {
		return "", fmt.Errorf("invalid info mode %q", mode)
	}
	if format != "text" && format != "json" {
		return "", fmt.Errorf("invalid output format %q", format)
	}
	fail := func(text string) (string, error) {
		if format == "json" {
			text = "{}"
		}
		return text + "\n", ErrInfoUnavailable
	}
	ref, ok := versionReference(identifier)
	if !ok {
		if format == "json" {
			return "{} 3\n", ErrInfoUnavailable
		}
		return fail(fmt.Sprintf("Failed to parse Feature identifier '%s'\n 5", identifier))
	}
	if client == nil {
		client = oci.NewClient()
	}
	body, digest, err := infoManifest(client, ref)
	if errors.Is(err, ErrInfoUnavailable) {
		return fail("No manifest found! If this manifest requires authentication, please login.")
	}
	if err != nil {
		return "", err
	}
	var text strings.Builder
	fields := []infoField{}
	if mode == "manifest" || mode == "verbose" {
		canonical := ref.Registry + "/" + ref.Repository + "@" + digest
		if format == "text" {
			pretty, e := infoPretty(body, 2)
			if e != nil {
				return "", e
			}
			fmt.Fprintf(&text, "%s\n%s\n\n%s\n%s\n\n", infoBox("Manifest"), pretty, infoBox("Canonical Identifier"), canonical)
		} else {
			fields = append(fields, infoField{"manifest", body}, infoField{"canonicalId", infoJSON(canonical)})
		}
	}
	if mode == "tags" || mode == "verbose" {

		tags, e := client.PublishedTagValue(ref)
		var value any
		_ = json.Unmarshal(tags, &value)
		items, array := value.([]any)
		empty := value == nil || value == false || value == "" || value == float64(0) || (array && len(items) == 0)
		if str, ok := value.(string); ok && str == "" {
			empty = true
		}
		if e != nil || empty {
			out, e := fail(fmt.Sprintf("No published versions found for feature '%s/%s'\n", ref.Registry, ref.Repository))
			return text.String() + out, e
		}
		if format == "text" {
			if !array {
				return text.String() + infoBox("Published Tags") + "\n", fmt.Errorf("publishedTags.join is not a function")
			}
			rendered := make([]string, len(items))
			for i, item := range items {
				if item != nil {
					rendered[i] = fmt.Sprint(item)
				}
			}
			fmt.Fprintf(&text, "%s\n%s\n", infoBox("Published Tags"), strings.Join(rendered, "\n   "))
		} else {
			fields = append(fields, infoField{"publishedTags", infoNormalize(tags)})
		}
	}
	if (mode == "dependencies" || mode == "verbose") && format == "text" {
		diagram, e := infoGraph(client, identifier)
		if e != nil {
			return text.String(), e
		}
		fmt.Fprintf(&text, "%s\n%s\n", infoBox("Dependency Tree (Render with https://mermaid.live/)"), diagram)
	}
	if format == "json" {
		pretty, e := infoPretty(infoObject(fields...), 4)
		if e != nil {
			return "", e
		}
		return pretty + "\n", nil
	}
	return text.String(), nil
}

func infoManifest(c *oci.Client, ref oci.Reference) (json.RawMessage, string, error) {
	if !strings.Contains(ref.Registry, ".") && !strings.HasPrefix(ref.Registry, "localhost") {
		return nil, "", ErrInfoUnavailable
	}
	body, digest, err := c.ReadArtifact(ref)
	if err != nil {
		if strings.Contains(err.Error(), "Digest did not match") {
			return nil, "", err
		}
		return nil, "", ErrInfoUnavailable
	}
	if !json.Valid(body) {
		return nil, "", fmt.Errorf("invalid manifest JSON")
	}
	if string(bytes.TrimSpace(body)) == "null" {
		return nil, "", ErrInfoUnavailable
	}
	var m struct {
		Config *struct {
			MediaType string `json:"mediaType"`
		} `json:"config"`
	}
	if err = json.Unmarshal(body, &m); err != nil {
		return nil, "", err
	}
	if m.Config == nil {
		return nil, "", fmt.Errorf("manifest has no config object")
	}
	if m.Config.MediaType != "application/vnd.devcontainers" {
		return nil, "", ErrInfoUnavailable
	}
	return infoNormalize(body), digest, nil
}

type infoField struct {
	key   string
	value json.RawMessage
}

func infoJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return infoNormalize(b) }
func infoNormalize(b []byte) json.RawMessage {
	return collectiondocs.StringifyJSONValue(b)
}
func infoObject(fields ...infoField) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(infoJSON(f.key))
		b.WriteByte(':')
		b.Write(f.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}
func infoPretty(b []byte, n int) (string, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, b, "", strings.Repeat(" ", n)); err != nil {
		return "", err
	}
	return out.String(), nil
}
func infoBox(s string) string {
	return "┌" + strings.Repeat("─", len(s)) + "┐\n│\x1b[1m" + s + "\x1b[22m│\n└" + strings.Repeat("─", len(s)) + "┘"
}

type infoNode struct {
	configPath, workspace, integrity string
	priority                         int
	plainSet                         json.RawMessage
	id, kind                         string
	options                          json.RawMessage
	set                              json.RawMessage
	aliases                          []string
	hard, soft                       []*infoNode
	compare                          *node
}

func (n *infoNode) raw() json.RawMessage {
	hard, soft := []json.RawMessage{}, []json.RawMessage{}
	for _, d := range n.hard {
		hard = append(hard, d.raw())
	}
	for _, d := range n.soft {
		soft = append(soft, d.raw())
	}
	fields := []infoField{{"type", infoJSON(n.kind)}, {"userFeatureId", infoJSON(n.id)}, {"options", n.options}}
	if n.kind != "user-provided" && n.set != nil {
		fields = append(fields, infoField{"featureSet", n.set})
	}
	fields = append(fields, infoField{"dependsOn", infoJSON(hard)}, infoField{"installsAfter", infoJSON(soft)}, infoField{"roundPriority", infoJSON(n.priority)})
	if n.kind == "user-provided" && n.set != nil {
		fields = append(fields, infoField{"featureSet", n.set})
	}
	if n.aliases != nil {
		fields = append(fields, infoField{"featureIdAliases", infoJSON(n.aliases)})
	}
	return infoObject(fields...)
}
func (n *infoNode) label() string {
	sum := sha256.Sum256(n.raw())
	aliases := ""
	if len(n.aliases) > 0 {
		aliases = "<br>aliases: " + strings.Join(n.aliases, ", ")
	}
	return fmt.Sprintf("%x[%s<br/><%d>%s]", sum[:3], n.id, n.priority, aliases)
}

func infoGraph(c *oci.Client, id string) (string, error) {
	root := &infoNode{id: id, kind: "user-provided", options: json.RawMessage(`{}`)}
	queue := []*infoNode{root}
	all := []*infoNode{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		meta, err := n.load(c, true)
		if err != nil {
			return "", err
		}
		duplicate := false
		for _, old := range all {
			if old.compare.equals(n.compare) {
				duplicate = true
				break
			}
		}
		if duplicate {
			n.aliases = nil
			n.set = n.plainSet
			continue
		}
		var m Metadata
		_ = json.Unmarshal(meta, &m)
		for _, dep := range m.DependsOn.Keys {
			d := &infoNode{id: dep, kind: "resolved", options: infoNormalize(m.DependsOn.Values[dep])}
			n.hard = append(n.hard, d)
			queue = append(queue, d)
		}
		for _, dep := range m.InstallsAfter {
			d := &infoNode{id: dep, kind: "resolved", options: json.RawMessage(`{}`)}
			if _, err := d.load(c, false); err != nil {
				return "", err
			}
			n.soft = append(n.soft, d)
		}
		all = append(all, n)
	}
	var out strings.Builder
	out.WriteString("flowchart\n")
	out.WriteString(root.label() + "\n")
	var walk func(*infoNode)
	walk = func(n *infoNode) {
		for _, d := range n.hard {
			for _, w := range all {
				if w.compare.equals(d.compare) {
					out.WriteString(n.label() + " --> " + w.label() + "\n")
				}
			}
			walk(d)
		}
		for _, d := range n.soft {
			ref := d.compare.ref()
			ref.aliases = d.aliases
			for _, w := range all {
				if w.compare.satisfies(ref) {
					out.WriteString(n.label() + " -.-> " + w.label() + "\n")
				}
			}
			walk(d)
		}
	}
	walk(root)
	return out.String(), nil
}
func (n *infoNode) load(c *oci.Client, merge bool) (json.RawMessage, error) {
	if n.configPath != "" && (strings.HasPrefix(n.id, ".") || filepath.IsAbs(n.id)) {
		return n.loadLocal(merge)
	}
	if strings.HasPrefix(n.id, "https://") && strings.HasSuffix(n.id, ".tgz") {
		return n.loadTarball(c, merge)
	}
	ref, ok := versionReference(n.id)
	if !ok {
		return nil, fmt.Errorf("Feature '%s' could not be processed", n.id)
	}
	fetchRef := ref
	if n.integrity != "" {
		fetchRef.Tag = ""
		fetchRef.Digest = n.integrity
	}
	body, digest, err := infoManifest(c, fetchRef)
	if err != nil {
		return nil, err
	}
	var manifest oci.Manifest
	_ = json.Unmarshal(body, &manifest)
	meta := json.RawMessage(nil)
	if annotation := manifest.Annotations["dev.containers.metadata"]; annotation != "" {
		meta = collectiondocs.StringifyJSONCValue([]byte(annotation))
	} else {
		dir, e := os.MkdirTemp("", "dcb-feature-info-")
		if e != nil {
			return nil, e
		}
		defer os.RemoveAll(dir)
		r := &Resolver{OCI: c}
		if len(manifest.Layers) > 0 {
			if e = r.fetchAndExtractLayer(ref, manifest.Layers[0], dir); e != nil {
				return nil, e
			}
		}
		b, e := os.ReadFile(filepath.Join(dir, "devcontainer-feature.json"))
		if e != nil && !(os.IsNotExist(e) && n.configPath != "") {
			return nil, e
		}
		if e == nil {
			meta = collectiondocs.StringifyJSONCValue(b)
		}
	}
	var metadata Metadata
	_ = json.Unmarshal(meta, &metadata)
	if len(meta) > 0 {
		n.aliases = metadata.aliases()
	}
	_, value := decodeOptions(n.options)
	n.compare = &node{res: Resolved{Source: n.id, Reference: &ref, Digest: digest, Metadata: metadata}, optionsValue: value}
	resource := ref.Registry + "/" + ref.Repository
	version := ref.Tag
	if ref.Digest != "" {
		version = ref.Digest
	}
	refFields := []infoField{{"id", infoJSON(path.Base(ref.Repository))}, {"owner", infoJSON(strings.Split(ref.Repository, "/")[0])}, {"namespace", infoJSON(path.Dir(ref.Repository))}, {"registry", infoJSON(ref.Registry)}, {"resource", infoJSON(resource)}, {"path", infoJSON(ref.Repository)}, {"version", infoJSON(version)}}
	if ref.Tag != "" {
		refFields = append(refFields, infoField{"tag", infoJSON(ref.Tag)})
	}
	if ref.Digest != "" {
		refFields = append(refFields, infoField{"digest", infoJSON(ref.Digest)})
	}
	source := infoObject(infoField{"type", infoJSON("oci")}, infoField{"manifest", body}, infoField{"manifestDigest", infoJSON(digest)}, infoField{"featureRef", infoObject(refFields...)}, infoField{"userFeatureId", infoJSON(n.id)}, infoField{"userFeatureIdWithoutVersion", infoJSON(FeatureIDWithoutVersion(n.id))})
	feature := infoObject(infoField{"id", infoJSON(path.Base(ref.Repository))}, infoField{"included", infoJSON(true)}, infoField{"value", n.options})
	n.plainSet = infoObject(infoField{"sourceInformation", source}, infoField{"features", infoJSON([]json.RawMessage{feature})})
	if merge {
		var a, b spec.OrderedMap
		_ = json.Unmarshal(feature, &a)
		_ = json.Unmarshal(meta, &b)
		for _, k := range b.Keys {
			if _, exists := a.Values[k]; !exists {
				a.Keys = append(a.Keys, k)
			}
			a.Values[k] = b.Values[k]
		}
		fs := []infoField{}
		for _, k := range a.Keys {
			fs = append(fs, infoField{k, a.Values[k]})
		}
		feature = infoObject(fs...)
	}
	n.set = infoObject(infoField{"sourceInformation", source}, infoField{"features", infoJSON([]json.RawMessage{feature})})
	return meta, nil
}

func (n *infoNode) loadTarball(c *oci.Client, merge bool) (json.RawMessage, error) {
	u, err := url.Parse(n.id)
	if err != nil {
		return nil, err
	}
	match := tarballName.FindStringSubmatch(path.Base(u.Path))
	if len(match) != 2 {
		return nil, fmt.Errorf("invalid Feature archive name")
	}
	source := infoObject(infoField{"type", infoJSON("direct-tarball")}, infoField{"tarballUri", infoJSON(u.String())}, infoField{"userFeatureId", infoJSON(n.id)})
	feature := infoObject(infoField{"id", infoJSON(match[1])}, infoField{"name", infoJSON(n.id)}, infoField{"value", n.options}, infoField{"included", infoJSON(true)})
	n.plainSet = infoObject(infoField{"sourceInformation", source}, infoField{"features", infoJSON([]json.RawMessage{feature})})
	n.set = n.plainSet
	_, value := decodeOptions(n.options)
	n.compare = &node{res: Resolved{Source: n.id, localPath: n.id}, optionsValue: value}
	if !merge {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "dcb-feature-info-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	r := &Resolver{OCI: c}
	_, err = r.resolveTarball(context.Background(), n.id, nil, dir, "", n.integrity)
	if err != nil {
		if n.configPath != "" {
			return nil, nil
		} // Reporting keeps an unexpanded archive node when metadata retrieval fails.
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "devcontainer-feature.json"))
	if err != nil {
		return nil, err
	}
	meta := collectiondocs.StringifyJSONCValue(data)
	var metadata Metadata
	_ = json.Unmarshal(meta, &metadata)
	if len(meta) > 0 {
		n.aliases = metadata.aliases()
	}
	n.compare.res.Metadata = metadata
	var a, b spec.OrderedMap
	_ = json.Unmarshal(feature, &a)
	_ = json.Unmarshal(meta, &b)
	for _, k := range b.Keys {
		if _, ok := a.Values[k]; !ok {
			a.Keys = append(a.Keys, k)
		}
		a.Values[k] = b.Values[k]
	}
	fields := []infoField{}
	for _, k := range a.Keys {
		fields = append(fields, infoField{k, a.Values[k]})
	}
	n.set = infoObject(infoField{"sourceInformation", source}, infoField{"features", infoJSON([]json.RawMessage{infoObject(fields...)})})
	return meta, nil
}
