// DMZ wizard.js — the creation flow in five steps: Create (what kind of thing), Configure (its fields, forms.js),
// Review (every parameter in words, the cost itemised, how many wallet confirmations and why), Pay (the two or three
// wallet steps, each written to this device's record as it lands, so a closed page picks up where it stopped) and
// Launch. The rules are create.js's. Every transaction goes through app.tx, the one door to the wallet; nothing here
// signs, holds a key or talks to a host but the user's node and wallet.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, C = V.create, { h, render, toast, openSheet, closeSheet, button, router } = U;
  const WZ = (V.wizard = {}), STEPS = ["Create", "Configure", "Review", "Pay", "Launch"], HEX64 = /^[0-9a-f]{64}$/;
  const store = { getItem: (k) => { try { return localStorage.getItem(k); } catch (e) { return null; } }, setItem: (k, v) => { try { localStorage.setItem(k, v); } catch (e) {} } };
  const short = (m) => String((m && m.message) || m || "").split("\n")[0].slice(0, 180);
  const big = (v) => (typeof v === "bigint" ? v : /^\d+$/.test(String(v)) ? BigInt(v) : null);
  const dero = (v) => A.fmtDero(v, 5) + " DERO";
  const RING2 = "Ring size 2: every one of these transactions names your address, and the contract records it as the creator.";
  // V.profile is the on-chain profile document (another file): used only when all three calls exist, checked at use
  const prof = () => { const p = V.profile; return p && typeof p.build === "function" && typeof p.install === "function" && typeof p.ref === "function" ? p : null; };
  const refOf = (scid) => (prof() ? prof().ref(scid) : "dmz:" + scid);
  WZ.KINDS = {
    launch: { what: "coin", title: "Launch a coin", reg: "RegisterLaunch", route: "token", icon: "icon", is: "A coin is a market: a new token with a fixed supply, sold on a bonding curve that graduates into a locked AMM." },
    pool: { what: "pool", title: "Create a pool", reg: "RegisterPool", route: "token", is: "A pool is a market for an asset that already exists: constant-product, owned by its liquidity providers." },
    nfa: { what: "NFA", title: "Mint an NFA", reg: "RegisterNFA", route: "nfa", icon: "iconURLHdr", is: "An NFA is a single NFT: one contract, one unit, which you hold. Its market is inside it and every sale pays your royalty." },
    nft: { what: "holder NFT", title: "Create a holder NFT", reg: "RegisterNFT", route: "nft", icon: "icon", is: "A holder NFT is an edition only holders of a coin can mint: lock the coin for a number of blocks, then mint one edition per lock." },
  };
  const CHOICES = [["launch", "Coin", "A market. A new token, a bonding curve, then a locked AMM.", "launch.bas"], ["pool", "Pool", "A market for an asset that already exists.", "pool.bas"],
    ["nfa", "Single NFT (NFA)", "One asset per contract. You hold its only unit; it sells itself and pays you a royalty.", "nfa.bas"],
    ["collection", "NFT collection", "Not a contract: the collection name NFAs share. Mint each piece as an NFA under one name. Names are not reserved: anyone can use any name, so buyers check the creator.", "nfa.bas · collection header"],
    ["nft", "Holder NFT", "Editions only holders of a coin can mint, one per lock of the coin.", "nft.bas"]];

  const code$ = new Map();
  WZ.template = async (kind) => {
    if (code$.has(kind)) return code$.get(kind);
    const t = D.cfg.templates[kind]; const r = await fetch(t.file); if (!r.ok) throw new Error("template " + kind + " missing"); const code = await r.text();
    if (t.sha256 && (await V.sha256Hex(code)) !== t.sha256) throw new Error("template hash mismatch"); code$.set(kind, code); return code;
  };
  // The node's estimate for exactly this call, the way app.tx asks for it (SIGNER() is read, so the signer is named)
  const estimate = async (o) => {
    const w = V.wallet; if (!w.connected) return { gas: null, why: "connect" };
    const args = o.args.map(([name, datatype, value]) => ({ name, datatype, value })), transfers = [];
    try {
      if (o.dero > 0n) { const dst = await V.node.randomAddress(); if (!V.isAddress(dst)) throw new Error("the node returned no burn destination"); transfers.push({ destination: dst, amount: 0, burn: o.dero }); }
      const sc_rpc = o.code ? [...args, { name: "SC_ACTION", datatype: "U", value: 1 }, { name: "SC_CODE", datatype: "S", value: o.code }] : [{ name: "entrypoint", datatype: "S", value: o.entry }, ...args, { name: "SC_ACTION", datatype: "U", value: 0 }, { name: "SC_ID", datatype: "H", value: o.scid }];
      const r = await V.node.call("DERO.GetGasEstimate", Object.assign({ transfers, sc_rpc, ringsize: 2, signer: w.address }, o.code ? { sc: o.code } : { scid: o.scid }));
      const g = big(r && r.gasstorage); return g === null ? { gas: null, why: "unreadable" } : { gas: g + 200n };
    } catch (e) { return o.code && /Bucket cannot start/.test(String(e.message)) ? { gas: BigInt(o.code.length) + 512n, why: "size" } : { gas: null, why: "fail", msg: short(e) }; }
  };
  // the factory counter and slot prefix each Register* writes (factory.bas); nft_ and nfa_ share nc
  const REG = { RegisterLaunch: ["lc", "launch_", "launches"], RegisterPool: ["pc", "pool_", "pools"], RegisterNFT: ["nc", "nft_", "nfts"], RegisterNFA: ["nc", "nfa_", "nfas"] };
  const slots = async (entry, from, to) => { const [, pre] = REG[entry], ks = []; for (let i = from; i < Math.min(to, from + 64); i++) ks.push(pre + i); if (!ks.length) return []; const r = await V.node.getSC(D.cfg.factory, { keys: ks }); return ks.map((k) => V.hexToStr(r.vals[k] || "").toLowerCase()); };
  const registered = async (entry, scid) => {
    const [cnt, , list] = REG[entry];
    if (D.registry && (D.registry[list] || []).includes(scid)) return true;
    const r0 = await V.node.getSC(D.cfg.factory, { keys: [cnt] }), n = Number(r0.vals[cnt] || 0);
    return (await slots(entry, Math.max(0, n - 64), n)).includes(scid);
  };
  // Refuses before the wallet is asked: no fee read, no contract at the id (the factory would list an empty id), or
  // already listed (a second registration would pay the fee again for nothing).
  const register = async (rec) => {
    const entry = WZ.KINDS[rec.kind].reg, [cnt] = REG[entry];
    if (!D.registry || typeof D.registry.regfee !== "bigint") throw new Error("The factory's registration fee could not be read from your node: refresh, then Register again.");
    const sc = await V.node.getSC(rec.scid, { code: true }).catch(() => null);
    if (!sc || !sc.code) throw new Error("No contract at " + V.shortAddr(rec.scid) + " on your node yet: the install is not in a block, or it did not execute. Nothing was sent.");
    if (await registered(entry, rec.scid).catch(() => false)) return "already";
    return V.app.tx({ scid: D.cfg.factory, entry, args: [["scid", "H", rec.scid]], dero: D.registry.regfee, describe: "Register on the factory", probe: { scid: D.cfg.factory, keys: [cnt], check: async (p0, p1) => {
      // our id must be among the entries added, not only a registration that landed in the same window
      const n0 = Number(p0.vals[cnt] || 0), n1 = Number(p1.vals[cnt] || 0); return n1 > n0 && (await slots(entry, n0, n1)).includes(rec.scid); } } });
  };

  // ---- fields: a label on every one, its error tied to it by aria-describedby, shown once it has been left ----
  let uid = 0;
  WZ.fields = () => {
    const reg = {}, touched = new Set(), fs = { onchange: () => {} };
    fs.field = (key, label, input, hint, extra) => {
      const id = "wz" + ++uid, grp = input.tagName === "DIV", err = h("div", { class: "wz-err", id: id + "e", hidden: true });
      if (grp) { input.setAttribute("role", input.getAttribute("role") || "group"); input.setAttribute("aria-labelledby", id + "l"); } else input.id = id;
      input.setAttribute("aria-describedby", (hint ? id + "h " : "") + id + "e");
      const left = () => { touched.add(key); fs.onchange(); };
      input.addEventListener("change", left); input.addEventListener("focusout", left); input.addEventListener("input", () => fs.onchange());
      reg[key] = { input, err };
      return h("div", { class: "field wz-f" }, h(grp ? "span" : "label", { for: grp ? null : id, id: id + "l" }, label), input, extra || null, hint ? h("div", { class: "note", id: id + "h" }, hint) : null, err);
    };
    fs.show = (errs, all) => {
      let first = null; if (all) Object.keys(reg).forEach((k) => touched.add(k));
      for (const [k, { input, err }] of Object.entries(reg)) {
        const m = touched.has(k) ? errs[k] || "" : ""; err.textContent = m; err.hidden = !m;
        if (m) input.setAttribute("aria-invalid", "true"); else input.removeAttribute("aria-invalid");
        if (m && !first) first = input.tagName === "DIV" ? input.querySelector("button") || input : input;
      }
      if (first) { const d = first.closest("details"); if (d) d.open = true; }
      return first;
    };
    return fs;
  };
  // a pick-one row of chips, announced as radios
  WZ.choice = (items, cur, label, pick) => {
    const el = h("div", { class: "chips wz-chips", role: "radiogroup" });
    const set = (q) => [...el.children].forEach((c, i) => { c.classList.toggle("on", items[i] === q); c.setAttribute("aria-checked", String(items[i] === q)); });
    items.forEach((q) => el.append(h("button", { class: "chip", type: "button", role: "radio", onclick: () => { set(q); pick(q); } }, label(q))));
    set(cur); return el;
  };

  // ---- logo and links, through the profile document ----
  WZ.brand = (fs, what) => {
    if (!prof()) return { off: true, node: h("div", { class: "note" }, "Unavailable in this build. A logo and links are kept in an on-chain profile document, and this build has no module that writes one. The icon header can still take an https link (in the fields above)."), read: () => ({ ok: true, errors: {}, any: false }) };
    let image = null;
    const file = h("input", { type: "file", name: "logo", accept: "image/png,image/jpeg,image/webp,image/gif" }), thumb = h("div", { class: "wz-logo", "aria-hidden": "true" }), meta = h("span", { class: "note" });
    const drop = button("Remove", "small ghost", () => { file.value = ""; image = null; paint(); fs.onchange(); });
    const paint = () => {
      render(thumb); meta.textContent = image ? U.clean(image.name, 40) + " · " + Math.ceil(image.size / 1024) + " KB" : "No logo chosen"; drop.hidden = !image;
      // the user's own file, read on this device: no request leaves it
      if (image && /^image\//.test(image.type)) { const r = new FileReader(); r.onload = () => { if (typeof r.result === "string" && r.result.startsWith("data:image/")) render(thumb, h("img", { src: r.result, alt: "" })); }; r.readAsDataURL(image); }
    };
    file.onchange = () => { image = (file.files && file.files[0]) || null; paint(); fs.onchange(); };
    const ins = {};
    const node = h("div", { class: "stack" },
      h("div", { class: "note" }, "Optional. They are stored in the clear, on chain, in a profile document installed before the " + what + ", and its id goes in the icon header. The image is inside that document: nothing is fetched from anyone's server."),
      fs.field("logo", "Logo image", file, "PNG, JPEG, WebP or GIF.", h("div", { class: "row" }, thumb, meta, drop)),
      C.LINKS.map(([k, label, , eg]) => fs.field(k, label, (ins[k] = h("input", { name: "link-" + k, type: "url", inputmode: "url", placeholder: eg, autocomplete: "off", spellcheck: "false" })))));
    paint();
    return { node, read: () => {
      const r = C.links(Object.fromEntries(Object.entries(ins).map(([k, el]) => [k, el.value])));
      if (image && !/^image\/(png|jpeg|webp|gif)$/.test(image.type)) r.errors.logo = "The logo must be a PNG, JPEG, WebP or GIF image.";
      return { ok: !Object.keys(r.errors).length, errors: r.errors, image, links: r.links, any: r.any || !!image };
    } };
  };

  // ---- the sheet ----
  let st = null, root = null, shown = -1;
  const vis = (my) => st === my && root && root.isConnected;
  const bar = (i) => h("ol", { class: "wz-steps", "aria-label": "Steps" }, STEPS.map((t, j) => h("li", { class: j < i ? "done" : j === i ? "now" : "", "aria-current": j === i ? "step" : null }, h("span", { class: "n" }, String(j + 1)), h("span", { class: "t" }, t), j < i ? h("span", { class: "sr" }, " (done)") : null)));
  const actions = (...b) => h("div", { class: "wz-act" }, b);
  const dl = (rows) => h("dl", { class: "wz-rv" }, rows.filter(Boolean).map(([k, v]) => h("div", null, h("dt", null, k), h("dd", null, v))));
  const draw = () => {
    if (!root || !root.isConnected) return;
    render(root, bar(st.step), [createStep, configureStep, reviewStep, payStep, launchStep][st.step]());
    if (shown !== st.step) { shown = st.step; const t = root.querySelector("h2"); if (t) { t.setAttribute("tabindex", "-1"); setTimeout(() => t.focus(), 80); } }
  };
  WZ.open = (kind, opts) => { st = { step: 0 }; root = h("div", { class: "wz" }); shown = -1; openSheet(root); if (kind) choose(kind, opts); else draw(); };

  const choose = (kind, opts) => {
    const k = WZ.KINDS[kind], fs = WZ.fields();
    st = { step: 1, kind, k, fs };
    st.brand = k.icon ? WZ.brand(fs, k.what) : null;
    st.spec = V.forms.specs[kind]({ fs, brand: st.brand, opts: opts || {} });
    fs.onchange = () => { if (st.spec.update) st.spec.update(); validate(false); };
    if (st.spec.update) st.spec.update();
    draw();
  };
  const validate = (all) => {
    const bd = st.brand ? st.brand.read() : { ok: true, errors: {}, any: false };
    const res = st.spec.read(bd.any ? refOf(C.ZEROQ) : null);
    const errs = Object.assign({}, res.errors, bd.errors);
    return { bd, res, errs, first: st.fs.show(errs, all) };
  };
  WZ.brandAny = () => !!(st && st.brand && !st.brand.off && st.brand.read().any);

  function createStep() {
    const mine = C.load(store).filter((r) => r.net === D.cfg.network && r.factory === D.cfg.factory);
    return [h("h2", null, "Create"), h("div", { class: "sub mt8" }, "What are you making? Each one is its own contract, installed from a template this app checks by hash and then registered on the factory. There is no limit on how many you make, and nobody approves them."),
      h("div", { class: "wz-kinds mt" }, CHOICES.map(([k, t, d, c]) => h("button", { class: "wz-kind", type: "button", onclick: () => (k === "collection" ? choose("nfa", { collection: true }) : choose(k)) }, h("b", null, t), h("span", { class: "sub" }, d), h("span", { class: "mono tiny dim2" }, c)))),
      mine.length ? [h("h3", { class: "mt" }, "Unfinished on this device"), h("div", { class: "note" }, "Your own installs that did not reach the end. Each is checked against your node before anything is offered."), mine.map(recRow)] : null];
  }
  const recRow = (rec) => {
    const k = WZ.KINDS[rec.kind], stat = h("div", { class: "note" }, "Reading it from your node…"), acts = h("div", { class: "row mt8" });
    (async () => {
      const has = async (id) => { if (!id) return null; const sc = await V.node.getSC(id, { code: true }).catch(() => null); return sc ? !!sc.code : null; };
      const chain = { profile: await has(rec.profile), code: await has(rec.scid), registered: rec.scid ? await registered(k.reg, rec.scid).catch(() => null) : false };
      const n = C.next(rec, chain);
      if (n === "done") { C.drop(store, rec.id); stat.textContent = "Registered and listed: removed from this list."; return; }
      stat.textContent = { register: "Installed as " + V.shortAddr(rec.scid) + ", not registered yet.", install: "Its profile document is installed; the " + k.what + " itself is not.", wait: "Sent; not in a block yet.", lost: "Not found on your node: the install never landed, or it did not execute." }[n];
      render(acts, n === "register" ? button("Register", "small", () => resume(rec)) : n === "install" ? button("Install it", "small", () => resume(rec)) : n === "wait" ? button("Check again", "small", draw) : null,
        button("Forget", "small ghost", () => { C.drop(store, rec.id); draw(); }));
    })();
    return h("div", { class: "wz-rec" }, h("b", null, U.clean(rec.label || k.what, 60)), h("span", { class: "sub tiny" }, " · " + k.what), stat, acts);
  };
  const rowsFor = (rec) => [rec.want || rec.profile ? { k: "profile", label: "Install the logo and links document", s: rec.profile ? "done" : "todo", d: rec.profile } : null,
    { k: "install", label: "Install the " + WZ.KINDS[rec.kind].what, s: rec.scid ? "done" : "todo", d: rec.scid }, { k: "register", label: "Register it on the factory", s: "todo" }].filter(Boolean);
  const resume = (rec) => { const r = Object.assign({}, rec, { args: C.argsIn(rec.args) }); st = { step: 3, kind: r.kind, k: WZ.KINDS[r.kind], rec: r, rows: rowsFor(r), bd: { any: false } }; draw(); run(); };

  function configureStep() {
    return [h("h2", null, st.k.title), h("div", { class: "sub mt8" }, st.k.is), st.spec.node,
      st.brand ? [h("h3", { class: "mt" }, "Logo and links"), st.brand.node] : null,
      actions(button("Back", "", () => { st.step = 0; draw(); }), st.spec.blocked ? null : button("Review", "primary", review))];
  }
  const review = async () => {
    const v = validate(true);
    if (Object.keys(v.errs).length) { if (v.first) v.first.focus(); toast(Object.keys(v.errs).length > 1 ? "Fix the " + Object.keys(v.errs).length + " marked fields first" : Object.values(v.errs)[0], "err", 6000); return; }
    if (st.spec.prepare) { const why = await st.spec.prepare(v.res); if (why) { const f = st.fs.show(why, true); if (f) f.focus(); return; } }
    Object.assign(st, { res: v.res, bd: v.bd, est: null, build: null, step: 2 }); draw(); estimateAll();
  };
  const estimateAll = async () => {
    const my = st, e = (my.est = { profile: undefined, install: null, reg: null, notes: [], block: [] });
    if (my.bd.any) {
      e.profile = null;
      try { my.build = await prof().build({ image: my.bd.image || null, links: my.bd.links }); e.profile = big(my.build && my.build.gasEstimate); } catch (x) { e.block.push("The profile document could not be built: " + short(x)); }
    }
    const gate = await V.app.netGate().catch((x) => short(x)); if (gate) e.block.push(gate);
    if (!D.registry || typeof D.registry.regfee !== "bigint") e.block.push("The factory's registration fee could not be read from your node: refresh, then try again.");
    if (!gate && V.wallet.connected) {
      try { const r = await estimate({ code: await WZ.template(my.kind), args: my.res.args }); e.install = r.gas; if (r.why === "fail") e.block.push("Your node says this install would fail: " + r.msg); if (r.why === "size") e.notes.push("Your node cannot simulate this install; the fee is set from its size, as app.tx does."); } catch (x) { e.block.push(short(x)); }
      if (D.registry) { const r = await estimate({ scid: D.cfg.factory, entry: my.k.reg, args: [["scid", "H", D.cfg.factory]], dero: D.registry.regfee }); e.reg = r.gas; }
    }
    e.done = true; if (vis(my) && my.step === 2) draw();
  };
  function reviewStep() {
    const e = st.est || {}, p = !!st.bd.any, conn = V.wallet.connected, fee = D.registry ? D.registry.regfee : null;
    const c = C.costs({ what: st.k.what, profile: p, profileGas: e.profile === undefined ? null : e.profile, installGas: e.install, regGas: e.reg, regfee: typeof fee === "bigint" ? fee : null });
    const g = (v) => (typeof v === "bigint" ? "≈ " + dero(v) + " network fee" : !e.done ? "estimating…" : !conn ? "connect a wallet to estimate" : "estimated when sent");
    const ic = st.k.icon ? (st.res.args.find((a) => a[0] === st.k.icon) || [])[2] : null;
    const brandRows = !st.k.icon ? [] : [["Icon header", p ? "dmz:<the profile document's id, known after the first confirmation>" : ic || "empty"],
      p ? ["Logo", st.bd.image ? U.clean(st.bd.image.name, 40) + ", " + Math.ceil(st.bd.image.size / 1024) + " KB, inside the profile document" : "none"] : null,
      p ? ["Links", Object.keys(st.bd.links).length ? Object.entries(st.bd.links).map(([k, u]) => C.LINKS.find((l) => l[0] === k)[1] + ": " + u).join(" · ") : "none"] : null];
    return [h("h2", null, "Review"), h("div", { class: "sub mt8" }, "This is what the contract is installed with. None of it can be changed afterwards: no function in it can."),
      dl(st.spec.review(st.res).concat(brandRows)),
      h("h3", { class: "mt" }, "Cost"),
      dl(c.items.map((i) => [i.label, g(i.gas) + (typeof i.fee === "bigint" ? " + " + dero(i.fee) + " registration fee, forwarded to the treasury" : "")]).concat([["Total", (c.complete ? "≈ " : "at least ") + dero(c.total) + (c.complete ? "" : ", plus what is estimated when sent")]])),
      !conn && e.done && !e.block.length ? button("Connect wallet to estimate", "small mt8", async () => { await V.app.connectWallet(); if (V.wallet.connected) { draw(); estimateAll(); } }) : null,
      h("h3", { class: "mt" }, C.CONFIRM_WORDS[c.confirmations] + " wallet confirmations"),
      h("ol", { class: "wz-why" }, c.items.map((i) => h("li", null, h("b", null, i.label), h("div", { class: "note" }, i.why)))),
      e.notes && e.notes.length ? h("div", { class: "note mt8" }, e.notes.join(" ")) : null,
      h("div", { class: "note mt8" }, RING2 + " Each network fee is your node's estimate when you confirm, and the wallet shows it before you approve."),
      (e.block || []).map((b) => h("div", { class: "errbox mt8", role: "alert" }, b)),
      actions(button("Back", "", () => { st.step = 1; draw(); }), button("Pay and launch", "primary", startPay, { disabled: !e.done || e.block.length ? true : null }))];
  }

  const startPay = () => {
    const rec = { id: Date.now().toString(36) + Math.random().toString(36).slice(2, 7), kind: st.kind, net: D.cfg.network, factory: D.cfg.factory, ts: Date.now(), label: st.spec.label(st.res), args: st.res.args, want: !!st.bd.any };
    C.save(store, rec); Object.assign(st, { rec, rows: rowsFor(rec), step: 3 }); draw(); run();
  };
  const run = async () => {
    const my = st, rec = my.rec, row = (k) => my.rows.find((r) => r.k === k);
    const set = (k, s, d) => { const r = row(k); if (r) { r.s = s; if (d) r.d = d; } if (vis(my)) draw(); };
    my.busy = true; my.err = null; my.failed = null;
    try {
      if (row("profile") && !rec.profile) {
        set("profile", "now");
        if (!prof()) throw new Error("This build has no profile module to install the document with.");
        const p = await prof().install(my.build || (await prof().build({ image: my.bd.image || null, links: my.bd.links })));
        if (!HEX64.test(String(p || ""))) throw new Error("The profile document was not installed. Nothing else was sent.");
        rec.profile = p; C.save(store, rec); set("profile", "done", p);
      }
      if (rec.profile) { const ic = WZ.KINDS[rec.kind].icon; rec.args = rec.args.map((a) => (a[0] === ic ? [a[0], a[1], refOf(rec.profile)] : a)); C.save(store, rec); }
      if (!rec.scid) {
        set("install", "now");
        const code = await WZ.template(rec.kind), before = V.app.lastTxid;
        // the install's txid is its contract id: kept the moment the wallet returns it, so a page closed while the
        // block is awaited still finds its contract on the Create step
        const tw = setInterval(() => { const t = V.app.lastTxid; if (t && t !== before && !rec.scid) { rec.scid = t; rec.sent = Date.now(); C.save(store, rec); } }, 400);
        let id; try { id = await V.app.tx({ code, args: rec.args, describe: "Install the " + my.k.what }); } finally { clearInterval(tw); }
        if (!id && rec.scid) { const sc = await V.node.getSC(rec.scid, { code: true }).catch(() => null); if (sc && sc.code) id = rec.scid; }
        if (!id) throw Object.assign(new Error(rec.scid ? "The install was sent as " + V.shortAddr(rec.scid) + " but is not confirmed. It stays under Unfinished on the Create step; once it is in a block, Register it from there." : "The install was not sent."), { k: "install" });
        rec.scid = id; C.save(store, rec); set("install", "done", id);
      }
      set("register", "now");
      const tx = await register(rec);
      if (!tx) throw new Error("Not registered. The " + my.k.what + " is installed; Try again here, or Register it later from the Create step.");
      rec.reg = tx === "already" ? "" : tx; C.drop(store, rec.id); set("register", "done", tx === "already" ? "it was already registered" : tx);
      my.step = 4; D.refresh().catch(() => {});
      if (vis(my)) draw(); else toast("Your " + my.k.what + " is created: " + V.shortAddr(rec.scid), "ok", 9000);
    } catch (x) {
      const r = my.rows.find((q) => q.s === "now"); if (r) r.s = "fail";
      my.err = short(x); my.failed = x.k || (r && r.k); if (vis(my)) draw(); else toast(my.err, "err", 9000);
    } finally { my.busy = false; if (vis(my)) draw(); }
  };
  const WORD = { todo: "next", now: "confirm in wallet", done: "done", fail: "stopped" };
  function payStep() {
    return [h("h2", null, "Pay"), h("div", { class: "sub mt8" }, "Your wallet asks you to confirm each step. Each waits for the one before it to be in a block."),
      h("ol", { class: "steps wz-pay" }, st.rows.map((r, i) => h("li", { class: "step " + ({ done: "done", now: "now", fail: "wait" }[r.s] || "todo") },
        h("span", { class: "n" }, String(i + 1)), h("div", { class: "grow" }, h("div", { class: "between" }, h("span", { class: "t" }, r.label), h("span", { class: "sst label" }, WORD[r.s])), r.d ? h("div", { class: "d mono wz-id" }, r.d) : null)))),
      st.err ? h("div", { class: "errbox mt", role: "alert" }, st.err) : st.busy ? h("div", { class: "note mt", role: "status" }, "You can close this sheet. Each step is recorded on this device as it lands, and the Create step offers what is left.") : null,
      !st.busy && st.err ? actions(button("Close", "", closeSheet), st.failed === "install" && st.rec.scid ? button("Install again", "", () => { st.rec.scid = ""; st.rec.sent = 0; st.rows = rowsFor(st.rec); run(); }) : null, button("Try again", "primary", run)) : null];
  }
  function launchStep() {
    const r = st.rec, k = WZ.KINDS[r.kind];
    return [h("h2", { class: "wz-done" }, "Launched"), h("div", { class: "sub mt8" }, "Your " + k.what + " is installed and registered. It is listed as soon as your node has read the registration, within a block or two."),
      dl([["What", U.clean(r.label || k.what, 80)], ["Contract id", h("span", { class: "mono wz-id" }, r.scid)], r.reg ? ["Registration", h("span", { class: "mono wz-id" }, r.reg)] : null, r.profile ? ["Profile document", h("span", { class: "mono wz-id" }, r.profile)] : null]),
      actions(button("Create another", "", () => WZ.open()), button("Open it", "primary", async () => { await D.refresh().catch(() => {}); closeSheet(); router.go(k.route, r.scid); }))];
  }
})();
