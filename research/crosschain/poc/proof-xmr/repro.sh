#!/bin/bash
# The prover build, reproduced (W-04: the rest of GAP-013). The committed sources of research/crosschain/poc/proof-xmr
# at HEAD, plus the patched crate fetch-bulletproofs.sh rebuilds, are built from two different directories and
# hashed; the hashes are compared with each other and with the build tools/xmrswap runs (xmrswap.ProverSHA256,
# tools/xmrswap/pin.go). Where two binaries differ, the embedded strings that differ are printed. Then the same two
# trees are built with cargo's trim-paths, once more under a fresh CARGO_HOME, and the path-independent binary is
# checked against the recorded vector and against the pinned binary (a proof from each verifies under the other).
#
# Writes repro.log beside this script. Edits nothing in the repository: every build happens under a fresh temporary
# directory, removed at the end. Needs network access (crates.io) for fetch-bulletproofs.sh and the fresh CARGO_HOME.
#
#   MSYS_NO_PATHCONV=1 wsl.exe -e bash -lc 'export REPO=<repo>; bash $REPO/research/crosschain/poc/proof-xmr/repro.sh'
set -euo pipefail
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
here="$REPO/research/crosschain/poc/proof-xmr"
pinned_bin="${PINNED_BIN:-$HOME/target/proof-xmr/release/proof-xmr}"
pin="$(sed -nE 's/^const ProverSHA256 = "([0-9a-f]{64})".*/\1/p' "$REPO/tools/xmrswap/pin.go")"
work="$(mktemp -d /tmp/pxr-repro.XXXXXX)"
trap 'rm -rf "$work"' EXIT
scrub() { sed -e "s#$work#<work>#g" -e "s#$REPO#<repo>#g" -e "s#$HOME#<home>#g"; }
trim='profile.release.trim-paths="all"'

declare -A SUM BIN
# build <name> <crate dir> <target dir> [cargo args...]: a release build exactly as build.sh runs it, plus --locked
build() {
    local name="$1" src="$2" tgt="$3"; shift 3
    (cd "$src" && CARGO_TARGET_DIR="$tgt" cargo build --release --locked "$@" 2>&1 | { grep -E '^(warning|error)|Finished' || true; } | scrub)
    BIN[$name]="$tgt/release/proof-xmr"
    SUM[$name]="$(sha256sum "${BIN[$name]}" | cut -d' ' -f1)"
    printf '  %-24s sha256 %s  %s bytes\n' "$name" "${SUM[$name]}" "$(stat -c %s "${BIN[$name]}")"
}
# compare <name> <name>
compare() {
    if [ "${SUM[$1]}" = "${SUM[$2]}" ]; then
        echo "  $1 == $2: byte-identical"
    else
        echo "  $1 != $2: $(cmp -l "${BIN[$1]}" "${BIN[$2]}" 2>/dev/null | wc -l) differing byte positions, sizes $(stat -c %s "${BIN[$1]}") and $(stat -c %s "${BIN[$2]}")"
    fi
}
# paths <binary>: the build-machine paths embedded in it (panic locations), one per source directory
paths() { strings -n 6 "$1" | { grep -oE '(/tmp/|/home/|/mnt/)[^ "]*' || true; } | sed -E 's#/src/.*#/src/...#' | sort -u | scrub; }
# disamb <binary>: the crate disambiguators (cargo's -C metadata, v0-mangled into every symbol name) of the path
# dependency, of this crate, and of one registry crate for comparison
disamb() { strings -n 6 "$1" | { grep -oE 'Cs[0-9A-Za-z]+_(12bulletproofs|9proof_xmr|16curve25519_dalek)' || true; } | sort -u | sed -E 's/^Cs([0-9A-Za-z]+)_[0-9]+(.*)/\2=\1/' | tr '\n' ' '; }
# export_tree <dir>: the committed proof-xmr sources at HEAD, and the patched crate beside them as a fresh clone has it
export_tree() {
    mkdir -p "$1"
    git -C "$REPO" archive --format=tar HEAD research/crosschain/poc/proof-xmr | tar -x -C "$1"
    bash "$1/research/crosschain/poc/proof-xmr/fetch-bulletproofs.sh" | scrub
}

{
    echo "proof-xmr reproducible-build check, $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo
    echo "== toolchain"
    rustc -vV | sed 's/^/  /'
    cargo -vV | sed -n '1,3s/^/  /p'
    echo "  linker: $(cc --version | head -1); $(ld --version | head -1); $(ldd --version | head -1)"
    echo "  os: $(. /etc/os-release; echo "$PRETTY_NAME"), $(uname -srm)"
    echo "  RUSTFLAGS='${RUSTFLAGS:-}' CARGO_ENCODED_RUSTFLAGS='${CARGO_ENCODED_RUSTFLAGS:-}' ~/.cargo/config*: $(ls "$HOME"/.cargo/config* 2>/dev/null | wc -l) file(s)"
    echo
    echo "== sources"
    echo "  HEAD $(git -C "$REPO" rev-parse HEAD); proof-xmr last changed in $(git -C "$REPO" log -1 --format='%h (%ad)' --date=short -- research/crosschain/poc/proof-xmr)"
    echo "  working tree of proof-xmr against HEAD: $(git -C "$REPO" status --porcelain -- research/crosschain/poc/proof-xmr | sed 's/^/  /' | tr '\n' ' ')"
    echo "  Cargo.lock: sha256 $(sha256sum "$here/Cargo.lock" | cut -d' ' -f1), $(grep -c '^\[\[package\]\]' "$here/Cargo.lock") packages, lockfile version $(sed -nE 's/^version = ([0-9]+)$/\1/p' "$here/Cargo.lock"), committed copy $(git -C "$REPO" diff --quiet HEAD -- research/crosschain/poc/proof-xmr/Cargo.lock && echo unchanged || echo MODIFIED)"
    echo "  every build below passes --locked, so it fails rather than resolve anything Cargo.lock does not already say;"
    echo "  bulletproofs is a path dependency (no checksum in Cargo.lock): it is fetch-bulletproofs.sh's crate + patch"
    echo
    echo "== the pinned build"
    echo "  xmrswap.ProverSHA256   $pin"
    if [ -f "$pinned_bin" ]; then
        SUM[pinned]="$(sha256sum "$pinned_bin" | cut -d' ' -f1)"; BIN[pinned]="$pinned_bin"
        echo "  $(echo "$pinned_bin" | scrub): sha256 ${SUM[pinned]}, $(stat -c %s "$pinned_bin") bytes ($([ "${SUM[pinned]}" = "$pin" ] && echo "is the pinned build" || echo "is NOT the pinned build"))"
        echo "  build-machine paths embedded in it:"; paths "$pinned_bin" | sed 's/^/    /'
    else
        echo "  no binary at $(echo "$pinned_bin" | scrub)"
    fi
    echo
    echo "== recipe 1: build.sh's own (cargo build --release), from the committed tree in three places"
    export_tree "$work/a"
    export_tree "$work/b/somewhere/else/entirely"
    build in-place "$here" "$work/target-inplace"
    build tree-a "$work/a/research/crosschain/poc/proof-xmr" "$work/target-a"
    build tree-b "$work/b/somewhere/else/entirely/research/crosschain/poc/proof-xmr" "$work/target-b"
    [ -n "${SUM[pinned]:-}" ] && compare in-place pinned
    compare tree-a tree-b
    compare tree-a in-place
    echo "  paths embedded in tree-a:"; paths "${BIN[tree-a]}" | sed 's/^/    /'
    echo "  paths embedded in tree-b:"; paths "${BIN[tree-b]}" | sed 's/^/    /'
    echo
    echo "== recipe 2a: the same with --config '$trim' (cargo's trim-paths)"
    (cd "$work/a/research/crosschain/poc/proof-xmr" && CARGO_TARGET_DIR="$work/target-trim" cargo build --release --locked --config "$trim" 2>&1 | sed 's/^/  /' | scrub) ||
        echo "  refused by cargo $(cargo --version | cut -d' ' -f2): trim-paths is not a stable profile setting in this toolchain"
    echo
    echo "== recipe 2b: the same with RUSTFLAGS=--remap-path-prefix for the tree and the registry"
    for t in a b/somewhere/else/entirely; do
        src="$work/$t/research/crosschain/poc"
        RUSTFLAGS="--remap-path-prefix=$src=/build --remap-path-prefix=$HOME/.cargo/registry/src=/registry" \
            build "remap-${t%%/*}" "$src/proof-xmr" "$work/target-remap-${t%%/*}"
        echo "    build-machine paths embedded: $(paths "${BIN[remap-${t%%/*}]}" | wc -l); crate disambiguators: $(disamb "${BIN[remap-${t%%/*}]}")"
    done
    compare remap-a remap-b
    echo "  (no path string is left, but cargo hashes the location of the path dependency into the disambiguators of"
    echo "  bulletproofs and proof_xmr, which are mangled into every symbol name; curve25519_dalek's, a registry crate's, is"
    echo "  the same in both, so it is the location and not the RUSTFLAGS that differs: each checkout gives its own binary)"
    echo
    echo "== recipe 3: build-reproducible.sh (the committed tree at a fixed path, CARGO_HOME fixed), from tree a and tree b"
    rm -rf /tmp/proof-xmr-cargo
    for t in a b/somewhere/else/entirely; do
        line="$(CARGO_TARGET_DIR="$work/target-repro-${t%%/*}" bash "$here/build-reproducible.sh" "$work/$t" 2>/dev/null)"
        BIN[repro-${t%%/*}]="$work/target-repro-${t%%/*}/release/proof-xmr"
        SUM[repro-${t%%/*}]="$(awk '{print $2}' <<<"$line")"
        printf '  %-24s %s\n' "repro-${t%%/*}" "$(awk '{print $1, $2, $3, $4}' <<<"$line")"
    done
    compare repro-a repro-b
    [ -n "${SUM[pinned]:-}" ] && compare repro-a pinned
    echo "  paths embedded in repro-a (fixed by the recipe, the same on every machine):"; paths "${BIN[repro-a]}" | sed 's/^/    /'
    echo
    echo "== the reproducible binary does what the pinned one does"
    mapfile -t vec <"$here/vector.txt"
    "${BIN[repro-a]}" verify "${vec[1]}" "${vec[2]}" "${vec[3]}" && echo "  repro-a verifies vector.txt: exit 0"
    if [ -n "${SUM[pinned]:-}" ]; then
        get() { sed -E "s/.*\"$1\":\"([0-9a-f]*)\".*/\1/" <<<"$2"; }
        kg="$("${BIN[repro-a]}" keygen)"; h="$(get h "$kg")"; s="$(get s "$kg")"
        p1="$(get proof "$("${BIN[repro-a]}" prove - <<<"$(get secret "$kg")")")"
        "$pinned_bin" verify "$h" "$s" "$p1" && echo "  a proof made by repro-a verifies under the pinned binary: exit 0"
        p2="$(get proof "$("$pinned_bin" prove - <<<"$(get secret "$kg")")")"
        "${BIN[repro-a]}" verify "$h" "$s" "$p2" && echo "  a proof made by the pinned binary verifies under repro-a: exit 0"
        # what differs between the two: the embedded paths, and the crate disambiguators the path dependency's
        # location feeds into (registry crates keep theirs)
        { diff <(strings -n 8 "$pinned_bin" | scrub | sed -E 's#/src/.*#/src/...#' | sort -u) <(strings -n 8 "${BIN[repro-a]}" | scrub | sed -E 's#/src/.*#/src/...#' | sort -u) || true; } | { grep '^[<>]' || true; } >"$work/strdiff"
        echo "  distinct strings that differ between the pinned binary (<) and repro-a (>): $(wc -l <"$work/strdiff"); the paths among them:"
        { grep '/' "$work/strdiff" || true; } | sed 's/^/    /'
        echo "  the other $(grep -vc '/' "$work/strdiff" || true) are symbol and codegen-unit names that carry the crate disambiguators:"
        echo "    pinned:  $(disamb "$pinned_bin")"
        echo "    repro-a: $(disamb "${BIN[repro-a]}")"
    fi
    rm -rf /tmp/proof-xmr-build /tmp/proof-xmr-cargo
    echo
    echo "== summary"
    echo "  pin in tools/xmrswap/pin.go:              $pin"
    echo "  recipe 1 in place (this checkout's path):  ${SUM[in-place]}"
    echo "  recipe 1 tree a / tree b:                  ${SUM[tree-a]} / ${SUM[tree-b]}"
    echo "  recipe 2b tree a / tree b:                 ${SUM[remap-a]} / ${SUM[remap-b]}"
    echo "  recipe 3 tree a / tree b:                  ${SUM[repro-a]} / ${SUM[repro-b]}"
} 2>&1 | scrub | tee "$here/repro.log"
