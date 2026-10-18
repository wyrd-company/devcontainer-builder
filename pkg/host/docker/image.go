package docker

import (
	"context"
	"io"

	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
)

// ensureImage pulls ref through the Engine API when the daemon does not
// have it, as `docker run` does. Registry pulls are anonymous.
func (h *Host) ensureImage(ctx context.Context, ref string) error {
	if _, err := h.client.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !dockerclient.IsErrNotFound(err) {
		return errorf("inspect image %s: %w", ref, err)
	}
	h.logf("pulling image %s", ref)
	rc, err := h.client.ImagePull(ctx, ref, imagetypes.PullOptions{})
	if err != nil {
		return errorf("pull image %s: %w", ref, err)
	}
	defer rc.Close()
	out := h.stderrOr(nil)
	if out == nil {
		out = io.Discard
	}
	if err := jsonmessage.DisplayJSONMessagesStream(rc, out, 0, false, nil); err != nil {
		return errorf("pull image %s: %w", ref, err)
	}
	return nil
}
