// DMZ home.js — Home, a private market console: one Level 1 read-out of the market, ranked market rows and an
// activity rail. Plus the Trade hub. The shared row and feed primitives live here; markets.js, token.js and
// screens.js build on them.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, { h, render, avatar, pill, changePill, sparkline, router, button, icon } = U;
  const me = () => V.wallet.canon;
  const H = (V.home = {});
  const shown = new Map(); // scid → last price shown, so rolling numbers animate across re-renders
  H.priceEl = (s, cls = "num") => { const el = h("span", { class: cls }); el._v = shown.get(s.scid); L.num(el, s.price, S.fmtPriceSub); shown.set(s.scid, s.price); return el; };
  // Badges describe what a market IS. None of them is an action, so none of them wears the accent — lime is
  // reserved for things you can do and things that are live.
  H.badges = (s) => [s.kind === "launch" ? (s.phase === 0n ? pill("curve " + s.m.progress.toFixed(0) + "%") : pill("AMM")) : pill("pool"), s.m.isNew ? pill("NEW", "warn") : null, s.m.hot > 12 ? pill("HOT", "up") : null];
  // risk flags shown on the token page and inside the trade sheet; plain words, derived from contract state only
  // the creator allocation is not stored; it is what the supply minus the curve and the AMM reserve leaves
  H.allocation = (s) => (s.kind === "launch" && s.supply > 0n && s.supply >= s.cs + s.Lq ? s.supply - s.cs - s.Lq : 0n);
  H.allocBps = (s) => { const a = H.allocation(s); return a > 0n ? Number((a * 10000n) / s.supply) : 0; };
  H.risks = (s) => {
    const out = [];
    // First, above every other risk. The others cost a user money on a market that is what it says it is; this
    // one means the market is not that thing at all, and the vault holds nothing for it.
    if (s.impersonates) out.push({ k: "fake", t: "This is not " + s.impersonates + ". It is a token whose ticker copies the wrapped asset. No vault holds any " + s.impersonates + " for it, and it cannot be withdrawn to " + s.impersonates + " on any chain. The real one is on the Wrap screen." });
    // Second, and on the trade sheet because H.risks is what it renders: the split pair. The thinner contract is
    // the one a large order empties, and the deepest one is named in full so the reader can go there instead.
    if (s.splitWith && s.splitWith.length > 1) {
      const g = s.splitWith.map((id) => D.assets.get(id)).filter(Boolean), deep = g[0];
      const held = (x) => A.fmtCompact(D.depth(x), D.qdec(x)) + " " + D.qsym(x);
      if (deep && deep !== s) out.push({ k: "split", t: "This is the thinner of " + g.length + " contracts trading " + (s.pair || s.ticker) + ": it holds " + held(s) + " and the deepest holds " + held(deep) + ". They do not share liquidity, so a large order can empty this one while the other is untouched. Deepest: " + deep.scid });
      else if (g[1]) out.push({ k: "split", t: g.length + " separate contracts trade " + (s.pair || s.ticker) + " and do not share liquidity. This is the deepest, holding " + held(s) + "; the next holds " + held(g[1]) + "." });
    }
    if (s.kind === "launch" && H.allocBps(s) >= 100) out.push({ k: "alloc", t: "The creator received " + (H.allocBps(s) / 100).toFixed(2) + "% of the supply at launch (" + A.fmtCompact(H.allocation(s), s.dec) + " " + s.ticker + "), sellable at any time." });
    if (s.kind === "launch" && s.tax > 500n) out.push({ k: "tax", t: "High tax: " + Number(s.tax) / 100 + "% of every trade's " + D.qsym(s) + " leg goes to " + ["nobody", "the creator", "buybacks", "stakers"][Number(s.tmode)] + "." });
    if (s.m.liq < 20) out.push({ k: "thin", t: "Thin market: about " + S.fmtDeroShort(s.m.liq) + " " + D.qsym(s) + " of liquidity. Small orders move the price a lot." });
    if (s.m.isNew) out.push({ k: "new", t: "Launched " + S.ageText(s.m.age) + " ago. New markets are the most volatile." });
    return out;
  };
  H.star = (s) => { const on = S.watch.has(s.scid); return h("button", { class: "star " + (on ? "on" : ""), type: "button", "aria-label": on ? "remove from watchlist" : "add to watchlist", onclick: (e) => { e.stopPropagation(); S.watch.toggle(s.scid); D.changed(); } }, icon("star", "")); };
  // A market quoted in anything but DERO is named by its pair: a BRW / wXMR row and a BRW / DERO row are two
  // markets at two prices, and a bare ticker would make them one row shown twice. The Markets table's rule.
  H.rowName = (s) => (s.quote && s.pair) || s.ticker;
  // The strongest identity warning as a few words of text, for every row that lists a market. The words and
  // the tooltip are U.warnings', so a row, the token page and the trade sheet say the same thing about the
  // same flag; a row has room for one, and the token page says the rest. It is text and not a title because a
  // title cannot be reached on a phone. `after` is the separator to what follows, outside the coloured span.
  H.mark = (s, after) => { const w = U.warnings(s)[0]; if (!w) return null; const el = h("span", { class: "warn", title: w.title }, "\u26a0 " + w.textContent); return after ? [el, after] : el; };

  // ---- shared composition ----
  // Level 3: a label above a number, no background and no border (docs/DESIGN_SYSTEM.md § Surfaces).
  const mini = (l, v, cls, extra) => h("div", { class: "mini", style: "min-width:0;padding-right:14px" }, h("span", { class: "l" }, l), h("span", { class: "v " + (cls || "") }, v), extra || null);
  // A strip of read-outs is divided from what is above it by a hairline, never by a second box. Four across
  // wherever the shell has room, two on a phone: wrapping them by min-width lands 3+1 at some widths, and a
  // read-out that falls out of line stops being a strip.
  const wideShell = () => !!(window.matchMedia && window.matchMedia("(min-width:960px)").matches);
  const RULE = () => "display:grid;grid-template-columns:repeat(" + (wideShell() ? 4 : 2) + ",minmax(0,1fr));gap:14px 0;border-top:1px solid var(--edge);padding-top:12px";
  // Rows share one grid, so rank, ticker, sparkline, price and change line up down the whole column. The grid is
  // written here rather than in .mrow because the Markets table's eight columns do not fit a main column that has
  // a rail beside it, and this file may not add a class.
  const MROW = (w) => "grid-template-columns:20px 22px minmax(0,1fr) " + w + "px auto;gap:9px;padding:9px 2px";
  const LROW = "grid-template-columns:22px minmax(0,1fr) 98px;gap:10px;padding:9px 2px";
  const TROW = "grid-template-columns:22px minmax(0,1fr) auto auto;gap:10px;padding:9px 2px";
  const PROW = "grid-template-columns:22px minmax(0,1fr) auto;gap:10px;padding:12px 2px;margin-top:26px;border-top:1px solid var(--edge)";
  const CROW = "grid-template-columns:minmax(0,1fr) auto;gap:12px;padding:12px 2px";
  const named = (s) => h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, h("b", { class: "ellipsis" }, H.rowName(s)), h("div", { class: "badges" }, H.badges(s).filter(Boolean).slice(0, 1))), h("div", { class: "sub tiny ellipsis" }, H.mark(s, " \u00b7 "), s.name));
  // one ranked market row: the reading order of the Markets table, at rail width
  H.marketRow = (s, i, w) => h("button", { class: "mrow", style: MROW(w = w || 62), type: "button", onclick: () => router.go("token", s.scid) },
    h("span", { class: "rank" + (i < 3 ? " r" + (i + 1) : "") }, i + 1), avatar(s, "sm"), named(s),
    h("div", { class: "sp", style: "width:" + w + "px;height:24px" }, sparkline(s.points.filter((p) => p.ts >= S.now() - 86400), s, (s.m.c24h || 0) >= 0)),
    h("div", { class: "right" }, H.priceEl(s, "num price"), changePill(s.m.c24h)));
  // a launch reads by how far its curve has filled: the bar is the value, the DERO pair is the detail
  const launchRow = (s) => h("button", { class: "mrow", style: LROW, type: "button", onclick: () => router.go("token", s.scid) },
    avatar(s, "sm"),
    h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, h("b", { class: "ellipsis" }, H.rowName(s)), h("span", { class: "sub tiny ellipsis" }, H.mark(s, " \u00b7 "), s.name)), h("div", { class: "progress data", style: "margin-top:7px" }, h("i", { style: "width:" + Math.min(100, s.m.progress) + "%" }))),
    h("div", { class: "right" }, h("span", { class: "num price" }, s.phase === 0n ? s.m.progress.toFixed(0) + "%" : "AMM"),
      h("span", { class: "sub tiny" }, s.phase === 0n ? A.fmtUnits(s.real, D.qdec(s), 1) + " / " + A.fmtUnits(s.G, D.qdec(s), 0) + " " + D.qsym(s) : "locked · " + S.ageText(s.m.age) + " old")));
  // 24h flow per quote asset, largest first. There is no rate between two quotes and there must not be one, so
  // a volume is only ever added to a volume in the same quote, and the quotes are listed rather than summed.
  // The hero and the Trade hub both read this, so neither can print a figure that adds XMR to DERO.
  const flowsOf = (list) => { const byQuote = new Map(); for (const s of list) byQuote.set(D.qsym(s), (byQuote.get(D.qsym(s)) || 0) + s.m.vol24); return [...byQuote.entries()].sort((a, b) => b[1] - a[1]); };
  // the rail's first head sits flush with the top of the main column; stacked below 1180 it keeps its air
  const head = (ic, t, sub, action, flush) => { const el = U.sectionHead(ic, t, sub, action); if (flush) el.style.marginTop = "0"; return el; };

  H.followBtn = (addr, small = true, cls) => {
    if (!me() || addr === me()) return null;
    const on = D.follows.has(addr);
    return button(on ? "Following" : "Follow", cls || "small " + (on ? "" : "primary"), (e) => { e.stopPropagation(); V.app.setFollow(addr, !on); });
  };
  const seen = new Set();
  // An activity row is two lines: who and how much, then what and where in the machine voice. The 2px leading
  // edge carries the side, so nothing else has to — and the ticker is a link, not an accent badge.
  H.feedItem = (t, animate, opts = {}) => {
    const s = t.asset, buy = t.side === 1, key = s.scid + ":" + t.n, fresh = animate && seen.size && !seen.has(key); seen.add(key);
    return h("div", { class: "feed-item side" + t.side + (fresh ? " new" : "") },
      h("button", { type: "button", onclick: () => router.go("profile", t.addr), "aria-label": "profile" }, avatar(t.addr)),
      h("div", { class: "body" },
        h("div", { class: "between" },
          h("button", { class: "ellipsis", type: "button", style: "font-weight:700", onclick: () => router.go("profile", t.addr) }, D.displayName(t.addr)),
          h("span", { class: "amt num" }, (() => { const m = D.assets.get(t.asset) || D.assets.get(t.scid); return A.fmtUnits(buy ? t.in : t.out, D.qdec(m), 3) + " " + D.qsym(m); })())),
        h("div", { class: "meta" }, (buy ? "Bought " : "Sold ") + A.fmtCompact(buy ? t.out : t.in, s.dec) + " ",
          h("button", { type: "button", style: "font:inherit;letter-spacing:.06em;color:var(--fg2)", onclick: () => router.go("token", s.scid) }, s.ticker), " ", H.mark(s),
          " · block " + t.height + (s.kind === "launch" && s.gh > 0n && t.height < Number(s.gh) ? " · on the curve" : "") + " · ",
          h("span", { "data-ts": t.ts }, U.timeAgo(t.ts) + " ago"))),
      opts.noFollow ? null : H.followBtn(t.addr, true, "small"));
  };
  const feedEls = new Map(); // key → element; bounded below
  H.feedRow = (t, i) => {
    const s = t.asset, key = s.scid + ":" + t.n + "|" + D.displayName(t.addr) + "|" + (D.follows.has(t.addr) ? 1 : 0) + "|" + (me() || "");
    let el = feedEls.get(key);
    if (!el) { el = H.feedItem(t, true); feedEls.set(key, el); if (feedEls.size > 400) feedEls.delete(feedEls.keys().next().value); U.rise(el, i); }
    return el;
  };

  // ---- HOME ----
  let feedFilter = "all";
  const FEEDS = [["all", "All"], ["following", "Following"], ["watch", "Watchlist"]];
  router.on("home", (el) => {
    const draw = () => {
      const trending = D.trending(6), movers = D.movers(4);
      // a new launch and a graduating one are usually the same contract: one pipeline, closest to the AMM first
      const pipe = [...new Map([...D.graduating(4), ...D.newest(4)].map((s) => [s.scid, s])).values()].slice(0, 4);
      const feed = D.feed(feedFilter, me());
      const act = D.active(); const blocks1h = act.reduce((a, s) => a + s.m.flow1h.blocks, 0);
      // the largest quote leads because that is the market this deployment actually is
      const flows = flowsOf(act);
      const vol = flows.length ? flows[0][1] : 0, leadSym = flows.length ? flows[0][0] : "DERO";
      const hold = V.app.holdings;
      const buys = act.reduce((a, s) => a + s.m.upBlocks24, 0), sells = act.reduce((a, s) => a + s.m.downBlocks24, 0), bp = Math.round((buys / (buys + sells || 1)) * 100);
      const day = feed.reduce((n, t) => n + (t.ts >= S.now() - 86400 ? 1 : 0), 0);
      const wide = !!(window.matchMedia && window.matchMedia("(min-width:1180px)").matches), spark = wideShell() ? 84 : 62;
      // #screen sets overflow-x:hidden, so it computes overflow-y:auto and is itself the sticky scrollport: the
      // rail's top:74px lands as a constant 74px push rather than clearance under the header, and the rail never
      // actually sticks. Zero it here until the sheet stops making #screen a scroll container — the condition
      // undoes this the moment it does.
      const sc = U.$("#screen"), railTop = wide && sc && getComputedStyle(sc).overflowY !== "visible" ? "top:0" : null;
      const flowEl = h("div", { class: "big num" }); flowEl._v = shown.get("__flow"); L.num(flowEl, vol, (v) => S.fmtDeroShort(v)); shown.set("__flow", vol);
      // Level 1 — the state of the market: one labelled number, one caption, then the read-outs that qualify it.
      const hero = h("div", { class: "hero" },
        h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Flow · last 24 hours"), h("span", { class: "pill live" }, h("i", { class: "dot" }), "LIVE")),
        h("div", { class: "pulse-row mt8", style: "gap:9px" }, flowEl, h("span", { class: "dim", style: "font-weight:650;letter-spacing:-.02em;padding-bottom:5px" }, leadSym)),
        flows.length > 1 ? h("div", { class: "row tight mt8", style: "flex-wrap:wrap;gap:4px 14px" },
          flows.slice(1).map(([sym, v]) => h("span", { class: "sub num" }, S.fmtDeroShort(v) + " " + sym))) : null,
        h("div", { class: "sub" }, act.length ? "Settled through " + act.length + " market" + (act.length === 1 ? "" : "s") + " on this deployment." : "No market has traded on this deployment yet."),
        h("div", { class: "mt", style: RULE() },
          mini("Trades / h", String(blocks1h)),
          buys + sells ? mini("Buy pressure", bp + "%", bp >= 50 ? "up" : "down", h("div", { class: "pressure" }, h("i", { style: "width:" + bp + "%" }))) : mini("Buy pressure", "—"),
          mini("Markets", String(act.length)),
          mini("Block", h("span", { class: "mono" }, String(L.height || "…")), "", h("span", { class: "tiny dim2" }, h("span", { "data-block-age": 1 }, "—"), " ago"))),
        h("div", { class: "between mt", style: "border-top:1px solid var(--edge);padding-top:12px" },
          h("span", { class: "note" }, "No accounts and no backend: every number here was read from the chain by this device."),
          V.wallet.connected ? null : button([icon("wallet"), "Connect wallet"], "primary small", () => V.app.connectWallet(), { style: "flex:none;white-space:nowrap" })));
      // the wallet's own position: a read-out under the instrument, not a second instrument competing with it
      const posn = V.wallet.connected && hold ? h("div", { class: "between", style: "gap:14px;flex-wrap:wrap;border-bottom:1px solid var(--edge);padding:14px 0" },
        h("div", { class: "grow" }, h("span", { class: "eyebrow" }, "Your private balance"),
          h("div", { class: "row", style: "align-items:baseline;gap:10px;margin-top:4px" },
            (() => {
              // one line per quote held, largest first — never one number, because there is no rate to make one
              const ts = [...(hold.totals || new Map())].filter(([, t]) => t.units > 0n).sort((a, b) => (a[1].units < b[1].units ? 1 : -1));
              if (!ts.length) return h("span", { class: "display num" }, "0");
              return h("span", { class: "row tight", style: "flex-wrap:wrap;gap:4px 12px" },
                ts.map(([sym, t], i) => h("span", { class: i ? "sub num" : "display num" }, A.fmtUnits(t.units, BigInt(t.dec), 3) + " " + sym)));
            })(), changePill(hold.change24)),
          h("div", { class: "sub tiny" }, hold.rows.length + " token" + (hold.rows.length === 1 ? "" : "s") + " · " + A.fmtDero(hold.dero, 2) + " DERO free · valued on this device")),
        h("div", { class: "row tight" }, button("Portfolio", "small", () => router.go("portfolio")), button("Trade", "small primary", () => router.go("trade")))) : null;
      const f = D.founders;
      // secondary, and it says so: a hairline row at the foot of the column, no accent edge and no accent badge
      const pass = f ? h("button", { class: "mrow", style: PROW, type: "button", onclick: () => router.go("nfts") },
        avatar({ icon: f.icon, ticker: "VFP", scid: f.scid }, "sm"),
        h("div", { class: "grow ellipsis" }, h("span", { class: "eyebrow" }, "Founders pass"),
          h("div", { class: "sub tiny ellipsis" }, f.name + " · " + Number(f.share) / 100 + "% of fees · " + A.fmtDero(f.paid, 1) + " DERO paid to holders · " + (f.maxsupply - f.minted) + " of " + f.maxsupply + " left")),
        icon("arrow", "svgi dim2")) : null;
      render(el, h("div", { class: "home-grid" }, h("div", { class: "home-main" },
        hero, posn,
        head("flame", "Trending", "Most active this hour", trending.length ? h("button", { class: "linkbtn", type: "button", onclick: () => router.go("markets") }, "All markets") : null),
        trending.length ? h("div", { class: "mlist" }, trending.map((s, i) => H.marketRow(s, i, spark)))
          : D.lastRefresh ? U.emptyState("flame", null, "Nothing has traded here yet. The first pool or launch fills this list.", button("Create a market", "small primary", () => router.go("trade")))
            : h("div", { class: "list" }, U.skeleton(4)),
        movers.length ? [head("trend", "Movers", "Largest moves over 24 hours"), h("div", { class: "mlist" }, movers.map((s, i) => H.marketRow(s, i, spark)))] : null,
        pipe.length ? [head("rocket", "Launch pipeline", "Curves filling toward a locked AMM"), h("div", { class: "mlist" }, pipe.map(launchRow))] : null,
        pass),
        h("div", { class: "home-side", style: railTop },
        head("feed", "Activity", "Trades their owners chose to publish", null, wide),
        h("div", { class: "chips mt8" }, FEEDS.map(([v, l]) => h("button", { class: "chip sm " + (feedFilter === v ? "on" : ""), type: "button", onclick: () => { feedFilter = v; draw(); } }, l))),
        feed.length ? h("div", { class: "note", style: "margin:9px 0 3px" }, day + " published in the last 24h · " + feed.length + " on record") : null,
        feed.length ? h("div", { class: "list" }, feed.slice(0, wide ? 60 : 24).map((t, i) => H.feedRow(t, i)))
          : D.lastRefresh ? (feedFilter === "following" ? U.emptyState("feed", "Following", "Nobody you follow has published a trade. Publishing is opt-in on every swap.", button("Find traders", "small", () => router.go("markets")))
            : feedFilter === "watch" ? U.emptyState("feed", "Watchlist", "No market you starred has a published trade yet.", button("Browse markets", "small", () => router.go("markets")))
              : U.emptyState("feed", null, "No trade has been published yet. Publishing is opt-in on every swap.", button("Open a market", "small", () => router.go("markets"))))
            : h("div", { class: "list" }, U.skeleton(4)))));
      L.tape(U.$("#tape"), D.trending(14));
    };
    draw(); D.onChange(() => router.current && router.current.name === "home" && draw());
  });

  // ---- TRADE hub ----
  router.on("trade", (el) => {
    const draw = () => {
      const list = D.active().sort((a, b) => b.m.hot - a.m.hot);
      const flows = flowsOf(list);
      const create = (t, d, fn) => h("button", { class: "mrow", style: CROW, type: "button", onclick: fn }, h("div", { class: "grow" }, h("b", null, t), h("div", { class: "sub tiny" }, d)), icon("arrow", "svgi dim2"));
      const row = (s) => h("div", { class: "mrow", style: TROW },
        avatar(s, "sm"),
        h("button", { class: "grow ellipsis", type: "button", style: "text-align:left", onclick: () => router.go("token", s.scid) }, named(s)),
        h("div", { class: "right" }, H.priceEl(s, "num price"), changePill(s.m.c24h)),
        // the filled buy/sell colours belong to the one decisive control in the trade sheet; twenty of them down a
        // list is a casino, so here the side is a word on a secondary control
        h("div", { class: "row tight" }, button("Buy", "small", () => V.token.tradeSheet(s, 1)), button("Sell", "small", () => V.token.tradeSheet(s, 2))));
      render(el,
        // Level 1 — what executing here actually does. The two ring sizes are the facts a trader needs before the
        // sheet opens, so they are read-outs rather than fine print.
        h("div", { class: "hero mt" },
          h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Execution"), h("span", { class: "pill live" }, h("i", { class: "dot" }), "LIVE")),
          h("h1", { class: "mt8" }, "Trade"),
          h("div", { class: "sub", style: "max-width:62ch" }, "A market settles against DERO or against a wrapped asset — trade a token straight against XMR or ZEC without touching DERO beyond gas. Balances stay encrypted. An ordinary swap is a ring-size-2 call, so the address that pays is public in that transaction; private mode raises the ring to 16 and pays a named address instead."),
          h("div", { class: "mt", style: RULE() },
            mini("Markets", String(list.length)),
            // one figure per quote, the largest first and the rest beneath it — never one total, which would be
            // XMR added to DERO and printed in a currency neither of them is
            mini("Flow 24h", flows.length ? S.fmtDeroShort(flows[0][1]) + " " + flows[0][0] : "\u2014", "", flows.length > 1 ? h("span", { class: "sub tiny num" }, flows.slice(1).map(([sym, v]) => S.fmtDeroShort(v) + " " + sym).join(" \u00b7 ")) : null),
            mini("Ordinary swap", "Ring 2"),
            mini("Private mode", "Ring 16"))),
        head("bolt", "Markets", "Ordered by activity · pick a side to open the trade sheet", list.length > 12 ? h("button", { class: "linkbtn", type: "button", onclick: () => router.go("markets") }, "All markets") : null),
        list.length ? h("div", { class: "mlist" }, list.slice(0, 12).map(row))
          : U.emptyState("bolt", null, "Nothing is listed on this deployment yet.", button("Create a market", "small primary", () => V.forms.launch())),
        head("sparkle", "Create", "Two wallet confirmations: install, then register"),
        h("div", { class: "mlist" },
          create("Launch a token", "Fixed supply, bonding curve, auto-graduation into a locked AMM. Optional tax: creator, buyback-and-lock, or staker rewards.", () => V.forms.launch()),
          create("Create a pool", "LP-owned constant-product pool for any existing DERO asset.", () => V.forms.pool()),
          create("Create a holder NFT", "Editions earned by locking a token for a number of blocks.", () => V.forms.nft())));
    };
    draw(); D.onChange(() => router.current && router.current.name === "trade" && draw());
  });
})();
