// Records a short demo video and viewport screenshots of the running app, phone and desktop.
//   PW_DIR=<playwright dir> node tools/uitest/capture.mjs http://127.0.0.1:8082/index.html docs/ui2
import { createRequire } from "node:module";
import fs from "node:fs";
import path from "node:path";
const require = createRequire((process.env.PW_DIR || ".") + "/package.json");
const { chromium } = require("playwright");
const url = process.argv[2], out = process.argv[3] || ".";
fs.mkdirSync(out, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const browser = await chromium.launch();

// ---- phone ----
const ctx = await browser.newContext({ bypassCSP: true, viewport: { width: 430, height: 860 }, deviceScaleFactor: 2, recordVideo: { dir: out, size: { width: 430, height: 860 } } });
const page = await ctx.newPage();
const shot = (n) => page.screenshot({ path: path.join(out, n + ".png") });
await page.goto(url, { waitUntil: "load" });
await page.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
await page.waitForFunction(() => window.Veil && window.Veil.data.lastRefresh > 0, null, { timeout: 60000 });
await sleep(2500);
await shot("shot-home");
await sleep(5000);
await page.evaluate(() => { location.hash = "#markets"; }); await sleep(2500);
await shot("shot-markets");
await sleep(2500);
await page.evaluate(() => { location.hash = "#nfts"; }); await sleep(2500);
await shot("shot-nfts");
await sleep(2000);
const top = await page.evaluate(() => { const t = window.Veil.data.trending(1); return t.length ? t[0].scid : null; });
if (top) {
  await page.evaluate((s) => { location.hash = "#token/" + s; }, top); await sleep(2500);
  await shot("shot-token");
  const box = await page.locator(".chart svg").first().boundingBox();
  if (box) { for (let i = 0; i <= 30; i++) { await page.mouse.move(box.x + 8 + ((box.width - 120) * i) / 30, box.y + box.height * 0.45); await sleep(100); } await shot("shot-token-crosshair"); await page.mouse.move(box.x - 10, box.y - 10); }
  await page.getByRole("button", { name: "Buy", exact: true }).first().click(); await sleep(600);
  await page.locator("#sheet input").first().fill("0.5"); await sleep(700); await shot("shot-trade");
  await sleep(2000); await page.keyboard.press("Escape"); await sleep(500);
}
await page.evaluate(() => { location.hash = "#home"; }); await sleep(4000);
const video = page.video();
await ctx.close();
const p = await video.path();
fs.renameSync(p, path.join(out, "veil-demo.webm"));

// ---- desktop ----
const d = await browser.newPage({ bypassCSP: true, viewport: { width: 1280, height: 860 }, deviceScaleFactor: 1.5 }); // Playwright polls through eval; the policy itself is verified by ui.mjs
await d.goto(url, { waitUntil: "load" });
await d.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
await d.waitForFunction(() => window.Veil && window.Veil.data.lastRefresh > 0, null, { timeout: 60000 });
await sleep(2500);
for (const tab of ["home", "markets", "nfts"]) { await d.evaluate((t) => { location.hash = "#" + t; }, tab); await sleep(2000); await d.screenshot({ path: path.join(out, "desk-" + tab + ".png") }); }
if (top) { await d.evaluate((s) => { location.hash = "#token/" + s; }, top); await sleep(2500); await d.screenshot({ path: path.join(out, "desk-token.png") }); }
await browser.close();
console.log("video", path.join(out, "veil-demo.webm"), fs.statSync(path.join(out, "veil-demo.webm")).size, "bytes; desktop shots written");
