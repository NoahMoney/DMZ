#!/bin/bash
R=$(ls -d "$HOME"/.cargo/registry/src/*/ | head -1); RD=$(ls -d "$R"/reddsa-0.5* | head -1); FC=$(ls -d "$R"/frost-core-3* | head -1)
echo "=== reddsa frost/redpallas/dkg.md"; sed -n '1,80p' "$RD/src/frost/redpallas/dkg.md"
echo "=== reddsa README frost section"; grep -n -i "audit\|frost\|production\|experimental\|warning" "$RD/README.md" | head -20
echo "=== frost-core README / lib.rs security notes"; grep -n -i "audit\|not been\|production\|nonce\|reuse\|warning" "$FC/README.md" "$FC/src/lib.rs" 2>/dev/null | head -20
echo "=== frost-core dkg module"; ls "$FC/src/keys" 2>/dev/null; grep -n "pub fn part1\|pub fn part2\|pub fn part3\|pub fn refresh\|pub fn repair" "$FC/src/keys/dkg.rs" "$FC/src/keys/refresh.rs" "$FC/src/keys/repairable.rs" 2>/dev/null | head -12
