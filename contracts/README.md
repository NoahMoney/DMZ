# DMZ contracts

Twelve immutable DVM-BASIC contracts. None defines `UpdateCode` or calls `UPDATE_SC_CODE`. Every state-changing entrypoint that pays the caller
requires **ringsize 2** (the node only reveals `SIGNER()` at ringsize 2; DECISIONS §2.4); the `…To` entrypoints pay a
named address and work at any ring size (§ private mode below). Arithmetic rules: DECISIONS §10.2. Executable
specification: `../reference/`. Tests: `../tests/` (run with `tests/run.ps1`).

Sizes and install cost, **measured** on 2026-09-12 by the DVM suite's tests (`tools/withlock.sh dvm bash tests/run.sh
-v`, go1.26.8) on the contracts of `e06c28a` (`factory.bas` v2, `htlc.bas` v3), which `contracts/` has not changed
since. A contract is installed in its canonical form, `reference.Canon`, which drops comment and blank lines
(`tools/simdeploy/main.go:153`), so only those bytes are charged. Install storage gas is capped at 20,000 per
transaction. It moves a little with the install arguments, so each figure names the tests that measured it:

| Contract | Source bytes | Installed bytes (`Canon`) | Install storage gas, measured | Install compute gas | Measured by (`tests/`) |
|---|---|---|---|---|---|
| `board.bas` v3 | 6,602 | 2,611 | 2,652 | — | `board_test.go`, `board_v3_test.go` |
| `chat.bas` | 2,401 | 1,146 | 1,315–1,320 (fee and `maxlen` vary) | — | `chat_test.go` |
| `factory.bas` v2 | 3,452 | 1,443 | 1,680 (`regfee` 10,000) | — | `registry_test.go` |
| `founders.bas` v2 (adopted 2026-09-13) | 15,884 | 9,250 | 10,221 at the mainnet shape; 11,144 with every header at its maximum | 3,765,000 | `founders_test.go` `TestFoundersV2InstallGas` |
| `htlc.bas` v3 | 11,206 | 5,104 | 5,168–5,180: 5,169 at `w1min` 2, `w2min` 1, `g2` 50; 5,177 at 400, 400, 2,400 | — | `htlc_test.go`, `htlc_v3_test.go` |
| `launch.bas` | 17,074 | 15,257 | 15,842–15,891; **16,915** with every metadata field at its maximum | 1,193,400–1,517,200 | `launch_test.go`; `zzquote_test.go` `TestLaunchMaxMetadataInstallGas` |
| `nfa.bas` | 24,234 | 14,937 | 15,874–15,877 DERO-quoted; 15,940–15,942 wrapped | 1,045,800 DERO-quoted; 1,066,600 wrapped | `nfa_test.go` |
| `nft.bas` | 4,959 | 3,873 | 4,489 | — | `registry_test.go` |
| `pool.bas` | 14,250 | 10,604 | 10,940 | 488,000 | `pool_test.go` |
| `social.bas` | 2,355 | 1,736 | **not measured**: no test and no install log prints it | — | — |
| `swap.bas` v2 | 5,967 | 2,767 | 2,816 at `wmin` 1,600; 2,813 at `wmin` 3 | — | `swap_gaps_test.go`, `swap_test.go` |
| `vault.bas` v3 | 22,196 | 16,027 | 16,358 (`k` 2) | — | `vault_smoke_test.go` |

The byte counts are `TestEveryContractFitsTheInstallGasCapAsInstalled` (`tests/canon_test.go`). Every measured figure
is under the cap. The closest is `launch.bas` with every metadata field at its maximum, 3,085 units below it, and next
`vault.bas`, 3,642 below. Per call, in the same run: an `htlc.bas` Claim
takes 304 storage and 537,800 compute gas, a Reclaim 231 and 434,600, a Punish 90 and 322,200 (`htlc_test.go`,
`htlc_v3_test.go`, `htlc_v2_test.go`); the founders v2 calls are tabled in its section below. Re-measure with `DERO.GetGasEstimate` before deploying rather than trusting a table.

Worst-case compute gas seen in fuzzing (limit 10 000 000 per call): AddLiquidity 1.72 M, Unstake 1.78 M, Stake 1.69 M,
launch Buy 1.61 M, pool swaps 1.06 M.

## Common conventions

- **Values in atomic units.** 1 DERO = 100 000. Token decimals are a display hint (`dec` on launches; 5 assumed for
  foreign assets).
- **Passing SCIDs**: as `H` arguments (the contract receives the raw 32 bytes). Stored raw; `DERO.GetSC` returns them
  hex-encoded, which is exactly the SCID hex.
- **Addresses**: stored raw (33 bytes) when the contract needs to send to them; stored/keyed as `ADDRESS_STRING`
  (canonical text form, **always the `dero1` prefix** regardless of network) when humans or the app read them. The app
  canonicalises by decoding bech32 and re-encoding with the `dero` HRP.
- **Sending value in**: `transfers:[{destination:<any>, burn:<dero>}]` and/or `[{scid:<asset>, burn:<units>}]`.
  Stray value (an asset the entrypoint does not expect) makes the call return 1, which refunds everything.
- **Fee**: `fee` bps of every trade (deployed with 50 = 0.5 %, hard cap 100). Protocol share `pfee` in bps of the fee.
  Accrued protocol fees sit in `fd` (DERO) / `fa` (asset) until **anyone** calls `ClaimFees()`, which always pays
  the `treasury` address recorded at install: a fee path only one key can move stops the moment that key does, and
  the protocol has no operator. `ClaimFees` reads no signer, so it works at any ring size.
- **Social feed keys** (pool and launch): `tc` count and `t_<n>` = `side|address|in|out|height|timestamp` written
  only when the trader passes `publish = 1` (side 1 = bought asset, 2 = sold asset). `pc` count and `p_<n>` =
  `height|timestamp|rd|ra` = closing reserves of the previous active block, written on the first reserve change in a
  new block. Read ranges with `DERO.GetSC keysstring`.
- **Return 0 commits, anything else reverts and refunds** (DECISIONS §2.3). Every `GOTO 900` below is a revert.

---

## pool.bas — LP-owned constant-product pool (asset ↔ DERO)

`InitializePrivate(asset H, quote H, treasury S, fee U, pfee U)` — asset ≠ DERO, asset ≠ quote, fee ≤ 100,
pfee ≤ 10000, treasury valid. `quote` is the 32-byte id of what the pool prices the asset in, DERO or another asset,
stored as `qt`; where this section says DERO, read the quote.

| Entrypoint | Params | Value in | Effect |
|---|---|---|---|
| `AddLiquidity` | `min_lp` | DERO `d` + asset `a` | First deposit (d ≥ 1 DERO): `lp = d`, mints `d − 1000` LP (MIN_LIQ locked forever). Else `L = min(⌊d·lp/rd⌋, ⌊a·lp/ra⌋)`, the other side's requirement `⌈L·r/lp⌉` is taken and the excess refunded. Mints `L` of the pool's own SCID. |
| `RemoveLiquidity` | `min_dero, min_asset` | LP `l` (this SCID) | `⌊l·rd/lp⌋` DERO and `⌊l·ra/lp⌋` asset; `l ≤ lp − 1000`. |
| `SwapDeroForAsset` | `min_out, publish` | DERO `d` | `fee = ⌈d·fee/10⁴⌉`, `pf = ⌊fee·pfee/10⁴⌋`, `out = ⌊(d−fee)·ra / (rd + d − fee)⌋`; `rd += d − pf`, `ra −= out`, `fd += pf`. |
| `SwapAssetForDero` | `min_out, publish` | asset `a` | symmetric with the fee in asset units; `fa += pf`. |
| `ClaimFees` | — | — | anyone, any ring size: sends `fd` DERO and `fa` asset to the `treasury` recorded at install, and adds `fd` to the monotone counter `fc`. Reverts if both are 0. |

Keys: `kind v asset treasury fee pfee rd ra lp fd fa fc tc pc lh lt t_<n> p_<n>`.

**Invariants (tested after every fuzz op):** contract DERO balance = `rd + fd`; asset balance = `ra + fa`; `lp ≥ 1000`
after the first deposit; `k = rd·ra` never decreases on swaps (floor rounding); every credit is guarded against
wrapping the real balance (`reserve + fees + incoming`).

**Why the refund side is always sufficient.** If `⌊d·lp/rd⌋ ≤ ⌊a·lp/ra⌋` then `a·lp/ra ≥ L`, so `a ≥ L·ra/lp`, and since
`a` is an integer `a ≥ ⌈L·ra/lp⌉`. Symmetric for the other side.

---

## launch.bas — memecoin: bonding curve → built-in AMM, fee, tax, staking

`InitializePrivate(name S, ticker S, descr S, icon S, tags S, quote H, dec U, supply U, grad U, cbps U, tax U, tmode U, treasury S, fee U, pfee U)`
- `quote` is the 32-byte id of what the curve and the AMM price the token in, DERO or another asset, stored as `qt`.
- `supply ∈ [10⁶, 2⁶³)`, `grad ∈ [10⁶, 10¹⁵]` atomic units of the quote (10 to 10¹⁰ DERO when it is DERO; `launch.bas`
  BASIC line 25), `cbps ≤ 500` (creator allocation ≤ 5 %), `tax ≤ 1000` (≤ 10 %),
  `tmode ∈ {0 none, 1 creator, 2 buyback-and-lock, 3 stakers}` (`tax` must be 0 iff `tmode` = 0), `fee ≤ 100`, `pfee ≤ 10000`.
- Derived: `alloc = ⌊supply·cbps/10⁴⌋` (sent to the creator at install), `rest = supply − alloc`, `Lq = ⌊rest/5⌋`,
  `cs = rest − Lq` (curve supply), `vd0 = ⌊grad/3⌋`, `vt0 = ⌊(grad + vd0)·cs / grad⌋`.
- ART-NFA headers stored: `nameHdr descrHdr iconURLHdr tagsHdr typeHdr="Token"`.

### Phase 0 — curve (`phase = 0`)
Virtual reserves `rd` (DERO, starts at `vd0`) and `ra` (tokens, starts at `vt0`); `real` = DERO actually held for the
curve; `sold` = tokens sold. Invariants: `rd − real = vd0` and `ra + sold = vt0` (both exact, tested).

- `SwapDeroForAsset(min_out, publish)` with DERO `d`: `fee = ⌈d·fee/10⁴⌉` (all to treasury in phase 0), `tx = ⌊d·tax/10⁴⌋`,
  `deff = d − fee − tx`, `out = ⌊deff·ra/(rd + deff)⌋`. If `out ≥ rem = cs − sold`: `out = rem`,
  `cost = ⌈rem·rd/(ra − rem)⌉` (≤ deff, proof below), the difference is refunded. Then `real += deff`, `rd += deff`,
  `ra −= out`, `sold += out`, `fd += fee`, `tacc += tx`. When `sold == cs` the contract **graduates**: `phase = 1`,
  `rd = real`, `ra = Lq`, `gh = height`.
- `SwapAssetForDero(min_out, publish)` with tokens `a ≤ sold`: `dout = ⌊a·rd/(ra + a)⌋` (must be ≤ real),
  `fee = ⌈dout·fee/10⁴⌉`, `tx = ⌊dout·tax/10⁴⌋`, pays `dout − fee − tx`; `real −= dout`, `rd −= dout`, `ra += a`, `sold −= a`.

**Why `cost ≤ deff` when the buy is capped.** `out = ⌊deff·ra/(rd+deff)⌋ ≥ rem` ⇒ `deff·ra ≥ rem·rd + rem·deff` ⇒
`deff ≥ rem·rd/(ra − rem)`; `deff` is an integer so `deff ≥ ⌈rem·rd/(ra − rem)⌉ = cost`.

**Why `real` never goes negative.** `k = rd·ra` is non-decreasing (floor rounding on both sides). A sell of
`a ≤ sold` gives `ra + a ≤ vt0`, so `rd − dout ≥ rd·ra/(ra+a) = k/(ra+a) ≥ k₀/vt0 = vd0`, i.e. `real' ≥ 0`. The guard
`dout > real ⇒ revert` is a belt-and-braces check.

**Price continuity at graduation.** End-of-curve price `(vd0 + G)/(vt0 − cs)`; AMM start price `G/Lq`. With
`vd0 = G/3`: `vt0 − cs = cs/3` and `Lq = cs/4`, so both equal `4G/cs` up to integer rounding (tested to 0.1 %). The
price multiple over the curve is `((G + vd0)/vd0)² = 16×`.

### Phase 1 — AMM (`phase = 1`), contract-owned liquidity, no LP token
Same formulas as `pool.bas` with `pfee` splitting the fee, plus `tx = ⌊dero_leg·tax/10⁴⌋` taken from the DERO side of
every swap into `tacc`. Nobody can withdraw the reserves: there is no entrypoint for it.

### Tax modes
- **1 creator**: `ClaimTax()` by the creator sends `tacc`.
- **2 buyback-and-lock**: `Buyback()` by anyone (any ringsize, no signer needed), phase 1 only: spends
  `d = min(tacc, ⌊rd/100⌋)` on an internal buy at the AMM price (no fee), adds the tokens to `locked` forever,
  `rd += d`, `ra −= out`, `tacc −= d`, and records the height in `bbh`; the next call needs 20 more blocks. The 1 %
  cap and the spacing keep a buy → Buyback → sell sandwich below the round-trip fees (2026-09-06).
- **3 stakers**: `Stake()` (send tokens), `Unstake(amt)`, `Claim()`, `Distribute()`. MasterChef accounting with
  `PREC = 2³²`: `dist()` moves `tacc` into the accumulator `sacc += ⌊tacc·2³²/stot⌋`, the exactly-distributable part
  `paid = ⌊inc·stot/2³²⌋` moves from `tacc` to `rpool`; a staker's pending reward is `⌊st·(sacc − sd)/2³²⌋`, taken from
  `rpool`. Sum of floors ≤ `paid`, so `rpool` cannot underflow; dust stays in the contract. If an increment would not
  fit in 64 bits `dist()` skips instead of reverting, so staking can never be bricked.

Keys: headers + `kind v ticker dec S creator treasury fee pfee tax tmode cs Lq G vd0 phase sold real rd ra fd fa fc tacc
rpool locked stot sacc gh tc pc lh lt t_<n> p_<n> st_<addr> sd_<addr>`.

**Invariant:** contract DERO balance = `(phase 0 ? real : rd) + fd + tacc + rpool` (tested after every fuzz op).

---

## factory.bas — registry + treasury pointer (v2: no owner, both fixed at install)

`Initialize(treasury S, regfee U)`. `RegisterPool(scid H)`, `RegisterLaunch(scid H)`,
`RegisterNFT(scid H)`, `RegisterNFA(scid H)`: pay ≥ `regfee` DERO (forwarded to the treasury in the same call, so the
factory never holds a balance), append the SCID hex under `pool_<n>` / `launch_<n>` / `nft_<n>` / `nfa_<n>` with
counters `pc` / `lc` / `nc` (NFTs and NFAs share `nc`). Registrations are hints: the DVM cannot read other contracts,
so the **app verifies** each SCID's code equals the shipped template and its `treasury` equals `tstr` before listing
it (DECISIONS §10.1, RISKS R-3).

**No owner** (v2, operator ruling MT-J15(b), 2026-09-11). `treasury`, `tstr` and `regfee` are written by `Initialize`
and by nothing else, and `Initialize` refuses a second run, so both the treasury and the fee are fixed for the life of
the factory. There is no owner key, no setter, no `Renounce` and no transfer of ownership. No function reads
`SIGNER()`, so no key has any power over the factory, the installer's included, and an install at any ring size is
accepted (v1 refused one above ring 2 only because it stored the signer as the owner). A deployment that needs
another treasury or fee installs a new factory and re-registers the listings on it (`tools/simdeploy -migrate-from`);
the app reads exactly one factory. `regfee` is unbounded, as it was in v1: an unpayable fee can now only be
installed, once and in public, and `tools/verifydeploy` prints it.

v1 had an owner with `SetTreasury` and `SetRegFee` until `Renounce` (2026-09-11, FINDINGS_2026-09-10 SEC-K-02 /
LIV-08). `verifydeploy` still recognises a v1 factory by its state and fails one whose `owner` is stored.

Tests: `tests/registry_test.go` `TestFactoryFixedAtInstall` (every name that was ever owner-gated, and `Initialize`,
called by the installer, a stranger and a ring>2 caller at three heights: all refused, state unchanged, the factory
holding 0) and `TestFactoryOldEntrypointsAreGone` (from the source: only `Initialize` writes `treasury`, `tstr` or
`regfee`; nothing writes `owner`; nothing reads `SIGNER()`); `tests/zz_adversary_owner_test.go`.

## social.bas — profiles and follows

`Profile(name ≤ 32, bio ≤ 160, avatar ≤ 200)` → `pn_ pb_ pa_` + address. `Follow(target)` → `f_<me>_<target> = height`,
`fl_<me>_<i> = target`, `fc_<me>`, `fr_<target>`. `Unfollow(target)` deletes `f_` and decrements `fr_`
(`fl_` entries stay; the app filters on `f_`). No entrypoint takes value; each refuses DERO attached (2026-09-11,
GAP-027). As everywhere, an asset the contract cannot name is not seen, and is kept.

## chat.bas — rooms of messages, append-only

`InitializePrivate(treasury S, fee U, maxlen U)`: the address every message fee goes to, the exact fee a message pays
(0 is allowed) and the longest message, `1 ≤ maxlen ≤ 400`; all fixed at install. `Post(room, text)` needs ring size 2,
since the author is the signer, and public, and exactly `fee` DERO attached, which is forwarded to the treasury in the
same call, so the contract keeps no DERO (`TestChatFeeIsExactAndPaid`). `room` is 1–32 bytes and `text` 1 to `maxlen`.
There is no owner, no edit and no delete: the fee is the only spam control. Like `social.bas`, it cannot see an asset
attached, which is then kept.

Keys: `kind v treasury fee maxlen n`, and per room `rn_<room>`, its message count, and `rm_<room>_<j>` =
`author|height|text`, the author as its `dero1…` string. **What a client must do:**
- read a room as `rn_<room>` and then the last few `rm_<room>_<j>` by exact key, never by scanning the contract;
- split a record on its first two `|` only, because the text may contain the separator;
- mute, hide unknown senders and strip control characters on the reader's own device; nothing a client does changes
  what is stored.

The DMZ app names a market's room `#` plus the first 12 characters of its contract id (`app/chat.js:23`) and opens on
the room `#dmz`. Tested in `tests/chat_test.go`.

## nft.bas — holder NFT collection (ART-NFA headers)

`InitializePrivate(name, descr, icon, file, tags, collection, token H, minhold U, holdblocks U, maxsupply U, price U, treasury S, fee U)`.
`Lock()` with ≥ `minhold` of `token` records `lk_<addr>`, `lh_<addr>` (height), `lm_<addr> = 0`. `Mint()` after
`holdblocks` blocks (≥ 1, enforced at install), **once per address for the life of the contract** (`mt_<addr>`,
which `Unlock` never clears: lock → mint → unlock → lock again mints nothing more, 2026-09-06), while
`minted < maxsupply`, paying ≥ `price` DERO (excess refunded; `fee` bps to `fd`, rest to `cacc`) sends **1 unit of
the contract's own asset** = one edition and records `ed_<n> = minter`. `Unlock()` returns the tokens at any time
and clears the lock. `ClaimCreator()` (the creator, ringsize 2) and `ClaimFees()` (anyone, any ring size, pays
the treasury the `fd` DERO; there is no asset leg and no `fc` counter here). Every entrypoint refuses value it does
not take (2026-09-11, GAP-027): `Lock` takes only the token, `Mint` only DERO, `Unlock`, `ClaimCreator` and
`ClaimFees` nothing, none an edition. Invariants: DERO = `fd` + `cacc`, token = Σ `lk_`, editions out = `minted`
(`tests/zzconservation_test.go`).

## nfa.bas — one ART-NFA-MS1 asset per contract, priced in a quote

The standard's contract (civilware ART-NFA-MS1 v1.1.1) with a quote asset and a protocol fee: `docs/NFA_BUILD.md`,
`docs/NFA.md`. Entrypoints are the standard's — `InitializePrivate`, `ClaimOwnership`, `Update`, `Start`, `BuyItNow`,
`Bid`, `CloseListing`, `CancelListing` — plus `PayOwed(payee S)`. Four behaviours diverge from the standard
(2026-09-11, `docs/NFA.md` §2.5):

- A settlement sends only the royalty (to `creatorAddr`) and the remainder (to `owner`), both signers, and the unit
  to the buyer; the artificer's, the charity's and the protocol's shares are credited under `ow_<address>` and paid
  by `PayOwed` — anyone, any ring size, nothing attached, one transfer. The DVM keeps only the last registry lookup of
  a call's transfer list and half-commits a call whose unregistered destination is not the last (FINDINGS_2026-09-10
  DVM-1, LIV-01), so no settlement may name an address that has never signed; a `PayOwed` to one reverts whole and
  the share waits.
- `BuyItNow` on an ended auction settles it to the winner, as `Bid` does (NFA-1, LIV-02, ADV-PRICE-01).
- `Start` stores a clamped charity share only after checking the address (DVM-2).
- Every entrypoint refuses, with `RETURN 1`, value it does not take (NFA-4); `ClaimOwnership` and `Start` check it
  after their ring>2 branch, which sends the unit to the recorded owner, so a refusal can never destroy the unit.

Quote balance = `owed` + `currBidAmt` while an auction holds a bid (`tests/nfa_test.go` asserts it after every call).
Gated calls are all the standard's: `Update` (the creator; the owner too if `ownerCanUpdate` = 1) and the asset
owner's `Start` / `CloseListing` / `CancelListing`. The treasury receives and controls nothing. A DVM hazard the
repair ran into: a key `DELETE`d earlier in a call makes a later `EXISTS` or `LOAD` of it in the same call panic
(`dvm_store.go` 81-98), so no function may look a key up after deleting it.

## founders.bas — Founders Pass (v2: per-asset pools)

A fixed-edition NFT (the contract's own asset, 0 decimals) whose *registered* passes share what is deposited for
them, each deposit in the asset it was made in. v2 replaced v1 here on 2026-09-13; git keeps v1 (one DERO pool, a
2^32-scaled accumulator with eras, `Distribute`, `PayBatch` and the `minhst` floor). v2 was built on the operator's
rulings of that day:
- every fee is taken in the asset being traded, and the passes are paid in that asset, never converted to DERO;
- the 0.5 % swap fees are to be taken in XMR and ZEC off the DERO chain, and the treasury's bot is to pay holders
  their share of them directly. That is not built yet, because no XMR or ZEC fee is collected yet;
- the bot deposits with any number of registered passes (MT-J14). While none is registered, it holds what it owes
  and deposits it after one registers (`tools/treasurybot`);
- 20 editions, 4 reserved.

It closes MT-K01/MT-K04 (per-asset pools), and FEE-01 and FEE-02 with them. What the adoption changed in the tools,
the app and the runbook, and what is still to do: `docs/FOUNDERS_V2_NOTES.md`.

**Canonical sha256:** `348f872ed49ec218f361c4a70a5f6cd96ada38c3d3d8a0b960fba473c02746d3` (`reference.Canon`, 9,250
installed bytes; pinned by `TestFoundersV2CanonicalHash`).

| Entry | Who | What |
|---|---|---|
| `InitializePrivate(name, descr, icon, file, price, maxsupply, start, reserve, share, mfee, treasury)` | installer, ring 2, once | v1's arguments and bounds exactly: `descr` ≤ 240 bytes, `maxsupply` 1..100,000, `reserve ≤ maxsupply / 5`, `share ≤ 10000`, `mfee ≤ 1000`. It also refuses DERO attached, and stores the base58 alphabet (58 keys `b<c>`) for the address check |
| `Mint(n)` | ring 2 | v1's |
| `Register()` | ring 2, the passes attached | opens a new **position** `p`: `po_` owner, `pu_` units, `pt_` the tick it opened at |
| `Withdraw(p, u)` | the position's owner | closes `p` (`pc_` = tick) and returns `u` passes. If `u < pu_`, the rest is a new position opened at the same tick |
| `Claim(p, i, ka, kb)` | **anyone**, any ring size, nothing attached | pays `p`'s owner what it earned in asset `i`, as the call's only transfer |
| `Deposit(asset H)` | anyone | the asset named by its 32-byte id (zero = DERO) is split at once among the registered passes. Refused while none is registered, when a token deposit carries DERO, or when the pass is attached |
| `List(price)` / `Unlist(id)` / `Buy(id, n)` | as v1 | `mfee` bps of a sale is deposited for the passes as DERO; with no pass registered the seller receives the whole price |
| `SetXmr(addr)` / `SetZec(addr)` | a registered holder, ring 2; clearing: its owner | store, replace or (with `""`) clear the caller's Monero address (95 or 106 base58 characters) or Zcash transparent address (35 base58 characters starting with `t`) |

`Distribute` and `PayBatch` are gone: there is no waiting pool to release, and no call loops over holders.

**Maths: exact, unscaled, per asset.**
- Asset `i` keeps `ac_<i>`, the reward per registered pass in atomic units of that asset.
- A deposit `d` adds `inc = ⌊(au_<i> + d) / hst⌋` to it. `inc·hst` moves to `ap_<i>`, what the positions are owed.
  The remainder `au_<i> < hst` waits for the next deposit of that asset (`credit`).
- A position is paid `pu·(ac_b − ac_a)` with no division, where `ac_a` is the accumulator at its open or its last
  claim, and `ac_b` the accumulator at its close or now. So nothing is ever rounded in a holder's favour. The dust is
  `au_<i>`, below `hst` units per asset, and it stays in the contract.
- No uint64 can wrap:
  - `ad_<i>`, every unit ever credited to the asset, is refused past 2^64 − 1;
  - `ac_<i> ≤ ap_<i> + aw_<i> ≤ ad_<i>`;
  - `au + d ≤ ad`;
  - a payout is at most the deposits of its window, because a registered position's units are at most `hst` at each
    of them. `Claim` also checks `amt / u == ac_b − ac_a` and `amt ≤ ap_<i>`, and refuses otherwise.
- No eras and no 128-bit helpers: v2 drops v1's `muldiv`, `mulhi`, `divlu`, `nlz`, `shl` and `shr`.

**O(1) for any number of assets and holders.**
- **Positions.** A position's units never change, so no call has to settle every asset before a registration moves.
- **Ticks and records.** The tick `ev` counts `Register` and `Withdraw` calls. The first deposit of asset `i` in a
  tick that moves `ac_<i>` writes a record: `rt_<i>_<k>` the tick, and `ra_<i>_<k>` the value of `ac_<i>` just before.
  The accumulator as tick `t` began is `ra_<i>_<k>` for the first record with `rt ≥ t`, or `ac_<i>` when there is
  none.
- **Hints.** `Claim` is handed that `k` for the open (`ka`) and close (`kb`) ticks, found off chain by binary search.
  It checks each in O(1): `rt_<k−1> < t ≤ rt_<k>`. The right `k` is unique; any other panics, and the call is refused
  (`at`).
- **Measured.** Every entrypoint's compute gas is identical on a pass with 7 holders, 3 assets and 9 positions and
  on one with 103 holders, 43 assets, 144 positions and 84 records of one asset. Only the width of a stored number
  moves storage gas, by at most 5 (`TestFoundersV2CostDoesNotGrowWithAssetsOrHolders`):

| Call | Compute gas | Storage gas |
|---|---|---|
| `Deposit`, a new asset | 537,800 | 197 |
| `Deposit`, writing a record | 389,600 | 153–154 |
| `Deposit`, same tick | 332,400 | 144 |
| `Register`, holder already registered | 364,800 | 128–131 |
| `Withdraw`, part of a position | 518,000 | 143–148 |
| `Claim`, closed position, two hints read | 492,200 | 127–131 |
| `Claim`, open position | 414,400 | 123–126 |
| `SetXmr`, 106 characters | 4,852,400 | 401 |
| `Buy`, fee to the passes | 718,600 | 132–133 |

`SetXmr`'s cost is the character check, 106 lookups at most. It is bounded by the address length, never by what a
stranger can grow.

**FEE-01, FEE-02 and "any number" (MT-A08, MT-J14).**
- v1 parked a deposit made below its floor `minhst` in `und`, and let whoever crossed the floor share it.
- v2 has no floor and no waiting pool:
  - a `Deposit` while no pass is registered is refused (at ring 2 the depositor is refunded);
  - a sale's fee with no pass registered stays with the seller;
  - a pass registered after a deposit earns nothing of it.
- So nothing can accumulate unowned, and nothing can be swept.
- The ruling "any number" is the rule that follows from this: one registered pass or twenty take every deposit made
  while they are registered, pro rata. The bot deposits whenever at least one pass is registered, and MT-K05's N is
  not needed.
- What the contract cannot decide is whether fees the treasury earned while no pass was registered are owed to the
  passes at all. That is the bot's policy, and an operator question (`docs/FOUNDERS_V2_NOTES.md` §8).
- FEE-07 (a pass registered just before a deposit takes a full share of it) is v1's, unchanged and still open.
- Tests: `TestFoundersV2NothingWaitsUnowned`.

**XMR and ZEC.**
- A registered holder stores one Monero address (`xm_<addr>`) and one Zcash transparent address (`zc_<addr>`).
- The check is what the DVM can do: length (95 or 106; 35 beginning with `t`) and every character in the base58
  alphabet, one `EXISTS("b" + c)` per character.
- Replacing is allowed. An empty string clears, and a holder who has withdrawn everything can still clear, but no
  longer set.
- **A stored address is public on chain for good, so a fresh subaddress is the one to store.**
- The contract pays neither coin. It records where the treasury's bot should pay the holders' `share` of the XMR and
  ZEC swap fees, proportional to registered passes (`hu_<addr>` over `hst`). The bot makes every such payment, and no
  contract forces it.
- Tests: `TestFoundersV2PayoutAddresses`.

**No owner, no setter, no upgrade.**
- Every number and text is written by `InitializePrivate` alone, no function reads `creator`, and nothing can
  replace the code (`TestFoundersV2HasNoOwnerAndNoSetter`).
- Liveness: no state needs a person to leave it.
  - Anyone can push any position's rewards.
  - The dust needs nobody.
  - A registered or listed pass waits only on its own holder.
- Stray value: every entrypoint refuses DERO and passes it does not take, and `Claim` refuses the asset it pays. An
  asset a call does not name is kept, as everywhere (GAP-027; `TestFoundersV2StrayValue`).

Keys:
- globals: `nameHdr descrHdr iconURLHdr fileURL typeHdr kind v`=2 `creator treasury price maxsupply start reserve
  share mfee minted hst ev pn an ln la mdep initd b<c>`;
- per asset: `ah_<i>` (raw id), `ai_<hex id>` = i+1, `ac_ au_ ap_ ad_ aw_ ar_`, and the records `rt_<i>_<k>`
  `ra_<i>_<k>`;
- per position: `po_ pu_ pt_ pc_` and `pd_<i>_<p>`;
- per holder: `hu_ xm_ zc_`;
- market: `ls_ lp_ lu_`.

Invariants, tested after every fuzz step:
- per asset, balance = `ap_<i> + au_<i>`, and `ad_<i> = aw_<i> + ap_<i> + au_<i>`;
- `ap_<i>` equals the sum of what every position can claim;
- `hst` = the sum of the open positions' units;
- pass units in wallets = `minted − hst − listed`.

**Install.** Storage gas is 10,221 at the mainnet shape (the approved 227-byte description, no image) and 11,144 with
every header at its maximum, against the 20,000 cap. Compute gas is 3,765,000 (`TestFoundersV2InstallGas`).

**Tests:** `tests/founders_test.go` (v1's tests went with v1; the v2 names are kept):
- `TestFoundersV2PerAssetShares`: DERO and two tokens, registrations and withdrawals between deposits, every payout
  worked by hand;
- `TestFoundersV2ConservationFuzz`: seeded; per asset, accepted = claimed + held after every accepted and refused
  call, against a Go model of the whole contract;
- `TestFoundersV2CostDoesNotGrowWithAssetsOrHolders`;
- `TestFoundersV2PayoutAddresses`;
- `TestFoundersV2KeptBehaviours`: mint, install and reserve bounds, marketplace, market fee, register;
- `TestFoundersV2NothingWaitsUnowned`;
- `TestFoundersV2HintsAreChecked`;
- `TestFoundersV2StrayValue`;
- `TestFoundersV2InstallGas`;
- `TestFoundersV2CanonicalHash`;
- `TestFoundersV2HasNoOwnerAndNoSetter`.

`parse_test.go` pins the entrypoints. `canon_test.go` and `sendorder_test.go` hold v2 to the canon, send-order and
payee rules.

## swap.bas — hash-time-locked escrow (atomic swaps)

The DERO leg of the transparent-Zcash atomic swap (v2). No fee, no admin, immutable.

`Initialize(wmin U)`: the least `expiry − lock height`, fixed for the contract's life, refused at 0 and above
1,000,000 (Z-D, MT-A04).

| Entry | Who | What |
|---|---|---|
| `Lock(hash, taker, expiry, asset)` | anyone, ringsize 2 | locks the DERO sent with the call (asset = "") or the asset sent (asset = 64-hex SCID) under `hash` = 64 lower-case hex of sha256(secret); `taker` is required (GAP-024); `expiry` is an absolute height at least `wmin` and at most 1,000,000 ahead |
| `Claim(id, secret)` | **anyone**, ringsize 2 | before expiry, with the right secret: the funds go to the stored taker `t_<id>` whoever sends it, and the secret is stored in `x_<id>` |
| `Refund(id)` | the maker | from expiry: the funds go back |

Each lock is final in one of two ways (`st_<id>` 1 claimed / 2 refunded). Every exit refuses DERO and the lock's own
asset attached (GAP-027). The Zcash side is the P2SH script keyed to the same hash (`tools/zecswap/script.go`).
Tested in `tests/swap_test.go`, `swap_v2_test.go`, `swap_gaps_test.go` and `zzconservation_test.go`.

## htlc.bas — the Monero swap's DERO leg (v3)

The maker (who has DERO) locks DERO or one asset for a named taker (who has XMR) under three sha256 hashes: `ha` of the
taker's Monero key share `s_a`, `hk` of the maker's gate secret `k_b`, `hb` of the maker's key share `s_b`. No fee, no
admin, immutable. Protocol: `docs/XMR_SWAP.md`, `docs/XMR_SWAP_BUILD.md`.

`Initialize(w1min U, w2min U, g2 U)`, each fixed for the contract's life: the least `t1 − lock height`, the least
`t2 − t1` (MT-A03(a)), and `g2`, the Punish window. Each is refused at 0 and above 1,000,000, and `w1min + w2min`
above 999,999, where no lock could meet both.

| Entry | Who | When | What |
|---|---|---|---|
| `Lock(ha, hk, hb, taker, t1, t2, asset)` | anyone, ringsize 2 | — | hashes 64 lower-case hex; `taker` required; `t1 ≥ height + max(2, w1min)`, `t2 ≥ t1 + w2min`, `t2 ≤ height + 1,000,000` |
| `Claim(id, sa, kb)` | the taker | before `t1` | pays the taker; stores `s_a` in `xa_<id>` (st 1) |
| `Refund(id, sb)` | the maker | [`t1`, `t2`) | pays the maker; stores `s_b` in `xb_<id>` (st 2) |
| `Punish(id)` | anyone, ringsize 2 | [`t2`, `t2 + g2`) | pays only the taker, with no secret (st 3) |
| `Reclaim(id, sb)` | the maker | from `t2 + g2`, no end | pays the maker; stores `s_b` in `xb_<id>` as Refund does (st 4) |

The windows are disjoint, so at any height at most one exit is open. Before `t2 + g2` time alone carries a lock to an
exit someone may send, and in [`t2`, `t2 + g2`) anyone may send it. From `t2 + g2` an open lock waits on its own maker:
the maker's per-party window, the one exception the operator's liveness ruling MT-J11(a) accepts (v3: the study's
decision 17 (a), MT-J13(b), 2026-09-12). v2 let anyone Punish from `t2` for ever; v1 let only the taker, and a lock she
never punished was frozen for ever (LIV-07). Every exit refuses DERO and the lock's own asset attached (GAP-027).

Keys: `kind v n w1min w2min g2`, per lock `m_ t_ ha_ hk_ hb_ a_ v_ t1_ t2_ st_ xa_ xb_`. Tests: `tests/htlc_test.go`,
`htlc_v2_test.go`, `htlc_v3_test.go` (`TestHtlcV3WindowMatrix`, the liveness pin), `zzconservation_htlc_test.go`,
`zzadversarial_liveness_test.go` `TestHtlcPastT2TheLockAlwaysHasAnExit`.

## board.bas — atomic-swap order board

Holds no funds (v3). `Post(chain, giveAsset, giveAmount, wantAmount, wantAddr, hash, expiry)`, from the maker at
ring 2, advertises an offer: `hash` is 64 lower-case hex of sha256 of the maker's secret, and `wantAddr` is where he
wants the counter-chain coins. `Accept(id, refundPkh, cltv)` is the taker's answer, from any ring-2 address but the
maker's, before the offer's expiry: `refundPkh` is the 40-hex hash of her refund key and `cltv` her refund height, a
block height below 500,000,000. It stores her as `t_<id>` and sets the acceptance deadline `ad_<id>` = the accept
height + `aw` (200, fixed at install; ADV-PRICE-07). `Cancel(id)` is the maker's: an open offer at any time, an
accepted one from its expiry or its acceptance deadline. After Accept the maker locks the DERO leg in `swap.bas` for
the taker under the same `hash`, and the taker funds the Zcash script. No call takes value: each refuses DERO, and the
offered asset (GAP-027). Tested in `tests/board_test.go`, `board_v3_test.go` and `zzconservation_test.go`.

## vault.bas — k-of-n bonded federation wrapper (v3)

Mints its own asset one for one against foreign-chain deposits the signers have seen, and burns it to withdraw.
The signers can steal or freeze what they hold abroad; this contract makes the DERO side honest and auditable.
Trust model: `research/wrapper/README.md`. Daemon: `tools/vaultsigner` (v3; it refuses to start against any
other `v`, see the version guard at `main.go:455`). Driver:
`tools/simvault`. Tests: `vault_test.go`, `vault_v3_test.go`, `vault_limits_test.go`, `vault_fuzz_test.go`,
`vault_haltslash_test.go`, `vault_smoke_test.go`.

**Liveness by construction — the property that outranks every other rule here.** A deployed vault has no
operator: the deployer keeps no power after `Seal`, and the app may be published from a key that is then thrown
away. So no state may exist that needs a human to leave it. Every limit either empties by itself (the rolling mint
window slides; a deposit it turned away mints later untouched) or expires into a payment (the withdrawal delay,
which is longer for a large withdrawal but always finite). There is deliberately **no per-deposit maximum**,
because a deposit above one could never be minted at all and nobody is guaranteed to exist who could send it back.
Stopping a withdrawal takes a k-of-n `Veto`. A `Halt` stops what adds exposure — `Register`, `Attest`, `Mint` and the
daemon starting a new payment — and never the exit: `Burn` and `Released` work while halted. A halt is a lease of
20000 blocks that `Halt(1)` renews and that otherwise lapses on its own. A burn no signer marks as being paid returns
its units through `Reclaim` 20000 blocks after it became releasable; one a signer has marked waits for k votes, because
its coins may already have left. There is no manual override anywhere in the contract or the daemon.

**The invariant everything serves:** `foreign reserve >= sup`, checkable by anyone from the published
`viewkey`. `sup` counts every unit that is a claim on the reserve, including units held by the contract
between a burn and its release, and the fee units in `feepool`. A burn does **not** reduce `sup`; `Released` does.
The coins that pay a burn can leave the foreign chain before `Released` closes it, so what a signer daemon can check
at one block is `reserve >= sup − inflight`, where `inflight` is every burned withdrawal the contract has not
closed (`tools/vaultsigner/main.go:296-310`). Comparing the reserve to `sup` alone is not a stricter reading of the
invariant but a broken one: it halts on the first ordinary withdrawal and can never clear, because `Released` is
refused while halted (found and fixed 2026-09-07).

### Setup and governance

`InitializePrivate(k, treasury, cap, minbond, minburn, wfee, mindep, regfee, capratio, bigbps, bigdelay)` —
`2 ≤ k ≤ 19`, `0 < cap < 2⁶³`, `minbond ≠ 0`, `minburn ≠ 0`, `wfee < minburn`, `mindep ≠ 0`,
`capratio ≠ 0`, `bigbps ≤ 10000`, `bigdelay ≤ 20000`. Then owner `AddSigner(addr)` × n, each signer
`Bond()`s at least `minbond`, optional `SetLimits(winmax, wdelay)`, then `Seal()`. After `Seal` the owner has no
power at all.

- **`n > k` is enforced at `Seal`.** A k-of-k set can never slash anyone (`Slash` needs k votes from signers other
  than the target), so one compromised key would sit in the set for good and one lost key would freeze the vault.
- **`n ≥ 2k` is a deployment rule, not a check.** Only with k honest signers left over can a colluding quorum be
  slashed. 3-of-5 and 4-of-7 satisfy it; 2-of-3 does not, and is allowed because it is a reasonable small-vault
  choice — but in a 2-of-3 vault a colluding pair keeps its bonds. Say so in the runbook of any vault you deploy.
- **`bigbps` / `bigdelay`** (fixed at install, never changeable): a withdrawal larger than `bigbps` basis points
  of the supply waits `wdelay + bigdelay` blocks instead of `wdelay`, recorded as `w_<j>_rel` at burn time. This is
  the whole of the outsized-withdrawal defence: extra time for honest signers to notice and vote, never a refusal.
- **`cap ≤ capratio × bondtot`**, checked at `Seal`, `SetCap` and every `Mint`, and re-established downward by
  `Slash`. `cap` is in foreign base units and `bondtot` in DERO atomic units, so `capratio` is the operator's
  written-down price assumption (how many foreign base units of cap one DERO atomic unit of bond supports), not a
  live price: there is no oracle. Choose it conservatively and record the assumption. The check is implemented as
  `cap / capratio > bondtot`, which is permissive by at most `capratio − 1` base units.
- Only an **active** signer's bond counts in `bondtot`. A candidate for `AddSigner2` bonds first; their bond is
  held but not at stake.
- **`Unbond()` before `Seal` is open to anyone**, and after `Seal` it refuses every signer, past or present.
  The pre-seal opening exists because one mistyped signer address makes `Seal` permanently unreachable — nobody
  holds that key, so it can never bond — and without an escape every honest signer who had already bonded was
  locked out for good. The contract cannot correct a typo; it must not eat the bonds of the people who did
  nothing wrong. `bondtot` unwinds with the bond, clamped so it can never underflow.
- **`k ≤ 19` because `n ≤ 20`.** `AddSigner` and `AddSigner2` refuse a 21st signer, and `Seal` requires
  `n ≥ k + 1`, so a `k = 20` vault could never be sealed and every bond in it would be stuck. A parameter the
  contract calls legal must be one the contract can seal.
- **The signer ceiling is on the ACTIVE set**, not on slots ever used: `AddSigner2` compares `na`, not `n`.
  `n` is a monotonic index that `Slash` never decrements, so comparing it made every replacement consume one of
  the twenty for good — a vault that had rotated enough keys could no longer replace any, drifted to `na == k`
  as keys were lost, and then stopped with nobody able to intervene.
- `AddSigner2(addr)` (k votes) replaces a slashed or lost signer; `Tighten(winmax, wdelay)` (k votes)
  moves the limits in the safe direction only, with floors so a quorum cannot brick the vault; `SetViewKey(key)`
  (k votes) publishes the viewing key.

### The vote helper

Every k-of-n decision goes through one internal function, so the counting rule has exactly one implementation:

```
<f>c_<kb>_<ch>   how many signers chose <ch> in scope <kb>
<f>v_<kb>_<s>    the choice signer s made in that scope (a signer may move it; the old choice loses one)
<f>d_<kb>        the decision executed; the scope is closed and no further vote is counted
```

| f | family | scope `kb` | choice `ch` |
|---|---|---|---|
| `d` | Attest | `<se>_<he>_<bucket>_<j>_<ver>` | sha256 hex of the address |
| `m` | Mint | `<se>_<he>_<bucket>_<id>` | sha256 hex of `"<j>\|<amount>"` |
| `r` | Released | `<se>_<he>_<bucket>_<j>` | the foreign txid |
| `g` | Veto | `<se>_<he>_<bucket>_<j>` | `1` |
| `x` | Refunded | `<se>_<he>_<bucket>_<id>` | the foreign refund txid |
| `h` | Halt | `<se>_<he>_<bucket>` | the target state |
| `c` | SetCap | `<se>_<ce>` | the cap |
| `s` | Slash | `<se>` | the target, raw |
| `a` | AddSigner2 | `<se>` | the new signer, raw |
| `t` | Tighten | `<se>_<te>` | `"<winmax>\|<wdelay>"` |
| `k` | SetViewKey | `<se>_<ve>` | sha256 hex of the key |

`se` slash epoch, `he` halt epoch, `ce` cap epoch, `te` limits epoch, `ve` view-key epoch, `bucket` =
`BLOCK_HEIGHT() / 2000`. **Operational votes expire**: they carry the halt epoch and a height bucket, so k−1
signers cannot bank votes and hold a one-vote trigger for a later day, and a halt voids everything in flight.
Governance votes carry the epoch they change, which bumps on execution.

### Deposits

`Register()` costs exactly `regfee` (to the treasury) and allocates a **fresh** index every time, so nothing on
chain links two deposits of one user. An address may hold only one index the signers have not attested yet, which
bounds the attestation work one account can force on the federation.

`Attest(j, addr)` — k votes publish index `j`'s foreign address, bump `dver_<j>` and record `dh_<j>`, the height
at which the address became live. **A signer must ignore any foreign deposit older than that**: an address derived
from a future index can be paid before anyone owns it.

`Mint(j, txid, vout, amount)` — `txid` is exactly **32 raw bytes**; the contract builds the id itself as
`HEX(txid) + ":" + vout`, so one deposit output has exactly one id however a caller spells it, and `mint_<id>`
makes it mintable once, ever. Votes count per `(j, amount)` tuple, so one wrong vote can neither block nor
redirect a deposit, and a signer can move their vote until the quorum executes. Every vote checks `mindep`; the
k-th also checks `cap`, the bond ratio and the sliding window (`mw_<bucket>`, this bucket plus the previous one)
before crediting `sup` and paying the index owner.

`Refunded(txid, vout, ftx)` — k votes record a deposit the federation could not credit and sent back abroad. The
id is then closed: a deposit is minted **or** refunded, never both, and never later flipped when the cap rises.

### Withdrawals

`Burn(dest)` — at least `minburn`, destination 16–200 characters; the units stay in the contract, `wfee` is the
fee and the rest is the net amount. `sup` is unchanged here. The burn records `w_<j>_rel`, the height from which
it may be released: `wdelay` blocks, plus `bigdelay` when it is larger than `bigbps` of the supply.

`Released(j, ftx)` — k votes on one foreign txid. Refused before `w_<j>_rel` (the honest signers' window to notice a bad burn and halt) and refused if that `ftx` already settled another
withdrawal (`fu_<ftx>`). On execution `sup` drops by the net amount and `feepool` gains the fee.

`Veto(j)` — k votes return the units and the fee to whoever burned them, so a user is never left with neither the
units nor the coins (a destination the foreign chain rejects, a burn the federation will not honour). Refused while a
`Paying` mark on `j` is fresh (2000 blocks), allowed while halted. Released and vetoed are mutually exclusive and final.

`Reclaim(j)` — anyone; pays only `w_<j>_by`. Returns an open withdrawal's units and fee once
`BLOCK_HEIGHT() >= w_<j>_rel + 20000`, **unless a signer has ever marked it `Paying`** — a marked withdrawal may already
have had its coins sent, so it is the quorum's to settle (`Released`, or `Veto` once the mark lapses). Needs no
`SIGNER()`, so it works at any ring size.

`Halt(want)` — k votes inside one 2000-block bucket. `Halt(1)` stores `halted = BLOCK_HEIGHT() + 20000` and may be
voted again to renew; `Halt(0)` sets it to 0 and is refused when the vault is not halted. `halted > BLOCK_HEIGHT()` is
the only test of "halted" anywhere — the daemon and the app compare the same way.

`ClaimFees()` — the treasury takes the fee units; they were counted in `sup` throughout, and the treasury
withdraws the coins behind them by burning like any other holder. Unlike the markets' `ClaimFees`, the vault's
**is** treasury-gated (`vault.bas:434` compares `SIGNER()`, so it needs ring size 2), because the fee units are a
claim on the foreign reserve and stay counted in `sup` until the treasury burns them.

### Storage keys

```
kind v k n na se ce he te ve sealed halted(lapse height, 0 = open) owner treasury cap capratio sup un wn bondtot
minbond minburn wfee mindep regfee feepool bigbps bigdelay winmax wdelay viewkey
signers   sg_<i>   isg_<raw> 1 active 0 removed   bond_<raw>
users     ua_<j>   uc_<raw>   ul_<raw>_<i>   da_<j>   dver_<j>   dh_<j>
deposits  mint_<id> 1 minted 2 refunded   mj_<id>   mh_<id>   ma_<id>   rf_<id>
withdraw  w_<j>_to _v _f _by _h _rel _st (0 pending 1 released 2 vetoed) _ftx   fu_<ftx>
window    mw_<height/2000>
votes     <f>c_<kb>_<ch>   <f>v_<kb>_<s>   <f>d_<kb>
```

Every attacker-controlled string in a key is either last or fixed-length, and every variable-length field is
preceded by a numeric or fixed-length one, so no two logical tuples can produce the same key.

## Rules every contract here follows (each one paid for by a bug)

1. **Initialisation runs once.** `Initialize`/`InitializePrivate` stay callable after install on the real DVM;
   every contract stores `initd` and refuses a second call (`tests/reentry_test.go`).
2. **A vote family is keyed by an epoch that every membership change bumps.** Otherwise removing a signer
   silently lowers the threshold (vault.bas `se`, `ce`, `he`).
3. **An accumulator that only grows gets an era.** `founders.bas` `acc` and `launch.bas` `sacc` are
   per-unit reward accumulators scaled by 2^32; when `acc + inc` would wrap, the contract stores the final value
   under `ea_<era>` / `sea_<era>`, increments `era` / `sera` and restarts at zero. Each holder carries the era
   of their snapshot (`he_<addr>` / `se_<addr>`); settle walks the eras they missed. Without this a single
   holder and ~43,000 DERO of rewards bricked the contract for good (`TestFoundersAccSaturation`,
   `TestLaunchDistSaturation`).
4. **Fees apply to what was used, not what was sent.** A graduating buy refunds the overshoot untaxed
   (`TestLaunchGraduationRefundFee`).
4b. **Anything an off-chain process must reconcile against gets a monotone counter.** `fc` (fees claimed,
   launch.bas and pool.bas), `dep` (founders.bas deposits), `sup` (vault.bas supply). A balance that can be
   refilled is not a witness that an action landed; the treasury bot journal learned this from a 4 % shortfall
   in its own test.
5. **SIGNER() is only real at ring size 2.** At any larger ring the DVM hands the contract 33 zero bytes
   (measured: `tools/ringprobe`, `research/RING_PRIVACY.md`). Every entrypoint that pays the signer checks
   `IS_ADDRESS_VALID(SIGNER())` first and so fails closed; an entrypoint that pays a named `dest` argument may be
   called at any ring size and hides the payer among the ring.
6. **`Uint64 + String` is a parse error; key builders start with a string literal.** `LOAD` of a missing key
   panics (which reverts and refunds): guard with `EXISTS` where a key may be absent.
7. **`IF cond THEN GOTO n` is the only conditional form**, and line numbers must ascend within a function.
8. **A transfer to an address not known to have signed must be the last transfer the call makes.** The DVM keeps only
   the last registry lookup of a call's transfers, and half-commits a call whose unregistered destination is not the
   last (FINDINGS_2026-09-10 DVM-1, DVM-3). `tests/sendorder_test.go` `TestNoUnverifiedPayeeBeforeTheLastTransfer`
   reads every contract and fails one that breaks this rule.

## Named-recipient swaps (launch.bas and pool.bas, 2026-09-06)

`SwapDeroForAsset(min_out, publish)` and `SwapAssetForDero(min_out, publish)` pay `SIGNER()` and therefore need ring
size 2, whose signer anyone can compute from the chain. `SwapDeroForAssetTo(dest, min_out, publish)` and
`SwapAssetForDeroTo(dest, min_out, publish)` pay `ADDRESS_RAW(dest)` and never read the signer, so a wallet may send
them at any ring size (the app uses 16: 3,336 bytes against 1,848 at ring 2, same gas). All four are six-line
wrappers over the internal `buyto`/`sellto`; the maths, fees and tax are identical. A `To` call **never publishes a
trade record**, whatever `publish` says: a record would name `dest` as the trader, letting one payer inflate the
"distinct publishers" the ranking counts under any address it likes, and printing "bought" next to people who did
nothing (2026-09-06). Tests: `tests/ringswap_test.go`. Measurements and design: `research/RING_PRIVACY.md`.
