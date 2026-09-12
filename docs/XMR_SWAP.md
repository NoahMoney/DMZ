# DERO ↔ Monero trustless swap — what it is, what can go wrong, and what is trusted

**Written 2026-09-08 against `docs/XMR_SWAP_BUILD.md` (the build specification; its §1 is the protocol this page
describes).** This is the page a user reads before swapping and a reviewer reads before accepting the word
"trustless". Every sentence here is one of three things, kept apart: a rule that a named file enforces, a number
with a primary source (§12), or an open question marked OPEN. Nothing on this page is running until
`tools/simhtlc` has driven all three exits on the simulator (build spec §5–§6), and the screen in §11 is a design,
not a shipped page.

---

## 0. In one paragraph

Bob has DERO and wants XMR. Alice has XMR and wants DERO. Bob locks his DERO in a contract on DERO
(`contracts/htlc.bas`) that pays Alice only if she reveals a secret `s_a`, and pays Bob back only if he reveals a
secret `s_b`. Alice sends her XMR to a Monero address whose spending key is `s_a + s_b` — a key nobody holds.
When Alice takes the DERO she reveals `s_a` on the DERO chain, so Bob can now form `s_a + s_b` and take the XMR.
If Bob takes his DERO back instead, he reveals `s_b`, so Alice can form the same key and take her XMR back. At no
moment does a third party hold anything: the DERO sits in a contract that has no owner, and the XMR sits under a
key that exists only when the protocol says it should. What each party trusts, exactly, is §10.

---

## 1. The protocol in plain words

### 1.1 Who is who, and why the order is fixed

The roles are those of the Bitcoin–Monero swap protocols (COMIT / eigenwallet; the Farcaster RFCs) with DERO in
the scripted seat. **Bob has DERO and locks first. Alice has XMR and locks second.** The order is not a choice.
A DERO contract can check a hash and a block height; Monero can check nothing but its own signatures. So the
enforceable lock has to exist before the unenforceable one, and every condition of the swap is written into the
DERO contract. Whoever locks second holds an option for as long as the first lock stands; that is §3.

### 1.2 Setup, off chain, over the rendezvous channel (§8)

| Who | Generates | Sends to the other |
|---|---|---|
| Alice | scalar `s_a` (32 bytes, < ℓ), `S_a = s_a·G` (ed25519), `H_a = SHA256(s_a)`, proof `π_a` of "SHA256(x)=H_a ∧ x·G=S_a"; the joint address's **view key pair** `(v, V)` | `S_a, H_a, π_a, V, v` (the private view key is shared: both must see the lock, neither can spend with it), her DERO address |
| Bob | `s_b, S_b, H_b, π_b` likewise; **gate secret** `k_b` (32 random bytes), `H_k = SHA256(k_b)` | `S_b, H_b, π_b, H_k` (never `k_b` yet) |

Three things in that table carry the whole design.

**The proof `π`.** Each party proves, without revealing the secret, that the value whose SHA-256 hash the DERO
contract will check is *the same value* as their share of the Monero key. Without `π_a`, Alice could take the
DERO by revealing a value that hashes correctly but is not her key share; Bob would then hold `s_a' + s_b`, which
opens nothing, and the XMR would be locked forever. Without `π_b`, Bob could refund with a non-share and leave
Alice's XMR locked forever. The proof is `research/crosschain/poc/proof-xmr`: a Bulletproofs rank-1 constraint
system with no trusted setup, 37,460 multiplication gates, measured at 8.51 s to prove and 0.74 s to verify, plus about 1.6 s each time to
build the generators, so about 10.1 s and 2.3 s from end to end; 1,601 bytes (`run.log`). Each party verifies the other's proof before anything is locked (build spec §4,
invariants 3 and 4).

**The joint address.** Spend public key `P = S_a + S_b`, view public key `V`, encoded as a standard Monero
address (network byte ‖ P ‖ V ‖ keccak256(…)[0:4], Monero base58 — `src/cryptonote_basic/cryptonote_basic_impl.cpp`
and `src/common/base58.cpp` in monero-project/monero). Its spend private key is `s_a + s_b mod ℓ`. Nobody holds it
until one of the reveals in §2 happens, and after that exactly one person does.

**The gate secret `k_b`.** Without it, Alice could claim the DERO the moment Bob locked, never having locked any
XMR. Bob hands `k_b` to Alice only after his own view-only wallet has seen her XMR at the joint address with at
least twenty confirmations by default, and never fewer than ten (§4). A "claim delay" in place of the gate was reviewed and rejected on 2026-09-06: a delay
lets Bob *notice* a claim without an XMR lock; it does not let him stop it (build spec §1).

### 1.3 On chain, step by step

1. **Bob locks DERO** in `htlc.bas`: `Lock(ha, hk, hb, taker = Alice, t1, t2, asset)` with the DERO (or a
   DERO-native asset: a token or an NFA unit, U1) attached to the call. Before he does, his companion reads the
   contract's code and refuses any SCID whose code does not hash to the `htlc.bas` v3 it was built for
   (`HTLCCodeSHA256`, `tools/xmrswap/pin.go`; `swap.go` `checkDeployment`), or whose install arguments his agreed
   windows cannot meet (w1min + 11 above his t1 width, or w2min above t2 − t1), refuses a taker whose DERO address has never
   registered, and checks that his own wallet can pay the lock, the stipend and his fees. Once the lock is 8 blocks
   deep he sends Alice a **stipend** of DERO (Path 0, `swap.go:1352`): 14,016 atomic units by default, a Claim and a
   Punish at their last re-sends, so that she can pay for her own exits without buying DERO from anyone.
2. **Alice reads the lock from the chain** and checks every field against what she holds and what Bob committed
   to over the channel — `ha` is the hash of her own `s_a`; `hk` is the `H_k` Bob sent; `hb` is the `H_b` Bob sent
   and `π_b` verified against it; `taker` is her address; `t1 − now ≥ T1_MIN`; `t2 − t1 ≥ T2_MIN`; the amount and
   asset are what was agreed (build spec §4, invariant 3). Only then does she **send XMR to the joint address**.
   Her companion also refuses to lock until the maker's stipend is in her own wallet, at least the agreed amount, in
   a block DERO can no longer replace, and she can spend at least that much (`swap.go:1402`).
3. **Bob watches the joint address** through a view-only wallet (`generate_from_keys` with the view key and no
   spend key). At twenty confirmations by default (never fewer than ten) of at least the agreed amount, read from his
   own wallet and not from anything Alice says, he sends Alice `k_b` over the channel (invariant 2). He releases it
   only while `height + G + 2 < t1`, and Alice ignores a `k_b` that reaches her at or after `t1 − G` (U2, G = 100 by
   default, `swap.go:64`), so that a Bob who hands the gate over late cannot count on suppressing her Claim.
4. **Alice claims** the DERO: `Claim(id, s_a, k_b)`. The contract stores `s_a` under `xa_<id>`, where anyone can
   read it.
5. **Bob reads `s_a`** from the contract, forms `s_a + s_b`, imports the joint wallet with that spend key
   (`generate_from_keys`, this time with the spend key) and **sweeps** the XMR to his own address (`sweep_all`).

### 1.4 The names the contract uses

For each lock `id`, `htlc.bas` stores `m_<id>` (maker, Bob), `t_<id>` (taker, Alice), `ha_<id>`, `hk_<id>`,
`hb_<id>` (the three hashes, 64 hex characters each), `a_<id>` (asset, empty for DERO), `v_<id>` (amount),
`t1_<id>`, `t2_<id>` (absolute DERO heights), `st_<id>` (0 open, 1 claimed, 2 refunded, 3 punished, 4 reclaimed),
and after a reveal `xa_<id>` (`s_a`) or `xb_<id>` (`s_b`; a Reclaim stores it where a Refund does). The two revealed
keys never coexist for one lock, because every exit requires `st == 0` and sets it to something else (build spec
§3.A). That mutual exclusion is the whole safety argument of §2 in one line. Since v3 the contract also stores its
three install arguments, `w1min`, `w2min` and `g2` (MT-A03(a), MT-J13): the same code installed with other values
hashes the same, so each companion reads them with the code and refuses an install outside its policy.

---

## 2. The four exits, and who ends with what

| Path | Condition | Who calls | Reveals | Result |
|---|---|---|---|---|
| **Claim** | height < T1, `SHA256(s_a) = H_a`, `SHA256(k_b) = H_k`, caller is the taker | Alice | `s_a` | Alice: DERO. Bob: XMR (via `s_a + s_b`). The swap. |
| **Refund** | T1 ≤ height < T2, `SHA256(s_b) = H_b`, caller is the maker | Bob | `s_b` | Bob: DERO back. Alice, if she locked: XMR back (via `s_a + s_b`). |
| **Punish** | T2 ≤ height < T2 + g2; **anyone** may send it, and it pays the taker (`htlc.bas` v2); refused from T2 + g2 (v3) | Alice, or anyone | nothing | Alice: the DERO. Bob: nothing — he had [T1, T2) to refund and did not. Any XMR Alice locked stays under the joint key forever (she is paid in DERO; Bob cannot take it without `s_a`). |
| **Reclaim** | height ≥ T2 + g2, with no end; `SHA256(s_b) = H_b`, caller is the maker (`htlc.bas` v3) | Bob | `s_b`, in `xb_<id>` as a Refund stores it | Bob: DERO back. Alice, if she locked: XMR back (via `s_a + s_b`), with no deadline. Nobody punished in time. |

**Claim is the trade.** Alice has the DERO she wanted; Bob has the XMR he wanted, at the price they agreed. There
is no better outcome, because this *is* the outcome both asked for. Bob's sweep has no deadline: `s_a` sits in
the contract forever, `s_b` is his, and nobody else can ever spend the joint output, so a Bob who crashes after
Alice's claim reads `s_a` when he comes back.

**Refund is "nothing happened", made safe for both.** Bob has his DERO back less two transaction fees. If Alice
never locked XMR, she has lost nothing but time. If she did lock XMR, `s_b` is now on the chain and she recovers it
with `s_a + s_b`, again with no deadline. This is the best result available once the trade did not complete,
because the DERO contract cannot know whether Alice ever locked XMR — it can only make Bob's exit *compatible* with
her recovery, which revealing `s_b` does. A refund path that revealed nothing would let Bob take his DERO back
while Alice's XMR stayed locked under a key she cannot form; that is the failure the reveal removes.

**Punish is the price of doing nothing.** Alice takes the DERO. Bob gets nothing. If Alice locked XMR, it is now
under a key that nobody will ever hold: Alice has `s_a`, Bob has `s_b`, neither is revealed, and nothing forces
either to reveal it. Why this is the right outcome and not a cruelty: without Punish, a Bob who neither released
`k_b` nor refunded would leave Alice with locked XMR and no DERO, at no cost to himself. With Punish, "do nothing"
costs Bob his DERO, so a rational Bob refunds within his 400-block window — and a refund reveals `s_b`, which is
what Alice needs. Alice, in the punish case, ends with exactly the DERO she agreed to buy; her XMR is gone, but it
was the XMR she agreed to sell. The only party who ends worse than the agreed trade is the one who did nothing.
The XMR itself is destroyed for everyone; that is the cost of a Monero side that can enforce nothing, and it is
paid only by a Bob who chose it.

**Anyone may Punish, in the taker's favour, for `g2` blocks; then the lock goes back to its maker.** In `htlc.bas` v1
only the taker could send Punish, and Claim and Refund have both closed by t2, so a lock she never punished — key
lost, walked away, or worth less than her fee — was frozen for ever (LIV-07). In v2 Punish pays the stored taker
`t_<id>` whoever sends it: the signer's only role is to be a valid ring-2 signer, and the payment goes to
`LOAD("t_" + id)` (`contracts/htlc.bas`, Punish). Punish carries no secret, so letting anyone send it changes who
may press the button and nothing else: it refuses attached value and pays only the taker. v3 (MT-J13(b),
`docs/ATOMIC_SWAP_STUDY.md` §5 decision 17 (a)) gives Punish an end: it is open in [t2, t2 + g2) only, `g2` being an
install argument (4,800 blocks, 24 h, proposed and pending the operator), and from t2 + g2 the lock is the maker's
to **Reclaim** with `s_b`, which the contract stores in `xb_<id>` as a Refund does. Bob therefore loses the lock only
to a timely Punish, and his companion sends none of its own (under v2 it did, at t2 + 20, because the DERO was lost to
him at t2 either way); it Reclaims from t2 + g2 instead, and counts the Reclaim only once it is stable, sending it again
if a replaced block undoes it. Alice's companion treats st 4 exactly as st 2: it reads `s_b` and recovers her XMR, with
no deadline. So the lock always has an exit, and from t2 + g2 it waits only on the party it pays, the maker; what that
means for Alice's XMR is the next paragraph (`tests/htlc_v3_test.go`, `TestHtlcV3Reclaim`, `TestHtlcV3WindowMatrix`;
`tests/zzadversarial_liveness_test.go`, `TestHtlcPastT2TheLockAlwaysHasAnExit`; `tools/xmrswap/swap_v2_test.go`,
`TestMakerReclaimsALockNobodyPunished`; `tools/xmrswap/htlcv3_test.go`, `TestMakerWatchesHisReclaimUntilStable`).

**What v3 costs the taker: her Punish has a deadline, t2 + g2, and past it her XMR depends on Bob.** Her companion
sends Punish at once from t2, counts it only once it is stable, and sends none from t2 + g2 − (11 + 8 + margin): DERO
mines a transaction at most 10 blocks after the height it was built at (`vendor/derohe/blockchain/blockchain.go:748`,
`miner_block.go:275`), the companion counts 11, one block conservative, and a Punish that executes at t2 + g2 is mined,
refused and keeps its fee. Anyone may send Punish: `htlc.bas` asks only for a valid ring-2 signer, and the call carries
no secret and pays only the stored taker, so someone she trusts, or a second machine of hers, can send it for her. Her
recovery card says so, prints t2 and the window's last block with their clock times, and registers a second wake
before t2 (`tools/xmrswap/recovery.go`). If nobody punishes in time, her XMR comes back only if Bob Reclaims, which
reveals `s_b` (`htlcv3_test.go`, `TestTakerPastHerPunishCutoffWaitsForTheReclaim`). Nothing makes him: a Bob who never
Reclaims — hostile, or having lost his state or `s_b`, a maker the contract's own header says cannot then leave —
leaves her XMR under the joint key and his DERO in the contract, both for ever. Under v2 she could not be left with
neither: she could Punish at any time after t2 and take his DERO (her XMR stayed under the joint key either way). This
dependence on Bob is new in v3, and only a Punish before t2 + g2 removes it.

### 2.1 What each party can lose, by state reached

| State reached | Alice | Bob |
|---|---|---|
| Bob locks; Alice never locks | nothing (she walked; §3) | two fees, ≥ 400 blocks of time |
| Alice locks; Bob never releases `k_b`; Bob refunds at T1 | Monero fees, ≥ 400 blocks with XMR locked; then XMR back | two DERO fees, the time |
| Alice locks; Bob releases `k_b`; Alice claims | nothing | nothing: the trade |
| Alice locks; Bob releases `k_b`; Alice never claims; Bob refunds at T1 | Monero fees; XMR back after the refund | two DERO fees, the time |
| Alice locks; Bob neither releases nor refunds; Alice punishes at T2 | her XMR is gone; she holds the DERO she bought | the DERO |
| Alice locks; Bob neither releases nor refunds; nobody punishes in [T2, T2 + g2); Bob reclaims | Monero fees, XMR locked until the reclaim; then XMR back | two DERO fees, the time from T1 to T2 + g2 |
| Alice locks; Bob neither releases nor refunds; nobody punishes in [T2, T2 + g2); Bob never reclaims (hostile, or he lost his state or `s_b`) | her XMR locked under the joint key for ever, and no DERO. New in v3 (under v2 she could Punish at any time); only a Punish before T2 + g2, hers or anyone's for her, prevents it | the DERO, locked in the contract for ever |
| Alice locks; Bob refunds at T1 but Alice's `s_a + s_b` never works | impossible if `π_b` verified (§9, R14) | — |
| Alice claims but Bob's `s_a + s_b` never works | — | impossible if `π_a` verified (§9, R13) |
| a party's own node lied to it | see §9, R18 | see §9, R18 |

---

## 3. The free option, and the bond idea — OPEN

Between Bob's lock and her own, Alice holds an option on the price at zero cost: if XMR/DERO moves in her favour
she completes; if it moves against her she does nothing and Bob refunds after T1. Farcaster's RFC 01 states it
for every swap of this shape — *"Alice always locks her coins later in the swap process, implying that she gets an
option to buy without cost."* Bob, once he has locked, has committed; his only way out is the refund window, and
it costs him fees and time. After Alice locks, the option is no longer free but it is not gone either: Alice can
still decline to claim (Bob refunds; she recovers her XMR) and Bob can still decline to release `k_b` (he refunds;
she recovers). Both of those cost fees and a few hours, not the notional.

`research/crosschain/RESEARCH.md` §4.6 sizes the option: with XMR/DERO daily volatility around 6 %, a two-hour
window has σ ≈ 1.7 %, so a taker who waits and walks gains a fraction of a percent per attempt, more in fast
markets. That volatility figure was assumed, not pulled from a feed (`research/crosschain/README.md`, "Q4
griefing: weakened"). The proposed answer is a **bond of 2–3 % of notional, posted by both sides in DERO and
slashed on DERO-observable failure**. It is not in this build (build spec §7), and one part of it does not
survive the gate secret: when a lock is refunded rather than claimed, the DERO chain cannot tell whether Alice
never locked XMR or Bob never released `k_b`, so "accepted but never claimed" is not attributable on chain. A bond
that slashes the taker on that signal would punish an honest Alice whose counterparty withheld the gate. Whether a
usable slashing rule exists — one that only fires on events DERO can see and attribute — is the open question, and
until it is answered the honest statement to a user is: **the counterparty can walk away before their lock, and
the cost to you is time and fees.**

---

## 4. Timelocks, and where every number comes from

DERO heights, not wall-clock times, are what the contract checks. The companion converts them for display with
the block-time constant, and §9 R19 says why that conversion is an estimate.

| Constant | Value | Reason | Primary source |
|---|---|---|---|
| DERO block time | 18 s | the unit of every DERO window | `config.BLOCK_TIME = 18`, vendored derohe `config/config.go:32` (upstream HEAD `e9df1205`) |
| DERO stable depth | 8 blocks (≈ 2.4 min) | a block deeper than this is never replaced: the node refuses a block whose height is below `chain height − STABLE_LIMIT` (`blockchain/blockchain.go:483–486`) and reports `height − STABLE_LIMIT` as the stable height (`Get_Stable_Height`, line 1139) | `config.STABLE_LIMIT = 8`, `config/config.go:77` |
| Monero block time | 120 s target | the unit of Alice's Monero wait | `DIFFICULTY_TARGET_V2 120`, `src/cryptonote_config.h` |
| Monero confirmations before `k_b` | 20 by default; the companion refuses fewer than 10 | ten is Monero's own spend age, so no smaller number is even meaningful. Above it, a reorganisation deeper than the depth Bob chose could still unwind Alice's lock after he has released the gate, and Monero has no bound on that depth, so the default is twice the floor. It is the one number in the protocol that is an assumption rather than a proof | `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE 10`, `src/cryptonote_config.h`; `DefaultConfirmations`, `tools/xmrswap/swap.go:26-30` |
| Monero reorganisation bound | none | `cryptonote_config.h` contains no constant limiting reorganisation depth (checked 2026-09-08); the 10-block spend age is a spend rule, not a finality rule | `src/cryptonote_config.h` |
| the deepest Monero reorganisation this project knows of | 18 blocks (≈ 36 min at target), 2026 | reported by exchanges and swap services; **secondary sources only**, no primary record found (`research/crosschain/README.md`, "Monero timing and reorg: secondary sources only. Flagged.") | SOURCES.md row "Monero timing" |
| `T1_MIN` | 400 DERO blocks (≈ 2 h) after the lock; Bob's companion sets `t1` = lock + 420 by default, and Alice's refuses a `t1` more than 1,200 blocks (≈ 6 h) out | Alice's whole path has to fit inside it: see the budget below. The 20 blocks of slack are there because Alice reads the lock a block or two after Bob chose `t1` and measures it against the same floor; the ceiling stops a maker holding her XMR against a deadline months away | `tools/xmrswap/swap.go:24, 44, 99`; a design margin, not a measured interval |
| `T2_MIN` | T1 + 400 blocks (≈ 2 h more), and no more than T1 + 1,200 | Bob's refund window; long enough that a Bob who is merely offline for an hour is not punished | `tools/xmrswap/swap.go:25, 45` |
| gate deadline G | 100 blocks (≈ 30 min) before T1 | Alice treats a `k_b` reaching her at or after `t1 − G` as never received, and Bob releases none that late: suppressing her Claim after a late gate must then last G blocks. 100 is the study's assumed value (operator decision 5) | `tools/xmrswap/swap.go:64`; `docs/ATOMIC_SWAP_STUDY.md` §2.1 U2 |
| stipend | 14,016 atomic units (0.14016 DERO) | a Claim at its fee (304 storage gas measured on the DVM, + 200) and a Punish at 3,000, each doubled twice by the re-send policy: (3,000 + 504) × 4. A walker keeps it: it is the maker's cost on every swap | `tools/xmrswap/swap.go:71-76`; `tests/htlc_test.go` `TestHtlcClaim` |
| g2, the Punish window | 4,800 blocks (≈ 24 h) after T2, **pending operator decision MT-J13** | an `htlc.bas` v3 install argument: Punish in [T2, T2 + g2), the maker's Reclaim from T2 + g2. It is the taker's floor (`-g2-min`): she refuses an install with less. Her companion sends no Punish from T2 + g2 − (11 + 8 + margin). Below about 19 + margin blocks no Punish can go out and be stable in time | `tools/xmrswap/mainnet_profile.json`; `docs/HTLC_V3_COMPANION_NOTES.md` §7 |
| contract bounds | `now + 1 < t1 < t2 ≤ now + 1,000,000`, and since v3 `t1 ≥ now + w1min`, `t2 ≥ t1 + w2min` at the Lock's execution height (install arguments, 400 and 400 at the profile) | the contract cannot know Monero's state; it enforces ordering and the minimum widths (MT-A03(a)), and the companion's own floors apply on top. DERO mines a Lock at most 10 blocks after it is built (the companion counts 11, one block conservative) and the daemon's estimate runs at height 0, so Bob's companion accepts no t1 width below max(T1_MIN, w1min + 11) = 411 and builds no Lock from t1 − w1min − 10. 411 is the contract's least with no slack: at exactly 411 the Lock can be built only at the height the windows were decided at, so one block arriving before the Lock reads the height makes his companion refuse (nothing is sent), and Alice's first-sight check leaves the Lock 11 blocks to be mined and read. The default of 420 is what leaves room: 10 heights to build at, 20 blocks for her check | build spec §3.A; `tools/xmrswap/dero.go` `lockCutoff` |

**The mainnet profile** (MT-B16, BL-27). Every policy number above lives in one file,
`tools/xmrswap/mainnet_profile.json`: T1Min 400, the default t1 420, T1Max 1,200, T2Min 400, the default T2 − T1 400,
T2Max 1,200, the confirmation floor 20 and its tiers, G 100, k 2 and the stipend floor 14,016. The companion's defaults
are generated from it, and `profile_test.go` fails if the file, the generated constants and the defaults ever disagree.
The same test checks the study's t1 rule at every tier: t1 − lock ≥ the waits before Alice's XMR lock + the expected
Monero path at the tier ((c + 1) blocks of 120 s) + G + k·σ (σ = √(c + 1) × 120 s), capped at T1Max. The waits are
18 DERO blocks as the companion runs: the lock mined and stable (1 + 8), then Bob's stipend mined and stable (1 + 8).
The study counted only the lock's 9 (§2.1 U7), before Path 0 added the stipend's wait, so its figures are 9 blocks
low:

| Confirmations | The rule needs (G = 100, k = 2) | The study's figure | t1 a maker quotes |
|---|---|---|---|
| 20 | 319.1 | 310 | 420 (the default) |
| 30 | 398.9 | 390 | 420 |
| 40 | 476.7 | 467 | 477 |

Three values wait on the operator, and the file marks each: G (MT-J04; the operator answered 30, and 100 is kept as
recommended), the confirmation floor and the notional thresholds of the 30 and 40 tiers (MT-J03; 10 only after
BL-30), and T1Max, T2Max and T2 − T1 (MT-J05). Answering one is a one-line edit to the file, then `go generate
./xmrswap`. What each answer does: G = 30 brings every tier inside the default 420 (249, 329 and 407 blocks) and makes
a suppressed Claim cost the maker 9 minutes instead of 30; a 10-confirmation tier needs 236 blocks at G = 100; T1Max
can come down to 477 at G = 100 with tiers up to 40, or to 420 at G = 30, and no lower without dropping the 40 tier;
T2 − T1 = 1,200 is already inside T2Max.

**Alice's budget inside T1, at target rates.** Bob's lock reaches stable depth: 8 DERO blocks, 144 s. Her XMR
transaction is mined: one Monero block expected, 120 s. Twenty confirmations, the default: 2,400 s. Bob's watcher notices and
his companion sends `k_b`: a poll interval plus one channel delivery (over spore, one DERO transaction, about a
block). Her Claim is mined and reaches stable depth before T1: 9 DERO blocks, 162 s. That is roughly forty-eight
minutes of a two-hour window, at target rates, and forty of them are the twenty Monero confirmations, which no
construction on DERO can shorten. Block intervals are targets, not promises, and a stuck Monero transaction
(fee too low, a full mempool) spends this budget with nothing to show; §9 R1 and R4 are what happens when it runs
out, and the answer is "nobody loses coins".

**For comparison**, COMIT's mainnet cancel and punish timelocks are 72 Bitcoin blocks each (`swap/src/env.rs:53–54`),
about twelve hours at Bitcoin's ten-minute target. Our windows are a sixth of that, and the reason we can afford
it is that DERO's finality is a consensus rule at eight blocks rather than a probability.

**What the numbers are not.** They are margins derived from source constants, not from measured mainnet
intervals; `research/crosschain/README.md` marks the timelock row "weakened … adequate for design margins, not
for a spec". Ten Monero confirmations is the *floor* — the smallest number at which anyone can spend the output —
not a number chosen for safety, which is why the companion defaults to twenty and refuses fewer than ten. For a
large amount it should use more still, and T1 must grow with it: every ten more confirmations is twenty more
minutes of Alice's budget. The 18-block figure
is a report, and this page treats it as one.

---

## 5. What a watcher of DERO learns

Contract storage on DERO is public, and `SIGNER()` is real only at ring size 2 (derohe
`blockchain/transaction_execute.go:430`; at any other ring size the contract sees 33 zero bytes and refuses the
call). So every call to `htlc.bas` is made at ring size 2, and anyone who reads the contract learns:

- Bob's address (the maker, from his `Lock`), the amount and the asset;
- Alice's address (the taker, stored at lock time);
- the three hashes `ha`, `hk`, `hb`, and the heights `t1`, `t2`;
- the state, and when it changes, who changed it;
- after a Claim, `s_a` — which is Alice's Monero key share, and from which anyone can compute `S_a`; after a
  Refund, `s_b`. Never both for one lock.

What stays hidden is what DERO always hides: everyone's *other* balances. A revealed `s_a` on its own does not
locate the Monero output: the joint address also needs `S_b` and `V`, which only travelled over the channel. Any
privacy language for the DERO leg must be limited to "balances, not participants or amounts"
(`research/crosschain/RESEARCH.md` §4.2).

## 6. What an observer of Monero sees

Two ordinary transactions. Alice's lock is a normal payment to a standard address: an output to a one-time key,
its amount hidden, in a ring with the network's fixed number of decoys. Bob's sweep, later, is a normal spend of
that output to his own address. Nothing on Monero names DERO, a swap, or either party. Someone holding the private
view key `v` — Alice, Bob, and anyone who read it off the channel — sees every payment to the joint address, its
amount and its timing, but can spend nothing. A third party who watched both chains could try to correlate a
DERO claim with a Monero sweep in the following hour; that is a statistical guess, not a link.

---

## 7. The companion

The companion is a program the user runs on their own machine for the length of one swap: `xmrswap bob` or
`xmrswap alice` (build spec §3.D). It talks to the user's own DERO wallet and daemon, the user's own
`monero-wallet-rpc`, and the proof tool; it holds `s_a` or `s_b`, `k_b`, the shared view key, and a durable state
file written with fsync-and-rename so that a crash and restart resume where they stopped and an irreversible
action (a lock, a gate send, a claim, a refund, a sweep) is attempted at most once (build spec §4, invariant 5).

Nothing the companion holds ever enters the browser. The app (§11) is a TELA page served from DERO with no server
behind it; it can read the contract and it can talk to the user's DERO wallet through XSWD, and that is all. A
secret leaves the companion only by the protocol's own reveals — `s_a` on DERO at Claim, `s_b` on DERO at Refund,
`k_b` over the channel after confirmation — and by no other route: not a log line, not a message of another kind,
not a status endpoint (invariant 1). Nobody hosts the companion for anybody else, and nothing is proxied: each
companion speaks to its own wallets (invariant 7). The operator of DMZ runs nothing at all for a swap to happen.

What the companion refuses before anything of either party is locked (`tools/xmrswap/cmd/xmrswap/main.go`):
- **A network left to a default, or two networks mixed.** `-net`, `-daemon`, `-dero` and `-monero` have no defaults;
  mainnet must be named with `-mainnet` and is refused unless the DERO daemon, the DERO wallet and the Monero wallet
  are all on it (`main.go:411`); the DERO network is a term of the offer (GAP-007).
- **A wallet anybody can drive.** The DERO wallet's `--rpc-login` is sent as basic auth, and on mainnet a DERO wallet
  that answers a call made without it, or a `monero-wallet-rpc` that never asks for its digest login, is refused;
  logins come from the environment (`VEIL_RPC_LOGIN`, `XMRSWAP_MONERO_LOGIN`), never the command line (GAP-008,
  GAP-009).
- **A remote wallet or monerod on mainnet.** `-monerod` is required there and on loopback, as are the wallets; the
  companion names the monerod to the wallet-rpc with `set_daemon` after every wallet it opens, because the rpc keeps
  it only for the wallet open when it was sent (GAP-010, `swap.go:902`).
- **Another contract or another prover.** The contract's code must hash to `HTLCCodeSHA256`, and `-prover` must be the
  absolute path of the proof-xmr build pinned by `ProverSHA256` (`pin.go:26`), hashed before every run (GAP-003).
  An omitted `-htlc` is the scid `deploy/networks.json` records for the daemon's DERO network, compiled in and checked
  against the file by a test; recorded or given, the contract is refused at start unless its code is the pinned
  `htlc.bas` v3 and every `-daemon` shows the same code and install arguments (MT-A18, MT-B09,
  `tools/xmrswap/record.go`). v3's code hashes the same for every install, so the companion also reads `w1min`,
  `w2min` and `g2` with it, and each party refuses an install outside its policy (the maker: widths his Lock cannot
  meet; the taker: a `g2` below `-g2-min`, the profile's 4,800 by default).
- **An answer it cannot read the same way twice.** Every daemon and wallet reply is read through a size cap and a
  timeout; one with trailing data, or with neither a result nor an error, is refused (`dero.go:256-295`,
  `monero.go:157`).

Every parser of those answers — the JSON-RPC envelope, each DERO and Monero reply, the digest challenge, the prover's
verdict, addresses and keys, the sealed channel's envelope — has a fuzz target; each ran for 30 seconds with no crash,
from 47,414 executions (the DERO replies, one HTTP round trip each) to 2,142,902 (the envelope) (MT-G06,
`tools/xmrswap/fuzz_test.go`).

Three things the companion does without being asked. It withholds s_a, s_b, k_b, the keys and the wallet passwords
from every error a daemon or wallet makes of a request that carried them, so a server that echoes a failed Claim
cannot put s_a in a log (MT-L02, `tools/xmrswap/redact.go`). It prints Alice a recovery card before her XMR moves —
the state file named as a secret, the command that resumes her, every window as a clock time — and asks the operating
system to wake the machine ten minutes before the first window closes: a Task Scheduler task set to WakeToRun on
Windows, a systemd timer with WakeSystem on Linux, and on macOS nothing, since only root can schedule a wake there
(U9, MT-B15, `tools/xmrswap/recovery.go`); it prints Bob his own before his DERO is locked — t1, the last height it
sends his Refund at (t2 − 12), t2 and t2 + g2 — and registers a wake before t1 and another before t2 + g2, when, nobody
having punished, only his Reclaim returns his DERO and her XMR (MT-B28). And a companion killed during its sweep resumes it: the joint spend
wallet's own record says whether the sweep went out, and if it did not, it is made, which can pay no one but the
party itself (MT-B12).

The joint wallets cost seconds, not minutes (MT-B13, `research/regtest/xmrswap-scan.log`). On the regtest
`monero-wallet-rpc` 0.18.5.1, a view-only wallet made from the joint address and view key took 2.5 to 4.1 s to create
(`generate_from_keys`, whatever the restore height) and 0.29 s to scan 50 blocks, 0.31 s for 500 and 0.46 s for 5,000;
a spend wallet, 3.2 s and 0.30 s for 500. The companion's restore height is the chain's height at setup, so its scan is
the swap's own few hundred blocks. In the reorganisation runs a whole pass — reopen the joint wallet, name the
`monerod`, refresh, read the balance — took about 10 s. Regtest blocks are nearly empty; a mainnet block carries far more
outputs to test, so the scan there is slower in proportion, and it has not been measured.

`xmrswap status` prints each swap in a state directory beside what the DERO chain and the joint Monero wallet say now,
and names every divergence: a state that says open where the chain says refunded, a lock whose fields are not the
ones recorded, a joint address that has received less than the swap acted on (`tools/xmrswap/status.go:20`).

## 8. The rendezvous: spore, and why it is only a conversation

The two parties need to exchange the setup table of §1.2 and, later, `k_b`. The intended transport is
**spore** (`liqdmetal/spore`, formerly mycelium): an end-to-end encrypted, no-relay messenger that rides DERO
transaction payloads — up to 144 bytes per transaction at ring size 2, about one DERO block per message, with long
bodies held by the sender and fetched peer to peer (`research/wrapper/MECHANISM_REVIEW.md` §4). This build ships
a directory-of-JSON-files transport for the simulator and for two people who can exchange files; spore is the
later piece (build spec §7).

The channel is a conversation and nothing more, and the protocol is built so that it *can* be nothing more:

- **It carries no value and holds no key that can spend.** The private view key travels over it, and it can see;
  it cannot spend. `k_b` travels over it, and it is useless to anyone but the named taker with the right `s_a`.
- **Its integrity is not a safety assumption.** Alice checks every field of Bob's on-chain lock against her own
  values and Bob's committed hashes before she locks XMR (invariant 3). A channel that lied to either party
  produces a lock that fails those checks, and the swap aborts with nothing locked but Bob's DERO, which he
  refunds. §9 R17 walks through it. The channel is needed for *liveness* — for the swap to happen at all — not
  for either party's coins to be safe.
- **It is not what makes the swap trustless.** MECHANISM_REVIEW §4's decision is exact: adopt spore as the
  rendezvous; do not adopt its Monero code; "do not describe it as making the cross-chain part trustless — it
  makes the *conversation* serverless; the value transfer is still whichever row of §3 applies."

**The channel is sealed (GAP-019).** The directory transport alone writes plain JSON files
(`tools/xmrswap/channel.go:33-121`): anyone who could read the directory — a sync provider, the other users of a
shared drive — read the private view key `v`, `S_a`, `S_b`, `H_a`, `H_b`, `H_k`, both DERO addresses and Alice's
Monero txid, and so learned the joint address and the amount and timing of the Monero lock; anyone who could write
it could put messages in either party's box as the other. The companion now seals every message
(`tools/xmrswap/sealed.go`):

- The maker makes a 32-byte key for this swap alone (`xmrswap channel-key`) and gives it to the taker with the offer,
  over whatever the two already trust; each companion takes it from `XMRSWAP_CHANNEL_KEY`, never the command line,
  and refuses to start without it.
- Each direction has its own key, HMAC-SHA256 of the swap key and a label naming the direction (HKDF-Expand for one
  block), and each message is AES-256-GCM under it with a fresh random nonce and the direction as associated data.
- A message that does not open — written by someone without the key, sealed under another swap's key, altered, or
  copied back to its own sender — is dropped and said; it never reaches the state machine
  (`TestSealedChannelRefusesImpersonationAndTampering`).

Why this and not more: the parties already need a trusted path to agree the offer, and a key handed over on it
costs nothing further; a key exchange on the directory itself would need that same path to authenticate it. The
primitives are the standard library's, so the channel adds no dependency (`golang.org/x/crypto`, which the module
already requires for Keccak, would have served as well).
What stays visible to the directory's reader: that two parties are talking, when, and how much (file names carry a
timestamp and the sender's name). What it does not stop: a replay of one of this swap's own messages into the box it
came from, which is a duplicate the channel already delivers by design and each side consumes once. For the coins
nothing changes: every message that matters is still checked against a proof or the chain.

---

## 9. Adversarial review of the protocol as specified in the build spec §1

Each item: the situation, what happens, and whether `docs/XMR_SWAP_BUILD.md` already covers it. "Covered" means
a rule or test in the build spec produces the stated outcome; "partly" and "not covered" name what should change.
Nothing in this section was run; every outcome is read off the contract rules in §3.A and the invariants in §4 of
the build spec, plus the chain facts in §4 above.

**R1. T1 passes while `k_b` is in flight.** Bob sends `k_b` late in the window; Alice's Claim reaches the chain at
height ≥ T1. *Outcome:* the contract refuses it (`height < t1` fails; state stays 0 — the same behaviour was
observed on the simulator for `swap.bas`, `poc/simswap-run.log`). Bob refunds in [T1, T2), revealing `s_b`; Alice
recovers her XMR with `s_a + s_b`. Both lose fees and hours; neither loses coins. *Covered:* yes, by the contract's
height gate and the reveal on Refund. The companion should stop trying to claim once `now + 1 ≥ t1` rather than
spending fees on refusals.

**R2. Alice's Claim lands inside DERO's last eight blocks before T1, and the block is replaced.** DERO replaces
no block deeper than `STABLE_LIMIT = 8` (§4), but a block shallower than that can be. Suppose the Claim is mined at
height `h` with `t1 − h ≤ 8`. Bob reads `s_a` from that block. If the block is displaced and the Claim is re-mined
below T1, nothing changes. If it is re-mined at or after T1, it is refused, the lock is open again, and Bob — who
already holds `s_a` from the displaced block — can now Refund, revealing `s_b`. *Outcome:* both parties hold
`s_a + s_b`; the XMR goes to whoever sweeps first, and Bob also has his DERO back. If Bob wins that race Alice has
lost everything. *Covered:* **not covered.** The build spec's state machines have `claimed(read s_a) → swept` with
no stability step, and invariant 3's checks are made once, at XMR-lock time. Two rules close it. Alice's companion
must send a Claim only while it can expect the Claim to be *stable* before T1 — never later than
`t1 − STABLE_LIMIT − 1`, less whatever margin the operator sets — and otherwise wait for Refund (recover XMR) or
T2 (Punish). Bob's companion must not sweep until the Claim is at or below the stable height (a sweep spends
Alice's XMR; if the Claim is later refused, Bob has both). With those two rules the race cannot start: a Claim
Bob acts on is one DERO can no longer undo.

**R3. A Monero reorganisation after the gate.** Bob sends `k_b` at twenty confirmations, the default. Monero then
reorganises deeper than twenty and Alice's lock transaction does not return — because its inputs were in the displaced blocks
and no longer exist, or because Alice broadcast a conflicting spend of the same key image into the new chain.
*Outcome:* Alice holds `k_b` and `s_a`; she claims the DERO. Bob forms `s_a + s_b` for an address that holds
nothing. Bob has lost the DERO. *Covered:* by policy, not by a rule that can be proven. Monero has no consensus
bound on reorganisation depth (§4), so for any confirmation count there is a reorganisation deep enough. The
18-block report (secondary) already exceeds the ten the build spec names as the minimum. The honest statement is
in §10: Bob's safety rests on the assumption that Monero does not reorganise deeper than his chosen depth *after*
he has released the gate. The companion should take that depth as a parameter, default it above ten for any
amount that would hurt, and the spec should state the default rather than "more for large amounts". Note the
asymmetry: below ten nobody can spend the output anyway, so releasing at exactly ten buys Bob nothing but time
against T1.

*Measured* (MT-B11, `research/regtest/xmrswap-reorg.log`): on a real regtest `monerod`, with the companions on the DERO
simulator, Alice's lock was taken off the chain with `pop_blocks` after Bob had released `k_b` at 12 confirmations
(his depth in that run: 10), a longer branch was mined in its place, and Alice spent the lock's input again on it,
with the same key image. She claimed the DERO. Bob read `s_a`, formed `s_a + s_b`, and his joint spend wallet held
nothing: `sweep_all` answered -4, his companion ended in `sweep_unknown`, and the joint address shows 0 received on
the chain that won. Alice's own wallet ended with what it held before the swap less the second spend's fee. Bob bore
it, as this paragraph says. The same reorganisation below his depth — at 3 confirmations, before any gate — opened no
gate: Bob refunded at t1, and Alice's XMR was back in her wallet to the piconero (R4). A DERO-side reorganisation
cannot be made on the simulator, which is one node mining its own chain: Invariant 9 and U3 have run only against the
in-memory fake chain.

**R4. Alice's lock never confirms in time.** Her transaction sits unmined, or confirms too late for the gate and
the Claim to land before T1. *Outcome:* Bob never sends `k_b` (invariant 2), refunds at T1 and reveals `s_b`;
Alice recovers her XMR once it does confirm. Alice loses a Monero fee and the time. *Covered:* yes.

**R5. A second deposit to the joint address.** Four cases. (a) Alice tops up, or pays in two transactions: Bob's
companion must count the *sum* at the joint address against the agreed amount, and `sweep_all` takes everything
there. (b) A third party pays the joint address: the address travels only over an encrypted channel, so this means
a leak; whoever ends the swap holding `s_a + s_b` (Bob after Claim, Alice after Refund) also takes the stranger's
coins, and after Punish nobody does. (c) A payment after the swap ended: same rule — it goes to whichever party
holds the completed key, forever. (d) The burning bug: two outputs sent to the same one-time key, of which only one
is spendable (Monero's post-mortem, 2018-09-25 — the wallet fix was to warn on a duplicate). If Alice does this
deliberately, a companion that sums `get_transfers` entries could see the agreed amount when only part of it can be
spent; Bob releases `k_b` and loses the difference. *Covered:* (a)–(c) yes, since only a holder of `s_a + s_b`
can spend. (d) **partly**: invariant 2 says "read from his own view-only wallet", which is the right source, but
does not say *what* to read. Bob's companion should require the wallet's own *unlocked* balance for the account,
not a sum over transfer entries, and treat a duplicate-output warning as a failed lock. Whether `get_transfers`
lists both entries of a burnt pair is not something this project has measured.

**R6. Alice locks with a far-future `unlock_time`.** Every Monero transaction carries `unlock_time`
("spend this tx not early then block/time", `src/cryptonote_basic/cryptonote_basic.h`), and `get_transfers` reports
it, with `locked`, per entry. Alice sends the agreed amount with `unlock_time` years ahead. *Outcome:* the output
confirms normally; a companion that checks only `confirmations ≥ 10` releases `k_b`; Alice claims the DERO; Bob's
`sweep_all` fails until the unlock. Bob's XMR is not stolen but it is **frozen** for as long as Alice chose, at no
cost to her. *Covered:* **not covered by the wording.** §3.C's `Incoming` struct has an `Unlocked` field, so the
data is there, but invariant 2 names confirmations only. The invariant must also require `unlock_time == 0` (or
`locked == false` *and* `unlock_time == 0`, since `locked` is also true during the ten-block age) before the gate
is sent. This is the one item on this page that would contradict "nobody can freeze either side's coins" if left
open, so it has to be closed before the build is called done. *Closed:* Bob's gate counts only a transfer whose
`unlock_time` is 0 as `get_transfers` reports it, and never more than the wallet's unlocked balance (invariant 8,
`tools/xmrswap/swap.go` `confirmedAtJoint`). A current `monero-wallet-rpc` refuses to build such an output at all
(0.18.5.1: -50 "Transaction cannot have non-zero unlock time", `research/regtest/monero-smoke.log`); the check on what
arrives stays, because other software can build one, and a coinbase always carries one.

*Measured* (MT-B14, `research/regtest/xmrswap-unlock.log`). The rule in monero 0.18.5.1's own words: an output is
spendable only once both its `unlock_time` has passed (a height below `CRYPTONOTE_MAX_BLOCK_NUMBER` = 500,000,000, else a
unix time) and it is `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE` = 10 blocks deep (`cryptonote_config.h:40, 49`;
`wallet2.cpp:7308-7344`, `is_transfer_unlocked` and `is_tx_spendtime_unlocked`); `get_transfers` reports that as
`locked`, beside the `unlock_time` itself, and lists a coinbase in `in` with type `block` (`wallet_rpc_server.cpp:498,
501, 517`); and a 0.18.5.1 wallet builds no transaction with an `unlock_time` at all (`wallet2.cpp:7826`, the RPC's
-50). So on regtest the locked output was made the one way it can be: a block mined to the joint address, whose coinbase
carries `unlock_time` = its height + 60 (`CRYPTONOTE_MINED_MONEY_UNLOCK_WINDOW`, `cryptonote_config.h:44`). Bob's view
wallet listed it, and he refused it from the first sight — "carries unlock_time 6649: it does not count towards the
gate" — and kept refusing it for as long as he watched, past his 10 confirmations, until t1, while it was still locked;
he never released `k_b` and refunded at t1, and Alice, after t1 − G, read `s_b` and swept the output once it unlocked. What does the work is counting only outputs
the wallet reports unlocked, capped at the unlocked balance: that alone keeps out an output still locked when Bob counts.
The explicit rule (`swap.go` `confirmedAtJoint`) also keeps out one whose lock has passed, and makes Bob say why; without
it an already-expired lock would count (`TestAFarLockedOutputOpensNoGate`, which fails on the code without the rule). A
non-coinbase transaction with a far-future `unlock_time` could not be made here, since the wallet refuses to build one;
the fake wallet's test covers it, on the same code path.

**R7. Bob's lock is wrong — smaller than agreed, the wrong asset, a T1 too close, a taker that is not Alice, a
hash that is not hers.** *Outcome:* Alice's checks (invariant 3) fail and she does not lock; Bob refunds at T1.
Alice loses nothing; Bob loses two fees. *Covered:* yes.

**R8. Bob sends `k_b` early, on purpose.** Before Alice has locked anything, Bob releases the gate. *Outcome:*
Alice could claim the DERO at once without ever locking XMR. That is Bob giving his coins away; nobody else is at
risk, and nothing Bob learns (`s_a`, once she claims) unlocks anything, because the joint address is empty. The
reference companion for Alice accepts a gate only in its `waiting_gate` state (build spec §3.D), after its own XMR
lock, so an early gate is held rather than acted on — a courtesy, not a safety property. *Covered:* yes;
invariant 2 exists to protect Bob from Bob.

**R9. Bob sends a wrong `k_b`.** *Outcome:* Alice's companion checks `SHA256(k_b) == hk` before it spends a fee on
a Claim, and ignores it; the contract would refuse it anyway. Alice waits; Bob refunds at T1 or is punished at T2.
*Covered:* yes.

**R10. Bob refunds at T1 in the same block as Alice claims.** *Outcome:* Claim requires `height < t1`, Refund
requires `t1 ≤ height`; no single block satisfies both, and within a block the first to set `st` wins while the
other is refused. Exactly one succeeds. At T2 the same holds between Refund (`height < t2`) and Punish
(`height ≥ t2`), and at T2 + g2 between Punish (`height < t2 + g2`) and Reclaim (`height ≥ t2 + g2`). *Covered:* yes,
by the contract; the only way this becomes a race is R2.

**R11. Bob vanishes after Alice locked.** No gate, no refund. *Outcome:* from T2 Alice punishes and takes the DERO.
Her XMR is under a key nobody will ever complete. She has what she agreed to buy; Bob has nothing. *Covered:* yes;
this is what Punish is for (§2) — provided a Punish, hers or anyone's for her, lands before T2 + g2 (v3). If none
does, her XMR comes back only if Bob Reclaims from T2 + g2 and so reveals `s_b` (his companion does, unattended); a Bob
who has vanished for good, lost his state or `s_b`, or chooses never to reclaim leaves her XMR and his DERO locked for
ever. Under v2 she could Punish at any later time; this is v3's cost to her, and §10 states it.

**R12. Alice vanishes after Bob locked.** *Outcome:* Bob refunds at T1; nothing else moved. Bob is out two fees and
about two hours; Alice exercised the free option of §3. *Covered:* yes, and the cost to Bob is the open item of §3.

**R13. Bob skips verifying `π_a`.** Alice's `H_a` is the hash of a random 32 bytes, not of her key share. *Outcome:*
she locks XMR, Bob releases `k_b`, she claims and reveals the random bytes. `s_a' + s_b` opens nothing. Bob has
lost the DERO; the XMR is locked forever unless Alice sells him the real `s_a` off chain, which is extortion, not a
protocol path. *Covered:* yes, by invariant 4 and the proof tool; this is why no lock happens before verification.

**R14. Alice skips verifying `π_b`.** Bob's `H_b` is the hash of a non-share. *Outcome:* Bob can refund at T1 with
it; the contract is satisfied, Bob has his DERO back, and Alice's `s_a + s_b'` opens nothing. Her XMR is locked
forever, again unless she buys the real `s_b`. Bob lost nothing. *Covered:* yes, by invariant 3, which checks `hb`
against the `H_b` that `π_b` was verified for.

**R15. The proof is valid but the bytes differ.** The DERO contract hashes the exact 32 bytes; Monero reduces a
scalar modulo ℓ (`src/crypto/crypto.cpp`). *Found* (L1, `docs/audit/PROOF_XMR_AUDIT_PACKAGE.md`): the circuit proves
`S = x·G` and `H = SHA256(x)` for the 32 bytes `x` and never that `x < ℓ`. Our `proof-xmr keygen` makes reduced
scalars and our `prove` refuses others, but nothing makes the peer use them: a party can prove, commit and reveal
`x = s + kℓ`, the contract accepts it on Claim or Refund (it checks the hash alone), and `(s + kℓ)·G = s·G`, so it is
the same share. *Outcome before the fix:* the companion refused the reveal as "not reduced mod l" and stopped —
griefing: no coins lost, the automatic path stalled. *Covered* (MT-B25): wherever the companion reads a revealed share
(`s_a` from a Claim, `s_b` from a Refund; `swap.go` `checkRevealed`), it checks that the 32 bytes as revealed hash to
the committed `H`, reduces them mod ℓ, checks that the result is the discrete log of the committed `S`, and uses the
reduced scalar; only a value failing one of those is refused (`keys.go` `RecoverShare`;
`TestARevealedShareNotReducedModLStillOpensTheKey`: `s + ℓ` and `s + 15ℓ` each open the joint key, a value reducing to
another share is refused). The circuit's own fix, a range check on `x`, is left for the auditor to rank.

**R16. A phantom taker, or a call at the wrong ring size.** Two readings. (a) Bob names as `taker` an address
nobody controls: Alice's check that `taker` is her own address fails and she never locks; Bob refunds. (b) A
Claim, Refund or Punish is sent with a ring larger than 2: `SIGNER()` is 33 zero bytes, `IS_ADDRESS_VALID` fails
and the call is refused before any state changes (`transaction_execute.go:430`; every entry point in the swap
contracts begins with that check). Those three calls carry no value, so a refusal costs a fee. **A Lock is
different.** It carries the DERO, and derohe returns the value of a refused call to the sender only when it can
identify the sender — which it can only at ring size 2. A non-zero `RETURN` becomes an execution error
(`dvm/sc.go:286`, "Discarded knowingly"); the daemon then tries `Extract_signer`, and when that fails the comment
reads *"we could not extract signer, we burn all the funds"* (`transaction_execute.go:397–404`). So a Lock sent at
any ring size but 2 is refused by line 21 of the contract *and its DERO is burned.* *Outcome:* Bob's own loss, to
nobody's gain. *Covered:* the build spec §3.D says the DERO client sends at ring size 2, which prevents it, but it
does not say why, and this is the why: the companion must set the ring size itself on every call and never
inherit a wallet default, and the send-at-2 rule belongs in §4 of the build spec as an invariant with a test,
not in an interface comment.

**R17. The channel is tampered with.** Someone between the parties replaces `S_b, H_b, π_b` with their own valid
triple, or replaces Alice's address, or replaces `V`. *Outcome:* Bob locks with his own `hb`, which no longer
matches the `H_b` Alice received, so her check fails and she does not lock. A replaced taker fails the same check.
A replaced `V` means Bob's view-only wallet watches an address Alice never paid, so he never sends `k_b`, refunds at
T1, and she recovers. In every case the swap aborts with Bob's DERO refundable and nothing else moved. What a
channel eavesdropper *does* get is the private view key, and with it the amount and timing of the Monero lock —
a privacy loss, never a fund loss. *Covered:* yes, by construction — invariant 3 makes the chain, not the channel,
the thing each party trusts — though the spec does not say so in words, and it should, because it is the reason
spore needs no audit before it is used here.

**R18. A party's own node lies, and colludes with the counterparty.** It is not a delay; it is a whole leg
(`docs/CROSS_CHAIN_TRUST_MODEL.md` §5). *For Bob:* his `monero-wallet-rpc` talks to a `monerod` that shows XMR at the
joint address, deep enough, that is not on the chain; he releases `k_b`, Alice claims the DERO, and Bob sweeps
nothing — **he loses the DERO-side asset**. *For Alice:* her DERO node shows an `htlc.bas` lock that does not exist;
she locks XMR, Bob releases the gate, her Claim goes out through that node and reveals `s_a` to it, and Bob sweeps —
**she loses her XMR**. *Covered:* **not by the protocol, and it cannot be.** Each party trusts the nodes they read
from, and a remote node they do not run is a trusted party who can cause a total loss (§10). What the companion does:
on mainnet it refuses a wallet-rpc, a DERO wallet or a `monerod` that is not loopback, and it names its own `monerod`
to the wallet-rpc with `set_daemon` after every wallet it opens (`-monerod`, GAP-010), because it cannot ask which
`monerod` a wallet-rpc was started with and the rpc keeps a `set_daemon` only for the wallet open when it is sent
(`research/regtest/monero-smoke.log`). Elsewhere it warns. Reading from several independent sources and acting
only when they agree narrows "one liar" to "every source colludes". For Alice's side it is built (MT-B09): she may
name further DERO nodes with `-daemon`, and she locks no XMR until every one shows her the same lock on the pinned
contract — same terms, open, inside her policy by its own height. A node that shows other terms makes her refuse;
one that shows no lock makes her wait until her budget refuses it (`tools/xmrswap/swap.go` `witnessCheck`;
`TestALyingDeroNodeBlocksTheXmrLock`). It moves the trust from one node to all of them, and only if they are run by
different people. For Bob's Monero side it is not built.

**R19. Heights are not clocks.** T1 and T2 are DERO heights; Alice's wait is Monero confirmations; the two are
coupled only through wall time. If DERO blocks run fast for an hour, Alice's two hours are shorter than she
displayed; if Monero runs slow, her twenty confirmations eat more of them. *Outcome:* R1 or R4 — no coins lost, a
swap that fails. *Covered:* partly; the minimums assume target rates. The companion should convert remaining
blocks to minutes using the rate it has actually observed since the lock, and refuse to lock XMR when the remaining
budget is below what its own path needs.

**R20. Secrets reused across swaps.** If Alice used the same `s_a` twice, the first Claim would publish the key
share of the second joint address. If Bob reused `k_b`, a counterparty in one swap could claim the other. *Outcome:*
theft of the second swap. *Covered:* `keygen` is called per swap, but no invariant says "fresh per swap". It should.

**R21. A crash at any step.** *Outcome:* the durable state file is written before every irreversible action and
loaded strictly on restart; a sweep is attempted at most once per swap id (invariant 5; the crash fuzz in build
spec §5.D). The chain-side facts do not decay: `s_a` and `s_b`, once revealed, are there forever, so neither
party's recovery has a deadline. *Covered:* yes.

**R22. Alice claims and also tries to take the XMR; Bob refunds and also tries to take the XMR.** *Outcome:* Alice
after Claim lacks `s_b`, which Refund never happened to reveal; Bob after Refund lacks `s_a`, which Claim never
happened to reveal. Exactly one of `xa_<id>`, `xb_<id>` ever exists (§1.4). Neither can. *Covered:* yes — this is the
protocol's central property, and the Claim/Refund tests in build spec §5.A are the tests of it.

**R23. Bob locks twice against the same setup.** Two lock ids with the same `ha, hk, hb`. *Outcome:* the same
`k_b` opens both; Alice can claim both. Bob pays twice for one XMR lock — his own doing, no risk to Alice. A
companion that generates fresh secrets per lock (R20) never produces this.

**R24. A refused-by-height Claim with a fee that will not mine.** The daemon's gas estimator runs at height 0, so
height-gated calls are sent with a fixed fee (`research/crosschain/RESEARCH.md` §4.1). A Claim whose fee is too low
sits unmined into T1. *Outcome:* R1. *Covered:* the client re-sends at double the fee, twice at most, and sends no
Claim at all — first attempt or re-send — once `now + 1 ≥ t1 − 8 − margin`, the same claim cutoff the state machine
stops at (`tools/xmrswap/dero.go:1016`; XMR-06: the re-send rule was once `now + 8 + 1 < t1`, which let a re-send
out inside the band the margin keeps empty; `TestCanResendStopsAtTheClaimCutoff`).

The pattern across all of these: every outcome that costs a party coins is either (i) that party's own choice
or mistake (R8, R11 for Bob, R16's burned Lock, R23), (ii) a skipped proof (R13, R14), (iii) a lie from a node
that party chose to read from (R18), or (iv) one of two gaps the build spec must close before "done" — the
last-eight-blocks rule (R2) and the `unlock_time` check (R6) — plus the Monero-depth assumption (R3), which cannot
be closed, only stated and sized. Since v3 there is one more: a taker who let T2 + g2 pass with no Punish on the chain,
facing a maker who then never reclaims (R11) — her missed window, compounded by his choice or his loss.

---

## 10. Trust statement

**What is trusted.**

- **The cryptography.** SHA-256 is one-way and collision-free in practice (the hashlocks). The discrete logarithm
  on ed25519 is hard (the key shares). The proof system — Bulletproofs over a rank-1 constraint system with a
  SHA-256 gadget, no trusted setup — proves what it says (`poc/proof-xmr`). That proof is a prototype built and
  measured on 2026-09-06; it has not been audited by anyone outside this project. Monero's own transaction proofs
  (RingCT, CLSAG) hold, so an output under the joint key can be spent by that key and no other.
- **The chains' rules.** The DERO VM executes `htlc.bas` as written — the contract is tested against the real DVM
  (`tests/htlc_test.go`) and the send-order rule is checked mechanically (`tests/sendorder_test.go`). DERO never
  replaces a block deeper than eight, which is a consensus rule (§4). Monero does not reorganise deeper than the
  confirmation depth Bob chose, *after* he has released the gate — which is **not** a consensus rule, only an
  observation and a margin (§4, §9 R3). This is the one assumption on this page that cannot be turned into a
  check.
- **Your own wallet, node and machine.** The companion runs on your machine and talks to a DERO wallet, a DERO
  daemon and a `monero-wallet-rpc` that you point it at. If any of those lies to you, the protocol cannot tell
  (§9 R18). A remote node you do not run can cause a total loss that nothing in the protocol detects: run your own,
  or name the one you trust and know that you are trusting it.
- **The companion's code**, which holds your share and the gate. Read it or trust whoever did. It pins the contract's
  code and the proof tool's binary by hash (`tools/xmrswap/pin.go`). The proof tool's pin can be checked by anyone: its
  build reproduces byte for byte from the committed sources (`research/crosschain/poc/proof-xmr/build-reproducible.sh`,
  sha256 `7293e9c9…`, MT-B26). The companion's own binary, which carries both pins, does not yet (BL-14): until it
  does, whoever built your companion is trusted to have pinned the right ones.
- **DERO for the fee comes from the counterparty.** Every DERO transaction takes its fee from a DERO payload
  (`vendor/derohe/blockchain/transaction_verify.go`, "we can not deduct fees, if no base"), so a holder who
  arrives with only XMR cannot sign the Claim that pays them. The maker sends the taker a stipend once his lock is
  stable and before she locks anything, and her companion locks no XMR until it is in her wallet at stable depth
  (Path 0, `tools/xmrswap/swap.go:1352, 1402`). *"Your first DERO for gas is a gift from your counterparty, sent
  after they have committed and before you have; you owe them nothing for it and they cannot take it back."* It is
  the maker's cost on every swap, a walker's included, and he cannot tell a newcomer from a sybil. Her address must
  be registered first, which costs no DERO.
- **Each party's own machine, at its window.** If Bob's companion is not running between t1 and his refund cutoff,
  t2 − 11, he cannot Refund, and from t2 anyone may Punish and pay Alice the DERO he locked. The cutoff is there
  because DERO mines a transaction at most 10 blocks after the height it was built at (the companion counts 11) and a
  Refund executing at t2 is refused: his companion sends none from t2 − 11, counts one only once it is stable, and
  sends it again if a replaced block undoes it before the cutoff. One undone after the cutoff leaves the lock to
  anyone's Punish until t2 + g2, and then to his Reclaim (MT-B27). His recovery card prints these heights, and his
  companion registers a wake before t1 and another before t2 + g2 (MT-B28). No construction on DERO removes that need:
  a Refund held by anyone else is held by someone who could use `s_b` to steal (`docs/ATOMIC_SWAP_STUDY.md` §6).
- **The taker's Punish has a deadline: t2 + g2** (`htlc.bas` v3, MT-J13). Her companion, or anyone she asks — it
  carries no secret and pays only her — must get a Punish in before t2 + g2 (4,800 blocks, about 24 hours, proposed
  and pending the operator); her companion sends none from t2 + g2 − (11 + 8 + margin), and her recovery card prints
  the window's last block and wakes her machine before t2. Missing it costs her the punishment and makes her XMR
  depend on Bob: from t2 + g2 the lock is his to Reclaim, and his Reclaim puts `s_b` on the chain as a Refund does, but
  nothing makes him send it. A Bob who never does — hostile, or having lost his state or `s_b` — leaves her XMR and his
  DERO locked for ever. Under v2 she could Punish at any time after t2; this dependence is new in v3.
- **Nobody, for the lock itself, after t2 — LIV-07 stays fixed.** In `htlc.bas` v1 only the taker could call Punish,
  so a lock she never punished was frozen for ever. In v2 and v3 anyone may send Punish from t2, and it pays the stored
  taker; in v3 it closes at t2 + g2, after which the maker Reclaims. The lock always has an exit and none waits on a
  person other than the party it would pay (§2); Alice's XMR, once nobody has punished by t2 + g2, waits on Bob (the
  item above).
- **DERO including Alice's Claim in time.** The companion sends no Claim from sixteen blocks before t1
  (`tools/xmrswap/swap.go:34-37`), so that any Claim it did send is final before Bob's Refund opens. The mempool is
  ordered by fee per byte (`vendor/derohe/blockchain/mempool/mempool.go:377`), and a miner, or a flood of
  higher-fee transactions, that holds her Claim out for longer than that margin reopens the race the margin
  exists to prevent, with `s_a` already readable in the Claim Bob saw. The margin prices the attack at sixteen
  blocks of censorship; it does not rule it out.

**What is not trusted.**

- **The operator of DMZ.** They host nothing for a swap, hold no key that touches it, and could not take or
  freeze either side's coins if they wished to. `htlc.bas` has no owner, no admin, no fee and no update path.
- **Any signer set.** There is none. Nothing is wrapped; nothing is held in reserve; nothing is minted.
- **The counterparty, once your checks have passed — provided you act in your windows.** Before their lock, they can
  walk (§3). After both locks, the only choices they have are the four exits (§2): Bob can decline the gate and
  refund, which returns your XMR; he can do nothing, which costs him the DERO if a Punish — yours, or anyone's for
  you — lands before t2 + g2, and otherwise leaves your XMR waiting on his Reclaim; Alice can decline to claim, which
  returns Bob's DERO. Two counterparty actions could freeze your coins. A lock with a far-future `unlock_time` is §9 R6,
  closed by the gate's check. And since v3, a Bob who does nothing and then never Reclaims freezes the XMR you locked,
  with his own DERO, for ever once nobody has punished by t2 + g2: only a timely Punish rules that out, which is why
  the taker's deadline is listed above among what is trusted.
- **The channel.** Needed for the swap to happen, not for your coins to be safe (§8, §9 R17).

**Why "trustless" is justified here and not for the federation.** The word means one thing on this page: *no
party — operator, signer set, or counterparty — can take or freeze either side's coins.* Here that is true by
construction for a party whose machine acts in its own window — for Bob, his refund in [t1, t2); for Alice, since v3, a
Punish in [t2, t2 + g2), hers or anyone's for her, if Bob has neither released the gate nor refunded: the DERO is in a
contract with no owner whose four exits are exhaustive and mutually exclusive, and the XMR is under a key that only
the protocol's own reveals complete. Past t2 + g2 with no Punish, a Bob who never reclaims freezes Alice's XMR, and this
page does not claim otherwise. The wrapped assets (wXMR, wZEC) are the
opposite by construction, and `research/wrapper/README.md` "Trust statement" says so in its first paragraph —
*"Nothing here is 'trustless'"*: k of the n signers, colluding, can pay themselves the reserve or mint units against
deposits that never happened, and nothing on chain stops them. That is a custodian with a bond, and the app names
it as one. The swap has no such party. It has an economic weakness (§3), an unaudited proof, a Monero-depth
assumption, a public DERO leg (§5), a window in which each party's own machine must be running, and a deadline on
the taker's Punish (t2 + g2), past which, if no Punish came, the maker can freeze her XMR by never reclaiming — none
of which is a party who can take your coins, and "trustless" was
never a claim that nothing can go wrong. It is the claim that when something does, it is not because someone
else held your keys.

---

## 11. The screen

Not built in this build (build spec §7: "No app screen. E designs it; nobody codes it until `simhtlc` has run all
three exits"). When it is, it is one TELA page under the DOC cap (18,432 bytes per file), served from DERO with no
server behind it, reading the contract through the same `DERO.GetSC` path every other screen uses. It follows
`docs/DESIGN_SYSTEM.md`: black ground, cut corners, one lime diagonal marking what is live, lime only on an
action or a live clock.

**What the screen shows** — all of it read from the DERO chain, none of it typed by the user:

1. **The lock, verbatim.** Contract id, maker address, taker address, amount and asset, the three hashes, `t1`,
   `t2`, `g2`, and the state word (open / claimed / refunded / punished / reclaimed). Shown as the chain has them, so a user can
   compare them with what their companion prints. The app does not say "matches" — it cannot, since it holds
   nothing to match against; the companion does the matching (§1.3 step 2) and the page says so.
2. **The block clock.** Current DERO height, blocks to T1, blocks to T2, and the same in minutes at 18 s per block
   with the word *target* beside it. The live diagonal sits on whichever window is open now.
3. **Which exit is open, for whom.** Before T1: "Alice may claim (needs `s_a` and `k_b` — companion)". In [T1, T2):
   "Bob may refund (needs `s_b` — companion)". In [T2, T2 + g2): "Alice, or anyone for her, may punish (needs nothing
   but a wallet), until block T2 + g2 − 1". At or after T2 + g2: "Bob may reclaim (needs `s_b` — companion); Alice's XMR
   comes back only through it".
   Grey for the party you are not.
4. **The reveal, when there is one.** `xa_<id>` or `xb_<id>` in full once written — they are public, and the party
   who needs one reads it from here or from their companion, whichever they trust more.
5. **The Monero leg: "not visible from here."** One line, every time. The app cannot see Monero and does not
   pretend to; the companion's own output is where lock, confirmations and sweep are watched.
6. **The plaque, one tap away.** The trust statement of §10, in the same register as `app/notice.js`. Beside every
   wrapped balance the app shows a "federation-backed" mark with the signers one tap away; beside a swap it shows
   the opposite: *nobody holds these coins — here is what you are trusting instead.*
7. **The companion line.** The command to run, with the SCID and lock id filled in and every secret absent:
   `xmrswap alice -htlc <scid> -state <file> …`. A user copies this; they never paste anything back.

**What the screen never holds, never asks for, and never shows:**

- `s_a`, `s_b`, `k_b`, the private view key, the proof, the state file, or any `monero-wallet-rpc` address or
  credential. These live in the companion (§7) and nowhere else.
- **A Monero address to pay.** The joint address is computed by the companion from values that never enter the
  browser; a web page that says "send your XMR here" is exactly the shape of a phishing page, so this page does not
  have that shape. If the address is ever on screen, something is wrong.
- **A swap transaction.** In this build the companions send all four exits. Punish needs no secret and, since
  `htlc.bas` v2, may be sent by anyone, so the page could send it through XSWD; it is left with the taker's companion,
  which sends it at once from t2 and none past its cutoff before t2 + g2, so that there is one place that acts and one
  that shows. If that changes, this paragraph changes first.
- **A Monero balance, confirmation count, or "your XMR is safe."** It cannot know, so it does not say.
- **Anything about the counterparty beyond their DERO address**, which the chain shows anyway.

The screen is a window onto a public contract and a set of instructions for a program the user runs. That is the
whole of it, and the reason a swap can be trustless while the page that shows it is served from a chain.

---

## 12. Sources

Primary, read for this page (fetch dates as noted; the derohe files are the vendored clone at upstream HEAD
`e9df1205`):

- `docs/XMR_SWAP_BUILD.md` — the protocol (§1), interfaces (§3), invariants (§4), tests (§5).
- `research/crosschain/RESEARCH.md` §4.2 (observer view), §4.4 (the design), §4.6 (free option and bonds), "DVM
  primitives" (`SIGNER()` at ring size 2), "dero xmr" (the sketch and loss table).
- `research/wrapper/MECHANISM_REVIEW.md` §3 (verdict by pair), §4 (spore), §6 (the operator's ruling).
- `research/crosschain/SOURCES.md` — every external source the research rests on, with what each established.
- `research/crosschain/poc/proof-xmr/run.log` — 37,460 gates; prove 8.51 s and verify 0.74 s, each after about
  1.6 s building generators; 1,601 bytes
  (r1cs 1,441 + commitment 32 + sigma 128); wrong-H, wrong-S, tampered-sigma and other-secret rejections.
- `research/crosschain/poc/simswap-run.log` — a claim mined after expiry and refused by the contract.
- derohe `config/config.go` — `BLOCK_TIME = 18` (line 32), `STABLE_LIMIT = 8` (line 77);
  https://github.com/deroproject/derohe/blob/master/config/config.go
- derohe `blockchain/blockchain.go` — block refused below `chain height − STABLE_LIMIT` (lines 483–486);
  `Get_Stable_Height` (line 1139); https://github.com/deroproject/derohe/blob/master/blockchain/blockchain.go
- derohe `blockchain/transaction_execute.go:430` — signer extracted only at `RingSize == 2`; lines 397–404 — a
  failed call's value is returned to the signer when one can be extracted, otherwise "we burn all the funds";
  https://github.com/deroproject/derohe/blob/master/blockchain/transaction_execute.go
- derohe `dvm/sc.go:278–288` — a non-zero `RETURN` discards the call's changes and becomes an execution error
  ("Discarded knowingly"); https://github.com/deroproject/derohe/blob/master/dvm/sc.go
- `contracts/swap.bas` — the conventions `htlc.bas` extends; `tests/sendorder_test.go` — the send-order rule.
- `contracts/htlc.bas` v3 — Punish sendable by anyone and paying `t_<id>`, in [t2, t2 + g2) only (Punish line 25);
  Reclaim, the maker's from t2 + g2, storing `xb_<id>` as Refund does; the Lock width refusal `t1 < height + w1min ||
  t2 < t1 + w2min` (Lock line 26); the install arguments (Initialize lines 6-7, 13-15); Lock refusing a hash that is
  not 64 lower-case hex, and every exit refusing the lock's own asset attached, as in v2; `tests/htlc_v3_test.go`,
  `tests/htlc_v2_test.go`; `docs/HTLC_V3_COMPANION_NOTES.md`.
- `research/regtest/monero-smoke.log`, `tools/regtest/README.md` — monero-wallet-rpc 0.18.5.1 against the companion's
  calls in regtest: `generate_from_keys` does not check keys against the address, a view-only wallet's `transfer`
  answers an `unsigned_txset` and sends nothing, `set_daemon` holds only for the open wallet, `get_height` is the
  wallet's scanned height, a non-zero `unlock_time` is refused (-50).
- `research/regtest/xmrswap-e2e.log` — all three exits run twice with real `monerod` and `monero-wallet-rpc` 0.18.5.1
  on regtest and `htlc.bas` v2 on the DERO simulator, every txid looked up on its chain; the second run with the
  companions naming the `monerod` after every open (55 `set_daemon` calls accepted, none refused). Run 3 is the same
  three exits on `htlc.bas` v3. Run 4 (2026-09-12) is all four on v3, the maker's Reclaim included: nobody punished,
  Bob, back at his refund cutoff, sent no Refund, reclaimed from t2 + g2 and counted it once stable, and Alice,
  restarted after t2 + g2, read `s_b` from `xb_<id>` (st 4) and swept her XMR back. The contract held 0 after every
  exit. No run is testnet or mainnet.
- `research/regtest/xmrswap-reorg.log` — a real reorganisation of the regtest chain under a live swap (MT-B11):
  `pop_blocks`, `flush_txpool` and a longer branch, once below Bob's depth before the gate and once after it with
  Alice spending the lock's input again; every txid, block hash and balance, and what each companion did.
- `research/regtest/xmrswap-cli.log` — the `xmrswap` command line as two processes on regtest and the simulator, with
  `-monerod`, logins and the channel key from the environment, the witness check and the recovery card and wake;
  run 2 (2026-09-12, on the restored simulator, `-htlc` taken from the record) went through Bob's sweep. Run 1 was
  interrupted after Alice's Claim by a stall of the companion's own — every state save's `icacls` failed once the
  console the companions had inherited was gone — fixed by setting the owner-only ACL (GAP-020) through the Windows
  API, with no program started (`tools/xmrswap/acl_windows.go`; `TestOwnerOnlyACLStartsNoProgram`).
- `research/regtest/xmrswap-scan.log` — the joint wallet's restore and scan times on the regtest wallet-rpc (MT-B13).
- `docs/CROSS_CHAIN_TRUST_MODEL.md` §5 — a lying node that colludes with the counterparty costs a whole leg.
- monero-project/monero `src/cryptonote_config.h` (fetched 2026-09-08) — `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE 10`,
  `DIFFICULTY_TARGET_V2 120`, `CRYPTONOTE_MINED_MONEY_UNLOCK_WINDOW 60`; no reorganisation-depth constant;
  https://raw.githubusercontent.com/monero-project/monero/master/src/cryptonote_config.h
- monero-project/monero `src/cryptonote_basic/cryptonote_basic.h` (fetched 2026-09-08) — `transaction_prefix::unlock_time`,
  "spend this tx not early then block/time";
  https://raw.githubusercontent.com/monero-project/monero/master/src/cryptonote_basic/cryptonote_basic.h
- monero-project/monero `src/cryptonote_basic/cryptonote_basic_impl.cpp`, `src/common/base58.cpp`,
  `src/crypto/crypto.cpp` — address encoding and scalar reduction (cited by the build spec §3.C);
  https://github.com/monero-project/monero/tree/master/src
- Monero wallet-RPC reference (fetched 2026-09-08) — `generate_from_keys` is view-only when `spendkey` is omitted;
  `get_transfers` entries carry `confirmations`, `height`, `locked`, `unlock_time`,
  `suggested_confirmations_threshold`; `sweep_all`; https://docs.getmonero.org/rpc-library/wallet-rpc/
- Monero, "A Post Mortem of The Burning Bug", 2018-09-25 — duplicate one-time keys, one spendable output, wallet
  warning; https://www.getmonero.org/2018/09/25/a-post-mortum-of-the-burning-bug.html
- COMIT `xmr-btc-swap` — `swap/src/protocol/bob/state.rs` (`s_a + s_b`), `swap/src/env.rs:53–54` (72-block
  mainnet timelocks); https://github.com/comit-network/xmr-btc-swap (archived; successor
  https://github.com/eigenwallet/core)
- Farcaster RFC 01 — "Alice always locks her coins later in the swap process, implying that she gets an option to
  buy without cost"; https://github.com/farcaster-project/RFCs
- Gugger, *Atomic Swaps between Bitcoin and Monero*, arXiv:2101.12332 — https://arxiv.org/abs/2101.12332
- spore — https://github.com/liqdmetal/spore (HEAD `254beba`, 2026-09-07); `PAYLOAD0_LIMIT = 144` from derohe.

Secondary, and marked so wherever used:

- Monero block time and confirmations, and the 18-block reorganisation report (2026):
  https://blog.monerica.com/articles/monero-block-time-target ;
  https://blog.monerica.com/articles/how-many-confirmations-for-monero ;
  https://www.monero.how/monero-transaction-confirmation-time ;
  https://intercom.help/stealthex/en/articles/12519146-monero-xmr-confirmations-why-more-are-needed-now-recent-network-risks
- XMR/DERO volatility "around 6 %" — assumed in RESEARCH.md §4.6, not measured.
