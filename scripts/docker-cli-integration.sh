#!/usr/bin/env bash
# relationships:
#   references: [cli-integration-shards, integration-timeout]
set -euo pipefail
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/integration-timeout.sh"
selection=${1:-all}
found=false
while IFS=$'\t' read -r shard pattern; do
  [[ -z "$shard" || "$shard" == \#* ]] && continue
  if [[ "$selection" != all && "$selection" != "$shard" ]]; then
    continue
  fi
  found=true
  echo "CLI Docker shard: $shard"
  go test -timeout "$integration_test_timeout" -v -tags docker_integration ./cmd/dcb -run "$pattern" -count=1
done < "$script_dir/cli-integration-shards.tsv"
if [[ "$found" != true ]]; then
  echo "Unknown CLI Docker shard: $selection" >&2
  exit 1
fi
