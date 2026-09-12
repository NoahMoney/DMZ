// Registry poisoning: app/feed.js refresh() loads every id in the factory's pool_ / launch_ lists and puts
// whatever loadContract() returns into this.assets WITHOUT checking the kind — unlike the nft_ and nfa_ loops
// beside it, which both filter on s.kind. loadNFA() returns a record with no `points` and no `trades`, so one
// RegisterPool(<an NFA's scid>) — permissionless, regfee only — makes decorate()'s enrich() loop throw and every
// market after it in map order lose its metrics. Home / Markets / Trade then throw on s.m.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

function loadData() {
  const window = {};
  const store = new Map();
  const localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => store.set(k, v),
    removeItem: (k) => store.delete(k),
  };
  const run = (f, ...extra) => new Function("window", "localStorage", "addEventListener", "setInterval", ...extra.map((e) => e[0]), readFileSync(join(ROOT, "app", f), "utf8"))(window, localStorage, () => {}, () => {}, ...extra.map((e) => e[1]));
  run("config.js");
  run("rpc.js");
  run("amm.js");
  run("stats.js");
  // feed.js, and names.js which adds decorate() to it, only need these four helpers off V.ui
  window.Veil.ui = {
    clean: (s, n) => String(s || "").slice(0, n),
    safeUrl: (u) => (/^https:\/\//.test(u) ? u : ""),
    tickerKey: (t) => String(t || "").toUpperCase().replace(/[^A-Z0-9]/g, ""),
    hasHomoglyph: () => false,
    router: { gen: 0 },
  };
  window.addEventListener = () => {};
  run("feed.js");
  run("names.js");
  return window.Veil;
}

// exactly the shape app/feed.js loadNFA() builds: no points, no trades, no rd/ra
const nfaRecord = (scid) => ({ scid, kind: "nfa", treasury: null, sig: "n", installHeight: 1, royalty: 5n, protoFee: 50n, scBalance: 1n, startPrice: 1n, name: "Grief", ticker: "Gri", quote: null });
const poolRecord = (scid, asset) => ({
  scid, kind: "pool", fee: 50n, pfee: 5000n, treasury: null, trades: [],
  points: [{ height: 1, ts: Math.floor(Date.now() / 1000) - 10, rd: 1000000n, ra: 2000000n }],
  rd: 1000000n, ra: 2000000n, lp: 1000n, fd: 0n, fa: 0n, tc: 0n, pc: 1n, lh: 1n, lt: 1n,
  asset, dec: 5n, quote: null, name: "pool", ticker: scid.slice(0, 4).toUpperCase(), sig: "s", installHeight: 1,
});

// FE-2, repaired. A registration is permissionless: anyone can pay the fee and file any contract id under
// pool_<i>. Before the repair a record with no price history — which is exactly what loadNFA returns — reached
// stats.enrich and threw, and the throw came out of decorate(), so one such registration cost every client its
// Home, Markets and Trade screens. The metrics now read a missing history as an empty one, and the loop files a
// record only when its verified kind is the one the list promises. Both halves are asserted.
test("stats.enrich survives a record with no price history (what loadNFA returns)", () => {
  const V = loadData();
  const m = V.stats.enrich(nfaRecord("c".repeat(64)), 1000);
  assert.ok(m && typeof m === "object", "enrich returns metrics rather than throwing");
});

test("an NFA filed under pool_<i> cannot abort decorate() or the ranking", () => {
  const V = loadData(), D = V.data;
  const nfa = nfaRecord("c".repeat(64));
  const p1 = poolRecord("a".repeat(64), "b".repeat(64));
  const p2 = poolRecord("d".repeat(64), "e".repeat(64));
  D.assets = new Map([[nfa.scid, nfa], [p1.scid, p1], [p2.scid, p2]]);
  D.height = 1000;
  D.decorate();
  assert.ok(p1.m, "a healthy market still gets its metrics");
  assert.ok(p2.m, "and so does every one after the poisoned record");
  assert.ok(Array.isArray(D.trending(5)), "Home and Markets can still sort");
});

test("every registry loop files a record only when its kind matches the list", () => {
  const feed = readFileSync(join(ROOT, "app", "feed.js"), "utf8");
  const body = feed.slice(feed.indexOf("async refresh("), feed.indexOf("failed(rec, e)"));
  const poolLoop = body.slice(body.indexOf("await pmap(ids,"), body.indexOf("await pmap(reg.nfts,"));
  assert.match(body, /reg\.nfts[\s\S]*?s\.kind === "nft"/, "the nft loop filters on kind");
  assert.match(body, /reg\.nfas[\s\S]*?s\.kind === "nfa"/, "the nfa loop filters on kind");
  assert.equal(/s\.kind/.test(poolLoop), true, "the pool_/launch_ loop must filter on kind as the others do");
});
