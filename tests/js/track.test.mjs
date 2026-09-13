// app/track.js: the swap stage tracker (docs/SWAP_STATUS.md). The status answers a paired xmrswap or zecswap gives
// are checked field by field before anything is shown; these run the four fixtures (each program on its happy path
// and its refund path) through the same parse and the same words the screens use, and the refusals.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const window = { Veil: { ui: { clean: (s, n) => String(s || "").slice(0, n), h: () => ({}), button: () => ({}), toast: () => {} }, shortAddr: (a) => a.slice(0, 8) } };
new Function("window", readFileSync(join(ROOT, "app", "track.js"), "utf8"))(window);
const T = window.Veil.track;
const fx = (n) => JSON.parse(readFileSync(join(ROOT, "tests", "js", "fixtures", n + ".json"), "utf8"));
const XMR = { program: "xmrswap", network: "simulator", contracts: ["c".repeat(64)] }, ZEC = { program: "zecswap", network: "simulator", contracts: ["d".repeat(64)] };

test("xmrswap, happy path: a deposit with the XMR lock confirming, 3 of 10", () => {
  const r = T.parse(fx("status-xmrswap-happy"), XMR);
  assert.ok(r.ok, r.why);
  const s = r.s;
  assert.equal(s.direction, "deposit");
  assert.equal(T.dirText(s.direction, "1 XMR", "20 DERO"), "Deposit: you give 1 XMR and receive 20 DERO");
  assert.equal(s.lock, 3); assert.equal(s.dero, 2000000n); assert.equal(s.foreign, 1000000000000n); assert.equal(s.fee, 5000000000n);
  assert.equal(s.outcome, "");
  const c = T.current(s.steps);
  assert.equal(c.id, "xmr_lock"); assert.equal(c.chain, "Monero");
  assert.equal(T.conf(c), "3 of 10");
  assert.deepEqual(s.steps.map((x) => x.state), ["done", "done", "active", "waiting", "waiting", "waiting"]);
  // the active step has no deadline of its own, so the one that counts down is the next step's: the gate, by t1 − G
  assert.equal(T.nextDeadline(s.steps).id, "gate");
  // a DERO deadline counts down from the tip this app read, at 18 s a block
  assert.equal(T.left(s.steps[3].deadline, 6390, s.updated), "DERO block 6400 · in 10 blocks · about 3 min");
  assert.equal(T.left(s.steps[3].deadline, 6400, s.updated), "DERO block 6400 · passed");
  // with no tip read, the program's own estimate, said to be its estimate
  assert.equal(T.left(s.steps[3].deadline, 0, s.updated), "DERO block 6400 · about 1.5 h left, by your swap program's estimate");
});

test("xmrswap, refund path: a withdrawal refunded, which ends the polling", () => {
  const r = T.parse(fx("status-xmrswap-refund"), XMR);
  assert.ok(r.ok, r.why);
  assert.equal(r.s.direction, "withdrawal");
  assert.equal(T.dirText("withdrawal", "1 XMR", "20 DERO"), "Withdrawal: you give 20 DERO and receive 1 XMR");
  assert.equal(r.s.outcome, "refunded");
  assert.equal(T.current(r.s.steps), null);
  assert.equal(T.nextDeadline(r.s.steps), null); // an ended swap counts nothing down
  assert.deepEqual(r.s.steps.map((x) => x.state), ["done", "done", "skipped", "skipped", "skipped", "done"]);
  assert.equal(T.conf(r.s.steps[5]), "9 of 8");
});

test("zecswap, happy path: a withdrawal whose ZEC claim is confirming, with a Zcash deadline the app cannot count", () => {
  const r = T.parse(fx("status-zecswap-happy"), ZEC);
  assert.ok(r.ok, r.why);
  assert.equal(r.s.direction, "withdrawal"); assert.equal(r.s.role, "maker");
  const c = T.current(r.s.steps);
  assert.equal(c.id, "zec_claim"); assert.equal(T.conf(c), "3 of 10");
  assert.equal(T.left(c.deadline, 6390, r.s.updated), "Zcash block 3100096 · about 2 h left, by your swap program's estimate");
  assert.equal(T.left(c.deadline, 6390, Date.parse("2026-09-13T15:00:00Z")), "Zcash block 3100096 · passed, by your swap program's estimate");
});

test("zecswap, refund path: a deposit whose ZEC came back at cltv", () => {
  const r = T.parse(fx("status-zecswap-refund"), ZEC);
  assert.ok(r.ok, r.why);
  assert.equal(r.s.direction, "deposit"); assert.equal(r.s.outcome, "refunded");
  assert.equal(T.current(r.s.steps).id, "dero_refund"); // the maker's own refund is still to come; it is not the user's
  assert.equal(T.conf(r.s.steps[4]), "12 of 10");
});

test("an answer is refused whole when it is not what this build expects", () => {
  const base = () => fx("status-xmrswap-happy");
  const why = (mut, want = XMR) => { const j = base(); mut(j); const r = T.parse(j, want); assert.ok(!r.ok, "accepted: " + JSON.stringify(j).slice(0, 80)); return r.why; };
  assert.match(why((j) => { j.v = 2; }), /version 1/);
  assert.match(why((j) => { j.program = "zecswap"; }), /not xmrswap/);
  assert.match(why((j) => { j.direction = "withdrawal"; }), /role and direction/);
  assert.match(why((j) => { j.network = "mainnet"; }), /this build is for simulator/);
  assert.match(why((j) => { j.contract = "e".repeat(64); }), /does not name/);
  assert.match(why((j) => { j.outcome = "stolen"; }), /outcome/);
  assert.match(why((j) => { j.steps = {}; }), /steps/);
  // a field NAMED like a secret, anywhere in the answer, refuses it: no status answer carries one
  assert.match(why((j) => { j.secret = "00"; }), /never carries a secret/);
  assert.match(why((j) => { j.steps[0].preimage = "00"; }), /preimage/);
  assert.match(why((j) => { j.channelKey = "00"; }), /channelKey/);
  assert.match(why((j) => { j.steps[1].spend_key = "00"; }), /spend_key/);
  assert.equal(T.parse(null, XMR).ok, undefined);
  assert.equal(T.parse([], XMR).ok, undefined);
});

test("a field that cannot be read is shown as unreadable, never as a value", () => {
  const j = fx("status-xmrswap-happy");
  j.deroAmount = "12.5"; j.fee = -1; j.steps[0].state = "exploded"; j.steps[1].conf = "lots"; j.steps[2].txid = "not a txid"; j.steps[3].deadline = { chain: "Bitcoin", height: 1 };
  const r = T.parse(j, XMR);
  assert.ok(r.ok, r.why);
  assert.equal(r.s.dero, null); assert.equal(r.s.fee, null);
  assert.equal(r.s.steps[0].state, "unreadable");
  assert.equal(r.s.steps[1].conf, null); assert.equal(T.conf(r.s.steps[1]), "");
  assert.equal(r.s.steps[2].txid, "");
  assert.equal(r.s.steps[3].deadline, null);
});
