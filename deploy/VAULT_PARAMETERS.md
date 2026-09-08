# Vault parameters, per foreign chain

Every parameter below is in the **foreign chain's base units** — piconero for Monero, zatoshi for Zcash — and
four of them are immutable the moment `Seal()` runs. The simulator's defaults are shaped for a five-decimal
asset. Copying them to a real Monero vault is wrong by 10⁷: the documented `capratio` of 3 would cap a Monero
vault at 0.00000018 XMR, and an operator following the old runbook would deploy a vault that cannot hold
anything. This table exists so that cannot happen again.

## The rule that ties them together

```
cap / capratio  <=  bondtot          checked at Mint, SetCap and Slash (vault.bas lines 243, 318, 497, 533)
```

`bondtot` is DERO atomic units the signers have bonded. `capratio` is how many foreign base units each bonded
atomic unit is allowed to back. So `capratio` is a **decimals-dependent** number: it converts between DERO's
five decimals and the foreign chain's, times whatever collateral ratio the operator wants. Every guard is
written as a division, never `bondtot * capratio`, so none can overflow a uint64.

## The table

| Parameter | Set at | Mutable after Seal? | DERO-sim (5 dec) | **Monero (12 dec)** | **Zcash (8 dec)** | Meaning |
|---|---|---|---|---|---|---|
| `cap` | install / `SetCap` | yes, k votes, bounded by the rule | 100 000 | 100 000 000 000 000 (100 XMR) | 100 000 000 000 (1 000 ZEC) | most units that may be minted |
| `capratio` | install | **no** | 3 | 2 000 000 000 | 2 000 000 | foreign base units per bonded DERO atomic unit |
| `minbond` | install | **no** | 20 000 | 20 000 | 20 000 | DERO atomic units each signer must bond (0.2 DERO) |
| `minburn` | install / `Tighten` | tighten only | 1 000 | 10 000 000 000 (0.01 XMR) | 1 000 000 (0.01 ZEC) | smallest withdrawal |
| `wfee` | install | **no** | 100 | 100 000 000 (0.0001 XMR) | 10 000 (0.0001 ZEC) | fee kept by the federation, per withdrawal; must be `< minburn` |
| `mindep` | install / `Tighten` | tighten only | 1 | 1 000 000 000 (0.001 XMR) | 100 000 (0.001 ZEC) | smallest deposit that will ever be minted — **below it is an unrecoverable loss** |
| `regfee` | install | **no** | 0 | 0 | 0 | DERO paid to the treasury per `Register` |
| `wdelay` | `SetLimits` before Seal | no | 0 | see below | see below | blocks a burn waits before it is releasable |
| `bigbps` / `bigdelay` | install | no | 2000 / 500 | 2000 / 500 | 2000 / 500 | a withdrawal above `bigbps` of supply waits `bigdelay` extra blocks |
| `winmax` | `SetLimits` before Seal | no | 0 | see below | see below | most minted over two consecutive 2000-block windows (0 = unlimited) |

With three signers at `minbond`, `bondtot = 60 000`. The Monero column gives `cap / capratio = 50 000 ≤ 60 000`;
so does the Zcash column. Raise `capratio` in proportion if the cap must grow without more bond.

## Constants that are not parameters

These are hard-coded in `vault.bas` and are in **DERO blocks** (18 s), so they are the same on every chain.

| Constant | Value | Where | Meaning |
|---|---|---|---|
| Paying mark window | 2 000 blocks (~10 h) | `Veto` line 25 | a `Veto` is refused this long after a signer marks a withdrawal as being paid |
| halt lease | 20 000 blocks (~4 days) | `Halt` line 41 | a `Halt(1)` quorum lasts this long unless renewed; then the vault is open again |
| reclaim window | 20 000 blocks after `w_<j>_rel` | `Reclaim` line 14 | an **unmarked** burn may be taken back by the burner after this |
| vote bucket | 2 000 blocks | every `votek` scope | operational votes are scoped by height bucket and epoch |

## Choosing `wdelay` and `winmax` for a real chain

Neither has a right answer the code can supply. `wdelay` is the honest signers' window to notice a bad burn and
halt before any payment is made; it should cover the slowest foreign-chain confirmation the federation waits for
plus a signer's polling interval, with margin. `winmax` bounds how fast the supply can grow if a deposit source is
compromised; set it from the cap and the expected honest inflow. Record the chosen values and the reasoning in
`deploy/federation.md` before Seal — Seal is the point of no return.

## What the simulator runs today

| Vault | SCID | Decimals | `cap` | `capratio` | `minburn` | `mindep` | `wfee` | Install gas |
|---|---|---|---|---|---|---|---|---|
| Monero (simulated) | `f23403b0…686fb` | 12 | 10¹⁴ | 2·10⁹ | 10¹⁰ | 10⁹ | 10⁸ | 16 407 |
| Zcash (simulated) | `93013bb2…3ccf0` | 8 | 10¹¹ | 2·10⁶ | 10⁶ | 10⁵ | 10⁴ | 16 392 |

Both installed 2026-09-08 with the halt-lease / `Reclaim` state machine. Earlier simulator vaults
(`d69c933f…`, `bfd444b7…`, `c32e6572…`, `ab32063f…`) were five-decimal-shaped and are superseded.
