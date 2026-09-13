// DMZ zswap.js — the transparent-Zcash swap market (docs/ZEC_SWAP.md §8): the order board read from board.bas, the
// Post and Accept forms, and each swap's page at #zswap/<id>, whose DERO leg is read live from swap.bas. This app
// cannot read Zcash, so that leg is the companion's (tools/zecswap) and the screen says so where it matters. It holds
// no key and stores nothing: a secret the maker generates here lives in this page's memory until it is handed to the
// companion, and losing it after the lock costs a refund at T_dero, not the coins.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, B = BigInt, N = Number;
  const { h, render, button, kv, router, openSheet, closeSheet, toast } = U;
  const me = () => V.wallet.canon, BLOCK = 18, SCAN = 120;
  const isNum = (x) => typeof x === "string" && /^\d+$/.test(x);
  const u = (x) => (isNum(x) ? B(x) : null), str = (x) => (x ? V.hexToStr(x) : ""), who = (x) => (x ? D.rawToAddr(x) : null);
  const hex64 = (x) => /^[0-9a-f]{64}$/.test(x || "");
  // docs/ZEC_SWAP.md §1, word for word, above the fields of both forms, every time either opens
  const PRIVACY = "This trade is visible on the Zcash chain forever: the amount, your transparent address, the maker's, and the link to your DERO address. Zcash's shielded pool cannot be used here.";
  const privacy = () => h("div", { class: "warnbox" }, h("b", null, PRIVACY), h("div", { class: "sub mt8" }, D.allVaults().length
    ? "The private route on this network is the wrapped path, which is custody by a federation (the Wrap tab). A shielded-Zcash swap is not built."
    : "There is no private route for Zcash on this network: wrapped assets are not offered, and a shielded-Zcash swap is not built."));
  const RING2 = "Ring size 2: the contract reads who signed, so your DERO address is public in this call.";
  const field = (l, i) => h("label", { class: "field" }, h("span", null, l), i);
  const inp = (ph, v, mono) => h("input", { placeholder: ph, value: v || "", autocomplete: "off", spellcheck: "false", class: mono ? "mono" : null });
  const full = (l, v) => h("button", { class: "mono sub", type: "button", "aria-label": "Copy " + l, style: "display:block;width:100%;text-align:left;word-break:break-all;line-height:1.5", onclick: () => navigator.clipboard && navigator.clipboard.writeText(v).then(() => toast(l + " copied", "ok")) }, v);

  // ---- data ----
  const Zs = (V.zswap = { state: "", offers: [], locks: [], at: 0, h: 0, fail: "" });
  const tip = () => (V.live && V.live.height) || D.height || 0;
  // An id is trusted only once the contract says what it is: board v3 and swap v2 are the shapes every key below is
  // read in. The code itself is your companion's to check (tools/zecswap compares it with the source).
  async function head(scid, kind, v) {
    const r = await V.node.getSC(scid, { keys: ["kind", "v", "n", "aw", "wmin"] });
    return V.hexToStr(r.vals.kind || "") === kind && r.vals.v === String(v) ? r.vals : null;
  }
  async function rows(scid, n, pre) {
    const from = Math.max(0, n - SCAN), keys = [];
    for (let i = from; i < n; i++) for (const p of pre) keys.push(p + i);
    const got = keys.length ? await D.fetchKeys(scid, keys) : {}, out = [];
    for (let i = from; i < n; i++) { const r = { id: i }; for (const p of pre) r[p] = got[p + i]; out.push(r); }
    return out;
  }
  Zs.load = async () => {
    const c = D.cfg;
    if (!hex64(c.board) || !hex64(c.swap)) return (Zs.state = "none");
    const [bh, sh] = await Promise.all([head(c.board, "board", 3), head(c.swap, "swap", 2)]);
    if (!bh || !sh) return (Zs.state = "wrong");
    const [bo, lo] = await Promise.all([rows(c.board, N(bh.n || 0), ["m_", "c_", "ga_", "gv_", "wv_", "wa_", "h_", "e_", "st_", "t_", "rp_", "cl_", "ad_"]),
      rows(c.swap, N(sh.n || 0), ["m_", "t_", "h_", "a_", "v_", "e_", "st_", "x_"])]);
    Zs.aw = u(bh.aw); Zs.wmin = u(sh.wmin);
    Zs.offers = bo.map((r) => ({ id: r.id, maker: who(r.m_), chain: U.clean(str(r.c_), 16), asset: str(r.ga_), give: u(r.gv_), want: u(r.wv_), addr: U.clean(str(r.wa_), 128),
      hash: str(r.h_), exp: u(r.e_), st: u(r.st_), taker: who(r.t_), rp: str(r.rp_), cltv: u(r.cl_), ad: u(r.ad_) }));
    Zs.locks = lo.map((r) => ({ id: r.id, maker: who(r.m_), taker: who(r.t_), hash: str(r.h_), asset: r.a_ || "", v: u(r.v_), exp: u(r.e_), st: u(r.st_), x: str(r.x_) }));
    Zs.at = Date.now(); Zs.h = tip(); Zs.state = "ok"; Zs.fail = "";
  };
  // the lock that belongs to an accepted offer: the same hash, maker and taker (the latest, if a maker locked twice)
  const lockOf = (o) => { let l = null; for (const x of Zs.locks) if (x.hash === o.hash && x.maker === o.maker && x.taker === o.taker) l = x; return l; };
  // Where a row stands, from the two contracts' own states and the tip. Nothing is assumed: an accepted offer with no
  // lock by its ad_ is dead to its taker (board.bas v3), and a lock is only ever claimed, refunded or still open.
  function where(o) {
    const H = B(tip()), l = o.st === 1n ? lockOf(o) : null;
    if (o.st === 2n) return ["closed", "Cancelled by the maker"];
    if (o.st === 0n) return o.exp !== null && H >= o.exp ? ["closed", "Expired unaccepted at block " + o.exp] : ["open", "Open"];
    if (o.st !== 1n) return ["closed", "Unreadable"];
    if (!l) return o.ad !== null && H >= o.ad ? ["dead", "Dead since block " + o.ad + ": accepted, and the maker never locked"] : ["live", "Accepted · the maker must lock by block " + o.ad];
    if (l.st === 1n) return ["closed", "Swapped: the taker's DERO is claimed"];
    if (l.st === 2n) return ["closed", "Refunded to the maker at the lock's expiry"];
    return l.exp !== null && H >= l.exp ? ["live", "Lock expired unclaimed: only the maker's refund is left"] : ["live", "DERO locked for the taker until block " + l.exp];
  }
  // the maker's record on this board: accepted offers that ended in a swap, and ones the maker let die or refunded
  const record = (m) => { let done = 0, gone = 0; for (const o of Zs.offers) { if (o.maker !== m || o.st !== 1n) continue; const w = where(o)[1]; if (/^Swapped/.test(w)) done++; else if (/^(Dead|Refunded|Lock expired)/.test(w)) gone++; } return done + " completed · " + gone + " abandoned"; };
  // An asset is named and scaled only by a DMZ listing; anything else is an id this app cannot scale, and says so
  const assetOf = (ga) => { if (!ga) return { sym: "DERO", dec: 5n }; const m = D.list().find((x) => x.asset === ga); return m ? { sym: m.ticker, dec: B(m.dec) } : { sym: "asset " + ga.slice(0, 8), dec: null }; };
  const amt = (a, v) => (v === null ? "?" : a.dec === null ? v + " units of " + a.sym + " (scale unknown)" : A.fmtUnits(v, a.dec, 6) + " " + a.sym);
  const zec = (v) => (v === null ? "?" : A.fmtUnits(v, 8n, 8) + " ZEC");
  const price = (o, a) => (a.dec === null || !o.give || !o.want ? "no price: the asset's scale is unknown" : (N(o.want) / 1e8 / (N(o.give) / 10 ** N(a.dec))).toPrecision(5) + " ZEC per " + a.sym);
  // A deadline is a DERO block. Its time is an estimate at 18 s a block; the block is what the contract reads.
  const when = (b) => { if (b === null) return "—"; const t = tip(), d = N(b) - t; return "block " + b + (!t ? "" : d <= 0 ? " · passed" : " · about " + new Date(Date.now() + d * BLOCK * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })); };
  const left = (b) => { const d = b === null ? 0 : N(b) - tip(); if (d <= 0) return "ended"; const m = Math.ceil((d * BLOCK) / 60); return m < 90 ? m + " min left" : Math.round(m / 60) + " h left"; };

  // ---- a transparent Zcash key address, as the 20-byte key hash the script needs; a reason when it is not one ----
  const B58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz";
  const sha = async (b) => new Uint8Array(await crypto.subtle.digest("SHA-256", b));
  async function taddr(s) {
    const a = String(s || "").trim(), main = D.cfg.network === "mainnet";
    if (/^[zu]/i.test(a)) return { why: "That is a shielded or unified address. The swap script can only use a transparent one: Zcash's shielded pool cannot be used here." };
    let n = 0n;
    for (const ch of a) { const i = B58.indexOf(ch); if (i < 0) return { why: "Not a Zcash transparent address." }; n = n * 58n + B(i); }
    const b = []; while (n > 0n) { b.unshift(N(n & 255n)); n >>= 8n; }
    if (b.length !== 26) return { why: "Not a Zcash transparent address." };
    const body = new Uint8Array(b.slice(0, 22)), d = await sha(await sha(body));
    if (b.slice(22).some((x, i) => x !== d[i])) return { why: "The address's checksum does not match: check it for a typo." };
    const pre = (body[0] << 8) | body[1];
    if (pre === (main ? 0x1cbd : 0x1cba)) return { why: "That is a script (P2SH) address. The swap needs a key address, one starting " + (main ? "t1" : "tm") + "." };
    if (pre !== (main ? 0x1cb8 : 0x1d25)) return { why: "That is not a " + (main ? "mainnet (t1…)" : "test-network (tm…)") + " address, and this build is for " + D.cfg.network + "." };
    return { pkh: V.bytesToHex(body.slice(2)), addr: a };
  }
  // the one secret this page may make: in this page's memory only, never stored, never sent, gone with the page
  let secret = null;
  async function makeSecret() { const b = new Uint8Array(32); crypto.getRandomValues(b); secret = { s: V.bytesToHex(b), h: V.bytesToHex(await sha(b)) }; return secret; }
  const matches = async (s, hash) => /^[0-9a-f]{64}$/.test(s) && V.bytesToHex(await sha(V.hexToBytes(s))) === hash;

  // every call names the key its success must move, so a reverted call is reported as one (app.tx's probe)
  // fee: a call gated on a height past the one the daemon's estimator runs at (0) is fee'd without an estimate
  const send = async (scid, entry, args, describe, keys, check, fee) => { const ok = await V.app.tx(Object.assign({ scid, entry, args, describe, probe: { scid, keys, check } }, fee ? { fee } : {})); if (ok) { await Zs.load().catch(() => {}); D.changed(); } return ok; };

  // ---- Post: the maker's offer ----
  function postSheet() {
    const net = D.cfg.network === "mainnet" ? "t1…" : "tm…";
    const f = { asset: inp("blank for DERO, or the asset's 64-hex id", "", 1), give: inp("amount you give"), want: inp("ZEC you want"), addr: inp("your transparent address, " + net, "", 1),
      hash: inp("the 64-hex hash from your companion", "", 1), life: inp("blocks the offer stays open", "2000") };
    const sec = h("div");
    const read = async () => {
      const ga = f.asset.value.trim().toLowerCase(), a = assetOf(ga);
      if (ga && !hex64(ga)) return { why: "The asset is blank for DERO, or its 64-hex id." };
      if (a.dec === null) return { why: "That asset is not a DMZ listing, so this app cannot scale it and will not write an amount for it." };
      const give = A.parseUnits(f.give.value, a.dec), want = A.parseUnits(f.want.value, 8n), life = Math.floor(N(f.life.value));
      if (!give || !want) return { why: "Enter both amounts." };
      if (!(life >= 1 && life <= 1000000)) return { why: "An offer stays open from 1 to 1,000,000 blocks." };
      const hash = f.hash.value.trim();
      if (!/^[0-9a-f]{64}$/.test(hash)) return { why: "The hash is 64 lower-case hex characters: SHA-256 of the secret only you hold." };
      const t = await taddr(f.addr.value); if (t.why) return t;
      return { ga, give, want, hash, addr: t.addr, exp: B(tip() + life) };
    };
    const gen = button("Generate a secret here instead", "small ghost", async () => {
      const s = await makeSecret(); f.hash.value = s.h;
      render(sec, h("div", { class: "warnbox mt8" }, h("b", null, "Your secret, shown now and only now"), full("Secret", s.s),
        h("div", { class: "sub mt8" }, "Hand it to your companion before you close this page: it is kept only in this page's memory, never stored and never sent. Lose it before you lock and it costs nothing. " +
          "Lose it after you lock and you cannot claim the ZEC: your DERO comes back to you at the lock's expiry (T_dero), so a lost secret costs a refund, not the coins.")));
    });
    const btn = button("Post the offer", "primary wide mt", async () => {
      const r = await read(); if (r.why) return toast(r.why, "err", 8000);
      closeSheet();
      send(D.cfg.board, "Post", [["chain", "S", "zcash-t"], ["giveAsset", "S", r.ga], ["giveAmount", "U", r.give], ["wantAmount", "U", r.want], ["wantAddr", "S", r.addr], ["hash", "S", r.hash], ["expiry", "U", r.exp]],
        "Post a ZEC offer", ["n"], async (p0, p1) => {
          const n0 = N(p0.vals.n || 0), n1 = N(p1.vals.n || 0), ks = []; for (let i = n0; i < n1 && ks.length < 16; i++) ks.push("h_" + i);
          const g = ks.length ? await D.fetchKeys(D.cfg.board, ks) : {}; return ks.some((k) => V.hexToStr(g[k] || "") === r.hash);
        });
    });
    openSheet(h("h2", null, "Sell for ZEC"), privacy(),
      h("div", { class: "stack mt" }, field("You give (blank: DERO)", f.asset), field("Amount you give", f.give), field("ZEC you want", f.want), field("Pay the ZEC to", f.addr),
        field("Hash of your secret", f.hash), gen, sec, field("Offer stays open for (blocks)", f.life)),
      h("div", { class: "note mt" }, "Once a taker accepts, you have " + (Zs.aw || "the board's") + " blocks (about 1 h) to lock the DERO leg, and a lock is at least " + (Zs.wmin || "the escrow's minimum of") +
        " blocks wide (about 8 h): that is how long your asset is committed if the taker walks away. Your companion sends the lock and claims the ZEC with your secret, which publishes it; this page does neither. " +
        "Post pays a DERO network fee, shown by your wallet; the lock's fee is DERO too. " + RING2), btn);
  }

  // ---- Accept: the taker's answer ----
  function acceptSheet(o) {
    const a = assetOf(o.asset), ref = inp("your transparent refund address, " + (D.cfg.network === "mainnet" ? "t1…" : "tm…"), "", 1), zt = inp("the Zcash height now"), out = h("div", { class: "note" });
    let cl = null;
    const recalc = () => { const t = Math.floor(N(zt.value)); cl = t > 0 && t + 96 < 500000000 ? t + 96 : null;
      out.textContent = cl ? "Your refund height (cltv): Zcash block " + cl + ", the height you gave plus 96 blocks (about 2 h). This app cannot read Zcash, so it trusts the height you give for this one number; your companion reads it itself." : "Give the Zcash height, from your companion or a Zcash explorer you choose, and the refund height is computed from it."; };
    zt.oninput = recalc; recalc();
    const btn = button("Accept the offer", "primary wide mt", async () => {
      const t = await taddr(ref.value); if (t.why) return toast(t.why, "err", 8000);
      if (!cl) return toast("Give the Zcash height first", "err"); closeSheet();
      send(D.cfg.board, "Accept", [["id", "U", B(o.id)], ["refundPkh", "S", t.pkh], ["cltv", "U", B(cl)]], "Accept ZEC offer #" + o.id, ["st_" + o.id, "t_" + o.id],
        (p0, p1) => p1.vals["st_" + o.id] === "1" && D.rawToAddr(p1.vals["t_" + o.id] || "") === me());
    });
    openSheet(h("h2", null, "Buy with ZEC"), privacy(),
      h("div", { class: "stack mt" }, kv("You get", amt(a, o.give)), kv("You pay", zec(o.want)), kv("Price", price(o, a)), field("Your transparent refund address", ref), field("Zcash height now", zt), out),
      h("div", { class: "note mt" }, "What it costs: the Accept fee in DERO, shown by your wallet, and the Zcash funding fee (ZIP-317: at least 10,000 zatoshi), paid by your companion. The maker may claim the ZEC after 10 Zcash confirmations, about 12.5 min. " +
        "If the maker never claims, your ZEC is back after about 2 hours, and you pay two Zcash fees. After you accept, the maker has " + (Zs.aw || "the board's") + " blocks (about 1 h) to lock the DERO leg. " +
        "Fund the Zcash script only through your companion, which checks that lock first: this page cannot fund Zcash. " + RING2), btn);
  }

  // ---- stages (app/track.js): the DERO side read here, the Zcash side only your companion sees ----
  const FEE = "The maker pays a 0.5% fee in ZEC, from the ZEC he receives, in his claim transaction. That output pays one public treasury address, so it is visible on Zcash's transparent chain.";
  const dirOf = (o) => (!me() ? null : o.maker === me() ? "withdrawal" : o.taker === me() ? "deposit" : null);
  const want = () => ({ program: "zecswap", network: D.cfg.network, contracts: [D.cfg.swap] });
  const fmt = { foreign: zec, dero: (a, v) => amt(assetOf(a), v) };
  function stages(o, l, H, dep) {
    const T = V.track, h0 = B(H), acc = o.st === 1n, S = (x) => Object.assign({ chain: "DERO", conf: null, need: null, deadline: null, txid: "" }, x), dl = (b) => (b === null ? null : { chain: "DERO", height: N(b), eta: null });
    const gone = o.st === 2n || (o.st === 0n && o.exp !== null && h0 >= o.exp) || (acc && !l && o.ad !== null && h0 >= o.ad), open = l && l.st === 0n, late = open && l.exp !== null && h0 >= l.exp, deep = open && dep !== null && dep !== undefined;
    const tail = (x) => (gone ? "skipped" : x);
    return [
      S({ label: "The maker posts the offer", state: "done" }),
      S({ label: "A taker accepts it", state: acc ? "done" : o.st === 0n && !gone ? "active" : o.st === 2n ? "skipped" : "failed", deadline: o.st === 0n ? dl(o.exp) : null }),
      S({ label: "The maker locks the DERO side in the escrow" + (open && !deep ? " (its depth could not be read)" : ""), state: l ? (deep && dep < T.STABLE ? "active" : "done") : acc ? (gone ? "failed" : "active") : tail("waiting"), conf: deep ? dep : null, need: deep ? T.STABLE : null, deadline: acc && !l ? dl(o.ad) : null }),
      S({ label: "The taker funds the Zcash script, refundable to her from Zcash block cltv", chain: "Zcash", state: gone ? "skipped" : "unseen" }),
      S({ label: "The maker claims the ZEC with his secret, which makes it public", chain: "Zcash", state: gone ? "skipped" : "unseen" }),
      S({ label: "The taker's DERO side is claimed with that secret, before T_dero", state: l && l.st === 1n ? "done" : l && l.st === 2n ? "skipped" : open ? (late ? "skipped" : "active") : tail("waiting"), deadline: l ? dl(l.exp) : null }),
      S({ label: "Or, from T_dero, the maker refunds it", state: l && l.st === 2n ? "done" : l && l.st === 1n ? "skipped" : open ? (late ? "active" : "waiting") : tail("waiting") }),
    ];
  }

  Zs.stages = stages;

  // ---- screens ----
  const flag = (k) => h("span", { class: "pill " + (k === "open" ? "up" : k === "dead" ? "bad" : k === "live" ? "warn" : "") }, k);
  // the age of every figure on these screens is the age of the read that produced it (BL-129)
  const readLine = () => h("div", { class: "note mt" }, (Zs.fail ? "The last read failed (" + Zs.fail + "). " : "") + "Read from your node at block " + Zs.h + ", ",
    h("span", { "data-ts": Math.floor(Zs.at / 1000) }, U.timeAgo(Math.floor(Zs.at / 1000)) + " ago"),
    ". Board " + V.shortAddr(D.cfg.board) + " and escrow " + V.shortAddr(D.cfg.swap) + " are the ids this build names; this page checks their kind and version, and your companion checks their code.");
  const row = (o) => { const a = assetOf(o.asset), [k, w] = where(o), d = dirOf(o);
    return h("button", { class: "feed-item", type: "button", onclick: () => router.go("zswap", String(o.id)) },
      h("div", { class: "body" }, h("div", { class: "between" }, h("b", null, amt(a, o.give)), h("b", { class: "num" }, zec(o.want))),
        h("div", { class: "between", style: "gap:10px" }, h("span", { class: "meta" }, (d ? (d === "deposit" ? "Your deposit" : "Your withdrawal") + " · " : "") + "#" + o.id + " · " + price(o, a) + " · maker " + record(o.maker)), h("span", { class: "meta" }, k === "open" ? left(o.exp) : w)))); };
  function list(el, redraw) {
    const all = [...Zs.offers].reverse(), g = (ks) => all.filter((o) => ks.includes(where(o)[0]));
    const open = g(["open"]), live = g(["live"]), gone = g(["dead", "closed"]).slice(0, 20);
    render(el, h("h1", { class: "mt" }, "ZEC swap"),
      h("div", { class: "sub mt8" }, "Buy a DERO-side asset with transparent ZEC, or sell one for ZEC. The DERO leg is a lock in swap.bas; the Zcash leg is a script your companion builds, funds and watches. " +
        "Nobody holds a coin for anyone: each side keeps an exit of its own for as long as it acts inside its window. The swap programs have completed every exit against real Monero and Zcash node software on a local test chain; neither has yet run on a public test network."),
      h("div", { class: "note mt8" }, FEE),
      V.track.panel("zecswap", want(), () => !!router.current && router.current.name === "zswap", redraw, fmt, tip()),
      h("div", { class: "row mt" }, button("Sell for ZEC", "primary", postSheet)),
      U.sectionHead("trend", "Buy with ZEC", "Open offers · pick one to accept it"),
      open.length ? h("div", { class: "list mt8" }, open.map(row)) : U.emptyState("trend", null, "No open offer on this board."),
      live.length ? [U.sectionHead("bolt", "In progress", "Accepted offers and their DERO locks"), h("div", { class: "list mt8" }, live.map(row))] : null,
      gone.length ? [U.sectionHead("feed", "Closed and dead", "Swapped, refunded, cancelled, expired, or accepted and never locked"), h("div", { class: "list mt8" }, gone.map(row))] : null,
      readLine());
  }
  function page(el, id, redraw) {
    const o = Zs.offers.find((x) => x.id === N(id));
    if (!o) return render(el, h("h1", { class: "mt mb" }, "ZEC swap #" + U.clean(id, 12)), h("div", { class: "errbox" }, "This board has no offer with that number."), readLine());
    const a = assetOf(o.asset), l = o.st === 1n ? lockOf(o) : null, [k, w] = where(o), H = tip(), mine = !!me() && o.maker === me(), acts = [], d = dirOf(o), p = V.track.paired("zecswap");
    // the stages: the paired companion's own, when it is this offer's lock; else the DERO side read here
    const track = [h("h3", { class: "mt" }, "Stages"), p && l && p.lock === l.id ? V.track.panel("zecswap", want(), () => !!router.current && router.current.name === "zswap", redraw, fmt, H)
      : [V.track.render(stages(o, l, H, l && Zs.depth && Zs.depth.id === l.id ? Zs.depth.d : null), H), h("div", { class: "note mt8" }, "The Zcash side is visible only to your swap program. Pair with it on the ZEC swap screen to see every stage here.")]];
    if (k === "open" && !mine) acts.push(button("Accept: buy with ZEC", "primary wide", () => acceptSheet(o)));
    // board.bas Cancel: the maker's, any time while open; once accepted, only from the offer's expiry or its ad_
    if (mine && o.st !== 2n && (o.st === 0n || H >= N(o.exp) || H >= N(o.ad))) acts.push(button("Cancel the offer", "wide", () => send(D.cfg.board, "Cancel", [["id", "U", B(o.id)]], "Cancel offer #" + o.id, ["st_" + o.id], (p0, p1) => p1.vals["st_" + o.id] === "2", o.st === 1n ? 2000 : 0)));
    if (l && l.st === 0n && H >= N(l.exp) && me() === l.maker) acts.push(button("Refund your DERO", "wide", () => send(D.cfg.swap, "Refund", [["id", "U", B(l.id)]], "Refund lock #" + l.id, ["st_" + l.id], (p0, p1) => p1.vals["st_" + l.id] === "2", 2000)));
    const sx = inp("the 64-hex secret s", "", 1);
    const claim = l && l.st === 0n && H < N(l.exp) ? [h("h3", { class: "mt" }, "Claim for the taker"),
      h("div", { class: "note mt8" }, "Once the maker claims the ZEC, the secret s is public on Zcash: your companion or any Zcash explorer shows it. This app cannot read Zcash, so paste it here. Anyone may send this claim. " +
        "It pays the taker named in the lock, " + V.shortAddr(l.taker) + ", never you: you pay only the DERO network fee. " + RING2),
      h("div", { class: "mt8" }, field("Secret s", sx)),
      button("Claim for the taker", "primary wide mt8", async () => {
        const s = sx.value.trim().toLowerCase();
        if (!(await matches(s, l.hash))) return toast("That is not this lock's secret: its SHA-256 is not the lock's hash. Nothing was sent.", "err", 8000);
        send(D.cfg.swap, "Claim", [["id", "U", B(l.id)], ["secret", "S", s]], "Claim for the taker", ["st_" + l.id], (p0, p1) => p1.vals["st_" + l.id] === "1");
      })] : null;
    render(el,
      h("div", { class: "between mt" }, h("h1", null, "ZEC swap #" + o.id), flag(k)), h("div", { class: "sub mt8" }, w),
      d ? h("div", { class: "mt8" }, h("b", null, V.track.dirText(d, zec(o.want), amt(a, o.give)))) : null,
      h("h3", { class: "mt" }, "The offer · board.bas"),
      kv("Maker gives", amt(a, o.give)), kv("Maker wants", zec(o.want)), kv("Price", price(o, a)),
      kv("Maker", h("span", null, h("span", { class: "mono" }, V.shortAddr(o.maker)), " · " + record(o.maker))), kv("Open until", when(o.exp)),
      o.st === 1n ? [kv("Taker", h("span", { class: "mono" }, V.shortAddr(o.taker))), kv("Maker must lock by", when(o.ad)),
        kv("Taker's refund height", "Zcash block " + o.cltv + " (this app cannot read Zcash, so it cannot give this as a time)"), kv("Taker's refund key hash", h("span", { class: "mono" }, o.rp))] : null,
      h("div", { class: "eyebrow mt" }, "Pay the ZEC to (the maker's address)"), full("Address", o.addr), h("div", { class: "eyebrow mt" }, "Hash"), full("Hash", o.hash),
      h("h3", { class: "mt" }, "The DERO leg · swap.bas"),
      l ? [kv("Lock", "#" + l.id + " · " + amt(assetOf(l.asset), l.v)), kv("For the taker", h("span", { class: "mono" }, V.shortAddr(l.taker))), kv("Claimable until (T_dero)", when(l.exp)),
        kv("State", l.st === 1n ? "claimed for the taker" : l.st === 2n ? "refunded to the maker" : H >= N(l.exp) ? "expired unclaimed: the maker may refund" : "open"),
        l.x ? [h("div", { class: "eyebrow mt" }, "The secret, public since the claim"), full("Secret", l.x)] : null]
        : h("div", { class: "note mt8" }, o.st === 1n ? "No lock under this offer's hash, maker and taker yet." : "Nothing is locked until an offer is accepted."),
      h("h3", { class: "mt" }, "The Zcash leg"),
      h("div", { class: "note mt8" }, "Not visible from here: this app cannot read Zcash. Your companion (tools/zecswap) builds, funds, watches and claims it, and a Zcash explorer you choose shows it too."),
      acts.length ? h("div", { class: "stack mt" }, acts) : null, claim, track, readLine());
  }

  router.on("zswap", async (el, param) => {
    const title = () => h("h1", { class: "mt mb" }, "ZEC swap");
    const draw = () => {
      if (Zs.state === "none") return render(el, title(), U.emptyState("trend", "No swap board on this network", "This build names no swap board or swap escrow, so there is nothing to show and nothing to sign."));
      if (Zs.state === "wrong") return render(el, title(), h("div", { class: "errbox" }, "The contracts this build names as the swap board and the escrow are not board v3 and swap v2 on your node, so nothing is shown and nothing can be signed."));
      if (Zs.state === "err") return render(el, title(), U.errorState("The swap board could not be read from your node: " + Zs.fail, () => reload()));
      return param ? page(el, param, redraw) : list(el, redraw);
    };
    const here = () => !!router.current && router.current.name === "zswap" && router.current.param === param;
    const reload = async () => {
      try {
        await Zs.load();
        // an offer's open lock: its depth against the stable 8, read once it is found (app/track.js)
        const o = param ? Zs.offers.find((x) => x.id === N(param)) : null, l = o && o.st === 1n ? lockOf(o) : null;
        if (l && l.st === 0n) Zs.depth = { id: l.id, d: await V.track.depth(D.cfg.swap, "st_" + l.id, tip()) };
      } catch (e) { Zs.fail = U.clean(String((e && e.message) || e), 160); if (Zs.state !== "ok") Zs.state = "err"; }
      if (here()) draw();
    };
    const busy = () => [...el.querySelectorAll("input")].some((i) => i.value && i.value.trim());
    const redraw = () => { if (here() && !busy()) draw(); };
    render(el, title(), h("div", { class: "list" }, U.skeleton(3)));
    await reload();
    V.track.watch("zecswap", want(), here, redraw);
    // one read per block while this screen is open, and none while a field on it holds text
    D.onChange(() => { if (router.current && router.current.name === "zswap" && !busy() && Date.now() - Zs.at > 4000) reload(); });
  });
  // a build that names no board has no swap screen to offer: the rail loses the tab rather than open an empty page
  if (!hex64(D.cfg.board)) { const t = document.querySelector('.tab[data-tab="zswap"]'); if (t) t.remove(); }
})();
