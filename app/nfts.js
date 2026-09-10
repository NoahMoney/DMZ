// DMZ nfts.js — the NFTs tab: the Founders Pass (mint, register to earn, rewards, built-in marketplace, public
// accounting), then NFAs (app/nfas.js: assets priced in XMR, ZEC or DERO), then the holder-NFT collections. Every
// number is read from the pass contract; nothing is estimated.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, { h, render, avatar, router, button, kv, openSheet, closeSheet, toast } = U;
  const me = () => V.wallet.canon, Z = 0n;
  const N = (x) => Number(x);
  const dero = (x, d = 2) => A.fmtDero(x, d) + " DERO";
  const stat = (l, v) => h("div", { class: "stat" }, h("div", { class: "l" }, l), h("div", { class: "v num" }, v));
  let mine = null, mineAt = 0, mineFor = "";

  async function loadMine(force) {
    const f = D.founders; if (!f || !V.wallet.connected) { mine = null; return; }
    if (!force && mine && mineFor === me() + f.sig && Date.now() - mineAt < 30000) return;
    try {
      const b = await V.wallet.getBalance(f.scid, true).catch(() => ({ balance: 0 }));
      const m = await D.myFounders(me());
      mine = Object.assign({ wallet: BigInt(b.balance || 0) }, m || { units: Z, pending: Z, listings: [] }); mineAt = Date.now(); mineFor = me() + f.sig;
    } catch (e) { mine = null; }
  }
  const tx = async (opts) => { const ok = await V.app.tx(opts); if (ok) { await D.refresh().catch(() => {}); await loadMine(true); D.changed(); } return ok; };

  // Level 1 — the instrument: what the contract has paid its holders, and the one control that joins them.
  function passInstrument(f) {
    const soldOut = f.minted >= f.maxsupply, open = (V.live.height || 0) >= N(f.start) || f.start === Z;
    const img = U.safeUrl(f.file);
    return [
      h("div", { class: "hero mt" },
        h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Founders Pass"), U.verified()),
        h("div", { class: "row mt8" }, avatar({ icon: f.icon, ticker: "VFP", scid: f.scid }, "big"),
          h("div", { class: "grow" }, h("h2", null, f.name),
            h("div", { class: "sub" }, N(f.share) / 100 + "% of the DERO trading fees the treasury claims, split between registered holders"))),
        h("div", { class: "label mt" }, "Paid to holders"),
        h("div", { class: "big num mt8" }, dero(f.paid)),
        h("div", { class: "row mt" }, mintButton(f, soldOut, open)),
        h("div", { class: "note mt8" }, soldOut ? "Every pass is minted. The marketplace below is the only way in."
          : !open ? "Minting opens at block " + f.start + "."
          : (f.maxsupply - f.minted) + " of " + f.maxsupply + " left at " + dero(f.price) + " each.")),
      img && V.settings.images ? h("img", { src: img, alt: "", referrerpolicy: "no-referrer", loading: "lazy", style: "width:100%;display:block;margin-top:14px;border:1px solid var(--edge)" }) : null,
      h("div", { class: "stats4 mt" }, stat("Minted", f.minted + " / " + f.maxsupply), stat("Mint price", dero(f.price)), stat("Holders", String(f.hn)), stat("Registered", f.hst + " / " + f.minted)),
      f.descr ? h("div", { class: "note mt8" }, f.descr) : null,
    ];
  }
  function mintButton(f, soldOut, open) {
    let n = 1; const label = () => "Mint " + n + " for " + dero(BigInt(n) * f.price);
    const btn = button(label(), "primary grow", () => tx({ scid: f.scid, entry: "Mint", args: [["n", "U", BigInt(n)]], dero: BigInt(n) * f.price, fee: 4000, describe: "Mint " + n + " pass" + (n > 1 ? "es" : ""), probe: { scid: f.scid, keys: ["minted"] } }), { disabled: soldOut || !open });
    const step = (d) => { n = Math.max(1, Math.min(N(f.maxsupply - f.minted) || 1, n + d)); btn.textContent = label(); };
    return [h("div", { class: "seg" }, h("button", { type: "button", onclick: () => step(-1), "aria-label": "fewer" }, "−"), h("button", { type: "button", onclick: () => step(1), "aria-label": "more" }, "+")), btn];
  }

  // Level 2 — your own position in the pass: three readings and the actions they enable, with no box inside a box.
  function minePanel(f) {
    if (!V.wallet.connected) return h("div", { class: "card mt" }, h("div", { class: "between" }, h("span", { class: "sub" }, "Connect a wallet to mint, register and trade passes."), button("Connect", "small", () => V.app.connectWallet())));
    if (!mine) return h("div", { class: "card mt" }, h("div", { class: "label" }, "Your passes"), h("div", { class: "sub mt8" }, "Reading your passes…"));
    return h("div", { class: "card mt" }, h("div", { class: "label" }, "Your passes"),
      h("div", { class: "row mt8", style: "gap:0" }, stat("In wallet", String(mine.wallet)), stat("Registered", String(mine.units)), stat("Pending", A.fmtDero(mine.pending, 4))),
      h("div", { class: "note mt8" }, mine.units > Z ? "Registered passes earn from every deposit. Pending rewards are sent to your wallet by Claim, or automatically when the treasury pushes a batch." : mine.wallet > Z ? "Passes in your wallet earn nothing until you register them here (balances are private on DERO, so the contract cannot see them). Withdraw any time." : "No passes yet."),
      h("div", { class: "row mt", style: "flex-wrap:wrap" },
        mine.wallet > Z ? button("Register " + mine.wallet, "primary", () => tx({ scid: f.scid, entry: "Register", args: [], asset: { scid: f.scid, amount: mine.wallet }, describe: "Register passes", probe: { scid: f.scid, keys: ["hst"] } })) : null,
        mine.pending > Z ? button("Claim " + A.fmtDero(mine.pending, 4), "", () => tx({ scid: f.scid, entry: "Claim", args: [], describe: "Claim rewards" })) : null,
        mine.units > Z ? button("Withdraw " + mine.units, "", () => tx({ scid: f.scid, entry: "Withdraw", args: [["u", "U", mine.units]], describe: "Withdraw passes" })) : null));
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
        own ? button("Unlist", "small ghost", () => tx({ scid: f.scid, entry: "Unlist", args: [["id", "U", BigInt(l.id)]], describe: "Unlist" })) : button("Buy", "small", () => buySheet(f, l)));
    });
    return h("div", null, U.sectionHead("sparkle", "Marketplace", f.la + " listing" + (f.la === 1n ? "" : "s") + " · " + N(f.mfee) / 100 + "% of every sale to holders", canSell ? button("Sell", "small", () => listSheet(f)) : null),
      rows.length ? h("div", { class: "mt8" }, rows)
        : h("div", { class: "mt" }, U.emptyState("gem", "No passes listed", "A listing sells at a fixed price and sits in the contract until it is bought or unlisted. Passes also move wallet-to-wallet like any DERO asset.", canSell ? button("Sell passes", "small primary", () => listSheet(f)) : null)));
  }
  function buySheet(f, l) {
    let n = 1n; const max = l.units; const out = h("div", { class: "outline mt" }); const btn = button("Buy", "primary wide mt", () => { closeSheet(); tx({ scid: f.scid, entry: "Buy", args: [["id", "U", BigInt(l.id)], ["n", "U", n]], dero: n * l.price, describe: "Buy " + n + " pass" + (n > 1n ? "es" : "") }); });
    const draw = () => render(out, h("div", { class: "l" }, "You pay"), h("div", { class: "v num" }, dero(n * l.price)), h("div", { class: "note mt8" }, n + " of " + max + " · " + N(f.mfee) / 100 + "% of the price goes to registered holders, the rest to the seller."));
    openSheet(h("h2", null, "Buy passes"), h("div", { class: "row mt" }, h("div", { class: "seg" }, h("button", { type: "button", onclick: () => { if (n > 1n) n--; draw(); } }, "−"), h("button", { type: "button", onclick: () => { if (n < max) n++; draw(); } }, "+")), h("span", { class: "sub" }, "quantity")), out, btn); draw();
  }
  function listSheet(f) {
    const units = h("input", { inputmode: "numeric", placeholder: "passes", value: "1" }), price = h("input", { inputmode: "decimal", placeholder: "price per pass in DERO" });
    openSheet(h("h2", null, "Sell passes"), h("div", { class: "stack mt" }, h("label", { class: "field" }, "How many (you have " + (mine ? mine.wallet : 0n) + " in your wallet)", units), h("label", { class: "field" }, "Price per pass (DERO)", price)),
      h("div", { class: "note mt" }, "Listed passes sit in the contract until sold or unlisted; they do not earn while listed. " + N(f.mfee) / 100 + "% of each sale goes to registered holders."),
      button("List for sale", "primary wide mt", () => { const u = BigInt(Math.max(0, parseInt(units.value || "0", 10))), p = A.parseUnits(price.value, 5n); if (!u || !p) return toast("Enter a quantity and a price", "err"); if (mine && u > mine.wallet) return toast("You only have " + mine.wallet + " in your wallet", "err"); closeSheet(); tx({ scid: f.scid, entry: "List", args: [["price", "U", p]], asset: { scid: f.scid, amount: u }, describe: "List " + u + " pass" + (u > 1n ? "es" : "") }); }));
  }
  // the ledger. No shield and no badge: the numbers are the assurance, and the contract id sits under them.
  function accounting(f) {
    const waiting = f.und, owed = f.rpool;
    const push = owed > Z && f.hn > Z, release = waiting > Z && f.hst > Z;
    return h("div", { class: "card mt" }, h("div", { class: "between" }, h("b", null, "Public accounting"), h("span", { class: "label" }, "On chain")),
      h("div", { class: "note mt8 mb" }, "The share is fixed in the contract and cannot be changed: " + N(f.share) / 100 + "%. What it is a share of is the DERO trading fees the treasury claims from DMZ pools and launches — the treasury's own bot claims them and deposits here, and no contract forces it to. Fees earned in a wrapped asset, and fees from NFA sales, reach no holder. Deposits, payouts and the registry are all on chain: the treasury cannot pay out less than what it deposited, and anyone can deposit or push payouts."),
      kv("Promised share of claimed DERO fees", N(f.share) / 100 + "%"), kv("Deposited by the treasury and others", dero(f.dep)), kv("Marketplace fees added", dero(f.mdep)), kv("Paid to holders", dero(f.paid)), kv("Owed, not yet pushed", dero(owed)), kv("Waiting for the first registration", dero(waiting)), kv("Registered passes", f.hst + " of " + f.minted + " minted"),
      kv("Contract", h("button", { class: "mono", type: "button", title: "Copy the contract id", onclick: () => { navigator.clipboard && navigator.clipboard.writeText(f.scid).then(() => toast("Contract id copied", "ok")); } }, V.shortAddr(f.scid))),
      push || release ? h("div", { class: "row mt", style: "flex-wrap:wrap" },
        push ? button("Push rewards to holders", "small", () => tx({ scid: f.scid, entry: "PayBatch", args: [["from", "U", 0n], ["n", "U", 15n]], fee: 20000, describe: "Push rewards" })) : null,
        release ? button("Release waiting DERO", "small", () => tx({ scid: f.scid, entry: "Distribute", args: [], describe: "Distribute" })) : null) : null);
  }
  // a collection is its images: a grid whose gaps are the hairlines, with the technical metadata beneath in mono
  function collections() {
    const nfts = [...D.nfts.values()];
    const tile = (n) => {
      const img = U.safeUrl(n.file);
      return h("button", { class: "card", type: "button", style: "padding:0;border:0;border-radius:0;overflow:hidden;outline:1px solid var(--edge)", onclick: () => router.go("nft", n.scid) },
        img && V.settings.images
          ? h("img", { src: img, alt: "", referrerpolicy: "no-referrer", loading: "lazy", style: "width:100%;aspect-ratio:1;object-fit:cover;display:block" })
          : h("div", { style: "aspect-ratio:1;display:grid;place-items:center" }, avatar({ icon: n.icon, ticker: "NFT", scid: n.scid }, "big")),
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
      const key = [f ? f.sig : "-", mine ? mine.wallet + ":" + mine.units + ":" + mine.pending : "-", V.wallet.connected, D.nfts.size, D.nfas.size, V.nfas.key(), V.settings.images, D.lastRefresh > 0].join("|");
      if (key === lastKey) return; lastKey = key;
      render(el, h("h1", { class: "mt" }, "NFTs"),
        h("div", { class: "sub mb" }, "Assets priced in XMR and ZEC, each sold or auctioned by its own contract; the Founders Pass, which shares the DERO fees the treasury claims with its holders; and holder NFTs, earned by holding a token."),
        f ? h("div", { class: "nft-grid" }, h("div", null, passInstrument(f), minePanel(f), marketplace(f)), h("div", null, accounting(f)))
          : h("div", { class: "card mt" }, h("div", { class: "label" }, "Founders Pass"), h("div", { class: "sub mt8" }, D.cfg.founders ? (D.lastRefresh ? "The contract at the configured id did not verify against its template, so nothing about it is shown." : "Reading the pass contract…") : "No Founders Pass configured for this deployment.")),
        V.nfas.section(), collections());
      if (f && V.wallet.connected) loadMine().then(() => { if (router.current && router.current.name === "nfts" && mine) draw(); });
    };
    mine = null; lastKey = null; draw(); D.onChange(() => router.current && router.current.name === "nfts" && draw());
  });
  V.nfts = { loadMine, get mine() { return mine; } };
})();
