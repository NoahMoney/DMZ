#!/usr/bin/env bash
# Prove the Zcash leg's node operations against the harness's real regtest zebrad: fund a throwaway key from a
# coinbase, spend it into a v5 transaction under the current branch id (paying a key and a P2SH), read everything
# back through the six RPC methods tools/zecswap uses, and record their shapes.
#
#   tools/regtest/smoke-zcash.sh      (run tools/regtest/zcash-up.sh first)
#
# Keys: .sim/regtest/zcash/keys (git-ignored, throwaway). Transcript: research/regtest/zcash-smoke.log (tracked;
# no private key is ever written to it). zfund, the Rust key/transaction tool, is built on first use into
# vendor/regtest/bin/zfund (cargo in WSL, Cargo.lock pinned).
. "$(dirname "${BASH_SOURCE[0]}")/common.sh"

need_wsl
command -v go >/dev/null 2>&1 || die "go is not on PATH"
ZDIR="$DATA/zcash"
[ -f "$ZDIR/.cookie" ] || die "no zebrad cookie in $ZDIR — run tools/regtest/zcash-up.sh"
curl -sf --max-time 5 -u "$(cat "$ZDIR/.cookie")" -H 'content-type: application/json' \
	-d '{"jsonrpc":"1.0","id":"s","method":"getblockhash","params":[0]}' "http://127.0.0.1:$ZCASH_RPC_PORT/" |
	grep -q '029f11d80ef9765602235e1bc9727e3eb6ba20839319f761fee920d63401e327' ||
	die "no regtest zebrad on 127.0.0.1:$ZCASH_RPC_PORT — run tools/regtest/zcash-up.sh"

SRC="$REGTEST_HERE/zfund"
ZFUND="$VENDOR/bin/zfund"
if [ ! -f "$ZFUND" ] || [ -n "$(find "$SRC/src" "$SRC/Cargo.toml" "$SRC/Cargo.lock" -newer "$ZFUND" 2>/dev/null)" ]; then
	say "building zfund (cargo build --release --locked, in WSL)"
	wsl_exec bash -lc "cd '$(to_wsl "$SRC")' && CARGO_TARGET_DIR=\$HOME/.dmz-regtest/target-zfund cargo build --release --locked" ||
		die "zfund did not build (cargo in WSL $WSL_DISTRO)"
	mkdir -p "$VENDOR/bin"
	wsl_exec bash -c "cp \$HOME/.dmz-regtest/target-zfund/release/zfund '$(to_wsl "$ZFUND")' && chmod +x '$(to_wsl "$ZFUND")'"
fi

mkdir -p "$ZDIR/keys" "$REPO/research/regtest"
LOG="$REPO/research/regtest/zcash-smoke.log"
SHAPES="$ZDIR/real-shapes.json"
cd "$REPO/tools"
status=0
# -repo: the transcript writes the repository root as <repo> (no machine-specific path in a tracked file)
go run ./regtest/zecsmoke -rpc "http://127.0.0.1:$ZCASH_RPC_PORT/" -cookie "$(cygpath -m "$ZDIR/.cookie")" \
	-distro "$WSL_DISTRO" -zfund "$(to_wsl "$ZFUND")" -keydir "$(cygpath -m "$ZDIR/keys")" -keydir-wsl "$(to_wsl "$ZDIR/keys")" \
	-repo "$(cygpath -m "$REPO")" -log "$(cygpath -m "$LOG")" -shapes "$(cygpath -m "$SHAPES")" || status=$?
if [ -f "$SHAPES" ]; then
	{
		echo
		echo "== tools/zecswap's client and ZIP 244 code against this node (go run ./regtest/zecshape)"
		go run ./regtest/zecshape -rpc "http://127.0.0.1:$ZCASH_RPC_PORT/" -cookie "$(cygpath -m "$ZDIR/.cookie")" \
			-refs "$(cygpath -m "$SHAPES")" 2>&1 || echo "zecshape did not run cleanly (tools/zecswap may be mid-edit): exit $?"
	} | scrub | tee -a "$LOG"
fi
exit $status
