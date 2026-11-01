#!/usr/bin/env bash
# relationships:
#   references: [stage, check-tag]
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/git-env.sh"
channel="${1:?publication channel required}"
python3 scripts/release/check-tag.py
case "$channel" in
  github)
    bash scripts/release/notes.sh "$RELEASE_VERSION" > dist/release-notes.md
    python3 scripts/release/channels.py github
    ;;
  npm)
    python3 scripts/release/channels.py npm
    ;;
  homebrew)
    tap="${2:?checked-out homebrew tap required}"
    python3 scripts/release/channels.py homebrew-publish "$tap"
    ;;
  *) echo "unknown publication channel: $channel" >&2; exit 2 ;;
esac
