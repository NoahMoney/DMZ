#!/usr/bin/env bash
# Installs the testnet bundle on TELA using the DEPLOYER wallet FILE (.testnet/deployer.db). telainstall drives
# walletapi directly, so the deployer's netwallet process (RPC 40405) must be stopped first: two processes on one
# wallet file corrupt its state. Run from the repo root after deploy/testnet-deploy.sh printed TESTNET_DEPLOY_DONE.
#   deploy/testnet-tela.sh [-index <existing INDEX scid> -reuse deploy/tela-testnet-reuse.txt] [-serve]
set -euo pipefail
cd "$(dirname "$0")/.."
test -f dist/testnet/manifest.json || { echo "dist/testnet missing: run tools/genconfig -dist -network testnet"; exit 1; }
if curl -s -m 3 -o /dev/null -X POST http://127.0.0.1:40405/json_rpc -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":"1","method":"GetHeight"}'; then
  echo "a wallet RPC answers on 127.0.0.1:40405: stop the deployer netwallet before installing (Stop-Process -Name netwallet)"
  exit 1
fi
vendor/bin/telainstall.exe -network testnet -wallet .testnet/deployer.db -password= -daemon 127.0.0.1:40402 -app dist/testnet -durl dmz.tela -out deploy/tela-testnet.json "$@"
echo "TESTNET_TELA_DONE"
