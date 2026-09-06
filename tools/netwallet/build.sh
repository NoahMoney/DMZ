#!/usr/bin/env bash
# Builds netwallet inside vendor/derohe (vendor mode, like the contract tests).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DST="$ROOT/vendor/derohe/cmd/netwallet"
mkdir -p "$DST"
cp "$ROOT/tools/netwallet/main.go" "$DST/main.go"
cd "$ROOT/vendor/derohe"
go build -mod=vendor -trimpath -o "$ROOT/vendor/bin/netwallet.exe" ./cmd/netwallet
echo "built $ROOT/vendor/bin/netwallet.exe"
