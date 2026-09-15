# Mechanism review — federation, atomic swaps, and what can actually ship on mainnet

**Written 2026-09-08.** The audit of 2026-09-08 (`docs/audit/DMZ_OPUS5_MASSIVE_AUDIT.md` §1) found that the
federation-versus-atomic-swaps question "has never been answered properly" and that this file did not exist.
That was half right. The analysis exists — `DESIGN.md` § "Phase A", `../crosschain/RESEARCH.md` § "Executive
verdict", and `../crosschain/README.md` § "DECISION (Phase 6)" — and the prototypes it rests on were built and
run. What was missing was one page that states the verdict, and the verdict had not been re-examined against a
constraint the operator only stated tonight: **they will not host anything 24/7.** That constraint changes the
answer, so this page is written from it.

**Superseded 2026-09-15.** The operator ruled that the ZEC and XMR atomic swaps are removed: "they should both be
removed, we should have a wrap and perhaps a market the wrapped tokens can be traded on 1:1"
(`docs/MAINNET_TODO.md` Phase Y). This page's verdict is reversed: the transparent-ZEC HTLC as the one trustless pair
that ships, and the Monero key-share swap after it. The federation wrap is the product. It is run by named signers
who are not the operator, and it is never called trustless. The no-hosting constraint below still binds it. The body
is kept as written.

Everything below is either a fact from a primary source (named in `../crosschain/SOURCES.md`) or a decision,
and the two are kept apart.

---

## 1. The constraint, and what it disqualifies

A k-of-n federation needs k signers online whenever a deposit is to be minted or a withdrawal paid. Someone runs
those machines around the clock. If the operator will not, one of two things is true: **other parties run the
signers**, which is a trust decision the operator must make and the app must name; or **there is no wrapped
asset**, and XMR and ZEC holders trade by atomic swap, where nobody holds anyone's coins and the only party who
must be online is the counterparty to that one trade, for the duration of that one trade.

The operator's constraint does not disqualify the federation. It disqualifies the *operator-run* federation,
and it makes the swap path the one that satisfies the constraint by construction.

## 2. The three facts that decide what a swap can be

1. **The DERO VM is a hash-and-height machine.** 35 built-ins; no signature verification, no curve arithmetic,
   no big integers, no cross-contract calls (`RESEARCH.md` § DVM primitives, from `derohe/dvm/dvm_functions.go`).
   A DERO contract can therefore enforce a hashlock and a height timelock — an HTLC — and nothing more. It
   **cannot** verify an adaptor signature.
2. **Monero has no script.** The Bitcoin–Monero protocols (Gugger 2020, arXiv:2101.12332; COMIT; Farcaster RFCs
   01/08) place every enforcement on the scripted chain: the Monero output is locked to a key both parties hold
   a share of, and the scripted side's *adaptor signature* leaks the missing share when the buyer redeems. That
   construction needs the scripted chain to verify the adaptor signature. DERO cannot, so **it does not apply.**
3. **Transparent Zcash is Bitcoin script.** A t-address HTLC is an ordinary script HTLC, verified in this repo
   under Zcash's own consensus interpreter (`../crosschain/README.md` § Resolved 3). Shielded Zcash has no
   script and no adaptor-signature ecosystem (§ Resolved 2, executive verdict 4).

From these, the pairs sort themselves.

## 3. Verdict by pair

| Pair | Mechanism | Trusted party | Who must be online | Buildable on mainnet DERO **today** | Privacy cost |
|---|---|---|---|---|---|
| **ZEC (transparent) ↔ DERO** | HTLC ↔ HTLC. `contracts/swap.bas` + `board.bas` are written, tested, and ran end to end on the simulator (`../crosschain/poc/simswap-run.log`) | **none** | maker and taker, for the swap's duration (minutes to tens of minutes) | **Yes** | the ZEC leg is transparent: amount and t-addresses are public on Zcash |
| **ZEC (shielded) ↔ DERO** | no script on the Orchard side; the Pallas key-share proof is prototyped (`poc/proof-zec`) but not integrated | none, once built | both | **No** — research item | none |
| **XMR ↔ DERO, trustless** | the DERO HTLC preimage tied to a Monero key share by a client-side proof "SHA-256(secret) = h AND secret is the discrete log of this ed25519 point" (`poc/proof-xmr`: Bulletproofs, no trusted setup, 37 460 gates, 8.5 s prove / 0.74 s verify, 1.6 KB, a CLI since 2026-09-08) | none | both | **Built 2026-09-08, simulator-verified**: `contracts/htlc.bas` (three exits, tested against the real DVM), the proof tool, and the companion `tools/xmrswap`; all three exits ran on the simulator with a fake Monero wallet. Remaining before mainnet: the companion's re-verification against invariants 8–13 of `docs/XMR_SWAP_BUILD.md`, a Monero stagenet run, and the spore rendezvous | **the DERO leg is public** — maker, taker, amount, the three hashes and the revealed share are contract storage (`RESEARCH.md` §4.2); only balances stay encrypted. The Monero leg is a normal Monero transaction |
| **XMR ↔ DERO, wrapped (wXMR)** | bonded k-of-n federation: `contracts/vault.bas` v3 with the halt-lease/Reclaim machine landed 2026-09-08 | **k of n signers, colluding** — see `README.md` § Trust statement | k signers, always | **Yes** — and the only way to have XMR on DMZ now | deposit and withdrawal addresses are attested on chain; the app must say so (`SEC·RT-04`, fixed) |
| **ZEC ↔ DERO, wrapped (wZEC)** | same federation; Orchard spend authority shared by FROST (`poc/frost-zec`, ZIP-312 draft) | k of n signers | k signers, always | **Yes**, same caveats | none beyond the federation |

Two things the table makes plain. **The one pair that is trustless, needs no host, and works on mainnet DERO
today is transparent Zcash.** And **Monero on DMZ today means the federation, and the federation means signers
somebody runs.**

## 4. Spore (formerly mycelium), read against this

`liqdmetal/mycelium` was reviewed on 2026-09-05 (`RESEARCH.md` Q1). It has since been renamed **spore**
(HEAD `254beba`, 2026-09-07); the "mycelium" the operator was told about is the substrate inside it — the
chain-agnostic `Chain` interface (`PostPayload` / `ListIncoming` / `Watch`), the sender-held rendezvous, and the
optional store-and-forward relay hop.

What it is, from its own README: *"an E2E-encrypted, no-relay private messenger — you talk wallet-to-wallet over
DERO, EVM, Solana, or Monero; no server, relay, or box sits in the middle."* DERO and Solana are live on mainnet.

What it gives DMZ:

- **A maker/taker channel with no server.** A swap needs the two parties to exchange offer, addresses, hash and
  (for the XMR path) proofs. Spore's DERO whisper is a real transaction carrying up to `PAYLOAD0_LIMIT = 144`
  bytes, ≤ 80 characters of text, at ring size 2, ~18 s per message; long bodies are held by the sender and
  fetched peer to peer "over the node's existing P2P channel — no new open port." That is exactly the shape
  `board.bas` needs beside it, and it means the operator hosts nothing for negotiation either.
- **A reference wallet-RPC driver for Monero.** `internal/xmr/xmr.go` uses precisely the four calls our unbuilt
  federation backend needs — `get_height`, `get_address`, `transfer`, `get_transfers` — and is worth reading
  when `tools/vaultsigner`'s Monero backend is written.

What it does not give, stated so nobody is misled:

- **Not a bridge, not custody, not value transfer.** Its roadmap has no atomic-swap or bridge item at all.
- **Its Monero backend is not usable as is.** `internal/backend/backend.go`: *"xmr backend is NOT live-verified
  (mock-verified only; the 8-byte payment-id seam is deprecated by modern monerod)"*. A federation backend must
  use subaddresses (or integrated addresses) and Monero multisig wallet-RPC, neither of which spore has.
- **The relay fabric is planned, not built** (ROADMAP item 5), and its home-node model asks each *user* to run
  an always-on node — the same constraint the operator is declining, moved onto users. A swap DEX does not need
  it: a maker is online while their offer stands, and nobody otherwise.

**Decision:** adopt spore as the rendezvous channel for `board.bas` offers and swap negotiation. Do not adopt
its Monero code. Do not describe it as making the cross-chain part "trustless" — it makes the *conversation*
serverless; the value transfer is still whichever row of §3 applies.

## 5. The NFA standard, read against the product

`civilware/artificer-nfa-standard` (ART-NFA-MS1, HEAD `d286b19`) is the DERO-native non-fungible standard that
Engram, Gnomon and dReams already index. Its shape, from the template: one contract per asset; ownership is one
unit of the contract's own asset (`ClaimOwnership` burns that unit back to the contract and records `owner`);
a built-in marketplace (`Start` sale or auction, `BuyItNow`, `Bid`, `CloseListing`, `CancelListing`); creator
royalty and an optional charity share paid at settlement; a headers standard (`nameHdr`, `descrHdr`, `typeHdr`,
`iconURLHdr`, `tagsHdr`) plus `fileURL` / `fileSignURL` with DERO filesign hashes (`fileCheckC` / `fileCheckS`)
so the file's authenticity is checkable; `collection` fixed at install.

It is **priced in DERO only**: `BuyItNow` and `Bid` read `DEROVALUE()`, `processDEROFinalPayment` splits and
sends DERO. That is the one thing DMZ must extend — an NFA an XMR holder can buy with wXMR — and it is the same
extension `pool.bas` and `launch.bas` already received: a `quote` parameter, `ASSETVALUE(quote)` in place of
`DEROVALUE()`, `SEND_ASSET_TO_ADDRESS(…, quote)` in place of `SEND_DERO_TO_ADDRESS`, with the zero hash meaning
DERO so a DERO-priced listing stays byte-for-byte standard.

**Decision:** replace `contracts/nft.bas` with an ART-NFA-MS1-compatible contract — the headers, the ownership
model, the marketplace and the royalty split as specified — extended with the quote parameter. Keep the
headers exactly, so every DMZ asset indexes in the existing DERO tooling. This retires the audit's `nft.bas is
DERO-only` item and the bespoke-contract risk (`PROTO·BC-7`, `mt_` never deleted) in one move.

## 6. The recommended sequence for mainnet

Ordered by "will actually work on mainnet, without the operator hosting anything":

1. **Transparent-Zcash swap market.** `board.bas` offers, `swap.bas` HTLCs, spore whispers for the negotiation.
   Trustless; no host; works on today's DVM. The UI must state the privacy cost of the ZEC leg plainly and offer
   nothing it cannot deliver. This is the first thing DMZ can honestly put on mainnet for a Zcash holder.
2. **NFA-compliant assets**, DERO-priced first (standard-compatible), quote-priced as soon as a wrapped or
   swapped quote exists.
3. **The federation, only with third-party signers.** If the operator will not run signers, the runbook must say
   who does, what they bonded, and that `n ≥ 2k` is the only configuration in which a colluding quorum can be
   slashed (`PROTO·BC-4`). The app names them (`app/notice.js` now does). Never "trustless".
4. **The trustless Monero path**: integrate the prototyped proof into an escrow with a gate secret, build the
   companion, run stagenet. It is the only way an XMR holder ever trades on DMZ without trusting a quorum.

**Operator's ruling, 2026-09-08 evening: "we want it trustless."** Item 4 is therefore not "later" — it is the
active track, and the federation is the fallback nobody is asked to trust by default. The build specification is
`docs/XMR_SWAP_BUILD.md` (protocol, ownership, interfaces, invariants, tests); the build began the same evening.
What "trustless" means here, and why the word is justified for the swap and not for the vault: no party — the
operator, a signer set, or the counterparty once both sides have locked — can take or freeze either side's
coins. What *is* trusted is the cryptography (the preimage-to-key-share proof, hashlocks, height timelocks) and
each person's own wallet. The one weakness is economic, not custodial: the second locker holds a free option on
the price for one lock window (§7 of the build spec).

## 7. What would change this verdict

- A DVM built-in for ed25519 or secp256k1 signature verification (or curve arithmetic) — then DERO becomes an
  arbitrating chain in the Farcaster sense and the standard adaptor-signature swap applies to Monero directly.
- Monero FCMP++ / FROSTLASS maturing (SOURCES.md rows) — cheaper, audited threshold custody for the federation.
- A shielded-Zcash swap ecosystem (none running today outside EVM-only early access).
- Spore's relay fabric shipping — offline delivery would let a maker leave an offer standing without staying
  online, which is the one hosting-shaped requirement the swap path still has.

## 8. What was not verified here

Nothing on a live foreign chain: no Zcash testnet HTLC was run in this cycle (the interpreter-level verification
is from 2026-09-06), spore's DERO mainnet whisper was not driven against our simulator (Q1 recorded the same
limitation), and the XMR proof's integration is unbuilt. Every "buildable today" claim above rests on the named
prototype and its recorded run, not on a mainnet transaction.
