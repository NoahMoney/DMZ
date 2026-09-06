#!/usr/bin/env bash
# Builds xswdwallet inside the civilware derohe fork (its go.mod declares no requirements, so the program must live
# inside the module). Output: vendor/bin/xswdwallet.exe
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
DST="$ROOT/vendor/derohe-civilware/cmd/xswdwallet"
mkdir -p "$DST"
cp "$ROOT/tools/xswdwallet/"*.go "$DST/"
cd "$ROOT/vendor/derohe-civilware"
go build -trimpath -o "$ROOT/vendor/bin/xswdwallet.exe" ./cmd/xswdwallet
echo "built $ROOT/vendor/bin/xswdwallet.exe"
