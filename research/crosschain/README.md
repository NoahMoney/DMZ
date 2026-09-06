# Cross-chain research: what was resolved, decided, corrected and reviewed

The conclusions. The technical research behind them is in `RESEARCH.md`, the bibliography in `SOURCES.md`, the prototypes in `poc/`.


---

# RESOLVED

## RESOLVED — what the experiments settled (Phase 5, 2026-09-06)

Each item names the question it closes, the artefact, the measurement, and what stays open.

### 1. The sha256-preimage ↔ ed25519-key-share proof exists now (decides Monero)

`poc/proof-xmr` (Rust, Bulletproofs R1CS over ristretto255 + a Chaum–Pedersen sigma protocol, no trusted setup).
Statement: "I know 32 bytes `s` with `SHA256(s) = H` and `s·G = S` on ed25519." One Pedersen commitment binds both
halves. Measured on this machine (16 cores, release build):

| metric | value |
|---|---|
| multiplication gates | 37,460 |
| prove | 14.8 s |
| verify | 1.15 s |
| proof size | 1,601 bytes (R1CS 1,441 + commitment 32 + sigma 128) |
| negative tests | wrong H, wrong S, tampered sigma, other secret: all rejected |
| DERO check | `HEX(SHA256(HEXDECODE(secret))) == H` with the same bytes: true |
| Monero check | preimage read mod ℓ reproduces S: true |

Byte convention: the preimage is the little-endian 32-byte scalar. A prover may hash `s + ℓ` instead of `s`; the
Monero side reduces mod ℓ, so this is harmless and is documented rather than constrained.
Open: the dalek R1CS module is unaudited ("yoloproofs"); the vendored copy has a 3-line compile fix
(`poc/vendor/bulletproofs`). The full swap (gate secret, punish path, companion, Monero sweep) is not built.

### 2. The same proof on Pallas exists (decides shielded Zcash key shares)

`poc/vendor/bp-pallas` is the dalek design ported to the Pallas curve (types swapped, Pippenger MSM, parallel
generator folding), and `poc/proof-zec` reuses the identical SHA-256 gadget. Statement: "`SHA256(s) = H` and
`s·G_spendauth = ak` on Pallas." Measured:

| metric | value |
|---|---|
| gates | 37,460 (same gadget) |
| prove | 12.2 s (85.6 s before the parallel fold) |
| verify | 0.47 s |
| proof size | 1,601 bytes |
| joint key | `ak_a + ak_b == ak` true; a full viewing key built from the joint `ak` plus ordinary `nk`, `rivk` yields an Orchard address |
| spend authority | a randomized RedPallas signature made with `ask_a + ask_b` verifies under the joint `ak`; a share alone is rejected |
| Orchard sign rule | when the joint `ak` has its top bit set both shares are negated before anything is hashed |

Open: spending from a joint key needs a transaction builder that accepts an external spend authorizer; the
orchard crate's `Bundle::apply_signatures` takes signatures, so this is plumbing, not research.

### 3. Transparent Zcash is an ordinary HTLC, verified under Zcash's consensus interpreter

`poc/zec-t-htlc`: redeem script `IF SHA256 <H> EQUALVERIFY P2PKH(taker) ELSE <lt> CLTV DROP P2PKH(maker) ENDIF`
(94 bytes), P2SH address, real secp256k1 signatures, evaluated with `zcash_script` 0.5.2 (pure Rust) under P2SH +
CLTV + strict-encoding flags. Claim with the right secret passes; wrong secret, wrong key, refund before the lock
time, refund with a final input, refund with the taker's key are all rejected; the maker's refund after the lock
time passes. CLTV is enforced from Zcash's genesis (zcash_script `opcode/mod.rs` cites the protocol spec's BIP-65
adoption). The stand-in sighash is the only gap: a real one comes from `signature_hash` with
`SignableInput::Transparent { script_code: redeem, .. }`, standard wallet code.

### 4. Threshold custody for Orchard spend keys works with Zcash's own library

`poc/frost-zec`: reddsa `frost` feature (ZIP 312 re-randomized FROST), 2-of-3, dealer keygen for the prototype.
The group verifying key is a valid Orchard `ak` (top bit clear, redrawn otherwise); a full viewing key built around
it derives per-index deposit addresses; two signers produce a randomized RedPallas signature that verifies under
the randomized group `ak`; one signer alone cannot. Result recorded in `poc/frost-zec/run.log`.
Open: DKG instead of a dealer (frost-core has it); a negation round if the DKG output has the sign bit set.

### 5. The DERO side of a federation wrapper is buildable and tested

`contracts/vault.bas` v2 (13,035 B, install storage gas 13,295) with `tests/vault_test.go`: tuple-keyed k-of-n mint
votes scoped by slash epoch, once-only deposit ids, supply cap with epoch-scoped k-of-n raise bounded by 3 × bonds,
minimum bonds before seal, k-of-n slash that voids the removed signer's votes, `Halt(want)` in 2000-block buckets,
burn-to-withdraw with a minimum and a federation fee, releases bound to the foreign txid, per-user deposit indexes
with versioned k-of-n attested addresses. Review in
`research/wrapper/README.md (security review)`. The DVM keeps no own-asset balance for a contract, so burned units vanish and the supply
counter is the invariant.

### 6. What SHLD does, from its own materials

SHLD (shld.fun) is an operator-held ledger: a dedicated shielded ZEC deposit address per wallet, a server that
detects deposits and credits an internal ledger at the bonding-curve price, a signed "holding note" as the user's
balance, and sells paid from the curve reserve to a withdrawal address. The market is public and the buyers are
shielded because they pay from shielded balances. Settlement is bounded by Zcash confirmations and the operator's
payouts. Borrowed: the per-user deposit address (done with attested indexes and diversified addresses, no memo),
and "market public, holders shielded", which DERO gives natively. Not borrowed: the operator ledger.

### 7. Still unresolved

- Monero threshold custody in production: native multisig is shipped but flagged experimental; FROSTLASS is
  pre-mainnet. The wrapper can start with native multisig behind low caps.
- Measured block-interval data for timelock margins (still constants and secondary sources).
- Whisper as a negotiation channel (unmeasured); the Zcash memo field and the DERO offer board are the
  alternatives for the swap path.
- End-to-end runs on Monero stagenet / Zcash testnet / Solana devnet (nothing blocks them; they are work).

### 8. Wrap and unwrap ran end to end on the DERO simulator (added after the first run)

`tools/vaultsigner` (signer daemon with a backend interface; `sim` backend) and `tools/simvault` (driver). The wrap
half is in `poc/vault-e2e.log`; that log ends on the first burn attempt failing (fresh-asset lag). The completed burn
and the two `Released` votes are recorded in the simulator log `.sim/sim.log` (transactions selected for mining at
09:50:05, 09:50:26 and 09:50:51) and in `docs/history/HISTORY.md (day log)`; they were run from the operator console, not the script: vault `a88778fc…` installed (storage gas 10,057), three signers added and sealed, bonds posted,
user registered as index 0, two `Attest` votes fixed `sim:0`, a simulated 250,000-unit deposit with three
confirmations was minted by two `Mint` votes (`0c11e284…`, `03d85ef0…`), the user burned 40,000 (`fc6085e4…`),
and two `Released` votes (`d5b48af1…`, `a776ddb1…`) closed the withdrawal with the foreign txid. Supply 210,000.
Two operational findings: `ADDRESS_STRING` yields a mainnet-prefixed string on the simulator (accepted by
`ADDRESS_RAW`), and a freshly minted asset cannot be spent for about two blocks.

### 9. The v2 vault ran end to end on the simulator (2026-09-06)

Vault `fd078359…` (v2, 2-of-3, cap 100,000, minbond 20,000, minburn 1,000, wfee 100): three signers bonded before
`Seal`, user registered as index 0, two `Attest` votes fixed `sim:0`, two simulated 25,000-unit deposits minted by
two `Mint(j, id, amount)` votes each (supply 50,000), the user burned 4,000 (`cdc00660…`), and two
`Released(j, ftx)` votes closed withdrawal 0 at 3,900 (the 100-unit federation fee stayed in `feepool`) against
foreign txid `1376f9c3…`; final supply 46,000, status 1. Logs: `poc/vault-e2e-v2.log` (wrap; its burn attempt hit
the fresh-asset lag), `poc/vault-e2e-v2-burn.log` (second deposit, burn, release). The script needs a two-block
wait between a mint and a burn.

### 10. Contract calls at ring size 16 and 32 work; SIGNER() is blank (2026-09-06)

`tools/ringprobe` (log `research/ringprobe.log`): the DVM receives DEROVALUE() and string arguments at any ring,
pays a destination named in the call, and hands the contract 33 zero bytes as SIGNER(). Gas is identical. The
signer of a ring-2 call is computable by any observer (`Extract_signer`). Design note: `research/RING_PRIVACY.md`.

Sizes (2026-09-06, `research/ringprobe-sizes.log`): 1,848 bytes at ring 2, 2,720 at 8, 3,336 at 16, 4,240 at 32, 5,784 at 64; about 63 bytes per extra member; the node reports a signer only for the ring-2 transaction.


---

# DECISION

## DECISION — what Veil builds for cross-chain (Phase 6, 2026-09-06)

### The decision

Veil ships two cross-chain layers with different trust and different speed, and says so on the screen.

1. **Wrapped assets through a bonded k-of-n federation (the wrapper).** wXMR, wZEC and wSOL are minted on DERO
   one-for-one against deposits the signers have seen, and burned to withdraw. Markets quoted in wrapped assets run
   on the existing curves and pools with instant fills and pooled liquidity. This is the layer that makes
   COIN/XMR feel like a Solana memecoin. It is trusted: k colluding signers can steal the reserve. The mitigations
   are threshold custody, published viewing keys for proof of reserves, DERO bonds slashable by the others, a
   supply cap tied to the bonds, and a halt.
2. **Atomic swaps as the exit hatch.** Two-party hash-locked swaps between DERO assets and SOL, transparent ZEC,
   EVM assets and Bitcoin-family coins, with no custody and no oracle, at minutes per fill. Monero and shielded
   Zcash swaps use the key-share proofs prototyped this week. Slow, but nobody can be trapped by the federation.

The operator's requirement of instant fills with pooled XMR liquidity forces layer 1. The requirement that the
platform be safe forces layer 2 to exist alongside it.

### Why not the alternatives

| alternative | why not |
|---|---|
| swaps only (the original hypothesis) | no pooled liquidity, minutes per fill, both parties online: not the product asked for |
| operator custody (fomo, SHLD) | one company's hot wallet; the same trust as the federation with none of the mitigations |
| collateralised synthetics (Haven) | needs a price feed the DVM cannot verify, so it is a federation with an extra failure mode; the reference implementation died of it |
| trustless bridge | impossible until DVM-BASIC gains ed25519 / secp256k1 verification |

### What is decided in detail

- **Custody primitives.** Zcash: FROST for RedPallas from the reddsa crate (audited, ZIP 312), demonstrated
  2-of-3. Monero: native multisig behind low caps first, FROSTLASS when it ships. Solana: FROST ed25519
  (ZF frost-ed25519) over a vault PDA.
- **Deposit attribution.** Per-user index on DERO (`Register`), a diversified address (Zcash) or subaddress
  (Monero) derived by the signers from the shared viewing key and attested on chain with k votes. No memos.
- **Confirmation policy.** Monero 10 blocks (20 for large deposits, the observed 18-block reorg), Zcash 10
  blocks, Solana 32 slots (finalized). Encoded in the signer daemon, not the contract.
- **Bounded loss rule.** `cap ≤ 3 × sum of bonds` at launch, raised only by k votes as track record grows.
- **Gas for foreign-only users.** A deposit also delivers a DERO sliver for fees, paid from a 0.5 % deposit fee.
- **Exit hatch.** `swap.bas` stays deployed; the SOL and transparent-ZEC legs are the first to get a companion.
- **Quote assets.** Markets may be quoted in DERO or any wrapped asset; the launch flow shows the quote.
- **Disclosure.** Every wrapped balance and every wrapped-quoted market carries a "federation-backed" mark with
  the signer set, the cap, the bonds and the proof-of-reserves link one tap away.

### Trust statement for users (the text the app will show)

"wXMR is backed one-for-one by Monero held by a 3-of-5 signer federation. You can verify the reserve with the
published view key. The signers post DERO bonds that the others can seize. They could still collude to steal the
reserve. If you do not want that trust, use an atomic swap: slower, no custody."

### Sequence

1. Signer daemon with a simulated foreign chain; end-to-end wrap/unwrap on the DERO simulator.
2. Wrap screen in the app; wrapped-quoted markets.
3. Zcash backend (FROST + Orchard scanning) on testnet; Monero backend (multisig) on stagenet.
4. Security round 2 on the contract (rate limits, withdrawal delay, rotation, unbonding, fuzzing).
5. Solana exit hatch; then the Monero swap once the proof is integrated.


---

# VERDICT CHANGES

## Verdict changes (running log)

| date | item | old verdict | new verdict | evidence that moved it |
|---|---|---|---|---|
| 2026-09-06 | Q4 DERO ↔ XMR protocol | draft: Alice may claim after a delay `h0 + D` | claim requires a second preimage `k_b` released by Bob after the Monero lock confirms; delay gate removed | Re-derivation against COMIT `alice/state.rs` / `bob/state.rs`: the Bitcoin redeem needs Bob's encrypted signature, handed over only after the XMR lock is confirmed; a plain hash lock had no equivalent gate, so the XMR side could take the DERO without locking XMR. |
| 2026-09-06 | Q4 DERO ↔ XMR confidence | M | L | The blocking proof (sha256 preimage ∧ ed25519 discrete log) has no implementation for this pairing; cost estimates are constraint counts from public circuits, not measurements; the protocol needed a correction in review. |
| 2026-09-06 | Q4 DERO leg privacy | draft implied the ring hides participants | maker, taker, amount, hash and secret are public in contract storage; only unrelated balances are encrypted | Contract storage keys read via `DERO.GetSC`; `SIGNER()` requires ring size 2 (`transaction_execute.go:430`). |
| 2026-09-06 | HTLC POC | "built and tested" | "built, tested and run on the simulator with a 32-byte preimage" — after fixing that the first version hashed a text string | `swap.bas` now decodes 64 hex chars with `HEXDECODE` and hashes raw bytes; harness tests and `poc/simswap-run.log`. |
| 2026-09-06 | Q5 market structure | Yes / H | Yes / M | Bond economics assume a volatility figure; slashing covers DERO-observable events only; the merged escrow+board contract is specified, not built; negotiation channel unmeasured. |
| 2026-09-06 | Q6 federation evidence | "FROST ed25519 (audited)" implying a signature could be produced here | mechanism unchanged (M); claim about running threshold code here withdrawn | Experiments 4 and 5 did not run: no C++ toolchain on this machine (`link.exe` is Git's; VS Build Tools without C++). Serai pre-mainnet. |
| 2026-09-06 | Q4 timelocks | "derived numbers" | design margins from source constants and secondary sources; not a measured basis for a spec | DERO `BLOCK_TIME`/`STABLE_LIMIT` from `config.go`; Monero figures from monerica / monero.how / StealthEX write-ups. |
| 2026-09-06 | whisper as negotiation channel | "partially usable" | unmeasured, L | Experiment 3 failed twice (mainnet-only address handling, then a daemon lookup); nothing measured. |
| 2026-09-06 | `RESEARCH.md (Feasibility matrix)` XMR cell | "Yes, with a client-side cross-curve consistency proof" | "Not on DERO today (needs the proof)" | Consistency with the report after the review. |
| 2026-09-06 | v1 hypothesis (for Phase 6) | Layer B = XMR + SOL | Layer B = SOL now; XMR deferred until the proof is prototyped | Everything above. |
| 2026-09-06 (later) | Q4 DERO ↔ XMR | "not shippable today", L | the blocking proof is prototyped and measured (`poc/proof-xmr`: 37,460 gates, 14.8 s prove, 1.15 s verify, 1.6 KB); confidence M; still needs the escrow with the gate secret, the companion and a stagenet run | `poc/proof-xmr/run.log` |
| 2026-09-06 (later) | Q4 ZEC shielded | "research item", L | the Pallas variant of the same proof works (`poc/proof-zec`: 12.2 s / 0.47 s) and a joint Orchard spend authority signs; confidence M; needs a builder with an external authorizer | `poc/proof-zec/run.log` |
| 2026-09-06 (later) | Q4 ZEC transparent | Yes, H (construction) | Yes, H (verified): HTLC evaluated under `zcash_script` 0.5.2 with P2SH + CLTV, all abort paths correct | `poc/zec-t-htlc/run.log` |
| 2026-09-06 (later) | Q6 federation, Zcash custody | mechanism M, production L | 2-of-3 re-randomized FROST RedPallas signature verifies under a valid Orchard `ak` using Zcash's reddsa crate; Zcash custody M→H for the primitive; Monero stays L (multisig experimental / FROSTLASS pre-mainnet) | `poc/frost-zec/run.log` |
| 2026-09-06 (later) | product decision | Layer B = swaps (SOL first) | Layer B = federation wrapper (instant, pooled, trusted) with swaps as the exit hatch; operator requirement of instant fills with pooled XMR/ZEC liquidity | `DECISION.md` |
| 2026-09-06 (Phase A wrapper review) | Q6 Zcash custody primitive | H (FROST demonstrated) | M: the rerandomized FROST path is excluded from the NCC audit and marked experimental by reddsa; ZIP 312 is a draft | ZF FROST README audit scope; reddsa README; ZIP 312 status |
| 2026-09-06 (Phase A wrapper review) | Monero custody at launch | "native multisig behind low caps" | unchanged in substance, downgraded in wording: multisig is a trusted-signers tool (2021 disclosure, experimental flag); FCMP++ will force a key migration; ZEC-first launch is the honest ordering | getmonero.org 2021-12-06; docs.getmonero.org; CCS !448 |
| 2026-09-06 (Phase A wrapper review) | wrapper mechanism verdict | "Yes, with mitigations" (DECISION.md) | **Yes-with-changes** (nine listed, none optional), M for ZEC, L for XMR | research/wrapper/DESIGN.md (Mechanism review) |


---

# WRONG PREMISES

## Wrong premises (v1, Phases 0–3)

Things the prompt, the requester, or this study's own earlier drafts assumed that the sources do not support.

1. **"mycelium is for DAO signers."** False. From code: a no-relay E2E messenger over transaction payload fields
   (DERO payload, EVM calldata / mailbox contract, Solana mailbox program, 8-byte Monero payment id). No multisig,
   threshold or approval logic anywhere in `internal/` or the contracts; "DAO" appears only in `docs/USE_CASES.md`
   and `docs/SOLANA_AND_USECASES.md` as a use case (private coordination before a vote). Its only signer is the
   Solana keypair paying for mailbox deliveries.
2. **"Locate and read the derohe-rs Rust port it references."** `github.com/liqdmetal/derohe-rs` returns 404
   (2026-09-06); nothing by that name is indexed. mycelium shells out to a `compost-peer` binary from it
   (`internal/peer/peer.go`); the claims about that port are unverifiable.
3. **mycelium's XMR support** is an 8-byte payment-id knock, mock-verified only (`internal/xmr/xmr.go`).
4. **"111-byte payload."** derohe: `PAYLOAD0_LIMIT = 144` bytes (`transaction/transaction.go:63`), enforced by the
   wallet's `CheckPack` (`walletapi/wallet_transfer.go:156`); mycelium's 111 / ~80–95 usable characters is after the
   sender byte and CBOR framing. A swap setup does not fit either way.
5. **"The DERO HTLC POC needs to be built."** It existed before the study (`poc/swap.bas`, tests green) and was run on
   the live simulator in experiment 2.
6. **"Cross-chain direct messaging is impossible" (mycelium)** is a statement about its transport (one payload
   encrypted to keys on two curves), not a cryptographic impossibility; cross-chain identity proofs are routine in
   swap protocols. mycelium has not built one (ROADMAP #7).
7. **"DERO's ring size hides the HTLC claimant."** No: `SIGNER()` requires ring size 2; the maker, taker, amount, hash
   and revealed secret are all public in contract storage. Only unrelated balances stay encrypted.
8. **"fomo lets you buy any chain's token in one click without bridging."** fomo runs one user balance, a fiat
   on-ramp (Crossmint) and its own routing across Solana, Base, BNB, Monad, Ethereum and Robinhood Chain; that is an
   aggregator with custody, not a trustless mechanism.
9. **This study's own first HTLC hashed a text string, not 32 raw bytes.** The first `swap.bas` compared
   `HEX(SHA256(secret_string))`; a Monero or Solana scalar is 32 raw bytes and would not round-trip as a JSON string.
   Fixed on 2026-09-06: the secret is 64 hex characters, decoded with `HEXDECODE` and hashed raw; tests updated;
   the simulator run used a random 32-byte secret. (Also: the first version's line numbers 251/252 were rejected by
   the DVM parser, which requires ascending line numbers within a function.)
10. **"mycelium whisper can be measured on the simulator."** Its `whisper send` treats anything not starting with
    `dero1` as a name and resolves it through a daemon (`cmd/mycelium/main.go:616`); simulator addresses start with
    `deto1`. After patching the prefix check, a daemon lookup still failed with `leaf not found` for the simulator
    address. Experiment 3 is inconclusive; it needs a testnet/mainnet wallet pair or a debugging session in
    mycelium's daemon client.
11. **"Run the FROST / Farcaster tests in the container."** This machine has Rust (msvc target) but no C++ compiler
    (`link.exe` on PATH is Git's; VS Build Tools 2019/2022 are installed without the C++ workload), so no Rust crate
    with a build script compiles. Experiments 4 and 5 are unresolved for that reason alone; the crates are cloned.
12. **"Serai proves threshold Monero signing is production-ready."** Serai's FROSTLASS protocol is proven secure
    (Cypher Stack) and its libraries are audited under monero-oxide, but Serai is pre-mainnet; the Monero custody
    for a federation is "audited library, no production federation yet".
13. **"COMIT xmr-btc-swap is the reference implementation."** The repository was archived on 2026-08-23 as
    unmaintained; the maintained successor is eigenwallet (`eigenwallet/core`, formerly UnstoppableSwap). The
    protocol and the cross-curve DLEQ (`sigma_fun::ext::dl_secp256k1_ed25519_eq`) are unchanged.

14. **"The sha256-preimage-to-key-share proof is a research problem."** (my own verdict of the morning.) It was a
    day of engineering: the discrete-log half is a sigma protocol on a Pedersen commitment, and the only real
    work is a SHA-256 gadget in a proof system whose native field is the key's scalar field. Bulletproofs over
    ristretto255 (Monero) and a port to Pallas (Orchard) both prove it in about 15 seconds without a trusted setup.
    The premise had confused "no library does exactly this" with "nobody knows how".
15. **"Pooled liquidity in XMR can be had without custody."** No. A pool must hold its reserve in the VM that
    settles the trade; the reserve XMR therefore sits under keys that are not the DERO contract. The honest
    product is a bonded, threshold, auditable federation plus a trustless exit hatch, not a trustless pool.
16. **"SHLD found a non-custodial way to run a Zcash memecoin market."** Its own description is a server that
    credits an internal ledger from deposits to per-user shielded addresses and pays sells from a reserve it
    holds. That is custody with shielded buyers, not a new settlement mechanism.

17. **"FROST for Zcash is audited, so our ZEC custody is audited."** The NCC audit of the ZF FROST crates (v0.6.0)
    "explicitly excludes … rerandomized FROST", which is exactly the variant ZIP 312 and our custody use; reddsa
    calls the feature experimental while the ZIP is a draft. The primitive is proven and implemented by Zcash's
    foundation, but the code path we run is not covered by a published audit.
18. **"Monero's FCMP++ hard fork activated in Q1 2026."** Repeated by several blogs; getmonero.org's own blog
    shows only 0.18.x "Fluorine Fermi" releases through July 2026, the CCS proposal is still marked unfinished,
    and the stressnet excludes multisig. No activation has happened as of 2026-09-06.
19. **"Monero multisig is a normal wallet feature."** It is gated behind an experimental flag with an explicit
    warning that funds can be stolen by a malicious group member, after a 2021 disclosure of exactly that class of
    bug. It is a trusted-signers tool, not a threshold scheme.
20. **"The wrapper is novel."** It is a federation with DERO-side enforcement; every part has prior art. The
    novelty in this repo lies in the swap proofs and the attested deposit indexes, not in custody.


---

# REVIEW

## REVIEW — adversarial pass over REPORT.md (Phase 4, 2026-09-06)

Method: for each verdict, a two-line independent derivation from the DVM primitives was written first, then
compared with the report's evidence block; every supporting source in `SOURCES.md` was re-opened; the seven usual
failure points of this design space were checked one by one; the three documents were cross-checked for
consistency. Changes to verdicts are logged in `README.md (Verdict changes)`.

### The three things the pass was told to expect

1. **The load-bearing error: the DERO ↔ XMR protocol as first written let the XMR side claim DERO without ever
   locking XMR.** In COMIT, Alice can only redeem the Bitcoin with an *encrypted signature that Bob hands over after
   he has seen the Monero lock confirm*; Bob's cooperation is the gate. A hash-lock has no such gate: once the claim
   window opened, Alice could reveal `s_a`, take the DERO and never send XMR. The first draft tried to cover this
   with a delay (`claim_after = h0 + D`), which only gives Bob time to *notice*, not a way to *stop* it. Fix (now in
   Q4 and `RESEARCH.md (Protocol sketches)`): the claim needs a second preimage `k_b` that Bob reveals off-chain only
   after Monero has confirmed Alice's lock (`Claim(id, s_a, k_b)` with `sha256(k_b)` fixed at lock time). If Bob
   withholds it, Alice cannot claim; Bob must then refund (revealing `s_b`, so Alice recovers her XMR) before `T2` or
   lose the DERO to the punish path. This is the hash-lock analogue of COMIT's encrypted signature and removes the
   delay gate. The same review confirmed the opposite ordering (XMR first) is unsafe: DERO is the only side with
   enforceable refund/punish paths, so it must lock first.
2. **The verdict stronger than its evidence: Q5 "Yes (H)".** The offer-book mechanism is sound, but the bond
   economics rest on an assumed volatility figure, the "Solana lock never existed" case is covered only by the maker
   checking Solana before locking, and the negotiation channel is unmeasured. Downgraded to **Yes (M)**.
3. **The "unresolved" item that decides the whole thing: the sha256-preimage-to-ed25519-discrete-log proof.** It
   decides the Monero layer entirely. It does **not** decide the Solana / Zcash-t / EVM layers, which are plain
   HTLC ↔ HTLC. Consequence for Phase 6: the default hypothesis "Layer B = XMR and SOL" is not supported by the
   evidence; SOL is buildable now, XMR is not.

### Verdict table

| question | prior verdict / confidence | review outcome | reason | revised |
|---|---|---|---|---|
| Q1 mycelium | established from code / H | **upheld** | Re-read `internal/` and the contracts: no signing, DAO or bridge code; interface and limits as stated. | H |
| DVM audit | complete table / H | **upheld** | Table regenerated from `dvm_functions.go` with argument counts; all 35 entries `>=0.0.0`; sha256/sha3/keccak present; no signature/curve/bigint. Confirmed at upstream HEAD via the GitHub API. | H |
| Q2 wallet hosting | Yes / H | **upheld** | Engineering only; Engram DERO-only; companion pattern is XSWD's own. Light-client leak analysis is from documentation, not measurement. | H |
| Q3 off-chain readings | per reading | **upheld** | Derivation from primitives: a chain conditions only on what it can verify; DERO verifies hashes and its own signatures only. Payment channels stay "not on DERO today". | H |
| Q4 DERO ↔ SOL / ZEC-t / EVM | Yes / H | **upheld, one caveat** | HTLC ↔ HTLC needs no proof; the DERO leg ran on the simulator with a random 32-byte preimage. Caveat: the Solana program does not exist yet; the claim is about the construction, not a demo. | H |
| Q4 DERO ↔ XMR | Yes in principle, not shippable / M | **weakened** | The load-bearing error above (claim without an XMR lock) was in the draft protocol; fixed with the `k_b` gate. The remaining blocker (the consistency proof) has no library; cost estimates are from constraint counts of public circuits, not a measurement. | L until a prototype proof exists |
| Q4 ZEC shielded | Not on DERO today / L | **upheld** | Zwap is the only running design; early access, EVM-only, unaudited (forum post 2026-05-27). | L |
| Q4 observer view | addresses and amounts public | **strengthened** | Verified by reading contract storage keys: `m_`, `t_`, `v_`, `h_`, `x_` are all plain `DERO.GetSC` reads. Any privacy language for the DERO leg is now limited to unrelated balances. | H |
| Q4 timelocks | numbers given | **weakened** | DERO figures come from source constants (`BLOCK_TIME = 18`, `STABLE_LIMIT = 8`), not from measured mainnet intervals; Monero figures (120 s, 10-block unlock, 18-block reorg) come from secondary write-ups. Adequate for design margins, not for a spec. | M; needs explorer data |
| Q4 griefing | bonds 2–3 % | **weakened** | Volatility input assumed, not pulled from a price feed; bonds enforce only DERO-observable events. | M |
| Q5 market structure | Yes / H | **weakened** | See item 2 above. `board.bas` holds no funds and cannot slash; the merged escrow+board contract that can is specified, not built. | Yes / M |
| Q6 federation | Yes-with-assumptions / M | **weakened on the evidence, mechanism unchanged** | FROST tests could not be run here (no C++ toolchain); no signature was produced. Serai is pre-mainnet; FROSTLASS proof and monero-oxide audits are real but secondary-sourced (X posts, CCS page). | mechanism M; "production-ready threshold Monero" L |
| Q7 product map | layered / — | **upheld with a scope change** | Layer B splits: SOL now, XMR later. fomo custody claim verified from its own materials (one balance, Crossmint on-ramp). | — |
| Q8 regulatory | questions only | **upheld** | Questions, not conclusions. | — |
| whisper channel (Q1/Q5) | partially usable / — | **weakened** | Experiment 3 failed on mycelium's mainnet-only address handling and then on a daemon lookup; nothing measured. | L |

### Source audit (each Yes / Yes* support re-opened)

- derohe `dvm_functions.go`, `sc.go`, `transaction_execute.go`, `config.go`: vendored clone is at upstream HEAD
  `e9df1205` (2024-02-29), confirmed as the latest commit through the GitHub API on 2026-09-06. Current.
- `poc/swap.bas` + simulator run: re-run after the 32-byte fix; log in `poc/simswap-run.log`. Current.
- COMIT `xmr-btc-swap`: repository archived 2026-08-23; the protocol code read (`protocol.rs`, `alice/state.rs`,
  `bob/state.rs`) is unchanged in the maintained fork eigenwallet/core (updated 2026-08-29). Cited fact stands;
  reference updated to eigenwallet.
- Farcaster `dleq.rs`: last commit 2023-01-02; the construction (bit-decomposition cross-group DLEQ) is documented
  in the file header. Stale but not superseded for what it is cited for.
- Universal Atomic Swaps (IEEE S&P 2022): abstract and requirements confirmed on ePrint; cited only to explain why
  DERO cannot use it (no 2-party DERO signing).
- Serai / FROSTLASS / monero-oxide: X posts and the CCS page; treated as secondary; no mainnet.
- ZF FROST: repository present and current; not executed here.
- pump.fun and fomo mechanics: pump.fun's own docs pages plus third-party summaries; fomo's own site and the
  Crossmint announcement. Adequate for a product comparison.
- Monero timing and reorg: secondary sources only. Flagged.

### Optimism hunt (the seven usual failure points)

| check | finding |
|---|---|
| sha256 confirmed on current mainnet from the function table, not docs? | Yes: `func_table["sha256"]`, range `>=0.0.0`, 25 000 gas; version `3.5.3-140` in `config/version.go`. |
| HTLC POC with the exact bytes an XMR scalar would produce? | Initially **no** (it hashed a text string); fixed to 64-hex → `HEXDECODE` → 32 raw bytes → `SHA256` → `HEX`; re-tested in the harness and on the simulator with a random 32-byte secret. |
| Ring size / SIGNER leak more than admitted? | Yes, the first draft implied more privacy than exists; corrected: maker, taker, amount, hash, secret are public. |
| Timelocks from measured data with margin? | From constants and secondary sources; margins are generous (2 h / 4 h against a 21-minute Monero unlock) but not derived from measured variance. |
| Cross-curve proof named with a library? | Named precisely (sha256 preimage ∧ ed25519 discrete log); **no library exists** for this pairing; COMIT/Farcaster prove a different statement (same scalar on two curves) because their script side verifies adaptor signatures. |
| Free option actually solved? | Partially: bonds on DERO-observable events plus the punish path; the counter-chain "never locked" case is timeouts only. |
| Federation assumed working because Serai exists? | The report says pre-mainnet; the FROST signature experiment did not run. Downgraded. |
| mycelium claims beyond its threat model? | None made; the channel is used only as "small signals, both peers online". |

### Consistency check

- `RESEARCH.md (Feasibility matrix)` XMR cell said "Yes, with a client-side proof"; `REPORT.md` says "not shippable today".
  Aligned: the cell now reads "Not on DERO today (needs the proof)". Trust model §2 already matched the report.
- `RESEARCH.md (Trust models)` §1 rule "second locker's expiry earlier than the first's" is restated identically in Q4.3 and
  in `dero-sol.md`.
- The XMR sketch and Q4.4 now both carry the `k_b` gate; the `claim_after` delay was removed from both.

### What remains unresolved after this pass

1. The consistency proof (decides XMR). 2. The FROST signature and the DLEQ tests (need a C++ toolchain).
3. Whisper measurements (need a testnet pair or a mycelium fix). 4. Measured block-interval data for the timelock
spec. 5. A Solana escrow program and a devnet run (nothing blocks it; it is work, not uncertainty).
