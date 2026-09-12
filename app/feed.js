// DMZ feed.js — the data layer. Everything is reconstructed from contract state read from the node; nothing is
// trusted that the app cannot verify (code hash + treasury). Built to scale: after the first load a market is
// synced incrementally — per block the app probes a fixed key set and fetches only the NEW t_<n> / p_<n> keys in
// ranges — and a bounded copy is kept in localStorage so reopening the app is instant. How a record is named and
// marked (vault names, impersonation, split pairs, decorate) is names.js, which loads next and adds to V.data.
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
  // The side a contract writes into t_<n> (pool.bas and launch.bas post()): 1 bought the asset, 2 sold it. Both
  // validators read this one list, because two hand-written copies once said 0|1 and dropped every sell from
  // the cache and from this device's own ledger.
  const SIDES = [1, 2], isSide = (v) => SIDES.includes(v);
  // An NFA (contracts/nfa.bas, docs/NFA.md §3) is read by the standard's own key names plus DMZ's quote, protoFee
  // and treasury. They are kept apart from NUMKEYS / STRKEYS / PROBE so the fixed set every pool and launch is
  // fetched and synced by does not grow by forty keys none of them has. NFA_PROBE is what a block can change on
  // one: who owns it, the listing and its bids, and the URLs Update() may rewrite.
  const NFA_NUM = ["royalty", "artificerFee", "ownerCanUpdate", "protoFee", "active", "scBalance", "cancelBuffer", "startBlockTime", "endBlockTime", "bidCount", "staticBidIncr", "percentBidIncr", "charityDonatePerc", "startPrice", "currBidPrice", "currBidAmt", "previousSalePrice", "previousAuctionPrice", "owed"];
  const NFA_STR = ["nameHdr", "descrHdr", "typeHdr", "iconURLHdr", "tagsHdr", "fileURL", "fileSignURL", "fileCheckC", "fileCheckS", "coverURL", "collection", "version", "listType", "quote", "treasury", "owner", "creatorAddr", "previousOwner", "charityDonateAddr", "currBidAddr", "artificerAddr"];
  // currBidAmt and currBidAddr are in the probe because a bid can be left held in the contract with no listing
  // around it (nfa.bas resetVars does not clear them), and a page that did not watch them would show a stale
  // holder their bid is still live when it is the only thing that changed.
  const NFA_PROBE = ["owner", "scBalance", "listType", "startBlockTime", "endBlockTime", "currBidPrice", "currBidAmt", "currBidAddr", "bidCount", "fileURL", "fileSignURL", "iconURLHdr", "tagsHdr", "owed"];
  const nfaSig = (vals) => NFA_PROBE.map((k) => vals[k] || "").join("|");

  const data = {
    cfg: window.VEIL_CONFIG, registry: null, assets: new Map(), nfts: new Map(), nfas: new Map(), profiles: new Map(), follows: new Set(), followers: new Map(), following: new Map(),
    heights: new Map(), hashes: {}, verified: new Map(), oldcode: new Map(), lastRefresh: 0, refreshing: null, listeners: [], height: 0,
    // Screen handlers subscribe on every dispatch, so a plain push leaks one closure per visit and redraws the
    // screen once per past visit on every block. Tag each subscription with the navigation that created it and
    // drop the stale ones; keep:true marks the few that must outlive navigation.
    onChange(fn, keep) { this.listeners.push({ fn, gen: keep ? -1 : (V.ui.router.gen | 0) }); },
    changed() { const g = V.ui.router.gen | 0; this.listeners = this.listeners.filter((e) => e.gen === -1 || e.gen === g); for (const e of this.listeners.slice()) { try { e.fn(); } catch (err) { console.error(err); } } },
    rawToAddr(rawHex) { try { return V.rawToAddress(rawHex); } catch (e) { return null; } },

    async registry_() {
      const cfg = this.cfg; if (!cfg.factory) throw new Error("no factory configured");
      const keys = ["pc", "lc", "nc", "regfee", "tstr", "owner", "renounced"]; const r0 = await V.node.getSC(cfg.factory, { keys });
      // factory.bas stores pc, lc, nc at Initialize: an absent factory is not an empty registry (it read as "0 markets")
      if (!["pc", "lc", "nc"].every((k) => isNum(r0.vals[k]))) throw new Error("the factory this build names is not on this node's chain");
      const n = (k) => (isNum(r0.vals[k]) ? B(r0.vals[k]) : 0n);
      const ids = (p, c) => { const out = []; for (let i = 0n; i < c; i++) out.push(p + i); return out; };
      // nft_<i> and nfa_<i> share the one counter nc (factory.bas RegisterNFA): a slot holds exactly one of the
      // two spellings, so both are asked for over the same range and each list keeps the slots that answer to it.
      const want = [...ids("pool_", n("pc")), ...ids("launch_", n("lc")), ...ids("nft_", n("nc")), ...ids("nfa_", n("nc"))];
      const vals = await this.fetchKeys(cfg.factory, want);
      const list = (p, c) => { const out = []; for (let i = 0n; i < c; i++) { const v = V.hexToStr(vals[p + i] || ""); if (/^[0-9a-f]{64}$/.test(v)) out.push(v); } return out; };
      const reg = { treasury: V.hexToStr(r0.vals.tstr || ""), regfee: n("regfee"), pools: list("pool_", n("pc")), launches: list("launch_", n("lc")), nfts: list("nft_", n("nc")), nfas: list("nfa_", n("nc")) };
      reg.treasuryCanon = V.canonAddress(reg.treasury);
      // A market's treasury is fixed at its install, so the factory's live pointer is a hint, not the rule: one
      // SetTreasury would delist every market ever made. A bundle that ships an accepted set (current + legacy,
      // the shape the template hashes take) makes that set the rule and reads the factory only for regfee.
      const shipped = (cfg.treasuries || []).map((a) => V.canonAddress(a)).filter(Boolean);
      reg.treasuries = new Set(shipped.length ? shipped : reg.treasuryCanon ? [reg.treasuryCanon] : []);
      // A treasury that cannot be read must not switch the check off in silence, and must not empty the list
      // either: the flag rides on every record instead, so the badge can say what was not checked.
      reg.treasuryUnverified = !reg.treasuries.size;
      // factory.bas's own powers, for the message from the dev: owner while it holds them, renounced (a height) after
      reg.owner = this.rawToAddr(r0.vals.owner || ""); reg.renounced = isNum(r0.vals.renounced) ? r0.vals.renounced : "";
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
      const t = this.cfg.templates; const h = {}, cur = {}; for (const k of Object.keys(t)) { h[t[k].sha256] = k; cur[t[k].sha256] = 1; for (const x of t[k].legacy || []) h[x] = k; }
      this.hashes = { map: h, cur, ok: true }; return this.hashes;
    },
    async installHeight(scid) {
      if (this.heights.has(scid)) return this.heights.get(scid);
      try { const t = await V.node.getTx(scid); const hh = t && t.block_height > 0 ? t.block_height : null; if (hh) this.heights.set(scid, hh); return hh; } catch (e) { return null; }
    },
    async probe(scid) { const r = await V.node.getSC(scid, { keys: PROBE }); return sigOf(r.vals); },
    // verify the code once per session (contracts are immutable, the app still checks what it lists)
    async verify(scid) {
      if (this.verified.has(scid)) return this.verified.get(scid);
      const r = await V.node.getSC(scid, { code: true }); const t = await this.templateHashes(), hh = await V.sha256Hex(r.code), kind = t.map[hh] || null;
      // Which tier it matched, kept beside the kind. A legacy hash is an EARLIER version of a template, accepted
      // so contracts already deployed from it still read — and one of the launch versions on that list refunds a
      // buy's overshoot before it checks min_out, which the ...To wrapper turns into a second payout of the whole
      // deposit. "Matches a shipped template" was the only thing the app knew and the only thing it said.
      this.oldcode.set(scid, !!kind && !t.cur[hh]);
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
      // No pc/tc invariant: a contract writes a block's price point only when a LATER block trades, and none at
      // all before the first (pool.bas: IF LOAD("lh") == 0), so any number of published trades can stand at pc = 0.
      let h = 0;
      for (const p of c.points) { if (!Array.isArray(p) || p.length !== 4 || !int(p[0]) || p[0] < h || !int(p[1]) || !dec(p[2]) || !dec(p[3])) return false; h = p[0]; }
      let n = c.t0;
      for (const t of c.trades) { if (!Array.isArray(t) || t.length !== 7 || t[0] !== n++ || !isSide(t[1]) || !V.canonAddress(t[2]) || !dec(t[3]) || !dec(t[4]) || !int(t[5]) || !int(t[6])) return false; }
      return true;
    },
    saveCache(s, force) {
      if (!force && Date.now() - (s._saved || 0) < SAVE_EVERY) { s._dirty = true; return; }
      s._saved = Date.now(); s._dirty = false;
      const key = this.cacheKey(s.scid); let body;
      try {
        const pts = s.points.filter((p) => !p.live).slice(-CACHE_P), tr = s.trades.slice(-CACHE_T);
        body = JSON.stringify({ v: 4, tc: Number(s.tc), pc: Number(s.pc), p0: Number(s.pc) - pts.length, t0: Number(s.tc) - tr.length,
          points: pts.map((p) => [p.height, p.ts, p.rd.toString(), p.ra.toString()]), trades: tr.map((t) => [t.n, t.side, t.addr, t.in.toString(), t.out.toString(), t.height, t.ts]) });
        localStorage.setItem(key, body);
      } catch (e) {
        // The origin's quota is shared with every page on it and nothing else ever evicts, so a refused write
        // first drops the copies of markets the registry no longer lists and tries once more; refused again,
        // this market goes without a copy until something frees room.
        if (body && this.sweepCache()) try { localStorage.setItem(key, body); } catch (e2) {}
      }
    },
    // Removes the cached copy of every market the registry does not list (delisted, or another network's) and
    // says whether anything went. Judged by the registry, not this.assets: on a cold start the assets map is
    // empty while the copies are exactly what the first load is about to read.
    sweepCache() {
      const reg = this.registry; if (!reg) return false;
      const keep = new Set([...reg.launches, ...reg.pools]); let n = 0;
      try { for (const k of Object.keys(localStorage)) if (k.startsWith("veil.m.") && !keep.has(k.slice(7))) { localStorage.removeItem(k); n++; } } catch (e) {}
      return n > 0;
    },

    // Reads one pool/launch/nft contract (incrementally when a previous record or cache exists).
    async loadContract(scid, prev) {
      const kind = await this.verify(scid);
      if (!kind) return { scid, untrusted: "code does not match a shipped template" };
      if (kind === "nfa") return this.loadNFA(scid);
      const r = await V.node.getSC(scid, { keys: [...NUMKEYS, ...STRKEYS] });
      const num = (k) => (isNum(r.vals[k]) ? B(r.vals[k]) : 0n), str = (k, n) => clean(V.hexToStr(r.vals[k] || ""), n), hex = (k) => (r.vals[k] || "");
      const tCanon = hex("treasury") ? this.rawToAddr(hex("treasury")) : null, reg = this.registry;
      if (reg && !reg.treasuryUnverified && !reg.treasuries.has(tCanon)) return { scid, untrusted: tCanon ? "treasury is not the protocol treasury" : "treasury could not be read" };
      const s = { scid, kind, fee: num("fee"), pfee: num("pfee"), treasury: tCanon, treasuryUnverified: !reg || reg.treasuryUnverified, templateLegacy: !!this.oldcode.get(scid), trades: [], points: [], sig: sigOf(r.vals), installHeight: await this.installHeight(scid) };
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
    // The NFA branch of loadContract (docs/NFA_BUILD.md §3.C): the code has verified against templates.nfa, legacy
    // included, by the time this runs. The treasury check is the one every kind passes. The fee check is the
    // pool's, for the pool's reason: the template fixes the code and not its install parameters, so an NFA whose
    // protoFee is not the deployment's would verify by code and treasury while paying the Founders Pass nothing.
    async loadNFA(scid) {
      const r = await V.node.getSC(scid, { keys: [...NFA_NUM, ...NFA_STR] });
      const num = (k) => (isNum(r.vals[k]) ? B(r.vals[k]) : 0n), str = (k, n) => clean(V.hexToStr(r.vals[k] || ""), n), addr = (k) => (r.vals[k] ? this.rawToAddr(r.vals[k]) : null);
      const tCanon = addr("treasury"), reg = this.registry;
      if (reg && !reg.treasuryUnverified && !reg.treasuries.has(tCanon)) return { scid, untrusted: tCanon ? "treasury is not the protocol treasury" : "treasury could not be read" };
      const s = { scid, kind: "nfa", treasury: tCanon, treasuryUnverified: !reg || reg.treasuryUnverified, templateLegacy: !!this.oldcode.get(scid), sig: nfaSig(r.vals), installHeight: await this.installHeight(scid) };
      for (const k of NFA_NUM) s[k] = num(k);
      if (s.protoFee !== B(this.cfg.feeBps || 0)) return { scid, untrusted: "protocol fee differs from the deployment's (" + s.protoFee + " bps)" };
      Object.assign(s, { name: str("nameHdr", 64) || "?", descr: str("descrHdr", 240), type: str("typeHdr", 32), icon: str("iconURLHdr", 200), tags: str("tagsHdr", 100), file: str("fileURL", 300), fileSign: str("fileSignURL", 300), checkC: str("fileCheckC", 120), checkS: str("fileCheckS", 120), cover: str("coverURL", 300), collection: str("collection", 64), version: str("version", 8), listType: str("listType", 8),
        owner: addr("owner"), creator: addr("creatorAddr"), prevOwner: addr("previousOwner"), charityAddr: addr("charityDonateAddr"), bidder: addr("currBidAddr"), artificer: addr("artificerAddr") });
      // The quote is stored as the raw 32-byte SCID, so its hex is the id the vault list is keyed by and the zero
      // hash is DERO — the reading a pool's qt gets, so a wXMR-quoted NFA shows XMR prices through nameQuote. The
      // avatar plate takes the name's first letters for want of a ticker.
      const q = r.vals.quote || ""; s.quote = q && !/^0+$/.test(q) ? q : null; s.ticker = s.name.slice(0, 3);
      return this.nameQuote(s);
    },
    async probeNFA(scid) { const r = await V.node.getSC(scid, { keys: NFA_PROBE }); return nfaSig(r.vals); },
    flushCache() { for (const s of this.assets.values()) if (s._dirty) this.saveCache(s, true); },
    fromCache(scid) {
      const c = this.loadCache(scid); if (!c) return null;
      return { tc: c.tc, pc: c.pc, points: c.points.map((p) => ({ height: p[0], ts: p[1], rd: B(p[2]), ra: B(p[3]) })), trades: c.trades.map((t) => ({ n: t[0], side: t[1], addr: t[2], in: B(t[3]), out: B(t[4]), height: t[5], ts: t[6], scid })) };
    },

    async refresh(full) {
      if (this.refreshing) return this.refreshing;
      this.refreshing = (async () => {
        this.lastAttempt = Date.now();
        try {
          const reg = await this.registry_();
          const ids = [...reg.launches, ...reg.pools]; const next = new Map(), nfts = new Map();
          await pmap(ids, 4, async (id) => {
            const cached = this.assets.get(id);
            try {
              if (cached && !full) { const sig = await this.probe(id); if (sig === cached.sig) { cached.lastError = null; next.set(id, cached); return; } }
              // Registration is permissionless and the factory cannot be pruned, and loadContract dispatches on
              // the code hash rather than on the list an id came from: an NFA registered with RegisterPool comes
              // back as an NFA record, which has no price history, and one of those in this map made decorate()
              // throw — losing every market's metrics and Home, Markets and Trade with them, permanently, for the
              // price of one registration. Take only what this list is for, as the two loops below always have.
              const s = await this.loadContract(id, cached);
              if (s && !s.untrusted && (s.kind === "pool" || s.kind === "launch")) next.set(id, s);
              else if (s) console.warn("skipping", id, s.untrusted || "registered as a market but its code is a " + s.kind);
            } catch (e) { console.warn("load", id, e); if (cached) { this.failed(cached, e); next.set(id, cached); } }
          });
          await pmap(reg.nfts, 4, async (id) => { const cached = this.nfts.get(id); try { if (cached && !full) { const sig = await this.probe(id); if (sig === cached.sig) { cached.lastError = null; nfts.set(id, cached); return; } } const s = await this.loadContract(id, cached); if (s && !s.untrusted && s.kind === "nft") nfts.set(id, s); } catch (e) { if (cached) { this.failed(cached, e); nfts.set(id, cached); } } });
          // NFAs sync by their own probe: the fixed PROBE set is pool and launch state, none of which an NFA has,
          // so a cached record's signature would never change and a sale would never reach the screen.
          const nfas = new Map();
          await pmap(reg.nfas, 4, async (id) => { const cached = this.nfas.get(id); try { if (cached && !full) { const sig = await this.probeNFA(id); if (sig === cached.sig) { cached.lastError = null; nfas.set(id, cached); return; } } const s = await this.loadContract(id, cached); if (s && !s.untrusted && s.kind === "nfa") nfas.set(id, s); else if (s) console.warn("skipping", id, s.untrusted); } catch (e) { console.warn("load", id, e); if (cached) { this.failed(cached, e); nfas.set(id, cached); } } });
          try { this.founders = await this.loadFounders(this.founders); } catch (e) { console.warn("founders", e); }
          this.assets = next; this.nfts = nfts; this.nfas = nfas; this.decorate(); this.lastRefresh = Date.now(); this.lastError = null;
        } catch (e) { this.failed(this, e); throw e; } finally { this.changed(); }
      })().finally(() => { this.refreshing = null; });
      return this.refreshing;
    },
    // A failed read keeps the last good record on screen, which is right, but not in silence: the error and the
    // time of the attempt ride on the record — and on the data layer itself when the registry read fails — so a
    // screen can stop animating a skeleton and mark a row stale instead of waiting for a refresh that never lands.
    failed(rec, e) { rec.lastError = String((e && e.message) || e); rec.lastAttempt = Date.now(); },
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
    // The thin half of a split pair never ranks: its hot score and its 24h move are the cheapest numbers on the
    // exchange to manufacture, and Home rank 1 is exactly where a drain would be advertised. It stays listed on
    // Markets, marked, next to the contract that holds the liquidity.
    ranked() { return this.active().filter((s) => !s.splitWith || s.splitDeepest); },
    trending(n = 12) { return this.ranked().sort((a, b) => b.m.hot - a.m.hot || b.m.vol24 - a.m.vol24 || b.points.length - a.points.length).slice(0, n); },
    movers(n = 8) { return this.ranked().filter((s) => s.m.c24h !== null && s.points.length > 1).sort((a, b) => Math.abs(b.m.c24h) - Math.abs(a.m.c24h)).slice(0, n); },
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
    return !!t && typeof t === "object" && HEX64.test(String(t.scid)) && HEX64.test(String(t.asset)) && isSide(t.side) && DEC.test(String(t.in)) && DEC.test(String(t.out)) && Number.isInteger(t.ts) && t.ts >= 0 && HEX64.test(String(t.txid)) && /^\d{1,2}$/.test(String(t.dec)) && typeof t.published === "boolean";
  }
  async function pmap(items, n, fn) { let i = 0; const w = Array.from({ length: Math.min(n, items.length) }, async () => { while (i < items.length) await fn(items[i++]); }); await Promise.all(w); }
  window.addEventListener("pagehide", () => data.flushCache());
  setInterval(() => data.flushCache(), SAVE_EVERY);
  V.data = data;
})();
