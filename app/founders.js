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
      const NUM = ["price", "maxsupply", "start", "reserve", "share", "mfee", "minted", "hst", "acc", "rpool", "und", "dep", "mdep", "paid", "hn", "ln", "la"];
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
    // the connected wallet's registered units, pending reward (exact contract arithmetic), and listings
    async myFounders(addr) {
      const f = this.founders; if (!f || !addr) return null;
      const k = await this.myKeys(f.scid, addr, ["hu_", "hd_"]);
      const hu = k.hu || 0n, hd = k.hd || 0n; const pend = hu > 0n && f.acc > hd ? (hu * (f.acc - hd)) / 4294967296n : 0n;
      return { units: hu, pending: pend > f.rpool ? f.rpool : pend, listings: f.listings.filter((l) => l.seller === addr) };
    },
  });
})();
