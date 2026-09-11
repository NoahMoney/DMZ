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
5. **The Go toolchain carries 10 reachable standard-library vulnerabilities** fixed in go1.26.3–1.26.6. Every
   release binary must be rebuilt with go1.26.6 or later (GAP-015).
6. **Operator decisions are open** (`MAINNET_TODO.md` §J). Among them are the liveness ruling for the swap windows
   (MT-J11), remote nodes (MT-J12), the first gas (MT-J02), the Founders fee design (MT-J14) and who signs
   releases (MT-J10).
7. **No reorganisation has been run on a real foreign chain** (MT-B11). Neither Monero nor Zcash bounds one in
   consensus, so the confirmation policy is the only protection. It has been reasoned about and tested against fakes,
   not shown on a real node.

---

## The gate

| Area | Result | Evidence and reason |
|---|---|---|
| **Smart contracts** | **FAIL** | *Internally:* every contract passes the DVM suite on the final code (168 runs, 0 failures). Every repaired finding has a test. No known critical or high defect is open in a mainnet contract. *Missing:* an external review (MT-A17). Open by operator decision: FEE-01/02 (MT-J14) and GAP-026, board griefing (MT-J18) |
| **Zcash integration** | **FAIL** | *Proven on real zebrad 6.3.0 in regtest:* three swaps (v5 claim with the maker's relay, v6 claim, a third party's broadcast of the pre-signed refund), all as intended (`research/regtest/zecswap-e2e.log`). The v6 gap is closed (MT-C16). Txids and digests agree with librustzcash. Crash fuzz ×3. *Missing:* Zcash testnet (MT-C15), and the command line itself run against a node |
| **Monero integration** | **FAIL** | *Proven on real monerod and monero-wallet-rpc 0.18.5.1 in regtest:* all three exits, twice, with 0 DERO left after each and balances matching the wallets' own records (`research/regtest/xmrswap-e2e.log`). The real-server fixes are in (MT-B20 to MT-B24). *Missing:* stagenet (MT-B19); the proof audit (MT-G03); a real reorganisation (MT-B11) |
| **DERO integration** | **FAIL** | the contracts run on derohe's own DVM and on its simulator with real XSWD wallets. The deployment tooling is proven there (`simdeploy`, `-renounce`, `verifydeploy`, `telainstall -ringsize 16`). *Missing:* the current contracts on the DERO testnet for a week (MT-F05) |
| **Cross-chain verification** | **PASS** | *By design:* no DERO contract verifies a foreign chain, and none needs to (`CROSS_CHAIN_TRUST_MODEL.md` §3). *In code and on real nodes:* each companion verifies the foreign chain with the node it is given, and both ran every exit against real Monero and Zcash node software in regtest. zecswap requires every configured source to agree before an irreversible step. The trust that remains, each party's own view of each chain, is named as T6 with its cost. Reads from several sources for xmrswap's DERO check (MT-B09) are an open hardening |
| **Replay protection** | **PASS** | every escrow and offer moves from state 0 once (`st_<id>`). Secrets and hashes are refused on reuse (`TestRefusesReusedSecret`, `ErrHashReused`). A Zcash funding's identity is (network, txid, vout), bound to one swap (`TestFundingCreditedTwiceIsRefused`, `TestSameFundingForTwoSwapsIsRefused`, `TestTestnetNodeOnMainnetSwapIsRefused`). A contract id exists on one network only (`CROSS_CHAIN_TRUST_MODEL.md` §4) |
| **Reorganisation protection** | **FAIL** | the policies are defined (DERO 8 by consensus, Monero 20, Zcash 10), and zecswap's reorganisation tests pass against fakes. *Missing:* a real reorganisation on regtest (MT-B11). *Inherent, disclosed:* neither Monero nor Zcash has a consensus bound (T3) |
| **Accounting invariants** | **PASS** | randomized conservation after every accepted and refused call, for every contract that holds value: pool, launch, founders, nft, nfa, swap, board (`tests/zzconservation_test.go`, `tests/nfa_test.go`) and htlc (`tests/zzconservation_htlc_test.go`: 119 accepted and 206 refused calls, 29 exits). Own-token supply is checked too. The foreign legs hold no pooled reserve that could drift: each swap's XMR or ZEC sits under its own lock |
| **Key management** | **PASS** | DMZ holds no production key: no server key exists, the app stores no secret, and after `Renounce` and a ring-16 INDEX no deployer key holds a role (`SECURITY_KEY_MANAGEMENT.md`). Mainnet wallets require an RPC login, given through the environment. A user's keys are in the user's own software: the joint Monero wallets are encrypted, state files are owner-only (ACL), and the Zcash key is overwritten after its one use. *Residual, stated:* the swap state file is not encrypted, so that the companion can restart unattended (GAP-020) |
| **Upgrade authority** | **PASS** | no DMZ contract calls `UPDATE_SC_CODE`. The factory's `Renounce` is proven live (`research/release/simdeploy-renounce-sim.log`). A ring-16 TELA INDEX is immutable, proven live (`research/release/tela-anon-index.log`). `verifydeploy` fails any surviving deployer power. `vault.bas` is not deployed |
| **Privacy** | **PASS** | the app makes no request beyond the user's node, the local wallet and its own files (the mainnet-shaped UI pass). What cannot be avoided is disclosed before use: ring-2 signers, the Zcash leg's public linkage, and the maker's view of the XMR amount (`SECURITY_THREAT_MODEL.md` §8). Open, low: a test that no companion writes a secret to its output (MT-L02) |
| **DoS resistance** | **PASS** | every contract call is bounded by paid gas and by the 20,000 storage-gas cap, and no contract loops over anything a stranger can grow. zecswap caps every response (32 MiB, 30 s) and every list. xmrswap reads every reply through a size cap and refuses malformed ones (MT-B07). The app reads only the user's own node |
| **Dependency security** | **FAIL** | the vendored trees are pinned by commit and verified. The prover's patched crate is pinned and verified (GAP-013). *Open:* go1.26.2's reachable vulnerabilities (GAP-015); derohe's vendored third-party code, which govulncheck cannot see (GAP-018); telainstall's gorilla/websocket (GAP-017) |
| **Build reproducibility** | **PASS** | two build paths give byte-identical manifests, and `-verify` matches 68 of 68 (`research/release/reproducible-build.log`). The contract hashes the manifest records are the ones `verifydeploy` checks on chain |
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
