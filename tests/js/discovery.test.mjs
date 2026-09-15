// The discovery work (app/discover.js, app/search.js, the pair label and the warning tiers in app/ui.js, the risk
// box in app/home.js), run without a browser: the scripts are loaded as the page loads them, over a small DOM
// shim and a V.data holding synthetic markets. Every ranking is checked against the figure it claims to rank by.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { ROOT } from "./harness.mjs";

function el(tag) {
  const e = { tag, attrs: {}, children: [], style: {}, className: "", nodeType: 1,
    append(...k) { for (const c of k) this.children.push(c); }, setAttribute(k, v) { this.attrs[k] = v; }, removeAttribute(k) { delete this.attrs[k]; },
    addEventListener() {}, removeChild(c) { this.children.splice(this.children.indexOf(c), 1); },
    get firstChild() { return this.children[0] || null; },
    get textContent() { return this.children.map((c) => (c.nodeType === 3 ? c.text : c.textContent)).join(""); },
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } } };
  return e;
}
function load() {
  const window = { Veil: {}, addEventListener() {}, scrollTo() {}, matchMedia: () => ({ matches: false }) };
  const document = { createElement: el, createElementNS: (ns, t) => el(t), createTextNode: (s) => ({ nodeType: 3, text: s }), querySelector: () => null, querySelectorAll: () => [],
    addEventListener() {}, body: { style: {} }, documentElement: { classList: { contains: () => false } } };
  const V = window.Veil;
  V.shortAddr = (a) => String(a).slice(0, 6) + "…";
  V.data = {
    cfg: {}, assets: new Map(), nfas: new Map(), nfts: new Map(),
    list() { return [...this.assets.values()]; },
    active() { return this.list().filter((s) => s.rd > 0n); },
    ranked() { return this.active().filter((s) => !s.splitWith || s.splitDeepest); },
    trending(n = 12) { return this.ranked().sort((a, b) => b.m.hot - a.m.hot || b.m.vol24 - a.m.vol24).slice(0, n); },
    displayName: (a) => String(a),
  };
  const ls = { getItem: () => null, setItem() {} };
  for (const f of ["amm.js", "stats.js", "ui.js", "live.js", "names.js", "home.js", "discover.js", "search.js"])
    new Function("window", "document", "location", "localStorage", readFileSync(join(ROOT, "app", f), "utf8"))(window, document, { hash: "" }, ls);
  return V;
}
const V = load(), D = V.data, U = V.ui, H = V.home, X = V.disc, Q = V.search;
// contract ids that differ from their first character, as real ones do, so a prefix search means something
let n = 0;
const id = () => createHash("sha256").update("market " + ++n).digest("hex");
const mkt = (o) => {
  const s = Object.assign({ scid: id(), kind: "pool", ticker: "TKN", name: "Token", rd: 100n, points: [{ ts: 1 }, { ts: 2 }], trades: [], sig: "s" + n, splitWith: null, splitDeepest: false }, o);
  s.m = Object.assign({ hot: 0, vol24: 0, liq: 100, thin: false, c24h: 0, age: 1000, isNew: false, upBlocks24: 0, downBlocks24: 0, buys24: 0, sells24: 0, progress: null }, o.m || {});
  D.nameQuote(s); D.assets.set(s.scid, s); return s;
};
const reset = () => D.assets.clear();

test("U.pair names a market by its pair, in the quote's real symbol, and never cuts the ticker", () => {
  reset();
  const a = mkt({ ticker: "ABCDEFGHIJ", name: "A name long enough that the row must cut it somewhere on a phone" });
  assert.equal(U.pairText(a), "ABCDEFGHIJ / DERO");
  assert.ok(!/wrapped/.test(U.pair(a).textContent), "a DERO market carries no wrapped mark");
  const vault = "f".repeat(64);
  D.cfg.vaults = [{ scid: vault, sym: "wXMR", name: "Monero", dec: 12 }];
  // a vault's unit reads as its chain's symbol, marked; the mark is what a token merely named XMR cannot have
  const b = mkt({ ticker: "BRW", quote: vault });
  assert.equal(U.pairText(b), "BRW / XMR");
  assert.equal(H.rowName(b), "BRW / XMR");
  assert.match(U.pair(b).textContent, /^BRW \/ XMRwrapped · 1:1$/);
  assert.equal(U.pairAria(b), "BRW / XMR, wrapped one to one");
  const real = mkt({ ticker: "?", asset: vault, wrappedBase: true });
  assert.equal(U.pairText(real), "XMR / DERO");
  assert.match(U.pair(real).textContent, /wrapped · 1:1/);
  const fake = mkt({ ticker: "XMR", name: "Monero" });
  assert.equal(U.pairText(fake), "XMR / DERO");
  assert.ok(!/wrapped/.test(U.pair(fake).textContent), "a token named XMR is not the vault's unit");
  assert.equal(D.impersonation(fake), "wXMR");
  delete D.cfg.vaults;
});

test("row warnings: impersonation, look-alike, the thin half of a split pair and a retired vault; a shared ticker is not one", () => {
  const sev = (s) => U.warnings(s).map((w) => [w.textContent, w.sev]);
  assert.deepEqual(sev({ dupTicker: true }), [["Ticker shared", 0]]);
  assert.equal(H.mark({ dupTicker: true }), null, "a copied ticker must not mark the legitimate market's row");
  assert.equal(H.mark({ splitWith: ["a", "b"], splitDeepest: true }), null, "the deepest contract of a split pair is the real market");
  assert.equal(sev({ impersonates: "XMR", dupTicker: true, lookalike: true })[0][1], 2);
  assert.match(H.mark({ impersonates: "XMR" }).textContent, /Not real XMR/);
  assert.match(H.mark({ splitWith: ["a", "b"], splitDeepest: false }).textContent, /thinner/);
  assert.match(H.mark({ lookalike: true, dupTicker: true }).textContent, /Look-alike/);
  assert.match(H.mark({ quoteLegacy: true }).textContent, /Retired/);
});

test("the risk box: new is not a risk, thin follows the quote's scale, an unknown scale is said to be unreadable", () => {
  reset();
  const base = { kind: "pool", m: { liq: 5, thin: false, isNew: true, age: 10 } };
  const r1 = H.risks(mkt(base)).map((r) => r.k);
  assert.ok(!r1.includes("new"), "age is informational");
  assert.ok(!r1.includes("thin"), "5 units of a quote whose thin line is lower is not thin: the raw 20 is gone");
  assert.ok(H.risks(mkt({ m: { liq: 50, thin: true } })).some((r) => r.k === "thin"));
  const unk = mkt({ quote: "e".repeat(64), m: { liq: 1e9, thin: false } });
  assert.equal(unk.quoteDecUnknown, true);
  assert.match(H.risks(unk).find((r) => r.k === "thin").t, /cannot be read/);
  assert.equal(H.risks(mkt({ m: { liq: 500, thin: false, isNew: true } })).length, 0, "a legitimate, liquid, new DERO pool carries no risk line");
});

test("badges: NEW is neutral, HOT is gone, Promoted comes from the promotion slots and is never 'endorsed'", () => {
  reset();
  const s = mkt({ m: { isNew: true, hot: 99 } });
  const b = H.badges(s).filter(Boolean);
  assert.ok(b.some((x) => x.textContent === "NEW" && !/warn/.test(x.className)));
  assert.ok(!b.some((x) => x.textContent === "HOT"));
  assert.equal(X.promos(), null, "no promotion interface: nothing is claimed either way");
  V.promo = { slots: () => [{ scid: s.scid }] };
  assert.deepEqual(X.promos().map((x) => x.scid), [s.scid]);
  assert.ok(H.badges(s).filter(Boolean).some((x) => x.textContent === "Promoted"));
  for (const f of ["discover.js", "home.js", "markets.js"]) assert.ok(!/endorse/i.test(readFileSync(join(ROOT, "app", f), "utf8")), f + " says endorse");
  delete V.promo;
});

test("sections rank by the figure they name", () => {
  reset();
  const vault = "f".repeat(64);
  D.cfg.vaults = [{ scid: vault, sym: "wXMR", name: "Monero", dec: 12 }];
  const big = mkt({ ticker: "BIG", m: { vol24: 500, upBlocks24: 3, downBlocks24: 1, buys24: 1, c24h: 12, age: 50 } });
  const small = mkt({ ticker: "SML", m: { vol24: 20, upBlocks24: 9, downBlocks24: 9, c24h: -3, age: 5 } });
  const xmr = mkt({ ticker: "XQ", quote: vault, m: { vol24: 9e9, c24h: 1 } });
  const dust = mkt({ ticker: "DST", m: { thin: true, c24h: 900 } });
  const thinHalf = mkt({ ticker: "BIG", splitWith: ["x", "y"], splitDeepest: false, m: { age: 1, c24h: 800 } });
  assert.deepEqual(X.volume(5).map((s) => s.ticker), ["BIG", "SML"], "volume ranks DERO markets only: there is no rate to put XMR beside them");
  assert.deepEqual(X.active(5).map((s) => s.ticker), ["SML", "BIG"]);
  assert.ok(!X.movers(9).includes(dust), "a market under the thin line never leads the movers");
  assert.ok(!X.movers(9).includes(thinHalf) && !X.fresh(9).includes(thinHalf), "the thin half of a split pair is advertised nowhere");
  assert.equal(X.movers(1)[0], big);
  assert.equal(X.fresh(1)[0], small, "youngest first");
  assert.ok(X.trending(9).includes(xmr));
  // with a vault, the XMR section lists the markets in its unit; a token merely named XMR is not among them
  const named = mkt({ ticker: "XMR", name: "Monero" });
  const v = D.cfg.vaults[0];
  assert.equal(X.vault("XM"), v);
  assert.equal(X.vault("ZC"), null);
  assert.deepEqual(X.wrapped(v, 9), [xmr]);
  assert.ok(!X.wrapped(v, 9).includes(named));
  delete D.cfg.vaults;
  assert.equal(X.vault("XM"), null, "no vault: the sections are the swaps alone");
});

test("XMR and ZEC sections: the markets priced in a vault's unit; with no vault, the wrapped asset is not offered", () => {
  reset();
  assert.match(X.wrapSec("XM", "XMR", "xmr").textContent, /Wrapped XMR is not offered on this network/);
  assert.match(X.wrapSec("ZC", "ZEC", "zec").textContent, /Wrapped ZEC is not offered on this network/);
  const vx = "f".repeat(64);
  D.cfg.vaults = [{ scid: vx, sym: "XMR", name: "Monero", dec: 12 }];
  mkt({ ticker: "TKN", quote: vx, m: { hot: 3 } });
  mkt({ ticker: "XMR", name: "Monero" });
  const t = X.wrapSec("XM", "XMR", "xmr").textContent;
  assert.match(t, /TKN \/ XMR/, "shown by its pair");
  assert.match(t, /wrapped · 1:1/, "with the wrapped mark, on the card's second line");
  assert.ok(!/not offered/.test(t) && !/XMR \/ DERO/.test(t), "a token merely named XMR is not an XMR market");
  assert.match(X.wrapSec("ZC", "ZEC", "zec").textContent, /not offered/);
  delete D.cfg.vaults;
  // the swaps are gone from DMZ: nothing on these screens reads the offer board or leads to a swap screen
  for (const f of ["discover.js", "search.js", "home.js", "markets.js", "nav.js"]) assert.ok(!/zswapOffers|router\.go\("(zswap|xmr|swap)"/.test(readFileSync(join(ROOT, "app", f), "utf8")), f + " still leads to a removed swap");
});

test("search: typed results over markets, NFAs and holder NFTs", () => {
  reset(); D.nfas.clear(); D.nfts.clear();
  const abc = mkt({ ticker: "ABC", name: "Alphabet", tags: "letters,learning", descr: "a token about the first letters", m: { hot: 1 } });
  const abcd = mkt({ ticker: "ABCD", name: "Longer", m: { hot: 50 } });
  mkt({ ticker: "ZZZ", name: "Other" });
  assert.deepEqual(Q.markets("abc", 5).map((s) => s.ticker), ["ABC", "ABCD"], "the exact ticker leads, however hot the other is");
  assert.deepEqual(Q.markets("learning", 5), [abc], "tags");
  assert.deepEqual(Q.markets("first letters", 5), [abc], "descriptions");
  assert.equal(Q.markets("dero", 9).length, 3, "the quote symbol");
  assert.equal(Q.markets("a", 9).filter((s) => s.ticker === "ZZZ").length, 0, "one hex letter does not match every contract id");
  assert.deepEqual(Q.markets(abcd.scid.slice(0, 8), 5), [abcd], "a contract id's start");
  D.nfas.set("n1", { scid: "a".repeat(64), name: "Moon Print", tags: "art", collection: "Prints" });
  D.nfts.set("t1", { scid: "b".repeat(64), name: "Holder badge", tokenTicker: "ABC" });
  const types = (q) => Q.others(q).map((x) => x.type);
  assert.deepEqual(types("art"), ["NFA"]);
  assert.deepEqual(types("badge"), ["NFT"]);
  assert.deepEqual(types("prints"), ["NFA"], "an NFA's collection");
});
