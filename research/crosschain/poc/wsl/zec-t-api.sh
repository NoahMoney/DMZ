#!/bin/bash
R=$(ls -d "$HOME"/.cargo/registry/src/*/ | head -1)
ZS=$(ls -d "$R"/zcash_script-0.5* | head -1); ZT=$(ls -d "$R"/zcash_transparent-0.10* | head -1); ZP=$(ls -d "$R"/zcash_primitives-0.30* | head -1); RD=$(ls -d "$R"/reddsa-0.5* | head -1)
echo "=== Script::eval (lib.rs 195-230)"; sed -n '195,230p' "$ZS/src/lib.rs"
echo "=== interpreter State/Flags/SighashCalculator (interpreter.rs 125-240, 450-470)"; sed -n '125,135p;170,200p;215,240p;450,470p' "$ZS/src/interpreter.rs"
echo "=== pattern helpers"; grep -n "^pub fn" "$ZS/src/pattern.rs"
echo "=== script::Code / PubKey / Sig types"; grep -n "^pub struct\|^pub enum\|^pub type\|pub fn from_raw_parts\|pub fn from_bytes\|pub fn to_bytes\|pub fn serialize\|pub fn parse\|pub fn from_slice" "$ZS/src/script/mod.rs" | head -30
echo "=== opcode PushValue"; grep -n "^pub enum PushValue\|^pub enum LargeValue\|^pub enum SmallValue\|pub fn push_value\|pub fn from_slice\|impl.*PushValue" "$ZS/src/opcode/push_value.rs" 2>/dev/null | head; ls "$ZS/src/opcode"
echo "=== zcash_transparent sighash.rs 1-130"; sed -n '1,130p' "$ZT/src/sighash.rs"
echo "=== zcash_transparent address.rs 100-200"; sed -n '100,200p' "$ZT/src/address.rs"
echo "=== zcash_primitives sighash.rs"; sed -n '1,80p' "$ZP/src/transaction/sighash.rs"
echo "=== TransactionData::from_parts"; sed -n '350,400p' "$ZP/src/transaction/mod.rs"
echo "=== reddsa frost module"; ls "$RD/src"; grep -n "pub fn\|pub mod\|pub use" "$RD/src/frost.rs" 2>/dev/null | head -20; ls "$RD/src/frost" 2>/dev/null
