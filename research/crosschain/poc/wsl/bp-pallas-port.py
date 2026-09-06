#!/usr/bin/env python3
: "${REPO:?set REPO to the repository path as seen from this shell (WSL: /mnt/c/.../repo)}"
"""Transform the copied dalek Bulletproofs files (r1cs/*, inner_product_proof.rs, util.rs) to the Pallas types."""
import re, pathlib

root = pathlib.Path('$REPO/research/crosschain/poc/vendor/bp-pallas/src')
files = ['r1cs/constraint_system.rs', 'r1cs/linear_combination.rs', 'r1cs/metrics.rs', 'r1cs/mod.rs',
         'r1cs/proof.rs', 'r1cs/prover.rs', 'r1cs/verifier.rs', 'inner_product_proof.rs', 'util.rs']

def strip_tests(s):
    i = s.find('#[cfg(test)]')
    return s[:i] if i >= 0 else s

def strip_block(s, start_pat):
    """Remove an `impl ... { ... }` block starting at the first line matching start_pat (brace-balanced)."""
    m = re.search(start_pat, s, re.M)
    if not m:
        return s
    i = m.start()
    j = s.find('{', i)
    depth = 0
    k = j
    while k < len(s):
        if s[k] == '{': depth += 1
        elif s[k] == '}':
            depth -= 1
            if depth == 0:
                break
        k += 1
    return s[:i] + s[k+1:]

for rel in files:
    p = root / rel
    s = p.read_text()
    s = strip_tests(s)
    # imports
    s = re.sub(r'^\s*use curve25519_dalek::[^;]+;\s*$', 'use crate::types::*;', s, flags=re.M)
    s = re.sub(r'^\s*use clear_on_drop::[^;]+;\s*$', '', s, flags=re.M)
    s = re.sub(r'^\s*use serde::[^;]+;\s*$', '', s, flags=re.M)
    s = re.sub(r'^\s*use serde_derive::[^;]+;\s*$', '', s, flags=re.M)
    s = re.sub(r'^\s*extern crate [^;]+;\s*$', '', s, flags=re.M)
    s = re.sub(r'^#!\[cfg_attr[^\n]*\n', '', s, flags=re.M)
    s = re.sub(r'^#\[cfg_attr\(feature = "docs"[^\n]*\n', '', s, flags=re.M)
    s = re.sub(r'^#!\[deny\(missing_docs\)\]\s*$', '', s, flags=re.M)
    s = s.replace('use alloc::', 'use std::')
    s = re.sub(r'^\s*#\[cfg\(feature = "yoloproofs"\)\]\s*$', '', s, flags=re.M)
    s = re.sub(r'^\s*#\[cfg\(feature = "std"\)\]\s*$', '', s, flags=re.M)
    s = re.sub(r'(use crate::types::\*;\n)(\s*use crate::types::\*;\n)+', r'\1', s)
    s = s.replace('.as_bytes()', '.to_bytes().as_ref()')
    s = re.sub(r'\b(\w+) \* self\.pc_gens\.B\b', r'self.pc_gens.B * \1', s)
    # types
    s = s.replace('RistrettoPoint', 'Point').replace('CompressedRistretto', 'CompressedPoint')
    s = s.replace('Scalar::from_bytes_mod_order_wide', 'Scalar::from_uniform_bytes')
    s = s.replace('Scalar::from_canonical_bytes', 'Scalar::from_repr')
    s = re.sub(r'\.invert\(\)(?!\.unwrap)', '.invert().unwrap()', s)
    # zeroing: drop clear() calls and Drop impls
    s = re.sub(r'^\s*[\w\.\[\]]+\.clear\(\);\s*$', '', s, flags=re.M)
    while re.search(r'^impl(<[^>]*>)? Drop for ', s, re.M):
        s = strip_block(s, r'^impl(<[^>]*>)? Drop for ')
    # serde impls in proof.rs
    if rel == 'r1cs/proof.rs':
        i = s.find('impl Serialize for R1CSProof')
        if i >= 0:
            s = s[:i]
        s = s.replace('#[derive(Clone, Debug, Serialize, Deserialize)]', '#[derive(Clone, Debug)]')
        s = s.replace('#[derive(Clone, Debug)]\n#[serde(', '#[derive(Clone, Debug)]\n#[allow(')  # harmless if absent
    if rel == 'inner_product_proof.rs':
        s = s.replace('#[derive(Clone, Debug, Serialize, Deserialize)]', '#[derive(Clone, Debug)]')
    # digest/sha3 leftovers
    s = re.sub(r'^\s*use (digest|sha3)::[^;]+;\s*$', '', s, flags=re.M)
    # Point::identity() needs the Group trait, is_identity gives Choice
    s = s.replace('mega_check.is_identity()', 'bool::from(mega_check.is_identity())')
    p.write_text(s)
    print('transformed', rel, len(s.splitlines()), 'lines')

# remaining suspicious references
import subprocess
out = subprocess.run(['grep', '-rn', r'curve25519\|clear_on_drop\|serde\|Sha3\|from_uniform_bytes(&uniform\|hash_from_bytes', str(root)], capture_output=True, text=True).stdout
print('=== leftovers ===')
print(out or '(none)')
