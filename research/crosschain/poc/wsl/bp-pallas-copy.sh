#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
# Stage a Pallas port of the dalek Bulletproofs R1CS: copy the files we keep, then list every curve-specific call.
set -e
src=$REPO/research/crosschain/poc/vendor/bulletproofs/src
dst=$REPO/research/crosschain/poc/vendor/bp-pallas/src
rm -rf "$dst"; mkdir -p "$dst/r1cs"
for f in constraint_system linear_combination metrics mod proof prover verifier; do cp "$src/r1cs/$f.rs" "$dst/r1cs/"; done
for f in inner_product_proof generators transcript util errors; do cp "$src/$f.rs" "$dst/"; done
cd "$dst"
echo "=== curve-specific references (file:line) ==="
grep -n "curve25519\|clear_on_drop\|\.clear()\|Scalar::\(random\|from_bytes\|from_canonical\|ONE\|ZERO\|from\)\|\.invert()\|compress()\|decompress()\|multiscalar\|to_bytes()\|from_bytes\|Identity\|identity()\|is_identity\|CompressedRistretto\|RistrettoPoint\|from_uniform_bytes\|Sha3\|shake\|Shake\|GeneratorsChain\|sha3::\|digest::" r1cs/*.rs *.rs | grep -v "^\s*//" | sed 's|/mnt/c/.*/src/||' 
echo "=== drop impls ==="
grep -n "impl.*Drop\|fn drop" -A 8 r1cs/prover.rs | head -40
echo "=== feature gates / cfg ==="
grep -n "cfg(feature\|#\[cfg" r1cs/*.rs *.rs | head -20
