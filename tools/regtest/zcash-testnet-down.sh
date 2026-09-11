#!/usr/bin/env bash
# Stop the harness's Zcash testnet zebrad and its rpcgate. The synced state (in WSL, $HOME/.dmz-regtest/zcash-testnet)
# is kept, so a restart resumes the sync; --wipe deletes it.
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

TDIR="$REPO/.sim/zcash-testnet"
WIPE=0
[ "${1:-}" = "--wipe" ] && WIPE=1

if [ -f "$TDIR/zebrad.pid" ]; then
	pid="$(cat "$TDIR/zebrad.pid")"
	# zebrad flushes its state and exits on SIGINT; a large RocksDB can take a while
	wsl_exec kill -INT "$pid" 2>/dev/null || true
	for _ in $(seq 1 90); do
		wsl_exec kill -0 "$pid" 2>/dev/null || break
		sleep 1
	done
	if wsl_exec kill -0 "$pid" 2>/dev/null; then
		say "zebrad (pid $pid in WSL) did not stop on SIGINT within 90 s; killing it"
		wsl_exec kill -KILL "$pid" 2>/dev/null || true
	fi
	rm -f "$TDIR/zebrad.pid"
	say "zcash testnet zebrad stopped"
else
	say "zcash testnet zebrad is not running (no $TDIR/zebrad.pid)"
fi
if [ -f "$TDIR/rpcgate.pid" ]; then
	wsl_exec kill "$(cat "$TDIR/rpcgate.pid")" 2>/dev/null || true
	rm -f "$TDIR/rpcgate.pid" "$TDIR/rpcgate.sha256"
	say "rpcgate stopped"
fi
stop_winpid zcash-testnet-rpcgate
stop_winpid zcash-testnet
rm -f "$TDIR/.cookie"

if [ "$WIPE" = 1 ]; then
	WHOME="$(wsl_exec printenv HOME | tr -d '\r')"
	wsl_exec rm -rf "$WHOME/.dmz-regtest/zcash-testnet"
	rm -f "$TDIR/progress.tsv"
	say "zcash testnet state deleted"
fi
