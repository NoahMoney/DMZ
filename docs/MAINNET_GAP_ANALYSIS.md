# DMZ — mainnet gap analysis

## Scope and method

Date 2026-09-11. Repository `<repo>` (GitHub NoahMoney/DMZ), branch `wip/2026-09-09-swap-nfa`
at `0fba2c6`, which is also the commit `origin/main` and `origin/wip/2026-09-09-swap-nfa` point at. The audit covers
the working tree as it stood during the pass, not HEAD: `contracts/{board,factory,htlc,nfa,swap}.bas`, `app/*`,
`tools/xmrswap/*` and many `tests/*` carried uncommitted edits, `tools/xmrswap/pin.go` and eight files of
`tools/zecswap` (`build dero fee node policy state swap watch`) were untracked, and `testnet/signer-3000{3,4}.json` were
staged for deletion. Four other agents were editing those files while this pass ran; rows that depend on them say
"in flux". Read line by line: all twelve `contracts/*.bas`; `vendor/tela/TELA-INDEX-1/TELA-INDEX-1.bas`,
`TELA-DOC-1/TELA-DOC-1.bas` and the `TELA-MOD-1/tx` modules; the DVM code for `SIGNER()`, `UPDATE_SC_CODE`,
entrypoint naming, external transfers and own-asset minting in `vendor/derohe` (`dvm/dvm_functions.go` and
`blockchain/transaction_execute.go` are byte-identical in `vendor/derohe-civilware`, checked by `diff`);
`tools/telainstall`, `tools/publish.sh`, `deploy/testnet-*.sh`, `tools/genconfig`, `tools/netcfg`, `tools/simrpc`; the
companions' entry points and the files cited below; README.md, deploy/README.md, `deploy/*.json`, `deploy/*.lock`;
the section list of `docs/audit/FINDINGS_2026-09-10.md` and a sample of its "repaired" lines checked against code.
Sections 3 to 7 were gathered by three read-only sub-audits run under this one (secrets and full git history; trust
points and privacy; supply chain and stubs). Every claim of theirs that became a BLOCKER, CRITICAL or HIGH row here
was re-read at the cited lines; the rest carry their citations as reported. Run, read-only: `git log --all -p`,
`git rev-list --all`, `git ls-files`, `git check-ignore`, `git diff`; `go version` (go1.26.2 windows/amd64);
`go list -m all` in every module with `GOFLAGS=-mod=readonly` (the go.mod and go.sum hashes were identical before and
after); `govulncheck` 1.x in source and binary mode; a Legendre-symbol computation over the BN254 field (Node
BigInt). Not run, by instruction: `tests/run.sh`, anything under `tools/withlock.sh`, any node, wallet or companion.
So "a test asserts X" below means the assertion is written in the test, not that it passed today. Findings of
`FINDINGS_2026-09-10.md` are not repeated; where one bears on a row, its ID is named. Files that other agents created
after this pass's reads were **not audited**. The names alone suggest work on GAP-001 / §2.7, GAP-004 and GAP-007,
so re-check those rows against them:
- `tools/verifydeploy/*` and `tools/regtest/*`;
- `tools/xmrswap/{status,bounded_test,swap_v2_test}.go` and `tools/xmrswap/cmd/xmrswap/main_network_test.go`;
- `tools/zecswap/{quorum,status}.go` and `tools/simdeploy/passheaders_test.go`;
- `docs/{CROSS_CHAIN_TRUST_MODEL,DECENTRALIZATION_ARCHITECTURE,MAINNET_TEST_PLAN,MAINNET_TODO,SECURITY_THREAT_MODEL}.md`.

---

## Findings

BLOCKER: must not reach mainnet as is. CRITICAL: loss or theft of funds, or a permanent freeze, is reachable.

**Counts: BLOCKER 5 · CRITICAL 3 · HIGH 6 · MEDIUM 8 · LOW 12 · INFORMATIONAL 5 (39 rows).**

| ID | Severity | Area | file:line | What is wrong | Concrete failure or attack | Status | Fix |
|---|---|---|---|---|---|---|---|
| GAP-001 | BLOCKER | TELA / app delivery | `tools/telainstall/main.go:65,232,249`; `vendor/tela/tela.go:1586-1600`; `vendor/tela/TELA-INDEX-1/TELA-INDEX-1.bas:52-63`; `deploy/README.md:284-285,307-309,332-338` | The app's TELA INDEX is installed at ring size 2, so it records the installer as `owner` and `UpdateCode` stays callable by that key for ever. The pipeline has no way to produce an ownerless INDEX: every install and update is hard-coded to ring 2, and the runbook's rollback depends on updating. Same root as SEC-K-03 / FE-6, rated HIGH/MEDIUM there; escalated here because the mainnet directive is "no administrative escape hatch". | The publisher, or whoever steals the publisher's wallet, replaces the whole app: new `transfer` parameters, look-alike contract ids in `config.js`, a changed template list. The only defence is that each wallet prompt is read correctly. `UpdateCode(code, mods)` can also inject TELA MODs such as `TXDWD`, an owner-only DERO withdrawal (`vendor/tela/TELA-MOD-1/tx/TELA-MOD-1-TXDWD.bas`, `WithdrawDero`). | open | Give `telainstall` a `-ringsize` and refuse `-network mainnet` below 16 for the INDEX. Install each release as a new INDEX above ring 2 (owner "anon", §2). Publish the verification recipe of §2.7. A new release is a new SCID, not an update. |
| GAP-002 | BLOCKER | Deployment | `deploy/networks.json:3-19`; `tools/netcfg/netcfg.go:45-61`; `deploy/README.md:237-256`; `tools/xmrswap/cmd/xmrswap/main.go:48`; `app/` (no file names htlc, board or swap; a search for them matches only `navigator.clipboard`, e.g. `app/token.js:10`) | There is no install step, network record or configuration field for `htlc.bas`, `board.bas` or `swap.bas` on any network. `simdeploy` does not install them, and the mainnet runbook installs only factory, social and founders. The contracts that settle XMR and ZEC — the product — have no canonical instance, and the app has no screen that reaches them. | Nobody can follow the runbook to a working XMR or ZEC market. Each companion takes the contract id from a flag the operator typed or a page filled in: xmrswap checks the code it finds there (its code pin, `tools/xmrswap/swap.go:484-495`), zecswap does not (GAP-011). | open | Add `htlc`, `board`, `swap` to `netcfg.Network`, `networks.json` and `simdeploy`. Install them in the mainnet runbook, publish the ids with their code hashes, and pin the code in both companions. |
| GAP-003 | BLOCKER | Cryptography / supply chain | `tools/xmrswap/proof.go:40-52,163-176`; `tools/xmrswap/cmd/xmrswap/main.go:54`; `research/crosschain/poc/proof-xmr/Cargo.toml:9,25-26`; `docs/XMR_SWAP.md:453-456` | The counterparty's cross-curve proof (SHA256(s)=H and s·G=S) counts as valid when an external program named `proof-xmr`, found on PATH by default, exits with status 0. The program is built from the Bulletproofs R1CS `yoloproofs` feature, which upstream documents as unaudited, plus a local patch. Nothing pins or hashes the binary. | A soundness bug, a stale build, or any other executable called `proof-xmr` makes a companion accept an H that is not the hash of the discrete log of S. The reveal the contract forces then does not open the Monero output, so the side that relied on the proof ends with neither asset. | open (in flux: xmrswap) | Have the proof system audited or replace it. Pin the prover's sha256 in the companion next to `HTLCCodeSHA256`, require an absolute `-prover` path, and track and reproducibly build its source (GAP-013). |
| GAP-004 | BLOCKER | Evidence / completeness | `README.md:23-24,35-36`; `tools/zecswap/cmd/zecswap/main.go:1-9`; `tools/zecswap/swap.go:203` | The Monero companion has only run against an in-process fake wallet. The Zcash companion's only binary builds a script and parses an address ("Nothing here holds a key or talks to anything"). The node, watcher and spend library is untracked and, during the supply-chain pass, did not compile (`undefined: ErrEstimate`). | No XMR or ZEC transaction has ever been produced by DMZ code on a real network. Every liveness margin in `docs/XMR_SWAP.md` §4 and `docs/ZEC_SWAP.md` §5 is unmeasured, and a first mainnet user would be the test. | in flux (zecswap being written) | Stagenet/testnet runs with recorded txids for every exit of both markets (the study's steps 2 and 5, `docs/ATOMIC_SWAP_STUDY.md:665-681`) before any mainnet install. |
| GAP-005 | BLOCKER | Privileged capability | `contracts/factory.bas:32-50,52-83`; `deploy/README.md:237-256`; `tools/netcfg/netcfg.go:45-61`; `app/feed.js:143-152,239-240,291-292`; `README.md:186,275-278` | Until `Renounce` runs, the factory owner holds `SetTreasury` and an unbounded `SetRegFee`. `Renounce` exists only in the uncommitted tree: HEAD 0fba2c6, which is what is on GitHub, has no such function. The mainnet runbook has no Renounce step. No bundle ships a `treasuries` list (the struct has no field for it), so the app's rule for a verified market is the factory's live `tstr` pointer. | One `SetTreasury` marks every pool, launch, NFT and NFA as "treasury differs from the factory" in every client and redirects all registration fees (LIV-08). One `SetRegFee` stops registration for every market type (SEC-K-06). | **fixed in the tree, 2026-09-11.** Renounce is committed (`TestFactoryRenounce`) and is a runbook step (`simdeploy -renounce`, `MAINNET_DEPLOYMENT.md`). `netcfg.Network.Treasuries` ships the treasury set in `config.js` (`treasuries`); `app/feed.js:147-154` checks markets against it and falls back to `tstr` only for a bundle without one (`tools/netcfg/treasuries_test.go`). Until Renounce the owner still redirects registration fees (`factory.bas:110`). `README.md` checked: it no longer states the old rule | Commit Renounce. Add "Renounce, then read back: no `owner`, no `tmpowner`, `renounced` set" to the mainnet runbook. Add a `treasuries` field to `netcfg.Network` so the accepted set ships in the bundle. Correct `README.md:186,275-278`. |
| GAP-006 | CRITICAL | App template / contracts | `app/templates/nfa.bas` (no `owe` or `PayOwed`; last changed in 58ff186) vs `contracts/nfa.bas:393-425`; `app/config.js:24`; `app/forms.js:15`; `tests/templates_test.go:43-70` | The NFA template the app installs, and whose hash (`e1e77b6b…`) `config.js` verifies, is the pre-repair contract. The repairs for LIV-01/DVM-1 (owed shares paid by `PayOwed`), NFA-1/LIV-02/ADV-PRICE-01 (ended auctions settle their bid) and DVM-2 (charity share stored only after its check) exist only in `contracts/nfa.bas`. `TestShippedTemplatesAreTheTestedContracts` fails on this by reading. | Every NFA minted from the app today carries the prior audit's CRITICAL defect: one unregistered charity or artificer address completes a sale and destroys the seller's remainder and the protocol fee. It also carries the stranded-bid call option. | in flux (nfa.bas and app/* being edited) | Regenerate `app/templates/nfa.bas` from `contracts/nfa.bas` with `simdeploy`, re-pin the hash, revoke `e1e77b6b…` from every record, and run the suite. |
| GAP-007 | CRITICAL | xmrswap configuration | `tools/xmrswap/cmd/xmrswap/main.go:7-8,17,47,53,109-119`; `tools/xmrswap/swap.go:1572-1577` | The companion asks the DERO daemon which network it is on only to decide whether the timelock minimums may be lowered. It never requires the DERO network and the Monero network (`-net`, default `stagenet`) to agree. The offer compares `-net` only between the two parties. | A maker (Bob) on DERO mainnet running the usage line's `-net stagenet` with a stagenet wallet-rpc releases `k_b` against worthless stagenet XMR, and the taker claims his real DERO. In the other direction, a taker with `-net mainnet` against the default `-daemon` (the simulator port) locks real XMR against test DERO. | in flux | Refuse to run unless (DERO is mainnet) equals (Monero is mainnet). Give `-net`, `-daemon` and `-dero` no defaults. Put the DERO network in the offer terms. |
| GAP-008 | CRITICAL | Wallet authentication | `tools/xmrswap/dero.go` and `tools/zecswap/dero.go` (neither sets basic auth or an Authorization header anywhere); `vendor/derohe/walletapi/rpcserver/rpc_websocket_server.go:103-106,188-190,279`; `tools/netwallet/main.go:74-77` | Neither companion can send credentials to the DERO wallet RPC. A DERO wallet RPC with no `--rpc-login` authenticates nobody, and its `/ws` upgrader accepts every Origin. The wallet the mainnet runbook creates requires a login, so a companion pointed at it gets 401. The configuration the companion works with is the unauthenticated one. | While a swap runs, which takes hours, any web page the user opens, including any TELA app, can open `ws://127.0.0.1:<port>/ws` and call `transfer` with no prompt: the user's whole DERO and asset balance. Precondition: an unauthenticated wallet RPC, which is the only configuration the companions document. Not tested: whether a given browser's local-network restrictions block the socket. | in flux | Add `-dero-login` (or an environment variable) using `SetBasicAuth` in both clients, and refuse mainnet without it. Document starting the wallet RPC with `--rpc-login`. |
| GAP-009 | HIGH | Monero wallet authentication | `tools/xmrswap/cmd/xmrswap/main.go:17` | The usage text starts `monero-wallet-rpc … --disable-rpc-login` for the wallet that will hold the joint spend key and can `transfer`, `sweep_all` and `generate_from_keys`. | Any local process, and possibly a browser page (not tested; Monero is not vendored), can spend from it or read the joint key. | in flux | Show `--rpc-login` in the usage text, as `docs/MAINNET_TEST_PLAN.md` does. Refuse mainnet when the first call gets no digest challenge. |
| GAP-010 | HIGH | Node trust | `tools/xmrswap/cmd/xmrswap/main.go:92-98`; `tools/xmrswap/swap.go:712-750`; `tools/xmrswap/monero.go:365-386` | The remote-node warning checks only the wallet-rpc URL. A loopback wallet-rpc whose `--daemon-address` is a public monerod passes silently. Bob's gate counts what that wallet reports, and the view key comes from Alice. A non-loopback `http://` wallet-rpc receives the joint spend key in cleartext with only a warning. | A monerod that Alice controls or colludes with shows Bob's view wallet a lock that does not exist. Bob releases `k_b`, Alice claims, and Bob loses the DERO. `docs/XMR_SWAP.md` R18 names the warning as the mitigation. | in flux | Add a `-monerod` flag, require it to be loopback on mainnet, and refuse non-loopback plain HTTP on mainnet. |
| GAP-011 | HIGH | Contract pinning | `tools/zecswap/dero.go:147` (`NewDeroRPC(daemon, wallet, board, escrow)`); no code-hash check anywhere in `tools/zecswap` | Unlike xmrswap (`pin.go:13`, `swap.go:484-495`), the Zcash companion reads the offer and the escrow from whatever board and escrow ids it is given, without comparing their code with `board.bas` / `swap.bas`. | A taker pointed at a look-alike escrow whose storage passes every read but which has an owner exit funds ZEC. The maker claims the ZEC with `s` and withdraws the DERO through the extra exit. | in flux | Pin both code hashes the way `tools/xmrswap/pin.go` does, and refuse any other code. |
| GAP-012 | HIGH | Design docs vs code | `docs/ZEC_SWAP.md:311-312,326-327`; `tools/zecswap/node.go:26-28,577-583`; `tools/zecswap/swap.go:780,1037-1056` | Z14/Z15 say a lying node costs only time. The code shows: (1) the maker hands its claim, which carries `s`, to its Zcash node, so a colluding node gives `s` to the taker, who claims the DERO escrow and later refunds the ZEC; (2) a lying DERO node shows the taker a fake escrow, she funds, and the maker takes the ZEC. The docs also name lightwalletd, which `node.go` does not support. | Users size their risk from a trust statement that understates it to "time", when each case is a total loss of one leg. | in flux | Correct Z14/Z15 and §10. Add loopback checks for both nodes when a zecswap binary exists. |
| GAP-013 | HIGH | Supply chain | `research/crosschain/poc/proof-xmr/Cargo.toml:25-26`; `.gitignore:2`; `deploy/vendor.lock` | The patched `bulletproofs` copy the prover builds against (and the project's `bp-pallas` port) are gitignored path dependencies. `Cargo.lock` gives path dependencies no checksum, and neither `vendor.lock` nor `verifyvendor` covers them. | A fresh clone cannot rebuild the prover the companion relies on, and a tampered local copy goes unnoticed. Today's copy differs from crates.io only by a 3-line fix (`src/r1cs/proof.rs:180-182`), checked by the sub-audit. | open | Commit the patch as a file, lock the crate checksum and the patch hash, and check both in `verifyvendor`. |
| GAP-014 | HIGH | Release integrity | `tools/xmrswap/pin.go:10-12`; `docs/BACKLOG.md:71-73` (BL-14); every binary's build info shows `vcs.modified=true` | The companion's defences (the contract pin, policy floors, statement checks) live in a binary that nobody builds reproducibly or signs. | A user who runs a companion built by someone else trusts that builder with their key share and both exits, and cannot check it. | open | Reproducible builds with published hashes and signatures (BL-14) before any mainnet user. |
| GAP-015 | MEDIUM | Supply chain | `deploy/binaries.lock:22`; govulncheck output (§5) | Everything is built with go1.26.2. Reachable standard-library vulnerabilities: 10 in the tools (e.g. GO-2026-6218, -6090), 10 in `derod.exe`, 7 in the wallets. `telainstall.exe` also carries GO-2026-6278 (gorilla/websocket v1.5.0 weak mask PRNG). | Denial of service in TLS / HTTP/2 / URL parsing on the user's node and tools. Weak WebSocket masking in the installer. | open | Rebuild with go ≥ 1.26.6, gorilla/websocket ≥ 1.5.3, and current x/crypto and x/net in tools and tela, then re-pin. |
| GAP-016 | MEDIUM | Supply chain / process | `tools/verifyvendor/main.go:48,106-117,186,206-212`; `deploy/README.md:17-19,206`; no `.github/` or any CI configuration | There is no CI at all. `verifyvendor` checks the commit and the patch diffs, but pins no tree hash, never contacts upstream, skips `build.sh` and non-`.exe` files, runs only by hand, and `-write-binaries` re-pins whatever is on disk. The bootstrap clones upstream default branches rather than the pinned commits. Related to SEC-K-07. | A modified vendored tree or binary reaches a release as long as nobody runs the gate, or it is re-pinned. | open | Clone by commit, pin `HEAD^{tree}`, hash every file in `vendor/bin`, and run `verifyvendor` and `govulncheck` in CI on every push. |
| GAP-017 | MEDIUM | Supply chain | `tools/telainstall/build.sh:9`; `vendor/tela/go.mod` (replace line) | `telainstall`, which opens the mainnet owner wallet, builds with `-mod=mod` against `civilware/derohe fa76d6016cc6` fetched from the module proxy. That commit appears nowhere in `vendor.lock`. | The wallet-handling code in the installer binary comes from outside the locked tree. | **fixed 2026-09-11.** `tools/telainstall/build.sh` builds `-mod=readonly` after `verifyvendor -modules-only`. `vendor.lock` `modules` pins the replace, the fork's commit and its go.sum hashes. verifyvendor also checks the module-cache copies and the build info of telainstall and tela-cli | Lock the replaced module and its go.sum hash, and build with `-mod=readonly`. |
| GAP-018 | MEDIUM | Supply chain | `vendor/derohe` (GOPATH vendor, no `modules.txt`); `derod.exe` build info with 0 dependency lines; `vendor/derohe/vendor/github.com/gorilla/websocket/conn.go:13,183` | derohe's third-party code (x/crypto from about late 2021; WebSocket masking with `math/rand`) is invisible to govulncheck. | Known vulnerabilities in the node and wallets cannot be found by tooling. | **inventoried 2026-09-11.** `docs/DEPENDENCIES.md`: 52 modules, each version recovered from the vendored source and checked against vuln.go.dev and osv.dev. Findings D-1…D-8: D-1 is closed by the go1.26.8 rebuild, D-2 is accepted with a reason, D-6 is fixed, and the rest are watch items | Keep an inventory of derohe's vendored versions and review it against the Go vulnerability database before mainnet. |
| GAP-019 | MEDIUM | xmrswap rendezvous | `tools/xmrswap/channel.go:49-168`; `tools/xmrswap/swap.go:359-386,402-425,1216-1261` | The channel is a shared directory with no authentication and no encryption. The first `alice_setup` written wins. | A sync provider or eavesdropper learns the private view key `v`, S_a, S_b and the Monero txid. Anyone who can write to the directory impersonates the taker: Bob locks and pays the stipend (≈0.14 DERO, lost) to someone who never locks XMR, and his DERO waits until t1. | in flux | Pin the peer's identity out of band, and sign and encrypt message bodies (or ship the planned transport). |
| GAP-020 | MEDIUM | Secrets at rest | `tools/xmrswap/state.go:36-44,64-65,200-210`; `tools/xmrswap/monero.go:372-386`; `tools/xmrswap/swap.go:806-815`; `tools/zecswap/swap.go:631` | Companion state files hold key shares, `k_b`, `v`, and after exit the peer's revealed share (together the joint spend key); zecswap holds the maker's `s`. All are cleartext JSON. Mode 0600 is not enforced on Windows (the program warns). The joint wallet is still created with an empty password and only closed after the sweep: SEC-K-09's "repaired" status is overstated. | Local malware or another account on the machine reads the keys. In `sweep_unknown` the coins remain under that key. | in flux | Encrypt the state with a passphrase, set a restrictive ACL on Windows, give the joint wallets a password, and remove them after the sweep. |
| GAP-021 | MEDIUM | zecswap refund | `tools/zecswap/swap.go:1211-1222`; `tools/zecswap/sighash.go:56-62`; `tools/zecswap/node.go:54-57` | The check that no network upgrade falls inside the refund window reads only the upgrades the node reports as pending, not the build's own schedule. | An out-of-date zcashd passes the check, the pre-signed refund is signed under the old branch id, and it becomes invalid at activation. | in flux | Also check the built-in schedule. |
| GAP-022 | MEDIUM | Accounting / tests | `vendor/derohe/dvm/sc.go:392-395`; `vendor/derohe/dvm/simulator.go:203-216`; tests in §8 | The chain keeps no balance for a contract's own asset: "an scid can mint its assets infinitely". Every own-token supply — pool LP, launch token, founders pass, holder NFT, NFA unit, wrapped units — is bounded only by that contract's arithmetic. Conservation of own-token supply is asserted only for nfa (`tests/nfa_test.go:137-143`) and vault (`tests/vault_fuzz_test.go:14-18,53-54`). | A bug in pool LP accounting (`lp`) or in launch circulation (`sold`, `Lq`, `locked`) mints value from nothing, and no test would see it. | open | Add model-based assertions: circulating LP == `lp` − 1000; launch circulating == alloc + sold (phase 0) and the graduated equivalent; founders units == `hst` + Σ `lu_`. |
| GAP-023 | LOW | htlc.bas | `contracts/htlc.bas:34,80,83`; `docs/ATOMIC_SWAP_STUDY.md:674` | `Lock` checks only that `ha`, `hk`, `hb` are 64 characters, not that they are canonical lower-case hex, whereas `swap.bas:41` and `board.bas:45` do and the study required it for v2. | An upper-case `hk` or `ha` makes Claim impossible; Refund and Punish still exit, so there is delay, not loss. xmrswap refuses such hashes (`tools/xmrswap/swap.go:437`), but other clients may not. | in flux | Add `HEX(HEXDECODE(x)) != x` for all three before the stores. |
| GAP-024 | LOW | swap.bas | `contracts/swap.bas:20,37-40,81-83` | A lock may name no taker, and then Claim pays whoever sends it. | Once the maker's claim reveals `s` on Zcash, the first observer to send Claim takes the DERO. zecswap always names the taker (`tools/zecswap/swap.go:427-431`); a hand-made lock does not have to. | in flux | Require a taker, as `htlc.bas:37` does. |
| GAP-025 | LOW | swap.bas | `contracts/swap.bas:36`; `docs/ATOMIC_SWAP_STUDY.md:482` (Z-D, "adopt") | No minimum escrow width: any expiry above height + 1 is accepted. | A short-expiry lock is refused only by the taker's companion policy. | in flux | Z-D: refuse an expiry below height + W_min. |
| GAP-026 | LOW | board.bas | `contracts/board.bas:67-83,88-100` | Any address can Accept any open offer and hold it for `aw` = 200 blocks. The maker's only remedy is Cancel and a new Post, which the same address can Accept again. Residual of ADV-PRICE-07. | Cheap, repeatable denial of service on the ZEC market's order flow. | in flux | Accept bond or maker allow-list, or accept that the board is advisory and say so. |
| GAP-027 | LOW | Stray value | `contracts/social.bas:15-65`; `contracts/board.bas:39,70,91`; `contracts/htlc.bas:72,101,128`; `contracts/swap.bas:73,99`; `contracts/nft.bas:86-123` | `social.bas` checks neither DERO nor assets anywhere. `board.bas` refuses DERO but not assets. The htlc/swap exits refuse DERO but not other assets. `nft.bas` Unlock, ClaimCreator and ClaimFees accept DERO. None of these contracts has a way to return such value. NFA-4 covers nfa.bas only. | Value attached by mistake is kept for ever, against the fail-closed house rule. | open / in flux | Refuse any attached value the entrypoint does not use. |
| GAP-028 | LOW | Secrets hygiene | `.gitignore:36-37` | Only `bob.json` and `alice.json` are ignored. zecswap's `maker.json`, `taker.json`, `*.refund.txt` and `zecswap-used.json`, and any custom xmrswap `-state` name, are not. | A maker's secret `s` or a key share is committed and pushed by `git add -A`. | in flux | Ignore every companion state and output pattern; better, write state outside the repository by default. |
| GAP-029 | LOW | Secrets hygiene | `tools/zecswap/cmd/zecswap/main.go:47` | `zecswap script -secret` takes the maker's secret on the command line. | The secret is visible in the process list and shell history. | in flux | Read it from stdin or a file, as `proof.go` does for the prover. |
| GAP-030 | LOW | Local secrets | `.testnet/{deployer,owner,treasury}.seed` and `.db` (ignored, `.gitignore:5,12,14,15`); `tools/netwallet/main.go:98,105-107`; `deploy/README.md:114` | Three plaintext 25-word DERO seeds and empty-password wallets. The Windows ACL grants Users and CodexSandboxUsers modify rights. The same keys control the `dero1…` mainnet form of those addresses. | Any local account takes the testnet deployer, owner and treasury, and any mainnet funds ever sent to their `dero1` forms. | open | Delete or move the seed files, and never send mainnet value to a key that was used with `-write-seed`. |
| GAP-031 | LOW | Repository | `origin/main:testnet/signer-3000{3,4}.json` (added in 5622e78); commits 5622e78, c872260, d68094d, 5cb8d1b (binaries) | Simulator vault-signer state (no key material) is on GitHub, and its deletion is staged but not committed. Five unsigned binaries remain in history (SEC-K-12). | Stale operational data and binaries with no provenance ship with the public tree. | open | Commit the deletion. A history rewrite is the operator's decision. |
| GAP-032 | LOW | Deployment tooling | `tools/telainstall/main.go:79-80,88-114`; `deploy/networks.json:17` vs `deploy/README.md:279` | `telainstall` does not check that the bundle's `config.js` or manifest names the `-network` it installs to. The default dURL `dmz.tela` differs from the record's `veil.tela`. | `-network mainnet -yes` with the default `-app ../app` installs the simulator bundle and wastes the DOC fees (the app then refuses to sign, `app/app.js:40-43`). | open | Require `-app dist/<network>` and check `manifest.json`. Settle one dURL. |
| GAP-033 | LOW | Tests | `tests/htlc_test.go:87-99,121,402`; `tests/swap_v2_test.go:80,106,171,204`; `tests/registry_test.go:258-262`; `tests/founders_test.go:33-39` | htlc and swap balances are checked per scenario, never as "balance == Σ `v_i` of open locks" over random sequences. nft.bas has no invariant check (DERO == `fd` + `cacc`, token == Σ `lk_`). founders computes the listed units and discards them (`_ = listed`). | A conservation bug on an unscripted path goes unseen. | open | Add randomized conservation tests (§8). |
| GAP-034 | LOW | Build hygiene | `tools/go.mod:7`; `tests/harness/chain.go:10-15` | secp256k1 is marked `// indirect` but `tools/zecswap/sign.go` imports it directly. The root module and several tools import derohe with no `require`, so they build only inside `tests/run.sh`'s copy and no scanner can analyse them. | Dependency reports are incomplete. | open | Declare the dependencies, and give the test harness a real module path. |
| GAP-035 | INFORMATIONAL | nfa.bas fee recipient | `contracts/nfa.bas:85,112,376`; `app/forms.js:208,251` | The artificer share is paid to Civilware's hard-coded mainnet address (the ART-NFA-MS1 standard's). The installer chooses the share (0–100 %); the app's default is 0. | A third party is a fee recipient on any NFA whose creator opts in. | open | Operator to confirm it is intended; the app already names Civilware. |
| GAP-036 | INFORMATIONAL | TELA serving | `vendor/tela/tela.go:749-752,1404-1410`; `vendor/engram/layouts.go:16601-16609`; `deploy/tela-deployment.json:5` | TELA hosts refuse, by default, to serve an INDEX whose `hash` is no longer its SCID (i.e. one that has been updated). Engram serves it after the user accepts updated content. The simulator INDEX is at commit 14. | A publisher update is both a code-replacement power and an availability lever: default `tela` hosts stop serving the app. | open | Covered by GAP-001: releases as new ownerless INDEXes. |
| GAP-037 | INFORMATIONAL | Test code in production package | `tools/xmrswap/fakewallet.go:32,85` | `FakeWallet` is exported from the production package; only `tools/simhtlc/main.go:159` uses it. | None today; a future caller could wire it. | in flux | Move it to a `_test` file or an internal test package. |
| GAP-038 | INFORMATIONAL | Documentation drift | `README.md:183,186,275-278`; `docs/XMR_SWAP.md:477-479`; `docs/ZEC_SWAP.md` §8; `tests/nfa_entrypoint_test.go:3-6` | README and XMR_SWAP still say only the taker can Punish (htlc v2 lets anyone, `htlc.bas:122-141`) and that the factory cannot be renounced (`factory.bas:75-83`). ZEC_SWAP §8 describes swap screens the app does not have. The test comment says the DVM has no private functions, which is false: `dvm/dvm.go:342-370` refuses any external entrypoint not starting with an upper-case letter; the tests assert that refusal. | Readers act on superseded statements. | in flux | Update when the contracts are committed. |
| GAP-039 | INFORMATIONAL | zecswap node trust | `tools/zecswap/node.go:130-134` | ZIP 244 Sapling digests are not implemented; v5 transactions with Sapling parts are identified by the node's word. The secret is still checked against its hash. | A lying node could misattribute a txid; it cannot forge a valid preimage. | in flux | Implement the Sapling digest or refuse Sapling-bearing spends. |

### Prior findings: a verification sample (2026-09-10 audit)

| ID | Stated status | Checked at | Result |
|---|---|---|---|
| XMR-1 | repaired `59a5851` | `tools/vaultsigner/main.go:127-139` | Holds: `-backend` has no default, `sim` is refused off the simulator, and real backends exit. |
| ZEC-1 | repaired `3175401` | `tools/zecswap/script.go:66-72` | Holds: `OP_IF OP_SIZE <32> OP_EQUALVERIFY OP_SHA256 …`. |
| ZEC-2 | repaired `3175401`, `cffdd29` | `contracts/swap.bas:41`, `contracts/board.bas:45` | Holds for swap and board; **htlc.bas has no such check** (GAP-023). |
| ZEC-3 | repaired `3175401` | `contracts/board.bas:74` | Holds (`cltv >= 500000000` refused). |
| XMR-02 | repaired `9064f82` | `tools/xmrswap/swap.go:955-964` | Holds (a resumed or retried lock waits `LockSettleBlocks`). |
| XMR-4 | repaired `9064f82` | `tools/xmrswap/cmd/xmrswap/main.go:268-276` | Holds for wallet versus `-net`; the DERO network is not compared (GAP-007). |
| SEC-K-09 | repaired `9064f82` | `tools/xmrswap/monero.go:372-386`, `swap.go:806-815` | **Partial**: the wallet is closed after the sweep, but the file is still written with an empty password and stays on disk. |
| UX-02 | repaired `59a5851` | `deploy/README.md:241-256` | Holds (explicit `-pass-descr`). |
| LIV-07, SEC-K-02, LIV-08, ADV-PRICE-07, LIV-01, DVM-1, DVM-2, NFA-1, LIV-02, ADV-PRICE-01 | no status line | `htlc.bas:122-141`, `factory.bas:75-83`, `board.bas:80,96`, `nfa.bas:164-171,213-215,393-425` | Addressed only in the uncommitted tree (in flux). HEAD / GitHub still carry the defects, and the NFA template the app installs still does (GAP-006). |
| SEC-K-06, SEC-K-03, FE-6 | no status line | `factory.bas:44-50`; `telainstall/main.go:249` | Still open (GAP-005, GAP-001). |

---

## 1. Privileged-capability scan

### 1.1 The DVM facts every row depends on

- `SIGNER()` returns the 33-byte key only when a payload's ring size is 2. Otherwise the transaction runs with a zero
  signer: `vendor/derohe/blockchain/transaction_execute.go:295-299` (anonymous calls allowed, "SC receives signer as
  all zeroes") and `:424-442` (`Extract_signer` succeeds only at `RingSize == 2`). `dvm_signer` returns that value
  (`vendor/derohe/dvm/dvm_functions.go:307-310`).
- `IS_ADDRESS_VALID` is a BN254 point decompression (`dvm_functions.go:324-340` → `cryptography/bn256/changes.go:215-225`).
  33 zero bytes means x = 0, which needs y² = 3 mod p, and 3 is not a quadratic residue mod the BN254 prime (computed:
  3^((p−1)/2) ≢ 1). So `xToY` fails (`changes.go:65-74`) and `IS_ADDRESS_VALID(SIGNER())` is 0 above ring 2. Every
  `IF IS_ADDRESS_VALID(s) == 0` and every `s != LOAD("…")` comparison below fails closed for anonymous callers.
- Only functions whose first letter is an upper-case ASCII letter can be invoked from a transaction
  (`vendor/derohe/dvm/dvm.go:342-370`, reached from `dvm/sc.go:262`). Internal calls use `runSmartContract_internal`
  (`dvm.go:931`). So `owe`, `settle`, `distribute`, `remove`, `transferOwnership`, `processDEROFinalPayment`, `init`,
  `buyto`, `sellto`, `refundto` and the like are not entrypoints. `tests/nfa_entrypoint_test.go:16-115` asserts the
  refusals, despite its header comment.
- A contract mints its own asset without limit: `StoreSCValue` drops the write of the contract's own balance
  (`dvm/sc.go:392-395`), and `SanityCheckExternalTransfers` skips the overflow and underflow checks for it
  (`dvm/simulator.go:203-216`).
- `UPDATE_SC_CODE(code)` overwrites the contract's key `"C"` (`dvm_functions.go:312-322`), which is the code `ReadSC`
  executes (`dvm/sc.go:79-81,296-310`). No DMZ contract calls it. Only TELA-INDEX-1 does.
- There are no cross-contract calls, so a contract's code can be changed only by itself.

### 1.2 Every role-gated path

"Owner of the unit" (NFA) is a property right that moves with the asset, not an administrative role; it is listed
for completeness.

| Contract · function | Who can call | What it can change | Steal funds? | Mint? | Freeze users? | Modify code? | Change settlement destination or fee recipient? | Ownership recover / transfer | Renounce |
|---|---|---|---|---|---|---|---|---|---|
| factory · `Initialize` (11-30) | first caller once (`initd`, 12); must be a ring-2 signer (14-15) | sets `owner`, `treasury`, `regfee` | no | no | no | no | sets them | — | — |
| factory · `SetTreasury` (32-42) | owner | `treasury`, `tstr` | no user funds held; redirects every later registration fee | no | not on chain; and not in the app for a bundle that ships `treasuries` (GAP-005): markets are checked against that set, and against `tstr` only when a bundle ships none (`app/feed.js:147-154`) | no | **yes** (the fee recipient; the app's verification rule only for a bundle without `treasuries`) | none: transfer of ownership was removed (SEC-K-02) | `Renounce`, committed |
| factory · `SetRegFee` (44-50) | owner | `regfee`, no bound | no | no | **yes**: registration of every kind stops (SEC-K-06) | no | **yes** (fee amount) | as above | as above |
| factory · `TransferOwnership` / `ClaimOwnership` | owner / pending owner | `tmpowner` → `owner` | no | no | no | no | indirectly | a transfer completes only when the new key signs, so a burn address can never claim | — |
| factory · `Renounce` (75-83) | owner | deletes `owner`, `tmpowner`; stores `renounced` | no | no | no | no | ends both setters for ever | none afterwards | **this is it** (in flux) |
| vault · `SetLimits` (101-111), `AddSigner` (153-170), `Seal` (242-260) | owner, only while `sealed == 0` | limits; the signer set; sealing | chooses the signers who will custody everything | indirectly | no | no | no | `owner` is never deleted, but no function reads it after Seal | Seal is the renounce |
| vault · `Mint` (313-350) | k active signers | `sup`; sends units to `ua_<j>` | **yes**: k signers mint against a deposit that never happened | **yes**, up to `cap`, with `cap/capratio ≤ bondtot` (329-330) | no | no | no | — | — |
| vault · `Attest` (288-307) | k signers | `da_<j>`, the foreign address a depositor pays | **yes**: the quorum names an address it controls (the custody model) | no | no | no | **yes** (deposit destination) | — | — |
| vault · `Released` (406-427) | k signers | marks withdrawal j paid; removes its claim from `sup` | **yes**: marking a withdrawal paid without paying confiscates it | no | yes | no | no | — | — |
| vault · `Veto` (451-468), `Refunded` (353-370) | k signers | returns burned units; records a foreign refund | no | re-issues units already counted in `sup` | no | no | no | — | — |
| vault · `Paying` (438-448) | **any one** signer | `w_<j>_p` | no | no | **yes**: blocks `Reclaim` for that withdrawal for ever and `Veto` for 2000 blocks (LIV-03) | no | no | — | — |
| vault · `Halt` (503-521) | k signers | a 20000-block lease, renewable | no | no | pauses Register, Attest, Mint (not Burn, Released, Reclaim) | no | no | — | — |
| vault · `SetCap` (524-539), `Tighten` (114-133), `SetViewKey` (136-150), `AddSigner2` (174-197), `Slash` (543-571) | k signers | cap (bounded by bonds); tighter limits; published view key; a new signer; slashing a bond to the treasury (565) | Slash moves a signer's bond to the treasury | SetCap raises mint headroom | Tighten can lower `winmax` to `mindep` | no | no | the signer set changes by k votes | — |
| vault · `ClaimFees` (487-498) | the treasury key, ring 2 | `feepool` units to the treasury | no | no | no | no | no (treasury fixed at install) | treasury fixed | no |
| nfa · `init` (81-117) via `InitializePrivate` (58-77) | install only (`EXISTS("owner")`, 59, 82) | `owner` = `creatorAddr` = installer (83-84); `artificerAddr` = Civilware's hard-coded address (85); `quote`, `protoFee`, `treasury` fixed | no | one unit, once (114) | no | no | fixes every fee recipient for ever | — | — |
| nfa · `Update` (130-146) | `creatorAddr` for ever; also `owner` if `ownerCanUpdate` = 1 | `iconURLHdr`, `coverURL`, `fileURL`, `fileSignURL`, `tagsHdr` | no | no | no | changes what the asset shows (NFA-5, UX-13) | no | `creatorAddr` cannot change | **no** |
| nfa · `Start`, `CloseListing`, `CancelListing` (154-290); `ClaimOwnership` (119-128) | owner of the unit | the listing; `owner` follows the unit | no | no | no | no | a per-listing charity address (the seller's own choice) | follows the unit | n/a |
| nft · `ClaimCreator` (100-110) | creator, ring 2 | pays `cacc` to the creator | only the creator's own accrued share | no | no | no | no | creator fixed | no |
| launch · `InitializePrivate` (6-73) | installer | mints `alloc = bps(supply, cbps)`, `cbps ≤ 500` (26, 40, 90-91) to the installer | no | once, ≤ 5 % | no | no | fixes treasury, fee, pfee, tax | — | — |
| launch · `ClaimTax` (266-277) | creator, `tmode` 1 | pays `tacc` to the creator | only accrued tax (`tax ≤ 1000` bps, 26) | no | no | no | no | creator fixed | no |
| founders · `InitializePrivate` (12-55) | installer | mints `reserve ≤ maxsupply/5` (18) to the installer (51-52); fixes `treasury` (mint proceeds, 71) and `share` | no | once | no | no | fixed at install | — | — |
| founders · (no gated function after install) | — | holders are paid only what someone `Deposit`s (137-146); `share` is a stored number nothing enforces (header 2-3) | the treasury can withhold the promised share for ever | no | no | no | no | — | — |
| pool, htlc, swap, board, chat, social (all functions) | — | no role gate; pool's (`pool.bas:27`) and chat's (`chat.bas:32`) treasury is fixed at install; `ClaimFees` is permissionless and pays only that treasury (`pool.bas:247-262`, `launch.bas:248-263`, `nft.bas:113-123`) | no | pool LP by formula only | no | no | no | — | — |
| TELA-INDEX-1 · `UpdateCode` (52-63) | `owner`, unless `owner == "anon"` (53, 55) | the contract's entire code, and `mods` | yes, for any DERO a later-injected MOD holds (`TXDWD WithdrawDero` pays the owner); for the app, any code the wallet then signs | no | can make the app unservable (GAP-036) | **yes** | **yes** (whatever the new code says) | only by rewriting its own code to include the `TXTO` MOD (two-step) | none in the template; only an "anon" install is final |
| TELA-INDEX-1 / DOC-1 · `Rate` | any ring-2 signer, once each | `likes`/`dislikes` and one key per rater | no | no | no | no | no | — | — |
| TELA-DOC-1 (all) | — | `owner` is stored (`TELA-DOC-1.bas:21`) and read by no function | no | no | no | no (no `UpdateCode`) | no | — | — |

Re-initialisation is refused everywhere: `initd` guards (`htlc.bas:17`, `swap.bas:11`, `board.bas:20`, `factory.bas:12`,
`pool.bas:16`, `launch.bas:7`, `founders.bas:13`, `nft.bas:8`, `chat.bas:15`, `social.bas:7`, `vault.bas:54`); nfa and
TELA by `EXISTS("owner")`.

### 1.3 Flags

- **Arbitrary mint:** the vault quorum (`Mint`). Install-time mints to the installer: founders reserve ≤ 20 %,
  launch allocation ≤ 5 %, one NFA unit.
- **Burn:** none arbitrary.
- **Withdrawal / confiscation:** vault quorum (`Released` without paying; `Attest` of its own address); one signer
  freezing `Reclaim` (`Paying`); a TELA INDEX owner through an injected MOD.
- **Destination or fee change:** factory `SetTreasury` / `SetRegFee` until `Renounce`; vault `Attest`.
- **Verifier change:** factory `SetTreasury` changes the app's verification rule only for a bundle that ships no `treasuries` set (GAP-005; every bundle `simdeploy` records ships one). The TELA INDEX owner
  changes the verifier itself, since the app holds the template hashes (GAP-001).
- **Upgrade:** TELA-INDEX-1 `UpdateCode` only.
- **Pause:** vault `Halt`; factory `SetRegFee` is a de facto pause of registration.
- **Ownership transfer:** factory (two-step); TELA INDEX only through code replacement.
- **Standing off-chain promise:** founders `share` depends on the treasury choosing to deposit.

---

## 2. TELA ownership and immutability, from source

### 2.1 What "owner" means

- TELA-INDEX-1 and TELA-DOC-1 are "private" contracts: they have `InitializePrivate`, so the node calls it at install
  (`vendor/derohe/blockchain/transaction_execute.go:324-338`). `init()` stores `owner = address()`
  (`TELA-INDEX-1.bas:18-21`, `TELA-DOC-1.bas:18-21`).
- `address()` is `ADDRESS_STRING(SIGNER())` when `IS_ADDRESS_VALID(SIGNER())`, else the literal `"anon"`
  (`TELA-INDEX-1.bas:31-37`, `TELA-DOC-1.bas:29-35`).
- Ownership is used by exactly one function in the base templates: INDEX `UpdateCode`.

### 2.2 Whether `UpdateCode` stays callable, and ring size > 2

- `UpdateCode` refuses when `owner == "anon"` (line 53) or `code == ""` (54). Otherwise it runs only if
  `owner == address()` (55), then `UPDATE_SC_CODE(code)` (57), bumps `commit`, stores `STORE(<commit>, HEX(TXID()))`
  and the new `hash`, and overwrites `mods` (58-61).
- For an INDEX installed at ring 2 it is callable by that key for ever. No template function removes it, deletes
  `owner` or sets it to `"anon"`. A replacement code without `UpdateCode` would no longer match any TELA-INDEX version
  (`vendor/tela/parse.go:942-954`), and hosts refuse to clone an INDEX that does not match
  (`vendor/tela/tela.go:1021-1026`). **So a ring-2 INDEX cannot be made final and remain a TELA app.**
- For an INDEX installed above ring 2: the signer is 33 zero bytes (§1.1), `IS_ADDRESS_VALID` is 0, `address()` is
  `"anon"`, and `owner = "anon"` is stored at install. From then on `UpdateCode` returns 1 at line 53 for every
  caller, at every ring size, and `init()` refuses a second run because `owner` exists (line 19).
- The `TXTO` MOD also refuses `"anon"` (`vendor/tela/TELA-MOD-1/tx/TELA-MOD-1-TXTO.bas`, BASIC line 10 of both
  TransferOwnership and ClaimOwnership), and
  a MOD can only arrive through `UpdateCode`. **An anon INDEX is immutable: code, DOC list, headers and mods.**
- Its `Rate` still works for ring-2 raters, which affects only the like counters.
- Not tested in this repository: an actual install above ring 2. One `tela.Installer(w, 16, idx)` on the simulator
  followed by `DERO.GetSC` showing `owner` = `"anon"` would settle it.

### 2.3 DOC contracts

TELA-DOC-1 has no `UpdateCode` and no code-writing function (`TELA-DOC-1.bas:4-52`). It is immutable at any ring
size, and its stored `owner` is inert.

### 2.4 Burn addresses, two-step transfer, hidden variables

- **Burn addresses:** nothing recognises a burn address as ownerless. The only ownerless marker in TELA is the
  literal `"anon"` set by `address()`. DMZ's factory expresses ownerlessness by deleting the key
  (`factory.bas:75-83`). The factory's `ClaimOwnership` (63-70) and TELA's `TXTO ClaimOwnership` require the new key
  to sign, so a transfer to a burn address can never complete.
- **Two-step transfer:** none in the base INDEX or DOC. It exists only as the optional `TXTO` MOD, and in
  `factory.bas`.
- **Hidden privileged state:**
  - INDEX variables are the headers, `dURL`, `mods`, `DOC1..n`, `owner`, `telaVersion`, `commit`, `<n>` → commit
    txids, `hash`, the like counters and one key per rater (`TELA-INDEX-1.bas:7-12,21-27,44`).
  - The owner-only variable-store functions (`tela.go:1745`) exist only when a `vs…` MOD is present.
  - DMZ passes no MODs: `telainstall` builds `tela.INDEX{DURL, DOCs, Headers}` with no `Mods` (`main.go:243`), and
    `NewInstallArgs` injects none when `Mods == ""` (`tela.go:1467-1474`).

### 2.5 How DMZ installs

| Tool | What it installs | Ring size | Resulting owner |
|---|---|---|---|
| `tools/telainstall/main.go:232` (`tela.Installer(w, 2, doc)`), `:65` (the size-based-fee fallback, `TransferPayload0(nil, 2, …)`) | every DOC | 2 | deployer's address (inert) |
| `tools/telainstall/main.go:249` (`tela.Installer(w, 2, idx)`) | the INDEX | 2 | **deployer's address — updatable** |
| `tools/telainstall/main.go:259` → `vendor/tela/tela.go:1586-1600` (`transfer0(wallet, 2, args)`) | INDEX update | 2 | unchanged |
| `deploy/testnet-tela.sh:13`; `deploy/README.md:279` (mainnet) | `telainstall`, as above | 2 | as above |
| `tools/simdeploy` via `tools/simrpc/simrpc.go:20,225,348-351` | factory, social, founders, chat | 2 (`RingSize` 0 means 2; simdeploy never sets it) | factory `owner` = deployer; others ownerless |
| `tools/publish.sh` | nothing on chain (GitHub publish) | — | — |

`deploy/README.md:332-333` states the choice outright: "installs the INDEX with **ringsize 2 (updatable, public owner)**".

### 2.6 Serving behaviour

`tela.AllowUpdates` defaults to false, and a clone of an INDEX whose `hash` ≠ its SCID is refused
(`vendor/tela/tela.go:749-752,1404-1410`). Engram turns updates on only after the user accepts updated content
(`vendor/engram/layouts.go:16601-16609`, `functions.go:4327-4338`). So an owner's update is visible to careful hosts,
but it is also a switch that stops default hosts from serving the app (GAP-036).

### 2.7 The final on-chain state with no administrative escape hatch, and how anyone checks it

Read every item with `DERO.GetSC {scid, code: true, variables: true}`. String values come back as hex.

| Component | Final state | Third-party check |
|---|---|---|
| App TELA INDEX | Installed above ring 2 (GAP-001); `owner` = `"anon"`, `commit` = 0, `hash` = its own SCID, `mods` = "" | Code equals TELA-INDEX-1 v1.1.0 with DMZ's headers (`tela.ValidINDEXVersion`, `parse.go:942-954`). `owner` hex is `616e6f6e`. `commit` is 0. `DOC1..DOCn` each resolve to a DOC whose code is TELA-DOC-1 plus the gzip bytes recorded in `dist/<network>/manifest.json` (`tools/genconfig/main.go:120-171`). `DOC1` is `index.html`. |
| App TELA DOCs | Immutable at any ring | Code has no `UpdateCode`; content hash as above. |
| factory.bas | `Renounce` has run (in flux) | No `owner` key, no `tmpowner` key, `renounced` present; code hash equals `Canon(contracts/factory.bas)` with `Renounce`. Registration still works (`tests/registry_test.go:361-431`). |
| pool, launch, nft, nfa markets | Nothing to renounce; parameters fixed at install | Code hash equals `app/templates/<kind>.bas` (after GAP-006 is fixed); `treasury` equals the published treasury; for an NFA, `ownerCanUpdate` = 0, and it must be stated that `creatorAddr` can still rewrite its URLs (`nfa.bas:130-146`). |
| founders.bas | Nothing to renounce | Code hash, `treasury`, `share`, `reserve`, `maxsupply` read back. The deposit of `share` remains a promise by the treasury key. |
| htlc, swap, board, chat, social | No owner exists | Code hash equals the canonical source. For htlc the companion does this itself (`tools/xmrswap/pin.go:13`); for swap and board nothing does (GAP-011). |
| vault.bas | Not deployed on mainnet (`deploy/README.md:202`; `deploy/networks.json:8-9`) | `mainnet.vault` empty. If ever deployed: `sealed` = 1, after which the owner has no power and the quorum's custody of §1.2 remains by design. |

**Conclusion.**
- An INDEX installed above ring size 2 stores `owner = "anon"`, because the DVM gives it a zero signer that
  `IS_ADDRESS_VALID` rejects, and its `UpdateCode` then refuses for ever, while every DOC is immutable at any ring.
- DMZ's tooling installs and updates the INDEX at ring 2 only, so as built the app has a publisher who can replace
  it.
- The state without an escape hatch is: an anon INDEX with `commit` 0; a renounced factory; canonical code hashes
  for every contract; no vault.

---

## 3. Secrets, including git history

**Method (sub-audit):**
- **History:** all 91 commits on every local ref, including both root commits (`60ab2e6` published; `00b70eb` local
  `master`, tagged `local-history-before-publish` and not contained in any remote-tracking ref).
- **Patterns:** PEM keys, `ghp_`, `github_pat_`, `AKIA`, `xprv`/`tprv`, WIF shapes, 64-hex strings near
  key/secret/seed/spend/view/private.
- **Mnemonics:** runs of 13+ words from DERO's English wordlist (`vendor/derohe/walletapi/mnemonics/english_mnemonics.go`;
  the scan was validated on the three `.testnet/*.seed` files).
- **Remote:** `https://github.com/NoahMoney/DMZ.git`, no embedded credential.
- **Not scanned for mnemonics:** other-language wordlists and logs over 2 MB (`.sim/sim.log`, `.testnet/derod*.log`).

| Location | What it is | Class | Tracked / in history / pushed |
|---|---|---|---|
| `testnet/signer-30003.json`, `signer-30004.json` | Identical 388-byte vault-signer vote memory: vault `b3efec69…`, one release intent to a fake `4Axmr…SimulatedChain…` destination, `liveAt`. Schema at `tools/vaultsigner/main.go:260-277`. No key. The signer's identity is the wallet it drives; ports 30003/30004 are derohe simulator wallets 3 and 4, derived from seeds hard-coded upstream (`vendor/derohe/cmd/simulator/wallets.go:41`, `simulator.go:96`). | Simulator state; the key behind it is a deterministic public simulator key | Added only in `5622e78`, an ancestor of `origin/main` and `origin/wip/…`: **pushed**. Deletion staged, not committed; now ignored (`.gitignore:32`). |
| `tools/vaultsigner-*.json` (9 files) | Same format (vaults `ab32063f`, `93013bb2`, `c32e6572`, `f23403b0`), written by `memory.save()` (`tools/vaultsigner/main.go:315-336`), mode 0600, temp + rename | Simulator state, no key | Not tracked, ignored (`.gitignore:31`), never committed |
| `.testnet/{deployer,owner,treasury}.seed` | 25-word DERO seeds from `Create_Encrypted_Wallet_Random` (`tools/netwallet/main.go:98`), written by `-write-seed` (105-107) | **Real secret** (random, not public). The same key also controls the `dero1…` mainnet address. | Ignored (`.gitignore:12,14`), never committed (GAP-030) |
| `.testnet/*.db`, `.db.bak` | Wallets with empty password (`deploy/README.md:114`, `deploy/testnet-tela.sh:13`) | Real secret, in effect unprotected | Ignored, never committed |
| `.sim/data/testnet_simulator/wallet_*.db`, `.sim/*wallet*.db`, `.sim/sim.log` | Simulator wallets and the seeds the simulator logs (`vendor/derohe/cmd/simulator/wallets.go:128`) | Deterministic simulator keys (public upstream) | Ignored, never committed |
| `vendor/derohe/cmd/simulator/wallets.go:39,41` | `genesis_seed` and 22 wallet seeds | Deterministic simulator keys | `vendor/` ignored; never in history |
| `tools/xmrswap/keys_test.go:167-168,196,200`; `monero_test.go:106-117` | Monero's published reference keys and addresses | Public test vector | Pushed |
| `tools/xmrswap/keys_test.go:343`; `research/crosschain/poc/proof-xmr/run.log:4,37`; `…/zec-t-htlc/run.log:1` | PoC scalars and a testnet HTLC secret | Throwaway PoC secret | Pushed |
| `tools/zecswap/sighash_test.go:145-150`; `tools/zecswap/testdata/zip_0244.json` | Private key 1 (the secp256k1 generator); ZIP 244 vectors | Public test vector | Tracked |
| `tests/js/vaultkeys.test.mjs`, `tests/js/vectors.json` | "keys" means contract storage keys; AMM fuzz numbers (PRNG seed 42) | Not a secret | Tracked |
| `tools/xmrswap/keys.go` | Key derivation using `crypto/rand` (`keys.go:54`) | Code, no key | Tracked |
| `tools/netwallet`, `tools/xswdwallet` | Password and `rpc-login` flags; `xswdwallet` limited to loopback test daemons (`tools/xswdwallet/guard.go:14-41`) | Placeholders only | Tracked |
| Five historic `tools/*.exe` (5622e78, c872260, d68094d, 5cb8d1b) | Go binaries with the build path `C:/Users/<user>/Desktop` | No seed, mnemonic, PEM or token found inside | Pushed (SEC-K-12) |
| `tools/publish.sh:11-24` | Takes the GitHub token from `git credential fill` and passes it on curl's command line | Not in the repo | SEC-K-15 |

**App bundle:**
- Nothing secret-like is in `app/`. No code path stores a private key, seed, HTLC secret or adaptor secret in the
  browser; signing is in the wallet over XSWD.
- Stored:
  - localStorage: `veil.settings` (`app/app.js:11,20`), `veil.trades` (`app/feed.js:437-438`), the `veil.m.*` cache
    (`feed.js:188-228`), the watchlist (`app/stats.js:155-158`), `dmz.chat.mute` (`app/chat.js:24-27`).
  - sessionStorage: `veil.endpointOk` (`app/app.js:16-17`) and `veil.xswd.id` (`app/xswd.js:12-13`), a random 32-byte
    session id for the wallet. It is not a key, but any same-origin script can read it.
- No cookies, no IndexedDB. The `viewkey` read by `app/wrap.js:40-43` is one the vault publishes on purpose.

**Companions:** see GAP-020, GAP-028 and GAP-029. Randomness is `crypto/rand` in both (`tools/xmrswap/swap.go:498-503`,
`tools/zecswap/swap.go:640`), and no secret is written to any log line that was read.

**Conclusion.**
- No real private key, seed, mnemonic or API token is in any tracked file or in any of the 91 commits, pushed or local.
  Every key-like hit in the repository is a public test vector, a throwaway PoC value, or simulator state.
- `testnet/signer-3000{3,4}.json`, which are on GitHub, and the ignored `tools/vaultsigner-*.json` hold vote memory
  only; the keys behind them are derohe's public simulator seeds.
- The live exposures are on the local disk and in the future:
  - plaintext testnet seeds whose keys also control mainnet addresses;
  - cleartext companion state;
  - `.gitignore` gaps that would let a maker's secret be committed.

---

## 4. Trust points and off-chain dependencies

- Nothing in DMZ requires a server, indexer, database, message queue, relayer, oracle, API provider, bridge signer,
  or a DNS name operated by DMZ.
- Every default endpoint is loopback, and the mainnet record's endpoints are empty (`deploy/networks.json:3-19`).
- The trust points are the user's own nodes and wallets, the file channel between two Monero parties, an external
  prover binary, and three operator keys: the TELA publisher, the factory owner until Renounce, and the treasury.

| Dependency | Why it exists | Mandatory? | Who runs it | Trust added | Availability | Privacy | Where it is called |
|---|---|---|---|---|---|---|---|
| derod, read by the app | Every screen's data: `DERO.GetHeight`, `GetInfo`, `GetSC`, `GetTransaction`, `GetGasEstimate`, `GetRandomAddress` | yes | the user; default `127.0.0.1:20000` (`app/config.js:7`), mainnet record `127.0.0.1:10102` (`deploy/networks.json:4`), or any `wss://` (`app/rpc.js:97-111`) | Decides what is displayed, including the template-hash checks the app runs on what it reads. Cannot sign. | The app falls back to the wallet's proxy | The user's IP and, through per-user reads and gas estimates, the wallet address (§7) | `app/rpc.js:69-94,124-151`; chosen in `app/app.js:47-53` |
| The wallet's own daemon, via XSWD `DERO.*` proxy | Read fallback | when no WS node answers | the user's wallet (may be remote) | as above; the app's loopback-or-wss rule does not apply on this path (`app/app.js:49,54`) | — | IP to that node | `app/xswd.js:117` |
| XSWD wallet, `ws://localhost:44326/xswd` | All signing | yes, to transact | the user | Key custody. Methods used: `GetAddress` (`xswd.js:65`), `GetDaemon` (66), `GetBalance` (113), `transfer` at ring 2, or 16 in private mode (`xswd.js:115`, `app.js:153,197`), `Subscribe` (`app.js:92`). `DERO.*` passes without prompts. No explicit permission list is requested. | nothing is signed without it | the wallet sees the app name, url and session id (`xswd.js:48`) | `app/xswd.js:30-81` |
| TELA host plus **the INDEX publisher** | Serves the app from DOCs | yes | the user runs the host; **the operator holds the INDEX key** | Can replace the app (GAP-001) | can stop default hosts (GAP-036) | — | `tools/telainstall/main.go:249,259` |
| **Factory owner** until `Renounce` | Registry parameters | — | operator | Treasury pointer and fee (GAP-005) | registration | — | `contracts/factory.bas:32-50` |
| **Treasury key** | Receives fees; deposits the Founders share; the vault's `ClaimFees` | payouts only | operator (`tools/treasurybot/main.go:63-197`; no RPC auth, SEC-K-08) | Decides whether holders are paid at all (`app/notice.js:39-40`) | payout liveness | — | `contracts/founders.bas:137-146` |
| Civilware | Artificer fee recipient | only if an NFA's creator sets a share | third party | Receives that share | — | — | `contracts/nfa.bas:85` |
| derod, read by the companions | Contract state, heights, gas, tx status | yes | the user; `-daemon`, default `127.0.0.1:20000` | Trusted for all contract state. xmrswap's code pin reads the code from the same node (`tools/xmrswap/dero.go:410-422`). | Refund needs it; xmrswap sends a blind Refund if reads fail (`tools/xmrswap/swap.go:1369-1405`) | own node | xmrswap `dero.go` (GetTransaction, GetHeight, GetInfo, GetEncryptedBalance, GetSC, GetRandomAddress, GetGasEstimate); zecswap `dero.go:246-441` |
| DERO wallet RPC, used by the companions | Signs Lock, Claim, Refund, Punish, the stipend, and board and escrow calls | yes | the user; `-dero`, default `127.0.0.1:30006` | **Full spend authority, with no credential support** (GAP-008) | Refund needs it | — | xmrswap `dero.go` (`GetAddress`, `GetBalance`, `transfer` at ring 2, `GetTransfers`, `GetHeight`); zecswap `dero.go:275-345,387` |
| monero-wallet-rpc | Alice's lock; the joint view and spend wallets; Bob's gate count; the sweeps | yes | the user; `-monero`, default `http://127.0.0.1:38082` (`cmd/xmrswap/main.go:49`) | Trusted for confirmations, `locked`, `unlock_time`, `unlocked_balance` (`tools/xmrswap/swap.go:712-750`); nothing is verified cryptographically | gate and sweep | joint keys written to its wallet-dir with an empty password | `tools/xmrswap/monero.go`: `get_height`, `get_address`, `transfer`, `generate_from_keys` (carries the joint spend key), `open_wallet`, `close_wallet`, `refresh`, `get_transfers`, `get_balance`, `sweep_all`; optional digest auth (`monero.go:157-226`) |
| monerod | Behind the wallet-rpc | indirectly | whatever `--daemon-address` the user set; **the companion never contacts it and cannot tell which it is** | A lying node costs Bob the DERO (GAP-010) | gate and sweep | a remote node sees the user's IP | none |
| zcashd or zebrad | Funding, watching, claim, refund | yes for the ZEC market; no binary wires it yet (GAP-004) | the user | UTXOs, depth, spends, branch id (the branch id is cross-checked against the built-in schedule, `tools/zecswap/sighash.go:102-114`); losses per GAP-012 | Refund at cltv needs it | the node learns the P2SH and the user's addresses | `tools/zecswap/node.go`: `getblockchaininfo`, `getblockhash`, `getaddressutxos`, `getaddresstxids`, `getrawtransaction`, `sendrawtransaction` (347-583); basic or cookie auth (189-235) |
| lightwalletd | — | not supported (`tools/zecswap/node.go:26-28`), although the docs say it is (GAP-012) | — | — | — | — | — |
| Zcash key file | Funding, refund, claim signatures | yes | the user; read per signature and zeroed (`tools/zecswap/build.go:111-123`, `swap.go:295-302`) | key custody | — | — | `tools/zecswap/swap.go` (`withKey`) |
| Rendezvous directory | xmrswap's seven setup messages | yes | both parties, over a shared filesystem or a sync service (a third party) | unauthenticated (GAP-019) | the swap stalls without it | carries `v`, S_a, S_b, the Monero txid | `tools/xmrswap/channel.go:49-168` |
| `proof-xmr` binary | keygen, prove, verify | yes | the user builds it | receives the secret on stdin (`proof.go:99-101`); its exit status is the verdict (GAP-003) | setup only | local | `tools/xmrswap/proof.go:39-69,163-176` |
| Holder of the Zcash refund file | Broadcasts the taker's refund while she is offline | optional | someone the taker chooses | cannot redirect funds (the payout is signed in) | covers the taker's absence | learns the swap | `tools/zecswap/swap.go:1261,1288-1319` |
| Vault federation / vaultsigner | Wrapped XMR/ZEC | simulator only | k-of-n signers | full custody of the foreign coins; the only backend is a JSON file, refused off the simulator (`tools/vaultsigner/main.go:127-139`) | — | — | not in the mainnet bundle (`deploy/networks.json:8-9`) |
| DERO for fees | Every DERO transaction's fee comes from a DERO payload | yes for a first trade | an exchange, or the maker's stipend | a party outside the protocol for newcomers (`docs/XMR_SWAP.md` §10) | — | — | `tools/xmrswap/swap.go:930-936` (stipend) |

**Remote or centralised web servers, DNS, storage:** none are required. The app loads no third-party resource
(§5). Image URLs from chain are fetched only when the images setting is on, which it is not by default
(`app/app.js:11`), and the current CSP blocks them anyway (`app/index.html:6`, in flux).

---

## 5. Supply chain

**Toolchain:** `go version` = go1.26.2 windows/amd64, pinned in `deploy/binaries.lock:22` (GAP-015).

| Module | go directive | Crypto dependencies, pinned → newest known | Notes |
|---|---|---|---|
| `go.mod` (`veil`) | 1.22 | none declared | `tests/harness/chain.go:10-15` imports derohe without a require line; it builds only inside `tests/run.sh`'s copy (GAP-034) |
| `tools/go.mod` (`veil/tools`) | 1.22 | `filippo.io/edwards25519` v1.1.1 → v1.2.0; `golang.org/x/crypto` v0.33.0 → v0.57.0; `decred/dcrd/dcrec/secp256k1/v4` v4.3.0 → v4.4.1 (marked `// indirect`, line 7, but imported by `tools/zecswap/sign.go`); `decred/dcrd/crypto/blake256` v1.0.1 → v1.1.0; x/net v0.21.0, x/sys v0.30.0, x/term v0.29.0, x/text v0.22.0 (all behind) | `replace veil => ../` (line 15) |
| `tools/telainstall/go.mod`, `tools/xswdwallet/go.mod` | 1.22 | none | placeholders; both binaries build inside vendored trees (`tools/telainstall/build.sh:9`) |
| `vendor/derohe` @ `e9df1205` | 1.17 | GOPATH vendor directory, no `modules.txt`; x/crypto about late 2021; gorilla/websocket masking with `math/rand` | invisible to govulncheck (GAP-018) |
| `vendor/tela` @ `aa892d2f` | 1.21 | x/crypto v0.17.0, x/net v0.19.0, gorilla/websocket v1.5.0 | `replace` to `civilware/derohe fa76d6016cc6`, not in `vendor.lock` (GAP-017) |

**Where each primitive comes from:**
- Ed25519 / edwards25519: filippo.io, in `tools/xmrswap/keys.go:15`.
- Keccak: `x/crypto/sha3`.
- SHA-256: the standard library.
- RIPEMD-160: the deprecated `x/crypto/ripemd160`, in `tools/zecswap/script.go:17`.
- secp256k1: decred.
- BLAKE2b: hand-written with personalisation, in `tools/zecswap/blake2.go`, pinned to RFC 7693 and ZIP 244 vectors.
- Bulletproofs: Rust `bulletproofs` 5.0.0 with `yoloproofs`, a local patched copy, curve25519-dalek 4.1.3, merlin
  3.0.0 (`research/crosschain/poc/proof-xmr/Cargo.toml:9,25-26`). This is the only proof reachable from production
  code (GAP-003, GAP-013).

**govulncheck** (installed at `%USERPROFILE%\go\bin`):
- Source mode on the tools module fails to load as a whole: derohe imports are unresolved, and zecswap did not
  compile (in flux).
- On the packages that compile: 10 reachable standard-library vulnerabilities, all fixed in go1.26.3–1.26.6.
- Binary mode:
  - `derod.exe` 10, `dero-wallet-cli-xswd.exe` 7, `xswdwallet.exe` 7, `treasurybot.exe` 9, `simswap.exe` 9.
  - `telainstall.exe` 8, including GO-2026-6278.

**package.json / lockfiles:**
- None outside `vendor/`. `tests/js/*.mjs` uses only `node:` built-ins.
- `tools/uitest` loads Playwright from `$PW_DIR` (`tools/uitest/ui.mjs:11`): unpinned, with no lockfile.
- No `node_modules` is tracked.

**Locks:**
- `deploy/vendor.lock` pins URL and commit for derohe, derohe-civilware, tela, engram and documentation, plus the
  allowed modified, deleted and untracked paths.
- `deploy/binaries.lock` pins the sha256 of 11 `vendor/bin/*.exe` and of 5 tool source directories.
- `tools/verifyvendor` checks:
  - HEAD equals the commit (`main.go:112`);
  - the origin URL (115);
  - that each modified file's diff equals the stored patch (139-151);
  - the allow-lists (153-176);
  - the binary hashes (213-240).
- It does not hash the tree, contact upstream, cover `build.sh`, or run automatically (GAP-016). All 11 binary
  hashes matched on the day.
- `deploy/patches/derod-private-genesis.patch` is guarded by `!globals.IsMainnet()`.

**CI:** none. There is no `.github/` or any other CI configuration.

**External URLs at runtime:**
- None by default. CSP `default-src 'none'; script-src 'self'` (`app/index.html:6`, in flux); every script and
  stylesheet is local; no fonts, CDN, analytics, price feed or explorer.
- Sockets: the XSWD wallet (`app/xswd.js:1,34`) and the node (`app/rpc.js:97-108`).
- Templates are same-origin and sha256-checked (`app/forms.js:15`).
- The CSP still allows `connect-src wss:` to any host (UX-09, FE-8).

**Install scripts:**
- None downloads a binary.
- The documented bootstrap `git clone --depth 1` takes upstream default branches, not the pinned commits
  (`deploy/README.md:17-19`), and builds `tela-cli` with `-mod=mod` (`:22`).

**Binaries:**
- None is tracked now (`.gitignore:62`).
- Five are in pushed history (SEC-K-12).
- `derod.exe` and `simulator.exe` were built without the `-trimpath` that `vendor.lock:44` asks for.
- No build is reproducible (GAP-014).

---

## 6. Stubs, mocks and placeholders that could ship

| file:line | Token | Test-only or mainnet-reachable | Why |
|---|---|---|---|
| `tools/vaultsigner/main.go:127-139` | `sim` backend | test-only | refused unless `-network simulator`; the real backends exit; mainnet has no vault |
| `tools/xmrswap/fakewallet.go:32,85` | `FakeWallet` | test-only in practice | used only by `tools/simhtlc/main.go:159`; exported from the production package (GAP-037) |
| `tools/xmrswap/cmd/xmrswap/main.go:47,49,53` | defaults `127.0.0.1:20000`, `http://127.0.0.1:38082`, `-net stagenet` | **mainnet-reachable** | GAP-007 |
| `tools/xmrswap/cmd/xmrswap/main.go:54`; `proof.go:24-25` | `-prover proof-xmr` (PATH); the help text names `/home/<user>/target/proof-xmr/...` | **mainnet-reachable** | GAP-003 |
| `tools/xmrswap/cmd/xmrswap/main.go:60,112-119` | lower t1/t2 minimums | guarded: simulator only | daemon must report "simulator" |
| `tools/xmrswap/cmd/xmrswap/main.go:64` | `-confs` | guarded | lowering it must be typed explicitly |
| `tools/xmrswap/cmd/xmrswap/main.go:67` | `-fee 3000` | mainnet-reachable | fixed fee for Refund and Punish (the daemon cannot estimate them); informational |
| `tools/xmrswap/cmd/xmrswap/main.go:17` | `--disable-rpc-login` in usage text | **mainnet-reachable** | GAP-009 |
| `tools/zecswap/node.go:132` | "not implemented" (Sapling digest) | mainnet-reachable | GAP-039 |
| `tools/zecswap/cmd/zecswap/main.go:52` | `-net testnet` default | mainnet-reachable | affects only the address prefix |
| `tools/xswdwallet/guard.go:14-41` | auto-approve | test-only | refused unless the daemon is loopback and on a test network |
| `tools/telainstall/main.go:78-80,88` | `-daemon 127.0.0.1:20000`, `-app ../app`, `-network simulator` | mainnet-reachable with `-yes` | GAP-032 |
| `tools/netwallet/main.go:34-38,74-77` | `-network testnet`; `-rpc-login` | guarded | login required on mainnet |
| `app/config.js:6-26` | simulator SCIDs and vaults | not in a mainnet bundle | `genconfig -dist` regenerates `config.js` per network (`tools/genconfig/main.go:128,167`) and builds nothing for an undeployed network (68-71) |
| `contracts/nfa.bas:85`, `app/templates/nfa.bas` | hard-coded `dero1qy0khp9s…` | mainnet-reachable | GAP-035 |
| `app/forms.js:42,154` | 64 zeros | mainnet-reachable, correct | the DERO asset id |
| `research/`, `tools/sim*` | `math/rand`, simulated chains | test-only | simulator drivers |
| `testnet/signer-3000{3,4}.json` | fake Monero destination | published | GAP-031 |

No Monero or Zcash address and no SCID is hard-coded in any companion path. The one 64-hex literal in `contracts/`
is the upstream file hash in `nfa.bas:6`. The mainnet block of `deploy/networks.json` is empty, so no simulator id
can reach a mainnet bundle.

---

## 7. Privacy map

| Data | Revealed to | Mechanism | file:line | Avoidable? |
|---|---|---|---|---|
| IP address | the DERO node the app reads (or the wallet's daemon through the proxy) | WebSocket JSON-RPC | `app/rpc.js:74`; `app/xswd.js:117` | yes: run your own node |
| IP address | image hosts, only when images are on | `<img>` with no referrer | `app/ui.js:73`; `app/nfts.js:40,115`; `app/nfas.js:213,230` | yes: off by default (`app/app.js:11`), and blocked by the current CSP |
| The wallet address, even for calls never sent | the node | the gas estimate names `signer` (`app/app.js:177`); per-user reads by key name (`app/feed.js:393`, `app/founders.js:34`, `app/nft.js:15`, `app/sheets.js:115`, `app/token.js:98`) | as cited | partly (PRIV-04, FE-5) |
| Signer address, amounts, timing of every call | everyone | ring 2 everywhere (`app/xswd.js:115`; xmrswap `dero.go:316,409,425`; zecswap `dero.go:325,334`); contracts store raw signer keys in the clear (`htlc.bas:51-52`, `swap.bas:53-54`, `board.bas:31,77`) | — | no, by design (`README.md` §3) |
| XMR swap on DERO: maker, taker, amount, asset, t1, t2, `ha`, `hk`, `hb`; then `s_a` (Claim) or `s_b` (Refund); `k_b` in the Claim's arguments | everyone | `htlc.bas:51-61,85,112` | — | no |
| **What H links, Monero** | Nothing on Monero carries H. Alice's lock and Bob's sweep are ordinary transfers (`tools/xmrswap/monero.go:340-355,461-473`). A revealed `s_a` does not locate the output without S_b and V. The only link is a timing correlation between a DERO Claim and a Monero sweep. | — | `docs/XMR_SWAP.md:216-225` | — |
| **What H links, Zcash** | H is in the board row (`board.bas:37`), the escrow (`swap.bas:55`) and the P2SH redeem script, and `s` is published on both chains (`swap.bas:85`; the claim scriptSig). The DERO addresses of both parties are therefore joined to the ZEC amount and both transparent addresses. `wa_<i>` and `rp_<i>` put the Zcash addresses on DERO even before any Zcash transaction exists (`board.bas:36,78`), so the P2SH address can be computed from DERO storage alone. | everyone | `docs/ZEC_SWAP.md:27-48` | no |
| The taker's transparent history ↔ her DERO address | everyone | the funding key is the refund key (`tools/zecswap/swap.go:1230-1231`); change returns to it (`build.go:206`); payout defaults to that key's address (`swap.go:304-308`) | as cited | partly: a fresh key and a separate payout address per swap |
| Joint Monero address, private view key `v`, S_a, S_b, the Monero txid, both DERO addresses | the counterparty; anyone who can read the channel directory or its sync provider | files | `tools/xmrswap/swap.go:390-433`; `channel.go:77-121` | yes (GAP-019) |
| P2SH and own Zcash addresses | the Zcash node | `getaddressutxos`, `getaddresstxids` | `tools/zecswap/node.go:453,497` | yes: run your own node |
| Addresses, amounts, txids, the joint Monero address — no secrets | whoever reads the companions' stdout (they write no log file) | `logf` | xmrswap `cmd/xmrswap/main.go:109`, `swap.go:796,892,952,1000,1238,1259,1300,1710`; zecswap `swap.go:349,378,650,908,935,961,1266` | yes |
| Key shares, `k_b`, `v`, `s`, pre-signed transactions | anyone who can read the state files | cleartext JSON, 0600 (not enforced on Windows) | GAP-020 | partly |
| Browser storage | anyone with the browser profile; any page on the same TELA-host origin (SEC-K-16) | §3 list | `app/app.js:11,16-20`; `app/feed.js:188-228,437-438`; `app/chat.js:24-27` (not removed by Clear, `app/screens.js:291`); `app/xswd.js:12-13` | local only |
| Referrer / phone-home | none | `no-referrer` meta (`app/index.html:7`), `rel=noopener noreferrer` (`app/nfas.js:176`); the only `fetch` is same-origin templates | — | — |
| The operator's DERO address | everyone | a ring-2 INDEX stores its installer as `owner`; every DOC carries the deployer's signature (`tools/telainstall/main.go:198-203`) | — | an anon INDEX removes the first (GAP-001) |

---

## 8. Accounting invariants

"Own asset" means the contract's own token, whose balance the chain does not track (§1.1). Its conservation holds
only through the contract's code, and only a test can show it.

| Contract | Invariant it must satisfy | Asserted by |
|---|---|---|
| pool.bas | quote balance == `rd` + `fd`; asset balance == `ra` + `fa` (`pool.bas:7`); own LP circulating == `lp` − 1000 | balances: the model check at `tests/pool_test.go:131-137`, used by the pool tests incl. `TestPoolFuzz` (355) and the non-DERO quote in `TestPoolNonDeroQuoteLifecycle` (`tests/zzquote_test.go:18`). **LP supply: not asserted** (GAP-022) |
| launch.bas | quote == `real` (or `rd` after graduation) + `fd` + `tacc` + `rpool`; `rd − real == vd0` and `ra + sold == vt0` in phase 0 (`launch.bas:4`); own token circulating == alloc + sold (phase 0), bounded by `S` | DERO side: `tests/launch_test.go:103-105` via the model (`TestLaunchFuzz`, 453); the state keys at 88. **Token circulation: not asserted**; a non-DERO quote on launch not covered by a balance check found in this pass |
| founders.bas | DERO == `rpool` + `und` (`founders.bas:9-10`); `hst` == Σ `hu_`; own units held == `hst` + Σ `lu_` | the first two: `tests/founders_test.go:28-50` (`checkFounders`); the third is computed and discarded (`:39`, GAP-033) |
| nft.bas | DERO == `fd` + `cacc`; token balance == Σ `lk_`; minted ≤ `maxsupply`; one edition per address | scenario only: `tests/registry_test.go:258-262` (`TestNFT`), `TestNFTMintOncePerAddress` (267). No general invariant (GAP-033) |
| nfa.bas | quote balance == `owed` + `currBidAmt` while a bid is held (`nfa.bas:55-56`); exactly one unit exists | `tests/nfa_test.go:137-143` (`oneUnit`), `:221-235` (owed and bid sums), `:207-208` (empty after every payee is paid); the `conserved` helper after each entry (`:125-126`) |
| htlc.bas | per asset, balance == Σ `v_i` over locks with `st_i == 0` holding that asset; each lock pays out once | scenario checks only: `tests/htlc_test.go:87-99` (open / empty helpers), 121, 402 (three asset locks) (GAP-033) |
| swap.bas | the same per asset | scenario checks: `tests/swap_v2_test.go:80,106,171,204`; `tests/swap_test.go` (`TestSwapEscrow`) |
| vault.bas | DERO == Σ bonds; `sup` == Σ minted − Σ released net; `feepool` == Σ released fees; `sup` ≤ `cap`; foreign reserve ≥ `sup` | the first four: `tests/vault_fuzz_test.go:14-18,53-54,70-74` (`TestVaultConservation`). **The foreign reserve cannot be asserted anywhere: no backend exists.** |
| factory.bas | holds nothing (every fee forwarded) | `tests/registry_test.go:38,345,425-429` |
| chat.bas | holds nothing | `TestChatFeeIsExactAndPaid` (`tests/chat_test.go:72`) |
| board.bas, social.bas | hold nothing | not asserted, and stray value can accumulate (GAP-027) |
| TELA-INDEX-1 / DOC-1 | hold nothing (no MODs) | — |

A single-transfer exit to an unregistered address is refused whole. Only the last destination's lookup error
survives `SanityCheckExternalTransfers` (`vendor/derohe/dvm/simulator.go:227-236`, the DVM-1 mechanism). So
htlc's Punish to an unregistered taker cannot succeed until that account exists. xmrswap refuses to lock for an
unregistered taker (`tools/xmrswap/swap.go:930-936`). swap.bas relies on the maker's companion for the same check
(`swap.bas:27-28`).
