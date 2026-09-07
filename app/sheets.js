// DMZ sheets.js — liquidity (pools) and staking (launch tmode 3) sheets. Both are execution instruments and are
// composed like the trade sheet: an amount, one estimated reading, the terms as hairline rows, one action.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, { h, render, button, kv, openSheet, closeSheet, toast } = U;
  const Z = 0n, me = () => V.wallet.canon;
  const Sh = (V.sheets = {});
  // every entrypoint in this file pays the caller, so it reads SIGNER() and the call goes out at ring size 2
  const RINGNOTE = "Ring size 2: the contract pays the signer, so anyone can compute which address made this call.";
  const AMM = "constant-product AMM";
  const slipBps = () => V.app.settings.slippage || 100;
  const confirmNote = () => h("div", { class: "note mt8", style: "text-align:center" }, "Your wallet shows the exact transaction and asks you to confirm.");
  // the estimated reading: a label, a number, one line of plain words. No box around the rows beneath it.
  const reading = (label) => { const v = h("div", { class: "v num" }, "0"), n = h("div", { class: "note mt8" }); return { el: h("div", { class: "outline mt" }, h("div", { class: "l" }, label), v, n), v, n }; };
  const rows = () => h("div", { class: "stack", style: "gap:0" });
  const amountBox = (input, unit) => h("div", { class: "amount mt8" }, input, h("span", { class: "unit" }, unit));

  Sh.liquidity = (s) => {
    let mode = "add";
    const body = h("div", { class: "mt" });
    const draw = () => {
      const slip = slipBps(), detail = rows();
      if (mode === "add") {
        const dIn = h("input", { inputmode: "decimal", placeholder: "0", autocomplete: "off", "aria-label": "DERO to deposit" }), aIn = h("input", { inputmode: "decimal", placeholder: "0", autocomplete: "off", "aria-label": s.ticker + " to deposit" }), out = reading("LP units minted (estimated)"); let d = Z, a = Z, q = null;
        const btn = button("Add liquidity", "primary wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "AddLiquidity", args: [["min_lp", "U", A.minOut(q.lp, V.app.settings.slippage || 100)]], dero: d, asset: { scid: s.asset, amount: a }, describe: "Add liquidity", holdings: true, probe: { scid: s.scid, keys: ["lp"] } }); }, { disabled: true });
        const upd = () => { d = A.parseUnits(dIn.value, 5n) || Z; if (s.lp > Z) { a = A.poolAssetFor(s, d) || Z; aIn.value = a > Z ? A.fmtUnits(a, s.dec, Number(s.dec)).replace(/,/g, "") : ""; } else a = A.parseUnits(aIn.value, s.dec) || Z; q = d > Z && a > Z ? A.poolAdd(s, d, a) : null;
          out.v.textContent = q ? A.fmtUnits(q.lp, 5, 3) : "0";
          out.n.textContent = q ? "Share of the pool after this deposit: " + (Number(q.lp * 10000n / (s.lp + q.lp)) / 100).toFixed(2) + "%" : s.lp === Z ? "Enter both amounts. The first deposit needs at least 1 DERO." : "Enter DERO; the asset amount follows the pool ratio.";
          render(detail, q ? [kv("Minimum LP, or the call reverts", A.fmtUnits(A.minOut(q.lp, slip), 5, 3)), kv("Route", AMM), kv("Ring size", "2")] : null); btn.disabled = !q; };
        dIn.oninput = upd; aIn.oninput = upd;
        render(body, h("div", { class: "eyebrow" }, "You deposit"), amountBox(dIn, "DERO"), amountBox(aIn, s.ticker),
          s.lp === Z ? h("div", { class: "warnbox mt" }, "This is the first deposit: your two amounts set the opening price, and 1 000 LP units stay locked forever.") : null,
          out.el, detail, h("div", { class: "note mt8" }, RINGNOTE), btn, confirmNote()); upd();
      } else {
        const lIn = h("input", { inputmode: "decimal", placeholder: "0", autocomplete: "off", "aria-label": "LP units to burn" }), out = reading("You receive (estimated)"); let l = Z, q = null;
        const btn = button("Remove liquidity", "primary wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "RemoveLiquidity", args: [["min_dero", "U", A.minOut(q.d, V.app.settings.slippage || 100)], ["min_asset", "U", A.minOut(q.a, V.app.settings.slippage || 100)]], asset: { scid: s.scid, amount: l }, describe: "Remove liquidity", holdings: true, probe: { scid: s.scid, keys: ["lp"] } }); }, { disabled: true });
        const upd = () => { l = A.parseUnits(lIn.value, 5n) || Z; q = l > Z ? A.poolRemove(s, l) : null;
          out.v.textContent = q ? A.fmtDero(q.d, 5) + " DERO" : "0";
          out.n.textContent = q ? "and " + A.fmtUnits(q.a, s.dec, 6) + " " + s.ticker : l > Z ? "More than the pool can release: 1 000 LP units stay locked forever." : "Enter LP units to burn.";
          render(detail, q ? [kv("Minimum received", A.fmtDero(A.minOut(q.d, slip), 5) + " DERO · " + A.fmtUnits(A.minOut(q.a, slip), s.dec, 6) + " " + s.ticker), kv("Route", AMM), kv("Ring size", "2")] : null); btn.disabled = !q; };
        lIn.oninput = upd;
        render(body, h("div", { class: "eyebrow" }, "You withdraw"), amountBox(lIn, "LP"),
          h("div", { class: "note mt8" }, "Your LP balance is the pool's own contract id, held as an asset in your wallet."),
          out.el, detail, h("div", { class: "note mt8" }, RINGNOTE), btn, confirmNote()); upd();
      }
    };
    openSheet(h("h2", null, "Liquidity · " + s.ticker), h("div", { class: "sub mt8" }, "Half of every trade's fee stays in the pool. LP units are your claim on both reserves."),
      h("div", { class: "mt" }, U.segmented([{ v: "add", l: "Add" }, { v: "remove", l: "Remove" }], mode, (v) => { mode = v; draw(); })), body); draw();
  };

  Sh.stake = async (s) => {
    // the user's own vault entry is read by key (st_<addr>, sd_<addr>); the app never enumerates other stakers
    const mine = me() ? await D.myKeys(s.scid, me(), ["st_", "sd_"]).catch(() => ({})) : {};
    let pending = Z;
    if (mine.st) { const acc = s.sacc + (s.stot > Z ? s.tacc * 4294967296n / s.stot : Z); pending = mine.st * (acc - (mine.sd || Z)) / 4294967296n; }
    const inp = h("input", { inputmode: "decimal", placeholder: "0", autocomplete: "off", "aria-label": "amount" });
    openSheet(h("h2", null, "Stake " + s.ticker), h("div", { class: "sub mt8" }, Number(s.tax) / 100 + "% of every trade's DERO leg is shared among stakers, pro rata, block by block."),
      h("div", { class: "outline mt" }, h("div", { class: "l" }, "Pending reward"), h("div", { class: "v num" }, A.fmtDero(pending, 5) + " DERO")),
      h("div", { class: "stack mt", style: "gap:0" }, kv("Your stake", mine.st ? A.fmtUnits(mine.st, s.dec, 4) : "0"), kv("Total staked", A.fmtCompact(s.stot, s.dec)), kv("Undistributed in the contract", A.fmtDero(s.tacc, 5) + " DERO"), kv("Ring size", "2")),
      h("div", { class: "note mt8" }, RINGNOTE),
      h("div", { class: "eyebrow mt" }, "Stake or unstake"), amountBox(inp, s.ticker),
      h("div", { class: "row mt" }, button("Stake", "primary grow", () => { const a = A.parseUnits(inp.value, s.dec); if (!a) return toast("Enter an amount"); closeSheet(); V.app.tx({ scid: s.scid, entry: "Stake", args: [], asset: { scid: s.scid, amount: a }, describe: "Stake", holdings: true, probe: { scid: s.scid, keys: ["stot", "st_" + me()] } }); }),
        button("Unstake", "grow", () => { const a = A.parseUnits(inp.value, s.dec); if (!a) return toast("Enter an amount"); closeSheet(); V.app.tx({ scid: s.scid, entry: "Unstake", args: [["amt", "U", a]], describe: "Unstake", holdings: true, probe: { scid: s.scid, keys: ["stot", "st_" + me()] } }); })),
      button("Claim rewards", "wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "Claim", args: [], describe: "Claim rewards", holdings: true }); }), confirmNote());
  };
})();
