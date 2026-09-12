#!/bin/bash
# MT-M03: the prover's timings on the release build, N runs each (default 25). For every run: keygen, then `prove -`
# (the secret on stdin, as tools/xmrswap runs it), then `verify` of that proof. Recorded per run: the tool's own
# in-process timings (PROOF_XMR_TIMING=1: generator setup, then prove or verify; main.rs) and the wall clock of the
# process, which is what the companion waits for (one process per call; the 2^16 generators are rebuilt every time,
# by design). Reported: min, median, p95 (nearest rank) and max. Writes bench.log beside this script. Run it on an
# otherwise idle machine; the load average before and after is in the log.
#
#   MSYS_NO_PATHCONV=1 wsl.exe -e bash -lc 'export REPO=<repo>; bash $REPO/research/crosschain/poc/proof-xmr/bench.sh'
#   BIN=<another build> N=<runs> ... bench.sh
set -euo pipefail
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
here="$REPO/research/crosschain/poc/proof-xmr"
bin="${BIN:-$HOME/target/proof-xmr/release/proof-xmr}"
n="${N:-25}"
pin="$(sed -nE 's/^const ProverSHA256 = "([0-9a-f]{64})".*/\1/p' "$REPO/tools/xmrswap/pin.go")"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
scrub() { sed -e "s#$REPO#<repo>#g" -e "s#$HOME#<home>#g"; }
get() { sed -E "s/.*\"$1\":\"([0-9a-f]*)\".*/\1/" <<<"$2"; }
now() { date +%s.%N; }
# secs: a Rust Duration's Debug form (1.61s, 737.45ms, 7.01µs, 12ns) in seconds
secs() { awk '{ v = $1
    if (v ~ /ms$/)      { sub(/ms$/, "", v); print v / 1e3 }
    else if (v ~ /µs$/) { sub(/µs$/, "", v); print v / 1e6 }
    else if (v ~ /ns$/) { sub(/ns$/, "", v); print v / 1e9 }
    else                { sub(/s$/, "", v);  print v + 0 } }'; }
# stats <label> <file of seconds, one per line>
stats() { sort -g "$2" | awk -v name="$1" '{ a[NR] = $1 } END {
    n = NR; med = (n % 2) ? a[(n + 1) / 2] : (a[n / 2] + a[n / 2 + 1]) / 2
    k = int(0.95 * n); if (k < 0.95 * n) k++
    printf "  %-34s n=%-3d min %7.3f s   median %7.3f s   p95 %7.3f s   max %7.3f s\n", name, n, a[1], med, a[k], a[n] }'; }

{
    echo "proof-xmr timings (MT-M03), $(date -u +%Y-%m-%dT%H:%M:%SZ), N=$n runs of each"
    echo "  binary: $(echo "$bin" | scrub), sha256 $(sha256sum "$bin" | cut -d' ' -f1) ($([ "$(sha256sum "$bin" | cut -d' ' -f1)" = "$pin" ] && echo "the pinned build, xmrswap.ProverSHA256" || echo "NOT the pinned build"))"
    echo "  machine: $(grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | sed 's/^ //'), $(nproc) logical cpus, $(free -g | awk '/^Mem:/ {print $2}') GiB, $(. /etc/os-release; echo "$PRETTY_NAME"), $(uname -r)"
    echo "  $(rustc --version) (the toolchain installed now; the binary's own is in its .comment section: $(readelf -p .comment "$bin" 2>/dev/null | grep -o 'rustc version [^)]*)' | head -1))"
    echo "  load average before: $(cut -d' ' -f1-3 /proc/loadavg)"
    echo
    echo "  run  keygen->prove: gens      prove   process |  verify: gens     verify   process"
    for i in $(seq 1 "$n"); do
        kg="$("$bin" keygen)"
        h="$(get h "$kg")"; s="$(get s "$kg")"
        t0="$(now)"
        pv="$(PROOF_XMR_TIMING=1 "$bin" prove - <<<"$(get secret "$kg")" 2>"$tmp/e")"
        t1="$(now)"
        # timing: generators <d> prove <d> proof 1601 bytes
        read -r _ _ pg _ pp _ <"$tmp/e"
        echo "$pg" | secs >>"$tmp/gens"
        echo "$pp" | secs >>"$tmp/prove"
        awk -v a="$t0" -v b="$t1" 'BEGIN { print b - a }' >>"$tmp/prove_wall"
        proof="$(get proof "$pv")"
        [ "${#proof}" -eq 3202 ] || { echo "run $i: proof is ${#proof} hex characters"; exit 1; }
        t0="$(now)"
        PROOF_XMR_TIMING=1 "$bin" verify "$h" "$s" "$proof" 2>"$tmp/e" || { echo "run $i: verify failed: $(cat "$tmp/e")"; exit 1; }
        t1="$(now)"
        # timing: generators <d> verify <d> proof 1601 bytes
        read -r _ _ vg _ vv _ <"$tmp/e"
        echo "$vg" | secs >>"$tmp/gens"
        echo "$vv" | secs >>"$tmp/verify"
        awk -v a="$t0" -v b="$t1" 'BEGIN { print b - a }' >>"$tmp/verify_wall"
        printf '  %3d  %22s %9s %8.3fs | %15s %9s %8.3fs\n' "$i" "$pg" "$pp" "$(tail -1 "$tmp/prove_wall")" "$vg" "$vv" "$(tail -1 "$tmp/verify_wall")"
    done
    echo
    echo "  load average after: $(cut -d' ' -f1-3 /proc/loadavg)"
    echo
    stats "generator setup (2^16, every call)" "$tmp/gens"
    stats "prove (in process, after gens)" "$tmp/prove"
    stats "verify (in process, after gens)" "$tmp/verify"
    stats "prove, whole process (wall)" "$tmp/prove_wall"
    stats "verify, whole process (wall)" "$tmp/verify_wall"
} 2>&1 | scrub | tee "$here/bench.log"
