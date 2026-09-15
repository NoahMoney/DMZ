// The market page and its chart at desktop, tablet and phone widths (the ui-market pass).
//   PW_DIR=<playwright> node tools/uitest/ui-market.mjs http://127.0.0.1:8082/index.html <outdir> [--mainnet]
// ui.mjs calls marketPass() from its read-only pass. Standalone, it opens its own browser; --mainnet serves the same
// app under a mainnet-shaped config (network "mainnet", no vault), as ui.mjs --mainnet does, and fails on any request
// that leaves this device. Writes [mainnet-]market-<width>.png, chart-<width>.png, chart-states-<width>.png and
// trade-sheet-<width>.png: read them.
import { createRequire } from "node:module";
import fs from "node:fs";
import { pathToFileURL } from "node:url";

const IVS = ["1m", "5m", "15m", "1h", "4h", "1D", "1W"];
const local = (s) => { try { const u = new URL(s); return u.protocol === "data:" || /^(127\.0\.0\.1|localhost)$/.test(u.hostname); } catch (e) { return false; } };
const shape = async (route) => {
  const r = await route.fetch();
  await route.fulfill({ status: 200, contentType: "text/javascript; charset=utf-8", body: (await r.text()) + '\n;(function (c) { c.network = "mainnet"; delete c.vaults; delete c.vault; delete c.vaultAsset; })(window.VEIL_CONFIG);\n' });
};

export async function marketPass({ browser, url, out, step, fail, sleep, mainnet = false }) {
  const pre = mainnet ? "mainnet-" : "";
  for (const width of [1280, 768, 390]) {
    const p = await browser.newPage({ viewport: { width, height: 900 }, bypassCSP: true });
    const perr = [], away = [];
    p.on("pageerror", (e) => perr.push("pageerror: " + e.message));
    p.on("console", (m) => { if (m.type() === "error" && !/favicon|net::ERR_/.test(m.text())) perr.push("console: " + m.text()); });
    p.on("request", (r) => { if (!local(r.url())) away.push(r.url()); });
    if (mainnet) await p.route(/\/config\.js(\?.*)?$/, shape);
    await p.goto(url, { waitUntil: "load" }); await p.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
    await p.waitForFunction(() => window.Veil && window.Veil.data.lastRefresh > 0, null, { timeout: 60000 }).catch(() => {});
    const scid = await p.evaluate(() => { const t = window.Veil.data.trending(1); return t.length ? t[0].scid : null; });
    const at = pre + width + "px";
    if (!scid) { step("market " + at + ": no listed market, page not exercised"); await p.close(); continue; }
    await p.evaluate((s) => { location.hash = "#token/" + s; }, scid); await sleep(2200);
    await p.screenshot({ path: `${out}/${pre}market-${width}.png`, fullPage: true });
    await p.locator(".tk-main .chart").first().screenshot({ path: `${out}/${pre}chart-${width}.png` });
    const read = () => p.evaluate(() => {
      const q = (s) => document.querySelector(s), chart = q(".tk-main .chart"), svg = chart && chart.querySelector("svg");
      return {
        h1: ((q(".tk-main h1") || {}).textContent || "").trim(), chips: [...document.querySelectorAll(".tk-ctl .chips .chip")].map((b) => b.textContent),
        on: ((q(".tk-ctl .chip.on") || {}).textContent || ""), iv: chart && chart.dataset.iv, bad: !svg || /NaN|Infinity|undefined/.test(svg.outerHTML),
        vb: svg ? +svg.getAttribute("viewBox").split(" ")[2] : 0, cw: chart ? Math.round(chart.getBoundingClientRect().width) : 0,
        over: document.documentElement.scrollWidth - document.documentElement.clientWidth, txt: document.getElementById("screen").innerText.replace(/\s+/g, " "),
        rels: [...document.querySelectorAll(".tk-links a")].map((a) => a.rel), panel: q(".tk-panel") ? getComputedStyle(q(".tk-panel")).display : "missing",
        bar: getComputedStyle(q(".sticky-actions")).position, tabs: [...document.querySelectorAll(".tk-side .seg button")].map((b) => b.textContent),
        // a market with a wrapped side (a wXMR or wZEC quote, or a wrapped base) carries the 1:1 mark, and only it
        wrapWant: (() => { const D = window.Veil.data, s = D.assets.get(location.hash.split("/")[1]), vs = D.allVaults(); return !!s && (vs.some((v) => v.scid === s.quote) || (!!s.wrappedBase && (!!s.vaultRec || vs.some((v) => v.scid === s.asset)))); })(),
        mark: [...document.querySelectorAll(".tk-main .pill")].some((p) => p.textContent === "wrapped · 1:1"),
      };
    });
    const r = await read();
    step("market " + at + ": " + r.h1 + " · intervals " + r.chips.join(" ") + " (on " + r.on + ") · chart " + r.cw + "px, viewBox " + r.vb + " · panel " + r.panel + ", bar " + r.bar);
    if (!/\S \/ \S/.test(r.h1)) fail("the market header is not a pair: " + JSON.stringify(r.h1));
    if (r.chips[r.chips.length - 1] !== "ALL" || r.chips.slice(0, -1).some((c, i, a) => !IVS.includes(c) || (i && IVS.indexOf(c) <= IVS.indexOf(a[i - 1])))) fail("interval chips: " + r.chips.join(","));
    if (r.bad) fail("the chart SVG at " + at + " holds NaN, Infinity or undefined");
    if (Math.abs(r.vb - 2 * r.cw) > 4) fail("the chart is not drawn to its box at " + at + ": viewBox " + r.vb + " for " + r.cw + "px");
    if (r.over > 1) fail("the market page overflows horizontally by " + r.over + "px at " + at);
    if (/\bTraders\b/.test(r.txt) || r.tabs.includes("Holders")) fail("a publisher count is labelled as traders or holders: tabs " + r.tabs.join(","));
    if (r.rels.some((x) => x !== "noopener noreferrer nofollow")) fail("an external link without rel=noopener noreferrer nofollow: " + r.rels.join("|"));
    if (/\bendorse/i.test(r.txt.replace(/not an endorsement/gi, ""))) fail("the market page uses the word endorse");
    step("wrapped side " + at + ": " + (r.wrapWant ? "yes" : "no") + ", mark " + (r.mark ? "shown" : "not shown"));
    if (r.wrapWant !== r.mark) fail("the wrapped · 1:1 mark is " + (r.mark ? "shown" : "missing") + " on a market " + (r.wrapWant ? "with" : "without") + " a wrapped side at " + at);
    // the ZEC and XMR swaps are removed from DMZ (operator ruling): no offers section, no way to a swap board
    if (/swap board|\bZEC \/ \S+ offers\b|atomic swap/i.test(r.txt)) fail("the market page still shows swap offers at " + at);
    if (width >= 1180 ? r.panel !== "block" || r.bar !== "static" : r.panel !== "none" || r.bar !== "fixed") fail("trade panel at " + at + ": panel " + r.panel + ", bar " + r.bar);
    // every offered interval draws, sanely, and says which it is
    for (const c of r.chips) {
      await p.locator(".tk-ctl .chips .chip", { hasText: new RegExp("^" + c + "$") }).click(); await sleep(250);
      const x = await read();
      if (x.on !== c || x.bad || x.iv !== (c === "ALL" ? "all" : c)) fail("interval " + c + " at " + at + ": on " + x.on + ", drawn " + x.iv + (x.bad ? ", NaN in the SVG" : ""));
    }
    // the crosshair by keyboard, and what it says
    await p.locator(".tk-main .chart").first().focus(); await p.keyboard.press("End"); await p.keyboard.press("ArrowLeft"); await sleep(150);
    const said = await p.evaluate(() => { const c = document.querySelector(".tk-main .chart"); return { sr: (c.querySelector(".sr") || {}).textContent || "", cross: !!c.querySelector('g[visibility="visible"]'), tip: !c.querySelector(".ctip").hidden }; });
    step("keyboard crosshair " + at + ": " + said.sr.slice(0, 120));
    if (!/candle \d+ of \d+: open .+ close .+/.test(said.sr) || !said.cross || !said.tip) fail("the keyboard crosshair at " + at + ": " + JSON.stringify(said));
    await p.keyboard.press("Escape");
    // a refresh that changes nothing redraws nothing: the same nodes survive two data ticks
    const same = await p.evaluate(async () => {
      const svg = document.querySelector(".tk-main .chart svg"), h1 = document.querySelector(".tk-main h1"); svg.__k = h1.__k = 1;
      window.Veil.data.changed(); window.Veil.data.changed(); await new Promise((r) => setTimeout(r, 300));
      return { svg: document.querySelector(".tk-main .chart svg").__k === 1, h1: document.querySelector(".tk-main h1").__k === 1 };
    });
    step("redraw on an unchanged refresh " + at + ": chart " + (same.svg ? "kept" : "REDRAWN") + ", header " + (same.h1 ? "kept" : "REDRAWN"));
    if (!same.svg || !same.h1) fail("an unchanged refresh redrew the market page at " + at + ": " + JSON.stringify(same));
    if (width === 1280) {
      await p.setViewportSize({ width: 900, height: 900 }); await sleep(700);
      const z = await read();
      step("resized to 900px: chart " + z.cw + "px, viewBox " + z.vb);
      if (Math.abs(z.vb - 2 * z.cw) > 4 || z.vb === r.vb) fail("the chart did not redraw to its new width: viewBox " + z.vb + " for " + z.cw + "px (was " + r.vb + ")");
      await p.setViewportSize({ width, height: 900 }); await sleep(500);
    }
    // the states nobody sees on a healthy simulator: drawn into a strip over the page and captured
    if (width === 1280 || width === 390) {
      const states = await p.evaluate((w) => {
        const V = window.Veil, now = Math.floor(Date.now() / 1000), RD = 10n ** 20n;
        const mk = (ps, step) => { const points = ps.map((x, i) => ({ height: i, ts: now - (ps.length - 1 - i) * step, rd: RD, ra: BigInt(Math.round(1e20 / x)) })); return { kind: "pool", dec: 5n, quoteDec: 5, points, trades: [], price: ps[ps.length - 1] || 0, pc: BigInt(ps.length) }; };
        const box = document.createElement("div"); box.id = "mkStates"; box.style.cssText = "position:absolute;left:0;top:0;right:0;z-index:99;background:var(--bg);padding:12px 16px";
        const cases = [["Loading", null, { loading: true }], ["Empty", mk([], 18)], ["One point", mk([0.5], 18)], ["Flat", mk(Array(200).fill(0.004), 18)], ["Sparse", mk([0.3, 0.3, 0.9, 0.2], 2 * 86400)],
          ["Swing ×1e9", mk(Array.from({ length: 300 }, (_, i) => 10 ** (-6 + (9 * i) / 299)), 60)], ["Tiny", mk(Array.from({ length: 300 }, (_, i) => 1.2e-13 * (1 + 0.3 * Math.sin(i / 9))), 18)], ["Huge", mk(Array.from({ length: 300 }, (_, i) => 3e12 * (1 + 0.1 * Math.cos(i / 7))), 18)]];
        const bad = [];
        for (const [name, s, o] of cases) {
          const c = V.chart.render(null, s, Object.assign({ width: Math.min(w - 32, 720), height: 170, quote: "DERO" }, o || {}));
          if (/NaN|Infinity|undefined/.test(c.outerHTML)) bad.push(name);
          const l = document.createElement("div"); l.className = "eyebrow mt"; l.textContent = name; box.append(l, c);
        }
        document.body.append(box); return bad;
      }, width);
      await sleep(200); await p.locator("#mkStates").screenshot({ path: `${out}/${pre}chart-states-${width}.png` });
      await p.evaluate(() => document.getElementById("mkStates").remove());
      if (states.length) fail("chart states with NaN at " + at + ": " + states.join(", "));
    }
    // the order sheet: pay and receive in the quote, the network fee apart in DERO, and a review naming the pair,
    // side, amounts, minimum and network
    await p.getByRole("button", { name: "Buy", exact: true }).first().click(); await sleep(400);
    await p.locator("#sheet input").first().fill("0.5"); await sleep(500);
    await p.screenshot({ path: `${out}/${pre}trade-sheet-${width}.png`, fullPage: false });
    const sh = (await p.locator("#sheet").innerText()).replace(/\s+/g, " "), net = await p.evaluate(() => window.Veil.data.cfg.network);
    for (const want of [/Review/i, /Pair \S+ \/ \S+/, /Side Buy \S+, pay \S+/, /You pay [\d.,]+ \S+/, /Minimum received, or it reverts/, new RegExp("Network DERO " + net), /Network fee \(gas\), in DERO/, /Max slippage/, /of it, protocol treasury/])
      if (!want.test(sh)) fail("the trade sheet at " + at + " does not show " + want + ": " + sh.slice(0, 300));
    step("trade sheet " + at + ": " + sh.slice(sh.search(/Review/i), sh.search(/Review/i) + 200));
    await p.keyboard.press("Escape"); await sleep(200);
    if (mainnet && away.length) fail("the mainnet-shaped market page reached a host other than this device: " + away.join(", "));
    if (perr.length) fail("console errors on the market page at " + at + ":\n" + perr.join("\n"));
    await p.close();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const url = process.argv[2], out = process.argv[3] || ".";
  const require = createRequire((process.env.PW_DIR || ".") + "/package.json");
  const { chromium } = require("playwright");
  console.log("• playwright " + require("playwright/package.json").version + " (not pin-checked: ui.mjs is the pass that enforces the pin)");
  fs.mkdirSync(out, { recursive: true });
  const browser = await chromium.launch();
  const step = (s) => console.log("• " + s), fail = (m) => { console.error("FAIL: " + m); process.exitCode = 1; }, sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  try { await marketPass({ browser, url, out, step, fail, sleep, mainnet: process.argv.includes("--mainnet") }); } catch (e) { fail(e.stack || String(e)); }
  await browser.close();
  console.log(process.exitCode ? "UI-MARKET FAILED" : "UI-MARKET OK");
}
