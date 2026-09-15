// DMZ wrapui.js — the Wrap screen's parts, split out of wrap.js (DOC size, MT-E10): amounts in the vault's own
// units, the network marks, the settlement panel and walkthrough, full-length copyable strings, and the screen a
// build with no vault shows instead. Published as V.wrapui for wrap.js, which loads after it.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data;
  const { h, button, kv, router, toast } = U;

  // ---- formatting ----

  const dec = () => { const d = (D.vault && D.vault.asset && D.vault.asset.dec) || 0; return Number.isInteger(d) && d >= 0 && d <= 18 ? d : 0; };
  const sym = () => U.clean((D.vault && D.vault.asset && D.vault.asset.sym) || "units", 12);
  // base units are exact; the pretty form is a courtesy. When no decimals were recorded, show the exact integer.
  const amt = (u, places) => (dec() === 0 ? String(u) + " base units" : A.fmtUnits(u, dec(), places === undefined ? 6 : places) + " " + sym());
  // A protocol constant (minimum deposit, minimum burn, the fee) is a rule, and a rule is printed to the last
  // digit: the "<0.00000001" fmtUnits gives a swallowed balance is honest for a balance and wrong for a threshold
  // someone has to meet exactly — on a 12-decimal vault the shipped minimum burn sits at the ninth place.
  const exact = (u) => amt(u, dec());
  // DERO blocks are about 18 seconds; say "about" and never round a wait down
  const blocksAway = (n) => {
    if (n <= 0) return "now";
    const s = n * 18;
    if (s < 90) return "about " + n + " block" + (n === 1 ? "" : "s") + " (~" + s + "s)";
    if (s < 5400) return "about " + Math.ceil(s / 60) + " min";
    if (s < 172800) return "about " + Math.ceil(s / 3600) + " h";
    return "about " + Math.ceil(s / 86400) + " days";
  };

  // ---- network grammar ----
  // A network is a two-letter mono label with a hairline leading edge in its tint (.net / .net.xmr / .net.zec),
  // so an amount, an address and a settlement each say plainly which chain they belong to. The tint is never a
  // fill, and the chain is read from the deployment record rather than guessed.
  const netOf = (a) => {
    const s = (String(a.sym || "") + " " + String(a.chain || "")).toUpperCase();
    if (/XMR|MONERO/.test(s)) return ["XM", "net xmr"];
    if (/ZEC|ZCASH/.test(s)) return ["ZC", "net zec"];
    return [(String(a.sym || "").toUpperCase().replace(/[^A-Z0-9]/g, "") + "??").slice(0, 2), "net"];
  };
  const mark = (code, cls, txt) => h("span", { class: "row tight" }, h("span", { class: cls }, code), h("span", { class: "label" }, txt));
  const arrow = () => h("span", { class: "dim2" }, "→");
  // state is stated, not glowed: a 6px square and precise words
  const flag = (tint, word) => h("span", { class: "row tight" }, h("i", { class: "dot", style: "background:var(--" + tint + ")" }), h("span", { class: "label" }, word));
  // a key/value row whose value is markup rather than a string
  const kvn = (k, ...v) => h("div", { class: "kv" }, h("span", { class: "dim" }, k),
    h("span", { class: "row tight", style: "flex-wrap:wrap;justify-content:flex-end" }, ...v));
  // The settlement panel, and the only shape a settlement is ever described in on this screen: which way it
  // moves, by what mechanism, who is holding the coins meanwhile, and what state it is in. Custody is a line of
  // its own because a federation holding the reserve is the fact a badge would hide.
  const panel = (c, from, to, custody, status) => [
    kvn("Settlement", from, arrow(), to),
    kv("Method", c.fed),
    kv("Custody", custody),
    kv("Status", status),
  ];
  // A string the user has to read in full — a deposit address, a destination, a settlement txid, the vault id —
  // is never truncated and never boxed: a label above, the mono value below, all of it selectable, and a click
  // copies it. Truncating any of these would hide the part that differs from a look-alike.
  // Wrapping is a sequence with waits in it, so it is shown as one: what is finished, what is happening now,
  // what is waiting on the federation, and what has not started. A user should never have to infer which.
  //   done — this has happened and cannot un-happen
  //   now  — your move; the action lives on this row
  //   wait — the chain or the federation is working; nothing for you to do
  //   todo — not reachable yet
  const steps = (items) => h("ol", { class: "steps" }, items.filter(Boolean).map((it, i) =>
    h("li", { class: "step " + it.state },
      h("span", { class: "n mono" }, it.state === "done" ? "✓" : String(i + 1)),
      h("div", { class: "grow" },
        h("div", { class: "between" },
          h("b", { class: "t" }, it.title),
          h("span", { class: "label sst" }, it.state === "done" ? "Done" : it.state === "now" ? "Your move" : it.state === "wait" ? "Waiting" : "")),
        it.detail ? h("div", { class: "d note" }, it.detail) : null,
        it.body || null))));

  const full = (label, value, copied) => h("div", { class: "stack", style: "gap:3px;margin-top:12px" },
    h("div", { class: "eyebrow" }, label),
    h("button", {
      class: "mono sub", type: "button", "aria-label": "Copy " + label,
      style: "display:block;width:100%;text-align:left;word-break:break-all;line-height:1.5",
      onclick: () => { navigator.clipboard && navigator.clipboard.writeText(value).then(() => toast(copied, "ok")); },
    }, value));

  // ---- no vault: the mainnet shape until a vault is installed ----
  // XMR and ZEC come in and go out only through a vault, so a build that names none has no deposit or withdrawal of
  // either. The screen says so, and why, and draws no address, no form and nothing to sign.
  const NODEP = "Deposits and withdrawals of XMR and ZEC are not available on this network.";
  const noVault = () => [
    h("div", { class: "warnbox" }, h("b", null, NODEP), h("div", { class: "sub mt8" }, V.notice.NOWRAP + " Wrapped XMR and ZEC are 1:1 claims on real Monero and Zcash held by a federation: " +
      "named people, any k of whom could move every coin. They exist only where a build names that federation's vault, and this one names none.")),
    h("div", { class: "note mt8" }, "Every market this app creates on this network is priced in DERO. Nothing on this screen asks you to sign."),
  ];

  V.wrapui = { dec, sym, amt, exact, blocksAway, netOf, mark, arrow, flag, kvn, panel, steps, full, noVault };
})();
