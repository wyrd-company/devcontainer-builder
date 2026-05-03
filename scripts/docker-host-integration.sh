#!/usr/bin/env bash
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

go test -v -tags docker_integration ./cmd/devcontainer-docker -run TestDockerHostIntegration -count=1
