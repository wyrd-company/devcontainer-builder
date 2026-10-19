#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/integration-timeout.sh"

cd "$(git rev-parse --show-toplevel)"

artifact_dir="${DCB_ARTIFACT_DIR:-artifacts/docker-host-integration}"
mkdir -p "${artifact_dir}"

export DCB_ARTIFACT_DIR="${artifact_dir}"
bash scripts/docker-cli-integration.sh all
