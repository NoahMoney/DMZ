// The market page's arithmetic (app/stats.js) and its chart (app/chart.js), without a browser: the price formatter
// at the extremes, which candle intervals a history can fill and which one is chosen, the candles themselves, and
// the SVG the chart draws for a flat market, a single point, sparse trades, a huge swing and an empty history.
// chart.js is run against a small fake DOM that records attributes and text, so a NaN anywhere in the drawing
// is a failed assertion rather than a glitch nobody screenshots.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, A } from "./harness.mjs";

class Text { constructor(s) { this.nodeType = 3; this.s = s; } get textContent() { return this.s; } }
class El {
  constructor(tag) { this.nodeType = 1; this.tag = tag; this.attrs = {}; this.children = []; this.style = {}; this.dataset = {}; this.on = {}; this.hidden = false; this.html = ""; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return this.attrs[k]; }
  append(...k) { for (const c of k) this.children.push(typeof c === "string" ? new Text(c) : c); }
  addEventListener(n, f) { (this.on[n] = this.on[n] || []).push(f); }
  set textContent(v) { this.children = [new Text(String(v))]; }
  get textContent() { return this.children.map((c) => c.textContent).join(""); }
  set innerHTML(v) { this.html = String(v); }
  get innerHTML() { return this.html; }
  getBoundingClientRect() { return { left: 0, top: 0, width: 430, height: 250 }; }
}
const h = (tag, attrs, ...kids) => {
  const e = new El(tag);
  for (const [k, v] of Object.entries(attrs || {})) { if (v === null || v === undefined || v === false) continue; if (k.startsWith("on")) e.addEventListener(k.slice(2), v); else if (k === "hidden") e.hidden = true; else e.setAttribute(k, v === true ? "" : v); }
  for (const kid of kids.flat(Infinity)) if (kid !== null && kid !== undefined && kid !== false) e.append(kid.nodeType ? kid : new Text(String(kid)));
  return e;
};
const document = { createElementNS: (ns, tag) => new El(tag) };
const window = { Veil: { amm: A, ui: { h } } };
for (const f of ["stats.js", "chart.js"]) new Function("window", "document", readFileSync(join(ROOT, "app", f), "utf8"))(window, document);
const S = window.Veil.stats, C = window.Veil.chart;
// market.js reads the rest of ui.js and two of feed/names' functions at load; stubs that record what it asks for
const VAULTS = [{ scid: "a".repeat(64), sym: "XMR", name: "Monero" }, { scid: "b".repeat(64), sym: "ZEC", name: "Zcash" }], went = [];
Object.assign(window.Veil.ui, { render: (el) => el, avatar: () => h("div"), kv: (k, v) => h("div", null, k, v), icon: () => h("i"), clean: (s, n) => String(s || "").slice(0, n), safeUrl: (u) => (/^https:\/\//.test(u) ? u : ""), button: (l, cls, onclick) => h("button", { class: cls, onclick }, l), router: { handlers: {}, go: (r) => went.push(r) } });
window.Veil.data = { qsym: (s) => (s && s.quoteSym) || "DERO", allVaults: () => VAULTS };
new Function("window", "document", readFileSync(join(ROOT, "app", "market.js"), "utf8"))(window, document);
const M = window.Veil.market;
const find = (n, tag) => (n && n.tag === tag ? n : ((n && n.children) || []).map((c) => find(c, tag)).find(Boolean));

// a pool whose closes follow prices[i], one every `step` seconds, the last at `end`. Price = rd / ra, so the
// reserve of the quote stays at 1e20 units and the asset reserve carries the price.
const RD = 10n ** 20n;
const mk = (prices, step, end, extra) => {
  const points = prices.map((p, i) => ({ height: i + 1, ts: end - (prices.length - 1 - i) * step, rd: RD, ra: p > 0 ? BigInt(Math.round(1e20 / p)) : 0n }));
  const last = points[points.length - 1] || { rd: RD, ra: 0n };
  return Object.assign({ kind: "pool", dec: 5n, quoteDec: 5, points, trades: [], rd: last.rd, ra: last.ra, price: prices.length ? prices[prices.length - 1] : 0, pc: BigInt(points.length) }, extra || {});
};
const AT = 1_800_000_000;
const flat = (n, step, p = 0.5) => Array.from({ length: n }, () => p);

test("fmtPriceSub: subscript zeros below a ten-thousandth, suffixes from a million, a dash for anything not a price", () => {
  assert.equal(S.fmtPriceSub(0.00000123), "0.0₅123");
  assert.equal(S.fmtPriceSub(0.000000456), "0.0₆456");
  assert.equal(S.fmtPriceSub(0.00001), "0.0₄1");
  assert.equal(S.fmtPriceSub(1.5e-20), "0.0₁₉15");
  assert.match(S.fmtPriceSub(5e-324), /^0\.0₃₂₃\d+$/);
  assert.equal(S.fmtPriceSub(0.000999999), "0.001"); // rounding carries into the exponent, not into a fifth digit
  assert.equal(S.fmtPriceSub(4.5e6), "4.5M");
  assert.equal(S.fmtPriceSub(1.23e9), "1.23B");
  assert.equal(S.fmtPriceSub(7e12), "7T");
  assert.equal(S.fmtPriceSub(2.5e15), "2.5Q");
  assert.equal(S.fmtPriceSub(1e21), "1.00e21");
  assert.equal(S.fmtPriceSub(Number.MAX_VALUE), "1.80e308");
  for (const bad of [0, -1, -1e-9, NaN, Infinity, -Infinity, null, undefined, "1", 1n]) assert.equal(S.fmtPriceSub(bad), "—", String(bad));
});

test("fmtPriceSub: every positive double from 1e-323 to 1e308 prints as a number, never NaN, Infinity or exponent-minus", () => {
  for (let e = -323; e <= 308; e++) for (const m of [1, 1.2345, 9.9999]) {
    const v = m * 10 ** e; if (!(v > 0) || !isFinite(v)) continue;
    const out = S.fmtPriceSub(v);
    assert.ok(out && out !== "—", "empty for " + v);
    assert.doesNotMatch(out, /NaN|Infinity|undefined|e-/, v + " → " + out);
    if (v < 9.99e-5) assert.match(out, /^0\.0[₀-₉]+\d+$/, v + " → " + out); // 0.0000999999 rounds up to 0.0001, as it should
  }
});

test("fmtDeroShort: suffixes past a billion, a dash for a non-number, the old scale untouched in between", () => {
  assert.equal(S.fmtDeroShort(2.5e9), "2.5B");
  assert.equal(S.fmtDeroShort(1.5e6), "1.50M");
  assert.equal(S.fmtDeroShort(12.345), "12.35");
  assert.equal(S.fmtDeroShort(0), "0");
  assert.equal(S.fmtDeroShort(NaN), "—");
  assert.equal(S.fmtDeroShort(Infinity), "—");
});

test("intervals: only those the loaded history spans four candles of, finest 1m, and the default chosen from them", () => {
  const young = mk(flat(34, 18), 18, AT - 12); // ~10 minutes old
  assert.deepEqual(S.intervals(young, AT), ["1m"]);
  assert.equal(S.pickInterval(young, null, AT), "1m");
  const busy = mk(flat(4000, 18), 18, AT); // 4000 active blocks: about 20 hours
  assert.deepEqual(S.intervals(busy, AT), ["1m", "5m", "15m", "1h", "4h"]);
  assert.equal(S.pickInterval(busy, null, AT), "15m", "the coarsest that still fills 30 candles");
  assert.equal(S.pickInterval(busy, "4h", AT), "4h", "a choice the history can fill is kept");
  assert.equal(S.pickInterval(busy, "1W", AT), "15m", "a choice it cannot is replaced");
  assert.equal(S.pickInterval(busy, "all", AT), "all");
  const old = mk(flat(240, 21600), 21600, AT); // one close every 6 hours for 60 days
  assert.deepEqual(S.intervals(old, AT), ["1m", "5m", "15m", "1h", "4h", "1D", "1W"]);
  assert.equal(S.pickInterval(old, null, AT), "1D");
  const one = mk([0.5], 18, AT);
  assert.deepEqual(S.intervals(one, AT), []);
  assert.equal(S.pickInterval(one, "15m", AT), "all");
  assert.equal(S.pickInterval(mk([], 18, AT), null, AT), "all");
  assert.equal(S.history(mk(flat(4000, 18), 18, AT, { pc: 10000n }), AT).truncated, true);
  assert.equal(S.history(busy, AT).truncated, false);
});

test("windowFor: the newest maxC candles of an interval; ALL at the finest interval that fits maxC across the history", () => {
  const busy = mk(flat(4000, 18), 18, AT), first = busy.points[0].ts;
  assert.deepEqual(S.windowFor(busy, "15m", 90, AT), { key: "15m", interval: 900, from: first });
  assert.deepEqual(S.windowFor(busy, "1m", 90, AT), { key: "1m", interval: 60, from: AT - 90 * 60 });
  assert.equal(S.windowFor(busy, "all", 90, AT).interval, 900);
  const decade = mk([1, 2], 10 * 365 * 86400, AT);
  const w = S.windowFor(decade, "all", 90, AT);
  assert.equal(w.interval % 604800, 0);
  assert.ok((AT - w.from) / w.interval <= 90);
});

test("candles: flat, sparse, a huge swing, a zero-reserve start, the left edge, and a bound on how many", () => {
  const f = S.candles(mk(flat(400, 18), 18, AT), 900, AT - 7200, AT);
  assert.ok(f.length >= 8 && f.length <= 9);
  for (const c of f) assert.deepEqual([c.o, c.h, c.l, c.c].map((x) => Math.round(x * 1e9) / 1e9), [0.5, 0.5, 0.5, 0.5]);
  // two closes three days apart: the hours between carry the first close as dojis, no candle is invented before it
  const sp = mk([0.25, 0.75], 3 * 86400, AT), sc = S.candles(sp, 3600, sp.points[0].ts, AT);
  assert.ok(sc.length >= 72 && sc.length <= 74, String(sc.length));
  assert.ok(Math.abs(sc[1].c - 0.25) < 1e-12 && sc[1].up === null);
  assert.ok(Math.abs(sc[sc.length - 1].c - 0.75) < 1e-12);
  // nine orders of magnitude
  const sw = mk(Array.from({ length: 200 }, (_, i) => 10 ** (-9 + (12 * i) / 199)), 60, AT);
  for (const c of S.candles(sw, 300, sw.points[0].ts, AT)) for (const x of [c.o, c.h, c.l, c.c]) assert.ok(isFinite(x) && x > 0, String(x));
  // a pool before its first deposit reads as price 0: no candle is drawn at 0
  const z = mk([0, 0, 0, 0.3, 0.4], 600, AT);
  const zc = S.candles(z, 600, z.points[0].ts, AT);
  assert.ok(zc.length && zc.every((c) => c.c > 0 && c.o > 0 && c.l > 0));
  // the window opens on the last close before it, not on the first price inside it
  const e = mk([0.1, 0.1, 0.9], 3600, AT), ec = S.candles(e, 600, AT - 2 * 3600 + 1200, AT);
  assert.ok(Math.abs(ec[0].o - 0.1) < 1e-12, String(ec[0].o));
  assert.ok(S.candles(mk([1, 2], 10 * 365 * 86400, AT), 60, 0, AT).length <= 2001);
  assert.deepEqual(S.candles(mk([], 18, AT), 60, 0, AT), []);
});

// every attribute and every string in a drawing, so a NaN in a path or a label cannot hide
const walk = (n, out = []) => { if (!n) return out; if (n.nodeType === 3) { out.push(n.s); return out; } out.push(...Object.values(n.attrs), n.html); for (const c of n.children) walk(c, out); return out; };
const sane = (w, what) => { const all = walk(w).join(" "); assert.doesNotMatch(all, /NaN|Infinity|undefined/, what + ": " + (all.match(/.{0,60}(NaN|Infinity|undefined).{0,20}/) || [""])[0]); return all; };
const NOW = () => Math.floor(Date.now() / 1000);

test("chart: flat, one point, sparse, a huge swing, tiny prices, empty and loading all draw without NaN", () => {
  const now = NOW();
  const cases = {
    flat: mk(flat(300, 18, 0.004), 18, now),
    sparse: mk([0.3, 0.3, 0.9, 0.2], 2 * 86400, now),
    swing: mk(Array.from({ length: 300 }, (_, i) => 10 ** (-6 + (9 * i) / 299)), 60, now),
    tiny: mk(Array.from({ length: 300 }, (_, i) => 1.2e-13 * (1 + 0.3 * Math.sin(i / 9))), 18, now),
    huge: mk(Array.from({ length: 300 }, (_, i) => 3e12 * (1 + 0.1 * Math.cos(i / 7))), 18, now),
  };
  for (const [name, s] of Object.entries(cases)) for (const width of [330, 700, 1200]) for (const mode of ["candles", "area"]) {
    const w = C.render(null, s, { width, mode, quote: "DERO" });
    const all = sane(w, name + " " + width + " " + mode);
    assert.ok(Number(w.dataset.candles) >= 2, name + " drew " + w.dataset.candles + " candles");
    assert.equal(w.children[0].attrs.viewBox, `0 0 ${width * 2} ${w.children[0].attrs.viewBox.split(" ")[3]}`);
    if (name === "swing") assert.match(all, /LOG SCALE/, "a 1e9 swing is drawn on a log axis and says so");
    if (name === "flat") assert.doesNotMatch(all, /LOG SCALE/);
    if (name === "tiny") assert.match(all, /0\.0₁₂/);
    if (name === "huge") assert.match(all, /\dT\b/);
  }
  const one = C.render(null, mk([0.5], 18, now), { width: 400 });
  assert.equal(one.dataset.state, "empty"); assert.match(sane(one, "one point"), /One price point/);
  const none = C.render(null, mk([], 18, now), { width: 400 });
  assert.equal(none.dataset.state, "empty"); assert.match(sane(none, "empty"), /No price history/);
  const busy = C.render(null, null, { loading: true, width: 400 });
  assert.equal(busy.dataset.state, "loading"); assert.match(sane(busy, "loading"), /Loading price history/);
});

test("market.js: the pair through U.pair, a wrapped side marked 1:1, a deposit link only for an empty wrapped quote", () => {
  const xq = { ticker: "BRW", quote: VAULTS[0].scid, quoteSym: "wXMR", pair: "BRW / wXMR" }, dero = { ticker: "BRW" }, wb = { ticker: "wZEC", asset: VAULTS[1].scid, wrappedBase: true };
  assert.equal(M.pairOf(dero), "BRW / DERO");
  assert.equal(M.pairOf(xq), "BRW / wXMR");
  window.Veil.ui.pair = (s) => "XMR / " + s.ticker;
  try { assert.equal(M.pairOf(xq), "XMR / BRW", "U.pair names the pair wherever ui.js provides it"); } finally { delete window.Veil.ui.pair; }
  const m = M.wrapMark(xq);
  assert.equal(m.textContent, "wrapped · 1:1");
  assert.match(m.attrs.title, /XMR held by its k-of-n federation/);
  assert.match(M.wrapMark(wb).attrs.title, /ZEC/, "a wrapped base is marked too");
  assert.equal(M.wrapMark(dero), null);
  const d = M.deposit(xq, 0n), btn = find(d, "button");
  assert.equal(btn.textContent, "Deposit XMR");
  btn.on.click.forEach((f) => f()); assert.deepEqual(went.splice(0), ["wrap"], "the Wrap screen until the move hub exists");
  window.Veil.ui.router.handlers.move = () => {};
  try { btn.on.click.forEach((f) => f()); assert.deepEqual(went.splice(0), ["move"]); } finally { delete window.Veil.ui.router.handlers.move; }
  for (const b of [5n, null, undefined]) assert.equal(M.deposit(xq, b), null, "no link while the wallet holds some, or its balance is unknown");
  assert.equal(M.deposit(dero, 0n), null);
  assert.equal(M.deposit(wb, 0n), null, "a wrapped base is not a quote to deposit");
});

test("the swaps are gone from the market page: no offers section, no V.zswapOffers, no route to the swap board", () => {
  for (const f of ["market.js", "token.js"]) assert.doesNotMatch(readFileSync(join(ROOT, "app", f), "utf8"), /zswapOffers|swap board|router\.go\("(zswap|xmr)"/, f);
});

test("chart: the crosshair works by keyboard and reads each candle out through a live region", () => {
  const s = mk(Array.from({ length: 200 }, (_, i) => 0.5 + 0.01 * Math.sin(i)), 18, NOW());
  const w = C.render(null, s, { width: 700, iv: "1m", quote: "DERO" });
  assert.equal(w.attrs.tabindex, "0");
  const live = w.children.find((c) => c.attrs && c.attrs["aria-live"] === "polite");
  assert.ok(live, "no live region");
  const key = (k) => { let prevented = false; for (const f of w.on.keydown) f({ key: k, preventDefault: () => { prevented = true; } }); return prevented; };
  assert.ok(key("End"));
  assert.match(live.textContent, new RegExp("candle " + w.dataset.candles + " of " + w.dataset.candles + ": open [\\d.]+, high [\\d.]+, low [\\d.]+, close [\\d.]+ DERO"));
  key("ArrowLeft"); assert.match(live.textContent, new RegExp("candle " + (w.dataset.candles - 1) + " of "));
  key("Home"); key("ArrowLeft"); assert.match(live.textContent, /candle 1 of /, "Home, then left, stays on the first");
  const cross = w.children[0].children.find((c) => c.tag === "g" && c.children[0] && c.children[0].tag === "line"); // its guides come first
  assert.equal(cross.attrs.visibility, "visible");
  key("Escape"); assert.equal(cross.attrs.visibility, "hidden");
  assert.equal(key("a"), false, "other keys are left alone");
});
