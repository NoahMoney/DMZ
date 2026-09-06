#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
P=$REPO/research/crosschain/poc
for crate in frost-zec zec-t-htlc; do
  echo "===== $crate"
  cd "$P/$crate" || exit 1
  CARGO_TARGET_DIR="$HOME/target/$crate" cargo build --release 2>&1 | grep -E "^error|error\[|^\s+-->|^\s+= |Finished" | head -40
  if [ -x "$HOME/target/$crate/release/$crate" ]; then "$HOME/target/$crate/release/$crate" | tee "$P/$crate/run.log"; fi
done
