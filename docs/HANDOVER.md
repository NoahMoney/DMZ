# DMZ handover: how to continue, how to build the wrapper without mistakes, how to wrap up

Written 2026-09-06 for the next agent or engineer (the operator named Opus 5). It assumes you have read
`README.md`. Everything here is either a standing operator ruling, a verified fact with its source, or a plan
whose done-criteria are stated. Where this file and the code disagree, the code and its tests win; fix the file.

## 0. Standing rules (operator rulings, do not relitigate)

0. **Nothing may need a human.** The operator intends to revoke their own access after deployment: the contract
   owner keeps no power after `Seal`, and the TELA INDEX key may be discarded. So **any state that requires a
   privileged party, a manual flag or an operator decision to leave is a permanent freeze of user funds**, and is
   a defect however well it is documented. Every limit must either empty by itself or expire into a payment;
   every judgement must be a k-of-n vote that daemons cast and clear from public state. Doing nothing must
   release funds; taking action must be what stops them, never the reverse. This rule caused the loop-19 redesign
   (a size breaker that refused withdrawals became a size-tiered delay, and the per-deposit maximum was deleted
   outright because a deposit above it could never be minted and nobody could refund it). Check every new
   mechanism against it before writing the code.

1. **Nothing touches mainnet.** Simulator → private testnet → mainnet, and mainnet only by the operator with the
   go/no-go gates in `deploy/README.md` (Mainnet). Every fund-moving tool refuses mainnet without `-yes`.
2. **Never store private keys in the browser; never proxy transactions through a server.** The wallet signs over
   XSWD; the app builds transactions and reads the node. No backend, no analytics, no cookies.
3. **Never claim trustless** for the wrapper or anything else unless the trust assumptions justify it. Name every
   trusted party (`research/wrapper/README.md`). Label every metric exact / provable / estimated / unavailable.
4. **Money is `uint64`.** No floats in contracts, models, harness or bots; the suite's C5 lint enforces it.
5. **Inclusion is not execution.** Anything off-chain that remembers an action proves it by reading contract
   state back (bot: the monotone counters `fc`, `dep`; app: balance moves or key changes with a caller-specific
   check). A shared counter is not enough on its own where a co-signer can move it in the same block: a signer
   proves its own vote by its own vote key `<f>v_<scope>_<address>`, which holds the choice it made.
6. **Contracts are immutable; templates are hashed.** A contract change = new template hash: regenerate with
   `tools/simdeploy` (all earlier hashes as `legacy`), redeploy the simulator, run both UI passes. Never edit
   `app/templates/*.bas` by hand. LF line endings are enforced (`.gitattributes`) because the hash is of the bytes.
7. **Commit after every loop that changes code and push to `origin main`** (the operator asked for the project
   to ship to GitHub on 2026-09-06; the published history starts from one clean commit because earlier local
   commits carried machine paths). Never force-push; never push anything untested; the operator controls releases.
8. **Product name DMZ, dURL `dmz.tela`.** Code identifiers still say Veil (`window.Veil`, `VEIL_CONFIG`, `veil.*`
   storage keys, Go module `veil`); rename them in one deliberate pass with a localStorage migration, not piecemeal.
9. **No personal or machine-specific information in the repository** (paths, names, emails, hostnames). The UI
   test takes `$PW_DIR`; the WSL scripts take `$REPO`.
10. **Work in ~20-minute loops** and append one record per loop to `docs/history/HISTORY.md` (last section) with
    the headings COMPLETED / DISCOVERED / SECURITY / UX / PERFORMANCE / ECONOMICS / STORAGE / PRIVACY /
    COMPETITIVE GAP / UNCONSIDERED (≥ 3) / NEXT. Keep looping until the operator says STOP. If blocked, research,
    write the missing specification or tests, or improve another dependency; never wait.
11. **Do not rebuild what works.** The gap matrix marks what exists; the backlog says what to change.

## 0b. The published repository

`https://github.com/NoahMoney/DMZ`, public, branch `main`. The published history starts from one clean root
commit written on 2026-09-06: the earlier local commits carried machine-specific paths and are kept only in the
local tag `local-history-before-publish`, which must never be pushed. Work continues on `main`, which tracks
`origin/main`.

Practical constraints learned while publishing:

- Pushes larger than about two megabytes returned HTTP 408 on this connection. Push small commits; if a push
  fails, split it (the screenshots went in their own commit).
- `tools/publish.sh` records the whole mechanism (read the stored credential with `git credential fill`, check
  the account through the API, create the repository, build a root commit with `commit-tree`, push). The token is
  never printed and never written to a file.
- The repository has no licence file yet, so it is "all rights reserved" by default; adding one is a wrap-up item
  (§6). There is no `SECURITY.md` either, and the README asks for findings as issues.

## 1. Where things stand (end of loop 16)

- Verified green at the last commit: the contract suite (`bash tests/run.sh`, ~450 s), `go test ./vaultsigner
  ./treasurybot`, `verifyvendor`, the read-only and wallet UI passes (launch + register, published buy, private
  buy at ring 16 with no record written, stake, pass mint + register), the wrapper end to end on the simulator.
- The simulator registry: `deploy/networks.json` (`simulator` block) and `app/config.js`. The private testnet:
  `deploy/networks.json` (`testnet` block), TELA INDEX in `deploy/tela-testnet.json` (installed under the old dURL;
  the next install uses `dmz.tela` and needs `tools/genconfig -dist -network testnet` first, because the
  templates changed in loop 16).
- Not done: everything in `docs/PLATFORM_BACKLOG.md` from B1.1 down, except W1 (the daemon's port to vault v3,
  done 2026-09-07 — see §5.1b); the wrapper's real backends (§5 below).
- Untracked local state you will not have: `vendor/` (recreate: `deploy/README.md` §1, then `verifyvendor`),
  `.sim/` (simulator data, e2e logs), `.testnet/` (wallets, seeds: ask the operator), `dist/` (regenerate).

## 2. Environment and daily commands

Ports and roles used by every script and test (change them consistently if you must):

| Thing | Where |
|---|---|
| Simulator daemon | `127.0.0.1:20000` (RPC + WS), explorer `:8089`; wallets `127.0.0.1:30000`–`30021`, 8 DERO each |
| Simulator roles | wallet 0 owner/deployer, wallet 2 treasury, wallets 3–5 vault signers, wallet 6 private-buy receiver, 16–21 traffic |
| App | `tools/serve` on `127.0.0.1:8082`; XSWD wallet on `localhost:44326` (`tools/xswdwallet`, auto-approving, test only) |
| Private testnet | patched `derod` RPC `40402`, getwork `40400`; miner wallet `40403`, treasury `40404`, deployer `40405` |
| Foreign chains (signer chain table) | Monero stagenet wallet-rpc `38082`, Zcash testnet `18232`, Solana devnet; mainnet `18082` / `8232` |

Commands you will run every loop:

```
bash tests/run.sh                                   # contracts + models + fuzz (~450 s)
cd tools && go test -count=1 ./vaultsigner ./treasurybot && go run ./verifyvendor
PW_DIR=<playwright dir> bash tools/uitest/run.sh     # both UI passes, needs the simulator, serve and xswdwallet
bash tools/simvault/e2e.sh <vault scid>              # after: simvault -install -cap … -bigbps … -bigdelay …
git add -A && git commit -m "…"                      # local only
```

Expected timings on a laptop: suite 380–460 s; UI read-only 1 min; wallet pass 8–12 min (each simulator block
is 18 s and the pass waits for inclusion and execution proofs); vault e2e 8–10 min.

Pitfalls that cost hours before (all sourced in `DECISIONS.md` and `RISKS.md`):

- `SIGNER()` is 33 zero bytes above ring size 2; a reverting call above ring 2 burns the deposit. Only the `…To`
  entrypoints are safe at ring 16, and they refund-and-commit instead of reverting.
- `DERO.GetGasEstimate` runs contracts at `BLOCK_HEIGHT()` 0: height-gated entrypoints (`Released` with a delay,
  Founders `Mint` with a start) must be sent with a fixed fee. It also panics on content whose simulated id starts
  with `:` (R-16); installs then use a size-based fee. Real install txids start ≥ 0x80, so live contracts are safe.
- A wallet whose balance changes every block decodes for minutes per change (R-10): the miner never transacts.
- DVM-BASIC: `IF … THEN GOTO` only; ascending line numbers per function; no `Uint64 + String`; a missing `LOAD`
  panics; 20,000 storage gas per transaction including the code; a contract cannot hold its own asset.
- `Initialize` stays callable after install unless guarded (`initd`); every contract guards it; a test covers it.
- Shell wrapper: commands containing apostrophes or backslash regexes fail; write a script file and run it.
- The founders `minhst` gate: below `1 + ⌊maxsupply/50⌋` registered units nothing is released; tests that
  register one unit must expect `und` to grow.

## 3. What "green" means before a commit

- `tests/run.sh` prints `ok … veil/tests`; no `FAIL`; the lint prints nothing.
- Both UI passes end with `UI TEST OK` and the wallet pass prints `published records before/after the private
  buy: n → n`.
- `verifyvendor` prints "vendor tree matches deploy/vendor.lock" (after a tool source change: rebuild the binary
  with its `build.sh` and `verifyvendor -write-binaries`).
- No new `float` in money code, no new external dependency, no new `innerHTML`, no new inline script or style.
- The loop record is appended and cites the commands run.

## 4. The platform programme: what to build next, in order

**The visual identity is its own programme: [`docs/DESIGN_DIRECTIVE.md`](DESIGN_DIRECTIVE.md)** (operator brief,
2026-09-07). It is design-led — render, critique, implement, re-render — and it is bounded by the things the brief
does not know about: the 18 432-byte TELA DOC cap per file, a CSP that forbids external fonts and stylesheets, no
build step, and a UI test that is the regression gate. Read its §1 before touching a stylesheet. It changes
tokens and primitives, never business logic, and every functional assertion in `tools/uitest/ui.mjs` must survive
it.

Follow `docs/PLATFORM_BACKLOG.md` for everything else. Notes that are not in the backlog:

- **B1.1 Ranking v2** (`app/stats.js`): keep everything derived from `p_<n>` and `t_<n>`; add per-block notional
  from `|Δrd|` with a floor (0.05 DERO), publisher weight `log1p(notional)`, decay `exp(−age/τ)` per window, age
  normalisation by the market's own history length, and a self-trade discount computed from published records
  whose buys and sells by one address net to < 10 % of their gross inside the window. Put the formula and the
  synthetic-history tests in `docs/RANKING.md` and a Node test under `tools/uitest/` (no framework). Show the
  three terms on the card ("why trending").
- **B1.6 Metric legend** (`app/token.js`, `app/ui.js`): one helper `U.metric(label, value, kind)` with kinds
  exact / provable / estimated / unavailable rendering a small tag and a hover; replace every stat call.
- **B1.10 Launch wizard** (`app/forms.js`): six steps on one route with the existing validation; the safety step
  is static text derived from the template (no mint, no burn, fixed supply, no admin, immutable metadata,
  images mutable at their host). Banner/website/socials/category require a new launch template (`bannerHdr`,
  `siteHdr`, `socialHdr`, `categoryHdr`, each ≤ 200 chars, https only): change `contracts/launch.bas`
  `InitializePrivate`, the model, the tests, `contracts/README.md`, then `simdeploy` (legacy hashes) and the UI pass.
- **B2.6 Asset pipeline**: `filehash` (sha256 of the bytes) in the NFT and launch templates; browser-side hash
  check; a canvas thumbnail (no SVG, no animation); `tools/nftpack` for hashing/resizing/optional TELA DOC upload.
- Always: the wallet pass launches from the app, so every template change is exercised there.

## 5. The wrapper: building the real backends without mistakes

The contract (`contracts/vault.bas` v3), the signer daemon (`tools/vaultsigner`), the trust statement and the
pipeline specification exist and are tested against a simulated foreign chain. What is missing is the foreign
side. Read, in order: `research/wrapper/README.md`, `research/wrapper/DESIGN.md` (Mechanism review, Controls,
Pipeline specification §C1–C6), `research/wrapper/RUNBOOKS.md`, `contracts/README.md` (vault), and the daemon.

### 5.1 Decisions already taken (do not reopen without evidence)

- **Zcash (the coordinator's decision of 2026-09-15, which replaces Orchard + FROST for launch):** the reserve is
  held in **transparent pay-to-script-hash k-of-n multisig** (`OP_k <keys> OP_n OP_CHECKMULTISIG`, the n compressed
  keys sorted, BIP 67). Each signer holds its own BIP 32 seed and publishes one account key; every vault address is
  derived from the n account keys by non-hardened steps, so the signers, the app and any auditor derive the same
  P2SH address for vault index j (`tools/vaultsigner/zcashvault`, §5.4).
  - *Why:* it is the most battle-tested threshold custody there is; it reuses the ZIP 244-vector-checked v5
    transparent builder and ECDSA signer in Go (`tools/zcashtx`, copied from `tools/zecswap` at c58e3e3) with no new
    dependency; anyone can audit the reserve on chain from the descriptor the signers publish under `viewkey`; and
    its privacy equals that of the transparent ZEC swaps DMZ already ran.
  - *What it costs:* deposits and the reserve are **public** — every deposit address, amount and time, and every
    withdrawal, is visible on the Zcash chain and tied to the vault by the published descriptor; **withdrawal
    destinations must be transparent addresses** (`t1…`/`t3…`; a shielded or unified destination is vetoed and the
    burn returned); and each release's ZIP 317 network fee comes out of the withdrawal, because what leaves the
    reserve must be exactly what `Released` removes from `sup`.
  - *Finality:* Zebra's 100-block consensus reorganisation limit: a deposit is minted, and a payment certified with
    `Released`, only at or below tip − 100, read from every configured node.
  - *The upgrade:* Orchard with re-randomized FROST (ZIP 312; `research/crosschain/poc/frost-zec` demonstrates 2-of-3
    RedPallas) remains the documented path to shielded custody. It needs a new vault and a migration of the reserve.
- **Monero:** native wallet-rpc multisig k-of-n at launch (experimental flag, all signers under contract, treated
  as trusted-not-malicious, low cap), migrating to FROSTLASS / FCMP++ multisig when released; per-index
  subaddresses as deposit addresses; confirmations 10; the private view key is published.
- **DERO side:** `SIGNER()` at ring size 2 gates every vote; tuple-keyed votes with slash, halt and cap epochs;
  `SetLimits` before `Seal`; a burn is paid only at or below DERO's stable height and not before the release
  height the contract recorded at burn time (`w_<j>_rel` = the burn height plus `wdelay`, plus `bigdelay` when
  the burn exceeds `bigbps` of the supply).
- **Solana** is optional and last.

### 5.1b State of the daemon (2026-09-07)

`tools/vaultsigner` speaks v3 (it refuses to start against any other `v`) and runs the whole loop on the
simulator: attest, mint against `(txid, vout)`, pay abroad, verify its own payment on the foreign chain and vote
`Released`, veto a destination the chain cannot pay, and halt on a reserve shortfall.
`bash tools/simvault/e2e.sh <scid>` walks the wrap and the unwrap end to end — register, attest, deposit, mint,
burn, the payment, the verification, the `Released` quorum that drops `sup` — and fails if the vault halts at
any point, because an ordinary withdrawal must never halt it. What is still missing is only the foreign side
(§5.3 and §5.4).

Rules the daemon now enforces, each of which came from an audit finding:

- **Every vote is proved by reading back this signer's own vote key** (`<f>v_<scope>_<address>` holds the choice
  it made). A counter can be moved by a co-signer in the same block; the vote key cannot.
- **A deposit older than the address it was paid to is never credited.** An address derived from a future index
  can be paid before anyone owns it, and crediting that would hand one user's coins to whoever registers the
  index next. The anchor is the foreign height at which this signer first saw the address published, kept in the
  state file. After a state loss the anchor is "now", which is conservative: other signers carry the vote.
- **A deposit is re-read at vote time** (`Verify`) and never voted above the chain's finalized height.
- **An outsized withdrawal is delayed, never refused.** `bigbps`/`bigdelay` are fixed at install and never
  changeable; `Burn` records `w_<j>_rel` = the height from which the withdrawal may be released (`wdelay`, plus
  `bigdelay` when it exceeds `bigbps` of the supply). Doing nothing pays the user; stopping the payment takes a
  k-of-n `Veto`. There is no daemon flag and no operator override.
- **A halt is a condition, not a decision.** The daemon votes `Halt(1)` only while the foreign reserve is below
  what the contract owes, and votes `Halt(0)` on its own once it is not. A halt reason the daemons cannot
  compute from public state will be voted back up by them on their next pass.
- **The reserve is compared to `sup` less the withdrawals in flight**, never to `sup` alone: `reserve ≥ sup −
  inflight`, where `inflight` is every burned withdrawal the contract has not closed. The claim leaves the
  reserve when the federation pays it and leaves `sup` only when the `Released` quorum records the payment, so
  between those moments the reserve is legitimately short. Comparing against `sup` alone halted the vault on its
  first withdrawal, and because `Released` refuses while halted the halt could never clear — a permanent freeze
  with no operator to lift it (found and fixed 2026-09-07; `tools/simvault/e2e.sh` now fails on any halt).
- **A signer votes `Released` only for a payment it verified itself** (`Backend.VerifyRelease`, re-checked
  before every vote): a peer or coordinator cannot make it certify a payment nobody made. A payment that stays
  invisible for `-verify-tries` passes is treated as one that did not happen and the backend is asked to pay
  again, which is safe because `Release` is idempotent per `j` — never voting at all would be a freeze.
- **A withdrawal already being paid is never vetoed** — the units would go back while the coins were gone.
- The state file is bound to one vault (its intents name that vault's withdrawal indexes) and the lock file is
  removed on every exit path, including a signal.

### 5.2 The backend contract every implementation must satisfy

`tools/vaultsigner` `Backend` is the whole interface a foreign chain must satisfy, and every method must fail
rather than guess: `Height()`, `Finalized()`, `Deposits()`, `Verify(d Deposit)`, `Address(index)`,
`ValidDest(dest)`, `Release(j, dest, amount)` (idempotent per `j`), `VerifyRelease(j, ftx, dest, amount)`,
`Reserve()`, `Health()`. The sim, Zcash and Monero backends implement it (the Monero release path is single-wallet
only until a Monero signing coordinator exists). What each one must mean on a real chain:

- `Finalized() (height int64)` — the highest height this chain treats as irreversible under the network table
  (Zebra: tip − 100; Monero: tip − 10 spendable age is *not* finality, use tip − 10 as policy and re-verify);
  the daemon votes `Mint` only for deposits at or below it and re-reads the deposit at vote time (S16b).
- `Verify(d Deposit) (still there bool)` — re-fetch by txid at vote time; a deposit that vanished is never voted.
- `ValidDest(dest string) bool` — whether the string is spendable on this chain. A withdrawal whose destination
  is not is vetoed rather than paid into nowhere, so this must never be optimistic.
- `VerifyRelease(j, ftx, dest, amount) (bool, error)` — re-read the foreign chain and say whether `ftx` really
  pays `amount` to `dest`. This is what turns the string `Release` returned into a fact; the daemon calls it
  before every `Released` vote, and a "not visible yet" answer must be an error or `false`, never a guess.
- `Health() error` — the node/wallet-rpc reachable, synced, the multisig or FROST share present; the daemon
  refuses to pass otherwise (fail closed).
- Amount semantics: foreign base units as `uint64` (piconero 1e12, zatoshi 1e8) with an explicit **per-vault
  unit** stored in the contract (`decimals`), converted exactly; refuse any amount above `cap` at the boundary.
- `Release` must be **deterministic across signers**: given `(j, dest, amount)` every signer must build the same
  unsigned transaction (same inputs selected by a documented rule, same fee policy, same change address) so the
  threshold signing can proceed without a coordinator; the foreign txid becomes `ftx` and k signers vote
  `Released(j, ftx)` on the same string.
- Every backend ships with a **replayable log**: the daemon must be able to recompute its state from chain data
  and its intents alone (DESIGN §C6 "deterministic and replayable").

### 5.3 Monero backend, step by step

1. Stand up `monerod --stagenet` and one `monero-wallet-rpc --stagenet` per signer (each with its own wallet
   file; `set enable-multisig-experimental 1`). Script it under `tools/foreign/monero/` with no paths from any
   machine (take a `$DATA` env).
2. Key ceremony: `prepare_multisig` → exchange → `make_multisig(threshold k)` → `exchange_multisig_keys` rounds
   until `is_multisig.ready`; record the multisig address and the **private view key** (`query_key view_key`),
   which the contract owner publishes (`pub_view`, backlog). Write the ceremony as a runbook with the exact
   RPC calls and the verification each signer performs (address equality across signers).
3. `Address(j)`: `create_address`/`get_address` on account 0, subaddress index `j` (deterministic; every signer
   derives the same string). Attest via the existing `Attest` vote.
4. `Deposits()`: `get_transfers {in: true, pool: false}` filtered by `subaddr_indices`; map to `Deposit{TxID,
   Index, Amount, Height}`; `unlock_time` must be 0 (refuse locked outputs); `Finalized()` = tip − 10.
5. `Reserve()`: `get_balance` (account 0) total, minus the amounts of intents in state `paying`/`paid` whose tx
   is not yet confirmed; exact integers.
6. `Release(j, dest, amount)`: idempotency first — look up a transfer with `payment_id` or tx note `dmz:j=<j>`
   (`get_transfers {out: true}` + `get_tx_notes`); if present return its txid. Otherwise: the proposer (signer
   `j mod n`, fallback to the next after 40 blocks) builds `transfer {destinations, priority, do_not_relay:
   true, get_tx_hex/metadata}` on the multisig wallet, distributes `multisig_txset` (through the contract? no:
   keep it off chain, a small authenticated relay between signers, or a shared directory in the pilot), each
   signer `sign_multisig`, the last `submit_multisig`; returns the txid. Store the note `dmz:j=<j>`.
7. Tests: a stagenet integration test behind a build tag (`-tags stagenet`) that runs the whole deposit → mint →
   burn → release loop with 2-of-3 wallet-rpc processes; unit tests with a recorded wallet-rpc fixture for the
   parsing; the existing crash-injection test in `exactly_once_test.go` extended with a backend whose `Release`
   fails after broadcasting (the idempotent lookup must find the payment).
8. Known limits to write into the trust statement: multisig is experimental upstream; the 2021 multisig
   vulnerabilities; the FCMP++ migration will require a new vault (funds move to a new multisig).

**As built (2026-11-09).** `tools/vaultsigner/monerovault` implements the wallet-rpc client and the backend: per-index
subaddresses, `get_transfers` deposits grouped by `(txid, subaddress)`, `get_balance` reserve, a payment-id idempotency
key for each withdrawal, `get_transfer_by_txid` verification at the tip less the finality policy, and a
`get_address`/`create_address` address path. Reads retry transport failures and 429/5xx; sends are single-attempt and
are looked up before they are repeated. It requires the vault's primary address in the configuration and refuses a
wallet that does not hold it. A wallet that returns a `multisig_txset` is refused with `ErrNeedsSignatures`: this
build has no Monero txset coordinator, so it will not record or certify a multisig payment. The daemon's DERO reads
also retry and return errors instead of exiting, so a node restart does not retire a signer.

`-monero-config` is JSON: `network` (`mainnet`, `stagenet` or `testnet`), `walletRpc`, the vault's primary `address`,
optional `account`, `finalityDepth` (default and minimum 10), `timeoutSeconds` (default 300), `retries` (default 4)
and `retryDelayMs` (default 500). The wallet-rpc login, when `--rpc-login` is set, comes from
`VEIL_MONERO_RPC_LOGIN` as `user:password`; it is never put in the URL. `-network simulator` and `-network testnet`
select Monero stagenet, and `-network mainnet` selects Monero mainnet. The wallet's primary address is checked at
startup, and a subaddress at an index must have a subaddress prefix; the wallet itself remains trusted for the keys.

### 5.4 Zcash backend, as built (transparent P2SH k-of-n, 2026-09-15)

`vaultsigner -backend zcash -zcash-config <file> -coord <coordination config>`; code in `tools/vaultsigner/zcashvault`
(the backend) and `tools/zcashtx` (transactions, ZIP 244, ZIP 317, the node client), wired by
`tools/vaultsigner/zcash_backend.go`; ceremony and audit tool `tools/zcashkey`. Evidence:
`research/regtest/vault-zcash-e2e.log`.

1. **Keys.** `zcashkey -new` writes a 32-byte seed, owner-only (a protected DACL on Windows), refusing any path
   inside a git work tree; it prints only the account key `m/44'/coin'/0'` (coin 133 mainnet, 1 test networks).
   Vault address j is `account/branch/0/j` under every signer's key, the change address `account/branch/1/0`, with
   `branch` the vault scid's top 31 bits, so two vaults never share an address. BIP 32 is checked against its test
   vectors 1–5; derivation is decred secp256k1, the module's existing dependency.
2. **Descriptor.** Network, k and the sorted account keys; `NewDescriptor` refuses k ≥ n, k < 2, more than 15 keys,
   an xprv, a key of the other network, and a repeated key (one signer would fill two CHECKMULTISIG slots). Its text
   (366 characters for 2-of-3) is published with `SetViewKey`; `zcashkey -audit` sums the reserve from it.
3. **Nodes.** One or more zebrad (or zcashd) JSON-RPC nodes. Reads retry transport failures and 429/5xx a bounded
   number of times (`nodeRetries`, default 4; `nodeRetryDelayMs`, default 500); a broadcast is not retried by the
   client. The tip is the lowest any node reports; the finalized
   height is that tip less 100, and every node must name the same block there, or nothing is read. `Deposits` are
   every output ever paid to an attested index address (spent or not — the vault may spend a deposit before minting
   it) in blocks at or below the finalized height, reported identically by every node; an index whose attested
   address is not the derived one is left out and named. `Verify` re-reads the transaction from every node. `Reserve`
   is the lowest total any node shows (a lying node can cause a halt vote, never hide a shortfall).
4. **Release, the same transaction from every signer.** v5; `nLockTime = j` with every input final (a marker, found
   again on the chain after a lost ledger); expiry `epoch + 100 + 10 + 40`, where the epoch is the finalized height
   rounded down to 10 blocks; input 0 is the **anchor** — the unspent change output of the vault's previous release,
   or, before the first, the oldest reserve output — then the reserve's other finalized outputs largest first (an
   output worth less than its own ZIP 317 cost is never selected; at most 200 inputs); output 0 pays the destination
   `w_<j>_v` less ZIP 317's fee for the transaction with maximum-size signatures, output 1 returns the rest to the
   change address (at least 0.0001 ZEC, so the change chain never ends). Because every release spends the change
   chain's head, any two releases conflict directly or through the chain: **Zcash consensus**, not this code, refuses
   the second payment.
5. **Signing.** Each signer builds the transaction itself, checks it against the contract's withdrawal
   (`checkRelease`: destination, amount less the fee, the exact fee, change to the vault, the marker, final
   sequences, every input a vault address), records it in its release ledger (fsync, rename) **before** signing, signs
   its inputs (RFC 6979, low-S), and publishes the signatures in the round `zcashvault/<vault>/<j>/<txid>`. A signature
   counts only if it verifies under the named signer's key against this signer's own digest; with k, any signer
   assembles, re-checks every input as CHECKMULTISIG would, records the signed transaction, publishes it in
   `…/<j>/final` and broadcasts to every node. A signer signs a different transaction for j only when the earlier one
   provably cannot be mined (its expiry is final, or an input is spent by a final transaction) or the two share an
   input directly or through the change chain.
6. **VerifyRelease** certifies only a transaction every node shows mined in the same block at or below the finalized
   height, with k valid signatures on every input and `checkRelease` holding.
7. **Tests.** `go test ./zcashtx ./vaultsigner/...`: the ZIP 244 vectors and zebrad-captured v5/v6 transactions;
   BIP 32 vectors 1–5; descriptor, redeem-script and CHECKMULTISIG checks; construction determinism, selection and
   every signing refusal; 2-of-3 releases, an offline signer, forged and replayed signatures, a lost ledger, a lost
   broadcast, an expired release, a replacement through the change chain, a restart after signing, lying and dead
   nodes, and deposits that must not be reported — on an in-memory chain that validates every spend with its own
   script check. The live run is `TestRegtestEndToEnd` (`ZCASHVAULT_REGTEST=1`, zebrad regtest plus the DERO
   simulator, three daemons).
8. **Still open before real funds:** an external review of `tools/vaultsigner/zcashvault` and `tools/zcashtx`; the
   signer ceremony on separate machines (`docs/SIGNER_RUNBOOK.md` §3–§4); a pilot on the Zcash testnet through
   `-coord` with at least two nodes per signer; the reserve's consolidation (a withdrawal needing more than 200 inputs
   waits; nothing yet merges small outputs); refunding a deposit below `mindep` (transparent senders are visible, so
   `Refunded` could return it, but no daemon does); and the header gate (`tools/zecspv`) is not used, so the nodes are
   trusted for work.

The Orchard + FROST plan it replaces for launch is kept as the upgrade path: a Rust scanning sidecar with the
published full viewing key, FROST participants with one share each (reddsa `frost` + rerandomized, ZIP 312), a DKG
ceremony, and `Release` with a `dmz:j=<j>` memo. Re-randomized FROST is outside the NCC audit and wallet integration
of FROST is young; that is why it waits.

### 5.5 Daemon and contract work that goes with the backends

- Daemon: a read-only health endpoint; structured logs; metrics counters; a stale-snapshot guard (re-read the
  contract if a pass takes longer than a block); vote refusal budget (stop re-voting after N refusals and
  alert); the proposer election for foreign payments. `Finalized`, `Verify`, `ValidDest`, `VerifyRelease` and
  `Health` are already in the `Backend` interface (§5.2); what is missing is a real chain behind them.
- Contract: `Veto(j)` (k votes cancel a withdrawal and refund the burn), `Refunded(txid, vout, ftx)` (k votes
  record on DERO that a deposit was sent back on the foreign chain, closing its id), the `feepool` payout
  (`ClaimFees`, treasury only), signer replacement on k votes (`AddSigner2`, which bumps the slash epoch so
  every pending vote is voided; `Unbond` returns the bond of a candidate who was never voted in) and
  viewing-key publication (`SetViewKey`) have all shipped in v3. Still open (new template, tests first):
  `RequestDeposit` intents with expiry so a deposit is attributed to exactly one account (DESIGN §C2),
  encrypted destinations (S19), and `Compensate` (W6: a slashed bond goes to the treasury, so victims are never
  made whole). Each is a backlog item with its own test file — but `vault.bas` is 22,196 bytes on disk and
  installs (canonicalised by `tools/simvault`) for 16,392 of the 20,000 storage gas one transaction may spend
  (measured 2026-09-15, `research/regtest/vault-zcash-e2e.log`), so an addition has about 3,600 units of headroom.
  Measure again after any change; past the limit the wrapper splits into two contracts.
- Formal model: update `research/wrapper/formal/` (TLA+) for every new vote family; run TLC.

### 5.6 The standard for "flawless"

A backend is done when all of these hold, and the loop record shows the evidence:

1. Unit tests with recorded fixtures for every RPC parse; property tests for unit conversion (never a float).
2. Crash injection at every step of `processWithdrawal` with the real backend's idempotency (payments exactly
   once, votes at least once).
3. Adversarial cases: deposit below `mindep`, above the cap, over the rolling mint window, to an unknown index,
   replaced by a reorg below policy depth, sent twice with the same txid, partial payments, a withdrawal whose
   `dest` is invalid on the foreign chain (must be vetoed/refunded, never lost), a proposer that disappears
   mid-signing. There is no per-deposit maximum to test: one that a deposit exceeded could never be minted at
   all and nobody is guaranteed to exist who could send it back, which is rule 0's definition of a defect.
4. An end-to-end run on the real test network with two signers and one observer, logged like
   `.sim/vault_e2e.log`, kept under `research/wrapper/evidence/` (no machine paths).
5. The reserve invariant `reserve ≥ sup − inflight` (inflight = every burned withdrawal the contract has not
   closed) checked continuously by every signer and by an independent script using only the published viewing
   key. Not `reserve ≥ sup`: that halts on every honest withdrawal, and since `Released` refuses while halted
   the halt can never clear. It froze the vault on its first withdrawal until it was fixed on 2026-09-07.
6. Runbooks updated (`research/wrapper/RUNBOOKS.md`): ceremony, rotation, halt/resume, shortfall, refund.
7. The trust statement updated and still true; the marketing copy still forbidden words.
8. A pilot with a low cap on testnet for at least a week of real deposits and withdrawals before any mainnet
   discussion, then the mainnet gates in `deploy/README.md`.

### 5.7 What may never be said

"Trustless", "non-custodial", "decentralised bridge", "fully backed" (until viewing keys are published and the
reserve is auditable), "insured", "guaranteed". The wrapped units are a claim on a bonded federation.

## 6. If the decision is to wrap up instead

1. Confirm green (§3) at the head commit; append a final loop record with the state and the open items.
2. Choose a licence (none exists; the README says so) and add `LICENSE`; add `SECURITY.md` with a disclosure
   address the operator owns; add a `CONTRIBUTING` section or file mirroring README §12.
3. Consider publishing from a squashed first commit: the history carries ~100 MB of deleted screenshots.
4. Re-run `git grep -i` for personal names, paths, emails and hostnames (loop 16 scrubbed the tree; the check
   must be repeated after every loop that adds logs or records).
5. Rename identifiers to DMZ in one pass with a localStorage migration, or state in the README that they stay.
6. Refresh the testnet bundle (`genconfig -dist -network testnet`, `deploy/testnet-tela.sh -index … -reuse …`)
   so the on-chain app matches the last commit, and record the INDEX in `deploy/tela-testnet.json`.
7. Hand the operator: the head commit id, the list of untracked local state they hold (wallets, seeds, vendor
   binaries), and this file.

## 7. Glossary (repository-specific words)

- **publish**: the `publish` argument of a swap; when 1 the market contract writes `t_<n>` naming the trader.
- **`…To` entrypoints**: `SwapDeroForAssetTo`, `SwapAssetForDeroTo` — pay a named address, work at any ring, never
  publish, refund-and-commit on failure.
- **probe**: the app's post-transaction check that a contract key changed (with an optional caller-specific
  `check`), or that a wallet balance moved, before saying "confirmed".
- **era**: a rollover of a reward accumulator (`era`/`ea_`, `sera`/`sea_`) when it would wrap 2^64.
- **`fc`, `dep`**: monotone contract counters the bots use as execution proof. A vault vote is counted in
  `<f>c_<scope>_<choice>` and attributed in `<f>v_<scope>_<address>`; the signer reads the second one back,
  because a co-signer can move the first in the same block.
- **legacy hash**: an earlier template sha256 the app still accepts; contracts installed from it stay verified.
- **minhst**: registered Founders units required before undistributed DERO is released.
- **stable height**: `DERO.GetHeight.stableheight`, the finality anchor for paying a burn.
- **R-n / B1.n / Cn / Sn**: ids in `RISKS.md`, `docs/PLATFORM_BACKLOG.md`, and the earlier backlog in
  `docs/history/HISTORY.md`.
