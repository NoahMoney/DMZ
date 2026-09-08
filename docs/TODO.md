# DMZ — the working list

**The product, and the test every item is judged by.** Zcash and Monero are the markets DMZ wants: private,
fast, cheap trading of tokens and NFTs against *their* coin, on 16-second blocks with encrypted balances. DERO
is the platform and the gas, not the destination. Before starting anything below, ask whether it serves an XMR
or ZEC holder. If it does not, it waits.

Status keys: **DONE** shipped and verified · **NOW** in progress · **NEXT** ready to start · **BLOCKED** waiting
on something named.

---

## 0. Where the quote-asset programme stands

Both market contracts are quote-agnostic as of 2026-09-08. A market can be TOKEN/DERO, TOKEN/wXMR or
TOKEN/wZEC, because in the DVM `ASSETVALUE(zero hash)` *is* `DEROVALUE()` and
`SEND_ASSET_TO_ADDRESS(a,n,zero hash)` *is* `SEND_DERO_TO_ADDRESS(a,n)` — so one asset-shaped path serves both
and no line branches on the quote.

- **DONE** `pool.bas` takes `quote` at install. 14 458 install gas of 20 000. Whole suite green with
  quote = zero, including the 3 000-step fuzz against `reference/pool.go`.
- **DONE** stray-DERO trap closed: with a non-DERO quote, DERO would otherwise be credited and unspendable.
- **DONE** `launch.bas` raises the quote. 18 668 gas of 20 000 at maximum metadata — 1 332 spare.
- **DONE** launch wizard asks "Raised in"; label, presets, help and validation all name the chosen asset.
- **DONE** Markets lists a network category per wrapped asset, and names non-DERO markets as pairs.
- **DONE** a live `BRW / SIM` market trades on the simulator.

---

## 1. Correctness blockers — the things that are wrong *now*

**1.1 The revenue chain is DERO-only, so fees on XMR/ZEC markets are lost or miscounted. HALF DONE.**

*Done:* the treasury bot now fails closed — it reads each market’s `qt` and sweeps only DERO-quoted ones,
naming the rest and their outstanding `fd` rather than depositing wXMR as if it were DERO. Fees on wrapped
markets are now visibly owed instead of silently miscounted. `TestForeignQuoteMarketsAreNotSwept` guards it.

*Still open:* holders cannot yet receive those fees at all.
`founders.bas Deposit()` reads only `DEROVALUE()`; `fd`/`dep` are single DERO counters; `tools/treasurybot`
reconciles `fd` as DERO units. A wXMR-quoted market accrues `fd` in wXMR and hands the bot a number it treats as
DERO 1:1. Founders Pass holders are promised a share of *every* fee — this breaks that promise silently, which
is worse than breaking it loudly. Decide between: per-asset accounting in `founders.bas`; a treasury that
converts before depositing (needs a market and a price, so probably no); or Pass holders claiming each asset
separately. Whichever, the app must never show a fee total that mixes assets.

**1.2 `AddLiquidity`'s first-deposit floor is hardcoded to 100 000 quote units. NEXT.**
`pool.bas` line 140. Written when the quote was always DERO at five decimals. For 12-decimal XMR that is
0.0000001 XMR — dust, so the floor stops protecting anything. For a coarser quote it could be enormous. It needs
to scale with the quote's decimals, or become an install parameter with a sane bound.

**1.3 The app prices, sorts and totals everything as if it were DERO. NEXT.**
`amm.js` (`fmtDero`, `price`), `stats.js` (`priceAt`, `change`, `flow`, `enrich`, ranking), `app.js` (holdings,
pnl, equity), the tape, and every "for 0.5 DERO" string. A DERO price and an XMR price cannot be compared
without a rate and **there is no oracle and must not be one**. The honest design: never mix quotes in one
ranking; show each market in its own quote; make the quote part of every number's label; and if a cross-quote
view is ever needed, say plainly that it is not available rather than inventing it.

**1.4 NFTs are priced in DERO only. NEXT.**
`nft.bas` takes `price` and pays `SEND_DERO_TO_ADDRESS`. Same treatment as the other two. An XMR holder should
be able to mint and trade an NFT priced in XMR.

---

## 2. The wrapper — is the federation even the right design?

**2.1 Federation versus atomic swaps: reach a verdict. NEXT.**
The standing question and it has never been answered properly. Compare from primary sources: what Monero and
Zcash scripting actually permit, adaptor signatures, whether an HTLC is possible without timelocked scripts,
what COMIT / farcaster-core / serai achieved and where they stopped, latency, liquidity requirements, failure
modes, and precisely who can steal in each design. Prior work is in `research/crosschain/` and
`research/wrapper/`. Sources to `research/crosschain/SOURCES.md`, verdict to
`research/wrapper/MECHANISM_REVIEW.md`, with what would change the answer.

**2.2 The Monero backend against recorded wallet-rpc fixtures. NEXT.**
`Backend.VerifyRelease` currently checks the sim backend's own JSON file, which proves the plumbing and nothing
about Monero. A real backend must verify against a node it does not control.

**2.3 The Zcash/Orchard backend, and FROST signing. BLOCKED** on 2.2 landing first.

**2.4 `mindep` is an unrecoverable loss and no code can fix it.**
A ring-signature or shielded chain carries no return address, so a deposit below the minimum can never be sent
back. Disclosure is shipped. What is *not* written down is how to choose the value — that belongs in the
deployment runbook.

---

## 3. Security — assume a motivated attacker

**3.1 Adversarial pass over the quote-asset change itself. NEXT.**
It is new and it touched every value path in two contracts. What happens when the quote's own vault halts mid
trade? When a wrapped asset is slashed or a withdrawal vetoed? Can a market be created against a *fake* asset
that merely looks like wrapped XMR, and how would a user tell? What does an LP in a TOKEN/wXMR pool actually
hold when the federation fails?

**3.2 `chat.bas` is permanent and unmoderated by design.** Client-side mute, `clean()` and length caps ship. It
has never been attacked. Try: hostile room names, separator injection into the record, unicode that breaks the
nick column, a room with 100 000 messages, and whether a reader can be made to render something the poster did
not write.

**3.3 The remaining audit findings not yet applied**: the localStorage cache re-verification, `stats.enrich`
called for assets whose probe signature did not change, and the pre-seal bond lock's neighbours.

**3.4 Every UGC surface, again, now that screens have been rewritten.** Seven agents rewrote the app; the
escaping and length bounds need re-checking against the new markup rather than the old.

---

## 4. Usability — the on-ramp is the product

**4.1 Drive the deposit/withdraw walkthrough and fix what confuses. NEXT.**
It is new and has never been used by anyone. Walk it end to end on the simulator, including the failure paths:
a deposit below the minimum, a withdrawal that is vetoed, a vault that halts mid-flow.

**4.2 Buying a token quoted in wXMR requires wrapped units first.** Today a user discovers that only when the
trade fails. The trade sheet should say it up front and link to the Wrap screen.

**4.3 Three design critique passes over fresh renders, per `docs/DESIGN_SYSTEM.md`.** Does anything still look
AI-generated; could a screenshot be another exchange; does it say privacy, cryptography, markets, neutral zone
without cliché.

**4.4 Mobile widths 390 and 768 are still unasserted.** The UI test covers 430, 1024, 1280.

---

## 5. Infrastructure

**5.1 The scheduled-loop mechanism did not fire once in nine hours** (2026-09-08, 02:13 → 11:00) while the
machine was awake and the chain was mining. Do not rely on it again without proving it fires. Backgrounded
`go run` processes also died when their shell exited — `simtraffic` lasted 21 minutes of a 480-minute run.

**5.2 A DVM source validator now runs before anything reaches the chain** (ascending line numbers, no
duplicates). Extend it: `DIM` at the top of every function, no `IF … THEN <non-GOTO>`, every `LOAD` key written
somewhere at install.

**5.3 `app/templates` drifted from `contracts/` and the app was verifying stale code.** `simdeploy` now demotes
an outgoing hash to legacy automatically. Add a test that fails when the two directories differ.

**5.4 `wrap.js` is 13 156 DOC bytes of an 18 432 cap.** The largest file in the bundle. Watch it.

---

## 6. Product surface still missing for the target user

- Trading pairs **between** wrapped assets: wXMR/wZEC. Possible today — both are just assets — but nothing
  creates one.
- A portfolio that answers "what do I hold, in the asset I care about" without inventing a cross-rate.
- NFT collections priced and traded in XMR/ZEC (see 1.4).
- The deposit → trade → withdraw round trip as one continuous flow rather than three screens.
