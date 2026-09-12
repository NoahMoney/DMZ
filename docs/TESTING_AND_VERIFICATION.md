# DMZ: testing and verification

*2026-09-11, the final mainnet pass. Every number below comes from a run on this date against the working tree of
the pass. Each row names its command and, where one is kept, its evidence. Logs under `.sim/` are local, because the
directory is git-ignored; logs under `research/` are committed. Rows marked **pending** wait on a run still in progress
when this page was written.*

---

## 1. Results

| Suite | What it covers | Command | Result |
|---|---|---|---|
| **DVM contract suite** | every contract, run by derohe's own DVM. It covers unit tests, adversarial and liveness cases, randomized conservation, the static send-order and payee-order checks, the install-gas cap, entrypoint lists and the shipped templates | `tools/withlock.sh dvm bash tests/run.sh -v` | **175 runs, 0 failures**, `ok … 82.3s` on go1.26.8 (`.sim/dvm-v3-0912.log`, 2026-09-12), on `factory.bas` v2 and `htlc.bas` v3, including the `htlc.bas` conservation test |
| **Browser arithmetic and sanitisers** | the app's AMM and launch maths against the Go reference; name sanitising; impersonation marks; the copy pinned to `app/notice.js` | `node --test tests/js` | **44 of 44** |
| `tools/treasurybot` | fee claims, pass deposits, `PayOwed` only for NFAs running the shipped template | `go test -v ./treasurybot` | **18 passed** |
| `tools/vaultsigner` | the federation daemon (simulator only): refusal of the file backend off the simulator, exactly-once payouts | `go test -v ./vaultsigner` | **18 passed** |
| `tools/netcfg` | the per-network record, including the swap contracts | `go test -v ./netcfg` | **2 passed** |
| `tools/simdeploy` | template revocation, Founders Pass limits, `wmin` bounds, the runbook's own description | `go test -v ./simdeploy` | **12 passed** |
| `tools/verifydeploy` | canonicalisation, first-difference reports, the role table held to the contracts, TELA INDEX analysis, and the record's contracts all being checked | `go test -v ./verifydeploy/...` | **28 passed** |
| `tools/release` | the manifest round trip and comparison | `go test -v ./release/...` | **5 passed** |
| `tools/xmrswap` | the Monero companion: 13 invariants by name, adversarial cases, the fixes found against the real wallet server, `TestCrashFuzz` | `go test ./xmrswap/...`; `go test -run TestCrashFuzz -count=3 ./xmrswap` | **110 passed**; crash fuzz ×3 `ok … 138.9s` |
| `tools/zecswap` | the Zcash companion: node client, watcher, ZIP 317 fee, builders, the pre-signed refund, state machines, reads from several sources that must agree, code pins, `TestCrashFuzz` | `go test ./zecswap/...`; `go test -run TestCrashFuzz -count=3 ./zecswap` | **135 passed** after the v6 and regtest changes (the command-line package `ok`). Crash fuzz ×3 `ok … 48.6s`, 132 runs each. The v6 tests use real zebrad transactions (`testdata/regtest_v6.json`) |
| **UI pass, normal** | every screen at 390, 768 and 1024 px on the final simulator record | `tools/withlock.sh ui env PW_DIR=<playwright> node tools/uitest/ui.mjs <url> .sim/ui-final-0911` | **UI TEST OK**; no horizontal overflow |
| **UI pass, mainnet-shaped** | the same app under a config with network "mainnet" and no vault | the same, with `--mainnet` | **UI TEST OK (mainnet-shaped)**; "requests that left this device: none" |
| **UI pass, wallet flows** | signing through a real XSWD wallet; the network gate; every outcome check | `ui.mjs --wallet` | **UI TEST OK** (`.sim/ui-wallet-2`). It found one real defect, fixed in this pass: a successful factory registration was reported as "did not execute" |
| **Deployment verification** | the final simulator record against this source: `factory.bas` v2 and `htlc.bas` v3 (installed with w1min 400, w2min 400, g2 4800) | `go run ./verifydeploy -network simulator -app ../app`, with no `-allow`: the v2 factory has no owner | **31 PASS, 13 WARN, no failure**, 2026-09-12 (`research/release/verifydeploy-final-sim.log`). The WARNs are 11 legacy template hashes the app still accepts and the vault's signers (k = 2 of 3, by design) |
| **Factory v2, no owner** | no function but `Initialize` writes the treasury or the fee; every former owner entrypoint is gone; the simulator's markets moved onto a v2 factory with `-migrate-from` | `TestFactoryFixedAtInstall`, `TestFactoryOldEntrypointsAreGone`, `TestFactoryHasNoOwnerToTransferOrRenounce`; the simulator record | green; all 6 listings migrated (2 pools, 3 launches, 1 NFA) |
| **An immutable TELA INDEX, live** | an INDEX at ring 16: owner `"anon"`; `UpdateCode` from the installer mined and without effect | `research/release/tela-anon-index.log` | as described |
| **Reproducible builds** | xmrswap and zecswap for five platforms | `go run ./release -out …`; `-verify` | two build paths **byte-identical**; `-verify` **68 of 68** (`research/release/reproducible-build.log`, go1.26.2). **Re-run on go1.26.8, 2026-09-12**, from a clean commit with the bundle regenerated: 10 binaries, `dirty=false`, `-verify` **62 of 62** (`research/release/reproducible-build-go1268.log`) |
| **Zcash swap, live on the simulator** | `swap.bas` v2 and `board.bas` v3 on the final code: a Claim sent by a third wallet pays the taker; a narrow lock refused on chain; a refund; the acceptance deadline; the escrow empty at the end | `tools/simswap` | **exit 0**; every txid in the contracts agent's report, summarised in `MAINNET_TODO.md` MT-A04 and MT-A05 |
| **Real Monero node (regtest)** | the 11 wallet-rpc methods xmrswap calls, on monerod and monero-wallet-rpc 0.18.5.1 | `tools/regtest/smoke-monero.sh` | every method answers as expected; **31 differences from the fake**, recorded (`research/regtest/monero-smoke.log`) |
| **Real Zcash node (regtest)** | the 7 core methods zecswap calls, on zebrad 6.3.0 (the header gate's `getblock` and `getblockheader` are exercised by `TestRegtestHeaderPlumbing`, `research/regtest/zecswap-headers.log`); zecswap's ZIP 244 digests against an independent librustzcash build | `tools/regtest/smoke-zcash.sh` | every method answers; **txids and signature digests agree** with librustzcash; v6 and regtest chain differences recorded (`research/regtest/zcash-smoke.log`) |
| **Monero swap on real regtest** | all three exits of `htlc.bas` v2 with real monerod and monero-wallet-rpc 0.18.5.1, and the DERO simulator | `tools/simhtlc` against `tools/regtest` | **all three exits, twice**:
- **Happy:** Bob's sweep in monerod block 2973 (run 2).
- **Refund:** Alice's recovery with `s_b` in block 3675.
- **Punish:** the maker's asset paid to the taker; her XMR stays under the joint key, by design.

0 DERO was left in the contract after every exit, and the balance changes match the wallets' own records. Run 2 named the monerod after every wallet open: 55 `set_daemon` calls accepted. Every txid is in `research/regtest/xmrswap-e2e.log` |
| **Monero swap on real regtest, `htlc.bas` v3** | the same three exits on v3, whose Punish is accepted only in [t2, t2 + g2) and whose fourth exit is the maker's Reclaim | `tools/simhtlc -regtest`, which installs its own v3 at w1min 10, w2min 6, g2 30 | **all three exits**, 2026-09-12, 46 min:
- **Happy:** Alice's claim was stable at 965; Bob's sweep is in monerod block 7128.
- **Refund:** Alice's recovery sweep is in block 7824.
- **Punish:** stable at 1086. Her XMR stays under the joint key, by design.

0 DERO was left after each exit. The run was made before the review's fixes, which changed the Reclaim path and the wording, not these three exits. **Reclaim has not run live**: only the DVM suite and the unit tests cover it. Every txid is in `research/regtest/xmrswap-e2e.log`, run 3 |
| **Zcash swap on real regtest** | fund, claim (v5 and v6), the DERO Claim, a refund, the pre-signed refund | `ZECSWAP_REGTEST=1 go test -run TestRegtestEndToEnd ./zecswap` | **three swaps as intended** on zebrad 6.3.0 and the DERO simulator:
- A: a v5 claim, with the maker's companion sending the away taker's DERO Claim;
- B: a v6 claim, whose secret the taker's own watcher read;
- C: a refund broadcast by a third party holding only the pre-signed file, refused before `cltv` and mined at `cltv + 1`.

Every txid is in `research/regtest/zecswap-e2e.log`. Swap C was rerun after a fix to the test's own third party; production code was unchanged |
| **Vendored and pinned trees** | every vendored checkout at its commit, the `bulletproofs` crate plus its patch, pinned binaries | `go run ./verifyvendor -root ..` | 3 findings, all rebuild-and-re-pin with provenance checked (`MAINNET_TODO.md` MT-H02). The crate tree verifies |

---

## 2. The directive's test categories, and where each lives

| Category | Where | Status |
|---|---|---|
| Unit | every `tests/*.go`, `tools/*/…_test.go`, `tests/js` | green |
| Integration | the live simulator drivers (`simswap`, `simhtlc`, `simvault`, `simwrapmkt`, `simnfa`, `simdeploy`, `verifydeploy`) | green on the final contracts where run |
| End to end | the UI passes; the regtest runs of both swaps | UI green; every exit of both swaps green on real regtest nodes, and the reorganisations on both foreign chains (`research/regtest/`) |
| Property-based | `tests/zzconservation_test.go` and `tests/zzconservation_htlc_test.go` (seeded random call sequences, accepted and refused, across every value-holding contract; htlc: 119 accepted and 206 refused calls, 29 exits); the pool and launch model fuzz | green |
| Fuzz | `TestCrashFuzz` in both companions; `tests/vault_fuzz_test.go`; zecswap's precision fuzz | green against fakes |
| State machine | the companions' transition tables (`TestTransitionTableRefusesEveryUnlistedMove`); the contracts' single-shot `st_<id>` states | green |
| Failure injection | the crash fuzz kills both parties at every durable write, with flaky DERO answers and lost broadcast replies | green |
| Reorganisation | zecswap: `TestDepthDetectsAReorganisation`, `TestStaleFundingBeforeReorgIsNotClaimed` (fakes). A real reorganisation on regtest (monerod `pop_blocks`, Zebra `invalidateblock`) has **not** been run (MT-B11) | open |
| Replay and duplicates | `st_<id>` single-shot tests; `TestRefusesReusedSecret`; `ErrHashReused`; `TestFundingCreditedTwiceIsRefused`; `TestSameFundingForTwoSwapsIsRefused`; `TestTestnetNodeOnMainnetSwapIsRefused` | green |
| Concurrency | the DVM executes serially per block; each companion holds a lock file on its state; the crash fuzz resumes from every interleaving it reaches | green |
| Serialization | ZIP 244 vectors (`tools/zecswap/testdata/zip_0244.json`); zecswap's txids against real Zebra and librustzcash; Monero reference keys | green |
| Contract invariants | the conservation suite; the send-order and payee-order static checks; the gas-cap test | green |
| Cryptographic vectors | RFC 7693 BLAKE2b; ZIP 244; Monero's published reference keys; the proof's own run (`research/crosschain/poc/proof-xmr/run.log`) | green. The proof is **unaudited** |
| Cross-chain settlement | the regtest runs of both swaps | in progress |
| Adversarial (directive §26) | the `zz*` suites, among them anonymous callers, stray value, forged callers against every former owner path (`TestFactoryOldEntrypointsAreGone`, `TestFactoryHasNoOwnerToTransferOrRenounce`), rounding and boundaries (`zzsplitpay`, `zzdvmarith`, `zzextreme`, `uint128`), griefing (`zzadv_market`), and the data-source attacks (`TestOneLyingZcashNodeBlocksTheReveal`, `TestOneLyingDeroNodeBlocksTheFunding`). "Mint without a deposit" does not apply: nothing mints against a deposit on mainnet | green |

---

## 3. What has not been tested

- Monero stagenet, Zcash testnet, or the DERO testnet with the current contracts (MT-F03 to MT-F05).
- Mainnet, ever. The rule is never mainnet from this repository.
- A DERO-side reorganisation: the one-node simulator cannot produce one. The foreign-chain reorganisations have been run on regtest (`research/regtest/xmrswap-reorg.log`, `zecswap-reorg.log`).
- An external audit of the contracts or of the Monero proof (MT-A17, MT-G03).
- Several independent real nodes disagreeing on a real network.
- The Monero FCMP++/Carrot fork, which has no mainnet height (MT-B17).

---

## 4. Measured figures

| Figure | Value | Source |
|---|---|---|
| Monero proof | ≈10.1 s to prove, ≈2.3 s to verify, generators included | `research/crosschain/poc/proof-xmr/run.log` |
| `htlc.bas` | installs at 3,907 storage gas (3,870 bytes); a Claim costs 304 | the Monero agent's DVM run |
| `swap.bas` | 2,816 storage gas with `wmin` 1,600 (2,767 bytes) | the contracts agent's DVM run |
| `board.bas` | 2,652 storage gas (2,611 bytes) | same |
| `nfa.bas` | 15,876 (DERO-quoted) and 15,941 (wrapped) storage gas; 14,937 bytes | the asset agent's DVM run |
| Monero on regtest | a transfer unlocks at 10 confirmations; a coinbase 60 blocks after its block; a wallet refresh 0.1–0.3 s | `research/regtest/monero-smoke.log` |
| Zcash on regtest | a v5 transaction 272–273 bytes, ZIP 317 fee 15,000 zatoshi; coinbase maturity 100 blocks | `research/regtest/zcash-smoke.log` |
| App bundle | 37 files plus `manifest.json`. The largest DOCs are `app.js` at 16,024 bytes and `token.js` at 15,192, of the 18,250-byte cap that keeps every DOC under the 20,000 storage-gas limit. `feed.js` (18,128) and `wrap.js` (18,080) were split to get there (MT-E10): `feed.js` 13,440 and `names.js` 5,792; `wrap.js` 12,960, `vault.js` 3,028 and `wrapui.js` 4,176 | `genconfig -dist -network simulator`; `research/release/tela-install-cost.log` |
| App install cost (MT-E07) | 35 DOCs at ring 2 and a ring-16 INDEX: 36 transactions, 322,732 atomic units (**3.227 DERO**), 12 min 15 s | `research/release/tela-install-cost.log` |
| Swaps on regtest (MT-M01) | Monero: happy exit 502 / 574 s, refund 805 / 909 s, punish 966 / 1,026 s (two runs); Zcash: a swap about 366 s, the refund 1,990 s. Regtest blocks come in seconds, so these are not mainnet times: DERO's 8-block stable waits dominate | `research/regtest/latency.md` |
| App start (MT-M02) | medians at 430 px: first paint 36 ms, first data render 212 ms cold (494.7 kB, 28 files), 97 ms warm; at 1,280 px, 206 ms cold. 33 node calls before the first data render. Loopback node (2.8 ms round trip): with a remote node the 33 calls dominate | `ui.mjs --perf --runs=5` |
| Zcash header verification | about 0.3 ms per header, almost all of it Equihash; a 210-header funding proof takes 55–80 ms. Modelled cost of forging 10 confirmations: 4.49 blocks of network work with only 28 headers below the funding, 8.94 with the recommended policy | `research/zecspv/bench.log`; `ZCASH_HEADER_VERIFICATION.md` §4 |

---

## 5. How to reproduce

`MAINNET_DEPLOYMENT.md` §2 brings a clean machine up to the pinned trees. Then:

```
tools/withlock.sh dvm bash tests/run.sh -v
node --test tests/js
cd tools && go test ./xmrswap/... ./zecswap/... ./treasurybot ./vaultsigner ./netcfg ./simdeploy ./verifydeploy/... ./release/...
cd tools && go test -run TestCrashFuzz -count=3 ./xmrswap ./zecswap
tools/regtest/fetch.sh && tools/regtest/monero-up.sh && tools/regtest/zcash-up.sh
tools/regtest/smoke-monero.sh && tools/regtest/smoke-zcash.sh
```

With the simulator running, `tools/simdeploy`, then `verifydeploy`, then the UI passes, as in §1.

`tools/check.sh` runs the same steps in one command and prints one PASS or FAIL line for each. `--ui` adds the UI passes, `--verifydeploy` adds the check against the simulator, and `--only=` or `--skip=` picks steps. The UI pass needs Playwright 1.62.0 (Chromium 151.0.7922.34) in `$PW_DIR`, and `ui.mjs` refuses any other version (`tools/uitest/README.md`).
