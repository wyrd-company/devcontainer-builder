// relationships:
//
// references: [features, documentation_jsonc, ../../reference/cli/src/spec-node/collectionCommonUtils/generateDocsCommandImpl]
package collectiondocs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// DocumentationOptions selects the collection folder and published references.
// ProjectFolder contains the collection entry folders. Registry and Namespace select the
// collection publication reference. Logger receives progress and skipped-entry
// diagnostics; malformed metadata without an ID is skipped, as upstream.
type DocumentationOptions struct {
	ProjectFolder, Registry, Namespace, GitHubOwner, GitHubRepo string
	Logger                                                      func(format string, args ...any)
	// Non-nil values override the corresponding string. Conversion is deferred
	// until rendering uses the value; skipped entries do not convert it.
	RegistryValue, NamespaceValue, GitHubOwnerValue, GitHubRepoValue *DocumentationValue
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
	return generateDocumentation(ctx, opts, false)
}

// GenerateTemplateDocumentation uses the upstream Template README and metadata.
func GenerateTemplateDocumentation(ctx context.Context, opts DocumentationOptions) error {
	return generateDocumentation(ctx, opts, true)
}

func generateDocumentation(ctx context.Context, opts DocumentationOptions, template bool) error {
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
		if err := generateEntryDocumentation(opts, entry.Name(), template); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func generateEntryDocumentation(opts DocumentationOptions, name string, template bool) error {
	metadataFile := "devcontainer-feature.json"
	if template {
		metadataFile = "devcontainer-template.json"
	}
	folder := filepath.Join(opts.ProjectFolder, name)
	readme := filepath.Join(folder, "README.md")
	documentationLog(opts, "Generating %s...", readme)
	metadata := filepath.Join(folder, metadataFile)
	if _, err := os.Stat(metadata); err != nil {
		documentationLog(opts, "warning: (!) Warning: %s not found at path '%s'. Skipping...", metadataFile, metadata)
		return nil
	}
	data, err := os.ReadFile(metadata)
	if err != nil {
		documentationLog(opts, "error: Failed to parse %s: %v", metadata, err)
		return nil
	}
	parsed := documentationJSON(data)
	if !documentationTruthy(documentationGet(parsed, "id")) {
		documentationLog(opts, "error: %s for '%s' does not contain an 'id'", metadataFile, name)
		return nil
	}
	text, err := renderDocumentation(parsed, opts, name, template)
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

func renderDocumentation(meta any, opts DocumentationOptions, folder string, template bool) (string, error) {
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
	generateOptions := func() (string, error) {
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
		return options, conversionErr
	}
	generateNotes := func() (string, error) {
		notes := ""
		notesPath := filepath.Join(opts.ProjectFolder, folder, "NOTES.md")
		if _, err := os.Stat(notesPath); err == nil {
			data, err := os.ReadFile(notesPath)
			if err != nil {
				return "", err
			}
			notes = documentationUTF8(data)
		}
		return notes, nil
	}
	metadataFile := "devcontainer-feature.json"
	if template {
		metadataFile = "devcontainer-template.json"
	}
	url := metadataFile
	owner := documentationPublicationValue(opts.GitHubOwnerValue, opts.GitHubOwner)
	repo := documentationPublicationValue(opts.GitHubRepoValue, opts.GitHubRepo)
	if owner != "" && repo != "" {
		base := strings.TrimPrefix(opts.ProjectFolder, "./")
		url = fmt.Sprintf("https://github.com/%s/%s/blob/main/%s/%s/%s", stringValue(owner), stringValue(repo), base, folder, metadataFile)
		if conversionErr != nil {
			return "", conversionErr
		}
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
	description := stringValue(desc)
	if conversionErr != nil {
		return "", conversionErr
	}
	options, err := generateOptions()
	if err != nil {
		return "", err
	}
	notes, err := generateNotes()
	if err != nil {
		return "", err
	}
	text := featureReadmeTemplate
	if template {
		text = templateReadmeTemplate
	}
	for _, r := range [][2]string{{"Id", id}, {"Name", name}, {"Description", description}, {"OptionsTable", options}, {"Notes", notes}, {"RepoUrl", url}, {"Registry", stringValue(documentationPublicationValue(opts.RegistryValue, opts.Registry))}, {"Namespace", stringValue(documentationPublicationValue(opts.NamespaceValue, opts.Namespace))}, {"Version", version}, {"Customizations", extensions}} {
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

const templateReadmeTemplate = `
# #{Name}

#{Description}

#{OptionsTable}

#{Notes}

---

_Note: This file was auto-generated from the [devcontainer-template.json](#{RepoUrl}).  Add additional notes to a ` + "`NOTES.md`" + `._
`
