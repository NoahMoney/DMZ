// DMZ stats.js — every market metric, derived only from contract state (price points p_<n>, published trades t_<n>,
// live reserves). Accuracy is block resolution: the chain records the closing reserves of every active block.
(function () {
  const V = window.Veil, A = V.amm;
  const WIN = { "5m": 300, "1h": 3600, "6h": 21600, "24h": 86400, "7d": 604800 };
  const price = (s, pt) => A.priceOf(s, pt);
  const now = () => Math.floor(Date.now() / 1000);
  // A record with no price history is not an error here. Anything the factory lists reaches these functions, and a
  // contract registered as a market that is not one arrives with neither points nor trades; reading them as empty
  // keeps one such registration from throwing here and taking every other market's metrics down with it. feed.js
  // also refuses to file it as a market, which is the other half of the same fix.
  const pts = (s) => (s && Array.isArray(s.points) ? s.points : []);
  const trs = (s) => (s && Array.isArray(s.trades) ? s.trades : []);

  // last recorded price at or before ts; falls back to the first point when the token is younger than the window.
  // Points are appended in ascending ts, so this is a binary search rather than a prefix scan: enrich() asks for
  // five windows per market on every block, and the 7d window used to walk almost the whole history each time.
  function priceAt(s, ts) {
    const p = pts(s); if (!p.length) return null;
    let lo = 0, hi = p.length - 1, r = -1;
    while (lo <= hi) { const mid = (lo + hi) >> 1; if (p[mid].ts <= ts) { r = mid; lo = mid + 1; } else hi = mid - 1; }
    return price(s, p[r < 0 ? 0 : r]);
  }
  function change(s, secs) {
    const p1 = price(s), p0 = priceAt(s, now() - secs);
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
  // One quote unit, in the atomic units the contract counts. Every reserve, volume and liquidity number on a
  // market is denominated in that market's QUOTE, and DERO's five decimals were hard-coded into all of them.
  // Both simulator vaults happen to be five-decimal too, so nothing looked wrong — but a real Monero vault is
  // twelve, which would have reported 0.431 XMR of liquidity as 4,310,000 XMR. quoteDec is set by nameQuote
  // before enrich runs; the fallback keeps a DERO market on exactly the numbers it had.
  const qunit = (s) => 10 ** (Number.isInteger(s && s.quoteDec) ? s.quoteDec : 5);
  // How many whole units of the quote stand where one DERO stands in the activity rules below (thin liquidity,
  // the hot ranking's volume term). Both were written as DERO counts, and a count of XMR is not a count of
  // DERO: twenty XMR is not a thin pool, and log1p of a third of an XMR ranks a busy Monero market under a
  // quiet DERO one. The scale belongs beside the quote's decimals in config, copied onto the market the way
  // quoteDec is; until a vault carries one, 1 keeps every market on exactly the numbers it had.
  const qscale = (s) => (s && Number.isFinite(s.quoteScale) && s.quoteScale > 0 ? s.quoteScale : 1);
  // net reserve flow per active block inside the window: covers unpublished trades too (netted per block)
  function flow(s, secs) {
    // walked backwards and stopped at the window edge: the sums are order-independent, and the old forward scan
    // read the whole history to reach a window that is usually the last few points of it
    const from = now() - secs, p = pts(s); let vol = 0n, up = 0, down = 0, blocks = 0, big = 0;
    for (let i = p.length - 1; i >= 1; i--) {
      const a = p[i - 1], b = p[i];
      if (b.ts < from) break;
      if (b.rd === a.rd) continue;
      if (lpMove(s, a, b)) continue;
      const d = b.rd > a.rd ? b.rd - a.rd : a.rd - b.rd; vol += d; blocks++; if (b.rd > a.rd) up++; else down++;
      // `big` is `blocks` with a size floor: a move of at least one basis point of the quote reserve. One atomic
      // unit moves the reserves as surely as a whole coin does, so a count of active blocks is bought with gas
      // alone — 329 atomic units a block, measured. Only `big` feeds the ranking; blocks, up and down still count
      // every active block, which is what the block-pressure read-out means.
      if (d * 10000n >= a.rd) big++;
    }
    return { vol: Number(vol) / qunit(s), blocks, big, up, down };
  }
  function published(s, secs) {
    const from = now() - secs, tr = trs(s); let buys = 0, sells = 0, bv = 0n, sv = 0n, last = 0;
    const who = new Set();
    for (let i = tr.length - 1; i >= 0; i--) { const t = tr[i]; if (t.ts < from) break; who.add(t.addr); if (t.side === 1) { buys++; bv += t.in; } else { sells++; sv += t.out; } if (t.ts > last) last = t.ts; }
    return { buys, sells, who: who.size, buyVol: Number(bv) / qunit(s), sellVol: Number(sv) / qunit(s), last };
  }
  function enrich(s, height) {
    const p = price(s);
    const f24 = flow(s, WIN["24h"]), f1 = flow(s, WIN["1h"]), pub24 = published(s, WIN["24h"]), pub1 = published(s, WIN["1h"]);
    const m = {
      price: p, c5m: change(s, WIN["5m"]), c1h: change(s, WIN["1h"]), c6h: change(s, WIN["6h"]), c24h: change(s, WIN["24h"]),
      flow1h: f1,
      vol24: f24.vol, buys24: pub24.buys, sells24: pub24.sells, upBlocks24: f24.up, downBlocks24: f24.down,
      mcap: s.kind === "launch" ? p * (Number(s.supply) / 10 ** Number(s.dec)) : null, // fully diluted: price × total supply (labelled FDV)
      liq: s.kind === "launch" && s.phase === 0n ? Number(s.real) / qunit(s) : (Number(s.rd) / qunit(s)) * 2, // a curve holds only the quote actually raised
      lastTs: pts(s).length ? pts(s)[pts(s).length - 1].ts : 0,
      age: s.installHeight && height ? Math.max(0, height - s.installHeight) : null,
      progress: s.kind === "launch" && s.phase === 0n && s.G > 0n ? Number(s.real * 10000n / s.G) / 100 : (s.kind === "launch" ? 100 : null),
      // distinct publishers over the whole history. Only the token screen reads it, so it is computed the first
      // time that screen asks and then kept on this m, rather than rebuilt for every market on every block.
      get holders() { const n = new Set(trs(s).map((t) => t.addr)).size; Object.defineProperty(this, "holders", { value: n }); return n; },
    };
    // anti-wash, three guards: distinct publishers count, not published trades (one account spamming posts gains
    // nothing); volume enters through a log so a single large reserve move does not own the ranking; and the
    // activity term counts only blocks whose move cleared flow()'s size floor, at most 40 of them. That term was
    // uncapped and floorless, which made it both the largest in the score and the cheapest: one dust swap a block
    // bought 4 points, up to 800 an hour, for gas.
    m.hot = Math.min(40, f1.big) * 4 + pub1.who * 3 + Math.min(50, Math.log1p(f1.vol / qscale(s)) * 8);
    m.isNew = m.age !== null && m.age < 400; // ~2 hours of 18 s blocks
    m.thin = m.liq < 20 * qscale(s); // small orders move the price a lot: twenty DERO, or the quote's equivalent
    s.m = m; s.price = p; s.change24 = m.c24h; s.vol24 = m.vol24;
    return m;
  }

  // ---- candles from per-block closes ------------------------------------------------------------------------
  // interval in seconds; returns [{t, o, h, l, c, v, up}] where v is the reserve flow (in the quote) and empty intervals
  // carry the previous close as a flat candle (drawn as a doji), exactly like an exchange with no trades.
  // The window starts at the last close before `from`, so the flat candles at its left edge carry the price the
  // market really had rather than the first price inside the window. A block whose price reads as zero (a pool
  // before its first deposit) never becomes a close, and candles before the first real price are dropped.
  function candles(s, interval, from, at) {
    const all = pts(s); if (!all.length || !(interval > 0)) return [];
    let lo = 0, hi = all.length; while (lo < hi) { const m = (lo + hi) >> 1; if (all[m].ts < from) lo = m + 1; else hi = m; }
    const p = all.slice(Math.max(0, lo - 1)), q = qunit(s), tEnd = at || now();
    let t0 = Math.floor(Math.max(from, p[0].ts) / interval) * interval;
    if ((tEnd - t0) / interval > 2000) t0 = tEnd - 2000 * interval - (tEnd % interval); // a bound, whatever the caller asks
    const out = []; let prevClose = 0, prevRd = p[0].rd, prevPt = p[0], i = 0;
    for (let t = t0; t <= tEnd; t += interval) {
      const c = { t, o: 0, h: 0, l: 0, c: 0, v: 0, bv: 0, sv: 0, up: true, n: 0 };
      while (i < p.length && p[i].ts < t) { const pr = price(s, p[i]); if (pr > 0) prevClose = pr; prevRd = p[i].rd; prevPt = p[i]; i++; }
      c.o = c.h = c.l = c.c = prevClose;
      while (i < p.length && p[i].ts < t + interval) {
        const pt = p[i], pr = price(s, pt);
        if (pr > 0) { if (!c.o) c.o = c.h = c.l = pr; c.h = Math.max(c.h, pr); c.l = Math.min(c.l, pr); c.c = pr; c.n++; prevClose = pr; }
        // reserve flow: quote in = net buying, quote out = net selling (per active block)
        if (!lpMove(s, prevPt, pt)) { if (pt.rd >= prevRd) c.bv += Number(pt.rd - prevRd) / q; else c.sv += Number(prevRd - pt.rd) / q; }
        prevRd = pt.rd; prevPt = pt; i++;
      }
      c.v = c.bv + c.sv; c.up = c.n === 0 ? null : c.c >= c.o;
      if (c.c > 0) out.push(c);
    }
    return out;
  }
  // ---- candle intervals --------------------------------------------------------------------------------------
  // The chain records one close per active block (~18 s), so a minute is the finest interval that is still exact:
  // nothing below it is offered. At the other end an interval is offered only when the history this app holds
  // spans at least MINC of its candles; fewer is a bar, not a chart. That history is what feed.js loaded (its
  // newest points), so a market whose older closes stay on chain is offered what the loaded part can fill.
  const IV = { "1m": 60, "5m": 300, "15m": 900, "1h": 3600, "4h": 14400, "1D": 86400, "1W": 604800 };
  const MINC = 4;
  function history(s, at) {
    const p = pts(s), t = at || now(), rec = p.filter((x) => !x.live).length;
    // it starts at the first close with a price: a pool's blocks before its first deposit draw nothing
    const i = p.findIndex((x) => price(s, x) > 0), n = i < 0 ? 0 : p.length - i;
    return { first: n ? p[i].ts : t, span: n > 1 ? Math.max(0, t - p[i].ts) : 0, points: n, truncated: !!s && s.pc !== undefined && Number(s.pc) > rec };
  }
  const intervals = (s, at) => { const hs = history(s, at); return Object.keys(IV).filter((k) => hs.span >= MINC * IV[k]); };
  // A choice the market can fill is kept. Otherwise: the coarsest interval that still fills 30 candles of the
  // history, else the finest offered, else ALL.
  function pickInterval(s, want, at) {
    const ks = intervals(s, at), span = history(s, at).span;
    if (want === "all" || ks.includes(want)) return want;
    return ks.filter((k) => span / IV[k] >= 30).pop() || ks[0] || "all";
  }
  // the window one interval draws: the newest maxC candles of it, or for ALL the whole history at the finest
  // interval that fits maxC candles across it
  function windowFor(s, key, maxC, at) {
    const t = at || now(), hs = history(s, at), n = Math.max(8, maxC || 90);
    if (key !== "all" && IV[key]) return { key, interval: IV[key], from: Math.max(hs.first, t - n * IV[key]) };
    const k = Object.keys(IV).find((x) => hs.span / IV[x] <= n);
    return { key: "all", interval: k ? IV[k] : Math.ceil(hs.span / n / IV["1W"]) * IV["1W"], from: hs.first };
  }

  // ---- formatting ----
  const SUB = "₀₁₂₃₄₅₆₇₈₉";
  // A price is positive and finite or it is not a price: anything else is a dash, never "NaN", "Infinity" or 0.
  // Below a ten-thousandth the zeros are counted in subscript (0.00000123 → 0.0₅123), read off toExponential so
  // the rounding carries into the exponent and no price is too small to print. From a million up, a suffix.
  function fmtPriceSub(p) {
    if (typeof p !== "number" || !isFinite(p) || p <= 0) return "—";
    if (p >= 1e6) return fmtBig(p, 4);
    if (p >= 1000) return p.toLocaleString(undefined, { maximumFractionDigits: 2 });
    if (p >= 1) return p.toLocaleString(undefined, { maximumFractionDigits: 4 });
    if (p >= 0.001) return p.toLocaleString(undefined, { maximumSignificantDigits: 5 });
    const [m, e] = p.toExponential(3).split("e"), z = -Number(e) - 1;
    if (z < 4) return p.toLocaleString(undefined, { maximumSignificantDigits: 4 });
    return "0.0" + String(z).split("").map((d) => SUB[+d]).join("") + (m.replace(".", "").replace(/0+$/, "") || "0");
  }
  // 1.23M, 4.5B, 7T, 2.1Q; past a quintillion the exponent itself, which is still a number and never Infinity
  const fmtBig = (x, sig) => { const t = Math.min(5, Math.floor(Math.log10(x) / 3)); return x >= 1e18 ? x.toExponential(2).replace("e+", "e") : String(+(x / 10 ** (3 * t)).toPrecision(sig)) + ["", "K", "M", "B", "T", "Q"][t]; };
  // Short amounts in whatever the quote is. Four fixed places were DERO's own scale: a twelve-decimal quote
  // puts a whole pool below a thousandth, where toFixed(4) printed "0.0000" for every aggregate on the market.
  // Below that line the number keeps three significant digits instead — toFixed rather than toPrecision,
  // because toPrecision switches to exponent form under 1e-6 and toFixed never does. From a thousandth up,
  // everything renders exactly as it did.
  const fmtTiny = (x) => x.toFixed(Math.min(100, Math.floor(-Math.log10(x)) + 3)).replace(/0+$/, "");
  const fmtDeroShort = (x) => (typeof x !== "number" || !isFinite(x) ? "—" : x >= 1e9 ? fmtBig(x, 3) : x >= 1e6 ? (x / 1e6).toFixed(2) + "M" : x >= 1e4 ? (x / 1e3).toFixed(1) + "K" : x >= 100 ? x.toFixed(0) : x >= 1 ? x.toFixed(2) : x >= 1e-3 ? x.toFixed(4) : x > 0 ? fmtTiny(x) : "0");
  const ageText = (blocks) => { if (blocks === null || blocks === undefined) return "—"; const s = blocks * 18; if (s < 3600) return Math.max(1, Math.round(s / 60)) + "m"; if (s < 86400) return Math.round(s / 3600) + "h"; return Math.round(s / 86400) + "d"; };

  // ---- watchlist (per device) ----
  const watch = {
    key: "veil.watch",
    all() { try { return new Set(JSON.parse(localStorage.getItem(this.key) || "[]")); } catch (e) { return new Set(); } },
    has(scid) { return this.all().has(scid); },
    toggle(scid) { const s = this.all(); s.has(scid) ? s.delete(scid) : s.add(scid); try { localStorage.setItem(this.key, JSON.stringify([...s])); } catch (e) {} return s.has(scid); },
  };

  V.stats = { WIN, IV, MINC, now, priceAt, change, flow, published, enrich, candles, history, intervals, pickInterval, windowFor, fmtPriceSub, fmtBig, fmtDeroShort, ageText, watch };
})();
