#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

go test -v -tags docker_integration ./pkg/host/dockercompose -run TestDockerComposeIntegration -count=1
