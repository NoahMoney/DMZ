#!/bin/bash
R=$(ls -d "$HOME"/.cargo/registry/src/*/ | head -1)
ZS=$(ls -d "$R"/zcash_script-0.5* | head -1); RD=$(ls -d "$R"/reddsa-0.5* | head -1); ZA=$(ls -d "$R"/zcash_address-0.13* | head -1)
echo "=== pattern.rs 405-500"; sed -n '405,500p' "$ZS/src/pattern.rs"
echo "=== pattern.rs 180-260"; sed -n '180,260p' "$ZS/src/pattern.rs"
echo "=== interpreter check_lock_time impl + CLTV opcode 690-730"; sed -n '480,510p' "$ZS/src/interpreter.rs"; sed -n '690,730p' "$ZS/src/opcode/mod.rs"
echo "=== op.rs push helpers"; grep -n "^pub fn\|^pub const [A-Z_]*: Opcode" "$ZS/src/op.rs" | head -60
echo "=== reddsa frost redpallas API"; grep -n "^pub \|^    pub fn\|^pub fn" "$RD/src/frost/redpallas.rs" | head -40
echo "=== reddsa frost/redpallas dir"; ls "$RD/src/frost/redpallas" 2>/dev/null; grep -rn "pub fn generate_with_dealer\|pub fn commit\|pub fn sign\|pub fn aggregate\|pub fn verifying_key\|pub struct RandomizedParams\|pub fn new\b" "$RD/src/frost/redpallas.rs" "$RD/src/frost/redpallas"/*.rs 2>/dev/null | head -20
echo "=== reddsa Cargo features for frost"; grep -n "frost" "$RD/Cargo.toml"
echo "=== zcash_address p2sh"; grep -rn "pub fn from_transparent_p2sh\|pub fn encode\|pub enum Network\|NetworkType" "$ZA/src/lib.rs" "$ZA/src/encoding.rs" 2>/dev/null | head; grep -rn "pub enum NetworkType" "$R"/zcash_protocol-0.10*/src/consensus.rs | head -3
