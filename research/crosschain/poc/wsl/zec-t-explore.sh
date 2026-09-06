#!/bin/bash
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
export PATH="$HOME/.cargo/bin:$PATH"
cd $REPO/research/crosschain/poc/zec-t-htlc || exit 1
CARGO_TARGET_DIR="$HOME/target/zec-t-htlc" cargo fetch 2>&1 | grep -E "error|Locking" | head
CARGO_TARGET_DIR="$HOME/target/zec-t-htlc" cargo tree --depth 1 2>&1 | grep -E "zcash|secp" 
R=$(ls -d "$HOME"/.cargo/registry/src/*/ | head -1)
ZS=$(ls -d "$R"/zcash_script-0.5* | head -1); ZT=$(ls -d "$R"/zcash_transparent-0.10* | head -1); ZP=$(ls -d "$R"/zcash_primitives-0.30* | head -1)
echo "=== zcash_script: $ZS"; ls "$ZS/src"
echo "--- lib.rs public items:"; grep -n "^pub \(fn\|struct\|enum\|trait\|mod\|use\|type\)" "$ZS/src/lib.rs" | head -40
echo "--- CLTV / flags:"; grep -rn "CHECKLOCKTIMEVERIFY\|CheckLockTimeVerify\|CHECKSEQUENCEVERIFY\|CheckSequenceVerify\|VerificationFlags\|pub struct Flags\|CHECKLOCKTIME" "$ZS/src" | head -20
echo "--- interpreter entry points:"; grep -rn "pub fn verify\|pub fn eval\|pub fn check_lock_time\|fn verify_script\|pub trait ZcashScript\|pub fn verify_callback\|SighashCalculator" "$ZS/src" | head -20
echo "=== zcash_transparent: $ZT"; ls "$ZT/src"
echo "--- sighash / builder / address:"; grep -rn "pub fn signature_hash\|pub fn v5_signature_hash\|pub fn v4_signature_hash\|pub enum TransparentAddress\|pub fn script\|pub struct Script\|pub fn encode\|SignableInput\|pub fn transparent_sig_digest\|pub fn from_script" "$ZT/src" | head -30
echo "=== zcash_primitives: $ZP"; grep -rn "pub fn signature_hash\|pub enum SignableInput\|pub fn to_txid_digest\|pub fn digest\|pub fn from_transaction\|TxDigests" "$ZP/src/transaction/sighash.rs" "$ZP/src/transaction/sighash_v5.rs" "$ZP/src/transaction/txid.rs" 2>/dev/null | head -20
grep -n "pub struct TransactionData\|pub fn from_parts\|pub fn unauthed\|pub struct Authorized\|pub fn freeze\|pub struct Unauthorized" "$ZP/src/transaction/mod.rs" | head -20
