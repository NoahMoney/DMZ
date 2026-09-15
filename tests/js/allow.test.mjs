// app/allow.js (Y1-10): the one allowlist V.app.tx checks before it builds anything. The build's promotion and sentiment
// contracts (config.js promo, rate) are sent to only for the entrypoints this app calls, and only once the node's copy of
// the code hashes to the shipped template; an unlisted contract, an unverified one, a wrong entrypoint and a node that
// does not answer are all refused. promo.js and sentiment.js keep no send path of their own.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const app = (f) => readFileSync(join(ROOT, "app", f), "utf8");
const id = (c) => c.repeat(64);
const PROMO = id("5"), RATE = id("6"), MKT = id("a"), STRANGER = id("9");
const TPL = { "templates/promo.bas": "Function Bid() Uint64\n10 RETURN 0\nEnd Function\n", "templates/rate.bas": "Function Vote() Uint64\n10 RETURN 0\nEnd Function\n" };

function load({ cfg = { promo: PROMO, rate: RATE, factory: id("f"), social: id("c"), chat: id("d") }, code = {}, fail = false, tpl = TPL } = {}) {
  const asked = [];
  const codes = Object.assign({ [PROMO]: TPL["templates/promo.bas"], [RATE]: TPL["templates/rate.bas"], [STRANGER]: TPL["templates/promo.bas"] }, code);
  const window = { Veil: {
    node: { async getSC(scid, o) { asked.push(scid); if (fail) throw new Error("no node"); return { code: o.code ? codes[scid] || "" : "", vals: {} }; } },
    sha256Hex: async (s) => createHash("sha256").update(s).digest("hex"),
    data: { cfg, assets: new Map([[MKT, {}]]), nfts: new Map(), nfas: new Map(), founders: { scid: id("b") }, allVaults: () => [{ scid: id("e") }] },
  } };
  const fetch = async (p) => (tpl[p] ? { ok: true, text: async () => tpl[p] } : { ok: false, text: async () => "" });
  new Function("window", "fetch", app("allow.js"))(window, fetch);
  return { A: window.Veil.allow, asked, V: window.Veil };
}

test("the listings and the build's own contracts pass as before; anything else is refused", async () => {
  const { A } = load();
  for (const s of [MKT, id("f"), id("c"), id("d"), id("b"), id("e")]) assert.equal(await A.check({ scid: s, entry: "Anything" }), null, s);
  assert.match(await A.check({ scid: STRANGER, entry: "Bid" }), /not a verified listing/, "an unlisted contract, even one running the promo code");
  assert.match(await A.check({ scid: "xyz", entry: "Bid" }), /bad contract id/);
  assert.match(await A.check({}), /bad contract id/);
});

test("promo and rate: only the entrypoints this app calls, and only once the code matches the shipped template", async () => {
  const { A, asked } = load();
  assert.equal(await A.check({ scid: PROMO, entry: "Bid" }), null);
  assert.equal(await A.check({ scid: PROMO, entry: "ClaimProceeds" }), null);
  assert.equal(await A.check({ scid: RATE, entry: "Vote" }), null);
  assert.match(await A.check({ scid: PROMO, entry: "InitializePrivate" }), /sends only Bid and ClaimProceeds to the promo contract/);
  assert.match(await A.check({ scid: RATE, entry: "Bid" }), /sends only Vote to the rate contract/);
  // the code is read once per contract: it is immutable
  const n = asked.length; await A.check({ scid: PROMO, entry: "Bid" }); assert.equal(asked.length, n);
});

test("an unverified promo or rate contract is refused: other code, no template, or no answer from the node", async () => {
  assert.match(await load({ code: { [PROMO]: "Function Bid() Uint64\n10 RETURN 1\nEnd Function\n" } }).A.check({ scid: PROMO, entry: "Bid" }), /promo contract's code on your node does not match/);
  assert.match(await load({ code: { [RATE]: "" } }).A.check({ scid: RATE, entry: "Vote" }), /rate contract's code on your node does not match/);
  assert.match(await load({ tpl: {} }).A.check({ scid: RATE, entry: "Vote" }), /does not match the template this build ships/);
  const off = load({ fail: true });
  assert.match(await off.A.check({ scid: PROMO, entry: "Bid" }), /does not match/, "a node that does not answer is a refusal");
  // and a build that names no promo contract admits none, whatever its code
  assert.match(await load({ cfg: { rate: RATE } }).A.check({ scid: PROMO, entry: "Bid" }), /not a verified listing/);
});

test("V.app.tx checks the allowlist, and promo.js and sentiment.js send only through V.app.tx", () => {
  const a = app("app.js");
  assert.match(a, /const no = await V\.allow\.check\(opts\); if \(no\)/);
  assert.doesNotMatch(a, /D\.assets\.has\(opts\.scid\)/, "the list lives in allow.js alone");
  for (const f of ["promo.js", "sentiment.js"]) {
    const s = app(f);
    assert.match(s, /V\.app\.tx\(\{/, f + " sends through V.app.tx");
    assert.doesNotMatch(s, /\.transfer\(|gasEstimate|randomAddress|netGate/, f + " keeps no send path of its own");
  }
  const html = app("index.html");
  assert.ok(html.indexOf('src="allow.js"') > 0 && html.indexOf('src="allow.js"') < html.indexOf('src="app.js"'), "allow.js loads before app.js");
});
