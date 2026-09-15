// The creation flow's rules, app/create.js: each step's validation against the contract's own checks
// (launch.bas 22-32, pool.bas 18-23, nft.bas 22-26, nfa.bas 200-500), the links a profile document carries, the
// itemised cost and confirmation count the Review step shows, and the local record that makes a closed page
// recoverable. create.js has no DOM; it is loaded the way the browser loads it, after amm.js, over a one-key window.
//
//   node --test tests/js/create.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const window = { Veil: {} };
for (const f of ["amm.js", "create.js"]) new Function("window", readFileSync(join(ROOT, "app", f), "utf8"))(window);
const C = window.Veil.create;
const T = "dero1qyx7qyvrtrhvaszeej487k2g689fav7h38ay37fja9qf40ycgl0m2qgayajpd";
const DERO = { scid: "0".repeat(64), sym: "DERO", dec: 5 };
const XMR = { scid: "e".repeat(64), sym: "XMR", dec: 12 };
const coin = (over) => Object.assign({ name: "Browser Coin", ticker: "brw", descr: "", tags: "", icon: "", supply: "1000000", dec: "3", grad: "10", cbps: "0", tax: "0", tmode: 0 }, over);
const env = (over) => Object.assign({ quote: DERO, treasury: T, feeBps: 50, launchPfeeBps: 5000 }, over);
const arg = (r, k) => (r.args.find((a) => a[0] === k) || [])[2];

// ---- Configure: coin ----
test("coin: a valid form gives launch.bas's fifteen arguments, in order, with the ticker upper-cased", () => {
  const r = C.launch(coin(), env());
  assert.equal(r.ok, true, r.first);
  assert.deepEqual(r.args.map((a) => a[0]), ["name", "ticker", "descr", "icon", "tags", "quote", "dec", "supply", "grad", "cbps", "tax", "tmode", "treasury", "fee", "pfee"]);
  assert.equal(arg(r, "ticker"), "BRW");
  assert.equal(arg(r, "supply"), 1000000000n);
  assert.equal(arg(r, "grad"), 1000000n); // 10 DERO at 5 decimals: launch.bas 25's floor exactly
  assert.equal(arg(r, "pfee"), 5000n);
});
test("coin: name and ticker say how to fix them", () => {
  assert.match(C.launch(coin({ name: "" }), env()).errors.name, /required: 1 to 32/);
  assert.match(C.launch(coin({ name: "x".repeat(35) }), env()).errors.name, /35 bytes; the contract takes 32\. Shorten it by 3\./);
  // STRLEN is bytes: 20 two-byte letters are 40
  assert.match(C.launch(coin({ name: "é".repeat(20) }), env()).errors.name, /40 bytes.*accented/);
  assert.match(C.launch(coin({ ticker: "" }), env()).errors.ticker, /required/);
  assert.match(C.launch(coin({ ticker: "BR W-" }), env()).errors.ticker, /remove space, “-”/);
  assert.match(C.launch(coin({ ticker: "ABCDEFGHIJK" }), env()).errors.ticker, /11 characters; 10 at most/);
});
test("coin: supply, decimals and graduation are held to InitializePrivate's bounds", () => {
  assert.match(C.launch(coin({ dec: "13" }), env()).errors.dec, /0 to 12/);
  assert.match(C.launch(coin({ supply: "999", dec: "3" }), env()).errors.supply, /at least 1,000,000 units: raise/);
  assert.match(C.launch(coin({ supply: "1.5", dec: "0" }), env()).errors.supply, /more decimal places/);
  assert.match(C.launch(coin({ supply: "10000000000", dec: "12" }), env()).errors.supply, /below 2\^63/);
  assert.match(C.launch(coin({ grad: "9.99999" }), env()).errors.grad, /between 10 and 10,000,000,000 DERO/);
  // the target is in the quote's decimals (the wXMR dust-graduation bug): 1 XMR is 10^12 units, inside the bounds
  assert.equal(arg(C.launch(coin({ grad: "1" }), env({ quote: XMR })), "grad"), 1000000000000n);
  assert.match(C.launch(coin({ grad: "0.1234567890123" }), env({ quote: XMR })).errors.grad, /at most 12 decimals/);
});
test("coin: allocation and tax, and the tax mode rule of lines 27-28", () => {
  assert.equal(arg(C.launch(coin({ cbps: "2.5" }), env()), "cbps"), 250n);
  assert.match(C.launch(coin({ cbps: "5.01" }), env()).errors.cbps, /0 to 5 percent/);
  assert.match(C.launch(coin({ tax: "2" }), env()).errors.tax, /Pick where the tax goes/);
  assert.match(C.launch(coin({ tax: "0", tmode: 3 }), env()).errors.tax, /above 0/);
  assert.match(C.launch(coin({ tax: "10.5", tmode: 1 }), env()).errors.tax, /0 to 10 percent/);
  assert.equal(arg(C.launch(coin({ tax: "2", tmode: 3 }), env()), "tmode"), 3n);
});
test("coin: the icon header takes an https URL, or the profile document's reference in its place", () => {
  assert.match(C.launch(coin({ icon: "http://x.org/a.png" }), env()).errors.icon, /https/);
  assert.equal(arg(C.launch(coin({ icon: "http://ignored" }), env({ iconRef: "dmz:" + "a".repeat(64) })), "icon"), "dmz:" + "a".repeat(64));
});
test("coin: the review's figures are the contract's (launch.bas 40-45): 80/20 split and a 16x rise", () => {
  const x = C.launch(coin({ cbps: "1" }), env()).facts;
  assert.equal(x.alloc, 10000000n); // 1% of 10^9 units
  assert.equal(x.c + x.lq + x.alloc, 1000000000n);
  assert.equal(x.lq, (1000000000n - x.alloc) / 5n);
  assert.equal(x.p1 / x.p0, 16);
});

// ---- Configure: pool, holder NFT, NFA ----
test("pool: the asset is 64 hex, not DERO, not the quote", () => {
  const a = "ab".repeat(32);
  const ok = C.pool({ asset: a.toUpperCase() }, { quote: DERO, treasury: T, feeBps: 50, pfeeBps: 5000 });
  assert.equal(ok.ok, true); assert.equal(arg(ok, "asset"), a); assert.equal(arg(ok, "quote"), DERO.scid);
  assert.match(C.pool({ asset: "abc" }, { quote: DERO }).errors.asset, /this one is 3/);
  assert.match(C.pool({ asset: "0".repeat(64) }, { quote: DERO }).errors.asset, /DERO itself/);
  assert.match(C.pool({ asset: XMR.scid }, { quote: XMR }).errors.asset, /same/);
});
test("holder NFT: nft.bas 22-24 — name, token, a hold above 0, 1 to 10,000,000 blocks, at least one edition", () => {
  const tok = { scid: "c".repeat(64), dec: 3n, ticker: "BRW" };
  const f = { name: "Pass", descr: "", icon: "", file: "", tags: "", coll: "", minhold: "1000", blocks: "1000", max: "100", price: "1" };
  const r = C.nft(f, { token: tok, treasury: T, feeBps: 50 });
  assert.equal(r.ok, true, r.first); assert.equal(arg(r, "minhold"), 1000000n); assert.equal(arg(r, "price"), 100000n);
  assert.equal(arg(C.nft(Object.assign({}, f, { price: "" }), { token: tok, treasury: T, feeBps: 50 }), "price"), 0n);
  assert.match(C.nft(Object.assign({}, f, { minhold: "0" }), { token: tok }).errors.minhold, /above 0/);
  assert.match(C.nft(Object.assign({}, f, { blocks: "10000001" }), { token: tok }).errors.blocks, /1 to 10,000,000/);
  assert.match(C.nft(Object.assign({}, f, { max: "0" }), { token: tok }).errors.max, /at least 1/);
  assert.match(C.nft(f, { token: null }).errors.token, /Pick the coin/);
  assert.match(C.nft(Object.assign({}, f, { file: "ftp://x" }), { token: tok }).errors.file, /https/);
});
test("NFA: royalty, artificer share and the protocol fee fit in 10000 bps; checks come in pairs", () => {
  const f = { name: "Unit", royalty: "5", art: "0", checkC: "", checkS: "" };
  const r = C.nfa(f, { quote: { scid: "" }, protoFee: 50n, treasury: T });
  assert.equal(r.ok, true, r.first); assert.equal(arg(r, "quote"), ""); assert.equal(arg(r, "protoFee"), 50n); assert.equal(r.args.length, 17);
  assert.match(C.nfa(Object.assign({}, f, { royalty: "99", art: "1" }), { quote: { scid: "" }, protoFee: 50n }).errors.royalty, /total 99% at most/);
  assert.match(C.nfa(Object.assign({}, f, { checkC: "ab" }), { quote: { scid: "" }, protoFee: 0n }).errors.checkC, /both/);
  assert.match(C.nfa(Object.assign({}, f, { name: "" }), { quote: { scid: "" }, protoFee: 0n }).errors.name, /required/);
  assert.equal(arg(C.nfa(Object.assign({}, f, { canUpdate: true }), { quote: { scid: "" }, protoFee: 0n, treasury: T }), "ownerCanUpdate"), 1n);
});

// ---- links for the profile document ----
test("links: https only, each on the host its name promises, with an example of the fix", () => {
  assert.equal(C.links({ website: "https://dmz.example", x: "https://x.com/dmz", telegram: "https://t.me/dmz", discord: "https://discord.gg/abc", github: "https://github.com/NoahMoney/DMZ" }).ok, true);
  assert.equal(C.links({}).any, false);
  assert.match(C.link("website", "http://dmz.example"), /must start with https:\/\//);
  assert.match(C.link("x", "https://evil.example/dmz"), /x\.com or twitter\.com, like https:\/\/x\.com\/yourname/);
  assert.match(C.link("x", "https://x.com/"), /add the account/);
  assert.match(C.link("github", "https://user:pw@github.com/a"), /user name or password/);
  assert.match(C.link("telegram", "https://t.me/a b"), /not a link/);
  assert.equal(C.link("x", "https://www.twitter.com/dmz"), "");
});

// ---- Review: the cost, itemised, and the confirmations ----
test("costs: two confirmations without a profile document, three with, and why each exists", () => {
  const two = C.costs({ what: "coin", profile: false, installGas: 17500n, regGas: 300n, regfee: 10000n });
  assert.equal(two.confirmations, 2); assert.equal(C.CONFIRM_WORDS[two.confirmations], "Two");
  assert.deepEqual(two.items.map((i) => i.k), ["install", "register"]);
  assert.equal(two.total, 27800n); assert.equal(two.complete, true);
  const three = C.costs({ what: "coin", profile: true, profileGas: 4000n, installGas: 17500n, regGas: 300n, regfee: 10000n });
  assert.equal(three.confirmations, 3); assert.deepEqual(three.items.map((i) => i.k), ["profile", "install", "register"]);
  assert.equal(three.total, 31800n);
  for (const i of three.items) assert.ok(i.why.length > 40, i.k + " says why");
  // an estimate not read yet is not a zero: the total is marked incomplete
  const part = C.costs({ what: "NFA", profile: false, installGas: null, regGas: null, regfee: 10000n });
  assert.equal(part.complete, false); assert.equal(part.total, 10000n);
});

// ---- Pay: the local record of the creator's own installs ----
test("record: stored without secrets, arguments round-trip, and what is left is read against the chain", () => {
  const m = new Map(), store = { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, v) };
  const args = C.launch(coin(), env()).args;
  C.save(store, { id: "r1", kind: "launch", net: "simulator", factory: "f", args, want: false });
  const raw = m.get(C.KEY);
  assert.doesNotMatch(raw, /seed|secret|private|spend/i);
  const [back] = C.load(store);
  assert.deepEqual(C.argsIn(back.args), args);
  assert.equal(C.next(back, {}), "lost");                                                     // nothing sent
  const inst = Object.assign({}, back, { scid: "a".repeat(64), sent: Date.now() });
  assert.equal(C.next(inst, { code: true, registered: false }), "register");                   // installed, not registered
  assert.equal(C.next(inst, { code: true, registered: true }), "done");
  assert.equal(C.next(inst, { code: false, registered: false }), "wait");                      // sent a moment ago
  assert.equal(C.next(Object.assign({}, inst, { sent: Date.now() - 600000 }), { code: false }), "lost");
  assert.equal(C.next(Object.assign({}, back, { profile: "b".repeat(64) }), { profile: true }), "install");
  C.save(store, Object.assign({}, inst, { args }));
  assert.equal(C.load(store).length, 1);                                                      // upsert by id
  C.drop(store, "r1"); assert.equal(C.load(store).length, 0);
  // a tampered entry is ignored, not trusted
  m.set(C.KEY, JSON.stringify([{ id: "x", kind: "launch", scid: "not-hex", args: [] }, "junk"]));
  assert.equal(C.load(store).length, 0);
});
