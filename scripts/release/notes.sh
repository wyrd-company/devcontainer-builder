#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Adapted from wyrd-company/trowel; see npm/NOTICE and npm/LICENSE-APACHE-2.0.

# relationships:
#   references: [config]

# Print the CHANGELOG.md section intentional wrote for one version.
set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "usage: release-notes.sh VERSION" >&2
  exit 2
fi

notes="$(awk -v heading="## $1" '
  $0 == heading { found = 1; next }
  found && /^## / { exit }
  found { print }
' CHANGELOG.md)"
if [[ -z "${notes//[[:space:]]/}" ]]; then
  echo "CHANGELOG.md has no section for $1" >&2
  exit 1
fi
printf '%s\n' "$notes"
