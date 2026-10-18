#!/usr/bin/env bash
# relationships:
#   references: [taskfile]
set -euo pipefail
suite="$1"
# Integration must not consume the operator's registry configuration.
export DOCKER_CONFIG
DOCKER_CONFIG=$(mktemp -d)
printf '{}\n' > "$DOCKER_CONFIG/config.json"
container=''
cleanup() {
  if [[ -n "$container" ]]; then
    docker rm -fv "$container" >/dev/null
  fi
  rm -rf "$DOCKER_CONFIG"
}
trap cleanup EXIT
unset BUILDKIT_HOST
mkdir -p artifacts
export DCB_ARTIFACT_DIR="$PWD/artifacts/docker-${suite}-integration"
if [[ "$suite" == remote ]]; then
  # Preserve the existing CI readiness budget and probe.
  container=$(docker run --rm --privileged -d -p 127.0.0.1::1234 \
    moby/buildkit:buildx-stable-1 --addr tcp://0.0.0.0:1234)
  BUILDKIT_HOST="tcp://$(docker port "$container" 1234/tcp)"
  export BUILDKIT_HOST
  ready=false
  for ((i=0; i<30; i++)); do
    if docker logs "$container" 2>&1 | grep -q 'running server'; then
      ready=true
      break
    fi
    sleep 1
  done
  if [[ "$ready" != true ]]; then
    docker logs "$container"
    exit 1
  fi
fi
case "$suite" in
  all)
    go test -v -tags docker_integration ./... -count=1 2>&1 | tee artifacts/docker-all-integration.log
    ;;
  remote)
    go test -v -tags docker_integration ./pkg/builder -run TestEngineBuildIntegration -count=1 2>&1 | tee artifacts/docker-remote-integration.log
    ;;
  *)
    bash "scripts/docker-${suite}-integration.sh" 2>&1 | tee "artifacts/docker-${suite}-integration.log"
    ;;
esac
