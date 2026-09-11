#!/usr/bin/env bash
# Prove tools/xmrswap's monero-wallet-rpc calls against the harness's real regtest monerod + monero-wallet-rpc,
# measure the unlock and fee facts, and compare every response shape with tools/xmrswap's fake wallet.
#
#   tools/regtest/smoke-monero.sh     (run tools/regtest/monero-up.sh first)
#
# Transcript: research/regtest/monero-smoke.log (tracked evidence; private keys are redacted from it).
# The real shapes go to .sim/regtest/monero/real-shapes.json; the comparison is appended to the transcript.
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

command -v go >/dev/null 2>&1 || die "go is not on PATH"
curl -sf --max-time 5 "http://127.0.0.1:$MONERO_RPC_PORT/json_rpc" -d '{"jsonrpc":"2.0","id":"0","method":"get_info"}' | tr -d ' \n' |
	grep -q '"nettype":"fakechain"' || die "no regtest monerod on 127.0.0.1:$MONERO_RPC_PORT — run tools/regtest/monero-up.sh"
curl -sf --max-time 5 "http://127.0.0.1:$MONERO_WALLET_RPC_PORT/json_rpc" -d '{"jsonrpc":"2.0","id":"0","method":"get_version"}' |
	grep -q '"version"' || die "no monero-wallet-rpc on 127.0.0.1:$MONERO_WALLET_RPC_PORT — run tools/regtest/monero-up.sh"

mkdir -p "$REPO/research/regtest" "$DATA/monero"
LOG="$REPO/research/regtest/monero-smoke.log"
SHAPES="$DATA/monero/real-shapes.json"
cd "$REPO/tools"
status=0
# -repo: the transcript writes the repository root as <repo> (no machine-specific path in a tracked file)
go run ./regtest/xmrsmoke -daemon "http://127.0.0.1:$MONERO_RPC_PORT" -wallet "http://127.0.0.1:$MONERO_WALLET_RPC_PORT" \
	-repo "$(cygpath -m "$REPO")" -log "$(cygpath -m "$LOG")" -shapes "$(cygpath -m "$SHAPES")" || status=$?
if [ -f "$SHAPES" ]; then
	{
		echo
		echo "== real vs tools/xmrswap FakeWallet (go run ./regtest/xmrshape)"
		go run ./regtest/xmrshape -real "$(cygpath -m "$SHAPES")" 2>&1 || echo "xmrshape did not run (tools/xmrswap may be mid-edit): exit $?"
	} | scrub | tee -a "$LOG"
fi
exit $status
