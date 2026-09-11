# The transparent-Zcash swap market — design note

*2026-09-09. BL-40. The first thing DMZ can honestly put on mainnet for a Zcash holder, and the only pair that
works today with no host: a hash-time-locked escrow on DERO against a hash-time-locked script on Zcash's
transparent side. Nobody holds anyone's coins. The cost is privacy on the Zcash leg, and this note says exactly
what is revealed before it says anything else.*

Companion documents: `research/wrapper/MECHANISM_REVIEW.md` (the verdict that put this first in the mainnet
sequence), `docs/XMR_SWAP.md` (the Monero swap, whose shape this note follows), `contracts/swap.bas` and
`contracts/board.bas` (the DERO leg as built in Phase 1), `research/crosschain/poc/zec-t-htlc` (the Zcash script,
proven under Zcash's own consensus interpreter for all seven cases: claim with the secret, claim with a wrong
secret refused, claim with the maker's key refused, refund after the lock time, refund before it refused, refund
with a final input refused, refund with the taker's key refused).

## 0. In one paragraph

A **maker** holding DERO, a DMZ token or a wrapped asset posts an offer on the board contract: how much of it for
how much ZEC, paid to which transparent Zcash address, under which hash. A **taker** holding ZEC accepts the
offer on the board, naming the Zcash key that will refund them and the block height at which it may. The maker
locks the offered asset in the DERO escrow for the taker under the hash; the taker, seeing that lock settle,
funds a Zcash script that pays the maker for the hash's preimage or the taker after the height. The maker takes
the ZEC with the preimage, which the whole world can then read on the Zcash chain; the taker takes the DERO
with it. If either side stops, the timelocks return every coin to whoever put it in. **Nothing here is
trustless in the sense of costless**: the maker's capital is exposed for hours to a taker who may never lock,
and the two Zcash addresses and the two DERO addresses in the swap are linked for anyone who looks.

## 1. What the Zcash leg reveals — read this first

Zcash's transparent side is Bitcoin script. Every transparent transaction is public: inputs, outputs, amounts,
addresses, scripts. A swap on it therefore publishes:

- **the amount of ZEC** traded, to the zatoshi;
- **the taker's transparent address** (the refund key of the script) and **the maker's transparent address**
  (its claim key) — both are in the redeem script, which is revealed the moment the script is spent;
- **the hash `H`**, which also appears in the DERO escrow; anyone can therefore join the Zcash transaction to the
  DERO swap id, and through the DERO side (`Claim` is a ring-size-2 call, so the taker's DERO address is
  computable, and `Lock` names it in clear) to **the taker's DERO address**. The maker's DERO address is the
  signer of `Lock` and `Post`, computable the same way;
- **the preimage `s`** in the maker's claim, harmless by itself but the join key between the two chains;
- if the taker's ZEC came from the shielded pool, **the unshielding**: an amount leaving the pool to a
  t-address shortly before it was locked.

None of this is a defect of DMZ; it is what transparent Zcash is. The app says it in these words, on the screen
where the taker enters their address, before the first button: *"This trade is visible on the Zcash chain
forever: the amount, your transparent address, the maker's, and the link to your DERO address. Zcash's shielded
pool cannot be used here."* A holder who wants privacy has the wrapped path (a federation, §10 of the mechanism
review) or waits for the shielded design (BL-50), and the screen says so too.

## 2. Roles, and why the order is fixed

The maker holds the secret. That is the one choice in this design that is not forced by the contracts, and it
is made for the customer:

- In a hash-time-locked swap the party who **knows the preimage locks first and refunds last**; the other
  party locks second under a shorter timelock and claims last, with the preimage read from the first party's
  claim. Whoever locks first bears the *free option*: the other side may watch the price and decide not to lock.
- A Zcash holder is the customer of this market. If they held the secret they would lock first and carry the
  option; the maker could let their ZEC sit for the whole timelock while the price moved. So the maker — the
  liquidity provider, who posts offers and prices this risk into them — holds the secret and locks first.
- The consequence is that a taker who accepts and never locks costs the maker the escrow's timelock in
  capital, for the price of one `Accept` transaction. §6 says what is done about it and what is not.

The Phase 1 board had the taker supply the hash at `Accept`, which put the secret with the taker. This note
moves the hash to `Post`. That is the one contract change (§4).

## 3. The protocol, step by step

Names: `H = SHA256(s)`, `s` thirty-two random bytes from the maker. `T_dero` a DERO height, `cltv` a Zcash height.
Heights are what both contracts read; wall-clock policy is derived from DERO's 18-second and Zcash's 75-second
blocks (§5).

| # | who | where | what |
|---|---|---|---|
| 1 | maker | `board.bas` `Post(chain, giveAsset, giveAmount, wantAmount, wantAddr, hash, expiry)` | `chain = "zcash-t"`; `giveAsset` empty for DERO or a 64-hex SCID (a token, or a wrapped asset); `wantAmount` in zatoshi; `wantAddr` the maker's transparent P2PKH address, whose key hash is the script's claim key; `hash = H`; `expiry` the height after which the offer dies by itself. Ring 2; the signer is the maker. Nothing is locked yet. |
| 2 | taker | `board.bas` `Accept(id, refundPkh, cltv)` | Binds the offer to the signer (the DERO address that will receive the escrow) and publishes the two things the maker needs to compute the Zcash script: the taker's refund key hash and the absolute Zcash height after which the taker may refund. `cltv` must lie inside policy relative to the current Zcash height, or the maker will not lock (§5). One `Accept` per offer; the maker can `Cancel` only before it. |
| 3 | maker | `swap.bas` `Lock(H, taker, T_dero, giveAsset)` with the asset attached | Within `P_lock` DERO blocks of the accept. `taker` is the accepting address; `T_dero` per policy. The maker's companion refuses to lock unless `cltv` and `T_dero` satisfy the margin in §5. |
| 4 | taker | Zcash | Waits until the DERO lock is `STABLE_LIMIT` = 8 blocks deep (DERO can replace a shallower block), checks every field — `H`, the taker address, `T_dero`, the asset and amount against the offer — then funds the P2SH script `IF SIZE <32> EQUALVERIFY SHA256 <H> EQUALVERIFY DUP HASH160 <makerPkh> EQUALVERIFY CHECKSIG ELSE <cltv> CHECKLOCKTIMEVERIFY DROP DUP HASH160 <refundPkh> EQUALVERIFY CHECKSIG ENDIF` (98 bytes) with exactly `wantAmount`. The maker can compute this script and its address from `Post` and `Accept` alone, so no message is needed. |
| 5 | maker | Zcash | Watches the script's address. When the funding output has `C_zec` confirmations (§5) and at least `wantAmount`, claims it with `s` before `cltv`. **`s` is now public on Zcash.** |
| 6 | taker | `swap.bas` `Claim(id, s)` | Reads `s` from the maker's claim transaction, claims the escrow before `T_dero`. Ring 2; the signer must be the taker `Lock` named. The contract stores `s` as `x_<id>`, so it is public on DERO too. |

"Checks every field" is four equalities and a bound, and the companion performs each of them; a taker who
skips one funds a script that cannot pay it back.

- `h_<id>` must be **character for character the lower-case hex** of the 32 bytes the taker put in the script
  (`zecswap.ParseSwapHash`). `swap.bas` `Claim` compares the stored string against `HEX(SHA256(secret))` and the
  DVM renders `HEX()` lower-case, so an upper-case or mixed-case hash is a DERO escrow that can never be opened
  while the Zcash claim, which holds `H` as bytes, still works.
- The redeem script must begin `SIZE <32> EQUALVERIFY`. Without it `OP_SHA256` accepts a preimage of any length
  up to 520 bytes while `swap.bas` accepts 32 bytes and nothing else, and a maker who picks a 31-byte secret
  takes the ZEC and leaves a secret the escrow refuses.
- `cltv` must pass `zecswap.CheckCLTV` against the Zcash tip the taker reads for itself: a height, not a Unix
  time, above the tip, and inside the window of §5.
- The claim key hash must be the maker’s `wantAddr` from the offer, and the refund key hash the taker’s own.
- `T_dero − now ≥ 1,200` DERO blocks, and the lock at least `STABLE_LIMIT` deep.

Exits:

- **Maker refunds** (`swap.bas` `Refund(id)` at or after `T_dero`): the taker never locked ZEC, or locked wrongly
  (the maker refuses to claim a script that does not match), or never claimed. The maker's asset comes back;
  `s` was never revealed, so the taker's ZEC, if locked, refunds after `cltv`.
- **Taker refunds** (Zcash, after `cltv`, with the refund key): the maker never claimed. Then the maker's escrow
  refunds after `T_dero`. Nobody loses coins; both lose time.
- **The bad exit, prevented by policy**: the maker claims ZEC late and the taker cannot claim DERO before
  `T_dero`. Then the maker has both. §5 makes `T_dero` far enough past `cltv` that a maker who claimed at the
  last possible Zcash block still leaves the taker hours on DERO.

## 4. The contracts

**`swap.bas` is used as built.** `Lock(hash, taker, expiry, asset)` with the value attached (DERO, or one asset
with no stray DERO — line 36 refuses both at once); `Claim(id, secret)` requires exactly 64 hex characters that
decode to 32 bytes and hash to `H`, before `expiry`, by the named taker; `Refund(id)` by the locker at or after
`expiry`. Every send is the last thing the call does (`tests/sendorder_test.go`). Expiry is bounded to one
million blocks. The secret is stored on claim, which is what lets a *DERO* watcher recover it — not needed in
this direction, but it is why the same contract serves a swap whose secret is revealed on DERO first.

**`board.bas` v2** (written 2026-09-10, `tests/board_test.go`, exercised by `tools/simswap -board`):

- `Post` gains `hash` (64 hex, stored as `h_<id>`); the secret is the maker's.
- `Accept(id, refundPkh, cltv)` replaces `Accept(id, hash, lockRef)`: `refundPkh` twenty bytes as 40 hex,
  `cltv` a `Uint64` above zero, both stored; the signer is stored as the taker. The offer moves to state 1.
- `Cancel`: the maker, at any time while the offer is open, and once it has expired whatever its state — so an
  accepted offer that went nowhere (v1 had no exit from `st_<id> = 1`) is closed by the maker without anyone
  else, and a cancelled offer cannot be cancelled again. 2,106 canonical bytes, 2,144 storage gas.
- Nothing on the board holds value, so nothing on the board can be drained. It is a bulletin, with signatures.

**Why not `htlc.bas`?** The Monero escrow has a third exit (`Punish`) because Monero cannot hash-lock and the
gate secret has to be enforced by a penalty. Zcash's transparent side hash-locks natively, so the classic two-exit
escrow is complete, and `swap.bas` is 3.5 KB against `htlc.bas`'s 5.8 KB of raw source.

## 5. Timelocks and confirmations — the numbers and where they come from

| constant | value | why |
|---|---|---|
| DERO block | 18 s | `BLOCK_TIME` |
| Zcash block | 75 s | mainnet target since Blossom |
| `STABLE_LIMIT` (DERO) | 8 blocks | DERO may replace a shallower block; the taker locks ZEC only against a lock this deep |
| `C_zec` | 10 confirmations (12.5 min) | the maker claims only a funding this deep; Zcash has no finality rule, and ten is the common exchange policy |
| `T_dero − lock height` | 1,600 blocks (8 h) | the maker's exposure if the taker walks (§6); long enough to contain the whole Zcash leg plus the taker's DERO claim |
| `cltv − Zcash height at lock` | 96 blocks (2 h) | the maker's window to claim; short, so a maker who walks costs the taker two hours, not eight |
| taker's precondition to lock ZEC | `T_dero − now ≥ 1,200` DERO blocks (6 h) | so that after a claim at the last Zcash block (≤ 2 h + reorg slack) the taker still has ≥ 3.5 h on DERO |
| `P_lock` | 200 DERO blocks (1 h) after `Accept` | the maker's deadline to lock; after it the taker treats the accept as dead and moves on |
| secret | 32 bytes | `swap.bas` line 25–27 refuses any other length, and the redeem script says the same in `SIZE <32> EQUALVERIFY`, so the two legs accept the same set of secrets and the companion is not the only guard |
| `cltv` tolerance | ± 32 blocks (40 min) around the 96 | the two parties read the tip at different moments and the accept is written before the lock. Below 64 blocks there is no window at all — the maker still needs `C_zec` = 10 confirmations (12.5 min) and time to broadcast; above 128 blocks the taker’s precondition `T_dero − now ≥ 1,200` stops holding. `zecswap.CheckCLTV` enforces it; nothing on DERO can, because the DVM cannot see the Zcash height |

The margins are argued from block times, as the Monero swap's are. Measuring them against real reorg statistics
on both chains is BL-30, and the numbers above are simulator policy until then. On the simulator the driver uses
short values (`tools/simswap`: expiry a handful of blocks) to exercise the exits in minutes.

## 6. The free option, and what is done about it

After `Accept`, the maker locks; a taker who then does nothing costs the maker eight hours of capital for one
transaction fee. A taker who watches the price for two hours before deciding to lock ZEC has a free option on
the maker's asset for that long. The option runs the other way too: once the taker has funded, the maker may
decline to claim and let her refund at `cltv`, about two hours, so for that window the option is his
(`docs/ATOMIC_SWAP_STUDY.md` §2.2).

What v1 does: **nothing beyond disclosure and pricing.** The offer screen tells the maker the exposure in hours
before they post; makers price it into `wantAmount`; the board shows a maker's completed and abandoned swaps by
DERO address (both are on chain: `st_<id>` on the board and on the escrow), so a taker who accepts and walks
builds a visible record, and so does a maker who never locks.

What v1 does not do, and why:

- **A taker bond held by the board**, returned on completion, paid to the maker on a walk. The board cannot see
  the escrow (the DVM has no cross-contract reads), so "the taker walked" would be the maker's word; a maker
  could accept honest ZEC locks, never claim, refund the escrow, and collect the bond every time. Rejected.
- **Symmetric bonds burned on any failure.** Deters both sides equally and lets either side grief the other at
  the cost of their own bond. A defensible v2; it needs the merged contract below and an operator decision on
  the bond size. Recorded as open (D-7 in the backlog).
- **Merging board and escrow** so the offer *is* the lock: `Offer(hash, wantAmount, wantAddr, expiry)` with the
  asset attached, `Accept(id, refundPkh, cltv)` binding the taker, then the Zcash leg. It removes step 3 and the
  `P_lock` wait, and makes a bond enforceable inside one contract. It also means every posted offer locks
  capital from the moment of posting, which is the same exposure moved earlier. Worth building once v1 has run
  on testnet; not before.

## 7. What each party runs

**The DERO leg needs no companion.** `Post`, `Accept`, `Lock`, `Claim` and `Refund` are ordinary contract calls
the app sends through the wallet (XSWD), exactly like a trade. The app can read every field it needs to display
from the two contracts.

**The Zcash leg needs a companion**, because no consumer Zcash wallet builds or spends a P2SH hash-time-locked
script. `tools/zecswap` (to be built, BL-42's sibling): one binary, no host, run by the party on the Zcash side
of each step:

- for the taker: computes the script and address from the offer and the accept, verifies the DERO lock through
  a DERO node RPC of the taker's choosing (the same read the app makes), funds the script from a transparent
  address the taker controls, watches for the maker's claim, extracts `s`, and either hands it to the app for
  the DERO `Claim` or sends the claim itself through the DERO wallet RPC; refunds after `cltv` if the maker
  never claimed;
- for the maker: watches the script address, counts confirmations, claims with `s`, and refunds the DERO
  escrow after `T_dero` if it comes to that.

It talks to Zcash through **`zcashd`/`zebrad` RPC on the user's own machine**, or through a light-client
protocol — which trusts the `lightwalletd` server it connects to for what the chain contains. That server is a
trusted party for liveness and for what it shows; the companion's `status` names it, and the default is a node
the user runs. Keys stay in the user's Zcash wallet and DERO wallet; the companion signs the script spend with a
key the user gives it for that purpose only, and the docs say so in those words.

The Monero companion's crash-fuzz discipline applies: durable state before every irreversible step, a `status`
command that says what happens if it stops now, a `resume` that reads both chains and continues.

**Signing and digests — the dependency decision (2026-09-10).** Three things the Zcash leg needs and how each is
provided, with the answer to "what does this provide that a smaller primitive cannot":

- **ECDSA over secp256k1**: `github.com/decred/dcrd/dcrec/secp256k1/v4` v4.3.0, pinned by sum in `tools/go.sum`;
  pure Go, no cgo, no transitive dependency beyond the standard library, no network. It provides constant-time
  field and scalar arithmetic and RFC 6979 deterministic nonces, verified upstream against published vectors. A
  hand-written curve would leak the key through timing or a bad nonce, and no test in this repository could
  detect either. It sees one key: the one the user hands the companion for one spend, zeroed after.
- **BLAKE2b with a personalization string**, which every ZIP 244 digest uses: written in-tree
  (`tools/zecswap/blake2.go`, a hundred lines) because `golang.org/x/crypto/blake2b` cannot set the parameter
  block. A hash has no secret and no timing surface, so no library is needed; it is pinned by RFC 7693's vector
  and by every ZIP 244 vector the package can read.
- **ZIP 244 digests and ZIP 225 serialization** for v5 transactions: transparent parts built and read, an
  Orchard bundle read (a maker's wallet may add Orchard change to the claim that reveals the secret) but never
  built, a Sapling bundle refused by name. The transaction id and the SIGHASH_ALL digest match Zcash's own test
  vectors (`tools/zecswap/testdata/zip_0244.json`, four transactions with transparent inputs and Orchard
  bundles). The consensus branch id is read from the node, never fixed at signing: `getblockchaininfo`’s
  `consensus.nextblock` — the epoch of the block the transaction will be **mined** in, not `consensus.chaintip`,
  which is the epoch of the block already at the tip. The two differ only across an activation boundary, which
  is the one moment the choice matters. `zecswap.CheckBranchID` takes the node’s answer and refuses to sign when
  the build does not recognise it (the binary is older than the chain) or when it disagrees with the upgrade
  schedule the build carries; `zecswap.Upgrades` holds that schedule, NU5 through NU6.3, from ZIPs 252, 253,
  255, 257 and 258.

  A wrong branch id is not “invalid, never a loss”, which is what this note said until 2026-09-10. The claim
  branch of the redeem script carries no timelock, so it never expires: a taker who cannot broadcast a valid
  refund at `cltv` — a stale branch id, a crashed watcher, an absence — faces a maker who has already refunded
  the DERO after `T_dero` and can still spend the ZEC with `s` at any later time. The six hours between `cltv`
  and `T_dero` are the taker’s whole margin.

## 8. The screen

- **Market list** — the board, read from `board.bas`: for each open offer the asset, the amount, the price in
  ZEC per unit, the maker's completed and abandoned counts, and time left. ZEC first: the tab reads *"Buy with
  ZEC"* and *"Sell for ZEC"*, with DERO shown only where it is the asset offered.
- **The privacy statement** (§1) on the accept screen and the post screen, above the fields, every time.
- **Accept** — the taker enters a transparent refund address (the app derives the key hash, refuses shielded
  addresses with the reason), sees `cltv` computed by policy from a Zcash height the app reads from the
  companion or a public block explorer the user names (and is told is trusted for that number), and the fees:
  the `Accept` gas in DERO, the Zcash funding fee (ZIP-317: a marginal fee per logical action, at least 10,000
  zatoshi), the maker's expected claim time, and the taker's own worst case ("if the maker never claims, your
  ZEC is back after 2 hours; you pay two Zcash fees").
- **Post** — the maker enters the asset and amounts and the transparent address to be paid at, sees the
  exposure (eight hours per accepted offer) and the gas for `Post` and `Lock`, and the app generates `s` — in
  the wallet's memory for the session and written nowhere by the browser: the maker's companion is the durable
  holder, and the screen says that a lost secret means a refund after eight hours, not a loss.
- **Swap page** — `#zswap/<board id>`: the state of both legs as the app can read them (the DERO leg live; the
  Zcash leg from the companion when connected, otherwise "not visible from here"), the next action for the
  visitor's role, and every deadline as a wall-clock time with the block it comes from.
- Nothing in the browser holds a key. The maker's secret is the one new thing the browser generates, and it is
  handed to the companion, not stored.

## 9. Adversarial review

| # | attack or failure | what happens | who bears it |
|---|---|---|---|
| Z1 | Taker accepts, never locks ZEC | maker refunds at `T_dero` | maker: 8 h of capital + two fees |
| Z2 | Maker never locks after accept | taker waits `P_lock`, walks; the accept row is dead | taker: one fee, an hour |
| Z3 | Taker locks the wrong amount or a script with a wrong `H`, key or `cltv` | the maker's companion refuses to claim; both refund | both: time |
| Z4 | Maker claims ZEC at the last block before `cltv` | a race, not a safe exit: from `cltv` the taker's refund can spend the same output, and the maker's claim has already put `s` in a public mempool. If her refund is mined first she holds her ZEC and `s`, and claims the DERO as well | the maker, who loses both legs; the companion must claim well before `cltv` (`docs/ATOMIC_SWAP_STUDY.md` §2.2) |
| Z5 | Zcash reorg removes the funding after the maker's claim | the claim is removed with it; `s` is already public; the taker refunds ZEC after `cltv` and claims DERO with `s` | maker: both assets gone. Prevented by `C_zec` = 10 before claiming |
| Z6 | DERO reorg removes the maker's `Lock` after the taker funded ZEC | the taker's ZEC refunds after `cltv`; the maker cannot claim (no `s` was revealed) | taker: 2 h. Prevented by the 8-block wait |
| Z7 | Maker posts one offer, two takers try to accept | the board takes the first; the second `Accept` fails on state | nobody |
| Z8 | Same `H` reused across offers | the second taker to fund could be claimed with the first's `s`; the companion generates `s` per offer and refuses a reused `H` (the board can index `h_<id>`) | prevented |
| Z9 | The taker funds with a transaction whose input is not final (`nSequence` max) or with `nLockTime` below `cltv` | the refund branch's `CHECKLOCKTIMEVERIFY` fails for a final input; the PoC's fifth and sixth cases | taker cannot refund early — which is the design |
| Z10 | Fee attack: the maker's claim cannot pay a fee because the funding is dust-sized | the maker checks `wantAmount` against the fee floor before posting; the companion refuses to fund below it | prevented |
| Z11 | Front-running `Claim` on DERO: someone sees `s` on Zcash and claims the escrow first | `Claim` requires the signer to be the named taker (line 30) | prevented |
| Z12 | Front-running the maker's Zcash claim | the script pays only the maker's key hash; knowing `s` is not enough | prevented |
| Z13 | Linkability | §1: the pairs of addresses are joined by `H` and `s` on two public chains | everyone in the swap, forever; disclosed |
| Z14 | The taker's DERO node lies about the lock | the taker locks ZEC against a lock that is not there; refunds after `cltv` | taker: 2 h. The node is a trusted party for that read; the app names it |
| Z15 | The maker's `lightwalletd` lies about the funding | the maker claims a funding that is not there: the claim fails, nothing is revealed; or the maker misses a real one and the taker refunds | time only; the server is named as trusted |
| Z16 | Liveness: the maker's companion is off when the funding confirms | the taker refunds after `cltv`; the maker's escrow refunds after `T_dero` | both: time. The companion's `status` says how long it may sleep |
| Z17 | The taker cannot broadcast a refund at `cltv` — offline, a crashed watcher, a signature under a branch id the chain has left behind | the claim branch of the redeem script carries no timelock, so it never expires. After `T_dero` the maker has refunded the DERO and can still spend the ZEC with `s`, at any later moment | taker: the whole ZEC leg. The margin is the six hours between `cltv` (2 h) and `T_dero` (8 h); the refund must be attempted at `cltv` and retried until it confirms |

## 10. Trust statement

- **Trustless in what it settles**: no party can end with both assets while the other ends with neither,
  given the policy margins of §5 and the confirmation waits of Z5/Z6. That is the whole of the claim.
- **Not costless**: the free option on the maker (§6), the timelock on both sides' capital, and two chains'
  fees.
- **Not private**: §1, in full, on the Zcash side; ring size 2 on the DERO side for every call that must know
  its signer.
- **Trusted parties, named**: the DERO node each party reads (for what the chain contains); the Zcash node or
  `lightwalletd` each party reads (the same); nothing else. No DMZ server, signer, relay or operator is in the
  path, and nothing needs one to finish: every state has a timelocked exit.

## 11. What is built, what is not

Built and verified: `swap.bas` and `board.bas` (Phase 1, `tests/run.sh -run 'Swap|Board'`); the Zcash script under
Zcash's consensus interpreter (`poc/zec-t-htlc`, all seven cases); the DERO leg on the simulator (`tools/simswap`:
lock, a late claim refused by the chain, refund; `research/crosschain/poc/simswap-run.log`, and this cycle's rerun
in the HISTORY entry).

Built 2026-09-10: `board.bas` v2 (§4) with its DVM test and the driver's board scenario; the script layer of
`tools/zecswap` (§7) — the redeem script, its P2SH address on both networks, transparent-address parsing that
refuses shielded forms with the reason, the two spend layouts — tested byte for byte against the PoC's vector.
Not built: the rest of `tools/zecswap` (the watcher against a node, the funding and spending transactions); the Zcash testnet run (BL-42); the screens (§8); the
measurement behind §5 (BL-30). In that order, each its own cycle, each verified before the next.

## 12. Sources

Zcash protocol specification (transparent transactions, script), BIP 65 (`CHECKLOCKTIMEVERIFY`), ZIP-317
(fees), `zec-t-htlc/run.log` (this repo), `swap.bas`/`board.bas` (this repo). Recorded with what was read in
`research/crosschain/SOURCES.md`.
