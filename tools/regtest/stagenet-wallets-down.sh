#!/usr/bin/env bash
# Stop the two stagenet monero-wallet-rpc that stagenet-wallets-up.sh started. The wallet files are kept.
#
# Only such a process is stopped: the pinned monero-wallet-rpc running --stagenet with its RPC on 127.0.0.1 at the
# party's port, inside WSL (common.sh stagenet_wallet_rpc_pid), found by the pid file stagenet-wallets-up.sh's launcher
# wrote or, when that file is gone or stale, by its command line. Whatever else holds those ports is left alone and
# said: it is sent no stop_wallet and no signal.
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

SW_ALICE_PORT="${STAGENET_WALLET_RPC_PORT:-38088}"
SW_BOB_PORT="${STAGENET_WALLET_RPC_PORT2:-38087}"
need_wsl
WHOME="$(wsl_exec printenv HOME | tr -d '\r')"
WDIR="$WHOME/.dmz-regtest/stagenet-wallets"
WRPC="$WHOME/.dmz-regtest/monero-v$MONERO_VERSION/monero-wallet-rpc"

for pair in "stagenet-wallet-alice:$SW_ALICE_PORT" "stagenet-wallet-bob:$SW_BOB_PORT"; do
	name="${pair%%:*}"
	port="${pair#*:}"
	pidf="$WDIR/$name.pid"
	ours=""
	pid="$(wsl_exec cat "$pidf" 2>/dev/null | tr -d '\r' || true)"
	if [ -n "$pid" ]; then
		ours="$(stagenet_wallet_rpc_pid "$WRPC" "$port" "$pid")"
		if [ -z "$ours" ]; then
			say "$name: pid $pid in $pidf is not the pinned stagenet monero-wallet-rpc on 127.0.0.1:$port (it has exited, or the pid is another process's now): not signalling it; removing the stale pid file"
			wsl_exec rm -f "$pidf" || true
		fi
	fi
	[ -n "$ours" ] || ours="$(stagenet_wallet_rpc_pid "$WRPC" "$port")"
	if [ -z "$ours" ]; then
		if port_listening "$port"; then
			say "$name: something holds 127.0.0.1:$port that is not the pinned stagenet monero-wallet-rpc: left alone"
		else
			say "$name is not running"
		fi
		rm -f "$DATA/$name.winpid" # its wsl.exe host went with it; a stale Windows pid is not signalled
		continue
	fi
	pid="$ours"
	# stop_wallet stores the open wallet and ends the rpc cleanly; then SIGINT, then SIGKILL, each only while the pid is
	# still the process checked above
	curl -sf --max-time 30 "http://127.0.0.1:$port/json_rpc" -H 'content-type: application/json' \
		-d '{"jsonrpc":"2.0","id":"0","method":"stop_wallet"}' >/dev/null 2>&1 || true
	for i in $(seq 1 30); do
		[ -n "$(stagenet_wallet_rpc_pid "$WRPC" "$port" "$pid")" ] || break
		if [ "$i" = 15 ]; then
			wsl_exec kill -INT "$pid" 2>/dev/null || true
		fi
		sleep 1
	done
	if [ -n "$(stagenet_wallet_rpc_pid "$WRPC" "$port" "$pid")" ]; then
		say "$name (pid $pid in WSL) did not stop within 30 s; killing it"
		wsl_exec kill -KILL "$pid" 2>/dev/null || true
	fi
	wsl_exec rm -f "$pidf" || true
	stop_winpid "$name"
	say "$name stopped"
done
