// Headless browser test of the Veil app against a live simulator.
//   node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html <outdir> [--wallet] [--follow=<address>]
// PW_DIR must point at a directory with playwright installed (node_modules/playwright), i.e.
// (any directory where `npm i playwright` and `npx playwright install chromium` were run).
// Read-only pass: every tab renders without console errors. --wallet: connect over XSWD (an auto-approving wallet
// must listen on 44326), launch a token from the form, buy with "Post this trade" on, and verify the feed shows it.
import { createRequire } from "node:module";
import fs from "node:fs";
const require = createRequire((process.env.PW_DIR || ".") + "/package.json");
const { chromium } = require("playwright");

const url = process.argv[2], out = process.argv[3] || ".";
const withWallet = process.argv.includes("--wallet");
// --dest=<address>: after the ordinary buy, buy again in private mode (ring 16, paid to that address) and check
// the node shows a 16-member ring on the transaction
const destArg = (process.argv.find((a) => a.startsWith("--dest=")) || "").slice(7);
const passOnly = process.argv.includes("--pass-only"); // wallet flow: connect + Founders Pass steps only
const portfolioOnly = process.argv.includes("--portfolio-only"); // wallet flow: connect + portfolio render only (no spend)
const followArg = (process.argv.find((a) => a.startsWith("--follow=")) || "").slice(9);
fs.mkdirSync(out, { recursive: true });
const browser = await chromium.launch();
// The app ships a strict Content Security Policy (script-src 'self'). It is checked first in a normal page: an
// injected inline script must NOT run. The functional pass then uses bypassCSP so Playwright's own injected
// helpers (waitForFunction polls through eval) can work.
{
  const probe = await browser.newPage({ viewport: { width: 430, height: 860 } });
  const refused = [];
  probe.on("console", (m) => { if (/Content Security Policy/.test(m.text())) refused.push(m.text()); });
  await probe.goto(url, { waitUntil: "load" });
  await probe.addScriptTag({ content: "window.__cspProbe = 1" }).catch(() => {});
  await new Promise((r) => setTimeout(r, 500));
  const ran = await probe.evaluate(() => window.__cspProbe === 1);
  const meta = await probe.evaluate(() => (document.querySelector('meta[http-equiv="Content-Security-Policy"]') || {}).content || "");
  console.log("• CSP: " + (ran ? "INLINE SCRIPT RAN (policy missing)" : "inline script blocked") + " · " + refused.length + " violation report(s) · " + meta.slice(0, 60) + "…");
  if (ran || !/script-src 'self'/.test(meta)) { console.error("FAIL: Content Security Policy not enforced"); process.exitCode = 1; }
  await probe.close();
}
const page = await browser.newPage({ viewport: { width: 430, height: 860 }, bypassCSP: true });
const errors = [], logs = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => { logs.push(m.type() + ": " + m.text()); if (m.type() === "error") errors.push("console: " + m.text()); });
const shot = (n) => page.screenshot({ path: `${out}/${n}.png`, fullPage: true });
const step = (s) => console.log("• " + s);
const fail = (m) => { console.error("FAIL: " + m); console.error(errors.join("\n")); process.exitCode = 1; };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const text = async () => (await page.locator("#screen").innerText()).replace(/\s+/g, " ");

try {
  step("open " + url);
  await page.goto(url, { waitUntil: "load" });
  await page.locator("#introOk").click({ timeout: 8000 }).catch(() => {}); // first-run intro
  await page.evaluate(() => new MutationObserver((ms) => ms.forEach((m) => m.addedNodes.forEach((n) => console.log("TOAST " + n.textContent)))).observe(document.getElementById("toasts"), { childList: true }));
  await sleep(4000);
  await shot("01-home");
  const net = await page.locator("#net").innerText();
  step("node pill: " + net);
  if (!/\d/.test(net)) fail("node not connected: " + net);

  // Eight destinations do not fit a phone bar, so Profile is reached from Portfolio and the wallet button
  // instead. Click the tab when it is in the bar at this width; otherwise take the route directly.
  for (const tab of ["markets", "trade", "wrap", "nfts", "portfolio", "profile"]) {
    const inBar = await page.evaluate((t) => { const e = document.querySelector(`.tab[data-tab=${t}]`); return !!e && getComputedStyle(e).display !== "none"; }, tab);
    if (inBar) await page.click(`.tab[data-tab=${tab}]`);
    else await page.evaluate((t) => { location.hash = "#" + t; }, tab);
    await sleep(900); await shot("02-" + tab);
    step(tab + (inBar ? ": " : " (not in the phone bar): ") + (await text()).slice(0, 90));
  }
  // a holder-NFT collection page when the simulator has one
  const nftId = await page.evaluate(() => { const n = [...window.Veil.data.nfts.keys()]; return n.length ? n[0] : null; });
  if (nftId) { await page.evaluate((s) => { location.hash = "#nft/" + s; }, nftId); await sleep(1500); await shot("02b-nft"); const nt = await text(); if (!/Public accounting/.test(nt)) fail("nft page missing accounting card: " + nt.slice(0, 160)); step("nft page: " + nt.slice(0, 80)); }
  await page.evaluate(() => { location.hash = "#settings"; }); await sleep(600); await shot("03-settings");
  // the wrapped-asset screen: it must publish the two numbers a user cannot discover any other way — the minimum
  // deposit and the height a withdrawal becomes payable — or it is a trap rather than a screen
  {
    await page.evaluate(() => { location.hash = "#wrap"; }); await sleep(1500); await shot("03b-wrap");
    const wt = await text();
    const configured = await page.evaluate(() => !!(window.Veil.data.cfg.vault));
    if (configured) {
      if (!/minimum deposit/i.test(wt)) fail("wrap screen does not publish the minimum deposit: " + wt.slice(0, 200));
      if (!/what you are trusting/i.test(wt)) fail("wrap screen has no trust panel: " + wt.slice(0, 200));
      if (/\bSAFE\b/.test(wt)) fail("wrap screen shows a SAFE badge; the trust panel must be factual");
    } else if (!/no vault on this network/i.test(wt)) {
      fail("wrap screen with no configured vault should say so: " + wt.slice(0, 200));
    }
    step("wrap: " + wt.replace(/\s+/g, " ").slice(0, 110));
  }
  // chat: an IRC client whose server is a contract. It must say the three things that are irreversible.
  {
    await page.evaluate(() => { location.hash = "#chat"; }); await sleep(2000); await shot("03c-chat");
    const ct = await text();
    const configured = await page.evaluate(() => !!(window.Veil.data.cfg.chat));
    if (configured) {
      if (!/ring-size-2/i.test(ct)) fail("chat does not say posting is a ring-size-2 call: " + ct.slice(0, 200));
      if (!/permanent/i.test(ct)) fail("chat does not say a message is permanent: " + ct.slice(0, 200));
      const rooms = await page.evaluate(() => document.querySelectorAll(".roomrow").length);
      if (rooms < 2) fail("chat listed " + rooms + " rooms");
      step("chat: " + rooms + " rooms · " + ct.replace(/\s+/g, " ").slice(0, 90));
    } else if (!/no chat on this network/i.test(ct)) {
      fail("chat with no configured contract should say so: " + ct.slice(0, 160));
    }
  }
  // the message from the dev outranks every other piece of copy, so it must be present and reachable
  {
    await page.evaluate(() => { location.hash = "#notice"; }); await sleep(900); await shot("03d-notice");
    const nt = await text();
    for (const want of [/no support/i, /code is law/i, /space cowboy/i, /revoked/i]) {
      if (!want.test(nt)) fail("the dev message is missing " + want + ": " + nt.slice(0, 200));
    }
    step("notice: " + nt.replace(/\s+/g, " ").slice(0, 90));
  }
  // deposit and withdraw must be reachable from the navigation, not only by typing a URL
  {
    const tabs = await page.evaluate(() => [...document.querySelectorAll(".tab")].map((t) => t.dataset.tab));
    for (const want of ["wrap", "chat"]) if (!tabs.includes(want)) fail("no " + want + " tab: " + tabs.join(","));
    const vis = await page.evaluate(() => [...document.querySelectorAll(".tab")].filter((t) => getComputedStyle(t).display !== "none").length);
    step("tabs: " + tabs.join(" ") + " (" + vis + " visible at this width)");
    if (vis > 7) fail(vis + " tabs visible on a phone");
  }
  // Screen handlers subscribe to D.onChange on every dispatch. Navigating must not accumulate them, or every
  // block redraws the screen once per past visit.
  {
    for (const t of ["home", "markets", "home", "nfts", "home"]) { await page.evaluate((x) => { location.hash = "#" + x; }, t); await sleep(250); }
    // stale subscriptions are dropped on the next data tick, which is what changed() is; force one rather
    // than waiting ~18s for a block
    const n = await page.evaluate(() => { window.Veil.data.changed(); return window.Veil.data.listeners.length; });
    step("onChange listeners after five navigations: " + n);
    if (n > 4) fail("D.onChange listeners are leaking: " + n + " after five navigations");
  }
  // a redraw of the busiest screen must stay cheap: this is the number to watch when the list grows
  {
    await page.evaluate(() => { location.hash = "#markets"; }); await sleep(1200);
    const ms = await page.evaluate(() => {
      const D = window.Veil.data, S = window.Veil.stats, t0 = performance.now();
      for (let i = 0; i < 10; i++) { for (const x of D.list()) S.enrich(x, D.height); D.changed(); }
      return Math.round((performance.now() - t0) / 10 * 100) / 100;
    });
    step("markets: enrich+redraw of " + (await page.evaluate(() => window.Veil.data.list().length)) + " markets: " + ms + " ms");
    if (ms > 120) fail("a markets redraw costs " + ms + " ms; it must stay well under one block");
  }
  // token page + trade sheet of the busiest market (read-only)
  const top = await page.evaluate(() => { const t = window.Veil.data.trending(1); return t.length ? t[0].scid : null; });
  if (top) {
    await page.evaluate((s) => { location.hash = "#token/" + s; }, top); await sleep(1500); await shot("04-token");
    step("token: " + (await text()).slice(0, 120));
    await page.getByRole("button", { name: "Buy", exact: true }).first().click(); await sleep(400);
    await page.locator("#sheet input").first().fill("0.5"); await sleep(300); await shot("05-trade-sheet");
    // market switcher inside the sheet: opens a picker, picking re-opens the sheet on that market, same side
    await page.getByRole("button", { name: "switch market" }).click(); await sleep(400);
    const pickN = await page.locator("#sheet .list button").count(); step("switcher lists " + pickN + " markets");
    if (!pickN) fail("market switcher listed nothing");
    await page.locator("#sheet .list button").nth(Math.min(1, pickN - 1)).click(); await sleep(500);
    const title = await page.locator("#sheet h2").first().innerText(); if (!/^Buy /.test(title)) fail("switcher did not re-open a buy sheet: " + title);
    await shot("05b-trade-switched");
    await page.keyboard.press("Escape"); await sleep(300);
    await page.getByText("Line", { exact: true }).click(); await sleep(400); await shot("06-token-line");
    await page.evaluate(() => { location.hash = "#markets"; }); await sleep(1200); await shot("07-markets-live");
    // launch sheet with its live preview (read-only: filled, never sent)
    await page.click(".tab[data-tab=trade]"); await sleep(600);
    await page.getByText("Launch a token").first().click(); await sleep(500);
    await page.locator("#sheet input[name=name]").fill("Preview Coin"); await page.locator("#sheet input[name=ticker]").fill("PRV"); await sleep(400); await shot("08-launch-sheet");
    const pv = await page.locator("#sheet").innerText(); if (!/16×/.test(pv) || !/Two wallet confirmations/.test(pv)) fail("launch preview missing: " + pv.slice(0, 200));
    await page.keyboard.press("Escape"); await sleep(300);
  }

  // desktop: the same app in a sidebar shell, chosen by viewport (not user agent); must render without errors
  {
    const d = await browser.newPage({ viewport: { width: 1280, height: 860 }, bypassCSP: true });
    const derr = [];
    d.on("pageerror", (e) => derr.push(e.message)); d.on("console", (m) => { if (m.type() === "error") derr.push(m.text()); });
    await d.goto(url, { waitUntil: "load" }); await d.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
    await d.waitForFunction(() => window.Veil && window.Veil.data.lastRefresh > 0, null, { timeout: 60000 });
    for (const tab of ["home", "markets", "nfts"]) { await d.evaluate((t) => { location.hash = "#" + t; }, tab); await sleep(1500); await d.screenshot({ path: `${out}/desk-${tab}.png` }); }
    const top2 = await d.evaluate(() => { const t = window.Veil.data.trending(1); return t.length ? t[0].scid : null; });
    if (top2) { await d.evaluate((s) => { location.hash = "#token/" + s; }, top2); await sleep(1800); await d.screenshot({ path: `${out}/desk-token.png` }); }
    const side = await d.evaluate(() => { const t = document.querySelector(".tabs"); return getComputedStyle(t).flexDirection === "column" && t.getBoundingClientRect().left < 300 && t.getBoundingClientRect().height > 500; });
    step("desktop 1280px: sidebar shell " + (side ? "on" : "MISSING"));
    if (!side) fail("desktop layout not applied at 1280px");
    // 1024 is inside the band where the fixed side rails used to leave the main column narrower than a phone
    {
      const n = await browser.newPage({ viewport: { width: 1024, height: 860 }, bypassCSP: true });
      await n.goto(url, { waitUntil: "load" }); await n.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
      await n.waitForFunction(() => window.Veil && window.Veil.data.lastRefresh > 0, null, { timeout: 60000 });
      const t1024 = await n.evaluate(() => { const t = window.Veil.data.trending(1); return t.length ? t[0].scid : null; });
      if (t1024) {
        await n.evaluate((s2) => { location.hash = "#token/" + s2; }, t1024); await sleep(1800);
        await n.screenshot({ path: `${out}/desk1024-token.png` });
        const w = await n.evaluate(() => { const m = document.querySelector(".tk-main"); return m ? Math.round(m.getBoundingClientRect().width) : -1; });
        step("1024px token main column: " + w + "px");
        if (w >= 0 && w < 480) fail("main column is " + w + "px at 1024: the side rail is starving it");
      }
      // nothing may stick out sideways at this width
      const over = await n.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      step("1024px horizontal overflow: " + over + "px");
      if (over > 1) fail("page overflows horizontally by " + over + "px at 1024");
      await n.close();
    }
    const bad2 = derr.filter((e) => !/favicon|net::ERR_/.test(e));
    if (bad2.length) fail("desktop console errors:\n" + bad2.join("\n"));
    await d.close();
  }

  if (withWallet) {
    step("connect wallet over XSWD");
    await page.evaluate(() => { location.hash = "#home"; }); await sleep(500);
    await page.click("#walletBtn");
    await page.waitForFunction(() => window.Veil.wallet.connected && window.Veil.wallet.address, null, { timeout: 60000 });
    const addr = await page.evaluate(() => window.Veil.wallet.address);
    step("connected " + addr);
    await sleep(1500); await shot("10-connected");

    if (portfolioOnly) {
      step("portfolio only");
      await page.click(".tab[data-tab=portfolio]");
      await page.waitForFunction(() => window.Veil.app.holdings !== null, null, { timeout: 180000 }); // balances decrypt through the wallet
      await sleep(2500); await shot("16-portfolio");
      const pf = await text(); if (!/Holdings value/.test(pf) || !/Cost basis from/.test(pf)) fail("portfolio incomplete: " + pf.slice(0, 200));
      const curve = await page.locator(".chart svg.cx path").count(); step("portfolio: " + pf.slice(0, 90) + " · chart paths " + curve);
    }
    if (!passOnly && !portfolioOnly) {
    step("launch a token from the form");
    await page.click(".tab[data-tab=trade]"); await sleep(600);
    await page.getByText("Launch a token").click(); await sleep(500);
    await page.locator("#sheet input[name=name]").fill("Browser Coin");
    await page.locator("#sheet input[name=ticker]").fill("BRW");
    await page.locator("#sheet textarea[name=descr]").fill("launched from the headless browser test");
    await page.locator("#sheet details summary").click(); // Advanced: supply, decimals, tax
    await page.locator("#sheet input[name=supply]").fill("1000000"); // 1e6 tokens × 10^3 = 1e9 units: wallet-friendly
    await page.locator("#sheet input[name=dec]").fill("3");
    await page.locator("#sheet input[name=grad]").fill("10"); // graduation DERO (min)
    await page.locator("#sheet input[name=tax]").fill("2");   // tax %
    await page.locator("#sheet .seg button").nth(3).click(); // stakers
    await shot("11-launch-form");
    await page.locator("#sheet button.primary").click();
    // two transactions: install (~2 blocks) then register
    await page.waitForFunction(() => location.hash.startsWith("#token/"), null, { timeout: 300000 });
    const scid = location_hash(await page.evaluate(() => location.hash));
    step("launched " + scid);
    await sleep(2500); await shot("12-token");
    const t = await text(); if (!t.includes("BRW")) fail("token page missing ticker: " + t.slice(0, 200));

    step("buy 0.5 DERO with publish on");
    await page.getByRole("button", { name: "Buy", exact: true }).first().click(); await sleep(500);
    await page.locator("#sheet input").first().fill("0.5");
    await sleep(300);
    await page.locator("#publishSw").click(); // Post this trade
    if (await page.locator("#ackSw").count()) { step("large price impact on a fresh curve: acknowledging"); await page.locator("#ackSw").click(); }
    await shot("13-trade-sheet");
    const quote = await page.locator("#sheet .outline").first().innerText();
    step("quote: " + quote.replace(/\s+/g, " "));
    await page.locator("#sheet button.buy").click();
    await page.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => /Buy BRW (confirmed|included in block)/.test(t.textContent)), null, { timeout: 240000 });
    await sleep(3000); await shot("14-after-buy");
    await page.waitForFunction((s) => { const a = window.Veil.data.assets.get(s); return a && a.trades.length > 0; }, scid, { timeout: 180000 });
    const trade = await page.evaluate((s) => window.Veil.data.assets.get(s).trades[0], scid);
    step("published trade on chain: " + JSON.stringify(trade, (k, v) => (typeof v === "bigint" ? v.toString() : v)));
    const mine = await page.evaluate(() => window.Veil.wallet.canon);
    if (trade.addr !== mine) fail("trade address " + trade.addr + " != " + mine);

    step("feed shows it");
    await page.click(".tab[data-tab=home]"); await sleep(1500); await shot("15-feed");
    const home = await text(); if (!/bought/i.test(home) || !home.includes("BRW")) fail("feed missing the trade: " + home.slice(0, 300));

    if (destArg) {
      step("private buy: ring 16, paid to " + destArg.slice(0, 14) + "…");
      await page.evaluate((s) => { location.hash = "#token/" + s; }, scid); await sleep(1500);
      await page.getByRole("button", { name: "Buy", exact: true }).first().click(); await sleep(500);
      await page.locator("#sheet input").first().fill("0.3"); await sleep(300);
      await page.getByText("Private mode: hide who paid").click(); await sleep(300);
      await page.locator("#privSw").click(); await sleep(200);
      await page.locator('#sheet input[aria-label="receive at"]').fill(destArg); await sleep(500);
      if (await page.locator("#ackSw").count()) await page.locator("#ackSw").click();
      await shot("19-private-sheet");
      const det = await page.locator("#sheet").evaluate((el) => el.textContent); // the details section is collapsed: textContent, not innerText
      if (!/16 \(the payer/.test(det)) fail("ring size 16 not shown in the sheet: " + det.replace(/\s+/g, " ").slice(0, 300));
      const tcBefore = await page.evaluate(async (s) => { const r = await window.Veil.node.getSC(s, { keys: ["tc"] }); return Number(r.vals.tc || 0); }, scid);
      await page.locator("#sheet button.buy").click();
      await page.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => /included in block|confirmed/.test(t.textContent)), null, { timeout: 240000 });
      await sleep(2000); await shot("20-after-private-buy");
      const info = await page.evaluate(async () => { const t = await window.Veil.node.getTx(window.Veil.app.lastTxid); return t ? { ring: t.ring ? t.ring.map((r) => r.length) : null, signer: t.signer || "" } : null; });
      step("private buy on the node: ring members per payload " + JSON.stringify(info && info.ring) + ", extracted signer " + JSON.stringify(info && info.signer));
      if (!info || !info.ring || !info.ring.some((n) => n === 16)) fail("expected a 16-member ring, got " + JSON.stringify(info));
      if (info && info.signer) fail("the node extracted a signer from the private buy: " + info.signer);
      // a private trade never publishes a record (it would name the receiver as the trader): tc must not move
      const tcAfter = await page.evaluate(async (s) => { const r = await window.Veil.node.getSC(s, { keys: ["tc"] }); return Number(r.vals.tc || 0); }, scid);
      step("published records before/after the private buy: " + tcBefore + " → " + tcAfter);
      if (tcAfter !== tcBefore) fail("the private buy wrote a public trade record");
    }

    step("portfolio + profile");
    await page.click(".tab[data-tab=portfolio]"); await sleep(4000); await shot("16-portfolio");
    const pf = await text(); if (!pf.includes("BRW")) fail("portfolio missing holding: " + pf.slice(0, 300));
    await page.click(".tab[data-tab=profile]"); await sleep(1500); await shot("17-profile");
    await page.getByRole("button", { name: "Edit" }).click(); await sleep(400);
    await page.locator("#sheet input").first().fill("headless tester");
    await page.locator("#sheet button.primary").click();
    // a call without a balance probe is reported as "included in block N", never as "confirmed" (C1)
    await page.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => /confirmed|included in block/.test(t.textContent)), null, { timeout: 240000 });
    await sleep(2500); await shot("18-profile-saved");
    if (!(await text()).includes("headless tester")) fail("profile name not shown");

    if (followArg) {
      step("follow " + followArg);
      await page.evaluate((a) => { location.hash = "#profile/" + a; }, followArg); await sleep(1500); // hash navigation keeps the wallet socket
      if (await page.getByRole("button", { name: "Following", exact: true }).count()) { // left over from an earlier run: unfollow first
        await page.getByRole("button", { name: "Following", exact: true }).click();
        await page.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => /Unfollow (confirmed|included in block)/.test(t.textContent)), null, { timeout: 240000 });
        await sleep(2000);
        if ((await text()).includes("Following")) fail("unfollow state not shown");
        step("unfollowed");
      }
      await page.getByRole("button", { name: "Follow", exact: true }).first().click();
      await page.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => /Follow (confirmed|included in block)/.test(t.textContent)), null, { timeout: 240000 });
      await sleep(2000); await shot("19-followed");
      if (!(await text()).includes("Following")) fail("follow state not shown");
    }

    step("stake from the token page");
    await page.evaluate((s) => { location.hash = "#token/" + s; }, scid); await sleep(1500);
    await page.getByText("Info").click(); await sleep(400);
    await page.getByText("Stake / rewards").click(); await sleep(400);
    await page.locator("#sheet input").first().fill("1000");
    await page.locator("#sheet button.primary").click();
    await page.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => /Stake (confirmed|included in block)/.test(t.textContent)), null, { timeout: 240000 });
    await sleep(2500); await shot("20-staked");
    const st = await page.evaluate((s) => String(window.Veil.data.assets.get(s).stot), scid);
    step("total staked on chain: " + st);
    if (st === "0") fail("stake not reflected");
    }

    if (!portfolioOnly) {
    step("founders pass: mint 1, register it");
    await page.evaluate(() => { location.hash = "#nfts"; }); await sleep(2500); await shot("21-nfts");
    await page.getByRole("button", { name: /^Mint 1 for/ }).click({ timeout: 60000 });
    await page.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => /Mint 1 pass (confirmed|included in block)/.test(t.textContent)), null, { timeout: 240000 });
    await page.waitForFunction(() => window.Veil.nfts.mine && window.Veil.nfts.mine.wallet > 0n, null, { timeout: 90000 });
    await sleep(800);
    await page.getByRole("button", { name: /^Register \d+/ }).click();
    await page.waitForFunction(() => [...document.querySelectorAll(".toast")].some((t) => /Register passes (confirmed|included in block)/.test(t.textContent)), null, { timeout: 240000 });
    await sleep(3000); await shot("22-nfts-registered");
    const reg = await page.evaluate(async () => { const m = await window.Veil.data.myFounders(window.Veil.wallet.canon); return m ? String(m.units) : "0"; });
    step("registered pass units on chain: " + reg);
    if (reg === "0") fail("pass not registered");
    }
  }

  const bad = errors.filter((e) => !/favicon|net::ERR_/.test(e));
  if (bad.length) fail("console errors:\n" + bad.join("\n"));
  fs.writeFileSync(out + "/console.log", logs.join("\n"));
  console.log(process.exitCode ? "UI TEST FAILED" : "UI TEST OK");
} catch (e) {
  fs.writeFileSync(out + "/console.log", logs.join("\n")); // the page console (toasts included) is the evidence for a failed step
  fail(e.stack || String(e));
  await shot("99-error").catch(() => {});
} finally {
  await browser.close();
}
function location_hash(h) { return h.replace(/^#token\//, ""); }
