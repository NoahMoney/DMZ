#!/bin/bash
# Re-records run.log from a release run of the CLI (docs/XMR_SWAP_BUILD.md §3.B): the generator, prove and
# verify timings, the proof size, and every exit path. Writes vector.txt (secret, H, S, proof — the worked vector
# README.md annotates and tests/vector.rs verifies) only when it does not exist yet, so a re-recording of the
# timings does not silently change the vector the README explains. Run from WSL; from Git Bash:
#   MSYS_NO_PATHCONV=1 wsl.exe -e bash -lc 'export REPO=/mnt/c/Users/Wilma/Desktop/fomo; bash $REPO/research/crosschain/poc/proof-xmr/record.sh'
set -euo pipefail
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
poc="$REPO/research/crosschain/poc"
here="$poc/proof-xmr"
"$poc/wsl/build.sh" proof-xmr build --release >/dev/null
bin="$HOME/target/proof-xmr/release/proof-xmr"
export PROOF_XMR_TIMING=1
get() { sed -E "s/.*\"$1\":\"([0-9a-f]*)\".*/\1/" <<<"$2"; }
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

{
    echo "proof-xmr CLI, release build, $(date -u +%Y-%m-%dT%H:%M:%SZ), $(uname -m), $(nproc) cpus, $(rustc --version)"
    echo
    echo '$ proof-xmr keygen'
    kg="$("$bin" keygen)"
    echo "$kg"
    secret="$(get secret "$kg")"; h="$(get h "$kg")"; s="$(get s "$kg")"

    echo
    echo '$ proof-xmr prove <secret>'
    pv="$("$bin" prove "$secret" 2>"$tmp/t")"
    cat "$tmp/t"
    proof="$(get proof "$pv")"
    echo "h = $(get h "$pv")"
    echo "s = $(get s "$pv")"
    echo "proof = ${#proof} hex chars = $(( ${#proof} / 2 )) bytes"

    echo
    echo '$ proof-xmr verify <h> <s> <proof>'
    "$bin" verify "$h" "$s" "$proof" 2>"$tmp/t" && rc=0 || rc=$?
    cat "$tmp/t"; echo "exit $rc"

    echo
    echo '$ proof-xmr verify <h with bit 0 flipped> <s> <proof>'
    bad_h="$(printf '%02x' $(( 0x${h:0:2} ^ 1 )))${h:2}"
    "$bin" verify "$bad_h" "$s" "$proof" 2>"$tmp/t" && rc=0 || rc=$?
    cat "$tmp/t"; echo "exit $rc"

    echo
    echo '$ proof-xmr verify <h> <s> <proof less its last byte>'
    "$bin" verify "$h" "$s" "${proof:0:-2}" 2>"$tmp/t" && rc=0 || rc=$?
    cat "$tmp/t"; echo "exit $rc"

    echo
    echo '$ proof-xmr prove edd3f55c1a631258d69cf7a2def9de1400000000000000000000000000000010   (l itself)'
    "$bin" prove edd3f55c1a631258d69cf7a2def9de1400000000000000000000000000000010 2>"$tmp/t" && rc=0 || rc=$?
    cat "$tmp/t"; echo "exit $rc"

    if [ ! -e "$here/vector.txt" ]; then
        printf '%s\n%s\n%s\n%s\n' "$secret" "$h" "$s" "$proof" >"$here/vector.txt"
        echo
        echo "vector.txt written from this run"
    fi

    echo
    echo '$ proof-xmr verify <vector.txt>'
    mapfile -t vec <"$here/vector.txt"
    "$bin" verify "${vec[1]}" "${vec[2]}" "${vec[3]}" 2>"$tmp/t" && rc=0 || rc=$?
    cat "$tmp/t"; echo "exit $rc"
    echo
    echo "worked vector (vector.txt):"
    echo "secret = ${vec[0]}"
    echo "h      = ${vec[1]}"
    echo "s      = ${vec[2]}"
    echo "proof  = ${vec[3]}"
} | tee "$here/run.log"
