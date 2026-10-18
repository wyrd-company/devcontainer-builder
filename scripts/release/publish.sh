#!/usr/bin/env bash
# relationships:
#   references: [stage, check-tag]
set -euo pipefail
channel="${1:?publication channel required}"
python3 scripts/release/check-tag.py
case "$channel" in
  github)
    bash scripts/release/notes.sh "$RELEASE_VERSION" > dist/release-notes.md
    gh release create "$RELEASE_VERSION" --verify-tag --title "$RELEASE_VERSION" \
      --notes-file dist/release-notes.md \
      dist/*.tar.gz dist/*.deb dist/*.rpm dist/checksums.txt dist/release-manifest.json
    ;;
  npm)
    # Optional native dependencies must exist before the launcher is discoverable.
    for package in linux-x64 linux-arm64 darwin-x64 darwin-arm64; do
      npm publish "dist/npm/tarballs/wyrd-company-dcb-${package}-${RELEASE_VERSION}.tgz" --access public --provenance
    done
    npm publish "dist/npm/tarballs/wyrd-company-dcb-${RELEASE_VERSION}.tgz" --access public --provenance
    ;;
  homebrew)
    tap="${2:?checked-out homebrew tap required}"
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
