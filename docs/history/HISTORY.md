# Project history: build brief, phase reports, day log, every R&D loop, the earlier backlog

Kept whole as evidence: what was built when, what was measured, what was learned. Nothing here is a plan; the plan is `docs/PLATFORM_BACKLOG.md`. Screenshots from the loops are in `docs/history/screenshots/`.


---

# The build brief (the original prompt, 2026-09-05)

## BUILD PROMPT — "Veil": a fomo-style social DEX on DERO, served via TELA

> Paste everything below this line into Claude Fable 5.1 (Claude Code or Cowork recommended — this is a multi-file, multi-phase build with a real toolchain). Replace the `{{...}}` placeholders first.

---

### 0. Who you are and how to work

You are a senior blockchain + frontend engineer building a production-quality decentralised application on the DERO blockchain, hosted through the TELA decentralised web standard. You have web access and a Linux container. You will build this in phases, verifying every DERO/TELA fact against primary sources **before** writing code that depends on it. Do not work from memory on DERO specifics — the ecosystem is small, the docs are sparse, and your training data on it is likely stale or wrong.

Operating rules:

1. **Docs first, always.** Before each phase, fetch and read the primary sources listed in §1 for that phase. Quote the exact function signature, RPC method name, JSON shape, or contract header you are relying on, with the URL, in a `DECISIONS.md` file. If a source contradicts your assumption, the source wins.
2. **Verify against source code, not blog posts.** The DVM-BASIC built-in function list, XSWD method names, and RPC parameter names must be confirmed from the `deroproject/derohe` repository. Medium articles and community sites are context only.
3. **Simulator before testnet before mainnet.** Nothing touches mainnet in this build. All contract work is validated on the derohe local simulator, then testnet.
4. **uint64 discipline.** DVM-BASIC has two types: `Uint64` and `String`. No floats, no signed ints, no arrays. Every arithmetic line in a contract must be annotated with its overflow bound. Treat this as the single highest-risk area of the project.
5. **Ask before inventing.** If a DERO capability you need doesn't exist (an oracle, a bridge, an event log), stop and say so. Do not fake it with an off-chain server and call it decentralised.
6. **Write DECISIONS.md and RISKS.md as you go.** Every non-obvious choice gets a dated entry with the source that justified it.
7. **No brand assets from fomo.** The product should share fomo's *design language* (see §5) but must not use its name, logo, wordmark, icon set, colour hex values copied from its app, or any of its screenshots. Product name for this build: `{{PRODUCT_NAME}}` (default: "Veil").

---

### 1. Primary sources (fetch these; do not substitute)

**DERO core**
- DERO docs root: https://docs.dero.io/
- DVM overview: https://docs.dero.io/Developers/dvm/
- DVM docs (repo): https://github.com/deroproject/documentation/tree/master/DVMDOCS
- derohe source (canonical): https://github.com/deroproject/derohe
  - DVM built-in functions: look in `dvm/` (the functions file — confirm exact filename in the repo) and extract the full list of built-ins with signatures.
  - Daemon RPC: `rpc/` package — confirm method names such as `DERO.GetSC`, `DERO.GetTransaction`, `DERO.GetHeight`, `DERO.GetBlock`, `DERO.GetInfo`, `DERO.GetRandomAddress`, `DERO.GetEncryptedBalance`.
  - Wallet RPC: `walletapi/rpcserver/` — confirm `transfer`, `scinvoke`, `GetBalance`, `GetAddress`, `GetTransfers`, and the `sc_rpc` argument structure (`rpc.Arguments` with `SC_ACTION`, `SC_ID`, `SC_CODE`, `entrypoint`, per-parameter `DataUint64`/`DataString`/`DataHash`).
  - XSWD protocol: `walletapi/xswd/` — confirm default port (expected 44326), the application-registration handshake (id, name, description, url, permissions), the permission model, JSON-RPC-over-WebSocket method names, and the `Subscribe` events (expected: `new_topoheight`, `new_balance`, `new_entry`).
  - Simulator: `cmd/simulator/` — confirm how to run a local chain with pre-funded wallets.
  - Asset model: confirm how a contract's own SCID acts as a native asset, how assets are sent *into* a contract (transfer with `scid` + `burn`), and how a contract reads incoming asset value (`ASSETVALUE(scid)`) vs DERO (`DEROVALUE()`).

**TELA**
- TELA standard: https://github.com/civilware/tela
- TELA-INDEX-1 (entrypoint/manifest): https://github.com/civilware/tela/blob/main/TELA-INDEX-1/README.md
- TELA-DOC-1 (immutable file contracts): https://github.com/civilware/tela/blob/main/TELA-DOC-1/README.md
- TELA-MOD-1 (variable stores etc.): https://github.com/civilware/tela/blob/main/TELA-MOD-1/README.md
- TELA-CLI: https://github.com/civilware/tela/blob/main/cmd/tela-cli/README.md
- Go package API: https://pkg.go.dev/github.com/civilware/tela
- ART-NFA header standard (TELA reuses it): https://github.com/civilware/artificer-nfa-standard/blob/main/Headers/README.md
- Engram wallet (compliant TELA host): https://github.com/DEROFDN/Engram
- Gnomon (DERO SC indexer, for feed/history): https://github.com/civilware/Gnomon — confirm it exists and its current API before depending on it.

Facts already confirmed from the TELA README (re-verify, they may have moved):
- TELA-INDEX-1 is the mutable entrypoint; TELA-DOC-1 contracts are immutable files (HTML, JS, CSS, JSON, Markdown supported). Multiple DOCs are embedded in one INDEX.
- Installing an INDEX with ringsize 2 makes it updatable (public owner); ringsize > 2 makes it immutable (anonymous).
- Updates embed new values in new code and store the update TXID in the contract — a commit history.
- TELA-MOD-1 enables per-wallet key/value stores on a public INDEX (keys prefixed `var_`), with view-only / overwrite / immutable modes.
- Built-in rating system: one rating per account per contract, 0–99, ringsize 2 required.
- Large files are split into "DocShards" (`CreateShardFiles` / `ConstructFromShards`).
- Host apps serve content locally (e.g. `localhost:8081/tela/`) from a connected node; there is no server.

---

### 2. What we are building (and what we are not)

#### The product
A **privacy-preserving social DEX for DERO-native assets** with a fomo-style mobile-first UI:
- Swap any DERO asset (SCID) ↔ DERO through on-chain AMM pools.
- Launch new tokens ("memecoins") on DERO through a bonding-curve launcher contract that auto-graduates into an AMM pool.
- An **opt-in** social layer: users can publish their trades, follow other addresses, and see a verified feed — verified because the feed is reconstructed from contract state, not from a database.
- Portfolio, P&L, watchlists, token pages with trade markers on the chart.
- Entire frontend stored on-chain as TELA-DOC-1 files under a TELA-INDEX-1, connecting to the user's own wallet via XSWD.

#### Explicitly out of scope (document in RISKS.md, do not build)
- **Tokenised equities / "stocks".** A tokenised share is a legal claim against a regulated issuer; none exists for DERO. Synthetic price-tracking assets would be derivatives and DERO has no native oracle. Leave a clearly-labelled placeholder in the architecture doc; no code.
- **Cross-chain memecoins (Solana/Base/etc.).** No production bridge to DERO. Do not build a custodial bridge.
- **Fiat / card on-ramp.** Out of scope.
- **Any centralised backend that holds keys, executes trades, or is required for the app to function.** An optional indexer (Gnomon or your own) is allowed *only* as a read cache the app can fall back from by reading the node directly.

---

### 3. Architecture

```
┌──────────────────────────────────────────────────────────┐
│  Browser (served locally by TELA host: Engram / tela-cli) │
│  index.html + app.js + styles.css + assets  ← TELA-DOC-1 │
│        │ XSWD (wss://localhost:44326?)                     │
│        ▼                                                  │
│  User's DERO wallet (Engram / dero-wallet-cli)            │
│        │ signs transfer / scinvoke                        │
│        ▼                                                  │
│  DERO node (user's own or configured remote)              │
│        │ DERO.GetSC / GetTransaction (read state)         │
│        ▼                                                  │
│  Contracts (DVM-BASIC)                                    │
│   • Factory      – registry of pools, creates Pool SCIDs  │
│   • Pool         – x*y=k AMM for one asset ↔ DERO         │
│   • Launcher     – bonding-curve token creation + grad.   │
│   • Social       – opt-in trade posts, follows, profiles  │
│   • TELA-INDEX-1 – app manifest (+ TELA-MOD-1 vars)       │
└──────────────────────────────────────────────────────────┘
```

Key design decisions to confirm and record:
- **One pool contract per pair** vs **one contract holding all pools in `MAPSTORE` keyed by SCID**. Evaluate gas (DVM charges per executed line), storage limits, and upgrade story. Default: one pool per pair, deployed by users via the Factory, because it isolates blast radius.
- **LP tokens** = the Pool contract's own SCID, minted via `SEND_ASSET_TO_ADDRESS(addr, amount, SCID())`. Confirm this pattern in derohe.
- **Ringsize.** `SIGNER()` only resolves the caller at ringsize 2. Every state-changing entrypoint that needs to know the caller must require ringsize 2; document what that means for privacy (the caller address is revealed to the contract, but *not* their balances). Swaps that don't need the caller (e.g. output goes back to `SIGNER()` anyway) still need it. Note this in the UI ("Trades reveal your address to the pool; balances stay private").
- **Reading state.** The frontend reads contract variables via daemon `DERO.GetSC` with `variables:true`. Design key names so the whole feed/pool state can be read in as few calls as possible (prefix schemes, counters, fixed-width keys).
- **Events.** DVM has no event log. Every action the UI needs to show must be *stored* (with a monotonic counter key) or *derivable from the TX*. Design this explicitly.

---

### 4. Contracts — specification

Write all contracts in DVM-BASIC, line-numbered, with `Initialize()` and typed entrypoints. Every entrypoint returns `Uint64` and returns `0` on success (non-zero reverts the TX — confirm semantics in derohe).

#### 4.1 Pool (AMM, asset ↔ DERO)

Storage (proposed; confirm naming limits):
- `asset` (String: SCID of the traded asset), `owner`, `fee_bps` (Uint64, default 30), `reserve_dero`, `reserve_asset`, `lp_supply`, `k_last`, `trade_count`, and per-trade records `t_<n>` for the social/verified feed (see 4.4).

Entrypoints:
- `AddLiquidity()` — reads `DEROVALUE()` and `ASSETVALUE(asset)`, enforces ratio within a tolerance passed as a parameter, mints LP.
- `RemoveLiquidity(lp_amount)` — burns LP received via `ASSETVALUE(SCID())`, returns proportional reserves.
- `SwapDeroForAsset(min_out)` and `SwapAssetForDero(min_out)` — constant product with fee, slippage check, sends output to `SIGNER()`.
- `Publish(flag)` — optional per-trade flag (see 4.4).

**Fixed-point math, mandatory analysis:**
- DERO atomic units are 1e5 (confirm); assets can declare their own decimals. All maths in atomic units.
- `x * y` where both are uint64 overflows at ~1.8e19. Reserves of 1e12 atomic each already overflow. You must implement **mulDiv with overflow-safe decomposition** (split into hi/lo halves, or bound reserves, or use a 128-bit emulation via two uint64s). Prove the bound for each formula line and write it as a comment on that line.
- Rounding must always favour the pool (round output down, LP mint down, LP burn up).
- Write a Go or Python reference model of every formula and fuzz it against the DVM output on the simulator (see §7).

#### 4.2 Factory
- `CreatePool(asset_scid)` — deploys nothing (DVM can't deploy contracts from contracts — confirm); instead the *user* installs a Pool contract and then calls `Register(pool_scid)` on the Factory. Factory verifies via `LOAD` on the pool that its `asset` matches and its code hash matches a whitelisted template hash (confirm whether cross-contract reads exist in DVM; if not, Factory only stores the mapping and the frontend verifies pool code with `tela.EqualSmartContracts`-style comparison client-side).
- Registry keys: `pool_<asset_scid>` → pool SCID, `pool_count`, `pool_idx_<n>`.

#### 4.3 Launcher (memecoin creation with bonding curve)
- `Create(name, ticker, decimals, supply, curve_params)` — the Launcher contract mints supply of a **new asset**. Confirm whether a DVM contract can mint an asset other than its own SCID (expected: no — a contract's SCID *is* its asset). If no, the launcher must be a **per-token contract template** the user installs (one contract = one token), with the Launcher registry pattern from 4.2.
- Bonding curve buy/sell in DERO with the same overflow discipline; a `graduate()` path that, once a DERO threshold is reached, seeds a Pool and burns/locks the curve.
- Anti-rug defaults: creator allocation cap, LP locked in-contract on graduation, no mint after creation.

#### 4.4 Social (opt-in, verified)
- Profile: `Profile(display_name, bio, avatar_ref)` stored under `p_<address>`.
- Follow/unfollow: `Follow(address)` → `f_<follower>_<followee>` (or a counter-indexed list; evaluate read cost).
- Trade posts: two options, implement the first, document the second:
  1. The Pool itself writes `t_<n>` = `<addr>|<side>|<in>|<out>|<height>` **only when the trader passed `publish=1`**. The feed is then literally the pool's state — no way to fake a trade. Non-publishers leave no record beyond the reserve change.
  2. A separate Social contract where users post a TXID; the frontend fetches the TX and proves it invoked the pool. Cheaper for the pool, more client work.
- Privacy statement in-app: publishing reveals address + trade; nothing else. Balances remain homomorphically encrypted.

#### 4.5 TELA-INDEX-1 + TELA-MOD-1
- Install with ringsize 2 so the app can be updated; record the update TXID chain in `DECISIONS.md`.
- Use TELA-MOD-1 variable stores for lightweight per-user settings (theme, hidden balances flag, watchlist) so they persist without a server. Confirm mod names and capabilities from the TELA-MOD-1 README.

---

### 5. Frontend — visual spec (fomo design language, original assets)

Build a **single-page, mobile-first web app** (vanilla JS or a small framework that compiles to plain files — every output file must be installable as a TELA-DOC-1; check size limits and shard large bundles). Desktop gets a centred ~430px phone-width column.

Target the *feel* of fomo:
- **Dark, near-black canvas** with high-contrast white type, one saturated accent for "up/buy" (green family) and one for "down/sell" (red/pink family). Choose your own hex values; do not lift them.
- **Bottom tab bar (5 tabs):** Home (feed + trending), Search, Trade (big central action), Portfolio, Profile.
- **Home:** horizontal "Trending" carousel of token cards (icon, ticker, price in DERO, 24h % pill, tiny sparkline), then a vertical **social feed** of trade cards: avatar, display name / short address, "bought 12.4K TICKER for 3.2 DERO", relative time, a follow button, and a tappable ticker chip that jumps to the token page.
- **Token page:** large price + % change, chart (lightweight, SVG or canvas, no heavy libs) with **trade markers** for followed accounts overlaid; tabs for "Trades", "Holders (public only)", "Info"; sticky full-width **Buy** / **Sell** buttons.
- **Trade sheet (bottom sheet):** amount input with preset chips (25/50/75/Max), live quote (output, price impact, fee, min received at slippage), slippage selector, a **"Post this trade"** toggle (default off, with the privacy note), and a single confirm button that hands off to the wallet via XSWD.
- **Portfolio:** total value in DERO, P&L card (realised/unrealised, computed client-side from the user's own published + unpublished trades stored locally), equity curve, holdings list.
- **Profile:** public stats (only from published trades), followers/following, a "hide balances" toggle.
- Micro-interactions: skeleton loaders, haptic-style button press scale, toast on TX submitted / confirmed (poll `DERO.GetTransaction`), confetti-free.
- Accessibility: 4.5:1 contrast minimum, focus states, reduced-motion respected.

State & data:
- All reads via daemon JSON-RPC; cache in-memory; optional indexer behind a feature flag.
- Wallet connection via XSWD: implement the handshake, permission requests, and a graceful "no wallet found" state with instructions for Engram.
- Never store private keys. Never proxy transactions through a server.

---

### 6. Deliverables (repo layout)

```
/contracts/pool.bas
/contracts/factory.bas
/contracts/launcher_token.bas
/contracts/social.bas          (only if option 2 in 4.4 is chosen)
/contracts/README.md           (every entrypoint, params, storage keys, ringsize requirements)
/reference/amm_model.py        (formula reference + fuzz harness)
/app/index.html
/app/app.js  /app/styles.css  /app/xswd.js  /app/rpc.js  /app/amm.js  /app/feed.js
/app/assets/*                  (original icons only)
/deploy/README.md (TELA)        (exact tela-cli / Go steps to shard, install DOCs, install INDEX, update)
/deploy/README.md (Simulator)           (run simulator, fund wallets, install contracts, invoke, read state)
/tests/                        (simulator scripts; see §7)
DECISIONS.md   RISKS.md   README.md
```

---

### 7. Test plan (must pass before you call anything done)

1. **Simulator smoke:** install Pool, add liquidity, swap both directions, remove liquidity; assert reserves and LP supply against `amm_model.py` to the atomic unit.
2. **Overflow fuzz:** 10k random reserve/amount pairs up to the documented bounds; DVM output == model output; any mismatch is a release blocker.
3. **Slippage / min_out:** confirm revert (non-zero return) when quote moves.
4. **Ringsize:** confirm behaviour when a ringsize > 2 TX calls an entrypoint that uses `SIGNER()`.
5. **Reentrancy / ordering:** DVM executes TXs sequentially per block; document why reentrancy is or isn't possible with cross-contract calls (confirm whether DVM supports calling other SCs at all).
6. **Feed integrity:** a published trade appears in `t_<n>`; an unpublished one does not; a forged post is impossible by construction.
7. **TELA round-trip:** install all DOCs + INDEX on simulator/testnet, serve with tela-cli, load in a browser, connect via XSWD, execute a swap end-to-end. Then push an INDEX update and verify the commit TXID chain.
8. **Bundle size:** report total on-chain bytes and the DERO fee to install and to update.

---

### 8. Phases and what to hand back at each

- **Phase 0 — Recon (no code):** fetch all §1 sources; produce `DECISIONS.md` v0 with the confirmed DVM built-in list, RPC/XSWD method table, TELA install procedure, asset/ringsize semantics, and a list of every assumption in this prompt that turned out to be wrong. Stop and show me.
- **Phase 1 — Pool + reference model + simulator tests.** Stop and show me the fuzz results.
- **Phase 2 — Factory + Launcher template.** Stop and show me.
- **Phase 3 — Frontend against simulator via XSWD**, static data first, then live.
- **Phase 4 — Social layer + TELA install/update procedure + RISKS.md** (include a plain-English section on regulatory exposure for an operator: this venue is non-custodial, no-KYC, and privacy-preserving — list what that implies and recommend legal review before any public deployment).

At every stop, include: what you verified, what you built, what you couldn't verify, and the exact next command I'd run.

---

### 9. Placeholders

- `{{PRODUCT_NAME}}`: Veil
- `{{NODE_ENDPOINT}}`: 127.0.0.1:20000 (simulator default — confirm)
- `{{WALLET_RPC}}`: 127.0.0.1:10103 (confirm)
- `{{XSWD_PORT}}`: 44326 (confirm)
- `{{FEE_BPS}}`: 30
- `{{GRADUATION_THRESHOLD_DERO}}`: 500

Begin with Phase 0.


---

# Phase reports (simulator, 2026-09-05)

## Phase reports

### Phase 0 — Recon (2026-09-05)

**Verified (from source, not memory):** DVM builtin table, costs and limits; commit/revert/refund semantics;
signer/ringsize; asset in/out and own-asset minting; absence of cross-contract calls; daemon + wallet RPC method
names and JSON shapes; GetSC encoding; GetGasEstimate; XSWD port, handshake, permission model, DERO.* proxying,
events, rate limit and which wallets ship it; TELA templates, size limits, MODs, serve path; simulator ports, wallets
and mining cadence. All in `DECISIONS.md` with file:line citations.

**Built:** `vendor/bin/simulator.exe` (official derohe), `tela-cli.exe` and `dero-wallet-cli-xswd.exe` (fork) —
the last two were still compiling when this report was written; see Phase 1 report for status.

**Could not verify:** Engram's exact menu path for enabling XSWD (source shows it is on by default at 44326 with
"ask" permissions); Gnomon's current HTTP API beyond the README (not needed).

**Assumptions corrected:** DECISIONS §8 (11 items). Biggest: XSWD lives in the civilware fork; no cross-contract
calls → launcher graduates into a built-in AMM; browser must read the node over WebSocket.

**Next command (Phase 1):**
```
cd <repo>\tests && go test ./... -run . -v
```

### Phase 1 — Pool + reference model + DVM tests (2026-09-05)

**Built:** `contracts/pool.bas` (11.4 KB), `reference/uint128.go` + `reference/pool.go` (Go executable spec),
`tests/harness/chain.go` (drives derohe's own `dvm.Execute_sc_function` / `SanityCheckExternalTransfers` /
`ErrorRevert` / `ProcessExternal` on an in-memory graviton store — the exact code path of
`blockchain.process_transaction_sc`), `tests/*_test.go`, `tests/run.sh|ps1`.

**Verified:**
- 128-bit helpers vs `math/big`: all edge triples + 600k random triples (`TestUint128*`).
- Pool vs model: scenarios (install validation, smoke, anonymous-caller rejection, slippage, stray assets,
  MIN_LIQ) and **10,000-op fuzz** (`VEIL_FUZZ_SEED=42`): every field (`rd ra lp fd fa tc pc lh lt`), every emitted
  transfer, every refund, the latest `t_<n>` and `p_<n>` strings, and the contract's own asset balances
  (= reserves + accrued fees) match after every op. A 200k-op run with seed 7 was started in the background.
- Gas: worst case seen SwapDeroForAsset 1.05M compute / 323 storage; AddLiquidity 1.69M — well under the 10M
  per-call limit. Install storage gas 11641 < 20000 cap.

**Two defects found and fixed by the tests** (kept in DECISIONS §1.1 / §10.2): the `LET` operator quirk, and a
ceiling overflow at q = 2^64−1 in `muldivup` (now panics → reverts instead of wrapping to 0).

**Could not verify yet:** the same flow on the real simulator over wallet RPC — tool written
(`tools/simsmoke`), run recorded below once complete.

**Next command:**
```
cd <repo>\tools && go run ./simsmoke -contracts ../contracts
```

**Simulator smoke (recorded run, 2026-09-05):** `tools/simsmoke` — install asset + pool (11 713 bytes, storage gas
11 970), AddLiquidity 2 DERO + 1 000 000 units, published buy (0.5 DERO → 199 199 units, `t_0` written with the
canonical `dero1…` address), unpublished sell, estimator rejects an over-tight `min_out`, RemoveLiquidity, ClaimFees
(125 DERO units + 50 asset units). Every reserve/fee field matched the model after every step.
`SIMULATOR SMOKE OK`. Two more defects found on the real chain and fixed: the balance-wrap guard must use the full
deposited amounts (200k-op fuzz), and `DERO.GetGasEstimate` needs `SC_ACTION`/`SC_ID`/`SC_CODE` supplied by the
caller (the wallet adds them only on `transfer`).

### Phase 2 — Factory + launch template (+ social, NFT) (2026-09-05)

**Built:** `contracts/launch.bas` (13.8 KB: curve → built-in AMM, 50 bps fee, tax modes 1/2/3, staking vault),
`contracts/factory.bas`, `contracts/social.bas`, `contracts/nft.bas`; `reference/launch.go`;
`tests/launch_test.go`, `tests/registry_test.go`, `tests/parse_test.go`; `contracts/README.md` (maths + proofs).

**Verified (DVM harness):** launch install validation (11 bad parameter sets rejected), price continuity at
graduation within 0.1 %, full lifecycle with creator tax, buyback-and-lock, staking (stake/claim/unstake, dust,
no-staker accrual), 3 parameter sets × 6 000-op fuzz and × 60 000-op fuzz through graduation (`VEIL_FUZZ_SEED=13`)
with every field, every transfer and the contract's DERO balance matching the model; factory registration, fee
forwarding, owner-only setters, two-step ownership; social profile/follow/unfollow key layout; NFT lock → wait →
mint → unlock → payouts. Every contract parses with derohe's parser and exposes exactly the documented entrypoints.

**Defect found by test:** `launch.bas` stored its curve supply under the key `"C"`, which is the node's code key —
the contract overwrote its own code at install. Renamed to `cs`; `parse_test.go` now forbids the pattern
(DECISIONS §1.2a).

**Simulator (live chain, `tools/simlaunch`):** factory install, launch install with creator allocation visible in the
creator's wallet, registration, 4 published curve buys to graduation (last one capped with refund), AMM sell/buy,
stake → taxed buy → claim, all matching the model step by step; the first run then hung on decrypting a
4.7×10¹³-unit balance (DECISIONS §5.2, RISKS R-10) — supplies were reduced to 10⁹ units and the run completed the
staking section before a test-wallet funding shortfall; rerun on fresh wallets recorded below.

**Gas:** launch Buy ≤ 1.61 M, Stake 1.71 M, Unstake 1.78 M compute (limit 10 M); install storage gas ≈ 14 300.

### Phase 3 — Frontend against the simulator via XSWD (2026-09-05)

**Built:** `app/` — 11 files, each under the 18 KB DOC limit: `rpc.js` (daemon WebSocket JSON-RPC, XSWD proxy
fallback, bech32 canonical addresses, sha256), `xswd.js` (handshake, serialised rate-limited queue, events),
`amm.js` (BigInt mirror of the reference formulas: quotes, impact, min received, formatting), `feed.js`
(registry → template-hash + treasury verification → assets, trades `t_<n>`, price points `p_<n>`, social),
`ui.js` (DOM kit, sheet, toasts, SVG sparkline/chart with trade markers, hash router), `screens.js` (Home with
trending carousel + verified feed with All/Following filter, Search, Trade hub, Portfolio with device-local P&L,
Profile, Settings), `token.js` (token page with graduation progress, chart markers, Trades/Holders/Info, sticky
Buy/Sell, trade sheet with presets/quote/impact/fee/tax/min-received/slippage/publish toggle, liquidity sheet,
staking sheet, NFT page), `app.js` (boot, node + wallet connection, gas-estimate → transfer → confirmation flow,
launch/pool/NFT/profile forms). Tooling: `tools/simdeploy` (installs factory + social, writes `config.js` with
template hashes), `tools/serve`, `tools/xswdwallet` (auto-approving XSWD wallet for tests), `tools/uitest/ui.mjs`
(Playwright).

**Verified in a real Chromium against the live simulator** (`docs/ui/*.png`): read-only pass over every screen with
zero console errors; wallet pass: XSWD connect → launch a token from the form (install + register, two wallet
confirmations) → buy 1 DERO with "Post this trade" → the published record appears in the pool state with the
canonical address → feed card → portfolio holding and P&L → profile edit → unfollow/follow → stake from the token
page (`stot` on chain = 1 000 000). `UI TEST OK`.

**Facts learned and recorded:** XSWD echoes ids quoted (§5.1); address payload = version byte + key (§5.1); balance
decryption cost (§5.2); a page reload drops the wallet socket (the app reconnects quietly when previously connected).

### Phase 4 — Social layer + TELA install/update + RISKS (2026-09-05)

**Social:** `social.bas` (profiles, follow graph) is deployed by `tools/simdeploy` next to the factory; trade posts
are option 1 from the prompt (written by the pool itself). Verified in the harness (`TestSocial`), on the live chain
(`simlaunch` step 8) and from the browser (profile edit, unfollow, follow with the state re-rendered).

**TELA round trip (`tools/telainstall`, recorded run):**
- 14 `TELA-DOC-1` installs (gzip + base64, DERO-signed `fileCheckC/S`, ringsize 2) + 1 `TELA-INDEX-1` install
  (ringsize 2, updatable): INDEX `ece546025848fd8f02849c1723d5578d5169e50948aa55c9a1c010b0863b65cb`, dURL
  `veil.tela`, 14 DOCs, 2.64 KB of the 11.64 KB INDEX budget. Sizes and fees per file in `deploy/README.md (TELA)`:
  ≈ 77.6 KB on chain, ≈ 0.86 DERO in fees for the whole app, 0.038 DERO for the INDEX.
- Served with the `tela` package (`tela.ServeTELA`) on `http://localhost:8082/index.html`; the full wallet flow
  (connect over XSWD, launch, buy with publish, feed, portfolio, profile, unfollow/follow, stake) passed from the
  served copy: `UI TEST OK` (`docs/ui-tela/`).
- **Update:** changed `index.html` (new `<meta name="veil-commit">`), re-ran with `-index=…`: 14 new DOCs +
  `UpdateCode` TX `8a5e5fe014c38cc473c0a1bcc4831645029445f1e419ed17e370e173c9e1e21a`. On chain afterwards:
  `commit = 1`, key `0` = install TXID hex, key `1` = the update TXID hex, and the served `index.html` contains the
  new tag. Record: `deploy/tela-deployment.json`.

**Bundle:** 11 app files + 3 templates; the largest source file is 17.6 KB (under the 18 KB DOC code cap), so no
DocShards were needed; compression is mandatory anyway (`*/` in CSS, non-ASCII in JS).

**Regulatory:** RISKS R-9 has the plain-English operator section; R-10 records the balance-decryption constraint.

**Live launch scenario (`simlaunch`, fresh wallets):** factory → launch (10⁹ units) → registration → 4 published
buys to graduation → AMM sell/buy → stake → taxed buy → claim (reward 83 892 units received, 1 unit of dust left in
`tacc`, exactly as the model) → unstake → treasury claim (6 375 units) → social profile + follow: all matching the
model after every step. The NFT step reverted at the estimator in that run; isolated with `tools/simnft` (result
below).

**NFT on the live chain (`tools/simnft`, recorded):** install (fee 4 453), registration, holder locks 5 000 VSC,
2 blocks later mints edition 0 for 1 DERO (`fd` 500 / `cacc` 99 500, wallet NFT balance 1, `ed_0` = holder),
unlock returns the tokens, creator and treasury claims leave the contract at 0. `SIMULATOR NFT OK`.
Cause of the earlier revert: the daemon's gas estimator runs contracts with `BLOCK_HEIGHT() = 0`, so a height-gated
`Mint` is rejected at estimate time only; the app and tools now send such calls with a fixed fee (DECISIONS §3).

**Test plan status (prompt §7):** 1 simulator smoke ✓, 2 overflow fuzz (10k/200k pool, 3×60k launch) ✓,
3 slippage/min_out reverts ✓, 4 ringsize > 2 → every signer-dependent entrypoint returns 1 (funds refunded) ✓,
5 re-entrancy impossible: no cross-contract call exists in the DVM (DECISIONS §1.2) ✓, 6 feed integrity: published
trades appear as `t_<n>`, unpublished ones do not, forging is impossible because only the pool writes the key ✓,
7 TELA round trip incl. XSWD swap and INDEX update ✓, 8 bundle size and fees reported ✓.

### Where things stand and what comes next

- Everything above ran on the local simulator. **Nothing was sent to testnet or mainnet.**
- Contracts are final candidates: immutable templates whose SHA-256 hashes are pinned in `app/config.js`.
- Suggested next steps, in order: (1) legal review per RISKS R-9; (2) repeat `tools/simdeploy`, `tools/simlaunch`
  and `tools/telainstall` against **testnet** (`--testnet`, daemon 40402, wallet 40403) and open the INDEX from a real
  Engram build to confirm the XSWD prompts read well; (3) decide the treasury and owner keys; (4) only then mainnet.
- To resume locally: `deploy/README.md (Simulator)` (build + run), `tests/run.ps1` (contracts), `tools/uitest/ui.mjs` (browser).
- Long-running helpers left in place after the build session: the simulator (`simulator.exe`) and the TELA host
  (`telainstall.exe -serve` on `http://localhost:8082`). Stop them with `taskkill /F /IM simulator.exe` and
  `taskkill /F /IM telainstall.exe`; the auto-approving test wallet was stopped.

**Second INDEX update (final sources, 2026-09-05):** `commit = 2`; keys `1` and `2` hold the two update TXIDs
(`8a5e5fe0…`, `6e40f635…`), identical to `deploy/tela-deployment.json`; the chain-served `index.html` shows
`veil-commit = 2`; read-only browser pass over the served final app: `UI TEST OK`.

### Phase 5 — "Alive": live markets, charts, mood (2026-09-05, operator request)

**Ask:** make it feel unique and alive, like Robinhood's energy and DexScreener's live markets; accurate live charts.

**Built (app only, no contract change):**
- `live.js` — block clock (polls `DERO.GetHeight` every 2.5 s; every new block probes the listed contracts and reloads
  only those whose state changed), rolling numbers with green/coral flashes, live "12s ago" labels, the price tape
  (marquee, pauses on hover), ambient mood glow (volume-weighted 24h change → hue).
- `stats.js` — 5m/1h/6h/24h/7d changes, per-block reserve flow (covers unpublished trades, netted per block),
  published buys/sells, market cap, liquidity, age from the install height, HOT/NEW, curve progress, candles.
- `chart.js` — SVG candles/line from per-block closes (exact at block resolution), volume bars from reserve flow,
  last-price line with a live dot, crosshair with OHLC + flow tooltip, markers for followed traders; interval adapts
  so a young market still shows ~60 candles; timeframes 1h/6h/24h/7d/all.
- `markets.js` — Markets tab (replaces Search): rank, sparkline, price with subscript zeros, 24h change, volume,
  liquidity, mcap, badges, watchlist star, filters (Trending/New/Gainers/Losers/Graduating/Graduated/Pools/Watchlist),
  sort, search, rows updated in place.
- `home.js` — hero (portfolio value live when connected, market pulse otherwise), Trending, Movers, New launches,
  Graduating soon strips, feed with slide-in for new trades and Watchlist filter. Watchlist (per device) added.
- Token page: change chips, stats grid, buy/sell pressure bar, chart controls, live trades.
- `tools/simtraffic` keeps the simulator busy so all of this can be seen moving.

**Accuracy statement:** the chain stores the closing reserves of every block that changed a pool, plus the live
reserves; the chart is exact at that resolution, never smoothed or sampled. Individual trades inside a block are
not visible unless published (by design: privacy). Volume bars use net reserve flow per interval, so unpublished
trades count; published volume is exact and shown separately.

**Verified:** read-only browser pass over every screen with live traffic (`docs/ui2/`), zero console errors; wallet
pass (connect, launch, published buy, feed, portfolio, profile, follow, stake) rerun below.

**Results:** wallet pass on the new interface `UI TEST OK` (connect, launch, published 0.5 DERO buy, feed,
portfolio with live value, profile edit, follow, stake). On-chain app updated to **commit 3** (INDEX
`ece54602…`, update TX `bc2c3d52…`, 22 DOCs). Three fixes came out of the screenshots: rolling numbers left a fresh
element empty when the value had not changed, the Markets row layout, and candle intervals for young markets;
wallet balance reads were moved to a low-priority XSWD queue so a trade never waits behind them.

### Phase 6 — Quality, security, scale (2026-09-05, operator request)

**Ask:** "improve the quality and security whilst keeping it lightweight, right now it looks cheap. It needs to be
able to host thousands in volume every day so it must be an extremely easy to use, intuitive, safe platform."

**Quality (app only, no contract change; still no framework, no build step, every file under the 18 KB DOC limit):**
- Design system: `styles.css` now holds only tokens, layout and type (surfaces s1–s4, hairline borders, 8-pt
  spacing, a real type scale with tabular numerals); components moved to a new `ui.css` (cards with an inner
  highlight, quieter chips, segmented controls, buttons with depth, risk boxes, collapsible details).
- Identity avatars: every token and account gets a two-hue gradient derived from a hash of its id, so listings
  without an icon no longer show grey letter boxes. Creator/profile images stay optional (see security).
- An inline SVG icon set (`ui.icon`) replaces text glyphs: wallet, shield, star, copy, share, warning, chevron.
- Feed rows redesigned: side accent bar, bold verb + amount, ticker pill, time on the right, follow button.
- Token page: Verified shield next to the ticker, copy-id and share buttons, plain-language risk box (high tax,
  thin liquidity, newly launched), "Public traders" instead of a stakers count that only existed for one tax mode.
- Trade sheet rebuilt around one question, "what do I get": big amount input, an outlined "You receive" box with
  the impact and the guaranteed minimum in one sentence, everything else (fees, tax, min received, route, ring
  size, slippage chips) under a collapsible Details row, the publish toggle, one confirm button whose label turns
  into "Insufficient DERO" when the wallet cannot cover it.
- First-run intro (three cards: no server, private by default, verified listings) and a "Show the intro again"
  button in Settings; the wallet button is the primary action while disconnected.

**Security (client side; the contracts were already fuzzed and are unchanged):**
- Content Security Policy in `index.html`: `default-src 'none'; script-src 'self'`: no inline or third-party
  script can run even if an on-chain string were ever mis-rendered; `connect-src` is limited to WebSocket/loopback
  (node and wallet), `img-src` to https/data, `object-src 'none'`, `base-uri 'none'`, `form-action 'none'`.
  The browser test injects an inline script first and asserts that it is refused.
- Every on-chain string passes `ui.clean` (control, bidi-override and zero-width characters stripped, length
  bounded) and is rendered as a text node; URLs must be `https://` (`ui.safeUrl`) or they are ignored. Forms
  refuse non-https image URLs and non-alphanumeric tickers before anything is sent.
- Images (token icons, avatars, NFT art) load with `referrerpolicy=no-referrer` and can be switched off in
  Settings, with the reason stated (loading an image tells that host your IP). `<meta name=referrer no-referrer>`
  covers everything else.
- Transaction gate in `app.tx`: refuses any call whose contract id is not a verified listing, the factory or the
  social contract; refuses malformed asset ids; trades never accept more than 10 % slippage (`MAX_SLIP`), and
  a price impact above 15 % needs an explicit "I understand" switch (5–15 % shows an amber warning).
- Per-user contract keys (stake `st_/sd_`, NFT lock `lk_/lh_/lm_`) are read by exact key for the connected
  address only; the app no longer enumerates other users' entries.
- Settings gained a "Security & privacy" panel that states what the app can and cannot do, the build commit,
  and the image switch; "Clear local data" now also drops the market cache.

**Scale (thousands of trades a day without getting heavier):**
- Incremental sync in `feed.js`: a market is loaded in full once (bounded to the last 4 000 price points and
  2 000 published trades); after that every block probes a fixed key set and fetches only the new `p_<n>` /
  `t_<n>` keys by `keysstring` ranges (250 keys per call). `variables:true` is no longer used for markets at all
  (only for the small social contract). Cost per block per market is now O(new keys), not O(history).
- A compact copy of each market (cache v2: last 1 500 points / 600 trades, written at most every 30 s) lives in
  localStorage so reopening the app is instant and the first block only fetches what changed while the tab was closed.
- Contract code is verified once per session per SCID (contracts are immutable) instead of on every reload.

**Verified (2026-09-05):**
- CSP probe: an inline script injected by the test is refused (1 violation report) before any functional step.
- Read-only pass over every screen with live simulator traffic: zero console errors (`docs/ui3/`).
- Wallet pass `UI TEST OK` (`docs/ui3w/`): connect over XSWD, launch from the form, buy 0.5 DERO with publish on
  (the fresh curve gave 17.6 % impact, so the run had to flip the new "I understand" switch), the published trade
  read back from the contract with the right address, feed, portfolio, profile edit, follow, stake (1 000 000 units
  on chain).
- On-chain app updated to **commit 5** (INDEX `ece54602…`, update TX `ed987407…`, 23 DOCs, ≈ 121.9 KB on chain,
  ≈ 1.30 DERO to re-install everything; sizes in `deploy/README.md (TELA)`). The two CSS fixes found in the
  screenshots (backdrop under the sticky Buy/Sell bar, one-line preset chips) landed before the installer reached
  those files, so the commit matches the working tree.

**Found by the runs:** a profile page listed one Follow button per trade row (now only in the header); the test
wallet had run down to 0.08 DERO after earlier launches (funded from a simulator wallet; the launch simply never
confirmed, which is the correct failure mode); Playwright's `waitForFunction` is itself blocked by the CSP (test now
probes the policy in a plain page, then runs the flow with `bypassCSP`).

### Phase 7 — Founders Pass, NFTs section, desktop (2026-09-05, operator request)

**Ask:** "needs a section for NFTs"; "my grand idea for the launch of the exchange is an NFT that gets a % of all
transactions on the website and the NFT holders are rewarded to their wallet, the NFT is also tradeable"; "we also
need a mobile and a desktop version, should be smart design and know what the person is using".

**What DERO allows, and the design that follows (D-10 in DECISIONS):**
- A contract cannot pay another contract and cannot see who holds an asset (balances are encrypted). So the pass is
  its own contract; holders *register* passes in it (one click, withdraw any time) to earn; and the hand-off of fees
  from the market contracts to the pass is a wallet step by the treasury, made public and automated.
- `contracts/founders.bas` (12.2 KB, immutable): fixed editions minted at a fixed price from block `start`
  (proceeds to the treasury, optional `reserve` to the creator), `share` bps promise stored at install, a per-unit
  reward accumulator (×2^32, 128-bit muldiv) so a deposit is O(1) for any number of holders, `PayBatch(from, n≤15)`
  pushes pending rewards to registered wallets, `Claim` pulls, `Register`/`Withdraw` keep a compact registry, and a
  built-in marketplace (`List`/`Buy`/`Unlist`, fixed price per unit, `mfee` bps of every sale to the holders). The
  pass is a normal DERO asset, so it also moves wallet-to-wallet.
- `tools/treasurybot`: sweeps every listed market, claims fees above a threshold, deposits the promised share
  (read from the contract, not a flag) and pushes rewards in batches of 15. Everything it does is visible on chain.
- Rounding fix found by the pass fuzz and applied to `launch.bas` too: the reward pool now shrinks by
  ceil(inc·units/2^32), so a residual below one atomic unit per unit is never credited twice; a payout can never
  exceed the pool. The old launch hash stays accepted through `templates.launch.legacy` (its contracts are immutable
  and were verified when current); the simulator keeps its history.

**App:**
- New **NFTs** tab (`app/nfts.js`): the pass card (verified, share, editions, price, holders, paid out), your passes
  (in wallet / registered / pending) with Register, Claim, Withdraw and Sell, a quantity mint, the marketplace with
  Buy/Unlist sheets, a **Public accounting** card (promise, deposited, marketplace fees, paid, owed, waiting,
  registered units, plus "push rewards" and "release waiting DERO" buttons anyone can use), then the holder-NFT
  collections. Home shows a Founders Pass card.
- `feed.js` loads the pass every block from fixed keys (verified against `templates.founders`), the last 300
  listings by key range, and the connected wallet's own registry entry by exact key.
- **Desktop and phone from one build** (`app/desk.css`, loaded last): at ≥ 960 px the bottom tab bar becomes a left
  sidebar with the brand, Home gets a two-column grid (markets and strips left, the live feed as a sticky rail), the
  token page a two-column grid (chart and stats left, Buy/Sell, trades, holders and info in a sticky side panel),
  Markets shows Vol / Liquidity / Mcap columns with a header row, carousels become 4–5-column grids, sheets become
  centred modals, toasts move to the corner. Hover states only apply on `hover:hover and pointer:fine`. Nothing
  sniffs the user agent; the layout follows viewport and pointer, so tablets and window resizes behave.

**Tests (`tests/founders_test.go`, run through derohe's DVM):** mint rules (start height, exact payment, cap,
anonymous refused, proceeds to treasury), register/withdraw registry compaction, deposit before anyone registers
(waits, then released on the first registration), pro-rata push and pull payouts, marketplace (list, underpaid buy
refused, partial buy, fee to holders, stranger cannot unlist), a 60-holder PayBatch gas measurement (15 holders ≈
6.18 M of the 10 M compute gas), and a 3 000-step randomised run of register/withdraw/deposit/claim/paybatch/
list-and-buy against a big.Int model of the accumulator with the DERO invariant (`balance == rpool + und`) checked
throughout. The whole suite (pool, launch incl. 3×6 000-op fuzz, factory, social, nft, founders) is green.

**Verified (2026-09-05):**
- Contract suite green through derohe's DVM: founders (rules, gas, 3 000-step model fuzz), pool, launch (rounding
  fix, 3×6 000-op fuzz), factory, social, nft, parse guards.
- Founders Pass deployed on the simulator by `simdeploy` (500 editions at 5 DERO, 50 reserved, share 50 %,
  marketplace fee 2.5 %), `config.js` rewritten with `founders` and the legacy launch hash.
- Browser, phone (430 px): read-only pass over every tab including NFTs, zero console errors; wallet pass
  `UI TEST OK` (connect, launch, published buy with the impact switch, feed, portfolio, profile, follow, stake), then
  the pass flow `UI TEST OK`: mint 1 for 5 DERO, register it, registry entry read back from the contract.
- Browser, desktop (1280 px): sidebar shell asserted (column layout, left rail), home / markets / NFTs / token
  rendered with zero console errors (`docs/ui4/desk-*.png`).
- Treasury sweep on the live simulator (`treasurybot -once`): claimed 16 629 atomic units of fees from six markets,
  deposited 8 314 (exactly 50 %) into the pass, one `PayBatch` paid 8 314 to the only registered holder; the contract
  then read `dep 8314, paid 8314, rpool 0, und 0, hn 1` — the promise, the deposit and the payout all agree.
- On-chain app updated to **commit 6** (INDEX `ece54602…`, update TX `e4a73d2d…`, 26 DOCs ≈ 137 KB on chain incl.
  `founders.bas`, `nfts.js`, `desk.css`; sizes in `deploy/README.md (TELA)`).

**Found by the runs:** the NFTs page re-rendered on every block, which made its buttons flicker under a fast
simulator (now re-renders only when the pass, the wallet's own state or the collections changed); the desktop
overrides had to move into a stylesheet loaded after the component styles; the TELA wallet ran dry after 26
installs (funded, update rerun).

### Phase 8 — Visual identity and the R&D loop (2026-09-05, operator request: "improve the UI significantly", then "do self improvement every 30 minutes")

**Built:** a stronger identity from the ground up — colour-mesh ground under a mood glow, glass surfaces with inner
highlights, pill actions with coloured glows, display numerals, a proper type scale; area-filled sparklines with
per-instance gradients; ranked market rows (medal colours for the top three); section heads with icons and
subtitles; staggered entrance motion and page transitions (all off under reduced motion); illustrated empty states;
a market-pulse hero (24h flow rolling number, trades per hour, buy pressure bar); desktop header search that drives
the Markets filter and a sidebar wallet card with the build commit. Stylesheets split into base / components 1 /
components 2 / desktop to stay under the 18 KB DOC limit each. Two alignment faults found in the screenshots were
fixed (desktop card grid now auto-fills at ≥ 200 px per card; hero mini stats stay together).

**R&D loop:** a session cron fires every 30 minutes with a fixed brief (one bounded improvement per cycle, research
from primary sources, tests and screenshots as evidence, entries in `docs/history/HISTORY.md (day log)`, TELA commit when app files
change). The backlog and the rules the loop must respect are in `docs/history/HISTORY.md (day log)`. Cron jobs live only in the
Claude session and expire after seven days.

**Verified:** phone + desktop read-only browser passes green (CSP probe, zero console errors), screenshots reviewed
before and after the two alignment fixes (`docs/ui5/`). On-chain app updated to **commit 7** (INDEX `ece54602…`,
update TX `ec0229a6…`, 27 DOCs ≈ 144 KB; sizes in `deploy/README.md (TELA)`).


---

# Day log (2026-09-06)

## Veil R&D log

Working file for the recurring self-improvement cycle (operator instruction 2026-09-05: "do self improvement every
30 minutes, research and development. Make it a fully fledged, lightweight, competitive decentralized exchange
with easy to launch smart contracts, clean effects and all graphics and charts properly aligned and neat").
One bounded improvement per cycle; each entry says what changed, why, and how it was verified. Rules that every
cycle must respect: app files < 18 KB each (TELA DOC limit), no frameworks or external requests (CSP), nothing
touches mainnet, contract maths changes need `tests/run.sh` green and a DECISIONS entry, template hash changes
need `simdeploy -legacy` for the old hash.

### Wrapper backlog (operator directive 2026-09-06: build the k-of-n wrapper to the highest level, SHLD as reference, security-checked; 20-minute loop)

Ground truth first: research/crosschain/RESEARCH.md (Report), REVIEW.md, DECISION.md, RESEARCH.md (Specification), research/wrapper/README.md (security review).
Done so far: contracts/vault.bas (k-of-n mint/burn, cap, bonds, slash, halt, register/attest) with tests/vault_test.go green;
proof-xmr and proof-zec (hash-preimage = key-share proofs, both curves); frost-zec (2-of-3 RedPallas); zec-t-htlc.

1. **Signer daemon `tools/vaultsigner`** (Go, like treasurybot): watches a foreign backend for deposits to attested
   addresses, votes Mint(id,to,amount) after N confirmations, watches Burn records and co-signs releases, votes
   Released. Backends: `sim` (JSON file of deposits, for simulator tests) first; `monero` (monero-wallet-rpc,
   subaddress per user index, multisig experimental) and `zcash` (lightwalletd/zallet, diversified address per
   index, FROST via reddsa) after. Two daemons + the simulator = end-to-end wrap/unwrap demo; record txids.
2. **Wrap screen in the TELA app** (app/wrap.js + sheet): Register, show the attested deposit address (`da_<j>`)
   with a QR, wrapped balance, Burn(dest) form with address validation per chain, pending withdrawals with status,
   proof-of-reserves panel (vault supply `sup` vs. federation-published reserve, view key link). Keep < 18 KB.
3. **Markets quoted in wrapped assets**: check whether launch.bas / pool.bas take any asset as quote; if not,
   add the quote-asset parameter so COIN/wXMR curves graduate into COIN/wXMR pools; the UI route pill shows the quote.
4. **Gas sliver on deposit**: the signers also send a DERO sliver (fee-funded) so an XMR-only user can transact;
   define the fee split in DECISIONS.md.
5. **Security hardening round 2** (see research/wrapper/README.md (security review) open items): per-deposit maximum, daily mint rate
   limit, withdrawal delay window with k-of-n veto, signer rotation (add/remove by k votes with a new epoch),
   unbonding with a cooldown, event log keys for indexers, fuzz test of vote counters.
6. **Proof of reserves**: publish the federation view key (XMR) / full viewing key (ZEC) in the contract
   (`pub_view`) so the app can show reserve = supply; write the checker tool.
7. **Monero side**: monero-wallet-rpc multisig on stagenet (2-of-3), subaddress derivation for user indexes,
   sweep on withdrawal; measure confirmation timing; FROSTLASS as a later upgrade.
8. **Zcash side**: Orchard note scanning for the federation FVK (zcash_client_backend), spend with the joint
   ask via FROST (orchard Bundle::apply_signatures takes signatures, so FROST output can be plugged in);
   the transparent HTLC exit hatch is already verified.
9. **Testnet exercise:** install the vault and run tools/simvault + two vaultsigners on the private testnet
   (owner 40403, treasury 40404; signer wallets via tools/netwallet on 40405–40407, funded from the owner premine);
   TELA install with `telainstall -network testnet -app dist/testnet`; open the INDEX and confirm the pill reads
   "Testnet"; record SCIDs in deploy/networks.json and the run in deploy/README.md (Testnet).
10. **Atomic-swap exit hatch v1** (Solana first): htlc.bas with the gate secret + punish path, Solana escrow
   program, companion state machine; keep it working so nobody depends on the federation.

### Backlog (highest value first)

1. **Profile page:** avatar upload guidance (https only), copy address, "share profile" link, follower list from
   `fr_` counts is fine but a followers list would need an indexer: document the limit.



- **Atomic swaps, part 2: Solana leg + order board.** A minimal Solana escrow program keyed to the same hash, an
   order-board contract on DERO (maker posts DERO↔SOL terms), and a client module that talks to the user's own
   Solana RPC and wallet behind an opt-in setting (the CSP stays strict by default). Transparent Zcash / BTC / LTC
   follow the same HTLC shape.
- **Atomic swaps, research: Monero and shielded Zcash.** No scripts there: secret-as-key-share design where DERO
   is the scripted side; the counterparty needs an off-chain consistency proof (DERO cannot verify signatures).
   Write the protocol sketch and cite Farcaster/COMIT before any code.

### Research notes

- 2026-09-06: DVM built-ins include `sha256`, `sha3256`, `keccak256` (25 000 compute gas each) and `BLOCK_HEIGHT`, but no
  signature verification and no way to read another chain: hash-time-locked escrows are possible on DERO, bridges and
  wrapped assets are not (they need an oracle or custodian).

- 2026-09-05: DVM compute gas is 5000 per line and 800 per expression evaluation with a 10 M cap per call
  (`vendor/derohe/dvm/dvm.go`, `sc.go`); a settle with 128-bit muldiv costs ≈ 0.41 M, hence PayBatch ≤ 15.
- 2026-09-05: Playwright's `waitForFunction` polls through `eval`; pages with `script-src 'self'` need
  `bypassCSP: true` in the test context (the policy is probed separately first).

- 2026-09-06: `DERO.GetGasEstimate` for an install can fail at random with `panic … Bucket cannot start with ':'`:
  `dvm.Simulator.SCInstall` names the graviton tree after a simulated SCID, and graviton refuses names whose first
  byte is 0x3a (1 in 256). `telainstall` stops on it (seen on screens.js, commit 11 attempt); rerunning simply draws
  another id. Worth a retry loop in the tool and a note in DECISIONS §3.

- 2026-09-06: Haven-style synthetics (xXMR on DERO) would need a price feed entering the chain as calls from whitelisted
  signers (a median over several is the best available) and DERO collateral; they give private price exposure, not
  real XMR, and failed in practice (stale-price arbitrage, collateral drain). Not pursued; atomic swaps are the
  trust-free route, a federated wrapped asset the transparent-trust route.

### Done

- 2026-09-05 (Phase 8, manual pass before the loop started): visual identity rebuilt — colour mesh ground, glass
  surfaces, pill actions, display numerals, area sparklines, ranked rows, section heads with icons, staggered
  entrance motion, page transitions, illustrated empty states, market-pulse hero, desktop header search and sidebar
  wallet card. Verified: phone + desktop read-only browser pass green, screenshots reviewed (`docs/ui5/`). On chain as TELA commit 7 (update TX `ec0229a6…`, 27 DOCs ≈ 144 KB).

- 2026-09-06 (cycle 1): **one-screen token launch with a live preview** (`app/forms.js`). Name, ticker, graduation
  target (preset chips 10/50/100/500 DERO), image and description on one screen; supply, decimals, creator
  allocation, tax and tags under "Advanced" with defaults. The preview card shows the token as it will appear in
  Markets, the bonding curve (40 samples of the real constant-product maths: vd0 = G/3, vt0 = (G+vd0)·C/G), first
  price, price at graduation (exactly 16×), market cap at graduation, tax, how many tokens sit on the curve versus
  the AMM reserve, and the cost line ("two wallet confirmations · fees ≈ 0.25 DERO"). Evidence: read-only pass opens
  the sheet and asserts the preview text (`docs/ui6/08-launch-sheet.png`, one overlap fixed: the preview curve
  needs an explicit height); the full wallet flow launched a token through the new form and passed every later
  step (`docs/ui6w/`, `UI TEST OK`); on chain as TELA commit 8 (update TX `8fa4773e…`, 27 DOCs ≈ 145 KB).

- 2026-09-06 (cycle 2): **chart axes and alignment audit** (`app/chart.js`). One shared frame for the price pane,
  the volume pane and both axes: 12-unit left padding so the first candle and label clear the rounded edge, a
  common axis column on the right for tick labels, the last-price tag and the crosshair tag (the last-price tag now
  hides while the crosshair tag would overlap it), a volume pane with its own baseline, separator and "VOL" label,
  tick counts that follow the layout (desktop: 8 time ticks, 6 price ticks; phone: 4 / 5), and the portfolio series
  chart drawn with the same frame, tag and live dot. Evidence: read-only pass green on phone and desktop; crosshair
  sweep screenshot (`docs/ui6/shot-token-crosshair.png`) and desktop token page (`docs/ui6/desk-token.png`)
  reviewed; on chain as TELA commit 9 (update TX `738738db…`).
- 2026-09-06 (cycle 2, docs): **creator guide** (`docs/CREATOR_GUIDE.md`): launching a token from the one-screen
  form, what the curve and graduation mean, tax modes, holder NFTs, the Founders Pass, fees, and what the wallet asks.

- 2026-09-06 (cycle 3): **trade sheet market switcher + route pill** (`app/token.js`). The sheet title is now a
  button that opens a picker of the 30 hottest markets (avatar, ticker, name, price, 24h change); picking one
  re-opens the sheet on that market with the same side. A pill next to Verified says which maths the trade uses,
  "curve" or "AMM". Evidence: the read-only pass opens the picker, counts the markets, picks one and asserts a new
  buy sheet (`docs/ui6/05b-trade-switched.png`); phone and desktop passes green; on chain as TELA commit 10
  (update TX `42c9faad…`). Note for later cycles: token.js is at 17.1 KB of the 18 KB DOC limit; the trade sheet
  should move to its own file before it grows again.

- 2026-09-06 (cycle 4): **portfolio cost basis from two sources** (`app/app.js`, `app/screens.js`). P&L now merges
  the trades made on this device (they carry a txid) with the trades the connected address published on chain,
  counting a published trade once when it matches a local one (side, asset, amounts). Each holding row shows its
  average cost and unrealised P&L; the footer says how many trades the basis uses and how many came from chain.
  Evidence: full wallet flow green with the portfolio rendered after a published buy (`docs/ui7w/16-portfolio.png`);
  phone and desktop read-only passes green; on chain as TELA commit 11 (update TX `463f9e7c…`; the app meta had drifted one ahead of the chain counter, re-aligned in cycle 8).
- 2026-09-06 (cycle 5): **markets table density** (`app/markets.js`, `app/desk.css`, `app/ui.css`). Desktop column
  headers are sortable (Market → hot, Vol 24h, Liquidity, Mcap, 24h) with an arrow on the active one, the header
  row is sticky under the app header, and a Compact toggle (remembered in settings) tightens rows and hides
  sparklines on both form factors. Evidence: read-only pass green on phone and desktop; `docs/ui7/desk-markets.png`
  reviewed. Same on-chain commit as cycle 4.

- 2026-09-06 (cycle 6): **keyed feed rows on Home** (`app/home.js`). The live feed used to rebuild up to 60 cards on
  every block; rows are now cached by trade id, trader name and follow state, so a block that adds one trade adds
  one card (with its entrance animation) and leaves the rest untouched; relative times keep ticking through the
  existing `data-ts` updater. Cache bounded at 400 rows. Evidence: phone and desktop read-only passes green; same
  on-chain commit as cycles 4 and 5. Also learned and noted above: `GetGasEstimate` can panic at random on an
  install when the simulated SCID starts with byte 0x3a; `telainstall` now retries the estimate a few times.

- 2026-09-06 (cycle 7): **holder-NFT page parity** (`app/nft.js`, `app/feed.js`). The collection page now has a
  "Your lock" card (amount, since block, ready-in countdown with a progress bar, minted state) and a "Public
  accounting" card (editions, creator revenue waiting, protocol fee and unclaimed fees, creator, contract), matching
  the Founders Pass page; the contract's `cacc`, `fd` and `fee` keys are now part of the collection's fixed key
  set. Evidence: phone and desktop read-only passes green; on chain as TELA commit 12 (update TX `9bec3cbb…`).

- 2026-09-06 (cycle 8): **feed.js split + accessibility pass.** The Founders Pass data layer moved to
  `app/founders.js` (feed.js 17.9 → 13.5 KB, a fourth of the DOC budget back); the main screen no longer carries
  `aria-live` (it re-renders every block, which would have made screen readers narrate the market), toasts keep it;
  the watchlist star and the icon buttons have 40 px hit areas; secondary text is one step lighter for contrast on
  the glass cards. Evidence: phone and desktop read-only passes green; on chain as TELA commit 12 (update TX `9bec3cbb…`).

- 2026-09-06 (cycle 10, contracts): **atomic-swap escrow on DERO** (`contracts/swap.bas`, 3.4 KB, immutable, no fee): `Lock(hash,
  taker, expiry, asset)` holds DERO or any asset under sha256(secret) until an absolute height; `Claim(id, secret)` pays
  the caller (the named taker only, when one was set) and leaves the secret on chain for the other leg; `Refund(id)`
  returns funds to the maker after expiry. `tests/swap_test.go` through derohe's VM: short hash, past expiry, empty
  and anonymous locks refused; stranger and wrong-secret claims refused; early refund refused; claim pays exactly once;
  asset lock with stray DERO refused; post-expiry claim refused; only the maker refunds, once. Claim costs ≈ 0.29 M
  compute gas. Not yet in the app: needs the order board and the Solana leg (backlog part 2).
- 2026-09-06 (cycle 10, contracts): **atomic-swap order board** (`contracts/board.bas`, 2.8 KB, holds no funds): `Post`
  advertises "I give X of this DERO asset for Y on chain C, pay me at this address" until an expiry; `Accept` is the
  taker's answer with the hash their counter-chain lock uses and a lock reference; `Cancel` by the maker while open.
  `tests/board_test.go`: empty chain, bad asset, attached DERO and anonymous posts refused; self-accept, short hash,
  double accept, cancel-after-accept, stranger cancel, accept-after-cancel and accept-after-expiry refused. The swap
  flow is therefore: taker locks on the counter-chain → Accept → maker locks in swap.bas for the taker with a shorter
  expiry → taker claims (secret revealed) → maker claims the counter-chain leg. App integration is backlog part 2.

- 2026-09-06 (cycle 9): **equity curve from the ledger** (`app/app.js`). The portfolio's holdings-value chart now
  walks each token's price points with the units actually held at that moment (device trades plus the address's
  published trades, in time order), so a market's price gaps no longer become spikes and the curve starts where
  the first buy happened; holdings without a known trade history are valued at their current units. Second pass in the same day: the per-token
  series are merged on one timeline with each token carrying its last value forward (the first pass summed values only
  at each token's own timestamps, so quiet markets read as zero between their points). Merged version on chain as TELA commit 14 (update TX `e48333cc…`).
  Reuse of unchanged DOCs did not trigger on that commit: the DOC code embeds a wallet signature that is randomised per
  run, so the tool now hashes the compressed content only (fixed and rebuilt). The install
  tool now reuses unchanged DOCs by stored-code hash (a small commit costs a few installs instead of 28) and retries
  the daemon estimator on its graviton panic. Evidence: wallet flow green with the portfolio rendered
  (`docs/ui8w/16-portfolio.png`); on chain as TELA commit 13 (update TX `c4b3202f…`).

#### 2026-09-06 — wrapper cycle 1: vault contract, signer daemon, end-to-end on the simulator, proofs for XMR/ZEC
- **Research closed out (research/crosschain/):** REVIEW.md found and fixed the load-bearing hole in the DERO↔XMR
  protocol (claim without an XMR lock; fixed with a gate secret `k_b`), README.md (Resolved) / DECISION.md / RESEARCH.md (Specification)
  written; README.md (Verdict changes) and README.md (Wrong premises) (#14–16) updated.
- **Proofs built and measured in WSL (Rust):** `poc/proof-xmr` (sha256 preimage = ed25519 key share; 37,460 gates,
  14.8 s prove, 1.15 s verify, 1,601 B, all negative tests reject); `poc/vendor/bp-pallas` (Bulletproofs R1CS ported
  to Pallas) + `poc/proof-zec` (same statement for an Orchard `ask` share; 12.2 s / 0.47 s; joint `ak` signs);
  `poc/frost-zec` (2-of-3 re-randomized FROST RedPallas under a valid Orchard `ak`, per-index deposit addresses);
  `poc/zec-t-htlc` (SHA256+CLTV P2SH HTLC under Zcash's consensus interpreter, all abort paths).
- **Wrapper on DERO:** `contracts/vault.bas` (9.8 KB, install storage gas 10,057 on the simulator): k-of-n mint votes,
  once-only deposit ids, supply cap with k-of-n raise, bonds + k-of-n slash, halt epochs, burn-to-withdraw with
  release records, per-user deposit indexes with k-of-n attested addresses. `tests/vault_test.go` green;
  `research/wrapper/README.md (security review)` reviews it (attacks table, open items).
- **Signer daemon + driver:** `tools/vaultsigner` (backend interface; `sim` backend) and `tools/simvault`. End-to-end
  on the simulator (`research/crosschain/poc/vault-e2e.log`): vault `a88778fc…` installed at height 4616; three
  signers, seal, bonds; user registered (index 0); two Attest votes → `sim:0`; simulated deposit `63a7a983…`
  (250,000 units, 3 confirmations) → two Mint votes (`0c11e284…`, `03d85ef0…`) → user balance 250,000; Burn 40,000
  (`fc6085e4…`, compute gas 286,400) → withdrawal 0 → two Released votes (`d5b48af1…`, `a776ddb1…`) → status 1 with
  the foreign txid; supply 210,000.
- **Findings:** (1) the DVM's `ADDRESS_STRING` renders a mainnet `dero1…` string on the simulator, and `ADDRESS_RAW`
  accepts it for the Mint recipient; the app must not compare address strings across networks. (2) A freshly minted
  asset cannot be spent for a couple of blocks ("could not obtain encrypted balance for self … Account Unregistered");
  the first burn attempt 7 s after the mint failed, the retry 40 s later passed. The app's Burn sheet should retry.
  (3) The DVM keeps no own-asset balance for a contract; burned units vanish, `sup` is the invariant.
- **Product decision (operator, 2026-09-06):** instant pooled COIN/XMR and COIN/ZEC markets need wrapped assets,
  hence the federation wrapper is Layer B; atomic swaps stay as the trustless exit hatch. SHLD reviewed: an
  operator ledger with per-user shielded deposit addresses; the per-user address idea is borrowed (attested indexes).
- **Loop:** the 30-minute app loop was replaced by a 20-minute wrapper R&D loop (job ba0ad69b) reading the
  "Wrapper backlog" above.

#### 2026-09-06 — multi-network build (testnet and mainnet)
- `deploy/networks.json` + `tools/netcfg` + `tools/genconfig` (`-network` → app/config.js; `-dist` → dist/<network>/
  with manifest.json and the DOC size check: all 28 simulator files ≤ 8.9 KB on chain, well under 18 KB).
- `tools/simdeploy` records its deployment in networks.json; `tools/telainstall -network`; `tools/netwallet` (headless
  wallet with the standard RPC server for any network, creates the wallet on first run); `tools/vaultsigner -network`
  with the foreign-chain parameter table (mainnet / testnet=stagenet+devnet / simulator).
- app.js: warns when the node's network differs from the build's network.
- Runbooks: `deploy/README.md (Testnet)`, `deploy/README.md (Mainnet)` (gates, costs, rollback). DECISIONS D-13.
- Testnet node: `vendor/bin/derod.exe --testnet` started; the single public seed node in derohe's source did not
  answer, so the chain is a private testnet mined locally (same rules and address prefix as the public one).

### 2026-09-06 loop 2 (see docs/history/HISTORY.md (loop records))

- S5: reward accumulators in founders.bas and launch.bas roll into eras instead of bricking; launch dist() carries what fits. Tests TestFoundersAccSaturation, TestLaunchDistSaturation; era-aware fuzz model. Template hashes changed (simdeploy -legacy needed).
- S6: the graduating buy taxes only the DERO it uses and refunds the rest untaxed; TestLaunchGraduationRefundFee checks it from chain state.
- v2 vault wrap and unwrap ran end to end on the simulator (RESOLVED section 9).
- Measured: contract calls run at ring 16 and 32 with SIGNER() blank; design in research/RING_PRIVACY.md (RESOLVED section 10).

### 2026-09-06 loops 3-4 (see docs/history/HISTORY.md (loop records))

- S7 endpoint validator + loopback-only plaintext CSP; S8 cache shape-check and chain re-read; S9 wallet boundary (toBig, canonical address).
- S16 signer daemon: durable release intents, atomic state, reserve-shortfall Halt vote; exactly-once test under crashes; e2e rerun on the simulator.
- S15 treasury bot journal with the new monotone fc counter in launch.bas and pool.bas (hashes changed again); crash test.
- Testnet: the 100 M DERO premine made the owner wallet decode balances for minutes per block; 99.9 M moved to the treasury, lookup table 2^23, redeploy script waits for sync.

### 2026-09-06 loop 5

- S10 anti-impersonation (ticker skeleton, shared/look-alike badges, unlisted contracts marked and kept out of lists, router id check, contract id in the trade sheet) and S11 two-tier pool badge. UI pass green. The 13 simulator launches really are all BRW: the badge is a true positive.
- D5: simulator config regenerated with the new template hashes; deployed contracts reused and their hashes kept as legacy.
- Testnet: fresh deployer wallet registering (miner and deployer must never be the same wallet).

### 2026-09-06 loop 6

- S13 operator rails: seed printed once unless -write-seed (refused on mainnet), VEIL_WALLET_PASSWORD, genesistx private temp wallet, xswdwallet test-network guard, treasurybot dry run on mainnet without -yes, vaultsigner -yes on mainnet.
- V1: creator images off by default.
- Testnet: deploy scripts use the deployer wallet (40405); the miner wallet never transacts.

### 2026-09-06 loop 7

- C1: three transaction outcomes; trades verified by the wallet balance that must move; UI wallet pass shows "Buy BRW confirmed" with the buy in the feed.
- P-RING contracts: SwapDeroForAssetTo / SwapAssetForDeroTo in launch.bas and pool.bas (wrappers over internal buyto/sellto); tests/ringswap_test.go; suite green. App side open (P-RING-APP).
- Testnet redeployed from the deployer wallet in 12 minutes: factory b0bbd7cc, social a5f3d33d, founders f8271d08. Simulator config regenerated with the split templates.

### 2026-09-06 loop 8

- P-RING-APP: private mode in the trade sheet (ring 16, pay a named address); UI wallet pass with --dest shows a 16-member ring on the transaction.
- C2: one readForm() for the launch preview and submit with the contract limits. T2: creator allocation shown and raised as a risk line.

### 2026-09-06 loop 9

- S14: deploy/vendor.lock, deploy/patches/derod-private-genesis.patch, tools/verifyvendor (commit, origin, allowed changes, patch bytes, binary hashes; red on a tampered file), -trimpath in every build script; derohe index repaired.
- C1 wording: calls without a probe read "included in block N"; the headless test accepts it; the private-buy check asserts the node extracted no signer.

### 2026-09-06 loop 10

- C1b: contract-key probes (register, stake, follow, pass mint/register); full wallet pass green with every toast as evidence; private buy included at ring 16 with no extractable signer.
- Ring sizes measured 2-64 (research/ringprobe-sizes.log). Simulator Founders Pass redeployed at 0.5 DERO; test wallet refilled.
- Mainnet runbook: verifyvendor gate, three-wallet rule, -yes gates.

### 2026-09-06 loop 11

- C3 metric truthfulness (curve liquidity = real DERO, FDV, published counts, LP moves excluded, pressure from flow). C4 vault conservation fuzz (600 steps). S1-S4 recorded as done. C5 zero-float lint in tests/run.sh.

### 2026-09-06 loop 12

- C6: a burn is paid abroad only at or below the DERO stable height (live: "withdrawal 2 waits for DERO finality (burned at 5805, stable height 5798)", released after 5806 with ftx 85ea84b2). C5 float lint. Liquidity sheets: slippage setting + lp probe. F1 anti-wash hot. README.md (Trust statement), README.md (Marketing copy), RUNBOOKS/ written.

### 2026-09-06 loop 13

- S22 in part: vault SetLimits (per-deposit max, per-bucket mint window, withdrawal delay) with tests. Harness: random contract ids never start with a colon (graviton refuses such tree names; one install in 256 panicked). Daemon: GetGasEstimate panics deterministically for some contents (founders.bas DOC); telainstall gained -reuse and a size-based fee fallback; the testnet install resumed from 21 DOCs.

### 2026-09-06 loop 14

- S22: sliding mint window (two buckets) with tests; signer refuses outsized withdrawals (-max-withdrawal-bps, default 20 %) and votes Halt. App: size-based fee fallback when the node cannot estimate an install (R-16). Full suite green; testnet TELA install resumed via -reuse, 23 of 28 DOCs.

### 2026-09-06 loop 15

- First local commits (00b70eb, d76f4c9): the whole project except vendor/, runtime state, wallets, seeds and upstream clones; LF pinned by .gitattributes because template hashes are byte-exact. Never pushed, no remote.
- vault limits proven on the simulator: a 40,000 deposit above maxdep 30,000 was refused at the k-th vote (supply 0), the 25,000 one is being minted; wdelay and finality gates follow.


---

# The earlier backlog (superseded by docs/PLATFORM_BACKLOG.md; §0 is the done list with evidence)

## MASTER TODO — Veil engineering backlog

Maintained every loop (operator directive 2026-09-06). Task format: **ID Title** · P0–P3 · rationale · components ·
depends on · approach · security · performance · tests · done when. P0 = must precede any real funds or public
release; P1 = next; P2 = valuable; P3 = research. Sources: five reconnaissance audits of 2026-09-06 (app,
contracts/economics, tools/supply chain, wrapper adversarial, product) plus the wrapper Phase A review. `[done]`
items stay one loop, then move to history/HISTORY.md (loop records).

### 0. Done this loop (verified)

- **[done] Re-initialisation guard on every contract.** `Initialize`/`InitializePrivate` were callable after
  install (proved on the real DVM: `swap.bas` and `board.bas` counters reset to 0, `factory.bas` ownership
  reset). Every contract now stores `initd` and refuses a second call; `tests/reentry_test.go` green; full suite
  green. Template hashes changed: simulator/testnet redeploy pending (see D5).
- **[done] Exact BigInt serialisation in the signing path.** `app.js` round-tripped amounts through
  `JSON.parse`, rounding anything above 2^53 (burn and `min_out`). Replaced by `V.jsonBig` on both sockets; no
  marker strings; unit check passes (2^60+1 kept byte-exact).
- **[done] Fee cap and disclosure, in-flight lock, txid validation** in `app.tx`: refuses a node estimate above
  1 DERO, shows the fee in the confirm toast, one transaction at a time, rejects a non-hash txid from the wallet.
- **[done] `ui.js` is text again** (control bytes in the sanitiser class escaped) so scanners no longer skip it.
- **[done] `.gitignore`**: `.testnet/`, `.mainnet/`, `*.seed`, `*.db.bak`, `dist/`, `**/target/`; research logs
  un-ignored so cited evidence is in the tree. **`tests/run.ps1`** now fails on failure. **telainstall** records
  the real network.

- **S5 done (loop 2)** · founders.bas and launch.bas reward accumulators roll over into eras (`era`/`ea_<n>`/`he_<a>`,
  `sera`/`sea_<n>`/`se_<a>`); launch `dist()` carries what fits instead of skipping for good ·
  `TestFoundersAccSaturation`, `TestLaunchDistSaturation`, era-aware founders fuzz model · suite green 2026-09-06.
- **S6 done (loop 2)** · graduating buy: fee and tax on `used` only, refund `d − used` untaxed, trade record carries
  `used` · `TestLaunchGraduationRefundFee` (chain-state invariant, not model parity) · reference model mirrors it.

- **S7 done (loop 3)** · `V.endpointURL` is the only path from a string to a socket: host:port / ws:// for
  127.0.0.1 and localhost only, wss:// for anything else; applied to the stored setting (on read and on write),
  to the wallet's GetDaemon (a refused one falls back to the wallet proxy with one toast) and to the shipped
  default; CSP `connect-src` now `'self' ws://127.0.0.1:* ws://localhost:* wss: http://127.0.0.1:* http://localhost:*`
  · UI read-only pass green on the simulator.
- **S8 done (loop 3)** · the localStorage market cache (`veil.m.<full scid>`, v3) is shape-checked on read (types,
  arity, contiguous trade numbers, non-decreasing heights, canonical addresses, counts matching the watermarks) and
  its newest 4 points and 4 trades are re-read from the chain before the rest is used; any mismatch drops the
  copy and refetches the window. No HMAC: a same-origin writer would hold the key too, so re-verification is
  the honest control · UI read-only pass green.

- **S16 done (loop 4, sim backend)** · every withdrawal goes through a durable intent (paying → paid → voted)
  written with fsync + rename before the next step; `Release` is contractually idempotent per j and the signer
  may repeat it after a crash; a corrupt state file stops the signer instead of starting it empty; state is keyed
  by signer address; an intent whose (dest, amount) differs from the chain stops with an operator message;
  `Reserve()` on the backend and a k-of-n `Halt(1)` vote when the foreign reserve is below the wrapped supply
  (once per 2000-block bucket); `-confs` defaults from the network table · `tools/vaultsigner/exactly_once_test.go`:
  100 withdrawals with crashes injected after every durable step and refused votes and backend errors mixed in:
  each paid exactly once, every vote carries the recorded txid (votes are at-least-once; the contract refuses a
  signer's second vote) · residual: S16b.

- **S9 done (loop 4)** · a wallet address that does not canonicalise ends the XSWD connection; wallet amounts
  go through `V.toBig` (number or decimal string, else null) at both balance reads; the wallet's node endpoint
  already passes `endpointURL` (S7) · UI read-only pass.
- **S15 done (loop 4)** · treasurybot journal (record claim → send → record; record deposit → send → record;
  reconcile on start) with a monotone witness: `fc` (cumulative fees claimed) added to launch.bas and pool.bas
  `ClaimFees`, mirrored in the reference models and checked by the differential tests ·
  `tools/treasurybot/journal_test.go`: 100 sweeps with crashes after either send, exact share minus at most one
  unit per deposit · residual: the pass `dep` counter is open to anyone (a stranger's deposit can mask a failed
  one: P2, per-depositor counter).

- **S10 done (loop 5)** · ticker skeleton (NFKC, upper-case, Cyrillic/Greek capitals and 0/1 folded, [A-Z0-9]
  only) groups every listed market; every member of a group of two or more carries "Ticker shared" and a
  look-alike ticker carries "Look-alike ticker" (token header, trade sheet, a mark in the markets table); a
  launch and its own pool are exempt; `#token/<id>` refuses anything but a 64-hex id; a contract reached by id
  that the registry does not list is shown as "Code verified · not listed" and never joins the lists; the trade
  sheet shows the contract id · UI pass green (the simulator's BRW launch/pool pair no longer trips it).
- **S11 done (loop 5)** · two-tier badge: a pool whose asset is not a Veil launch shows "Pool verified · asset
  unknown" (amber), inherits no name or icon, its details say so and give the asset id with a copy button; the
  pool form says what an unknown asset means.

- **S13 done (loop 6)** · netwallet: the seed is printed once unless `-write-seed` (refused on mainnet), the
  password may come from `VEIL_WALLET_PASSWORD`, a mainnet wallet refuses an empty password; genesistx restores
  the seed into a private temp dir under a random password and removes it; xswdwallet refuses any daemon that is
  not loopback or not a test network (DERO.GetInfo) before it auto-approves anything; treasurybot is a dry run on
  a mainnet config without `-yes` (or with `-dry-run` anywhere); vaultsigner refuses mainnet without `-yes` ·
  all three vendored tools compile; both bots' tests green · residual: the fork's XSWD server still binds every
  interface (documented at start-up), and `.testnet/*.seed` files from before this change remain on disk.

- **V1 done (loop 6)** · creator-hosted images are off by default; the settings toggle explains the IP exposure.

- **C1 done (loop 7)** · `app.tx` has three outcomes: not in a block after 4.5 minutes (no local entry, "check
  the wallet history before sending again"), included but not executed (an install with no code, or a trade whose
  asset balance did not move: "only the network fee was spent"), executed. For trades the wallet balance of the
  asset is read before and after (after the wallet has the block: `GetHeight` poll), buy = up, sell = down; a
  local P&L entry is written only when executed · UI wallet pass on the simulator: XSWD log shows
  GetBalance → transfer → GetHeight → GetBalance, the toast reads "Buy BRW confirmed", the feed shows the buy.
- **P-RING contracts done (loop 7)** · `SwapDeroForAssetTo(dest, min_out, publish)` and
  `SwapAssetForDeroTo(dest, …)` in launch.bas and pool.bas: thin wrappers that resolve the recipient
  (`SIGNER()` for the classic names, `ADDRESS_RAW(dest)` for the new ones) over internal `buyto`/`sellto`
  that never read the signer · `tests/ringswap_test.go` (nil signer = ring > 2: the classic entrypoints refuse,
  the To variants pay the named address, a bad destination is refused, records name the destination) · the app
  side (ring 16 through XSWD, a destination field, the settings copy) stays open as P-RING-APP.

- **P-RING-APP done (loop 8)** · trade sheet "Private mode: hide who paid": ring size 16 and a "Receive at"
  address; `app.tx` takes a ring option (16 only for the …To entrypoints), `app.trade` builds
  `SwapDeroForAssetTo`/`SwapAssetForDeroTo` with `dest`, verifies by balance only when dest is this wallet
  and by inclusion otherwise, never writes another wallet's trade into this P&L; the sheet warns when dest is the
  connected address; settings copy updated · UI wallet pass with `--dest=<simulator wallet 30006>`: the buy went
  through XSWD at ring 16 and the node reports a 16-member ring on the transaction (`ring members per payload:
  [16]`) · measured: 1,848 B at ring 2, 3,336 B at ring 16, 5,784 B at ring 64, block time unchanged (research/ringprobe-sizes.log).
- **C2 done (loop 8)** · one `readForm()` feeds the launch preview and the submit with InitializePrivate's own
  limits (decimals 0–12, supply ≥ 10^6 and < 2^63, graduation 10–10 000 000 DERO, allocation ≤ 5 %, tax ≤ 10 %,
  tax mode consistency, icon ≤ 200 / tags ≤ 100 / description ≤ 240); a bad field shows its reason in the
  preview and blocks the button; nothing the contract would refuse reaches the wallet.
- **T2 done (loop 8)** · creator allocation derived as S − cs − Lq (`H.allocation`, `H.allocBps`): shown on the
  token page ("Creator allocation … % at launch") and raised as a risk line wherever risks show (token header,
  trade sheet) from 1 % up.

- **S14 done (loop 9)** · `deploy/vendor.lock` pins the five vendored checkouts (derohe e9df1205, civilware
  derohe 433c4559, tela aa892d2f, Engram 12b6e61a, documentation 5f689fee) with their origin URLs and the exact
  local changes allowed (the derod private-genesis patch stored as `deploy/patches/derod-private-genesis.patch`,
  one Windows-uncheckoutable file, and the directories our tools are built from); `tools/verifyvendor` checks
  commit, origin, every modified/deleted/untracked path and the patch bytes (CR at end of line ignored) and
  the sha256 of every `vendor/bin/*.exe` against `deploy/binaries.lock`; a one-line tamper in a vendored README
  turned it red and the restore turned it green again; every build script now passes `-trimpath`. Found on the
  way: the derohe checkout's index recorded 4,941 staged deletions (a broken clone step), repaired with
  `git reset` (index only) · residual: the tela checkout the app is served by is not the same fork the wallet uses
  (an 11-month-older civilware derohe), noted, not changed.

- **C1b done (loop 10)** · contract-state probes: a call names the keys that must change when it executes
  (`probe: { scid, keys }`); `app.tx` reads them before sending and after inclusion and says "confirmed" only
  when one changed, "included but did not execute" when none did. Wired: RegisterLaunch/RegisterPool (factory
  `lc`/`pc`), Stake/Unstake (`stot`, `st_<me>`), Follow/Unfollow (`fc_<me>`), pass Mint (`minted`),
  pass Register (`hst`). Claims and profile saves stay "included in block N" (a no-op claim is a legitimate
  execution with no state change) · UI wallet pass exercises register, stake, follow, mint and register-pass with
  the probes.

- **S1–S4 done (loop 1, recorded loop 11)** · vault v2: tuple-keyed mint votes (`(j, id, amount)`) so one
  signer cannot freeze a deposit; quorum floor, `Halt(want)` buckets, cap epochs, slash epochs voiding stale votes;
  `cap ≤ 3 × Σ bonds`, minimum bond, `sup` underflow guard, halted checks; deposit → index → registered
  recipient with no `ADDRESS_STRING` round-trip · `tests/vault_test.go`, `research/wrapper/README.md (security review)`.
- **C3 done (loop 11)** · curve liquidity = real DERO in the contract (not the virtual reserve); "FDV" labelled
  FDV everywhere; published counts are published counts (no double count with reserve flow); proportional reserve
  moves on a pool (liquidity added/removed) excluded from volume; buy pressure from reserve flow blocks; "Public
  traders" instead of holders · UI read-only pass.

- **C4 done (loop 11)** · `tests/vault_fuzz_test.go`: 600 random steps over a sealed 2-of-3 vault (mint votes
  with lying signers mixed in, burns above and below the minimum, releases, halt/resume flips, cap moves); after
  every step `sup == Σ minted − Σ burned`, `feepool == Σ wfee`, every withdrawal record equals its burn minus the
  fee, the vault's DERO balance equals the bonds, `sup ≤ cap`. The model predicts nothing about policy: it
  observes what landed. One run: 94 mints (136 refused), 70 burns (97 refused), 61 halt flips.

- **C5 done (loop 11)** · `tests/run.sh` refuses any `float32`/`float64` in reference/, tests/harness/,
  tools/treasurybot and tools/vaultsigner (non-test files) before vetting; today's tree has none. Units and
  decimals are pinned in PIPELINE_SPEC C1.

- **C6 done (loop 12)** · the signer pays a withdrawal on the foreign chain only when the burn's DERO height
  (`w_<j>_h`) is at or below the daemon's `stableheight` (`DERO.GetHeight`; 8 blocks behind the tip on the
  simulator and the private testnet); a burn above it waits with a log line · `withdrawalFinal` +
  `TestWithdrawalFinality`; signer tests green.

- **Liquidity sheets (loop 12)** · Add/Remove liquidity use the user's slippage setting instead of a fixed 1 %,
  refresh holdings afterwards, and prove their outcome through the pool's `lp` key (C1b probe).

- **F1 done in part (loop 12)** · the hot ranking counts distinct publishers in the last hour (not published
  trades, which one account could spam) and enters reserve flow through a log; the streak penalty is left out.

- **S18 done as text (loop 12)** · `research/wrapper/README.md (Trust statement)` names every trusted party with what
  stops it and the evidence, lists what is verifiable on chain and what is not, and discloses the linkage
  (registration and every vote at ring 2 are public; withdrawal destinations are plaintext until S19);
  `README.md (Marketing copy)` holds the allowed and forbidden sentences ("federated, bonded"; never "trustless").
  The app has no wrapped-asset screen yet; when it does, these two files are its copy.
- **X3 done (loop 12)** · `research/wrapper/RUNBOOKS.md `: signer daemon (start, state, crash recovery, finality,
  backups), halt and resume, cap and bonds, reserve-shortfall incident.

- **S22 done in part (loop 13)** · vault.bas `SetLimits(maxdep, winmax, wdelay)`: owner-only, before Seal, never
  again after; a deposit above `maxdep` is refused at the k-th vote, mints over the current and previous 2000-block
  buckets are capped by `winmax` (`mw_<bucket>`, sliding), a `Released` vote does not count before `wdelay` blocks after the burn. Defaults 0
  (off) so installers are unchanged. `tests/vault_limits_test.go`: stranger and post-Seal refused, 600 > 500
  refused, 500 + 500 + 300 > 1200 refused, 200 fits, next bucket opens, release refused before the delay and
  completes after. Contract 13,825 B, install gas 14,091.

- **R-16 fallback in the app (loop 13)** · when `DERO.GetGasEstimate` fails with the graviton ':' bucket panic on
  an install, `app.tx` sends it with a size-based fee (code length + 512 units) under the same 1 DERO cap and
  says so in a toast, instead of "would fail"; calls are not affected (the estimator only panics on installs).
  The same rule is in `tools/simrpc` (every simulator/testnet installer) and `telainstall`; the current vault.bas is
  itself unestimatable (its simulated id starts with 0x3a), which is how the rule got tested on the simulator.
- **S22 sliding window done (loop 14)** · the mint window is the sum of this 2000-block bucket and the previous one,
  so a bucket boundary alone never opens a fresh window (`tests/vault_limits_test.go`: 300 refused 2000 blocks later,
  accepted 4000 blocks later); vault.bas 14,010 B, install gas 14,276. The signer daemon now refuses to pay one withdrawal above -max-withdrawal-bps (default 20 %) of the
  foreign reserve and votes Halt(1) instead (`suspiciousWithdrawal`, tested).

- **D-GIT done (loop 15)** · operator ruling 2026-09-06: commit locally, never push. First commit `00b70eb`
  (540 files, 26,455 lines: everything except vendor/, .sim/, .testnet/, dist/, wallets, seeds, logs other than the
  research evidence, and the four upstream research clones now in .gitignore); `d76f4c9` adds `.gitattributes`
  (`eol=lf`, binaries marked) and `core.autocrlf=false` so a checkout can never change a template sha256.
  Rule from here: commit at the end of every loop that changes code; no remote is configured.

### 1. CRITICAL SECURITY

- **S16b Signer daemon: reorg detection and real-backend finality** · P0 before any real backend · the intent
  pipeline is done (S16); still missing: `FinalizedTip()` per backend (Zebra 100-block limit, Monero 10-block
  spendable age, Solana finalized commitment), re-verification of a deposit at policy depth before the Mint
  vote, and a stale-snapshot guard when a pass takes longer than a block · tools/vaultsigner · backends · add
  `Finalized(height)` to the Backend interface and refuse to vote on anything above it; re-read the deposit at
  vote time · — · — · a deposit that vanishes below policy depth is never voted · done when the sim backend can
  simulate a reorg and the test shows no vote.
- **S17 Proof canonicity** · P0 (before any swap use) · the SHA-256 gadget accepts `s + ℓ` preimages; a
  canonical-only decoder on the receiving side panics → fund loss · poc/proof-*/sha256_gadget.rs, README.md (Resolved) ·
  — · in-circuit `< ℓ` comparison; re-measure; assert-based negative tests; `batch_invert` fallible; MSM test at
  `n > 32768`; document variable-time · — · — · `cargo test` green · done.
- **S19 Encrypted withdrawal destinations** · P0 · plaintext `dest` on DERO · vault.bas, app, signer · — ·
  X25519 to the signer set, independent decryption and comparison, `Refund(j)` on disagreement · — · — ·
  substituted destination halts · done.
- **S20 Watcher quorum on observation; `output_index` in the tuple** · P0 · tools/vaultsigner, vault.bas · S1.
- **S21 Signer rotation and unbonding with epochs** · P0 · vault.bas · S2.
- **S22 Rate limits, per-deposit max, withdrawal delay with veto, circuit breaker** · P0 · vault.bas, signer · done in part (loop 13): `SetLimits(maxdep, winmax, wdelay)` owner-only before Seal — per-deposit maximum, rolling per-2000-block mint window, withdrawal delay before any Released vote counts (`tests/vault_limits_test.go`); still open: a k-of-n veto on a withdrawal and a circuit breaker on outflow rate; the reserve-shortfall Halt from the daemons is the breaker today.
- **S23 Published viewing keys and continuous reserve invariant; fee accounting and minimum withdrawal** · P0 ·
  without fees the reserve drifts below supply on every honest withdrawal; dust burns grief the federation ·
  vault.bas, reconciler · — · `minburn`, withdrawal fee retained in the reserve accounting, `pub_view`,
  reconciler loop · — · — · property test `reserve ≥ sup + pending fees` · done.
- **S24 DKG ceremony (no dealer), negation round, `nk`/`rivk` ceremony** · P0 · poc/frost-zec · —.
- **S25 Audits** · P0 · AUDIT_SCOPE.md; candidates Cypher Stack, NCC, Least Authority, Trail of Bits.

### 2. CRITICAL CORRECTNESS


- **C7 Registry/factory: dedupe, refund overpayment, treasury history** · P1 · `SetTreasury` delists every
  market in one tx; duplicates and garbage SCIDs registrable · factory.bas · — · `has_<scid>`; refund; append-only
  `tstr_<i>` accepted by the app; owner `Delist` · — · — · tests · **status 2026-09-06: open.**
- **C8 Pool first-deposit degenerate ratio; AddLiquidity off-by-one** · P1 · `(100000, 1)` bricks a pool ·
  pool.bas · — · `MIN_ASSET`/`MIN_K`; retry `l−1` · — · — · targeted search · done.
- **C9 Founders: `und` sweep by the first registrant; mint proceeds unescrowed; PayBatch gas on the `divlu`
  branch; settle under-payment; `price = 0` legal** · P1 · founders.bas · S5 · gate on `MIN_HST`; linear
  release; escrow with heartbeat; batch cap 8 or era reset; pay `rpool` and adjust `hd_`; reject `price 0` ·
  **status 2026-09-06: `minhst` gate done (loop 16, `tests/founders_test.go`); the rest open** (the trailing
  "done" on these rows was the done-when column, not a status).
- **C10 NFT: `holdblocks = 0` legal; lock/unlock farming; editions fungible; metadata URL-only** · P1 · nft.bas ·
  — · reject 0; per-address lifetime entitlement; `filehash`; document editions as a mint log ·
  **status 2026-09-06: reject 0 and lifetime entitlement (`mt_`) done (loop 16); `filehash` open (B2.6).**
- **C11 Social: follow churn grows state; free impersonation; sybil followers** · P2 · social.bas · — ·
  swap-delete on unfollow; name claim; app-side weighting · **status 2026-09-06: all open** (`Unfollow` decrements
  `fr_` but leaves `fl_`; names are free-form; see PLATFORM_BACKLOG B2.4/B2.5).
- **C12 Launch: Buyback sandwich, `tmode 2` tax stranded pre-graduation, creator premine unvested, overflow
  guard form, `p_<n>` unbounded** · P1 · launch.bas · — · `min_out` + size cap; phase-0 buyback; vest `alloc`;
  correct guard; ring-buffer points · **status 2026-09-06: Buyback capped at 1 % of the reserve per call and 20
  blocks apart (loop 16); the rest open.**
- **C13 Treasury key loss = permanent fee loss per market** · P1 · two-step transfer or fallback treasury ·
  pool/launch/nft/founders templates.

### 3. CRYPTOGRAPHIC SAFETY
- **K1 Sigma/R1CS binding review** · P1 (see S17). **K2 audited proof system decision** · P2. **K3 registration
  proof search** · [done] (treasury registered on the private testnet? see D5). **K4 no invented cryptography** ·
  standing.

### 4. TOKEN CREATION
- **T1 Dangerous settings visible** (allocation, tax, destination out of Advanced, plain-language consequences) ·
  P1. ~~T2 Creator allocation derived and shown everywhere~~ (done, loop 8). **T3 Presets as
  intents** · P2. **T4 Simulation before signing** · P1 (estimate shown; extend to per-field failure reasons).
  **T5 Post-launch checklist and failed-registration recovery** · P1. **T6 Rename "Verified" to "Standard
  contract" + risk chip** · P1.

### 5–9. NFT CREATION, PROGRAMMABLE ASSETS, HOSTING, METADATA, STORAGE
- **N1 Art integrity tiers**: generated mark (Tier 0) → on-contract restricted vector art (Tier 1, DOM built by the
  app, never parsed) → companion TELA-STATIC DOC by SCID + sha256 (Tier 2) → https URL permanently "unverified"
  with hash, dimensions and a 24–48 byte blur signature (Tier 3); `onerror` ladder on every image · P1.
- **N2 Do not widen the CSP to hash external images** · decision. **N3 Drop `data:` from img-src unless share
  cards need it** · P2. **N4 Programmable NFTs = deterministic state-derived traits, evolving tiers, committed
  reveal, a total non-Turing trait DSL, composition by reference; no parsed SVG/HTML, no execution** · P2
  (write into DECISIONS). **N5 `filehash` in nft.bas; document editions** · P1 (C10).

### 10–11. TRADING, CHARTS
- **G1 Log scale, zoom/pan, VWAP, graduation marker, average-cost line, volume ticks, unique gradient ids,
  memoised candles, `touch-action`** · P1 (chart.js has candles/OHLC/volume/MA20/crosshair already; ~5 KB
  headroom). **G2 Activity tab from `p_` deltas; rename Holders → Traders; supply composition bar; depth ladder
  (`Δd = rd(√f − 1)`)** · P1. **G3 Effective all-in price, curve-sell capacity message, max-size helper** · P2.

### 12–13. WALLET UX, TRANSACTION UX
- **W1 Pre-signature disclosure complete**: contract id and network fee rows in every sheet; max spent + refund +
  editable slippage on liquidity sheets; stake/unstake separated; re-read balances before building · P0/P1.
- **W2 First-run registration state; spendable-in-N-blocks after a mint** · P1. **W3 Wrap screen** · P1 (S18/S19
  first). **W4 Offline/stale state: backoff, "updated Xs ago", block confirm when stale** · P1.

### 14–16. DISCOVERY, PORTFOLIO, SOCIAL
- ~~F1 Anti-wash `hot`~~ (done loop 12: distinct publishers per hour instead of published-trade count, log-scaled reserve flow; the streak penalty is not implemented) · P1. **F2 Safety filters, duplicate
  badges, tag chips, creator page** · P1/P2. **F3 Portfolio: pools/LP, unclaimed value, fee/tax per trade,
  export/import, mask chart, watchlist-only balances** · P2. **F4 Follow-graph privacy disclosure at the point of
  action; chain-derived feed events; `keysstring` fallback for the social read (DoS today)** · P1.

### 17. PERFORMANCE
- **P1 Listener leak and per-block O(history) work**: `onChange` never removed; `enrich` on unchanged `sig`;
  `feed()` sorts all trades every block; sparklines rebuilt per row per block; `watch.all()` parses localStorage
  per row · P1 (largest single win). **P2 One RPC per market per block; indexer toggle unwired; timers run when
  hidden; no backoff** · P1. **P3 IndexedDB cache with quota handling** · P2. **P4 Measured per-screen budget**
  · P1.

### 18–19. MOBILE, ACCESSIBILITY
- **M1 Nested buttons in market rows; dialog without name/trap/restore; tabs without `aria-selected`; in-app
  reduce-motion ignored by the rAF number roll; sub-44 px targets on amount presets; chart inaccessible;
  assertive toast region** · P1 (the focus-escaping signing dialog is a safety issue).

### 20–22. API, INDEXING, DATABASE
- **I1 User-controlled indexing as a verifiable artefact** (Merkle-rooted per-market index, node-verified) · P2.

### 23. ECONOMIC SECURITY
- **E1 Cap ratio on chain** (S3). **E2 Unpaid public goods**: `Buyback`, `Distribute`, `PayBatch` run only when
  someone pays; wash-trading profitable in ranking, founders self-buy, social follows · P1. **E3 Founders bot is
  the only fee path; escrow mint proceeds** · P1 (C9).

### 24. ANTI-SCAM
- **A1 Impersonation** (S10), **A2 pool asset verification** (S11), **A3 risk chips**: LP-withdrawable
  liquidity, any creator tax, allocation > 0, repeat-creator history, FDV label, treasury-check fail-closed,
  template version shown · P1.

### 25. PRIVACY



### 27. OBSERVABILITY
- **O1 Status page on TELA** (invariant, signer liveness, queues, halt) · P1. **O2 Structured logs, metrics,
  supervisor units for the daemons; `Try*` RPC API instead of `log.Fatal`** · P1. **O3 Fee-transparency ledger
  page** (`fd` → claimed → `dep` → `paid`) · P2.

### 28. TESTING
- **Q1 Formal model** · [done] TLA+ VeilWrap, 8 invariants, 2.7 M states. **Q2 Fault-injection harness** · P0.
  **Q3 Second-language re-derivation** · P1. **Q4 Property invariants independent of the reference model**
  (emitted ≤ supply; `rd−real == vd0`; `ra+sold == vt0`; k non-decreasing; Σ pending ≤ rpool; fee on refund) ·
  P0. **Q5 Real `testing.F` fuzzers with corpora; vault/swap/nft/social fuzzers; `-race -shuffle`** · P1.
  **Q6 tools/ unit tests and CI** · P1. **Q7 Playwright injection and hostile-node/wallet stubs** · P1.
  **Q8 Model-to-code conformance** · P2.

### 29. DOCUMENTATION

- **X8 Settings copy: a ring-2 signer is public** · P1 · app/screens.js "Ringsize 2 is required for contract
  calls, so the contract sees your address" understates it: anyone can compute the signer of a ring-2 call ·
  app/screens.js:80 · P-RING · say "the address that paid is visible to everyone, not only the contract"; add the
  ring-16 option when P-RING ships · honesty · — · — · done with P-RING or as a copy-only change before it.
- **X1 DESIGN.md (Controls)** [done]. **X2 DESIGN.md (Pipeline specification)** [done]. **X3 RUNBOOKS/** [done loop 12: research/wrapper/RUNBOOKS.md ]. **X4 [done loop 12] README.md (Trust statement),
  README.md (Marketing copy)** · P0. **X5 AUDIT_SCOPE.md, BOUNTY_SCOPE.md** · P1. **X6 Regulatory note** · P1.
  **X7 Corrections**: DECISIONS §2.6 (unregistered send does not panic), §10.11 (`und` overflow is a loss),
  RISKS R-16…R-19 (secrets, supply chain, build, operator tooling; founders brick; init re-entry; vault stale
  votes; fee-on-refund); SECURITY_WRAPPER key-collision reasoning; RESOLVED §8 cite `.sim/sim.log` · P0.

### 30. FUTURE R&D
- **R1 FCMP++ key migration plan** · P1. **R2 Wrapped assets and USDT** · decision recorded below · P2 table.
  **R3 Solana escrow + companion** · P2. **R4 XMR swap integration** · P2. **R5 Oracle elimination**: replace
  `Date.now()` with chain time; derive block time; no fiat anywhere as written policy · P1. **R6 Share cards**
  (proof card for published trades only; private card with bucketed multiple; canvas + clipboard/share; no CSP
  change) · P2. **R7 Deterministic receipts, proof routes, self-verifying `#build` page** · P2. **R8 Archival
  bundles, user disaster-recovery doc (manual `sc_rpc` payloads), censorship-resistance test job, offline mode,
  creator reputation from chain state, asset health as published inputs, adversarial product suite** · P2/P3.

### Decisions recorded from the product audit

- **USDT:** never a synthetic dollar; never rendered as "$"; HTLC swaps are the preferred USDT path (issuer freeze
  degrades to a refund); a federation wUSDT only with an explicit issuer-freeze disclosure, a low cap, ≤ 4
  decimals and a published reserve address. No fiat denomination anywhere (already true, now policy).
- **Asset order to ship:** ZEC-t (HTLC, verified) → BTC/LTC → SOL/SPL → EVM stables → XMR swap → XMR federation
  → shielded ZEC.
- **Oracles:** Veil has no price oracle; the hidden ones are the device clock and the 18 s block-time constant
  (both removable) and image hosts (removable with on-chain art). The only true oracle is the federation's view
  of a foreign chain, bounded not eliminated.

### Rejected ideas (with reason)
- Trustless XMR/ZEC pool (impossible without DVM signature verification). Operator ledger (SHLD/fomo). Haven-style
  synthetics (oracle). Widening the CSP to hash external images (exfiltration channel). Arbitrary executable NFT
  content.

### Open questions for the operator (refreshed 2026-09-06, loop 12)
- Signer identities, hardware, jurisdictions. ZEC-first or wait for a Monero threshold scheme. Mainnet wallets and
  who holds the treasury key. Whether per-token comment threads are wanted (storage and moderation cost).
- Private mode default: ring 16 (3,336 bytes per trade) or 8 (2,720 bytes); and whether the app should refuse a
  private trade whose destination is the connected address (today it warns).
- The testnet TELA bundle was built before this afternoon's app changes; refresh it (28 DOCs, about 40 minutes of
  private-testnet blocks) now or with the next batch.


---

# Loop records (one per ~20-minute R&D loop; new loops append here)

## Loop status records

One record per autonomous R&D cycle (operator directive 2026-09-06, "UltraCode"). Newest at the bottom. Each
record has the fixed sections SECURITY, CORRECTNESS, PRODUCT, PERFORMANCE, RESEARCH, ARCHITECTURE, TODO,
UNCONSIDERED, NEXT. Numbers are measured or absent.

### Loop 0 — bootstrap (2026-09-06, afternoon)

#### SECURITY
- Wrapper Phase A review written (`research/wrapper/DESIGN.md (Mechanism review)`): federation, Yes-with-changes, nine
  mandatory changes; Zcash custody path (rerandomized FROST) is outside the NCC audit scope; Monero multisig is
  a trusted-signers tool; FCMP++ will force a vault key migration.
- Five reconnaissance audits launched in parallel (app, contracts/economics, tools/supply chain, wrapper
  adversarial, product/opportunities).

#### CORRECTNESS
- Private testnet: the wallet registration search was serialised inside walletapi (0.6 cores effective, >45 min);
  reimplemented with hoisted constants and per-goroutine state: 16 k attempts/s; the treasury registered after
  38 min (proof found at 35.9 M attempts).

#### PRODUCT
- none (bootstrap).

#### PERFORMANCE
- registration search 0.6 cores → 16 cores (16 364 attempts/s measured).

#### RESEARCH
- Primary sources added to `research/crosschain/SOURCES.md`: Monero multisig disclosure 2021-12-06, docs
  warning text, cryptonote_config constants, CCS !448 FCMP++, monero-oxide/FROSTLASS audit, Serai eVRF DKG audit,
  ZIP 312 (Draft), ZF FROST audit scope, Zebra RFC 0005 (100-block reorg limit), ZIP 316, six bridge post-mortems.

#### ARCHITECTURE
- The 20-minute loop runs the directive's six-step cycle (job 42622cd8) from `docs/history/HISTORY.md (earlier backlog)`.
- Multi-network build in place (deploy/networks.json, genconfig, netwallet, genesistx, patched derod).
- Formal model `research/wrapper/formal/VeilWrap.tla`: 8 invariants, 2,745,505 states, no error (TLC, `-deadlock`).
- `research/wrapper/DESIGN.md (Controls)` (CCSS L3, NIST 800-57/800-53, DKG ceremony, six bridge failures with the
  control that answers each) and `DESIGN.md (Pipeline specification)` (units, attribution, exactly-once release, reserve equation).

#### TODO
- `docs/history/HISTORY.md (earlier backlog)` v0.

#### UNCONSIDERED
1. A wallet registration proof takes minutes to an hour on a laptop: first-run UX must explain it.
2. The DVM renders addresses with the mainnet prefix on testnet: never compare raw strings across networks.
3. A freshly minted asset cannot be spent for ~2 blocks: every "you received X" needs a "spendable in N blocks".

#### NEXT
- Merge the five recon reports, then fix the top CRITICAL items.

### Loop 1 — audits merged, critical fixes (2026-09-06, evening)

#### SECURITY
- **Confirmed and fixed: post-install re-initialisation.** `Initialize`/`InitializePrivate` were callable after
  install on the real DVM (swap and board counters reset to 0; factory ownership resettable). Every contract now
  refuses a second call (`initd`); `tests/reentry_test.go`; full suite green. Template hashes changed.
- **Fixed: BigInt rounding in the signing path** (`JSON.parse` round-trip above 2^53 corrupted `burn` and
  `min_out`); exact serialiser on both sockets; verified byte-exact for 2^60+1.
- **Fixed in `app.tx`:** fee capped at 1 DERO and shown before the wallet prompt; one transaction in flight; wallet
  txid validated as a hash.
- **Vault v2** (`contracts/vault.bas`, 13,035 B, install gas 13,295): tuple-keyed mint votes (one signer can no
  longer freeze a deposit), slash epochs void a removed signer's votes, quorum floor, `Halt(want)` in 2000-block
  buckets, cap epochs, `cap ≤ 3 × Σ bonds` enforced, bonds before seal with a minimum, recipient = registered
  index address (no `ADDRESS_STRING` round-trip), `ftx` bound into the release vote, `minburn`/`wfee`/`feepool`,
  `sup` underflow guard, halted checks on Released/Attest, re-attestation with versions. `tests/vault_test.go`
  covers each. `research/wrapper/README.md (security review)` rewritten for v2.
- `.gitignore`: seeds, wallet backups, `.testnet/`, `.mainnet/`, `dist/`, Cargo targets; research logs un-ignored.
  `tests/run.ps1` now fails on failure. `ui.js` is text again (control bytes escaped).

#### CORRECTNESS
- Full contract suite green after the guard (227 s); UI read-only pass green on the patched app; DOC sizes
  unchanged in kind (app.js 9,176 B on chain).
- Correction accepted from the wrapper audit: the end-to-end burn/release did run (txids in `.sim/sim.log`); the
  cited `vault-e2e.log` stops at the first failed attempt. RESOLVED §8 now cites the right artefact.

#### PRODUCT
- none this loop (security first, per the directive).

#### PERFORMANCE
- none measured this loop (the frontend audit lists the per-block hot spots; P1 in the backlog).

#### RESEARCH
- Five audit reports merged into `docs/history/HISTORY.md (earlier backlog)` v1 (≈ 90 tasks across the 30 categories) with the USDT and
  asset-order decisions recorded.

#### ARCHITECTURE
- Vault votes are now epoch-scoped and tuple-keyed by design; every future vote family must follow the same shape.
- Decision: contracts changed after deployment on the simulator/testnet require `simdeploy -legacy <old hash>`;
  the private testnet is being redeployed with the guarded contracts (running).

#### TODO
- Next P0s in order: S5 founders accumulator brick, S6 launch fee-on-refund, S7 endpoint/CSP, S8 cache
  authentication, S16 signer daemon exactly-once, C1 transaction outcome detection, V1 images default off,
  T2 creator allocation shown, S13/S14 tooling and supply chain, D5 simulator redeploy.

#### UNCONSIDERED
1. A vote family that stores counters must always be keyed by an epoch that every removal bumps; otherwise any
   membership change silently lowers the threshold. This is a general DVM pattern worth a `contracts/README.md`
   rule.
2. The DVM refuses `Uint64 + String`; every key builder must start with a string literal. A lint over `.bas` files
   for `LET <str> = <uint>` patterns would have caught today's failure before the test did.
3. Heredocs with apostrophes break this environment's shell wrapper; file writes must go through the Write tool.
   Trivial, but it cost three failed attempts today.

#### NEXT
- S5 (founders accumulator) and S6 (launch fee-on-refund): both need a reference-model change plus an
  economic-invariant test that is independent of the model.

### Loop 2 — accumulator eras, fee on used DERO, ring-size measurement (2026-09-06, 13:30–14:30)

#### SECURITY
- **Fixed S5 (P0): reward accumulators can no longer brick.** `founders.bas` `distribute` closes an era when
  `acc + inc` would wrap (`ea_<era>` keeps the final value, `era` increments, `acc` restarts at 0); `settle` walks
  every era a holder missed (`he_<addr>`); `Register` stamps the era, `remove` deletes it. `launch.bas` `dist()`
  gets the same mechanism (`sera`, `sea_<era>`, `se_<addr>`) and no longer skips for good once
  `tacc·2^32 ≥ stot`: it carries `stot·(2^32−1)` and leaves the rest for the next call. Tests:
  `TestFoundersAccSaturation` (hst 2; two deposits of 6·10^9 wrap 2^64; claims across the boundary pay exactly;
  a holder who missed both eras is paid both; full withdrawal and re-registration land in era 2),
  `TestLaunchDistSaturation` (stot 1, tax 6.9·10^9: clamp, then wrap; a second staker joins in era 1; unstake
  deletes the era key). The founders fuzz model is era-aware (uint64 accumulator, era map, the clamp); 3,000
  random steps agree with the chain.
- **Fixed S6 (P0): the graduating buy charged fee and tax on the refund.** `SwapDeroForAsset` now derives
  `used = ceil(cost·10000/(10000−fee−tax)) + 1` (capped at `d`), recomputes fee and tax on `used`, refunds
  `d − used`, and records `used` in the trade entry. `TestLaunchGraduationRefundFee` checks from chain state,
  not the model: fee = bpsup(used), tax = bps(used), used = fee + tax + curve, curve within 2 units of the exact
  remainder price, fee < bpsup(d).
- Both contracts changed hash: founders.bas 12,586 B (install gas 12,984), launch.bas 14,642 B (15,177). The
  simulator deployment needs `simdeploy -legacy` (D5); the private testnet redeploy in flight installs the new
  sources.

#### CORRECTNESS
- Full suite green after the change (`tests/run.sh`, 54.6 s).
- v2 vault wrap/unwrap ran end to end on the simulator: vault `fd078359…`, two 25,000-unit deposits minted by
  2-of-3 votes (supply 50,000), burn 4,000 → withdrawal 3,900 (wfee 100) released against foreign txid
  `1376f9c3…`, status 1 (`research/crosschain/poc/vault-e2e-v2.log`, `vault-e2e-v2-burn.log`). The first run's
  burn hit the fresh-asset lag again (R-15): the script must wait two blocks after a mint before burning.
- Testnet: the owner `netwallet` stopped syncing at height 15 while the daemon reached 36 (its sync loop died
  silently; the treasury wallet kept up); a restart resynced at once. The redeploy failed on "wallet did not sync
  to 20" and was rerun with a 45-minute allowance (the private chain mines about one block a minute).

#### PRODUCT
- none shipped; the ring finding opens a private-trade entrypoint (P1 in the backlog).

#### PERFORMANCE
- Contract-call gas is independent of ring size (compute 310,800 / storage 154 at rings 2, 16, 32); a larger
  ring costs transaction size and wallet build time only (22 s, 25 s, 21 s wall-clock on the simulator, all
  block time).

#### RESEARCH
- **Measured (`tools/ringprobe`, `research/ringprobe.log`): DERO contract calls work at ring sizes 16 and
  32.** The DVM receives DEROVALUE() and string arguments intact and pays a destination named in the call;
  SIGNER() is 33 zero bytes (IS_ADDRESS_VALID = 0). derohe allows this on purpose ("allow anonymous SC
  transactions with condition that SC will not call Signer", blockchain/transaction_execute.go:296).
  `Extract_signer` works only on a ring-2 DERO payload, so the signer of every ring-2 call is computable by any
  observer, not only by the contract: every Veil trade today publishes the trader. A `dest`-argument entrypoint
  at ring 16 hides the payer among 16 accounts. Design and verification plan: `research/RING_PRIVACY.md`.
- SIGNER-paying entrypoints fail closed under a larger ring: every one of ours checks
  `IS_ADDRESS_VALID(SIGNER())` first (the probe's `valid = 0` is that value).

#### ARCHITECTURE
- `contracts/README.md` rules: an accumulator that only grows gets an era; a vote family gets an epoch; an
  entrypoint that pays SIGNER() requires ring 2 and is public; one that pays a named address may take any ring.

#### TODO
- MASTER_TODO: S5, S6 moved to done; new P-RING (signerless swap entrypoints, P1), D-WD (wallet sync
  watchdog in deploy scripts, P2), D-GIT (the repository has no commits, operator decision), X8 (settings copy:
  a ring-2 signer is public).

#### UNCONSIDERED
1. The trader's privacy in the DEX is weaker than the wallet's: a ring-2 contract call leaks the signer to any
   observer running the daemon's own function, so "unpublished" trades are pseudonymous, not private. The
   product copy should say so until signerless entrypoints ship.
2. A wallet sync loop can die while its RPC keeps answering; every script that waits on a wallet height needs a
   liveness rule (height must advance within a few block times) and a restart path.
3. A repository with zero commits and fifteen untracked top-level paths has no recovery point; one bad `rm` or
   script erases the project. Committing is the operator's call, and it is the largest single loss risk today.

#### NEXT
- S7 (endpoint validation + CSP connect-src) and S8 (cache authentication); then the
  `SwapDeroForAssetTo(dest, …)` spike at ring 16 through XSWD (the wallet RPC passes `ringsize` through,
  walletapi/rpcserver/rpc_transfer.go:79).

### Loop 3 — endpoint boundary, cache re-verification, the premine that starved the testnet (2026-09-06, 14:15–14:35)

#### SECURITY
- **Fixed S7 (P0).** `V.endpointURL` (app/rpc.js) is now the only path from a string to a socket: `host:port`
  or `ws://` only for 127.0.0.1 and localhost, `wss://` for anything else, `/ws` fixed, port required. It is
  applied to the stored setting on read and on write, to the wallet's `GetDaemon` (a refused endpoint falls back
  to the wallet proxy with one toast), and to the shipped default. CSP `connect-src` is
  `'self' ws://127.0.0.1:* ws://localhost:* wss: http://127.0.0.1:* http://localhost:*` — a plaintext remote
  socket is impossible even if the validator is bypassed.
- **Fixed S8 (P0).** The localStorage market cache is keyed by the full SCID (v3), shape-checked on read (types,
  arity, contiguous trade numbers, non-decreasing heights, canonical addresses, counts equal to the watermarks),
  and its newest 4 points and 4 trades are re-read from the chain before the rest is used; any mismatch removes
  the copy and refetches the window. No HMAC on purpose: a same-origin writer would hold the key too;
  re-reading the chain is the verifiable control.
- Settings copy corrected (X8): "anyone can compute which address paid a ringsize-2 call, not only the contract".

#### CORRECTNESS
- UI read-only pass green on the simulator after the change (Playwright, `PW_DIR=<a directory with playwright installed>`:
  every tab renders, node pill "Simulator · 5543", 13 markets in the switcher). `node --check` on the five
  changed files.
- **Testnet root cause found.** The owner wallet lagged the daemon by up to 20 blocks and its RPC blocked for
  minutes: the 100,000,000 DERO premine (10^13 atomic units) makes walletapi's balance decode
  (`balance_decoder.go` `Lookup`) walk about 4.8 M steps of 2^21 units on every balance change, and the owner's
  balance changes on every coinbase. Moved 99.9 M DERO to the treasury (tx `f47a974b…`; the treasury's balance
  changes only when it transacts), raised netwallet's lookup table to 2^23 (`vendor/bin/netwallet-next.exe`,
  swapped in at the next restart), and wrote `deploy/testnet-redeploy-when-synced.sh` (waits for the owner
  wallet to reach the daemon height, stops any stuck simdeploy, redeploys). Redeploy running.

#### PRODUCT
- none.

#### PERFORMANCE
- Cache re-verification costs 8 extra keys per market per session, fetched in the same batch as the
  incremental keys; refreshes from memory cost nothing extra.

#### RESEARCH
- DERO wallet economics: a balance of B atomic units costs B / table_size elliptic-curve steps to decode
  whenever it changes (no incremental path from the previous balance unless the balance is unchanged). Any
  genesis premine, faucet or treasury design must keep the frequently-changing balance small and park the bulk
  where it changes rarely. Belongs in deploy/README.md (Testnet) and mainnet.md.

#### ARCHITECTURE
- One validator for endpoints, one shape-check plus chain re-read for caches: both are single functions with
  no state, in the module that owns the boundary.

#### TODO
- MASTER_TODO: S7, S8 moved to done; D-WD updated with the cause.

#### UNCONSIDERED
1. The premine size was chosen for convenience (100 M) without asking what the wallet does with it; every
   number that lands in a wallet balance has a decode cost. The mainnet treasury will be small, but a
   Founders Pass treasury that receives fees every block is exactly the frequently-changing case.
2. A wallet whose RPC blocks for minutes looks identical to a dead wallet from the outside; deploy scripts need
   "is the wallet busy" (a fast method like GetAddress) separated from "is it synced".
3. The three copies of Playwright on this machine share one browser cache with different pinned builds; the
   test should print the browser path it needs when launch fails instead of the generic banner.

#### NEXT
- S9 (wallet trust boundary) or S16 (signer daemon exactly-once and intent log), whichever the redeploy leaves
  time for; record the testnet SCIDs when the redeploy completes.

### Loop 4 — money paths that survive a crash (2026-09-06, 14:25–14:45)

#### SECURITY
- **Fixed S16 (P0, sim backend).** `tools/vaultsigner` pays every withdrawal through a durable intent
  (paying → paid → voted) written with fsync + rename before the next step; `Release` is contractually
  idempotent per withdrawal index so a restart may repeat it; a corrupt state file stops the signer instead of
  starting it empty; state is keyed by signer address; an intent whose (dest, amount) differs from the chain stops
  with an operator message; `Reserve()` on the backend and a k-of-n `Halt(1)` vote when the foreign reserve is
  below the wrapped supply (once per 2000-block bucket); `-confs` defaults from the network table. Test:
  100 withdrawals with a crash injected after each durable step, refused votes and backend errors mixed in — each
  paid exactly once, every vote carries the recorded txid. Votes are at-least-once by design: the contract
  refuses a signer's second vote (`rv_<epoch>_<j>_<signer>`), payments are the thing that must not repeat.
- **Fixed S15 (P0).** `tools/treasurybot` keeps a journal: every claim is recorded before `ClaimFees` is sent
  and every deposit before `Deposit`; on start it reconciles open records against the chain and finishes them.
  Reconciling a claim needed a monotone witness — the unclaimed `fd` balance is none (new fees refill it
  between a crash and the restart; the first test caught exactly that with a 4 % shortfall) — so `launch.bas`
  and `pool.bas` now keep `fc`, the cumulative fees claimed, moved only by `ClaimFees`. Test: 100 sweeps over
  three markets with crashes after either send, 139 claims and 52 deposits, the pass received the exact share
  minus at most one unit per deposit.
- **Fixed S9 (P0).** The wallet boundary: an address the wallet returns that does not canonicalise ends the
  connection; every amount from the wallet passes `V.toBig` (JSON number or decimal string, else null) instead
  of a throwing `BigInt`, so a hostile or buggy wallet cannot kill the Portfolio screen.

#### CORRECTNESS
- v2 vault wrap/unwrap ran again with the intent-logging signer: third deposit minted (supply 71,000), burn
  4,000 → withdrawal 1 released at 3,900 with intent state `voted` on disk
  (`research/crosschain/poc/vault-e2e-v2-intent.log`, `.sim/vaultsigner-30004.json`).
- Contract suite: rerun after the `fc` counter (result recorded in the next loop if not green here).
- Testnet: the owner wallet spent 10 minutes on one block at the old balance; restarted on the 2^23-table
  binary to shorten the remaining slow blocks; the redeploy waits on it.

#### PRODUCT
- none.

#### PERFORMANCE
- none measured.

#### RESEARCH
- A reconcile step needs a witness that only moves one way. Balances that can be refilled (fd, wallet
  balances) are not witnesses; cumulative counters (`fc`, the pass's `dep`, the vault's `sup`) are. Rule added
  to `contracts/README.md`.

#### ARCHITECTURE
- Both bots now share one shape: record intent → send → record outcome → reconcile on start against a monotone
  chain counter. The signer's `Backend` and the bot's `chain` interfaces are narrow enough to fake in tests.

#### TODO
- S9, S15, S16 to done; S16b (reorg/finality for real backends) opened; template hashes of launch.bas and
  pool.bas changed again (D5).

#### UNCONSIDERED
1. The pass's `dep` counter is open to anyone: a stranger's deposit between a crash and a restart can mask a
   failed treasury deposit (the holders lose that share). A per-depositor counter (`dep_<addr>`) would close it.
2. A bot that stops with `log.Fatal` on any send error is correct but noisy for a transient RPC failure; the
   journal makes a retry loop safe, so the next version can retry instead of exiting.
3. The vault's `Halt` vote from the signer is one-shot per 2000-block bucket; if a shortfall persists across
   buckets and the quorum was not reached, the signer votes again — good — but nothing un-halts automatically
   once the reserve recovers; that is an operator decision by design and should be in the runbook.

#### NEXT
- Record the suite result and the testnet SCIDs; then S10 (anti-impersonation) or S11 (pool asset behind the
  shield), the two remaining app P0s.

### Loop 5 — impersonation, the pool behind the shield, and a wallet that should never have mined (2026-09-06, 14:35–14:55)

#### SECURITY
- **Fixed S10 (P0).** `V.ui.tickerKey` folds a ticker to a skeleton (NFKC, upper-case, Cyrillic and Greek
  capitals mapped to Latin, 0→O, 1→I, then [A-Z0-9] only: "VЕIL", "Ⅴ𝐄IL", "vei l" and "VEIL" all fold to VEIL,
  "B0NK" to BONK). Every listed market in a group of two or more carries "Ticker shared"; a ticker whose
  skeleton differs from its plain upper-case form carries "Look-alike ticker"; both show in the token header,
  the trade sheet and as a mark in the markets table. A launch and its own pool are exempt (they share the
  ticker by design). `#token/<id>` refuses anything but a 64-hex id. A contract reached by id that the registry
  does not list is rendered "Code verified · not listed", enriched in place, and never added to the market
  lists. The trade sheet shows the contract id.
- **Fixed S11 (P0).** Two-tier badge: a pool whose asset is not a Veil launch shows "Pool verified · asset
  unknown" in amber, inherits no name or icon, its details say the asset is not a Veil launch and give the asset
  id with a copy button; the pool form explains the badge before creation.

#### CORRECTNESS
- UI read-only pass green after each of the three app changes (S9, S10/S11, the pool exemption).
- Testnet: the owner wallet answers `GetHeight` but a `GetBalance` never returned within 150 s even with the
  small balance and the 2^23 table, while the height still advanced in bursts; the redeploy is progressing
  through it (factory, social, founders installed by 14:45). A fresh deployer wallet
  (`deto1qypk4g4q…`, `.testnet/deployer.db`, rpc 40405) is registering (65 k attempts/s); it will be the deploy
  owner from now on so that the miner's wallet never sits in a transaction path again.
- D5 on the simulator: `simdeploy` rerun with the existing factory, social and founders reused and the previous
  template hashes kept as legacy, so `app/config.js` now verifies both the deployed (old) contracts and anything
  launched from the new templates.

#### PRODUCT
- Honest badges are product: "Verified" no longer means "safe to trade" for a pool over an arbitrary asset.

#### PERFORMANCE
- The skeleton grouping is O(markets) per decorate; nothing measurable.

#### RESEARCH
- The wallet stall is not the balance decode alone: `GetHeight` answers while `GetBalance` blocks, so the
  wallet's balance path holds a lock for minutes on every change even at 10^10 units. Left as an observation
  (walletapi internals); the mitigation is architectural: no deploy or bot wallet may be a mining or integrator
  address.

#### ARCHITECTURE
- Rule for every deployment doc: the miner/integrator wallet, the treasury wallet and the deploy wallet are three
  different wallets; only the deploy wallet transacts during a deploy.

#### TODO
- S10, S11 to done. Remaining P0 in §1: S13 tool secrets, S14 supply chain, S16b finality, S17 proof
  canonicity, S18–S25 wrapper items; correctness C1/C2; product V1/T2.

#### UNCONSIDERED
1. A ticker skeleton is only as good as its table; the app should show the raw code points of a ticker on
   demand (a "characters" tooltip) so a user can see a homoglyph the table missed.
2. "Not listed" contracts reachable by id are a phishing surface even with the amber badge; a link shared in
   chat lands on a page that looks like every other market. The page should carry a full-width warning box,
   not only a badge.
3. Every unencrypted seed the tools write (`.testnet/deployer.seed` today) is a habit that will one day run on
   mainnet; S13 should land before any mainnet wallet is created.

#### NEXT
- Finish the testnet redeploy with the deployer wallet if the owner-wallet run does not complete; then S13
  (tool secrets and operator rails) before any more wallets are made.

### Loop 6 — operator rails, images opt-in, the deployer wallet (2026-09-06, 14:50–15:05)

#### SECURITY
- **Fixed S13 (P0).** netwallet prints the recovery seed once unless `-write-seed` (refused on mainnet), takes
  the password from `VEIL_WALLET_PASSWORD` when none is given and refuses an empty password on mainnet;
  genesistx restores the seed into a private temporary directory under a random password and removes it;
  xswdwallet (the auto-approving test wallet) refuses any daemon that is not loopback or whose `DERO.GetInfo`
  is not a test network, and says at start-up that it listens on every interface; treasurybot is a dry run on a
  mainnet config unless `-yes` (and `-dry-run` anywhere); vaultsigner refuses mainnet without `-yes`. All three
  vendored tools compile; both bots' tests green.
- **V1 shipped.** Creator-hosted images are off by default (`images: false`); the toggle text already explains
  that loading one tells that host your IP address.

#### CORRECTNESS
- The old-owner redeploy spent 7 minutes on one contract and was stopped; the deploy scripts now use the
  deployer wallet (`-w0 127.0.0.1:40405`, funded with 50,000 DERO from the treasury, answers `GetBalance` at
  once); the redeploy is running from it. Rule recorded in the script header: the miner/integrator wallet never
  transacts.

#### PRODUCT
- Images opt-in is a privacy default, not a feature; the settings toggle stays one tap away.

#### PERFORMANCE
- none measured.

#### RESEARCH
- The deployer wallet registered in 2 m 16 s at 65 k attempts/s (the earlier 38-minute registration ran on a
  loaded machine at 16 k/s): registration cost is a function of the machine, not of the chain.

#### ARCHITECTURE
- Three wallets per deployment, three roles: miner/integrator (never transacts), treasury (receives fees, rarely
  sends), deployer (sends every install and registration). deploy/README.md (Testnet) must say so.

#### TODO
- S13, V1 to done. Remaining P0: S14 supply chain, S16b finality, S17 proof canonicity, S18–S25; C1/C2; T2.

#### UNCONSIDERED
1. `guardTestOnly` trusts the daemon's own `DERO.GetInfo`; a malicious local daemon could lie. The real guard is
   the wallet file: an auto-approving wallet should refuse to open a wallet whose network flag is mainnet, which
   walletapi exposes at open time.
2. The seed printed to a console is captured by terminal logs and screen recorders; the mainnet runbook needs a
   "clear the scrollback" step, or the tool should print the words with a pause and then clear them.
3. Funding the deployer from the treasury put the treasury wallet through a one-minute decode; on mainnet the
   treasury will hold far less than 100 M DERO, but the pattern (fund once, then never touch) is the one to keep.

#### NEXT
- Record the testnet SCIDs when the deployer run completes; then S14 (vendor pins + verifyvendor) or C1
  (transaction outcome detection in the app).

### Loop 7 — outcomes, not inclusion; swaps that hide the payer; the testnet redeployed (2026-09-06, 14:45–15:00)

#### SECURITY
- **P-RING contracts shipped.** `SwapDeroForAssetTo(dest, min_out, publish)` and `SwapAssetForDeroTo(dest, …)`
  in launch.bas and pool.bas. Each classic entrypoint is now a six-line wrapper that resolves `SIGNER()` and
  fails closed when it is blank; each `…To` wrapper resolves `ADDRESS_RAW(dest)`; both call internal
  `buyto`/`sellto` that do the maths and pay the recipient and never read the signer. `tests/ringswap_test.go`
  drives both contracts with a nil signer (the harness renders it as the DVM does, 33 zero bytes): the classic
  names refuse, the `To` names pay the named address, a bad destination is refused with nothing moved, records
  name the destination; the models agree unchanged. Suite green (56 s).

#### CORRECTNESS
- **C1 fixed and verified.** `app.tx` distinguishes not-in-a-block (4.5 min), included-but-not-executed and
  executed. For trades the asset balance is read from the wallet before and after (after the wallet reports the
  block): buy = up, sell = down; a P&L entry is written only when executed. UI wallet pass on the simulator: the
  XSWD log shows GetBalance → transfer → GetHeight → GetBalance; the screenshot after the buy reads
  "Buy BRW confirmed" with the trade in the feed; zero console errors.
- **Private testnet redeployed from the deployer wallet** (factory `b0bbd7cc…`, social `a5f3d33d…`, founders
  `f8271d08…`; template hashes at install time equal the current files, ring entrypoints included). The whole run
  took about 12 minutes once the wallet in the transaction path was one that never mines. `dist/testnet` (28
  DOCs) built; TELA install running.
- Simulator config regenerated once more with the split templates; every earlier hash stays accepted.

#### PRODUCT
- A trade now ends in one of three honest states; the old single "confirmed" is gone.

#### PERFORMANCE
- Outcome detection costs two wallet balance reads and a height poll per trade; nothing on the node.

#### RESEARCH
- The `…To` split cost 8 lines per contract and no gas worth measuring; the privacy gain is bounded by what the
  user names as `dest` (research/RING_PRIVACY.md §4). The app decides the default; a second address of the
  user's own is the only default that hides anything.

#### ARCHITECTURE
- Contract rule: an entrypoint that pays someone takes the recipient as a parameter of an internal function;
  the public names only decide where the recipient comes from. Every future payout entrypoint follows it.

#### TODO
- C1, P-RING (contracts) to done; P-RING-APP opened; testnet SCIDs recorded in deploy/networks.json and
  deploy/README.md (Testnet).

#### UNCONSIDERED
1. A `…To` call whose `dest` is the caller's own address is worse than a ring-2 call for privacy: the ring
   hides the payer but `dest` names the same person in clear. The app must never default `dest` to the
   connected address.
2. The balance-probe outcome detection assumes the wallet's balance at the block reflects the trade; a second
   trade of the same asset by the same wallet in the same block would confuse it. The in-flight lock already
   prevents that from this app; another app on the same wallet could not be stopped.
3. 28 TELA installs at one block a minute is half an hour of a private testnet's time; a bundle format that
   packs several small files into one DOC would cut installs by two thirds.

#### NEXT
- P-RING-APP (ring 16 through XSWD with a destination field) after the TELA install completes; then S14
  (supply chain) or T2 (creator allocation shown).

### Loop 8 — private mode in the app, the launch form told the truth, the allocation shown (2026-09-06, 15:00–15:08)

#### SECURITY
- **P-RING-APP shipped.** The trade sheet has "Private mode: hide who paid": ring size 16 and a "Receive at"
  address. `app.tx` takes a ring option that only the `…To` entrypoints may use; `app.trade` builds
  `SwapDeroForAssetTo` / `SwapAssetForDeroTo` with `dest`; the outcome is verified by balance only when `dest`
  is this wallet and by inclusion otherwise; a trade paid elsewhere never enters this wallet's P&L; the sheet warns
  when `dest` is the connected address (the ring would hide the payer, the receiver would be the same person
  in clear). Verified end to end: the UI wallet pass with `--dest=<simulator wallet 30006>` bought through XSWD
  and the node reports a 16-member ring on that transaction.
- **C2 fixed.** One `readForm()` feeds the launch preview and the submit with the limits `InitializePrivate`
  enforces; a value the contract would refuse never reaches the wallet, and the reason shows in the preview.

#### CORRECTNESS
- The first private-mode run flagged one false failure: the test read the sheet with `innerText`, which
  excludes the collapsed details section where the ring size is printed; `textContent` fixes the check.
- TELA install of the testnet bundle in progress (3 of 28 DOCs after 10 minutes; the bundle predates this
  loop's app changes and will need a refresh of the changed DOCs).

#### PRODUCT
- **T2 shipped.** The creator allocation is derived (S − cs − Lq) and shown on the token page and as a risk
  line from 1 % up, wherever risks are shown.

#### PERFORMANCE
- none measured; the ring-16 transaction took the same block time as a ring-2 one on the simulator.

#### RESEARCH
- DERO's wallet builds a 16-member ring for a contract call without complaint through XSWD; the remaining
  measurements are tx size and build time per ring (RING_PRIVACY §6.4).

#### ARCHITECTURE
- The app's privacy story now has two explicit modes, each with one sentence of consequence next to the control.

#### TODO
- P-RING-APP, C2, T2 to done. Remaining P0/P1 of note: S14 supply chain, S16b finality, S17 proof canonicity,
  S18–S25 wrapper, C3 metric truthfulness, D-GIT.

#### UNCONSIDERED
1. Private mode with `publish` on writes `dest` into the public trade record; the sheet should grey out
   "Post this trade" in private mode or say plainly that publishing names the receiver.
2. The `…To` entrypoints let anyone buy tokens for someone else; a spam of dust buys to a victim's address is a
   new nuisance vector (also on DERO natively). Not a security issue, but the feed's "bought" lines could be
   attributed to people who never acted; the record should mark `dest` trades as "delivered to".
3. Ring 16 costs the wallet decoy lookups; on a chain with few registered accounts (a fresh private testnet has
   three) a ring of 16 cannot be built at all. The app should read the wallet's error and fall back with a
   clear message instead of a generic wallet failure.

#### NEXT
- Mark `dest` trades in the record (UNCONSIDERED 2) and grey out publish in private mode (1); then S14.

### Loop 9 — the vendor tree pinned, and the honest toast that broke the tests (2026-09-06, 15:08–15:15)

#### SECURITY
- **Fixed S14 (P0).** `deploy/vendor.lock` pins the five vendored checkouts (derohe e9df1205, civilware derohe
  433c4559, tela aa892d2f, Engram 12b6e61a, documentation 5f689fee) with origin URLs and the exact local changes
  allowed: the derod private-genesis patch stored as `deploy/patches/derod-private-genesis.patch`, one file whose
  name Windows cannot check out, and the directories our tools are built from. `tools/verifyvendor` checks the
  commit, the origin, every modified/deleted/untracked path, the patch bytes (CR at end of line ignored) and
  the sha256 of every `vendor/bin/*.exe` against `deploy/binaries.lock`. A one-line tamper in a vendored README
  turned it red; the restore turned it green. Every build script passes `-trimpath`. Found on the way: the derohe
  checkout's index held 4,941 staged deletions from a broken clone step; `git reset` (index only) repaired it.

#### CORRECTNESS
- The C1 wording ("included in block N" for a call whose execution the app cannot verify) broke five headless
  test expectations that waited for "confirmed" on profile, follow, stake, mint and register steps: the honest
  wording stays and the test accepts either; the private-buy check now also asserts that the node extracted no
  signer from the ring-16 transaction (`GetTransaction.signer` is empty above ring 2).
- The wallet pass with a private buy is rerunning with the corrected checks (the first run proved the ring:
  `[16]`).

#### PRODUCT
- none.

#### PERFORMANCE
- verifyvendor runs in about two seconds over 4,900 tracked files (git status once per repo, no per-file
  `git show`).

#### RESEARCH
- `DERO.GetTransaction` carries `signer` ("if signer could be extracted") and `ring`: the app can show, for any
  transaction, whether the network could name its payer. A "what this transaction reveals" line in the wallet
  history would make the ring choice tangible.

#### ARCHITECTURE
- Supply-chain rule: a vendored tree may differ from its pinned commit only by files named in the lock, and a
  patch is a file in the repository, never an in-place edit remembered by nobody.

#### TODO
- S14 to done. Open P0: S16b finality, S17 proof canonicity, S18–S25 wrapper items; C1b state-key probes for
  non-trade calls (so "included in block" becomes "confirmed" where a contract key proves execution).

#### UNCONSIDERED
1. The tela checkout the app is served by pins an older civilware derohe than the wallet fork; two DERO
   implementations in one deployment is a consensus-drift risk for anything that parses transactions.
2. `binaries.lock` records what was built on this machine, not what a clean build produces; reproducibility
   needs a second machine or a container build to compare against.
3. The launch flow's second transaction (register) is a call without a probe: a launch whose registration
   reverted would show "included" and never appear in the registry. C1b should start there.

#### NEXT
- C1b for RegisterLaunch/RegisterPool (probe: the factory's counters), then S16b.

### Loop 10 — every call names its proof; the ring measured; the simulator refilled (2026-09-06, 15:15–15:35)

#### SECURITY
- none new; the C1b probes close the last "confirmed without evidence" toasts for the calls that have a
  contract key to check.

#### CORRECTNESS
- **C1b shipped and verified.** A call names the contract keys that must change (`probe: { scid, keys }`);
  `app.tx` reads them before and after inclusion and says "confirmed" only when one changed. Wired for
  RegisterLaunch/RegisterPool (factory `lc`/`pc`), Stake/Unstake (`stot`, `st_<me>`), Follow/Unfollow
  (`fc_<me>`), pass Mint (`minted`), pass Register (`hst`). The full wallet pass ran green with the toasts as
  evidence: "Install launch confirmed" (code landed), "Register on the factory confirmed" (probe), "Buy BRW
  confirmed" (balance), "Buy BRW → deto1qyx…ywu80 included in block 5769" (private buy, inclusion only, ring 16,
  no signer extractable), "Update profile included in block 5770" (no probe: honest), "Stake confirmed",
  "Mint 1 pass confirmed", "Register passes confirmed" (probes).
- The mint step had failed twice before for a reason outside the code: the simulator's test wallet was drained
  to 3 DERO by a day of runs and the pass costs 5. The Founders Pass was redeployed on the simulator at 0.5 DERO
  (new SCID, era-capable contract) and the wallet refilled from the richest simulator wallet (6.9 DERO). The
  headless test now writes the page console on failure, which is how the cause was read.

#### PRODUCT
- none.

#### PERFORMANCE
- Ring sizes measured (`research/ringprobe-sizes.log`): 1,848 B at ring 2, 2,720 at 8, 3,336 at 16, 4,240 at 32,
  5,784 at 64 — about 63 bytes per member, block time unchanged, gas unchanged; the node reports a signer only
  at ring 2.

#### RESEARCH
- `DERO.GetTransaction` exposes `signer` and `ring`: the app can tell a user, per transaction, whether the network
  could name the payer. Recorded in RING_PRIVACY §6 and RESOLVED §10.

#### ARCHITECTURE
- The mainnet runbook gained three gates (verifyvendor green; the installing wallet never mines; `-yes` on every
  fund-moving tool) and the three-wallet rule.

#### TODO
- C1b to done; C3 (metric truthfulness) in progress: curve liquidity = real DERO, FDV labelled FDV, published
  counts not double counted, proportional reserve moves on pools excluded from volume, buy pressure from reserve
  flow.

#### UNCONSIDERED
1. A test economy drains: every headless run spends real simulator DERO and nothing mints new coins for the test
   wallets. A faucet step in the test harness (refill from the richest wallet when below a floor) would have
   saved two failed runs.
2. Probe keys are strings assembled in the app (`"st_" + me()`); a contract that renames a key silently turns
   "confirmed" into "did not execute". The probe keys should come from one table next to the template hashes.
3. The private buy's record on chain names `dest`; the feed will attribute a "bought" line to an address that
   never acted. The feed should render `To` records as "received", not "bought".

#### NEXT
- Finish C3, then D-GIT is the operator's; S16b/S17/S18–S25 remain for the wrapper.

### Loop 11 — metrics that mean what they say, and the vault's supply under random fire (2026-09-06, 15:35–15:50)

#### SECURITY
- **C4 done.** `tests/vault_fuzz_test.go`: 600 random steps over a sealed 2-of-3 vault — mint votes with lying
  signers mixed in, burns above and below the minimum, releases, halt/resume flips, cap moves — and after every
  step `sup == Σ minted − Σ burned`, `feepool == Σ wfee`, every withdrawal record equals its burn minus the fee,
  the vault's DERO balance equals the bonds, `sup ≤ cap`. The model predicts no policy; it observes what landed.
  One run: 94 mints (136 refused), 70 burns (97 refused), 61 halt flips.
- S1–S4 (vault v2, done in loop 1) were still listed as open; recorded as done with their evidence.

#### CORRECTNESS
- **C3 done.** A curve's liquidity is the DERO it actually holds (`real`), not the virtual reserve (4.7× less
  on a fresh launch, which now trips the thin-market flag as it should); "FDV" is labelled FDV; published counts
  are published counts (no double count with reserve flow); a proportional reserve move on a pool (liquidity
  added or removed) is not volume; buy pressure comes from reserve-flow blocks, so unpublished trades count;
  "Public traders" instead of "Holders". UI read-only pass green.

#### PRODUCT
- The numbers a trader sees on a fresh launch got smaller and truer.

#### PERFORMANCE
- none measured.

#### RESEARCH
- none this loop.

#### ARCHITECTURE
- Metric rule: a number on screen is named for what it measures (public trades, reserve-flow blocks, FDV) —
  never for what a reader might hope it measures.

#### TODO
- C3, C4, S1–S4 to done. TELA install on the testnet at 16 of 28 DOCs.

#### UNCONSIDERED
1. `holders` (distinct publishers) still feeds sorting somewhere; a market with one loud publisher ranks as
   "popular". Rank by reserve-flow blocks instead.
2. The conservation fuzz never slashes; a slash changes bonds and the DERO-balance invariant. A second fuzz with
   slashes and re-bonds would cover S21's territory before it is built.
3. The proportional-move filter uses a 1e-6 relative price tolerance; a very large pool could see a genuine
   tiny trade below it and lose it from volume. Acceptable, but the tolerance should scale with the reserve.

#### NEXT
- C5 zero-float lint in the test runner; then the wrapper items that need real backends stay parked until a
  foreign node is available.

### Loop 12 — a burn is paid only when DERO calls it final (2026-09-06, 15:50–16:05)

#### SECURITY
- **C6 done.** `tools/vaultsigner` pays a withdrawal on the foreign chain only when the burn's DERO height
  (`w_<j>_h`, stored by `Burn`) is at or below the daemon's `stableheight` (8 blocks behind the tip on the
  simulator and the private testnet); above it the pass logs "waits for DERO finality" and moves on.
  `withdrawalFinal` + `TestWithdrawalFinality`; signer tests green; `simrpc.StableHeight` added.
  Live on the simulator (`research/crosschain/poc/vault-e2e-v2-finality.log`): the burn at height 5805 was met by
  "withdrawal 2 waits for DERO finality (burned at 5805, stable height 5798)" on the first signer pass; the e2e
  script now polls `stableheight` before the release passes.
- C5 done: the test runner refuses `float32`/`float64` in the money code (reference, harness, both bots); a
  planted float turned it red, its removal green.

#### CORRECTNESS
- Liquidity sheets use the user's slippage setting (was a fixed 1 %), refresh holdings, and prove their outcome
  through the pool's `lp` key.
- TELA install of the testnet bundle continues (18 of 28 DOCs at 15:55).

#### PRODUCT
- none.

#### PERFORMANCE
- The finality wait adds about 8 simulator blocks (2–3 minutes) between a burn and its foreign payment; on
  mainnet that is DERO's own stability rule, not a Veil constant.

#### RESEARCH
- DERO exposes `stableheight` in `DERO.GetHeight`; it is the right anchor for every cross-chain payout and for
  the app's "confirmed" wording on high-value calls (unexplored).

#### ARCHITECTURE
- Rule for the wrapper: nothing leaves the federation's foreign wallet for a DERO event above the stable height.

#### TODO
- C5, C6, the liquidity-sheet item to done. Operator questions refreshed (first commit, ring default, testnet
  bundle refresh).

#### UNCONSIDERED
1. The e2e vault on the simulator is near its cap (92,000 of 100,000); the next run's mint will be refused by
   design and the script should raise the cap with two `SetCap` votes first.
2. The signer reads `stableheight` once per pass; a pass that takes longer than a block could pay a withdrawal
   that became stable during the pass — harmless — or, in a reorg during the pass, one that was stable and is no
   longer: re-reading the burn record before paying (S16b) closes that.
3. Finality on the DERO side is one half; the foreign side's finality per backend (S16b) still decides when a
   deposit may be minted.

#### NEXT
- S16b foreign finality per backend once a foreign node exists; until then the app-side items (feed rendering of
  `To` records, hot ranking by reserve flow).

### Loop 13 — limits a quorum cannot lift, and a daemon that cannot estimate every contract (2026-09-06, 16:05–16:25)

#### SECURITY
- **S22 in part.** vault.bas `SetLimits(maxdep, winmax, wdelay)`: owner-only before `Seal`, refused by everyone
  afterwards; a deposit above `maxdep` is refused at the k-th vote; mints per 2000-block bucket are capped by
  `winmax`; a `Released` vote does not count before `wdelay` blocks after the burn. Defaults are 0 (off), so
  every installer is unchanged. `tests/vault_limits_test.go` covers each refusal and the next bucket; the
  conservation fuzz and the federation test still pass; contract 13,825 B, install gas 14,091. Still open in
  S22: a k-of-n veto on a withdrawal and an outflow-rate breaker.

#### CORRECTNESS
- **A one-in-256 flake in the harness, and the same bug in the daemon.** graviton refuses a tree whose name
  starts with ':' (`snapshot.go:162`). The test harness named trees after random 32-byte ids, so about one
  install in 256 panicked "Bucket cannot start with ':'" — the full suite failed once today on
  `TestLaunchInstallValidation` and passed on rerun; `randHash` now never starts with ':'. The real daemon has
  the same failure in `DERO.GetGasEstimate`: it simulates the install under an id derived from the content, and
  for `templates/founders.bas` that id starts with ':' every time — `telainstall` retried five times and gave
  up 21 DOCs into the testnet install. Fixes: `telainstall -reuse <file>` points at DOCs already on chain
  instead of reinstalling them, and a DOC the daemon cannot estimate is sent with a size-based fee (code size +
  512; measured overhead is +348). The resumed install is running against the 21 installed DOCs.
- Full suite green after the harness fix (53.8 s).

#### PRODUCT
- none.

#### PERFORMANCE
- none measured.

#### RESEARCH
- The daemon's estimator is not total: some contract contents cannot be estimated at all. Every installer that
  relies on `GetGasEstimate` (the app's launch and pool forms, simdeploy, telainstall) needs the size-based
  fallback, or a user will meet an install that "would fail" for no reason they can see. Recorded as a risk.

#### ARCHITECTURE
- Rule: never let a helper derive a graviton tree name from arbitrary bytes without checking the first byte.

#### TODO
- S22 (part) to done; RISKS: the estimator finding; app-side fallback for the launch/pool forms queued.

#### UNCONSIDERED
1. The app's `app.tx` refuses when the estimate fails ("would fail"); for a launch whose template hits the
   estimator bug the user is stuck with no explanation. The size-based fallback belongs in the app too, behind
   the same 1 DERO cap.
2. `winmax` per 2000-block bucket resets on the bucket boundary, so a quorum can mint two windows' worth across
   one boundary in a few blocks. A sliding window (two buckets summed) closes it cheaply.
3. `wdelay` gives honest signers time to halt after a bad burn only if they watch burns; the daemon watches
   the reserve, not individual burns. A "burn looks wrong" heuristic (a withdrawal larger than N % of the
   reserve) could trigger a halt vote too.

#### NEXT
- App-side size-based fee fallback (UNCONSIDERED 1); sliding mint window (2); then the operator's decisions.

### Loop 14 — the window slides, the daemon refuses the outsized burn, the app fees what it cannot estimate (2026-09-06, 16:25–16:45)

#### SECURITY
- **S22, two more pieces.** The vault's mint window now slides (this 2000-block bucket plus the previous one),
  so a bucket boundary alone never opens a fresh window: 300 refused 2000 blocks after a full window, accepted
  4000 blocks after (`tests/vault_limits_test.go`); vault.bas 14,010 B, install gas 14,276; full suite green
  (55 s). The signer daemon refuses to pay one withdrawal above `-max-withdrawal-bps` (default 20 %) of the
  foreign reserve, logs `SUSPICIOUS WITHDRAWAL`, votes `Halt(1)` once per bucket and leaves the intent for a
  human (`suspiciousWithdrawal`, exact integers, tested at the boundary and with a zero reserve). Still open in
  S22: a k-of-n veto on a specific withdrawal.
- **R-16 in the app.** When the node cannot estimate an install (the graviton ':' panic), `app.tx` sends it with
  a size-based fee under the 1 DERO cap and says so, instead of "would fail". UI read-only pass green.

#### CORRECTNESS
- The resumed TELA install reused the 21 DOCs and installed `templates/founders.bas` with the size-based fee
  (the daemon accepted it: gas 7,006 for 6,630 bytes on chain), then `launch.bas`; 23 of 28 at 16:40.

#### PRODUCT
- none.

#### PERFORMANCE
- none measured.

#### RESEARCH
- A halt heuristic is only as good as its threshold; 20 % of the reserve is a guess that a live federation must
  replace with a number from its own history of honest withdrawals.

#### ARCHITECTURE
- Two layers of outflow control now exist: on chain (`SetLimits`: per-deposit, sliding window, delay) and in the
  daemon (reserve shortfall, outsized withdrawal → halt). The chain layer cannot be changed after `Seal`; the
  daemon layer can be tuned per signer.

#### TODO
- S22 sliding window and daemon heuristic to done; the veto stays open.

#### UNCONSIDERED
1. `-max-withdrawal-bps` is per signer: a colluding quorum runs daemons without it. The on-chain delay is what
   gives honest signers time; the heuristic only helps the honest ones notice.
2. The sliding window doubles the memory of the limit but not its shape: a quorum can still mint `winmax`
   every 4000 blocks forever. The cap bounded by bonds is the only absolute limit.
3. The size-based fee (code + 512) is measured on one daemon; a fork with different storage pricing would
   under-fee and the install would be rejected with a message the app does not parse.

#### NEXT
- Record the testnet INDEX when the install completes; then the operator's decisions (first commit, ring
  default, bundle refresh) — the loop has run out of items that need no external resource.

### Loop 15 — inclusion is not execution: every daemon proves its votes and claims by a counter (2026-09-06, 16:45–19:00)

#### SECURITY
- **Signer daemon** (`tools/vaultsigner`): a vote is remembered only when the contract's own count key grew
  (`mc_<se>_<id>_<sha256(j|amount)>`, `dc_<se>_<j>_<ver>_<sha256(addr)>`, `rc_<se>_<j>_<ftx>`,
  `hc_<se>_<he>_<want>_<bucket>`): an included-but-reverted vote is cast again next pass. The withdrawal delay now
  gates the foreign payment, not just the `Released` vote (the delay is the honest signers' time to halt). A
  withdrawal the quorum released is never re-judged by the size heuristic; the heuristic runs once, before this
  daemon has paid. The reserve check compares the foreign reserve with `sup + Σ unpaid burns` (sup drops at the
  burn, the reserve only at the payment). Halt memory is keyed by halt epoch: a fresh shortfall after a resume is
  voted again. A deposit over the cap or over `maxdep` is reported with the reason instead of voted into a refusal
  every pass. `ftx` validated before "paid"; a lock file per state; `-network` checked against `DERO.GetInfo`;
  `-confs` floor from the chain table on mainnet. Simulator e2e green with the rebuilt daemon (`.sim/vault_e2e.log`):
  mint by two signers with the counter proof, the 250 000 over-cap deposit reported, burn, finality wait, two
  delayed `Released` votes, both intents `voted`, no lock left.
- **Treasury bot**: a dry run no longer sends during reconcile (`dryChain`); a claim owes the `fc` growth it
  caused and is dropped when `fc` did not move; a deposit closes only when the pass's `dep` grew by its units;
  dust claims wait for the next batch instead of being forgotten; journal per treasury address, lock file,
  `-min-units` (integer) replaces the float flag; an unreadable network fails closed and is checked against the
  daemon. Four new journal tests (unexecuted sends, counter growth with two claims on one market, dust, dry chain).
- **App**: a stored remote (wss) endpoint is used only after a per-session confirmation in Settings and its host is
  shown in the network pill; a private buy that overshoots the curve is refused (the refund would go to `dest`);
  the registration probe checks that *our* scid landed among the new entries; the burn destination is fetched
  inside the guard; a transfer the wallet has not answered in 10 minutes blocks new transactions and is reported
  when the wallet answers late; slippage must be an integer in (0, 1000]; local trade records are shape-checked;
  the wallet app id lives for the session only; a cache with trades but no points is refused.
- **Ops**: `telainstall` needs `-yes` on mainnet, takes `VEIL_WALLET_PASSWORD`, verifies every reused DOC against
  its on-chain code hash and every fresh install after inclusion; `netwallet -rpc-login` (required on mainnet) and
  the seed only on a terminal; `simdeploy -yes` + daemon network check; `verifyvendor` scans `--ignored` files (it
  found and removed a reviewer's stale `veilrv/` copy inside the derohe checkout), refuses a bad `binaries.lock`,
  flags unlisted exes and pins each tool's source-tree hash and Go version; `testnet-tela.sh` refuses while the
  deployer wallet RPC answers.

#### CORRECTNESS
- `simrpc.GetSC` decodes with `json.Number`: a uint64 above 2^53 (piconero amounts) no longer rounds through
  float64. Sell sheet reads the pool's asset balance, not the LP token. Pool first deposit and proportional LP moves
  are excluded from flow, buy pressure and candle volume. Reviewer scenario "halted, C votes resume, A+B slash C,
  A alone votes resume" kept as `tests/vault_haltslash_test.go`: the vault stays halted (the halt key carries the
  slash epoch). Full suite 389 s green; treasurybot and vaultsigner packages green; UI read-only and wallet passes
  green (launch + register with the new probe, published buy, private buy at ring 16, stake, pass mint + register).

#### PRODUCT
- none beyond the endpoint confirmation card in Settings.

#### PERFORMANCE
- none measured.

#### RESEARCH
- Bonds deter a minority, not a quorum: `Slash` needs k votes, so honest signers can slash a colluding quorum only
  when n ≥ 2k. With 2-of-3 a colluding pair keeps its bonds. TRUST_STATEMENT now says so; the "a third of the cap"
  claim is gone.

#### ARCHITECTURE
- Every off-chain actor now follows one rule: never remember an action the chain has not counted. The pattern
  (read counter → send → re-read counter) is in `vote()` (signer) and `claimedUnits`/`sendDeposit` (bot).

#### TODO
- Wrapper backlog unchanged (real backends, viewing-key publication, veto). Committed locally as the review batch.

#### UNCONSIDERED
1. The counter proof reads the contract twice per vote; on a public node that is two extra requests per vote and a
   race with another signer's vote in the same block is read as "counted" (harmless for a signer that did vote,
   wrong for one whose vote reverted while another's landed: k-1 votes then wait for the next pass).
2. `-max-withdrawal-bps` is judged once, at the reserve of that pass; a reserve that shrinks after the intent is
   written no longer stops the payment.
3. The app's late-transfer report depends on the wallet keeping the socket open; a wallet restart drops the id and
   the user learns the outcome only from the wallet history (the toast says so).

#### NEXT
- New operator directive received at 18:50: the platform programme (inventory → parity matrix → backlog → loops).
  Loop 16 starts it; records from here use the directive's section list.


## Loop 16 — the platform programme starts: inventory, matrix, backlog, seven P0 fixes, DMZ, one document set (2026-09-06, 19:00–19:45)

### COMPLETED
- **Reconnaissance** by six independent readers plus a competitor benchmark and one synthesis:
  `docs/PLATFORM_GAP_MATRIX.md` (181 capabilities: 74 existing, 54 partial, 53 missing, 41 superficial; evidence
  as file:line; security surface; privacy constraints; measured sizes; benchmark of pump.fun, shld.fun,
  DexScreener, Magic Eden, OpenSea, Axiom/Photon/GMGN/Jupiter, the launchpads). The prioritised plan is
  `docs/PLATFORM_BACKLOG.md` (P0 wrong-or-unsafe → P1 competitor standard → P2 where DMZ pulls ahead → P3).
- **Seven P0 fixes, all verified.** B0.1 templates were stale: `app/templates/{launch,pool,founders}.bas` lagged
  `contracts/`, so every launch from the app installed code without the private refund and the init guard;
  regenerated by `simdeploy` with every earlier hash kept as legacy. B0.2 verification now checks `fee`/`pfee`
  against the protocol's (a fee-0 market showed Verified). B0.3 the `…To` entrypoints never publish: one payer could
  write `t_<n>` records under any address (free "distinct publishers", strangers shown as buyers). B0.4 holder NFTs:
  one edition per address for the life of the contract (`mt_`), `holdblocks ≥ 1`. B0.5 Founders: `minhst =
  1 + ⌊maxsupply/50⌋` before undistributed DERO is released (the first registrant of one unit swept it). B0.6
  Buyback ≤ 1 % of the reserve per call, 20 blocks apart (sandwichable before). B0.7 Losers sorted by the wrong key.
  Plus: the NFT page reads `mt_`; the review scenario "halted, C votes resume, A+B slash C, A alone resumes" kept as
  `tests/vault_haltslash_test.go` (passes).
- **Verified:** full suite green (453 s, with the founders/buyback/NFT tests rewritten for the new rules);
  `go test ./vaultsigner ./treasurybot` green; UI read-only and wallet passes green on tokens launched from the
  new templates, including the private buy at ring 16 with `tc` unchanged (new assertion); simulator vault e2e
  green with the counter-proving signer.
- **DMZ.** Operator naming ruling: the product is DMZ, dURL `dmz.tela`. User-visible strings, install defaults and
  the README say DMZ; identifiers (`window.Veil`, `VEIL_CONFIG`, `veil.*` keys, Go module) unchanged on purpose.
- **One document set.** 44 markdown files became 19 without losing a paragraph: `deploy/README.md` (four
  runbooks), `research/wrapper/{README,DESIGN,RUNBOOKS}.md`, `research/crosschain/{README,RESEARCH}.md`,
  `docs/history/HISTORY.md` (build brief, phase reports, day log, earlier backlog, every loop record — new loops
  append here). Thirteen of fifteen screenshot folders removed (~100 MB), the two newest kept under
  `docs/history/screenshots/`. Every cross-reference rewritten. Machine-specific paths and the operator's name
  scrubbed from every tracked file; the WSL scripts take `$REPO`, the UI test takes `$PW_DIR`.
- **README** rewritten for outside developers; `docs/HANDOVER.md` written for the next agent, including the
  wrapper build plan.

### DISCOVERED
- The reviewer's copy of the tests in the derohe checkout (`veilrv/`) was invisible to `git status` until
  `verifyvendor` scanned ignored files; it held one scenario worth keeping.
- The founders `minhst` gate changes a documented behaviour (the first registrant used to receive everything
  that accrued before); three tests encoded the old behaviour and were rewritten with the new arithmetic
  (2/5 : 3/5 splits, exact).
- Buyback with the 1 % cap needs ~20 calls to drain a tax pile that accrued during the curve phase; the test
  now drains it in a loop with 20-block gaps.

### SECURITY
- New attack surface closed: published-record spoofing through `dest`; NFT edition farming; the Founders sweep;
  the Buyback sandwich; fee-0 markets passing verification.
- Still open (backlog): dust-trade ranking (B1.1), social names free-form (B2.5), social full-state read (R-12),
  factory registration without dedupe (C7), treasury key single point of failure (C13).

### UX
- Losers now lists the largest fall first. The NFT page says when an address has already minted.

### PERFORMANCE
- Baseline recorded in the backlog: 202,869 B JS (63,252 gzipped), 32,551 B CSS (8,091 gzipped); budgets set.

### ECONOMICS
- No change to fees. The Founders `minhst` gate delays the first payout until ~2 % of editions are registered.

### STORAGE
- Unchanged: images are creator-hosted URLs without hashes (B2.6 is the fix).

### PRIVACY
- A private trade can no longer leave a public record naming the receiver.

### COMPETITIVE GAP
- Discovery surfaces, launch wizard, charts (zoom/pan/timeframes), portfolio breadth, alerts, share cards,
  collection pages, comments: all still behind (B1.x, B2.x).

### UNCONSIDERED
1. Renaming code identifiers to DMZ touches localStorage keys: a rename must migrate `veil.*` entries or every
   user loses cache, watchlist and local trades.
2. `docs/history/HISTORY.md` is 160 KB; GitHub renders it, but the first push carries every screenshot ever
   committed in history unless the operator starts from a squashed commit.
3. The `mt_` lifetime flag is per address string; a user with two wallets mints twice, which is the intended
   sybil cost (two funded wallets, two locks), not a bug.
4. The sed-based reference rewrite left a few parenthetical section hints in code comments (harmless, but a
   reader may look for a file named "README.md (Resolved)").

### NEXT
- B1.1 ranking v2 with the "why trending" line; then B1.6 metric legend and B1.10 launch wizard.


## Loop 17 — vault v3: one vote helper, canonical deposit ids, safety valves, and two audits folded in (2026-09-06, 19:45–21:15)

Two independent audits arrived during this cycle: an eight-lens adversarial workflow I ran over the wrapper
(contract, votes, accounting, deposits, withdrawals, daemon, governance, foreign chains) and an external report
from the operator ("Hermes"). Both are folded in below; where they disagreed with the code, I checked the code.

### COMPLETED

- **`contracts/vault.bas` v3**, 17,920 bytes, install storage gas well under the 20,000 cap. Rewritten around one
  internal `votek(f, kb, ch, s)` helper that every k-of-n family now shares, so the counting rule has exactly one
  implementation instead of seven copies. It allows a signer to **move** a vote (the old choice loses one) and
  closes a scope on execution (`<f>d_<kb>`), so no decision can execute twice.
- **Canonical deposit ids.** `Mint(j, txid, vout, amount)` takes the txid as exactly 32 raw bytes and builds
  `HEX(txid) + ":" + vout` itself. Two spellings of one deposit are now impossible, and (txid, vout) means a
  batched transaction can credit several users.
- **The reserve invariant is now exact.** `Burn` no longer reduces `sup`; `Released` does. So `foreign reserve >=
  sup` holds at every moment with no off-chain bookkeeping — the daemon no longer has to add pending burns back.
- **New safety valves:** `Veto(j)` (k votes return a withdrawal that cannot be paid, so a user is never left with
  neither units nor coins), `Refunded(txid, vout, ftx)` (a deposit that could not be credited is closed for good:
  minted or refunded, never both), `ClaimFees()`, `Tighten` (limits move the safe way only, with floors),
  `AddSigner2` (replace a slashed or lost signer), `Unbond` (a candidate never voted in gets their bond back),
  `SetViewKey` (the key that makes the reserve auditable lives on chain).
- **Six audit fixes in the contract:** the txid ambiguity above; `cap ≤ capratio × bondtot` replacing a
  dimensionally meaningless `3 ×` (cap is in foreign base units, bondtot in DERO atomic units — `capratio` is now
  the operator's written-down price assumption, and there is still no oracle); `Tighten` floors so a colluding
  quorum cannot brick the vault with `maxdep 1` and a three-week exit delay; operational vote scopes now carry the
  halt epoch and a 2000-block bucket so k−1 signers cannot bank votes and hold a one-vote trigger; `Register`
  hands out one unattested index per address, bounding the attestation work one account can force; `Slash` walks
  the cap down with the bond instead of silently blocking every later mint.
- **External audit F3 fixed:** `Seal` now requires `n > k`. A k-of-k set can never slash anyone, so one
  compromised key would sit in it for good and one lost key would freeze the vault. `n ≥ 2k` (what makes a
  colluding quorum slashable) stays a deployment rule, because enforcing it would forbid 2-of-3.
- **External audit F6 fixed:** the README claimed "trades are private by default". False — an ordinary swap is
  ring 2 and the node reveals the signer. Replaced with a precise paragraph, and the same overclaim in the gap
  matrix is struck through with a correction.
- **The signer daemon fails closed against v3** (it speaks v2 only: wrong Mint signature, wrong reserve
  arithmetic, wrong counter keys). It refuses to start rather than voting nothing through and halting a vault.
- Tests: `vault_v3_test.go` (safety valves, k-of-k refusal), rewritten `vault_test.go`, `vault_limits_test.go`,
  `vault_fuzz_test.go` (600 random steps now including vetoes, conservation checked against the v3 accounting),
  `vault_smoke_test.go` (a fast parse/gas/round-trip check), shared scaffolding in `vault_env_test.go`.
  `contracts/README.md` now carries the full v3 rule set, the vote-family table and the key map.

### DISCOVERED / RESEARCH

- The DVM charges storage gas on the **source bytes including comments**, so prose in a contract competes with
  code for the 20,000-unit install budget. The vault hit the ceiling twice this cycle; the fix was to move the
  key map and rationale into `contracts/README.md` and keep one or two lines per function. Worth knowing before
  any contract grows again: the budget is ~19,600 bytes of source.
- `HEX()` of an already-hex string silently produces a different id. That is exactly how the two-spellings bug
  reappeared after I "fixed" it — the guard has to be on the input length (32 raw bytes), not on the output.
- The operator shared an analysis of their other repositories. Three fit real gaps: FROST-ZEC / bulletproof-XMR /
  zec-t-htlc prototypes are the missing foreign-custody legs (partly already in `research/crosschain/poc/`); a
  chain-agnostic adversarial/soak/replay harness is what the vault needs next; and **Spore's private P2P messaging
  over DERO transaction payloads answers a question the pipeline spec left open** — the signers need an
  authenticated channel to exchange Monero multisig rounds and FROST nonces, and doing it through DERO
  transactions removes a whole service, keeps it auditable and fails closed. Recorded as B3.6.

### SECURITY

- Closed this cycle: two-spellings double mint; vote banking across halts; a quorum bricking the limits; the
  cap surviving a bond reduction; unbounded registration work; a non-signer inflating the stake bound; a k-of-k
  set that can never slash; a decision executing twice via vote-change.
- Still open and now documented: the whole foreign side (no Monero/Zcash backend exists), exactly-once payment
  across signers on a chain that cannot dedupe by withdrawal id, wallet-RPC authentication for the signer, and
  the fact that a colluding quorum in a 2-of-3 vault keeps its bonds.

### CORRECTNESS

- All vault tests green against the real DVM (43 s for the vault group). Full suite running.
- The conservation fuzz now asserts the v3 identity `sup == Σ minted − Σ released` and
  `feepool == Σ released fees` after every one of 600 random steps, with vetoes in the mix.

### PERFORMANCE / STORAGE

- Contract 19,510 → 17,920 bytes with more functionality, purely by moving prose out.
- No app changes this cycle, so the TELA bundle is unchanged (238,714 B raw, ~72 KB gzipped).

### PRIVACY

- `Register` now issues a fresh index per deposit, so two deposits by one user share no on-chain address. The
  README no longer overstates what ring 2 hides.

### ECONOMICS

- `capratio` makes the bond-to-cap relationship an explicit, recorded assumption instead of an accidental
  comparison of two different units.
- Slashed bonds still go to the treasury rather than to victims (external audit F4). Documented, not fixed; a
  `Compensate(dest, amount)` k-vote payout is specified in the backlog.

### UNCONSIDERED

1. `Veto` has no interlock with a payment already broadcast abroad. k signers could veto a withdrawal the
   federation has in fact paid, returning the units **and** the coins. The daemon must never vote Veto once its
   own intent has reached `paying`, and the contract cannot see that. This needs a rule in the daemon and a line
   in the runbook — it is the mirror image of the double-pay risk.
2. The 2000-block vote bucket means a decision straddling a bucket boundary loses its votes and needs a fresh
   round. Harmless for liveness (the daemon re-votes when the counter does not move) but it will look like a
   stall in logs, and a federation polling slower than ~10 hours would never complete anything.
3. `capratio` is a price assumption frozen at install. If the foreign asset appreciates 10× against DERO, the
   bond bound silently becomes 10× weaker in real terms, and nothing on chain notices. Only `Tighten` and
   `SetCap` can respond, both k-of-n and both manual.
4. Every operational vote now carries `he`, so **a halt-and-resume cycle voids every in-flight deposit vote**.
   That is the safe direction, but a federation that halts often will strand deposits repeatedly and the daemon
   must re-cast from scratch each time.

### TODO / NEXT

- **Next cycle:** port `tools/vaultsigner` to v3 (the version guard currently refuses to run): new Mint call, the
  simplified reserve check (`reserve >= sup`), the new counter-key shapes, `Veto`/`Refunded` handling, and the
  `Finalized`/`Verify`/`Health` backend methods the audit demands before any real chain.
- Then: the Monero and Zcash backends against recorded RPC fixtures, and the adversarial/soak/replay harness.
- Also queued from the external audit: wallet-RPC authentication in `simrpc` (F5), treasurybot finality gating
  and the unconditional PayBatch sweep (F7), `netcfg` accepting an unknown key as a network (F12), and three
  stale documentation contradictions (F13).


## Loop 18 — the signer daemon speaks v3, and the whole wrapper runs end to end (2026-09-07, 00:30–01:50)

### COMPLETED

- **`tools/vaultsigner` ported to vault v3 (backlog W1).** New `Mint(j, txid, vout, amount)` call, the exact
  reserve check (`reserve >= sup`, no pending-burn arithmetic now that a burn keeps its claim until release),
  the new operational vote scopes (slash epoch, halt epoch, 2000-block bucket), veto handling, and the version
  guard flipped from "refuse v3" to "require v3".
- **Every vote is now proved by reading back this signer's own vote key.** v3 stores each signer's choice at
  `<f>v_<scope>_<address>`, so the daemon asks "did the contract record *my* vote for *this* choice" instead of
  watching a counter a co-signer can also move. This closes an audit finding where a co-signer voting in the same
  block made a reverted vote look successful and it was never re-sent.
- **`simrpc.DVMAddress`** (`tools/simrpc/address.go`, with tests): the DVM's `ADDRESS_STRING` always renders the
  **mainnet** `dero1…` spelling, even on testnet and the simulator where the wallet reports `deto1…`. Any tool
  looking up a key named after an address must convert first. This cost a whole end-to-end run to find: the
  daemon voted correctly and then reported "the contract did not record our vote" because it was reading a key
  that could not exist. The vectors in the test are taken from a live vault's vote keys.
- **The halt deadlock is closed** (audit finding, reproduced for real in this cycle's first end-to-end run). A
  withdrawal above `-max-withdrawal-bps` used to make every signer vote `Halt(1)` on every pass: an operator
  resumes, the withdrawal is still there, every signer halts again, forever. Now a signer flags the withdrawal,
  halts once, and then leaves it to a human — who either vetoes it with k votes or clears the flag with
  `-allow-withdrawal <j>`. Verified live: halt fired, operator resumed, override let both signers pay and record
  the release.
- **A deposit older than the address it was paid to is never credited.** An address derived from a future index
  can be paid before anyone owns it; crediting that would hand one user's coins to whoever registers the index
  next. The anchor is the foreign height at which this signer first saw the address published.
- Backend interface gained `Finalized()`, `Verify(deposit)`, `ValidDest(dest)` and `Health()`: nothing is voted
  above the chain's finalized height, every deposit is re-read at vote time, a destination the chain cannot pay
  is vetoed rather than paid into nowhere, and an unhealthy backend makes the daemon do nothing at all.
- State is bound to one vault (its intents name that vault's withdrawal indexes) and the lock file is removed on
  every exit path including a signal, so a Ctrl-C no longer takes a signer offline until a human deletes a file.
- `tools/simvault` gained `-halt 0|1` (an operator resume is a real operation and now has a tool) and the v3
  install/deposit format. `tools/simvault/e2e.sh` walks the whole story including the breaker and the override.
- Contract: the vote key is named by `ADDRESS_STRING(s)` so a daemon can read its own vote back. 18,145 bytes.

### VERIFIED

- Full contract suite green, 417 s. `go test ./vaultsigner ./simrpc ./treasurybot` green.
- Live on the simulator: attest → mint (supply 50,000) → burn 40,000 → breaker halts the vault → operator resume
  → `-allow-withdrawal 0` → both signers pay abroad and vote Released → supply 10,100, reserve 60,100. The
  accounting closes exactly: 10,000 unburned + 100 fee in the pool, and reserve − supply = 50,000 of headroom.

### SECURITY

- Closed: the co-signer vote-attribution race, the halt deadlock, crediting a deposit that predates its address,
  voting above finality, paying a destination the chain rejects, replaying one vault's intents against another,
  and a lock file that outlived its process.
- Open and unchanged: the foreign side does not exist, so none of this touches a real coin yet.

### PERFORMANCE / STORAGE

- No app change this cycle. Contract 17,920 → 18,145 bytes (the address-named vote key), still well inside the
  install budget.

### PRIVACY

- Unchanged. Worth noting for later: the vote key now contains each signer's address in clear, which was already
  true of `isg_`/`bond_` — the signer set is public by design, so this adds nothing new.

### UNCONSIDERED

1. The sim backend's `Reserve()` counts deposits that were never minted (the stale one from an earlier run kept
   the reserve at 100,000 while supply was 50,000). A real backend must count *spendable* balance, not the sum of
   observed deposits, or the shortfall detector will be blind to exactly the loss it exists to catch.
2. `-allow-withdrawal` is a per-signer local override. If only some signers set it, the withdrawal gets fewer
   than k payments and sits half-done: one signer has paid abroad while the quorum never forms. The operator
   runbook must say "all k signers or none", and the daemon should probably refuse to pay when it can see that
   fewer than k signers have flagged it clear. Not yet implemented.
3. The deposit-ordering anchor is per signer and set at "first observation". Two signers that start at different
   times have different anchors, so a deposit near the boundary can be votable by one and not the other. It fails
   safe (no quorum, funds stay put) but it will look like a stall, and the operator has no tool to inspect it.
4. Nothing yet stops a signer from voting `Released` with an `ftx` it invented rather than one it paid: the
   contract only checks that k signers agree on the same string. On a real chain the other signers must verify
   the txid pays the right destination and amount before agreeing. That verification is not in the interface yet
   and belongs in `Verify`-shaped form for releases.

### NEXT

- W2 is largely done inside W1; the remaining gap is release-side verification (unconsidered item 4): a
  `VerifyRelease(j, ftx, dest, amount)` on the Backend so a signer only agrees to an `ftx` it has checked pays
  the right destination.
- Then the Monero backend against recorded wallet-rpc fixtures (W3), which is the first time any of this touches
  a real chain's semantics.


## Loop 19 — liveness by construction: nothing may need a human (2026-09-07, 01:50–03:10)

The operator ruled that they will revoke their own access after deployment. That turns a whole class of design
into a defect: **any state that needs a privileged party, a manual flag or an operator decision to leave is a
permanent freeze of user funds.** The rule is now HANDOVER rule 0, and this cycle rebuilt everything that broke it.

### COMPLETED

- **The outsized-withdrawal breaker became a size-tiered delay.** It used to refuse to pay and halt the vault; an
  operator resumed and every signer halted again, forever — I reproduced exactly that in loop 18's first live run.
  Now `bigbps`/`bigdelay` are fixed at install and never changeable, `Burn` records `w_<j>_rel` (the height from
  which the withdrawal may be released: `wdelay`, plus `bigdelay` when it is larger than `bigbps` of the supply),
  and `Released` gates on that. A large withdrawal waits longer and then pays. Doing nothing releases.
- **`maxdep` is deleted outright.** A deposit above a per-deposit maximum could never be minted at all, and with
  no operator nobody could send it back — a silent permanent loss. The rolling window bounds the mint rate
  instead and empties by itself; `tests/vault_limits_test.go` now asserts that a deposit the window turned away
  mints untouched two buckets later.
- **A halt is a condition, not a decision.** The daemon votes `Halt(1)` while the reserve is short of the supply
  and `Halt(0)` once it is not, one vote per direction per 2000-block bucket. Every honest signer computes the
  same thing from the same public state, so the quorum forms and dissolves with nobody to ask.
- **The daemon lost every discretionary power over user funds**: `-allow-withdrawal`, the flag memory and
  `suspiciousWithdrawal` are gone. It pays what the contract says is releasable and nothing else.
- **`ClaimFees` is permissionless on every market** (`launch.bas`, `pool.bas`, `nft.bas`). It always pays the
  treasury address recorded at install, so who receives it is unchanged, but the *claiming* no longer waits for
  one key to be online. An empty pool is refused rather than burning a fee for nothing.
- Over-cap deposits are reported as waiting for headroom, which is what they are: the daemon retries every pass,
  so a later burn or the window sliding credits them with nobody intervening.

### VERIFIED

- Full suite green, 38 tests, 56 s. `go test ./vaultsigner ./simrpc ./treasurybot` green.
- The limits test now proves both halves of the principle: a window-refused deposit mints later untouched, and
  both an ordinary and a large withdrawal release once their recorded height passes (5 and 505 blocks).

### DISCOVERED

- Writing the rule down changed what counts as a bug. Three mechanisms that all looked reasonable in isolation —
  a size breaker, a per-deposit maximum, and an operator override — were each a permanent freeze once you assume
  nobody is there. None of them would have failed a test; they would have failed a user, years later.
- The DVM's inability to let one contract pay another is what forces the treasury bot to exist, and it is now the
  only remaining trusted step in the fee path. Everything around it has been made permissionless.

### SECURITY

- Removed: the halt deadlock, the permanent-stall paths through `maxdep` and the manual override, and the
  single-key dependency of the fee claim.
- The size defence is weaker on paper (a delay, not a refusal) and stronger in practice: a refusal that needs an
  absent human to lift is not a defence, it is a loss. The delay is what honest signers actually use — time to
  see the burn and vote a k-of-n `Veto` or `Halt`.

### UX

- A user's large withdrawal now completes on its own, late instead of never. The app should show the release
  height so the wait is visible rather than mysterious (not yet done — added to the backlog).

### PERFORMANCE / STORAGE

- Contract 18,895 bytes, install gas 19,229 of 20,000. Removing `maxdep` paid for `bigbps`/`bigdelay`.

### ECONOMICS

- Permissionless `ClaimFees` means a Founders Pass holder who sees fees accruing can move them without asking
  anyone, which is a real improvement to the credibility of the fee-share promise.

### PRIVACY

- Unchanged this cycle.

### UNCONSIDERED

1. `mindep` is still a permanent loss for a deposit below it: never mintable, never refundable without an
   operator. It exists to stop a griefer forcing k signer transactions per dust payment. The honest resolution is
   that the minimum must be published in the app *next to the deposit address*, so it is a rule the user saw
   rather than a trap. Not yet in the UI.
2. Signer replacement (`AddSigner2`) still needs a human to choose the new signer's address. That human is the
   federation, not the operator, so it does not break rule 0 — but if the whole federation goes silent the vault
   drifts to `na == k` and then stops. Nothing on chain can fix that, and the trust statement should say so.
3. The TELA INDEX key is the last operator-shaped power in the system: whoever holds it can replace the app the
   users load. Discarding it makes the app immutable (safe, unfixable); keeping it is a standing trust. The
   deployment runbook must make that an explicit, recorded choice rather than an accident.
4. A halt that is voted for a reason *other* than a reserve shortfall will now be auto-resumed by the daemons,
   because they only know the shortfall rule. If a future signer halts for a reason the daemon cannot see, the
   others will vote it back up. Any new halt reason must therefore be computable from public state too.

### NEXT

- Publish the minimum deposit and the release height in the app (unconsidered 1 and the UX note).
- Then release-side verification (`VerifyRelease`), and the Monero backend against recorded fixtures.

## Loop 20 — the freeze that survived the liveness rewrite (2026-09-07)

Loop 19 wrote down the rule that no state may need a human to leave it, and removed three mechanisms that broke
it. This cycle put six independent adversarial readers on the result and found that **the freeze was still
there**, in the daemon rather than the contract, and would have hit every vault on its first withdrawal.

### THE BUG

A claim leaves the foreign reserve when the federation pays it, and leaves `sup` when the `Released` quorum
records that payment. Those are necessarily different moments — the payment is the thing the vote is about. The
daemon halted on `reserve < sup`, which is true for the whole interval between them. So:

1. a signer pays abroad, the reserve drops, and the next pass reads a shortfall;
2. it votes `Halt(1)` and returns before reaching the `Released` vote;
3. `vault.bas Released` refuses while halted, so `sup` can never fall;
4. the resume test is the same comparison, so it can never become true;
5. mints and burns are refused too, so the reserve can never rise.

Every deposit and every burned unit frozen, permanently, with nobody who could lift it. `tools/simvault/e2e.sh`
had been *watching for this halt and reporting it as a size breaker working as designed* — a mechanism that no
longer exists. That is how it survived two cycles of review: the test asserted the bug.

The check is now `reserve >= sup - inflight`, where inflight is every burned withdrawal the contract has not
closed. It is computed from contract state at one block and nothing else, deliberately: narrowing it by release
height or by local belief would let two honest signers at different tips disagree, and one would halt the other's
honest payment.

### ALSO FIXED

- **A deposit larger than `winmax` could never be minted, in any bucket, ever** — `maxdep` returning through
  another door, found while correcting the runbooks rather than by the audit. The window now refuses to *start* a
  mint once it is full, instead of asking each deposit to fit what is left. Overshoot is one deposit; `cap` bounds
  it.
- **`Burn` destroyed the whole withdrawal of anyone who did not pin ring size 2.** It read `SIGNER()`, which is
  blank above ring 2, so the call reverted — and a revert above ring 2 cannot address its refund. It now takes a
  named `refund` address, which also lets a burn stay private.
- **`AddSigner` never credited a bond paid before it**, so a later `Slash` subtracted a bond that was never added
  and wrapped `bondtot` to near 2^64 — lifting the cap bound entirely. An ordinary race between two parties, not
  an attack.
- **The daemon paid destinations `vetoPass` had just declared unpayable**, from a snapshot taken before its own
  veto vote. It now skips them and re-reads the withdrawal's status immediately before paying.
- **A signer only votes `Released` for a payment it has verified itself** (`Backend.VerifyRelease`). A recorded
  payment that stays invisible for `-verify-tries` passes returns the intent to `paying` and the backend is asked
  again, because never voting is a freeze and `Release` is idempotent per withdrawal.
- **The app leaked a `D.onChange` subscription per screen visit**, so every block redrew the current screen once
  per past visit. Eleven listeners after five navigations; now two, asserted in the UI test.
- **The desktop market table overflowed its column between 960 and ~1300px** and was silently clipped; the
  two-column pages left their main column 176–260px wide, narrower than the phone layout.
- **A look-alike ticker warning fired on every ticker containing a 0 or a 1** (the fold maps those to O and I on
  purpose) and missed six real Cyrillic homoglyphs. A contract opened by deep link had no impersonation warning
  at all — the one route a scam link actually arrives by.

### SHIPPED

- `app/wrap.js`: the wrapped-asset screen. Publishes the minimum deposit *before* the wallet is connected,
  because it is a property of the vault and not of the visitor, and a deposit below it is an unrecoverable loss.
  Shows each withdrawal's release height counting down, the reserve against the claims, and a trust panel that
  names what k signers can do. No SAFE badge, and the UI test fails if one appears.
- `netcfg` gained a `vaultAsset` record (name, symbol, chain, decimals, confirmations) so the app renders base
  units as a number a person recognises without ever guessing the decimals.
- `simdeploy` no longer renders its own copy of `config.js`; it records the deployment and then renders from that
  record, the same way `genconfig` does. The second copy had been silently dropping the vault from the config
  every time it ran.

### VERIFIED

- Contract suite green (56 s). Install storage gas **19,112 of 20,000** — headroom went up despite two entrypoint
  changes, by moving rationale into `contracts/README.md`.
- `go test ./vaultsigner ./simrpc ./treasurybot` green, including four new tests that fail on the old halt rule.
- Live on the simulator: deposit → attest → mint → burn → pay abroad → verify → `Released`, supply 50 000 →
  45 100, reserve 100 000 → 95 100, `halted=0` throughout. The large withdrawal correctly waited for its
  size-tiered height.
- UI test extended and green: wrap screen renders and publishes the minimum; listeners stay at 2; a markets
  redraw of 19 markets costs 1.01 ms; the 1024px token column is 700px (was ~260px); zero horizontal overflow.

### UNCONSIDERED

1. The e2e script asserted the bug for two cycles. Any test that watches for a failure and calls it a feature is
   worse than no test. Every remaining "expected failure" assertion in this repository should be re-read against
   the current design, not the design it was written for.
2. `VerifyRelease` is only as good as the backend implementing it. The sim backend checks its own JSON file,
   which proves the plumbing and nothing about Monero. The Monero and Zcash backends must verify against a node
   they do not control, and that is where the real work is.
3. The daemon re-reads a withdrawal's status before paying, which narrows the veto race to two RPC round-trips
   but does not close it. Closing it needs a contract-side claim, and the gas budget does not have room.
4. `hasHomoglyph` only knows the confusables in one table. A real answer is the Unicode confusables data, which
   is far too large to ship in a TELA DOC — so the honest position is that the warning is a hint, not a check,
   and the app should say so where it shows it.
5. Six adversarial readers found 50 real defects in a codebase that had already passed two audits. The rate says
   the next six would find more, and that "audited" is a description of effort, not of state.

### NEXT

- The Monero backend against recorded wallet-rpc fixtures, with `VerifyRelease` reading a real transaction.
- The remaining audit findings not yet applied: the localStorage cache re-verification, `stats.enrich` being
  called for unchanged assets, `AddSigner2`'s ceiling counting signers ever added, the pre-seal bond lock, and
  `k = 20` being accepted at install when it can never be sealed.
- Chart quality: zoom, pan and selectable timeframes on the token chart.

## Loop 21 — the rest of the ways it could stop, and one lie in a comment (2026-09-07)

Loop 20 fixed the freeze that would have hit every vault on its first withdrawal. This cycle worked the same
class of defect down the audit list until the list was empty of them: five more places where the system could
stop, or eat money, with nobody able to intervene.

### FIXED

- **One mistyped signer address locked every honest signer's bond for ever.** A typo in a 66-character bech32
  string makes `Seal` permanently unreachable — nobody holds that key, so it can never bond — and `Unbond`
  refused anyone with an `isg_` key regardless of whether the vault was ever sealed, `Slash` needs a sealed
  vault, and there is no `RemoveSigner`. The contract cannot correct a typo, but it must not eat the bonds of
  people who did nothing wrong: `Unbond` is now open to anyone before `Seal` and closed to every signer after it,
  with `bondtot` unwound and clamped so it cannot underflow.
- **Signer replacement ran out.** `AddSigner2` compared `n`, the monotonic slot index that `Slash` never
  decrements, so every rotation consumed one of the twenty permanently. A long-lived vault would eventually be
  unable to seat anyone, drift down to `na == k` as keys were lost, and stop. The ceiling is now on the active
  set, where it belongs.
- **`k = 20` was legal at install and could never be sealed.** `AddSigner` stops at twenty signers and `Seal`
  needs `k + 1`, so the vault was born dead with twenty bonds inside it. `k` is capped at 19.
- **A stale lock retired a signer permanently.** The lock meant "locked" by existing, and the comment claiming it
  was "removed on every exit path, including a fatal one" was simply false: `simrpc` leaves through `log.Fatal`
  on any RPC trouble, which skips deferred calls, and SIGKILL skips the signal handler. One transient failure
  took a signer offline until a human deleted a file, and there is no human. The claim is now held by a heartbeat
  and a lock untouched for 30 minutes is taken over — worst case a duplicate vote, which the contract refuses,
  instead of a vault quietly running one signer short for ever.
- **The daemon told the truth about dust.** A deposit below `mindep` printed "operator refund needed". There is
  no operator, and on a ring-signature or shielded chain the federation cannot see who paid, so there is no
  address to return it to either. It now says that, and the app publishes the minimum beside the deposit address
  before the wallet is even connected.

### VERIFIED

- Contract suite green (56 s). Three new tests — `TestBondsEscapeAnUnsealableVault`,
  `TestSignerReplacementDoesNotRunOut`, `TestInstallRefusesAnUnsealableQuorum` — were run against the previous
  contract first and all three fail on it. A regression test that has never seen the bug is a guess.
- Two new daemon tests cover the lock: a fresh lock is respected, a stale one is taken over, and the heartbeat
  keeps a slow but healthy signer from being displaced.
- Install storage gas **19 400 of 20 000**, re-measured on the simulator rather than estimated, and recorded in
  `contracts/README.md` where the figure lives.

### UNCONSIDERED

1. Every one of these was a *pre-deployment* mistake with a *post-deployment* cost: a typo, a parameter the
   contract called legal, a ceiling on the wrong counter. The install path deserves the same adversarial reading
   the runtime path has now had twice, because after `Seal` none of it can be corrected.
2. The lock takeover trades a permanent stall for a possible duplicate vote. That is right for votes, which the
   contract deduplicates — but it assumes every future backend keeps `Release` idempotent per withdrawal. That
   requirement is now load-bearing in two places and should be a test every backend must pass, not a comment.
3. `mindep` remains a genuine unrecoverable loss and no code change can fix it: the chains carry no return
   address. The only honest treatments are disclosure, which is shipped, and choosing the value low enough that
   the loss is small — which is a deployment decision nobody has written down yet.

### NEXT

- The Monero backend against recorded wallet-rpc fixtures, with `VerifyRelease` reading a real transaction.
- An adversarial pass over the install path itself: everything that must be right before `Seal`.
- The remaining app findings: the localStorage cache re-verification, and `stats.enrich` being called for assets
  whose probe signature did not change.


## 2026-09-08 — the audit, its follow-up, and the mechanism verdict

### SHIPPED

- **A reproduced P0 fund drain closed** (`d1c5a89`): `launch.bas buyto` refunded a caller's overshoot one line
  before its `min_out` exit, and the `...To` wrapper turned that into a double payment — 2,052,806 taken from a
  2,150,000 pot on the real DVM. The refund is recorded and paid past every exit; `pool.bas AddLiquidity` had the
  same shape and got the same treatment before a wrapper made it exploitable. `TestNoExternalSendBeforeAFailureExit`
  walks control flow from every external send and fails on the pre-fix contract naming line 59.
- **The vault state machine** (`71de9e8`): a halt is a 20 000-block lease that lapses unless renewed; `Burn` and
  `Released` work while halted; `Reclaim(j)` returns an unmarked burn's units by itself 20 000 blocks past
  release; the halt matrix is a test. Install gas 16 368 of 20 000 on a real transaction; proven live through a
  halt, an epoch bump and out the other side to `sup 43 200 = reserve 43 200`.
- **Comments no longer cost install gas** (`e3fc36c`): `reference.Canon` strips them at install; the vault went
  from 771 units of headroom to 4 244. Template verification now hashes the executable source only.
- **The audit follow-up, applied by seven agents told not to trust the audit** (`ef3acac`, `9b4ad6b`, `0e6c97e`):
  41 findings fixed, the JS test layer that found two more quoting bugs the same hour, legacy vault records, and
  split pairs on the data layer so the thin half never ranks and the trade sheet names the deepest contract.
- **The simulator runs real decimals**: Monero at 12, Zcash at 8, both with the new state machine and parameters
  that make sense at those decimals (`deploy/VAULT_PARAMETERS.md`).
- **`research/wrapper/MECHANISM_REVIEW.md`** — the verdict, re-examined against "the operator hosts nothing 24/7".

### ALSO FIXED

- The units regression my own decimals fix had introduced (`priceAt` and `candles` still at DERO's 5): every
  change window on a wrapped market read −100 %. `A.priceOf(market, pt)` reads both decimals off the market so
  a call site cannot express the wrong thing.
- Every Zcash deposit and withdrawal was refused as "not a verified listing" (the allow-list tested the
  single-vault field). Create-a-pool could never succeed (four arguments where the contract needs five). A
  "100 XMR" graduation target installed as 0.00001 XMR, immutably.
- A token calling itself WXMR listed clean while the real wrapped Monero was flagged; vault names are reserved
  by ticker and, since tonight, by display name as whole words.
- Two pre-existing daemon liveness defects the live vault run exposed: a stranded `Released` vote in a dead epoch
  scope, and a stale epoch after the daemon's own halt vote.
- `simwrapmkt` re-registered two pools on the factory because my "already registered" check compared the wrong
  encoding; the factory has no dedup, so `pool_7`/`pool_8` are permanent duplicates on this simulator. Fixed,
  and the tool no longer re-seeds a pool that already has liquidity.

### VERIFIED (this cycle's evidence for the five standing requirements)

1. *XMR/ZEC first*: cold-cache probe and the UI pass at 390/768/1024 — Wrap offers XMR and ZEC only; the
   superseded vaults read `XMR-old`/`ZEC-old`; the ZEC pool reads "Wrapped asset · verified".
2. *Balances*: per-quote totals only; `holdings.total` and `change24` (cross-quote sums) deleted.
3. *Fees before the button*: the trade sheet's cost rows are per asset charged; the wrap screen prints vault
   constants at full precision (a 12-decimal minimum no longer rounds to "0 XMR").
4. *Nobody extracts more than deposited*: BC-1 fixed and guarded; the Paying/Veto interlock plus `Released`
   while halted; the DVM suite (63 s) green; live run `sup = reserve`, in flight 0.
5. *No drainable duplicates*: `s.splitWith` on the data layer; UI test asserts no thin member ranks and exactly
   one deepest per group.

### UNCONSIDERED

1. **The factory registry has no removal and no dedup**, so any tool mistake — mine tonight — leaves permanent
   per-block RPC amplification on every client (P7-04). A registry with a bounded, prunable list is a contract
   change nobody has designed.
2. **A superseded vault's holders have no UI exit.** Legacy vault records keep old markets honest and old units
   burnable, but the Wrap picker hides them; a holder of `XMR-old` must use the CLI. On mainnet a vault
   supersession is a migration event that needs its own runbook and screen.
3. **The daemon's stranded-vote and epoch fixes are verified live only.** They live inside `pass()`, which needs
   a chain; the unit tests cannot reach them. A harness that fakes the vote scopes would make them regression-safe.
4. **The transparent-ZEC swap path has never been driven from the app.** `board.bas` and `swap.bas` ran on the
   simulator from Go tools on 2026-09-06; no screen offers or takes a swap. The first mainnet product has no UI.
5. **Spore's whisper was not driven against our simulator** — the same limitation Q1 recorded on 2026-09-05.
   Adopting it as the rendezvous rests on its own README and code, not on a run here.

### NEXT

- The NFA-compliant asset contract with a quote parameter, replacing `nft.bas`, with tests and a simulator run.
- A design note and a simulator run of the DERO leg for the transparent-ZEC swap market, with spore as the
  rendezvous — the first thing DMZ can honestly put on mainnet.
- Founders Pass per-asset accounting.
- The Monero backend for `vaultsigner` against recorded wallet-rpc fixtures — subaddresses, not payment ids.
- `deploy/federation.md`: who runs the signers if the operator does not, and the `n ≥ 2k` disclosure.
