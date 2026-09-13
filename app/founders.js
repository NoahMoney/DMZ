// DMZ founders.js — the Founders Pass data layer, split out of feed.js (DOC size). Adds loadFounders / myFounders
// to V.data; feed.js calls loadFounders on every refresh. Same rules as every listing: code verified against its
// template hash once per session, fixed key reads, per-user keys by exact key only.
//
// founders.bas v2 keeps one reward pool per asset. A deposit of asset i raises ac_<i>, the reward per registered pass,
// and a registration is a position p (po_ owner, pu_ units, pt_ and pc_ the ticks it opened and closed at). Claim(p, i,
// ka, kb) pays p's owner pu × (to − from) in asset i: from is pd_<i>_<p> once p has claimed, else the accumulator at
// pt_; to is ac_<i> while p is open, else the accumulator at pc_. "The accumulator at tick t" is ra_<i>_<k> for the
// first record k with rt_<i>_<k> ≥ t, else ac_<i>: ka and kb are those k, found here by binary search with exact-key
// reads, and the contract checks each in O(1) (docs/FOUNDERS_V2_NOTES.md §4).
(function () {
  const V = window.Veil, B = BigInt, clean = (s, n) => V.ui.clean(s, n);
  const isNum = (v) => typeof v === "string" && /^\d+$/.test(v), isId = (v) => typeof v === "string" && /^[0-9a-f]{64}$/.test(v);
  const DERO = "0".repeat(64), POSN = 100; // positions read per refresh, newest first: a bounded window, like listings
  Object.assign(V.data, {
    founders: null,
    // The assets the pass page names: DERO, and every token with a verified DMZ market, as its token or as its quote.
    // Anyone can deposit any token and so give it an index; a token this app cannot name is never read, never shown
    // and never called a fee asset.
    passAssets() {
      const out = new Map([[DERO, { id: DERO, sym: "DERO", dec: 5n }]]);
      for (const s of this.assets.values()) {
        if (!s || s.untrusted || (s.kind !== "pool" && s.kind !== "launch")) continue;
        if (isId(s.asset) && !out.has(s.asset)) out.set(s.asset, { id: s.asset, sym: clean(s.ticker || "", 10) || s.asset.slice(0, 6), dec: typeof s.dec === "bigint" ? s.dec : 5n });
        if (isId(s.quote) && !out.has(s.quote)) out.set(s.quote, { id: s.quote, sym: clean(s.quoteSym || "", 10) || s.quote.slice(0, 6), dec: s.quoteDecUnknown ? null : B(Number(s.quoteDec) || 5) });
      }
      return [...out.values()];
    },
    async loadFounders(prev) {
      const scid = this.cfg.founders; if (!scid) return null;
      if ((await this.verify(scid)) !== "founders") { console.warn("founders contract does not match its template"); return null; }
      const NUM = ["v", "price", "maxsupply", "start", "reserve", "share", "mfee", "minted", "hst", "ev", "pn", "an", "ln", "la", "mdep"];
      const r = await V.node.getSC(scid, { keys: [...NUM, "nameHdr", "descrHdr", "iconURLHdr", "fileURL", "creator", "treasury"] });
      // this page reads v2's keys, so a pass of any other version is not shown, whatever template hash its code matched
      if (r.vals.v !== "2") { console.warn("founders contract is not a v2 pass"); return null; }
      const num = (k) => (isNum(r.vals[k]) ? B(r.vals[k]) : 0n), str = (k, n) => clean(V.hexToStr(r.vals[k] || ""), n);
      const f = { scid, kind: "founders", name: str("nameHdr", 64) || "Founders Pass", descr: str("descrHdr", 240), icon: str("iconURLHdr", 200), file: str("fileURL", 200), creator: this.rawToAddr(r.vals.creator || ""), treasury: this.rawToAddr(r.vals.treasury || ""), listings: [], assets: [] };
      for (const k of NUM) f[k] = num(k);
      // the named assets' indices by exact key (ai_<hex id> = i + 1), never by walking `an`, which anyone can grow
      const named = this.passAssets(), ai = await this.fetchKeys(scid, named.map((a) => "ai_" + a.id));
      f.assets = named.filter((a) => isNum(ai["ai_" + a.id]) && ai["ai_" + a.id] !== "0").map((a) => Object.assign({}, a, { i: B(ai["ai_" + a.id]) - 1n }));
      const AK = ["ac", "au", "ap", "ad", "aw", "ar"], got = f.assets.length ? await this.fetchKeys(scid, f.assets.flatMap((a) => AK.map((k) => k + "_" + a.i))) : {};
      for (const a of f.assets) for (const k of AK) a[k] = isNum(got[k + "_" + a.i]) ? B(got[k + "_" + a.i]) : 0n;
      f.sig = ["minted", "hst", "ev", "pn", "an", "ln", "la", "mdep"].map((k) => r.vals[k] || "").concat(f.assets.map((a) => a.i + ":" + a.ac + ":" + a.aw)).join("|");
      if (prev && prev.sig === f.sig) { f.listings = prev.listings; return f; }
      // listings: the last 300 ids (deleted ones simply do not exist)
      const from = Math.max(0, Number(f.ln) - 300), keys = []; for (let i = from; i < Number(f.ln); i++) keys.push("ls_" + i, "lp_" + i, "lu_" + i);
      const lg = keys.length ? await this.fetchKeys(scid, keys) : {};
      for (let i = from; i < Number(f.ln); i++) { const seller = V.hexToStr(lg["ls_" + i] || ""); if (V.canonAddress(seller) && isNum(lg["lp_" + i]) && isNum(lg["lu_" + i])) f.listings.push({ id: i, seller, price: B(lg["lp_" + i]), units: B(lg["lu_" + i]) }); }
      f.listings.sort((a, b) => (a.price < b.price ? -1 : a.price > b.price ? 1 : a.id - b.id));
      return f;
    },
    // The connected wallet's registered units, its positions with what each can claim per named asset (the contract's
    // arithmetic, exactly), its stored XMR and ZEC payout addresses, and its listings.
    async myFounders(addr) {
      const f = this.founders; if (!f || !addr) return null;
      const k = await this.fetchKeys(f.scid, ["hu_" + addr, "xm_" + addr, "zc_" + addr]);
      // xm_ and zc_ are strings, which the node returns as hex: always decoded, never read as a number
      const text = (x) => (typeof x === "string" && /^[0-9a-f]+$/.test(x) ? clean(V.hexToStr(x), 106) : "");
      const last = Number(f.pn) - 1, from = Math.max(0, Number(f.pn) - POSN), pk = [];
      for (let p = last; p >= from; p--) pk.push("po_" + p, "pu_" + p, "pt_" + p, "pc_" + p);
      const g = pk.length ? await this.fetchKeys(f.scid, pk) : {}, positions = [];
      for (let p = last; p >= from; p--) {
        if (!g["po_" + p] || this.rawToAddr(g["po_" + p]) !== addr || !["pu_", "pt_", "pc_"].every((x) => isNum(g[x + p]))) continue;
        positions.push({ p: B(p), u: B(g["pu_" + p]), pt: B(g["pt_" + p]), pc: B(g["pc_" + p]), owed: [] });
      }
      const rec = new Map();
      const read = async (key) => {
        if (!rec.has(key)) { const x = await this.fetchKeys(f.scid, [key]); rec.set(key, isNum(x[key]) ? B(x[key]) : null); }
        const v = rec.get(key); if (v === null) throw new Error("the pass has no " + key); return v;
      };
      // founders.bas at(): the first record k of asset a with rt >= t, or ar when there is none
      const hint = async (a, t) => { let lo = 0n, hi = a.ar; while (lo < hi) { const mid = (lo + hi) / 2n; if ((await read("rt_" + a.i + "_" + mid)) >= t) hi = mid; else lo = mid + 1n; } return lo; };
      const accAt = (a, kk) => (kk === a.ar ? a.ac : read("ra_" + a.i + "_" + kk));
      const pdk = positions.flatMap((q) => f.assets.map((a) => "pd_" + a.i + "_" + q.p)), pd = pdk.length ? await this.fetchKeys(f.scid, pdk) : {};
      for (const q of positions) for (const a of f.assets) {
        const c = pd["pd_" + a.i + "_" + q.p]; let ka = 0n, kb = 0n, lo, hi;
        if (isNum(c)) lo = B(c); else { ka = await hint(a, q.pt); lo = await accAt(a, ka); }
        if (q.pc === 0n) hi = a.ac; else { kb = await hint(a, q.pc); hi = await accAt(a, kb); }
        const amt = hi > lo ? q.u * (hi - lo) : 0n;
        // Claim refuses more than the pool owes (amt > ap_), a state the contract cannot reach: offer nothing then
        if (amt > 0n && amt <= a.ap) q.owed.push({ asset: a, amt, ka, kb });
      }
      return { units: isNum(k["hu_" + addr]) ? B(k["hu_" + addr]) : 0n, positions, windowed: from > 0, xmr: text(k["xm_" + addr]), zec: text(k["zc_" + addr]), listings: f.listings.filter((l) => l.seller === addr) };
    },
  });

  // ---- payout addresses (founders.bas SetXmr / SetZec) ----
  // The contract checks only the length and the characters. These check what the treasury's bot can pay: a Monero
  // standard address or subaddress of the build's network whose checksum holds, and a TRANSPARENT Zcash address of
  // the build's network, because the bot can pay only transparent addresses. Each answers { ok } or { why }.
  const A58 = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz", M64 = (1n << 64n) - 1n;
  // Keccak-256 with the original Keccak padding (Monero's cn_fast_hash, not SHA-3), for the address checksum
  function keccak256(msg) {
    const s = new Array(25).fill(0n), rate = 136, n = (Math.floor(msg.length / rate) + 1) * rate, p = new Uint8Array(n);
    p.set(msg); p[msg.length] ^= 1; p[n - 1] ^= 0x80;
    const rol = (x, k) => (k ? ((x << B(k)) | (x >> B(64 - k))) & M64 : x);
    for (let o = 0; o < n; o += rate) {
      for (let i = 0; i < 17; i++) { let w = 0n; for (let j = 7; j >= 0; j--) w = (w << 8n) | B(p[o + i * 8 + j]); s[i] ^= w; }
      for (let r = 0, R = 1; r < 24; r++) {
        const C = [0, 1, 2, 3, 4].map((x) => s[x] ^ s[x + 5] ^ s[x + 10] ^ s[x + 15] ^ s[x + 20]);
        for (let x = 0; x < 5; x++) { const d = C[(x + 4) % 5] ^ rol(C[(x + 1) % 5], 1); for (let y = 0; y < 25; y += 5) s[x + y] ^= d; }
        for (let t = 0, x = 1, y = 0, cur = s[1]; t < 24; t++) { [x, y] = [y, (2 * x + 3 * y) % 5]; const k = x + 5 * y, tmp = s[k]; s[k] = rol(cur, ((t + 1) * (t + 2) / 2) % 64); cur = tmp; }
        for (let y = 0; y < 25; y += 5) { const T = s.slice(y, y + 5); for (let x = 0; x < 5; x++) s[y + x] = T[x] ^ (~T[(x + 1) % 5] & M64 & T[(x + 2) % 5]); }
        for (let j = 0; j < 7; j++) { R = ((R << 1) ^ ((R >> 7) * 0x71)) & 255; if (R & 2) s[0] ^= 1n << B((1 << j) - 1); }
      }
    }
    const out = new Uint8Array(32); for (let i = 0; i < 32; i++) out[i] = Number((s[i >> 3] >> B(8 * (i & 7))) & 255n); return out;
  }
  // Monero's base58: 11 characters per 8-byte block, a short last block; null when it is not that
  const SIZES = [0, 2, 3, 5, 6, 7, 9, 10, 11];
  function xmr58(a) {
    const out = [];
    for (let i = 0; i < a.length; i += 11) {
      const blk = a.slice(i, i + 11), size = SIZES.indexOf(blk.length); if (size < 1) return null;
      let v = 0n; for (const c of blk) { const k = A58.indexOf(c); if (k < 0) return null; v = v * 58n + B(k); }
      if (v >> B(8 * size)) return null;
      for (let j = size - 1; j >= 0; j--) out.push(Number((v >> B(8 * j)) & 255n));
    }
    return new Uint8Array(out);
  }
  // prefixes: monero src/cryptonote_config.h (standard 18 / 24 / 53, subaddress 42 / 36 / 63, integrated 19 / 25 / 54)
  function xmrAddr(s, network) {
    const a = String(s || "").trim(), main = network === "mainnet", std = main ? [18] : [24, 53], sub = main ? [42] : [36, 63];
    const where = main ? "Monero mainnet" : "Monero stagenet or testnet";
    if (!a) return { why: "Enter a Monero address." };
    if (a.length === 106) return { why: "That is an integrated address, which carries a payment id. Store a subaddress instead." };
    if (a.length !== 95) return { why: "A Monero standard address or subaddress is 95 characters; that is " + a.length + "." };
    const b = xmr58(a);
    if (!b || b.length !== 69) return { why: "Not a Monero address: it is not in Monero's base58." };
    const sum = keccak256(b.subarray(0, 65));
    if ([0, 1, 2, 3].some((i) => sum[i] !== b[65 + i])) return { why: "The address's checksum does not match: check it for a typo." };
    if (!std.includes(b[0]) && !sub.includes(b[0])) return { why: "That is not a " + where + " address, and this build is for " + network + "." };
    return sub.includes(b[0]) ? { ok: true, sub: true } : { ok: true, sub: false, warn: "Valid, but a primary address. A stored address is public on the DERO chain for good, so a fresh subaddress you use for nothing else is the one to store." };
  }
  // transparent Zcash: base58check, 2-byte prefix. Mainnet t1 0x1cb8 / t3 0x1cbd; testnet and regtest tm 0x1d25 / t2 0x1cba
  async function zecAddr(s, network) {
    const a = String(s || "").trim(), main = network === "mainnet", want = main ? "t1… or t3…" : "tm… or t2…";
    if (!a) return { why: "Enter a Zcash transparent address." };
    if (/^[zu]/i.test(a) || /^tex1/i.test(a)) return { why: "That is not a transparent address. The treasury's bot can pay only a transparent one: " + want };
    let v = 0n;
    for (const c of a) { const k = A58.indexOf(c); if (k < 0) return { why: "Not a Zcash transparent address." }; v = v * 58n + B(k); }
    const b = []; while (v > 0n) { b.unshift(Number(v & 255n)); v >>= 8n; }
    if (b.length !== 26) return { why: "Not a Zcash transparent address." };
    const body = new Uint8Array(b.slice(0, 22)), sha = async (x) => new Uint8Array(await crypto.subtle.digest("SHA-256", x)), d = await sha(await sha(body));
    if (b.slice(22).some((x, i) => x !== d[i])) return { why: "The address's checksum does not match: check it for a typo." };
    const pre = (body[0] << 8) | body[1], ok = main ? [0x1cb8, 0x1cbd] : [0x1d25, 0x1cba], other = main ? [0x1d25, 0x1cba] : [0x1cb8, 0x1cbd];
    if (ok.includes(pre)) return { ok: true };
    return { why: other.includes(pre) ? "That is a " + (main ? "test-network" : "mainnet") + " address, and this build is for " + network + ": use " + want : "Not a Zcash transparent address." };
  }
  V.payout = { xmr: xmrAddr, zec: zecAddr, keccak256 };
})();
