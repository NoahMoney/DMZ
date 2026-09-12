// The Wrap screen's per-user reads never resolve.
//
// Every other DMZ contract keys a per-address record by ADDRESS_STRING(SIGNER()) — the bech32 "dero1…" string.
// contracts/vault.bas is the exception: Register() stores uc_/ul_ under the raw SIGNER() (33 compressed bytes)
// and Burn() stores w_<j>_by under ADDRESS_RAW(refund). app/vault.js myVault()/vaultWithdrawals() build those
// keys from the canonical dero1 string, so DERO.GetSC keysstring answers "NOT AVAILABLE" for every one of them:
// the connected holder never sees their index, their deposit address, or any withdrawal — and the Reclaim
// button, the vault's only permissionless exit, is rendered from that list and so can never appear.
//
// Raw bytes cannot be asked for through keysstring either: the parameter is JSON, and a byte above 0x7f becomes
// its UTF-8 encoding in the Go string the daemon builds. So the fix belongs in the contract (key by
// ADDRESS_STRING, as founders.bas / launch.bas / nft.bas do) or in a whole-tree read on the app side.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const contract = (n) => readFileSync(join(ROOT, "contracts", n), "utf8");
const app = (n) => readFileSync(join(ROOT, "app", n), "utf8");

test("every contract but vault.bas keys per-address records by ADDRESS_STRING", () => {
  for (const [file, key] of [["founders.bas", "hu_"], ["launch.bas", "st_"], ["nft.bas", "lk_"]]) {
    const src = contract(file);
    assert.match(src, /LET a = ADDRESS_STRING\(s\)/, file + " derives the string form");
    assert.match(src, new RegExp('STORE\\("' + key + '" \\+ a'), file + " keys " + key + " by it");
  }
});

test("vault.bas keys uc_ / ul_ / w_*_by by raw bytes, not by the dero1 string", () => {
  const v = contract("vault.bas");
  const register = v.slice(v.indexOf("Function Register()"), v.indexOf("Function Attest("));
  assert.match(register, /LET s = SIGNER\(\)/);
  assert.match(register, /STORE\("uc_" \+ s/, "uc_ is keyed by the raw signer");
  assert.equal(/ADDRESS_STRING/.test(register), false, "Register() never takes the string form");

  const burn = v.slice(v.indexOf("Function Burn("), v.indexOf("Function Released("));
  assert.match(burn, /LET s = ADDRESS_RAW\(refund\)/);
  assert.match(burn, /STORE\("w_" \+ j \+ "_by", s\)/, "w_<j>_by is the raw key, not ADDRESS_STRING");
});

test("app/vault.js (the Wrap screen's data layer) builds those same keys from the canonical dero1 address", () => {
  const w = app("vault.js");
  assert.match(w, /"uc_" \+ addr/, 'myVault() asks for "uc_" + a dero1 string');
  assert.match(w, /"ul_" \+ addr \+ "_"/, "and ul_ the same way");
  assert.match(w, /hexToStr\(by\["w_" \+ j \+ "_by"\] \|\| ""\) === addr/, "vaultWithdrawals() compares w_by to the dero1 string");
  // and the comment above it states the wrong convention, which is how this survived review
  assert.match(w, /w_<j>_by is the burner as ADDRESS_STRING/);
});

test("a dero1 string and the raw signer bytes are different keys", () => {
  // the exact pair read off the simulator vault f23403b0… on 2026-09-10:
  //   uc_<0d a6 f5 70 …>  = "1"        (present)
  //   uc_dero1qyx6dat…    = NOT AVAILABLE
  const window = {};
  new Function("window", app("rpc.js"))(window);
  const V = window.Veil;
  const raw = "0da6f57058e06383fbc2d0133562cd567df15fa8f99d03ea2c3d9c5245bba6ca01";
  const addr = V.rawToAddress(raw);
  assert.equal(addr, "dero1qyx6datstrsx8qlmctgpxdtze4t8mu2l4rue6ql29s7ec5j9hwnv5qgnajyz8");
  const rawKey = "uc_" + Buffer.from(raw, "hex").toString("latin1");
  assert.notEqual(rawKey, "uc_" + addr, "the two spellings are not the same storage key");
  // and the raw one cannot be carried in the JSON keysstring parameter without changing its bytes
  assert.notEqual(Buffer.from(JSON.parse(JSON.stringify(rawKey)), "utf8").length, Buffer.from(rawKey, "latin1").length);
});
