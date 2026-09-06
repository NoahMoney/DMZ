#!/usr/bin/env bash
# Waits until the owner wallet (40405) has caught up with the daemon (40402), stops any running simdeploy, and
# runs deploy/testnet-deploy.sh. Used after moving the premine off the owner: a 100,000,000 DERO balance costs
# the wallet minutes per block to decode (walletapi Lookup walks 2^21 units per step), which starves the deploy.
# Run from the repo root: bash deploy/testnet-redeploy-when-synced.sh > .testnet/deploy3.log 2>&1
set -u
height() { curl -s -m 5 -X POST "http://127.0.0.1:$1/json_rpc" -H "Content-Type: application/json" --data "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$2\"}" | sed -n 's/.*"height":\([0-9]*\).*/\1/p'; }
for i in $(seq 1 120); do
  d=$(height 40402 DERO.GetHeight); w=$(height 40405 GetHeight)
  echo "$(date +%T) daemon ${d:-?} owner wallet ${w:-?}"
  if [ -n "$d" ] && [ -n "$w" ] && [ "$w" -ge "$d" ]; then break; fi
  sleep 30
done
powershell -NoProfile -Command "Get-Process simdeploy -ErrorAction SilentlyContinue | Stop-Process -Force" >/dev/null 2>&1 || true
sleep 2
exec bash deploy/testnet-deploy.sh
