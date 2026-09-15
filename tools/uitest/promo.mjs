// The Promote screen (app/promo.js), READ at desktop and phone width against a live simulator: the current winners, the
// next epoch's standings, the highest and minimum next bid, the time left in blocks and estimated time, "Your bids",
// and the rules with where the money goes. It signs nothing. Screenshots: <outdir>/promote-<width>.png.
//   PW_DIR=<playwright> node tools/uitest/promo.mjs http://127.0.0.1:8106/index.html <outdir> [--as=<address>] [--any-playwright]
// --as names the address whose bids "Your bids" lists (the page reads bids by address; no wallet is connected).
// ui.mjs's ordinary pass runs the same checks with --promo, through its one-line hook.
import { createRequire } from "node:module";
import fs from "node:fs";
import { pathToFileURL } from "node:url";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function run({ browser, url, out, step, fail, as }) {
  for (const width of [1280, 430]) {
    const p = await browser.newPage({ viewport: { width, height: 900 }, bypassCSP: true });
    const errs = [];
    p.on("pageerror", (e) => errs.push("pageerror: " + e.message));
    p.on("console", (m) => { if (m.type() === "error") errs.push("console: " + m.text()); });
    await p.goto(url, { waitUntil: "load" });
    await p.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
    if (as) await p.evaluate((a) => { window.Veil.wallet.canon = window.Veil.canonAddress(a); }, as);
    await p.waitForFunction(() => window.Veil.data.lastRefresh > 0, null, { timeout: 90000 }).catch(() => {});
    await p.evaluate(() => { location.hash = "#promote"; });
    await p.waitForFunction(() => window.Veil.promo && window.Veil.promo.state, null, { timeout: 60000 }).catch(() => {});
    await sleep(2500);
    await p.screenshot({ path: `${out}/promote-${width}.png`, fullPage: true });
    const tt = (await p.locator("#screen").innerText()).replace(/\s+/g, " ");
    step("promote " + width + "px: " + tt.slice(0, 160));
    // labels are set in capitals by the stylesheet, and innerText returns them so: the checks ignore case
    for (const want of [/Promoted now · epoch \d+/i, /Standing for epoch \d+/i, /Highest bid (none yet|[\d.,]+ (DERO|w[A-Z]{3}))/i, /Minimum next bid [\d.,]+ (DERO|w[A-Z]{3})/i, /Time left \d+ blocks about (\d+[dhm] ?)+ at 18 s a block/i,
      /Your bids/i, /The rules, and where the money goes/i, /Paid to the treasury/i, /Claimable now/i, /Bids in (DERO|w[A-Z]{3} \([A-Z]{3}, wrapped 1:1\))/, /cannot be ranked against each other without a price oracle/, /ClaimProceeds, which pays them to the treasury and to nobody else/,
      /no contract forces it/, /labelled Promoted: paid for, not endorsed/]) if (!want.test(tt)) fail("the Promote screen at " + width + "px does not show " + want + ": " + tt.slice(0, 400));
    if (/<0\.0/.test(tt)) fail("an amount on the Promote screen is rounded away (\"<0.0…\")");
    for (const bad of [/\btrustless\b/i, /endorsed by/i, /Verified project/i]) if (bad.test(tt)) fail("the Promote screen says " + bad);
    // the one mark a paid placement wears is the word Promoted, and never on an unlisted target
    const tags = await p.evaluate(() => [...document.querySelectorAll("#screen .promo-tag")].map((t) => t.textContent));
    if (tags.some((t) => t !== "Promoted")) fail("a promo tag reads other than Promoted: " + JSON.stringify(tags));
    const api = await p.evaluate(() => {
      const P = window.Veil.promo, s = P.slots();
      return { slots: s.map((x) => ({ slot: x.slot, target: x.target, bid: String(x.bid), epoch: String(x.epoch) })), status: s.length ? P.statusOf(s[0].target) : null,
        unlisted: P.state.slots.filter((x) => !P.eligible(x.target).ok).map((x) => x.target), bogus: P.statusOf("7a".repeat(32)) };
    });
    step("V.promo.slots() " + JSON.stringify(api.slots) + " · statusOf(top) " + JSON.stringify(api.status, (k, v) => (typeof v === "bigint" ? String(v) : v)));
    if (api.slots.some((x) => api.unlisted.includes(x.target))) fail("V.promo.slots() offers a target that is not a listed, verified market or NFA");
    if (api.bogus && (api.bogus.promoted || api.bogus.eligible)) fail("statusOf an unlisted id says it is promoted or eligible");
    if (as && !/Your bids .*(standing for epoch|on display now|finished)/i.test(tt)) fail("Your bids lists no bid for " + as);
    const over = await p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (over > 1) fail("the Promote screen overflows horizontally by " + over + "px at " + width);
    const bad = errs.filter((e) => !/favicon|net::ERR_/.test(e));
    if (bad.length) fail("console errors on the Promote screen at " + width + "px:\n" + bad.join("\n"));
    await p.close();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const url = process.argv[2], out = process.argv[3] || ".", as = (process.argv.find((a) => a.startsWith("--as=")) || "").slice(5);
  const require = createRequire((process.env.PW_DIR || ".") + "/package.json");
  const { chromium } = require("playwright"), v = require("playwright/package.json").version;
  if (v !== "1.62.0" && !process.argv.includes("--any-playwright") && process.env.PW_ANY !== "1") { console.error("FAIL: playwright " + v + " is not the pinned 1.62.0 (tools/uitest/README.md); pass --any-playwright to run anyway"); process.exit(2); }
  fs.mkdirSync(out, { recursive: true });
  const browser = await chromium.launch();
  console.log("• playwright " + v + " · chromium " + browser.version());
  let failed = false;
  try { await run({ browser, url, out, as, step: (s) => console.log("• " + s), fail: (m) => { failed = true; console.error("FAIL: " + m); } }); }
  catch (e) { failed = true; console.error("FAIL: " + (e.stack || e)); }
  await browser.close();
  console.log(failed ? "PROMO UI TEST FAILED" : "PROMO UI TEST OK");
  process.exit(failed ? 1 : 0);
}
