// app/xmr.js: which htlc.bas exit a transaction meets, and so when Punish is offered, against the contract's own
// comparisons (Claim: h < t1; Refund: t1 <= h < t2; Punish: t2 <= h < t2 + g2; Reclaim: h >= t2 + g2), at every
// boundary; the stages the Monero screen draws for a lock; and the ZEC screen's DERO-side stages for an offer.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const noop = () => ({});
const window = { Veil: { ui: { clean: (s, n) => String(s || "").slice(0, n), h: noop, render: noop, button: noop, kv: noop, router: { on() {}, current: null }, openSheet: noop, closeSheet: noop, toast: noop },
  amm: { fmtUnits: (v) => String(v) }, data: { cfg: { htlc: "c".repeat(64), board: "b".repeat(64), swap: "d".repeat(64), network: "simulator" }, list: () => [] }, wallet: {}, shortAddr: (a) => a } };
for (const f of ["track.js", "zswap.js", "xmr.js"]) new Function("window", "document", readFileSync(join(ROOT, "app", f), "utf8"))(window, { querySelector: () => null });
const X = window.Veil.xmr, Zs = window.Veil.zswap;

test("the exit a transaction meets, at every boundary of the four windows", () => {
  const e = (h, st = 0) => X.exit(st, 100, 150, 30, h).k;
  assert.equal(e(1), "claim"); assert.equal(e(99), "claim");
  assert.equal(e(100), "refund"); assert.equal(e(149), "refund");
  assert.equal(e(150), "punish"); assert.equal(e(179), "punish");
  assert.equal(e(180), "reclaim"); assert.equal(e(1000000), "reclaim");
  assert.deepEqual(X.exit(0, 100, 150, 30, 150), { k: "punish", from: 150, until: 180 });
  for (const st of [1, 2, 3, 4]) assert.equal(e(160, st), "closed");
});

test("anything unread is unreadable, never a window", () => {
  assert.equal(X.exit(null, 100, 150, 30, 160).k, "unreadable");
  assert.equal(X.exit(0, null, 150, 30, 160).k, "unreadable");
  assert.equal(X.exit(0, 100, null, 30, 160).k, "unreadable");
  assert.equal(X.exit(0, 100, 150, null, 160).k, "unreadable");
  assert.equal(X.exit(0, 100, 150, 30, 0).k, "unreadable"); // no tip read
  assert.equal(X.exit(0, 150, 150, 30, 160).k, "unreadable"); // t2 <= t1: no contract lock looks like this
});

test("Punish is offered only when the next block is inside [t2, t2 + g2)", () => {
  const l = { st: 0, t1: 100, t2: 150 };
  assert.equal(X.canPunish(l, 30, 148), false); // the next block, 149, is Refund's
  assert.equal(X.canPunish(l, 30, 149), true);  // the next block is t2
  assert.equal(X.canPunish(l, 30, 178), true);  // the next block, 179, is the window's last
  assert.equal(X.canPunish(l, 30, 179), false); // the next block is t2 + g2: refused by the contract
  assert.equal(X.canPunish(l, null, 160), false);
  assert.equal(X.canPunish(l, 30, 0), false);
  assert.equal(X.canPunish({ st: 3, t1: 100, t2: 150 }, 30, 160), false);
  assert.equal(X.canPunish({ st: null, t1: 100, t2: 150 }, 30, 160), false);
});

test("a lock's stages: its depth against 8, the window open now, the Monero side unseen", () => {
  const l = { st: 0, t1: 100, t2: 150, asset: "" };
  const s = X.stages(l, 30, 160, 3);
  assert.deepEqual(s.map((x) => x.state), ["active", "unseen", "skipped", "skipped", "active", "waiting", "unseen"]);
  assert.equal(s[0].conf, 3); assert.equal(s[0].need, 8);
  assert.deepEqual(s.map((x) => x.chain), ["DERO", "Monero", "DERO", "DERO", "DERO", "DERO", "Monero"]);
  assert.deepEqual([s[2].deadline.height, s[3].deadline.height, s[4].deadline.height], [100, 150, 180]);
  assert.equal(X.stages(l, 30, 50, 8)[0].state, "done");
  assert.deepEqual(X.stages(l, 30, 50, 8).slice(2, 6).map((x) => x.state), ["active", "waiting", "waiting", "waiting"]);
  assert.equal(X.stages(l, 30, 50, null)[0].conf, null); // depth unreadable: no number, and it says so
  assert.match(X.stages(l, 30, 50, null)[0].label, /could not be read/);
  assert.deepEqual(X.stages({ ...l, st: 1 }, 30, 160, null).slice(2, 6).map((x) => x.state), ["done", "skipped", "skipped", "skipped"]);
  assert.deepEqual(X.stages({ ...l, st: 3 }, 30, 160, null).slice(2, 6).map((x) => x.state), ["skipped", "skipped", "done", "skipped"]);
  assert.deepEqual(X.stages({ ...l, st: 4 }, 30, 900, null).slice(2, 6).map((x) => x.state), ["skipped", "skipped", "skipped", "done"]);
  assert.deepEqual(X.stages({ ...l, st: null }, 30, 160, null).slice(2, 6).map((x) => x.state), ["unreadable", "unreadable", "unreadable", "unreadable"]);
});

test("a ZEC offer's DERO-side stages", () => {
  const o = { st: 0n, exp: 500n, ad: null };
  assert.deepEqual(Zs.stages(o, null, 100, null).map((x) => x.state), ["done", "active", "waiting", "unseen", "unseen", "waiting", "waiting"]);
  // accepted, and the maker never locked by ad: dead, everything after skipped
  const dead = { st: 1n, exp: 500n, ad: 300n };
  assert.deepEqual(Zs.stages(dead, null, 300, null).map((x) => x.state), ["done", "done", "failed", "skipped", "skipped", "skipped", "skipped"]);
  assert.equal(Zs.stages(dead, null, 299, null)[2].state, "active");
  assert.equal(Zs.stages(dead, null, 299, null)[2].deadline.height, 300);
  const l = { id: 1, st: 0n, exp: 2000n };
  const s = Zs.stages(dead, l, 400, 2);
  assert.deepEqual(s.map((x) => x.state), ["done", "done", "active", "unseen", "unseen", "active", "waiting"]);
  assert.equal(s[2].conf, 2); assert.equal(s[2].need, 8);
  assert.deepEqual(Zs.stages(dead, l, 2000, 8).slice(5).map((x) => x.state), ["skipped", "active"]);
  assert.deepEqual(Zs.stages(dead, { ...l, st: 1n }, 400, null).slice(5).map((x) => x.state), ["done", "skipped"]);
  assert.deepEqual(Zs.stages({ st: 2n, exp: 500n, ad: null }, null, 100, null).map((x) => x.state), ["done", "skipped", "skipped", "skipped", "skipped", "skipped", "skipped"]);
});
