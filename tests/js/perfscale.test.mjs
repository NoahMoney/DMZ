// The data layer at scale (docs/PERFORMANCE.md): the refresh planner, the backoff, the localStorage LRU, the
// incremental registry, one-state refresh cycles, and the incremental decorate — each checked against the thing it
// replaced, so "only what changed" is shown to compute exactly what "everything" computed.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { createHash } from "node:crypto";
import { ROOT } from "./harness.mjs";

// a Storage with a quota: stored keys are own properties (so Object.keys works as on the real one)
function storage(limit = Infinity) {
  const size = (o) => Object.keys(o).reduce((a, k) => a + k.length + o[k].length, 0);
  return Object.create({
    get length() { return Object.keys(this).length; },
    key(i) { return Object.keys(this)[i] ?? null; },
    getItem(k) { return Object.prototype.hasOwnProperty.call(this, k) ? this[k] : null; },
    setItem(k, v) { v = String(v); const had = this.getItem(k); if (size(this) - (had === null ? 0 : k.length + had.length) + k.length + v.length > limit) throw Object.assign(new Error("quota"), { name: "QuotaExceededError" }); this[k] = v; },
    removeItem(k) { delete this[k]; },
  });
}

function load(ls = storage()) {
  const window = { addEventListener: () => {} };
  const run = (f) => new Function("window", "localStorage", "setInterval", "location", readFileSync(join(ROOT, "app", f), "utf8"))(window, ls, () => {}, { hash: "" });
  for (const f of ["config.js", "rpc.js", "amm.js", "stats.js", "live.js", "store.js"]) run(f);
  window.Veil.ui = { clean: (s, n) => String(s || "").slice(0, n), safeUrl: () => "", tickerKey: (t) => String(t || "").toUpperCase().replace(/[^A-Z0-9]/g, ""), hasHomoglyph: () => false, router: { gen: 0, current: null }, h: () => ({}) };
  run("feed.js"); run("names.js");
  return { V: window.Veil, D: window.Veil.data, ls };
}

test("backoff doubles from its base, is 0 with no failure, and never passes its cap", () => {
  const { V } = load();
  const b = (n) => V.live.backoff(n, 2000, 60000);
  assert.deepEqual([0, 1, 2, 3, 4, 5, 6].map(b), [0, 2000, 4000, 8000, 16000, 32000, 60000]);
  assert.equal(b(1000), 60000, "a long outage stays at the cap (no overflow to Infinity)");
});

test("plan: new, shown, starred and recently changed records every block; the cold tier in turn, spread", () => {
  const { D } = load();
  const recs = new Map(), ids = [];
  for (let i = 0; i < 44; i++) { const id = String(i).padStart(64, "0"); ids.push(id); recs.set(id, { scid: id, readH: 100, readT: 100, changedH: 50 }); }
  const fresh = "f".repeat(64); ids.push(fresh);                   // listed, never read
  recs.get(ids[1]).changedH = 95;                                   // changed 6 blocks ago: RECENT
  recs.get(ids[2]).retryH = 110;                                    // failing: backing off
  const hot = new Set([ids[0]]);
  const p = D.plan(ids, recs, hot, 101, 101);
  assert.deepEqual(p.slice(0, 3), [ids[0], ids[1], fresh], "the hot ones, the recent one and the new one come first");
  assert.ok(!p.includes(ids[2]), "a record waiting out its backoff is not read");
  assert.equal(p.length - 3, Math.ceil(41 / 8), "one eighth of the cold tier per block");
  assert.equal(recs.get(ids[5]).everyBlock, false, "a cold record says it is not read every block");
  // no declaration → everything is hot: the plan is every record not read at this state
  assert.equal(D.plan(ids, recs, null, 101, 101).length, 44, "hot = null reads every record but the backing-off one");
  assert.equal(D.plan(ids, recs, null, 100, 100).length, 1, "a state already read is not read again (only the new one)");
});

test("plan: every cold record is read within COLD_EVERY blocks, and no block reads more than its share", () => {
  const { D } = load();
  const recs = new Map(), ids = [];
  for (let i = 0; i < 40; i++) { const id = String(i).padStart(64, "0"); ids.push(id); recs.set(id, { scid: id, readH: 100, readT: 100, changedH: 0 }); }
  const last = new Map(ids.map((id) => [id, 100])); let worst = 0, most = 0;
  for (let H = 101; H <= 140; H++) {
    const p = D.plan(ids, recs, new Set(), H, H);
    most = Math.max(most, p.length);
    for (const id of p) { worst = Math.max(worst, H - last.get(id)); last.set(id, H); Object.assign(recs.get(id), { readH: H, readT: H }); }
  }
  assert.ok(most <= 5, "at most 40/8 cold reads a block: " + most);
  assert.ok(worst <= 8, "no cold record waits more than 8 blocks: " + worst);
  for (const [id, h] of last) assert.ok(140 - h < 8, id.slice(-2) + " was read recently");
});

test("LRU: a full store drops the least recently used copy, never the one being written nor other keys", () => {
  const { D, ls } = load(storage(9000));
  ls.setItem("veil.settings", "{}");
  const put = (i, u) => ls.setItem("veil.m." + i, JSON.stringify({ u, v: 4, pad: "x".repeat(2000) }));
  put("a", 10); put("b", 30); put("c", 20);
  assert.equal(D.idx().size, 3, "the index is rebuilt from storage, read from each copy's leading u");
  assert.ok(D.store("veil.m.d", JSON.stringify({ u: 40, v: 4, pad: "y".repeat(3000) }), "d"), "the write succeeds");
  assert.equal(ls.getItem("veil.m.a"), null, "the oldest copy (u 10) went");
  assert.ok(ls.getItem("veil.m.b") && ls.getItem("veil.settings") !== null, "the newer copy and the settings stay");
  assert.ok(ls.getItem("veil.m.d"), "and the new one is there");
  assert.equal(D.store("veil.m.e", "z".repeat(20000), "e"), false, "a copy larger than the whole quota fails, having tried everything");
  assert.ok(ls.getItem("veil.settings") !== null, "without touching anything but copies");
});

test("LRU: copies are kept within their budget, evicted oldest first in one batch", () => {
  const { D, ls } = load(storage());
  D.budget = 10000;
  for (let i = 0; i < 8; i++) assert.ok(D.store("veil.m.k" + i, JSON.stringify({ u: 0, pad: "x".repeat(2000) }), "k" + i));
  let used = 0; for (const k of Object.keys(ls)) used += k.length + ls[k].length;
  assert.ok(used <= 10000, "within the budget: " + used);
  assert.ok(ls.getItem("veil.m.k7") && !ls.getItem("veil.m.k0"), "the newest stays, the oldest went");
});

test("the trade ledger is not cut at 500, and a full store makes room for it from market copies", () => {
  const { D, ls } = load(storage(200000));
  const t = (i) => ({ scid: "a".repeat(64), asset: "b".repeat(64), side: 1, in: "1", out: String(i + 1), ts: i, txid: createHash("sha256").update(String(i)).digest("hex"), published: false, dec: "5" });
  for (let i = 0; i < 600; i++) D.addLocalTrade(t(i));
  assert.equal(D.localTrades().length, 600, "every trade of the cost basis is kept");
  const used = Object.keys(ls).reduce((a, k) => a + k.length + ls[k].length, 0); // filled to within 40 characters of the quota
  ls.setItem("veil.m.old", JSON.stringify({ u: 1, v: 4, pad: "x".repeat(200000 - used - 32 - 40) }));
  D._idx = null; D.addLocalTrade(t(600));
  assert.equal(D.localTrades().length, 601, "the 601st trade is written");
  assert.equal(ls.getItem("veil.m.old"), null, "by dropping a market copy");
});

// ---- a small chain with versioned state, behind a node that logs every read ----
const sha = (s) => createHash("sha256").update(s).digest("hex");
const hex = (s) => Buffer.from(s, "utf8").toString("hex");
function chain(V, nLaunch) {
  const raw = "02" + sha("treasury"), code = "Function Initialize() Uint64\n10 RETURN 0\nEnd Function", F = sha("factory");
  const trader = V.rawToAddress("03" + sha("trader")), states = new Map(); let topo = 1000;
  const st = { [F]: { pc: "0", lc: String(nLaunch), nc: "0", regfee: "1", tstr: hex(V.rawToAddress(raw)), v: "2" } };
  const ids = [];
  const add = (i) => {
    const id = sha("m" + i); ids.push(id); st[F]["launch_" + i] = hex(id); st[F].lc = String(ids.length);
    const s = (st[id] = { fee: "50", pfee: "5000", treasury: raw, nameHdr: hex("Coin " + i), ticker: hex(i % 3 ? "C" + i : "DUP"), dec: "5", S: "1000000000", phase: "1", rd: "1000000", ra: "2000000", lp: "1", tc: "0", pc: "0", lh: "0", lt: "0" });
    for (let k = 0; k < 5; k++) trade(id, k);
    return s;
  };
  const trade = (id, k = 0) => {
    const s = st[id], pc = +s.pc, tc = +s.tc, ts = Math.floor(Date.now() / 1000) - 3000 + pc * 60;
    s.rd = String(+s.rd + 1000 + k); s["p_" + pc] = hex([topo, ts, s.rd, s.ra].join("|")); s["t_" + tc] = hex(["1", trader, "1000", "10", topo, ts].join("|"));
    s.pc = String(pc + 1); s.tc = String(tc + 1); s.lh = String(topo); s.lt = String(ts);
  };
  for (let i = 0; i < nLaunch; i++) add(i);
  const snap = () => states.set(topo, JSON.parse(JSON.stringify(st)));
  snap();
  const log = [];
  const node = {
    call: async (m) => { if (m === "DERO.GetHeight") return { height: topo, topoheight: topo }; throw new Error("no " + m); },
    height: async () => topo,
    getTx: async () => ({ block_height: 900 }),
    getSC: async (scid, o = {}) => {
      if (node.down) throw new Error("the node refused");
      const T = o.topoheight || topo, s = (states.get(T) || {})[scid] || {}, vals = {};
      log.push({ scid, keys: o.keys || [], topo: o.topoheight, code: !!o.code });
      for (const k of o.keys || []) if (s[k] !== undefined) vals[k] = s[k];
      return { vals, vars: {}, strs: {}, balances: {}, code: o.code && scid !== F ? code : "" };
    },
  };
  return { F, raw, code, ids, node, log, st, add, trade, get topo() { return topo; }, mine(fn) { topo++; if (fn) fn(); snap(); } };
}
function wire(V, c) {
  V.node = c.node;
  V.live.height = c.topo; V.live.topo = c.topo;
  Object.assign(V.data.cfg, { factory: c.F, treasuries: [V.rawToAddress(c.raw)], vaults: [], vault: "", feeBps: 50, launchPfeeBps: 5000, pfeeBps: 5000, templates: { launch: { sha256: sha(c.code), legacy: [] } } });
  V.data.hashes = {};
  V.data.loadFounders = async () => null;
}
const block = (V, c, fn) => { c.mine(fn); V.live.height = c.topo; V.live.topo = c.topo; c.log.length = 0; };

test("the registry is read once and then only its new slots; a lower counter reads it whole", async () => {
  const { V, D } = load(); const c = chain(V, 30); wire(V, c);
  await D.registry_(c.topo, false);
  assert.equal(D.registry.launches.length, 30);
  block(V, c, () => { c.add(30); c.add(31); });
  await D.registry_(c.topo, false);
  const slots = c.log.flatMap((r) => r.keys).filter((k) => /^launch_/.test(k));
  assert.deepEqual(slots, ["launch_30", "launch_31"], "only the two new slots are asked for");
  assert.equal(D.registry.launches.length, 32);
  c.log.length = 0; await D.registry_(c.topo, false);
  assert.equal(c.log.length, 0, "the same state is not read twice");
  block(V, c, () => { c.st[c.F].lc = "5"; });
  await D.registry_(c.topo, false);
  assert.equal(D.registry.launches.length, 5, "a counter that went down is another chain: read whole");
});

test("a refresh reads one state, and asks for no key twice", async () => {
  const { V, D } = load(); const c = chain(V, 12); wire(V, c);
  await D.refresh(true);
  assert.equal(D.assets.size, 12, "every launch loaded");
  block(V, c, () => c.trade(c.ids[3]));
  await D.refresh();
  assert.ok(c.log.every((r) => r.topo === c.topo), "every read of the cycle is pinned to its topoheight");
  const per = new Map();
  for (const r of c.log) for (const k of r.keys) { const id = r.scid + "/" + k; per.set(id, (per.get(id) || 0) + 1); }
  const twice = [...per].filter(([, n]) => n > 1);
  assert.deepEqual(twice, [], "no key read twice in one cycle");
  const m = D.assets.get(c.ids[3]);
  assert.equal(m.pc, 6n, "the changed market has its new price point");
  assert.equal(m.readH, c.topo, "and says which block it was read at");
  assert.equal(c.log.filter((r) => r.scid === c.ids[3]).length, 2, "a changed market costs its probe and one more call");
  c.log.length = 0; await D.refresh();
  assert.equal(c.log.length, 0, "a second refresh at the same state reads nothing");
});

test("a first load is not a change: once a screen declares, the markets it does not show go cold at the next block", async () => {
  const { V, D } = load(); const c = chain(V, 24); wire(V, c);
  await D.refresh(true);
  D.see([c.ids[0]]);
  block(V, c);
  await D.refresh();
  const probes = c.log.filter((r) => r.scid !== c.F && r.keys.includes("rd")).length;
  assert.equal(probes, 1 + Math.ceil(23 / 8), "the shown market and an eighth of the rest, not all 24: " + probes);
  const cold = [...D.assets.values()].filter((s) => s.everyBlock === false);
  assert.equal(cold.length, 23, "every market not shown is in the cold tier");
  block(V, c);
  const unread = cold.find((s) => s.readH < c.topo - 1);
  assert.ok(unread && D.stale(unread) && D.behind(unread) >= 2, "a cold record not read this block says it is behind");
  assert.equal(D.stale(D.assets.get(c.ids[0])), false, "the shown one, read at the last block, is not stale");
});

test("asks made during a refresh run once after it; a failing node is left alone until its backoff ends", async () => {
  const { V, D } = load(); const c = chain(V, 4); wire(V, c);
  await D.refresh(true);
  block(V, c, () => c.trade(c.ids[0]));
  const a = D.refresh(), b = D.refresh(), d = D.refresh();
  block(V, c, () => c.trade(c.ids[1]));
  await Promise.all([a, b, d]);
  const reg = c.log.filter((r) => r.scid === c.F && r.keys.includes("pc"));
  assert.equal(reg.length, 1, "three asks, two cycles, and the second at a new state reads the factory once: " + reg.length);
  assert.equal(D.assets.get(c.ids[1]).pc, 6n, "the queued cycle saw the later block");
  c.node.down = true; block(V, c);
  await D.refresh().catch(() => {});
  assert.equal(D.fails, 1); assert.ok(D.retryAt > Date.now(), "a failure sets a backoff");
  assert.ok(D.lastError, "and is stated, so the stale banner can say so");
  c.log.length = 0; await D.refresh();
  assert.equal(c.log.length, 0, "during the backoff an ordinary refresh does not reach the node");
  c.node.down = false; await D.refresh(true);
  assert.equal(D.fails, 0); assert.equal(D.lastError, null, "an explicit full refresh goes through and clears it");
});

test("incremental decorate computes exactly what a full decorate computes", async () => {
  const { V, D } = load(); const c = chain(V, 9); wire(V, c);
  await D.refresh(true);
  const view = () => [...D.assets.values()].map((s) => JSON.stringify([s.scid, s.name, s.pair, s.dupTicker, s.lookalike, s.impersonates, s.splitWith, s.splitDeepest, s.m.price, s.m.c24h, s.m.vol24, s.m.hot, s.m.age]));
  for (const change of [() => c.trade(c.ids[2]), () => c.add(9), () => { c.trade(c.ids[0]); c.trade(c.ids[4]); }]) {
    block(V, c, change);
    await D.refresh();
    const inc = view(); D.decorate(); assert.deepEqual(inc, view());
  }
});

test("a cold market keeps the points its figures read, gives the same figures, and gets the rest back", async () => {
  const { V, D } = load(); const S = V.stats, now = Math.floor(Date.now() / 1000);
  const pts = []; for (let i = 0; i < 400; i++) pts.push({ height: i, ts: now - 3 * 86400 + i * 648, rd: BigInt(1000000 + i * 37), ra: 2000000n });
  const s = { scid: "a".repeat(64), kind: "pool", rd: 1000000n, ra: 2000000n, pc: 400n, tc: 0n, trades: [], points: pts.slice(), installHeight: 1, quote: null };
  D.assets = new Map([[s.scid, s]]);
  const before = JSON.stringify(S.enrich(s, 10));
  D.trim(null); assert.equal(s.points.length, 400, "with no declaration nothing is cut");
  D.trim(new Set([s.scid])); assert.equal(s.points.length, 400, "a hot market is not cut");
  D.trim(new Set());
  assert.ok(s.trimmed && s.points.length < 150, "a cold one keeps about a day: " + s.points.length);
  assert.ok(s.points[0].ts < now - 86400 && s.points[1].ts >= now - 86400, "and the one point before the day");
  assert.equal(JSON.stringify(S.enrich(s, 10)), before, "its figures are unchanged");
  V.node = { getSC: async (id, o) => ({ vals: Object.fromEntries(o.keys.map((k) => { const p = pts[+k.slice(2)]; return [k, Buffer.from([p.height, p.ts, p.rd, p.ra].join("|")).toString("hex")]; })) }) };
  await D.deepen(s.scid);
  assert.equal(s.points.length, 400, "deepen gives the whole window back"); assert.equal(s.trimmed, false);
  assert.deepEqual(s.points.map((p) => p.ts), pts.map((p) => p.ts), "in order");
});

test("read-age tags are tracked, not found by scanning the document, and let go once removed", () => {
  const { V, D } = load();
  const s = { scid: "a".repeat(64), readH: 90, everyBlock: false };
  D.assets = new Map([[s.scid, s]]); V.live.height = 100;
  const el = { dataset: { read: s.scid }, isConnected: true, hidden: true, textContent: "" };
  V.live.readTag(el);
  assert.equal(el.hidden, false); assert.equal(el.textContent, "updated 10 blocks ago", "a cold record ten blocks old says so");
  s.readH = 100; V.live.tickReads();
  assert.equal(el.hidden, true, "current again: the tag hides"); assert.equal(el.textContent, "");
  el.isConnected = false; V.live.tickReads();
  assert.equal(V.live.tags.has(el), false, "a tag that has left the page is let go");
  const never = { dataset: { read: s.scid }, isConnected: false };
  V.live.readTag(never); never._t -= 61000; V.live.tickReads();
  assert.equal(V.live.tags.has(never), false, "and so is one never put on a page within a minute");
});

test("until: before it, enrich gives the same figures for an unchanged market", () => {
  const { V, D } = load(); const S = V.stats, real = Date.now, t0 = Math.floor(real() / 1000);
  const pts = []; for (let i = 0; i < 300; i++) pts.push({ height: i, ts: t0 - 90000 + 150 + i * 300, rd: BigInt(1000000 + ((i * 7919) % 5000)), ra: 2000000n });
  const s = { scid: "b".repeat(64), kind: "pool", rd: 1000000n, ra: 2000000n, trades: [], points: pts, installHeight: 1, quote: null };
  try {
    const u = D.until(s, t0); assert.ok(u > t0 && u <= t0 + 3600, "a boundary in the next hour: " + (u - t0));
    Date.now = () => t0 * 1000; const a = JSON.stringify(S.enrich(s, 10));
    Date.now = () => (u - 1) * 1000; const b = JSON.stringify(S.enrich(s, 10));
    assert.equal(b, a, "one second before the boundary nothing has moved");
  } finally { Date.now = real; }
});
