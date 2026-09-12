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
# monerod can exit on its own at any moment of a restart, between any two of its lines, so no signal or check in the
# restart fails because the process has already gone: under set -e one failing kill would end the watchdog mid-restart,
# with monerod stopped and nothing left to start it. The restart always either starts monerod again or logs why not.
#
# A restarted monerod runs in the watchdog's own WSL session, whose wsl.exe keeps the instance up; its pid is in
# monerod.pid as before (run-monerod.sh writes it), so stagenet-down.sh stops it the same way. The wsl.exe that hosted
# the monerod it stopped exits with it, so the watchdog rewrites .sim/regtest/stagenet-monerod.winpid (common.sh) to
# name its own wsl.exe host, copied from stagenet-watchdog.winpid, or removes it when that file is not there: a stale
# Windows pid there could by now be any process's.
#
# Settings, from the environment (the defaults are the real node's):
#   WD_DIR        the stagenet directory as WSL sees it (run-monerod.sh, monerod.pid)   default <repo>/.sim/stagenet
#   WD_WINPID_DIR where common.sh's start_detached keeps the .winpid files                default <repo>/.sim/regtest
#   WD_RPC_URL    get_info's URL                                     default http://127.0.0.1:$STAGENET_RPC_PORT/json_rpc
#   WD_LOG        monerod's log                                                           default $WD_DIR/monerod.log
#   WD_OUT_LOG    this watchdog's log                                                    default $WD_DIR/watchdog.log
#   WD_INTERVAL   seconds between checks (60); WD_STALL, seconds unwell before a restart (600)
#   WD_TEST=1     test mode: WD_RPC_URL, WD_LOG, WD_OUT_LOG, WD_DIR, WD_WINPID_DIR and WD_RESTART (a command run instead
#                 of the restart) must all be given, none may name the real node or its directories, and the real
#                 restart cannot be reached from the loop
set -euo pipefail

WD_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WD_REPO="$(cd "$WD_HERE/../.." && pwd)"
# shellcheck source=pins.env
. "$WD_HERE/pins.env"
WD_REAL_DIR="$WD_REPO/.sim/stagenet"
WD_REAL_WINPID_DIR="$WD_REPO/.sim/regtest"

WD_DIR="${WD_DIR:-$WD_REAL_DIR}"
WD_WINPID_DIR="${WD_WINPID_DIR:-$WD_REAL_WINPID_DIR}"
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
STOP_LEFT=0 # stop_pid's result: 1 when the monerod it stopped is still there after SIGKILL

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

# ours <pid>: true while <pid> is a process started with --stagenet. A pid the system has since given to another process
# is not ours, and neither is one that has exited; nothing is signalled that is not ours at the moment of the check.
ours() { tr '\0' ' ' 2>/dev/null <"/proc/$1/cmdline" | grep -q -- '--stagenet'; }

# restart_monerod stops the stagenet monerod as stagenet-down.sh does and starts it with stagenet-up.sh's launcher.
restart_monerod() {
	if [ "$WD_TEST" = 1 ]; then
		bash -c "$WD_RESTART" >>"$WD_OUT_LOG" 2>&1 || wlog "the test's restart command failed"
		return 0
	fi
	real_restart
}

# stop_pid <pid>: monerod's own clean shutdown (stop_daemon), SIGINT at 20 s, SIGKILL at 60 s; LMDB is flushed by the
# first two. It sets STOP_LEFT to 0 once the process has gone, and to 1 when it is still there 10 s after SIGKILL.
# monerod may exit between any check and the signal that follows it, and a signal it can no longer receive is not an
# error here. It is called as a plain command, never as a condition (which would switch set -e off inside it), so the
# self-test's check 8 sees any step here that would fail on a process already gone.
stop_pid() {
	local pid="$1" i
	STOP_LEFT=0
	curl -sf --max-time 10 -X POST "${WD_RPC_URL%/json_rpc}/stop_daemon" >/dev/null 2>&1 || true
	for i in $(seq 1 60); do
		ours "$pid" || return 0
		if [ "$i" = 20 ]; then
			kill -INT "$pid" 2>/dev/null || true
		fi
		sleep 1
	done
	ours "$pid" || return 0
	wlog "monerod (pid $pid) did not stop within 60 s; killing it"
	kill -KILL "$pid" 2>/dev/null || true
	for i in $(seq 1 10); do
		ours "$pid" || return 0
		sleep 1
	done
	STOP_LEFT=1
}

# real_restart is the restart itself. Every step tolerates monerod having gone already, and every way it ends without
# a monerod answering is logged, with the reason.
real_restart() {
	local launcher="$WD_DIR/run-monerod.sh" pid="" i
	if [ ! -f "$launcher" ]; then
		wlog "no $launcher (stagenet-up.sh writes it): not restarting, since nothing here makes up a command line"
		return 0
	fi
	pid="$(tr -dc 0-9 2>/dev/null <"$WD_DIR/monerod.pid")" || pid=""
	# only the stagenet monerod that pid file names; a pid the system has since given to another process is left alone
	if [ -n "$pid" ] && ours "$pid"; then
		stop_pid "$pid"
		if [ "$STOP_LEFT" = 1 ]; then
			wlog "monerod (pid $pid) is still there 10 s after SIGKILL: not starting a second one beside it; the next checks decide"
			return 0
		fi
	fi
	rm -f "$WD_DIR/monerod.pid" || true
	if [ "$STOPPING" = 1 ]; then
		drop_winpid "the monerod it hosted was stopped and the watchdog is stopping"
		wlog "stopping: monerod was stopped and is not started again"
		return 0
	fi
	if ! setsid -f bash "$launcher" </dev/null >/dev/null 2>&1; then
		drop_winpid "the monerod it hosted was stopped"
		wlog "monerod was stopped, and starting $launcher failed; the next checks decide"
		return 0
	fi
	refresh_winpid
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

# refresh_winpid: stagenet-monerod.winpid (common.sh start_detached) named the wsl.exe that hosted the monerod just
# stopped, which exits with it; Windows may then give that pid to any process. The new monerod runs in this
# watchdog's WSL session, so the file now names this watchdog's host, with the launcher line that lets stop_winpid
# prove it: a copy of stagenet-watchdog.winpid. Without a usable copy, the file is removed.
refresh_winpid() {
	local src="$WD_WINPID_DIR/stagenet-watchdog.winpid" dst="$WD_WINPID_DIR/stagenet-monerod.winpid"
	if [ -n "$(sed -n 2p "$src" 2>/dev/null || true)" ] && cp "$src" "$dst.new" 2>/dev/null && mv -f "$dst.new" "$dst" 2>/dev/null; then
		wlog "stagenet-monerod.winpid now names this watchdog's wsl.exe host (pid $(head -n 1 "$dst" 2>/dev/null || echo ?)), which keeps the new monerod up"
		return 0
	fi
	rm -f "$dst.new" 2>/dev/null || true
	drop_winpid "no stagenet-watchdog.winpid with a launcher names the host of the new one"
}

# drop_winpid <why>: removes stagenet-monerod.winpid, whose wsl.exe exited with the monerod it hosted
drop_winpid() {
	local dst="$WD_WINPID_DIR/stagenet-monerod.winpid"
	[ -f "$dst" ] || return 0
	if rm -f "$dst" 2>/dev/null; then
		wlog "stagenet-monerod.winpid removed: $1"
	else
		wlog "could not remove $dst ($1); stagenet-down.sh checks the pid's command line before it stops anything"
	fi
}

# check_test_mode fails closed: a test names every endpoint and path itself, and none of them is the real node's.
check_test_mode() {
	[ -n "${WD_RESTART:-}" ] || die "test mode needs WD_RESTART, the command run instead of a restart"
	case "$WD_RPC_URL" in
	*":$STAGENET_RPC_PORT/"* | *":$STAGENET_RPC_PORT") die "test mode refuses $WD_RPC_URL: that is the real stagenet node's port" ;;
	esac
	local p
	for p in "$WD_LOG" "$WD_OUT_LOG" "$WD_DIR" "$WD_WINPID_DIR"; do
		case "$p" in
		"$WD_REAL_DIR" | "$WD_REAL_DIR"/*) die "test mode refuses $p: that is the real node's directory" ;;
		"$WD_REAL_WINPID_DIR" | "$WD_REAL_WINPID_DIR"/*) die "test mode refuses $p: that is where the real .winpid files are" ;;
		esac
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
	# the sleep may have ended already, and a kill it can no longer receive must not skip the log line below
	[ -z "$sleeper" ] || kill "$sleeper" 2>/dev/null || true
	wlog "watchdog stopped"
}

# --- the self-test: a fake node made of two files, and a clock the test moves ---

# restart_child is the body of the child bash that check 8 runs: it loads this script's functions (with its set -euo
# pipefail, and without the dispatch at the end), replaces every command that could reach a process or a node — kill,
# sleep, curl, setsid, and ours, the /proc check — by a fake that only writes files in $RS, and runs real_restart.
# The fake process lives while $RS/alive exists; SC picks the moment at which it exits by itself.
# shellcheck disable=SC2016
restart_child='
. "$1"
case "$-" in *e*) ;; *) echo "not under set -e" >&2; exit 97 ;; esac
ours() { [ -f "$RS/alive" ]; }
kill() {
	echo "kill $*" >>"$RS/signals"
	case "$SC:$1" in
	gone-before-int:-INT | gone-before-kill:-KILL) rm -f "$RS/alive"; return 1 ;; # it exited just before: no such process
	gone-before-kill:-INT | never-dies:*) return 0 ;;                               # received, and ignored
	*:-INT | *:-KILL) rm -f "$RS/alive"; return 0 ;;                                # received: it exits, as monerod does
	esac
	return 0
}
sleep() { :; }
curl() {
	case "$*" in
	*get_info*) if [ -f "$RS/started" ]; then printf "%s" "{\"result\":{\"nettype\":\"stagenet\"}}"; else return 7; fi ;;
	*) return 7 ;; # stop_daemon: no answer, so the signals have to do it
	esac
}
setsid() { : >"$RS/started"; echo "start $*" >>"$RS/signals"; }
check_test_mode
real_restart
: >"$RS/finished"
'

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
	WD_DIR="$T/stagenet"
	WD_WINPID_DIR="$T/winpids"
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
	WD_TEST=1 WD_RPC_URL="$WD_RPC_URL" WD_LOG="$WD_LOG" WD_OUT_LOG="$T/loop.log" WD_DIR="$WD_DIR" WD_WINPID_DIR="$WD_WINPID_DIR" \
		WD_INTERVAL=1 WD_STALL=3 \
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

	# 7. Test mode refuses the real node's port before it asks anything, and the real node's directories.
	local refusal
	refusal="$(WD_TEST=1 WD_RPC_URL="http://127.0.0.1:$STAGENET_RPC_PORT/json_rpc" WD_LOG="$WD_LOG" WD_OUT_LOG="$T/refuse.log" \
		WD_DIR="$WD_DIR" WD_WINPID_DIR="$WD_WINPID_DIR" WD_RESTART=true bash "${BASH_SOURCE[0]}" --run 2>&1)" &&
		die "self-test: test mode ran against the real node's port"
	case "$refusal" in *"real stagenet node's port"*) ;; *) die "self-test: the port was refused for another reason: $refusal" ;; esac
	n=$((n + 1))
	refusal="$(WD_TEST=1 WD_RPC_URL="$WD_RPC_URL" WD_LOG="$WD_LOG" WD_OUT_LOG="$T/refuse.log" WD_WINPID_DIR="$WD_WINPID_DIR" \
		WD_RESTART=true bash "${BASH_SOURCE[0]}" --run 2>&1)" && die "self-test: test mode ran with the real stagenet directory"
	case "$refusal" in *"real node's directory"*) ;; *) die "self-test: the directory was refused for another reason: $refusal" ;; esac
	n=$((n + 1))
	refusal="$(WD_TEST=1 WD_RPC_URL="$WD_RPC_URL" WD_LOG="$WD_LOG" WD_OUT_LOG="$T/refuse.log" WD_DIR="$WD_DIR" \
		WD_RESTART=true bash "${BASH_SOURCE[0]}" --run 2>&1)" && die "self-test: test mode ran with the real .winpid directory"
	case "$refusal" in *"real .winpid files"*) ;; *) die "self-test: the .winpid directory was refused for another reason: $refusal" ;; esac
	n=$((n + 1))

	# 8. The restart itself (the 2026-09-12 review): real_restart under set -e, in a child bash whose every command that
	#    could reach a process or a node is a fake (restart_child), so nothing is signalled, started or asked. monerod
	#    exits by itself at the worst moments — after the check and before the SIGINT, after the last check and before
	#    the SIGKILL — and each time the restart finishes, starts monerod once, and says so. A monerod that outlives
	#    SIGKILL is not joined by a second one, and that is said; one whose pid file names another process is not
	#    signalled. After a start, stagenet-monerod.winpid names the watchdog's host, or is gone if nothing names it.
	local rs="$T/rs" rc
	restart_case() {
		rm -rf "$rs"
		mkdir -p "$rs/stagenet" "$rs/winpids"
		printf 'exit 1\n' >"$rs/stagenet/run-monerod.sh"
		echo 4242 >"$rs/stagenet/monerod.pid"
		: >"$rs/alive"
		: >"$rs/signals"
		printf '%s\n%s\n' 777 /mnt/x/.sim/stagenet/run-watchdog.sh >"$rs/winpids/stagenet-watchdog.winpid"
		printf '%s\n%s\n' 555 /mnt/x/.sim/stagenet/run-monerod.sh >"$rs/winpids/stagenet-monerod.winpid"
		case "$1" in
		not-ours) rm -f "$rs/alive" ;;
		no-watchdog-winpid) rm -f "$rs/winpids/stagenet-watchdog.winpid" ;;
		esac
		rc=0
		WD_TEST=1 WD_RESTART=true WD_RPC_URL="$WD_RPC_URL" WD_LOG="$WD_LOG" WD_OUT_LOG="$rs/out.log" \
			WD_DIR="$rs/stagenet" WD_WINPID_DIR="$rs/winpids" RS="$rs" SC="$1" \
			bash -c "$restart_child" restart-case "${BASH_SOURCE[0]}" 2>"$rs/stderr" || rc=$?
		n=$((n + 1))
		[ "$rc" = 0 ] && [ -f "$rs/finished" ] ||
			die "self-test: restart case $1 ended mid-restart (exit $rc): $(tr '\n' '|' <"$rs/stderr") log: $(tr '\n' '|' 2>/dev/null <"$rs/out.log")"
	}
	started() { grep -c '^start ' "$rs/signals" || true; }
	logged() { grep -q -- "$2" "$rs/out.log" 2>/dev/null || die "self-test: restart case $1: no '$2' in the log: $(tr '\n' '|' 2>/dev/null <"$rs/out.log")"; }
	winpid_is_watchdogs() { cmp -s "$rs/winpids/stagenet-monerod.winpid" "$rs/winpids/stagenet-watchdog.winpid" || die "self-test: restart case $1: stagenet-monerod.winpid does not name the watchdog's host"; }

	restart_case gone-before-int
	[ "$(started)" = 1 ] || die "self-test: gone-before-int: monerod was started $(started) times, want 1"
	grep -q '^kill -INT 4242$' "$rs/signals" || die "self-test: gone-before-int: the SIGINT was never tried"
	! grep -q '^kill -KILL' "$rs/signals" || die "self-test: gone-before-int: SIGKILL sent to a process that had gone"
	logged gone-before-int 'monerod restarted, answering as stagenet'
	winpid_is_watchdogs gone-before-int

	restart_case gone-before-kill
	[ "$(started)" = 1 ] || die "self-test: gone-before-kill: monerod was started $(started) times, want 1"
	logged gone-before-kill 'did not stop within 60 s; killing it'
	logged gone-before-kill 'monerod restarted, answering as stagenet'
	winpid_is_watchdogs gone-before-kill

	restart_case never-dies
	[ "$(started)" = 0 ] || die "self-test: never-dies: a second monerod was started beside one that outlived SIGKILL"
	logged never-dies 'still there 10 s after SIGKILL: not starting a second one'

	restart_case not-ours
	[ "$(started)" = 1 ] || die "self-test: not-ours: monerod was started $(started) times, want 1"
	! grep -q '^kill' "$rs/signals" || die "self-test: not-ours: a process that is not the stagenet monerod was signalled"
	logged not-ours 'monerod restarted, answering as stagenet'

	restart_case no-watchdog-winpid
	[ "$(started)" = 1 ] || die "self-test: no-watchdog-winpid: monerod was started $(started) times, want 1"
	[ ! -e "$rs/winpids/stagenet-monerod.winpid" ] || die "self-test: no-watchdog-winpid: the stale stagenet-monerod.winpid was kept"
	logged no-watchdog-winpid 'stagenet-monerod.winpid removed'

	echo "stagenet-watchdog: self-test passed ($n checks; no node was contacted)"
}

# sourced (the self-test's check 8 loads the functions this way): define, and dispatch nothing
[ "${BASH_SOURCE[0]}" = "$0" ] || return 0

case "${1:-}" in
--run) run_loop ;;
--self-test) self_test ;;
*)
	echo "usage: $0 --run | --self-test" >&2
	exit 2
	;;
esac
