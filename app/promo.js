// DMZ promo.js — paid visibility (contracts/promo.bas): the "promote" route, and V.promo, which the discovery and market
// pages read. Everything is read from the contract; nothing is kept anywhere else.
//
// The contract: one asset per auction (its quote: DERO, or a DERO-chain asset such as wXMR), K slots and an epoch of E
// blocks, a minimum bid and a minimum increment, fixed at install. Bids placed in epoch N (ep_ = N) compete for display in
// N + 1; when N ends they ARE N + 1's winners, with no call. A new bid takes a free slot, or beats the lowest standing bid
// by the increment and displaces it (it goes back to its bidder in the same call). Final bids are proceeds;
// ClaimProceeds, which anyone may send, pays them only to the treasury, in the quote.
//
// What this app adds is the only judgement anywhere in it: a winner is SHOWN only when its target is a listed, verified
// DMZ market or NFA (D.assets, D.nfas: code hash and treasury checked). The contract takes a bid for any 32-byte id and
// nobody can remove one. "Promoted" means paid, never endorsed or verified. An auction in an asset this build cannot name
// (not DERO, not a vault it ships) is not shown at all.
//
// Exposed: V.promo.slots() -> [{ slot, target, bid, epoch }] the current epoch's shown winners, best first (slot 0 is
// the top), one per target; V.promo.statusOf(scid) -> null (no verified promotion contract) or { promoted, slot, bid,
// epoch, eligible, next: null | { rank, bid, epoch } }; V.promo.asset() -> { sym, dec, label } of the bids. Bids are
// BigInt atomic units of that asset; epochs BigInt.
(function () {
  const V = window.Veil, U = V.ui, D = V.data, A = V.amm, B = BigInt;
  const { h, render, button, kv, toast, router } = U;
  const Z = 0n, BLOCK_S = 18, FEE = 1000, MAXK = 10, SLOTK = ["tg_", "bd_", "by_", "ep_", "sq_", "cl_"];
  const NUM = ["slots", "epoch", "minbid", "incbps", "sq", "pr", "fc", "bi", "rf", "sx", "initd"];
  const isNum = (v) => typeof v === "string" && /^\d+$/.test(v), isId = (v) => typeof v === "string" && /^[0-9a-f]{64}$/.test(v);
  let st = null, why = "Reading the promotion contract…", busy = false, loading = null, lastH = -1;

  // ---- the contract's arithmetic, exactly ----
  const bps = (x, f) => (x / 10000n) * f + ((x % 10000n) * f) / 10000n;
  // best first: bid down, then the earliest placed (a lower sq_) first, as the contract breaks ties
  const rank = (l) => l.slice().sort((a, b) => (a.bid !== b.bid ? (a.bid > b.bid ? -1 : 1) : a.sq < b.sq ? -1 : a.sq > b.sq ? 1 : 0));
  const epochOf = (s, height) => B(Math.max(0, Math.floor(Number(height) || 0))) / s.e;
  // what a new bid must be at least: minbid while a slot of the bank is free, else lowest + bps(lowest, inc), at least + 1
  function minNextOf(s, cur) {
    const live = s.slots.filter((x) => x.ep === cur);
    if (live.length < s.k) return { need: s.minbid, full: false, low: null };
    let low = live[0];
    for (const x of live) if (x.bid < low.bid || (x.bid === low.bid && x.sq > low.sq)) low = x;
    let need = low.bid + bps(low.bid, s.inc); if (need <= low.bid) need = low.bid + 1n;
    return { need: need < s.minbid ? s.minbid : need, full: true, low };
  }
  const claimableOf = (s, cur) => s.slots.reduce((a, x) => (x.ep < cur && !x.cl ? a + x.bid : a), s.pr);
  const heldOf = (s) => s.slots.reduce((a, x) => (x.cl ? a : a + x.bid), Z);
  // slot i's keys, as DERO.GetSC returns them: numbers as decimal strings, strings (the raw target and bidder) as hex
  function parse(vals, k) {
    const out = [];
    for (let i = 0; i < 2 * k; i++) {
      const g = (p) => vals[p + i], by = D.rawToAddr(typeof g("by_") === "string" ? g("by_") : "");
      if (!isId(g("tg_")) || !by || !["bd_", "ep_", "sq_", "cl_"].every((p) => isNum(g(p)))) continue;
      out.push({ i, target: g("tg_"), bid: B(g("bd_")), by, ep: B(g("ep_")), sq: B(g("sq_")), cl: g("cl_") !== "0" });
    }
    return out;
  }
  // the auction's asset: DERO (the zero id), or a wrapped asset whose vault ships with this build; any other is not named
  function assetOf(q) {
    if (!isId(q)) return null;
    if (/^0{64}$/.test(q)) return { id: q, sym: "DERO", dec: 5n, label: "DERO", dero: true };
    const v = D.allVaults().find((x) => x.scid === q), sym = v && U.clean(v.sym || "", 8);
    return sym && Number.isInteger(v.dec) && v.dec >= 0 && v.dec <= 18 ? { id: q, sym: "w" + sym, coin: sym, dec: B(v.dec), label: "w" + sym + " (" + sym + ", wrapped 1:1)", dero: false } : null;
  }

  async function load() {
    const scid = D.cfg.promo;
    if (!isId(scid || "")) { st = null; why = "This build names no promotion contract."; return; }
    if ((await D.verify(scid)) !== "promo") { st = null; why = "The promotion contract this build names does not match the shipped promo template, so nothing it holds is shown."; return; }
    const r = await V.node.getSC(scid, { keys: [...NUM, "treasury", "quote"] }), n = (x) => B(r.vals[x]);
    const k = isNum(r.vals.slots) ? Number(r.vals.slots) : 0;
    if (!NUM.every((x) => isNum(r.vals[x])) || k < 1 || k > MAXK || n("epoch") === Z) { st = null; why = "The promotion contract's terms could not be read."; return; }
    const asset = assetOf(r.vals.quote || "");
    if (!asset) { st = null; why = "The promotion contract takes bids in an asset this build cannot name, so none of its winners is shown."; return; }
    const keys = []; for (let i = 0; i < 2 * k; i++) for (const p of SLOTK) keys.push(p + i);
    const s = { scid, k, asset, e: n("epoch"), minbid: n("minbid"), inc: n("incbps"), pr: n("pr"), fc: n("fc"), bi: n("bi"), rf: n("rf"), sx: n("sx"), treasury: D.rawToAddr(r.vals.treasury || ""), slots: parse(await D.fetchKeys(scid, keys), k) };
    // the check every listing passes (feed.js): what it pays must be a treasury this build ships, or the factory's
    const reg = D.registry, ship = (D.cfg.treasuries || []).map((a) => V.canonAddress(a)).filter(Boolean);
    const ok = reg && reg.treasuries && reg.treasuries.size ? reg.treasuries : new Set(ship);
    if (!s.treasury || !ok.has(s.treasury)) { st = null; why = "The promotion contract pays " + (s.treasury ? V.shortAddr(s.treasury) : "an address that could not be read") + ", which is not a treasury this build lists, so none of its winners is shown."; return; }
    st = s; why = ""; V.promo.verified = scid;
  }
  // one read per block, after the data layer's refresh; a change is announced so the pages that show winners redraw
  function sync() {
    const hgt = V.live.height || 0; if (loading || (hgt === lastH && st)) return; lastH = hgt;
    const before = sigOf();
    loading = load().catch((e) => { console.warn("promo", e); }).finally(() => { loading = null; if (sigOf() !== before) D.changed(); });
  }
  const sigOf = () => (st ? [st.fc, st.bi, st.pr].concat(st.slots.map((x) => x.i + ":" + x.bid + ":" + x.ep + ":" + x.cl)).join("|") : why) + "@" + (st ? epochOf(st, V.live.height) : "");

  const cur = () => (st ? epochOf(st, V.live.height) : Z);
  // a target this app can name: a listed, verified pool or launch (D.assets holds only those), or a verified NFA
  function eligible(id) {
    const a = isId(id) && D.assets.get(id), f = isId(id) && D.nfas.get(id);
    if (a && !a.untrusted) return { ok: true, name: U.clean(a.ticker || "", 10) + " · " + U.clean(a.name || "", 32), kind: a.kind === "pool" ? "pool" : "token market" };
    if (f && !f.untrusted) return { ok: true, name: U.clean(f.name || "NFA", 40), kind: "NFA" };
    return { ok: false, why: isId(id) ? "not a listed, verified DMZ market or NFA" : "not a 64-character contract id" };
  }
  function slots() {
    if (!st) return [];
    const c = cur(), seen = new Set(), out = [];
    for (const w of rank(st.slots.filter((x) => x.ep === c - 1n))) { if (seen.has(w.target) || !eligible(w.target).ok) continue; seen.add(w.target); out.push({ slot: out.length, target: w.target, bid: w.bid, epoch: c }); }
    return out;
  }
  function statusOf(id) {
    if (!st || !isId(id)) return null;
    const c = cur(), s = slots().find((x) => x.target === id), nx = rank(st.slots.filter((x) => x.ep === c)), ni = nx.findIndex((x) => x.target === id);
    return { promoted: !!s, slot: s ? s.slot : null, bid: s ? s.bid : null, epoch: c, eligible: eligible(id).ok, next: ni < 0 ? null : { rank: ni, bid: nx[ni].bid, epoch: c + 1n } };
  }

  // ---- sending: app.tx admits only listings it knows, so the promotion contract, verified here, is sent to directly
  // with the same gates. The fee is fixed: the node's estimator runs a contract at BLOCK_HEIGHT() 0, where Bid's epoch
  // check refuses (the DVM suite measures Bid at 237 to 329 storage gas, ClaimProceeds at 132 to 219).
  async function send(entry, args, pay, what, done) {
    const W = V.wallet, app = V.app;
    if (!W.connected) { await app.connectWallet(); if (!W.connected) return null; }
    { const g = await app.netGate(); if (g) { toast(g, "err", 10000); return null; } }
    if (!st || st.scid !== D.cfg.promo || V.promo.verified !== st.scid) { toast("Refusing: the promotion contract did not verify", "err"); return null; }
    if (busy || (W.unanswered && W.unanswered.size)) { toast("A transaction is already in progress", "err"); return null; }
    busy = true;
    try {
      const transfers = [];
      if (pay > Z && st.asset.dero) { const dst = await V.node.randomAddress().catch(() => null); if (!V.isAddress(dst || "")) { toast("No node answers for a burn destination", "err"); return null; } transfers.push({ destination: dst, amount: 0, burn: pay }); }
      else if (pay > Z) transfers.push({ scid: st.asset.id, amount: 0, burn: pay });
      const sc_rpc = [{ name: "entrypoint", datatype: "S", value: entry }, ...args.map(([n, t, v]) => ({ name: n, datatype: t, value: t === "U" ? B(v) : v }))];
      const before = st;
      toast("Confirm in your wallet… network fee " + A.fmtUnits(B(FEE), 5n, 5) + " DERO", "", 4000);
      let r; try { r = await W.transfer({ transfers, sc_rpc, ringsize: 2, fees: FEE, scid: st.scid }); } catch (e) { toast(W.isDenied(e) ? "Cancelled in wallet" : "Wallet: " + U.clean(String(e.message), 160), "err", 6000); return null; }
      const txid = r && r.txid; if (!isId(txid)) { toast("Wallet returned an unexpected transaction id", "err", 8000); return null; }
      V.app.lastTxid = txid; toast(what + " sent · " + V.shortAddr(txid), "ok");
      const w = await app.waitTx(txid, false);
      if (w.state !== "included") { toast(what + (w.state === "timeout" ? " is not in a block after 4.5 minutes: check the wallet before sending again" : " did not execute: only the network fee was spent"), "err", 9000); return null; }
      await load().catch(() => {}); const ok = !!st && done(before, st);
      toast(ok ? what + " confirmed" : what + " was included but did not execute: the DVM hands back what you attached, and only the network fee was spent", ok ? "ok" : "err", 9000);
      D.changed(); return ok ? txid : null;
    } finally { busy = false; }
  }
  const me = () => V.wallet.canon || null;
  const mine = (s, id, c) => s.slots.find((x) => x.by === me() && x.target === id && x.ep === c);
  const bid = (id, v, raise, c) => send("Bid", [["target", "H", id], ["ep", "U", c], ["raise", "U", raise ? 1n : Z]], v, (raise ? "Raise" : "Bid") + " for epoch " + (c + 1n),
    (a, b) => b.bi >= a.bi + v && !!mine(b, id, c) && (!raise || mine(b, id, c).bid > mine(a, id, c).bid));
  const claim = () => send("ClaimProceeds", [], Z, "Send the proceeds to the treasury", (a, b) => b.fc > a.fc);

  // ---- the screen ----
  // every digit the asset has: a bid is money, and "<0.000001" is not an amount anyone can check against the chain
  const amt = (x) => (st ? A.fmtUnits(x, st.asset.dec, Number(st.asset.dec)) + " " + st.asset.sym : String(x));
  const dur = (blocks) => { const s = Number(blocks) * BLOCK_S, d = Math.floor(s / 86400), hh = Math.floor((s % 86400) / 3600), m = Math.round((s % 3600) / 60); return "about " + (d ? d + "d " : "") + (d || hh ? hh + "h " : "") + m + "m"; };
  const tag = () => h("span", { class: "pill promo-tag", title: "Paid placement: one of this epoch's highest bids. Not an endorsement and not a check of the project." }, "Promoted");
  function row(x, n, shown) {
    const e = eligible(x.target), own = x.by === me();
    return h("div", { class: "kv promo-row" },
      h("div", { class: "row tight grow", style: "min-width:0" }, h("span", { class: "promo-rank num" }, String(n + 1)),
        h("div", { class: "grow ellipsis" }, e.ok ? h("b", null, e.name) : h("b", { class: "mono" }, x.target.slice(0, 12) + "…"),
          // the id as well as the name: two listings can share a ticker, and the id is what the bid was for
          h("div", { class: "sub tiny ellipsis" }, (e.ok ? e.kind + " " + V.shortAddr(x.target) : "not shown: " + e.why) + " · bid by " + (own ? "you" : V.shortAddr(x.by))))),
      h("div", { class: "row tight" }, h("b", { class: "num" }, amt(x.bid)), shown && e.ok ? tag() : null));
  }
  function list(items, shown, empty) { return items.length ? h("div", { class: "mt8" }, rank(items).map((x, i) => row(x, i, shown))) : h("div", { class: "sub mt8" }, empty); }

  function form(el, s, c, pre) {
    const mn = minNextOf(s, c), left = (c + 1n) * s.e - B(V.live.height || 0);
    const tIn = h("input", { placeholder: "a market or NFA contract id (64 hex)", value: pre || "", autocomplete: "off", spellcheck: "false", "aria-label": "target contract id", class: "mono" });
    const aIn = h("input", { inputmode: "decimal", value: A.fmtUnits(mn.need, s.asset.dec, 18).replace(/,/g, ""), "aria-label": "bid in " + s.asset.sym });
    const pick = h("select", { "aria-label": "pick a listed market or NFA", onchange: () => { tIn.value = pick.value; upd(); } }, h("option", { value: "" }, "Pick a listed market or NFA…"),
      [...D.assets.values()].filter((a) => !a.untrusted).slice(0, 60).map((a) => h("option", { value: a.scid }, eligible(a.scid).name)), [...D.nfas.values()].slice(0, 30).map((f) => h("option", { value: f.scid }, eligible(f.scid).name)));
    const ack = h("input", { type: "checkbox", "aria-label": "I understand" }), ackRow = h("label", { class: "row tight mt8 promo-ack" }, ack, h("span", null, "I understand this app will not show it if it wins, and the bid is paid all the same."));
    const note = h("div", { class: "note mt8", role: "status" }), btn = button("Bid", "primary wide mt", null, { disabled: true });
    let raise = false;
    const upd = () => {
      const id = tIn.value.trim().toLowerCase(), e = eligible(id), m = isId(id) && mine(s, id, c), other = isId(id) && s.slots.some((x) => x.ep === c && x.target === id && x.by !== me());
      raise = !!m; ackRow.hidden = !isId(id) || e.ok;
      const v = A.parseUnits(aIn.value, s.asset.dec) || Z, okAmt = raise ? v > Z : v >= mn.need;
      render(note, !isId(id) ? "Enter or pick the contract id to promote." : e.ok ? "A listed, verified " + e.kind + ": " + e.name + "." : h("span", { class: "promo-warn" }, "Not a listed, verified DMZ market or NFA. The contract takes a bid for any id, but this app shows a winner only when it can verify it: this one would not be shown."),
        raise ? " You stand for it in this epoch with " + amt(m.bid) + ": this adds to that bid (any amount)."
          : " New bids start at " + amt(mn.need) + (mn.full ? ", " + Number(s.inc) / 100 + "% over the lowest standing bid, which it displaces and refunds." : ", the minimum: a slot is free."),
        other && !raise ? " Someone else already bids for this id: a second bid takes another slot, and the winner is shown once." : "",
        left <= 12n ? h("b", null, " The epoch ends in " + left + " blocks. A bid that lands after it is refused and refunded; only the fee is spent.") : "");
      btn.textContent = (raise ? "Raise by " : "Bid ") + amt(v) + " for epoch " + (c + 1n);
      btn.disabled = !isId(id) || !okAmt || (!e.ok && !ack.checked);
    };
    btn.onclick = async () => { const id = tIn.value.trim().toLowerCase(), v = A.parseUnits(aIn.value, s.asset.dec) || Z; if (await bid(id, v, raise, c)) draw(el); };
    tIn.oninput = aIn.oninput = ack.onchange = upd; upd();
    return h("div", { class: "card mt" }, h("div", { class: "label" }, "Bid for epoch " + (c + 1n)), h("div", { class: "stack mt8" }, pick, tIn, h("label", { class: "field" }, "Your bid, in " + s.asset.sym, aIn)), ackRow, note, btn,
      h("div", { class: "sub tiny mt8" }, "Sent from your wallet at ring size 2, with a network fee of " + A.fmtUnits(B(FEE), 5n, 5) + " DERO. If it is outbid before the epoch ends, it comes back to you, in " + s.asset.sym + ", in the outbidding transaction."));
  }
  function yours(s, c) {
    if (!me()) return h("div", { class: "card mt" }, h("div", { class: "label" }, "Your bids"), h("div", { class: "sub mt8" }, "Connect a wallet to see and raise your bids."));
    const l = rank(s.slots.filter((x) => x.by === me()));
    const st2 = (x) => (x.ep === c ? "standing for epoch " + (c + 1n) + ", rank " + (rank(s.slots.filter((y) => y.ep === c)).indexOf(x) + 1) + " of " + s.k : x.ep === c - 1n ? "on display now (epoch " + c + ")" : "finished (epoch " + (x.ep + 1n) + ")");
    return h("div", { class: "card mt" }, h("div", { class: "label" }, "Your bids"),
      l.length ? l.map((x) => h("div", { class: "kv" }, h("div", { class: "grow ellipsis" }, h("b", null, eligible(x.target).ok ? eligible(x.target).name : x.target.slice(0, 12) + "…"), h("div", { class: "sub tiny" }, st2(x))), h("b", { class: "num" }, amt(x.bid))))
        : h("div", { class: "sub mt8" }, "No bid of yours is in the contract. A bid that was outbid is gone from it: it came back to your wallet in the transaction that displaced it."));
  }
  function rules(el, s, c) {
    const cl = claimableOf(s, c), share = D.founders && D.founders.share !== undefined ? Number(D.founders.share) / 100 + "%" : "the pass contract's share", a = s.asset;
    return h("div", { class: "card mt" }, h("div", { class: "label" }, "The rules, and where the money goes"),
      h("ul", { class: "promo-rules" },
        h("li", null, s.k + " highlighted slots per epoch of " + s.e + " blocks (" + dur(s.e) + "). Bids placed during an epoch compete for the next one; when it ends, the " + s.k + " highest are its winners, with no call from anyone."),
        h("li", null, "A bid is at least " + amt(s.minbid) + ". Once every slot is taken, a new bid must beat the lowest by " + Number(s.inc) / 100 + "% and takes its slot; the outbid bid goes back to its bidder in that same transaction. You can raise your own bid; ties keep the earlier bid."),
        h("li", null, a.dero ? "Bids in DERO." : ["Bids in " + a.label + ". It is a DERO-chain asset, so this contract holds each bid and refunds it itself; the " + a.coin + " behind it is held by the vault's federation, not by this contract (",
          h("button", { class: "linkish", type: "button", onclick: () => router.go("notice") }, "the dev's message"), " names who holds it)."],
          " One auction takes one asset: bids in different assets cannot be ranked against each other without a price oracle, and there is none."),
        h("li", null, "When an epoch ends its winning bids are final: proceeds. Anyone may send ClaimProceeds, which pays them to the treasury and to nobody else, in " + a.sym + ". The treasury's bot then deposits " + share + " of them into the Founders Pass, in " + a.sym + ", as with every other fee asset; the bot makes that deposit, and no contract forces it."),
        h("li", null, "Nobody moderates it. The contract takes a bid for any id and nothing can remove one. This app shows a winner only when it is a listed, verified DMZ market or NFA, labelled Promoted: paid for, not endorsed, and not a check of the project.")),
      kv("Bid in", amt(s.bi)), kv("Refunded to outbid bidders", amt(s.rf)), kv("Paid to the treasury", amt(s.fc)), s.sx > Z ? kv("Sent with no signer, counted as proceeds", amt(s.sx)) : null,
      // the balance is pr + every unswept bid; what is not claimable yet is the standing bids of this epoch
      kv("Held for standing bids", amt(s.pr + heldOf(s) - cl)), kv("Claimable now", amt(cl)),
      cl > Z ? button("Send " + amt(cl) + " to the treasury", "small mt8", async () => { if (await claim()) draw(el); }) : null,
      kv("Treasury", V.shortAddr(s.treasury)), kv("Contract", h("span", { class: "mono" }, V.shortAddr(s.scid))));
  }
  function draw(el, pre) {
    const top = [h("h1", { class: "mt" }, "Promote"), h("div", { class: "sub mb" }, "Paid visibility. Projects bid for a few highlighted slots per epoch; the highest bids win the next epoch. What wins is shown as Promoted: paid, never endorsed or verified.")];
    if (!st) return render(el, top, h("div", { class: "card mt" }, h("div", { class: "sub" }, why)));
    const c = cur(), s = st, hgt = B(V.live.height || 0), end = (c + 1n) * s.e, left = end - hgt, nx = s.slots.filter((x) => x.ep === c), mn = minNextOf(s, c);
    const hi = nx.reduce((m, x) => (x.bid > m ? x.bid : m), Z), stat = (l, v, sub) => h("div", { class: "stat" }, h("div", { class: "l" }, l), h("div", { class: "v num" }, v), sub ? h("div", { class: "sub tiny" }, sub) : null);
    render(el, top, h("div", { class: "pill promo-asset" }, "Bids in " + s.asset.label),
      h("div", { class: "stats4 mt promo-clock" }, stat("Epoch", String(c), "ends at block " + end), stat("Time left", left + " blocks", dur(left) + " at " + BLOCK_S + " s a block"),
        stat("Highest bid", hi ? amt(hi) : "none yet", "for epoch " + (c + 1n)), stat("Minimum next bid", amt(mn.need), mn.full ? "beats the lowest by " + Number(s.inc) / 100 + "%" : nx.length + " of " + s.k + " slots taken")),
      h("div", { class: "promo-grid" },
        h("div", null,
          h("div", { class: "card mt" }, h("div", { class: "between" }, h("b", null, "Promoted now · epoch " + c), tag()), list(s.slots.filter((x) => x.ep === c - 1n), true, "No bid won this epoch.")),
          h("div", { class: "card mt" }, h("b", null, "Standing for epoch " + (c + 1n)), h("div", { class: "sub tiny" }, "Live bids: the " + s.k + " highest when block " + end + " arrives are the winners."), list(nx, false, "No bid yet: the minimum takes a slot.")),
          yours(s, c)),
        h("div", null, form(el, s, c, isId(pre || "") ? pre : ""), rules(el, s, c))));
  }

  router.on("promote", (el, param) => {
    let key = null;
    const redraw = () => { const k = sigOf() + "|" + me() + "|" + D.assets.size + "|" + D.nfas.size + "|" + (V.live.height || 0); if (k === key || (document.activeElement && el.contains(document.activeElement) && /INPUT|SELECT/.test(document.activeElement.tagName))) return; key = k; draw(el, param); };
    draw(el, param); load().then(redraw, redraw); D.onChange(() => router.current && router.current.name === "promote" && redraw());
  });
  D.onChange(sync, true);
  V.promo = { verified: null, load, slots, statusOf, eligible, get state() { return st; }, why: () => why, cur, asset: () => (st ? { sym: st.asset.sym, dec: st.asset.dec, label: st.asset.label } : null),
    minNext: () => (st ? minNextOf(st, cur()) : null), claimable: () => (st ? claimableOf(st, cur()) : null),
    _: { bps, rank, minNextOf, claimableOf, heldOf, parse, epochOf, assetOf, set(s) { st = s; why = s ? "" : why; } } };
})();
