// DMZ screens.js — Portfolio, Profile, Settings (with the security & privacy panel).
// Portfolio is the app's visual anchor: one Level 1 instrument reading, then Level 3 accounting with no box
// around it, then holdings as a hairline table with one aligned value column. Profile separates what this
// address published from what the address itself gives away from what never leaves this browser. Settings is a
// technical panel: labelled sections, hairline rows, mono for machine strings.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, H = V.home, { h, render, avatar, changePill, router, button, kv, icon } = U;
  const me = () => V.wallet.canon;
  const connectCTA = (title, why) => U.emptyState("wallet", title, why, button("Connect wallet", "primary", () => V.app.connectWallet()));
  const monoKv = (k, v) => kv(k, h("span", { class: "mono" }, v));
  // network state: a square and precise words. Never a badge, never a glow.
  const netLine = (tail) => h("div", { class: "row tight mt8" },
    h("i", { class: "dot", style: L.height ? null : "background:var(--fg3)" }),
    h("span", { class: "mono dim2" }, (D.cfg.network || "dero") + " · block " + (L.height || "—") + " · ",
      h("span", { "data-block-age": 1 }, "—"), " ago" + (tail ? " · " + tail : "")));

  // ---- PORTFOLIO ----
  router.on("portfolio", async (el) => {
    if (!V.wallet.connected) return render(el, h("h1", { class: "mt mb" }, "Portfolio"),
      connectCTA("No wallet connected", "Balances are encrypted on DERO. DMZ reads yours through your wallet and adds them up on this device; there is nowhere else for the number to come from."));
    const hide = V.app.settings.hideBalances, mask = (s) => (hide ? "•••••" : s);
    const draw = () => {
      const hold = V.app.holdings;
      if (!hold) return render(el, h("h1", { class: "mt" }, "Portfolio"), h("div", { class: "list mt" }, U.skeleton(3)), h("div", { class: "note" }, "Reading balances through your wallet…"));
      const pnl = V.app.pnl(hold.rows), n = hold.rows.length, va = D.cfg.vaultAsset || {};
      const total = h("div", { class: "big num mt8" }); L.num(total, hold.total, (v) => mask(v.toFixed(3)) + " DERO");
      // one hairline row per holding: name and value on the first line, the working out on the second, and every
      // value printed to the same decimals so that the column reads as a column
      const hrow = (av, name, sub, val, right, go) => h(go ? "button" : "div", { class: "feed-item", type: go ? "button" : null, onclick: go },
        av, h("div", { class: "body" },
          h("div", { class: "between" }, h("b", { class: "ellipsis" }, name), h("b", { class: "num" }, val)),
          h("div", { class: "between" }, h("span", { class: "meta" }, sub), right || null)));
      render(el,
        h("h1", { class: "mt" }, "Portfolio"),
        // Level 1 — the instrument reading. There is no price source for DERO, so there is no fiat line.
        h("div", { class: "hero cut mt8" },
          h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Total private balance"),
            h("span", { class: "row tight" }, h("span", { class: "label" }, "24h"), changePill(hold.change24))),
          total,
          h("div", { class: "sub mt8" }, n + " token" + (n === 1 ? "" : "s") + " · " + mask(A.fmtDero(hold.dero, 2)) + " DERO free"),
          netLine("added up on this device")),
        // Level 3 — accounting. A label above a number, hairlines between, no card.
        h("div", { class: "stats3 mt" },
          h("div", { class: "stat" }, h("div", { class: "l" }, "Realised"), h("div", { class: "v num " + (pnl.realised >= 0n ? "up" : "down") }, mask(A.fmtDero(pnl.realised, 2)))),
          h("div", { class: "stat" }, h("div", { class: "l" }, "Unrealised"), h("div", { class: "v num " + (pnl.unrealised >= 0n ? "up" : "down") }, mask(A.fmtDero(pnl.unrealised, 2)))),
          h("div", { class: "stat" }, h("div", { class: "l" }, "Trades"), h("div", { class: "v num" }, pnl.count))),
        U.sectionHead("wallet", "Holdings", "Valued at what the curve or pool would pay for them right now"),
        h("div", { class: "kv", style: "padding:12px 12px 8px" }, h("span", { class: "label" }, "Asset"), h("span", { class: "label" }, "Value · DERO")),
        h("div", { class: "list" },
          hrow(avatar({ ticker: "DERO", icon: "", scid: "dero" }, "sm"), "DERO", "Native asset · " + mask(A.fmtDero(hold.dero, 5)), mask(A.fmtDero(hold.dero, 3)), null, null),
          hold.rows.map((r) => {
            const b = pnl.byAsset[r.s.asset];
            return hrow(avatar(r.s, "sm"), r.s.ticker,
              [mask(A.fmtUnits(r.units, r.s.dec, 4)),
                b && b.avg ? " · avg " + S.fmtPriceSub(b.avg) : null,
                b && b.avg ? h("span", { class: b.unreal >= 0n ? "up" : "down" }, " · " + (b.unreal >= 0n ? "+" : "") + mask(A.fmtDero(b.unreal, 3))) : null],
              mask(A.fmtDero(r.value, 3)), changePill(r.s.m.c24h), () => router.go("token", r.s.scid));
          })),
        !n ? U.emptyState("wallet", "No tokens held", "This wallet holds no listed DMZ token, so the table has only your DERO. Buying one on any market adds it here.", button("Find a market", "small primary", () => router.go("markets"))) : null,
        U.sectionHead("trend", "Holdings value", "What these holdings were worth, from each market's own block history"),
        V.chart.series(null, V.app.equityPoints(hold.rows), { height: 150, empty: "Trade once and the curve starts here", wide: window.innerWidth >= 960 }),
        D.cfg.vault ? h("button", { class: "card row mt", type: "button", onclick: () => router.go("wrap") },
          icon("gem", "svgi"),
          h("div", { class: "grow" }, h("b", null, "Wrapped " + (va.sym || "assets")),
            h("div", { class: "sub" }, "Deposit or withdraw " + (va.sym || "the wrapped coin") + " on " + (va.chain || "its own chain") + ". A k-of-n federation holds the reserve one for one, so it is not trustless.")),
          icon("chevron", "svgi dim2")) : null,
        h("div", { class: "between mt" }, h("span", { class: "note" }, "Cost basis from " + pnl.count + " trade" + (pnl.count === 1 ? "" : "s") + ": the ones made on this device" + (pnl.chainCount ? " plus " + pnl.chainCount + " this address published from elsewhere" : "") + ". Nothing is uploaded."),
          button("Refresh", "small", () => V.app.loadHoldings(true))));
    };
    draw(); if (!V.app.holdings) V.app.loadHoldings(); D.onChange(() => router.current && router.current.name === "portfolio" && draw());
  });

  // ---- PROFILE ----
  router.on("profile", async (el, param) => {
    const addr = param ? V.canonAddress(param) || null : me();
    if (param && !addr) return render(el, h("div", { class: "errbox mt" }, "That is not a valid DERO address."));
    if (!addr) return render(el, h("h1", { class: "mt mb" }, "Profile"),
      connectCTA("No wallet connected", "Your address is the only identity DMZ has. Nothing about you is visible to anyone else until you publish it to the social contract."));
    if (!D.profiles.size && D.cfg.social) await D.loadSocial(me()).catch(() => {});
    const draw = () => {
      const p = D.profile(addr), mine = addr === me(), trades = D.tradesOf(addr);
      const vol = trades.reduce((a, t) => a + (t.side === 1 ? t.in : t.out), 0n);
      const stat = (l, v) => h("div", { class: "stat" }, h("div", { class: "l" }, l), h("div", { class: "v num" }, v));
      const local = D.localTrades().length, watched = S.watch.all().size;
      render(el,
        // Level 1 — the identity. Everything on this surface is on chain under this address.
        h("div", { class: "hero cut mt" },
          h("div", { class: "eyebrow" }, "Public identity"),
          h("div", { class: "row mt8" }, avatar(addr, "big"),
            h("div", { class: "grow" }, h("h1", { class: "ellipsis" }, D.displayName(addr)),
              h("button", { class: "mono dim ellipsis", type: "button", style: "display:block;max-width:100%", onclick: () => { navigator.clipboard && navigator.clipboard.writeText(addr).then(() => U.toast("Address copied", "ok")); } }, V.shortAddr(addr))),
            mine ? button("Edit", "small", () => V.forms.profile(p)) : H.followBtn(addr, false)),
          p.bio ? h("div", { class: "sub mt8" }, p.bio) : null),

        U.sectionHead("feed", "Public", "Written to the social contract by this address · anyone can read it"),
        h("div", { class: "stats4 mt8" }, stat("Followers", D.followers.get(addr) || 0), stat("Following", D.followingOf(addr).size), stat("Published trades", trades.length), stat("Published vol", A.fmtDero(vol, 1))),
        h("div", { class: "note mt8" }, "Name, bio and avatar are whatever this address published, and any address can publish any name. A trade counts here only because its owner switched “Post this trade” on."),

        U.sectionHead("lock", "Pseudonymous", "The address is the identity · that is not the same as anonymous"),
        monoKv("Address", V.shortAddr(addr)),
        kv("Ordinary swap", "ring 2 · payer public"),
        kv("Private mode", "ring 16 · pays a named address"),
        kv("Balances", "encrypted on chain"),
        h("div", { class: "note mt8" }, "An ordinary swap is a ring-size-2 contract call, so anyone can work out which address paid it, not only the contract. Private mode signs with a ring of 16 and pays a named address instead: the payer is one of sixteen, the receiving address is public."),

        mine ? [
          U.sectionHead("shield", "Local to this device", "Held in this browser · never uploaded, never published"),
          U.toggle("Hide balances", V.app.settings.hideBalances, (on) => V.app.saveSettings({ hideBalances: on }), "Masks the balances and P&L on the Portfolio screen, on this device only."),
          kv("Trade ledger", local + " trade" + (local === 1 ? "" : "s") + " kept for cost basis"),
          kv("Watchlist", watched + " market" + (watched === 1 ? "" : "s")),
          h("div", { class: "between mt" }, h("span", { class: "note" }, "Node endpoint, privacy, security and stored data"), button("Settings", "small", () => router.go("settings"))),
        ] : null,

        U.sectionHead("feed", "Published trades", mine ? "The trades you chose to post" : "The trades this address chose to post"),
        trades.length ? h("div", { class: "list mt8" }, trades.slice(0, 50).map((t) => H.feedItem(t, false, { noFollow: true })))
          : U.emptyState("feed", "Nothing published", mine ? "You have not posted a trade. “Post this trade” in the trade sheet writes one here." : "This address has posted no trades. An ordinary swap is never listed unless its owner asks for it.", mine ? button("Trade", "small primary", () => router.go("trade")) : null));
    };
    draw(); D.onChange(() => router.current && router.current.name === "profile" && draw());
  });

  // ---- SETTINGS ----
  router.on("settings", (el) => {
    const st = V.app.settings;
    const daemon = h("input", { value: st.daemon || "", placeholder: D.cfg.daemon + " (from wallet when connected)", autocomplete: "off", spellcheck: "false" });
    const commit = (document.querySelector('meta[name="veil-commit"]') || {}).content || "—";
    const can = (ok, t) => h("div", { class: "row tight", style: "align-items:flex-start" }, icon(ok ? "check" : "x", "svgi " + (ok ? "up" : "down")), h("span", { class: "sub" }, t));
    const u = V.endpointURL(st.daemon);
    render(el,
      h("h1", { class: "mt" }, "Settings"),

      U.sectionHead("bolt", "Node", "Every price, trade and profile on screen was read from here"),
      h("div", { class: "stack mt8" },
        h("div", { class: "field" }, "Node endpoint override (host:port on this device, or wss://host:port)", daemon),
        u && !u.loopback && !V.app.endpointConfirmed(u.url) ? h("div", { class: "warnbox" }, "The stored node " + V.app.hostOf(u.url) + " is not used this session until you confirm it: a remote node decides what this app sees. ", button("Use it this session", "small", () => { V.app.confirmEndpoint(u.url); V.app.connectNode(); router.dispatch(); })) : null,
        button("Save endpoint", "primary", () => { const v = daemon.value.trim(); const n = V.endpointURL(v); if (v && !n) return U.toast("Use host:port for a node on this device, or wss://host:port for a remote one", "err", 6000); V.app.saveSettings({ daemon: v }); if (n && !n.loopback) V.app.confirmEndpoint(n.url); V.app.connectNode(); U.toast("Saved", "ok"); router.dispatch(); }),
        U.toggle("Use indexer read cache", !!st.indexer, (on) => V.app.saveSettings({ indexer: on }), "Optional Gnomon cache. The app always falls back to reading the node.")),
      monoKv("Network", D.cfg.network),
      kv("Block", h("span", { class: "mono" }, String(L.height || "—"), " · ", h("span", { "data-block-age": 1 }, "—"), " ago")),
      monoKv("Endpoint", V.node.endpoint || (V.wallet.connected ? "via wallet" : "—")),
      monoKv("Wallet", V.wallet.connected ? V.shortAddr(V.wallet.address) : "not connected"),

      U.sectionHead("shield", "Security & privacy", "What this build can and cannot do"),
      U.toggle("Load token and profile images", !!st.images, (on) => { V.app.saveSettings({ images: on }); D.changed(); }, "Images come from the URL each creator set. Loading one tells that host your IP address. Sent without a referrer; off means generated avatars only."),
      h("div", { class: "stack mt8", style: "gap:7px" },
        can(true, "Runs entirely in your browser from the DERO chain. No server, no analytics, no cookies."),
        can(true, "Reads go to your node. Transactions are built here and signed only inside your wallet after you approve them there."),
        can(true, "Every listed contract's code is checked against the shipped templates before it is shown or traded."),
        can(true, "A Content Security Policy blocks any script that is not part of this build and any connection except your node, your wallet and image hosts."),
        can(true, "Trades never accept more than 10% slippage; large price impact needs an explicit acknowledgement."),
        can(false, "Cannot see your keys, seed or balances of other people. Cannot send anything without your wallet's confirmation."),
        can(false, "There is no operator once this is deployed: no support desk, no review, no refund.")),
      monoKv("Build", "TELA commit " + commit),
      kv("Fee", Number(D.cfg.feeBps) / 100 + "% per trade to the protocol treasury"),
      monoKv("Factory", V.shortAddr(D.cfg.factory)),
      monoKv("Social", V.shortAddr(D.cfg.social)),

      U.sectionHead("eye", "What publishing reveals", "Read this before switching “Post this trade” on"),
      h("div", { class: "note mt8" }, "Publishing a trade reveals your address and that trade to everyone; it never reveals balances. Contract calls that pay you need ringsize 2, and anyone can compute which address paid a ringsize-2 call, not only the contract. Private mode in the trade sheet sends at ring size 16 and pays a named address instead: the payer is one of 16 accounts, the receiving address is public. Charts are exact at block resolution: the chain records the closing reserves of every active block, never individual unpublished trades."),

      U.sectionHead("wallet", "This device", "Kept in this browser · nothing here is uploaded"),
      h("div", { class: "stack mt8" },
        U.toggle("Reduce motion", !!st.reduceMotion, (on) => { V.app.saveSettings({ reduceMotion: on }); document.documentElement.classList.toggle("reduce", on); }, "Turns off rolling numbers, flashes and the tape."),
        button("Clear local trade history, watchlist and cache", "", () => { for (const k of Object.keys(localStorage)) if (k.startsWith("veil.") && k !== "veil.settings") localStorage.removeItem(k); U.toast("Cleared", "ok"); }),
        button("Show the intro again", "ghost", () => V.app.intro())));
  });

  router.on("nft", (el, scid) => V.nft.page(el, scid));
  router.on("token", (el, scid) => V.token.page(el, scid));
})();
