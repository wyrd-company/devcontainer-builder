#!/usr/bin/env bash
# relationships:
#   references: [release, check-tag]
set -euo pipefail
python3 scripts/release/check-tag.py
for name in NPM_TOKEN FORMULAE_PUBLISH_KEY REPO_WYRD_FOO_PUBLISHER_APP_ID REPO_WYRD_FOO_PUBLISHER_PRIVATE_KEY WYRD_TOOLS_DOCS_PUBLISHER_APP_ID WYRD_TOOLS_DOCS_PUBLISHER_PRIVATE_KEY; do
  if [[ -z "${!name:-}" ]]; then
    echo "Required release credential is absent: $name" >&2
    exit 1
  fi
done
