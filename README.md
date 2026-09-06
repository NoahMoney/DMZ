# DMZ — a social DEX, token launchpad and NFT platform for DERO, served from the chain

DMZ (dURL `dmz.tela`) is a mobile-first web app whose files live on the DERO blockchain (TELA) and whose only
server is the user's own DERO node. It lets anyone launch a token on a bonding curve that graduates into a
permanently locked AMM, swap any DERO asset against DERO, earn holder NFTs, share protocol fees through a
Founders Pass, and (research stage) trade wrapped XMR/ZEC through a bonded k-of-n federation. Balances stay
encrypted, trades are private by default, and every number the app shows is either read from contract state or
labelled as what it cannot know.

> **Status (2026-09-06):** experimental, unaudited, simulator- and private-testnet-tested. Nothing has touched
> mainnet. The internal code name was *Veil*; the product name is now **DMZ** — user-visible strings say DMZ,
> code identifiers (`window.Veil`, `VEIL_CONFIG`, `veil.*` storage keys, Go module `veil`) still say Veil and
> will be renamed in one deliberate pass (backlog). No licence has been chosen yet: all rights reserved until the
> operator decides. Read [RISKS.md](RISKS.md) (R-9 legal, R-10 balance decryption, R-14 federation custody)
> before any public deployment.

---

## Table of contents

1. [What it does](#1-what-it-does)
2. [How it is built](#2-how-it-is-built)
3. [The privacy and security model](#3-the-privacy-and-security-model)
4. [Repository layout](#4-repository-layout)
5. [Build and run](#5-build-and-run)
6. [Tests and verification](#6-tests-and-verification)
7. [Deploying](#7-deploying)
8. [The wrapper (wXMR / wZEC)](#8-the-wrapper-wxmr--wzec)
9. [Economics](#9-economics)
10. [Current state, known issues, backlog](#10-current-state-known-issues-backlog)
11. [Documentation map](#11-documentation-map)
12. [Contributing and house rules](#12-contributing-and-house-rules)

---

## 1. What it does

| Pillar | Today |
|---|---|
| **Launch** | One form: name, ticker, description, icon URL, supply, decimals, creator allocation (≤ 5 %), graduation target, tax mode. The token is a DERO asset with a fixed supply; 80 % of the remainder is sold on a virtual constant-product curve with a 16× price range and graduates atomically into a contract-owned AMM at the same price. No mint, no burn, no admin key, no metadata setter (`contracts/launch.bas`). |
| **Trade** | Buy/sell on launches and pools with an exact BigInt quote that mirrors the contract maths, slippage presets under a hard 10 % cap, a 1 DERO fee cap, and *transaction outcome proofs*: the app says "confirmed" only when the wallet balance or a contract counter proves the call executed; an included-but-reverted call is reported as such. |
| **Private mode** | Every contract pays the caller by `SIGNER()`, which only exists at ring size 2 and is public. The `…To` entrypoints pay a named address instead, so the wallet can send the call at ring size 16: the payer is one of sixteen accounts, the receiver is public. |
| **Discover** | Markets tab with filters (trending, new, gainers, losers, graduating, graduated, pools, watchlist), six sorts, search, per-device watchlist; home hero, movers, new launches, live feed, price tape; block-resolution candle charts (pure SVG, no dependency) with reserve-flow volume, crosshair, follower markers. |
| **NFTs** | Holder NFTs: lock ≥ N tokens for M blocks, mint one edition (one per address for the life of the contract). The Founders Pass: 500 editions whose registered holders receive 50 % of protocol fees, pushed to their wallets by a treasury bot, with a built-in fixed-price marketplace. |
| **Social** | Opt-in trade publishing (the market contract writes the record, nobody can fake it), profiles and a follow graph on a tiny contract, a feed with All / Following / Watchlist. |
| **Portfolio** | Holdings, average cost, realised and unrealised P&L and an equity curve, computed on the device from local records and the address's own published trades. Nothing leaves the browser. |
| **Wrapper (research)** | `contracts/vault.bas` v2: a bonded k-of-n federation mints wrapped units against foreign deposits and pays burns, with per-deposit and windowed limits, a withdrawal delay, halt and slash votes keyed by epochs, and a signer daemon that proves every vote by the contract's own counters. Foreign backends (Monero, Zcash, Solana) are not implemented; only the simulated backend runs. |

## 2. How it is built

```
 DERO node (your own)  ──WebSocket JSON-RPC──▶  app/rpc.js ─┐
                                                            ├─▶ app/feed.js (data layer: verify code hash,
 Wallet (Engram / civilware ─XSWD ws://localhost:44326─▶ app/xswd.js ┘    treasury, fees; read keys in ranges;
 fork with XSWD)                                                          bounded localStorage cache, re-verified)
                                                                            │
                     app/stats.js (metrics)  app/amm.js (exact quotes)  app/chart.js (SVG)  app/*.js screens
                                                                            │
 contracts/*.bas (DVM-BASIC, immutable, no UpdateCode)  ◀── app/app.js builds every transaction; the wallet signs
```

- **Contracts** (`contracts/`, DVM-BASIC): `launch.bas` (curve → AMM, taxes, staking), `pool.bas` (constant-product
  AMM with LP tokens), `factory.bas` (registry + fee), `social.bas` (profiles, follows), `nft.bas` (holder NFTs),
  `founders.bas` (fee-sharing pass + marketplace), `swap.bas` (HTLC escrow), `board.bas` (order board),
  `vault.bas` (federation wrapper). All arithmetic is `uint64`; overflow paths are tested. Every formula has a Go
  model in `reference/` checked against `math/big`, and `tests/` drives derohe's own DVM executor.
- **App** (`app/`): 19 plain-JS files, 4 CSS files, one HTML file, **no framework, no bundler, no external
  dependency**. 238 KB raw, ~72 KB gzipped. Served from a TELA INDEX (28 DOCs on chain) or from any static host.
  Strict CSP (`default-src 'none'; script-src 'self'`), loopback or `wss://` nodes only, a remote node needs a
  per-session confirmation.
- **Tools** (`tools/`, Go): simulator drivers (`simdeploy`, `simlaunch`, `simsmoke`, `simtraffic`, `simnft`,
  `simvault`), the treasury bot, the wrapper signer daemon, a headless wallet (`netwallet`), an auto-approving
  test wallet (`xswdwallet`), the TELA installer (`telainstall`), per-network config (`genconfig`, `netcfg`),
  a private-testnet genesis tool, vendor verification (`verifyvendor`) and Playwright UI tests (`uitest`).
- **Verification of what is listed**: a contract is shown only if its on-chain code hashes to a shipped template
  (`app/templates/*.bas`, hashes in `app/config.js`, older versions kept as `legacy`), its treasury equals the
  factory's, and its fee parameters equal the protocol's. The registry is a paid hint, not a trust anchor.

## 3. The privacy and security model

What DERO gives, what DMZ adds, and what neither can do. Longer form: [DECISIONS.md](DECISIONS.md) §10,
[research/RING_PRIVACY.md](research/RING_PRIVACY.md), [research/wrapper/README.md](research/wrapper/README.md).

- **Balances are encrypted.** Holder counts, holder lists and "Dev %" do not exist. The app shows "Public
  traders" (distinct addresses that chose to publish) and says so; it never ranks on a number that address minting
  can inflate.
- **`SIGNER()` is real only at ring size 2**, and the node's `Extract_signer` makes that signer public. Ordinary
  trades therefore reveal the trader to anyone reading the chain; private mode (`…To` entrypoints, ring 16) hides
  the payer and names the receiver. A reverting call above ring 2 loses its deposit, so the `…To` wrappers refund
  and commit instead of reverting. A private trade never publishes a record.
- **Publishing is opt-in** and written by the market contract itself: a feed entry cannot be forged. Unpublished
  trades are visible only as per-block reserve movements, which is what the volume and candle data are built
  from, exactly and honestly labelled.
- **No server, no analytics, no cookies.** Reads go to the node the user chose (loopback, the wallet's node, or a
  confirmed `wss://` endpoint). Images are off by default because loading a creator's URL reveals the viewer's IP.
- **The wallet signs; the app never sees keys.** Every transaction is built in the app, shown by the wallet, and
  sent only on approval. Local sanity gates: verified listing only, slippage ≤ 10 %, network fee ≤ 1 DERO, one
  transaction in flight, unanswered transfers block new ones and are reported when the wallet answers late.
- **Contracts are immutable** (no `UpdateCode`), `Initialize` is guarded, and every state-changing path is
  fuzzed against a model. The wrapper's trust is stated once and never called trustless: k of n signers can steal
  the reserve; bonds deter a minority, not a colluding quorum (n ≥ 2k is needed for honest signers to slash).
- **Off-chain daemons prove their actions by counters**, never by transaction inclusion: the signer remembers a
  vote only when the contract's count key grew; the treasury bot records a claim only when `fc` grew and closes
  a deposit only when `dep` grew.

## 4. Repository layout

```
contracts/        the nine DVM-BASIC contracts + README.md (every entrypoint, key, invariant, size and gas)
reference/        Go executable specification of every formula (launch.go, pool.go, uint128.go)
tests/            DVM-level tests + fuzz against the models; run.sh / run.ps1 copy them into vendor/derohe
app/              the TELA app (index.html, *.css, *.js, config.js, templates/*.bas)
tools/            Go tools (see §2); tools/uitest/*.mjs are the Playwright passes
deploy/           README.md (simulator, private testnet, mainnet and TELA runbooks in one file), networks.json
                  (per-network SCIDs + template hashes), vendor.lock, binaries.lock, patches/, testnet-*.sh,
                  tela-*.json records
research/         RING_PRIVACY.md (measured ring sizes, the private-mode design)
research/crosschain/   README.md (what was resolved, decided, corrected, reviewed), RESEARCH.md (report,
                  feasibility, trust models, primitives, protocol sketches, specification), SOURCES.md, poc/
research/wrapper/ README.md (trust statement, security review, what may be said), DESIGN.md (mechanism review,
                  controls, pipeline specification), RUNBOOKS.md (operator runbooks), formal/ (TLA+ model)
docs/             ARCHITECTURE.md, CREATOR_GUIDE.md, PLATFORM_GAP_MATRIX.md, PLATFORM_BACKLOG.md, HANDOVER.md,
                  history/HISTORY.md (build brief, phase reports, day log, earlier backlog, every R&D loop record),
                  history/screenshots/
DECISIONS.md      every fact and choice with its primary source in the vendored code
RISKS.md          the risk register (R-1 … R-16)
```

Untracked by design: `vendor/` (upstream clones and built binaries; `deploy/vendor.lock` pins them),
`.sim/` (simulator state and logs), `.testnet/` and `.mainnet/` (wallets, seeds), `dist/` (generated bundles).

## 5. Build and run

Tested on Windows 10 with Go 1.26 and Git Bash; the tools are portable Go and the scripts are POSIX shell, so
Linux/macOS should work with path changes. Node ≥ 18 is needed only for the UI tests.

### 5.1 Vendor the upstream code (once)

Follow [deploy/README.md](deploy/README.md) (Simulator) §1: clone `deroproject/derohe`, `civilware/derohe` (XSWD
wallet) and `civilware/tela` into `vendor/`, build `simulator.exe`, `dero-wallet-cli-xswd.exe` and `tela-cli.exe`
into `vendor/bin/`. Then pin and verify:

```
cd tools && go run ./verifyvendor            # commits, patches, untracked/ignored files, binaries, tool sources
```

`deploy/vendor.lock` records the commits and the one patch (`derod` private genesis); `deploy/binaries.lock`
records sha256 of every binary and of each tool's source tree.

### 5.2 Run the simulator and the app

```
vendor/bin/simulator.exe --data-dir=%CD%\.sim\data --rpc-bind=127.0.0.1:20000 --http-address=127.0.0.1:8089
cd tools
go run ./simdeploy -contracts ../contracts -app ../app     # factory, social, founders; templates; app/config.js
go run ./serve -dir ../app -addr 127.0.0.1:8082           # or install on chain: deploy/README.md (TELA)
```

Open `http://127.0.0.1:8082/index.html`. Connect a wallet with XSWD enabled (Engram, or `dero-wallet-cli` from the
civilware fork, menu 16). For headless testing `tools/xswdwallet` is an auto-approving wallet that refuses
non-loopback and non-test daemons; never point it at real funds.

### 5.3 Populate a simulator

```
go run ./simlaunch -contracts ../contracts      # factory + launch + staking + social + nft flows
go run ./simtraffic -duration 40m               # random buys/sells from wallets 16–21
go run ./treasurybot -wallet 127.0.0.1:30002 -once   # claim fees → deposit the pass share → PayBatch
```

## 6. Tests and verification

| What | Command | Expect |
|---|---|---|
| Contract suite (DVM executor, fuzz, models) | `bash tests/run.sh` (or `tests\run.ps1`) | `ok … veil/tests` in ~390 s; the C5 lint refuses any `float` in money code |
| Go tools | `cd tools && go test ./vaultsigner ./treasurybot` | crash-injection and counter-proof tests green |
| Vendor tree | `cd tools && go run ./verifyvendor` | "vendor tree matches deploy/vendor.lock" |
| UI, read-only | `PW_DIR=<dir with playwright> node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html out/` | `UI TEST OK`; asserts CSP blocks inline script, every tab renders, desktop shell at 1280 px |
| UI, wallet flow | `… ui.mjs … --wallet --dest=<address>` | launch + register (probe: our scid landed), published buy, private buy at ring 16 with no record written, stake, pass mint + register |
| Wrapper end to end | `bash tools/simvault/e2e.sh <vault scid> [deposit]` after `simvault -install` | two signers mint with the counter proof, over-cap deposit reported, burn, finality wait, two delayed `Released` votes |

`tools/uitest/run.sh` runs both UI passes against the local simulator. Every loop record in
`docs/history/HISTORY.md` names the commands it ran and their timings.

## 7. Deploying

- **Simulator**: `tools/simdeploy` (above). `deploy/README.md (Simulator)` is the full runbook including manual RPC.
- **Testnet**: the public seed node did not answer, so a private testnet chain is mined locally with a patched
  `derod` (`deploy/patches/derod-private-genesis.patch`, `tools/genesistx`). Three wallets with three roles
  (miner never transacts, treasury rarely sends, deployer sends) because a wallet whose balance changes every
  block decodes for minutes per change. Runbook: [deploy/README.md](deploy/README.md) (Testnet); scripts
  `deploy/testnet-deploy.sh` and `deploy/testnet-tela.sh` (refuses while the deployer wallet RPC is running).
  Recorded SCIDs: `deploy/networks.json`; TELA record: `deploy/tela-testnet.json` (installed under the old dURL
  `veil.tela`; the next install uses `dmz.tela`).
- **Mainnet**: not done. [deploy/README.md](deploy/README.md) (Mainnet) lists the go/no-go gates, costs and rollback.
  Every fund-moving tool refuses mainnet without `-yes` (`simdeploy`, `telainstall`, `treasurybot`,
  `vaultsigner`) and checks the daemon's network against the flag.
- **TELA**: `tools/genconfig -dist -network <net>` builds `dist/<net>/` (config, templates, manifest with sha256
  and DOC sizes); `tools/telainstall` installs each file as a TELA DOC and the INDEX, reuses unchanged DOCs only
  after verifying their on-chain code hash, and checks that every install executed.

## 8. The wrapper (wXMR / wZEC)

Research and a working simulator prototype, not a product. Read in this order:
[research/crosschain/README.md](research/crosschain/README.md) (Resolved) (why atomic swaps alone cannot give instant
pooled liquidity, and what can), [research/wrapper/README.md](research/wrapper/README.md) (Trust statement)
(every trusted party, in the order they can hurt you), [research/wrapper/README.md](research/wrapper/README.md) (what the
contract enforces, attacks considered, what it cannot protect against), `contracts/README.md` §vault,
`tools/vaultsigner` (the signer daemon: durable release intents, counter-proven votes, reserve-shortfall and
outsized-withdrawal halts), and [research/wrapper/RUNBOOKS](research/wrapper/RUNBOOKS.md ). Foreign-chain backends
are stubs; the network table (confirmations, RPC ports) is in the daemon.

## 9. Economics

- 0.5 % on every trade (`fee_bps = 50`, hard cap 1 %). Pools: half stays in the reserves for LPs, half accrues to
  the protocol treasury. Launches: the whole fee to the treasury on the curve; after graduation half stays in
  the locked reserves as price support.
- Creator tax ≤ 10 % of the DERO leg: to the creator, buyback-and-lock (capped at 1 % of the reserve per call,
  20 blocks apart), or pro-rata to stakers.
- Registration fee (0.1 DERO) to the treasury; Founders Pass mint proceeds to the treasury; NFT mint fee bps;
  slashed wrapper bonds to the treasury. 50 % of the DERO-leg protocol fees is promised to registered Founders
  Pass holders and delivered by `tools/treasurybot` (journaled, counter-proven, dry-run on mainnet without `-yes`).
- No paid promotion exists. If it is ever built it lives on a separate labelled shelf and never touches ranking
  (`docs/PLATFORM_BACKLOG.md` B2.3).

## 10. Current state, known issues, backlog

- **Done and verified on the simulator** (with tests): everything in §1, the wrapper prototype, the treasury bot,
  the private testnet deployment of the registry, social and pass contracts, and the TELA install of the app.
- **Known limits** (details in `RISKS.md` and `docs/PLATFORM_GAP_MATRIX.md` §4–5): the social contract is read
  with a full variable dump (scale limit R-12); images are creator-hosted URLs without content hashes; the
  Founders marketplace UI reads the last 300 listings; the launch form is one sheet, not a wizard; charts have
  no zoom/pan; search is substring only; ranking can be gamed by dust trades (ranking v2 is the next backlog
  item); no comments, alerts, share cards or collection pages yet.
- **Backlog**: [docs/PLATFORM_BACKLOG.md](docs/PLATFORM_BACKLOG.md) (prioritised, with done criteria) built from
  the evidence-backed inventory in [docs/PLATFORM_GAP_MATRIX.md](docs/PLATFORM_GAP_MATRIX.md).
- **How work is recorded**: one record per ~20-minute R&D loop in [docs/history/HISTORY.md](docs/history/HISTORY.md)
  (what changed, what was learned, security, unconsidered items, next), the day log in `docs/history/HISTORY.md`.

## 11. Documentation map

| Read this | For |
|---|---|
| [DECISIONS.md](DECISIONS.md) | Every DVM/daemon/wallet/XSWD/TELA fact with its source line, and the design decisions |
| [contracts/README.md](contracts/README.md) | Entrypoints, keys, maths, invariants, sizes, ring rules, private entrypoints |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | The app's data flow, incremental sync, what is deliberately not built |
| [docs/CREATOR_GUIDE.md](docs/CREATOR_GUIDE.md) | Launching a token, what the curve, taxes, NFTs and the pass do |
| [RISKS.md](RISKS.md) | The risk register, including regulatory notes and DERO-specific pitfalls |
| [research/wrapper/README.md](research/wrapper/README.md), [DESIGN.md](research/wrapper/DESIGN.md), [RUNBOOKS.md](research/wrapper/RUNBOOKS.md) | The federation wrapper: trust statement, security review, mechanism, controls, runbooks |
| [research/RING_PRIVACY.md](research/RING_PRIVACY.md) | Measured ring sizes, what the node reveals, the private-mode design |
| [deploy/README.md](deploy/README.md) | Simulator, private testnet, mainnet and TELA runbooks; locks and records |
| [docs/PLATFORM_GAP_MATRIX.md](docs/PLATFORM_GAP_MATRIX.md) | 181-capability inventory with evidence, competitor benchmark, security surface |
| [docs/PLATFORM_BACKLOG.md](docs/PLATFORM_BACKLOG.md) | The prioritised plan |
| [docs/HANDOVER.md](docs/HANDOVER.md) | How to continue or wrap up the project (for the next agent or engineer) |
| [docs/history/HISTORY.md](docs/history/HISTORY.md) | The full R&D history, loop by loop |

## 12. Contributing and house rules

- **Money is `uint64`.** No floating point in contracts, models, harness or bots (the suite's C5 lint fails on
  `float32|float64|Float64`). Every formula lands in `reference/` first, then in the contract, then in a test that
  compares both.
- **Contracts are immutable; templates are hashed.** Any contract change is a new template hash: regenerate with
  `simdeploy` (keeping the old hashes as `legacy`), redeploy the simulator, rerun both UI passes. Never edit
  `app/templates/*.bas` by hand. `.gitattributes` forces LF because the hash is of the exact bytes.
- **Never claim trustless.** Name every trusted party. Label every metric: exact, provable, estimated, unavailable.
- **Inclusion is not execution.** Anything off-chain that remembers an action must prove it by a contract counter.
- **Simulator → testnet → mainnet**, in that order, with the runbooks. No mainnet transaction without an operator
  and `-yes`.
- **Every change ships with its test and its doc line** (`contracts/README.md`, `RISKS.md` or the relevant runbook),
  and a loop record in `docs/history/HISTORY.md`.
- Commit messages describe behaviour, not files. Every commit on `main` passed the checks above.

Questions and findings: open an issue with the loop record or the file:line the finding is about. Security
findings about the wrapper: see `research/wrapper/README.md` §open items first.
