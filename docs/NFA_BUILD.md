# NFA on DMZ — build specification

**Status 2026-09-08: specified, not started; launches after the swap build's verifier finishes (they share
the test tree).** Verdict: `research/wrapper/MECHANISM_REVIEW.md` §5. Standard: `civilware/artificer-nfa-standard`,
ART-NFA-MS1 v1.1.1 (clone at HEAD `d286b19`). The DVM accepts the `IF … THEN GOTO x ELSE GOTO y` form the
standard uses throughout (`vendor/derohe/dvm/dvm.go:731`).

## 1. What changes, and what is deliberately kept

**What DMZ has today is not an NFT in the standard's sense.** `contracts/nft.bas` is a holder-earned badge
collection: lock a token, wait, mint an "edition" that is a *storage entry* (`ed_<n>` = an address). Editions are
not assets, cannot be transferred, and have no market. It uses the NFA *headers* and nothing else of the standard.

**An NFA is one contract per asset.** Ownership is one unit of the contract's own asset; `ClaimOwnership()` burns
that unit back and records `owner`, so the asset can also change hands by a plain private DERO transfer and be
claimed afterwards. The marketplace is inside the contract: `Start(listType, duration, startPrice, charityAddr,
charityPerc)`, `BuyItNow()`, `Bid()` (outbid refunded in the same call since 1.1.0), `CloseListing()`,
`CancelListing()` (within `cancelBuffer` = 300 s of Start). Settlement splits `royalty` to `creatorAddr`,
`artificerFee` to Civilware, an optional charity share, the rest to the owner. Listings cap at 7 days. Headers
(`nameHdr descrHdr typeHdr iconURLHdr tagsHdr`), `fileURL`/`fileSignURL` with DERO filesign hashes, and
`collection` (install-only) are what Engram, Gnomon and dReams index.

**It is priced in DERO only.** `BuyItNow` line 60 `IF DEROVALUE() >= LOAD("startPrice")`; `Bid` `LET bidAmt =
DEROVALUE()`; `processDEROFinalPayment` sends DERO. That is the one thing DMZ extends.

**Kept:** every header name, every standard entrypoint's name and arguments, `ClaimOwnership`, the listing states
and their constants (`cancelBuffer`, `staticBidIncr`, `percentBidIncr`, the 15-minute anti-snipe extension, the
7-day cap), the royalty and `artificerFee` arithmetic (default 0 — it is the standard's donation slot, and a DMZ
deployment that sets it is making a donation, which is fine and is said so), `version`. A DERO-priced DMZ NFA must
remain a valid ART-NFA-MS1 asset to every other DERO tool.

**Changed on purpose (2026-09-11)**, each to close a finding in which the standard's own code loses or locks money
(`docs/NFA.md` §2.5 has the reasons and the tests): (1) a settlement pays only the parties that signed — royalty to
the creator, remainder to the owner — and credits the artificer's, the charity's and the protocol's shares for
`PayOwed`, because the DVM destroys every transfer after one to an unregistered address (DVM-1, LIV-01); (2)
`BuyItNow` on an ended auction settles it to the highest bidder (NFA-1, LIV-02, ADV-PRICE-01); (3) `Start` stores a
clamped charity share only after checking the address (DVM-2); (4) every entrypoint refuses value it does not take
(NFA-4). A tool that reads the headers and calls the entrypoints sees the standard; one that watches for the
standard's direct payment to Civilware or a charity sees it arrive only when `PayOwed` is called.

**Extended:**

1. **`quote`** — a 64-hex SCID stored at install (zero hash = DERO). Every `DEROVALUE()` that means "the money
   offered" becomes `ASSETVALUE(q)`; every `SEND_DERO_TO_ADDRESS(x, n)` that pays or refunds becomes
   `SEND_ASSET_TO_ADDRESS(x, n, q)`. In the DVM `ASSETVALUE(zero)` *is* `DEROVALUE()` and
   `SEND_ASSET_TO_ADDRESS(a, n, zero)` *is* `SEND_DERO_TO_ADDRESS(a, n)`, so a DERO-quoted NFA is byte-for-byte the
   standard's behaviour. On a non-DERO quote, stray DERO on a market call is refused (`DEROVALUE() != 0`), as
   `pool.bas` does, so nothing is stranded.
2. **`protoFee` (bps) and `treasury`** — the protocol's share of a sale, in the quote, credited at settlement with
   the artificer's and the charity's shares and paid by `PayOwed` (`docs/NFA.md` §2.5);
   `protoFee + royalty·100 + artificerFee·100 + charity·100 ≤ 10000`. It reaches the treasury's wallet and no
   further: no NFA fee reaches Founders Pass holders in any quote until `tools/treasurybot` walks `nfa_`
   (FINDINGS_2026-09-10 NFA-3; until 2026-09-11 this line said NFA sales feed the pass).
3. **`typeHdr`** is whatever the creator says; DMZ adds nothing.

**Dropped from DMZ's side:** the holder-gated mint. The DVM has no cross-contract calls, so a contract cannot
install per-edition NFAs on demand; per the standard, the creator mints each asset (Artificer does it for them).
`nft.bas` stays as the badge mechanic under its own name until someone decides to retire it — recorded as open,
not silently removed.

## 2. Ownership

| Agent | Owns | Builds |
|---|---|---|
| **A `nfa`** | `contracts/nfa.bas`, `tests/nfa_test.go`, one line in `tests/parse_test.go` | the contract (§3.A) and every test in §5.A |
| **B `registry`** | `contracts/factory.bas`, `tests/registry_test.go` (additions), `tools/simdeploy/main.go`, `tools/genconfig/**`, `tools/netcfg/**`, `deploy/networks.json` (the `nfa` template entry) | `RegisterNFA` on the factory; the `nfa` template shipped and hashed like `pool`/`launch`; legacy-hash sequencing (§3.B) |
| **C `app`** | `app/nfas.js` (screen and its action sheets), `app/nfts.js` (mounts the section), `app/screens.js` (the `nfa` route), `app/index.html` (the script line, no tab), `app/nft.js`, `app/feed.js` (only the NFA loading/verification branch), `app/forms.js` (the mint form), `tools/simnfa` | the NFA screen: list by collection, headers, quote-aware marketplace actions through XSWD (§3.C). **Starts only after A and B report green** |
| **D `spec`** | `docs/NFA.md` | the user-facing description, the compatibility statement, the adversarial review (§3.D) |
| **verify** | nothing | §6 |

## 3. Components

### 3.A `contracts/nfa.bas`

Start from the standard's `ART-NFA-MS1.bas` **verbatim** (the clone is at
`research/crosschain/poc/../../..` — fetch `https://raw.githubusercontent.com/civilware/artificer-nfa-standard/main/ART-NFA-MS1/ART-NFA-MS1.bas`
and record its sha256 in a comment), then apply §1's extensions and nothing else. `InitializePrivate` gains
`quote String, protoFee Uint64, treasury String` after the standard's metadata arguments; `init()` stores them
after validation (`STRLEN(quote) == 64` and `HEXDECODE` to 32 bytes, or `""` → zero hash; `protoFee ≤ 10000`;
treasury a valid address). Line numbers must stay strictly ascending within each function and `DIM` first — the
standard's spacing (10, 20, 30…) leaves room; where it does not, renumber that function and say so in the
commit. `reference.Canon` strips comments at install; keep the standard's comments, they are documentation.

The send-ordering rule (`tests/sendorder_test.go`, mechanical) applies: the standard already refunds a refused
bid and returns 0 (`Bid` 920–930), which is the safe shape; keep it, and never add a send before a `RETURN 1`.

### 3.B Registry and templates

`factory.bas` gains `RegisterNFA(scid)` storing `nfa_<nc>` / `nc`, mirroring `RegisterPool`. `simdeploy` ships
`nfa.bas` into `app/templates` and hashes it into `config.js templates.nfa` exactly as `pool`/`launch`; the
current `nft` template stays under its own key. **Every contract edit changes a Canon hash: re-run `simdeploy`
with the existing SCIDs so the outgoing hash is demoted to `legacy`** — otherwise every listed market de-verifies.
`tests/templates_test.go` must keep passing (it compares `app/templates` to `Canon(contracts)`).

### 3.C The app

`feed.js` learns a third listed kind, `nfa`: loaded from the factory's `nfa_<i>` list, code verified against
`templates.nfa` (legacy included), headers read by exact key, `quote` named through `D.nameQuote` so an NFA
listed in wXMR shows XMR prices. The NFA screen lists by `collection`, shows `nameHdr`/`descrHdr`/`iconURLHdr`
(images off by default, as today), `fileURL` with its filesign hash, the owner, the listing state, and the
market action for the visitor's role: **Buy now** / **Bid** (amount in the quote's decimals; the split shown
before the button: royalty, protocol fee, charity, owner), **Claim ownership** (if the wallet holds the unit),
**Start sale / auction**, **Close**, **Cancel** (only inside the buffer, said so). Every amount is in the quote's
symbol; DERO appears only as gas. Nothing new in the browser holds a key. Keep every touched file under the DOC
cap (`genconfig -dist -network simulator`).

The screen has no tab of its own: the phone tab bar is a seven-column grid (`styles.css` `.tabs`, profile hidden on
phones) and an eighth visible tab failed the UI pass. NFAs are the first section of the NFTs tab (`app/nfas.js`
`V.nfas.section()`, mounted from `app/nfts.js`), and each has its own page at `#nfa/<scid>` (`V.nfas.page`, the route
in `app/screens.js`); `app/nft.js` forwards a `#nft` link that names an NFA. The market actions are sheets inside
`nfas.js`, not `sheets.js` (that file is the trade sheet). The mint form is `V.forms.nfa()`; `tools/simnfa` installs one
on the simulator, DERO- or wrapped-quoted, and can list it.

### 3.D `docs/NFA.md`

What an NFA is on DMZ, in plain words; the compatibility statement (a DERO-quoted DMZ NFA is a standard
ART-NFA-MS1 asset; a wrapped-quoted one is an extension other tools will not settle); the fee split with a
worked example; the adversarial review — `ClaimOwnership` with a fake unit, a listing started while a private
transfer is in flight, bidding on a wrapped-quote NFA whose vault is halted (the quote cannot be withdrawn; say
what the bidder holds), the anti-snipe extension and the 7-day cap, `cancelBuffer` and a buyer inside it,
royalty + fees summing past 100 %, `artificerFee` as a donation the deployment chooses; and what the app never
holds. Primary sources by URL.

## 4. Invariants

1. A DERO-quoted DMZ NFA behaves as ART-NFA-MS1 v1.1.1 to any DERO tool except in the four changes stated in §1
   (`docs/NFA.md` §2.5); every share is the standard's own arithmetic (`tests/nfa_test.go` `checkSettlement`).
2. Every unit of value that enters a market call leaves it in the same call or is accounted to a named party;
   nothing is stranded — including stray DERO on a wrapped-quote NFA.
3. Exactly one unit of the NFA's asset exists; it is either in the contract (listed / unclaimed) or in one
   wallet.
4. Fees in a wrapped quote are counted where the treasury bot can see them and are never shown summed with
   DERO.

## 5. Tests

- **A.** Against the real DVM: install DERO-quoted and wXMR-quoted (use a plain asset contract as the quote,
  like `tests/pool_test.go` does); `ClaimOwnership` by the holder and refused for a non-holder; a sale bought at
  and above the price, refused below, split checked to the unit for royalty / artificerFee / charity / protoFee /
  owner; an auction with three bidders where every outbid is refunded in the same call in the quote, the
  anti-snipe extension, `CloseListing` paying the winner's amount, `CancelListing` inside and outside the buffer;
  a private transfer then `ClaimOwnership` by the new holder; stray DERO refused on a wrapped-quote call;
  install gas < 20,000 measured.
- **B.** `RegisterNFA` on the factory; templates test; a simulator install through `simdeploy`.
- **C.** The UI pass with an NFA listed; a cold-cache deep link to an NFA page; amounts labelled in the quote.

## 6. Verification

```
bash tests/run.sh
cd tools && go build ./... && go vet ./... && go run ./genconfig -dist -network simulator
PW_DIR=<playwright> node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-nfa
```
