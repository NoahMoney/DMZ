// DMZ forms.js — the Configure step of each creation (coin, pool, holder NFT, NFA), and the profile editor. The flow
// around them (steps, review, cost, pay, recovery) is wizard.js; what each contract accepts is create.js. Every field
// here is one InitializePrivate takes, and nothing else: the treasury and the fees have no field, they are this
// deployment's, and the review says so.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, W = V.wallet, S = V.stats, C = V.create, { h, render, toast, openSheet, closeSheet, button, router } = U;
  const Z = 0n, F = (V.forms = {}), X = (F.specs = {});
  const inp = (attrs) => h("input", Object.assign({ autocomplete: "off" }, attrs));
  const group = (t) => h("h3", { class: "mt" }, t);
  const mini = (label, value) => h("div", { class: "mini", style: "flex:1 0 44%" }, h("div", { class: "l" }, label), value);
  const pct = (bps) => Number(bps) / 100 + "%";
  const badUrl = (v) => v && !U.safeUrl(v.trim()); // the profile editor's avatar: empty, or https
  // GAP-005: a new market pays the treasury this build ships (the set feed.js verifies listings against), and the
  // factory's live pointer only when the build ships none or that set already names it
  const treas = () => { const t = D.cfg.treasuries || [], r = (D.registry || {}).treasury; return t.length && !t.some((a) => V.canonAddress(a) === V.canonAddress(r)) ? t[0] : r; };
  const DERO = { scid: C.ZEROQ, sym: "DERO", dec: 5 };
  // A build with no vault (mainnet's names none) prices in DERO only, and names nothing else
  const vaults = () => D.vaults().map((v) => ({ scid: v.scid, sym: U.clean(v.sym || v.name || "wrapped", 8), dec: v.dec, chain: v.chain }));
  const quotePick = (fs, quotes, cur, pick, note) => quotes.length > 1 ? fs.field("quote", "Priced in", V.wizard.choice(quotes, cur, (q) => q.chip || q.sym, pick), null, note) : h("div", { class: "note" }, "Priced in DERO: buyers pay DERO, and DERO is the gas.");
  const qWords = (q) => (q.sym === "DERO" ? "DERO" : q.sym + ", a claim on " + U.clean(q.chain || "its own chain", 40) + " held by a federation; buyers deposit on the Wrap screen first");
  const iconHint = "Other DERO wallets and explorers may show it; this app never loads it. With a logo or links below, the profile document's id goes here instead.";

  // ---- coin: launch.bas ----
  X.launch = ({ fs, brand }) => {
    const f = { name: inp({ name: "name", placeholder: "Name", maxlength: 32 }), ticker: inp({ name: "ticker", placeholder: "TICKER", maxlength: 10, style: "text-transform:uppercase", autocapitalize: "characters" }),
      descr: h("textarea", { name: "descr", placeholder: "What is it?", maxlength: 240 }), icon: inp({ name: "icon", placeholder: "https://…", inputmode: "url" }), tags: inp({ name: "tags", placeholder: "#tags", maxlength: 100 }),
      supply: inp({ name: "supply", value: "1000000", inputmode: "numeric" }), dec: inp({ name: "dec", value: "3", inputmode: "numeric" }), grad: inp({ name: "grad", value: "100", inputmode: "decimal" }),
      cbps: inp({ name: "cbps", value: "0", inputmode: "decimal" }), tax: inp({ name: "tax", value: "0", inputmode: "decimal" }) };
    // Operator ruling 2026-09-15: XMR and ZEC (wrapped, 1:1) first whenever the build names vaults, then DERO; a build
    // with none offers DERO alone. The first is selected at open: XMR where the build names vaults, else DERO.
    const quotes = vaults().map((v) => Object.assign({}, v, { chip: v.sym + " · wrapped 1:1" })).concat([DERO]), quoteNote = h("div", { class: "note" });
    let tmode = 0, quote = quotes[0];
    const fed = D.vault && D.vault.k ? D.vault.k + "-of-" + D.vault.na : "k-of-n";
    const modes = U.segmented([{ v: 0, l: "No tax" }, { v: 1, l: "Creator" }, { v: 2, l: "Buyback" }, { v: 3, l: "Stakers" }], 0, (v) => { tmode = v; aria(); fs.onchange(); });
    const aria = () => { modes.setAttribute("role", "radiogroup"); [...modes.children].forEach((b, i) => { b.setAttribute("role", "radio"); b.setAttribute("aria-checked", String(i === tmode)); }); };
    aria();
    const GRADS = [10, 50, 100, 500], grads = h("div", { class: "chips nowrap mt8" }, GRADS.map((g) => h("button", { class: "chip sm", type: "button", onclick: () => { f.grad.value = String(g); fs.onchange(); } })));
    const gradLabel = h("span"), taxLabel = h("span"), unitsNote = h("div", { class: "note" });
    const relabel = () => { const q = quote.sym; gradLabel.textContent = "Graduation target (" + q + ")"; taxLabel.textContent = "Tax % of each trade's " + q + " leg (≤10)"; [...grads.children].forEach((c, i) => { c.textContent = GRADS[i] + " " + q; }); quoteNote.textContent = "Priced in " + qWords(quote) + ". Every buy, sell, fee and payout on it is in " + q + "."; };
    const vals = () => Object.assign(Object.fromEntries(Object.entries(f).map(([k, el]) => [k, el.value])), { tmode });
    const env = (iconRef) => ({ quote, treasury: treas(), feeBps: D.cfg.feeBps, launchPfeeBps: D.cfg.launchPfeeBps, iconRef });
    // Level 1: the coin about to be installed, priced with the maths the contract itself runs (D-4: vd0 = G/3,
    // vt0 = (G + vd0)·C/G, so the price rises exactly 16× from the first buy to graduation)
    const pv = { av: h("div"), name: h("b", { class: "ellipsis" }), tick: h("div", { class: "sub ellipsis" }), start: h("div", { class: "v" }), end: h("div", { class: "v" }), mcap: h("div", { class: "v" }), tax: h("div", { class: "v" }), curve: h("div"), cost: h("div", { class: "note mt8" }) };
    const update = () => {
      const r = C.launch(vals(), env(V.wizard.brandAny() ? "dmz:" + C.ZEROQ : null)), t = (f.ticker.value || "").toUpperCase().slice(0, 10), n = V.wizard.brandAny() ? 3 : 2;
      render(pv.av, U.avatar({ ticker: t || "?", scid: "preview:" + (t || f.name.value) }));
      pv.name.textContent = f.name.value.trim() || "Your coin"; pv.tick.textContent = (t || "TICKER") + " · curve 0%";
      pv.cost.textContent = C.CONFIRM_WORDS[n] + " wallet confirmations: " + (n === 3 ? "the profile document, then the install, then the registration" : "install, then register") + " · registration fee " + (D.registry ? A.fmtDero(D.registry.regfee, 5) + " DERO" : "not read yet");
      const x = r.ok ? r.facts : null, dec = x ? BigInt(r.args[6][2]) : 0n;
      unitsNote.textContent = x && x.heavy ? "Warning: " + A.fmtCompact(x.units, 0n) + " units in total. DERO wallets find balances by search, so holders of very large unit counts wait minutes to see them. Keep supply × 10^decimals at or below 100 billion." : x ? "Total units: " + A.fmtCompact(x.units, 0n) + " (fine for wallets)." : "";
      unitsNote.className = "note" + (x && x.heavy ? " down" : "");
      if (!x) { pv.start.textContent = pv.end.textContent = pv.mcap.textContent = pv.tax.textContent = "—"; render(pv.curve); return; }
      const qd = Number.isInteger(quote.dec) ? quote.dec : 5, mkt = { dec, quoteDec: qd }, k = x.vd0 * x.vt0, pts = [], grad = r.args[8][2];
      for (let i = 0; i <= 40; i++) { const d = (grad * BigInt(i)) / 40n; pts.push({ ts: i, rd: x.vd0 + d, ra: k / (x.vd0 + d) }); }
      pv.start.textContent = S.fmtPriceSub(x.p0) + " " + quote.sym; pv.end.textContent = S.fmtPriceSub(x.p1) + " " + quote.sym; pv.mcap.textContent = S.fmtDeroShort(x.p1 * (Number(x.units) / 10 ** Number(dec))) + " " + quote.sym;
      pv.tax.textContent = tmode === 0 ? "none" : pct(r.args[10][2]) + " → " + ["", "creator", "buyback & lock", "stakers"][tmode];
      const sp = U.sparkline(pts, mkt, true); sp.style.height = "64px"; render(pv.curve, sp);
    };
    relabel();
    const node = h("div", null,
      h("div", { class: "hero mt" }, h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Preview"), h("span", { class: "net" }, "DR")),
        h("div", { class: "row mt8" }, pv.av, h("div", { class: "grow ellipsis" }, pv.name, pv.tick)), h("div", { class: "mt8" }, pv.curve),
        h("div", { class: "pulse-row mt" }, mini("First price", pv.start), mini("At graduation (16×)", pv.end), mini("FDV at graduation", pv.mcap), mini("Tax", pv.tax)), pv.cost),
      group("Coin"),
      h("div", { class: "stack mt8" }, h("div", { class: "row wz-2" }, fs.field("name", "Name", f.name, "Up to 32 bytes."), fs.field("ticker", "Ticker", f.ticker, "A to Z and 0 to 9, up to 10.")),
        quotePick(fs, quotes, quote, (q) => { quote = q; relabel(); fs.onchange(); }, [h("div", { class: "note" }, "A wrapped unit is backed 1:1 by coins held by the " + fed + " federation, any " + fed.split("-")[0] + " of whom could move them."), quoteNote]),
        fs.field("grad", gradLabel, f.grad, "How much the curve raises before the AMM opens.", grads), fs.field("descr", "Description", f.descr, "Optional, up to 240 bytes.")),
      U.details("Advanced: supply, decimals, allocation, tax, tags, icon",
        h("div", { class: "stack", style: "padding-bottom:8px" }, h("div", { class: "row wz-2" }, fs.field("supply", "Supply (whole tokens)", f.supply), fs.field("dec", "Decimals (≤12)", f.dec)), unitsNote,
          h("div", { class: "row wz-2" }, fs.field("cbps", "Creator allocation % (≤5)", f.cbps, "Sent to you by the install."), fs.field("tax", taxLabel, f.tax)), fs.field("tmode", "Tax goes to", modes),
          fs.field("tags", "Tags", f.tags, "Optional, up to 100 bytes."), fs.field("icon", "Icon URL (https)", f.icon, iconHint))),
      h("div", { class: "note mt" }, "80% of the supply after the creator allocation is sold on the curve; 20% seeds the AMM at graduation with everything raised, locked for ever."));
    return { node, update, read: (ref) => C.launch(vals(), env(ref)), label: (r) => r.args[1][2] + " · " + r.args[0][2],
      review: (r) => {
        const a = Object.fromEntries(r.args.map(([k, , v]) => [k, v])), x = r.facts, t = a.ticker, fu = (v) => A.fmtUnits(v, a.dec, Number(a.dec)) + " " + t, q = quote.sym, qd = Number.isInteger(quote.dec) ? quote.dec : 5;
        return [["What", "A coin: a new token and its own market, installed from the shipped launch template."], ["Name", a.name], ["Ticker", t], ["Description", a.descr || "none"], ["Tags", a.tags || "none"],
          ["Supply", fu(a.supply) + " in " + a.dec + " decimals (" + A.fmtUnits(a.supply, 0n) + " units), fixed for ever"],
          ["To you at install", a.cbps > Z ? pct(a.cbps) + ": " + fu(x.alloc) + ", sent to your wallet by the install; you may sell it at any time" : "nothing (0% creator allocation)"],
          ["On the curve", fu(x.c) + ", sold on the bonding curve"], ["Kept for the AMM", fu(x.lq) + ", paired with everything raised when the curve sells out, and locked for ever"],
          ["Priced in", qWords(quote)], ["Graduates at", A.fmtUnits(a.grad, BigInt(qd), qd) + " " + q + " raised. The price rises 16× from the first buy (" + S.fmtPriceSub(x.p0) + " " + q + ") to graduation (" + S.fmtPriceSub(x.p1) + " " + q + ")"],
          ["Tax", a.tmode === Z ? "none" : pct(a.tax) + " of each trade's " + q + " leg, " + ["", "to you, claimed when you like", "spent buying the coin back and locking it", "shared by stakers"][Number(a.tmode)]],
          ["Trading fee", pct(a.fee) + " of every trade. On the curve all of it goes to the protocol treasury; after graduation " + pct(a.pfee) + " of it does and the rest stays in the pool"],
          ["Treasury", h("span", { class: "mono wz-id" }, a.treasury)], ["Creator", "your wallet's address, recorded by the install"]];
      } };
  };

  // ---- pool: pool.bas ----
  X.pool = ({ fs }) => {
    const asset = inp({ name: "asset", placeholder: "64 characters, 0-9 a-f", class: "mono", spellcheck: "false" });
    const quotes = [DERO].concat(vaults()); let quote = DERO;
    const read = () => C.pool({ asset: asset.value }, { quote, treasury: treas(), feeBps: D.cfg.feeBps, pfeeBps: D.cfg.pfeeBps });
    const node = h("div", { class: "stack mt" },
      fs.field("asset", "Asset contract id", asset, "The DERO asset this pool trades. DMZ cannot tell what an arbitrary asset is: a pool over one that is not a DMZ coin is listed with an amber “asset unknown” badge. Check the id yourself."),
      quotePick(fs, quotes, quote, (q) => { quote = q; fs.onchange(); }),
      h("div", { class: "note" }, "The first liquidity is added from the pool's page once it exists."));
    return { node, read, label: (r) => "Pool " + V.shortAddr(r.args[0][2]),
      prepare: async (r) => { const sc = await V.node.getSC(r.args[0][2], { code: true }).catch(() => null); return sc && sc.code ? null : { asset: "No contract at that id on your node: check the id, or wait until its install is in a block." }; },
      review: (r) => { const a = r.args[0][2], known = D.assets.get(a);
        return [["What", "A pool: a constant-product market for an asset that already exists, owned by its liquidity providers."], ["Asset", h("span", { class: "mono wz-id" }, a)], known ? ["Listed as", U.clean(known.ticker + " · " + known.name, 60)] : ["Listed as", "not a DMZ coin: it shows an “asset unknown” badge"],
          ["Priced in", qWords(quote)], ["Trading fee", pct(D.cfg.feeBps) + " of every trade; " + pct(D.cfg.pfeeBps) + " of it to the protocol treasury, the rest to liquidity providers"], ["Treasury", h("span", { class: "mono wz-id" }, treas())], ["Liquidity", "none yet: add it from the pool's page"]]; } };
  };

  // ---- holder NFT: nft.bas ----
  X.nft = ({ fs }) => {
    const launches = D.list().filter((s) => s.kind === "launch");
    if (!launches.length) return { blocked: true, node: U.emptyState("gem", "No coin to gate on", "A holder NFT is minted only by holders of a DMZ coin, and none is listed yet.", button("Launch a coin first", "primary", () => V.wizard.open("launch"))), read: () => ({ ok: false, errors: {} }) };
    const sel = h("select", { name: "token" }, launches.map((s) => h("option", { value: s.scid }, U.clean(s.ticker, 12) + " · " + U.clean(s.name, 40))));
    const f = { name: inp({ name: "nname", placeholder: "Name", maxlength: 64 }), descr: h("textarea", { name: "ndescr", placeholder: "Description", maxlength: 240 }), icon: inp({ name: "nicon", placeholder: "https://…", inputmode: "url" }), file: inp({ name: "nfile", placeholder: "https://…", inputmode: "url" }),
      tags: inp({ name: "ntags", placeholder: "#tags", maxlength: 100 }), coll: inp({ name: "ncoll", placeholder: "Optional", maxlength: 64 }), minhold: inp({ name: "minhold", value: "1000", inputmode: "decimal" }), blocks: inp({ name: "blocks", value: "1000", inputmode: "numeric" }), max: inp({ name: "max", value: "100", inputmode: "numeric" }), price: inp({ name: "price", value: "1", inputmode: "decimal" }) };
    const tok = () => D.assets.get(sel.value);
    const read = (ref) => C.nft(Object.fromEntries(Object.entries(f).map(([k, el]) => [k, el.value])), { token: tok(), treasury: treas(), feeBps: D.cfg.feeBps, iconRef: ref });
    const node = h("div", null, group("Holder gate"),
      h("div", { class: "stack mt8" }, fs.field("token", "Coin to hold", sel), h("div", { class: "row wz-2" }, fs.field("minhold", "Minimum hold (tokens)", f.minhold), fs.field("blocks", "Hold blocks", f.blocks, "1 to 10,000,000."))),
      group("Edition"), h("div", { class: "row wz-2 mt8" }, fs.field("max", "Max editions", f.max), fs.field("price", "Mint price (DERO)", f.price)),
      group("Headers"), h("div", { class: "stack mt8" }, fs.field("name", "Name", f.name, "Up to 64 bytes."), fs.field("descr", "Description", f.descr), fs.field("file", "Image URL (https)", f.file, "Other DERO tools may show it; this app never loads it."),
        fs.field("icon", "Icon URL (https)", f.icon, iconHint), fs.field("tags", "Tags", f.tags), fs.field("coll", "Collection name", f.coll, "A label other tools group by. It is not a contract and it is not reserved.")));
    return { node, read, label: (r) => r.args[0][2],
      review: (r) => { const a = Object.fromEntries(r.args.map(([k, , v]) => [k, v])), t = tok();
        return [["What", "A holder NFT: editions only holders of " + U.clean(t.ticker, 12) + " can mint."], ["Name", a.name], ["Description", a.descr || "none"], ["Image URL", a.file || "none"], ["Tags", a.tags || "none"], ["Collection name", a.collection || "none"],
          ["Gate", "lock at least " + A.fmtUnits(a.minhold, t.dec, Number(t.dec)) + " " + U.clean(t.ticker, 12) + " for " + a.holdblocks + " blocks, then mint one edition per lock; unlock at any time"],
          ["Editions", String(a.maxsupply) + " at most, " + (a.price > Z ? A.fmtDero(a.price, 5) + " DERO each" : "free") + "; " + pct(a.fee) + " of each price to the protocol treasury, the rest to you"], ["Treasury", h("span", { class: "mono wz-id" }, a.treasury)], ["Creator", "your wallet's address, recorded by the install"]]; } };
  };

  // ---- NFA: nfa.bas (docs/NFA_BUILD.md §3.C). protoFee and treasury have no field: loadNFA refuses any but the deployment's ----
  X.nfa = ({ fs, opts }) => {
    const reg = D.registry;
    if (!reg || !V.canonAddress(reg.treasury)) return { blocked: true, node: h("div", { class: "errbox mt" }, "The factory's treasury could not be read from your node, and an NFA must name it. Refresh and try again."), read: () => ({ ok: false, errors: {} }) };
    const lid = "wzcoll" + Date.now();
    const f = { name: inp({ name: "aname", placeholder: "Name", maxlength: 64 }), type: inp({ name: "atype", placeholder: "Image, Audio, Video…", maxlength: 32 }), descr: h("textarea", { name: "adescr", placeholder: "Description", maxlength: 240 }), icon: inp({ name: "aicon", placeholder: "https://…", inputmode: "url" }),
      tags: inp({ name: "atags", placeholder: "#tags", maxlength: 100 }), coll: inp({ name: "acoll", placeholder: opts.collection ? "The collection's name" : "Optional", maxlength: 64, list: lid }),
      file: inp({ name: "afile", placeholder: "https://…", inputmode: "url" }), sign: inp({ name: "asign", placeholder: "https://…", inputmode: "url" }), checkC: inp({ name: "checkC", placeholder: "C line", class: "mono" }), checkS: inp({ name: "checkS", placeholder: "S line", class: "mono" }), cover: inp({ name: "acover", placeholder: "https://…", inputmode: "url" }),
      royalty: inp({ name: "royalty", value: "5", inputmode: "numeric" }), art: inp({ name: "art", value: "0", inputmode: "numeric" }) };
    const upd = h("input", { type: "checkbox", name: "canUpdate" });
    const fee = BigInt(D.cfg.feeBps || 0);
    // XMR and ZEC first, DERO last; a wrapped asset's id is its vault's, and a superseded vault is not offered
    const quotes = vaults().concat([{ scid: "", sym: "DERO", dec: 5 }]); let quote = quotes[0];
    // the collection names this wallet's own NFAs already carry, offered, never enforced
    const names = [...new Set([...D.nfas.values()].filter((n) => W.canon && V.canonAddress(n.creator) === W.canon && n.collection).map((n) => U.clean(n.collection, 64)))];
    const read = (ref) => C.nfa(Object.assign(Object.fromEntries(Object.entries(f).map(([k, el]) => [k, el.value])), { canUpdate: upd.checked }), { quote, protoFee: fee, treasury: reg.treasury, iconRef: ref });
    const coll = fs.field("coll", "Collection name", f.coll, opts.collection ? "Mint each piece of the collection as its own NFA under this same name. The name is not reserved: anyone can use it." : "Optional. NFAs that share a name form a collection; the name is not reserved.");
    const node = h("div", null,
      opts.collection ? h("div", { class: "stack mt" }, coll) : null,
      group("Headers"), h("div", { class: "stack mt8" }, h("div", { class: "row wz-2" }, fs.field("name", "Name", f.name, "Up to 64 bytes."), fs.field("type", "Type", f.type)), fs.field("descr", "Description", f.descr),
        fs.field("tags", "Tags", f.tags), opts.collection ? null : coll, fs.field("icon", "Icon URL (https)", f.icon, iconHint)),
      h("datalist", { id: lid }, names.map((n) => h("option", { value: n }))),
      h("div", { class: "stack mt" }, quotePick(fs, quotes, quote, (q) => { quote = q; fs.onchange(); })),
      group("File"), h("div", { class: "stack mt8" }, fs.field("file", "File URL (https)", f.file), fs.field("sign", "Signature URL (.sign)", f.sign), h("div", { class: "row wz-2" }, fs.field("checkC", "Check C", f.checkC), fs.field("checkS", "Check S", f.checkS)),
        h("div", { class: "note" }, "The C and S lines of the DERO wallet's filesign, fixed, so the file can be checked after its URL changes."), fs.field("cover", "Cover URL (https)", f.cover)),
      group("Shares of every sale"), h("div", { class: "stack mt8" }, h("div", { class: "row wz-2" }, fs.field("royalty", "Royalty % to you", f.royalty), fs.field("art", "Artificer %", f.art)),
        h("div", { class: "note" }, "The artificer share is the standard's donation to its authors: 0 unless you choose to give. The " + pct(fee) + " protocol fee is this deployment's, paid to " + V.shortAddr(reg.treasury) + "."),
        h("label", { class: "wz-check" }, upd, h("span", null, "The owner may update the URLs and tags too", h("span", { class: "note", style: "display:block" }, "Otherwise only you, the creator, can. Name, description, type, checks and collection never change.")))));
    upd.onchange = () => fs.onchange();
    return { node, read, label: (r) => r.args[3][2],
      review: (r) => { const a = Object.fromEntries(r.args.map(([k, , v]) => [k, v])), q = quote.sym;
        return [["What", "An NFA: a single NFT. One contract, one unit, sent to your wallet by the install."], ["Name", a.nameHdr], ["Type", a.typeHdr || "none"], ["Description", a.descrHdr || "none"], ["Tags", a.tagsHdr || "none"],
          ["Collection", a.collection ? a.collection + ". A shared name, not reserved: anyone can mint under it" : "none"], ["File", a.fileURL || "none"], ["Signature", a.fileSignURL || "none"], ["Checks", a.fileCheckC ? "C " + a.fileCheckC + " · S " + a.fileCheckS : "none"], ["Cover", a.coverURL || "none"],
          ["Priced in", qWords(quote)], ["Every sale pays", pct(a.protoFee) + " to the protocol treasury, " + a.royalty + "% royalty to you" + (a.artificerFee > Z ? ", " + a.artificerFee + "% to the standard's authors" : "") + ", the rest to the owner, in " + q],
          ["Updates", a.ownerCanUpdate ? "you and the owner may change the URLs and tags" : "only you may change the URLs and tags"], ["Treasury", h("span", { class: "mono wz-id" }, a.treasury)]]; } };
  };

  F.create = () => V.wizard.open();
  F.launch = () => V.wizard.open("launch");
  F.pool = () => V.wizard.open("pool");
  F.nft = () => V.wizard.open("nft");
  F.nfa = () => V.wizard.open("nfa");
  F.collection = () => V.wizard.open("nfa", { collection: true });

  F.profile = (p) => {
    const name = inp({ value: p.name || "", maxlength: 32, placeholder: "Display name", "aria-label": "Display name" }), bio = h("textarea", { maxlength: 160, placeholder: "Bio", "aria-label": "Bio" }, p.bio || ""), av = inp({ value: p.avatar || "", placeholder: "Avatar URL", "aria-label": "Avatar URL" });
    const field = (label, input) => h("label", { class: "field" }, label, input);
    openSheet(h("h2", null, "Edit profile"), h("div", { class: "sub mt8" }, "What this address shows other people. Your balances and your trades are not part of it."),
      h("div", { class: "stack mt" }, field("Name", name), field("Bio", bio), field("Avatar URL", av)),
      h("div", { class: "warnbox mt" }, "This is stored in the clear on the social contract, under your address, for anyone to read. Ring size 2: the transaction names your address."),
      button("Save", "primary wide mt", async () => { if (badUrl(av.value)) return toast("Avatar URL must start with https://", "err"); closeSheet(); const ok = await V.app.tx({ scid: D.cfg.social, entry: "Profile", args: [["name", "S", name.value], ["bio", "S", bio.value], ["avatar", "S", av.value]], describe: "Update profile" }); if (ok) { await D.loadSocial(W.canon); router.dispatch(); } }));
  };
})();
