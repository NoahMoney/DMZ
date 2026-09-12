#!/bin/bash
# The whole proof-xmr suite in release — tests/{proof,cli,vector,gadget,audit}.rs — recorded in test.log (W-04).
# One test at a time and --nocapture, so the audit tests' summaries (constraint counts, the tamper sweep by field,
# the rejection reasons) read in order in the log. Built exactly as build.sh builds, plus --locked.
#
#   MSYS_NO_PATHCONV=1 wsl.exe -e bash -lc 'export REPO=<repo>; bash $REPO/research/crosschain/poc/proof-xmr/test-all.sh'
#   PROOF_XMR_SEED=<n> ... test-all.sh      # another seed for the seeded random inputs and masks (the default is fixed)
set -euo pipefail
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
here="$REPO/research/crosschain/poc/proof-xmr"
{
    echo "proof-xmr test suite, release, $(date -u +%Y-%m-%dT%H:%M:%SZ), $(uname -m), $(nproc) cpus, $(rustc --version), $(cargo --version)"
    echo "HEAD $(git -C "$REPO" rev-parse --short HEAD); proof-xmr files changed or new against HEAD: $(git -C "$REPO" status --porcelain -- research/crosschain/poc/proof-xmr | tr '\n' ' ')"
    bash "$here/fetch-bulletproofs.sh" -check
    # build.sh's target directory also holds the binary tools/xmrswap pins; cargo rebuilds it only if its sources or
    # flags changed, and this says whether it did
    bin="$HOME/target/proof-xmr/release/proof-xmr"
    echo "before: $bin sha256 $(sha256sum "$bin" 2>/dev/null | cut -d' ' -f1)"
    start="$(date +%s)"
    "$REPO/research/crosschain/poc/wsl/build.sh" proof-xmr test --release --locked -- --test-threads=1 --nocapture
    echo "wall clock: $(( $(date +%s) - start )) s"
    echo "after:  $bin sha256 $(sha256sum "$bin" | cut -d' ' -f1)"
} 2>&1 | sed -e "s#$REPO#<repo>#g" -e "s#$HOME#<home>#g" | tee "$here/test.log"
