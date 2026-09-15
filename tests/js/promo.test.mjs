// app/promo.js V.promo: the app's reading of contracts/promo.bas must be the contract's own arithmetic (the increment,
// the tie rule, what is claimable), and V.promo.slots() / statusOf() — what the discovery and market pages show — must
// show only listed, verified targets, once each, best first. The contract side is tests/promo_test.go.
//
//   node --test tests/js/promo.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, rng, randU } from "./harness.mjs";

const assets = new Map(), nfas = new Map();
const nop = () => null;
const id = (c) => c.repeat(64);
const vaults = [{ scid: id("e"), name: "Monero", sym: "XMR", dec: 12 }, { scid: id("f"), name: "Zcash", sym: "ZEC", dec: 8 }];
const window = { Veil: {
  ui: { h: nop, render: nop, button: nop, kv: nop, toast: nop, clean: (s, n) => String(s || "").slice(0, n), router: { on() {} } },
  data: { cfg: {}, assets, nfas, onChange() {}, allVaults: () => vaults, rawToAddr: (x) => (/^[0-9a-f]{66}$/.test(x) ? "dero1" + x.slice(2, 14) : null) },
  amm: {}, live: { height: 0 }, wallet: {} } };
new Function("window", readFileSync(join(ROOT, "app", "promo.js"), "utf8"))(window);
const P = window.Veil.promo, _ = P._;

const slot = (i, target, bid, ep, sq, cl = false, by = "dero1bidder" + i) => ({ i, target, bid, by, ep, sq, cl });
const state = (slots, o = {}) => Object.assign({ scid: id("5"), k: 3, asset: _.assetOf(id("e")), e: 4800n, minbid: 100000n, inc: 500n, pr: 0n, fc: 0n, bi: 0n, rf: 0n, sx: 0n, treasury: "dero1t", slots }, o);

test("the auction's asset is DERO or a wrapped asset this build ships, named 'wXMR (XMR, wrapped 1:1)'; any other is not named", () => {
  assert.deepEqual(_.assetOf("0".repeat(64)), { id: "0".repeat(64), sym: "DERO", dec: 5n, label: "DERO", dero: true });
  const x = _.assetOf(id("e"));
  assert.equal(x.label, "wXMR (XMR, wrapped 1:1)"); assert.equal(x.dec, 12n); assert.equal(x.dero, false);
  assert.equal(_.assetOf(id("f")).label, "wZEC (ZEC, wrapped 1:1)");
  assert.equal(_.assetOf(id("a")), null); // a token with no vault in this build: its scale and name are unknown
  assert.equal(_.assetOf("xyz"), null);
});

test("bps is floor(x * f / 10000), exactly, for any uint64", () => {
  const r = rng(7);
  for (let i = 0; i < 2000; i++) {
    const x = randU(r, 1n << 64n), f = randU(r, 10001n);
    assert.equal(_.bps(x, f), (x * f) / 10000n);
  }
});

test("the minimum next bid is the contract's: the minimum while a slot is free, else lowest + increment, at least + 1", () => {
  const s = state([slot(3, id("a"), 100000n, 10n, 0n), slot(4, id("b"), 100000n, 10n, 1n)]);
  assert.deepEqual(_.minNextOf(s, 10n), { need: 100000n, full: false, low: null });
  s.slots.push(slot(5, id("c"), 100000n, 10n, 2n));
  const m = _.minNextOf(s, 10n);
  // tests/promo_test.go TestPromoOutbidRefundsAndTies: 104,999 is refused, 105,000 displaces the latest of the equal bids
  assert.equal(m.need, 105000n); assert.equal(m.full, true); assert.equal(m.low.sq, 2n);
  const tiny = state([slot(0, id("a"), 5n, 3n, 0n)], { k: 1, e: 20n, minbid: 1n, inc: 1n });
  assert.equal(_.minNextOf(tiny, 3n).need, 6n); // 1 bps of 5 rounds to 0: the increment is still one unit
  // bids from another epoch do not fill this one
  assert.equal(_.minNextOf(s, 11n).need, 100000n);
});

test("ranking is bid down, then the earlier bid first", () => {
  const l = [slot(0, id("a"), 5n, 1n, 4n), slot(1, id("b"), 9n, 1n, 7n), slot(2, id("c"), 5n, 1n, 2n)];
  assert.deepEqual(_.rank(l).map((x) => x.i), [1, 2, 0]);
});

test("claimable is pr plus every final bid not yet swept, and nothing standing", () => {
  const s = state([slot(0, id("a"), 7n, 9n, 0n), slot(1, id("b"), 11n, 8n, 1n, true), slot(3, id("c"), 13n, 10n, 2n)], { pr: 100n });
  assert.equal(_.claimableOf(s, 10n), 107n);
  assert.equal(_.claimableOf(s, 11n), 120n);
  assert.equal(_.heldOf(s), 20n); // the balance is pr + held
  assert.equal(_.epochOf(s, 4800 * 10 + 4799), 10n);
});

test("slots() shows the current epoch's winners that the app can verify, once each, best first", () => {
  assets.clear(); nfas.clear();
  assets.set(id("a"), { scid: id("a"), kind: "launch", ticker: "AAA", name: "Aaa" });
  assets.set(id("b"), { scid: id("b"), kind: "pool", ticker: "BBB", name: "Bbb" });
  nfas.set(id("c"), { scid: id("c"), kind: "nfa", name: "Cee" });
  window.Veil.live.height = 4800 * 11 + 3; // epoch 11: epoch 10's bids are on display
  _.set(state([
    slot(3, id("a"), 200000n, 10n, 5n), slot(4, id("d"), 900000n, 10n, 6n), slot(5, id("c"), 300000n, 10n, 7n), // d is not listed
    slot(0, id("a"), 150000n, 11n, 8n), slot(1, id("b"), 120000n, 11n, 9n)]));
  assert.deepEqual(P.slots(), [{ slot: 0, target: id("c"), bid: 300000n, epoch: 11n }, { slot: 1, target: id("a"), bid: 200000n, epoch: 11n }]);
  assert.deepEqual(P.statusOf(id("a")), { promoted: true, slot: 1, bid: 200000n, epoch: 11n, eligible: true, next: { rank: 0, bid: 150000n, epoch: 12n } });
  assert.deepEqual(P.statusOf(id("d")), { promoted: false, slot: null, bid: null, epoch: 11n, eligible: false, next: null });
  assert.deepEqual(P.statusOf(id("b")).next, { rank: 1, bid: 120000n, epoch: 12n });
  assert.equal(P.statusOf("not an id"), null);
  // two bids for one target: shown once, at its best rank
  _.set(state([slot(3, id("a"), 200000n, 10n, 5n), slot(4, id("a"), 400000n, 10n, 6n, false, "dero1other")]));
  assert.deepEqual(P.slots().map((x) => [x.slot, x.bid]), [[0, 400000n]]);
  // no verified contract: nothing, and statusOf says so with null
  _.set(null);
  assert.deepEqual(P.slots(), []); assert.equal(P.statusOf(id("a")), null);
});

test("parse keeps only well-formed slots", () => {
  const raw = "02" + "ab".repeat(32);
  const vals = { tg_0: id("a"), bd_0: "100000", by_0: raw, ep_0: "10", sq_0: "0", cl_0: "0",
    tg_1: "abcd", bd_1: "5", by_1: raw, ep_1: "10", sq_1: "1", cl_1: "0", // a target that is not 32 bytes
    tg_2: id("b"), bd_2: "x", by_2: raw, ep_2: "10", sq_2: "2", cl_2: "0", // a bid that is not a number
    tg_3: id("c"), bd_3: "7", by_3: "zz", ep_3: "9", sq_3: "3", cl_3: "1", // a bidder that is not a key
    tg_4: id("e"), bd_4: "9", by_4: raw, ep_4: "9", sq_4: "4", cl_4: "1" };
  const got = _.parse(vals, 3);
  assert.deepEqual(got.map((x) => [x.i, x.bid, x.cl]), [[0, 100000n, false], [4, 9n, true]]);
});
