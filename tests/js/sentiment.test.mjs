// app/sentiment.js: the display rule (under N = 5 votes, counts only; from N, percentages that add up to 100 and never
// round a side that has votes to 0%, nor the other to 100%), and the fail-closed reads: no contract, a bad id, a
// contract whose code is not the shipped template, a wrong kind or version, or a node that does not answer all give
// null, never a count.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const SRC = readFileSync(join(ROOT, "app", "sentiment.js"), "utf8");
const TPL = "Function Vote(target String, v Uint64) Uint64\n10 RETURN 0\nEnd Function\n";
const RATE = "e".repeat(64), MKT = "a".repeat(64), ME = "dero1qmeqmeqme";
const hexOf = (s) => Buffer.from(s).toString("hex");

// Loads sentiment.js as the browser does, against a node stub that answers DERO.GetSC the way rpc.js normalises it.
function load({ rate = RATE, code = TPL, tpl = TPL, kind = "rate", v = "1", fee = "10000", vals = {}, canon = null, fail = false } = {}) {
  const calls = [];
  const node = {
    async getSC(scid, opt) {
      calls.push({ scid, opt });
      if (fail) throw new Error("no node");
      const all = Object.assign({ kind: hexOf(kind), v, fee }, vals), out = {};
      for (const k of opt.keys || []) if (all[k] !== undefined) out[k] = all[k];
      return { vals: out, code: opt.code ? code : "" };
    },
  };
  const window = { Veil: { data: { cfg: { rate }, onChange() {} }, node, wallet: { canon },
    hexToStr: (h) => Buffer.from(h, "hex").toString(), sha256Hex: async (s) => createHash("sha256").update(s).digest("hex") } };
  const fetch = async () => ({ ok: !!tpl, text: async () => tpl });
  new Function("window", "fetch", SRC)(window, fetch);
  return { S: window.Veil.sentiment, calls };
}

test("under 5 votes only the counts are shown", () => {
  const { S } = load();
  assert.equal(S.N, 5);
  for (let b = 0; b < 5; b++) for (let s = 0; b + s < 5; s++) {
    const m = S.summary(b, s);
    assert.deepEqual([m.bull, m.sus, m.n, m.pct], [b, s, b + s, null], `${b} bullish, ${s} suspicious`);
  }
  // one address is never a landslide
  assert.equal(S.summary(0, 1).pct, null);
  assert.equal(S.summary(0, 4).pct, null);
});

test("from 5 votes the percentages add up to 100, and a side with votes never reads 0% or its complement 100%", () => {
  const { S } = load();
  assert.deepEqual(S.summary(5, 0).pct, { bull: "100%", sus: "0%", bw: 100 });
  assert.deepEqual(S.summary(0, 5).pct, { bull: "0%", sus: "100%", bw: 0 });
  assert.deepEqual(S.summary(4, 1).pct, { bull: "80%", sus: "20%", bw: 80 });
  assert.deepEqual([S.summary(999, 1).pct.bull, S.summary(999, 1).pct.sus], [">99%", "<1%"]);
  assert.deepEqual([S.summary(1, 999).pct.bull, S.summary(1, 999).pct.sus], ["<1%", ">99%"]);
  for (let n = 5; n <= 400; n++) for (let b = 0; b <= n; b++) {
    const p = S.summary(b, n - b).pct, s = n - b;
    assert.ok(p, `${b}/${n} shows no percentage`);
    if (b > 0) assert.notEqual(p.bull, "0%"); if (s > 0) assert.notEqual(p.bull, "100%");
    if (s > 0) assert.notEqual(p.sus, "0%"); if (b > 0) assert.notEqual(p.sus, "100%");
    if (/^\d+%$/.test(p.bull) && /^\d+%$/.test(p.sus)) assert.equal(parseInt(p.bull) + parseInt(p.sus), 100, `${b}/${n}`);
    assert.ok(Math.abs(p.bw - (100 * b) / n) < 1e-9);
  }
  // nonsense in, nothing out of range
  assert.deepEqual(S.summary(-3, "x"), { bull: 0, sus: 0, n: 0, pct: null });
});

test("of() reads the two counters, and the wallet's own vote only with a wallet", async () => {
  let { S, calls } = load({ vals: { ["b_" + MKT]: "3", ["s_" + MKT]: "2" } });
  assert.deepEqual(await S.of(MKT), { bull: 3, sus: 2, n: 5 });
  assert.equal(calls.filter((c) => c.opt.code).length, 1, "the code is checked once");
  assert.deepEqual(await S.of(MKT.toUpperCase()), { bull: 3, sus: 2, n: 5 }, "one market, one spelling");
  assert.equal(calls.filter((c) => c.opt.code).length, 1, "and not again");
  assert.deepEqual(await S.of("b".repeat(64)), { bull: 0, sus: 0, n: 0 }, "a market nobody voted on");
  ({ S } = load({ canon: ME, vals: { ["b_" + MKT]: "1", ["x_" + MKT + "_" + ME]: "1" } }));
  assert.deepEqual(await S.of(MKT), { bull: 1, sus: 0, n: 1, mine: 1 });
  ({ S } = load({ canon: ME, vals: { ["x_" + MKT + "_" + ME]: "7" } }));
  assert.equal((await S.of(MKT)).mine, 0, "a vote the contract cannot hold reads as none");
  assert.equal(S.fee, 10000n);
});

test("every doubt reads as null, never as a count", async () => {
  const cases = {
    "no rate contract in this build": { rate: "" },
    "a rate id that is not 64 hex": { rate: "E".repeat(64) },
    "code that is not the shipped template": { code: TPL + "20 RETURN 1\n" },
    "no code at the id": { code: "" },
    "a template the bundle could not serve": { tpl: "" },
    "another kind": { kind: "chat" },
    "another version": { v: "2" },
    "a fee that is not a number": { fee: "abc" },
    "a node that does not answer": { fail: true },
  };
  for (const [why, opt] of Object.entries(cases)) {
    const { S } = load(Object.assign({ vals: { ["b_" + MKT]: "3" } }, opt));
    assert.equal(await S.of(MKT), null, why);
    assert.equal(S.verified, "", why);
  }
  const { S, calls } = load();
  for (const bad of ["", "a".repeat(63), "a".repeat(65), "g".repeat(64), null, undefined]) assert.equal(await S.of(bad), null, String(bad));
  assert.equal(calls.length, 0, "a bad id reaches no node");
});

test("the words: many addresses, a count and not a verdict, no warning colour", () => {
  const { S } = load();
  assert.equal(S.MANY, "Anyone can vote, including from many addresses.");
  assert.equal(S.BULL, "Bullish");
  assert.equal(S.SUS, "Suspicious / Scam");
  assert.match(SRC, /not DMZ's verdict/);
  assert.match(SRC, /your address is public in that transaction/);
  assert.match(SRC, /Percentages are shown from/);
  for (const m of SRC.matchAll(/class: "([^"]*)"/g)) assert.doesNotMatch(m[1], /down|err|warn|bad|scam|danger/i, "the widget's class " + m[1]);
  assert.doesNotMatch(SRC, /SCAM/);
  const css = readFileSync(join(ROOT, "app", "parts.css"), "utf8"), i = css.indexOf("/* ==== sentiment ==== */");
  assert.ok(i >= 0, "parts.css has the sentiment section");
  assert.doesNotMatch(css.slice(i).replace(/\/\*[\s\S]*?\*\//g, ""), /--down|--warn|errbox/, "the sentiment section uses no warning ink");
});
