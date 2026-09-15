// DMZ sentiment.js — community sentiment on a market: what addresses said about it, read from contracts/rate.bas.
// It is a count, never DMZ's verdict, and the screen must keep saying so:
//   - anyone can vote, including from many addresses: the contract counts addresses, not people;
//   - under N votes only the counts are shown, because a single address would otherwise read as 100%;
//   - neither side wears a warning colour: this is proportional information, not a flag;
//   - voting is a ring-size-2 call, so the voter's address is public in that transaction.
// Nothing is read and nothing is signed unless the node's copy of the contract hashes to templates/rate.bas, the
// template this build ships.
//
// Interface, for the market page and discovery:
//   V.sentiment.of(scid)       -> Promise<{ bull, sus, n, mine? } | null>; null: no verified contract, a bad id or no
//                                 answer from the node. `mine` (0, 1 or 2) only while a wallet is connected.
//   V.sentiment.widget(scid)   -> an element that draws itself and refreshes on each new block while it is on screen.
//   V.sentiment.summary(b, s)  -> the display rule, pure: { bull, sus, n, pct: null | { bull, sus, bw } }.
// A consumer shows the counts as counts: it must not hide, sort down or badge a market by them.
(function () {
  const V = window.Veil, D = V.data, B = BigInt;
  const N = 5, HEX64 = /^[0-9a-f]{64}$/, isNum = (x) => typeof x === "string" && /^\d{1,20}$/.test(x);
  const BULL = "Bullish", SUS = "Suspicious / Scam", MANY = "Anyone can vote, including from many addresses.";

  // ---- the display rule ----
  // Counts always. Percentages only from N votes, rounded to add up to 100; a side with any vote never reads 0% (it
  // reads <1%), so the other never reads 100%. `bw` is the exact bullish share, for the bar.
  const summary = (bull, sus) => {
    const b = Math.max(0, Math.floor(Number(bull) || 0)), s = Math.max(0, Math.floor(Number(sus) || 0)), n = b + s;
    if (n < N) return { bull: b, sus: s, n, pct: null };
    const r = Math.round((100 * b) / n);
    const txt = (x, cnt, other) => (cnt > 0 && x === 0 ? "<1%" : other > 0 && x === 100 ? ">99%" : x + "%");
    return { bull: b, sus: s, n, pct: { bull: txt(r, b, s), sus: txt(100 - r, s, b), bw: (100 * b) / n } };
  };

  const S = (V.sentiment = { N, summary, BULL, SUS, MANY, verified: "", fee: null });

  // ---- the code check ----
  // The configured id is trusted only once its code equals the shipped template byte for byte (by hash), and it
  // says it is rate v1. A mismatch is remembered for that id; a node that did not answer is asked again next time.
  let checking = null, bad = "";
  const load = () => {
    const scid = D.cfg && D.cfg.rate;
    if (typeof scid !== "string" || !HEX64.test(scid) || scid === bad) return Promise.resolve(false);
    if (S.verified === scid) return Promise.resolve(true);
    if (!checking) checking = (async () => {
      const [r, t] = await Promise.all([V.node.getSC(scid, { code: true, keys: ["kind", "v", "fee"] }),
        fetch("templates/rate.bas").then((x) => (x.ok ? x.text() : ""), () => "")]);
      if (!t) return false;
      const ok = !!r.code && (await V.sha256Hex(r.code)) === (await V.sha256Hex(t)) &&
        V.hexToStr(r.vals.kind || "") === "rate" && r.vals.v === "1" && isNum(r.vals.fee);
      if (!ok) { bad = scid; return false; }
      S.fee = B(r.vals.fee); S.verified = scid; return true;
    })().catch(() => false).finally(() => { checking = null; });
    return checking;
  };

  // ---- data ----
  // Two counters and, with a wallet, the wallet's own standing vote: one read by exact key, nothing scanned.
  S.of = async (scid) => {
    const t = String(scid || "").toLowerCase();
    if (!HEX64.test(t)) return null;
    try {
      if (!(await load())) return null;
      const me = V.wallet && V.wallet.canon, keys = ["b_" + t, "s_" + t];
      if (me) keys.push("x_" + t + "_" + me);
      const r = await V.node.getSC(S.verified, { keys });
      const u = (k) => (isNum(r.vals[k]) ? Number(r.vals[k]) : 0);
      const out = { bull: u(keys[0]), sus: u(keys[1]) };
      out.n = out.bull + out.sus;
      if (me) { const m = u(keys[2]); out.mine = m === 1 || m === 2 ? m : 0; }
      return out;
    } catch (e) { return null; }
  };

  // ---- the vote ----
  // Vote(target H, v U): 1 bullish, 2 suspicious, 0 clear, sent through V.app.tx and its gates: the network gate, the
  // allowlist (app/allow.js: this contract, for Vote, once its code matches templates/rate.bas), a bounded network fee,
  // one transaction at a time. The voter's own standing vote is read back, because inclusion is not execution.
  const send = async (t, v) => {
    if (!(await load())) { V.ui.toast("Refusing: this network's sentiment contract could not be checked against the code this build ships. Nothing was sent.", "err", 9000); return false; }
    const fee = v ? S.fee || 0n : 0n;
    return !!(await V.app.tx({ scid: S.verified, entry: "Vote", args: [["target", "H", t], ["v", "U", B(v)]], dero: fee, describe: v ? "Vote " + (v === 1 ? BULL : SUS) : "Clear your vote",
      probe: { scid: S.verified, keys: ["b_" + t, "s_" + t], check: async () => { const d = await S.of(t); return !!d && d.mine === v; } } }));
  };

  // ---- the widget ----
  const live = new Set();
  let listening = false;
  S.widget = (scid) => {
    const U = V.ui, h = U.h, t = String(scid || "").toLowerCase();
    const el = h("section", { class: "senti", "aria-label": "Community sentiment" });
    let last = "";
    const head = () => h("div", { class: "senti-h" }, h("h3", null, "Community sentiment"), h("span", { class: "tiny dim2" }, "what addresses said, not DMZ's verdict"));
    const draw = (d) => {
      if (d === undefined) return U.render(el, head(), h("div", { class: "note mt8" }, "Reading the votes…"));
      if (d === null) return U.render(el, head(), h("div", { class: "note mt8" },
        "Not available: this build names no sentiment contract, its code on your node is not the code this build ships, or your node did not answer. Nothing to read and nothing to sign."));
      const m = summary(d.bull, d.sus), mine = d.mine || 0, fee = S.fee || 0n;
      const vote = (v) => async () => { if (await send(t, v)) refresh(); };
      const btn = (v, label) => U.button(label, "small ghost", vote(v), { "aria-pressed": mine === v ? "true" : "false", disabled: mine === v ? true : null });
      U.render(el, head(),
        h("div", { class: "senti-c" },
          h("span", null, BULL + " ", h("b", null, String(m.bull)), m.pct ? " · " + m.pct.bull : ""),
          h("span", null, SUS + " ", h("b", null, String(m.sus)), m.pct ? " · " + m.pct.sus : ""),
          h("span", { class: "dim2" }, m.n + (m.n === 1 ? " vote" : " votes"))),
        m.pct
          ? h("div", { class: "senti-bar", role: "img", "aria-label": m.pct.bull + " " + BULL + ", " + m.pct.sus + " " + SUS },
            h("i", { class: "sb", style: "width:" + m.pct.bw.toFixed(2) + "%" }), h("i", { class: "ss", style: "width:" + (100 - m.pct.bw).toFixed(2) + "%" }))
          : h("div", { class: "tiny dim2 mt8" }, "Percentages are shown from " + N + " votes. Under " + N + ", only the counts: one address would otherwise read as 100%."),
        h("div", { class: "tiny senti-many" }, MANY),
        h("div", { class: "senti-btns" }, btn(1, BULL), btn(2, SUS), U.button("Clear", "small ghost", vote(0), mine ? { title: "Clear my vote" } : { disabled: true })),
        h("div", { class: "note mt8" }, "Voting is a ring-size-2 call, so your address is public in that transaction. A vote costs " +
          (fee > 0n ? V.amm.fmtUnits(fee, 5n, 5) + " DERO, paid to the DMZ treasury," : "only the network fee,") +
          " and changing it costs the same again; clearing costs only the network fee. Nobody else can change or remove your vote, including whoever deployed DMZ."));
    };
    const refresh = async () => { const d = await S.of(t); const k = JSON.stringify(d); if (k !== last) { last = k; draw(d); } };
    draw(undefined);
    refresh();
    // a new block is the only thing that can move a count: one listener refreshes every widget still on screen, and
    // forgets one that has left it
    live.add({ el, refresh });
    if (!listening && D.onChange) {
      listening = true;
      D.onChange(() => { for (const w of live) if (w.el.isConnected) w.refresh(); else live.delete(w); });
    }
    return el;
  };
})();
