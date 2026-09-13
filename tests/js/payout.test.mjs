// app/founders.js V.payout: the Founders Pass payout addresses are checked before SetXmr / SetZec is offered. A Monero
// address must be a standard address or subaddress of the build's network with a good Keccak checksum; a Zcash address
// must be TRANSPARENT and of the build's network, because the treasury's bot can pay only transparent addresses.
//
//   node --test tests/js
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createHash, randomBytes } from "node:crypto";
import { join } from "node:path";
import { ROOT } from "./harness.mjs";

const window = { Veil: { ui: { clean: (s, n) => String(s || "").slice(0, n) }, data: {} } };
new Function("window", readFileSync(join(ROOT, "app", "founders.js"), "utf8"))(window);
const P = window.Veil.payout;
const hex = (b) => Buffer.from(b).toString("hex");

test("Keccak-256 is the original Keccak (Monero's cn_fast_hash), not SHA-3", () => {
  assert.equal(hex(P.keccak256(new Uint8Array(0))), "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470");
  assert.equal(hex(P.keccak256(new TextEncoder().encode("abc"))), "4e03657aea45a94fc7d47ba826c8d667c0d1e6e33a64a036ec44f58fa12d6c45");
});

// addresses from tools/xmrswap/keys_test.go, which builds and parses them the way monero's own code does
const MAIN = "42gt8cXJSHAL4up8XoZh7fikVuswDU7itAoaCjSQyo6fFoeTQpAcAwrQ1cs8KvFynLFSBdabhmk7HEe3HS7UsAz4LYnVPYM";
const STAGE = "55LTR8KniP4LQGJSPtbYDacR7dz8RBFnsfAKMaMuwUNYX6aQbBcovzDPyrQF9KXF9tVU6Xk3K8no1BywnJX6GvZX8yJsXvt";
const STAGE_SUB = "7BnERTpvL5MbCLtj5n9No7J5oE5hHiB3tVCK5cjSvCsYWD2WRJLFuWeKTLiXo5QJqt2ZwUaLy2Vh1Ad51K7FNgqcHgjW85o";
const TEST = "9tTLtauaEKSj7xoVXytVH32R1pLZBk4VV4mZFGEh4wkXhDWqw1soPyf3fGixf1kni31VznEZkWNEza9d5TvjWwq5PaohYHC";

test("Monero: a standard address or subaddress of the build's network, with its checksum", () => {
  const m = P.xmr(MAIN, "mainnet");
  assert.ok(m.ok); assert.equal(m.sub, false); assert.match(m.warn, /subaddress/); // accepted, and the subaddress recommended
  assert.deepEqual(P.xmr(STAGE_SUB, "simulator"), { ok: true, sub: true });
  assert.ok(P.xmr(STAGE, "testnet").ok);
  assert.ok(P.xmr(TEST, "simulator").ok);
  assert.ok(P.xmr("  " + STAGE_SUB + " ", "testnet").ok);
});

test("Monero: refused, each with its reason", () => {
  assert.match(P.xmr(MAIN, "simulator").why, /not a Monero stagenet or testnet address/);
  assert.match(P.xmr(STAGE_SUB, "mainnet").why, /not a Monero mainnet address/);
  // one character changed: the checksum no longer holds
  const typo = STAGE_SUB.slice(0, 40) + (STAGE_SUB[40] === "a" ? "b" : "a") + STAGE_SUB.slice(41);
  assert.match(P.xmr(typo, "simulator").why, /checksum|base58/);
  assert.match(P.xmr(MAIN.slice(0, 94), "mainnet").why, /95 characters/);
  assert.match(P.xmr(MAIN + "11111111111", "mainnet").why, /integrated/);
  assert.match(P.xmr("0".repeat(95), "mainnet").why, /base58/);
  assert.match(P.xmr("", "mainnet").why, /Enter/);
});

// a transparent Zcash address: base58check of a 2-byte prefix and a 20-byte hash
const B58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz", sha = (b) => createHash("sha256").update(b).digest();
const taddr = (pre) => { const p = Buffer.concat([Buffer.from(pre), randomBytes(20)]), b = Buffer.concat([p, sha(sha(p)).subarray(0, 4)]); let n = BigInt("0x" + b.toString("hex")), s = ""; while (n > 0n) { s = B58[Number(n % 58n)] + s; n /= 58n; } return s; };
const T1 = taddr([0x1c, 0xb8]), T3 = taddr([0x1c, 0xbd]), TM = taddr([0x1d, 0x25]), T2 = taddr([0x1c, 0xba]);

test("Zcash: transparent only, of the build's network", async () => {
  assert.ok(T1.startsWith("t1") && T3.startsWith("t3") && TM.startsWith("tm") && T2.startsWith("t2"));
  for (const a of [T1, T3]) assert.deepEqual(await P.zec(a, "mainnet"), { ok: true });
  for (const a of [TM, T2]) { assert.deepEqual(await P.zec(a, "testnet"), { ok: true }); assert.deepEqual(await P.zec(a, "simulator"), { ok: true }); }
  assert.match((await P.zec(TM, "mainnet")).why, /test-network address/);
  assert.match((await P.zec(T1, "simulator")).why, /mainnet address.*tm… or t2…/);
  // shielded and unified addresses: the bot can pay only a transparent one
  for (const a of ["zs1z7rejlpsa98s2rrrfkwmaxu53e4ue0ulcrw0h4x5g8jl04tak0d3mm47vdtahatqrlkngh9sly", "u1qwertyuiopasdfghjklzxcvbnm", "ztestsapling1abc", "tex1s2rt77ggv6q989lr49rkgzmh5slsksa9khdgte"])
    assert.match((await P.zec(a, "mainnet")).why, /transparent/);
  const typo = T1.slice(0, 20) + (T1[20] === "a" ? "b" : "a") + T1.slice(21);
  assert.match((await P.zec(typo, "mainnet")).why, /checksum|Not a Zcash/);
  assert.match((await P.zec("t1short", "mainnet")).why, /Not a Zcash transparent address/);
});
