#!/usr/bin/env bash
# Builds genesistx inside vendor/derohe (vendor mode) and rebuilds derod with the private-genesis patch.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DST="$ROOT/vendor/derohe/cmd/genesistx"
mkdir -p "$DST"
cp "$ROOT/tools/genesistx/main.go" "$DST/main.go"
cd "$ROOT/vendor/derohe"
go build -mod=vendor -trimpath -o "$ROOT/vendor/bin/genesistx.exe" ./cmd/genesistx
go build -mod=vendor -trimpath -o "$ROOT/vendor/bin/derod.exe" ./cmd/derod
echo "built genesistx.exe and derod.exe (private-genesis patch)"
