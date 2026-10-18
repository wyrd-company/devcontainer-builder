#!/usr/bin/env bash
# relationships:
#   references: [taskfile]
set -euo pipefail
mode="$1"
shift
if (($# == 0)); then
  mapfile -d '' -t files < <(git ls-files -z '*.go' ':!:reference/**')
else
  files=("$@")
fi
((${#files[@]})) || exit 0
if [[ "$mode" == write ]]; then
  gofmt -w "${files[@]}"
else
  unformatted=$(gofmt -l "${files[@]}")
  if [[ -n "$unformatted" ]]; then
    printf 'Go files need formatting:\n%s\n' "$unformatted" >&2
    exit 1
  fi
fi
