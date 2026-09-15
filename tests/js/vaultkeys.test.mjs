// The Wrap screen's per-user reads, in the encoding contracts/vault.bas really stores.
//
// Every other DMZ contract keys a per-address record by ADDRESS_STRING(SIGNER()), the bech32 "dero1…" text. vault.bas
// does not: Register() stores uc_ and ul_ under the raw SIGNER() (33 compressed bytes), ua_<j> holds those bytes,
// and Burn() stores w_<j>_by = ADDRESS_RAW(refund). Until 2026-09-15 app/vault.js asked for "uc_" + the dero1 text
// through keysstring and compared w_<j>_by as text, so the connected wallet never saw its index, its deposit address
// or a withdrawal (and so never its Reclaim), and the Wrap screen said "included but did not execute" of a Register
// that had executed. Shown on the simulator's XMR vault (e136e851…) that day: wallet 30014 registered through the Wrap
// screen, the toast said it did not execute, and then ua_1 = 08c01cc1…6400 (its 33 raw bytes), keysstring
// "uc_dero1qyyvq8xp…" answered NOT AVAILABLE, and keysbytes "uc_" + those bytes answered 1, "ul_" + them + "_0" 1.
//
// A raw key cannot travel in keysstring: the parameter is JSON text, and a byte above 0x7f becomes its UTF-8 encoding
// in the Go string the daemon builds. DERO.GetSC also takes keysbytes ([][]byte, base64 in JSON) and looks up exactly
// those bytes, which is what the app now sends. The node below answers GetSC as derohe's
// cmd/derod/rpc/rpc_dero_getsc.go does, over storage laid out byte for byte as vault.bas lays it out.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const contract = (n) => readFileSync(join(ROOT, "contracts", n), "utf8");
const app = (n) => readFileSync(join(ROOT, "app", n), "utf8");

// read off the simulator on 2026-09-15: wallet 30014 (index 1 of the XMR vault) and the owner of index 0
const ME = { deto: "deto1qyyvq8xp22kkzta3g25r8eful0nqsm9jr23522mqfn4xmp89skhkgqqc62nc9", raw: "08c01cc152ad612fb142a833e53cfbe6086cb21aa3452b604cea6d84e585af6400" };
const OTHER = { raw: "0da6f57058e06383fbc2d0133562cd567df15fa8f99d03ea2c3d9c5245bba6ca01" };
const STRANGER = "deto1qy9zd2uaw5hahz628n863sszfuzupy2npmzlsjq3342erpfvze2kzqqr3axw3"; // wallet 30015: no index, no burn
const SCID = "e".repeat(64);

// ---- a DERO node over vault.bas's storage ----
// keys and string values are byte strings (latin1 in JS, one char per byte), as graviton holds them
const bytes = (...parts) => parts.map((p) => (typeof p === "string" ? p : Buffer.from(p.hex, "hex").toString("latin1"))).join("");
const R = (hex) => ({ hex });
function vaultState() {
  const s = new Map(), u = (k, v) => s.set(k, { t: "u", v: BigInt(v) }), str = (k, v) => s.set(k, { t: "s", v });
  str("kind", "vault");
  for (const [k, v] of Object.entries({ v: 3, k: 2, n: 3, na: 3, sup: 50000, cap: 100000, capratio: 3, bondtot: 60000, minbond: 1, minburn: 1000, wfee: 100, mindep: 1, regfee: 0, wdelay: 0, winmax: 0, bigbps: 0, bigdelay: 0, halted: 0, sealed: 1, un: 2, wn: 3, feepool: 0 })) u(k, v);
  str("viewkey", "");
  // Register() by OTHER, then by ME (vault.bas lines 276-279): ua_<j> = s, ul_<s>_<c> = j, uc_<s> = c + 1
  str("ua_0", bytes(R(OTHER.raw))); u(bytes("ul_", R(OTHER.raw), "_0"), 0); u(bytes("uc_", R(OTHER.raw)), 1);
  str("ua_1", bytes(R(ME.raw))); u(bytes("ul_", R(ME.raw), "_0"), 1); u(bytes("uc_", R(ME.raw)), 1);
  // Attest(1, "sim:1") (lines 302-304)
  str("da_1", "sim:1"); u("dh_1", 93); u("dver_1", 1);
  // Burn() x3 (lines 392-398): w_<j>_by = ADDRESS_RAW(refund)
  const burn = (j, by, v, st, ftx) => { str("w_" + j + "_to", "44AFFq5kSiGBoZ4NMDwYtN18obc8AemS33DBLWs3H7otXft3XjrpDtQGv7SqSsaBYBb98uNbr2VBBEt7f2wfn3RVGQBEP3A"); u("w_" + j + "_v", v); u("w_" + j + "_f", 100);
    str("w_" + j + "_by", bytes(R(by))); u("w_" + j + "_h", 100 + j); u("w_" + j + "_st", st); u("w_" + j + "_rel", 160 + j); if (ftx) str("w_" + j + "_ftx", ftx); };
  burn(0, ME.raw, 900, 0); burn(1, OTHER.raw, 400, 0); burn(2, ME.raw, 1900, 1, "ab".repeat(32));
  return s;
}
// DERO.GetSC as rpc_dero_getsc.go answers it: keysstring names a key by the UTF-8 bytes of a JSON string, keysbytes by
// base64-decoded bytes; a uint64 comes back as decimal, a string as hex (keysstring) or as itself (keysbytes); a
// missing key as "NOT AVAILABLE…". omitempty drops an empty list. `bytesUnsupported` is a node that ignores keysbytes.
function makeNode(state, { bytesUnsupported = false } = {}) {
  const sent = [];
  const call = async (method, p) => {
    assert.equal(method, "DERO.GetSC"); sent.push(p);
    const r = { stringkeys: {}, balances: {}, balance: 0, code: "", status: "OK" };
    const look = (k) => state.get(k);
    if (p.keysstring) r.valuesstring = p.keysstring.map((ks) => { const e = look(Buffer.from(ks, "utf8").toString("latin1")); return !e ? "NOT AVAILABLE err: leaf not found" : e.t === "u" ? String(e.v) : Buffer.from(e.v, "latin1").toString("hex"); });
    if (p.keysbytes && !bytesUnsupported) r.valuesbytes = p.keysbytes.map((b) => { const e = look(Buffer.from(b, "base64").toString("latin1")); return !e ? "NOT AVAILABLE" : e.t === "u" ? String(e.v) : Buffer.from(e.v, "latin1").toString("utf8"); });
    return JSON.parse(JSON.stringify(r)); // what crosses the wire
  };
  return { call, sent };
}
function loadApp(node) {
  const window = { Veil: {} };
  new Function("window", app("rpc.js"))(window);
  const V = window.Veil;
  V.node.call = node.call;
  V.ui = { clean: (s, n) => String(s || "").slice(0, n) };
  V.wallet = { getBalance: async () => ({ balance: 0 }) };
  V.data = {
    cfg: { vault: SCID }, vaults: () => [{ scid: SCID, sym: "XMR" }], allVaults: () => [{ scid: SCID, sym: "XMR" }],
    // feed.js fetchKeys: keysstring, in chunks, keeping the keys the node answered
    async fetchKeys(scid, keys) { const out = {}; const r = await V.node.getSC(scid, { keys }); for (const k of keys) if (r.vals[k] !== undefined) out[k] = r.vals[k]; return out; },
  };
  new Function("window", app("vault.js"))(window);
  return V;
}

test("vault.bas keys uc_ / ul_ / ua_ / w_*_by by the raw 33 bytes, never by the dero1 string", () => {
  const v = contract("vault.bas");
  const register = v.slice(v.indexOf("Function Register()"), v.indexOf("Function Attest("));
  assert.match(register, /LET s = SIGNER\(\)/);
  assert.match(register, /STORE\("ua_" \+ j, s\)/);
  assert.match(register, /STORE\("ul_" \+ s \+ "_" \+ c, j\)/);
  assert.match(register, /STORE\("uc_" \+ s, c \+ 1\)/);
  assert.equal(/ADDRESS_STRING/.test(register), false, "Register() never takes the string form");
  const burn = v.slice(v.indexOf("Function Burn("), v.indexOf("Function Released("));
  assert.match(burn, /LET s = ADDRESS_RAW\(refund\)/);
  assert.match(burn, /STORE\("w_" \+ j \+ "_by", s\)/);
});

test("every other contract keys per-address records by ADDRESS_STRING", () => {
  for (const [file, key] of [["founders.bas", "hu_"], ["launch.bas", "st_"], ["nft.bas", "lk_"]]) {
    const src = contract(file);
    assert.match(src, /LET a = ADDRESS_STRING\(s\)/, file + " derives the string form");
    assert.match(src, new RegExp('STORE\\("' + key + '" \\+ a'), file + " keys " + key + " by it");
  }
});

test("V.addressRaw gives the bytes SIGNER() stores, for the dero1 and the deto1 spelling alike", () => {
  const V = loadApp(makeNode(new Map()));
  assert.equal(V.addressRaw(ME.deto), ME.raw, "the simulator's ua_1 is wallet 30014's raw key");
  assert.equal(V.addressRaw(V.canonAddress(ME.deto)), ME.raw);
  assert.equal(V.addressRaw(V.rawToAddress(OTHER.raw)), OTHER.raw);
  assert.equal(V.rawToAddress(ME.raw), "dero1qyyvq8xp22kkzta3g25r8eful0nqsm9jr23522mqfn4xmp89skhkgqqzrktad");
  for (const bad of ["", "dero1", "not an address", null, undefined]) assert.equal(V.addressRaw(bad), null);
});

test("the text spelling of a raw key is a different key, and keysstring cannot carry the raw one", async () => {
  const V = loadApp(makeNode(vaultState()));
  const addr = V.canonAddress(ME.deto);
  const r = await V.node.getSC(SCID, { keys: ["uc_" + addr, "uc_" + Buffer.from(ME.raw, "hex").toString("latin1")] });
  assert.deepEqual(r.vals, {}, "neither the dero1 text nor the raw bytes sent as JSON text finds uc_");
});

test("myVault finds the connected wallet's index, its deposit address and only its own withdrawals", async () => {
  const node = makeNode(vaultState()), V = loadApp(node), D = V.data;
  await D.loadVault(SCID);
  const m = await D.myVault(V.canonAddress(ME.deto));
  assert.equal(m.indexes, 1);
  assert.deepEqual({ j: m.dep.j, addr: m.dep.addr, since: m.dep.since, ver: m.dep.ver }, { j: "1", addr: "sim:1", since: 93n, ver: 1n });
  assert.deepEqual(m.withdrawals.map((x) => x.j), [2, 0], "newest first, and burn 1 is someone else's");
  assert.deepEqual([m.withdrawals[0].st, m.withdrawals[0].ftx, m.withdrawals[1].units, m.withdrawals[1].fee], [1, "ab".repeat(32), 900n, 100n]);
  // the request that found uc_: exactly "uc_" and the 33 bytes, sent as keysbytes
  const req = node.sent.find((p) => p.keysbytes);
  assert.equal(Buffer.from(req.keysbytes[0], "base64").toString("hex"), Buffer.from("uc_").toString("hex") + ME.raw);
  // the deto1 spelling of the same wallet reads the same records
  assert.equal((await D.myVault(ME.deto)).dep.j, "1");
  // the owner of index 0: registered, not yet attested, one burn
  const o = await D.myVault(V.rawToAddress(OTHER.raw));
  assert.deepEqual([o.indexes, o.dep.j, o.dep.addr, o.withdrawals.map((x) => x.j)], [1, "0", "", [1]]);
  // and a wallet with neither
  const s = await D.myVault(STRANGER);
  assert.deepEqual([s.indexes, s.dep, s.withdrawals], [0, null, []]);
  assert.equal(await D.vaultIndexes(SCID, ME.deto), 1);
  assert.equal(await D.vaultIndexes(SCID, STRANGER), 0);
});

test("fail closed: a node that does not answer the raw read, or an address that cannot be encoded, is an error, not 'no index'", async () => {
  const V = loadApp(makeNode(vaultState(), { bytesUnsupported: true })), D = V.data;
  await D.loadVault(SCID);
  await assert.rejects(D.myVault(ME.deto), /raw key/);
  await assert.rejects(D.vaultIndexes(SCID, ME.deto), /raw key/);
  const W = loadApp(makeNode(vaultState())).data;
  await W.loadVault(SCID);
  await assert.rejects(W.vaultIndexes(SCID, "not an address"), /not a DERO address/);
});

test("the Wrap screen's Register and Burn probes read the contract's encoding, and Deposit & withdraw decodes raw", () => {
  const w = app("wrap.js");
  assert.doesNotMatch(w, /"uc_" \+ me\(\)/, "uc_ is never asked for by the dero1 text");
  assert.doesNotMatch(w, /hexToStr\(g\[k\]/, "w_<j>_by is never compared as text");
  const reg = w.slice(w.indexOf("const doRegister"), w.indexOf("const doReclaim"));
  assert.match(reg, /const n0 = await D\.vaultIndexes\(w\.scid, me\(\)\)\.catch\(\(\) => null\)/);
  assert.match(reg, /check: async \(\) => \(await D\.vaultIndexes\(w\.scid, me\(\)\)\) > n0/);
  const burn = w.slice(w.indexOf("const doBurn"), w.indexOf("// ---- load ----"));
  assert.match(burn, /const raw = V\.addressRaw\(me\(\)\)/);
  assert.match(burn, /ks\.some\(\(k\) => g\[k\] === raw\)/);
  const v = app("vault.js");
  assert.doesNotMatch(v, /"uc_" \+ addr|"ul_" \+ addr/);
  assert.match(v, /by\["w_" \+ j \+ "_by"\] === raw/);
  // move.js reads ua_<j> and w_<j>_by (both raw) through rawToAddr
  assert.match(app("move.js"), /D\.rawToAddr\(v\) === me/);
});
