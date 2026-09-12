# The Monero cross-curve proof (`proof-xmr`): audit package

W-04 (`docs/MAINNET_TODO.md:35`), the preparation for the external audit MT-G03 (`docs/MAINNET_TODO.md:202`), with
MT-G04 (`:203`), MT-M03 (`:293`) and what remained of GAP-013 (`docs/MAINNET_GAP_ANALYSIS.md:56`). Written
2026-09-11 against HEAD `df077b8`. The sources under review last changed in these commits: `src/sha256_gadget.rs`
and `Cargo.lock` 60ab2e6, `src/lib.rs`, `src/main.rs` and `Cargo.toml` 4e54072, the patch and
`fetch-bulletproofs.sh` 7c087ff.

The proof is the largest trust item on the Monero path (`docs/SECURITY_THREAT_MODEL.md:85`). Without it a party
could reveal, on DERO, a value that hashes correctly but is not their Monero key share, and the other side's XMR
would be unspendable (`docs/XMR_SWAP.md:43-47`). Nobody outside the project has reviewed it.

## Results at a glance

| item | result | where |
|---|---|---|
| Statement | the circuit proves `SHA256(LE32(x)) = H` for **some 256-bit `x` congruent to the key mod ℓ**, not for the reduced key itself. A preimage that is not a reduced scalar verifies (**L1**, §4.3). The honest prover refuses such bytes, but a dishonest one does not have to | `tests/audit.rs` `known_gap_an_unreduced_preimage_verifies`; `tests/gadget.rs` `the_bit_decomposition_is_not_unique` |
| Circuit | 37,460 multipliers, 75,489 linear constraints, padded to 2^16. Derived by hand and measured, and the two agree | §3.4; `tests/gadget.rs` `constraint_counts` |
| SHA-256 gadget | equals the `sha2` crate on 2,573 inputs (525 edge, 2,048 random), every constraint satisfied. All 10,728 allocated bits, flipped one at a time, break a constraint | §5; `test.log` |
| Rejections | 0 of 3,202 tampered proofs (every byte, two masks), 0 of 261 wrong `H`, 0 of 269 wrong `S`, 0 of 3,395 wrong lengths, 0 of 14 other-party and spliced proofs verified | §10; `test.log` |
| Replay | a proof carries nothing swap-specific. With fresh per-swap keys that is harmless (§11) | `tests/audit.rs` `replay_…` |
| Reproducible build | `ProverSHA256` (`4d3bde0a…`) reproduces byte for byte, but only from the same absolute checkout path and `$CARGO_HOME`. `build-reproducible.sh` gives **`7293e9c97e0330966499fff397a0488ef40d38f65da49423429c8c548c83d39d`** from any checkout | §12; `repro.log` |
| Timings | 25 runs each on the pinned release build, per process with generators included: prove median 9.01 s (p95 9.85 s), verify median 1.92 s (p95 2.31 s). In process: generators 1.25 s (p95 1.59 s), prove 7.70 s (p95 8.60 s), verify 0.68 s (p95 0.80 s). Measured beside a monerod sync | §13; `bench.log` |
| Second verifier | no independent implementation exists. A port written here would share the design it is supposed to check, so it is not built; costed plan in §14 | §14 |

## 0. Start here

**Scope.** `research/crosschain/poc/proof-xmr/src/{lib.rs,sha256_gadget.rs,main.rs}`, the patch
`research/crosschain/poc/proof-xmr/patches/bulletproofs-5.0.0-r1cs-ctoption.patch`, and the parts of `bulletproofs`
5.0.0 (the R1CS module behind the `yoloproofs` feature), `curve25519-dalek` 4.1.3 and `merlin` 3.0.0 that the proof
runs through. Out of scope, but cited where the proof's guarantees are used: the DERO contract
(`contracts/htlc.bas`) and the companion (`tools/xmrswap`).

**Build and run** (WSL, Ubuntu 24.04, rustc 1.98.1):

```
wsl.exe -e bash -lc 'REPO=<repo> research/crosschain/poc/wsl/build.sh proof-xmr test --release'   # the suite
bash <repo>/research/crosschain/poc/proof-xmr/test-all.sh        # the same, recorded into test.log
bash <repo>/research/crosschain/poc/proof-xmr/build-reproducible.sh   # the pinnable binary (§12)
```

**Citation convention.** `lib.rs:N`, `gadget.rs:N` and `main.rs:N` are `research/crosschain/poc/proof-xmr/src/`
`lib.rs`, `sha256_gadget.rs` and `main.rs`. `bp/…:N` is bulletproofs 5.0.0 as `fetch-bulletproofs.sh` rebuilds it
(the crates.io `.crate` with sha256 `012e2e5f…` plus the patch) at `research/crosschain/poc/vendor/bulletproofs/`.
`dalek/…:N` is curve25519-dalek 4.1.3 (Cargo.lock checksum `97fb8b7c…`) and `merlin/…:N` is merlin 3.0.0 (checksum
`58c38e27…`), both as published on crates.io. `XMR_SWAP.md` is `docs/XMR_SWAP.md`. The recorded evidence sits next
to the crate: `test.log` (the whole suite), `repro.log`, `bench.log`, `run.log` and `vector.txt` (the worked vector).
`tools/xmrswap/swap.go` has uncommitted edits by its owner. Its line numbers here are those of the working tree on
2026-09-11 and may drift, so each citation also names the function.

## 1. The statement

### 1.1 What the project says is proved

"I know `s` such that `SHA256(s) = H` and `s·G = S` on ed25519" (`research/crosschain/poc/proof-xmr/README.md:3`,
`lib.rs:1-2`), where `s` is "32 bytes, < ℓ" (`XMR_SWAP.md:38`).

### 1.2 What the code proves

Let ℓ = 2^252 + 27742317777372353535851937790883648493 (`dalek/src/constants.rs:68-80`), let `G` be the ed25519
basepoint, and let `B` and `B̃` be the Pedersen generators of §6.1. For public `(H, S)` and the commitment `C` carried
in the proof, the verifier accepts a proof that some `(x, r)` with **x ∈ [0, 2^256)** and r ∈ Z_ℓ satisfies:

1. `S` decodes to a point of E[ℓ] \ {O}: prime order, not the identity (`lib.rs:186-189`);
2. `C = (x mod ℓ)·B + r·B̃` in ristretto255 (`bp/src/r1cs/verifier.rs:246-254` with `lib.rs:195`; `lib.rs:206`);
3. `SHA-256(LE32(x)) = H`, where `LE32(x)` is the 32-byte little-endian encoding of `x`, hashed as one 32-byte
   message (`gadget.rs:144-273`);
4. `(x mod ℓ)·G = S` (`lib.rs:209`).

The code does **not** require `x < ℓ`. Only the honest prover does (`lib.rs:156-158`). §4.3 explains the gap and
what it means for the swap (L1). Knowledge soundness of this relation, including extraction of `x`, is the claim put
to the auditor (Q1–Q4).

### 1.3 Public inputs

| input | size | encoding | checked by the verifier |
|---|---|---|---|
| `H` | 32 bytes | the SHA-256 digest, as DERO stores it (`contracts/htlc.bas:85,117` compare `HEX(SHA256(raw))`) | only through the circuit's 256 digest constraints (`gadget.rs:255-273`) and the transcripts (§7) |
| `S` | 32 bytes | compressed Edwards y with the x sign bit (`CompressedEdwardsY`) | decodes, torsion-free, not the identity (`lib.rs:186-189`). A non-canonical y is **accepted** (§6.5) |
| proof | 1,601 bytes | the R1CS proof ‖ `C` ‖ `A1` ‖ `A2` ‖ `z1` ‖ `z2` (`lib.rs:32-39,76-85`; layout table in the crate's `README.md:60-91`) | §8 |

Fixed parameters, not inputs: the circuit, `GENS = 2^16` (`lib.rs:29-30`), the Pedersen generators
(`bp/src/generators.rs:44-51`), and the two Merlin labels `veil.xmr.sha256-dl.v1` and `veil.xmr.sigma.v1`
(`lib.rs:143,163,191`). These are frozen so that a proof from one build verifies under another (`lib.rs:10-13`).

### 1.4 Who relies on it

Each party proves its own share before anything is locked and verifies the other's (`XMR_SWAP.md:34-50`). The
companion calls the pinned binary (`tools/xmrswap/proof.go:43-56`). It makes its own share and proof
(`tools/xmrswap/swap.go:608-629` `keygen`), and accepts the peer's `(H, S, proof)` only on exit 0 (`swap.go:631-654` `verifyPeer`). When the
peer's share appears on DERO it re-checks it before forming the joint key (`swap.go:690-706` `checkRevealed`). The contract accepts a
32-byte preimage of the stored hash (`contracts/htlc.bas:82-85` Claim, `:114-117` Refund).

## 2. Objects

| symbol | what | code |
|---|---|---|
| ℓ | order of ristretto255 and of the ed25519 basepoint | `dalek/src/constants.rs:68-80` |
| `Scalar` | integers mod ℓ, 32 bytes little-endian | `dalek/src/scalar.rs:237` (`from_bytes_mod_order`), `:250` (`_wide`), `:261` (`from_canonical_bytes`) |
| `G` | `ED25519_BASEPOINT_POINT` on edwards25519, #E = 8ℓ | `lib.rs:19,138,177,209` |
| `B` | `RISTRETTO_BASEPOINT_POINT = RistrettoPoint(ED25519_BASEPOINT_POINT)` | `dalek/src/constants.rs:66`; `bp/src/generators.rs:47` |
| `B̃` | `RistrettoPoint::hash_from_bytes::<Sha3_512>(RISTRETTO_BASEPOINT_COMPRESSED)` | `bp/src/generators.rs:48-50`; `dalek/src/ristretto.rs:749,787` |
| `G_i, H_i` | 2^16 + 2^16 Bulletproofs generators: SHAKE256("GeneratorsChain" ‖ "G"/"H" ‖ u32le(0)), 64 bytes per point, Elligator | `bp/src/generators.rs:58-70,157,186-200`; `lib.rs:49` |
| `C` | the Pedersen commitment to the key, `s·B + r·B̃` | `bp/src/r1cs/prover.rs:295-305`; `lib.rs:167` |
| `S` | `(bytes mod ℓ)·G` | `lib.rs:135-140` |

## 3. The circuit

### 3.1 The constraint system

dalek's R1CS (`bp/src/r1cs/constraint_system.rs:19-77`) has `n` multiplication gates with
`a_L[i]·a_R[i] = a_O[i]`, `m` committed values `v_j` (here `m = 1`, the key), and `q` linear constraints
`Σ W_L·a_L + W_R·a_R + W_O·a_O + W_V·v + c = 0`. The verifier flattens them with powers of a challenge `z`
(`bp/src/r1cs/verifier.rs:271-309`). There is no randomized second phase (the gadget calls no
`specify_randomized_constraints`; `tests/gadget.rs` `constraint_counts` measures 0 phase-2 constraints), so the
proof's version byte is 0 (`bp/src/r1cs/proof.rs:15-16,85-89`). The library does not check that a constraint is well
formed or satisfied as it is added (`bp/src/r1cs/prover.rs:180-184`, `bp/src/r1cs/verifier.rs:131-136`). The only
check is the proof itself.

### 3.2 Bits

A bit is either a constant `Bit::C(bool)` or a variable `Bit::V { lc, v }`: a linear combination over the wires,
plus its value on the prover's side (`gadget.rs:11-31`). A word is 32 bits, index 0 least significant
(`gadget.rs:33`). Operations with a constant operand fold to a constant or to a relabelled variable and cost nothing
(`gadget.rs:35-67`). So the circuit's shape (its matrices W_L, W_R, W_O, W_V) depends only on the public constants
`IV`, `K` and the padding, never on the witness. `H` enters only as the constant term `c` of the 256 digest
constraints (`gadget.rs:266-269`).

### 3.3 The gadgets

| gadget | code | multipliers | linear constraints | enforces |
|---|---|---|---|---|
| `alloc_bit` | `gadget.rs:95-103` | 1 (`allocate_multiplier`) | 2: `o = 0`, `l + r = 1` | `l·r = 0 ∧ l + r = 1 ⇒ l ∈ {0,1}`; the bit is `l` |
| `not` | `gadget.rs:35-40` | 0 | 0 | `1 − a` |
| `xor` (two variables) | `gadget.rs:42-54` | 1 (`multiply`) | 2: `l = a`, `r = b` (`bp/src/r1cs/prover.rs:122-126`) | `a ⊕ b = a + b − 2ab` |
| `and` (two variables) | `gadget.rs:56-67` | 1 | 2 | `a ∧ b = ab` |
| `rotr`, `shr` | `gadget.rs:78-83` | 0 | 0 | rewiring; `shr` shifts in constant zeros |
| `add_words` (w words) | `gadget.rs:105-127` | 32 + ⌈log2 w⌉ (`alloc_bit` each) | 2 per bit + 1: `Σ b_j 2^j = Σ words` | the sum mod 2^32. The bits are boolean and the sum is below 2^35 ≪ ℓ, so the field equation is an integer equation and the bits are unique |
| `decompose` | `gadget.rs:143-158` | 256 | 2·256 + 1: `Σ b_i (2^i mod ℓ) = v` | a 256-bit string congruent to `v` mod ℓ (**not unique**, §4) |
| `constrain_digest` | `gadget.rs:254-273` | 0 | 256: output bit = digest bit | the output is `H` |

### 3.4 Counts

Measured by `tests/gadget.rs` `constraint_counts` (`test.log`) and equal to the analytic count:

| phase | code | multipliers | linear constraints |
|---|---|---|---|
| decomposition | `gadget.rs:144-158` | 256 | 513 |
| message schedule, t = 16..63 | `gadget.rs:179-196` | 6,556 | 13,160 |
| compression, 64 rounds | `gadget.rs:205-244` | 30,384 | 61,024 |
| finalisation H(1) = H(0) + state | `gadget.rs:246-251` | 264 | 536 |
| digest | `gadget.rs:255-273` | 0 | 256 |
| **total** | | **37,460** | **75,489** |

Of the 37,460 multipliers, 10,728 are allocated bits (256 input bits and 312 carry-extended sums) and 26,732 are
XOR/AND gates. The circuit is padded to 2^16 = 65,536 = `GENS` (`lib.rs:29-30`;
`bp/src/r1cs/verifier.rs:373-385`), so the inner-product argument has 16 rounds (`lib.rs:32-35`).

### 3.5 How the counts arise

XOR and AND of two variable bits cost one multiplier each. Anything with a constant operand is free.
- **Schedule.** σ0 = ROTR7 ⊕ ROTR18 ⊕ SHR3 (`gadget.rs:180-186`) applies to `W[t−15]`. That word is constant for
  t = 23..30, because `W[8..15]` is padding, so σ0 is evaluated on 40 variable words at 32 + 29 multipliers each
  (the SHR3 bits 29..31 are constant zeros). σ1 = ROTR17 ⊕ ROTR19 ⊕ SHR10 (`gadget.rs:187-193`) applies to `W[t−2]`,
  constant for t = 16, 17, so it is evaluated on 46 words at 32 + 22 each. Each `W[t]` is a 4-word sum of 34 bits
  (`gadget.rs:194`). Total 40·61 + 46·54 + 48·34 = 6,556.
- **Compression.** Σ0 and Σ1 cost 64 each from round 1, since round 0 works on `IV` only (`gadget.rs:206-212,221-227`).
  Ch = g ⊕ (e ∧ (f ⊕ g)) (`gadget.rs:214-218`) costs 0 in rounds 0–1, 32 in round 2 (only the AND has two
  variables) and 96 from round 3. Maj = (a ∧ (b ⊕ c)) ⊕ (b ∧ c) (`gadget.rs:229-234`) costs 0 in rounds 0–1. In
  round 2 it costs 32 plus popcount(IV[0]) = 16, because `c = IV[0]` there and the final XOR is variable only where
  `c`'s bit is 1. From round 3 it costs 128. The sums per round are T1 (5 words, 35 bits) plus T2, e and a (2 words,
  33 bits each) (`gadget.rs:220,235,239,243`). Total 2·63·64 + (32 + 61·96) + (48 + 61·128) + 64·134 = 30,384.
- **Constraints.** Every multiplier brings two linear constraints. Add one per sum (48 + 64·4 + 8 = 312), one for
  the decomposition and 256 for the digest: 2·37,460 + 312 + 1 + 256 = 75,489.

## 4. How `s` becomes bits, and the range checks

### 4.1 The decomposition

`decompose` allocates 256 bits. Bit `i` of the witness is bit `i mod 8` of byte `i/8` of the 32 secret bytes,
little-endian as Monero stores a scalar (`gadget.rs:148`). Each bit is boolean (`alloc_bit`, `gadget.rs:149`).
One constraint ties them to the committed value, `Σ_{i<256} b_i·(2^i mod ℓ) − v = 0` (`gadget.rs:150-156`); the
coefficient `2^i mod ℓ` comes from `Scalar::from_bytes_mod_order` of a one-bit array, so it is reduced for i ≥ 253.
The prover commits `v = Scalar::from_bytes_mod_order(secret)` (`lib.rs:159,167`).

### 4.2 The range checks that exist

- Every one of the 10,728 allocated bits is boolean (`gadget.rs:99-101`).
- Every modular addition is exact: its carry-extended bits represent an integer below 2^35, far below ℓ
  (`gadget.rs:107-124`), so no wrap-around in F_ℓ can fake a sum. `tests/gadget.rs` `no_single_wire_is_free`
  flips each of the 10,728 bits alone: each flip breaks 1 to 6 constraints. A seeded sample of 512 non-boolean
  values breaks 2 to 7 (`test.log`).

### 4.3 The range check that does not exist (L1)

Nothing constrains `Σ b_i 2^i < ℓ`. Because 15ℓ < 2^256 < 16ℓ, every committed value `v` has 16 bit strings
`v + kℓ < 2^256`, and 15 for the ≈2^-124 fraction of values above 2^256 − 15ℓ. Each satisfies every constraint with
its own SHA-256 digest (`tests/gadget.rs` `the_bit_decomposition_is_not_unique`: 16 strings for random values and 0,
15 for ℓ − 1). A dishonest prover therefore proves `(H' = SHA256(LE32(s + kℓ)), S = s·G)` by building `prove`
without the refusal at `lib.rs:156-158`, and the verifier accepts. `tests/audit.rs`
`known_gap_an_unreduced_preimage_verifies` does exactly that with a full 1,601-byte proof, and the honest `prove`
refuses the same bytes.

**What it does in the swap.** The counterparty publishes `H' = SHA256(s + kℓ)`. The contract later accepts
`s + kℓ` as the 32-byte preimage (`contracts/htlc.bas:83-85` or `:115-117`: only the length and the hash are
checked). The honest companion reads it from the chain and refuses it. `checkRevealed` calls `ParseScalar`
(`tools/xmrswap/swap.go:693-697` in `checkRevealed`), which rejects any value that is not reduced (`tools/xmrswap/keys.go:72-87`).
`spendKey` would refuse it too (`swap.go:678-687` `spendKey`). So the automatic claim of the Monero output stops. The funds are
not lost: `(s + kℓ) mod ℓ = s` and `s·G = S`, so reducing the revealed bytes by hand yields the key share. Only the
party holding the other share can sweep, so there is no race. The cost is liveness, plus an operator facing a
"scalar is not reduced mod l" error at the worst moment. Either party can trigger it: Alice through her Claim
(Bob's companion stalls), Bob through his Refund (Alice's). The documented statement (§1.1) is stronger than what
the code proves.

**Ways to close it** (for after the audit; each changes either the circuit or the companion, not decided here):
(a) four linear constraints `b_252 = b_253 = b_254 = b_255 = 0`, so `x < 2^252 < ℓ` at zero multipliers. `keygen`
must then draw below 2^252, which excludes ≈2^-127.6 of the key space; the transcript labels move to `.v2`.
(b) a full `x < ℓ` comparator, about 500 multipliers, still under 2^16, so the proof stays 1,601 bytes; also `.v2`.
(c) the companion reduces a revealed value itself, after checking `SHA256(raw) = H` and `(raw mod ℓ)·G = S`. That
needs no proof change, but leaves the statement weaker than documented. Q5 asks the auditor to rank these.

## 5. The SHA-256 gadget against FIPS 180-4

One block (N = 1), because the message is exactly 256 bits.

| FIPS 180-4 | gadget |
|---|---|
| §5.1.1 padding: ℓ_M = 256, a 1 bit, k = 191 zeros, the 64-bit length | `W[8] = 0x80000000`, `W[9..14] = 0`, `W[15] = 256` (`gadget.rs:174-178`) |
| §5.2.1 parsing into big-endian 32-bit words | word `j`, bit `k` = message byte `4j + 3 − ⌊k/8⌋`, bit `k mod 8` (`gadget.rs:163-173`) |
| §5.3.3 initial hash H(0) | `IV` (`gadget.rs:139-141`) |
| §4.2.2 constants K_0..K_63 | `K` (`gadget.rs:129-138`) |
| §3.2, §4.1.2 ROTR^n, SHR^n | `rotr`, `shr` on LSB-first words: bit `i` ← bit `(i+n) mod 32`, or `i+n` and zero past 31 (`gadget.rs:78-83`) |
| §4.1.2 σ0, σ1 | `gadget.rs:180-193` |
| §4.1.2 Σ0, Σ1 | `gadget.rs:221-227`, `:206-212` |
| §4.1.2 Ch(x,y,z) = (x∧y) ⊕ (¬x∧z) | `g ⊕ (e ∧ (f ⊕ g))` (`gadget.rs:213-218`): e = 1 gives f, e = 0 gives g |
| §4.1.2 Maj(x,y,z) = (x∧y) ⊕ (x∧z) ⊕ (y∧z) | `(a ∧ (b ⊕ c)) ⊕ (b ∧ c)` (`gadget.rs:228-234`): b = c gives b, b ≠ c gives a |
| §6.2.2 step 1, `W_t = σ1(W_{t−2}) + W_{t−7} + σ0(W_{t−15}) + W_{t−16}` | `gadget.rs:179-196`; the order of addends is irrelevant |
| §6.2.2 steps 2–3: T1, T2 and the working-variable rotation | `gadget.rs:197-244` |
| §6.2.2 step 4, `H(1)_i = a..h + H(0)_i` | `gadget.rs:246-251` |
| §6.2.2 output H(1)_0 ‖ … ‖ H(1)_7, big-endian | `constrain_digest` reads the digest as big-endian words (`gadget.rs:257`) |

Evidence: `tests/gadget.rs` `gadget_matches_sha2` compares the gadget's own witness digest with `sha2` 0.10.9 and
checks that every constraint holds, on 2,573 inputs. The 525 edge inputs are zero, 2^256 − 1, ℓ − 1, ℓ, ℓ + 1,
2^252, 2^255, byte patterns, all 256 single-bit inputs, all 256 all-but-one-bit inputs, and the vector's secret; the
other 2,048 are random under a fixed seed. `a_wrong_claim_leaves_exactly_the_wrong_bits_unsatisfied` shows a wrong
digest leaves exactly `hamming(wrong, true)` constraints unsatisfied, all of them digest constraints. `word_not` is
defined but unused (`gadget.rs:75-77,245`).

## 6. Tying the hash to `s·G = S`: which groups, which scalar

This is the delicate part. Everything here is read from the code.

### 6.1 Two groups of order ℓ

- **The scalar field.** The R1CS works over F_ℓ, with curve25519-dalek's `Scalar`. The same type is used for the
  ed25519 and the ristretto255 scalars. ℓ is "the order of the Ristretto group and of the Ed25519 basepoint"
  (`dalek/src/constants.rs:68-80`).
- **The commitment group** is ristretto255. `B = RISTRETTO_BASEPOINT_POINT`, defined as
  `RistrettoPoint(ED25519_BASEPOINT_POINT)` (`dalek/src/constants.rs:66`): the Ristretto element whose Edwards
  representative is `G`, as `lib.rs:4-5` says. `B̃` is a hash-to-group point: SHA3-512 of `B`'s encoding, mapped by
  the Ristretto Elligator (`bp/src/generators.rs:48-50`; `dalek/src/ristretto.rs:749,787`). Its discrete log to `B`
  is unknown, which is what makes `C` binding. Equality of Ristretto points is equality of cosets
  (`dalek/src/ristretto.rs:828-835`), and decoding accepts only canonical encodings
  (`dalek/src/ristretto.rs:255-292`).
- **The key group** is the prime-order subgroup E[ℓ] = ⟨G⟩ of edwards25519, whose full group has order 8ℓ.
  `S = (secret mod ℓ)·G` (`lib.rs:136-139`). The verifier requires `S` to be torsion-free and not the identity
  (`lib.rs:186-189`); `is_torsion_free` is `ℓ·S == O` (`dalek/src/edwards.rs:1257-1258`).
- **The relation between them.** Sending P ∈ E[ℓ] to its Ristretto class is a group isomorphism E[ℓ] → ristretto255
  that sends G to B. Both groups are Z_ℓ-modules, so one scalar means the same thing in both. That is why a plain
  equality-of-discrete-logs proof works here with no cross-group range proof, unlike ed25519 against secp256k1 where
  the orders differ.

### 6.2 The sigma protocol

Prover (`lib.rs:173-179`): draw `k1, k2` from `OsRng`. Set `A1 = k1·B + k2·B̃` in Ristretto and `A2 = k1·G` in
Edwards. The challenge `c = Merlin("veil.xmr.sigma.v1"; H, S, C, A1, A2)` is 64 bytes reduced mod ℓ
(`lib.rs:142-152`). Respond `z1 = k1 + c·v` and `z2 = k2 + c·r`.

Verifier (`lib.rs:200-211`): recompute `c`, decode `C`, `A1` and `A2`, and check
`z1·B + z2·B̃ = A1 + c·C` (Ristretto) and `z1·G = A2 + c·S` (Edwards: exact point equality, not up to torsion).
`z1` and `z2` must be canonical (`lib.rs:95-97`).

### 6.3 Why that is sound (the argument the auditor should check)

Take two accepting transcripts with the same `(A1, A2)` and `c ≠ c'`. Then `v* = (z1 − z1')/(c − c')` and
`r* = (z2 − z2')/(c − c')` give `C = v*·B + r*·B̃`. The Edwards equations give `(c − c')·(S − v*·G) = O`. `S` and
`G` both lie in E[ℓ] (the torsion check), and `c − c'` is invertible mod ℓ, so `S = v*·G`. `A2` is not
torsion-checked, and does not need to be: the equation forces `A2 = z1·G − c·S` ∈ E[ℓ]. The R1CS proof's extractor
yields an opening `(v, γ)` of the same `C` that satisfies the circuit. `C` is the R1CS commitment `V`
(`bp/src/r1cs/verifier.rs:246-254`, used in the final check at `:484,500`), and the sigma challenge covers it
(`lib.rs:146`). By binding, `v = v*`. So `(x mod ℓ)·G = S` for the `x` the circuit hashed (§1.2).

### 6.4 Composition and zero knowledge

The two proofs are separate Fiat–Shamir proofs over the same commitment. The R1CS transcript contains `H`, `S` and
`C` but not the sigma proof, and the sigma transcript contains `H`, `S` and `C` but not the R1CS proof. Splicing
parts of two valid proofs fails (§10). The R1CS proof reveals `t_x_blinding`, which includes `wV·r` masked by fresh
`t_i` blindings (`bp/src/r1cs/prover.rs:636-652`); the sigma reveals `z2 = k2 + c·r`. That a simulator exists for the
pair is Q3.

### 6.5 Encodings

- `S` and `A2` go through `CompressedEdwardsY::decompress`, which reduces `y` mod p without checking that the
  encoding is canonical (`dalek/src/edwards.rs:194-205,209-212`; `dalek/src/backend/serial/u64/field.rs:325-338`:
  "will happily decode 2^255 − 18 to 1").
- The transcripts hash the bytes as given (`lib.rs:145,148,193`). So a non-canonical `A2` is just another
  commitment encoding a prover may choose (§15 L5).
- The companion refuses a non-canonical `S` before calling the tool (`tools/xmrswap/keys.go:156-199`), as Monero's
  `ge_frombytes_vartime` does.
- Ristretto encodings (`C`, `A1`, every R1CS point) are canonical by construction.

## 7. Transcripts and domain separation

Merlin 3.0.0 is STROBE-128 over Keccak-f[1600] (`merlin/src/strobe.rs:9-16,49-54,93-97`), started with the label
"Merlin v1.0" (`merlin/src/constants.rs`, `merlin/src/transcript.rs:83`). Its operations:
- `Transcript::new(label)` appends `("dom-sep", label)` (`merlin/src/transcript.rs:69-85`);
- `append_message(l, m)` is meta-AD(l), then meta-AD(u32le(|m|)), then AD(m) (`:96-100`);
- `append_u64` appends 8 bytes little-endian (`:155-156`);
- `challenge_bytes(l, n)` is meta-AD(l), then meta-AD(u32le(n)), then PRF(n) (`:175-179`);
- every challenge scalar is 64 PRF bytes reduced mod ℓ (`bp/src/transcript.rs:89-94`; `lib.rs:149-151`).

**T1, the R1CS transcript.** The verifier's order is below. The prover's is identical (`bp/src/r1cs/prover.rs:260,302,415-417,491-496,569-576,625-632,671-676`).

| # | operation | label | value | code |
|---|---|---|---|---|
| 1 | new | `dom-sep` | `veil.xmr.sha256-dl.v1` | `lib.rs:191` |
| 2 | append | `H` | H | `lib.rs:192` |
| 3 | append | `S` | S as given | `lib.rs:193` |
| 4 | append | `dom-sep` | `r1cs v1` | `bp/src/r1cs/verifier.rs:219-220`; `bp/src/transcript.rs:55-57` |
| 5 | append | `V` | C | `bp/src/r1cs/verifier.rs:251` |
| 6 | append | `m` | u64le(1) | `verifier.rs:361` |
| 7 | append | `A_I1`, `A_O1`, `S1` | each refused if the identity | `verifier.rs:364-366`; `bp/src/transcript.rs:75-87` |
| 8 | append | `dom-sep` | `r1cs-1phase` | `verifier.rs:317-319` |
| 9 | append | `A_I2`, `A_O2`, `S2` | the identity (32 zero bytes) | `verifier.rs:390-392`; `bp/src/r1cs/proof.rs:162-168` |
| 10 | challenge | `y`, `z` | | `verifier.rs:394-395` |
| 11 | append | `T_1`, `T_3`, `T_4`, `T_5`, `T_6` | each refused if the identity | `verifier.rs:397-401` |
| 12 | challenge | `u`, `x` | | `verifier.rs:403-404` |
| 13 | append | `t_x`, `t_x_blinding`, `e_blinding` | | `verifier.rs:406-408` |
| 14 | challenge | `w` | | `verifier.rs:410` |
| 15 | append | `dom-sep`, `n` | `ipp v1`, u64le(65536) | `bp/src/inner_product_proof.rs:213`; `bp/src/transcript.rs:50-53` |
| 16 | 16× append, challenge | `L`, `R`, then `u` | each point refused if the identity | `bp/src/inner_product_proof.rs:218-221` |

After step 16 the verifier draws its batching weight `r` from a transcript-keyed RNG that is also mixed with
`thread_rng` (`verifier.rs:458-467`). It is not part of the proof.

**T2, the sigma transcript** (`lib.rs:142-152`): new `veil.xmr.sigma.v1`; append `H`, `S`, `C`, `A1`, `A2`;
`challenge_bytes("c", 64)`.

**What is separated from what.**
- T1 from T2: by protocol label.
- R1CS from IPP within T1: by `ipp v1` and `n`.
- One-phase from two-phase circuits: by `r1cs-1phase`.
- The statement: `H` and `S` come before any prover message in both transcripts. The circuit itself is not hashed.
  It is fixed by the code and the `.v1` label, and its only dependence on `H` is the constant term of the digest
  constraints, with `H` in T1. That rules out a weak Fiat–Shamir ("frozen heart") choice of `H` after the fact
  (Q6).
- Nothing names a swap, a role, a counterparty, a network or a chain (§11).
- The label `u` is used both for the R1CS phase challenge and for the IPP rounds, at different transcript positions.
- The prefix `veil` is the project's old name, frozen with the labels (`lib.rs:10-13`).

## 8. What the verifier checks

In order, for `verify <h> <s> <proof>` (`main.rs:88-116` → `lib.rs:216-218` → `lib.rs:184-213`):

1. `h` and `s` are exactly 64 hex characters and `proof` is hex; otherwise exit 2 (`main.rs:41-45,89-100`).
2. The proof is exactly 1,601 bytes, checked before any parsing (`lib.rs:90-92`).
3. The R1CS part parses:
   - version 0 or 1, the length a multiple of 32 after the version byte (`bp/src/r1cs/proof.rs:129-148`);
   - `t_x`, `t_x_blinding` and `e_blinding` canonical (`proof.rs:180-182`: the patched lines, §9);
   - the inner-product part has an even number of points, `lg_n < 32`, and canonical `a`, `b`
     (`bp/src/inner_product_proof.rs:373-407`);
   - points are not decoded at this stage.
   A version-1 prefix of this length cannot parse (the inner-product element count becomes odd).
4. `z1` and `z2` are canonical scalars (`lib.rs:95-97,101`).
5. `S` decodes, is torsion-free and is not the identity (`lib.rs:186-189`).
6. The R1CS check (`lib.rs:191-199`):
   - rebuild T1 and the circuit with `H` as its constants;
   - `Verifier::verify` (`bp/src/r1cs/verifier.rs:340-518`) enforces the identity checks of §7;
   - the generator capacity must be at least 2^16 (`:383-385`);
   - the inner product must have n = 2^lg_n = 65,536 (`bp/src/inner_product_proof.rs:203-211`);
   - every point must decode (`optional_multiscalar_mul` yields nothing otherwise: `verifier.rs:477-509`);
   - one multiscalar product must be the identity (`verifier.rs:511-515`). It batches the t(x) commitment equation
     and the inner-product equation with the random weight `r`, so a false statement passes with probability at
     most about 1/ℓ per verification.
7. The sigma: `C`, `A1` and `A2` decode, and both equations of §6.2 hold (`lib.rs:201-211`).

Exit 0 means valid. Exit 1 means invalid, with one line on stderr (`main.rs:109-115`).

**Not checked:**
- that `C` is not the identity (harmless: `C = O` would need `log_B B̃`);
- that `A2` is torsion-free (forced by the equation);
- that `S` and `A2` are canonical encodings (§6.5);
- that `x < ℓ` (§4.3).

## 9. The local patch, and why it changes nothing

The whole diff is three lines, `bp/src/r1cs/proof.rs:180-182`
(`research/crosschain/poc/proof-xmr/patches/bulletproofs-5.0.0-r1cs-ctoption.patch`):
`Scalar::from_canonical_bytes(read32!()).ok_or(R1CSError::FormatError)?` becomes
`Option::<Scalar>::from(Scalar::from_canonical_bytes(read32!())).ok_or(R1CSError::FormatError)?`.

**Why it is needed.** curve25519-dalek 4 returns `CtOption<Scalar>` (`dalek/src/scalar.rs:261`), and `CtOption` has
no `ok_or`. The `yoloproofs` code in bulletproofs 5.0.0 does not compile without the change. Upstream marks the R1CS
API "UNSTABLE AND UNSUITABLE FOR DEPLOYMENT" (`bp/README.md:165-166`) and leaves it out of the published build
(`bp/README.md:44-47`), which is how the error survived there.

**Why it changes nothing.**
- `Option::from(CtOption)` is `Some(v)` exactly when the `CtOption` is set, and `from_canonical_bytes` sets it
  exactly for a canonical scalar. So the patched lines accept and refuse the same byte strings as upstream intends,
  and still refuse with `FormatError`.
- The same crate already makes the same conversion in its inner-product parser (`bp/src/inner_product_proof.rs:401-404`).
- No arithmetic, transcript or serialisation changes. Losing constant time here reveals only whether a public proof
  scalar is canonical.

**How it is pinned.**
- `fetch-bulletproofs.sh` downloads the crate, checks sha256 `012e2e5f…` (crates.io's own index checksum), applies
  the patch, and with `-check` compares a local copy against crate plus patch, byte for byte
  (`research/crosschain/poc/proof-xmr/fetch-bulletproofs.sh:11-14,23-30,32-44`).
- `deploy/vendor.lock:45-52` pins the crate, the patch and the tree hash.
- `Cargo.toml:25-26` points the build at that copy.
- `test.log` begins with the `-check` result: "ok: … bulletproofs 5.0.0 … plus the committed patch, and nothing else".

## 10. Test evidence

`test.log` holds the whole suite, recorded by `test-all.sh`: 23 tests, all passing, run with `--release`. The tests
existing before W-04 are `tests/proof.rs` (7), `tests/cli.rs` (2) and `tests/vector.rs` (1), described in the crate's
`README.md:186-194`. W-04 adds:

| test | what it establishes |
|---|---|
| `tests/gadget.rs` `gadget_matches_sha2` | circuit = SHA-256 on 2,573 inputs, every constraint satisfied (§5) |
| `…` `a_wrong_claim_leaves_exactly_the_wrong_bits_unsatisfied` | the digest constraints pin every output bit; the decomposition constraint pins the committed value |
| `…` `no_single_wire_is_free` | 10,728 of 10,728 single-bit flips, and 512 of 512 sampled non-boolean values, break a constraint |
| `…` `constraint_counts` | §3.4 |
| `…` `the_bit_decomposition_is_not_unique` | L1 at the circuit level (§4.3) |
| `tests/audit.rs` `rejects_every_other_h` | 261 wrong `H` values: 256 one-bit changes, the other party's `H`, the hash of the secret's hex text, of its reversed bytes, of `s + ℓ`, and zero. All rejected by the R1CS check |
| `…` `rejects_every_other_s` | 269 wrong `S` values: 256 one-bit changes, the other party's `S`, −S, S + G, 2S, (s+1)G, the identity, and S plus each of the 7 non-trivial torsion points. All rejected (decoding, torsion, or R1CS) |
| `…` `rejects_every_single_byte_tamper` | 1,601 positions × 2 masks = 3,202 tampered proofs, 0 accepted. The reasons by field are in the table below |
| `…` `rejects_every_truncation_and_extension` | every prefix and suffix shorter than 1,601 bytes, and 193 longer proofs: 3,395 in all, refused on length |
| `…` `rejects_a_valid_proof_against_the_other_partys_statement` | Alice's valid proof against Bob's `(H, S)` and every mix of the two, the reverse, and six splices of R1CS, `C` and sigma parts from different valid proofs, including two proofs of the same statement. All rejected |
| `…` `replay_a_proof_is_bound_to_its_statement_and_nothing_else` | §11 |
| `…` `known_gap_an_unreduced_preimage_verifies` | L1 as a full proof. The test asserts today's behaviour and must be inverted once the gap is closed |

The tamper sweep, by field (`test.log`). Each field has 32 bytes × 2 masks = 64 tampered proofs; the version byte has
2 and the inner-product vectors 2,048. Nothing verified:

| offset | field | first check that refused it |
|---|---|---|
| 0 | version | parse: 2 of 2 |
| 1–256 | `A_I1`, `A_O1`, `S1`, `T_1`, `T_3`–`T_6` | the R1CS check: 512 of 512 |
| 257–352 | `t_x`, `t_x_blinding`, `e_blinding` | the R1CS check 189, parse (non-canonical) 3 |
| 353–1376 | `L_0 … R_15` | the R1CS check: 2,048 of 2,048 |
| 1377–1440 | `a`, `b` | the R1CS check 126, parse 2 |
| 1441 | `C` | the R1CS check: 64 of 64 (`C` is `V` in T1) |
| 1473 | `A1` | does not decode 47, commitment equation 17 |
| 1505 | `A2` | does not decode 29, commitment equation 35 (`A2` is in T2, so the challenge changes and the first equation fails first) |
| 1537, 1569 | `z1`, `z2` | commitment equation 126, non-canonical 2 |

Wrong `S` (`test.log`): 127 fail to decode, 118 have torsion or are the identity, and 24 are valid prime-order
points that fail the R1CS check, since `S` is in T1. Wrong `H`: all 261 fail the R1CS check.

## 11. Replay across swaps

**Finding.** A proof is bound to `(H, S)` and nothing else. T1 and T2 hold fixed labels, `H`, `S` and the proof's
own messages (§7). `verify` takes no other input (`lib.rs:184`). The same bytes verify for the same `(H, S)` in any
process, any number of times. A second proof of the same statement differs in 19 of 20 fields and verifies just the
same, so a replay can be recognised by its statement but never by its bytes (`tests/audit.rs`
`replay_a_proof_is_bound_to_its_statement_and_nothing_else`).

**Why that does no harm when keys are fresh per swap:**
1. What a verifier relies on is a property of `(H, S)` alone: whoever reveals a 32-byte preimage of `H` reveals a
   value congruent mod ℓ to the discrete log of `S`. That holds whoever made the proof and whenever.
2. Someone who presents another person's statement does not know its preimage, so cannot take the exit that
   reveals it. Alice cannot Claim and Bob cannot Refund (`contracts/htlc.bas:74-129`). They can only hurt
   themselves: Punish pays the taker after `t2` (`XMR_SWAP.md:98-137`).
3. Rogue keys. Bob cannot choose `S_b = X − S_a` for an `X` he controls, because the sigma protocol is a proof of
   knowledge of `log_G S_b`. A replay only offers statements proved earlier, and none of those depends on this
   swap's fresh `S_a`.
4. Reflection: a peer sends back the other party's own statement.
   - Bob reflecting Alice's gives a joint key of 2·s_a, which only Alice can compute, and Bob cannot Refund.
   - Alice reflecting Bob's locks her XMR under 2·s_b, which Bob alone controls.
   Either way only the reflector loses. The companion does not refuse `H_peer = H_own` (`swap.go:631-654` `verifyPeer`); doing so
   would be hygiene, not safety (Q9, for the xmrswap owner).
5. Reusing one's own share across swaps is the real danger: a share revealed in swap 1 is public in swap 2. Fresh
   keys prevent it. Each swap runs `keygen` (`swap.go:608-629` `keygen`), and a register refuses a secret already used by
   another swap (`tools/xmrswap/state.go:313-331`, `swap.go:599-606` `refuseIfReused`).

Binding the transcripts to a swap identifier would be defence in depth only. It would matter if a protocol version
ever reused `(H, S)` deliberately (long-lived maker keys), or treated a proof as authentication of a party.

## 12. Reproducible build (the rest of GAP-013)

`repro.sh` records this in `repro.log`. It uses the committed sources at HEAD plus `fetch-bulletproofs.sh`, built with
rustc 1.98.1 (48a229cea 2026-09-01), cargo 1.98.1 (797e8a9bc 2026-08-05), `x86_64-unknown-linux-gnu`, Ubuntu 24.04.4,
gcc 13.3.0-6ubuntu2~24.04.1, binutils 2.42 and glibc 2.39-0ubuntu8.8, with no `RUSTFLAGS` and no `~/.cargo/config`.
`Cargo.lock` is committed and unchanged (sha256 `0ca4c4cb…`, 49 packages, lockfile v4). Every build passes
`--locked`. `bulletproofs` is a path dependency with no checksum in `Cargo.lock`; the fetch script's pin and
`deploy/vendor.lock` cover it.

| build | sha256 | result |
|---|---|---|
| the pinned binary, `xmrswap.ProverSHA256` (`tools/xmrswap/pin.go:24`) | `4d3bde0a…c20ac444`, 1,340,392 bytes | |
| recipe 1: `build.sh`'s `cargo build --release`, in this checkout, fresh target directory | `4d3bde0a…c20ac444` | **byte-identical to the pin** |
| recipe 1 from two other directories | two different hashes (`repro.log`) | differ from each other and from the pin |
| recipe 2a: cargo `trim-paths` | | refused: not stable in cargo 1.98.1 |
| recipe 2b: `RUSTFLAGS=--remap-path-prefix` | no build path left, but two different hashes | differ |
| **recipe 3: `build-reproducible.sh`**, from both directories | **`7293e9c97e0330966499fff397a0488ef40d38f65da49423429c8c548c83d39d`**, 1,340,640 bytes | **byte-identical** |

**Why recipe 1 differs across directories.**
- The patched `bulletproofs` is a path dependency outside the crate's directory (`Cargo.toml:25-26`). Cargo hands rustc
  its absolute path, and rustc keeps that path in panic-location strings.
- The same path is hashed into the crate disambiguators mangled into every symbol, for `bulletproofs` and so for
  `proof_xmr`. Registry crates keep theirs: `repro.log` lists the disambiguators of both builds.
- Registry crates also embed `$CARGO_HOME`'s absolute path, which contains the user's home directory.
- The target directory is not embedded: a fresh one reproduces the pin.
- `trim-paths` is not stable in this cargo. `--remap-path-prefix` removes the strings but not the location from the
  disambiguators. In recipe 2b curve25519_dalek's disambiguator is identical in both builds while those of
  `bulletproofs` and `proof_xmr` differ, so the differing `RUSTFLAGS` are not the cause (`repro.log`).
- So the recipe fixes the two paths instead. `build-reproducible.sh` copies the committed tree to
  `/tmp/proof-xmr-build`, fetches and patches `bulletproofs` there, and builds with `CARGO_HOME=/tmp/proof-xmr-cargo`
  and `--locked`.

**The pin, for the coordinator.** tools/xmrswap is not edited here.
- The current pin `4d3bde0a…` is correct: it is the release build of the committed sources. But only a machine with
  the same checkout path and home directory can reproduce it, since both are embedded.
- The reproducible pin is `7293e9c97e0330966499fff397a0488ef40d38f65da49423429c8c548c83d39d` (1,340,640 bytes). Its
  recipe is `research/crosschain/poc/proof-xmr/build-reproducible.sh` on the toolchain above, and the binary lands at
  `$CARGO_TARGET_DIR/release/proof-xmr` (default `~/target/proof-xmr-reproducible`).
- The two binaries run the same code. The reproducible one verifies `vector.txt`, and a proof made by each verifies
  under the other (`repro.log`).
- Remaining dependencies of the hash: the toolchain, the system linker, and glibc, whose C runtime objects are linked
  into the binary.
- `pin.go:21-22`'s "The build is not reproducible yet" would then name the recipe. `-prover` would point at its output.

## 13. Timings (MT-M03)

`bench.sh` recorded this in `bench.log` on 2026-09-11. It ran the pinned release binary (`4d3bde0a…`, rustc 1.98.1)
25 times each way, as the companion runs it:
- `keygen`, then `prove -` with the secret on stdin, then `verify` of that proof;
- each call a fresh process that rebuilds the 2^16 generators (`main.rs:68-70,101-103`);
- in-process times from `PROOF_XMR_TIMING=1` (`main.rs:81-83,106-108`), and each process's wall clock measured
  around it.

The machine is a Ryzen 7 5700X (8 cores, 16 threads, 27 GiB) running WSL2 Ubuntu 24.04.4. It was not idle. Another
engineer's monerod sync (about 3.6 cores), a zebrad and two monero-wallet-rpc processes ran throughout, and the
one-minute load average was 7.5 before and 4.7 after. Proving and verifying are single-threaded, so the load costs
mainly shared cores. These are working-conditions figures, not best-case ones.

| step | n | min | median | p95 | max |
|---|---|---|---|---|---|
| generator setup (every call) | 50 | 1.091 s | 1.254 s | 1.585 s | 1.908 s |
| prove, in process | 25 | 7.098 s | 7.695 s | 8.603 s | 8.819 s |
| verify, in process | 25 | 0.587 s | 0.683 s | 0.802 s | 1.394 s |
| **prove, whole process** | 25 | 8.249 s | **9.008 s** | **9.850 s** | 10.750 s |
| **verify, whole process** | 25 | 1.734 s | **1.922 s** | **2.310 s** | 3.083 s |

p95 is the nearest-rank 24th of 25. The maxima come from runs 22 and 23, during a burst of the background load
(`bench.log`).

**MT-M03.** The medians are below the figures the docs carry, ≈10.1 s to prove and ≈2.3 s to verify end to end
(`docs/MAINNET_TODO.md:293`; `XMR_SWAP.md:47-49`), and the p95s are at them. The 2026-09-08 `run.log` single run
(generators 1.61 s, prove 8.51 s, verify 0.74 s) is within these ranges. Generator setup is about two thirds of a
verify (L8): the tool keeps no state between calls, by design (the crate's `README.md:54-55`). The companion allows
5 minutes per call (`tools/xmrswap/proof.go:128`), about 30 times the p95.

## 14. A second verifier (MT-G04): assessment

**What exists.** No implementation outside dalek's code can verify these proof bytes. Checked on 2026-09-11 from the
known implementations and a web search:
- Every Bulletproofs R1CS over ristretto255 with Merlin transcripts is dalek's code or a fork or wrapper of it:
  dalek-cryptography/bulletproofs, zkcrypto/bulletproofs (the 5.0.0 used here), tari-project/bulletproofs,
  suterusu-team/bulletproofs, lovesh/bulletproofs, renegade-fi/mpc-bulletproof (a wrapper).
- Monero's Bulletproofs(+) are range proofs with Monero's own transcript, and have no R1CS.
- Go and Python have ristretto255 (filippo.io/edwards25519 plus gtank/ristretto255) and Merlin (gtank/merlin), but no
  R1CS verifier.

**What a second verifier must contain.**
- Keccak-f[1600], STROBE-128 and Merlin (§7);
- ristretto255 decoding, encoding and the Elligator map (RFC 9496);
- the generator chains (SHAKE256) and `B̃` (SHA3-512);
- the gadget's constraint system, reproduced exactly: dalek's constraint order, multiplier indexing and constant
  folding (§3), because the powers of `z` depend on the order;
- flattening, the inner-product verification scalars (`bp/src/inner_product_proof.rs:198-252`) and the batched check
  (`bp/src/r1cs/verifier.rs:412-515`);
- the sigma protocol and the parser.
In Go on `filippo.io/edwards25519`, which tools/xmrswap already uses (`tools/xmrswap/keys.go:18-48`), that is about
1,300 to 1,500 lines, or one to two engineer-weeks with the vector debugging. The multiscalar product over 131,118
points (`bp/src/r1cs/verifier.rs:477-507`: 6 + 1 + 5 + 2 + 2·65,536 + 2·16) would take seconds; that is an
estimate, not a measurement.

**What it would catch, and what it would not.**
- It would catch arithmetic, encoding and transcript bugs in curve25519-dalek, merlin, bulletproofs' multiscalar
  product and parser, and the patch.
- It would expose divergences such as §6.5: `filippo.io/edwards25519` refuses a non-canonical `y`, which dalek
  accepts, so the two would disagree on proofs with a non-canonical `A2`. Deciding which verdict rules would be part
  of MT-G04.
- It would catch nothing in the circuit or the protocol. To verify the same bytes it must reproduce the constraint
  system exactly, L1 included, and dalek's verification equation. A flaw in either is copied, not found. Those are
  the largest risks, and they are the auditor's.

**Decision: not built here.** Written in this pass, by this package's author, from the same code, it would be
independent in its libraries but not in its reading, which is the independence MT-G04 is for. It would also be
redone if the audit changes the circuit, and closing L1 does change it.

**Recommendation.** After MT-G03's findings, have someone other than this package's author write it clean-room from
§3–§8 of this package and the upstream protocol notes (`bp/docs/notes-r1cs.md`, `bp/docs/cs-proof.md`,
`bp/docs/notes-ipp.md`).
- Acceptance: agree with `proof-xmr` on `vector.txt`; reject all 3,202 tampered variants, 261 wrong `H` values,
  269 wrong `S` values and 14 cross or spliced cases, generated as `tests/audit.rs` does from a fixed proof; and agree
  on the L1 vector while the circuit is `.v1`.

## 15. Known limitations

| # | limitation | severity for the swap | code |
|---|---|---|---|
| L1 | a preimage that is not a reduced scalar (s + kℓ, up to 15 per key) verifies | liveness: the honest companion refuses the revealed value, and a manual reduction recovers the key. The statement is weaker than documented | §4.3 |
| L2 | no swap binding in either transcript | none while keys are fresh per swap | §11 |
| L3 | the R1CS back end is upstream's unaudited `yoloproofs` API; constraints are never validated as added | the circuit's soundness rests on this package and the audit | `bp/README.md:165-166`; `bp/src/r1cs/prover.rs:180-184` |
| L4 | the sigma nonces `k1, k2` and the blinding `r` come from `OsRng` alone, not hedged with the witness as the R1CS prover's RNG is. Two proofs of one `s` with a repeated `k1` give `s = (z1 − z1')/(c − c')` | a broken RNG leaks the share before the swap ends, and the counterparty takes both sides | `lib.rs:161,174-175`; compare `bp/src/r1cs/prover.rs:432-442` |
| L5 | non-canonical Edwards encodings of `S` and `A2` are accepted | none known: the companion refuses a non-canonical `S`, and an `A2` variant is the prover's own choice | §6.5 |
| L6 | proofs are randomised, so a statement has many valid proofs | replay detection must key on `(H, S)` | §11 |
| L7 | witness generation branches on secret bits (`gadget.rs:42-67,106-127`); only the proving multiscalar products are constant-time (`bp/src/r1cs/prover.rs:462-488`). The secret's copies in `lib.rs` and `main.rs` are not zeroised (the prover's own are: `bp/src/r1cs/prover.rs:74-96`) | a local process, and the share becomes public at reveal anyway | as cited |
| L8 | every call rebuilds 2^16 generators | about 1.6 s per call | `main.rs:69,102`; §13 |
| L9 | the pinned build reproduces only at its original paths | closed by `build-reproducible.sh` once the pin is replaced | §12 |
| L10 | verification is probabilistic (the batching weight) | error ≤ about 1/ℓ | `bp/src/r1cs/verifier.rs:458-515` |
| L11 | the circuit hashes exactly 32 bytes | consistent with the contract's `STRLEN(raw) == 32` | `gadget.rs:174-178`; `contracts/htlc.bas:83-85,115-117` |

## 16. Questions for the auditor

1. Is the relation of §1.2 what `verify` enforces, with `x` extractable (knowledge soundness), given dalek's R1CS
   protocol as implemented in `bp/src/r1cs/{prover,verifier}.rs`?
2. Is dalek's one-phase R1CS verification (`verifier.rs:340-518`) sound as implemented? That covers the identity
   checks, the padding, the `u` factors, the `wc` and `δ` terms, and the batching with `r`.
3. Is the composition of the R1CS proof and the sigma protocol over one commitment `C` zero-knowledge and sound
   (§6.3–§6.4), given that each transcript omits the other proof?
4. Is the cross-group argument of §6.1–§6.3 correct as the code does it: the same scalar in ristretto255 and in
   E[ℓ], `S` torsion-checked, `A2` not?
5. L1: confirm the gap and its swap impact (§4.3), and rank the three remedies, including whether a `.v2` circuit
   with `b_252..b_255 = 0` is acceptable.
6. Is the Fiat–Shamir binding complete? `H` and `S` come first in T1, `C` enters as `V`, and the circuit is fixed by
   code rather than hashed.
7. Does each gadget of §3.3 constrain what it claims? Especially `add_words`' carry bounds, `decompose`, and the
   constant folding in `xor` and `and`.
8. Is the SHA-256 mapping of §5 complete, and the 32-byte padding right?
9. Replay (§11): do you agree it is harmless with fresh keys? Should T1 and T2 bind a swap identifier regardless,
   and should the companion refuse a reflected statement?
10. L4: should the sigma nonces be derived from a transcript RNG rekeyed with the witness?
11. §6.5: should `verify` refuse non-canonical `S` and `A2` encodings itself?
12. Is the patch (§9) semantically neutral, and does anything else in the `yoloproofs` code fail to compile, or
    compile with changed meaning, against curve25519-dalek 4.1.3?
13. Are the generator derivation and `B̃` (§2) sound: unknown discrete logs, no overlap between the G, H and B̃
    chains?
14. Does anything in the CLI contract (`main.rs`) let a malformed invocation read as "valid"? Exit 0 is the only
    accept.
15. Is 2^16 generators exactly right, and is a proof with a different `lg_n` refused before any work (§8)?
16. MT-G04 (§14): what would you require of a second verifier?

## 17. Reproducing everything

```
bash <repo>/research/crosschain/poc/proof-xmr/test-all.sh            # test.log: the 23 tests, about 10 min
bash <repo>/research/crosschain/poc/proof-xmr/repro.sh               # repro.log: the builds of §12
bash <repo>/research/crosschain/poc/proof-xmr/build-reproducible.sh  # the pinnable binary
bash <repo>/research/crosschain/poc/proof-xmr/bench.sh               # bench.log: §13, on an idle machine
```

Each runs inside WSL with `REPO` set to the repository's WSL path. From Git Bash, prefix
`MSYS_NO_PATHCONV=1 wsl.exe -e bash -lc 'export REPO=<repo>; …'`, as `record.sh` shows. `PROOF_XMR_SEED` changes the
seed of the random inputs and masks; the fixed default makes a failure replayable.
