# DMZ: incident response

*2026-09-11. Incident response for a system that was built so that nobody can pause it, patch it, or reach into it.
Read this page first for what cannot be done, because every plan below starts from it.*

---

## 0. What nobody can do, by design

Once DMZ is deployed and the operator has revoked their own access (`MAINNET_DEPLOYMENT.md`), **nobody** can do any
of the following, the operator and the authors of this repository included:

- **Pause** a contract. No mainnet contract has a pause.
- **Freeze** an address, **blacklist** it, or **confiscate** from it.
- **Reverse** a transaction, or **recover** coins sent to the wrong place.
- **Upgrade** a contract. None calls `UPDATE_SC_CODE`; a fix is a new contract at a new id.
- **Change** a treasury, a fee or a registration fee: `factory.bas` v2 fixes them at install and has no owner.
- **Move** anyone's coins out of an escrow, a pool or an NFA except through that contract's own exits.

That is the security property, and in an incident it is also the limitation. An incident here is handled by telling
people, by the people themselves taking the exits the contracts give them, and by publishing new contracts or new
software that they choose to move to.

---

## 1. Who can act, after revocation

| Who | What they can still do |
|---|---|
| **Every user** | stop using a contract; take its exits (every escrow has one that needs no particular person: `SECURITY_THREAT_MODEL.md` §5); move to a new contract; switch the node their companion reads |
| **The companion's publisher** (operator decision 14) | ship a release that refuses a contract's code hash, a proof version, or a daemon version. Users choose whether to run it |
| **The app's publisher** | only if the TELA INDEX was left updatable, which the deployment plan rules out (`MAINNET_DEPLOYMENT.md`). Otherwise, publish a **new** INDEX under a new name, which users choose to open |
| **Anyone** | publish an advisory; deploy fixed contracts from this repository; run `tools/verifydeploy` against anything |
| **The operator** | nothing that a stranger cannot also do |

**Where reports go.** The repository's issue tracker on GitHub (NoahMoney/DMZ) is the only channel today. After the
operator revokes their access, nobody is obliged to read it. Who receives security reports after revocation is an
operator decision, and until it is made this page says so plainly (`MAINNET_TODO.md` MT-J10 covers the publisher).

---

## 2. Scenarios

### I-1. A contract defect that allows theft or a freeze

1. **Confirm it** on the simulator with `tests/` and a live driver. Record the reproduction.
2. **Tell users** through every channel that exists: the repository's README and a pinned advisory. State:
   - which contract ids are affected;
   - what an attacker can do;
   - which exit each user should take, and before which height.
3. **Users exit** what they hold, through the contract's own exits. Nobody can do it for them.
4. **Companion release.** A release refuses the affected code hash for new swaps, while still completing in-flight
   swaps through their exits. The companions compare installed code with a canonical list (U4).
5. **Fix, test and deploy** a new contract at a new id, and verify it (`tools/verifydeploy`). Publish its hash.
6. **The app.** A new build points at the new contract. If the INDEX is immutable, it is a new INDEX, and users are
   told its id.
7. **Accept the race.** Nothing stops an attacker who acts first. That is why an external review of every contract is
   a mainnet gate (MT-A17).

### I-2. The Monero proof is found unsound

- **Who is exposed.** Every in-flight XMR swap where a counterparty could have lied.
- **The companion release** refuses to start new swaps under the affected proof version.
- **In-flight swaps** end by their exits. Bob Refunds in `[t1, t2)`, which reveals `s_b` so Alice can recover her XMR.
  If the proof was unsound, Alice's recovery might fail. That exposure is exactly why the audit is a gate (MT-G03).

### I-3. A companion defect

- A release fixes it. Its state files must stay readable by the fixed build, which the crash-fuzz discipline makes
  possible: durable state, and `resume`.
- If a release would have to change a state file's format, it must read the old one. That is a release requirement.

### I-4. A key is compromised

| Key | What the attacker gets | Response |
|---|---|---|
| a user's DERO, Monero or Zcash wallet | that user's coins | the user moves what is left; nothing in DMZ can help |
| a swap's key share or secret (`xmrswap` or `zecswap` state) | that swap's counterparty's leg, in some states | finish or refund the swap at once; the state file is a secret, and the recovery card says so |
| the **factory owner key** | none exists: `factory.bas` v2 has no owner. (An older v1 factory's owner could move the treasury pointer and the fee until Renounce.) | — |
| the **treasury key** | the protocol fees paid to the treasury from then on. The treasury address is fixed at the factory's install, so fees keep flowing to the compromised key | announce it; no contract change is possible. Choose and protect this key accordingly (`SECURITY_KEY_MANAGEMENT.md`) |
| the **TELA INDEX key**, if the INDEX was left updatable | the ability to republish a lying app | users run `tools/verifydeploy` against the INDEX, and move to a verified one. Avoided by the INDEX's final state |
| the **deployer key, after installation** | nothing, beyond any passes or DERO still in that wallet | none needed; it holds no role once the contracts are installed |

### I-5. A foreign-chain event

- **A deep Monero or Zcash reorganisation.** There is nothing to do on DERO. The receiving party bears the loss named
  in `CROSS_CHAIN_TRUST_MODEL.md` T3. Advise makers to raise their confirmation tier until the chain settles.
- **A Zcash network upgrade** (NU7, or any later one):
  - `zecswap` refuses to sign under a branch id it does not know (`CheckBranchID`), so a stale build fails safely;
  - a pre-signed refund expires at the next activation, so takers must re-sign before it (`docs/ZEC_SWAP.md` Z-B);
  - a new release carries the new branch id.
- **The Monero FCMP++/Carrot fork.** Finish in-flight swaps before its activation height. Use `xmrswap` after it only
  once a release has been tested against the forked daemon (MT-B17).
- **A foreign chain halts.** See `SECURITY_THREAT_MODEL.md` §5. The Zcash taker must broadcast her refund the moment
  Zcash resumes.

### I-6. A DERO incident

- **A halt.** Every swap counts in DERO heights, so nothing expires while DERO is stopped. When it resumes, the exits
  resume. The foreign chain's heights keep running, which is covered in `CROSS_CHAIN_TRUST_MODEL.md` §4.
- **A consensus fork.** Users follow the branch their own node follows. This is outside DMZ, and no DMZ code can choose
  for them.

### I-7. A lying or compromised data source

- Users who read through a remote node should re-check every in-flight swap against their own node, or against
  several independent ones, before any irreversible step.
- The companions' `status` command names every endpoint as trusted (MT-B09, MT-C10).

### I-8. Hostile content in contract storage

- NFA names, descriptions, chat messages and market names are written by strangers.
- The app sanitises them and flags impersonation (`tests/js/sanitize.test.mjs`).
- A new pattern that gets through needs a new app build. That is the INDEX question again (I-1, step 6).

---

## 3. The user's checklist in an incident

1. Read the advisory, and check the contract ids it names against your own swaps: the companions' `status` prints them.
2. For each in-flight swap, run `status` and take the exit it names **before the height it names**.
3. Do not start new swaps on an affected contract. Update your companion only from a release whose hash matches the
   one printed in the repository (`tools/release -verify`).
4. If you use a remote node, re-check against your own.

## 4. The publisher's checklist

1. Reproduce the defect, and record it in `docs/audit/`.
2. Publish the advisory with exact contract ids, heights and exits.
3. Ship a companion release that refuses what is affected and still completes what is in flight. Publish its manifest.
4. Deploy and verify fixed contracts. Publish their hashes.
5. Record everything in `docs/history/HISTORY.md`.
