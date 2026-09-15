// DMZ founders.js — the Founders Pass data layer, split out of feed.js (DOC size). Adds loadFounders / myFounders
// to V.data; feed.js calls loadFounders on every refresh. Same rules as every listing: code verified against its
// template hash once per session, fixed key reads, per-user keys by exact key only.
//
// founders.bas v2 keeps one reward pool per asset. A deposit of asset i raises ac_<i>, the reward per registered pass,
// and a registration is a position p (po_ owner, pu_ units, pt_ and pc_ the ticks it opened and closed at). Claim(p, i,
// ka, kb) pays p's owner pu × (to − from) in asset i: from is pd_<i>_<p> once p has claimed, else the accumulator at
// pt_; to is ac_<i> while p is open, else the accumulator at pc_. "The accumulator at tick t" is ra_<i>_<k> for the
// first record k with rt_<i>_<k> ≥ t, else ac_<i>: ka and kb are those k, found here by binary search with exact-key
// reads, and the contract checks each in O(1) (docs/FOUNDERS_V2_NOTES.md §4).
(function () {
  const V = window.Veil, B = BigInt, clean = (s, n) => V.ui.clean(s, n);
  const isNum = (v) => typeof v === "string" && /^\d+$/.test(v), isId = (v) => typeof v === "string" && /^[0-9a-f]{64}$/.test(v);
  const DERO = "0".repeat(64), POSN = 100; // positions read per refresh, newest first: a bounded window, like listings
  Object.assign(V.data, {
    founders: null,
    // The assets the pass page names: DERO, and every token with a verified DMZ market, as its token or as its quote.
    // Anyone can deposit any token and so give it an index; a token this app cannot name is never read, never shown
    // and never called a fee asset.
    passAssets() {
      const out = new Map([[DERO, { id: DERO, sym: "DERO", dec: 5n }]]);
      for (const s of this.assets.values()) {
        if (!s || s.untrusted || (s.kind !== "pool" && s.kind !== "launch")) continue;
        if (isId(s.asset) && !out.has(s.asset)) out.set(s.asset, { id: s.asset, sym: clean(s.ticker || "", 10) || s.asset.slice(0, 6), dec: typeof s.dec === "bigint" ? s.dec : 5n });
        if (isId(s.quote) && !out.has(s.quote)) out.set(s.quote, { id: s.quote, sym: clean(s.quoteSym || "", 10) || s.quote.slice(0, 6), dec: s.quoteDecUnknown ? null : B(Number(s.quoteDec) || 5) });
      }
      return [...out.values()];
    },
    async loadFounders(prev) {
      const scid = this.cfg.founders; if (!scid) return null;
      if ((await this.verify(scid)) !== "founders") { console.warn("founders contract does not match its template"); return null; }
      const NUM = ["v", "price", "maxsupply", "start", "reserve", "share", "mfee", "minted", "hst", "ev", "pn", "an", "ln", "la", "mdep"];
      const r = await V.node.getSC(scid, { keys: [...NUM, "nameHdr", "descrHdr", "iconURLHdr", "fileURL", "creator", "treasury"] });
      // this page reads v2's keys, so a pass of any other version is not shown, whatever template hash its code matched
      if (r.vals.v !== "2") { console.warn("founders contract is not a v2 pass"); return null; }
      const num = (k) => (isNum(r.vals[k]) ? B(r.vals[k]) : 0n), str = (k, n) => clean(V.hexToStr(r.vals[k] || ""), n);
      const f = { scid, kind: "founders", name: str("nameHdr", 64) || "Founders Pass", descr: str("descrHdr", 240), icon: str("iconURLHdr", 200), file: str("fileURL", 200), creator: this.rawToAddr(r.vals.creator || ""), treasury: this.rawToAddr(r.vals.treasury || ""), listings: [], assets: [] };
      for (const k of NUM) f[k] = num(k);
      // the named assets' indices by exact key (ai_<hex id> = i + 1), never by walking `an`, which anyone can grow
      const named = this.passAssets(), ai = await this.fetchKeys(scid, named.map((a) => "ai_" + a.id));
      f.assets = named.filter((a) => isNum(ai["ai_" + a.id]) && ai["ai_" + a.id] !== "0").map((a) => Object.assign({}, a, { i: B(ai["ai_" + a.id]) - 1n }));
      const AK = ["ac", "au", "ap", "ad", "aw", "ar"], got = f.assets.length ? await this.fetchKeys(scid, f.assets.flatMap((a) => AK.map((k) => k + "_" + a.i))) : {};
      for (const a of f.assets) for (const k of AK) a[k] = isNum(got[k + "_" + a.i]) ? B(got[k + "_" + a.i]) : 0n;
      f.sig = ["minted", "hst", "ev", "pn", "an", "ln", "la", "mdep"].map((k) => r.vals[k] || "").concat(f.assets.map((a) => a.i + ":" + a.ac + ":" + a.aw)).join("|");
      if (prev && prev.sig === f.sig) { f.listings = prev.listings; return f; }
      // listings: the last 300 ids (deleted ones simply do not exist)
      const from = Math.max(0, Number(f.ln) - 300), keys = []; for (let i = from; i < Number(f.ln); i++) keys.push("ls_" + i, "lp_" + i, "lu_" + i);
      const lg = keys.length ? await this.fetchKeys(scid, keys) : {};
      for (let i = from; i < Number(f.ln); i++) { const seller = V.hexToStr(lg["ls_" + i] || ""); if (V.canonAddress(seller) && isNum(lg["lp_" + i]) && isNum(lg["lu_" + i])) f.listings.push({ id: i, seller, price: B(lg["lp_" + i]), units: B(lg["lu_" + i]) }); }
      f.listings.sort((a, b) => (a.price < b.price ? -1 : a.price > b.price ? 1 : a.id - b.id));
      return f;
    },
    // The connected wallet's registered units, its positions with what each can claim per named asset (the contract's
    // arithmetic, exactly), any XMR or ZEC address it stored while those were paid (read only so it can be cleared), and
    // its listings.
    async myFounders(addr) {
      const f = this.founders; if (!f || !addr) return null;
      const k = await this.fetchKeys(f.scid, ["hu_" + addr, "xm_" + addr, "zc_" + addr]);
      // xm_ and zc_ are strings, which the node returns as hex: always decoded, never read as a number
      const text = (x) => (typeof x === "string" && /^[0-9a-f]+$/.test(x) ? clean(V.hexToStr(x), 106) : "");
      const last = Number(f.pn) - 1, from = Math.max(0, Number(f.pn) - POSN), pk = [];
      for (let p = last; p >= from; p--) pk.push("po_" + p, "pu_" + p, "pt_" + p, "pc_" + p);
      const g = pk.length ? await this.fetchKeys(f.scid, pk) : {}, positions = [];
      for (let p = last; p >= from; p--) {
        if (!g["po_" + p] || this.rawToAddr(g["po_" + p]) !== addr || !["pu_", "pt_", "pc_"].every((x) => isNum(g[x + p]))) continue;
        positions.push({ p: B(p), u: B(g["pu_" + p]), pt: B(g["pt_" + p]), pc: B(g["pc_" + p]), owed: [] });
      }
      const rec = new Map();
      const read = async (key) => {
        if (!rec.has(key)) { const x = await this.fetchKeys(f.scid, [key]); rec.set(key, isNum(x[key]) ? B(x[key]) : null); }
        const v = rec.get(key); if (v === null) throw new Error("the pass has no " + key); return v;
      };
      // founders.bas at(): the first record k of asset a with rt >= t, or ar when there is none
      const hint = async (a, t) => { let lo = 0n, hi = a.ar; while (lo < hi) { const mid = (lo + hi) / 2n; if ((await read("rt_" + a.i + "_" + mid)) >= t) hi = mid; else lo = mid + 1n; } return lo; };
      const accAt = (a, kk) => (kk === a.ar ? a.ac : read("ra_" + a.i + "_" + kk));
      const pdk = positions.flatMap((q) => f.assets.map((a) => "pd_" + a.i + "_" + q.p)), pd = pdk.length ? await this.fetchKeys(f.scid, pdk) : {};
      for (const q of positions) for (const a of f.assets) {
        const c = pd["pd_" + a.i + "_" + q.p]; let ka = 0n, kb = 0n, lo, hi;
        if (isNum(c)) lo = B(c); else { ka = await hint(a, q.pt); lo = await accAt(a, ka); }
        if (q.pc === 0n) hi = a.ac; else { kb = await hint(a, q.pc); hi = await accAt(a, kb); }
        const amt = hi > lo ? q.u * (hi - lo) : 0n;
        // Claim refuses more than the pool owes (amt > ap_), a state the contract cannot reach: offer nothing then
        if (amt > 0n && amt <= a.ap) q.owed.push({ asset: a, amt, ka, kb });
      }
      return { units: isNum(k["hu_" + addr]) ? B(k["hu_" + addr]) : 0n, positions, windowed: from > 0, xmr: text(k["xm_" + addr]), zec: text(k["zc_" + addr]), listings: f.listings.filter((l) => l.seller === addr) };
    },
  });
})();
