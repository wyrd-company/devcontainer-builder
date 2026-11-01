// relationships:
//
//	references: [../../internal/collectiondocs/documentation]
package features

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/internal/collectiondocs"
)

// DocumentationOptions selects the collection folder and published references.
type DocumentationOptions = collectiondocs.DocumentationOptions

// DocumentationValue retains publication values until rendering uses them.
type DocumentationValue = collectiondocs.DocumentationValue

// GenerateDocumentation replaces visible Feature child READMEs from metadata
// and NOTES.md. A failure can leave other entries already written.
func GenerateDocumentation(ctx context.Context, opts DocumentationOptions) error {
	return collectiondocs.GenerateDocumentation(ctx, opts)
}
