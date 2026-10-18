#!/usr/bin/env sh
# Validate that a commit subject is a conventional commit.
#
# Used for both commit messages locally and pull request titles in CI, because
# squash merge turns the title into the commit on main — the same rule has to
# apply to both or history is inconsistent.

# relationships:
#   references: [taskfile]
set -eu
if [ "${1-}" = --file ]; then
  shift
  set -- "$(head -n 1 "$1")"
fi

subject=$(printf '%s' "${1-}" | head -n 1)
pattern='^(build|chore|ci|docs|feat|fix|perf|refactor|revert|style|test)(\([a-z0-9._/-]+\))?!?: .+'

if printf '%s' "$subject" | grep -Eq "$pattern"; then
  exit 0
fi

echo "not a conventional commit subject: $subject" >&2
echo "expected <type>[(scope)][!]: <description>" >&2
exit 1
