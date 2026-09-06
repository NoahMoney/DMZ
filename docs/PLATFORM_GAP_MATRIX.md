# Veil platform gap matrix (2026-09-06)

Reconnaissance for the platform directive of 2026-09-06 (pump.fun + shld.fun + DexScreener + NFT marketplace +
portfolio tracker, DERO-native). Method: six independent readers inventoried the repository by capability cluster
(create, discover, trade, collect/track/share/social, infrastructure, contracts/economics), every claim tied to a
`file:line`; one reader benchmarked the competitors; one synthesis produced the matrix below. The line numbers are
those of commit `f42d12d`. Nothing in this file is a plan; the plan is `docs/PLATFORM_BACKLOG.md`, which cites the
rows here by capability.

**Status after loop 16 (same evening):** the P0 rows are closed in code — templates regenerated from `contracts/`
(B0.1), fee/pfee checked at verification (B0.2), `…To` calls never publish (B0.3), one holder-NFT edition per
address and `holdblocks ≥ 1` (B0.4), Founders `minhst` gate (B0.5), Buyback capped and spaced (B0.6), Losers sort
(B0.7), stale claims in RISKS/MASTER_TODO corrected (B0.8). The rows below still describe the state the readers
saw, so the evidence stays checkable; the backlog says what changed.



Sources: the evidence inventory (six clusters, `file:line` cites) and the competitor benchmark (pump.fun, shld.fun, DexScreener, Magic Eden, OpenSea, Axiom/Photon/GMGN/Jupiter, Bonk.fun/Bags/Zora/Believe/Jupiter Studio). Counts below are of the 181 inventoried capabilities: 74 existing, 54 partial, 53 missing; 41 of the existing/partial ones are superficial.

## 1. Where the product stands

Veil is a 238,714 B (72,272 B gzipped), zero-dependency app served from the chain via TELA (deploy/README.md (TELA):1-20) that already has the hard core: hash-verified launch templates with a bonding curve graduating into contract-locked AMM liquidity (contracts/launch.bas:107-139), an exact BigInt quote mirror (app/amm.js:14-67), ring-16 private buys (app/app.js:180-192), transaction outcome proofs rather than "confirmed on inclusion" (app/app.js:159-179), a pure-SVG chart (app/chart.js:1-122), local-only portfolio P&L (app/app.js:202-214), holder-reward NFTs (contracts/nft.bas:42-97) and a Founders Pass fee share with a tested treasury bot (tools/treasurybot). Against pump.fun, DexScreener and the Solana terminals the visible gaps are the token page's social surface (no socials, website, banner, comments, announcements or creator page), no alerts, no share cards, substring-only search, NFT "collections" that are fungible editions with no items, owners, floor or activity (contracts/nft.bas:79-80), and a hot score a dust swap per block can buy (app/stats.js:60; contracts/launch.bas:96). The deeper problem is docs versus shipped code: app/templates/launch.bas lags contracts/launch.bas (sha bc05b8e3 vs 8c0bf10d), MASTER_TODO C7/C9-C13 read as done but are absent from the contracts, verification never checks fee/pfee (app/feed.js:92-98), and the `...To` entrypoints let one payer publish trades under any address (contracts/launch.bas:405-410). The privacy constraints (encrypted balances, unpublished trades) are handled honestly in places ("Public traders", "Public trades 24h") but not systematically, and systematic honesty about what the chain can and cannot show is the product's only credible differentiator.

## 2. Feature parity matrix

✓ marks the status; "Existing" rows are kept so nothing gets rebuilt. Cites are in the Capability cell.

### CREATE

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| Launch wizard steps (app/forms.js:24-26,70-83) |  | ✓ |  | pump.fun: one form, live in seconds; Studio adds vesting/locks | Keep one sheet; add per-field validation and review state, not steps |
| Fields: name, ticker, description, icon (app/forms.js:27; contracts/launch.bas:12-13) | ✓ |  |  | Name, ticker, image, description everywhere | Keep; render stored tags or drop field (CREATOR_GUIDE.md:27 is wrong) |
| Fields: banner, website, socials, category, docs (contracts/launch.bas:6,28-35) |  |  | ✓ | Socials and website on every launchpad token page | New hash-verified template with socials/website keys; app renders links |
| Economics: supply and decimals (app/forms.js:28,38-46) | ✓ |  |  | Fixed defaults, rarely configurable | Keep; parser mirrors contract limits |
| Economics: creator / holder / liquidity allocation (contracts/launch.bas:16,22-25) |  | ✓ |  | Jupiter Studio vesting to 80%; Bags creator splits | Vesting as on-chain commitment (C12); keep fixed 80/20 |
| Economics: burn, emission, permissions (contracts/launch.bas entrypoints; README.md:9-10) |  |  | ✓ | Mint/freeze authority shown as safety line | Keep absent by design; print "no mint, no burn, no admin" |
| Review / sign / launch steps (app/forms.js:84-94; app/app.js:94-158) |  | ✓ |  | Single confirm; token live instantly | Review screen with fee schedule; retry failed RegisterLaunch (T5) |
| Post-launch summary (app/forms.js:93; app/token.js:53-54) |  | ✓ |  | Token page plus share-to-X card | Launch receipt: txid, height, registration status, proof link |
| NFT creation: single 1/1 (app/forms.js:109-125; contracts/nft.bas:13-14) |  |  | ✓ | Magic Eden/OpenSea no-code creator studio | Decide: 1/1 template, or state "editions only" plainly |
| NFT creation: editions (contracts/nft.bas:31,65-80) | ✓ |  |  | Edition mints with counters | Keep; surface `ed_<n>` numbering |
| NFT creation: collection field / grouping (contracts/nft.bas:13,23; app/nfts.js:81-85) |  | ✓ |  | Collections group items with floor and owners | Group contracts by collection string, or drop the field |
| NFT creation: media types (contracts/nft.bas:19; app/nft.js:14,17) |  | ✓ |  | Image, animated, audio, video with player | Images only, stated plainly; no CSP widening (N2) |
| NFT creation: traits / attributes (contracts/nft.bas:4-7) |  |  | ✓ | Trait filters, trait floors | N4 state-derived traits (P2), after hash tiers |
| NFT creation: rarity (contracts/nft.bas:31,34) |  |  | ✓ | Rarity rank, colour-coded | None until traits exist; say "editions are identical" |
| NFT creation: provenance (contracts/nft.bas:26,79; app/nft.js:19,26) |  | ✓ |  | Provenance/activity tab per item | Read `ed_<n>` minter log; add filehash (N5) |
| Collection creation, standalone entity (contracts/factory.bas:81-87; app/screens.js:86-87) |  |  | ✓ | Collection is the primary entity | Collection page grouping contracts; no new contract needed |
| Image upload / branding / banners (app/forms.js:7,27,112; app/ui.js:26) |  | ✓ |  | Upload to platform CDN or IPFS | URL-only stays; add content hash; banner via new template |

### DISCOVER

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| Trending (app/stats.js:58-60; app/home.js:64,84) | ✓ |  |  | DexScreener: 5m/1h/6h/24h on volume, txns, unique buyers | Keep; add decay and multi-window (see ranking row) |
| Hot badge / sort (app/stats.js:60; app/home.js:8) | ✓ |  |  | Trending and boost badges on rows | Badge on rows too; threshold from decayed score |
| Newest (app/feed.js:46-49,194) | ✓ |  |  | New-pairs column seconds after liquidity | Keep; include pools; NEW badge on rows |
| Highest volume (app/stats.js:22-37,50) | ✓ |  |  | 24h volume column, sortable | Keep; label "net reserve flow" |
| Gainers (app/markets.js:15,25) | ✓ |  |  | Gainers screen ordered by change | Order by change when filter active |
| Losers (app/markets.js:16,25) |  | ✓ |  | Losers ordered by change | Fix ascending-by-active-key bug |
| Most traded (app/stats.js:36,50,60) |  | ✓ |  | Txns and makers columns 5m..24h | Sort by active blocks; label "blocks with trades" |
| Most holders (app/stats.js:56; app/token.js:38) |  |  | ✓ | Holder count, sortable | Privacy: no holders; sort "public traders" with honest label |
| Fastest growing (app/feed.js:195; app/stats.js:48) |  | ✓ |  | Unique-buyer growth; about-to-graduate column | Make 1h/6h change sortable; graduation column |
| Under-the-radar (app/markets.js:5-27) |  |  | ✓ | Rare; low-cap/age filters approximate it | Filter: liquid, low attention, rising blocks |
| NFT trending (app/nfts.js:52,82-84) |  |  | ✓ | Collection volume and floor rankings | Mint velocity from minted-count deltas per block |
| Creator trending (app/screens.js:40-45) |  |  | ✓ | Trader leaderboards, creator profiles | Creator page listing launches (F2); no leaderboard |
| Sorting / filtering surfaces (app/markets.js:5-27,50-77) | ✓ |  |  | Filters by age, cap, volume, chain | Keep; fix sort under Graduating; min-liquidity filter |
| Search: tokens, tickers, NFTs, creators, addresses, tx ids (app/markets.js:12,51) |  | ✓ |  | Search by name or address, on mobile too | Mobile search; creators, addresses; fold via tickerKey |
| Search: fuzzy matching (app/markets.js:12; app/ui.js:25) |  |  | ✓ | Typo-tolerant, ranked results | Use tickerKey skeleton; rank by hot |
| Ranking algorithm, hot score (app/stats.js:58-60) | ✓ |  |  | Volume, txn count, unique-buyer growth; paid boosts | Decay, age normalisation, streak penalty; publish formula |
| Transparency of why something trends (app/home.js:8,27,84) |  | ✓ |  | "Boosted" badge only; formula hidden | "Why trending" line showing the three terms |

### TRADE

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| Token icon (app/token.js:28; app/ui.js:26) | ✓ |  |  | Image always loaded | Keep off-by-default; explain IP trade-off inline |
| Banner image (contracts/launch.bas:6,28-32) |  |  | ✓ | Banner on token pages | Only via new template; low priority |
| Name (app/token.js:28) | ✓ |  |  | Name shown | Keep |
| Ticker (app/token.js:28; app/ui.js:25) | ✓ |  |  | Ticker with verified badge | Keep look-alike pills |
| Description (app/token.js:56) |  | ✓ |  | Description in header beside socials | Show in header pane, not only Info tab |
| Creator (app/token.js:54; app/home.js:11-15) | ✓ |  |  | Dev wallet tagged in holder list | Keep; link to creator page (F2) |
| Creation time (app/feed.js:46-49; app/stats.js:54) |  | ✓ |  | Pair age plus absolute date | Show height and "estimated"; date from block |
| Price (app/amm.js:95; app/token.js:30-32) | ✓ |  |  | Live price with change chips | Keep |
| Market cap, circulating (app/stats.js:51) |  |  | ✓ | "Market cap" shown, usually curve-implied | Do not add; keep FDV with "curve-implied" hover |
| FDV (app/stats.js:51; app/token.js:37) | ✓ |  |  | FDV and market cap side by side | Keep; hover explains computation |
| Liquidity (app/stats.js:52; app/token.js:37,54) | ✓ |  |  | Liquidity USD, LP lock status | Annotate 2x rd; add exit-impact at 1/5/10% |
| 24h volume (app/stats.js:20-37,50) |  | ✓ |  | Gross volume 5m..24h | Label "net flow (verified)"; explain what nets out |
| Transaction count (app/token.js:37,53) |  | ✓ |  | Txns per window | Keep "Public" prefix; show active-block lower bound |
| Buys / sells (app/token.js:38-40) |  | ✓ |  | Buys/sells, buyers/sellers per window | Keep; label unpublished sides unavailable |
| Holders count (app/stats.js:56; app/token.js:38,48) |  | ✓ |  | Holder count with dev/curve tags | "Holders: private by design"; show public traders |
| Supply (app/token.js:54-55) | ✓ |  |  | Total supply | Keep; show decimals |
| Circulating supply (app/feed.js:102-103; app/home.js:11) |  |  | ✓ | Circulating supply shown | Supply bar: sold, curve, locked, staked, creator (G2) |
| Graduation progress (app/token.js:26,33) | ✓ |  |  | Bonding-curve progress bar | Keep |
| Chart (app/token.js:17-24; app/chart.js:31-101) | ✓ |  |  | TradingView chart | Keep pure SVG; memoise redraws; accessibility |
| Recent transactions list (app/token.js:44; app/home.js:40-49) |  | ✓ |  | Full txn list with wallets | Fix dest attribution ("delivered to"); keep published-only note |
| Holder distribution (app/token.js:45-50) |  |  | ✓ | Top 10 %, Dev %, Bundle % | Privacy: replace with protocol facts (allocation, locked reserve) |
| Top holders (app/token.js:46-50) |  | ✓ |  | Top holders by balance | Rename tab "Public traders"; keep caption |
| Liquidity info (app/token.js:53-55; app/sheets.js:7-25) | ✓ |  |  | Pooled tokens, LP status | Keep; add LP-withdrawable warning for pools (A3) |
| Metadata (app/token.js:53-54) |  | ✓ |  | Contract, pair addresses, socials | Render tags and decimals, or drop tags |
| Socials (contracts/launch.bas:6,12-13) |  |  | ✓ | Twitter/Telegram/Discord links | New template keys; render https links only |
| Website (contracts/launch.bas:6) |  |  | ✓ | Website link | Same template change as socials |
| Candles (app/chart.js:57-62) | ✓ |  |  | OHLC candles | Keep; render empty intervals as "no trades" |
| Line chart (app/chart.js:52-55) | ✓ |  |  | Line/area mode | Keep |
| OHLC bar mode (app/token.js:22) |  |  | ✓ | Bar mode in TradingView | Not needed; tooltip carries OHLC |
| Volume bars (app/chart.js:47-51) | ✓ |  |  | Volume histogram | Keep |
| Timeframes 1m..ALL (app/token.js:21; app/stats.js:88-97) |  | ✓ |  | 1m..1w intervals independent of range | Interval selector; 1m only where blocks allow |
| Zoom (app/chart.js, no wheel/pinch) |  |  | ✓ | Wheel and pinch zoom | G1 backlog; wheel zoom first |
| Pan (app/chart.js, no drag) |  |  | ✓ | Drag to pan | G1 backlog with zoom |
| Crosshair (app/chart.js:76-92) | ✓ |  |  | Crosshair with axis tags | Keep |
| Tooltips (app/chart.js:82,94-97) | ✓ |  |  | OHLC tooltip | Keep; replace innerHTML with text nodes |
| Trade markers (app/token.js:18; app/chart.js:72) |  | ✓ |  | Own trades marked on chart | Link marker to trade row; opt-in all published |
| Launch / graduation marker (contracts/launch.bas:139; app/feed.js:103) |  |  | ✓ | Migration marker on chart | Draw gh line from loaded height |
| Chart implementation, chart.js (app/chart.js:1-122) | ✓ |  |  | TradingView dependency | Keep zero-dependency SVG; memoise; aria |
| Swap: buy (app/token.js:71,81-144; app/app.js:183-196) | ✓ |  |  | One-click presets, priority fees | Keep |
| Swap: sell (app/app.js:193; app/token.js:142) | ✓ |  |  | Quick-sell percentages | Keep |
| Quote (app/amm.js:14-24,40-67) | ✓ |  |  | Route, impact, min received | Keep exact BigInt mirror |
| Slippage (app/token.js:115; app/app.js:9,12) | ✓ |  |  | Presets plus custom value | Custom value inside the 10% cap |
| Private mode (app/token.js:116-117; app/app.js:180-192) | ✓ |  |  | None; every wallet public | Grey out "Post this trade"; explain dest verification |
| Transaction inspection (app/home.js:47; app/app.js:143) |  | ✓ |  | Explorer link per trade | Local trade list with txid; explorer link |
| Transaction outcome verification (app/app.js:88-179) | ✓ |  |  | "Confirmed" on inclusion | Keep included-versus-confirmed distinction |

### COLLECT

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| NFT pages (app/nft.js:6-33) |  | ✓ |  | Per-item page | Read `ed_<n>`; edition list on collection page |
| Collection page (app/nft.js:15-32) | ✓ |  |  | Floor, owners, volume, traits, activity | Keep; add what the chain allows (rows below) |
| Collection: banner (contracts/nft.bas:20-21) |  |  | ✓ | Banner image | New template only; low priority |
| Collection: image (app/nft.js:14,16-17) | ✓ |  |  | Image always loaded | Keep opt-in; add onerror |
| Collection: creator (app/nft.js:19,26) | ✓ |  |  | Creator link | Keep |
| Collection: description (app/nft.js:18) | ✓ |  |  | Description | Keep |
| Collection: verified (app/nft.js:16; app/ui.js:77-81) | ✓ |  |  | Curated verified badge | Keep; label "template verified", not identity |
| Collection: supply (app/nft.js:19,26) | ✓ |  |  | Items and minted count | Keep |
| Collection: owners (contracts/nft.bas:79; app/nft.js:9) |  |  | ✓ | Owners count | "Owners: private"; list original minters from `ed_<n>` |
| Collection: activity (contracts/nft.bas:79; contracts/founders.bas:225-228) |  |  | ✓ | Mint/sale/transfer feed | Mint events from `ed_<n>` growth per block |
| Collection: volume (app/nfts.js:75) |  |  | ✓ | Collection volume | Founders: derive from mdep and mfee; label estimated |
| Collection: floor (app/founders.js:24) |  |  | ✓ | Floor price | Label lowest Founders listing as floor |
| Collection: traits (contracts/nft.bas:4-5) |  |  | ✓ | Trait filters | N4, P2 |
| Collection: rarity (contracts/nft.bas:80) |  |  | ✓ | Rarity rank | None; state editions are identical |
| Collection: items (contracts/nft.bas:79-80) |  |  | ✓ | Item grid | Edition list from `ed_<n>` (number, minter) |
| Collection: socials (contracts/nft.bas:4-5) |  |  | ✓ | Socials on collection | New template keys; render or drop tags meanwhile |
| Collection: provenance (app/nft.js:26; contracts/nft.bas:79) |  | ✓ |  | Provenance tab | Surface `ed_<n>`; add filehash (C10 not done) |
| NFT marketplace, list/buy/sell (contracts/founders.bas:177-234; app/nfts.js:51-70) |  | ✓ |  | Buy, offer, sweep, collection offers | Fixed-price for holder NFTs; read all listings, not 300 |
| Holder-reward NFTs (contracts/nft.bas:42-97; app/nft.js:28-31) | ✓ |  |  | No direct equivalent | Keep; fix re-lock farming (C10) |
| Programmable NFT primitives (contracts/nft.bas:7; contracts/founders.bas:12) |  | ✓ |  | Custom contracts allowed | Fixed templates by design; N4 trait DSL later |

### TRACK

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| Portfolio: total value (app/app.js:75-85) | ✓ |  |  | Portfolio value with PnL card | Include LP, staked and NFT units |
| Portfolio: token holdings (app/screens.js:24-27) | ✓ |  |  | Open positions list | Keep; read pool assets |
| Portfolio: NFT holdings (app/app.js:78-80; app/nfts.js:14-16) |  |  | ✓ | Owned NFTs tab | Read nft and founders balances |
| Portfolio: allocation (app/screens.js:24-27) |  |  | ✓ | Allocation pie or percent | Percent column; category split |
| Portfolio: history (app/app.js:219-238) | ✓ |  |  | Equity curve | Keep; label private-mode exclusions |
| Portfolio: realized/unrealized P&L (app/app.js:202-214) | ✓ |  |  | PnL public on profile | Keep local-only; itemise fees (F3) |
| Portfolio: tx history (app/screens.js:20,28) |  | ✓ |  | Full activity list with export | List local trades with txid; mints, locks, LP; export |
| Asset breakdown (app/screens.js:24-27; app/app.js:80) |  | ✓ |  | Category split | Tokens / LP / staked / NFTs with percent |
| Watchlist: tokens/markets (app/stats.js:113-118) | ✓ |  |  | Multiple watchlists, synced | Keep; stop per-row localStorage parse; export (F3) |
| Watchlists: persistence, notes (app/stats.js:113-118; app/screens.js:82) | ✓ |  |  | Synced across devices, notes | Export/import; no sync server |
| Watchlist: NFTs and collections (app/nfts.js:81-85) |  |  | ✓ | Watch collections | Star on NFT page and tab |
| Watchlist: creators (app/app.js:197; app/feed.js:172-188) |  | ✓ |  | Wallet tracker, follow | Private local creator watch, distinct from public Follow |
| Alerts/notifications (app/ui.js:36-39) |  |  | ✓ | Price/volume alerts, push | Local alerts while open; no push server |
| Notifications: toasts, push, service worker (app/ui.js:36-39; app/live.js:35-53) |  | ✓ |  | Real-time notification centre | Local persisted inbox; no service worker yet |

### SHARE

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| Share card, post-launch (app/token.js:29; app/index.html:6) |  |  | ✓ | Share-to-X preview card | Local canvas card, no CSP change (R6) |
| Share cards, trades/PnL (docs/history/HISTORY.md (earlier backlog):333-335) |  |  | ✓ | PnL share image | Bucketed private card, opt-in (R6) |
| Sharing: trades (app/token.js:117,130) |  | ✓ |  | Trade share link | Per-trade URL to `t_<n>`; navigator.share |
| Sharing: wins (app/app.js:202-214) |  |  | ✓ | PnL card | Verifiable card without revealing the wallet |
| Sharing: launches (app/token.js:8,29) |  | ✓ |  | Preview card on launch | Offer share when launch completes |
| Sharing: NFTs (app/nft.js:26) |  |  | ✓ | Share link with card | Copy/share control on NFT page |
| Sharing: portfolio milestones (app/screens.js:9-31) |  |  | ✓ | Milestone cards | Low priority; opt-in only |
| Proofs (contracts/README.md:39-41; app/feed.js:127) |  | ✓ |  | Explorer link as proof | Proof route verifying `t_<n>` against node (R7) |

### SOCIAL

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| Creator profile: created tokens (app/screens.js:34-52) |  |  | ✓ | Creator's launches listed | Filter launches by s.creator (F2) |
| Creator profile: collections (app/screens.js:34-52; app/feed.js:108) |  |  | ✓ | Creator's collections | Filter NFTs by n.creator |
| Creator profile: launch history (app/feed.js:46-49) |  |  | ✓ | Launch history, graduation rate | Timeline from install heights |
| Creator profile: stats (app/screens.js:41,45,47) |  | ✓ |  | Public PnL, followers | Add launches, graduations; keep PnL private |
| Creator profile: links (contracts/social.bas:15-26; app/forms.js:126-130) |  |  | ✓ | Socials on profile | Add link keys to social.bas |
| Creator profile: reputation (docs/history/HISTORY.md (earlier backlog):337) |  |  | ✓ | Verified badge, dev history | Chain-state reputation: launches, graduations, allocations (R8) |
| Social: follow (contracts/social.bas:28-65; app/app.js:197) | ✓ |  |  | Follow, copy-trade | Keep; disclose "public on chain" at tap; fix fc_ |
| social.bas: profiles and follows, contract (contracts/social.bas:15-65) | ✓ |  |  | Unique handles | Name claim (C11); fl_ cleanup |
| Social: comments (contracts/social.bas:3-4) |  |  | ✓ | Comment threads with replies | Decide (MASTER_TODO:357); verified-holder badge if built |
| Social: replies (contracts/social.bas) |  |  | ✓ | Threaded replies | Follows the comments decision |
| Social: announcements (contracts/social.bas:15-26) |  |  | ✓ | Creator updates on token page | Post entrypoint on social.bas keyed by scid |
| Social: activity feed (app/feed.js:196-204; app/home.js:40-56) |  | ✓ |  | Feed of trades, launches, mints | Add launches, graduations, mints, listings (F4) |

### SAFETY

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| Safety explanations: mint/burn, supply mutability (app/forms.js:70; app/token.js:53-54) |  | ✓ |  | Mint/freeze authority lines | Explicit rows: no mint, no burn, fixed supply (T1) |
| Safety explanations: transfer restrictions, ownership, metadata (contracts/launch.bas:247-257) |  |  | ✓ | Freeze authority, ownership renounced | State: no admin key, immutable metadata, free transfer |
| Search: impersonation protection (app/ui.js:21-25,77-86) | ✓ |  |  | Verified badges only | Fold search via tickerKey; mark original by height |
| Anti-manipulation: wash trading (app/stats.js:29-36,58-60) |  | ✓ |  | None; boosts reward it | Decay, streak penalty, cross-block netting |
| Anti-manipulation: self-trading (app/stats.js:41) |  |  | ✓ | Not detected anywhere | Detect same-address round trips in published records |
| Anti-manipulation: spam (app/stats.js:41,60; contracts/launch.bas:83-91) |  | ✓ |  | Server-side rate limits | Mark `...To` records "delivered to"; minimum size |
| Anti-manipulation: fake volume (app/stats.js:22-37,60) |  | ✓ |  | Volume trivially inflatable | Keep reserve-derived; add wash-adjusted view |
| Anti-manipulation: sybil (contracts/launch.bas:83-91,405-410) |  |  | ✓ | Bundle % heuristics | Cap publisher term; do not count dest |
| Anti-manipulation: fake holders (app/stats.js:56; app/token.js:38,48) |  | ✓ |  | Holder counts inflated by bundles | Keep "Public traders"; never rank on it |
| Anti-manipulation: liquidity manipulation (app/stats.js:22-27,52) |  | ✓ |  | LP lock/burn status | Time-weighted liquidity; LP-withdrawable chip (A3) |
| Verification badge tiers (app/feed.js:52-56,92-97; app/ui.js:77-81) | ✓ |  |  | Audit tab, third-party scanners | Keep; also check fee/pfee |
| factory.bas: verification by template hash (contracts/factory.bas:1-3; app/feed.js:92-98) |  | ✓ |  | Contract scanner | Compare fee/pfee to config; correct RISKS R-3 |
| Risk indicators (app/home.js:13-20; app/token.js:34-35) | ✓ |  |  | Safety strip: Top10, Dev, Snipers, Bundle | Keep four flags; add allocation/tax on listings |
| Safety panel signals (app/token.js:34-35,53,108-111) |  | ✓ |  | Dedicated safety panel | "Standard curve, unmodified" checklist; pool LP risk |
| Metric labelling (app/token.js:23,37-38) |  | ✓ |  | Unlabelled market cap and volume | Per-metric hover: known / netted / estimated / unavailable |
| Anti-spam / anti-scam: templates, registry, treasury, confusables (app/config.js:12-17; app/feed.js:41-56) | ✓ |  |  | Verified badge, paid promotion | Keep; dedupe names; widen fold table |
| Moderation: delist, hide, mute, block, report (contracts/factory.bas:6-90) |  |  | ✓ | Centralised delisting | Local hide/mute only; no delist by design |

### INFRA/PERFORMANCE

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| Indexing / data read path (app/rpc.js:69-94; app/feed.js:23-40) | ✓ |  |  | Centralised indexer backend | Keep node-direct; range-read social (R-12) |
| Optional Gnomon indexer read cache (app/config.js:18; app/screens.js:64) |  | ✓ |  | Indexer is the source of truth | Wire or remove toggle; verify-against-node if wired |
| App files on-chain via TELA (deploy/README.md (TELA):1-20) | ✓ |  |  | Domain-hosted SPA | Keep; minify to cut DOC count |
| Asset hosting model (contracts/launch.bas:30; app/ui.js:26,59) |  | ✓ |  | Platform CDN or IPFS | Add content hash (N1/N5); no IPFS gateway |
| Image storage: hashes, thumbnails, MIME, EXIF (app/ui.js:26,51-60) |  | ✓ |  | Thumbnails, MIME checks, EXIF strip | Hash tier only; no processing pipeline |
| Caching (app/feed.js:10-12,58-88) | ✓ |  |  | Server cache | Keep; add quota and eviction |
| CSP and endpoint policy (app/index.html:6-7; app/rpc.js:96-111) | ✓ |  |  | Loose CSP, third-party scripts | Drop style unsafe-inline (app/ui.js:106) |
| Wallet integration, XSWD (app/xswd.js:1-118) | ✓ |  |  | Embedded custodial wallets | Keep self-custody; Engram only (R-7) |
| Analytics / telemetry (app/screens.js:71; app/index.html:6) |  |  | ✓ | Full tracking, ads | Keep absent by design |
| Mobile / responsive design (app/styles.css:63-82; app/desk.css:1-63) | ✓ |  |  | Native mobile apps | 480-960 layout; dvh; swipe-dismiss; focus trap |
| Lightweightness (wc -c app/*) | ✓ |  |  | Multi-MB bundles | Keep zero dependencies; 72 KB gzipped |
| Test coverage of the app (tools/uitest/ui.mjs:1-246) |  | ✓ |  | CI pipelines | JS unit tests (amm, bech32); CI (Q6/Q7) |

### ECONOMICS

| Capability | Existing | Partial | Missing | Competitor standard | Our target |
|---|---|---|---|---|---|
| launch.bas: fixed supply (contracts/launch.bas:14,37,66-68) | ✓ |  |  | Fixed supply is the norm | Keep |
| launch.bas: creator allocation (contracts/launch.bas:16,22,67-68) | ✓ |  |  | Vesting available (Jupiter Studio) | Vest alloc (C12); risk chip on listings |
| launch.bas: creator tax, modes 1/2/3 (contracts/launch.bas:247-340) | ✓ |  |  | 0.3-1% creator fee share | Keep; min_out on Buyback; show mode on page |
| launch.bas: graduation to built-in AMM (contracts/launch.bas:107-139) | ✓ |  |  | Auto-migration to DEX pool | Keep; locked by construction |
| launch.bas: trade fee and protocol split (contracts/launch.bas:19,99,142-148) | ✓ |  |  | 1.25% curve plus 1% terminal | Print schedule on Create and every buy panel |
| pool.bas: fee split, LP vs protocol (contracts/pool.bas:150-159) | ✓ |  |  | LP fee share | Keep |
| nft.bas: mint price and max supply (contracts/nft.bas:14,31-33) | ✓ |  |  | Mint price, supply, allowlist phases | Keep; phases not planned |
| nft.bas: holder rewards (contracts/nft.bas:42-97) |  | ✓ |  | Rewards and points programs | Fix re-lock farming; reject holdblocks 0 (C10) |
| nft.bas: metadata mutability (contracts/nft.bas:17-22) | ✓ |  |  | Mutable metadata common | Add filehash so art is pinned (N5) |
| nft.bas: who can mint / burn (contracts/nft.bas:42-83) | ✓ |  |  | Creator-controlled mint | Keep; document on page |
| founders.bas: fee share to pass holders (contracts/founders.bas:57-101) | ✓ |  |  | Points or token rewards | Fix first-registrant sweep; reject price 0 (C9) |
| factory.bas: registration fee (contracts/factory.bas:90-97) | ✓ |  |  | Free listing (DexScreener) | Dedupe; refund overpay; keep no delist |
| swap.bas / board.bas: escrow and order board (contracts/swap.bas:19-97) | ✓ |  |  | Limit orders, DCA | Keep; no fee |
| vault.bas: wrapper fees (contracts/vault.bas:29,49-50,239-248) |  | ✓ |  | No equivalent | Payout entrypoint, or drop feepool |
| Fee collection end to end (app/config.js:11; app/token.js:53) | ✓ |  |  | Published fee schedule | Keep; show on every buy panel |
| Founders share delivery, treasurybot (tools/treasurybot/main.go:119-170) | ✓ |  |  | Protocol buybacks | Sweep nft_ too; include fa fees |
| Promotion / paid placement (app/stats.js:57-60) |  |  | ✓ | Boosts, Enhanced Info, ads | Never in ranking; separate "Sponsored" shelf if ever |
| Revenue streams present today (contracts/pool.bas:223-237; contracts/founders.bas:70) | ✓ |  |  | 1% terminal fees plus promotion | Keep five inflows; no promotion revenue |
| Incentive and sybil resistance (app/stats.js:57-60; contracts/launch.bas:405-412) |  | ✓ |  | Heuristics on transparent chains | Close E2/F1/C9-C12; do not count dest |

## 3. Superficial implementations

Items the inventory flags as superficial: the capability is nominally present but a user relying on it would be misled or blocked.

**CREATE**
- Safety explanations, mint/burn and supply (PARTIAL): copy says "fixed supply, nothing to rug" but no mint/burn-authority rows; T1 open. app/forms.js:70,83; app/token.js:53-54; docs/history/HISTORY.md (earlier backlog):242-243
- Post-launch summary (PARTIAL): routes to the token page; txid only in a toast, no timestamp, proof link or registration status. app/forms.js:93; app/app.js:143,154; docs/history/HISTORY.md (earlier backlog):246
- NFT collection field (PARTIAL): free-text <=64 subtitle; lists are flat, no grouping. app/forms.js:112; contracts/nft.bas:13,23; app/nfts.js:81-85
- NFT media types (PARTIAL): typeHdr hardcoded "Image"; one `<img>`; CSP has no media-src. contracts/nft.bas:19; app/nft.js:14,17; app/index.html:6
- NFT provenance (PARTIAL): `ed_<n>` minter log on chain but never read; no filehash. contracts/nft.bas:79; grep `ed_` in app/*.js empty; docs/history/HISTORY.md (earlier backlog):255
- Image upload / branding (PARTIAL): every image is a typed https URL; no upload, no banner. app/forms.js:7,27,112,127; app/ui.js:26; app/assets/ empty
- Asset hosting model (PARTIAL): unhashed URL stored as ART-NFA header; host can swap the art. contracts/launch.bas:30; contracts/nft.bas:20-21; RISKS.md:71-77

**DISCOVER**
- Hot (EXISTING): badge renders only on the token header; threshold 12 is arbitrary; no decay. app/stats.js:60; app/home.js:8; app/markets.js:42
- Losers (PARTIAL): sorts ascending by the active key, so default "Hot" lists coldest-first. app/markets.js:16,25
- Search (PARTIAL): substring on name/ticker and SCID prefix; header search desktop-only; no creators, addresses, tx ids, tags. app/markets.js:12,51,64; app/app.js:255; app/desk.css:33-34
- Ranking algorithm (EXISTING): single 1h window, no decay, active-block term dominates. app/stats.js:58-60; docs/history/HISTORY.md (earlier backlog):156-157
- Wash trading (PARTIAL): same-block netting only; cross-block round trips count fully; a dust swap per block is 4 points. app/stats.js:29-36,60; contracts/launch.bas:96; docs/history/HISTORY.md (earlier backlog):292-293
- Spam (PARTIAL): dedupe and caps only; no rate limit; `...To` records attribute trades to strangers. app/stats.js:41,60; contracts/launch.bas:83-91,134; docs/history/HISTORY.md (loop records):510-512
- Transparency of why something trends (PARTIAL): subtitle and badge; the score terms are never shown. app/home.js:8,27,84; app/token.js:23,37-38

**TRADE**
- Description (PARTIAL): Info tab only, absent from the header. app/token.js:56
- Creation time (PARTIAL): height times an assumed 18 s, not labelled as an estimate. app/feed.js:46-49; app/stats.js:54,110
- 24h volume (PARTIAL): net reserve flow shown as plain "Vol 24h". app/stats.js:20-37,50; app/token.js:37
- Top holders (PARTIAL): ranked by published trade count, captioned as such. app/token.js:46-50
- Metadata (PARTIAL): tags loaded but never rendered; decimals absent. app/token.js:53-54; app/feed.js:101
- Metric labelling (PARTIAL): ad hoc "Public", "FDV", "(estimated)"; Vol, Liquidity, Age carry no qualifier. app/token.js:23,37-38,48,87,142
- Timeframes (PARTIAL): five ranges with auto interval; no interval selector. app/token.js:21; app/stats.js:88-97
- Trade markers (PARTIAL): own and followed trades only; not linked to rows. app/token.js:18; app/chart.js:72
- Transaction inspection (PARTIAL): txid only in a toast; no explorer link; rf_ refund keys never read. app/home.js:47; app/app.js:143,195; docs/history/HISTORY.md (loop records):600-601
- Safety panel signals (PARTIAL): risk lines plus badges; no checklist; pool LP, creator unsold, asset provenance unscored. app/token.js:34-35,53,108-111; app/home.js:13-20

**COLLECT / TRACK / SHARE / SOCIAL**
- Collection provenance (PARTIAL): creator and id only; `ed_<n>` unread; C10 "filehash" claimed done, absent. app/nft.js:26; contracts/nft.bas:79; docs/history/HISTORY.md (earlier backlog):227-228
- Programmable NFT primitives (PARTIAL): two fixed templates parameterised at install. contracts/nft.bas:7; contracts/founders.bas:12; app/config.js:15-16
- Portfolio tx history (PARTIAL): trade count and a note; local txids never listed. app/screens.js:20,28; app/feed.js:210-211
- Asset breakdown (PARTIAL): DERO row plus launches; no LP, staked or NFT categories. app/screens.js:24-27; app/app.js:80
- Creator profile stats (PARTIAL): followers, following, public trades; no creator-side stats. app/screens.js:41,45,47; app/feed.js:183,188,207
- Sharing launches (PARTIAL): copy-link icon only. app/token.js:8,29
- Proofs (PARTIAL): `t_<n>` primitive exists; no proof route or receipt. contracts/README.md:39-41; app/feed.js:127; app/screens.js:28

**INFRA**
- Gnomon indexer toggle (PARTIAL): declared and shown, never read; dead UI. app/config.js:18; app/app.js:11; app/screens.js:64
- Image storage (PARTIAL): URL only; no hash, thumbnail, MIME or EXIF handling. app/ui.js:26,51-60; app/nft.js:14-17; docs/history/HISTORY.md (earlier backlog):250-256
- Notifications (PARTIAL): toasts and a "new" highlight; nothing persists a reload. app/ui.js:36-39; app/home.js:39-42; app/live.js:35-53

**ECONOMICS**
- launch.bas creator allocation (EXISTING): liquid at install, no vesting, no listing chip. contracts/launch.bas:16,22,67-68; app/ui.js:82-87
- nft.bas holder rewards (PARTIAL): hold-to-mint only; re-lock farming; holdblocks 0 accepted. contracts/nft.bas:14,42-56,85-97
- nft.bas metadata mutability (EXISTING): headers immutable but URLs unhashed, so art can change. contracts/nft.bas:17-22; docs/history/HISTORY.md (earlier backlog):249-253
- factory.bas registration fee (EXISTING): no dedupe, no existence check, overpayment kept. contracts/factory.bas:90-97
- factory.bas verification (PARTIAL): app-side hash plus treasury only; fee/pfee unchecked. app/feed.js:92-98; RISKS.md:15-19
- vault.bas wrapper fees (PARTIAL): feepool STOREd, never paid out. contracts/vault.bas:50,248
- Incentive and sybil resistance (PARTIAL): log-capped volume and distinct publishers; dest spoofing, dust blocks, free follows open. app/stats.js:57-60; contracts/launch.bas:405-412; contracts/social.bas:28-50

## 4. Security surface

Contract-level
- Published-record spoofing: `SwapDeroForAssetTo` / `SwapAssetForDeroTo` pass dest into post(), so one wallet writes `t_<n>` under any address (contracts/launch.bas:83-91,134,405-410; contracts/pool.bas:130-134,256-263). Inflates the hot publisher term (app/stats.js:60), "Public traders" (app/token.js:38), the Holders tab (app/token.js:46-50) and prints "Bought" for strangers (app/home.js:46). docs/history/HISTORY.md (loop records):510-512,619.
- Dust-swap ranking: active blocks x4, only d==0 refused (contracts/launch.bas:96; app/stats.js:60); HOT at >12 is three dust blocks (app/home.js:8). Buys Trending, tape, Trade hub and picker for fees plus 0.5%.
- Verification ignores fee/pfee (app/feed.js:92-98): a fee=0 install with the factory treasury lists as Verified. RISKS.md:16-17 claims the check exists.
- Template drift: app/templates/launch.bas (sha bc05b8e3, app/config.js:14, installed by app/forms.js:9,11) differs from contracts/launch.bas (sha 8c0bf10d, adds `refundto`, moves `initd`; diff lines 66-68, 87-89, 170-182). pool.bas and founders.bas templates also lag; contracts/README.md:14,225-232 documents the newer code.
- nft.bas re-lock farming: Unlock deletes lm_ (contracts/nft.bas:92-94), Lock checks only lk_ (nft.bas:50), holdblocks 0 accepted (nft.bas:14). docs/history/HISTORY.md (earlier backlog):227-228 says fixed; tests/registry_test.go:232-241 expects re-lock to succeed.
- founders.bas: distribute(0) before and after adding units lets the first registrant sweep all of und (contracts/founders.bas:84,98,259); price 0 legal (founders.bas:18); mint proceeds go unescrowed to treasury (founders.bas:70). MASTER_TODO C9 says done.
- social.bas: display names free-form and non-unique (contracts/social.bas:19-23) while feed rows show only displayName (app/home.js:45); Unfollow leaves fl_ and never decrements fc_ (social.bas:52-65). MASTER_TODO C11 says done.
- Social read is a full `variables:true` dump on every load (app/feed.js:174); cheap Profile/Follow spam slows every client (RISKS.md:83; docs/history/HISTORY.md (earlier backlog):274).
- launch.bas Buyback is callable by anyone, any ring, no min_out (contracts/launch.bas:260-276): sandwichable; tmode 2 tax stranded until graduation (launch.bas:262).
- factory.reg forwards the whole DEROVALUE, no refund, no dedupe, any 32-byte string (contracts/factory.bas:90-97); SetTreasury delists every market at once (factory.bas:27-36; MASTER_TODO C7).
- Treasury key is a single point of failure for ClaimFees on every market (contracts/pool.bas:227; contracts/launch.bas:234; contracts/nft.bas:115; MASTER_TODO C13).
- treasurybot sweeps only pool_/launch_ (tools/treasurybot/main.go:122); share on DERO leg only (journal.go:189); single operator can stop.
- vault.bas feepool written but never paid out (contracts/vault.bas:50,248).
- Creator allocation to 5% sent liquid at install (contracts/launch.bas:67-68); tax to 10% claimable at will; no listing chip (app/ui.js:82-87).
- Pool liquidity is point-in-time rd*2 (app/stats.js:52): add LP, top the sort, remove next block; no LP-withdrawable chip (docs/history/HISTORY.md (earlier backlog):297-299).
- Ring-2 calls expose the signer (contracts/README.md:217-220); hand-crafted higher-ring calls to SIGNER() entrypoints lose deposits on revert (RISKS R-2).
- Reading note: the trailing "· done" in MASTER_TODO backlog rows is the done-criterion column; C7, C9-C13 are open against current sources.

App-level
- Holder-NFT form has no parser: `BigInt(f.blocks.value || 0)` throws after the sheet closes (app/forms.js:121-122); description textarea has no maxlength (forms.js:112) against nft.bas:12, surfacing as an opaque estimate error (app/app.js:122).
- Failed RegisterLaunch/RegisterNFT leaves a verified, unlisted contract with a toast only (app/forms.js:21; app/ui.js:79); install fee spent, no retry (MASTER_TODO T5).
- Artwork is an unhashed https URL (app/ui.js:26; contracts/nft.bas:20-21): host can swap it; opted-in loading leaks viewer IP (RISKS.md:71-77); img-src allows any https plus data: (app/index.html:6); NFT images lack onerror (app/nft.js:17; app/nfts.js:37). Stale docs: RISKS.md:75-77 and DECISIONS.md:395 say default on; app/app.js:11 ships off.
- CSP by `<meta>` cannot set frame-ancestors and TELA adds no headers (DECISIONS.md:249): the app can be framed. style-src 'unsafe-inline' retained for app/ui.js:106.
- Sole innerHTML is the chart tooltip (app/chart.js:94; app/ui.js:2), formatted numbers only; a future formatter passing chain text becomes an injection point.
- Private mode leaves "Post this trade" active though publish is suppressed (app/token.js:117; docs/history/HISTORY.md (loop records):508-509); ring-2 gas estimate sends the signer to the node (app/app.js:117).
- Fee estimate falls back to size-based on the graviton ":" panic (app/app.js:119-121; RISKS R-16); capped at 1 DERO (app/app.js:88,125).
- Balance-move proof fires only after the wallet scans that height (app/app.js:170-179); otherwise "included", not "confirmed".
- Positive controls: slippage <=10% (app/app.js:9,184), fee <=1 DERO (app.js:88,117), one in-flight tx (app.js:89,126), verified listing only (app.js:100), burn destination validated (app.js:104).
- localStorage is shared across the TELA origin: market cache shape-checked and newest records re-verified (app/feed.js:58-79,118-125); settings/endpoint validated (app/app.js:12-17); XSWD app id per session (app/xswd.js:9-15); watchlist ids unvalidated but only a filter Set (app/stats.js:115).
- Founders marketplace reads only the last 300 listing ids (app/founders.js:21): older open listings invisible to buyers and to the seller's Unlist (app/nfts.js:52-55).
- Founders "Holders" is registered addresses (app/nfts.js:39; contracts/founders.bas:45,91), not pass holders.
- Search does not fold look-alike tickers (app/markets.js:12; app/ui.js:25 unused there), so the genuine ticker's search misses the impostor.
- Losers filter sorts by the wrong key (app/markets.js:25); functional, not security.
- Indexer toggle unwired (app/screens.js:64); if wired, Gnomon becomes a trusted middle (DECISIONS.md:405-406).
- Follow button gives no point-of-action disclosure that the graph is public (app/home.js:34-38); only Settings explains (app/screens.js:81).
- Dead tags field: validated, passed and stored (app/forms.js:50,92,122; contracts/launch.bas:31; contracts/nft.bas:22) but never rendered; docs/CREATOR_GUIDE.md:27 claims otherwise.
- No service worker or manifest: no offline cache to poison, but no update pinning beyond the TELA commit shown (app/index.html:9).

## 5. Privacy constraints that change the design

Each line: what transparent-chain products show, why DERO cannot, and the honest alternative.

- Holder count, holder list, Top 10 %, Dev %, Sniper %, Bundle % (Axiom/GMGN safety strip; pump.fun holder tags). Balances are encrypted; holders cannot be enumerated (app/stats.js:56). Today: "Public traders" = distinct publishing addresses, captioned in the Holders tab (app/token.js:38,48). Target: "Holders: private by design", replaced by protocol-enforced facts the page can prove: creator allocation (app/home.js:11-15), locked reserve (contracts/launch.bas:107-139), no mint/burn/admin (contracts/README.md:3).
- Collection owners and wallet-held NFTs (Magic Eden/OpenSea owners count, profile holdings). Same encryption. Today: nothing (app/nft.js:9). Target: original minters from `ed_<n>` (contracts/nft.bas:79) labelled as minters, and Founders "registered" rather than "holders" (app/nfts.js:39).
- Gross volume, txns, buyers/sellers, makers per window (DexScreener). Only per-block net reserve flow is observable (app/stats.js:22-37) plus opt-in published trades `t_<n>`. Today: "Vol 24h" unlabelled, "Public trades 24h" labelled (app/token.js:37). Target: "net flow, verified from reserves" and a hover saying intra-block round trips net to zero.
- Full trade tape with wallets, wallet trackers, copy-trade lists. Unpublished trades are invisible; publishing is off by default (app/token.js:117); ring-16 private mode hides the payer (app/app.js:180-192). Today: feed shows published trades only with a note (app/token.js:44); chart marks own and followed trades only (app/chart.js:72). Keep this; it is the anti-surveillance feature, not a gap.
- Public PnL profiles and trader leaderboards (pump.fun, Axiom). PnL is computed locally from device trades and the address's own published trades (app/app.js:202-214; app/feed.js:210-216); no server sees it. Target: opt-in bucketed share card without wallet disclosure (R6), never a leaderboard.
- Instant "confirmed" with explorer link. Private-mode outcomes are only provable when dest is the connected wallet (app/app.js:180-182); balance moves prove execution only after wallet sync (app.js:170-179). Today: "included in block N" versus "confirmed". Keep the distinction; add the txid to a local list (Transaction inspection row).
- Sniper and bundle detection in the first 400 ms. No wallet visibility to detect them, but trades settle per ~18 s block (app/stats.js:110), so the block is the batch. Target: present the block interval as the fair-launch window and show honest latency, not fake instant fills.
- Sybil heuristics from wallet age and clustering. Addresses cannot be clustered, and `...To` lets one payer publish under many dest addresses (contracts/launch.bas:405-410). Honest alternative: rank on reserve flow, which costs DERO, and cap or drop the address-count term (app/stats.js:60); never show a number that address minting can inflate.
- Creation timestamps. Install height only (app/feed.js:46-49); time is height times an assumed block interval (app/stats.js:54). Show the height and label the time as estimated.
- Always-loaded token images and avatars. Loading a creator-chosen URL reveals the viewer's IP to that host (RISKS.md:71-77). Images off by default with an opt-in switch (app/app.js:11; app/screens.js:69); keep, and say why inline.
- Indexer-backed pages. No server: every client reads node keys directly (app/rpc.js:69-94; app/feed.js:23-40), so unbounded reads like social `variables:true` (app/feed.js:174) are a scale limit that indexers hide elsewhere. Range-read everything; if Gnomon is wired it stays verify-against-node.
- Comments with holder badges. Not built (contracts/social.bas:3-4). If built, the benchmark's "verified holder without revealing which address" is the only form consistent with the above.

## 6. Measured sizes

App (raw bytes): app/*.js 19 files 202,869; app/*.css 4 files 32,551; app/index.html 3,294; total 238,714 raw, 72,272 gzipped. Zero external dependencies (no CDN, framework, font or remote image; "Inter var" named but never loaded); no bundler or minifier, 19 script tags.

Per file: app.js 26,069 (270 lines); token.js 19,856 (145 lines); feed.js 19,220; forms.js 15,490; ui.js 13,595; chart.js 12,911 (122 lines); home.js 12,401; nfts.js 11,834; screens.js 11,622; rpc.js 10,373; xswd.js 8,011; stats.js 7,855 (121 lines); markets.js 7,555; amm.js 6,368 (100 lines); sheets.js 5,114 (40 lines); live.js 4,726; nft.js 4,659; founders.js 2,993; config.js 2,217; ui.css 11,186; styles.css 8,704; parts.css 7,133; desk.css 5,528.

Templates shipped for install (app/templates/*.bas, fetched only on launch, app/forms.js:9): 44,726 B; launch template 15,425 B versus contracts/launch.bas 15,781 B (drift); nft template identical.

Contract sources: launch.bas 15,781; vault.bas 14,098; pool.bas 13,166; founders.bas 12,586; nft.bas 4,025; swap.bas 3,599; factory.bas 2,852; board.bas 2,848; social.bas 2,062; contracts/README.md 17,285.

Gas (contracts/README.md): install storage launch ~16,000, vault ~14,400, pool ~13,400, founders ~12,800, nft ~4,600, factory ~3,100 (cap 20,000 per tx). Compute per README: launch ~1,200,000, pool 409,000. Fuzz worst case: Unstake 1.78M, AddLiquidity 1.72M, Stake 1.69M, launch Buy 1.61M, pool swaps 1.06M (cap 10M).

On-chain app (deploy/README.md (TELA):64-68, commit 13): 28 TELA DOCs, 253,702 source B to 154,578 B on chain, ~1.64 DERO to install; largest DOC app.js 9,720 B (limit 18 KB); founders DOC 6,630 B.

Tooling: tools/treasurybot main.go 6,807; journal.go 7,799; chain.go 1,960; journal_test.go 8,638.

Client caps: 500 local trades; 1,500 points and 600 trades per market cache; 300 Founders listings read; balance reads cached 60 s; saves at most every 30 s.

Deployed economics (app/config.js, tools/simdeploy): feeBps 50, pfeeBps 5000, launchPfeeBps 5000, regfee 10,000 units (0.1 DERO), pass price 500,000 units (5 DERO), 500 editions with 50 reserved, share 5000 bps, mfee 250 bps.## 7. Competitor benchmark (what users now expect, and where each product fails)

## 7. Competitor benchmark (what users now expect, and where each product fails)

### pump.fun (Solana bonding-curve launchpad)
- strengths: Zero-friction launch: name, ticker, image, description, socials; token is live on a bonding curve in seconds with instant liquidity, and graduates automatically to PumpSwap when the curve fills.; Creator economics are real and visible: 0.30% of every curve trade goes to the creator; since Jan 2026 fees can be split across up to 10 wallets, coin ownership transferred, and Community Takeover admins assigned a share. ~$2M was paid to creators in the first 24h of the new model.; Fee schedule is published and declines with size: 1.25% total on the curve, falling to 0.30% on PumpSwap once market cap is high. Web, mobile and Pump Advanced add no extra platform fee.; Social layer inside the product: livestreams on the token page, comment threads, trader profiles with PnL, follows, alerts, and a feed; Pump Advanced gives a filterable new-launch / about-to-graduate / graduated view.; Unmatched attention network effect on Solana: ~$935M cumulative revenue, its own PUMP token, and the default place traders look for new coins.
- weaknesses: Solidus Labs measured 98.6% of launches as scams/rugs; only a low single-digit percent ever graduate. Retail is structurally last in line behind snipers, bundlers and bots in the first seconds.; $500M class action alleging a rigged casino with insider pre-access; EU DAC8 reporting from Jan 2026; geo-blocks (e.g. UK) and a July 2026 team unlock overhang on PUMP.; Every wallet is fully transparent: holdings, PnL and timing are public, so users get copy-traded, front-run, doxxed and targeted. Trader profiles with public PnL amplify this.; Headline numbers mislead: 'market cap' is a curve-implied figure on an illiquid pool; holder counts are inflated by bundles; volume is not distinguished from bot churn.; Livestream moderation has failed badly before (2024 shutdown after harmful streams); reputational risk to any coin listed there.; Heavy JavaScript front-end that depends on a centralised backend/indexer; the site, not the chain, decides what you see.
- standard: One-form, no-code, near-free launch with image, name, ticker, description and social links; Bonding curve with a visible progress bar and automatic graduation to a DEX pool; Token page: chart, buy/sell panel with slippage and priority presets, holder list with dev / bonding-curve tags, comment thread with replies, livestream, creator socials; Three-column live discovery: new launches, about to graduate, graduated; filters by age, market cap, volume; Creator fee share shown on the token page; fee reassignment on Community Takeover; Trader profiles, PnL, follow, alerts feed; Native mobile app and an advanced/pro terminal; Share-to-X with a link preview card

### shld.fun (Zcash shielded memecoin market)
- strengths: The only launchpad whose headline promise is buyer privacy: 'the market is public, the buyers are not, when a buy is paid from a shielded balance.' Fills are verifiable but no shielded buyer is nameable.; Clean, minimal market table: TOKEN, PRICE, 24H %, 24H VOL, MARKET CAP, LIQUIDITY, GRADUATION, plus Create and Portfolio tabs. Nothing pay-to-play on the surface.; Honest framing: page-level disclaimer that it is experimental and unaffiliated with ECC/Zcash Foundation, and an explicit warning that paying from a transparent address links you on-chain forever.; Onboarding is pointed at real shielded wallets (Zodl, Zingo) rather than a custodial signup.; Sells pay out ZEC to any address the user chooses, so exit destination is under the user's control.
- weaknesses: Custodial in practice: per-wallet shielded deposit addresses are detected by the SHLD server, balances live on an internal ledger, and ownership is a signed 'holding note' rather than a token in your wallet. Users trust the operator for solvency, uptime and honest curve accounting.; Privacy is conditional and easy to get wrong: one transparent-address payment permanently de-anonymises that buy. The burden is on the user to know the difference.; Discovery is a single sortable table; no trending/new feeds, no charts detail, no comments, no profiles, no alerts, no mobile app were visible.; No safety signals at all (creator allocation, lock status, sniper/bundle share), and because holders are hidden, it cannot show concentration even if it wanted to.; Zcash has no general smart contracts, so the curve, fees and graduation terms are off-chain rules; fees were not visible on the pages fetched.; Latency: Zcash block time plus shielded transaction construction means deposits and withdrawals are slow relative to Solana terminals.; Tiny market; no ecosystem of terminals, aggregators or indexers reading it.
- standard: Sortable market table with price, 24h change, 24h volume, market cap, liquidity and graduation status; Create / Market / Portfolio as the three top-level surfaces; Wallet connect plus a per-user shielded deposit address; Bonding curve with graduation; A one-line plain-language statement of what is public and what is private; Links to recommended wallets for onboarding

### DexScreener (multi-chain DEX terminal)
- strengths: Indexes every DEX pair on ~100 chains automatically, seconds after liquidity is added, with no listing fee. Its pair URL is the canonical link the whole industry shares.; Trending across 5m/1h/6h/24h ranked on volume, transaction count and unique-buyer growth; New Pairs, Gainers/Losers, chain and filter screens.; Pro tooling: TradingView charts, MultiCharts (up to 16 at once), multiple watchlists, price and volume alerts, embedded swap, mobile app, iframe-embeddable charts.; Paid promotion is at least labelled: Boosts (~$100 packs, $300-1,500 for a campaign) show a golden/lightning badge; Enhanced Token Info (~$299) is a visible banner. Reactions (rocket/fire/poop/flag) give a lightweight sentiment layer.; Moonshot launchpad and audit/holder/top-trader tabs make it a one-stop screen for most retail flow.
- weaknesses: Pay-to-be-seen culture: Boosts and paid Enhanced Token Info have become a de facto 'legit' signal that rug operators buy first; third-party 'booster' volume bots wash-trade to hit trending thresholds. The organic/paid line is real but invisible to most users.; No first-party contract risk assessment; sniper, bundle and dev-holding analytics are absent, so the terminal shows what happened but not who is holding the bag.; Displays market cap / FDV on illiquid pools and counts makers/buyers that are trivially inflated by split wallets.; Reactions are identity-free and spammable; ads everywhere; heavy web app that is unusable on weak connections.; Centralised: it decides what is 'trending', can delist, and its API is rate-limited.
- standard: Pair page: TradingView chart; price, FDV/market cap, liquidity, 24h volume; txns, buys/sells, buyers/sellers, makers for 5m/1h/6h/24h; pooled tokens; pair age; contract and pair addresses with copy buttons; Token profile: socials, description, Enhanced Info banner, boost count, reactions, audit tab, holders tab, top traders tab, embedded swap; Trending / New Pairs / Gainers-Losers screens with chain and metric filters, search by name or address; Watchlists, price/volume alerts, MultiCharts, mobile app; Share links with a rich preview card; embeddable chart; Clearly badged paid promotion (Boosts, Enhanced Token Info) and ad slots

### Magic Eden (NFT marketplace)
- strengths: Dominant Solana NFT venue (85%+ of its own volume) with a mature collection page: floor, listed count, owners, volume, rarity rank, trait filters, activity, price history, sweep/bulk buy, collection offers.; Launchpad with allowlist phases, mint counters and countdowns has been the default way Solana projects mint.; Added Solana token swapping alongside NFTs, and a points/rewards program tied to the ME token.; Mobile app and, historically, a multi-chain wallet that bridged and traded in one interface.
- weaknesses: Feb 2026 reversal: shut down the Bitcoin Ordinals/Runes and EVM marketplaces, put the multi-chain wallet in export-only mode and killed it by 1 May 2026, and pivoted toward a gambling product (Dicey). Users learned a marketplace can delete the surfaces they built on.; ME token down ~99% from its high; incentive programs left holders with little.; Optional royalties (forced by Tensor/Blur competition) hollowed out creator income.; NFT volume is a small fraction of 2022 levels; the marketplace is now a Solana-only niche.; Centralised listing/escrow programs and moderation; verified-badge gatekeeping.
- standard: Collection page: floor price, listed %, total volume, owners, items, price-history chart, trait filters, rarity rank, activity feed; Buy now, make offer, collection offer, sweep N items, list/delist, bulk actions; Launchpad: allowlist phases, mint count, countdown, creator profile; User profile: owned, listed, offers made/received, activity; portfolio value; Verified collection badge, watchlist, notifications, share link; Rewards/points program

### OpenSea (NFT marketplace, OS2)
- strengths: OS2 is fully live across 19 chains with NFTs and fungible token trading in one app; cross-chain purchases without manual bridging; prices aggregated across marketplaces.; Low headline fees: 0.5% on NFTs, 0% on token swaps at launch.; Polished product surface: redesigned home, wallet sidebar, real-time notifications, colour-coded rarity, in-depth collection stats, community hub, no-code drop/creator studio.; Voyages XP quests and the pending SEA token keep a large user base active; the strongest brand recognition in NFTs.
- weaknesses: Creator royalties became optional under Blur pressure and OpenSea flip-flopped publicly; creators no longer trust it as an income channel.; By late 2025, 90%+ of volume was token trading, not NFTs; the NFT market it defined is a fraction of its former size.; SEA token launch was delayed repeatedly, frustrating users farming Voyages XP and making rewards look like an engagement trap.; Centralised control: delistings, sanctions compliance, past insider-trading case, and Seaport signature phishing remain the main way users lose assets.; Web-heavy app with aggregation that hides which venue actually filled you.
- standard: Collection page: floor, volume, owners, items, trait floors, rarity, activity, listings/offers tabs; Token swap terminal alongside NFT buying, cross-chain purchase, wallet sidebar; Drops calendar and no-code creator studio; Profile with holdings, offers, activity, notifications; XP / quest rewards program; Share links with preview cards; verified badges; watchlist

### Modern DEX terminal (Axiom / Photon / GMGN / Jupiter)
- strengths: Pulse-style live board (new pairs / final stretch / migrated) that updates in real time with one-click buy and quick-sell presets; Photon has the cleanest UI, Axiom the deepest feature set (perps, wallet and X tracking, copy trade).; Safety strip on every token: Top 10 holders %, Dev holding %, Snipers %, Insiders %, Bundle %, plus mint authority, freeze authority and LP lock/burn status. This is now what 'safe enough to ape' looks like to users.; Fee competition: standard 1% per trade but Axiom tiers from 0.95% down to 0.75% with SOL cashback; Axiom holds ~55% of terminal fee share, GMGN ~23%.; Jupiter: best-price aggregation, limit orders and DCA, perps, lending, prediction markets, Mobile V3 native pro terminal at '10x lower cost', Jupiter Studio launches with anti-sniper protection and creator updates displayed on the token page.; Portfolio and PnL cards, wallet tracking, alerts, referral programs and airdrop points keep users inside one app.
- weaknesses: Axiom/Photon-style embedded wallets are effectively custodial key management; 1% on every trade compounds fast; referral trees and points farming shape behaviour.; Safety signals only exist because the chain is transparent, so they are a cat-and-mouse game: devs split supply across wallets to lower 'Dev %', bundlers randomise timing, and the numbers stop meaning what users think.; Every user is surveilled: wallet trackers and copy-trade lists turn any successful trader into prey; MEV and sniping remain.; Heavy JavaScript apps that break on weak connections; Photon lacks perps, tracking and cashback; Jupiter's superapp sprawl is intimidating.; Terminals index other people's launchpads, so they inherit the 98% rug base rate and cannot change the launch rules.
- standard: Three-column real-time launch board (new / about to migrate / migrated) with per-column filters; Token page: chart, one-click buy/sell presets, slippage and priority controls, bonding-curve progress, socials; Safety strip: Top 10 %, Dev %, Snipers %, Insiders %, Bundle %, mint/freeze authority, LP status; Wallet tracker, copy trade, trader leaderboards; Portfolio with open positions and PnL cards; PnL share image; Price alerts, limit orders/DCA, referral and points programs; Native mobile app; fee cashback tiers

### Token launchpads (Bonk.fun, Bags, Zora, Believe, Jupiter Studio)
- strengths: Creator fee share is the standard now: Bags pays creators 1% of every trade on curve and open market and lets devs split with the meme's original creator; Bonk.fun routes fees into BONK buybacks/burns; Bags reports $5B volume and $40M creator payouts.; Social-native launch: Zora and Believe let you launch from a post or tweet, turning content into a coin; Bags and Bonk.fun are mobile-first with alerts and social discovery.; Fairness features are moving up the stack: Jupiter Studio ships anti-sniper protection, vesting for up to 80% of supply, liquidity locks, custom swap fees, and creator updates rendered on the token page.; Ecosystem alignment (Bonk community, Jupiter distribution, Base/Farcaster for Zora) gives each launchpad a built-in audience.
- weaknesses: Same rug base rate as pump.fun; launchpads compete on creator payouts, not on user outcomes.; Flavour-of-the-month churn: LetsBonk, Believe, Bags each spiked and faded; 'internet capital markets' hype collapsed quickly.; Launch-from-a-tweet invites impersonation coins and account-hijack launches.; Tokenomics knobs (custom fees, vesting, locks) become new ways to confuse buyers unless surfaced plainly.; Still fully transparent wallets and centralised front-ends; most run on the same Solana terminals with the same surveillance problems.
- standard: No-code launch with image, name, ticker, description, socials; optional vesting, liquidity lock and custom fee settings; Bonding curve with graduation and automatic DEX pool; Creator fee split shown on the token page; ability to share fees with other wallets or the original meme creator; Anti-sniper launch window; Token page with creator updates/posts, chart, buy/sell, holders, comments; Mobile app with price alerts and social discovery; Ecosystem buyback/burn or points program

### How Veil beats them
- Privacy by default, not by discipline: every balance is encrypted and every buy is hidden by a ring signature, so there is no 'pay from a transparent address and it's linked forever' footgun (shld.fun) and no public wallet to copy-trade, front-run or dox (every Solana terminal). The user sees one privacy state, always on, with a plain 'What is public / what is private' panel on every page.
- Self-custody where shld.fun is custodial: the token is a real on-chain asset in the user's own wallet, not a server-side 'holding note'. Sells and withdrawals never route through an operator ledger; if the site vanishes the tokens and the app are still on the chain.
- Honest metric labelling as the brand: rename 'market cap' to 'curve-implied value', show 'exit liquidity' as the price impact of selling 1% / 5% / 10% of supply right now, and show 'fills (verified on-chain)' instead of an inflatable 'volume'. Every number has a hover that says how it was computed and what it cannot know.
- Turn unknowables into trust signals instead of fake numbers: where terminals show a gameable 'holders: 1,204' or 'Dev %: 3.2%', show 'Holders: private by design' and replace it with protocol-enforced facts that do not need surveillance: creator allocation and its vesting schedule as an immutable on-chain commitment, curve reserve that cannot be withdrawn, no mint/freeze authority, graduation terms fixed at launch.
- Standard, unmodified contract badge: launches use a small set of audited templates; the token page shows 'Standard curve, unmodified' or 'Custom terms: see diff' so buyers get the equivalent of a rug check without a third-party scanner.
- No pay-to-trend, ever: discovery ranks are computed only from verified fills and age. If sponsorship exists it lives on a separate shelf labelled 'Sponsored', never touches ranking or search order, and the token page shows how much promotion that token has bought. This is the exact gap DexScreener boosts and Enhanced Info opened.
- Fair-launch window as visible behaviour: the first block(s) after launch are batched at a single price so the 'first 400ms' sniper race pump.fun users lose does not exist; ring-signature privacy also removes wallet-reputation sniping and the copy-trade lists that make Axiom/GMGN users targets.
- Fee schedule shown before the first tap: total trade fee, creator share and platform share are printed on the Create screen and on every buy panel, immutable per token, and lower than the 1% terminal fee stacked on a 1.25% curve fee that Solana users pay twice.
- Creator economics without extraction: creator fee share and fee-split recipients are displayed on the token page as on-chain commitments, so a 'community takeover' is a visible state change rather than a support ticket.
- A portfolio only you can see: PnL, open positions and history are computed locally in the wallet and never posted to a public leaderboard. Sharing is opt-in: the user can publish a verifiable PnL card without revealing which wallet earned it.
- Holder-verified social without doxxing: comments and posts on a token page can carry a 'verified holder' badge proving the author holds the token without revealing which address or how much, which pump.fun cannot offer and shld.fun does not attempt.
- App served from the chain (TELA): the token page and terminal load from a DERO node by contract ID, so there is no domain to seize, geo-block or phish-clone, no tracking scripts, no ToS delisting, and the user can verify the exact app version they are running. Sub-second loads on low-end phones and weak connections, where DexScreener/Axiom bundles stall.
- Honest latency instead of fake instant: show 'Confirms in ~18 s' with a live pending state, and use the block interval as the fairness batch rather than pretending Solana-speed execution; users get fewer failed transactions and no priority-fee auctions.
- Charts that do not lie: candles are built only from real fills with timestamps; periods with no trades render as 'no trades' rather than a flat synthetic line; no indexer-side smoothing.
- One venue for tokens and collectibles: DERO assets carry the same encrypted ownership, so NFT listings, offers and mints live beside token markets in the same lightweight app, with private ownership that OpenSea/Magic Eden cannot provide and no risk of a marketplace deleting an entire chain's surface as Magic Eden did in 2026.
- Sharing that is clean: deep links resolve to the on-chain token page, preview cards are generated locally, and links carry no referral tracking unless the sharer explicitly adds one. Referral and points schemes, if any, are labelled as what they are.
- Explicit, standing risk language: every launch and token page carries the base-rate truth ('most launches on any curve lose money') instead of hiding it behind a footer disclaimer, which is the honest version of shld.fun's 'experimental tech' line and something no incumbent will ever print.
