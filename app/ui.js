// DMZ ui.js — tiny DOM toolkit, icons, identity avatars, sheet, toasts, sparkline, router. Every on-chain string
// is rendered as a text node; the only innerHTML in the app is the chart tooltip, which contains numbers only.
(function () {
  const V = window.Veil, A = V.amm;
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    if (attrs) for (const [k, v] of Object.entries(attrs)) {
      if (v === null || v === undefined || v === false) continue;
      if (k === "class") el.className = v; else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
      else if (k === "style") el.style.cssText = v; else el.setAttribute(k, v === true ? "" : v);
    }
    for (const kid of kids.flat(Infinity)) if (kid !== null && kid !== undefined && kid !== false) el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    return el;
  }
  const clear = (el) => { while (el.firstChild) el.removeChild(el.firstChild); return el; };
  const render = (el, ...nodes) => { clear(el).append(...nodes.flat(Infinity).filter(Boolean)); return el; };
  // sanitiser for anything that came from a contract: no control / bidi-override / zero-width characters, bounded length
  // Every invisible formatting character is stripped, not only the ones that are easy to type: a bidi
  // override can reverse a displayed address and a word joiner can hide a second word inside a name. The
  // class is written in escape form on purpose, because a raw U+2028 inside a regex literal will not parse.
  // A truncation says so: without the ellipsis a creator picks a name whose first `max` characters read as
  // a complete and different sentence, and the screen shows that sentence as if it were the whole of it.
  const clean = (s, max = 64) => { const t = String(s || "").replace(/[\u0000-\u001f\u007f-\u009f\u061c\u180e\u200b-\u200f\u202a-\u202e\u2028\u2029\u2060-\u2064\u2066-\u2069\ufeff\ufff9-\ufffb]/g, ""); return t.length > max ? t.slice(0, max - 1) + "…" : t; };
  // impersonation guard: NFKC, upper-case, common look-alikes folded (Cyrillic and Greek capitals, 0 to O, 1 to I),
  // then [A-Z0-9] only. Two tickers with one skeleton read the same to a person. The two strings are paired by
  // index, so anything appended to one must be appended to the other.
  const CONF_FROM = "\u0410\u0412\u0415\u041a\u041c\u041d\u041e\u0420\u0421\u0422\u0425\u0423\u0391\u0392\u0395\u0396\u0397\u0399\u039a\u039c\u039d\u039f\u03a1\u03a4\u03a5\u03a7" + "01" + "\u0405\u0406\u0408\u051a\u051c\u04ae";
  const CONF_TO = "ABEKMHOPCTXY" + "ABEZHIKMNOPTYX" + "OI" + "SIJQWY";
  const tickerKey = (t) => { let o = ""; for (const ch of String(t || "").normalize("NFKC").toUpperCase()) { const i = CONF_FROM.indexOf(ch); o += i >= 0 ? CONF_TO[i] : ch; } return o.replace(/[^A-Z0-9]/g, ""); };
  // A look-alike ticker is one that really contains a non-ASCII character standing in for an ASCII one. Deriving
  // this from "the fold changed something" warned about every honest ticker containing a 0 or a 1, because
  // tickerKey folds those to O and I deliberately so that S0L and SOL group together.
  const hasHomoglyph = (t) => { for (const ch of String(t || "").normalize("NFKC").toUpperCase()) { if (ch > "\u007f" && CONF_FROM.indexOf(ch) >= 0) return true; } return false; };
  const safeUrl = (u) => (typeof u === "string" && /^https:\/\/[^\s"'<>\\]{3,300}$/i.test(u) ? u : "");

  // ---- icons (inline SVG paths, 24-unit grid) ----
  const P = { star: "M12 2.5l2.9 6 6.6.9-4.8 4.6 1.2 6.5L12 17.4 6.1 20.5l1.2-6.5L2.5 9.4l6.6-.9z", check: "M9 16.2 4.8 12l-1.4 1.4L9 19 21 7l-1.4-1.4z", copy: "M16 1H4a2 2 0 0 0-2 2v12h2V3h12V1zm3 4H8a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h11a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2zm0 16H8V7h11v14z", shield: "M12 2 4 5v6c0 5 3.4 9.7 8 11 4.6-1.3 8-6 8-11V5l-8-3zm-1.5 14.5-3.5-3.5 1.4-1.4 2.1 2.1 5.1-5.1 1.4 1.4-6.5 6.5z", chevron: "M7.4 8.6 12 13.2l4.6-4.6L18 10l-6 6-6-6z", bolt: "M11 21h-1l1-7H7.5c-.6 0-.6-.3-.4-.7L13 3h1l-1 7h3.5c.5 0 .6.3.4.7L11 21z", warn: "M1 21h22L12 2 1 21zm12-3h-2v-2h2v2zm0-4h-2v-4h2v4z", lock: "M12 2a5 5 0 0 1 5 5v2h1a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2h1V7a5 5 0 0 1 5-5zm0 2a3 3 0 0 0-3 3v2h6V7a3 3 0 0 0-3-3z", eye: "M12 5c5 0 9 3.5 10.5 7-1.5 3.5-5.5 7-10.5 7S3 15.5 1.5 12C3 8.5 7 5 12 5zm0 3.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7z", share: "M18 16a3 3 0 0 0-2.1.8L8.9 12.7a3 3 0 0 0 0-1.4l7-4.1A3 3 0 1 0 15 5c0 .2 0 .5.1.7L8.1 9.8a3 3 0 1 0 0 4.4l7 4.1A3 3 0 1 0 18 16z", x: "M18.3 5.7 12 12l6.3 6.3-1.4 1.4L12 13.4l-6.3 6.3-1.4-1.4L10.6 12 4.3 5.7l1.4-1.4L12 10.6l6.3-6.3z", arrow: "M5 17.6 15.6 7H8V5h11v11h-2V8.4L6.4 19z", wallet: "M20 7H4a1 1 0 0 1 0-2h15V3H4a3 3 0 0 0-3 3v12a3 3 0 0 0 3 3h16a1 1 0 0 0 1-1V8a1 1 0 0 0-1-1zm-3 8a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3z", flame: "M12 2c1 4 4 5.5 4 10a4 4 0 0 1-8 0c0-1.5.5-2.5 1.2-3.4.2 1.4 1 2.4 1.8 2.4 1.2 0 1.5-1.4 1-3C11.4 6.5 12 4 12 2zm0 20c-4 0-7-3-7-7 0-2.4 1.1-4.5 2.6-6.2C7.2 10.5 7 12 7 13a5 5 0 0 0 10 0c0-2.2-.9-4-2-5.6 2.6 1.8 4 4.6 4 7.6 0 4-3 7-7 7z", trend: "M3 17l6-6 4 4 8-8v5h2V3h-9v2h5l-7 7-4-4-7 7z", rocket: "M13 2c4 0 8 4 8 9l-4 4-1-4-4-1zm-1 5 4 4-6 6-4-4zM5 14l4 4-2 2H3v-4zm2-4 3 3-2 2-3-3z", cap: "M12 2 1 7l11 5 9-4.1V15h2V7zm-7 9.6V16c0 2 3.1 4 7 4s7-2 7-4v-4.4l-7 3.2z", feed: "M4 4h16v3H4zm0 6.5h16v3H4zM4 17h10v3H4z", sparkle: "M12 2l2.2 6.3L20 10l-5.8 1.7L12 18l-2.2-6.3L4 10l5.8-1.7z", gem: "M12 2 2 9l10 13L22 9zM5.6 9 12 4.6 18.4 9 12 18.2z" };
  function icon(name, cls = "svgi") {
    const s = document.createElementNS("http://www.w3.org/2000/svg", "svg"); s.setAttribute("viewBox", "0 0 24 24"); s.setAttribute("class", cls); s.setAttribute("aria-hidden", "true");
    const p = document.createElementNS("http://www.w3.org/2000/svg", "path"); p.setAttribute("d", P[name] || ""); p.setAttribute("fill", "currentColor"); s.append(p); return s;
  }

  // ---- toasts / sheet ----
  function toast(msg, kind = "", ms = 4200) {
    const t = h("div", { class: "toast " + kind, role: "status" }, kind === "ok" ? icon("check") : kind === "err" ? icon("warn") : null, h("span", null, msg));
    $("#toasts").append(t); setTimeout(() => t.remove(), ms); return t;
  }
  function openSheet(...nodes) {
    const s = $("#sheet"), b = $("#sheetBackdrop"); render(s, ...nodes); s.hidden = false; b.hidden = false;
    b.onclick = closeSheet; document.body.style.overflow = "hidden";
    const f = s.querySelector("input,button"); if (f) setTimeout(() => f.focus(), 50);
  }
  function closeSheet() { const s = $("#sheet"); s.hidden = true; $("#sheetBackdrop").hidden = true; document.body.style.overflow = ""; clear(s); }
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !$("#sheet").hidden) closeSheet(); });

  // ---- widgets ----
  const skeleton = (n = 3, cls = "skel skel-card") => Array.from({ length: n }, () => h("div", { class: cls }));
  const timeAgo = (ts) => { const d = Math.max(0, Math.floor(Date.now() / 1000) - ts); if (d < 60) return d + "s"; if (d < 3600) return Math.floor(d / 60) + "m"; if (d < 86400) return Math.floor(d / 3600) + "h"; return Math.floor(d / 86400) + "d"; };
  // network grammar (docs/DESIGN_SYSTEM.md §Networks): a two-letter mono code and the .net class for a vault
  // record. The chain is read from the record that ships with the app, never guessed from a ticker anyone can type.
  const netOf = (a) => { const s = (String(a && a.sym || "") + " " + String(a && a.chain || "")).toUpperCase(); if (/XMR|MONERO/.test(s)) return ["XM", "net xmr"]; if (/ZEC|ZCASH/.test(s)) return ["ZC", "net zec"]; return [(String(a && a.sym || "").toUpperCase().replace(/[^A-Z0-9]/g, "") + "??").slice(0, 2), "net"]; };
  // identity avatar: a plate carrying the ticker and, on its leading edge, the tint of the chain the market
  // settles on — a wrapped asset's own vault or the vault its quote comes from. Images only when allowed, https
  // only, no referrer. Nothing here is a colour the system did not already name.
  function avatar(x, size = "") {
    const isAsset = typeof x === "object", id = isAsset ? x.scid || x.ticker || "" : String(x || "");
    const src = safeUrl(isAsset ? x.icon : (V.data.profile(id).avatar || ""));
    const label = clean(isAsset ? (x.ticker || "?").slice(0, 3) : V.data.displayName(id).slice(0, 2).toUpperCase(), 3);
    const v = isAsset ? V.data.vaultOf(x) : null;
    const el = h("div", { class: "avatar " + size + (v ? " " + netOf(v)[1].slice(4) : ""), "aria-hidden": "true" }, label);
    if (src && V.settings && V.settings.images) { const img = h("img", { src, alt: "", loading: "lazy", referrerpolicy: "no-referrer", decoding: "async" }); img.onerror = () => img.remove(); el.prepend(img); }
    return el;
  }
  const pill = (txt, cls = "") => h("span", { class: "pill " + cls }, txt);
  // direction is a number in a numeric column, not a boxed control: .chg carries colour and tabular figures only
  const changePill = (c) => (c === null || c === undefined ? h("span", { class: "chg" }, "—") : h("span", { class: "chg " + (c >= 0 ? "up" : "down") }, A.pct(c)));
  function toggle(label, on, cb, note, id) {
    const sw = h("button", { class: "sw " + (on ? "on" : ""), role: "switch", "aria-checked": String(on), type: "button", id: id || null, "aria-label": label });
    sw.onclick = () => { on = !on; sw.classList.toggle("on", on); sw.setAttribute("aria-checked", String(on)); cb(on); };
    return h("div", { class: "toggle" }, h("div", null, h("div", null, label), note ? h("div", { class: "note" }, note) : null), sw);
  }
  const kv = (k, v, cls) => h("div", { class: "kv" }, h("span", { class: "dim" }, k), h("b", { class: cls || "" }, v));
  function segmented(opts, cur, cb) {
    const el = h("div", { class: "seg", role: "tablist" });
    for (const o of opts) el.append(h("button", { class: o.v === cur ? "on" : "", role: "tab", type: "button", onclick: (ev) => { $$("button", el).forEach((b) => b.classList.remove("on")); ev.currentTarget.classList.add("on"); cb(o.v); } }, o.l));
    return el;
  }
  function button(label, cls, onclick, attrs) { return h("button", Object.assign({ class: "btn " + (cls || ""), type: "button", onclick }, attrs || {}), label); }
  // two tiers: the contract code is one thing, what it trades or where it is listed is another
  const verified = (s) => {
    // A market that wears a wrapped asset's name must not be handed the word "Verified" on its own. The badge
    // is true — the code does match the template — but next to a copy it reads as a blessing of the name, which
    // is the one thing it says nothing about. Say what was checked instead.
    if (s && s.impersonates) return h("span", { class: "verified warn", title: "Only the contract code was checked, and it matches the shipped template. That says nothing about the name: this is not " + s.impersonates + ", and no vault holds anything for it." }, icon("shield"), "Code checked · name is not");
    // The code and the fee were checked; the factory's treasury could not be read this session, so whether this
    // market pays the protocol treasury was NOT checked. Before the wrapped-asset branch, whose title says it was.
    if (s && s.treasuryUnverified) return h("span", { class: "verified warn", title: "The contract code matches the shipped template and the fee is right, but the factory's treasury could not be read this session, so whether this market pays the protocol treasury was not checked." }, icon("shield"), "Code checked \u00b7 treasury unread");
    // A wrapped asset is not a launch, so the launch check called wrapped Monero "asset unknown" — the app
    // telling an XMR holder that the one asset it ships a vault for is a stranger. Its id comes from the same
    // bundle as this code and its vault is named there, which is a stronger claim than any launch can make.
    if (s && s.wrappedBase && s.vaultRec) return h("span", { class: "verified", title: "This market trades " + (s.vaultRec.name || "a wrapped asset") + ", whose vault contract id ships with this app. The pool contract matches the shipped template and pays the protocol treasury." }, icon("shield"), "Wrapped asset \u00b7 verified");
    if (s && s.kind === "pool" && !s.assetVerified) return h("span", { class: "verified warn", title: "The pool contract matches the shipped template and pays the protocol treasury, but its asset is not a DMZ launch: anyone can create a DERO asset with any name. Check the asset id yourself." }, icon("shield"), "Pool verified \u00b7 asset unknown");
    if (s && s.unlisted) return h("span", { class: "verified warn", title: "The code matches a shipped template, but this contract is not in the DMZ registry: nobody registered it with the factory" }, icon("shield"), "Code verified \u00b7 not listed");
    return h("span", { class: "verified", title: "Contract code matches the shipped template and pays the protocol treasury" }, icon("shield"), "Verified");
  };
  const warnings = (s) => {
    const w = [];
    // First, and worded as a statement rather than a caution: "check the contract id" asks a user to do work
    // they cannot do. This one is knowable — the vault that owns the name is in the shipped config — so say it.
    if (s && s.impersonates) w.push(h("span", { class: "pill bad", title: "This is a token that named itself after " + s.impersonates + ". It is not the wrapped asset, it is not backed by anything held in the vault, and it has nothing to do with the " + s.impersonates + " you can deposit and withdraw on the Wrap screen." }, "Not real " + s.impersonates));
    if (s && s.splitWith && !s.splitDeepest) w.push(h("span", { class: "pill warn", title: "Another contract trades this same pair and holds more of it. This one is thinner: the same order moves its price more, and a large one can empty it. Markets shows both, deepest first." }, "Split pair \u00b7 thinner"));
    if (s && (s.quoteLegacy || s.baseLegacy)) w.push(h("span", { class: "pill warn", title: "This market trades a retired vault's wrapped units. The federation mints no more of them, so the supply is only what already exists; they leave by being burned back on the Wrap screen, where the vault that replaced this one also is." }, "Retired vault"));
    if (s && s.dupTicker) w.push(h("span", { class: "pill warn", title: "Another listed market uses a ticker that reads the same. Check the contract id before trading." }, "Ticker shared"));
    if (s && s.lookalike) w.push(h("span", { class: "pill warn", title: "This ticker contains characters that look like others (Cyrillic or Greek letters, digits for letters)." }, "Look-alike ticker"));
    if (s && s.splitWith && s.splitDeepest) w.push(h("span", { class: "pill dim", title: "Another contract trades this same pair with less liquidity. This is the deeper one." }, "Split pair"));
    return w;
  };
  // section head with an icon, and an illustrated empty state
  const sectionHead = (ic, title, sub, action) => h("div", { class: "sh" }, h("span", { class: "ico" }, icon(ic)), h("div", { class: "grow" }, h("h2", null, title), sub ? h("div", { class: "sub tiny" }, sub) : null), action || null);
  const emptyState = (ic, title, hint, action) => h("div", { class: "empty" }, h("div", { class: "ico" }, icon(ic)), title ? h("b", null, title) : null, hint ? h("div", null, hint) : null, action ? h("div", { class: "mt" }, action) : null);
  // The third state a list can be in: it did not load. A skeleton that never resolves reads as slow, not broken,
  // so the failure is named and the two recoveries the app can offer are on the row: try again, or change the
  // node, which is the usual cause.
  const errorState = (message, onRetry) => emptyState("warn", "Could not load", message, [button("Retry", "small primary", onRetry), button("Node settings", "small ghost", () => router.go("settings"))]);
  // staggered entrance for freshly rendered lists
  const rise = (el, i) => { el.classList.add("rise"); el.style.animationDelay = Math.min(i, 12) * 45 + "ms"; return el; };
  function details(label, ...kids) { return h("details", { class: "more" }, h("summary", null, h("span", null, label), icon("chevron")), ...kids); }

  // ---- sparkline ----
  const NS = "http://www.w3.org/2000/svg";
  const svg = (tag, a) => { const e = document.createElementNS(NS, tag); for (const [k, v] of Object.entries(a || {})) e.setAttribute(k, v); return e; };
  // Takes the market so the quote's decimals travel with it; the shape is relative, but a mis-scaled series
  // still disagreed with every number beside it.
  function sparkline(points, mkt, up) {
    let s = points.map((p) => ({ x: p.ts, y: A.priceOf(mkt, p) })).filter((p) => p.y > 0); const w = 120, hgt = 34; const el = svg("svg", { viewBox: `0 0 ${w} ${hgt}`, preserveAspectRatio: "none" });
    // A day of blocks is thousands of points and the box is 120px wide, so past 160 the path only costs. Every
    // n-th point is kept, plus the extremes so the scale does not move and the last so the line ends at now.
    if (s.length > 160) { const step = Math.ceil(s.length / 160); let lo = 0, hi = 0; s.forEach((p, i) => { if (p.y < s[lo].y) lo = i; if (p.y > s[hi].y) hi = i; }); s = s.filter((p, i) => i % step === 0 || i === lo || i === hi || i === s.length - 1); }
    if (s.length < 2) { el.append(svg("line", { x1: 0, y1: hgt / 2, x2: w, y2: hgt / 2, stroke: "var(--fg3)", "stroke-width": 1, "vector-effect": "non-scaling-stroke" })); return h("div", { class: "spark" }, el); }
    const xs = s.map((p) => p.x), ys = s.map((p) => p.y); const x0 = Math.min(...xs), x1 = Math.max(...xs) || x0 + 1, y0 = Math.min(...ys), y1 = Math.max(...ys);
    const X = (x) => ((x - x0) / (x1 - x0 || 1)) * (w - 2) + 1, Y = (y) => hgt - 2 - ((y - y0) / (y1 - y0 || 1)) * (hgt - 4);
    const d = s.map((p, i) => (i ? "L" : "M") + X(p.x).toFixed(1) + " " + Y(p.y).toFixed(1)).join(" ");
    // no gradient fill and no glow: the line is the information. A fill under a 74px sparkline is decoration
    // that costs a <defs>, a gradient and a second path per row, on a list that redraws every block.
    const col = up ? "var(--up)" : "var(--down)";
    el.append(svg("path", { d, fill: "none", stroke: col, "stroke-width": 1.5, "stroke-linejoin": "round", "stroke-linecap": "round", "vector-effect": "non-scaling-stroke" }));
    return h("div", { class: "spark" }, el);
  }
  // ---- router ----
  const router = {
    handlers: {}, current: null, gen: 0,
    on(name, fn) { this.handlers[name] = fn; },
    go(name, param) { location.hash = param ? `#${name}/${param}` : `#${name}`; },
    dispatch() {
      this.gen++; // every navigation starts a generation; listeners from older ones are dropped on the next tick
      const [name, ...rest] = location.hash.replace(/^#/, "").split("/"); const n = name || "home"; const param = rest.join("/");
      this.current = { name: n, param };
      $$(".tab").forEach((t) => t.classList.toggle("on", t.dataset.tab === n || (n === "token" && t.dataset.tab === "home")));
      const fn = this.handlers[n] || this.handlers.home; const screen = $("#screen"); screen.scrollTop = 0; window.scrollTo(0, 0);
      screen.classList.remove("enter"); void screen.offsetWidth; screen.classList.add("enter");
      // Half the handlers are async, so the catch alone saw only the synchronous half; a rejection after the first
      // await left skeletons on screen for good. The same recovery covers both, and only while this navigation is
      // still the current one — a late failure must not paint over the screen the user has since moved to.
      const g = this.gen, fail = (e) => { console.error(e); if (this.gen === g) render(screen, h("div", { class: "errbox" }, String(e.message || e))); };
      try { const r = fn(screen, param); if (r && typeof r.then === "function") r.then(null, fail); } catch (e) { fail(e); }
    },
  };
  window.addEventListener("hashchange", () => router.dispatch());
  // A rejection nobody caught is the failure the user would otherwise never hear about: the console is closed.
  window.addEventListener("unhandledrejection", (e) => { const r = e.reason; toast("Something failed: " + clean(String(r && r.message || r), 200), "err", 8000); });

  V.ui = { $, $$, h, clear, render, clean, safeUrl, icon, toast, openSheet, closeSheet, skeleton, timeAgo, avatar, pill, changePill, toggle, kv, segmented, button, verified, warnings, tickerKey, hasHomoglyph, sectionHead, emptyState, errorState, rise, details, sparkline, netOf, router };
})();
