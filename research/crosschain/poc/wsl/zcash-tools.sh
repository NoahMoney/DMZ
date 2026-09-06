#!/bin/bash
export PATH="$HOME/.cargo/bin:$PATH"
echo "g++: $(which g++) clang: $(which clang) cmake: $(which cmake) python3: $(which python3)"
cargo info zcash_script 2>/dev/null | sed -n '1,40p' | grep -E "^(version|license|features|  )" | head -20
echo "--- zcash_transparent features:"
cargo info zcash_transparent 2>/dev/null | grep -E "^(version|features|  )" | head -12
echo "--- reddsa features:"
cargo info reddsa 2>/dev/null | grep -E "^(version|features|  )" | head -12
