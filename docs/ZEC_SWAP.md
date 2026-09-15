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
  t-address shortly before it was locked;
- **that it is a DMZ swap, to anyone at all**: the maker's claim pays the 0.5 % swap fee (§7, *The swap fee*) to one
  public treasury address, the same for every DMZ Zcash swap on the network. Anyone can list every DMZ Zcash swap by
  listing the payments to that address, and each payment sits in a claim that carries the amount, both transparent
  addresses, `H` and `s`.

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
| 2 | taker | `board.bas` `Accept(id, refundPkh, cltv)` | Binds the offer to the signer (the DERO address that will receive the escrow) and publishes the two things the maker needs to compute the Zcash script: the taker's refund key hash and the absolute Zcash height after which the taker may refund. `cltv` must lie inside policy relative to the current Zcash height, or the maker will not lock (§5); the board itself refuses a `cltv` of 500,000,000 or more, which a Zcash script reads as a Unix time. v3 stores the acceptance deadline `ad_<id>`, the accept height + `aw` (200 blocks, `P_lock`). One `Accept` per offer; the maker can `Cancel` before it, and after it from `ad_<id>` or the offer's expiry, whichever comes first. |
| 3 | maker | `swap.bas` `Lock(H, taker, T_dero, giveAsset)` with the asset attached | Before the board's `ad_<id>`, `P_lock` DERO blocks after the accept. `taker` is the accepting address — required: `swap.bas` refuses a lock that names none — and must be registered on DERO (a Claim's transfer to an unregistered one is refused and reverted: nothing is lost, and nothing is paid until she registers); `T_dero` per policy, and `swap.bas` refuses one fewer than its `wmin` blocks ahead of the height the Lock *executes* at, which DERO allows up to 10 blocks after the wallet built it (§5). The maker's companion refuses to lock unless `cltv` and `T_dero` satisfy the margin in §5; the taker's funds nothing against a lock mined at or after `ad_<id>`. |
| 4 | taker | Zcash | Waits until the DERO lock is `STABLE_LIMIT` = 8 blocks deep (DERO can replace a shallower block), checks every field — `H`, the taker address, `T_dero`, the asset and amount against the offer — then funds the P2SH script `IF SIZE <32> EQUALVERIFY SHA256 <H> EQUALVERIFY DUP HASH160 <makerPkh> EQUALVERIFY CHECKSIG ELSE <cltv> CHECKLOCKTIMEVERIFY DROP DUP HASH160 <refundPkh> EQUALVERIFY CHECKSIG ENDIF` (98 bytes) with exactly `wantAmount`. The maker can compute this script and its address from `Post` and `Accept` alone, so no message is needed. |
| 5 | maker | Zcash | Watches the script's address. When the funding output has `C_zec` confirmations (§5) and at least `wantAmount`, claims it with `s` before `cltv`. **`s` is now public on Zcash.** |
| 6 | anyone | `swap.bas` `Claim(id, s)` | Reads `s` from the maker's claim transaction and sends the Claim before `T_dero`. Ring 2, no value attached. v2 pays the taker `Lock` named whoever signs it, so the maker's companion can send it straight after its Zcash claim (`docs/ATOMIC_SWAP_STUDY.md` §2.2, Z-C), and so can the taker, her second device or anyone who reads `s`: the sender pays the fee and receives nothing. The contract stores `s` as `x_<id>`, so it is public on DERO too. |

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
  last possible Zcash block still leaves the taker hours on DERO, and with `swap.bas` v2 they need not be her
  own: anyone who reads `s` can send her Claim, and it pays only her (Z19).

## 4. The contracts

**`swap.bas` v2** (2026-09-11, `tests/swap_v2_test.go` and `tests/swap_gaps_test.go`, exercised by `tools/simswap`).

- `Initialize(wmin)` fixes the minimum escrow width at install and stores it as `wmin`: 1,600 blocks for this
  market (`T_dero`, §5), 20 on the simulator (Z-D, GAP-025). The install fails closed: the DVM refuses an
  Initialize missing the argument (`vendor/derohe/dvm/sc.go:232-234`), line 6 refuses 0 and any width past the
  million-block expiry ceiling, and a refused Initialize installs nothing
  (`vendor/derohe/blockchain/transaction_execute.go:400-406` returns before the state is committed).
- `Lock(hash, taker, expiry, asset)` with the value attached (DERO, or one asset with no stray DERO — line 36
  refuses both at once). It must name its taker (GAP-024, as `htlc.bas` does), and its expiry must lie at least
  `wmin` and at most a million blocks ahead (line 24).
- `Claim(id, secret)` requires exactly 64 hex characters that decode to 32 bytes and hash to `H`, before `expiry`,
  from a ring-2 signer, and pays the taker `Lock` named, whoever sends it (Z-C). v1 paid only the named taker's own
  Claim, so a taker who was away when a late maker claim revealed `s` lost her DERO leg to the Refund; v2 lets
  anyone complete it for her (`docs/ATOMIC_SWAP_STUDY.md` §2.2). It does nothing against Z17, where `s` is
  revealed only after the maker has refunded.
- `Refund(id)` by the locker at or after `expiry`.
- Both exits refuse DERO attached and, since v2, the lock's own asset attached, which would otherwise stay in the
  contract for ever (GAP-027). An asset the lock does not name cannot be seen: the DVM reads `ASSETVALUE` only for
  an asset it is named.
- Lock, Claim and Refund keep v1's arguments and keys; `v` is 2, and `wmin` is new. The named taker must be
  registered on DERO, or every Claim is refused and reverted until she is; the contract cannot test registration
  without paying for it, so the maker's companion checks it before locking. Every send is the last thing the call
  does (`tests/sendorder_test.go`). The secret is stored on claim, which is what lets a *DERO* watcher recover it —
  not needed in this direction, but it is why the same contract serves a swap whose secret is revealed on DERO first.
  2,767 canonical bytes; 2,813 storage gas to install with `wmin` = 3, 2,816 with 1,600.

**`wmin` belongs to a deployment, not to its code.** The same code installed with a smaller `wmin` has the same code
hash, so pinning the code does not pin the width. The taker's companion reads `wmin` from the contract and refuses a
`swap.bas` whose `wmin` is below its own policy (1,600 here) in the same step as it checks the code, and the app
shows it beside the SCID.

**`board.bas` v3** (2026-09-11, `tests/board_test.go` and `tests/board_v3_test.go`, exercised by
`tools/simswap -board`). v2, written 2026-09-10:

- `Post` gains `hash` (64 hex, stored as `h_<id>`); the secret is the maker's.
- `Accept(id, refundPkh, cltv)` replaces `Accept(id, hash, lockRef)`: `refundPkh` twenty bytes as 40 hex,
  `cltv` a `Uint64` above zero, both stored; the signer is stored as the taker. The offer moves to state 1.
- `Cancel`: the maker, at any time while the offer is open, and once it has expired whatever its state — so an
  accepted offer that went nowhere (v1 had no exit from `st_<id> = 1`) is closed by the maker without anyone
  else, and a cancelled offer cannot be cancelled again.

v3 keeps every entrypoint, argument and key of v2, and adds:

- `aw` = 200, stored at install: the acceptance window, §5's `P_lock` (assumed, as every number there is until
  BL-30). It is written into the code, not taken at install, because the DVM refuses a call missing a declared
  argument and a window a deployer could leave out would fail open.
- `Accept` stores `ad_<id>` = its height + `aw`, the height by which the maker must have locked. The taker's
  companion funds nothing against a lock mined at or after it, and the app shows the accept dead from it — read
  from the chain, not from anyone's word.
- `Cancel` of an accepted offer from `ad_<id>` or `e_<id>`, whichever comes first. One `Accept` from anyone, which
  need never fund, therefore holds an offer for at most `aw` blocks (about an hour), not until its expiry, up to a
  million blocks out (ADV-PRICE-07). The deadline does not reopen the row to another taker: a maker who locked in
  time named the first one, and a second `Accept` would overwrite the key hash and `cltv` his script was built
  from. He cancels and posts again, under a new `H` (Z8).
- `Accept` refuses `cltv` ≥ 500,000,000: a transparent script reads that as a Unix time, every one of them already
  past, so the taker could refund at once (ZEC-3). Whether `cltv` sits inside §5's window of the Zcash tip only
  the companions can tell; the DVM cannot see Zcash.
- Every call refuses the offer's give asset attached, as it already refused DERO (GAP-027): Post line 29, Accept
  line 28, Cancel line 25. An asset the offer does not name cannot be seen — the DVM has no builtin that lists what
  a call carries — so nothing should be attached to a board call.
- `v` is 3. 2,611 canonical bytes, 2,652 storage gas.
- Nothing on the board holds value, so nothing on the board can be drained. It is a bulletin, with signatures. A
  swap whose DERO leg was locked in time goes on in `swap.bas` whatever its row says, so the app reads the escrow,
  not `st_<id>`, for a swap in progress.

**Why not `htlc.bas`?** The Monero escrow has a third exit (`Punish`) because Monero cannot hash-lock and the
gate secret has to be enforced by a penalty. Zcash's transparent side hash-locks natively, so the classic two-exit
escrow is complete, and `swap.bas` is the smaller contract of the two.

## 5. Timelocks and confirmations — the numbers and where they come from

| constant | value | why |
|---|---|---|
| DERO block | 18 s | `BLOCK_TIME` |
| Zcash block | 75 s | mainnet target since Blossom |
| `STABLE_LIMIT` (DERO) | 8 blocks | DERO may replace a shallower block; the taker locks ZEC only against a lock this deep |
| `C_zec` | 10 confirmations (12.5 min) | the maker claims only a funding this deep; Zcash has no finality rule, and ten is the common exchange policy |
| `T_dero − lock height` | 1,600 blocks (8 h), or the escrow's `wmin` + 11 when that is wider: 1,611 with the `wmin` of 1,600 a mainnet deployment installs | the maker's exposure if the taker walks (§6); long enough to contain the whole Zcash leg plus the taker's DERO claim. `swap.bas` v2 refuses a lock narrower than its `wmin` at the height the Lock executes, up to 10 blocks after the height it was built at, so the companion sets `T_dero` = the build height + max(1,600, `wmin` + 11) (`Policy.TDeroWidth`; the paragraph below) |
| `cltv − Zcash height at lock` | 96 blocks (2 h) | the maker's window to claim; short, so a maker who walks costs the taker two hours, not eight |
| taker's precondition to lock ZEC | `T_dero − now ≥ 1,200` DERO blocks (6 h) | so that after a claim at the last Zcash block (≤ 2 h + reorg slack) the taker still has ≥ 3.5 h on DERO |
| `P_lock` | 200 DERO blocks (1 h) after `Accept` | the maker's deadline to lock. `board.bas` v3 carries it: `aw` = 200, and each accept's `ad_<id>` = the accept height + `aw`. From it the maker may cancel the row, and the taker's companion funds nothing against a lock mined at or after it |
| secret | 32 bytes | `swap.bas` line 25–27 refuses any other length, and the redeem script says the same in `SIZE <32> EQUALVERIFY`, so the two legs accept the same set of secrets and the companion is not the only guard |
| `cltv` tolerance | ± 32 blocks (40 min) around the 96 | the two parties read the tip at different moments and the accept is written before the lock. Below 64 blocks there is no window at all — the maker still needs `C_zec` = 10 confirmations (12.5 min) and time to broadcast; above 128 blocks the taker’s precondition `T_dero − now ≥ 1,200` stops holding. `zecswap.CheckCLTV` enforces it; nothing on DERO can, because the DVM cannot see the Zcash height |

**Every height is judged when the call executes, not when it was sent.** DERO mines a transaction only while the
block's height minus the transaction's height (the wallet's tip when it built it) is below 11
(`vendor/derohe/blockchain/blockchain.go:748`), so a call built at height `h` executes by `h + 10` or never. `swap.bas`
and `board.bas` read `BLOCK_HEIGHT()` as it executes. The companion counts 11 (`zecswap.DeroTxValidity`), one block
conservative, as the Monero companion does, and holds each call to it:

- `Lock`: `T_dero` ≥ the build height + `wmin` + 11 (above). The earlier `T_dero` = the build height + 1,603 was refused
  on chain, and its fee lost, by any Lock mined 4 or more blocks late against a `wmin` of 1,600.
- The lock deadline (`P_lock`, `ad_<id>`): the maker sends his Lock only while the build height + 11 + `SettleBlocks` is
  below it, so the Lock executes, and the taker can read it, before the deadline. The taker counts `P_lock` from the
  last height her own `Accept` could execute at, because `board.bas` stamps `ad_<id>` from that height, and she walks
  only then. Counted from the height she built it at, she could walk while the maker could still lock in time.
- `Accept`: the taker sends it only while the build height + 11 is below the offer's expiry. `Post`: the expiry the
  maker writes is more than 11 blocks above the build height.
- `Claim`: no margin. A Claim that executes too late costs one fee, and a Claim not sent costs the taker the whole leg, so
  she and the maker's relay send it while `h + 1 < T_dero`. The taker calls her Claim settled only once the last block
  it could execute in, its build height + 11, is `STABLE_LIMIT` deep.
- `Refund`, and `Cancel` of an accepted offer: no margin. Each is refused before a height, never after one, so a late
  execution only helps.
- A second `Lock` is not refused. It is a second escrow under the same hash, for the same taker, and the one secret opens
  both. So the maker writes the attempt to disk before he sends a Lock. After a restart, he waits 11 blocks from that
  height before he looks for the Lock and decides whether to send another; the other calls wait `SettleBlocks`.

`Policy.Validate` holds the numbers to this rule: `TakerMinDeroLeft` + 11 + 8 ≤ the `T_dero` window, so a lock that
executed as late as DERO allows, then waited to its stable depth, can still be funded; `P_lock` > 11 + `SettleBlocks`;
an offer lives more than 22 blocks; and the wall-clock relation leaves the taker's Claim two stable depths plus 11
blocks after `cltv`.

The margins are argued from block times, as the Monero swap's are. Measuring them against real reorg statistics
on both chains is BL-30, and the numbers above are simulator policy until then. On the simulator the driver uses
short values (`tools/simswap`: `wmin` = 20 and expiries a few blocks past it) to exercise the exits in minutes, and
the board's real `aw` = 200, so its acceptance deadline takes an hour.

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
script. `tools/zecswap`: one binary, no host, run by the party on the Zcash side of each step:

- for the taker: computes the script and address from the offer and the accept, verifies the DERO lock through
  a DERO node RPC of the taker's choosing (the same read the app makes), funds the script from a transparent
  address the taker controls, watches for the maker's claim, extracts `s`, and either hands it to the app for
  the DERO `Claim` or sends the claim itself through the DERO wallet RPC; refunds after `cltv` if the maker
  never claimed;
- for the maker: watches the script address, counts confirmations, claims with `s`, and refunds the DERO
  escrow after `T_dero` if it comes to that.

**The Zcash node.**
- It talks to Zcash through node JSON-RPC, and through nothing else:
  - **`zebrad`** first, with its RPC enabled;
  - `zcashd` only with `txindex=1`, `experimentalfeatures=1`, `insightexplorer=1` and a reindex. zcashd is past its end
    of support (2026-07-18), will not follow NU6.3, and is untested here;
  - not `lightwalletd`: it speaks gRPC, which would need a new dependency, and it would be just as much a trusted party
    (`tools/zecswap/node.go:23-29`).
- It uses exactly nine methods (`node.go`, its METHODS comment). Seven are used on every run: `getblockchaininfo`,
  `getblockhash`, `getaddressutxos`, `getaddresstxids`, `getrawtransaction` (verbose), `sendrawtransaction` and
  `gettxout`. Two are used only with `-verify-headers`: `getblock` (verbosity 1) and `getblockheader` (raw).
  `tools/regtest/rpcgate` serves exactly these nine, and a test holds its list to `node.go`'s calls.
- The first seven were exercised against zebrad 6.3.0 on regtest (`research/regtest/zecswap-e2e.log`). The last two
  were exercised on regtest, and through the gate against the Zcash testnet node on historical blocks
  (`research/regtest/zecswap-headers.log`).

**The flags.**
- `-network` is required and has no default. Only `-network regtest` reaches regtest, and only for a node whose block 0
  is regtest's genesis.
- `-zcash` may be repeated, with `-zcash-cookie` or `-zcash-user`/`-zcash-pass`.
- `-daemon` (DERO) may be repeated.
- `-wallet` takes its login from `VEIL_RPC_LOGIN`, or from `-dero-login`, which is refused on mainnet. On mainnet a
  wallet that answers without credentials is refused.
- `-board` and `-escrow` default to the network's entry in `deploy/networks.json` (`-networks`; regtest reads the
  simulator's). The id is only a pointer: the code at it must still hash to the pinned `board.bas` v3 and `swap.bas`
  v2. An entry with no id fails closed.
- `-verify-headers` turns on the header gate (below). It is off by default.

**Every node named is a trusted party**, the local one included, and above all a remote one. It is trusted for everything
the companion knows about that chain: the height, the outputs paying the script, how deep they are, the transactions
that spend them. One that colludes with the counterparty costs its reader a whole leg (Z14, Z15). Extra nodes are
witnesses that must all agree before the maker reveals `s` and before the taker funds (`quorum.go`). They turn "one
liar" into "every source colludes" and never remove the trust. A node on the user's own machine is the default.

**The header gate** (`-verify-headers`, `tools/zecswap/spv.go`, `tools/zecspv`). It narrows the node's trust to what costs
work.
- Before the maker reveals `s`, and again on a restart in "claiming", the funding's block and every header above it to
  the tip must verify. That means Equihash, the difficulty rule with 200 headers below the funding (so its own
  difficulty is recomputed, not taken from the node), a floor of Mainnet nBits `1c00802e` × 16, a newest header at most
  an hour old, and the funding in the block by its Merkle branch. The leaf is computed from the funding's own bytes. On
  any failure there is no claim; it tries again next pass, and the claim cutoff still ends it.
- Before the taker funds, the headers up to the tip must verify the same way, and her `cltv` checks read that tip.
- It proves work, not that this is the network's chain. Ten confirmations cost a forger about 8.94 blocks of network
  work (`docs/ZCASH_HEADER_VERIFICATION.md` §4).
- On regtest it is skipped, and says so: zebrad's regtest mines with no proof of work. On testnet it checks the plumbing
  only: the minimum-difficulty rule makes forged headers nearly free.
- It stays off by default until it has run against the Zcash testnet node.

**What it checks and writes.**
- It builds v5 or v6 transactions (ZIP 225, ZIP 229) and reads versions 1–6. It stops on any transaction at the script
  address that it cannot read. It computes the id of every v5 transaction from its bytes, Sapling and Orchard
  bundles included, so a funding with shielded change is credited by its own id. It refuses to credit a v6
  transaction with shielded parts, the one kind whose id it cannot compute (GAP-039).
- On every DERO node it checks the code pins of `board.bas` v3 and `swap.bas` v2 (`pin.go`), the escrow's `wmin` against
  its own policy, and the taker's registration.
- The maker's companion sends the taker's DERO Claim once its own Zcash claim is deep: swap v2 pays the named taker
  whoever sends it.
- The `cltv` on the board is checked against the companion's own node: by the maker before he locks, and by the taker
  both when she adopts an accept made elsewhere (the app computes `cltv` from a height typed by hand) and again before
  she funds. Too soon or too far is refused.
- The maker's secret is read from a file or stdin, never from the command line.
- It lists every file it writes, in a directory outside any repository by default: the state, its lock, the
  pre-signed refund, and the used-hash and consumed-funding registers.
- Keys stay in the user's Zcash wallet and DERO wallet. The companion signs the script spend with a key the user gives
  it for that purpose only, and the docs say so in those words.

**The swap fee** (the operator's ruling of 2026-09-13: 0.5 % on every swap, taken in ZEC, never in DERO).
- **Who pays, and when.** The maker, who locks the DERO-side asset and receives the ZEC. His claim, the transaction
  that spends the taker's P2SH output with `s`, has a second output of `floor(funding × 50 / 10,000)` zatoshi to the
  network's treasury (`zecswap.SwapFee`, `tools/zecswap/fee.go`). The network fee (ZIP 317) still comes out of the
  maker's own output. Two outputs are still two logical actions, so it stays 10,000 zatoshi: on a 200,000-zatoshi swap
  the treasury receives 1,000 and the maker 189,000.
- **The treasury** is `zecTreasury` in the network's block of `deploy/networks.json` (regtest reads the simulator's).
  The companion reads it from the record only; no flag names another. It must be a transparent address, P2PKH or
  P2SH, on the swap's network, because the companion builds only transparent transactions.
  - On mainnet an empty or invalid one refuses every swap, for maker and taker alike, before any node is contacted,
    with a message naming the field. The mainnet block is empty today, so no mainnet swap starts.
  - Off mainnet an empty one means no fee, and the companion says so at start. An invalid one is refused everywhere.
  - The maker's state fixes the address at setup, so a later edit of the record does not move a running swap's fee.
  - The regtest address's key was made outside the repository and is in no file here.
- **Dust.** zebrad 6.3.0 refuses a transaction with an output below `3 × (100 × (size + 148) / 1,000)` zatoshi
  (`zebra-chain/src/transparent.rs`, `ONE_THIRD_DUST_THRESHOLD_RATE` and `Output::is_dust`, applied by the mempool's
  `reject_if_non_standard_tx`): 54 zatoshi for a P2PKH or a P2SH output. A fee below that is left out, and the maker's
  log says so. No swap the companion allows reaches it: a claim spends at least 15,000 zatoshi (the 10,000 fee and the
  5,000 output floor), whose fee is 75, and the policy's smallest swap is 100,000.
- **No fee on a failed swap.** A refund never carries it: `BuildRefund` refuses a request that names a treasury. The
  taker's funding, her pre-signed refunds and her amounts are what they were. Her companion only states, when she
  accepts, the fee the maker will pay.
- **Enforced by the software, not by a contract.** The script pays the maker's key whatever else the claim pays, and
  nothing on Zcash or in `swap.bas` checks the fee. A modified maker client can leave the output out and the swap
  still completes (Z20). The fee holds against unmodified clients only.
- **What it reveals.** Every fee output pays the same public address, so anyone can list every DMZ Zcash swap from the
  payments to it (§1).

**Reorganisations** (tested on the real regtest zebrad with `invalidateblock`, `research/regtest/zecswap-reorg.log`).
- If the chain drops a funding the maker read below `C_zec`, the companion reports it and the funding counts for nothing.
  Depth starts over on the chain as it is, and `s` stays with the maker until a funding is `C_zec` deep there.
- If a claim is taken off the chain with its funding, the companion reports it and puts the funding back from its own
  bytes. Once the funding is `C_zec` deep again it sends the same claim, while the claim cutoff is open or once the
  taker's Claim has landed. Only a node's refusal counts as a verdict: a timeout is not one.
- If the funding can never be mined again (its coin spent a second time) and the taker's Claim has landed, the maker's
  companion ends in `lost`: the Z5 loss, stated in its outcome. With the escrow still open, it refunds the DERO at
  `T_dero` instead, and its outcome says the secret was revealed.
- A companion that has already ended does not see a later reorganisation. `status` flags a claim that is no longer
  where it was seen mined.

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
- **ZIP 244 digests, ZIP 225 serialization for v5, and ZIP 229 for v6.** The v6 digests were added on
  2026-09-11 and checked against real zebrad 6.3.0 transactions, with no new dependency. Current Zcash wallets
  build v6 by default under NU6.3, so a claim that reveals `s` may be either. For v5: transparent parts built and read, an
  Orchard bundle read (a maker's wallet may add Orchard change to the claim that reveals the secret) but never
  built, and a Sapling bundle read and digested (ZIP 244 T.3, added 2026-09-12) but never built. The transaction
  id and the SIGHASH_ALL digest match every vector in Zcash's own test file (`tools/zecswap/testdata/zip_0244.json`),
  including those carrying Sapling spends and outputs. A v6 transaction with shielded parts is refused: ZIP 229 has
  no vectors here for its shielded digests. The consensus branch id is read from the node, never fixed at signing: `getblockchaininfo`’s
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

- **`-status 127.0.0.1:PORT`** (off by default) serves the swap's stages to the DMZ app on the same machine
  (docs/SWAP_STATUS.md). It serves on 127.0.0.1 only, behind a Host check and a one-time pairing code printed once
  to standard error. The stages are:
  - the offer, the accept and the DERO lock;
  - the Zcash funding and the maker's claim, each with its confirmations against C_zec;
  - the DERO Claim;
  - when it comes to that, the refunds.

  Each carries its txid and its deadline: a DERO height, or a Zcash height with the companion's estimate of when (none
  on regtest). The answer never carries the secret, the taker's copy of it, or a key (`tools/zecswap/statusv1.go`,
  `statusv1_test.go`). Once the swap ends its outcome stays served for ten minutes, or until the program is stopped.

## 8. The screen

- **Market list** — the board, read from `board.bas`: for each open offer the asset, the amount, the price in
  ZEC per unit, the maker's completed and abandoned counts, and time left. An accepted offer past its `ad_<id>` with no
  lock under its `h_<id>` in `swap.bas` is shown dead from that block. ZEC first: the tab reads *"Buy with
  ZEC"* and *"Sell for ZEC"*, with DERO shown only where it is the asset offered.
- **The privacy statement** (§1) on the accept screen and the post screen, above the fields, every time.
- **Accept** — the taker enters a transparent refund address (the app derives the key hash, refuses shielded
  addresses with the reason), sees `cltv` computed by policy from a Zcash height the app reads from the
  companion or a public block explorer the user names (and is told is trusted for that number), and the fees:
  the `Accept` gas in DERO, the Zcash funding fee (ZIP-317: a marginal fee per logical action, at least 10,000
  zatoshi), the maker's 0.5 % swap fee (paid out of the ZEC he receives, not out of what she funds), the maker's
  expected claim time, and the taker's own worst case ("if the maker never claims, your
  ZEC is back after 2 hours; you pay two Zcash fees").
- **Post** — the maker enters the asset and amounts and the transparent address to be paid at, sees the
  exposure (eight hours per accepted offer) and the gas for `Post` and `Lock`, and the app generates `s` — in
  the wallet's memory for the session and written nowhere by the browser: the maker's companion is the durable
  holder, and the screen says that a lost secret means a refund after eight hours, not a loss.
- **Swap page** — `#zswap/<board id>`: the state of both legs as the app can read them (the DERO leg live; the
  Zcash leg from the companion when connected, otherwise "not visible from here"), the next action for the
  visitor's role — once `s` is public and the lock names a taker, *Claim for the taker* is an action for any
  visitor, and it pays her, not them — and every deadline (`ad_<id>`, `cltv`, `T_dero`) as a wall-clock time with
  the block it comes from.
- Nothing in the browser holds a key. The maker's secret is the one new thing the browser generates, and it is
  handed to the companion, not stored.

## 9. Adversarial review

| # | attack or failure | what happens | who bears it |
|---|---|---|---|
| Z1 | Taker accepts, never locks ZEC | maker refunds at `T_dero` | maker: 8 h of capital + two fees |
| Z2 | Maker never locks after accept | the accept is dead at the board's `ad_<id>` (the accept height + `aw` = 200); the taker's companion funds nothing against a later lock, and from then the maker may cancel the row | taker: one fee, an hour |
| Z3 | Taker locks the wrong amount or a script with a wrong `H`, key or `cltv` | the maker's companion refuses to claim; both refund | both: time |
| Z4 | Maker claims ZEC at the last block before `cltv` | a race, not a safe exit: from `cltv` the taker's refund can spend the same output, and the maker's claim has already put `s` in a public mempool. If her refund is mined first she holds her ZEC and `s`, and claims the DERO as well | the maker, who loses both legs; the companion must claim well before `cltv` (`docs/ATOMIC_SWAP_STUDY.md` §2.2) |
| Z5 | Zcash reorg removes the funding after the maker's claim | the claim is removed with it; `s` is already public; the taker refunds ZEC after `cltv` and claims DERO with `s` | maker: both assets gone. Prevented by `C_zec` = 10 before claiming |
| Z6 | DERO reorg removes the maker's `Lock` after the taker funded ZEC | the taker's ZEC refunds after `cltv`; the maker cannot claim (no `s` was revealed) | taker: 2 h. Prevented by the 8-block wait |
| Z7 | Maker posts one offer, two takers try to accept | the board takes the first; the second `Accept` fails on state | nobody |
| Z8 | Same `H` reused across offers | the second taker to fund could be claimed with the first's `s`; the companion generates `s` per offer and refuses a reused `H` (the board can index `h_<id>`) | prevented |
| Z9 | The taker funds with a transaction whose input is not final (`nSequence` max) or with `nLockTime` below `cltv` | the refund branch's `CHECKLOCKTIMEVERIFY` fails for a final input; the PoC's fifth and sixth cases | taker cannot refund early — which is the design |
| Z10 | Fee attack: the maker's claim cannot pay a fee because the funding is dust-sized | the maker checks `wantAmount` against the fee floor before posting; the companion refuses to fund below it | prevented |
| Z11 | Front-running `Claim` on DERO: someone sees `s` on Zcash and sends the escrow's Claim first | v2 pays the taker `Lock` named whoever sends the call (Claim line 29), and `st_<id>` stops a second payout; the front-runner pays a fee to complete her leg | nobody; it is how a relayer helps (Z19) |
| Z12 | Front-running the maker's Zcash claim | the script pays only the maker's key hash; knowing `s` is not enough | prevented |
| Z13 | Linkability | §1: the pairs of addresses are joined by `H` and `s` on two public chains | everyone in the swap, forever; disclosed |
| Z14 | The taker's DERO node lies about the lock | she funds the script against a `swap.bas` lock that does not exist. A node that is merely wrong costs her a refund after `cltv`. A node that colludes with the maker lets him claim the ZEC with `s` before `cltv`, and her DERO Claim finds no lock | taker: 2 h if the node is merely wrong; **the whole ZEC leg** if it is the maker's accomplice (`docs/CROSS_CHAIN_TRUST_MODEL.md` §5). The node is a trusted party for that read, and the app names it |
| Z15 | The maker's Zcash node lies about the funding | it shows a funding that does not exist, at full depth; the maker claims it, broadcasting the claim through that node, which reveals `s` to it. A node that is merely wrong costs a failed claim. A node that colludes with the taker hands her `s`, and `swap.bas` v2 Claim pays the stored taker whoever sends it: she takes the DERO leg without ever having funded. Or the node hides a real funding, and the taker refunds | maker: time if the node is merely wrong; **the DERO-side asset** if it is the taker's accomplice (`docs/CROSS_CHAIN_TRUST_MODEL.md` §5). The node is a trusted party for that read, and the companion names it (`tools/zecswap/node.go:23-29`) |
| Z16 | Liveness: the maker's companion is off when the funding confirms | the taker refunds after `cltv`; the maker's escrow refunds after `T_dero` | both: time. The companion's `status` says how long it may sleep |
| Z17 | The taker cannot broadcast a refund at `cltv` — offline, a crashed watcher, a signature under a branch id the chain has left behind | the claim branch of the redeem script carries no timelock, so it never expires. After `T_dero` the maker has refunded the DERO and can still spend the ZEC with `s`, at any later moment | taker: the whole ZEC leg. The margin is the six hours between `cltv` (2 h) and `T_dero` (8 h); the refund must be attempted at `cltv` and retried until it confirms |
| Z18 | Any address accepts an offer it will never fund (ADV-PRICE-07) | board v3: the row is held until `ad_<id>`, then the maker cancels and posts again under a new `H`; v2 held it until the offer's expiry, up to a million blocks | maker: an hour per griefing `Accept`, which costs the griefer one fee; nobody's coins. A bond that would cost the griefer more is an operator decision (D-7, BL-45) |
| Z19 | The maker claims ZEC late, revealing `s` shortly before `T_dero`, while the taker is away | swap v2: anyone who reads `s` — the maker's companion, her second device, a relayer — sends her Claim before `T_dero`, and it pays only her. v1 needed the taker herself; after `T_dero` the maker refunded and held both | taker: nothing, if anyone sends the Claim before `T_dero`. Z17 is the case v2 does not reach |

| Z20 | A maker runs a modified client that leaves the swap fee out of his claim | the claim is valid: the script pays his key, and no contract or consensus rule checks the fee | DMZ's treasury loses the fee; the fee is enforced by the companion, not by a contract (§7) |

Z14 and Z15 cost only time when the lying source is not the counterparty's accomplice, and a whole leg when it is.
What reduces that, strongest first: each party's own nodes, which is the companions' default; reads from several
independent nodes that must all agree before the step each side's safety rests on, which `zecswap` does (MT-C10;
`tools/zecswap/quorum.go`) and which turn "one liar" into "every source colludes" without removing
the trust; and checking Equihash and the Merkle path in the client before revealing `s` or funding, which is not
built. A single remote node is disclosed as able to cause a loss that nothing in the protocol can detect.

## 10. Trust statement

- **Trustless in what it settles**: no party can end with both assets while the other ends with neither,
  given the policy margins of §5, the confirmation waits of Z5/Z6, and each party's honest view of both chains
  (Z14, Z15). That is the whole of the claim.
- **Not costless**: the free option on the maker (§6), the timelock on both sides' capital, and two chains'
  fees.
- **Not private**: §1, in full, on the Zcash side; ring size 2 on the DERO side for every call that must know
  its signer.
- **Trusted parties, named**: the DERO node each party reads and the Zcash node (`zebrad` or `zcashd`) each party
  reads, for what the two chains contain. A node that colludes with the counterparty costs its reader a whole leg,
  not time (Z14, Z15; `docs/CROSS_CHAIN_TRUST_MODEL.md` §5). Nothing else. No DMZ server, signer, relay or operator is in the
  path, and nothing needs one to finish: every state has a timelocked exit.
- **The swap fee is outside that settlement**: the companion adds it to the maker's claim, nothing enforces it (§7,
  Z20), and its one treasury address makes every DMZ Zcash swap listable (§1).

## 11. What is built, what is not

Built and verified: `swap.bas` and `board.bas` (Phase 1, `tests/run.sh -run 'Swap|Board'`); the Zcash script under
Zcash's consensus interpreter (`poc/zec-t-htlc`, all seven cases); the DERO leg on the simulator (`tools/simswap`:
lock, a late claim refused by the chain, refund; `research/crosschain/poc/simswap-run.log`, and this cycle's rerun
in the HISTORY entry).

Built 2026-09-10: `board.bas` v2 (§4) with its DVM test and the driver's board scenario; the script layer of
`tools/zecswap` (§7) — the redeem script, its P2SH address on both networks, transparent-address parsing that
refuses shielded forms with the reason, the two spend layouts — tested byte for byte against the PoC's vector.
Built 2026-09-11: `swap.bas` v2 and `board.bas` v3 (§4) with their DVM tests (`tests/swap_v2_test.go`,
`tests/swap_gaps_test.go`, `tests/board_v3_test.go`, and the updated swap, board, reentry, market and conservation
tests), and the driver's scenarios run on the simulator: a Claim sent by a third wallet paying the taker, a lock
naming no taker and one narrower than `wmin` refused, a late claim refused, a refund to an empty escrow, and a board
offer cancelled at its acceptance deadline, before its expiry.

Built 2026-09-11 and 2026-09-12, the rest of `tools/zecswap`:
- the node client, the multi-node quorum, the watcher and the ZIP 317 fee;
- the funding, claim and pre-signed refund builders, in v5 and v6;
- maker and taker state machines, with durable state and a crash fuzz;
- the DERO client, the code pins, and the command line;
- the reorganisation handling and the `lost` phase (§7);
- the header gate behind `-verify-headers` (§7);
- contract ids read from `deploy/networks.json`;
- the 0.5 % swap fee in the maker's claim, its treasury read from `deploy/networks.json` (2026-09-13, §7).

It is verified:
- by unit tests against fake nodes, plus real zebrad v6 transactions and real Mainnet headers
  (`tools/zecspv/testdata`). These include fuzzing of every node-response parser, a test that no secret reaches the
  output, and the crash fuzz;
- by three swaps on zebrad 6.3.0 regtest with the DERO simulator, every txid in `research/regtest/zecswap-e2e.log`:
  - a v5 claim, where the maker's companion sent the away taker's DERO Claim;
  - a v6 claim, where the taker's own watcher read the secret;
  - a refund refused before `cltv` and mined after it, broadcast by a third party holding only the pre-signed file;
- by three reorganisations on the regtest zebrad, made with `invalidateblock`, every txid in
  `research/regtest/zecswap-reorg.log`:
  - a funding removed below `C_zec`: the maker reported it and never revealed;
  - a reorganisation deeper than `C_zec` after both sides settled, with the taker's coin spent again: the maker ended
    `lost`, the Z5 loss;
  - the same reorganisation without the second spend: the maker's claim was mined again and he ended `swapped`;
- by the `zecswap` command line itself: the built binary, run as separate maker, taker and status processes against
  zebrad regtest and the DERO simulator, every txid in `research/regtest/zecswap-cli.log` (`TestRegtestCLI`):
  - a whole swap that ended `swapped` on both sides. It ran with `-verify-headers`, which was skipped on regtest and
    said so;
  - a refund. The maker's process was stopped after it locked, and the taker's process refunded at `cltv`. The maker's
    process, started again, refunded the DERO after `T_dero`;
- by the swap fee on the same nodes, through the command line, every txid in `research/regtest/zecswap-fee.log`
  (`TestRegtestFee`, 2026-09-13, DERO wallets 30018–30020):
  - a whole swap whose claim zebrad mined with two outputs: 189,000 zatoshi to the maker, and exactly 1,000 zatoshi
    (floor(200,000 × 50 / 10,000)) to the regtest treasury. It was read back through `getrawtransaction`, `gettxout`
    and `getaddressutxos`, and the treasury went from 0 to 1,000 zatoshi;
  - a refund with one output, 190,000 zatoshi to the taker, and nothing to the treasury;
- by the header gate's reads, in `research/regtest/zecswap-headers.log`:
  - on regtest: the shapes and the Merkle branches; the gate itself is skipped there;
  - through the gate against the Zcash testnet node, on historical blocks, as plumbing only: Equihash, the difficulty
    rule with 200 headers below, and the Merkle branch hold, and the shipped tip-age rule refuses the still-syncing
    node's tip.

Not built, or not run, in this order, each its own cycle and each verified before the next:
1. the Zcash testnet run of a swap (BL-42), and `-verify-headers` on by default after it. The driver and the
   procedure are in §11.1, and the run waits for the testnet node to reach the tip;
2. zcashd, which is past end of support and never run here;
3. the screens (§8);
4. the measurement behind §5 (BL-30).

Mainnet, never from this repository.

### 11.1 The Zcash testnet run (MT-C15)

**The driver.** `TestTestnetSwap` (`tools/zecswap/testnet_swap_test.go`) runs the command line the way
`TestRegtestCLI` does, as separate maker, taker and `status` processes. The differences:
- It runs with `-network testnet`, through the harness's rpcgate (`http://127.0.0.1:18232`).
- It runs under the policy the command line ships for testnet (§5): `C_zec` 10, `cltv` 96 ± 32 Zcash blocks, and
  `T_dero` 1,600 DERO blocks after the height the lock is built at (the policy's window, wider than `wmin` + 11). The
  taker funds only while 1,200 DERO blocks are left, and `swap.bas` is installed with `wmin` 1,200.
- Its waits follow the chains. The taker's process gets 10 h, since everything it waits for is on Zcash or a few
  DERO blocks. The maker's process may have to wait for `T_dero`, so its wait lasts until the DERO chain reaches the
  maker's `T_dero`, however slow that chain is, and then 10 h more. If the DERO chain stops moving for 30 minutes,
  the wait stops following it and the 10 h run from there. `ZECSWAP_TESTNET_WAIT` (a Go duration, such as `14h`)
  replaces the 10 h.
- It runs one whole swap and one refund, both with `-verify-headers`. On testnet the gate checks the plumbing only:
  the minimum-difficulty rule makes forged headers nearly free.
- It mines nothing. The taker funds from a key file the operator has filled, and the maker's claim pays another.

It refuses to start unless:
- the node is the testnet (genesis `05a60a92…2c38`);
- its tip passes the shipped header check, so it is synced and recent;
- the taker's address holds enough confirmed TAZ.

Every txid goes to `research/regtest/zecswap-testnet.log`. The driver is gated by `ZECSWAP_TESTNET_SWAP=1`.

**The DERO side is the simulator**, with a board and an escrow the driver installs from wallet 30013. MT-C15 tests the
Zcash leg on a public network. The DERO leg's calls and rules have already run on the simulator. A private DERO
testnet would add two things:
- persistence: the simulator deletes its chain when it restarts, and this run lasts about seven hours;
- DERO's own 18 s block time. The simulator makes a block about every 14 s, so `T_dero` passes in about 6.2 h
  instead of 8 h. The policy's wall-clock checks assume 18 s, which overstates the DERO time left by about a quarter.

Neither changes what MT-C15 tests, so the simulator is enough, provided nothing restarts it during the run. DERO's
public testnet seed is not reachable (`tools/regtest/README.md`).

A private DERO testnet (W-01: derod `--testnet` on 127.0.0.1:40402, with its own miner and wallets) has both
properties the simulator lacks. It is reserved for MT-F05's week-long exercise, which starts over once the new factory
and `htlc.bas` are deployed on it, so the driver doesn't use it by default. To run the DERO leg there instead, set
`ZECSWAP_TESTNET_DERO_DAEMON` and three wallets of the driver's own, funded from that chain:
`ZECSWAP_TESTNET_DERO_MAKER`, `ZECSWAP_TESTNET_DERO_TAKER` and `ZECSWAP_TESTNET_DERO_INSTALLER`. The driver installs its
own board and escrow either way, and refuses a DERO daemon that is not on a testnet.

**Testnet ZEC (TAZ).** zecswap funds from a transparent key (`tm…`). Each source below is described from its own
page, as read on 2026-09-12. Nothing has been claimed and nothing sent.

| Source | Pays | Limit | To a `tm…` address? |
|---|---|---|---|
| [zcashfaucet.jinolabs.xyz](https://zcashfaucet.jinolabs.xyz/) (Jino Labs, "not an official Zcash service"; source `github.com/jinolabs-xyz/zcash-faucet`) | 0.1 TAZ | once per address per 24 h | no: "the drip is shielded" |
| [fauzec.com](https://fauzec.com/) (mines its own TAZ through a Stratum bridge to a Zebra node) | 1 TAZ | per address per 24 h, plus an IP/24 limit and a Cloudflare Turnstile captcha | no: unified or Sapling addresses only |
| `faucet.zecpages.com`, the faucet the Zcash documentation's [Testnet Guide](https://zcash.readthedocs.io/en/latest/rtd_pages/testnet_guide.html) names | — | — | connection refused on 2026-09-12 |
| [zcash/zfaucet](https://github.com/zcash/zfaucet), the Zcash project's faucet app | — | — | yes, by its README, which names no running instance |
| Asking on the Zcash Discord, as the Testnet Guide suggests | a person's choice | — | yes; takes hours to days |

The live faucets pay shielded addresses only. So one shielded wallet must sit between a faucet and the taker's key: it
receives the drip and sends transparent TAZ to the `tm…` address. A faucet takes minutes; the wallet can spend the drip
10 blocks after it is mined (about 13 min); then one shielded-to-transparent send (about one block).

**The shielded wallet: Zallet, on our own zebrad.** It is an operator's tool for test coins, never a zecswap or DMZ
dependency: nothing in the repository runs it, links it or reads its files, its config names testnet and nothing else,
and it is never pointed at mainnet. The rule for a new dependency asks what it gives that a smaller primitive cannot.
zfund signs transparent spends only. To take a faucet's drip it would need a Sapling or Orchard key, a scan that
trial-decrypts every block's shielded outputs from the wallet's birthday, the note commitment trees with a witness for
each note, and a shielded spend with its proof. That is a wallet, and writing one to move test coins once would be the
bigger dependency. The candidates, each from its own pages, read on 2026-09-12:

| Candidate | Reads the chain from | Verdict |
|---|---|---|
| [Zallet](https://github.com/zcash/zallet) 0.1.0-beta.3, `zaino` backend (`zallet-zaino`) | our zebrad's JSON-RPC | **chosen** |
| Zallet, default `zebra` backend | zebrad's state database, read directly | needs a zebrad "built with the non-default `indexer` feature" and an `indexer_listen_addr`; ours is the stock release, and it is not rebuilt or restarted for this |
| zingo-cli ([zingolib](https://github.com/zingolabs/zingolib)) | a lightwalletd | the harness runs none, so a public testnet lightwalletd would serve the wallet its blocks and see its queries; v6.0.0 ships no binaries |
| [zcash-devtool](https://github.com/zcash/zcash-devtool) | a lightwalletd | the same third party; "No binary artifacts are provided"; "DO NOT USE THIS IN PRODUCTION" |
| zcashd | its own node | past its end-of-support halt (2026-07-18), and does not follow NU6.3 (`tools/regtest/PINS.md`), which testnet activated at 4,134,000; a second full sync besides |

Why Zallet's `zaino` backend fits:
- Its book says it "fetches chain data over JSON-RPC" and "does not require the `zebrad` `indexer` feature". It runs
  the Zaino indexer ephemerally: "no persistent finalised-state database is actually opened … finalised reads are
  served from the backing validator" (`backends/zaino/src/chain.rs` at the tag). So no second index of the chain is
  built, and the wallet scans only from its account's birthday, near the tip.
- It reads blocks, tree states and the mempool, which rpcgate does not serve. So it uses zebrad's internal port 18242
  with zebrad's own cookie, from inside WSL. rpcgate is unchanged, and zecswap still goes through it.
- It is the Zcash developers' replacement for the zcashd wallet, with the zcashd RPCs this needs: `z_getnewaccount`,
  `z_getaddressforaccount`, `z_sendmany`.

What it trusts:
- our zebrad, which zecswap already trusts, for blocks and for broadcasting. No party is added to the chain path;
- the release, through two checks that do not depend on each other:
  - the ZODL release key signs the tarball: `0338 34DD 49DE CF9D BB99 34BC 6C93 CA8E 58E2 6AB1`, from
    `apt.z.cash/zcash.asc`, the key Zallet's book names. keys.openpgp.org serves a key under that fingerprint, but
    with no verified user ID, so it is a weak second source;
  - the SLSA provenance names the tarball's sha256 and the tag's commit. It is signed through sigstore by the
    release workflow's GitHub identity, and holds without the PGP key;
- the faucet, which sees the address it pays.

Its costs:
- It is beta: "Breaking changes may occur at any time, requiring you to delete and recreate your Zallet wallet". That
  is acceptable for a wallet holding at most 1 TAZ for a day.
- The send to a `tm…` address is public on chain by design. Zallet refuses it unless the call names a privacy policy
  that allows it.

The pin is in `tools/regtest/pins.env` and `PINS.md`, and the procedure in `tools/regtest/README.md`. The binaries run
from WSL's own filesystem, and the wallet's data and keys live in `$HOME/.dmz-regtest/zcash-testnet-wallet`, outside
the repository.

**Mining it ourselves is not a practical funding path.**
- zebrad 6.3.0 mines only through `getblocktemplate`/`submitblock` with an external Equihash miner, or with the
  experimental internal miner. That needs a zebrad built with `--features internal-miner`, and Zebra documents it for
  Regtest ([Regtest with Zebra](https://zebra.zfnd.org/user/regtest.html)). The harness's rpcgate refuses
  `submitblock` (measured), so a miner would need zebrad's internal RPC.
- Testnet's minimum-difficulty rule: from height 299,188, a block may carry difficulty PoWLimit only when its time is
  more than 6 × the target spacing after the previous block, which is 7.5 min
  ([Zebra RFC 0006](https://zebra.zfnd.org/dev/rfcs/0006-contextual-difficulty.html); `tools/zecspv/params.go`). Since
  [Zebra 6.2.1](https://github.com/ZcashFoundation/zebra/releases/tag/v6.2.1), `getblocktemplate` switches to a
  minimum-difficulty template only once that threshold is reached, not 150 s early. A CPU miner therefore wins an
  easy block only when the network has gone 7.5 minutes without one; otherwise it mines at the network's difficulty.
  Fauzec's operator reports two blocks from a solo miner in its first month: "mining is a lottery ticket, not a budget"
  ([Zcash forum, 2026-08-01](https://forum.zcashcommunity.com/t/another-faucet-post-i-know-this-one-mines-its-own-taz/56868)).
- A coinbase can be spent only 100 blocks later (about 2 h), and a transaction spending a transparent coinbase output
  may have no transparent outputs ([ZIP 213](https://zips.z.cash/zip-0213)). The coins would still have to pass
  through a shielded wallet.

**How much.** The run is two swaps of 100,000 zatoshi, plus ZIP 317 fees. The driver requires 280,000 zatoshi confirmed
at the taker's address; 0.01 TAZ leaves room for a retry.

**The procedure.**
1. Bring the Zcash testnet node to the tip. `tools/regtest/zcash-testnet-up.sh` reports its height against the
   target, and the driver checks it again.
2. Keep the DERO simulator running, and don't restart it for about seven hours. Wallets 30011–30013 need at least
   1 DERO each: the maker locks 0.5 DERO per swap. Alternatively, with the four `ZECSWAP_TESTNET_DERO_*` variables,
   use the private DERO testnet and three wallets of the driver's own there.
3. Make two keys in a directory outside the repository that both WSL and Windows can reach, with zfund
   (`tools/regtest/README.md`):
   ```sh
   wsl -d Ubuntu-24.04 --exec <repo-in-wsl>/vendor/regtest/bin/zfund keygen <keys-dir>/maker.key   # prints "address": "tm…"
   wsl -d Ubuntu-24.04 --exec <repo-in-wsl>/vendor/regtest/bin/zfund keygen <keys-dir>/taker.key
   ```
   Send at least 0.01 TAZ, transparent, to the taker's `tm…` address, and wait for one confirmation.
4. Run the driver:
   ```sh
   cd tools
   ZECSWAP_TESTNET_SWAP=1 ZECSWAP_TESTNET_MAKER_KEY=<keys-dir>/maker.key ZECSWAP_TESTNET_TAKER_KEY=<keys-dir>/taker.key \
     PATH="$HOME/sdk/go1.26.8/bin:$PATH" GOTOOLCHAIN=local go test -count=1 -run '^TestTestnetSwap$' -timeout 12h -v ./zecswap
   ```
   `-timeout 12h` covers the simulator, where `T_dero` passes about 6 h after the lock. On the private DERO testnet,
   `T_dero` alone takes 22–27 h at that chain's pace, so give `-timeout 40h`. Go's own timeout is the one bound the
   driver cannot stretch to follow the chain.

**Wall clock**, at testnet's 75 s blocks and the simulator's ~14 s:

| Step | Takes |
|---|---|
| Preflight, build, and install the board and escrow (two DERO transactions) | ~2 min |
| Whole swap: post, accept, lock, and the lock 8 DERO blocks deep | ~4 min |
| Whole swap: funding mined, then 10 deep (`C_zec`) | ~13–15 min |
| Whole swap: claim mined, and the taker's DERO Claim settled | ~3–5 min |
| Whole swap: claim 10 deep; both sides end swapped | ~12–13 min |
| **Whole swap, total** | **~35–40 min** (Zcash block times vary; an unlucky run doubles it) |
| Refund: post, accept, lock, lock deep, funding | ~6 min |
| Refund: `cltv` (96 blocks after the accept), then the taker's refund 10 deep; she ends refunded | ~2 h 15 min |
| Refund: `T_dero` (1,600 DERO blocks after the lock is built), then the maker's Refund; he ends refunded | ~6 h 15 min after the lock (22–27 h on the private DERO testnet) |
| **Total** | **about 7 h** |

**Done, for MT-C15:**
- `TestTestnetSwap` passes: the whole swap ends swapped on both sides, and the refund ends refunded on both.
- `research/regtest/zecswap-testnet.log` holds every txid:
  - on Zcash testnet: the two fundings, the claim and the refund;
  - on the simulator: the DERO Post, Accept, Lock, Claim and Refund;
  - and the header gate's lines on testnet (plumbing only).
- Each Zcash txid is checked on a public testnet block explorer, independently of the node the swap used.
- Hygiene is green.

After that, `-verify-headers` can be made the default (MT-C14's last step), with the testnet caveat unchanged.

## 12. Sources

Zcash protocol specification (transparent transactions, script), BIP 65 (`CHECKLOCKTIMEVERIFY`), ZIP-317
(fees), `zec-t-htlc/run.log` (this repo), `swap.bas`/`board.bas` (this repo). Recorded with what was read in
`research/crosschain/SOURCES.md`.
