// statusstub: a stand-in for a swap program's -status endpoint (docs/SWAP_STATUS.md), for the UI pass only. It is
// never shipped in app/ and holds nothing: it serves one fixture, with the contract, network and lock the pass names,
// behind the same three gates the companions must keep (the Host check, the pairing code, no data before either).
//
//   node tools/uitest/statusstub.mjs --port=18095 --fixture=tests/js/fixtures/status-xmrswap-happy.json [--code=<32 hex>] [--contract=<id>] [--network=simulator] [--lock=N]
import http from "node:http";
import fs from "node:fs";
import crypto from "node:crypto";

const opt = (k) => (process.argv.find((a) => a.startsWith("--" + k + "=")) || "").slice(k.length + 3);
const port = Number(opt("port") || 18095), fixture = opt("fixture"), code = opt("code") || crypto.randomBytes(16).toString("hex");
if (!fixture || !/^[0-9a-f]{32}$/.test(code) || !(port >= 1024 && port <= 65535)) { console.error("usage: statusstub.mjs --port=N --fixture=<file> [--code=<32 hex>]"); process.exit(2); }
const body = () => {
  const j = JSON.parse(fs.readFileSync(fixture, "utf8"));
  if (opt("contract")) j.contract = opt("contract");
  if (opt("network")) j.network = opt("network");
  if (opt("lock")) j.lock = Number(opt("lock"));
  j.updated = new Date().toISOString();
  return JSON.stringify(j);
};
// the only Hosts it answers: a page on another site that rebinds its own name to 127.0.0.1 sends its own Host
const hosts = new Set(["127.0.0.1:" + port, "localhost:" + port]);
const want = Buffer.from("Bearer " + code);
http.createServer((req, res) => {
  if (!hosts.has(req.headers.host || "")) { res.writeHead(403); return res.end(); }
  const cors = { "Access-Control-Allow-Origin": "*", "Access-Control-Allow-Methods": "GET", "Access-Control-Allow-Headers": "Authorization", "Access-Control-Max-Age": "600", "Cache-Control": "no-store", Vary: "Origin" };
  if (req.method === "OPTIONS") { if (req.headers["access-control-request-private-network"]) cors["Access-Control-Allow-Private-Network"] = "true"; res.writeHead(204, cors); return res.end(); }
  if (req.method !== "GET" || req.url !== "/v1/status") { res.writeHead(404, cors); return res.end(); }
  const got = Buffer.from(req.headers.authorization || "");
  if (got.length !== want.length || !crypto.timingSafeEqual(got, want)) { res.writeHead(401, cors); return res.end(); }
  res.writeHead(200, Object.assign({ "Content-Type": "application/json" }, cors)); res.end(body());
}).listen(port, "127.0.0.1", () => console.log("statusstub on 127.0.0.1:" + port + " serving " + fixture));
