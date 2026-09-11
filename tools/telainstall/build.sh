#!/usr/bin/env bash
# Builds telainstall inside vendor/tela. The tela module replaces derohe with the civilware fork, which the go command
# fetches from the module proxy, so this is a module-mode build. It is a read-only one (GAP-017):
# - go.mod and go.sum stay the pinned commit's;
# - every module must already be in go.sum with its hash, and the go command refuses to add or change one;
# - verifyvendor -modules-only runs first. The go.mod replace and the go.sum hashes must be the ones pinned in
#   deploy/vendor.lock ("modules"), and every copy of a go.sum module in the local module cache must still hash to
#   its go.sum line. go checks a module only when it downloads it, so a cache copy changed later would be built as-is.
#   tools/telainstall/build.sh               # the go on PATH
#   GO=go1.26.8 tools/telainstall/build.sh   # another toolchain
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
GO="${GO:-go}"
# GOFLAGS could carry -mod=mod; the -mod flag below wins over it, and it is dropped anyway so nothing else rides in
unset GOFLAGS
(cd "$ROOT/tools" && "$GO" run ./verifyvendor -root .. -modules-only)
DST="$ROOT/vendor/tela/cmd/telainstall"
mkdir -p "$DST"
cp "$ROOT/tools/telainstall/main.go" "$DST/main.go"
cd "$ROOT/vendor/tela"
"$GO" build -mod=readonly -trimpath -o "$ROOT/vendor/bin/telainstall.exe" ./cmd/telainstall
echo "built $ROOT/vendor/bin/telainstall.exe (-mod=readonly; re-pin with: cd tools && go run ./verifyvendor -root .. -write-binaries)"
