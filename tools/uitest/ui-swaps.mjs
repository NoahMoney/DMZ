// ui-swaps: the UI checks for Deposit & withdraw (app/move.js) and the Wrap screen's limits (app/wrap.js), run by
// ui.mjs with one line in both its normal and --mainnet passes. It opens its own pages at 390 and 1280 px and writes
// sw-<width>-<screen>.png (swm- under --mainnet): read them. Under --mainnet it reshapes the config as ui.mjs does
// (network mainnet, no vault) and checks that deposits are said to be unavailable, with no word of a swap. The tracker
// is drawn for the address that owns the XMR vault's first deposit index, read from the contract, by setting the
// page's wallet address: nothing is connected and nothing is signed.
export default async function swaps({ browser, url, out, step, fail, sleep, mainnet }) {
  const shape = async (route) => { const r = await route.fetch(); await route.fulfill({ status: 200, contentType: "text/javascript; charset=utf-8",
    body: (await r.text()) + '\n;(function (c) { c.network = "mainnet"; delete c.vaults; delete c.vault; delete c.vaultAsset; })(window.VEIL_CONFIG);\n' }); };
  for (const width of [390, 1280]) {
    const p = await browser.newPage({ viewport: { width, height: 900 }, bypassCSP: true }), perr = [], tag = (mainnet ? "swm-" : "sw-") + width + "-";
    p.on("pageerror", (e) => perr.push(e.message));
    p.on("console", (m) => { if (m.type() === "error" && !/net::ERR_|favicon/.test(m.text())) perr.push(m.text()); });
    if (mainnet) await p.route(/\/config\.js(\?.*)?$/, shape);
    await p.goto(url, { waitUntil: "load" }); await p.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
    await p.waitForFunction(() => (window.Veil.live && window.Veil.live.height) > 0, null, { timeout: 30000 }).catch(() => {});
    // the first-run intro can arrive after the click above was tried: it is dismissed before every screen is read
    const go = async (hash, wait = 2500) => { await p.evaluate((x) => { const b = document.getElementById("introOk"); if (b && b.offsetParent) b.click(); location.hash = x; }, hash); await sleep(wait); };
    const txt = async () => (await p.locator("#screen").innerText()).replace(/\s+/g, " ");
    const shot = (n) => p.screenshot({ path: `${out}/${tag}${n}.png`, fullPage: true });
    // every control at least 40 px tall, and the page never wider than the screen
    const targets = async (where) => {
      const small = await p.evaluate(() => [...document.querySelectorAll("#screen .swp a.btn, #screen .swp button, #screen .swp summary, #screen .swp input, #screen .swp select, #screen .swp-pick, #screen .swp-item-h, #screen .swp-crumb a")]
        .filter((e) => e.offsetParent && e.getBoundingClientRect().height < 39.5).map((e) => (e.getAttribute("aria-label") || e.textContent || e.tagName).trim().slice(0, 40) + " (" + Math.round(e.getBoundingClientRect().height) + "px)"));
      if (small.length) fail(where + " at " + width + "px has controls under 40 px: " + small.join("; "));
      const over = await p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      if (over > 1) fail(where + " overflows horizontally by " + over + "px at " + width);
    };
    if (mainnet) {
      for (const [hash, n] of [["#move", "hub"], ["#move/xmr/deposit", "guide-xmr-deposit"], ["#wrap", "wrap"]]) {
        await go(hash); await shot(n); const t = await txt(); step("ui-swaps mainnet " + width + "px " + hash + ": " + t.slice(0, 150));
        if (!/not available on this network/i.test(t)) fail(hash + " under the mainnet shape does not say deposits are unavailable: " + t.slice(0, 200));
        if (/\bswap/i.test(t)) fail(hash + " under the mainnet shape mentions swaps: " + t.slice(0, 300));
        if (await p.locator("#screen input, #screen button").count()) fail(hash + " under the mainnet shape draws a control");
        await targets(hash);
      }
    } else {
      await go("#move"); await shot("hub"); let t = await txt(); step("ui-swaps " + width + "px #move: " + t.slice(0, 160));
      for (const want of [/1:1/, /named signers/, /no size limit per deposit/i, /Choose a coin/]) if (!want.test(t)) fail("#move does not say " + want);
      if (/\bswap/i.test(t)) fail("#move mentions swaps: " + t.slice(0, 300));
      await targets("#move");
      for (const coin of ["xmr", "zec"]) {
        await go("#move/" + coin); await shot("hub-" + coin); t = await txt();
        for (const want of [/Who holds the coins/, /\d+ of the \d+ named signers hold the reserve/, /The cap/, /bonds/, /Choose a direction/]) if (!want.test(t)) fail("#move/" + coin + " does not say " + want + ": " + t.slice(0, 200));
        await targets("#move/" + coin);
        for (const dir of ["deposit", "withdraw"]) {
          await go("#move/" + coin + "/" + dir); await shot("guide-" + coin + "-" + dir); t = await txt();
          const n = await p.locator(".swp-gstep").count(); step("ui-swaps " + width + "px guide " + coin + " " + dir + ": " + n + " steps · " + t.slice(0, 120));
          if (n !== (dir === "deposit" ? 5 : 4)) fail("the " + coin + " " + dir + " guide has " + n + " steps");
          for (const want of dir === "deposit" ? [/Register a deposit index/, /publish your deposit address/, /confirmations/i, /vote Mint/, /Fees and limits/] : [/Burn w(XMR|ZEC)/, /release height/i, /Released/, /Reclaim/, /Fees and limits/])
            if (!want.test(t)) fail("the " + coin + " " + dir + " guide does not say " + want);
          if (!(await p.locator("#screen details.more summary", { hasText: "Technical details" }).count())) fail("the " + coin + " " + dir + " guide has no Technical details");
          if (/\bswap/i.test(t)) fail("the " + coin + " " + dir + " guide mentions swaps");
          await targets("#move/" + coin + "/" + dir);
        }
      }
      // the tracker, for the address that owns the XMR vault's first deposit index, read from the contract
      const owner = await p.evaluate(async () => { const D = window.Veil.data, v = D.vaults()[0]; const r = await window.Veil.node.getSC(v.scid, { keys: ["ua_0"] }); return r.vals.ua_0 ? D.rawToAddr(r.vals.ua_0) : ""; });
      step("ui-swaps tracker: the first vault's index 0 belongs to " + (owner || "nobody"));
      if (owner) {
        await p.evaluate((a) => { window.Veil.wallet.canon = a; }, owner); await go("#move/xmr", 5000); await shot("tracker"); t = await txt();
        const chips = await p.locator(".swp-chip").count(), at = t.search(/Your deposits and withdrawals/);
        step("ui-swaps " + width + "px tracker: " + chips + " stage chips · " + t.slice(at, at + 260));
        if (!/Deposit index #0/.test(t) || !chips) fail("the tracker does not list index 0 with its stages for its owner");
        await targets("#move/xmr (tracker)");
        await p.evaluate(() => { window.Veil.wallet.canon = null; });
      }
      // the screen, not a fixed wait: with thousands of listed markets the first page's registry read can outlast 3.5 s
      await go("#wrap", 1500); await p.waitForFunction(() => /Limits and fees|could not be/i.test(document.getElementById("screen").innerText), null, { timeout: 45000 }).catch(() => {});
      await shot("wrap"); t = await txt(); step("ui-swaps " + width + "px #wrap: " + t.slice(0, 160));
      // the card's label and the hero's eyebrow are set in capitals by CSS, and innerText follows it: match any case
      for (const want of [/Limits and fees/i, /Why there is a cap/, /Step-by-step guide/, /Custodial vault/i]) if (!want.test(t)) fail("#wrap does not say " + want);
      await targets("#wrap");
    }
    if (perr.length) fail("ui-swaps: page errors at " + width + "px: " + perr.join("; "));
    await p.close();
  }
}
