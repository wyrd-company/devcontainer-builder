#!/usr/bin/env bash
# relationships:
#   references: [compatibility, publish]

# Run a command without inherited Git overrides. Explicit fixture overrides can
# be passed through env after this boundary.
git_env() (
  for name in ${!GIT_@}; do
    unset "$name"
  done
  command "$@"
)

git() {
  git_env git "$@"
}
