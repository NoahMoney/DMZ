// What the app says about the Founders Pass fee share, and what it computes as a position's claim.
//
// UX-03: three screens once said the share was stored in the contract and covered every market. app/notice.js — the
// page this app declares authoritative — says a bot deposits it and no contract forces the deposit. The contract
// stores the *number* (founders.bas `share`, bps, immutable), never a duty to deposit, and tools/treasurybot walks
// pool_ and launch_ only. founders.bas v2 (2026-09-13) pays each fee in the asset it was taken in, so the v1 words
// ("DERO fees", "wrapped-asset fees to none") are gone everywhere. This half of the file pins the screens to
// notice.js by string, because app copy has no other test.
//
// The other half runs app/founders.js against a hand-built v2 pass: Claim(p, i, ka, kb) must be offered with the
// record indices founders.bas at() checks, found by binary search, and with the amount it pays, pu × (to − from).
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

// Each sentence is a claim the mechanism cannot support, or a v1 one v2 made false.
const FORBIDDEN = [
  "every protocol fee, split",
  "claimed from every market",
  "% of fees",
  "The promise is stored in the contract",
  "of claimed DERO fees",
  "of the DERO trading fees",
  "reach no holder today",
  "wrapped-asset fees to none",
  "other than DERO do not reach",
  "Push rewards",
  "Release waiting DERO",
];

test("no screen claims the fee share is enforced or universal, or repeats a v1 sentence", () => {
  for (const f of appFiles) {
    const src = app(f);
    for (const s of FORBIDDEN) {
      if (f === "notice.js" && s === "% of fees") continue;
      assert.ok(!src.includes(s), `app/${f} says "${s}" — app/notice.js says otherwise`);
    }
  }
});

test("the screens that state the founders share name the asset rule and the trust in the bot", () => {
  const nfts = app("nfts.js");
  assert.match(nfts, /% of the fees the treasury claims, each paid in the asset it was taken in, split between registered passes/);
  assert.match(nfts, /The share is fixed in the contract and cannot be changed/);
  assert.match(nfts, /makes every deposit, and no contract forces it to/);
  assert.match(nfts, /Fees from NFA sales reach no holder/);
  assert.match(nfts, /public on the DERO chain for good/);
  assert.match(nfts, /fresh Monero subaddress/);
  assert.match(nfts, /no contract forces it, and no swap fee is taken yet/);
  assert.doesNotMatch(nfts, /PayBatch|"Distribute"/);
  assert.match(app("home.js"), /% of claimed fees, paid in each fee's asset/);
});

test("notice.js says how fees reach holders, in the description's terms", () => {
  const src = app("notice.js");
  for (const s of [
    "each in the asset the fee was taken in, never converted to DERO",
    "DERO and DERO tokens are deposited into the pass contract, where each registered pass claims its part",
    "XMR and ZEC are paid by the treasury's bot to the payout address a holder stored",
    "The bot makes every deposit and every XMR and ZEC payment, and no contract forces either",
    "A deposit is shared by the passes registered when it is made",
    "While no pass is registered nothing is deposited",
    "no swap fee is taken yet",
  ]) assert.ok(src.includes(s), "notice.js does not say: " + s);
});

// ---- app/founders.js against a v2 pass ------------------------------------------------------------------

const hex = (s) => Buffer.from(s).toString("hex");
const TOKA = "a1".repeat(32), JUNK = "e5".repeat(32), DERO = "0".repeat(64);
const XMR = "8" + "9AbCdEfGhJ".repeat(9) + "kmno"; // 95 base58 characters, digits-only in hex for the first 60

// A pass after: p0 (alice, 2) opened at tick 1; a DERO deposit (record 0 at tick 1, ac 0 -> 100); p1 (alice, 1) at
// tick 2; a tokA deposit (record 0 at tick 2, ac 0 -> 7); p2 (bob) at tick 3; a DERO deposit (record 1 at tick 3,
// ac 100 -> 150); p1 withdrawn at tick 4; p3 (alice, 3) opened at tick 5 and already claimed DERO through 150; a junk
// token deposited as asset 2.
const STATE = {
  v: "2", price: "100000", maxsupply: "20", start: "0", reserve: "4", share: "5000", mfee: "250", minted: "20", hst: "6",
  ev: "5", pn: "4", an: "3", ln: "0", la: "0", mdep: "0",
  nameHdr: hex("DMZ Founders Pass"), descrHdr: "", iconURLHdr: "", fileURL: "", creator: hex("deto1creator"), treasury: hex("deto1treasury"),
};
const KEYS = {
  ["ai_" + DERO]: "1", ac_0: "150", au_0: "0", ap_0: "1200", ad_0: "1200", aw_0: "0", ar_0: "2",
  rt_0_0: "1", ra_0_0: "0", rt_0_1: "3", ra_0_1: "100",
  ["ai_" + TOKA]: "2", ac_1: "7", au_1: "0", ap_1: "21", ad_1: "21", aw_1: "0", ar_1: "1", rt_1_0: "2", ra_1_0: "0",
  ["ai_" + JUNK]: "3", ac_2: "999", ap_2: "999", ar_2: "1", rt_2_0: "5", ra_2_0: "0",
  po_0: hex("deto1a"), pu_0: "2", pt_0: "1", pc_0: "0",
  po_1: hex("deto1a"), pu_1: "1", pt_1: "2", pc_1: "4",
  po_2: hex("deto1b"), pu_2: "3", pt_2: "3", pc_2: "0",
  po_3: hex("deto1a"), pu_3: "3", pt_3: "5", pc_3: "0", pd_0_3: "150",
  hu_deto1a: "5", xm_deto1a: hex(XMR),
};

function loadFounders(state, keys) {
  const window = { Veil: null }, all = { ...state, ...keys }, asked = [];
  const pick = (ks) => { const out = {}; for (const k of ks) { asked.push(k); if (all[k] !== undefined) out[k] = all[k]; } return out; };
  const V = {
    ui: { clean: (s, n) => String(s || "").slice(0, n) },
    hexToStr: (h) => Buffer.from(String(h || ""), "hex").toString("utf8"),
    canonAddress: (a) => a || null,
    node: { getSC: async (_scid, o) => ({ vals: pick(o.keys) }) },
    data: {
      cfg: { founders: "f".repeat(64) },
      founders: null,
      // one verified pool of tokA against DERO: tokA is a named asset; the junk token has no DMZ market
      assets: new Map([["m".repeat(64), { kind: "pool", asset: TOKA, ticker: "TKA", dec: 5n, quote: null }]]),
      async verify() { return "founders"; },
      rawToAddr: (r) => (r ? Buffer.from(r, "hex").toString("utf8") : null),
      async fetchKeys(_scid, ks) { return pick(ks); },
    },
  };
  window.Veil = V;
  new Function("window", app("founders.js"))(window);
  return { D: V.data, asked };
}

test("loadFounders reads only the assets the app can name, and only a v2 pass", async () => {
  const { D, asked } = loadFounders(STATE, KEYS);
  const f = await D.loadFounders(null);
  assert.deepEqual(f.assets.map((a) => a.sym), ["DERO", "TKA"]);
  assert.equal(f.assets[0].ac, 150n);
  assert.equal(f.assets[1].i, 1n);
  assert.ok(!asked.some((k) => /_2$/.test(k) && !k.startsWith("p")), "a key of the junk token's index was read: " + asked.filter((k) => /_2$/.test(k)));
  const v1 = loadFounders({ ...STATE, v: "1" }, KEYS).D;
  assert.equal(await v1.loadFounders(null), null, "a pass that is not v2 must not be read with v2's keys");
});

test("myFounders offers each claim with the hints and the amount founders.bas computes", async () => {
  const { D } = loadFounders(STATE, KEYS);
  D.founders = await D.loadFounders(null);
  const m = await D.myFounders("deto1a");
  assert.equal(m.units, 5n);
  assert.equal(m.xmr, XMR, "a stored address is decoded from hex, never read as a number");
  assert.equal(m.zec, "");
  assert.deepEqual(m.positions.map((q) => q.p), [3n, 1n, 0n], "alice's positions, newest first; bob's is not hers");
  const owed = (p) => m.positions.find((q) => q.p === p).owed.map((o) => [o.asset.sym, o.amt, o.ka, o.kb]);
  // p0: from the accumulator at tick 1 (record 0, ra 0) to ac now; DERO 2 x 150, tokA 2 x 7
  assert.deepEqual(owed(0n), [["DERO", 300n, 0n, 0n], ["TKA", 14n, 0n, 0n]]);
  // p1: opened at tick 2 (DERO record 1, ra 100), closed at tick 4 (no record at or after it: ac); tokA 0 -> 7
  assert.deepEqual(owed(1n), [["DERO", 50n, 1n, 2n], ["TKA", 7n, 0n, 1n]]);
  // p3 claimed DERO through 150 and opened after every tokA deposit: nothing to offer
  assert.deepEqual(owed(3n), []);
  assert.equal((await D.myFounders("deto1nobody")).positions.length, 0);
});
