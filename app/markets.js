// DMZ markets.js — the Markets tab: every verified market in one table, in views that rank it by figures the app
// reads from the chain (stats.js), with the watchlist and a search that also finds NFAs and holder NFTs.
// The table is the instrument: every head names the column under it, every numeric column is right aligned and
// tabular, and a row is one line high on a desktop — except a row whose pair is traded by more than one contract,
// which carries a second line saying so. There is no cap on the number of markets: rows are drawn a page at a time
// as the list scrolls, rows off screen are skipped by the browser, and a block updates only the rows already drawn.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, H = V.home, { h, render, avatar, changePill, sparkline, router, button } = U;
  // Networks come first: a wrapped market settles on another chain, which outranks how it is trending.
  // They are built from the deployment record, so a build with no vault simply has no network categories.
  const netFilters = () => D.vaults().map((v) => ["net:" + v.scid, U.clean(v.sym || v.name || "wrapped", 8)]);
  // A view is a filter and an order. The first five rank the way Home's discovery sections do, so "See all" there
  // opens the same ranking here over every market.
  const VIEWS = [["trending", "Trending"], ["volume", "Volume"], ["active", "Most active"], ["new", "New"], ["movers", "Movers"], ["gainers", "Gainers"], ["losers", "Losers"], ["grad", "Launches"], ["done", "Graduated"], ["pools", "Pools"], ["watch", "Watchlist"]];
  const SORTS = [["view", "View order"], ["hot", "Hot"], ["vol", "Volume 24h"], ["chg", "Change 24h"], ["liq", "Liquidity"], ["mcap", "FDV"], ["age", "Newest"]];
  const PAGE = 60;
  // watchlist snapshot for the current draw: S.watch.all() re-parses localStorage on every call
  let watching = new Set();
  let view = "trending", sort = "view", q = "", limit = PAGE, compact = !!(V.app && V.app.settings.compact);

  // ---- two markets over one pair --------------------------------------------------------------------------
  // A duplicate ticker is a naming problem; a duplicate PAIR is a money problem. Two contracts over the same base
  // AND the same quote do not share reserves: the thin one fills at a worse price and a single order can drain it
  // while the deep one sits untouched. Both are real contracts, so neither is hidden or merged — they are grouped,
  // ranked deepest first, and made to state their depth next to each other. A launch and its own graduated AMM are
  // one SCID and one record (launch.bas graduates in place), so they never appear here as two.
  // The pair key, the depth and the grouping are the data layer's (names.js decorate), so every surface agrees.
  const depth = (s) => D.depth(s);
  const dupOf = (s) => (s.splitWith ? s.splitWith.map((id) => D.assets.get(id)).filter(Boolean) : null);
  const pairName = (s) => U.pairText(s);
  // the quote each contract actually holds, only ever compared inside one pair (same asset, same decimals)
  const held = (s) => A.fmtCompact(depth(s), D.qdec(s)) + " " + D.qsym(s);
  // plain words, and the full contract ids: the id is the only thing that tells these rows apart (§Technical data)
  const dupTitle = (s, g) => g.length + " separate contracts trade " + pairName(s) + ", and they do not share liquidity."
    + " Each holds its own reserves, so the thinner one gives a worse price for the same size and a large order can empty it while the deeper one is untouched."
    + " The ticker, the name and the pair are identical; the contract id is the only thing that tells them apart."
    + " The figure is the quote each contract actually holds — the Liquidity column counts both sides of a market, so it reads about twice this."
    + "\n\nThis one: " + s.scid + "\nholds " + held(s)
    + (g[0] === s ? ", the deepest of them." : ".\n\nDeepest: " + g[0].scid + "\nholds " + held(g[0]) + ".")
    + (g.length > 2 ? "\n\nAll " + g.length + ":\n" + g.map((x) => x.scid + "  " + held(x)).join("\n") : "");

  const blocks = (s) => (s.m.upBlocks24 || 0) + (s.m.downBlocks24 || 0), posted = (s) => (s.m.buys24 || 0) + (s.m.sells24 || 0);
  const chg = (s) => (s.m.c24h === null ? 0 : s.m.c24h);
  const ORDER = {
    trending: (a, b) => b.m.hot - a.m.hot, volume: (a, b) => b.m.vol24 - a.m.vol24, active: (a, b) => blocks(b) - blocks(a) || posted(b) - posted(a),
    new: (a, b) => (b.installHeight || 0) - (a.installHeight || 0), movers: (a, b) => Math.abs(chg(b)) - Math.abs(chg(a)),
    gainers: (a, b) => chg(b) - chg(a), losers: (a, b) => chg(a) - chg(b), grad: (a, b) => b.m.progress - a.m.progress,
  };
  const KEY = { hot: (s) => s.m.hot, vol: (s) => s.m.vol24, chg: (s) => (s.m.c24h === null ? -1e9 : s.m.c24h), liq: (s) => s.m.liq, mcap: (s) => s.m.mcap || 0, age: (s) => s.installHeight || 0 };
  function select(list) {
    const watch = (watching = S.watch.all());
    let out = list.filter((s) => {
      if (!s.m || s.kind === "nft") return false;
      if (q && !V.search.market(s, q)) return false;
      switch (view) {
        // the same floor as Home's Biggest movers: a move on a market thinner than the thin line means little
        case "movers": return s.m.c24h !== null && !s.m.thin && !s.quoteDecUnknown;
        case "gainers": return s.m.c24h !== null && s.m.c24h > 0;
        case "losers": return s.m.c24h !== null && s.m.c24h < 0;
        case "active": return blocks(s) > 0;
        case "grad": return s.kind === "launch" && s.phase === 0n;
        case "done": return s.kind === "launch" && s.phase === 1n;
        case "pools": return s.kind === "pool";
        case "split": return !!dupOf(s);
        case "watch": return watch.has(s.scid);
        default: if (view.startsWith("net:")) { const v = D.vaultOf(s); return !!v && v.scid === view.slice(4); } return true;
      }
    });
    const by = sort !== "view" ? (a, b) => KEY[sort](b) - KEY[sort](a) : ORDER[view] || ORDER.trending;
    out.sort((a, b) => by(a, b) || b.m.vol24 - a.m.vol24);
    // Inside a split pair the deepest contract is always first, under every view and every sort: a thin duplicate
    // must never sit above the market that actually holds the liquidity. The group takes the rank of its
    // best-placed member and stays together, so the two depths are next to each other on screen.
    if (out.some((s) => s.splitWith)) {
      const shown = new Set(out), done = new Set(), ranked = [];
      for (const s of out) {
        const k = D.pairKey(s), g = dupOf(s);
        if (!g) { ranked.push(s); continue; }
        if (done.has(k)) continue;
        done.add(k);
        for (const m of g) if (shown.has(m)) ranked.push(m);
      }
      out = ranked;
    }
    return out;
  }

  // The trend cell is a reading, not a picture: a hairline in the direction colour, nothing filled under it.
  function trend(s, up) {
    const box = sparkline(s.points.filter((x) => x.ts >= S.now() - 86400), s, up);
    const g = box.firstChild;
    if (g) { g.querySelectorAll("defs,path[fill^='url']").forEach((n) => n.remove()); const line = g.querySelector("path,line"); if (line) line.setAttribute("stroke-width", "1"); }
    return box;
  }

  const rows = new Map(); // scid → {el, parts, key, bucket, txt, c}
  // A new 10-minute bucket re-cuts the sliding 24h window of every drawn row's sparkline. With thousands of rows
  // drawn that is seconds of work, so it runs in slices of about 8 ms between frames rather than inside one draw.
  const stale = [];
  const recut = () => { const t0 = performance.now(); while (stale.length && performance.now() - t0 < 8) { const [r, s] = stale.shift(); if (r.el.isConnected) render(r.parts.spark, trend(s, (s.m.c24h || 0) >= 0)); } if (stale.length) setTimeout(recut, 16); };
  function row(s, i) {
    let r = rows.get(s.scid);
    if (!r) {
      // Cells in the order the heads name them: # · market · vol · liquidity · FDV · age · trend · price · change.
      // .mcol cells exist only on the desktop grid, .msub cells only on the phone, where the row folds to two lines.
      // The row is not a button: the pair is, and it stretches over the whole row (parts.css), so the row is one
      // tab stop that opens the market and the star inside it is a second — a button inside a button is neither.
      const parts = { rank: h("span", { class: "rank" }), go: h("button", { class: "go", type: "button", onclick: () => router.go("token", s.scid) }), sub: h("div", { class: "sub tiny ellipsis num msub" }), nm: h("div", { class: "sub tiny ellipsis mcol nm" }), badges: h("div", { class: "badges" }), star: h("span", { class: "starbox" }), spark: h("div", { class: "sp mtrend" }), price: h("div", { class: "num price" }), chg: h("div", { class: "msub" }), chgcol: h("div", { class: "mcol", style: "width:70px" }), vol: h("div", { class: "mcol" }), liq: h("div", { class: "mcol" }), mcap: h("div", { class: "mcol mfdv" }), age: h("div", { class: "mcol" }),
        // the split-pair band spans every column on both layouts, so it is the width of the row and cannot be
        // mistaken for one more column; display:none while there is nothing to say adds no grid row
        dup: h("div", { class: "tiny mdup", style: "display:none" }) };
      const el = h("div", { class: "mrow mkt", "data-scid": s.scid },
        parts.rank, avatar(s, "sm"), h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, parts.star, parts.go, parts.badges, parts.nm), parts.sub),
        parts.vol, parts.liq, parts.mcap, parts.age, parts.spark, h("div", { class: "right" }, parts.price, parts.chg), parts.chgcol, parts.dup);
      r = { el, parts }; rows.set(s.scid, r);
    }
    const p = r.parts, dup = dupOf(s);
    p.rank.textContent = i + 1; p.rank.className = "rank" + (i < 3 ? " r" + (i + 1) : "");
    // Rewritten on every pass: a market becomes a duplicate the moment someone installs a contract over its pair,
    // and depth moves on every block. Rows are reused across draws, so a band written once would freeze.
    if (!dup) p.dup.style.display = "none";
    else {
      const me = dup.indexOf(s), deepest = dup[0];
      p.dup.style.display = ""; p.dup.title = dupTitle(s, dup);
      // the thinner contract is warned; the deepest one is the market people mean, and is told so in neutral ink
      p.dup.className = "tiny mdup" + (me === 0 ? "" : " thin");
      render(p.dup, me === 0
        ? [dup.length + " contracts trade " + pairName(s) + " · this is the deepest, holding ", h("span", { class: "num" }, held(s)), " · next holds ", h("span", { class: "num" }, held(dup[1]))]
        : [h("span", { class: "warn" }, "⚠ " + dup.length + " markets for " + pairName(s) + " · liquidity is split"), " · ",
          // "this contract holds", not "liquidity": the Liquidity column counts both sides of a market
          h("span", { class: "num" }, "this contract holds " + held(s) + " · the deepest holds " + held(deepest)), " · ", h("span", { class: "mono" }, V.shortAddr(s.scid))]);
    }
    // The name, the warning mark and the phone's figures change rarely; they are re-rendered only when their text
    // would, because U.warnings builds its pills fresh on each call and a block redraws every drawn row.
    const vol = S.fmtDeroShort(s.m.vol24), liq = S.fmtDeroShort(s.m.liq), age = S.ageText(s.m.age);
    const txt = s.name + "|" + vol + "|" + liq + "|" + age + "|" + [s.impersonates, !!s.splitWith, s.splitDeepest, s.quoteLegacy, s.baseLegacy, s.lookalike, s.dupTicker].join();
    if (r.txt !== txt) {
      r.txt = txt;
      // The mark is text beside the name it contradicts, not a tooltip: a title is unreachable on a phone. A ticker
      // another market also uses gets the contract's short id, neutral. On the phone's second line the figures come
      // before the name, so it is the name that is cut, never a figure.
      const id = s.dupTicker ? h("span", { class: "mono" }, V.shortAddr(s.scid) + " · ") : null;
      render(p.sub, U.wmark(s), H.mark(s, " · "), id, age + " · vol " + vol + " · liq " + liq + " · " + s.name);
      render(p.nm, H.mark(s, " · "), id && id.cloneNode(true), s.name);
      p.age.textContent = age;
    }
    // Each cell carries its symbol. The column head cannot, because the column is sorted across quotes
    // (docs/TODO.md §1.3): a bare 4.2 above a bare 0.0004 reads as a comparison there is no rate to make.
    const qs = " " + D.qsym(s);
    p.vol.textContent = vol + qs; p.liq.textContent = liq + qs; p.mcap.textContent = s.m.mcap ? S.fmtDeroShort(s.m.mcap) + qs : "—";
    // The expensive children are rebuilt only when their content can have changed. sig covers contract state; the
    // 10 min bucket re-cuts the sliding 24h window; the 2 s one lets a promotion land.
    const up = (s.m.c24h || 0) >= 0, bucket = Math.floor(S.now() / 600);
    const key = s.sig + "|" + up + "|" + (watching.has(s.scid) ? 1 : 0) + "|" + (compact ? 1 : 0) + "|" + H.promoted(s);
    if (r.key === key && r.bucket !== bucket) { r.bucket = bucket; stale.push([r, s]); if (stale.length === 1) setTimeout(recut, 0); }
    if (r.key !== key) {
      r.key = key; r.bucket = bucket;
      render(p.go, U.pair(s)); p.go.setAttribute("aria-label", U.pairAria(s) + ", " + s.name); p.go.title = dup ? dupTitle(s, dup) : "";
      render(p.badges, H.vmark(s), H.rowBadges(s).slice(0, 1));
      render(p.star, H.star(s));
      // compact drops the trend cell in CSS, which would slide price and change one column left of their heads
      p.spark.style.display = compact ? "block" : "";
      render(p.spark, trend(s, up));
    }
    L.num(p.price, s.price, S.fmtPriceSub);
    if (r.c !== s.m.c24h) { r.c = s.m.c24h; render(p.chg, changePill(s.m.c24h)); render(p.chgcol, changePill(s.m.c24h)); }
    return r.el;
  }

  router.on("markets", (el, param) => {
    // #markets/<view> opens a view: Home's "See all" links land here
    if (param && (VIEWS.some((v) => v[0] === param) || param === "split" || param.startsWith("net:"))) { view = param; sort = "view"; }
    limit = PAGE;
    const input = h("input", { type: "search", placeholder: "Search markets, NFAs, NFTs, tags, ids", autocomplete: "off", spellcheck: "false", "aria-label": "Search markets, NFAs and holder NFTs", value: q, class: "search" });
    const sortSel = h("select", { class: "sortsel", "aria-label": "Sort markets" }, SORTS.map(([v, l]) => h("option", { value: v, selected: v === sort }, l)));
    sortSel.onchange = () => { sort = sortSel.value; limit = PAGE; draw(); };
    const chips = h("div", { class: "chips scrollx mkt-views", role: "group", "aria-label": "Views" });
    const list = h("div", { class: "mlist" });
    const summary = h("div", { class: "sub tiny between" });
    const more = h("div", { class: "mmore" });
    const nftBox = h("div");
    const clearTo = (f) => { view = f; sort = "view"; q = ""; input.value = ""; limit = PAGE; draw(); };
    let total = 0;
    const draw = () => {
      drawHead(); compactBtn.classList.toggle("primary", compact); list.classList.toggle("compact", compact);
      sortSel.value = sort;
      // the pair groups are a property of the whole registry, not of the current view: "how many markets exist for
      // this pair" must not change because the list is filtered to Pools. The Split pairs chip exists only when
      // there is a split pair to look at, and stays while it is the active view.
      const all = D.list();
      const splitN = new Set(all.filter((s) => s.splitWith).map((s) => s.splitWith.join())).size;
      const extra = splitN || view === "split" ? [["split", "Split pairs"]] : [];
      render(chips, netFilters().concat(extra).concat(VIEWS).map(([v, l]) => h("button", { class: "chip " + (view === v ? "on" : ""), type: "button", "aria-pressed": String(view === v), onclick: () => { view = v; sort = "view"; limit = PAGE; draw(); } }, l)));
      const items = select(all);
      total = items.length;
      // 24h flow per quote: a volume is added only to a volume in the same asset, there being no rate between them
      const byQuote = new Map();
      for (const s of D.active()) byQuote.set(D.qsym(s), (byQuote.get(D.qsym(s)) || 0) + s.m.vol24);
      const wrapped = new Set(D.allVaults().map((v) => U.clean(v.sym || v.name || "wrapped", 10)));
      const flowText = [...byQuote].sort((a, b) => (wrapped.has(b[0]) ? 1 : 0) - (wrapped.has(a[0]) ? 1 : 0) || b[1] - a[1]).map(([sym, v]) => S.fmtDeroShort(v) + " " + sym).join(" · ");
      // before the registry is read there is no count to give, and "0 markets" would be a number nobody read
      render(summary, h("span", null, D.lastRefresh ? items.length + (q ? " matching" : "") + " market" + (items.length === 1 ? "" : "s") + " · 24h flow " + (flowText || "none") : D.lastError ? "registry unread" : "reading the registry…",
        splitN ? h("span", { class: "warn", title: "A split pair is one traded by more than one contract. They do not share liquidity, so the thinner one prices worse and can be drained. Every row of a split pair says how much its own contract holds." },
          " · " + splitN + " split " + (splitN === 1 ? "pair" : "pairs")) : null),
        h("span", null, h("i", { class: "dot" }), " block " + (L.height || "…") + " · ", h("span", { "data-block-age": 1 }, "—")));
      // an empty state names the area, says why it is empty, and offers the one action that fills it
      if (!items.length) render(list, q.length === 64 && /^[0-9a-f]+$/.test(q) ? U.emptyState("markets", "Open a contract", "Not listed here, but you can open it by id.", button("Open " + q.slice(0, 10) + "…", "small", () => router.go("token", q)))
        : !D.lastRefresh ? (D.lastError ? U.errorState("The market registry could not be read from your node: " + U.clean(D.lastError, 160), () => V.app.refresh(true)) : h("div", { class: "list" }, U.skeleton(5)))
          : view === "watch" ? U.emptyState("markets", "Watchlist", "No markets observed. Star a market to build a watchlist; it is kept on this device.", button("Explore markets", "small", () => clearTo("trending")))
            : U.emptyState("markets", "No markets", "Nothing matches " + (q ? "this search" : "this view") + " at block " + (L.height || "…") + ".", button("Show every market", "small", () => clearTo("trending"))));
      else {
        const els = items.slice(0, limit).map((s, i) => row(s, i));
        // a page appended below the drawn rows is appended; anything else is a redraw of the list
        let k = 0; while (k < els.length && list.children[k] === els[k]) k++;
        if (k < els.length || list.children.length !== els.length) { if (k === list.children.length) list.append(...els.slice(k)); else render(list, els); }
      }
      render(more, items.length > limit ? button("Show " + Math.min(PAGE * 2, items.length - limit) + " more · " + limit + " of " + items.length + " shown", "small", () => { limit += PAGE * 2; draw(); }) : null);
      if (io && items.length > limit) { io.unobserve(more); io.observe(more); }
      // A search reaches past markets: NFAs and holder NFTs matching it, each labelled by what it is.
      // With no search, the holder NFTs follow the Trending and Watchlist views as before.
      const others = q ? V.search.others(q) : [];
      const nfts = q ? [] : [...D.nfts.values()];
      render(nftBox, others.length ? [h("h2", { class: "mt" }, "Also matching"), h("div", { class: "list mt8 dz-list" }, others.map((it) => V.search.line(it)))]
        : nfts.length && (view === "trending" || view === "watch") ? [h("h2", { class: "mt" }, "Holder NFTs"), h("div", { class: "list mt" }, nfts.map((n) => h("button", { class: "card tight row", type: "button", onclick: () => router.go("nft", n.scid) }, avatar({ icon: n.icon, ticker: "NFT", scid: n.scid }), h("div", { class: "grow ellipsis" }, h("b", null, n.name), h("div", { class: "sub" }, "hold " + A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker + " · " + n.minted + "/" + n.maxsupply + " minted")), h("div", { class: "num" }, A.fmtDero(n.price, 1) + " DERO"))))] : null);
      L.tape(U.$("#tape"), D.trending(14));
    };
    // the next page is drawn as the end of the list comes within about a screen of view; the button does the same
    // for a keyboard, and for a browser with no IntersectionObserver
    const io = typeof IntersectionObserver === "function" ? new IntersectionObserver((es) => { if (es.some((e) => e.isIntersecting) && more.isConnected && total > limit) { limit += PAGE * 2; draw(); } }, { rootMargin: "900px 0px" }) : null;
    let t = 0;
    input.oninput = () => { clearTimeout(t); t = setTimeout(() => { q = V.search.norm(input.value); limit = PAGE; draw(); }, 120); };
    V.markets = { search(v) { q = V.search.norm(v); input.value = v || ""; limit = PAGE; draw(); }, view(v) { clearTo(v); } };
    // desktop column heads double as sort buttons and stick under the app header. Their order is the row's order,
    // so a head always sits over the number it names; the trend cell is labelled, not left anonymous.
    const head = h("div", { class: "mcols-head" });
    const sortBtn = (k, l, style) => h("button", { type: "button", class: sort === k ? "on" : "", style, onclick: () => { sort = k; limit = PAGE; draw(); } }, l + (sort === k ? " ▾" : ""));
    const drawHead = () => render(head, h("span", null, "#"), h("span"), sortBtn("hot", "Market"), sortBtn("vol", "Vol 24h"), sortBtn("liq", "Liquidity"), sortBtn("mcap", "FDV"), sortBtn("age", "Age"),
      h("span", { style: "text-align:left" }, "Trend"), h("span", null, "Price"), sortBtn("chg", "Chg 24h", "width:70px"));
    const compactBtn = button("Compact", "small", () => { compact = !compact; V.app.saveSettings({ compact }); draw(); }, { "aria-pressed": String(compact) });
    render(el, h("div", { class: "mhead" }, h("h1", null, "Markets"), h("div", { class: "row tight" }, compactBtn, sortSel)), h("div", { class: "mt" }, input), chips, summary, head, list, more, nftBox);
    if (io) io.observe(more);
    draw(); D.onChange(() => router.current && router.current.name === "markets" && draw());
  });
})();
