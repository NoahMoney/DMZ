# DMZ regtest harness — real Monero and Zcash nodes, local only

DMZ's per-user companions (`tools/xmrswap`, `tools/zecswap`) had only ever run against in-process fakes. The
operator's rules: never ship simulated XMR/ZEC verification, and never touch mainnet. This directory brings up
**real, pinned, signature-verified node software** in local regression-test mode, with one command per chain, and
proves the node operations the companions rely on against it. It does not integrate the companions.

Nothing here can reach a public network. monerod runs `--regtest --offline` with P2P bound to 127.0.0.1. zebrad runs
`network = "Regtest"` with no peers configured and P2P on 127.0.0.1, and every smoke refuses a node whose chain is not
regtest (monerod `nettype` `fakechain`; Zebra's regtest genesis block `029f11d8…e327`).

## What runs

| | Software | Endpoint (from Windows) | Auth | Data |
|---|---|---|---|---|
| Monero daemon | monerod 0.18.5.1 (official linux-x64 release) | `http://127.0.0.1:28081` (P2P 127.0.0.1:28080, offline) | none | `.sim/regtest/monero/chain` |
| Monero wallet A | monero-wallet-rpc 0.18.5.1 | `http://127.0.0.1:28083/json_rpc` | none (`--disable-rpc-login`) | `.sim/regtest/monero/wallets` |
| Monero wallet B | monero-wallet-rpc 0.18.5.1 | `http://127.0.0.1:28084/json_rpc` | none | `.sim/regtest/monero/wallets-2` |
| Zcash node | zebrad 6.3.0 (Zcash Foundation release) | `http://127.0.0.1:28232/` (P2P 127.0.0.1:28233) | HTTP basic, cookie `.sim/regtest/zcash/.cookie` | `.sim/regtest/zcash/state` |

Versions, URLs, hashes and signature results are in [PINS.md](PINS.md). Ports are in `pins.env`, away from the DERO
simulator (20000/8089), the app server (8082), the DERO wallets (30000–30010) and XSWD (44326).

All of it runs inside WSL2 (distro `Ubuntu-24.04`, or `DMZ_REGTEST_DISTRO`), and WSL's localhost forwarding makes
the ports reachable at 127.0.0.1 on Windows. Each daemon's host is a hidden `wsl.exe` process started through WMI,
so it outlives the shell that started it and keeps the WSL instance alive. There are three reasons for WSL:

- Zebra publishes Linux release binaries only.
- **Windows Defender quarantines monerod and monero-wallet-rpc anywhere on the Windows drive** — both the Windows
  build (`Trojan:Win32/Wacatac.A!ml`) and the Linux one (`Trojan:Linux/CoinMiner.T!MTB`) — although both hash to
  binaryFate's signed list. So the verified Monero tarball is extracted into the distro's own filesystem
  (`$HOME/.dmz-regtest/monero-v0.18.5.1`), and its binaries' hashes are re-checked on every start. Adding an antivirus
  exclusion is the operator's decision, and this harness does not make it.
- zfund, the Zcash key/transaction tool, is a Rust program built with cargo in WSL.

## Commands

Run these from Git Bash, from anywhere; paths are derived from the scripts' own location.

```sh
tools/regtest/fetch.sh          # download + verify (PGP / sigstore / sha256) whatever is missing in vendor/regtest/dl
tools/regtest/monero-up.sh      # monerod + two wallet-rpc; idempotent (reports what is already running)
tools/regtest/zcash-up.sh       # zebrad; idempotent
tools/regtest/smoke-monero.sh   # proves xmrswap's wallet-rpc calls, writes research/regtest/monero-smoke.log
tools/regtest/smoke-zcash.sh    # proves zecswap's node calls, writes research/regtest/zcash-smoke.log
tools/regtest/monero-down.sh [--wipe]   # stop (the chain and wallets are kept unless --wipe)
tools/regtest/zcash-down.sh  [--wipe]
```

Every up script fails closed: a missing binary, a sha256 other than the one pinned, or a port held by something else
stops it with a message. The chains persist across restarts (monerod `--keep-fakechain`; zebrad's state directory).

## Monero

**Mine** (the daemon's JSON-RPC; any mainnet-form address — regtest uses mainnet's prefixes):

```sh
curl -s http://127.0.0.1:28081/json_rpc -d '{"jsonrpc":"2.0","id":"0","method":"generateblocks","params":{"amount_of_blocks":100,"wallet_address":"4..."}}'
```

**Fund a wallet:** `create_wallet` on a wallet-rpc, `get_address`, mine 60+ blocks to that address (a coinbase is
spendable 60 blocks after its own), then mine another ~20. A transfer's change is itself locked for 10 blocks, so
several transfers in a row each need their own unlocked output. `transfer` from it, then mine 10 blocks for the
receiver to be able to spend. Regtest's block reward is ~35 XMR.

**Point xmrswap at it:** `-net regtest -monero http://127.0.0.1:28083` for one party and `…:28084` for the other,
with `-monero-name <wallet file>`. Both wallet-rpc **start with no wallet open**. xmrswap's startup calls `get_height`
and `get_address` before anything else, and both answer -13 "No wallet file" until a wallet is open, so
`create_wallet` / `open_wallet` (over the rpc) first. Two parties cannot share one wallet-rpc: the companion switches
the rpc's open wallet between its own and the joint ones.

### Monero facts, measured (research/regtest/monero-smoke.log)

| | |
|---|---|
| Coinbase unlock | a coinbase mined in block *h* first counts in `unlocked_balance` at chain height *h* + 60 |
| Incoming transfer unlock | `locked` turns false and `unlocked_balance` includes it at exactly 10 confirmations (confirmations = chain height − block height) |
| `get_transfers` `min_height` | exclusive: `min_height = h` omits a transfer in block *h*, `h − 1` includes it (xmrswap's `minHeight − 1` is right) |
| Transfer fee | ~0.0026 XMR (1 destination + change, default priority, ring 16); `get_fee_estimate` 1,200,000 piconero/byte |
| Sweep fee | ~0.00181 XMR (1 input, 1 output) |
| Refresh time | ~0.1–0.3 s for a new wallet over the whole ~700-block chain; generating 80 blocks ~3.7 s |
| Regtest hard fork | v16 from height 1 |

Regtest fees are regtest's: the fee per byte scales with the block reward and the median block weight, so they are
not mainnet's figures.

### Monero: real monero-wallet-rpc 0.18.5.1 vs tools/xmrswap's FakeWallet

`smoke-monero.sh` replays the same scenario on the fake (`tools/regtest/xmrshape`) and diffs every answer. The
transcript ends with the full list. The ones that matter:

1. **A view-only wallet's `transfer` and `sweep_all` are not refused.** They answer a `tx_hash` / `tx_hash_list` plus
   an `unsigned_txset` (for offline signing), and broadcast nothing (the hash is not in the daemon's pool). The fake
   answers -29 "watch-only". xmrswap's `Transfer`/`SweepAll` would return that hash as a sent transaction. If
   `openJoint` ever opened a view-only file under the spend wallet's name, a sweep would be "recorded" and never sent.
2. **`generate_from_keys` does not check the keys against the address.** A random view key, or the right view key
   with a random spend key, is accepted ("Watch-only wallet / Wallet has been generated successfully.") and the
   address is echoed back, so xmrswap's echo check passes. The fake refuses (-1 "… does not match main address"), and
   xmrswap's comments say the server does too. With a wrong view key the wallet then sees nothing. With a wrong spend
   key, `refresh` fails with -1 "Failed to generate key image". In 0.18.5.1 `on_generate_from_keys` calls
   `wallet2::generate` with no comparison, so the companion has to verify s·G and v·G itself.
3. **A non-zero `unlock_time` is refused:** -50 "Transaction cannot have non-zero unlock time". The fake accepts it.
   An honest 0.18.5.1 wallet cannot create review R6's frozen output; whether consensus still accepts one built by
   other software was not tested.
4. `sweep_all` with nothing unlocked answers **-4** "No unlocked balance in the specified account"; the fake answers -37.
5. `get_height` is the **wallet's scanned height** (1 for a new wallet until its first refresh), not the chain's; with
   no wallet open it is -13. The fake answers the chain height, wallet or not. xmrswap takes `RestoreHeight` from it
   right after opening its own wallet, which can only err low — slower scans, never a missed payment.
6. A new wallet's balance *before* `refresh` depends on timing: wallet-rpc's own auto-refresh runs every 20 s. It read
   0 in one run and the full balance in another. The fake's `scanLag` models only the 0.
7. Extra fields on the real side: `transfer` adds `amounts_by_dest`, `multisig_txset`, `spent_key_images`,
   `unsigned_txset`, `weight`, and `sweep_all` their `_list` forms. On the real side `get_transfers` **omits** `in` /
   `pool` when empty (the fake always sends arrays), and pool entries carry no `confirmations`. The entry shape of
   `in` is identical, including `locked`, `unlock_time` and `confirmations`.
8. **`set_daemon` holds only for the wallet open when it is sent.** After `close_wallet` + `open_wallet`, and on a
   wallet then made by `generate_from_keys`, the rpc is back on its own `--daemon-address`. This was measured by
   pointing it at a dead port: refresh failed (-38) on that wallet only. So a `SetDaemon` issued once does not cover
   the joint wallets xmrswap opens later. With no wallet open, `set_daemon` is -13 "No wallet file"; the fake answers
   `{}`. It accepts any address without checking; the next refresh shows the failure.
9. Same codes, different text: -2 ("WALLET_RPC_ERROR_CODE_WRONG_ADDRESS: <addr>" vs "Invalid destination
   address"), -46 ("destination amount is zero", lower case), -1 open_wallet (the real text includes the path; a wrong
   password is "Failed to open wallet : Invalid password.").
   The same code and text on both: -13 on every no-wallet call, -17 not enough money, -37 not enough unlocked money,
   -1 "Wallet already exists.". Neither `create_wallet` nor `get_version` is served by the fake.

## Zcash

**Mine:** `generate` (pays `[mining] miner_address`, a P2SH nobody here holds) or `generatetoaddress [n, "tm…"]`:

```sh
C=.sim/regtest/zcash/.cookie
curl -s -u "$(cat $C)" -d '{"jsonrpc":"1.0","id":"x","method":"generatetoaddress","params":[1,"tm..."]}' http://127.0.0.1:28232/
```

**Fund a key** (zebrad has no wallet). Use zfund, built on first use into `vendor/regtest/bin/zfund`:

```sh
wsl -d Ubuntu-24.04 --exec <repo-in-wsl>/vendor/regtest/bin/zfund keygen <repo-in-wsl>/.sim/regtest/zcash/keys/me.key
#   -> {"address":"tm...","pubkey":...,"pubkey_hash":...,"wif":...}   (key file: 64 hex; the WIF is what zecswap -key reads too)
# generatetoaddress 1 to it, then 100 more blocks (coinbase maturity), then spend the coinbase:
wsl ... zfund send --key .../me.key --height <tip+1> --in <coinbase txid>:0:<zat> --to <t-address>:<zat> [--to ...] [--version 5|6]
#   -> {"txid":...,"hex":...,"version":5,"branch_id":"37a5165b","fee":...}; then sendrawtransaction [hex]
```

Change goes back to the key's own address; the fee is ZIP 317's. From Git Bash, set `MSYS_NO_PATHCONV=1` before
calling `wsl.exe` with Linux paths, or Git Bash rewrites them.

**Point zecswap at it:** `-network regtest -zcash http://127.0.0.1:28232 -zcash-cookie <repo>/.sim/regtest/zcash/.cookie`.
Today zecswap refuses this node (difference 1 below).

**The chain's rules.** Every upgrade through NU6.3 is active: Overwinter–Canopy at height 1, NU5 2, NU6 3, NU6.1 4,
NU6.2 5, NU6.3 6. `consensus.chaintip` and `consensus.nextblock` both report **`37a5165b`** (NU6.3, ZIP 258). Canopy
has to be at height 1: zebrad 6.3.0 mines only through `getblocktemplate`, which panics on any block before Heartwood
("history root is required for block templates") and refuses anything before Canopy ("Zebra does not support
generating pre-Canopy block templates"). Both were measured. Transparent coinbase may be spent straight to transparent
outputs (`should_allow_unshielded_coinbase_spends`, Zebra's regtest default). On mainnet and testnet, ZIP 213's rule
still requires coinbase to be shielded, so a test funding like this one is regtest-only.

### Zcash facts, measured (research/regtest/zcash-smoke.log)

| | |
|---|---|
| Coinbase maturity | a coinbase from block *h* is refused while the next block is *h* + 99 ("spends are invalid before Height(h+100)") and accepted when it is *h* + 100 |
| Regtest coinbase | 625,000,000 zatoshi to the miner address, no funding-stream outputs |
| Funding tx | v5, branch `37a5165b`, 1 input 3 outputs, 272–273 bytes, ZIP 317 fee 15,000 zatoshi |
| Confirmations | `getrawtransaction` confirmations = tip − height + 1 (1 when mined, 10 after 9 more blocks) |
| Block time | `generate` stamps blocks with the genesis time + 1 s per block (2011-02-02) |
| Mining | ~0.2–0.4 s per block through `generate` |

### Zcash: real zebrad 6.3.0 vs what tools/zecswap assumes

`smoke-zcash.sh` exercises the seven core methods zecswap calls (`getblockchaininfo`, `getblockhash`, `getaddressutxos`,
`getaddresstxids`, `getrawtransaction` verbose, `sendrawtransaction`, `gettxout`) with zecswap's parameters. It then
runs zecswap's own client and ZIP 244 code against the node (`tools/regtest/zecshape`).

1. **`getblockchaininfo` says `"chain": "test"` on regtest**, not `"regtest"`. zecswap's `ChainInfo` therefore reads
   the node as testnet, and `Sane` refuses it: "the node is at testnet height …, below NU5 (1842420)". zecswap
   `-network regtest` cannot run against Zebra as written. Regtest can be told apart by its genesis hash.
   `CheckBranchID` also refuses, because its schedule is testnet's heights, not the node's `upgrades` table.
2. **v6 transactions are live.** Zebra 6.3.0 mines its coinbase as **v6** (ZIP 229), accepts transparent v6
   transactions, and librustzcash 0.30.1's builder produces **v6 by default** under NU6.3. zecswap reads versions 1–5
   only (`ParseV5`, `transparentParts`: "version 80000006 is not v5"). So a maker's claim built by a current
   librustzcash wallet would be passed over by `FindSpend`, and the taker would never see the secret. `RawTx` of a v6
   transaction comes back `Verified: false`.
3. `estimatedheight` is the tip plus the time since the tip at 75 s a block. With regtest's 2011 timestamps that is
   ~6.5 million, and `Sane` would refuse the node as "behind". The harness sets Zebra's test-only
   `debug_force_finished_sync` so it equals the tip. On a real network it is meaningful.
   `initial_block_download_complete` is not sent at all (zecswap treats it as optional).
4. Duplicate broadcasts are **-1**, not zcashd's txid / -27: "transaction already exists in mempool", and "…
   transaction was committed to the best chain" once mined. zecswap's `Broadcast` falls back to `RawTx` and was
   verified idempotent against it. Other refusals: immature coinbase, input already spent, and mempool double spend
   are -25; an unknown txid is -5 (zecswap's `ErrTxNotFound`, verified); malformed hex is -22. Zebra answers every
   error with HTTP 200.
5. `getaddresstxids` clamps a range above the tip to the tip, so `[tip+1, tip+5]` lists the tip block's transactions.
   It answers InvalidParams for start > end, and start = end = 0 means the whole chain. Bounds are inclusive.
6. `gettxout`: an unspent output gives `bestblock`, `confirmations`, `scriptPubKey.hex` (what zecswap reads) plus
   `value` in **ZEC, as a float**. A spent or absent output gives `result: null`, error null (zecswap's `TxOut` → nil,
   verified). An output spent only in the mempool is still returned with `include_mempool=false`, and null with `true`.
7. As zecswap assumes: a mempool transaction's verbose `getrawtransaction` carries no `height`, `confirmations` or
   `blockhash`. `getaddressutxos` gives `address, txid, outputIndex, script, satoshis, height` for P2PKH and P2SH
   alike, spent outputs drop out, and `script` is the `a914…87` of a P2SH.
8. **ZIP 244 agrees.** For both smoke transactions — a coinbase spend and an ordinary P2PKH spend, built and signed by
   librustzcash — zecswap's `ParseV5` + `TxID` gives the node's txid, and its `SigDigest` verifies the librustzcash
   signature (branch `37a5165b`).

### Why zfund is Rust (librustzcash) and not Go

A Go funding tool in this repository would have reused, or re-derived, zecswap's own serializer and ZIP 244 code, so
a bug shared by both would have gone unseen. zfund uses the Zcash developers' crates (`zcash_primitives` builder,
`zcash_transparent`, pinned in `Cargo.lock`). zebrad accepting its transactions is the first check. zecswap
verifying the same signatures with its own digest code (item 8) is the second, and it came for free. The costs: a
cargo build in WSL (~30 s once the crates are cached), and the builder's Sapling prover type parameters, filled with
the crate's mock provers because a transparent-only transaction never proves anything.

## Tracked files

| | |
|---|---|
| `fetch.sh`, `monero-up.sh`, `monero-down.sh`, `zcash-up.sh`, `zcash-down.sh`, `smoke-*.sh`, `common.sh`, `pins.env` | the harness |
| `xmrsmoke/`, `xmrshape/` | Monero smoke (raw JSON-RPC) and the fake-vs-real diff (imports tools/xmrswap) |
| `zecsmoke/`, `zecshape/` | Zcash smoke and zecswap's own client/ZIP 244 against the node (imports tools/zecswap) |
| `zfund/` | Rust key/transaction tool (Cargo.toml, Cargo.lock, src) |
| `../../research/regtest/*.log` | the evidence, from real runs; private keys redacted |

Untracked: binaries and downloads in `vendor/regtest/`, chain data, wallet files, throwaway keys and cookies in
`.sim/regtest/`. The xmrshape and zecshape diffs compile against tools/xmrswap and tools/zecswap as they stand. While
another agent is mid-edit there, the smoke still runs and records that the diff did not.

## Public test networks (never mainnet)

Measured 2026-09-11: C: has 318.9 GB free (931 GB disk); WSL's own ext4 disk has 950 GB free.
Evidence: `research/regtest/public-testnets.log`.

### Monero stagenet — pruned, syncing

```sh
tools/regtest/stagenet-up.sh           # start; when running, prints height / target, size on disk, rate, estimate
tools/regtest/stagenet-down.sh [--wipe]
```

- **Gate.** A pruned stagenet had to need under 30 GB with at least 100 GB free. Six public stagenet nodes (four of
  them the seeds in monero `src/p2p/net_node.inl`) all reported height 2,205,297 and `database_size` 16,106,127,360
  bytes, i.e. 15 GiB. Restricted RPC rounds that figure **up** to a multiple of 5 GiB (`core_rpc_server.cpp`
  on_get_info). So a stagenet database is at most 15 GiB even unpruned, and pruning keeps about a third of the prunable data.
- **The node.** monerod 0.18.5.1 (the same pinned binary) with `--stagenet --prune-blockchain --sync-pruned-blocks`.
  P2P is outbound only: bound to 127.0.0.1, `--in-peers 0`, `--hide-my-port`, `--no-igd`. There is no update check and
  there are no DNS checkpoints, so stagenet peers are the only outside contact. RPC is on `http://127.0.0.1:38081`
  (loopback, unrestricted), with no wallet-rpc. The chain lives in `.sim/stagenet/chain`. The script stops and refuses
  any daemon whose `get_info` does not say `nettype: stagenet`.
- **Progress.** The sync started 08:40:58Z, with 5–9 outbound peers and never an inbound one.
  - Through the first ~50,000 blocks it ran at ~170 blocks/s: 51,438 by 08:47Z.
  - From ~120,000 it slowed to 10–20 blocks/s (130,231 of 2,205,310 by 09:05Z, 1.25 GB on disk).
  - The slow part is CPU, not disk. monerod was at ~9 of 16 cores, verifying these fuller blocks' signatures, and
    reading only ~0.7 MiB/s. Moving the chain off the /mnt/c mount would not help.
  - At 10–20 blocks/s the remaining ~2.08 M blocks take **1–2 days**. It will be faster wherever stagenet blocks are
    near-empty again.
  - Run `stagenet-up.sh` again for a fresh reading. It computes the rate over the last five readings, so the estimate
    follows the current speed, and appends to `.sim/stagenet/progress.tsv`.

### The stagenet swap run (MT-B19) — prepared, waiting for the sync

The three exits (happy, refund, punish) of the DERO ↔ Monero swap, with the Monero side on stagenet and the DERO side
on the simulator (wallets 30006 bob and 30007 alice; check nothing else is using them first). Nothing here runs
against a node that is still syncing: the wallet script creates no wallet, monerod's `start_mining` answers `BUSY`
(`core_rpc_server.cpp` `on_start_mining` → `CHECK_CORE_READY`, which is `is_synchronized()`, v0.18.5.1), and
`simhtlc -stagenet` refuses before it touches the DERO side.

```sh
tools/regtest/stagenet-wallets-up.sh            # two monero-wallet-rpc: alice 127.0.0.1:38088, bob 127.0.0.1:38087
tools/regtest/stagenet-wallets-up.sh --create   # once synced: create xmrswap-stagenet-alice and -bob at the tip, print both addresses
tools/regtest/stagenet-wallets-down.sh          # stop both; the wallet files stay in WSL at $HOME/.dmz-regtest/stagenet-wallets
```

- **The wallets.** The pinned monero-wallet-rpc 0.18.5.1, checked against `MONERO_WALLET_RPC_SHA256` before it starts;
  `--stagenet`, so it refuses a daemon on any other network; `--daemon-address 127.0.0.1:38081 --trusted-daemon` (our
  own node); RPC bound to 127.0.0.1 with no login, as in the regtest harness (the companion warns about it off mainnet).
  The script starts nothing unless a daemon answers `nettype: stagenet`, and creates the wallets only once it answers
  `synchronized: true`, so they start at the tip and never scan the chain from an old height. Ports:
  `STAGENET_WALLET_RPC_PORT` and `STAGENET_WALLET_RPC_PORT2`. A port already taken is used only if what holds it is
  shown to be exactly this: its command line inside WSL (`pgrep -af`) names the pinned binary with `--stagenet`,
  `--rpc-bind-ip 127.0.0.1` and that port, and its running image (`/proc/<pid>/exe`) hashes to the pin; anything else
  is refused. `stagenet-wallets-down.sh` stops only such a process, found by the pid file the launcher wrote (or, when
  that is gone, by its command line), and leaves anything else on those ports alone.
- **What alice needs.** Each exit locks 0.05 XMR (`simhtlc -stagenet`'s default `-amount-xmr`, 50,000,000,000
  piconero), and the driver sets aside 0.001 XMR a exit for the fee: 0.153 XMR she can spend, in at least three
  outputs of 0.051 XMR or more. With fewer, a later exit waits up to 10 blocks (20 min) for an earlier one's change
  to unlock; the driver says so before it starts, and refuses outright when the spendable total is short.

**Where the stagenet XMR comes from** — the operator's choice; neither has been used.

1. **Mining on our own node** (no outside service). Once synced:
   ```sh
   curl -s http://127.0.0.1:38081/start_mining -d '{"miner_address":"<alice address>","threads_count":8,"do_background_mining":false,"ignore_battery":true}'
   curl -s http://127.0.0.1:38081/mining_status   # speed (H/s) and the difficulty being mined at
   curl -s http://127.0.0.1:38081/stop_mining -d '{}'
   ```
   - *Reward.* At least 0.6 XMR a block: the tail subsidy, `FINAL_SUBSIDY_PER_MINUTE` 3·10¹¹ piconero × 2 minutes
     (`cryptonote_config.h:56`; `cryptonote_basic_impl.cpp:88-91`). One block pays for the whole run. A coinbase is
     spendable only 60 blocks later (`CRYPTONOTE_MINED_MONEY_UNLOCK_WINDOW`, `cryptonote_config.h:44`): about 2 h.
   - *Difficulty.* The node read 104,846 at height 635,167 (2026-09-12 04:01Z, still syncing). The tip's difficulty
     is known only once synced (`get_info` `difficulty`); it is what stagenet's hash rate makes it, about network
     H/s × 120 (`DIFFICULTY_TARGET_V2`, window 720 blocks).
   - *This CPU.* An AMD Ryzen 7 5700X, 8 cores / 16 threads, 31.9 GB. It is not in tevador's RandomX table (fast
     mode, 2,080 MiB): a Ryzen 7 1700, also 8 cores, does 4,100 H/s, an i9-9900K 5,770. Its own speed is what
     `mining_status` reports. Expected time to one block is difficulty ÷ H/s: 26 s at the difficulty above and 4,100
     H/s; 4 min if the tip is ten times harder; 43 min at a hundred times. Whether a CPU is practical is decided by
     those two numbers read after the sync.
   - *Time.* Time to a block, then 60 blocks (≈ 2 h) to unlock, then the split below (≈ 20 min).
2. **A public faucet** (an outside service, paying to alice's address). The two in the Monero docs
   (docs.getmonero.org/infrastructure/networks): XMR-TW, https://stagenet-faucet.xmr-tw.org/ — its page, fetched
   2026-09-12, states no amount, limit or captcha; CypherFaucet, https://cypherfaucet.com/xmr-stagenet — unreachable
   when tried on 2026-09-12 (a timeout, then HTTP 522). *Time:* a payout is an ordinary transfer, spendable after 10
   blocks (≈ 20 min), plus the faucet's own queue, which neither states.

**Split alice's funds** into separate outputs, then wait 10 blocks (≈ 20 min):
```sh
curl -s http://127.0.0.1:38088/json_rpc -d '{"jsonrpc":"2.0","id":"0","method":"open_wallet","params":{"filename":"xmrswap-stagenet-alice","password":""}}'
curl -s http://127.0.0.1:38088/json_rpc -d '{"jsonrpc":"2.0","id":"0","method":"transfer","params":{"destinations":[
  {"amount":60000000000,"address":"<alice address>"},{"amount":60000000000,"address":"<alice address>"},
  {"amount":60000000000,"address":"<alice address>"},{"amount":60000000000,"address":"<alice address>"}]}}'
```

**The run** (one process; it runs the exits one after the other, and logs every DERO and Monero txid with where the
simulator and the stagenet node have it):
```sh
cd tools && go run ./simhtlc -stagenet -htlc <the simulator's recorded htlc.bas> -prover "<proof-xmr command>" 2>&1 | tee <tmp>/stagenet-run.log
```
`-stagenet` defaults to the mainnet profile's numbers: 20 real confirmations, t1 420 and t2 400 DERO blocks, G 100,
stable depth and claim margin 8; 0.05 XMR an exit, a 5 s round, 6 h per exit. The simulator's recorded `htlc.bas` v3
was installed with the profile's w1min 400, w2min 400 and g2 4,800, so t1 420 clears its floor of max(400, 400 + 11).
`-paths reorg-before,reorg-after,unlock` are regtest-only and refused.

| exit | what bounds it | wall clock |
|---|---|---|
| happy | the DERO lock and the stipend, each stable (about 20 DERO blocks, 6 min); alice's lock mined plus 20 confirmations, 21 stagenet blocks, mean 42 min, σ ≈ 9 min under the exponential model the profile's t1 rule uses; the gate, the Claim stable (3 min), bob's sweep | about 50–60 min. The gate must go out before t1 − G, lock + 320 DERO blocks (about 96 min) |
| refund | bob away until t1, lock + 420 DERO blocks (2 h 6 min at 18 s); his Refund; alice's recovery sweep of an output long unlocked | about 2 h 15 min |
| punish | t2 = t1 + 400 (4 h 6 min from the lock); alice's Punish at once from t2, watched to stable depth; her 0.05 XMR stays at the joint address | about 4 h 15 min |

About 7.5 h for the three in sequence; they share wallets 30006 and 30007, so they cannot overlap. The simulator mines
a block at once when a transaction waits, so the DERO-bounded times are upper estimates.

**Done, for MT-B19:**
- all three exits end as they should: happy with alice claimed and bob swept; refund with bob refunded and alice's
  recovery swept; punish with alice punished and the lock at st 3;
- the contract holds 0 after each exit;
- every Monero txid (alice's locks, bob's sweep, alice's recovery) is found on the stagenet node and every DERO txid
  on the simulator, as the run prints;
- `set_daemon` accepted and never refused;
- the run is written to `research/regtest/xmrswap-stagenet.log` with `<repo>` and `<tmp>` for machine paths, and
  `go test ./hygiene` passes;
- nothing is left behind: no simhtlc or xmrswap process, no `xmrswap-*` scheduled task, and
  `stagenet-wallets-down.sh` run.

### Zcash testnet (zebrad 6.3.0) — syncing

```sh
tools/regtest/zcash-testnet-up.sh                 # start; when running: testnet check, height / target, size, peers, ETA
tools/regtest/zcash-testnet-up.sh --restart-gate  # replace the running rpcgate with the current build; zebrad keeps syncing
tools/regtest/zcash-testnet-down.sh [--wipe]
```

- **Node.** The same pinned zebrad, with `network = "Testnet"` and `initial_mainnet_peers = []`. P2P listens on
  127.0.0.1:18233 only, so no peer can connect in (Zebra has no UPnP). The state, the peer cache and zebrad's own
  cookie live on WSL's ext4 filesystem (`$HOME/.dmz-regtest/zcash-testnet`), because a ~10 GB RocksDB is much faster
  there than over the /mnt/c mount. Config, logs, progress and the client cookie are in `.sim/zcash-testnet`.
- **No update check or telemetry to switch off.** Zebra 6.3.0 has none. Its end-of-support task compares heights on
  mainnet only, and its metrics, tracing, OpenTelemetry and health endpoints are all off unless configured.
- **RPC: only zecswap's methods.** zebrad cannot restrict its RPC by method; its config has `listen_addr`,
  `cookie_dir`, `enable_cookie_auth` and nothing per method. So zebrad's own RPC stays internal on 127.0.0.1:18242,
  and clients use **`tools/regtest/rpcgate`** on **`http://127.0.0.1:18232`**, with the cookie in
  `.sim/zcash-testnet/.cookie`. The gate:
  - serves only `getblockchaininfo getblockhash getaddressutxos getaddresstxids getrawtransaction sendrawtransaction gettxout getblock getblockheader`, the methods zecswap calls (the last two only with `-verify-headers`);
  - refuses everything else with -32601 without forwarding it (measured: `getpeerinfo`, `stop`, `getinfo`, `submitblock`);
  - refuses batches with -32600 and a request without its cookie with HTTP 401;
  - forwards a request rebuilt from the fields it checked, never the client's bytes. A body naming `"method"` twice
    is checked on the last value, and zebrad receives that value and no other. Measured on the live node on
    2026-09-11: `{"method":"stop","method":"getblockhash","params":[0]}` came back as `getblockhash 0`, and zebrad kept
    running. `rpcgate/main_test.go` holds the rebuild, the smuggling attempt and every refusal.

  The gate runs under a `wsl.exe` host of its own, not zebrad's, so `--restart-gate` swaps in a new build without
  stopping the sync. The up script records the sha256 of the build it started and, on every status check, warns when
  that is not the build in `vendor/regtest/bin`. The first gate of this sync ran for half an hour on the build before
  the hardening, with nothing reporting it.
  zecswap: `-network testnet -zcash http://127.0.0.1:18232 -zcash-cookie <repo>/.sim/zcash-testnet/.cookie`.
- **Testnet only.** Every start and every status check requires `getblockhash 0` to be testnet's genesis block
  (`05a60a92…2c38`, v6.3.0 `test-checkpoints.txt`) and `chain` to be `test`. Otherwise it stops zebrad and the gate
  and fails. The genesis check is needed because Zebra says `test` on regtest too.
- **Progress.** The sync started 08:54Z with 19 peers, verifying 400-block checkpoint ranges every 2–3 s.
  - It reached 54,000 blocks (0.22 GB) by 09:05Z, at 79 blocks/s over the last 7 minutes.
  - The rate followed the peers, not this machine: ~25 blocks/s while they fell from 41 to 17, back up with 40.
    zebrad itself used ~14% of one core.
  - zebrad's `estimatedheight` (~3.52 M) is below the last testnet checkpoint it ships (4,238,000), so the progress
    target uses that checkpoint as a floor.
  - At 25–110 blocks/s the ~4.18 M blocks left take **10 h to 2 days**. Zebra's own full-testnet test allows 72 h.
- **State size.** About 10 GB ("10 GB for cached Testnet data"), against about 300 GB for mainnet (Zebra book,
  *System Requirements*).
- **Chain.** The testnet tip is past 4,238,000 (the last testnet checkpoint shipped in v6.3.0,
  `zebra-chain/src/parameters/checkpoint/test-checkpoints.txt`). Mainnet's last checkpoint is 3,436,171.
- **Sync time.** No primary source gives a measured testnet figure. The Zebra book says only "A first sync from
  genesis takes days on Mainnet" (*Running Zebra*). Zebra's own `sync_full_testnet` test (`zebrad/tests/e2e/sync.rs`)
  allows at most 72 h (`FINISH_FULL_SYNC_TIMEOUT`, `zebrad/tests/common/sync.rs`). The same file notes that testnet
  variants of other sync tests were removed "because testnet peers and DNS seeders were unstable" (#1222, #1791).
- **Estimate.** With about 1/30 of mainnet's data to download and verify, a few hours to a day on this machine is
  plausible, 72 h is the ceiling Zebra allows itself, and unstable testnet peers are the main risk. This is an
  estimate, not a measurement.

### DERO testnet — seed not reachable

derohe's only testnet seed (`config/seed_nodes.go`, `Testnet_seed_nodes = {"212.8.242.60:40401"}`, derohe
e9df120) did not accept a TCP connection. Tests at 08:36Z and 08:37Z: 10 s and 20 s timeouts, no SYN-ACK and no
reset; `Test-NetConnection` failed the same way; ICMP also failed. Outbound TCP from this machine otherwise works
(github.com:443 connected). derod was not started.

## Known differences from mainnet

- Monero: offline single node; hard fork v16 from height 1; difficulty fixed at 1; ~35 XMR block reward, so regtest
  fees; mainnet-form addresses; the wallet needs `--allow-mismatched-daemon-version`, which a person's wallet never does.
- Zcash: `chain` reads `test`; upgrade heights 1–6; no funding streams; transparent coinbase spendable without
  shielding; 2011 block timestamps and a forced `estimatedheight`; blocks are mined on demand, with no proof of work.
- Both: one node per chain, fully trusted, with no reorganisations unless a test makes them (`invalidateblock` exists
  on Zebra). Monero's FCMP++/Carrot upgrade is not on mainnet (v0.19 alphas are stressnet-only). When it lands, the
  wallet and transaction layer under xmrswap changes, and these pins and this smoke must be redone.
