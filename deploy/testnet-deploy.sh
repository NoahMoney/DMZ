#!/usr/bin/env bash
# Deploys the contracts to the (private) testnet once the treasury wallet RPC answers, then builds the testnet bundle.
# Run from the repo root. Wallets: deployer (owner) 40405, treasury 40404 (tools/netwallet), daemon 40402. The miner/integrator wallet (40403) never transacts: a balance that changes every block makes the wallet decode for minutes.
set -euo pipefail
cd "$(dirname "$0")/.."
echo "waiting for the treasury wallet rpc ..."
for i in $(seq 1 120); do
  if curl -s -m 5 -H "Content-Type: application/json" -X POST http://127.0.0.1:40404/json_rpc -d '{"jsonrpc":"2.0","id":"1","method":"GetAddress"}' | grep -q deto1; then break; fi
  sleep 10
done
cd tools
# the private testnet mines slowly; allow long confirmations
# htlc.bas v3's install widths are required; g2 4800 is the simulator record's value, pending operator decision MT-J13 (G2)
go run ./simdeploy -network testnet -daemon 127.0.0.1:40402 -w0 127.0.0.1:40405 -w2 127.0.0.1:40404 -networks ../deploy/networks.json -timeout 45m \
  -htlc-w1min 400 -htlc-w2min 400 -htlc-g2 4800
go run ./genconfig -dist -network testnet
go run ./genconfig -network simulator   # keep app/config.js on the simulator for development
echo "TESTNET_DEPLOY_DONE"
