#!/usr/bin/env bash
# Stop the harness's regtest zebrad. The chain in .sim/regtest/zcash/state is kept; --wipe deletes it too.
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

ZDIR="$DATA/zcash"
WIPE=0
[ "${1:-}" = "--wipe" ] && WIPE=1

if [ -f "$ZDIR/zebrad.pid" ]; then
	pid="$(cat "$ZDIR/zebrad.pid")"
	# zebrad shuts down cleanly on SIGINT (its state is flushed), so no RPC is needed and nothing is assumed about one.
	wsl_exec kill -INT "$pid" 2>/dev/null || true
	for _ in $(seq 1 30); do
		wsl_exec kill -0 "$pid" 2>/dev/null || break
		sleep 1
	done
	if wsl_exec kill -0 "$pid" 2>/dev/null; then
		say "zebrad (pid $pid in WSL) did not stop on SIGINT within 30 s; killing it"
		wsl_exec kill -KILL "$pid" 2>/dev/null || true
	fi
	rm -f "$ZDIR/zebrad.pid"
	say "zebrad stopped"
else
	say "zebrad is not running (no $ZDIR/zebrad.pid)"
fi
stop_winpid zebrad
rm -f "$ZDIR/.cookie"

if [ "$WIPE" = 1 ]; then
	rm -rf "$ZDIR/state"
	say "regtest chain state deleted"
fi
