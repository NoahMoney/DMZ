# DMZ: the road to mainnet, every item

*Opened 2026-09-11, at the operator's instruction to write the full list and not stop until DMZ is ready for mainnet.
This is the working checklist for the final mainnet pass. Every item has an owner, a status and a statement of what
"done" means.*
- *`MAINNET_SECURITY_GATE.md` gives the verdict.*
- *`docs/BACKLOG.md` keeps the older numbered backlog (BL-…), referenced here where an item continues one.*

**Columns**
- **B**: blocks mainnet. **Nothing is mainnet-ready while any B item is open.**
- **Owner**:
  - `eng`: engineering work done in this repository;
  - `op`: a decision or action only the operator can take;
  - `ext`: needs a party outside the project.
- **Status**:
  - `done`: finished, verified in the working tree, not yet committed;
  - `wip`: being built now;
  - `open`: not started;
  - `decide`: waiting on the operator;
  - `blocked`: waits on another item.

---

## Phase W: everything that can be done while the public test chains sync

*Opened 2026-09-11 at the operator's instruction: "complete everything that can be done in a massive phase while we
wait for the chains". The Monero stagenet and Zcash testnet nodes are syncing (MT-F03, MT-F04). This phase is every
remaining item that waits on neither the syncs, the external audits nor the operator's decisions.*

| ID | Item | Owner | Status |
|---|---|---|---|
| W-01 | **DERO private testnet**, with the current contracts, the swap contracts and the app on a ring-16 INDEX; verified by `verifydeploy`, the factory renounced, exercised with trades and swaps. It starts the 7-day clock of MT-F05. The public DERO testnet seed is unreachable, so it has to be the private testnet | testnet agent | wip |
| W-02 | **Client-side Zcash header verification** (MT-C14): Equihash (200, 9), the header chain, target and difficulty checks, Merkle inclusion, in a new package `tools/zecspv` that zecswap can call. With it a lying node needs real Zcash work to fake a funding | header-verification agent | **done** 2026-09-11, reviewed: `tools/zecspv` and `docs/ZCASH_HEADER_VERIFICATION.md`. The difficulty rule is exact: it reproduces zcashd's `pow_tests` and 106 real headers recomputed across Blossom and NU6.3. Vectors are zcashd's and librustzcash's Equihash sets and 20 real Merkle roots. About 0.3 ms per header. Finding: exact difficulty is not enough without an anchor or 100 headers below the funding. With them, forging 10 confirmations costs about 8.9 blocks of network work, about 14 ZEC of mining, so 10 confirmations do not protect a large swap. Wiring it into zecswap is W-09 |
| W-03 | **The app:** the Zcash swap screen (MT-D05); the age of cached figures on every screen (MT-D07, BL-129); copy saying the swaps have run on real node software (MT-D08); the treasury carried in the bundle (GAP-005); start time measured (MT-M02) | app agent | **done** 2026-09-11, reviewed. Found while doing it: market names cut to "B.." in the Markets table at 1024 px on the mainnet-shaped render (MT-D12); the dead state of an accepted, never-locked offer is coded but not yet seen on screen |
| W-04 | **The Monero proof's audit package** (preparing MT-G03): the statement, circuit, transcript and soundness argument, with vectors; property tests of the SHA-256 gadget; rejection of malformed and mismatched proofs; replay across swaps; a reproducible prover build checked against xmrswap's pin (the rest of GAP-013); timings on the release build (MT-M03); whether a second verifier is feasible (MT-G04) | proof agent | wip |
| W-05 | **Supply chain and measurements:** telainstall built from locked modules (GAP-017); derohe's vendored dependencies checked against the Go vulnerability database (GAP-018); Playwright pinned (MT-H03); one-command validation `tools/check.sh`; the bundle's install cost measured (MT-E07); a latency table from the regtest logs (MT-M01) | supply agent | **done** 2026-09-11 except MT-E07, reviewed. GAP-017: telainstall builds `-mod=readonly` after `verifyvendor -modules-only`; the fork is pinned by version, commit and go.sum hash in `vendor.lock` `modules`. GAP-018: `docs/DEPENDENCIES.md` covers 52 modules, each version recovered from the vendored source, with 8 findings D-1…D-8. Playwright pinned at 1.62.0 (MT-H03). `tools/check.sh`. `research/regtest/latency.md` (MT-M01). MT-E07 waits for the simulator |
| W-06 | go1.26.8 for every build; `x/crypto` and `x/sys` to latest; `vendor/bin` rebuilt and re-pinned (MT-G10, MT-G07) | coordinator | waits for W-08 and W-09 to release the simulator |
| W-07 | `rpcgate` restarted with the hardened build | coordinator | **done** 2026-09-11. The gate runs the hardened build (sha256 `9b89b5ba…`), verified against the live testnet node: a body naming `stop` then `getblockhash` came back as `getblockhash 0`, `stop` alone was refused with -32601, a batch with -32600, no cookie with 401. The gate now has a `wsl.exe` host of its own. `zcash-testnet-up.sh --restart-gate` swaps in a new build without stopping zebrad, and the status report warns when the gate running is not the current build. That is how the old build ran unnoticed for 30 minutes. As for the "stale simulator htlc lock": the recorded simulator `htlc.bas` (`b01a9e7c…`) holds nothing (`n = 0`, balance 0), so no lock was left to close |
| W-08 | **Monero companion.** Now: the reorganisation (MT-B11) and the command-line run. Then:<br>• a resumable sweep (MT-B12)<br>• the scan time measured (MT-B13)<br>• the recovery card and wake (MT-B15)<br>• contract ids taken from the record (MT-A18)<br>• RPC-parser fuzzing (MT-G06)<br>• no secret in output (MT-L02)<br>• an authenticated, encrypted rendezvous (GAP-019) | Monero agent | wip |
| W-09 | **Zcash companion.** Now: the reorganisation, the command-line run and the status fix. Then:<br>• contract ids from the record (MT-A18)<br>• RPC-parser fuzzing (MT-G06)<br>• no secret in output (MT-L02)<br>• W-02 wired into the watcher | Zcash agent | wip |
| W-10 | The docs, the gate, the testing record and the README updated as each item lands; each piece committed and pushed | coordinator | continuous |

**Not in this phase:** the stagenet and testnet swaps (they wait on the syncs), the external audits, the operator
decisions (§J) and the seven days of DERO testnet time.

## A. Contracts (DVM-BASIC, `contracts/`)

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-A01 | B | `htlc.bas` v2: anyone may send Punish from `t2`, and it pays only `t_<id>`. Closes LIV-07: no lock is frozen when its taker never punishes | eng | wip | DVM tests: a stranger's Punish pays the taker; it is refused before `t2` and a second time; the old liveness pin is inverted |
| MT-A02 | B | `htlc.bas` v2: Lock refuses `ha`/`hk`/`hb` that are not 64 lower-case hex characters (U4, the contract half; attack D) | eng | wip | a DVM test refusing upper case and non-hex, as `swap.bas:41` and `board.bas:45` already do |
| MT-A03 | | `htlc.bas`: minimum widths for `t1 − lock` and `t2 − t1` in the contract, not only in companion policy | eng/op | decide | a decision recorded; if adopted, a DVM test |
| MT-A04 | B | `swap.bas` v2: a minimum escrow width (Z-D). Lock refuses an expiry below lock height + `W_min` | eng | done | `Initialize(wmin Uint64)`. A missing argument, 0, or more than 1,000,000 fail closed (the DVM refuses a call with a missing parameter, `vendor/derohe/dvm/sc.go:232-234`). `simdeploy -swap-wmin`, default 1,600. Also: every lock names its taker (GAP-024), and the exits refuse stray assets (GAP-027). Full DVM suite green, 168 tests. Live: a lock narrower than `wmin` refused on chain (tx `2f9a1fc7…`) |
| MT-A05 | | `board.bas` v3: the acceptance deadline `ad_<id>`, so one Accept freezes an offer for 200 blocks at most (ADV-PRICE-07) | eng | done | DVM tests; the final live run cancelled at the deadline, height 688, against an expiry of 744 |
| MT-A06 | B | `nfa.bas`: a seller's payout can no longer be destroyed by an unregistered payee (LIV-01/DVM-1; `PayOwed`); an ended auction's bid always settles (NFA-1/LIV-02); a refused Start leaves no charity share behind (DVM-2) | eng | done | reviewed; DVM tests pass; the template is re-hashed and the defective hash revoked (`simdeploy -revoke`) |
| MT-A07 | B | `factory.bas`: `Renounce` ends ownership for good; transfer of ownership removed (SEC-K-02, LIV-08) | eng | done | reviewed; `TestFactoryRenounce` calls every setter after the renounce, as the old owner, a stranger and an anonymous caller, and each is refused |
| MT-A08 | B | `founders.bas` FEE-01/FEE-02: the floor and the deposit-with-no-holders problem | op then eng | decide | the operator picks option (a), (b), (c) or (d) (`contracts/README.md`); the choice is built and tested |
| MT-A09 | | `nfa.bas` NFA-4: value attached to ClaimOwnership or Update is kept for ever. Refuse it | eng | done | every NFA entrypoint refuses value it does not take (`TestNFAStrayValueIsRefused`, both quotes). One exception is stated: ClaimOwnership above ring 2 returns the unit and cannot refund anything else. GAP-027 for `social.bas` and `nft.bas` too (`tests/zzstray_test.go`); the DVM cannot list the assets a call carries, so an asset a contract cannot name is still kept, and a test pins that |
| MT-A10 | | DVM-3: a static test that no settlement names an unregistered-capable payee before the last transfer | eng | done | `tests/sendorder_test.go` traces signer-derived addresses through variables, arguments and stores. Every contract passes; it flags HEAD's `nfa.bas`; one allow-list entry (founders PayBatch, whose holders all signed Register) |
| MT-A11 | | `tests/parse_test.go` entrypoints (`Renounce`, `PayOwed`, no transfer of ownership) | eng | done | `TestContractsParse` passes |
| MT-A12 | | `TestTheMainnetRunbookDoesNotOfferTheFileBackend`: re-anchored on the runbook's current text | eng | done | passes |
| MT-A13 | B | A conservation invariant checked after every call, for every contract that holds value: `pool`, `launch`, `nft`, `founders`, `htlc`, `swap` (`nfa` done) | eng | done | `tests/zzconservation_test.go`: seeded random sequences with accepted and refused calls (pool, launch in three tax modes, founders, nft, swap on its final `wmin` and required taker, board), plus own-token supply (GAP-022). `tests/zzconservation_htlc_test.go` (added in this pass): for `htlc.bas`, the contract holds exactly its open locks per asset after each of 119 accepted and 206 refused calls; 90 locks, 29 exited by Claim, Refund or Punish; stray DERO or the lock's own asset attached to an exit never stayed |
| MT-A14 | B | A table of every privileged path, line by line, with who, what, and whether it can steal, mint, freeze, change code or change destinations | eng | wip | `MAINNET_GAP_ANALYSIS.md` §1, checked against `tools/verifydeploy`'s role table |
| MT-A15 | | Installed storage gas recorded for every contract (all under 20,000) | eng | wip | the canon test's figures copied into `MAINNET_DEPLOYMENT.md` |
| MT-A16 | | Rerun the extreme and boundary suites (`zzextreme`, `zzdvmarith`, `uint128`) against the final sources | eng | done | the full DVM suite on the final contracts: 168 test runs, 0 failures, `ok … 72.052s` (`.sim/dvm-final-0911.log`) |
| MT-D11 | B | The UI passes on the final contracts, after the last `simdeploy` | eng | done | normal pass `UI TEST OK`, no horizontal overflow at 390, 768 or 1024. Mainnet-shaped pass `UI TEST OK (mainnet-shaped)`: no request leaves the device; XMR and ZEC stay reserved names with no vault (`.sim/ui-final-0911`, `.sim/ui-final-mainnet-0911`) |
| MT-A17 | B | An external review of `contracts/*.bas` | ext | open | the reviewer's report in `docs/audit/` |
| MT-A18 | B | Record the swap contracts per network: `htlc`, `swap` and `board` keys in `deploy/networks.json`, so the app, the companions and `verifydeploy` read one record (GAP-002) | eng | wip | done: `netcfg.Network` has the three fields, `config.js` and the bundle manifest carry them, `simdeploy` installs or reuses them, `tools/netcfg/swapcontracts_test.go`. Open: the companions default to the record; the mainnet runbook installs them |
| MT-A19 | | Choose the factory's final shape: setters kept for the deployment window and then Renounce (built), or treasury and regfee fixed at install with no owner | op | decide | the choice recorded in `MAINNET_DEPLOYMENT.md` |
| MT-A20 | | ART-NFA-MS1 divergence (`PayOwed`, an ended-auction BuyItNow, the charity check, stray value), stated wherever the docs said "kept exactly" | eng | done | `docs/NFA_BUILD.md` "kept exactly" lists the four changes; invariant 1 restated; `docs/NFA.md` §2.5 |

## B. The Monero companion (`tools/xmrswap`)

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-B00 | | **Status of section B, 2026-09-11, from the Monero agent's final report:**
- **All three exits ran twice** against real monerod and monero-wallet-rpc 0.18.5.1 (regtest), on `htlc.bas` v2 and the DERO simulator. The contract held 0 DERO after every exit, and the balance changes match the wallets' own records (`research/regtest/xmrswap-e2e.log`).
- **Node pinning.** Run 2 exercised `set_daemon` 55 times, with none refused. It named the same monerod the wallet-rpcs already used, so it shows the call is accepted, not that it would move a wallet-rpc off a different one.
- **Done:** the real-server fixes (B20–B24); bounded replies (B07); replay tests (B08); crash fuzz ×3 (B18); GAP-003 (absolute, pinned `-prover`); GAP-007 (no network defaults, mainnet only when both agree); GAP-008/009 (logins by environment, required on mainnet); GAP-010 (`-monerod`, loopback on mainnet); GAP-020 in part (owner-only ACL, encrypted joint wallets); the lower-case hashes and stray-asset refusal in `htlc.bas`.
- **Declined, with a reason:**
  - encrypting the state file would block unattended restarts;
  - moving FakeWallet would create an import cycle, and a test guards that the companion never uses it (GAP-037).
- **Open:** B09, B11, B12, B13, B15, B16, B17, B19; the asset leg run with a real token | eng | wip | — |
| MT-B01 | B | U2: the gate deadline G. Alice ignores a gate secret that arrives after `t1 − G`; Bob's margin is at least G (raises attack A's cost) | eng | wip | unit tests on both sides; G from the mainnet profile |
| MT-B02 | B | U3 / XMR-06: re-sends respect the claim margin; Alice finishes only once her Claim is 8 deep | eng | wip | tests with a fake chain that holds a Claim in the pool |
| MT-B03 | B | U4: pin the contract's code (read the SCID's code once and compare it with canonical `htlc.bas`); lower-case hashes | eng | wip | a test where a look-alike contract is refused |
| MT-B04 | | U1: the asset leg (lock a token or NFA unit, not only DERO) | eng | wip | three exits with an asset leg on the simulator |
| MT-B05 | | The gas stipend from the maker (Path 0, decision 3) | op then eng | wip/decide | a stipend to a freshly registered empty wallet that then Claims |
| MT-B06 | | U6: the exit counts as done at the stable Claim | eng | wip | status text and tests |
| MT-B07 | B | Every wallet-rpc and daemon response read through a size cap and a timeout | eng | wip | a test where a hostile response is refused deterministically |
| MT-B08 | B | Replay and duplicates: a used secret, a lock already seen, and the same payment offered for two swaps each fail closed | eng | wip | named tests |
| MT-B09 | B | Reads of the DERO lock confirmed by every configured DERO node before Alice locks XMR (the data-source attack, `CROSS_CHAIN_TRUST_MODEL.md` §5) | eng | open | a test where one lying node of two blocks the XMR lock |
| MT-B10 | B | Run all three exits against **real** `monerod` and `monero-wallet-rpc` (regtest, 0.18.5.1), with the DERO simulator | eng | done | twice; every txid in `research/regtest/xmrswap-e2e.log`; 0 DERO left in the contract after each exit |
| MT-B11 | B | A Monero reorganisation on regtest (`pop_blocks`, then mine a different branch) across the gate, and what each side does | eng | blocked on MT-F01 | a recorded run and the outcome matching `XMR_SWAP.md` R3 |
| MT-B12 | B | XMR-R1: killing Bob's companion during its sweep is terminal. Make it resumable | eng | open | the crash fuzz covers the sweep and resumes it |
| MT-B13 | B | XMR-2: measure the joint wallet's restore and scan time on real `monero-wallet-rpc` | eng | blocked on MT-F01 | the measured figure in `XMR_SWAP.md` |
| MT-B14 | B | R6: refuse a Monero lock carrying a far-future `unlock_time` | eng | open | a test with a crafted transfer on regtest |
| MT-B15 | | U9: wake scheduling and a recovery card | eng | open | the card is printed before the XMR lock; a wake is registered on each OS |
| MT-B16 | B | The mainnet profile (BL-27): T2 − T1, T1Max, confirmation tiers, G, the stipend floor | op then eng | decide | one profile file, checked by a test at every tier |
| MT-B17 | | Readiness for FCMP++/Carrot: rerun the suite against the v0.19 stressnet build when its mainnet height is set | eng | open | a recorded run; the refusal path for an unknown version |
| MT-B18 | B | `TestCrashFuzz` green after every change above | eng | wip | the full `go test ./xmrswap` |
| MT-B19 | B | Monero stagenet run of all three exits (BL-11) | eng | open | txids in `tools/xmrswap/testdata` |

## C. The Zcash companion (`tools/zecswap`)

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-C00 | | **Status of section C, 2026-09-11:**
- **Built and green against fakes:** C01–C12, 89 tests and fuzz targets, `TestCrashFuzz` ×3 (132 runs each).
- **In `tools/zecswap`:** the node client, watcher, ZIP 317 fee, builders, idempotent broadcast, the pre-signed refund at three fees, the state machines, the DERO client, reads from several sources that must all agree, the code pins (`pin.go`, recomputed from the tree by a test), and `wmin` checks. The maker sends the taker's DERO Claim for her.
- **GAP rows closed:** GAP-008, GAP-011, GAP-021, GAP-028, GAP-029; GAP-039 by refusing a Sapling-bearing funding.
- **On real zebrad 6.3.0 (regtest), with the DERO simulator:** three swaps ran as intended (`research/regtest/zecswap-e2e.log`):
  - a v5 claim, whose maker's companion sent the away taker's DERO Claim;
  - a v6 claim, read by the taker's own watcher;
  - a refund, broadcast by a third party that held only the pre-signed file. Zebra refused it before `cltv` and mined it after.
- **Tests:** green again; crash fuzz ×3 at 132 runs each.
- **Open:** C14 (header verification), C15 (Zcash testnet), and the command line itself run against a node | eng | wip | — |
| MT-C01 | B | Node client (Zebra JSON-RPC); the branch id read from `consensus.nextblock` | eng | done (fakes) | 30 s timeout, 32 MiB cap, shape checks; a real Zebra run is MT-C13 |
| MT-C02 | B | Watcher for the P2SH funding and for the counterparty's claim, extracting `s` | eng | wip | tests including a reorganised-away funding |
| MT-C03 | B | The ZIP 317 fee | eng | wip | tests against ZIP 317's formula |
| MT-C04 | B | Broadcast and idempotent rebroadcast | eng | wip | tests |
| MT-C05 | B | DERO client (read the board and the escrow, send Claim) | eng | wip | tests against the simulator |
| MT-C06 | B | Z-B: the taker's pre-signed refund at several fee levels, `nExpiryHeight = 0`, refused when an upgrade activates before `T_dero` | eng | wip | tests; one hex broadcast from another machine on regtest |
| MT-C07 | B | The maker's claim cutoff R (the claim near `cltv` is a race) | eng | wip | test |
| MT-C08 | B | The design note's rules in code: `T_dero − now ≥ 1,200`; the lock at least 8 deep; the escrow's asset and amount equal the board's; P_lock | eng | wip | tests |
| MT-C09 | B | Explicit maker and taker state machines, durable state before every irreversible step, crash fuzz | eng | wip | crash fuzz green |
| MT-C10 | B | Reads from several sources: the maker's funding check before revealing `s`, and the taker's lock check before funding, confirmed by every configured endpoint | eng | wip | tests: one lying endpoint of two blocks each step |
| MT-C11 | B | Bounded reads, and replay identity by funding outpoint | eng | wip | named tests |
| MT-C12 | B | A `cmd/zecswap` command line | eng | wip | builds for every release target |
| MT-C13 | B | A full swap on **real** Zebra regtest with the DERO simulator: fund, the maker's claim, the taker's DERO Claim sent by a third address; a refund; a late claim racing a refund | eng | blocked on MT-F01 | txids and logs in `research/regtest/` |
| MT-C14 | | Client-side header verification: Equihash and the Merkle path before `s` is revealed | eng | wip | the package and its cost model are done (`tools/zecspv`, `ZCASH_HEADER_VERIFICATION.md`, W-02); open: wiring it into zecswap behind a flag, then on by default after the Zcash testnet run (W-09) |
| MT-C15 | B | Zcash testnet run (BL-42) | eng | open | txids recorded |

## D. The app (`app/`, TELA)

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-D01 | B | Wrap is inert on any network with no vault; the reserved names XMR and ZEC stay protected | eng | done | mainnet-shaped UI pass green at 390 and 1024 |
| MT-D02 | B | The network gate: a build refuses to sign when its network differs from the node's or the wallet's | eng | done | the mainnet-shaped pass shows the banner and the refusal |
| MT-D03 | B | No request leaves the device except to the user's node, the local wallet and the app's own files | eng | done | the UI pass records "requests that left this device: none" |
| MT-D04 | B | NFA screen: waiting shares with a "pay out" action (`PayOwed`); remove the false line about returning the unit; say that BuyItNow settles an ended auction | eng | done | `02c-nfa-owed.png` shows the treasury's owed share with its Pay out button; `ui.mjs` checks it. The notice reads the factory's `owner` or `renounced` state from chain |
| MT-D05 | | The Zcash swap screen (`#zswap`, `ZEC_SWAP.md` §8), if not yet built | eng | done | `app/zswap.js`: the board read from `board.bas` v3 and `swap.bas` v2 (kind and version checked); Post and Accept with the §1 privacy statement above the fields, checked by position; addresses checksum-verified, shielded, script and wrong-network ones refused; a generated secret kept in page memory only; `#zswap/<id>` with the DERO leg live and "Claim for the taker", which refuses a secret whose SHA-256 is not the lock's. Proved on the simulator from a wallet on neither side: the taker received exactly the lock (382,438 → 582,438). UI passes `.sim/ui-w03b`, `ui-w03m`; PNGs read |
| MT-D06 | B | The normal simulator UI pass on the redeployed chain, with the wallet flows (the signing gate, and the outcome checks on every button) | eng | done | `UI TEST OK` in `.sim/ui-wallet-2`, every PNG read. It found and fixed one real defect: a successful factory registration was reported as "did not execute", because the check compared the registry's hex value with the raw scid (`app/forms.js`). The private buy went out at ring 16 with no signer extracted. The mainnet-shaped run is also green. To repeat on the final contracts after the last `simdeploy` |
| MT-D07 | | BL-129: the age of cached figures on every screen | eng | done | a `#stale` banner on every screen when the node stops answering, with the age of the figures shown; Portfolio, Home and the ZEC swap screen say when they read. The pass makes the node refuse every read, checks the banner and its age, then checks it clears |
| MT-D08 | | The Wrap tab's line about swaps never having run on a real network, updated once MT-B10/MT-C13 pass | eng | done | `wrap.js` and `zswap.js` now say both programs completed every exit against real Monero and Zcash node software on regtest, and neither has run on a public test network. It changes again after a public-testnet swap (`MAINNET_TEST_PLAN.md` §7) |
| MT-D12 | | Market names cut to "B.." in the Markets table at 1024 px on the mainnet-shaped render (seen during W-03) | eng | open | names readable at 1024 px in both passes |
| MT-D09 | B | Every app file under the TELA DOC cap after all changes | eng | wip | `genconfig -dist` passes |
| MT-D10 | | `treasurybot`: walk NFA listings and call `PayOwed` for the treasury (NFA-3) | eng | done | `tools/treasurybot/payowed.go`: only for NFAs whose code hash matches the template, proven by the `op_` counter, dry run by default, `-min-owed` floor; 5 unit tests. Live: NFA `82f93987…` `owed` 500 → 0, `op_` → 500 |

## E. Deployment and verification tooling

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-E01 | B | `tools/verifydeploy`: installed code equals `Canon(source)`; market templates known; privileged roles gone; the TELA INDEX and DOCs equal the bundle | eng | done | read-only: `DERO.GetInfo` and `DERO.GetSC` only. A role table with line citations, pinned to the code by tests. `research/release/verifydeploy-sim.log`: it fails before the ceremony, passes after Seal and Renounce, and fails on a one-character change, naming `social.bas:8`.
- **A blind spot, found and fixed in this pass:** it checked the swap contracts only when they were passed with `-extra`. Now `recordContracts` reads `htlc`, `swap` and `board` from the record, and the bundle manifest must name them too. `TestRecordContractsCoverTheRecord` holds the list to every contract field of `netcfg.Network` by reflection.
- **The final simulator record** (`research/release/verifydeploy-final-sim.log`): 31 PASS, 13 WARN, 1 ALLOWED (the simulator factory's owner, kept on purpose), no failure. `htlc`, `swap` and `board` pass, and swap and board match the hashes the companion pins.
- **Eleven of the WARNs** are legacy template hashes from before publication, which no committed source produces. The mainnet record starts with none (MT-H07) |
| MT-H07 | | The simulator record accepts eleven legacy template hashes that no committed version of the source produces: they come from before the repository was published. The mainnet record's `templates` start empty, so a mainnet bundle carries only hashes `verifydeploy` can trace to this repository | eng | done for mainnet | keep `mainnet.templates` free of untraceable legacy entries; `verifydeploy` WARNs on any |
| MT-E02 | B | `tools/release`: reproducible companion builds and a manifest of hashes | eng | done | xmrswap and zecswap for five platforms. Two build paths gave byte-identical manifests, and `-verify` from a third path matched all 68 hashes (`research/release/reproducible-build.log`) |
| MT-E03 | B | The TELA INDEX's final state: installed so that nobody can update it, or its update key's fate written down and checked | eng/op | done | `telainstall -ringsize`: 16 by default on mainnet; below that is refused without `-updatable`, and `-index` is refused above ring 2. Proven on the simulator (`research/release/tela-anon-index.log`): owner `"anon"`, commit 0, hash equal to its own SCID; an `UpdateCode` sent from the installer was mined and changed nothing. The runbook's update and rollback text is rewritten to match |
| MT-E10 | | GAP-032: `telainstall` takes only a `dist/<network>` bundle whose manifest and `config.js` name that network, and whose files match the manifest. dURL settled as `dmz.tela` for mainnet and the simulator; the testnet record keeps `veil.tela`, which is what was installed there. Also fixed: `telainstall` hashed the whole DOC contract instead of its content block, so every install stopped at its first DOC | eng | done | nine refusals in the log, each exiting 2 |
| MT-E04 | B | `simdeploy -network mainnet` installs `htlc`, `swap` and `board` and records them, and refuses to install a vault | eng | wip | installs and records them on any network, and never installs a vault. `swap.bas` gets its minimum width from `-swap-wmin` (default 1,600), checked against the contract's own bounds before anything is installed (`tools/simdeploy/swapwmin_test.go`). The install path ran on the simulator (htlc `ae5044c0…`, swap `92d3e44b…`, board `fc33535d…`) before `wmin` existed. Open: the final run on the final contracts |
| MT-E09 | B | `simdeploy` refuses a Founders Pass that `founders.bas` would refuse, before installing anything. The default description was 258 bytes against a 240-byte limit, so every founders install from commit 59a5851 onwards failed after the factory and social contracts were paid for; the mainnet runbook's was 268 | eng | done | `checkPassHeaders`; `tools/simdeploy/passheaders_test.go` ties the limits to `founders.bas:22-24` and checks the runbook's own `-pass-descr`; redeployed on the simulator |
| MT-E05 | B | The revocation ceremony as a script: SetTreasury and SetRegFee, then Renounce, then `verifydeploy` confirming no owner remains | eng | done | `simdeploy -renounce -factory <scid>` calls Renounce from the owner wallet at ring 2, then reads back that `owner` is absent and `renounced` set, and exits 1 otherwise. Live on the simulator (`research/release/simdeploy-renounce-sim.log`): a stranger's call was refused at the estimate (nothing sent, owner still present); the owner's call ended ownership at height 552; a second run was a no-op. `verifydeploy` checks the same state (MT-E01). `MAINNET_DEPLOYMENT.md` §5 step 8 uses it |
| MT-E06 | B | `verifyvendor` green; `binaries.lock` re-pinned from a clean tree | eng | done | re-pinned from the committed tree in `50d4e78`, with provenance checked: `telainstall.exe` was rebuilt from the committed source, and no pinned binary imports `tools/simrpc`. `verifyvendor`: "vendor tree matches deploy/vendor.lock". The go1.26.6 rebuild (GAP-015) will re-pin again |
| MT-E07 | | The install cost of the current bundle measured on the simulator (the STALE table in `deploy/README.md`) | eng | open | a new measured table |
| MT-E08 | | Base set redeployed on the fresh simulator, then `simvault -install` and `simwrapmkt` | eng | wip | the record updated; the UI pass green |

## F. Real networks

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-F01 | B | A real regtest harness: `monerod` and `monero-wallet-rpc` 0.18.5.1, Zebra with every upgrade through NU6.3; pinned and signature-checked | eng | done | `tools/regtest/`:
- **Verification:** Monero's `hashes.txt` carries binaryFate's PGP signature and the tarball matches it; Zebra 6.3.0's `SHA256SUMS` sigstore bundle was verified against the Zcash Foundation's release workflow.
- **Smoke tests:** both pass (`research/regtest/`). Every RPC method each companion calls exists on the real node.
- **Cross-check:** zecswap's txids and digests agree with an independent librustzcash build.
- **Runs in WSL2:** Windows Defender quarantines the Monero binaries on C: although they match the signed hashes. No exclusion was added.
- **Tracked logs are clean.** The smoke scripts write the repository root as `<repo>` and redact keys, WIFs and non-empty wallet passwords. The two existing logs were cleaned in place and diffed byte for byte against the originals, and a final grep found no path, key, password or cookie |
| MT-C16 | B | **CRITICAL, found on real Zebra.** v6 transactions are live under NU6.3 (ZIP 229), and current Zcash wallets build them by default. zecswap parses only v1–v5 and skips the rest, so a maker's claim built by a current wallet is invisible to the taker's watcher: she never learns `s`, the maker refunds the DERO, and he keeps both legs | eng | done | zecswap builds, signs and reads v6 (ZIP 229), and reads versions 1–6. The spend scan stops on anything at the address it cannot read. `TestRealV6TransactionsFromZebra`, `TestRealV6ClaimFromZebra`, `TestFindSpendFailsClosedOnAnUnreadableTransaction`. On real Zebra, the v6 claim `54f6e152…` was mined at 514 and the taker's watcher read the secret from it |
| MT-C17 | B | Zebra reports `"chain":"test"` on regtest, and zecswap refuses it as testnet. Add real regtest support, unreachable on mainnet | eng | done | a node counts as regtest only if block 0 is regtest's genesis (`029f11d8…e327`), and only `-network regtest` reaches it (`TestRegtestIsRecognisedByItsGenesis`, `TestCheckNetworksFailsClosed`) |
| MT-B20 | B | **Found on the real wallet-rpc.** `generate_from_keys` does not check the keys against the address, so xmrswap's echo check proves nothing. Derive the address locally and compare it before trusting any wallet made from keys | eng | done | the client derives v·G and s·G and compares them first (`TestTheClientRefusesKeysThatDoNotDeriveTheAddress`) |
| MT-B21 | B | A view-only wallet's `transfer` or `sweep_all` returns a hash and an `unsigned_txset`, and broadcasts nothing; the fake refused. Treat it as a failure, and confirm every send from the chain side | eng | done | `TestAViewOnlySendIsNotASend`, `TestAHashTheWalletDoesNotListIsNotASend` |
| MT-B22 | B | `set_daemon` lasts only while that wallet stays open, so the GAP-010 node pinning must be re-applied after every open or generate, or require the wallet-rpc's own `--daemon-address` to be loopback | eng | done | re-sent after every open and generate (`TestMonerodIsNamedAfterEveryOpen`). 55 calls accepted on the real server, naming the monerod the wallet-rpc already used |
| MT-B23 | B | `get_height` on the wallet-rpc is the wallet's scanned height, not the chain's. Find every use of it as chain height and fix it | eng | done | the client refreshes before reading it, and the meaning is documented |
| MT-B24 | | Make the fake wallet match the real responses (31 differences recorded), so the unit tests stop passing on a server that does not exist | eng | done | the fake returns -4 when nothing is unlocked, omits empty arrays, and treats a txset reply as unsent |
| MT-F02 | B | MT-B10 and MT-C13 run on it | eng | blocked | as above |
| MT-F03 | B | Monero stagenet: all three exits (MT-B19) | eng | wip | a pruned stagenet node is syncing (`tools/regtest/stagenet-up.sh`: monerod 0.18.5.1, outbound-only P2P, loopback RPC `127.0.0.1:38081`, no update or DNS checks). The database is ≤ 15 GiB. Started 2026-09-11 08:41Z. It ran about 170 blocks/s at first, then 10–20/s once blocks filled; it is CPU-bound, and the estimate is now **1–2 days** (`research/regtest/public-testnets.log`). Then: two wallet-rpcs, stagenet XMR, the three exits |
| MT-F04 | B | Zcash testnet: fund, claim, refund (MT-C15) | eng | wip | **zebrad 6.3.0 testnet syncing** since 2026-09-11 08:54Z (`tools/regtest/zcash-testnet-up.sh`):
- **Testnet only:** every start and status check requires testnet's genesis block.
- **No inbound P2P:** it listens on 127.0.0.1 only.
- **Gated RPC:** zebrad sits behind `tools/regtest/rpcgate`, which serves only zecswap's seven methods with the cookie.
- **Estimate:** 10 h to 2 days, limited by testnet peers. Then testnet ZEC and the three swaps.
- **Reviewed and hardened on 2026-09-11.** `rpcgate` now forwards a request rebuilt from the fields it checked, never the client's bytes. A duplicated `"method"` key can otherwise pass the check as one method and reach zebrad as another. `tools/regtest/rpcgate/main_test.go` covers the rebuild, the smuggling attempt, and every refusal. The hardened build has been running on the testnet node since 2026-09-11 (W-07); no client used the older one |
| MT-F05 | B | DERO testnet: the current contracts, the swap contracts and the app, live and exercised for at least a week | eng | open | **The public DERO testnet cannot be used from here:** its only seed in derohe's source (`212.8.242.60:40401`) refused TCP twice on 2026-09-11, while other outbound connections worked. The gate can be met on the private testnet procedure in `deploy/README.md`, a testnet in every rule but with no outside peers. The alternative is a public seed someone else runs |
| MT-F06 | B | The mainnet stage of `docs/MAINNET_TEST_PLAN.md` at the smallest amounts, reconciled four ways | op | blocked | the plan's reconciliation records |

## G. Security

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-G01 | B | `MAINNET_GAP_ANALYSIS.md`: a full sweep, classified | eng | wip | the file; every BLOCKER and CRITICAL item closed or carried here |
| MT-G02 | B | `SECURITY_THREAT_MODEL.md`, with the failure model (directive §21) | eng | open | the file |
| MT-G03 | B | An external audit of `research/crosschain/poc/proof-xmr` (U8) | ext/op | open | the auditor's report published |
| MT-G04 | | A second, independent verifier of the proof | eng | open | both verifiers agree on the pinned vectors |
| MT-G05 | B | A regression test for each attack in `CROSS_CHAIN_TRUST_MODEL.md` §5 (a data source colluding with the counterparty) | eng | wip | named tests in both companions |
| MT-G06 | | Fuzz every companion parser of RPC responses | eng | open | fuzz targets run for a fixed time with no crash |
| MT-G07 | B | `govulncheck` on every module; known vulnerabilities fixed or justified | eng | wip | Measured 2026-09-11 over the tools packages that load (simdeploy, netcfg, verifydeploy, release, treasurybot, vaultsigner, hygiene, xmrswap, zecswap).
- **Reachable (called):** 9 standard-library vulnerabilities under go1.26.2; **none under go1.26.8**.
- **Imported but never called:** 22 findings. 21 are in `golang.org/x/crypto` v0.33.0 (fixed up to v0.56.0), and one, GO-2026-5932, has no fix yet. One more is in `golang.org/x/sys` v0.30.0 (fixed in v0.44.0).
- **Next:** move both pins to their latest with the go1.26.8 switch (MT-G10), then rerun. GO-2026-5932 stays, justified, until upstream fixes it.
- **Not covered:** derohe's vendored code, which govulncheck cannot see (GAP-018) |
| MT-G10 | B | GAP-015: move every build off go1.26.2, whose standard library has 10 reachable vulnerabilities fixed in go1.26.3–1.26.6. **Target: go1.26.8**, the newest patch of the same line (go.dev lists go1.27.1 and go1.26.8 as current on 2026-09-11), so the change is a security fix and not a language upgrade | eng | wip | go1.26.8 installed beside the default and checked by `golang.org/dl`. Rebuild `vendor/bin` and the companions with it, rerun `govulncheck`, re-pin `binaries.lock`, and record the toolchain in `MAINNET_DEPLOYMENT.md` §2. This waits until the running regtest work finishes, so no build changes under it |
| MT-G08 | | Five Windows executables sit in the pushed git history (provenance, not a secret leak) | op | decide | rewrite history (destructive; the operator's call), or record them |
| MT-G09 | B | No secret anywhere: the tree, the history, the bundle, the logs | eng | wip | the gap analysis §3 |

## H. Supply chain and reproducibility

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-H01 | B | Every Go dependency pinned by sum; no new dependency without a written justification | eng | wip | `go.sum` reviewed; the gap analysis §5 |
| MT-H02 | B | Vendored trees (`derohe`, `tela`, the civilware fork) pinned by commit and verified | eng | wip | `verifyvendor` green. Down from 76 findings to 3: the stray probe copies of 2026-09-06 (`vendor/derohe/veilprobe/`, `veil2/`) were moved to `.sim/archive/vendor-probes-2026-09-06/`, not deleted. The 3 left are `telainstall.exe` and the `tools/telainstall` and `tools/simrpc` sources changed since pinning: rebuild and re-pin from a clean tree at the end. Provenance is checked, so re-pinning hides nothing:
- `tools/simrpc` has no uncommitted change; it changed in the committed `6ad2109` (2026-09-07, vault v3), after `binaries.lock` was last written in `60ab2e6` (2026-09-06). The lock is stale, not the tree.
- `tools/telainstall` is changing in this pass on purpose (GAP-001, `-ringsize`). The bootstrap now clones at the pinned commits (`deploy/README.md`, `README.md` §8; GAP-016) |
| MT-H06 | B | The patched `bulletproofs` 5.0.0 copy the Monero prover builds against, an ignored path dependency that nothing pinned (GAP-013) | eng | done | the one change is committed as `research/crosschain/poc/proof-xmr/patches/bulletproofs-5.0.0-r1cs-ctoption.patch`: three lines converting curve25519-dalek 4's `CtOption`, with no change of arithmetic. The crate's sha256 `012e2e5f…` matches crates.io's own index (not yanked). `fetch-bulletproofs.sh` rebuilds the copy and checks it (`-check` passed on 2026-09-11). `deploy/vendor.lock` `crates` pins the tree hash, and `verifyvendor` checks it offline |
| MT-H03 | | The Playwright version the UI pass uses, pinned and recorded | eng | done | Playwright 1.62.0 with Chromium 151.0.7922.34. `ui.mjs` prints both and refuses any other unless given `--any-playwright` (`tools/uitest/README.md`). A UI pass has run under the pin |
| MT-H04 | B | The regtest daemon binaries pinned by sha256 and a checked signature | eng | wip | `tools/regtest/PINS.md` |
| MT-H05 | B | The deployed artifacts checked against source by a party other than the deployer | ext | open | a second machine runs `verifydeploy` and `release -verify` |

## I. Documentation

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-I01 | B | `MAINNET_GAP_ANALYSIS.md` | eng | wip | |
| MT-I02 | B | `SECURITY_THREAT_MODEL.md` | eng | open | |
| MT-I03 | B | `CROSS_CHAIN_TRUST_MODEL.md` | eng | wip | |
| MT-I04 | B | `DECENTRALIZATION_ARCHITECTURE.md` | eng | done (draft) | reviewed against the final code |
| MT-I05 | B | `MAINNET_DEPLOYMENT.md`: build commands, versions, contract hashes, the sequence, the final ownership state, the verification commands | eng | open | |
| MT-I06 | B | `SECURITY_KEY_MANAGEMENT.md` | eng | open | |
| MT-I07 | B | `MAINNET_SECURITY_GATE.md` with PASS/FAIL per area | eng | open | |
| MT-I08 | B | `TESTING_AND_VERIFICATION.md` with exact test counts | eng | open | |
| MT-I09 | B | `INCIDENT_RESPONSE.md` for a system with no pause and no owner | eng | open | |
| MT-I10 | B | README brought up to date: the factory can be renounced; LIV-07 closed; swap v2, board v3; NFA payouts; the Zcash companion's real state | eng | wip | done: Punish by anyone (no lock frozen); Renounce; swap v2 Claim by anyone, the 200-block acceptance deadline, a required taker, `wmin`; NFA shares owed until `PayOwed`; the federation simulator-only; ring 16 hides among 8, not 16; non-DERO fees reach no holder; a node colluding with the counterparty costs a whole leg; the bootstrap clones at pinned commits. Open: the companion and real-node rows, once those agents report |
| MT-I11 | | `XMR_SWAP.md` §10: LIV-07 moves from "a defect" to fixed; `ZEC_SWAP.md` Z14/Z15: the data-source attacks at their true cost | eng | open | |
| MT-I12 | | Stale: `NFA_BUILD.md`, `DECISIONS.md:313`, BL-241, BL-147, D-10, the renounce ceremony in `deploy/README.md` | eng | done | the asset agent updated each; `contracts/README.md` sizes refreshed |
| MT-I13 | | `docs/history/HISTORY.md` and `docs/TODO.md`: the record of this pass | eng | open | |
| MT-I14 | | `docs/audit/FINDINGS_2026-09-10.md`: status lines for every finding repaired in this pass | eng | open | |

## J. Operator decisions that block mainnet

| ID | B | Decision | Options (the study's numbering) | Status |
|---|---|---|---|---|
| MT-J01 | B | Swap track adopted and the federation parked (decision 1) | recommended (a) | decide. The code already follows (a) |
| MT-J02 | B | The first gas (decision 3) | (a) a maker stipend; (b) holders bring DERO | decide |
| MT-J03 | B | Monero confirmations (decision 4) | (a) 20 minimum with tiers; (b) allow 10, disclosed | decide |
| MT-J04 | B | The gate deadline G (decision 5) | 100 blocks is assumed | decide |
| MT-J05 | B | T2 − T1, and T1Max/T2Max (decision 6) | (a) 400; (b) up to 1,200 | decide |
| MT-J06 | B | Fund the proof audit (decision 7) | — | decide |
| MT-J07 | | The first ZEC gas (decision 9) | (a) lock by name, off board; (b) taker brings DERO | decide |
| MT-J08 | | Handing the pre-signed refund to third parties (decision 10) | allow and disclose (recommended) | decide |
| MT-J09 | | Who runs makers (decision 13) | a named third party, or "no maker yet" said on screen | decide |
| MT-J10 | B | Who signs and publishes companion releases (decision 14) | (a) a named third party; (b) frozen at a reproducible tagged build | decide |
| MT-J11 | B | The liveness ruling and the swap windows (decision 15) | (a) accept per-party windows, each named on screen; (b) no swap track | decide |
| MT-J12 | B | Remote nodes (decision 16) | (a) full-node holders only; (b) allowed and disclosed as able to cause total loss | decide |
| MT-J13 | | `htlc.bas` v2's form (decision 17). Built: anyone may Punish from `t2`, and it pays the taker. The study's (a) was a maker window from `t2 + G2` | confirm the built form, or ask for (a) | decide |
| MT-J14 | B | FEE-01/FEE-02 (MT-A08) | (a)–(d) | decide |
| MT-J15 | | The factory's final shape (MT-A19) | renounce, or fixed at install | decide |
| MT-J16 | B | Founders Pass numbers (supply, reserve, share, fee, name, description) | the operator's to set | decide |
| MT-J17 | | Rewriting git history for the binaries (MT-G08) | — | decide |
| MT-J18 | | GAP-026: anyone can Accept a Zcash offer and hold it for 200 blocks, then do it again, for one fee each time. Options: a taker bond on Accept (D-7, BL-45); a maker allow-list; or the board declared advisory, with makers reposting | bond, allow-list, or advisory | decide |
| MT-J19 | | `board.bas` `aw` = 200 blocks, the acceptance window, is an assumed P_lock value; changing it later means a new SCID | confirm 200, or set another value before the mainnet install | decide |

## K. Economics and fees

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-K01 | | BL-90: fees in a non-DERO quote reach no pass holder. Per-asset pools, or no non-DERO quotes on mainnet | op | decide | the choice written into the README and the app notice |
| MT-K02 | | A fee on the swaps, if wanted: only possible on the DERO side, by a new escrow version | op | decide | recorded |
| MT-K03 | | The `htlc.bas` Claim gas measured (it sizes the stipend) | eng | open | the figure in `XMR_SWAP.md` |

## L. Privacy

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-L01 | B | A privacy map: what each component reveals, to whom | eng | wip | gap analysis §7; the threat model |
| MT-L02 | | Companion logs: no secrets, no keys; amounts and addresses only where needed | eng | open | a grep test on log output in both companions |
| MT-L03 | | Ring 2 and the Zcash linkage stated on every screen that sends such a call | eng | done | UI pass |

## M. Performance

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-M01 | | Latency of each swap step measured on regtest (Monero: the gate, the Claim, the sweep; Zcash: fund to claim) | eng | done | `research/regtest/latency.md`, summarised in `TESTING_AND_VERIFICATION.md` §4. Regtest blocks come in seconds, so these are not mainnet times: DERO's 8-block stable waits dominate |
| MT-M02 | | App start time and the first render against the simulator | eng | done | `ui.mjs --perf --runs=5`, medians: at 430 px cold, first paint 36 ms and first data render 212 ms (494.7 kB, 28 files); warm, 97 ms. At 1280 px cold, 206 ms. 33 node calls before the first data render. Loopback node (2.8 ms round trip): against a remote node those 33 calls dominate |
| MT-M03 | | Proof timings (≈10.1 s prove, ≈2.3 s verify, generators included) rechecked on the release build | eng | open | measured |

## N. After deployment (the operator, once, then never again)

| ID | B | Item | Owner | Status | Done means |
|---|---|---|---|---|---|
| MT-N01 | B | Install, verify with `verifydeploy`, publish the hashes, Renounce the factory, deal with the INDEX key as `MAINNET_DEPLOYMENT.md` says, destroy the deployer key | op | blocked | `verifydeploy` on mainnet reports no live privileged role |
| MT-N02 | B | Hand over the companions' reproducible release (MT-J10) | op | blocked | the release manifest published |
