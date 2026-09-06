#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
python3 $REPO/research/crosschain/poc/wsl/bp-pallas-port.py
cd $REPO/research/crosschain/poc/vendor/bp-pallas || exit 1
CARGO_TARGET_DIR="$HOME/target/bp-pallas" cargo build --release 2>&1 | grep -E "^(error|warning: unused)|error\[|^\s+-->|^\s+\|.*\^|Finished" | head -80
