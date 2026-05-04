#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

artifact_dir="${DCB_ARTIFACT_DIR:-artifacts/docker-build-integration}"
mkdir -p "${artifact_dir}"

generate_dir="${artifact_dir}/generated-features"
rm -rf "${generate_dir}"
go run ./cmd/dcb generate \
	--workspace testdata/smoke/features \
	--work-dir "${generate_dir}" \
	--skip-vscode-server \
	> "${artifact_dir}/generated-dockerfile-path.txt" \
	2> "${artifact_dir}/generate.log"
find "${generate_dir}" -maxdepth 5 -type f | sort > "${artifact_dir}/generated-context-files.txt"

export DCB_ARTIFACT_DIR="${artifact_dir}"
go test -v -tags docker_integration ./cmd/dcb -run TestDockerBuildIntegration -count=1
