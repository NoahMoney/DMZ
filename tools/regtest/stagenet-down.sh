#!/usr/bin/env bash
# Stop the harness's stagenet monerod. The synced chain in .sim/stagenet is kept (a restart resumes the sync);
# --wipe deletes it.
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SDIR="$REPO/.sim/stagenet"
WIPE=0
[ "${1:-}" = "--wipe" ] && WIPE=1

# The watchdog first (X-25), so that it does not restart the monerod being stopped. A restart it is making when told to
# stop finishes the stop and starts nothing; that can take up to about 100 s.
wd="$(stagenet_watchdog_pid "$SDIR")"
if [ -n "$wd" ]; then
	wsl_exec kill -TERM "$wd" 2>/dev/null || true
	for i in $(seq 1 120); do
		wsl_exec kill -0 "$wd" 2>/dev/null || break
		sleep 1
	done
	if wsl_exec kill -0 "$wd" 2>/dev/null; then
		say "the stagenet watchdog (pid $wd in WSL) did not stop within 120 s; killing it"
		wsl_exec kill -KILL "$wd" 2>/dev/null || true
	fi
	say "stagenet watchdog stopped"
fi
rm -f "$SDIR/watchdog.pid"
stop_winpid stagenet-watchdog

if [ -f "$SDIR/monerod.pid" ]; then
	pid="$(cat "$SDIR/monerod.pid")"
	# monerod's own clean shutdown, then SIGINT, then SIGKILL; LMDB is flushed on the first two
	curl -sf --max-time 10 -X POST "http://127.0.0.1:$STAGENET_RPC_PORT/stop_daemon" >/dev/null 2>&1 || true
	for i in $(seq 1 60); do
		wsl_exec kill -0 "$pid" 2>/dev/null || break
		[ "$i" = 20 ] && wsl_exec kill -INT "$pid" 2>/dev/null
		sleep 1
	done
	if wsl_exec kill -0 "$pid" 2>/dev/null; then
		say "stagenet monerod (pid $pid in WSL) did not stop within 60 s; killing it"
		wsl_exec kill -KILL "$pid" 2>/dev/null || true
	fi
	rm -f "$SDIR/monerod.pid"
	say "stagenet monerod stopped"
else
	say "stagenet monerod is not running (no $SDIR/monerod.pid)"
fi
stop_winpid stagenet-monerod

if [ "$WIPE" = 1 ]; then
	# the chain is on WSL's ext4 since 2026-09-12 (stagenet-up.sh); an older one may still sit on the 9p mount
	WHOME="$(wsl_exec printenv HOME | tr -d '\r')"
	wsl_exec rm -rf "$WHOME/.dmz-regtest/stagenet"
	rm -rf "$SDIR/chain" "$SDIR/chain.old-9p" "$SDIR/progress.tsv"
	say "stagenet chain deleted"
fi
