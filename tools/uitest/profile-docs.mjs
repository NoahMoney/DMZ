// The profile doc pass (app/profile.js, docs/PROFILE_DOCS.md). Runs under the app's own Content Security Policy,
// with no bypass, and fails if the page makes any request but to the app's own origin and the node's socket.
//
//   PW_DIR=<playwright> node tools/uitest/profile-docs.mjs <app url> <outdir> --encode [--pass-src=<png>]
//     builds a logo doc (from an image drawn in the page, with links) and, given --pass-src, a pass-image doc, in the
//     browser, through the editor at #profiledoc and #profiledoc/pass; writes logo.webp, logo.bas, pass.webp,
//     pass.bas and encode.json to <outdir>, and screenshots of both editors.
//   PW_DIR=<playwright> node tools/uitest/profile-docs.mjs <app url> <outdir> --launch=<scid> [--founders=<scid>]
//     a market whose icon header names a logo doc: its card on Markets and Home and its market page must show the
//     logo, read once through the node; --founders serves the app with that pass contract (config.js rewritten in
//     the browser, never on disk) and the pass screen must show the pass image. Phone and desktop screenshots.
import { createRequire } from "node:module";
import fs from "node:fs";
import path from "node:path";
const require = createRequire(path.resolve(process.env.PW_DIR || ".", "package.json"));
const { chromium } = require("playwright");
const url = process.argv[2], out = process.argv[3] || ".";
const arg = (k) => (process.argv.find((a) => a.startsWith("--" + k + "=")) || "").slice(k.length + 3);
const encodeMode = process.argv.includes("--encode"), passSrc = arg("pass-src"), launch = arg("launch"), founders = arg("founders");
if (!url || (!encodeMode && !/^[0-9a-f]{64}$/.test(launch))) { console.error("usage: profile-docs.mjs <app url> <outdir> --encode [--pass-src=<png>] | --launch=<scid> [--founders=<scid>]"); process.exit(2); }
fs.mkdirSync(out, { recursive: true });
const app = new URL(url), sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const step = (s) => console.log("• " + s), problems = [];
const fail = (m) => { console.error("FAIL: " + m); problems.push(m); };
const LINKS = { Website: "https://example.org/pdoc", X: "https://x.com/example", Telegram: "https://t.me/example", Discord: "https://discord.gg/example", GitHub: "https://github.com/example" };

const browser = await chromium.launch();
console.log(`• playwright ${require("playwright/package.json").version}, chromium ${browser.version()}`);

// every request the page makes: the app's origin and data: only; sockets to loopback only
function audit(page, tag) {
  const log = { requests: 0, off: [], sockets: [], codeReads: 0 };
  page.on("request", (r) => { log.requests++; const u = new URL(r.url()); if (u.protocol !== "data:" && u.origin !== app.origin) log.off.push(r.url()); });
  page.on("websocket", (ws) => {
    const u = new URL(ws.url()); log.sockets.push(ws.url()); if (!["127.0.0.1", "localhost"].includes(u.hostname)) log.off.push(ws.url());
    ws.on("framesent", (f) => { const p = String(f.payload); if (p.includes('"DERO.GetSC"') && p.includes('"code":true')) log.codeReads++; });
  });
  page.on("pageerror", (e) => fail(tag + " pageerror: " + e.message));
  page.on("console", (m) => { if (m.type() === "error" && !/favicon/.test(m.text())) fail(tag + " console: " + m.text()); });
  return log;
}
// polling through evaluate, never waitForFunction: the page's CSP has no unsafe-eval and is not bypassed here
async function until(page, fn, a, ms = 30000) {
  const t0 = Date.now();
  for (;;) { const v = await page.evaluate(fn, a).catch(() => null); if (v) return v; if (Date.now() - t0 > ms) return null; await sleep(250); }
}
async function open(page, hash) {
  await page.goto(url + (hash ? "#" + hash : ""), { waitUntil: "load" });
  await page.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
  if (!(await until(page, () => window.Veil && window.Veil.data && window.Veil.data.lastRefresh > 0, null, 60000))) fail("the app did not load its data");
}
const go = async (page, hash) => { await page.evaluate((h) => { location.hash = h; }, "#" + hash); await sleep(600); };
const shot = (page, n) => page.screenshot({ path: path.join(out, n + ".png"), fullPage: true });

async function encode() {
  const page = await browser.newPage({ viewport: { width: 430, height: 860 }, deviceScaleFactor: 2 });
  const log = audit(page, "encode");
  await open(page, "profiledoc");
  step("logo: links typed, an image drawn in the page, encoded by the editor");
  for (const [label, v] of Object.entries(LINKS)) await page.fill(`.pd-ed input[aria-label="${label}"]`, v);
  await page.evaluate(async () => {
    const c = document.createElement("canvas"); c.width = c.height = 512; const g = c.getContext("2d");
    g.fillStyle = "#0a0b0a"; g.fillRect(0, 0, 512, 512); g.fillStyle = "#b4f03f"; g.beginPath(); g.moveTo(300, 0); g.lineTo(420, 0); g.lineTo(212, 512); g.lineTo(92, 512); g.fill();
    g.fillStyle = "#e9eae7"; g.font = "bold 230px sans-serif"; g.textAlign = "center"; g.textBaseline = "middle"; g.fillText("PD", 256, 270);
    const b = await new Promise((r) => c.toBlob(r, "image/png")), dt = new DataTransfer(); dt.items.add(new File([b], "logo.png", { type: "image/png" }));
    const inp = document.querySelector('.pd-ed input[type=file]'); inp.files = dt.files; inp.dispatchEvent(new Event("change"));
  });
  const logo = await until(page, () => { const b = window.Veil.profile.lastBuild; return b && b.preview && b.links && Object.keys(b.links).length === 5 ? b : null; });
  if (!logo) { fail("the logo build did not finish: " + (await page.locator("#screen").innerText()).slice(0, 300)); return; }
  await sleep(400); await shot(page, "pd-01-logo-editor");
  const rel = await page.evaluate(() => [...document.querySelectorAll(".pd-link")].map((a) => [a.rel, a.target, a.getAttribute("href"), a.textContent]));
  if (rel.length !== 5 || rel.some(([r, t]) => r !== "noopener noreferrer nofollow" || t !== "_blank")) fail("links are not external anchors with rel noopener noreferrer nofollow: " + JSON.stringify(rel));
  save("logo", logo);
  if (passSrc) {
    step("pass image: " + path.basename(passSrc) + " through #profiledoc/pass");
    await go(page, "profiledoc/pass");
    await page.setInputFiles('.pd-ed input[type=file]', passSrc);
    const pass = await until(page, () => { const b = window.Veil.profile.lastBuild; return b && b.kind === "pass" ? b : null; }, null, 120000);
    if (!pass) { fail("the pass build did not finish: " + (await page.locator("#screen").innerText()).slice(0, 300)); return; }
    await sleep(400); await shot(page, "pd-02-pass-editor");
    await page.locator(".pd-prev .avatar.big").screenshot({ path: path.join(out, "pd-03-pass-preview.png") });
    save("pass", pass);
  }
  report(log, "encode");
}
function save(kind, b) {
  const img = Buffer.from(b.preview.split(",")[1], "base64");
  fs.writeFileSync(path.join(out, kind + "." + b.type), img); fs.writeFileSync(path.join(out, kind + ".bas"), b.code);
  const meta = { kind, type: b.type, width: b.width, height: b.height, imageBytes: b.imageBytes, quality: b.quality, codeBytes: b.bytes, sha256: b.sha256, gasEstimate: b.gasEstimate, gasSource: b.gasSource, links: b.links };
  const f = path.join(out, "encode.json"); let all = {}; try { all = JSON.parse(fs.readFileSync(f, "utf8")); } catch (e) {}
  all[kind] = meta; fs.writeFileSync(f, JSON.stringify(all, null, 1));
  step(kind + ": " + JSON.stringify(meta));
}
function report(log, tag) {
  step(tag + ": " + log.requests + " requests, all to " + app.origin + (log.off.length ? "; OFF-ORIGIN: " + log.off.join(", ") : "") + "; sockets " + [...new Set(log.sockets)].join(", ") + "; code reads " + log.codeReads);
  if (log.off.length) fail(tag + ": requests left the app's origin and the node: " + log.off.join(", "));
}
// evaluated in the page with the selector as its argument
const imgOf = (s) => { const i = document.querySelector(s); return i && i.complete && i.naturalWidth > 0 ? { src: i.getAttribute("src").slice(0, 23), w: i.getAttribute("width"), h: i.getAttribute("height"), alt: i.getAttribute("alt"), decoding: i.getAttribute("decoding"), natural: i.naturalWidth, shown: Math.round(i.getBoundingClientRect().width) } : null; };

async function show(width) {
  const tag = width < 700 ? "phone" : "desktop", pre = width < 700 ? "pd-1" : "pd-2";
  const page = await browser.newPage({ viewport: { width, height: 860 }, deviceScaleFactor: 2 });
  if (founders) await page.route("**/config.js", async (route) => { const r = await route.fetch(); await route.fulfill({ response: r, body: (await r.text()).replace(/founders: "[0-9a-f]{64}"/, `founders: "${founders}"`) }); });
  const log = audit(page, tag);
  await open(page, "markets");
  step(tag + ": the PDOC card on Markets");
  const card = await until(page, () => { const i = [...document.querySelectorAll(".avatar.pd-img img")].find((x) => (x.closest("a,button,.mrow,.card,li") || document.body).textContent.includes("PDOC")); return i && i.complete && i.naturalWidth > 0 ? { src: i.getAttribute("src").slice(0, 23), alt: i.alt, decoding: i.getAttribute("decoding"), w: i.getAttribute("width"), shown: Math.round(i.getBoundingClientRect().width) } : null; });
  if (!card) fail(tag + ": no logo on the PDOC card"); else step(tag + " card img " + JSON.stringify(card));
  await shot(page, pre + "0-markets");
  const row = page.locator(".mrow", { hasText: "PDOC" }).first();
  if (await row.count()) await row.screenshot({ path: path.join(out, pre + "1-markets-row.png") });
  const reads = log.codeReads;
  await go(page, "home"); await sleep(1500); await shot(page, pre + "2-home");
  step(tag + ": the PDOC market page");
  await go(page, "token/" + launch);
  const big = await until(page, imgOf, ".avatar.big.pd-img img");
  if (!big) fail(tag + ": no logo on the market page"); else step(tag + " market page img " + JSON.stringify(big));
  if (big && (big.decoding !== "async" || !big.w || !big.h || !/logo$/.test(big.alt) || !/^data:image\/(webp|png);/.test(big.src))) fail(tag + ": the img is not <img decoding=async width height alt> from a data: URI: " + JSON.stringify(big));
  await sleep(500); await shot(page, pre + "3-token");
  if (log.codeReads !== reads) fail(tag + ": the doc was read again on the market page (" + reads + " → " + log.codeReads + "): the cache missed");
  if (founders) {
    step(tag + ": the pass screen with the demo pass");
    await go(page, "nfts");
    const pass = await until(page, imgOf, ".hero .avatar.big.pd-pass img", 40000);
    if (!pass) fail(tag + ": no pass image on the pass screen"); else step(tag + " pass img " + JSON.stringify(pass));
    await sleep(500); await shot(page, pre + "4-pass");
    const hero = page.locator(".hero").first(); if (await hero.count()) await hero.screenshot({ path: path.join(out, pre + "5-pass-hero.png") });
  }
  report(log, tag);
  await page.close();
}

try {
  if (encodeMode) await encode();
  else { await show(430); await show(1280); }
} catch (e) { fail(e.stack || String(e)); }
await browser.close();
console.log(problems.length ? "PROFILE DOCS PASS FAILED (" + problems.length + ")" : "PROFILE DOCS PASS OK");
process.exit(problems.length ? 1 : 0);
