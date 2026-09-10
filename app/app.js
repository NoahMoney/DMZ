// DMZ app.js — boot, node + wallet connection, block-driven refresh, the transaction flow, holdings and P&L.
// Security posture: no server, no accounts, no keys. Reads go to the node, writes go through the wallet's own
// confirmation prompt. The page ships a Content Security Policy (index.html): only its own scripts run, and the
// only network destinations are the node/wallet sockets and, if enabled, image hosts (loaded without a referrer).
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, W = V.wallet, L = V.live, S = V.stats, { h, toast, openSheet, closeSheet, button, kv, router, icon } = U;
  const app = (V.app = {});
  const Z = 0n;
  const MAX_SLIP = 1000; // 10%: the app never signs a trade that accepts more than this

  app.settings = Object.assign({ daemon: "", hideBalances: false, slippage: 100, reduceMotion: false, autoConnect: false, images: false, seen: false }, (() => { try { return JSON.parse(localStorage.getItem("veil.settings") || "{}"); } catch (e) { return {}; } })());
  if (!Number.isInteger(app.settings.slippage) || app.settings.slippage <= 0 || app.settings.slippage > MAX_SLIP) app.settings.slippage = 100;
  if (app.settings.daemon && !V.endpointURL(app.settings.daemon)) app.settings.daemon = ""; // a stored endpoint is validated on read as well as on write
  // a stored remote (wss://) endpoint is used only after this browser session confirmed it in Settings: any page on
  // this origin can write the setting, and the node chooses what the app sees
  app.endpointConfirmed = (url) => { try { return sessionStorage.getItem("veil.endpointOk") === url; } catch (e) { return false; } };
  app.confirmEndpoint = (url) => { try { sessionStorage.setItem("veil.endpointOk", url); } catch (e) {} };
  app.hostOf = (url) => String(url || "").replace(/^wss?:\/\//, "").replace(/\/ws$/, "");
  V.settings = app.settings;
  app.saveSettings = (patch) => { Object.assign(app.settings, patch); try { localStorage.setItem("veil.settings", JSON.stringify(app.settings)); } catch (e) {} };

  // ---- node ----
  app.connectNode = async () => {
    // settings first, then the wallet's node, then the shipped default; each must pass endpointURL (this device, or wss://)
    if (W.connected && W.daemon && !V.endpointURL(W.daemon) && !app.warnedDaemon) { app.warnedDaemon = true; toast("The wallet's node is not on this device and not wss://: reading through the wallet instead", "", 7000); }
    let stored = V.endpointURL(app.settings.daemon);
    if (stored && !stored.loopback && !app.endpointConfirmed(stored.url)) { if (!app.warnedStored) { app.warnedStored = true; toast("Stored node " + app.hostOf(stored.url) + " is not used until you confirm it in Settings for this session", "", 8000); } stored = null; }
    const pick = stored || (W.connected && V.endpointURL(W.daemon)) || V.endpointURL(D.cfg.daemon);
    V.node.setEndpoint(pick ? pick.url : "");
    V.node.proxy = W.connected ? (m, p) => W.proxy(m, p) : null;
    const pill = U.$("#net");
    try { const i = await V.node.info(); pill.textContent = (i.network || D.cfg.network) + " · " + i.height + (pick && !pick.loopback ? " · " + app.hostOf(pick.url) : ""); pill.className = "pill up"; if (i.network && D.cfg.network && String(i.network).toLowerCase() !== String(D.cfg.network).toLowerCase()) toast("This build is for " + D.cfg.network + " but the node is " + i.network + ": contracts will not be found", "err", 9000); L.start(); }
    catch (e) { pill.textContent = W.connected ? "node via wallet" : "no node"; pill.className = "pill " + (W.connected ? "acc" : "down"); if (W.connected) L.start(); }
  };

  // ---- wallet ----
  app.connectWallet = async () => {
    const btn = U.$("#walletBtn");
    try { btn.textContent = "Approve in wallet…"; btn.disabled = true; await W.connect(); }
    catch (e) { toast("Wallet: " + e.message + ". Open Engram (or dero-wallet-cli menu 16) and enable XSWD.", "err", 7000); }
    finally { btn.disabled = false; app.walletUI(); }
  };
  app.walletUI = () => {
    const btn = U.$("#walletBtn");
    U.render(btn, W.connected ? [icon("wallet"), V.shortAddr(W.address)] : [icon("wallet"), "Connect"]);
    btn.className = "btn small " + (W.connected ? "ghost" : "primary");
    const side = U.$("#sideWallet"), note = U.$("#sideNote");
    if (side) U.render(side, W.connected
      ? h("button", { class: "side-wallet", type: "button", onclick: btn.onclick || (() => {}) }, U.avatar(W.canon, "sm"), h("div", { class: "grow ellipsis" }, h("b", null, D.displayName(W.canon)), h("div", { class: "sub ellipsis" }, app.holdings ? A.fmtDero(app.holdings.dero, 2) + " DERO" : "connected")))
      : h("button", { class: "side-wallet", type: "button", onclick: app.connectWallet }, h("span", { class: "iconbtn" }, icon("wallet")), h("div", { class: "grow" }, h("b", null, "Connect wallet"), h("div", { class: "sub" }, "Engram · XSWD"))));
    // The rail foot carries the build stamp and, above it, the one page that outranks every other piece of copy.
    if (note) U.render(note,
      h("button", { class: "notice-link", type: "button", onclick: () => U.router.go("notice") }, "A message from the dev"),
      h("span", { class: "notice-build" }, (D.cfg.network || "") + " · TELA commit " + ((document.querySelector('meta[name="veil-commit"]') || {}).content || "—")));
    btn.onclick = W.connected ? () => openSheet(h("h2", null, "Wallet"), h("div", { class: "card mt stack" }, kv("Address", h("span", { class: "mono" }, V.shortAddr(W.address))), kv("Node", W.daemon || "—"), kv("Connection", "XSWD on this device")),
      h("div", { class: "note mt" }, "DMZ never sees your keys. Every transaction is built here, shown to you by your wallet, and only sent when you approve it there."),
      button("Disconnect", "wide mt", () => { app.saveSettings({ autoConnect: false }); W.disconnect(); closeSheet(); })) : app.connectWallet;
  };
  W.on("connect", async () => { app.saveSettings({ autoConnect: true }); app.walletUI(); app.connectNode(); try { await W.subscribe("new_topoheight"); } catch (e) {} D.loadSocial(W.canon).catch(() => {}); app.loadHoldings(); router.dispatch(); });
  D.onChange(() => { if (W.connected) app.walletUI(); }, true); // keep: not owned by any screen
  W.on("disconnect", () => { app.holdings = null; app.walletUI(); app.connectNode(); D.follows = new Set(); router.dispatch(); });
  // A hidden tab does no per-block work. The safety-net timer in refresh() already backs off to two minutes
  // when hidden, but both block clocks (the wallet's event and the node poll) cleared it on every block, so a
  // background tab kept running the full refresh for nobody. The visibilitychange handler in init catches up
  // the moment the tab is shown again.
  W.on("event", (ev) => { if (!document.hidden && (ev.event === "new_topoheight" || (ev.result && ev.result.event === "new_topoheight"))) app.scheduleRefresh(1500); });
  // a transfer the wallet answered after the app stopped waiting: say what happened, the wallet history has the rest
  W.on("late", (r) => { toast(r && r.error ? "The wallet refused the transaction it had left waiting: " + short(r.error) : r && isScid(r.txid) ? "The transaction left waiting was sent: " + V.shortAddr(r.txid) + ". Check the wallet history." : "The wallet answered a transaction late: check the wallet history.", r && r.error ? "err" : "", 10000); app.scheduleRefresh(300); });

  // ---- refresh: every new block probes the listed contracts and reloads the ones that changed ----
  let timer = null;
  app.scheduleRefresh = (ms) => { clearTimeout(timer); timer = setTimeout(() => app.refresh(), ms); };
  app.refresh = async (full) => {
    clearTimeout(timer);
    try { D.height = L.height || D.height; await D.refresh(full); } catch (e) { console.warn("refresh", e); }
    timer = setTimeout(() => app.refresh(), document.hidden ? 120000 : 45000); // safety net; blocks drive the real cadence
  };
  L.onBlock(() => { if (!document.hidden) app.scheduleRefresh(200); });

  // ---- holdings (balances read through the wallet, only for listed launches) ----
  app.holdings = null;
  app.loadHoldings = async (force) => {
    if (!W.connected || app.loadingHold || (!force && app.holdings && Date.now() - app.holdings.at < 60000)) return; app.loadingHold = true;
    try { // low-priority wallet reads: a user action never waits behind them
      const dero = V.toBig((await W.getBalance("", true).catch(() => ({ balance: 0 }))).balance) ?? 0n;
      const rows = [];
      for (const s of D.list()) { if (s.kind !== "launch") continue; const b = await W.getBalance(s.scid, true).catch(() => null); const units = b ? V.toBig(b.balance) : null; if (!units) continue; const q = A.launchSell(s, units) || { out: Z }; rows.push({ s, units, value: q.out }); }
      // Per quote, never summed across. q.out is atomic units of THAT market's quote asset; adding wrapped XMR
      // to DERO and dividing by DERO's decimals produced a scalar that was never a quantity of anything.
      const totals = new Map();
      for (const r of rows) {
        const k = D.qsym(r.s), cur = totals.get(k) || { units: Z, dec: D.qdec(r.s) };
        cur.units += r.value;
        totals.set(k, cur);
      }
      // No cross-quote total and no cross-quote 24h change. Both were dead fields, and both were a sum of
      // wrapped XMR and DERO: a number that is not a quantity of anything and cannot be made one without a rate.
      // There is no oracle here and there must not be one, so the honest shape is per-quote totals only.
      app.holdings = { dero, rows, totals, at: Date.now() };
      D.changed();
    } finally { app.loadingHold = false; }
  };

  // ---- transactions ----
  const MAX_FEE = 100000n; // 1 DERO: a node or wallet that estimates more than this is refused, never signed
  let inFlight = false;    // one transaction at a time: the sheet closes before the wallet answers
  const arg = ([name, type, value]) => ({ name, datatype: type, value: type === "U" ? BigInt(value) : value });
  const short = (m) => (m || "").split("\n")[0].slice(0, 160);
  const isScid = (x) => typeof x === "string" && /^[0-9a-f]{64}$/.test(x);
  // opts: {scid | code, entry, args:[[name,type,value]], dero, asset:{scid,amount}, fee (skip estimate), describe}
  app.tx = async (opts) => {
    if (!W.connected) { await app.connectWallet(); if (!W.connected) return null; }
    // local sanity gates: never build a transaction that references something the app cannot vouch for
    if (!opts.code && !isScid(opts.scid)) { toast("Refusing: bad contract id", "err"); return null; }
    if (opts.asset && !isScid(opts.asset.scid)) { toast("Refusing: bad asset id", "err"); return null; }
    const ring = opts.ring === 16 ? 16 : 2; // 16 only for entrypoints that pay a named address (SIGNER() is blank above 2)
    if (!opts.code && !(D.assets.has(opts.scid) || D.nfts.has(opts.scid) || D.nfas.has(opts.scid) || opts.scid === D.cfg.factory || opts.scid === D.cfg.social || (D.founders && opts.scid === D.founders.scid) || D.allVaults().some((v) => v.scid === opts.scid) || (D.cfg.chat && opts.scid === D.cfg.chat))) { toast("Refusing: contract is not a verified listing", "err"); return null; }
    // A contract whose code matched a LEGACY template hash is still listed — its code is immutable and it was
    // verified when that template was current — but it is not running the code this build ships. One kept launch
    // version refunds a buy's overshoot before it checks min_out, and the ...To wrappers turn that failure into a
    // second payout of the whole raise; those wrappers are the only entrypoint this app can fire that reaches it.
    const rec = opts.code ? null : D.assets.get(opts.scid) || D.nfas.get(opts.scid) || D.nfts.get(opts.scid);
    if (rec && rec.templateLegacy && /To$/.test(String(opts.entry || ""))) { toast("Refusing: this contract runs an earlier version of the template, and this app does not send private-mode calls to code that is not the current one", "err", 9000); return null; }
    const transfers = [];
    if (opts.dero && opts.dero > Z) {
      let dst; try { dst = await V.node.randomAddress(); } catch (e) { toast("No node answers for a burn destination: " + short(e.message), "err", 6000); return null; }
      if (!V.isAddress(dst)) { toast("Refusing: the node returned an invalid burn destination", "err"); return null; }
      transfers.push({ destination: dst, amount: 0, burn: opts.dero });
    }
    if (opts.asset && opts.asset.amount > Z) transfers.push({ scid: opts.asset.scid, amount: 0, burn: opts.asset.amount });
    const args = (opts.args || []).map(arg);
    const scRPC = opts.code ? args : [{ name: "entrypoint", datatype: "S", value: opts.entry }, ...args];
    const estRPC = opts.code ? [...args, { name: "SC_ACTION", datatype: "U", value: 1 }, { name: "SC_CODE", datatype: "S", value: opts.code }] : [...scRPC, { name: "SC_ACTION", datatype: "U", value: 0 }, { name: "SC_ID", datatype: "H", value: opts.scid }];
    const base = opts.code ? { sc: opts.code } : { scid: opts.scid };
    // opts.fee skips the estimate: the daemon estimator runs contracts at BLOCK_HEIGHT() = 0 (DECISIONS §3)
    let fees = opts.fee;
    if (!fees) {
      // the estimator takes the signer only for entrypoints that read SIGNER(); a private (ring 16) call never does,
      // and naming the payer to the node would undo the ring (review 2026-09-06)
      try { const est = await V.node.call("DERO.GetGasEstimate", Object.assign({ transfers, sc_rpc: estRPC, ringsize: ring }, ring === 2 ? { signer: W.address } : {}, base)); const gs = est && /^\d+$/.test(String(est.gasstorage)) ? BigInt(est.gasstorage) : null; if (gs === null || gs + 200n > MAX_FEE) { toast("Refusing: the node estimated a fee of " + (gs === null ? "?" : A.fmtUnits(gs + 200n, 5n, 5)) + " DERO (limit " + A.fmtUnits(MAX_FEE, 5n, 5) + ")", "err", 8000); return null; } fees = Number(gs + 200n); }
      catch (e) {
        // R-16: the daemon cannot simulate some contract contents at all (graviton refuses a tree name that starts
        // with ':'); an install is then sent with a size-based fee (measured storage gas is the code size + ~350)
        if (opts.code && /Bucket cannot start/.test(String(e.message))) { const gs = BigInt(opts.code.length) + 512n; if (gs > MAX_FEE) { toast("Refusing: the contract is too large to fee without an estimate", "err", 8000); return null; } fees = Number(gs); toast("The node cannot estimate this install; sending with a size-based fee of " + A.fmtUnits(gs, 5n, 5) + " DERO", "", 6000); }
        else { toast((opts.describe || "Transaction") + " would fail: " + short(e.message), "err", 7000); return null; }
      }
    }
    if (!Number.isInteger(fees) || fees < 0 || BigInt(fees) > MAX_FEE) { toast("Refusing: bad fee", "err"); return null; }
    if (inFlight) { toast("A transaction is already in progress", "err"); return null; }
    if (W.unanswered && W.unanswered.size) { toast("A transaction is still waiting for the wallet's answer: check the wallet before sending another", "err", 8000); return null; }
    inFlight = true;
    try {
    // outcome probe: the balance the caller says must move (asset up for a buy, down for a sell), read before
    const b0 = opts.outcome ? V.toBig(((await W.getBalance(opts.outcome.scid, true).catch(() => null)) || {}).balance) : null;
    // state probe (C1b): contract keys that must change when the call executes, read before and re-read after
    const readProbe = async () => (opts.probe && isScid(opts.probe.scid) ? (await V.node.getSC(opts.probe.scid, { keys: opts.probe.keys }).catch(() => null)) : null);
    const p0 = await readProbe();
    const params = Object.assign({ transfers, sc_rpc: scRPC, ringsize: ring, fees }, base);
    let r;
    try { toast("Confirm in your wallet… network fee " + A.fmtUnits(BigInt(fees), 5n, 5) + " DERO" + (ring > 2 ? " · ring size " + ring : ""), "", 4000); r = await W.transfer(params); }
    catch (e) { toast(W.isDenied(e) ? "Cancelled in wallet" : "Wallet: " + short(e.message), "err", 6000); return null; }
    const txid = r && r.txid;
    if (!isScid(txid)) { toast("Wallet returned an unexpected transaction id", "err", 8000); return null; }
    app.lastTxid = txid; // the headless test reads the ring of the last transaction from the node
    const what = opts.describe || "Transaction";
    toast(what + " sent · " + V.shortAddr(txid), "ok");
    const w = await app.waitTx(txid, !!opts.code);
    if (w.state === "timeout") { toast(what + " is not in a block after 4.5 minutes: check the wallet history before sending it again", "err", 9000); app.scheduleRefresh(300); return null; }
    if (w.state === "reverted") { toast(what + " was included but did not execute: only the network fee was spent", "err", 8000); app.scheduleRefresh(300); return null; }
    // inclusion is not execution: a reverted call is mined too. When the caller named a balance that must move,
    // read it back from the wallet once the wallet has the block; otherwise say "included", not "confirmed".
    let executed = true, known = !!opts.code;
    if (opts.outcome && b0 !== null) { const b1 = await app.balanceAfter(opts.outcome.scid, w.height); if (b1 !== null) { known = true; executed = opts.outcome.expect === "up" ? b1 > b0 : b1 < b0; } }
    // a probe with a check answers "did OUR call execute" (the keys alone can move for anyone's call)
    if (!known && p0) { const p1 = await readProbe(); if (p1) { known = true; executed = opts.probe.check ? await Promise.resolve(opts.probe.check(p0, p1)).catch(() => false) : opts.probe.keys.some((k) => (p1.vals[k] || "") !== (p0.vals[k] || "")); } }
    if (!executed) { toast(what + " was included but did not execute: your balance did not move, only the network fee was spent", "err", 9000); app.scheduleRefresh(300); return null; }
    toast(known ? what + " confirmed" : what + " included in block " + w.height, "ok", 6000);
    app.scheduleRefresh(300); if (opts.holdings) setTimeout(() => app.loadHoldings(true), 2500);
    return txid;
    } finally { inFlight = false; }
  };
  // waitTx: { state: "included" | "reverted" | "timeout", height }. An install is "reverted" when no code landed.
  app.waitTx = async (txid, install) => {
    for (let i = 0; i < 180; i++) {
      await new Promise((r) => setTimeout(r, 1500));
      try { const t = await V.node.getTx(txid); if (t && t.valid_block && !t.in_pool) { const height = Number(t.block_height) || 0; if (!install) return { state: "included", height }; const sc = await V.node.getSC(txid, { code: true }); return { state: sc.code ? "included" : "reverted", height }; } } catch (e) {}
    }
    return { state: "timeout", height: 0 };
  };
  // the wallet's view of a balance once it has scanned the block that holds the transaction (null if unreadable)
  // null (unknown) unless the wallet has scanned that block: a balance read from a lagging wallet says nothing
  // about the trade, and "did not execute" must never rest on it (review 2026-09-06)
  app.balanceAfter = async (scid, height) => {
    let synced = false;
    for (let i = 0; i < 40 && !synced; i++) {
      try { const h = await W.call("GetHeight"); synced = Number(h && h.height) >= height; } catch (e) { return null; }
      if (!synced) await new Promise((r) => setTimeout(r, 1500));
    }
    if (!synced) return null;
    const b = await W.getBalance(scid, true).catch(() => null);
    return b ? V.toBig(b.balance) : null;
  };
  // priv = { dest }: private mode. The …To entrypoints pay dest and never read SIGNER(), so the call goes out at
  // ring 16 and the payer is one of 16 accounts. dest is public. Only a trade paid to this wallet can be verified
  // by its balance; a trade paid elsewhere is verified by inclusion alone and never enters this wallet's P&L.
  app.trade = async (s, side, amount, minOut, publish, q, priv) => {
    if (minOut <= Z || minOut < A.minOut(q.out, MAX_SLIP)) { toast("Refusing: minimum received is below the 10% slippage cap", "err"); return; }
    const dest = priv && priv.dest ? String(priv.dest).trim() : null;
    if (priv && !V.isAddress(dest)) { toast("Private mode needs a valid address to receive at", "err"); return; }
    if (priv && q.refund > Z) { toast("Refusing: this buy overshoots the curve and the refund would go to the receive address, not back to you: lower the amount", "err", 8000); return; }
    const toMe = !dest || V.canonAddress(dest) === W.canon;
    const opts = { scid: s.scid, entry: (side === 1 ? "SwapDeroForAsset" : "SwapAssetForDero") + (dest ? "To" : ""),
      args: [...(dest ? [["dest", "S", dest]] : []), ["min_out", "U", minOut], ["publish", "U", publish]],
      describe: (side === 1 ? "Buy " : "Sell ") + s.ticker + (dest && !toMe ? " → " + V.shortAddr(dest) : ""), holdings: toMe, ring: dest ? 16 : 2 };
    if (toMe) opts.outcome = { scid: s.asset, expect: side === 1 ? "up" : "down" }; // the asset balance must move, or the swap reverted
    // A buy pays the market's QUOTE. It used to always send native DERO, so on a wXMR or wZEC market the
    // contract's own stray-DERO guard rejected the call and the trade reverted every time — the user filled in
    // an amount, saw a quote, signed, paid gas and got nothing back but a revert. The sell leg already branched
    // on the asset; the buy leg has to branch on the quote the same way.
    if (side === 1) { if (s.quote) opts.asset = { scid: s.quote, amount }; else opts.dero = amount; }
    else opts.asset = { scid: s.asset, amount };
    const txid = await app.tx(opts);
    if (txid && toMe) D.addLocalTrade({ scid: s.scid, asset: s.asset, side, in: amount.toString(), out: q.out.toString(), ts: S.now(), txid, published: !!publish, dec: s.dec.toString() });
  };
  app.setFollow = async (addr, on) => { if (!D.cfg.social) return toast("Social contract not configured"); if (!V.canonAddress(addr)) return toast("Bad address", "err"); const ok = await app.tx({ scid: D.cfg.social, entry: on ? "Follow" : "Unfollow", args: [["target", "S", addr]], describe: on ? "Follow" : "Unfollow", probe: { scid: D.cfg.social, keys: ["fc_" + W.canon] } }); if (ok) { await D.loadSocial(W.canon).catch(() => {}); router.dispatch(); } };

  // ---- P&L (average cost). Two sources: trades made from this device (they carry a txid) and trades the connected
  // address published on chain; a published trade that matches a local one (side, asset, amounts) is the same trade
  // and is counted once. Every figure in byAsset[asset] (cost, realised, unreal) is atomic units of that market's
  // own quote, and nothing here is summed across assets: two quotes are two currencies, so the portfolio screen
  // groups by quote and divides by the quote's decimals there, where the quote is known.
  app.pnl = (rows) => {
    const local = D.localTrades(), me = W.canon, key = (t) => [t.side, t.asset, String(t.in), String(t.out)].join("|"), seen = new Set(local.map(key));
    const chain = me ? D.tradesOf(me).map((t) => ({ side: t.side, asset: t.asset.asset, in: t.in.toString(), out: t.out.toString(), ts: t.ts, onchain: true })).filter((t) => !seen.has(key(t))) : [];
    const trades = [...local, ...chain].sort((x, y) => (x.ts || 0) - (y.ts || 0)); const byAsset = {};
    for (const t of trades) { const b = (byAsset[t.asset] = byAsset[t.asset] || { units: Z, cost: Z, realised: Z, n: 0 }); b.n++; const inn = BigInt(t.in), out = BigInt(t.out); if (t.side === 1) { b.units += out; b.cost += inn; } else { const avg = b.units > Z ? b.cost * inn / b.units : Z; b.realised += out - avg; b.cost -= avg > b.cost ? b.cost : avg; b.units = b.units > inn ? b.units - inn : Z; } }
    for (const [asset, b] of Object.entries(byAsset)) {
      const row = rows.find((r) => r.s.asset === asset); b.unreal = Z;
      if (row && b.units > Z) b.unreal = row.value * (b.units > row.units ? row.units : b.units) / (b.units || 1n) - b.cost;
    }
    return { count: trades.length, chainCount: chain.length, byAsset };
  };
  // holdings value over time: for each token, the units actually held at each of its price points (from the trade
  // ledger: device + published trades, in time order) × the price at that point; the per-token series are then merged
  // on one timeline where every token carries its last value forward, so a quiet market never reads as zero.
  // Tokens with holdings but no known trade history (received, airdropped) count their current units throughout.
  app.equityPoints = (rows) => {
    const local = D.localTrades(), me = W.canon, ledger = {};
    const push = (asset, ts, side, inn, out) => { (ledger[asset] = ledger[asset] || []).push({ ts, d: side === 1 ? BigInt(out) : -BigInt(inn) }); };
    const seen = new Set(local.map((t) => [t.side, t.asset, String(t.in), String(t.out)].join("|")));
    for (const t of local) push(t.asset, t.ts, t.side, t.in, t.out);
    if (me) for (const t of D.tradesOf(me)) { const k = [t.side, t.asset.asset, t.in.toString(), t.out.toString()].join("|"); if (!seen.has(k)) push(t.asset.asset, t.ts, t.side, t.in, t.out); }
    const series = [], stamps = new Set();
    for (const r of rows) {
      const lg = (ledger[r.s.asset] || []).sort((x, y) => x.ts - y.ts); let held = Z, li = 0; const out = [];
      for (const p of r.s.points) {
        if (lg.length) { while (li < lg.length && lg[li].ts <= p.ts) { held += lg[li].d; li++; } if (held < Z) held = Z; } else held = r.units;
        out.push({ ts: p.ts, v: p.ra > Z && held > Z ? (Number(held) / 10 ** Number(r.s.dec)) * A.priceOf(r.s, p) : 0 }); stamps.add(p.ts);
      }
      series.push(out);
    }
    let ts = [...stamps].sort((x, y) => x - y); const step = Math.ceil(ts.length / 600); if (step > 1) ts = ts.filter((t, i) => i % step === 0 || i === ts.length - 1);
    const idx = series.map(() => 0), last = series.map(() => 0), pts = [];
    for (const t of ts) { let total = 0; for (let k = 0; k < series.length; k++) { const sr = series[k]; while (idx[k] < sr.length && sr[idx[k]].ts <= t) { last[k] = sr[idx[k]].v; idx[k]++; } total += last[k]; } pts.push({ ts: t, v: total }); }
    return pts;
  };

  // ---- first run ----
  app.intro = () => {
    const step = (n, t, d) => h("div", { class: "step" }, h("div", { class: "n" }, n), h("div", null, h("b", null, t), h("div", { class: "sub" }, d)));
    openSheet(h("div", { class: "intro-mark" }), h("h2", { class: "grad", style: "font-size:26px" }, "Welcome to DMZ"), h("div", { class: "sub mt8" }, "A social exchange that runs entirely on the DERO chain."),
      h("div", { class: "intro mt" },
        step("1", "Your node, your wallet, nothing else", "There is no server and no account. Prices, trades and profiles are read from the contracts by your own node."),
        step("2", "Trading is pseudonymous, not private", "Balances are encrypted, and a trade only reaches the feed if you switch on “Post this trade”. The contract still records its reserves at the end of every block they move in, so the size of the trade is readable by anyone whether you post it or not, and the address that paid a ring-size-2 call can be computed from the block. Private mode hides the payer, not the trade."),
        step("3", "Every listing is verified", "The app checks each contract's code against the shipped templates before it shows it. Warnings appear on thin or taxed markets before you confirm.")),
      button("Got it", "primary wide mt", () => { app.saveSettings({ seen: true }); closeSheet(); }, { id: "introOk" }));
  };

  // ---- boot ----
  app.init = async () => {
    U.$$(".tab").forEach((t) => (t.onclick = () => router.go(t.dataset.tab)));
    U.$("#brand").onclick = () => router.go("home");
    const gs = U.$("#gsearch"); if (gs) gs.oninput = () => { if (!(router.current && router.current.name === "markets")) router.go("markets"); setTimeout(() => V.markets && V.markets.search(gs.value), 0); };
    if (app.settings.reduceMotion) document.documentElement.classList.add("reduce");
    document.documentElement.dataset.pointer = window.matchMedia && window.matchMedia("(pointer: coarse)").matches ? "touch" : "fine";
    app.walletUI();
    router.dispatch();
    if (!app.settings.seen) app.intro();
    await app.connectNode();
    if (!D.cfg.factory) toast("No factory configured: run tools/simdeploy or edit config.js", "err", 9000);
    app.refresh(true);
    document.addEventListener("visibilitychange", () => { if (!document.hidden) app.scheduleRefresh(300); });
    D.loadSocial(null).catch(() => {});
    if (app.settings.autoConnect) W.connect().catch(() => {}).finally(app.walletUI);
    window.addEventListener("hashchange", () => { const t = U.$("#tape"); if (t) t.hidden = !(router.current && (router.current.name === "home" || router.current.name === "markets")) || !t.querySelector(".tape-track"); });
  };
  window.addEventListener("DOMContentLoaded", app.init);
})();
