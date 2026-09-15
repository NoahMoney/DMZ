// DMZ token.js — the market page and the trade sheet. What a trader reads first: the pair and who it is (avatar,
// name, the links the creator published), the price with its four change windows, and the chart; then one stats
// band, the trade panel (sticky in the desktop rail, a bottom bar on a phone) with the trades beside it, and below
// the chart the published activity, block pressure, sentiment and the contract. A market with a wrapped side
// (wXMR, wZEC) is marked "wrapped · 1:1", and its prices, fees and volume are in its quote like any other.
// Regions are named and separated by hairlines rather than stacked in cards. market.js draws the regions that read
// other files' interfaces, and the side rail's tabs.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, H = V.home, M = V.market, { h, render, avatar, pill, changePill, router, button, kv, openSheet, closeSheet, toast, icon } = U;
  const T = (V.token = {});
  const me = () => V.wallet.canon;
  const Z = 0n, { head, mini, band, ask } = M;
  // the chart's settings outlive one page, so moving between markets keeps the interval a trader chose wherever
  // the next market can fill it; in memory only
  const chartState = { mode: "candles", iv: null };
  let ro = null;
  const copy = (txt, what) => { if (navigator.clipboard) navigator.clipboard.writeText(txt).then(() => toast(what + " copied", "ok"), () => toast("Clipboard blocked")); };
  // technical data: the mono voice, middle-truncated, the whole value on the clipboard. Never lime — an id is not
  // an action, and lime is reserved for what you can do.
  const cpy = (what, v) => h("button", { class: "mono dim", type: "button", title: "Copy " + what.toLowerCase(), "aria-label": "copy " + what.toLowerCase(), onclick: () => copy(v, what) }, V.shortAddr(v));
  // A region is redrawn only when what it shows has changed: each keeps the key it was last drawn from. A block
  // that moved nothing on this market repaints nothing on this page.
  const once = (el, key, draw) => { key = JSON.stringify(key, (k, v) => (typeof v === "bigint" ? v.toString() : v)); if (el._k === key) return; el._k = key; draw(); };

  T.page = async (el, scid) => {
    if (!/^[0-9a-f]{64}$/.test(String(scid || ""))) return render(el, h("div", { class: "errbox mt" }, "Not a contract id"));
    if (ro) { ro.disconnect(); ro = null; }
    let s = D.assets.get(scid);
    // a contract reached by id that the registry does not list is shown, marked, and kept out of every list
    if (!s) {
      render(el, h("div", { class: "list mt" }, U.skeleton(1)), V.chart.render(null, null, { loading: true, width: Math.min(el.clientWidth || 390, 900) }));
      // A link opened directly lands here before the node connection exists, so the first read fails with
      // "no node connection" and the page gives up — on the one route a shared link always takes. Wait for the
      // app to reach the chain instead, then read. Bounded, so a genuinely offline node still reports itself.
      for (let i = 0; i < 60 && !D.lastRefresh; i++) await new Promise((r) => setTimeout(r, 250));
      // A market found in the registry is listed; only one the registry does not carry is not. Marking every
      // deep-linked market "not listed" told visitors a registered, verified market was unvouched for — on the
      // exact route a shared link takes, which is where that warning does the most damage.
      const listed = D.assets.get(scid);
      try { s = listed || (await D.loadContract(scid)); } catch (e) { return render(el, h("div", { class: "errbox mt" }, "Cannot load " + scid + ": " + e.message)); } if (!s || s.untrusted) return render(el, h("div", { class: "errbox mt" }, "Not listed: " + (s ? s.untrusted : "unknown"))); if (!listed) s.unlisted = true; D.nameBase(s); if (s.kind === "pool" && s.assetVerified) { const l = D.assets.get(s.asset); s.dupTicker = !!l.dupTicker; s.lookalike = !!l.lookalike; } else { const k = U.tickerKey(s.ticker); s.lookalike = U.hasHomoglyph(s.ticker); s.dupTicker = !!k && D.list().some((x) => x.scid !== s.scid && !(x.kind === "pool" && x.assetVerified) && !(s.wrappedBase && x.wrappedBase && x.asset === s.asset) && U.tickerKey(x.ticker) === k); } s.impersonates = D.impersonation(s); D.nameQuote(s); S.enrich(s, D.height); }
    let tab = "trades", lastSig = null, chartKey = "", lastW = 0, rz = 0, qBal;
    const chartBox = h("div"), priceEl = h("div", { class: "big num" }), headEl = h("div", { class: "hero card mt" }), riskEl = h("div"), bandEl = h("div"), gradEl = h("div"), actEl = h("div"), pressEl = h("div"), sentEl = h("div"), techEl = h("div"), panelEl = h("div"), depEl = h("div"), bodyEl = h("div", { class: "mt" });
    const qs = () => " " + D.qsym(s);
    const drawBody = () => M.body(bodyEl, s, tab, () => T.tradeSheet(s, 1));
    const drawChart = () => {
      const w = lastW || Math.round(chartBox.clientWidth) || 360, key = S.pickInterval(s, chartState.iv), offered = S.intervals(s);
      const markers = s.trades.filter((t) => D.follows.has(t.addr) || t.addr === me()).map((t) => ({ ts: t.ts, side: t.side, label: D.displayName(t.addr) }));
      const ck = [s.sig, s.price, w, key, chartState.mode, markers.length, offered.join()].join("|");
      if (ck === chartKey) return; chartKey = ck;
      const hs = S.history(s);
      // the controls sit above the instrument they set: only the intervals this history can fill, then ALL
      render(chartBox,
        h("div", { class: "between mt tk-ctl" },
          h("div", { class: "chips", role: "group", "aria-label": "candle interval" }, [...offered, "all"].map((r) => h("button", { class: "chip " + (key === r ? "on" : ""), type: "button", "aria-pressed": String(key === r), "aria-label": r === "all" ? "all loaded history" : r + " candles", onclick: () => { chartState.iv = r; drawChart(); } }, r === "all" ? "ALL" : r))),
          U.segmented([{ v: "candles", l: "Candles" }, { v: "area", l: "Line" }], chartState.mode, (v) => { chartState.mode = v; drawChart(); })),
        V.chart.render(null, s, { mode: chartState.mode, iv: key, markers, width: w, quote: D.qsym(s) }),
        h("div", { class: "note mt8" }, "Exact at block resolution: candles come from the closing reserves the contract writes every active block. An interval is offered once the history spans " + S.MINC + " of its candles.",
          hs.truncated ? " This app holds the newest " + hs.points + " closes; the older ones are on chain." : "", markers.length ? " Triangles: trades by you and accounts you follow." : ""));
    };
    const drawHead = () => {
      const m = s.m, launch = s.kind === "launch", curve = launch && s.phase === Z, pl = M.pairOf(s), us = M.linksOf(s), promo = M.promoted(s);
      // Level 1 — the instrument: which pair this is, whose it is, and the one number the screen is about
      once(headEl, [s.sig, pl, m.c5m, m.c1h, m.c6h, m.c24h, S.watch.has(s.scid), promo, us, U.warnings(s).map((w) => w.textContent), H.badges(s).filter(Boolean).map((b) => b.textContent)], () => render(headEl,
        h("div", { class: "row" }, avatar(s, "big"), h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, h("h1", { class: "ellipsis" }, pl), M.wrapMark(s), H.star(s), U.warnings(s)), h("div", { class: "sub ellipsis" }, s.name),
          h("div", { class: "row tight mt8" }, U.verified(s), H.badges(s), promo ? M.promoPill() : null), M.links(us)),
          h("button", { class: "iconbtn", type: "button", "aria-label": "copy link to this market", title: "Copy link", onclick: () => copy(location.href.split("#")[0] + "#token/" + s.scid, "Link") }, icon("share"))),
        h("div", { class: "eyebrow mt" }, "Price · " + D.qsym(s)),
        h("div", { class: "pulse-row mt8" }, priceEl,
          // the four changes are one aligned block: it sits beside the price where there is room and drops whole
          // below it where there is not, so a single window never strands itself on its own line
          h("div", { style: "display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;flex:1 1 250px" },
            [["5m", m.c5m], ["1h", m.c1h], ["6h", m.c6h], ["24h", m.c24h]].map(([l, c]) => mini(l, c === null ? "—" : A.pct(c), c === null ? "dim2" : c >= 0 ? "up" : "down"))))));
      L.num(priceEl, s.price, S.fmtPriceSub);
      once(gradEl, [curve, m.progress, s.real], () => render(gradEl, curve ? [head("Graduation", h("b", { class: "num" }, m.progress.toFixed(1) + "%")),
        h("div", { class: "progress mt8" }, h("i", { style: "width:" + Math.min(100, m.progress) + "%" })),
        h("div", { class: "note mt8" }, A.fmtUnits(s.real, D.qdec(s), 1) + " of " + A.fmtUnits(s.G, D.qdec(s), 0) + " " + D.qsym(s) + " raised. At 100% the curve becomes a permanently locked AMM at the same price.")] : null));
      // warnings as the definitions give them (home.js H.risks), where the reader decides whether to trade
      const risks = H.risks(s);
      once(riskEl, risks.map((r) => r.t), () => render(riskEl, risks.length ? h("div", { class: "riskbox warn mt" }, icon("warn"), h("div", null, risks.map((r) => h("div", null, r.t)))) : null));
      // one band of what the market is worth; FDV only where the contract states a total supply (a launch)
      const fdv = m.mcap > 0 && isFinite(m.mcap);
      once(bandEl, [m.vol24, m.liq, m.mcap, m.age, s.tc, D.qsym(s)], () => render(bandEl, head("Market"),
        band(mini("Volume 24h", S.fmtDeroShort(m.vol24) + qs()), mini("Liquidity", S.fmtDeroShort(m.liq) + qs()), fdv ? mini("FDV", S.fmtDeroShort(m.mcap) + qs()) : null, mini("Age", S.ageText(m.age)), mini("Published trades", String(s.tc))),
        h("div", { class: "note mt8" }, "Volume is the reserve flow of every active block, posted or not." + (fdv ? " FDV is the price times the total supply the launch contract states." : ""))));
      once(actEl, [m.buys24, m.sells24, s.tc, m.lastTs], () => render(actEl, head("Published activity · 24h"),
        band(mini("Trades", String(m.buys24 + m.sells24)),
          mini("Buys / sells", h("span", null, h("span", { class: "up" }, String(m.buys24)), " / ", h("span", { class: "down" }, String(m.sells24)))),
          mini("Accounts that posted", String(m.holders)),
          mini("Last posted", m.lastTs ? h("span", { "data-ts": m.lastTs }, U.timeAgo(m.lastTs) + " ago") : "—")),
        h("div", { class: "note mt8" }, "Counted from trades whose sender chose to post them. \"Accounts that posted\" is every address that ever published a trade here, not a count of holders: balances are private on DERO. A trade nobody posted still moves the price — the reserve change between two blocks is its size — so an unpublished trade hides who, not what.")));
      const tot = m.upBlocks24 + m.downBlocks24 || 1, bp = Math.round((m.upBlocks24 / tot) * 100);
      once(pressEl, [bp, m.upBlocks24 + m.downBlocks24], () => render(pressEl,
        head("Block pressure · 24h", h("span", { class: "num tiny" }, h("span", { class: "up" }, bp + "% buy"), h("span", { class: "dim2" }, " · "), h("span", { class: "down" }, 100 - bp + "% sell"))),
        h("div", { class: "pressure mt8", role: "img", "aria-label": bp + "% of active blocks moved the price up" }, h("i", { style: "width:" + bp + "%" })),
        h("div", { class: "note mt8" }, "Reserve direction per active block, so unposted trades are counted here too.")));
      // a connected wallet holding none of a wrapped quote is pointed at the deposit; read once per page, and no
      // link while the balance is unknown or unreadable
      if (M.quoteVault(s) && V.wallet.connected && qBal === undefined) { qBal = null; V.wallet.getBalance(s.quote).then((b) => { qBal = V.toBig(b && b.balance) || Z; drawHead(); }, () => {}); }
      once(depEl, [qBal, !!M.quoteVault(s)], () => render(depEl, M.deposit(s, qBal)));
      const fee = Number(s.fee) / 100;
      once(panelEl, [pl, s.price, fee, curve, m.liq], () => render(panelEl,
        h("div", { class: "eyebrow" }, "Trade " + pl),
        h("div", { class: "mt8" }, kv("Price", S.fmtPriceSub(s.price) + qs()), kv("Trading fee", fee + "%" + (curve ? ", all to the treasury" : "")), kv("Route", curve ? "bonding curve" : "constant-product AMM"), kv("Liquidity", S.fmtDeroShort(m.liq) + qs())),
        h("div", { class: "note mt8" }, "Buy and Sell open the order: every cost, the minimum received and the network are stated before your wallet signs.")));
    };
    const draw = () => {
      s = D.assets.get(scid) || s;
      if (!el.contains(headEl)) {
        render(el, h("div", { class: "tk-grid" },
          h("div", { class: "tk-main" }, headEl, riskEl, chartBox, bandEl, gradEl, actEl, pressEl, sentEl, techEl),
          h("div", { class: "tk-side" },
            h("div", { class: "tk-panel" }, panelEl), depEl,
            h("div", { class: "sticky-actions" }, button("Buy", "buy wide", () => T.tradeSheet(s, 1)), button("Sell", "sell wide", () => T.tradeSheet(s, 2))),
            h("div", { class: "mt" }, U.segmented([{ v: "trades", l: "Trades" }, { v: "holders", l: "Publishers" }, { v: "info", l: "Info" }], tab, (v) => { tab = v; drawBody(); })),
            bodyEl, h("div", { style: "height:70px" }))));
        // the sentiment widget (V.sentiment.widget) keeps its own state, so it is placed once per page and never
        // rebuilt by a refresh
        const sw = ask(() => V.sentiment && typeof V.sentiment.widget === "function" && V.sentiment.widget(scid));
        if (sw && sw.nodeType) render(sentEl, head("Sentiment"), h("div", { class: "mt8" }, sw));
        // the technical block: the ids a person has to check for themselves
        render(techEl, head("Contract"),
          h("div", { class: "mt8" }, kv("Contract id", cpy("Contract id", s.scid)), s.kind === "launch" ? kv("Creator", cpy("Creator", s.creator)) : kv("Asset id", cpy("Asset id", s.asset)), s.treasury ? kv("Protocol treasury", cpy("Treasury", s.treasury)) : null),
          h("div", { class: "note mt8" }, "Click a value to copy it in full. A ticker is not an identity: check the contract id before you trade."));
        // Redrawn on a real width change only, at most once per 120 ms: the observer reports every frame of a
        // window drag, and each report is a size read, not a layout of the chart.
        if (typeof ResizeObserver === "function") {
          ro = new ResizeObserver((es) => {
            if (!chartBox.isConnected) { if (ro) ro.disconnect(); ro = null; return; }
            const w = Math.round(es[es.length - 1].contentRect.width); if (!w || Math.abs(w - lastW) < 8) return;
            lastW = w; if (!rz) rz = setTimeout(() => { rz = 0; drawChart(); }, 120);
          });
          ro.observe(chartBox);
        }
      }
      drawHead(); drawChart();
      if (s.sig !== lastSig) { lastSig = s.sig; drawBody(); }
    };
    draw(); D.onChange(() => router.current && router.current.name === "token" && router.current.param === scid && draw());
  };

  // ---- trade sheet ----
  // Requirement 3: every cost stands in front of the button, in the asset it is actually charged in. The trading
  // fee, the protocol's share of it, the token tax, price impact and the minimum received are all in the market's
  // own quote (D.qsym / D.qdec — never assume DERO). A fee the contract takes in the token (a sell into an AMM) is
  // stated in the token, with its value in the quote beside it, marked as an estimate. The network fee is DERO on
  // every market whatever it trades, so it is a row of its own, named as DERO. Nothing that costs money sits behind
  // a disclosure, and the Review block above the button restates the pair, side, amounts, minimum and network.
  T.tradeSheet = (s, side, pre) => {
    const launch = s.kind === "launch", buy = side === 1, curve = launch && s.phase === Z;
    const qsym = D.qsym(s), qdec = D.qdec(s), qv = s.quote ? D.allVaults().find((v) => v.scid === s.quote) : null;
    // the fee is charged on whatever goes in, except on the curve where both legs are priced in the quote
    const feeQ = buy || curve, feeSym = feeQ ? qsym : s.ticker, feeDec = feeQ ? qdec : s.dec;
    const feeTxt = (v) => A.fmtUnits(v, feeDec, 5) + " " + feeSym + (feeQ || !(s.price > 0) ? "" : " ≈ " + S.fmtDeroShort((Number(v) / 10 ** Number(s.dec)) * s.price) + " " + qsym);
    // on the curve the whole fee is the protocol's; in an AMM pfee of it is and the rest stays in the reserves
    const protoBps = curve ? 10000n : (s.pfee || Z);
    const taxTo = ["nobody", "the creator", "a buyback that locks tokens", "stakers"][Number(s.tmode || Z)] || "the contract";
    let slip = V.app.settings.slippage || 100, publish = false, ack = false, amount = Z, balance = null, q = null, priv = false;
    let gasTxt = "", gasSeq = 0, gasTimer = null;
    const destIn = h("input", { placeholder: "address that is not linked to you", class: "mono", autocomplete: "off", spellcheck: "false", "aria-label": "receive at" });
    const input = h("input", { inputmode: "decimal", placeholder: "0", "aria-label": "amount to " + (buy ? "pay" : "sell"), autocomplete: "off" });
    if (pre) input.value = pre;
    const unit = buy ? qsym : s.ticker, dec = buy ? qdec : s.dec, outUnit = buy ? s.ticker : qsym, outDec = buy ? s.dec : qdec;
    const outV = h("div", { class: "v num" }, "0"), outL = h("div", { class: "l" }, "You receive (estimated)"), rate = h("div", { class: "note mt8" }), costs = h("div", { class: "stack", style: "gap:0" }), review = h("div", { class: "stack", style: "gap:0" }), riskBox = h("div");
    const confirm = button((buy ? "Buy " : "Sell ") + s.ticker, (buy ? "buy" : "sell") + " wide mt", () => go(), { disabled: true });
    const risks = H.risks(s), net = String((D.cfg && D.cfg.network) || "unknown");
    const slipEl = h("div", { class: "chips", role: "group", "aria-label": "maximum slippage" }, [50, 100, 200, 500].map((b) => h("button", { class: "chip sm " + (slip === b ? "on" : ""), type: "button", "aria-pressed": String(slip === b), onclick: (e) => { slip = b; U.$$("button", slipEl).forEach((x) => { x.classList.remove("on"); x.setAttribute("aria-pressed", "false"); }); e.currentTarget.classList.add("on"); e.currentTarget.setAttribute("aria-pressed", "true"); V.app.saveSettings({ slippage: b }); quote(); } }, b / 100 + "%")));
    // the cost block, drawn whether or not an amount is typed: the fee, the route and the ring are facts about the
    // market and are true before anything is entered; the amounts fill in as soon as there is a quote
    const paint = () => {
      const big = q && q.impact > 1500, mid = q && q.impact > 500, dash = "—";
      render(costs,
        kv("Trading fee · " + Number(s.fee) / 100 + "%", q ? feeTxt(q.fee) : dash),
        kv("of it, protocol treasury · " + Number(protoBps) / 100 + "%", q ? feeTxt(A.bps(q.fee, protoBps)) : dash),
        q && q.tax > Z ? kv("Token tax · " + Number(s.tax) / 100 + "% to " + taxTo, A.fmtUnits(q.tax, qdec, 5) + " " + qsym) : null,
        q && q.refund > Z ? kv("Refund, the curve ends here", A.fmtUnits(q.refund, qdec, 5) + " " + qsym + (priv ? " → paid to the receive address, not to you" : "")) : null,
        kv("Price impact", q ? (q.impact / 100).toFixed(2) + "%" : dash, big ? "down" : mid ? "warn" : ""),
        V.sheets.gasRow(gasTxt, !!q),
        kv("Route", curve ? "bonding curve" : "constant-product AMM"),
        // DERO's proof carries a public bit saying which half of the ring the payer is in, at every ring size
        // (vendor/derohe cryptography/crypto/proof_generate.go: aa[0] = 0, so f[0] = w·bit), so 16 hides among 8
        kv("Ring size", priv ? "16 · the payer is one of 8 to an observer" : "2 · the payer is public"),
        h("div", { class: "note mt8" }, (curve ? "On the bonding curve the whole trading fee goes to the protocol treasury. " : "The rest of the trading fee stays in the pool, for its liquidity providers. ")
          + (priv ? "At ring size 16 the contract never learns who paid, but the address you name below receives in clear. " : "At ring size 2 the contract pays the signer, so anyone can compute which address made this call. ")
          + "Either way the contract records its reserves at the end of every block they move in, and the difference between two of them is the size of the trade: posting a trade adds your address to the record, never the amount, which is on chain already. "
          + "The network fee is DERO on every market" + (s.quote ? ", so this one needs a little DERO as well as " + qsym : "") + "; your wallet shows the exact amount before you confirm."),
        priv && V.wallet.canon && V.canonAddress(destIn.value.trim()) === V.wallet.canon ? h("div", { class: "warnbox mt8" }, "This is the connected address: the ring hides who paid, but the receiver is you, in clear.") : null,
        q && q.graduates ? h("div", { class: "warnbox mt8" }, "This buy completes the curve: the token graduates to its locked AMM.") : null);
      // what the button will do, in one place: pair, side, both amounts, the floor, and the network it signs on
      render(review,
        kv("Pair", M.pairOf(s)),
        kv("Side", buy ? "Buy " + s.ticker + ", pay " + qsym : "Sell " + s.ticker + ", receive " + qsym, buy ? "up" : "down"),
        kv("You pay", amount > Z ? A.fmtUnits(amount, dec, 6) + " " + unit : dash),
        kv("You receive, estimated", q ? A.fmtUnits(q.out, outDec, 6) + " " + outUnit : dash),
        kv("Max slippage", slipEl),
        kv("Minimum received, or it reverts", q ? A.fmtUnits(A.minOut(q.out, slip), outDec, 6) + " " + outUnit : dash),
        kv("Network", "DERO " + net + (priv ? " · ring 16, paid to the address above" : " · ring 2")));
    };
    // the node prices the exact call before the user commits, so the gas row is a number rather than a promise
    const schedGas = () => { clearTimeout(gasTimer); gasSeq++; gasTxt = ""; if (q) gasTimer = setTimeout(estGas, 320); };
    const estGas = async () => {
      const my = ++gasSeq, at = q;
      try {
        const dest = priv && V.isAddress(destIn.value.trim()) ? destIn.value.trim() : null;
        const args = dest ? [["dest", "S", dest]] : [];
        args.push(["min_out", "U", A.minOut(at.out, slip)], ["publish", "U", publish && !priv ? 1n : Z]);
        // what the call actually sends: the quote asset on a buy (DERO is the asset whose id is the zero hash),
        // the token on a sell
        const t = await V.sheets.gas({ scid: s.scid, entry: (buy ? "SwapDeroForAsset" : "SwapAssetForDero") + (dest ? "To" : ""), args, send: [{ scid: buy ? s.quote : s.asset, amount }] });
        if (my !== gasSeq || q !== at) return;
        gasTxt = t;
      } catch (e) { if (my !== gasSeq) return; gasTxt = ""; }
      if (my === gasSeq) paint();
    };
    let balFail = false;
    const quote = () => {
      amount = A.parseUnits(input.value, dec) || Z;
      q = amount > Z ? (buy ? (launch ? A.launchBuy(s, amount) : A.poolBuy(s, amount)) : (launch ? A.launchSell(s, amount) : A.poolSell(s, amount))) : null;
      const big = q && q.impact > 1500, mid = q && q.impact > 500;
      // fail closed: a connected wallet whose balance has not been read, or could not be, gets no button
      const noBal = V.wallet.connected && balance === null;
      confirm.disabled = (!!s.quote && !qv) || !q || noBal || (big && !ack) || (balance !== null && amount > balance) || (priv && !V.isAddress(destIn.value.trim())) || (priv && q.refund > Z);
      confirm.textContent = s.quote && !qv ? "Priced in an asset this app does not name" : noBal ? (balFail ? "Balance unreadable" : "Reading your " + unit + " balance…") : balance !== null && amount > balance ? "Not enough " + unit + ": " + A.fmtUnits(amount - balance, dec, 4) + " short" : priv && q.refund > Z ? "Overshoots the curve: lower the amount" : (buy ? "Buy " : "Sell ") + s.ticker;
      outV.textContent = q ? A.fmtUnits(q.out, outDec, 6) + " " + outUnit : amount > Z ? "—" : "0";
      // the price this order really executes at, next to the price the market quotes: the gap is what size costs
      const exec = q ? (buy ? Number(amount) / 10 ** Number(qdec) / (Number(q.out) / 10 ** Number(s.dec)) : Number(q.out) / 10 ** Number(qdec) / (Number(amount) / 10 ** Number(s.dec))) : 0;
      rate.textContent = q ? "1 " + s.ticker + " = " + S.fmtPriceSub(exec) + " " + qsym + " at this size · quoted " + S.fmtPriceSub(s.price) + " " + qsym
        : amount > Z ? "This amount cannot be filled: too small, above what the curve has left, or more than the pool can take."
          : "1 " + s.ticker + " ≈ " + S.fmtPriceSub(s.price) + " " + qsym;
      rate.className = "note mt8 " + (big ? "down" : mid ? "warn" : "");
      schedGas(); paint();
      render(riskBox,
        big ? h("div", { class: "riskbox danger mt" }, icon("warn"), h("div", null, h("b", null, "Price impact " + (q.impact / 100).toFixed(1) + "%"), h("div", null, "You would move the market a lot and get much less than the current price suggests. Split the order or trade less."), U.toggle("I understand, continue anyway", ack, (on) => { ack = on; quote(); }, null, "ackSw")))
          : mid ? h("div", { class: "riskbox warn mt" }, icon("warn"), h("div", null, "Price impact " + (q.impact / 100).toFixed(1) + "%: this order noticeably moves the price.")) : null,
        risks.length && !big ? h("div", { class: "riskbox warn mt8" }, icon("warn"), h("div", null, risks.map((r) => h("div", null, r.t)))) : null);
    };
    input.oninput = quote;
    const presets = h("div", { class: "chips nowrap" }, [25, 50, 75, 100].map((p) => h("button", { class: "chip sm", type: "button", onclick: () => { if (balance === null) return toast("Connect the wallet to use presets"); let v = balance * BigInt(p) / 100n; if (buy && p === 100) v = v > 20000n ? v - 20000n : Z; input.value = A.fmtUnits(v, dec, Number(dec)).replace(/,/g, ""); quote(); } }, p === 100 ? "Max" : p + "%")));
    // a private trade is never published: a public record would name the receiver
    // The record this sheet closed over is a snapshot: feed.js replaces this.assets wholesale on every refresh,
    // and no sheet re-reads it. min_out was therefore derived from reserves of unbounded age, which is what the
    // slippage setting was supposed to bound — a sheet left open across one opposing trade turned a 1% setting
    // into an 81.9% giveaway. Re-read the market and re-price the same amount here, at the moment of signing:
    // sign against the fresh quote, and when the market has moved further than the chosen slippage, re-draw the
    // sheet on the new numbers instead of signing numbers the user never saw.
    const go = async () => {
      if (!q) return;
      const cur = D.assets.get(s.scid);
      if (!cur || !cur.m) return toast("This market could not be re-read from your node; nothing was signed", "err", 8000);
      const fresh = buy ? (cur.kind === "launch" ? A.launchBuy(cur, amount) : A.poolBuy(cur, amount))
        : (cur.kind === "launch" ? A.launchSell(cur, amount) : A.poolSell(cur, amount));
      if (!fresh || fresh.out <= Z) return toast("This amount can no longer be filled at this market's reserves; nothing was signed", "err", 9000);
      if (fresh.out < A.minOut(q.out, slip)) { T.tradeSheet(cur, side, input.value); return toast("The price moved while this sheet was open. Re-priced from the market as it is now — check the numbers and confirm again.", "err", 9000); }
      const min = A.minOut(fresh.out, slip);
      closeSheet();
      await V.app.trade(cur, side, amount, min, publish && !priv ? 1 : 0, fresh, priv ? { dest: destIn.value.trim() } : null);
    };
    destIn.oninput = () => quote();
    // switch to another market without leaving the sheet (same side); the route row says which maths applies
    const pick = () => openSheet(h("h2", null, "Choose a market"), h("div", { class: "sub mt8" }, "Same side, same sheet."),
      h("div", { class: "list mt" }, D.active().sort((a, b) => b.m.hot - a.m.hot).slice(0, 30).map((x) => h("button", { class: "feed-item", type: "button", onclick: () => T.tradeSheet(x, side) }, avatar(x, "sm"), h("div", { class: "body ellipsis" }, h("b", null, H.rowName(x)), h("div", { class: "sub ellipsis" }, H.mark(x, " · "), x.name)), h("div", { style: "text-align:right" }, h("div", { class: "num" }, S.fmtPriceSub(x.price) + " " + D.qsym(x)), changePill(x.m.c24h))))));
    openSheet(
      h("div", { class: "row" }, avatar(s, "sm"), h("div", { class: "grow" }, h("button", { class: "row tight", type: "button", onclick: pick, "aria-label": "switch market", title: "Switch market" }, h("h2", null, (buy ? "Buy " : "Sell ") + s.ticker), icon("chevron", "svgi dim2")), h("div", { class: "sub num" }, M.pairOf(s) + " · " + S.fmtPriceSub(s.price) + " " + qsym, " ", changePill(s.m.c24h))), h("div", { class: "badges col" }, U.verified(s), pill(curve ? "curve" : "AMM"))),
      h("div", { class: "row tight mt8", style: "flex-wrap:wrap" }, h("span", { class: "sub tiny mono" }, "Contract " + V.shortAddr(s.scid)), U.warnings(s)),
      // The code hash matched a LEGACY entry: an earlier version of this template, accepted so contracts already
      // deployed from it still read. One kept launch version refunds a buy's overshoot before it checks min_out,
      // which is a total drain of the raise, and nothing on this sheet distinguished it from the current code.
      s.templateLegacy ? h("div", { class: "warnbox mt" }, h("b", null, "Older template — not the current code."),
        h("div", { class: "mt8" }, "This contract's code matches an earlier version of the shipped template, not the one this build ships. It was verified when that version was current and it cannot be changed now. Some earlier versions have defects that were fixed in later ones; read the contract before you trade it.")) : null,
      // A quote this build does not name is not a wrapped asset: nothing says what backs it or what its decimals
      // are. It was called federation-backed here (ADV-PRICE-05); it is named for what it is, and not traded.
      s.quote && !qv ? h("div", { class: "errbox mt" }, h("b", null, "This market is priced in an asset this build does not name."),
        h("div", { class: "mt8" }, "Its quote is the contract " + s.quote + ". This app knows neither that asset's decimals nor what backs it, so no amount on this sheet can be read, and it will not send this trade.")) :
      // a wrapped quote is stated before the amount box, not discovered when the trade fails
      s.quote ? h("div", { class: "warnbox mt" }, h("b", null, "This market settles in wrapped " + qsym + "."),
        h("div", { class: "mt8" }, (buy ? "You spend wrapped " + qsym + ", held on DERO as an asset" : "You are paid in wrapped " + qsym + ", held on DERO as an asset") + " — not " + qsym + " on " + ((qv && qv.name) || "its own chain") + ", and not DERO. Wrapped units are backed one for one by a k-of-n federation that can fail, so they are not trustless. The network fee is DERO either way."),
        button((buy ? "Get wrapped " : "Unwrap ") + qsym, "ghost small mt8", () => { closeSheet(); router.go(M.moveRoute()); })) : null,
      h("div", { class: "amount mt" }, input, h("span", { class: "unit" }, unit)),
      h("div", { class: "between mt8" }, h("span", { class: "note ellipsis", id: "balNote" }, "Balance: …"), presets),
      h("div", { class: "outline mt" }, outL, outV, rate),
      riskBox,
      h("div", { class: "eyebrow mt" }, "What this trade costs"),
      h("div", { class: "mt8" }, costs),
      U.toggle("Post this trade", publish, (on) => { publish = on; schedGas(); paint(); }, "Off by default. Publishing writes your address and this trade into the contract for everyone to see, and costs a little more gas. Balances stay private either way.", "publishSw"),
      U.details("Private mode: hide who paid", h("div", { class: "stack", style: "gap:8px;padding:6px 0 10px" },
        U.toggle("Ring size 16, pay a named address", priv, (on) => { priv = on; quote(); }, "The contract never learns who paid. To anyone reading the chain the payer is one of 8: the ring has 16 members, and a public bit in every DERO proof says which half the payer is in. What you buy or sell goes to the address below, which is public. Sending it to the connected address hides nothing.", "privSw"),
        h("div", { class: "field" }, "Receive at", destIn),
        h("div", { class: "note" }, "A private trade is never posted: a public record would name the receiver. A trade paid to another address can only be checked by inclusion in a block: this app cannot read that wallet's balance, and the trade will not appear in this portfolio."))),
      h("div", { class: "eyebrow mt" }, "Review"),
      h("div", { class: "mt8" }, review),
      confirm,
      h("div", { class: "note mt8", style: "text-align:center" }, "Your wallet shows the exact transaction and asks you to confirm."));
    quote();
    (async () => {
      const note = U.$("#balNote");
      if (!V.wallet.connected) { if (note) note.textContent = "Connect the wallet for balances"; return; }
      // a pool sells its asset, not its own LP token
      try { const b = await V.wallet.getBalance(buy ? (s.quote || "") : s.asset); balance = V.toBig(b && b.balance) || Z; if (note) note.textContent = "Balance: " + A.fmtUnits(balance, dec, 4) + " " + unit; quote(); } catch (e) { balFail = true; if (note) note.textContent = "Balance unreadable: the wallet did not answer"; quote(); }
    })();
  };
})();
