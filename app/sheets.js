// DMZ sheets.js — liquidity (pools) and staking (launch tmode 3) sheets. Both are execution instruments and are
// composed like the trade sheet: an amount, one estimated reading, the terms as hairline rows, one action.
// Nothing that costs money hides behind a disclosure, and no amount is called DERO unless it is DERO: a pool
// quoted in a wrapped asset takes and pays that asset, and only the network fee is ever DERO.
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
  // The gate every sheet's button passes while a wallet is connected: the wallet must have answered and hold the
  // amount, or the button stays shut and says why. An amount the wallet would refuse is never offered, and a
  // balance that cannot be read counts as not enough. need: [{have, amount, sym, dec}] — have is null while the
  // balance is being read and undefined when the wallet did not answer.
  const gate = (btn, label, need) => {
    btn.textContent = label; if (!V.wallet.connected || btn.disabled) return;
    for (const n of need) {
      if (n.have === undefined) { btn.disabled = true; btn.textContent = "Balance unreadable"; return; }
      if (n.have === null) { btn.disabled = true; btn.textContent = "Reading your " + n.sym + " balance…"; return; }
      if (n.amount > n.have) { btn.disabled = true; btn.textContent = "Not enough " + n.sym + ": " + A.fmtUnits(n.amount - n.have, n.dec, 4) + " short"; return; }
    }
  };
  const held = async (scid) => { try { const b = await V.wallet.getBalance(scid); return V.toBig(b && b.balance) || Z; } catch (e) { return undefined; } };

  // ---- the network fee, before the button --------------------------------------------------------------------
  // Gas is DERO on every market, whatever that market trades in, so it is never folded into the trading fee. The
  // node prices the exact call before the user commits; the signer is a random address and never the connected
  // one, because asking "what would this cost" must not tell the node who is about to act.
  let gasAddr = null;
  // opts: {scid, entry, args:[[name,datatype,value]], send:[{scid?,amount}]}  — a send with no scid is DERO.
  // Returns "0.00397 DERO", or "" when the node will not price it. Mirrors app.tx: gasstorage + 200.
  Sh.gas = async (opts) => {
    if (!gasAddr) gasAddr = await V.node.randomAddress();
    if (!V.isAddress(gasAddr)) throw new Error("no address");
    const rpc = [{ name: "entrypoint", datatype: "S", value: opts.entry }];
    for (const [name, datatype, value] of opts.args || []) rpc.push({ name, datatype, value });
    rpc.push({ name: "SC_ACTION", datatype: "U", value: Z }, { name: "SC_ID", datatype: "H", value: opts.scid });
    const transfers = (opts.send || []).filter((t) => t && t.amount > Z).map((t) => (t.scid ? { scid: t.scid, amount: 0, burn: t.amount } : { destination: gasAddr, amount: 0, burn: t.amount }));
    const r = await V.node.gasEstimate({ scid: opts.scid, ringsize: 2, signer: gasAddr, sc_rpc: rpc, transfers });
    const g = r && /^\d+$/.test(String(r.gasstorage)) ? BigInt(r.gasstorage) + 200n : null;
    return g === null ? "" : A.fmtUnits(g, 5, 5) + " DERO";
  };
  Sh.gasRow = (txt, pending) => kv("Network fee (gas), in DERO", txt || (pending ? "estimating…" : "—"), txt ? "" : "dim");

  Sh.liquidity = (s) => {
    const qsym = D.qsym(s), qdec = D.qdec(s), lockedLp = A.fmtUnits(1000n, qdec, Number(qdec));
    // LP units are minted in quote units (pool.bas mints d - 1000 on the first deposit and stores lp = d), so the
    // LP figures carry the quote's decimals too — reading them as DERO would misstate them by whatever differs.
    // AddLiquidity has to receive both sides in one call. V.app.tx can carry one asset plus DERO, so a pool quoted
    // in a wrapped asset (two assets, no DERO) cannot be funded from here: pool.bas line 42 rejects stray DERO and
    // would take the network fee for nothing. Say that instead of sending it.
    const canAdd = !s.quote;
    let mode = canAdd ? "add" : "remove";
    const body = h("div", { class: "mt" });
    const draw = () => {
      const slip = slipBps(), detail = rows();
      let gasTxt = "", gasSeq = 0, gasTimer = null;
      if (mode === "add") {
        const dIn = h("input", { inputmode: "decimal", placeholder: "0", autocomplete: "off", "aria-label": qsym + " to deposit" }), aIn = h("input", { inputmode: "decimal", placeholder: "0", autocomplete: "off", "aria-label": s.ticker + " to deposit" }), out = reading("LP units minted (estimated)"); let d = Z, a = Z, q = null;
        const btn = button("Add liquidity", "primary wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "AddLiquidity", args: [["min_lp", "U", A.minOut(q.lp, V.app.settings.slippage || 100)]], dero: d, asset: { scid: s.asset, amount: a }, describe: "Add liquidity", holdings: true, probe: { scid: s.scid, keys: ["lp"] } }); }, { disabled: true });
        let bq = null, ba = null; if (V.wallet.connected) Promise.all([held(s.quote || ""), held(s.asset)]).then(([x, y]) => { bq = x; ba = y; upd(); });
        const paint = () => render(detail, q ? [kv("Minimum LP, or the call reverts", A.fmtUnits(A.minOut(q.lp, slip), qdec, 3)),
          s.lp === Z ? kv("Locked forever, nobody can withdraw it", lockedLp + " LP") : null,
          q.refundA > Z ? kv("Returned to you, the ratio decides", A.fmtUnits(q.refundA, s.dec, 6) + " " + s.ticker) : null,
          q.refundD > Z ? kv("Returned to you, the ratio decides", A.fmtUnits(q.refundD, qdec, 6) + " " + qsym) : null,
          Sh.gasRow(gasTxt, true), kv("Route", AMM), kv("Ring size", "2 · the payer is public")] : null);
        const estimate = async () => { const my = ++gasSeq, at = q; try { const t = await Sh.gas({ scid: s.scid, entry: "AddLiquidity", args: [["min_lp", "U", A.minOut(at.lp, slip)]], send: [{ amount: d }, { scid: s.asset, amount: a }] }); if (my !== gasSeq || q !== at) return; gasTxt = t; } catch (e) { if (my !== gasSeq) return; gasTxt = ""; } if (my === gasSeq) paint(); };
        const upd = () => { d = A.parseUnits(dIn.value, qdec) || Z; if (s.lp > Z) { a = A.poolAssetFor(s, d) || Z; aIn.value = a > Z ? A.fmtUnits(a, s.dec, Number(s.dec)).replace(/,/g, "") : ""; } else a = A.parseUnits(aIn.value, s.dec) || Z; q = d > Z && a > Z ? A.poolAdd(s, d, a) : null;
          out.v.textContent = q ? A.fmtUnits(q.lp, qdec, 3) : "0";
          out.n.textContent = q ? "Share of the pool after this deposit: " + (Number(q.lp * 10000n / (s.lp + q.lp)) / 100).toFixed(2) + "%" : s.lp === Z ? "Enter both amounts. The first deposit must be at least " + A.fmtUnits(100000n, qdec, Number(qdec)) + " " + qsym + "." : "Enter " + qsym + "; the asset amount follows the pool ratio.";
          clearTimeout(gasTimer); gasSeq++; gasTxt = ""; if (q) gasTimer = setTimeout(estimate, 320);
          paint(); btn.disabled = !q; gate(btn, "Add liquidity", [{ have: bq, amount: d, sym: qsym, dec: qdec }, { have: ba, amount: a, sym: s.ticker, dec: s.dec }]); };
        dIn.oninput = upd; aIn.oninput = upd;
        render(body, h("div", { class: "eyebrow" }, "You deposit"), amountBox(dIn, qsym), amountBox(aIn, s.ticker),
          s.lp === Z ? h("div", { class: "warnbox mt" }, "This is the first deposit: your two amounts set the opening price, and " + lockedLp + " LP stays locked forever.") : null,
          out.el, h("div", { class: "eyebrow mt" }, "What this costs"), h("div", { class: "mt8" }, detail),
          h("div", { class: "note mt8" }, "Depositing pays no trading fee — you are on the other side of it from now on. " + RINGNOTE + " The network fee is DERO" + (s.quote ? ", not " + qsym : "") + "."),
          btn, confirmNote()); upd();
      } else {
        const lIn = h("input", { inputmode: "decimal", placeholder: "0", autocomplete: "off", "aria-label": "LP units to burn" }), out = reading("You receive (estimated)"); let l = Z, q = null;
        const btn = button("Remove liquidity", "primary wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "RemoveLiquidity", args: [["min_dero", "U", A.minOut(q.d, V.app.settings.slippage || 100)], ["min_asset", "U", A.minOut(q.a, V.app.settings.slippage || 100)]], asset: { scid: s.scid, amount: l }, describe: "Remove liquidity", holdings: true, probe: { scid: s.scid, keys: ["lp"] } }); }, { disabled: true });
        let bl = null; if (V.wallet.connected) held(s.scid).then((x) => { bl = x; upd(); });
        const paint = () => render(detail, q ? [kv("Minimum received, or it reverts", A.fmtUnits(A.minOut(q.d, slip), qdec, 6) + " " + qsym + " · " + A.fmtUnits(A.minOut(q.a, slip), s.dec, 6) + " " + s.ticker),
          Sh.gasRow(gasTxt, true), kv("Route", AMM), kv("Ring size", "2 · the payer is public")] : null);
        const estimate = async () => { const my = ++gasSeq, at = q; try { const t = await Sh.gas({ scid: s.scid, entry: "RemoveLiquidity", args: [["min_dero", "U", A.minOut(at.d, slip)], ["min_asset", "U", A.minOut(at.a, slip)]], send: [{ scid: s.scid, amount: l }] }); if (my !== gasSeq || q !== at) return; gasTxt = t; } catch (e) { if (my !== gasSeq) return; gasTxt = ""; } if (my === gasSeq) paint(); };
        const upd = () => { l = A.parseUnits(lIn.value, qdec) || Z; q = l > Z ? A.poolRemove(s, l) : null;
          out.v.textContent = q ? A.fmtUnits(q.d, qdec, 6) + " " + qsym : "0";
          out.n.textContent = q ? "and " + A.fmtUnits(q.a, s.dec, 6) + " " + s.ticker : l > Z ? "More than the pool can release: " + lockedLp + " LP stays locked forever." : "Enter LP units to burn.";
          clearTimeout(gasTimer); gasSeq++; gasTxt = ""; if (q) gasTimer = setTimeout(estimate, 320);
          paint(); btn.disabled = !q; gate(btn, "Remove liquidity", [{ have: bl, amount: l, sym: "LP", dec: qdec }]); };
        lIn.oninput = upd;
        render(body, h("div", { class: "eyebrow" }, "You withdraw"), amountBox(lIn, "LP"),
          h("div", { class: "note mt8" }, "Your LP balance is the pool's own contract id, held as an asset in your wallet. LP units carry the quote's decimals: " + Number(qdec) + " for " + qsym + "."),
          out.el, h("div", { class: "eyebrow mt" }, "What this costs"), h("div", { class: "mt8" }, detail),
          h("div", { class: "note mt8" }, RINGNOTE + " The network fee is DERO" + (s.quote ? ", not " + qsym : "") + "."),
          btn, confirmNote()); upd();
      }
    };
    openSheet(h("h2", null, "Liquidity · " + s.ticker + " / " + qsym), h("div", { class: "sub mt8" }, "Half of every trade's fee stays in the pool. LP units are your claim on both reserves, in " + qsym + " and " + s.ticker + "."),
      canAdd ? null : h("div", { class: "warnbox mt" }, h("b", null, "Adding is not available on a wrapped-quote pool yet."),
        h("div", { class: "mt8" }, "A deposit has to arrive as two assets in one call — wrapped " + qsym + " and " + s.ticker + " — and this build can only send one asset alongside DERO. The contract rejects stray DERO, so the call would revert and take the network fee for nothing. Removing liquidity works normally.")),
      h("div", { class: "mt" }, U.segmented(canAdd ? [{ v: "add", l: "Add" }, { v: "remove", l: "Remove" }] : [{ v: "remove", l: "Remove" }], mode, (v) => { mode = v; draw(); })), body); draw();
  };

  Sh.stake = async (s) => {
    // the user's own vault entry is read by key (st_<addr>, sd_<addr>); the app never enumerates other stakers
    const qsym = D.qsym(s), qdec = D.qdec(s);
    const mine = me() ? await D.myKeys(s.scid, me(), ["st_", "sd_"]).catch(() => ({})) : {};
    let pending = Z;
    if (mine.st) { const acc = s.sacc + (s.stot > Z ? s.tacc * 4294967296n / s.stot : Z); pending = mine.st * (acc - (mine.sd || Z)) / 4294967296n; }
    const inp = h("input", { inputmode: "decimal", placeholder: "0", autocomplete: "off", "aria-label": "amount" });
    // both buttons pass the gate: Stake against the wallet's tokens, Unstake against the stake the contract records
    const stakeBtn = button("Stake", "primary grow", () => { const a = A.parseUnits(inp.value, s.dec); if (!a) return toast("Enter an amount"); closeSheet(); V.app.tx({ scid: s.scid, entry: "Stake", args: [], asset: { scid: s.scid, amount: a }, describe: "Stake", holdings: true, probe: { scid: s.scid, keys: ["stot", "st_" + me()] } }); }, { disabled: true });
    const unBtn = button("Unstake", "grow", () => { const a = A.parseUnits(inp.value, s.dec); if (!a) return toast("Enter an amount"); closeSheet(); V.app.tx({ scid: s.scid, entry: "Unstake", args: [["amt", "U", a]], describe: "Unstake", holdings: true, probe: { scid: s.scid, keys: ["stot", "st_" + me()] } }); }, { disabled: true });
    let bt = null; if (V.wallet.connected) held(s.scid).then((x) => { bt = x; upd(); });
    const upd = () => { const a = A.parseUnits(inp.value, s.dec) || Z; stakeBtn.disabled = !a; gate(stakeBtn, "Stake", [{ have: bt, amount: a, sym: s.ticker, dec: s.dec }]); unBtn.disabled = !a; gate(unBtn, "Unstake", [{ have: mine.st || Z, amount: a, sym: "staked " + s.ticker, dec: s.dec }]); };
    inp.oninput = upd;
    openSheet(h("h2", null, "Stake " + s.ticker), h("div", { class: "sub mt8" }, Number(s.tax) / 100 + "% of every trade's " + qsym + " leg is shared among stakers, pro rata, block by block."),
      h("div", { class: "outline mt" }, h("div", { class: "l" }, "Pending reward"), h("div", { class: "v num" }, A.fmtUnits(pending, qdec, 5) + " " + qsym)),
      h("div", { class: "stack mt", style: "gap:0" }, kv("Your stake", mine.st ? A.fmtUnits(mine.st, s.dec, 4) : "0"), kv("Total staked", A.fmtCompact(s.stot, s.dec)), kv("Undistributed in the contract", A.fmtUnits(s.tacc, qdec, 5) + " " + qsym), kv("Ring size", "2 · the payer is public")),
      h("div", { class: "note mt8" }, RINGNOTE + " Staking, unstaking and claiming are three separate transactions, and each one pays a network fee in DERO" + (s.quote ? ", not in " + qsym : "") + "."),
      h("div", { class: "eyebrow mt" }, "Stake or unstake"), amountBox(inp, s.ticker),
      h("div", { class: "row mt" }, stakeBtn, unBtn),
      button("Claim rewards", "wide mt", () => { closeSheet(); V.app.tx({ scid: s.scid, entry: "Claim", args: [], describe: "Claim rewards", holdings: true }); }), confirmNote()); upd();
  };
})();
