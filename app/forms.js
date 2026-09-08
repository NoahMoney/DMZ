// DMZ forms.js — creation forms: launch, pool, holder NFT, profile.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, W = V.wallet, S = V.stats, { h, render, toast, openSheet, closeSheet, button, router } = U;
  const Z = 0n, F = (V.forms = {});
  const field = (label, input) => h("label", { class: "field" }, label, input);
  const inp = (attrs) => h("input", Object.assign({ autocomplete: "off" }, attrs));
  // a group of fields sits one level above a field label: h3, not the label type the fields already use
  const group = (t) => h("h3", { class: "mt" }, t);
  const mini = (label, value) => h("div", { class: "mini", style: "flex:1 0 44%" }, h("div", { class: "l" }, label), value);
  const badUrl = (v) => v && !U.safeUrl(v.trim()); // creators may leave it empty; if set it must be https
  const TICK = /^[A-Z0-9]{1,10}$/;
  // every entrypoint here reads SIGNER(), so the call goes out at ring size 2 and the payer is public
  const RING2 = "Ring size 2: the transaction names your address. Anyone can compute who signed it.";
  async function template(kind) { const t = D.cfg.templates[kind]; const r = await fetch(t.file); if (!r.ok) throw new Error("template " + kind + " missing"); const code = await r.text(); if (t.sha256 && (await V.sha256Hex(code)) !== t.sha256) throw new Error("template hash mismatch"); return code; }
  async function installAndRegister(kind, args, registerEntry) {
    const code = await template(kind);
    const scid = await V.app.tx({ code, args, describe: "Install " + kind, install: true });
    if (!scid) return null;
    const fee = D.registry ? D.registry.regfee : Z;
    const cnt = registerEntry === "RegisterLaunch" ? "lc" : "pc", pre = registerEntry === "RegisterLaunch" ? "launch_" : "pool_";
    const reg = await V.app.tx({ scid: D.cfg.factory, entry: registerEntry, args: [["scid", "H", scid]], dero: fee, describe: "Register on the factory", probe: { scid: D.cfg.factory, keys: [cnt], check: async (p0, p1) => {
      // our scid must be among the entries added, not just any registration that landed in the same window
      const n0 = Number(p0.vals[cnt] || 0), n1 = Number(p1.vals[cnt] || 0); if (!(n1 > n0)) return false;
      const ks = []; for (let i = n0; i < Math.min(n1, n0 + 16); i++) ks.push(pre + i);
      const r = await V.node.getSC(D.cfg.factory, { keys: ks }); return ks.some((k) => String(r.vals[k] || "").toLowerCase() === scid); } } });
    if (!reg) toast("Installed but not registered: " + scid, "err", 9000);
    return scid;
  }
  // One screen, two wallet confirmations. The preview is computed with the same maths the contract runs at install
  // (D-4: vd0 = G/3, vt0 = (G + vd0)·C/G, so the price rises exactly 16× from the first buy to graduation).
  F.launch = () => {
    const f = { name: inp({ name: "name", placeholder: "Name", maxlength: 32 }), ticker: inp({ name: "ticker", placeholder: "TICKER", maxlength: 10, style: "text-transform:uppercase" }), descr: h("textarea", { name: "descr", placeholder: "What is it? (optional, ≤240)", maxlength: 240 }), icon: inp({ name: "icon", placeholder: "https://… (optional, 100×100)" }), tags: inp({ name: "tags", placeholder: "#tags (optional)" }),
      supply: inp({ name: "supply", value: "1000000", inputmode: "numeric" }), dec: inp({ name: "dec", value: "3", inputmode: "numeric" }), grad: inp({ name: "grad", value: "100", inputmode: "decimal" }), cbps: inp({ name: "cbps", value: "0", inputmode: "numeric" }), tax: inp({ name: "tax", value: "0", inputmode: "numeric" }) };
    let tmode = 0;
    const modes = U.segmented([{ v: 0, l: "No tax" }, { v: 1, l: "Creator" }, { v: 2, l: "Buyback" }, { v: 3, l: "Stakers" }], 0, (v) => { tmode = v; update(); });
    const unitsNote = h("div", { class: "note" });
    // Which asset the curve raises. This is the market's quote: pick wXMR and the token is priced in XMR, and
    // every buy, sell, fee and payout on it is denominated in XMR rather than DERO.
    const ZEROQ = "0000000000000000000000000000000000000000000000000000000000000000";
    const quotes = [{ scid: ZEROQ, sym: "DERO", name: "DERO", dec: 5 }].concat(D.vaults());
    let quote = quotes[0];
    const quoteChips = h("div", { class: "chips nowrap mt8" }, quotes.map((q) =>
      h("button", {
        class: "chip sm" + (q.scid === quote.scid ? " on" : ""), type: "button",
        onclick: () => {
          quote = q;
          [...quoteChips.children].forEach((c, i) => c.classList.toggle("on", quotes[i].scid === q.scid));
          quoteNote.textContent = q.scid === ZEROQ
            ? "Priced in DERO. Buyers pay DERO and the AMM opens as a DERO market."
            : "Priced in " + (q.sym || "wrapped") + ", a claim on " + U.clean(q.chain || "its own chain", 40) + " held by a federation. " +
              "Buyers need wrapped " + (q.sym || "units") + " — they deposit on the Wrap screen first. Fees and payouts are in " + (q.sym || "units") + " too.";
          update();
        },
      }, U.clean(q.sym || q.name || "wrapped", 8))));
    const quoteNote = h("div", { class: "note" });
    // live preview
    const pv = { av: h("div"), name: h("b", { class: "ellipsis" }), tick: h("div", { class: "sub ellipsis" }), start: h("div", { class: "v" }), end: h("div", { class: "v" }), mcap: h("div", { class: "v" }), tax: h("div", { class: "v" }), curve: h("div"), cost: h("div", { class: "note mt8" }), err: h("div") };
    const chips = h("div", { class: "chips nowrap mt8" }, [10, 50, 100, 500].map((g) => h("button", { class: "chip sm", type: "button", onclick: () => { f.grad.value = String(g); update(); } }, g + " " + (quote.sym || "DERO"))));
    // one parser for the preview and the submit, with the limits InitializePrivate enforces: what the preview
    // shows is what the contract receives, and a value the contract would refuse never reaches the wallet
    const readForm = () => {
      const int = (v, lo, hi) => { const n = Number(String(v).trim()); return Number.isInteger(n) && n >= lo && n <= hi ? n : null; };
      // The graduation target is in the QUOTE's base units, not DERO's. Parsing "100" at DERO's five decimals
      // while the creator had picked wXMR installed a target of 0.00001 XMR — the curve then graduated on a
      // dust buy, and grad is immutable after install.
      const qd = Number.isInteger(quote.dec) ? quote.dec : 5;
      const dec = int(f.dec.value || "0", 0, 12), supply = dec === null ? null : A.parseUnits(f.supply.value, BigInt(dec)), grad = A.parseUnits(f.grad.value, BigInt(qd));
      const cbpsN = Number(String(f.cbps.value || "0").trim()), taxN = Number(String(f.tax.value || "0").trim());
      const cbps = Number.isFinite(cbpsN) && cbpsN >= 0 && cbpsN <= 5 ? BigInt(Math.round(cbpsN * 100)) : null, tax = Number.isFinite(taxN) && taxN >= 0 && taxN <= 10 ? BigInt(Math.round(taxN * 100)) : null;
      const t = (f.ticker.value || "").toUpperCase().slice(0, 10), name = f.name.value.trim();
      let error = "";
      if (dec === null) error = "Decimals must be a whole number from 0 to 12.";
      else if (!supply || supply < 1000000n || supply >= 9223372036854775808n) error = "Supply must be at least 1 000 000 units and below 2^63.";
      else if (!grad || grad < 1000000n || grad > 1000000000000000n) error = "Graduation target must be between " + A.fmtUnits(1000000n, BigInt(qd), 6) + " and " + A.fmtUnits(1000000000000000n, BigInt(qd), 0) + " " + (quote.sym || "DERO") + ".";
      else if (cbps === null) error = "Creator allocation must be 0 to 5 percent.";
      else if (tax === null) error = "Tax must be 0 to 10 percent.";
      else if ((tmode === 0) !== (tax === Z)) error = "Set a tax rate for a tax mode, or no tax.";
      else if (f.icon.value.trim().length > 200 || f.tags.value.length > 100 || f.descr.value.length > 240) error = "Image URL up to 200 characters, tags up to 100, description up to 240.";
      return { ok: !error, error, dec: dec === null ? 0n : BigInt(dec), supply: supply || Z, grad: grad || Z, cbps: cbps === null ? Z : cbps, tax: tax === null ? Z : tax, ticker: t, name };
    };
    const update = () => {
      const r = readForm(), dec = r.dec, supply = r.supply, grad = r.grad, cbps = r.cbps, tax = Number(r.tax) / 100;
      const u = supply; unitsNote.textContent = u > 100000000000n ? "Warning: " + A.fmtCompact(u, 0n) + " atomic units in total. DERO wallets decrypt balances by search, so holders of very large unit counts wait minutes to see them. Keep supply × 10^decimals at or below 100 billion." : "Total atomic units: " + A.fmtCompact(u, 0n) + " (fine for wallets)."; unitsNote.className = "note " + (u > 100000000000n ? "down" : "");
      const t = (f.ticker.value || "").toUpperCase().slice(0, 10);
      render(pv.av, U.avatar({ ticker: t || "?", scid: "preview:" + (t || f.name.value), icon: U.safeUrl(f.icon.value) }));
      pv.name.textContent = f.name.value.trim() || "Your token"; pv.tick.textContent = (t || "TICKER") + " · curve 0%";
      if (!r.ok) { pv.start.textContent = pv.end.textContent = pv.mcap.textContent = pv.tax.textContent = "—"; render(pv.curve); pv.cost.textContent = ""; render(pv.err, h("div", { class: "errbox mt" }, r.error)); return; }
      render(pv.err);
      const alloc = (supply / 10000n) * cbps + ((supply % 10000n) * cbps) / 10000n, rest = supply - alloc, lq = rest / 5n, c = rest - lq, vd0 = grad / 3n, vt0 = ((grad + vd0) * c) / grad;
      // priced through the same helper every screen uses, with the chosen quote's decimals — the preview used
      // to be computed at DERO's five and labelled "DERO" whichever quote the creator had picked
      const qsym = quote.sym || "DERO", qd = Number.isInteger(quote.dec) ? quote.dec : 5, mkt = { dec, quoteDec: qd };
      const k = vd0 * vt0, p0 = A.priceOf(mkt, { rd: vd0, ra: vt0 }), p1 = p0 * 16, pts = [];
      for (let i = 0; i <= 40; i++) { const d = (grad * BigInt(i)) / 40n; pts.push({ ts: i, rd: vd0 + d, ra: k / (vd0 + d) }); }
      pv.start.textContent = S.fmtPriceSub(p0) + " " + qsym; pv.end.textContent = S.fmtPriceSub(p1) + " " + qsym; pv.mcap.textContent = S.fmtDeroShort(p1 * (Number(supply) / 10 ** Number(dec))) + " " + qsym;
      pv.tax.textContent = tmode === 0 ? "none" : tax + "% → " + ["", "creator", "buyback & lock", "stakers"][tmode];
      const sp = U.sparkline(pts, mkt, true); sp.style.height = "64px"; render(pv.curve, sp);
      const regfee = D.registry ? D.registry.regfee : 10000n;
      pv.cost.textContent = "Two wallet confirmations (install, then register) · fees ≈ " + A.fmtDero(15000n + regfee, 2) + " DERO · " + A.fmtCompact(c, dec) + " " + (t || "tokens") + " on the curve, " + A.fmtCompact(lq, dec) + " reserved for the AMM";
    };
    for (const k of Object.keys(f)) f[k].oninput = update;
    openSheet(h("h2", null, "Launch a token"), h("div", { class: "sub mt8" }, "Fixed supply on a bonding curve that graduates into a locked AMM. Nothing to run, nothing to rug."),
      // Level 1: the token you are about to install, priced with the maths the contract itself runs
      h("div", { class: "hero mt" }, h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Preview"), h("span", { class: "net" }, "DR")),
        h("div", { class: "row mt8" }, pv.av, h("div", { class: "grow ellipsis" }, pv.name, pv.tick)),
        h("div", { class: "mt8" }, pv.curve),
        h("div", { class: "pulse-row mt" }, mini("First price", pv.start), mini("At graduation (16×)", pv.end), mini("FDV at graduation", pv.mcap), mini("Tax", pv.tax)),
        pv.cost),
      pv.err,
      group("Token"),
      h("div", { class: "stack mt8" }, h("div", { class: "row" }, field("Name", f.name), field("Ticker", f.ticker)),
        h("div", null, h("div", { class: "label" }, "Raised in"), quoteChips, quoteNote,
          field("Graduation target (" + (quote.sym || "DERO") + ")", f.grad), chips,
          h("div", { class: "note mt8" }, "How much " + (quote.sym || "DERO") + " the curve raises before the AMM opens.")),
        field("Image", f.icon), field("Description", f.descr)),
      U.details("Advanced: supply, decimals, allocation, tax",
        h("div", { class: "stack", style: "padding-bottom:8px" }, h("div", { class: "row" }, field("Supply (whole tokens)", f.supply), field("Decimals (≤12)", f.dec)), unitsNote,
          h("div", { class: "row" }, field("Creator allocation % (≤5)", f.cbps), field("Tax % of each trade's DERO leg (≤10)", f.tax)), field("Tax goes to", modes), field("Tags", f.tags))),
      h("div", { class: "stack mt", style: "gap:0" }, U.kv("Route", "bonding curve, then a locked AMM"), U.kv("Protocol fee", Number(D.cfg.feeBps) / 100 + "% per trade")),
      h("div", { class: "note mt8" }, "80% of the supply after the creator allocation is sold on the curve; 20% seeds the AMM at graduation with the DERO raised, locked forever. " + RING2),
      button("Launch", "primary wide mt", async () => {
        const r = readForm();
        if (!r.ok) return toast(r.error, "err");
        const { dec, supply, grad, cbps, tax } = r;
        if (!f.name.value.trim() || !f.ticker.value) return toast("Fill name and ticker", "err");
        if (!TICK.test(f.ticker.value.toUpperCase())) return toast("Ticker: letters and digits only, up to 10", "err");
        if (badUrl(f.icon.value)) return toast("Image URL must start with https://", "err");
        closeSheet();
        const scid = await installAndRegister("launch", [["name", "S", f.name.value.trim()], ["ticker", "S", f.ticker.value.toUpperCase()], ["descr", "S", f.descr.value], ["icon", "S", f.icon.value.trim()], ["tags", "S", f.tags.value], ["quote", "H", quote.scid], ["dec", "U", dec], ["supply", "U", supply], ["grad", "U", grad], ["cbps", "U", cbps], ["tax", "U", tax], ["tmode", "U", BigInt(tmode)], ["treasury", "S", D.registry.treasury], ["fee", "U", BigInt(D.cfg.feeBps)], ["pfee", "U", BigInt(D.cfg.launchPfeeBps)]], "RegisterLaunch");
        if (scid) { await D.refresh(); router.go("token", scid); }
      }));
    update();
  };
  F.pool = () => {
    const asset = inp({ placeholder: "Asset SCID (64 hex)", class: "mono" });
    openSheet(h("h2", null, "Create a pool"), h("div", { class: "sub mt8" }, "A constant-product market over any DERO asset. Two wallet confirmations: install, then register."),
      h("div", { class: "stack mt" }, field("Asset", asset)),
      h("div", { class: "stack mt", style: "gap:0" }, U.kv("Fee", Number(D.cfg.feeBps) / 100 + "% per trade"), U.kv("Fee split", "half to LPs, half to the protocol"), U.kv("Route", "constant-product AMM")),
      h("div", { class: "warnbox mt" }, "DMZ cannot verify what an arbitrary asset is: anyone can create a DERO asset with any name. A pool over an asset that is not a DMZ launch is listed with an amber “asset unknown” badge. Check the asset id yourself."),
      h("div", { class: "note mt8" }, "Add the first liquidity from the token page once the pool exists. " + RING2),
      button("Create", "primary wide mt", async () => {
        const a = asset.value.trim().toLowerCase(); if (!/^[0-9a-f]{64}$/.test(a)) return toast("Enter a 64-hex SCID", "err");
        try { const sc = await V.node.getSC(a, { code: true }); if (!sc.code) throw new Error(); } catch (e) { return toast("No contract at that SCID", "err"); }
        closeSheet();
        const scid = await installAndRegister("pool", [["asset", "H", a], ["quote", "H", "0000000000000000000000000000000000000000000000000000000000000000"], ["treasury", "S", D.registry.treasury], ["fee", "U", BigInt(D.cfg.feeBps)], ["pfee", "U", BigInt(D.cfg.pfeeBps)]], "RegisterPool");
        if (scid) { await D.refresh(); router.go("token", scid); }
      }));
  };
  F.nft = () => {
    const launches = D.list().filter((s) => s.kind === "launch");
    if (!launches.length) return openSheet(h("h2", null, "Create a holder NFT"),
      U.emptyState("gem", "No launch to gate on", "A holder NFT is minted only by holders of a DMZ launch, and no launch is listed yet.", button("Launch one first", "primary", () => F.launch())));
    const sel = h("select", null, launches.map((s) => h("option", { value: s.scid }, s.ticker + " · " + s.name)));
    const f = { name: inp({ placeholder: "Name (≤64)", maxlength: 64 }), descr: h("textarea", { placeholder: "Description" }), icon: inp({ placeholder: "Icon URL" }), file: inp({ placeholder: "Image URL" }), tags: inp({ placeholder: "#tags" }), coll: inp({ placeholder: "Collection name" }), minhold: inp({ value: "1000", inputmode: "decimal" }), blocks: inp({ value: "1000", inputmode: "numeric" }), max: inp({ value: "100", inputmode: "numeric" }), price: inp({ value: "1", inputmode: "decimal" }) };
    openSheet(h("h2", null, "Create a holder NFT"), h("div", { class: "sub mt8" }, "An edition only a holder of your token can mint."),
      group("Identity"),
      h("div", { class: "stack mt8" }, field("Name", f.name), field("Description", f.descr), field("Icon URL", f.icon), field("Image URL", f.file), field("Tags", f.tags), field("Collection", f.coll)),
      group("Holder gate"),
      h("div", { class: "stack mt8" }, field("Token to hold", sel), h("div", { class: "row" }, field("Min hold (tokens)", f.minhold), field("Hold blocks", f.blocks))),
      group("Edition"),
      h("div", { class: "stack mt8" }, h("div", { class: "row" }, field("Max editions", f.max), field("Mint price (DERO)", f.price))),
      h("div", { class: "note mt" }, "Two wallet confirmations: install, then register. " + RING2),
      button("Create", "primary wide mt", async () => {
        const tok = D.assets.get(sel.value); if (!tok) return toast("Pick a token", "err");
        const minhold = A.parseUnits(f.minhold.value, tok.dec), price = A.parseUnits(f.price.value, 5n) || Z;
        if (!f.name.value.trim() || !minhold) return toast("Fill name and minimum hold", "err");
        if (badUrl(f.icon.value) || badUrl(f.file.value)) return toast("Image URLs must start with https://", "err");
        closeSheet();
        const scid = await installAndRegister("nft", [["name", "S", f.name.value], ["descr", "S", f.descr.value], ["icon", "S", f.icon.value], ["file", "S", f.file.value], ["tags", "S", f.tags.value], ["collection", "S", f.coll.value], ["token", "H", tok.scid], ["minhold", "U", minhold], ["holdblocks", "U", BigInt(f.blocks.value || 0)], ["maxsupply", "U", BigInt(f.max.value || 1)], ["price", "U", price], ["treasury", "S", D.registry.treasury], ["fee", "U", BigInt(D.cfg.feeBps)]], "RegisterNFT");
        if (scid) { await D.refresh(); router.go("nft", scid); }
      }));
  };
  F.profile = (p) => {
    const name = inp({ value: p.name || "", maxlength: 32, placeholder: "Display name" }), bio = h("textarea", { maxlength: 160, placeholder: "Bio" }, p.bio || ""), av = inp({ value: p.avatar || "", placeholder: "Avatar URL" });
    openSheet(h("h2", null, "Edit profile"), h("div", { class: "sub mt8" }, "What this address shows other people. Your balances and your trades are not part of it."),
      h("div", { class: "stack mt" }, field("Name", name), field("Bio", bio), field("Avatar URL", av)),
      h("div", { class: "warnbox mt" }, "This is stored in the clear on the social contract, under your address, for anyone to read. " + RING2),
      button("Save", "primary wide mt", async () => { if (badUrl(av.value)) return toast("Avatar URL must start with https://", "err"); closeSheet(); const ok = await V.app.tx({ scid: D.cfg.social, entry: "Profile", args: [["name", "S", name.value], ["bio", "S", bio.value], ["avatar", "S", av.value]], describe: "Update profile" }); if (ok) { await D.loadSocial(W.canon); router.dispatch(); } }));
  };
})();
