# DMZ — the follow-up list

Everything known that is left to do, in one place, written 2026-09-09 at the operator's request ("the largest
to-do list you can, to follow up this work"). `docs/TODO.md` stays the *working* list — what is in flight this
week, in order. This is the reservoir it draws from. When an item here starts, it moves there; when it ships, it
moves to `docs/history/HISTORY.md` with its evidence.

**The test every item is judged by.** DMZ is a Zcash and Monero exchange on DERO. An XMR or ZEC holder arrives,
sees their balance, understands every cost, trades their coin against tokens and NFAs, and withdraws — with DERO
visible only as gas. An item that does not serve that holder waits behind one that does.

**Hard rules that bind every item:** never touch mainnet; never store private keys in the browser; never proxy
transactions through a server; never call anything trustless unless its trust assumptions justify it; document
every trusted party; no new dependency, service or framework without a written answer to "what does this provide
that a smaller primitive cannot"; fail closed; no machine-specific paths in the repository; **liveness by
construction** — the operator revokes their own access after deployment, so any state that needs a human to leave
it is a defect; never contradict `app/notice.js`.

Priority letters: **A** blocks the mainnet sequence or the five standing requirements · **B** a holder would
notice within a week · **C** hardening, tooling, hygiene · **R** research before anything can be built. Items are
numbered for reference (`BL-…`), not for order.

---

## 1. The mainnet sequence (the order things go live)

The operator's ruling stands: 1. transparent-ZEC swap market, 2. NFA-compliant assets, 3. federation only with
named third-party signers and n ≥ 2k, 4. the trustless Monero path — with "we want it trustless" making 4 the
active build track. Nothing below reaches mainnet before its gate here is met.

- **BL-1 (A)** A written mainnet deployment checklist per contract: canonical hash printed and matched, install
  gas measured on testnet, `simdeploy -network mainnet -yes` dry-run transcript, the SCIDs recorded in
  `deploy/networks.json`, the TELA publish, and the operator-revocation step (`factory.bas` `Renounce`, then `owner`
  read back absent) as the last line.
- **BL-2 (A)** A testnet deployment of the whole set (factory, social, founders, chat, both vaults, one pool, one
  launch, one NFA, one HTLC) from `deploy/testnet.md`, with every SCID and every install gas in the record, and
  the app served from TELA against it. Nothing has run outside the simulator yet.
- **BL-3 (A)** The "what happens when the operator is gone" audit: for every contract, list every state and prove
  either that it resolves by itself (a lease lapses, a timelock passes) or that any third party can resolve it
  with no privilege. `vault.bas` had this done (halt lease, `Reclaim`); `factory.bas` (`SetTreasury`,
  `SetRegFee` — owner-only forever), `founders.bas`, `chat.bas`, `social.bas`, `nfa.bas` (`ownerCanUpdate`) have
  not.
- **BL-4 (A)** The mainnet timing policy for the swap (t1 = 400 blocks, t2 = t1 + 400, 20 Monero confirmations)
  measured against real reorg statistics on both chains rather than argued from block times (see BL-30).
- **BL-5 (B)** The `app/notice.js` text re-read against every screen once the swap and NFA screens exist: it must
  still be true that nothing is regulated, owned, changeable or supported, and every new screen must say the same
  thing in the same words where it matters.

## 2. The trustless DERO↔XMR swap (`docs/XMR_SWAP_BUILD.md`, `contracts/htlc.bas`, `tools/xmrswap`)

The contract, the proof and the companion are built; all three exits ran on the real DVM on 2026-09-09. What is
left is everything between "works for two people who share a folder" and "works for two strangers on mainnet".

- **BL-10 (A)** **The rendezvous channel between strangers.** The companion's channel is a directory
  (`DirChannel`). Build the spore transport (liqdmetal/spore, a no-relay E2E messenger over DERO tx payloads, live
  on mainnet): a `Channel` implementation that sends each protocol message as a spore payload and reads the
  peer's, sized to `PAYLOAD0_LIMIT` = 144 bytes per tx (so `bob_setup` / `alice_setup` with a 1,601-byte proof
  need chunking — 12 txs each — or a proof exchanged out of band with only its hash in the channel). Write the
  answer to "what does spore provide that a smaller primitive cannot" (an existing on-chain, no-host, encrypted
  channel with a live user base) before adopting it.
- **BL-11 (A)** **The Monero backend against a real node.** `tools/xmrswap/monero.go` has only ever been driven
  by the in-process fake. Run the happy, refund and punish paths against `monero-wallet-rpc` on **stagenet** with
  a real joint address, real `get_transfers` output, real unlock times, real `sweep_all`. Record the transcript.
- **BL-12 (A)** **Offer discovery.** A stranger must find a counterparty: an offer board for swaps (rate, amount,
  t1/t2 policy, the maker's spore address) — `board.bas` from the Phase 1 PoC, or a spore group, or both. Decide
  with a design note; the board must be readable by the app with no host (BL-50).
- **BL-13 (A)** **The swap screen in the app.** Nothing in the browser holds a key, so the browser cannot be a
  swap companion; the screen shows swaps the wallet is party to (read from `htlc.bas` by the taker's address),
  their state, the deadlines as wall-clock times, and what each exit means for the holder — including, after
  `Punish`, that the XMR stays under the joint key unless Bob reveals `s_b`. Copy that a Monero holder understands.
- **BL-14 (A)** **Companion packaging for a Monero holder.** A signed release of `xmrswap` for Windows, macOS and
  Linux, a first-run that checks the wallet-RPC and the DERO wallet, a state directory it explains, and a recovery
  command that reads the chain and says what to do next. Reproducible builds; hashes in the release notes.
- **BL-15 (A)** **Companion liveness.** The protocol needs each companion alive at specific windows (Bob's refund
  window at t1, Alice's claim before the cutoff). Document the exact windows in hours for the mainnet policy, and
  build the companion's "sleep until" scheduling so a laptop that closes does not lose funds; test with the
  crash fuzz extended to long sleeps.
- **BL-16 (A)** **The refund-window race (R2/R11).** Bob's Refund and Alice's Claim can both be valid in the last
  block before t1. Measure on the simulator how often a Claim sent at `t1 − 8 − margin` lands after t1 under load;
  set the mainnet margin from the measurement, not from 8.
- **BL-17 (B)** **Amount limits and fee disclosure.** The companion prints the DERO fee for Refund/Punish; it
  does not yet show the Monero fee of the sweep or the total cost of the swap in each asset before the maker
  commits. Add a cost table per role, per asset, before the first lock.
- **BL-18 (B)** **Rate and slippage.** The swap fixes DERO and XMR amounts at setup; a price move between setup
  and Alice's lock (up to `stable` blocks + Monero confirmations) is borne by whoever locked first. State it in
  the docs and in the companion's terms check; consider a maker-set expiry on the offer.
- **BL-19 (B)** **Partial fills / multiple swaps.** One swap = one HTLC id. A maker with 10 XMR to sell serves
  ten takers only by ten setups. Design batching (many ids under one offer) or say plainly it is one at a time.
- **BL-20 (C)** **`htlc.bas` for a wrapped asset.** `Lock(..., asset)` already takes an asset SCID; test the DVM
  path where the DERO leg is wXMR or a token (a token-for-XMR swap with no DERO at risk); the companion's
  `Dero` interface would need the asset's decimals for display.
- **BL-21 (C)** **Proof performance.** 8.5 s to prove on this machine; measure on a low-end laptop and a phone
  (WASM build of the prover is a research item, BL-33). Cache the generators.
- **BL-22 (C)** **Secret hygiene tests.** `TestStateFileIsPrivate` checks permissions; add a test that no secret
  is ever written to the log at any verbosity, and a redaction pass over every `Printf`.
- **BL-23 (C)** **A second implementation of the verifier.** The proof's verifier in Go (not only the Rust CLI)
  so a companion does not depend on a subprocess; or at least a fixed test vector suite the Rust CLI must pass,
  pinned in `tools/xmrswap/proof_test.go` (partly done: `TestProofXMRVector`).
- **BL-24 (C)** **The DERO leg's ring size.** Every call is ring 2 (`SIGNER()` is blank above it), which links
  Bob's and Alice's DERO addresses to the swap on-chain. State the privacy cost in `docs/XMR_SWAP.md` §trust and
  research whether the taker can be identified by a hash of a one-time address instead (BL-34).
- **BL-25 (B)** **Refund after Alice never locked.** Bob's DERO sits until t1 if Alice walks away after his lock
  (R7). Quantify the griefing cost (Bob's capital × 2 h) and add the maker's counter-measure (a small taker bond
  in DERO, or a reputation note in the offer board) as a design decision for the operator.
- **BL-26 (C)** **Companion observability without a host.** A `status` command that prints every swap's state,
  the next deadline, and what would happen if the companion stopped now — for the holder's own machine only.
- **BL-27 (C)** **Mainnet policy constants in one place.** `t1`, `t2`, confirmations, stable depth and margin
  are flags with simulator defaults and "policy" values in comments; make the mainnet policy a named profile the
  companion refuses to run without on mainnet.
- **BL-28 (R)** **Adaptor signatures instead of the hash-lock + proof.** The current design proves
  SHA256(s)=H ∧ s·G=S with Bulletproofs. A CLSAG/ed25519 adaptor-signature design (as in the Farcaster / COMIT
  work) may remove the proof entirely on the Monero side; the DVM side would still need a hash lock. Compare
  proof size, prover time, and which side must be online when.
- **BL-29 (R)** **FCMP++.** Monero's next hard fork replaces ring signatures with full-chain membership proofs.
  Confirm the joint-key sweep (`s_a + s_b`) survives — the spend authority model changes — and whether the
  view-key pair the companion generates per swap still suffices to watch the joint address.
- **BL-30 (R)** **Reorg statistics.** Pull a year of DERO and Monero block data: deepest observed reorg, block
  time variance, and derive t1/t2/confirmation policy with a stated failure probability (BL-4).
- **BL-31 (R)** **DERO's `STABLE_LIMIT` = 8 under attack.** What a miner with a large share can do to a Claim in
  the last 8 blocks; whether invariant 9 (Bob waits for the Claim block to be stable) needs more than 8 on
  mainnet.
- **BL-32 (R)** **Zcash via the same escrow.** Transparent ZEC with a SHA256 + CLTV HTLC works today
  (`zec-t-htlc` PoC); shielded ZEC needs the Orchard key-share proof (`proof-zec` PoC, 12.2 s) and a RedPallas
  joint signature. Decide whether the companion gets a `Zcash` backend beside `Monero` or the ZEC market goes
  through the board (§3).
- **BL-33 (R)** **The prover in the browser.** A WASM build of `proof-xmr` would let a phone be a taker without
  a subprocess — but a phone cannot hold the Monero wallet-RPC either. Decide whether a phone is ever a swap
  party or only a viewer (BL-13).
- **BL-34 (R)** **Taker identity without an address.** `Lock(..., taker)` stores Alice's DERO address in the
  contract; research a commitment (hash of address + nonce revealed at Claim) so the contract never stores a
  readable address.

## 3. The transparent-Zcash swap market (`board.bas`, `swap.bas`, spore)

The one trustless pair that works on mainnet DERO today with no host. Its privacy cost is real: the ZEC leg is
transparent (t-addresses), so the ZEC amount and address are public on the Zcash chain.

- **BL-40 (A) — DONE 2026-09-09, `docs/ZEC_SWAP.md`.** **The design note.** Roles, messages, deadlines, fees, and the privacy statement for the ZEC leg
  in plain words; which side locks first; what the app shows; what the companion is (the `xmrswap` binary with a
  Zcash backend, or a separate tool). One page, reviewed adversarially before code.
- **BL-41 (A) — run 2026-09-09 (HISTORY), the driver as built.** **The DERO leg on the simulator.** `board.bas` + `swap.bas` (or `htlc.bas` with a ZEC-shaped
  hash lock) driven end to end by a `simswap` run with the three exits, like `simhtlc`.
- **BL-40a (A) — DONE 2026-09-10.** **`board.bas` v2** per the note §4: the hash moves to `Post` (the maker holds the secret and locks first, so the Zcash holder bears no free option before her lock; after it the maker holds one until `cltv`, about two hours — `docs/ATOMIC_SWAP_STUDY.md` §2.2), `Accept(id, refundPkh, cltv)`, `Cancel` after expiry regardless of state; tests; `simswap` updated; the template shipped.
- **BL-40b (A) — script layer and transaction layer DONE 2026-09-10 (`tools/zecswap`: script, address, spends; secp256k1 signing; ZIP 225/244 serialization and digests against Zcash's vectors); the watcher, the ZIP 317 fee and broadcast remain.** **`tools/zecswap`** per the note §7: the taker's and the maker's Zcash side against `zcashd`/`zebrad` RPC (light-client mode names its server as trusted), crash-fuzz discipline as in `xmrswap`.
- **BL-42 (A)** **The Zcash leg on testnet.** The `zec-t-htlc` script against a Zcash testnet node: fund, redeem
  with the preimage, refund after CLTV. Record txids.
- **BL-43 (A)** **The privacy disclosure in the app.** Before a ZEC holder commits: "this trade is visible on the
  Zcash chain: amount, your t-address, the counterparty's t-address" — in those words, before the button.
- **BL-44 (B)** **Shielded-to-transparent guidance.** A holder with shielded ZEC must unshield to trade here;
  say so, say what it reveals, and link to no wallet in particular (no endorsement, per `notice.js`).
- **BL-45 (B)** **The offer board's spam control.** A board entry costs DERO gas; decide whether a bond is
  required and refunded on completion; a board full of dead offers is a UX failure.
- **BL-46 (C)** **Board expiry by construction.** Every offer has a height after which anyone can prune it; no
  operator step.
- **BL-47 (R)** **Zcash `NU6+` changes.** Confirm CLTV and P2SH remain in the transparent consensus rules on
  the Zcash roadmap; if transparent pools are ever deprecated, the market has no leg.

## 4. Shielded Zcash (research → product)

- **BL-50 (R)** **Orchard key-share swap.** `proof-zec` proves a share of `ask` and `frost-zec` does 2-of-3
  re-randomized FROST under a valid `ak`. Write the protocol the way `docs/XMR_SWAP_BUILD.md` does, with the
  three exits, and find the equivalent of the "gate" (who releases what after the shielded lock confirms).
- **BL-51 (R)** **Note detection without a host.** Watching a shielded address needs trial decryption over the
  chain; for a companion that must run on the holder's machine, decide between a light-client protocol (which
  trusts a lightwalletd) and a full node. Document the trusted party either way.
- **BL-52 (R)** **Zcash finality.** No reorg limit like DERO's; derive the confirmation policy.

## 5. NFA — the assets (`contracts/nfa.bas`, `app/nfas.js`, `docs/NFA.md`)

- **BL-60 (A)** **Finish and verify the screen** (in flight on 2026-09-09): the UI pass with a DERO-quoted and an
  XMR-quoted NFA listed, a cold-cache deep link, amounts labelled in the quote, the split before the button, the
  cancel buffer said. Then the HISTORY entry.
- **BL-61 (B)** **The `Update` sheet.** The standard's `Update(iconURL, coverURL, fileURL, fileSignURL, tags)` by
  the owner (`ownerCanUpdate`) has no sheet — deliberately, since it is the R12 hazard (URLs rewritten under a
  buyer). Decide: no sheet ever (and say so on the page) or a sheet with the hazard stated.
- **BL-62 (B)** **Filesign verification.** The page shows `fileCheckC`/`fileCheckS` and says the app does not
  check the file. Implement DERO filesign verification in the browser (a hash of the fetched file against the
  check strings; the signature against the creator's key) so "not verified" can become "verified by this app",
  with images still off by default.
- **BL-63 (B)** **Collection ownership.** `collection` is a free string at install; two creators can share one.
  Design a collection registry (a `collection_<name>` claim on the factory, first-come, or a creator-signed
  collection contract) or make the screen say "collection names are not owned".
- **BL-64 (B)** **The Founders Pass share of NFA sales in a wrapped quote.** `protoFee` in wXMR reaches the
  treasury, not the pass holders (§7). Until BL-80 lands, the NFA page must say where the protocol fee goes in
  that quote and that holders do not receive it.
- **BL-65 (B)** **Indexer compatibility test.** Run Gnomon (civilware) against the simulator and confirm a
  DERO-quoted DMZ NFA indexes as ART-NFA-MS1 v1.1.1 and a wXMR-quoted one is at least not mis-indexed. Record the
  transcript in `docs/NFA.md`.
- **BL-66 (B)** **Block-timestamp times.** Listing times are `BLOCK_TIMESTAMP()` seconds; the app compares with
  the wall clock and says "times are the block's". Read the tip block's timestamp from the daemon
  (`getblockheaderbyheight`) so "3 min left" on the cancel window is the chain's minute.
- **BL-67 (B)** **One bid per block per address (Bid 51–60).** A second bid from the same address in the same
  block is refunded and costs a fee; the sheet does not warn. Read the caller's `-bidDate` key first.
- **BL-68 (B)** **The stranded-bid case (R4).** A `BuyItNow` on an auction with bids strands the top bid under
  `currBidAmt` until the bidder settles; the app never offers that path, but a foreign tool could. The page shows
  the "Held bid" row; add the "Settle" action test to `tests/nfa_test.go`.
- **BL-69 (C)** **Royalty + fees sum.** `royalty·100 + artificerFee·100 + protoFee + charity·100 ≤ 10000` is
  checked at install and at `Start`; add a fuzz test over the four values against the settlement arithmetic
  (nothing stranded, nothing over-paid, to the unit).
- **BL-70 (C)** **Minting cost transparency.** The mint form shows the install estimate from the node; add the
  measured storage gas of the template as a floor so a node that under-estimates cannot show a low number.
- **BL-71 (C)** **NFA search and filters.** By collection, by quote, listed only, mine only — the section groups
  by collection today; a hundred NFAs need a filter row.
- **BL-72 (C)** **`nft.bas` retirement decision.** The holder-badge mechanic stays under its own name; decide
  whether it is retired (a note, the tab section removed) or kept as a feature with its own copy.
- **BL-73 (C)** **NFA in portfolio.** The Portfolio tab must list NFAs the wallet holds (one unit of each) and
  NFAs the wallet has listed (the unit in the contract), with the quote's symbol on any price.
- **BL-74 (R)** **Programmable NFAs.** The DVM has no cross-contract calls, so an NFA cannot mint editions or
  react to other contracts. Research what "programmable" can mean here (owner-callable state machines inside the
  asset; an NFA as the key to a vault; an NFA as the LP position of a pool).

## 6. The federation vault (`contracts/vault.bas`, `tools/vaultsigner`, `deploy/`)

The fallback for Monero and the current path for wrapped assets; never called trustless. Every trusted party is
named or the vault is not deployed.

- **BL-80 (A)** **`deploy/federation.md`**: who runs signers (named third parties, their bonds, their
  jurisdictions if they choose to say), n ≥ 2k at `Seal` so a colluding quorum can be slashed, the disclosure the
  app shows when `na < 2k` (PROTO·BC-4 — an operator decision; write the disclosure text now).
- **BL-81 (A)** **The Monero backend for `vaultsigner`**: `VerifyRelease` against recorded `monero-wallet-rpc`
  fixtures with subaddresses (not payment ids), deposit detection by subaddress index, the release as a
  multisig spend or a threshold-signed sweep — decide which, since Monero multisig wallets are their own
  operational risk.
- **BL-82 (A)** **The Zcash backend for `vaultsigner`**: transparent multisig (P2SH k-of-n) is straightforward;
  shielded custody needs FROST (`frost-zec` PoC). Fixtures and a testnet run.
- **BL-83 (A)** **The watcher.** Who watches a signer that stops answering, and what happens by construction:
  the halt lease lapses, `Burn`/`Released` continue, `Reclaim` returns unmarked burns after 20,000 blocks. Write
  the holder-facing timeline: "if the federation vanishes, you get your units back on DERO but the XMR behind
  them is gone unless k signers return" — in the wrap screen before the first deposit.
- **BL-84 (B)** **Slashing economics.** Bond sizes against the vault's reserve; who gets a slashed bond (the
  burners waiting on `Paying` marks); a test that a k-quorum cannot slash an honest signer.
- **BL-85 (B)** **Rolling window review.** The mint/burn rolling limit — its parameters at real decimals
  (`deploy/VAULT_PARAMETERS.md`) against a plausible daily volume; a limit a whale hits in one deposit is a UX
  failure.
- **BL-86 (B)** **Wrapped units as a quote inside pools and launches when the vault is halted.** The trade sheet
  must say the quote cannot be withdrawn right now; the LP's exposure must be stated (they hold a claim on a
  halted vault).
- **BL-87 (C)** **Signer software packaging**: `vaultsigner` as a service others run — config, backups of the
  signer key, upgrade path, and the operator's explicit non-involvement.
- **BL-88 (C)** **Key ceremony document** for a federation: how k-of-n keys are generated with no single party
  holding all, verified on chain by `Seal`.
- **BL-89 (R)** **Signer set rotation** without a halt: epochs exist; design the handover of custody (Monero
  multisig re-keying is expensive; Zcash P2SH needs a new script and a sweep).

## 7. Fees, the Founders Pass, and the treasury (`contracts/founders.bas`, `tools/treasurybot`)

- **BL-90 (A)** **Fees earned in a wrapped asset cannot reach pass holders** (`founders.bas` reads
  `DEROVALUE()`). Design the real answer: per-asset reward pools in the pass contract (`Deposit(asset)` with
  `ASSETVALUE`, per-asset `pending`), or the treasury bot converting through a pool with the price impact
  disclosed, or holders paid in the asset itself. The app must never show a fee total that mixes assets.
- **BL-91 (B)** **Treasury bot fail-closed review.** It refuses wrapped fees today; verify it also refuses when a
  vault is halted, when a pool's quote is unknown, and when the founders contract's `share` changes.
- **BL-92 (B)** **Fee disclosure matrix.** One table in the docs and one sheet in the app: trading fee, protocol
  share, LP share, launch fee, registration fee, NFA protocol fee, royalty, withdrawal fee, minimum deposit,
  gas — each with the asset it is charged in and the contract line that charges it.
- **BL-93 (C)** **`SetRegFee`/`SetTreasury` after revocation.** With no owner these are frozen forever; decide the
  final values before mainnet and state them (BL-3).
- **BL-94 (C)** **Pass marketplace in wrapped quotes?** The pass trades in DERO only; decide whether that is
  fine (it is a DERO-native instrument) and say so on the page.

## 8. Markets, pools and launches (`contracts/pool.bas`, `launch.bas`, `app/markets.js`, `trade`)

- **BL-100 (A)** **Split pairs everywhere.** `s.splitWith` detects duplicate pairs and ranks deepest first; audit
  every surface (home, markets, trade sheet, token page, portfolio, share cards) so the thin half never ranks and
  the duplicate is named where the user could pick it by mistake.
- **BL-101 (A)** **Price impact before the button** at every size, in the quote asset, with the pool's depth
  shown; refuse a trade whose impact exceeds a stated bound unless the user confirms it in a second step.
- **BL-102 (B)** **Multi-quote tokens.** A token with pools in DERO, wXMR and wZEC: which price is "the" price on
  the token page; the answer is "one per quote, never merged" — implement and test.
- **BL-103 (B)** **LP position statement.** What an LP holds (a share of two assets), in both assets, with the
  impermanent-loss note and, for a wrapped quote, the federation disclosure (BL-83).
- **BL-104 (B)** **Launch graduation with a wrapped target.** A "100 XMR" graduation target at 12 decimals was
  once installed as 0.00001 XMR (fixed); add a property test that every amount field in every form round-trips
  through the asset's decimals and back to the same string.
- **BL-105 (B)** **Factory dedup.** The factory has no dedup, so the simulator once had duplicate pool
  registrations (now collapsed by `simdeploy -migrate-from`). Add `IF EXISTS("r_" + scid)` to `reg` on the next
  factory version so a listing registers once.
- **BL-106 (C)** **Oracle elimination statement.** DMZ has no oracle; document that every price is a pool
  reading and what that implies (manipulation cost = depth).
- **BL-107 (C)** **Volume and candle sources.** `chart.js` builds candles from the pool's own trades; state the
  reading window and that off-app trades on the same contract count.
- **BL-108 (C)** **Anti-scam surfaces.** Ticker collisions (WXMR vs the real wrapped Monero — handled by reserved
  tickers and display names); extend to homoglyphs and to a "verified wrapped asset" plate that a token cannot
  imitate.
- **BL-109 (R)** **Concentrated liquidity on the DVM.** Whether a range-order pool is expressible in uint64
  DVM-BASIC under 20,000 install gas; probably not — write down why.

## 9. The app, screen by screen (`app/`)

- **BL-120 (A)** **The five standing requirements audit, every cycle**, by rendering each screen (home, markets,
  trade, wrap, NFTs, chat, portfolio, profile, token, nft, nfa, wrap/<vault>, swap) at 390/768/1024, cold and
  warm, and reading the PNG: XMR/ZEC first; balances with their source; costs before the button in the asset
  charged; no mixed-currency total; duplicates surfaced.
- **BL-121 (A)** **Balances screen.** One place that lists wXMR, wZEC, DERO (as gas), each token and each NFA the
  wallet holds, each with "read from your wallet" or "read from contract X", never summed across assets.
- **BL-122 (B)** **Empty states as onboarding.** Every empty state names the two coins and the one action that
  changes the state ("Wrap XMR to trade", "List an asset priced in XMR").
- **BL-123 (B)** **Deep links.** `#token/<scid>`, `#nft/<scid>`, `#nfa/<scid>`, `#wrap/<vault>`, `#trade/<pool>`
  all load cold (registry not yet read) and warm; add `#swap/<id>` when BL-13 lands. A Playwright test per link.
- **BL-124 (B) — DONE 2026-09-12 (MT-E10).** **DOC cap headroom.** `feed.js` reached 18,128 of a cap now 18,250.
  It was split along the naming seam rather than registry / contract loading / cache: `feed.js` keeps the reads
  (13,440) and `names.js` takes the naming and marking (5,792), moved verbatim, with the JS tests green. `wrap.js`
  was split the same day (`vault.js`, `wrapui.js`).
- **BL-125 (B)** **Error copy.** Every `toast(..., "err")` string read as a holder would: "Refusing: contract is
  not a verified listing" tells a user nothing about what to do. Rewrite with the action.
- **BL-126 (B)** **XSWD permission flow.** What the wallet asks the user to approve, per action, in the app's
  own words before the wallet's dialog appears; a "what you are signing" panel with entrypoint, arguments,
  transfers, and ring size.
- **BL-127 (C)** **Accessibility.** Tab order, focus rings, ARIA on the sheet and the tab bar, contrast on the
  network tints; a screen-reader pass over the trade sheet.
- **BL-128 (C)** **Performance budget.** Time to first render cold on a phone over a slow node; the registry
  fetch is N calls per pool — measure and batch through the daemon's multi-key `GetSC`.
- **BL-129 (C)** **Offline behaviour.** No node answers: every screen must say so and show the cache with its
  age, never a stale number without its age.
- **BL-130 (C)** **Node choice.** The app talks to a node the user configures; document that the node sees the
  app's reads (which contracts, which keys) and let the user pick their own; never ship a default that is
  operator-run (there is none).
- **BL-131 (C)** **Share cards.** A token/NFA/pool card as an image the user can share, generated in the browser
  with no server, no tracking, and the contract id on the card.
- **BL-132 (C)** **The Chat tab against `notice.js`.** Unmoderated by construction; the fee is the only spam
  control; say so on the tab.
- **BL-133 (C)** **Translations.** Not before the copy is final; then a strings table with no framework.

## 10. Contracts and DVM hygiene (`contracts/`, `reference/`, `tests/`)

- **BL-140 (A)** **Install-gas headroom per contract**, measured on every run of the suite and printed: vault
  16,368 / 20,000, nfa ~14,700, launch, pool, founders, factory, htlc 3,598. Fail the suite if any is within
  500 of the cap.
- **BL-141 (A)** **`TestNoExternalSendBeforeAFailureExit` coverage.** It walks control flow from every external
  send; extend it to `STORE` before a failure exit (a state write that a `RETURN 1` should have prevented is the
  same shape) and to every new contract automatically.
- **BL-142 (B)** **A DVM-BASIC lint** in `reference/`: `LOAD` of a possibly-missing key without `EXISTS`;
  arithmetic that can wrap (`a - b` with no `a >= b` guard); `SIGNER()` used at ring 16; non-ascending lines;
  `DIM` not first — each a test that fails on a synthetic bad contract.
- **BL-143 (B)** **Fuzz every entrypoint** of every contract with random arguments and random attached
  values, asserting the reserve invariant (`reserve >= sup - inflight`), the NFA unit invariant (exactly one
  unit exists), the HTLC balance invariant (contract balance == sum of open locks).
- **BL-144 (B)** **The formal model** of `vault.bas` (Phase C of the wrapper plan): a TLA+ or Alloy model of
  mint/burn/release/veto/halt/reclaim/slash with the invariant checked over every interleaving of k-of-n votes.
- **BL-145 (C)** **Canonicalisation edge cases.** `reference.Canon` strips whole-line comments and blank lines;
  test a comment inside a string literal, a trailing comment on a code line (kept — and charged), and CRLF.
- **BL-146 (C)** **Contract version headers.** Every `.bas` carries its version and the date its canonical
  hash last changed; `simdeploy` refuses to demote a template whose header did not change.
- **BL-147 (C)** **Ownership-transfer review — done for `factory.bas` on 2026-09-11.** A transfer to a burn address
  could never complete (the new owner has to sign the claim), so the factory's two-step `TransferOwnership` /
  `ClaimOwnership` was removed and `Renounce` added: the owner deletes `owner`, after which `SetTreasury`,
  `SetRegFee` and `Renounce` refuse every caller (`tests/registry_test.go` `TestFactoryRenounce`). Any other
  contract with an owner key needs the same review.
- **BL-148 (R)** **DVM changes upstream.** Track derohe releases for changes to `PAYLOAD0_LIMIT`, gas pricing,
  `STABLE_LIMIT`, and any new built-ins (cross-contract calls would change §5 and §9).

## 11. Tools and the simulator (`tools/`)

- **BL-160 (B)** **A simulator reset and seed script.** The wallet fleet is nearly drained (0.02–2.9 DERO per
  wallet); every live run begins with funding. One script that resets the simulator, funds the 22 wallets
  generously, deploys everything with `simdeploy`, seeds two pools, one launch, one NFA of each quote, and
  records the SCIDs — so a cycle starts from a known state.
- **BL-161 (B)** **`simsmoke` extended** to the swap and the NFA: one run that touches every contract once.
- **BL-162 (B)** **Every sim tool fails closed on a wrong factory** the way `simnfa` now does (checks the on-chain
  entrypoint exists before spending gas); `simnft`, `simlaunch`, `simwrapmkt` do not.
- **BL-163 (C)** **`genesistx` and `netwallet` build.** They import derohe directly and do not build in this
  checkout (derohe is not a Go module here; `vendor/` is ignored). Either move them under the vendored module
  the way `tests/run.sh` does, or document that they are built elsewhere, and take them out of `go build ./...`
  expectations in `docs/NFA_BUILD.md` §6.
- **BL-164 (C)** **A bare-checkout bootstrap.** `vendor/derohe` is a local, ignored copy; a fresh clone cannot
  run the DVM suite or build the tools. A `tools/bootstrap.sh` that fetches derohe at the pinned commit into
  `vendor/derohe` (and prints its hash) — with the "what this provides" answer: nothing runs without it.
- **BL-165 (C)** **`uitest` as a checklist runner**: one PNG per screen per viewport per cache state, with a
  manifest the loop reads, so "look at the PNGs" is a list to tick rather than a directory to browse.
- **BL-166 (C)** **`simhtlc` mainnet-policy mode**: run the three exits with the mainnet constants against the
  simulator's clock scaled, to catch any arithmetic that assumes the small values.
- **BL-167 (C)** **Treasury bot dry-run mode** with a report of what it would do, per asset.

## 12. Testing discipline

- **BL-180 (A)** **A regression test for every bug in HISTORY.** BC-1 (the double payment) has one; the units
  regression, the WXMR impersonation, the allow-list single-vault field, the create-pool argument count, the
  duplicate registration — each needs a test that fails on the old code.
- **BL-181 (B)** **JS tests over the data layer**: `feed.js` registry parsing, `splitWith`, `nameQuote`,
  `loadNFA` refusal cases, the cache round-trip, with fixtures recorded from the simulator.
- **BL-182 (B)** **A Playwright wallet flow for the NFA**: mint through the form on the simulator, register, list,
  buy from a second wallet, claim — the whole life of an asset as a user would live it.
- **BL-183 (B)** **The crash fuzz across a restart with a changed config** (a companion restarted with a
  different `-confs` must refuse to continue a swap whose policy changed).
- **BL-184 (C)** **Property tests for every formatter**: `fmtUnits`/`parseUnits` at 5, 8, 12 decimals round-trip;
  no float anywhere (the C5 grep in `tests/run.sh` covers Go; add JS).
- **BL-185 (C)** **Replay of the simulator's history** into a fresh simulator as a determinism check for the
  data layer (same chain → same cache).

## 13. Security and privacy

- **BL-200 (A)** **A threat model document** per component: assets, adversaries (a miner, a signer, a
  counterparty, a node operator, a browser extension, the operator themselves after revocation), what each can
  do, and the control that stops them or the disclosure that admits them.
- **BL-201 (A)** **Second-opinion audit of `htlc.bas` + the companion** by agents told not to trust the first
  (the pattern that found BC-1), before any testnet swap with real counterparties.
- **BL-202 (B)** **What a node sees.** The app's reads reveal interest in specific contracts; the swap
  companion's reads reveal the swap; document and, where cheap, pad (read a few extra keys).
- **BL-203 (B)** **spore metadata.** A swap negotiation over spore puts ~24 txs on DERO per swap with linkable
  timing; state it; consider a shared group address so messages are not pairwise.
- **BL-204 (B)** **XSWD surface.** Which methods the app requests, that it never requests a seed or a key, and
  a test that greps the app for any wallet method outside the allow-list.
- **BL-205 (C)** **CSP and integrity.** `default-src 'none'` holds; add a build-time check that no file
  references an external URL except through `U.safeUrl`; the TELA publish pins hashes.
- **BL-206 (C)** **Vendored derohe provenance.** Record the upstream commit and hash of `vendor/derohe`; a
  script that diffs it against upstream (BL-164).
- **BL-207 (C)** **Release signing** for the companion and the app bundle; the public key in the README and on
  chain.
- **BL-208 (R)** **Timing side channels in the companion** (proof verification time, response latency)
  linking a swap to a machine; likely irrelevant, write the argument down.

## 14. Documentation

- **BL-220 (A)** **The README trust statement** re-written once the swap screen exists: what is trustless (the
  DERO↔XMR swap, the transparent-ZEC swap), what is federated (wrapped assets, named signers), what is neither
  (nothing else), and the liveness-by-construction promise, each with the line of code or contract that makes
  it true.
- **BL-221 (B)** **A holder's guide** per coin: "I have XMR" / "I have ZEC" — the three paths (swap, wrap, wait),
  what each costs and reveals, in the holder's asset.
- **BL-222 (B)** **`docs/history/HISTORY.md` discipline**: every entry has SHIPPED / ALSO FIXED / VERIFIED /
  UNCONSIDERED (≥ 3) / NEXT with commit hashes and txids; a script that fails the loop if an entry is missing a
  section.
- **BL-223 (C)** **`research/crosschain/SOURCES.md`** kept primary-only (specs, code, advisories), with the date
  read and the commit read.
- **BL-224 (C)** **A glossary**: quote, wrapped, vault, federation, epoch, halt lease, reclaim, HTLC, gate,
  companion, rendezvous, NFA, listing buffer — one line each, used consistently in the app copy.
- **BL-225 (C)** **Operator runbooks for the one thing they still do**: deploy. Nothing after deploy needs them
  (BL-3), and the runbook says so.

## 15. Deployment and operations without a host

- **BL-240 (A)** **TELA publish pipeline**: `genconfig -dist` → DOC files ≤ 18,432 bytes each → `telainstall` →
  the SCIDs of every DOC in `deploy/networks.json`; a verification that the served bundle hashes match the
  repo at the tagged commit.
- **BL-241 (A)** **The revocation ceremony**: after mainnet deploy, set the factory's treasury and registration fee
  to their final values, call `factory.bas` `Renounce`, read back that `owner` is absent and `renounced` holds the
  height (`deploy/README.md`, mainnet), and record the proof (txids) in `deploy/mainnet.md`. Possible since
  2026-09-11 (FINDINGS_2026-09-10 SEC-K-02, LIV-08); before that the code had no no-owner state.
- **BL-242 (B)** **Monitoring that no one runs**: the app itself shows vault health (halted? reserve vs
  supply?), signer liveness (last vote height), and swap contract balance — read from chain by every visitor,
  so the "monitor" is the users.
- **BL-243 (C)** **Upgrade story**: contracts are immutable; the app can be republished to a new TELA SCID; the
  factory can be migrated with `simdeploy -migrate-from`; write how a v2 app finds v1 markets (legacy template
  hashes already exist) and how a v2 factory is announced to v1 apps (it is not — say so).

## 16. Product beyond the core (only after §1–§9)

- **BL-260 (C)** Token creation with a wrapped-quote launch as the default (XMR-priced launches first).
- **BL-261 (C)** Hosting: the app is a TELA DOC set; a "run your own" guide with no operator involvement.
- **BL-262 (C)** A trading terminal view (depth, trades, candles per quote) that stays under the DOC cap by
  being its own DOC.
- **BL-263 (C)** Profiles and follows (`social.bas`) surfaced on the token and NFA pages as social proof with the
  cost of a follow stated.
- **BL-264 (R)** The USDT question (a wrapped stable via a federation): the same vault, the same disclosures,
  a different issuer risk — write the argument for and against before anyone asks.

## 17. Unconsidered, carried from HISTORY

Every HISTORY entry lists at least three things nobody had thought about. They live here until each becomes an
item above or is closed with a written reason.

- The rendezvous channel is a directory (→ BL-10).
- The Monero leg has only ever run against the in-process fake (→ BL-11).
- The mainnet timing policy is argued, not measured (→ BL-4, BL-30).
- After `Punish` the XMR stays under the joint key; no screen says it (→ BL-13).
- `collection` is a free string; nothing owns a name (→ BL-63).
- The simulator wallet fleet is drained (→ BL-160).
- Founders fees in a wrapped asset reach no holder (→ BL-90).
- A bare checkout cannot build or test (→ BL-163, BL-164).
- The factory has no dedup (→ BL-105).
- `genesistx`/`netwallet` do not build (→ BL-163).
- The NFA page compares block timestamps with the wall clock (→ BL-66).
- A phone can view a swap but cannot be a party to one (→ BL-33).
- The ZEC leg of the transparent market is public (→ BL-43).
- Nothing has run outside the simulator (→ BL-2).

## 18. Closed with a reason (so they are not re-opened)

- **A ninth tab for NFAs.** The phone tab bar is a seven-column grid; an eighth visible tab failed the UI pass.
  NFAs are the first section of the NFTs tab with their own page (`docs/NFA_BUILD.md` §3.C). 2026-09-09.
- **spore as a bridge.** It is a messenger; its Monero backend is mock-only. Rendezvous channel only. 2026-09-08.
- **The federation as "trustless".** It is k-of-n custody by named signers; the README says so; the trustless
  Monero path is the key-share swap. 2026-09-08.

## 19. Decisions only the operator can make

Each of these blocks an item above. They are questions, not tasks; the answer goes into the item's design note.

- **D-1** The federation's signers: who, how many, and the bonds (BL-80). Without names there is no federation
  and wrapped assets stay on the simulator.
- **D-2** `n ≥ 2k` at `Seal`, or a smaller set with the `na < 2k` disclosure shown to every holder (BL-80).
- **D-3** The Founders Pass and wrapped-asset fees: per-asset reward pools, a conversion through a pool with the
  impact disclosed, or holders paid in the asset itself (BL-90).
- **D-4** `artificerFee`: leave it at 0 (no donation to the standard's authors) or set a percentage and say so
  on every NFA page (`docs/NFA_BUILD.md` §1).
- **D-5** Collection names: owned by a first claim, or explicitly unowned with the page saying so (BL-63).
- **D-6** The `Update` sheet for NFA URLs: never offered, or offered with the R12 hazard stated (BL-61).
- **D-7** The swap's mainnet policy after the measurement (BL-4, BL-30): t1/t2 in hours, confirmations, and the
  taker bond question (BL-25).
- **D-8** A phone as a swap party (a WASM prover and a wallet it cannot hold) or a viewer only (BL-33).
- **D-9** Registration fee, chat fee and treasury address at mainnet deploy — frozen forever once the owner is
  revoked (BL-93).
- **D-10** The revocation itself: the block height at which `factory.bas` `Renounce` is sent (it cannot be undone,
  and there is no transfer of ownership), and who witnesses it (BL-241).
- **D-11** `nft.bas` (the holder badge): retired or kept as a feature (BL-72).
- **D-12** The transparent-ZEC market's place in the mainnet sequence given its privacy cost (§3): first as
  ruled, or after the Monero swap now that the trustless path exists.
- **D-13** Whether DMZ ever lists a wrapped stable (BL-264).
