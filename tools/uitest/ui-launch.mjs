// The creation flow's headless pass (app/wizard.js, app/forms.js, app/create.js): Create → Configure → Review → Pay →
// Launch, at phone (430) and desktop (1280) width, against a running simulator.
//
//   PW_DIR=<playwright> node tools/uitest/ui-launch.mjs http://127.0.0.1:8104/index.html <outdir> [--wallet-port=30014]
//        [--mainnet] [--spend] [--recover] [--profile-stub] [--width=430]
//
// default    every step up to Review for each kind, both widths: labels on every field, errors tied to their field,
//            40px touch targets, no horizontal overflow, the step rail's current step, the itemised cost
// --mainnet  the same app under a mainnet-shaped config.js (network "mainnet", no vault), rewritten in the browser as
//            ui.mjs --mainnet does: DERO is the only quote, nothing names a wrapped asset, and Review refuses to pay
// --spend    one coin launched end to end through the flow, with its ids printed (LAUNCHED ...)
// --recover  the registration is refused once in the wallet, the page is closed, and a new page registers the coin
//            from the Create step's unfinished list: the record on this device, checked against the chain
// --profile-stub  a stand-in for V.profile (the profile document is another file, absent from this build): it
//            installs a tiny real document holding the links, so the three-confirmation path runs on the chain
//
// The wallet is the simulator's wallet RPC at --wallet-port, reached through a WebSocket route that answers the
// XSWD handshake and forwards each request as it is (DERO.* to the node): no xswdwallet and no port 44326 needed.
// ui.mjs's wallet pass calls hook() below, one line, because its launch now has a Review step before the pay.
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import fs from "node:fs";

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const primary = "#sheet .wz-act .btn.primary";

// For ui.mjs: from the filled Configure step to an enabled "Pay and launch" (ui.mjs clicks it next), and, when the
// Launch step appears, "Open it", which takes the page to the new coin, where ui.mjs goes on.
export async function hook(page) {
  await page.locator(primary).click();
  await page.waitForFunction((s) => { const b = document.querySelector(s); return b && /Pay and launch/.test(b.textContent) && !b.disabled; }, primary, { timeout: 90000 });
  page.waitForSelector("#sheet h2.wz-done", { timeout: 300000 }).then(() => page.locator(primary).click()).catch(() => {});
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) await main();

async function main() {
  const require = createRequire((process.env.PW_DIR || ".") + "/package.json");
  const { chromium } = require("playwright");
  const url = process.argv[2], out = process.argv[3] || ".", opt = (k) => (process.argv.find((a) => a.startsWith("--" + k + "=")) || "").split("=")[1];
  const has = (k) => process.argv.includes("--" + k);
  const wport = opt("wallet-port") || "30014", mainnet = has("mainnet"), spend = has("spend"), recover = has("recover"), stub = has("profile-stub");
  const widths = opt("width") ? [+opt("width")] : [430, 1280];
  fs.mkdirSync(out, { recursive: true });
  const browser = await chromium.launch();
  console.log("• playwright " + require("playwright/package.json").version + " · chromium " + browser.version() + " · wallet 127.0.0.1:" + wport);
  let failed = 0; const fail = (m) => { failed++; console.error("FAIL: " + m); }, step = (m) => console.log("• " + m);
  const cfg = fs.readFileSync(new URL("../../app/config.js", import.meta.url), "utf8"), daemon = (cfg.match(/daemon: "([^"]+)"/) || [])[1] || "127.0.0.1:20000";
  let denyRegister = recover ? 1 : 0;
  const ctx = await browser.newContext({ viewport: { width: widths[0], height: 860 }, bypassCSP: true });
  // the XSWD route: handshake, then each JSON-RPC text forwarded untouched (large integers stay exact)
  await ctx.routeWebSocket(/^ws:\/\/localhost:44326\/xswd/, (ws) => {
    let ready = false;
    ws.onMessage(async (m) => {
      const text = String(m);
      if (!ready) { ready = true; ws.send(JSON.stringify({ accepted: true, message: "ui-launch wallet route" })); return; }
      let q; try { q = JSON.parse(text); } catch (e) { return; }
      if (q.method === "GetDaemon") return ws.send(JSON.stringify({ jsonrpc: "2.0", id: q.id, result: { endpoint: daemon } }));
      if (q.method === "transfer" && denyRegister && (q.params.sc_rpc || []).some((a) => a.name === "entrypoint" && /^Register/.test(a.value))) {
        denyRegister--; step("wallet route: refusing this registration once (--recover)");
        return ws.send(JSON.stringify({ jsonrpc: "2.0", id: q.id, error: { code: -32043, message: "denied by the test" } }));
      }
      const to = q.method.startsWith("DERO.") ? daemon : "127.0.0.1:" + wport;
      try { const r = await fetch("http://" + to + "/json_rpc", { method: "POST", headers: { "Content-Type": "application/json" }, body: text }); ws.send(await r.text()); }
      catch (e) { ws.send(JSON.stringify({ jsonrpc: "2.0", id: q.id, error: { code: -32000, message: String(e.message) } })); }
    });
  });
  if (mainnet) await ctx.route(/\/config\.js(\?.*)?$/, async (route) => {
    const r = await route.fetch();
    await route.fulfill({ status: 200, contentType: "text/javascript; charset=utf-8", body: (await r.text()) + '\n;(function (c) { c.network = "mainnet"; delete c.vaults; delete c.vault; delete c.vaultAsset; })(window.VEIL_CONFIG);\n' });
  });

  const errs = [];
  const open = async (width) => {
    const p = await ctx.newPage(); await p.setViewportSize({ width, height: 860 });
    p.on("pageerror", (e) => errs.push("pageerror: " + e.message)); p.on("console", (m) => { if (m.type() === "error" && !/favicon|net::ERR_/.test(m.text())) errs.push("console: " + m.text()); if (/^TOAST /.test(m.text())) console.log("  " + m.text().slice(0, 200)); });
    await p.goto(url, { waitUntil: "load" }); await p.locator("#introOk").click({ timeout: 5000 }).catch(() => {});
    await p.evaluate(() => new MutationObserver((ms) => ms.forEach((m) => m.addedNodes.forEach((n) => console.log("TOAST " + n.textContent)))).observe(document.getElementById("toasts"), { childList: true }));
    await p.waitForFunction(() => window.Veil.data.lastRefresh || window.Veil.data.lastError, null, { timeout: 30000 }).catch(() => {});
    if (stub) await p.evaluate(() => {
      const V = window.Veil;
      // TEST STUB for V.profile: a document holding the links as keys, installed for real through app.tx
      V.profile = { async build({ links }) { const ls = Object.entries(links || {}).map(([k, u], i) => (20 + i) + ' STORE("' + k + '", "' + u + '")'); const code = 'Function InitializePrivate() Uint64\n10 STORE("kind", "profile-stub")\n' + ls.join("\n") + '\n90 RETURN 0\nEnd Function\n'; return { code, bytes: code.length, gasEstimate: code.length + 600 }; },
        install: (b) => V.app.tx({ code: b.code, args: [], describe: "Install the profile document" }), ref: (s) => "dmz:" + s };
    });
    return p;
  };
  const connect = async (p) => { if (mainnet) return; await p.click("#walletBtn"); await p.waitForFunction(() => window.Veil.wallet.connected, null, { timeout: 60000 }); };
  const text = async (p) => (await p.locator("#sheet").innerText()).replace(/\s+/g, " ");
  const snap = async (p, name, full) => {
    await sleep(250); await p.screenshot({ path: `${out}/${name}.png` });
    if (full) { const t = await p.addStyleTag({ content: "#sheet{position:absolute!important;max-height:none!important;overflow:visible!important;top:0!important;bottom:auto!important;left:50%!important;transform:translateX(-50%)!important;animation:none!important}.wz-act{position:static!important}" }); await sleep(150); await p.screenshot({ path: `${out}/${name}-full.png`, fullPage: true }); await t.evaluate((n) => n.remove()); }
  };
  // every field labelled, 40px targets, the rail's current step, no sideways scroll
  const check = async (p, want, where) => {
    const r = await p.evaluate(() => {
      const s = document.getElementById("sheet"), bad = [];
      for (const el of s.querySelectorAll("input,select,textarea")) if (!((el.labels && el.labels.length) || el.getAttribute("aria-label") || el.getAttribute("aria-labelledby"))) bad.push("unlabelled " + (el.name || el.type));
      for (const el of s.querySelectorAll(".wz button, .wz input:not([type=checkbox]), .wz select, .wz summary")) { const b = el.getBoundingClientRect(); if (b.width && b.height && b.height < 39.5) bad.push("target " + Math.round(b.height) + "px: " + (el.textContent || el.name || el.tagName).trim().slice(0, 24)); }
      for (const el of s.querySelectorAll(".wz input[type=checkbox]")) if (el.closest("label").getBoundingClientRect().height < 40) bad.push("checkbox label under 40px");
      const cur = s.querySelector(".wz-steps [aria-current=step] .t");
      return { bad, step: cur ? cur.textContent : null, over: document.documentElement.scrollWidth - document.documentElement.clientWidth, sheetOver: s.scrollWidth - s.clientWidth };
    });
    if (r.step !== want) fail(where + ": the rail's current step is " + r.step + ", not " + want);
    if (r.bad.length) fail(where + ": " + r.bad.join("; "));
    if (r.over > 1 || r.sheetOver > 1) fail(where + ": overflows sideways by " + Math.max(r.over, r.sheetOver) + "px");
    const t = await text(p);
    if (mainnet && /\bw(XMR|ZEC)\b|wrapped (XMR|ZEC|Monero|Zcash|units|asset)|\bXMR\b|\bZEC\b/i.test(t)) fail(where + ": a mainnet-shaped build names a wrapped asset: " + t.slice(0, 200));
    return t;
  };
  const toCreate = async (p) => { await p.evaluate(() => window.Veil.forms.create()); await p.locator("#sheet .wz-kind").first().waitFor(); };
  const kind = async (p, i) => { await toCreate(p); await p.locator("#sheet .wz-kind").nth(i).click(); await sleep(300); };
  const fillCoin = async (p, name, tick) => {
    await p.fill("#sheet input[name=name]", name); await p.fill("#sheet input[name=ticker]", tick); await p.fill("#sheet input[name=grad]", "10");
    await p.fill("#sheet textarea[name=descr]", "launched through the creation flow by tools/uitest/ui-launch.mjs");
  };
  const toReview = async (p) => {
    await p.locator(primary).click();
    await p.waitForFunction(() => { const b = document.querySelector("#sheet .wz-act .btn.primary"); return b && /Pay and launch/.test(b.textContent) && (!b.disabled || document.querySelector("#sheet .errbox")); }, null, { timeout: 90000 });
  };
  const ids = (p) => p.evaluate(() => [...document.querySelectorAll("#sheet .wz-rv dd .wz-id")].map((e) => e.textContent));

  try {
    if (!spend && !recover) for (const w of widths) {
      const p = await open(w), tag = (mainnet ? "mainnet-" : "") + w;
      await connect(p); step(w + "px: " + (mainnet ? "mainnet-shaped" : "wallet " + (await p.evaluate(() => window.Veil.wallet.address)).slice(0, 16) + "…"));
      await toCreate(p); await snap(p, tag + "-01-create");
      let t = await check(p, "Create", w + " create");
      for (const want of [/Coin/, /Pool/, /Single NFT \(NFA\)/, /NFT collection/, /Holder NFT/, /no limit/i, /nobody approves/]) if (!want.test(t)) fail("the Create step does not say " + want);
      await kind(p, 0); await fillCoin(p, "Preview Coin", "PRV"); await snap(p, tag + "-02-configure-coin", true);
      t = await check(p, "Configure", w + " coin");
      if (!/16×/.test(t) || !/Two wallet confirmations/.test(t)) fail("coin preview: " + t.slice(0, 200));
      if (!stub && !/Unavailable in this build/.test(t)) fail("with no V.profile the logo and links are not said to be unavailable");
      if (mainnet && (!/Priced in DERO/.test(t) || (await p.locator("#sheet [role=radiogroup] >> text=DERO").count()))) fail("mainnet coin: DERO is not the only quote");
      // errors are tied to their field and say how to fix it; the first is focused
      await p.fill("#sheet input[name=ticker]", "PR V"); await p.locator(primary).click(); await sleep(400);
      const tied = await p.evaluate(() => { const i = document.querySelector("#sheet input[name=ticker]"); return { inv: i.getAttribute("aria-invalid"), said: (i.getAttribute("aria-describedby") || "").split(" ").map((d) => (document.getElementById(d) || {}).textContent || "").join(" "), focus: document.activeElement === i }; });
      if (tied.inv !== "true" || !/remove space/.test(tied.said) || !tied.focus) fail("ticker error not tied to its field: " + JSON.stringify(tied));
      await snap(p, tag + "-03-configure-error");
      await p.fill("#sheet input[name=ticker]", "PRV"); await toReview(p); await snap(p, tag + "-04-review", true);
      t = await check(p, "Review", w + " review");
      // innerText follows text-transform, so a heading styled in capitals reads in capitals: compare without case
      for (const want of [/Two wallet confirmations/i, /registration fee/i, /Total/i, /Ring size 2/i, /Graduates at/i, /Treasury/i]) if (!want.test(t)) fail("review lacks " + want + ": " + t.slice(0, 160));
      if (mainnet) { const dis = await p.locator(primary).isDisabled(); if (!dis || !/mainnet/i.test(t)) fail("a mainnet-shaped build reading a simulator node would pay: " + t.slice(-240)); }
      else if (!/≈ [\d.,]+ DERO network fee/.test(t)) fail("no live gas estimate in the review: " + t.slice(0, 400));
      step(w + " review: " + t.replace(/.*Cost/, "Cost").slice(0, 260));
      for (const [i, name, stepName, f] of [[1, "pool", "Configure"], [4, "nft", "Configure"], [2, "nfa", "Configure"], [3, "collection", "Configure"]]) {
        await kind(p, i); await snap(p, tag + "-05-configure-" + name, true);
        const tt = await check(p, stepName, w + " " + name);
        if (name === "collection" && !/not reserved/.test(tt)) fail("the collection step does not say a collection name is not reserved");
        if (name === "nft" && !/Coin to hold|No coin to gate on/i.test(tt)) fail("holder NFT configure: " + tt.slice(0, 120));
      }
      await p.close();
    }

    if (spend || recover) {
      const w = widths[0], p = await open(w), tag = (recover ? "recover-" : "spend-") + w;
      await connect(p); const me = await p.evaluate(() => window.Veil.wallet.address); step("wallet " + wport + ": " + me);
      const tick = (recover ? "RC" : "FL") + String(Date.now() % 100000);
      await kind(p, 0); await fillCoin(p, "Flow Coin " + tick, tick);
      if (stub) {
        await p.fill("#sheet input[name=link-x]", "https://x.com/dmz_flow"); await p.fill("#sheet input[name=link-github]", "https://github.com/NoahMoney/DMZ");
        const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "base64");
        await p.setInputFiles("#sheet input[type=file]", { name: "logo.png", mimeType: "image/png", buffer: png }); await sleep(300);
      }
      await snap(p, tag + "-02-configure", true);
      await toReview(p); await snap(p, tag + "-04-review", true);
      const rt = await text(p); step("review: " + rt.replace(/.*Cost/, "Cost").slice(0, 300));
      if (stub && !/Three wallet confirmations/i.test(rt)) fail("a coin with links does not say three wallet confirmations");
      await p.locator(primary).click(); await p.locator("#sheet .wz-pay").waitFor(); await sleep(2500); await snap(p, tag + "-06-pay");
      await check(p, "Pay", "pay");
      if (recover) {
        await p.waitForSelector("#sheet .errbox", { timeout: 300000 }); await snap(p, tag + "-06b-pay-stopped");
        const at = await ids(p).catch(() => []), scid = await p.evaluate(() => { const r = [...document.querySelectorAll("#sheet .wz-pay .wz-id")].map((e) => e.textContent); return r[r.length - 1]; });
        step("stopped before the registration; installed " + scid + "; closing the page"); await p.close();
        for (const ww of [1280, 430]) {
          const q = await open(ww); if (ww !== 1280) await connect(q); // before the sheet: its backdrop covers the Connect button
          await toCreate(q);
          await q.waitForFunction(() => /not registered yet/.test(document.getElementById("sheet").innerText), null, { timeout: 30000 });
          await snap(q, "recover-" + ww + "-07-create-unfinished", true); await check(q, "Create", ww + " unfinished");
          if (ww === 1280) { await q.close(); continue; }
          await q.getByRole("button", { name: "Register", exact: true }).click();
          await q.waitForSelector("#sheet h2.wz-done", { timeout: 300000 }); await snap(q, "recover-" + ww + "-08-launch");
          const got = await ids(q); console.log("LAUNCHED (recovered) " + tick + " contract=" + got[0] + " registration=" + got[1] + (got[2] ? " profile=" + got[2] : ""));
          if (got[0] !== scid) fail("the recovered registration is for " + got[0] + ", not the install " + scid);
          await q.close();
        }
      } else {
        await p.waitForSelector("#sheet h2.wz-done", { timeout: 300000 }); await snap(p, tag + "-07-launch", true); await check(p, "Launch", "launch");
        const got = await ids(p); console.log("LAUNCHED " + tick + " contract=" + got[0] + " registration=" + got[1] + (got[2] ? " profile=" + got[2] : ""));
        await p.locator(primary).click(); await p.waitForFunction(() => location.hash.startsWith("#token/"), null, { timeout: 60000 }); await sleep(2500);
        await p.screenshot({ path: `${out}/${tag}-08-token.png` });
        const tt = (await p.locator("#screen").innerText()).replace(/\s+/g, " "); if (!tt.includes(tick)) fail("the token page does not show " + tick + ": " + tt.slice(0, 160));
        const reg = await p.evaluate((s) => window.Veil.data.registry.launches.includes(s), got[0]); step("listed in the factory registry: " + reg); if (!reg) fail("not in the registry");
        await p.close();
      }
    }
  } catch (e) { fail(e.stack || String(e)); }
  if (errs.length) fail("page errors:\n" + errs.join("\n"));
  await browser.close();
  console.log(failed ? "UI-LAUNCH FAILED (" + failed + ")" : "UI-LAUNCH OK");
  process.exit(failed ? 1 : 0);
}
