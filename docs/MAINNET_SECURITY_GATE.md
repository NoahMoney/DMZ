# DMZ: mainnet security gate

*2026-09-11, the final mainnet pass. One line per area, PASS or FAIL, each with the evidence that decides it. A line
reads PASS only when the evidence is in this repository and was produced in this pass, or is cited to where it was.
Nothing is marked PASS because it is expected to pass.*

## Verdict

# NOT MAINNET READY

This is not because a known exploitable defect was left open in code: every one found that the repository could fix
was fixed and has a regression test (`MAINNET_TODO.md`, `docs/audit/FINDINGS_2026-09-10.md` status lines,
`MAINNET_GAP_ANALYSIS.md`). It is because these conditions for mainnet are not met, and several of them cannot be
met from this machine:

1. **The one new piece of cryptography has not been audited.** The Monero swap's cross-curve proof (SHA256(s) = H and
   s·G = S) is built on dalek's R1CS "yoloproofs" API, which upstream marks unaudited (MT-G03).
2. **No swap has run on a public test network.** Both companions now run against real Monero and Zcash node software
   in regtest, but not on Monero stagenet or Zcash testnet (MT-F03, MT-F04).
3. **The current contracts are not deployed on the DERO testnet** (MT-F05).
4. **No external review of the contracts** (MT-A17).
5. **Operator decisions are open** (`MAINNET_TODO.md` §J). Among them are the liveness ruling for the swap windows
   (MT-J11), remote nodes (MT-J12), the first gas (MT-J02), the Founders fee design (MT-J14) and who signs
   releases (MT-J10).

*Closed since this verdict was first written:*
- the go1.26.2 standard library's reachable vulnerabilities (GAP-015). Every build now uses go1.26.8, and govulncheck
  finds nothing reachable in the tools, derod, the simulator or netwallet (`research/release/govulncheck-2026-09-12.log`);
- the missing real reorganisation (MT-B11, MT-C02), now run on real Monero and Zcash node software in regtest
  (`research/regtest/xmrswap-reorg.log`, `zecswap-reorg.log`).

---

## The gate

| Area | Result | Evidence and reason |
|---|---|---|
| **Smart contracts** | **FAIL** | *Internally:* every contract passes the DVM suite on the final code, `factory.bas` v2 and `htlc.bas` v3 included (175 runs, 0 failures, go1.26.8, 2026-09-12). Every repaired finding has a test. No known critical or high defect is open in a mainnet contract. *Missing:* an external review (MT-A17). Open by operator decision: FEE-01/02 (MT-J14) and GAP-026, board griefing (MT-J18) |
| **Zcash integration** | **FAIL** | *Proven on real zebrad 6.3.0 in regtest:*<br>• three swaps: a v5 claim with the maker's relay, a v6 claim, and a third party's broadcast of the pre-signed refund, all as intended (`research/regtest/zecswap-e2e.log`);<br>• three reorganisations, which found and fixed a maker that could wait for ever after a deep one (`zecswap-reorg.log`);<br>• the header gate's plumbing, also run on the real testnet node (`zecswap-headers.log`).<br>The v6 gap is closed (MT-C16). Txids and digests agree with librustzcash. Crash fuzz ×3. The command line ran a full swap and a refund as separate maker, taker and status processes (`zecswap-cli.log`). *Missing:* Zcash testnet (MT-C15); `-verify-headers` on by default |
| **Monero integration** | **FAIL** | *Proven on real monerod and monero-wallet-rpc 0.18.5.1 in regtest:* all three exits, twice, with 0 DERO left after each and balances matching the wallets' own records (`research/regtest/xmrswap-e2e.log`). The real-server fixes are in (MT-B20 to MT-B24). A real reorganisation before and after the gate behaved as R3 says (`xmrswap-reorg.log`, MT-B11). The command line ran a whole swap as two separate processes, through Bob's sweep, with the sealed channel, the witness check, the recovery card and a real wake (`xmrswap-cli.log`). *Missing:* stagenet (MT-B19); the proof audit (MT-G03) |
| **DERO integration** | **FAIL** | the contracts run on derohe's own DVM and on its simulator with real XSWD wallets. The deployment tooling is proven there (`simdeploy`, a v2 factory with `-migrate-from`, `verifydeploy`, `telainstall -ringsize 16`). *Missing:* the current contracts on the DERO testnet for a week (MT-F05) |
| **Cross-chain verification** | **PASS** | *By design:* no DERO contract verifies a foreign chain, and none needs to (`CROSS_CHAIN_TRUST_MODEL.md` §3). *In code and on real nodes:* each companion verifies the foreign chain with the node it is given, and both ran every exit against real Monero and Zcash node software in regtest. zecswap requires every configured source to agree before an irreversible step. The trust that remains, each party's own view of each chain, is named as T6 with its cost. xmrswap reads its DERO check from several sources too (MT-B09) |
| **Replay protection** | **PASS** | every escrow and offer moves from state 0 once (`st_<id>`). Secrets and hashes are refused on reuse (`TestRefusesReusedSecret`, `ErrHashReused`). A Zcash funding's identity is (network, txid, vout), bound to one swap (`TestFundingCreditedTwiceIsRefused`, `TestSameFundingForTwoSwapsIsRefused`, `TestTestnetNodeOnMainnetSwapIsRefused`). A contract id exists on one network only (`CROSS_CHAIN_TRUST_MODEL.md` §4) |
| **Reorganisation protection** | **PASS** | the policies are defined: DERO 8 blocks by consensus, Monero 20, Zcash 10. Reorganisations were run on real node software in regtest:<br>• Monero: before and after the gate (`research/regtest/xmrswap-reorg.log`);<br>• Zcash: a funding removed below claim depth, and removed after the claim with and without a second spend (`zecswap-reorg.log`).<br>Each companion behaved as its design says. One defect was found and fixed, and has a test. *Inherent, disclosed:* neither Monero nor Zcash bounds a reorganisation in consensus (T3). *Not shown:* a DERO-side reorganisation, which the one-node simulator cannot produce; DERO's stable depth is its consensus rule |
| **Accounting invariants** | **PASS** | randomized conservation after every accepted and refused call, for every contract that holds value: pool, launch, founders, nft, nfa, swap, board (`tests/zzconservation_test.go`, `tests/nfa_test.go`) and htlc (`tests/zzconservation_htlc_test.go`: 119 accepted and 206 refused calls, 29 exits). Own-token supply is checked too. The foreign legs hold no pooled reserve that could drift: each swap's XMR or ZEC sits under its own lock |
| **Key management** | **PASS** | DMZ holds no production key: no server key exists, the app stores no secret, the factory has no owner (v2), and after a ring-16 INDEX no deployer key holds a role (`SECURITY_KEY_MANAGEMENT.md`). Mainnet wallets require an RPC login, given through the environment. A user's keys are in the user's own software: the joint Monero wallets are encrypted, state files are owner-only (ACL), and the Zcash key is overwritten after its one use. *Residual, stated:* the swap state file is not encrypted, so that the companion can restart unattended (GAP-020) |
| **Upgrade authority** | **PASS** | no DMZ contract calls `UPDATE_SC_CODE`. The factory has no owner at all (`factory.bas` v2, MT-J15(b)): no function but `Initialize` writes its treasury or fee (`TestFactoryFixedAtInstall`, `TestFactoryOldEntrypointsAreGone`). A ring-16 TELA INDEX is immutable, proven live (`research/release/tela-anon-index.log`). `verifydeploy` fails any surviving deployer power. `vault.bas` is not deployed |
| **Privacy** | **PASS** | the app makes no request beyond the user's node, the local wallet and its own files (the mainnet-shaped UI pass). What cannot be avoided is disclosed before use: ring-2 signers, the Zcash leg's public linkage, and the maker's view of the XMR amount (`SECURITY_THREAT_MODEL.md` §8). Both companions have a test that no secret or key reaches their output (MT-L02) |
| **DoS resistance** | **PASS** | every contract call is bounded by paid gas and by the 20,000 storage-gas cap, and no contract loops over anything a stranger can grow. zecswap caps every response (32 MiB, 30 s) and every list. xmrswap reads every reply through a size cap and refuses malformed ones (MT-B07). The app reads only the user's own node |
| **Dependency security** | **PASS** | Every build uses go1.26.8 (GAP-015). govulncheck finds 0 reachable in the tools, derod, the simulator and netwallet (`research/release/govulncheck-2026-09-12.log`). derohe's vendored code, invisible to govulncheck, is inventoried module by module against vuln.go.dev and osv.dev, and none is reachable (GAP-018, `docs/DEPENDENCIES.md`). The vendored trees, telainstall's fork (GAP-017) and the prover's patched crate (GAP-013) are pinned and verified. *Accepted, with the reason recorded:* gorilla/websocket GO-2026-6278 in telainstall and the DERO binaries (D-2: loopback only, shipped to no user). *Unreachable, with no upstream fix:* x/crypto GO-2026-5932 |
| **Build reproducibility** | **PASS** | two build paths give byte-identical manifests, and `-verify` matches 68 of 68 (`research/release/reproducible-build.log`). After the move to go1.26.8 it was run again from a clean commit: 10 binaries, 62 of 62 (`reproducible-build-go1268.log`). The contract hashes the manifest records are the ones `verifydeploy` checks on chain |
| **End-to-end tests** | **PASS** | every exit of both swaps ran end to end on real Monero and Zcash node software (regtest) against the DERO contracts on the simulator. That is three Monero exits twice, plus a v5 claim, a v6 claim and a third-party refund on Zcash (`research/regtest/`). The UI passes are green on the final contracts, including with wallet flows. *Covered by other lines, not this one:* the public test networks (Monero and Zcash integration) |
| **Adversarial tests** | **PASS** | every attack found in the 2026-09-10 audit or this pass that the code could close has a named regression test that fails on the old code (`TESTING_AND_VERIFICATION.md` §2) |
| **Mainnet deployment** | **FAIL** | not deployed, correctly: the procedure and its checks exist (`MAINNET_DEPLOYMENT.md`), and the lines above must pass first |

---

## The directive's thirteen conditions (§36)

| # | Condition | Met? |
|---|---|---|
| 1 | Every critical path implemented | **no**: the companions' real-node fixes are in progress, and there is no discovery for Monero counterparties (BL-10, BL-12) |
| 2 | No known critical or high vulnerability | **none known in code**: the CRITICAL and HIGH rows of the gap analysis are repaired, or are process items (GAP-004 real networks, GAP-014 signed releases, GAP-015 the toolchain) listed as blockers. The unaudited proof stays the largest unknown |
| 3 | External-chain verification cryptographically sound | **the design is**: nothing on chain claims to verify another chain. The implementation is not proven on real networks |
| 4 | Accounting invariants hold | **yes**: every value-holding contract is in the randomized conservation suite |
| 5 | Replay and reorganisation attacks handled | replay yes; reorganisation not proven on a real chain |
| 6 | Privileged authority minimised or removed | **yes**: the tooling to reach and verify the final state is proven |
| 7 | Production keys cannot be extracted from software | **yes**: DMZ holds no production key. A user's swap state is in the user's own software: owner-only, and not encrypted, so it can restart unattended |
| 8 | Cross-chain settlement tested end to end | **on real node software in regtest, yes**, every exit of both swaps. On public networks, no |
| 9 | Failure scenarios tested | in part: crash fuzz and the failure model; not on real networks |
| 10 | Dependencies pinned and reproducible | pinned yes; the toolchain upgrade is outstanding |
| 11 | No undocumented centralised dependency | **yes** (`DECENTRALIZATION_ARCHITECTURE.md` §2) |
| 12 | The decentralisation and trust model documented | **yes** (`CROSS_CHAIN_TRUST_MODEL.md`, `DECENTRALIZATION_ARCHITECTURE.md`) |
| 13 | The deployment artifact independently verified against source | the tooling exists and is proven on the simulator. No mainnet artifact exists yet to verify |

---

## What turns this page to PASS

In order, with the items that carry each:

1. A real reorganisation on regtest for both swaps (MT-B11).
2. go1.26.6 or later, re-pin, rebuild.
3. The companions' command lines run as separate processes against the regtest nodes.
4. Monero stagenet and Zcash testnet runs (MT-F03, MT-F04), then the DERO testnet for a week (MT-F05).
5. The external reviews (MT-A17, MT-G03).
6. The operator decisions (§J).
7. Then `MAINNET_DEPLOYMENT.md`, and the mainnet stage of `docs/MAINNET_TEST_PLAN.md` at the smallest amounts.
