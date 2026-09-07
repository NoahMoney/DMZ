#!/bin/bash
# End-to-end wrap and unwrap on the simulator, in the order a real user follows:
#   register -> signers attest the deposit address -> the user pays it (a deposit older than the published
#   address is refused on purpose) -> mint -> burn -> the signers pay abroad, verify their own payment on the
#   foreign chain, and vote Released -> sup falls.
#
# What this script asserts, and why it is the assertion that matters:
#
#   THE VAULT MUST NEVER HALT DURING AN ORDINARY WITHDRAWAL.
#
# A claim leaves the foreign reserve when the federation pays it, and leaves `sup` only when the Released quorum
# records that payment. Between those two moments the reserve is legitimately below `sup`. A signer that halts on
# `reserve < sup` therefore halts on every honest withdrawal — and vault.bas Released refuses while halted, so
# `sup` can never fall, the condition can never clear, and the vault is frozen forever with no operator to lift
# it. An earlier version of this script watched for exactly that halt and reported it as an outsized-withdrawal
# breaker working as designed, which is how the freeze went unnoticed. There is no breaker. A halt here is a bug.
#
#   bash tools/simvault/e2e.sh <vault scid> [deposit units, default 50000] [burn units, default 5000]
#
# The default burn is deliberately under bigbps of the supply so it is payable at once. A burn above that share
# waits bigdelay extra blocks by design — correct, but too slow to assert inside a test run.
# Build first: cd tools && go build -o ../.sim/vaultsigner.exe ./vaultsigner && go build -o ../.sim/simvault.exe ./simvault
set -u
cd "$(dirname "$0")/../.." || exit 1
V=${1:?vault scid}
AMT=${2:-50000}
BURN=${3:-5000}
A="127.0.0.1:30003"
B="127.0.0.1:30004"
rm -f .sim/vs-a.json .sim/vs-b.json .sim/vs-a.json.lock .sim/vs-b.json.lock

sig()  { (cd tools && ../.sim/vaultsigner.exe -wallet "$1" -vault "$V" -state "../.sim/$2" -once); }
sv()   { (cd tools && ../.sim/simvault.exe -vault "$V" "$@"); }
fail() { echo "E2E FAILED: $*"; exit 1; }
# a halt at any point after the first burn is the freeze this script exists to catch
nohalt() { sv -status | grep -q "halted=1" && fail "the vault halted ($1). An ordinary withdrawal must never halt it: see the reserve invariant in tools/vaultsigner/main.go."; return 0; }

echo "== signers attest the address for index 0"
sig $A vs-a.json; sig $B vs-b.json
sv -status | head -2

echo "== the user pays the published address ($AMT units)"
sv -deposit "0:$AMT"
sv -advance 4                      # past the confirmation policy

echo "== signers mint"
sig $A vs-a.json; sig $B vs-b.json
sup=$(sv -status | sed -n 's/.*sup=\([0-9]*\).*/\1/p' | head -1)
[ "$sup" = "$AMT" ] || fail "supply is $sup after minting $AMT"
echo "-- supply $sup"

echo "== the user burns $BURN to withdraw"
sv -burn "$BURN:4AxmrDestinationAddressForTheSimulatedChain00000000000000000000"
nohalt "immediately after the burn"

echo "== signers wait for DERO finality, pay abroad, verify the payment, then vote Released"
want=$((sup - BURN))
for i in $(seq 1 25); do
  sig $A vs-a.json; sig $B vs-b.json
  nohalt "while paying withdrawal 0"
  now=$(sv -status | sed -n 's/.*sup=\([0-9]*\).*/\1/p' | head -1)
  echo "-- pass $i: supply $now (want $want)"
  [ "$now" = "$want" ] && break
  sleep 35
done

now=$(sv -status | sed -n 's/.*sup=\([0-9]*\).*/\1/p' | head -1)
[ "$now" = "$want" ] || fail "supply is $now, want $want: the withdrawal was never released"
grep -q '"state": "voted"' .sim/vs-a.json || fail "signer A never reached the voted state"
grep -q '"state": "voted"' .sim/vs-b.json || fail "signer B never reached the voted state"
nohalt "at the end of the run"

echo "== final status"; sv -status
echo "== state A"; cat .sim/vs-a.json
echo "== state B"; cat .sim/vs-b.json
echo "== locks left:"; ls .sim/*.lock 2>/dev/null || echo none
echo "E2E OK: wrapped $AMT, unwrapped $BURN, no halt at any point"
