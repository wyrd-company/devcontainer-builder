// relationships:
//
//	references: [up, override, spawn]
package dockercompose

import (
	"context"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/wyrd-company/devcontainer-builder/internal/dockerimage"
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

// wrapPrimaryCommand always wraps a preserved primary command, including when
// there are no Features. Image defaults are read only for an absent entrypoint.
func (h *Host) wrapPrimaryCommand(ctx context.Context, proj *types.Project, in OverrideInputs) error {
	if in.OverrideCommand {
		return nil
	}
	svc, ok := proj.Services[in.Spec.Service]
	if !ok {
		return nil
	}
	var imageEntrypoint, imageCmd []string
	if svc.Entrypoint == nil {
		img, err := dockerimage.InspectConfig(ctx, h.client, svc.Image, svc.Platform, h.opts.RegistryAuth, h.stderrOr(nil))
		if err != nil {
			return errorf("inspect image %s: %w", svc.Image, err)
		}
		if img != nil {
			imageEntrypoint, imageCmd = img.Entrypoint, img.Cmd
		}
	}
	wrapServiceCommand(&svc, in.Entrypoints, imageEntrypoint, imageCmd)
	proj.Services[in.Spec.Service] = svc
	return nil
}

// applyPrimaryCommand uses the Compose default (false), independently of the
// image/Dockerfile default. Both modes run the same Feature wrapper.
func (h *Host) applyPrimaryCommand(ctx context.Context, proj *types.Project, in OverrideInputs) error {
	in.OverrideCommand = in.Spec.OverrideCommand != nil && *in.Spec.OverrideCommand
	if in.OverrideCommand {
		svc := proj.Services[in.Spec.Service]
		svc.Entrypoint = types.ShellCommand{"/bin/sh", "-c", keepaliveScript(in.Entrypoints), "-"}
		svc.Command = types.ShellCommand{}
		proj.Services[in.Spec.Service] = svc
	}
	return h.wrapPrimaryCommand(ctx, proj, in)
}

// resolveEmptyCommand prevents Engine from restoring image Cmd for an explicit
// empty Compose command, while retaining the image Entrypoint.
func (h *Host) resolveEmptyCommand(ctx context.Context, svc *types.ServiceConfig) error {
	if svc.Entrypoint != nil || svc.Command == nil || len(svc.Command) != 0 {
		return nil
	}
	img, err := dockerimage.InspectConfig(ctx, h.client, svc.Image, svc.Platform, h.opts.RegistryAuth, h.stderrOr(nil))
	if err != nil {
		return errorf("inspect image %s for empty command: %w", svc.Image, err)
	}
	svc.Entrypoint = types.ShellCommand{}
	if img != nil {
		svc.Entrypoint = append(svc.Entrypoint, img.Entrypoint...)
	}
	return nil
}
