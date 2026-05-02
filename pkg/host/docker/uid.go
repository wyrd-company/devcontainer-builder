package docker

import (
	"context"
	"fmt"
	"strings"
)

// RemoteUserUIDUpdate, when supplied on UpRequest, asks the host to
// rewrite the configured remoteUser's UID/GID inside the running
// container so files created in bind-mounted host paths land with the
// invoking user's ownership instead of the image-baked user's. This is
// the embeddable equivalent of upstream's `updateRemoteUserUID`
// behavior — except we patch /etc/passwd + /etc/group at runtime via
// `docker exec` instead of rebuilding the image, which keeps the
// package shell-out-free.
//
// The update is skipped — without error — when:
//
//   - the spec sets `updateRemoteUserUID: false`,
//   - the resolved remoteUser is "root" or a numeric UID,
//   - the in-image UID/GID already match HostUID/HostGID,
//   - or the in-image /etc/passwd does not contain remoteUser.
//
// The rewrite is idempotent. It runs on every Up call so a host that
// later moves to a different UID still converges.
type RemoteUserUIDUpdate struct {
	// HostUID / HostGID are the IDs the in-image remoteUser should be
	// remapped to. Typically the embedder calls os.Getuid / os.Getgid
	// on linux/macOS; the package never does so itself.
	HostUID int
	HostGID int
}

// uidUpdateScript is the runtime equivalent of upstream's
// scripts/updateUID.Dockerfile. The shape is intentionally identical
// to make audit easy. Differences:
//
//   - We don't need ARGs since the values are interpolated by the Go
//     caller into a single shell exec.
//   - We exit 0 in every branch — a "remote user not found" or
//     "matching UID exists" condition is a deliberate skip, not an
//     error. The caller's logger captures the reason.
const uidUpdateScript = `set -e
REMOTE_USER='%s'
NEW_UID='%d'
NEW_GID='%d'
eval "$(sed -n "s/${REMOTE_USER}:[^:]*:\([^:]*\):\([^:]*\):[^:]*:\([^:]*\).*/OLD_UID=\1;OLD_GID=\2;HOME_FOLDER=\3/p" /etc/passwd)"
eval "$(sed -n "s/\([^:]*\):[^:]*:${NEW_UID}:.*/EXISTING_USER=\1/p" /etc/passwd)"
eval "$(sed -n "s/\([^:]*\):[^:]*:${NEW_GID}:.*/EXISTING_GROUP=\1/p" /etc/group)"
if [ -z "${OLD_UID:-}" ]; then
  echo "updateRemoteUserUID: remote user not found in /etc/passwd ($REMOTE_USER)"
  exit 0
fi
if [ "$OLD_UID" = "$NEW_UID" ] && [ "$OLD_GID" = "$NEW_GID" ]; then
  echo "updateRemoteUserUID: UIDs and GIDs already match ($NEW_UID:$NEW_GID)"
  exit 0
fi
if [ "$OLD_UID" != "$NEW_UID" ] && [ -n "${EXISTING_USER:-}" ]; then
  echo "updateRemoteUserUID: user with UID $NEW_UID exists ($EXISTING_USER), skipping"
  exit 0
fi
if [ "$OLD_GID" != "$NEW_GID" ] && [ -n "${EXISTING_GROUP:-}" ]; then
  echo "updateRemoteUserUID: group with GID $NEW_GID exists ($EXISTING_GROUP), keeping old GID"
  NEW_GID="$OLD_GID"
fi
echo "updateRemoteUserUID: $REMOTE_USER $OLD_UID:$OLD_GID -> $NEW_UID:$NEW_GID"
sed -i -e "s/\(${REMOTE_USER}:[^:]*:\)[^:]*:[^:]*/\1${NEW_UID}:${NEW_GID}/" /etc/passwd
if [ "$OLD_GID" != "$NEW_GID" ]; then
  sed -i -e "s/\([^:]*:[^:]*:\)${OLD_GID}:/\1${NEW_GID}:/" /etc/group
fi
chown -R "$NEW_UID:$NEW_GID" "$HOME_FOLDER" 2>/dev/null || true
`

// applyUIDUpdate runs the UID/GID remap inside containerID. Returns
// nil on a successful run (or a deliberate skip described in the
// runtime script); any non-zero exit from the exec itself surfaces as
// an error so the caller can surface it to logs.
func (h *Host) applyUIDUpdate(ctx context.Context, containerID, remoteUser string, upd RemoteUserUIDUpdate) error {
	if remoteUser == "" || remoteUser == "root" || isAllDigits(remoteUser) {
		return nil
	}
	script := fmt.Sprintf(uidUpdateScript,
		shellSQRaw(remoteUser),
		upd.HostUID,
		upd.HostGID,
	)
	exit, out, err := h.execShellQuiet(ctx, containerID, "root", script)
	if err != nil {
		return errorf("updateRemoteUserUID exec: %w", err)
	}
	if msg := strings.TrimSpace(out); msg != "" {
		h.logf("%s", msg)
	}
	if exit != 0 {
		return errorf("updateRemoteUserUID: exit %d: %s", exit, strings.TrimSpace(out))
	}
	return nil
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
