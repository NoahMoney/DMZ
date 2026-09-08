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
system with no trusted setup, 37,460 multiplication gates, measured at 14.8 s to prove and 1.15 s to verify,
1,601 bytes (`run.log`). Each party verifies the other's proof before anything is locked (build spec §4,
invariants 3 and 4).

**The joint address.** Spend public key `P = S_a + S_b`, view public key `V`, encoded as a standard Monero
address (network byte ‖ P ‖ V ‖ keccak256(…)[0:4], Monero base58 — `src/cryptonote_basic/cryptonote_basic_impl.cpp`
and `src/common/base58.cpp` in monero-project/monero). Its spend private key is `s_a + s_b mod ℓ`. Nobody holds it
until one of the reveals in §2 happens, and after that exactly one person does.

**The gate secret `k_b`.** Without it, Alice could claim the DERO the moment Bob locked, never having locked any
XMR. Bob hands `k_b` to Alice only after his own view-only wallet has seen her XMR at the joint address with at
least ten confirmations (§4). A "claim delay" in place of the gate was reviewed and rejected on 2026-09-06: a delay
lets Bob *notice* a claim without an XMR lock; it does not let him stop it (build spec §1).

### 1.3 On chain, step by step

1. **Bob locks DERO** in `htlc.bas`: `Lock(ha, hk, hb, taker = Alice, t1, t2, asset)` with the DERO (or a
   DERO-native asset) attached to the call.
2. **Alice reads the lock from the chain** and checks every field against what she holds and what Bob committed
   to over the channel — `ha` is the hash of her own `s_a`; `hk` is the `H_k` Bob sent; `hb` is the `H_b` Bob sent
   and `π_b` verified against it; `taker` is her address; `t1 − now ≥ T1_MIN`; `t2 − t1 ≥ T2_MIN`; the amount and
   asset are what was agreed (build spec §4, invariant 3). Only then does she **send XMR to the joint address**.
3. **Bob watches the joint address** through a view-only wallet (`generate_from_keys` with the view key and no
   spend key). At ten or more confirmations of at least the agreed amount, read from his own wallet and not from
   anything Alice says, he sends Alice `k_b` over the channel (invariant 2).
4. **Alice claims** the DERO: `Claim(id, s_a, k_b)`. The contract stores `s_a` under `xa_<id>`, where anyone can
   read it.
5. **Bob reads `s_a`** from the contract, forms `s_a + s_b`, imports the joint wallet with that spend key
   (`generate_from_keys`, this time with the spend key) and **sweeps** the XMR to his own address (`sweep_all`).

### 1.4 The names the contract uses

For each lock `id`, `htlc.bas` stores `m_<id>` (maker, Bob), `t_<id>` (taker, Alice), `ha_<id>`, `hk_<id>`,
`hb_<id>` (the three hashes, 64 hex characters each), `a_<id>` (asset, empty for DERO), `v_<id>` (amount),
`t1_<id>`, `t2_<id>` (absolute DERO heights), `st_<id>` (0 open, 1 claimed, 2 refunded, 3 punished), and after a
reveal `xa_<id>` (`s_a`) or `xb_<id>` (`s_b`). The two revealed keys never coexist for one lock, because every exit
requires `st == 0` and sets it to something else (build spec §3.A). That mutual exclusion is the whole safety
argument of §2 in one line.

---

## 2. The three exits, and who ends with what

| Path | Condition | Who calls | Reveals | Result |
|---|---|---|---|---|
| **Claim** | height < T1, `SHA256(s_a) = H_a`, `SHA256(k_b) = H_k`, caller is the taker | Alice | `s_a` | Alice: DERO. Bob: XMR (via `s_a + s_b`). The swap. |
| **Refund** | T1 ≤ height < T2, `SHA256(s_b) = H_b`, caller is the maker | Bob | `s_b` | Bob: DERO back. Alice, if she locked: XMR back (via `s_a + s_b`). |
| **Punish** | height ≥ T2, caller is the taker | Alice | nothing | Alice: the DERO. Bob: nothing — he had [T1, T2) to refund and did not. Any XMR Alice locked stays under the joint key forever (she is paid in DERO; Bob cannot take it without `s_a`). |

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

### 2.1 What each party can lose, by state reached

| State reached | Alice | Bob |
|---|---|---|
| Bob locks; Alice never locks | nothing (she walked; §3) | two fees, ≥ 400 blocks of time |
| Alice locks; Bob never releases `k_b`; Bob refunds at T1 | Monero fees, ≥ 400 blocks with XMR locked; then XMR back | two DERO fees, the time |
| Alice locks; Bob releases `k_b`; Alice claims | nothing | nothing: the trade |
| Alice locks; Bob releases `k_b`; Alice never claims; Bob refunds at T1 | Monero fees; XMR back after the refund | two DERO fees, the time |
| Alice locks; Bob neither releases nor refunds; Alice punishes at T2 | her XMR is gone; she holds the DERO she bought | the DERO |
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
| Monero confirmations before `k_b` | 10 | Monero itself refuses to spend an output younger than this, so no smaller number is even meaningful; below it the output could be unwound after Bob has released the gate | `CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE 10`, `src/cryptonote_config.h` |
| Monero reorganisation bound | none | `cryptonote_config.h` contains no constant limiting reorganisation depth (checked 2026-09-08); the 10-block spend age is a spend rule, not a finality rule | `src/cryptonote_config.h` |
| the deepest Monero reorganisation this project knows of | 18 blocks (≈ 36 min at target), 2026 | reported by exchanges and swap services; **secondary sources only**, no primary record found (`research/crosschain/README.md`, "Monero timing and reorg: secondary sources only. Flagged.") | SOURCES.md row "Monero timing" |
| `T1_MIN` | 400 DERO blocks (≈ 2 h) after the lock | Alice's whole path has to fit inside it: see the budget below | build spec §1; a design margin, not a measured interval |
| `T2_MIN` | T1 + 400 blocks (≈ 2 h more) | Bob's refund window; long enough that a Bob who is merely offline for an hour is not punished | build spec §1 |
| contract bounds | `now + 1 < t1 < t2 ≤ now + 1,000,000` | ordering only; the contract cannot know Monero's state, so the minimums are policy in the companion, which refuses a lock below them | build spec §3.A |

**Alice's budget inside T1, at target rates.** Bob's lock reaches stable depth: 8 DERO blocks, 144 s. Her XMR
transaction is mined: one Monero block expected, 120 s. Ten confirmations: 1,200 s. Bob's watcher notices and
his companion sends `k_b`: a poll interval plus one channel delivery (over spore, one DERO transaction, about a
block). Her Claim is mined and reaches stable depth before T1: 9 DERO blocks, 162 s. That is roughly half an hour
of a two-hour window, at target rates. Block intervals are targets, not promises, and a stuck Monero transaction
(fee too low, a full mempool) spends this budget with nothing to show; §9 R1 and R4 are what happens when it runs
out, and the answer is "nobody loses coins".

**For comparison**, COMIT's mainnet cancel and punish timelocks are 72 Bitcoin blocks each (`swap/src/env.rs:53–54`),
about twelve hours at Bitcoin's ten-minute target. Our windows are a sixth of that, and the reason we can afford
it is that DERO's finality is a consensus rule at eight blocks rather than a probability.

**What the numbers are not.** They are margins derived from source constants, not from measured mainnet
intervals; `research/crosschain/README.md` marks the timelock row "weakened … adequate for design margins, not
for a spec". Ten Monero confirmations is the *floor* — the smallest number at which anyone can spend the output —
not a number chosen for safety. For a large amount the companion should use more (the wrapper design uses 20
above 50 XMR; `research/wrapper/DESIGN.md`, "Confirmation policy"), and T1 must grow with it. The 18-block figure
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

**R3. A Monero reorganisation after the gate.** Bob sends `k_b` at ten confirmations. Monero then reorganises
deeper than ten and Alice's lock transaction does not return — because its inputs were in the displaced blocks
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
open, so it has to be closed before the build is called done.

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
(`height ≥ t2`). *Covered:* yes, by the contract; the only way this becomes a race is R2.

**R11. Bob vanishes after Alice locked.** No gate, no refund. *Outcome:* at T2 Alice punishes and takes the DERO.
Her XMR is under a key nobody will ever complete. She has what she agreed to buy; Bob has nothing. *Covered:* yes;
this is what Punish is for (§2).

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
scalar modulo ℓ (`src/crypto/crypto.cpp`). A secret at or above ℓ would hash to one value on DERO and act as a
different scalar on Monero. *Outcome:* a sweep with the wrong key. *Covered:* yes; `proof-xmr keygen` produces a
reduced scalar and `prove` refuses an unreduced one rather than silently reducing (build spec §3.B, and a test
that must exist).

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

**R18. A party's own node lies.** Alice reads Bob's lock from a DERO node that shows her a lock that is not in the
chain; she locks XMR against nothing. Or Bob's `monero-wallet-rpc` talks to a daemon that shows a confirmed deposit
that does not exist; he releases `k_b` and Alice claims real DERO for no XMR. *Outcome:* a loss, and one the
protocol cannot see. *Covered:* **not by the protocol, and it cannot be.** It belongs in §10: each party trusts the
node they read from. The app already refuses non-loopback plaintext endpoints (`app/rpc.js`, `V.endpointURL`); the
companion takes `-daemon` and `-monero` as flags and should warn, loudly, when either is not loopback.

**R19. Heights are not clocks.** T1 and T2 are DERO heights; Alice's wait is Monero confirmations; the two are
coupled only through wall time. If DERO blocks run fast for an hour, Alice's two hours are shorter than she
displayed; if Monero runs slow, her ten confirmations eat more of them. *Outcome:* R1 or R4 — no coins lost, a
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
sits unmined into T1. *Outcome:* R1. *Covered:* operationally; the companion should re-send with a higher fee while
`now + STABLE_LIMIT + 1 < t1` and stop after.

The pattern across all of these: every outcome that costs a party coins is either (i) that party's own choice
or mistake (R8, R11 for Bob, R16's burned Lock, R23), (ii) a skipped proof (R13, R14), (iii) a lie from a node
that party chose to read from (R18), or (iv) one of two gaps the build spec must close before "done" — the
last-eight-blocks rule (R2) and the `unlock_time` check (R6) — plus the Monero-depth assumption (R3), which cannot
be closed, only stated and sized.

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
  (§9 R18). Run your own, or name the one you trust and know that you are trusting it.
- **The companion's code**, which holds your share and the gate. Read it or trust whoever did.

**What is not trusted.**

- **The operator of DMZ.** They host nothing for a swap, hold no key that touches it, and could not take or
  freeze either side's coins if they wished to. `htlc.bas` has no owner, no admin, no fee and no update path.
- **Any signer set.** There is none. Nothing is wrapped; nothing is held in reserve; nothing is minted.
- **The counterparty, once your checks have passed.** Before their lock, they can walk (§3). After both locks, the
  only choices they have are the three exits, and every one of them leaves you whole or paid (§2): Bob can decline
  the gate and refund, which returns your XMR; he can do nothing, which costs him the DERO; Alice can decline to
  claim, which returns Bob's DERO. The one counterparty action that would freeze your coins — a lock with a
  far-future `unlock_time` — is §9 R6, and it must be closed by a check before this is used.
- **The channel.** Needed for the swap to happen, not for your coins to be safe (§8, §9 R17).

**Why "trustless" is justified here and not for the federation.** The word means one thing on this page: *no
party — operator, signer set, or counterparty — can take or freeze either side's coins.* Here that is true by
construction: the DERO is in a contract with no owner whose three exits are exhaustive and mutually exclusive,
and the XMR is under a key that only the protocol's own reveals complete. The wrapped assets (wXMR, wZEC) are the
opposite by construction, and `research/wrapper/README.md` "Trust statement" says so in its first paragraph —
*"Nothing here is 'trustless'"*: k of the n signers, colluding, can pay themselves the reserve or mint units against
deposits that never happened, and nothing on chain stops them. That is a custodian with a bond, and the app names
it as one. The swap has no such party. It has an economic weakness (§3), an unaudited proof, a Monero-depth
assumption, and a public DERO leg (§5) — none of which is a party who can take your coins, and "trustless" was
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
   `t2`, and the state word (open / claimed / refunded / punished). Shown as the chain has them, so a user can
   compare them with what their companion prints. The app does not say "matches" — it cannot, since it holds
   nothing to match against; the companion does the matching (§1.3 step 2) and the page says so.
2. **The block clock.** Current DERO height, blocks to T1, blocks to T2, and the same in minutes at 18 s per block
   with the word *target* beside it. The live diagonal sits on whichever window is open now.
3. **Which exit is open, for whom.** Before T1: "Alice may claim (needs `s_a` and `k_b` — companion)". In [T1, T2):
   "Bob may refund (needs `s_b` — companion)". At or after T2: "Alice may punish (needs nothing but her wallet)".
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
- **A swap transaction.** In this build the companion sends all three exits. Punish is the one that needs no secret
  and could be sent through XSWD from the page; it is left with the companion so that there is one place that
  acts and one that shows. If that changes, this paragraph changes first.
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
- `research/crosschain/poc/proof-xmr/run.log` — 37,460 gates; prove 14.85 s; verify 1.15 s; 1,601 bytes
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
