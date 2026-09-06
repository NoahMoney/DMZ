// DMZ sheets.js — liquidity (pools) and staking (launch tmode 3) sheets.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, { h, render, button, kv, openSheet, closeSheet, toast } = U;
  const Z = 0n, me = () => V.wallet.canon;
  const Sh = (V.sheets = {});

  Sh.liquidity = (s) => {
    let mode = "add";
    const body = h("div", { class: "mt" });
    const draw = () => {
      if (mode === "add") {
        const dIn = h("input", { inputmode: "decimal", placeholder: "0" }), aIn = h("input", { inputmode: "decimal", placeholder: "0" }), out = h("div", { class: "card tight mt stack" }); let d = Z, a = Z, q = null;
        const btn = button("Add liquidity", "primary wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "AddLiquidity", args: [["min_lp", "U", A.minOut(q.lp, V.app.settings.slippage || 100)]], dero: d, asset: { scid: s.asset, amount: a }, describe: "Add liquidity", holdings: true, probe: { scid: s.scid, keys: ["lp"] } }); }, { disabled: true });
        const upd = () => { d = A.parseUnits(dIn.value, 5n) || Z; if (s.lp > Z) { a = A.poolAssetFor(s, d) || Z; aIn.value = a > Z ? A.fmtUnits(a, s.dec, Number(s.dec)).replace(/,/g, "") : ""; } else a = A.parseUnits(aIn.value, s.dec) || Z; q = d > Z && a > Z ? A.poolAdd(s, d, a) : null; render(out, q ? [kv("LP minted", A.fmtUnits(q.lp, 5, 3)), kv("Share after", (Number(q.lp * 10000n / (s.lp + q.lp)) / 100).toFixed(2) + "%"), s.lp === Z ? h("div", { class: "note" }, "First deposit sets the price; 1000 LP units stay locked forever.") : null] : h("div", { class: "note" }, s.lp === Z ? "Enter both amounts (first deposit needs at least 1 DERO)." : "Enter DERO; the asset amount follows the pool ratio.")); btn.disabled = !q; };
        dIn.oninput = upd; aIn.oninput = upd;
        render(body, h("div", { class: "amount" }, dIn, h("span", { class: "unit" }, "DERO")), h("div", { class: "amount mt" }, aIn, h("span", { class: "unit" }, s.ticker)), out, btn); upd();
      } else {
        const lIn = h("input", { inputmode: "decimal", placeholder: "0" }), out = h("div", { class: "card tight mt stack" }); let l = Z, q = null;
        const btn = button("Remove liquidity", "sell wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "RemoveLiquidity", args: [["min_dero", "U", A.minOut(q.d, V.app.settings.slippage || 100)], ["min_asset", "U", A.minOut(q.a, V.app.settings.slippage || 100)]], asset: { scid: s.scid, amount: l }, describe: "Remove liquidity", holdings: true, probe: { scid: s.scid, keys: ["lp"] } }); }, { disabled: true });
        const upd = () => { l = A.parseUnits(lIn.value, 5n) || Z; q = l > Z ? A.poolRemove(s, l) : null; render(out, q ? [kv("DERO out", A.fmtDero(q.d, 5)), kv(s.ticker + " out", A.fmtUnits(q.a, s.dec, 6))] : h("div", { class: "note" }, "Enter LP units (your LP balance is the pool SCID asset in your wallet).")); btn.disabled = !q; };
        lIn.oninput = upd; render(body, h("div", { class: "amount" }, lIn, h("span", { class: "unit" }, "LP")), out, btn); upd();
      }
    };
    openSheet(h("h2", null, "Liquidity · " + s.ticker), h("div", { class: "mt" }, U.segmented([{ v: "add", l: "Add" }, { v: "remove", l: "Remove" }], mode, (v) => { mode = v; draw(); })), body); draw();
  };

  Sh.stake = async (s) => {
    // the user's own vault entry is read by key (st_<addr>, sd_<addr>); the app never enumerates other stakers
    const mine = me() ? await D.myKeys(s.scid, me(), ["st_", "sd_"]).catch(() => ({})) : {};
    let pending = Z;
    if (mine.st) { const acc = s.sacc + (s.stot > Z ? s.tacc * 4294967296n / s.stot : Z); pending = mine.st * (acc - (mine.sd || Z)) / 4294967296n; }
    const inp = h("input", { inputmode: "decimal", placeholder: "0" });
    openSheet(h("h2", null, "Stake " + s.ticker), h("div", { class: "note mt" }, Number(s.tax) / 100 + "% of every trade's DERO leg is shared among stakers, pro rata, block by block."),
      h("div", { class: "card tight mt stack" }, kv("Total staked", A.fmtCompact(s.stot, s.dec)), kv("Your stake", mine.st ? A.fmtUnits(mine.st, s.dec, 4) : "0"), kv("Pending reward", A.fmtDero(pending, 5) + " DERO"), kv("Undistributed", A.fmtDero(s.tacc, 5) + " DERO")),
      h("div", { class: "amount mt" }, inp, h("span", { class: "unit" }, s.ticker)),
      h("div", { class: "row mt" }, button("Stake", "primary grow", () => { const a = A.parseUnits(inp.value, s.dec); if (!a) return toast("Enter an amount"); closeSheet(); V.app.tx({ scid: s.scid, entry: "Stake", args: [], asset: { scid: s.scid, amount: a }, describe: "Stake", holdings: true, probe: { scid: s.scid, keys: ["stot", "st_" + me()] } }); }),
        button("Unstake", "grow", () => { const a = A.parseUnits(inp.value, s.dec); if (!a) return toast("Enter an amount"); closeSheet(); V.app.tx({ scid: s.scid, entry: "Unstake", args: [["amt", "U", a]], describe: "Unstake", holdings: true, probe: { scid: s.scid, keys: ["stot", "st_" + me()] } }); })),
      button("Claim rewards", "wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "Claim", args: [], describe: "Claim rewards", holdings: true }); }));
  };
})();
