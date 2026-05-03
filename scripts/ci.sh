#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

echo "==> go test ./..."
go test ./...

echo "==> go test -race ./pkg/host/runner ./pkg/substitute ./pkg/features ./pkg/lifecycle"
go test -race ./pkg/host/runner ./pkg/substitute ./pkg/features ./pkg/lifecycle

echo "==> go vet ./..."
go vet ./...

echo "==> gofmt check"
mapfile -d '' go_files < <(git ls-files -z '*.go' ':!:reference/**')
if ((${#go_files[@]} > 0)); then
	unformatted="$(gofmt -l "${go_files[@]}")"
	if [[ -n "${unformatted}" ]]; then
		echo "The following Go files need gofmt:"
		echo "${unformatted}"
		exit 1
	fi
fi

echo "CI checks passed."
