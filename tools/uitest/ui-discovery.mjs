// The discovery checks of the UI pass: Home's sections, Markets' views and rows, the typed search, the Swap hub, the
// Launch screen, the navigation, and the warnings a legitimate market must not wear. ui.mjs calls run() by one line,
// before its own pass, in the normal and the --mainnet mode alike; the pages here are this file's own. It writes
// disc-<sim|mainnet>-<width>-<screen>.png at 390 and 1280: read them.
const WRAPPED = /\bw(XMR|ZEC)\b|wrapped (XMR|ZEC|Monero|Zcash|units|asset)/i;
export async function run({ browser, url, out, step, fail, sleep, mainnet }) {
  const mode = mainnet ? "mainnet" : "sim";
  const shape = async (route) => {
    const r = await route.fetch();
    const body = (await r.text()) + '\n;(function (c) { c.network = "mainnet"; delete c.vaults; delete c.vault; delete c.vaultAsset; })(window.VEIL_CONFIG);\n';
    await route.fulfill({ status: 200, contentType: "text/javascript; charset=utf-8", body });
  };
  for (const width of [390, 1280]) {
    const p = await browser.newPage({ viewport: { width, height: 900 }, bypassCSP: true });
    const errs = [], tag = `${out}/disc-${mode}-${width}`, at = `(${mode}, ${width}px)`;
    p.on("pageerror", (e) => errs.push("pageerror: " + e.message));
    p.on("console", (m) => { if (m.type() === "error") errs.push("console: " + m.text()); });
    if (mainnet) await p.route(/\/config\.js(\?.*)?$/, shape);
    await p.goto(url, { waitUntil: "load" });
    await p.locator("#introOk").click({ timeout: 8000 }).catch(() => {});
    await p.waitForFunction(() => window.Veil && window.Veil.data.lastRefresh > 0, null, { timeout: 60000 }).catch(() => fail("the registry was never read " + at));
    const go = async (h, ms = 1500) => { await p.evaluate((x) => { location.hash = x; }, h); await sleep(ms); };
    const txt = async () => (await p.locator("#screen").innerText()).replace(/\s+/g, " ");
    const common = async (name) => {
      const t = await txt();
      const over = await p.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      if (over > 1) fail(`${name} overflows horizontally by ${over}px ${at}`);
      if (/endors/i.test(t)) fail(`${name} calls something endorsed ${at}`);
      if (/\btrustless\b/i.test(t)) fail(`${name} says trustless ${at}`);
      if (mainnet && WRAPPED.test(t) && !/not offered on this network/i.test(t)) fail(`${name} mentions a wrapped asset on a build with none ${at}: ${t.slice(0, 200)}`);
      // every market named on these screens is named by its pair
      const bad = await p.evaluate(() => [...document.querySelectorAll("#screen .dcard .pair, #screen .mrow.mkt .pair")].map((e) => (e.querySelector("b") || {}).textContent + (e.querySelector(".q") || {}).textContent).filter((t) => !/^\S+ \/ \S+$/.test(t)));
      if (bad.length) fail(`${name}: market labels that are not a pair ${at}: ${bad.slice(0, 5).join(", ")}`);
      // "wrapped · 1:1" only on a market whose base or quote is a vault this build ships, never on a copy
      const marks = await p.evaluate(() => { const D = window.Veil.data, ids = new Set(D.allVaults().map((v) => v.scid)); return [...document.querySelectorAll("#screen [data-scid]")].map((e) => [D.assets.get(e.dataset.scid), !!e.querySelector(".wmark")]).filter(([s]) => s)
        .filter(([s, m]) => m !== (ids.has(s.quote) || (!!s.wrappedBase && ids.has(s.asset))) || (m && s.impersonates)).map(([s]) => s.ticker); });
      if (marks.length) fail(`${name}: the wrapped mark is wrong on ${marks.join(", ")} ${at}`);
      // A legitimate market wears no warning: a row may carry one only for impersonation, a look-alike ticker, the
      // thin half of a split pair or a retired vault
      const warned = await p.evaluate(() => { const D = window.Veil.data; return [...document.querySelectorAll("#screen [data-scid]")].filter((e) => {
        const s = D.assets.get(e.dataset.scid), real = s && (s.impersonates || s.lookalike || (s.splitWith && !s.splitDeepest) || s.quoteLegacy || s.baseLegacy);
        return s && !real && [...e.querySelectorAll(".warn,.pill.bad,.pill.warn")].some((w) => !w.closest(".mdup"));
      }).map((e) => e.dataset.scid.slice(0, 10)); });
      if (warned.length) fail(`${name}: legitimate markets carry a warning ${at}: ${warned.join(", ")}`);
      // touch targets on a phone
      if (width < 500) {
        const small = await p.evaluate(() => [...document.querySelectorAll("#screen .dcard, #screen .mrow.mkt, #screen .mkt-views .chip, #screen .dz .linkbtn, .tabs > .tab, .tabx")].filter((e) => { const r = e.getBoundingClientRect(); return r.width > 0 && r.height > 0 && r.height < 40; }).map((e) => e.className + " " + Math.round(e.getBoundingClientRect().height)));
        if (small.length) fail(`${name}: touch targets under 40px ${at}: ${small.slice(0, 5).join("; ")}`);
      }
      return t;
    };

    // ---- navigation
    {
      const nav = await p.evaluate(() => {
        const vis = (e) => !!e && getComputedStyle(e).display !== "none" && e.getBoundingClientRect().height > 0;
        return { tabs: [...document.querySelectorAll(".tab")].filter(vis).map((t) => t.dataset.tab), wrap: vis(document.getElementById("tabWrap")), more: vis(document.getElementById("tabMore")),
          swaps: [...document.querySelectorAll(".tab")].filter((t) => /^(zswap|xmr|swap)$/.test(t.dataset.tab)).length };
      });
      step(`nav ${at}: ${nav.tabs.join(" ")}${nav.more ? " + More" : ""}`);
      if (!nav.wrap) fail("no Wrap in the navigation " + at);
      if (nav.swaps) fail("the navigation still offers a removed swap " + at);
      for (const want of ["markets", "launch", "nfts", "portfolio"]) if (!nav.tabs.includes(want)) fail(`no ${want} in the navigation ${at}`);
      if (!nav.more) fail("no More in the navigation " + at);
      if (width < 500 && nav.tabs.length > 7) fail(nav.tabs.length + " tabs in the phone bar");
      await p.click("#tabMore"); await sleep(300);
      const open = await p.evaluate(() => ({ exp: document.getElementById("tabMore").getAttribute("aria-expanded"), items: [...document.querySelectorAll("#navMore .tab")].filter((e) => e.getBoundingClientRect().height > 0).map((e) => e.dataset.tab) }));
      await p.screenshot({ path: `${tag}-more.png` });
      step(`More ${at}: ${open.exp} · ${open.items.join(" ")}`);
      if (open.exp !== "true" || !open.items.includes("chat")) fail("More does not open onto Chat " + at + ": " + JSON.stringify(open));
      await p.keyboard.press("Escape"); if (width >= 960) await p.click("#tabMore"); await sleep(200);
    }

    // ---- Home: every section from a real figure, none for a figure the app does not have
    await go("#home");
    await p.screenshot({ path: `${tag}-home.png`, fullPage: true });
    {
      const t = await common("Home");
      // innerText follows CSS text-transform, and section heads are set in capitals: compare in one case
      const heads = await p.evaluate(() => [...document.querySelectorAll(".dz-sec h2")].map((e) => e.textContent.toLowerCase()));
      for (const want of ["Trending", "Highest volume", "Most active", "New", "Biggest movers", "XMR", "ZEC"]) if (!heads.includes(want.toLowerCase())) fail(`Home has no ${want} section ${at}: ${heads.join(", ")}`);
      if (/most viewed/i.test(t)) fail("Home ranks by views: the app has no view count");
      if (/swap board|Monero swap|ZEC offer/i.test(t)) fail("Home still offers a removed swap " + at);
      // XMR and ZEC: the markets priced in the vault's unit, or, with no vault, the notice's words
      const w = await p.evaluate(() => { const D = window.Veil.data, U = window.Veil.ui; return ["XM", "ZC"].map((code) => {
        const v = D.vaults().find((x) => U.netOf(x)[0] === code), e = document.querySelector('[aria-labelledby="dz-' + (code === "XM" ? "xmr" : "zec") + '"]');
        return { code, vault: !!v, text: e ? e.innerText.replace(/\s+/g, " ") : null, strays: e && v ? [...e.querySelectorAll("[data-scid]")].filter((r) => (D.assets.get(r.dataset.scid) || {}).quote !== v.scid).length : 0 };
      }); });
      for (const s of w) {
        if (s.text === null) fail(`no ${s.code} section on Home ${at}`);
        else if (!s.vault && !/not offered on this network/i.test(s.text)) fail(`the ${s.code} section on a build with no vault does not say it is not offered ${at}: ${s.text}`);
        else if (s.vault && /not offered/i.test(s.text)) fail(`the ${s.code} section says not offered on a build with the vault ${at}`);
        if (s.strays) fail(`${s.strays} market(s) in the ${s.code} section are not priced in the vault's unit ${at}`);
      }
      step(`wrapped sections ${at}: ` + w.map((s) => s.code + (s.vault ? " vault" : " no vault") + ": " + String(s.text).slice(0, 90)).join(" | "));
      step(`home ${at}: ` + (await p.evaluate(() => [...document.querySelectorAll(".dz-sec h2")].map((e) => e.textContent + " " + e.closest(".dz-sec").querySelectorAll(".dcard").length).join(" · "))));
      // the movers floor, read back from what is on screen
      const thinMover = await p.evaluate(() => [...document.querySelectorAll('[aria-labelledby="dz-mov"] .dcard')].some((e) => window.Veil.data.assets.get(e.dataset.scid).m.thin));
      if (thinMover) fail("a market under the thin line is listed among the biggest movers " + at);
    }

    // ---- Markets: views, the pair in every row, and a list with no cap drawn a page at a time
    await go("#markets/movers");
    {
      await common("Markets · Movers");
      const bad = await p.evaluate(() => [...document.querySelectorAll("#screen .mrow.mkt")].filter((e) => window.Veil.data.assets.get(e.dataset.scid).m.thin).length);
      if (bad) fail(bad + " thin markets in the Movers view " + at);
    }
    await go("#markets/trending");
    await p.screenshot({ path: `${tag}-markets.png`, fullPage: true });
    await common("Markets");
    {
      // three thousand synthetic markets, cloned from a listed one: the first draw must stay cheap and draw one page,
      // and scrolling must draw more. They are removed again before the next check.
      // D.refresh is held still for the check; a registry read already in flight that rebuilds the list drops the
      // synthetic records, and is detected (kept) and the check run once more, so the count is of the list under test
      const once = () => p.evaluate(async () => {
        const D = window.Veil.data, base = D.list().find((s) => s.m && s.kind !== "nft");
        if (!base) return { skip: true };
        const hold = D.refresh, ids = [];
        D.refresh = () => Promise.resolve();
        try {
          for (let i = 0; i < 3000; i++) { const id = (i + 1).toString(16).padStart(8, "0") + "d15c0".padEnd(56, "0"); ids.push(id); D.assets.set(id, Object.assign({}, base, { scid: id, asset: id, sig: base.sig + ":" + i, splitWith: null, splitDeepest: false, name: base.name + " " + i })); }
          const n = D.list().length;
          const t0 = performance.now(); D.changed(); const ms = performance.now() - t0;
          const first = document.querySelectorAll("#screen .mrow.mkt").length;
          for (let k = 0; k < 6; k++) { window.scrollTo(0, document.body.scrollHeight); await new Promise((res) => setTimeout(res, 250)); }
          const later = document.querySelectorAll("#screen .mrow.mkt").length;
          // the median of three redraws, so one draw slowed by the machine's other work is not read as the list's cost
          const rd = []; for (let k = 0; k < 3; k++) { const t1 = performance.now(); D.changed(); rd.push(performance.now() - t1); }
          const ms2 = rd.sort((a, b) => a - b)[1];
          return { n, ms: Math.round(ms), ms2: Math.round(ms2), first, later, kept: ids.filter((id) => D.assets.has(id)).length };
        } finally { for (const id of ids) D.assets.delete(id); D.refresh = hold; D.changed(); window.scrollTo(0, 0); }
      });
      let r = await once();
      if (!r.skip && r.kept < 3000) { step(`scale ${at}: a registry read landed during the check (${r.kept} of 3000 kept); running it again`); r = await once(); }
      if (r.skip) step("scale: no listed market to clone " + at);
      else {
        step(`scale ${at}: ${r.n} markets · first draw ${r.ms} ms, ${r.first} rows drawn · after scrolling ${r.later} rows · redraw ${r.ms2} ms`);
        if (r.first > 200) fail(`the first draw of ${r.n} markets drew ${r.first} rows: the list is not paged`);
        if (r.later <= r.first) fail("scrolling to the end of the list drew no further rows " + at);
        if (r.ms > 400 || r.ms2 > 400) fail(`a markets draw with ${r.n} markets costs ${Math.max(r.ms, r.ms2)} ms`);
      }
    }

    // ---- search: typed results
    {
      const top = await p.evaluate(() => { const t = window.Veil.data.trending(1)[0]; return t ? t.ticker : null; });
      if (top && width >= 960) {
        await go("#home", 600);
        await p.fill("#gsearch", top); await sleep(500);
        const lines = await p.evaluate(() => [...document.querySelectorAll("#gsr .gsr-i")].map((e) => e.innerText.replace(/\s+/g, " ")));
        await p.screenshot({ path: `${tag}-search.png` });
        step(`search "${top}" ${at}: ${lines.slice(0, 4).join(" | ")}`);
        // a line reads: avatar label, type label, pair, name
        if (!lines.some((l) => /\bMARKET\b.* \/ /i.test(l))) fail("the header search lists no typed market result for " + top);
        await p.keyboard.press("ArrowDown"); await p.keyboard.press("Enter"); await sleep(800);
        if (!/^#token\//.test(await p.evaluate(() => location.hash))) fail("Enter on the first search result did not open the market");
        const nfa = await p.evaluate(() => { const x = [...window.Veil.data.nfas.values()][0]; return x ? x.name : null; });
        if (nfa) { await p.fill("#gsearch", nfa); await sleep(500); const l = await p.evaluate(() => [...document.querySelectorAll("#gsr .gsr-i")].map((e) => e.innerText)); if (!l.some((x) => /^NFA/i.test(x))) fail("the search does not find the NFA " + nfa); }
        await p.fill("#gsearch", ""); await p.keyboard.press("Escape");
      } else if (top) {
        await go("#markets", 800);
        await p.fill("#screen input.search", top); await sleep(600);
        const n = await p.evaluate(() => document.querySelectorAll("#screen .mrow.mkt").length);
        await p.screenshot({ path: `${tag}-search.png`, fullPage: true });
        step(`markets search "${top}" ${at}: ${n} row(s)`);
        if (!n) fail("the Markets search finds nothing for a listed ticker " + at);
      }
    }

    // ---- the Launch screen
    await go("#launch");
    await p.screenshot({ path: `${tag}-launch.png`, fullPage: true });
    { const t = await common("Launch"); if (!/Launch a token/.test(t)) fail("the Launch screen offers no launch " + at); }

    const bad = errs.filter((e) => !/favicon|net::ERR_/.test(e));
    if (bad.length) fail(`console errors on the discovery screens ${at}:\n` + bad.join("\n"));
    await p.close();
  }
}
