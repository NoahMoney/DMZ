# Deploying DMZ: simulator, private testnet, mainnet, TELA

Four runbooks in one file, in the order they are used. Every command was run on the stated date; the locks (`vendor.lock`, `binaries.lock`), the per-network record (`networks.json`) and the TELA records (`tela-*.json`) live next to this file.


---

# simulator

## Simulator: build, run, fund, install, invoke, read state

Everything below was executed on Windows 10 with Go 1.26 (2026-09-05). Paths are relative to this repo.

### 1. Clone and build (once)

```
git -c core.longpaths=true clone https://github.com/deroproject/derohe vendor/derohe
git -C vendor/derohe checkout e9df1205b6603c62f0651d0e18e5e77a2584b15e
git -c core.longpaths=true clone https://github.com/civilware/derohe vendor/derohe-civilware   # XSWD wallet
git -C vendor/derohe-civilware checkout 433c45590f6792117ff81a2eb3e45513bb856ff5
git clone https://github.com/civilware/tela vendor/tela
git -C vendor/tela checkout aa892d2ff8d4deb9c519dd19c135e36ff9eac12b
research/crosschain/poc/proof-xmr/fetch-bulletproofs.sh   # the Monero prover's one patched dependency, checked against its crate
cd tools && go run ./verifyvendor -root .. && cd ..         # refuses anything but the pinned commits and the locked changes
cd vendor/derohe && go build -mod=vendor -o ../bin/simulator.exe ./cmd/simulator && cd ../..
cd vendor/derohe-civilware && go build -o ../bin/dero-wallet-cli-xswd.exe ./cmd/dero-wallet-cli && cd ../..
cd vendor/tela/cmd/tela-cli && go build -mod=readonly -trimpath -o ../../../bin/tela-cli.exe . && cd ../../../..   # read-only against vendor/tela/go.sum (GAP-017); verifyvendor checks the result
```
(If the derohe checkout aborts on a non-ASCII vendored filename, `git restore --source=HEAD :/` inside the clone; the
missing file is documentation only.)

### 2. Run the simulator

```
mkdir .sim
vendor\bin\simulator.exe --data-dir=%CD%\.sim\data --rpc-bind=127.0.0.1:20000 --http-address=127.0.0.1:8089
```
- **Every start is a new chain.** The simulator deletes its `--data-dir` when it starts
  (`vendor/derohe/cmd/simulator/simulator.go:187`, `os.RemoveAll`), so a restart, a crash or a rebuild of
  `simulator.exe` removes every contract on it. Run step 3 again afterwards, then the NFA, vault and market seeding,
  before any UI pass or `verifydeploy`.
- Daemon JSON-RPC: `http://127.0.0.1:20000/json_rpc` and WebSocket `ws://127.0.0.1:20000/ws`.
- Block explorer: `http://127.0.0.1:8089`.
- 22 wallets, RPC on `127.0.0.1:30000` … `30021`, **8 DERO each**, empty password. Wallet 0 is
  `deto1qyvyeyzrcm2fzf6kyq7egkes2ufgny5xn77y6typhfx9s7w3mvyd5qqynr5hx`.
- A block is mined as soon as a TX is in the pool, otherwise every 18 s. The genesis wallet (no RPC) collects rewards.
- Stop with Ctrl-C. Delete `.sim\data` for a fresh chain.

### 3. Read state

```
curl -s http://127.0.0.1:20000/json_rpc -d "{\"jsonrpc\":\"2.0\",\"id\":\"1\",\"method\":\"DERO.GetSC\",\"params\":{\"scid\":\"<scid>\",\"variables\":true,\"code\":false}}"
```
Uint64 values are numbers; string values are **hex** (see DECISIONS §3). Balances are keyed by asset SCID hex,
`000…0` is DERO.

### 4. Install a contract with Initialize arguments

1. Estimate: `DERO.GetGasEstimate` with `sc` = code, `sc_rpc` = your args **plus** `{"name":"SC_ACTION","datatype":"U","value":1}`
   and `{"name":"SC_CODE","datatype":"S","value":<code>}`, `signer` = your address, `ringsize` 2.
2. Submit to the wallet: `transfer` with `sc` = code, `sc_rpc` = your args only, `ringsize` 2,
   `fees` = the estimate's `gasstorage` (the tool adds 200). The TXID is the SCID.
3. `DERO.GetSC {code:true}` must return your code; if the install failed on chain the TX still confirms but the SC is absent.

Storage gas per TX is capped at 20000 units and the code bytes count, so every contract must stay under ~19 KB.
`vault.bas` is the one with no room left: 18 895 bytes, 19 229 gas of 20 000, comments included.

### 5. Invoke

`transfer` with `scid`, `sc_rpc` = `[{"name":"entrypoint","datatype":"S","value":"SwapDeroForAsset"}, …params]`,
`transfers` = `[{"destination":<any address>,"burn":<DERO atomic>}]` for DERO in and/or `[{"scid":<asset>,"burn":<units>}]`
for assets in, `ringsize` 2, `fees` from the estimate (estimate `sc_rpc` needs `SC_ACTION` 0 and `SC_ID` H added).
A DERO burn needs a `destination` (`DERO.GetRandomAddress` gives one); asset burns do not.

### 6. Automated runs

```
tests\run.ps1                                   # DVM-level tests + fuzz (no simulator needed)
cd tools && go run ./simsmoke  -contracts ../contracts   # pool on the live simulator
cd tools && go run ./simlaunch -contracts ../contracts   # factory + launch + staking + social + nft
cd tools && go run ./simdeploy  -factory <scid> -social <scid>   # + installs founders.bas (v2), rewrites app/config.js
##   founders flags, defaults the operator's numbers: -pass-price 100000 -pass-max 20 -pass-reserve 4 -pass-share 5000 -pass-mfee 250 -pass-start 0
##   -legacy launch:<old sha256>   keeps earlier template versions accepted by the app
cd tools && go run ./simtraffic -duration 40m               # random buys/sells from wallets 16–21
cd tools && go run ./treasurybot -wallet 127.0.0.1:30002 -once   # claim both legs of every market's fees → deposit the
##   promised share of each asset into the Founders Pass (Deposit(asset)); holders claim from the app; drop -once to
##   keep sweeping every -interval (default 60s)
```
Recorded outputs: `docs/history/HISTORY.md (phase reports)`.

### 7. Network exposure of the helper tools (read before running them)

- `vendor/bin/xswdwallet.exe` (test wallet, auto-approves every request) and the TELA host started by
  `telainstall -serve` bind **all interfaces** (`:44326` and `:8082`), because the fork's XSWD server and the `tela`
  package listen on `:port`. Windows will ask for firewall permission the first time; **choose Block, or only run
  them on a network you trust**, and stop them as soon as the test is done (`taskkill /F /IM xswdwallet.exe`,
  `taskkill /F /IM telainstall.exe`). Never point `xswdwallet` at a wallet with real funds.
- The simulator itself binds only loopback (`127.0.0.1:20000`, explorer `8089`, wallets `30000-30021`).
- `tools/serve` binds `127.0.0.1:8082` only.

### 2026-09-06 simulator state

- The simulator wallets are nearly drained after a day of headless runs (richest 6.9 DERO; the funder 30001 has 3 DERO); the test wallet `xswd_wallet14.db` was topped up with 4 DERO from 30006.
- Founders Pass redeployed at 0.5 DERO (`aee674633edab4c12fe5007765629f06852d1e379847da5553de6a1a4928dde2`, era-capable founders.bas) with the factory and social contracts reused; every earlier template hash stays in `legacy`.
- `tools/uitest/ui.mjs --wallet --dest=<address>` adds a private ring-16 buy and checks the node extracted no signer. Toasts read "confirmed" only when a balance or a contract key proves execution, otherwise "included in block N".


---

# testnet

## Testnet deployment — what runs, how it was set up, what is recorded

The DERO testnet uses testnet ports (daemon 40402, wallet 40403+), `deto1…` addresses and the same consensus
code as mainnet. The public testnet has one seed node in derohe's source (`212.8.242.60:40401`); when it answers,
the node below syncs to it, otherwise the node forms a private testnet chain that our miner extends (same rules,
same address prefix, no other peers). Either way the deployment procedure and the app bundle are identical to
mainnet's except for the flags.

### Node, wallets, coins

```
vendor\bin\derod.exe --testnet --data-dir=%CD%\.testnet\derod --rpc-bind=127.0.0.1:40402 --p2p-bind=0.0.0.0:40401
vendor\bin\netwallet.exe -network testnet -daemon 127.0.0.1:40402 -wallet .testnet\owner.db -password "" -rpc-bind 127.0.0.1:40403 -create -write-seed
vendor\bin\netwallet.exe -network testnet -daemon 127.0.0.1:40402 -wallet .testnet\treasury.db -password "" -rpc-bind 127.0.0.1:40404 -create
vendor\bin\dero-miner.exe --testnet --wallet-address <owner deto1…> --daemon-rpc-address=127.0.0.1:40402 --mining-threads=8
```
Testnet coins come from mining (there is no faucet). Block rewards are spendable after the usual maturity.

### Contracts and app

```
cd tools
go run ./simdeploy -network testnet -daemon 127.0.0.1:40402 -w0 127.0.0.1:40405 -w2 127.0.0.1:40404 -app <tmp>/simdeploy-app-testnet -timeout 45m -htlc-w1min 400 -htlc-w2min 400 -htlc-g2 4800
go run ./genconfig -dist -network testnet
cd .. && vendor\bin\telainstall.exe -network testnet -wallet .testnet\deployer.db -password "" -daemon 127.0.0.1:40402 -app dist\testnet -durl dmz.tela -ringsize 16 -out deploy\tela-testnet.json
```
This is what ran on 2026-09-12 (`research/testnet/README.md`). `-w0` is the deployer wallet (40405): the owner wallet
on 40403 mines and never transacts (see "Three wallets, three roles"). `-app <tmp>/…` keeps simdeploy's `config.js`
out of `app/`, which stays on the simulator; the testnet `config.js` is the one `genconfig -dist` renders. htlc.bas v3
takes `-htlc-w1min`, `-htlc-w2min` and `-htlc-g2` with no default; 4800 is the simulator record's G2, still the
operator's to decide (MT-J13). Stop the deployer's netwallet before `telainstall`, which opens the wallet file itself.
`simdeploy` records the testnet block in `deploy/networks.json`; `genconfig` refuses to bundle if the template
hashes and the record disagree.

### Record (filled in as the steps run)

| item | value |
|---|---|
| node | see `.testnet/derod.out.log` |
| owner wallet | `.testnet/owner.db`, `deto1qy8k3guvefgp6lanx8xecppwt5pnd28l20vujszvactfcj2xz782xqganv068` (seed in `.testnet/owner.seed`, testnet only) |
| treasury wallet | `.testnet/treasury.db`, `deto1qy067pxeld72w0s7kqe99kwal28j54mchpnsj3wlm083wgju6e3uzqge0j9x8` |
| factory / social / founders | `deploy/networks.json` → `testnet` |
| TELA INDEX | `deploy/tela-testnet.json` |

### Private testnet bootstrap (why and how)

DERO refuses miniblocks from unregistered miners, and an account registers only through a transaction that must be
mined, so a fresh testnet chain with no other peers can never start. derohe's own simulator tests solve this by
generating the genesis transaction from a wallet; this repo does the same: `tools/genesistx` prints a genesis
registration/premine transaction for the owner's seed, and the vendored `derod` takes it from the
`DERO_PRIVATE_GENESIS_TX` environment variable (patch in `vendor/derohe/cmd/derod/main.go`, testnet only). The
resulting chain is a testnet in every rule (ports, `deto1…` addresses, consensus) with our owner in genesis.
Other accounts register normally: `tools/netwallet` searches the registration proof of work on all cores (a tx
hash with three leading zero bytes, the same rule `dero-wallet-cli` uses).

Genesis for this deployment: `.testnet/genesis.txt` (block hash `44292f0c65e41c141995df75df8e6826ea1d3e04cedd0702b090e14649a5b267` as reported by derod,
owner premine 100,000,000 DERO).
The daemon must also run with `--integrator-address=<owner>`: block templates name the integrator, and the stock
integrator account does not exist on a private chain ("block template error: miner address is not registered").

### Three wallets, three roles (added 2026-09-06)

The miner/integrator wallet (`.testnet/owner.db`, rpc 40403) never transacts: a balance that changes every block
makes walletapi decode it on every change, and with the 100,000,000 DERO premine that took minutes per block and
starved every deploy. 99.9 M DERO were moved to the treasury (tx `f47a974b…`). The treasury (`treasury.db`, 40404)
receives fees and rarely sends. The deployer (`deployer.db`, 40405, address `deto1qypk4g4q…`, funded with
50,000 DERO, tx `0a3f6d70…`) sends every install and registration; `deploy/testnet-deploy.sh` uses it as `-w0`.
Create wallets with `netwallet -create` (the seed is printed once; `-write-seed` stores it next to the wallet on
test networks only).

### Deployment of 2026-09-06 (guarded contracts, eras, fc counter, ring entrypoints)

Installed from the deployer wallet: factory `b0bbd7cc8c9ebd907dbb373372b1e92ecf0eebfb914f0b4c6e6c33fdd19abbf3`, social
`a5f3d33d74a581a9873312940a28ada27d5c64d6aef106ca30f1ca1adc27dbce`, founders
`f8271d089c8601710af11b53d32fd9a9132bbf60b3eebd0963a93227199254ca` (deploy/networks.json is the record; template
hashes there match the files at install time). App bundle: dist/testnet (29 DOCs: 28 app files plus the bundle's
`manifest.json`; `wrap.js` did not exist yet). TELA install: `deploy/testnet-tela.sh`
(stop the deployer netwallet first: telainstall opens the wallet file itself).

TELA (2026-09-06 16:05): INDEX `f20511a84b44a768866efa3a42ec6b7ea26b61089d67abdbcd361f58276469a8`, dURL `veil.tela`, 29 DOCs, author the
deployer wallet; record `deploy/tela-testnet.json`; `deploy/tela-testnet-reuse.txt` lists every DOC for a resumed or
updated install (`telainstall -reuse`, `-index` to update). The bundle predates the afternoon app changes.


---

# mainnet

## Mainnet deployment — runbook and go/no-go gates

The build is network-agnostic: one contract set, one app, `deploy/networks.json` holds the per-network SCIDs and
`tools/genconfig -dist` renders `dist/mainnet/` for TELA. Mainnet differs from testnet only in the daemon, the
wallet flags, the address prefix (`dero1…`) and the fact that every transaction spends real DERO and cannot be
undone. Nothing in this repo sends a mainnet transaction until an operator runs the commands below with a funded
wallet.

### Go / no-go gates (all must be true)

| gate | how it is checked |
|---|---|
| contract suite green on the exact sources to be installed | `tests/run.sh` |
| template hashes in `networks.json` equal `sha256` of `app/templates/*.bas` | `genconfig -dist` refuses otherwise |
| the same commit ran a full simulator pass (launch, trade, graduate, founders, vault wrap/unwrap) | `docs/history/HISTORY.md (day log)` entry with txids |
| testnet deployment live and exercised for at least one week | `deploy/README.md (Testnet)` record |
| external review of `contracts/*.bas` and of `research/wrapper/README.md (security review)` items 1–3 closed | review report in `docs/` |
| **no vault in the mainnet bundle**: `mainnet.vault` stays empty and `mainnet.vaults` null in `networks.json`. The atomic-swap study (§3, operator decision 12) recommends the federation is not deployed; XMR and ZEC settle by `htlc.bas` + `xmrswap` and `board.bas` + `swap.bas` + `zecswap` instead. A vault would first need every condition this row used to list — signers other than the operator named in `deploy/federation.md`, bonds with `cap / capratio ≤ Σ bonds`, a published viewing key, a real foreign backend — and none of them exists | `deploy/networks.json`; the Wrap tab reads "not offered on this network" |
| treasury wallet and deployer wallet are separate, offline-backed-up, seeds stored off machine | operator checklist |
| the app's mainnet bundle shows "Mainnet" in the status pill against a mainnet node, with no network banner under the header; and the mainnet-shaped UI pass is green: no screen mentions a wrapped asset without saying it is not offered, the Wrap tab draws no control, and a mainnet-labelled build reading a non-mainnet node says so on every screen and refuses to sign | manual, and `tools/withlock.sh ui env PW_DIR=<playwright> node tools/uitest/ui.mjs <url> .sim/ui-mainnet --mainnet` (read the PNGs) |
| every step of `docs/MAINNET_TEST_PLAN.md` ran on the simulator and on the test networks, and its mainnet stage ran at the smallest amounts, each reconciled chain against wallets against companion state against the app | the plan's reconciliation records |
| `tools/verifyvendor` green: every vendored checkout at its pinned commit with only the locked changes, binaries matching `deploy/binaries.lock` | `go run ./verifyvendor -root ..` — see the note below |
| the Founders Pass `name`, `descr`, `maxsupply`, `reserve`, `share` and `mfee` were passed explicitly on the command line and read back from chain with `DERO.GetSC` after install: none can be changed afterwards, and the app renders `descr` under the mint button. The command below carries the operator's recorded answers (MT-J16): 1 DERO, 20 editions, reserve 4, share 5000 bps, market fee 250 bps. MT-J16 said 5 reserved, but `founders.bas` refuses `reserve > maxsupply / 5`, so 5 cannot install at 20 editions and the operator is re-confirming (MX-02, MISS-03). The image URL has no answer yet | operator checklist + block explorer |
| the wallet that installs never mines and never receives block rewards; the treasury never transacts during the install | operator checklist (see Wallets) |
| every fund-moving tool was started with `-yes` on purpose (`simdeploy`, `telainstall` and `vaultsigner` exit on mainnet without it; `treasurybot` runs as a dry run) | operator checklist |

`verifyvendor -write-binaries` re-pins whatever is on disk, hashes and all. Run it only from a clean
`git status`, on a tree freshly rebuilt from the committed sources, and commit `deploy/binaries.lock` in the same
change — otherwise clearing the gate is what re-pins a modification. A finding that reads "source changed since
its binary was pinned" must be read first: if the source did not in fact change since the lock commit
(`git rev-parse <lock commit>:<path>` against `HEAD:<path>`), the lock is wrong rather than the tree, and
re-pinning hides that instead of fixing it.

### Wallets and node

```
vendor\bin\derod.exe --data-dir=%CD%\.mainnet\derod --rpc-bind=127.0.0.1:10102
```
Then each wallet in a console of its own, its password and the RPC login in that console's environment
(`docs/MAINNET_DEPLOYMENT.md` §5 step 3):
```
set VEIL_WALLET_PASSWORD=<the wallet's password>
set VEIL_RPC_LOGIN=<user>:<password>
vendor\bin\netwallet.exe -network mainnet -daemon 127.0.0.1:10102 -wallet .mainnet\deployer.db -rpc-bind 127.0.0.1:10103 -create
```
and the same with `.mainnet\treasury.db` on `127.0.0.1:10104`, with the same `VEIL_RPC_LOGIN`: `simdeploy` and
`treasurybot` read it once and send it to every wallet they call.
The deployer wallet pays for the contracts and every TELA DOC: 3.582 DERO measured by the X-09 rehearsal
(`docs/MAINNET_DEPLOYMENT.md` §7), so fund it with 8 DERO. The treasury needs 0.1 DERO for its calls. Keep the seeds
off this machine: `netwallet -create` prints the recovery words once and refuses `-write-seed` on mainnet. The
password goes through `VEIL_WALLET_PASSWORD` and the RPC login through `VEIL_RPC_LOGIN`, never through argv, and the
login is not optional — `netwallet` exits on mainnet without it,
because every local process could otherwise drive an unauthenticated wallet RPC. Three wallets, three roles
(learned on the private testnet, 2026-09-06): a wallet whose balance changes every block (a miner or integrator
address) decodes its balance for minutes on every change and must never sit in a transaction path; the deployer
installs and registers; the treasury receives fees and rarely sends. Never point `xswdwallet` at any of them: it
auto-approves, and it refuses any daemon that is not a loopback test network.

### Contracts

```
cd tools
set VEIL_RPC_LOGIN=<the wallets' login, as above>
go run ./simdeploy -network mainnet -yes -daemon 127.0.0.1:10102 -w0 127.0.0.1:10103 -w2 127.0.0.1:10104 ^
  -app <an empty scratch directory> -regfee 10000 ^
  -pass-name "DMZ Founders Pass" -pass-image "<the operator's image URL (https), supplied before launch>" ^
  -pass-descr "Register a pass for a fixed share of the fees DMZ takes, paid in the asset each fee was taken in: DERO and DERO tokens through this contract, XMR and ZEC by the treasury's bot. The bot makes each payment; no contract forces it." ^
  -pass-price 100000 -pass-max 20 -pass-reserve 4 -pass-share 5000 -pass-mfee 250 ^
  -swap-wmin 1600 -htlc-w1min 400 -htlc-w2min 400 -htlc-g2 <G2, operator decision MT-J13>
```
This is `docs/MAINNET_DEPLOYMENT.md` §5 step 4, which has the rest. It installs `factory.bas`, `social.bas`,
`founders.bas`, `chat.bas` and the three swap contracts, and records the mainnet block in `deploy/networks.json`;
`-app <scratch>` keeps it from rewriting `app/config.js` and `app/templates/` in the release checkout (the TELA bundle
renders its own `config.js` from the record). Verify each SCID on a block explorer before continuing: code hash,
treasury, registration fee. The rehearsal measured the seven installs at 27,995 atomic units (0.280 DERO).

The wallets' login comes from `VEIL_RPC_LOGIN`, never the command line: on mainnet `simdeploy` refuses before its
first call without it, and refuses a login written into `-w0` or `-w2`.

The Founders Pass numbers are the operator's answers (MT-J16, confirmed 2026-09-13): name "DMZ Founders Pass", price
1 DERO, 20 editions, reserve 4 (the most `founders.bas` allows at 20: `-pass-max / 5`), share 5000 bps, market fee
250 bps, and the description above, approved for founders.bas v2 (227 of 240 bytes). Prices are atomic units (1 DERO
= 100,000), so 1 DERO is `-pass-price 100000`; `-pass-price 1` would sell the pass for 0.00001 DERO, for ever.
**The image URL comes from the operator before launch.** It is written once, at install, and nothing can add it
later: an empty `-pass-image` installs a pass with no image for good. The install attaches no DERO (v2 refuses any),
and the 4 reserved editions go to the `-w0` wallet.
The factory's treasury and registration fee are the `-w2` address and the `-regfee` of this command, for the life of
the factory: `factory.bas` v2 has no owner, no setter and no `Renounce` (MT-J15(b), 2026-09-11). Check both before
the app is published against the factory. A wrong one is answered by installing a new factory and moving the
listings to it (`simdeploy -migrate-from <old factory>`), never by a call to the old one.
`htlc.bas` v3 takes `-htlc-w1min`, `-htlc-w2min` and `-htlc-g2`, none with a default; the command refuses to install
without them.

`-pass-name` and `-pass-descr` are written into `founders.bas` at install and there is no setter and no update
path: whatever this command line says is on chain for the life of the contract, and `app/nfts.js` renders the
description under the mint button. Pass both explicitly, as above. This runbook used to omit them and inherit the
tool's defaults, and the description default then read "Registered holders receive the promised share of every
fee the exchange earns, paid in DERO to their wallets" — which the system cannot do: the treasury bot sweeps
DERO-quoted pool and launch fees only, a wrapped-asset fee can never be paid out in DERO, and no contract obliges
the treasury to deposit anything. The description to pass is the wording the operator approved under MT-J16
(`docs/MAINNET_TODO.md`, 209 bytes), which is not the tool's default word for word.

Vault: **not on mainnet.** The go/no-go table keeps the mainnet bundle free of any vault (the atomic-swap study
recommends the federation is not deployed), and the app then says on its Wrap tab that wrapped assets are not
offered on this network. The rest of this paragraph is kept only as the record of how a vault would be installed
if that decision were ever reversed. `simvault` is a simulator tool — it has no `-network` flag and its
wallet endpoints are the simulator's, so a mainnet vault is installed by the manual sequence in
`contracts/README.md`:

```
InitializePrivate(k, treasury, cap, minbond, minburn, wfee, mindep, regfee, capratio, bigbps, bigdelay)
AddSigner(addr) ×n   →   each signer calls Bond()   →   optional SetLimits(winmax, wdelay)   →   Seal()
```
DERO binds call arguments by name, so all eleven `InitializePrivate` parameters must be sent; there is no
per-deposit maximum to set. `Seal` refuses unless `n ≥ k + 1`, every signer already holds `minbond`, and
`cap / capratio ≤ bondtot` — so the bonds are posted before the seal, not after. Sealing is the point of no
return: it is what revokes the deployer, and from then on there is no owner and no override. Record the SCID
under `mainnet.vault` in `networks.json`.

### App on TELA

```
cd tools && go run ./genconfig -dist -network mainnet          # dist/mainnet/, manifest.json, DOC size check
cd .. && set VEIL_WALLET_PASSWORD=<the deployer wallet's password>
vendor\bin\telainstall.exe -network mainnet -yes -wallet .mainnet\deployer.db -daemon 127.0.0.1:10102 -app dist\mainnet -durl dmz.tela -ringsize 16 -out deploy\tela-mainnet.json
```
`telainstall` opens the wallet file itself, so **stop the deployer's netwallet first**: two processes on one wallet
file corrupt it. It uses no wallet RPC, so it needs no RPC login; the wallet's password comes from
`VEIL_WALLET_PASSWORD`, not the command line. An install that stops half-way is resumed with `-reuse`, as
`docs/MAINNET_DEPLOYMENT.md` §5 step 6 says. Measured DOC sizes are in `dist/mainnet/manifest.json`. The X-09
rehearsal measured 3.302 DERO for the 38 DOCs and the ring-16 INDEX (`docs/MAINNET_DEPLOYMENT.md` §7); mainnet
charges the same storage gas. Re-measure if the bundle changes much. Record the INDEX SCID under
`mainnet.tela.index`.

**There are no updates on mainnet.** An INDEX installed above ring size 2 records its owner as `"anon"`, and
`UpdateCode` then refuses every caller for ever. This was proven on the simulator: an update sent anyway was mined
and changed nothing (`research/release/tela-anon-index.log`). `telainstall` refuses a mainnet INDEX below ring 16
unless `-updatable` is given, and that flag leaves a key able to replace the app. A new release is a new INDEX with a
new SCID, published the same way. Users open an INDEX they have checked with `tools/verifydeploy`.

### After install

1. Open the INDEX with `tela-cli serve <index scid>` on a clean machine; connect a mainnet node; the pill must read
   "Mainnet · <height>" and the Founders Pass must render from chain data.
2. Start `tools/treasurybot` from the treasury wallet: `-wallet 127.0.0.1:10104 -config ../dist/mainnet/config.js`,
   with `VEIL_RPC_LOGIN` set in its console (it refuses a mainnet config without it, dry run included), a dry run
   first (`docs/MAINNET_DEPLOYMENT.md` §5 step 11).
3. **Not on mainnet: there is no vault to sign for** (see the go/no-go table). Were a vault ever installed, the signers
   (`tools/vaultsigner -network mainnet -yes -vault <scid> -backend <chain> …`) would start only after the
   federation gates. The daemon speaks vault v3 and exits against any other `v`; it needs no operator afterwards,
   and there is no flag with which one could hold or release a withdrawal.

   `-backend` has no default and `-backend sim` is refused on any network but the simulator. `sim` is a JSON file
   the signer reads and appends to: a deposit in it is a line somebody typed, so a signer running on it votes
   Mint for Monero that does not exist and votes Released for a payout nobody was sent. No real backend
   (`zcash`, `monero`, `solana`) is implemented — each one exits saying so — so **there is no configuration in
   which a mainnet vault can be signed for today**, and the federation gate above cannot pass. Sealing a mainnet
   vault before a backend exists is what this refusal is there to stop.
4. Publish: INDEX SCID, factory SCID, template hashes, signer set, caps and viewing keys in one signed post.
5. Nothing to renounce. `factory.bas` v2 has no owner: the treasury and the registration fee were fixed at install and
   no function changes them, so the step that used to call `Renounce` here is gone (MT-J15(b)). Read back with
   `DERO.GetSC` that the factory holds no `owner` key and that `tstr` and `regfee` are the intended values; the app's
   message from the dev reads "No owner: the treasury and the registration fee were fixed when it was installed".
6. Start `tools/treasurybot` with `-yes` only after a dry run has printed what it would claim and deposit, per asset.
   It now also calls `PayOwed` for the treasury on every listed NFA running the shipped template: `nfa.bas` owes its
   protocol fee instead of sending it, and nothing else ever moves that balance out of the contract.

### Rollback

Contracts are immutable, and so is the mainnet app: its INDEX is installed at ring 16 and nobody can update it.
- **A broken app release** is replaced by installing a new INDEX and publishing its SCID. The old one stays on chain,
  unchanged, for anyone who has checked it.
- **A broken contract** is retired by pointing a new factory at the same treasury and shipping a new app. The old
  markets keep working for their holders.

`docs/INCIDENT_RESPONSE.md` covers the rest of what can and cannot be done after deployment.


---

# tela install

## Installing DMZ on-chain (TELA)

The app is 37 files (32 in `app/` — 27 JS, 4 CSS, `index.html` — plus 5 contract templates in `app/templates/`). Each becomes one `TELA-DOC-1`
contract, gzip-compressed and base64-encoded (`tela.Compress`), with a DERO signature of the stored code in
`fileCheckC/S`, then one `TELA-INDEX-1` embeds the DOC SCIDs (38 for the current bundle: its 37 files and `manifest.json`). `index.html` is `DOC1` (the entrypoint).
`telainstall` installs every file it finds under `-app` except dotfiles and `.png`, so a `dist/<network>` bundle is
one DOC more than `app/` is: `genconfig` writes `manifest.json` into the bundle and that file is installed too.
Compression is mandatory here: `styles.css` contains `*/` and every JS file contains non-ASCII characters, neither of
which can sit raw inside the DOC template's comment block (TELA-DOC-1 README, "Guidelines").

### Scripted (what this repo runs)

```
tools\telainstall\build.sh                                   # builds vendor/bin/telainstall.exe inside vendor/tela
cd tools && go run ./genconfig -dist -network simulator && cd ..   # dist/simulator/, the only kind of -app it takes
vendor\bin\telainstall.exe -wallet=<wallet.db> -password= -daemon=127.0.0.1:20000 -app=dist/simulator -durl=dmz.tela -serve
```
- **What it refuses.** `-app` must be a `dist/<network>` bundle whose `manifest.json` and `config.js` name the
  `-network` being installed to, and whose files match the manifest's hashes.
- **The install.** Every DOC goes in at `-doc-ringsize` (2 by default; a DOC is immutable at any ring size), waiting
  for each transaction. Then the INDEX goes in at `-ringsize`:
  - 2 by default on the simulator and testnet: updatable, with a public owner, for development;
  - 16 by default on mainnet: owner `"anon"`, and nobody can ever update it.
- **Afterwards.** It verifies the INDEX with `tela.GetINDEXInfo` and writes `deploy/tela-deployment.json` (the index
  SCID, per-file SCIDs, the commit number, update TXIDs). With `-serve`, it serves the app exactly as a host app would
  (`tela.ServeTELA`).
- **Updates, on a ring-2 INDEX only.** `... -index=<index scid>` re-installs only the DOCs whose bytes changed. An
  unchanged DOC is pointed at again, but only after its on-chain code hash has been read back and matched. Then it
  calls `UpdateCode` on the INDEX. `-index` is refused above ring 2, and on an INDEX whose owner is `"anon"`.
  The INDEX stores `commit` and `STORE(<n>, HEX(TXID()))` for each update — the on-chain commit history that
  `tela-cli clone <scid>@<txid>` can check out.

### Manual (tela-cli)

```
vendor\bin\tela-cli.exe --simulator --daemon=127.0.0.1:20000 --wallet=<wallet.db> --password=
install-doc app/index.html      (answer: dURL dmz.tela, compress yes, ringsize 2)   × 37 files
install-index DMZ               (paste the 37 DOC SCIDs; DOC1 = index.html)
serve <index scid>
update-index <index scid>       (later changes)
```

### Costs and sizes (measured on the simulator, 2026-09-12)

Measured by installing the whole bundle on a fresh simulator from one wallet that nothing else drives, so its balance
change is the install and nothing else (MT-E07, `research/release/tela-install-cost.log`). The bundle was built by
`genconfig -dist -network simulator` from `cc92174` (34 files plus `manifest.json`). Every DOC went at ring 2 and
the INDEX at ring 16, as on mainnet. The simulator charges storage gas and nothing else, so each fee is that
transaction's storage gas.

| File | Source bytes | DOC bytes (`genconfig`, gzip+base64) | Install fee (storage gas, atomic units) |
|---|---|---|---|
| amm.js | 9,232 | 4,708 | 6,286 |
| app.js | 33,752 | 16,024 | 17,600 |
| chart.js | 15,632 | 7,632 | 9,218 |
| chat.js | 9,832 | 4,868 | 6,452 |
| config.js | 4,099 | 2,952 | 4,544 |
| desk.css | 6,567 | 3,184 | 4,774 |
| feed.js | 40,889 | 18,128 | 19,712 |
| forms.js | 29,914 | 12,968 | 14,554 |
| founders.js | 4,052 | 2,404 | 4,004 |
| home.js | 26,487 | 12,200 | 13,782 |
| index.html | 4,710 | 1,828 | 3,428 |
| live.js | 3,989 | 2,264 | 3,846 |
| manifest.json | 5,916 | — | 4,488 (written by genconfig) |
| markets.js | 20,609 | 10,472 | 12,068 |
| nfas.js | 32,402 | 14,172 | 15,756 |
| nft.js | 6,854 | 3,340 | 4,920 |
| nfts.js | 15,723 | 7,240 | 8,822 |
| notice.js | 9,191 | 4,968 | 6,560 |
| parts.css | 15,411 | 5,740 | 7,332 |
| rpc.js | 10,350 | 5,400 | 6,978 |
| screens.js | 28,560 | 13,636 | 15,228 |
| sheets.js | 15,391 | 6,612 | 8,202 |
| stats.js | 11,985 | 6,664 | 8,250 |
| styles.css | 9,390 | 4,856 | 6,454 |
| templates/founders.bas | 10,398 | 3,844 | 5,492 |
| templates/launch.bas | 15,257 | 5,368 | 7,010 |
| templates/nfa.bas | 14,937 | 4,480 | 6,108 |
| templates/nft.bas | 3,873 | 1,508 | 3,138 |
| templates/pool.bas | 10,604 | 3,668 | 5,298 |
| token.js | 32,853 | 15,192 | 16,778 |
| ui.css | 11,773 | 4,832 | 6,412 |
| ui.js | 20,709 | 11,344 | 12,920 |
| wrap.js | 40,362 | 18,080 | 19,660 |
| xswd.js | 8,023 | 3,960 | 6,224 (size-based fallback fee: the node could not estimate it) |
| zswap.js | 24,550 | 11,884 | 13,470 |
| INDEX (ring 16, owner "anon") | — | — | 6,964 |
| **35 DOCs and the INDEX** | 564,276 | | **322,732** |

Total: **3.22732 DERO** in 36 transactions, 12 min 15 s on the simulator. The wallet went from
800,000 to 477,268 atomic units, exactly the sum of the fees. A later release is a new INDEX (a ring-16 INDEX
cannot be updated), and it pays again only for the DOCs whose bytes changed.

**The limit that binds is storage gas, not DOC size.** Every transaction is capped at 20,000 units of storage gas.
A DOC's install gas measured 1,576 to 1,648 above its `genconfig` DOC bytes, so a DOC larger than about 18,350
bytes cannot be installed at all. `genconfig` refuses any file above `-max-doc`, now 18,250 bytes by default
(install gas at most about 19,900). The two closest in this table, `feed.js` (18,128 B, 19,712 gas) and `wrap.js`
(18,080 B, 19,660 gas), have since been split with their code moved verbatim (MT-E10): `feed.js` 13,440 and
`names.js` 5,792; `wrap.js` 12,960, `vault.js` 3,028 and `wrapui.js` 4,176 DOC bytes, so `feed.js` and `wrap.js`
now have 4,810 and 5,290 bytes to grow. The largest DOC is now `app.js` at 16,024. The three extra DOCs add about
8,300 units of storage gas (0.08 DERO) to the total above.

`xswd.js` could not be estimated: the node's estimator hit the graviton bucket panic five times, and `telainstall`
sent it with its size-based fallback fee. It was mined, and overpaid by about 680 atomic units.

### Hosts

- **Engram** (DEROFDN, 0.6.1+): Explore TELA → enter the INDEX SCID; XSWD is on 44326 by default.
- **tela-cli**: `serve <scid>` opens `http://localhost:808x/index.html`.
The app's XSWD `url` is derived from `location`, so any host port works.

---

## Verifying a deployment

Anyone can check a deployment against this repository with no wallet and no help from the operator. All it needs is
a DERO node they run and have synced themselves:

```
cd tools
go run ./verifydeploy -network mainnet -daemon <your node> -app ../app -extra htlc=<scid>,swap=<scid>,board=<scid>
go run ./release -verify <published manifest.json> -binaries <the binaries you downloaded>
```

`verifydeploy` exits 1 unless every one of these holds:

- every contract the record names is, byte for byte, `reference.Canon` of its source;
- every market in the factory registry runs a template the record accepts;
- no deployer role survives: the factory has no owner (`factory.bas` v2 stores none), there is no vault owner after
  `Seal`, and there is no TELA INDEX owner;
- the INDEX serves exactly the app's files.

An INDEX passes only if it was installed above ring 2 (`telainstall -ringsize 16`, stored owner "anon"). A ring-2
INDEX can always be replaced by its installer's key. `release -verify` rebuilds the companions and compares every
hash with the published manifest. `tools/verifydeploy/README.md` and `tools/release/README.md` say what each check
proves and what it does not. The simulator transcripts are in `research/release/`.
