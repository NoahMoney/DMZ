// DMZ nfas.js — NFAs: one contract per asset (ART-NFA-MS1 v1.1.1 with a quote, contracts/nfa.bas), listed by
// collection as the first section of the NFTs tab, and each asset's own page at #nfa/<scid>. Every number is read
// from the contract; every amount is in the quote's own symbol and decimals, and DERO appears on a wrapped-quote
// page only as gas. Every market entrypoint reads SIGNER() (docs/NFA.md §4), so every call goes at ring size 2
// and there is no private mode. Nothing here holds a key or the unit: the wallet signs each transfer over XSWD.
// The listing clock is the block's timestamp in seconds; the page reads it against the wall clock, which can
// differ by about a block near a boundary — the contract decides, and a button is a reading, not a promise.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, { h, render, avatar, router, button, kv, openSheet, closeSheet, toast } = U;
  const me = () => V.wallet.canon, Z = 0n, N = (x) => Number(x), now = () => Math.floor(Date.now() / 1000);
  const BOUND = 1844674407370955n; // BuyItNow 65 / Bid 85: above this the settlement products would wrap
  const RING2 = "Ring size 2: the transaction names your address, and anyone can compute who signed it.";
  const stat = (l, v) => h("div", { class: "stat" }, h("div", { class: "l" }, l), h("div", { class: "v num" }, v));
  // An amount in the quote. A quote whose vault the config does not name has no known scale: raw units, said so.
  const sym = (s) => D.qsym(s);
  const amt = (s, v) => (s.quoteDecUnknown ? v + " units of " + sym(s) + " (scale unknown)" : A.fmtUnits(v, D.qdec(s)) + " " + sym(s));
  const when = (t) => new Date(N(t) * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
  const left = (x) => (x >= 3600 ? Math.floor(x / 3600) + " h " + Math.floor((x % 3600) / 60) + " min" : x >= 60 ? Math.floor(x / 60) + " min" : x + " s");
  const who = (a) => h("button", { class: "mono linkbtn", type: "button", onclick: () => router.go("profile", a) }, D.displayName(a));
  // checkActive, read off the record: "none" while the unit is not in the contract, "live" until endBlockTime,
  // "ended" after it (111) — the state CloseListing needs and Bid/BuyItNow refund on.
  const state = (s) => (s.scBalance !== 1n || !s.listType ? "none" : s.endBlockTime > BigInt(now()) ? "live" : "ended");
  const minBid = (s) => (s.currBidPrice > s.startPrice ? s.currBidPrice : s.startPrice); // Bid 70–80
  const nextMin = (s, v) => { const p = v + (v * s.percentBidIncr) / 10000n, q = v + s.staticBidIncr; return p < q ? p : q; }; // findLesserIncrease
  const cancelLeft = (s) => N(s.startBlockTime + s.cancelBuffer) - now(); // CancelListing 60
  // one mono line per tile: what a visitor can do about it, in the quote
  function line(s) {
    const st = state(s), own = s.owner && s.owner === me() ? " · yours" : "";
    if (st === "none") return own ? "Yours · not listed" : "Not listed";
    const ended = st === "ended" ? " ended" : "";
    if (s.listType === "sale") return "Sale" + ended + " · " + amt(s, s.startPrice) + own;
    return "Auction" + ended + " · " + (s.bidCount > Z ? "top bid " + amt(s, s.currBidAmt) : "from " + amt(s, s.startPrice)) + " · " + s.bidCount + " bid" + (s.bidCount === 1n ? "" : "s") + own;
  }
  // processDEROFinalPayment to the unit: each share floors on its own, the owner takes the remainder
  function split(s, v, cp, ca) {
    const c = cp === undefined ? s.charityDonatePerc : cp, r = (s.royalty * v) / 100n, a = (s.artificerFee * v) / 100n, ch = (c * v) / 100n, p = (s.protoFee * v) / 10000n, cto = ca || s.charityAddr;
    return h("div", { class: "mt8" },
      kv("Royalty " + s.royalty + "% → creator", amt(s, r), "num"),
      kv("Protocol fee " + N(s.protoFee) / 100 + "% → treasury", amt(s, p), "num"),
      s.artificerFee > Z ? kv("Artificer fee " + s.artificerFee + "% → Civilware", amt(s, a), "num") : null,
      c > Z ? kv("Charity " + c + "% → " + (cto ? V.shortAddr(cto) : "the address given"), amt(s, ch), "num") : null,
      kv("Owner receives", amt(s, v - r - a - ch - p), "num"),
      h("div", { class: "note mt8" }, "Paid in " + sym(s) + " by the contract, in the same call that moves the unit. The network fee is DERO: gas, shown by your wallet."));
  }
  const pay = (s, v) => (s.quote ? { asset: { scid: s.quote, amount: v } } : { dero: v }); // ASSETVALUE(quote); the zero hash is DEROVALUE()
  // the wallet's balance of this asset: 1n when it holds the unit; null while unread or when the wallet did not answer
  let unit = null, unitFor = "";
  async function loadUnit(scid, force) {
    const k = scid + ":" + me(); if (!me()) { unit = null; unitFor = ""; return; } if (!force && unitFor === k) return;
    try { const b = await V.wallet.getBalance(scid, true); unit = BigInt(b.balance || 0); unitFor = k; } catch (e) { unit = null; unitFor = ""; }
  }
  const tx = async (scid, opts) => { const ok = await V.app.tx(Object.assign({ scid, ring: 2 }, opts)); if (ok) { await D.refresh().catch(() => {}); await loadUnit(scid, true); D.changed(); } return ok; };
  // what the wallet holds of the quote, read for the sheet and never summed with anything
  // `then` receives the amount, or null when the wallet did not answer, so a sheet's button can refuse before the tap
  function balanceLine(s, then) {
    const el = h("div", { class: "note mt8" }, "Reading your " + sym(s) + " balance…");
    V.wallet.getBalance(s.quote || "", true).then((b) => { const v = BigInt(b.balance || 0); el.textContent = "In your wallet: " + amt(s, v); if (then) then(v); }, () => { el.textContent = "Your wallet did not answer for " + sym(s) + ": balance unreadable."; if (then) then(null); });
    return el;
  }

  function buySheet(s) {
    const p = s.startPrice;
    // The button opens only once the wallet has answered and holds the price: a tap the wallet would refuse is not
    // offered, and a balance that cannot be read counts as not enough — the button says which.
    const btn = button("Reading your " + sym(s) + " balance…", "primary wide mt", () => { closeSheet(); tx(s.scid, Object.assign({ entry: "BuyItNow", args: [], describe: "Buy " + s.name, outcome: { scid: s.scid, expect: "up" }, probe: { scid: s.scid, keys: ["owner", "scBalance"] } }, pay(s, p))); }, { disabled: true });
    const gate = (v) => { if (v === null) { btn.textContent = "Balance unreadable"; return; } if (v < p) { btn.textContent = "Not enough " + sym(s) + ": " + amt(s, p - v) + " short"; return; } btn.textContent = "Buy for " + amt(s, p); btn.disabled = false; };
    openSheet(h("h2", null, "Buy now"), h("div", { class: "outline mt" }, h("div", { class: "l" }, "You pay"), h("div", { class: "v num" }, amt(s, p))), balanceLine(s, gate),
      h("div", { class: "label mt" }, "Where it goes"), split(s, p),
      h("div", { class: "note mt8" }, "Refused if someone bought first: your " + sym(s) + " comes back in the same call. " + RING2),
      btn);
  }
  function bidSheet(s) {
    const min = minBid(s), dec = D.qdec(s), cl = cancelLeft(s);
    const inp = h("input", { inputmode: "decimal", placeholder: "amount in " + sym(s), value: A.fmtUnits(min, dec) }), out = h("div", { class: "mt" });
    let v = null, bal; // bal: undefined while the wallet is being read, null when it did not answer
    const btn = button("Reading your " + sym(s) + " balance…", "primary wide mt", () => { if (v === null || v < min || v > BOUND) return toast("Enter a bid of at least " + amt(s, min), "err"); closeSheet(); tx(s.scid, Object.assign({ entry: "Bid", args: [], describe: "Bid " + amt(s, v), probe: { scid: s.scid, keys: ["bidCount", "currBidPrice"] } }, pay(s, v))); }, { disabled: true });
    // the same gate as the Buy sheet, re-read on every keystroke: the button opens only for a bid the wallet holds
    const gate = () => { const okv = v !== null && v >= min && v <= BOUND; btn.disabled = true; btn.textContent = bal === undefined ? "Reading your " + sym(s) + " balance…" : bal === null ? "Balance unreadable" : okv && v > bal ? "Not enough " + sym(s) + ": " + amt(s, v - bal) + " short" : "Place bid"; if (okv && bal !== undefined && bal !== null && v <= bal) btn.disabled = false; };
    const draw = () => { v = A.parseUnits(inp.value, dec); render(out, v === null || v < min ? h("div", { class: "errbox" }, "The minimum bid is " + amt(s, min) + ": the higher of the start price and the current minimum.")
      : v > BOUND ? h("div", { class: "errbox" }, "Above the contract's bound of " + amt(s, BOUND) + ": it would be refunded.")
      : [h("div", { class: "label" }, "Where it goes if you win"), split(s, v), h("div", { class: "note mt8" }, "After this bid the next one must be at least " + amt(s, nextMin(s, v)) + " — the lesser of +" + N(s.percentBidIncr) / 100 + "% and +" + amt(s, s.staticBidIncr) + ". Outbid, your " + sym(s) + " comes back in the call that takes the higher bid.")]); gate(); };
    inp.oninput = draw; draw();
    openSheet(h("h2", null, "Bid"), h("label", { class: "field mt" }, "Your bid (" + sym(s) + ")", inp), balanceLine(s, (b) => { bal = b; gate(); }), out,
      h("div", { class: "note mt8" }, "A bid in the last 15 minutes moves the end to 15 minutes after it, every time and without a cap; only the initial duration is capped, at 7 days." + (cl > 0 ? " The seller can still cancel for " + left(cl) + "; a cancel refunds your bid in the same call." : "") + " " + RING2),
      btn);
  }
  function startSheet(s) {
    const dec = D.qdec(s), room = (10000n - s.protoFee - s.artificerFee * 100n - s.royalty * 100n) / 100n; // Start 150: the charity share's room
    let type = "sale";
    const price = h("input", { inputmode: "decimal", placeholder: "start price in " + sym(s) }), hours = h("input", { inputmode: "numeric", value: "24" }), caddr = h("input", { placeholder: "dero… (only with a share)" }), cperc = h("input", { inputmode: "numeric", value: "0" });
    const out = h("div", { class: "mt" }); let ok = null, why = "";
    const read = () => {
      const p = A.parseUnits(price.value, dec); let hrs = BigInt(Math.max(0, parseInt(hours.value || "0", 10) || 0)); if (hrs === Z) hrs = 1n; if (hrs > 168n) hrs = 168n; // generateEndBlock
      let cp = BigInt(Math.max(0, parseInt(cperc.value || "0", 10) || 0)); if (cp > room) cp = room; // Start 155–165 clamps it the same way
      const ca = cp > Z ? V.canonAddress(caddr.value.trim()) : ""; return { p, hrs, cp, ca };
    };
    const draw = () => {
      const r = read(); ok = null;
      if (!r.p || r.p === Z) return render(out, h("div", { class: "note" }, why = "Enter the price a buyer pays" + (type === "auction" ? " to open the bidding" : "") + ", in " + sym(s) + "."));
      if (r.p > BOUND) return render(out, h("div", { class: "errbox" }, why = "Above the contract's bound of " + amt(s, BOUND) + ": a sale at this price could never settle."));
      if (r.cp > Z && !r.ca) return render(out, h("div", { class: "errbox" }, why = "A charity share needs a valid DERO address; the contract returns the unit otherwise."));
      ok = r;
      render(out, h("div", { class: "label" }, type === "sale" ? "If it sells at " + amt(s, r.p) : "If it sells at the start price"), split(s, r.p, r.cp, r.ca),
        h("div", { class: "note mt8" }, (type === "sale" ? "Runs " : "Bidding runs ") + r.hrs + " hour" + (r.hrs === 1n ? "" : "s") + " from the block that includes it (1 to 168; the contract clamps)" + (type === "auction" ? ", and a bid in the last 15 minutes extends it by 15 minutes, without a cap" : "") + ". You can cancel within the first " + left(N(s.cancelBuffer)) + "; after that it runs to its end. The unit moves into the contract now and comes back to you if nothing sells" + (r.cp === room && r.cp > Z ? " · charity share clamped to the " + room + "% the fees leave" : "") + ". " + RING2));
    };
    for (const i of [price, hours, caddr, cperc]) i.oninput = draw;
    openSheet(h("h2", null, "List " + s.name), U.segmented([{ v: "sale", l: "Sale" }, { v: "auction", l: "Auction" }], type, (v) => { type = v; draw(); }),
      h("div", { class: "stack mt" }, h("label", { class: "field" }, "Price (" + sym(s) + ")", price), h("label", { class: "field" }, "Duration (hours, up to 168)", hours),
        h("div", { class: "row" }, h("label", { class: "field" }, "Charity share (%, up to " + room + ")", cperc), h("label", { class: "field", style: "flex:2" }, "Charity address", caddr))),
      out, button("Start listing", "primary wide mt", () => { if (!ok) return toast(why, "err"); closeSheet(); tx(s.scid, { entry: "Start", args: [["listType", "S", type], ["duration", "U", ok.hrs], ["startPrice", "U", ok.p], ["charityDonateAddr", "S", ok.ca || ""], ["charityDonatePerc", "U", ok.cp]], asset: { scid: s.scid, amount: 1n }, describe: "Start " + type, probe: { scid: s.scid, keys: ["listType", "startBlockTime"] } }); }));
    draw();
  }

  // Level 2 — the visitor's role, read from the record: every button's precondition is a stored value or the
  // wallet's own balance, and a precondition that cannot be read shows no button.
  function position(s, st) {
    const scid = s.scid, my = me(), own = !!my && s.owner === my, out = [], note = (t) => out.push(h("div", { class: "note" }, t));
    if (!my) return h("div", { class: "card" }, h("div", { class: "between" }, h("span", { class: "sub" }, "Connect a wallet to buy, bid, claim or list."), button("Connect", "small", () => V.app.connectWallet())));
    const holds = unit !== null && unit >= 1n, noScale = !!s.quoteDecUnknown;
    if (holds && !own) { out.push(button("Claim ownership", "primary wide", () => tx(scid, { entry: "ClaimOwnership", args: [], asset: { scid, amount: 1n }, describe: "Claim ownership", probe: { scid, keys: ["owner"] } })));
      note("Your wallet holds the unit, but the contract records " + (s.owner ? D.displayName(s.owner) : "no one") + " as owner. Claiming sends the unit through the contract and straight back to you, and records you: do it before anything else. " + RING2); }
    if (st === "none" && own) {
      if (holds && noScale) note("This app does not know the scale of " + sym(s) + ", so no price can be entered here.");
      else if (holds) out.push(button("Start a sale or auction", "primary wide", () => startSheet(s)));
      else if (unit === 0n) note("The contract records you as owner, but this wallet does not hold the unit: whoever holds it can claim it, and nothing can be listed from here.");
      else note("Could not read whether this wallet holds the unit, so nothing can be listed from here.");
    }
    if (st === "live" && !own) {
      if (s.listType === "sale") { if (s.startPrice > BOUND) note("The price is above the contract's settlement bound (" + amt(s, BOUND) + "): this sale cannot complete."); else out.push(button("Buy now · " + amt(s, s.startPrice), "primary wide", () => buySheet(s))); }
      else if (noScale) note("This app does not know the scale of " + sym(s) + ", so no bid can be entered here.");
      else out.push(button((s.bidder === my ? "Raise" : "Bid") + " · from " + amt(s, minBid(s)), "primary wide", () => bidSheet(s)));
      if (s.listType === "auction" && s.bidder === my) note("You are the top bidder at " + amt(s, s.currBidAmt) + ".");
    }
    if (st === "live" && own) { const cl = cancelLeft(s);
      if (cl > 0) { out.push(button("Cancel · " + left(cl) + " left", "wide", () => tx(scid, { entry: "CancelListing", args: [], describe: "Cancel listing", probe: { scid, keys: ["scBalance", "listType"] } }))); note("Cancelling refunds the top bidder in the same call and returns the unit to you. " + RING2); }
      else note("A listing cannot be cancelled after its first " + left(N(s.cancelBuffer)) + ": this one runs to its end" + (s.listType === "auction" && s.bidCount > Z ? ", then closes to the top bidder." : ".")); }
    if (st === "ended") {
      const won = s.listType === "auction" && s.bidCount > Z;
      if (own) { out.push(button(won ? "Close · settle to the top bidder" : "Close · unit back to you", "primary wide", () => tx(scid, { entry: "CloseListing", args: [], describe: "Close listing", probe: { scid, keys: ["scBalance", "listType"] } }))); note((won ? "Closing pays " + amt(s, s.currBidAmt) + " out by the split below and hands the unit to " + D.displayName(s.bidder) + ". " : "Nothing sold; closing returns the unit to your wallet. ") + RING2); }
      else if (won && s.bidder === my) { out.push(button("Settle · you won", "primary wide", () => tx(scid, { entry: "Bid", args: [], describe: "Settle auction", outcome: { scid, expect: "up" }, probe: { scid, keys: ["owner", "scBalance"] } }))); note("Bid() with nothing attached on an ended auction settles it: the contract pays your " + amt(s, s.currBidAmt) + " out by the split and sends you the unit. The owner's Close does the same. " + RING2); }
      else note("Ended. " + (won ? "The owner or the top bidder settles it." : "The owner closes it and takes the unit back."));
      if (won && (own || s.bidder === my)) out.push(split(s, s.currBidAmt));
    }
    if (!out.length) note(st === "none" ? "Not listed. Only the recorded owner can start a sale or auction." : "");
    return h("div", { class: "card" }, h("div", { class: "label" }, own ? "Your asset" : "Your position"), h("div", { class: "stack mt8" }, out));
  }
  // Level 3 — the listing and the record, as rows
  function listing(s, st) {
    // A bid left in the contract with no listing: an ended auction closed by an empty BuyItNow (docs/NFA.md §7 R4)
    // keeps currBidAmt under the bidder's name; the contract refunds it on the next listing's first bid or cancel.
    if (st === "none") return [kv("Listing", "none"), s.previousSalePrice > Z ? kv("Previous sale", amt(s, s.previousSalePrice), "num") : null, s.previousAuctionPrice > Z ? kv("Previous auction", amt(s, s.previousAuctionPrice), "num") : null,
      s.currBidAmt > Z && s.bidder ? [kv("Held bid", h("span", null, amt(s, s.currBidAmt), " · ", who(s.bidder)), "num"), h("div", { class: "note mt8" }, "A bid the contract still holds from an auction closed without settling. It is refunded when the owner's next listing takes a bid or is cancelled; nothing else releases it.")] : null];
    const auc = s.listType === "auction", cl = cancelLeft(s);
    return [kv("Listing", (auc ? "Auction" : "Sale") + " · " + (st === "live" ? "live" : "ended, not yet closed")),
      kv(auc ? "Start price" : "Price", amt(s, s.startPrice), "num"),
      auc ? kv("Top bid", s.bidCount > Z ? h("span", null, amt(s, s.currBidAmt), " · ", who(s.bidder)) : "none yet", "num") : null,
      auc ? kv("Bids", String(s.bidCount)) : null, auc && st === "live" ? kv("Minimum next bid", amt(s, minBid(s)), "num") : null,
      kv("Started", when(s.startBlockTime)), kv(st === "live" ? "Ends" : "Ended", when(s.endBlockTime) + (auc && st === "live" ? " · extends while bids arrive" : "")),
      st === "live" ? kv("Seller can cancel", cl > 0 ? "for " + left(cl) + " more" : "no longer: the first " + left(N(s.cancelBuffer)) + " have passed") : null,
      s.charityDonatePerc > Z ? kv("Charity share", s.charityDonatePerc + "% → " + (s.charityAddr ? V.shortAddr(s.charityAddr) : "?")) : null,
      h("div", { class: "note mt8" }, "Times are the block's, shown in your local zone.")];
  }
  function record(s) {
    const v = D.vaultOf(s), file = U.safeUrl(s.file), sig = U.safeUrl(s.fileSign);
    const halted = s.quote && D.vault && D.vault.scid === s.quote && D.vault.halted > BigInt(D.height || 0);
    const link = (u) => h("a", { href: u, target: "_blank", rel: "noopener noreferrer", class: "mono", style: "word-break:break-all" }, u);
    return h("div", { class: "card" }, h("div", { class: "between" }, h("b", null, "On chain"), h("span", { class: "label" }, "Read from the contract")),
      h("div", { class: "mt8" },
        kv("Owner", s.owner ? h("span", { class: "row tight" }, avatar(s.owner, "sm"), who(s.owner)) : "in the contract, unclaimed"),
        kv("Creator", s.creator ? who(s.creator) : "?"), s.prevOwner ? kv("Previous owner", who(s.prevOwner)) : null,
        kv("Priced in", s.quote ? sym(s) + (v ? " · wrapped by the " + U.clean(v.name || v.sym || "", 24) + " vault" : " · a vault this app does not name") : "DERO"),
        halted ? h("div", { class: "warnbox mt8" }, h("b", null, "The " + sym(s) + " vault is halted."), " Its signers start no new foreign payment while the halt lasts; " + sym(s) + " still moves on DERO, so a price here is a price in a halted reserve.") : null,
        v ? h("div", { class: "row mt8" }, button("Open the vault", "small ghost", () => router.go("wrap", s.quote))) : null,
        kv("Royalty", s.royalty + "% of every sale to the creator"), kv("Protocol fee", N(s.protoFee) / 100 + "% to the treasury"), kv("Artificer fee", s.artificerFee + "%" + (s.artificerFee > Z ? " to Civilware" : "")),
        kv("Type", s.type || "—"), kv("Collection", s.collection || "—"), s.tags ? kv("Tags", s.tags) : null, kv("Standard", "ART-NFA-MS1 " + (s.version || "?")),
        kv("File", file ? h("span", null, link(file), " · not verified against the hashes by this app") : s.file ? "a link this app does not open (not https)" : "none"),
        s.checkC ? kv("fileCheckC", h("span", { class: "mono", style: "word-break:break-all" }, s.checkC)) : null, s.checkS ? kv("fileCheckS", h("span", { class: "mono", style: "word-break:break-all" }, s.checkS)) : null,
        sig ? kv("Signature file", link(sig)) : null,
        kv("URL updates", "the creator" + (s.ownerCanUpdate === 1n ? " and the owner" : "") + " can change the file, cover, icon and tag URLs at any time, listed or not; the name, hashes and collection cannot change"),
        kv("Contract", h("button", { class: "mono", type: "button", title: "Copy the contract id", onclick: () => { navigator.clipboard && navigator.clipboard.writeText(s.scid).then(() => toast("Contract id copied", "ok")); } }, V.shortAddr(s.scid)))));
  }

  function page(el, scid) {
    if (!/^[0-9a-f]{64}$/.test(String(scid || ""))) return render(el, h("div", { class: "errbox mt" }, "Not a contract id"));
    let lastKey = null;
    const draw = () => {
      const s = D.nfas.get(scid);
      // a cold deep link lands before the registry is read: wait for it, then say unknown rather than guess
      if (!s) { const k = D.lastRefresh ? "unknown" : "wait"; if (k === lastKey) return; lastKey = k; return render(el, k === "wait" ? h("div", { class: "card mt" }, h("div", { class: "label" }, "NFA"), h("div", { class: "sub mt8" }, "Reading the registry…")) : h("div", { class: "errbox mt" }, "Unknown NFA: not in the registry, or its code, treasury or fee did not verify, so nothing about it is shown.")); }
      const st = state(s), cl = cancelLeft(s);
      const key = [s.sig, st, me(), unit, Math.floor(cl / 60), V.settings.images, s.currBidAmt, s.bidder].join("|");
      if (key === lastKey) return; lastKey = key;
      const cover = U.safeUrl(s.cover), own = !!me() && s.owner === me();
      const big = st === "none" ? (s.previousSalePrice > Z ? ["Last sale", amt(s, s.previousSalePrice)] : s.previousAuctionPrice > Z ? ["Last auction", amt(s, s.previousAuctionPrice)] : ["Not listed", "—"])
        : s.listType === "sale" ? [st === "live" ? "Buy now" : "Sale ended at", amt(s, s.startPrice)] : s.bidCount > Z ? ["Top bid", amt(s, s.currBidAmt)] : ["Bidding from", amt(s, s.startPrice)];
      render(el,
        // Level 1 — the asset and its price, in the quote
        h("div", { class: "hero mt" },
          h("div", { class: "between" }, h("span", { class: "eyebrow" }, "NFA · priced in " + sym(s)), U.verified(s)),
          h("div", { class: "row mt8" }, avatar(s, "big"), h("div", { class: "grow" }, h("h1", null, s.name), h("div", { class: "sub ellipsis" }, [s.collection, s.type].filter(Boolean).join(" · ") || "no collection"))),
          h("div", { class: "label mt" }, big[0]), h("div", { class: "big num mt8" }, big[1]),
          h("div", { class: "note mt8" }, line(s) + (own ? "" : s.owner ? " · owner " + D.displayName(s.owner) : " · unclaimed"))),
        cover && V.settings.images ? h("img", { src: cover, alt: "", referrerpolicy: "no-referrer", loading: "lazy", style: "width:100%;display:block;margin-top:14px;border:1px solid var(--edge)" }) : null,
        h("div", { class: "stats4 mt" }, stat("Royalty", s.royalty + "%"), stat("Protocol fee", N(s.protoFee) / 100 + "%"), stat("Artificer fee", s.artificerFee + "%"), stat("Bids", String(s.bidCount))),
        s.descr ? h("div", { class: "note mt8" }, s.descr) : null,
        h("div", { class: "nft-grid mt" }, h("div", null, position(s, st), h("div", { class: "mt" }, listing(s, st))), h("div", null, record(s))));
      if (me()) loadUnit(scid).then(() => { if (router.current && router.current.name === "nfa" && router.current.param === scid) draw(); });
    };
    unit = null; unitFor = ""; draw();
    D.onChange(() => router.current && router.current.name === "nfa" && router.current.param === scid && draw());
  }

  // The section on the NFTs tab: tiles by collection, a mono line each, the quote's symbol on every amount.
  function section() {
    const all = [...D.nfas.values()], groups = new Map();
    for (const s of all) { const k = s.collection || ""; if (!groups.has(k)) groups.set(k, []); groups.get(k).push(s); }
    const keys = [...groups.keys()].sort((a, b) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)));
    const tile = (s) => { const img = U.safeUrl(s.cover) || U.safeUrl(s.icon);
      return h("button", { class: "card", type: "button", style: "padding:0;border:0;border-radius:0;overflow:hidden;outline:1px solid var(--edge)", onclick: () => router.go("nfa", s.scid) },
        img && V.settings.images ? h("img", { src: img, alt: "", referrerpolicy: "no-referrer", loading: "lazy", style: "width:100%;aspect-ratio:1;object-fit:cover;display:block" }) : h("div", { style: "aspect-ratio:1;display:grid;place-items:center" }, avatar(s, "big")),
        h("div", { style: "padding:10px;text-align:left" }, h("b", { class: "ellipsis", style: "display:block" }, s.name), h("div", { class: "mono dim2 ellipsis", style: "margin-top:5px" }, line(s)))); };
    const create = typeof (V.forms && V.forms.nfa) === "function" ? button("Create", "small", () => V.forms.nfa()) : null;
    return [U.sectionHead("gem", "NFAs", "Assets priced in XMR, ZEC or DERO, each with its sale or auction inside its own contract", all.length ? create : null),
      all.length ? keys.map((k) => [keys.length > 1 || k ? h("div", { class: "label mt" }, k || "No collection") : null, h("div", { class: "mt8", style: "display:grid;grid-template-columns:repeat(auto-fill,minmax(148px,1fr));gap:1px" }, groups.get(k).map(tile))])
        : D.lastRefresh ? U.emptyState("gem", "No assets yet", "List an asset priced in XMR or ZEC: one contract per asset, its sale or auction inside it, and every payment in the quote you choose. Anyone can create one.", create)
          : U.emptyState("gem", "NFAs", "Reading the registry…")];
  }
  V.nfas = { section, page, key: () => [...D.nfas.values()].map((s) => s.sig + line(s)).join("|") };
})();
