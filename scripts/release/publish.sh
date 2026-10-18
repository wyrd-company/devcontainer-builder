#!/usr/bin/env bash
# relationships:
#   references: [stage, check-tag]
set -euo pipefail
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
    python3 scripts/release/channels.py homebrew "$tap"
    mkdir -p "$tap/Formula"
    cp dist/dcb.rb "$tap/Formula/dcb.rb"
    git -C "$tap" config user.name 'dcb release bot'
    git -C "$tap" config user.email '41898282+github-actions[bot]@users.noreply.github.com'
    git -C "$tap" add Formula/dcb.rb
    if ! git -C "$tap" diff --cached --quiet; then
      git -C "$tap" commit -m "Brew formula update for dcb version $RELEASE_VERSION"
    fi
    # Retry a previously committed but unpushed update even when no file changed.
    git -C "$tap" push origin HEAD:main
    ;;
  *) echo "unknown publication channel: $channel" >&2; exit 2 ;;
esac
