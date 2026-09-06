#!/bin/bash
R=$(ls -d "$HOME"/.cargo/registry/src/*/ | head -1)
ZS=$(ls -d "$R"/zcash_script-0.5* | head -1); RD=$(ls -d "$R"/reddsa-0.5* | head -1); ZA=$(ls -d "$R"/zcash_address-0.13* | head -1)
echo "=== Flags consts"; sed -n '133,182p' "$ZS/src/interpreter.rs" | grep -n "const \|///" | grep "const" 
echo "=== signature.rs decode API"; grep -n "pub fn\|pub struct\|pub enum" "$ZS/src/signature.rs" | head -20
echo "=== PubKey verify"; grep -rn "pub fn verify\|pub fn is_valid\|pub struct PubKey" "$ZS/src/external"/*.rs "$ZS/src/interpreter.rs" 2>/dev/null | head
echo "=== zcash_address constructors"; grep -n "pub fn from_transparent\|pub fn from_" "$ZA/src/lib.rs" | head
echo "=== reddsa frost_redpallas test (rerandomized part)"; grep -n "rerandomized\|RandomizedParams\|randomizer\|fn check_randomized\|aggregate\|verify\|VerificationKey\|serialize" "$RD/tests/frost_redpallas.rs" | head -40
echo "=== reddsa rerandomized.rs"; sed -n '1,60p' "$RD/src/frost/redpallas/rerandomized.rs"
echo "=== frost_rerandomized RandomizedParams API"; FR=$(ls -d "$R"/frost-rerandomized-* | head -1); echo $FR; grep -n "pub fn new\|pub fn from_randomizer\|pub fn randomizer\|pub fn randomized_verifying_key\|pub struct RandomizedParams\|pub struct Randomizer\|pub fn sign\|pub fn aggregate\|pub fn serialize\|pub fn deserialize" "$FR/src/lib.rs" | head -30
echo "=== frost_core KeyPackage/PublicKeyPackage/SigningPackage API"; FC=$(ls -d "$R"/frost-core-* | head -1); echo $FC; grep -n "pub fn try_from\|pub fn verifying_key\|pub fn new\|pub fn signing_share\|impl.*TryFrom<SecretShare" "$FC/src/keys.rs" "$FC/src/lib.rs" "$FC/src/signing_key.rs" "$FC/src/verifying_key.rs" 2>/dev/null | head -30
grep -n "pub enum IdentifierList\|Default" "$FC/src/keys.rs" | head -5
