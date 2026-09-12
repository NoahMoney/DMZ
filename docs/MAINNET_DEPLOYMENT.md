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
| `factory.bas` | one contract (v2) | treasury address, registration fee, both fixed by `Initialize` for the factory's life | **no owner exists**: v2 stores none and has no setter, so nothing can change the treasury or the fee, and there is nothing to renounce (MT-J15(b), 2026-09-11) |
| `social.bas` | one contract | none | no role exists |
| `founders.bas` | one contract | name, description, image, price, supply, reserve, share, market fee, treasury (operator decision MT-J16) | no function changes any of them after install |
| `chat.bas` | one contract | treasury, fee per message, maximum length | fixed at install |
| `htlc.bas` | one contract (v3), the Monero swap's DERO leg | `w1min` and `w2min`, the least t1 − lock height and t2 − t1 (400 each, from the Monero profile), and `g2`, the Punish window: 4,800 blocks (24 h), the operator's answer to MT-J13 on 2026-09-12 | fixed at install; no owner, admin, fee or update path |
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
| Go toolchain | **go1.26.8** since 2026-09-12: both go.mod files name `toolchain go1.26.8`, and `binaries.lock` records it for every binary. It replaced go1.26.2, whose standard library had 10 reachable vulnerabilities fixed in 1.26.3–1.26.6 (GAP-015). govulncheck now finds none reachable (`research/release/govulncheck-2026-09-12.log`). Build with `GOTOOLCHAIN=local` and go1.26.8 on PATH, so the go command does not fetch a toolchain on its own | `go env GOVERSION`; the release manifest's `go` |
| `tools/go.mod` | `filippo.io/edwards25519` v1.1.1, `golang.org/x/crypto` v0.57.0 (and `golang.org/x/sys` v0.48.0, indirect), `github.com/decred/dcrd/dcrec/secp256k1/v4` v4.3.0 | `tools/go.sum` |
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
| Registration fee | 10,000 atomic units by default (`-regfee`) | the factory's `Initialize`, for its life: v2 has no setter |
| Factory treasury | the `-w2` wallet's address | the factory's `Initialize`, for its life: v2 has no setter |
| Chat | a fee of 10,000 per message; 280 characters at most | `chat.bas` install |
| Founders Pass | the operator's numbers (MT-J16); the description must be at most 240 bytes, checked before anything is installed | `founders.bas` install |
| Zcash escrow width | `wmin` = 1,600 blocks (`-swap-wmin`) | `swap.bas` install |
| Monero escrow widths | `w1min` = 400 and `w2min` = 400 blocks (`-htlc-w1min`, `-htlc-w2min`); `g2` = 4,800 blocks, the Punish window (`-htlc-g2 4800`; MT-J13, answered 2026-09-12). None has a default in simdeploy | `htlc.bas` install |
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
   `tools/netwallet -create`, each in a console window of its own:
   ```
   set VEIL_WALLET_PASSWORD=<the wallet's password>
   set VEIL_RPC_LOGIN=<user>:<password>
   vendor\bin\netwallet.exe -network mainnet -daemon 127.0.0.1:10102 -wallet .mainnet\deployer.db -rpc-bind 127.0.0.1:10103 -create
   ```
   and the same with `.mainnet\treasury.db` on `127.0.0.1:10104`.
   - The seed is printed once, and only to a console: `-create` refuses when its output is a pipe or a file, and
     mainnet refuses `-write-seed`. Write the words down.
   - The password and the RPC login go through the environment, never the command line. `-rpc-login` (here
     `VEIL_RPC_LOGIN`) is required on mainnet.
   - Give both wallets the same login. simdeploy (step 4) and treasurybot (step 11) read `VEIL_RPC_LOGIN` once and send
     it, as HTTP basic auth, to every wallet they call, and never to the daemon.
   - A new account registers before it can receive anything: the wallet searches a proof of work on every core and
     sends it by itself. Fund a wallet only once its registration is mined (`DERO.GetEncryptedBalance` returns a
     `registration` height instead of "Account Unregistered"): a transfer to an unregistered account is refused.
   - Closing a wallet's console kills it, and a wallet killed while it saves leaves only its `.bak` (copy it back to
     `.db`). Once the seed is written down, a wallet can be restarted without `-create` and without a window.
   - `SECURITY_KEY_MANAGEMENT.md` §3 has the rest. Fund the deployer and the treasury from the funding table (§7).

4. **Contracts:**
   ```
   cd tools
   set VEIL_RPC_LOGIN=<the wallets' login, as in step 3>
   go run ./simdeploy -network mainnet -yes -daemon 127.0.0.1:10102 ^
     -w0 127.0.0.1:10103 -w2 127.0.0.1:10104 ^
     -app <an empty scratch directory> -regfee 10000 ^
     -pass-name "DMZ Founders Pass" -pass-descr "<the approved description, at most 240 bytes>" -pass-image "<url, or empty>" ^
     -pass-price 100000 -pass-max 20 -pass-reserve 4 -pass-share 5000 -pass-mfee 250 ^
     -swap-wmin 1600 -htlc-w1min 400 -htlc-w2min 400 -htlc-g2 4800
   ```
   This installs factory, social, founders, chat, htlc, swap and board, and records them in the `mainnet` block. It
   refuses before paying for anything if a Founders Pass parameter, `wmin` or an `htlc.bas` width would be refused by
   its contract, and if any of the three `htlc.bas` widths is missing: none has a default, so each install names it.
   - **`-htlc-g2 4800`** is the operator's G2, answered on 2026-09-12 (MT-J13): 4,800 blocks, 24 h at 18 s, the Punish
     window `[t2, t2 + g2)`. `tools/xmrswap/mainnet_profile.json` records it, and xmrswap's `DefaultG2Min`, the least
     `g2` a taker's companion accepts, is generated from it. A taker's `-g2-min` cannot be lowered below 4,800 off the
     simulator. So an htlc installed with a smaller `g2` would be refused by every taker's companion, and would have to
     be installed again. A larger one is accepted, but keeps a maker who missed his refund waiting longer for his
     Reclaim, from `t2 + g2`. Install exactly 4,800.
   - **The wallets' RPC login comes from `VEIL_RPC_LOGIN`**, set in this console as in step 3, never from the command
     line. simdeploy reaches the wallets through `tools/simrpc`, which sends the variable to them as HTTP basic auth
     and never to the daemon. On mainnet simdeploy refuses before its first call when the variable is unset, and when
     `-w0` or `-w2` carries a login in its address, which would put the password in the process list and the shell
     history. A wrong login stops the run at its first wallet call with "the wallet refused the login (401)", before
     anything is paid. The rehearsal (`research/testnet/rehearsal-2026-09-12.log`) ran before this was fixed: its
     simdeploy sent no login and stopped there with `GetAddress: decode: invalid character 'A'`, the wallet's 401
     page read as a reply (X-27).
   - **Prices are atomic units** (1 DERO = 100,000). MT-J16's "1 DERO" is `-pass-price 100000`; `-pass-price 1`
     would sell the pass for 0.00001 DERO, for ever. `-pass-reserve` is at most `-pass-max / 5`: 4 of 20 (MT-J16's 5
     is being re-confirmed, since 5 of 20 does not install). `-pass-image` has no operator answer yet; empty is valid.
   - **`-regfee` is final** (below), so it is written out rather than left to the default.
   - **`-app <scratch>`** keeps simdeploy from rewriting `app/config.js` and `app/templates/` in the release checkout;
     the bundle of step 6 renders its own `config.js` from the record.
   - **A run that stops half-way** (a wallet or node error, or a transaction slower than `-timeout`, 180 s by default)
     exits before it writes the record, with the contracts it has installed already paid for. Do not rerun the same
     command: it would install them again. Read their SCIDs from its output and rerun with `-factory <scid>`,
     `-social`, `-founders`, `-chat`, `-htlc`, `-swap` or `-board` for each one already installed.

   **The treasury (`-w2`) and the registration fee (`-regfee`) are final at this step.** `factory.bas` v2 has no owner
   and no setter (MT-J15(b)), so a wrong value can only be answered by a new factory, installed with
   `simdeploy -migrate-from <this factory>` before the app is published against either.

5. **Read the contracts back:**
   ```
   go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102
   ```
   Every contract's code must match its source, and the run must exit 0: with no INDEX recorded yet, the TELA part
   reads INFO ("the record names no TELA INDEX"), not FAIL. The factory has no owner, so the report shows no owner
   role for it; its treasury and fee are printed under roles, and must be the `-w2` address and the `-regfee` of step 4.

6. **The app:**
   ```
   go run ./genconfig -dist -network mainnet
   cd .. && set VEIL_WALLET_PASSWORD=<the deployer wallet's password>
   vendor\bin\telainstall.exe -network mainnet -yes -wallet .mainnet\deployer.db ^
     -daemon 127.0.0.1:10102 -app dist\mainnet -durl dmz.tela -ringsize 16 -out deploy\tela-mainnet.json
   ```
   - The wallet is the deployer's of step 3. telainstall opens the wallet file itself, so **stop the deployer's
     netwallet first**: two processes on one wallet file corrupt it. The password comes from `VEIL_WALLET_PASSWORD`,
     not the command line.
   - Every DOC goes in at ring 2 (a DOC is immutable at any ring), then the INDEX at ring 16. Each transaction is
     waited for, so the step takes about two blocks per file.
   - **If it stops at `index estimate: … Bucket cannot start with ':'`** (X-21): derod's gas estimator draws the SCID
     it simulates an install under from a generator it reseeds with the current second (`dvm/simulator.go`), and about
     one draw in 256 is a SCID graviton refuses. telainstall retries five times, all inside one second, so all five
     draw the same SCID and fail together; the DOCs are installed and paid for, the INDEX is not. Wait a minute and run
     the same command again with `-reuse <file>`, the file listing `<path> <scid>` for every DOC already installed
     (the `DOC … scid <scid>` lines of the first run's output). telainstall points at a listed DOC only after reading
     back that its on-chain content is this bundle's, so nothing is installed twice. A DOC whose own estimate hits the
     same panic is sent with a size-based fee and needs nothing from you.
   - **An install stopped for any other reason** (the process killed, the machine restarted) is resumed the same way:
     the same command with `-reuse` listing the DOCs its output printed. **Do not run `genconfig -dist` again first**:
     it stamps `config.js` with the time, so a rebuilt bundle's `config.js` no longer equals the DOC already on chain,
     and `-reuse` refuses it. The rehearsal was killed after 16 of 38 DOCs and resumed this way (X-09).
   - **Record the INDEX SCID under `mainnet.tela.index`** in `deploy/networks.json`, by hand: no tool writes it, and
     steps 7 and 8 read it from there.

7. **Read the app back:**
   ```
   cd tools && go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102 -dist ../dist/mainnet ^
     -tela-record ../deploy/tela-mainnet.json
   ```
   The INDEX must read owner `"anon"`, commit 0, and hash equal to its SCID, and every DOC must equal its file.
   verifydeploy checks the INDEX the record names: with `mainnet.tela.index` still empty it reports INFO and checks no
   DOC at all, and exits 0. So record the INDEX first (step 6), and see the `tela index` PASS lines in the report.

   There is no Renounce step. Until 2026-09-11 the factory had an owner who ended its powers here; `factory.bas` v2
   has no owner at all (MT-J15(b)), so the deployer key holds no role in any contract from step 4 on, and the owner
   key this runbook used to revoke does not exist. `simdeploy -renounce` now only says so and exits.

8. **The final check:**
   ```
   go run ./verifydeploy -network mainnet -daemon 127.0.0.1:10102 -dist ../dist/mainnet -tela-record ../deploy/tela-mainnet.json
   ```
   It must exit 0 with no `-allow`. This is the state of §6.

9. **Publish:**
    - commit the `mainnet` block of `deploy/networks.json`, `deploy/tela-mainnet.json` and the release manifest;
    - post the INDEX SCID, the contract SCIDs, the template hashes and the manifest's hash;
    - who signs the post, and later releases, is operator decision MT-J10.

10. **Retire the deployer key.** It never held a role in any contract, only any reserve passes and leftover DERO.
    Move those, and destroy it or put it offline. Keep the treasury key backed up offline: it receives fees for ever.

11. **Start `treasurybot`** from the treasury wallet: a dry run first, then `-yes`.
    ```
    cd tools
    set VEIL_RPC_LOGIN=<the treasury wallet's login, as in step 3>
    go run ./treasurybot -daemon 127.0.0.1:10102 -wallet 127.0.0.1:10104 ^
      -config ../dist/mainnet/config.js -journal ../.mainnet/treasurybot-journal.json -once
    ```
    - Without `-yes` a mainnet config is a dry run: it prints what it would claim, deposit and pay, and sends nothing.
      Then drop `-once` and add `-yes` to keep sweeping every `-interval` (60 s).
    - `-config` is the published bundle's `config.js` (its default, `../app/config.js`, is the development copy); the
      bot reads the factory and the pass from it and refuses a daemon on another network.
    - The wallet's RPC login comes from `VEIL_RPC_LOGIN`, as in step 4. On a mainnet config the bot refuses before
      its first call, dry run included, when the variable is unset or `-wallet` carries a login in its address.
    - `-journal` keeps the claim journal out of the tree (its default is a file in the working directory).
    - Its calls' fees, computed from the code (the rehearsal ran it only as a dry run, so none is measured):
      - `ClaimFees` and `Deposit` go through `simrpc.Send`, which pays the node's gas estimate plus 200 atomic units
        (`tools/treasurybot/chain.go:34`, `:41`; `tools/simrpc/simrpc.go:334`).
      - `PayBatch` is sent at a fixed 3,000, whatever its estimate (`tools/treasurybot/main.go:217`, `SendWithFee`).
      - Hold 1 DERO in the treasury for them; the reasoning is in §7.

12. **Run the mainnet stage of `docs/MAINNET_TEST_PLAN.md`** at the smallest amounts, reconciled four ways.

---

## 6. The final on-chain state, and how anyone checks it

| Component | State | Check |
|---|---|---|
| factory | no `owner` key (v2 never stores one); treasury and fee as installed; code = `Canon(contracts/factory.bas)` | `verifydeploy` roles and identity |
| social, founders, chat, htlc, swap, board | code = canonical source; no role key a function can still act on | `verifydeploy` identity and roles |
| every registered market | code = a template hash the record accepts | `verifydeploy` templates |
| the app INDEX | owner `"anon"`, commit 0, hash = its SCID, code = TELA-INDEX-1 | `verifydeploy` tela |
| every app DOC | TELA-DOC-1, content = the published bundle | `verifydeploy` tela |
| vault | none | `mainnet.vault` = `""` |
| companions | binaries = the manifest | `release -verify -binaries` |

Anyone can run all of it with their own DERO node, a clone of this repository at the published commit, and Go. No
key, account or cooperation from the deployer is needed. `verifydeploy` proves what is on chain equals this
source. It does not prove the source is safe: that is the audits' job (`MAINNET_SECURITY_GATE.md`).

---

## 7. Funding

What each step of §5 spends, measured by the X-09 rehearsal of §5 on the private DERO testnet on 2026-09-12
(`research/testnet/rehearsal-2026-09-12.log`), from the wallets' balances before and after each step. The treasury's
row is the exception: the rehearsal ran treasurybot only as a dry run, which sends nothing, so its figures are
computed from the code (below), not measured. A contract transaction pays its storage gas as its fee (simdeploy adds
200 atomic units, and so does treasurybot, except for `PayBatch`, which it sends at a fixed 3,000), and storage gas is
a function of the code and the arguments alone, so these are mainnet's numbers too for this commit's contracts and
bundle. Re-measure if either changes. 1 DERO = 100,000 atomic units.

| Step | Wallet | Measured | What it covers |
|---|---|---|---|
| 3. Wallets | — | nothing | registering an account is a proof of work, not a fee (7 min 33 s and 38 min 40 s on 16 cores: it varies widely) |
| 4. Contracts | deployer | **27,995 (0.280 DERO)** | factory v2, social, founders, chat, htlc v3, swap, board. The Founders Pass reserve (4 editions) is minted to the deployer inside the founders install: no separate cost |
| 6. The app | deployer | **330,215 (3.302 DERO)** | 38 DOCs at ring 2 and the ring-16 INDEX (7,417). telainstall pays each DOC its estimate and nothing more (the 16 DOCs before the rehearsal's interruption cost exactly the sum of their printed gas, 142,264). The W-01 install of the same bundle cost 330,199: `config.js` differs by the SCIDs it names |
| 11. treasurybot | treasury | **computed, not measured** (the dry run sends nothing): at most 10,920 for a sweep that claims 5 markets and pays 20 holders | per sweep that finds fees to claim: one `ClaimFees` per market holding at least `-min-units`, one `Deposit`, and one `PayBatch` at 3,000 per 15 registered holders who are owed something. A sweep with nothing to claim sends nothing. Per call and per day: below |
| **Total** | | **deployer 358,210 (3.582 DERO)**; the treasury's spend depends on how often there are fees to sweep (below) | |

The steps took, on the private testnet's blocks of about a minute: step 4 about 15 minutes (seven installs, two
blocks each); step 5 seconds; step 6 about 71 minutes of installing (39 transactions); steps 7, 8 and 11 seconds.
Mainnet's 18-second blocks make steps 4 and 6 about a third as long.

**The treasury's calls, computed from the code** (`tools/treasurybot`, this commit; nothing here was measured):

| Call | Fee, atomic units | How often |
|---|---|---|
| `ClaimFees` | the node's gas estimate + 200 (`chain.go:34`, `simrpc.go:334`). Its storage gas is not measured in this repository. It writes three keys (`fd`, `fa`, `fc`: `pool.bas:255-257`, `launch.bas:256-258`), fewer than `PayBatch(0, 15)`, whose 620 is the only storage gas of these calls on record (`tests/founders_attack_test.go`, `contracts/README.md`). Budget **820** (620 + 200) | once per sweep for each market whose fees reached `-min-units` (5,000 by default) |
| `Deposit` | the node's gas estimate + 200 (`chain.go:41`). It writes four keys (`dep`, `acc`, `rpool`, `und`: `founders.bas:142`, `:256-258`). Budget **820** | once per sweep that claimed something |
| `PayBatch` | **3,000**, fixed, whatever the estimate (`main.go:217`) | once per slice of 15 registered holders who are owed something (`payoutBatches`): after a deposit, every slice. The pass has at most 20 editions (`-pass-max 20`), so at most 20 holders and **two calls, 6,000** |
| `PayOwed` | the node's gas estimate + 200 (`payowed.go:112`) | once per sweep for each NFA that owes the treasury at least `-min-owed` |

No run in `research/` records the earlier "about 300, 290 and 305", and `PayBatch` is never sent at its estimate. A sweep
that claims from M markets and pays 20 holders costs at most 820 × M + 820 + 6,000. At 5 markets that is **10,920**,
so 0.1 DERO (10,000 units) does not cover a single such sweep. The treasury's own income does not arrive in a fixed
order: the claims send it the markets' fees, and it keeps what it does not deposit (the pass's `share` is 5,000 bps,
so half). It also receives the Founders Pass mint proceeds (`founders.bas:71`), every market's registration fee and
the chat fees. The figure below counts none of that, because the operator may move the treasury's income out.

- **Fund the treasury with 1 DERO** (100,000 units) and keep at least that in it. At 5 markets and 20 holders, with
  one claiming sweep a day, it covers about **9 days** (100,000 / 10,920) with nothing counted from the claims.
- **A sweep can cost more than it brings in.** One market claimed at the default threshold returns 2,500 to the
  treasury (half of 5,000) and costs up to 820 + 820 + 6,000 = 7,640: it loses 5,140. Sweeps run every 60 s, so a
  market that keeps crossing the threshold can do this many times a day, and 1 DERO covers about 19 such sweeps.
  `-min-units 16000` (0.16 DERO) makes any single-market sweep pay for itself (half of 16,000 is 8,000). That choice
  is the operator's.

**Margin, kept separate:** fund the deployer with **8 DERO** (the measured 3.58 plus more than 100%: a run of step 4
that stops half-way is resumed without paying twice only if the reuse flags are used; a changed bundle changes every
DOC's size), and the treasury with **1 DERO** (above). Leftover DERO goes back when the deployer key is retired
(step 10).

**The smallest test amounts** of `MAINNET_TEST_PLAN.md` §5 (stage 3, mainnet), per test:

| Coin | Amount | Tests | What the wallets hold on top (from the plan) |
|---|---|---|---|
| DERO | 0.1 DERO per Monero swap | T1–T3c, T8b, T9, T10 | Bob's DERO wallet 0.5 DERO: the amount, the stipend (14,016) and a fee reserve (13,000). Alice's DERO wallet needs none |
| XMR | 0.01 XMR per Monero swap (`-amount-xmr 10000000000`) | same | each Monero wallet: the amount plus its transfer fee |
| DERO | 0.1 DERO per Zcash swap | T4, T5, T8a | the maker: the amount plus Post, Lock and a Refund or Cancel at `-fee` (5,000 each); the taker: her Accept and Claim fees |
| ZEC | 0.01 ZEC (1,000,000 zatoshi) per Zcash swap | same | her Zcash address: the amount plus the funding fee (above zecswap's 100,000 minimum and the ZIP 317 fee floor of 10,000) |
| DERO | an NFA priced 0.01 DERO (1,000 atomic) | T6 | the creator: the NFA's install and its registration (the factory's `regfee`, 10,000) |
| DERO | a 0.01 DERO buy, the sale of what was bought, and one 0.01 DERO private buy | T7 | the trader: the three amounts and their fees |
