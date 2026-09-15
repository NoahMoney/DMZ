// tools/uitest/scale/mock.mjs — a TEST-ONLY mock of a DERO node for the scale harness (docs/PERFORMANCE.md).
// It serves N synthetic markets with the keys the shipped contracts write (launch.bas / pool.bas: the fixed state
// keys, p_<n> price points, t_<n> published trades) behind a factory with pool_/launch_ slots, over the same
// WebSocket JSON-RPC the app uses, and serves the app itself with a generated config.js. Nothing here is shipped
// in app/, and nothing it measures is a product number: the chain is invented, the node answers in microseconds.
//
// State is versioned by height, so a read pinned to a topoheight sees the state as it was then, as a real node does.
// Every block, about `activity` of the markets trade (weighted toward the busy ones), and every `regEvery` blocks a
// new launch registers, so the registry grows while the app runs.
import http from "node:http";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
const hexOf = (s) => Buffer.from(String(s), "utf8").toString("hex");
const sha = (s) => crypto.createHash("sha256").update(s).digest("hex");

// rpc.js gives the bech32 encoding the app itself uses, so every generated address is one it accepts
function rpcHelpers() {
  const w = { Veil: {} };
  new Function("window", fs.readFileSync(path.join(ROOT, "app", "rpc.js"), "utf8"))(w);
  return w.Veil;
}

function rng(seed) {
  let a = seed >>> 0;
  return () => { a = (a + 0x6d2b79f5) >>> 0; let t = a; t = Math.imul(t ^ (t >>> 15), t | 1); t ^= t + Math.imul(t ^ (t >>> 7), t | 61); return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
}

export async function startMock(o) {
  const n = o.n, blockMs = o.blockMs || 3000, activity = o.activity ?? 0.01, regEvery = o.regEvery || 10, rtt = o.rtt || 0;
  const appDir = o.appDir || path.join(ROOT, "app");
  const R = rng(o.seed || 1), V = rpcHelpers();
  const rawKey = () => "02" + crypto.createHash("sha256").update(String(R())).digest("hex");
  const tRaw = rawKey(), treasury = V.rawToAddress(tRaw);
  const traders = Array.from({ length: 200 }, () => V.rawToAddress(rawKey()));
  const code = { launch: fs.readFileSync(path.join(ROOT, "app", "templates", "launch.bas"), "utf8"), pool: fs.readFileSync(path.join(ROOT, "app", "templates", "pool.bas"), "utf8") };
  const factory = sha("factory" + (o.seed || 1));
  let height = 100000;
  const now = () => Math.floor(Date.now() / 1000);

  // ---- the chain ----
  const markets = new Map(), slots = { launch: [], pool: [] }; // slots: [{scid, h}] in registration order
  const weight = [];
  function addMarket(i, kind, h, pc) {
    const scid = sha("market" + (o.seed || 1) + ":" + i);
    const st = {
      fee: 50, pfee: 5000, dec: 5, treasury: tRaw, creator: rawKey(),
      ...(kind === "launch" ? { nameHdr: hexOf("Token " + i), ticker: hexOf("T" + i.toString(36).toUpperCase()), descrHdr: hexOf("A synthetic market for the scale harness, number " + i), iconURLHdr: "", tagsHdr: hexOf("scale,test"), S: 100000000000000, tax: 0, tmode: 0, cs: 0, Lq: 0, G: 50000000000, vd0: 1000000000, gh: 0, stot: 0, sacc: 0, tacc: 0, rpool: 0, locked: 0 } : {}),
    };
    if (kind === "pool") { const l = slots.launch.length && R() < 0.5 ? slots.launch[Math.floor(R() * slots.launch.length)].scid : sha("asset" + i); st.asset = l; }
    const m = { scid, kind, st, installH: h, versions: [], p: [], t: [] };
    let rd = 1e9 + Math.floor(R() * 1e11), ra = 1e12 + Math.floor(R() * 1e13), ts = now() - pc * (18 + Math.floor(R() * 200)), bh = h;
    for (let k = 0; k < pc; k++) {
      ts += 18 + Math.floor(R() * 200); bh += 1 + Math.floor(R() * 10);
      const side = R() < 0.5 ? 1 : 2, d = Math.floor(rd * 0.002 * (0.2 + R()));
      if (side === 1) { rd += d; ra -= Math.floor(ra * d / rd); } else { rd -= d; ra += Math.floor(ra * d / rd); }
      m.p.push({ h, s: hexOf([bh, ts, rd, ra].join("|")) });
      if (R() < 0.6) m.t.push({ h, s: hexOf([side, traders[Math.floor(R() * traders.length)], d, Math.floor(d * 1.3), bh, ts].join("|")) });
    }
    m.versions.push({ h, rd, ra, lp: 1000000, fd: 0, fa: 0, tc: m.t.length, pc: m.p.length, lh: bh, lt: ts, phase: kind === "launch" ? (R() < 0.8 ? 1 : 0) : 0, real: Math.floor(rd / 3), sold: 1000000 });
    markets.set(scid, m); slots[kind].push({ scid, h });
    return m;
  }
  const perm = Array.from({ length: n }, (_, i) => i).sort(() => R() - 0.5); // rank of market i
  for (let i = 0; i < n; i++) {
    const rank = perm[i] + 1, pc = Math.min(6000, Math.floor(3000 / Math.pow(rank, 0.9)));
    addMarket(i, i % 10 < 7 ? "launch" : "pool", height - 5000 + i, pc);
    weight.push(1 / Math.pow(rank, 0.9));
  }
  let wsum = weight.reduce((a, b) => a + b, 0);
  const pick = () => { let x = R() * wsum; for (let i = 0; i < weight.length; i++) { x -= weight[i]; if (x <= 0) return i; } return weight.length - 1; };
  const ids = [...markets.keys()];

  const at = (arr, T) => { let lo = 0, hi = arr.length - 1, r = -1; while (lo <= hi) { const mid = (lo + hi) >> 1; if (arr[mid].h <= T) { r = mid; lo = mid + 1; } else hi = mid - 1; } return r; };
  const cnt = (arr, T) => at(arr, T) + 1; // arrays are appended in height order
  function block() {
    height++;
    const k = Math.max(1, Math.round(n * activity));
    for (let j = 0; j < k; j++) {
      const m = markets.get(ids[pick()]), v = Object.assign({}, m.versions[m.versions.length - 1]), ts = now();
      const side = R() < 0.5 ? 1 : 2, d = Math.max(1, Math.floor(v.rd * 0.002 * (0.2 + R())));
      if (side === 1) { v.rd += d; v.ra -= Math.floor(v.ra * d / v.rd); } else { v.rd -= d; v.ra += Math.floor(v.ra * d / v.rd); }
      m.p.push({ h: height, s: hexOf([height, ts, v.rd, v.ra].join("|")) });
      m.t.push({ h: height, s: hexOf([side, traders[Math.floor(R() * traders.length)], d, Math.floor(d * 1.3), height, ts].join("|")) });
      Object.assign(v, { h: height, pc: m.p.length, tc: m.t.length, lh: height, lt: ts });
      if (m.versions[m.versions.length - 1].h === height) m.versions[m.versions.length - 1] = v; else m.versions.push(v);
    }
    if (regEvery && height % regEvery === 0) { const i = ids.length; addMarket(i, "launch", height, 3); ids.push([...markets.keys()].pop()); weight.push(1 / Math.pow(i + 1, 0.9)); wsum += weight[weight.length - 1]; }
    for (const f of waiters.splice(0)) f(height);
  }
  const waiters = [];
  const timer = setInterval(block, blockMs);

  // ---- JSON-RPC ----
  const stats = { calls: {}, keys: 0, bytes: 0 };
  const count = (m, k, b) => { stats.calls[m] = (stats.calls[m] || 0) + 1; stats.keys += k; stats.bytes += b; };
  const NA = "NOT AVAILABLE err: leaf not found";
  function valueOf(scid, key, T) {
    if (scid === factory) {
      const fk = { pc: cnt(slots.pool, T), lc: cnt(slots.launch, T), nc: 0, regfee: 10000, tstr: hexOf(treasury), v: 2 };
      if (key in fk) return String(fk[key]);
      const mm = /^(pool|launch)_(\d+)$/.exec(key);
      if (mm) { const s = slots[mm[1]][+mm[2]]; return s && s.h <= T ? hexOf(s.scid) : NA; }
      return NA;
    }
    const m = markets.get(scid); if (!m || m.installH > T) return NA;
    const vi = at(m.versions, T), v = m.versions[vi];
    if (key in v && key !== "h") return String(v[key]);
    if (key in m.st) return String(m.st[key]);
    const hm = /^([pt])_(\d+)$/.exec(key);
    if (hm) { const arr = hm[1] === "p" ? m.p : m.t, i = +hm[2]; return i < (hm[1] === "p" ? v.pc : v.tc) && arr[i] ? arr[i].s : NA; }
    return NA;
  }
  function handle(req) {
    const p = req.params || {}, T = p.topoheight > 0 ? Math.min(p.topoheight, height) : height;
    switch (req.method) {
      case "DERO.GetHeight": return { height, stableheight: height - 8, topoheight: height, status: "OK" };
      case "DERO.GetInfo": return { network: "simulator", height, topoheight: height, stableheight: height - 8, status: "OK" };
      case "DERO.GetTransaction": { const m = markets.get((p.txs_hashes || [])[0]); return { txs: [m ? { block_height: m.installH, valid_block: true, in_pool: false } : {}], status: "OK" }; }
      case "DERO.GetSC": {
        const m = markets.get(p.scid), keys = p.keysstring || [];
        return { valuesstring: keys.map((k) => valueOf(p.scid, k, T)), balance: 0, code: p.code && m && m.installH <= T ? code[m.kind] : "", status: "OK" };
      }
      default: throw Object.assign(new Error("method not found"), { code: -32601 });
    }
  }

  // ---- WebSocket (RFC 6455, text frames only: all a JSON-RPC client sends) ----
  const frame = (s) => { const b = Buffer.from(s), l = b.length; const h = l < 126 ? Buffer.from([0x81, l]) : l < 65536 ? Buffer.from([0x81, 126, l >> 8, l & 255]) : (() => { const x = Buffer.alloc(10); x[0] = 0x81; x[1] = 127; x.writeBigUInt64BE(BigInt(l), 2); return x; })(); return Buffer.concat([h, b]); };
  const sockets = new Set();
  function onUpgrade(req, sock) {
    const acc = crypto.createHash("sha1").update(req.headers["sec-websocket-key"] + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").digest("base64");
    sock.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + acc + "\r\n\r\n");
    sockets.add(sock); sock.on("close", () => sockets.delete(sock)); sock.on("error", () => {});
    let buf = Buffer.alloc(0);
    sock.on("data", (d) => {
      buf = Buffer.concat([buf, d]);
      for (;;) {
        if (buf.length < 2) return;
        const op = buf[0] & 15; let len = buf[1] & 127, off = 2;
        if (len === 126) { if (buf.length < 4) return; len = buf.readUInt16BE(2); off = 4; } else if (len === 127) { if (buf.length < 10) return; len = Number(buf.readBigUInt64BE(2)); off = 10; }
        if (buf.length < off + 4 + len) return;
        const mask = buf.subarray(off, off + 4), body = Buffer.from(buf.subarray(off + 4, off + 4 + len));
        for (let i = 0; i < body.length; i++) body[i] ^= mask[i & 3];
        buf = buf.subarray(off + 4 + len);
        if (op === 8) { sock.end(); return; }
        if (op === 9) { sock.write(Buffer.concat([Buffer.from([0x8a, body.length]), body])); continue; }
        if (op !== 1) continue;
        let req; try { req = JSON.parse(body.toString()); } catch (e) { continue; }
        let out;
        try { out = JSON.stringify({ jsonrpc: "2.0", id: req.id, result: handle(req) }); } catch (e) { out = JSON.stringify({ jsonrpc: "2.0", id: req.id, error: { code: e.code || -32000, message: e.message } }); }
        count(req.method, ((req.params || {}).keysstring || []).length, out.length);
        const send = () => { if (!sock.destroyed) sock.write(frame(out)); };
        rtt ? setTimeout(send, rtt) : send();
      }
    });
  }

  // ---- the app, with a generated config.js naming this node and this factory ----
  const cfg = { network: "simulator", daemon: "", factory, treasuries: [treasury], vaults: [], feeBps: 50, pfeeBps: 5000, launchPfeeBps: 5000,
    templates: { launch: { file: "templates/launch.bas", sha256: sha(code.launch), legacy: [] }, pool: { file: "templates/pool.bas", sha256: sha(code.pool), legacy: [] }, nft: { sha256: "0".repeat(64), legacy: [] }, nfa: { sha256: "1".repeat(64), legacy: [] }, founders: { sha256: "2".repeat(64), legacy: [] } } };
  const TYPES = { ".js": "text/javascript", ".css": "text/css", ".html": "text/html", ".svg": "image/svg+xml", ".png": "image/png", ".bas": "text/plain" };
  const server = http.createServer((req, res) => {
    const u = decodeURIComponent(req.url.split("?")[0].split("#")[0]);
    if (u === "/config.js") { res.writeHead(200, { "Content-Type": "text/javascript" }); return res.end("window.VEIL_CONFIG = " + JSON.stringify(cfg) + ";"); }
    const f = path.join(appDir, path.normalize(u === "/" ? "/index.html" : u));
    if (!f.startsWith(path.resolve(appDir))) { res.writeHead(403); return res.end(); }
    fs.readFile(f, (e, b) => { if (e) { res.writeHead(404); return res.end(); } res.writeHead(200, { "Content-Type": TYPES[path.extname(f)] || "application/octet-stream" }); res.end(b); });
  });
  server.on("upgrade", (req, sock) => (req.url === "/ws" ? onUpgrade(req, sock) : sock.destroy()));
  await new Promise((r) => server.listen(o.port || 0, "127.0.0.1", r));
  const port = server.address().port;
  cfg.daemon = "127.0.0.1:" + port;

  return {
    port, url: "http://127.0.0.1:" + port + "/index.html", factory,
    get height() { return height; },
    markets: () => markets.size,
    nextBlock: () => new Promise((r) => waiters.push(r)),
    stats: () => JSON.parse(JSON.stringify(stats)),
    reset: () => { stats.calls = {}; stats.keys = 0; stats.bytes = 0; },
    stop: () => { clearInterval(timer); for (const s of sockets) s.destroy(); return new Promise((r) => server.close(r)); },
  };
}

// standalone: node tools/uitest/scale/mock.mjs --n=500 [--port=8120] [--block-ms=3000] [--app=app] — open the printed URL
if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  const arg = (k, d) => { const a = process.argv.find((x) => x.startsWith("--" + k + "=")); return a ? a.slice(k.length + 3) : d; };
  const m = await startMock({ n: +arg("n", 500), port: +arg("port", 0), blockMs: +arg("block-ms", 3000), appDir: arg("app") && path.resolve(arg("app")), rtt: +arg("rtt", 0) });
  console.log("mock node with " + m.markets() + " markets: " + m.url);
}
