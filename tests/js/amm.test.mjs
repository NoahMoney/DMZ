// The app's BigInt quote layer against the contract's own numbers.
//
// app/amm.js is the third implementation of the AMM maths. The contract and reference/*.go are fuzzed against
// each other in tests/*_test.go; nothing had ever executed amm.js outside a browser. Three checks: pool swaps
// against VECTOR lines TestPoolFuzz printed from the chain (vectors.json), launch buys and sells against
// reference/launch.go transcribed below and walked along a curve through graduation, and priceOf at the three
// quote decimals this exchange meets — DERO 5, ZEC 8, XMR 12.
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { A, B, rng, randU } from "./harness.mjs";

const Z = 0n, TEN4 = 10000n, U64 = (1n << 64n) - 1n;

// ---- pool.bas: what the chain paid ----
const vectors = JSON.parse(readFileSync(new URL("./vectors.json", import.meta.url), "utf8")).filter((v) => v.op);
// amm.js's impact() divides by the spot price at 18 decimals, and rd*1e18 < ra floors that to zero: poolBuy throws
// a RangeError on a pool the contract quotes without complaint (the fuzz drained one to rd=1 against 1.4e18
// tokens). Vectors on that side are asserted under todo until app/amm.js guards the division; the rest are hard.
const spotFloorsToZero = (v) => B(v.rd) * 10n ** 18n < B(v.ra);

function checkVectors(vs) {
  let paid = 0, refused = 0;
  for (const v of vs) {
    const s = { rd: B(v.rd), ra: B(v.ra), fee: B(v.fee) }, amt = B(v.in);
    const q = v.op === "poolBuy" ? A.poolBuy(s, amt) : A.poolSell(s, amt);
    const tag = `${v.op}(rd=${v.rd} ra=${v.ra} in=${v.in} minOut=${v.minOut})`;
    if (v.ok) {
      assert.ok(q, `${tag}: the contract paid ${v.out}, amm.js quoted nothing`);
      assert.equal(q.out, B(v.out), `${tag}: the contract paid ${v.out}, amm.js quoted ${q.out}`);
      paid++;
    } else {
      // The contract refused: under that min_out the mirror must not show a number the chain would not pay.
      assert.ok(q === null || q.out < B(v.minOut), `${tag}: the contract refused, amm.js quoted ${q && q.out}`);
      refused++;
    }
  }
  return { paid, refused };
}

test("pool swaps quote what the contract paid (vectors.json, from TestPoolFuzz)", () => {
  assert.ok(vectors.length >= 20, "an empty vectors.json asserts nothing");
  const { paid, refused } = checkVectors(vectors.filter((v) => !spotFloorsToZero(v)));
  assert.ok(paid >= 10 && refused >= 5, `both outcomes must be covered (paid ${paid}, refused ${refused})`);
});

test("pool swaps quote a pool whose spot price floors to zero at 18 decimals", () => {
  const vs = vectors.filter(spotFloorsToZero);
  assert.ok(vs.length > 0, "no vector on that side of the price");
  checkVectors(vs);
});

// ---- launch.bas: reference/launch.go, transcribed ----
// No launch vectors come out of the pool fuzz and tests/launch_test.go prints none, so the second implementation
// stands in for the chain: the Go model is fuzzed against launch.bas by TestLaunchFuzz, and these are its Buy and
// Sell in BigInt, line for line, minus the min_out and stray-asset checks the sheet never sends. The fee split
// is kept so the state after each step is the contract's state, not an approximation of it.
const bps = (x, f) => (x / TEN4) * f + ((x % TEN4) * f) / TEN4;
const bpsUp = (x, f) => { const r = bps(x, f); return ((x % TEN4) * f) % TEN4 === Z ? r : r + 1n; };
const mulDiv = (a, b, c) => { if (c === Z) return null; const q = (a * b) / c; return q > U64 ? null : q; };
const mulDivUp = (a, b, c) => { if (c === Z) return null; const p = a * b; let q = p / c; if (p % c !== Z) q += 1n; return q > U64 ? null : q; };

function newLaunch(p) { // NewLaunch
  const { supply, grad, cbps, tax, tmode, fee, pfee } = p;
  if (supply < 1000000n || supply >= 1n << 63n || grad < 1000000n || grad > 1000000000000n) return null;
  if (cbps > 500n || tax > 1000n || tmode > 3n || (tmode === Z && tax !== Z) || (tmode !== Z && tax === Z) || fee > 100n || pfee > 10000n) return null;
  const alloc = bps(supply, cbps), rest = supply - alloc, lq = rest / 5n, c = rest - lq, vd0 = grad / 3n;
  const vt0 = mulDiv(grad + vd0, c, grad);
  return vt0 === null ? null : { fee, pfee, tax, c, lq, phase: Z, sold: Z, real: Z, rd: vd0, ra: vt0 };
}
function refBuy(l, d) { // Buy
  if (d === Z) return null;
  let fee = bpsUp(d, l.fee), tx = bps(d, l.tax);
  if (fee + tx >= d) return null;
  let deff = d - fee - tx, used = d;
  if (l.phase === Z) {
    const rem = l.c - l.sold;
    if (rem === Z) return null;
    let out = mulDiv(deff, l.ra, l.rd + deff);
    if (out === null) return null;
    if (out >= rem) {
      out = rem;
      const cost = mulDivUp(rem, l.rd, l.ra - rem);
      if (cost === null || cost > deff) return null;
      if (cost !== deff) {
        const u = mulDivUp(cost, TEN4, TEN4 - l.fee - l.tax);
        if (u === null) return null;
        used = u + 1n > d ? d : u + 1n;
        fee = bpsUp(used, l.fee); tx = bps(used, l.tax); deff = used - fee - tx;
        if (deff < cost) return null;
      }
    }
    if (out === Z) return null;
    l.real += deff; l.rd += deff; l.ra -= out; l.sold += out;
    const graduates = out === rem;
    if (graduates) { l.phase = 1n; l.rd = l.real; l.ra = l.lq; }
    return { out, refund: d - used, graduates };
  }
  const out = mulDiv(deff, l.ra, l.rd + deff);
  if (out === null || out === Z) return null;
  l.rd = l.rd + deff + fee - bps(fee, l.pfee); l.ra -= out;
  return { out, refund: Z, graduates: false };
}
function refSell(l, a) { // Sell
  if (a === Z) return null;
  if (l.phase === Z) {
    if (a > l.sold) return null;
    const dout = mulDiv(a, l.rd, l.ra + a);
    if (dout === null || dout > l.real) return null;
    const fee = bpsUp(dout, l.fee), tx = bps(dout, l.tax);
    if (fee + tx >= dout) return null;
    l.real -= dout; l.rd -= dout; l.ra += a; l.sold -= a;
    return { out: dout - fee - tx };
  }
  const fee = bpsUp(a, l.fee), aeff = a - fee;
  const dout = mulDiv(aeff, l.rd, l.ra + aeff);
  if (dout === null) return null;
  const out = dout - bps(dout, l.tax);
  if (out === Z) return null;
  l.ra = l.ra + a - bps(fee, l.pfee); l.rd -= dout;
  return { out };
}

// The curves tests/launch_test.go installs, the parameter floor, and one drawn at random, each walked for 300
// steps by a single trader: buys of every scale from a few units to twice the raise (so the capped last buy and
// its refund run), sells of what was bought and a few of more than that, and the walk carries on past
// graduation so the pool phase is covered from the state graduation actually left.
const DERO = 100000n;
const curves = [
  { supply: 1000000000n * DERO, grad: 500n * DERO, cbps: 300n, tax: 200n, tmode: 1n, fee: 50n, pfee: 10000n },
  { supply: 21000000n * DERO, grad: 10n * DERO, cbps: 0n, tax: 500n, tmode: 2n, fee: 50n, pfee: 5000n },
  { supply: 1000000n * DERO, grad: 20n * DERO, cbps: 500n, tax: 1000n, tmode: 3n, fee: 50n, pfee: 5000n },
  { supply: 1000000n, grad: 1000000n, cbps: 0n, tax: 0n, tmode: 0n, fee: 0n, pfee: 0n },
  (() => {
    const r = rng(7), tmode = randU(r, 4n);
    return { supply: randU(r, (1n << 63n) - 1000000n) + 1000000n, grad: randU(r, 1000000000000n - 1000000n) + 1000000n, cbps: randU(r, 501n), tax: tmode === Z ? Z : randU(r, 1000n) + 1n, tmode, fee: randU(r, 101n), pfee: randU(r, 10001n) };
  })(),
];
const snapshot = (l) => ({ rd: l.rd, ra: l.ra, fee: l.fee, tax: l.tax, phase: l.phase, cs: l.c, sold: l.sold, real: l.real });

function walk(params, seed, steps) {
  const r = rng(seed), l = newLaunch(params);
  assert.ok(l, `seed ${seed}: parameters the contract accepts`);
  const cover = { buys: 0, sells: 0, refused: 0, graduated: 0, pool: 0 }, refunds = [];
  let held = Z;
  for (let i = 0; i < steps; i++) {
    const s = snapshot(l), tag = `seed ${seed} step ${i} phase ${l.phase}`;
    if (l.phase === 1n) cover.pool++;
    if (held === Z || r() < 0.6) {
      const scale = [3n, params.grad / 100n, params.grad / 10n, params.grad, params.grad * 2n][Math.floor(r() * 5)];
      const amt = randU(r, scale) + 1n;
      const want = refBuy(l, amt), got = A.launchBuy(s, amt);
      assert.equal(got === null, want === null, `${tag}: launchBuy(${amt}) ${got ? "quoted " + got.out : "refused"}, the contract ${want ? "pays " + want.out : "refuses"}`);
      if (!want) { cover.refused++; continue; }
      assert.equal(got.out, want.out, `${tag}: launchBuy(${amt}) quoted ${got.out}, the contract pays ${want.out}`);
      assert.equal(got.graduates, want.graduates, `${tag}: launchBuy(${amt}) graduates`);
      if (want.graduates) { cover.graduated++; refunds.push({ tag, amt, got: got.refund, want: want.refund }); }
      held += want.out; cover.buys++;
    } else {
      const amt = l.phase === Z && r() < 0.1 ? held + 1n + randU(r, held) : randU(r, held) + 1n;
      const want = refSell(l, amt), got = A.launchSell(s, amt);
      assert.equal(got === null, want === null, `${tag}: launchSell(${amt}) ${got ? "quoted " + got.out : "refused"}, the contract ${want ? "pays " + want.out : "refuses"}`);
      if (!want) { cover.refused++; continue; }
      assert.equal(got.out, want.out, `${tag}: launchSell(${amt}) quoted ${got.out}, the contract pays ${want.out}`);
      held -= amt; cover.sells++;
    }
  }
  return { cover, refunds };
}

test("launch buys and sells quote what reference/launch.go pays, through graduation", () => {
  let refused = 0;
  curves.forEach((p, i) => {
    const { cover } = walk(p, 100 + i, 300);
    assert.equal(cover.graduated, 1, `curve ${i}: the walk must graduate exactly once (${JSON.stringify(cover)})`);
    assert.ok(cover.pool >= 50 && cover.buys >= 50 && cover.sells >= 20, `curve ${i}: thin coverage ${JSON.stringify(cover)}`);
    refused += cover.refused;
  });
  assert.ok(refused > 0, "no refusal was ever exercised");
});

test("the graduating buy's refund is the contract's refund", () => {
  const seen = curves.flatMap((p, i) => walk(p, 100 + i, 300).refunds);
  assert.ok(seen.length > 0, "no graduating buy was seen");
  for (const f of seen) assert.equal(f.got, f.want, `${f.tag}: launchBuy(${f.amt}).refund is ${f.got}, the contract sends back ${f.want}`);
});

// ---- priceOf: the display boundary, at the decimals this exchange meets ----
const close = (got, want, tag) => assert.ok(Math.abs(got - want) <= 1e-9 * Math.max(1, Math.abs(want)), `${tag}: ${got} vs ${want}`);

test("priceOf reads the quote's decimals off the market — DERO 5, ZEC 8, XMR 12", () => {
  // one whole token is a quarter of the quote coin, on each of the three quotes
  close(A.priceOf({ rd: 250000n, ra: 10000000n, dec: 6, quoteDec: 5 }), 0.25, "DERO");
  close(A.priceOf({ rd: 100000000n, ra: 400000n, dec: 5, quoteDec: 8 }), 0.25, "ZEC");
  close(A.priceOf({ rd: 3000000000000n, ra: 1200000n, dec: 5, quoteDec: 12 }), 0.25, "XMR");
  // F1: the same reserves read as a 5-decimal quote are ten million times the 12-decimal truth
  const rd = 103095n, ra = 9876543210n;
  close(A.priceOf({ rd, ra, dec: 5, quoteDec: 5 }), A.priceOf({ rd, ra, dec: 5, quoteDec: 12 }) * 1e7, "5 vs 12");
  // a point carries reserves, the market carries the decimals
  const m = { rd: 1000000000000n, ra: 400000n, dec: 5, quoteDec: 12 };
  close(A.priceOf(m, { rd: m.rd * 2n, ra: m.ra }), 2 * A.priceOf(m), "point");
  assert.equal(A.priceOf({ rd: 5n, ra: 0n, dec: 5, quoteDec: 5 }), 0, "no asset, no price");
  // a market without quoteDec is quoted in DERO
  close(A.priceOf({ rd, ra, dec: 5 }), A.priceOf({ rd, ra, dec: 5, quoteDec: 5 }), "default quote");
});

test("parseUnits and fmtUnits carry a 12-decimal amount without a float", () => {
  assert.equal(A.parseUnits("1.5", 12), 1500000000000n);
  assert.equal(A.parseUnits("0.0000000000019", 12), 1n, "extra digits truncate, never round up");
  assert.equal(A.parseUnits("1,234.5", 5), 123450000n);
  assert.equal(A.parseUnits("abc", 5), null);
  assert.equal(A.parseUnits(".", 5), null);
  assert.equal(A.fmtUnits(1n, 12), "0.000000000001");
  assert.equal(A.fmtUnits(123456789012345n, 12), "123.456789012345");
  assert.equal(A.fmtUnits(A.parseUnits("42.12345678", 8), 8), "42.12345678");
  assert.equal(A.fmtUnits(123456789n, 5, 2), "1,234.56", "display truncates too");
  assert.equal(A.minOut(1000000n, 50), 995000n, "0.5% slippage");
  assert.equal(A.minOut(1n, 50), 0n, "slippage rounds against the trader");
});
