// Headless browser test of the Veil app against a live simulator.
//   node tools/uitest/ui.mjs http://127.0.0.1:8082/index.html <outdir> [--wallet] [--follow=<address>] [--mainnet] [--perf [--runs=5]]
// --perf: app start and first render, cold and warm, measured against the node the build names (perfPass, at the
// foot of this file); writes perf.json and perf-<width>-<cold|warm>.png. Both passes also run
// tools/uitest/ui-swaps.mjs: Deposit & withdraw (app/move.js) and the Wrap screen's limits, at 390 and 1280 px.
// --mainnet: the same app under a mainnet-shaped config (network "mainnet", no vault), served by rewriting the
// config.js response inside the browser; writes mainnet-<width>-<screen>.png and checks the no-vault copy.
// PW_DIR must point at a directory with playwright installed (node_modules/playwright), i.e.
// (any directory where `npm i playwright` and `npx playwright install chromium` were run).
// Read-only pass: every tab renders without console errors. --wallet: connect over XSWD (an auto-approving wallet
// must listen on 44326), launch a token from the form, buy with "Post this trade" on, and verify the feed shows it.
// The pass runs against one pinned Playwright (MT-H03, tools/uitest/README.md): it prints the Playwright version and the
// browser build it drives, and refuses any other Playwright unless --any-playwright (or PW_ANY=1) is given.
import { createRequire } from "node:module";
import fs from "node:fs";
import crypto from "node:crypto";
const PLAYWRIGHT_PIN = "1.62.0"; // drives Chromium 151.0.7922.34 (Playwright browser revision 1234)
const CHROMIUM_PIN = "151.0.7922.34";
const pwDir = process.env.PW_DIR || ".";
const require = createRequire(pwDir + "/package.json");
const { chromium } = require("playwright");
const pwVersion = require("playwright/package.json").version;
const anyPlaywright = process.argv.includes("--any-playwright") || process.env.PW_ANY === "1";
console.log(`• playwright ${pwVersion} (pinned ${PLAYWRIGHT_PIN}) from ${pwDir}`);
if (pwVersion !== PLAYWRIGHT_PIN && !anyPlaywright) {
  console.error(`FAIL: playwright ${pwVersion} is not the pinned ${PLAYWRIGHT_PIN}. In PW_DIR: npm i playwright@${PLAYWRIGHT_PIN} && npx playwright install chromium; or pass --any-playwright to run anyway`);
  process.exit(2);
}

const url = process.argv[2], out = process.argv[3] || ".";
const withWallet = process.argv.includes("--wallet");
// --dest=<address>: after the ordinary buy, buy again in private mode (ring 16, paid to that address) and check
// the node shows a 16-member ring on the transaction
const destArg = (process.argv.find((a) => a.startsWith("--dest=")) || "").slice(7);
const passOnly = process.argv.includes("--pass-only"); // wallet flow: connect + Founders Pass steps only
const portfolioOnly = process.argv.includes("--portfolio-only"); // wallet flow: connect + portfolio render only (no spend)
const followArg = (process.argv.find((a) => a.startsWith("--follow=")) || "").slice(9);
// --mainnet: only the mainnet-shaped pass (mainnetPass, at the foot of this file). app/config.js is never edited.
const mainnetMode = process.argv.includes("--mainnet");
const perfMode = process.argv.includes("--perf");
fs.mkdirSync(out, { recursive: true });
const browser = await chromium.launch();
console.log(`• browser: chromium ${browser.version()} (pinned ${CHROMIUM_PIN})`);
if (browser.version() !== CHROMIUM_PIN && !anyPlaywright) {
  console.error(`FAIL: chromium ${browser.version()} is not the build Playwright ${PLAYWRIGHT_PIN} pins (${CHROMIUM_PIN}); pass --any-playwright to run anyway`);
  await browser.close();
  process.exit(2);
}
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
if (!perfMode) try { await (await import("./ui-swaps.mjs")).default({ browser, url, out, step, fail, sleep, mainnet: mainnetMode }); } catch (e) { fail("ui-swaps: " + (e.stack || e)); }

// The mainnet-shaped pass runs on its own and ends the process: its registry may be empty or unreadable, which
// the ordinary pass (it waits for a loaded registry) cannot survive and this one is written to report.
if (!perfMode) await (await import("./ui-discovery.mjs")).run({ browser, url, out, step, fail, sleep, mainnet: mainnetMode }).catch((e) => fail("ui-discovery: " + (e.stack || e)));
if (mainnetMode) {
  try { await mainnetPass(); } catch (e) { fail(e.stack || String(e)); }
  await browser.close();
  console.log(process.exitCode ? "UI TEST FAILED" : "UI TEST OK (mainnet-shaped)");
  process.exit(process.exitCode || 0);
}
if (perfMode) {
  try { await perfPass(); } catch (e) { fail(e.stack || String(e)); }
  await browser.close();
  console.log(process.exitCode ? "UI TEST FAILED" : "UI TEST OK (perf)");
  process.exit(process.exitCode || 0);
}

try {
  step("open " + url);
  await page.goto(url, { waitUntil: "load" });
  await page.locator("#introOk").waitFor({ timeout: 8000 }).catch(() => {});
  // PRIV-01: the intro used to say trading is private by default. The contract stores its reserves at the end of
  // every block they move in, so the size of every trade is public whether it was posted or not; the first screen
  // a user reads has to say so, and this is the assertion that fails if it goes back to claiming otherwise.
  {
    const it = await page.locator("#sheet").innerText().catch(() => "");
    for (const want of [/pseudonymous/i, /the size of the trade/i]) if (!want.test(it)) fail("the intro sheet does not state " + want + ": " + it.replace(/\s+/g, " ").slice(0, 240));
    step("intro: " + it.replace(/\s+/g, " ").slice(0, 90));
  }
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
    const tt = await text();
    // FEE-05: app/notice.js is the app's authoritative page and no screen may say something stronger than it.
    // The pass earns a share of the trading fees the treasury claims, each in the asset it was taken in, and no
    // contract obliges the deposit. "Every protocol fee" is the claim that broke that.
    if (tab === "nfts") {
      for (const bad of [/every protocol fee/i, /fees claimed from every market/i]) if (bad.test(tt)) fail("the NFTs screen states " + bad + ", which app/notice.js contradicts: " + tt.slice(0, 240));
      if (!/DERO/.test(tt)) fail("the NFTs screen never names the asset the pass share is paid in: " + tt.slice(0, 240));
    }
    step(tab + (inBar ? ": " : " (not in the phone bar): ") + tt.slice(0, 90));
  }
  if (process.argv.includes("--promo")) await (await import("./promo.mjs")).run({ browser, url, out, step, fail, as: (process.argv.find((a) => a.startsWith("--as=")) || "").slice(5) });
  // a holder-NFT collection page when the simulator has one
  const nftId = await page.evaluate(() => { const n = [...window.Veil.data.nfts.keys()]; return n.length ? n[0] : null; });
  if (nftId) { await page.evaluate((s) => { location.hash = "#nft/" + s; }, nftId); await sleep(1500); await shot("02b-nft"); const nt = await text(); if (!/Public accounting/.test(nt)) fail("nft page missing accounting card: " + nt.slice(0, 160)); step("nft page: " + nt.slice(0, 80)); }
  // An NFA that owes a share (contracts/nfa.bas header, note 5) must show it with a Pay out anyone may press, and say
  // whom it pays. Checked on the first listed NFA that owes something, when there is one; said, when there is not.
  const nfaOwed = await page.evaluate(() => { const s = [...window.Veil.data.nfas.values()].find((x) => x.owed > 0n); return s ? s.scid : null; });
  if (nfaOwed) {
    await page.evaluate((s) => { location.hash = "#nfa/" + s; }, nfaOwed); await sleep(3000); await shot("02c-nfa-owed");
    const at = await text();
    // innerText follows CSS text-transform, and the box's label is set in the uppercase .label style: match any case
    for (const want of [/Owed, waiting to be paid out/i, /never the person who presses it/i]) if (!want.test(at)) fail("the NFA page does not show its owed share: missing " + want + ": " + at.slice(0, 240));
    if (!(await page.getByRole("button", { name: "Pay out", exact: true }).count())) fail("the NFA page offers no Pay out for an owed share");
    const o = Math.max(0, at.search(/owed, waiting/i));
    step("nfa owed: " + at.slice(o, o + 140));
  } else step("no listed NFA owes a share: Pay out not exercised");
  await page.evaluate(() => { location.hash = "#settings"; }); await sleep(600); await shot("03-settings");
  {
    const st = await text();
    for (const want of [/pseudonymous/i, /the size of the trade/i]) if (!want.test(st)) fail("Settings does not state " + want + ": " + st.slice(0, 240));
    // The banned phrases, in the source rather than in a render: both were load-bearing claims that the chain
    // contradicts, and a render only catches the one on screen at the time.
    const appDir = new URL("../../app/", import.meta.url);
    for (const f of fs.readdirSync(appDir).filter((n) => n.endsWith(".js"))) {
      const src = fs.readFileSync(new URL(f, appDir), "utf8");
      for (const bad of [/private by default/i, /never individual unpublished trades/]) if (bad.test(src)) fail("app/" + f + " contains the banned phrase " + bad);
    }
    step("privacy copy: intro and Settings both say pseudonymous; no banned phrase in app/*.js");
  }
  // the wrapped-asset screen: it must publish the two numbers a user cannot discover any other way — the minimum
  // deposit and the height a withdrawal becomes payable — or it is a trap rather than a screen
  {
    await page.evaluate(() => { location.hash = "#wrap"; }); await sleep(1500); await shot("03b-wrap");
    const wt = await text();
    const configured = await page.evaluate(() => !!(window.Veil.data.cfg.vault));
    if (configured) {
      if (!/smallest deposit/i.test(wt)) fail("wrap screen does not publish the smallest deposit: " + wt.slice(0, 200));
      if (!/what you are trusting/i.test(wt)) fail("wrap screen has no trust panel: " + wt.slice(0, 200));
      if (/\bSAFE\b/.test(wt)) fail("wrap screen shows a SAFE badge; the trust panel must be factual");
      // UX-01: the ceiling belongs where the floor already is. A vault at its cap does not refuse a deposit, it
      // strands it, so the headroom has to be stated in the deposit section and not only in the hero.
      const depFrom = wt.indexOf("Smallest deposit"), depTo = wt.indexOf("Burn and withdraw");
      const depSlice = depFrom >= 0 && depTo > depFrom ? wt.slice(depFrom, depTo) : wt;
      if (!/room to mint now/i.test(depSlice)) fail("the deposit walkthrough does not state the headroom: " + depSlice.slice(0, 240));
      // UX-06: the deployment record's chain string is a clause, not a noun. Splicing it into prose and cutting it
      // to fit severed a word and left the parenthesis open seven times on this screen; count them instead.
      const raw = await page.locator("#screen").innerText();
      const opens = (raw.match(/\(/g) || []).length, closes = (raw.match(/\)/g) || []).length;
      if (opens !== closes) fail("the wrap screen has " + opens + " '(' and " + closes + " ')': a config string is being cut mid-clause");
      // UX-05: the bond deters nobody when a quorum of the others cannot be reached (n < 2k), which is the shape
      // both simulator vaults are in. The line must not carry the green check there.
      const bond = await page.evaluate(() => {
        const w = window.Veil.data.vault; if (!w) return null;
        const r = [...document.querySelectorAll("#screen .card .row")].find((x) => /bonded|bond is at stake/i.test(x.innerText));
        return r ? { txt: r.innerText.replace(/\s+/g, " "), ok: !!r.querySelector(".svgi.up"), na: String(w.na), k: String(w.k) } : null;
      });
      if (bond) {
        if (BigInt(bond.na) < 2n * BigInt(bond.k) && bond.ok) fail("the signer bond is shown with a green check on a " + bond.k + "-of-" + bond.na + " vault, where the quorum that can take the reserve is the only quorum that could slash it: " + bond.txt);
        step("wrap: " + bond.k + "-of-" + bond.na + ", bond line " + (bond.ok ? "affirmed" : "qualified") + "; parens " + opens + "/" + closes);
      }
    } else if (!/not offered on this network/i.test(wt)) {
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
  // the message from the dev outranks every other piece of copy, so it must be present and reachable — and it must
  // name the federation that holds the foreign coins, because a plaque that says nobody holds anything is the lie
  {
    await page.evaluate(() => { location.hash = "#notice"; }); await sleep(900); await shot("03d-notice");
    const nt = await text();
    for (const want of [/no support/i, /code is law/i, /space cowboy/i, /federation/i]) {
      if (!want.test(nt)) fail("the dev message is missing " + want + ": " + nt.slice(0, 200));
    }
    step("notice: " + nt.replace(/\s+/g, " ").slice(0, 90));
  }
  const D_VAULTS = await page.evaluate(() => { const v = window.Veil.data.vaults()[0]; return v ? (v.sym || v.name) : null; });
  // The name of a wrapped asset is the most valuable thing to steal on DMZ: the whole product points a Monero
  // holder at a ticker called XMR, so a token that simply calls itself WXMR is the cheapest attack there is.
  // Checked against synthetic markets rather than chain state, so it holds on any network and needs no fixture.
  {
    const r = await page.evaluate(() => {
      const D = window.Veil.data, v = D.vaults()[0];
      if (!v) return { skip: true };
      const sym = v.sym || v.name;
      return {
        sym,
        copy: D.impersonation({ ticker: sym, scid: "f".repeat(64) }),
        wCopy: D.impersonation({ ticker: "w" + sym, scid: "e".repeat(64) }),
        genuine: D.impersonation({ ticker: sym, scid: "d".repeat(64), asset: v.scid }),
        vaultItself: D.impersonation({ ticker: sym, scid: v.scid }),
        unrelated: D.impersonation({ ticker: "SOMETHINGELSE", scid: "c".repeat(64) }),
        listedCopies: D.list().filter((x) => x.impersonates).map((x) => x.ticker + "→" + x.impersonates),
        flaggedGenuine: D.list().filter((x) => x.wrappedBase && (x.impersonates || x.dupTicker)).map((x) => x.ticker),
      };
    });
    if (r.skip) step("impersonation: no vault configured, skipped");
    else {
      if (r.copy !== r.sym) fail("a token calling itself " + r.sym + " is not flagged as a copy: " + r.copy);
      if (r.wCopy !== r.sym) fail("a token calling itself w" + r.sym + " is not flagged as a copy: " + r.wCopy);
      if (r.genuine) fail("the real wrapped asset is flagged as a copy of itself: " + r.genuine);
      if (r.vaultItself) fail("the vault contract is flagged as a copy of itself: " + r.vaultItself);
      if (r.unrelated) fail("an unrelated ticker is flagged as a copy: " + r.unrelated);
      if (r.flaggedGenuine.length) fail("a genuine wrapped market carries an impersonation or shared-ticker warning: " + r.flaggedGenuine.join(","));
      step("impersonation: " + r.sym + " and w" + r.sym + " reserved to the vault; listed copies: " + (r.listedCopies.join(", ") || "none"));
    }
  }
  // The thin half of a split pair must never rank: its hot score and 24h move are the cheapest numbers on the
  // exchange to manufacture, and Home rank 1 is where a drain would be advertised. Every member carries the
  // group, exactly one is the deepest, and only that one may appear in trending or movers.
  {
    const r = await page.evaluate(() => {
      const D = window.Veil.data;
      const groups = new Map();
      for (const s of D.list()) if (s.splitWith) (groups.get(s.splitWith.join()) || groups.set(s.splitWith.join(), []).get(s.splitWith.join())).push(s);
      const ranked = new Set(D.trending(100).concat(D.movers(100)).map((s) => s.scid));
      const bad = [];
      for (const [k, g] of groups) {
        if (g.filter((s) => s.splitDeepest).length !== 1) bad.push("group " + k.slice(0, 8) + " has " + g.filter((s) => s.splitDeepest).length + " deepest");
        for (const s of g) if (!s.splitDeepest && ranked.has(s.scid)) bad.push("thin " + s.scid.slice(0, 8) + " ranked");
      }
      return { groups: groups.size, bad, imp: D.impersonation({ name: "Wrapped Monero", ticker: "WM", scid: "f".repeat(64) }), safe: D.impersonation({ name: "Zecret", ticker: "ZCR", scid: "e".repeat(64) }) };
    });
    if (r.bad.length) fail("split pairs: " + r.bad.join("; "));
    if (D_VAULTS && r.imp !== D_VAULTS) fail("a token NAMED after the wrapped asset is not flagged: " + r.imp);
    if (r.safe) fail("a name that merely contains the letters of a symbol is flagged: " + r.safe);
    step("split pairs: " + r.groups + " group(s), thin halves never rank; name impersonation " + (r.imp || "n/a"));
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
    await (await import("./ui-market.mjs")).marketPass({ browser, url, out, step, fail, sleep }); // the market page at 1280/768/390
    await page.evaluate(() => { location.hash = "#markets"; }); await sleep(1200); await shot("07-markets-live");
    // a coin's Configure step with its live preview (read-only: filled, never sent). The launch sheet is now the
    // Create → Configure → Review → Pay → Launch flow (app/wizard.js); its chooser opens as ui-launch.mjs opens it
    await page.evaluate(() => window.Veil.forms.create()); await page.locator("#sheet .wz-kind").first().waitFor({ timeout: 15000 });
    await page.locator("#sheet .wz-kind").first().click(); await sleep(500); // Coin
    await page.locator("#sheet input[name=name]").fill("Preview Coin"); await page.locator("#sheet input[name=ticker]").fill("PRV"); await sleep(400); await shot("08-launch-sheet");
    const pv = await page.locator("#sheet").innerText(); if (!/16×/.test(pv) || !/Two wallet confirmations/.test(pv)) fail("launch preview missing: " + pv.slice(0, 200));
    await page.keyboard.press("Escape"); await sleep(300);
  }

  // BL-129: when the node stops answering, the figures on screen are the last good read, and every screen says how
  // old they are. The node is made to refuse every read, a refresh is forced, and the banner must say so with an age;
  // the node is given back, and the banner must go.
  {
    await page.evaluate(() => { location.hash = "#markets"; }); await sleep(800);
    const down = await page.evaluate(async () => {
      const n = window.Veil.node, D = window.Veil.data, saved = (window.__nodeSaved = {});
      for (const k of Object.keys(n)) if (typeof n[k] === "function" && !/^set/.test(k)) { saved[k] = n[k]; n[k] = () => Promise.reject(new Error("test: the node refused")); }
      await D.refresh(true).catch(() => {});
      const b = document.getElementById("stale");
      return { shown: !!b && !b.hidden, text: b ? b.innerText.replace(/\s+/g, " ") : "" };
    });
    await sleep(1500); await shot("02j-stale");
    step("stale banner with the node refusing: " + (down.shown ? down.text.slice(0, 160) : "NOT SHOWN"));
    if (!down.shown || !/did not answer the last read/.test(down.text) || !/were read \d+[smhd] ago/.test(down.text)) fail("no stale-data banner with an age while the node refuses: " + JSON.stringify(down));
    const up = await page.evaluate(async () => {
      const n = window.Veil.node; Object.assign(n, window.__nodeSaved);
      await window.Veil.data.refresh(true).catch(() => {});
      return !document.getElementById("stale").hidden;
    });
    step("stale banner once the node answers again: " + (up ? "STILL SHOWN" : "gone"));
    if (up) fail("the stale-data banner stays after the node answers again");
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
    // 390 is the narrowest phone the design targets and 768 a portrait tablet, the band between the phone bar
    // and the sidebar shell. Same rule as 1024: nothing may stick out sideways on any screen.
    for (const width of [390, 768]) {
      const n = await browser.newPage({ viewport: { width, height: 860 }, bypassCSP: true });
      await n.goto(url, { waitUntil: "load" }); await n.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
      await n.waitForFunction(() => window.Veil && window.Veil.data.lastRefresh > 0, null, { timeout: 60000 });
      let worst = 0;
      for (const tab of ["home", "markets", "wrap", "notice"]) {
        await n.evaluate((t) => { location.hash = "#" + t; }, tab); await sleep(1200);
        const over = await n.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
        worst = Math.max(worst, over);
        if (over > 1) fail("page overflows horizontally by " + over + "px at " + width + " on " + tab);
      }
      await n.screenshot({ path: out + "/w" + width + "-notice.png" });
      step(width + "px horizontal overflow: " + worst + "px (worst of home, markets, wrap, notice)");
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
    step("launch a token through the creation flow");
    await page.evaluate(() => window.Veil.forms.create()); await page.locator("#sheet .wz-kind").first().waitFor({ timeout: 15000 });
    await page.locator("#sheet .wz-kind").first().click(); await sleep(500); // Coin: its Configure step
    await page.locator("#sheet input[name=name]").fill("Browser Coin");
    await page.locator("#sheet input[name=ticker]").fill("BRW");
    await page.locator("#sheet textarea[name=descr]").fill("launched from the headless browser test");
    await page.locator("#sheet details summary").click(); // Advanced: supply, decimals, tax
    await page.locator("#sheet input[name=supply]").fill("1000000"); // 1e6 tokens × 10^3 = 1e9 units: wallet-friendly
    await page.locator("#sheet input[name=dec]").fill("3");
    await page.locator("#sheet input[name=grad]").fill("10"); // graduation DERO (min)
    await page.locator("#sheet input[name=tax]").fill("2");   // tax %
    // stakers, by its words: in the Configure step the row sits under the sheet's sticky actions, and a click by index did not land
    await page.locator("#sheet .seg button", { hasText: /^Stakers$/ }).click();
    await shot("11-launch-form");
    await (await import("./ui-launch.mjs")).hook(page); // the creation flow: Configure → Review here; the click below is "Pay and launch"
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
      if (!/16 · the payer is one of 8/.test(det)) fail("ring size 16, and what it hides, not shown in the sheet: " + det.replace(/\s+/g, " ").slice(0, 300));
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
    // Profile is not in the phone bar (the read-only pass above reaches it the same way): a click waits 30 s and fails
    await page.evaluate(() => { location.hash = "#profile"; }); await sleep(1500); await shot("17-profile");
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
  if (process.argv.includes("--sentiment")) await (await import("./sentiment.mjs")).sentimentPass({ browser, url, out, step, fail }); // community sentiment, read at 430 and 1280
  console.log(process.exitCode ? "UI TEST FAILED" : "UI TEST OK");
} catch (e) {
  fs.writeFileSync(out + "/console.log", logs.join("\n")); // the page console (toasts included) is the evidence for a failed step
  fail(e.stack || String(e));
  await shot("99-error").catch(() => {});
} finally {
  await browser.close();
}
function location_hash(h) { return h.replace(/^#token\//, ""); }

// --mainnet. The app under a mainnet-shaped configuration, without editing app/config.js: the config.js the server
// sends is kept and amended on its way to the page. The network label becomes "mainnet" and every vault record
// goes, which is the shape deploy/networks.json gives mainnet. The daemon is left as served (the simulator's), so
// the same run proves a mainnet-labelled build says out loud that its node is on another network, and will not
// sign. Wrap, Home, Markets and Trade, with Portfolio and the notice, are captured at 390 and 1024: read the PNGs.
async function mainnetPass() {
  const shape = async (route) => {
    const r = await route.fetch();
    const body = (await r.text()) + '\n;(function (c) { c.network = "mainnet"; delete c.vaults; delete c.vault; delete c.vaultAsset; })(window.VEIL_CONFIG);\n';
    await route.fulfill({ status: 200, contentType: "text/javascript; charset=utf-8", body });
  };
  for (const width of [390, 1024]) {
    const p = await browser.newPage({ viewport: { width, height: 860 }, bypassCSP: true });
    const perr = [];
    p.on("pageerror", (e) => perr.push("pageerror: " + e.message));
    p.on("console", (m) => { if (m.type() === "error") perr.push("console: " + m.text()); });
    // the mainnet rule: no request and no socket to any host but this device (the node and the XSWD wallet here)
    const away = [], local = (s) => { try { const u = new URL(s); return u.protocol === "data:" || /^(127\.0\.0\.1|localhost)$/.test(u.hostname); } catch (e) { return false; } };
    p.on("request", (r) => { if (!local(r.url())) away.push(r.url()); });
    p.on("websocket", (w) => { if (!local(w.url())) away.push(w.url()); });
    await p.route(/\/config\.js(\?.*)?$/, shape);
    await p.goto(url, { waitUntil: "load" });
    await p.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
    const cfg = await p.evaluate(() => ({ network: window.Veil.data.cfg.network, vaults: window.Veil.data.allVaults().length }));
    step(width + "px, mainnet-shaped config: network " + cfg.network + ", " + cfg.vaults + " vault(s)");
    if (cfg.network !== "mainnet" || cfg.vaults !== 0) fail("the mainnet-shaped config did not take: " + JSON.stringify(cfg));
    // the node's own answer, then the banner that must stay on screen while the two disagree
    await p.waitForFunction(() => /\d/.test(document.getElementById("net").textContent), null, { timeout: 30000 }).catch(() => {});
    await sleep(1500);
    const pill = await p.locator("#net").innerText();
    const banner = await p.evaluate(() => { const b = document.getElementById("netwarn"); return b && !b.hidden ? b.innerText.replace(/\s+/g, " ") : ""; });
    step("pill " + JSON.stringify(pill) + " · banner: " + banner.slice(0, 170));
    if (!/mainnet/i.test(banner) || !/simulator/i.test(banner)) fail("a mainnet-labelled build reading a simulator node shows no standing banner saying so: " + JSON.stringify(banner));
    // and the gate every transaction passes refuses before any wallet is asked
    const gate = await p.evaluate(() => window.Veil.app.netGate());
    step("signing gate: " + String(gate).slice(0, 150));
    if (!gate) fail("a mainnet-labelled build reading a simulator node would open a signing flow");
    for (const tab of ["wrap", "move", "home", "markets", "trade", "portfolio", "notice"]) {
      await p.evaluate((t) => { location.hash = "#" + t; }, tab); await sleep(1500);
      await p.screenshot({ path: `${out}/mainnet-${width}-${tab}.png`, fullPage: true });
      const tt = (await p.locator("#screen").innerText()).replace(/\s+/g, " ");
      step("mainnet " + width + "px " + tab + ": " + tt.slice(0, 110));
      // a wrapped asset is never mentioned on a build that has none without the words that say it is not offered
      if (/\bw(XMR|ZEC)\b|wrapped (XMR|ZEC|Monero|Zcash|units|asset)/i.test(tt) && !/not offered on this network/i.test(tt)) fail(tab + " mentions a wrapped asset on a build with no vault and never says it is not offered: " + tt.slice(0, 300));
      if (tab === "wrap") {
        const at = tt.search(/not available on this network/i);
        if (at < 0 || at > 80) fail("the Wrap screen does not open with 'not available on this network' (found at " + at + "): " + tt.slice(0, 200));
        for (const want of [/federation/i, /named people/i, /not offered on this network/i]) if (!want.test(tt)) fail("the Wrap screen does not say why: missing " + want);
        for (const bad of [/minimum deposit/i, /burn and withdraw/i, /register a deposit index/i, /deposit address/i, /could not be verified/i, /\btrustless\b/i, /\bswap/i]) if (bad.test(tt)) fail("the Wrap screen on a build with no vault shows " + bad);
        // no control at all: a build with no vault has nothing here to sign or to open
        const ctl = await p.evaluate(() => ({ inputs: document.querySelectorAll("#screen input").length, buttons: [...document.querySelectorAll("#screen button")].map((b) => b.innerText.trim()) }));
        if (ctl.inputs || ctl.buttons.length) fail("the Wrap screen on a build with no vault draws controls: " + JSON.stringify(ctl));
      }
      if (tab === "notice") {
        if (/federation holds the|bonded signers hold/i.test(tt)) fail("the dev message names a federation holding coins on a build with none");
        if (!/not offered on this network/i.test(tt)) fail("the dev message does not say wrapped assets are not offered here");
      }
      if (tab === "trade" && /against a wrapped asset/i.test(tt)) fail("the Trade hub offers markets against a wrapped asset on a build with none");
      const over = await p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      if (over > 1) fail("the mainnet-shaped " + tab + " overflows horizontally by " + over + "px at " + width);
    }
    // XMR and ZEC stay reserved names with no vault: a token wearing them is marked, not waved through
    const imp = await p.evaluate(() => { const D = window.Veil.data; return [
      D.impersonation({ ticker: "XMR", scid: "f".repeat(64), asset: "f".repeat(64) }), D.impersonation({ ticker: "wZEC", scid: "e".repeat(64) }),
      D.impersonation({ ticker: "PRV", name: "Wrapped Monero", scid: "d".repeat(64) }), D.impersonation({ ticker: "ZCR", name: "Zecret", scid: "c".repeat(64) })]; });
    step("impersonation with no vault: " + JSON.stringify(imp));
    if (imp[0] !== "XMR" || imp[1] !== "ZEC" || imp[2] !== "XMR" || imp[3] !== null) fail("names reserved with no vault: " + JSON.stringify(imp));
    step("requests that left this device at " + width + "px: " + (away.length ? away.join(", ") : "none"));
    if (away.length) fail("the mainnet-shaped app reached a host other than this device: " + away.join(", "));
    const bad = perr.filter((e) => !/favicon|net::ERR_/.test(e));
    if (bad.length) fail("console errors under the mainnet-shaped config at " + width + "px:\n" + bad.join("\n"));
    await p.close();
  }
}

// --perf (MT-M02). App start and first render, against the node this build names. Each run opens the app in a new
// browser context (cold: no HTTP cache and no storage, so the first-run intro shows too), then reloads it in the
// same context (warm: the HTTP cache and the stored settings kept). Every mark is page time from navigation start:
// dcl and load (Navigation Timing), fp and fcp (Paint Timing), and three of the app's own, sampled on every
// animation frame by a script injected before the app's: shell (#screen holds its first element: the scripts ran
// and the router drew), node (the pill shows a height: the node answered) and data (the registry read has landed
// and #screen holds no skeleton: the first screen with figures on it). rpc counts the node calls started before
// data; files and kb are the app's scripts and styles and the bytes they cost over the wire. A median of --runs
// runs (default 5) per width, 430 and 1280. The node's own round trip is measured first, for scale.
async function perfPass() {
  const runs = Math.max(1, +((process.argv.find((a) => a.startsWith("--runs=")) || "").slice(7)) || 5);
  const daemon = await (async () => { const p = await browser.newPage(); await p.goto(url, { waitUntil: "domcontentloaded" }); const d = await p.evaluate(() => window.VEIL_CONFIG && window.VEIL_CONFIG.daemon); await p.close(); return d; })();
  const rtt = [];
  for (let i = 0; i < 7; i++) {
    const t = performance.now();
    await fetch("http://" + daemon + "/json_rpc", { method: "POST", headers: { "Content-Type": "application/json" }, body: '{"jsonrpc":"2.0","id":1,"method":"DERO.GetInfo"}' }).then((r) => r.text()).catch(() => "");
    rtt.push(performance.now() - t);
  }
  const med = (a) => { const s = a.filter((x) => typeof x === "number" && isFinite(x)).sort((x, y) => x - y); return s.length ? Math.round(s[Math.floor(s.length / 2)] * 10) / 10 : null; };
  step("node " + daemon + ": DERO.GetInfo round trip, median of 7: " + med(rtt) + " ms");
  const probe = () => {
    const m = (window.__perf = { sent: [] });
    // the app reads its node over a WebSocket (rpc.js), so a node call is a frame sent, not a resource entry
    const send = WebSocket.prototype.send;
    WebSocket.prototype.send = function (d) { m.sent.push(performance.now()); return send.call(this, d); };
    const tick = () => {
      const now = performance.now(), sc = document.getElementById("screen"), net = document.getElementById("net"), D = window.Veil && window.Veil.data;
      if (!m.shell && sc && sc.children.length) m.shell = now;
      if (!m.node && net && /\d/.test(net.textContent)) m.node = now;
      if (!m.reg && D && D.lastRefresh) m.reg = now;
      if (!m.data && m.reg && sc && sc.children.length && !sc.querySelector(".skel")) m.data = now;
      if (!m.data) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  };
  const read = (p) => p.evaluate(() => {
    const n = performance.getEntriesByType("navigation")[0] || {}, m = window.__perf || {};
    const paint = Object.fromEntries(performance.getEntriesByType("paint").map((e) => [e.name, e.startTime]));
    const res = performance.getEntriesByType("resource"), own = res.filter((r) => /\.(js|css)(\?|$)/.test(r.name));
    return { dcl: n.domContentLoadedEventEnd, load: n.loadEventEnd, fp: paint["first-paint"], fcp: paint["first-contentful-paint"], shell: m.shell, node: m.node, reg: m.reg, data: m.data,
      rpc: (m.sent || []).filter((t) => !m.data || t < m.data).length + res.filter((r) => /json_rpc/.test(r.name) && (!m.data || r.startTime < m.data)).length, files: own.length, kb: Math.round(own.reduce((a, r) => a + (r.transferSize || 0), 0) / 102.4) / 10 };
  });
  const rows = [], fields = ["dcl", "load", "fp", "fcp", "shell", "node", "reg", "data", "rpc", "files", "kb"];
  for (const width of [430, 1280]) {
    for (let i = 0; i < runs; i++) {
      const ctx = await browser.newContext({ viewport: { width, height: 860 }, bypassCSP: true });
      await ctx.addInitScript(probe);
      const p = await ctx.newPage(), perr = [];
      p.on("pageerror", (e) => perr.push(e.message));
      for (const kind of ["cold", "warm"]) {
        if (kind === "cold") await p.goto(url, { waitUntil: "load" }); else await p.reload({ waitUntil: "load" });
        await p.waitForFunction(() => window.__perf && window.__perf.data, null, { timeout: 60000 }).catch(() => {});
        const r = Object.assign({ width, kind, run: i + 1 }, await read(p));
        rows.push(r);
        step(width + "px " + kind + " #" + (i + 1) + ": " + fields.map((k) => k + " " + (typeof r[k] === "number" ? Math.round(r[k] * 10) / 10 : "—")).join(" · "));
        if (i === 0) await p.screenshot({ path: `${out}/perf-${width}-${kind}.png` });
        if (kind === "cold") await p.locator("#introOk").click({ timeout: 3000 }).catch(() => {});
      }
      if (perr.length) fail("page errors during the perf run: " + perr.join("; "));
      await ctx.close();
    }
  }
  const summary = [];
  for (const width of [430, 1280]) for (const kind of ["cold", "warm"]) {
    const rs = rows.filter((r) => r.width === width && r.kind === kind), s = { width, kind, runs: rs.length };
    for (const k of fields) s[k] = med(rs.map((r) => r[k]));
    summary.push(s);
    step("MEDIAN " + width + "px " + kind + " (" + rs.length + " runs): " + fields.map((k) => k + " " + s[k]).join(" · ") + " (ms, calls, files, kB)");
  }
  fs.writeFileSync(out + "/perf.json", JSON.stringify({ url, daemon, when: new Date().toISOString(), rttMs: rtt, summary, rows }, null, 2));
  if (rows.some((r) => !r.data)) fail(rows.filter((r) => !r.data).length + " run(s) never reached a first render with data within 60 s");
}
