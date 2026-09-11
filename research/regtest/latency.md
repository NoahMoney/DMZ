# Swap latency on regtest (MT-M01)

*Measured 2026-09-11 from the committed end-to-end logs: `research/regtest/xmrswap-e2e.log` (two runs of all three
exits) and `research/regtest/zecswap-e2e.log` (swaps A, B and C). Nothing was re-run for this page. Times come from
the log lines, to the second. Heights come from the txid sections and the status lines.*

> **These are not mainnet latencies.** Monero and Zcash ran as regtest chains whose blocks the test drivers mined on
> demand, every few seconds: Monero took 38–50 s for 10 blocks, and Zcash about 5.6 s a block in swap C. Mainnet
> targets are 120 s (Monero) and 75 s (Zcash), so every step that waits for foreign confirmations takes one to two
> orders of magnitude longer there.
>
> DERO ran on the local simulator. It aims at DERO's 18 s block but also mines as soon as a transaction is in the pool:
> 15.3–17.4 s a block on average over these runs. Its blocks are the closest of the three chains to mainnet, but it
> is still one node with no network and no competing load.
>
> The timelocks were the drivers' short test values, not the companions' defaults (below). Read the tables as
> "which step waits for what, and for how many blocks". Do not read them as a user's wait.

DERO's "stable depth" is the node's stable height, the tip less 8 blocks. Every "until stable" step below is 8 DERO
blocks by construction, and the heights confirm it (a stipend mined at 585 is stable at 593).

---

## 1. Monero swap (`htlc.bas` v2, `tools/simhtlc -regtest`)

Real monerod and monero-wallet-rpc 0.18.5.1 in regtest, and the DERO simulator. Driver policy: t1 = lock + 48 DERO
blocks, t2 = t1 + 8, G = 4, 10 Monero confirmations, one regtest block mined per driver round. Bob is the maker (he
locks DERO), Alice the taker (she locks XMR). Each cell reads run 1 / run 2.

### Happy exit (Alice claims the DERO, Bob sweeps the XMR)

| Step | From → to (log marker) | Wall time, s | DERO blocks | Monero blocks |
|---|---|---|---|---|
| Setup and both proofs | `bob: -> setup` → `alice: … -> verified_pi_b` | 14 / 16 | – | – |
| DERO lock | `-> dero_locking` → `DERO locked` | 21 / 22 | mined at 575 / 766 | – |
| Bob waits for his lock to be stable, then pays the stipend | `-> stipend_sending` → `sent the taker a stipend` | 156 / 156 | 10 (lock 575 → stipend 585; 766 → 776) | – |
| Alice waits for the stipend to be stable | → `stipend … is at stable depth` | 129 / 126 | 8 (585 → 593; 776 → 784) | – |
| XMR lock | `-> xmr_locking` → `XMR locked` | 5 / 9 | – | in block 1085 / 2825 |
| 10 confirmations, seen by Bob's view wallet | `XMR locked` → `≥ 10 confirmations` | 38 / 50 | – | 10 (1085 → 1095; 2825 → 2835) |
| Gate | → `gate_received` | 2 / 3 | – | – |
| Claim sent | → `claimed: tx` | 7 / 25 | 4 after the XMR lock (593 → 597; 784 → 788) | – |
| Bob reads `s_a` from the chain | → `alice claimed` | 1 / 1 | – | – |
| Claim stable, Bob sweeps | → `the claim is stable`, then `swept` | 104 + 14 / 140 + 15 | 8 | sweep in 1198 / 2973 |
| **Whole exit** | setup → sweep sent | **502 / 574** | 30 (575 → 605) | – |

### Refund exit (Bob goes offline after the stipend and comes back at t1; Alice recovers her XMR with `s_b`)

| Step | Wall time, s | DERO blocks |
|---|---|---|
| Setup and proofs | 16 / 16 | – |
| DERO lock | 24 / 23 | mined at 609 / 800 |
| Lock stable, stipend paid | 135 / 153 | 10 (609 → 619; 800 → 810) |
| Stipend stable | 111 / 125 | 8 (619 → 627; 810 → 818) |
| XMR lock | 4 / 3 | Monero block 1421 / 3224 |
| XMR locked → t1 − G passes with no gate | 388 / 452 | 25 (627 → 652; 818 → 843) |
| → Bob back (the driver brings him back at t1 + 1) | 78 / 91 | t1 = 656 / 847 |
| Refund sent (reveals `s_b`) | 25 / 20 | mined at 658 / 849 |
| Alice sees the refund, recovery sweep sent | 0 + 12 / 0 + 12 | Monero block 1810 / 3675 |
| **Whole exit** | **805 / 909** | 49 (609 → 658; 800 → 849) |

### Punish exit (Bob vanishes after the stipend; Alice takes the DERO after t2)

| Step | Wall time, s | DERO blocks |
|---|---|---|
| Setup and proofs | 16 / 16 | – |
| DERO lock | 21 / 24 | mined at 662 / 854 |
| Lock stable, stipend paid | 160 / 156 | 10 (662 → 672; 854 → 864) |
| Stipend stable | 126 / 123 | 8 (672 → 680; 864 → 872) |
| XMR lock | 2 / 5 | Monero block 2069 / 3925 |
| XMR locked → t1 − G | 436 / 449 | 25 (680 → 705; 872 → 897) |
| t1 − G → t2 | 175 / 219 | 12 (705 → 717; 897 → 909) |
| Punish sent | 21 / 21 | mined at 718 / 910 |
| Bob's companion sees it | 2 / 2 | – |
| **Whole exit** | **966 / 1026** | 56 (662 → 718; 854 → 910) |

**What dominates.** On the happy path about 390 of the 502–574 s are DERO stable-depth waits: the lock before the
stipend, the stipend before any XMR, and the claim before the sweep. That is three times 8 DERO blocks plus the
blocks in between, and at mainnet's 18 s target it would be much the same. The Monero legs took under a minute here.
On mainnet, the companion's default 20 confirmations at a 120 s target would add about 40 minutes. Refund and punish
last as long as the timelocks: with the companion's defaults (t1 ≥ 400 DERO blocks, G = 100) the refund cannot start
before about 2 hours at 18 s a block.

## 2. Zcash swap (`swap.bas` v2 and `board.bas` v3, `ZECSWAP_REGTEST=1 go test -run TestRegtestEndToEnd ./zecswap`)

zebrad 6.3.0 regtest and the DERO simulator. The maker locks DERO in escrow; the taker funds a Zcash script. Swap A:
a v5 claim, with the maker's companion sending the away taker's DERO Claim. Swap B: a v6 claim, whose secret the
taker's own watcher read. Swap C: the refund. Swap C is the rerun at 07:28 UTC; the first C was stopped by hand, as
the log says.

| Step | A (s) | B (s) | C (s) | DERO blocks | Zcash blocks |
|---|---|---|---|---|---|
| Maker posts the offer | 24 | 25 | 21 | – | – |
| Taker accepts | 23 | 22 | 22 | accepted at 604 / 629 / 740 | – |
| Maker locks DERO | 20 | 19 | 21 | escrow read at 606 / 631 / 742; T_dero = +110 | – |
| Taker waits for the escrow to be stable, then funds | 133 | 147 | 145 | 8 | funding mined at 464 / 511 / 798 |
| Maker sees the funding 2 deep | 2 | 1 | – | – | 2 |
| Maker claims ZEC (the secret goes public) | 0 | 1 | – | – | claim mined at 467 / 514 |
| Taker has the secret | 40 (the maker sent her Claim for her) | 17 (her own watcher) | – | – | – |
| Taker's DERO Claim sent | with the above | 23 | – | – | – |
| Taker's Claim stable (taker done) | 123 | 107 | – | 8 | – |
| **Whole swap, post → last side done** | **369** | **366** | – | – | – |

Swap C, the refund:

| Step | Time | Height |
|---|---|---|
| Taker funds the script | 07:32:57 | Zcash funding mined at 798, cltv 836 |
| A third party with only the pre-signed file broadcasts the refund: refused by zebrad | 07:32:59 | tip 800 < cltv |
| The same file accepted | 07:36:20 (201 s later) | tip 836 = cltv; 36 regtest blocks, about 5.6 s each |
| Refund mined; the taker's companion records it | 07:36:38–39 | 837 |
| Maker waits for T_dero, then refunds its DERO | 07:36:37 → 08:02:23 → 08:02:35 | DERO 742 → T_dero 852: 110 blocks in about 1911 s, about 17.4 s a block |
| **Whole swap** | **1990 s** (33 min 10 s) | – |

**What dominates.** On a successful Zcash swap about 250 of the 366–369 s are two DERO stable-depth waits: the
escrow before the taker funds, and her Claim before she is done. Posting, accepting and locking are about 20 s each:
each is one DERO transaction to inclusion. The Zcash legs took seconds because zebrad's blocks came on demand. On
mainnet the maker's 2 confirmations alone are about 2.5 minutes at 75 s a block. The refund runs as long as its
timelocks: here 110 DERO blocks and cltv − funding = 38 Zcash blocks. At mainnet targets those are about 33 minutes
and about 48 minutes, if the same windows were used.

## 3. How these numbers were taken

- `research/regtest/xmrswap-e2e.log`: each companion line carries an `HH:MM:SS` stamp (AEST). The per-step gaps are
  the differences between the first line matching each marker in the table, within each exit and each run. Heights
  come from each run's "Txids" section, the `checked at height` / `stable height` lines and the `from height` in
  Alice's lock line.
- `research/regtest/zecswap-e2e.log`: each line carries an ISO UTC time; heights come from the `status` lines and the
  txid lines.
- Wall times include the companions' own poll intervals, of a few seconds. A step shown as 0–2 s happened within one
  poll.
