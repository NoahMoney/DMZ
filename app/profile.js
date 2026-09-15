// DMZ profile.js — coin and NFT logos, the pass image and project links, stored on chain in a "profile doc" and read
// only through the user's own node. A profile doc is a DVM contract with no entrypoint and no storage: its code is a
// fixed wrapper, byte for byte, around two base64 payloads carried in comment lines (the DVM parser skips comments;
// the chain keeps the code exactly as installed). A launch or NFA names one in its icon header as dmz:<scid>.
// The app never fetches an image or a link from anywhere else, never renders SVG, and refuses the whole doc when
// any part of it fails a check. docs/PROFILE_DOCS.md is the specification; tools/profiledoc builds the same bytes.
(function () {
  const V = window.Veil;

  // ---- the format --------------------------------------------------------------------------------------------
  const HEAD = "Function InitializePrivate() Uint64\n10 RETURN 0\nEnd Function\n// DMZ profile doc v1. Data only: no entrypoint, no storage, no update path.\n";
  // storage gas for an install is the code's length + 25 (measured on the simulator); DERO refuses a transaction
  // above 20,000, so no doc is longer than 19,500 and every one installs with room to spare
  const MAX_CODE = 19500, LINKS_MAX = 1024;
  const KINDS = { logo: { max: 8192, dim: 256, links: true }, pass: { max: 14000, dim: 384, links: false } };
  const LINK_KEYS = ["website", "x", "telegram", "discord", "github"];
  const HOSTS = { x: ["x.com", "www.x.com", "twitter.com", "www.twitter.com"], telegram: ["t.me"], discord: ["discord.gg", "discord.com", "www.discord.com"], github: ["github.com", "www.github.com"] };
  const LABEL = { website: "Website", x: "X", telegram: "Telegram", discord: "Discord", github: "GitHub" };
  const BODY = /^\/\/ (logo|pass) ([A-Za-z0-9+/=]*)\n\/\/ links ([A-Za-z0-9+/=]*)\n$/;
  const B64 = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;
  const REF = /^dmz:([0-9a-f]{64})$/;
  const compose = (kind, img, lk) => HEAD + "// " + kind + " " + img + "\n// links " + lk + "\n";

  const tag = (b, o) => String.fromCharCode(b[o], b[o + 1], b[o + 2], b[o + 3]);
  const be32 = (b, o) => b[o] * 16777216 + ((b[o + 1] << 16) | (b[o + 2] << 8) | b[o + 3]);
  const le32 = (b, o) => (b[o] | (b[o + 1] << 8) | (b[o + 2] << 16)) + b[o + 3] * 16777216;
  const le24 = (b, o) => b[o] | (b[o + 1] << 8) | (b[o + 2] << 16);
  // canonical base64 only: one string per byte sequence, so two docs cannot differ in bytes and read the same
  function unb64(s) {
    if (!B64.test(s)) return null;
    let bin; try { bin = atob(s); } catch (e) { return null; }
    const b = new Uint8Array(bin.length); for (let i = 0; i < b.length; i++) b[i] = bin.charCodeAt(i);
    return b64(b) === s ? b : null;
  }
  function b64(b) { let s = ""; for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode.apply(null, b.subarray(i, i + 0x8000)); return btoa(s); }

  // PNG: the signature, IHDR first, chunks that tile the file exactly, IEND last with nothing after it (a file
  // that is also something else is refused), no second header and no animation (acTL / fcTL / fdAT).
  function png(b) {
    const SIG = [137, 80, 78, 71, 13, 10, 26, 10];
    if (b.length < 57 || SIG.some((x, i) => b[i] !== x)) return null;
    let o = 8, w = 0, h = 0, idat = false;
    while (o + 12 <= b.length) {
      const len = be32(b, o), t = tag(b, o + 4), end = o + 12 + len;
      if (end > b.length || !/^[A-Za-z]{4}$/.test(t)) return null;
      if (o === 8) { if (t !== "IHDR" || len !== 13) return null; w = be32(b, 16); h = be32(b, 20); }
      else if (t === "IHDR" || t === "acTL" || t === "fcTL" || t === "fdAT") return null;
      if (t === "IDAT") idat = true;
      o = end;
      if (t === "IEND") return o === b.length && idat && w > 0 && h > 0 ? { type: "png", w, h, n: b.length } : null;
    }
    return null;
  }
  // WebP: RIFF whose size is the file's, one VP8 / VP8L bitstream (after a VP8X header when there is one), chunks
  // that tile the file exactly, and no animation (the VP8X flag, ANIM, ANMF).
  function webp(b) {
    if (b.length < 30 || tag(b, 0) !== "RIFF" || tag(b, 8) !== "WEBP" || le32(b, 4) !== b.length - 8) return null;
    let o = 12, w = 0, h = 0, bits = 0;
    while (o < b.length) {
      if (o + 8 > b.length) return null;
      const t = tag(b, o), len = le32(b, o + 4), d = o + 8, end = d + len + (len & 1);
      if (!/^[A-Z0-9 ]{4}$/.test(t) || end > b.length || t === "ANIM" || t === "ANMF") return null;
      if (o === 12) {
        if (t === "VP8 ") { if (len < 10 || b[d + 3] !== 0x9d || b[d + 4] !== 0x01 || b[d + 5] !== 0x2a) return null; w = (b[d + 6] | (b[d + 7] << 8)) & 0x3fff; h = (b[d + 8] | (b[d + 9] << 8)) & 0x3fff; bits = 1; }
        else if (t === "VP8L") { if (len < 5 || b[d] !== 0x2f) return null; const v = le32(b, d + 1); w = (v & 0x3fff) + 1; h = Math.floor(v / 16384) % 16384 + 1; bits = 1; }
        else if (t === "VP8X") { if (len !== 10 || b[d] & 0x02) return null; w = le24(b, d + 4) + 1; h = le24(b, d + 7) + 1; }
        else return null;
      } else if (t === "VP8X") return null;
      else if (t === "VP8 " || t === "VP8L") { if (++bits > 1) return null; }
      o = end;
    }
    return bits === 1 && w > 0 && h > 0 ? { type: "webp", w, h, n: b.length } : null;
  }
  const imageInfo = (b) => png(b) || webp(b);

  // A link is https on a plain ASCII host: no user name, no port, no IDN (xn--), no IP address, no character that
  // could leave an attribute, no stray %. X, Telegram, Discord and GitHub must be on their own hosts, exactly.
  const URLRE = /^https:\/\/([A-Za-z0-9.-]{1,253})([/?#][\x21-\x7e]*)?$/;
  const LABELRE = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/;
  function link(kind, s) {
    if (typeof s !== "string" || s.length > 200 || !LABEL[kind]) return "";
    const m = URLRE.exec(s); if (!m || /["'<>\\`]|%(?![0-9A-Fa-f]{2})/.test(s)) return "";
    const host = m[1].toLowerCase(), labels = host.split(".");
    if (labels.length < 2 || labels.some((l) => !LABELRE.test(l) || l.startsWith("xn--")) || /^[0-9]+$/.test(labels[labels.length - 1])) return "";
    if (kind !== "website" && !HOSTS[kind].includes(host)) return "";
    let u; try { u = new URL(s); } catch (e) { return ""; }
    return u.protocol === "https:" && u.hostname === host && !u.port && !u.username && !u.password ? u.href : "";
  }
  function parseLinks(b) {
    // ignoreBOM keeps a byte-order mark in the text, so JSON.parse refuses it as tools/profiledoc does
    let o; try { o = JSON.parse(new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(b)); } catch (e) { return null; }
    if (!o || typeof o !== "object" || Array.isArray(o)) return null;
    const out = {};
    for (const k of Object.keys(o)) { if (!LINK_KEYS.includes(k)) return null; const u = link(k, o[k]); if (!u) return null; out[k] = u; }
    return out;
  }
  // parse(code) → { kind, type, w, h, bytes, uri, links } or null. Every check fails the whole doc.
  function parse(code) {
    if (typeof code !== "string" || code.length > MAX_CODE || !code.startsWith(HEAD)) return null;
    const m = BODY.exec(code.slice(HEAD.length)); if (!m) return null;
    const kind = m[1], K = KINDS[kind];
    let img = null, links = null;
    if (m[2]) { const b = unb64(m[2]); if (!b || b.length > K.max) return null; img = imageInfo(b); if (!img || img.w > K.dim || img.h > K.dim) return null; }
    else if (kind === "pass") return null;
    if (m[3]) { if (!K.links) return null; const b = unb64(m[3]); if (!b || b.length > LINKS_MAX) return null; links = parseLinks(b); if (!links) return null; if (!Object.keys(links).length) links = null; }
    if (!img && !links) return null;
    return { kind, type: img ? img.type : "", w: img ? img.w : 0, h: img ? img.h : 0, bytes: img ? img.n : 0, uri: img ? "data:image/" + img.type + ";base64," + m[2] : "", links };
  }
  const refScid = (a) => { const m = REF.exec(typeof a === "string" ? a : a && typeof a === "object" && typeof a.icon === "string" ? a.icon : ""); return m ? m[1] : null; };
  const isRef = (s) => typeof s === "string" && REF.test(s);
  const ref = (scid) => { if (typeof scid !== "string" || !/^[0-9a-f]{64}$/.test(scid)) throw new Error("not a contract id"); return "dmz:" + scid; };

  // ---- the cache: immutable by scid. A doc's code cannot change (the wrapper has no update path), so a read is
  // kept for good: in memory, and its code in localStorage within a budget, least recently used out first. The
  // stored code is parsed again on every load, never trusted as stored.
  const mem = new Map(), pending = new Map(), failedAt = new Map(), queue = [];
  const MEM_MAX = 400, MAXC = 3, PFX = "dmz.pd.", IDX = "dmz.pd.lru", BUDGET = 600000;
  let active = 0;
  const remember = (scid, d) => { mem.delete(scid); mem.set(scid, d); if (mem.size > MEM_MAX) mem.delete(mem.keys().next().value); };
  const store = {
    idx: null, t: 0,
    load() {
      if (this.idx) return this.idx;
      let a = []; try { a = JSON.parse(localStorage.getItem(IDX) || "[]"); } catch (e) {}
      this.idx = (Array.isArray(a) ? a : []).filter((x) => Array.isArray(x) && /^[0-9a-f]{64}$/.test(x[0]) && Number.isSafeInteger(x[1]) && x[1] > 0);
      try { const keep = new Set(this.idx.map((x) => PFX + x[0])); for (const k of Object.keys(localStorage)) if (/^dmz\.pd\.[0-9a-f]{64}$/.test(k) && !keep.has(k)) localStorage.removeItem(k); } catch (e) {}
      return this.idx;
    },
    get(scid) { try { if (!this.load().some((x) => x[0] === scid)) return null; const v = localStorage.getItem(PFX + scid); return typeof v === "string" ? v : null; } catch (e) { return null; } },
    put(scid, code) {
      const a = this.load().filter((x) => x[0] !== scid); a.push([scid, code.length]);
      let tot = a.reduce((s, x) => s + x[1], 0);
      while (tot > BUDGET && a.length > 1) { const [old, n] = a.shift(); tot -= n; try { localStorage.removeItem(PFX + old); } catch (e) {} }
      try { localStorage.setItem(PFX + scid, code); } catch (e) { a.pop(); }
      this.idx = a; this.save(true);
    },
    del(scid) { this.idx = this.load().filter((x) => x[0] !== scid); try { localStorage.removeItem(PFX + scid); } catch (e) {} this.save(true); },
    touch(scid) { const a = this.load(), i = a.findIndex((x) => x[0] === scid); if (i < 0 || i === a.length - 1) return; a.push(a.splice(i, 1)[0]); this.save(false); },
    save(now) { clearTimeout(this.t); const w = () => { try { localStorage.setItem(IDX, JSON.stringify(this.idx)); } catch (e) {} }; if (now) w(); else this.t = setTimeout(w, 1500); },
  };
  // doc(scid) → Promise<doc|null>. At most MAXC reads in flight; a node failure is retried after 30 s, an invalid
  // doc is not read again this session.
  function doc(scid) {
    if (typeof scid !== "string" || !/^[0-9a-f]{64}$/.test(scid)) return Promise.resolve(null);
    if (mem.has(scid)) { const d = mem.get(scid); if (d) store.touch(scid); return Promise.resolve(d); }
    if (pending.has(scid)) return pending.get(scid);
    const st = store.get(scid);
    if (st !== null) { const d = parse(st); if (d) { remember(scid, d); store.touch(scid); return Promise.resolve(d); } store.del(scid); }
    if (Date.now() - (failedAt.get(scid) || 0) < 30000) return Promise.resolve(null);
    const p = new Promise((res) => { queue.push({ scid, res }); pump(); });
    pending.set(scid, p); p.then(() => pending.delete(scid));
    return p;
  }
  function pump() {
    while (active < MAXC && queue.length) {
      const { scid, res } = queue.shift(); active++;
      V.node.getSC(scid, { code: true }).then((r) => {
        if (!r || !r.code) { failedAt.set(scid, Date.now()); return res(null); } // not on this node (yet): not a verdict
        const d = parse(r.code); remember(scid, d); if (d) store.put(scid, r.code); res(d);
      }, () => { failedAt.set(scid, Date.now()); res(null); }).then(() => { active--; pump(); });
    }
  }
  const logo = async (asset) => { const s = refScid(asset), d = s ? await doc(s) : null; return d && d.uri ? d.uri : null; };
  const links = async (asset) => { const s = refScid(asset), d = s ? await doc(s) : null; return d && d.links ? Object.assign({}, d.links) : null; };

  // ---- rendering: an avatar that names a doc keeps its text plate until the doc is read, and keeps it for good
  // when the doc is invalid or the image does not decode. Reads start only when the plate nears the viewport.
  let io = null, sweepT = 0;
  const watching = new Map();
  function observer() {
    if (io || typeof IntersectionObserver !== "function") return io;
    io = new IntersectionObserver((es) => { for (const e of es) if (e.isIntersecting) { io.unobserve(e.target); const go = watching.get(e.target); watching.delete(e.target); if (go) go(); } }, { rootMargin: "240px" });
    return io;
  }
  // lists redraw every block: a plate that left the page is never going to intersect, so it is let go
  const sweep = () => { sweepT = 0; for (const el of watching.keys()) if (!el.isConnected) { io.unobserve(el); watching.delete(el); } };
  function paint(el, d, asset) {
    if (!el || !d || !d.uri || el.querySelector("img")) return;
    const kids = Array.from(el.childNodes), name = V.ui.clean(asset && typeof asset === "object" ? asset.ticker || asset.name || "" : "", 16);
    const img = V.ui.h("img", { src: d.uri, width: d.w, height: d.h, alt: (name ? name + " " : "") + (d.kind === "pass" ? "pass image" : "logo"), decoding: "async" });
    img.onerror = () => { img.remove(); el.classList.remove("pd-img", "pd-pass"); el.append(...kids); };
    el.replaceChildren(img); el.classList.add("pd-img"); if (d.kind === "pass") el.classList.add("pd-pass");
  }
  function mount(el, asset) {
    const scid = refScid(asset); if (!scid) return el;
    if (mem.has(scid)) { paint(el, mem.get(scid), asset); return el; }
    const go = () => doc(scid).then((d) => paint(el, d, asset));
    const o = observer();
    if (!o) { go(); return el; }
    watching.set(el, go); o.observe(el);
    if (!sweepT) sweepT = setTimeout(sweep, 5000);
    return el;
  }
  // links as external anchors: https only, the domain shown, no referrer, no opener. The app never requests them.
  function anchors(L) {
    const { h } = V.ui, kids = [];
    for (const k of LINK_KEYS) {
      const href = L && typeof L[k] === "string" ? link(k, L[k]) : ""; if (!href) continue;
      kids.push(h("a", { class: "pd-link", href, target: "_blank", rel: "noopener noreferrer nofollow", referrerpolicy: "no-referrer", title: href },
        h("span", { class: "pd-k" }, LABEL[k]), h("span", { class: "pd-host" }, new URL(href).hostname.replace(/^www\./, ""))));
    }
    return kids.length ? h("div", { class: "pd-links" }, kids) : null;
  }
  function linksEl(asset) { const el = V.ui.h("div", { class: "pd-linkrow" }), s = refScid(asset); if (s) doc(s).then((d) => { const a = d && anchors(d.links); if (a) el.append(a); }); return el; }

  // ---- build: the creator's image is decoded, cropped to a square, drawn to a canvas and encoded in this
  // browser (WebP, PNG where WebP cannot be encoded), the quality searched down until it fits the cap. Nothing is
  // uploaded anywhere; the result is only the contract code the wallet will install.
  const SIZES = { logo: [160, 144, 128], pass: [384, 352, 320, 288, 256] };
  const toBlob = (c, t, q) => new Promise((r) => c.toBlob(r, t, q));
  const inputOK = (b) => (b[0] === 0x89 && tag(b, 1) === "PNG\r") || (b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff) || (tag(b, 0) === "RIFF" && tag(b, 8) === "WEBP") || tag(b, 0) === "GIF8";
  async function fit(c, max, qmin) {
    const top = await toBlob(c, "image/webp", 0.95); if (!top) return null;
    if (top.type !== "image/webp") { const p = await toBlob(c, "image/png"); return p && p.size <= max ? { blob: p, q: null } : null; }
    if (top.size <= max) return { blob: top, q: 0.95 };
    let lo = qmin, hi = 0.95, best = null;
    const b0 = await toBlob(c, "image/webp", lo); if (!b0 || b0.size > max) return null; best = { blob: b0, q: lo };
    for (let i = 0; i < 6; i++) { const q = (lo + hi) / 2, b = await toBlob(c, "image/webp", q); if (b && b.size <= max) { best = { blob: b, q }; lo = q; } else hi = q; }
    return best;
  }
  async function encode(file, kind, o) {
    const K = KINDS[kind];
    if (typeof Blob === "undefined" || !(file instanceof Blob)) throw new Error("Not a file");
    if (file.size > 20 * 1024 * 1024) throw new Error("The image file is larger than 20 MB");
    if (!inputOK(new Uint8Array(await file.slice(0, 16).arrayBuffer()))) throw new Error("PNG, JPEG, WebP or GIF only. SVG is never accepted.");
    let bmp; try { bmp = await createImageBitmap(file); } catch (e) { throw new Error("This browser could not decode that image"); }
    const side = Math.min(bmp.width, bmp.height), sx = (bmp.width - side) / 2, sy = (bmp.height - side) / 2;
    const sizes = Array.isArray(o.sizes) ? o.sizes.filter((n) => Number.isInteger(n) && n >= 32 && n <= K.dim) : SIZES[kind];
    const qmin = typeof o.qmin === "number" && o.qmin >= 0.05 && o.qmin <= 0.95 ? o.qmin : 0.5;
    for (const px of sizes) {
      const c = document.createElement("canvas"); c.width = c.height = px;
      const g = c.getContext("2d"); g.imageSmoothingEnabled = true; g.imageSmoothingQuality = "high"; g.drawImage(bmp, sx, sy, side, side, 0, 0, px, px);
      const r = await fit(c, K.max, qmin); if (!r) continue;
      const bytes = new Uint8Array(await r.blob.arrayBuffer()); if (!imageInfo(bytes)) continue;
      return { b64: b64(bytes), q: r.q, px };
    }
    throw new Error("The image does not fit in " + K.max.toLocaleString("en-US") + " bytes at " + (sizes[sizes.length - 1] || 0) + " px and quality " + qmin + ". A simpler image will.");
  }
  async function gas(code) {
    try {
      const W = V.wallet, est = await V.node.call("DERO.GetGasEstimate", Object.assign({ transfers: [], sc_rpc: [{ name: "SC_ACTION", datatype: "U", value: 1 }, { name: "SC_CODE", datatype: "S", value: code }], ringsize: 2, sc: code }, W && W.connected ? { signer: W.address } : {}));
      if (est && /^\d+$/.test(String(est.gasstorage))) return { gas: Number(est.gasstorage), src: "node" };
    } catch (e) {}
    return { gas: code.length + 25, src: "size" };
  }
  const why = (k) => (k === "website" ? "an https address on a plain ASCII domain, with no user name, port or xn-- label" : "an https link on " + HOSTS[k].filter((x) => !x.startsWith("www.")).join(" or "));
  // build({ image: File|null, links: {website, x, telegram, discord, github}, kind: "logo"|"pass" })
  //   → { code, bytes, gasEstimate, gasSource, kind, preview, width, height, type, imageBytes, quality, links, sha256 }
  async function build(opts) {
    const o = opts || {}, kind = o.kind === "pass" ? "pass" : "logo", K = KINDS[kind], raw = o.links || {}, L = {};
    for (const k of LINK_KEYS) {
      const v = String(raw[k] || "").trim(); if (!v) continue;
      if (!K.links) throw new Error("A pass image carries no links");
      if (!link(k, v)) throw new Error(LABEL[k] + " must be " + why(k) + ".");
      L[k] = v;
    }
    const lj = Object.keys(L).length ? new TextEncoder().encode(JSON.stringify(L)) : null;
    if (lj && lj.length > LINKS_MAX) throw new Error("The links take " + lj.length + " bytes; the doc holds " + LINKS_MAX);
    if (!o.image && kind === "pass") throw new Error("A pass image needs an image");
    const enc = o.image ? await encode(o.image, kind, o) : null;
    if (!enc && !lj) throw new Error("Nothing to store: choose an image or add a link");
    const code = compose(kind, enc ? enc.b64 : "", lj ? b64(lj) : ""), d = parse(code);
    if (!d) throw new Error("The doc did not pass this app's own reader, so it is not offered for install");
    const g = await gas(code);
    return { kind, code, bytes: code.length, gasEstimate: g.gas, gasSource: g.src, preview: d.uri, width: d.w, height: d.h, type: d.type, imageBytes: d.bytes, quality: enc ? enc.q : null, links: d.links, sha256: await V.sha256Hex(code) };
  }
  // install(build) → scid: a wallet install over XSWD, the way every contract in this app is installed (app.tx:
  // the network gate, the fee estimate and its 1 DERO ceiling). The code is checked again here, then read back.
  async function install(b) {
    const code = b && b.code, d = parse(code);
    if (!d) throw new Error("Refusing: that is not a valid profile doc");
    if (!V.app || typeof V.app.tx !== "function") throw new Error("No wallet path in this build");
    const scid = await V.app.tx({ code, args: [], describe: d.kind === "pass" ? "Pass image install" : "Logo doc install" });
    if (!scid) throw new Error("The doc was not installed");
    mem.delete(scid); failedAt.delete(scid);
    const back = await doc(scid); if (!back) throw new Error("Installed as " + scid + ", but the node does not return it as a valid doc yet");
    return scid;
  }

  // ---- the creator's screen: #profiledoc (a logo and links) and #profiledoc/pass (the Founders Pass image) ----
  const PH = { website: "https://your.site", x: "https://x.com/handle", telegram: "https://t.me/group", discord: "https://discord.gg/invite", github: "https://github.com/org" };
  const num = (n) => Number(n).toLocaleString("en-US");
  function preview(b) {
    const { h, kv } = V.ui, pass = b.kind === "pass";
    const img = b.preview ? h("img", { src: b.preview, width: b.width, height: b.height, alt: pass ? "pass image preview" : "logo preview", decoding: "async" }) : null;
    return h("div", null,
      h("div", { class: "label" }, "Preview, as this app shows it"),
      img ? h("div", { class: "row mt8 pd-prev" + (pass ? " pass" : "") }, h("div", { class: "avatar big pd-img" + (pass ? " pd-pass" : "") }, img), h("div", { class: "avatar pd-img" }, img.cloneNode()), h("div", { class: "avatar sm pd-img" }, img.cloneNode()))
        : h("div", { class: "note mt8" }, "No image: markets that name this doc keep their text plate."),
      b.links ? h("div", { class: "mt8" }, anchors(b.links)) : null,
      h("div", { class: "mt" },
        img ? kv("Image", b.type.toUpperCase() + " · " + b.width + "×" + b.height + " · " + num(b.imageBytes) + " bytes" + (b.quality ? " · quality " + b.quality.toFixed(2) : "")) : null,
        kv("Contract code", num(b.bytes) + " bytes"),
        kv("Storage gas", num(b.gasEstimate) + " of 20,000" + (b.gasSource === "size" ? " (from its size)" : "")),
        kv("Network fee", V.amm.fmtUnits(BigInt(b.gasEstimate + 200), 5n, 5) + " DERO"),
        kv("sha256", h("span", { class: "pd-hash" }, b.sha256))));
  }
  function installed(scid, kind) {
    const { h, button, toast } = V.ui, r = ref(scid);
    return h("div", { class: "hero mt" }, h("div", { class: "eyebrow" }, "Installed"), h("div", { class: "pd-ref mt8" }, r),
      button("Copy reference", "small mt8", () => navigator.clipboard && navigator.clipboard.writeText(r).then(() => toast("Copied", "ok"), () => {})),
      h("div", { class: "note mt8" }, kind === "pass" ? "Give it to tools/simdeploy as -pass-image " + r + " when the pass contract is installed: its icon and file headers take it for good."
        : "Put it in the Image field when you launch a token, or in an NFA's icon. A launch's header is fixed at install, so the market names this doc for good."));
  }
  function editor(kind) {
    const { h, render, button, toast } = V.ui, K = KINDS[kind];
    const file = h("input", { type: "file", accept: "image/png,image/jpeg,image/webp,image/gif", "aria-label": kind === "pass" ? "pass image file" : "logo file" });
    const inputs = {}; if (K.links) for (const k of LINK_KEYS) inputs[k] = h("input", { autocomplete: "off", spellcheck: "false", maxlength: 200, placeholder: PH[k], "aria-label": LABEL[k] });
    const out = h("div", { class: "mt" }), err = h("div"), done = h("div");
    let cur = null, seq = 0, timer = 0;
    const send = button(kind === "pass" ? "Install pass image" : "Install logo doc", "primary wide mt", async () => {
      if (!cur) return; send.disabled = true;
      try { render(done, installed(await install(cur), kind)); } catch (e) { toast(String(e.message || e), "err", 8000); } finally { send.disabled = !cur; }
    }, { disabled: true });
    const run = async () => {
      const n = ++seq, f = file.files && file.files[0], L = {}; for (const k in inputs) L[k] = inputs[k].value;
      cur = null; V.profile.lastBuild = null; send.disabled = true; render(err); render(done);
      if (!f && !Object.values(L).some((v) => v.trim())) return render(out, h("div", { class: "note" }, "Choose an image" + (K.links ? " or add a link" : "") + " to see the doc and its cost."));
      render(out, h("div", { class: "note" }, "Encoding in this browser…"));
      try { const b = await build({ kind, image: f || null, links: L }); if (n !== seq) return; cur = b; V.profile.lastBuild = b; render(out, preview(b)); send.disabled = false; }
      catch (e) { if (n === seq) { render(out); render(err, h("div", { class: "errbox mt" }, String(e.message || e))); } }
    };
    file.onchange = run; for (const k in inputs) inputs[k].oninput = () => { clearTimeout(timer); timer = setTimeout(run, 500); };
    run();
    return h("div", { class: "card mt pd-ed" },
      h("label", { class: "field" }, kind === "pass" ? "Pass image (square art; up to 384 px and " + num(K.max) + " bytes once encoded)" : "Logo (cropped square, 128 to 160 px, at most " + num(K.max) + " bytes once encoded)", file),
      K.links ? LINK_KEYS.map((k) => h("label", { class: "field mt8" }, LABEL[k], inputs[k])) : null,
      err, out, send, done);
  }
  const trust = (kind) => V.ui.h("div", { class: "note mt" },
    "You choose what is stored here, and nobody checks it. Anyone who opens a market that names this doc sees the " + (kind === "pass" ? "image" : "logo and links") + " you chose, not a claim that they are true. " +
    "Once installed the doc can never change: its contract has no function. The app reads it only from each user's own node, shows only WebP or PNG within these caps and never SVG, and requests nothing from any other host. A link opens only when someone clicks it.");
  V.ui.router.on("profiledoc", (el, param) => {
    const kind = param === "pass" ? "pass" : "logo";
    V.ui.render(el, V.ui.sectionHead(kind === "pass" ? "gem" : "sparkle", kind === "pass" ? "Pass image" : "Logo and links", "Held in a contract of your own and read by everyone through their own node"), editor(kind), trust(kind));
  });

  V.profile = { build, install, ref, logo, links, isRef, mount, anchors, linksEl, doc, parse, link, imageInfo, compose, HEAD, KINDS, MAX_CODE, LINKS_MAX, LINK_KEYS, HOSTS, lastBuild: null };
})();
