// What the app says about the Founders Pass fee share, and what it computes as a holder's reward.
//
// UX-03: three screens said the share was stored in the contract and covered every market. app/notice.js — the
// page this app declares authoritative — says a bot deposits it, no contract forces the deposit, and fees earned
// in a wrapped asset reach no holder. The contract stores the *number* (founders.bas `share`, bps, immutable),
// never a duty to deposit, and tools/treasurybot walks pool_ and launch_ only. This half of the file pins the
// screens to notice.js by string, because app copy has no other test.
//
// INT-01: app/founders.js computed a holder's pending reward as one term, hu·(acc-hd)/2^32. founders.bas settle()
// (lines 263-288) sums one term per era the holder has not been settled through and then the current one, because
// distribute() restarts acc at 0 when a deposit would wrap it past 2^64. The numbers below are the ones the DVM
// printed in tests/zzfoundersapp_test.go: after the roll era=1, acc=ea_0=12884901888000000000, rpool=9000000000,
// and a holder whose hd_ is the pre-roll accumulator was paid 3000000000 while the page showed 0.
//
//   node --test tests/js/fees.repair.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const app = (f) => readFileSync(join(ROOT, "app", f), "utf8");
const appFiles = readdirSync(join(ROOT, "app")).filter((f) => f.endsWith(".js"));

// ---- UX-03: no screen may contradict notice.js -------------------------------------------------------------

// Each sentence is a claim the mechanism cannot support: the contract holds the number, not the promise, and the
// bot sweeps two registry lists, not every market.
const FORBIDDEN = [
  "every protocol fee, split",
  "claimed from every market",
  "% of fees",
  "The promise is stored in the contract",
];

test("no screen outside notice.js claims the fee share is enforced or universal", () => {
  for (const f of appFiles) {
    if (f === "notice.js") continue; // notice.js is the page the app declares true; it is checked below
    const src = app(f);
    for (const s of FORBIDDEN) {
      assert.ok(!src.includes(s), `app/${f} says "${s}" — app/notice.js says the opposite`);
    }
  }
});

test("the screens that state the founders share name the asset it is a share of", () => {
  assert.match(app("nfts.js"), /% of the DERO trading fees the treasury claims, split between registered holders/);
  assert.match(app("nfts.js"), /The share is fixed in the contract and cannot be changed/);
  assert.match(app("nfts.js"), /Fees earned in a wrapped asset, and fees from NFA sales, reach no holder/);
  assert.match(app("home.js"), /% of claimed DERO fees/);
});

test("notice.js still says what the other screens are being held to", () => {
  const src = app("notice.js");
  assert.ok(src.includes("The treasury's own bot makes that deposit; no contract forces it."));
  assert.ok(src.includes("Fees earned in a wrapped asset reach no holder today."));
  assert.ok(src.includes("of claimed DERO fees to Pass holders; wrapped-asset fees to none"));
});

// ---- INT-01: myFounders against settle() -------------------------------------------------------------------

// app/founders.js is an IIFE that assigns to window.Veil.data, like every other file in app/. Nothing here is a
// dependency: the shim is the handful of helpers myFounders and loadFounders reach for.
function loadFounders(state, keys) {
  const window = { Veil: null };
  const V = {
    ui: { clean: (s, n) => String(s || "").slice(0, n) },
    hexToStr: (h) => Buffer.from(String(h || ""), "hex").toString("utf8"),
    canonAddress: (a) => a || null,
    node: { getSC: async () => ({ vals: state }) },
    data: {
      cfg: { founders: "f".repeat(64) },
      founders: null,
      async verify() { return "founders"; },
      rawToAddr: (r) => r || null,
      async fetchKeys(_scid, ks) { const out = {}; for (const k of ks) if (keys[k] !== undefined) out[k] = keys[k]; return out; },
      async myKeys(_scid, addr, prefixes) {
        const out = {};
        for (const p of prefixes) { const v = keys[p + addr]; out[p.replace(/_$/, "")] = v === undefined ? null : BigInt(v); }
        return out;
      },
    },
  };
  window.Veil = V;
  new Function("window", app("founders.js"))(window);
  return V.data;
}

// the pass exactly as the DVM left it after the era roll
const ROLLED = {
  price: "0", maxsupply: "20", start: "0", reserve: "0", share: "5000", mfee: "100", minted: "2",
  hst: "2", acc: "12884901888000000000", rpool: "9000000000", und: "0", dep: "12000000000", mdep: "0",
  paid: "3000000000", hn: "2", ln: "0", la: "0", era: "1",
  nameHdr: Buffer.from("Founders Pass").toString("hex"), descrHdr: "", iconURLHdr: "", fileURL: "",
  creator: "deto1creator", treasury: "deto1treasury",
};
const HOLDER_KEYS = {
  ea_0: "12884901888000000000",
  // a claimed once, in era 0: its hd_ is the accumulator from before the roll
  "hu_deto1a": "1", "hd_deto1a": "12884901888000000000", "he_deto1a": "0",
  // b has never claimed
  "hu_deto1b": "1", "hd_deto1b": "0", "he_deto1b": "0",
};

test("loadFounders reads the era counter", async () => {
  const D = loadFounders(ROLLED, HOLDER_KEYS);
  const f = await D.loadFounders(null);
  assert.equal(f.era, 1n, "without `era` the page cannot know an era closed");
  assert.equal(f.acc, 12884901888000000000n);
});

test("myFounders pays what the contract paid across an era roll", async () => {
  const D = loadFounders(ROLLED, HOLDER_KEYS);
  D.founders = await D.loadFounders(null);
  const a = await D.myFounders("deto1a");
  // settle(): (ea_0 - hd)/2^32 = 0, then hd = 0 and acc/2^32 = 3000000000 — what Claim paid on the DVM
  assert.equal(a.pending, 3000000000n, "the holder is owed a full era the single-term formula could not see");
  assert.equal(a.units, 1n);
  const b = await D.myFounders("deto1b");
  // never settled: one term for the closed era, one for this one
  assert.equal(b.pending, 6000000000n);
});

test("myFounders is still capped by rpool and quiet for a stranger", async () => {
  const keys = { ...HOLDER_KEYS, "hu_deto1b": "10" }; // more units than the pool can cover
  const D = loadFounders(ROLLED, keys);
  D.founders = await D.loadFounders(null);
  assert.equal((await D.myFounders("deto1b")).pending, 9000000000n, "settle() pays at most rpool");
  assert.equal((await D.myFounders("deto1nobody")).pending, 0n);
});

test("myFounders is unchanged on a pass that has never rolled an era", async () => {
  const state = { ...ROLLED, era: "0", acc: "4294967296", rpool: "1000", paid: "0" };
  const keys = { "hu_deto1a": "3", "hd_deto1a": "0", "he_deto1a": "0" };
  const D = loadFounders(state, keys);
  D.founders = await D.loadFounders(null);
  assert.equal((await D.myFounders("deto1a")).pending, 3n);
});
