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

## 0b. Nobody may take out more than was put in — the veto double-payment hole. DONE 2026-09-08.

A withdrawal is `st == 0` from the moment it is burned until k signers vote `Released`. A signer sends the
Monero in the middle of that window. Nothing on chain said so, so k signers could vote `Veto` in good faith and
hand the burner their units back as well: coins gone, units returned, reserve short by the amount for good. The
only record that a payment was under way was each signer's own local intent file, which the quorum cannot read.

`Paying(j)` puts it on chain before the money moves. `Veto` refuses for 2 000 blocks (~10 hours) after the mark
— long enough for any Monero or Zcash payment to confirm and for k signers to record `Released`, short enough
that nobody is stranded. The mark cannot be refreshed, so one signer cannot freeze a withdrawal for ever, and it
expires on its own, so a payment that is marked and then never made unfreezes without anyone's help.
`vaultsigner` sends `Paying` before `be.Release` and **fails closed**: no mark, no payment.

Proven, not assumed: `TestVetoCannotPayAWithdrawalTwice` fails with "the burner is paid twice" when only the
two guard lines are removed. Live on the simulator, vault `c32e6572`: signer 0 marked withdrawal 0, the quorum's
veto could not even be built ("Discarded knowingly"), state stayed `st 0 / sup 50000 / halted 0`, and the
payment then completed to 45 100. On withdrawal 1 the daemon marked at height 15074 and voted Released at 15076
with no prompting. `simvault -paying <j>` and `-veto <j>` drive it by hand.

**Comments are no longer charged as install gas.** The DVM charges storage gas on source bytes, comments
included, against 20 000 units. vault.bas sat at 19 400 with its documentation, leaving 771 — not enough for
this fix. Documentation that costs gas is documentation that gets deleted under pressure. `reference.Canon`
drops whole-line comments and blank lines at install time; the repo keeps the prose and the chain gets the code.
vault.bas installs at **15 765 gas on a real transaction, 4 235 spare**. pool.bas gives back 3 646 bytes,
launch.bas 1 817, founders.bas 2 239. `TestCanonOnlyDropsCommentsAndBlanks` proves every surviving line is
byte-identical and that no numbered statement can go missing.

This also fixed what template verification attests. It used to hash prose, so rewording a comment invalidated
every deployed market for no change in behaviour, and a user comparing hashes was being asked to trust
paragraphs. It now covers the executable source and only that. Templates and `config.js` regenerated; the four
outgoing hashes were demoted to legacy, so markets already deployed still verify.

*Still open on this front:* `deploy/networks.json`'s **testnet** record has been stale since the first
deployment (its `founders` hash `8afd70fb` is the simulator's *first* legacy entry), so `genconfig -dist` with
no `-network` fails on testnet. This predates the Canon change. It needs a testnet redeploy, not a record edit.

---

## 0c. A token calling itself WXMR listed with no warning at all. DONE 2026-09-08.

The name of a wrapped asset is the most valuable thing to steal here: the whole product points a Monero holder
at a ticker called XMR, so a token that simply calls itself WXMR is the cheapest attack there is. Installed one
(`tools/simscam`) and looked, rather than reasoning about it. The app listed it as
`WXMR / Wrapped Monero / Verified`, `dupTicker: false`, `lookalike: false`, no warning of any kind — while the
two **genuine** wrapped-XMR pools each carried "Ticker shared" for matching each other, and the real wrapped
Monero market said **"Pool verified · asset unknown"**. Every guard was pointing the wrong way: the copy looked
cleaner than the original, and the original was described as a stranger.

The impersonation guard only ever compared listed markets against each other, so a name with no duplicate had
nothing to collide with — and the wrapped assets are named in config, not in the market list, so they were not
in the namespace at all. Now each vault's symbol, name and `w` + symbol are reserved to the vault config names,
and the vault SCID is what settles who is genuine: nothing a creator types is evidence either way.

- `D.reservedBy(ticker)` / `D.impersonation(s)` in feed.js; the mark rides on the market as `s.impersonates`.
- A filled red `Not real XMR` pill (every other pill is an outline, so filled reads as louder without a new
  colour), the market row's ⚠ and title, and the first line of `H.risks`, in plain words: no vault holds any
  XMR for it and it cannot be withdrawn to XMR on any chain.
- The "Verified" badge no longer stands alone next to a copy. It now reads **"Code checked · name is not"**,
  because the code genuinely does match the template and that says nothing whatever about the name.
- The genuine article reads **"Wrapped asset · verified"**, citing the vault whose id ships with the app.
- Two genuine pools of one wrapped asset no longer flag each other; that is a split pair, which Markets already
  reports far more usefully. Fixed in feed.js and again in token.js, which recomputed it independently.

Guarded by an assertion in `tools/uitest/ui.mjs` that runs against synthetic markets, so it needs no fixture
and holds on any network. Verified it fails when the rule is removed.

*Trust root, written down:* the vault list is `config.js`, which ships inside the TELA bundle the user verifies
by its own hash — as trustworthy as the app itself, and the right place for it. What is **not** yet checked is
that a configured vault SCID's on-chain code matches a vault template; today a wrong config entry would go
unnoticed. That is an app-integrity gap, not an attacker path.

---

## 0d. Every price, liquidity and volume figure assumed DERO's five decimals. DONE 2026-09-08.

`A.price` divided the quote reserve by `1e5`, and `stats.js` did the same in five more places (`flow`,
`published`, `liq`, the candle volumes). Both simulator vaults were configured as five-decimal too, so nothing
ever looked wrong — but **real Monero is twelve decimals and real Zcash is eight**. On a real vault this would
have reported 0.431 XMR of liquidity as **4 310 000 XMR**, and a token priced in XMR wrong by a factor of ten
million, with no warning of any kind. `screens.js` already knew and carried a `10^(5 − quote decimals)`
correction to undo it at the display end — the right diagnosis applied at the wrong place, because every new
caller inherits the fault and has to remember the correction.

Fixed where the units are known: `A.price(s, dec, qdec)` and one `qunit(s)` helper in stats.js. The downstream
correction is gone (leaving it would double-correct). `app.holdings.total` and `change24` were **deleted**
rather than fixed — nothing read them and they summed wrapped XMR into DERO, which is not a quantity of anything
and cannot be made one without a rate. There is no oracle here and there must not be one.

**The simulator now runs XMR at 12 decimals and ZEC at 8**, so these paths are actually exercised and the demo
stops misrepresenting both chains. Verified against contract state by hand: the `BRW / XMR` pool holds
rd = 103 095 base units, and the app reports liquidity 2.0619e-7 XMR (= rd / 10¹² × 2) and price 1.2074e-11 XMR
per BRW — both exact. Before the fix the same pool would have read 2.0619 XMR.

*Checked and cleared:* `capratio` guards are written as `cap / capratio > bondtot`, never
`bondtot * capratio`, so they cannot overflow a uint64 — and `Slash`'s `STORE("cap", bt * capratio)` is only
reached when `bt < cap / capratio`, which bounds the product below `cap`. Deliberate, and worth not undoing.

*Discovered, still open:* **`capratio` is decimals-dependent and its documented default of 3 is only meaningful
for a five-decimal asset.** A real Monero vault deployed with it could wrap at most 1.8e-7 XMR — economically
nothing. An operator following the current runbook would deploy a vault that cannot hold anything. The
deployment runbook must state capratio per foreign chain, alongside `mindep`, `minburn`, `wfee` and `cap`,
all of which are in that chain's base units and all of which are wrong by 10⁷ if copied from the simulator.

---

## 0e. The vault state machine — no state needs a human to leave it. DONE 2026-09-08.

The audit reproduced three failures on the DVM, all contradicting `vault.bas`'s own header: a halt was a
boolean with no lease that blocked `Burn` (still frozen at +2,000,000 blocks); `Released` was refused while
halted but `Veto` was not, so a withdrawal already paid abroad could be refunded on chain once its `Paying`
mark lapsed; and a burn that never reached quorum had no way out. One change, designed as a machine rather than
three patches:

- **A halt is a lease.** `halted` now holds the height the lease lapses at (0 = open), 20 000 blocks from a
  `Halt(1)` quorum, renewable by voting again. A federation that stops answering cannot leave the vault halted.
  `halted > BLOCK_HEIGHT()` is the only test of "halted" — the contract (`ishalted()`), the daemon and the app
  all compare the same way, with no constant to mirror.
- **A halt never blocks the exit.** `Burn` and `Released` work while halted; only `Register`, `Attest` and
  `Mint` — the operations that add exposure — wait. The daemon starts no new payment while halted, but a
  payment already made still gets its `Released` vote, and a co-signer's idempotent lookup of a marked payment
  proceeds (the on-chain `Paying` mark is the signal that a `Release` call is a lookup, not a new payment).
- **`Reclaim(j)`** — an open burn no signer has ever marked returns its units and fee to the burner 20 000
  blocks after it became releasable. Permissionless, pays only `w_<j>_by`, reads no `SIGNER()` so it works at
  ring 16. A **marked** burn can never self-reclaim: its coins may already have left, and paying twice is worse
  than waiting for k votes.
- **The halt matrix is a test** (`TestHaltMatrix`), not eleven guard lines that could disagree.

Proven in the harness (`tests/vault_statemachine_test.go`, the real DVM): the lease is stored and lapses; a
renewal moves it; `Burn` and `Released` succeed while halted; `Reclaim` refuses too-early, refuses a marked
withdrawal, pays only the burner, refuses twice, and works above ring 2. Install gas **16 368 of 20 000** on a
real transaction. Proven live on vault `ab32063f`: halt stored as lapse height 35 533 at tip 15 534; a burn
accepted while halted (50 000 → 47 000); `Released` votes recorded by the contract while halted (heights 15 539,
15 541); and both withdrawals settled to `sup 43 200 = reserve 43 200`, in flight 0.

**Two pre-existing daemon liveness defects surfaced by that live run, both fixed:**

1. *A stranded vote.* Every vote is scoped by the halt epoch and the 2 000-block bucket. A `Halt` flip — or two
   signers whose passes straddle a bucket boundary — left one signer's `Released` vote in a dead scope while the
   co-signer voted in the live one, and the daemon treated "voted" as terminal so it never re-voted. Reproduced:
   two votes per signer on the same withdrawal, none counted. Now "voted" is re-examined every pass until the
   chain closes the withdrawal, and re-cast if the signer's key is absent from the live scope.
2. *A stale epoch.* After its own `Halt` vote completed a quorum, the daemon kept keying that pass's remaining
   votes with the epoch it had read at the start. It now refreshes `he` when a halt vote lands.

Also: `markPaying` refuses a lapsed mark (fail closed), and the verify-retry path no longer resets a "paid"
intent to "paying" under a lapsed mark — it keeps the record and keeps verifying, since a second payment is the
one outcome worse than a slow one (`TestALapsedMarkKeepsTheIntentPaidRatherThanPayingAgain`).

*Not unit-tested, verified live only:* the daemon's stranded-vote re-cast and epoch refresh live inside
`pass()`, which needs a chain. The live run above is the evidence.

*Still open from this cluster:* per-signer `Paying` marks (`SEC·RT-09`) — deliberately not done. A renewable
mark lets one signer freeze a withdrawal for ever; the one-shot mark plus `Released`-while-halted closes the
double-pay without that cost. `PROTO·BC-4` (`n ≥ 2k` at Seal) remains a decision for the operator.

---

## 0f. The audit follow-up, applied. 2026-09-08, second half.

Seven agents with disjoint file ownership verified every assigned finding against the code as it stood before
touching it; an eighth ran every test layer and read the money-path diffs. 41 findings FIXED, 4 ALREADY_FIXED,
1 FALSE_POSITIVE (a finding id that did not exist), the rest DEFERRED with a written design. The full ledger is
commit `ef3acac`; the two that matter most:

- **Every sell was being erased from the local ledger** and the whole cache discarded for any market with a
  published sell — two validators requiring `side ∈ {0,1}` against contracts that emit `{1,2}`. One constant.
- **The new JS test layer** (`tests/js/`, plain `node --test`, no dependency, vectors printed by the Go fuzz)
  found two quoting bugs in `amm.js` within minutes of existing: `impact()` divided by a spot price that
  floors to zero on a drained pool (a `RangeError` where the contract simply paid), and `launchBuy` mirrored
  the *old* overshoot refund. Both fixed; both tests are hard assertions now. This layer is the guard the audit
  said was missing between the DVM suite and the browser.

**Legacy vault records.** Switching the simulator to the real-decimal vaults orphaned every market quoted in or
based on the old ones: they rendered a hex label where a symbol belongs and scaled their numbers by a
placeholder 5 — wrong by 10⁷, the exact fault this project keeps finding. A vault record can now be
`legacy: true`: named and scaled by `D.allVaults()` everywhere a market is read, transactable (a holder of
superseded units must always be able to burn), and absent from `D.vaults()` — the Wrap picker and the launch
wizard's quote chips. The superseded simulator vaults are `XMR-old` and `ZEC-old` at their true five
decimals, so nothing is mistaken for the real thing. Same shape as a legacy template hash.

**The simulator now runs two real-decimal vaults** — Monero `f23403b0` at 12 decimals and Zcash `93013bb2`
at 8 — with the halt-lease/Reclaim state machine and parameters that make sense at those decimals
(`deploy/VAULT_PARAMETERS.md`). `XMR / DERO` reads 5.85 DERO per XMR with 2.16 DERO of liquidity;
`ZEC / DERO` 0.62 with 2.23. Cold-cache probe and the UI pass at 390/768/1024 confirm every number.

**A mistake of mine, on the record.** `simwrapmkt`'s new "already registered" check compared a decoded scid
against the hex of its ASCII form and never matched, so both new pools were registered on the factory a second
time before I caught it. The factory has no dedup and no removal (audit P7-04), so `pool_7` and `pool_8` are
permanent duplicates of `pool_5` and `pool_6` on this simulator (and `pool_1`/`pool_2` were already a pair
from before tonight). The app collapses by scid so nothing lists twice; the cost is P7-04's per-block RPC
amplification. The comparison is fixed and the tool now refuses to re-seed a pool that already has liquidity.

**Deferred, with designs written by the agents that found them** (see `docs/audit/DMZ_FABLE_5_1_FOLLOWUP.md`
and commit `ef3acac`): the `decorateOne` extraction (identity computed per screen — `token.js` still has a
drifted duplicate for the unlisted path, now at least naming its base through `D.nameBase`); display-name
impersonation and NFT name checks; split-pair detection on the trade path (the owner's own requirement, still
absent where the trade is signed); the shipped accepted-treasury set (`cfg.treasuries` — data layer done,
`networks.json` field not yet emitted); per-asset Founders accounting; the ring-16 `BurnTo` wrapper; the
`simrpc` `Try*` API for `vaultsigner`; and a Wrap-screen picker entry for a superseded vault so a holder can
burn old units from the UI rather than the CLI.

---

## 0g. The mechanism verdict, written; and three new threads. 2026-09-08, late.

**`research/wrapper/MECHANISM_REVIEW.md` now exists** — the standalone verdict the audit said was never
written. The analysis had been done (DESIGN.md Phase A, RESEARCH.md, the Phase 6 DECISION); what changed
tonight is a constraint the operator stated for the first time: **they will not host anything 24/7.** That does
not disqualify the federation, it disqualifies the *operator-run* federation. The verdict by pair:

- **Transparent Zcash ↔ DERO: trustless HTLC, no host, works on mainnet DERO today.** `swap.bas` + `board.bas`
  exist and ran. The one pair DMZ can honestly put on mainnet first. Privacy cost: the ZEC leg is transparent.
- **Monero ↔ DERO, trustless: not shippable yet.** The DVM has no curve or signature ops, so the COMIT/Farcaster
  adaptor-signature swap does not apply; the key-share consistency proof is prototyped (`poc/proof-xmr`) but the
  escrow, companion and stagenet runs are unbuilt. **Monero on DMZ today means the federation**, run by
  third-party signers, never called trustless.
- **NFA (`civilware/artificer-nfa-standard`, ART-NFA-MS1): adopt it.** Headers, ownership as one unit of the
  contract's own asset, built-in marketplace, royalties — all DERO-priced; extend with a `quote` parameter the
  same way pool.bas and launch.bas were. Replaces `nft.bas`; every DMZ asset then indexes in Engram/Gnomon.
- **spore (formerly mycelium, HEAD `254beba`): adopt as the maker/taker rendezvous channel** for board offers
  and swap negotiation — serverless, on DERO mainnet now. Not a bridge; its Monero backend is mock-only on a
  deprecated payment-id seam and must not be reused.

**Mainnet sequence:** ZEC-transparent swap market → NFA assets → federation only with named third-party
signers (`n ≥ 2k`) → the trustless Monero path.

*The loop.* Three scheduled loops "never fired" for one reason: a cron job runs only while the session is
idle, and the turn never ended. Job `fe29e0cd` (20 min) carries the cycle text and these threads; each cycle
must end its turn.

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

**1.2 ~~`AddLiquidity`'s first-deposit floor is decimals-blind~~ — WRONG, checked and withdrawn.**
`pool.bas` line 140 requires the first deposit to be at least 100 000 quote units, and line 150 mints
`d - 1000` LP with 1 000 locked forever. Both numbers are in the SAME units — the quote's — so the floor is a
ratio, not a value: it guarantees the permanently locked liquidity is at most 1 % of the first deposit, and that
holds identically for a 12-decimal XMR quote and a 5-decimal DERO one. It never was a value floor and needs no
decimals. I recorded this as a bug from the shape of the constant without reading what it guards.

What IS true, and is a different point: nothing anywhere requires a pool to hold economically meaningful
liquidity, so a market can be created with dust and its price is then meaningless. That is equally true of DERO
markets today and is a product decision (a minimum, a warning, or a ranking that ignores thin pools), not a
quote-asset bug.

**1.3 The app prices, sorts and totals everything as if it were DERO. PART DONE.**

*Done:* `D.qsym(s)` and `D.qdec(s)` name a market's quote once, and every label that describes a market amount now
uses them — the token page's price eyebrow, reserve, graduation progress and buyback; the trade sheet's units, fee,
tax and refund rows; the thin-market warning; the launch pipeline; the market row's sub line. A wXMR market no
longer claims its prices are DERO.

*Still open:* sorting and ranking still compare across quotes as though the numbers were commensurable —
`select()` orders by `m.liq`, `m.vol24` and `m.mcap` regardless of quote, so a market quoted in a 12-decimal
asset sorts against a 5-decimal one on raw magnitude. Portfolio totals, P&L and the equity curve sum across
quotes the same way. The tape mixes them too. None of this can be fixed with a rate — there is no oracle. The
answer is to rank and total WITHIN a quote and never across.
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
