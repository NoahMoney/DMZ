# DMZ on the private DERO testnet (W-01, MT-F05)

The mainnet gate asks for the current contracts, the swap contracts and the app live on a DERO testnet and exercised
for at least a week (MT-F05). The public DERO testnet has one seed in derohe's source, `212.8.242.60:40401`, and it
refused TCP on 2026-09-11 (`research/regtest/public-testnets.log`, "DERO testnet: seed reachability"). So this is the
private testnet of `deploy/README.md` "# testnet": derod `--testnet` on the private genesis from `tools/genesistx`
(`deploy/patches/derod-private-genesis.patch`, which is guarded off mainnet), a miner, and `tools/netwallet` wallets.
It is a testnet in every rule (ports, `deto1…` addresses, consensus code) with no outside peers. What that does not
show is under "What a private chain cannot show".

**Day 0 is 2026-09-12. The window runs to day 7, 2026-09-19.** An earlier attempt set day 0 on 2026-09-11 and was
stopped half-way through its install when the contracts changed (e06c28a: factory.bas v2, htlc.bas v3); nothing it
installed is in the record, and its clock is void.

## Files here

| file | what |
|---|---|
| `exercised.json` | every contract, market and lock exercised, the state each must stay in, and the heights at which a lock left open must take its exit. `tools/testnetcheck` reads it |
| `daily.log` | one block per daily run of `tools/testnetcheck`, day 0 first. Appended by the tool; never edited by hand |
| `deploy.log` | the day-0 transcripts: simdeploy, genconfig, the exercise, telainstall, verifydeploy. Machine paths are written `<repo>`, `<tmp>` |

## The deployment (2026-09-12, main at e06c28a)

The chain is the one started on 2026-09-06 (block 0 `44292f0c65e41c141995df75df8e6826ea1d3e04cedd0702b090e14649a5b267`),
resumed at height 6922; see "How the chain runs" for why it was resumed and not restarted.

`tools/simdeploy` installed the seven contracts from the deployer wallet, with the treasury wallet as `-w2`, and wrote
the testnet block of `deploy/networks.json`:

```
cd tools
go run ./simdeploy -network testnet -daemon 127.0.0.1:40402 -w0 127.0.0.1:40405 -w2 127.0.0.1:40404 \
  -networks ../deploy/networks.json -app <tmp>/simdeploy-app-testnet -timeout 45m \
  -htlc-w1min 400 -htlc-w2min 400 -htlc-g2 4800 \
  -revoke founders:8afd70fb…,launch:bc05b8e3…,nft:389deff9…,pool:d4e42ece…
```

- `-app` points at a scratch directory: simdeploy writes `config.js` and `templates/` there, so `app/config.js` stays on
  the simulator for development. The testnet `config.js` is the one `genconfig -dist -network testnet` renders into
  `dist/testnet/` from the record.
- `-revoke` drops the four 2026-09-06 testnet template hashes instead of carrying them as legacy. The v1 factory they
  were recorded against (`b0bbd7cc…`) lists no market (`pc`, `lc`, `nc` all 0), so nothing that was ever registered
  stops verifying, and the testnet record now accepts exactly the current templates, as the mainnet record will.
- Everything else is the tool's default: `-regfee 10000`, `-swap-wmin 1600`, `-chat-fee 10000`, the Founders Pass at
  0.5 DERO × 500 editions with 50 reserved to the deployer, share 5000 bps, market fee 250 bps.

| contract | SCID | installed code sha256 = `reference.Canon(contracts/<kind>.bas)` | install parameters, read back |
|---|---|---|---|
| factory.bas v2 | `a902d8f37dfe529bfd669d4225be0b64cf7847e15234d08ba4c5db8872f1c6b8` | `73f281df0258863d46930452624d95fdc566b8cb8c6c11b618ece324c9174ac1` | `v` 2, no `owner` key; treasury = the treasury wallet, `regfee` 10000, both fixed for the factory's life |
| social.bas | `8768f9c5be9a802b0783ca80f7c7eddf44877b2cbe03def09dbe19ef951428eb` | `da7ef3f09fccf468c9c03605e0de14a4b129b8cfbb29ced6efad8bf9b5b617ad` | — |
| founders.bas | `bfdb00b17f9420764c8a23204fd382e4b5d423b9c8f1ad8fe499603761b0b28c` | `49009d2c7afa3f85d2d0c949383557562333c7163d05a042f59aded06b588b4a` | price 500000, maxsupply 500, reserve 50 |
| chat.bas | `9fb375bda416f90a7c83b7af3c05fc31f14b085180749acd35f7621c42470fdf` | `c1d7136bd137a114f4f529ab3b5864a72780e68cd1fd9fd8a61a23856a1ca9d4` | fee 10000, maxlen 280 |
| htlc.bas v3 | `a80f38f2587b144ee4064a21d529fbbd7da782d101169d1c65cb5cda4e1d0798` | `b785028da4a2554521d47f989f1011ec1f0a26be491e6a7eb2786c1f50834c36` | `w1min` 400, `w2min` 400, `g2` 4800 |
| swap.bas | `8ccee4e2b81df67866c4f076ff3af8dc1d1feea104bc6fd9bc3a87eb50b3c4fa` | `b9830de831dbaf34f61ea15b484c8f25105e41dc790aa06412db421a990fd6e3` | `wmin` 1600 |
| board.bas | `955bcb40b99101c1a3712e8167657222337c8cccbc8867816897962cee69cbc9` | `162f8d46ad400b1fef981d029ba99f61c9646d6453c3bf9f0cc4bd9e63bc5ab1` | `aw` 200 |

**`g2` 4800 is not a decision.** The htlc.bas v3 install arguments are the simulator record's (400 / 400 / 4800).
`w1min` and `w2min` are the Monero profile's T1 and T2 − T1; `g2`, the Punish window, is open with the operator
(MT-J13 G2). A different G2 means a new htlc.bas install with a new SCID, and the htlc part of this exercise would be
run again on it. The same code with other values has the same code hash, so the table's third column is the only place
the parameters show.

The app: `go run ./genconfig -dist -network testnet` built `dist/testnet/` (37 files and `manifest.json`), and
`telainstall` installed it with every DOC at ring 2 and the INDEX at **ring 16**, so the INDEX's stored owner is
`"anon"` and nobody can update it: INDEX `836969a905a7ea1c40d1a6bc3b5c804d541fcc962fffea5dbb9dbb4b01b8eaff`, 38 DOCs,
commit 0. It took four starts, each after the first with `telainstall -reuse`, which points at a DOC only once it has read
back that the DOC's on-chain content is this bundle's:

1. 16:35, stopped after two DOCs when the coordinator paused this work;
2. 17:31, failed at its first read: the testnet's processes had died with a closed console window (see below);
3. 18:40, all 38 DOCs in, then the INDEX's gas estimate hit derod's graviton "Bucket cannot start with ':'" panic five
   times and telainstall gave up. `dvm.Simulator.SCInstall` (vendor/derohe/dvm/simulator.go) reseeds math/rand with
   `time.Now().Unix()` on every call and draws the simulated SCID from it, so every estimate inside one second draws the
   same SCID, and telainstall's five retries run inside one second. Retrying a second later is what helps; the comment
   on telainstall's `installWithFee` ("the simulated SCID is a function of the content") is wrong;
4. 18:44, every DOC reused, the INDEX installed.

Nothing was installed twice. The record is `deploy/tela-testnet.json`, which `telainstall -out` wrote. The INDEX
is not in `networks.json` (`tela.index` there is written by no tool), so `testnetcheck` hands it to verifydeploy as
`-extra tela=<index>`.

## The exercise (day 0)

`tools/testnetcheck/exercise` is the driver. It sends from the testnet wallets, and it refuses the simulator's port, any
daemon that does not report a testnet, and any chain whose block 0 is not the one above. The maker is the trader
wallet; the taker, liquidity provider and creator is the deployer wallet.

```
go build -o ../.testnet/bin/exercise.exe ./testnetcheck/exercise
../.testnet/bin/exercise.exe -mode market|htlc|swap|board|social     # each prints "TX <step> <txid> height <h>"
```

| mode | what it does | the state that must hold for the week |
|---|---|---|
| market | launch.bas "DMZ Testnet Coin" installed and registered; a published 1 DERO buy; pool.bas DTC/DERO installed and registered; 2 DERO + 1,000,000 DTC of liquidity; three trades, the first published. Every step compared with `reference/` | both markets in the factory registry, at least one published trade each |
| htlc | lock A, claimed by the taker at once with s_a and k_b; lock B, left for the maker's Refund in [t1, t2); lock C, left for anyone's Punish in [t2, t2 + g2), which pays the taker | A st 1; B st 2 once its window opens; C st 3 once its window opens |
| swap | lock 1, claimed by the taker with the secret; lock 2, left to expire (`wmin` + 20 blocks) and then refunded | 1 st 1; 2 st 2 after its expiry |
| board | offer 0 posted and cancelled by its maker; offer 1 posted and accepted by the taker | 0 st 2; 1 st 1 |
| social | social.bas Profile, a chat.bas message, a Founders Pass minted and registered | not checked daily |

Locks B and C and swap lock 2 are left open on purpose, so that the window sees exits made days after their locks:
`exercise -mode wait` (detached, see below) sends each exit once its window opens and records it in
`.testnet/exercise-state.json`, which also holds lock B's refund secret and so stays out of the tree. htlc.bas v3's
Reclaim, from t2 + 4800, is the one exit this week cannot reach: at about one block a minute, t2 + g2 of a lock made
on day 0 is some 5,600 blocks away.

### Day 0 result (2026-09-12)

- market, htlc, swap and board ran in one pass, heights 6944–6982; every market step matched `reference/`. social ran
  its Profile and chat message at 6984, and was then stopped twice (the coordinator's pause, the 17:23 crash); its third
  start sent a second Profile and chat message and the Founders Pass mint and registration (7080–7086; `minted` 51,
  one registered holder). Every txid is in `deploy.log`.
- `verifydeploy -network testnet -dist ../dist/testnet -tela-record ../deploy/tela-testnet.json -extra tela=836969a9…`,
  no `-allow`: exit 0, `summary: 11 INFO, 35 PASS — no failure` (`deploy.log`, section 4).
- `testnetcheck`, day 0 at height 7125: `result: PASS (0 FAIL, 0 WARN)` (`daily.log`).
- No Zcash swap was run against `swap.bas` here, and none should be until the zecswap fix lands: zecswap sets T_dero
  only 3 blocks above `wmin`, and this escrow was installed with `wmin` 1600, so a Lock mined 4–10 blocks late would be
  refused. The DERO legs above were driven by the exercise driver, with 20 blocks of margin.

## The daily check

One command, once a day from day 1 to day 7, from `<repo>`:

```
cd tools && PATH="$HOME/sdk/go1.26.8/bin:$PATH" GOTOOLCHAIN=local go run ./testnetcheck
```

It is read-only: `DERO.GetInfo`, `DERO.GetBlockHeaderByTopoHeight` and `DERO.GetSC`, and `go run ./verifydeploy -network
testnet -dist ../dist/testnet -tela-record ../deploy/tela-testnet.json -extra tela=<the record's INDEX>` with no
`-allow`. It appends one block to `daily.log` and exits 0 when nothing FAILed, 1 otherwise.

**A day PASSes when the block ends `result: PASS (0 FAIL, …)`**, which is when all of these hold:

- node: the daemon reports a testnet, block 0 is the private genesis above, the top block is less than 30 minutes old,
  and the height is above the previous run's;
- verify: verifydeploy exits 0. Every contract the record names is byte for byte its source, every registered market
  runs an accepted template, the factory has no owner, the INDEX is ring 16 (`"anon"`, commit 0), and every DOC serves
  its `dist/testnet` file and is the one `deploy/tela-testnet.json` lists;
- factory: factory.bas v2 with no `owner` and no `renounced` key, the treasury and `regfee` it was installed with, and
  at least the listings registered on day 0;
- markets and locks: each in the state `exercised.json` gives it. A lock left open is INFO until its window opens, WARN
  while the window is open and the scheduled exit has not landed, and FAIL if the window closes with the lock open.

A WARN does not fail the day, but a WARN on the same lock two days running means the waiter is not running: see below.
`dist/testnet` is git-ignored; if it is missing the check compares the DOCs with `../app` and says so (INFO). That
comparison fails as soon as `app/` changes after day 0, so rebuild it with `go run ./genconfig -dist -network testnet`,
never with anything that would change the record.

Expected on the way: htlc lock B's window opens about 410 blocks after day 0 (day 1), lock C's about 820 (day 1–2),
swap lock 2 expires about 1,620 blocks after it (day 2–3). `exercised.json` has the exact heights.

## How the chain runs

All of it binds 127.0.0.1. The node, the miner, the wallets and the exercise driver run from copies in `.testnet/bin/`
(git-ignored), never from `vendor/bin/`, so the coordinator can rebuild `vendor/bin/*.exe`. On 2026-09-12 the copies
from 2026-09-11 no longer matched `deploy/binaries.lock` (re-pinned since), so they were replaced by the `vendor/bin`
builds, whose sha256 matched the lock (derod `ef3f8a92…`, dero-miner `bf57df9a…`, netwallet `1a806e77…`, telainstall
`b28a4aea…`, genesistx `3e9dc320…`). Launchers: `.testnet/run-derod.cmd`, `.testnet/run-miner.cmd` and
`.testnet/run-wallet.cmd <name> <port>`; logs go to `.testnet/logs/`.

| process | endpoint | notes |
|---|---|---|
| derod `--testnet` | RPC `127.0.0.1:40402`, getwork `127.0.0.1:40400`, P2P `127.0.0.1:40401` | `DERO_PRIVATE_GENESIS_TX` from `.testnet/genesis.txt`; `--integrator-address` = the owner |
| dero-miner | 4 threads | mines to the owner wallet, which never transacts. At the minimum difficulty 4 threads give a block every 20–80 s; 2 gave 3–5 minutes |
| treasury wallet | `127.0.0.1:40404` | receives fees; sends nothing |
| deployer wallet | `127.0.0.1:40405` | installed the contracts and the app; the taker and liquidity provider of the exercise. Stopped while `telainstall` held its wallet file |
| trader wallet | `127.0.0.1:40406` | the maker; funded with 200 DERO from the deployer on 2026-09-12. Must stay up for the week: `exercise -mode wait` sends from it |
| `exercise -mode wait` | — | detached; output in `.testnet/logs/exercise-wait.log` |

**Resumed, not restarted.** On 2026-09-12 nothing ran on 40402; `.testnet/derod` held the chain at height 6922 with
its genesis, and the wallets were intact and registered (the trader's registration, which costs about 16 minutes of
proof of work, was mined at 6915). A restart would have meant a new genesis, re-registering and re-funding every wallet,
and nothing gained: the contracts of 2026-09-06 and the half-finished install of 2026-09-11 stay on the chain, unused,
and the record names only the 2026-09-12 set. derod came back at 6922 with block 0 unchanged.

### Starting it detached

Every process is started the way `tools/regtest/common.sh` `start_detached` starts the regtest daemons: created
through WMI (`Win32_Process.Create`), so it is no child of the shell that asked, with `ShowWindow` 0, so it has no
console window anyone can close. `.testnet/start-detached.ps1` does exactly that, and refuses to start a process that is
already running:

```
powershell -NoProfile -File .testnet\start-detached.ps1 -What derod                      # wait until 127.0.0.1:40402 answers
powershell -NoProfile -File .testnet\start-detached.ps1 -What miner
powershell -NoProfile -File .testnet\start-detached.ps1 -What wallet -Name treasury -Port 40404
powershell -NoProfile -File .testnet\start-detached.ps1 -What wallet -Name trader -Port 40406
powershell -NoProfile -File .testnet\start-detached.ps1 -What waiter                     # exercise -mode wait
```

run from `<repo>`, in that order. The same data directory is used every time (`.testnet\derod`); nothing here ever
deletes it. The core of the script, should it need re-creating, is:

```
$si = New-CimInstance -ClassName Win32_ProcessStartup -ClientOnly -Property @{ ShowWindow = [uint16]0 }
Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
  CommandLine = 'cmd.exe /c "<repo>\.testnet\run-derod.cmd"'; CurrentDirectory = '<repo>\.testnet'; ProcessStartupInformation = $si }
```

with `run-miner.cmd`, `run-wallet.cmd <name> <port>`, or, from `<repo>\tools`,
`..\.testnet\bin\exercise.exe -mode wait >> ..\.testnet\logs\exercise-wait.log 2>&1` in its place. The waiter reads what
is still open from `.testnet/exercise-state.json` and exits when every scheduled exit is closed.

Why this matters: on 2026-09-12 the processes were first started through WMI with a visible console, and at about 17:23
that console was closed by accident. derod and the wallets died with it and the miner was left orphaned. derod came
back on the same data directory at the height it had (block 0 unchanged). No install or transfer was lost: the TELA
install that had begun at 17:31 failed on its first read, and a second social run had sent nothing (both were started
again, see `deploy.log`). One file was: the trader wallet was killed in the middle of a save, which left only
`trader.db.bak` (written 17:20:39). It was copied back to `trader.db` (the copy kept as `trader.db.crash-2026-09-12.bak`);
a DERO wallet re-derives its balance from the chain, so nothing depends on how recent the file is.

If the machine restarts during the week, or the daily check FAILs on node because nothing answers on 40402, start the
five processes above before running it again.

## What a private chain cannot show

- No outside peer, node or miner: no reorganisation beyond what one miner makes, no propagation delay, no competing
  transactions, no fee market. Every block is ours.
- The ring members are the chain's 93 registered accounts, all made by this deployment's history: a ring-16
  transaction here says nothing about the anonymity set on mainnet.
- One operator runs every wallet, so nothing here tests two parties who do not trust each other; the swaps are the
  DERO legs only (no Monero or Zcash leg).
- Block times follow our miner, not the network's: heights the swaps depend on pass at our speed.
