// relationships:
//
//	references: [../../internal/collectiondocs/documentation]
package templates

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/internal/collectiondocs"
)

// DocumentationOptions selects the Template collection folder and metadata links.
// ProjectFolder directly contains Template folders; use "." for the current folder.
// Logger receives progress and skipped-entry diagnostics.
type DocumentationOptions struct {
	ProjectFolder, GitHubOwner, GitHubRepo string
	Logger                                 func(format string, args ...any)
	GitHubOwnerValue, GitHubRepoValue      *DocumentationValue
}

// DocumentationValue retains publication values until rendering uses them.
type DocumentationValue = collectiondocs.DocumentationValue

// GenerateDocumentation replaces README.md in visible Template child folders
// using devcontainer-template.json and optional NOTES.md. README symlinks are
// unlinked before writing, as upstream. Failures can leave other entries written.
func GenerateDocumentation(ctx context.Context, opts DocumentationOptions) error {
	return collectiondocs.GenerateTemplateDocumentation(ctx, collectiondocs.DocumentationOptions{
		ProjectFolder: opts.ProjectFolder, GitHubOwner: opts.GitHubOwner, GitHubRepo: opts.GitHubRepo,
		Logger: opts.Logger, GitHubOwnerValue: opts.GitHubOwnerValue, GitHubRepoValue: opts.GitHubRepoValue,
	})
}
