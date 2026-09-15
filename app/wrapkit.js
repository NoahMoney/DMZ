// DMZ wrapkit.js — what the Wrap screen (wrap.js) and the Deposit & withdraw guide (move.js) must say the same way:
// which chain a thing is on, a failure in words a person can act on, and the confirmation shown before any call that
// moves money. It holds no state, stores nothing and sends nothing.
(function () {
  const V = window.Veil, U = V.ui, { h, button, openSheet, closeSheet } = U;

  // XMR and ZEC must never be confusable. Each is written with its two-letter code, its chain's name and its own
  // tint (--net-xmr, --net-zec: docs/DESIGN_SYSTEM.md §Networks), and the tint never carries the meaning alone.
  const CHAIN = { xmr: ["XM", "Monero", "XMR"], zec: ["ZC", "Zcash", "ZEC"], dero: ["DR", "DERO", "DERO"] };
  const chain = (k, extra) => { const c = CHAIN[k] || CHAIN.dero; return h("span", { class: "swp-chain " + (CHAIN[k] ? k : "dero") }, h("b", { class: "mono" }, c[0]), c[1] + (extra ? " · " + extra : "")); };

  // ---- failures, in words: what happened, then what to do; what was reported sits behind "Technical detail" ----
  // The first rule whose pattern matches the reported text wins, so the specific ones come first.
  const RULES = [
    [/cancel+ed in (the |your )?wallet|denied|rejected by (the )?user/i, "You cancelled it in your wallet.", "Nothing was sent and nothing changed. Try again when you are ready."],
    [/already in progress|still waiting for the wallet/i, "Another transaction is still waiting for your wallet.", "Answer it in your wallet first, and check the wallet's history before you send this again."],
    [/wallet (is )?not connected|wallet disconnected|connect (a|your) wallet/i, "No wallet is connected.", "Connect your wallet and try again. Nothing was sent."],
    [/would fail/i, "Your node says this call would fail, so it was not sent.", "Nothing was sent. The vault may be halted or at its cap, or its state changed since this page read it: wait for the next block and look again."],
    [/did not execute|reverted|did not move/i, "The transaction was mined, but the contract refused it.", "Only the DERO network fee was spent. This page now shows what the chain holds: the vault may have halted, or its state changed first."],
    [/not in a block after/i, "The transaction is not in a block yet.", "Check your wallet's history before you send it again: a second send could be mined as well."],
    [/estimated a fee|bad fee|too large to fee/i, "The network fee would be above this app's limit, so nothing was sent.", "Nothing was sent. Try again after the next block."],
    [/insufficient|not enough|low balance/i, "Your wallet does not hold enough for this.", "Nothing was sent. DERO pays every network fee: add some, or more of the asset, then try again."],
    [/this build is for|another network|not the same network|build names/i, "Your node, your wallet and this build are not on the same DERO network.", "Nothing was signed. Point this app (Settings) and your wallet at nodes on the network this build names."],
    [/not a verified listing|bad contract id|bad asset id/i, "This app refused to call a contract it has not checked.", "Nothing was sent. Reload the page, so the contract is read and checked again."],
    [/429|too many requests/i, "Your node is turning requests away for now.", "Wait a minute, then retry."],
    [/timed? ?out|timeout|abort/i, "Your node took too long to answer.", "Check that it is running and in sync, then retry, or choose another node in Settings."],
    [/fetch|websocket|socket|econnrefused|connection|no node|not answer|offline|closed/i, "Your DERO node did not answer.", "Check that it is running and reachable, or choose another node in Settings, then retry."],
    [/sc not found|no such|not found|invalid scid/i, "Your node does not know this contract.", "It may be on another network, or still syncing. Check it, or choose another node in Settings."],
  ];
  const NONE = ["It was not sent, or it did not complete.", "Nothing is shown as done unless the chain says so. Check your wallet's history, then look at this page again after the next block."];
  const OTHER = ["Something failed that this app has no words for.", "Nothing is shown as done unless the chain says so. The technical detail is what was reported; check your wallet's history before you try again."];
  function explain(raw, title) {
    const d = U.clean(String((raw && raw.message) || raw || ""), 400), m = d ? RULES.find((x) => x[0].test(d)) : null, w = m ? [m[1], m[2]] : d ? OTHER : NONE;
    return { title: title || "", what: w[0], todo: w[1], detail: d };
  }
  const box = (e, onRetry) => h("div", { class: "errbox swp-err", role: "alert" },
    h("b", null, (e.title ? e.title + ": " : "") + e.what), h("div", { class: "mt8" }, e.todo),
    e.detail ? U.details("Technical detail", h("div", { class: "mono tiny swp-detail" }, e.detail)) : null,
    onRetry ? h("div", { class: "row mt8" }, button("Retry", "small primary", onRetry)) : null);
  // app.tx reports why it stopped only as the toast it shows; that text is read back from the page, never guessed
  async function tx(opts) {
    const t = document.getElementById("toasts"), seen = new Set(t ? t.children : []);
    const r = await V.app.tx(opts);
    if (r) return { ok: r };
    const fresh = t ? [...t.querySelectorAll(".toast.err")].filter((x) => !seen.has(x)) : [];
    return { err: explain(fresh.length ? fresh[fresh.length - 1].textContent : "", opts.describe) };
  }

  // ---- the confirmation before any call that moves money ----
  // rows: [label, value] pairs, each a fact with its chain. Back comes first, so the key a sheet focuses is not Send.
  function confirm(title, rows, note, go, onGo, onBack) {
    openSheet(h("div", { class: "swp" }, h("h2", null, title), h("div", { class: "stack mt swp-confirm" }, rows.filter(Boolean).map(([k, v]) => U.kv(k, v))),
      note ? h("div", { class: "note mt" }, note) : null,
      h("div", { class: "row mt swp-go" }, button(onBack ? "Back" : "Not now", "wide", onBack || closeSheet), button(go, "primary wide", () => { closeSheet(); onGo(); }))));
  }

  V.wrapkit = { CHAIN, chain, explain, box, tx, confirm };
})();
