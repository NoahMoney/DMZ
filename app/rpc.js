// DMZ rpc.js — node access (WebSocket JSON-RPC to the daemon, or DERO.* proxied through the wallet), plus the
// byte/hex/bech32 helpers every other module needs. No server anywhere: reads go to the user's own node.
(function () {
  const V = (window.Veil = window.Veil || {});

  // ---- bytes ----------------------------------------------------------------------------------------------
  const hexToBytes = (h) => { const a = new Uint8Array(h.length / 2); for (let i = 0; i < a.length; i++) a[i] = parseInt(h.substr(i * 2, 2), 16); return a; };
  const bytesToHex = (b) => Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  const hexToStr = (h) => { try { return new TextDecoder().decode(hexToBytes(h)); } catch (e) { return ""; } };
  async function sha256Hex(text) {
    const d = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
    return bytesToHex(new Uint8Array(d));
  }

  // ---- bech32 (BIP173, checksum constant 1 — what derohe/rpc/bech32.go implements) ------------------------
  const CS = "qpzry9x8gf2tvdw0s3jn54khce6mua7l";
  const GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3];
  function polymod(values) {
    let chk = 1;
    for (const v of values) { const top = chk >>> 25; chk = ((chk & 0x1ffffff) << 5) ^ v; for (let i = 0; i < 5; i++) if ((top >>> i) & 1) chk ^= GEN[i]; }
    return chk >>> 0;
  }
  const hrpExpand = (hrp) => [...hrp].map((c) => c.charCodeAt(0) >>> 5).concat([0], [...hrp].map((c) => c.charCodeAt(0) & 31));
  function bech32Encode(hrp, data) {
    const values = hrpExpand(hrp).concat(data);
    const pm = polymod(values.concat([0, 0, 0, 0, 0, 0])) ^ 1;
    const chk = []; for (let i = 0; i < 6; i++) chk.push((pm >>> (5 * (5 - i))) & 31);
    return hrp + "1" + data.concat(chk).map((d) => CS[d]).join("");
  }
  function bech32Decode(s) {
    s = s.toLowerCase(); const pos = s.lastIndexOf("1");
    if (pos < 1 || pos + 7 > s.length) return null;
    const hrp = s.slice(0, pos), data = [];
    for (const c of s.slice(pos + 1)) { const d = CS.indexOf(c); if (d < 0) return null; data.push(d); }
    if (polymod(hrpExpand(hrp).concat(data)) !== 1) return null;
    return { hrp, data: data.slice(0, -6) };
  }
  function convertBits(data, from, to, pad) {
    let acc = 0, bits = 0; const out = []; const max = (1 << to) - 1;
    for (const v of data) { acc = (acc << from) | v; bits += from; while (bits >= to) { bits -= to; out.push((acc >>> bits) & max); } }
    if (pad) { if (bits) out.push((acc << (to - bits)) & max); } else if (bits >= from || ((acc << (to - bits)) & max)) return null;
    return out;
  }
  // Address payload (rpc/address.go UnmarshalText): version byte 1, 33-byte compressed key, optional integrated
  // arguments. Canonical form = the string the DVM writes (ADDRESS_STRING: mainnet "dero" prefix, no arguments).
  function canonAddress(addr) {
    const d = bech32Decode(addr); if (!d || !(d.hrp === "dero" || d.hrp === "deto" || d.hrp === "deroi" || d.hrp === "detoi")) return null;
    const bytes = convertBits(d.data, 5, 8, false); if (!bytes || bytes.length < 34 || bytes[0] !== 1) return null;
    return bech32Encode("dero", convertBits(bytes.slice(0, 34), 8, 5, true));
  }
  // raw 33-byte key (hex, as stored by contracts) → canonical address string
  function rawToAddress(rawHex) {
    if (!/^[0-9a-f]{66}$/i.test(rawHex)) return null;
    return bech32Encode("dero", convertBits([1, ...hexToBytes(rawHex)], 8, 5, true));
  }
  const isAddress = (a) => !!canonAddress(a);
  const shortAddr = (a) => (a && a.length > 14 ? a.slice(0, 8) + "…" + a.slice(-5) : a || "");

  // ---- exact JSON: BigInt values become bare integer literals; nothing is ever parsed back into a Number -----
  // (a JSON.parse round-trip would round any amount above 2^53 before the wallet sees it)
  function jsonBig(v) {
    if (typeof v === "bigint") return v.toString();
    if (v === null || typeof v !== "object") return v === undefined ? "null" : JSON.stringify(v);
    if (Array.isArray(v)) return "[" + v.map(jsonBig).join(",") + "]";
    return "{" + Object.keys(v).filter((k) => v[k] !== undefined).map((k) => JSON.stringify(k) + ":" + jsonBig(v[k])).join(",") + "}";
  }

  // ---- JSON-RPC over WebSocket ----------------------------------------------------------------------------
  class WSRPC {
    constructor(url) { this.url = url; this.ws = null; this.id = 0; this.pending = new Map(); this.ready = null; this.onclose = null; }
    connect() {
      if (this.ready) return this.ready;
      this.ready = new Promise((resolve, reject) => {
        let ws; try { ws = new WebSocket(this.url); } catch (e) { this.ready = null; return reject(e); }
        const t = setTimeout(() => { try { ws.close(); } catch (e) {} reject(new Error("timeout " + this.url)); }, 6000);
        ws.onopen = () => { clearTimeout(t); this.ws = ws; resolve(this); };
        ws.onerror = () => { clearTimeout(t); this.ready = null; reject(new Error("cannot reach " + this.url)); };
        ws.onclose = () => { this.ws = null; this.ready = null; for (const p of this.pending.values()) p.reject(new Error("connection closed")); this.pending.clear(); if (this.onclose) this.onclose(); };
        ws.onmessage = (ev) => { let m; try { m = JSON.parse(ev.data); } catch (e) { return; } const p = this.pending.get(String(m.id)); if (!p) return; this.pending.delete(String(m.id)); m.error ? p.reject(Object.assign(new Error(m.error.message || "rpc error"), { code: m.error.code })) : p.resolve(m.result); };
      });
      return this.ready;
    }
    async call(method, params) {
      await this.connect();
      const id = String(++this.id);
      return new Promise((resolve, reject) => {
        this.pending.set(id, { resolve, reject });
        const body = { jsonrpc: "2.0", id, method }; if (params !== undefined) body.params = params;
        try { this.ws.send(jsonBig(body)); } catch (e) { this.pending.delete(id); reject(e); }
        setTimeout(() => { if (this.pending.has(id)) { this.pending.delete(id); reject(new Error(method + " timed out")); } }, 25000);
      });
    }
    close() { try { this.ws && this.ws.close(); } catch (e) {} }
  }

  // endpointURL is the only way an endpoint string becomes a socket. host:port and ws:// are accepted for this
  // device only (127.0.0.1, localhost); a remote node must be wss:// (the CSP allows nothing else). Returns
  // { url, loopback } or null. Applied to settings, to the wallet's GetDaemon and to the shipped default alike.
  function endpointURL(ep) {
    if (typeof ep !== "string") return null;
    let s = ep.trim().toLowerCase(); if (!s || s.length > 200) return null;
    let scheme = "ws";
    if (s.startsWith("wss://")) { scheme = "wss"; s = s.slice(6); } else if (s.startsWith("ws://")) s = s.slice(5); else if (/^[a-z]+:\/\//.test(s)) return null;
    s = s.replace(/\/ws\/?$/, "").replace(/\/$/, "");
    const m = /^([a-z0-9.-]+)(?::(\d{1,5}))?$/.exec(s); if (!m) return null;
    const host = m[1], port = m[2] ? Number(m[2]) : scheme === "wss" ? 443 : 0;
    if (!port || port > 65535 || host.length > 253) return null;
    const loopback = host === "127.0.0.1" || host === "localhost";
    if (!loopback && scheme !== "wss") return null;
    return { url: scheme + "://" + host + ":" + port + "/ws", loopback };
  }

  // toBig accepts what a wallet or node may return for an amount (a JSON number or a decimal string) and
  // refuses everything else with null instead of a thrown BigInt error. Every amount that crosses the wallet
  // boundary goes through it.
  function toBig(v) {
    if (typeof v === "bigint") return v;
    if (typeof v === "number") return Number.isSafeInteger(v) && v >= 0 ? BigInt(v) : null;
    if (typeof v === "string" && /^\d{1,20}$/.test(v)) return BigInt(v);
    return null;
  }

  // ---- Node facade: daemon WS first, XSWD DERO.* proxy second --------------------------------------------
  const node = {
    endpoint: null, ws: null, proxy: null, // proxy = function(method, params) provided by xswd.js
    setEndpoint(ep) { if (this.ws) this.ws.close(); const u = endpointURL(ep); this.endpoint = u ? u.url : null; this.ws = u ? new WSRPC(u.url) : null; },
    async call(method, params) {
      if (this.ws) { try { return await this.ws.call(method, params); } catch (e) { if (!this.proxy) throw e; } }
      if (this.proxy) return this.proxy(method, params);
      throw new Error("no node connection");
    },
    async height() { const r = await this.call("DERO.GetHeight"); return r.height; },
    async info() { return this.call("DERO.GetInfo"); },
    // Variables come back with uint64 as numbers and strings hex-encoded (DECISIONS §3); normalise here.
    async getSC(scid, opt = {}) {
      const p = { scid, variables: !!opt.variables, code: !!opt.code };
      if (opt.keys) p.keysstring = opt.keys;
      const r = await this.call("DERO.GetSC", p);
      // uint64 values arrive as JSON numbers (exact only below 2^53: ask for exact ones through `keys`),
      // string values arrive hex-encoded. vars: numbers → BigInt, strings → hex; strs: strings decoded as text.
      const vars = {}, strs = {};
      if (r.stringkeys) for (const [k, v] of Object.entries(r.stringkeys)) { if (typeof v === "number") vars[k] = BigInt(Math.trunc(v)); else if (typeof v === "string") { vars[k] = v; strs[k] = hexToStr(v); } }
      // requested keys: uint64 → decimal string, string → hex, missing → "NOT AVAILABLE …"
      const vals = {};
      if (opt.keys && r.valuesstring) opt.keys.forEach((k, i) => { const v = r.valuesstring[i]; if (typeof v === "string" && !v.startsWith("NOT AVAILABLE") && v !== "Unmarshal error") vals[k] = v; });
      return { vars, strs, vals, balances: r.balances || {}, balance: r.balance, code: r.code || "" };
    },
    async getTx(txid) { const r = await this.call("DERO.GetTransaction", { txs_hashes: [txid] }); return r.txs && r.txs[0]; },
    async gasEstimate(p) { return this.call("DERO.GetGasEstimate", p); },
    async randomAddress() { const r = await this.call("DERO.GetRandomAddress", {}); return r.address && r.address[0]; },
  };

  Object.assign(V, { jsonBig, endpointURL, toBig, hexToBytes, bytesToHex, hexToStr, sha256Hex, bech32Encode, bech32Decode, convertBits, canonAddress, rawToAddress, isAddress, shortAddr, WSRPC, node });
})();
