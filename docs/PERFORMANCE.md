# Performance at scale: the data layer

What one browser tab asks of the user's own DERO node, and what it costs the page, as the registry grows from
50 to 5,000 markets. Covers `app/feed.js`, `app/live.js`, `app/store.js`, `decorate()` in `app/names.js`, and
the refresh timer in `app/app.js`. `store.js` is the localStorage layer, split out of `feed.js` so that every file
keeps at least 1,000 bytes of headroom under the 18,250-byte DOC cap. Measured 2026-09-15 on the `perf-scale` branch.

## How it was measured

`tools/uitest/scale/` is a test-only harness. It is never shipped in `app/`, and nothing it measures is a
product number.

- `mock.mjs` is a mock DERO node. It serves N synthetic markets with the keys `launch.bas` and `pool.bas`
  write: the fixed state keys, `p_<n>` price points and `t_<n>` published trades. They sit behind a factory
  with write-once `pool_`/`launch_` slots, over the WebSocket JSON-RPC the app uses. The mock also serves the
  app with a generated `config.js`. State is versioned by height, so a read pinned to a topoheight sees that
  state. History follows a Zipf-like curve: the busiest market has 3,000 points and most have a handful. Every
  block about 1% of markets trade, weighted toward the busy ones, and every 10 blocks a new launch registers.
- `drive.mjs` opens the app on `#markets` in headless Chromium 151 at 1280×860. It records the time to the
  first market row and to the complete list (cold with empty storage, then warm after a reload). It then waits
  2 blocks and measures over 6 whole blocks (one block every 3 s):
  - requests per block, from the mock's own counters;
  - main-thread time per block, from the CDP `TaskDuration` delta;
  - `decorate()` and `changed()` time;
  - the median refresh;
  - the JS heap after a GC;
  - the size of localStorage.
- `--see=40` simulates a virtualized Markets list. After loading, the page declares the 40 rows a first
  screenful shows (`D.see`, below). Without that flag no screen declares anything, which is how the app runs
  today.

```
PW_DIR=<playwright> node tools/uitest/scale/drive.mjs --app=<before>/app --n=50,500,2000,5000 --label=before --out=<tmp>
PW_DIR=<playwright> node tools/uitest/scale/drive.mjs --app=app --n=50,500,2000,5000 --label=after --out=<tmp>
PW_DIR=<playwright> node tools/uitest/scale/drive.mjs --app=app --n=50,500,2000,5000 --see=40 --label=after-see40 --out=<tmp>
```

`<before>` is `git archive 71c86c1 app` unpacked, which is `main` after the discovery merge. That merge pages
the Markets list, but no screen calls `D.see` yet, so the "no declaration" rows below are what today's screens
cost. The branch is rebased on `37f6c96`. The commits between the two remove the swap screens and restore a
single navigation, and none of them touches a file this harness exercises: `feed.js`, `names.js`, `stats.js`,
`rpc.js`, `live.js`, `app.js`, `markets.js`, `home.js` or `founders.js`. Runs were at below-normal
priority on one Windows machine. The node answers in microseconds, so these numbers compare builds with each
other. A real node adds its round trip to every request, which makes the request counts matter more, not less.

## Results

Each N ran as before, after with no declaration, and after with 40 rows declared, back to back. Before is `main`
at `71c86c1`, and after is this branch rebased on it. The request and key columns are exact counts from the
mock's own counters. The millisecond columns are wall clock on a machine that other processes held near 97% CPU
through every run. Read them for direction, not size: a cell that got worse in this series went the other way
in an earlier one.

| N | build | calls per market, first load | requests per block | keys per block | KB per block | requests per refresh | decorate ms per block | main thread ms per block | redraw (changed) ms per block | refresh ms (median) | first load ms | warm load ms | heap MB | localStorage k chars (copies) | errors |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 50 | before | 5.58 | 55.3 | 1,007 | 23 | 47 | 7.7 | 711 | 11 | 133 | 5,228 | 2,751 | 4.8 | 1,618 (51) | 0 |
| 50 | after, no declaration | 4.60 | 53.2 | 939 | 17 | 53 | 0.8 | 217 | 9 | 45 | 1,215 | 4,262 | 5.0 | 1,619 (51) | 0 |
| 50 | after, 40 rows declared | 4.60 | 49.2 | 867 | 16 | 49 | 0.7 | 230 | 8 | 42 | 2,453 | 1,044 | 5.1 | 1,619 (51) | 0 |
| 500 | before | 4.16 | 362.7 | 6,870 | 156 | 435 | 114.0 | 443 | 49 | 1,402 | 15,670 | 7,282 | 7.9 | 2,974 (501) | 0 |
| 500 | after, no declaration | 3.21 | 562.0 | 10,157 | 173 | 482 | 1.6 | 408 | 52 | 105 | 6,258 | 3,549 | 9.0 | 2,854 (457) | 0 |
| 500 | after, 40 rows declared | 3.16 | 122.0 | 2,209 | 39 | 105 | 1.1 | 149 | 12 | 44 | 3,101 | 1,965 | 7.9 | 2,728 (456) | 0 |
| 2,000 | before | 4.04 | 2,046.5 | 38,902 | 878 | 2,047 | 52.2 | 473 | 26 | 475 | 7,051 | 6,780 | 17.4 | 4,043 (2,001) | 0 |
| 2,000 | after, no declaration | 3.04 | 1,877.2 | 33,988 | 563 | 2,253 | 2.1 | 416 | 15 | 342 | 3,872 | 7,288 | 17.1 | 2,726 (1,132) | 0 |
| 2,000 | after, 40 rows declared | 3.05 | 355.7 | 6,570 | 126 | 305 | 8.0 | 1,108 | 186 | 572 | 8,998 | 17,262 | 13.6 | 2,828 (1,315) | 0 |
| 5,000 | before | 4.02 | 3,128.2 | 59,859 | 1,394 | 4,692 | 86.6 | 805 | 41 | 4,416 | 20,787 | 23,055 | 28.1 | 5,006 (5,002) | 0 |
| 5,000 | after, no declaration | 3.03 | 5,009.8 | 90,693 | 1,527 | 5,010 | 31.5 | 1,145 | 62 | 2,895 | 13,363 | 6,196 | 30.4 | 2,731 (1,290) | 0 |
| 5,000 | after, 40 rows declared | 3.03 | 858.5 | 15,851 | 299 | 859 | 30.2 | 1,110 | 74 | 513 | 20,861 | 11,434 | 25.9 | 2,839 (2,059) | 0 |

What the table says:

- **A first load costs a quarter fewer calls:** 3.0 per market instead of 4.0, and 4.6 instead of 5.6 at N=50,
  because a market's code and its fixed keys now share one `GetSC`.
- **With nothing declared, every block is read.** Every market is then still probed on every refresh, and a
  refresh asks 1 + N probes + 1 call per changed market; the before build asked 1 + ⌈slots/250⌉ + N + 2 per
  changed market. The before build also dropped a block that arrived while a refresh ran: the refresh returned
  the one in flight and nothing re-ran. At 5,000 its 4.4 s refresh outlasted the 3 s block, so its lower
  requests-per-block figure (3,128 against 5,010) means fewer blocks read, not cheaper reads. The after build
  queues the ask and reads every block. In a 6-block window, one refresh more or less moves this column by N/6.
- **With the shown rows declared, the node load falls with N.** Requests per block fall 363 → 122 at 500,
  2,047 → 356 at 2,000, and 3,128 → 859 at 5,000. Keys per block fall 59,859 → 15,851 at 5,000. What remains
  is the 40 shown rows, everything that changed in the last 20 blocks (about 1% of markets trade each block
  here), and an eighth of the rest.
- **decorate:** 52–114 ms per block before, 0.7–31.5 after.
- **Refresh (median) at 5,000:** 4.4 s before, 2.9 s with no declaration, 0.5 s declared. At 500: 1.4 s before,
  0.1 s after.
- **localStorage:** the before build filled the browser's quota at 5,000, with 5,006 k characters in 5,002
  keys; every copy after that failed without a word. The after build stays inside its 3,000 k budget and
  drops the least recently used copies.
- **Heap:** within noise. The trim only cuts cold markets with long histories, and most of this synthetic
  chain's long tail is short.
- **Profiled (CDP sampling, 5,000 markets, steady state):** the first after build spent 1,320 ms per block in
  `querySelectorAll`, the read-age scan (see the interface section). After the fix it spends 274 ms, where
  the before build spends 287 (the older `data-ts` / `data-block-age` scans, which are not this layer's).
  Script time went back to 286 ms per block against the before build's 295.

## What changed

- **One state per refresh.** `live.js` keeps the topoheight from `DERO.GetHeight`. A new height or topoheight
  counts as a block, because a side block changes state without a new height. Every read in a refresh is
  pinned to that topoheight, so a record is one block's state, never two mixed. Each record carries `readH`, the
  height it was confirmed at. A record already read at the current state is not read again, so a second
  refresh in the same block reads nothing.
- **Incremental registry.** `factory.bas` writes a slot once, in the call that raises its counter. The registry
  is therefore read whole once, then only the slots added since, then nothing while the topoheight is
  unchanged. If a counter goes down (another chain) or a slot fails to answer, the next refresh reads it whole.
- **Tiered refresh** (`live.js`, `plan`). These are read at every block: records never read, what a screen
  shows (`D.see`), what is starred, held or open, the other halves of a shown split pair, and anything that
  changed in the last `RECENT` (20) blocks. Everything else is cold. The oldest-read eighth of the cold tier is
  read each block, so every cold record is re-read within `COLD_EVERY` (8) blocks and the load never arrives
  all at once. A screen that declares nothing is taken to show everything, so no value goes stale unseen.
- **No key read twice in a refresh.** A probe reads 18 keys at the pinned state. A market whose probe changed
  asks, in one more request, only for the 29 fixed keys the probe did not read plus its new history. That is 2
  calls instead of 3, with no key repeated. On a first read the code and the fixed keys share one `GetSC`, so a
  new market costs 3 calls instead of 4.
- **Only what changed is decorated.** Naming and quote are redone for re-read records. The ticker and pair
  groups are redone only when a member joins, leaves or is renamed; otherwise the marks carry over and only the
  split pairs whose depth moved are re-sorted. `enrich` reruns for a re-read market, and for any market whose
  sliding windows (5m, 1h, 6h, 24h) have since crossed one of its own points (`until`). An unchanged market's
  figures are therefore recomputed exactly when they would come out different. A test compares every figure
  and mark with a full decorate.
- **Bounded memory.** A cold market keeps only the price points its figures read: the last day, plus the one
  point before it. Its figures stay exact (tested). The rest comes back from the node (`deepen`) when a page
  opens the market or the wallet holds it. Trades are kept, because the feed, profiles and P&L read them across
  every market.
- **Request hygiene.** One refresh is in flight at a time, and asks made meanwhile run once after it. A failed
  registry read backs off for 2, 4 … 60 s. During the backoff, block-driven refreshes do not reach the node;
  an explicit full refresh (Retry) still does. A failing market waits 2, 4 … 32 blocks. A refused one (wrong
  code, treasury, fee or kind, all fixed at install) is not read again until a full refresh; before, it was
  read every block. The block poll backs off to 30 s while the node fails, and slows to 10 s in a hidden tab.
  Reads stay at 4 in flight.
- **localStorage by LRU** (`store.js`). Market copies share a 3,000,000-character budget, and past it the least
  recently used go first, in one batch down to 90%. A first try at 2,000,000 kept only 226 of 500 copies and
  made warm starts re-read history; 3,000,000 still leaves over 2,000,000 of Chrome's 5,242,880 for everything
  else on the origin. A write the browser refuses first drops unlisted markets' copies,
  then the LRU. Nothing but market copies is ever evicted. Each copy leads with its last-used time (`u`), so
  the index is rebuilt without parsing a copy. Before, a full store meant a market simply had no copy.
- **Two fixes found on the way.** A reloaded record is a new object, so the 30 s write throttle never applied
  to a changed market, which was rewritten on every block it traded. It now carries its `_saved` forward. The
  trade ledger (`veil.trades`) was cut at 500 entries, which silently corrupted the P&L cost basis of anyone
  past 500 trades. The cut is gone, and market copies make room for the ledger instead.

## DOC sizes

These are gzip+base64 bytes, as `go run ./genconfig -dist -network simulator` reports them. Before is `main`
at `37f6c96`, the base this branch is rebased on. The cap is 18,250, and every file this branch touches keeps
at least 1,000 bytes under it.

| File | Before | After | Headroom |
|---|---|---|---|
| `app/feed.js` | 13,516 | 14,684 | 3,566 |
| `app/store.js` (new) | none | 4,392 | 13,858 |
| `app/live.js` | 2,368 | 6,044 | 12,206 |
| `app/names.js` | 5,792 | 7,324 | 10,926 |
| `app/app.js` | 16,004 | 15,984 | 2,266 |
| `app/parts.css` | 12,476 | 12,588 | 5,662 |
| `app/index.html` | 3,516 | 3,520 | 14,730 |

## Caps

Each cap in the data layer, and why it stays or goes.

| Cap | Value | Verdict | What it protects |
|---|---|---|---|
| `CHUNK` keys per `GetSC` | 250 | kept | one request's size for the node |
| `POOL` reads in flight | 4 | kept | the user's node answers every one of them |
| `MAXP` / `MAXT` history per market in memory | 4,000 / 2,000 | kept | memory; older history stays on chain |
| `CACHE_P` / `CACHE_T` per localStorage copy | 1,500 / 600 | kept | one copy cannot take the budget |
| `SAVE_EVERY` | 30 s | kept (now actually applied) | write churn on the shared quota |
| `LS_BUDGET` all copies together (`store.js`) | 3,000,000 chars | new | the origin's quota, shared with settings, the ledger, the watchlist and every page on the origin |
| `VERIFY_N` cached records re-read | 4 | kept | security: a forged copy is caught |
| `COLD_EVERY` | 8 blocks | new | node load from markets nothing shows |
| `RECENT` | 20 blocks | new | keeps an active market current off-screen |
| refresh backoff | 2 s doubling to 60 s | new | a failing node is not hammered |
| per-market retry | 2 doubling to 32 blocks | new | one broken contract does not cost a read every block |
| block poll | 2.5 s; 5 s doubling to 30 s while failing; 10 s hidden | kept, backoff new | node load |
| safety refresh | 45 s (120 s hidden) | kept | a missed block event |
| trade ledger cut at 500 | removed | removed | it protected nothing the budget does not, and corrupted P&L |

## Interface for the screens

These are the calls a screen uses. The data layer stays correct without any of them.

- **`D.see(ids, key)`**: the ids a surface is showing now. After virtualization, that means the rows on
  screen. Declarations belong to the current navigation (`router.gen`), one set per `key` (default `"main"`).
  A screen that declares nothing makes every record hot, which is today's behaviour and cost. This call is
  what turns on the tiered savings. A cold row that scrolls in is read within about 250 ms.
- **`D.ageTag(s)`**: a `<span class="readage" data-read="<scid>">` to put beside a record's values.
  `live.js` shows "updated N blocks ago" in it while the values come from an older read (`D.stale(s)`), and
  hides it otherwise. Any surface that shows values of a record it does not declare must carry this tag. Make
  every tag through `D.ageTag`. `live.js` keeps the tags it made in a set and never scans the document for them;
  a first version did scan, once a second, and at 5,000 rows that `querySelectorAll` alone cost about a second of
  main thread per block (profiled). A hand-written `data-read` span would therefore never be updated.
  `D.behind(s)`, `D.stale(s)`, `s.readH` and `s.everyBlock` are the raw facts behind it.
- **`s.trimmed`**: a cold market holds only its last day of points. `D.deepen(scid)` restores the whole
  window, then calls `changed()`. The data layer calls it itself when `#token/<scid>` opens and for held
  markets. A chart that shows more than 24h should say "loading older history" while `s.trimmed` is true.
- **`D.refresh(full)`**: while a refresh is in flight, the call returns a promise for the one that runs after
  it. During a backoff (`D.retryAt`) a non-full call returns without reading.
- **`D.decorate(changed)`**: with no argument it decorates everything, as before. `D.fetchKeys(scid, keys,
  topo)` and `D.loadContract(scid, prev, pin)` take an optional pin, and existing two-argument calls are
  unchanged. `D.probe` and `D.probeNFA` now return `{ sig, vals }`.

## What still bounds it

- **No batch `GetSC`.** DERO's RPC reads one contract per call, and noticing a change needs at least one call
  per market. With no screen declaring, a block still costs about N probes. The tiers cut this to the hot set
  plus N/8, and a batch read in the node is the only way below that.
- **The first load is O(n).** Each market needs its code and fixed keys, its install transaction (for age and
  "new") and its history window: 3 or more calls, 4 in flight. On a remote node with round trip r, that is
  about 3·N·r / 4, so 5,000 markets at 50 ms is about 3 minutes. The session does it once; the registry itself
  is now read once and then only its tail.
- **The registry grows O(n) on chain.** Its slots are never pruned, and every client reads the whole thing once
  per session.
- **The user's own node sets the ceiling.** It answers every request, and nothing here can make it faster.
- **Screen redraws.** `changed()` redraws the current screen on every refresh, and at 2,000 markets or more
  that redraw is most of the main-thread time left (the redraw column). The Markets list now pages its rows,
  but no screen passes them to `D.see` yet. Until one does, every market is read at every block, and the
  tiered numbers stay what the "40 rows declared" rows show them to be. That call belongs to the screens' owners.
- **Outside this layer.** `app.loadHoldings` asks the wallet for one balance per launch (O(n) wallet calls,
  at most once a minute). `loadSocial` reads the whole social contract with `variables: true`. The Founders
  Pass is read on every refresh.
