#!/bin/bash
export PATH="$HOME/.cargo/bin:$PATH"
for c in zcash_script zcash_primitives zcash_transparent zcash_protocol zcash_address zcash_encoding; do cargo search "$c" --limit 1 2>/dev/null | head -1; done
echo "--- zcash_script features / build deps:"
cargo info zcash_script 2>/dev/null | sed -n '1,60p' | grep -iE "version|features|cxx|cc |bindgen|pure|rust|legacy|external-secp" | head -12
echo "--- reddsa orchard.rs 60-90:"
sed -n '60,90p' "$HOME"/.cargo/registry/src/*/reddsa-0.5.2/src/orchard.rs
echo "--- orchard keys.rs 120-200:"
sed -n '120,200p' "$HOME"/.cargo/registry/src/*/orchard-0.15.5/src/keys.rs
echo "--- orchard FullViewingKey 318-345, 425-470:"
sed -n '318,345p;425,470p' "$HOME"/.cargo/registry/src/*/orchard-0.15.5/src/keys.rs
