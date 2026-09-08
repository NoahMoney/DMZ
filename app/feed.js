// DMZ feed.js — the data layer. Everything is reconstructed from contract state read from the node; nothing is
// trusted that the app cannot verify (code hash + treasury). Built to scale: after the first load a market is
// synced incrementally — per block the app probes a fixed key set and fetches only the NEW t_<n> / p_<n> keys in
// ranges — and a bounded copy is kept in localStorage so reopening the app is instant.
(function () {
  const V = window.Veil, B = BigInt, S = V.stats, clean = (s, n) => V.ui.clean(s, n);
  const NUMKEYS = ["rd", "ra", "lp", "fd", "fa", "tc", "pc", "lh", "lt", "sold", "real", "cs", "Lq", "G", "vd0", "tacc", "rpool", "locked", "stot", "sacc", "gh", "S", "dec", "fee", "pfee", "tax", "tmode", "phase", "minted", "maxsupply", "price", "minhold", "holdblocks", "cacc"];
  const STRKEYS = ["nameHdr", "descrHdr", "iconURLHdr", "tagsHdr", "ticker", "creator", "treasury", "asset", "qt", "kind", "token", "fileURL", "collection"];
  const PROBE = ["rd", "ra", "tc", "pc", "lp", "phase", "stot", "tacc", "fd", "fa", "lh", "lt", "real", "sold", "locked", "sacc", "rpool", "minted"];
  const CHUNK = 250, MAXP = 4000, MAXT = 2000; // in-memory history caps per market (older history stays on chain)
  const CACHE_P = 1500, CACHE_T = 600, SAVE_EVERY = 30000; // localStorage copy: smaller and written at most every 30 s
  const VERIFY_N = 4; // newest cached points and trades re-read from the chain before a localStorage copy is trusted
  const sigOf = (vals) => PROBE.map((k) => vals[k] || "").join("|");
  const isNum = (v) => typeof v === "string" && /^\d+$/.test(v);

  const data = {
    cfg: window.VEIL_CONFIG, registry: null, assets: new Map(), nfts: new Map(), profiles: new Map(), follows: new Set(), followers: new Map(), following: new Map(),
    heights: new Map(), hashes: {}, verified: new Map(), lastRefresh: 0, refreshing: null, listeners: [], height: 0,
    // Screen handlers subscribe on every dispatch, so a plain push leaks one closure per visit and redraws the
    // screen once per past visit on every block. Tag each subscription with the navigation that created it and
    // drop the stale ones; keep:true marks the few that must outlive navigation.
    onChange(fn, keep) { this.listeners.push({ fn, gen: keep ? -1 : (V.ui.router.gen | 0) }); },
    changed() { const g = V.ui.router.gen | 0; this.listeners = this.listeners.filter((e) => e.gen === -1 || e.gen === g); for (const e of this.listeners.slice()) { try { e.fn(); } catch (err) { console.error(err); } } },
    rawToAddr(rawHex) { try { return V.rawToAddress(rawHex); } catch (e) { return null; } },
    // Every wrapped asset this build knows about: one vault per foreign chain, because a vault holds exactly one
    // asset. `vault`/`vaultAsset` are the older single-vault spelling and are folded in for compatibility.
    vaults() {
      const c = this.cfg, out = [];
      const add = (v) => { if (v && /^[0-9a-f]{64}$/.test(v.scid || "") && !out.some((x) => x.scid === v.scid)) out.push(v); };
      for (const v of c.vaults || []) add(v);
      if (c.vault) add(Object.assign({ scid: c.vault }, c.vaultAsset || {}));
      return out;
    },
    // The vault whose wrapped asset this market trades, or null when the market is a native DERO one.
    // The symbol a market's prices, reserves and fees are denominated in. Never assume DERO: a market quoted in
    // wrapped XMR shows XMR amounts, and labelling them DERO would misstate the price by whatever the rate is.
    // Give a market its quote's display name. Safe to call more than once and on a market with no quote.
    nameQuote(s) {
      if (!s) return s;
      const v = s.quote ? this.vaults().find((x) => x.scid === s.quote) : null;
      s.quoteSym = !s.quote ? "DERO" : v ? V.ui.clean(v.sym || v.name || "wrapped", 10) : s.quote.slice(0, 6);
      s.quoteDec = !s.quote ? 5 : v && Number.isInteger(v.dec) ? v.dec : 5;
      s.pair = (s.ticker || "?") + " / " + s.quoteSym;
      return s;
    },
    qsym(s) { return (s && s.quoteSym) || "DERO"; },
    qdec(s) { return BigInt(Number.isInteger(s && s.quoteDec) ? s.quoteDec : 5); },
    vaultOf(s) {
      if (!s) return null;
      for (const v of this.vaults()) if (s.asset === v.scid || s.scid === v.scid || s.quote === v.scid) return v;
      return null;
    },

    async registry_() {
      const cfg = this.cfg; if (!cfg.factory) throw new Error("no factory configured");
      const keys = ["pc", "lc", "nc", "regfee", "tstr"]; const r0 = await V.node.getSC(cfg.factory, { keys });
      const n = (k) => (isNum(r0.vals[k]) ? B(r0.vals[k]) : 0n);
      const ids = (p, c) => { const out = []; for (let i = 0n; i < c; i++) out.push(p + i); return out; };
      const want = [...ids("pool_", n("pc")), ...ids("launch_", n("lc")), ...ids("nft_", n("nc"))];
      const vals = await this.fetchKeys(cfg.factory, want);
      const list = (p, c) => { const out = []; for (let i = 0n; i < c; i++) { const v = V.hexToStr(vals[p + i] || ""); if (/^[0-9a-f]{64}$/.test(v)) out.push(v); } return out; };
      const reg = { treasury: V.hexToStr(r0.vals.tstr || ""), regfee: n("regfee"), pools: list("pool_", n("pc")), launches: list("launch_", n("lc")), nfts: list("nft_", n("nc")) };
      reg.treasuryCanon = V.canonAddress(reg.treasury);
      this.registry = reg; return reg;
    },
    // fetch many string keys in chunks; returns {key: hexvalue}
    async fetchKeys(scid, keys) {
      const out = {};
      for (let i = 0; i < keys.length; i += CHUNK) { const part = keys.slice(i, i + CHUNK); const r = await V.node.getSC(scid, { keys: part }); for (const k of part) if (r.vals[k] !== undefined) out[k] = r.vals[k]; }
      return out;
    },
    async templateHashes() {
      if (this.hashes.ok) return this.hashes;
      const t = this.cfg.templates; const h = {}; for (const k of Object.keys(t)) { h[t[k].sha256] = k; for (const x of t[k].legacy || []) h[x] = k; }
      this.hashes = { map: h, ok: true }; return this.hashes;
    },
    async installHeight(scid) {
      if (this.heights.has(scid)) return this.heights.get(scid);
      try { const t = await V.node.getTx(scid); const hh = t && t.block_height > 0 ? t.block_height : null; if (hh) this.heights.set(scid, hh); return hh; } catch (e) { return null; }
    },
    async probe(scid) { const r = await V.node.getSC(scid, { keys: PROBE }); return sigOf(r.vals); },
    // verify the code once per session (contracts are immutable, the app still checks what it lists)
    async verify(scid) {
      if (this.verified.has(scid)) return this.verified.get(scid);
      const r = await V.node.getSC(scid, { code: true }); const kind = (await this.templateHashes()).map[await V.sha256Hex(r.code)] || null;
      this.verified.set(scid, kind); return kind;
    },

    cacheKey(scid) { return "veil.m." + scid; },
    // The cache is a hint, never truth: any page on this origin can write localStorage, so every record is
    // shape-checked here and the newest ones are re-read from the chain before the rest is used (loadContract).
    loadCache(scid) {
      try {
        const c = JSON.parse(localStorage.getItem(this.cacheKey(scid)) || "null");
        if (this.validCache(c)) return c;
        if (c) localStorage.removeItem(this.cacheKey(scid));
      } catch (e) {}
      return null;
    },
    validCache(c) {
      const int = (x) => Number.isInteger(x) && x >= 0, dec = (x) => typeof x === "string" && /^\d{1,20}$/.test(x);
      if (!c || c.v !== 4 || !int(c.tc) || !int(c.pc) || !int(c.p0) || !int(c.t0) || !Array.isArray(c.points) || !Array.isArray(c.trades)) return false;
      if (c.p0 + c.points.length !== c.pc || c.t0 + c.trades.length !== c.tc || c.points.length > CACHE_P || c.trades.length > CACHE_T) return false;
      if (c.pc === 0 && c.tc > 0) return false; // a published trade always leaves a price point
      let h = 0;
      for (const p of c.points) { if (!Array.isArray(p) || p.length !== 4 || !int(p[0]) || p[0] < h || !int(p[1]) || !dec(p[2]) || !dec(p[3])) return false; h = p[0]; }
      let n = c.t0;
      for (const t of c.trades) { if (!Array.isArray(t) || t.length !== 7 || t[0] !== n++ || (t[1] !== 0 && t[1] !== 1) || !V.canonAddress(t[2]) || !dec(t[3]) || !dec(t[4]) || !int(t[5]) || !int(t[6])) return false; }
      return true;
    },
    saveCache(s, force) {
      if (!force && Date.now() - (s._saved || 0) < SAVE_EVERY) { s._dirty = true; return; }
      s._saved = Date.now(); s._dirty = false;
      try {
        const pts = s.points.filter((p) => !p.live).slice(-CACHE_P), tr = s.trades.slice(-CACHE_T);
        localStorage.setItem(this.cacheKey(s.scid), JSON.stringify({ v: 4, tc: Number(s.tc), pc: Number(s.pc), p0: Number(s.pc) - pts.length, t0: Number(s.tc) - tr.length,
          points: pts.map((p) => [p.height, p.ts, p.rd.toString(), p.ra.toString()]), trades: tr.map((t) => [t.n, t.side, t.addr, t.in.toString(), t.out.toString(), t.height, t.ts]) }));
      } catch (e) {}
    },

    // Reads one pool/launch/nft contract (incrementally when a previous record or cache exists).
    async loadContract(scid, prev) {
      const kind = await this.verify(scid);
      if (!kind) return { scid, untrusted: "code does not match a shipped template" };
      const r = await V.node.getSC(scid, { keys: [...NUMKEYS, ...STRKEYS] });
      const num = (k) => (isNum(r.vals[k]) ? B(r.vals[k]) : 0n), str = (k, n) => clean(V.hexToStr(r.vals[k] || ""), n), hex = (k) => (r.vals[k] || "");
      const tCanon = hex("treasury") ? this.rawToAddr(hex("treasury")) : null;
      if (this.registry && this.registry.treasuryCanon && tCanon !== this.registry.treasuryCanon) return { scid, untrusted: "treasury differs from the factory" };
      const s = { scid, kind, fee: num("fee"), pfee: num("pfee"), treasury: tCanon, trades: [], points: [], sig: sigOf(r.vals), installHeight: await this.installHeight(scid) };
      // the template fixes the code, not its install parameters: a market with the right code and treasury but a
      // fee of 0 (or 1%) would pay the protocol nothing (or overcharge) and still look Verified without this check
      const std = kind === "launch" ? [B(this.cfg.feeBps), B(this.cfg.launchPfeeBps)] : kind === "pool" ? [B(this.cfg.feeBps), B(this.cfg.pfeeBps)] : kind === "nft" ? [B(this.cfg.feeBps), null] : null;
      if (std && (s.fee !== std[0] || (std[1] !== null && s.pfee !== std[1]))) return { scid, untrusted: "fee differs from the protocol's (" + s.fee + " / " + s.pfee + " bps)" };
      for (const k of ["rd", "ra", "lp", "fd", "fa", "tc", "pc", "lh", "lt"]) s[k] = num(k);
      // The quote side, for launches and pools alike. The zero hash means DERO, and a v1 pool has no key at all.
      const qt = hex("qt");
      s.quote = qt && !/^0+$/.test(qt) ? qt : null;
      if (kind === "launch") {
        Object.assign(s, { name: str("nameHdr", 32) || "?", ticker: str("ticker", 10) || "?", descr: str("descrHdr", 240), icon: str("iconURLHdr", 200), tags: str("tagsHdr", 100),
          dec: num("dec"), supply: num("S"), tax: num("tax"), tmode: num("tmode"), cs: num("cs"), Lq: num("Lq"), G: num("G"), vd0: num("vd0"), phase: num("phase"), sold: num("sold"), real: num("real"),
          tacc: num("tacc"), rpool: num("rpool"), locked: num("locked"), stot: num("stot"), sacc: num("sacc"), gh: num("gh"), creator: this.rawToAddr(hex("creator")), asset: scid });
      } else if (kind === "pool") {
        s.asset = hex("asset"); s.dec = 5n; s.name = "Asset " + s.asset.slice(0, 6); s.ticker = s.asset.slice(0, 4).toUpperCase();
      } else if (kind === "nft") {
        Object.assign(s, { name: str("nameHdr", 64) || "?", descr: str("descrHdr", 240), icon: str("iconURLHdr", 200), file: str("fileURL", 200), tags: str("tagsHdr", 100), collection: str("collection", 64),
          token: hex("token"), minhold: num("minhold"), holdblocks: num("holdblocks"), maxsupply: num("maxsupply"), price: num("price"), minted: num("minted"), cacc: num("cacc"), fd: num("fd"), fee: num("fee"), creator: this.rawToAddr(hex("creator")) });
        return s;
      }
      // history: reuse what we have (memory or cache) and fetch only the new keys
      const cached = !(prev && prev.points), base = cached ? this.fromCache(scid) : prev;
      let points = [], trades = [], pFrom = Math.max(0, Number(s.pc) - MAXP), tFrom = Math.max(0, Number(s.tc) - MAXT);
      if (base && Number(base.pc) <= Number(s.pc) && Number(base.tc) <= Number(s.tc)) { // a usable copy (a higher watermark means another chain)
        points = base.points.filter((p) => !p.live); trades = base.trades.slice(); pFrom = Number(base.pc); tFrom = Number(base.tc);
      }
      const keys = (a, b) => { const k = []; for (let i = a; i < Number(s.pc); i++) k.push("p_" + i); for (let i = b; i < Number(s.tc); i++) k.push("t_" + i); return k; };
      // a localStorage copy is re-read at its newest records before the rest of it is trusted
      const chk = cached && base && (points.length || trades.length) ? [...points.slice(-VERIFY_N).map((p, i, a) => ["p_" + (pFrom - a.length + i), [p.height, p.ts, p.rd, p.ra].join("|")]), ...trades.slice(-VERIFY_N).map((t) => ["t_" + t.n, [t.side, t.addr, t.in, t.out, t.height, t.ts].join("|")])] : [];
      let got = await this.fetchKeys(scid, [...keys(pFrom, tFrom), ...chk.map((x) => x[0])]);
      if (chk.some((x) => V.hexToStr(got[x[0]] || "") !== x[1])) { // tampered or foreign: drop it and read the window from the chain
        try { localStorage.removeItem(this.cacheKey(scid)); } catch (e) {}
        points = []; trades = []; pFrom = Math.max(0, Number(s.pc) - MAXP); tFrom = Math.max(0, Number(s.tc) - MAXT);
        got = await this.fetchKeys(scid, keys(pFrom, tFrom));
      }
      for (let i = pFrom; i < Number(s.pc); i++) { const v = V.hexToStr(got["p_" + i] || ""); const p = v.split("|"); if (p.length >= 4) points.push({ height: Number(p[0]), ts: Number(p[1]), rd: B(p[2]), ra: B(p[3]) }); }
      for (let i = tFrom; i < Number(s.tc); i++) { const v = V.hexToStr(got["t_" + i] || ""); const p = v.split("|"); if (p.length >= 6 && V.canonAddress(p[1])) trades.push({ n: i, side: Number(p[0]), addr: p[1], in: B(p[2]), out: B(p[3]), height: Number(p[4]), ts: Number(p[5]), scid }); }
      if (points.length > MAXP) points = points.slice(-MAXP); if (trades.length > MAXT) trades = trades.slice(-MAXT);
      s.points = points; s.trades = trades;
      s.points.push({ height: Number(s.lh), ts: Number(s.lt) || S.now(), rd: s.rd, ra: s.ra, live: true });
      this.saveCache(s);
      return s;
    },
    flushCache() { for (const s of this.assets.values()) if (s._dirty) this.saveCache(s, true); },
    fromCache(scid) {
      const c = this.loadCache(scid); if (!c) return null;
      return { tc: c.tc, pc: c.pc, points: c.points.map((p) => ({ height: p[0], ts: p[1], rd: B(p[2]), ra: B(p[3]) })), trades: c.trades.map((t) => ({ n: t[0], side: t[1], addr: t[2], in: B(t[3]), out: B(t[4]), height: t[5], ts: t[6], scid })) };
    },
    decorate() {
      for (const s of this.assets.values()) if (s.kind === "pool") { const l = this.assets.get(s.asset); s.assetVerified = !!(l && l.kind === "launch"); if (s.assetVerified) { s.name = l.name + " pool"; s.ticker = l.ticker; s.dec = l.dec; s.icon = l.icon; } else { s.icon = ""; } }
      // A wrapped asset is not a launch, so nothing else names it. Take its ticker and decimals from the
      // deployment record, or its market shows a contract id where a symbol belongs.
      for (const s of this.assets.values()) {
        if (s.kind !== "pool") continue;
        const bv = this.vaults().find((x) => x.scid === s.asset);
        if (bv) { s.ticker = V.ui.clean(bv.sym || bv.name || "wrapped", 10); s.name = V.ui.clean(bv.name || "wrapped asset", 40); s.dec = B(Number.isInteger(bv.dec) ? bv.dec : 5); s.wrappedBase = true; }
      }
      // Name the quote so a market can be read as a pair. A wrapped quote is named by its vault record; anything
      // else is shown by its contract id rather than guessed at.
      for (const s of this.assets.values()) this.nameQuote(s);
      // impersonation: markets whose tickers read alike (look-alikes folded) are all marked, so the copy never looks safer than the original
      const groups = new Map();
      for (const s of this.assets.values()) { if (s.kind === "nft" || (s.kind === "pool" && s.assetVerified)) continue; const k = V.ui.tickerKey(s.ticker); s.lookalike = V.ui.hasHomoglyph(s.ticker); (groups.get(k) || groups.set(k, []).get(k)).push(s); }
      for (const g of groups.values()) for (const s of g) s.dupTicker = g.length > 1;
      for (const s of this.assets.values()) if (s.kind === "pool" && s.assetVerified) { const l = this.assets.get(s.asset); if (!l) continue; s.dupTicker = !!l.dupTicker; s.lookalike = !!l.lookalike; } // a launch and its own pool share a ticker by design
      for (const s of this.assets.values()) S.enrich(s, this.height);
      for (const n of this.nfts.values()) { const t = this.assets.get(n.token); n.tokenTicker = t ? t.ticker : V.shortAddr(n.token); n.tokenDec = t ? t.dec : 5n; }
    },

    async refresh(full) {
      if (this.refreshing) return this.refreshing;
      this.refreshing = (async () => {
        const reg = await this.registry_();
        const ids = [...reg.launches, ...reg.pools]; const next = new Map(), nfts = new Map();
        await pmap(ids, 4, async (id) => {
          const cached = this.assets.get(id);
          try {
            if (cached && !full) { const sig = await this.probe(id); if (sig === cached.sig) { next.set(id, cached); return; } }
            const s = await this.loadContract(id, cached); if (s && !s.untrusted) next.set(id, s); else if (s) console.warn("skipping", id, s.untrusted);
          } catch (e) { console.warn("load", id, e); if (cached) next.set(id, cached); }
        });
        await pmap(reg.nfts, 4, async (id) => { const cached = this.nfts.get(id); try { if (cached && !full) { const sig = await this.probe(id); if (sig === cached.sig) { nfts.set(id, cached); return; } } const s = await this.loadContract(id, cached); if (s && !s.untrusted && s.kind === "nft") nfts.set(id, s); } catch (e) { if (cached) nfts.set(id, cached); } });
        try { this.founders = await this.loadFounders(this.founders); } catch (e) { console.warn("founders", e); }
        this.assets = next; this.nfts = nfts; this.decorate(); this.lastRefresh = Date.now();
        this.changed();
      })().finally(() => { this.refreshing = null; });
      return this.refreshing;
    },
    // per-user keys read on demand (stake, NFT lock) — never enumerated
    async myKeys(scid, addr, prefixes) { if (!addr) return {}; const got = await this.fetchKeys(scid, prefixes.map((p) => p + addr)); const out = {}; for (const p of prefixes) { const v = got[p + addr]; out[p.replace(/_$/, "")] = isNum(v) ? B(v) : (v === undefined ? null : v); } return out; },

    async loadSocial(me) {
      if (!this.cfg.social) return;
      const r = await V.node.getSC(this.cfg.social, { variables: true });
      const profiles = new Map(), followers = new Map(), following = new Map();
      for (const [k, v] of Object.entries(r.strs)) {
        const a = k.slice(3); if (!V.canonAddress(a)) continue;
        if (k.startsWith("pn_")) profiles.set(a, Object.assign(profiles.get(a) || {}, { name: clean(v, 32) }));
        else if (k.startsWith("pb_")) profiles.set(a, Object.assign(profiles.get(a) || {}, { bio: clean(v, 160) }));
        else if (k.startsWith("pa_")) profiles.set(a, Object.assign(profiles.get(a) || {}, { avatar: V.ui.safeUrl(v) }));
      }
      for (const [k, v] of Object.entries(r.vars)) {
        if (k.startsWith("fr_")) followers.set(k.slice(3), Number(v));
        else if (k.startsWith("f_")) { const [a, b] = k.slice(2).split("_"); if (a && b) { if (!following.has(a)) following.set(a, new Set()); following.get(a).add(b); } }
      }
      this.profiles = profiles; this.followers = followers; this.following = following; this.follows = (me && following.get(me)) || new Set(); this.changed();
    },
    followingOf(addr) { return (this.following && this.following.get(addr)) || new Set(); },

    list() { return [...this.assets.values()]; },
    active() { return this.list().filter((s) => s.rd > 0n); },
    trending(n = 12) { return this.active().sort((a, b) => b.m.hot - a.m.hot || b.m.vol24 - a.m.vol24 || b.points.length - a.points.length).slice(0, n); },
    movers(n = 8) { return this.active().filter((s) => s.m.c24h !== null && s.points.length > 1).sort((a, b) => Math.abs(b.m.c24h) - Math.abs(a.m.c24h)).slice(0, n); },
    newest(n = 8) { return this.list().filter((s) => s.kind === "launch").sort((a, b) => (b.installHeight || 0) - (a.installHeight || 0)).slice(0, n); },
    graduating(n = 8) { return this.list().filter((s) => s.kind === "launch" && s.phase === 0n).sort((a, b) => b.m.progress - a.m.progress).slice(0, n); },
    feed(filter, me) {
      const watch = S.watch.all(); const out = [];
      for (const s of this.assets.values()) for (const t of s.trades) {
        if (filter === "following" && !(this.follows.has(t.addr) || t.addr === me)) continue;
        if (filter === "watch" && !watch.has(s.scid)) continue;
        out.push(Object.assign({ asset: s }, t));
      }
      return out.sort((a, b) => b.height - a.height || b.n - a.n);
    },
    profile(addr) { return this.profiles.get(addr) || {}; },
    displayName(addr) { const p = this.profile(addr); return p.name || V.shortAddr(addr); },
    tradesOf(addr) { const out = []; for (const s of this.assets.values()) for (const t of s.trades) if (t.addr === addr) out.push(Object.assign({ asset: s }, t)); return out.sort((a, b) => b.height - a.height); },
    // this wallet's own trades, written by app.trade; localStorage is shared with every page on the origin, so each
    // record is shape-checked before anything is computed from it
    localTrades() { try { const l = JSON.parse(localStorage.getItem("veil.trades") || "[]"); return Array.isArray(l) ? l.filter(validLocalTrade) : []; } catch (e) { return []; } },
    addLocalTrade(t) { const l = this.localTrades(); l.push(t); try { localStorage.setItem("veil.trades", JSON.stringify(l.slice(-500))); } catch (e) {} },
  };
  const HEX64 = /^[0-9a-f]{64}$/, DEC = /^\d{1,20}$/;
  function validLocalTrade(t) {
    return !!t && typeof t === "object" && HEX64.test(String(t.scid)) && HEX64.test(String(t.asset)) && (t.side === 0 || t.side === 1) && DEC.test(String(t.in)) && DEC.test(String(t.out)) && Number.isInteger(t.ts) && t.ts >= 0 && HEX64.test(String(t.txid)) && /^\d{1,2}$/.test(String(t.dec)) && typeof t.published === "boolean";
  }
  async function pmap(items, n, fn) { let i = 0; const w = Array.from({ length: Math.min(n, items.length) }, async () => { while (i < items.length) await fn(items[i++]); }); await Promise.all(w); }
  window.addEventListener("pagehide", () => data.flushCache());
  setInterval(() => data.flushCache(), SAVE_EVERY);
  V.data = data;
})();
