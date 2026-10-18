#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

artifact_dir="${DCB_ARTIFACT_DIR:-artifacts/docker-compose-integration}"
mkdir -p "${artifact_dir}"

export DCB_ARTIFACT_DIR="${artifact_dir}"
go test -v -tags docker_integration ./pkg/host/dockercompose -count=1
