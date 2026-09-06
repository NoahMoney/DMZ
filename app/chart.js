// DMZ chart.js — pure-SVG trading chart. Candles from per-block closes (exact at block resolution): wicks, green/coral
// bodies, doji for empty intervals, MA20, buy/sell volume split, nice price ticks, live last-price tag, crosshair with
// price and time tags and an OHLC tooltip, markers for followed trades. Drawn at 2× resolution for crisp edges.
// Layout: one shared frame (padL / padR / top / padB) for the price pane, the volume pane and the axes, so labels,
// grid lines, the last-price tag and the crosshair tags all align; tick counts follow the layout width.
(function () {
  const V = window.Veil, S = V.stats, U = V.ui, { h } = U;
  const NS = "http://www.w3.org/2000/svg";
  const svg = (tag, a, ...kids) => { const e = document.createElementNS(NS, tag); for (const [k, v] of Object.entries(a || {})) e.setAttribute(k, v); for (const k of kids) e.append(k); return e; };
  const txt = (x, y, s, a) => { const t = svg("text", Object.assign({ x, y, fill: "var(--fg3)", "font-size": 20, "font-family": "var(--font)" }, a || {})); t.textContent = s; return t; };
  const fmtT = (ts, span) => { const d = new Date(ts * 1000); const hm = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" }); return span > 172800 ? d.toLocaleDateString(undefined, { month: "short", day: "numeric" }) + " " + hm : hm; };
  const UP = "var(--up)", DN = "var(--down)";
  const W = 860, padL = 12, padR = 112, padB = 36, top = 16, TAG = 30;
  // "nice" tick step for the price axis
  function ticks(y0, y1, n) {
    const raw = (y1 - y0) / n, mag = Math.pow(10, Math.floor(Math.log10(raw))), r = raw / mag;
    const step = (r < 1.5 ? 1 : r < 3.5 ? 2 : r < 7.5 ? 5 : 10) * mag; const out = [];
    for (let v = Math.ceil(y0 / step) * step; v <= y1 + 1e-12; v += step) out.push(v);
    return out;
  }
  // right-hand price tag (last price, crosshair): rect + text, both anchored to the axis column
  const tag = (R, y, s, fill, color, extra) => [svg("rect", Object.assign({ x: R + 4, y: y - TAG / 2, width: padR - 8, height: TAG, rx: 7, fill }, extra || {})), txt(R + 10, y + 7, s, Object.assign({ fill: color, "font-weight": 800 }, extra || {}))];
  // time axis labels at tick fractions, first/last anchored inside the frame
  function timeAxis(el, X, x0, x1, H, n) {
    const span = x1 - x0;
    for (let i = 0; i <= n; i++) { const t = x0 + (span * i) / n; el.append(txt(X(t), H - 10, fmtT(t, span), { "text-anchor": i === 0 ? "start" : i === n ? "end" : "middle" })); }
    return span;
  }

  // opts: { mode: "candles"|"area", range, markers: [{ts, side, label}], height, wide }
  function render(root, s, opts = {}) {
    const mode = opts.mode || "candles", H = (opts.height || 250) * 2, volH = 70, nT = opts.wide ? 8 : 4, nP = opts.wide ? 6 : 5;
    const { from, interval } = S.rangeFor(s, opts.range || "24h");
    const cs = S.candles(s, interval, from);
    const el = svg("svg", { viewBox: `0 0 ${W} ${H}`, class: "cx", role: "img", "aria-label": "price chart" });
    const wrap = h("div", { class: "chart" }, el);
    if (cs.length < 2) { el.append(txt(W / 2, H / 2, "Waiting for the first trades", { "text-anchor": "middle", "font-size": 24 })); return wrap; }
    const ys = cs.flatMap((c) => [c.h, c.l]).filter((y) => y > 0); let y0 = Math.min(...ys), y1 = Math.max(...ys);
    if (y1 === y0) { y0 *= 0.995; y1 *= 1.005; } const padY = (y1 - y0) * 0.1; y0 -= padY; y1 += padY;
    const plotW = W - padR - padL, R = padL + plotW, plotH = H - padB - volH - top, volTop = top + plotH + 8, volBase = H - padB - 4, x0 = cs[0].t, x1 = cs[cs.length - 1].t + interval;
    const X = (t) => padL + ((t - x0) / (x1 - x0)) * plotW, Y = (y) => top + (1 - (y - y0) / (y1 - y0)) * plotH;
    const slot = plotW / cs.length, bw = Math.max(3, Math.floor(slot * 0.64)), wick = Math.max(1.5, Math.min(3, slot * 0.14));
    const last = cs[cs.length - 1], upAll = last.c >= cs[0].o, col = upAll ? UP : DN;
    // grid + price ticks (labels share the axis column with the tags)
    for (const v of ticks(y0, y1, nP)) { const yy = Y(v); el.append(svg("line", { x1: padL, y1: yy, x2: R, y2: yy, stroke: "var(--line)", "stroke-width": 1 })); el.append(txt(R + 10, yy + 7, S.fmtPriceSub(v))); }
    // volume pane: buy (green) and sell (coral) stacked, own baseline and separator, label at the left
    const vmax = Math.max(...cs.map((c) => c.v), 1e-9);
    el.append(svg("line", { x1: padL, y1: volTop - 4, x2: R, y2: volTop - 4, stroke: "var(--line)", "stroke-width": 1 }));
    el.append(txt(padL + 2, volTop + 14, "VOL", { "font-size": 15, "font-weight": 700, opacity: .8 }));
    for (const c of cs) { if (!c.v) continue; const xc = X(c.t) + slot / 2, bh = (c.bv / vmax) * (volH - 10), sh = (c.sv / vmax) * (volH - 10); el.append(svg("rect", { x: xc - bw / 2, y: volBase - bh, width: bw, height: bh, fill: UP, opacity: .55 })); el.append(svg("rect", { x: xc - bw / 2, y: volBase - bh - sh, width: bw, height: sh, fill: DN, opacity: .55 })); }
    el.append(svg("line", { x1: padL, y1: volBase, x2: R, y2: volBase, stroke: "var(--line)", "stroke-width": 1 }));
    if (mode === "area") {
      const d = cs.map((c, i) => (i ? "L" : "M") + (X(c.t) + slot / 2).toFixed(1) + " " + Y(c.c).toFixed(1)).join(" ");
      const g = svg("linearGradient", { id: "ga", x1: 0, y1: 0, x2: 0, y2: 1 }, svg("stop", { offset: 0, "stop-color": col, "stop-opacity": .38 }), svg("stop", { offset: 1, "stop-color": col, "stop-opacity": 0 }));
      el.append(svg("defs", {}, g), svg("path", { d: d + ` L${(X(last.t) + slot / 2).toFixed(1)} ${top + plotH} L${(X(cs[0].t) + slot / 2).toFixed(1)} ${top + plotH} Z`, fill: "url(#ga)" }), svg("path", { d, fill: "none", stroke: col, "stroke-width": 3, "stroke-linejoin": "round", "stroke-linecap": "round" }));
    } else {
      for (const c of cs) {
        const xc = X(c.t) + slot / 2, cc = c.up === null ? "var(--fg3)" : c.up ? UP : DN;
        if (c.up !== null) el.append(svg("rect", { x: xc - wick / 2, y: Y(c.h), width: wick, height: Math.max(1, Y(c.l) - Y(c.h)), fill: cc, rx: wick / 2 }));
        const yo = Y(c.o), yc = Y(c.c), bh = Math.max(2, Math.abs(yc - yo));
        el.append(svg("rect", { x: xc - bw / 2, y: Math.min(yo, yc), width: bw, height: c.up === null ? 2 : bh, fill: cc, rx: 1.5, opacity: c.up === null ? .45 : 1 }));
      }
      // MA20 of closes
      if (cs.length >= 20) { let sum = 0; const pts = []; for (let i = 0; i < cs.length; i++) { sum += cs[i].c; if (i >= 20) sum -= cs[i - 20].c; if (i >= 19) pts.push((pts.length ? "L" : "M") + (X(cs[i].t) + slot / 2).toFixed(1) + " " + Y(sum / 20).toFixed(1)); } el.append(svg("path", { d: pts.join(" "), fill: "none", stroke: "var(--acc)", "stroke-width": 2, opacity: .85, "stroke-linejoin": "round" })); el.append(txt(padL + 2, top + 18, "MA20", { fill: "var(--acc)", "font-weight": 700, "font-size": 15 })); }
    }
    // last price: dashed level, axis tag, live dot (the in-progress candle is fed from live reserves)
    const ly = Y(last.c);
    el.append(svg("line", { x1: padL, y1: ly, x2: R, y2: ly, stroke: col, "stroke-width": 1.5, "stroke-dasharray": "5 5", opacity: .9 }));
    const lastTag = svg("g", {}, ...tag(R, ly, S.fmtPriceSub(last.c), col, "#06120b")); el.append(lastTag);
    el.append(svg("circle", { cx: X(last.t) + slot / 2, cy: ly, r: 6, fill: col, class: "live-dot" }));
    // markers
    for (const m of opts.markers || []) { if (m.ts < x0 || m.ts > x1) continue; const c = cs.find((k) => m.ts >= k.t && m.ts < k.t + interval) || last; const cx = X(c.t) + slot / 2, cy = Y(m.side === 1 ? c.l : c.h) + (m.side === 1 ? 20 : -20); const g = svg("g", {}, svg("path", { d: m.side === 1 ? `M${cx} ${cy - 9} l9 12 h-18 z` : `M${cx} ${cy + 9} l9 -12 h-18 z`, fill: m.side === 1 ? UP : DN, stroke: "var(--bg)", "stroke-width": 2 })); const t = svg("title"); t.textContent = (m.side === 1 ? "buy" : "sell") + " · " + (m.label || ""); g.append(t); el.append(g); }
    // time axis
    const span = timeAxis(el, X, x0, x1, H, nT);
    // crosshair: vertical + horizontal guides, a price tag in the axis column, a time tag under the plot
    const cross = svg("g", { visibility: "hidden" },
      svg("line", { x1: 0, y1: top, x2: 0, y2: volBase, stroke: "var(--fg2)", "stroke-width": 1.5, "stroke-dasharray": "4 4" }),
      svg("line", { x1: padL, y1: 0, x2: R, y2: 0, stroke: "var(--fg2)", "stroke-width": 1.5, "stroke-dasharray": "4 4" }),
      ...tag(R, 0, "", "var(--fg)", "var(--bg)"),
      svg("rect", { x: 0, y: H - padB + 2, width: 150, height: 28, rx: 7, fill: "var(--fg)" }), txt(0, H - padB + 22, "", { fill: "var(--bg)", "font-weight": 700, "text-anchor": "middle" }));
    el.append(cross);
    const tip = h("div", { class: "ctip", hidden: true }); wrap.append(tip);
    const onMove = (ev) => {
      const r = el.getBoundingClientRect(); const px = ((ev.clientX - r.left) / r.width) * W, py = ((ev.clientY - r.top) / r.height) * H;
      if (px < padL || px > R || py < top || py > volBase) return onLeave();
      const i = Math.min(cs.length - 1, Math.max(0, Math.floor(((px - padL) / plotW) * cs.length))); const c = cs[i]; const xc = X(c.t) + slot / 2;
      const yp = Math.min(top + plotH, py), pv = y1 - ((yp - top) / plotH) * (y1 - y0);
      cross.setAttribute("visibility", "visible"); const [vl, hl, pr, pt, tr, tt] = cross.children;
      vl.setAttribute("x1", xc); vl.setAttribute("x2", xc); hl.setAttribute("y1", yp); hl.setAttribute("y2", yp);
      pr.setAttribute("y", yp - TAG / 2); pt.setAttribute("y", yp + 7); pt.textContent = S.fmtPriceSub(pv);
      lastTag.setAttribute("visibility", Math.abs(yp - ly) < TAG + 2 ? "hidden" : "visible"); // never two tags on top of each other
      const tx = Math.max(padL, Math.min(R - 150, xc - 75)); tr.setAttribute("x", tx); tt.setAttribute("x", tx + 75); tt.textContent = fmtT(c.t, span);
      const chg = c.o > 0 ? ((c.c - c.o) / c.o) * 100 : 0;
      tip.hidden = false; tip.innerHTML = `<b>${fmtT(c.t, span)} · ${c.n} blk</b><span>O ${S.fmtPriceSub(c.o)}</span><span>H ${S.fmtPriceSub(c.h)}</span><span>L ${S.fmtPriceSub(c.l)}</span><span>C ${S.fmtPriceSub(c.c)} <i class="${chg >= 0 ? "up" : "down"}">${chg >= 0 ? "+" : ""}${chg.toFixed(2)}%</i></span><span><i class="up">buy ${S.fmtDeroShort(c.bv)}</i> · <i class="down">sell ${S.fmtDeroShort(c.sv)}</i> DERO</span>`;
      tip.style.left = (xc / W < 0.5 ? (xc / W) * 100 + 3 : (xc / W) * 100 - 44) + "%";
    };
    const onLeave = () => { cross.setAttribute("visibility", "hidden"); lastTag.setAttribute("visibility", "visible"); tip.hidden = true; };
    el.addEventListener("pointermove", onMove); el.addEventListener("pointerdown", onMove); el.addEventListener("pointerleave", onLeave);
    wrap.dataset.candles = cs.length; wrap.dataset.interval = interval;
    return wrap;
  }

  // generic area series for the portfolio: [{ts, v}] in DERO — same frame and axis column as the trading chart
  function series(root, pts, opt = {}) {
    const H = (opt.height || 160) * 2, nP = 3;
    const el = svg("svg", { viewBox: `0 0 ${W} ${H}`, class: "cx" }); const wrap = h("div", { class: "chart", style: `height:${opt.height || 160}px` }, el);
    if (pts.length < 2) { el.append(txt(W / 2, H / 2, opt.empty || "Not enough history yet", { "text-anchor": "middle", "font-size": 24 })); return wrap; }
    const x0 = pts[0].ts, x1 = pts[pts.length - 1].ts, ys = pts.map((p) => p.v); let y0 = Math.min(...ys), y1 = Math.max(...ys); if (y1 === y0) { y0 *= .99; y1 *= 1.01; }
    const plotW = W - padR - padL, R = padL + plotW, plotH = H - padB - top;
    const X = (t) => padL + ((t - x0) / (x1 - x0 || 1)) * plotW, Y = (v) => top + (1 - (v - y0) / (y1 - y0)) * plotH;
    const up = ys[ys.length - 1] >= ys[0], col = up ? UP : DN;
    for (const v of ticks(y0, y1, nP)) { const yy = Y(v); el.append(svg("line", { x1: padL, y1: yy, x2: R, y2: yy, stroke: "var(--line)" }), txt(R + 10, yy + 7, S.fmtDeroShort(v))); }
    const d = pts.map((p, i) => (i ? "L" : "M") + X(p.ts).toFixed(1) + " " + Y(p.v).toFixed(1)).join(" ");
    const g = svg("linearGradient", { id: "gs", x1: 0, y1: 0, x2: 0, y2: 1 }, svg("stop", { offset: 0, "stop-color": col, "stop-opacity": .38 }), svg("stop", { offset: 1, "stop-color": col, "stop-opacity": 0 }));
    el.append(svg("defs", {}, g), svg("path", { d: d + ` L${X(x1).toFixed(1)} ${top + plotH} L${X(x0).toFixed(1)} ${top + plotH} Z`, fill: "url(#gs)" }), svg("path", { d, fill: "none", stroke: col, "stroke-width": 3, "stroke-linejoin": "round", "stroke-linecap": "round" }));
    const lv = ys[ys.length - 1], ly = Y(lv);
    el.append(svg("line", { x1: padL, y1: ly, x2: R, y2: ly, stroke: col, "stroke-width": 1.5, "stroke-dasharray": "5 5", opacity: .9 }), ...tag(R, ly, S.fmtDeroShort(lv), col, "#06120b"), svg("circle", { cx: X(x1), cy: ly, r: 6, fill: col, class: "live-dot" }));
    timeAxis(el, X, x0, x1, H, opt.wide ? 6 : 3);
    return wrap;
  }
  V.chart = { render, series };
})();
