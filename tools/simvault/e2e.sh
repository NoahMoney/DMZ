#!/bin/bash
# End-to-end wrap and unwrap on the simulator, in the order a real user follows, including the safety machinery:
#   register -> signers attest the deposit address -> the user pays it (a deposit older than the published address
#   is refused on purpose) -> mint -> burn -> the outsized-withdrawal breaker halts the vault -> an operator
#   resumes and judges the withdrawal legitimate -> the signers pay abroad and record the release.
#
#   bash tools/simvault/e2e.sh <vault scid> [deposit units, default 50000] [burn units, default 40000]
# Build first: cd tools && go build -o ../.sim/vaultsigner.exe ./vaultsigner && go build -o ../.sim/simvault.exe ./simvault
set -u
cd "$(dirname "$0")/../.." || exit 1
V=${1:?vault scid}
AMT=${2:-50000}
BURN=${3:-40000}
A="127.0.0.1:30003"
B="127.0.0.1:30004"
rm -f .sim/vs-a.json .sim/vs-b.json .sim/vs-a.json.lock .sim/vs-b.json.lock

sig()  { (cd tools && ../.sim/vaultsigner.exe -wallet "$1" -vault "$V" -state "../.sim/$2" ${3:-} -once); }
sv()   { (cd tools && ../.sim/simvault.exe -vault "$V" "$@"); }

echo "== signers attest the address for index 0"
sig $A vs-a.json; sig $B vs-b.json
sv -status | head -2

echo "== the user pays the published address ($AMT units)"
sv -deposit "0:$AMT"
sv -advance 4                      # past the confirmation policy

echo "== signers mint"
sig $A vs-a.json; sig $B vs-b.json
sv -status | head -2

echo "== the user burns $BURN to withdraw"
sv -burn "$BURN:4AxmrDestinationAddressForTheSimulatedChain00000000000000000000"

echo "== signers act: finality, the withdrawal delay, then the size breaker"
for i in 1 2 3 4 5 6; do
  sig $A vs-a.json; sig $B vs-b.json
  st=$(sv -status | grep -c "halted=1")
  if [ "$st" = "1" ]; then echo "-- the breaker halted the vault, as designed"; break; fi
  if grep -q '"state": "voted"' .sim/vs-a.json 2>/dev/null; then break; fi
  sleep 40
done

if sv -status | grep -q "halted=1"; then
  echo "== an operator judges the withdrawal legitimate: resume, then allow it explicitly"
  sv -halt 0
  sig $A vs-a.json "-allow-withdrawal 0"
  sig $B vs-b.json "-allow-withdrawal 0"
fi

echo "== final status"; sv -status
echo "== state A"; cat .sim/vs-a.json
echo "== state B"; cat .sim/vs-b.json
echo "== locks left:"; ls .sim/*.lock 2>/dev/null || echo none
