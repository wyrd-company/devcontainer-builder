#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Adapted from wyrd-company/trowel; see npm/NOTICE and npm/LICENSE-APACHE-2.0.
# relationships:
#   references: [manifest, release]

set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/git-env.sh"

# Operator checkout reads retain ordinary Git configuration after isolation.
# Fixed fixture identity and configuration apply only to temporary inbox commands.
inbox_git() {
  git_env env GIT_AUTHOR_NAME=Example GIT_AUTHOR_EMAIL=example@example.invalid \
    GIT_COMMITTER_NAME=Example GIT_COMMITTER_EMAIL=example@example.invalid \
    GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 git "$@"
}

if [[ "$#" -ne 5 ]]; then
  echo "usage: validate-publisher-contract.sh PUBLISHER COMMIT MANIFEST ARTIFACTS OUTPUT" >&2
  exit 2
fi

publisher="$1"
expected_commit="$2"
manifest="$3"
artifacts="$4"
output="$5"

if [[ ! "$expected_commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "publisher contract must be pinned to a full commit SHA" >&2
  exit 2
fi
if [[ "$(git -C "$publisher" rev-parse HEAD)" != "$expected_commit" ]]; then
  echo "publisher checkout does not match the required commit" >&2
  exit 2
fi

repository_script="$publisher/scripts/repository.py"
public_key="$publisher/pubkey.gpg"
allowlist="$publisher/config/products.json"
if [[ ! -f "$repository_script" || ! -f "$public_key" || ! -f "$allowlist" ]]; then
  echo "publisher checkout does not expose the package contract" >&2
  exit 2
fi

mkdir -p "$output"
output="$(mktemp -d "$output/contract.XXXXXX")"
inbox="$output/inbox"
stage="$output/stage"
aur="$output/aur"
mkdir -p "$inbox" "$stage" "$aur"

git_env python3 "$repository_script" validate "$manifest"
inbox_git init --quiet --initial-branch=release-manifests "$inbox"
inbox_git -C "$inbox" commit --quiet --allow-empty -m "Initialize package inbox"
submission="$(git_env python3 "$repository_script" submit "$manifest" "$inbox")"
IFS=$'\t' read -r state submitted_path <<<"$submission"
if [[ "$state" != "created" ]]; then
  echo "publisher did not create a new immutable inbox manifest" >&2
  exit 2
fi
if [[ ! "$submitted_path" =~ ^releases/dcb/[0-9]+\.[0-9]+\.[0-9]+\.json$ ]]; then
  echo "publisher returned an unexpected immutable inbox path: $submitted_path" >&2
  exit 2
fi
inbox_git -C "$inbox" add -- "$submitted_path"
inbox_git -C "$inbox" commit --quiet -m "Add package release manifest"
inbox_commit="$(inbox_git -C "$inbox" rev-parse HEAD)"
submitted_manifest="$inbox/$submitted_path"
git_env python3 "$repository_script" validate-queue \
  "$inbox" "$inbox_commit" "$submitted_path" "$allowlist"
git_env python3 "$repository_script" stage "$submitted_manifest" "$artifacts" "$stage" "$public_key"
git_env python3 "$repository_script" render-aur "$submitted_manifest" "$aur" --artifacts "$artifacts"
test -s "$stage/.new-rpms"
test -s "$aur/PKGBUILD"
