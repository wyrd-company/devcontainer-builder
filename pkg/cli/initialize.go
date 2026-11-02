// relationships:
//
//	references: [host, ../host/local/initialize, config]
package cli

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/local"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func (a *application) initialize(ctx context.Context, dc *spec.DevContainer, workspace string) error {
	return local.Initialize(ctx, dc.Initialize, local.InitializeOptions{Workspace: workspace, Stdin: a.stdin, Output: a.logs, Substitution: a.substitutionContext(dc, workspace, nil)})
}
