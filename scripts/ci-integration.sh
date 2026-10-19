#!/usr/bin/env bash
# relationships:
#   references: [taskfile]
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/integration-timeout.sh"
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
  container=$(docker run --rm --privileged -d -p 127.0.0.1::1234 \
    moby/buildkit:buildx-stable-1 --addr tcp://0.0.0.0:1234)
  BUILDKIT_HOST="tcp://$(docker port "$container" 1234/tcp)"
  export BUILDKIT_HOST
  ready_pattern='running server'
elif [[ "$suite" == classic ]]; then
  container=$(docker run --rm --privileged -d -e DOCKER_TLS_CERTDIR= -p 127.0.0.1::2375 \
    docker:dind --storage-driver=overlay2 --feature containerd-snapshotter=false)
  classic_host="tcp://$(docker port "$container" 2375/tcp)"
  ready_pattern='API listen on.*:2375'
fi
if [[ -n "$container" ]]; then
  # Reuse the existing CI readiness budget for either disposable service.
  ready=false
  for ((i=0; i<30; i++)); do
    if docker logs "$container" 2>&1 | grep -q "$ready_pattern"; then
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
    go test -timeout "$integration_test_timeout" -v -tags docker_integration ./... -count=1 2>&1 | tee artifacts/docker-all-integration.log
    ;;
  classic)
    # Check the daemon variant, then address only that daemon for the tests.
    # Container management and cleanup keep using the parent Docker endpoint.
    driver=$(docker exec "$container" docker info --format '{{.Driver}}')
    if [[ "$driver" != overlay2 ]]; then
      echo "classic Docker driver = $driver, want overlay2" >&2
      exit 1
    fi
    echo "Classic Docker image store: $driver"
    env -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH DOCKER_HOST="$classic_host" \
      go test -timeout "$integration_test_timeout" -v -tags docker_integration ./pkg/buildkit ./pkg/builder \
      -run 'TestDaemon.*Integration|TestEngineBuildIntegration' -count=1 2>&1 | tee artifacts/docker-classic-integration.log
    ;;
  remote)
    go test -timeout "$integration_test_timeout" -v -tags docker_integration ./pkg/builder -run TestEngineBuildIntegration -count=1 2>&1 | tee artifacts/docker-remote-integration.log
    ;;
  *)
    bash "scripts/docker-${suite}-integration.sh" 2>&1 | tee "artifacts/docker-${suite}-integration.log"
    ;;
esac
