#!/usr/bin/env bash
# End-to-end wrap/unwrap on the DERO simulator with the simulated foreign chain. Run from fomo/.sim.
set -e
SIM=../research/crosschain/poc/sim-foreign.json
[ -n "$FUND" ] && ./simvault.exe -fund $FUND -sim $SIM
if [ -n "$VAULT" ]; then V=$VAULT; else
  ./simvault.exe -install -sim $SIM | tee /tmp/vault-install.log
  V=$(grep '^VAULT=' /tmp/vault-install.log | cut -d= -f2)
fi
echo "vault $V"
echo "== signers attest the user's deposit address (index 0)"
./vaultsigner.exe -wallet 127.0.0.1:30003 -vault $V -once -sim $SIM -state vaultsigner-30003.json
./vaultsigner.exe -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-30004.json
./simvault.exe -vault $V -status -sim $SIM
echo "== user deposits 25000 foreign units; chain advances 3 blocks"
./simvault.exe -vault $V -deposit 0:25000 -sim $SIM
./simvault.exe -vault $V -advance 3 -sim $SIM
echo "== signers vote Mint (2 of 3)"
./vaultsigner.exe -wallet 127.0.0.1:30003 -vault $V -once -sim $SIM -state vaultsigner-30003.json
./vaultsigner.exe -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-30004.json
./simvault.exe -vault $V -status -sim $SIM
echo "== waiting two blocks: a freshly minted asset is unspendable for about two blocks (R-15)"; sleep 45
echo "== user burns 4000 to withdraw"
./simvault.exe -vault $V -burn 4000:4AxmrDestinationAddressStandIn -sim $SIM
echo "== a signer pass right after the burn must wait: the burn is not yet at the stable height (C6)"
./vaultsigner.exe -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-30004.json | grep -i "finality\|withdrawals" || true
echo "== waiting until the daemon calls the burn block stable (about 8 blocks)"
BURNH=$(curl -s -m 5 -X POST http://127.0.0.1:20000/json_rpc -H "Content-Type: application/json" --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"DERO.GetHeight\"}" | sed -n "s/.*\"height\":\([0-9]*\).*/\1/p")
for i in $(seq 1 40); do
  S=$(curl -s -m 5 -X POST http://127.0.0.1:20000/json_rpc -H "Content-Type: application/json" --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"DERO.GetHeight\"}" | sed -n "s/.*\"stableheight\":\([0-9]*\).*/\1/p")
  [ -n "$S" ] && [ "$S" -ge "$BURNH" ] && break
  sleep 10
done
echo "stable height $S, burn height at most $BURNH"
echo "== signers pay the withdrawal on the foreign chain and vote Released"
./vaultsigner.exe -wallet 127.0.0.1:30004 -vault $V -once -sim $SIM -state vaultsigner-30004.json
./vaultsigner.exe -wallet 127.0.0.1:30005 -vault $V -once -sim $SIM -state vaultsigner-30005.json
./simvault.exe -vault $V -status -sim $SIM
echo "== foreign chain file"; cat $SIM
