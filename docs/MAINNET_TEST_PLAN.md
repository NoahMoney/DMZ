# DMZ mainnet test plan

This is the controlled live test sequence for DMZ, written for the system as it is built. There is no custodian,
so nothing is deposited into DMZ and nothing is withdrawn from it: there is nothing to test on that axis. What a
holder does is swap XMR or ZEC for a DERO-side asset, buy an NFA, and trade a token. Those are the tests, together
with the failures a holder will meet: a deadline that passes, a companion restarted in the middle of a swap, a
message that arrives twice.

Every test runs three times, in this order, and a stage does not start until the one before it is reconciled clean:

1. the DERO simulator;
2. the test networks: the private DERO testnet, Monero stagenet and Zcash testnet;
3. mainnet, at the smallest amounts in §5.

No test uses a production key. Every test ends with the reconciliation in §4.

Written 2026-09-11 against branch `wip/2026-09-09-swap-nfa` (0fba2c6 plus the readiness pass). The companions
are still changing, so before each stage, check every command below against `-h` at the commit under test.

---

## 1. What must exist before each stage

| Needed | State on 2026-09-11 | Blocks |
|---|---|---|
| DERO simulator: daemon `127.0.0.1:20000`, 22 wallets on `30000`–`30021` with 8 DERO each (`deploy/README.md`, simulator §2) | exists | — |
| `htlc.bas`, `swap.bas`, `board.bas` on the simulator | installed per run: `tools/simhtlc` installs `htlc.bas` (through `reference.Canon`), and `tools/simswap` installs `swap.bas` and `board.bas` | — |
| An in-process fake Monero wallet | exists, but only inside `tools/simhtlc -fake-monero`; `cmd/xmrswap` has no flag for it | — |
| `proof-xmr`, the prover the Monero swap calls (`research/crosschain/poc/proof-xmr`) | builds (on Windows through WSL: `research/crosschain/poc/wsl/build.sh proof-xmr build --release`); **not externally audited** | stage 3 T1–T3, until an external audit (study §0) |
| Monero stagenet: `monerod --stagenet` and two `monero-wallet-rpc --stagenet --wallet-dir <dir>` | **does not exist in this environment** (audit, "Not performed"; BL-11) | T1–T3, T8b, T9, T10 on stages 1b and 2 |
| Private DERO testnet (`derod --testnet`, RPC `127.0.0.1:40402`, wallets `40403`–`40405`; `deploy/README.md`, testnet) | exists; carries factory, social and founders only | — |
| `htlc.bas`, `swap.bas`, `board.bas` on the DERO testnet | **does not exist**, and no tool installs them outside the simulator | T1–T5, T8–T10 on stage 2 |
| Zcash testnet node with an address index (zcashd `-addressindex`, zebrad, or lightwalletd) | **does not exist** | T4, T5 |
| `tools/zecswap` node client, watcher and broadcast | **not built** (BL-40b). The CLI prints the P2SH script and decodes an address, nothing else; it holds no key, keeps no state and talks to no node | the Zcash leg of T4, T5 and T9 on every stage |
| DERO mainnet: factory, social, founders and the TELA INDEX, then `htlc.bas`, `swap.bas` and `board.bas` | nothing deployed | stage 3 |
| The study's pre-mainnet items: an `htlc.bas` v2 that closes LIV-07, the maker's gas stipend, `swap.bas` v2 and `board.bas` v3, the pre-signed Zcash refund | not built; they wait on operator decisions 15–17 | stage 3 |

What that means today:
- Stage 1 can run T0, T1–T3 on the fake wallet, T6, T7, T8a, T8c, T9 and T10 for `xmrswap`, and the DERO leg of
  T4 and T5.
- Stage 1b needs the two stagenet processes.
- Stage 2 needs every row above marked "does not exist" or "not built".
- **No Zcash leg can run anywhere until BL-40b lands.** T4 and T5 list their Zcash steps anyway, so that on the day
  `zecswap` can broadcast, the test is already written. Until then they are recorded as blocked, never as passed.

## 2. Stages, and the gate between them

| Stage | DERO | Monero | Zcash | Timelocks |
|---|---|---|---|---|
| 1a | simulator | the fake wallet in `simhtlc` | — | lowered: `simhtlc` defaults `-t1 28 -t2 8 -t1-min 10 -t2-min 6` |
| 1b | simulator | stagenet | — | may be lowered: `xmrswap` accepts `-t1-min`/`-t2-min` below 400 only when the DERO daemon reports network `Simulator` |
| 2 | private testnet | stagenet | testnet | the floors: `t1 − now ≥ 400` and `t2 − t1 ≥ 400` DERO blocks (about 2 h each at 18 s), 20 Monero confirmations |
| 3 | mainnet | mainnet | mainnet | the same floors, unchanged |

A stage passes only when both of these hold:
- every test in it that is not blocked (§1) has run;
- every run is reconciled (§4) with no unexplained difference.

The records go into `docs/history/HISTORY.md` with every txid on every chain. A blocked test is recorded as blocked,
naming the row of §1 that blocks it.

On mainnet, tests run one at a time, and each is reconciled before the next begins.

## 3. Wallets and keys

| Role | Stage 1 (simulator) | Stage 2 (testnets) | Stage 3 (mainnet) |
|---|---|---|---|
| Swap maker "Bob": holds DERO, locks first, ends with XMR | `30006` | a new `netwallet` testnet wallet | a new wallet made for this plan |
| Swap taker "Alice": holds XMR, ends with DERO | `30007` | a second new testnet wallet | a second new wallet |
| ZEC market maker / taker | `30003` / `30004` (`simswap -a`/`-b`) | two more testnet wallets | two more new wallets |
| NFA creator / buyer | `30010` / `30013` (`simnfa -creator`/`-buyer`) | two testnet wallets | two new wallets |
| Token trader / private-mode receiver | `30001` / `30016` | two testnet wallets | two new wallets |
| Treasury (read, never a signer) | `30002` | testnet treasury `40404` | the mainnet treasury, **read only** |
| Monero | the fake wallet | two stagenet wallets, each in its own `--wallet-dir`, funded from stagenet | two new mainnet wallets holding only the test amount |
| Zcash | — | two transparent testnet addresses (`tm…`) | two new transparent addresses (`t1…`) |

Rules:
- **Never a production key.** The owner, treasury and deployer wallets in `deploy/README.md` (mainnet) never sign
  a test transaction.
- **The app signs through a real wallet** with XSWD on `localhost:44326`, such as Engram or `dero-wallet-cli` with
  XSWD on, opened on the test wallet. `xswdwallet` approves everything it is asked. It is for the simulator only
  and must never open a wallet that holds value.
- **Alice's DERO wallet is funded in advance.** She pays her own Claim fee in DERO, because the maker's gas stipend
  is not built. A holder arriving with only XMR could not claim.
- **Fund and retire stage 3 wallets tightly.**
  - Fund each test wallet with the amount in §5 plus its fees, and no more.
  - Write its seed down off the machine.
  - Retire the wallet when the plan ends.
- **Treat `xmrswap` state files as secrets.**
  - Each state file holds that party's key share in plaintext. After a Claim or Refund has revealed the other
    share, it holds both halves of the joint Monero spend key.
  - Keep every state file and channel directory in a directory made for the test, outside the repository.
    `.gitignore` covers `bob.json`, `alice.json`, `*.json.lock`, `xmrswap-used.json` and `swap-channel/` in case
    one lands inside it.
  - The `0600` file mode is not enforced on Windows, and the program warns about this. Use an encrypted volume.
  - Delete a state file only after its swap is `done` and its record is filed.
- **Separate the parties' accounts.** `-monero-pass` puts the wallet-rpc password on the process command line
  (XMR-5), so on stage 3 each party's companion runs under its own OS account.

## 4. Reconciliation after every test (directive §40)

Take readings only when:
- the last transaction of the test is at least 8 DERO blocks deep;
- anything that moved on Monero has at least 20 confirmations.

Then read the same facts from four places. **Any difference between them is a finding.** Stop the stage, and record
the difference and its cause before anything else runs.

| Source | What to read | How |
|---|---|---|
| The chains | every transaction the test sent, and every contract key it moved | DERO: `DERO.GetTransaction {"txs_hashes":["<txid>"]}` (`valid_block`, `in_pool` false, `block_height`); `DERO.GetSC {"scid":"<scid>","keysstring":[…]}` for the keys the test names. Monero: each wallet-rpc's `get_transfer_by_txid`, and the view wallet the companion made for the joint address. Zcash: `getrawtransaction <txid> 1` and `getaddressbalance` on an address-indexed node |
| Each person's wallet | the balance of every asset the test touched, before and after | DERO: `GetBalance` on each test wallet, with `"scid"` for a token, NFA unit or LP and without it for DERO. Monero: `get_balance`. Zcash: the address balance |
| Each companion's state | `phase`, `outcome`, `done`, `lock` (`id`, `tx`), `claimTx`, `refundTx`, `punishTx`, `revealed`, `sweep` | open each party's `-state` file (JSON) |
| What the app shows | every screen that shows what the test moved | a screenshot per screen, filed with the record |

What must agree:
- **Wallet balances.** Each wallet's change equals what the chain says moved, less exactly the fees the chain
  charged.
- **Companion outcomes.** A companion's outcome is the one the chain shows:
  - `claimed` or `swept` only if `st_<id>` is 1 and the sweep's txid is confirmed;
  - `refunded` only if `st_<id>` is 2;
  - `punished` only if `st_<id>` is 3.
- **The app.** No screen shows a balance, an owner, a listing, a fee or a finished state that the chain does not.
  The app has no swap screen, so a swap reaches it only as a balance of the DERO-side asset on Portfolio.

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
vault, so the app offers no other quote.

| Test | Stage 1 | Stage 2 | Stage 3 (mainnet) |
|---|---|---|---|
| T1–T3, T8b, T9, T10 (Monero) | 1a: `simhtlc` defaults, 2,000 atomic DERO against 1 fake XMR. 1b: 10,000 atomic (0.1 DERO) against 0.01 XMR | 0.1 DERO against 0.01 XMR | **0.1 DERO against 0.01 XMR** per swap |
| T4, T5, T8a (Zcash market) | `simswap`'s 100,000 and 50,000 atomic DERO; the board asks 25,000,000 zats | 0.1 DERO against 0.01 TAZ | 0.1 DERO against 0.01 ZEC, well above the ZIP 317 fee floor of 10,000 zats |
| T6 (NFA) | `simnfa -price 1000 -royalty 5` | price 0.01 DERO | **price 0.01 DERO** (1,000 atomic) |
| T7 (token) | the simulator's markets | 0.01 DERO | **a 0.01 DERO buy**, a sale of what was bought, and one 0.01 DERO private buy |

What the stage 3 wallets need on top of those amounts:
- **Monero swaps.** Each DERO wallet holds 0.5 DERO:
  - Lock and Claim pay the gas estimate plus 200.
  - Refund and Punish pay a fixed 3,000 atomic units (0.03 DERO), re-sent at double the fee up to twice.
  - The Monero wallet holds 0.01 XMR plus its transfer fee.
- **NFA.** At 1,000 atomic units, the 5% royalty is 50 and the 0.5% protocol fee is 5, so each payee's share can be
  reconciled to the unit. The creator also holds enough DERO for the install and the registration; measure the
  install on the simulator first.

## 6. The tests

Run them in this order in each stage: T0, T7, T6, T1, T2, T3, T8, T9, T10, then T4 and T5 once they are unblocked.
On mainnet, T2 and T3 run only after T1 is reconciled.

### T0 — the app's network gate (no funds)

This proves the app never reads one network and signs on another without saying so.

1. Run `tools/withlock.sh ui env PW_DIR=<playwright> node tools/uitest/ui.mjs <app url> .sim/ui-mainnet --mainnet`
   and read the `mainnet-390-*.png` and `mainnet-1024-*.png` files it writes.
   - The Wrap tab opens with "Wrapped XMR and ZEC are not offered on this network" and names the federation and
     the atomic swap. It draws no control.
   - The banner under the header names both networks.
   - The run ends `UI TEST OK (mainnet-shaped)`.
2. On stage 2, serve `dist/mainnet/` against the testnet node.
   - The pill is red, and the banner names both networks on every screen.
   - Any action answers "Refusing: …" before the wallet is asked.
3. On stage 3, serve `dist/mainnet/` against a mainnet node.
   - The pill reads `Mainnet · <height>`, with no banner.
   - With a testnet (`deto1…`) wallet connected, every action is refused, and the refusal names the address's
     network.

Reconcile: none of these steps may produce a transaction. Check each test wallet's history.

### T1 — XMR for a DERO-side asset: the Claim exit

The contract is `htlc.bas` (`Lock`, `Claim`), with the joint Monero address. The companion is `tools/xmrswap`, run
once as Bob and once as Alice.

The DERO leg is DERO itself today. `htlc.bas` accepts a token or an NFA unit as its `asset`, but `cmd/xmrswap` has
no flag for one yet (the study's asset leg). Once it has, repeat T1 with a listed token as the leg.

**Stage 1a:**

```
cd tools
go run ./simhtlc -fake-monero -prover "<proof-xmr command>" -paths happy
```

**Stages 1b and 2.** First start the Monero side. Fund the two stagenet wallets from stagenet before starting. The
stagenet daemon's RPC is `38081`.

```
monerod --stagenet --data-dir <test dir>/monerod
monero-wallet-rpc --stagenet --daemon-address 127.0.0.1:38081 --wallet-dir <test dir>/bob   --rpc-bind-port 38082 --rpc-login bob:<pw>
monero-wallet-rpc --stagenet --daemon-address 127.0.0.1:38081 --wallet-dir <test dir>/alice --rpc-bind-port 38083 --rpc-login alice:<pw>
```

`xmrswap` refuses a wallet-rpc in any of these cases:
- it was started with `--restricted-rpc`;
- it was started without `--wallet-dir`;
- its wallet's address is not on `-net`.

Then the DERO side:
- **Stage 1b:** the simulator, with the `htlc.bas` a `simhtlc` run installed.
- **Stage 2:** the testnet, with `htlc.bas` installed by hand as in `deploy/README.md`, simulator §4. `htlc.bas`
  takes no Initialize arguments.
  - Estimate the install first, to be sure the file fits the 20,000-unit storage-gas ceiling.
  - The tools install the comment-stripped form (`reference.Canon`), and none of them does so outside the
    simulator yet.

Then run the two parties in two terminals, with the same amounts on both sides:

```
cd tools
go run ./xmrswap/cmd/xmrswap bob   -state <test dir>/bob.json   -channel <test dir>/channel -htlc <scid> \
  -dero <Bob's DERO wallet rpc>   -daemon <DERO daemon> -monero http://127.0.0.1:38082 -monero-user bob   -monero-pass <pw> \
  -monero-name <Bob's wallet file>   -net stagenet -prover "<proof-xmr command>" -amount-dero 10000 -amount-xmr 10000000000
go run ./xmrswap/cmd/xmrswap alice -state <test dir>/alice.json -channel <test dir>/channel -htlc <scid> \
  -dero <Alice's DERO wallet rpc> -daemon <DERO daemon> -monero http://127.0.0.1:38083 -monero-user alice -monero-pass <pw> \
  -monero-name <Alice's wallet file> -net stagenet -prover "<proof-xmr command>" -amount-dero 10000 -amount-xmr 10000000000
```

**Stage 3** uses the same commands with `-net mainnet`, mainnet wallet-rpc processes (no `--stagenet`), the mainnet
DERO daemon and wallets, the default timelocks, and `-confs 20`.

Expected phases:
- **Bob:** `setup`, `dero_locked`, `watching_xmr`, `xmr_confirmed`, `gate_sent`, `claimed`, `sweep_ready`, then
  `swept` with `done` set.
- **Alice:** `setup`, `saw_dero_lock`, `xmr_locked`, `gate_received`, `claiming`, then `claimed` with `done` set.

Chain checks:
- `htlc.bas` `st_<id>` = 1, and `xa_<id>` holds Alice's revealed secret.
- The Claim's height is below `t1_<id>`.
- Alice's lock to the joint address has at least 20 confirmations.
- Bob's sweep from the joint address is confirmed.

Reconcile:
- Alice's DERO rises by `v_<id>`, less her Claim fee.
- Bob's DERO falls by `v_<id>` plus his Lock fee.
- Alice's XMR falls by the amount plus her transfer fee.
- Bob's XMR rises by the amount, less the sweep fee.
- The joint address is empty.

### T2 — the Refund exit

This proves that when the gate is never released, the maker gets his DERO back and the taker gets her XMR back.

1. Run T1's commands.
2. Stop Bob's companion (Ctrl-C) once Alice reaches `xmr_locked`, before `gate_sent`.
3. Leave him stopped until the height passes `t1_<id>`, then run his command again.
4. He moves to `refund_window` and sends Refund while `t1 ≤ h < t2`, which publishes `s_b`.
5. Alice's companion reads `s_b` and sweeps her XMR back from the joint address, passing through `await_refund`,
   `refund_seen` and `sweep_ready` to `swept`.

Stage 1a: `go run ./simhtlc -fake-monero -prover "<proof-xmr command>" -paths refund`.

Chain checks:
- `st_<id>` = 2, and `xb_<id>` holds `s_b`.
- The Refund's height is in `[t1_<id>, t2_<id>)`.

Reconcile:
- Bob's DERO is back, less his Lock and Refund fees.
- Alice's XMR is back, less her transfer fee and the sweep fee.
- Bob's companion ends `refunded`, and Alice's ends `swept`.

### T3 — the Punish exit

This proves the taker takes the maker's DERO when he neither releases the gate nor refunds.

It runs as T2, except that Bob stays stopped past `t2_<id>`. Alice's companion moves through `await_refund`,
`punish_window` and `punishing`, and sends Punish at `h ≥ t2`. Under `htlc.bas` v2 anyone may send Punish, and it
always pays the taker.

Stage 1a: `-paths punish`.

Chain checks:
- `st_<id>` = 3.
- The DERO went to `t_<id>`.

Reconcile:
- Alice's DERO rises by `v_<id>`, less the Punish fee.
- Alice's XMR stays at the joint address, and she cannot spend it unless Bob ever reveals `s_b`.
  - **On mainnet that is the cost of this test: 0.01 XMR stranded.**
  - Both shares belong to the operator, in the two state files, so the joint key could be rebuilt by hand. No tool
    in this repository does that.
  - Record the stranded amount.

### T4 — ZEC for a DERO-side asset: the Claim exit

The contracts are `board.bas` (`Post`, `Accept`), `swap.bas` (`Lock`, `Claim`) and a Zcash P2SH output. The DERO leg
runs today; the Zcash leg is blocked on BL-40b.

**The DERO leg, stage 1:** `cd tools && go run ./simswap`. It installs `swap.bas` and `board.bas`, then drives Post,
Accept, Lock and Claim from `30003` (maker) and `30004` (taker).

Chain checks:
- `board.bas`: `st_<id>` = 1 after Accept, with `rp_<id>` and `cl_<id>` set.
- `swap.bas`: `st_<id>` = 1 after Claim, and `x_<id>` holds the secret.

**The Zcash leg, stage 2 onwards — blocked on BL-40b:**
1. The taker derives the script:
   `go run ./zecswap/cmd/zecswap script -hash <h_<id> from the board> -claim <maker t-address> -refund <taker t-address> -cltv <cl_<id>> -tip <zcash height> -net testnet`
2. The taker funds the P2SH address it prints.
3. The maker spends that output with the secret, which puts the secret on Zcash.
4. The taker claims `swap.bas` with the secret.

Steps 3 and 4 need a Zcash node client and a broadcast. `zecswap` has neither.

Reconcile, once it runs:
- The P2SH output was funded, then spent by the maker's claim.
- `swap.bas` `st_<id>` = 1.
- The taker's DERO rose by `v_<id>`, less the Claim fee.
- The secret in the Zcash spend equals `x_<id>`.

### T5 — the ZEC market's Refund exit

**The DERO side runs today, in `simswap`.**
1. A `swap.bas` Lock's expiry passes with no claim.
2. The maker sends `Refund(id)` at `h ≥ e_<id>`.
3. `simswap` also sends a late Claim, which the contract must refuse.

Check that `st_<id>` = 2 and that the maker's DERO is back, less fees.

**The Zcash side is blocked on BL-40b.** It is the taker's refund of the P2SH output after `cltv`. When it runs,
check that the output was spent by the refund branch and that the taker's ZEC is back, less the fee.

### T6 — an NFA purchase

**Stage 1:**

```
cd tools
go run ./simnfa -factory <factory scid> -creator 10 -buyer 13 -treasury 2 -start -buy -price 1000 -royalty 5
```

Then open the NFA's page in the app.

**Stage 3, in the app:**
1. The creator mints an NFA priced in DERO, the only quote the mainnet bundle offers.
2. The creator lists it for sale at 0.01 DERO.
3. The buyer buys it.

Chain checks:
- `owner` is the buyer.
- `previousSalePrice` = 1,000.
- `scBalance` = 0.

Reconcile:
- The buyer holds 1 unit of the NFA's SCID, and their DERO fell by 1,000 plus gas.
- The creator's DERO rose by 50 (the royalty) and the treasury's by 5 (the protocol fee).
- The seller received the rest.
- The app's NFA page shows the buyer as owner, and the sheet showed that same split before it was signed.

### T7 — a token trade

**Stage 1:**
- `tools/uitest/run.sh` launches a token, buys with "Post this trade" on, makes a private buy paid to `30006`,
  stakes, and registers a pass.
- `go run ./simsmoke` and `go run ./simlaunch` cover the contract paths the app does not drive.

**Stage 3, in the app,** on a listed DERO-quoted market:
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

**T8a — ZEC market, DERO side (runs today).** A board offer is accepted and the maker never locks.
- After `e_<id>`, the maker's `Cancel` succeeds and the board's `st_<id>` = 2.
- A `Cancel` before expiry is refused.
- `simswap` drives both.

**T8b — Monero.** Alice's companion is stopped after `lock_announced`, before `xmr_locking`, and is not restarted.
- Bob's companion reaches `refund_window` at `t1_<id>` and refunds.
- Check that `st_<id>` = 2, Bob's DERO is back less fees, and no Monero transaction exists.
- Then restart Alice after `t1`. She must lock nothing (she will not lock at `h ≥ t1`) and must end `aborted` or
  `refused`.

**T8c — the app.** Start a trade and leave the wallet's prompt unanswered for 10 minutes. The app must:
- say the wallet has not answered;
- refuse every new transaction while that one is unanswered;
- after a late approval, report the late transaction's id, and send nothing twice.

### T9 — a restart of each companion mid-swap

At each phase below, kill the process hard (`taskkill /F`, or `kill -9`), then run the same command again.
Reconciliation must show:
- exactly one Lock for the swap;
- exactly one Monero transfer to the joint address;
- a final phase the chain supports.

Bob's companion:

| Kill in | Expected on restart |
|---|---|
| `dero_locking` (Lock sent, not yet seen) | waits 3 blocks from the attempt, then finds the existing lock; `Lock` never runs twice (XMR-02, repaired) |
| `watching_xmr` | resumes watching |
| `gate_sending` | re-sends the gate |
| `sweep_ready` | sweeps once |
| `sweeping` | **ends `sweep_unknown` and does not retry** (open finding XMR-R1); recovery is by hand from the state file |

Alice's companion:

| Kill in | Expected on restart |
|---|---|
| `xmr_locking` | resumes as "lock unknown" and never pays a second time |
| `claiming` | re-sends the Claim only while still inside its margin below `t1` |
| `await_refund` | resumes watching for the Refund, or for `t2` |

Also:
- Start two copies of one party against one state file. The second must refuse, because of the lock file
  `<state>.lock` (XMR-04, repaired).
- `zecswap` has no running process and no state to restart. This part is blocked along with the rest of the Zcash
  leg.
- Run the `sweeping` kill on stages 1 and 2 only, to rehearse the manual recovery. **Never kill a mainnet companion
  in `sweeping`.** On mainnet, run only the kills that stage 2 proved safe.

### T10 — a duplicated message

**`xmrswap` channel.** Each party reads `<channel>/<its name>/` oldest-first and deletes each file it reads.
- Copy a delivered message (`dero_locked`, or `gate`) back into the receiver's inbox under a new, later name. It
  must be dropped as already consumed, and nothing must happen twice.
- Watch the companion's own repeats: its last message is re-sent every minute until the peer moves on, and each
  repeat is dropped.
- Put a malformed file into an inbox. It must be renamed with a `.bad` suffix, and nothing must happen.

**The ZEC market, on chain** (runs today on stage 1). A second `Accept` on the same board offer, and a second
`Claim` on the same `swap.bas` lock, must both be refused because `st_<id>` is no longer 0.
- The refused call still costs its fee.
- No companion may send either call. Send them by hand with the wallet RPC to prove the contract refuses them.

**The app.** Press the confirm button twice, or send while a transaction is in flight. The app must refuse the
second one with "A transaction is already in progress".

## 7. After the plan

- **Update the swap status sentence** in `app/wrap.js` and its twin on the ZEC swap screen in `app/zswap.js`. Today
  it reads: "Both programs have completed swaps, every exit included, against real Monero and Zcash node software on
  a local test chain (regtest); neither has yet run on a public test network."
  - Update it once a swap has run on Monero stagenet or Zcash testnet, and again once T1 has passed on mainnet.
  - It must say only what those tests showed.
- **File every record** in `docs/history/HISTORY.md`.
- **Retire the mainnet test wallets.** Keep their seeds until every record is reconciled.
- **Tick this plan's go/no-go row** in `deploy/README.md` only when every stage-3 test that is not blocked has a
  clean record.
