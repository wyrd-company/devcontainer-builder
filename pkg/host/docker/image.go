// relationships:
//
//	references: [../../../internal/dockerimage]
package docker

import (
	"context"

	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/internal/dockerimage"
)

// ensureImage pulls ref through the Engine API when the daemon does not
// have it, as `docker run` does, with the credentials of
// Options.RegistryAuth.
func (h *Host) ensureImage(ctx context.Context, ref string) error {
	if _, err := h.client.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !dockerclient.IsErrNotFound(err) {
		return errorf("inspect image %s: %w", ref, err)
	}
	h.logf("pulling image %s", ref)
	if err := dockerimage.Pull(ctx, h.client, ref, h.opts.RegistryAuth, h.stderrOr(nil)); err != nil {
		return errorf("%w", err)
	}
	return nil
}
