// DMZ stats.js — every market metric, derived only from contract state (price points p_<n>, published trades t_<n>,
// live reserves). Accuracy is block resolution: the chain records the closing reserves of every active block.
(function () {
  const V = window.Veil, A = V.amm;
  const WIN = { "5m": 300, "1h": 3600, "6h": 21600, "24h": 86400, "7d": 604800 };
  const price = (p, dec) => A.price(p, dec);
  const now = () => Math.floor(Date.now() / 1000);

  // last recorded price at or before ts; falls back to the first point when the token is younger than the window
  function priceAt(s, ts) {
    let r = null;
    for (const p of s.points) { if (p.ts <= ts) r = p; else break; }
    if (!r && s.points.length) r = s.points[0];
    return r ? price(r, s.dec) : null;
  }
  function change(s, secs) {
    const p1 = price(s, s.dec), p0 = priceAt(s, now() - secs);
    return p0 > 0 && p1 > 0 ? ((p1 - p0) / p0) * 100 : null;
  }
  // liquidity added or removed on a pool is not a trade: both reserves move in proportion (price unchanged), and the
  // first deposit moves them from nothing
  function lpMove(s, a, b) {
    if (s.kind !== "pool") return false;
    if (a.rd === 0n || a.ra === 0n || b.ra === 0n) return true;
    const pa = Number(a.rd) / Number(a.ra), pb = Number(b.rd) / Number(b.ra);
    return Math.abs(pb - pa) / pa < 1e-6;
  }
  // net reserve flow per active block inside the window: covers unpublished trades too (netted per block)
  function flow(s, secs) {
    const from = now() - secs, pts = s.points; let vol = 0n, up = 0, down = 0, blocks = 0;
    for (let i = 1; i < pts.length; i++) {
      const a = pts[i - 1], b = pts[i]; if (b.ts < from || b.rd === a.rd) continue;
      if (lpMove(s, a, b)) continue;
      const d = b.rd > a.rd ? b.rd - a.rd : a.rd - b.rd; vol += d; blocks++; if (b.rd > a.rd) up++; else down++;
    }
    return { vol: Number(vol) / 1e5, blocks, up, down };
  }
  function published(s, secs) {
    const from = now() - secs; let buys = 0, sells = 0, bv = 0n, sv = 0n, last = 0;
    const who = new Set();
    for (const t of s.trades) { if (t.ts < from) continue; who.add(t.addr); if (t.side === 1) { buys++; bv += t.in; } else { sells++; sv += t.out; } if (t.ts > last) last = t.ts; }
    return { buys, sells, who: who.size, buyVol: Number(bv) / 1e5, sellVol: Number(sv) / 1e5, last };
  }
  function enrich(s, height) {
    const p = price(s, s.dec);
    const f24 = flow(s, WIN["24h"]), f1 = flow(s, WIN["1h"]), pub24 = published(s, WIN["24h"]), pub1 = published(s, WIN["1h"]);
    const m = {
      price: p, c5m: change(s, WIN["5m"]), c1h: change(s, WIN["1h"]), c6h: change(s, WIN["6h"]), c24h: change(s, WIN["24h"]), c7d: change(s, WIN["7d"]),
      flow24: f24, flow1h: f1, pub24, pub1,
      vol24: f24.vol, blocks24: f24.blocks, buys24: pub24.buys, sells24: pub24.sells, upBlocks24: f24.up, downBlocks24: f24.down,
      mcap: s.kind === "launch" ? p * (Number(s.supply) / 10 ** Number(s.dec)) : null, // fully diluted: price × total supply (labelled FDV)
      liq: s.kind === "launch" && s.phase === 0n ? Number(s.real) / 1e5 : (Number(s.rd) / 1e5) * 2, // a curve holds only the DERO actually raised
      lastTs: s.points.length ? s.points[s.points.length - 1].ts : 0,
      age: s.installHeight && height ? Math.max(0, height - s.installHeight) : null,
      progress: s.kind === "launch" && s.phase === 0n && s.G > 0n ? Number(s.real * 10000n / s.G) / 100 : (s.kind === "launch" ? 100 : null),
      holders: new Set(s.trades.map((t) => t.addr)).size,
    };
    // anti-wash: distinct publishers count, not published trades (one account spamming posts gains nothing), and
    // volume enters through a log so a single large reserve move does not own the ranking
    m.hot = f1.blocks * 4 + pub1.who * 3 + Math.min(50, Math.log1p(f1.vol) * 8);
    m.isNew = m.age !== null && m.age < 400; // ~2 hours of 18 s blocks
    s.m = m; s.price = p; s.change24 = m.c24h; s.vol24 = m.vol24;
    return m;
  }

  // ---- candles from per-block closes ------------------------------------------------------------------------
  // interval in seconds; returns [{t, o, h, l, c, v, up}] where v is the reserve flow (DERO) and empty intervals
  // carry the previous close as a flat candle (drawn as a doji), exactly like an exchange with no trades.
  function candles(s, interval, from) {
    const pts = s.points.filter((p) => p.ts >= from - interval); if (!pts.length) return [];
    const dec = s.dec; const out = []; let prevClose = price(pts[0], dec), prevRd = pts[0].rd, prevPt = pts[0];
    const t0 = Math.floor(Math.max(from, pts[0].ts) / interval) * interval, tEnd = now();
    let i = 0;
    for (let t = t0; t <= tEnd; t += interval) {
      const c = { t, o: prevClose, h: prevClose, l: prevClose, c: prevClose, v: 0, bv: 0, sv: 0, up: true, n: 0 };
      while (i < pts.length && pts[i].ts < t) { prevClose = price(pts[i], dec); prevRd = pts[i].rd; prevPt = pts[i]; i++; c.o = prevClose; c.h = prevClose; c.l = prevClose; c.c = prevClose; }
      while (i < pts.length && pts[i].ts < t + interval) {
        const p = pts[i], pr = price(p, dec); if (pr > 0) { c.h = Math.max(c.h, pr); c.l = Math.min(c.l, pr); c.c = pr; c.n++; }
        // reserve flow: DERO in = net buying, DERO out = net selling (per active block)
        if (!lpMove(s, prevPt, p)) { if (p.rd >= prevRd) c.bv += Number(p.rd - prevRd) / 1e5; else c.sv += Number(prevRd - p.rd) / 1e5; }
        prevRd = p.rd; prevPt = p; prevClose = pr; i++;
      }
      c.v = c.bv + c.sv; c.up = c.n === 0 ? null : c.c >= c.o;
      out.push(c);
    }
    return out;
  }
  const RANGES = { "1h": [3600, 60], "6h": [21600, 300], "24h": [86400, 900], "7d": [604800, 7200], all: [0, 0] };
  // interval follows the range, but a market younger than the range gets finer candles (~60 across its life)
  function rangeFor(s, key) {
    let [span, iv] = RANGES[key] || RANGES["24h"];
    const first = s.points.length ? s.points[0].ts : now(), life = Math.max(600, now() - first);
    if (!span) { span = Math.max(3600, life); iv = 0; }
    if (life < span) { span = life; iv = 0; }
    if (!iv) iv = Math.max(60, Math.ceil(span / 60 / 60) * 60);
    return { from: now() - span, interval: iv };
  }

  // ---- formatting ----
  const SUB = "₀₁₂₃₄₅₆₇₈₉";
  function fmtPriceSub(p) {
    if (!p || !isFinite(p)) return "—";
    if (p >= 1) return p.toLocaleString(undefined, { maximumFractionDigits: 4 });
    if (p >= 0.001) return p.toLocaleString(undefined, { maximumSignificantDigits: 5 });
    const s = p.toFixed(20).slice(2); let z = 0; while (z < s.length && s[z] === "0") z++;
    if (z < 4) return p.toLocaleString(undefined, { maximumSignificantDigits: 4 });
    return "0.0" + String(z).split("").map((d) => SUB[+d]).join("") + s.slice(z, z + 4);
  }
  const fmtDeroShort = (x) => (x >= 1e6 ? (x / 1e6).toFixed(2) + "M" : x >= 1e4 ? (x / 1e3).toFixed(1) + "K" : x >= 100 ? x.toFixed(0) : x >= 1 ? x.toFixed(2) : x > 0 ? x.toFixed(4) : "0");
  const ageText = (blocks) => { if (blocks === null || blocks === undefined) return "—"; const s = blocks * 18; if (s < 3600) return Math.max(1, Math.round(s / 60)) + "m"; if (s < 86400) return Math.round(s / 3600) + "h"; return Math.round(s / 86400) + "d"; };

  // ---- watchlist (per device) ----
  const watch = {
    key: "veil.watch",
    all() { try { return new Set(JSON.parse(localStorage.getItem(this.key) || "[]")); } catch (e) { return new Set(); } },
    has(scid) { return this.all().has(scid); },
    toggle(scid) { const s = this.all(); s.has(scid) ? s.delete(scid) : s.add(scid); try { localStorage.setItem(this.key, JSON.stringify([...s])); } catch (e) {} return s.has(scid); },
  };

  V.stats = { WIN, RANGES, now, priceAt, change, flow, published, enrich, candles, rangeFor, fmtPriceSub, fmtDeroShort, ageText, watch };
})();
