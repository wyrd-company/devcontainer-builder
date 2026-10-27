// relationships:
//
// references: [features, documentation_jsonc, ../../reference/cli/src/spec-node/collectionCommonUtils/generateDocsCommandImpl]
package features

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// DocumentationOptions selects the collection folder and published references.
// ProjectFolder contains the Feature folders. Registry and Namespace select the
// collection publication reference. Logger receives progress and skipped-entry
// diagnostics; malformed metadata without an ID is skipped, as upstream.
type DocumentationOptions struct {
	ProjectFolder, Registry, Namespace, GitHubOwner, GitHubRepo string
	Logger                                                      func(format string, args ...any)
}

const featureReadmeTemplate = `
# #{Name}

#{Description}

## Example Usage

` + "```json" + `
"features": {
    "#{Registry}/#{Namespace}/#{Id}:#{Version}": {}
}
` + "```" + `

#{OptionsTable}
#{Customizations}
#{Notes}

---

_Note: This file was auto-generated from the [devcontainer-feature.json](#{RepoUrl}).  Add additional notes to a ` + "`NOTES.md`" + `._
`

// GenerateDocumentation replaces README.md in each visible child of ProjectFolder
// using devcontainer-feature.json and optional NOTES.md. It does not search src or
// test implicitly. Existing README symlinks are unlinked before writing. Like
// upstream, a failure can leave other entries' documentation already written.
func GenerateDocumentation(ctx context.Context, opts DocumentationOptions) error {
	entries, err := os.ReadDir(opts.ProjectFolder)
	if err != nil {
		return err
	}
	var first error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if err := generateFeatureDocumentation(opts, entry.Name()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func generateFeatureDocumentation(opts DocumentationOptions, name string) error {
	folder := filepath.Join(opts.ProjectFolder, name)
	readme := filepath.Join(folder, "README.md")
	documentationLog(opts, "Generating %s...", readme)
	metadata := filepath.Join(folder, "devcontainer-feature.json")
	if _, err := os.Stat(metadata); err != nil {
		documentationLog(opts, "warning: (!) Warning: devcontainer-feature.json not found at path '%s'. Skipping...", metadata)
		return nil
	}
	data, err := os.ReadFile(metadata)
	if err != nil {
		documentationLog(opts, "error: Failed to parse %s: %v", metadata, err)
		return nil
	}
	parsed := documentationJSON(data)
	if !documentationTruthy(documentationGet(parsed, "id")) {
		documentationLog(opts, "error: devcontainer-feature.json for '%s' does not contain an 'id'", name)
		return nil
	}
	text, err := renderFeatureDocumentation(parsed, opts, name)
	if err != nil {
		return fmt.Errorf("%s: %w", metadata, err)
	}
	// Unlink the previous file, rather than following a README symlink or keeping
	// its old mode. A dangling symlink is followed, matching existsSync upstream.
	if info, err := os.Stat(readme); err == nil {
		if info.IsDir() {
			link, err := os.Lstat(readme)
			if err != nil {
				return err
			}
			if link.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("unlink %s: is a directory", readme)
			}
		}
		if err := os.Remove(readme); err != nil {
			return err
		}
	}
	if err := os.WriteFile(readme, []byte(text), 0o666); err != nil {
		return err
	}
	return nil
}

func documentationLog(opts DocumentationOptions, format string, args ...any) {
	if opts.Logger != nil {
		opts.Logger(format, args...)
	}
}

func renderFeatureDocumentation(meta any, opts DocumentationOptions, folder string) (string, error) {
	get := func(key string) any { return documentationGet(meta, key) }
	var conversionErr error
	stringValue := func(value any) string {
		text, err := documentationString(value)
		if err != nil && conversionErr == nil {
			conversionErr = err
		}
		return text
	}
	version := "latest"
	if v := get("version"); documentationTruthy(v) {
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("version.split is not a function")
		}
		version = strings.Split(s, ".")[0]
	}
	options := ""
	if value := get("options"); documentationTruthy(value) {
		rows := []string{}
		for _, key := range documentationKeys(value) {
			val := documentationGet(value, key)
			if val == nil || val == documentationUndefined {
				return "", fmt.Errorf("cannot read option %s", key)
			}
			desc, typ, def := documentationGet(val, "description"), documentationGet(val, "type"), documentationGet(val, "default")
			if !documentationTruthy(desc) {
				desc = "-"
			}
			if !documentationTruthy(typ) {
				typ = "-"
			}
			if def == "" {
				def = "-"
			}
			rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s |", key, stringValue(desc), stringValue(typ), stringValue(def)))
		}
		options = "## Options\n\n| Options Id | Description | Type | Default Value |\n|-----|-----|-----|-----|\n" + strings.Join(rows, "\n")
	}
	notes := ""
	notesPath := filepath.Join(opts.ProjectFolder, folder, "NOTES.md")
	if _, err := os.Stat(notesPath); err == nil {
		data, err := os.ReadFile(notesPath)
		if err != nil {
			return "", err
		}
		notes = documentationUTF8(data)
	}
	url := "devcontainer-feature.json"
	if opts.GitHubOwner != "" && opts.GitHubRepo != "" {
		base := strings.TrimPrefix(opts.ProjectFolder, "./")
		url = fmt.Sprintf("https://github.com/%s/%s/blob/main/%s/%s/devcontainer-feature.json", opts.GitHubOwner, opts.GitHubRepo, base, folder)
	}
	header := ""
	legacy := get("legacyIds")
	legacyLength, err := documentationLength(legacy)
	if err != nil {
		return "", err
	}
	hasLegacy := documentationTruthy(legacy) && legacyLength > 0
	if documentationTruthy(get("deprecated")) || hasLegacy {
		header = "### **IMPORTANT NOTE**\n"
		if documentationTruthy(get("deprecated")) {
			header += "- **This Feature is deprecated, and will no longer receive any further updates/support.**\n"
		}
		if hasLegacy {
			values, err := documentationMap(legacy, "'", "'")
			if err != nil {
				return "", fmt.Errorf("legacyIds.%w", err)
			}
			header += "- **Ids used to publish this Feature in the past - " + strings.Join(values, ", ") + "**\n"
		}
	}
	extensions := ""
	ext := documentationGet(documentationGet(get("customizations"), "vscode"), "extensions")
	extensionLength, err := documentationLength(ext)
	if err != nil {
		return "", err
	}
	if documentationTruthy(ext) && extensionLength > 0 {
		items, err := documentationMap(ext, "- `", "`")
		if err != nil {
			return "", fmt.Errorf("extensions.%w", err)
		}
		extensions = "\n## Customizations\n\n### VS Code Extensions\n\n" + strings.Join(items, "\n") + "\n"
	}
	id := stringValue(get("id"))
	name := id
	if documentationTruthy(get("name")) {
		name = stringValue(get("name")) + " (" + id + ")"
	}
	desc := get("description")
	if desc == nil || desc == documentationUndefined {
		desc = ""
	}
	text := featureReadmeTemplate
	for _, r := range [][2]string{{"Id", id}, {"Name", name}, {"Description", stringValue(desc)}, {"OptionsTable", options}, {"Notes", notes}, {"RepoUrl", url}, {"Registry", opts.Registry}, {"Namespace", opts.Namespace}, {"Version", version}, {"Customizations", extensions}} {
		text = documentationReplace(text, "#{"+r[0]+"}", r[1])
	}
	if conversionErr != nil {
		return "", conversionErr
	}
	return string(utf16.Decode(documentationUnits(header + text))), nil
}

// String.replace's replacement strings expand dollar patterns, including those
// in user-authored NOTES.md. Each placeholder is replaced once, in source order.
func documentationReplace(text, token, replacement string) string {
	at := strings.Index(text, token)
	if at < 0 {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(replacement); i++ {
		if replacement[i] == '$' && i+1 < len(replacement) {
			switch replacement[i+1] {
			case '$':
				b.WriteByte('$')
				i++
				continue
			case '&':
				b.WriteString(token)
				i++
				continue
			case '`':
				b.WriteString(text[:at])
				i++
				continue
			case '\'':
				b.WriteString(text[at+len(token):])
				i++
				continue
			}
		}
		b.WriteByte(replacement[i])
	}
	return text[:at] + b.String() + text[at+len(token):]
}
