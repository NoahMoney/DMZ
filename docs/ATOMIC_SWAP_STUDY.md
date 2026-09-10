# Atomic-swap upgrades for DMZ — a decision study

*2026-09-11. Branch `wip/2026-09-09-swap-nfa`, code as at `f680069` (HEAD `d5d234e` adds documentation only). The
operator asked: "run some considerations if we should use some 'atomic swap' type upgrades to enhance security
without sacrificing too much speed". Just before that they asked how safe the federation lock is and whether it
can run with no human, the operator included, having access to it. This note answers both, path by path, for an XMR or
ZEC holder who arrives, trades against tokens and NFAs, and leaves. It changes no code. It is built from five
design passes (security, speed, the holder's journey, an adversary, a buildability review), three judgements of
those passes, and a fresh reading of every line of code it cites. Where the passes disagreed it decides, and says
why. Every number carries a `file:line` in this repository or a URL. A number without a source is marked
**assumed**. Every latency is wall-clock time at the chains' target block rates, and §1 says where those targets
are promises the chains do not keep.*

Companion documents: `docs/XMR_SWAP.md` and `docs/XMR_SWAP_BUILD.md` (the Monero swap), `docs/ZEC_SWAP.md` (the
transparent-Zcash market), `contracts/README.md` (the vault), `docs/audit/FINDINGS_2026-09-10.md` (the audit),
`docs/BACKLOG.md`.

---

## 0. The answer in one screen

**Yes, adopt them, and they cost little speed.** Every recommended upgrade is free on the happy path, or shortens it,
except three:
- the stipend adds one DERO inclusion plus a spendability wait (≈18-60 s, not measured) to a first trade, after a
  one-time registration of a couple of minutes (§2.0.1);
- U3 adds 144 s before the taker's companion may stop;
- U7 adds 20 min per tier of 10 confirmations, on large trades only.

What sets the speed of a first trade is the foreign chain. Twenty Monero confirmations are
2,400 of the Monero swap's ≈2,858 seconds (§1), and every path pays the equivalent, the federation included. No
construction on DERO can shorten that. After the first trade, every path trades again in one DERO transaction.
So the operator is not choosing between speed and security. The choice is **what a holder carries between
trades, and who can take it**.

**Where to adopt them:**

1. **The XMR↔DERO swap stays the trustless track.** No contract change is required for U1-U9. LIV-07 is different:
   a lock past t2 is frozen for ever if the taker never Punishes, and closing that needs an `htlc.bas` v2 before any
   mainnet SCID (U13, operator decision 17). The companion is extended so that the
   DERO leg can be a token, an NFA unit or wXMR, not only DERO. The contract already allows it: `htlc.bas:37-43` for
   the lock, and `:87, :114, :133` for the three exits. Five fixes that cost nothing on the happy path close holes
   the designs found:
   - a deadline on the gate;
   - the XMR-06 re-send margin;
   - the taker waiting for her Claim to be stable before she stops watching;
   - pinning the contract's code;
   - lower-case hashes.

   A wider refund window protects the maker. Twenty confirmations stays the default. The proof needs an external
   audit before mainnet. Finally, a proof on Monero stagenet (BL-11).
2. **A gas stipend from the maker** becomes part of the protocol on every swap path. Every DERO transaction pays its
   fee from a DERO payload (`vendor/derohe/blockchain/transaction_verify.go:265-276`). So a holder who arrives with
   only XMR or ZEC cannot sign the Claim that pays them. Today the only way round that is an unnamed seller of DERO.
3. **The ZEC market** gets its missing client and three more changes, each of which removes a way for the taker to
   lose the whole ZEC leg by being absent:
   - the client itself: node, watcher and broadcast;
   - a pre-signed refund that anyone may broadcast (ZIP 203, `nExpiryHeight = 0`);
   - `swap.bas` v2, whose Claim pays the stored taker whoever sends it;
   - `board.bas` v3 with an acceptance deadline.
4. **The federation is not deployed.** It is custody by construction and can never be made trustless on this VM (§3). If
   it ever ships, it is an opt-in, capped product for holding an XMR- or ZEC-denominated balance and for shielded ZEC.
   It is never the default route and never called trustless. Two conditions come first:
   - signers other than the operator must be named;
   - the operator must decide who bears the loss in LIV-03.
5. **Rejected:** adaptor signatures, bonds that cannot be attributed, watchtowers and relayed claims **on the Monero
   swap** (a holder of s_b or s_a can steal), confirmation checks inside a contract, and cutting Monero confirmations
   for speed (§6). On the ZEC market, a third-party broadcast of the pre-signed refund (Z-B) and a Claim anyone may send
   (Z-C) are adopted, because neither can pay anyone but the taker.
6. **No cross-chain swap meets LIVENESS BY CONSTRUCTION as the ruling is written.** Three states on the swap track
   still need a party, or a machine or delegate of theirs, online at a stated height:
   - the maker must Refund inside [t1, t2), or the taker may Punish;
   - on the ZEC market, someone must broadcast the taker's refund between cltv and the maker's spend (Z17), even with
     Z-B;
   - on the Monero swap, the taker must Punish, or the lock is frozen for ever (LIV-07), until an `htlc.bas` v2 exists.

   For the first two, no construction on this VM removes the need: a Refund holder of s_b can steal (§6), and a
   transparent Zcash script has no "spend before" condition, only "spend after" (BIP 65). This study does not
   reinterpret the ruling on the operator's behalf. It is put to them as operator decision 15.

**Path by path, today against recommended.** Times are at target block rates. "Final" means included and then 8 DERO
blocks deep (`vendor/derohe/config/config.go:77`). "Discovery" is the time to find a counterparty. No mechanism for
it exists on any swap path (BL-12, `docs/BACKLOG.md:64`), so it is excluded everywhere and unbounded. First-trade
figures exclude Path 0 (registration and the stipend) unless the cell says so.

**Trusted on every path, and not repeated in each cell:**
- **the TELA INDEX publisher key.** It can republish the whole app for ever, until it is destroyed or the INDEX is
  reinstalled above ring 2 (SEC-K-03, `docs/audit/FINDINGS_2026-09-10.md:408`). The app is the page that fills in
  the SCIDs attack E warns about (§2.1).
- **the companion binary's publisher and signer** (decision 14).
- **the operator of any remote node a holder reads from** instead of their own. A remote Monero, Zcash or DERO node can
  cause a total loss, and nothing in the protocol can detect it (R18, `docs/XMR_SWAP.md:402-407`; Z14 and Z15,
  `docs/ZEC_SWAP.md:270-271`). The "What the holder runs" column assumes full nodes; a newcomer who arrives with a
  light wallet will not run one. Operator decision 16.

| Path | | Trusted parties | First trade | Next trade | Exit to native coin | Capital locked up | What the holder runs |
|---|---|---|---|---|---|---|---|
| **0. First DERO for gas** | today | an unnamed seller of DERO, in practice an exchange | only after buying DERO off-platform | — | — | — | — |
| | recommended | nobody: a gift from a counterparty who is already committed (§2.0.1) | +≈1-3 min on a first trade (registration, one inclusion, spendability; not measured) | — | — | the maker's stipend: a floor of 0.12 DERO plus 4 × the Claim estimate; lost to every walker | — |
| **1. XMR ↔ DERO-side asset** (htlc.bas) | today | own nodes and companion; the unaudited proof; no Monero reorg deeper than 20 after the gate; **unstated:** DERO not censored over the claim margin | ≈48 min to final DERO; the token is one pool trade later | 18 s, ≈2.7 min final (if the holder keeps DERO or tokens) | ≈70 min plus discovery, as maker, after selling the token for DERO | maker: to t1 ≈2.1 h default (≤ 6 h, the taker's T1Max), or t2 ≈4.1 h default (≤ 12 h); taker's XMR: no exit of her own until t1 | xmrswap companion, proof-xmr CLI, monero-wallet-rpc and node, DERO wallet and daemon |
| | recommended | the same, with the audited proof; DERO censorship over G blocks and maker liveness in [t1, t2) **named**; the taker's Punish until U13 (LIV-07) | ≈48 min straight into the token (+ Path 0 on a first trade) | same | ≈48 min + the joint wallet's scan (unmeasured), plus discovery, straight from the token, counted at the stable Claim | maker: to t1 ≈2.1 h default, ≤ 6 h; t2 ≈8.1 h with U5 at the default t1, ≤ 12 h | the same (a signed release is BL-14) |
| **2. transparent ZEC ↔ DERO-side asset** (board + swap) | today | own nodes; margins nobody enforces; the taker's own liveness at cltv, on pain of losing the whole ZEC leg (Z17) | ≈21 min; **cannot run** (no watcher) | same | ≈18 min plus discovery, as maker | maker: to T_dero = 8 h; taker's ZEC: to cltv ≈2 h | zecswap companion, Zcash node or lightwalletd, DERO wallet |
| | recommended | own nodes; margins in code; someone — the taker, her second device or a person she trusts with the hex — broadcasts one transaction between cltv and the maker's spend (decision 15) | ≈20-21 min | same | ≈18 min plus discovery | unchanged | the same |
| **3. federation** (vault + vaultsigner) | today | **k of n signers** (can mint against nothing or take the reserve); any one signer can strand a withdrawal (LIV-03); **no backend exists** | ≈26 min XMR / ≈18 min ZEC to minted units | 18 s, ≈2.7 min final | ≈25 min XMR plus an **unbuilt** signing round plus a wdelay that has no deployed value | the whole holding, in custody, indefinitely | DERO wallet and the app |
| | recommended | **not deployed.** If ever deployed: the same custody, capped, opt-in, with the LIV-03 rule chosen and written down | unchanged | unchanged | unchanged; the swap exit from Path 1 as a liveness hedge | the same | the same |
| **4. tokens and NFAs** (pool, nfa) | today | the DERO contracts; the factory owner key, whose SetTreasury delists every market (LIV-08, SEC-K-02); a wXMR quote adds Path 3; an NFA's creator can rewrite its metadata forever; the open NFA defects NFA-1 / LIV-02 (a stranded bid is a standing call on the asset) and LIV-01 / DVM-1 (an unregistered payee destroys the seller's payout) | via Path 1/2 plus one pool trade | 18 s, ≈2.7 min final | via Path 1/2 | the pool's reserves only | DERO wallet and the app |
| | recommended | the same, disclosed on screen; the companion refuses an NFA with a stranded bid and shows the unit's identity read from the chain (§2.4) | XMR or ZEC straight into the token or NFA unit (≈48 / ≈21 min) | same | straight from the token or NFA unit | the seller's unit, per swap | the same |

Sources for the table:
- **Path 1:** §2.1.
- **Path 2:** §2.2.
- **Path 3:** §2.3 and §3.
- **Pool trade:** `contracts/pool.bas:136-191`.
- **Stipend floor:** Punish at a fixed 3,000 atomic units doubled at most twice, 3,000 × 2² = 12,000
  (`tools/xmrswap/dero.go:122, 761-764, 804`), plus a Claim that is mined and refused, at its estimate (gas + 200,
  `dero.go:774`) × 4 (§2.0.1). 1 DERO = 100,000 atomic units, from `FEE_PER_KB = 20 // .00020 dero per kb`
  (`config.go:47`).
- **The ceilings:** the taker's companion accepts t1 − now ≤ 1,200 and t2 − t1 ≤ 1,200 (`tools/xmrswap/swap.go:44-45`,
  checked at `swap.go:1068-1071`).
- **Registration and spendability:** §2.0.1.

---

## 1. The speed floor

A holder receiving a coin on one chain for a coin on another must wait for two things. Both belong to the chains, and
no construction removes either:

- **the foreign payment must be deep enough** that the chain will not undo it;
- **the DERO side must be final** for the person receiving on DERO. That is inclusion plus `STABLE_LIMIT = 8` blocks:
  18 s + 144 s = 162 s (`config.go:32, 77`; a block below `height − 8` is refused, `blockchain/blockchain.go:483-486`).

| | Monero | Zcash | DERO |
|---|---|---|---|
| target block interval | 120 s (`DIFFICULTY_TARGET_V2`, [cryptonote_config.h](https://raw.githubusercontent.com/monero-project/monero/master/src/cryptonote_config.h)) | 75 s since Blossom ([ZIP 208](https://zips.z.cash/zip-0208)) | 18 s (`config.go:32`) |
| consensus bound on a reorganisation | **none** (no such constant in `cryptonote_config.h`) | none in consensus; zcashd shuts down on a reorganisation deeper than 99 blocks (`MAX_REORG_LENGTH = COINBASE_MATURITY − 1`, zcashd `src/main.h`, `src/consensus/consensus.h`), a node rule | 8 blocks (`config.go:77`) |
| a received output can be spent after | 10 blocks (`CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE`) | — | — |
| depth DMZ waits today | 20 (swap, `tools/xmrswap/swap.go:30`); 10 (federation, `tools/vaultsigner/networks.go:16`) | 10 (`docs/ZEC_SWAP.md:137`; federation `networks.go:17`) | 8 |

**The floor is the foreign chain's own confirmation depth, plus 162 s on DERO.** Measured against it:

| Entry | Floor | Path at target | What the path adds | Where the addition comes from |
|---|---|---|---|---|
| XMR swap, 20 confirmations (the default) | 120 + 20×120 + 162 = 2,682 s ≈ **44.7 min** | 2,858 s ≈ **47.6 min** | 176 s | proof setup ≈12 s in parallel (8.51 s prove + 1.61 s generators; 0.74 s verify + 1.57 s generators, `research/crosschain/poc/proof-xmr/run.log`); Bob's Lock 18 s; Alice's wait for it to be stable, 144 s; the gate, one poll (2 s, `swap.go:110`) |
| XMR swap, 10 confirmations | 1,482 s ≈ 24.7 min | ≈1,658 s ≈ 27.6 min | 176 s | the same |
| ZEC swap, C_zec = 10 | 75 + 10×75 + 162 = 987 s ≈ **16.5 min** | 1,242 s ≈ **20.7 min** | 255 s | Accept 18 s, the maker's Lock 18 s, the 8-deep wait 144 s, the maker's Zcash claim mined 75 s |
| federation deposit, XMR, 10 confirmations | 1,482 s ≈ 24.7 min | ≈1,578 s ≈ **26.3 min** | ≈96 s | Register 18 s, the attestation (one daemon pass of ≈20 s, `vaultsigner/main.go:53`, then inclusion), the wait for every signer to see the address, the k Mint votes |
| federation deposit, ZEC | 987 s | ≈1,083 s ≈ 18 min | ≈96 s | the same |

Four things follow.

1. **Everything the swap adds is about three minutes.** The rest of the 48 is Monero. Atomic-swap machinery is not
   the slow part. What is proposed here adds only the stipend on a holder's first trade (≈1-3 min), U3's 144 s before
   the taker's companion stops, and U7's tiers on large trades.
2. **The federation is not faster to enter.** At equal depth it enters in about 26 minutes against the swap's 28.
   Its apparent 22-minute lead today comes from policy: the daemon waits a flat 10 confirmations
   (`vaultsigner/main.go:790`, `networks.go:16-17`), where the swap waits 20. `vault.bas` Mint (`contracts/vault.bas:313-350`)
   checks no confirmations at all; only each honest daemon waits.
3. **The one real speed lever is the confirmation count, and it is a security lever.** Each 10 Monero
   confirmations is about 20 minutes. Going below 20 moves reorganisation risk onto whoever receives the XMR, and
   Monero has no reorganisation bound. The largest miner observed in 2025, Qubic, averaged **23.38 %** of Monero blocks
   over the measured window and **28.33 %** during its identified selfish-mining periods
   ([arXiv 2512.01437](https://arxiv.org/html/2512.01437)). An 18-block reorganisation is reported only by secondary
   sources (`docs/XMR_SWAP.md:170`). **This study does not recommend cutting depth for speed** (§2.1, §6).
4. **Below 10 is not possible even if wanted.** The gate counts only outputs the wallet reports as unlocked
   (`swap.go:594-596`), which is Monero's 10-block spend age. The command line refuses `-confs` below 10
   (`tools/xmrswap/cmd/xmrswap/main.go:82-83`).

**Where the targets are promises the chains do not keep:**
- **Monero and Zcash blocks** arrive at random intervals, with long tails. Under an exponential model (assumed),
  20 Monero blocks take 40 min on average, with a standard deviation of √20 × 2 min ≈ 8.9 min. A transaction paid
  below the market fee can wait much longer.
- **No interval or reorganisation statistics exist for any of the three chains** (BL-30, `docs/BACKLOG.md:117`).
- **Every figure in this note is a target, not a measurement.**
- **No simulator run is evidence of chain latency.** The simulator mines a block whenever its pool holds a transaction
  (`vendor/derohe/cmd/simulator/simulator.go:588-589`).

---

## 2. The paths

### 2.0 Facts this study corrects, and the constraint every path obeys

**Corrections to the baseline and the earlier documents, each checked against the code:**

- **XMR-01 is mitigated at HEAD.** XMR-01 is padding the shared `htlc.bas` until the companion's reads fail.
  - The companion reads named keys (`tools/xmrswap/dero.go:469`, `getKeys`, `keysstring`, `variables:false`), and
    `FindLock` walks the contract in windows of 256 ids (`dero.go:659`).
  - A maker who still cannot read refunds anyway (`swap.go:916-919` → `refundBlind`, `swap.go:980`).
  - The inverted regression tests exist (`tools/xmrswap/adversary_test.go:288, 354`) and pass: two judges ran them and
    got `ok veil/tools/xmrswap`.
  - Padding with junk assets does not re-open it: derod returns a contract's balance map only when the whole state is
    asked for (`variables:true`, `vendor/derohe/cmd/derod/rpc/rpc_dero_getsc.go:97-110`).
  - What remains is a linear lookup cost on a padded contract, not blindness.
  - The ground pass reported XMR-01 as not fixed. That follows the audit's description (whole-contract reads at
    `dero.go:411`), which is the code before commit `9064f82` (2026-09-10) introduced the keyed reads. The comment above
    `refundBlind` (`swap.go:973-979`) still says every read is one `DERO.GetSC` over the whole contract; it is stale
    (§4 step 0).
- **XMR-06 is still open.**
  - `canResend` lets a Claim go out again while `h + 9 < t1` (`dero.go:821-825`). The claim cutoff stops new Claims
    at `h + 1 ≥ t1 − 16` (`swap.go:1435-1441`). So a re-send can land inside the band the margin exists to keep
    empty (`docs/audit/FINDINGS_2026-09-10.md:887-898`).
  - Separately, Alice's companion finishes as soon as her Claim *executes*, before it is stable (`swap.go:1349-1356`).
- **The Monero gate's default depth is 20, not 10** (`swap.go:30`). Bob's default t1 is lock + 420, not 400
  (`swap.go:99`). Stale figures remain in the docs:
  - `docs/XMR_SWAP.md:59, 71-72, 168` and §4 still say 10 confirmations and "roughly half an hour"
    (`XMR_SWAP.md:178`); the real path is ≈48 min.
  - `XMR_SWAP.md:48` and `docs/XMR_SWAP_BUILD.md:4` give the proof as 14.8 s / 1.15 s. `run.log` measures ≈10.1 s /
    ≈2.3 s wall clock, generators included.
- **`nfa.bas` ClaimOwnership returns the unit; it does not burn it** (`contracts/nfa.bas:93-101`). When `SIGNER()` is
  blank, which happens above ring size 2, it sends the unit to the recorded *old* owner (`nfa.bas:95, 99`).
- **The NFA's creator can rewrite** icon, cover, file, file-signature URLs and tags forever (`nfa.bas:105`). Every NFA
  therefore carries a trusted party that no document lists. With `ownerCanUpdate == 1` the recorded owner can do the
  same (`nfa.bas:106-107`).
- **The asset leg is tested at contract level** (`tests/htlc_test.go:386-421`, all three exits paid in an asset). The
  gap is the companion. `DeroRPC.Lock` hard-codes `asset ""` (`dero.go:732`), and Alice's offer check refuses any
  asset (`swap.go:1133-1138`).
- **Registration is free of fees.** A registration transaction is checked by its signature alone
  (`transaction_verify.go:230-243`). Every other transaction needs a DERO payload for its fee
  (`transaction_verify.go:265-276`, "we can not deduct fees, if no base").
- **A pre-signed DERO transaction dies within 11 blocks** (`TX_VALIDITY_HEIGHT = 11`, `blockchain/miner_block.go:43`,
  enforced at `blockchain.go:748`). No watchtower can hold a DERO Refund or Claim for later.
- **The DERO mempool is ordered by fee per byte** (`blockchain/mempool/mempool.go:377`). A secret carried in a call's
  arguments is readable before it is final.
- **Two statements in the ZEC docs are wrong.**
  - "The Zcash holder never bears the free option" (`docs/BACKLOG.md:143`): the maker holds an option until cltv, and
    ZEC_SWAP.md's own Z4 (`docs/ZEC_SWAP.md:260`) records nobody bearing it.
  - A maker claim at the last block before cltv is a race, not a safe exit (§2.2).

**The constraint.** The DVM has 37 built-ins. None verifies a signature, does curve arithmetic, or reads another
contract (`vendor/derohe/dvm/dvm_functions.go:58-94`). `SIGNER()` is real only when the transaction's DERO payload has
ring size 2 (`blockchain/transaction_execute.go:424-443`). A refused call returns its value only if a signer can be
identified. Otherwise the value is lost (`transaction_execute.go:395-404`). Storage gas is capped at 20,000 for every
execution (`config.go:44`, enforced in `dvm/sc.go:244-251`). So on DERO the only locks there are come from three
sources:
- a SHA-256 hash;
- a block height;
- `SIGNER()` equality at ring 2.

Every construction below fits inside that. Anything that needs DERO to check a signature, a curve point or a foreign
chain is impossible on this VM, not merely expensive.

### 2.0.1 Path 0 — the first DERO (missing from every earlier document)

**Today.** Every action a holder takes on DERO is a transaction that pays its fee from a DERO payload:
- the htlc Claim (signer-only, `htlc.bas:68-73`);
- Punish (`htlc.bas:122-127`);
- the board's Accept;
- the vault's Register (plus its `regfee`);
- a pool trade;
- a spore channel message.

The wallet adds a zero-DERO transfer just to carry the fee (`vendor/derohe/walletapi/wallet_transfer.go:132-150`). A
holder who arrives with only XMR or ZEC has none. A search of `docs/`, `research/` and `app/` finds no design for how
they get it. So today "no custodian" is false at the front door: the first DERO comes from an unnamed seller, in
practice an exchange.

**Upgrades considered.**

| Upgrade | Buys | Costs | Opens | Verdict |
|---|---|---|---|---|
| Stipend **before the maker's Lock** (designs 1, 3) | covers every later call, Punish included | nothing on the happy path | farming: an address that never locks XMR collects a stipend for about 10 s of proof CPU | **rejected**: the cheapest farm |
| Stipend **with the gate**, once her XMR is 20 deep (designs 4, 5) | farming costs a real XMR lock | nothing | **a DERO-less Alice cannot Punish**. If Bob vanishes before the gate and never refunds, her only exit is Punish at t2 (taker-only, `htlc.bas:127`), which needs a fee. She then has to buy DERO from someone off-platform, which is the trusted party the stipend was meant to remove, and a state that needs a human to leave it | **rejected** |
| **Stipend after the maker's own Lock is stable and π_a has verified, before Alice's XMR lock** (judge 3) | covers Claim *and* Punish; Alice's companion refuses to lock XMR until the stipend has arrived and can be spent | per taker who walks, the maker pays the stipend, the Lock fee (≈859 atomic units, measured, `FINDINGS_2026-09-10.md:342`) and a 3,000 Refund fee, and holds his capital to t1 (≈2.1 h); on every first trade the stipend sits on the critical path, between Bob's stable Lock and Alice's XMR lock: one inclusion (18 s) plus the spendability wait | **farming is profitable**: every walk pays the walker the stipend for about 10 s of proof CPU and a free registration, and the maker has no way to tell a newcomer from a sybil. A standing maker should expect to lose one stipend per walker, priced into the spread | **adopt**, with the cost named (decision 3) |
| A stipend forwarded by `htlc.bas` Lock | on chain | pays before Alice locks; needs DERO and an asset in one Lock, which `htlc.bas:43` forbids; redeploy | — | rejected (§6) |

**Size.** The three exits are not sent at the same fee.
- **Claim** is sent at its estimate: the contract's storage gas plus 200 (`Claim` calls `exit(…, true)`,
  `dero.go:833-835`; `fee = g.GasStorage + 200`, `dero.go:774`).
- **Refund and Punish** are sent at a fixed 3,000 atomic units (`FixedFee: 3000`, `dero.go:122`; the fallback at
  `:761-764`).
- **Every exit** is doubled on each re-send, twice at most (`Resends: 2`, `dero.go:122`; `fee *= 2`, `:804`). Only the
  mined attempt is paid.
- **Punish has no deadline in the companion** (`canResend` returns true for it, `dero.go:821-830`). Its doubling to
  12,000 exists only because the retry code caps re-sends at two, not because a deadline forces it.

Alice's worst case is a Claim that is mined and then refused, whose fee is still paid, followed by Punish. The stipend
must cover **4 × the Claim estimate + 12,000 atomic units**. The 12,000 (**0.12 DERO**) is a floor. The `htlc.bas`
Claim estimate has not been measured. The nearest measured analogue, `swap.bas`'s Claim, used 232 gas
(`HISTORY.md:2983`); a similar figure would put the sum near 0.14 DERO (**assumed** until measured on the simulator,
§4 step 2). A first pool trade after the Claim needs an estimated fee on top. The amount is a maker's offer term, not a
price.

**Two prerequisites the screen must state:**
- The receiving DERO address must first be **registered**: a fee-free proof of work that the wallet's own text calls
  "a couple of minutes" (`cmd/dero-wallet-cli/easymenu_post_open.go:127-129`, not measured).
- A freshly received balance was once seen unspendable for a couple of blocks ("Account Unregistered",
  `docs/history/HISTORY.md:801`, observed for a newly minted asset; the retry 40 s later passed). The companion must
  wait for the stipend to be *spendable*, not merely present.

Both sit on a first trade's critical path. The maker cannot pay an address that is not registered: a contract's
single transfer to one is refused (the LIV-04 mechanism, `FINDINGS_2026-09-10.md:324`), and that a wallet transfer
fails the same way is **assumed**, not re-read. And Alice's companion will not lock XMR until the stipend is spendable.
Registration can start the moment the app opens, so it overlaps setup when started early. The inclusion and the
spendability wait, ≈18-60 s (not measured), cannot overlap anything.

**The first-time setup channel.** Setup messages travel over spore, and spore costs a DERO transaction per message
(`docs/XMR_SWAP.md:242-246`). A newcomer's first setup therefore travels off-DERO: a file, the directory channel, a QR
code. The alternative is a small setup budget from the maker, rate-limited. That choice is operator decision 3.

**For the ZEC market:**
- With `swap.bas` v2 (§2.2) the taker needs no gas for the Claim. She still needs a registered address: a Claim whose
  one transfer goes to an unregistered taker is refused and reverted (the LIV-04 mechanism). Nothing is lost — she
  registers and anyone sends the Claim again before T_dero — but the maker's companion should refuse to lock for an
  unregistered taker. Registration overlaps the Zcash confirmations, so it adds nothing to this path's ≈21 min.
- `Accept` still needs gas. It comes before the maker knows who the taker is, so the maker cannot send a stipend in
  time.
- The only way round it: a first-time taker sends the maker its DERO address, refund key hash and cltv off-chain. The
  maker then locks `swap.bas` naming that taker directly (`swap.bas:24`, `taker` argument), and the board is bypassed.
  Operator decision 9.

**Recommendation.** The stipend described above is a protocol step, not a courtesy. The trust statement says: *"your
first DERO for gas is a gift from your counterparty, sent after they have committed and before you have; you owe them
nothing for it and they cannot take it back."*

### 2.1 Path 1 — XMR ↔ DERO-side asset (three-exit htlc.bas + proof + xmrswap)

**What it is today.**
- **Contract:** `contracts/htlc.bas`, 3,598 storage gas at install (`HISTORY.md:2766`).
- **Exits:** three, each guarded by `st == 0`:
  - Claim, by the taker, before t1, with s_a and the gate secret k_b (`htlc.bas:66-90`);
  - Refund, by the maker, in [t1, t2), with s_b (`:94-117`);
  - Punish, by the taker, from t2, with nothing (`:120-136`).
- **Run:** only on the simulator, with a fake Monero wallet (`HISTORY.md:2779-2783`). Never against a Monero node,
  stagenet included (BL-11). The channel is a shared directory (BL-10).
- **Trusted today:**
  - the cryptography, including a proof nobody outside the project has audited (`XMR_SWAP.md:449-455`);
  - each party's own DERO node, Monero wallet-rpc and daemon (R18);
  - the companion's code;
  - that Monero does not reorganise deeper than 20 after the gate, which is not a consensus rule (`XMR_SWAP.md:455-461`).

**Roles decide who can lose.** The taker's role (Alice, the XMR holder entering) is **forgiving**:
- a missed cutoff means Bob refunds, which reveals s_b, and she recovers her XMR with no deadline;
- if Bob does nothing at all, she Punishes from t2, and the contract sets no end to her window (`htlc.bas:120-136`).

That open end cuts the other way too. If she never Punishes, nobody else can move the lock, and the maker's asset is
frozen for ever (attack H, LIV-07).

Her one real loss is losing the companion's state file after she has sent XMR. She then cannot form the joint key even
after Bob reveals s_b. The maker's role (Bob) is **unforgiving**. Refund is valid only in [t1, t2) (`htlc.bas:102`),
and Punish needs no XMR lock and no secret. A maker whose companion is off for that whole window loses what he locked
to a taker who may have locked nothing. **A holder who exits takes the maker's role.**

**Attacks that stand at HEAD:**

- **A. Claim withholding (found only by the adversary pass; confirmed in code).**
  - *The precondition.* Alice accepts a gate at any height below t1 (`swap.go:1307`, the only height check). Bob's
    companion releases the gate while `h + 18 < t1` (`GateMargin`, `swap.go:106`). A dishonest Bob releases at t1 − 19.
  - *The theft.* Her Claim carries s_a in clear into a mempool ordered by fee per byte (`mempool.go:377`). Bob reads
    s_a, which gives him s_a + s_b, sweeps the joint XMR at once, and keeps her Claim unmined until t1 by out-bidding
    it or with mining power. At t1 he Refunds. He takes both legs; she learns s_b too late, after the sweep.
  - *What it costs him.* The reference block template takes transactions in fee-per-byte order
    (`Mempool_List_TX_SortedInfo`) and stops at `STARGATE_HE_MAX_BLOCK_SIZE` − 100 KB = 10,396 KB
    (`vendor/derohe/blockchain/miner_block.go:229-282`; `config.go:52`; `mempool.go:377`). That miners run the
    reference template is **assumed**. At the minimum fee, filling one block costs 10,396 KB × 20 atomic units/KB
    ≈ 2.1 DERO (`config.go:47`), about 40 DERO for 19 blocks. That is a minimum-fee floor. The true price per block is
    Alice's Claim fee per byte × 10,396 KB, and her fee is known from code: the estimate, gas + 200, doubled at most
    twice (`dero.go:774, 804`). A mining majority can do it for nothing.
  - *The gap in the documents.* No trust statement names "DERO is not censored over the claim margin".
    `XMR_SWAP.md` R2 covers only the reorganisation form of this attack.
- **B. XMR-06** (above): a re-send inside the band, and a finish before the Claim is stable.
- **C. Punish without an XMR lock** makes the maker's liveness a theft condition, as described under roles.
- **D. The case asymmetry.** `htlc.bas` Lock checks only the length of the three hashes (`htlc.bas:31`). `swap.bas:36`
  and `board.bas` refuse hex that is not lower case. The companion's `isHash` accepts upper case (`swap.go:353-356`).
  An upper-case H_k passes Alice's check, she locks XMR, and then every k_b fails the lower-case comparison. This is
  griefing, not theft: Refund reveals s_b and Punish pays her.
- **E. The code at the SCID is never read.** Every companion read passes `code:false`; only Install compares code
  (`dero.go:442-446`). The SCID comes from the user's flags, and the planned page fills them in. A phished page can
  point the companion at a look-alike contract whose storage passes every check. The genuine page can be replaced too,
  by whoever holds the TELA INDEX publisher key (SEC-K-03, `FINDINGS_2026-09-10.md:408`).
- **F. Free options on both sides.**
  - Alice, before she locks: t1 = lock + 420 must still hold her 20-confirmation path (≈140 blocks) plus the cutoff
    (16) and the gate, so she can wait about 250 blocks, ≈75 min.
  - Bob, at his own default t1, from her 20th confirmation (≈block 150) to t1 − 19: about 250 blocks, ≈75 min.
  - At 6 % daily volatility (**assumed**, `XMR_SWAP.md:592`, `research/crosschain/RESEARCH.md:204-205`), σ over
    75 min ≈ 1.4 %. An at-the-money option is worth ≈ 0.4σ ≈ 0.55 % of notional (a standard approximation, assumed).
  - **But the maker chooses t1.** Alice's companion accepts any t1 − now up to T1Max = 1,200 and any t2 − t1 up to
    T2Max = 1,200 (`swap.go:44-45`, checked at `swap.go:1068-1071`). A maker who names the ceiling holds the option
    from ≈block 150 to ≈1,190: about 1,040 blocks, ≈5.2 h. At the same assumed volatility that is σ ≈ 2.8 % and an
    option worth ≈1.1 %, twice the default. Alice's XMR is then committed for up to ≈6 h before she has any exit of her
    own, and t2 can be ≈2,400 blocks (12 h) out. Operator decision 6 carries an option to lower T1Max toward the
    default plus slack.
  - Han, Lin and Yu model a swap as an American call option without the premium and put the premium for
    cryptocurrencies at 2-3 % of the asset's value (<https://eprint.iacr.org/2019/896>). Their parameters are not these.
- **G. A Monero reorganisation after the gate** (R3). No depth defeats a sustained majority; §1 has the observed shares.
- **H. LIV-07: an abandoned lock past t2 is frozen for ever** (`htlc.bas:94-136`, `FINDINGS_2026-09-10.md:831-845`,
  reproduced with 100,000 DERO still held ten million blocks past t2).
  - Refund is the maker's only in [t1, t2). Punish is the taker's only from t2. There is no third window and no
    permissionless sweep.
  - If the taker never Punishes — key lost, walked away, or the lock worth less than her attention — nobody can move
    the lock. A taker who never locked XMR can destroy a maker's lock at no cost by doing nothing.
  - The same corner is reached when the maker cannot Refund at all: `htlc.bas` checks only the length of hb
    (`htlc.bas:31`), so a lock against an hb the maker holds no preimage for has Refund closed for good.
  - This is a state that needs a human to leave it, which the operator's ruling forbids. With U1 the frozen value is a
    token or an NFA unit.
  - `htlc.bas` is undeployed (no SCID in `deploy/networks.json` or `app/config.js`, per the audit), so now is the
    cheapest time to change it. The fix is U13.
- **Privacy.** Alice hands the counterparty the joint address's private view key (`XMR_SWAP.md:38`). A dominant maker
  therefore sees every taker's XMR amount and timing. On DERO the maker, the taker, the amount, the hashes and s_a are
  public (`XMR_SWAP.md` §5).

**Upgrades considered.**

| # | Upgrade | Buys | Speed cost | Opens | Build | Verdict |
|---|---|---|---|---|---|---|
| U1 | **Asset leg in the companion** (BL-20). `Lock` carries a SCID; a transfer of `{SCID: asset, Burn: v}` with no DERO burn, **always ring 2** (the wallet's zero-DERO payload supplies the signer, `wallet_transfer.go:132-150`); balance per asset; a flag; decimals; `Terms` gains the asset | XMR straight into a token, an NFA unit or wXMR; the exit leaves straight from the token (no pre-sale); no DERO exposure mid-swap; a federation-independent exit for wXMR (§3) | none; saves one pool trade each way | a Lock above ring 2 is refused and its asset lost (`transaction_execute.go:395-404`); a thin token's price can be moved between setup and lock, so the price is fixed at lock; a mis-scaled amount (the BL-104 class); **a look-alike token or NFA with the agreed name**: the agreed SCID reaches Alice's companion from the offer or the page, and CheckLock only compares the lock with it (`swap.go:1075`), while U4 pins `htlc.bas`'s code, not the asset's. The companion must show the asset's SCID, name and supply read from the chain, and run the app's look-alike check, before the XMR lock (APP-1, `FINDINGS_2026-09-10.md:649`) | about 1 week, plus 2-3 days for the on-chain identity screen and look-alike check, plus the simulator run (assumed); no contract change | **adopt** |
| U2 | **Gate deadline G** (Alice treats a k_b arriving after t1 − G as not received and waits for Refund or Punish; Bob's GateMargin rises to ≥ G) | raises attack A from about 19 blocks of suppression to about G: at G = 100 (**assumed** value) the minimum-fee floor is ≈210 DERO; also cuts Bob's free option by G − 18 blocks | none: the gate normally arrives near block 150 of 420 | an honest maker whose gate misses t1 − G turns a trade into a refund; both lose about 2 h and fees, nobody loses coins | days (assumed) | **adopt** |
| U3 | **XMR-06:** give `canResend` the claim margin, and make Alice wait until her Claim is stable before finishing | closes the re-send band and the early finish | +144 s before her companion may stop; her DERO is already hers | none | days | **adopt** |
| U4 | **Pin the code.** The companion reads the SCID's code once and compares it with the canonical `htlc.bas` it was built with; and it requires lower-case hex for H_k and H_b now (the contract gains the check at its next redeploy) | removes phished and look-alike contracts; closes attack D | none | the canonical bytes live in the binary, so a reproducible, signed build (BL-14) is the only check on it | days | **adopt** |
| U5 | **Wider maker refund window:** default T2 − T1 from 400 toward the existing ceiling of 1,200 (`swap.go:25, 45`) | a maker offline for up to ≈6 h is not Punished; the largest liveness risk on the exit | none on the happy path | a taker whose maker vanished waits until t2 = lock + 1,620 blocks ≈ 8.1 h to Punish at the default t1, ≤ 12 h at the ceilings (she ends with the maker's asset) | configuration | **adopt**, value is operator decision 6 |
| U6 | **The exit counts as done at Claim-stable.** Once Alice's Claim is stable, Bob alone holds s_a + s_b, and the joint output is already 20 deep, past Monero's 10-block spend age; the sweep is his first spend, not a condition of ownership | the exit falls from ≈70 to **≈48 min + scan** (18 + 144 + 120 + 2,400 + 2 + 18 + 144 s ≈ 2,846 s plus setup), to ownership. His first spend still waits for the joint wallet's restore and scan: made from keys, it must scan before it can spend anything (XMR-2, `FINDINGS_2026-09-10.md:184`; the sweep wait allows up to 20 min, `swap.go:46-52`), and that scan has never run against a real `monero-wallet-rpc` (BL-11). ≈48 min is a lower bound | none | none, as long as Invariant 9 holds (never act on an unstable Claim, never Refund after reading s_a, `XMR_SWAP_BUILD.md:257-262`) | a reporting change | **adopt** |
| U7 | **Confirmations scale up with notional:** 20 is the lowest default; 30 or 40 above thresholds the operator sets; t1 grows to match, capped by T1Max = 1,200 (`swap.go:44`) | narrows R3; it can never close it | +20 min per 10 confirmations (≈68 min at 30, ≈88 at 40) | a longer window means a longer free option for both sides. **With U2, t1 must be checked, not assumed**: the rule is t1 − lock ≥ the stable wait + the expected Monero path at the tier + G + k·σ (k set in the profile), capped at T1Max. At G = 100 and k = 2 (both **assumed**), with an exponential model of Monero blocks (assumed): the Lock's inclusion (1 block), the stable wait (8), the XMR transaction mined plus c confirmations ((c + 1) × 120 s) and G. At 20 confirmations that is 1 + 8 + 140 + 100 = 249 blocks plus 2σ (σ = √21 × 120 s ≈ 31 blocks) ≈ 310, inside 420; at 30, 316 + 2 × 37 ≈ 390, inside 420 with 30 blocks spare; at 40, 382 + 2 × 43 ≈ 467, **above 420**, so t1 must rise to ≈470 or honest trades end in refunds. DERO's own block variance is left out (assumed small against Monero's) | configuration, in one mainnet profile (BL-27); the thresholds wait on BL-30 | **adopt** upward only, with the t1 rule |
| U8 | **External audit of proof-xmr and a second verifier** (BL-23) | the largest trust item left. If the proof is unsound, a counterparty reveals a value that hashes right but is not their key share, and the other side's XMR is locked forever (`XMR_SWAP.md:43-50`) | none; caching the generators would save ≈1.6 s a call | none | the audit's price is not known to this project | **adopt as the mainnet gate** |
| U9 | **Sleep-until scheduling and a recovery card** (BL-15): at each irreversible step the companion registers an OS wake for its next window and prints the heights as wall-clock times, the resume command, and the state file's path marked as a secret to back up *before* the XMR lock | turns "stay awake" into "have your machine on once, at a stated time" | none | the backup widens where s_a and s_b live | moderate (three OS wake APIs) | **adopt** |
| U10 | **Standing makers** run by a named third party (an analogue of an eigenwallet ASB) | removes the discovery wait | none | holding inventory, the maker is the natural target of free options and griefing | medium to large, after BL-10 and BL-12 | **only when a named someone-else runs one**; the operator hosts nothing |
| U11 | htlc v2 with the gate on chain and a taker bond (design 1, U1.5) | attributes one kind of walk | ≈+18 s | **under attack A, the bond becomes the maker's profit**: he records the gate, withholds the Claim until t1, and collects the bond, the refund and the XMR | new contract | **rejected for now** (§6) |
| U12 | Quoted depth as low as 10 (design 2, U1-C) | ≈20 min on every entry and exit | — | reorganisation risk against a pool with a 23-28 % miner | configuration | **rejected as a default**; at most a maker's disclosed choice, after BL-30 (§6) |
| U13 | **`htlc.bas` v2** (LIV-07): a final maker window from t2 + G2 in which the maker takes the lock back **by revealing s_b, as Refund does**; and Lock refuses an ha, hk or hb that is not 64 lower-case hex characters (U4's contract half) | every lock resolves without a specific human: if the taker never Punishes, the maker recovers after t2 + G2, and because he must reveal s_b, the taker can still recover her XMR at any later time | none on the happy path | **the taker's Punish gains a deadline**: a taker offline for the whole of [t2, t2 + G2) loses the punishment payment, though not her XMR. The audit's version (the maker takes the lock back with no secret) is worse for her: her XMR would stay locked for good. A maker who has lost s_b is still frozen, by his own loss | a new contract and SCID; `htlc.bas` installs at 3,598 storage gas (`HISTORY.md:2766`), far inside the cap; the canonical bytes for U4 change; days plus the DVM tests (assumed) | **operator decision 17**, not a default; if adopted, before §4 step 2's stagenet run and before any mainnet SCID |

**Latency and lock-up with the adopted set** (targets):
- **First trade:** ≈48 min to the token, final; on a holder's first trade add Path 0, ≈1-3 min (registration, one
  inclusion, spendability; not measured).
- **Next trade:** 18 s to inclusion, ≈2.7 min to final.
- **Exit:** ≈48 min to ownership plus discovery, straight from the token; the first spend of the XMR also waits for the
  joint wallet's scan (unmeasured).
- **The maker's capital:** locked ≈48 min on the happy path, until t1 (126 min at the default, ≤ 6 h at T1Max) if the
  taker walks, and until t2 (≈8.1 h with U5 at the default t1, ≤ 12 h at the ceilings) if the swap stalls.
- **Alice's XMR:** held from her lock until her Claim, or until Bob's Refund at t1 (≤ 6 h at T1Max). If Bob never
  refunds, she Punishes at t2 and takes his asset, and her XMR stays locked for good, because she never learns s_b.

**Recommendation.** Ship U1-U6 and U9 with the stipend (§2.0.1) and prove them on stagenet, as one milestone (§4,
steps 1-2). Adopt U7 upward only, with the t1 rule. Treat U8 as the gate before mainnet. U10 waits for a name. U13 is
operator decision 17; if it is adopted, the v2 contract is what step 2 runs on stagenet.

Rewrite `XMR_SWAP.md` §10 so it names, beside the reorganisation assumption:
- **"DERO is not censored for G blocks after the gate"**;
- **"a maker who is offline for the whole of [t1, t2) loses the locked asset, because Punish needs no lock"**;
- **"a lock the taker never Punishes is frozen for ever"** (LIV-07), until U13 exists;
- **"whoever runs the node you read from is trusted"** (R18).

Only with those stated does the word **trustless** stand for this path, and it always carries the list of what is
still trusted: each party's own nodes and companion, and the cryptography.

### 2.2 Path 2 — transparent ZEC ↔ DERO-side asset (board.bas v2 + swap.bas + tools/zecswap)

**What it is today.**
- **Run:** the DERO leg has run on the simulator (`HISTORY.md:2980-2988, 3041-3048`). The Zcash script has run only
  under Zcash's consensus interpreter.
- **Missing:** `tools/zecswap` has no node client, watcher, broadcast or DERO client, so the path cannot run.
- **Enforced by nobody:** T_dero = 1,600, the taker's 1,200-block precondition and P_lock = 200 are written in the
  design note alone (`ZEC_SWAP.md:138-141`). Only the cltv window is enforced in code
  (`tools/zecswap/script.go:387-425`).
- **Trusted today:** each party's DERO node and Zcash node or lightwalletd (`ZEC_SWAP.md:283-285`). Not private: the
  amount, both t-addresses, and H and s join the two chains (`ZEC_SWAP.md:27-47`).

**Roles.** Here the taker (the ZEC holder entering) has the **unforgiving** role, twice over:
- **Z17.** The claim branch of the Zcash script never expires. A taker who does not refund between cltv (≈2 h) and
  T_dero (8 h) loses the ZEC to a maker who refunds the DERO and later spends with s (`ZEC_SWAP.md:273`).
- **The late claim.** If the maker claims late and so reveals s, the taker must send her own DERO Claim before T_dero.
  `swap.bas` Claim requires the named taker to be the signer (`swap.bas:73-74`). After T_dero the maker can Refund,
  with no deadline (`swap.bas:93-94`), and hold both assets.

The maker's role, which a holder takes when exiting to ZEC, is forgiving: a lost secret costs a refund after 8 h
(`ZEC_SWAP.md:246`).

**Attacks that stand:**
- **The claim near cltv is a race.** After cltv both of the script's branches are valid. A maker claim still in the
  mempool at cltv shows s, and if the taker's refund confirms first, she holds her ZEC *and* s, claims the DERO, and the
  maker loses both legs. Z4's "nobody" (`ZEC_SWAP.md:260`) is wrong.
- **The maker's option.** After the taker funds, the maker may decline to claim until cltv, about 2 h. That contradicts
  `BACKLOG.md:143`.
- **Board freeze** (ADV-PRICE-07, `FINDINGS_2026-09-10.md:1111`). One `Accept` freezes an offer until its expiry: Cancel
  of an accepted offer waits for expiry (`contracts/board.bas:79-91`), which may be up to 1,000,000 blocks ahead
  (`board.bas:40`).

**Upgrades considered.**

| # | Upgrade | Buys | Speed cost | Opens | Build | Verdict |
|---|---|---|---|---|---|---|
| Z-A | **Finish zecswap** (a `zcashd`/`zebrad` JSON-RPC client, a watcher, the ZIP 317 fee, broadcast, a DERO client). The rules that live only in the note go into code: T_dero − now ≥ 1,200; the lock at least 8 deep; the escrow's asset and amount equal the board's (the board is not bound to the escrow); P_lock; and a **maker claim cutoff** at cltv − R (R assumed ≥ 20 blocks) | the path can run; the taker's margins stop being a document | none | the node read is a trusted party (Z14, Z15), as already stated | 3-5 weeks (assumed); no new dependency (JSON over HTTP is in Go's standard library; secp256k1 is already justified, `ZEC_SWAP.md:204-208`) | **adopt; nothing on this path runs without it** |
| Z-B | **The taker's pre-signed refund.** As soon as the funding transaction exists (v5 transaction ids are not malleable, [ZIP 244](https://zips.z.cash/zip-0244)), the companion signs the refund: `nLockTime = cltv`, a non-final `nSequence` ([BIP 65](https://github.com/bitcoin/bips/blob/master/bip-0065.mediawiki); `script.go:120-122, 324-326`), `nExpiryHeight = 0` ("To set no limit on transactions (so that they do not expire), nExpiryHeight should be set to 0", [ZIP 203](https://zips.z.cash/zip-0203); zecswap caps expiry only from above, `tools/zecswap/sighash.go:22-24, 255`). It is signed **at two or three fee levels**, because [ZIP 317](https://zips.z.cash/zip-0317)'s block template picks each transaction on its own fee ratio, with no ancestor or package logic, so child-pays-for-parent cannot rescue a stuck one. The companion refuses to sign if a network-upgrade activation falls in [now, T_dero], because the signature commits to the branch id (`ZEC_SWAP.md:217-229`) | Z17 no longer depends on the taker being alive: anyone holding the hex (a second device, a friend, any node) can broadcast it from cltv on, and it can pay only her refund key | none | the fee is frozen at signing; whoever holds the hex learns about the swap; **the hex expires at the next network-upgrade activation**. The claim branch never expires, so the refund stays needed after T_dero, until the maker spends; but the signature commits to the branch id, and after an activation the hex must be re-signed, which needs the taker. The screen gives that activation height as the hex's expiry | ≈1 week on top of Z-A (assumed) | **adopt** |
| Z-C | **`swap.bas` v2: Claim pays the stored taker, whoever sends it.** When `t_<id>` is set, Claim sends to `LOAD("t_"+id)` and drops the equality check at `swap.bas:73-74`. The maker's companion sends that Claim straight after broadcasting its Zcash claim; so can anyone who reads s. Precedent: vault Reclaim pays the stored burner, whoever calls it (`vault.bas:475-483`) | removes the late-claim loss: if the maker reveals s before T_dero, anyone completes the taker's Claim. It does nothing against Z17, where a dishonest maker reveals s only after he has Refunded the DERO (`ZEC_SWAP.md:273`); by then `st_` is 2 and no Claim can fire. There the taker's only guard is Z-B's refund being broadcast by someone between cltv and the maker's spend. The taker also needs no DERO for the Claim | none; ≈75 s saved if the maker sends it at broadcast | none found that loses value: front-running pays the taker; `st_` prevents a second payout (`swap.bas:67, 75`). The stored taker must be registered, or every Claim is refused and reverted (§2.0.1). Keep the signer-validity check (`swap.bas:65`); a ring-16 variant would need it dropped as well, and gains nothing, because Claim carries no value | a small change well inside the cap (`swap.bas` installs at 3,559, `research/crosschain/RESEARCH.md:129`); a new SCID | **adopt before any mainnet `swap.bas` exists** |
| Z-D | **A minimum escrow width** in swap v2: Lock refuses an expiry below lock height + W_min, a per-deployment constant (1,600 for this market) | removes "the taker's companion checked T_dero" as the only guard against a short-expiry lock (the bad exit, `ZEC_SWAP.md:103-105`) | none | none | one comparison, folded into Z-C | **adopt** |
| Z-E | **`board.bas` v3 acceptance deadline:** the maker may Cancel an accepted offer after the accept height + W (W = P_lock = 200, **assumed**) | bounds ADV-PRICE-07's freeze to W; the board holds no value, so the taker, whose protection is the escrow, loses nothing | none | none | small, a new SCID | **adopt** |
| Z-F | Shorten T_dero once Z-B exists (design 1) | halves the maker's lock-up | — | T_dero also protects the taker's DERO Claim after a late maker claim; a shorter one shrinks the window Z-C covers | — | **rejected** |
| Z-G | ECDSA adaptor signatures on the Zcash side | privacy of H and s only; amounts and addresses stay public | ≈+12 s of proof (assumed, by analogy with proof-xmr) and a channel before funding | a new, unaudited SHA-256-to-secp256k1 proof | large | **rejected** (§6) |
| Z-H | Merged offer and escrow with bonds burned on failure (D-7) | removes P_lock and the maker's Lock step (≈18-36 s); makes griefing cost a bond | the maker's capital is locked from Post | a maker can burn an honest taker's bond by never claiming; a first-time taker has no DERO to bond with | medium | **deferred** until after testnet (`ZEC_SWAP.md:171-172`) |

**Latency with the adopted set:**
- **First trade:** ≈20-21 min.
- **Next trade:** one DERO transaction.
- **Exit:** ≈18 min plus discovery, as maker.
- **Capital:** the maker's asset up to T_dero = 8 h if the taker walks; the taker's ZEC until the maker claims, or
  until cltv ≈2 h.
- **The floor** is 16.5 min (§1). This path is the closest to it.

**Recommendation.** Build Z-A with Z-B, and redeploy Z-C/Z-D and Z-E, before the first mainnet holder. Then
correct Z4 and `BACKLOG.md:143` and prove the path on Zcash testnet (BL-42). The trust statement of `ZEC_SWAP.md` §10
holds once the margins are in code, the refund is pre-signed and the Claim can be sent by anyone. It still carries one
party-online window: someone must broadcast the taker's refund between cltv and the maker's spend, and no construction
on these two chains removes it (operator decision 15). The disclosure in §1
of that note stays, word for word, before the first button.

### 2.3 Path 3 — the federation (vault.bas v3 + vaultsigner)

**What it is today.**
- **No real backend exists:** the daemon exits on `zcash`, `monero` and `solana`, and refuses the JSON-file backend off
  the simulator (`tools/vaultsigner/main.go:121-145`). No signer has ever held a real coin.
- **What k colluding signers can do:** mint against nothing or take the reserve, invisibly to DERO. Mint counts votes
  and checks the cap and window, not foreign depth (`vault.bas:313-350`). Bonds limit what can be extracted
  (`vault.bas:329-330`), but slashed bonds go to the treasury (`vault.bas:565`), and colluders do not slash themselves.
- **The daemon's view of real chains:** it waits `fh − d.Height ≥ confs` (`main.go:790`), and it has no finality
  function for any real chain.
- **Deposit lag strands coins by omission.** Each signer credits only deposits at or above the foreign height at which
  *it* first saw the attested address (`main.go:745-756, 808-813`). A payment made before the slowest n − k + 1
  signers noticed the address can never gather k votes. Nothing tells the user when paying is safe.
- **LIV-03 / SEC-K-01** (`FINDINGS_2026-09-10.md:310-321, 380-391`):
  - any one signer's Paying mark (`vault.bas:438-448`) disables Reclaim for that withdrawal forever (`vault.bas:479`);
  - the daemon marks every withdrawal before paying (`main.go:388-395`);
  - so every paid withdrawal depends on k live signers forever, and at na == k one lost key strands it.

  This contradicts the contract's own header (`vault.bas:4-9`).

**Upgrades considered, briefly** (§3 carries the reasoning):
- **A LIV-03 rule (vault v4).** An operator choice of who loses, not a free fix.
- **A swap exit and entry for wrapped units through Path 1's asset leg.** Adopt it as a liveness hedge, not a solvency
  hedge.
- **An HTLC-settled withdrawal.** Rejected: the federation would need a single scalar with a SHA-256 proof. If one
  signer holds it, that signer steals every refunded withdrawal. If k of n hold it, it needs MPC over SHA-256 plus a
  Bulletproof, which is research.
- **Confirmation checks inside the contract.** Rejected: the DVM cannot read a foreign chain.

**Latency:**
- **First deposit:** ≈26 min for XMR, ≈18 min for ZEC, to DERO-final minted units (§1).
- **Next trade:** one DERO transaction.
- **Withdrawal:** Burn 18 s, DERO-final 144 s (`main.go:902-906`), then wdelay. That is 0-20,000 blocks plus the
  large-withdrawal delay (`vault.bas:101-111, 387-390`), with **no deployment value anywhere in the repository**. Then
  Paying 18 s, the payout mined 120 s, a multisig or FROST signing round that is **not built and not measured**, and 10
  confirmations before the user can spend (1,200 s). That is ≈25 min plus the round at wdelay = 0.

**Recommendation.** Not deployable under the operator's rulings until non-operator signers are named and the LIV-03
rule is chosen (§3). It is never called trustless, atomic or non-custodial.

### 2.4 Path 4 — tokens and NFAs (pool.bas, launch.bas, nfa.bas)

**Today.**
- **A trade:** one DERO transaction (`pool.bas:136-191`), with a `min_out` bound (`pool.bas:136, 183`), so a sandwich
  can take at most the holder's own slippage setting. 18 s to inclusion, ≈2.7 min to final.
- **Trust:** the DERO contracts; a pool quoted in wXMR adds Path 3; the factory owner key, which can never be revoked
  and whose SetTreasury delists every existing market from the app in one call (LIV-08 and SEC-K-02,
  `FINDINGS_2026-09-10.md:845, 394`).
- **Open NFA defects** that bear on any unit a holder buys:
  - **NFA-1 and LIV-02** (`FINDINGS_2026-09-10.md:170, 296`): a stale bid record survives the listing that made it
    and is a standing call on the asset. When any later listing expires, any wallet can settle it to the old bidder at
    the old price, taking the asset from an owner who never agreed.
  - **LIV-01 and DVM-1** (`FINDINGS_2026-09-10.md:41, 240`): an unregistered payee earlier in the settlement order
    destroys the seller's whole payout.
- **Repaired since the audit:** APP-1 (`FINDINGS_2026-09-10.md:649`). NFAs now get the impersonation and look-alike
  checks a market gets, so a hostile name no longer wears the plain "Verified" badge (`099a4b1`: `app/feed.js`
  `decorate()`, asserted by `tests/js/sanitize.test.mjs`).
- **An NFA** is sold through `nfa.bas`'s own marketplace, and only for DERO or a wrapped quote.
- **XMR to a token** is two steps, a swap and then a pool trade, with no atomic link. `htlc.bas` cannot call
  `pool.bas`, because the DVM has no cross-contract calls.

**Upgrades.** Only one is worth building: XMR or ZEC straight into a token or one NFA unit through the asset leg (Path
1 U1; for ZEC, `board.bas` already carries `giveAsset`, `board.bas:37`, and `swap.bas` locks assets, `swap.bas:31-46`).
Four things go with it:
- **For an NFA:** only a unit held outside its marketplace can be locked. The buyer's companion sends ClaimOwnership
  straight after the Claim, **pinned to ring 2**, because a blank signer pays the unit to the old owner (`nfa.bas:95,
  99`).
- **Until ClaimOwnership lands:** with `ownerCanUpdate == 1`, the seller can still rewrite the headers (`nfa.bas:106-107`).
- **The NFA screen shows:** the creator's lifelong Update power (`nfa.bas:105`) and `ownerCanUpdate`.
- **Every wXMR-quoted price** carries the federation-backed label (`XMR_SWAP.md:516-518`).
- **An NFA unit acquired by swap inherits NFA-1.** If the NFA carries a stranded bid, relisting it lets any wallet
  settle it to the old bidder at the old price. Until NFA-1 is decided, the companion refuses to lock, and the screen
  refuses to quote, an NFA whose `currBidAddr` is set (`nfa.bas:211, 296-307`). The companion is not the app and has none of
  its name checks, so it shows the unit's identity read from the chain — its contract id, `nameHdr` and creator (U1).

**Recommendation.** Default to DERO-quoted markets. The one decision here is the float: what a holder keeps between
trades (operator decision 2).

---

## 3. What the federation is still for

**The operator's question, answered again.** The contract can run with nobody. The lock cannot, ever, by construction.
Wrapping means real XMR or ZEC sitting under keys that k of n people hold. On this VM, no construction moves that
custody into a contract:
- the DVM cannot verify a signature or read a foreign chain (`dvm_functions.go:58-94`);
- every "atomic" form of the federation needs either one signer holding a scalar that steals, or threshold MPC over
  SHA-256 (§2.3).

A federation is therefore always a set of humans holding keys, and the operator's ruling forbids the operator being
one of them.

**What the recommended upgrades take away from it.**

- **Entry.** Once the asset leg exists, an XMR holder reaches a token with no custodian in the same order of time, ≈48
  min against ≈26 min. The federation's lead is 10 against 20 confirmations, not structure (§1).
- **The next trade.** Not the federation's to claim. A holder in DERO or tokens trades again in one DERO transaction,
  exactly like a holder of wXMR.
- **Exit.** The federation is faster at wdelay = 0: ≈25 min plus a signing round, against the swap's ≈48 min plus the
  joint wallet's scan. It pays
  from a reserve that is already confirmed. That lead disappears once wdelay is above zero (every 100 blocks is 30 min),
  it depends on a signing round that does not exist, and under LIV-03 a paid withdrawal depends on k live signers
  forever.

**What only the federation can give.**
1. **A balance denominated in XMR or ZEC on DERO**, usable as a market's quote. A swap holder between trades holds DERO
   or tokens and carries their price.
2. **Shielded ZEC.** No trustless construction exists: the Orchard key-share swap is research (BL-50), and re-randomised
   FROST's ZIP 312 is still a draft (`research/crosschain/SOURCES.md`, reddsa row). The transparent market cannot be
   used from the shielded pool without a public unshielding (`ZEC_SWAP.md:40-47`).

**The smallest role it can shrink to**, if the operator keeps it at all:

- **Opt-in, capped, never the default.** No market defaults to a wrapped quote. Every wrapped balance and price is
  labelled federation-backed. A cap set low against the bonds (`vault.bas:329-330`) bounds what collusion can take.
- **Users enter and leave through swaps where possible.** The Path 1 asset leg with wXMR as the asset lets a holder
  exit to XMR through any XMR holder, with no Burn, no Paying mark and no wait on k signers. It is a **liveness hedge
  only**: if the federation fails, nobody accepts wXMR. The federation's own Burn and Mint then settle only the
  imbalance.
- **Prerequisites before any real coin:**
  - (a) signers who are not the operator, named in the documentation (D-1); **who** they are is the whole question,
    and today the answer is nobody;
  - (b) a LIV-03 rule chosen and written into the trust statement;
  - (c) deposit-lag stranding removed, not disclosed. A deposit paid before the slowest n − k + 1 signers saw the
    address can never gather k votes and has no exit (`main.go:745-756, 808-813`); under the operator's ruling that is
    a defect. Either record the address's live foreign height on chain at Attest, so every signer credits from the same
    height, or give every uncreditable deposit a permissionless refund route. A screen notice alone does not satisfy
    the ruling;
  - (d) values for cap, capratio and wdelay, none of which exists in the repository;
  - (e) a real backend with a finality function (BL-81/82, many weeks).

**LIV-03 is a choice of who loses.** There is no free fix.
- **Keep v3.** A withdrawal a signer has marked can only be finished by k votes. If the quorum is gone, the burner is
  stranded forever.
- **v4, "Reclaim after the mark + L"** (L ≥ 20,000 blocks, **assumed**). The burner gets the units back after about
  100 h. But if the coins were in fact paid and Released never reached k, the reserve pays twice, and every other
  holder bears the shortfall.
- **v4 turns LIV-03 from a strand into theft by na − k + 1 signers** — one at na == k. That signer burns his own units,
  co-signs the payout, withholds his Released vote (`vault.bas:406-427`) and Reclaims after L, then repeats until the
  reserve is gone. Under v3, stealing the reserve takes k keys; under v4 it takes na − k + 1. The daemon rule "never
  pay within a margin of a withdrawal's Reclaim height" does not stop it, because the payout happens early.
- **v4 is bounded only with a payout circuit breaker:** every daemon stops all new payouts while any paid withdrawal
  has gone more than X blocks without k Released votes. Then the loss is at most the payouts in flight during X
  blocks. That breaker is **unbuilt**, X is unset, and it too must fit the gas budget if any part of it is on chain.
- **Against v4's risk:** when a quorum is truly lost, the reserve is lost with it, so v4 adds exposure only in the case
  where the quorum still exists but some members refuse.
- **The gas budget:** `vault.bas` installs at 19,112 of the 20,000 cap (`HISTORY.md:2593`), and the cap applies to
  every call. The change must be measured before it is promised.

**This study's recommendation:**
- **Do not deploy the federation.**
- **Do not delete the design either.** Record it as the only candidate for an XMR-denominated balance and for shielded
  ZEC, parked until someone other than the operator agrees, by name, to hold a key.
- **If it is ever built:** no recommendation between v3 and v4 without the breaker. v3 strands one burner for ever
  when a quorum is lost; v4 lowers the theft threshold from k signers to na − k + 1. v4 is preferable **only if** the
  payout circuit breaker exists, because only then is its loss bounded; with the breaker it restores what
  `vault.bas:4-9` claims. Both are set side by side in decision 12.

This is operator decision 12.

---

## 4. The build sequence

Each step says what it costs and what network proof it needs before it can be called done. Effort figures are
estimates (**assumed**). No step adds a dependency that the repository has not already justified in writing.

| Step | What | Why this order | Cost | Done means |
|---|---|---|---|---|
| **0** | **Correct the documents** (§2.0): 20 confirmations, t1 + 420, ≈48 min, the proof's timings in `XMR_SWAP.md` and `XMR_SWAP_BUILD.md`; name the censorship, maker-liveness, LIV-07 and remote-node assumptions in `XMR_SWAP.md` §10; correct Z4 and `BACKLOG.md:143`; mark XMR-01 mitigated and XMR-06 open; correct the stale comment above `refundBlind` (`tools/xmrswap/swap.go:973-979`), which still describes whole-contract reads; add the gas bootstrap, the NFA creator's Update power, the TELA publisher key and the factory owner key to the trust statements | the numbers the operator signs off on must match the code | a day; documentation and one code comment | a review against this note |
| **1** | **Harden the Path 1 companion:** the gate deadline G with Bob's margin ≥ G (U2), XMR-06 and the stable wait (U3), code pinning and lower-case hashes (U4), the exit counted at the stable Claim (U6) | each closes a hole with no happy-path cost, and all are the prerequisite for anyone trusting the next step | 1-2 weeks | unit tests, including a fake-chain hook that holds a Claim in the pool and replaces blocks; no such hook exists today (the XMR-06 finding, `FINDINGS_2026-09-10.md:896`) |
| **1b** | **`htlc.bas` v2 (U13), only if decision 17 adopts it:** the final maker window from t2 + G2 with s_b revealed, and lower-case hex checks on ha, hk and hb | LIV-07 must be closed before anyone locks real value, and the contract is undeployed, so a redeploy costs nothing today; step 2's stagenet run must exercise the contract that will ship | days (assumed) | DVM tests: `TestHtlcPastT2NeedsTheTakerOrTheFundsNeverMove` inverted (the maker recovers at t2 + G2 and the contract is empty), a Lock refused for upper-case or non-hex hashes; a new canonical code hash for U4 |
| **2** | **The asset leg and the stipend, on stagenet** (U1, §2.0.1, U9) | the best security bought per week: no contract change beyond step 1b, if adopted; it makes "XMR into a token with no custodian" real and gives wrapped units an exit that needs no federation | ≈3 weeks | (a) simulator: a real launched token, all three exits, the contract's asset balance read back after each, a refused asset Lock at ring 2 that returns the asset, a stipend to a freshly registered empty wallet that then Claims, the `htlc.bas` Claim estimate measured (it sizes the stipend), the look-alike check refusing a same-name asset, and an NFA with `currBidAddr` set refused; (b) **Monero stagenet**: happy, refund and punish exits with an asset leg against a real `monero-wallet-rpc`, every txid recorded in `tools/xmrswap/testdata`, using the audit's recipe (`FINDINGS_2026-09-10.md:2068`). The same run is the real-software test of XMR-2's sweep wait (`FINDINGS_2026-09-10.md:184`; `swap.go:46-52`) |
| **3** | **One mainnet profile** (BL-27): T2 − T1 (U5), T1Max, the upward confirmation tiers (U7), G, k, the stipend's floor | policy in one place, so the app and the companion quote the same numbers | days | the profile checked by a test, including the rule t1 − lock ≥ stable wait + expected Monero path at the tier + G + k·σ, capped at T1Max, at every tier (§2.1 U7: ≈310 blocks at 20 confirmations, ≈390 at 30, ≈470 at 40, at G = 100 and k = 2); values from operator decisions 4-6 |
| **4** | **The proof-xmr audit and a second verifier** (U8, BL-23) | the largest trust item left before mainnet | unknown money; a moderate amount of work for the verifier | the auditor's report published; the two verifiers agree on the pinned vectors |
| **5** | **The ZEC market:** Z-A (client, watcher, broadcast, the note's rules in code, the maker's cutoff), Z-B (the pre-signed refund at several fees), Z-C/Z-D (`swap.bas` v2), Z-E (`board.bas` v3) | Z-C must exist before any mainnet `swap.bas` SCID; Z-B removes the one total-loss state that depends on the taker being alive | 5-7 weeks | **Zcash testnet (BL-42)**, every txid recorded: fund, the maker's claim with s, the DERO Claim sent by a third address and paid to the taker; a refund broadcast from a machine other than the one that signed it; an early refund refused; a late maker claim losing the race to a refund at cltv |
| **6** | **Discovery and a channel between strangers** (BL-12, BL-10). Discovery must be on chain, or run by a named third party; the operator hosts none of it. Evaluate `board.bas` for XMR offers before adopting spore groups: Post already takes a `chain` string of up to 16 characters and a `wantAddr` of up to 128 (`contracts/board.bas:31, 36`), so it could carry an XMR offer with no new contract, and it needs no new dependency. Its fields were shaped for the ZEC market (one hash, an Accept with a refund key hash and cltv, `board.bas:60`), so the fit must be checked, or a board v3 serves both markets. Accept needs gas (Path 0) | without them, "exit time" includes "find someone", and a first-time holder cannot talk to a maker | medium | a stranger finds an offer on chain and completes step 2's run over spore |
| **7** | **Standing makers** (U10) | the only thing that removes the wait for a counterparty | theirs, not DMZ's | a named third party runs one; if none exists at launch, the app says so |
| — | **The federation** | parked (§3) | — | nothing until operator decision 12 |

---

## 5. The decisions only the operator can make

1. **Adopt the swap track and park the federation.** The options:
   - (a) as recommended. Cost: no XMR-denominated balance at launch; shielded ZEC has no path.
   - (b) keep the federation for launch. Cost: named signers are needed first, LIV-03 must be decided, a backend that
     does not exist must be built, and custody is in the product.
2. **The float between trades.** The options:
   - (a) DERO or tokens. Trustless and fast, but the holder carries DERO or token price, which contradicts "DERO only
     as gas".
   - (b) wXMR or wZEC. Keeps XMR exposure, but it is custody, and it needs decision 1(b).
   - (c) a swap for every trade. No exposure and no custody, but ≈48 min (XMR) or ≈21 min (ZEC) per trade.

   Speed does not decide this; trust does. The recommendation: offer (a) and (c) and say plainly what each carries.
3. **The first gas.** The options:
   - (a) the stipend as a protocol step, after the maker's Lock is stable and before the XMR lock, sized at 0.12 DERO
     plus 4 × the Claim estimate, paid by makers and priced into their spread. A first setup travels off-DERO, or with
     a small rate-limited setup budget from the maker. Named cost: farming is profitable. Every walk pays the walker
     the stipend, and the maker has no way to tell a newcomer from a sybil, so a standing maker should expect to lose
     one stipend per walker. It also adds ≈1-3 min to a holder's first trade.
   - (b) holders bring their own DERO. Cost: an unnamed custodial on-ramp in front of the product, and the "no
     custodian" claim withdrawn for newcomers.
4. **Monero confirmations.** The options:
   - (a) 20 as the lowest default, with tiers upward by notional. Cost: +20 min per 10 confirmations for large trades.
   - (b) also allow a maker to quote down to 10, disclosed. Cost: a maker carries reorganisation risk against a 23-28 %
     miner; saves ≈20 min. Only after BL-30.

   The recommendation is (a).
5. **The gate deadline G.** 100 blocks is **assumed**. A larger G makes withholding a Claim costlier, and makes an
   honest but slow maker's trade more likely to end in a refund (time, not coins).
6. **The maker's refund window T2 − T1.** The options:
   - (a) 400 (≈2 h), as today;
   - (b) up to 1,200 (≈6 h).

   (b) protects exiting holders from losing to Punish through an ordinary absence. It costs a taker whose maker
   vanished up to ≈8.1 h of waiting to Punish.

   A second choice sits beside it: **the taker's ceilings T1Max and T2Max**, today 1,200 each (`swap.go:44-45`).
   - (a) keep them. Cost: a maker who names the ceiling holds a ≈5 h free option (≈1.1 %, assumed volatility), and the
     taker's XMR can be committed ≈6 h before she has an exit, with t2 up to 12 h out.
   - (b) lower T1Max toward the default t1 plus slack (for example 420 + the U7 tier's extra blocks). Cost: a maker
     who needs a longer t1 for a large, high-confirmation trade must raise the tier's T1Max in the profile explicitly.
7. **Fund the proof-xmr audit** as the gate before a mainnet XMR swap. The price is unknown to this project.
8. **Count a swap exit as done at the stable Claim** (≈48 min + scan) rather than after the sweep and 10 more
   confirmations (≈70 min). The recommendation is yes: ownership is complete at that point. The first spend still
   waits for the joint wallet's restore and scan, which is unmeasured (XMR-2, BL-11), so ≈48 min is a lower bound.
9. **The ZEC redeploys and the first ZEC gas.**
   - Redeploy `swap.bas` v2 (a Claim anyone may send, paying the stored taker, with a minimum width) and `board.bas` v3
     (the acceptance deadline) before any mainnet SCID. They are new SCIDs because the contracts are immutable in
     practice: none calls `UPDATE_SC_CODE` (`dvm_functions.go:75`).
   - For a first-time ZEC taker, choose: (a) an off-chain request with the maker locking by name, which bypasses the
     board and costs a channel on the first trade; or (b) the taker brings DERO for `Accept`.
10. **Handing the pre-signed Zcash refund to third parties.** It is safe for the funds and leaks the swap to whoever
    holds it. The recommendation: allow it, and say so on screen.
11. **The merged ZEC escrow with burned bonds** (D-7). Only after testnet. Accepting it means accepting that a maker can
    burn an honest taker's bond.
12. **The federation, if kept.** Four things to settle:
    - name its signers (D-1);
    - choose between, side by side:
      - v3: a withdrawal a signer has marked is stranded for ever when the quorum is lost; stealing the reserve takes
        k keys;
      - v4: the burner Reclaims after the mark + L; the reserve bears double payments, stealing it takes na − k + 1
        keys (one at na == k), and the loss is bounded only if a payout circuit breaker is built;

      either within 888 units of gas headroom;
    - set cap, capratio and wdelay;
    - decide whether a wrapped unit may ever be a market's quote.

    Otherwise, record Path 3 as not deployable.
13. **Makers.** Who runs them. The operator cannot, under the no-hosting ruling. If nobody is named at launch, the app
    and this note say "exit time = confirmations + time to find a maker, and no maker exists yet".
14. **The canonical code list, and who signs the companion.** Who publishes the canonical code hashes of `htlc.bas`,
    `swap.bas` and `board.bas` once the operator has revoked their own access. The recommendation: they are compiled
    into a reproducible, signed companion build (BL-14) and printed in this repository, so nobody has to be online to
    vouch for them. That leaves who signs the build and where it is downloaded. Today the only publisher is the
    operator's own repository (`github.com/NoahMoney/DMZ`), which the revocation ruling removes. The options:
    - (a) a named third party signs and hosts releases. Cost: that party is a trusted party on every path.
    - (b) nobody is named: the build is frozen at the last signed release, reproducible from a tagged commit, with its
      hash printed here and its download location stated. Cost: no fix after revocation reaches a holder except
      through a new, separately trusted publisher.
15. **The liveness ruling and the swap track.** No cross-chain swap can meet LIVENESS BY CONSTRUCTION as written: each
    party must be online, or have a machine or delegate online, at stated heights. The options:
    - (a) accept per-party protocol windows as the one exception, each named on screen with its height, its wall-clock
      time and its loss: the maker in [t1, t2) (the locked asset); the ZEC taker's refund between cltv and the maker's
      spend (the whole ZEC leg); the Monero taker's Punish until LIV-07 is fixed (the maker's asset, frozen). U9's wake
      scheduling and Z-B's broadcast-by-anyone make each window "have a machine on at a stated time", not "stay awake".
    - (b) rule the swap track out as well. Cost: with the federation also parked, DMZ has no XMR or ZEC path at all.
16. **Remote nodes.** Whether the product targets holders who run their own Monero, Zcash and DERO nodes, or discloses
    remote-node trust on the first screen. The options:
    - (a) full-node holders only. Cost: most arriving holders cannot use it.
    - (b) allow remote nodes, and say on the first screen that whoever runs the node can cause a total loss that
      nothing in the protocol can detect (R18, Z14, Z15). Cost: a trusted party the holder chooses, named.
17. **`htlc.bas` v2 (U13, LIV-07).** The options:
    - (a) redeploy before any mainnet SCID, with a final maker window from t2 + G2 in which the maker reveals s_b, and
      the lower-case hex checks. Cost: the taker's Punish gains a deadline; a new canonical hash.
    - (b) the audit's variant, where the maker takes the lock back with no secret. Cost: a taker who misses the window
      loses her XMR for good.
    - (c) keep v1. Cost: every lock a taker abandons past t2 is frozen for ever, and a taker who never locked XMR can
      destroy a maker's lock at no cost; the trust statement must say so.

    The recommendation is (a), and the operator decides.

---

## 6. Rejected, and why

- **Adaptor signatures on the DERO side.** The DVM cannot verify a signature or do curve arithmetic
  (`dvm_functions.go:58-94`), so the DERO leg stays a hash lock. The Monero side already uses the key-share half of the
  COMIT/Farcaster construction. An adaptor needs a scripted chain that verifies it, and here that chain is DERO, which
  cannot. Adaptors do not remove the SHA-256-to-ed25519 proof; they would move it. Only an audit removes the proof's
  trust (BL-28).
- **ECDSA adaptors on transparent Zcash.** They buy privacy for H and s on a pool whose amounts and addresses stay
  public. They cost a new, unaudited SHA-256-to-secp256k1 proof and a channel before funding. The pre-signed refund
  (Z-B) gets the liveness gain with no new cryptography.
- **A watchtower or delegated Refund on Path 1.** A tower holding s_b can combine it with the s_a a later Claim
  publishes and sweep the maker's XMR. And a pre-signed DERO Refund expires within 11 blocks (`miner_block.go:43`).
- **A relayed or sponsored Claim on Path 1.** A relayer holding s_a can hold the Claim back until Bob refunds, then sweep
  the XMR. With Bob as the relayer, he can sweep at once and refund later.
- **Bonds on the XMR swap, and a taker bond with the gate on chain (htlc v2).**
  - A refund cannot be attributed to either side (`XMR_SWAP.md:147-153`).
  - With the gate on chain, attack A turns the bond into the maker's profit (§2.1).
  - An arriving holder has no DERO to bond with.
- **An up-front option premium paid in XMR or ZEC.** The taker would have to trust the maker with the premium. That is a
  small custodial hop inside a path that has none.
- **The anti-spam deposit, eigenwallet style.** "The maker can, in rare circumstances, choose to withhold" part of it,
  and a taker whose deposit is withheld is told to "reach out to the maker on Discord or Matrix"
  ([eigenwallet docs](https://raw.githubusercontent.com/eigenwallet/core/master/docs/content/advanced/anti_spam_deposit.mdx)).
  That is a trusted party, and a state a human must leave. It breaks both binding rulings.
- **A board-held taker bond paid to the maker.** The board cannot see the escrow, so the maker would be the judge
  (`ZEC_SWAP.md:162-164`).
- **Shortening T_dero** once the Zcash refund is pre-signed. T_dero also covers the taker's DERO Claim after a late maker
  claim (§2.2).
- **Child-pays-for-parent to rescue a stuck pre-signed refund.** ZIP 317's block template has no package logic, and a
  child needs the taker online, which defeats the point. Sign at several fees instead.
- **Cutting Monero confirmations below 20 as a default.** It saves ≈20 min. It moves reorganisation risk onto the maker,
  on a chain with no bound and a 23-28 % miner (§1). Below 10 it is impossible anyway (`swap.go:594-596`,
  `cmd/xmrswap/main.go:82-83`).
- **Confirmation checks inside `vault.bas`, and any SPV, optimistic or light-client bridge.** The DVM has no RandomX,
  Equihash, BLAKE2b or Keccak and no cross-contract reads, and every call is capped at 20,000 storage gas. A fraud proof
  has nothing on chain to judge it.
- **An HTLC-settled or atomic federation withdrawal.** It needs either a single scalar holder who steals, or MPC over
  SHA-256 (§2.3). It would also need a vault redeploy with 888 units of gas headroom.
- **Paying slashed federation bonds to victims.** Slashing needs k votes, and k colluders will not slash themselves.
- **An oracle-backed synthetic XMR.** DMZ has no oracle, and an oracle is a trusted party.
- **Atomic XMR-to-token in one transaction.** `htlc.bas` cannot call `pool.bas`. The asset leg (U1) reaches the same
  result as a single swap.
- **A maker pre-locking before a taker exists.** `htlc.bas` Lock binds the taker and three per-swap hashes
  (`htlc.bas:26`). A late-binding Accept would need its own 144 s stability wait, so nothing is saved.
- **Payment channels on DERO for repeated trades.** Closing a channel means checking a signed state, which the DVM cannot
  do.
- **A per-swap `htlc.bas` install as the default.** The taker would have to verify an unknown SCID's code byte for byte.
  Keyed reads (§2.0) already neutralise XMR-01 on the shared contract.
- **An operator-run maker, relay, watcher or signer.** It breaks the no-hosting ruling.
- **The browser or a phone as a swap party.** Neither can hold `monero-wallet-rpc` or a Zcash node, and the browser holds
  no keys by rule (`XMR_SWAP.md` §11).
- **Calling the federation "the fast path".** At equal depth its entry is not faster. Its exit lead depends on
  wdelay = 0 and a signing round that has not been built.

---

## 7. Sources

**In this repository** (as at `f680069`):
- **Contracts:**
  - `contracts/htlc.bas:26-136` (Lock, Claim, Refund, Punish; the length-only hash check at `:31`);
  - `contracts/swap.bas:24-103` (the lower-case check `:36`, Claim `:62-84`, Refund `:87-103`);
  - `contracts/board.bas:31-91`;
  - `contracts/vault.bas:4-9, 101-111, 313-350, 387-390, 406-485, 565`;
  - `contracts/nfa.bas:93-107`;
  - `contracts/pool.bas:136-191`.
- **The Monero companion:**
  - `tools/xmrswap/swap.go:22-52, 95-110, 353-356, 594-596, 916-919, 973-980, 1068-1071, 1075, 1133-1138, 1307, 1349-1356, 1435-1441`;
  - `tools/xmrswap/dero.go:122, 185, 264, 274, 411, 442-446, 469, 659, 699-754, 761-764, 774, 804, 821-843`;
  - commit `9064f82` (2026-09-10), which introduced the keyed reads;
  - `tools/xmrswap/state.go:76`;
  - `tools/xmrswap/adversary_test.go:215, 288, 354`;
  - `tools/xmrswap/cmd/xmrswap/main.go:82-83`.
- **The Zcash companion:** `tools/zecswap/script.go:120-122, 324-326, 387-425`; `tools/zecswap/sighash.go:22-24, 255`.
- **The vault daemon:** `tools/vaultsigner/main.go:53, 121-145, 388-395, 745-756, 790, 808-813, 902-910`;
  `tools/vaultsigner/networks.go:16-17`.
- **Tests:** `tests/htlc_test.go:330-337, 386-421`; `tests/swap_test.go:71-94`.
- **Vendored derohe:**
  - `vendor/derohe/config/config.go:32, 44, 47, 52, 77`;
  - `vendor/derohe/blockchain/transaction_verify.go:230-243, 265-276`;
  - `vendor/derohe/blockchain/transaction_execute.go:395-404, 424-443`;
  - `vendor/derohe/blockchain/mempool/mempool.go:377`;
  - `vendor/derohe/blockchain/miner_block.go:43, 229-282`;
  - `vendor/derohe/cmd/derod/rpc/rpc_dero_getsc.go:97-110`;
  - `vendor/derohe/blockchain/blockchain.go:483-486, 748`;
  - `vendor/derohe/dvm/dvm_functions.go:58-94`;
  - `vendor/derohe/dvm/sc.go:244-251`;
  - `vendor/derohe/walletapi/wallet_transfer.go:132-150`;
  - `vendor/derohe/cmd/simulator/simulator.go:588-589`;
  - `vendor/derohe/cmd/dero-wallet-cli/easymenu_post_open.go:127-129`.
- **Documents:**
  - `docs/XMR_SWAP.md:38, 43-50, 147-153, 170, 178, 242-246, 402-407, 449-461, 516-518, 592`;
  - `docs/XMR_SWAP_BUILD.md:4, 249-269`;
  - `docs/ZEC_SWAP.md:27-47, 103-105, 137-143, 162-172, 204-229, 246, 260, 270-273, 277-285`;
  - `docs/BACKLOG.md:54-66, 71, 89-91, 96, 107, 110, 117, 143`;
  - `docs/audit/FINDINGS_2026-09-10.md:8, 41, 170, 184, 240, 296, 310-321, 324, 338-349, 380-391, 394, 408, 649,
    831-845, 845, 887-898, 1111, 2068` (LIV-01, NFA-1, XMR-2, DVM-1, LIV-02, LIV-03, LIV-04, XMR-01, SEC-K-01,
    SEC-K-02, SEC-K-03, APP-1, LIV-07, LIV-08, XMR-06);
  - `docs/history/HISTORY.md:801, 2593, 2766, 2779-2783, 2980-2988, 3041-3048`.
- **Contracts, added:** `contracts/htlc.bas:94-136` (Refund and Punish, LIV-07); `contracts/board.bas:31, 36, 60`
  (Post's `chain` field, Accept); `contracts/nfa.bas:211, 296-307` (`currBidAddr`).
- **Research:** `research/crosschain/poc/proof-xmr/run.log`; `research/crosschain/RESEARCH.md:129, 204-205`;
  `research/crosschain/SOURCES.md` (the project's reading list, including the Farcaster RFCs, Gugger, ZIP 244 and ZIP
  317).

**External, primary:**
- Monero `src/cryptonote_config.h`: `DIFFICULTY_TARGET_V2 120`, `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE 10`, and no
  reorganisation bound. <https://raw.githubusercontent.com/monero-project/monero/master/src/cryptonote_config.h>
- ZIP 208 (the 75 s block target): <https://zips.z.cash/zip-0208>
- zcashd `src/main.h` and `src/consensus/consensus.h` (`MAX_REORG_LENGTH = COINBASE_MATURITY − 1 = 99`), read by the
  ground pass: <https://github.com/zcash/zcash>
- ZIP 203 (`nExpiryHeight = 0` means no expiry): <https://zips.z.cash/zip-0203>
- ZIP 244 (non-malleable transaction ids; the sighash commits to the branch id): <https://zips.z.cash/zip-0244>
- ZIP 317 (the conventional fee is 5,000 zatoshi × max(2, logical actions); the block template picks transactions
  individually, weighted by fee ratio): <https://zips.z.cash/zip-0317>
- BIP 65 (`OP_CHECKLOCKTIMEVERIFY`): <https://github.com/bitcoin/bips/blob/master/bip-0065.mediawiki>
- R. Han, H. Lin, J. Yu, *On the Optionality and Fairness of Atomic Swaps* (a swap is an American call option without
  the premium; the premium for cryptocurrencies is 2-3 %): <https://eprint.iacr.org/2019/896>
- *Qubic selfish mining on Monero*, full text (23.38 % average share, 28.33 % in selfish periods; no single maximum
  reorganisation depth stated): <https://arxiv.org/html/2512.01437>
- COMIT xmr-btc-swap `swap/src/env.rs` (mainnet: 10 Monero finality confirmations, 1 Bitcoin, cancel and punish
  timelocks of 72 blocks each; verified by two judges):
  <https://raw.githubusercontent.com/comit-network/xmr-btc-swap/master/swap/src/env.rs>
- eigenwallet, *Anti-spam deposit* (funds locked "up to 36h"; a withheld deposit is settled by contacting the maker):
  <https://raw.githubusercontent.com/eigenwallet/core/master/docs/content/advanced/anti_spam_deposit.mdx>
- The 18-block Monero reorganisation: **secondary sources only** (`docs/XMR_SWAP.md:170`;
  <https://www.theblock.co/post/370628/monero-shaken-by-block-reorg-reviving-tensions-with-qubic>).

**Assumed, and marked where used:**
- 6 % daily XMR/DERO volatility;
- the 0.4σ option approximation;
- the exponential model of block arrivals;
- G = 100;
- R ≥ 20;
- W = 200;
- L ≥ 20,000;
- that DERO miners run the reference block template (which fills blocks by fee per byte);
- every effort estimate in §4;
- the `htlc.bas` Claim estimate, and so the stipend beyond its 0.12 DERO floor;
- the ≈18-60 s spendability wait for a stipend;
- that a wallet transfer to an unregistered address fails like a contract's;
- k = 2 and G2 in the t1 rule and U13;
- X, the circuit breaker's threshold.
