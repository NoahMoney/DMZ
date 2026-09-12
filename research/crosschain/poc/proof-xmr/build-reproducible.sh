#!/bin/bash
# The reproducible build of the prover (W-04; the rest of GAP-013): the one recipe whose output can be pinned in
# tools/xmrswap/pin.go and rebuilt, byte for byte, by anyone with the same toolchain.
#
# Why build.sh's own `cargo build --release` is not that recipe (repro.log): the patched bulletproofs is a path
# dependency outside the crate's directory, so rustc is handed its absolute path and keeps it in the binary's panic
# locations, and cargo hashes that location into the crate disambiguators of bulletproofs and of proof-xmr, which are
# mangled into every symbol name; the registry crates keep $CARGO_HOME's absolute path in their panic locations. The
# hash therefore depends on where the checkout sits and on the user's home directory. Cargo's trim-paths is not
# available on stable 1.98.1, and --remap-path-prefix removes the strings but not the location from the
# disambiguators. So this builds the committed sources at one fixed path with one fixed CARGO_HOME instead; the target
# directory never enters the binary.
#
#   build-reproducible.sh           the committed sources at HEAD of the repository this script is in
#   build-reproducible.sh <tree>    a directory holding research/crosschain/poc/proof-xmr as committed (its target/
#                                   and any vendor copy beside it are ignored; the crate is always fetched afresh)
#   prints one line:  sha256 <hash>  <bytes> bytes  <binary>
#
# Uses /tmp/proof-xmr-build (removed and recreated: one build at a time) and /tmp/proof-xmr-cargo (a download cache;
# every crate is checked against Cargo.lock's checksum, bulletproofs against fetch-bulletproofs.sh's pin). The binary
# goes to $CARGO_TARGET_DIR (default ~/target/proof-xmr-reproducible)/release/proof-xmr. Needs network access to
# crates.io. The hash depends on the toolchain; the one measured (repro.log): rustc and cargo 1.98.1,
# x86_64-unknown-linux-gnu, linked by Ubuntu 24.04's cc (gcc 13.3.0-6ubuntu2~24.04.1, binutils 2.42, glibc
# 2.39-0ubuntu8.8), no RUSTFLAGS, no ~/.cargo/config.
set -euo pipefail
export PATH="$HOME/.cargo/bin:$PATH"
here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../../../.." && pwd)"
build=/tmp/proof-xmr-build
export CARGO_HOME=/tmp/proof-xmr-cargo
target="${CARGO_TARGET_DIR:-$HOME/target/proof-xmr-reproducible}"
unset RUSTFLAGS CARGO_ENCODED_RUSTFLAGS CARGO_BUILD_RUSTFLAGS
want="rustc 1.98.1 (48a229cea 2026-09-01)"
if [ "$(rustc --version)" != "$want" ]; then
    echo "warning: $(rustc --version) is not $want: the hash will not be repro.log's" >&2
fi

rm -rf "$build"
mkdir -p "$build"
if [ $# -ge 1 ]; then
    tar -C "$1" --exclude=research/crosschain/poc/proof-xmr/target -c research/crosschain/poc/proof-xmr | tar -x -C "$build"
else
    git -C "$repo" archive --format=tar HEAD research/crosschain/poc/proof-xmr | tar -x -C "$build"
fi
bash "$build/research/crosschain/poc/proof-xmr/fetch-bulletproofs.sh" >&2
(cd "$build/research/crosschain/poc/proof-xmr" && CARGO_TARGET_DIR="$target" cargo build --release --locked --quiet 2>/dev/null) ||
    (cd "$build/research/crosschain/poc/proof-xmr" && CARGO_TARGET_DIR="$target" cargo build --release --locked)
bin="$target/release/proof-xmr"
echo "sha256 $(sha256sum "$bin" | cut -d' ' -f1)  $(stat -c %s "$bin") bytes  $bin"
