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
      try { s = D.assets.get(scid) || (await D.loadContract(scid)); } catch (e) { return render(el, h("div", { class: "errbox mt" }, "Cannot load " + scid + ": " + e.message)); } if (!s || s.untrusted) return render(el, h("div", { class: "errbox mt" }, "Not listed: " + (s ? s.untrusted : "unknown"))); s.unlisted = true; if (s.kind === "pool") { const l = D.assets.get(s.asset); s.assetVerified = !!(l && l.kind === "launch"); } if (s.kind === "pool" && s.assetVerified) { const l = D.assets.get(s.asset); s.ticker = l.ticker; s.name = l.name + " pool"; s.dec = l.dec; s.icon = l.icon; s.dupTicker = !!l.dupTicker; s.lookalike = !!l.lookalike; } else { const k = U.tickerKey(s.ticker); s.lookalike = U.hasHomoglyph(s.ticker); s.dupTicker = !!k && D.list().some((x) => x.scid !== s.scid && !(x.kind === "pool" && x.assetVerified) && U.tickerKey(x.ticker) === k); } D.nameQuote(s); S.enrich(s, D.height); }
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
      const m = s.m, launch = s.kind === "launch", curve = launch && s.phase === Z;
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
        band(mini("FDV", m.mcap ? S.fmtDeroShort(m.mcap) : "—"), mini("Liquidity", S.fmtDeroShort(m.liq)), mini("Volume 24h", S.fmtDeroShort(m.vol24)), mini("Age", S.ageText(m.age))),
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
        else items.push(kv("LP supply", A.fmtUnits(s.lp, 5, 2)));
        items.unshift(h("div", { class: "row tight mb", style: "align-items:flex-start" }, icon("shield", unsure ? "svgi warn" : "svgi up"),
          h("div", { class: "note" }, (s.unlisted ? "Matches the shipped “" + s.kind + "” template but is not in the DMZ registry." : "Matches the shipped “" + s.kind + "” template and pays the protocol treasury.") + (s.kind === "pool" && !s.assetVerified ? " The asset is not a DMZ launch: verify its id yourself." : ""))));
        if (s.descr) items.unshift(h("div", { class: "sub mb", style: "line-height:1.5" }, s.descr));
        const actions = [];
        if (launch && s.tmode === 3n) actions.push(button("Stake / rewards", "wide", () => V.sheets.stake(s)));
        if (launch && s.tmode === 2n && s.phase === 1n && s.tacc > Z) actions.push(button("Trigger buyback (" + A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s) + ")", "wide", () => V.app.tx({ scid: s.scid, entry: "Buyback", args: [], describe: "Buyback" })));
        if (launch && s.tmode === 1n && me() === s.creator && s.tacc > Z) actions.push(button("Claim creator tax (" + A.fmtDero(s.tacc, 2) + ")", "wide", () => V.app.tx({ scid: s.scid, entry: "ClaimTax", args: [], describe: "Claim tax" })));
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
  T.tradeSheet = (s, side) => {
    const launch = s.kind === "launch", buy = side === 1;
    let slip = V.app.settings.slippage || 100, publish = false, ack = false, amount = Z, balance = null, q = null, priv = false;
    const destIn = h("input", { placeholder: "address that is not linked to you", class: "mono", autocomplete: "off", spellcheck: "false", "aria-label": "receive at" });
    const input = h("input", { inputmode: "decimal", placeholder: "0", "aria-label": "amount", autocomplete: "off" });
    const unit = buy ? D.qsym(s) : s.ticker, dec = buy ? D.qdec(s) : s.dec, outUnit = buy ? s.ticker : D.qsym(s), outDec = buy ? s.dec : D.qdec(s);
    const outV = h("div", { class: "v num" }, "0"), outL = h("div", { class: "l" }, "You receive (estimated)"), rate = h("div", { class: "note mt8" }), detail = h("div", { class: "stack", style: "gap:0" }), riskBox = h("div");
    const confirm = button((buy ? "Buy " : "Sell ") + s.ticker, (buy ? "buy" : "sell") + " wide mt", () => go(), { disabled: true });
    const risks = H.risks(s);
    const quote = () => {
      amount = A.parseUnits(input.value, dec) || Z;
      q = amount > Z ? (buy ? (launch ? A.launchBuy(s, amount) : A.poolBuy(s, amount)) : (launch ? A.launchSell(s, amount) : A.poolSell(s, amount))) : null;
      const big = q && q.impact > 1500, mid = q && q.impact > 500;
      confirm.disabled = !q || (big && !ack) || (balance !== null && amount > balance) || (priv && !V.isAddress(destIn.value.trim())) || (priv && q.refund > Z);
      confirm.textContent = balance !== null && amount > balance ? "Insufficient " + unit : priv && q.refund > Z ? "Overshoots the curve: lower the amount" : (buy ? "Buy " : "Sell ") + s.ticker;
      if (!q) { outV.textContent = amount > Z ? "—" : "0"; rate.textContent = amount > Z ? "This amount cannot be filled: too small, above what the curve has left, or more than the pool can take." : "1 " + s.ticker + " ≈ " + S.fmtPriceSub(s.price) + " DERO"; render(detail); render(riskBox, risks.length ? h("div", { class: "riskbox warn mt" }, icon("warn"), h("div", null, risks.map((r) => h("div", null, r.t)))) : null); return; }
      const min = A.minOut(q.out, slip);
      outV.textContent = A.fmtUnits(q.out, outDec, 6) + " " + outUnit;
      rate.textContent = "Price impact " + (q.impact / 100).toFixed(2) + "% · at least " + A.fmtUnits(min, outDec, 6) + " " + outUnit + " or the trade reverts";
      rate.className = "note mt8 " + (big ? "down" : mid ? "warn" : "");
      render(detail,
        kv("Fee " + Number(s.fee) / 100 + "%", A.fmtUnits(q.fee, buy || (launch && s.phase === Z) ? D.qdec(s) : s.dec, 5) + " " + (buy || (launch && s.phase === Z) ? D.qsym(s) : s.ticker)),
        q.tax > Z ? kv("Token tax " + Number(s.tax) / 100 + "%", A.fmtUnits(q.tax, D.qdec(s), 5) + " " + D.qsym(s)) : null,
        q.refund > Z ? kv("Refund (curve ends)", A.fmtUnits(q.refund, D.qdec(s), 5) + " " + D.qsym(s) + (priv ? " → would be paid to the receive address, not to you" : "")) : null,
        kv("Min received", A.fmtUnits(min, outDec, 6) + " " + outUnit), kv("Route", launch && s.phase === Z ? "bonding curve" : "constant-product AMM"), kv("Ring size", priv ? "16 (the payer is one of 16 accounts)" : "2 (the contract pays the signer; anyone can compute it)"),
        priv && V.wallet.canon && V.canonAddress(destIn.value.trim()) === V.wallet.canon ? h("div", { class: "warnbox mt8" }, "This is the connected address: the ring hides who paid, but the receiver is you, in clear.") : null,
        q.graduates ? h("div", { class: "warnbox mt8" }, "This buy completes the curve: the token graduates to its locked AMM.") : null);
      render(riskBox,
        big ? h("div", { class: "riskbox danger mt" }, icon("warn"), h("div", null, h("b", null, "Price impact " + (q.impact / 100).toFixed(1) + "%"), h("div", null, "You would move the market a lot and get much less than the current price suggests. Split the order or trade less."), U.toggle("I understand, continue anyway", ack, (on) => { ack = on; quote(); }, null, "ackSw")))
          : mid ? h("div", { class: "riskbox warn mt" }, icon("warn"), h("div", null, "Price impact " + (q.impact / 100).toFixed(1) + "%: this order noticeably moves the price.")) : null,
        risks.length && !big ? h("div", { class: "riskbox warn mt8" }, icon("warn"), h("div", null, risks.map((r) => h("div", null, r.t)))) : null);
    };
    input.oninput = quote;
    const presets = h("div", { class: "chips nowrap" }, [25, 50, 75, 100].map((p) => h("button", { class: "chip sm", type: "button", onclick: () => { if (balance === null) return toast("Connect the wallet to use presets"); let v = balance * BigInt(p) / 100n; if (buy && p === 100) v = v > 20000n ? v - 20000n : Z; input.value = A.fmtUnits(v, dec, Number(dec)).replace(/,/g, ""); quote(); } }, p === 100 ? "Max" : p + "%")));
    const slipEl = h("div", { class: "chips" }, [50, 100, 200, 500].map((b) => h("button", { class: "chip " + (slip === b ? "on" : ""), type: "button", onclick: (e) => { slip = b; U.$$("button", slipEl).forEach((x) => x.classList.remove("on")); e.currentTarget.classList.add("on"); V.app.saveSettings({ slippage: b }); quote(); } }, b / 100 + "%")));
    // a private trade is never published: a public record would name the receiver
    const go = async () => { if (!q) return; const min = A.minOut(q.out, slip); closeSheet(); await V.app.trade(s, side, amount, min, publish && !priv ? 1 : 0, q, priv ? { dest: destIn.value.trim() } : null); };
    destIn.oninput = () => quote();
    // switch to another market without leaving the sheet (same side); the route pill says which maths applies
    const pick = () => openSheet(h("h2", null, "Choose a market"), h("div", { class: "sub mt8" }, "Same side, same sheet."),
      h("div", { class: "list mt" }, D.active().sort((a, b) => b.m.hot - a.m.hot).slice(0, 30).map((x) => h("button", { class: "feed-item", type: "button", onclick: () => T.tradeSheet(x, side) }, avatar(x, "sm"), h("div", { class: "body ellipsis" }, h("b", null, x.ticker), h("div", { class: "sub ellipsis" }, x.name)), h("div", { style: "text-align:right" }, h("div", { class: "num" }, S.fmtPriceSub(x.price)), changePill(x.m.c24h))))));
    openSheet(
      h("div", { class: "row" }, avatar(s, "sm"), h("div", { class: "grow" }, h("button", { class: "row tight", type: "button", onclick: pick, "aria-label": "switch market", title: "Switch market" }, h("h2", null, (buy ? "Buy " : "Sell ") + s.ticker), icon("chevron", "svgi dim2")), h("div", { class: "sub num" }, S.fmtPriceSub(s.price) + " DERO", " ", changePill(s.m.c24h))), h("div", { class: "badges col" }, U.verified(s), pill(launch && s.phase === Z ? "curve" : "AMM"))),
      h("div", { class: "row tight mt8", style: "flex-wrap:wrap" }, h("span", { class: "sub tiny mono" }, "Contract " + V.shortAddr(s.scid)), U.warnings(s)),
      h("div", { class: "amount mt" }, input, h("span", { class: "unit" }, unit)),
      h("div", { class: "between mt8" }, h("span", { class: "note ellipsis", id: "balNote" }, "Balance: …"), presets),
      h("div", { class: "outline mt" }, outL, outV, rate),
      riskBox,
      U.details("Details, slippage and fees", h("div", { class: "between", style: "padding:6px 0 10px" }, h("span", { class: "sub" }, "Max slippage"), slipEl), detail),
      U.toggle("Post this trade", publish, (on) => { publish = on; }, "Off by default. Publishing writes your address and this trade into the contract for everyone to see. Balances stay private either way.", "publishSw"),
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
