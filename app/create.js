// DMZ create.js — the rules of the creation flow, with no DOM: what each creation contract accepts, checked the way
// its InitializePrivate checks it (contracts/launch.bas 22-32, pool.bas 18-23, nft.bas 22-26, nfa.bas 200-500),
// with a message that says how to fix the value it refuses; the links a profile document carries; the cost of a
// creation, itemised; and the creator's local record of their own unfinished installs (public data only: ids and the
// install arguments, which are going on chain anyway). forms.js and wizard.js draw it; tests/js/create.test.mjs runs it.
(function () {
  const V = window.Veil, A = V.amm, C = (V.create = {});
  const Z = 0n, U64 = 1n << 64n, ZEROQ = "0".repeat(64);
  // STRLEN in the DVM is Go's len(): bytes, not characters. An accented letter is two, an emoji four.
  const bytes = (s) => new TextEncoder().encode(String(s || "")).length;
  const HEX64 = /^[0-9a-f]{64}$/;
  C.ZEROQ = ZEROQ; C.bytes = bytes;
  // n bytes at most; the message names the excess so the fix is a number, not a guess
  const maxB = (label, s, n) => { const b = bytes(s); return b > n ? label + " is " + b + " bytes; the contract takes " + n + ". Shorten it by " + (b - n) + (b !== String(s).length ? " (accented letters and emoji take 2 to 4 bytes each)." : ".") : ""; };
  const reqB = (label, s, n) => (!String(s || "").trim() ? label + " is required: 1 to " + n + " characters." : maxB(label, s, n));
  // https only; the app never loads it (ui.js avatar: no image from a creator's host), other DERO tools may
  const httpsErr = (label, s, n) => { const v = String(s || "").trim(); if (!v) return ""; const m = maxB(label, v, n); if (m) return m; return /^https:\/\/[^\s"'<>\\]{3,}$/i.test(v) ? "" : label + " must start with https:// (http and other schemes are refused)."; };
  // a decimal amount with at most `dec` places, as base units; null when it is not a number at all
  const units = (s, dec) => { const v = String(s || "").trim().replace(/,/g, ""); if (!/^\d*\.?\d*$/.test(v) || v === "" || v === ".") return null; const f = v.split(".")[1] || ""; return f.length > Number(dec) ? undefined : A.parseUnits(v, BigInt(dec)); };
  const whole = (s, lo, hi) => { const v = String(s === undefined ? "" : s).trim(); if (!/^\d+$/.test(v)) return null; const n = BigInt(v); return n >= BigInt(lo) && n <= BigInt(hi) ? n : null; };
  // a percentage to basis points: 0..max, two decimals at most
  const pctBps = (s, max) => { const v = String(s === undefined || s === "" ? "0" : s).trim(); if (!/^\d+(\.\d{1,2})?$/.test(v)) return null; const [i, f = ""] = v.split("."); const b = BigInt(i) * 100n + BigInt((f + "00").slice(0, 2)); return b <= BigInt(max) * 100n ? b : null; };
  const out = (errors, args, facts) => { const e = {}; for (const [k, v] of Object.entries(errors)) if (v) e[k] = v; const ok = !Object.keys(e).length; return { ok, errors: e, first: ok ? "" : Object.values(e)[0], args: ok ? args() : null, facts: ok && facts ? facts() : {} }; };

  // ---- coin: contracts/launch.bas InitializePrivate ----
  // f: name ticker descr tags icon supply dec grad cbps tax (strings), tmode 0..3; env: quote {scid, sym, dec},
  // treasury, feeBps, launchPfeeBps, iconRef (a profile document's "dmz:<scid>", which then fills the icon header)
  C.TICK = /^[A-Z0-9]{1,10}$/;
  C.launch = (f, env) => {
    const q = env.quote || { scid: ZEROQ, sym: "DERO", dec: 5 }, qd = Number.isInteger(q.dec) ? q.dec : 5, sym = q.sym || "DERO";
    const name = String(f.name || "").trim(), t = String(f.ticker || "").trim().toUpperCase(), icon = env.iconRef || String(f.icon || "").trim();
    const bad = [...new Set(t.replace(/[A-Z0-9]/g, ""))].map((c) => (c === " " ? "space" : "“" + c + "”"));
    const dec = whole(f.dec, 0, 12), supply = dec === null ? null : units(f.supply, dec), grad = units(f.grad, qd);
    const cbps = pctBps(f.cbps, 5), tax = pctBps(f.tax, 10), tmode = Number(f.tmode) || 0;
    const lo = 1000000n, hi = 1000000000000000n;
    const e = {
      name: reqB("Name", name, 32),
      ticker: !t ? "A ticker is required: 1 to 10 letters or digits, like DMZ." : bad.length ? "The ticker takes only A to Z and 0 to 9: remove " + bad.join(", ") + "." : t.length > 10 ? "The ticker is " + t.length + " characters; 10 at most." : "",
      descr: maxB("The description", f.descr, 240), tags: maxB("Tags", f.tags, 100), icon: env.iconRef ? "" : httpsErr("The icon URL", icon, 200),
      dec: dec === null ? "Decimals: a whole number from 0 to 12." : "",
      supply: dec === null ? "" : supply === null ? "Supply: a number of whole tokens, like 1000000." : supply === undefined ? "Supply has more decimal places than the " + dec + " decimals you chose." :
        supply < lo ? "Supply × 10^decimals must be at least 1,000,000 units: raise the supply or the decimals." : supply >= 1n << 63n ? "Supply × 10^decimals must stay below 2^63 units: lower the supply or the decimals." : "",
      grad: grad === null || grad === undefined ? "Graduation target: an amount of " + sym + " with at most " + qd + " decimals." :
        grad < lo || grad > hi ? "Graduation target: between " + A.fmtUnits(lo, BigInt(qd), 8) + " and " + A.fmtUnits(hi, BigInt(qd), 0) + " " + sym + "." : "",
      cbps: cbps === null ? "Creator allocation: 0 to 5 percent, two decimals at most." : "",
      tax: tax === null ? "Tax: 0 to 10 percent, two decimals at most." : tmode === 0 && tax > Z ? "Pick where the tax goes, or set the rate to 0." : tmode !== 0 && tax === Z ? "Set a rate above 0 for this tax, or pick No tax." : "",
    };
    return out(e, () => [["name", "S", name], ["ticker", "S", t], ["descr", "S", String(f.descr || "")], ["icon", "S", icon], ["tags", "S", String(f.tags || "")], ["quote", "H", q.scid || ZEROQ],
      ["dec", "U", dec], ["supply", "U", supply], ["grad", "U", grad], ["cbps", "U", cbps], ["tax", "U", tax], ["tmode", "U", BigInt(tmode)], ["treasury", "S", env.treasury], ["fee", "U", BigInt(env.feeBps)], ["pfee", "U", BigInt(env.launchPfeeBps)]],
    () => C.curve(supply, dec, grad, cbps, qd));
  };
  // what InitializePrivate computes from them (launch.bas 40-45), so the review states what the contract will hold
  C.curve = (supply, dec, grad, cbps, qd) => {
    const alloc = (supply / 10000n) * cbps + ((supply % 10000n) * cbps) / 10000n, rest = supply - alloc, lq = rest / 5n, c = rest - lq, vd0 = grad / 3n, vt0 = ((grad + vd0) * c) / grad;
    const p0 = A.priceOf({ dec, quoteDec: qd }, { rd: vd0, ra: vt0 });
    return { alloc, lq, c, vd0, vt0, p0, p1: p0 * 16, units: supply, heavy: supply > 100000000000n };
  };

  // ---- pool: contracts/pool.bas InitializePrivate ----
  C.pool = (f, env) => {
    const a = String(f.asset || "").trim().toLowerCase(), q = (env.quote && env.quote.scid) || ZEROQ;
    const e = { asset: !a ? "Paste the asset's contract id: 64 characters, 0 to 9 and a to f." : !HEX64.test(a) ? "A contract id is 64 characters of 0 to 9 and a to f; this one is " + a.length + (/[^0-9a-f]/.test(a) ? " and has other characters." : ".") :
      a === ZEROQ ? "DERO itself cannot be the pooled asset: pick the asset to trade against it." : a === q ? "The asset and the quote are the same: pick another quote." : "" };
    return out(e, () => [["asset", "H", a], ["quote", "H", q], ["treasury", "S", env.treasury], ["fee", "U", BigInt(env.feeBps)], ["pfee", "U", BigInt(env.pfeeBps)]]);
  };

  // ---- holder NFT: contracts/nft.bas InitializePrivate ----
  // env.token: the launch it gates on {scid, dec, ticker}
  C.nft = (f, env) => {
    const tok = env.token, name = String(f.name || "").trim(), icon = env.iconRef || String(f.icon || "").trim();
    const mh = tok ? units(f.minhold, tok.dec) : null, hb = whole(f.blocks, 1, 10000000), mx = whole(f.max, 1, U64 - 1n), pr = String(f.price || "").trim() === "" ? Z : units(f.price, 5);
    const e = {
      name: reqB("Name", name, 64), descr: maxB("The description", f.descr, 240), icon: env.iconRef ? "" : httpsErr("The icon URL", icon, 200), file: httpsErr("The image URL", f.file, 200),
      tags: maxB("Tags", f.tags, 100), coll: maxB("The collection name", f.coll, 64),
      token: !tok || !HEX64.test(String(tok.scid)) ? "Pick the coin a holder must lock." : "",
      minhold: !tok ? "" : mh === null || mh === undefined || mh === Z || mh >= U64 ? "Minimum hold: an amount above 0 with at most " + tok.dec + " decimals." : "",
      blocks: hb === null ? "Hold blocks: a whole number from 1 to 10,000,000." : "",
      max: mx === null ? "Max editions: a whole number, at least 1." : "",
      price: pr === null || pr === undefined || pr >= U64 ? "Mint price: an amount of DERO with at most 5 decimals, or 0." : "",
    };
    return out(e, () => [["name", "S", name], ["descr", "S", String(f.descr || "")], ["icon", "S", icon], ["file", "S", String(f.file || "").trim()], ["tags", "S", String(f.tags || "")], ["collection", "S", String(f.coll || "")],
      ["token", "H", tok.scid], ["minhold", "U", mh], ["holdblocks", "U", hb], ["maxsupply", "U", mx], ["price", "U", pr], ["treasury", "S", env.treasury], ["fee", "U", BigInt(env.feeBps)]]);
  };

  // ---- NFA: contracts/nfa.bas InitializePrivate + init ----
  // env.quote {scid: "" for DERO or the vault's}, protoFee (bps), treasury; f.canUpdate boolean
  C.nfa = (f, env) => {
    const s = (k) => String(f[k] || "").trim(), hx = (k) => { const v = s(k).toLowerCase(); return v === "" || /^[0-9a-f]{1,64}$/.test(v) ? v : null; };
    const pc = (k) => whole(f[k] === undefined || f[k] === "" ? "0" : f[k], 0, 100), fee = BigInt(env.protoFee || 0), icon = env.iconRef || s("icon");
    const cC = hx("checkC"), cS = hx("checkS"), roy = pc("royalty"), art = pc("art");
    const room = (10000n - fee) / 100n;
    const e = {
      name: reqB("Name", s("name"), 64), type: maxB("The type", s("type"), 32), descr: maxB("The description", f.descr, 240), icon: env.iconRef ? "" : httpsErr("The icon URL", icon, 200),
      tags: maxB("Tags", f.tags, 100), coll: maxB("The collection name", s("coll"), 64),
      file: httpsErr("The file URL", s("file"), 300), sign: httpsErr("The signature URL", s("sign"), 300), cover: httpsErr("The cover URL", s("cover"), 300),
      checkC: cC === null ? "Check C: the hex of filesign's C line, up to 64 characters." : cC !== null && cS !== null && !cC !== !cS ? "Give both checks, C and S, or neither." : "",
      checkS: cS === null ? "Check S: the hex of filesign's S line, up to 64 characters." : "",
      royalty: roy === null ? "Royalty: a whole percent from 0 to 100." : "", art: art === null ? "Artificer share: a whole percent from 0 to 100." : "",
    };
    if (roy !== null && art !== null && roy * 100n + art * 100n + fee > 10000n) e.royalty = "With the " + Number(fee) / 100 + "% protocol fee, royalty and artificer share can total " + room + "% at most: lower one of them.";
    return out(e, () => [["artificerFee", "U", art], ["royalty", "U", roy], ["ownerCanUpdate", "U", f.canUpdate ? 1n : Z], ["nameHdr", "S", s("name")], ["descrHdr", "S", s("descr")], ["typeHdr", "S", s("type")],
      ["iconURLHdr", "S", icon], ["tagsHdr", "S", s("tags")], ["fileCheckC", "S", cC], ["fileCheckS", "S", cS], ["fileURL", "S", s("file")], ["fileSignURL", "S", s("sign")], ["coverURL", "S", s("cover")],
      ["collection", "S", s("coll")], ["quote", "S", (env.quote && env.quote.scid) || ""], ["protoFee", "U", fee], ["treasury", "S", env.treasury]]);
  };

  // ---- links a profile document carries: https only, each on the host its name promises ----
  C.LINKS = [["website", "Website", null, "https://example.org"], ["x", "X", ["x.com", "twitter.com"], "https://x.com/yourname"], ["telegram", "Telegram", ["t.me", "telegram.me"], "https://t.me/yourgroup"],
    ["discord", "Discord", ["discord.gg", "discord.com"], "https://discord.gg/invite"], ["github", "GitHub", ["github.com"], "https://github.com/you"]];
  C.link = (key, v) => {
    const L = C.LINKS.find((x) => x[0] === key), s = String(v || "").trim();
    if (!s) return "";
    if (bytes(s) > 200) return L[1] + ": 200 bytes at most.";
    if (!/^https:\/\//i.test(s)) return L[1] + ": the link must start with https://, like " + L[3] + ".";
    let u; try { u = new URL(s); } catch (e) { return L[1] + ": not a link. Use one like " + L[3] + "."; }
    const host = u.hostname.toLowerCase().replace(/^www\./, "");
    if (u.username || u.password) return L[1] + ": remove the user name or password from the link.";
    if (/[\s"'<>\\]/.test(s) || !/\./.test(host)) return L[1] + ": not a link. Use one like " + L[3] + ".";
    if (L[2] && !L[2].includes(host)) return L[1] + ": use a link on " + L[2].join(" or ") + ", like " + L[3] + ".";
    if (L[2] && u.pathname.replace(/\//g, "") === "") return L[1] + ": add the account or invite after the host, like " + L[3] + ".";
    return "";
  };
  C.links = (links) => { const errors = {}, clean = {}; for (const [k] of C.LINKS) { const m = C.link(k, links && links[k]); if (m) errors[k] = m; else if (links && String(links[k] || "").trim()) clean[k] = String(links[k]).trim(); } return { ok: !Object.keys(errors).length, errors, links: clean, any: Object.keys(clean).length > 0 }; };

  // ---- cost, itemised: one wallet confirmation per item ----
  // p: {kind label, profile: bool, profileGas, installGas, regGas, regfee} — a gas of null is "not estimated"
  C.costs = (p) => {
    const items = [];
    if (p.profile) items.push({ k: "profile", label: "Install the logo and links document", gas: p.profileGas, why: "It is its own contract, installed first so that its id can go in the icon header, which is fixed at install." });
    items.push({ k: "install", label: "Install the " + p.what + " contract", gas: p.installGas, why: "One contract per " + p.what + ": the DVM cannot install a contract from inside another, so your wallet sends the install itself." });
    items.push({ k: "register", label: "Register it on the factory", gas: p.regGas, fee: p.regfee, why: "The factory can list only a contract that already exists, so registering waits for the install to be in a block." });
    let total = Z, complete = true;
    for (const i of items) { if (typeof i.gas === "bigint") total += i.gas; else complete = false; if (typeof i.fee === "bigint") total += i.fee; }
    return { items, total, complete, confirmations: items.length };
  };
  C.CONFIRM_WORDS = ["", "One", "Two", "Three"];

  // ---- the local record of the creator's own installs: recoverable after the page closes ----
  // A record holds the kind, the install arguments (public: they go on chain), the profile document's id and the
  // installed contract's id once known. No key, no seed, nothing the wallet holds.
  C.KEY = "dmz.creates.v1";
  const argOut = (a) => a.map(([n, t, v]) => [n, t, typeof v === "bigint" ? v.toString() : v]);
  C.argsIn = (a) => (Array.isArray(a) ? a.map(([n, t, v]) => [n, t, t === "U" ? BigInt(v) : v]) : null);
  const valid = (r) => r && typeof r.id === "string" && ["launch", "pool", "nft", "nfa"].includes(r.kind) && (!r.scid || HEX64.test(r.scid)) && (!r.profile || HEX64.test(r.profile)) && Array.isArray(r.args);
  C.load = (store) => { try { const a = JSON.parse(store.getItem(C.KEY) || "[]"); return Array.isArray(a) ? a.filter(valid) : []; } catch (e) { return []; } };
  C.save = (store, rec) => { const all = C.load(store).filter((r) => r.id !== rec.id); const r = Object.assign({}, rec, { args: argOut(rec.args || []) }); all.push(r); try { store.setItem(C.KEY, JSON.stringify(all)); } catch (e) {} return r; };
  C.drop = (store, id) => { try { store.setItem(C.KEY, JSON.stringify(C.load(store).filter((r) => r.id !== id))); } catch (e) {} };
  // chain: {profile, code, registered}: true, false, or null when it could not be read
  // what the record still needs: done | register | install | wait | lost
  C.next = (rec, chain) => {
    if (rec.scid) return chain.registered ? "done" : chain.code ? "register" : chain.code === false ? (rec.sent && Date.now() - rec.sent < 300000 ? "wait" : "lost") : "wait";
    if (rec.profile) return chain.profile ? "install" : chain.profile === false ? "lost" : "wait";
    return "lost";
  };
})();
