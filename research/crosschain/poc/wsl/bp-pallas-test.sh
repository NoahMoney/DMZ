#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
cd $REPO/research/crosschain/poc/vendor/bp-pallas || exit 1
CARGO_TARGET_DIR="$HOME/target/bp-pallas" cargo build --release 2>&1 | grep -E "^error|error\[|^\s+-->|Finished" | head -40
CARGO_TARGET_DIR="$HOME/target/bp-pallas" cargo test --release 2>&1 | grep -E "^error|error\[|^\s+-->|test result|running|panicked|FAILED" | head -20
