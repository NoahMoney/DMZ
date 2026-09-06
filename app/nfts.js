// DMZ nfts.js — the NFTs tab: the Founders Pass (mint, register to earn, rewards, built-in marketplace, public
// accounting) and the holder-NFT collections. Every number is read from the pass contract; nothing is estimated.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, { h, render, avatar, pill, router, button, kv, icon, openSheet, closeSheet, toast } = U;
  const me = () => V.wallet.canon, Z = 0n;
  const N = (x) => Number(x);
  const dero = (x, d = 2) => A.fmtDero(x, d) + " DERO";
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

  function passCard(f) {
    const soldOut = f.minted >= f.maxsupply, open = (V.live.height || 0) >= N(f.start) || f.start === Z;
    const img = U.safeUrl(f.file);
    const stat = (l, v) => h("div", { class: "stat" }, h("div", { class: "l" }, l), h("div", { class: "v num" }, v));
    const ownedRow = !V.wallet.connected ? h("div", { class: "card tight mt" }, h("div", { class: "between" }, h("span", { class: "sub" }, "Connect your wallet to mint, register and trade passes."), button("Connect", "small primary", () => V.app.connectWallet())))
      : !mine ? h("div", { class: "card tight mt" }, h("div", { class: "sub" }, "Reading your passes…"))
      : h("div", { class: "card mt" }, h("div", { class: "eyebrow" }, "Your passes"),
        h("div", { class: "row mt8 stats3" }, stat("In wallet", String(mine.wallet)), stat("Registered", String(mine.units)), stat("Pending", A.fmtDero(mine.pending, 4))),
        h("div", { class: "note mt8" }, mine.units > Z ? "Registered passes earn from every deposit. Pending rewards are sent to your wallet by Claim, or automatically when the treasury pushes a batch." : mine.wallet > Z ? "Passes in your wallet earn nothing until you register them here (balances are private on DERO, so the contract cannot see them). Withdraw any time." : "No passes yet."),
        h("div", { class: "row mt", style: "flex-wrap:wrap" },
          mine.wallet > Z ? button("Register " + mine.wallet, "primary", () => tx({ scid: f.scid, entry: "Register", args: [], asset: { scid: f.scid, amount: mine.wallet }, describe: "Register passes", probe: { scid: f.scid, keys: ["hst"] } })) : null,
          mine.pending > Z ? button("Claim " + A.fmtDero(mine.pending, 4), "buy", () => tx({ scid: f.scid, entry: "Claim", args: [], describe: "Claim rewards" })) : null,
          mine.units > Z ? button("Withdraw " + mine.units, "", () => tx({ scid: f.scid, entry: "Withdraw", args: [["u", "U", mine.units]], describe: "Withdraw passes" })) : null,
          mine.wallet > Z ? button("Sell", "", () => listSheet(f)) : null));
    return h("div", { class: "card hero" },
      h("div", { class: "row" }, avatar({ icon: f.icon, ticker: "VFP", scid: f.scid }, "big"), h("div", { class: "grow" }, h("h2", null, f.name), h("div", { class: "row tight mt8", style: "flex-wrap:wrap" }, U.verified(), pill("Earns " + N(f.share) / 100 + "% of exchange fees", "acc"), soldOut ? pill("Sold out", "warn") : pill(String(f.maxsupply - f.minted) + " left", "up")))),
      img && V.settings.images ? h("img", { src: img, alt: "", referrerpolicy: "no-referrer", loading: "lazy", style: "width:100%;border-radius:18px;margin-top:14px" }) : null,
      f.descr ? h("div", { class: "sub mt" }, f.descr) : null,
      h("div", { class: "grid4 mt" }, stat("Minted", f.minted + " / " + f.maxsupply), stat("Price", A.fmtDero(f.price, 2)), stat("Holders", String(f.hn)), stat("Paid out", A.fmtDero(f.paid, 2))),
      ownedRow,
      h("div", { class: "row mt" }, mintButton(f, soldOut, open)),
      !open ? h("div", { class: "note mt8" }, "Minting opens at block " + f.start + ".") : null);
  }
  function mintButton(f, soldOut, open) {
    let n = 1; const label = () => "Mint " + n + " for " + dero(BigInt(n) * f.price);
    const btn = button(label(), "primary grow", () => tx({ scid: f.scid, entry: "Mint", args: [["n", "U", BigInt(n)]], dero: BigInt(n) * f.price, fee: 4000, describe: "Mint " + n + " pass" + (n > 1 ? "es" : ""), probe: { scid: f.scid, keys: ["minted"] } }), { disabled: soldOut || !open });
    const step = (d) => { n = Math.max(1, Math.min(N(f.maxsupply - f.minted) || 1, n + d)); btn.textContent = label(); };
    return [h("div", { class: "seg" }, h("button", { type: "button", onclick: () => step(-1), "aria-label": "fewer" }, "−"), h("button", { type: "button", onclick: () => step(1), "aria-label": "more" }, "+")), btn];
  }

  function marketplace(f) {
    const rows = f.listings.slice(0, 50).map((l) => {
      const own = l.seller === me();
      return h("div", { class: "card tight row" }, avatar(l.seller, "sm"), h("div", { class: "grow ellipsis" }, h("b", { class: "num" }, dero(l.price) + " each"), h("div", { class: "sub ellipsis" }, l.units + " pass" + (l.units > 1n ? "es" : "") + " · " + D.displayName(l.seller))),
        own ? button("Unlist", "small", () => tx({ scid: f.scid, entry: "Unlist", args: [["id", "U", BigInt(l.id)]], describe: "Unlist" })) : button("Buy", "small buy", () => buySheet(f, l)));
    });
    return h("div", null, U.sectionHead("sparkle", "Marketplace", f.la + " listing" + (f.la === 1n ? "" : "s") + " · " + N(f.mfee) / 100 + "% of every sale to holders"),
      rows.length ? h("div", { class: "list mt" }, rows) : h("div", { class: "mt" }, U.emptyState("gem", "No passes for sale", "Passes also move wallet-to-wallet like any DERO asset.")));
  }
  function buySheet(f, l) {
    let n = 1n; const max = l.units; const out = h("div", { class: "outline mt" }); const btn = button("Buy", "buy wide mt", () => { closeSheet(); tx({ scid: f.scid, entry: "Buy", args: [["id", "U", BigInt(l.id)], ["n", "U", n]], dero: n * l.price, describe: "Buy " + n + " pass" + (n > 1n ? "es" : "") }); });
    const draw = () => render(out, h("div", { class: "l" }, "You pay"), h("div", { class: "v num" }, dero(n * l.price)), h("div", { class: "note mt8" }, n + " of " + max + " · " + N(f.mfee) / 100 + "% of the price goes to registered holders, the rest to the seller."));
    openSheet(h("h2", null, "Buy passes"), h("div", { class: "row mt" }, h("div", { class: "seg" }, h("button", { type: "button", onclick: () => { if (n > 1n) n--; draw(); } }, "−"), h("button", { type: "button", onclick: () => { if (n < max) n++; draw(); } }, "+")), h("span", { class: "sub" }, "quantity")), out, btn); draw();
  }
  function listSheet(f) {
    const units = h("input", { inputmode: "numeric", placeholder: "passes", value: "1" }), price = h("input", { inputmode: "decimal", placeholder: "price per pass in DERO" });
    openSheet(h("h2", null, "Sell passes"), h("div", { class: "stack mt" }, h("label", { class: "field" }, "How many (you have " + (mine ? mine.wallet : 0n) + " in your wallet)", units), h("label", { class: "field" }, "Price per pass (DERO)", price)),
      h("div", { class: "note mt" }, "Listed passes sit in the contract until sold or unlisted; they do not earn while listed. " + N(f.mfee) / 100 + "% of each sale goes to registered holders."),
      button("List for sale", "primary wide mt", () => { const u = BigInt(Math.max(0, parseInt(units.value || "0", 10))), p = A.parseUnits(price.value, 5n); if (!u || !p) return toast("Enter a quantity and a price", "err"); if (mine && u > mine.wallet) return toast("You only have " + mine.wallet + " in your wallet", "err"); closeSheet(); tx({ scid: f.scid, entry: "List", args: [["price", "U", p]], asset: { scid: f.scid, amount: u }, describe: "List " + u + " pass" + (u > 1n ? "es" : "") }); }));
  }
  function accounting(f) {
    const waiting = f.und, owed = f.rpool;
    return h("div", { class: "card mt" }, h("div", { class: "row tight" }, icon("shield", "svgi up"), h("b", null, "Public accounting")),
      h("div", { class: "note mt8" }, "The promise is stored in the contract and cannot be changed: " + N(f.share) / 100 + "% of the protocol fees claimed from every market is deposited here. Deposits, payouts and the registry are all on chain; the treasury cannot pay out less than what it deposited, and anyone can deposit or push payouts."),
      kv("Promised share of fees", N(f.share) / 100 + "%"), kv("Deposited by the treasury and others", dero(f.dep)), kv("Marketplace fees added", dero(f.mdep)), kv("Paid to holders", dero(f.paid)), kv("Owed, not yet pushed", dero(owed)), kv("Waiting for the first registration", dero(waiting)), kv("Registered passes", f.hst + " of " + f.minted + " minted"),
      h("div", { class: "row mt", style: "flex-wrap:wrap" },
        owed > Z && f.hn > Z ? button("Push rewards to holders", "small", () => tx({ scid: f.scid, entry: "PayBatch", args: [["from", "U", 0n], ["n", "U", 15n]], fee: 20000, describe: "Push rewards" })) : null,
        waiting > Z && f.hst > Z ? button("Release waiting DERO", "small", () => tx({ scid: f.scid, entry: "Distribute", args: [], describe: "Distribute" })) : null,
        h("button", { class: "mono linkbtn", type: "button", onclick: () => { navigator.clipboard && navigator.clipboard.writeText(f.scid).then(() => toast("Contract id copied", "ok")); } }, V.shortAddr(f.scid))));
  }
  function collections() {
    const nfts = [...D.nfts.values()];
    return [U.sectionHead("gem", "Holder NFTs", "Editions earned by holding a token for a number of blocks", button("Create", "small", () => V.forms.nft())),
      nfts.length ? h("div", { class: "list mt" }, nfts.map((n) => h("button", { class: "card tight row", type: "button", onclick: () => router.go("nft", n.scid) }, avatar({ icon: n.icon, ticker: "NFT", scid: n.scid }), h("div", { class: "grow ellipsis" }, h("b", null, n.name), h("div", { class: "sub" }, "hold " + A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker + " · " + n.minted + "/" + n.maxsupply + " minted")), pill(A.fmtDero(n.price, 1) + " DERO", "acc")))) : U.emptyState("gem", null, D.lastRefresh ? "No collections yet." : "Loading…")];
  }

  router.on("nfts", (el) => {
    let lastKey = null;
    const draw = () => {
      const f = D.founders;
      // re-render only when something on this page changed (the rest of the app refreshes every block)
      const key = [f ? f.sig : "-", mine ? mine.wallet + ":" + mine.units + ":" + mine.pending : "-", V.wallet.connected, D.nfts.size, D.lastRefresh > 0].join("|");
      if (key === lastKey) return; lastKey = key;
      render(el, h("h1", { class: "mt" }, "NFTs"),
        h("div", { class: "sub mb" }, "The Founders Pass shares the exchange's fees with its holders. Holder NFTs reward people for holding a token."),
        f ? [passCard(f), marketplace(f), accounting(f)] : h("div", { class: "card" }, h("div", { class: "sub" }, D.cfg.founders ? (D.lastRefresh ? "The Founders Pass contract did not verify against its template." : "Loading the Founders Pass…") : "No Founders Pass configured for this deployment.")),
        collections());
      if (f && V.wallet.connected) loadMine().then(() => { if (router.current && router.current.name === "nfts" && mine) draw(); });
    };
    mine = null; lastKey = null; draw(); D.onChange(() => router.current && router.current.name === "nfts" && draw());
  });
  V.nfts = { loadMine, get mine() { return mine; } };
})();
