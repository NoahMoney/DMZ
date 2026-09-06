#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
cd $REPO/research/crosschain/poc/proof-zec || exit 1
cargo search orchard --limit 3 2>/dev/null | grep '^orchard '
cargo add orchard --no-default-features 2>&1 | grep -E "Adding|error" | head -3
CARGO_TARGET_DIR="$HOME/target/proof-zec" cargo fetch 2>&1 | grep -E "error|Locking" | head -5
CARGO_TARGET_DIR="$HOME/target/proof-zec" cargo tree --depth 1 2>&1 | head -20
echo "--- registry sources:"
ls -d "$HOME"/.cargo/registry/src/*/{orchard,reddsa,pasta_curves,halo2_gadgets,halo2_proofs}-* 2>/dev/null
echo "--- orchard: ak sign / spend auth key derivation:"
grep -rn "is_odd\|negate\|SPEND_AUTH\|y.*odd\|ct_eq(&1)\|conditional_negate" "$HOME"/.cargo/registry/src/*/orchard-*/src/keys.rs | head -20
grep -rn "fn basepoint" -A 3 "$HOME"/.cargo/registry/src/*/reddsa-*/src/orchard.rs | head -20
grep -rn "pub fn from_bytes\|pub fn to_bytes\|pub struct FullViewingKey\|pub struct SpendAuthorizingKey\|pub struct SpendValidatingKey\|impl From<&SpendingKey> for SpendAuthorizingKey\|pub fn sign\|fn randomize" "$HOME"/.cargo/registry/src/*/orchard-*/src/keys.rs | head -30
