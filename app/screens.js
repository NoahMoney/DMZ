// DMZ screens.js — Portfolio, Profile, Settings (with the security & privacy panel).
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, H = V.home, { h, render, avatar, pill, changePill, router, button, kv, icon } = U;
  const me = () => V.wallet.canon;
  const empty = (txt) => U.emptyState("feed", null, txt);
  const connectCTA = (txt) => h("div", { class: "card stack", style: "text-align:center;align-items:center" }, icon("wallet", "svgi dim2"), h("div", { class: "sub" }, txt), button("Connect wallet", "primary", () => V.app.connectWallet()));

  // ---- PORTFOLIO ----
  router.on("portfolio", async (el) => {
    if (!V.wallet.connected) return render(el, h("h1", { class: "mt mb" }, "Portfolio"), connectCTA("Connect your wallet to see holdings and P&L. Everything is computed on this device."));
    const hide = V.app.settings.hideBalances, mask = (s) => (hide ? "•••••" : s);
    const draw = () => {
      const hold = V.app.holdings;
      if (!hold) return render(el, h("h1", { class: "mt" }, "Portfolio"), h("div", { class: "list mt" }, U.skeleton(3)), h("div", { class: "note" }, "Reading balances through your wallet…"));
      const pnl = V.app.pnl(hold.rows);
      render(el,
        h("div", { class: "between mt" }, h("h1", null, "Portfolio"), h("span", { class: "pill live" }, h("i", { class: "dot" }), "LIVE")),
        h("div", { class: "hero card mt" }, h("div", { class: "eyebrow" }, "Total value"), (() => { const e = h("div", { class: "big num mt8" }); L.num(e, hold.total, (v) => mask(v.toFixed(3)) + " DERO"); return e; })(),
          h("div", { class: "row mt8" }, changePill(hold.change24), h("span", { class: "sub" }, "24h, from price history")),
          h("div", { class: "row mt stats3" }, h("div", { class: "stat" }, h("div", { class: "l" }, "Realised"), h("div", { class: "v num " + (pnl.realised >= 0n ? "up" : "down") }, mask(A.fmtDero(pnl.realised, 2)))), h("div", { class: "stat" }, h("div", { class: "l" }, "Unrealised"), h("div", { class: "v num " + (pnl.unrealised >= 0n ? "up" : "down") }, mask(A.fmtDero(pnl.unrealised, 2)))), h("div", { class: "stat" }, h("div", { class: "l" }, "Trades"), h("div", { class: "v" }, pnl.count)))),
        h("h3", { class: "mt" }, "Holdings value"),
        V.chart.series(null, V.app.equityPoints(hold.rows), { height: 150, empty: "Trade once and the curve starts here", wide: window.innerWidth >= 960 }),
        h("h3", { class: "mt" }, "Holdings"),
        h("div", { class: "list mt" },
          h("div", { class: "card tight row" }, avatar({ ticker: "DERO", icon: "", scid: "dero" }), h("div", { class: "grow" }, h("b", null, "DERO")), h("div", { class: "num" }, mask(A.fmtDero(hold.dero, 5)))),
          hold.rows.map((r) => h("button", { class: "card tight row", type: "button", onclick: () => router.go("token", r.s.scid) }, avatar(r.s), h("div", { class: "grow ellipsis" }, h("b", null, r.s.ticker), h("div", { class: "sub" }, mask(A.fmtUnits(r.units, r.s.dec, 4))), (() => { const b = pnl.byAsset[r.s.asset]; return b && b.avg ? h("div", { class: "sub tiny num" }, "avg " + S.fmtPriceSub(b.avg) + " · ", h("span", { class: b.unreal >= 0n ? "up" : "down" }, (b.unreal >= 0n ? "+" : "") + mask(A.fmtDero(b.unreal, 3)) + " DERO")) : null; })()), h("div", { style: "text-align:right" }, h("div", { class: "num" }, mask(A.fmtDero(r.value, 3)) + " DERO"), changePill(r.s.m.c24h)))),
          !hold.rows.length ? h("div", { class: "note" }, "No listed tokens in this wallet yet.") : null),
        D.cfg.vault ? h("button", { class: "card tight row mt", type: "button", onclick: () => router.go("wrap") },
          icon("gem", "svgi"), h("div", { class: "grow ellipsis" }, h("b", null, "Wrapped assets"), h("div", { class: "sub" }, "Deposit or withdraw the coin this vault wraps one for one")), icon("chevron", "svgi dim2")) : null,
        h("div", { class: "between mt" }, h("span", { class: "note" }, "Cost basis from " + pnl.count + " trade" + (pnl.count === 1 ? "" : "s") + ": the ones made on this device" + (pnl.chainCount ? " plus " + pnl.chainCount + " this address published from elsewhere" : "") + ". Nothing is uploaded."), button("Refresh", "small", () => V.app.loadHoldings(true))));
    };
    draw(); if (!V.app.holdings) V.app.loadHoldings(); D.onChange(() => router.current && router.current.name === "portfolio" && draw());
  });

  // ---- PROFILE ----
  router.on("profile", async (el, param) => {
    const addr = param ? V.canonAddress(param) || null : me();
    if (param && !addr) return render(el, h("div", { class: "errbox mt" }, "That is not a valid DERO address."));
    if (!addr) return render(el, h("h1", { class: "mt mb" }, "Profile"), connectCTA("Connect your wallet to have a profile. Only what you publish is visible to others."));
    if (!D.profiles.size && D.cfg.social) await D.loadSocial(me()).catch(() => {});
    const draw = () => {
      const p = D.profile(addr), mine = addr === me(), trades = D.tradesOf(addr);
      const vol = trades.reduce((a, t) => a + (t.side === 1 ? t.in : t.out), 0n);
      render(el,
        h("div", { class: "row mt" }, avatar(addr, "big"), h("div", { class: "grow" }, h("h1", { class: "ellipsis" }, D.displayName(addr)), h("button", { class: "sub mono ellipsis", type: "button", style: "display:block;max-width:100%", onclick: () => { navigator.clipboard && navigator.clipboard.writeText(addr).then(() => U.toast("Address copied", "ok")); } }, V.shortAddr(addr)), p.bio ? h("div", { class: "sub" }, p.bio) : null),
          mine ? button("Edit", "small", () => V.forms.profile(p)) : H.followBtn(addr, false)),
        h("div", { class: "stats4 mt" }, h("div", { class: "stat" }, h("div", { class: "l" }, "Followers"), h("div", { class: "v" }, D.followers.get(addr) || 0)), h("div", { class: "stat" }, h("div", { class: "l" }, "Following"), h("div", { class: "v" }, D.followingOf(addr).size)), h("div", { class: "stat" }, h("div", { class: "l" }, "Public trades"), h("div", { class: "v" }, trades.length)), h("div", { class: "stat" }, h("div", { class: "l" }, "Public vol"), h("div", { class: "v num" }, A.fmtDero(vol, 1)))),
        mine ? h("div", { class: "card mt" }, U.toggle("Hide balances", V.app.settings.hideBalances, (on) => V.app.saveSettings({ hideBalances: on })), h("div", { class: "between" }, h("span", { class: "sub" }, "Node, privacy, security, local data"), button("Settings", "small", () => router.go("settings")))) : null,
        h("div", { class: "note mt" }, mine ? "Stats only count trades you chose to publish. Your balances and unpublished trades never leave this device." : "Only trades this account published are shown; balances are private on DERO."),
        h("h3", { class: "mt" }, "Published trades"),
        trades.length ? h("div", { class: "list mt" }, trades.slice(0, 50).map((t) => H.feedItem(t, false, { noFollow: true }))) : empty("None yet."));
    };
    draw(); D.onChange(() => router.current && router.current.name === "profile" && draw());
  });

  // ---- SETTINGS ----
  router.on("settings", (el) => {
    const st = V.app.settings;
    const daemon = h("input", { value: st.daemon || "", placeholder: D.cfg.daemon + " (from wallet when connected)", autocomplete: "off", spellcheck: "false" });
    const commit = (document.querySelector('meta[name="veil-commit"]') || {}).content || "—";
    const can = (ok, t) => h("div", { class: "row tight", style: "align-items:flex-start" }, icon(ok ? "check" : "x", "svgi " + (ok ? "up" : "down")), h("span", { class: "sub" }, t));
    render(el,
      h("h1", { class: "mt" }, "Settings"),
      h("div", { class: "card stack mt" },
        h("div", { class: "field" }, "Node endpoint override (host:port on this device, or wss://host:port)", daemon),
        U.toggle("Use indexer read cache", !!st.indexer, (on) => V.app.saveSettings({ indexer: on }), "Optional Gnomon cache. The app always falls back to reading the node."),
        U.toggle("Reduce motion", !!st.reduceMotion, (on) => { V.app.saveSettings({ reduceMotion: on }); document.documentElement.classList.toggle("reduce", on); }, "Turns off rolling numbers, flashes and the tape."),
        (() => { const u = V.endpointURL(st.daemon); return u && !u.loopback && !V.app.endpointConfirmed(u.url) ? h("div", { class: "warnbox" }, "The stored node " + V.app.hostOf(u.url) + " is not used this session until you confirm it: a remote node decides what this app sees. ", button("Use it this session", "small", () => { V.app.confirmEndpoint(u.url); V.app.connectNode(); router.dispatch(); })) : null; })(),
        button("Save endpoint", "primary", () => { const v = daemon.value.trim(); const u = V.endpointURL(v); if (v && !u) return U.toast("Use host:port for a node on this device, or wss://host:port for a remote one", "err", 6000); V.app.saveSettings({ daemon: v }); if (u && !u.loopback) V.app.confirmEndpoint(u.url); V.app.connectNode(); U.toast("Saved", "ok"); router.dispatch(); })),
      h("div", { class: "card stack mt" }, h("div", { class: "row tight" }, icon("shield", "svgi up"), h("b", null, "Security & privacy")),
        U.toggle("Load token and profile images", !!st.images, (on) => { V.app.saveSettings({ images: on }); D.changed(); }, "Images come from the URL each creator set. Loading one tells that host your IP address. Sent without a referrer; off means generated avatars only."),
        h("div", { class: "stack", style: "gap:6px" },
          can(true, "Runs entirely in your browser from the DERO chain. No server, no analytics, no cookies."),
          can(true, "Reads go to your node. Transactions are built here and signed only inside your wallet after you approve them there."),
          can(true, "Every listed contract's code is checked against the shipped templates before it is shown or traded."),
          can(true, "A Content Security Policy blocks any script that is not part of this build and any connection except your node, your wallet and image hosts."),
          can(true, "Trades never accept more than 10% slippage; large price impact needs an explicit acknowledgement."),
          can(false, "Cannot see your keys, seed or balances of other people. Cannot send anything without your wallet's confirmation.")),
        kv("Build", "TELA commit " + commit), kv("Fee", Number(D.cfg.feeBps) / 100 + "% per trade to the protocol treasury")),
      h("div", { class: "card stack mt" },
        kv("Network", D.cfg.network), kv("Block", h("span", null, String(L.height || "—"), " · ", h("span", { "data-block-age": 1 }, "—"), " ago")), kv("Factory", h("span", { class: "mono" }, V.shortAddr(D.cfg.factory))), kv("Social", h("span", { class: "mono" }, V.shortAddr(D.cfg.social))),
        kv("Wallet", V.wallet.connected ? V.shortAddr(V.wallet.address) : "not connected"), kv("Node", V.node.endpoint || (V.wallet.connected ? "via wallet" : "—"))),
      h("div", { class: "card stack mt" }, h("b", null, "What publishing reveals"), h("div", { class: "note" }, "Publishing a trade reveals your address and that trade to everyone; it never reveals balances. Contract calls that pay you need ringsize 2, and anyone can compute which address paid a ringsize-2 call, not only the contract. Private mode in the trade sheet sends at ring size 16 and pays a named address instead: the payer is one of 16 accounts, the receiving address is public. Charts are exact at block resolution: the chain records the closing reserves of every active block, never individual unpublished trades."),
        button("Clear local trade history, watchlist and cache", "", () => { for (const k of Object.keys(localStorage)) if (k.startsWith("veil.") && k !== "veil.settings") localStorage.removeItem(k); U.toast("Cleared", "ok"); }),
        button("Show the intro again", "ghost", () => V.app.intro())));
  });

  router.on("nft", (el, scid) => V.nft.page(el, scid));
  router.on("token", (el, scid) => V.token.page(el, scid));
})();
