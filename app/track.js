// DMZ track.js — the stage tracker both swap screens share (docs/SWAP_STATUS.md). Unpaired, a screen builds the
// DERO-side stages it reads from the chain and marks the Monero or Zcash side as visible only to the user's swap
// program. Paired with that program over loopback, it shows the full stages the program reports. One renderer draws
// both. The pairing code is kept in this page's memory and in sessionStorage, never in localStorage, and it is sent
// to nothing but 127.0.0.1. A status answer is shown only after every field is checked; an answer that names a
// secret, or is for another program, network or contract, is refused whole.
(function () {
  const V = window.Veil, U = V.ui, N = Number, { h, button, toast } = U;
  const BLOCK = { DERO: 18, Monero: 120, Zcash: 75 }, STABLE = 8, POLL = 10000;
  const STATES = ["done", "active", "waiting", "skipped", "failed"], OUTCOMES = ["", "swapped", "refunded", "punished", "reclaimed", "lost"];
  const SECRET = /secret|preimage|priv|seed|mnemonic|spend.?key|view.?key|channel.?key|pairing/i;
  const hex64 = (x) => /^[0-9a-f]{64}$/.test(x || "");
  const int = (x) => (typeof x === "number" && Number.isSafeInteger(x) && x >= 0 ? x : typeof x === "string" && /^\d{1,15}$/.test(x) ? N(x) : null);
  const amount = (x) => (typeof x === "string" && /^\d{1,30}$/.test(x) ? BigInt(x) : typeof x === "number" && Number.isSafeInteger(x) && x >= 0 ? BigInt(x) : null);
  const clean = (x, n) => (typeof x === "string" ? U.clean(x, n) : "");
  const when = (x) => { const t = typeof x === "string" ? Date.parse(x) : NaN; return isNaN(t) ? null : t; };
  // a field whose NAME says secret refuses the whole answer: a status answer carries none (docs/SWAP_STATUS.md §4)
  const leak = (o, d = 0) => { if (!o || typeof o !== "object" || d > 4) return ""; for (const k of Object.keys(o)) { if (SECRET.test(k)) return k; const x = leak(o[k], d + 1); if (x) return x; } return ""; };

  // want: { program, network, contracts: [ids this build names] }. Returns { ok, s } or { why }.
  function parse(j, want) {
    if (!j || typeof j !== "object" || Array.isArray(j)) return { why: "The answer is not a JSON object." };
    if (j.v !== 1) return { why: "The answer is not version 1 of the status format." };
    const bad = leak(j); if (bad) return { why: "The answer carries a field named “" + clean(bad, 40) + "”. A status answer never carries a secret, so nothing from it is shown." };
    if (j.program !== want.program) return { why: "The answer is from " + (clean(j.program, 20) || "an unnamed program") + ", not " + want.program + "." };
    const dir = { maker: "withdrawal", taker: "deposit" }[j.role];
    if (!dir || j.direction !== dir) return { why: "The answer's role and direction do not agree." };
    if (String(j.network || "").toLowerCase() !== String(want.network || "").toLowerCase()) return { why: "Your swap program is on " + (clean(j.network, 20) || "no named network") + " and this build is for " + want.network + "." };
    if (!want.contracts.includes(j.contract)) return { why: "Your swap program is using a contract this build does not name, so its answer is not shown here." };
    if (!OUTCOMES.includes(j.outcome)) return { why: "The answer's outcome is not one this app knows." };
    if (!Array.isArray(j.steps) || j.steps.length > 32) return { why: "The answer's steps are not a list of at most 32." };
    const steps = j.steps.map((s) => { s = s && typeof s === "object" ? s : {}; const dl = s.deadline && typeof s.deadline === "object" ? s.deadline : null;
      return { id: clean(s.id, 40), label: clean(s.label, 120) || "(unnamed step)", chain: BLOCK[s.chain] ? s.chain : "?", state: STATES.includes(s.state) ? s.state : "unreadable",
        txid: hex64(s.txid) ? s.txid : "", conf: int(s.conf), need: int(s.need), deadline: dl && BLOCK[dl.chain] ? { chain: dl.chain, height: int(dl.height), eta: when(dl.eta) } : null }; });
    return { ok: true, s: { program: j.program, role: j.role, direction: dir, network: clean(j.network, 20), contract: j.contract, lock: int(j.lock), asset: hex64(j.asset) ? j.asset : j.asset === "" ? "" : null,
      dero: amount(j.deroAmount), foreign: amount(j.foreignAmount), fee: amount(j.fee), phase: clean(j.phase, 40), outcome: j.outcome, next: clean(j.next, 240), updated: when(j.updated), steps } };
  }

  // ---- words ----
  const dur = (s) => (s < 90 ? "1 min" : s < 5400 ? Math.round(s / 60) + " min" : s < 172800 ? Math.round(s / 360) / 10 + " h" : Math.round(s / 8640) / 10 + " days");
  const conf = (s) => (s.conf === null ? "" : s.need === null ? s.conf + " confirmations" : s.conf + " of " + s.need);
  // A deadline is a block. On DERO it is counted from the tip this app read; on Monero and Zcash, which this app
  // cannot read, the time is the swap program's own estimate and says so. The height is what the chain's rule reads.
  function left(dl, tip, now) {
    if (!dl || dl.height === null) return "";
    const at = dl.chain + " block " + dl.height;
    if (dl.chain === "DERO" && tip > 0) { const d = dl.height - tip; return d <= 0 ? at + " · passed" : at + " · in " + d + " block" + (d === 1 ? "" : "s") + " · about " + dur(d * BLOCK.DERO); }
    if (dl.eta !== null) { const s = Math.round((dl.eta - now) / 1000); return s <= 0 ? at + " · passed, by your swap program's estimate" : at + " · about " + dur(s) + " left, by your swap program's estimate"; }
    return at;
  }
  // the step that is running now, or failing that the next one waiting: its deadline is the one that counts down
  const current = (steps) => steps.find((s) => s.state === "active") || steps.find((s) => s.state === "waiting") || null;
  // the first deadline from the current step on, among steps still to happen: the one that counts down
  const nextDeadline = (steps) => { const c = current(steps); return c ? steps.slice(steps.indexOf(c)).find((s) => s.deadline && s.deadline.height !== null && (s.state === "active" || s.state === "waiting")) || null : null; };
  // DEPOSIT and WITHDRAWAL in the user's words: a deposit gives XMR or ZEC and receives the DERO-side asset
  const dirText = (direction, foreign, dero) => direction === "deposit" ? "Deposit: you give " + foreign + " and receive " + dero : "Withdrawal: you give " + dero + " and receive " + foreign;

  // ---- the tracker: every state has a shape as well as a word ----
  const MARK = { done: "✓", active: "●", waiting: "○", skipped: "–", failed: "✕", unseen: "?", unreadable: "?" };
  const WORD = { done: "done", active: "now", waiting: "waiting", skipped: "skipped", failed: "failed", unseen: "not visible here", unreadable: "unreadable" };
  const COL = { done: "var(--up)", active: "var(--acc)", failed: "var(--down)" };
  function render(steps, tip) {
    const now = Date.now(), c = nextDeadline(steps);
    return [c ? h("div", { class: "note mt8" }, h("b", null, "Next deadline: "), c.label + " · " + left(c.deadline, tip, now)) : null,
      h("ol", { class: "track", style: "list-style:none;margin:8px 0 0;padding:0" }, steps.map((s) => h("li", { class: "kv tr-" + s.state, style: "justify-content:flex-start;align-items:flex-start" },
        h("span", { "aria-hidden": "true", style: "flex:0 0 22px;height:22px;display:inline-flex;align-items:center;justify-content:center;border-radius:50%;font-size:12px;border:1.5px " + (s.state === "waiting" || s.state === "unseen" ? "dashed" : "solid") + " currentColor;color:" + (COL[s.state] || "var(--fg3)") + (s.state === "active" ? ";background:color-mix(in srgb,var(--acc) 18%,transparent)" : "") }, MARK[s.state] || "?"),
        h("div", { style: "min-width:0;flex:1" }, h("div", { style: "font-weight:" + (s.state === "active" ? 700 : 500) }, s.label),
          h("div", { class: "sub tiny" }, [s.chain, WORD[s.state] || s.state, conf(s), left(s.deadline, tip, now), s.txid ? "tx " + V.shortAddr(s.txid) : ""].filter(Boolean).join(" · "))))))];
  }

  // ---- a lock's depth on DERO, against the stable depth of 8 both swap programs wait for ----
  // Read by asking the node whether the lock's key existed 8 blocks ago, then at each block since; a lock found
  // stable is never asked about again. Unreadable (a node without that history) is null, never 0.
  const depths = new Map();
  async function depth(scid, key, tip) {
    const c = depths.get(scid + key); if (c) return c.stable ? STABLE : tip - c.at;
    if (!(tip > STABLE)) return null;
    const has = async (t) => (await V.node.getSC(scid, { keys: [key], topoheight: t })).vals[key] !== undefined;
    try {
      if (await has(tip - STABLE)) { depths.set(scid + key, { stable: true }); return STABLE; }
      for (let d = STABLE - 1; d >= 0; d--) if (await has(tip - d)) { depths.set(scid + key, { at: tip - d }); return d; }
    } catch (e) {}
    return null;
  }

  // ---- pairing with the user's own swap program (docs/SWAP_STATUS.md) ----
  const P = {}, KEY = (p) => "veil.pair." + p;
  function pair(program) {
    if (!P[program]) { let s = null; try { s = JSON.parse(sessionStorage.getItem(KEY(program)) || "null"); } catch (e) {}
      const ok = s && Number.isInteger(s.port) && s.port >= 1024 && s.port <= 65535 && /^[0-9a-f]{32}$/.test(s.code || "");
      P[program] = { port: ok ? s.port : 0, code: ok ? s.code : "", last: null, at: 0, err: "", timer: 0 }; }
    return P[program];
  }
  async function poll(program, want) {
    const p = pair(program); if (!p.code) return;
    const ctl = new AbortController(), t = setTimeout(() => ctl.abort(), 5000);
    try {
      const r = await fetch("http://127.0.0.1:" + p.port + "/v1/status", { headers: { Authorization: "Bearer " + p.code }, cache: "no-store", credentials: "omit", referrerPolicy: "no-referrer", redirect: "error", signal: ctl.signal });
      if (r.status === 401) { p.err = "Your swap program refused the pairing code. Pair again with the code it printed when it started."; p.last = null; return; }
      if (!r.ok) { p.err = "Your swap program answered HTTP " + r.status + "."; return; }
      const txt = await r.text(); let j = null;
      if (txt.length > 65536) { p.err = "The answer is too large to be a status."; return; }
      try { j = JSON.parse(txt); } catch (e) { p.err = "The answer is not JSON."; return; }
      const x = parse(j, want); if (!x.ok) { p.err = x.why; p.last = null; return; }
      p.last = x.s; p.at = Date.now(); p.err = "";
    } catch (e) { p.err = "Nothing answered at 127.0.0.1:" + p.port + ". Is your swap program running with -status 127.0.0.1:" + p.port + "?"; }
    finally { clearTimeout(t); }
  }
  // every 10 s while open() says the screen is showing; never again once the swap has an outcome
  function watch(program, want, open, redraw) {
    const p = pair(program); clearInterval(p.timer);
    if (!p.code || (p.last && p.last.outcome)) return;
    const tick = async () => { if (!open()) return clearInterval(p.timer); await poll(program, want); if (p.last && p.last.outcome) clearInterval(p.timer); if (open()) redraw(); };
    p.timer = setInterval(tick, POLL); tick();
  }
  // fmt: { foreign(v), dero(asset, v) } — the screen's own amount words
  function panel(program, want, open, redraw, fmt, tip) {
    const p = pair(program), name = program === "xmrswap" ? "Monero" : "Zcash";
    if (!p.code) {
      const port = h("input", { inputmode: "numeric", placeholder: "port", autocomplete: "off", "aria-label": "Status port", style: "flex:0 0 90px;min-width:0" }),
        code = h("input", { class: "mono", placeholder: "pairing code (32 hex)", autocomplete: "off", spellcheck: "false", "aria-label": "Pairing code", style: "flex:1;min-width:0" });
      return h("div", { class: "card mt" }, h("div", { class: "label" }, "Pair with your swap program"),
        h("div", { class: "note mt8" }, "The " + name + " side of a swap is visible only to your own " + program + ". Started with -status 127.0.0.1:PORT, it answers this page on your machine only, with the stages of its swap; " +
          "it prints a one-time pairing code, and without that code it answers nothing. The code stays in this tab (sessionStorage), never in localStorage, and is sent only to 127.0.0.1. " +
          "The answer never carries a key or a secret (docs/SWAP_STATUS.md)."),
        h("div", { class: "row mt8", style: "gap:8px" }, port, code, button("Pair", "small", () => {
          const n = N(port.value.trim()), c = code.value.trim().toLowerCase();
          if (!Number.isInteger(n) || n < 1024 || n > 65535) return toast("The port is a number from 1024 to 65535.", "err");
          if (!/^[0-9a-f]{32}$/.test(c)) return toast("The pairing code is the 32 hex characters your swap program printed.", "err");
          Object.assign(p, { port: n, code: c, last: null, err: "" });
          try { sessionStorage.setItem(KEY(program), JSON.stringify({ port: n, code: c })); } catch (e) {}
          port.value = code.value = ""; watch(program, want, open, redraw); redraw();
        })));
    }
    const s = p.last;
    return h("div", { class: "card mt" }, h("div", { class: "between" }, h("span", { class: "label" }, "Your swap program · 127.0.0.1:" + p.port),
      button("Unpair", "small ghost", () => { clearInterval(p.timer); delete P[program]; try { sessionStorage.removeItem(KEY(program)); } catch (e) {} redraw(); })),
      p.err ? h("div", { class: "errbox mt8" }, p.err) : null,
      s ? [h("div", { class: "mt8" }, h("b", null, dirText(s.direction, s.foreign === null ? "an unreadable amount of " + (program === "xmrswap" ? "XMR" : "ZEC") : fmt.foreign(s.foreign), s.dero === null || s.asset === null ? "an unreadable amount" : fmt.dero(s.asset, s.dero))),
          h("div", { class: "sub tiny" }, (s.lock === null ? "No lock yet" : "Lock #" + s.lock) + " · " + (s.role === "maker" ? "you are the maker" : "you are the taker") + (s.fee === null ? "" : " · fee " + fmt.foreign(s.fee) + ", paid by the maker") + (s.outcome ? " · ended: " + s.outcome : ""))),
        s.next ? h("div", { class: "note mt8" }, h("b", null, "What happens next: "), s.next) : null, render(s.steps, tip),
        h("div", { class: "sub tiny mt8" }, "Reported by your swap program" + (s.updated ? " at " + new Date(s.updated).toLocaleTimeString() : "") + (s.outcome ? ". The swap has ended, so it is not asked again." : ". Asked again every 10 s while this screen is open."))]
        : p.err ? null : h("div", { class: "sub mt8" }, "Asking your swap program…"));
  }
  const paired = (program) => { const p = pair(program); return p.code && p.last ? p.last : null; };

  V.track = { parse, left, conf, current, nextDeadline, dirText, render, depth, panel, watch, paired, BLOCK, STABLE };
})();
