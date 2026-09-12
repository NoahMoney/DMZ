#!/usr/bin/env bash
# The stagenet monerod's watchdog (X-25). tools/regtest/stagenet-up.sh starts it detached inside WSL, beside monerod,
# and tools/regtest/stagenet-down.sh stops it before monerod.
#
#   stagenet-watchdog.sh --run         the watch loop: what stagenet-up.sh starts, inside WSL
#   stagenet-watchdog.sh --self-test   drives the watch against a fake node in a temporary directory (Git Bash or WSL);
#                                      it talks to no monerod and starts, stops or restarts nothing
#
# Why. On 2026-09-12 the stagenet monerod twice sat at 0 peers with target_height 0 after an internet outage and did not
# recover until it was restarted by hand; each restart found peers within a minute.
#
# The rule. Once a minute it asks get_info. The node is unwell when it answers with no peers, or when it does not answer
# and the last "Synced a/b" line of its log is the one the previous check saw. monerod's RPC stops answering while it
# verifies heavy blocks, so no answer alone is not a fault: a log that moves is a node at work. After 10 minutes unwell
# with no healthy check between, the watchdog restarts monerod: the stop stagenet-down.sh makes (stop_daemon, then
# SIGINT, then SIGKILL; LMDB is flushed by the first two), then the launcher stagenet-up.sh wrote, run-monerod.sh,
# unchanged: the same binary, flags, data directory and log. The chain is reused. Nothing here deletes any chain data;
# stagenet-down.sh --wipe is the only thing that does. Each restart is a line in .sim/stagenet/watchdog.log.
#
# A restarted monerod runs in the watchdog's own WSL session, whose wsl.exe keeps the instance up; its pid is in
# monerod.pid as before (run-monerod.sh writes it), so stagenet-down.sh stops it the same way.
#
# Settings, from the environment (the defaults are the real node's):
#   WD_DIR        the stagenet directory as WSL sees it (run-monerod.sh, monerod.pid)   default <repo>/.sim/stagenet
#   WD_RPC_URL    get_info's URL                                     default http://127.0.0.1:$STAGENET_RPC_PORT/json_rpc
#   WD_LOG        monerod's log                                                           default $WD_DIR/monerod.log
#   WD_OUT_LOG    this watchdog's log                                                    default $WD_DIR/watchdog.log
#   WD_INTERVAL   seconds between checks (60); WD_STALL, seconds unwell before a restart (600)
#   WD_TEST=1     test mode: WD_RPC_URL, WD_LOG, WD_OUT_LOG and WD_RESTART (a command run instead of the restart) must
#                 all be given, none may name the real node or its directory, and the real restart cannot be reached
set -euo pipefail

WD_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WD_REPO="$(cd "$WD_HERE/../.." && pwd)"
# shellcheck source=pins.env
. "$WD_HERE/pins.env"
WD_REAL_DIR="$WD_REPO/.sim/stagenet"

WD_DIR="${WD_DIR:-$WD_REAL_DIR}"
WD_RPC_URL="${WD_RPC_URL:-http://127.0.0.1:$STAGENET_RPC_PORT/json_rpc}"
WD_LOG="${WD_LOG:-$WD_DIR/monerod.log}"
WD_OUT_LOG="${WD_OUT_LOG:-$WD_DIR/watchdog.log}"
WD_INTERVAL="${WD_INTERVAL:-60}"
WD_STALL="${WD_STALL:-600}"
WD_TEST="${WD_TEST:-0}"

# the watch's memory: since when the node has been unwell ("" when it is not), and the log line the last check saw
BAD_SINCE=""
LAST_SYNCED=""
STOPPING=0

die() {
	echo "stagenet-watchdog: $*" >&2
	exit 1
}

now_s() { if [ -n "${WD_NOW:-}" ]; then printf '%s\n' "$WD_NOW"; else date +%s; fi; }

wlog() { printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$WD_OUT_LOG"; }

# 30 s, as stagenet-up.sh allows: a node verifying heavy blocks can take more than 10 s to answer
info() { curl -sf --max-time 30 "$WD_RPC_URL" -H 'content-type: application/json' -d '{"jsonrpc":"2.0","id":"0","method":"get_info"}'; }

field() { printf '%s' "$1" | grep -o "\"$2\":[0-9]*" | head -n 1 | cut -d: -f2; }

# the last "Synced a/b" line of the log; the tail keeps the read small however large the log grows
last_synced() {
	[ -f "$WD_LOG" ] || return 0
	tail -c 1048576 "$WD_LOG" 2>/dev/null | grep -a -o 'Synced [0-9]*/[0-9]*' | tail -n 1 || true
}

# watch_step is one check. It restarts monerod once the node has been unwell for WD_STALL seconds without a break.
watch_step() {
	local now j out in synced unwell=""
	now="$(now_s)"
	synced="$(last_synced)"
	if j="$(info 2>/dev/null)"; then
		j="$(printf '%s' "$j" | tr -d ' \r\n')"
		if ! printf '%s' "$j" | grep -q '"nettype":"stagenet"'; then
			# not the node this watches: nothing of it is ours to restart
			[ "$BAD_SINCE" = foreign ] || wlog "the RPC answers, but not as a stagenet get_info: leaving it alone"
			BAD_SINCE=foreign
			LAST_SYNCED="$synced"
			return 0
		fi
		out="$(field "$j" outgoing_connections_count)"
		in="$(field "$j" incoming_connections_count)"
		if [ $((${out:-0} + ${in:-0})) -eq 0 ]; then
			unwell="0 peers (height $(field "$j" height), target_height $(field "$j" target_height))"
		fi
	elif [ -n "$synced" ] && [ "$synced" != "$LAST_SYNCED" ]; then
		: # no answer, and the log moved: verifying heavy blocks, which is work, not a fault
	else
		unwell="no answer, and the log's last line is still '${synced:-no Synced line}'"
	fi
	LAST_SYNCED="$synced"
	[ "$BAD_SINCE" = foreign ] && BAD_SINCE=""
	if [ -z "$unwell" ]; then
		[ -n "$BAD_SINCE" ] && wlog "healthy again after $((now - BAD_SINCE)) s unwell"
		BAD_SINCE=""
		return 0
	fi
	if [ -z "$BAD_SINCE" ]; then
		BAD_SINCE="$now"
		wlog "unwell: $unwell"
	fi
	if [ $((now - BAD_SINCE)) -ge "$WD_STALL" ]; then
		wlog "RESTART: unwell for $((now - BAD_SINCE)) s, last: $unwell"
		restart_monerod
		BAD_SINCE=""
		LAST_SYNCED=""
	fi
}

is_stagenet() { info 2>/dev/null | tr -d ' \r\n' | grep -q '"nettype":"stagenet"'; }

# restart_monerod stops the stagenet monerod as stagenet-down.sh does and starts it with stagenet-up.sh's launcher.
restart_monerod() {
	if [ "$WD_TEST" = 1 ]; then
		bash -c "$WD_RESTART" >>"$WD_OUT_LOG" 2>&1 || wlog "the test's restart command failed"
		return 0
	fi
	local launcher="$WD_DIR/run-monerod.sh" pid i
	if [ ! -f "$launcher" ]; then
		wlog "no $launcher (stagenet-up.sh writes it): not restarting, since nothing here makes up a command line"
		return 0
	fi
	if [ -f "$WD_DIR/monerod.pid" ]; then
		pid="$(tr -dc 0-9 <"$WD_DIR/monerod.pid")"
		# only the stagenet monerod that pid file names; a pid the system has since given to another process is left alone
		if [ -n "$pid" ] && tr '\0' ' ' <"/proc/$pid/cmdline" 2>/dev/null | grep -q -- '--stagenet'; then
			curl -sf --max-time 10 -X POST "${WD_RPC_URL%/json_rpc}/stop_daemon" >/dev/null 2>&1 || true
			for i in $(seq 1 60); do
				kill -0 "$pid" 2>/dev/null || break
				[ "$i" = 20 ] && kill -INT "$pid" 2>/dev/null
				sleep 1
			done
			if kill -0 "$pid" 2>/dev/null; then
				wlog "monerod (pid $pid) did not stop within 60 s; killing it"
				kill -KILL "$pid" 2>/dev/null || true
				sleep 1
			fi
		fi
		rm -f "$WD_DIR/monerod.pid"
	fi
	if [ "$STOPPING" = 1 ]; then
		wlog "stopping: monerod was stopped and is not started again"
		return 0
	fi
	setsid -f bash "$launcher" </dev/null >/dev/null 2>&1
	# stagenet-up.sh's own check: a stagenet daemon answers within 120 s
	for i in $(seq 1 120); do
		[ "$STOPPING" = 1 ] && return 0
		if is_stagenet; then
			wlog "monerod restarted, answering as stagenet (pid $(cat "$WD_DIR/monerod.pid" 2>/dev/null || echo ?))"
			return 0
		fi
		sleep 1
	done
	wlog "monerod restarted and not answering as stagenet within 120 s; the next checks decide"
}

# check_test_mode fails closed: a test names every endpoint and path itself, and none of them is the real node's.
check_test_mode() {
	[ -n "${WD_RESTART:-}" ] || die "test mode needs WD_RESTART, the command run instead of a restart"
	case "$WD_RPC_URL" in
	*":$STAGENET_RPC_PORT/"* | *":$STAGENET_RPC_PORT") die "test mode refuses $WD_RPC_URL: that is the real stagenet node's port" ;;
	esac
	local p
	for p in "$WD_LOG" "$WD_OUT_LOG"; do
		case "$p" in "$WD_REAL_DIR"/*) die "test mode refuses $p: that is the real node's directory" ;; esac
	done
}

run_loop() {
	if [ "$WD_TEST" = 1 ]; then
		check_test_mode
	else
		[ "$(uname -s)" = Linux ] || die "--run belongs inside WSL beside monerod (stagenet-up.sh starts it there)"
	fi
	[ "$WD_INTERVAL" -gt 0 ] && [ "$WD_STALL" -gt 0 ] || die "WD_INTERVAL and WD_STALL must be positive"
	local sleeper=""
	trap 'STOPPING=1' TERM INT
	wlog "watchdog started (pid $$): get_info every $WD_INTERVAL s at $WD_RPC_URL; a restart after $WD_STALL s unwell"
	while [ "$STOPPING" != 1 ]; do
		watch_step
		[ "$STOPPING" = 1 ] && break
		# the wait is a child, so a TERM ends it at once rather than after the interval
		sleep "$WD_INTERVAL" &
		sleeper=$!
		wait "$sleeper" 2>/dev/null || true
	done
	[ -n "$sleeper" ] && kill "$sleeper" 2>/dev/null
	wlog "watchdog stopped"
}

# --- the self-test: a fake node made of two files, and a clock the test moves ---

self_test() {
	local T n=0 pid i
	T="$(mktemp -d)"
	# shellcheck disable=SC2064
	trap "rm -rf '$T'" EXIT
	local path="$T/info.json"
	command -v cygpath >/dev/null 2>&1 && path="$(cygpath -m "$T")/info.json"
	WD_TEST=1
	WD_RPC_URL="file:///${path#/}"
	WD_LOG="$T/monerod.log"
	WD_OUT_LOG="$T/watchdog.log"
	WD_RESTART="echo restart >>'$T/restarts'; printf '%s' '{\"result\":{\"nettype\":\"stagenet\",\"height\":7,\"target_height\":9,\"outgoing_connections_count\":6,\"incoming_connections_count\":0}}' >'$T/info.json'"
	WD_STALL=600
	check_test_mode
	: >"$WD_LOG"

	answer() { printf '{"jsonrpc":"2.0","id":"0","result":{"nettype":"stagenet","height":%s,"target_height":%s,"outgoing_connections_count":%s,"incoming_connections_count":0,"status":"OK"}}' "$1" "$2" "$3" >"$T/info.json"; }
	silent() { rm -f "$T/info.json"; }
	synced() { printf '2026-09-12 12:00:00.000\t[P2P6]\tINFO\tglobal\tcryptonote_protocol_handler.inl:1615\tSynced %s/2205990 (71%%, 1 left)\n' "$1" >>"$WD_LOG"; }
	restarts() { if [ -f "$T/restarts" ]; then wc -l <"$T/restarts" | tr -d ' '; else echo 0; fi; }
	at() { WD_NOW="$1" watch_step; }
	want() {
		n=$((n + 1))
		[ "$(restarts)" = "$1" ] || die "self-test: $2: $(restarts) restarts, want $1 ($(tail -n 3 "$WD_OUT_LOG" | tr '\n' '|'))"
	}
	reset() { BAD_SINCE=""; LAST_SYNCED=""; rm -f "$T/restarts"; : >"$WD_LOG"; }

	# 1. The outage (X-25): healthy, then 0 peers and target_height 0 from t = 180. Nothing happens for 9 minutes; at 10
	#    minutes monerod is restarted once, the node comes back with peers, and no second restart follows.
	reset
	answer 1580000 2205990 7
	for t in 0 60 120; do at "$t"; done
	answer 1580000 0 0
	for t in 180 240 300 360 420 480 540 600 660 720; do at "$t"; done
	want 0 "9 minutes at 0 peers"
	at 780
	want 1 "10 minutes at 0 peers"
	grep -q 'RESTART: unwell for 600 s, last: 0 peers' "$WD_OUT_LOG" || die "self-test: the restart is not in the log"
	for t in $(seq 840 60 2400); do at "$t"; done
	want 1 "after the restart the node has peers"

	# 2. Heavy blocks: no answer for 30 minutes while the log gains a Synced line each minute. No restart.
	reset
	silent
	for t in $(seq 0 60 1800); do
		synced $((1580000 + t))
		at "$t"
	done
	want 0 "no answer while the log moves"

	# 3. Hung: no answer and a log that stands still. The first check has no earlier line to compare, so the stall is
	#    counted from the second (t = 60), and the restart comes 10 minutes after it, not before.
	reset
	silent
	synced 1590000
	for t in $(seq 0 60 600); do at "$t"; done
	want 0 "9 minutes of no answer with a stalled log"
	at 660
	want 1 "10 minutes of no answer with a stalled log"

	# 4. Unwell without a break is what counts: 9 minutes at 0 peers, one healthy check, 9 more minutes, no restart; and
	#    9 minutes of silence with a stalled log ended by one line of progress, then 9 more, no restart.
	reset
	answer 1590000 0 0
	for t in $(seq 0 60 540); do at "$t"; done
	answer 1590000 2205990 3
	at 600
	answer 1590000 0 0
	for t in $(seq 660 60 1200); do at "$t"; done
	want 0 "0 peers broken by one healthy check"
	reset
	silent
	synced 1600000
	for t in $(seq 0 60 540); do at "$t"; done
	synced 1600020
	at 600
	for t in $(seq 660 60 1200); do at "$t"; done
	want 0 "a stall broken by one line of progress"

	# 5. An answer that is not a stagenet get_info is not this node's: no restart however long it lasts.
	reset
	printf '{"result":{"nettype":"mainnet","height":1,"outgoing_connections_count":0}}' >"$T/info.json"
	for t in $(seq 0 60 1800); do at "$t"; done
	want 0 "a node that is not stagenet"

	# 6. The loop itself, in test mode, at a 1 s interval and a 3 s stall: 0 peers, one restart, and TERM ends it.
	reset
	answer 1600000 0 0
	WD_TEST=1 WD_RPC_URL="$WD_RPC_URL" WD_LOG="$WD_LOG" WD_OUT_LOG="$T/loop.log" WD_INTERVAL=1 WD_STALL=3 \
		WD_RESTART="echo restart >>'$T/restarts'; rm -f '$T/info.json'; printf '2026 Synced 1/2\n' >>'$WD_LOG'" \
		bash "${BASH_SOURCE[0]}" --run &
	pid=$!
	for i in $(seq 1 30); do
		[ "$(restarts)" = 0 ] || break
		sleep 1
	done
	kill -TERM "$pid" 2>/dev/null || true
	wait "$pid" 2>/dev/null || true
	want 1 "the loop at a 3 s stall"
	grep -q 'watchdog stopped' "$T/loop.log" || die "self-test: the loop did not log its stop on TERM"

	# 7. Test mode refuses the real node's port before it asks anything.
	if WD_TEST=1 WD_RPC_URL="http://127.0.0.1:$STAGENET_RPC_PORT/json_rpc" WD_LOG="$WD_LOG" WD_OUT_LOG="$T/refuse.log" \
		WD_RESTART=true bash "${BASH_SOURCE[0]}" --run 2>/dev/null; then
		die "self-test: test mode ran against the real node's port"
	fi
	n=$((n + 1))

	echo "stagenet-watchdog: self-test passed ($n checks; no node was contacted)"
}

case "${1:-}" in
--run) run_loop ;;
--self-test) self_test ;;
*)
	echo "usage: $0 --run | --self-test" >&2
	exit 2
	;;
esac
