#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/integration-timeout.sh"

cd "$(git rev-parse --show-toplevel)"

artifact_dir="${DCB_ARTIFACT_DIR:-artifacts/docker-build-integration}"
mkdir -p "${artifact_dir}"

generate_dir="${artifact_dir}/generated-features"
rm -rf "${generate_dir}"
go run ./cmd/dcb generate \
	--workspace-folder testdata/smoke/features \
	--work-dir "${generate_dir}" \
	> "${artifact_dir}/generated-dockerfile-path.txt" \
	2> "${artifact_dir}/generate.log"
find "${generate_dir}" -maxdepth 5 -type f | sort > "${artifact_dir}/generated-context-files.txt"

export DCB_ARTIFACT_DIR="${artifact_dir}"
go test -timeout "$integration_test_timeout" -v -tags docker_integration ./pkg/builder ./pkg/buildkit ./test/integration/buildcontext -count=1
bash scripts/docker-cli-integration.sh all
