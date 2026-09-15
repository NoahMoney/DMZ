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
  assert.doesNotMatch(nfts, /PayBatch|"Distribute"/);
  assert.match(app("home.js"), /% of claimed fees, paid in each fee's asset/);
});

// 2026-09-15: the swaps are removed, so no fee is taken in XMR or ZEC and nothing pays a holder's XMR or ZEC address.
// founders.bas keeps SetXmr / SetZec; the pass screen offers only Clear, and only for an address already stored.
test("the pass screen offers no payout address: only Clear, for an address stored before", () => {
  const nfts = app("nfts.js"), form = nfts.slice(nfts.indexOf("function payoutForm"), nfts.indexOf("// Level 3"));
  assert.doesNotMatch(form, /h\("input"|"Store"|"Replace"|V\.payout/, "no field to store or replace an address");
  assert.match(form, /\.filter\(\(x\) => x\[3\]\);\s+if \(!set\.length\) return null;/, "nothing is drawn for a wallet with none stored");
  assert.match(form, /button\("Clear", "small ghost", \(\) => tx\(\{ scid: f\.scid, entry, args: \[\["addr", "S", ""\]\]/, "Clear sends the empty address setp deletes");
  assert.match(form, /Nothing reads or pays these any more/);
  assert.equal(/V\.payout|xmrAddr|zecAddr/.test(app("founders.js") + nfts), false, "the address checks went with the form");
  for (const f of appFiles) assert.doesNotMatch(app(f), /0\.5% swap fee|swap fees? it receives|payout address a holder stored|fresh Monero subaddress|XMR and ZEC payment/, "app/" + f);
  assert.doesNotMatch(nfts, /makes each payment|off the DERO chain|paid by the treasury's bot|XMR and ZEC share|addresses holders store/, "nothing on the pass screen says XMR or ZEC is paid");
  // the approved description names wXMR and wZEC: a build with no vault says, in notice.js's words, that neither is offered
  assert.match(nfts, /\/\\bw\(XMR\|ZEC\)\\b\/i\.test\(f\.descr\) && V\.notice && V\.notice\.noWrap\(\) \? h\("div", \{ class: "note mt8" \}, V\.notice\.NOWRAP \+ " Here only DERO and DERO tokens are deposited\."\)/);
});

// ---- app/notice.js, rendered for a build with the vault and for one with none (the mainnet test's) ----------------

function renderNotice({ vaults, network, share = 10000n, vault = { k: 2n, na: 3n }, promo = null }) {
  const handlers = {}, el = {};
  const flat = (x) => (Array.isArray(x) ? x.map(flat).join(" ") : x === null || x === undefined || x === false ? "" : String(x));
  const U = { h: (_t, _a, ...kids) => flat(kids), render: (e, ...n) => { e.out = flat(n).replace(/\s+/g, " "); }, router: { on: (n, f) => { handlers[n] = f; }, current: null },
    kv: (k, v) => k + ": " + v, clean: (s, n) => String(s || "").slice(0, n) };
  const V = { ui: U, shortAddr: (a) => a, data: { cfg: { network, promo }, vaults: () => vaults, allVaults: () => vaults, vault: vaults.length ? vault : null,
    founders: share === null ? null : { share }, registry: null, onChange() {} } };
  new Function("window", app("notice.js"))({ Veil: V });
  handlers.notice(el);
  return { text: el.out, N: V.notice };
}
const TWO = [{ scid: "e".repeat(64), sym: "XMR", name: "Monero" }, { scid: "8".repeat(64), sym: "ZEC", name: "Zcash" }];
const NODEP = "Deposits and withdrawals of XMR and ZEC are not available on this network.";

test("notice.js on a build with the vault: no swaps, custody through the vault, and the fee flow in the pass's terms", () => {
  const { text } = renderNotice({ vaults: TWO, network: "simulator" });
  for (const want of [/There are no atomic swaps on DMZ\. XMR and ZEC come in and go out only through a vault/, /A vault is custody: k of the n named signers of each vault can move its whole reserve, and the vault contract cannot/,
    /A k-of-n federation of bonded signers holds the XMR and ZEC behind the wrapped assets\. They can move all of it; the vault contract cannot\. This is custody\./, /Custody: k-of-n federation of named signers holds the XMR and ZEC and can move it; the vault contract cannot/,
    /The registered Founders Passes receive 100% of the trading fees the treasury claims from DMZ markets, each in the asset the fee was taken in, never converted to DERO: DERO, wXMR, wZEC and DERO tokens\./,
    /The treasury's bot deposits them into the pass contract, where each registered pass claims its part\. The bot makes every deposit, and no contract forces it\./,
    /A deposit is shared by the passes registered when it is made/, /While no pass is registered nothing is deposited/,
    /registration and chat fees, the rate contract's vote fees, the protocol fee on NFA sales, pass mints, and from each vault its index registration and withdrawal fees and slashed signer bonds/,
    /no support/i, /Code is law/, /space cowboy/]) assert.match(text, want);
  assert.doesNotMatch(text, /not available on this network|not offered on this network/);
  // one vault: its own numbers, read from the contract
  const one = renderNotice({ vaults: TWO.slice(0, 1), network: "simulator" }).text;
  assert.match(one, /A vault is custody: 2 of the 3 named signers of the vault can move its whole reserve/); assert.match(one, /A 2-of-3 federation of bonded signers holds the XMR/);
  assert.match(one, /DERO, wXMR and DERO tokens\./);
  // the share before the pass is read: a share, as the contract stores it, and no invented number
  assert.match(renderNotice({ vaults: TWO, network: "simulator", share: null }).text, /receive a share of the trading fees the treasury claims from DMZ markets, as stored in the pass contract, each in the asset/);
});

test("notice.js on a build with no vault: nothing comes in or goes out, and no federation or wrapped unit is named", () => {
  const { text, N } = renderNotice({ vaults: [], network: "mainnet" });
  assert.equal(N.NODEP, NODEP);
  for (const want of [/There are no atomic swaps on DMZ/, new RegExp(NODEP.replace(/\./g, "\\.")), /Wrapped XMR and ZEC are not offered on this network/,
    /never converted to DERO: DERO and DERO tokens\./, /no contract forces it/, /the protocol fee on NFA sales, and pass mints\. It runs the bot/]) assert.match(text, want);
  // ui.mjs --mainnet refuses these on the notice
  assert.doesNotMatch(text, /federation holds the|bonded signers hold/);
  assert.doesNotMatch(text, /\bw(XMR|ZEC)\b|withdrawal fees|signer bonds/);
  // the other screens say the same words (app/move.js and app/wrapui.js carry their own copy)
  for (const f of ["move.js", "wrapui.js"]) assert.ok(app(f).includes(NODEP), f);
  assert.doesNotMatch(N.NOWRAP_WHY, /swap/i);
});

test("notice.js has no word of the removed swaps' fee or of XMR and ZEC payouts, and never calls custody trustless", () => {
  for (const build of [{ vaults: TWO, network: "simulator" }, { vaults: [], network: "mainnet" }]) {
    const { text } = renderNotice(build);
    assert.doesNotMatch(text, /0\.5%|swap fee|swap program|by atomic swap|payout|XMR and ZEC are paid|maker of each|refunded swap|trustless/i);
  }
  assert.doesNotMatch(app("notice.js"), /0\.5%|payout|swap fee|trustless/i);
});

// A build whose config names a promotion contract (contracts/promo.bas) lists its proceeds among what the treasury
// receives, and says the pass share of them is deposited by the bot in the auction's asset; a build with none (the
// mainnet test's: simdeploy installs no promo without a vault) says nothing about it.
test("notice.js names promotion proceeds only on a build whose config names a promotion contract", () => {
  const P = "9".repeat(64);
  for (const b of [{ vaults: TWO, network: "simulator", promo: P }, { vaults: [], network: "mainnet", promo: P }]) {
    const { text } = renderNotice(b);
    assert.match(text, /the protocol fee on NFA sales, promotion proceeds \(the winning bids, in the auction's asset, claimable by anyone and paid only to the treasury\), (and )?pass mints/);
    assert.match(text, /The treasury also receives promotion proceeds, the winning bids of the promotion auction; the bot deposits the pass share of them in the auction's own asset, and no contract forces that either\./);
  }
  for (const b of [{ vaults: TWO, network: "simulator" }, { vaults: [], network: "mainnet" }, { vaults: [], network: "mainnet", promo: "not an id" }])
    assert.doesNotMatch(renderNotice(b).text, /promotion|auction/i);
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
