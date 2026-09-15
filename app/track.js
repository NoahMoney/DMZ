// DMZ track.js — the stage tracker. It draws a sequence of steps, each with a mark whose shape carries its state as
// well as the word, its confirmations and its deadline, as a list (render) or on one line (chips); the Deposit &
// withdraw screen (app/move.js) draws a vault deposit's and a withdrawal's stages with it, every one read from the
// vault contract. parse() is the app's reading of a swap program's status answer (docs/SWAP_STATUS.md): no screen
// pairs with a swap program any more, but tools/xmrswap and tools/zecswap still check every answer they can serve
// against it (tests/js/statuscheck.mjs), so it stays, unchanged, and is refused whole on anything it cannot read.
(function () {
  const V = window.Veil, U = V.ui, N = Number, { h } = U;
  const BLOCK = { DERO: 18, Monero: 120, Zcash: 75 }, STABLE = 8;
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
  // cannot read, the time is the estimate it was given and says so. The height is what the chain's rule reads.
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

  // ---- stage chips: the same steps on one line, for a list. The mark's shape carries the state, the word is read
  // out, and the full label is the chip's title. A step with no short name is named from its id or its label. ----
  const CAP = { dero: "DERO", xmr: "XMR", zec: "ZEC" };
  const shortOf = (s) => s.short || (s.id ? s.id.split("_").map((w) => CAP[w] || w).join(" ").replace(/^./, (c) => c.toUpperCase()) : s.label.length > 24 ? s.label.slice(0, 23) + "…" : s.label);
  const chips = (steps) => h("ol", { class: "swp-chips", "aria-label": "Stages" }, steps.map((s) => h("li", { class: "swp-chip tr-" + s.state, title: s.chain + ": " + s.label + " · " + (WORD[s.state] || s.state) },
    h("span", { class: "m", "aria-hidden": "true" }, MARK[s.state] || "?"), shortOf(s), h("span", { class: "swp-sr" }, ": " + (WORD[s.state] || s.state)))));
  const LEGEND = "✓ done · ● now · ○ waiting · – skipped · ✕ failed · ? not visible here";
  const due = (steps, tip) => { const c = nextDeadline(steps); return c ? "Next deadline: " + shortOf(c) + " · " + left(c.deadline, tip, Date.now()) : ""; };

  V.track = { parse, left, conf, current, nextDeadline, dirText, render, chips, due, shortOf, LEGEND, WORD, BLOCK, STABLE };
})();
