# Veil contracts

Nine immutable DVM-BASIC contracts. None defines `UpdateCode`. Every state-changing entrypoint that pays the caller
requires **ringsize 2** (the node only reveals `SIGNER()` at ringsize 2; DECISIONS §2.4); the `…To` entrypoints pay a
named address and work at any ring size (§ private mode below). Arithmetic rules: DECISIONS §10.2. Executable
specification: `../reference/`. Tests: `../tests/` (run with `tests/run.ps1`).

Sizes and install cost (storage gas units, cap 20000 per TX — the code counts; install gas is the byte count plus
a few hundred units, measured where given):

| Contract | Bytes (2026-09-06) | Install storage gas | Compute gas |
|---|---|---|---|
| `pool.bas` | 13 166 | ~13 400 | 409 000 |
| `launch.bas` | 15 781 | ~16 000 | ~1 200 000 |
| `founders.bas` | 12 586 | ~12 800 | — |
| `vault.bas` | 14 098 | ~14 400 (14 276 measured at 14 010 B) | — |
| `nft.bas` | 4 025 | ~4 600 | — |
| `swap.bas` | 3 599 | — | — |
| `factory.bas` | 2 852 | ~3 100 | — |
| `board.bas` | 2 848 | — | — |
| `social.bas` | 2 062 | — | — |

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
  Accrued protocol fees sit in `fd` (DERO) / `fa` (asset) until the treasury calls `ClaimFees()`.
- **Social feed keys** (pool and launch): `tc` count and `t_<n>` = `side|address|in|out|height|timestamp` written
  only when the trader passes `publish = 1` (side 1 = bought asset, 2 = sold asset). `pc` count and `p_<n>` =
  `height|timestamp|rd|ra` = closing reserves of the previous active block, written on the first reserve change in a
  new block. Read ranges with `DERO.GetSC keysstring`.
- **Return 0 commits, anything else reverts and refunds** (DECISIONS §2.3). Every `GOTO 900` below is a revert.

---

## pool.bas — LP-owned constant-product pool (asset ↔ DERO)

`InitializePrivate(asset H, treasury S, fee U, pfee U)` — asset ≠ DERO, fee ≤ 100, pfee ≤ 10000, treasury valid.

| Entrypoint | Params | Value in | Effect |
|---|---|---|---|
| `AddLiquidity` | `min_lp` | DERO `d` + asset `a` | First deposit (d ≥ 1 DERO): `lp = d`, mints `d − 1000` LP (MIN_LIQ locked forever). Else `L = min(⌊d·lp/rd⌋, ⌊a·lp/ra⌋)`, the other side's requirement `⌈L·r/lp⌉` is taken and the excess refunded. Mints `L` of the pool's own SCID. |
| `RemoveLiquidity` | `min_dero, min_asset` | LP `l` (this SCID) | `⌊l·rd/lp⌋` DERO and `⌊l·ra/lp⌋` asset; `l ≤ lp − 1000`. |
| `SwapDeroForAsset` | `min_out, publish` | DERO `d` | `fee = ⌈d·fee/10⁴⌉`, `pf = ⌊fee·pfee/10⁴⌋`, `out = ⌊(d−fee)·ra / (rd + d − fee)⌋`; `rd += d − pf`, `ra −= out`, `fd += pf`. |
| `SwapAssetForDero` | `min_out, publish` | asset `a` | symmetric with the fee in asset units; `fa += pf`. |
| `ClaimFees` | — | — | treasury only: sends `fd` DERO and `fa` asset. |

Keys: `kind v asset treasury fee pfee rd ra lp fd fa tc pc lh lt t_<n> p_<n>`.

**Invariants (tested after every fuzz op):** contract DERO balance = `rd + fd`; asset balance = `ra + fa`; `lp ≥ 1000`
after the first deposit; `k = rd·ra` never decreases on swaps (floor rounding); every credit is guarded against
wrapping the real balance (`reserve + fees + incoming`).

**Why the refund side is always sufficient.** If `⌊d·lp/rd⌋ ≤ ⌊a·lp/ra⌋` then `a·lp/ra ≥ L`, so `a ≥ L·ra/lp`, and since
`a` is an integer `a ≥ ⌈L·ra/lp⌉`. Symmetric for the other side.

---

## launch.bas — memecoin: bonding curve → built-in AMM, fee, tax, staking

`InitializePrivate(name S, ticker S, descr S, icon S, tags S, dec U, supply U, grad U, cbps U, tax U, tmode U, treasury S, fee U, pfee U)`
- `supply ∈ [10⁶, 2⁶³)`, `grad ∈ [10 DERO, 10⁷ DERO]`, `cbps ≤ 500` (creator allocation ≤ 5 %), `tax ≤ 1000` (≤ 10 %),
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

Keys: headers + `kind v ticker dec S creator treasury fee pfee tax tmode cs Lq G vd0 phase sold real rd ra fd fa tacc
rpool locked stot sacc gh tc pc lh lt t_<n> p_<n> st_<addr> sd_<addr>`.

**Invariant:** contract DERO balance = `(phase 0 ? real : rd) + fd + tacc + rpool` (tested after every fuzz op).

---

## factory.bas — registry + treasury pointer (owner-operated, minimal mutability)

`Initialize(treasury S, regfee U)`. Owner = installer. `SetTreasury`, `SetRegFee`, `TransferOwnership` /
`ClaimOwnership` (two-step). `RegisterPool(scid H)`, `RegisterLaunch(scid H)`, `RegisterNFT(scid H)`: pay ≥ `regfee`
DERO (forwarded to the treasury at once), append the SCID hex under `pool_<n>` / `launch_<n>` / `nft_<n>` with counters
`pc` / `lc` / `nc`. Registrations are hints: the DVM cannot read other contracts, so the **app verifies** each SCID's
code equals the shipped template and its `treasury` equals `tstr` before listing it (DECISIONS §10.1, RISKS R-3).

## social.bas — profiles and follows

`Profile(name ≤ 32, bio ≤ 160, avatar ≤ 200)` → `pn_ pb_ pa_` + address. `Follow(target)` → `f_<me>_<target> = height`,
`fl_<me>_<i> = target`, `fc_<me>`, `fr_<target>`. `Unfollow(target)` deletes `f_` and decrements `fr_`
(`fl_` entries stay; the app filters on `f_`).

## nft.bas — holder NFT collection (ART-NFA headers)

`InitializePrivate(name, descr, icon, file, tags, collection, token H, minhold U, holdblocks U, maxsupply U, price U, treasury S, fee U)`.
`Lock()` with ≥ `minhold` of `token` records `lk_<addr>`, `lh_<addr>` (height), `lm_<addr> = 0`. `Mint()` after
`holdblocks` blocks (≥ 1, enforced at install), **once per address for the life of the contract** (`mt_<addr>`,
which `Unlock` never clears: lock → mint → unlock → lock again mints nothing more, 2026-09-06), while
`minted < maxsupply`, paying ≥ `price` DERO (excess refunded; `fee` bps to `fd`, rest to `cacc`) sends **1 unit of
the contract's own asset** = one edition and records `ed_<n> = minter`. `Unlock()` returns the tokens at any time
and clears the lock. `ClaimCreator()`, `ClaimFees()`.


## founders.bas — Veil Founders Pass

A fixed-edition NFT (the contract's own asset, 0 decimals) whose *registered* holders receive a share of the
exchange's fees in DERO. Entrypoints:

| Entry | Who | What |
|---|---|---|
| `Mint(n)` | anyone, ringsize 2 | from block `start`, exactly `n × price` DERO; `n` passes to the caller, DERO to the treasury |
| `Register()` | holder | passes sent with the call start earning (registry `h_<i>`, `hi_`, `hu_`, `hd_`) |
| `Withdraw(u)` | holder | settles rewards, returns `u` passes; leaving compacts the registry (swap-delete) |
| `Claim()` | holder | sends pending rewards to the wallet |
| `Deposit()` | anyone | DERO for the holders (the treasury deposits its promised `share`); O(1) |
| `Distribute()` | anyone | releases DERO that arrived while nobody was registered |
| `PayBatch(from, n ≤ 15)` | anyone | pushes pending rewards to registered wallets `from..from+n-1` |
| `List(price)` / `Unlist(id)` / `Buy(id, n)` | holders / buyers | fixed-price marketplace, `mfee` bps of each sale to the holders |

Maths: `acc` accumulates `floor(pool·2^32 / hst)` per distribution; the pool shrinks by `ceil(inc·hst / 2^32)`; a
holder's pending reward is `floor(hu·(acc − hd) / 2^32)`, capped at `rpool`. Invariant: contract DERO ==
`rpool + und`. Nothing is released while fewer than `minhst = 1 + ⌊maxsupply/50⌋` units are registered (about 2 %
of the editions): DERO that arrives earlier waits in `und`, so the first registrant of one unit cannot sweep what
accrued before anyone held the pass (2026-09-06). Tested in `tests/founders_test.go` (rules, gas, 3 000-step
randomised run against a big.Int model).

## swap.bas — hash-time-locked escrow (atomic swaps)

The DERO leg of a cross-chain atomic swap. No fee, no admin, immutable.

| Entry | Who | What |
|---|---|---|
| `Lock(hash, taker, expiry, asset)` | anyone, ringsize 2 | locks the DERO sent with the call (asset = "") or the asset sent (asset = 64-hex SCID) under `hash` = hex(sha256(secret)); `taker` is an address or "" for anyone; `expiry` is an absolute height at most 1 000 000 ahead |
| `Claim(id, secret)` | the taker (or anyone when none was named) | before expiry, with the right secret: the funds go to the caller and the secret is stored in `x_<id>` for the other chain's leg |
| `Refund(id)` | the maker | after expiry: the funds go back |

Each lock is final in one of two ways (`st_<id>` 1 claimed / 2 refunded). The other chain runs the mirror escrow keyed to the
same hash (a script on Bitcoin/Litecoin/transparent Zcash, a small program on Solana). Tested in `tests/swap_test.go`.

## board.bas — atomic-swap order board

Holds no funds. `Post(chain, giveAsset, giveAmount, wantAmount, wantAddr, expiry)` advertises an offer (the maker's
counter-chain address travels with it); `Accept(id, hash, lockRef)` is the taker's answer once their counter-chain
escrow is locked under `hash`; `Cancel(id)` withdraws an open offer. The maker then locks the DERO leg in `swap.bas`
for the taker with an expiry shorter than the taker's counter-chain lock. Tested in `tests/board_test.go`.

## vault.bas — k-of-n bonded federation wrapper (v3)

Mints its own asset one for one against foreign-chain deposits the signers have seen, and burns it to withdraw.
The signers can steal or freeze what they hold abroad; this contract makes the DERO side honest and auditable.
Trust model: `research/wrapper/README.md`. Daemon: `tools/vaultsigner` (v2 only, see the version guard). Driver:
`tools/simvault`. Tests: `vault_test.go`, `vault_v3_test.go`, `vault_limits_test.go`, `vault_fuzz_test.go`,
`vault_haltslash_test.go`, `vault_smoke_test.go`.

**The invariant everything serves:** `foreign reserve >= sup`, at every moment, checkable by anyone from the
published `viewkey`. `sup` counts every unit that is a claim on the reserve, including units held by the contract
between a burn and its release, and the fee units in `feepool`. A burn does **not** reduce `sup`; `Released` does.

### Setup and governance

`InitializePrivate(k, treasury, cap, minbond, minburn, wfee, mindep, regfee, capratio)` — `2 ≤ k ≤ 20`,
`wfee < minburn`, `mindep ≠ 0`, `capratio ≠ 0`. Then owner `AddSigner(addr)` × n, each signer `Bond()`s at least
`minbond`, optional `SetLimits(maxdep, winmax, wdelay)`, then `Seal()`. After `Seal` the owner has no power at all.

- **`n > k` is enforced at `Seal`.** A k-of-k set can never slash anyone (`Slash` needs k votes from signers other
  than the target), so one compromised key would sit in the set for good and one lost key would freeze the vault.
- **`n ≥ 2k` is a deployment rule, not a check.** Only with k honest signers left over can a colluding quorum be
  slashed. 3-of-5 and 4-of-7 satisfy it; 2-of-3 does not, and is allowed because it is a reasonable small-vault
  choice — but in a 2-of-3 vault a colluding pair keeps its bonds. Say so in the runbook of any vault you deploy.
- **`cap ≤ capratio × bondtot`**, checked at `Seal`, `SetCap` and every `Mint`, and re-established downward by
  `Slash`. `cap` is in foreign base units and `bondtot` in DERO atomic units, so `capratio` is the operator's
  written-down price assumption (how many foreign base units of cap one DERO atomic unit of bond supports), not a
  live price: there is no oracle. Choose it conservatively and record the assumption. The check is implemented as
  `cap / capratio > bondtot`, which is permissive by at most `capratio − 1` base units.
- Only an **active** signer's bond counts in `bondtot`. A candidate for `AddSigner2` bonds first; their bond is
  held but not at stake, and `Unbond()` returns it if they are never voted in. A signer, past or present, can
  never unbond.
- `AddSigner2(addr)` (k votes) replaces a slashed or lost signer; `Tighten(maxdep, winmax, wdelay)` (k votes)
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
| `t` | Tighten | `<se>_<te>` | `"<maxdep>\|<winmax>\|<wdelay>"` |
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
redirect a deposit, and a signer can move their vote until the quorum executes. The k-th vote checks `mindep`,
`cap`, the bond ratio, `maxdep` and the sliding window (`mw_<bucket>`, this bucket plus the previous one) before
crediting `sup` and paying the index owner.

`Refunded(txid, vout, ftx)` — k votes record a deposit the federation could not credit and sent back abroad. The
id is then closed: a deposit is minted **or** refunded, never both, and never later flipped when the cap rises.

### Withdrawals

`Burn(dest)` — at least `minburn`, destination 16–200 characters; the units stay in the contract, `wfee` is the
fee and the rest is the net amount. `sup` is unchanged here.

`Released(j, ftx)` — k votes on one foreign txid. Refused before `wdelay` blocks have passed since the burn (the
honest signers' window to notice a bad burn and halt) and refused if that `ftx` already settled another
withdrawal (`fu_<ftx>`). On execution `sup` drops by the net amount and `feepool` gains the fee.

`Veto(j)` — k votes return the units and the fee to whoever burned them, so a user is never left with neither the
units nor the coins (a destination the foreign chain rejects, a burn the federation will not honour). Allowed
while halted, which is when it is needed most. Released and vetoed are mutually exclusive and final.

`ClaimFees()` — the treasury takes the fee units; they were counted in `sup` throughout, and the treasury
withdraws the coins behind them by burning like any other holder.

### Storage keys

```
kind v k n na se ce he te ve sealed halted owner treasury cap capratio sup un wn bondtot
minbond minburn wfee mindep regfee feepool maxdep winmax wdelay viewkey
signers   sg_<i>   isg_<raw> 1 active 0 removed   bond_<raw>
users     ua_<j>   uc_<raw>   ul_<raw>_<i>   da_<j>   dver_<j>   dh_<j>
deposits  mint_<id> 1 minted 2 refunded   mj_<id>   mh_<id>   ma_<id>   rf_<id>
withdraw  w_<j>_to _v _f _by _h _st (0 pending 1 released 2 vetoed) _ftx   fu_<ftx>
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

## Named-recipient swaps (launch.bas and pool.bas, 2026-09-06)

`SwapDeroForAsset(min_out, publish)` and `SwapAssetForDero(min_out, publish)` pay `SIGNER()` and therefore need ring
size 2, whose signer anyone can compute from the chain. `SwapDeroForAssetTo(dest, min_out, publish)` and
`SwapAssetForDeroTo(dest, min_out, publish)` pay `ADDRESS_RAW(dest)` and never read the signer, so a wallet may send
them at any ring size (the app uses 16: 3,336 bytes against 1,848 at ring 2, same gas). All four are six-line
wrappers over the internal `buyto`/`sellto`; the maths, fees and tax are identical. A `To` call **never publishes a
trade record**, whatever `publish` says: a record would name `dest` as the trader, letting one payer inflate the
"distinct publishers" the ranking counts under any address it likes, and printing "bought" next to people who did
nothing (2026-09-06). Tests: `tests/ringswap_test.go`. Measurements and design: `research/RING_PRIVACY.md`.
