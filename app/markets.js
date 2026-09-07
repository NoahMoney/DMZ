// DMZ markets.js — the Markets tab: live table of every verified market with filters, sort, search, watchlist,
// updated in place on every block. The table is the instrument: every head names the column under it, every
// numeric column is right aligned and tabular, and a row is one line high.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, H = V.home, { h, render, avatar, changePill, sparkline, router, button } = U;
  // Networks come first: a wrapped market settles on another chain, which outranks how it is trending.
  // They are built from the deployment record, so a build with no vault simply has no network categories.
  const netFilters = () => D.vaults().map((v) => ["net:" + v.scid, U.clean(v.sym || v.name || "wrapped", 8)]);
  const FILTERS = [["trending", "Trending"], ["new", "New"], ["gainers", "Gainers"], ["losers", "Losers"], ["grad", "Graduating"], ["done", "Graduated"], ["pools", "Pools"], ["watch", "Watchlist"]];
  const SORTS = [["hot", "Hot"], ["vol", "Volume 24h"], ["chg", "Change 24h"], ["liq", "Liquidity"], ["mcap", "FDV"], ["age", "Newest"]];
    // watchlist snapshot for the current draw: S.watch.all() re-parses localStorage on every call, and row()
  // needs it once per row
  let watching = new Set();
let filter = "trending", sort = "hot", q = "", compact = !!(V.app && V.app.settings.compact);

  function select(list) {
    const watch = (watching = S.watch.all());
    let out = list.filter((s) => {
      if (q && !(s.name.toLowerCase().includes(q) || s.ticker.toLowerCase().includes(q) || s.scid.startsWith(q))) return false;
      switch (filter) {
        case "new": return s.kind === "launch" && (s.m.isNew || s.m.age === null);
        case "gainers": return s.m.c24h !== null && s.m.c24h > 0;
        case "losers": return s.m.c24h !== null && s.m.c24h < 0;
        case "grad": return s.kind === "launch" && s.phase === 0n;
        case "done": return s.kind === "launch" && s.phase === 1n;
        case "pools": return s.kind === "pool";
        case "watch": return watch.has(s.scid);
        default: {
          if (filter.startsWith("net:")) { const v = D.vaultOf(s); return !!v && v.scid === filter.slice(4); }
          return true;
        }
      }
    });
    const key = { hot: (s) => s.m.hot, vol: (s) => s.m.vol24, chg: (s) => (s.m.c24h === null ? -1e9 : s.m.c24h), liq: (s) => s.m.liq, mcap: (s) => s.m.mcap || 0, age: (s) => s.installHeight || 0 }[sort];
    // Losers is "largest fall first" unless the user picked another sort explicitly; every other filter keeps the sort
    const chg = (s) => (s.m.c24h === null ? 0 : s.m.c24h);
    if (filter === "losers" && (sort === "hot" || sort === "chg")) out.sort((a, b) => chg(a) - chg(b) || b.m.vol24 - a.m.vol24);
    else if (filter === "gainers" && (sort === "hot" || sort === "chg")) out.sort((a, b) => chg(b) - chg(a) || b.m.vol24 - a.m.vol24);
    else if (filter === "grad") out.sort((a, b) => b.m.progress - a.m.progress);
    else out.sort((a, b) => key(b) - key(a) || b.m.vol24 - a.m.vol24);
    return out;
  }

  // The trend cell is a reading, not a picture: a hairline in the direction colour, nothing filled under it.
  function trend(s, up) {
    const box = sparkline(s.points.filter((x) => x.ts >= S.now() - 86400), s.dec, up);
    const g = box.firstChild;
    if (g) {
      g.querySelectorAll("defs,path[fill^='url']").forEach((n) => n.remove());
      const line = g.querySelector("path,line"); if (line) line.setAttribute("stroke-width", "1");
    }
    return box;
  }

  const rows = new Map(); // scid → {el, parts}
  function row(s, i) {
    let r = rows.get(s.scid);
    if (!r) {
      // Nine cells in the order the heads name them: # · market · vol · liquidity · FDV · trend · price · change.
      // .mcol cells exist only on the desktop grid, .msub cells only on the phone, where the row folds to two lines.
      const parts = { rank: h("span", { class: "rank" }), name: h("b", { class: "ellipsis" }), sub: h("div", { class: "sub tiny ellipsis num msub" }), nm: h("div", { class: "sub tiny ellipsis mcol", style: "text-align:left;flex:1;min-width:0" }), badges: h("div", { class: "badges" }), star: h("span", { class: "starbox" }), spark: h("div", { class: "sp" }), price: h("div", { class: "num price" }), chg: h("div", { class: "msub" }), chgcol: h("div", { class: "mcol", style: "width:70px" }), vol: h("div", { class: "mcol" }), liq: h("div", { class: "mcol" }), mcap: h("div", { class: "mcol" }) };
      const el = h("button", { class: "mrow", type: "button", onclick: () => router.go("token", s.scid) },
        parts.rank, avatar(s, "sm"), h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, parts.name, parts.badges, parts.nm, parts.star), parts.sub),
        parts.vol, parts.liq, parts.mcap, parts.spark, h("div", { class: "right" }, parts.price, parts.chg), parts.chgcol);
      r = { el, parts }; rows.set(s.scid, r);
    }
    const p = r.parts;
    p.rank.textContent = i + 1; p.rank.className = "rank" + (i < 3 ? " r" + (i + 1) : "");
    // written on every pass: rows are reused across renders, so a warning set once at first render would
    // freeze there, and a ticker that only later collides with another would never gain one
    p.name.textContent = s.ticker + (s.dupTicker || s.lookalike ? " \u26a0" : "");
    p.name.title = s.dupTicker ? "Another listed market uses a ticker that reads the same. Check the contract id before trading." : s.lookalike ? "This ticker contains characters that look like others (Cyrillic or Greek letters)." : "";
    p.sub.textContent = s.name + " · V " + S.fmtDeroShort(s.m.vol24) + " · L " + S.fmtDeroShort(s.m.liq) + (s.m.mcap ? " · FDV " + S.fmtDeroShort(s.m.mcap) : "");
    p.nm.textContent = s.name; p.vol.textContent = S.fmtDeroShort(s.m.vol24); p.liq.textContent = S.fmtDeroShort(s.m.liq); p.mcap.textContent = s.m.mcap ? S.fmtDeroShort(s.m.mcap) : "—";
    // The three expensive children are rebuilt only when their content can actually have changed. draw() runs
    // on every block and on every keystroke in the search box, and the sparkline alone filtered the whole
    // point history and built a fresh SVG with a gradient and two long paths, per row, every time.
    const up = (s.m.c24h || 0) >= 0;
    const key = s.sig + "|" + up + "|" + (watching.has(s.scid) ? 1 : 0) + "|" + (compact ? 1 : 0) + "|" + Math.floor(S.now() / 600);
    if (r.key !== key) { // sig covers contract state; the 10 min bucket re-cuts the sliding 24h window
      r.key = key;
      render(p.badges, H.badges(s).filter(Boolean).slice(0, 1));
      render(p.star, H.star(s));
      // compact drops the trend cell in CSS, which would slide price and change one column left of their heads
      p.spark.style.display = compact ? "block" : "";
      render(p.spark, trend(s, up));
    }
    L.num(p.price, s.price, S.fmtPriceSub);
    render(p.chg, changePill(s.m.c24h));
    render(p.chgcol, changePill(s.m.c24h));
    return r.el;
  }

  router.on("markets", (el) => {
    const input = h("input", { placeholder: "Search name, ticker or SCID", autocomplete: "off", spellcheck: "false", "aria-label": "search", value: q, class: "search" });
    const sortSel = h("select", { class: "sortsel", "aria-label": "sort" }, SORTS.map(([v, l]) => h("option", { value: v, selected: v === sort }, l)));
    sortSel.onchange = () => { sort = sortSel.value; draw(); };
    const chips = h("div", { class: "chips scrollx" });
    const list = h("div", { class: "mlist" });
    const summary = h("div", { class: "sub tiny between" });
    const nftBox = h("div");
    const clearTo = (f) => { filter = f; q = ""; input.value = ""; draw(); };
    const draw = () => {
      drawHead(); compactBtn.classList.toggle("primary", compact); list.classList.toggle("compact", compact);
      render(chips, netFilters().concat(FILTERS).map(([v, l]) => h("button", { class: "chip " + (filter === v ? "on" : ""), type: "button", onclick: () => { filter = v; draw(); } }, l)));
      const items = select(D.list());
      const act = D.active(); const vol = act.reduce((a, s) => a + s.m.vol24, 0);
      render(summary, h("span", null, items.length + " markets · 24h flow " + S.fmtDeroShort(vol) + " DERO"), h("span", null, h("i", { class: "dot" }), " block " + (L.height || "…") + " · ", h("span", { "data-block-age": 1 }, "—")));
      // an empty state names the area, says why it is empty, and offers the one action that fills it
      if (!items.length) render(list, q.length === 64 ? U.emptyState("markets", "Open a contract", "Not listed here, but you can open it by id.", button("Open " + q.slice(0, 10) + "…", "small", () => router.go("token", q)))
        : !D.lastRefresh ? h("div", { class: "list" }, U.skeleton(5))
          : filter === "watch" ? U.emptyState("markets", "Watchlist", "No markets observed. Star a market to build a watchlist; it is kept on this device.", button("Explore markets", "small", () => clearTo("trending")))
            : U.emptyState("markets", "No markets", "Nothing matches " + (q ? "this search" : "this filter") + " at block " + (L.height || "…") + ".", button("Show every market", "small", () => clearTo("trending"))));
      else { const els = items.map((s, i) => row(s, i)); if (els.some((e, i) => list.children[i] !== e) || list.children.length !== els.length) render(list, els); }
      const nfts = [...D.nfts.values()].filter((n) => !q || n.name.toLowerCase().includes(q) || n.scid.startsWith(q));
      render(nftBox, nfts.length && (filter === "trending" || filter === "watch") ? [h("h2", { class: "mt" }, "Holder NFTs"), h("div", { class: "list mt" }, nfts.map((n) => h("button", { class: "card tight row", type: "button", onclick: () => router.go("nft", n.scid) }, avatar({ icon: n.icon, ticker: "NFT", scid: n.scid }), h("div", { class: "grow ellipsis" }, h("b", null, n.name), h("div", { class: "sub" }, "hold " + A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker + " · " + n.minted + "/" + n.maxsupply + " minted")), h("div", { class: "num" }, A.fmtDero(n.price, 1) + " DERO"))))] : null);
      L.tape(U.$("#tape"), D.trending(14));
    };
    input.oninput = () => { q = input.value.trim().toLowerCase(); draw(); };
    V.markets = { search(v) { q = String(v || "").trim().toLowerCase(); input.value = v || ""; draw(); } };
    // desktop column heads double as sort buttons and stick under the app header. Their order is the row's
    // order, so a head always sits over the number it names; the trend cell is labelled, not left anonymous.
    const head = h("div", { class: "mcols-head" });
    const sortBtn = (k, l, style) => h("button", { type: "button", class: sort === k ? "on" : "", style, onclick: () => { sort = k; sortSel.value = k; draw(); } }, l + (sort === k ? " ▾" : ""));
    const drawHead = () => render(head, h("span", null, "#"), h("span"), sortBtn("hot", "Market"), sortBtn("vol", "Vol 24h"), sortBtn("liq", "Liquidity"), sortBtn("mcap", "FDV"),
      h("span", { style: "text-align:left" }, "Trend"), h("span", null, "Price"), sortBtn("chg", "Chg 24h", "width:70px"));
    const compactBtn = button("Compact", "small", () => { compact = !compact; V.app.saveSettings({ compact }); draw(); });
    render(el, h("div", { class: "mhead" }, h("h1", null, "Markets"), h("div", { class: "row tight" }, compactBtn, sortSel)), h("div", { class: "mt" }, input), chips, summary, head, list, nftBox);
    draw(); D.onChange(() => router.current && router.current.name === "markets" && draw());
  });
})();
