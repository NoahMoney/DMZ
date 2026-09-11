# DMZ: mainnet deployment

*2026-09-11. How DMZ is put on DERO mainnet, and how anyone can check the result. **No mainnet deployment exists.**
The `mainnet` block of `deploy/networks.json` names no contract, and the verdict in `MAINNET_SECURITY_GATE.md` is
NOT MAINNET READY. This page is the procedure for the day that verdict changes, and nothing here should be run
before then. Every tool that spends DERO refuses mainnet without `-yes`. `deploy/README.md` is the runbook these
steps come from, with the reasons behind each.*

---

## 1. What is deployed, and what is not

| Component | Where | Parameters | Final state |
|---|---|---|---|
| `factory.bas` | one contract | treasury address, registration fee | **`Renounce` has run**: no `owner`, `renounced` = the height it ran at |
| `social.bas` | one contract | none | no role exists |
| `founders.bas` | one contract | name, description, image, price, supply, reserve, share, market fee, treasury (operator decision MT-J16) | no function changes any of them after install |
| `chat.bas` | one contract | treasury, fee per message, maximum length | fixed at install |
| `htlc.bas` | one contract, the Monero swap's DERO leg | none | no owner, admin, fee or update path |
| `swap.bas` | one contract, the Zcash swap's DERO leg | `wmin` = 1,600 blocks | fixed at install |
| `board.bas` | one contract, the Zcash market's offers | `aw` = 200 (in code) | fixed |
| Market templates `pool`, `launch`, `nft`, `nfa` | installed later by users, from `app/templates/` | per market | each market is checked by the app against the template hashes in the record |
| The app | TELA DOCs plus one TELA-INDEX-1, **installed at ring size 16** | dURL `dmz.tela` | INDEX owner `"anon"`, `commit` 0: nobody can update it |
| `vault.bas` (the federation) | **not deployed** | — | `mainnet.vault` stays `""` and `vaults` stays `null` |
| The companions `xmrswap`, `zecswap` | each user's own machine | per user | a reproducible release whose hashes are published |

---

## 2. Build, versions and dependencies

| Item | Pinned to | Checked by |
|---|---|---|
| Go toolchain | **go1.26.2 on 2026-09-11. It must move to ≥ go1.26.6 before a release**, because govulncheck lists 10 reachable standard-library vulnerabilities fixed in 1.26.3–1.26.6 (GAP-015) | `go env GOVERSION`; the release manifest's `go` |
| `tools/go.mod` | `filippo.io/edwards25519` v1.1.1, `golang.org/x/crypto` v0.33.0, `github.com/decred/dcrd/dcrec/secp256k1/v4` v4.3.0 | `tools/go.sum` |
| `vendor/derohe` | `e9df1205b6603c62f0651d0e18e5e77a2584b15e` plus one patch (`deploy/patches/derod-private-genesis.patch`, guarded off mainnet) | `tools/verifyvendor` |
| `vendor/derohe-civilware` (XSWD wallet) | `433c45590f6792117ff81a2eb3e45513bb856ff5` | `tools/verifyvendor` |
| `vendor/tela` | `aa892d2ff8d4deb9c519dd19c135e36ff9eac12b` | `tools/verifyvendor` |
| The Monero prover's `bulletproofs` | crate 5.0.0 (sha256 `012e2e5f…073d`, equal to crates.io's index) plus one committed patch | `fetch-bulletproofs.sh -check`; `tools/verifyvendor` (`crates`) |
| `vendor/bin/*.exe` | `deploy/binaries.lock` | `tools/verifyvendor` (re-pin from a clean tree after rebuilding) |

**The build machine:**

```
git clone https://github.com/NoahMoney/DMZ && cd DMZ && git checkout <release tag>
git -c core.longpaths=true clone https://github.com/deroproject/derohe vendor/derohe
git -C vendor/derohe checkout e9df1205b6603c62f0651d0e18e5e77a2584b15e
git -c core.longpaths=true clone https://github.com/civilware/derohe vendor/derohe-civilware
git -C vendor/derohe-civilware checkout 433c45590f6792117ff81a2eb3e45513bb856ff5
git clone https://github.com/civilware/tela vendor/tela
git -C vendor/tela checkout aa892d2ff8d4deb9c519dd19c135e36ff9eac12b
research/crosschain/poc/proof-xmr/fetch-bulletproofs.sh
cd tools && go run ./verifyvendor -root ..              # must print "vendor tree matches deploy/vendor.lock"
cd .. && tools/withlock.sh dvm bash tests/run.sh         # every contract against derohe's own DVM: must be green
node --test tests/js                                     # the app's arithmetic and sanitisers
cd tools && go test ./xmrswap/... ./zecswap/... ./simdeploy ./netcfg ./verifydeploy/... ./release/... ./treasurybot
```

`TESTING_AND_VERIFICATION.md` has every suite, the exact counts from the last run, and what each proves.

---

## 3. Hashes

Every hash that matters comes out of one command, run at the release commit on a clean tree:

```
cd tools && go run ./release -out ../release/<tag>       # writes ../release/<tag>/manifest.json
```

The manifest records:
- the Go version and the commit;
- every companion binary for five platforms;
- for every `contracts/*.bas`, its raw sha256 and its `canonSha256`, which is sha256 of `reference.Canon(source)`, the
  exact code `simdeploy` installs and `verifydeploy` compares;
- every file of the app bundle.

A third party checks it with `go run ./release -verify manifest.json`, with the same Go version, at the same
commit. The manifest is published with the release. No release has been cut, so no hash table is reproduced on this
page: a table copied here from a working tree would describe code that was never released.

After installation, the SCIDs are in the `mainnet` block of `deploy/networks.json` and the TELA record
`deploy/tela-mainnet.json`, both committed.

---

## 4. Network and configuration

| Setting | Value | Where it is fixed |
|---|---|---|
| Network | DERO mainnet; addresses `dero1…`; `simdeploy` and `verifydeploy` refuse a daemon that reports another network | `-network mainnet` |
| Daemon | the operator's own synced `derod`; `127.0.0.1:10102` in the record | `deploy/networks.json` |
| Trade fee | 50 bps (`feeBps`) | each pool's and launch's install |
| Protocol share | 5,000 bps of the trade fee (`pfeeBps`, `launchPfeeBps`) | same |
| Registration fee | 10,000 atomic units by default (`-regfee`) | the factory, until `Renounce`; then fixed |
| Chat | a fee of 10,000 per message; 280 characters at most | `chat.bas` install |
| Founders Pass | the operator's numbers (MT-J16); the description must be at most 240 bytes, checked before anything is installed | `founders.bas` install |
| Zcash escrow width | `wmin` = 1,600 blocks (`-swap-wmin`) | `swap.bas` install |
| Board acceptance window | 200 blocks | `board.bas` code |
| Confirmation policy | DERO 8 (`STABLE_LIMIT`, consensus); Monero 20 by default, never below 10; Zcash 10. The companions enforce them, not the contracts | `CROSS_CHAIN_TRUST_MODEL.md` §4 |
| Verifier configuration | **none**: no verifier, relayer, oracle or signer set exists | — |
| Settlement configuration | each companion takes contract ids from the record or by flag, and refuses any whose code does not hash to the canonical source (`tools/xmrswap/pin.go`, `tools/zecswap/pin.go`) | companion release |
| Swap fees | **none**. The swaps take no protocol fee | — |

---

## 5. The sequence

0. **Preconditions.**
   - Every PASS/FAIL line of `MAINNET_SECURITY_GATE.md` reads PASS.
   - The operator decisions of `MAINNET_TODO.md` §J are made.
   - The proof audit is published.
   - `docs/MAINNET_TEST_PLAN.md` has run on the simulator and the test networks.

1. **Build and test** on the build machine (§2). Everything must be green.

2. **Release the companions:** `go run ./release -out ../release/<tag>`. Publish the manifest. A party other than the
   deployer runs `-verify` and publishes its result (MT-H05).

3. **Node and wallets.** Run your own synced `derod`. Create a deployer wallet and a treasury wallet with
   `tools/netwallet -create`: the seed is printed once, `-write-seed` is refused, and `-rpc-login` is required.
   `SECURITY_KEY_MANAGEMENT.md` §3 has the rest. Fund the deployer.

4. **Contracts:**
   ```
   cd tools
   go run ./simdeploy -network mainnet -yes -daemon 127.0.0.1:10102 -w0 127.0.0.1:10103 -w2 127.0.0.1:10104 ^
     -pass-name "<name>" -pass-descr "<at most 240 bytes>" -pass-image <url> -pass-price <p> -pass-max <n> ^
     -pass-reserve <r> -pass-share <bps> -pass-mfee <bps> -swap-wmin 1600
   ```
   This installs factory, social, founders, chat, htlc, swap and board, and records them in the `mainnet` block. It
   refuses before paying for anything if a Founders Pass parameter or `wmin` would be refused by its contract.

5. **Read the contracts back:**
   ```
   go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102
   ```
   Every contract's code must match its source. At this point the only acceptable FAILs are the factory owner (not
   yet renounced) and the missing INDEX.

6. **The app:**
   ```
   go run ./genconfig -dist -network mainnet
   cd .. && vendor\bin\telainstall.exe -network mainnet -yes -wallet .mainnet\owner.db -password <pw> ^
     -daemon 127.0.0.1:10102 -app dist\mainnet -durl dmz.tela -ringsize 16 -out deploy\tela-mainnet.json
   ```
   Record the INDEX SCID under `mainnet.tela.index`.

7. **Read the app back:**
   ```
   cd tools && go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102 -dist ../dist/mainnet ^
     -tela-record ../deploy/tela-mainnet.json
   ```
   The INDEX must read owner `"anon"`, commit 0, and hash equal to its SCID, and every DOC must equal its file.

8. **Renounce the factory**, once the treasury and the registration fee read back as intended and the app shows them:
   ```
   go run ./simdeploy -network mainnet -yes -renounce -factory <scid> -daemon 127.0.0.1:10102 -w0 127.0.0.1:10103
   ```
   It calls `Renounce` from the owner wallet at ring size 2, then reads the factory back: `owner` must be absent and
   `renounced` set. **It cannot be undone.** A wrong treasury can only be answered by a new factory.

9. **The final check:**
   ```
   go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102 -dist ../dist/mainnet -tela-record ../deploy/tela-mainnet.json
   ```
   It must exit 0 with no `-allow`. This is the state of §6.

10. **Publish:**
    - commit the `mainnet` block of `deploy/networks.json`, `deploy/tela-mainnet.json` and the release manifest;
    - post the INDEX SCID, the contract SCIDs, the template hashes and the manifest's hash;
    - who signs the post, and later releases, is operator decision MT-J10.

11. **Retire the deployer key.** After step 9 it holds no role, only any reserve passes and leftover DERO. Move those,
    and destroy it or put it offline. Keep the treasury key backed up offline: it receives fees for ever.

12. **Start `treasurybot`** from the treasury wallet: a dry run first, then `-yes`.

13. **Run the mainnet stage of `docs/MAINNET_TEST_PLAN.md`** at the smallest amounts, reconciled four ways.

---

## 6. The final on-chain state, and how anyone checks it

| Component | State | Check |
|---|---|---|
| factory | no `owner` key, `renounced` set; code = `Canon(contracts/factory.bas)` | `verifydeploy` roles and identity |
| social, founders, chat, htlc, swap, board | code = canonical source; no role key a function can still act on | `verifydeploy` identity and roles |
| every registered market | code = a template hash the record accepts | `verifydeploy` templates |
| the app INDEX | owner `"anon"`, commit 0, hash = its SCID, code = TELA-INDEX-1 | `verifydeploy` tela |
| every app DOC | TELA-DOC-1, content = the published bundle | `verifydeploy` tela |
| vault | none | `mainnet.vault` = `""` |
| companions | binaries = the manifest | `release -verify -binaries` |

Anyone can run all of it with their own DERO node, a clone of this repository at the published commit, and Go. No
key, account or cooperation from the deployer is needed. `verifydeploy` proves what is on chain equals this
source. It does not prove the source is safe: that is the audits' job (`MAINNET_SECURITY_GATE.md`).
