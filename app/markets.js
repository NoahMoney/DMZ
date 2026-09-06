// DMZ markets.js — the Markets tab: live table of every verified market with filters, sort, search, watchlist,
// updated in place on every block.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, H = V.home, { h, render, avatar, pill, changePill, sparkline, router, button } = U;
  const FILTERS = [["trending", "Trending"], ["new", "New"], ["gainers", "Gainers"], ["losers", "Losers"], ["grad", "Graduating"], ["done", "Graduated"], ["pools", "Pools"], ["watch", "Watchlist"]];
  const SORTS = [["hot", "Hot"], ["vol", "Volume 24h"], ["chg", "Change 24h"], ["liq", "Liquidity"], ["mcap", "FDV"], ["age", "Newest"]];
  let filter = "trending", sort = "hot", q = "", compact = !!(V.app && V.app.settings.compact);

  function select(list) {
    const watch = S.watch.all();
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
        default: return true;
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

  const rows = new Map(); // scid → {el, parts}
  function row(s, i) {
    let r = rows.get(s.scid);
    if (!r) {
      const parts = { rank: h("span", { class: "rank" }), name: h("b", { class: "ellipsis" }, s.ticker, s.dupTicker || s.lookalike ? " \u26a0" : ""), sub: h("div", { class: "sub tiny ellipsis num msub" }), nm: h("div", { class: "sub tiny ellipsis mcol", style: "text-align:left" }), badges: h("div", { class: "badges" }), star: h("span", { class: "starbox" }), spark: h("div", { class: "sp" }), price: h("div", { class: "num price" }), chg: h("div"), vol: h("div", { class: "mcol" }), liq: h("div", { class: "mcol" }), mcap: h("div", { class: "mcol" }) };
      const el = h("button", { class: "mrow", type: "button", onclick: () => router.go("token", s.scid) },
        parts.rank, avatar(s, "sm"), h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, parts.name, parts.badges, parts.star), parts.sub, parts.nm), parts.vol, parts.liq, parts.mcap, parts.spark, h("div", { class: "right" }, parts.price, parts.chg));
      r = { el, parts }; rows.set(s.scid, r);
    }
    const p = r.parts;
    p.rank.textContent = i + 1; p.rank.className = "rank" + (i < 3 ? " r" + (i + 1) : "");
    p.sub.textContent = s.name + " · V " + S.fmtDeroShort(s.m.vol24) + " · L " + S.fmtDeroShort(s.m.liq) + (s.m.mcap ? " · FDV " + S.fmtDeroShort(s.m.mcap) : "");
    p.nm.textContent = s.name; p.vol.textContent = S.fmtDeroShort(s.m.vol24); p.liq.textContent = S.fmtDeroShort(s.m.liq); p.mcap.textContent = s.m.mcap ? S.fmtDeroShort(s.m.mcap) : "—";
    render(p.badges, H.badges(s).filter(Boolean).slice(0, 1));
    render(p.star, H.star(s));
    render(p.spark, sparkline(s.points.filter((x) => x.ts >= S.now() - 86400), s.dec, (s.m.c24h || 0) >= 0));
    L.num(p.price, s.price, S.fmtPriceSub);
    render(p.chg, changePill(s.m.c24h));
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
    const draw = () => {
      drawHead(); compactBtn.classList.toggle("primary", compact); list.classList.toggle("compact", compact);
      render(chips, FILTERS.map(([v, l]) => h("button", { class: "chip " + (filter === v ? "on" : ""), type: "button", onclick: () => { filter = v; draw(); } }, l)));
      const items = select(D.list());
      const act = D.active(); const vol = act.reduce((a, s) => a + s.m.vol24, 0);
      render(summary, h("span", null, items.length + " markets · 24h flow " + S.fmtDeroShort(vol) + " DERO"), h("span", null, h("i", { class: "dot" }), " block " + (L.height || "…") + " · ", h("span", { "data-block-age": 1 }, "—")));
      if (!items.length) render(list, q.length === 64 ? U.emptyState("markets", "Open a contract", "Not listed here, but you can open it by id.", button("Open " + q.slice(0, 10) + "…", "small", () => router.go("token", q))) : (D.lastRefresh ? U.emptyState("markets", "Nothing here", filter === "watch" ? "Star a market to watch it." : "Try another filter.") : h("div", { class: "list" }, U.skeleton(5))));
      else { const els = items.map((s, i) => row(s, i)); if (els.some((e, i) => list.children[i] !== e) || list.children.length !== els.length) render(list, els); }
      const nfts = [...D.nfts.values()].filter((n) => !q || n.name.toLowerCase().includes(q) || n.scid.startsWith(q));
      render(nftBox, nfts.length && (filter === "trending" || filter === "watch") ? [h("h2", { class: "mt" }, "Holder NFTs"), h("div", { class: "list mt" }, nfts.map((n) => h("button", { class: "card tight row", type: "button", onclick: () => router.go("nft", n.scid) }, avatar({ icon: n.icon, ticker: "NFT", scid: n.scid }), h("div", { class: "grow ellipsis" }, h("b", null, n.name), h("div", { class: "sub" }, "hold " + A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker + " · " + n.minted + "/" + n.maxsupply + " minted")), pill(A.fmtDero(n.price, 1) + " DERO", "acc"))))] : null);
      L.tape(U.$("#tape"), D.trending(14));
    };
    input.oninput = () => { q = input.value.trim().toLowerCase(); draw(); };
    V.markets = { search(v) { q = String(v || "").trim().toLowerCase(); input.value = v || ""; draw(); } };
    // desktop column headers double as sort buttons; the header row sticks under the app header
    const head = h("div", { class: "mcols-head" });
    const drawHead = () => render(head, h("span", null, "#"), h("span"), [["hot", "Market"], ["vol", "Vol 24h"], ["liq", "Liquidity"], ["mcap", "FDV"], ["chg", "24h"]].map(([k, l]) => h("button", { type: "button", class: sort === k ? "on" : "", onclick: () => { sort = k; sortSel.value = k; draw(); } }, l + (sort === k ? " ▾" : ""))), h("span", null, "Price"));
    const compactBtn = button("Compact", "small", () => { compact = !compact; V.app.saveSettings({ compact }); draw(); });
    render(el, h("div", { class: "mhead" }, h("h1", null, "Markets"), h("div", { class: "row tight" }, compactBtn, sortSel)), h("div", { class: "mt" }, input), chips, summary, head, list, nftBox);
    draw(); D.onChange(() => router.current && router.current.name === "markets" && draw());
  });
})();
