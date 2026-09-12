#!/usr/bin/env bash
# Bring up a PRUNED monerod on Monero STAGENET, the public test network (never mainnet), inside WSL2, and sync it.
#
#   tools/regtest/stagenet-up.sh      start it (or, when it is running, report its sync progress and an estimate)
#
# monerod 0.18.5.1 (the same pinned, verified binary as the regtest harness) with --stagenet --prune-blockchain
# --sync-pruned-blocks. Outbound P2P only: P2P is bound to 127.0.0.1 and --in-peers 0, so no peer can connect in;
# UPnP off; no update check and no DNS checkpoints, so stagenet peers are the only outside contact.
# RPC on 127.0.0.1:$STAGENET_RPC_PORT, unrestricted, loopback only. No wallet-rpc. The script refuses to leave
# running any daemon whose get_info does not say stagenet.
#
# The chain lives on WSL's own ext4 disk ($HOME/.dmz-regtest/stagenet), as the Zcash testnet state does; logs and
# progress stay in .sim/stagenet (git-ignored). It used to live in .sim/stagenet/chain, on the /mnt/c 9p mount, and on
# 2026-09-12 that ran at about 0.6 blocks/s with monerod at 19% of one core and 10,119 downloaded blocks queued to be
# added (sync_info): LMDB memory-maps its data file, and every page it touches over 9p is a round trip. The first start
# after the move copies the old chain across once, checks the copy's size, and keeps the old one aside as chain.old-9p.
# Measured after the move, the same day: 3,216 blocks in 302 s from height 579,916, 10.6 blocks/s with 7 peers.
#
# Size, measured 2026-09-11 from six public stagenet nodes' get_info: height 2,205,297, database_size reported as
# 16,106,127,360 bytes (15 GiB), a figure restricted RPC rounds UP to a multiple of 5 GiB (core_rpc_server.cpp): a
# stagenet database is at most 15 GiB unpruned, and pruning keeps about a third of it.
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

need_wsl
SDIR="$REPO/.sim/stagenet"
SW="$(to_wsl "$SDIR")"
TARBALL="$VENDOR/dl/$MONERO_TARBALL"
WHOME="$(wsl_exec printenv HOME | tr -d '\r')"
MBIN="$WHOME/.dmz-regtest/monero-v$MONERO_VERSION"
WDATA="$WHOME/.dmz-regtest/stagenet"

bin_hashes() { wsl_exec sha256sum "$MBIN/monerod" 2>/dev/null | tr -d '\r' | awk '{print $1}'; }
if [ "$(bin_hashes || true)" != "$MONEROD_SHA256" ]; then
	check_sha256 "$TARBALL" "$MONERO_TARBALL_SHA256" "the Monero $MONERO_VERSION release tarball"
	say "extracting Monero $MONERO_VERSION into WSL at $MBIN"
	wsl_exec bash -c "rm -rf '$MBIN' && mkdir -p '$MBIN' && tar -xjf '$(to_wsl "$TARBALL")' -C '$MBIN' --strip-components=1"
	[ "$(bin_hashes || true)" = "$MONEROD_SHA256" ] || die "monerod in $MBIN does not match pins.env after extraction"
fi

# 30 s: while monerod verifies heavy stagenet blocks its RPC can take more than 10 s to answer (measured 2026-09-12
# near block 577,000), and a status that gave up then reported a running, syncing node as not answering
info() { curl -sf --max-time 30 "http://127.0.0.1:$STAGENET_RPC_PORT/json_rpc" -H 'content-type: application/json' -d '{"jsonrpc":"2.0","id":"0","method":"get_info"}'; }
is_stagenet() { info | tr -d ' \n' | grep -q '"nettype":"stagenet"'; }

# progress: height / target, the database's size on disk, and (from two readings in $SDIR/progress.tsv) a rate and
# an estimated finish. Every call appends one reading.
progress() {
	local j h t p db now
	j="$(info | tr -d ' \n')" || { say "no answer from 127.0.0.1:$STAGENET_RPC_PORT"; return 1; }
	h="$(echo "$j" | grep -o '"height":[0-9]*' | head -1 | cut -d: -f2)"
	t="$(echo "$j" | grep -o '"target_height":[0-9]*' | cut -d: -f2)"
	p="$(echo "$j" | grep -o '"outgoing_connections_count":[0-9]*' | cut -d: -f2)"
	db="$(wsl_exec du -sb "$WDATA" 2>/dev/null | tr -d '\r' | cut -f1)"
	now="$(date +%s)"
	echo -e "$now\t$h\t$t\t${db:-0}" >>"$SDIR/progress.tsv"
	# the target is the largest of monerod's target_height, the height, and STAGENET_TIP_FLOOR: with no peers
	# monerod reports a target of 0, which alone would read as a finished sync
	# the rate is over the last five readings (the sync slows as blocks fill up, so the average since the start
	# would promise too early a finish)
	awk -v n="$now" -v h="$h" -v t="$t" -v f="$STAGENET_TIP_FLOOR" -v p="${p:-?}" -v db="${db:-0}" '{T[NR]=$1; H[NR]=$2}
		END {
			k = (NR > 5) ? NR - 5 : 1
			t0 = T[k]; h0 = H[k]
			tt = (t > h) ? t : h
			if (f > tt) tt = f
			printf "regtest: stagenet height %d of %d (%.1f%%), chain on disk %.2f GB, %s peers", h, tt, (tt ? 100*h/tt : 0), db/1e9, p
			if (n > t0 && h > h0) {
				r = (h - h0) / (n - t0)
				printf ", %.1f blocks/s over the last %.0f min", r, (n - t0) / 60
				if (tt > h) printf ", about %.1f h to go at that rate", (tt - h) / r / 3600
			}
			printf "\n"
		}' "$SDIR/progress.tsv"
}

# start_watchdog: the watchdog (X-25, stagenet-watchdog.sh) runs detached inside WSL beside monerod, as monerod itself
# does. It restarts monerod after 10 minutes at 0 peers, or of no answer while its log stands still, reusing the chain
# and never wiping it. One runs at a time; DMZ_STAGENET_WATCHDOG=0 starts none. stagenet-down.sh stops it first.
start_watchdog() {
	local pid
	if [ "${DMZ_STAGENET_WATCHDOG:-1}" = 0 ]; then
		say "no watchdog (DMZ_STAGENET_WATCHDOG=0)"
		return 0
	fi
	pid="$(stagenet_watchdog_pid "$SDIR")"
	if [ -n "$pid" ]; then
		say "watchdog running (pid $pid in WSL); its log is $SDIR/watchdog.log"
		return 0
	fi
	cat >"$SDIR/run-watchdog.sh" <<EOF
#!/usr/bin/env bash
# generated by tools/regtest/stagenet-up.sh
echo \$\$ > "$SW/watchdog.pid"
exec env WD_DIR="$SW" bash "$(to_wsl "$REGTEST_HERE")/stagenet-watchdog.sh" --run < /dev/null >> "$SW/watchdog.out" 2>&1
EOF
	start_detached stagenet-watchdog "$SW/run-watchdog.sh"
	say "watchdog started: it restarts monerod after 10 min at 0 peers, or of no answer with its log standing still; log $SDIR/watchdog.log"
}

if port_listening "$STAGENET_RPC_PORT"; then
	# a running monerod gets its watchdog whether or not its RPC answers just now: it stops answering while it verifies
	# heavy blocks
	stagenet_monerod_alive "$SDIR" && start_watchdog
	is_stagenet || die "port $STAGENET_RPC_PORT is taken by something that is not a stagenet monerod"
	progress
	exit 0
fi
# fail closed: a watchdog that is running restarts monerod itself, and a second start here would race it for the ports
wd="$(stagenet_watchdog_pid "$SDIR")"
[ -z "$wd" ] || die "monerod is not answering on $STAGENET_RPC_PORT and its watchdog (pid $wd in WSL) is running, which restarts it; to start it by hand, stop both with stagenet-down.sh first"
port_listening "$STAGENET_P2P_PORT" && die "P2P port $STAGENET_P2P_PORT is taken"

mkdir -p "$SDIR"
wsl_exec mkdir -p "$WDATA"
# One move of the chain off the 9p mount, only while monerod is stopped (this is the start path). The copy must match
# the original byte for byte in size before the original is set aside; a copy that does not is removed, and the start
# fails rather than sync from a half-copied database.
if wsl_exec test -f "$SW/chain/stagenet/lmdb/data.mdb" && ! wsl_exec test -f "$WDATA/stagenet/lmdb/data.mdb"; then
	say "moving the stagenet chain from $SDIR/chain to WSL ext4 at $WDATA (once; this copies about $(du -sh "$SDIR/chain" 2>/dev/null | cut -f1))"
	wsl_exec cp -a "$SW/chain/." "$WDATA/"
	a="$(wsl_exec du -sb --apparent-size "$SW/chain" | tr -d '\r' | cut -f1)"
	b="$(wsl_exec du -sb --apparent-size "$WDATA" | tr -d '\r' | cut -f1)"
	if [ -z "$a" ] || [ "$a" != "$b" ]; then
		wsl_exec rm -rf "$WDATA/stagenet"
		die "the copy of the stagenet chain is $b bytes against $a: removed it; the chain is still at $SDIR/chain"
	fi
	mv "$SDIR/chain" "$SDIR/chain.old-9p"
	say "copied $a bytes; the old chain is kept at $SDIR/chain.old-9p until you delete it"
fi
cat >"$SDIR/run-monerod.sh" <<EOF
#!/usr/bin/env bash
# generated by tools/regtest/stagenet-up.sh
echo \$\$ > "$SW/monerod.pid"
exec "$MBIN/monerod" --stagenet --prune-blockchain --sync-pruned-blocks \\
	--data-dir "$WDATA" --log-file "$SW/monerod.log" --log-level 0 \\
	--rpc-bind-ip 127.0.0.1 --rpc-bind-port $STAGENET_RPC_PORT \\
	--p2p-bind-ip 127.0.0.1 --p2p-bind-port $STAGENET_P2P_PORT --in-peers 0 --hide-my-port --no-igd \\
	--check-updates disabled --disable-dns-checkpoints \\
	--no-zmq --non-interactive \\
	< /dev/null >> "$SW/monerod.out" 2>&1
EOF
say "starting monerod $MONERO_VERSION on STAGENET (pruned, outbound only) in WSL, chain at $WDATA, log $SDIR/monerod.log"
start_detached stagenet-monerod "$SW/run-monerod.sh"
if ! wait_for 120 "stagenet monerod RPC" is_stagenet; then
	tail -n 20 "$SDIR/monerod.out" "$SDIR/monerod.log" >&2 || true
	# fail closed: whatever answered (or did not) is stopped, never left syncing some other network
	bash "$REGTEST_HERE/stagenet-down.sh" >/dev/null 2>&1 || true
	die "monerod did not answer as a STAGENET daemon on 127.0.0.1:$STAGENET_RPC_PORT within 120 s; stopped it"
fi
rm -f "$SDIR/progress.tsv"
say "stagenet monerod up: RPC http://127.0.0.1:$STAGENET_RPC_PORT (loopback, no wallet-rpc); syncing"
progress
