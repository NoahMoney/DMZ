# DECISIONS.md — DMZ (fomo-style social DEX on DERO / TELA; formerly Veil, a name that now survives only in code identifiers)

Every non-obvious choice, dated, with the primary source that justified it.
Source references use the clones under `vendor/` (git-ignored; re-create with `deploy/README.md (Simulator)`):

| Clone | Upstream | Commit (date) |
|---|---|---|
| `vendor/derohe` | https://github.com/deroproject/derohe (official, consensus rules) | `e9df1205` (2024-02-29) — version `3.5.3-140.DEROHE.STARGATE+13062023` |
| `vendor/derohe-civilware` | https://github.com/civilware/derohe (fork used by TELA + Engram; adds XSWD) | `433c4559` (2025-08-13) — version `3.5.5-142.DEROHE.STARGATE+13082025` |
| `vendor/tela` | https://github.com/civilware/tela | `aa892d2f` (2025-08-06) |
| `vendor/documentation` | https://github.com/deroproject/documentation | `5f689fee` (2024-03-31) |
| `vendor/engram` | https://github.com/DEROFDN/Engram | `12b6e61a` (2025-08-28), release 0.6.1 |

`vendor/derohe/dvm` and `vendor/derohe-civilware/dvm` are **byte-identical** (`diff -rq`, 2026-09-05), and so is
`blockchain/transaction_execute.go`. Contract semantics below therefore hold on both the official node and the fork.

---

## 0. Environment (2026-09-05)

- Windows 10, Go 1.26.2, Node 22.10, git 2.52. **No Python, no gcc.** Consequence: the formula reference model and
  fuzz harness are written in Go (`reference/`), not Python. The prompt allowed "Go or Python".
- `vendor/bin/simulator.exe` built from official derohe with `go build -mod=vendor ./cmd/simulator` (exit 0).
- Cloning derohe on Windows needs `core.longpaths=true`; one vendored file with a non-ASCII name
  (`vendor/github.com/tjfoc/gmsm/API????.md`) cannot be checked out. It is documentation only; builds are unaffected.

---

## 1. DVM-BASIC — confirmed from `vendor/derohe/dvm`

### 1.1 Types, operators, parser
- Two types only: `Uint64`, `String` (`dvm.go:42`, `check_valid_type` `dvm.go:160-168`).
- Source is tokenised by Go's `text/scanner` with `SkipComments` (`dvm.go:90-93`): `//`, `/* */` comments and lines
  starting with `;`/`REM` are dropped; **string literals are scanner tokens, so spaces inside quotes survive**.
  A statement must fit on one physical line (`dvm.go:117-131`).
- Each line is re-joined with spaces and parsed by **Go's expression parser** (`parser.ParseExpr`, `dvm.go:~565`),
  so expression syntax is Go's: `&&`, `||`, `==`, `!=`, `<<`, `>>`, `&`, `|`, `^`, parentheses, decimal literals.
- Statements: `DIM`, `LET`, `GOTO`, `IF … THEN GOTO n` and `IF … THEN GOTO n ELSE GOTO m` (`dvm.go:738-762`),
  `RETURN`, bare function-call lines, `PRINT`/`PRINTF` (`dvm.go:541-556`).
- Line numbers: mandatory, strictly ascending inside a function, never 0 or 2^64−1, no duplicates (`dvm.go:253-273`).
- Function names must start with a letter; **an entrypoint's first character must be an upper-case ASCII letter**
  (`RunSmartContract`, `dvm.go:354-372`); lower-case functions are internal only. Nested functions are rejected.
- Params: only `Uint64`/`String` (`dvm.go:212-236`). A `Uint64` param literally named `value` is auto-filled with
  the DERO deposited (`sc.go:218-221`).
- **Arithmetic is Go `uint64` arithmetic: `+ - * << ` wrap silently, `/` and `%` by zero panic** (`dvm.go:1060-1075`).
  A panic aborts the call and discards state (see §2). No overflow detection exists → every line must be bounded by us.
- `string + uint64` appends the decimal rendering (`dvm.go:1002-1004`). Strings support only `+`, `==`, `!=`
  (`dvm.go:1029-1050`); a concatenation ≥ 1 MiB panics (`dvm.go:1034`).
- Mixed-type comparisons panic (`dvm.go:1006-1008`).
- **`LET` quirk (found by test, 2026-09-05):** the scanner splits `>> << >= <= == != && ||` into single characters and
  only `IF` (`dvm.go:774`), `RETURN` (`:816`) and bare call lines (`:565`) rejoin them with `replacer`; `LET`
  (`:684`) does not, so in a `LET` only `+ - * / % & | ^` and parentheses work. Shifts are written as
  multiply/divide by a power of two (identical under wrapping) or through the `shl`/`shr` helpers, which use `RETURN`.

### 1.2 Built-in functions (complete table, `dvm_functions.go:58-94`, compute cost per call)

| Builtin | Cost | Notes (implementation lines in `dvm_functions.go`) |
|---|---|---|
| `VERSION(s)` | 1000 | sets semver used to select builtin variants |
| `LOAD(k)` | 5000 | **panics if key missing** (`Load` → `Unhandled data_type`, :121-134). Also charges storage-gas `len/10` (`dvm_store.go:88-127`) |
| `EXISTS(k)` | 5000 | 1/0 |
| `STORE(k,v)` | 10000 | charges storage gas = serialized value bytes (`dvm_store.go:128-135`) |
| `DELETE(k)` | 3000 | |
| `MAPEXISTS/MAPGET/MAPSTORE/MAPDELETE` | 1000 | per-TX RAM map, shared across internal calls |
| `RANDOM()` / `RANDOM(limit)` | 2500 | PRNG seeded from SCID,BLID,TXID |
| `SCID()` `BLID()` `TXID()` | 2000 | **raw 32-byte strings** (`:271-282`) |
| `DERO()` | 10000 | raw 32 zero bytes |
| `BLOCK_HEIGHT()` | 2000 | `BLOCK_TIMESTAMP()` 2500 (epoch seconds). `BLOCK_TOPOHEIGHT` is **commented out** (`:294-301`) |
| `SIGNER()` | 5000 | raw 33-byte compressed key; **33 zero bytes when ringsize ≠ 2** (see §2.4) |
| `UPDATE_SC_CODE(code)` | 5000 | overwrites key `"C"`; no authenticity check (`:312-322`) |
| `IS_ADDRESS_VALID(s)` | 50000 | 1 if 33-byte compressed point decodes (`:324-340`) |
| `ADDRESS_RAW(s)` | 60000 | textual → raw 33 bytes, `""` on failure |
| `ADDRESS_STRING(raw)` | 50000 | raw → `dero1…`/`deto1…` |
| `SEND_DERO_TO_ADDRESS(raw_addr, amt)` | 70000 | amount 0 is a no-op (`:377-397`) |
| `SEND_ASSET_TO_ADDRESS(raw_addr, amt, raw_scid)` | 90000 | (`:398-432`) |
| `DEROVALUE()` | 10000 | burn on the zero-SCID payload (`:434-438`) |
| `ASSETVALUE(raw_scid)` | 10000 | **argument must be exactly 32 raw bytes** (`:440-456`) — pass an `H` arg or `HEXDECODE(hex)` |
| `ATOI` `ITOA` | 5000 | |
| `SHA256` `SHA3256` `KECCAK256` | 25000 | return raw bytes |
| `HEX(s)` `HEXDECODE(s)` | 10000 | |
| `MIN` `MAX` | 5000 | |
| `STRLEN` `SUBSTR(s,off,len)` | 20000 | |
| `PANIC()` | 10000 | |

`TOKENVALUE` and `ADD_VALUE` from the old docs **do not exist** in the current table (docs repo `support_functions.md` is stale).
There is **no cross-contract call builtin** of any kind (the whole table is above; `Shared_State.SCIDSELF` comment
`dvm.go:~405` says "if we enable cross SC calls"). Consequences in §10.

### 1.2a Reserved storage keys (found by test, 2026-09-05)
The contract's code lives in its own data tree under the string key `"C"` (`dvm.SC_Code_Key`, `sc.go:79-81`;
`UPDATE_SC_CODE` simply stores to it, `dvm_functions.go:317`). **A contract that executes `STORE("C", …)` replaces
its own code** — `launch.bas` did exactly that with its curve-supply variable and bricked itself; the key is now `cs`
and `tests/parse_test.go` forbids the pattern. Raw 32-byte keys are the contract's asset balances and raw 33-byte keys
are account balances of its own asset (`simulator.go:181-243`, `rpc_dero_getsc.go:96`); all DMZ keys are short ASCII
strings or longer than 33 bytes, so they cannot collide.

### 1.3 Limits per call
- Compute gas: fixed `10_000_000` per invocation (`sc.go:237-240`); every interpreted line costs 5000 (`dvm.go:522`),
  every binary expression 800 (`dvm.go:994`).
- `LIMIT_interpreted_lines = 2000`, `LIMIT_evals = 11000` (`dvm.go:71-72`).
- Storage gas: limited by the TX fee, capped at `MAX_STORAGE_GAS_ATOMIC_UNITS = 20000` (`config.go:44`,
  `sc.go:243-250`); the serialised `sc_rpc` bytes are charged first (`sc.go:252-258`). ⇒ **a call may persist at most
  ~20 kB of new values**, and the fee paid is what `DERO.GetGasEstimate.gasstorage` reports.
- Max TX size `300 KiB` (`config.go:54`) is not the binding limit for code: the install TX's `sc_rpc` (which
  carries `SC_CODE`) is charged against the same 20000-unit storage budget (`sc.go:252-258`), so **code bytes +
  Initialize stores must stay below 20000** (this is where TELA's 19.2 KB DOC limit comes from). Measured
  2026-09-05, when `pool.bas` was 11384 bytes: storage gas 11641 at install, i.e. bytes + 257. Every contract has grown
  since (`pool.bas` is 13262 bytes today); the current byte counts and install gas per contract live in `contracts/README.md`.

---

## 2. Execution semantics — `vendor/derohe/blockchain/transaction_execute.go`, `dvm/sc.go`, `dvm/simulator.go`

1. **Install**: `SC_ACTION=1` + `SC_CODE`. SCID = install TXID. If the code defines `InitializePrivate` the contract is
   *private* and that is the entrypoint, else `Initialize` (`transaction_execute.go:301-347`).
2. **Call**: `SC_ACTION=0`, `SC_ID` (type `H`), `entrypoint` (type `S`), plus one arg per declared param
   (`U` for Uint64; `S` or `H` for String — `H` arrives as the raw 32 bytes, `sc.go:222-234`).
3. **Commit rule**: state and outgoing transfers persist only when the entrypoint returns `Uint64 0`
   (`sc.go:278-289`). Any other return, any panic, or a failed `SanityCheckExternalTransfers` → error →
   **all incoming burned value is refunded to the signer** (`ErrorRevert`, `simulator.go:244-279`,
   invoked at `transaction_execute.go:395-405`) and nothing is written. If the signer is unknown (ringsize ≠ 2)
   the refund cannot be addressed; treat funds as lost. Fees are always spent.
4. **Signer / ringsize**: `Extract_signer` succeeds only when the DERO payload has `RingSize == 2`
   (`transaction_execute.go:424-442`); otherwise `signer` is 33 zero bytes and `SIGNER()` returns that string
   (`sc.go:191`, `dvm_functions.go:307-310`). TELA's `address()` helper relies on `IS_ADDRESS_VALID(SIGNER())` being 0 then.
   The wallet **always adds a zero-SCID (DERO) payload** when a TX has only asset payloads
   (`walletapi/wallet_transfer.go:132-149`), so ringsize-2 asset-only calls still expose the signer.
   Ringsize must be a power of two in `[2,128]` (`wallet_transfer.go:97-104`, `config.go:56-57`).
5. **Assets in**: each payload's `burn` on `scid` becomes `incoming_value[scid]` (`transaction_execute.go:288-291`)
   and is credited to the contract's per-asset balance **before** the code runs (`sc.go:207-213`). Read with
   `DEROVALUE()` / `ASSETVALUE(scid)`. Wallet RPC: `transfer` params `transfers:[{scid, burn}]` (+ `sc_rpc`, `scid`).
6. **Assets out**: `SEND_*_TO_ADDRESS` queue external transfers; after the call the node checks the contract
   actually holds the totals (`SanityCheckExternalTransfers`, `simulator.go:181-243`) — **except for the contract's own
   SCID, which it may emit without limit** ("an SCID can generate it's token infinitely", `:191`). Destinations must be
   registered accounts; sending to an unregistered address panics the block-level processing (`:377-381`).
7. **A contract has exactly one native asset: its own SCID.** It cannot mint any other asset. LP tokens = pool SCID;
   a launched token = its own per-token contract (§10).
8. **No re-entrancy is possible**: TXs execute sequentially per block and a contract cannot invoke another contract.
9. Atomic units: 1 DERO = 100000 (`globals.go:295,345`).

---

## 3. Daemon JSON-RPC — `vendor/derohe/cmd/derod/rpc/websocket_server.go:300-345`

- Methods (namespaced `DERO.*`, `:319-340`): `Echo Ping GetInfo GetBlock GetBlockHeaderByTopoHeight
  GetBlockHeaderByHash GetTxPool GetRandomAddress GetTransaction SendRawTransaction SubmitBlock GetHeight
  GetBlockCount GetLastBlockHeader GetBlockTemplate GetEncryptedBalance GetSC GetGasEstimate NameToAddress`.
  Lower-case legacy aliases (`getsc`, `gettransactions`, …) exist (`:300-317`).
- Transports: HTTP `POST /json_rpc` and **WebSocket `/ws`**. The WebSocket upgrader accepts any `Origin`
  (`:251`). **No CORS headers are emitted anywhere** (grep `Access-Control` → none), so a browser page cannot use
  `/json_rpc`; **the app reads the node over `ws://host:port/ws`**, which is exactly what `tela.go:377,412,442`
  does. XSWD also proxies `DERO.*` (§5).
- `DERO.GetSC` (`rpc/daemon_rpc.go:232-251`): params `{scid, code, variables, topoheight, keysuint64[], keysstring[]}`;
  result `stringkeys{}`, `uint64keys{}`, `balances{hexscid:uint64}`, `balance`, `code`, `valuesstring[]`.
  **Encoding** (`rpc_dero_getsc.go:96-118`): Uint64 values arrive as numbers; **String values arrive hex-encoded**
  (`fmt.Sprintf("%x")`). Frontend `rpc.js` decodes accordingly.
- `DERO.GetGasEstimate` (`rpc_dero_estimategas.go`): same shape as `transfer` params + `signer`; runs the call in an
  in-memory `dvm.Simulator` on the current snapshot and returns `{gascompute, gasstorage}`. `gasstorage` is the fee
  to pass as `fees`. **Two quirks found by test:** the caller must add `SC_ACTION`/`SC_ID` (or `SC_CODE`) to `sc_rpc`
  itself, and the estimator's simulator never sets its height, so **`BLOCK_HEIGHT()` is 0 during an estimate**
  (`dvm/simulator.go:39-47,118`): height-gated entrypoints such as `nft.bas Mint` are rejected by the estimator
  while the real TX succeeds. The app and tools send those with a fixed fee (`opts.fee`, `SendWithFee`).
- `DERO.GetTransaction` (`daemon_rpc.go:199-228`): `{txs_hashes[], decode_as_json}` → per tx `signer`, `block_height`,
  `valid_block`, `as_json` (contains `SCDATA`), `code`/`codenow`, `balance`/`balancenow`.


### 3.x `DERO.GetGasEstimate` can panic at random on installs (2026-09-06)
`dvm.Simulator.SCInstall` (vendor/derohe/dvm/simulator.go:107 → sc.go:113 `Wrapped_tree`) names the graviton tree after a
simulated SCID; graviton rejects tree names whose first byte is 0x3a (`Bucket cannot start with ':'`), so about one
estimate in 256 fails with a daemon panic that the RPC returns as error -32098. Seen on a TELA DOC install
(`screens.js`, commit 11 attempt). The app treats an estimate failure as "would fail" and does not send; `tools/telainstall`
retries the estimate up to five times. Open question for the chain itself: a real TXID starting with 0x3a would hit
the same check at install time — not verified here.

## 4. Wallet JSON-RPC — `vendor/derohe/walletapi/rpcserver/rpc_websocket_server.go:298-320`, `rpc/wallet_rpc.go`

- Methods: `Echo GetAddress GetBalance{scid} GetHeight GetTransferbyTXID GetTransfers MakeIntegratedAddress
  SplitIntegratedAddress QueryKey transfer/Transfer scinvoke` (+ snake_case aliases). Default ports: mainnet 10103,
  testnet 40403, simulator wallets 30000+i.
- `transfer` params (`wallet_rpc.go:194-210`): `{transfers:[{scid, destination, amount, burn, payload_rpc}],
  sc, scid, sc_rpc:[{name, datatype, value}], ringsize, fees}` → `{txid}`. `rpc_transfer.go:60-72` appends
  `SC_ACTION`/`SC_ID`/`SC_CODE` automatically from `scid`/`sc`.
- `scinvoke` (`wallet_rpc.go:218-226`, `rpc_scinvoke.go`): `{scid, sc_rpc, sc_dero_deposit, sc_token_deposit, ringsize}` —
  `sc_token_deposit` can only burn the *invoked contract's own* asset (`rpc_scinvoke.go:45-49`), so for
  "send asset X to pool" we use `transfer` with explicit `transfers[{scid:X, burn}]`.
- `sc_rpc` datatypes (`rpc/rpc.go:52-61`): `S` string, `U` uint64, `H` 32-byte hash (hex in JSON), `I`, `F`, `A`, `T`.
- `install_sc` HTTP helper: `POST /install_sc` with raw code (`rpc_websocket_server.go:211`).
- History: `GetTransfers{scid, in, out, coinbase, min_height, max_height, …}` → `Entry` with `txid`, `burn`, `amount`,
  `payload_rpc`, `time` — enough for the client-side portfolio.

## 5. XSWD — `vendor/derohe-civilware/walletapi/xswd/{xswd.go,methods.go}`

- **Lives only in the civilware fork** (official repo has no `walletapi/xswd`); Engram (`go.mod`) and TELA
  (`replace github.com/deroproject/derohe => github.com/civilware/derohe`) use that fork, so it is the de-facto
  wallet API for TELA apps. Enabled in `dero-wallet-cli` (fork) via menu item **16 "Start XSWD Server"**
  (`cmd/dero-wallet-cli/easymenu_post_open.go:72,510-524`); Engram starts it with
  `NewXSWDServerWithPort` on `127.0.0.1:44326` (`engram/functions.go:3469`, `layouts.go:7313`).
- Endpoint `ws://localhost:44326/xswd` (`XSWD_PORT` `xswd.go:186`, route in `NewXSWDServerWithPort`).
- **Handshake** = first WS message is `ApplicationData` (`xswd.go:25-35`):
  `{id: 64 hex chars, name: 1-255 ASCII, description: 1-255 ASCII, url: must equal the HTTP Origin header and start
  with http(s)://, permissions?: {method: int}, signature?: bytes}` (validation `xswd.go:417-475`).
  Permissions may only be pre-requested with a wallet signature over the id (`:479-508`); without one every wallet
  method **asks the user** (forceAsk is on in both CLI and Engram defaults). Reply: `{"accepted": bool, "message"}`
  (`AuthorizationResponse` `:80-83`). Duplicate ids are rejected (`:847-855`).
- Requests: single JSON-RPC 2.0 objects; **batches are rejected** (`:800-807`); responses echo the request id
  (`RPCResponse` `:37-63`). Rate limit `rate.NewLimiter(10, 20)` — exceeding it **closes the connection**
  (`:598`, `:769-777`, error `-32070`).
- **`DERO.*` methods are proxied to the wallet's daemon without any permission prompt** (`handleMessage`
  `:640-693`). All wallet methods from §4 are available (`rpcHandler` = `rpcserver.WalletHandler`, `:206-209`) plus
  `HasMethod`, `Subscribe{event}`, `Unsubscribe`, `SignData`, `CheckSignature`, `GetDaemon` (`:255-262`, `methods.go`).
- Permission values (`:85-93`): `Ask=0 Allow=1 Deny=2 AlwaysAllow=3 AlwaysDeny=4`; denial errors `-32043` /
  `-32044` (`:118-119`). `Subscribe/SignData/CheckSignature/GetDaemon/QueryKey` can never be "always allowed" (`:193`).
- Events (`rpc/wallet_rpc.go:35-44`): `new_balance`, `new_topoheight`, `new_entry`, broadcast to subscribed apps.

### 5.1 XSWD facts found by the browser test (2026-09-05)
- **Response ids are the raw JSON text of the request id**: a request with `"id":"1"` is answered with `"id":"\"1\""`
  (`ResponseWithResult` stores `request.ID()`, which jrpc2 returns quoted). `app/xswd.js` sends numeric ids and strips
  quotes when matching.
- The handshake `url` must equal the browser's `Origin` exactly; `location.protocol + "//" + location.host` works for
  both `localhost` and `127.0.0.1` hosts.
- `GetDaemon` returns the wallet's daemon endpoint (`127.0.0.1:20000` on the simulator); the app opens its own
  WebSocket to it for reads and keeps `DERO.*` over XSWD as the fallback.
- Address text layout (`rpc/address.go:121-160`): bech32 payload = version byte `1` + 33-byte compressed key +
  optional integrated-address arguments; HRP `dero`/`deto` (`deroi`/`detoi` with arguments). `ADDRESS_STRING` in the
  DVM always renders `dero…` without arguments, which is the canonical form the app uses for keys and comparisons.
  **This bites any off-chain tool that looks up a contract key named after an address**: on testnet and on the
  simulator a wallet reports the same account as `deto1…`, so the key it computes does not exist. `vault.bas`
  names each signer's vote key that way, and the signer daemon read nothing until it converted first. Go tools use
  `simrpc.DVMAddress(addr)` (`tools/simrpc/address.go`, the same bech32 as the app's `canonAddress`); the vectors in
  `address_test.go` are taken from a live vault's vote keys (2026-09-07).

### 5.2 Balance decryption cost — a token-design constraint (measured 2026-09-05)
DERO balances are ElGamal ciphertexts; a wallet decrypts one by **search** (`walletapi/balance_decoder.go`,
baby-step/giant-step over a precomputed table: `TABLE_SIZE` default `1<<16` in the library's `init()`, `1<<21` in
`dero-wallet-cli`). Cost grows with the balance value ÷ table size. `GetBalance{scid}` re-syncs and decrypts on the
spot (`rpc_getbalance.go:27-31`), and XSWD holds `handlerMutex` for the whole call (`xswd.go:696-699`), so a slow
decode **blocks every other wallet request and even new XSWD connections**. Measured: a 2.4×10¹³-unit token balance
did not decode within 10 minutes with the default table and wedged the test wallet.
Consequences: (a) the launch form defaults to 1 000 000 tokens × 3 decimals = 10⁹ units and warns above 10¹¹ units;
(b) `app/xswd.js` keeps one request in flight and the portfolio only queries balances of listed launches;
(c) RISKS R-10. This is a property of DERO, not of DMZ; supplies in the 10¹⁴ range (pump.fun-style) are unusable here.

## 6. TELA — `vendor/tela`

- `TELA-INDEX-1.bas` / `TELA-DOC-1.bas` templates read verbatim (this repo's `deploy/` uses them). Both are private
  contracts (`InitializePrivate`) storing ART-NFA v2 headers `var_header_name/description/icon`, `dURL`, and
  `owner` = `ADDRESS_STRING(SIGNER())` or `"anon"` when installed with ringsize > 2 (`address()` helper).
- `UpdateCode(code, mods)` only when `owner != "anon"` and caller is owner; stores `commit` counter and
  `STORE(commit_no, HEX(TXID()))` → the on-chain commit history. `Rate(r)`: one 0–99 rating per address, ringsize 2.
- Size limits enforced by the Go package (`parse.go:22-24`): DOC code ≤ **18 KB**, DOC contract total ≤ 19.2 KB,
  INDEX ≤ **11.64 KB** (README's "9 KB" is conservative). Larger files → DocShards (`tela.CreateShardFiles`,
  dURL suffix `.shard`/`.shards`; a shard cannot be the entrypoint) or gzip (`nameHdr` ending `.gz`).
- docTypes: `TELA-HTML-1 TELA-JS-1 TELA-CSS-1 TELA-JSON-1 TELA-MD-1 TELA-STATIC-1 TELA-GO-1`.
- Serving: host clones DOCs to disk and serves with `http.FileServer` on `localhost:808x` (`tela.go:1140-1180`),
  **no CSP or extra headers**. The XSWD `url` must be `"http://localhost:" + location.port` (README, `app1/main.js:8`).
- MODs (`TELA-MOD-1/*.bas`, single-class rule for `vs`): `vsoo`, `vsooim`, `vspubsu`, `vspubow`, `vspubim`,
  `txdwd`, `txdwa`, `txto`. Public stores are keyed `var_<address>_<key>` (256-byte key/value cap).
- Install/update via `tela-cli` (`install-doc`, `install-index`, `update-index`, `file-shard`) or the Go API
  (`NewInstallArgs`, `NewUpdateArgs`). Endpoint defaults: simulator `127.0.0.1:20000`, testnet `40402`, mainnet `10102`.
- Gnomon exists (https://github.com/civilware/Gnomon, active) with an HTTP API on `127.0.0.1:8082`; TELA-CLI embeds it.
  **Not required** by DMZ (feature-flagged read cache only).

## 7. Simulator — `vendor/derohe/cmd/simulator`

- `simulator.exe [--data-dir=D] [--rpc-bind=127.0.0.1:20000] [--http-address=127.0.0.1:8080]`: daemon RPC on
  **127.0.0.1:20000**, block explorer on 8080, **22 pre-funded wallets** from fixed seeds (`wallets.go:37-61`) each
  with an RPC server on **30000+i** (`simulator.go:96`, `wallets.go:118`), empty password (`WALLET_PASSWORD = ""`).
  Genesis wallet seed `0206a2fc…`; wallet 0 seed `171eeaa8…` (address printed at startup).
- Auto-mines a block whenever the pool has a TX or every `BLOCK_TIME` seconds (`simulator.go:569-596`);
  `--noautomine` is Linux-only (`/dev/shm` trigger).
- Simulator network = testnet address prefix `deto1…`.

---

## 8. Prompt assumptions that turned out wrong or need adjusting

1. **XSWD is not in `deroproject/derohe`.** It is in `civilware/derohe` (§5). Facts confirmed from that fork.
2. **No cross-contract calls or reads exist.** Factory cannot verify a pool; a launcher cannot "seed a Pool"
   contract; a pool cannot push fees into a vault contract. Resolved in §10.
3. **Daemon HTTP RPC is unusable from a browser (no CORS).** Reads go over the daemon WebSocket `/ws` or the XSWD
   `DERO.*` proxy.
4. `TOKENVALUE`/`ADD_VALUE` (docs) do not exist; `ASSETVALUE(raw32)` does. `BLOCK_TOPOHEIGHT` is disabled.
5. `SIGNER()` at ringsize > 2 is 33 zero bytes, not `""`.
6. `ASSETVALUE`/`SEND_ASSET_TO_ADDRESS` need the **raw** 32-byte SCID, not hex.
7. `scinvoke.sc_token_deposit` can only burn the invoked contract's own asset; generic asset deposits use `transfer`.
8. Storage limits are per-call storage gas (≤ 20000 bytes) rather than a per-contract cap; INDEX limit is 11.64 KB.
9. The reference model is Go, not Python (no Python on this machine).
10. Placeholders confirmed: node `127.0.0.1:20000`, XSWD `44326`. `{{WALLET_RPC}} = 127.0.0.1:10103` is the mainnet
    default; on the simulator the wallets are `30000+i`.
11. Fee changed by operator instruction (2026-09-05) from 30 bps to **50 bps on every trade** (§10.3).

---

## 9. Verified-by-test status

| Item | Status (2026-09-05) |
|---|---|
| DVM semantics above | exercised by `tests/` through derohe's own executor: 200k-op pool fuzz, 3×60k-op launch fuzz, all scenarios green |
| simulator.exe runs on Windows | yes; `tools/simsmoke` and `tools/simlaunch` runs recorded in `docs/history/HISTORY.md (phase reports)` |
| XSWD handshake against a real wallet | yes: fork `xswd` package (`tools/xswdwallet`) driven from Chromium; launch, buy (with impact acknowledgement), publish, follow, stake all confirmed on chain; CSP probed first |
| Founders Pass fee loop | yes: `treasurybot -once` on the simulator claimed 16 629 units from six markets, deposited 8 314 (50 %), PayBatch paid 8 314 to the registered holder; contract counters agree |
| TELA install/serve round trip | yes: INDEX `ece54602…` installed and updated through commit 6 (26 DOCs, Phase 7), served by the `tela` package, full wallet flow passed from the served copy in Phase 4/5 |

---

## 10. Design decisions

### 10.1 Contract set (all DVM-BASIC, all immutable — none defines `UpdateCode`)

| File | Role |
|---|---|
| `contracts/pool.bas` | LP-owned constant-product pool, one per (asset ↔ DERO). Installed by any user, registered on the factory. |
| `contracts/factory.bas` | Registry + social directory: pools, launches, NFT collections, profiles, follows, protocol treasury address. Owned by the operator until `Renounce`: **only the treasury pointer and the registration fee are mutable**, only by the owner (`SetTreasury`, `SetRegFee`), and the owner's `Renounce` ends both for ever by deleting `owner` (2026-09-11; there is no transfer of ownership — the two-step `TransferOwnership`/`ClaimOwnership` was removed, FINDINGS_2026-09-10 SEC-K-02, LIV-08). Until it renounces, the factory owner is a trusted party: the app copies the treasury pointer from here. *Superseded on 2026-09-12 by v2, which has no owner: see the addendum below this table.* |
| `contracts/launch.bas` | Per-token memecoin contract: fixed supply minted at install, bonding-curve sale, **built-in AMM after graduation** (contract-owned, permanently locked liquidity), 50 bps fee, optional creator tax with three modes (creator / buyback-and-lock / holder staking rewards), staking vault. |
| `contracts/nft.bas` | ART-NFA-header NFT collection whose editions are **earned by holding**: lock ≥ `min_hold` of a chosen token for ≥ `hold_blocks` blocks, then mint one edition. Optional DERO mint price to creator. |
| `contracts/social.bas` | Profiles + follow graph (kept separate from the factory so the factory stays tiny and auditable). |

Why the launcher graduates into a *built-in* AMM (decision D-1, 2026-09-05): §2.6-2.8 — a contract can neither install
nor fund another contract, and a TX-driven "someone seeds the pool" hand-off is a custodial step. Keeping the curve and
the AMM in one contract makes graduation atomic and the LP position un-ruggable (no LP token exists to withdraw).
`pool.bas` remains for arbitrary existing DERO assets and for people who want LP-owned liquidity.

Four contracts were added by later decisions and belong to the same set: `contracts/swap.bas` (atomic-swap escrow,
§10.12), `contracts/founders.bas` (Founders Pass, §10.11), `contracts/vault.bas` (federation wrapper, §10.13) and
`contracts/board.bas` (the swap order board). None of them defines `UpdateCode` either, so the immutability claim in
the heading holds across all nine.

*Addendum, 2026-09-12.* The set is now twelve contracts (`contracts/*.bas`): the nine above, `contracts/htlc.bas` (the
Monero swap's DERO leg, `docs/XMR_SWAP.md`), `contracts/chat.bas` (rooms of messages) and `contracts/nfa.bas` (one
ART-NFA-MS1 asset per contract, `docs/NFA.md`). None defines `UpdateCode` or calls `UPDATE_SC_CODE`.
`contracts/factory.bas` is v2 (`e06c28a`, operator ruling MT-J15(b)): it has no owner, no setter and no `Renounce`.
`Initialize(treasury, regfee)` fixes the treasury pointer and the registration fee for the factory's life, and nothing
else writes them (`tests/registry_test.go` `TestFactoryFixedAtInstall`, `TestFactoryOldEntrypointsAreGone`). The
row above describes v1, and is kept as it was decided.

### 10.2 Fixed-point / overflow discipline (D-2)
- All maths in atomic units. Every product `a*b` with both operands possibly ≥ 2^32 goes through `mulhi/muldiv`
  (64×64→128-bit multiply via 32-bit limbs; 128÷64 long division, Hacker's Delight `divlu2`, two 32-bit quotient
  digits, no loops except the bounded `again` corrections). Remainder recovered as `lo − q*c` in wrapping arithmetic,
  which is exact because the true remainder < 2^64. Reference implementation `reference/uint128.go` is checked against
  `math/big` in `tests/`.
- Every other arithmetic line carries a comment with its bound. `bps(x, f) = (x/10000)*f + ((x%10000)*f)/10000`
  is exact and overflow-free for `f ≤ 10000`.
- Rounding: outputs floor; fees ceil; LP mint floor; LP burn outputs floor; required-input computations ceil.
- Additions guarded: `IF a + b < a THEN revert`.

### 10.3 Fee model (D-3, operator instruction 2026-09-05: "0.5% fee on every trade")
- `fee_bps = 50` on every swap, curve buy and curve sell. Immutable per contract, validated `≤ 100` at install.
- `pool.bas`: `pfee_bps` = share of the fee to the protocol treasury, default **5000** (half): 25 bps to LPs (stays in
  reserves, grows `k`), 25 bps accrued in `fd`/`fa` and paid to the treasury address recorded at install by
  `ClaimFees()`, which anyone may call at any ring size — a fee path only one key can move stops the moment that key
  does, and after install there is no operator to restart it (`pool.bas`, `launch.bas` and `nft.bas` read no `SIGNER()`
  here). The vault's own `ClaimFees` is the exception and is treasury-gated (`vault.bas:433-434`).
- `launch.bas`: whole 50 bps to the treasury (no external LPs to reward). Post-graduation, `pfee_bps` is the same
  parameter; the non-protocol share stays in the locked reserves (price support).
- Treasury address is stored per contract at install (copied by the app from the factory); the app refuses to show a
  pool/launch whose treasury differs from the factory's, or whose code differs from the shipped template.

### 10.4 Bonding curve (D-4)
- Virtual constant product (pump.fun style). Supply `S` fixed at install; `creator_bps ≤ 500` (5 %) sent to creator;
  `C = 80 %` of the rest sold on the curve, `Lq = 20 %` reserved for the AMM seed. Virtual DERO `vd0 = G/3` where
  `G` = graduation DERO. This choice makes the curve end price equal the AMM start price exactly
  (`Lq = C·vd0/(G+vd0) = C/4`) and gives a 16× price multiple over the curve.
- Buy: `t_out = muldiv(d_eff, vT, vD + d_eff)`; the final buy is capped at the remaining tokens with the unused DERO
  refunded; sell: `d_out = muldiv(t_in, vD, vT + t_in)`. Real DERO never goes below zero (proof in
  `contracts/README.md`). Graduation fires when the curve is sold out: `phase = 1`, `rd = real DERO`, `ra = Lq`.

### 10.5 Taxes, buyback, holder rewards (D-5)
- DERO cannot tax wallet-to-wallet transfers (contracts never see them, §2.7, `token.bas` header comment). Tax
  therefore applies to trades on the launch contract only: `tax_bps ≤ 1000`, immutable, taken from the DERO leg.
- `tax_mode`: `1` creator (claimable), `2` buyback-and-lock (anyone can call `Buyback()` after graduation: the
  accrued DERO buys from the built-in AMM and the tokens are locked forever, counted in `locked`), `3` holders
  (stakers): reward-per-share accumulator with `PREC = 2^32`, `Stake()/Unstake(amt)/Claim()`, dust stays in contract.
- "NFTs that reward you for holding tokens": implemented as `nft.bas` hold-to-mint because the only trustless proof
  of holding available to a contract is a deposit it can see (`ASSETVALUE`).

### 10.6 Storage keys (short string keys; frontend decodes hex)
Pool/launch common: `kind`, `v`, `asset`(raw), `treasury`(raw), `fee`, `pfee`, `rd`, `ra`, `fd`, `fa`, `tc`, `t_<n>`,
`pc`, `p_<n>`. Pool: `lp`, `lp_<address>` not needed (LP is an asset). Launch: `phase`, `S`, `C`, `Lq`, `sold`, `vd0`,
`G`, `tax`, `tmode`, `tacc`, `locked`, `st_<addr>`, `sd_<addr>`, `sacc`, `stot`, headers `nameHdr descrHdr iconURLHdr tagsHdr typeHdr` (ART-NFA).
Trade record `t_<n>` = `"<side>|<address>|<in>|<out>|<height>|<ts>"` written **only when `publish = 1`**;
price point `p_<n>` = `"<height>|<ts>|<rd>|<ra>"` written at most once per block on the first swap of that block
(gives the chart without an indexer). Both are read with `keysstring` ranges, not `variables:true`, once counts grow.

### 10.7 Ringsize and privacy (D-6)
Every state-changing entrypoint requires ringsize 2 (checked with `IS_ADDRESS_VALID(SIGNER())`) because output is
sent to `SIGNER()`. UI copy: "Trades reveal your address to this contract. Your balances stay encrypted."

### 10.9 Live charts and market metrics (D-8, operator request 2026-09-05)
Source of truth for every chart is contract state: `p_<n>` = closing reserves of each block that changed the pool,
plus the live `rd/ra`. Candles are built from those per-block closes (open = previous close, high/low/close exact at
block resolution, ~18 s), the in-progress candle from live reserves, and volume bars from the **net reserve flow per
interval**, so unpublished trades are counted (netted per block) without revealing them individually — that is the
privacy line and it is stated in the app. Published trades (`t_<n>`) give exact per-trade sizes and are shown
separately. Change windows (5m/1h/6h/24h/7d) use the last recorded price at or before the window start, falling back
to the first point for young markets. Refresh cadence is the block clock: on every new height the app probes a fixed
key set per contract (`rd ra tc pc lp phase stot tacc fd fa lh lt real sold locked sacc rpool minted`) and reloads
only contracts whose probe changed. Intervals adapt so a young market still shows ~60 candles.

### 10.8 Frontend data path (D-7)
Reads: daemon WebSocket (`DERO.GetSC`, `DERO.GetTransaction`, `DERO.GetHeight`) at the endpoint returned by XSWD
`GetDaemon`, falling back to the XSWD `DERO.*` proxy, then to a user-configured endpoint. Writes: XSWD
`transfer` with `sc_rpc` + `transfers[{scid,burn}]`, fees from `DERO.GetGasEstimate`. No server anywhere.
Files split to stay under 18 KB per DOC (or sharded), which fixes the module boundaries in `app/`.

### 10.10 Client hardening and incremental reads (D-9, 2026-09-05)
- **Content Security Policy** as a `<meta http-equiv>` in `index.html` (TELA serves plain files; there are no HTTP
  headers to set). `default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'` (inline style
  attributes are used for widths and gradients; styles cannot execute code); `connect-src 'self' ws: wss:
  http://127.0.0.1:* http://localhost:*` because the node endpoint is user-configurable; `img-src 'self' https:
  data:`; `object-src 'none'; base-uri 'none'; form-action 'none'; frame-src 'none'`. Verified in
  `tools/uitest/ui.mjs`: an injected inline script is refused before the functional pass (which runs with
  Playwright's `bypassCSP` because its own `waitForFunction` polls through `eval`).
- **Untrusted strings**: `ui.clean` + text nodes everywhere; the only `innerHTML` in the app is the chart tooltip and
  it is built from numbers. `ui.safeUrl` accepts `https://` only. `ui.avatar` derives a gradient from an FNV-1a hash
  of the id so no image is needed for a good-looking listing; images load with `referrerpolicy=no-referrer` and the
  `images` setting (default on) can disable them.
- **Transaction gate** (`app.tx`): the contract must be a verified listing, the factory or the social contract;
  ids must be 64-hex; `MAX_SLIP` 10 % is enforced on `min_out` before the wallet is asked; impact > 15 % requires an
  acknowledgement switch in the sheet.
- **Incremental sync** (`feed.js`): first load bounded to the last 4 000 `p_` and 2 000 `t_` keys, then per block a
  probe of 18 fixed keys and range fetches of only the new keys via `keysstring` in chunks of 250. Cache v2 in
  localStorage (`veil.m.<scid16>`) holds `{tc, pc, points[], trades[]}` compactly (last 1 500 points / 600 trades per
  market, written at most every 30 s per market and on `pagehide`, so a busy block never stalls on synchronous storage). `variables:true` remains only
  for the social contract. Per-user keys (`st_`, `sd_`, `lk_`, `lh_`, `lm_`) are read by exact key for the
  connected address (`data.myKeys`).
- **Why not an indexer by default**: a Gnomon cache would make first loads cheaper but adds a trusted middle; the
  app keeps the node as the source of truth and treats the cache as optional (D-7).

### 10.11 Founders Pass: fee-sharing NFT (D-10, 2026-09-05)
Operator ask: an NFT for the launch of the exchange that receives a share of every transaction, pays holders to
their wallets, and is tradeable. Two DERO facts shape it:
- **No contract-to-contract payments** (`SEND_DERO_TO_ADDRESS` takes a wallet address; a SCID has none), so market
  contracts cannot forward fees to the pass by themselves. The treasury wallet does it (`tools/treasurybot`), and
  the contract records the promise (`share` bps, immutable) and every deposit (`dep`, `mdep`) and payout (`paid`)
  so the promise is auditable by anyone. This is the single trust point and it is documented as R-13.
- **Encrypted balances**: the contract cannot enumerate holders, so passes must be registered in the contract to
  earn (`Register` deposits units, `Withdraw` returns them at any time). Registered units are what `hst` counts.
- **Payout model**: per-unit accumulator `acc` (×2^32, 128-bit `muldiv`), O(1) per deposit. `Claim` pulls;
  `PayBatch(from, n ≤ 15)` pushes to registered wallets in slices, because each settle runs the 128-bit helpers and
  costs ≈ 0.41 M of the 10 M compute gas per call (measured: 15 holders = 6.18 M). The treasury bot pushes after
  every deposit, so holders normally never need to claim.
- **Rounding**: `distribute` moves ceil(inc·hst/2^32) out of the pool for an increment of floor(pool·2^32/hst), so the
  residual (< 1 atomic unit per registered unit) is never credited twice, and `settle` caps a payout at `rpool`.
  The same double-credit existed in `launch.bas`'s staker distribution (bounded by one atomic unit per `dist()`
  call, taken from the contract's other DERO); fixed the same way, reference model updated, fuzz green. The old
  launch hash is listed under `templates.launch.legacy` so already-installed simulator launches stay verified.
- **Limits**: `maxsupply ≤ 100 000`, `price ≤ 10^13` (so `n × price` cannot wrap), `reserve ≤ 20 %`, `mfee ≤ 10 %`.
  `acc` overflows only after ≈ 42 949 DERO of rewards *per registered unit*; a deposit that would overflow stays in
  `und` rather than being lost.
- **Marketplace**: fixed-price listings inside the contract (`ls_/lp_/lu_<id>`), exact payment required, `mfee`
  of the price to the holders, the rest to the seller. Listed units do not earn. The app reads the last 300 ids.
- **Front-end**: singleton from `config.js` (`founders`), code verified against `templates.founders.sha256` once
  per session like every listing; the transaction gate accepts it explicitly.

### 10.12 Cross-chain assets (D-11, 2026-09-06)
Operator questions: XMR / ZEC / SOL on DMZ without a centralised oracle; Haven-style storage of XMR inside DERO.
- The DVM has `sha256` / `sha3256` / `keccak256` (25 000 gas each), `HEX`/`HEXDECODE`, `BLOCK_HEIGHT`, and no signature
  verification or foreign-chain reads. Consequences: hash-time-locked escrows are possible; bridges, wrapped assets
  and light clients are not without an oracle or custodian.
- **Chosen:** peer-to-peer atomic swaps. `contracts/swap.bas` is the DERO leg (any DMZ asset or DERO); the other leg is
  a mirror escrow on the counter-chain (script on BTC/LTC/transparent ZEC, a program on Solana). The app will add an
  order board and a client behind an opt-in RPC setting so the CSP stays strict by default.
- **Monero and shielded Zcash:** no scripts; a secret-as-key-share swap with DERO as the scripted side is possible in
  principle, but the counterparty must verify off chain that the hash preimage equals the key share (DERO cannot
  check adaptor signatures). Research item; no promise.
- **Not chosen:** Haven-style synthetics (mint xXMR against DERO at an oracle price). They require whitelisted price
  signers (a median over several at best), give price exposure rather than real XMR, and failed in practice through
  stale-price arbitrage and collateral drain. A federated wrapped asset is the honest alternative if exposure inside
  DERO is ever wanted; it is trusted and says so.

### 10.13 Federation wrapper for instant cross-chain markets (D-12, operator instruction 2026-09-06)
The operator requires COIN/XMR and COIN/ZEC markets that fill instantly with pooled liquidity, "exactly how a
Solana memecoin would". A pool must hold its reserve inside the VM that settles the trade, and DERO cannot see or
sign for Monero or Zcash, so the reserve is held by a k-of-n signer federation and represented on DERO by wrapped
assets (wXMR, wZEC, wSOL) minted one-for-one against deposits and burned to withdraw (`contracts/vault.bas`).
Rules: no mint without k signer votes on an identical (id, to, amount); a deposit id mints once, ever; outstanding
supply never exceeds `cap`, and `cap ≤ capratio × Σ bonds`, checked at `Seal`, at `SetCap` and on every `Mint`, and
re-established downward by `Slash`; `capratio` is a required install parameter, not an oracle — `cap` is in foreign
base units and `Σ bonds` in DERO atomic units, so it is the operator's written-down price assumption frozen at
install (3 in the simulator and the tests; risk W11 in `docs/PLATFORM_BACKLOG.md`). The check is implemented as
`cap / capratio > bondtot`, so it is permissive by at most `capratio − 1` base units. `cap` is raised only by k votes;
signers post DERO bonds that k votes can slash; k votes halt, and the daemons vote the halt in both directions from
public state, so it clears itself; the owner has no power after `Seal()`; deposit addresses are per-user indexes
attested by k votes (no memos). Threshold custody: FROST RedPallas for Zcash (reddsa, demonstrated 2-of-3), native
multisig for Monero behind low caps until FROSTLASS ships, FROST ed25519 for Solana. Proof of reserves by
published viewing keys. Disclosure: every wrapped balance and wrapped-quoted market shows "federation-backed" with
the signer set, cap, bonds and reserve link. The atomic-swap path (`swap.bas`, the key-share proofs in
`research/crosschain/poc`) stays deployed as the trustless exit so nobody depends on the federation. The deposit fee
of the D-12 sketch was not built: `Mint` credits the whole deposit. The vault's two fees are `wfee`, taken from each
burn and accrued in `feepool` for the treasury, and `regfee`, paid once per deposit index and forwarded to the
treasury immediately. A DERO gas sliver for foreign-only users is therefore a signer-side courtesy the contract
neither funds nor can enforce — treat it as a promise of the federation, not a property of `vault.bas`. Full reasoning:
`research/crosschain/README.md (Decision)`; spec: `research/crosschain/RESEARCH.md (Specification)`; security: `research/wrapper/README.md (security review)`.

### 10.14 One build, three networks (D-13, operator instruction 2026-09-06: "build for mainnet and for testnet")
The contracts and the app are network-agnostic. `deploy/networks.json` is the per-network record (daemon,
factory/social/founders/vault SCIDs, template hashes with legacy versions, TELA index); `tools/simdeploy -network X`
installs and records a block; `tools/genconfig` renders `app/config.js` for development or `dist/<network>/`
bundles (config, templates, manifest with sha256 and DOC sizes, refuses if a template hash disagrees with the
record); `tools/telainstall -network X` and `tools/netwallet -network X` set the wallet and TELA flags
(simulator = testnet+simulator, testnet, mainnet). The app warns when the connected node's network differs from
the build's. Rules: mainnet transactions are only ever sent by an operator following `deploy/README.md (Mainnet)` with its
go/no-go gates; testnet is exercised first (`deploy/README.md (Testnet)`); the same commit must pass the simulator suite.
