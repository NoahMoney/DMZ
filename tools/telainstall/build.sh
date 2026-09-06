#!/usr/bin/env bash
# Builds telainstall inside vendor/tela (module mode; the tela module pins the civilware derohe fork).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DST="$ROOT/vendor/tela/cmd/telainstall"
mkdir -p "$DST"
cp "$ROOT/tools/telainstall/main.go" "$DST/main.go"
cd "$ROOT/vendor/tela"
GOFLAGS=-mod=mod go build -trimpath -o "$ROOT/vendor/bin/telainstall.exe" ./cmd/telainstall
echo "built $ROOT/vendor/bin/telainstall.exe"
