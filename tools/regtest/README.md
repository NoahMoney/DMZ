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

`smoke-zcash.sh` exercises all seven methods zecswap calls (`getblockchaininfo`, `getblockhash`, `getaddressutxos`,
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

## Known differences from mainnet

- Monero: offline single node; hard fork v16 from height 1; difficulty fixed at 1; ~35 XMR block reward, so regtest
  fees; mainnet-form addresses; the wallet needs `--allow-mismatched-daemon-version`, which a person's wallet never does.
- Zcash: `chain` reads `test`; upgrade heights 1–6; no funding streams; transparent coinbase spendable without
  shielding; 2011 block timestamps and a forced `estimatedheight`; blocks are mined on demand, with no proof of work.
- Both: one node per chain, fully trusted, with no reorganisations unless a test makes them (`invalidateblock` exists
  on Zebra). Monero's FCMP++/Carrot upgrade is not on mainnet (v0.19 alphas are stressnet-only). When it lands, the
  wallet and transaction layer under xmrswap changes, and these pins and this smoke must be redone.
