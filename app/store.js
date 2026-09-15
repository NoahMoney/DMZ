// DMZ store.js — what the data layer keeps in localStorage, split out of feed.js (DOC size): a bounded copy of each
// market's history, so reopening the app is instant, kept within a least-recently-used budget, and this device's own
// trade ledger. Loaded before feed.js, which adds these to V.data (V.storeParts). Everything read back is
// shape-checked: any page on this origin can write localStorage.
(function () {
  const V = window.Veil, B = BigInt;
  // CACHE_P / CACHE_T / SAVE_EVERY: one market's copy, smaller than what memory holds and written at most every
  // 30 s. LS_BUDGET: characters all copies may take together, since the origin's quota is shared with every page on it.
  const CACHE_P = 1500, CACHE_T = 600, SAVE_EVERY = 30000, LS_BUDGET = 3000000, HEX = /^[0-9a-f]{64}$/;
  // The side a contract writes into t_<n> (pool.bas and launch.bas post()): 1 bought the asset, 2 sold it. Both
  // validators read this one list, because two hand-written copies once said 0|1 and dropped every sell from
  // the cache and from this device's own ledger.
  const SIDES = [1, 2], isSide = (v) => SIDES.includes(v);
  V.storeParts = {
    budget: LS_BUDGET, _idx: null, _used: 0,
    cacheKey(scid) { return "veil.m." + scid; },
    // The cache is a hint, never truth: any page on this origin can write localStorage, so every record is
    // shape-checked here and the newest ones are re-read from the chain before the rest is used (loadContract).
    loadCache(scid) {
      try {
        const c = JSON.parse(localStorage.getItem(this.cacheKey(scid)) || "null");
        if (this.validCache(c)) { const e = this.idx().get(scid); if (e) e[0] = Math.floor(Date.now() / 1000); return c; }
        if (c) this.drop(scid);
      } catch (e) {}
      return null;
    },
    validCache(c) {
      const int = (x) => Number.isInteger(x) && x >= 0, dec = (x) => typeof x === "string" && /^\d{1,20}$/.test(x);
      if (!c || c.v !== 4 || !int(c.tc) || !int(c.pc) || !int(c.p0) || !int(c.t0) || !Array.isArray(c.points) || !Array.isArray(c.trades)) return false;
      if (c.p0 + c.points.length !== c.pc || c.t0 + c.trades.length !== c.tc || c.points.length > CACHE_P || c.trades.length > CACHE_T) return false;
      // No pc/tc invariant: a contract writes a block's price point only when a LATER block trades, and none at
      // all before the first (pool.bas: IF LOAD("lh") == 0), so any number of published trades can stand at pc = 0.
      let h = 0;
      for (const p of c.points) { if (!Array.isArray(p) || p.length !== 4 || !int(p[0]) || p[0] < h || !int(p[1]) || !dec(p[2]) || !dec(p[3])) return false; h = p[0]; }
      let n = c.t0;
      for (const t of c.trades) { if (!Array.isArray(t) || t.length !== 7 || t[0] !== n++ || !isSide(t[1]) || !V.canonAddress(t[2]) || !dec(t[3]) || !dec(t[4]) || !int(t[5]) || !int(t[6])) return false; }
      return true;
    },
    saveCache(s, force) {
      if (s.trimmed) return; // a record cut to its recent window would overwrite a fuller copy
      if (!force && Date.now() - (s._saved || 0) < SAVE_EVERY) { s._dirty = true; return; }
      s._saved = Date.now(); s._dirty = false;
      try {
        const pts = s.points.filter((p) => !p.live).slice(-CACHE_P), tr = s.trades.slice(-CACHE_T);
        // u (last used, seconds) leads the record, so idx() can read it without parsing the copy
        this.store(this.cacheKey(s.scid), JSON.stringify({ u: Math.floor(s._saved / 1000), v: 4, tc: Number(s.tc), pc: Number(s.pc), p0: Number(s.pc) - pts.length, t0: Number(s.tc) - tr.length,
          points: pts.map((p) => [p.height, p.ts, p.rd.toString(), p.ra.toString()]), trades: tr.map((t) => [t.n, t.side, t.addr, t.in.toString(), t.out.toString(), t.height, t.ts]) }), s.scid);
      } catch (e) {}
    },
    // The copies' index, built from storage the first time it is needed: scid → [last used (s), size (chars)].
    idx() {
      if (this._idx) return this._idx;
      const m = (this._idx = new Map()); this._used = 0;
      try { for (let i = 0; i < localStorage.length; i++) { const k = localStorage.key(i); if (!k || !k.startsWith("veil.m.")) continue; const v = localStorage.getItem(k) || "", u = /^\{"u":(\d{1,12}),/.exec(v), z = v.length + k.length; m.set(k.slice(7), [u ? +u[1] : 0, z]); this._used += z; } } catch (e) {}
      return m;
    },
    drop(id) { try { localStorage.removeItem(this.cacheKey(id)); } catch (e) {} const m = this.idx(), x = m.get(id); if (x) { this._used -= x[1]; m.delete(id); } },
    // Writes one key, and says whether it did. A copy (keep: its scid) first makes room within the budget; a write
    // the browser refuses makes room and tries again: unlisted markets' copies first, then the least recently used.
    store(key, body, keep) {
      const m = this.idx(), size = body.length + key.length, old = keep ? (m.get(keep) || [0, 0])[1] : 0; let order = null, swept = false;
      const next = () => { if (!order) order = [...m].filter((e) => e[0] !== keep).sort((a, b) => a[1][0] - b[1][0]).map((e) => e[0]); return order.shift(); };
      // evicted in one batch down to 90% of the budget, so the order is sorted once per batch and not per write
      if (keep && this._used - old + size > this.budget) for (let id; this._used - old + size > this.budget * 0.9 && (id = next());) this.drop(id);
      for (;;) {
        try { localStorage.setItem(key, body); if (keep) { this._used += size - (m.has(keep) ? m.get(keep)[1] : 0); m.set(keep, [Math.floor(Date.now() / 1000), size]); } return true; }
        catch (e) { if (!swept && (swept = true) && this.sweepCache()) continue; const id = next(); if (!id) return false; this.drop(id); }
      }
    },
    // Removes the cached copy of every market the registry does not list (delisted, or another network's) and
    // says whether anything went. Judged by the registry, not this.assets: on a cold start the assets map is
    // empty while the copies are exactly what the first load is about to read.
    sweepCache() {
      const reg = this.registry; if (!reg) return false;
      const keep = new Set([...reg.launches, ...reg.pools]); let n = 0;
      try { for (const k of Object.keys(localStorage)) if (k.startsWith("veil.m.") && !keep.has(k.slice(7))) { this.drop(k.slice(7)); n++; } } catch (e) {}
      return n > 0;
    },

    flushCache() { for (const s of this.assets.values()) if (s._dirty) this.saveCache(s, true); },
    fromCache(scid) {
      const c = this.loadCache(scid); if (!c) return null;
      return { tc: c.tc, pc: c.pc, points: c.points.map((p) => ({ height: p[0], ts: p[1], rd: B(p[2]), ra: B(p[3]) })), trades: c.trades.map((t) => ({ n: t[0], side: t[1], addr: t[2], in: B(t[3]), out: B(t[4]), height: t[5], ts: t[6], scid })) };
    },
    // this wallet's own trades, written by app.trade; localStorage is shared with every page on the origin, so each
    // record is shape-checked before anything is computed from it. The ledger is the cost basis of P&L, so it is
    // not cut at a count: when the store is full, market copies make room for it (store).
    localTrades() { try { const l = JSON.parse(localStorage.getItem("veil.trades") || "[]"); return Array.isArray(l) ? l.filter(validLocalTrade) : []; } catch (e) { return []; } },
    addLocalTrade(t) { const l = this.localTrades(); l.push(t); this.store("veil.trades", JSON.stringify(l)); },
  };
  const DEC = /^\d{1,20}$/;
  function validLocalTrade(t) {
    return !!t && typeof t === "object" && HEX.test(String(t.scid)) && HEX.test(String(t.asset)) && isSide(t.side) && DEC.test(String(t.in)) && DEC.test(String(t.out)) && Number.isInteger(t.ts) && t.ts >= 0 && HEX.test(String(t.txid)) && /^\d{1,2}$/.test(String(t.dec)) && typeof t.published === "boolean";
  }
  window.addEventListener("pagehide", () => V.data && V.data.flushCache());
  setInterval(() => V.data && V.data.flushCache(), SAVE_EVERY);
})();
