// app/move.js: the Deposit & withdraw guide for each vault and on a build with none, the stages its tracker draws
// from the vault contract's own records (contracts/vault.bas), and app/wrapkit.js's failure words.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, A } from "./harness.mjs";

const noop = () => ({});
const XMRV = { scid: "e".repeat(64), name: "Monero", sym: "XMR", chain: "Monero — simulated on this network", dec: 12, conf: 10 };
const ZECV = { scid: "8".repeat(64), name: "Zcash", sym: "ZEC", chain: "Zcash — simulated on this network", dec: 8, conf: 6 };
const netOf = (a) => { const s = (a.sym + " " + a.chain).toUpperCase(); return /XMR|MONERO/.test(s) ? ["XM", "net xmr"] : /ZEC|ZCASH/.test(s) ? ["ZC", "net zec"] : ["??", "net"]; };
const window = { Veil: { amm: A, shortAddr: (a) => a, wallet: {}, wrapui: { netOf },
  ui: { clean: (s, n) => String(s || "").slice(0, n), h: noop, render: noop, button: noop, kv: noop, details: noop, sectionHead: noop, emptyState: noop, skeleton: noop, router: { on() {}, current: null }, openSheet: noop, closeSheet: noop, toast: noop },
  data: { cfg: { network: "simulator" }, vaults: () => [XMRV, ZECV], allVaults: () => [XMRV, ZECV], list: () => [] } } };
for (const f of ["wrapkit.js", "track.js", "move.js"]) new Function("window", "document", readFileSync(join(ROOT, "app", f), "utf8"))(window, { getElementById: () => null, querySelector: () => null });
const M = window.Veil.move, K = window.Veil.wrapkit, T = window.Veil.track;
// the simulator's vaults as read on 2026-09-15: 2 of 3 signers, a cap of 100,000 base units at a ratio of 3 against 0.6 DERO of bonds
const W = { k: 2n, na: 3n, n: 3n, sup: 50000n, cap: 100000n, headroom: 50000n, capratio: 3n, bondtot: 60000n, mindep: 1n, regfee: 0n, wfee: 100n, minburn: 1000n, wdelay: 0n, bigbps: 0n, bigdelay: 0n, halted: 0n, sealed: 1n, viewkey: "", winmax: 0n };
const text = (g) => JSON.stringify(g);

test("the guides: five steps to deposit and four to withdraw, each with who acts, on which chain, in the vault's own numbers", () => {
  for (const [coin, rec] of [["xmr", XMRV], ["zec", ZECV]]) {
    const d = M.guide(coin, "deposit", rec, W, 100), w = M.guide(coin, "withdraw", rec, W, 100);
    assert.equal(d.available, true); assert.equal(d.steps.length, 5); assert.equal(w.steps.length, 4);
    assert.match(d.steps[0].t, /^Register a deposit index$/);
    assert.match(d.steps[1].t, /^2 of the 3 signers publish your deposit address$/);
    assert.match(d.steps[4].t, /2 of the 3 signers vote Mint: w(XMR|ZEC) arrives in your wallet/);
    assert.match(w.steps[0].t, /^Burn w(XMR|ZEC) with your (Monero|Zcash) address$/);
    assert.match(w.steps[3].t, /Released$/); assert.match(w.steps[3].d, /Reclaim/);
    assert.deepEqual(d.steps.map((s) => s.chain), ["dero", "dero", coin, coin, "dero"]);
    assert.deepEqual(w.steps.map((s) => s.chain), ["dero", "dero", coin, "dero"]);
    assert.ok([...d.steps, ...w.steps].every((s) => s.who && s.t && s.d.length > 20));
    assert.match(d.lead, /1:1, with no fee on the mint/);
    assert.match(d.steps[3].t, new RegExp("^" + rec.conf + " confirmations on (Monero|Zcash)$"));
    // no size limit per deposit, and no word of a swap anywhere
    assert.match(text(d.fees), /no limit per deposit/); assert.match(text(w.fees), /no limit per burn/);
    for (const g of [d, w]) { assert.doesNotMatch(text(g), /\bswap/i); assert.doesNotMatch(text(g), /trustless/i); assert.doesNotMatch(text(g), /undefined|NaN/); }
    assert.equal(d.details.length, 5); assert.match(text(d.details), new RegExp(rec.scid));
  }
});

test("custody in plain words: who holds the reserve, whether it can be checked, and that the bonds bound the cap", () => {
  const c = text(M.custody("xmr", XMRV, W, 100));
  assert.match(c, /2 of the 3 named signers hold the reserve and can move all of it/);
  assert.match(c, /No viewing key is published yet/);
  assert.match(c, /a small bond means a small cap/); assert.match(c, /Room to mint now: 0\.00000005 XMR/);
  assert.match(text(M.custody("xmr", XMRV, { ...W, viewkey: "vk" }, 100)), /anyone can check that the reserve on Monero covers it/);
  // bonds that no longer cover cap / capratio stop every mint (vault.bas Mint line 42), and it says so
  assert.match(text(M.custody("zec", ZECV, { ...W, bondtot: 10n }, 100)), /nothing can be minted until the signers bond more/);
  assert.match(text(M.custody("zec", ZECV, { ...W, halted: 500n }, 100)), /halted until block 500/);
  assert.match(text(M.custody("zec", ZECV, { ...W, sealed: 0n }, 100)), /NOT sealed/);
  // before the vault is read, general words and no invented number
  const g = M.guide("xmr", "deposit", XMRV, null, 0);
  assert.equal(g.available, true); assert.match(g.steps[1].t, /k of the n signers/); assert.doesNotMatch(text(g), /undefined|NaN|null/);
});

test("a build with no vault: deposits and withdrawals are unavailable, and nothing mentions a swap", () => {
  for (const dir of ["deposit", "withdraw"]) {
    const g = M.guide("xmr", dir, null, null, 0);
    assert.equal(g.available, false); assert.equal(g.why, "Deposits and withdrawals of XMR and ZEC are not available on this network.");
  }
  assert.doesNotMatch(M.NODEP, /swap/i);
});

test("a deposit index's stages are the ones the vault records; the foreign payment and the mint are marked not visible", () => {
  const pend = M.depositStages({ j: 4, addr: "", since: null, ver: 0, votes: 1 }, "2", "xmr");
  assert.deepEqual(pend.map((s) => s.state), ["done", "active", "waiting", "waiting"]);
  assert.equal(T.conf(pend[1]), "1 of 2"); // this window's attestation votes (vault.bas votek)
  assert.equal(M.depositStages({ j: 4, addr: "", since: null, ver: 0, votes: null }, "2", "xmr")[1].conf, null); // unread is not 0
  const live = M.depositStages({ j: 4, addr: "sim:4", since: 93, ver: 1, votes: null }, "2", "zec");
  assert.deepEqual(live.map((s) => s.state), ["done", "done", "unseen", "unseen"]);
  assert.match(live[1].label, /live since block 93/);
  assert.deepEqual(live.map((s) => s.chain), ["DERO", "DERO", "Zcash", "DERO"]);
  assert.deepEqual(live.map((s) => s.short), ["Registered", "Address", "Your ZEC", "Mint"]);
});

test("a withdrawal's stages: burned, release height, marked, then Released or returned, with the height each waits for", () => {
  const x = { j: 0, units: 900n, fee: 100n, dest: "44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP3A", burned: 100, rel: 160, st: 0, ftx: "", marked: 0 };
  const states = (o, tip) => M.withdrawalStages({ ...x, ...o }, "2", tip, "xmr").map((s) => s.state);
  assert.deepEqual(states({}, 120), ["done", "active", "waiting", "waiting", "waiting"]);
  assert.equal(M.withdrawalStages(x, "2", 120, "xmr")[1].deadline.height, 160);
  assert.match(T.due(M.withdrawalStages(x, "2", 120, "xmr"), 120), /^Next deadline: Release · DERO block 160 · in 40 blocks/);
  assert.deepEqual(states({}, 170), ["done", "done", "active", "waiting", "waiting"]);
  assert.equal(M.withdrawalStages(x, "2", 170, "xmr")[4].deadline.height, 160 + M.RECLAIM_AFTER); // Reclaim: rel + 20000, unmarked only
  assert.deepEqual(states({ marked: 165 }, 170), ["done", "done", "done", "waiting", "waiting"]);
  assert.equal(M.withdrawalStages({ ...x, marked: 165 }, "2", 170, "xmr")[4].deadline.height, 165 + 2000); // a Veto waits out the mark
  assert.deepEqual(states({ st: 1, ftx: "ab".repeat(32), marked: 165 }, 400), ["done", "done", "done", "done", "skipped"]);
  assert.deepEqual(states({ st: 2 }, 30000), ["done", "done", "skipped", "skipped", "done"]);
  assert.equal(M.withdrawalStages({ ...x, st: 1 }, "2", 400, "xmr")[4].deadline, null); // an ended withdrawal counts nothing down
});

test("every failure in words: what happened, what to do, and what was reported behind it", () => {
  const e = (raw, t) => K.explain(raw, t);
  assert.match(e("Cancelled in wallet").what, /cancelled it in your wallet/);
  assert.match(e("Burn 0.1 XMR and withdraw was included but did not execute: only the network fee was spent").what, /contract refused it/);
  assert.match(e("Refusing: the node estimated a fee of 1 DERO (limit 0.5 DERO)").what, /above this app's limit/);
  assert.match(e("Wallet: wallet disconnected").what, /No wallet is connected/);
  assert.match(e("Register a deposit index would fail: SC not found").what, /would fail/);
  assert.match(e("Failed to fetch").what, /did not answer/);
  assert.match(e("The operation was aborted due to timeout").what, /too long/);
  assert.match(e("A transaction is still waiting for the wallet's answer: check the wallet before sending another").what, /still waiting/);
  assert.match(e("Burn is not in a block after 4.5 minutes: check the wallet history before sending it again").todo, /history/);
  const x = e("error 0x2837", "Burn");
  assert.equal(x.title, "Burn"); assert.match(x.what, /no words for/); assert.equal(x.detail, "error 0x2837");
  assert.match(e("").what, /not sent, or it did not complete/); assert.equal(e("").detail, "");
  for (const r of ["Cancelled in wallet", "Failed to fetch", "error 0x2837", ""]) assert.ok(e(r).todo.length > 30, r);
  assert.equal(e("x".repeat(900)).detail.length, 400); // what was reported is kept, bounded
});
