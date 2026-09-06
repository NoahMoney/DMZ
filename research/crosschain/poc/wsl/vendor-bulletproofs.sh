#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
# Vendors bulletproofs 5.0.0 and fixes the yoloproofs (R1CS) build break against curve25519-dalek 4:
# Scalar::from_canonical_bytes returns subtle::CtOption, which has no ok_or.
set -e
src=$(ls -d "$HOME"/.cargo/registry/src/*/bulletproofs-5.0.0 | head -1)
dst=$REPO/research/crosschain/poc/vendor/bulletproofs
rm -rf "$dst"; mkdir -p "$dst"
cp -r "$src"/. "$dst"/
rm -f "$dst/.cargo-ok"
sed -i 's/Scalar::from_canonical_bytes(read32!()).ok_or(R1CSError::FormatError)?/Option::<Scalar>::from(Scalar::from_canonical_bytes(read32!())).ok_or(R1CSError::FormatError)?/g' "$dst/src/r1cs/proof.rs"
grep -n "Option::<Scalar>::from" "$dst/src/r1cs/proof.rs" | head
grep -rn "from_canonical_bytes" "$dst/src" | grep -v "Option::<Scalar>" | head
