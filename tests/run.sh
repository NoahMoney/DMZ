#!/usr/bin/env bash
# Runs the Veil contract tests inside derohe's module (vendor mode), because derohe declares no requirements
# in its go.mod and can only be built against its own vendor directory. Sources stay in this repo; they are
# copied to vendor/derohe/veil/ on every run.
#   tests/run.sh                 # all tests
#   tests/run.sh -run Fuzz -v    # extra go test flags
#   VEIL_FUZZ_N=100000 tests/run.sh -run Fuzz
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DST="$ROOT/vendor/derohe/veil"
rm -rf "$DST"
mkdir -p "$DST"
cp -r "$ROOT/reference" "$DST/reference"
cp -r "$ROOT/tests" "$DST/tests"
cp -r "$ROOT/contracts" "$DST/contracts"
rm -f "$DST/tests/run.sh" "$DST/tests/run.ps1"
# C5: no floating point in money code (contracts are uint64; the models and bots must match them exactly)
if grep -rnE "float32|float64|Float64" "$ROOT/reference" "$ROOT/tests/harness" "$ROOT/tools/treasurybot" "$ROOT/tools/vaultsigner" --include="*.go" | grep -v _test.go; then
  echo "floating point in money code"
  exit 1
fi
cd "$ROOT/vendor/derohe"
go vet -mod=vendor ./veil/...
go test -mod=vendor -count=1 ./veil/... "$@"
