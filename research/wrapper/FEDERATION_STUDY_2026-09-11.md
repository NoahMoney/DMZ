# The federation, as the atomic-swap study saw it

*Copied on 2026-09-15 from `docs/ATOMIC_SWAP_STUDY.md` (2026-09-11). That day the operator ruled that the ZEC and XMR
atomic swaps are removed and the federation wrap became the product (`docs/MAINNET_TODO.md` Phase Y). The study is
retired with the swaps. What follows are its parts about the federation, copied word for word from its text at
`71c86c1`:*
- *§2.3 (Path 3);*
- *§3 (what the federation is still for), with prerequisites (a)–(e) and LIV-03 under v3 and v4;*
- *decisions 2 and 12 of its §5.*

*Read them with three things in mind:*
- *Their file and line citations are to the code the study read (`f680069`, by its own header). `vault.bas`,
  `tools/vaultsigner/main.go` and `docs/audit/FINDINGS_2026-09-10.md` have changed since, and vault v4 is under review
  (Y3-3).*
- *"Path 1", "the asset leg", "Path 0" and "the swap" name the Monero swap, which is removed. The advice that leans on
  it has nothing left to lean on: entering and leaving through swaps, and the liveness hedge.*
- *The recommendation "Do not deploy the federation" is overruled: mainnet waits until wXMR and wZEC work end to end.
  The prerequisites and the LIV-03 analysis are why this copy exists. They are open blockers in
  `docs/MAINNET_TODO.md` (YB-3 to YB-7).*

---

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

## From §5: the operator decisions 2 and 12

2. **The float between trades.** The options:
   - (a) DERO or tokens. Trustless and fast, but the holder carries DERO or token price, which contradicts "DERO only
     as gas".
   - (b) wXMR or wZEC. Keeps XMR exposure, but it is custody, and it needs decision 1(b).
   - (c) a swap for every trade. No exposure and no custody, but ≈48 min (XMR) or ≈21 min (ZEC) per trade.

   Speed does not decide this; trust does. The recommendation: offer (a) and (c) and say plainly what each carries.

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
