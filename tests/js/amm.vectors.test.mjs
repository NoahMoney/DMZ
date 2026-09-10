// Cross-language differential: every VECTOR line below was printed by the real DVM running contracts/pool.bas
// under tests/zzextreme_test.go and tests/pool_test.go. app/amm.js is the sheet the user reads and the source of
// the min_out the wallet signs, so a disagreement here is the page promising a fill the chain will not give.
// Regenerate with:
//   tools/withlock.sh dvm bash tests/run.sh -run "PoolFuzz|Extreme" -v | grep VECTOR
import { test } from "node:test";
import assert from "node:assert/strict";
import { A, B, rng, randU } from "./harness.mjs";

const U64 = (1n << 64n) - 1n, Z = 0n;

// --- vectors from the DVM ---------------------------------------------------------------------------------
const VECTORS = [
  // tests/zzextreme_test.go TestPoolExtremeReserves: fee 100 bps (the ceiling), reserves near 2^62
  {"op":"poolBuy","fee":100,"rd":"4611686018427387904","ra":"2305843009213693952","in":"1","minOut":"0","ok":false,"out":"0"},
  {"op":"poolBuy","fee":100,"rd":"4611686018427387904","ra":"2305843009213693952","in":"2","minOut":"0","ok":false,"out":"0"},
  {"op":"poolBuy","fee":100,"rd":"4611686018427387904","ra":"2305843009213693952","in":"99","minOut":"0","ok":true,"out":"48"},
  {"op":"poolBuy","fee":100,"rd":"4611686018427388002","ra":"2305843009213693904","in":"100","minOut":"0","ok":true,"out":"49"},
  {"op":"poolBuy","fee":100,"rd":"4611686018427388101","ra":"2305843009213693855","in":"101","minOut":"0","ok":true,"out":"49"},
  {"op":"poolBuy","fee":100,"rd":"4611686018427388200","ra":"2305843009213693806","in":"10000","minOut":"0","ok":true,"out":"4949"},
  {"op":"poolBuy","fee":100,"rd":"4611686018427398100","ra":"2305843009213688857","in":"1099511627776","minOut":"0","ok":true,"out":"544258127285"},
  {"op":"poolBuy","fee":100,"rd":"4611687106943909598","ra":"2305842464955561572","in":"2305843009213693952","minOut":"0","ok":true,"out":"763472802640241812"},
  {"op":"poolSell","fee":100,"rd":"6894471686065466610","ra":"1542369662315319760","in":"1","minOut":"0","ok":false,"out":"0"},
  {"op":"poolSell","fee":100,"rd":"6894471686065466610","ra":"1542369662315319760","in":"2","minOut":"0","ok":true,"out":"4"},
  {"op":"poolSell","fee":100,"rd":"6894471686065466606","ra":"1542369662315319761","in":"100","minOut":"0","ok":true,"out":"442"},
  {"op":"poolSell","fee":100,"rd":"6894471686065466164","ra":"1542369662315319860","in":"1073741824","minOut":"0","ok":true,"out":"4751684337"},
  {"op":"poolSell","fee":100,"rd":"6894471681313781827","ra":"1542369663378324265","in":"576460752303423488","minOut":"0","ok":true,"out":"1862056729909383954"},
  // tests/pool_test.go TestPoolFuzz, fee 50 bps: a drained pool and inputs at 2^60 scale
  {"op":"poolBuy","fee":50,"rd":"1","ra":"1435251552120627795","in":"679815","minOut":"0","ok":true,"out":"1435249430273196450"},
  {"op":"poolSell","fee":50,"rd":"970273804","ra":"643560024","in":"1438848673159967690","minOut":"0","ok":true,"out":"970273803"},
  {"op":"poolBuy","fee":50,"rd":"237973","ra":"514562751835676222","in":"3","minOut":"0","ok":true,"out":"4324510993471"},
  {"op":"poolBuy","fee":50,"rd":"237976","ra":"514558427324682751","in":"1","minOut":"0","ok":false,"out":"0"},
  {"op":"poolSell","fee":50,"rd":"237976","ra":"514558427324682751","in":"152131504932","minOut":"0","ok":false,"out":"0"},
  {"op":"poolSell","fee":50,"rd":"102523717112","ra":"1185733659839","in":"3","minOut":"1","ok":false,"out":"0"},
  {"op":"poolBuy","fee":50,"rd":"715142","ra":"2012251877500","in":"639770035611","minOut":"2012249616882","ok":true,"out":"2012249616882"},
  {"op":"poolSell","fee":50,"rd":"1656309730","ra":"873192333","in":"720419376350","minOut":"743724794258","ok":false,"out":"0"},
];

test("app/amm.js reproduces every DVM vector exactly, at 2^62-scale reserves and at the fee ceiling", () => {
  for (const v of VECTORS) {
    const s = { fee: B(v.fee), tax: Z, rd: B(v.rd), ra: B(v.ra), lp: 1n, phase: 1n, cs: Z, sold: Z, real: Z };
    const q = v.op === "poolBuy" ? A.poolBuy(s, B(v.in)) : A.poolSell(s, B(v.in));
    const min = B(v.minOut);
    const jsOK = !!q && q.out >= min;
    assert.equal(jsOK, v.ok, "ok mismatch for " + JSON.stringify(v) + " (js out=" + (q && q.out) + ")");
    if (v.ok) assert.equal(q.out.toString(), v.out, "out mismatch for " + JSON.stringify(v));
  }
});

// --- properties ------------------------------------------------------------------------------------------
test("bps and bpsUp are exact floor and ceil for every f <= 10000, including at MAX_UINT64", () => {
  const r = rng(0xbadf00d);
  const xs = [Z, 1n, 9999n, 10000n, 10001n, U64, U64 - 1n, 1n << 63n, 1n << 53n];
  for (let i = 0; i < 20000; i++) xs.push(randU(r, U64));
  for (const x of xs) {
    for (const f of [0n, 1n, 50n, 100n, 1000n, 5000n, 9999n, 10000n]) {
      const lo = A.bps(x, f), up = A.bpsUp(x, f);
      assert.equal(lo, (x * f) / 10000n, "bps(" + x + "," + f + ")");
      assert.equal(up, (x * f + 9999n) / 10000n, "bpsUp(" + x + "," + f + ")");
      assert.ok(lo <= U64 && up <= U64, "bps overflowed uint64 at (" + x + "," + f + ")");
      assert.ok(up <= x || x === Z, "bpsUp(" + x + "," + f + ") exceeds x");
    }
  }
});

test("mulDiv and mulDivUp refuse exactly what the contract muldiv panics on", () => {
  const r = rng(7);
  for (let i = 0; i < 20000; i++) {
    const a = randU(r, U64 + 1n), b = randU(r, U64 + 1n), c = randU(r, U64 + 1n);
    const lo = A.mulDiv(a, b, c), up = A.mulDivUp(a, b, c);
    if (c === Z) { assert.equal(lo, null); assert.equal(up, null); continue; }
    const want = (a * b) / c;
    if (want > U64) assert.equal(lo, null, "mulDiv should refuse " + a + "," + b + "," + c);
    else assert.equal(lo, want, "mulDiv " + a + "," + b + "," + c);
    const wantUp = (a * b + c - 1n) / c;
    if (wantUp > U64) assert.equal(up, null, "mulDivUp should refuse " + a + "," + b + "," + c);
    else assert.equal(up, wantUp, "mulDivUp " + a + "," + b + "," + c);
  }
});

test("poolBuy and poolSell never return zero, a negative, or more than the reserve, at any scale", () => {
  const r = rng(99);
  for (let i = 0; i < 30000; i++) {
    const s = { fee: [0n, 1n, 50n, 100n][i & 3], tax: Z, rd: randU(r, U64) + 1n, ra: randU(r, U64) + 1n, lp: 1n, phase: 1n, cs: Z, sold: Z, real: Z };
    const d = randU(r, U64) + 1n;
    const b = A.poolBuy(s, d);
    if (b) {
      assert.ok(b.out > Z && b.out < s.ra, "poolBuy out " + b.out + " against ra " + s.ra);
      assert.ok(b.fee <= d, "fee " + b.fee + " > input " + d);
      const m = A.minOut(b.out, 1000);
      assert.ok(m <= b.out && m >= Z, "minOut out of range");
      assert.ok(Number.isFinite(b.impact), "impact not finite at rd=" + s.rd + " ra=" + s.ra + " d=" + d);
    }
    const sl = A.poolSell(s, d);
    if (sl) {
      assert.ok(sl.out > Z && sl.out < s.rd, "poolSell out " + sl.out + " against rd " + s.rd);
      assert.ok(Number.isFinite(sl.impact), "sell impact not finite at rd=" + s.rd + " ra=" + s.ra + " a=" + d);
    }
  }
});

test("parseUnits and fmtUnits round trip every value at 0, 5, 8 and 12 decimals up to MAX_UINT64", () => {
  const r = rng(1234);
  const vals = [Z, 1n, 9n, 10n, 99999n, 100000n, 1n << 53n, (1n << 53n) + 1n, U64 - 1n, U64];
  for (let i = 0; i < 5000; i++) vals.push(randU(r, U64 + 1n));
  for (const dec of [0, 5, 8, 12]) {
    for (const v of vals) {
      const s = A.fmtUnits(v, dec);
      const back = A.parseUnits(s.replace(/,/g, ""), dec);
      assert.equal(back, v, "round trip failed at " + dec + " decimals for " + v + " via " + s);
    }
  }
});

test("minOut never exceeds the quote and never underflows, for every slippage the app allows", () => {
  const r = rng(5150);
  for (let i = 0; i < 20000; i++) {
    const out = randU(r, U64 + 1n);
    for (const slip of [0, 1, 50, 500, 1000]) {
      const m = A.minOut(out, slip);
      assert.ok(m >= Z, "minOut(" + out + "," + slip + ") = " + m + " is negative");
      assert.ok(m <= out, "minOut(" + out + "," + slip + ") = " + m + " exceeds " + out);
    }
  }
});
