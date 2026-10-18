package dockercompose

import (
	"context"

	"github.com/compose-spec/compose-go/v2/types"
)

// wrapServiceCommand makes the keepalive script, which runs the feature
// entrypoints, the service's entrypoint while it keeps its own command,
// as upstream does when overrideCommand is false. The script execs its
// arguments: the service's entrypoint (else the image's) and the
// service's command (else, when the service sets no entrypoint, the
// image's).
func wrapServiceCommand(svc *types.ServiceConfig, entrypoints, imageEntrypoint, imageCmd []string) {
	userEntrypoint := []string(svc.Entrypoint)
	if userEntrypoint == nil {
		userEntrypoint = imageEntrypoint
	}
	userCommand := []string(svc.Command)
	if userCommand == nil && svc.Entrypoint == nil {
		userCommand = imageCmd
	}
	wrapped := append([]string{"/bin/sh", "-c", keepaliveScript(entrypoints), "-"}, userEntrypoint...)
	svc.Entrypoint = types.ShellCommand(wrapped)
	svc.Command = types.ShellCommand(userCommand)
}

// wrapPrimaryCommand applies wrapServiceCommand to the primary service
// when overrideCommand is false and features contribute entrypoints
// (with overrideCommand true the override's keepalive runs them). The
// image is inspected only when the service lacks an entrypoint or
// command of its own.
func (h *Host) wrapPrimaryCommand(ctx context.Context, proj *types.Project, in OverrideInputs) error {
	if in.OverrideCommand || len(in.Entrypoints) == 0 {
		return nil
	}
	svc, ok := proj.Services[in.Spec.Service]
	if !ok {
		return nil
	}
	var imageEntrypoint, imageCmd []string
	if svc.Entrypoint == nil || svc.Command == nil {
		img, err := h.client.ImageInspect(ctx, svc.Image)
		if err != nil {
			return errorf("inspect image %s: %w", svc.Image, err)
		}
		if img.Config != nil {
			imageEntrypoint, imageCmd = img.Config.Entrypoint, img.Config.Cmd
		}
	}
	wrapServiceCommand(&svc, in.Entrypoints, imageEntrypoint, imageCmd)
	proj.Services[in.Spec.Service] = svc
	return nil
}
