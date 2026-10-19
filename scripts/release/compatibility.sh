#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Adapted from wyrd-company/trowel; see npm/NOTICE and npm/LICENSE-APACHE-2.0.
# relationships:
#   references: [manifest, release]

set -euo pipefail

# Git commands below operate on disposable repositories, never the caller's
# repository selected by a hook or rebase environment. Keep overrides local
# to Git so the publisher commands retain their normal environment.
git() (
  for name in ${!GIT_@}; do
    unset "$name"
  done
  export GIT_AUTHOR_NAME=Example GIT_AUTHOR_EMAIL=example@example.invalid
  export GIT_COMMITTER_NAME=Example GIT_COMMITTER_EMAIL=example@example.invalid
  export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
  command git "$@"
)

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

python3 "$repository_script" validate "$manifest"
git init --quiet --initial-branch=release-manifests "$inbox"
git -C "$inbox" config user.name "Package Contract Test"
git -C "$inbox" config user.email "package-contract@example.invalid"
git -C "$inbox" commit --quiet --allow-empty -m "Initialize package inbox"
submission="$(python3 "$repository_script" submit "$manifest" "$inbox")"
IFS=$'\t' read -r state submitted_path <<<"$submission"
if [[ "$state" != "created" ]]; then
  echo "publisher did not create a new immutable inbox manifest" >&2
  exit 2
fi
if [[ ! "$submitted_path" =~ ^releases/dcb/[0-9]+\.[0-9]+\.[0-9]+\.json$ ]]; then
  echo "publisher returned an unexpected immutable inbox path: $submitted_path" >&2
  exit 2
fi
git -C "$inbox" add -- "$submitted_path"
git -C "$inbox" commit --quiet -m "Add package release manifest"
inbox_commit="$(git -C "$inbox" rev-parse HEAD)"
submitted_manifest="$inbox/$submitted_path"
python3 "$repository_script" validate-queue \
  "$inbox" "$inbox_commit" "$submitted_path" "$allowlist"
python3 "$repository_script" stage "$submitted_manifest" "$artifacts" "$stage" "$public_key"
python3 "$repository_script" render-aur "$submitted_manifest" "$aur" --artifacts "$artifacts"
test -s "$stage/.new-rpms"
test -s "$aur/PKGBUILD"
