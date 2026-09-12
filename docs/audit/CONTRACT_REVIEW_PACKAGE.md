# DMZ contracts: the external review package

X-12 of `docs/MAINNET_TODO.md` (Phase X), preparing the external review of `contracts/*.bas`, MT-A17 (§A of the same
file). Written 2026-09-12 at commit **`e06c28a`** (`e06c28a416ddc1d74b58e50ca1998d7caf2051b5`, branch `main`). No
contract, reference model or test under `contracts/`, `reference/` or `tests/` differs from `e06c28a` in the working
tree this was written in, so every citation of those files is at that commit. Nothing has been sent to anyone: the
operator decides who receives this page.

The companion package for the Monero proof is `docs/audit/PROOF_XMR_AUDIT_PACKAGE.md` (MT-G03). §7 says what belongs
to which.

**Conventions.**
- `file:N` is a line of that file at `e06c28a`. In the tables below, `htlc:25` means `contracts/htlc.bas:25`.
- Several documents cited here (`docs/MAINNET_TODO.md`, `docs/HTLC_V3_COMPANION_NOTES.md`, `docs/XMR_SWAP.md`) are
  being edited by other people while this is written. They are cited by item ID or section, not by line.
- `<repo>` is the checkout and `<tmp>` a scratch directory.
- Atomic units: 1 DERO = 100,000 (`DECISIONS.md` §2, item 9).

---

## 0. Start here

**What DMZ is.** An exchange on DERO for Monero and Zcash, plus the markets around it. The DERO side of each swap is
an escrow contract that pays out against a SHA-256 preimage or a block height. The foreign side is a lock that only
the swap's own reveals can complete, and each party's own companion program watches the foreign chain
(`docs/DECENTRALIZATION_ARCHITECTURE.md` §0–§1). No contract reads another chain, and none can.

**What to review.** Twelve DVM-BASIC files. Eleven are used on mainnet and `vault.bas` is not (§1). By how much value
they hold and how new they are, the order that gives most for the time is:
1. `htlc.bas` v3: the Monero swap's DERO leg. Its current form was built on 2026-09-12, the day this page was written,
   and it has four exits, each in its own height window.
2. `swap.bas` v2 and `board.bas` v3: the Zcash swap's DERO leg and its order board.
3. `launch.bas` and `pool.bas`: the AMMs, with 128-bit arithmetic built from 64-bit limbs, and rounding that must
   favour the contract.
4. `nfa.bas`: an adapted third-party standard (ART-NFA-MS1) with eight documented changes.
5. `founders.bas`: a reward accumulator with eras. Two findings in it are deliberately open (FEE-01, FEE-02).
6. `factory.bas`, `nft.bas`, `social.bas`, `chat.bas`: small.

**How to begin.** Bootstrap the vendored DERO node and run the suite as §5 says. It took 82.3 s in the recorded run
and 440 s on a loaded machine.
Then read §2, which covers the platform rules every contract leans on, before any single contract.

---

## 1. Scope

### 1.1 The contracts and their canonical hashes at `e06c28a`

A contract is installed as `reference.Canon(source)`: the source with every whole-line comment and every blank line
removed, and nothing else changed (`reference/canon.go:22-33`; `tests/canon_test.go`
`TestCanonOnlyDropsCommentsAndBlanks`). `tools/simdeploy` sends exactly those bytes as the code (`read()`,
`tools/simdeploy/main.go:153`), the chain stores them, and `DERO.GetSC` returns them. The **canonical hash** is
sha256 of those bytes. It is what `tools/verifydeploy` compares with the chain, what `tools/release` writes into its
manifest (both through `tools/verifydeploy/srchash/srchash.go:29-30`), and what the companions pin.

| Contract | Purpose | `v` stored | Canonical sha256 (installed code) | Installed bytes | File bytes | Mainnet |
|---|---|---|---|---|---|---|
| `htlc.bas` | the Monero swap's DERO leg: a lock under three hashes, with four exits in disjoint height windows | 3 (`htlc:39`) | `b785028da4a2554521d47f989f1011ec1f0a26be491e6a7eb2786c1f50834c36` | 5,104 | 11,206 | yes, one instance |
| `swap.bas` | the Zcash swap's DERO leg: a hash-time-locked escrow for a named taker | 2 (`swap:20`) | `b9830de831dbaf34f61ea15b484c8f25105e41dc790aa06412db421a990fd6e3` | 2,767 | 5,967 | yes, one instance |
| `board.bas` | the Zcash swap's public order board. It holds no value | 3 (`board:25`) | `162f8d46ad400b1fef981d029ba99f61c9646d6453c3bf9f0cc4bd9e63bc5ab1` | 2,611 | 6,602 | yes, one instance |
| `factory.bas` | a registry of markets, and the treasury pointer. It has no owner | 2 (`factory:31`) | `73f281df0258863d46930452624d95fdc566b8cb8c6c11b618ece324c9174ac1` | 1,443 | 3,452 | yes, one instance |
| `pool.bas` | an LP-owned constant-product AMM, one asset against one quote | 2 (`pool:81`) | `304ca766363a493edea83f19102091291275eb0ba2573f27cc0680c5a5041580` | 10,604 | 14,250 | template: users install it from `app/templates/` |
| `launch.bas` | a token with a bonding curve that graduates into a built-in AMM, plus fee, tax and staking | 2 (`launch:56`) | `ac9dbb1e9cd613cc8022cc958f4d7e17285fc853a6c2598664a5efd53c0771f4` | 15,257 | 17,074 | template |
| `nft.bas` | a collection whose editions are earned by holding a token | 1 (`nft:38`) | `345e8227c4f51e0d72f045ec8567bdd5514e4c955661aa96b1abd27aa36933f2` | 3,873 | 4,959 | template |
| `nfa.bas` | one ART-NFA-MS1 v1.1.1 asset per contract, priced in a quote, with a protocol fee | `version` "1.1.1" (`nfa:109`) | `f900c7533b782aee5ce33ddf410df5685058a8c792cd264647740bef1119e114` | 14,937 | 24,234 | template |
| `founders.bas` | the Founders Pass: a fixed-edition NFT whose registered holders share DERO deposited for them | 1 (`founders:36`) | `49009d2c7afa3f85d2d0c949383557562333c7163d05a042f59aded06b588b4a` | 10,398 | 12,637 | yes, one instance |
| `social.bas` | profiles and a follow graph. It holds no value | 1 (`social:20`) | `da7ef3f09fccf468c9c03605e0de14a4b129b8cfbb29ced6efad8bf9b5b617ad` | 1,736 | 2,355 | yes, one instance |
| `chat.bas` | append-only chat rooms, with a fee per message that is forwarded at once | 1 (`chat:31`) | `c1d7136bd137a114f4f529ab3b5864a72780e68cd1fd9fd8a61a23856a1ca9d4` | 1,146 | 2,401 | yes, one instance |
| `vault.bas` | a k-of-n bonded federation that wraps a foreign asset | 3 (`vault:64`) | `db1aba5e0664267bf89105d6d349ddb5eabb3dbdda17163e2cdb6ee6ebf6223d` | 16,027 | 22,196 | **no**: not deployed (`docs/MAINNET_DEPLOYMENT.md` §1; `deploy/networks.json` `mainnet.vault` is `""` and `vaults` is `null`) |

The file sha256s, comments included, are listed in §1.3 so that a reviewer can confirm the checkout.

**What ships, and how.** `docs/MAINNET_DEPLOYMENT.md` §1 is the list. The deployer installs `factory`, `social`,
`founders`, `chat`, `htlc`, `swap` and `board` once each (`simdeploy -network mainnet`, same file §5). `pool`,
`launch`, `nft` and `nfa` are templates. Users install them later from `app/templates/<kind>.bas`, which must be
byte-for-byte `Canon(contracts/<kind>.bas)` (`tests/templates_test.go` `TestShippedTemplatesAreTheTestedContracts`).
The app lists a market only if its code hash is in the network record's `templates` (`docs/MAINNET_DEPLOYMENT.md` §1).
The mainnet record's `templates` block is empty today, and no mainnet deployment exists (`deploy/networks.json`
`mainnet`).

**A hash does not name a deployment.** The same code installed with other install arguments hashes the same.
`htlc.bas`'s `w1min`, `w2min` and `g2`, `swap.bas`'s `wmin`, and the factory's `treasury` and `regfee` are read from
storage by whoever relies on them. Each companion reads the swap parameters and refuses a deployment outside its
policy (`tools/xmrswap/pin.go:10-13`; `tools/zecswap/pin.go` `CheckEscrowWidth`). `tools/verifydeploy` prints the
roles and parameters (`tools/verifydeploy/README.md`, "roles").

### 1.2 How the hashes were computed

With the repository's own code: `srchash.Contracts` (`tools/verifydeploy/srchash/srchash.go:101-116`), which
`verifydeploy` and `release` both use. It was run on a clean export of the commit:

```
git -C <repo> archive e06c28a contracts | tar -x -C <tmp>/src
# <tmp>/go.work:  use ( .  <repo>  <repo>/tools )        (modules veil and veil/tools)
# <tmp>/main.go:  for each c in srchash.Contracts(os.Args[1]) print c.File, c.CanonSha256, c.SourceSha256
cd <tmp> && go run . ./src/contracts
```

The same program over the working tree gave the same twelve lines. Independent cross-checks:
- `htlc.bas` equals `xmrswap.HTLCCodeSHA256` (`tools/xmrswap/pin.go:18`).
- `board.bas` and `swap.bas` equal `zecswap.BoardCodeSHA256` and `EscrowCodeSHA256` (`tools/zecswap/pin.go:21-22`).
- Each pin is recomputed from the tree by its own test (`tools/xmrswap/pin_test.go` `TestPinnedCodeIsTheTreesHtlc`;
  `tools/zecswap/pin_test.go`), and both passed on 2026-09-12.
- `factory.bas` equals the hash recorded in `docs/MAINNET_TODO.md` MT-J15.
- `pool`, `launch`, `nft`, `nfa` and `founders` equal `sha256sum app/templates/<kind>.bas` and the `simulator.templates`
  block of `deploy/networks.json`.

`go test ./verifydeploy/srchash` (under `tools/`) checks that `ContractSum` is sha256 of `Canon`, and it passed.

### 1.3 File sha256, comments included, at `e06c28a`

| File | sha256 |
|---|---|
| `board.bas` | `d42204cfd7605ac6a1ea4a4e62663ab8050e29d5f6b8e532d5e9f491e76996ea` |
| `chat.bas` | `6486c535ab5134a6f1820108539b85aabb949aefe860493c2482a19047a72dfb` |
| `factory.bas` | `ca2ee400153f06e4de2bdfe0ac0d9c9c9a3a86c2cb11ee91ab392237088962ab` |
| `founders.bas` | `fd0e1e28d229c9e58b8304b5eb315c6118d5273e94d278a58020fc8adb1c2aeb` |
| `htlc.bas` | `0a93449d50bec1a8df32ddb068cbc6e88c645155e6b4dccf16f42b0f9efb0723` |
| `launch.bas` | `4730cbbeff21f0243d12eb26670b854032eff1000425e75049583190f299f079` |
| `nfa.bas` | `76f1dcba08a81bb97d01f663759ed41e4a3544eb99dfc34e66ed91274bee3fe9` |
| `nft.bas` | `1fb090db256284ebc486698f6b2439ec0d992d8bb8e4d14a4e6b7444f6ff1975` |
| `pool.bas` | `a4fab52923cbcc75d79882536a4446454554f6fdf576993ff94bfab600bcd03b` |
| `social.bas` | `de4d23499a4e37f55e4c527b2c5e3bb767a9adde121b095b4e648e62b6232dfd` |
| `swap.bas` | `4e51e7db5bd77c7118d4ee52bef2e2225d9f64ce61897ac2567c4b715f69b150` |
| `vault.bas` | `4cad5dc98f808557bc417a9816e149d1d1aaceab8732450a9883750492c307c6` |

(`founders.bas`'s file hash `fd0e1e28…` also appears as a legacy template hash in the simulator record. That record
used to hash files with their comments; see MT-H07 in `docs/MAINNET_TODO.md`.)

---

## 2. The platform: DVM-BASIC semantics that matter for review

The vendored node is `deroproject/derohe` at `e9df1205b6603c62f0651d0e18e5e77a2584b15e` (`deploy/vendor.lock`,
`repos."vendor/derohe"`). `vendor/` is not in git, so §5 says how to fetch it. Paths below are under `vendor/derohe/`.
`DECISIONS.md` §1–§2 is the project's own reading of the same code, and `docs/MAINNET_GAP_ANALYSIS.md` §1.1 is a
second one.

1. **Two types, no floating point.** A value is `Uint64` or `String` (`dvm/dvm.go:42-43`); there is no signed type and
   no float. `+ - *` wrap modulo 2^64, so each contract guards additions by hand (`IF a + b < a`) and computes every
   product that could exceed 64 bits as a 128-bit value from 32-bit limbs (`muldiv`, `muldivup`, `divlu`:
   `pool.bas:314-408`, the same code in `launch.bas` and `founders.bas`). The discipline and its rounding rules are in
   `DECISIONS.md` §10.2. The models and tests match it in integers: `tests/run.sh:17-21` refuses `float32`/`float64`
   in `reference/`, `tests/harness` and the two money daemons.
2. **No cross-contract calls or reads.** No built-in calls or reads another contract. There are 37 built-ins in all
   (`dvm/dvm_functions.go:58-94`; `DECISIONS.md` §1.2), and a contract cannot invoke another (`DECISIONS.md` §2,
   item 8). So there is no re-entrancy, and nothing on chain can check that a registered market is genuine: factory
   registrations are paid hints (`factory.bas:2-3`; `RISKS.md` R-3).
3. **Storage gas: at most 20,000 per transaction.** The limit is `MAX_STORAGE_GAS_ATOMIC_UNITS = 20000`
   (`config/config.go:44`), clamped at `dvm/sc.go:245-246`. Compute gas is a fixed 10,000,000 per call
   (`dvm/sc.go:237`). An install is charged for its code bytes plus what `Initialize` stores (`DECISIONS.md` §1.3).
   That is why the chain receives `Canon(source)` and not the commented file. `tests/canon_test.go`
   `TestEveryContractFitsTheInstallGasCapAsInstalled` fails any contract whose installed form exceeds 20,000 bytes.
   Measured install storage gas: `contracts/README.md`, the table at the top. For `htlc.bas` v3 it is 5,168–5,180 by
   the install arguments (`docs/HTLC_V3_COMPANION_NOTES.md` §2). For `nfa.bas` it is 15,876 DERO-quoted and 15,941
   wrapped, the largest measured of any mainnet contract.
4. **`SIGNER()` is real only at ring size 2.** `Extract_signer` succeeds only when the DERO payload's ring size is 2
   (`blockchain/transaction_execute.go:424-442`, the test at `:430`). Otherwise the call runs with a signer of 33 zero
   bytes (`:295-299`), and `SIGNER()` returns that (`dvm/dvm_functions.go:307-310`).
   - `IS_ADDRESS_VALID` of the zero key is 0: x = 0 would need y² = 3, and 3 is not a square mod the BN254 prime
     (`docs/MAINNET_GAP_ANALYSIS.md` §1.1). So every `IS_ADDRESS_VALID(SIGNER()) == 0` guard, and every
     `s != LOAD(...)` against a stored real address, fails closed for an anonymous caller.
   - The `…To` swap entrypoints of `pool.bas` and `launch.bas` pay a named `dest` and never read the signer. They are
     the paths meant for ring 16 (`contracts/README.md`, "Named-recipient swaps").
5. **A refused call refunds only a known signer.** State and transfers commit only when the entrypoint returns
   `Uint64 0` (`dvm/sc.go:278-289`). Any other return, and any panic (a `LOAD` of a missing key, `PANIC()` in
   `muldiv`), discards them. The attached value is then refunded to the signer, and that is possible only when the
   signer is known, which means ring 2 (`blockchain/transaction_execute.go:395-405`; `DECISIONS.md` §2, item 3;
   `RISKS.md` R-2). Fees are always spent.
6. **Transfers are checked after the call, and only the last registry error survives.** `SEND_*` queues a transfer.
   After a committing call, the node checks that the contract holds each total (`dvm/simulator.go:196-224`) and that
   each destination is a registered account (`:227-236`). That second loop keeps only the last lookup's error. So a
   call that pays an unregistered address and then a registered one half-commits: this is DVM-1 / LIV-01 in
   `docs/audit/FINDINGS_2026-09-10.md`, and the reason for `nfa.bas`'s `PayOwed` (`nfa.bas:27-37`).
   `tests/sendorder_test.go` `TestNoUnverifiedPayeeBeforeTheLastTransfer` checks every contract for that shape.
7. **A contract's own asset has no chain-kept balance.** `StoreSCValue` drops the write of the contract's own balance,
   "an scid can mint its assets infinitely" (`dvm/sc.go:392-395`), and the transfer check skips its overflow and
   underflow tests (`dvm/simulator.go:203-216`). So the supply of pool LP, launch tokens, founders passes, nft
   editions and the nfa unit is bounded only by each contract's own arithmetic (GAP-022). The conservation tests
   follow every own-asset unit through a ledger (`tests/zzconservation_test.go:9-14`).
8. **Entrypoints are the functions whose first letter is an upper-case ASCII letter** (`dvm/dvm.go:343-370`).
   Lower-case helpers (`buyto`, `settle`, `owe`, `distribute`, …) cannot be called from a transaction
   (`docs/MAINNET_GAP_ANALYSIS.md` §1.1). `tests/parse_test.go` `TestContractsParse` pins each contract's exact
   entrypoint list. `Initialize` / `InitializePrivate` stays callable after install, so every contract refuses a second
   run through `initd` (NFA through `owner`). See `contracts/README.md`, rule 1, and `tests/reentry_test.go`
   `TestInitializeNotCallable`.
9. **A call with a missing parameter is refused** before the code runs (`dvm/sc.go:232-234`). `htlc.bas` and
   `swap.bas` rely on this so that an install cannot leave out a width (`htlc.bas:28-33`, `swap.bas:12-15`).
10. **The gas estimator runs the contract at height 0.** `DERO.GetGasEstimate` builds a fresh `dvm.Simulator`
    (`cmd/derod/rpc/rpc_dero_estimategas.go:73-81`), whose `height` is never assigned (`dvm/simulator.go:44`, read at
    `:117` and `:155`). So in an estimate `BLOCK_HEIGHT()` is 0:
    - a Lock that the chain will refuse for being too narrow passes its estimate;
    - every exit gated from a height (Refund, Punish, Reclaim) fails its estimate and must be sent at a fixed fee.
    Both were seen live on the simulator (`docs/HTLC_V3_COMPANION_NOTES.md` §3, §4 and §8).
11. **A transaction is mined at most 10 blocks after it is built.** A block includes a transaction only while
    `block height − tx.Height < TX_VALIDITY_HEIGHT = 11` (`blockchain/miner_block.go:43,275`;
    `blockchain/blockchain.go:748`). `tx.Height` is the builder's tip. A contract therefore sees a `BLOCK_HEIGHT()` up
    to 10 above the height its caller planned for. Every height window must be read with that in mind (§6, Q4).
12. **Stable depth is 8.** `STABLE_LIMIT = 8` (`config/config.go:77`). The companions act only on state 8 deep
    (`docs/SECURITY_THREAT_MODEL.md` §5, "DERO reorganisation"). An exit mined within 8 blocks of a window's edge can
    be undone and re-mined on the other side of it.
13. **`UPDATE_SC_CODE` is not used.** No contract contains it (a search of `contracts/*.bas` finds 0 occurrences), and
    none defines `UpdateCode` (`contracts/README.md:3`; `DECISIONS.md` §10.1). What it would do is overwrite key `"C"`
    (`dvm/dvm_functions.go:312-322`). `TestContractsParse` also fails any contract that writes `"C"`. A fix is a new
    contract at a new SCID.
14. **A contract sees only the assets it names.** `ASSETVALUE(x)` reads the value attached for one asset `x`, and no
    built-in lists what a call carries. So a contract can refuse DERO, its own asset and the assets it stores. Any
    other asset attached to a call is accepted and kept (GAP-027; `tests/zzstray_test.go:1-8` pins this limit).
15. **A key `DELETE`d earlier in a call makes a later `EXISTS` or `LOAD` of it in the same call panic**
    (`dvm/dvm_store.go:81-98`, as `nfa.bas:43-44` records). `nfa.bas`'s `resetVars` is ordered around this.
16. **`BLOCK_TIMESTAMP()`** is the miner's block timestamp. Only `nfa.bas` uses it for its rules (listing duration,
    anti-snipe extension, cancel buffer). The others use it only in informational records.

---

## 3. Per contract

Common to all:
- A refused call returns `1` via `GOTO 900` (or `999`), which discards it (§2, item 5).
- "ring 2" means the entrypoint requires `IS_ADDRESS_VALID(SIGNER())`, directly or by comparing the signer with a
  stored address.
- "Anyone" means any caller at any ring size unless stated otherwise.
- The privileged-path table for the whole set is `docs/MAINNET_GAP_ANALYSIS.md` §1.2. `tools/verifydeploy/roles.go`
  is the machine-checked version, held to `contracts/` by three tests (`tools/verifydeploy/README.md`, "roles").

### 3.1 `htlc.bas` v3: the Monero swap's DERO leg

Protocol: `docs/XMR_SWAP.md` §1–§2 and `docs/XMR_SWAP_BUILD.md`. What changed from v2, and why: `docs/HTLC_V3_COMPANION_NOTES.md`
§1–§4. The maker (Bob, who has DERO) locks DERO or one asset for a named taker (Alice, who has XMR) under three
sha256 hashes: `ha` of her Monero key share `s_a`, `hk` of his gate secret `k_b`, and `hb` of his key share `s_b`.

| Entrypoint | Who | When / state | Checks | Effect |
|---|---|---|---|---|
| `Initialize(w1min, w2min, g2)` | anyone, once | not `initd` (`htlc:35`) | each ≥ 1 (`:36`), ≤ 1,000,000, and `w1min + w2min` ≤ 999,999 (`:37`) | stores `kind v n w1min w2min g2 initd` (`:38-44`) |
| `Lock(ha, hk, hb, taker, t1, t2, asset)` | ring 2 (`:60-61`) | — | hashes are 64 lower-case hex (`:62-63`); `h+1 < t1 < t2 ≤ h+1,000,000` (`:65`); `t1 ≥ h + w1min`, `t2 ≥ t1 + w2min` (`:66`); `taker` is required and valid (`:67-69`); value > 0: DERO, or exactly one named asset with no DERO (`:70-79`) | stores `m_ t_ ha_ hk_ hb_ a_ v_ t1_ t2_ st_=0`, `n += 1` (`:80-91`) |
| `Claim(id, sa, kb)` | the taker `t_<id>`, ring 2 (`:101-106`) | `st` 0, `h < t1` (`:103-105`) | no DERO attached (`:102`); `sa` and `kb` are 32 bytes whose sha256 are `ha` and `hk` (`:107-113`); the lock's asset is not attached (`:114-116`) | `st = 1`, `xa_ = sa`; pays `v_` to the taker (`:117-122`) |
| `Refund(id, sb)` | the maker `m_<id>`, ring 2 (`:133-138`) | `st` 0, `t1 ≤ h < t2` (`:135-137`) | `sb` hashes to `hb` (`:139-142`); nothing attached (`:133,143-145`) | `st = 2`, `xb_ = sb`; pays the maker (`:146-151`) |
| `Punish(id)` | any ring-2 signer (`:169`) | `st` 0, `t2 ≤ h < t2 + g2` (`:171-173`) | nothing attached (`:169,174-176`) | `st = 3`; pays `v_` to the **taker**, whoever sent it (`:177-182`) |
| `Reclaim(id, sb)` | the maker, ring 2 (`:194-198`) | `st` 0, `h ≥ t2 + g2`, with no end (`:196-197`) | `sb` hashes to `hb` (`:199-202`); nothing attached (`:194,203-205`) | `st = 4`, `xb_ = sb` (the key Refund uses); pays the maker (`:206-211`) |

- **Value flows.** The only outflow is `v_<id>` of `a_<id>`, once per lock. It goes to the taker on Claim or Punish,
  and to the maker on Refund or Reclaim. There is no fee and no other payee.
- **Storage keys.** `kind v n w1min w2min g2 initd`, and per lock `m_ t_ ha_ hk_ hb_ a_ v_ t1_ t2_ st_ xa_ xb_`
  (`htlc:23-26`).
- **Privileged paths:** none. There is no owner, admin or setter, and the install arguments are final.
- **Arithmetic.** `t2 + g2` stays below 2^64: `t2 ≤ lock height + 10^6` and `g2 ≤ 10^6` (`htlc:159-160`).

| Invariant | Test (`tests/`) |
|---|---|
| Per asset, the contract holds exactly Σ `v_i` over open locks after every accepted and refused call; each of the four exits happens | `zzconservation_htlc_test.go` `TestConservationHtlc` |
| The windows are disjoint: at each boundary height (`t1 − 1`, `t1`, `t2 − 1`, `t2`, `t2 + g2 − 1`, `t2 + g2`, `t2 + g2 + 1`, and 10^7 blocks on), for the taker, the maker, a stranger and a ring>2 caller, exactly the documented exit is open | `htlc_v3_test.go` `TestHtlcV3WindowMatrix` |
| Liveness: before `t2 + g2` time carries an open lock to an exit someone can send; after it, only the maker's Reclaim moves it | `zzadversarial_liveness_test.go` `TestHtlcPastT2TheLockAlwaysHasAnExit`; `htlc_v3_test.go` `TestHtlcV3OnlyTheMakersOwnWindowStaysOpen` |
| The install arguments and the minimum widths are enforced | `TestHtlcV3InstallArguments`, `TestHtlcV3LockWidths` |
| Reclaim pays the maker and stores `s_b` | `TestHtlcV3Reclaim` |
| A hash no preimage can match (capitals, not hex) is refused at Lock | `htlc_test.go` `TestHtlcLockRefusals` (`:366-369`) |
| Punish by a stranger pays the taker; exits refuse an attached asset | `htlc_v2_test.go` `TestHtlcPunishByAnyonePaysTheTaker`, `TestHtlcExitsRefuseAnAttachedAsset` |
| Claim, Refund and Punish as specified; asset locks; no second `Initialize` | `htlc_test.go` `TestHtlcClaim`, `TestHtlcRefund`, `TestHtlcPunish`, `TestHtlcAssetRoundTrip`, `TestHtlcInitializeNotCallable` |

Residual risk (§4, HTLC-R1): a taker who misses the Punish window depends on the maker's Reclaim to recover her XMR.

### 3.2 `swap.bas` v2: the Zcash swap's DERO leg

| Entrypoint | Who | When / state | Checks | Effect |
|---|---|---|---|---|
| `Initialize(wmin)` | anyone, once | not `initd` (`swap:17`) | `1 ≤ wmin ≤ 1,000,000` (`:18`) | stores `kind v n wmin` (`:19-23`) |
| `Lock(hash, taker, expiry, asset)` | ring 2 (`:41-42`) | — | `hash` is 64 lower-case hex (`:43,49`); `h+1 < expiry`, `expiry ≥ h + wmin`, `expiry ≤ h + 10^6` (`:45`); `taker` is required and valid (`:46-48`); value as in htlc (`:50-59`) | stores `m_ t_ h_ a_ v_ e_ st_=0`, `n += 1` (`:60-68`) |
| `Claim(id, secret)` | any ring-2 signer (`:81`) | `st` 0, `h < e_` (`:82-84`) | 32 bytes whose sha256 is `h_` (`:85-88`); nothing attached (`:81,90-92`) | `st = 1`, `x_ = secret`; pays the **stored taker** (`:93-98`) |
| `Refund(id)` | the maker, ring 2 (`:106-111`) | `st` 0, `h ≥ e_` (`:108-110`) | nothing attached (`:107,112-114`) | `st = 2`; pays the maker (`:115-119`) |

- **Value flows.** `v_<id>` of `a_<id>`, once: to the taker on Claim, to the maker on Refund.
- **Keys:** `swap:9-10`.
- **Privileged paths:** none.
- Claim can be front-run harmlessly: it pays `t_<id>` whoever sends it, and the secret is public from the moment the
  call reaches a mempool (`swap:73-77`).
- **Tests:**
  - conservation per asset: `zzconservation_test.go` `TestConservationSwap`;
  - `swap_test.go` `TestSwapEscrow`;
  - `swap_v2_test.go` `TestSwapV2ClaimPaysTheNamedTakerWhoeverSendsIt`, `TestSwapV2EveryOtherRuleHolds`;
  - `swap_gaps_test.go` `TestSwapInstallNamesAMinimumEscrowWidth`, `TestSwapLockRequiresATaker`,
    `TestSwapExitsRefuseTheLocksAssetAttached`;
  - `swap_repair_test.go` `TestSwapLockRefusesAHashItCouldNeverMatch`;
  - `zzswap_preimage_test.go` `TestSwapPreimageAsymmetryLetsTheMakerTakeBothLegs`, which pins "32 bytes only,
    lower-case hash only" against ZEC-1 and ZEC-2.

### 3.3 `board.bas` v3: the Zcash order board

It holds no value. Every call refuses DERO, and refuses the offer's give asset (`board:17-19`).

| Entrypoint | Who | When / state | Checks | Effect |
|---|---|---|---|---|
| `Initialize()` | anyone, once | not `initd` | — | `kind v n`, `aw = 200` in code (`board:24-28`) |
| `Post(chain, giveAsset, giveAmount, wantAmount, wantAddr, hash, expiry)` | ring 2 (`:42-43`) | — | lengths (`:44-46`); `h < expiry ≤ h + 10^6` (`:48`); `hash` is lower-case hex (`:49`); give asset not attached (`:50-51`) | stores the offer, `st = 0` (`:53-62`) |
| `Accept(id, refundPkh, cltv)` | any ring-2 signer except the maker (`:76,81`) | `st` 0, `h < e_` (`:78-79`) | `refundPkh` is 40 characters; `0 < cltv < 500,000,000`, the BIP 65 threshold (`:80`) | `st = 1`, `t_ rp_ cl_`, and `ad_ = h + aw` (`:84-88`) |
| `Cancel(id)` | the maker (`:102`) | open: any time; accepted: from `min(e_, ad_)` (`:105-107`) | — | `st = 2` (`:108`) |

- **Value flows:** none.
- **Keys:** `board:7-12`.
- **Privileged paths:** none. `aw` is a constant: changing it is a new contract (MT-J19).
- **What the board does not do.** It does not bind the `swap.bas` lock: nothing on chain ties an accepted row to the
  maker's later Lock. The companions compare the two (`docs/ZEC_SWAP.md`; `docs/MAINNET_TODO.md` MT-C08).
- **Tests:**
  - `board_test.go` `TestSwapBoard`;
  - `board_v3_test.go` `TestBoardV3AcceptanceDeadline`, `TestBoardV3AcceptRefusesAUnixTimeRefundHeight`,
    `TestBoardV3RefusesTheGiveAssetAttached`;
  - `swap_repair_test.go` `TestBoardPostRefusesAHashTheDeroLegCouldNeverMatch`;
  - balance stays 0: `zzconservation_test.go` `TestConservationBoard`;
  - the GAP-026 residual, pinned: `zzadv_market_test.go` `TestBoardAcceptByAnyoneHoldsAnOfferOnlyUntilItsAcceptanceDeadline`.

### 3.4 `factory.bas` v2: the registry, with no owner

| Entrypoint | Who | Checks | Effect |
|---|---|---|---|
| `Initialize(treasury, regfee)` | anyone, once (`factory:17`), at any ring size: no signer is read | `treasury` is valid (`:19-20`) | stores `kind v treasury tstr regfee pc lc nc initd` (`:21-29`) |
| `RegisterPool`, `RegisterLaunch`, `RegisterNFT`, `RegisterNFA` (`scid H`) | anyone | `STRLEN(scid) == 32`, `DEROVALUE() ≥ regfee` (`:72-73`) | forwards the whole `DEROVALUE()` to `treasury` in the same call (`:74-75`); appends `HEX(scid)` under `pool_<n>`, `launch_<n>`, or `nft_<n>` / `nfa_<n>` (the last two share `nc`) (`:35-68`) |

- **Value flows.** Every registration fee goes straight to `treasury`, so the factory holds 0 DERO.
- **Privileged paths:** none. No function but `Initialize` writes `treasury`, `tstr` or `regfee`, and nothing reads
  `SIGNER()` for authority (`factory:5-12`).
- `regfee` is unbounded, but fixed in public at install, and `verifydeploy` prints it (`contracts/README.md`, factory).
- A registration is a hint. The app verifies each registered SCID's code hash and `treasury` before listing it (§2,
  item 2).
- **Tests:**
  - `registry_test.go` `TestFactory`, `TestFactoryRegisterNFA`, `TestFactoryFixedAtInstall` (every name that was ever
    owner-gated, and `Initialize`, from the installer, a stranger and a ring>2 caller at three heights: all refused,
    state unchanged, balance 0), `TestFactoryOldEntrypointsAreGone` (reads the source);
  - `zz_adversary_owner_test.go` `TestFactoryHasNoOwnerToTransferOrRenounce`, `TestNoCallCanFreezeTheRegistry`.

### 3.5 `pool.bas` v2: an LP-owned constant-product AMM

Maths, and the proof that the refund side suffices: `contracts/README.md`, pool. Executable model:
`reference/pool.go`. Rounding: outputs floor, fees ceil, LP mint floor (`pool:6`).

| Entrypoint | Who | Effect and key checks |
|---|---|---|
| `InitializePrivate(asset H, quote H, treasury S, fee U, pfee U)` | installer, once | `asset` ≠ DERO and ≠ `quote`; `fee ≤ 100`; `pfee ≤ 10000`; treasury valid (`pool:15-44`) |
| `AddLiquidity(min_lp)` | ring 2 (`:51-52`) | quote `d` and asset `a` both > 0, no LP attached (`:58-59`), no stray DERO on a non-DERO quote (`:55`), wrap guard on both totals (`:63`). First deposit: `d ≥ 100,000`, `lp = d`, mints `d − 1000` (`:130-210`). Later deposits: `L = min(⌊d·lp/rd⌋, ⌊a·lp/ra⌋)`, the other side takes `⌈L·r/lp⌉`, and the excess is refunded (`:300-554`). Mints `L` LP (the pool's own asset) |
| `RemoveLiquidity(min_dero, min_asset)` | ring 2 | LP `l` attached, `l ≤ lp − 1000` (`:121`); pays `⌊l·rd/lp⌋` quote and `⌊l·ra/lp⌋` asset (`:122-130`) |
| `SwapDeroForAsset(min_out, publish)`, `SwapAssetForDero(…)` | ring 2 | `buyto` / `sellto` paying the signer: `fee = ⌈x·fee/10⁴⌉`, `pf = ⌊fee·pfee/10⁴⌋`, `out = ⌊(x−fee)·r_out/(r_in + x − fee)⌋` (`:154-243`) |
| `SwapDeroForAssetTo(dest, …)`, `SwapAssetForDeroTo(dest, …)` | anyone, any ring size | the same maths, paying `dest`. If the inner call fails, `refundto(dest)` returns everything attached **to `dest`**, records `rf_<txid>`, and the call **commits** (`:144-152,191-215`). They never publish a trade record |
| `ClaimFees()` | anyone, any ring size | pays `fd` quote and `fa` asset to the `treasury` fixed at install; `fc += fd` (`:247-262`) |

- **Value flows.**
  - Traders receive the swap output.
  - LPs receive their proportional reserves.
  - The treasury receives `fd` and `fa` through `ClaimFees`.
  - `MIN_LIQ`, the first 1,000 LP units, stays locked for ever (`RISKS.md` R-4).
- **Keys:** `pool:11-13`, plus `fc`, `qt`, `initd` and `rf_<txid>`.
- **Privileged paths:** none. The treasury only receives.
- **Invariants:** quote balance = `rd + fd`; asset balance = `ra + fa`; LP held by wallets = `lp − 1000`;
  `k = rd·ra` never falls on a swap (`pool:7-9`; `contracts/README.md`).
- **Tests:**
  - `pool_test.go` `TestPoolFuzz` (against `reference/pool.go`), `TestPoolSmoke`, `TestPoolRejects`,
    `TestPoolInstallValidation`;
  - `zzconservation_test.go` `TestConservationPool`, which includes LP supply;
  - `zzquote_test.go` `TestPoolNonDeroQuoteLifecycle`, `TestPoolNonDeroQuoteRefusesStrayDero`;
  - `zzextreme_test.go` `TestPoolExtremeReserves`, `TestPoolQuoteBalanceNearWrap`;
  - `ringswap_test.go` `TestPoolSwapToNamedAddress`;
  - `sendorder_test.go` `TestNoExternalSendBeforeAFailureExit`;
  - `uint128_test.go` and `zzdvmarith_test.go` `TestDVMArithmeticMatchesMathBig` for `muldiv`.

### 3.6 `launch.bas` v2: a bonding curve that graduates into a built-in AMM

Maths, and proofs of `cost ≤ deff`, `real ≥ 0` and price continuity: `contracts/README.md`, launch. Model:
`reference/launch.go`.

| Entrypoint | Who | Effect and key checks |
|---|---|---|
| `InitializePrivate(name, ticker, descr, icon, tags, quote, dec, supply, grad, cbps, tax, tmode, treasury, fee, pfee)` | ring 2; the signer becomes `creator` (`launch:20-21,60`) | `supply ∈ [10⁶, 2⁶³)`, `grad ∈ [10⁶, 10¹⁵]` atomic units, `cbps ≤ 500`, `tax ≤ 1000`, `tmode ≤ 3` with `tax = 0` iff `tmode = 0`, `fee ≤ 100`, `pfee ≤ 10000` (`:22-32`). Sends `alloc = ⌊supply·cbps/10⁴⌋` to the creator (`:40,90-91`) |
| `SwapDeroForAsset` / `…To`, `SwapAssetForDero` / `…To` | as in pool (ring 2 / any ring) | Phase 0, the curve (`:108-148`): the whole fee goes to `fd`, tax to `tacc`; the last buy is capped at the remaining curve tokens and the overshoot refunded untaxed (`:45-59,146-147`); when `sold == cs` it graduates: `phase = 1`, `rd = real`, `ra = Lq` (`:141-145`). A sell needs `a ≤ sold` and `dout ≤ real` (`:208-211`). Phase 1, the AMM (`:149-161,227-243`): the fee is split by `pfee`, and tax is taken from the DERO leg |
| `ClaimFees()` | anyone | `fd` quote and `fa` tokens to the treasury; `fc += fd` (`:248-263`) |
| `ClaimTax()` | the creator, `tmode` 1 (`:270`) | pays `tacc` (`:271-274`) |
| `Buyback()` | anyone, any ring size, `tmode` 2, phase 1 (`:284`) | spends `min(tacc, ⌊rd/100⌋)` on an internal buy; the tokens go to `locked`; at least 20 blocks between calls (`:285-301`) |
| `Distribute()`, `Stake()`, `Unstake(amt)`, `Claim()` | anyone / ring 2, `tmode` 3 | a MasterChef accumulator `sacc` at 2^32 with eras (`sera`, `sea_<era>`); rewards are taken from `rpool` and capped by it (`:307-420`) |

- **Value flows.**
  - Buyers and sellers receive their output.
  - The treasury receives `fd` and `fa` through `ClaimFees`.
  - The creator receives `alloc` at install, and `tacc` in `tmode` 1.
  - Stakers receive from `rpool` in `tmode` 3.
  - Reserves have no withdrawal path at all.
- **Keys:** `contracts/README.md`, launch, plus `qt fc sera sea_<n> se_<addr> bbh gh rf_<txid> initd`.
- **Privileged paths.**
  - The creator: `ClaimTax` in `tmode` 1, which pulls accrued tax only, and the install allocation of at most 5 %.
  - The treasury only receives.
- **Invariants:**
  - quote balance = (phase 0: `real`, phase 1: `rd`) + `fd + tacc + rpool`;
  - in phase 0, `rd − real = vd0` and `ra + sold = vt0`;
  - tokens held by wallets = `alloc + sold − staked` in phase 0, and `supply − ra − fa − locked − staked` in phase 1
    (`launch:4`; `tests/zzconservation_test.go:12-14`).
- **Tests:**
  - `launch_test.go` `TestLaunchFuzz`, `TestLaunchLifecycleCreatorTax`, `TestLaunchBuyback`, `TestLaunchStaking`,
    `TestLaunchInstallValidation`, `TestLaunchGradBound`, `TestLaunchGradBand`;
  - `zzconservation_test.go` `TestConservationLaunch`, in all three tax modes;
  - `saturation_test.go` `TestLaunchDistSaturation`, `TestLaunchGraduationRefundFee`;
  - `zzextreme_test.go` `TestLaunchExtremeCurve`, `TestLaunchCurveAtContractCeiling`, `TestLaunchGradCeilingIsUnmodelled`;
  - `ringswap_test.go` `TestLaunchSwapToNamedAddress`;
  - `zzquote_test.go` `TestLaunchMaxMetadataInstallGas`;
  - `launch_reinit_test.go` `TestLaunchInitializeNotCallableAnyCbps`.

### 3.7 `nft.bas` v1: editions earned by holding a token

| Entrypoint | Who | Effect |
|---|---|---|
| `InitializePrivate(…, token H, minhold, holdblocks, maxsupply, price, treasury, fee)` | ring 2; the signer becomes `creator` (`nft:20-21,39`) | `minhold, maxsupply ≥ 1`; `1 ≤ holdblocks ≤ 10⁷`; `fee ≤ 100` (`:24`) |
| `Lock()` | ring 2 | takes `token ≥ minhold`, only that; one lock per address (`:46-60`) |
| `Mint()` | ring 2, after `holdblocks`, once per address for the contract's life (`mt_`), while `minted < maxsupply` | takes DERO `≥ price` and refunds the excess; `fee` bps to `fd`, the rest to `cacc`; sends one edition (`:62-88`) |
| `Unlock()` | the locker | returns all the locked tokens (`:90-102`) |
| `ClaimCreator()` | the creator (`:108`) | pays `cacc` (`:109-111`) |
| `ClaimFees()` | anyone | pays `fd` to the treasury (`:117-128`) |

- **Privileged paths:** the creator pulls `cacc` only.
- **Invariants:** DERO = `fd + cacc`; the token balance = Σ `lk_`; editions out = `minted` (`nft:6`).
- **Tests:** `zzconservation_test.go` `TestConservationNFT`; `registry_test.go` `TestNFT`, `TestNFTMintOncePerAddress`;
  `zzstray_test.go` `TestNFTRefusesStrayValue`.

### 3.8 `nfa.bas`: ART-NFA-MS1 v1.1.1 with a quote and a protocol fee

The upstream file, its hash and the eight changes are in the header (`nfa:1-66`). More in `docs/NFA_BUILD.md` and
`docs/NFA.md` §2.5. Line numbers inside functions are the standard's own.

| Entrypoint | Who | Effect |
|---|---|---|
| `InitializePrivate(artificerFee, royalty, ownerCanUpdate, headers…, quote, protoFee, treasury)` | installer, once (`owner` absent) | `owner = creatorAddr = signer`; `artificerAddr` is Civilware's hard-coded address; `royalty, artificerFee ≤ 100`; `protoFee + 100·(royalty + artificerFee + charity) ≤ 10000`; sends the one unit to the installer (`nfa:68-127`) |
| `ClaimOwnership()` | whoever sends the unit | becomes `owner` and gets the unit back. Above ring 2 the unit goes to the recorded owner (`:131-141`) |
| `Update(...)` | `creatorAddr` for ever, and `owner` if `ownerCanUpdate` = 1 | rewrites the icon, cover, file, file-signature and tags URLs (`:143-160`) |
| `Start(listType, duration, startPrice, charityAddr, charityPerc)` | the owner, with the unit attached | a sale or an auction. Duration 1 h to 7 days from `BLOCK_TIMESTAMP()`; the charity share is clamped, and stored only after its address is checked (`:169-201`) |
| `BuyItNow()` | any ring-2 signer except the owner | on an active sale, a quote `≥ startPrice` buys the unit; an ended auction settles to its winner (`:207-238`) |
| `Bid()` | any ring-2 signer except the owner | outbids and refunds the previous bid; the anti-snipe rule extends the end to `now + 900 s` (`:240-275`) |
| `CloseListing()`, `CancelListing()` | the owner | settle or return. Cancel only within `cancelBuffer` = 300 s of the start, refunding the bid (`:277-305`) |
| `PayOwed(payee)` | anyone, any ring size | pays everything owed to `payee`, as the call's only transfer (`:422-440`) |

- **Value flows.**
  - A settlement sends the royalty to `creatorAddr`, the remainder to `owner` and the unit to the buyer
    (`processDEROFinalPayment`, `nfa:379-404`).
  - The artificer's, charity's and treasury's shares are credited to `ow_<address>` and leave only through `PayOwed`.
  - An outbid bidder is refunded in the same call.
- **Keys:** the standard's, plus `quote protoFee treasury owed ow_<addr> op_<addr>` (`nfa:61-63`).
- **Privileged paths.**
  - `Update` by `creatorAddr`, which lasts for the contract's life (NFA-5).
  - The owner's listing calls.
  - `artificerAddr`, fixed in code (GAP-035).
  - The treasury receives only.
- **Invariants.** The quote balance = `owed` + `currBidAmt` while an auction holds a bid, and exactly one unit exists
  (`tests/nfa_test.go`, checked after every call; `TestNFAConservationAfterEveryFeePath`).
- **Tests:**
  - `nfa_test.go`: every test;
  - `nfa_r4_test.go`, `nfa_stray_test.go` (`TestNFAStrayValueIsRefused`), `zzsplitpay_test.go`;
  - `zzadversarial_liveness_test.go` `TestNFAUnregisteredCharityAddressNoLongerCostsTheSeller`,
    `TestAStrangerCannotStrandAnAuctionWinnersPayment`, `TestNFAArtificerFeeOnAChainWhereTheArtificerNeverRegistered`;
  - `zzadv_market_test.go` `TestNFAEndedAuctionBuyItNowSettlesTheWinningBid`;
  - `sendorder_test.go` `TestNoUnverifiedPayeeBeforeTheLastTransfer` (`TestPayeeCheckerFlagsTheLIV01Shape` shows it
    catches the pre-repair shape).

### 3.9 `founders.bas` v1: the Founders Pass

| Entrypoint | Who | Effect |
|---|---|---|
| `InitializePrivate(name, descr, icon, file, price, maxsupply, start, reserve, share, mfee, treasury)` | ring 2; the signer becomes `creator` | `maxsupply ≤ 100,000`; `reserve ≤ maxsupply/5`, minted to the installer; `minhst = 1 + ⌊maxsupply/50⌋` (`founders:12-55`) |
| `Mint(n)` | ring 2, from block `start` | pays exactly `n × price`; the units go to the caller and the DERO straight to `treasury` (`:58-74`) |
| `Register()` / `Withdraw(u)` | ring 2 holder | registers the units sent with the call, or returns `u` of them; settles first; the registry is compacted by swap-delete (`:77-124,289-305`) |
| `Claim()` | ring 2 holder | settles its pending reward (`:126-134`) |
| `Deposit()` | anyone | DERO for the holders, `dep += d` (`:137-146`) |
| `Distribute()` | anyone | releases `und` once `hst ≥ minhst` (`:149-154,241-262`) |
| `PayBatch(from, n ≤ 15)` | anyone | settles holders `from..from+n-1` (`:158-175`) |
| `List(price)`, `Unlist(id)`, `Buy(id, n)` | holders / buyers, ring 2 | fixed-price market; `mfee` bps of each sale is distributed to the holders (`:178-235`) |

- **Maths.** `acc += ⌊pool·2^32/hst⌋`. The pool gives up `⌈inc·hst/2^32⌉`, and a holder's pending reward is
  `⌊hu·(acc − hd)/2^32⌋`, capped by `rpool`. Eras roll over on wrap (`contracts/README.md`, founders and rule 3).
- **Value flows.**
  - Mint proceeds go to the treasury.
  - Sale proceeds go to the seller, less `mfee`.
  - Deposits and `mfee` go to the registered holders.
  - Nothing else leaves.
- **Privileged paths.**
  - None after install.
  - The installer's advantage is the free `reserve`.
  - `share` is a stored number that nothing enforces (`founders:2-3`; `docs/MAINNET_GAP_ANALYSIS.md` §1.2).
- **Invariants:** DERO = `rpool + und`; units held by wallets = `minted − hst − listed` (`founders:9-10`;
  `tests/zzconservation_test.go:14`).
- **Tests:**
  - `founders_test.go` `TestFoundersMintRegisterRewards`, `TestFoundersPayBatchGas`, `TestFoundersFuzz` (3,000
    randomised steps against a big.Int model);
  - `zzconservation_test.go` `TestConservationFounders`;
  - `saturation_test.go` `TestFoundersAccSaturation`;
  - `founders_supply_test.go`;
  - the adversarial tests that pin today's open behaviour: `founders_attack_test.go`,
    `refute_founders_minhst_test.go`, `zzrefute_founders_minhst_test.go` (§4).

### 3.10 `social.bas` v1 and `chat.bas` v1

- **`social.bas`: `Profile`, `Follow`, `Unfollow`** (ring 2, `social:17-67`). They store per-address strings and
  counters, take no value and refuse DERO. It has no privileged path. Tests: `registry_test.go` `TestSocial`;
  `zzstray_test.go` `TestSocialRefusesStrayValue`.
- **`chat.bas`: `InitializePrivate(treasury, fee, maxlen ≤ 400)`, then `Post(room, text)`** (ring 2, exactly `fee`
  attached, forwarded to the treasury at once; `chat:14-52`). Messages are append-only and permanent (`chat:5-8`). It
  holds nothing and has no privileged path. Tests: `chat_test.go` `TestChatFeeIsExactAndPaid`, `TestChatBounds`,
  `TestChatRoomsAreIndependent`, `TestChatTextKeepsItsSeparators`.

### 3.11 `vault.bas` v3: not deployed

A k-of-n federation that wraps a foreign asset: `contracts/README.md`, vault, and `research/wrapper/README.md`. It is
in the suite, but no mainnet record names it and `simdeploy` never installs it (`docs/MAINNET_DEPLOYMENT.md` §1).
Its signers hold custody of the foreign coins (`docs/MAINNET_GAP_ANALYSIS.md` §1.2, the vault rows). Reviewing it is
optional. If you do, the open vault findings are LIV-03, LIV-06, LIV-09, LIV-10, SEC-K-01, SEC-K-04 and SEC-K-05 in
`docs/audit/FINDINGS_2026-09-10.md`, none of which has a status line.

---

## 4. Known issues and accepted risks

"Open" in `docs/audit/FINDINGS_2026-09-10.md` means the finding has no Status line (its own lines 10-16).

### 4.1 Open, or accepted, in a mainnet contract

| ID | Contract | Issue | Status | Evidence |
|---|---|---|---|---|
| **FEE-01** (= FOUND-1) | founders | At 20 editions `minhst = 1`, so the first single registrant takes everything waiting in `und` | **open, operator decision** MT-A08 / MT-J14. The options (a)–(d), and what each costs, are in `contracts/README.md`, founders, "Open, and deliberately not changed". (d) makes the operator the party trusted to be fair | `founders_attack_test.go` `TestFoundersMainnet20FirstRegistrantSweepsEverything`; `founders_supply_test.go` `TestFoundersMinhstAt20LetsOneUnitSweepEverything` |
| **FEE-02** | founders | `und` has no time weighting: whoever registers the stake that crosses the floor shares the whole backlog | **open, operator decision**, as FEE-01 | `TestFoundersFrozenPoolIsSweptByWhoeverCrossesTheFloor` |
| FEE-07 | founders | Just-in-time registration earns a full share of a deposit | open. Option (c) of FEE-01 would close it | `TestFoundersJustInTimeRegistrationTakesAFullShare` |
| FEE-M1 | founders | DERO deposited below `minhst` has no exit until the floor is reached | open, and tied to FEE-01 | `zzrefute_founders_minhst_test.go` `TestRefuteFoundersUndIsFrozenBelowMinhstAtTheDeployedSupply` |
| FEE-04 / MT-K01 | founders; non-DERO quotes | Fees in a non-DERO quote reach no pass holder; an asset attached to `Deposit` is kept | open, operator decision MT-K01 | `TestFoundersCannotTakeAWrappedFee`; `zzrefute_nfa_protofee_test.go` |
| MT-J16 | founders | The pass's numbers (supply, reserve, share, fee, texts) are immutable once installed | **operator decision**. FEE-12, MX-02 and MISS-03 are the findings behind it | `docs/MAINNET_TODO.md` §J |
| **GAP-026** | board | Anyone may Accept any open offer and hold it for `aw` = 200 blocks, then do it again, for one fee each time | **open, operator decision** MT-J18: a taker bond, a maker allow-list, or the board declared advisory. `docs/MAINNET_SECURITY_GATE.md` lists it as open by decision | `TestBoardAcceptByAnyoneHoldsAnOfferOnlyUntilItsAcceptanceDeadline` |
| MT-J19 | board | `aw = 200` is an assumed value; changing it later means a new SCID | **operator decision** | `board.bas:27` |
| **HTLC-R1**, the v3 residual | htlc | A taker who misses the Punish window `[t2, t2 + g2)` recovers her XMR only if the maker Reclaims, which reveals `s_b`. A maker who never reclaims (hostile, or he has lost his state or `s_b`) leaves his DERO and her XMR locked for good. Anyone may send the Punish for her inside the window | **accepted by design.** `htlc.bas:16-20` and `docs/HTLC_V3_COMPANION_NOTES.md` §4 treat it as the one per-party window that ruling MT-J11(a) accepts, and MT-J13 chose the form (decided 2026-09-11). At `e06c28a`, however, `docs/MAINNET_TODO.md` §J still lists MT-J11 as `decide`, and **G2's value is open** (MT-J13; the proposal is 4,800 blocks, `docs/HTLC_V3_COMPANION_NOTES.md` §7) | `TestHtlcV3OnlyTheMakersOwnWindowStaysOpen`; `TestHtlcPastT2TheLockAlwaysHasAnExit`; `docs/SECURITY_THREAT_MODEL.md` §4 (LIV-07 row) |
| HTLC-R2 | htlc (companion side) | Exits built near a window's edge can land past it (§2, items 11–12): a Refund near `t2` (MT-B27), a Punish near `t2 + g2` (MT-B29), a Lock at exactly `w1min` | the contract is as designed; the cutoffs are companion policy. At `e06c28a` MT-B27 and MT-B28 are `open` and MT-B29 is `decide`. The uncommitted working tree of `docs/HTLC_V3_COMPANION_NOTES.md` §4 describes MT-B27 and MT-B28 as built | `TestHtlcV3LockWidths`; `docs/HTLC_V3_COMPANION_NOTES.md` §3–§4 |
| LIV-05 | launch | In `tmode` 2, tax accrued on a launch that never graduates can never move | open | `zzadversarial_liveness_test.go` `TestLaunchTmode2TaxIsLockedForeverIfTheTokenNeverGraduates` |
| NFA-5 | nfa | `creatorAddr` can rewrite the file URLs under a live bid, even with `ownerCanUpdate` 0 | open, operator decision (FINDINGS D-6) | `nfa_stray_test.go` `TestNFAUpdateRewritesTheFileUnderALiveBid` |
| ADV-PRICE-02 | nfa | The anti-snipe extension has no cap, so two bidders can keep an auction open indefinitely | open, operator decision (`docs/NFA.md` R4). It is the standard's behaviour | `zzadv_market_test.go` `TestNFAAntiSnipeExtensionHasNoCap` |
| GAP-035 | nfa | The artificer share is paid to Civilware's hard-coded mainnet address; the installer chooses 0–100 % | informational; the operator is to confirm it | `nfa.bas:95` |
| GAP-027 residual | all | An asset the contract cannot name, attached to a call, is kept (§2, item 14) | **accepted**: a DVM limit | `zzstray_test.go` (the last case of each test) |
| NFA-4 residual | nfa | A `ClaimOwnership` or `Start` above ring 2 keeps whatever else was attached; the unit goes to the recorded owner | **accepted**: there is no signer to refund (`nfa.bas:48-53`) | `TestNFAStrayValueIsRefused` |
| R-2 | pool, launch, all | A hand-made call above ring 2 that is refused loses what was attached | **accepted** (`RISKS.md` R-2); the app's own paths avoid it | `ringswap_test.go` |
| R-5 / XMR-01 | htlc, swap, board, pool, launch, chat | Storage grows without bound; anyone can post locks, offers and messages at the gas price | **accepted** (`RISKS.md` R-5). The companions bound their reads (XMR-01, repaired in `9064f82`) | — |
| MT-K02 | swaps | No protocol fee on the swaps; one would need a new escrow version | operator decision | — |

### 4.2 Fixed, with the test that holds each

| ID | Fix | Test |
|---|---|---|
| LIV-07 | `htlc.bas` v2 let anyone Punish, and v3 bounds it to `[t2, t2 + g2)` and adds Reclaim | `TestHtlcPastT2TheLockAlwaysHasAnExit`, `TestHtlcV3WindowMatrix` |
| GAP-023 / MT-A02 | `htlc.bas` Lock refuses a hash that is not lower-case hex (`htlc:63`) | `TestHtlcLockRefusals` |
| GAP-024, GAP-025 | `swap.bas` requires a taker (`swap:46-48`) and a minimum width `wmin` (`swap:45`) | `TestSwapLockRequiresATaker`, `TestSwapInstallNamesAMinimumEscrowWidth` |
| GAP-027 | DERO, the lock's or offer's asset, the token and editions refused where they are not taken | `TestHtlcExitsRefuseAnAttachedAsset`, `TestSwapExitsRefuseTheLocksAssetAttached`, `TestBoardV3RefusesTheGiveAssetAttached`, `zzstray_test.go` |
| ZEC-1, ZEC-2, ZEC-3 | 32-byte preimages only; lower-case hashes on swap and board; `cltv` below 5·10⁸ | `TestSwapPreimageAsymmetryLetsTheMakerTakeBothLegs`, `TestSwapLockRefusesAHashItCouldNeverMatch`, `TestBoardPostRefusesAHashTheDeroLegCouldNeverMatch`, `TestBoardV3AcceptRefusesAUnixTimeRefundHeight` |
| ADV-PRICE-07 | `board.bas` v3's acceptance deadline `ad_` (the residual is GAP-026) | `TestBoardV3AcceptanceDeadline` |
| SEC-K-02, LIV-08, SEC-K-06, GAP-005 | `factory.bas` v2 has no owner (MT-J15(b)) | `TestFactoryFixedAtInstall`, `TestFactoryOldEntrypointsAreGone`, `TestFactoryHasNoOwnerToTransferOrRenounce` |
| LIV-01, DVM-1 | `nfa.bas` settles only to signers and owes the other shares (`PayOwed`) | `TestNFAPayOwed`, `TestNFAUnregisteredPayeesCannotCostTheSeller`, `zzsplitpay_test.go` |
| DVM-3 / MT-A10 | a static check across every contract. One allow-list entry, founders `PayBatch`, whose payees all signed `Register` (`tests/sendorder_test.go:139-141`) | `TestNoUnverifiedPayeeBeforeTheLastTransfer` |
| NFA-1, LIV-02, ADV-PRICE-01 | every end of an auction settles or refunds its bid | `nfa_r4_test.go`, `TestNFAEndedAuctionBuyItNowSettlesTheWinningBid`, `TestAStrangerCannotStrandAnAuctionWinnersPayment` |
| DVM-2 | `Start` stores the charity share only after checking the address | `TestARefusedStartCannotPoisonTheCharityShare`, `TestAResetThenARefusedStartLeavesNoShare` |
| NFA-4 / MT-A09 | every NFA entrypoint refuses value it does not take | `TestNFAStrayValueIsRefused` |
| GAP-022, GAP-033 / MT-A13 | randomised conservation after every call, own-token supply included, for every contract that holds value | `zzconservation_test.go`, `zzconservation_htlc_test.go`, `nfa_test.go` |
| INT-02 | `reference/launch.go` models `grad` up to 10¹⁵, as the contract accepts (`reference/launch.go:33`) | `TestLaunchGradCeilingIsUnmodelled` |
| a double payment in the `…To` wrappers | no external send may precede a failure exit in any function a wrapper can reach | `TestNoExternalSendBeforeAFailureExit` |
| the accumulators wrapping | eras in `founders.bas` and `launch.bas` (`contracts/README.md`, rule 3) | `TestFoundersAccSaturation`, `TestLaunchDistSaturation` |

### 4.3 What the operator must still decide before the install

These were taken from `docs/MAINNET_TODO.md` §J–§K at `e06c28a`. The decision sheet is being brought up to date
under X-14.
- **MT-J13:** G2, `htlc.bas`'s `g2`. It has no default, and `simdeploy` refuses to install without it.
- **MT-J14 / MT-A08:** FEE-01 and FEE-02.
- **MT-J16:** the Founders Pass numbers.
- **MT-J18:** GAP-026.
- **MT-J19:** confirm `aw = 200`.
- **MT-J11:** confirm that the per-party window is accepted (see HTLC-R1).
- **MT-K01:** fees in non-DERO quotes.
- **MT-K02:** a fee on the swaps.
- **NFA-5 and ADV-PRICE-02:** operator calls.
- **GAP-035:** confirm the artificer address.
- **MT-J05 and MT-B29:** the profile values behind `w1min` and `w2min` (400 each, `docs/MAINNET_DEPLOYMENT.md` §1)
  and the Punish cutoff.

---

## 5. How to run everything

**What you need.**
- Go at the toolchain `go.mod` names: `toolchain go1.26.8`.
- A POSIX shell. Git Bash works on Windows; `tests/run.ps1` is the PowerShell twin of `tests/run.sh`.
- The vendored DERO node at its pinned commit. `vendor/` is git-ignored (`.gitignore:2`), so fetch it:

```
cd <repo>
git -c core.longpaths=true clone https://github.com/deroproject/derohe vendor/derohe
git -C vendor/derohe checkout e9df1205b6603c62f0651d0e18e5e77a2584b15e
cd tools && go run ./verifyvendor -root .. && cd ..     # refuses anything but the pinned commits (deploy/vendor.lock)
```

That is `README.md` §8 and `deploy/README.md:17-24`. The last command also checks the other vendored trees and
`vendor/bin`: fetch those as `deploy/README.md` says, or read its errors about them as expected when you only need
derohe.

**The DVM suite: every contract, run by derohe's own interpreter.**

```
tools/withlock.sh dvm bash tests/run.sh -v          # everything
tools/withlock.sh dvm bash tests/run.sh -run Htlc   # a subset
VEIL_CONS_N=4000 tools/withlock.sh dvm bash tests/run.sh -run Conservation   # longer conservation runs (default 400)
```

- `tests/run.sh` copies `reference/`, `tests/` and `contracts/` into `vendor/derohe/veil/`, refuses floating point in
  money code, and runs `go vet` and `go test -mod=vendor` inside derohe's module. derohe has no `require` lines, so
  its own vendor directory is the only way it builds.
- `tools/withlock.sh` serialises runs through a directory lock under `.sim/`.
- The harness is derohe's executor itself: `dvm.Execute_sc_function`, `SanityCheckExternalTransfers`, `ErrorRevert`
  and `ProcessExternal`, on an in-memory store, with no node (`tests/harness/chain.go:1-3`).
- **Results.** `docs/TESTING_AND_VERIFICATION.md` records 175 runs and 0 failures on these contracts (2026-09-12). A
  run made for this package at `e06c28a`: see the last line of this section.

**`tools/check.sh`: the full validation, as the coordinator runs it before a commit.**
- Its steps: `dvm gofmt vet test fuzz-xmrswap fuzz-zecswap js genconfig hygiene verifyvendor`. `--only=` and `--skip=`
  select steps (`tools/check.sh:2-12`).
- Each step prints PASS or FAIL with its log under `.sim/check/`.
- It needs Node for the `js` step, and about 30 minutes for the two crash fuzzes. For the contracts alone,
  `tools/check.sh --only=dvm` is enough.

**`tools/verifydeploy` against the simulator.**

```
tools/check.sh --verifydeploy                                  # or, by hand:
cd tools && go run ./verifydeploy -network simulator -app ../app
```

- It needs the DERO simulator's daemon at `127.0.0.1:20000` (`deploy/networks.json` `simulator.daemon`), built from
  `vendor/derohe` (`deploy/README.md:25`), with the base set installed as `deploy/README.md` describes. The simulator
  wipes its chain on every start (`deploy/README.md:39`).
- It reads with `DERO.GetInfo` and `DERO.GetSC` and sends nothing.
- It checks installed code against `Canon(source)`, the record's template hashes, and every role
  (`tools/verifydeploy/README.md`).
- `go run ./verifydeploy -roles` prints the role table, with citations, and needs no node.
- A recorded simulator run: `research/release/verifydeploy-final-sim.log` (`docs/MAINNET_TODO.md` MT-E01).

**The hashes again.**
- §1.2's program.
- Or the existing tests, from `tools/`:

```
go test -run TestPinnedCodeIsTheTreesHtlc ./xmrswap
go test -run Pin ./zecswap
go test ./verifydeploy/srchash
```

- The template equality check, `TestShippedTemplatesAreTheTestedContracts`, is in the DVM suite.

**Run for this package** (2026-09-12, at `e06c28a`, contracts unchanged in the working tree):
`tools/withlock.sh dvm bash tests/run.sh -v` gave 175 `--- PASS` lines, 0 `--- FAIL`, and
`ok github.com/deroproject/derohe/veil/tests 440.135s`, exit 0, on go1.26.8. The time is from a shared, loaded machine;
the recorded run took 82.3 s.

---

## 6. Questions for the reviewer

Where review adds the most, most valuable first.

1. **What to try to break first.**
   - (a) Value conservation in `launch.bas` across graduation (`launch:45-59,141-147`) and the staking eras
     (`:373-420`).
   - (b) The `…To` wrappers in `pool.bas` and `launch.bas`. After a failed inner call they **commit** and refund to
     `dest` (`pool:144-152,201-215`). `TestNoExternalSendBeforeAFailureExit` proves no send precedes a failure exit.
     Can any `STORE` in `buyto`/`sellto` or their helpers (`tick`, `post`) precede one, and so commit in a call that
     "failed"?
   - (c) `htlc.bas`'s four windows at their edges.
   - (d) `nfa.bas`'s settlement and `PayOwed` under unregistered payees.
2. **Storage gas near 20,000.**
   - The largest installs are `nfa.bas` (15,941 wrapped) and `launch.bas` with maximal metadata
     (`TestLaunchMaxMetadataInstallGas`).
   - Is there an entrypoint whose storage gas can pass 20,000 with arguments at their maxima, and so be permanently
     uncallable? Candidates: a `board.bas` Post with a 128-byte `wantAddr`, a `launch.bas` buy that writes a
     trade record, a price point and graduation keys in one call, a `founders.bas` Register that creates a holder,
     an `nfa.bas` settlement that creates three `ow_` keys.
   - A path any exit depends on matters most.
3. **Ring 2.**
   - Does every path that pays `SIGNER()` fail closed for the 33-zero signer? Some rely on `s != LOAD(...)` alone,
     with no `IS_ADDRESS_VALID`: `launch.bas` ClaimTax (`:270`), `nft.bas` ClaimCreator (`:108`).
   - `nfa.bas` tests `ADDRESS_STRING(SIGNER()) == ""` (`:133,172,210,243`). Is that exactly the ring>2 case on this
     derohe?
   - Can the ring>2 branch of `ClaimOwnership` or `Start` ever move the unit to anyone but the recorded owner?
4. **`htlc.bas` window arithmetic.**
   - Check the boundaries (`h < t1`; `t1 ≤ h < t2`; `t2 ≤ h < t2 + g2`; `h ≥ t2 + g2`) against Lock's constraints:
     `t1 ≥ h + max(2, w1min)`, `t2 ≥ t1 + w2min`, `t2 ≤ h + 10^6`, and `w1min + w2min ≤ 999,999`.
   - Three platform facts apply: execution up to 10 blocks after the build (§2, item 11), an 8-block reorganisation
     that can move an exit across an edge (§2, item 12), and an estimator that runs at height 0 (§2, item 10).
   - Is there a height at which no exit is open, or two are? Is there an exit that a correctly behaving party can
     build but never land?
   - `TestHtlcV3WindowMatrix` is the claim to check.
5. **Rounding in pool and launch.**
   - `bps` / `bpsup` exactness for `f ≤ 10000` (`pool:291-302`).
   - `muldiv` / `muldivup` / `divlu` against Hacker's Delight (`pool:314-408`; `reference/uint128.go`).
   - AddLiquidity's `need = ⌈L·r/lp⌉ ≤` the other side (`contracts/README.md`, pool).
   - The graduation re-pricing `used = ⌈cost·10⁴/(10⁴ − fee − tax)⌉ + 1`, then re-taxed and checked
     `deff ≥ cost` (`launch:50-57`). Can it overcharge a buyer, or refuse a legitimate graduating buy?
   - Does every rounding favour the contract?
   - In `founders.bas`, is `covered = ⌈inc·hst/2^32⌉ ≤ und + x` always, so that `und` (`founders:243-258`) cannot
     wrap?
6. **Own-asset supply** (§2, item 7). Can any path emit more of a contract's own asset than its arithmetic bounds?
   That covers the LP mint and burn, launch token sends, `Stake`/`Unstake`, founders units (`Register`, `Withdraw`,
   `List`, `Unlist`, `Buy`), nft editions, and the nfa unit.
7. **`board.bas` and `swap.bas` together.**
   - Nothing on chain ties an accepted offer to the `swap.bas` lock that follows.
   - Can a maker, taker or third party exploit the gap between the row (`h_`, `t_`, `cl_`, `ad_`) and the lock?
     For example: a lock with another taker, hash, amount or asset; a lock after `ad_`; two rows under one hash.
   - Which checks must a companion make that the contracts cannot?
   - `refundPkh` is checked for length only (`board:80`).
   - Is there a better on-chain answer to GAP-026 than the three options in MT-J18?
8. **`founders.bas` FEE-01 / FEE-02.** Which of the options (a)–(d) (`contracts/README.md`, founders) would you
   accept, and what would you require of its implementation?
9. **`nfa.bas`'s use of `BLOCK_TIMESTAMP()`.**
   - It sets the listing end, the anti-snipe extension and the 300-second cancel buffer (`nfa:314-323,262-263,298`).
   - What can a miner's timestamp freedom on DERO do to a listing or an auction?
10. **Factory registration.**
    - A treasury that is not a registered account makes every paid registration's single transfer fail, so the
      registration reverts. Is that the only way `Initialize`'s arguments can brick the factory?
    - Is forwarding the whole `DEROVALUE()` when it exceeds `regfee` (`factory:74-75`) what a registrant expects?
11. **Anything the platform rules in §2 do not cover.** A derohe behaviour that a contract here depends on and that
    this package does not list.

---

## 7. What is out of scope, and where it is covered

- **The Monero cross-curve proof** (`research/crosschain/poc/proof-xmr`): `docs/audit/PROOF_XMR_AUDIT_PACKAGE.md`
  (MT-G03). It is the other external review. This review may take the proof's guarantee as given, including its known
  gap L1: the circuit does not require the hashed value to be below ℓ. `htlc.bas` checks only the hash and the length
  of a revealed secret (`htlc:107-113,139-142,199-202`), so it accepts an unreduced preimage. The companion reduces it
  (`tools/xmrswap/keys.go` `RecoverShare`, MT-B25).
- **The companions** `tools/xmrswap` and `tools/zecswap`: the protocols are `docs/XMR_SWAP.md`, `docs/ZEC_SWAP.md` and
  `docs/CROSS_CHAIN_TRUST_MODEL.md`, and their state is in `docs/MAINNET_TODO.md` §B–§C. They are cited here only
  where a contract's safety depends on them: the checks the contracts cannot make (§3.3; HTLC-R2).
- **The app** (`app/`) and its TELA INDEX and DOCs:
  - the TELA contracts are upstream templates, checked by `tools/verifydeploy`'s `tela` step;
  - the app's findings are in `docs/audit/FINDINGS_2026-09-10.md` (UX, FE, APP) and `docs/MAINNET_GAP_ANALYSIS.md`
    (GAP-001, GAP-032);
  - the app matters to this review in one way: it lists a market only if its code hash is a shipped template.
- **`vault.bas`**: in the tree and the suite, but not deployed (§3.11).
- **derohe itself**: the platform, at the pinned commit. Findings about the DVM are welcome, but it is not the target.
- **The deployment tooling** (`simdeploy`, `verifydeploy`, `release`, `telainstall`): only as far as it defines what
  is installed and hashed (§1).

**Threat model and trust assumptions** for everything above: `docs/SECURITY_THREAT_MODEL.md` §2 (adversaries
A1–A10) and §4, and `docs/CROSS_CHAIN_TRUST_MODEL.md` (T1–T10). For the contracts alone, the parties someone must
rely on are these:
- DERO consensus to 8 blocks, and miners who may delay a transaction but not beyond a window's margin;
- each swap party's presence in its own window (the maker in `[t1, t2)`, and HTLC-R1);
- the treasury key, which receives fees and decides whether the Founders Pass is paid at all;
- each market's creator, as §3 lists (NFA `Update`, launch `ClaimTax`, the allocations made at install);
- Civilware, as the artificer payee;
- the companion software, and the proof in the other package.

No contract here has an owner, admin, pause or upgrade path.
