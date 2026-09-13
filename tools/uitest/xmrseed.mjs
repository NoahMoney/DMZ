// xmrseed: seeds htlc.bas locks on the DERO simulator for the Monero swap screen's UI checks (ui.mjs reads the
// result from XSEED, default .sim/xmr-seed.json). The simulator's own htlc.bas is installed with mainnet's windows
// (w1min = w2min = 400, g2 = 4,800), so no lock of it reaches its Punish window in a UI pass; this installs a second
// copy of the same template (app/templates/htlc.bas, the same code hash) with w1min = w2min = 1 and g2 = 60 blocks,
// and locks three times:
//
//   punish   maker A for taker B, t2 = its height + 6: in its Punish window from about 2 minutes after seeding, for 18
//   claim    maker A for taker B, t1 = its height + 240: in its Claim window for about an hour
//   refund   maker B for taker A, t1 = its height + 4, t2 = t1 + 150: in its Refund window for about 45 minutes
//
// Each lock carries 2,000 atomic units of DERO under three hashes of random secrets, written to the seed file so a
// later run could still send the maker's exits. Simulator only: the daemon must say so.
//
//   node tools/uitest/xmrseed.mjs [--maker=127.0.0.1:30014] [--taker=127.0.0.1:30015] [--daemon=127.0.0.1:20000] [--out=.sim/xmr-seed.json]
import fs from "node:fs";
import crypto from "node:crypto";

const opt = (k, d) => (process.argv.find((a) => a.startsWith("--" + k + "=")) || "").slice(k.length + 3) || d;
const daemon = opt("daemon", "127.0.0.1:20000"), A = opt("maker", "127.0.0.1:30014"), B = opt("taker", "127.0.0.1:30015"), out = opt("out", ".sim/xmr-seed.json");
const W1 = 1, W2 = 1, G2 = 60, VALUE = 2000;
const rpc = async (host, method, params) => {
  const r = await fetch("http://" + host + "/json_rpc", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(params === undefined ? { jsonrpc: "2.0", id: "1", method } : { jsonrpc: "2.0", id: "1", method, params }) });
  const j = await r.json(); if (j.error) throw new Error(host + " " + method + ": " + j.error.message); return j.result;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const sha = (b) => crypto.createHash("sha256").update(b).digest("hex");
const say = (s) => console.log(new Date().toISOString().slice(11, 19) + " " + s);

async function mined(txid) {
  for (let i = 0; i < 200; i++) {
    const r = await rpc(daemon, "DERO.GetTransaction", { txs_hashes: [txid] }).catch(() => null), t = r && r.txs && r.txs[0];
    if (t && t.valid_block && !t.in_pool) { while ((await rpc(daemon, "DERO.GetHeight")).height <= t.block_height) await sleep(1000); return t.block_height; }
    await sleep(1500);
  }
  throw new Error("tx " + txid + " not mined");
}
const keys = async (scid, ks) => { const r = await rpc(daemon, "DERO.GetSC", { scid, code: false, variables: false, keysstring: ks }); return Object.fromEntries(ks.map((k, i) => [k, r.valuesstring[i]])); };

const info = await rpc(daemon, "DERO.GetInfo");
if (!/simulator/i.test(info.network)) { console.error("the daemon reports " + info.network + ": this seeds the simulator only"); process.exit(2); }
const addr = async (w) => (await rpc(w, "GetAddress")).address;
const [ma, ta] = [await addr(A), await addr(B)];
for (const [w, a] of [[A, ma], [B, ta]]) say(w + " " + a + " holds " + (await rpc(w, "GetBalance")).balance + " atomic units");

const code = fs.readFileSync(new URL("../../app/templates/htlc.bas", import.meta.url), "utf8");
say("template sha256 " + sha(code));
const inst = await rpc(A, "transfer", { sc: code, sc_rpc: [{ name: "w1min", datatype: "U", value: W1 }, { name: "w2min", datatype: "U", value: W2 }, { name: "g2", datatype: "U", value: G2 }], ringsize: 2, fees: code.length + 712 });
const htlc = inst.txid; await mined(htlc);
const got = await rpc(daemon, "DERO.GetSC", { scid: htlc, code: true, variables: false });
if (got.code !== code) throw new Error("install " + htlc + " did not execute");
say("INSTALL " + htlc + " (w1min " + W1 + ", w2min " + W2 + ", g2 " + G2 + ")");

async function lock(w, taker, d1, d2) {
  const s = [0, 1, 2].map(() => crypto.randomBytes(32)), h = s.map(sha), height = (await rpc(daemon, "DERO.GetHeight")).height;
  const t1 = height + d1, t2 = t1 + d2, dst = (await rpc(daemon, "DERO.GetRandomAddress", {})).address[0];
  const n0 = Number((await keys(htlc, ["n"])).n);
  const r = await rpc(w, "transfer", { scid: htlc, ringsize: 2, fees: 4000, transfers: [{ destination: dst, amount: 0, burn: VALUE }],
    sc_rpc: [{ name: "entrypoint", datatype: "S", value: "Lock" }, { name: "ha", datatype: "S", value: h[0] }, { name: "hk", datatype: "S", value: h[1] }, { name: "hb", datatype: "S", value: h[2] },
      { name: "taker", datatype: "S", value: taker }, { name: "t1", datatype: "U", value: t1 }, { name: "t2", datatype: "U", value: t2 }, { name: "asset", datatype: "S", value: "" }] });
  const at = await mined(r.txid), k = await keys(htlc, ["n", "t1_" + n0, "st_" + n0]);
  if (Number(k.n) !== n0 + 1 || k["t1_" + n0] !== String(t1)) throw new Error("lock " + r.txid + " did not execute: " + JSON.stringify(k));
  say("LOCK #" + n0 + " by " + w + " for " + taker.slice(0, 12) + "…: tx " + r.txid + " in block " + at + ", t1 " + t1 + ", t2 " + t2 + ", Punish until " + (t2 + G2));
  return { id: n0, txid: r.txid, block: at, t1, t2, sa: s[0].toString("hex"), kb: s[1].toString("hex"), sb: s[2].toString("hex") };
}
// three separate transactions from two wallets: the refund lock's maker is B, so its wallet sends it
const punish = await lock(A, ta, 4, 2), claim = await lock(A, ta, 240, 60), refund = await lock(B, ma, 4, 150);
fs.mkdirSync(out.replace(/[\\/][^\\/]*$/, ""), { recursive: true });
fs.writeFileSync(out, JSON.stringify({ htlc, w1min: W1, w2min: W2, g2: G2, makerA: ma, makerB: ta, punish, claim, refund }, null, 2));
say("wrote " + out);
