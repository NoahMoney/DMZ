#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
# usage: build.sh <crate-dir-under-poc> [cargo args...]
export PATH="$HOME/.cargo/bin:$PATH"
crate="$1"; shift
mkdir -p "$HOME/target"
cd "$REPO/research/crosschain/poc/$crate" || exit 1
CARGO_TARGET_DIR="$HOME/target/$crate" cargo "$@"
