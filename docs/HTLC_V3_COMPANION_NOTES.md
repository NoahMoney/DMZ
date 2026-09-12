# htlc.bas v3: what the Monero companion must change

*2026-09-12. For the engineer who owns `tools/xmrswap`, `docs/XMR_SWAP.md` and `docs/XMR_SWAP_BUILD.md`, from the
contracts pass that built `contracts/htlc.bas` v3. The operator's rulings behind it are:
- MT-J13(b), which is `docs/ATOMIC_SWAP_STUDY.md` §5 decision 17 (a) (U13);
- MT-A03(a): the minimum widths enforced in the contract.

When this note was written, nothing under `tools/xmrswap`, nor either of those two documents, had been touched, and
`go test ./xmrswap` failed at `TestPinnedCodeIsTheTreesHtlc` by design, because the pin was still v2's hash.*

**Status, later on 2026-09-12: the companion has adopted v3.** `tools/xmrswap` pins `b785028d…`, reads and enforces
the three install arguments, builds no Lock past its cutoff, sends no Punish past its cutoff, sends Reclaim and watches
it to stable depth, and treats st 4 as st 2; both documents describe v3. The simulator's recorded htlc is now a v3
install (§1). §5 and §6 are the list that adaptation worked through, kept as written; §3, §4 and §7 have been corrected
where they said a transaction executes up to 11 blocks after it is built (at most 10) and where they promised the
taker her XMR back unconditionally.

---

## 1. The new canonical hash

| | v2 (pinned before) | v3 (pinned now) |
|---|---|---|
| `HTLCCodeSHA256` (`tools/xmrswap/pin.go`) | `44864ec3cddac871286b9cfd18d18e0b1d2ef0e31960436cee3bacc3025fef06` | **`b785028da4a2554521d47f989f1011ec1f0a26be491e6a7eb2786c1f50834c36`** |
| canonical bytes, `reference.Canon(contracts/htlc.bas)` | 3,561 | 5,104 (11,206 raw) |
| `v` in storage | 2 | 3 |

The v3 hash is sha256 of `reference.Canon(contracts/htlc.bas)`, which is exactly what `pin_test.go` recomputes. The
method was checked by reproducing v2's `44864ec3…` from `git show HEAD:contracts/htlc.bas`.

**The same code installed with other install arguments has the same hash.** The pin therefore no longer identifies a
deployment on its own: the companion must also read the three install arguments (§3).

Elsewhere:
- **`tools/simhtlc`** installed `htlc.bas` with no `Initialize` arguments, which v3 refuses, because the DVM refuses a
  call with a missing parameter; this pass left it alone. *Now:* it takes `-htlc-w1min`, `-htlc-w2min` and `-htlc-g2`
  for an install, or reads the install's own values with `-htlc`, and compares the installed code with
  `xmrswap.HTLCCodeSHA256`.
- **The simulator's recorded htlc** was v2's `9422717f…` when this note was written, and was not replaced, as
  instructed. *Now:* `deploy/networks.json` `simulator.htlc` records
  `ef576fce2edaa15c3e09916463ea86a49bfb428ca030d439e69a63a48a612496`, a v3 install with `w1min` 400, `w2min` 400 and
  `g2` 4,800, the profile's values (`tools/regtest/README.md`, the stagenet swap run), and
  `tools/xmrswap/record_gen.go` carries the same scid.
- **Error text.** The companion's refusal said "not htlc.bas v2"; it now says "not htlc.bas v3" (`swap_v2_test.go`,
  `TestALookAlikeContractIsRefused`).

## 2. Entrypoints, v2 → v3

| Entry | v2 | v3 |
|---|---|---|
| `Initialize` | `Initialize()` | **`Initialize(w1min Uint64, w2min Uint64, g2 Uint64)`**. Each is refused at 0 and above 1,000,000, and `w1min + w2min` above 999,999 (`htlc.bas` lines 6-7) |
| `Lock(ha, hk, hb, taker, t1, t2, asset)` | same arguments | same arguments, plus **one new refusal**: `t1 < height + w1min` or `t2 < t1 + w2min` (line 26). `height` is the block the Lock *executes* in, not the one it was built at (§3) |
| `Claim(id, sa, kb)` | taker, before `t1` | unchanged |
| `Refund(id, sb)` | maker, [`t1`, `t2`) | unchanged, byte for byte |
| `Punish(id)` | anyone, from `t2`, **for ever** | anyone, **[`t2`, `t2 + g2`) only**. Refused from `t2 + g2` (line 25) |
| `Reclaim(id, sb)` | — | **new.** Maker only (`SIGNER() == m_<id>`), from `t2 + g2` with no end. `sb` is 64 hex characters of 32 bytes whose sha256 is `hb_<id>`. Nothing may be attached: no DERO, and not the lock's asset. Pays `v_<id>` to the maker, in DERO or in the asset. Stores `st_<id> = 4` and **`xb_<id> = sb`, the key Refund uses** |

**Storage:**
- new top-level keys `w1min`, `w2min` and `g2`;
- new state `st_<id> = 4`, reclaimed;
- every per-lock key is unchanged.

**Gas** (DVM suite, `tests/htlc_v3_test.go`):
- **Install:** 5,177 storage gas at `w1min` 400, `w2min` 400 and `g2` 2,400; 5,168 to 5,180 across the tested
  arguments. v2 was 3,598. All are far under the 20,000 cap.
- **Reclaim:** 231 storage gas and 434,600 compute gas, the size of a Refund. For comparison, Claim is 304 / 537,800
  and Punish 90 / 322,200.
- **Lock, Claim, Refund and Punish:** no per-lock key was added, so their storage gas is unchanged. Claim and Refund
  are v2's code byte for byte, so their compute gas is too, and the Claim gas that sizes the stipend (MT-K03) does
  not move. Lock reads `w1min` and `w2min`, and Punish reads `g2`, so each costs a little more compute than in v2.

## 3. Install arguments, and what each side must check

The Monero profile supplies the values: `w1min` = 400 (`t1_min`) and `w2min` = 400 (`t2_minus_t1`). `g2` has no
chosen value (§7). `simdeploy` requires `-htlc-w1min`, `-htlc-w2min` and `-htlc-g2`, none with a default. It refuses
before installing anything a value the contract would refuse (`tools/simdeploy/htlcwindows_test.go`).

**Read them.** Before trusting a deployment, read `w1min`, `w2min` and `g2` from `DERO.GetSC`, in the same read as
the code, confirmed by every configured node (MT-B09). Then:

- **Alice (the taker).** `g2` is her Punish window. She refuses a deployment whose `g2` is below her policy floor. The
  proposal is the profile's `g2`, and never below the 19-block floor in §7. `w1min` and `w2min` only add refusals to
  her own per-lock checks; she needs them only to turn away an offer that the contract would refuse.
- **Bob (the maker).** He sets `t1 = IntentHeight + T1Delta` (`swap.go`, phase `verified_pi_a`). DERO mines a
  transaction at most 10 blocks after it is built — a block takes one only while its height minus the transaction's
  height (the wallet's tip when it built it) is below `TX_VALIDITY_HEIGHT` = 11 (`vendor/derohe/blockchain/
  blockchain.go:748`, `miner_block.go:275`) — and Lock compares `t1` with the height it executes at. This note first
  said "up to 11 blocks"; the companion still counts 11 (`DeroTxValidity`), one block conservative. So **his `T1Delta`
  must be at least `w1min + 11`**, and his `T2Delta` at least `w2min`. Otherwise his Lock is refused on chain: the
  value is refunded and the fee is lost.
  - At the profile's `t1_default` of 420 there are 20 blocks above `w1min`, and 9 above the floor below.
  - At `t1_min` 400, which equals `w1min`, there is none: a Lock built at `T1Delta` 400 is refused whenever it lands
    a block late. `TestHtlcV3LockWidths` shows both cases at 400/400.
  - One of these two had to change: make Bob's floor `max(T1Min, w1min + 11)`, or install `w1min` at 389 and keep
    `t1_min` at 400. The contract enforces whatever is installed. *Adopted:* the first (`deploymentRefusal`,
    `lockCutoff`), 411 at the profile. 411 is the contract's least with no slack: at exactly 411 a Lock can be built
    only at the intent height itself, so one block arriving between the saved decision and the Lock's own height read
    makes Bob refuse (`ErrLockTooLate`, nothing sent), and Alice's first-sight check (`t1 − now ≥ T1Min`) leaves the
    Lock 11 blocks to be mined and read. Nothing is lost either way beyond fees and time. The profile's default of 420
    is what leaves room: 10 heights to build at, and 20 blocks for her check.
  - **The daemon's estimate will not catch a narrow Lock.** `DERO.GetGasEstimate` runs the contract at height 0
    (`dvm.Simulator.height` is never set, as `dero.go:98-100` already records). There, `t1 < 0 + w1min` holds only
    for a `t1` below `w1min`, so a Lock that the chain will refuse passes its estimate. Seen live on the simulator:
    an estimate of a Lock 9 blocks wide was accepted with `w1min` 10 (§8). Bob's own check is the only one before
    the fee is spent. The `w2min` check does not depend on the height, so the estimate catches that one: seen live,
    refused.
- **`tools/xmrswap/mainnet_profile.json`** gains `g2`, pending MT-J13's value, and a test that checks the rule above
  at every tier.

## 4. The new window, and what each side does

```
lock ─────── t1 ─────────── t2 ──────────────── t2 + g2 ──────────────────────────▶
   Claim (Alice)   Refund (Bob)    Punish (anyone, pays Alice)    Reclaim (Bob, reveals s_b), no end
```

**Alice.** Her Punish now has a deadline, which is the study's named cost.

- **Punish at once.** From `t2`, if the lock is still open (st 0), send Punish immediately. A Punish built at height
  `h` is mined by `h + 10` or never (this note first said `h + 11`) and must execute before `t2 + g2`, so she must not
  build one after `t2 + g2 − 11`; counting 11, as the companion does, `t2 + g2 − 12`. It counts as done only when 8
  deep. A cutoff of `t2 + g2 − (11 + 8 + margin)` is the point after which she stops
  sending and waits for Reclaim instead. After `t2 + g2`, a restart that finds st 0 must not send Punish: the contract
  refuses it, and the fee is wasted.
- **Anyone can send it for her.** Punish carries no secret and pays only her, so a relayer or a second device can
  send it. The recovery card and wake (U9, MT-B15) should schedule a wake at `t2`, not only at `t1`, and print the
  Punish window's last block and its wall-clock time.
- **If nobody punished, she recovers only if Bob reclaims.** From `t2 + g2` the lock is Bob's to Reclaim. She watches
  for **st 4 as well as st 2**: both leave `s_b` in `xb_<id>`, with the same check `sha256(xb) == hb`. She then
  recovers her XMR with `s_a + s_b`, with no deadline. In her state machine, st 4 is terminal exactly as st 2 is. But
  nothing makes Bob reclaim: one who never does — hostile, or having lost his state or `s_b`, a maker the contract's
  header says cannot leave — leaves her XMR and his DERO locked for ever. Under v2 she could Punish at any time after
  `t2`; this dependence on him is new in v3, and only a Punish in [`t2`, `t2 + g2`), from her or anyone she asks, rules
  it out. (This bullet first said "she still recovers", without that condition.)

**Bob.**

- **He loses only to a timely Punish.** Once `t1` has passed without a Claim, he loses the lock only if someone
  Punishes in [`t2`, `t2 + g2`).
- **A late recovery path.** If he missed Refund and the lock is still open at `t2 + g2`, he Reclaims with `s_b`: same
  arguments as Refund, a different entrypoint. His companion needs this new path and a wake at `t2 + g2`. Invariant 9
  (never act on an unstable Claim; never Refund after reading `s_a`) applies to Reclaim exactly as to Refund.
  *Built:* the Reclaim from `t2 + g2`, counted only once 8 deep and sent again if a replaced block undoes it (phase
  `reclaim_confirming`). *Not built:* a wake for the maker; his companion registers none, and must be running at
  `t2 + g2`.
- **Reclaim cannot be estimated.** At the estimator's height 0 it is always refused (`0 < t2 + g2`), so it goes out at
  `FixedFee` (3,000) as Refund and Punish do. It measured 231 storage gas, and it writes the same two keys Refund does.
  Punish was always sent at `FixedFee`; what v3 changes for it is that from `t2 + g2` the chain refuses it too, so a
  Punish that lands late is mined, refused, and costs its fee (seen live, §8).
- **Accounting.** For his accounting, st 4 is a refund.

**Unchanged:** the gate, `GateMargin`, the claim cutoff and every Claim-side rule.

## 5. Tests and code in `tools/xmrswap` that encode v2

- **`pin.go` / `pin_test.go`:** the hash (§1).
- **`fakes_test.go`:** the in-memory `fakeChain` implements v2.
  - Punish must be refused from `t2 + g2`, and Reclaim added (line 195's `punish`).
  - The fake's `codeText` is `Function Initialize() Uint64`.
  - Lock must refuse the new widths.
- **`swap_v2_test.go`:**
  - the look-alike refusal text at lines 327 and 339;
  - every Punish scenario, including the "v1 left it open for ever" test at line 396.
- **`dero_test.go:495`:** the third lock is punished after `t2`; it must now fall inside `g2`.
- **`adversary_test.go:66`:** a `huge` t1. Also check the permissionless junk locks (line 274): each now has to meet
  `w1min` and `w2min`.
- **`swap_repair_test.go:150`:** `XMRSWAP_SIM_SCID` must name a v3 install.
- **`cmd/xmrswap/main_record_test.go`:** the pinned-code refusal.
- **`dero.go`:** a `Reclaim` call, with the same argument shape as Refund (`id` Uint64, `sb` String). It also needs
  the reads of `w1min`, `w2min` and `g2`.

## 6. Statements in your documents that v3 makes false

- **`docs/XMR_SWAP.md`:**
  - "Punish from `t2`" with no end;
  - "three exits";
  - the LIV-07 paragraph. Its test, `TestHtlcPastT2AnyoneClosesTheLockForTheTaker` (line 136), is now
    `TestHtlcPastT2TheLockAlwaysHasAnExit`.
  - §10's trust statement must name the taker's Punish deadline.
- **`docs/XMR_SWAP_BUILD.md`:**
  - §1 and §3.A describe a three-exit `htlc.bas`;
  - they say the minimum widths are companion policy, not the contract's (MT-A03(a) moved them into the contract).

## 7. G2: a proposal, not a decision

**Proposed: `g2` = 4,800 blocks, 24 hours at 18 s a block.**

**What `g2` decides.** It sets how long the taker, or anyone on her behalf, has to Punish a maker who missed his own
[`t1`, `t2`) window, before the lock turns back into his. It matters only in that case: after a Refund or a Claim the
lock is closed, and `g2` never comes into play.

**What a long `g2` costs:**
- A maker who already missed his 400-block refund window waits `g2` more after `t2` to take his lock back.
- In the sub-case where nobody punished, the taker's XMR recovery waits the same, since it needs the `s_b` his Reclaim
  reveals.
- Nobody else pays anything. At the profile ceilings, the reclaim height `t2 + g2` is at most the lock's height +
  2,400 + 4,800 blocks, about 36 hours.

**What a short `g2` costs:**
- A taker who is away for `g2` blocks from `t2` loses the punishment payment, and her XMR then waits on the maker: she
  recovers it once he reclaims, and not at all if he never does (§4).
- It also lowers the risk to a maker who skips his refund on purpose and hopes nobody punishes in time. He cannot
  profit from that, since at best he gets back what a refund would have given him, but a short `g2` makes the
  griefing nearly free.

**The floor.** Below about 19 blocks (the 11-block validity window plus the 8-block stable depth), a correctly sent
Punish can miss the window. Below a few hundred, "have a machine on at a stated time" becomes a race.

| `g2` | wall clock | Protects a taker away for | A missed-refund maker waits after `t2` |
|---|---|---|---|
| 400 | 2 h | an errand; the same as the maker's own refund window | 2 h |
| 2,400 | 12 h | a night | 12 h |
| **4,800** | **24 h** | **a day, which is the absence people actually have** | **24 h** |
| 9,600 | 48 h | a weekend | 48 h |

**Why 24 hours.** It covers the ordinary daily absence of a person who is not running a server. It makes skipping a
refund on purpose a losing bet for the maker. Its whole cost falls on the party who missed his own window.

**If the operator wants symmetry,** 400 matches the maker's window. It would be a deliberate choice to protect the
taker no more than the maker.

## 8. What was verified in this pass

- **The DVM suite, green:** 175 tests passed, 0 failed (`tools/withlock.sh dvm bash tests/run.sh -v`, 2026-09-12). The
  v3 tests are in `tests/htlc_v3_test.go`:
  - `TestHtlcV3InstallArguments`, `TestHtlcV3LockWidths` and `TestHtlcV3Reclaim`;
  - `TestHtlcV3WindowMatrix`: every exit by the taker, the maker, a stranger and a ring>2 caller, at `t1 − 1`, `t1`,
    `t2 − 1`, `t2`, `t2 + g2 − 1`, `t2 + g2`, `t2 + g2 + 1` and ten million blocks on;
  - `TestHtlcV3OnlyTheMakersOwnWindowStaysOpen`.

  Beside them:
  - `TestHtlcPastT2TheLockAlwaysHasAnExit` (`zzadversarial_liveness_test.go`);
  - the conservation run (`zzconservation_htlc_test.go`), which now includes Reclaim, the Punish deadline and the width
    refusals, and requires every one of the four exits to have happened.
- **`tools/simdeploy`:** `TestHtlcWindowBoundsAreTheContracts`.
- **On the simulator: a v3 scratch install, recorded nowhere.** SCID
  `c0a958551bf5d5c2e95c6a28c75186893432c26eb757d3afd7dff80a641b28fd`, installed from wallet 30017 with `w1min` 10,
  `w2min` 2 and `g2` 3. It measured 5,168 storage gas, and the code hash on chain is `b785028d…`. The simulator's
  recorded htlc (`9422717f…`, v2) was not changed by this pass; it has since been replaced by `ef576fce…` (§1). Logs
  are in `.sim/htlc-v3-live-0912.log` and
  `.sim/htlc-v3-live2-0912.log`, which git ignores.
  - **An install without the three arguments** was refused at the estimate: "entrypoint 'Initialize' parameter type
    missing".
  - **A Lock estimate with `t2 − t1` = 1** was refused.
  - **A Lock estimate 9 blocks wide**, against `w1min` 10, was *accepted*. This is the height-0 estimator described in
    §3.
  - **Lock 0:** 1,000 atomic units, `t1` 627, `t2` 629, mined at height 612.
  - **The taker's Punish after `t2 + g2`,** sent without an estimate at a fee of 3,000, was mined at height 635 and
    refused (`st_0` stayed 0).
  - **The maker's `Reclaim`,** at a fee of 3,000, was mined at height 637: `st_0` is 4, `xb_0` equals `s_b`, and the
    contract holds 0.
  - **Lock 1:** `t1` 650, `t2` 652, mined at height 639. A stranger's Punish (wallet 30019, a fee of 3,000) was mined at
    height 653, inside [652, 655): `st_1` is 3, the taker was paid, and the contract holds 0.
