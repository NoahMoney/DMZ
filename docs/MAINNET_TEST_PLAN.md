# DMZ mainnet test plan

This is the controlled live test sequence for DMZ, written for the system as it is built. There is no custodian,
so nothing is deposited into DMZ and nothing is withdrawn from it: there is nothing to test on that axis. What a
holder does is swap XMR or ZEC for a DERO-side asset, buy an NFA, and trade a token. Those are the tests, together
with the failures a holder will meet: a deadline that passes, a companion restarted in the middle of a swap, a
message that arrives twice.

Every test runs three times, in this order, and a stage does not start until the one before it is reconciled clean:

1. the DERO simulator: 1a with local Monero and Zcash (the fake wallet, or the regtest nodes), 1b with Monero
   stagenet and Zcash testnet;
2. the test networks: the private DERO testnet, Monero stagenet and Zcash testnet;
3. mainnet, at the smallest amounts in §5.

No test uses a production key. Every test ends with the reconciliation in §4.

Written 2026-09-11. Brought up to date on 2026-09-12 against main `e06c28a` and that day's working tree, which also
carries MT-B27 (the maker's Refund cutoff), MT-B28 (his wakes) and `simhtlc -paths reclaim`, not yet committed. Every
flag below was read from `-h` of these builds:

```
cd <repo>/tools
export PATH="$HOME/sdk/go1.26.8/bin:$PATH" GOTOOLCHAIN=local
go build -o <tmp>/xmrswap ./xmrswap/cmd/xmrswap
go build -o <tmp>/zecswap ./zecswap/cmd/zecswap
go build -o <tmp>/simhtlc ./simhtlc
```

Before each stage, rebuild at the commit under test and check every command against `-h` again. A command marked
*to confirm* was written from the design, not from a flag seen in `-h`.

---

## 1. What must exist before each stage

| Needed | State on 2026-09-12, with its evidence | Blocks |
|---|---|---|
| DERO simulator: daemon `127.0.0.1:20000`, 22 wallets on `30000`–`30021` (`deploy/README.md`, simulator §2) | exists. Every start wipes its chain; the base set was redeployed on 2026-09-12 after the go1.26.8 rebuild (MT-E08) | — |
| `htlc.bas` v3, `swap.bas` v2 and `board.bas` v3 on the simulator | recorded in `deploy/networks.json` `simulator`: htlc `ef576fce…` (v3, `w1min` 400, `w2min` 400, `g2` 4,800, read back in `research/regtest/xmrswap-cli.log` run 3), swap `8d951df3…`, board `894af60b…`. `simhtlc` installs a v3 of its own (10 / 6 / 30) when `-htlc` is omitted; `simswap` and `TestRegtestCLI` install their own swap and board | — |
| A fake Monero wallet | only inside `simhtlc -fake-monero`; `xmrswap bob -h` has no flag for it | — |
| The regtest harness: monerod and monero-wallet-rpc 0.18.5.1, zebrad 6.3.0 (`tools/regtest/README.md`) | exists, pinned and signature-checked (MT-F01) | — |
| `proof-xmr`, the prover the Monero swap calls | the reproducible build `7293e9c9…` is pinned (MT-B26); **not externally audited** (MT-G03). `-prover` must be the absolute path of a binary the companion runs directly. The build is Linux, so the command-line runs on Windows used a wrapper pinned with `-prover-sha256`, which is refused on mainnet (`xmrswap-cli.log`). The wrapper is not in the repository | every Monero test on stage 3, until MT-G03; the command-line runs of T1–T3c from Windows (*to confirm*: T1) |
| Monero stagenet node (`tools/regtest/stagenet-up.sh`, RPC `127.0.0.1:38081`) | **syncing** (MT-F03, X-01). Last reading in `.sim/stagenet/progress.tsv`: height 1,214,530 at 07:19Z, of about 2,205,000. It lost every peer once on 2026-09-12 and was restarted | every Monero test on stages 1b and 2 |
| Stagenet wallet-rpcs (`tools/regtest/stagenet-wallets-up.sh`: bob `127.0.0.1:38087`, alice `38088`, no login) | the scripts exist; no wallet is created: `--create` waits for `synchronized: true` | the same |
| Stagenet XMR | none. The source is mining on our own node once it is synced (`start_mining` answers `BUSY` until then); a faucet only if the operator says so (X-06). One block pays at least 0.6 XMR, spendable 60 blocks (about 2 h) later | the same |
| Zcash testnet node (`tools/regtest/zcash-testnet-up.sh`: zebrad 6.3.0 behind the hardened `rpcgate` on `127.0.0.1:18232`, cookie `<repo>/.sim/zcash-testnet/.cookie`) | **syncing** (MT-F04, X-01): 1,706,000 at 03:10Z (`.sim/zcash-testnet/progress.tsv`), 43.8% at 13:33 (MT-C00), against a tip past 4,238,000. The gate serves only zecswap's nine methods (W-07) | every Zcash test on stages 1b and 2 |
| Testnet ZEC (TAZ) | none. **Its source is the operator's decision** (X-07, X-14). The live faucets pay shielded addresses only, so a shielded wallet sits between a faucet and the taker's `tm…` key (`ZEC_SWAP.md` §11.1). The driver wants 280,000 zatoshi confirmed at the taker's address | the same |
| `tools/zecswap` | complete: node client, watcher, ZIP 317 fee, idempotent broadcast, the pre-signed refund, the state machines and the command line (MT-C01–C13, C16, C17). `-verify-headers` is off by default until a testnet swap has run (MT-C14). Prior runs on regtest: `zecswap-e2e.log`, `zecswap-reorg.log`, `zecswap-cli.log` | — |
| The Zcash testnet driver, `TestTestnetSwap` (`ZEC_SWAP.md` §11.1) | written (`70fbba9`), never run (MT-C15) | — (it is T4 and T5's procedure) |
| The private DERO testnet (`derod --testnet`, RPC `127.0.0.1:40402`; `research/testnet/README.md`) | running, resumed at height 6,922 on 2026-09-12. W-01's week runs on it from 2026-09-12 to 2026-09-19. Its wallets are W-01's: owner `40403`, treasury `40404`, deployer `40405`, trader `40406` | — |
| The current contracts on the private testnet | **being deployed now** (W-01, X-05). The working tree's `deploy/networks.json` `testnet`, uncommitted, names factory v2 `a902d8f3…`, htlc v3 `a80f38f2…` (400 / 400 / 4,800), swap `8ccee4e2…` (`wmin` 1,600) and board `955bcb40…`. Not yet in the tree: `research/testnet/deploy.log` and `daily.log`, and a new `deploy/tela-testnet.json` (the file is still the 2026-09-06 install). `xmrswap`'s compiled-in record (`record_gen.go`) names no testnet htlc, so stage 2 passes `-htlc` | stage 2, until `verifydeploy` passes and the record is committed |
| Stage-2 DERO test wallets | none yet: `netwallet -network testnet … -create`, each with its registration proof of work (about 16 minutes), funded from the deployer (`40405`) | stage 2 |
| DERO mainnet: the contracts, then the app | nothing deployed: the `mainnet` block names no contract | stage 3 |
| The study's pre-mainnet items | built: `htlc.bas` v3 (`e06c28a`), the maker's stipend (Path 0), `swap.bas` v2, `board.bas` v3, the pre-signed Zcash refund (MT-C06). Open: G2 (MT-J13) and the other §J decisions, the external reviews (MT-A17, MT-G03), the gate's verdict | stage 3 |
| MT-B27, MT-B28 and `simhtlc -paths reclaim` | in the working tree, uncommitted (X-08). Their live regtest run, RUN 4 of `xmrswap-e2e.log`, is not recorded | T2 step 4, T3b and T3c, until committed |

What that means today:
- Stage 1a can run every test once X-08 is committed. Until then T2's cutoff, T3b and T3c run on an uncommitted tree
  and are recorded as such.
- Stage 1b waits for both public chains to reach the tip, and for their coins.
- Stage 2 also waits for the testnet record and the stage-2 wallets.
- Nothing on stage 3 can run.

## 2. Stages, and the gate between them

| Stage | DERO | Monero | Zcash | Windows and floors |
|---|---|---|---|---|
| 1a | simulator | the fake wallet in `simhtlc`, or regtest | regtest zebrad | lowered. `simhtlc` defaults: `-t1 48 -t2 16 -t1-min 10 -t2-min 6 -stable 2 -confs 10 -gate-deadline 4 -claim-margin 2 -fee 2000`, and an htlc of its own at `-htlc-w1min 10 -htlc-w2min 6 -htlc-g2 30`. zecswap's regtest policy: C_zec 2, cltv 50 ± 20, T_dero 110 |
| 1b | simulator | stagenet | testnet | the mainnet profile. `simhtlc -stagenet` sets `-t1 420 -t2 400 -confs 20 -gate-deadline 100 -stable 8 -claim-margin 8` on the recorded htlc (400 / 400 / 4,800). zecswap's testnet policy: C_zec 10, cltv 96 ± 32 Zcash blocks, T_dero the lock height + 1,603, and the taker funds only with 1,200 DERO blocks left |
| 2 | private testnet | stagenet | testnet | the floors: `t1 − now ≥ 400` and `t2 − t1 ≥ 400` DERO blocks, Bob's t1 width at least max(400, `w1min` + 11) = 411 (420 by default); `g2` 4,800, **pending operator decision MT-J13 G2**; 20 Monero confirmations; zecswap's testnet policy |
| 3 | mainnet | mainnet | mainnet | the same floors, with `g2` as the operator sets it |

- **Lowered windows are for the simulator only.** `xmrswap` refuses `-t1-min`/`-t2-min` below 400/400,
  `-gate-deadline` below 100, `-stipend`/`-stipend-min` below 14,016 and `-g2-min` below 4,800 unless the DERO daemon
  reports `simulator`. It also refuses `-net regtest` against anything but the simulator.
- **The cutoffs.** They follow from the windows at every stage:
  - Bob builds no Lock from `t1 − w1min − 10`.
  - Alice sends no Claim from `t1 − 8 − claim margin`.
  - Bob sends no Refund from `t2 − 11` (MT-B27).
  - Alice sends no Punish from `t2 + g2 − (11 + 8 + claim margin)`: `t2 + 4,773` at the profile, `t2 + 9` at
    `simhtlc`'s defaults.
  - Bob's Reclaim, from `t2 + g2`, has no end.
- **Heights are not clocks.**
  - The simulator mines at once when a transaction waits, and otherwise every 14–18 s.
  - The private testnet's one miner makes a block every 20–80 s. At one block a minute, a stage-2 swap's t1 falls
    about 7 h after the lock, t2 about 14 h after it, and t2 + g2 about 94 h after it.

A stage passes only when both of these hold:
- every test in it that is not blocked (§1) has run;
- every run is reconciled (§4) with no unexplained difference.

The records go into `docs/history/HISTORY.md` with every txid on every chain. A blocked test is recorded as blocked,
naming the row of §1 that blocks it. A run already recorded on regtest or the simulator is prior evidence, not a
stage-1 pass.

Stage 2 shares the private testnet with W-01's week:
- use none of its wallets as a test party;
- do not restart derod or its miner;
- leave the locks in `research/testnet/exercised.json` alone.

On mainnet, tests run one at a time, and each is reconciled before the next begins.

## 3. Wallets and keys

| Role | Stage 1 (simulator) | Stage 2 (testnets) | Stage 3 (mainnet) |
|---|---|---|---|
| Swap maker "Bob": holds DERO, locks first, ends with XMR | `30006` | a new `netwallet` testnet wallet | a new wallet made for this plan |
| Swap taker "Alice": holds XMR, ends with DERO | `30007` | a second new testnet wallet | a second new wallet |
| ZEC market maker / taker / third party | `30003` / `30004` / `30005` (`simswap -a`/`-b`/`-c`); the zecswap drivers use `30011`–`30013` (`30013` installs) | three new testnet wallets, given to the driver as `ZECSWAP_TESTNET_DERO_MAKER`, `_TAKER` and `_INSTALLER` | two new wallets |
| NFA creator / buyer | `30010` / `30013` (`simnfa -creator`/`-buyer`) | two testnet wallets | two new wallets |
| Token trader / private-mode receiver | `30001` / `30006` (`tools/uitest/run.sh` pays its private buy to `30006`) | two testnet wallets | two new wallets |
| Treasury (read, never a signer) | `30002` | testnet treasury `40404` | the mainnet treasury, **read only** |
| Monero | the fake wallet, or regtest wallet-rpcs `28084` (bob) and `28083` (alice) | stagenet `xmrswap-stagenet-bob` on `38087` and `xmrswap-stagenet-alice` on `38088` | two new mainnet wallets holding only the test amount, each behind its own wallet-rpc with `--rpc-login` and `--wallet-dir` |
| Zcash | regtest keys the drivers make | two `zfund` keys in `<keys-dir>` (`ZEC_SWAP.md` §11.1 step 3), the taker's funded with transparent TAZ | two new transparent keys (`t1…`) made for this plan only |

Rules:
- **Never a production key.** The owner, treasury and deployer wallets in `deploy/README.md` (mainnet) never sign
  a test transaction.
- **The app signs through a real wallet** with XSWD on `localhost:44326`, such as Engram or `dero-wallet-cli` with
  XSWD on, opened on the test wallet. `xswdwallet` approves everything it is asked. It is for the simulator only
  and must never open a wallet that holds value.
- **Alice needs a registered DERO wallet, not DERO.**
  - Bob's stipend (Path 0, 14,016 atomic units) pays her Claim or Punish. Her companion locks no XMR until the stipend
    is at stable depth and she can spend at least that.
  - Registration costs no DERO, only `netwallet`'s proof of work.
  - The ZEC market has no stipend. The taker pays her own Accept, and her Claim unless the maker's companion sends it
    for her, which it does once its Zcash claim is deep. So her DERO wallet is funded in advance (MT-J07 is open).
- **Fund and retire stage 3 wallets tightly.**
  - Fund each test wallet with the amount in §5 plus its fees, and no more.
  - Write its seed down off the machine.
  - Retire the wallet when the plan ends.
- **Treat the companions' files as secrets.**
  - An `xmrswap` state file holds that party's key share. After a Claim, Refund or Reclaim has revealed the other
    share, it holds both halves of the joint Monero spend key.
  - A `zecswap` maker's state holds the secret `s`. A taker's `<state>.refund.txt` is her pre-signed refund, and
    anyone holding it learns the swap.
  - `XMRSWAP_CHANNEL_KEY` is the swap's channel key. The maker makes it with `xmrswap channel-key` and hands it over
    out of band.
  - Keep every state file and channel directory in a directory made for the test, outside the repository.
    `.gitignore` covers `bob.json`, `alice.json`, `*.json.lock`, `xmrswap-used.json`, `swap-channel/`, `*.keys`,
    `maker.json`, `taker.json`, `zecswap-used.json` and `*.refund.txt` in case one lands inside it.
  - `xmrswap` restricts its files to their owner on Windows (GAP-020). `zecswap` does not, and says so: give it a
    directory only this account can read, and on stage 3 an encrypted volume.
  - Delete a state file only after its swap is done and its record is filed.
- **Logins come from the environment:**
  - `VEIL_RPC_LOGIN` for the DERO wallet;
  - `XMRSWAP_MONERO_LOGIN` for the wallet-rpc, and `XMRSWAP_MONERO_WALLET_PASSWORD` for its wallet file.

  `-dero-login` and `-monero-pass` are refused on mainnet. On stage 3 each party's companion runs under its own OS
  account.
- **On stage 3 the Monero companions run where the pinned prover runs directly** (Linux): `-prover-sha256` is
  refused on mainnet.

## 4. Reconciliation after every test (directive §40)

Take readings only when:
- the last transaction of the test is at least 8 DERO blocks deep;
- anything that moved on Monero has at least 20 confirmations;
- anything that moved on Zcash has at least 10.

Then read the same facts from four places. **Any difference between them is a finding.** Stop the stage, and record
the difference and its cause before anything else runs.

| Source | What to read | How |
|---|---|---|
| The chains | every transaction the test sent, and every contract key it moved | DERO: `DERO.GetTransaction {"txs_hashes":["<txid>"]}` (`valid_block`, `in_pool` false, `block_height`); `DERO.GetSC {"scid":"<scid>","keysstring":[…]}` for the keys the test names. Monero: each wallet-rpc's `get_transfer_by_txid`, and the view wallet the companion made for the joint address. Zcash, through the gate: `getrawtransaction <txid> 1`, and `getaddressutxos` / `getaddresstxids` for the script's address and each party's; every Zcash txid also on a public block explorer |
| Each person's wallet | the balance of every asset the test touched, before and after | DERO: `GetBalance` on each test wallet, with `"scid"` for a token, NFA unit or LP and without it for DERO. Monero: `get_balance`. Zcash: the address's unspent outputs |
| Each companion's state | `xmrswap`: `phase`, `outcome`, `done`, `lock` (`id`, `tx`), `stipend`, `claimTx`, `refundTx`, `punishTx`, `reclaimTx`, `revealed`, `sweep`. `zecswap`: `phase`, `outcome`, `done`, `note` and its txids | open each party's state file (JSON), and run `xmrswap status -dir <test dir> -daemon <rpc> -monero <wallet-rpc>` and `zecswap status`, which print each swap beside the chain and name every divergence |
| What the app shows | every screen that shows what the test moved | a screenshot per screen, filed with the record |

What must agree:
- **Wallet balances.** Each wallet's change equals what the chain says moved, less exactly the fees the chain
  charged.
- **Companion outcomes.** A companion's outcome is the one the chain shows:
  - `claimed` or `swept` after a Claim only if `st_<id>` is 1 and the sweep's txid is confirmed;
  - `refunded` only if `st_<id>` is 2;
  - `punished` only if `st_<id>` is 3;
  - `reclaimed` only if `st_<id>` is 4;
  - the taker's `swept` after a Refund or a Reclaim only if `st_<id>` is 2 or 4 and her sweep is confirmed.
- **No scheduled wake left behind.** No `xmrswap-*` task remains once both companions are done.
- **The app.** No screen shows a balance, an owner, a listing, a fee or a finished state that the chain does not.
  - The Monero swap has no screen, so it reaches the app only as a balance on Portfolio.
  - The ZEC swap has `#zswap/<board id>` (MT-D05), which must show both DERO contracts' state as the chain has it.

One record per run:

```
T<n> · stage <1a|1b|2|3> · <date> · commit <hash>
wallets:                role → address, per chain
txids:                  chain: txid, height, fee …
contract keys after:    scid: key=value …
balances before → after: wallet: asset: before → after (Δ, of which fee)
companions:             party: phase / outcome / done, txids
app:                    screen: what it showed · screenshot path
differences:            none | each one, with its cause
```

## 5. Amounts

These are the smallest amounts that still prove each step. Every payee's share is non-zero, and every fee is small
next to the amount without swallowing it. On mainnet every market is priced in DERO: the mainnet bundle names no
vault, so the app offers no other quote. One atomic unit is 10⁻⁵ DERO, and one piconero is 10⁻¹² XMR.

| Test | Stage 1 | Stage 2 | Stage 3 (mainnet) |
|---|---|---|---|
| T1–T3c, T8b, T9, T10 (Monero) | 1a: `simhtlc`'s 2,000 atomic DERO against 1 fake or regtest XMR. 1b: `simhtlc -stagenet`'s 2,000 atomic against 0.05 XMR per exit | 10,000 atomic (0.1 DERO) against 0.01 XMR (`-amount-xmr 10000000000`) | **0.1 DERO against 0.01 XMR** per swap |
| T4, T5, T8a (Zcash market) | `simswap`'s locks of 1 and 0.5 DERO, and a board offer of 1 DERO for 0.25 ZEC. `TestRegtestCLI`: 50,000 atomic for 200,000 zatoshi. 1b: `TestTestnetSwap`: 100,000 zatoshi per swap, two swaps | `TestTestnetSwap`'s 100,000 zatoshi per swap, two swaps | 0.1 DERO against 0.01 ZEC (1,000,000 zatoshi), well above zecswap's 100,000 minimum and the ZIP 317 fee floor of 10,000 |
| T6 (NFA) | `simnfa -price 1000 -royalty 5` | price 0.01 DERO | **price 0.01 DERO** (1,000 atomic) |
| T7 (token) | the simulator's markets | 0.01 DERO | **a 0.01 DERO buy**, a sale of what was bought, and one 0.01 DERO private buy |

What the wallets need on top of those amounts:
- **Monero swaps.**
  - Bob's companion refuses to lock unless his wallet can spend the amount plus the stipend (14,016) plus a fee
    reserve of 13,000. That is 37,016 atomic units at 0.1 DERO, so hold 0.5 DERO. At the smallest amount the stipend
    costs him more than the DERO he sells.
  - The Lock and Claim pay the gas estimate plus 200. Refund, Punish and Reclaim cannot be estimated, so they go out
    at a fixed 3,000, and are re-sent at double the fee up to twice.
  - Alice's DERO wallet needs no DERO of its own (§3).
  - Each Monero wallet holds the amount plus its transfer fee. On stage 1b, alice holds 0.153 XMR in at least three
    outputs of 0.051 XMR (`tools/regtest/README.md`).
- **ZEC market.**
  - The maker's DERO wallet holds the amount plus Post, Lock and a Refund or Cancel at `-fee` (5,000 by default).
  - The taker's holds her Accept and Claim fees.
  - Her Zcash address holds the amount plus the funding fee. `TestTestnetSwap` wants 280,000 zatoshi confirmed.
- **NFA.** At 1,000 atomic units:
  - the 5% royalty, 50, is sent to the creator;
  - the 0.5% protocol fee, 5, is owed to the treasury inside the NFA until someone calls `PayOwed`;
  - the seller receives the other 945.

  So each share can be reconciled to the unit. The creator also holds enough DERO for the install and the
  registration; measure the install on the simulator first.

## 6. The tests

Run them in this order in each stage: T0, T7, T6, T1, T2, T3, T3b, T3c, T8, T9, T10, T4, T5. On mainnet, T2 and
everything after it run only after T1 is reconciled.

### T0 — the app's network gate (no funds)

This proves the app never reads one network and signs on another without saying so.

1. Run `tools/withlock.sh ui env PW_DIR=<playwright> node tools/uitest/ui.mjs <app url> .sim/ui-mainnet --mainnet`
   and read the `mainnet-390-*.png` and `mainnet-1024-*.png` files it writes.
   - The Wrap tab opens with "Wrapped XMR and ZEC are not offered on this network", then says how XMR and ZEC settle
     instead (atomic swap, no custodian).
   - It draws no deposit address, no burn form and nothing to sign. Its only button opens the ZEC swap board, when the
     build names one.
   - The banner under the header names both networks.
   - The run ends `UI TEST OK (mainnet-shaped)`.
2. On stage 2, serve `dist/mainnet/` (`cd tools && go run ./serve -dir ../dist/mainnet`) against the testnet node.
   - The pill is red, and the banner names both networks on every screen.
   - Any action answers "Refusing: …" before the wallet is asked.
3. On stage 3, serve `dist/mainnet/` against a mainnet node.
   - The pill reads `Mainnet · <height>`, with no banner.
   - With a testnet (`deto1…`) wallet connected, every action is refused, and the refusal names the address's
     network.

Reconcile: none of these steps may produce a transaction. Check each test wallet's history.

Prior evidence: `UI TEST OK (mainnet-shaped)` on the final contracts (MT-D11, `.sim/ui-final-mainnet-0911`).

### T1 — XMR for a DERO-side asset: the Claim exit

The contract is `htlc.bas` v3 (`Lock`, `Claim`), with the joint Monero address. The companion is `tools/xmrswap`, run
once as Bob and once as Alice.

The DERO leg is DERO itself. `xmrswap` and `simhtlc` both take `-asset <scid>` and `-asset-decimals` for a token or
an NFA unit, but no run with a real token is recorded (MT-B04). On stage 1, repeat T1 once with a listed token as the
leg.

**Stage 1a:**

```
cd <repo>/tools
go run ./simhtlc -fake-monero -prover "<proof-xmr command>" -paths happy
go run ./simhtlc -regtest -prover "<proof-xmr command>" -paths happy     # after tools/regtest/monero-up.sh
```

**Stage 1b** is MT-B19's run (`tools/regtest/README.md`, "The stagenet swap run"):
1. Once the node is synced, run `tools/regtest/stagenet-wallets-up.sh --create`.
2. Fund alice by mining, and split her funds.
3. Run:
   ```
   cd <repo>/tools
   go run ./simhtlc -stagenet -htlc ef576fce2edaa15c3e09916463ea86a49bfb428ca030d439e69a63a48a612496 \
     -prover "<proof-xmr command>" -paths happy,refund,punish 2>&1 | tee <tmp>/stagenet-run.log
   ```

That one run covers T1, T2 and T3 on stage 1b. `simhtlc` refuses any daemon but the simulator.

**Stage 2** runs the command line as two processes, on the private testnet and stagenet:
1. Start the stagenet wallet-rpcs with `tools/regtest/stagenet-wallets-up.sh`.
2. Make the two DERO wallets with `netwallet -network testnet -daemon 127.0.0.1:40402 … -rpc-login <user>:<pw>
   -create`.
3. Run the two parties in two shells, with the same amounts on both sides:

```
# Bob's shell. He makes the channel key and hands it to Alice out of band.
cd <repo>/tools
export XMRSWAP_CHANNEL_KEY=$(<tmp>/xmrswap channel-key) VEIL_RPC_LOGIN=<bob's DERO wallet login>
<tmp>/xmrswap bob -state <test dir>/bob.json -channel <test dir>/channel -net stagenet \
  -daemon 127.0.0.1:40402 -dero 127.0.0.1:<bob's wallet port> -htlc a80f38f2587b144ee4064a21d529fbbd7da782d101169d1c65cb5cda4e1d0798 \
  -monero http://127.0.0.1:38087 -monero-name xmrswap-stagenet-bob -monerod 127.0.0.1:38081 \
  -prover <prover> -amount-dero 10000 -amount-xmr 10000000000
# Alice's shell
export XMRSWAP_CHANNEL_KEY=<the key from Bob> VEIL_RPC_LOGIN=<alice's DERO wallet login>
<tmp>/xmrswap alice -state <test dir>/alice.json -channel <test dir>/channel -net stagenet \
  -daemon 127.0.0.1:40402 -dero 127.0.0.1:<alice's wallet port> -htlc a80f38f2587b144ee4064a21d529fbbd7da782d101169d1c65cb5cda4e1d0798 \
  -monero http://127.0.0.1:38088 -monero-name xmrswap-stagenet-alice -monerod 127.0.0.1:38081 \
  -prover <prover> -amount-dero 10000 -amount-xmr 10000000000
```

- **`-htlc`.** The htlc scid is the working tree's testnet record (§1). Take it from the committed record once W-01
  lands, and drop the flag once `go generate ./xmrswap` has compiled that record in.
- **`<prover>`.** It is the absolute path of the pinned build. From Windows it is a wrapper pinned with
  `-prover-sha256`, as the regtest command-line run used (*to confirm*: the wrapper is not in the repository, and
  running the companions inside WSL instead needs WSL to reach the DERO node on Windows).
- **The joint wallets.** The stagenet wallet-rpcs keep their files in WSL, so `-monero-wallet-dir` is left out and
  the joint wallets stay in `$HOME/.dmz-regtest/stagenet-wallets`, encrypted. Delete them once the record is filed.

`xmrswap` refuses a wallet-rpc in any of these cases:
- it was started with `--restricted-rpc`;
- it was started without `--wallet-dir`;
- its wallet's address is not on `-net`;
- on mainnet only, it has no `--rpc-login`, it or the monerod is not loopback, or `-monerod` or `-monero-wallet-dir`
  is missing.

**Stage 3** uses the same commands, run on Linux with the release build, and with these changes:
- `-mainnet -net mainnet`;
- `-daemon 127.0.0.1:10102` and the mainnet DERO wallets;
- `-monerod 127.0.0.1:18081`, the mainnet wallet-rpcs, and `-monero-wallet-dir <that wallet-rpc's --wallet-dir>`;
- `-prover` the pinned build itself;
- `XMRSWAP_MONERO_LOGIN` and `XMRSWAP_MONERO_WALLET_PASSWORD` set;
- no `-htlc`, since the release's record names the mainnet install;
- the default timelocks and `-confs 20`.

Expected phases:
- **Bob:** `setup`, `verified_pi_a`, `dero_locking`, `dero_locked`, `stipend_sending`, `watching_xmr`,
  `xmr_confirmed`, `gate_sending`, `gate_sent`, `claimed`, `sweep_ready`, `sweeping`, then `swept` with `done` set.
- **Alice:** `setup`, `offer_checked`, `verified_pi_b`, `lock_announced`, `saw_dero_lock`, `xmr_locking`,
  `xmr_locked`, `gate_received`, `claiming`, `claim_confirming`, then `claimed` with `done` set.
- Bob prints his recovery card before his Lock and Alice hers before her XMR moves. Each registers its wakes, and both
  wakes are gone when the swap ends.

Chain checks:
- `htlc.bas` `st_<id>` = 1, and `xa_<id>` holds Alice's revealed secret.
- The stipend reached Alice's wallet at stable depth before her Monero lock.
- The Claim is stable below `t1_<id>`.
- Alice's lock to the joint address has at least 20 confirmations.
- Bob's sweep from the joint address is confirmed.

Reconcile:
- Alice's DERO rises by `v_<id>` plus the stipend, less her Claim fee.
- Bob's DERO falls by `v_<id>`, the stipend, and his Lock and stipend fees.
- Alice's XMR falls by the amount plus her transfer fee.
- Bob's XMR rises by the amount, less the sweep fee.
- The joint address is empty.

Prior evidence, not a stage-1 pass: `xmrswap-e2e.log` RUN 3 (v3 on the simulator with real regtest wallets) and
`xmrswap-cli.log` run 3 (the command line as two processes, through Bob's sweep).

### T2 — the Refund exit

This proves that when the gate is never released, the maker gets his DERO back and the taker gets her XMR back.

1. Run T1's commands.
2. Stop Bob's companion (Ctrl-C) once Alice reaches `xmr_locked`, before `gate_sent`.
3. Leave him stopped until the height passes `t1_<id>`, then run his command again.
4. He moves to `refund_window` and sends Refund while `t1 ≤ h < t2 − 11` (his refund cutoff, MT-B27), which publishes
   `s_b`, and counts it once it is stable (`refund_confirming`).
5. Alice's companion reads `s_b` and sweeps her XMR back from the joint address, passing through `await_refund`,
   `refund_seen`, `sweep_ready` and `sweeping` to `swept`.

Stage 1a: `go run ./simhtlc -fake-monero -prover "<proof-xmr command>" -paths refund`. At the defaults Bob's Refund
can go out only from `t1` to `t1 + 4`.

Chain checks:
- `st_<id>` = 2, and `xb_<id>` holds `s_b`.
- The Refund's height is in `[t1_<id>, t2_<id>)`, and Bob's log shows it was built below `t2 − 11`.

Reconcile:
- Bob's DERO is back, less his Lock and Refund fees and the stipend, which Alice keeps.
- Alice's XMR is back, less her transfer fee and the sweep fee.
- Bob's companion ends `refunded`, and Alice's ends `swept`.

Prior evidence: RUN 3's refund. It ran before the refund cutoff existed.

### T3 — the Punish exit

This proves the taker takes the maker's DERO when he neither releases the gate nor refunds.

It runs as T2, except that Bob stays stopped past `t2_<id>`.
- Alice's companion moves through `await_refund`, `punish_window`, `punishing` and `punish_confirming` to `punished`.
- She sends Punish at once from `t2`, and counts it once it is stable.
- Under `htlc.bas` v3 anyone may send Punish in `[t2, t2 + g2)`, and it always pays the taker.
- Bob, restarted afterwards, ends `punished`.

Stage 1a: `-paths punish`.

Chain checks:
- `st_<id>` = 3.
- The Punish's height is in `[t2_<id>, t2_<id> + g2)`.
- The DERO went to `t_<id>`.

Reconcile:
- Alice's DERO rises by `v_<id>` plus the stipend, less the Punish fee.
- Alice's XMR stays at the joint address, and she cannot spend it unless Bob ever reveals `s_b`.
  - **On mainnet that is the cost of this test: 0.01 XMR stranded.**
  - Both shares belong to the operator, in the two state files, so the joint key could be rebuilt by hand. No tool
    in this repository does that.
  - Record the stranded amount.

Prior evidence: RUN 3's punish.

### T3b — the Reclaim exit

This proves that when nobody punishes, the maker takes his DERO back from `t2 + g2`, and his Reclaim, which reveals
`s_b`, is what returns the taker's XMR.

1. Run T1's commands. Stop Bob once his lock and the stipend are in (`watching_xmr`).
2. Stop Alice once she is in `await_refund`, after her Monero lock and before `t2`.
3. Restart Bob at or past his refund cutoff `t2 − 11`, before `t2`.
   - He sends no Refund, and waits out `[t2, t2 + g2)` in `punish_window`.
   - He sends Reclaim from `t2 + g2` (`reclaiming`), and counts it once it is stable (`reclaim_confirming`).
   - He ends `reclaimed`.
4. Restart Alice at or after `t2 + g2`.
   - She sends no Punish, since she is past her cutoff, and moves to `await_reclaim`.
   - She reads `s_b` from `xb_<id>` once `st_<id>` is 4 (`refund_seen`), then sweeps her XMR back through
     `sweep_ready` and `sweeping` to `swept`.

Stage 1a is `go run ./simhtlc -fake-monero -prover "<proof-xmr command>" -paths reclaim`, or the same with
`-regtest`. It takes about 35 minutes at the defaults, and does steps 1–4 itself.

Stage 1b adds `-stagenet -htlc <the simulator's recorded htlc> -paths reclaim -timeout 30h`. There, t2 + g2 is the
lock + 5,620 simulator blocks, 22–28 h.

Stage 2 and stage 3 run steps 1–4 by hand with T1's commands.

Chain checks:
- `st_<id>` = 4, and `sha256(xb_<id>)` = `hb_<id>`.
- The Reclaim's height is at least `t2_<id> + g2`.
- No Refund and no Punish exist for the lock.
- The contract's balance is back to what it held before the lock.

Reconcile:
- Bob's DERO is back, less his Lock fee, the stipend and the Reclaim fee.
- Alice's XMR is back, less her transfer and sweep fees.
- Alice's DERO rose by the stipend.

What this test cannot show is a Bob who never reclaims. That leaves her XMR and his DERO locked for ever. It is
`XMR_SWAP.md` §10's stated risk, not a test.

Prior evidence:
- The contract's Reclaim, sent by hand on a scratch simulator install (`HTLC_V3_COMPANION_NOTES.md` §8; the logs are
  in `.sim/`, which git ignores).
- No companion run is recorded yet (X-08).

### T3c — the Punish deadline

This proves that the chain refuses a Punish at or after `t2 + g2`, and that the taker's companion sends none from its
cutoff, `t2 + g2 − (11 + 8 + claim margin)`.

1. Run T3b's steps 1 and 2.
2. Restart Alice at a height in `[her cutoff, t2 + g2)`: from `t2 + 4,773` on stages 2 and 3.
   - She sends no Punish, and moves to `await_reclaim`.
   - `punishTx` stays empty in her state.
3. At a height of at least `t2 + g2`, with the lock still open, send Punish by hand from a third wallet:
   ```
   curl -s -u <login> http://127.0.0.1:<wallet port>/json_rpc -d '{"jsonrpc":"2.0","id":"1","method":"transfer",
     "params":{"scid":"<htlc scid>","ringsize":2,"fees":3000,"sc_rpc":[
       {"name":"entrypoint","datatype":"S","value":"Punish"},{"name":"id","datatype":"U","value":<id>}]}}'
   ```
   It is mined and refused: `st_<id>` stays 0, and the sender pays the fee.
4. Continue with T3b's steps 3 and 4.

**Stage 1.** Run T1's command-line pair on the simulator, against an htlc a `simhtlc` run installed (it prints the
scid), with Monero on regtest:
- `-htlc <that scid> -t1 48 -t2 16 -t1-min 10 -t2-min 6 -gate-deadline 4 -g2-min 30 -net regtest`;
- `-monero http://127.0.0.1:28084` for Bob and `:28083` for Alice, and `-monerod 127.0.0.1:28081`;
- mine regtest blocks as `tools/regtest/README.md` shows while the companions run.

At `-claim-margin 8` her cutoff is then `t2 + 3`.

Chain checks:
- The hand-sent Punish is in a block at or above `t2 + g2`, and `st_<id>` is unchanged by it.
- No Punish from Alice's wallet exists at or after her cutoff.

Reconcile: the third wallet lost exactly its fee, and nothing else moved.

Prior evidence:
- `TestHtlcV3WindowMatrix`, in the DVM suite.
- On the scratch simulator install, a Punish mined after `t2 + g2` was refused (`HTLC_V3_COMPANION_NOTES.md` §8).
- The companion's cutoff: `TestTakerPastHerPunishCutoffWaitsForTheReclaim`.

### T4 — ZEC for a DERO-side asset: the Claim exit

The contracts are `board.bas` v3 (`Post`, `Accept`), `swap.bas` v2 (`Lock`, `Claim`) and a Zcash P2SH output. The
companion is `tools/zecswap`, run once as maker and once as taker.

**Stage 1a, the DERO leg:** `cd <repo>/tools && go run ./simswap`. It uses maker `30003`, taker `30004` and third
party `30005`, and does the following:
- installs `swap.bas` (`wmin` 20) and `board.bas` v3;
- locks, has a wrong secret refused, and Claims from the third wallet, which pays the taker;
- has a Lock naming no taker refused, and a late Claim refused;
- refunds;
- on the board: Post; an Accept with `cltv` 500,000,000, refused; an Accept; an early Cancel, refused; a Cancel after
  expiry; and a Cancel at the acceptance deadline, which takes about an hour (`-deadline=false` skips it).

**Stage 1a, both legs on regtest:** after `tools/regtest/zcash-up.sh`,
`cd <repo>/tools && ZECSWAP_REGTEST=1 go test -count=1 -run '^TestRegtestCLI$' -timeout 3h -v ./zecswap`. It runs the
built `zecswap` as separate maker, taker and status processes: one whole swap, then T5's refund. The `-timeout` is
*to confirm*: the test states no run time.

**Stages 1b and 2** use `TestTestnetSwap`, as in `ZEC_SWAP.md` §11.1 (the procedure, keys and funding). On stage 1b
its DERO leg is the simulator. On stage 2 it is the private testnet:

```
cd <repo>/tools
ZECSWAP_TESTNET_SWAP=1 ZECSWAP_TESTNET_MAKER_KEY=<keys-dir>/maker.key ZECSWAP_TESTNET_TAKER_KEY=<keys-dir>/taker.key \
ZECSWAP_TESTNET_DERO_DAEMON=127.0.0.1:40402 ZECSWAP_TESTNET_DERO_MAKER=127.0.0.1:<port> \
ZECSWAP_TESTNET_DERO_TAKER=127.0.0.1:<port> ZECSWAP_TESTNET_DERO_INSTALLER=127.0.0.1:<port> \
  PATH="$HOME/sdk/go1.26.8/bin:$PATH" GOTOOLCHAIN=local go test -count=1 -run '^TestTestnetSwap$' -timeout 40h -v ./zecswap
```

- It installs its own board and escrow (`wmin` 1,200), and writes every txid to
  `research/regtest/zecswap-testnet.log`.
- Its refund half waits for `T_dero`, 1,603 DERO blocks after the lock. That is about 6 h on the simulator, and about
  22–27 h at the private testnet's pace.
- The driver gives each process 10 h (`testnet_swap_test.go:56`). *To confirm*: on the private testnet the maker's
  refund may outlast that. If it does, restart the maker by hand (below) and it refunds after `T_dero`.

**By hand**, for T9's restarts on every stage, and for stage 3:

```
cd <repo>/tools
VEIL_RPC_LOGIN=<maker login> <tmp>/zecswap maker -network testnet -key <keys-dir>/maker.key \
  -zcash http://127.0.0.1:18232 -zcash-cookie <repo>/.sim/zcash-testnet/.cookie -verify-headers \
  -daemon 127.0.0.1:40402 -wallet 127.0.0.1:<maker port> -networks <repo>/deploy/networks.json \
  -give-amount 10000 -want-zat 1000000 -dir <test dir>/zmaker
VEIL_RPC_LOGIN=<taker login> <tmp>/zecswap taker -network testnet -key <keys-dir>/taker.key \
  -zcash http://127.0.0.1:18232 -zcash-cookie <repo>/.sim/zcash-testnet/.cookie -verify-headers \
  -daemon 127.0.0.1:40402 -wallet 127.0.0.1:<taker port> -networks <repo>/deploy/networks.json \
  -offer <the id the maker posted> -dir <test dir>/ztaker
<tmp>/zecswap status -zcash http://127.0.0.1:18232 -zcash-cookie <repo>/.sim/zcash-testnet/.cookie \
  -daemon 127.0.0.1:40402 -dir <test dir>/zmaker
```

- **The recorded contracts.** `-networks` points at the record, which gives `-board` and `-escrow` from the network's
  entry. Its default, `deploy/networks.json`, is relative to where the command runs.
- **Posting.** The maker posts the offer itself. To adopt an offer posted from the app, use
  `-offer <id> -secret-file <file>` instead of the two amounts.
- **The recorded testnet escrow's `wmin` is 1,600, the maker's own `T_dero` window.** zecswap sets `T_dero` 3 blocks
  above it, so a Lock mined more than 3 blocks after it was built is refused on chain. Record such a refusal as a
  finding.
- **Stage 3** uses `-network mainnet`, the operator's own zebrad, `-daemon 127.0.0.1:10102` and the mainnet wallets.
  There, `VEIL_RPC_LOGIN` is required.

Expected phases:
- **Maker:** `posting`, `posted`, `locking`, `locked`, `claiming`, `claimed`, `swapped`.
- **Taker:** `accepting`, `accepted`, `lock_seen`, `funding`, `funded`, `secret_seen`, `dero_claimed`, `swapped`.

Chain checks:
- `board.bas`: `st_<id>` = 1 after Accept, with `rp_<id>`, `cl_<id>` and `ad_<id>` set.
- `swap.bas`: `st_<id>` = 1 after Claim, and `x_<id>` holds the secret.
- The P2SH address is the one `zecswap script -hash <h_<id>> -claim <maker t-address> -refund <taker t-address>
  -cltv <cl_<id>> -tip <Zcash height> -net testnet` prints. On mainnet use `-net mainnet`, or `-tip 0` once funded.
- The funding had 10 confirmations before the maker's claim, and the claim was sent before `cltv − 20`.

Reconcile:
- The P2SH output was funded, then spent by the maker's claim.
- The secret in the Zcash spend equals `x_<id>`.
- The taker's DERO rose by `v_<id>`, less the Claim fee if she sent it herself.
- The maker's ZEC rose by the amount, less the claim fee.
- Each Zcash txid is also found on a public explorer.

Prior evidence: `zecswap-e2e.log`, `zecswap-cli.log`.

### T5 — the ZEC market's Refund exit

**The DERO side runs in `simswap`:**
1. A `swap.bas` Lock's expiry passes with no claim.
2. The maker sends `Refund(id)` at `h ≥ e_<id>`.
3. `simswap` also sends a late Claim, which the contract must refuse.

Check that `st_<id>` = 2 and that the maker's DERO is back, less fees.

**Both sides** are the second half of `TestRegtestCLI` and `TestTestnetSwap`. By hand, with T4's commands:
1. Stop the maker once he is `locked`, and let the taker fund.
2. From `cltv`, the taker's companion broadcasts her pre-signed refund (`refunding`), and ends `refunded`.
   - Anyone holding `<state>.refund.txt` can broadcast a level of it with `sendrawtransaction` from that height.
   - Before `cltv` it is refused.
3. Restart the maker after `T_dero`. He refunds the escrow (`refund_wait`, `dero_refunding`) and ends `refunded`.

Check:
- the output was spent by the refund branch above `cltv`, and the taker's ZEC is back, less the fee;
- `st_<id>` = 2, and the maker's DERO is back, less fees.

Prior evidence: `zecswap-e2e.log` swap C, which was refused before `cltv` and mined after it, and `zecswap-cli.log`'s
refund.

### T6 — an NFA purchase

**Stage 1:**

```
cd <repo>/tools
go run ./simnfa -factory <factory scid> -creator 10 -buyer 13 -treasury 2 -start -buy -price 1000 -royalty 5
```

Then open the NFA's page in the app.

**Stages 2 and 3, in the app:**
1. The creator mints an NFA priced in DERO, the only quote the mainnet bundle offers.
2. The creator lists it for sale at 0.01 DERO.
3. The buyer buys it.
4. Anyone presses Pay out for the treasury's owed share on the NFA's page (MT-D04). `treasurybot` does the same.

Chain checks:
- `owner` is the buyer.
- `previousSalePrice` = 1,000.
- `scBalance` = 0.
- After the sale, `owed` and the treasury's `ow_` key are 5. After Pay out, `owed` is 0, the treasury's `op_` key is
  5, and the contract holds no DERO.

Reconcile:
- The buyer holds 1 unit of the NFA's SCID, and their DERO fell by 1,000 plus gas.
- The creator, who is also the seller here, rose by the 50 royalty and the 945 payout.
- The treasury rose by 5, and only after Pay out.
- The app's NFA page shows the buyer as owner, and the sheet showed that same split before it was signed.

### T7 — a token trade

**Stage 1:**
- `PW_DIR=<playwright> tools/uitest/run.sh` launches a token, buys with "Post this trade" on, makes a private buy
  paid to `30006`, stakes, and registers a pass.
- `cd <repo>/tools && go run ./simsmoke && go run ./simlaunch` covers the contract paths the app does not drive.

**Stages 2 and 3, in the app,** on a listed DERO-quoted market:
1. Buy 0.01 DERO of the token, with "Post this trade" off.
2. Sell back what was bought.
3. Buy 0.01 DERO in private mode, paid to the second test wallet.

Chain checks:
- The pool's change in `rd`/`ra` equals the trade, and the new price point matches the chart.
- `DERO.GetTransaction` for the private buy shows a 16-member ring and no extracted signer.
- `tc` did not move for either the private trade or the unposted one.

Reconcile:
- The trader's DERO and token balances against the quote and the fees the sheet showed.
- The receiving wallet's token balance.
- The app said "confirmed" only for the trades it checked against a balance. For the private buy paid elsewhere
  it said "included in block …: this app did not check that it executed".

### T8 — an intentional timeout

**T8a — ZEC market, DERO side.** A board offer is accepted and the maker never locks.
- From `ad_<id>` (the accept height + 200) or `e_<id>`, whichever comes first, the maker's `Cancel` succeeds and the
  board's `st_<id>` = 2.
- A `Cancel` of the accepted offer before then is refused, and `st_<id>` stays 1.
- `simswap` drives both. By hand, the taker's companion ends `walked` after `P_lock`.

**T8b — Monero.** Alice's companion is stopped after `lock_announced`, before `xmr_locking`, and is not restarted.
- Bob's companion reaches `refund_window` at `t1_<id>` and refunds before `t2 − 11`.
- Check that `st_<id>` = 2, Bob's DERO is back less his fees and the stipend, and no Monero transaction exists.
- Then restart Alice after `t1`. She must lock nothing, and must end `aborted` ("bob refunded before any XMR was
  locked") or `refused`.

**T8c — the app.** Start a trade and leave the wallet's prompt unanswered for 10 minutes. The app must:
- say "the wallet has not answered in 10 minutes; if you approve it later the transaction is still sent";
- refuse every new transaction while that one is unanswered ("A transaction is still waiting for the wallet's
  answer");
- after a late approval, report the late transaction's id, and send nothing twice.

### T9 — a restart of each companion mid-swap

At each phase below, kill the process hard (`taskkill /F`, or `kill -9`), then run the same command again.
Reconciliation must show:
- exactly one Lock for the swap;
- exactly one Monero transfer to the joint address, or one Zcash funding;
- a final phase the chain supports.

A kill between a sent exit and its record may cost one extra fee: the second call is mined and refused, because
`st_<id>` is no longer 0.

Bob's companion:

| Kill in | Expected on restart |
|---|---|
| `dero_locking` (Lock sent, not yet seen) | waits out the settle blocks, then finds the existing lock; `Lock` never runs twice (XMR-02) |
| `stipend_sending` | waits 12 blocks, finds the stipend in his wallet's history; it is never paid twice |
| `watching_xmr` | resumes watching |
| `gate_sending` | re-sends the gate |
| `sweep_ready` | sweeps once |
| `sweeping` | the joint spend wallet's own record decides: the sweep is recorded, or made once (MT-B12) |
| `refunding` | re-sends Refund only below `t2 − 11` |
| `refund_confirming` | watches the Refund to stable depth; if a replaced block undid it, re-sends only below the cutoff, and otherwise leaves the lock to Punish or his Reclaim |
| `reclaiming` | sends Reclaim again if the lock is still open |
| `reclaim_confirming` | watches the Reclaim to stable depth, and re-sends it if it was undone |

Alice's companion:

| Kill in | Expected on restart |
|---|---|
| `xmr_locking` | resumes as "lock unknown" and never pays a second time |
| `claiming` | re-sends the Claim only while still below the claim cutoff |
| `claim_confirming` | watches the Claim to stable depth; if it was undone, back to `claiming` below the cutoff, otherwise `await_refund` |
| `await_refund` | resumes watching for the Refund, or for `t2` |
| `punishing` | re-sends Punish only below her punish cutoff |
| `punish_confirming` | watches the Punish to stable depth; re-sends below the cutoff if it was undone |
| `await_reclaim` | sends no Punish; waits for `st_<id>` 4, then reads `s_b` |
| `sweeping` | sweeps once (MT-B12) |

`zecswap`, on T4's by-hand runs:

| Kill in | Expected on restart |
|---|---|
| maker `posting` | looks for the offer under the stored hash before posting again |
| maker `locking` | waits for the Lock to settle and looks for it before sending another |
| maker `claiming` | sends the same stored claim while the cutoff is open, never after it |
| maker `claimed` | finishes; if the taker never claims, refunds the escrow after `T_dero` |
| maker `dero_refunding` | reads the escrow and sends Refund again only if it is still open |
| taker `accepting` | reads the board before sending another Accept |
| taker `funding` | sends the same funding until it expires; the refund file is already written |
| taker `funded` | resumes watching; from `cltv` broadcasts her refund |
| taker `secret_seen` / `dero_claimed` | claims the escrow before `T_dero`, once; then checks the Claim once it is stable |
| taker `refunding` | re-broadcasts the refund until it is deep |

Also:
- Start two copies of one party against one state file. The second must refuse, because of the lock file
  `<state>.lock` (XMR-04; zecswap the same).
- Run each kill on stages 1 and 2. On mainnet, run only the kills stage 2 proved safe, and never kill a mainnet
  companion in `sweeping` or `funding`.

Prior evidence: both companions' crash fuzz (`TestCrashFuzz`) kills after every durable step. A live kill is not yet
recorded for any phase added since v3.

### T10 — a duplicated message

**`xmrswap` channel.** Each party reads `<channel>/<its name>/` oldest-first and deletes each file it reads. Every
message is sealed under the swap's channel key (GAP-019).
- Copy a delivered message (`dero_locked`, or `gate`) back into the receiver's inbox under a new, later name. It
  must be dropped as already consumed, and nothing must happen twice.
- Watch the companion's own repeats: its last message is re-sent every minute until the peer moves on, and each
  repeat is dropped.
- Put in an inbox a file written without the channel key, or a copy of a message back into its own sender's inbox.
  It must be dropped and reported.
- Put a malformed file into an inbox. It must be renamed with a `.bad` suffix, and nothing must happen.

**The ZEC market, on chain.** A second `Accept` on the same board offer, and a second `Claim` on the same `swap.bas`
lock, must both be refused because `st_<id>` is no longer 0.
- The refused call still costs its fee.
- No companion may send either call. Send them by hand with the wallet RPC, in the form of T3c step 3 (`Accept` takes
  `id`, `refundPkh`, `cltv`; `Claim` takes `id`, `secret`), to prove the contract refuses them.

**The app.** Press the confirm button twice, or send while a transaction is in flight. The app must refuse the
second one with "A transaction is already in progress".

## 7. After the plan

- **Update the swap status sentence** in `app/wrapui.js` (the Wrap tab's no-vault screen) and its twin on the ZEC swap
  screen in `app/zswap.js`.
  - `wrapui.js` reads today: "Both programs have completed swaps, every exit included, against real Monero and Zcash
    node software on a local test chain (regtest); neither has yet run on a public test network."
  - `zswap.js` reads: "The swap programs have completed every exit against real Monero and Zcash node software on a
    local test chain; neither has yet run on a public test network."
  - Under `htlc.bas` v3, "every exit" is not yet true of the Monero swap: its Reclaim has not run through the
    companions. Correct it now, or once T3b has run on stage 1.
  - Update it again once a swap has run on Monero stagenet or Zcash testnet, and again once T1 has passed on mainnet.
  - It must say only what those tests showed.
- **File every record** in `docs/history/HISTORY.md`.
- **Retire the mainnet test wallets.** Keep their seeds until every record is reconciled.
- **Tick this plan's go/no-go row** in `deploy/README.md` only when every stage-3 test that is not blocked has a
  clean record.
