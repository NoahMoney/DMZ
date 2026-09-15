// DMZ screens.js — Portfolio, Profile, Settings (with the security & privacy panel).
// Portfolio answers one question for a Monero or Zcash holder: what do I hold, in the asset I actually care
// about. So the wrapped assets lead — present or zero — DERO is named as gas rather than as a market, and token
// holdings are grouped by the quote they trade against, subtotalled inside each quote and never across them.
// There is no rate between XMR, ZEC and DERO here and there must never be an oracle, so a cross-currency total
// would be an invented number: where one used to be printed there is now a sentence saying why there is not.
// Profile separates what this address published from what the address itself gives away from what never leaves
// this browser. Settings is a technical panel: labelled sections, hairline rows, mono for machine strings.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, H = V.home, { h, render, avatar, changePill, router, button, kv, icon } = U;
  const Z = 0n;
  const me = () => V.wallet.canon;
  const connectCTA = (title, why) => U.emptyState("wallet", title, why, button("Connect wallet", "primary", () => V.app.connectWallet()));
  const monoKv = (k, v) => kv(k, h("span", { class: "mono" }, v));
  // network state: a square and precise words. Never a badge, never a glow.
  const netLine = (tail) => h("div", { class: "row tight mt8" },
    h("i", { class: "dot", style: L.height ? null : "background:var(--fg3)" }),
    h("span", { class: "mono dim2" }, (D.cfg.network || "dero") + " · block " + (L.height || "—") + " · ",
      h("span", { "data-block-age": 1 }, "—"), " ago" + (tail ? " · " + tail : "")));
  // network grammar (docs/DESIGN_SYSTEM.md §Networks): a two-letter mono label with a hairline leading edge in
  // the origin chain's tint. The chain is read from the deployment record, never guessed from the symbol alone.
  const netOf = (a) => {
    const s = (String(a.sym || "") + " " + String(a.chain || "")).toUpperCase();
    if (/XMR|MONERO/.test(s)) return ["XM", "net xmr"];
    if (/ZEC|ZCASH/.test(s)) return ["ZC", "net zec"];
    return [(String(a.sym || "").toUpperCase().replace(/[^A-Z0-9]/g, "") + "??").slice(0, 2), "net"];
  };
  // A.fmtUnits trims trailing zeros, which is right in prose and wrong in a column: a balance column only reads
  // as a column when every row has the same shape, and a Monero holder with nothing wrapped yet must see
  // "0.00000" — an answer — rather than "0", which reads like a placeholder.
  const fixed = (v, dec, frac) => {
    const s = A.fmtUnits(v, dec, frac), i = s.indexOf(".");
    return frac <= 0 ? s : i < 0 ? s + "." + "0".repeat(frac) : s + "0".repeat(frac - (s.length - i - 1));
  };
  const symOf = (v) => U.clean(v.sym || v.name || "wrapped", 10);
  const decOf = (v) => (Number.isInteger(v.dec) ? v.dec : 5);

  // ---- PORTFOLIO ----
  router.on("portfolio", async (el) => {
    const hide = V.app.settings.hideBalances, mask = (s) => (hide ? "•••••" : s);
    // A wrapped asset is not a launch, so V.app.holdings never covers it: the vault's own balance is read here,
    // one wallet call per vault, and kept for a minute like the rest of the holdings read.
    const wb = new Map(); let wbAt = 0, wbBusy = false;
    const loadWrapped = async (force) => {
      if (wbBusy || !V.wallet.connected || (!force && wbAt && Date.now() - wbAt < 60000)) return;
      wbBusy = true;
      try {
        for (const v of D.allVaults()) { const b = await V.wallet.getBalance(v.scid, true).catch(() => null); wb.set(v.scid, b ? V.toBig(b.balance) : null); }
        wbAt = Date.now();
      } finally { wbBusy = false; }
      if (router.current && router.current.name === "portfolio") draw();
    };

    // ---- the wrapped assets, which is what an XMR or ZEC holder came here for ----
    // Every vault this build ships is listed whether the balance is zero or not, and before a wallet is even
    // connected: a zero line says "this is where your Monero goes and there is none here yet", a missing line
    // says "unsupported", and only one of those is true.
    // a superseded vault is listed only while the wallet still holds its units: it is not where new Monero goes
    const vaults = D.allVaults().filter((v) => !v.legacy || (wb.get(v.scid) || Z) > Z);
    const wrapLine = (v) => {
      const [code, cls] = netOf(v), sym = symOf(v), dec = decOf(v);
      const bal = !V.wallet.connected ? null : wb.has(v.scid) ? wb.get(v.scid) : undefined;
      return h("div", { class: "between mt", style: "align-items:flex-end;gap:14px" },
        h("div", { class: "grow" },
          h("span", { class: cls }, code + " · w" + sym),
          h("div", { class: "note", style: "margin-top:7px" },
            "Wrapped " + U.clean(v.name || sym, 40) + " — a claim on " + D.chainName(v) +
            ", held one for one by a k-of-n federation. Backed, not trustless."),
          v.chain ? h("div", { class: "note tiny", style: "margin-top:5px" }, U.clean(v.chain, 100)) : null,
          h("div", { class: "row tight", style: "margin-top:8px" },
            button("Deposit / withdraw", "small", () => router.go("move", cls.slice(4))))),
        h("div", { style: "text-align:right;flex:none" },
          h("div", { class: vaults.length > 1 ? "display num" : "big num" },
            bal === undefined ? "…" : bal === null ? "—" : mask(fixed(bal, dec, Math.min(dec, 5)))),
          h("div", { class: "label", style: "margin-top:6px" }, "w" + sym)));
    };
    // With no vault in the build (mainnet's), the Level 1 reading says so, and why, instead of "0 chains"
    const wrapHero = () => h("div", { class: "hero cut mt8" },
      h("div", { class: "between" }, h("span", { class: "eyebrow" }, vaults.length ? "Wrapped assets you hold" : "Wrapped assets"),
        vaults.length ? h("span", { class: "label" }, vaults.length + " chain" + (vaults.length === 1 ? "" : "s")) : null),
      vaults.length ? vaults.map(wrapLine)
        : h("div", { class: "mt8" }, h("b", null, V.notice.NOWRAP), h("div", { class: "note mt8" }, V.notice.NOWRAP_WHY)),
      netLine(V.wallet.connected ? "read through your wallet on this device" : "no wallet connected"));
    const wrapNote = () => h("div", { class: "note mt8" }, (vaults.length ? "One line per asset, never a combined figure: wrapped XMR and wrapped ZEC are different coins and DMZ has no price oracle to convert between them. " : "") +
      (V.wallet.connected ? "Each balance is decrypted by your wallet on this device." : "Balances on DERO are encrypted, so only your own wallet can read them."));

    if (!V.wallet.connected) return render(el, h("h1", { class: "mt" }, "Portfolio"), wrapHero(), wrapNote(),
      connectCTA("No wallet connected", "Connect your wallet and this screen reads the balances above, the DERO that pays your gas, and each token you hold — on this device, through your wallet. Nothing is uploaded and there is nowhere else for a number here to come from."));

    const draw = () => {
      const hold = V.app.holdings;
      if (!hold) return render(el, h("h1", { class: "mt" }, "Portfolio"), wrapHero(), wrapNote(), h("div", { class: "list mt" }, U.skeleton(3)), h("div", { class: "note" }, "Reading balances through your wallet…"));
      const pnl = V.app.pnl(hold.rows);
      // one hairline row per holding: name and value on the first line, the working out on the second, and every
      // value printed to the same decimals so that the column reads as a column
      const hrow = (av, name, sub, val, right, go) => h(go ? "button" : "div", { class: "feed-item", type: go ? "button" : null, onclick: go },
        av, h("div", { class: "body" },
          h("div", { class: "between" }, h("b", { class: "ellipsis" }, name), h("b", { class: "num" }, val)),
          h("div", { class: "between" }, h("span", { class: "meta" }, sub), right || null)));

      // ---- group holdings and P&L by the quote each market trades against ----
      // A quote is a currency. Two quotes are two currencies, and DMZ has no oracle, so a number from one is
      // never added to, averaged with, or ranked against a number from the other. Subtotals stop at the group.
      const marketOf = (asset) => { const r = hold.rows.find((x) => x.s.asset === asset); return r ? r.s : D.list().find((x) => x.asset === asset) || null; };
      const groups = new Map();
      const groupFor = (s) => {
        const k = (s && s.quote) || "dero";
        let g = groups.get(k);
        // q is the decimal count every figure in this block prints to, so the value column adds up to the
        // subtotal printed under it; a 12-decimal quote is still read at five rather than as a wall of digits.
        if (!g) { const dec = Number(D.qdec(s)); groups.set(k, (g = { key: k, quote: (s && s.quote) || null, sym: D.qsym(s), dec, q: Math.min(dec, 5), rows: [], value: Z, realised: Z, unreal: Z, trades: 0 })); }
        return g;
      };
      for (const r of hold.rows) { const g = groupFor(r.s); g.rows.push(r); g.value += r.value; }
      let unplaced = 0; // traded assets whose market is no longer listed: their quote is unknown, so they belong to no group
      for (const [asset, b] of Object.entries(pnl.byAsset)) { // a closed position still has realised P&L and still belongs to one quote
        const s = marketOf(asset); if (!s) { unplaced++; continue; }
        const g = groupFor(s); g.realised += b.realised; g.unreal += b.unreal || Z; g.trades += b.n;
      }
      const vidx = new Map(vaults.map((v, i) => [v.scid, i]));
      const rank = (g) => (g.quote ? (vidx.has(g.quote) ? vidx.get(g.quote) : 900) : 1000); // wrapped quotes first, DERO last: gas ranks below the thing you came to trade
      const gs = [...groups.values()].sort((a, b) => rank(a) - rank(b) || a.sym.localeCompare(b.sym));

      // equityPoints now values a point in its own market's quote (A.price takes the quote decimals), so the
      // correction factor that used to live here is gone. Leaving it would double-correct.
      const curve = (g) => V.app.equityPoints(g.rows);
      // zero is not a gain: it takes no sign and no direction colour. Only a real move is coloured.
      const sign = (g, v) => (v > Z ? "+" : "") + fixed(v, g.dec, g.q);
      const dir = (v) => (v > Z ? " up" : v < Z ? " down" : "");

      const block = (g) => [
        U.sectionHead("wallet", "Priced in " + g.sym, g.rows.length + " token" + (g.rows.length === 1 ? "" : "s") +
          " trading against " + g.sym + " · every number below is " + g.sym + " and is only ever compared with other " + g.sym +
          (g.quote ? ", and is bought and sold with the wrapped " + g.sym + " above" : "")),
        h("div", { class: "kv", style: "padding:12px 12px 8px" }, h("span", { class: "label" }, "Asset"), h("span", { class: "label" }, "Value · " + g.sym)),
        h("div", { class: "list" }, g.rows.slice().sort((a, b) => (a.value < b.value ? 1 : a.value > b.value ? -1 : 0)).map((r) => {
          const b = pnl.byAsset[r.s.asset];
          // avg cost in the quote's own decimals. V.app.pnl still accumulates cost in atomic units, so the
          // division belongs here where the group's quote is known.
          const avg = b && b.units > Z ? (Number(b.cost) / 10 ** g.dec) / (Number(b.units) / 10 ** Number(r.s.dec)) : null;
          return hrow(avatar(r.s, "sm"), r.s.ticker,
            [mask(A.fmtUnits(r.units, r.s.dec, 4)) + " " + r.s.ticker,
              avg ? " · avg " + S.fmtPriceSub(avg) : null,
              b && avg ? h("span", { class: "num" + dir(b.unreal) }, " · " + mask(sign(g, b.unreal)) + " " + g.sym) : null],
            mask(fixed(r.value, g.dec, g.q)), changePill(r.s.m.c24h), () => router.go("token", r.s.scid));
        })),
        // Accounting for this quote and no other. Hairline rows rather than the old three-up tile strip: the
        // strip is a bordered box around data (against §Surfaces Level 3) and a third of a 390px phone cannot
        // hold "+0.0000 wXMR" without truncating the number, which is the one thing a balance may never do.
        kv("Subtotal · " + g.sym + " only", mask(fixed(g.value, g.dec, g.q)) + " " + g.sym, "num"),
        kv("Realised", mask(sign(g, g.realised)) + " " + g.sym, "num" + dir(g.realised)),
        kv("Unrealised", mask(sign(g, g.unreal)) + " " + g.sym, "num" + dir(g.unreal)),
        kv("Trades in this quote", String(g.trades), "num"),
        U.sectionHead("trend", "Value over time · " + g.sym, "Holdings value in " + g.sym + ", from each market's own block history. Never merged with another quote's curve — there is no rate to merge them with"),
        V.chart.series(null, curve(g), { height: 150, empty: "Trade once and the curve starts here", wide: window.innerWidth >= 960 }),
      ];

      render(el,
        h("h1", { class: "mt" }, "Portfolio"),
        // Level 1 — the instrument reading, and it is the wrapped assets. There is no rate between them, so
        // there is no single total here and no fiat line: one asset, one number, its own row.
        wrapHero(), wrapNote(),

        // DERO is the rail, not the destination, and saying so is the whole point of this section.
        U.sectionHead("bolt", "DERO · gas", "What pays the network fee on every transaction here — not one of the assets you came to trade"),
        // a balance the wallet did not give is unreadable, never zero (it was read as 0 on any failed call)
        kv("Free balance", hold.dero === null ? "unreadable: the wallet did not answer" : mask(fixed(hold.dero, 5, 5)) + " DERO", "num"),
        h("div", { class: "note mt8" }, vaults.length ? "Keep a little DERO even if you only trade XMR or ZEC markets: a buy quoted in wrapped XMR still pays its DERO network fee, and a wallet with none cannot send anything."
          : "Every transaction here pays its network fee in DERO, and a wallet with none cannot send anything."),

        // Holdings, one block per quote. Never a figure across two of them.
        gs.length ? gs.map(block) : [
          U.sectionHead("wallet", "Tokens", "Grouped by the asset each market is priced in"),
          U.emptyState("wallet", "No tokens held", "This wallet holds no listed DMZ token. Buy one on any market and it appears here, in a group named after the asset that market is priced in" + (vaults.length ? " — XMR markets with XMR, DERO markets with DERO." : "."), button("Find a market", "small primary", () => router.go("markets"))),
          U.sectionHead("trend", "Value over time", "Holdings value is drawn here from each market's own block history, one curve per quote, as soon as you hold something"),
          V.chart.series(null, [], { height: 150, empty: "Trade once and the curve starts here", wide: window.innerWidth >= 960 }),
        ],
        gs.length > 1 ? h("div", { class: "note mt" }, "These " + gs.length + " subtotals are deliberately not added together. " +
          gs.map((g) => g.sym).join(", ").replace(/, ([^,]*)$/, " and $1") + " are different assets and DMZ has no price oracle and must never have one, so a single portfolio number would be a rate this app invented.") : null,

        // silently dropping a figure is the same sin as inventing one, so say when one was dropped and why
        unplaced ? h("div", { class: "note mt" }, unplaced + " traded asset" + (unplaced === 1 ? " is" : "s are") + " left out of the groups above: " +
          (unplaced === 1 ? "its market is" : "their markets are") + " no longer listed, so the app cannot tell which asset " +
          (unplaced === 1 ? "it was" : "they were") + " priced in, and putting the figure in the wrong quote would be worse than omitting it.") : null,
        h("div", { class: "note mt" }, "Balances read through your wallet ", h("span", { "data-ts": Math.floor(hold.at / 1000) }, U.timeAgo(Math.floor(hold.at / 1000)) + " ago"),
          " and kept for a minute" + (vaults.length && wbAt ? "; wrapped balances read " + U.timeAgo(Math.floor(wbAt / 1000)) + " ago" : "") + ". Refresh reads them again."),
        hold.unread ? h("div", { class: "note mt" }, hold.unread + " token balance" + (hold.unread === 1 ? "" : "s") + " could not be read from your wallet, so " +
          (hold.unread === 1 ? "that token is" : "those tokens are") + " left out above rather than shown as zero. Refresh to ask again.") : null,
        h("div", { class: "between mt" }, h("span", { class: "note" }, "Cost basis from " + pnl.count + " trade" + (pnl.count === 1 ? "" : "s") + ": the ones made on this device" + (pnl.chainCount ? " plus " + pnl.chainCount + " this address published from elsewhere" : "") + ", kept in this browser. Nothing is uploaded."),
          button("Refresh", "small", () => { V.app.loadHoldings(true); loadWrapped(true); })));
    };
    draw(); if (!V.app.holdings) V.app.loadHoldings(); loadWrapped(); D.onChange(() => router.current && router.current.name === "portfolio" && draw());
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
      // Published volume per quote, never one sum (MISS-04): a trade in wrapped XMR and a trade in DERO are two
      // currencies with no rate between them, and one figure scaled at DERO's decimals was a number of nothing.
      const byQ = new Map();
      for (const t of trades) { const s = t.asset, k = D.qsym(s), g = byQ.get(k) || { v: Z, d: D.qdec(s) }; g.v += t.side === 1 ? t.in : t.out; byQ.set(k, g); }
      const vol = [...byQ].sort((a, b) => a[0].localeCompare(b[0])).map(([k, g]) => h("div", null, A.fmtUnits(g.v, g.d, 1) + " " + k));
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
        h("div", { class: "stats4 mt8" }, stat("Followers", D.followers.get(addr) || 0), stat("Following", D.followingOf(addr).size), stat("Published trades", trades.length), stat("Published volume", vol.length ? vol : "0")),
        h("div", { class: "note mt8" }, "Name, bio and avatar are whatever this address published, and any address can publish any name. A trade counts here only because its owner switched “Post this trade” on."),

        U.sectionHead("lock", "Pseudonymous", "The address is the identity · that is not the same as anonymous"),
        monoKv("Address", V.shortAddr(addr)),
        kv("Ordinary swap", "ring 2 · payer public"),
        kv("Private mode", "ring 16 · pays a named address"),
        kv("Balances", "encrypted on chain"),
        h("div", { class: "note mt8" }, "An ordinary swap is a ring-size-2 contract call, so anyone can work out which address paid it, not only the contract. Private mode signs with a ring of 16 and pays a named address instead. The ring hides the payer from the contract, but a public bit in every DERO proof says which half of the ring the payer is in, so to anyone reading the chain the payer is one of 8; the receiving address is public."),

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
        button("Save endpoint", "primary", () => { const v = daemon.value.trim(); const n = V.endpointURL(v); if (v && !n) return U.toast("Use host:port for a node on this device, or wss://host:port for a remote one", "err", 6000); V.app.saveSettings({ daemon: v }); if (n && !n.loopback) V.app.confirmEndpoint(n.url); V.app.connectNode(); U.toast("Saved", "ok"); router.dispatch(); })),
      // An "indexer read cache" toggle stood here: the setting was written and never read (UX-10), an option that did nothing.
      monoKv("Network", D.cfg.network),
      monoKv("Node reports", (V.app.net && V.app.net.node) || "—"),
      kv("Block", h("span", { class: "mono" }, String(L.height || "—"), " · ", h("span", { "data-block-age": 1 }, "—"), " ago")),
      monoKv("Endpoint", V.node.endpoint || (V.wallet.connected ? "via wallet" : "—")),
      monoKv("Wallet", V.wallet.connected ? V.shortAddr(V.wallet.address) : "not connected"),

      U.sectionHead("shield", "Security & privacy", "What this build can and cannot do"),
      // An image toggle stood here. Images came from hosts creators chose, and the mainnet rule is that the app makes
      // no request but to the user's own node and wallet: the plates are the only avatars, and the CSP has no https: image source.
      h("div", { class: "stack mt8", style: "gap:7px" },
        can(true, "Runs entirely in your browser from the DERO chain. No server, no analytics, no cookies."),
        can(true, "Reads go to your node. Transactions are built here and signed only inside your wallet after you approve them there."),
        can(true, "Before a contract is shown or traded, its code is hashed and compared with the shipped templates, and its treasury with the one this build ships (the factory's, when it ships none). The code, the registry and the treasury all come from the node above — the app has no second source, so a node you do not run can misreport all three."),
        // UX-09: the policy's connect-src admits any wss:// host, so the limit to one node is this app's code, not the policy
        can(true, "A Content Security Policy blocks any script that is not part of this build. It lets the page connect to this device and to any wss:// host; this app's own code connects only to the node you set and your wallet on this device, and loads no image, font or script from anywhere else. A link a creator set opens outside DMZ, and only when you click it."),
        can(true, "Trades never accept more than 10% slippage; large price impact needs an explicit acknowledgement."),
        can(true, "Nothing is signed unless this build's network, your node's and your wallet's node's are one and the same."),
        can(false, "Cannot see your keys, seed or balances of other people. Cannot send anything without your wallet's confirmation."),
        // SEC-K-13: "there is no operator" contradicted the message from the dev, which names the keys that survive
        can(false, "No support desk, no review, no refund. The keys that survive deployment are listed in the message from the dev.")),
      monoKv("Build", "TELA commit " + commit),
      kv("Fee", Number(D.cfg.feeBps) / 100 + "% per trade; on a curve all of it is the treasury's, on an AMM " + Number(D.cfg.pfeeBps) / 100 + "% of it and the rest stays in the pool"),
      monoKv("Factory", V.shortAddr(D.cfg.factory)),
      monoKv("Social", V.shortAddr(D.cfg.social)),

      U.sectionHead("eye", "What publishing reveals", "Read this before switching “Post this trade” on"),
      h("div", { class: "note mt8" }, "Publishing a trade reveals your address and that trade to everyone; it never reveals balances. Contract calls that pay you need ringsize 2, and anyone can compute which address paid a ringsize-2 call, not only the contract. Private mode in the trade sheet sends at ring size 16 and pays a named address instead: the contract never learns the payer, but a public bit in every DERO proof says which half of the ring the payer is in, so to an observer the payer is one of 8 of those 16 accounts; the receiving address is public. Charts are exact at block resolution: the chain records the closing reserves of every active block, and the difference between two of them is the size of the trade that caused it — so every trade is pseudonymous whether or not you post it, and an unpublished trade is unpublished in name only. Posting adds your address to the record; it does not add the amount, which is already there."),

      U.sectionHead("wallet", "This device", "Kept in this browser · nothing here is uploaded"),
      h("div", { class: "stack mt8" },
        U.toggle("Reduce motion", !!st.reduceMotion, (on) => { V.app.saveSettings({ reduceMotion: on }); document.documentElement.classList.toggle("reduce", on); }, "Turns off rolling numbers, flashes and the tape."),
        button("Clear local trade history, watchlist and cache", "", () => { for (const k of Object.keys(localStorage)) if (k.startsWith("veil.") && k !== "veil.settings") localStorage.removeItem(k); U.toast("Cleared", "ok"); }),
        button("Show the intro again", "ghost", () => V.app.intro())));
  });

  router.on("nft", (el, scid) => V.nft.page(el, scid));
  router.on("nfa", (el, scid) => V.nfas.page(el, scid));
  router.on("token", (el, scid) => V.token.page(el, scid));
})();
