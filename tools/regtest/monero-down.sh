#!/usr/bin/env bash
# Stop the harness's monero-wallet-rpc and regtest monerod. The chain and the wallet files are kept; --wipe deletes
# both (the wallet files hold throwaway regtest keys and nothing else).
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

MDIR="$DATA/monero"
WIPE=0
[ "${1:-}" = "--wipe" ] && WIPE=1

# stop_proc <name> <ask>: <ask> is the polite request (an RPC); then the pid, then a kill.
stop_proc() {
	local name="$1" ask="$2" pidf="$MDIR/$1.pid" pid
	if [ ! -f "$pidf" ]; then
		say "$name is not running (no $pidf)"
		stop_winpid "$name"
		return 0
	fi
	pid="$(cat "$pidf")"
	# The polite request can be refused (stop_wallet answers -13 "No wallet file" when no wallet is open, and does
	# not exit), so SIGINT follows after a short wait; both programs shut down cleanly on it. SIGKILL is last.
	eval "$ask" >/dev/null 2>&1 || true
	local i
	for i in $(seq 1 40); do
		wsl_exec kill -0 "$pid" 2>/dev/null || break
		[ "$i" = 10 ] && wsl_exec kill -INT "$pid" 2>/dev/null
		sleep 1
	done
	if wsl_exec kill -0 "$pid" 2>/dev/null; then
		say "$name (pid $pid in WSL) did not stop on request or SIGINT within 40 s; killing it"
		wsl_exec kill -KILL "$pid" 2>/dev/null || true
	fi
	rm -f "$pidf"
	stop_winpid "$name"
	say "$name stopped"
}

# stop_wallet saves the open wallet and exits; /stop_daemon is monerod's own clean shutdown.
stop_proc wallet-rpc "curl -sf --max-time 10 http://127.0.0.1:$MONERO_WALLET_RPC_PORT/json_rpc -H 'content-type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":\"0\",\"method\":\"stop_wallet\"}'"
stop_proc wallet-rpc-2 "curl -sf --max-time 10 http://127.0.0.1:$MONERO_WALLET_RPC_PORT2/json_rpc -H 'content-type: application/json' -d '{\"jsonrpc\":\"2.0\",\"id\":\"0\",\"method\":\"stop_wallet\"}'"
stop_proc monerod "curl -sf --max-time 10 -X POST http://127.0.0.1:$MONERO_RPC_PORT/stop_daemon -H 'content-type: application/json'"

if [ "$WIPE" = 1 ]; then
	rm -rf "$MDIR/chain" "$MDIR/wallets" "$MDIR/wallets-2" "$MDIR/ringdb"
	say "regtest chain and wallet files deleted"
fi
