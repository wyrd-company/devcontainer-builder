#!/usr/bin/env bash
# relationships:
#   references: [taskfile]
# Validate release metadata. Before the first release tag there is no authority
# to check, so status validates config, discovery, and intents. While the
# tag-only docs unit has no baseline tag, check runs in a scratch clone that
# baselines it at the docs-version in docs/user/docs.yml; this repository is
# never tagged.
set -euo pipefail
if [ -z "$(git tag --list '[0-9]*.[0-9]*.[0-9]*')" ]; then
  intentional status
  exit 0
fi
if [ -n "$(git tag --list 'dcb-docs@*')" ]; then
  intentional check
  exit 0
fi
root="$(git rev-parse --show-toplevel)"
version="$(sed -n 's/^ *docs-version: *//p' "$root/docs/user/docs.yml" | head -n 1)"
if [ -z "$version" ]; then
  echo "docs/user/docs.yml has no docs-version to baseline dcb-docs." >&2
  exit 1
fi
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
git clone --quiet --shared "$root" "$scratch"
# Include pending edits so this is useful before committing.
rm -rf "$scratch/.intentional"
cp -R "$root/.intentional" "$scratch/.intentional"
cp "$root/docs/user/docs.yml" "$scratch/docs/user/docs.yml"
cp "$root/npm/package.json" "$scratch/npm/package.json"
cd "$scratch"
intentional tag --baseline --version "dcb-docs=$version"
intentional check
