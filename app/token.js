// DMZ token.js — the token page and the trade sheet. The page is one Level 1 instrument (the price and its four
// change windows), the chart as its hero, then named Level 3 regions — market, published activity, block pressure,
// contract — separated by hairlines rather than stacked in cards. Nothing here is a box around a number.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, L = V.live, H = V.home, { h, render, avatar, pill, changePill, router, button, kv, openSheet, closeSheet, toast, icon } = U;
  const T = (V.token = {});
  const me = () => V.wallet.canon;
  const Z = 0n;
  const chartState = { mode: "candles", range: "24h" };
  const copy = (txt, what) => { if (navigator.clipboard) navigator.clipboard.writeText(txt).then(() => toast(what + " copied", "ok"), () => toast("Clipboard blocked")); };
  // technical data: the mono voice, middle-truncated, the whole value on the clipboard. Never lime — an id is not
  // an action, and lime is reserved for what you can do.
  const cpy = (what, v) => h("button", { class: "mono dim", type: "button", title: "Copy " + what.toLowerCase(), "aria-label": "copy " + what.toLowerCase(), onclick: () => copy(v, what) }, V.shortAddr(v));
  // Level 3: a label above a number, no box and no rule. The grid keeps the columns aligned at every width.
  const mini = (l, v, cls) => h("div", { class: "mini" }, h("div", { class: "l" }, l), h("div", { class: "v num " + (cls || "") }, v));
  const band = (...items) => h("div", { class: "mt8", style: "display:grid;grid-template-columns:repeat(auto-fit,minmax(78px,1fr));gap:14px 12px" }, items);
  // a section is a named region, not a card: a hairline, a label, and — where the section has one — its headline
  // number on the same line. Five of them give the page its rhythm below the instrument.
  const head = (l, right) => h("div", { class: "between mt", style: "border-top:1px solid var(--edge);padding-top:13px" }, h("span", { class: "eyebrow" }, l), right || null);

  T.page = async (el, scid) => {
    if (!/^[0-9a-f]{64}$/.test(String(scid || ""))) return render(el, h("div", { class: "errbox mt" }, "Not a contract id"));
    let s = D.assets.get(scid);
    // a contract reached by id that the registry does not list is shown, marked, and kept out of every list
    if (!s) {
      render(el, h("div", { class: "list mt" }, U.skeleton(3)));
      // A link opened directly lands here before the node connection exists, so the first read fails with
      // "no node connection" and the page gives up — on the one route a shared link always takes. Wait for the
      // app to reach the chain instead, then read. Bounded, so a genuinely offline node still reports itself.
      for (let i = 0; i < 60 && !D.lastRefresh; i++) await new Promise((r) => setTimeout(r, 250));
      // A market found in the registry is listed; only one the registry does not carry is not. Marking every
      // deep-linked market "not listed" told visitors a registered, verified market was unvouched for — on the
      // exact route a shared link takes, which is where that warning does the most damage.
      const listed = D.assets.get(scid);
      try { s = listed || (await D.loadContract(scid)); } catch (e) { return render(el, h("div", { class: "errbox mt" }, "Cannot load " + scid + ": " + e.message)); } if (!s || s.untrusted) return render(el, h("div", { class: "errbox mt" }, "Not listed: " + (s ? s.untrusted : "unknown"))); if (!listed) s.unlisted = true; if (s.kind === "pool") { const l = D.assets.get(s.asset); s.assetVerified = !!(l && l.kind === "launch"); } if (s.kind === "pool" && s.assetVerified) { const l = D.assets.get(s.asset); s.ticker = l.ticker; s.name = l.name + " pool"; s.dec = l.dec; s.icon = l.icon; s.dupTicker = !!l.dupTicker; s.lookalike = !!l.lookalike; } else { const k = U.tickerKey(s.ticker); s.lookalike = U.hasHomoglyph(s.ticker); s.dupTicker = !!k && D.list().some((x) => x.scid !== s.scid && !(x.kind === "pool" && x.assetVerified) && !(s.wrappedBase && x.wrappedBase && x.asset === s.asset) && U.tickerKey(x.ticker) === k); } s.impersonates = D.impersonation(s); D.nameQuote(s); S.enrich(s, D.height); }
    let tab = "trades", lastSig = null;
    const chartBox = h("div"), priceEl = h("div", { class: "big num" }), statsEl = h("div"), pressEl = h("div"), techEl = h("div"), bodyEl = h("div", { class: "mt" }), headEl = h("div", { class: "hero card mt" }), gradEl = h("div"), riskEl = h("div");
    const drawChart = () => {
      const markers = s.trades.filter((t) => D.follows.has(t.addr) || t.addr === me()).map((t) => ({ ts: t.ts, side: t.side, label: D.displayName(t.addr) }));
      // the controls sit above the instrument they set, so the chart runs into the readings below it
      render(chartBox,
        h("div", { class: "between mt" },
          h("div", { class: "chips" }, ["1h", "6h", "24h", "7d", "all"].map((r) => h("button", { class: "chip " + (chartState.range === r ? "on" : ""), type: "button", onclick: () => { chartState.range = r; drawChart(); } }, r))),
          U.segmented([{ v: "candles", l: "Candles" }, { v: "area", l: "Line" }], chartState.mode, (v) => { chartState.mode = v; drawChart(); })),
        V.chart.render(null, s, { mode: chartState.mode, range: chartState.range, markers, wide: window.innerWidth >= 960 }),
        h("div", { class: "note mt8" }, "Exact at block resolution: candles come from the closing reserves the contract writes every active block.", markers.length ? " Triangles: trades by you and accounts you follow." : ""));
    };
    const drawHead = () => {
      // the Market band's amounts are in the quote and say so: the price eyebrow names it, and a bare number
      // under that eyebrow would still be read as DERO by anyone who did not look up
      const m = s.m, launch = s.kind === "launch", curve = launch && s.phase === Z, qs = " " + D.qsym(s);
      // Level 1 — the instrument: what this market is, and the one number the screen is about
      render(headEl,
        h("div", { class: "row" }, avatar(s, "big"), h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, h("h1", { class: "ellipsis" }, s.ticker), H.star(s), U.warnings(s)), h("div", { class: "sub ellipsis" }, s.name), h("div", { class: "row tight mt8" }, U.verified(s), H.badges(s))),
          h("button", { class: "iconbtn", type: "button", "aria-label": "copy link to this market", title: "Copy link", onclick: () => copy(location.href.split("#")[0] + "#token/" + s.scid, "Link") }, icon("share"))),
        h("div", { class: "eyebrow mt" }, "Price · " + D.qsym(s)),
        h("div", { class: "pulse-row mt8" }, priceEl,
          // the four changes are one aligned block: it sits beside the price where there is room and drops whole
          // below it where there is not, so a single window never strands itself on its own line
          h("div", { style: "display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;flex:1 1 250px" },
            [["5m", m.c5m], ["1h", m.c1h], ["6h", m.c6h], ["24h", m.c24h]].map(([l, c]) => mini(l, c === null ? "—" : A.pct(c), c === null ? "dim2" : c >= 0 ? "up" : "down")))));
      L.num(priceEl, s.price, S.fmtPriceSub);
      render(gradEl, curve ? [head("Graduation", h("b", { class: "num" }, m.progress.toFixed(1) + "%")),
        h("div", { class: "progress mt8" }, h("i", { style: "width:" + Math.min(100, m.progress) + "%" })),
        h("div", { class: "note mt8" }, A.fmtUnits(s.real, D.qdec(s), 1) + " of " + A.fmtUnits(s.G, D.qdec(s), 0) + " " + D.qsym(s) + " raised. At 100% the curve becomes a permanently locked AMM at the same price.")] : null);
      const risks = H.risks(s);
      render(riskEl, risks.length ? h("div", { class: "riskbox warn mt" }, icon("warn"), h("div", null, risks.map((r) => h("div", null, r.t)))) : null);
      // Level 3 — two readings, in two voices: what the market is worth, and what was said in public about it
      render(statsEl,
        head("Market"),
        band(mini("FDV", m.mcap ? S.fmtDeroShort(m.mcap) + qs : "—"), mini("Liquidity", S.fmtDeroShort(m.liq) + qs), mini("Volume 24h", S.fmtDeroShort(m.vol24) + qs), mini("Age", S.ageText(m.age))),
        head("Published activity"),
        band(mini("Trades 24h", String(m.buys24 + m.sells24)),
          mini("Buys / sells", h("span", null, h("span", { class: "up" }, String(m.buys24)), " / ", h("span", { class: "down" }, String(m.sells24)))),
          mini("Traders", String(m.holders)),
          mini("Last trade", m.lastTs ? h("span", { "data-ts": m.lastTs }, U.timeAgo(m.lastTs) + " ago") : "—")),
        h("div", { class: "note mt8" }, "Counted from trades whose sender chose to post them. Balances are private on DERO, and a trade nobody posted still moves the price."));
      const tot = m.upBlocks24 + m.downBlocks24 || 1, bp = Math.round((m.upBlocks24 / tot) * 100);
      render(pressEl,
        head("Block pressure · 24h", h("span", { class: "num tiny" }, h("span", { class: "up" }, bp + "% buy"), h("span", { class: "dim2" }, " · "), h("span", { class: "down" }, 100 - bp + "% sell"))),
        h("div", { class: "pressure mt8" }, h("i", { style: "width:" + bp + "%" })),
        h("div", { class: "note mt8" }, "Reserve direction per active block, so unposted trades are counted here too."));
      // the technical block: the ids a person has to check for themselves
      render(techEl,
        head("Contract"),
        h("div", { class: "mt8" }, kv("Contract id", cpy("Contract id", s.scid)),
          launch ? kv("Creator", cpy("Creator", s.creator)) : kv("Asset id", cpy("Asset id", s.asset)),
          s.treasury ? kv("Protocol treasury", cpy("Treasury", s.treasury)) : null),
        h("div", { class: "note mt8" }, "Click a value to copy it in full. A ticker is not an identity: check the contract id before you trade."));
    };
    const drawBody = async () => {
      const launch = s.kind === "launch";
      if (tab === "trades") render(bodyEl, s.trades.length
        ? h("div", { class: "list" }, [...s.trades].reverse().slice(0, 60).map((t) => H.feedItem(Object.assign({ asset: s }, t), true)))
        : U.emptyState("feed", "Published trades", "Nobody has posted a trade on this market. Posting is off by default, and an unposted trade still moves the price and still draws a candle.", button("Buy " + s.ticker, "primary", () => T.tradeSheet(s, 1))));
      else if (tab === "holders") {
        const traders = new Map(); for (const t of s.trades) traders.set(t.addr, (traders.get(t.addr) || 0) + 1);
        const mine = launch && s.tmode === 3n && me() ? await D.myKeys(s.scid, me(), ["st_"]).catch(() => ({})) : {};
        render(bodyEl, h("div", { class: "note mb" }, "Balances are private on DERO. Listed: accounts that published trades" + (launch && s.tmode === 3n ? ", plus the staking vault totals." : ".")),
          launch && s.tmode === 3n ? h("div", { class: "mb" }, kv("Total staked", A.fmtCompact(s.stot, s.dec) + " " + s.ticker), mine.st ? kv("Your stake", A.fmtUnits(mine.st, s.dec, 4)) : null) : null,
          traders.size ? h("div", { class: "list" }, [...traders.entries()].sort((x, y) => y[1] - x[1]).slice(0, 30).map(([a, n]) => h("button", { class: "feed-item", type: "button", onclick: () => router.go("profile", a) }, avatar(a, "sm"), h("div", { class: "body ellipsis" }, D.displayName(a)), h("span", { class: "sub tiny num" }, n + " posted"))))
            : U.emptyState("user", "Public traders", "No account has published a trade here. Posting names the sender, so an empty list is not an empty market.", null));
      } else {
        const fee = Number(s.fee) / 100;
        // the verification sentence is prose, so it is a line of prose — a right-aligned value column is for numbers
        const unsure = s.unlisted || (s.kind === "pool" && !s.assetVerified);
        const items = [kv("Fee", fee + "% per trade"), kv(D.qsym(s) + " reserve", A.fmtUnits(s.rd, D.qdec(s), 2)), kv("Token reserve", A.fmtCompact(s.ra, s.dec)), kv("Price points", String(s.pc)), kv("Published trades", String(s.tc))];
        if (launch) items.push(kv("Supply", A.fmtCompact(s.supply, s.dec)), kv("Creator allocation", H.allocation(s) > Z ? A.fmtCompact(H.allocation(s), s.dec) + " (" + (H.allocBps(s) / 100).toFixed(2) + "% at launch)" : "none"), kv("Creator", button("View profile", "ghost small", () => router.go("profile", s.creator))), kv("Tax", s.tax > Z ? Number(s.tax) / 100 + "% → " + ["none", "creator", "buyback & lock", "stakers"][Number(s.tmode)] : "none"), kv("Locked by buyback", A.fmtCompact(s.locked, s.dec)), kv("Staked", A.fmtCompact(s.stot, s.dec)), kv("Liquidity", s.phase === Z ? "curve (virtual)" : "contract-owned, locked forever"));
        else items.push(kv("LP supply", A.fmtUnits(s.lp, D.qdec(s), 2))); // LP is minted one unit per quote unit deposited, so it carries the quote's decimals
        items.unshift(h("div", { class: "row tight mb", style: "align-items:flex-start" }, icon("shield", unsure ? "svgi warn" : "svgi up"),
          h("div", { class: "note" }, (s.unlisted ? "Matches the shipped “" + s.kind + "” template but is not in the DMZ registry." : "Matches the shipped “" + s.kind + "” template and pays the protocol treasury.") + (s.kind === "pool" && !s.assetVerified ? " The asset is not a DMZ launch: verify its id yourself." : ""))));
        if (s.descr) items.unshift(h("div", { class: "sub mb", style: "line-height:1.5" }, s.descr));
        const actions = [];
        if (launch && s.tmode === 3n) actions.push(button("Stake / rewards", "wide", () => V.sheets.stake(s)));
        if (launch && s.tmode === 2n && s.phase === 1n && s.tacc > Z) actions.push(button("Trigger buyback (" + A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s) + ")", "wide", () => V.app.tx({ scid: s.scid, entry: "Buyback", args: [], describe: "Buyback" })));
        if (launch && s.tmode === 1n && me() === s.creator && s.tacc > Z) actions.push(button("Claim creator tax (" + A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s) + ")", "wide", () => V.app.tx({ scid: s.scid, entry: "ClaimTax", args: [], describe: "Claim tax" })));
        if (!launch) actions.push(button("Add / remove liquidity", "wide", () => V.sheets.liquidity(s)));
        if (me() && me() === s.treasury && (s.fd > Z || s.fa > Z)) actions.push(button("Claim protocol fees", "wide", () => V.app.tx({ scid: s.scid, entry: "ClaimFees", args: [], describe: "Claim fees" })));
        render(bodyEl, h("div", { class: "mt8" }, items), actions.length ? h("div", { class: "stack mt" }, actions) : null);
      }
    };
    const draw = () => {
      s = D.assets.get(scid) || s;
      if (!el.contains(headEl)) render(el, h("div", { class: "tk-grid" },
        h("div", { class: "tk-main" }, headEl, riskEl, chartBox, gradEl, statsEl, pressEl, techEl),
        h("div", { class: "tk-side" },
          h("div", { class: "sticky-actions" }, button("Buy", "buy wide", () => T.tradeSheet(s, 1)), button("Sell", "sell wide", () => T.tradeSheet(s, 2))),
          h("div", { class: "mt" }, U.segmented([{ v: "trades", l: "Trades" }, { v: "holders", l: "Holders" }, { v: "info", l: "Info" }], tab, (v) => { tab = v; drawBody(); })),
          bodyEl, h("div", { style: "height:70px" }))));
      drawHead();
      if (s.sig !== lastSig) { lastSig = s.sig; drawChart(); drawBody(); }
    };
    draw(); D.onChange(() => router.current && router.current.name === "token" && router.current.param === scid && draw());
  };

  // ---- trade sheet ----
  // Requirement 3: every cost stands in front of the button, in the asset it is actually charged in. The trading
  // fee, the protocol's share of it, the token tax, price impact and the minimum received are all in the market's
  // own quote (D.qsym / D.qdec — never assume DERO). The network fee is DERO on every market whatever it trades,
  // so it is a row of its own, named as DERO. Nothing that costs money sits behind a disclosure.
  T.tradeSheet = (s, side) => {
    const launch = s.kind === "launch", buy = side === 1, curve = launch && s.phase === Z;
    const qsym = D.qsym(s), qdec = D.qdec(s), qv = s.quote ? D.vaults().find((v) => v.scid === s.quote) : null;
    // the fee is charged on whatever goes in, except on the curve where both legs are priced in the quote
    const feeQ = buy || curve, feeSym = feeQ ? qsym : s.ticker, feeDec = feeQ ? qdec : s.dec;
    // on the curve the whole fee is the protocol's; in an AMM pfee of it is and the rest stays in the reserves
    const protoBps = curve ? 10000n : (s.pfee || Z);
    const taxTo = ["nobody", "the creator", "a buyback that locks tokens", "stakers"][Number(s.tmode || Z)] || "the contract";
    let slip = V.app.settings.slippage || 100, publish = false, ack = false, amount = Z, balance = null, q = null, priv = false;
    let gasTxt = "", gasSeq = 0, gasTimer = null;
    const destIn = h("input", { placeholder: "address that is not linked to you", class: "mono", autocomplete: "off", spellcheck: "false", "aria-label": "receive at" });
    const input = h("input", { inputmode: "decimal", placeholder: "0", "aria-label": "amount", autocomplete: "off" });
    const unit = buy ? qsym : s.ticker, dec = buy ? qdec : s.dec, outUnit = buy ? s.ticker : qsym, outDec = buy ? s.dec : qdec;
    const outV = h("div", { class: "v num" }, "0"), outL = h("div", { class: "l" }, "You receive (estimated)"), rate = h("div", { class: "note mt8" }), costs = h("div", { class: "stack", style: "gap:0" }), riskBox = h("div");
    const confirm = button((buy ? "Buy " : "Sell ") + s.ticker, (buy ? "buy" : "sell") + " wide mt", () => go(), { disabled: true });
    const risks = H.risks(s);
    const slipEl = h("div", { class: "chips" }, [50, 100, 200, 500].map((b) => h("button", { class: "chip sm " + (slip === b ? "on" : ""), type: "button", onclick: (e) => { slip = b; U.$$("button", slipEl).forEach((x) => x.classList.remove("on")); e.currentTarget.classList.add("on"); V.app.saveSettings({ slippage: b }); quote(); } }, b / 100 + "%")));
    // the cost block, drawn whether or not an amount is typed: the fee, the route and the ring are facts about the
    // market and are true before anything is entered; the amounts fill in as soon as there is a quote
    const paint = () => {
      const big = q && q.impact > 1500, mid = q && q.impact > 500, dash = "—";
      render(costs,
        kv("Trading fee · " + Number(s.fee) / 100 + "%", q ? A.fmtUnits(q.fee, feeDec, 5) + " " + feeSym : dash),
        kv("of it, protocol treasury · " + Number(protoBps) / 100 + "%", q ? A.fmtUnits(A.bps(q.fee, protoBps), feeDec, 5) + " " + feeSym : dash),
        q && q.tax > Z ? kv("Token tax · " + Number(s.tax) / 100 + "% to " + taxTo, A.fmtUnits(q.tax, qdec, 5) + " " + qsym) : null,
        q && q.refund > Z ? kv("Refund, the curve ends here", A.fmtUnits(q.refund, qdec, 5) + " " + qsym + (priv ? " → paid to the receive address, not to you" : "")) : null,
        kv("Price impact", q ? (q.impact / 100).toFixed(2) + "%" : dash, big ? "down" : mid ? "warn" : ""),
        kv("Minimum received, or it reverts", q ? A.fmtUnits(A.minOut(q.out, slip), outDec, 6) + " " + outUnit : dash),
        kv("Max slippage", slipEl),
        V.sheets.gasRow(gasTxt, !!q),
        kv("Route", curve ? "bonding curve" : "constant-product AMM"),
        kv("Ring size", priv ? "16 · the payer is hidden" : "2 · the payer is public"),
        h("div", { class: "note mt8" }, (curve ? "On the bonding curve the whole trading fee goes to the protocol treasury. " : "The rest of the trading fee stays in the pool, for its liquidity providers. ")
          + (priv ? "At ring size 16 the contract never learns who paid, but the address you name below receives in clear. " : "At ring size 2 the contract pays the signer, so anyone can compute which address made this call. ")
          + "The network fee is DERO on every market" + (s.quote ? ", so this one needs a little DERO as well as " + qsym : "") + "; your wallet shows the exact amount before you confirm."),
        priv && V.wallet.canon && V.canonAddress(destIn.value.trim()) === V.wallet.canon ? h("div", { class: "warnbox mt8" }, "This is the connected address: the ring hides who paid, but the receiver is you, in clear.") : null,
        q && q.graduates ? h("div", { class: "warnbox mt8" }, "This buy completes the curve: the token graduates to its locked AMM.") : null);
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
    const quote = () => {
      amount = A.parseUnits(input.value, dec) || Z;
      q = amount > Z ? (buy ? (launch ? A.launchBuy(s, amount) : A.poolBuy(s, amount)) : (launch ? A.launchSell(s, amount) : A.poolSell(s, amount))) : null;
      const big = q && q.impact > 1500, mid = q && q.impact > 500;
      confirm.disabled = !q || (big && !ack) || (balance !== null && amount > balance) || (priv && !V.isAddress(destIn.value.trim())) || (priv && q.refund > Z);
      confirm.textContent = balance !== null && amount > balance ? "Insufficient " + unit : priv && q.refund > Z ? "Overshoots the curve: lower the amount" : (buy ? "Buy " : "Sell ") + s.ticker;
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
    const go = async () => { if (!q) return; const min = A.minOut(q.out, slip); closeSheet(); await V.app.trade(s, side, amount, min, publish && !priv ? 1 : 0, q, priv ? { dest: destIn.value.trim() } : null); };
    destIn.oninput = () => quote();
    // switch to another market without leaving the sheet (same side); the route row says which maths applies
    const pick = () => openSheet(h("h2", null, "Choose a market"), h("div", { class: "sub mt8" }, "Same side, same sheet."),
      h("div", { class: "list mt" }, D.active().sort((a, b) => b.m.hot - a.m.hot).slice(0, 30).map((x) => h("button", { class: "feed-item", type: "button", onclick: () => T.tradeSheet(x, side) }, avatar(x, "sm"), h("div", { class: "body ellipsis" }, h("b", null, H.rowName(x)), h("div", { class: "sub ellipsis" }, H.mark(x, " \u00b7 "), x.name)), h("div", { style: "text-align:right" }, h("div", { class: "num" }, S.fmtPriceSub(x.price) + " " + D.qsym(x)), changePill(x.m.c24h))))));
    openSheet(
      h("div", { class: "row" }, avatar(s, "sm"), h("div", { class: "grow" }, h("button", { class: "row tight", type: "button", onclick: pick, "aria-label": "switch market", title: "Switch market" }, h("h2", null, (buy ? "Buy " : "Sell ") + s.ticker), icon("chevron", "svgi dim2")), h("div", { class: "sub num" }, S.fmtPriceSub(s.price) + " " + qsym, " ", changePill(s.m.c24h))), h("div", { class: "badges col" }, U.verified(s), pill(curve ? "curve" : "AMM"))),
      h("div", { class: "row tight mt8", style: "flex-wrap:wrap" }, h("span", { class: "sub tiny mono" }, "Contract " + V.shortAddr(s.scid)), U.warnings(s)),
      // a wrapped quote is stated before the amount box, not discovered when the trade fails
      s.quote ? h("div", { class: "warnbox mt" }, h("b", null, "This market settles in wrapped " + qsym + "."),
        h("div", { class: "mt8" }, (buy ? "You spend wrapped " + qsym + ", held on DERO as an asset" : "You are paid in wrapped " + qsym + ", held on DERO as an asset") + " — not " + qsym + " on " + ((qv && qv.name) || "its own chain") + ", and not DERO. Wrapped units are backed one for one by a k-of-n federation that can fail, so they are not trustless. The network fee is DERO either way."),
        button((buy ? "Get wrapped " : "Unwrap ") + qsym, "ghost small mt8", () => { closeSheet(); router.go("wrap"); })) : null,
      h("div", { class: "amount mt" }, input, h("span", { class: "unit" }, unit)),
      h("div", { class: "between mt8" }, h("span", { class: "note ellipsis", id: "balNote" }, "Balance: …"), presets),
      h("div", { class: "outline mt" }, outL, outV, rate),
      riskBox,
      h("div", { class: "eyebrow mt" }, "What this trade costs"),
      h("div", { class: "mt8" }, costs),
      U.toggle("Post this trade", publish, (on) => { publish = on; schedGas(); paint(); }, "Off by default. Publishing writes your address and this trade into the contract for everyone to see, and costs a little more gas. Balances stay private either way.", "publishSw"),
      U.details("Private mode: hide who paid", h("div", { class: "stack", style: "gap:8px;padding:6px 0 10px" },
        U.toggle("Ring size 16, pay a named address", priv, (on) => { priv = on; quote(); }, "The contract never learns who paid: the wallet hides the payer among 16 accounts. What you buy or sell goes to the address below, which is public. Sending it to the connected address hides nothing.", "privSw"),
        h("div", { class: "field" }, "Receive at", destIn),
        h("div", { class: "note" }, "A private trade is never posted: a public record would name the receiver. A trade paid to another address can only be checked by inclusion in a block: this app cannot read that wallet's balance, and the trade will not appear in this portfolio."))),
      confirm,
      h("div", { class: "note mt8", style: "text-align:center" }, "Your wallet shows the exact transaction and asks you to confirm."));
    quote();
    (async () => {
      const note = U.$("#balNote");
      if (!V.wallet.connected) { if (note) note.textContent = "Connect the wallet for balances"; return; }
      // a pool sells its asset, not its own LP token
      try { const b = await V.wallet.getBalance(buy ? (s.quote || "") : s.asset); balance = V.toBig(b && b.balance) || Z; if (note) note.textContent = "Balance: " + A.fmtUnits(balance, dec, 4) + " " + unit; quote(); } catch (e) { if (note) note.textContent = "Balance unavailable"; }
    })();
  };
})();
