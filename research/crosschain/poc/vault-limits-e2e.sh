#!/usr/bin/env bash
# Limits on the real DVM: a fresh 2-of-3 vault with SetLimits(maxdep 30000, winmax 60000, wdelay 3). A 40,000
# deposit is refused at the k-th vote (above maxdep), a 25,000 one is minted, a burn is released only after the
# delay and DERO finality. Run from fomo/.sim with the simulator up.
set -e
SIM=../research/crosschain/poc/sim-foreign.json
H() { curl -s -m 5 -X POST http://127.0.0.1:20000/json_rpc -H "Content-Type: application/json" --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"DERO.GetHeight\"}" | sed -n "s/.*\"$1\":\([0-9]*\).*/\1/p"; }
./simvault.exe -install -maxdep 30000 -winmax 60000 -wdelay 3 -sim $SIM | tee /tmp/vault-limits-install.log
V=$(grep '^VAULT=' /tmp/vault-limits-install.log | cut -d= -f2)
echo "vault $V"
echo "== attest index 0"
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30003 -vault $V -once -sim $SIM -state vaultsigner-limits-30003.json | grep -i "attest\|vault:"
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-limits-30004.json | grep -i "attest\|vault:"
echo "== a 40,000 deposit: above maxdep, the k-th vote must be refused and nothing minted"
./simvault.exe -vault $V -deposit 0:40000 -sim $SIM
./simvault.exe -vault $V -advance 3 -sim $SIM
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30003 -vault $V -once -sim $SIM -state vaultsigner-limits-30003.json | grep -i "mint\|refused\|vault:"
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-limits-30004.json | grep -i "mint\|refused\|vault:"
./simvault.exe -vault $V -status -sim $SIM | head -1
echo "== a 25,000 deposit: minted"
./simvault.exe -vault $V -deposit 0:25000 -sim $SIM
./simvault.exe -vault $V -advance 3 -sim $SIM
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30003 -vault $V -once -sim $SIM -state vaultsigner-limits-30003.json | grep -i "mint\|vault:"
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-limits-30004.json | grep -i "mint\|vault:"
./simvault.exe -vault $V -status -sim $SIM | head -1
echo "== waiting two blocks (fresh asset), then burn 4000"; sleep 45
./simvault.exe -vault $V -burn 4000:4AxmrDestinationAddressStandIn -sim $SIM
BURNH=$(H height)
echo "== immediate signer pass: must wait (finality and the 3-block delay)"
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-limits-30004.json | grep -i "finality\|vault:" || true
for i in $(seq 1 40); do S=$(H stableheight); [ -n "$S" ] && [ "$S" -ge "$BURNH" ] && break; sleep 10; done
echo "stable height $S (burn at most $BURNH)"
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-limits-30004.json | grep -i "released\|vault:"
./vaultsigner.exe -backend sim -wallet 127.0.0.1:30005 -vault $V -once -sim $SIM -state vaultsigner-limits-30005.json | grep -i "released\|vault:"
./simvault.exe -vault $V -status -sim $SIM
echo "VAULT_LIMITS_E2E_DONE"
