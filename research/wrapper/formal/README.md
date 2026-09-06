# Formal model of the wrapper (TLA+)

`VeilWrap.tla` models the deposit and withdrawal state machines of the federation wrapper **as specified in
PIPELINE_SPEC / MECHANISM_REVIEW A4**, not the v1 contract as deployed. The differences are listed below so the
model cannot be mistaken for a proof about today's code.

## What is modelled

- A foreign chain per deposit: inclusion, confirmations up to `MaxDepth`, a shallow reorg (below `ConfDepth`)
  that removes the deposit, and a **deep reorg** beyond the policy depth as an environment fault.
- `N` signers with quorum `K`; at most one Byzantine signer whose watcher reports a wrong amount at any time.
- The on-chain vote contract: honest votes only at policy depth; votes are a set (delivering a vote twice is a
  no-op, which is how vault.bas behaves); mint when `K` signers voted the same amount; never twice per deposit.
- DERO burns with a finality depth `DeroDepth` and a possible DERO reorg before it (supply restored).
- A threshold release round: shares from honest signers, completion at `K` shares recorded in a ledger
  **before** the release is counted, a crash that abandons the round, and a retry that can only start from
  "abandoned", never from "released".
- Message loss (an action simply not taken) and duplication (set semantics).
- A halt triggered by the deep-reorg fault.

## Invariants checked (all hold)

| invariant | meaning |
|---|---|
| `NoDoubleCredit` | outstanding supply plus released value never exceeds what minted deposits justify |
| `NoCreditWithoutFinalDeposit` | every minted deposit reached `ConfDepth` and is still on chain unless a deep reorg (fault) occurred |
| `ReorgedCreditImpliesHalt` | a credited deposit that was reorged out exists only after the fault flag that forces a halt |
| `ByzantineCannotMint` | the Byzantine watcher's amount never reaches quorum |
| `NoDoubleRelease` | a burn is released at most once and only through the ledger's "released" state |
| `NoReleaseWithoutFinalBurn` | releases happen only for burns at DERO finality depth |
| `Conservation` | `sup = Amount × |minted| − Amount × |pending or final burns|` |
| `TypeOK` | typing |

## Result

```
tlc2.TLC -workers 8 -deadlock -config VeilWrap.cfg VeilWrap.tla   (OpenJDK 21, WSL)
Model checking completed. No error has been found.
2745505 states generated, 443152 distinct states found, 0 states left on queue.
The depth of the complete state graph search is 30.
```
Constants: 2 deposits, 2 burns, 3 signers, K = 2, Byzantine = s3, ConfDepth = 2, MaxDepth = 3, DeroDepth = 2.
The first run reported "Deadlock reached" on the terminal state (everything reorged and halted), which is the
intended end of that behaviour; `-deadlock` disables that check. Full output: `tlc-output.txt`. Run: `run-tlc.sh`.

## What the model does not cover (and what code must add)

- v1 `vault.bas` keys mints by `(id)` only; the model assumes `(chain, txid, output_index, amount, index)` (S1).
- v1 has no release ledger, no encrypted destinations, no DERO-finality gate, no rate limits (S2, S3, C1, S5).
- One Byzantine signer with a wrong amount; not a Byzantine signer that votes a wrong recipient (the contract's
  first-vote-fixes-the-tuple rule refuses it; the model counts support per amount only).
- Fees and rounding on release (C5), and the reserve invariant against real vault balances (C4).
- Liveness: the model checks safety only. A halted vault is safe and stuck by design.

## Model-to-code conformance (Phase D item 7)

To be done: record simulator runs of `vaultsigner` and map each transaction to a model action; check that the
recorded trace is a behaviour of `Spec` (TLC `-simulate` with a trace module, or a small Go checker).
