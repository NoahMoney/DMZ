# Veil platform backlog (2026-09-06, evening directive)

The master backlog for turning the DEX into the reference DERO asset platform. Built from the inventory in
`docs/PLATFORM_GAP_MATRIX.md` (evidence per row), the competitor benchmark (same file, §7) and the operator
directive. Ordered by value: what stops a user from trusting or using the product comes before what makes it
richer. Every item names the file(s) it lands in and what proves it done. `docs/history/HISTORY.md (earlier backlog)` stays the record
of the earlier programme (its §0 is the done list with evidence); new work is tracked here.

Legend: **P0** wrong or unsafe today · **P1** what a user expects from any competitor · **P2** where Veil pulls
ahead · **P3** research and later. `[contract]` needs a new template hash and a redeploy; `[app]` is JS only.

## P0 — wrong or unsafe today

- **B0.1 Templates drift** `[app+ops]` — `app/templates/{launch,pool,founders}.bas` were older than `contracts/`,
  so every launch from the app installed code without the private-refund and init fixes. Done criterion: templates
  regenerated from `contracts/` by `simdeploy`, hashes in `config.js`, old hashes kept as legacy, simulator
  redeployed, UI wallet pass green on a token launched from the new template. **Loop 16.**
- **B0.2 Verification checks the install parameters** `[app]` — the code hash and treasury were checked, `fee`
  and `pfee` were not: a market paying the protocol nothing showed Verified. Done: `feed.js` refuses a fee or pfee
  that differs from `config.js` (launch, pool, nft). **Loop 16.**
- **B0.3 Private trades never publish** `[contract]` — the `…To` entrypoints wrote `t_<n>` under `dest`, so one
  payer could mint "distinct publishers" for free and label strangers as buyers. Done: the wrappers pass
  `publish = 0`; `tests/ringswap_test.go` asserts `tc` unchanged. **Loop 16.**
- **B0.4 Holder NFT farming** `[contract]` — lock → mint → unlock → lock again minted another edition;
  `holdblocks = 0` was accepted. Done: `mt_<addr>` lifetime flag, `holdblocks ≥ 1`, `TestNFTMintOncePerAddress`.
  **Loop 16.**
- **B0.5 Founders first-registrant sweep** `[contract]` — DERO deposited before anyone registered went entirely
  to whoever registered one unit first. Done: `minhst = 1 + ⌊maxsupply/50⌋`; below it nothing is released
  (`tests/founders_test.go`). **Loop 16.**
- **B0.6 Buyback sandwich** `[contract]` — `Buyback()` spent the whole accrued tax at once with no min_out; buy →
  Buyback → sell was profitable. Done: ≤ 1 % of the reserve per call, 20-block spacing, model mirrors it. **Loop 16.**
- **B0.7 Losers filter** `[app]` — sorted ascending by whatever sort key was active (coldest first under Hot).
  Done: Losers orders by 24h change ascending unless another sort is chosen explicitly.
- **B0.8 Stale claims in docs** — `RISKS.md` says images default on (they are off); `MASTER_TODO` marks C7, C9–C13
  as done (the "· done" column was the criterion). Done: corrected, and the done column renamed.
- **B0.9 Dust-trade ranking** `[app]` — any non-zero swap activates a block and a block is worth 4 hot points, so a
  dust swap per block buys the top of Trending for network fees. Done: see B1.1 (ranking v2).

## P1 — what a user expects from any competitor

### Discovery (DexScreener standard, without the pay-to-trend)
- **B1.1 Ranking v2** `[app] stats.js` — hot score from DERO-weighted activity: per-block flow counts with a
  minimum notional (a block moving < 0.05 DERO scores 0), distinct publishers weighted by their published notional
  (log), decay across 5m/1h/6h/24h windows, age normalisation so a 3-day-old market does not need 10× the activity
  of a 1-hour-old one to appear, and a self-trade discount (an address whose published buys and sells net to zero
  inside the window counts once). Transparent: every trending card and the token page carry a "why" line with the
  three terms and their values. Done: unit test of the scoring on synthetic histories (dust bot, wash pair,
  organic), `docs/RANKING.md` with the formula.
- **B1.2 Surfaces** `[app] markets.js, home.js` — Hot (unusual activity vs the market's own baseline), Volume,
  Gainers/Losers per window (5m/1h/6h/24h), Most traded (active blocks), Fastest growing (publisher acceleration),
  Under the radar (activity high, published notional low, age > 6h), NFT trending (mint velocity from `minted`
  deltas), Creator trending (sum of their markets' hot). Sort by any window; filters by kind, phase, min liquidity,
  age. Done: each surface has a one-line definition visible in the UI.
- **B1.3 Search v2** `[app] ui.js, markets.js` — fold the query with `tickerKey` so a look-alike ticker shows next to
  the genuine one with its badge; fuzzy (edit distance ≤ 1 on tickers, prefix on names), search creators
  (profile names and addresses), NFTs and collections, tx ids (opens the trade or install), tags. Mobile search.
- **B1.4 Watchlists v2** `[app] stats.js` — tokens, NFTs, collections, creators; validated entries; export/import
  as a file the user keeps (no sync server); "Watchlist" surface with the same columns as Markets.
- **B1.5 Liquidity honesty** `[app]` — pool liquidity time-weighted over 24h and an "LP can leave" chip on pools
  (contract-locked liquidity stays a launch-only guarantee).

### Token page (terminal standard)
- **B1.6 Metric legend** `[app] token.js, ui.js` — every number tagged **exact** (contract state), **provable**
  (published on chain), **estimated** (derived: 2× reserve, 18 s blocks) or **unavailable** (encrypted balances),
  with a hover/tap explanation; a legend sheet. Circulating supply (supply − curve remainder − locked − creator
  unsold), absolute creation time (block × 18 s, labelled estimate), description in the header, tags rendered,
  decimals shown, install txid + link to the trade/tx inspector.
- **B1.7 Chart v2** `[app] chart.js` — timeframes 1m/5m/15m/1h/4h/1D/1W/ALL with an interval selector, OHLC bar
  mode, wheel/pinch zoom and drag pan (pointer events, no dependency), launch and graduation markers, markers for
  every published trade (not only followed ones) with a link to the row, memoised candles so a block does not
  redraw everything. Budget: chart.js ≤ 20 KB raw. Benchmark: render 1,500 candles < 16 ms on a 2019 phone
  (measure with `performance.now()` in the UI test).
- **B1.8 Transaction inspector** `[app]` — a route `#tx/<txid>` that shows what the node knows (block, ring size,
  signer when ring 2, contract call name and args when it is a Veil contract) and what the app knows (local
  record). Local trade history screen with txids.
- **B1.9 Safety panel** `[app] home.js` — factual rows, no score: supply fixed (template), no mint/freeze/admin
  (template), liquidity locked / LP can leave, creator allocation % and whether it is still unsold, tax mode and
  rate, launch age, graduation state, promotion bought (B2.3), ticker look-alike, asset provenance for pools,
  storage type of the icon (B2.6). Each row says "from contract state" or "from the template".

### Create (pump.fun standard, with the safety step)
- **B1.10 Launch wizard** `[app] forms.js` — six steps on one route with a progress rail: basics (name, ticker,
  description, icon, banner URL, website, socials, category) → economics (supply, decimals, creator allocation,
  graduation target, tax mode/rate; the fixed 80/20 split explained) → safety (who can mint: nobody; burn: nobody;
  supply change: impossible; transfers: unrestricted DERO asset; ownership: none; metadata: immutable on chain,
  images mutable at their host) → review (the full pre-launch summary, fees in DERO) → sign (exactly what the
  wallet will authorise: two transactions, their arguments) → launched (address, block, timestamp, supply, creator,
  install txid, registration status with retry, share card). Banner/website/socials/category need a new launch
  template with `bannerHdr`, `siteHdr`, `socialHdr`, `categoryHdr` `[contract]`. Mobile-first.
- **B1.11 Registration recovery** `[app]` — a verified-but-unlisted contract reached by id offers "Register on the
  factory" to its creator; the failure toast links there.

### Portfolio (first-class)
- **B1.12 Holdings v2** `[app] app.js, screens.js` — token, LP, staked, holder-NFT and Founders units in one list
  with categories and allocation %; NFT holdings with provenance (`ed_<n>`); value only where an exit quote exists,
  otherwise "no market"; realised/unrealised P&L labelled by source (local record / published on chain); fees and
  tax itemised; full history with txids; export as a file. Privacy: nothing leaves the device.
- **B1.13 Alerts** `[app]` — local, in-tab alerts (price crosses, graduation, new launch by a followed creator,
  watchlist activity) with the Notification API when granted; no server, no push.

### Collect
- **B1.14 Collection entity** `[contract+app]` — a collection is a registry entry (`collection_<n>` on the
  factory or a tiny `collection.bas`) that NFT contracts reference by id; collection page with banner, image,
  creator, description, verified, supply, minters (from `ed_<n>`), activity (mints from `minted` deltas, listings,
  sales), volume (`mdep`/`mfee`), floor from listings, items grid (per-edition where the template records it),
  socials, provenance. Standalone 1/1 and edition NFTs minted by the creator (a `mint.bas` template: fixed price,
  max supply, optional allowlist by holder proof).
- **B1.15 Marketplace for all NFTs** `[contract]` — the Founders fixed-price List/Unlist/Buy generalised into
  `market.bas` (one contract per collection or one shared escrow keyed by asset id), reading every listing with
  range keys instead of the last 300.

## P2 — where Veil pulls ahead

- **B2.1 "What is public / what is private" panel** on every page (shld.fun's one line, done properly): balances
  private; ring-2 trades name the signer; published trades name the address; private mode names the receiver; the
  follow graph is public; images reveal your IP to the host.
- **B2.2 Wins cards** `[app]` — canvas-rendered cards (token launched, NFT minted, swap, +X % position, milestone)
  generated locally, downloadable, with a verification stub: the on-chain record key (`t_<n>` of `<scid>`) and a
  local-only variant that buckets the multiple (×2–3) and hides the wallet. `navigator.share` where available.
- **B2.3 Promotion shelf** `[contract+app]` — `promo.bas`: fixed-price, duration-based slots (highlighted,
  featured token, featured NFT, banner, launch feature) paid in DERO to the treasury with a public ledger; the app
  renders a separate "Sponsored" shelf, never touches ranking or search order, shows "promotion bought: N DERO" on
  the token page's safety panel, and refuses to feature an unverified contract. Fee distribution model chosen by
  analysis (B3.2) before any split is coded.
- **B2.4 Holder-verified comments** `[contract+app]` — a per-token thread where a post carries a proof of holding
  (a signed message over a fresh ring? research: DERO has no on-chain balance proof; the honest v1 is "published
  trader" badge from `t_<n>`), strict length and link rules (no URLs in v1), rate limited by a fee to the treasury,
  hidden by default until a threshold of holders exist, and per-device mute.
- **B2.5 Creator page** `[app]` — launched tokens, collections, launch history with outcomes (graduated / stalled),
  fees earned where public, followers; reputation shown as facts (graduations, oldest live market, allocation
  history), never a score; no identity beyond what the address published.
- **B2.6 Asset pipeline v1** `[contract+app+tools]` — metadata carries `filehash` (sha256 of the bytes) and
  `filesize`; the app fetches the image, hashes it in the browser and shows verified / mismatch / unreachable; icons
  are proxied to a fixed-size canvas thumbnail (no SVG, no animation, MIME sniffed, EXIF irrelevant after
  re-encoding); storage state label: on-chain (TELA DOC ≤ 18 KB, for icons), content-addressed (IPFS CID with a
  gateway list the user can edit), centrally hosted (https), mutable/immutable. Tool `tools/nftpack` that hashes,
  resizes, strips and optionally installs an icon as a TELA DOC. Research: cost of on-chain images per KB.
- **B2.7 Programmable NFT primitives** `[contract]` — deterministic, constrained: traits derived from
  `(scid, edition, install height)` by a documented hash → trait table; time-dependent tier from hold duration
  (`lh_`), collection state machine with a fixed transition table; no code execution.
- **B2.8 Fair-launch batching** `[research→contract]` — the first N blocks of a curve fill at one price (orders
  batched per block, pro rata): removes the sniper race; research the DVM cost and whether block-level batching is
  possible without an operator.
- **B2.9 Social feed v2** `[app]` — launches, graduations, mints, listings and follows as feed events derived from
  chain state deltas; per-token announcements are out (immutable contracts) unless a `post.bas` with a fee exists.
- **B2.10 Mobile polish** `[app]` — 480–960 layout, `dvh`, swipe-to-dismiss sheets, focus trap, no nested buttons,
  chart gestures; Playwright at 390/430/768/1024/1280.

## P3 — research and later

- **B3.1 Economic model** — per revenue stream (creation fee, NFT fee, promotion, registration, trade fee share,
  marketplace fee, premium analytics) who pays, who receives, sybil/perverse incentives, regulatory shape; pick
  the simplest sustainable set. Founders Pass gets the treasury share; promotion revenue split analysed
  (holders/creators/LPs/treasury) with dust-holder and snapshot attacks in mind.
- **B3.2 Fee-distribution attack analysis** for any holder/creator distribution: sybil, dust, snapshot, wash, NFT
  farming, cost of distribution (PayBatch gas), privacy (who receives is public), accounting.
- **B3.3 Indexer** — a Gnomon-style read cache stays optional and verify-against-node; wire or remove the toggle.
- **B3.4 BlackBox readiness** — abstractions that survive a DERO where contract state is not globally readable:
  every read goes through `V.node` with a "what would we need from a proof" note per screen.
- **B3.5 Public proof pages, QR asset pages, saved searches, transaction bookmarks, offline metadata bundles,
  portable NFT bundles, deterministic receipts.**

## W — the XMR/ZEC wrapper (added loop 17, after two audits)

The contract side is v3 and tested; the foreign side does not exist. Nothing here may reach mainnet before all of
W1–W5 are done and a testnet pilot has run. Full build instructions: `docs/HANDOVER.md` section 5.

- **W1 Port `tools/vaultsigner` to v3** `[daemon]` — **done 2026-09-07** (loop 18): new Mint call, exact reserve check, self-proving votes, deposit-ordering rule, veto path, halt-once breaker, vault-bound state, signal-safe lock. Verified by `tools/simvault/e2e.sh`. Original scope: The daemon speaks v2 and now refuses to start
  against a v3 vault. Needs: the new `Mint(j, txid, vout, amount)` call; the simplified reserve check
  (`reserve >= sup`, no pending-burn arithmetic); the new counter-key shapes (operational scopes carry `he` and a
  2000-block bucket); handling for `Veto` and `Refunded`; and the rule that a signer never votes `Veto` once its
  own intent reached `paying`. Done: `go test ./vaultsigner` green and the simulator end-to-end script passes.
- **W2 Backend contract** `[daemon]` — add `Finalized()`, `Verify(deposit)` and `Health()` to the `Backend`
  interface and refuse to vote above the finalized height, re-reading the deposit at vote time. Refuse to credit
  any deposit older than `dh_<j>` (an address for a future index can be paid before anyone owns it).
- **W3 Monero backend** `[tools/foreign/monero]` — stagenet wallet-rpc multisig; per-index subaddresses;
  deterministic transaction construction so every signer builds the same unsigned transaction; idempotency by tx
  note `dmz:j=<j>`; integration test behind a build tag plus recorded fixtures for the parsing.
- **W4 Zcash backend** `[tools/foreign/zcash]` — Orchard-only unified address, a scanning sidecar holding only
  the full viewing key, FROST signing extended from `research/crosschain/poc/frost-zec`, golden vectors that the
  Go and Rust sides must agree on.
- **W5 Adversarial / soak / replay harness** `[tests]` — a deterministic replay of a recorded operation sequence
  against the vault plus a long soak, so a change is proved not to alter behaviour. The operator has a
  chain-agnostic harness of this shape in another repository; check it before writing a new one.
- **W6 `Compensate(dest, amount)`** `[contract]` — slashed bonds currently go to the treasury, so victims are
  never made whole (external audit F4). A k-vote payout from a `slashpool` is the smallest honest fix.
- **W7 Wallet-RPC authentication** `[tools/simrpc]` (external audit F5) — the client sends no credentials;
  `netwallet` already accepts `-rpc-login`. Add it to the client and require it on mainnet.
- **W8 Treasurybot finality and sweep cost** `[tools/treasurybot]` (external audit F7) — gate claims and deposits
  on the stable height so a reorg cannot strand a recorded claim, and stop sweeping `PayBatch` over every holder
  batch when little is pending.
- **W9 `netcfg` unknown-key handling** (external audit F12) — a typo'd network name silently becomes an empty
  network; fail closed instead.
- **W10 Documentation contradictions** (external audit F13) — `docs/ARCHITECTURE.md` still describes the app
  reading contracts with `variables:true`; `DECISIONS.md` says images default on while the app ships them off.
- **W11 `capratio` is a frozen price assumption** — if the foreign asset appreciates against DERO the bond bound
  weakens in real terms and nothing on chain notices. Decide whether a periodic k-of-n review is enough, and say
  so in the trust statement.

## B3.6 — signer coordination over DERO transaction payloads

Monero multisig rounds and FROST nonce exchange need an authenticated channel between signers. The current design
hand-waves "a small relay or a shared directory", which is a new service, a new port and a new trust assumption.
The operator's Spore work (private P2P messaging riding DERO transaction payloads) suggests carrying those rounds
inside DERO transactions instead: no new service, the coordination is auditable, and it fails closed with the
chain. Research what a round trip costs in fees and latency, and whether payload size bounds the protocol.

## Measured baseline (2026-09-06)

| | raw | gzip |
|---|---|---|
| app/*.js (19 files) | 202,869 B | 63,252 B |
| app/*.css (4 files) | 32,551 B | 8,091 B |
| index.html | 3,294 B | — |
| total | 238,714 B | ~72 KB |

Zero external dependencies, no bundler, 28 TELA DOCs (154,578 B on chain, ~1.64 DERO to install). Chart:
12,911 B pure SVG. Contract suite: 389 s. Budgets from here: total JS ≤ 300 KB raw / ≤ 90 KB gzip; no feature adds a
dependency; every new screen renders in the UI pass at 430 and 1280.
