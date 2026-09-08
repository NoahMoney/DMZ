#!/bin/bash
# UI passes against the running simulator: read-only, then the wallet flow with a private buy paid to wallet 30006
set -u
cd "$(dirname "$0")/../.." || exit 1
# The maths first, with no browser: amm.js picks every min_out and prints every quote, so if it disagrees with
# the contract there is nothing worth screenshotting until it is green.
echo "== amm assertions (node --test tests/js)"
node --test tests/js || { echo "exit 1: tests/js failed"; exit 1; }
: "${PW_DIR:?set PW_DIR to a directory that has playwright and its browsers installed}"
DEST=$(curl -s -m 10 -X POST http://127.0.0.1:30006/json_rpc -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":"1","method":"GetAddress"}' | sed -E 's/.*"address":"([^"]+)".*/\1/')
echo "dest wallet 30006: $DEST"
echo "== read-only pass"
node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-ro 2>&1 | tail -40
echo "exit ${PIPESTATUS[0]}"
echo "== wallet pass (private buy to $DEST)"
node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-w --wallet --dest="$DEST" 2>&1 | tail -60
echo "exit ${PIPESTATUS[0]}"
