// tools/uitest/sentiment.mjs: the community-sentiment widget (app/sentiment.js) on a running simulator, read at phone
// (430) and desktop (1280) width. It opens a market page and puts the widget in the page's slot the way the market
// page will (V.sentiment.widget(scid) appended to the screen), with the market's real counts; then draws the same
// widget from a fixture of 17 votes, the state with percentages. Nothing is signed: votes go through the wallet, and
// this pass has none.
//
//   PW_DIR=<playwright> node tools/uitest/sentiment.mjs http://127.0.0.1:8107/index.html <outdir> [--market=<scid>]
//   node tools/uitest/ui.mjs <url> <outdir> --sentiment        # the same pass, after the ordinary one
//
// Fails when: the build's rate contract does not verify; a heading, the many-addresses sentence or the not-a-verdict
// line is missing; a count under 5 votes shows a percentage; 5 or more votes show none; or the widget paints anything
// in the app's warning or loss colours.
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import fs from "node:fs";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function sentimentPass({ browser, url, out, step, fail }) {
  const market = (process.argv.find((a) => a.startsWith("--market=")) || "").slice(9);
  for (const [w, hgt] of [[430, 900], [1280, 900]]) {
    const p = await browser.newPage({ viewport: { width: w, height: hgt } });
    await p.goto(url, { waitUntil: "load" });
    await p.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
    await sleep(3000);
    // the market: the one named, or the first listed launch the app verified
    const scid = market || (await p.evaluate(() => { const a = [...window.Veil.data.assets.values()].find((x) => x.kind === "launch") || [...window.Veil.data.assets.values()][0]; return a ? a.scid : null; }));
    if (!scid) { fail("sentiment: no market to open"); await p.close(); continue; }
    await p.evaluate((s) => { location.hash = "#token/" + s; }, scid);
    await sleep(2500);
    await p.evaluate((s) => { const el = window.Veil.sentiment.widget(s); el.id = "sentiLive"; document.querySelector("#screen").append(el); }, scid);
    await p.locator("#sentiLive .senti-btns").waitFor({ timeout: 20000 }).catch(() => {});
    const st = await p.evaluate(() => ({ rate: window.VEIL_CONFIG.rate, verified: window.Veil.sentiment.verified }));
    if (!st.rate || st.verified !== st.rate) fail(`sentiment: the build's rate contract ${st.rate} did not verify (verified: "${st.verified}")`);
    const live = await p.evaluate((s) => window.Veil.sentiment.of(s), scid);
    const lt = (await p.locator("#sentiLive").innerText()).replace(/\s+/g, " ");
    check(lt, live, fail, "live " + w);
    await p.locator("#sentiLive").scrollIntoViewIfNeeded();
    await p.locator("#sentiLive").screenshot({ path: `${out}/senti-${w}-live.png` });
    await p.screenshot({ path: `${out}/senti-${w}-page.png`, fullPage: true });
    step(`sentiment ${w}: market ${scid.slice(0, 12)}… ${JSON.stringify(live)}: ${lt.slice(0, 160)}`);
    // the percentage state, from a fixture: the widget reads its counts through of(), so of() answers once
    const fixture = { bull: 14, sus: 3, n: 17 };
    await p.evaluate((f) => { const S = window.Veil.sentiment, of = S.of; S.of = async () => f; const el = S.widget("f".repeat(64)); S.of = of; el.id = "sentiFix"; document.querySelector("#screen").append(el); }, fixture);
    await p.locator("#sentiFix .senti-bar").waitFor({ timeout: 8000 }).catch(() => {});
    const ft = (await p.locator("#sentiFix").innerText()).replace(/\s+/g, " ");
    check(ft, fixture, fail, "fixture " + w);
    if (!/82%/.test(ft) || !/18%/.test(ft)) fail(`sentiment fixture ${w}: 14 of 17 should read 82% and 18%: ${ft}`);
    await p.locator("#sentiFix").screenshot({ path: `${out}/senti-${w}-fixture.png` });
    step(`sentiment ${w} fixture: ${ft.slice(0, 160)}`);
    // no warning or loss colour anywhere in either widget
    const red = await p.evaluate(() => {
      const cs = getComputedStyle(document.documentElement), bad = ["--down", "--warn"].map((v) => cs.getPropertyValue(v).trim().toLowerCase());
      const probe = document.createElement("i"); document.body.append(probe);
      const rgb = bad.map((c) => { probe.style.color = c; return getComputedStyle(probe).color; }); probe.remove();
      const hits = [];
      for (const el of document.querySelectorAll(".senti, .senti *")) {
        const s = getComputedStyle(el);
        for (const v of [s.color, s.backgroundColor, s.borderTopColor]) if (rgb.includes(v)) hits.push(el.className + " " + v);
      }
      return hits;
    });
    if (red.length) fail(`sentiment ${w}: warning or loss colour in the widget: ${red.join("; ")}`);
    await p.close();
  }
}

function check(text, d, fail, what) {
  for (const want of ["Community sentiment", "not DMZ's verdict", "Anyone can vote, including from many addresses", "Bullish", "Suspicious / Scam", "Clear", "your address is public"]) {
    // innerText follows CSS text-transform, and h3 is set in capitals: compare without case
    if (!text.toLowerCase().includes(want.toLowerCase())) fail(`sentiment ${what}: missing "${want}": ${text}`);
  }
  if (!d) return fail(`sentiment ${what}: no counts read`);
  const pct = /\d+%|<1%|>99%/.test(text.replace(/one address would otherwise read as 100%/, ""));
  if (d.n < 5 && pct) fail(`sentiment ${what}: ${d.n} votes but a percentage is shown: ${text}`);
  if (d.n >= 5 && !pct) fail(`sentiment ${what}: ${d.n} votes but no percentage: ${text}`);
  if (d.n < 5 && !text.includes("Percentages are shown from 5 votes")) fail(`sentiment ${what}: under 5 votes the rule is not stated: ${text}`);
}

// standalone: node tools/uitest/sentiment.mjs <url> <outdir>
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const require = createRequire((process.env.PW_DIR || ".") + "/package.json");
  const { chromium } = require("playwright");
  const url = process.argv[2], out = process.argv[3] || ".";
  fs.mkdirSync(out, { recursive: true });
  const browser = await chromium.launch();
  console.log("• playwright " + require("playwright/package.json").version + ", chromium " + browser.version());
  const step = (s) => console.log("• " + s), fail = (m) => { console.error("FAIL: " + m); process.exitCode = 1; };
  try { await sentimentPass({ browser, url, out, step, fail }); } catch (e) { fail(e.stack || String(e)); } finally { await browser.close(); }
  console.log(process.exitCode ? "SENTIMENT UI FAILED" : "SENTIMENT UI OK");
}
