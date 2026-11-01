// relationships:
//
//	references: [info, order, outdated, ../../reference/cli/src/spec-node/featuresCLI/resolveDependencies]
package features

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/internal/collectiondocs"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// ResolveDependencies reads the workspace configuration and reports its Mermaid
// graph followed by ordered, pretty JSON. It does not install Features or write
// a lockfile. A failed installation order still returns the completed diagram.
// A nil client uses the existing registry credential integration.
func ResolveDependencies(client *oci.Client, workspace string) (string, error) {
	if workspace == "" {
		workspace = "."
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	configPath := filepath.Join(workspace, ".devcontainer.json")
	if st, e := os.Stat(configPath); e != nil || !st.Mode().IsRegular() {
		configPath = filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	data = collectiondocs.StringifyJSONCValue(data)
	var document spec.OrderedMap
	if err = json.Unmarshal(data, &document); err != nil {
		return "", err
	}
	var values any
	_ = json.Unmarshal(data, &values)
	if err = validateReportSubstitutions(values); err != nil {
		return "", err
	}
	config := &spec.DevContainer{Path: configPath}
	if raw := document.Values["features"]; len(raw) > 0 && raw[0] == '{' {
		if err = json.Unmarshal(raw, &config.Features); err != nil {
			return "", err
		}
	}
	seen := map[string]bool{}
	keys := []string{}
	for _, id := range config.Features.Keys {
		if !seen[id] {
			keys = append(keys, id)
			seen[id] = true
		}
	}
	config.Features.Keys = keys
	if config.Features.Len() == 0 {
		return "", fmt.Errorf("Could not parse features object in configuration %q", configPath)
	}
	env := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			env[k] = v
		}
	}
	ctx := substitute.Context{LocalWorkspaceFolder: workspace, LocalEnv: env}
	var containerFolder string
	_ = json.Unmarshal(document.Values["workspaceFolder"], &containerFolder)
	if _, compose := document.Values["dockerComposeFile"]; containerFolder == "" {
		if compose {
			containerFolder = "/"
		} else {
			containerFolder = "/workspaces/" + filepath.Base(workspace)
		}
	}
	ctx.ContainerWorkspaceFolder = containerFolder
	ctx.ContainerWorkspaceFolder = substitute.Substitute(ctx, containerFolder)
	subst := func(raw json.RawMessage) json.RawMessage {
		return collectiondocs.TransformJSONStrings(raw, func(s string) string {
			if ctx.ContainerWorkspaceFolder == "/" {
				s = strings.ReplaceAll(s, "${containerWorkspaceFolderBasename}", "")
			}
			return substitute.Substitute(ctx, s)
		})
	}
	for _, id := range config.Features.Keys {
		config.Features.Values[id] = subst(config.Features.Values[id])
	}
	var override []string
	if raw := document.Values["overrideFeatureInstallOrder"]; raw != nil {
		if err = json.Unmarshal(subst(infoNormalize(raw)), &override); err != nil {
			return "", err
		}
	}
	state, err := ReadLockfile(configPath, false, false)
	if err != nil {
		return "", err
	}
	if client == nil {
		client = oci.NewClient()
	}
	makeNode := func(id, kind string, options json.RawMessage) *infoNode {
		n := &infoNode{id: id, kind: kind, options: infoNormalize(options), configPath: configPath, workspace: workspace}
		if state.Lockfile != nil {
			n.integrity = state.Lockfile.Features[id].Integrity
		}
		return n
	}
	queue := []*infoNode{}
	for _, id := range config.Features.Keys {
		queue = append(queue, makeNode(id, "user-provided", config.Features.Values[id]))
	}
	all := []*infoNode{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		meta, e := n.load(client, true)
		if e != nil {
			return "", e
		}
		duplicate := false
		for _, old := range all {
			x, y := old.compare.optionsValue, n.compare.optionsValue
			isObject := func(v any) bool {
				switch v.(type) {
				case nil, map[string]any, []any:
					return true
				}
				return false
			}
			if old.compare.ref().resource == n.compare.ref().resource && (x == nil || y == nil) && isObject(x) && isObject(y) {
				return "", fmt.Errorf("cannot compare null Feature options")
			}
			if old.compare.equals(n.compare) {
				duplicate = true
				break
			}
		}
		if duplicate {
			n.aliases = nil
			n.aliasesValue = nil
			n.set = n.plainSet
			continue
		}
		var m Metadata
		if len(meta) > 0 && meta[0] != '[' {
			if e = json.Unmarshal(meta, &m); e != nil {
				return "", e
			}
		}
		for _, id := range m.DependsOn.Keys {
			d := makeNode(id, "resolved", infoNormalize(m.DependsOn.Values[id]))
			n.hard = append(n.hard, d)
			queue = append(queue, d)
		}
		for _, id := range m.InstallsAfter {
			d := makeNode(id, "resolved", json.RawMessage(`{}`))
			if _, e = d.load(client, false); e != nil {
				return "", e
			}
			n.soft = append(n.soft, d)
		}
		all = append(all, n)
	}
	for i := len(override) - 1; i >= 0; i-- {
		d := makeNode(override[i], "override", json.RawMessage(`{}`))
		if _, err = d.load(client, false); err != nil {
			return "", err
		}
		ref := d.compare.ref()
		for _, n := range all {
			if n.compare.satisfies(ref) {
				n.priority = max(n.priority, len(override)-i)
				n.compare.priority = n.priority
			}
		}
	}
	var out strings.Builder
	out.WriteString("flowchart\n")
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
	for _, n := range all {
		if n.kind == "user-provided" {
			out.WriteString(n.label() + "\n")
			walk(n)
		}
	}
	out.WriteByte('\n')
	for _, n := range all {
		if string(n.options) == "null" {
			return out.String(), fmt.Errorf("cannot read options of null Feature")
		}
	}
	if len(all) == 0 {
		return out.String(), fmt.Errorf("No viable installation order!")
	}
	nodes := []*node{}
	for _, n := range all {
		nodes = append(nodes, n.compare)
		for _, d := range n.hard {
			for _, w := range all {
				if w.compare.equals(d.compare) {
					n.compare.dependsOn = append(n.compare.dependsOn, w.compare)
				}
			}
		}
		for _, d := range n.soft {
			ref := d.compare.ref()
			ref.aliases = d.aliases
			for _, w := range all {
				if w.compare.satisfies(ref) {
					n.compare.installsAfter = append(n.compare.installsAfter, ref)
					break
				}
			}
		}
	}
	remaining := nodes
	installed := []*node{}
	entries := []json.RawMessage{}
	for len(remaining) > 0 {
		round := nextRound(remaining, installed)
		if len(round) == 0 {
			return out.String(), fmt.Errorf("No viable installation order!")
		}
		next := []*node{}
		for _, n := range remaining {
			found := false
			for _, r := range round {
				if r == n {
					found = true
				}
			}
			if !found {
				next = append(next, n)
			}
		}
		remaining = next
		installed = append(installed, round...)
		for _, r := range round {
			for _, n := range all {
				if n.compare == r {
					id := n.id
					if r.res.Reference != nil {
						id = r.ref().resource + "@" + r.res.Digest
					}
					var set struct {
						Features []struct {
							Value json.RawMessage `json:"value"`
						} `json:"features"`
					}
					_ = json.Unmarshal(n.set, &set)
					options := n.options
					if len(set.Features) > 0 && set.Features[0].Value != nil {
						options = set.Features[0].Value
					}
					entries = append(entries, infoObject(infoField{"id", infoJSON(id)}, infoField{"options", options}))
				}
			}
		}
	}
	pretty, err := infoPretty(infoObject(infoField{"installOrder", infoJSON(entries)}), 2)
	if err != nil {
		return out.String(), err
	}
	return out.String() + pretty + "\n", nil
}

func (n *infoNode) loadLocal(merge bool) (json.RawMessage, error) {
	resolved := filepath.Join(filepath.Dir(n.configPath), n.id)
	if strings.HasSuffix(n.id, string(filepath.Separator)) && !strings.HasSuffix(resolved, string(filepath.Separator)) {
		resolved += string(filepath.Separator)
	}
	relative, err := filepath.Rel(filepath.Join(n.workspace, ".devcontainer"), resolved)
	if err != nil || filepath.IsAbs(n.id) || strings.Contains(relative, "..") {
		return nil, fmt.Errorf("local Feature must be inside .devcontainer: %s", n.id)
	}
	source := infoObject(infoField{"type", infoJSON("file-path")}, infoField{"resolvedFilePath", infoJSON(resolved)}, infoField{"userFeatureId", infoJSON(n.id)})
	feature := infoObject(infoField{"id", infoJSON(filepath.Base(resolved))}, infoField{"name", infoJSON(n.id)}, infoField{"value", n.options}, infoField{"included", infoJSON(true)})
	n.plainSet = infoObject(infoField{"sourceInformation", source}, infoField{"features", infoJSON([]json.RawMessage{feature})})
	n.set = n.plainSet
	value := collectiondocs.JSONComparisonValue(n.options)
	n.compare = &node{res: Resolved{Source: n.id, localPath: resolved}, optionsValue: value}
	if !merge {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(resolved, "devcontainer-feature.json"))
	if err != nil {
		return nil, err
	}
	meta := collectiondocs.StringifyJSONCValue(data)
	var m Metadata
	if len(meta) > 0 && meta[0] != '[' {
		if err = json.Unmarshal(meta, &m); err != nil {
			return nil, err
		}
	}
	n.setReportAliases(meta, m)
	n.compare.res.Metadata = m
	var a, b spec.OrderedMap
	_ = json.Unmarshal(feature, &a)
	b = infoMetadataProperties(meta)
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
