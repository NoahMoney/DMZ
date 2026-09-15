// DMZ feed.js — the data layer. Everything is reconstructed from contract state read from the node; nothing is
// trusted that the app cannot verify (code hash + treasury). Built to scale (docs/PERFORMANCE.md): a refresh reads
// one state of the chain, only the registry slots added since the last, and only the markets live.js's plan picks;
// a changed market is read incrementally, and a bounded copy is kept in localStorage (LRU). Naming and marks
// (decorate) are names.js, which loads next and adds to V.data.
(function () {
  const V = window.Veil, B = BigInt, S = V.stats, clean = (s, n) => V.ui.clean(s, n);
  const NUMKEYS = ["rd", "ra", "lp", "fd", "fa", "tc", "pc", "lh", "lt", "sold", "real", "cs", "Lq", "G", "vd0", "tacc", "rpool", "locked", "stot", "sacc", "gh", "S", "dec", "fee", "pfee", "tax", "tmode", "phase", "minted", "maxsupply", "price", "minhold", "holdblocks", "cacc"];
  const STRKEYS = ["nameHdr", "descrHdr", "iconURLHdr", "tagsHdr", "ticker", "creator", "treasury", "asset", "qt", "kind", "token", "fileURL", "collection"];
  const PROBE = ["rd", "ra", "tc", "pc", "lp", "phase", "stot", "tacc", "fd", "fa", "lh", "lt", "real", "sold", "locked", "sacc", "rpool", "minted"];
  // The caps, and what each protects (docs/PERFORMANCE.md §caps). CHUNK: keys in one GetSC, so one request stays
  // small for the node. MAXP / MAXT: history held in memory per market (older history stays on chain). POOL: reads
  // in flight at once, all answered by the user's own node. The localStorage caps are store.js's; the refresh
  // plan's, live.js's.
  const CHUNK = 250, MAXP = 4000, MAXT = 2000, POOL = 4;
  const VERIFY_N = 4; // newest cached points and trades re-read from the chain before a localStorage copy is trusted
  const FIXED = [...NUMKEYS, ...STRKEYS], REST = FIXED.filter((k) => !PROBE.includes(k)); // REST: what a probe did not read
  const sigOf = (vals) => PROBE.map((k) => vals[k] || "").join("|");
  const isNum = (v) => typeof v === "string" && /^\d+$/.test(v);
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
  const NFA_REST = [...NFA_NUM, ...NFA_STR].filter((k) => !NFA_PROBE.includes(k));
  const nfaSig = (vals) => NFA_PROBE.map((k) => vals[k] || "").join("|");
  const HEX = /^[0-9a-f]{64}$/;

  const data = {
    cfg: window.VEIL_CONFIG, registry: null, assets: new Map(), nfts: new Map(), nfas: new Map(), profiles: new Map(), follows: new Set(), followers: new Map(), following: new Map(),
    heights: new Map(), hashes: {}, verified: new Map(), oldcode: new Map(), lastRefresh: 0, refreshing: null, listeners: [], height: 0,
    // bad: ids not to read until a full refresh (refused) or until a height (failing); fails / retryAt: the backoff
    bad: new Map(), seen: null, fails: 0, retryAt: 0,
    // Screen handlers subscribe on every dispatch, so a plain push leaks one closure per visit and redraws the
    // screen once per past visit on every block. Tag each subscription with the navigation that created it and
    // drop the stale ones; keep:true marks the few that must outlive navigation.
    onChange(fn, keep) { this.listeners.push({ fn, gen: keep ? -1 : (V.ui.router.gen | 0) }); },
    changed() { const g = V.ui.router.gen | 0; this.listeners = this.listeners.filter((e) => e.gen === -1 || e.gen === g); for (const e of this.listeners.slice()) { try { e.fn(); } catch (err) { console.error(err); } } if (V.live) V.live.tickReads(); },
    rawToAddr(rawHex) { try { return V.rawToAddress(rawHex); } catch (e) { return null; } },

    // The factory's lists. factory.bas writes a slot once, in the call that raises its counter, so a registry already
    // read is extended by the slots added since and never read again. A counter that went down means another chain,
    // and a slot that did not answer an incomplete read: either way the next refresh reads the whole registry.
    async registry_(topo, full) {
      const cfg = this.cfg, old = this.registry; if (!cfg.factory) throw new Error("no factory configured");
      if (!full && topo && old && old.readT === topo && old.factory === cfg.factory) return old; // this very state was read
      const keys = ["pc", "lc", "nc", "regfee", "tstr", "owner", "renounced", "v"]; const r0 = await V.node.getSC(cfg.factory, { keys, topoheight: topo });
      // factory.bas stores pc, lc, nc at Initialize: an absent factory is not an empty registry (it read as "0 markets")
      if (!["pc", "lc", "nc"].every((k) => isNum(r0.vals[k]))) throw new Error("the factory this build names is not on this node's chain");
      const n = (k) => (isNum(r0.vals[k]) ? B(r0.vals[k]) : 0n), c = { pc: n("pc"), lc: n("lc"), nc: n("nc") };
      const inc = !full && old && old.n && old.factory === cfg.factory && Object.keys(c).every((k) => old.n[k] <= c[k]), f = (k) => (inc ? old.n[k] : 0n);
      const ids = (p, k) => { const out = []; for (let i = f(k); i < c[k]; i++) out.push(p + i); return out; };
      // nft_<i> and nfa_<i> share the one counter nc (factory.bas RegisterNFA): a slot holds exactly one of the
      // two spellings, so both are asked for over the same range and each list keeps the slots that answer to it.
      const want = [...ids("pool_", "pc"), ...ids("launch_", "lc"), ...ids("nft_", "nc"), ...ids("nfa_", "nc")];
      const vals = want.length ? await this.fetchKeys(cfg.factory, want, topo) : {};
      const list = (p, k, had) => { const out = inc ? had.slice() : []; for (const key of ids(p, k)) { const v = V.hexToStr(vals[key] || ""); if (HEX.test(v)) out.push(v); } return out; };
      const hole = [...ids("pool_", "pc"), ...ids("launch_", "lc")].some((k) => vals[k] === undefined) || ids("nft_", "nc").some((k) => vals[k] === undefined && vals["nfa_" + k.slice(4)] === undefined);
      const reg = { factory: cfg.factory, readT: topo, n: hole ? null : c, treasury: V.hexToStr(r0.vals.tstr || ""), regfee: n("regfee"), pools: list("pool_", "pc", inc && old.pools), launches: list("launch_", "lc", inc && old.launches), nfts: list("nft_", "nc", inc && old.nfts), nfas: list("nfa_", "nc", inc && old.nfas) };
      reg.treasuryCanon = V.canonAddress(reg.treasury);
      // A market's treasury is fixed at its install, so the factory's pointer is a hint, not the rule: on an older
      // factory with an owner, one SetTreasury would delist every market. A bundle that ships an accepted set (current + legacy,
      // the shape the template hashes take) makes that set the rule and reads the factory only for regfee.
      const shipped = (cfg.treasuries || []).map((a) => V.canonAddress(a)).filter(Boolean);
      reg.treasuries = new Set(shipped.length ? shipped : reg.treasuryCanon ? [reg.treasuryCanon] : []);
      // A treasury that cannot be read must not switch the check off in silence, and must not empty the list
      // either: the flag rides on every record instead, so the badge can say what was not checked.
      reg.treasuryUnverified = !reg.treasuries.size;
      // factory.bas's own powers, for the message from the dev. v2 has none: no owner, treasury and regfee fixed at
      // install. An older v1 factory: owner while it holds its powers, renounced (a height) after
      reg.owner = this.rawToAddr(r0.vals.owner || ""); reg.renounced = isNum(r0.vals.renounced) ? r0.vals.renounced : "";
      reg.v = isNum(r0.vals.v) ? Number(r0.vals.v) : 0;
      this.registry = reg; return reg;
    },
    // fetch many string keys in chunks, each key once; returns {key: hexvalue}. topo pins every chunk to one state.
    async fetchKeys(scid, keys, topo) {
      const out = {}, all = [...new Set(keys)];
      for (let i = 0; i < all.length; i += CHUNK) { const part = all.slice(i, i + CHUNK); const r = await V.node.getSC(scid, { keys: part, topoheight: topo }); for (const k of part) if (r.vals[k] !== undefined) out[k] = r.vals[k]; }
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
    async probe(scid, topo) { const r = await V.node.getSC(scid, { keys: PROBE, topoheight: topo }); return { sig: sigOf(r.vals), vals: r.vals }; },
    // verify the code once per session (contracts are immutable, the app still checks what it lists). keys, when
    // given, ride on the same request as the code, so a first read costs one call rather than two: { kind, vals }.
    async verifyRead(scid, keys, topo) {
      if (this.verified.has(scid)) return { kind: this.verified.get(scid), vals: null };
      const r = await V.node.getSC(scid, { code: true, keys, topoheight: topo }); const t = await this.templateHashes(), hh = await V.sha256Hex(r.code), kind = t.map[hh] || null;
      // Which tier it matched, kept beside the kind. A legacy hash is an EARLIER version of a template, accepted
      // so contracts already deployed from it still read — and one of the launch versions on that list refunds a
      // buy's overshoot before it checks min_out, which the ...To wrapper turns into a second payout of the whole
      // deposit. "Matches a shipped template" was the only thing the app knew and the only thing it said.
      this.oldcode.set(scid, !!kind && !t.cur[hh]);
      this.verified.set(scid, kind); return { kind, vals: keys ? r.vals : null };
    },
    async verify(scid) { return (await this.verifyRead(scid)).kind; },

    // Reads one pool/launch/nft contract (incrementally when a previous record or cache exists). A pin from the
    // refresh fixes every read to one topoheight; with its probe's vals, only the rest of the fixed set is asked
    // for, in the same call as the new history. No pin (token.js, an unlisted contract) reads the latest state.
    async loadContract(scid, prev, pin) {
      const topo = pin && pin.topo, known = pin && pin.vals;
      const v = await this.verifyRead(scid, known ? null : FIXED, topo), kind = v.kind;
      if (!kind) return { scid, untrusted: "code does not match a shipped template" };
      if (kind === "nfa") return this.loadNFA(scid, pin);
      const vals = Object.assign({}, known || v.vals || (await this.fetchKeys(scid, FIXED, topo)));
      const pc = isNum(vals.pc) ? Number(vals.pc) : 0, tc = isNum(vals.tc) ? Number(vals.tc) : 0, hist = kind !== "nft";
      // history: reuse what we have (memory or cache) and fetch only the new keys
      const cached = !(prev && prev.points), base = hist ? (cached ? this.fromCache(scid) : prev) : null;
      let points = [], trades = [], pFrom = Math.max(0, pc - MAXP), tFrom = Math.max(0, tc - MAXT), reused = false;
      if (base && Number(base.pc) <= pc && Number(base.tc) <= tc) { // a usable copy (a higher watermark means another chain)
        points = base.points.filter((p) => !p.live); trades = base.trades.slice(); pFrom = Number(base.pc); tFrom = Number(base.tc); reused = true;
      }
      const keys = (a, b) => { const k = []; for (let i = a; i < pc; i++) k.push("p_" + i); for (let i = b; i < tc; i++) k.push("t_" + i); return k; };
      // a localStorage copy is re-read at its newest records before the rest of it is trusted
      const chk = cached && base && (points.length || trades.length) ? [...points.slice(-VERIFY_N).map((p, i, a) => ["p_" + (pFrom - a.length + i), [p.height, p.ts, p.rd, p.ra].join("|")]), ...trades.slice(-VERIFY_N).map((t) => ["t_" + t.n, [t.side, t.addr, t.in, t.out, t.height, t.ts].join("|")])] : [];
      const hk = hist ? [...keys(pFrom, tFrom), ...chk.map((x) => x[0])] : [];
      let got = known ? await this.fetchKeys(scid, [...REST, ...hk], topo) : {};
      if (known) for (const k of REST) if (got[k] !== undefined) vals[k] = got[k];
      const num = (k) => (isNum(vals[k]) ? B(vals[k]) : 0n), str = (k, n) => clean(V.hexToStr(vals[k] || ""), n), hex = (k) => (vals[k] || "");
      const tCanon = hex("treasury") ? this.rawToAddr(hex("treasury")) : null, reg = this.registry;
      if (reg && !reg.treasuryUnverified && !reg.treasuries.has(tCanon)) return { scid, untrusted: tCanon ? "treasury is not the protocol treasury" : "treasury could not be read" };
      const s = { scid, kind, fee: num("fee"), pfee: num("pfee"), treasury: tCanon, treasuryUnverified: !reg || reg.treasuryUnverified, templateLegacy: !!this.oldcode.get(scid), trades: [], points: [], sig: sigOf(vals), installHeight: await this.installHeight(scid), _saved: prev && prev._saved };
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
      if (!known && hk.length) got = await this.fetchKeys(scid, hk, topo);
      if (chk.some((x) => V.hexToStr(got[x[0]] || "") !== x[1])) { // tampered or foreign: drop it and read the window from the chain
        this.drop(scid); reused = false;
        points = []; trades = []; pFrom = Math.max(0, pc - MAXP); tFrom = Math.max(0, tc - MAXT);
        got = await this.fetchKeys(scid, keys(pFrom, tFrom), topo);
      }
      for (let i = pFrom; i < pc; i++) { const v = V.hexToStr(got["p_" + i] || ""); const p = v.split("|"); if (p.length >= 4) points.push({ height: Number(p[0]), ts: Number(p[1]), rd: B(p[2]), ra: B(p[3]) }); }
      for (let i = tFrom; i < tc; i++) { const v = V.hexToStr(got["t_" + i] || ""); const p = v.split("|"); if (p.length >= 6 && V.canonAddress(p[1])) trades.push({ n: i, side: Number(p[0]), addr: p[1], in: B(p[2]), out: B(p[3]), height: Number(p[4]), ts: Number(p[5]), scid }); }
      if (points.length > MAXP) points = points.slice(-MAXP); if (trades.length > MAXT) trades = trades.slice(-MAXT);
      s.points = points; s.trades = trades; s.trimmed = reused && !cached && !!prev.trimmed;
      s.points.push({ height: Number(s.lh), ts: Number(s.lt) || S.now(), rd: s.rd, ra: s.ra, live: true });
      this.saveCache(s);
      return s;
    },
    // The NFA branch of loadContract (docs/NFA_BUILD.md §3.C): the code has verified against templates.nfa, legacy
    // included, by the time this runs. The treasury check is the one every kind passes. The fee check is the
    // pool's, for the pool's reason: the template fixes the code and not its install parameters, so an NFA whose
    // protoFee is not the deployment's would verify by code and treasury while paying the Founders Pass nothing.
    async loadNFA(scid, pin) {
      const known = pin && pin.vals, r = { vals: Object.assign({}, known || {}, await this.fetchKeys(scid, known ? NFA_REST : [...NFA_NUM, ...NFA_STR], pin && pin.topo)) };
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
    async probeNFA(scid, topo) { const r = await V.node.getSC(scid, { keys: NFA_PROBE, topoheight: topo }); return { sig: nfaSig(r.vals), vals: r.vals }; },

    // One cycle in flight: asks made meanwhile run once after it (full if any was). During a backoff (retryAt) only
    // an explicit full refresh, a Retry button, reaches the node.
    async refresh(full) {
      if (this.refreshing) { this.qFull = this.qFull || !!full; return (this.queued = this.queued || this.refreshing.catch(() => {}).then(() => { const f = this.qFull; this.queued = null; this.qFull = false; return this.refresh(f); })); }
      if (!full && Date.now() < this.retryAt) return;
      this.refreshing = (async () => {
        this.lastAttempt = Date.now();
        try {
          // one state for the whole cycle: the height and topoheight the block clock last saw
          const L = V.live || {}; let H = L.height, topo = L.topo;
          if (!H || !topo) { const g = await V.node.call("DERO.GetHeight"); H = g.height; topo = g.topoheight; }
          this.height = H || this.height; if (full) this.bad.clear();
          const reg = await this.registry_(topo, full), hot = this.hotSet(), changed = new Map();
          const all = [...reg.launches, ...reg.pools], next = this.carry(all, this.assets, changed), ids = full ? all : this.plan(all, this.assets, hot, H, topo);
          await pmap(ids, POOL, async (id) => {
            const cached = this.assets.get(id);
            try {
              const pin = await this.probeFor(id, cached, full, topo, H, this.probe); if (!pin) return;
              // Registration is permissionless and the factory cannot be pruned, and loadContract dispatches on
              // the code hash rather than on the list an id came from: an NFA registered with RegisterPool comes
              // back as an NFA record, which has no price history, and one of those in this map made decorate()
              // throw — losing every market's metrics and Home, Markets and Trade with them, permanently, for the
              // price of one registration. Take only what this list is for, as the two loops below always have.
              const s = await this.loadContract(id, cached, pin);
              if (s && !s.untrusted && (s.kind === "pool" || s.kind === "launch")) this.took(next, changed, id, s, cached, H, topo);
              else this.refused(next, changed, id, s, cached);
            } catch (e) { this.miss(id, cached, e, H); }
          });
          const nfts = this.carry(reg.nfts, this.nfts, changed), nftDue = new Set(full ? reg.nfts : this.plan(reg.nfts, this.nfts, hot, H, topo));
          await pmap(reg.nfts, POOL, async (id) => { if (!nftDue.has(id)) return; const cached = this.nfts.get(id); try { const pin = await this.probeFor(id, cached, full, topo, H, this.probe); if (!pin) return; const s = await this.loadContract(id, cached, pin); if (s && !s.untrusted && s.kind === "nft") this.took(nfts, changed, id, s, cached, H, topo); else this.refused(nfts, changed, id, s, cached); } catch (e) { this.miss(id, cached, e, H); } });
          // NFAs sync by their own probe: the fixed PROBE set is pool and launch state, none of which an NFA has,
          // so a cached record's signature would never change and a sale would never reach the screen.
          const nfas = this.carry(reg.nfas, this.nfas, changed), nfaDue = new Set(full ? reg.nfas : this.plan(reg.nfas, this.nfas, hot, H, topo));
          await pmap(reg.nfas, POOL, async (id) => { if (!nfaDue.has(id)) return; const cached = this.nfas.get(id); try { const pin = await this.probeFor(id, cached, full, topo, H, this.probeNFA); if (!pin) return; const s = await this.loadContract(id, cached, pin); if (s && !s.untrusted && s.kind === "nfa") this.took(nfas, changed, id, s, cached, H, topo); else this.refused(nfas, changed, id, s, cached); } catch (e) { this.miss(id, cached, e, H); } });
          try { this.founders = await this.loadFounders(this.founders); } catch (e) { console.warn("founders", e); }
          this.assets = next; this.nfts = nfts; this.nfas = nfas; this.decorate(full ? undefined : changed); this.trim(hot); this.lastRefresh = Date.now(); this.lastError = null; this.fails = 0; this.retryAt = 0;
          for (const id of this.deepWanted()) this.deepen(id).catch(() => {});
        } catch (e) { this.fails++; this.retryAt = Date.now() + (V.live ? V.live.backoff(this.fails, 2000, 60000) : 2000); this.failed(this, e); throw e; } finally { this.changed(); }
      })().finally(() => { this.refreshing = null; });
      return this.refreshing;
    },
    // A failed read keeps the last good record on screen, which is right, but not in silence: the error and the
    // time of the attempt ride on the record — and on the data layer itself when the registry read fails — so a
    // screen can stop animating a skeleton and mark a row stale instead of waiting for a refresh that never lands.
    failed(rec, e) { rec.lastError = String((e && e.message) || e); rec.lastAttempt = Date.now(); },

    // (plan, see, hotSet, deepWanted, rec, behind, stale, ageTag and trim, the refresh plan, are live.js's tiers)
    // carried as they are unless read again below; a record the registry no longer lists is dropped and noted
    carry(ids, from, changed) { const m = new Map(); for (const id of ids) if (from.has(id)) m.set(id, from.get(id)); for (const [id, p] of from) if (!m.has(id)) changed.set(id, p); return m; },
    // the probe: null when the record is unchanged at this state (and it is marked read), else the pin to load with
    async probeFor(id, cached, full, topo, H, probe) {
      if (!cached || full) return { topo };
      const p = await probe.call(this, id, topo);
      if (p.sig !== cached.sig) return { topo, vals: p.vals };
      Object.assign(cached, { lastError: null, fails: 0, readH: H, readT: topo }); return null;
    },
    // changedH moves on a new signature or a market registered after the first load; a first load is no change
    took(map, changed, id, s, cached, H, topo) { Object.assign(s, { readH: H, readT: topo, changedH: cached ? (cached.sig !== s.sig ? H : cached.changedH || 0) : this.lastRefresh ? H : 0, everyBlock: !cached || cached.everyBlock !== false }); map.set(id, s); changed.set(id, cached || null); this.bad.delete(id); },
    // refused (code, treasury, fee or kind): all fixed at install, so not asked again until a full refresh
    refused(map, changed, id, s, cached) { if (s) console.warn("skipping", id, s.untrusted || "registered as a market but its code is a " + s.kind); map.delete(id); if (cached) changed.set(id, cached); this.bad.set(id, [Infinity, 0]); },
    // a read that failed: the last good record stays, marked, and the id waits 2, 4 … 32 blocks before the next try
    miss(id, cached, e, H) {
      console.warn("load", id, e);
      if (cached) { this.failed(cached, e); cached.fails = (cached.fails | 0) + 1; cached.retryH = H + Math.min(32, 2 ** cached.fails); }
      else { const n = ((this.bad.get(id) || [])[1] | 0) + 1; this.bad.set(id, [H + Math.min(32, 2 ** n), n]); }
    },
    // a market cut to its last day (live.js trim) gets its whole MAXP window back, when a page opens it or it is held
    async deepen(scid) {
      const s = this.assets.get(scid); if (!s || !s.trimmed || s._deep) return;
      s._deep = true;
      try {
        const first = Number(s.pc) - s.points.filter((p) => !p.live).length, keys = [];
        for (let i = Math.max(0, Number(s.pc) - MAXP); i < first; i++) keys.push("p_" + i);
        const got = await this.fetchKeys(scid, keys), older = [];
        for (const k of keys) { const p = V.hexToStr(got[k] || "").split("|"); if (p.length >= 4) older.push({ height: Number(p[0]), ts: Number(p[1]), rd: B(p[2]), ra: B(p[3]) }); }
        // applied to whatever record holds the id now, if it still starts where this read ends
        const c = this.assets.get(scid);
        if (c && c.trimmed && Number(c.pc) - c.points.filter((p) => !p.live).length === first) { c.points = older.concat(c.points); c.trimmed = false; this.changed(); }
      } finally { s._deep = false; }
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
  };
  Object.assign(data, V.live && V.live.tiers, V.storeParts); // the refresh plan (live.js) and localStorage (store.js), both loaded first
  async function pmap(items, n, fn) { let i = 0; const w = Array.from({ length: Math.min(n, items.length) }, async () => { while (i < items.length) await fn(items[i++]); }); await Promise.all(w); }
  // a page opened on a contract gets its whole history back now rather than at the next block
  window.addEventListener("hashchange", () => { const m = /^#(?:token|nft|nfa)\/([0-9a-f]{64})/.exec(location.hash); if (m) data.deepen(m[1]).catch(() => {}); });
  V.data = data;
})();
