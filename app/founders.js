// DMZ founders.js — the Founders Pass data layer, split out of feed.js (DOC size). Adds loadFounders / myFounders
// to V.data; feed.js calls loadFounders on every refresh. Same rules as every listing: code verified against its
// template hash once per session, fixed key reads, per-user keys by exact key only.
(function () {
  const V = window.Veil, B = BigInt, clean = (s, n) => V.ui.clean(s, n);
  const isNum = (v) => typeof v === "string" && /^\d+$/.test(v);
  Object.assign(V.data, {
    // ---- Founders Pass (one contract, from config; code verified against its template hash) ----
    founders: null,
    async loadFounders(prev) {
      const scid = this.cfg.founders; if (!scid) return null;
      if ((await this.verify(scid)) !== "founders") { console.warn("founders contract does not match its template"); return null; }
      const NUM = ["price", "maxsupply", "start", "reserve", "share", "mfee", "minted", "hst", "minhst", "acc", "rpool", "und", "dep", "mdep", "paid", "hn", "ln", "la", "era"];
      const r = await V.node.getSC(scid, { keys: [...NUM, "nameHdr", "descrHdr", "iconURLHdr", "fileURL", "creator", "treasury"] });
      const num = (k) => (isNum(r.vals[k]) ? B(r.vals[k]) : 0n), str = (k, n) => clean(V.hexToStr(r.vals[k] || ""), n);
      const f = { scid, kind: "founders", name: str("nameHdr", 64) || "Founders Pass", descr: str("descrHdr", 240), icon: str("iconURLHdr", 200), file: str("fileURL", 200), creator: this.rawToAddr(r.vals.creator || ""), treasury: this.rawToAddr(r.vals.treasury || ""), listings: [] };
      for (const k of NUM) f[k] = num(k);
      f.sig = ["minted", "hst", "acc", "rpool", "und", "dep", "paid", "hn", "ln", "la"].map((k) => r.vals[k] || "").join("|");
      if (prev && prev.sig === f.sig) { f.listings = prev.listings; return f; }
      // listings: the last 300 ids (deleted ones simply do not exist)
      const from = Math.max(0, Number(f.ln) - 300), keys = []; for (let i = from; i < Number(f.ln); i++) keys.push("ls_" + i, "lp_" + i, "lu_" + i);
      const got = keys.length ? await this.fetchKeys(scid, keys) : {};
      for (let i = from; i < Number(f.ln); i++) { const seller = V.hexToStr(got["ls_" + i] || ""); if (V.canonAddress(seller) && isNum(got["lp_" + i]) && isNum(got["lu_" + i])) f.listings.push({ id: i, seller, price: B(got["lp_" + i]), units: B(got["lu_" + i]) }); }
      f.listings.sort((a, b) => (a.price < b.price ? -1 : a.price > b.price ? 1 : a.id - b.id));
      return f;
    },
    // the connected wallet's registered units, pending reward (exact contract arithmetic), and listings.
    // settle() (founders.bas:263-288) sums one term per era the holder has not been settled through, then the
    // term for the current era, and caps the total at rpool. It has to: distribute() restarts acc at 0 when a
    // deposit would wrap it past 2^64 and files the pre-wrap accumulator under ea_<era>, so a hd_ snapshot taken
    // in an earlier era cannot be subtracted from this era's acc. One term is only right until the first roll.
    async myFounders(addr) {
      const f = this.founders; if (!f || !addr) return null;
      const k = await this.myKeys(f.scid, addr, ["hu_", "hd_", "he_"]);
      const hu = k.hu || 0n; let hd = k.hd || 0n, pend = 0n;
      if (hu > 0n) {
        const he = k.he || 0n, keys = []; for (let e = he; e < f.era; e++) keys.push("ea_" + e);
        const got = keys.length ? await this.fetchKeys(f.scid, keys) : {};
        for (let e = he; e < f.era; e++) { const ea = isNum(got["ea_" + e]) ? B(got["ea_" + e]) : 0n; if (ea > hd) pend += (hu * (ea - hd)) / 4294967296n; hd = 0n; }
        // the comparisons only fire on a state the contract cannot produce (hd_ is always a past value of the
        // accumulator it is subtracted from); without them a missing key would print a negative reward
        if (f.acc > hd) pend += (hu * (f.acc - hd)) / 4294967296n;
      }
      return { units: hu, pending: pend > f.rpool ? f.rpool : pend, listings: f.listings.filter((l) => l.seller === addr) };
    },
  });
})();
