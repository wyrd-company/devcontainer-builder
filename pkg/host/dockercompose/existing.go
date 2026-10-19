// relationships:
//
//	references: [up, ../../cli/options_lifecycle]
package dockercompose

import (
	"context"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockerclient "github.com/docker/docker/client"
)

// FindExisting returns the first container ID for a Compose project and service,
// including stopped containers, or an empty string if none exists. Compose's own
// labels allow reuse of containers created by docker compose.
func FindExisting(ctx context.Context, cli *dockerclient.Client, projectName, serviceName string) (string, error) {
	args := filters.NewArgs(
		filters.Arg("label", LabelProject+"="+projectName),
		filters.Arg("label", LabelService+"="+serviceName),
	)
	list, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return "", errorf("list containers: %w", err)
	}
	if len(list) == 0 {
		return "", nil
	}
	return list[0].ID, nil
}
