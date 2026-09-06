// DMZ home.js — Home (tape, hero, movers, new, graduating, live feed) and the Trade hub. Shared cards.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, { h, render, avatar, pill, changePill, sparkline, router, button, icon } = U;
  const me = () => V.wallet.canon;
  const H = (V.home = {});
  const shown = new Map(); // scid → last price shown, so rolling numbers animate across re-renders
  H.priceEl = (s, cls = "num") => { const el = h("span", { class: cls }); el._v = shown.get(s.scid); L.num(el, s.price, S.fmtPriceSub); shown.set(s.scid, s.price); return el; };
  H.badges = (s) => [s.kind === "launch" ? (s.phase === 0n ? pill("curve " + s.m.progress.toFixed(0) + "%", "acc") : pill("AMM", "up")) : pill("pool", "acc"), s.m.isNew ? pill("NEW", "warn") : null, s.m.hot > 12 ? pill("HOT", "down") : null];
  // risk flags shown on the token page and inside the trade sheet; plain words, derived from contract state only
  // the creator allocation is not stored; it is what the supply minus the curve and the AMM reserve leaves
  H.allocation = (s) => (s.kind === "launch" && s.supply > 0n && s.supply >= s.cs + s.Lq ? s.supply - s.cs - s.Lq : 0n);
  H.allocBps = (s) => { const a = H.allocation(s); return a > 0n ? Number((a * 10000n) / s.supply) : 0; };
  H.risks = (s) => {
    const out = [];
    if (s.kind === "launch" && H.allocBps(s) >= 100) out.push({ k: "alloc", t: "The creator received " + (H.allocBps(s) / 100).toFixed(2) + "% of the supply at launch (" + A.fmtCompact(H.allocation(s), s.dec) + " " + s.ticker + "), sellable at any time." });
    if (s.kind === "launch" && s.tax > 500n) out.push({ k: "tax", t: "High tax: " + Number(s.tax) / 100 + "% of every trade's DERO leg goes to " + ["nobody", "the creator", "buybacks", "stakers"][Number(s.tmode)] + "." });
    if (s.m.liq < 20) out.push({ k: "thin", t: "Thin market: about " + S.fmtDeroShort(s.m.liq) + " DERO of liquidity. Small orders move the price a lot." });
    if (s.m.isNew) out.push({ k: "new", t: "Launched " + S.ageText(s.m.age) + " ago. New markets are the most volatile." });
    return out;
  };
  H.star = (s) => { const on = S.watch.has(s.scid); return h("button", { class: "star " + (on ? "on" : ""), type: "button", "aria-label": on ? "remove from watchlist" : "add to watchlist", onclick: (e) => { e.stopPropagation(); S.watch.toggle(s.scid); D.changed(); } }, icon("star", "")); };

  H.tokenCard = (s) => h("button", { class: "tcard", type: "button", onclick: () => router.go("token", s.scid) },
    h("div", { class: "row" }, avatar(s, "sm"), h("div", { class: "grow ellipsis" }, h("b", null, s.ticker), h("div", { class: "sub ellipsis" }, s.name))),
    h("div", { class: "between" }, H.priceEl(s), changePill(s.m.c24h)),
    sparkline(s.points.filter((p) => p.ts >= S.now() - 86400), s.dec, (s.m.c24h || 0) >= 0),
    h("div", { class: "sub tiny" }, "Vol 24h " + S.fmtDeroShort(s.m.vol24) + " · " + s.m.blocks24 + " blk"));
  H.launchCard = (s) => h("button", { class: "tcard", type: "button", onclick: () => router.go("token", s.scid) },
    h("div", { class: "row" }, avatar(s, "sm"), h("div", { class: "grow ellipsis" }, h("b", null, s.ticker), h("div", { class: "sub ellipsis" }, s.name))),
    h("div", { class: "between" }, H.priceEl(s), pill(S.ageText(s.m.age) + " old", "dim")),
    h("div", { class: "progress" }, h("i", { style: "width:" + Math.min(100, s.m.progress) + "%" })),
    h("div", { class: "sub tiny" }, s.phase === 0n ? s.m.progress.toFixed(1) + "% to graduation · " + A.fmtDero(s.real, 1) + " / " + A.fmtDero(s.G, 0) + " DERO" : "graduated · AMM locked"));

  H.followBtn = (addr, small = true) => {
    if (!me() || addr === me()) return null;
    const on = D.follows.has(addr);
    return button(on ? "Following" : "Follow", "small " + (on ? "" : "primary"), (e) => { e.stopPropagation(); V.app.setFollow(addr, !on); });
  };
  const seen = new Set();
  H.feedItem = (t, animate, opts = {}) => {
    const s = t.asset, buy = t.side === 1, key = s.scid + ":" + t.n, fresh = animate && seen.size && !seen.has(key); seen.add(key);
    return h("div", { class: "feed-item side" + t.side + (fresh ? " new" : "") },
      h("button", { type: "button", onclick: () => router.go("profile", t.addr), "aria-label": "profile" }, avatar(t.addr)),
      h("div", { class: "body" },
        h("div", { class: "between" }, h("button", { class: "ellipsis", type: "button", style: "font-weight:700", onclick: () => router.go("profile", t.addr) }, D.displayName(t.addr)), h("span", { class: "meta", "data-ts": t.ts }, U.timeAgo(t.ts) + " ago")),
        h("div", { class: "row tight", style: "flex-wrap:wrap" }, h("span", { class: "amt" }, buy ? "Bought" : "Sold"), h("span", { class: "num" }, A.fmtCompact(buy ? t.out : t.in, s.dec)), h("button", { class: "pill acc", type: "button", onclick: () => router.go("token", s.scid) }, s.ticker), h("span", { class: "dim" }, "for"), h("b", { class: "num" }, A.fmtDero(buy ? t.in : t.out, 3) + " DERO")),
        h("div", { class: "meta" }, "block " + t.height + (s.kind === "launch" && s.gh > 0n && t.height < Number(s.gh) ? " · on the curve" : ""))),
      opts.noFollow ? null : H.followBtn(t.addr));
  };
  const feedEls = new Map(); // key → element; bounded below
  H.feedRow = (t, i) => {
    const s = t.asset, key = s.scid + ":" + t.n + "|" + D.displayName(t.addr) + "|" + (D.follows.has(t.addr) ? 1 : 0) + "|" + (me() || "");
    let el = feedEls.get(key);
    if (!el) { el = H.feedItem(t, true); feedEls.set(key, el); if (feedEls.size > 400) feedEls.delete(feedEls.keys().next().value); U.rise(el, i); }
    return el;
  };
  const strip = (ic, title, sub, items, card, empty) => [U.sectionHead(ic, title, sub, items.length > 4 ? h("button", { class: "linkbtn", type: "button", onclick: () => router.go("markets") }, "See all") : null), items.length ? h("div", { class: "carousel" }, items.map((s, i) => U.rise(card(s), i))) : h("div", { class: "note mt8" }, empty)];
  const empty = (txt, action) => U.emptyState("feed", null, txt, action);

  // ---- HOME ----
  let feedFilter = "all";
  router.on("home", (el) => {
    const draw = () => {
      const trending = D.trending(8), movers = D.movers(8), newest = D.newest(8), grad = D.graduating(8);
      const feed = D.feed(feedFilter, me());
      const act = D.active(); const vol = act.reduce((a, s) => a + s.m.vol24, 0), blocks1h = act.reduce((a, s) => a + s.m.flow1h.blocks, 0);
      const hold = V.app.holdings;
      const buys = act.reduce((a, s) => a + s.m.upBlocks24, 0), sells = act.reduce((a, s) => a + s.m.downBlocks24, 0), bp = Math.round((buys / (buys + sells || 1)) * 100);
      const flowEl = h("div", { class: "big num" }); flowEl._v = shown.get("__flow"); L.num(flowEl, vol, (v) => S.fmtDeroShort(v)); shown.set("__flow", vol);
      const hero = V.wallet.connected && hold
        ? h("div", { class: "hero card" }, h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Portfolio"), h("span", { class: "pill live" }, h("i", { class: "dot" }), "LIVE")),
          (() => { const el = h("div", { class: "big num mt8" }); el._v = shown.get("__pf"); L.num(el, hold.total, (v) => v.toFixed(3) + " DERO"); shown.set("__pf", hold.total); return el; })(),
          h("div", { class: "row mt8" }, changePill(hold.change24), h("span", { class: "sub" }, hold.rows.length + " tokens · " + A.fmtDero(hold.dero, 2) + " DERO free")),
          h("div", { class: "row mt" }, button("Portfolio", "small", () => router.go("portfolio")), button("Trade", "small primary", () => router.go("trade"))))
        : h("div", { class: "hero card" }, h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Market pulse · DMZ on DERO"), h("span", { class: "pill live" }, h("i", { class: "dot" }), "LIVE")),
          h("div", { class: "pulse-row mt" }, h("div", null, flowEl, h("div", { class: "sub" }, "DERO moved through " + act.length + " market" + (act.length === 1 ? "" : "s") + " in 24h")),
            h("div", { class: "row", style: "gap:22px" }, h("div", { class: "mini" }, h("span", { class: "l" }, "Trades / h"), h("span", { class: "v" }, String(blocks1h))), h("div", { class: "mini" }, h("span", { class: "l" }, "Buy pressure"), h("span", { class: "v " + (bp >= 50 ? "up" : "down") }, bp + "%")))),
          h("div", { class: "pressure mt" }, h("i", { style: "width:" + bp + "%" })),
          h("div", { class: "between mt" }, h("span", { class: "sub" }, "Block " + (L.height || "…") + " · ", h("span", { "data-block-age": 1 }, "—"), " ago · no server, no accounts"), V.wallet.connected ? null : button([icon("wallet"), "Connect wallet"], "primary small", () => V.app.connectWallet())));
      const f = D.founders;
      const pass = f ? h("button", { class: "card row founders", type: "button", onclick: () => router.go("nfts") }, avatar({ icon: f.icon, ticker: "VFP", scid: f.scid }), h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, h("b", null, f.name), pill(Number(f.share) / 100 + "% of fees", "acc")), h("div", { class: "sub ellipsis" }, A.fmtDero(f.paid, 1) + " DERO paid to holders · " + (f.maxsupply - f.minted) + " of " + f.maxsupply + " left")), icon("arrow", "svgi dim2")) : null;
      render(el, h("div", { class: "home-grid" }, h("div", { class: "home-main" },
        hero, pass,
        strip("flame", "Trending", "Most active this hour", trending, H.tokenCard, D.lastRefresh ? "No pools or launches yet. Be the first from the Trade tab." : "Loading…"),
        movers.length ? strip("trend", "Movers", "Biggest 24h moves", movers, H.tokenCard, "") : null,
        newest.length ? strip("rocket", "New launches", "Fresh curves", newest, H.launchCard, "") : null,
        grad.length ? strip("cap", "Graduating soon", "Closest to a locked AMM", grad, H.launchCard, "") : null),
        h("div", { class: "home-side" },
        U.sectionHead("feed", "Feed", "Trades their owners chose to publish"),
        h("div", { class: "chips mt8 mb" }, [["all", "All"], ["following", "Following"], ["watch", "Watchlist"]].map(([v, l]) => h("button", { class: "chip sm " + (feedFilter === v ? "on" : ""), type: "button", onclick: () => { feedFilter = v; draw(); } }, l))),
        feed.length ? h("div", { class: "list" }, feed.slice(0, 60).map((t, i) => H.feedRow(t, i))) : (D.lastRefresh ? empty(feedFilter === "all" ? "No published trades yet." : feedFilter === "following" ? "Follow traders from their profile to build this feed." : "Star a market to watch it here.") : h("div", { class: "list" }, U.skeleton(4))))));
      L.tape(U.$("#tape"), D.trending(14));
    };
    draw(); D.onChange(() => router.current && router.current.name === "home" && draw());
  });

  // ---- TRADE hub ----
  router.on("trade", (el) => {
    const draw = () => {
      const list = D.active().sort((a, b) => b.m.hot - a.m.hot);
      const create = (t, d, fn) => h("button", { class: "card row", type: "button", onclick: fn }, h("div", { class: "grow" }, h("b", null, t), h("div", { class: "sub" }, d)), icon("arrow", "svgi dim2"));
      render(el,
        h("h1", { class: "mt" }, "Trade"),
        h("div", { class: "sub mb" }, "Swap any listed asset against DERO. Balances stay encrypted; the contract only sees your address."),
        list.length ? h("div", { class: "list" }, list.slice(0, 20).map((s) => h("div", { class: "card tight row" }, avatar(s), h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, h("b", null, s.ticker), changePill(s.m.c24h)), h("div", { class: "sub" }, H.priceEl(s, "num"), " DERO")), button("Buy", "small buy", () => V.token.tradeSheet(s, 1)), button("Sell", "small sell", () => V.token.tradeSheet(s, 2))))) : empty("No markets yet."),
        U.sectionHead("sparkle", "Create", "Two wallet confirmations: install, then register"),
        h("div", { class: "list mt" },
          create("Launch a token", "Fixed supply, bonding curve, auto-graduation into a locked AMM. Optional tax: creator, buyback-and-lock, or staker rewards.", () => V.forms.launch()),
          create("Create a pool", "LP-owned constant-product pool for any existing DERO asset.", () => V.forms.pool()),
          create("Create a holder NFT", "Editions earned by locking a token for a number of blocks.", () => V.forms.nft())));
    };
    draw(); D.onChange(() => router.current && router.current.name === "trade" && draw());
  });
})();
