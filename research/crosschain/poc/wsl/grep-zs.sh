#!/bin/bash
R=$(ls -d "$HOME"/.cargo/registry/src/*/ | head -1)
ZS=$(ls -d "$R"/zcash_script-0.5* | head -1); ZA=$(ls -d "$R"/zcash_address-0.13* | head -1); FR=$(ls -d "$R"/frost-rerandomized-3* | head -1)
grep -rn "fn to_bytes" "$ZS/src/script/mod.rs" | head -3; grep -n "^pub trait\|^impl.*for Component" "$ZS/src/script/mod.rs" | head -10
grep -rn "fn from_transparent_p2sh" "$ZA/src" | head -2; grep -n "^pub trait" "$ZA/src/convert.rs" | head
sed -n '128,140p' "$FR/src/lib.rs"; grep -n "derive.*Clone\|derive.*Copy" "$FR/src/lib.rs" | sed -n '1,4p'
