// Regression tests for the browser app's own defects, repaired 2026-09-10:
//   FE-2         one permissionless RegisterPool of an NFA scid bricked Home, Markets and Trade for every client
//   APP-1        the impersonation and look-alike fold was never run over NFAs
//   ADV-PRICE-04 the trending rank was dominated by a term one dust swap a block manufactures
//   FE-1         a contract matching a LEGACY template hash was indistinguishable from one on the current code
//   UX-06        the deployment record's chain clause was spliced into prose and cut mid-word
//   PRIV-01      the intro and Settings claimed a privacy property the price points do not leave
//   UX-04        the withdraw form said the contract records who burned; Burn never reads SIGNER()
//   UX-01        the deposit walkthrough stated the floor and never the ceiling
//   FE-4         every sheet signed a min_out derived from the reserves it opened with, however old
//
//   node --test tests/js/appcore.repair.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, A, B } from "./harness.mjs";

const src = (f) => readFileSync(join(ROOT, "app", f), "utf8");

// app/feed.js and app/names.js the way the browser loads them, in index.html's order: IIFEs over `window`, with the
// four V.ui helpers they reach for.
// hasHomoglyph is the real fold's job (tests/js/sanitize.test.mjs covers that); here it only has to be able to
// answer, so decorate()'s wiring is what is under test.
function loadData() {
  const window = {};
  const store = new Map();
  const localStorage = { getItem: (k) => (store.has(k) ? store.get(k) : null), setItem: (k, v) => store.set(k, v), removeItem: (k) => store.delete(k) };
  const run = (f) => new Function("window", "localStorage", "addEventListener", "setInterval", src(f))(window, localStorage, () => {}, () => {});
  run("config.js");
  run("rpc.js");
  run("amm.js");
  run("stats.js");
  window.Veil.ui = {
    clean: (s, n) => String(s || "").slice(0, n),
    safeUrl: (u) => (/^https:\/\//.test(u) ? u : ""),
    tickerKey: (t) => String(t || "").toUpperCase().replace(/[^A-Z0-9]/g, ""),
    hasHomoglyph: (t) => /[Ѐ-ӿͰ-Ͽ]/.test(String(t || "")),
    router: { gen: 0 },
  };
  window.addEventListener = () => {};
  run("feed.js");
  run("names.js");
  return window.Veil;
}

const now = () => Math.floor(Date.now() / 1000);
// exactly the shape app/feed.js loadNFA() builds: no points, no trades, no rd/ra
const nfaRecord = (scid, name) => ({ scid, kind: "nfa", treasury: null, sig: "n", installHeight: 1, royalty: 5n, protoFee: 50n, scBalance: 1n, startPrice: 1n, name, ticker: name.slice(0, 3), quote: null });
const poolRecord = (scid, asset, points) => ({
  scid, kind: "pool", fee: 50n, pfee: 5000n, treasury: null, trades: [],
  points: points || [{ height: 1, ts: now() - 10, rd: 1000000n, ra: 2000000n }],
  rd: 1000000n, ra: 2000000n, lp: 1000n, fd: 0n, fa: 0n, tc: 0n, pc: 1n, lh: 1n, lt: 1n,
  asset, dec: 5n, quote: null, name: "pool", ticker: scid.slice(0, 4).toUpperCase(), sig: "s", installHeight: 1,
});

// ---- FE-2 ---------------------------------------------------------------------------------------------------
test("FE-2: the pool_/launch_ loop takes only pool and launch records", () => {
  const feed = src("feed.js");
  const body = feed.slice(feed.indexOf("async refresh("), feed.indexOf("failed(rec, e)"));
  const poolLoop = body.slice(body.indexOf("await pmap(ids,"), body.indexOf("await pmap(reg.nfts,"));
  assert.match(poolLoop, /s\.kind === "pool" \|\| s\.kind === "launch"/, "the loop must file only what this list is for");
});

test("FE-2: a record with no price history no longer throws, so one poisoned id cannot cost every market its metrics", () => {
  const V = loadData(), D = V.data;
  const nfa = nfaRecord("c".repeat(64), "Grief");
  const p1 = poolRecord("a".repeat(64), "b".repeat(64));
  const p2 = poolRecord("d".repeat(64), "e".repeat(64));
  assert.doesNotThrow(() => V.stats.enrich(nfa, 1000), "enrich() used to throw TypeError on a record with no points");
  D.assets = new Map([[nfa.scid, nfa], [p1.scid, p1], [p2.scid, p2]]);
  D.height = 1000;
  assert.doesNotThrow(() => D.decorate());
  assert.ok(p1.m && p2.m, "every healthy market still gets its metrics");
  assert.doesNotThrow(() => D.trending(5));
});

// ---- APP-1 --------------------------------------------------------------------------------------------------
test("APP-1: NFAs get the impersonation, look-alike and shared-name marks the markets get", () => {
  const V = loadData(), D = V.data;
  const sym = (D.vaults()[0] || {}).sym || "XMR";
  const fake = nfaRecord("1".repeat(64), "w" + sym);
  const cyr = nfaRecord("2".repeat(64), "АВЕ");
  const dupA = nfaRecord("3".repeat(64), "Relic");
  const dupB = nfaRecord("4".repeat(64), "relic");
  const plain = nfaRecord("5".repeat(64), "Quietly Numbered");
  D.assets = new Map();
  D.nfas = new Map([fake, cyr, dupA, dupB, plain].map((x) => [x.scid, x]));
  D.height = 1000;
  D.decorate();
  assert.equal(fake.impersonates, sym, "an NFA named after the wrapped asset names the asset it copies");
  assert.equal(cyr.lookalike, true, "a Cyrillic name is marked as a look-alike");
  assert.equal(dupA.dupTicker, true);
  assert.equal(dupB.dupTicker, true);
  assert.equal(plain.impersonates, null);
  assert.equal(plain.dupTicker, false);
  assert.equal(plain.lookalike, false);
});

// ---- ADV-PRICE-04 -------------------------------------------------------------------------------------------
// The manufactured market moves its reserves in 200 consecutive blocks by 50 atomic units of a 1,000,000 reserve
// — five thousandths of a basis point each, one transaction fee apiece. The honest market moves five times by 1%.
function series(n, step, spacing) {
  const out = []; let rd = 1000000n;
  for (let i = 0; i < n; i++) { out.push({ height: i + 1, ts: now() - (n - i) * spacing, rd, ra: 2000000n }); rd += step; }
  return out;
}
test("ADV-PRICE-04: 200 dust blocks no longer out-rank five real ones", () => {
  const V = loadData(), S = V.stats;
  const dust = poolRecord("6".repeat(64), "7".repeat(64), series(200, 50n, 15));
  const real = poolRecord("8".repeat(64), "9".repeat(64), series(5, 10000n, 300));
  const hDust = S.enrich(dust, 1000).hot, hReal = S.enrich(real, 1000).hot;
  assert.ok(hDust < hReal, "manufactured hot " + hDust.toFixed(2) + " must rank below real hot " + hReal.toFixed(2));
  assert.equal(S.flow(dust, S.WIN["1h"]).blocks, 199, "every dust block is still an active block for block pressure");
  assert.equal(S.flow(dust, S.WIN["1h"]).big, 0, "none of them clears the size floor");
  // and the term itself is bounded, which it was not: 200 active blocks used to be worth 800 points on their own
  assert.ok(hDust < 40 * 4, "the activity term is capped");
});

// ---- FE-1 ---------------------------------------------------------------------------------------------------
test("FE-1: a contract on a legacy template hash is recorded as such, not merely as verified", async () => {
  const V = loadData(), D = V.data;
  const cur = D.cfg.templates.launch.sha256, old = D.cfg.templates.launch.legacy[0];
  let want = cur;
  V.node.getSC = async () => ({ code: "irrelevant", vals: {} });
  V.sha256Hex = async () => want;
  assert.equal(await D.verify("a".repeat(64)), "launch");
  assert.equal(D.oldcode.get("a".repeat(64)), false, "the current template is not legacy");
  want = old;
  assert.equal(await D.verify("b".repeat(64)), "launch", "a legacy hash still verifies: the contract is immutable and was current once");
  assert.equal(D.oldcode.get("b".repeat(64)), true, "but the app now knows it is not the code this build ships");
});

test("FE-1: app.tx refuses the ...To entrypoints on a contract that is not on the current template", () => {
  const app = src("app.js");
  assert.match(app, /rec\.templateLegacy && \/To\$\/\.test\(String\(opts\.entry \|\| ""\)\)/);
  assert.match(src("feed.js"), /templateLegacy: !!this\.oldcode\.get\(scid\)/);
  assert.match(src("token.js"), /s\.templateLegacy \?/, "and the sheet says so before the button");
});

// ---- UX-06 --------------------------------------------------------------------------------------------------
test("UX-06: the chain noun is short and whole for every vault this build ships", () => {
  const V = loadData(), D = V.data;
  for (const v of D.allVaults()) {
    const n = D.chainName(v);
    assert.ok(n.length > 0 && n.length <= 24, JSON.stringify(v.name) + " -> " + JSON.stringify(n));
    assert.equal((n.match(/\(/g) || []).length, (n.match(/\)/g) || []).length, "an unbalanced parenthesis: " + n);
    assert.doesNotMatch(n, /[(—–]/, "the descriptive clause is not part of the noun: " + n);
  }
  assert.equal(D.chainName({ name: "Monero (superseded simulator vault)", sym: "XMR-old" }), "Monero");
  assert.equal(D.chainName({}), "its own chain");
  // and the screens that used to splice the clause into a sentence take the noun instead. The clause itself
  // survives in exactly one place on each screen, rendered whole rather than cut to fit. The Wrap screen is three
  // files (its data layer, its parts, the screen), read here as one.
  const w = ["vault.js", "wrapui.js", "wrap.js"].map(src).join("\n");
  assert.match(w, /D\.chainName\(a\)/);
  assert.match(w, /D\.chainName\(w\.asset\)/);
  assert.equal((w.match(/U\.clean\(a\.chain, 120\)/g) || []).length, 1);
  assert.doesNotMatch(w, /U\.clean\(\(w\.asset && w\.asset\.chain\)/);
  assert.doesNotMatch(src("screens.js"), /U\.clean\(v\.chain \|\| "its own chain", 44\)/);
});

// ---- PRIV-01 ------------------------------------------------------------------------------------------------
test("PRIV-01: no screen claims a trade is private, and both first-read screens say what is public", () => {
  for (const f of ["app.js", "screens.js", "token.js", "wrap.js", "wrapui.js", "vault.js", "home.js", "markets.js", "feed.js", "names.js", "notice.js"]) {
    assert.doesNotMatch(src(f), /private by default/i, f);
    assert.doesNotMatch(src(f), /never individual unpublished trades/, f);
  }
  for (const f of ["app.js", "screens.js", "token.js"]) assert.match(src(f), /the size of the trade/, f + " must state what the price points give away");
  assert.match(src("app.js"), /pseudonymous/);
  assert.match(src("screens.js"), /pseudonymous/);
});

// ---- UX-04 --------------------------------------------------------------------------------------------------
test("UX-04: the burn form no longer blames the contract for publishing the burner", () => {
  const w = src("wrap.js");
  assert.doesNotMatch(w, /contract must record who burned/);
  assert.match(w, /Burn never reads who signed/);
  // and the app still names an address the chain knows: a refund to an unregistered address can never be paid
  // by Veto or Reclaim (LIV-04), so the form takes no typed return address.
  assert.match(w, /\["refund", "S", me\(\)\]/);
});

// ---- UX-01 --------------------------------------------------------------------------------------------------
test("UX-01: the deposit section states the ceiling as well as the floor, and the pill reads what the vault can do", () => {
  const w = src("wrap.js");
  const dep = w.slice(w.indexOf("// ---- deposit ----"), w.indexOf("// ---- withdraw ----"));
  assert.match(dep, /can be minted right now/);
  assert.match(dep, /headroom/);
  assert.match(w, /const canMint = !halted && w\.headroom >= w\.mindep/);
  assert.match(w, /"Deposits paused"/);
});

// ---- FE-4 ---------------------------------------------------------------------------------------------------
test("FE-4: min_out is derived from the market re-read at the moment of signing", () => {
  const t = src("token.js");
  const go = t.slice(t.indexOf("const go = async () =>"), t.indexOf("destIn.oninput"));
  assert.match(go, /D\.assets\.get\(s\.scid\)/, "the sheet must re-read the record it closed over");
  assert.match(go, /A\.minOut\(fresh\.out, slip\)/, "and sign against the fresh quote, not the one on screen");
  assert.match(go, /T\.tradeSheet\(cur, side, input\.value\)/, "re-price rather than sign numbers the user never saw");
});

test("FE-4: the guard is the one that makes the slippage setting mean something", () => {
  // The pool the finder used: one opposing sell between the quote and the signature.
  const s = { kind: "pool", rd: B(10000000), ra: B(20000000), fee: B(50), pfee: B(5000), dec: B(5), quoteDec: 5 };
  const amount = B(2000000), slip = 100; // 1%, the default chip
  const q0 = A.poolBuy(s, amount);
  const stale = A.minOut(q0.out, slip);
  // a sell of 30,000,000 units of the asset into the same pool moves it hard
  const after = A.poolSell(s, B(30000000));
  const s1 = Object.assign({}, s, { rd: s.rd - after.out, ra: s.ra + B(30000000) });
  const fresh = A.poolBuy(s1, amount);
  const good = A.minOut(fresh.out, slip);
  const bound = (min, out) => Number(out - min) / Number(out);
  assert.ok(bound(stale, fresh.out) > 0.01, "the stale floor is outside the chosen 1%: " + (bound(stale, fresh.out) * 100).toFixed(1) + "%");
  assert.ok(bound(good, fresh.out) <= 0.0101, "the fresh floor is inside it");
});

// ---- LIV-04 -------------------------------------------------------------------------------------------------
// Burn's `refund` is stored raw and is the sole target of both Veto and Reclaim, and the DVM refuses a transfer
// to an address that has never registered — so a burn naming one can never be returned by any route or quorum.
// The contract cannot test registration, so the guard is the caller's: this app names the connected wallet, which
// signed the burn and is therefore registered, and offers no field in which another address could be typed.
test("LIV-04: the burn names the wallet that signs it, and no other address can be named from this app", () => {
  const w = src("wrap.js");
  const form = w.slice(w.indexOf("const burnForm = (w) =>"), w.indexOf("const doBurn = async"));
  assert.doesNotMatch(form, /refund/i, "the burn form must not take a typed return address");
  const burn = w.slice(w.indexOf("const doBurn = async"), w.indexOf("// ---- load ----"));
  assert.match(burn, /\["refund", "S", me\(\)\]/, "the only refund address the app sends is the connected wallet");
});

// ---- FEE-05 -------------------------------------------------------------------------------------------------
// app/notice.js is the page the app declares authoritative and no other surface may contradict it: the share is of
// the fees the treasury claims, each paid in the asset it was taken in (founders.bas v2), deposited by a bot no
// contract obliges. Three screens once said something stronger and unconditional.
test("FEE-05: no screen promises a share of every protocol fee", () => {
  // notice.js says the treasury "Receives every protocol fee", which is true of the treasury and is not a claim
  // about the pass; the two screens that sell the pass are the ones under test.
  for (const f of ["nfts.js", "home.js"]) {
    const s = src(f);
    for (const bad of [/every protocol fee/i, /protocol fees claimed from every market/i]) assert.doesNotMatch(s, bad, f + " contradicts app/notice.js");
  }
  const n = src("nfts.js");
  // the sentence that sits above the mint button says what the share is paid in, and the ledger what is not shared
  assert.match(n, /fees the treasury claims, each paid in the asset it was taken in/);
  assert.match(n, /Fees from NFA sales reach no holder/);
  assert.match(src("home.js"), /% of claimed fees, paid in each fee's asset/);
});
