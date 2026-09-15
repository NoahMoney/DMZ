// DMZ move.js — Deposit & withdraw (#move): XMR and ZEC in and out of DERO through the build's vault, the only way
// they come in or go out. A deposit is sent to a deposit address of your own, and k of the vault's n named signers
// mint the same amount of wXMR or wZEC, 1:1; a withdrawal burns it with a destination, and the signers pay it and
// record the payment. #move chooses the coin and the direction, #move/<coin>/<deposit|withdraw> is the guide, and
// both list the connected wallet's own deposit indexes and burns with the stages the vault contract records. Doing it
// is the Wrap screen's (wrap.js): this screen reads and explains, and signs nothing.
//
// guide(), depositStages() and withdrawalStages() are pure, and tests/js/move.test.mjs holds them to the contract's
// own rules (contracts/vault.bas).
(function () {
  const V = window.Veil, U = V.ui, D = V.data, A = V.amm, B = BigInt, N = Number, { h, router, kv } = U;
  const K = () => V.wrapkit, T = () => V.track;
  // every screen here is drawn inside .swp, whose controls are at least 40 px tall (app/parts.css)
  const render = (el, ...n) => U.render(el, h("div", { class: "swp" }, ...n));
  // vault.bas: Reclaim opens 20000 blocks past the release height, and a signer's mark holds off a Veto for 2000
  const RECLAIM_AFTER = 20000, MARK_WINDOW = 2000, SCAN = 200, WSCAN = 60;
  const FOREIGN = { xmr: { name: "Monero", sym: "XMR", block: 120 }, zec: { name: "Zcash", sym: "ZEC", block: 75 } };
  const NODEP = "Deposits and withdrawals of XMR and ZEC are not available on this network.";
  const isNum = (x) => typeof x === "string" && /^\d+$/.test(x), num = (x) => (isNum(x) ? N(x) : null), hexv = (x) => typeof x === "string" && /^[0-9a-f]+$/.test(x);
  const coinOf = (rec) => { const c = rec && V.wrapui ? V.wrapui.netOf(rec)[1] : ""; return /xmr/.test(c) ? "xmr" : /zec/.test(c) ? "zec" : ""; };
  const vaultFor = (coin) => D.vaults().find((v) => coinOf(v) === coin) || null;
  const tip = () => (V.live && V.live.height) || D.height || 0;
  const dur = (s) => (s < 5400 ? Math.max(1, Math.round(s / 60)) + " min" : s < 172800 ? Math.round(s / 360) / 10 + " h" : Math.round(s / 8640) / 10 + " days");
  const blocks = (n) => N(n).toLocaleString("en-US") + " DERO block" + (N(n) === 1 ? "" : "s") + " (about " + dur(N(n) * 18) + ")";
  const units = (rec, v) => { const d = N(rec && rec.dec) || 0, F = FOREIGN[coinOf(rec)]; return d ? A.fmtUnits(v, d, d) + " " + (F ? F.sym : "units") : String(v) + " base units"; };
  const S = (who, chain, t, d) => ({ who, chain, t, d });

  // ---- custody, in plain words: who holds the coins, whether the backing can be checked, and what bounds the cap ----
  function custody(coin, rec, w, H) {
    const F = FOREIGN[coin], ws = "w" + F.sym, amt = (v) => units(rec, v);
    if (!w) return [["Who holds the " + F.sym, "k of the n named signers hold the reserve and can move all of it; the vault contract cannot. The numbers are read from the vault contract."]];
    const covered = w.capratio > 0n && w.cap / w.capratio <= w.bondtot, halted = w.halted > B(H || 0);
    return [
      ["Who holds the " + F.sym, w.k + " of the " + w.na + " named signers hold the reserve and can move all of it; the vault contract cannot. This is custody: you trust them with the coins for as long as you hold " + ws + "."],
      ["The reserve and the supply", ws + " in existence: " + amt(w.sup) + ", on DERO for anyone to read. " + (w.viewkey ? "A viewing key is published, so anyone can check that the reserve on " + F.name + " covers it."
        : "No viewing key is published yet, so nobody outside the federation can check the reserve against the supply: until one is, the backing is the signers' word.")],
      ["The cap", "The vault mints at most " + amt(w.cap) + " in all, fixed when it was installed. It refuses every mint while the signers' bonds (" + A.fmtDero(w.bondtot, 2) + " DERO) cover less than the cap at the contract's ratio of " + w.capratio +
        ", because it cannot promise more than the signers stand to lose: a small bond means a small cap. " + (covered ? "Room to mint now: " + amt(w.headroom) + "." : "Right now the bonds do not cover it, so nothing can be minted until the signers bond more.")],
      ["The vault now", halted ? "halted until block " + w.halted + ": registering and minting wait, and burning still works" : w.sealed > 0n ? "open and sealed: the deployer has no power over it, and changing the signers takes their own votes" : "NOT sealed: the deployer can still change the signer set. Do not deposit."],
    ];
  }

  // ---- the guide: five steps to deposit, four to withdraw, each saying what happens and who acts ----
  // rec: the build's record of the vault (sym, dec, conf, chain); w: its state read from the contract (D.loadVault), or
  // null before it is read, when the words stay general and no number is invented. Every value returned is a string.
  function guide(coin, dir, rec, w, H) {
    const F = FOREIGN[coin];
    if (!F || !rec) return { available: false, why: NODEP };
    const ws = "w" + F.sym, amt = (v) => units(rec, v), k = w ? String(w.k) : "k", q = w ? w.k + " of the " + w.na : "k of the n", conf = N(rec.conf) || 0, wait = w ? N(w.wdelay) : null;
    const g = { available: true, coin, dir, ws, title: (dir === "deposit" ? "Deposit " : "Withdraw ") + F.sym, custody: custody(coin, rec, w, H),
      lead: dir === "deposit" ? "Send " + F.sym + " to a deposit address of your own, and " + q + " signers mint you the same amount of " + ws + " on DERO: 1:1, with no fee on the mint."
        : "Burn " + ws + " with your " + F.name + " address, and the signers pay you the same amount of " + F.sym + ", less the withdrawal fee." };
    if (dir === "deposit") {
      g.steps = [
        S("You", "dero", "Register a deposit index", "On the Wrap screen. It costs " + (!w ? "the vault's registration fee, paid to the treasury, and" : w.regfee > 0n ? A.fmtDero(w.regfee, 5) + " DERO, paid to the treasury, and" : "no registration fee on this vault, only") +
          " the DERO network fee. The call is ring size 2: your DERO address is public in it, and stored with the index."),
        S("The signers", "dero", q + " signers publish your deposit address", "Each signer derives the same " + F.name + " address for your index and votes for it; it is live once " + k + " agree. Nothing for you to do: the Wrap screen shows the address once it is live."),
        S("You", coin, "Send " + F.sym + " to that address", "From your own " + F.name + " wallet, at least " + (w ? amt(w.mindep) : "the vault's minimum") + ": less can never be minted, and nobody can send it back. There is no maximum per deposit. " +
          "Read the room to mint just before you send: a deposit beyond it waits in the federation's wallet until there is room."),
        S("The signers", coin, (conf ? conf + " confirmations" : "Confirmations") + " on " + F.name, "The signers wait for them" + (conf ? ", about " + dur(conf * F.block) + " at " + F.name + "'s target of " + F.block + " s a block," : "") +
          " before they vote. This app cannot see " + F.name + ", so it shows nothing until the mint."),
        S("The signers", "dero", q + " signers vote Mint: " + ws + " arrives in your wallet", "The same amount you sent, 1:1, in the wallet that registered the index. The vault records each mint against the " + F.name + " payment it is for."),
      ];
      g.fees = [["Registration", !w ? "a fixed fee per index, to the treasury" : w.regfee > 0n ? A.fmtDero(w.regfee, 5) + " DERO per index, to the treasury" : "none on this vault"], ["The mint", "no fee: " + ws + " 1:1 for the " + F.sym + " you sent"],
        ["Smallest deposit", w ? amt(w.mindep) : "read from the vault"], ["Largest deposit", "no limit per deposit; the room to mint now is under Who holds the coins"], ["Network fees", "the DERO fee of the registration, and the " + F.name + " fee of your payment"]];
      g.time = ["Your address: once " + q + " signers have voted, a few DERO blocks after you register, while their daemons are running.",
        "Your payment: " + (conf ? conf + " confirmations, about " + dur(conf * F.block) + "." : "the confirmations the signers wait for."),
        "The mint: a few DERO blocks once " + k + " signers have voted.",
        "No step after your payment has a deadline in the contract: the mint waits for the signers, and nothing makes them act."];
    } else {
      const big = !!w && w.bigbps > 0n;
      g.steps = [
        S("You", "dero", "Burn " + ws + " with your " + F.name + " address", "On the Wrap screen: at least " + (w ? amt(w.minburn) : "the vault's minimum burn") + ". The vault keeps " + (w ? amt(w.wfee) : "a withdrawal fee") +
          " of it for the treasury. The destination is stored on DERO in the clear, beside the address it would come back to."),
        S("The contract", "dero", "The release height", w ? (wait ? "Payable " + blocks(wait) + " after the burn." : "Payable from the next block.") + (big ? " A burn larger than " + N(w.bigbps) / 100 + "% of the supply waits " + blocks(w.bigdelay) + " more." : "")
          : "Payable after the vault's delay, from the height recorded with the burn."),
        S("The signers", coin, "The signers pay you on " + F.name, "A signer marks it as being paid, sends the " + F.sym + " to your address, and checks its own payment. This app cannot see " + F.name + "."),
        S("The signers", "dero", q + " signers record it: Released", "They record the " + F.name + " transaction on DERO, and the claim leaves the supply. If no signer marks it within " + RECLAIM_AFTER.toLocaleString("en-US") + " blocks (about " +
          dur(RECLAIM_AFTER * 18) + ") of the release height, you can take your " + ws + " back yourself: Reclaim. If the signers cannot pay, " + k + " of them can return it to you."),
      ];
      g.fees = [["Withdrawal fee", (w ? amt(w.wfee) : "a fixed amount") + " per burn, for the treasury"], ["Smallest burn", w ? amt(w.minburn) : "read from the vault"], ["Largest burn", "no limit per burn, up to the " + ws + " in existence"],
        ["Network fees", "the DERO fee of the burn. The contract does not say whether the " + F.name + " fee of the payment comes out of what you receive: that is the signers' practice"]];
      g.time = [w ? (wait ? "Payable " + blocks(wait) + " after the burn" : "Payable from the next block") + (big ? ", or " + blocks(w.wdelay + w.bigdelay) + " for a burn above " + N(w.bigbps) / 100 + "% of the supply." : ".") : "Payable after the vault's delay.",
        "Then the signers pay and record it; the contract gives them no deadline.",
        "If no signer marks it as being paid: from " + RECLAIM_AFTER.toLocaleString("en-US") + " blocks after the release height (about " + dur(RECLAIM_AFTER * 18) + "), you can reclaim your " + ws + ".",
        "If a signer marked it: only " + k + " votes settle it, paid (Released) or returned (Veto), and a return waits " + MARK_WINDOW.toLocaleString("en-US") + " blocks (about " + dur(MARK_WINDOW * 18) + ") from the mark, because the coins may already have left."];
    }
    g.details = [["The contract", "vault.bas v3, " + rec.scid + ". Every figure on this page is read from it through your node."],
      ["The calls", dir === "deposit" ? "Register() from your wallet with the registration fee; then Attest(j, address) and Mint(j, txid, vout, amount) from the signers, each counted to " + k + " votes. Refunded(txid, vout, ftx) records a deposit the signers sent back instead."
        : "Burn(dest, refund) from your wallet, carrying the " + ws + "; Paying(j) from one signer; Released(j, ftx) and Veto(j) by " + k + " votes; Reclaim(j) by anyone, and it pays only the burner."],
      ["What is recorded", dir === "deposit" ? "ua_<j>, your address, for the index; da_<j>, dh_<j> and dver_<j> once it is attested; mint_<txid:vout>, mj_, ma_ and mh_ for each mint."
        : "w_<j>_to, _v, _f, _by, _h and _rel at the burn; _p when a signer marks it; _st and _ftx when it is Released or returned."],
      ["Privacy", "Your DERO address is stored with each index, and a burn's destination beside the address it returns to, in the clear: anyone reading the contract can link them."],
      ["This network", rec.chain ? U.clean(String(rec.chain), 120) : "the deployment record does not describe the chain"]];
    return g;
  }

  // ---- the tracker's stages: only what the vault contract records, and what it cannot see marked as such ----
  const st = (x, short) => Object.assign({ chain: "DERO", conf: null, need: null, deadline: null, txid: "", short }, x);
  const dl = (b) => ({ chain: "DERO", height: b, eta: null });
  // d: { j, addr, since, votes } — votes is this window's attestation count, null when it could not be read
  function depositStages(d, k, coin) {
    const F = FOREIGN[coin] || FOREIGN.xmr, live = !!d.addr;
    return [
      st({ label: "Deposit index #" + d.j + ", registered by your wallet", state: "done" }, "Registered"),
      st({ label: live ? "Your deposit address, attested by " + k + " signers, live since block " + d.since : "Your deposit address, waiting for " + k + " signers to attest it", state: live ? "done" : "active",
        conf: live || d.votes === null ? null : d.votes, need: live || d.votes === null ? null : N(k) }, "Address"),
      st({ label: "Your payment to it and its confirmations: the signers see these, and this app cannot", chain: F.name, state: live ? "unseen" : "waiting" }, "Your " + F.sym),
      st({ label: "The mint: the vault records each one by the " + F.name + " payment it is for, which this app cannot see, so it cannot tie a mint to this index", state: live ? "unseen" : "waiting" }, "Mint"),
    ];
  }
  // x: one burn as vault.bas stores it (w_<j>_*): units, fee, dest, burned, rel, st (0 open, 1 Released, 2 returned), ftx, marked
  function withdrawalStages(x, k, H, coin) {
    const F = FOREIGN[coin] || FOREIGN.xmr, open = x.st === 0, paid = x.st === 1, back = x.st === 2, due = H >= x.rel;
    return [
      st({ label: "Burned at block " + x.burned + ", to " + U.clean(x.dest, 60), state: "done" }, "Burned"),
      st({ label: "Payable from block " + x.rel, state: !open || due ? "done" : "active", deadline: open && !due ? dl(x.rel) : null }, "Release"),
      st({ label: x.marked ? "A signer marked it as being paid, at block " + x.marked : "A signer marks it as being paid", state: x.marked ? "done" : open ? (due ? "active" : "waiting") : "skipped" }, "Paying"),
      st({ label: "Paid on " + F.name + " and recorded by " + k + " signers" + (x.ftx ? ": " + F.name + " tx " + U.clean(x.ftx, 128) : ""), state: paid ? "done" : back ? "skipped" : "waiting" }, "Released"),
      st({ label: back ? "Returned to you, the units and the fee" : x.marked ? "Or returned by " + k + " signers' votes, from block " + (x.marked + MARK_WINDOW) : "Or reclaimed by you, from block " + (x.rel + RECLAIM_AFTER) + " if no signer marks it",
        state: back ? "done" : paid ? "skipped" : "waiting", deadline: open ? dl(x.marked ? x.marked + MARK_WINDOW : x.rel + RECLAIM_AFTER) : null }, "Returned"),
    ];
  }

  // ---- reads: the wallet's own indexes and burns in one vault, by exact key, among the newest SCAN and WSCAN ----
  async function mine(rec, w, me, H) {
    const scid = rec.scid, un = N(w.un), wn = N(w.wn), ks = ["se", "he"], lo = Math.max(0, un - SCAN), wlo = Math.max(0, wn - WSCAN);
    for (let j = lo; j < un; j++) ks.push("ua_" + j);
    for (let j = wlo; j < wn; j++) ks.push("w_" + j + "_by");
    const g = await D.fetchKeys(scid, ks);
    const isMe = (v) => { if (!hexv(v)) return false; try { return D.rawToAddr(v) === me || V.hexToStr(v) === me; } catch (e) { return false; } };
    const idx = [], wj = [];
    for (let j = un - 1; j >= lo && idx.length < 8; j--) if (isMe(g["ua_" + j])) idx.push(j);
    for (let j = wn - 1; j >= wlo && wj.length < 8; j--) if (isMe(g["w_" + j + "_by"])) wj.push(j);
    const want = [...idx.flatMap((j) => ["da_" + j, "dh_" + j, "dver_" + j]), ...wj.flatMap((j) => ["_v", "_f", "_to", "_h", "_rel", "_st", "_p", "_ftx"].map((s) => "w_" + j + s))];
    const f = want.length ? await D.fetchKeys(scid, want) : {};
    const deps = idx.map((j) => ({ j, addr: hexv(f["da_" + j]) ? U.clean(V.hexToStr(f["da_" + j]), 200) : "", since: num(f["dh_" + j]), ver: num(f["dver_" + j]) || 0, votes: null }));
    // an index not yet attested: this window's attestation votes, one key per signer (vault.bas votek, Attest)
    const wait = deps.filter((d) => !d.addr);
    if (wait.length && isNum(g.se) && isNum(g.he)) {
      try {
        const sk = []; for (let i = 0; i < N(w.n); i++) sk.push("sg_" + i);
        const sg = sk.length ? await D.fetchKeys(scid, sk) : {}, who = sk.map((x) => (hexv(sg[x]) ? D.rawToAddr(sg[x]) : null)).filter(Boolean), win = Math.floor(H / 2000);
        const key = (d, a) => "dv_" + g.se + "_" + g.he + "_" + win + "_" + d.j + "_" + d.ver + "_" + a, vk = wait.flatMap((d) => who.map((a) => key(d, a)));
        const got = vk.length ? await D.fetchKeys(scid, vk) : {};
        for (const d of wait) d.votes = who.filter((a) => hexv(got[key(d, a)])).length;
      } catch (e) {}
    }
    const W = (j, s) => f["w_" + j + "_" + s];
    const wds = wj.map((j) => ({ j, units: isNum(W(j, "v")) ? B(W(j, "v")) : 0n, fee: isNum(W(j, "f")) ? B(W(j, "f")) : 0n, dest: hexv(W(j, "to")) ? U.clean(V.hexToStr(W(j, "to")), 200) : "",
      burned: num(W(j, "h")) || 0, rel: num(W(j, "rel")) || 0, st: num(W(j, "st")) || 0, marked: num(W(j, "p")) || 0, ftx: hexv(W(j, "ftx")) ? U.clean(V.hexToStr(W(j, "ftx")), 128) : "" }));
    return { deps, wds };
  }
  let S0 = null, at = 0;
  async function load() {
    const next = {}, me = V.wallet.canon, H = tip();
    for (const rec of D.vaults()) {
      try { const w = await D.loadVault(rec.scid); next[rec.scid] = { w, deps: [], wds: [] }; if (w && me) Object.assign(next[rec.scid], await mine(rec, w, me, H)); }
      catch (e) { next[rec.scid] = { w: null, err: K().explain(e, "The " + U.clean(rec.sym || "", 8) + " vault could not be read") }; }
    }
    S0 = next; at = Date.now();
  }

  // ---- screens ----
  const facts = (list) => h("ul", { class: "swp-facts" }, list.map(([k, v]) => h("li", null, h("b", null, k + ". "), v)));
  const item = (coin, href, title, sub, steps, H) => { const d = T().due(steps, H);
    return h("li", { class: "swp-item " + coin }, h("a", { class: "swp-item-h", href }, K().chain(coin), h("b", null, title)), h("div", { class: "sub tiny" }, sub), T().chips(steps), d ? h("div", { class: "note" }, d) : null); };
  function tracker(only) {
    const H = tip(), me = V.wallet.canon, head = U.sectionHead("bolt", "Your deposits and withdrawals", "Read from the vault contract for your wallet's address. Nothing here is kept anywhere.");
    if (!me) return [head, U.emptyState("wallet", null, "Connect a wallet to list your deposit indexes and burns: each is read from the vault contract for your address.")];
    if (!S0) return [head, h("div", { class: "list" }, U.skeleton(2))];
    const rows = [], errs = [];
    for (const rec of D.vaults()) {
      const s = S0[rec.scid], coin = coinOf(rec), F = FOREIGN[coin];
      if ((only && rec.scid !== only) || !s || !F) continue;
      if (s.err) { errs.push(K().box(s.err, () => load().then(redrawNow))); continue; }
      const k = s.w ? String(s.w.k) : "k";
      for (const d of s.deps || []) rows.push(item(coin, "#wrap/" + rec.scid, "Deposit index #" + d.j + " · " + F.sym + " to w" + F.sym, d.addr ? "Your " + F.name + " deposit address: " + d.addr : "Its deposit address is not attested yet", depositStages(d, k, coin), H));
      for (const x of s.wds || []) rows.push(item(coin, "#wrap/" + rec.scid, "Withdrawal #" + x.j + ": " + units(rec, x.units) + " to " + U.clean(x.dest, 24), x.st === 1 ? "Released" : x.st === 2 ? "Returned to you" : "Under way", withdrawalStages(x, k, H, coin), H));
    }
    return [head, errs, rows.length ? [h("ul", { class: "swp-items" }, rows), h("div", { class: "note mt8" }, T().LEGEND)]
      : U.emptyState("bolt", null, "No deposit index among the newest " + SCAN + ", and no burn among the newest " + WSCAN + ", names your wallet.")];
  }
  const crumb = (coin, dir) => h("nav", { class: "swp-crumb mt", "aria-label": "Where you are" }, h("a", { href: "#move" }, "Deposit & withdraw"), " / ", h("a", { href: "#move/" + coin }, FOREIGN[coin].sym), dir ? " / " + (dir === "deposit" ? "Deposit" : "Withdraw") : null);
  function hub(coin) {
    const H = tip(), rec = coin ? vaultFor(coin) : null, s = rec && S0 ? S0[rec.scid] : null;
    return [h("h1", { class: "mt" }, "Deposit & withdraw"),
      h("p", { class: "swp-lead" }, h("b", null, "Bring XMR or ZEC to DERO 1:1, and take it back out."), " You send it to a deposit address of your own and receive the same amount of wXMR or wZEC; you burn that to withdraw. " +
        "A federation of named signers holds the XMR and ZEC. There is no size limit per deposit: what can be minted at any moment is the vault's room to mint."),
      h("h2", { class: "mt" }, "1 · Choose a coin"),
      h("div", { class: "swp-choose" }, D.vaults().map((r) => { const c = coinOf(r), x = S0 && S0[r.scid] && S0[r.scid].w; if (!c) return null;
        return h("a", { class: "swp-pick " + c + (coin === c ? " on" : ""), href: "#move/" + c, "aria-current": coin === c ? "true" : null }, h("span", { class: "row tight" }, K().chain(c), h("b", null, FOREIGN[c].sym + " and w" + FOREIGN[c].sym)),
          h("span", { class: "sub tiny" }, x ? "1:1 · " + x.k + " of " + x.na + " signers · room to mint now: " + units(r, x.headroom) : "1:1 · read from the vault contract")); })),
      coin && !rec ? h("div", { class: "warnbox mt" }, "This build names no vault for " + FOREIGN[coin].sym + ", so it cannot be deposited or withdrawn here.") : null,
      rec ? [h("h2", { class: "mt" }, "2 · Choose a direction"), h("div", { class: "swp-choose" }, ["deposit", "withdraw"].map((d) => h("a", { class: "swp-pick " + coin, href: "#move/" + coin + "/" + d },
        h("b", null, (d === "deposit" ? "Deposit " : "Withdraw ") + FOREIGN[coin].sym), h("span", { class: "sub tiny" }, d === "deposit" ? "Send " + FOREIGN[coin].sym + ", receive w" + FOREIGN[coin].sym + " 1:1" : "Burn w" + FOREIGN[coin].sym + ", receive " + FOREIGN[coin].sym)))),
        h("h2", { class: "mt" }, "Who holds the coins"), s && s.err ? K().box(s.err) : facts(custody(coin, rec, s ? s.w : null, H))] : null,
      tracker()];
  }
  function guidePage(coin, dir) {
    const rec = vaultFor(coin), s = rec && S0 ? S0[rec.scid] : null, g = guide(coin, dir, rec, s ? s.w : null, tip());
    if (!g.available) return [h("h1", { class: "mt" }, "Deposit & withdraw"), h("div", { class: "warnbox mt" }, h("b", null, D.vaults().length ? "This build names no vault for " + FOREIGN[coin].sym + ", so it cannot be deposited or withdrawn here." : g.why))];
    return [crumb(coin, dir), h("div", { class: "between mt8" }, h("h1", null, g.title), K().chain(coin)), h("p", { class: "swp-lead" }, g.lead), s && s.err ? K().box(s.err) : null,
      h("h2", { class: "mt" }, "The steps"),
      h("ol", { class: "swp-guide" }, g.steps.map((x, i) => h("li", { class: "swp-gstep" }, h("span", { class: "n", "aria-hidden": "true" }, String(i + 1)),
        h("div", { class: "grow" }, h("div", { class: "swp-who" }, h("span", { class: "label" }, x.who), K().chain(x.chain)), h("div", { class: "t" }, x.t), h("div", { class: "sub mt8" }, x.d))))),
      h("div", { class: "row mt" }, h("a", { class: "btn primary", href: "#wrap/" + rec.scid }, dir === "deposit" ? "Register on the Wrap screen" : "Burn on the Wrap screen")),
      h("h2", { class: "mt" }, "Fees and limits"), h("div", { class: "swp-limits" }, g.fees.map(([a, b]) => kv(a, b))),
      h("h2", { class: "mt" }, "How long it takes"), h("ul", { class: "swp-facts" }, g.time.map((t) => h("li", null, t))),
      h("h2", { class: "mt" }, "Who holds the coins"), facts(g.custody),
      U.details("Technical details", g.details.map(([a, b]) => h("div", { class: "mt8" }, h("b", null, a + ": "), h("span", { class: "sub" }, b)))),
      tracker(rec.scid)];
  }

  let redrawNow = () => {};
  router.on("move", async (el, param) => {
    const [c, d] = String(param || "").split("/"), coin = FOREIGN[c] ? c : "", dir = coin && (d === "deposit" || d === "withdraw") ? d : "";
    if (!D.vaults().length) return render(el, h("h1", { class: "mt mb" }, "Deposit & withdraw"), V.wrapui.noVault());
    const here = () => !!router.current && router.current.name === "move" && router.current.param === param;
    const draw = () => render(el, dir ? guidePage(coin, dir) : hub(coin));
    redrawNow = () => { if (here()) draw(); };
    draw();
    try { await load(); } catch (e) {}
    redrawNow();
    // one read per block while this screen is open: the stages count down with the chain
    D.onChange(() => { if (here() && Date.now() - at > 4000) load().then(redrawNow, () => {}); });
  });

  V.move = { guide, custody, depositStages, withdrawalStages, RECLAIM_AFTER, NODEP };
})();
