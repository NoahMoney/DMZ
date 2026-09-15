// The screens' declarations to the data layer (D.see, docs/PERFORMANCE.md §interface). Each surface passes exactly
// the markets it has drawn on screen, one key per surface. The set follows scrolling, paging and navigation, a sheet
// adds to the screen's set and never stands alone, and every value shown from an older read carries D.ageTag.
// The scripts load as the page loads them, over a small DOM and a stand-in IntersectionObserver that the test
// drives. The real data layer (feed.js, live.js, names.js) reads a small chain from a stand-in node.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { ROOT } from "./harness.mjs";

// ---- a small DOM: what ui.js's h/render, the router's screens and the sheet use ----
class Text { constructor(s) { this.nodeType = 3; this.data = String(s); this.parentNode = null; } get textContent() { return this.data; } }
const camel = (k) => k.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
class El {
  constructor(tag) {
    Object.assign(this, { nodeType: 1, tag: String(tag).toLowerCase(), attrs: {}, childNodes: [], parentNode: null, dataset: {}, on: {}, hidden: false, className: "", value: "", style: { setProperty() {} } });
    const cl = () => this.className.split(/\s+/).filter(Boolean);
    this.classList = { contains: (c) => cl().includes(c), add: (...c) => { this.className = [...new Set(cl().concat(c))].join(" "); }, remove: (...c) => { this.className = cl().filter((x) => !c.includes(x)).join(" "); }, toggle: (c, on) => (on ?? !cl().includes(c) ? this.classList.add(c) : this.classList.remove(c)) };
  }
  setAttribute(k, v) { v = String(v); this.attrs[k] = v; if (k === "hidden") this.hidden = true; if (k === "id") this.id = v; if (k.startsWith("data-")) this.dataset[camel(k.slice(5))] = v; }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  append(...ns) { for (let n of ns) { if (typeof n === "string") n = new Text(n); if (n.parentNode) n.parentNode.removeChild(n); n.parentNode = this; this.childNodes.push(n); } }
  removeChild(n) { const i = this.childNodes.indexOf(n); if (i >= 0) this.childNodes.splice(i, 1); n.parentNode = null; return n; }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  get firstChild() { return this.childNodes[0] || null; }
  get children() { return this.childNodes.filter((n) => n.nodeType === 1); }
  get isConnected() { let n = this; while (n.parentNode) n = n.parentNode; return !!n.root; }
  contains(n) { for (; n; n = n.parentNode) if (n === this) return true; return false; }
  get textContent() { return this.childNodes.map((n) => n.textContent).join(""); }
  set textContent(v) { for (const n of this.childNodes) n.parentNode = null; this.childNodes = []; if (v !== "" && v != null) this.append(String(v)); }
  set innerHTML(v) { this.textContent = ""; }
  get innerHTML() { return ""; }
  addEventListener(t, f) { (this.on[t] = this.on[t] || []).push(f); }
  click() { for (const f of this.on.click || []) f({ currentTarget: this, target: this, stopPropagation() {}, preventDefault() {} }); }
  focus() {}
  getBoundingClientRect() { return { left: 0, top: 0, right: 400, bottom: 250, width: 400, height: 250 }; }
  get clientWidth() { return 0; }
  get offsetWidth() { return 0; }
  querySelectorAll(sel) { const m = matcher(sel), out = []; const walk = (n) => { for (const c of n.children) { if (m(c)) out.push(c); walk(c); } }; walk(this); return out; }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
}
// simple selectors, comma separated: tag, .class, #id, [attr], [attr="value"] and compounds of them; anything
// else matches nothing
function matcher(sel) {
  const alts = sel.split(",").map((s) => {
    const m = /^([a-z0-9]*)((?:[.#][\w-]+)*)((?:\[[\w-]+(?:="[^"]*")?\])*)$/i.exec(s.trim()); if (!m) return () => false;
    const tag = m[1].toLowerCase(), parts = m[2].match(/[.#][\w-]+/g) || [], attrs = [...m[3].matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g)];
    return (e) => (!tag || e.tag === tag) && parts.every((p) => (p[0] === "." ? e.classList.contains(p.slice(1)) : e.id === p.slice(1))) && attrs.every(([, k, v]) => (v === undefined ? k in e.attrs : e.attrs[k] === v));
  });
  return (e) => alts.some((f) => f(e));
}
const body = new El("body"); body.root = true;
const document = { createElement: (t) => new El(t), createElementNS: (ns, t) => new El(t), createTextNode: (s) => new Text(s), body, documentElement: new El("html"), hidden: false,
  addEventListener() {}, querySelector: (s) => body.querySelector(s), querySelectorAll: (s) => body.querySelectorAll(s) };
for (const id of ["screen", "sheet", "sheetBackdrop", "tape", "toasts"]) { const e = new El("div"); e.setAttribute("id", id); e.hidden = id.startsWith("sheet"); body.append(e); }

// the stand-in IntersectionObserver: the test says which targets are on screen
const eyes = [];
class IO {
  constructor(cb, o) { Object.assign(this, { cb, o: o || {}, els: new Set() }); eyes.push(this); }
  observe(e) { this.els.add(e); } unobserve(e) { this.els.delete(e); } disconnect() { this.els.clear(); }
  fire(pairs) { this.cb(pairs.map(([target, isIntersecting]) => ({ target, isIntersecting }))); }
}
const store = new Map();
const localStorage = { get length() { return store.size; }, key: (i) => [...store.keys()][i] ?? null, getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => void store.set(k, String(v)), removeItem: (k) => void store.delete(k) };
const window = { Veil: {}, addEventListener() {}, matchMedia: () => ({ matches: false }), scrollTo() {} };
const run = (f) => new Function("window", "document", "localStorage", "location", "IntersectionObserver", "requestAnimationFrame", "setInterval", readFileSync(join(ROOT, "app", f), "utf8"))(window, document, localStorage, { hash: "", href: "http://127.0.0.1/index.html" }, IO, () => 0, () => 0);
const V = window.Veil;
for (const f of ["config.js", "rpc.js", "amm.js", "stats.js", "ui.js", "live.js", "chart.js", "store.js", "feed.js", "names.js", "sheets.js"]) run(f);
Object.assign(V, { app: { settings: {}, holdings: null, saveSettings() {}, refresh() {}, connectWallet() {} }, wallet: { connected: false, canon: null } });
for (const f of ["home.js", "markets.js", "discover.js", "search.js", "market.js", "token.js"]) run(f);
const D = V.data, U = V.ui, R = U.router, T = V.token, L = V.live;
L.tape = () => {}; // the tape's own drawing is live.js's; what it shows is declared by the screens that draw it

// ---- a chain of launches with different amounts of recent trading, read by the real data layer ----
const sha = (s) => createHash("sha256").update(s).digest("hex");
const hex = (s) => Buffer.from(s, "utf8").toString("hex");
const TOPO = 1000, N = 150;
{
  const raw = "02" + sha("treasury"), code = "Function Initialize() Uint64\n10 RETURN 0\nEnd Function", F = sha("factory"), trader = V.rawToAddress("03" + sha("trader"));
  const st = { [F]: { pc: "0", lc: String(N), nc: "0", regfee: "1", tstr: hex(V.rawToAddress(raw)), v: "2" } };
  for (let i = 0; i < N; i++) {
    const id = sha("m" + i); st[F]["launch_" + i] = hex(id);
    const s = (st[id] = { fee: "50", pfee: "5000", treasury: raw, nameHdr: hex("Coin " + i), ticker: hex("C" + i), dec: "5", S: "1000000000", phase: "1", rd: "1000000", ra: "2000000", lp: "1", tc: "0", pc: "0", lh: "0", lt: "0" });
    for (let k = 0; k <= i % 9; k++) {
      const pc = +s.pc, ts = Math.floor(Date.now() / 1000) - 3000 + pc * 60;
      s.rd = String(+s.rd + 1000 * (1 + (i % 5))); s["p_" + pc] = hex([TOPO, ts, s.rd, s.ra].join("|")); s["t_" + s.tc] = hex(["1", trader, "1000", "10", TOPO, ts].join("|"));
      s.pc = String(pc + 1); s.tc = String(+s.tc + 1); s.lh = String(TOPO); s.lt = String(ts);
    }
  }
  V.node = {
    call: async (m) => { if (m === "DERO.GetHeight") return { height: TOPO, topoheight: TOPO }; throw new Error("no " + m); },
    getTx: async () => ({ block_height: 900 }),
    getSC: async (scid, o = {}) => { const s = st[scid] || {}, vals = {}; for (const k of o.keys || []) if (s[k] !== undefined) vals[k] = s[k]; return { vals, vars: {}, strs: {}, balances: {}, code: o.code && scid !== F ? code : "" }; },
  };
  L.height = TOPO; L.topo = TOPO;
  Object.assign(D.cfg, { factory: F, treasuries: [V.rawToAddress(raw)], vaults: [], vault: "", feeBps: 50, launchPfeeBps: 5000, pfeeBps: 5000, templates: { launch: { sha256: sha(code), legacy: [] } } });
  D.hashes = {}; D.loadFounders = async () => null;
}
await D.refresh(true);

// every D.see the screens make, so a redraw that re-sends an unchanged set is caught
const sent = [];
{ const see = D.see; D.see = function (ids, key) { sent.push(key); return see.call(this, ids, key); }; }
const screen = document.querySelector("#screen");
// a navigation as router.dispatch makes it, without the tab bar; a refresh a declaration scheduled is not wanted here
const go = (name, param = "") => { R.gen++; R.current = { name, param }; const r = (R.handlers[name] || (() => null))(screen, param); clearTimeout(D._seeT); return r; };
const by = () => { const x = D.seen; return x && x.gen === R.gen ? Object.fromEntries(Object.entries(x.by).map(([k, v]) => [k, [...v].sort()])) : null; };
const ids = (els) => [...new Set(els.map((e) => e.dataset.scid))].sort();
const sheet = document.querySelector("#sheet");

test("setup: the data layer read every market", () => {
  assert.equal(D.assets.size, N);
  assert.ok(D.lastRefresh, "and the registry is read");
});

let eye1, rows1, stale;
test("Markets declares every drawn row until the observer reports, then exactly the rows on screen, as it scrolls and pages", () => {
  go("markets");
  const list = screen.querySelector(".mlist"), rows = (rows1 = list.children);
  assert.equal(rows.length, 60, "a first page of rows");
  assert.deepEqual(by().markets, ids(rows), "before the observer has reported: every drawn row");
  assert.deepEqual(by().tape, ids(D.trending(14).map((s) => ({ dataset: { scid: s.scid } }))), "the tape Markets draws is declared too");
  const eye = (eye1 = eyes.findLast((e) => [...e.els].some((x) => x.classList.contains("mkt"))));
  assert.equal(eye.els.size, 60, "the observer watches every drawn row");
  eye.fire(rows.map((e, i) => [e, i < 14]));
  assert.deepEqual(by().markets, ids(rows.slice(0, 14)), "then the rows on screen, exactly");
  eye.fire([...rows.slice(0, 5).map((e) => [e, false]), ...rows.slice(14, 19).map((e) => [e, true])]);
  assert.deepEqual(by().markets, ids(rows.slice(5, 19)), "scrolling down five rows moves the set with it");
  const n = sent.filter((k) => k === "markets").length;
  D.changed();
  assert.equal(sent.filter((k) => k === "markets").length, n, "a redraw with the same rows on screen does not declare again");
  // the foot of the list comes near the screen: the next pages are drawn
  const pager = eyes.findLast((e) => [...e.els].some((x) => x.classList.contains("mmore")));
  pager.fire([[[...pager.els][0], true]]);
  const all = screen.querySelector(".mlist").children;
  assert.equal(all.length, N, "every market drawn after paging");
  assert.equal(eye.els.size, N, "and every drawn row watched");
  assert.deepEqual(by().markets, ids(rows.slice(5, 19)), "drawing more rows below does not declare them while they are off screen");
  eye.fire([...all.slice(5, 19).map((e) => [e, false]), ...all.slice(130, 150).map((e) => [e, true])]);
  assert.deepEqual(by().markets, ids(all.slice(130, 150)), "scrolled to the foot: the last rows, and none of the first");
  // every drawn row carries its read-age tag, for its own market
  for (const e of all) { const t = e.querySelector("span.readage"); assert.ok(t && t.dataset.read === e.dataset.scid, "row " + e.dataset.scid.slice(0, 6) + " has its tag"); }
  // a row drawn but scrolled away is cold: three blocks later its tag says so, and a row on screen says nothing
  stale = D.assets.get(all[40].dataset.scid); Object.assign(stale, { everyBlock: false, readH: TOPO - 3 });
  L.tickReads();
  const t = all[40].querySelector("span.readage");
  assert.equal(t.hidden, false); assert.equal(t.textContent, "updated 3 blocks ago");
  const on = all[140].querySelector("span.readage");
  assert.equal(on.hidden, true, "a current row shows no tag"); assert.equal(on.textContent, "");
});

test("the summary counts every market, so it carries the tag of the oldest read", () => {
  D.changed(); // a read ends in changed(), which redraws: the summary picks its oldest record as it is drawn
  const tag = screen.querySelectorAll("span.readage").find((t) => !t.parentNode.classList.contains("right"));
  assert.ok(tag, "a tag outside the rows");
  assert.equal(tag.dataset.read, stale.scid, "for the oldest record");
});

test("navigating to Home: the sections' markets exactly, the old list's observer declares nothing more", () => {
  go("home");
  const cards = screen.querySelectorAll(".dcard");
  assert.ok(cards.length >= 20, "the discovery sections are drawn: " + cards.length);
  assert.deepEqual(by().home, ids(cards), "Home declares exactly the markets its sections draw");
  assert.ok(by().tape, "and the tape");
  assert.equal(by().markets, undefined, "Markets' set belonged to the last navigation");
  eye1.fire(rows1.map((e) => [e, true]));
  assert.equal(by().markets, undefined, "a late report from the old list's observer does not declare into this one");
  for (const c of cards) { const t = c.querySelector("span.readage"); assert.ok(t && t.dataset.read === c.dataset.scid, "each card carries its tag"); }
  // the hero's figures are summed over every market: the tag of the oldest read, shown, is beside them
  const hero = screen.querySelector(".hero"), tag = hero.querySelector("span.readage");
  assert.ok(tag && tag.dataset.read === stale.scid, "the hero carries the oldest record's tag");
  assert.equal(tag.hidden, false); assert.match(tag.textContent, /^updated 3 blocks ago$/);
});

test("back to Markets: a new set in a new navigation, from a new observer", () => {
  go("markets");
  assert.equal(eye1.els.size, 0, "the last visit's observer is let go");
  const rows = screen.querySelector(".mlist").children;
  assert.equal(rows.length, 60, "the list starts from its first page again");
  assert.deepEqual(by().markets, ids(rows)); assert.equal(by().home, undefined);
});

test("the Trade hub and the Launch screen declare the rows they draw", () => {
  go("trade");
  const t = screen.querySelectorAll(".mrow[data-scid]");
  assert.equal(t.length, 12); assert.deepEqual(by().trade, ids(t));
  for (const r of t) assert.equal(r.querySelector("span.readage").dataset.read, r.dataset.scid);
  go("launch");
  const l = screen.querySelectorAll(".mrow[data-scid]");
  assert.ok(l.length > 0 && l.length <= 8); assert.deepEqual(by().launch, ids(l));
});

test("the market page declares its market; the chart says older history is loading while it is trimmed", async () => {
  const s = D.trending(1)[0], rec = D.assets.get(s.scid);
  R.gen++; R.current = { name: "token", param: s.scid };
  await T.page(screen, s.scid);
  assert.deepEqual(by(), { page: [s.scid] }, "the page's market, and nothing else");
  assert.equal(screen.querySelector(".hero").querySelector("span.readage").dataset.read, s.scid, "its price carries the tag");
  assert.doesNotMatch(screen.textContent, /older history/i);
  rec.trimmed = true; D.changed();
  assert.match(screen.textContent, /Loading older history from your node/, "trimmed: the chart says its older history is loading");
  rec.trimmed = false; D.changed();
  assert.doesNotMatch(screen.textContent, /older history/i, "back: the line is gone");
});

test("the trade sheet declares its market and the picker its rows; closed, it is cleared; alone it declares nothing", () => {
  const s = D.assets.get(R.current.param);
  T.tradeSheet(s, 1);
  assert.deepEqual(by().sheet, [s.scid], "the sheet's market, beside the page's");
  assert.ok(sheet.querySelector("span.readage"), "and its price carries the tag");
  sheet.querySelector('[aria-label="switch market"]').click();
  const picked = sheet.querySelectorAll("button[data-scid]");
  assert.equal(picked.length, 30, "the picker lists thirty markets");
  assert.deepEqual(by().sheet, ids(picked), "the picker replaces the sheet's set with its rows, exactly");
  for (const b of picked) assert.equal(b.querySelector("span.readage").dataset.read, b.dataset.scid);
  assert.deepEqual(by().page, [s.scid], "the page's own set stays");
  U.closeSheet(); D.changed();
  assert.deepEqual(by().sheet, [], "closed: cleared at the next refresh");
  // a screen that declares nothing shows every market; a sheet opened over it must not turn the tiers on
  go("portfolio");
  T.tradeSheet(s, 1); D.changed();
  assert.equal(by(), null, "nothing declared in this navigation, so every market stays read every block");
  U.closeSheet();
});
