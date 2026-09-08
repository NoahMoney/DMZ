# NFA on DMZ — what one is, what stays standard, what is extended, and what can go wrong

**Written 2026-09-08 against `docs/NFA_BUILD.md` (the build specification; its §1 is the design this page
describes) and against the standard itself: ART-NFA-MS1 v1.1.1, `ART-NFA-MS1.bas` at
`civilware/artificer-nfa-standard` HEAD `d286b19`, sha256
`a5e4ad654acb92bdca8d7e951173d6dae2f4f75f55772b9bedaabbac8499b174`.** This is the page a creator reads before
minting, a buyer reads before bidding in wXMR, and a reviewer reads before accepting the word "compatible".
Nothing on this page is running: `contracts/nfa.bas` did not exist when it was written, so every rule below is
read off the standard's source (line numbers are the BASIC line numbers inside the named function) and the
build spec; every number has a primary source (§9) or is computed in full on the page; OPEN marks a question
this page cannot close. Where the build spec's own wording was overtaken by a change made to the vault the same
day, this page follows the vault and says so (§7 R3).

---

## 0. In one paragraph

An NFA is one DERO contract per asset. The contract issues exactly one unit of its own asset at install and
hands it to the creator; whoever holds that unit is the owner, and the contract records the owner's address
when the holder proves it by sending the unit through `ClaimOwnership()`, which sends it straight back. The
market is inside the same contract: the owner starts a sale or an auction by depositing the unit; a buyer pays
the price, or bidders bid and the highest wins when the clock runs out; the contract pays the creator's
royalty, an optional donation to the standard's authors, an optional charity share and — on DMZ — the protocol's
share to the treasury, and gives the rest to the owner, in the same call that hands the unit to the buyer. The
standard prices all of that in DERO. DMZ's one extension is a `quote`: the asset the price is in, which is DERO
(the zero hash) or a wrapped asset such as wXMR. Priced in DERO, a DMZ NFA behaves exactly as the standard's
contract, and every DERO tool that reads the standard reads it. Priced in a wrapped asset it is a DMZ extension:
the same headers and the same state machine, but a buyer who sends DERO is refused, and what the seller
receives is a claim on a federation's reserve, not a coin (§7 R3).

---

## 1. What an NFA is, in plain words

### 1.1 One contract, one unit, one owner

Installing the contract runs `InitializePrivate`, which stores the metadata and calls `init()`. `init()` records
the installer as both `owner` and `creatorAddr` (`init` 20–30) and sends one unit of the contract's own asset —
`SEND_ASSET_TO_ADDRESS(SIGNER(), 1, SCID())`, `init` 600 — to the installer. That unit is the asset. There is
never a second one: nothing in the contract mints, and the unit either sits in one wallet or in the contract
while it is listed (build spec §4, invariant 3).

The unit moves in two ways. Through the market (§1.2), where the contract itself hands it to the buyer and
records the buyer as `owner` in the same call. Or by a plain private DERO transfer from one wallet to another,
which the contract cannot see; the new holder then calls `ClaimOwnership()` with the unit attached, the
contract checks `ASSETVALUE(SCID()) == 1` (`ClaimOwnership` 10), writes the caller as `owner` and the old owner
as `previousOwner` (`transferOwnership` 20–30), and sends the unit back (`ClaimOwnership` 40). Ownership is
therefore two things that can briefly disagree: the wallet that holds the unit, and the address the contract
has recorded. The unit is the truth; the record follows it (§7 R2).

### 1.2 The marketplace is inside the contract

The owner starts a listing with `Start(listType, duration, startPrice, charityDonateAddr, charityDonatePerc)`,
attaching the unit: `listType` is `"sale"` or `"auction"`, `duration` is in hours (0 means one hour; anything
above 168 is cut to 168 — `generateEndBlock` 30–60), `startPrice` is in atomic units of the quote, and the
charity address and percentage are chosen per listing. A sale ends when a buyer calls `BuyItNow()` with at
least `startPrice` attached (`BuyItNow` 60); the unit, the payment and the ownership record all change in that
one call (70–96). An auction takes `Bid()` calls with the bid attached; each accepted bid refunds the previous
highest bidder in the same call (`Bid` 100 → `outbidReturns`), and the next bid must be at least the lesser of
+10 % and +10,000 atomic units above it (`findLesserIncrease`). A bid in the last fifteen minutes moves the end
to fifteen minutes from now (`Bid` 170–180). When the end has passed, the owner calls `CloseListing()` and the
highest bidder gets the unit and the owner the money (`processHighestBidder`) — or anyone but the owner calls
`Bid()` with nothing attached, which settles it the same way (`Bid` 500–550). Within `cancelBuffer` = 300 seconds of
`Start` the owner can `CancelListing()`, which refunds the current highest bidder and takes the unit back
(`CancelListing` 60, 460–600). §4 walks through every state.

### 1.3 Headers, the file and its signature, the collection

Five stored strings — `nameHdr`, `descrHdr`, `typeHdr`, `iconURLHdr`, `tagsHdr` — are the headers standard
that Engram, Gnomon and dReams index; `coverURL` is a larger preview. `fileURL` points at the file the NFA is
about and `fileSignURL` at the `.sign` file the creator produced with the DERO wallet's `filesign` command;
`fileCheckC` and `fileCheckS` are the two values from inside that signature, fixed at install, so anyone can
check that the file at `fileURL` is the one the creator signed even after the URL has been changed
(`Update()` lets the creator — and the owner too if `ownerCanUpdate` is 1 — change the icon, cover, file and
signature URLs and the tags, but never the name, description, type, the two check values, or the collection).
`collection` is a string set at install and never changed; the DMZ app groups NFAs by it (§7 R8).

### 1.4 What DMZ has today, and why it is not this

`contracts/nft.bas` is a holder-earned badge: lock some of a token for a holding period, then mint an "edition"
for a DERO price. An edition is a storage entry (`ed_<n>` = the minter's address), not an asset; it cannot be
transferred, cannot be listed, and has no owner record that could change. It carries the same header names as
the standard and nothing else of it. It stays as it is, under its own name and its own template key, until
someone decides to retire it (build spec §1); nothing on this page changes it.

---

## 2. What DMZ extends, and what it keeps

### 2.1 `quote` — the asset the price is in

A 64-character hex SCID given at install, or the empty string for DERO; the contract stores its 32-byte form
under `quote` (the zero hash for DERO). Every place the standard reads "the money offered" as `DEROVALUE()`
reads `ASSETVALUE(quote)` instead, and every place it pays or refunds with `SEND_DERO_TO_ADDRESS(x, n)` pays
with `SEND_ASSET_TO_ADDRESS(x, n, quote)` — the exact list is §5.2. This is the same extension `pool.bas` and
`launch.bas` already carry (`pool.bas` header, lines 2–4).

Why a DERO-quoted NFA is not merely "similar" to the standard: in the DERO VM the two builtins are the same
lookup. `DEROVALUE()` returns `dvm.State.Assets[dvm.State.SCIDZERO]` and `ASSETVALUE(h)` returns
`dvm.State.Assets[h]` (`dvm/dvm_functions.go` 434–455); `SEND_DERO_TO_ADDRESS(a, n)` calls
`SendExternal(scid, zerohash, a, n)` and `SEND_ASSET_TO_ADDRESS(a, n, h)` calls `SendExternal(scid, h, a, n)`
(377–431); the `DERO()` builtin is the zero hash (284–288). With `quote` = zero hash the extended lines evaluate
to exactly the standard's lines.

On a wrapped quote the contract additionally refuses any market call that carries DERO (`DEROVALUE() != 0`),
as `pool.bas` does at `AddLiquidity` 42 and at line 71 of `RemoveLiquidity`, `buyto` and `sellto`, so a buyer
who attaches DERO to a wXMR listing by mistake is refused rather than having the DERO kept (§7 R9).

### 2.2 `protoFee` and `treasury` — the protocol's share

A fee in basis points and the address that receives it, both fixed at install (`protoFee ≤ 10000`; the
treasury must be a valid address). At settlement the contract pays `protoFee` of the sale, in the quote, in the
same call as the royalty and the charity share. The rule that the shares fit is
`protoFee + royalty·100 + artificerFee·100 + charityDonatePerc·100 ≤ 10000` (build spec §1). This is how NFA
sales feed the Founders Pass (`founders.bas`, `contracts/README.md` "founders.bas"), and it lands on an open
item: fees earned in a wrapped asset cannot yet reach Pass holders (`docs/TODO.md` §1.1; §6.6 here).

### 2.3 Kept exactly

Every header name; `ClaimOwnership`; the listing state machine and its constants — `cancelBuffer` 300 s,
`staticBidIncr` 10,000, `percentBidIncr` 1,000 (= 10 %), the 900-second anti-snipe extension, the 604,800-second
cap on a listing's initial duration; the royalty and `artificerFee` split, with `artificerFee` defaulting to 0
and `artificerAddr` hard-coded to the standard's authors (`init` 40); `version` `"1.1.1"`. The standard's
comments stay in the source as documentation; `reference.Canon` strips them at install (`reference/canon.go`),
so they cost no gas.

### 2.4 Dropped: the holder-gated mint

`nft.bas` mints an edition to whoever held a token long enough. An NFA is a contract, and the DERO VM cannot
install, call, read or fund another contract (`docs/ARCHITECTURE.md`, "Why one contract per launch"), so no
contract can mint an NFA for a holder on demand. Under the standard, the creator installs each asset. That is
recorded as open rather than solved (§7 R8).

---

## 3. The names the contract uses

| Key | Set | Meaning |
|---|---|---|
| `owner`, `previousOwner` | `init`; every settlement; `ClaimOwnership` | the recorded owner, and the one before |
| `creatorAddr` | `init` | receives `royalty` |
| `artificerAddr`, `artificerFee` | `init`; install | the standard's authors and their percentage (default 0) |
| `royalty`, `ownerCanUpdate` | install | creator's percentage; whether the owner may `Update` |
| `nameHdr descrHdr typeHdr iconURLHdr tagsHdr coverURL fileURL fileSignURL fileCheckC fileCheckS collection` | install (`Update` for the URLs and tags) | §1.3 |
| `quote`, `protoFee`, `treasury` | install — **DMZ** | §2.1, §2.2 |
| `active`, `scBalance`, `listType`, `startBlockTime`, `endBlockTime`, `startPrice` | `Start`; `resetVars` | the listing: `scBalance` = 1 while the unit is in the contract; `listType` `"sale"`, `"auction"` or `""` |
| `charityDonateAddr`, `charityDonatePerc` | `Start`; `resetVars` | this listing's charity share |
| `currBidPrice`, `currBidAddr`, `currBidAmt`, `bidCount`, `<addr>-bidDate` | `Bid`; settlement | minimum next bid; the highest bidder and their bid; one bid per address per block timestamp |
| `cancelBuffer` 300, `staticBidIncr` 10000, `percentBidIncr` 1000 | `init` | §2.3 |
| `previousSalePrice`, `previousAuctionPrice` | settlement | the last price paid, in the quote's units |
| `version` `"1.1.1"` | `init` | the standard's version |

`Start` is the only call that writes `startBlockTime` and `endBlockTime`; `resetVars(1)` zeroes the listing
keys after every settlement or cancel but leaves `currBidAddr` and `currBidAmt` to `processHighestBidder` and
`outbidReturns`, which `DELETE` them — a detail that matters in §7 R4.

---

## 4. The listing, step by step

`checkActive(t)` is the one function every market call asks. It returns **0** if the listing has started, the
unit is in the contract, the end has not passed and the stored `listType` equals `t`; **111** if the listing has
started and the unit is in the contract but the end has passed — *before it compares the type*, `checkActive`
40 runs ahead of 50; and **999** otherwise, including "no listing" and "wrong type while live".

| Call | Who | Live listing | Ended listing | No listing |
|---|---|---|---|---|
| `Start` | owner, unit attached, ring 2 | refused — the unit is already in the contract, so nothing can be attached (`Start` 40 → 400 → 999) | refused the same way | listing begins; unit stays in the contract |
| `BuyItNow` | anyone but the owner, ring 2 | sale: pays at ≥ `startPrice`, unit to buyer, money split (`BuyItNow` 60–97); auction: money refunded (`BuyItNow` 40 → 500 → 920) | **unit back to the owner, listing reset, caller refunded — for a sale or an auction** (510–540; §7 R4) | money refunded |
| `Bid` | anyone but the owner, ring 2 | auction: accepted at ≥ `startPrice` and ≥ `currBidPrice`, previous bidder refunded (`Bid` 70–100); sale: refunded | caller refunded, then `processHighestBidder` — the highest bidder wins, or the unit returns to the owner if there was none (520–550) | refunded |
| `CloseListing` | owner | refused (`CloseListing` 20) | auction with bids: highest bidder wins (50); otherwise unit back to owner (210) | refused |
| `CancelListing` | owner, within 300 s of `Start` | highest bidder refunded, unit back to owner (460–620) | refused (50) | refused |
| `ClaimOwnership` | holder of the unit, ring 2 | impossible: the unit is in the contract | impossible | owner record set to the caller, unit returned (10–50) |

"Refused" means the function returns 1; the DVM then discards every change the call made and the node returns
the call's attached value to the signer — but only when it can extract a signer, which is only at ring size 2
(`blockchain/transaction_execute.go` 395–404 and 422–430; `dvm/sc.go` 278–288). At any larger ring the value is
credited to the 33-zero-byte key, which nobody holds (`dvm/simulator.go` 244–279): the standard's README says
so in its own words for `BuyItNow` and `Bid` — "will just burn". Every market call on DMZ therefore goes at
ring size 2; the app already does this for every call that pays the signer (`docs/ARCHITECTURE.md`, "Writes").

The two guards that make settlement safe are the standard's own. Settlement pays the previous bidder or the
seller *and* moves the unit in one call, so there is no state in which the money has moved and the unit has not.
And a refused bid is refunded before the function returns 0 (`Bid` 920–930), never before a `RETURN 1` — the
shape `tests/sendorder_test.go` checks mechanically across every contract, because a send that can still reach
a non-zero return is a send the DVM will make twice once a wrapper exists (that test's header explains the
2,052,806-unit drain that taught it).

---

## 5. Compatibility statement

### 5.1 A DERO-quoted DMZ NFA is a standard ART-NFA-MS1 asset

With `quote` = the zero hash, every extended line evaluates to the standard's line (§2.1), the `DEROVALUE() != 0`
refusal never fires (DERO *is* the quote), and — provided `protoFee` is 0 — the settlement arithmetic is the
standard's, unit for unit; with `protoFee` > 0 the split has one more recipient, which is a DMZ deployment
choice and not a change of mechanism. Build spec §5.A tests the settlement amounts against the standard's own
arithmetic. The state machine, the constants, the header names and `version` are unchanged, so a tool that reads
headers indexes it; a wallet that calls `Bid` or `BuyItNow` with DERO attached settles it; `ClaimOwnership`
after a private transfer works from any DERO wallet at ring size 2.

Two qualifications, so that "compatible" is not read as "identical":

- **The source text differs.** `InitializePrivate` takes arguments, `init()` validates and stores three more
  keys, and the market functions read `ASSETVALUE(LOAD("quote"))`. A tool that verifies an NFA by comparing its
  code to the standard's template text — rather than by reading its headers and calling its functions — would
  not verify a DMZ NFA, DERO-quoted or not. OPEN: which of Engram, Gnomon and dReams, if any, verify by source.
  Not checked here.
- **The install call differs.** The standard's `InitializePrivate()` takes no parameters; the metadata is edited
  into the `STORE` lines before install (standard README, "Initialization"). DMZ's takes the metadata and
  `quote, protoFee, treasury` as arguments, so it is installed by DMZ's mint form or by anyone who supplies
  them, not by Artificer's flow. After install the stored keys are the same.

### 5.2 A wrapped-quoted DMZ NFA is a DMZ extension — exactly which calls differ

Every line below reads the quote instead of DERO. `q` is `LOAD("quote")`.

| Function | Standard line | Standard | DMZ |
|---|---|---|---|
| `BuyItNow` | 60 | `IF DEROVALUE() >= LOAD("startPrice")` | `IF ASSETVALUE(q) >= LOAD("startPrice")` |
| `BuyItNow` | 90 | `processDEROFinalPayment(DEROVALUE())` | `…(ASSETVALUE(q))` |
| `BuyItNow` | 97 | `STORE("previousSalePrice", DEROVALUE())` | `…ASSETVALUE(q)` |
| `BuyItNow` | 920–925 | `IF DEROVALUE() > 0 … SEND_DERO_TO_ADDRESS(SIGNER(), DEROVALUE())` | `IF ASSETVALUE(q) > 0 … SEND_ASSET_TO_ADDRESS(SIGNER(), ASSETVALUE(q), q)` |
| `Bid` | 25 | `LET bidAmt = DEROVALUE()` | `LET bidAmt = ASSETVALUE(q)` |
| `Bid` | 530, 925 | `SEND_DERO_TO_ADDRESS(SIGNER(), bidAmt)` | `SEND_ASSET_TO_ADDRESS(SIGNER(), bidAmt, q)` |
| `outbidReturns` | 60 | `SEND_DERO_TO_ADDRESS(LOAD("currBidAddr"), LOAD("currBidAmt"))` | `SEND_ASSET_TO_ADDRESS(…, q)` |
| `processDEROFinalPayment` | 70, 90, 110, 130 | `SEND_DERO_TO_ADDRESS(creator / artificer / charity / owner, …)` | `SEND_ASSET_TO_ADDRESS(…, q)`, plus one send of `protoFee` to `treasury` |
| `BuyItNow`, `Bid` | new guard | — | `IF q != DERO() && DEROVALUE() != 0` → refuse (§7 R9) |

Unchanged in signature and in behaviour: `ClaimOwnership`, `Update`, `Start`, `CloseListing`, `CancelListing`.
Unchanged in signature but settling in the quote: `CloseListing` (through `processHighestBidder`) and
`CancelListing` (through `outbidReturns`).

What this means for another DERO tool, concretely:

- A tool that **reads headers** still indexes the asset; nothing in the headers changed.
- A tool that **pays DERO** to `BuyItNow` or `Bid` on a wXMR-quoted NFA is refused: the new guard returns 1, the
  DVM discards the call, and the node returns the DERO to the signer (ring size 2). The buyer loses a fee and
  keeps the coins; they cannot buy.
- A tool that **reads a price** — `startPrice`, `currBidPrice`, `currBidAmt`, `previousSalePrice`,
  `previousAuctionPrice` — and labels it DERO misreads it by whatever the rate is and by the decimals: those
  numbers are atomic units of the quote, and wXMR has twelve decimals to DERO's five. DMZ's own app names every
  amount by the quote (`app/feed.js` `nameQuote`, `qsym`, `qdec`; build spec §3.C, invariant 4).
- A tool that **settles** a listing — `CloseListing`, or `Bid` after the end — makes the contract pay in the quote.
  That works from any wallet; what the seller then holds is §7 R3.

The `quote` key itself is the tell. A tool that wants to know whether an NFA is standard reads `quote`: absent
means a plain standard contract; the zero hash means DERO-quoted DMZ; anything else means a wrapped quote, and
the SCID it holds is the vault whose unit the price is in.

---

## 6. The fee split, with a worked example to the unit

### 6.1 Who sets what, and when

| Share | Set by | When | Recipient |
|---|---|---|---|
| `royalty` (percent) | the creator | install, never changed | `creatorAddr` |
| `artificerFee` (percent) | the creator | install, never changed | `artificerAddr` — the standard's authors (§7 R7) |
| `charityDonatePerc` (percent) | the owner | each `Start` | `charityDonateAddr`, chosen with it |
| `protoFee` (basis points) — **DMZ** | the deployment | install, never changed | `treasury` |
| the remainder | — | — | `owner` at the moment of settlement |

### 6.2 The arithmetic

The standard's `processDEROFinalPayment(saleAmt)` computes, in uint64 with integer division:

```
royaltyPaymt   = royalty            * saleAmt / 100      (65; sent only if > 0, 66–70)
artificerPaymt = artificerFee       * saleAmt / 100      (85; 86–90)
charityPaymt   = charityDonatePerc  * saleAmt / 100      (105; 106–110)
payoutAmt      = saleAmt − royaltyPaymt − artificerPaymt − charityPaymt   (120; sent if > 0, 125–130)
```

DMZ adds `protoPaymt = protoFee * saleAmt / 10000`, sent to `treasury` before the remainder, and subtracts it in
`payoutAmt`. Each share is floored separately; the owner receives the remainder, so the fractions that flooring
drops are not lost — they stay with the owner. The sum of the sends is `saleAmt` exactly (invariant 2). Nothing
in this arithmetic knows the quote's decimals: the same lines split 5-decimal DERO and 12-decimal wXMR.

### 6.3 Example A — a DERO-quoted sale

Price paid 12,345,678 atomic units = 123.45678 DERO. `royalty` 7, `artificerFee` 0, `charityDonatePerc` 3,
`protoFee` 125 bps (1.25 %). Fit: 125 + 700 + 0 + 300 = 1,125 ≤ 10,000.

| Share | Exact | Floored (sent) | Dropped |
|---|---|---|---|
| royalty 7 % | 7 × 12,345,678 / 100 = 864,197.46 | **864,197** to `creatorAddr` | 0.46 |
| artificer 0 % | — | nothing (line 80 skips) | — |
| charity 3 % | 3 × 12,345,678 / 100 = 370,370.34 | **370,370** to the charity | 0.34 |
| protocol 1.25 % | 125 × 12,345,678 / 10,000 = 154,320.975 | **154,320** to `treasury` | 0.975 |
| owner | 12,345,678 − 864,197 − 370,370 − 154,320 | **10,956,791** to `owner` | (+1.775 kept) |

Check: 864,197 + 370,370 + 154,320 + 10,956,791 = 12,345,678. The owner receives 109.56791 DERO; the three
fractions, 1.775 units together, are inside that figure.

### 6.4 Example B — a wXMR-quoted sale

Price paid 1,234,567,890,123 atomic units = 1.234567890123 XMR (wXMR has Monero's twelve decimals). `royalty` 7,
`artificerFee` 1, `charityDonatePerc` 3, `protoFee` 125. Fit: 125 + 700 + 100 + 300 = 1,225 ≤ 10,000.

| Share | Floored (sent), in wXMR units | In XMR |
|---|---|---|
| royalty 7 % | **86,419,752,308** | 0.086419752308 |
| artificer 1 % | **12,345,678,901** | 0.012345678901 |
| charity 3 % | **37,037,036,703** | 0.037037036703 |
| protocol 1.25 % | **15,432,098,626** | 0.015432098626 |
| owner (remainder) | **1,083,333,323,585** | 1.083333323585 |

Check: 86,419,752,308 + 12,345,678,901 + 37,037,036,703 + 15,432,098,626 + 1,083,333,323,585 =
1,234,567,890,123. Every one of these is a send of the vault's asset, `SEND_ASSET_TO_ADDRESS(…, q)`; no DERO
moves in this call except the gas the buyer paid the network.

### 6.5 The bound, and what happens above it

Percent times amount is a multiplication in uint64, and the DVM wraps silently (build spec, "uint64 only,
silent wrap"). `protoFee * saleAmt` wraps when `saleAmt ≥ 2^64 / protoFee`: at the maximum 10,000 bps that is
1,844,674,407,370,955 units, which is 18.4 billion DERO (impossible) but **1,844.67 XMR** at twelve decimals
(not impossible). At 125 bps the bound is 147,573.95 XMR; at 250 bps, 73,786.98 XMR. The standard's own
`royalty * saleAmt` wraps at 184,467 XMR for a 100 % royalty. A wrapped product makes the fee tiny and the
owner's remainder huge — `payoutAmt` is a subtraction that then wraps the other way — and the DVM's
over-send check refuses the whole call (`SanityCheckExternalTransfers`, `transaction_execute.go` 391–393), so
the sale cannot complete and the buyer is refunded at ring 2: not a theft, a listing that can never settle.
`vault.bas` computes the same basis-point product without wrapping —
`(sup / 10000) * bigbps + ((sup % 10000) * bigbps) / 10000`, line 30 of `Burn` — and gives the same result
(checked on Example B: 15,432,098,626 both ways). §7 R10 asks for that form.

### 6.6 Where the protocol's share goes

`treasury` is the factory's treasury address, the same one every pool and launch pays. On a DERO-quoted NFA the
share is DERO and the treasury bot sweeps it to `founders.bas` `Deposit()` for Pass holders. On a wrapped quote
it is wXMR, and the bot fails closed: it reads each contract's quote and sweeps only DERO-quoted ones, naming
the rest and what they hold (`tools/treasurybot/chain.go` `Quote`; `TestForeignQuoteMarketsAreNotSwept`), because
`founders.bas` reads only `DEROVALUE()` and would count wXMR as DERO one to one. So a wrapped-quote NFA's
protocol fee is counted, visible, and owed — and not yet payable to holders. That is `docs/TODO.md` §1.1, not
this page, to close; the rule this page carries is invariant 4: the app must never show that fee summed with
DERO.

---

## 7. Adversarial review

Each item: the situation, what happens, and whether the build spec already covers it. "Covered" means a rule
or test in `docs/NFA_BUILD.md` produces the stated outcome; "partly" and "not covered" say what should change.
Nothing in this section was run: every outcome is read off the standard's source and the DVM's, with the line.

**R1. `ClaimOwnership` with a fake unit.** The check is `ASSETVALUE(SCID()) == 1` (`ClaimOwnership` 10): the
value the DVM credited to the call under the contract's *own* asset id. The node fills that map from the
transaction's payloads by the asset actually spent; a caller cannot label one asset as another. So (a) a unit of
some other asset attached: `ASSETVALUE(SCID())` is 0, the call returns 1, the DVM discards it and the node
returns the attached asset to the signer at ring 2. (b) Two units: there is only one (invariant 3). (c) Nothing
attached: refused. (d) The real unit, but at ring size > 2: `ADDRESS_STRING(SIGNER())` is `""` (`SIGNER()` is
33 zero bytes above ring 2, `contracts/README.md` rule 5), line 20 jumps to 500, and the unit is sent to the
**recorded owner** — which is the previous owner if this was a private transfer. The claimant loses the unit to
the person they bought it from; the standard's README says so ("1 asset sent back to current owner"). *Covered:*
(a)–(c) by the contract; (d) by build spec §3.C only implicitly (every market action through XSWD at ring 2). The
app must pin `ClaimOwnership` to ring size 2 and must not offer a private mode for it; that should be said in
§3.C.

**R2. A listing started while a private transfer is in flight.** Owner O sends the unit privately to P; before
P claims, `owner` still reads O. Cases. (a) O calls `Start` after the transfer is mined: O no longer holds the
unit, `ASSETVALUE(SCID()) == 1` fails (`Start` 40), 400 finds nothing attached, the call returns 1. O cannot
list what O does not hold. (b) P calls `Start` with the unit before claiming: `LOAD("owner") == SIGNER()` fails
(100), 400 returns the unit to P. P must `ClaimOwnership` first — one extra transaction, no loss. (c) O's
`Start` and O's transfer to P in the same block: the unit is spent once; whichever the block orders second fails
its balance proof or its asset check. Exactly one lands. (d) While `owner` still reads O, O can still call
`Update` if `ownerCanUpdate` is 1 (creator always can): the URLs and tags can change under P until P claims. The
name, description, type, `fileCheckC`/`fileCheckS` and `collection` cannot. *Covered:* yes; the unit decides,
the record follows. The app should tell a holder whose wallet has the unit but is not the recorded owner to
claim before doing anything else — §3.C lists "Claim ownership (if the wallet holds the unit)", which is the
right hook.

**R3. Bidding on a wrapped-quote NFA whose vault is halted.** The build spec words this as "the quote cannot be
withdrawn". Since `vault.bas` v3's state-machine change of the same day that is no longer what a halt does
(`docs/TODO.md` §0e; `vault.bas` header lines 4–9): a halt is a lease, `halted` holds the height it lapses at
(20,000 blocks from the vote, about 4.2 days at DERO's 18-second block time, renewable by another k-vote), and it
blocks only `Register`, `Attest` and `Mint` — the operations that add exposure. `Burn` and `Released` work while
halted. What the halt does change is off chain: the signers' daemons start no new foreign payment while it
stands. So: the NFA contract never reads the vault and does not know a halt exists; `Bid` accepts wXMR as on any
other day; an outbid bidder is refunded in wXMR in the same call; the winner gets the unit and the seller gets
wXMR. *What the seller (or an outbid bidder) holds* is wXMR: a claim on one piconero each, held by a k-of-n
federation (`research/wrapper/README.md` §1). They can burn it on DERO at once; the XMR arrives when the halt
lapses or is lifted and the federation pays, and if no signer ever marks the withdrawal, `Reclaim` returns the
wXMR to them 20,000 blocks after it became releasable (`vault.bas` `Reclaim` 10–21). What they do not hold is a
coin: if k signers collude, the reserve is gone and the wXMR is worth nothing, and the NFA contract cannot undo
the sale. Accepting a wrapped quote is accepting the federation's credit — the wrapper's trust statement,
restated for a seller. *Covered:* **partly.** The contract is right to proceed; the page's job is to say what is
held, done above. §3.C should show the vault's state next to a wrapped price (the app already resolves a
market's vault, `app/feed.js` `vaultOf`, and every screen compares `halted > height` the same way), so a bidder
bids on a halted reserve knowingly. The spec's "cannot be withdrawn" wording should be corrected to "the payment
abroad waits".

**R4. The anti-snipe extension and the 7-day cap — and the close that strands a bid.** Two separate things.

*The extension is not capped.* `generateEndBlock` caps the *initial* duration at 604,800 seconds (50–60) and is
called only from `Start` (240). `Bid` 170–180 then moves `endBlockTime` to `BLOCK_TIMESTAMP() + 900` on every
bid that lands within 900 seconds of the end, with no reference to the cap. An auction whose last fifteen
minutes keep receiving bids runs for as long as they do. Two cooperating bidders can keep it open indefinitely:
each outbid is refunded in the same call, so their capital is never locked beyond one bid, and their cost is one
transaction fee per fifteen minutes between them. On a DERO quote the price they must keep meeting rises by
0.1 DERO a bid (`staticBidIncr`, the lesser increment above 1 DERO), so the top bid grows by at least 9.6 DERO
a day (96 bids) and the game gets expensive; on a wXMR quote the same 10,000 units are 0.00000001 XMR (R11), so it costs fees only. The
owner cannot end it: `CloseListing` needs 111 (ended) and `CancelListing` needs the first 300 seconds. The
timestamps are the block's (`BLOCK_TIMESTAMP()` is the block timestamp in seconds, `blockchain.go` 959,
`dvm.go` 401), so no clock of the parties' is involved. *Covered:* **not covered**, and by the standard's design
— "kept exactly" (build spec §1) keeps this. What to say: the listing page must show the end as "extends while
bids arrive", not as a date; and the operator has a choice — keep the standard's behaviour and say so, or cap
the extension at, for instance, `startBlockTime + 604,800 + 900`, which is a DMZ divergence for DERO-quoted
NFAs too and would need invariant 1 restated. OPEN.

*Closing an ended auction through `BuyItNow`.* `checkActive` returns 111 for any ended listing before it looks
at the type (40 precedes 50). `BuyItNow` on an ended **auction** therefore takes the 111 branch (510–540): the
unit goes back to the owner, `scBalance` is zeroed, `resetVars(1)` runs, and the caller's attached value is
refunded. `processHighestBidder` is never called. The highest bidder's money stays in the contract under
`currBidAddr` / `currBidAmt`, which `resetVars` does not clear. Anyone but the recorded owner can make that
call (`BuyItNow` 20 turns away the owner's own address only), with nothing attached, from the first second after
the end. So an owner who dislikes the winning price closes the auction from a second wallet, keeps the asset,
and holds the winner's bid — which the winner cannot recover alone: no call refunds `currBidAmt` except
`outbidReturns`, which runs on the next accepted `Bid` of a *new* listing (100) or on a `CancelListing` (460).
The owner can release it by starting a new listing and cancelling inside the buffer, or by never listing again.
And if the owner starts a new auction that ends with no bids, a `Bid()` with nothing attached after its end
calls `processHighestBidder`, which finds the *stale* `currBidAddr` / `currBidAmt` and settles the new listing
to the old winner at the old bid (100–320) — a settlement neither party asked for. The mirror case is harmless:
`Bid` after an ended **sale** reaches `processHighestBidder` with no `currBidAmt`, `bidAddr` stays the owner,
`processDEROFinalPayment(0)` sends nothing, and `DELETE` of a missing key is a no-op in the DVM
(`dvm_functions.go` 147–149), so the unit goes back to the owner and the caller is refunded. *Covered:* **not
covered.** This is the most consequential item on the page, and it is in the standard, not in DMZ's extension.
The fix is one line: `BuyItNow` 510 should take the 520 branch only when `LOAD("listType") == "sale"`, and
otherwise call `processHighestBidder()` (or simply refuse with a refund at 920 and leave the close to
`CloseListing` / `Bid`). Either changes a DERO-quoted DMZ NFA's behaviour from the standard's in this one
state and needs invariant 1 restated to "as the standard, except that an ended auction cannot be closed past
its bidders". A §5.A test — three bidders, the end passes, a fourth wallet calls `BuyItNow` with nothing
attached — decides it on the real DVM in minutes. OPEN, for the operator: keep the standard exactly, or close
this.

**R5. `cancelBuffer` and a buyer inside it.** `Start` at time t; until t + 300 the owner may `CancelListing`
(60). A **buyer** who `BuyItNow`s at t + 100 is done in that call: unit, payment and owner record all move
(70–96) and `scBalance` is 0, so `checkActive` returns 999 and `CancelListing` 50 refuses. A completed sale
cannot be undone. A **bidder** inside the buffer can be cancelled on: `CancelListing` 460 runs `outbidReturns`,
which refunds the highest bidder's full bid in the same call, before the unit leaves (600). The bidder is out a
fee. A cancel and a buy in the same block: the block orders them; the second either finds `scBalance` 0 and is
refunded (`BuyItNow` 50 → 920) or finds the sale done and is refused (`CancelListing` 50). *Covered:* yes by the
contract; §3.C already says "Cancel (only inside the buffer, said so)". Add the other side: a bidder should see
"the seller can still cancel for N seconds" while inside the buffer.

**R6. Royalty and fees summing past 100 %.** At install, `init` 500 refuses `charityDonatePerc + artificerFee +
royalty > 100` (with `charityDonatePerc` still 0, that is `artificerFee + royalty ≤ 100`). At `Start`, the
standard does not refuse an over-large charity share: 150–170 **clamp** it to `100 − artificerFee − royalty` and
store the clamped value, so the owner's remainder can be zero and line 125 then sends nothing. DMZ adds
`protoFee`. Its install check is `protoFee ≤ 10000`, and the combined rule is
`protoFee + royalty·100 + artificerFee·100 + charity·100 ≤ 10000` (build spec §1). Where that rule is enforced
matters: if `Start` keeps the standard's percent-only clamp, a charity share that fills to `100 − artificerFee −
royalty` leaves nothing for `protoFee`, `payoutAmt` wraps below zero in uint64, and the settlement's over-send
is refused by the node (§6.5) — the listing can never settle, the buyer is refunded, the seller's asset sits in
the contract until the end passes and `CloseListing` returns it. *Covered:* **partly.** The install rule is in
the spec; the `Start` clamp is not. `Start` must clamp (or refuse) in basis points: `charity·100 ≤ 10000 −
royalty·100 − artificerFee·100 − protoFee`, and the §5.A settlement test should include the corner where the
shares sum to exactly 10,000 (owner receives 0 and line 125 skips) and the corner one unit past it. The app
shows the split before the button (§3.C), so a buyer sees "owner receives 0" when that is so.

**R7. `artificerFee` as a donation the deployment chooses.** `artificerAddr` is hard-coded to
`dero1qy0khp9s9yw2h0eu20xmy9lth3zp5cacmx3rwt6k45l568d2mmcf6qgcsevzx` at `init` 40 and validated at 50;
`artificerFee` defaults to 0 and is set by the creator at install. It is the standard's donation slot for its
authors, Civilware; it is not DMZ's fee, and DMZ's fee (`protoFee`) is not it. A creator who sets it to 5 sends
5 % of every sale of that asset to Civilware for the life of the asset, and nothing can change that later. That
is fine, and the mint form must say it in those words rather than presenting it as a platform fee. One thing to
confirm on the simulator: `init` 50 requires `ADDRESS_RAW` of that mainnet address to parse on whatever network
the contract is installed on; the §5.A install test proves it either way. *Covered:* yes (build spec §1 says
"a DMZ deployment that sets it is making a donation").

**R8. The dropped holder-gated mint, and what "collection" is.** OPEN by the build spec's own words. Two things
worth saying now. First, `collection` is a free string: nothing verifies it, so anyone can install an NFA whose
`collection` reads the same as an existing creator's, and a screen that lists by collection alone would show the
impostor beside the originals. The app should group by `(collection, creatorAddr)` — the creator's address is
stored, fixed, and cannot be borrowed — and show the creator with the collection name, in the way it already
folds look-alike tickers (`app/feed.js` `decorate`). Second, the badge mechanic has one thing the standard does
not: an edition costs nothing to the creator, because it is a storage entry. Under the standard each edition is
an install, gas-priced against the 20,000-unit cap (`tests/canon_test.go` `TestEveryContractFits…`), paid by the
creator per asset. A creator who wants a hundred editions installs a hundred contracts. Whether `nft.bas` stays
for that reason, is retired, or is reworked so that a badge can be *converted* into an NFA the creator installs
for the holder, is the open decision; this page records the cost, not the answer.

**R9. Stray DERO on a wrapped-quote call, and the ring size.** A buyer attaches DERO (or DERO *and* wXMR) to
`BuyItNow` on a wXMR listing. The guard `q != DERO() && DEROVALUE() != 0` returns 1; the DVM discards the call
and the node returns everything attached to the signer — DERO and wXMR both — because the call was made at ring
size 2. Nothing is kept, nothing is stranded (invariant 2). The same call at ring size 16 would credit both to
the 33-zero-byte key (§4). *Covered:* yes for the guard (build spec §1 item 1, §5.A "stray DERO refused"); the
ring-size half is R1's point again: the app must not offer a private mode for any NFA market call, and the
estimate-then-transfer path names the signer to the node only at ring 2 (`docs/ARCHITECTURE.md`, "Writes").

**R10. Multiplication wrap in the split.** §6.5. `protoFee * saleAmt` wraps at 1,844.67 XMR for the maximum fee
and at 147,573 XMR for 125 bps; the standard's `royalty * saleAmt` at 184,467 XMR. The result is a listing that
cannot settle, not a loss, because the node refuses the over-send. *Covered:* **not covered.** `nfa.bas` should
compute the basis-point share as `vault.bas` does (`(saleAmt / 10000) * protoFee + ((saleAmt % 10000) *
protoFee) / 10000`), which cannot wrap for any `saleAmt` and `protoFee ≤ 10000`, and §5.A should settle one sale
above 2^64 / 10000 units on the wXMR-quoted install. The standard's percent lines are kept as they are: their
bound is beyond any plausible sale, and changing them changes the standard's arithmetic for a DERO-quoted asset.

**R11. Bid increments on a twelve-decimal quote.** `findLesserIncrease` sets the next minimum bid to the *lesser*
of `bid + 10 %` and `bid + 10,000 units`. In DERO's five decimals 10,000 units is 0.1 DERO, so bids under 1 DERO
step by 10 % and bids above it by 0.1 DERO — the standard's stated rule. In wXMR's twelve decimals 10,000 units
is 0.00000001 XMR, and the crossover is 100,000 units = 0.0000001 XMR, so on any real wXMR auction the minimum
outbid is a hundred-millionth of an XMR: there is, in effect, no minimum increment. That does not lose anyone
money — a bidder can always be outbid by more — but it makes R4's indefinite extension free and makes "current
bid + increment" in the app a meaningless number. *Covered:* **not covered**; "kept exactly" keeps the
constants. OPEN for the operator: keep, or scale `staticBidIncr` by the quote's decimals at install (a wrapped
quote is already a DMZ extension, so this changes nothing for a DERO-quoted asset).

**R12. `Update` while listed.** The creator (always) and the owner (if `ownerCanUpdate`) can change `fileURL`,
`fileSignURL`, `iconURLHdr`, `coverURL` and `tagsHdr` at any time, including during a live listing. A buyer who
saw one file can receive a URL to another. What cannot change is `fileCheckC` / `fileCheckS`, the signature
values fixed at install, so the file at `fileURL` can always be checked against what the creator signed.
*Covered:* partly — §3.C shows "`fileURL` with its filesign hash". The page should say whether the app *checks*
the file against the hash or only displays it; until it checks, it must say "not verified" beside the link.

**R13. Same-block races.** Two `BuyItNow` in one block: the first completes, the second finds `scBalance` 0 and is
refunded (50 → 920). Two `Bid`s: the second sees the first's `currBidPrice`; if below it, refunded (80 → 920); one
address cannot bid twice in one block timestamp (51–60). A `Bid` and the owner's `CloseListing` in the block the
auction ends: `CloseListing` needs 111 and `Bid` accepts only on 0, and one `BLOCK_TIMESTAMP` gives one answer,
so they cannot both act. *Covered:* yes, by the contract.

**R14. The owner as bidder.** `BuyItNow` 20 and `Bid` 30 turn away the recorded owner's own address. An owner
bidding from a second wallet is not stopped by anything, pays royalty, `protoFee` and any charity share on the
wash, and gets the asset back with a new `previousAuctionPrice` on record. That is the standard's position and
DMZ's: a fee paid is a fee paid; the record is what it says, a price someone paid. *Covered:* yes.

---

## 8. What the app never holds

- **No key and no seed.** The browser builds a transfer request and hands it to the user's wallet over XSWD; the
  wallet prompts and signs (`docs/ARCHITECTURE.md`, "Writes"). Nothing new in the browser holds a key (build
  spec §3.C).
- **No unit.** The NFA's one unit is in the holder's wallet or in the contract. The app never has custody of it,
  never relays it, and cannot claim, list or sell it without the wallet's approval of that exact transfer.
- **No quote.** A bid or a purchase is the user's own transfer to the contract; a refund or a payout is the
  contract's transfer to the user. The app is not in the path.
- **No server.** Listings are read from the factory's `nfa_<i>` list and each contract's own storage; code is
  verified against the shipped template hash before anything is shown as verified (`docs/ARCHITECTURE.md`,
  "Reads"). An optional read cache is off and the node is the fallback.
- **No rate.** Every amount is shown in the quote's own symbol and decimals, and never summed or ranked across
  quotes (`docs/TODO.md` §1.3; invariant 4). DERO appears on a wrapped-quote page only as gas.

What the app *does* decide, and is trusted for: which template hash counts as verified, which ring size a call
goes at (ring 2 for every NFA market call, R1 and R9), and what it tells the user before the button — the split,
the buffer, the "extends while bids arrive" end, the vault's state behind a wrapped price. Those are the places a
wrong screen can cost someone money, and they are the §3.C requirements this page adds to.

---

## 9. Sources

Primary, read for this page (the derohe files are the vendored clone at upstream HEAD `e9df1205`,
`deploy/vendor.lock`; the standard's files at HEAD `d286b19`, fetched 2026-09-08):

- `docs/NFA_BUILD.md` — what changes and what is kept (§1), ownership (§2), components (§3), invariants (§4),
  tests (§5).
- `research/wrapper/MECHANISM_REVIEW.md` §5 — the decision to adopt ART-NFA-MS1 with a quote.
- civilware/artificer-nfa-standard, `ART-NFA-MS1/ART-NFA-MS1.bas` — the contract; sha256
  `a5e4ad654acb92bdca8d7e951173d6dae2f4f75f55772b9bedaabbac8499b174`;
  https://raw.githubusercontent.com/civilware/artificer-nfa-standard/main/ART-NFA-MS1/ART-NFA-MS1.bas
- civilware/artificer-nfa-standard, `ART-NFA-MS1-Functions.md` — parameters and units of each public function;
  "bids are refunded after being out-bid"; "if a bid is placed within the last 15 minutes … extended to 15
  minutes"; `cancelBuffer` 300 seconds;
  https://raw.githubusercontent.com/civilware/artificer-nfa-standard/main/ART-NFA-MS1/ART-NFA-MS1-Functions.md
- civilware/artificer-nfa-standard, `README.md` — "Initialization" (metadata edited into the source before
  install; `artificerFee` "a donation back to the Civilware developers"); "Utilization" ("always use ringsize 2";
  `ClaimOwnership` at ringsize > 2 "1 asset sent back to current owner"; `BuyItNow`/`Bid` at ringsize > 2 "will
  just burn"); https://github.com/civilware/artificer-nfa-standard/blob/main/ART-NFA-MS1/README.md
- civilware/artificer-nfa-standard, `ART-NFA-MS1-Minting.md` — `filesign` / `fileverify`; the meaning of each
  metadata value; `collection` "only capable of being set on installation";
  https://github.com/civilware/artificer-nfa-standard/blob/main/ART-NFA-MS1/ART-NFA-MS1-Minting.md
- civilware/artificer-nfa-standard, `Changelog.md` — 1.1.0: 7-day cap, same-call outbid refund, `version`;
  1.1.1: the `Bid` GOTO fix; https://github.com/civilware/artificer-nfa-standard/blob/main/ART-NFA-MS1/Changelog.md
- derohe `dvm/dvm_functions.go` — `dvm_derovalue` (434–437) reads `Assets[SCIDZERO]`, `dvm_assetvalue` (440–455)
  reads `Assets[asset]`; `dvm_send_dero_to_address` (377–396) and `dvm_send_asset_to_address` (398–431) both call
  `SendExternal`, the first with the zero hash; `dvm_dero` (284–288) returns the zero hash; `dvm_signer`
  (307–310); `dvm_is_address_valid` / `dvm_address_raw` / `dvm_address_string` (324–370); `Delete` (147–149)
  does not check for the key; https://github.com/deroproject/derohe/blob/master/dvm/dvm_functions.go
- derohe `dvm/dvm.go` — `interpret_IF` (728–…): "IF has two forms … IF expr THEN GOTO x ELSE GOTO y";
  `BL_TIMESTAMP` "epoch second resolution" (401); https://github.com/deroproject/derohe/blob/master/dvm/dvm.go
- derohe `dvm/sc.go` 278–288 — a non-zero `RETURN` discards the call's changes ("Discarded knowingly");
  https://github.com/deroproject/derohe/blob/master/dvm/sc.go
- derohe `blockchain/transaction_execute.go` — a failed call's value is returned to the signer when one can be
  extracted, "otherwise we burn all the funds" (395–404); `Extract_signer` only at `RingSize == 2` (422–430);
  `SanityCheckExternalTransfers` (391–393);
  https://github.com/deroproject/derohe/blob/master/blockchain/transaction_execute.go
- derohe `blockchain/blockchain.go` 959 — the DVM receives `bl_current.Timestamp/1000`, seconds; `block/block.go`
  38 — the block stores milliseconds; https://github.com/deroproject/derohe/blob/master/blockchain/blockchain.go
- derohe `dvm/simulator.go` `ErrorRevert` (244–279) — the refused value is added to the balance under the signer
  key it was given, which above ring 2 is 33 zero bytes;
  https://github.com/deroproject/derohe/blob/master/dvm/simulator.go
- derohe `config/config.go` 32 — `BLOCK_TIME = 18`; https://github.com/deroproject/derohe/blob/master/config/config.go
- `contracts/pool.bas` header lines 2–4 and the `DEROVALUE() != 0` guards (`AddLiquidity` 42; line 71 of
  `RemoveLiquidity`, `buyto`, `sellto`) — the quote extension DMZ already ships; `contracts/vault.bas` — header lines 4–9 (a halt is a lease and never blocks the exit),
  `ishalted`, `Burn` 20–47 (line 30: the non-wrapping basis-point product), `Reclaim` 10–21, `Halt` 41;
  `contracts/nft.bas` header — the badge mechanic; `contracts/factory.bas` `RegisterPool` 65–71 — the shape
  `RegisterNFA` mirrors; `contracts/README.md` rule 5 (`SIGNER()` at ring size 2), rule 6 (`LOAD` panics), and
  "founders.bas".
- `tests/sendorder_test.go` — the send-order rule and the drain that taught it; `tests/canon_test.go`
  `TestEveryContractFitsTheInstallGasCapAsInstalled` — the 20,000-unit install cap on the canonical form;
  `reference/canon.go` — comments stripped at install.
- `tools/treasurybot/chain.go` `Quote`; `tools/treasurybot/journal_test.go` `TestForeignQuoteMarketsAreNotSwept`
  — the bot sweeps DERO-quoted contracts only.
- `app/feed.js` `nameQuote`, `qsym`, `qdec`, `vaultOf`, `decorate` — how the app names a quote and folds
  look-alikes; `docs/ARCHITECTURE.md` "Reads" and "Writes" — template verification, XSWD, ring sizes.
- `docs/TODO.md` §0e (the vault state machine, 2026-09-08), §1.1 (fees in a wrapped asset), §1.3 (no rate, no
  cross-quote totals), §1.4 (NFTs priced in DERO only — the item this build closes).
- `research/wrapper/README.md` §1–§2 — what a wrapped unit is and who can hurt its holder.

Computed on this page, with BigInt, 2026-09-08: the splits in §6.3 and §6.4; the wrap bounds in §6.5
(2^64 / 10000 = 1,844,674,407,370,955; 2^64 / 125 = 147,573,952,589,676,412; 2^64 / 250 =
73,786,976,294,838,206; 2^64 / 100 = 184,467,440,737,095,516); the `vault.bas`-form product for Example B; the
increment crossover in R11 (10,000 × 10,000 / 1,000 = 100,000 units); 20,000 × 18 s = 4.17 days.
