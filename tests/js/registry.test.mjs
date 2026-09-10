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
  // feed.js only needs these four helpers off V.ui at load and inside decorate()
  window.Veil.ui = {
    clean: (s, n) => String(s || "").slice(0, n),
    safeUrl: (u) => (/^https:\/\//.test(u) ? u : ""),
    tickerKey: (t) => String(t || "").toUpperCase().replace(/[^A-Z0-9]/g, ""),
    hasHomoglyph: () => false,
    router: { gen: 0 },
  };
  window.addEventListener = () => {};
  run("feed.js");
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

test("stats.enrich throws on a record with no price history (what loadNFA returns)", () => {
  const V = loadData();
  assert.throws(() => V.stats.enrich(nfaRecord("c".repeat(64)), 1000), TypeError);
});

test("one NFA in the pool_/launch_ list aborts decorate() and breaks every ranking", () => {
  const V = loadData(), D = V.data;
  const nfa = nfaRecord("c".repeat(64));
  const p1 = poolRecord("a".repeat(64), "b".repeat(64));
  const p2 = poolRecord("d".repeat(64), "e".repeat(64));
  D.assets = new Map([[nfa.scid, nfa], [p1.scid, p1], [p2.scid, p2]]);
  D.height = 1000;
  assert.throws(() => D.decorate(), TypeError, "decorate() should abort on the poisoned record");
  assert.equal(p1.m, undefined, "a healthy market after it never gets its metrics");
  assert.equal(D.lastRefresh, 0, "refresh() never reaches lastRefresh, so screens stay in the 'reading' state");
  assert.throws(() => D.trending(5), TypeError, "Home/Markets sort on s.m.hot");
});

test("the pool_/launch_ loop has no kind guard while the nft_ and nfa_ loops do", () => {
  const feed = readFileSync(join(ROOT, "app", "feed.js"), "utf8");
  const body = feed.slice(feed.indexOf("async refresh("), feed.indexOf("failed(rec, e)"));
  const poolLoop = body.slice(body.indexOf("await pmap(ids,"), body.indexOf("await pmap(reg.nfts,"));
  assert.match(body, /reg\.nfts[\s\S]*?s\.kind === "nft"/, "the nft loop filters on kind");
  assert.match(body, /reg\.nfas[\s\S]*?s\.kind === "nfa"/, "the nfa loop filters on kind");
  // FINDING (today's behaviour, asserted so the fix flips it): the pool_/launch_ loop accepts any kind.
  assert.equal(/s\.kind/.test(poolLoop), false, 'the pool_/launch_ loop now checks s.kind — invert this assertion and drop the finding');
});
