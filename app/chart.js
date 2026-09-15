// DMZ chart.js — the chart instrument, hand-drawn SVG: no library, no gradient fill, no glow, nothing rounded.
// A hairline grid, a 1px line, cut corners, axis labels in Label type and prices in Numeric. Candles come from the
// closing reserves the contract writes on every active block, so the series is exact at block resolution: wicks,
// bodies, a doji for a block with no trade, MA20, the buy/sell volume split, a current-price line and tag, a
// crosshair with price and time tags and an OHLC readout, markers for followed trades.
// The viewBox is drawn at 2x the width the chart is given (the token page measures its box and redraws when it
// changes), so k = 2 converts one rendered pixel into user units and every size is written in rendered pixels.
// Strokes are non-scaling: a hairline is one device pixel at any width.
(function () {
  const V = window.Veil, S = V.stats, U = V.ui, { h } = U;
  const NS = "http://www.w3.org/2000/svg";
  const svg = (tag, a, ...kids) => { const e = document.createElementNS(NS, tag); for (const [k, v] of Object.entries(a || {})) e.setAttribute(k, v); for (const k of kids) e.append(k); return e; };
  const txt = (x, y, s, a) => { const t = svg("text", Object.assign({ x, y, fill: "var(--fg3)", "font-family": "var(--font)" }, a || {})); t.textContent = s; return t; };
  // the two voices, on an axis: a label is tracked upper case, a value is tabular and tight
  const lbl = (x, y, s, k, a) => txt(x, y, String(s).toUpperCase(), Object.assign({ "font-size": Math.round(10.5 * k), "font-weight": 700, "letter-spacing": (1.05 * k).toFixed(1) }, a || {}));
  const val = (x, y, s, k, a) => txt(x, y, s, Object.assign({ "font-size": Math.round(10 * k), "font-weight": 650, fill: "var(--fg2)", style: "font-variant-numeric:tabular-nums" }, a || {}));
  const rule = (x1, y1, x2, y2, stroke, a) => svg("line", Object.assign({ x1, y1, x2, y2, stroke, "stroke-width": 1, "vector-effect": "non-scaling-stroke", "shape-rendering": "crispEdges" }, a || {}));
  const day = (d) => d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  const fmtT = (ts, span, iv) => { const d = new Date(ts * 1000); if (iv >= 86400) return day(d); const hm = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }); return span > 172800 ? day(d) + " " + hm : hm; };
  const UP = "var(--up)", DN = "var(--down)", GRID = "var(--edge)", GUIDE = "var(--edge2)";
  // one frame for the price pane, the volume pane and both axes, so grid lines, tick labels, the price tag and the
  // crosshair tags all land in the same columns
  const frame = (W, H, k, volH, topPx) => {
    const padL = 6 * k, padR = 64 * k, padB = 21 * k, top = (topPx || 9) * k, plotW = W - padR - padL, plotH = H - padB - volH - top;
    return { W, H, padL, padR, padB, top, plotW, R: padL + plotW, plotH, volTop: top + plotH + 8, volBase: H - padB - (volH ? 6 : 0), base: H - 7.5 * k };
  };
  // "nice" tick step for a linear axis; bounded, so a degenerate range can never loop
  function ticks(y0, y1, n) {
    if (!(y1 > y0)) return [y0];
    const raw = (y1 - y0) / n, mag = Math.pow(10, Math.floor(Math.log10(raw))), r = raw / mag;
    const step = (r < 1.5 ? 1 : r < 3.5 ? 2 : r < 7.5 ? 5 : 10) * mag; const out = [];
    for (let v = Math.ceil(y0 / step) * step; v <= y1 + step * 1e-9 && out.length < 40; v += step) out.push(v);
    return out;
  }
  // a log axis ticks at 1-2-5 of each decade when few decades are shown, at whole decades when many are
  function logTicks(f0, f1, n) {
    const e0 = Math.floor(f0), e1 = Math.ceil(f1), step = Math.max(1, Math.ceil((e1 - e0) / n)), ms = e1 - e0 <= n / 2 ? [1, 2, 5] : [1], out = [];
    for (let e = e0; e <= e1 && out.length < 40; e += step) for (const m of ms) { const v = m * 10 ** e, fv = Math.log10(v); if (fv >= f0 && fv <= f1) out.push(v); }
    return out.length >= 2 ? out : ticks(10 ** f0, 10 ** f1, n);
  }
  // right-hand axis tag (current price, crosshair price): a cut rectangle in the axis column, never a rounded chip
  const tag = (f, y, s, fill, color, k) => { const hgt = 18 * k; return [svg("rect", { x: f.R + 4 * k, y: y - hgt / 2, width: f.padR - 6 * k, height: hgt, fill, "shape-rendering": "crispEdges" }), txt(f.R + 9 * k, y + 3.6 * k, s, { fill: color, "font-size": Math.round(10 * k), "font-weight": 700, style: "font-variant-numeric:tabular-nums" })]; };
  // time axis: labels at tick fractions, first and last anchored inside the frame
  function timeAxis(el, X, x0, x1, f, k, n, iv, a) {
    const span = x1 - x0;
    for (let i = 0; i <= n; i++) { const t = x0 + (span * i) / n; el.append(lbl(X(t), f.base, fmtT(t, span, iv || 0), k, Object.assign({ "text-anchor": i === 0 ? "start" : i === n ? "end" : "middle" }, a || {}))); }
    return span;
  }
  // an empty instrument reads as an instrument with no signal: the frame is drawn, and the message says what the
  // area is, why it is blank and what fills it
  function blank(wrap, el, f, k, title, hint, busy, low) {
    for (let i = 0; i <= 4; i++) { const y = f.top + (f.plotH * i) / 4; el.append(rule(f.padL, y, f.R, y, GRID)); }
    el.append(rule(f.padL, f.volBase, f.R, f.volBase, GRID));
    // the message is punched out of the grid on the ground itself, so no rule runs through a sentence; under a
    // lone price level it sits low, so the level and its tag stay readable
    wrap.append(h("div", { style: "position:absolute;inset:0;display:flex;align-items:" + (low ? "flex-end;padding:0 12px 30px" : "center;padding:0 12px") + ";justify-content:center", role: "status" },
      h("div", { style: "background:var(--bg);padding:10px 16px;text-align:center;max-width:40ch" },
        h("div", { class: "eyebrow" }, title), h("div", { class: "note", style: "margin-top:5px" }, hint))));
    wrap.dataset.state = busy ? "loading" : "empty";
    return wrap;
  }

  // opts: { mode: "candles"|"area", iv: an S.IV key or "all", width (rendered px), height, quote, markers: [{ts, side, label}], loading }
  function render(root, s, opts = {}) {
    const mode = opts.mode || "candles", cw = Math.max(260, Math.round(opts.width || 430)), k = 2, W = cw * k;
    const H = (opts.height || (cw < 520 ? 250 : cw < 860 ? 300 : 340)) * k, volH = 32 * k;
    const nT = Math.max(2, Math.min(7, Math.floor(cw / 125))), nP = cw < 520 ? 5 : 6, qs = opts.quote || "DERO", fp = S.fmtPriceSub;
    const el = svg("svg", { viewBox: `0 0 ${W} ${H}`, class: "cx", role: "img", "aria-label": "price chart" });
    const wrap = h("div", { class: "chart" }, el), f = frame(W, H, k, volH, 20);
    const { padL, top, plotW, R, plotH, volTop, volBase } = f;
    if (opts.loading) return blank(wrap, el, f, k, "Loading price history", "Reading the closes this market's contract wrote, from your node.", true);
    // one candle per ~8 rendered pixels, so a phone gets fewer, wider candles and a desktop more
    const key = S.pickInterval(s, opts.iv), win = S.windowFor(s, key, Math.max(16, Math.min(150, Math.floor(plotW / k / 8))));
    const interval = win.interval, cs = S.candles(s, interval, win.from);
    wrap.dataset.interval = interval; wrap.dataset.iv = key; wrap.dataset.candles = cs.length;
    // fewer than two candles is not a failure: it is a market the chain has not written across two intervals yet
    if (cs.length < 2) {
      const one = cs.length === 1 ? cs[0] : null, my = top + plotH * 0.18, np = S.history(s).points;
      blank(wrap, el, f, k, !one ? "No price history" : np > 1 ? "One candle so far" : "One price point",
        !one ? "Candles are drawn from the closing reserves the contract writes on each active block. The first trade starts the series."
          : np > 1 ? "Every close this app holds falls inside one candle. The chart draws once the history spans two." : "The contract has written one closing price. The next active block draws the first candle.", false, !!one);
      if (one) {
        el.append(rule(padL, my, R, my, "var(--fg3)", { "stroke-dasharray": "3 3", "shape-rendering": "auto" }));
        el.append(svg("rect", { x: padL + plotW / 2 - 3.5 * k, y: my - 3.5 * k, width: 7 * k, height: 7 * k, fill: "var(--fg2)" }));
        el.append(...tag(f, my, fp(one.c), "var(--fg3)", "var(--bg)", k));
      }
      return wrap;
    }
    const last = cs[cs.length - 1], cur = s && s.price > 0 && isFinite(s.price) ? s.price : last.c;
    const ys = cs.flatMap((c) => [c.h, c.l]).concat(cur).filter((y) => y > 0 && isFinite(y));
    let y0 = Math.min(...ys), y1 = Math.max(...ys);
    // A swing of more than 25x would flatten everything before it into the floor on a linear axis, so the axis
    // turns logarithmic and says so in its unit label. A flat market gets a band of half a percent either side.
    const log = y1 / y0 > 25, F = log ? Math.log10 : (v) => v;
    if (!(y1 > y0)) { y0 *= 0.995; y1 *= 1.005; }
    let f0 = F(y0), f1 = F(y1); const padY = (f1 - f0) * 0.1; f0 -= padY; f1 += padY;
    if (!log && f0 < 0) f0 = 0; // a price axis does not run below zero
    const x0 = cs[0].t, x1 = last.t + interval;
    const X = (t) => padL + ((t - x0) / (x1 - x0)) * plotW, Y = (y) => top + (1 - (F(y) - f0) / (f1 - f0)) * plotH;
    const slot = plotW / cs.length, bw = Math.max(3, Math.floor(slot * 0.62)), wick = Math.max(1.5, Math.min(3, slot * 0.13));
    const upAll = last.c >= cs[0].o, col = upAll ? UP : DN, ly = Y(cur);
    // grid + price ticks: the label sits in the axis column, on its rule; a tick under the current-price tag is
    // drawn as a rule alone, because two readings in one slot is no reading at all
    // past a million a suffix keeps four digits, which a narrow range would print as the same label on every
    // rule: a linear tick gets as many digits as its step needs
    const tv = log ? logTicks(f0, f1, nP) : ticks(f0, f1, nP), st = tv.length > 1 ? tv[1] - tv[0] : 0;
    const tf = (v) => (!log && v >= 1e6 && st > 0 ? S.fmtBig(v, Math.max(4, Math.min(12, Math.ceil(Math.log10(v / st)) + 1))) : fp(v));
    for (const v of tv) { const yy = Y(v); if (!(yy >= top - 1 && yy <= top + plotH + 1)) continue; el.append(rule(padL, yy, R, yy, GRID)); if (Math.abs(yy - ly) > 19 * k) el.append(val(R + 9 * k, yy + 3.6 * k, tf(v), k)); }
    // the unit of the price axis above it, and the scale, where it is not linear, at the plot's right edge
    el.append(lbl(R + 9 * k, 12 * k, qs, k));
    if (log) el.append(lbl(R - 4 * k, 12 * k, "log scale", k, { "text-anchor": "end" }));
    // volume pane: buy over sell, its own baseline and separator, the label in Label type at the left
    const vmax = Math.max(...cs.map((c) => c.v), 1e-9);
    el.append(rule(padL, volTop - 4, R, volTop - 4, GRID), lbl(padL + 2, volTop + 13 * k, "Vol · " + qs, k));
    for (const c of cs) { if (!c.v) continue; const xc = X(c.t) + slot / 2, bh = (c.bv / vmax) * (volH - 10), sh = (c.sv / vmax) * (volH - 10); el.append(svg("rect", { x: xc - bw / 2, y: volBase - bh, width: bw, height: bh, fill: UP, opacity: .5 }), svg("rect", { x: xc - bw / 2, y: volBase - bh - sh, width: bw, height: sh, fill: DN, opacity: .5 })); }
    el.append(rule(padL, volBase, R, volBase, GRID));
    if (mode === "area") {
      // a 1px line and nothing under it: a gradient fill would say something the data does not
      const d = cs.map((c, i) => (i ? "L" : "M") + (X(c.t) + slot / 2).toFixed(1) + " " + Y(c.c).toFixed(1)).join(" ");
      el.append(svg("path", { d, fill: "none", stroke: col, "stroke-width": 1, "vector-effect": "non-scaling-stroke", "stroke-linejoin": "round", "stroke-linecap": "round" }));
    } else {
      for (const c of cs) {
        const xc = X(c.t) + slot / 2, cc = c.up === null ? "var(--fg3)" : c.up ? UP : DN;
        if (c.up !== null) el.append(svg("rect", { x: xc - wick / 2, y: Y(c.h), width: wick, height: Math.max(1, Y(c.l) - Y(c.h)), fill: cc }));
        const yo = Y(c.o), yc = Y(c.c), bh = Math.max(2, Math.abs(yc - yo));
        el.append(svg("rect", { x: xc - bw / 2, y: Math.min(yo, yc), width: bw, height: c.up === null ? 2 : bh, fill: cc, opacity: c.up === null ? .45 : 1 }));
      }
      // MA20 of closes — structure, so it is drawn in graphite ink: lime is a verb and never a chart line
      if (cs.length >= 20) { let sum = 0; const pts = []; for (let i = 0; i < cs.length; i++) { sum += cs[i].c; if (i >= 20) sum -= cs[i - 20].c; if (i >= 19) pts.push((pts.length ? "L" : "M") + (X(cs[i].t) + slot / 2).toFixed(1) + " " + Y(sum / 20).toFixed(1)); } el.append(svg("path", { d: pts.join(" "), fill: "none", stroke: "var(--fg3)", "stroke-width": 1, "vector-effect": "non-scaling-stroke", "stroke-linejoin": "round" }), lbl(padL + 2, 12 * k, "MA20", k)); }
    }
    // the current price: a dashed level across the pane, the axis tag, and a square on the last close
    el.append(rule(padL, ly, R, ly, col, { "stroke-dasharray": "4 4", "shape-rendering": "auto" }));
    const lastTag = svg("g", {}, ...tag(f, ly, fp(cur), col, "var(--bg)", k)); el.append(lastTag);
    el.append(svg("rect", { x: X(last.t) + slot / 2 - 3.5 * k, y: Y(last.c) - 3.5 * k, width: 7 * k, height: 7 * k, fill: col, class: "live-dot" }));
    // markers: a triangle per trade by you or an account you follow
    for (const m of opts.markers || []) { if (m.ts < x0 || m.ts > x1) continue; const c = cs.find((q) => m.ts >= q.t && m.ts < q.t + interval) || last; const cx = X(c.t) + slot / 2, cy = Y(m.side === 1 ? c.l : c.h) + (m.side === 1 ? 20 : -20); const g = svg("g", {}, svg("path", { d: m.side === 1 ? `M${cx} ${cy - 9} l9 12 h-18 z` : `M${cx} ${cy + 9} l9 -12 h-18 z`, fill: m.side === 1 ? UP : DN, stroke: "var(--bg)", "stroke-width": 2 })); const t = svg("title"); t.textContent = (m.side === 1 ? "buy" : "sell") + " · " + (m.label || ""); g.append(t); el.append(g); }
    // time labels are values the reader needs, so they take the secondary ink: --fg3 at this size is under 4.5:1
    // as many time labels as fit without touching: a date-and-time label needs twice the room of a time
    const wide = fmtT(x1, x1 - x0, interval).length * 8 + 26, span = timeAxis(el, X, x0, x1, f, k, Math.max(1, Math.min(nT, Math.floor(plotW / k / wide))), interval, { fill: "var(--fg2)" });
    const lo = Math.min(...cs.map((c) => c.l)), hi = Math.max(...cs.map((c) => c.h));
    el.setAttribute("aria-label", "Price chart, " + cs.length + " candles of " + (key === "all" ? S.ageText(interval / 18) : key) + " from " + fmtT(x0, span, 0) + " to " + fmtT(last.t, span, 0) + ": now " + fp(cur) + " " + qs + ", high " + fp(hi) + ", low " + fp(lo) + (log ? ", log scale" : ""));
    // crosshair: quiet guides in --edge2, precise tags. The reading is in the tags, not in a bright line.
    const tw = 84 * k, th = 18 * k, ty = H - f.padB + 2 * k;
    const cross = svg("g", { visibility: "hidden" },
      rule(0, top, 0, volBase, GUIDE, { "stroke-dasharray": "3 3", "shape-rendering": "auto" }),
      rule(padL, 0, R, 0, GUIDE, { "stroke-dasharray": "3 3", "shape-rendering": "auto" }),
      ...tag(f, 0, "", "var(--fg)", "var(--bg)", k),
      svg("rect", { x: 0, y: ty, width: tw, height: th, fill: "var(--fg)", "shape-rendering": "crispEdges" }),
      txt(0, ty + 12.4 * k, "", { fill: "var(--bg)", "font-size": Math.round(10 * k), "font-weight": 700, "text-anchor": "middle", style: "font-variant-numeric:tabular-nums" }));
    el.append(cross);
    const tip = h("div", { class: "ctip", hidden: true }), said = h("div", { class: "sr", "aria-live": "polite" }); wrap.append(tip, said);
    const row = (l, v) => `<span style="display:flex;justify-content:space-between;gap:14px"><b>${l}</b>${v}</span>`;
    const chgOf = (c) => (c.o > 0 ? ((c.c - c.o) / c.o) * 100 : 0);
    const show = (i, yp) => {
      const c = cs[i], xc = X(c.t) + slot / 2; if (yp === undefined) yp = Y(c.c);
      const pv = log ? 10 ** (f1 - ((yp - top) / plotH) * (f1 - f0)) : f1 - ((yp - top) / plotH) * (f1 - f0);
      cross.setAttribute("visibility", "visible"); const [vl, hl, pr, pt, tr, tt] = cross.children;
      vl.setAttribute("x1", xc); vl.setAttribute("x2", xc); hl.setAttribute("y1", yp); hl.setAttribute("y2", yp);
      pr.setAttribute("y", yp - 9 * k); pt.setAttribute("y", yp + 3.6 * k); pt.textContent = fp(pv);
      lastTag.setAttribute("visibility", Math.abs(yp - ly) < 19 * k ? "hidden" : "visible"); // never two tags on top of each other
      const tx = Math.max(padL, Math.min(R - tw, xc - tw / 2)); tr.setAttribute("x", tx); tt.setAttribute("x", tx + tw / 2); tt.textContent = fmtT(c.t, span, interval).toUpperCase();
      const chg = chgOf(c);
      // numbers only: this is the one innerHTML in the app
      tip.hidden = false; tip.innerHTML = `<b>${fmtT(c.t, span, interval)} · ${c.n} blk</b>${row("O", fp(c.o))}${row("H", fp(c.h))}${row("L", fp(c.l))}${row("C", `${fp(c.c)} <i class="${chg >= 0 ? "up" : "down"}">${chg >= 0 ? "+" : ""}${chg.toFixed(2)}%</i>`)}${row("Vol", `<i class="up">${S.fmtDeroShort(c.bv)}</i> / <i class="down">${S.fmtDeroShort(c.sv)}</i>`)}`;
      tip.style.left = (xc / W < 0.5 ? (xc / W) * 100 + 3 : (xc / W) * 100 - 44) + "%";
      return c;
    };
    const onLeave = () => { cross.setAttribute("visibility", "hidden"); lastTag.setAttribute("visibility", "visible"); tip.hidden = true; };
    el.addEventListener("pointermove", (ev) => {
      const r = el.getBoundingClientRect(); const px = ((ev.clientX - r.left) / r.width) * W, py = ((ev.clientY - r.top) / r.height) * H;
      if (px < padL || px > R || py < top || py > volBase) return onLeave();
      show(Math.min(cs.length - 1, Math.max(0, Math.floor(((px - padL) / plotW) * cs.length))), Math.min(top + plotH, py));
    });
    el.addEventListener("pointerleave", onLeave);
    // the same crosshair by keyboard: the chart is one tab stop, the arrows step a candle, PageUp/PageDown ten,
    // Home and End the ends, Escape clears. Each step is read out in words through a polite live region.
    let ki = -1;
    const say = (c) => { const chg = chgOf(c); said.textContent = fmtT(c.t, span, 0) + ", candle " + (ki + 1) + " of " + cs.length + ": open " + fp(c.o) + ", high " + fp(c.h) + ", low " + fp(c.l) + ", close " + fp(c.c) + " " + qs + ", " + (chg >= 0 ? "up " : "down ") + Math.abs(chg).toFixed(2) + "%. Volume " + S.fmtDeroShort(c.bv) + " bought, " + S.fmtDeroShort(c.sv) + " sold."; };
    wrap.setAttribute("tabindex", "0"); wrap.setAttribute("role", "group"); wrap.setAttribute("aria-roledescription", "chart");
    wrap.setAttribute("aria-label", "Price chart. Arrow keys step through the candles; Home and End jump to the first and last.");
    wrap.addEventListener("keydown", (e) => {
      if (e.key === "Escape") { ki = -1; return onLeave(); }
      const d = { ArrowLeft: -1, ArrowRight: 1, PageUp: -10, PageDown: 10 }[e.key];
      if (d === undefined && e.key !== "Home" && e.key !== "End") return;
      e.preventDefault();
      ki = e.key === "Home" ? 0 : e.key === "End" ? cs.length - 1 : Math.max(0, Math.min(cs.length - 1, (ki < 0 ? cs.length - 1 : ki) + d));
      say(show(ki));
    });
    wrap.addEventListener("blur", () => { ki = -1; onLeave(); });
    return wrap;
  }

  // generic line series for the portfolio: [{ts, v}] in DERO — same frame, same axis column, same empty state
  function series(root, pts, opt = {}) {
    const W = 860, H = (opt.height || 160) * 2, k = opt.wide ? 1.62 : 2.16, nP = 3;
    const el = svg("svg", { viewBox: `0 0 ${W} ${H}`, class: "cx" }); // height comes from the viewBox aspect; a fixed px height overflowed it
    const wrap = h("div", { class: "chart" }, el), f = frame(W, H, k, 0);
    const { padL, top, plotW, R, plotH } = f;
    if (pts.length < 2) return blank(wrap, el, f, k, "No history yet", opt.empty || "Not enough history yet");
    const x0 = pts[0].ts, x1 = pts[pts.length - 1].ts, ys = pts.map((p) => p.v); let y0 = Math.min(...ys), y1 = Math.max(...ys); if (y1 === y0) { y0 *= .99; y1 *= 1.01; } if (y1 === y0) { y0 -= 1; y1 += 1; }
    const X = (t) => padL + ((t - x0) / (x1 - x0 || 1)) * plotW, Y = (v) => top + (1 - (v - y0) / (y1 - y0)) * plotH;
    const up = ys[ys.length - 1] >= ys[0], col = up ? UP : DN;
    for (const v of ticks(y0, y1, nP)) { const yy = Y(v); el.append(rule(padL, yy, R, yy, GRID), val(R + 9 * k, yy + 3.6 * k, S.fmtDeroShort(v), k)); }
    const d = pts.map((p, i) => (i ? "L" : "M") + X(p.ts).toFixed(1) + " " + Y(p.v).toFixed(1)).join(" ");
    el.append(svg("path", { d, fill: "none", stroke: col, "stroke-width": 1, "vector-effect": "non-scaling-stroke", "stroke-linejoin": "round", "stroke-linecap": "round" }));
    const lv = ys[ys.length - 1], ly = Y(lv);
    el.append(rule(padL, ly, R, ly, col, { "stroke-dasharray": "4 4", "shape-rendering": "auto" }), ...tag(f, ly, S.fmtDeroShort(lv), col, "var(--bg)", k), svg("rect", { x: X(x1) - 3.5 * k, y: ly - 3.5 * k, width: 7 * k, height: 7 * k, fill: col, class: "live-dot" }));
    timeAxis(el, X, x0, x1, f, k, opt.wide ? 6 : 3);
    return wrap;
  }
  V.chart = { render, series };
})();
