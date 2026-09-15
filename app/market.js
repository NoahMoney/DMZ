// DMZ market.js — the market page's regions that read other files' interfaces or restate the contract: the pair a
// market is named by, the links its creator published, its promotion status, what a wrapped side of it is and
// where its quote is deposited, and the side rail's three tabs (published trades, the accounts that posted them,
// the contract's terms). token.js lays the page out and owns the chart and the trade sheet.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, { h, render, avatar, router, button, kv, icon } = U;
  const Z = 0n, me = () => V.wallet.canon;
  const M = (V.market = {});
  // Other files' interfaces are feature-checked: absent, or throwing, the region they fill is not drawn.
  M.ask = (f) => { try { return f(); } catch (e) { return null; } };
  // The pair a market is named by: U.pair where ui.js provides it, else the market's own pair, else TICKER / QUOTE.
  M.pairOf = (s) => { const p = typeof U.pair === "function" ? M.ask(() => U.pair(s)) : s.pair || s.ticker; return p && String(p).includes("/") ? String(p) : (p || s.ticker) + " / " + D.qsym(s); };
  // a section is a named region, not a card: a hairline, a label, and — where the section has one — its headline
  // number on the same line
  M.head = (l, right) => h("div", { class: "between mt", style: "border-top:1px solid var(--edge);padding-top:13px" }, h("span", { class: "eyebrow" }, l), right || null);
  // Level 3: a label above a number, no box and no rule. The grid keeps the columns aligned at every width.
  M.mini = (l, v, cls) => h("div", { class: "mini" }, h("div", { class: "l" }, l), h("div", { class: "v num " + (cls || "") }, v));
  M.band = (...items) => h("div", { class: "tk-band" }, items);

  // The creator's links (V.profile.links), as external anchors that show where they go: the domain, never a label a
  // creator chose, and https only (U.safeUrl). The title says nobody checked them.
  const LINKS = [["website", "Website"], ["x", "X"], ["telegram", "Telegram"], ["discord", "Discord"], ["github", "GitHub"]];
  const host = (u) => { try { return new URL(u).hostname.replace(/^www\./, ""); } catch (e) { return ""; } };
  M.linksOf = (s) => { const l = M.ask(() => V.profile && typeof V.profile.links === "function" && V.profile.links(s)); return LINKS.map(([k]) => (l && U.safeUrl(l[k])) || ""); };
  M.links = (us) => { const out = LINKS.map(([, name], i) => { const u = us[i], d = u && host(u); return d ? h("a", { href: u, target: "_blank", rel: "noopener noreferrer nofollow", referrerpolicy: "no-referrer", title: name + ": " + u + ". Published by the creator; this app did not check it.", "aria-label": name + ": " + d + ", external site" }, h("span", { class: "l" }, name), " " + d) : null; }).filter(Boolean); return out.length ? h("div", { class: "tk-links" }, out) : null; };
  // promotion is a paid placement (V.promo.statusOf), stated as that and nothing more
  M.promoted = (s) => { const p = M.ask(() => V.promo && typeof V.promo.statusOf === "function" && V.promo.statusOf(s.scid)); return p === true || !!(p && typeof p === "object" && (p.promoted || p.active || p.status === "promoted")); };
  M.promoPill = () => h("span", { class: "pill dim", title: "Someone paid for this market to be placed where more people see it. It is not a review and not an endorsement." }, "Promoted");

  // Wrapped XMR and ZEC are first-class quotes. A wrapped token is a DERO asset the vault issues one for one
  // against coins its k-of-n federation holds, so a market with a wrapped side carries a small mark that says
  // so and names who holds the coins. The quote's vault comes first; a wrapped base is marked the same way.
  const vaults = () => M.ask(() => D.allVaults()) || [];
  M.quoteVault = (s) => (s && s.quote ? vaults().find((v) => v.scid === s.quote) || null : null);
  M.wrapOf = (s) => M.quoteVault(s) || (s && s.wrappedBase ? s.vaultRec || vaults().find((v) => v.scid === s.asset) || null : null);
  M.wrapMark = (s) => { const v = M.wrapOf(s); if (!v) return null; const c = U.clean(v.sym || v.name || "the coin", 8); return h("span", { class: "pill dim", title: "Wrapped " + c + " is a DERO token the vault issues one for one against " + c + " held by its k-of-n federation. Those signers can move the " + c + "; the vault contract cannot. It is not " + c + " on its own chain." }, "wrapped · 1:1"); };
  // the wrap hub's route: "move" once that screen exists, the Wrap screen until then
  M.moveRoute = () => (router.handlers && router.handlers.move ? "move" : "wrap");
  // On a market quoted in a wrapped coin, a connected wallet that holds none of it is pointed at the deposit.
  // bal is the wallet's balance of the quote: a BigInt once read, anything else while unknown (no link then).
  M.deposit = (s, bal) => {
    const v = M.quoteVault(s); if (!v || bal !== Z) return null;
    const c = U.clean(v.sym || v.name || "", 8);
    return h("div", { class: "mb" }, h("div", { class: "note" }, "This market is priced in " + D.qsym(s) + " and your wallet holds none. Depositing " + c + " with the vault's federation mints it one for one."),
      button("Deposit " + c, "ghost small mt8", () => router.go(M.moveRoute())));
  };

  // The side rail's tabs. "Publishers" lists accounts that published a trade here: balances are private on DERO,
  // so a list of holders cannot exist, and the tab is not called one.
  M.body = async (el, s, tab, buy) => {
    const H = V.home, launch = s.kind === "launch";
    if (tab === "trades") return render(el, s.trades.length
      ? h("div", { class: "list" }, [...s.trades].reverse().slice(0, 60).map((t) => H.feedItem(Object.assign({ asset: s }, t), true)))
      : U.emptyState("feed", "Published trades", "Nobody has posted a trade on this market. Posting is off by default, and an unposted trade still moves the price and still draws a candle.", button("Buy " + s.ticker, "primary", buy)));
    if (tab === "holders") {
      const traders = new Map(); for (const t of s.trades) traders.set(t.addr, (traders.get(t.addr) || 0) + 1);
      const mine = launch && s.tmode === 3n && me() ? await D.myKeys(s.scid, me(), ["st_"]).catch(() => ({})) : {};
      return render(el, h("div", { class: "note mb" }, "Balances are private on DERO, so nobody can list holders. Listed: accounts that published a trade here" + (launch && s.tmode === 3n ? ", plus the staking vault totals." : ".")),
        launch && s.tmode === 3n ? h("div", { class: "mb" }, kv("Total staked", A.fmtCompact(s.stot, s.dec) + " " + s.ticker), mine.st ? kv("Your stake", A.fmtUnits(mine.st, s.dec, 4)) : null) : null,
        traders.size ? h("div", { class: "list" }, [...traders.entries()].sort((x, y) => y[1] - x[1]).slice(0, 30).map(([a, n]) => h("button", { class: "feed-item", type: "button", onclick: () => router.go("profile", a) }, avatar(a, "sm"), h("div", { class: "body ellipsis" }, D.displayName(a)), h("span", { class: "sub tiny num" }, n + " posted"))))
          : U.emptyState("user", "Accounts that posted", "No account has published a trade here. Posting names the sender, so an empty list is not an empty market.", null));
    }
    const fee = Number(s.fee) / 100;
    // the verification sentence is prose, so it is a line of prose — a right-aligned value column is for numbers
    const unsure = s.unlisted || (s.kind === "pool" && !s.assetVerified);
    const items = [kv("Fee", fee + "% per trade"), kv(D.qsym(s) + " reserve", A.fmtUnits(s.rd, D.qdec(s), 2)), kv("Token reserve", A.fmtCompact(s.ra, s.dec)), kv("Price points", String(s.pc)), kv("Published trades", String(s.tc))];
    if (launch) items.push(kv("Supply", A.fmtCompact(s.supply, s.dec)), kv("Creator allocation", H.allocation(s) > Z ? A.fmtCompact(H.allocation(s), s.dec) + " (" + (H.allocBps(s) / 100).toFixed(2) + "% at launch)" : "none"), kv("Creator", button("View profile", "ghost small", () => router.go("profile", s.creator))), kv("Tax", s.tax > Z ? Number(s.tax) / 100 + "% → " + ["none", "creator", "buyback & lock", "stakers"][Number(s.tmode)] : "none"), kv("Locked by buyback", A.fmtCompact(s.locked, s.dec)), kv("Staked", A.fmtCompact(s.stot, s.dec)), kv("Liquidity", s.phase === Z ? "curve (virtual)" : "contract-owned, locked forever"));
    else items.push(kv("LP supply", A.fmtUnits(s.lp, D.qdec(s), 2))); // LP is minted one unit per quote unit deposited, so it carries the quote's decimals
    items.unshift(h("div", { class: "row tight mb", style: "align-items:flex-start" }, icon("shield", unsure ? "svgi warn" : "svgi up"),
      h("div", { class: "note" }, (s.unlisted ? "Matches the shipped “" + s.kind + "” template but is not in the DMZ registry." : "Matches the shipped “" + s.kind + "” template and pays the protocol treasury.") + (s.kind === "pool" && !s.assetVerified ? " The asset is not a DMZ launch: verify its id yourself." : ""))));
    if (s.descr) items.unshift(h("div", { class: "sub mb", style: "line-height:1.5" }, s.descr));
    const actions = [];
    if (launch && s.tmode === 3n) actions.push(button("Stake / rewards", "wide", () => V.sheets.stake(s)));
    // each one names the key its success must move, so a reverted call is reported as one, never as "included"
    if (launch && s.tmode === 2n && s.phase === 1n && s.tacc > Z) actions.push(button("Trigger buyback (" + A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s) + ")", "wide", () => V.app.tx({ scid: s.scid, entry: "Buyback", args: [], describe: "Buyback", probe: { scid: s.scid, keys: ["tacc"] } })));
    if (launch && s.tmode === 1n && me() === s.creator && s.tacc > Z) actions.push(button("Claim creator tax (" + A.fmtUnits(s.tacc, D.qdec(s), 2) + " " + D.qsym(s) + ")", "wide", () => V.app.tx({ scid: s.scid, entry: "ClaimTax", args: [], describe: "Claim tax", probe: { scid: s.scid, keys: ["tacc"] } })));
    if (!launch) actions.push(button("Add / remove liquidity", "wide", () => V.sheets.liquidity(s)));
    if (me() && me() === s.treasury && (s.fd > Z || s.fa > Z)) actions.push(button("Claim protocol fees", "wide", () => V.app.tx({ scid: s.scid, entry: "ClaimFees", args: [], describe: "Claim fees", probe: { scid: s.scid, keys: ["fd", "fa"] } })));
    render(el, h("div", { class: "mt8" }, items), actions.length ? h("div", { class: "stack mt" }, actions) : null);
  };
})();
