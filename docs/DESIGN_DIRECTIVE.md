# DMZ — UI/UX signature redesign directive

**For the implementing agent (Opus 5).** This is a complete brief: what to build, what must not break, and the
facts about this codebase you need before touching a stylesheet. Read all of it before Phase 1. The operator's
own words are preserved in §2–§7; §1 and §8 are the engineering constraints that turn those words into work
that can actually ship on TELA.

The one-sentence target: **someone should see a screenshot with the logo cropped out and say "that's DMZ."**

---

## 1. Read this first — the constraints the directive does not know about

### 1.1 What the app is, mechanically

- **Vanilla JS, no build step, no framework.** `app/index.html` loads ~20 scripts in order (see the `<script>`
  tags); each attaches to `window.Veil` (`V.ui`, `V.data`, `V.chart`, …). There is no bundler, no transpiler,
  no CSS preprocessor. What you write is what ships.
- **Every file is a TELA DOC with a hard size cap: 18 432 bytes after gzip+base64, per file.** Measure with
  `cd tools && go run ./genconfig -dist`; it prints every file's DOC size and refuses the bundle if one is over.
  Current CSS: `styles.css` 4 160, `ui.css` 4 328, `parts.css` 3 012, `desk.css` 2 704 DOC bytes. The largest
  scripts are `app.js` 12 440 and `token.js` 8 860. **You cannot add a 200 KB font file.** Budget accordingly.
- **Content Security Policy (`app/index.html` line 6):** `default-src 'none'; script-src 'self'; style-src 'self'
  'unsafe-inline'; font-src 'self'; img-src 'self' https: data:`. Consequences:
  - **No Google Fonts, no external stylesheets, no CDN.** A typeface either ships as a file in `app/` (and counts
    against the DOC cap — a subsetted WOFF2 of one weight is ~15–25 KB, which is already over the cap for one
    file) or it is a system font stack. The typography upgrade in §5 must be achieved with **system stacks,
    weight, size, tracking, tabular figures and hierarchy** — not by shipping a display face. If you find a way
    to ship one under the cap, prove the size with `genconfig -dist` first.
  - Inline `style=` attributes are allowed. Inline `<script>` is not (the UI test asserts this).
- **Icons are inline SVG paths** in the `P` map in `app/ui.js` (`icon(name, cls)`). No icon library is loaded.
  Bespoke icons go in that map; keep each path short — they are bytes.
- **Charts are hand-drawn SVG** in `app/chart.js` (`series`, `render`, sparklines in `ui.js`). No charting
  library. Keep it that way.
- **The router is hash-based** (`#home`, `#markets/…`, `#token/<scid>`, `#wrap`); screens are functions
  registered with `router.on(name, fn)` that render into `#screen`. Navigation, tabs and the sidebar are one
  set of `.tab[data-tab]` buttons styled differently at ≥960px (`desk.css`).
- **Theme:** single dark theme. There is no light mode and no `prefers-color-scheme` handling. You may keep it
  single-theme; if you add a light theme it must be complete, not partial.

### 1.2 The current design tokens (and what the directive is objecting to)

`app/styles.css` `:root`:

```
--bg:#07080d  --solid1:#0f1019  --solid2:#151726
--s1..--s4: white at 3.5% / 6% / 9.5% / 14%       (stacked translucent surfaces = card-in-card)
--hair / --hair2 / --line: white at 8% / 15% / 7%
--fg:#f6f6fb  --fg2:#b0b1c4  --fg3:#7a7c94
--up:#2ee6a6  --down:#ff5c8a
--acc:#7c6cff  --acc3:#5ad1ff  --warn:#ffb547      (the "Web3 purple" and its glow)
--mood:#7c6cff --mood-a:.16                         (a mood tint driven by market state, live.js)
--r1:14px --r2:18px --r3:24px --pill:999px          (the rounded-everything problem)
--sh: 0 18px 50px rgba(0,0,0,.55)  --glow-acc: 0 10px 30px rgba(124,108,255,.35)
--font: Inter / SF Pro / Segoe UI Variable / system   --mono: JetBrains Mono / SF Mono / ui-monospace
```

Everything the directive names — purple accent, glow shadow, 14–24px radii, translucent stacked surfaces,
pills — is a token. **Change the tokens and the primitives; do not hunt through screens.** `live.js` writes
`--mood` from market state; decide deliberately whether that survives (a quiet version can be a DMZ signature;
the current glowing version cannot).

### 1.3 Where things live

| Concern | File(s) |
|---|---|
| Tokens, base, layout shell, header, tabs | `app/styles.css` |
| Components: buttons, pills, sheet, market rows, toasts | `app/ui.css` |
| Widgets: stats, kv rows, warn/err boxes, fields, progress | `app/parts.css` |
| Desktop shell ≥960 (sidebar), two-column pages ≥1180 | `app/desk.css` |
| DOM toolkit, icons, avatar, sheet, toasts, router, sparkline | `app/ui.js` |
| Home (console, trending, feed, trade screen) | `app/home.js` |
| Markets table + filters + search | `app/markets.js` |
| Token page (chart, stats, trade sheet) | `app/token.js`, `app/sheets.js` |
| Portfolio, Profile, Settings | `app/screens.js` |
| Wrapped assets (the XMR/ZEC vault screen) | `app/wrap.js` |
| NFTs, Founders Pass | `app/nfts.js`, `app/nft.js`, `app/founders.js` |
| Forms (launch wizard, profile edit) | `app/forms.js` |
| Charts | `app/chart.js`, `app/stats.js` |
| Block clock, rolling numbers, mood, the ticker tape | `app/live.js` |
| Wallet (XSWD), node RPC | `app/xswd.js`, `app/rpc.js`, `app/app.js` |

### 1.4 The process is design-led, and it is measured

1. **Render before you design.** The UI test produces screenshots of every route at 430px, 1024px and 1280px:
   `PW_DIR=<dir with node_modules/playwright> node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html .sim/ui-<n>`
   (the simulator and `tools/serve` must be running; `bash tools/uitest/run.sh` wraps it). Look at the PNGs in
   `.sim/ui-<n>/` — `02-home.png`, `02-markets.png`, `04-token.png`, `desk-*.png`, `desk1024-token.png` — and
   write down what is wrong *in the render*, not in the CSS.
2. Implement.
3. **Re-render and reassess.** Every critique pass in §7.7 is a pass over new screenshots, not over the diff.
4. **The UI test is the regression gate.** It must stay green: CSP intact, every tab renders without console
   errors, the wrap screen publishes the minimum deposit, listeners do not leak, a markets redraw stays under
   120 ms, no horizontal overflow at 1024, the token main column ≥ 480 px. Extend it with what you change
   (new widths, new assertions); never weaken it.
5. **`cd tools && go run ./genconfig -dist` must pass** (every file under the DOC cap) before any commit.

### 1.5 Facts that constrain the copy and the iconography

These are true of the system and the UI must not contradict them:

- Balances are encrypted on DERO; the app computes portfolio value on-device from the wallet's own reads.
- An ordinary swap is a **ring-size-2** call: the caller's address is public in that transaction. "Private
  mode" trades are ring 16 and are paid to a named address. The trade sheet already says which; keep saying it.
- Wrapped XMR/ZEC are **backed one for one by a k-of-n federation** that holds the foreign coins. It is not
  trustless and the app must never say it is. The wrap screen's trust panel names what k signers can do; there
  is deliberately **no SAFE badge**, and the UI test fails if one appears.
- There is **no operator** after deployment. Nothing in the UI may imply a support desk, a refund request, or a
  manual review.
- The product name is **DMZ**, dURL `dmz.tela`. The global `window.VEIL_CONFIG` and the `V`/`Veil` namespace are
  internal identifiers and stay.

---

## 2. Role and intent (operator's directive)

You are acting as a principal product designer, interaction designer, design systems architect, and senior
frontend engineer conducting a complete visual and UX refinement of DMZ.

DMZ is a privacy-first decentralized exchange and trading environment built around DERO, Monero (XMR) and Zcash
(ZEC). The existing application is functional and already has the fundamental product architecture in place.

Your job is **not** to redesign the product into another generic crypto dashboard. Your job is to develop a
distinctive visual identity for DMZ that feels like a serious, highly engineered, premium financial instrument —
something that could plausibly have been designed by an exceptional independent product studio with a very
strong design philosophy. It should be immediately recognizable as DMZ.

### 2.1 Understand what DMZ actually is

Not "a crypto trading website." DMZ is the intersection of privacy, decentralized finance, private digital cash,
censorship resistance, cryptographic settlement, user sovereignty, DERO, Monero, Zcash, permissionless markets,
private transactions and autonomous markets.

The name — **Demilitarized Zone** — matters. A DMZ is a boundary. A neutral zone. A place where different systems
meet without one side owning the other. The design should communicate **neutrality + privacy + cryptography +
markets + infrastructure**, without becoming cliché.

### 2.2 Absolutely avoid the generic AI/crypto look

Reduce or eliminate: purple gradients, giant glowing blobs, glassmorphism, excessive rounded cards, excessive pill
buttons, generic gradient backgrounds, generic Lucide-style iconography everywhere, floating neon elements, "AI
dashboard" visual language, excessive card nesting, arbitrary glowing borders, excessive empty space, generic
fintech and exchange templates, "Web3 purple", excessive rounded rectangles, decorative elements with no
information value.

Do **not** replace these with another fashionable trend. Establish a DMZ design language.

---

## 3. The DMZ visual philosophy

A hybrid of:

- **Cryptographic instrument** — precise, restrained, technical.
- **Financial terminal** — dense when appropriate, information-rich, extremely legible.
- **Independent software** — opinionated rather than corporate.
- **Privacy infrastructure** — quiet, deliberate, discreet.
- **Modern editorial design** — excellent typography, spacing and hierarchy.
- **Physical instrument** — controls that feel deliberate and tactile rather than like generic web buttons.

It should communicate *"this software was engineered for a specific purpose"* rather than *"this is a template
customized for crypto."*

### 3.1 Create a DMZ signature — 2 to 4 motifs that belong to DMZ alone

Explore, and choose:

**A. The zone.** Subtle spatial divisions representing the boundary. Instead of cards everywhere: bounded
information regions, fine dividing lines, offset sections, technical grid relationships, deliberate negative
space, asymmetric layouts. The UI should sometimes feel like looking through an instrument rather than
navigating a dashboard.

**B. The three networks.** DERO / XMR / ZEC. Do not assign them random colours. Build a visual grammar that tells
a user which network an asset belongs to, where it originated, where it is going, what settlement mechanism is
involved, whether the asset is native or represented, whether an operation is pending. Useful information
architecture, not decoration.

**C. Cryptographic precision.** Quiet references to hashes, transaction identifiers, block heights, proofs,
commitments, signatures, network states, confirmations. No Matrix rain, no cyberpunk, no terminal cosplay. Quiet
technical sophistication.

### 3.2 The product should feel quiet

It should not scream CRYPTO. It should whisper INFRASTRUCTURE. Confident enough not to decorate itself. The
sophistication comes from restraint.

### 3.3 Do not overdesign

The common failure mode when asked for "unique" is to add gradients, animations, decorative graphics, unusual
layouts, strange fonts, floating elements, visual effects. Do not. A world-class interface is distinctive through
extremely subtle decisions. Recognizable, not bizarre. Premium, not flashy. Technical, not nerdy. Private, not
paranoid. Financial, not casino-like. Decentralized, not "Web3."

### 3.4 References — conceptual only

Study the *principles* behind professional trading terminals, high-end financial software, Swiss editorial design,
industrial instrumentation, cryptographic software, aerospace instrumentation, high-end developer tools, Japanese
industrial UI, brutalist information design, contemporary typography. Do not copy any product. Do not clone
Bloomberg, TradingView, Coinbase, Uniswap, Jupiter, Pump.fun or Dexscreener.

---

## 4. The design system to build

Do not make isolated tweaks. Create reusable primitives that all share the same tokens.

```
DMZ Design System
 Typography   Display · Heading · Body · Label · Technical · Numeric
 Surfaces     Base · Raised · Instrument · Data
 Controls     Primary · Secondary · Ghost · Toggle · Selector · Icon
 Data         Metric · Price · Network · Transaction · Status · Activity
 Navigation   Sidebar · Header · Market ticker · Breadcrumb
 Feedback     Success · Warning · Error · Pending · Empty
```

### 4.1 Colour

A deliberate system, not sprinkled gradients. Base: near-black, charcoal, graphite, restrained off-white. Accents
have semantic purpose only — positive, negative, network, action, warning. Do not use purple because purple is
common; if any purple remains it must be justified by the DMZ identity. Consider whether a more distinctive accent
system makes the product immediately recognizable.

### 4.2 Surfaces and materiality

Extremely subtle material differences: graphite, black, slightly lifted black, fine borders, hairline dividers,
subtle grain only if it costs nothing. No obvious gradients, no glassmorphism, no heavy shadows. The interface
should feel solid.

### 4.3 Three levels of information surface (rethink cards)

- **Level 1 — Instrument.** High-importance information. Minimal border, strong hierarchy, large typography, very
  little decoration. Example: portfolio balance.
- **Level 2 — Module.** Functional grouping (market, trade, portfolio, feed). May use borders and subtle
  backgrounds.
- **Level 3 — Data.** Small units (volume, liquidity, FDV, price, block, confirmations). Usually **without** a
  card.

The goal: no more card inside card inside card.

### 4.4 Typography — DMZ's strongest identifying feature

(See §1.1: system stacks only, unless a subsetted face is proven under the DOC cap.)

- **Primary display type** — a distinctive grotesk feel achieved through weight, size, tracking and tabular
  numerals. Large numbers must feel *intentional*: `1.906 DERO` should read as a financial instrument reading,
  not a dashboard statistic.
- **Secondary technical type** — restrained monospace for contract IDs, SCIDs, transaction IDs, block heights,
  addresses, timestamps, network identifiers, technical metadata. **Not** monospace everywhere. The contrast
  between human-readable and machine-readable type becomes part of the identity.

### 4.5 Technical data component

One consistent component for SCID, address, TX, BLOCK, CONFIRMATIONS, SETTLEMENT: monospace, excellent spacing,
intelligent truncation that stays copyable, copy controls without clutter. Long strings must never be
ellipsised into unreadability where the full value is the point (deposit addresses, destinations, txids —
`wrap.js` already wraps these with `word-break:break-all`; keep that behaviour).

### 4.6 Iconography

A coherent icon language; bespoke where it matters — DMZ mark, network state, settlement, atomic swap, privacy
state, markets, portfolio, activity. Geometric, simple, highly legible, slightly distinctive. No detailed
illustrations. They live in the `P` map in `ui.js` and are bytes: keep paths short.

### 4.7 The DMZ mark

A system identifier, not a SaaS logo. Explore *three systems separated by a boundary* / *a zone between three
cryptographic networks*: split geometry, negative space, boundary, three-part structure, offset planes, protected
zone. No military imagery — no barbed wire, no camouflage, no stencil. The acronym should be sophisticated.

### 4.8 Microinteractions

A subtle interaction language: balances update smoothly (live.js already rolls numbers — refine, do not replace),
market values transition intelligently, rows respond to hover, buttons have tactile press states, navigation is
quick, trade state changes are clearly animated, confirmations feel deliberate, network indicators pulse very
subtly. 100–250 ms, purposeful. No spring physics, bouncing cards, huge transitions, flashy animations,
gratuitous loaders. DMZ should feel fast. Honour `prefers-reduced-motion`.

### 4.9 Information density

Adapt density to the task: markets dense; portfolio spacious; trade execution focused; technical transaction
screens extremely information-rich. Do not over-optimise for minimal SaaS emptiness.

### 4.10 Remove visual noise

For every element: does it communicate information, hierarchy, state or affordance? If not, remove it. Do not
decorate a region because it feels empty. Negative space is valuable.

---

## 5. Screen by screen

### 5.1 Navigation

The sidebar resembles conventional SaaS navigation. Make it a navigation instrument. A grouping like
`ZONE (Home · Markets · Trade) / ASSETS (Portfolio · NFTs · Wrap) / IDENTITY (Profile)` is acceptable **only if it
improves the information architecture** — no category labels for aesthetics. Navigation must be immediately
understandable. The wrap screen (`#wrap`) currently has no nav entry and is reached from Portfolio; decide where
it belongs.

### 5.2 Header and market ticker

The ticker (`#tape`, driven by `live.js`) looks generic. Make it a market telemetry strip: extremely thin,
extremely clean, small technical type — `DERO 0.09292 ▲0.4%  XMR 312.42 —0.2%  ZEC 41.20 ▲1.1%` — possibly with
block state. Ambient, never dominant.

Network state (Connected · Connecting · Degraded · Offline · Syncing · Unknown) is a tiny indicator plus precise
text, not a banner. No glowing LIVE badge.

### 5.3 Home — a private market console

Do not stack portfolio card / promo card / trending cards / feed cards. Compose: persistent navigation left,
primary market/portfolio state centre, activity / watchlist / market intelligence right — and let it breathe.
Editorial and asymmetric, not a Bootstrap grid.

Current problems visible in the render: excessive rounded cards; generic purple glow; weak typographic
personality; reliance on pills; generic card grid; insufficient distinction between important and secondary
information; template-like trending cards; the feed disconnected from the primary experience; SaaS sidebar;
generic ticker; composition centred rather than composed.

### 5.4 Portfolio — the major visual anchor

Not `1.906 DERO / 0.00%`. A financial display:

```
TOTAL PRIVATE BALANCE
1.906 DERO
≈ $X.XX                       ← only if a price source exists; never invent one
0 tokens · 1.9 DERO available
```

Network status quiet and clear. The wrapped-assets entry point stays.

### 5.5 Markets — a serious market instrument

Currently too close to a conventional screener. Prioritise asset, network, price, change, volume, liquidity,
FDV, activity, market state. Alignment is everything: numbers line up, decimals are consistent, column spacing
is intentional, hover is subtle. Not every row a rounded card — horizontal rules, whitespace, restrained
surfaces. Keep the filters and sorts working (Trending / New / Gainers / Losers / Graduating / Graduated / Pools /
Watchlist; Hot / Volume / Change / Liquidity / FDV / Newest), keep the compact mode, keep the impersonation
glyph (`⚠` with title) that `markets.js` writes on every pass.

Current problems: every market a rounded rectangle; no terminal refinement; generic typography; weak hierarchy
between name / price / liquidity / volume; SaaS pill controls; interchangeable purple accent; no sense of market
intelligence; no DMZ identity.

### 5.6 Charts — a DMZ signature

Do not copy TradingView. Analytical, restrained, extremely legible, information-dense. Subtle grid. No giant
gradient fills, neon, rainbow, glow. The chart is the hero. It is hand-drawn SVG in `chart.js`; improve axis
labelling, tick density, empty and single-point states, high-DPI crispness, tooltip/crosshair. Zoom, pan and
timeframes are in the backlog and welcome if they stay dependency-free and under the cap.

### 5.7 Trade — a professional execution instrument

```
FROM      asset selector
TO        asset selector
AMOUNT    large input
RATE      current quote
ROUTE     settlement path
PRIVACY / SETTLEMENT STATE   what is happening, no more than necessary
EXECUTE
```

Calm. A user should never feel they are gambling. No gamified aesthetics. Design every state deliberately:
Idle · Selecting asset · Entering amount · Quote loading · Quote available · Awaiting approval · Executing ·
Pending · Partially completed · Completed · Failed · Expired · Cancelled. Clear hierarchy in each; never colour
alone.

### 5.8 Privacy — visible, never gimmicky

No padlocks, shields, "100% PRIVATE", masks, surveillance graphics. Communicate through information
architecture:

```
Settlement    DERO → XMR
Method        k-of-n federation vault        ← say what it really is (§1.5)
Custody       federation holds the reserve
Counterparty  —
Status        awaiting settlement · payable at block 10 957
```

That beats any banner.

### 5.9 DERO / XMR / ZEC as first-class citizens

Not a DERO app that mentions the others. A network identity system — marks, labels, typographic identifiers,
settlement indicators, network-specific metadata — that makes `DERO ↔ XMR ↔ ZEC` obvious without a logo collage.
The wrap screen (`wrap.js`) is where this system is exercised most; the `vaultAsset` record in `config.js`
(name, symbol, chain, decimals, confirmations) is the data it renders from.

### 5.10 Empty states

Every empty state states what the area is, why it is empty, what populates it. Restrained:

```
WATCHLIST
No markets observed.
Star a market to establish a private watchlist.
EXPLORE MARKETS →
```

`U.emptyState(icon, title, hint, action)` in `ui.js` is the primitive; redesign it once.

### 5.11 Profile / identity

Identity controls, not social vanity. Distinguish public, pseudonymous, wallet, activity, preferences. Expose
nothing merely because a social app would. The settings screen's security & privacy panel stays.

### 5.12 NFTs

Not bolted on. Slightly more visual freedom, same DMZ language, no Web3 neon.

---

## 6. Responsive, accessible, fast

- Fully functional at 1440p, 1080p, laptop, tablet, mobile. Intentional compositions, not a shrunken desktop.
  On mobile: deliberate navigation, compact market rows, easy trading, prominent numbers, accessible technical
  information. The current breakpoints are 960 (sidebar shell) and 1180 (two-column pages); the UI test checks
  430, 1024 and 1280 — add 390 and 768.
- Excellent contrast, keyboard navigation, focus states, semantic HTML, screen-reader labels, readable sizes,
  reduced-motion support.
- No large image assets, animation libraries, heavy dependencies, GPU effects, blur, expensive canvas. The
  sophistication comes from typography, spacing, composition, hierarchy and interaction — not computation.

---

## 7. Method

### 7.1 Phase 1 — audit
Inspect the whole app (§1.3). Render every route (§1.4). Understand framework, components, routing, state,
data fetching, wallet, market data, trading, token creation, NFTs, portfolio, feeds, profile, responsive
behaviour, simulator, TELA and DERO integration, tokens, CSS, component library. Do not blindly replace working
components. Do not rewrite backend logic. Do not break functionality.

### 7.2 Phase 2 — design strategy, before any CSS
Write, in `docs/DESIGN_SYSTEM.md`:
- **DMZ design principles** (8–12).
- **DMZ visual vocabulary**: typography, colour, surfaces, borders, spacing, iconography, data visualisation,
  animation, layout.
- **DMZ component rules**: cards, tables, buttons, inputs, selectors, statuses, metrics, network indicators,
  charts.
Do not randomly modify CSS until the application looks "cool."

### 7.3 Phases 3–4 — the system and its primitives
Tokens first (`styles.css` `:root`), then the primitives in `ui.css` / `parts.css` / `ui.js`. Everything
downstream should change by inheritance.

### 7.4 Phase 5 — screens
Navigation → header → ticker → Home → Markets → Trade → Portfolio → Wrap → NFTs → Feed → Profile.

### 7.5 Phases 6–8 — responsive, accessibility, performance passes
Measure: the UI test's redraw timing, `genconfig -dist` sizes, screenshots at every width.

### 7.6 Phase 9–10 — consistency and regression
Every major route inspected: no overflow, no broken layouts, no inaccessible controls, no unreadable text, no
broken mobile states, no broken trade flows, wallet states, market filters, search, navigation; no lost
functionality. UI test green. Contract and Go suites untouched and green.

### 7.7 Self-critique loop — at least three passes over fresh screenshots
- **Pass 1 — genericity.** Does anything still look like AI-generated SaaS? Fix it.
- **Pass 2 — identity.** Could this screenshot be mistaken for another exchange? Fix it.
- **Pass 3 — DMZ.** Does it communicate privacy + cryptography + markets + neutral zone without cliché? If not,
  fix it.

### 7.8 Do not break functionality — non-negotiable
Preserve wallet, trading, market discovery, portfolio, token data, NFTs, feeds, profiles, navigation, search,
sorting, filtering, watchlists, simulator, network data, TELA, DERO, existing API behaviour, and every
assertion in `tools/uitest/ui.mjs`. If a component works, improve its presentation rather than rewriting its
logic.

---

## 8. Definition of done

- [ ] `docs/DESIGN_SYSTEM.md` exists with principles, vocabulary and component rules, and the code matches it.
- [ ] Tokens replaced; no `--glow-*`, no purple accent unless justified in writing; radii and surfaces per §4.
- [ ] Every screen in §5 re-rendered and critiqued three times; the screenshots are in the commit's PR/notes.
- [ ] `PW_DIR=… node tools/uitest/ui.mjs …` green at 390, 430, 768, 1024, 1280.
- [ ] `cd tools && go run ./genconfig -dist` green: every file under 18 432 DOC bytes.
- [ ] `bash tests/run.sh` and `cd tools && go test ./vaultsigner ./simrpc ./treasurybot` untouched and green.
- [ ] No new dependency, font file, or external resource (CSP unchanged).
- [ ] Copy still true to §1.5 — ring sizes, federation custody, no operator, no SAFE badge.
- [ ] A loop record appended to `docs/history/HISTORY.md`; `docs/PLATFORM_BACKLOG.md` updated; committed in
      focused pieces and pushed to `origin main`.

The bar: not "cleaner", not "colours changed", not "prettier cards". A coherent visual thesis. Logo cropped out,
still unmistakably DMZ.

**Preserve functionality. Preserve information. Preserve performance. Replace the generic visual language with a
coherent DMZ-native design system. Do not merely skin the existing UI. Develop the identity.**
