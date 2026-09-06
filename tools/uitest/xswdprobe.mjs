// Minimal XSWD handshake probe from Node (no browser): node tools/uitest/xswdprobe.mjs [ws://localhost:44326/xswd]
const url = process.argv[2] || "ws://localhost:44326/xswd";
const ws = new WebSocket(url);
const log = (...a) => console.log(new Date().toISOString().slice(11, 19), ...a);
let id = 0;
const send = (o) => { log("→", JSON.stringify(o).slice(0, 200)); ws.send(JSON.stringify(o)); };
ws.onopen = () => { log("open"); send({ id: "a".repeat(64), name: "Veil probe", description: "handshake probe", url: "http://127.0.0.1:8082" }); };
ws.onerror = (e) => { log("error", e.message || e); };
ws.onclose = (e) => { log("close", e.code, e.reason); process.exit(0); };
ws.onmessage = (m) => {
  log("←", String(m.data).slice(0, 300));
  const r = JSON.parse(m.data);
  if (r.accepted) send({ jsonrpc: "2.0", id: String(++id), method: "GetAddress" });
  else if (r.result && r.result.address) send({ jsonrpc: "2.0", id: String(++id), method: "DERO.GetHeight" });
  else if (r.result && r.result.height !== undefined) send({ jsonrpc: "2.0", id: String(++id), method: "GetDaemon" });
  else if (r.result && r.result.endpoint) { log("probe ok"); ws.close(); }
};
setTimeout(() => { log("timeout"); process.exit(1); }, 20000);
