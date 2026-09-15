// DMZ nfts.js — the NFTs tab: the Founders Pass (mint, register to earn, per-asset claims, payout addresses,
// built-in marketplace, public accounting), then NFAs (app/nfas.js: assets priced in XMR, ZEC or DERO), then the
// holder-NFT collections. Every number is read from the pass contract; nothing is estimated.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, { h, render, avatar, router, button, kv, openSheet, closeSheet, toast } = U;
  const me = () => V.wallet.canon, Z = 0n, DERO = "0".repeat(64);
  const N = (x) => Number(x);
  const dero = (x, d = 2) => A.fmtDero(x, d) + " DERO";
  // an amount in a pass asset, in that asset; a quote whose scale the app does not know is shown in raw units
  const amt = (a, x) => (a.dec === null ? x + " units of " + a.sym + " (scale unknown)" : A.fmtUnits(x, a.dec, a.id === DERO ? 5 : 4) + " " + a.sym);
  const stat = (l, v) => h("div", { class: "stat" }, h("div", { class: "l" }, l), h("div", { class: "v num" }, v));
  let mine = null, mineAt = 0, mineFor = "";

  async function loadMine(force) {
    const f = D.founders; if (!f || !V.wallet.connected) { mine = null; return; }
    if (!force && mine && mineFor === me() + f.sig && Date.now() - mineAt < 30000) return;
    try {
      // a pass balance the wallet did not give is unreadable, not zero: it was read as 0 and said "No passes yet"
      const b = await V.wallet.getBalance(f.scid, true).catch(() => null);
      const m = await D.myFounders(me());
      mine = Object.assign({ wallet: b ? V.toBig(b.balance) : null }, m || { units: Z, positions: [], windowed: false, xmr: "", zec: "", listings: [] }); mineAt = Date.now(); mineFor = me() + f.sig;
      mine.sig = [mine.wallet, mine.units, mine.xmr, mine.zec].concat(mine.positions.map((q) => q.p + "/" + q.pc + "/" + q.owed.map((o) => o.amt).join(","))).join(":");
    } catch (e) { mine = null; }
  }
  const tx = async (opts) => { const ok = await V.app.tx(opts); if (ok) { await D.refresh().catch(() => {}); await loadMine(true); D.changed(); } return ok; };

  // Level 1 — the instrument: what the contract has paid its holders, and the one control that joins them.
  function passInstrument(f) {
    const soldOut = f.minted >= f.maxsupply, open = (V.live.height || 0) >= N(f.start) || f.start === Z;
    const d0 = f.assets.find((a) => a.id === DERO);
    return [
      h("div", { class: "hero mt" },
        h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Founders Pass"), U.verified()),
        h("div", { class: "row mt8" }, avatar({ icon: f.icon, ticker: "VFP", scid: f.scid }, "big"),
          h("div", { class: "grow" }, h("h2", null, f.name),
            h("div", { class: "sub" }, N(f.share) / 100 + "% of the fees the treasury claims, each paid in the asset it was taken in, split between registered passes"))),
        h("div", { class: "label mt" }, "Claimed by holders, in DERO"),
        h("div", { class: "big num mt8" }, dero(d0 ? d0.aw : Z)),
        h("div", { class: "row mt" }, mintButton(f, soldOut, open)),
        h("div", { class: "note mt8" }, soldOut ? "Every pass is minted. The marketplace below is the only way in."
          : !open ? "Minting opens at block " + f.start + "."
          : (f.maxsupply - f.minted) + " of " + f.maxsupply + " left at " + dero(f.price) + " each.")),
      // no image is fetched from the creator's host: the app makes no request but to your node and your wallet
      h("div", { class: "stats4 mt" }, stat("Minted", f.minted + " / " + f.maxsupply), stat("Mint price", dero(f.price)), stat("Share", N(f.share) / 100 + "%"), stat("Fee assets", String(f.assets.length))),
      f.descr ? h("div", { class: "note mt8" }, f.descr) : null,
    ];
  }
  function mintButton(f, soldOut, open) {
    let n = 1; const label = () => "Mint " + n + " for " + dero(BigInt(n) * f.price);
    const btn = button(label(), "primary grow", () => tx({ scid: f.scid, entry: "Mint", args: [["n", "U", BigInt(n)]], dero: BigInt(n) * f.price, fee: 4000, describe: "Mint " + n + " pass" + (n > 1 ? "es" : ""), outcome: { scid: f.scid, expect: "up" }, probe: { scid: f.scid, keys: ["minted"] } }), { disabled: soldOut || !open });
    const step = (d) => { n = Math.max(1, Math.min(N(f.maxsupply - f.minted) || 1, n + d)); btn.textContent = label(); };
    return [h("div", { class: "seg" }, h("button", { type: "button", onclick: () => step(-1), "aria-label": "fewer" }, "−"), h("button", { type: "button", onclick: () => step(1), "aria-label": "more" }, "+")), btn];
  }

  // Level 2 — your own position in the pass: readings, then each position with a Claim per asset it has earned in.
  function minePanel(f) {
    if (!V.wallet.connected) return h("div", { class: "card mt" }, h("div", { class: "between" }, h("span", { class: "sub" }, "Connect a wallet to mint, register and trade passes."), button("Connect", "small", () => V.app.connectWallet())));
    if (!mine) return h("div", { class: "card mt" }, h("div", { class: "label" }, "Your passes"), h("div", { class: "sub mt8" }, "Reading your passes…"));
    const tot = new Map(); for (const q of mine.positions) for (const o of q.owed) { const t = tot.get(o.asset.id) || { a: o.asset, x: Z }; t.x += o.amt; tot.set(o.asset.id, t); }
    return h("div", { class: "card mt" }, h("div", { class: "label" }, "Your passes"),
      h("div", { class: "row mt8", style: "gap:0;flex-wrap:wrap" }, stat("In wallet", mine.wallet === null ? "unreadable" : String(mine.wallet)), stat("Registered", String(mine.units)), stat("To claim", tot.size ? [...tot.values()].map((t) => amt(t.a, t.x)).join(" · ") : "nothing")),
      h("div", { class: "note mt8" }, mine.units > Z ? "Registered passes earn from every deposit made while they are registered, each in the asset deposited. Claim pays a position's owner what it has earned in one asset; anyone may send it, and what a withdrawn position earned stays claimable."
        : mine.wallet === null ? "Your wallet did not answer for its pass balance, so nothing that needs it is offered. Refresh to ask again." : mine.wallet > Z ? "Passes in your wallet earn nothing until you register them here (balances are private on DERO, so the contract cannot see them). Withdraw any time." : "No passes registered."),
      mine.wallet > Z ? h("div", { class: "row mt" }, button("Register " + mine.wallet, "primary", () => tx({ scid: f.scid, entry: "Register", args: [], asset: { scid: f.scid, amount: mine.wallet }, describe: "Register passes", probe: { scid: f.scid, keys: ["hst"] } }))) : null,
      mine.positions.length ? h("div", { class: "mt" }, mine.positions.map((q) => positionRow(f, q))) : null,
      mine.windowed ? h("div", { class: "note mt8" }, "Only the pass's newest 100 positions are read here.") : null,
      payoutForm(f));
  }
  function positionRow(f, q) {
    return h("div", { class: "kv" },
      h("div", { class: "grow", style: "min-width:0" }, h("b", null, "Position " + q.p + " · " + q.u + " pass" + (q.u === 1n ? "" : "es")),
        h("div", { class: "sub tiny" }, q.pc === Z ? "registered" : "withdrawn; what it earned stays claimable")),
      h("div", { class: "row tight", style: "flex-wrap:wrap;justify-content:flex-end" },
        q.owed.map((o) => button("Claim " + amt(o.asset, o.amt), "small", () => tx({ scid: f.scid, entry: "Claim", args: [["p", "U", q.p], ["i", "U", o.asset.i], ["ka", "U", o.ka], ["kb", "U", o.kb]], describe: "Claim " + o.asset.sym, probe: { scid: f.scid, keys: ["aw_" + o.asset.i] } }))),
        q.pc === Z ? button("Withdraw", "small ghost", () => withdrawSheet(f, q)) : null));
  }
  function withdrawSheet(f, q) {
    const inp = h("input", { inputmode: "numeric", value: String(q.u) });
    openSheet(h("h2", null, "Withdraw passes"), h("div", { class: "stack mt" }, h("label", { class: "field" }, "How many of position " + q.p + "'s " + q.u, inp)),
      h("div", { class: "note mt" }, "Withdrawing closes the position: what it has earned stays claimable, by anyone, for ever. Any passes you leave form a new position that earns from the next deposit on."),
      button("Withdraw", "primary wide mt", () => { const u = BigInt(Math.max(0, parseInt(inp.value || "0", 10) || 0)); if (!u || u > q.u) return toast("Enter 1 to " + q.u, "err"); closeSheet(); tx({ scid: f.scid, entry: "Withdraw", args: [["p", "U", q.p], ["u", "U", u]], describe: "Withdraw passes", probe: { scid: f.scid, keys: ["hst"] } }); }));
  }
  // The XMR and ZEC payout addresses: shown to a registered holder, and to a former one who still has one stored.
  // Each address is checked by founders.js (V.payout) before Store is offered: a Monero standard address or
  // subaddress of this build's network with a good checksum, and a TRANSPARENT Zcash address of this build's network,
  // because the treasury's bot can pay only a transparent one. Until it passes, the button stays off and says why.
  function payoutForm(f) {
    if (mine.units === Z && !mine.xmr && !mine.zec) return null;
    const net = D.cfg.network, main = net === "mainnet";
    const one = (label, entry, key, cur, hint, check) => {
      const why = h("div", { class: "sub tiny mt8", role: "status" }, "Store is offered once the address checks out.");
      const btn = button(cur ? "Replace" : "Store", "small", async () => { const v = inp.value.trim(), r = await check(v, net); if (!r.ok) return toast(r.why, "err", 8000); tx({ scid: f.scid, entry, args: [["addr", "S", v]], describe: (cur ? "Replace " : "Store ") + label + " address", probe: { scid: f.scid, keys: [key + me()] } }); }, { disabled: true });
      const inp = h("input", { placeholder: hint, autocomplete: "off", spellcheck: "false", "aria-label": label + " payout address", style: "flex:1;min-width:0" });
      inp.oninput = async () => { const v = inp.value.trim(), r = v ? await check(v, net) : null; if (inp.value.trim() !== v) return;
        btn.disabled = !(r && r.ok); why.textContent = !r ? "Store is offered once the address checks out." : r.ok ? r.warn || "A valid address for " + net + "." : r.why; why.style.color = r && !r.ok ? "var(--down)" : ""; };
      return h("div", { class: "mt" }, h("div", { class: "between" }, h("span", { class: "label" }, label),
        cur ? button("Clear", "small ghost", () => tx({ scid: f.scid, entry, args: [["addr", "S", ""]], describe: "Clear " + label + " address", probe: { scid: f.scid, keys: [key + me()] } })) : null),
        h("div", { class: "mono dim2 mt8", style: "word-break:break-all" }, cur || "None stored."),
        mine.units > Z ? [h("div", { class: "row mt8" }, inp, btn), why] : null);
    };
    return h("div", { class: "mt" }, h("div", { class: "label" }, "XMR and ZEC payout addresses"),
      h("div", { class: "note mt8" }, "Your share of the fees taken in XMR and ZEC (the 0.5% swap fee the maker of each completed swap pays) is paid by the treasury's bot, off the DERO chain, to the addresses stored here, split between the passes registered when each period ends. The bot makes each payment; no contract forces it, and a pass with no address here keeps its part owed until it stores one. ",
        h("b", null, "An address stored here is public on the DERO chain for good."), " So store a fresh Monero subaddress that you use for nothing else, not your primary address, and a fresh Zcash transparent address: the bot can pay only a transparent one. " +
        "The contract checks only the length and the characters; this page also checks the checksum and that the address is for " + net + ". Only a registered holder can store one; you can always clear your own."),
      one("Monero", "SetXmr", "xm_", mine.xmr, main ? "a fresh subaddress: 95 characters, starting 8" : "a fresh stagenet or testnet subaddress: 95 characters", V.payout.xmr),
      one("Zcash (transparent)", "SetZec", "zc_", mine.zec, main ? "a transparent address: t1… or t3…" : "a transparent address: tm… or t2…", V.payout.zec));
  }

  // Level 3 — listings are rows on the ground, hairline separated: a price, who is selling, one control.
  function marketplace(f) {
    const canSell = mine && mine.wallet > Z;
    const rows = f.listings.slice(0, 50).map((l) => {
      const own = l.seller === me();
      return h("div", { class: "kv" },
        h("div", { class: "row tight grow", style: "min-width:0" }, avatar(l.seller, "sm"),
          h("div", { class: "grow ellipsis" }, h("b", { class: "num" }, dero(l.price)),
            h("div", { class: "mono dim2 ellipsis" }, l.units + " × pass · " + D.displayName(l.seller)))),
        own ? button("Unlist", "small ghost", () => tx({ scid: f.scid, entry: "Unlist", args: [["id", "U", BigInt(l.id)]], describe: "Unlist", probe: { scid: f.scid, keys: ["ls_" + l.id] } })) : button("Buy", "small", () => buySheet(f, l)));
    });
    return h("div", null, U.sectionHead("sparkle", "Marketplace", f.la + " listing" + (f.la === 1n ? "" : "s") + " · " + N(f.mfee) / 100 + "% of every sale to the registered passes", canSell ? button("Sell", "small", () => listSheet(f)) : null),
      rows.length ? h("div", { class: "mt8" }, rows)
        : h("div", { class: "mt" }, U.emptyState("gem", "No passes listed", "A listing sells at a fixed price and sits in the contract until it is bought or unlisted. Passes also move wallet-to-wallet like any DERO asset.", canSell ? button("Sell passes", "small primary", () => listSheet(f)) : null)));
  }
  function buySheet(f, l) {
    let n = 1n; const max = l.units; const out = h("div", { class: "outline mt" }); const btn = button("Buy", "primary wide mt", () => { closeSheet(); tx({ scid: f.scid, entry: "Buy", args: [["id", "U", BigInt(l.id)], ["n", "U", n]], dero: n * l.price, describe: "Buy " + n + " pass" + (n > 1n ? "es" : ""), outcome: { scid: f.scid, expect: "up" } }); });
    const draw = () => render(out, h("div", { class: "l" }, "You pay"), h("div", { class: "v num" }, dero(n * l.price)), h("div", { class: "note mt8" }, n + " of " + max + " · " + N(f.mfee) / 100 + "% of the price goes to the registered passes, the rest to the seller; with no pass registered, the seller receives all of it."));
    openSheet(h("h2", null, "Buy passes"), h("div", { class: "row mt" }, h("div", { class: "seg" }, h("button", { type: "button", onclick: () => { if (n > 1n) n--; draw(); } }, "−"), h("button", { type: "button", onclick: () => { if (n < max) n++; draw(); } }, "+")), h("span", { class: "sub" }, "quantity")), out, btn); draw();
  }
  function listSheet(f) {
    const units = h("input", { inputmode: "numeric", placeholder: "passes", value: "1" }), price = h("input", { inputmode: "decimal", placeholder: "price per pass in DERO" });
    openSheet(h("h2", null, "Sell passes"), h("div", { class: "stack mt" }, h("label", { class: "field" }, "How many (you have " + (mine ? mine.wallet : 0n) + " in your wallet)", units), h("label", { class: "field" }, "Price per pass (DERO)", price)),
      h("div", { class: "note mt" }, "Listed passes sit in the contract until sold or unlisted; they do not earn while listed. " + N(f.mfee) / 100 + "% of each sale goes to the registered passes."),
      button("List for sale", "primary wide mt", () => { const u = BigInt(Math.max(0, parseInt(units.value || "0", 10))), p = A.parseUnits(price.value, 5n); if (!u || !p) return toast("Enter a quantity and a price", "err"); if (mine && u > mine.wallet) return toast("You only have " + mine.wallet + " in your wallet", "err"); closeSheet(); tx({ scid: f.scid, entry: "List", args: [["price", "U", p]], asset: { scid: f.scid, amount: u }, describe: "List " + u + " pass" + (u > 1n ? "es" : ""), outcome: { scid: f.scid, expect: "down" } }); }));
  }
  // the ledger, per asset. No shield and no badge: the numbers are the assurance, and the contract id sits under them.
  function accounting(f) {
    return h("div", { class: "card mt" }, h("div", { class: "between" }, h("b", null, "Public accounting"), h("span", { class: "label" }, "On chain")),
      h("div", { class: "note mt8 mb" }, "The share is fixed in the contract and cannot be changed: " + N(f.share) / 100 + "%. It is a share of the trading fees the treasury claims from DMZ pools and launches, deposited here in the asset each fee was taken in, never converted to DERO; the market fee on pass sales is added in DERO. " +
        "The treasury's own bot claims the fees and makes every deposit, and no contract forces it to. A deposit is split at once between the passes registered when it is made; while none is registered, the bot holds the fees and deposits them after one registers. " +
        "The XMR and ZEC share is the bot's to pay, to the addresses holders store. Fees from NFA sales reach no holder. Only DERO and the tokens with a DMZ market are listed: a token anyone else deposits is not a fee asset."),
      f.assets.map((a) => kv(a.sym, "deposited " + amt(a, a.ad) + " · claimed " + amt(a, a.aw) + " · owed " + amt(a, a.ap) + (a.au > Z ? " · carried " + amt(a, a.au) : ""))),
      kv("Market fees on pass sales", dero(f.mdep)), kv("Registered passes", f.hst + " of " + f.minted + " minted"),
      kv("Contract", h("button", { class: "mono", type: "button", title: "Copy the contract id", onclick: () => { navigator.clipboard && navigator.clipboard.writeText(f.scid).then(() => toast("Contract id copied", "ok")); } }, V.shortAddr(f.scid))));
  }
  // a collection is its images: a grid whose gaps are the hairlines, with the technical metadata beneath in mono
  function collections() {
    const nfts = [...D.nfts.values()];
    const tile = (n) => {
      return h("button", { class: "card", type: "button", style: "padding:0;border:0;border-radius:0;overflow:hidden;outline:1px solid var(--edge)", onclick: () => router.go("nft", n.scid) },
        h("div", { style: "aspect-ratio:1;display:grid;place-items:center" }, avatar({ icon: n.icon, ticker: "NFT", scid: n.scid }, "big")),
        h("div", { style: "padding:10px;text-align:left" },
          h("b", { class: "ellipsis", style: "display:block" }, n.name),
          h("div", { class: "mono dim2 ellipsis", style: "margin-top:5px" }, n.minted + "/" + n.maxsupply + " · " + A.fmtDero(n.price, 1) + " DERO"),
          h("div", { class: "mono dim2 ellipsis" }, "HOLD " + A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker)));
    };
    return [U.sectionHead("gem", "Holder NFTs", "Editions earned by holding a token for a number of blocks", nfts.length ? button("Create", "small", () => V.forms.nft()) : null),
      nfts.length ? h("div", { class: "mt", style: "display:grid;grid-template-columns:repeat(auto-fill,minmax(148px,1fr));gap:1px" }, nfts.map(tile))
        : D.lastRefresh ? U.emptyState("gem", "No collections yet", "A collection mints one edition to every address that has held a token for a set number of blocks. Anyone can create one.", button("Create a collection", "small", () => V.forms.nft()))
          : U.emptyState("gem", "Holder NFTs", "Reading the registry…")];
  }

  router.on("nfts", (el) => {
    let lastKey = null;
    const draw = () => {
      const f = D.founders;
      // re-render only when something on this page changed (the rest of the app refreshes every block)
      const key = [f ? f.sig : "-", mine ? mine.sig : "-", V.wallet.connected, D.nfts.size, D.nfas.size, V.nfas.key(), V.settings.images, D.lastRefresh > 0].join("|");
      if (key === lastKey) return; lastKey = key;
      render(el, h("h1", { class: "mt" }, "NFTs"),
        h("div", { class: "sub mb" }, "Assets priced in " + (D.allVaults().length ? "XMR and ZEC" : "DERO") + ", each sold or auctioned by its own contract; the Founders Pass, whose registered passes share the fees the treasury claims, each in the asset it was taken in; and holder NFTs, earned by holding a token."),
        f ? h("div", { class: "nft-grid" }, h("div", null, passInstrument(f), minePanel(f), marketplace(f)), h("div", null, accounting(f)))
          : h("div", { class: "card mt" }, h("div", { class: "label" }, "Founders Pass"), h("div", { class: "sub mt8" }, D.cfg.founders ? (D.lastRefresh ? "The contract at the configured id did not verify as a Founders Pass v2, so nothing about it is shown." : "Reading the pass contract…") : "No Founders Pass configured for this deployment.")),
        V.nfas.section(), collections());
      if (f && V.wallet.connected) loadMine().then(() => { if (router.current && router.current.name === "nfts" && mine) draw(); });
    };
    mine = null; lastKey = null; draw(); D.onChange(() => router.current && router.current.name === "nfts" && draw());
  });
  V.nfts = { loadMine, get mine() { return mine; } };
})();
