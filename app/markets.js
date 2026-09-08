// DMZ markets.js — the Markets tab: live table of every verified market with filters, sort, search, watchlist,
// updated in place on every block. The table is the instrument: every head names the column under it, every
// numeric column is right aligned and tabular, and a row is one line high — except a row whose pair is traded
// by more than one contract, which carries a second line saying so.
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

  // ---- two markets over one pair --------------------------------------------------------------------------
  // A duplicate ticker is a naming problem; a duplicate PAIR is a money problem. Two contracts over the same
  // base asset AND the same quote do not share reserves: the thin one fills at a worse price and a single order
  // can drain it while the deep one sits untouched, and nothing on the row tells them apart except the contract
  // id. Both are real contracts, so neither is hidden or merged — they are grouped, ranked deepest first, and
  // made to state their depth next to each other.
  //
  // A launch and its own graduated AMM are NOT two rows. launch.bas graduates in place (`STORE("phase", 1)`
  // inside Buy, line 71), so the curve and the AMM that replaces it are one SCID and one record: grouping by
  // asset already collapses them. What feed.js relates through `assetVerified` is a different thing — a
  // separate pool.bas contract installed over a launched token — and that one really is a second venue holding
  // its own liquidity, so it is flagged like any other duplicate.
  const ID = (x) => (/^[0-9a-f]{64}$/i.test(String(x || "")) ? String(x).toLowerCase() : "");
  // a launch's base asset is its own SCID (feed.js sets `asset: scid`); a pool names its base in `asset`.
  // Anything with no readable base asset is left ungrouped rather than lumped in with the other unreadable ones.
  // KNOWN GAP, and it belongs to feed.js, not here: loadContract() reads `qt` only in its pool branch, so every
  // launch arrives with s.quote unset and is grouped as DERO-quoted whatever launch.bas was installed with. The
  // day a launch raises in wXMR, this would put it in the same group as a DERO pool over the same token. The fix
  // is one line in feed.js (read `qt` for launches too); until then the grouping is only as right as s.quote is.
  const pairKey = (s) => { const b = ID(s.asset) || ID(s.scid); return b ? b + "/" + (ID(s.quote) || "dero") : ""; };
  // The quote the contract actually holds, in that quote's own units: a curve holds what it raised, an AMM
  // holds its quote reserve. Only ever compared inside one pair, so both sides are the same asset with the same
  // decimals — this never becomes a cross-quote comparison, which there is no rate for and must never be one.
  const depth = (s) => (s.kind === "launch" && s.phase === 0n ? s.real || 0n : s.rd || 0n);
  let pairs = new Map(); // pairKey → markets, deepest first. Only pairs traded by more than one contract.
  function findPairs(list) {
    const g = new Map();
    for (const s of list) { const k = pairKey(s); if (!k) continue; (g.get(k) || g.set(k, []).get(k)).push(s); }
    for (const [k, a] of g) { if (a.length < 2) g.delete(k); else a.sort((x, y) => (depth(y) > depth(x) ? 1 : depth(y) < depth(x) ? -1 : 0)); }
    return g;
  }
  const dupOf = (s) => pairs.get(pairKey(s)) || null;
  const pairName = (s) => s.pair || (s.ticker || "?") + " / " + D.qsym(s);
  const held = (s) => A.fmtCompact(depth(s), D.qdec(s)) + " " + D.qsym(s);
  // plain words, and the full contract ids: the id is the only thing that distinguishes these rows, so it is the
  // one value the reader must have in full (§Technical data). The deepest row names the others rather than
  // naming itself twice.
  const dupTitle = (s, g) => g.length + " separate contracts trade " + pairName(s) + ", and they do not share liquidity."
    + " Each holds its own reserves, so the thinner one gives a worse price for the same size and a large order can empty it while the deeper one is untouched."
    + " The ticker, the name and the pair are identical; the contract id is the only thing that tells them apart."
    + " The figure is the quote each contract actually holds — the Liquidity column counts both sides of a market, so it reads about twice this."
    + "\n\nThis one: " + s.scid + "\nholds " + held(s)
    + (g[0] === s ? ", the deepest of them." : ".\n\nDeepest: " + g[0].scid + "\nholds " + held(g[0]) + ".")
    + (g.length > 2 ? "\n\nAll " + g.length + ":\n" + g.map((x) => x.scid + "  " + held(x)).join("\n") : "");

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
        case "split": return !!dupOf(s);
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
    // Inside a split pair the deepest contract is always first, under every filter and every sort: a thin
    // duplicate must never be able to sit above the market that actually holds the liquidity — that ordering is
    // what a newcomer reads as "the real one". The group takes the rank of its best-placed member and stays
    // together, so the two depths are next to each other on screen rather than pages apart.
    if (pairs.size) {
      const shown = new Set(out), done = new Set(), ranked = [];
      for (const s of out) {
        const k = pairKey(s), g = pairs.get(k);
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
      const parts = { rank: h("span", { class: "rank" }), name: h("b", { class: "ellipsis" }), sub: h("div", { class: "sub tiny ellipsis num msub" }), nm: h("div", { class: "sub tiny ellipsis mcol", style: "text-align:left;flex:1;min-width:0" }), badges: h("div", { class: "badges" }), star: h("span", { class: "starbox" }), spark: h("div", { class: "sp" }), price: h("div", { class: "num price" }), chg: h("div", { class: "msub" }), chgcol: h("div", { class: "mcol", style: "width:70px" }), vol: h("div", { class: "mcol" }), liq: h("div", { class: "mcol" }), mcap: h("div", { class: "mcol" }),
        // the split-pair band: a tenth child that spans every column of the row's grid on both the phone and the
        // desktop layout, so the warning is the width of the row and cannot be mistaken for one more column.
        // display:none while there is nothing to say — a hidden grid child is not a grid item, so it adds no row.
        dup: h("div", { class: "tiny", style: "grid-column:1/-1;display:none;margin:3px 0 0;padding-left:8px;border-left:2px solid var(--warn);color:var(--fg2);white-space:normal;line-height:1.4" }) };
      const el = h("button", { class: "mrow", type: "button", onclick: () => router.go("token", s.scid) },
        parts.rank, avatar(s, "sm"), h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, parts.name, parts.badges, parts.nm, parts.star), parts.sub),
        parts.vol, parts.liq, parts.mcap, parts.spark, h("div", { class: "right" }, parts.price, parts.chg), parts.chgcol, parts.dup);
      r = { el, parts }; rows.set(s.scid, r);
    }
    const p = r.parts;
    p.rank.textContent = i + 1; p.rank.className = "rank" + (i < 3 ? " r" + (i + 1) : "");
    // written on every pass: rows are reused across renders, so a warning set once at first render would
    // freeze there, and a ticker that only later collides with another would never gain one
    // A market quoted in something other than DERO is a PAIR, and the pair is its name. Showing only the base
    // ticker would let a BRW/wXMR market and a BRW/DERO market look like the same row at two different prices.
    // A split pair always shows its full pair, not the bare ticker: the pair is the thing two contracts are
    // fighting over, and the row has to name it before the warning underneath can mean anything.
    const dup = dupOf(s);
    p.name.textContent = (s.quote || dup ? s.pair : s.ticker) + (dup || s.dupTicker || s.lookalike ? " \u26a0" : "");
    // a split pair outranks a look-alike ticker: one costs you the spread, the other only the wrong logo
    p.name.title = dup ? dupTitle(s, dup) : s.dupTicker ? "Another listed market uses a ticker that reads the same. Check the contract id before trading." : s.lookalike ? "This ticker contains characters that look like others (Cyrillic or Greek letters)." : "";
    // Rewritten on every pass, like the name and the sub line: a market becomes a duplicate the moment someone
    // else installs a contract over its pair, and depth moves on every block. A band written once at first
    // render would freeze at whatever was true then \u2014 rows are reused across draws.
    if (!dup) p.dup.style.display = "none";
    else {
      const me = dup.indexOf(s), deepest = dup[0];
      p.dup.style.display = "";
      p.dup.title = dupTitle(s, dup);
      render(p.dup,
        h("span", { class: "warn" }, "\u26a0 " + dup.length + " markets for " + pairName(s) + " \u00b7 liquidity is split"),
        " \u00b7 ",
        // "this contract holds", not "liquidity": the Liquidity column counts both sides of a market, and two
        // numbers on one row that differ by a factor of two would read as one of them being wrong. This is the
        // quote actually sitting in the contract, which is the number that says how far it can be pushed.
        h("span", { class: "num" }, me === 0
          ? "deepest of the " + dup.length + ", holding " + held(s) + " \u00b7 next holds " + held(dup[1])
          : "this contract holds " + held(s) + " \u00b7 the deepest holds " + held(deepest)),
        " \u00b7 ",
        h("span", { class: "mono" }, V.shortAddr(s.scid)));
    }
    p.sub.textContent = s.name + " · " + D.qsym(s) + " V " + S.fmtDeroShort(s.m.vol24) + " · L " + S.fmtDeroShort(s.m.liq) + (s.m.mcap ? " · FDV " + S.fmtDeroShort(s.m.mcap) : "");
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
      // the pair groups are a property of the whole registry, not of the current filter: "how many markets exist
      // for this pair" must not change because the list is filtered to Pools. Computed before the chips, because
      // the Split pairs chip only exists when there is a split pair to look at.
      const all = D.list();
      pairs = findPairs(all);
      // the chip appears only when there is a split pair to look at — and stays while it is the active filter,
      // so the last split pair closing does not leave an empty list under a filter the user can no longer see
      const extra = pairs.size || filter === "split" ? [["split", "Split pairs"]] : [];
      render(chips, netFilters().concat(extra).concat(FILTERS).map(([v, l]) => h("button", { class: "chip " + (filter === v ? "on" : ""), type: "button", onclick: () => { filter = v; draw(); } }, l)));
      const items = select(all);
      const act = D.active();
    // 24h flow per quote. Adding a market's volume to another market's volume is only meaningful when both are
    // denominated in the same asset, and there is no rate between them. Wrapped assets lead: they are the venue.
    const byQuote = new Map();
    for (const s of act) byQuote.set(D.qsym(s), (byQuote.get(D.qsym(s)) || 0) + s.m.vol24);
    const wrapped = new Set(D.vaults().map((v) => U.clean(v.sym || v.name || "wrapped", 10)));
    const flowText = [...byQuote].sort((a, b) => (wrapped.has(b[0]) ? 1 : 0) - (wrapped.has(a[0]) ? 1 : 0) || b[1] - a[1])
      .map(([sym, v]) => S.fmtDeroShort(v) + " " + sym).join(" · ");
      render(summary, h("span", null, items.length + " markets · 24h flow " + flowText,
        pairs.size ? h("span", { class: "warn", title: "A split pair is one traded by more than one contract. They do not share liquidity, so the thinner one prices worse and can be drained. Every row of a split pair says how much its own contract holds." },
          " · " + pairs.size + " split " + (pairs.size === 1 ? "pair" : "pairs")) : null),
        h("span", null, h("i", { class: "dot" }), " block " + (L.height || "…") + " · ", h("span", { "data-block-age": 1 }, "—")));
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
