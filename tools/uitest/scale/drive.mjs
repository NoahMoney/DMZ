// tools/uitest/scale/drive.mjs — the scale harness driver (TEST-ONLY; results in docs/PERFORMANCE.md).
// Starts mock.mjs with N synthetic markets, opens the app on #markets in headless Chromium and records, per N:
//   first row painted and full list (cold, empty localStorage; then warm, after a reload), ms since navigation;
//   requests per block in steady state (from the mock's own counters), keys asked for per block;
//   main-thread time per block (CDP TaskDuration and ScriptDuration deltas), long tasks per block;
//   decorate() and changed() time per block, the median refresh wall time; JS heap after a GC; localStorage size.
//
//   PW_DIR=<dir with playwright 1.62.0> node tools/uitest/scale/drive.mjs --app=app --n=50,500 [--blocks=6]
//     [--block-ms=3000] [--see=40] [--label=after] [--out=.sim/scale] [--rtt=0] [--any-playwright]
// The Markets screen declares its own rows on screen (D.see, docs/PERFORMANCE.md §interface), and each run records
// what was declared per key and the size of the hot set that made (declared, hot). --see=K adds, under the key
// "main", the K rows a first screenful of trending shows, as the data layer's own runs did before any screen
// declared.
// The numbers describe this harness's invented chain against a node that answers in microseconds: they compare
// builds with each other, and are never product numbers.
import { createRequire } from "node:module";
import fs from "node:fs";
import path from "node:path";
import { startMock } from "./mock.mjs";

const arg = (k, d) => { const a = process.argv.find((x) => x.startsWith("--" + k + "=")); return a ? a.slice(k.length + 3) : d; };
const require = createRequire(path.join(process.env.PW_DIR || ".", "package.json"));
const { chromium } = require("playwright");
const appDir = path.resolve(arg("app", "app")), Ns = arg("n", "50,500").split(",").map(Number), B = +arg("blocks", 6), blockMs = +arg("block-ms", 3000);
const see = +arg("see", 0), label = arg("label", path.basename(path.dirname(appDir)) || "run"), out = arg("out", ".sim/scale"), rtt = +arg("rtt", 0);
fs.mkdirSync(out, { recursive: true });
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// runs in the page before any app script: marks the first market row and the first complete refresh, counts long
// tasks, and times every refresh cycle, decorate() and changed()
const probe = () => {
  try { if (!localStorage.getItem("veil.settings")) localStorage.setItem("veil.settings", JSON.stringify({ seen: true })); } catch (e) {}
  const m = (window.__scale = { lt: 0, ltn: 0, refresh: [], dec: 0, chg: 0 });
  try { new PerformanceObserver((l) => { for (const e of l.getEntries()) { m.lt += e.duration; m.ltn++; } }).observe({ type: "longtask", buffered: true }); } catch (e) {}
  const time = (D, k, acc) => { const f = D[k]; D[k] = function (...a) { const t0 = performance.now(); try { return f.apply(this, a); } finally { m[acc] += performance.now() - t0; } }; };
  const tick = () => {
    const t = performance.now(), D = window.Veil && window.Veil.data;
    if (D && !m.wrapped && D.refresh && D.decorate) {
      m.wrapped = 1; time(D, "decorate", "dec"); time(D, "changed", "chg");
      const f = D.refresh;
      D.refresh = function (...a) { const fresh = !this.refreshing, t0 = performance.now(), p = f.apply(this, a); if (fresh) Promise.resolve(p).catch(() => {}).then(() => m.refresh.push(performance.now() - t0)); return p; };
    }
    if (!m.row && document.querySelector(".mrow.mkt")) m.row = t;
    if (!m.full && D && D.lastRefresh) m.full = t;
    if (!(m.row && m.full)) setTimeout(tick, 20);
  };
  tick();
};

const browser = await chromium.launch();
console.log("• chromium " + browser.version() + " · app " + appDir + " · label " + label + (see ? " · see " + see : ""));
const rows = [];
for (const n of Ns) {
  const mock = await startMock({ n, blockMs, appDir, rtt });
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 }, bypassCSP: true });
  await ctx.addInitScript(probe);
  const page = await ctx.newPage(), errs = [];
  page.on("pageerror", (e) => errs.push(e.message));
  const cdp = await ctx.newCDPSession(page); await cdp.send("Performance.enable");
  const metrics = async () => Object.fromEntries((await cdp.send("Performance.getMetrics")).metrics.map((x) => [x.name, x.value]));
  const r = { label, n, markets: mock.markets(), see };
  const load = async (kind) => {
    if (kind === "cold") await page.goto(mock.url + "#markets", { waitUntil: "load" }); else await page.reload({ waitUntil: "load" });
    await page.waitForFunction(() => window.__scale && window.__scale.full && window.__scale.row, null, { timeout: 900000, polling: 250 }).catch(() => {});
    const t = await page.evaluate(() => ({ row: window.__scale.row, full: window.__scale.full }));
    r[kind + "Row"] = Math.round(t.row || NaN); r[kind + "Full"] = Math.round(t.full || NaN);
  };
  mock.reset();
  await load("cold");
  // calls the node answered until the list was complete: load-independent, unlike the milliseconds around it
  r.coldCallsPerMarket = Math.round((Object.values(mock.stats().calls).reduce((a, b) => a + b, 0) / mock.markets()) * 100) / 100;
  if (see) r.seeOk = await page.evaluate((k) => { const D = window.Veil.data; if (!D.see) return false; const f = () => D.see(D.trending(k).map((s) => s.scid)); D.onChange(f, true); f(); return true; }, see);
  // settle: two blocks, so a tiered build has planned its cold tier once, then measure over B whole blocks
  await mock.nextBlock(); await mock.nextBlock();
  const s0 = await page.evaluate(() => ({ lt: window.__scale.lt, ltn: window.__scale.ltn, dec: window.__scale.dec, chg: window.__scale.chg, nref: window.__scale.refresh.length }));
  const c0 = await metrics(); mock.reset();
  for (let i = 0; i < B; i++) await mock.nextBlock();
  const st = mock.stats(), c1 = await metrics();
  const s1 = await page.evaluate(() => ({ lt: window.__scale.lt, ltn: window.__scale.ltn, dec: window.__scale.dec, chg: window.__scale.chg, ref: window.__scale.refresh }));
  const per = (x) => Math.round((x / B) * 10) / 10;
  r.reqPerBlock = per(Object.values(st.calls).reduce((a, b) => a + b, 0));
  r.getscPerBlock = per(st.calls["DERO.GetSC"] || 0);
  r.keysPerBlock = per(st.keys);
  r.kbPerBlock = per(st.bytes / 1024);
  r.taskMsPerBlock = per((c1.TaskDuration - c0.TaskDuration) * 1000);
  r.scriptMsPerBlock = per((c1.ScriptDuration - c0.ScriptDuration) * 1000);
  r.longTaskMsPerBlock = per(s1.lt - s0.lt);
  r.decorateMsPerBlock = per(s1.dec - s0.dec);
  r.changedMsPerBlock = per(s1.chg - s0.chg);
  const refs = s1.ref.slice(s0.nref).sort((a, b) => a - b);
  r.refreshMedianMs = refs.length ? Math.round(refs[Math.floor(refs.length / 2)]) : null;
  r.refreshes = refs.length;
  // what one refresh cycle asks of the node, whatever the machine's load did to how many cycles fit in the window
  r.reqPerRefresh = refs.length ? Math.round(Object.values(st.calls).reduce((a, b) => a + b, 0) / refs.length) : null;
  // what the screens declared at the end of the window, per key, and the hot set that made (null: none declared)
  Object.assign(r, await page.evaluate(() => {
    const D = window.Veil.data, x = D.seen, cur = !!x && x.gen === (window.Veil.ui.router.gen | 0), hs = D.hotSet ? D.hotSet() : null;
    return { declared: cur ? Object.fromEntries(Object.entries(x.by).map(([k, v]) => [k, v.size])) : null, hot: hs ? hs.size : null };
  }));
  await page.evaluate(() => window.Veil.data.flushCache && window.Veil.data.flushCache());
  await cdp.send("HeapProfiler.collectGarbage");
  r.heapMB = Math.round(((await metrics()).JSHeapUsedSize / 1048576) * 10) / 10;
  r.lsKchars = await page.evaluate(() => { let c = 0; for (let i = 0; i < localStorage.length; i++) { const k = localStorage.key(i); c += k.length + (localStorage.getItem(k) || "").length; } return Math.round(c / 1024); });
  r.lsKeys = await page.evaluate(() => localStorage.length);
  await load("warm");
  r.errors = errs.length; if (errs.length) r.firstError = errs[0].slice(0, 200);
  rows.push(r);
  console.log(JSON.stringify(r));
  await ctx.close(); await mock.stop();
}
await browser.close();
fs.writeFileSync(path.join(out, "scale-" + label + ".json"), JSON.stringify({ when: new Date().toISOString(), appDir, blocks: B, blockMs, rtt, rows }, null, 2));
const cols = ["n", "hot", "coldRow", "coldFull", "warmFull", "coldCallsPerMarket", "reqPerRefresh", "reqPerBlock", "getscPerBlock", "keysPerBlock", "kbPerBlock", "taskMsPerBlock", "decorateMsPerBlock", "changedMsPerBlock", "refreshMedianMs", "heapMB", "lsKchars", "errors"];
console.log("| " + cols.join(" | ") + " |\n|" + cols.map(() => "---").join("|") + "|\n" + rows.map((r) => "| " + cols.map((c) => r[c]).join(" | ") + " |").join("\n"));
