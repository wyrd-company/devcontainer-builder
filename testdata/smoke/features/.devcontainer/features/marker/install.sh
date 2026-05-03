#!/bin/sh
set -eu

marker="${MARKER:-/usr/local/share/dcb-smoke-feature}"
mkdir -p "$(dirname "$marker")"
printf 'feature-smoke\n' > "$marker"
