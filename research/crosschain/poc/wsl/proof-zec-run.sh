#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
cd $REPO/research/crosschain/poc/proof-zec || exit 1
CARGO_TARGET_DIR="$HOME/target/proof-zec" cargo build --release 2>&1 | grep -E "^error|error\[|^\s+-->|^\s+\|.*\^|Finished" | head -60
if [ -x "$HOME/target/proof-zec/release/proof-zec" ]; then "$HOME/target/proof-zec/release/proof-zec" | tee $REPO/research/crosschain/poc/proof-zec/run.log; fi
