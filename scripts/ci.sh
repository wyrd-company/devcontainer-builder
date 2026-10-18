#!/usr/bin/env bash
# relationships:
#   references: [taskfile]
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
exec task check
