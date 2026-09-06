#!/bin/bash
# end-to-end wrap/unwrap on the simulator: deposit → two signer passes (mint) → burn → passes until both signers voted Released
#   bash tools/simvault/e2e.sh <vault scid> [deposit units, default 50000]   (after: go build -o .sim/vaultsigner.exe ./vaultsigner; go build -o .sim/simvault.exe ./simvault; .sim/simvault.exe -install ...)
set -u
cd "$(dirname "$0")/.." || exit 1
V=${1:?vault scid}
S=../.sim/vaultsigner.exe
SV=../.sim/simvault.exe
rm -f ../.sim/vs-a.json ../.sim/vs-b.json ../.sim/vs-a.json.lock ../.sim/vs-b.json.lock
echo "== deposit"; $SV -vault "$V" -deposit 0:${2:-50000}
echo "== advance 3"; $SV -vault "$V" -advance 3
echo "== signer A pass 1"; $S -wallet 127.0.0.1:30003 -vault "$V" -state ../.sim/vs-a.json -once
echo "== signer B pass 1"; $S -wallet 127.0.0.1:30004 -vault "$V" -state ../.sim/vs-b.json -once
echo "== status"; $SV -vault "$V" -status
echo "== burn"; $SV -vault "$V" -burn 40000:4AxmrDestinationAddressForTheSimulatedChain0000000000000000000000000000000000000000000000
for i in 1 2 3 4 5 6; do
  echo "== signer A pass $((i+1))"; $S -wallet 127.0.0.1:30003 -vault "$V" -state ../.sim/vs-a.json -once
  echo "== signer B pass $((i+1))"; $S -wallet 127.0.0.1:30004 -vault "$V" -state ../.sim/vs-b.json -once
  if grep -q '"state": "voted"' ../.sim/vs-a.json 2>/dev/null && grep -q '"state": "voted"' ../.sim/vs-b.json 2>/dev/null; then break; fi
  sleep 40
done
echo "== final status"; $SV -vault "$V" -status
echo "== state A"; cat ../.sim/vs-a.json
echo "== state B"; cat ../.sim/vs-b.json
echo "== lock files left:"; ls ../.sim/*.lock 2>/dev/null || echo none
