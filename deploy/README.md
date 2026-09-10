# Deploying DMZ: simulator, private testnet, mainnet, TELA

Four runbooks in one file, in the order they are used. Every command was run on the stated date; the locks (`vendor.lock`, `binaries.lock`), the per-network record (`networks.json`) and the TELA records (`tela-*.json`) live next to this file.


---

# simulator

## Simulator: build, run, fund, install, invoke, read state

Everything below was executed on Windows 10 with Go 1.26 (2026-09-05). Paths are relative to this repo.

### 1. Clone and build (once)

```
git -c core.longpaths=true clone --depth 1 https://github.com/deroproject/derohe vendor/derohe
git -c core.longpaths=true clone --depth 1 https://github.com/civilware/derohe vendor/derohe-civilware   # XSWD wallet
git clone --depth 1 https://github.com/civilware/tela vendor/tela
cd vendor/derohe && go build -mod=vendor -o ../bin/simulator.exe ./cmd/simulator && cd ../..
cd vendor/derohe-civilware && go build -o ../bin/dero-wallet-cli-xswd.exe ./cmd/dero-wallet-cli && cd ../..
cd vendor/tela/cmd/tela-cli && GOFLAGS=-mod=mod go build -o ../../../bin/tela-cli.exe . && cd ../../../..
```
(If the derohe checkout aborts on a non-ASCII vendored filename, `git restore --source=HEAD :/` inside the clone; the
missing file is documentation only.)

### 2. Run the simulator

```
mkdir .sim
vendor\bin\simulator.exe --data-dir=%CD%\.sim\data --rpc-bind=127.0.0.1:20000 --http-address=127.0.0.1:8089
```
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
cd tools && go run ./simdeploy  -factory <scid> -social <scid>   # + installs founders.bas, rewrites app/config.js
##   founders flags: -pass-price 500000 -pass-max 500 -pass-reserve 50 -pass-share 5000 -pass-mfee 250 -pass-start 0
##   -legacy launch:<old sha256>   keeps earlier template versions accepted by the app
cd tools && go run ./simtraffic -duration 40m               # random buys/sells from wallets 16–21
cd tools && go run ./treasurybot -wallet 127.0.0.1:30002 -once   # claim market fees → deposit the promised share into the
##   Founders Pass → PayBatch to registered holders; drop -once to keep sweeping every -interval (default 60s)
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
go run ./simdeploy -network testnet -daemon 127.0.0.1:40402 -w0 127.0.0.1:40403 -w2 127.0.0.1:40404
go run ./genconfig -dist -network testnet
cd .. && vendor\bin\telainstall.exe -network testnet -wallet .testnet\owner.db -password "" -daemon 127.0.0.1:40402 -app dist\testnet -durl dmz.tela -out deploy\tela-testnet.json
```
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
| federation: signer set named, bonds posted, `cap / capratio ≤ Σ bonds` (`capratio` is fixed at install; the contract refuses any cap above the bound at `Seal`, at `SetCap` and at every mint), viewing key published with `SetViewKey`, foreign backends on mainnet nodes | vault state + `deploy/federation.md` |
| treasury wallet and owner wallet are separate, offline-backed-up, seeds stored off machine | operator checklist |
| the app's mainnet bundle shows "mainnet" in the status pill against a mainnet node | manual |
| `tools/verifyvendor` green: every vendored checkout at its pinned commit with only the locked changes, binaries matching `deploy/binaries.lock` | `go run ./verifyvendor -root ..` — see the note below |
| the Founders Pass `name` and `descr` were passed explicitly on the command line and read back from chain with `DERO.GetSC` after install: neither can be changed afterwards, and the app renders `descr` under the mint button | operator checklist + block explorer |
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
vendor\bin\netwallet.exe -network mainnet -daemon 127.0.0.1:10102 -wallet .mainnet\owner.db -password <pw> -rpc-login <user:pw> -rpc-bind 127.0.0.1:10103 -create
vendor\bin\netwallet.exe -network mainnet -daemon 127.0.0.1:10102 -wallet .mainnet\treasury.db -password <pw> -rpc-login <user:pw> -rpc-bind 127.0.0.1:10104 -create
```
The owner wallet pays for both the contracts and every TELA DOC. The three contracts are ≈ 0.18 DERO measured on
the simulator; the app is the larger half and is unmeasured for the current bundle (see "Costs and sizes"), so
fund the owner wallet with at least 4 DERO and re-measure before committing to a number. The treasury needs 0.1
DERO for its first claims. Keep the seeds off this machine: `netwallet -create` prints the recovery words once and
refuses `-write-seed` on mainnet; pass the password through `VEIL_WALLET_PASSWORD` rather than argv, and
`-rpc-login user:password` (or `VEIL_RPC_LOGIN`) is not optional — `netwallet` exits on mainnet without it,
because every local process could otherwise drive an unauthenticated wallet RPC. Three wallets, three roles
(learned on the private testnet, 2026-09-06): a wallet whose balance changes every block (a miner or integrator
address) decodes its balance for minutes on every change and must never sit in a transaction path; the owner
installs and registers; the treasury receives fees and rarely sends. Never point `xswdwallet` at any of them: it
auto-approves, and it refuses any daemon that is not a loopback test network.

### Contracts

```
cd tools
go run ./simdeploy -network mainnet -yes -daemon 127.0.0.1:10102 -w0 127.0.0.1:10103 -w2 127.0.0.1:10104 ^
  -pass-name "DMZ Founders Pass" ^
  -pass-descr "Register a pass to earn a share of the DERO trading fees the treasury claims from DMZ markets. The share is fixed in this contract; the deposit is made by the treasury's own bot, and no contract forces it. Fees earned in a wrapped asset do not reach holders." ^
  -pass-image https://<your host>/pass.png -pass-price 500000 -pass-max 500 -pass-reserve 50 -pass-share 5000 -pass-mfee 250
```
This installs `factory.bas`, `social.bas`, `founders.bas`, writes `app/config.js` and records the mainnet block
in `deploy/networks.json`. Verify each SCID on a block explorer before continuing: code hash, treasury, owner.
Measured on the simulator: factory ≈ 3.0k, social ≈ 2.2k, founders ≈ 12.8k atomic units of storage gas.

`-pass-name` and `-pass-descr` are written into `founders.bas` at install and there is no setter and no update
path: whatever this command line says is on chain for the life of the contract, and `app/nfts.js` renders the
description under the mint button. Pass both explicitly, as above. This runbook used to omit them and inherit the
tool's defaults, and the description default then read "Registered holders receive the promised share of every
fee the exchange earns, paid in DERO to their wallets" — which the system cannot do: the treasury bot sweeps
DERO-quoted pool and launch fees only, a wrapped-asset fee can never be paid out in DERO, and no contract obliges
the treasury to deposit anything. The sentence above is the one `app/notice.js` will support.

Vault (only when the federation gates pass): `simvault` is a simulator tool — it has no `-network` flag and its
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
vendor\bin\telainstall.exe -network mainnet -yes -wallet .mainnet\owner.db -password <pw> -daemon 127.0.0.1:10102 -app dist\mainnet -durl dmz.tela -out deploy\tela-mainnet.json
```
Measured DOC sizes are in `dist/mainnet/manifest.json`; the install cost is unmeasured for the current app — see
the STALE note under "Costs and sizes" and re-measure on the simulator before the go/no-go, because the last
figure recorded there (≈ 1.64 DERO) predates `wrap.js`. Record the INDEX SCID under `mainnet.tela.index`.
Updates: the same command with `-index <scid>` (only changed DOCs are re-installed; the INDEX keeps the commit
history).

### After install

1. Open the INDEX with `tela-cli serve <index scid>` on a clean machine; connect a mainnet node; the pill must read
   "Mainnet · <height>" and the Founders Pass must render from chain data.
2. Start `tools/treasurybot` from the treasury wallet (`-wallet 127.0.0.1:10104`).
3. Start the signers (`tools/vaultsigner -network mainnet -yes -vault <scid> -backend <chain> …`) only after the
   federation gates. The daemon speaks vault v3 and exits against any other `v`; it needs no operator afterwards,
   and there is no flag with which one could hold or release a withdrawal.

   `-backend` has no default and `-backend sim` is refused on any network but the simulator. `sim` is a JSON file
   the signer reads and appends to: a deposit in it is a line somebody typed, so a signer running on it votes
   Mint for Monero that does not exist and votes Released for a payout nobody was sent. No real backend
   (`zcash`, `monero`, `solana`) is implemented — each one exits saying so — so **there is no configuration in
   which a mainnet vault can be signed for today**, and the federation gate above cannot pass. Sealing a mainnet
   vault before a backend exists is what this refusal is there to stop.
4. Publish: INDEX SCID, factory SCID, template hashes, signer set, caps and viewing keys in one signed post.

### Rollback

Contracts are immutable; the app is not: a broken app release is fixed by another `telainstall -index` (the INDEX
`commit` counter records it). A broken contract is retired by pointing a new factory at the same treasury and
shipping a new app; the old markets keep working for their holders.


---

# tela install

## Installing DMZ on-chain (TELA)

The app is 29 files (25 in `app/` — 20 JS, 4 CSS, `index.html` — plus 4 contract templates in `app/templates/`). Each becomes one `TELA-DOC-1`
contract, gzip-compressed and base64-encoded (`tela.Compress`), with a DERO signature of the stored code in
`fileCheckC/S`, then one `TELA-INDEX-1` embeds the DOC SCIDs (29 for the current bundle). `index.html` is `DOC1` (the entrypoint).
`telainstall` installs every file it finds under `-app` except dotfiles and `.png`, so a `dist/<network>` bundle is
one DOC more than `app/` is: `genconfig` writes `manifest.json` into the bundle and that file is installed too.
Compression is mandatory here: `styles.css` contains `*/` and every JS file contains non-ASCII characters, neither of
which can sit raw inside the DOC template's comment block (TELA-DOC-1 README, "Guidelines").

### Scripted (what this repo runs)

```
tools\telainstall\build.sh                                   # builds vendor/bin/telainstall.exe inside vendor/tela
vendor\bin\telainstall.exe -wallet=<wallet.db> -password= -daemon=127.0.0.1:20000 -app=app -durl=dmz.tela -serve
```
- Installs every DOC with ringsize 2, waits for each TX, installs the INDEX with **ringsize 2 (updatable, public
  owner)**, verifies it with `tela.GetINDEXInfo`, writes `deploy/tela-deployment.json` (index SCID, per-file SCIDs,
  commit number, update TXIDs) and, with `-serve`, serves it exactly as a host app would (`tela.ServeTELA`).
- Update after changing files: `... -index=<index scid>` re-installs only the DOCs whose bytes changed — an
  unchanged one is pointed at again, but only after its on-chain code hash is read back and matched — and calls
  `UpdateCode` on the INDEX.
  The INDEX stores `commit` and `STORE(<n>, HEX(TXID()))` for each update — the on-chain commit history that
  `tela-cli clone <scid>@<txid>` can check out.

### Manual (tela-cli)

```
vendor\bin\tela-cli.exe --simulator --daemon=127.0.0.1:20000 --wallet=<wallet.db> --password=
install-doc app/index.html      (answer: dURL dmz.tela, compress yes, ringsize 2)   × 29 files
install-index DMZ               (paste the 29 DOC SCIDs; DOC1 = index.html)
serve <index scid>
update-index <index scid>       (later changes)
```

### Costs and sizes (measured update, simulator, 2026-09-05, commit 13 — STALE)

> STALE. This table is the 28-DOC bundle of 2026-09-05. The app has since gained `wrap.js` and several files have
> grown, so every row, the total and the DERO figure understate the current app. Per-file source and DOC bytes for it
> are in `dist/<network>/manifest.json` (`bytes`, `docBytes`, `sha256` for all 29 files), written by
> `cd tools && go run ./genconfig -dist -network <net>`; the install fee is only known from a real install, so
> re-measure on the simulator before any mainnet install rather than reading a number off this table.

| File | Source bytes | On-chain DOC bytes (gzip+base64 inside the template) | Install fee (storage gas, atomic units) |
|---|---|---|---|
| index.html | 3 264 | 2 645 | 2 996 |
| amm.js | 6 368 | 4 170 | 4 510 |
| app.js | 16 917 | 9 720 | 10 062 |
| chart.js | 12 911 | 7 299 | 7 644 |
| config.js | 1 475 | 2 425 | 2 772 |
| desk.css | 5 528 | 3 949 | 4 296 |
| feed.js | 15 487 | 8 090 | 8 434 |
| forms.js | 13 312 | 7 204 | 7 550 |
| founders.js | 2 993 | 3 077 | 3 428 |
| home.js | 11 797 | 6 734 | 7 078 |
| live.js | 4 726 | 3 905 | 4 248 |
| markets.js | 7 517 | 5 044 | 5 394 |
| nft.js | 4 659 | 3 596 | 3 938 |
| nfts.js | 11 751 | 6 698 | 7 042 |
| parts.css | 7 010 | 4 089 | 4 436 |
| rpc.js | 8 096 | 5 471 | 5 812 |
| screens.js | 10 883 | 6 456 | 6 806 |
| sheets.js | 4 813 | 3 561 | 3 908 |
| stats.js | 6 869 | 4 995 | 5 340 |
| styles.css | 8 704 | 5 409 | 5 760 |
| templates/founders.bas | 12 154 | 6 458 | 6 834 |
| templates/launch.bas | 13 867 | 6 835 | 7 208 |
| templates/nft.bas | 3 964 | 3 012 | 3 378 |
| templates/pool.bas | 11 814 | 6 651 | 7 020 |
| token.js | 17 098 | 8 872 | 9 218 |
| ui.css | 11 186 | 5 569 | 5 912 |
| ui.js | 11 715 | 8 022 | 8 362 |
| xswd.js | 6 824 | 4 622 | 4 966 |
| **28 DOCs** | 253 702 | 154 578 | 164 352 |

Total for that bundle: 28 DOCs ≈ 151.0 KB on chain, fees ≈ 164 352 atomic units ≈ **1.64 DERO** for the whole app
(that run re-installed every file; a later update pays only for the DOCs that changed, and the INDEX `UpdateCode`
call adds ≈ 0.04 DERO). Those 28 came from `app/` directly; the current app is one file larger and its biggest DOC
is still `app.js`. The DOC limit needs no hand-checking: `genconfig` computes each file's gzip+base64 DOC size and
refuses to write the bundle if any file exceeds `-max-doc` (default 18 KB).

### Hosts

- **Engram** (DEROFDN, 0.6.1+): Explore TELA → enter the INDEX SCID; XSWD is on 44326 by default.
- **tela-cli**: `serve <scid>` opens `http://localhost:808x/index.html`.
The app's XSWD `url` is derived from `location`, so any host port works.
