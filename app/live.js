// DMZ live.js — what makes the app feel alive: a block clock driving refreshes, rolling numbers with up/down
// flashes, live "ago" labels and the price tape. Respects prefers-reduced-motion.
(function () {
  const V = window.Veil;
  // the system setting, or the app's own Reduce motion (Settings, html.reduce): either one stops every roll and the tape
  const reduced = () => document.documentElement.classList.contains("reduce") || !!(window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches);

  const live = {
    height: 0, topo: 0, blockAt: 0, listeners: [], polling: false, interval: 2500, fails: 0,
    onBlock(fn) { this.listeners.push(fn); },
    // The wait after n consecutive failures: base, doubling, never above cap; 0 while nothing has failed. Used by
    // this poll and by the data layer's refresh (feed.js), so a node that stops answering is asked less and less.
    backoff(n, base, cap) { return n > 0 ? Math.min(cap, base * 2 ** Math.min(n - 1, 30)) : 0; },
    start() { if (this.polling) return; this.polling = true; this.poll(); setInterval(() => this.tickAges(), 1000); },
    // A new block is a new height or a new topoheight (a side block changes contract state without a new height).
    // The topoheight is what the data layer pins every read of one refresh to (feed.js cycle).
    async poll() {
      try {
        const r = await V.node.call("DERO.GetHeight"), h = r && r.height, t = (r && r.topoheight) || 0; this.fails = 0;
        if (h && (h !== this.height || t !== this.topo)) { const first = !this.height; this.height = h; this.topo = t; this.blockAt = Date.now(); this.listeners.forEach((f) => { try { f(h); } catch (e) { if (!first) console.error(e); } }); }
      } catch (e) { this.fails++; }
      // failing: 5 s, 10 s … at most 30 s between asks; a hidden tab asks every 10 s (it does no per-block work)
      setTimeout(() => this.poll(), this.backoff(this.fails, this.interval * 2, 30000) || (document.hidden ? this.interval * 4 : this.interval));
    },
    // <span data-ts="1700000000"> → "12s ago"; <span data-block-age> → seconds since the last block
    tickAges() {
      const now = Date.now();
      for (const el of document.querySelectorAll("[data-ts]")) { const t = +el.dataset.ts; if (t) el.textContent = V.ui.timeAgo(t) + " ago"; }
      for (const el of document.querySelectorAll("[data-block-age]")) el.textContent = this.blockAt ? Math.floor((now - this.blockAt) / 1000) + "s" : "—";
      this.tickReads();
    },
    // <span data-read="<scid>"> (data.ageTag): "updated N blocks ago" while that record's values come from an older
    // read than the chain's (data.stale), empty and hidden while they are current. Run each second and after every
    // refresh (data.changed), so a row never shows an older read without saying so. The tags ageTag made are kept
    // in a set, never found by scanning the document: a document-wide querySelectorAll each second cost about a
    // second of main thread per block at 5,000 rows (docs/PERFORMANCE.md). A tag is let go once it has left the
    // page, or if it was never put on one within a minute.
    tags: new Set(),
    tickReads() {
      if (!V.data) return;
      for (const el of this.tags) { if (el.isConnected) { el._in = true; this.readTag(el); } else if (el._in || Date.now() - el._t > 60000) this.tags.delete(el); }
    },
    readTag(el) {
      if (!this.tags.has(el)) { el._t = Date.now(); this.tags.add(el); }
      const D = V.data, s = D.rec(el.dataset.read), n = s && D.stale(s) ? D.behind(s) : 0;
      el.hidden = !n; el.textContent = n ? "updated " + n + (n === 1 ? " block" : " blocks") + " ago" : "";
      return el;
    },

    // rolling number: animates from the previous value shown in this element and flashes on change
    num(el, value, fmt, opt = {}) {
      const prev = el._v; el._v = value;
      if (prev === undefined || !el.textContent || reduced() || typeof value !== "number" || typeof prev !== "number" || !isFinite(prev) || !isFinite(value)) { el.textContent = fmt(value); return; }
      if (prev === value) return;
      const t0 = performance.now(), dur = opt.ms || 500;
      const step = (t) => { const k = Math.min(1, (t - t0) / dur), e = 1 - Math.pow(1 - k, 3); el.textContent = fmt(prev + (value - prev) * e); if (k < 1) requestAnimationFrame(step); else el.textContent = fmt(value); };
      requestAnimationFrame(step);
      if (!opt.noFlash) this.flash(el, value > prev ? "up" : "down");
    },
    flash(el, dir) { el.classList.remove("flash-up", "flash-down"); void el.offsetWidth; el.classList.add(dir === "up" ? "flash-up" : "flash-down"); setTimeout(() => el.classList.remove("flash-up", "flash-down"), 900); },

    // price tape: <div id="tape"> gets two copies of the items for a seamless marquee; existing items update in place
    tape(container, assets) {
      const S = V.stats, h = V.ui.h;
      const items = assets.filter((s) => s.rd > 0n).slice(0, 14);
      if (!items.length) { container.hidden = true; return; }
      container.hidden = false;
      let track = container.querySelector(".tape-track");
      if (!track || track.dataset.n !== String(items.length)) {
        track = h("div", { class: "tape-track", "data-n": items.length });
        for (let copy = 0; copy < 2; copy++) for (const s of items) track.append(h("button", { class: "tape-item", type: "button", "data-scid": s.scid, "data-copy": copy, onclick: () => V.ui.router.go("token", s.scid) }, V.ui.pair(s), h("span", { class: "tp num" }), h("span", { class: "tc" })));
        V.ui.render(container, track);
        if (reduced()) track.style.animation = "none";
      }
      for (const el of track.querySelectorAll(".tape-item")) {
        const s = assets.find((x) => x.scid === el.dataset.scid); if (!s) continue;
        this.num(el.querySelector(".tp"), s.price, S.fmtPriceSub, { ms: 350 });
        const c = s.m ? s.m.c24h : null; const tc = el.querySelector(".tc"); tc.textContent = c === null ? "" : V.amm.pct(c); tc.className = "tc " + (c === null ? "" : c >= 0 ? "up" : "down");
      }
      container.style.setProperty("--tape-dur", Math.max(20, items.length * 4) + "s");
    },
  };
  // ---- the refresh plan (docs/PERFORMANCE.md): what each block reads. feed.js adds these to V.data. ----
  // Two caps on what a block asks of the user's node. COLD_EVERY: a record nothing shows is re-read about once per
  // this many blocks. RECENT: a record that changed within this many blocks is read every block, shown or not.
  const COLD_EVERY = 8, RECENT = 20, HEX = /^[0-9a-f]{64}$/;
  live.tiers = {
    // Which of ids to read this cycle, most urgent first. Read at every block: a record not read yet, one a screen
    // shows or the user holds, stars or has open (hot; null when no screen declared, and then all are), and one
    // that changed within RECENT blocks. The rest (cold) are read oldest first, 1/COLD_EVERY of them a cycle, so
    // each is re-read about every COLD_EVERY blocks and the load arrives spread, never all at once. A record read
    // at this very state (topo) already is not read again, and one whose reads failed waits out its backoff.
    plan(ids, recs, hot, H, topo) {
      const due = [], cold = [];
      for (const id of ids) {
        const s = recs.get(id), b = this.bad.get(id);
        if (b && b[0] > H) continue;
        if (!s) { due.push(id); continue; }
        if (s.retryH > H) continue;
        s.everyBlock = !hot || hot.has(id) || H - (s.changedH || 0) <= RECENT;
        if (topo && s.readT === topo) continue;
        if (s.everyBlock) due.push(id); else cold.push(s);
      }
      cold.sort((a, b) => (a.readH || 0) - (b.readH || 0));
      return due.concat(cold.slice(0, Math.ceil(cold.length / COLD_EVERY)).map((s) => s.scid));
    },
    // The ids a screen shows, one set per surface (key), for this navigation only (router.gen, as onChange). Once
    // the Markets list is virtualized it declares the rows on screen; a screen that declares nothing is taken to
    // show everything, so nothing goes stale unseen. A cold row that scrolls into view is read now, not next block.
    see(ids, key) {
      const g = V.ui.router.gen | 0; if (!this.seen || this.seen.gen !== g) this.seen = { gen: g, by: {} };
      const set = (this.seen.by[key || "main"] = new Set(ids));
      for (const id of set) { const s = this.rec(id); if (s && s.everyBlock === false && this.behind(s)) { clearTimeout(this._seeT); this._seeT = setTimeout(() => this.refresh().catch(() => {}), 250); break; } }
    },
    hotSet() {
      const by = this.seen && this.seen.gen === (V.ui.router.gen | 0) ? this.seen.by : null; if (!by) return null;
      const hot = new Set(this.deepWanted()); for (const k in by) for (const id of by[k]) hot.add(id);
      for (const id of V.stats.watch.all()) hot.add(id);
      for (const t of this.localTrades()) hot.add(t.scid);
      // a shown half of a split pair states the other halves' depth, so they are current too
      for (const id of [...hot]) { const s = this.assets.get(id); if (s && s.splitWith) for (const x of s.splitWith) hot.add(x); }
      return hot;
    },
    // what needs its whole history: the contract a page is open on, and what the wallet holds (Portfolio charts it)
    deepWanted() { const c = V.ui.router.current, out = [], hd = V.app && V.app.holdings; if (c && HEX.test(c.param || "")) out.push(c.param); if (hd && hd.rows) for (const r of hd.rows) out.push(r.s.scid); return out; },
    rec(id) { return this.assets.get(id) || this.nfts.get(id) || this.nfas.get(id) || null; },
    // blocks since this record was last confirmed against the chain (0 when unknown: a page's own read of an unlisted id)
    behind(s) { const h = live.height || this.height; return s && s.readH ? Math.max(0, h - s.readH) : 0; },
    // a record's values are shown from an older read when two blocks behind, or one behind and not read every block
    stale(s) { const n = this.behind(s); return n >= 2 || (n >= 1 && s.everyBlock === false); },
    // "updated N blocks ago", for a screen to put beside a record's values; readTag keeps it current
    ageTag(s) { return live.readTag(V.ui.h("span", { class: "readage", "data-read": s.scid })); },
    // Memory: a market in the cold tier keeps only the price points its figures read — the last day, and the one
    // point before it that the 24h change is measured from — and gets the rest back (feed.js deepen) when a page
    // opens it. Trades are kept: they are the exchange-wide index the feed, profiles and P&L read.
    trim(hot) {
      if (!hot) return;
      const edge = V.stats.now() - V.stats.WIN["24h"];
      for (const s of this.assets.values()) {
        const p = s.points; if (hot.has(s.scid) || !p || p.length < 64) continue;
        let lo = 0, hi = p.length - 1; while (lo < hi) { const m = (lo + hi) >> 1; if (p[m].ts < edge) lo = m + 1; else hi = m; }
        if (lo > 1) { s.points = p.slice(lo - 1); s.trimmed = true; }
      }
    },
  };
  V.live = live;
})();
