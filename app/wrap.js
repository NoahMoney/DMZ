// DMZ wrap.js — the wrapped-asset screen: deposit a foreign coin, hold a DERO asset backed one for one, burn it
// to withdraw. Everything here is read from the vault contract; nothing is assumed. The two numbers a user is
// most often surprised by — the minimum deposit and the height a withdrawal becomes payable — are published in
// the place the decision is made, because a rule you can see is a rule and a rule you cannot see is a trap.
//
// Honesty rules this screen follows:
//   - no SAFE badge and no green shield: the federation is named, counted and its bond is shown, and the fact
//     that k of them can move the reserve is stated in plain words on the screen, not in a linked document;
//   - Register and Burn call SIGNER(), which is real only at ring size 2, so the calling address is public. That
//     is said before the button, not after the transaction;
//   - amounts are foreign base units. The decimals come from the deployment record (vaultAsset.dec), never from
//     a guess, and when the record is missing the raw base units are shown as-is.
(function () {
  // vault.bas Reclaim: an unmarked burn may be taken back this many blocks after its release height
  const RECLAIM_AFTER = 20000;
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, B = BigInt;
  const { h, button, kv, icon, router, toast } = U, WK = () => V.wrapkit;
  // every screen here is drawn inside .swp, whose controls are at least 40 px tall (app/parts.css, ui-swaps). Failures
  // and the confirmation before a call that moves money are app/wrapkit.js's, the same words as Deposit & withdraw.
  const render = (el, ...n) => U.render(el, h("div", { class: "swp" }, ...n));
  const me = () => V.wallet.canon;
  const Z = 0n;
  // amounts, network marks, the settlement panel and walkthrough, and the no-vault screen are wrapui.js; the
  // vault's data layer (D.loadVault, D.myVault, D.vaultWithdrawals) is vault.js. Both load before this file.
  const { dec, amt, exact, blocksAway, netOf, mark, arrow, flag, panel, steps, full, noVault } = V.wrapui;

  // ---- screen ----

  // the tip drives the "payable in N blocks" countdown; live.js already polls it, so read that rather than
  // asking the node again on every draw
  let tip = 0;
  const refreshTip = async () => { if (V.live && V.live.height) { tip = V.live.height; return; } try { tip = await V.node.height(); } catch (e) {} };

  // #wrap/<scid> opens that vault; #wrap opens the first one recorded for this build
  router.on("wrap", async (el, param) => {
    const title = () => h("h1", { class: "mt mb" }, "Wrapped assets");
    if (!D.allVaults().length) return render(el, title(), noVault());
    // only a vault this build ships opens by id: any other contract would borrow a shipped vault's words (MISS-01)
    const picked = D.allVaults().some((v) => v.scid === param) ? param : null;
    if (param && !picked) return render(el, title(), h("div", { class: "errbox" }, "This build ships no vault at " + U.clean(param, 64) + ", so nothing about it is shown and nothing can be signed."));
    const draw = (w, my, err) => {
      if (err) return render(el, title(), WK().box(err, () => reload(true)));
      if (!w) return render(el, title(),
        U.emptyState("wallet", "The vault could not be verified",
          "This build names a vault, but the contract at that id is not a version-3 vault this app reads. Nothing about it is shown, and nothing can be signed."));

      const a = w.asset || {}, name = D.chainName(a), chain = D.chainName(a);
      // halted is the height the lease lapses at (0: not halted); a halt nobody renews expires on its own
      const halted = w.halted > BigInt(D.height || 0);
      // Whether a deposit arriving now could be minted at all. The pill read OPEN off `halted` alone, so a vault
      // at its cap said Open while every Mint vote against a new deposit was refused - and the deposit walkthrough
      // three sections below invited one anyway. Over the cap is not a refusal: the signers' daemon waits, so the
      // coins sit in the federation's foreign wallet with no claim and no contract able to return them.
      const canMint = !halted && w.headroom >= w.mindep && w.headroom > Z;
      const mine = my && my.withdrawals ? my.withdrawals : [];
      const dep = my && my.dep && my.dep.addr ? my.dep : null;
      const nc = netOf(a);
      // the two ends of every settlement on this screen, and the mechanism between them
      const c = {
        chain,
        fed: w.k + "-of-" + w.na + " federation vault",
        foreign: () => mark(nc[0], nc[1], chain),
        dero: () => mark("DR", "net", "DERO"),
      };
      const depStatus = !V.wallet.connected
        ? "no wallet connected, so no index and no address"
        : dep
          ? "address live since block " + dep.since + (a.conf ? ", credited after " + a.conf + " confirmations" : "")
          : my && my.indexes > 0
            ? "index registered, awaiting " + w.k + " signer attestations"
            : "no deposit index registered on this wallet";

      // One vault holds one foreign asset, so a build wrapping Monero and Zcash runs two of them and the user
      // has to be able to say which. With a single vault there is nothing to choose and no strip is drawn.
      const all = D.vaults();
      const picker = all.length < 2 ? null : h("div", { class: "chips scrollx" }, all.map((v) =>
        h("button", {
          class: "chip " + (v.scid === w.scid ? "on" : ""), type: "button",
          onclick: () => router.go("wrap", v.scid),
        }, U.clean(v.sym || v.name || "wrapped", 10))));

      render(el,
        h("div", { class: "between mt" },
          h("h1", null, "Deposit & withdraw " + (a.sym ? U.clean(a.sym, 12) : "assets")),
          null),
        picker,
        lastErr ? WK().box(lastErr) : null,
        h("div", { class: "row mt8" }, h("a", { class: "btn small ghost", href: "#move/" + nc[1].slice(4) }, "Step-by-step guide"), h("a", { class: "btn small ghost", href: "#move" }, "Your deposits and withdrawals")),

        // ---- Level 1: what the vault holds, which way it settles, and whether it is open ----
        h("div", { class: "hero cut mt8" },
          h("div", { class: "between" },
            h("span", { class: "eyebrow" }, "Custodial vault · a federation holds the " + (a.sym ? U.clean(a.sym, 12) : "coins")),
            flag(halted ? "down" : canMint ? "acc" : "warn", halted ? "Halted" : canMint ? "Open" : "Deposits paused")),
          h("div", { class: "row tight mt8", style: "flex-wrap:wrap;gap:6px 14px" }, c.foreign(), arrow(), c.dero()),
          h("div", { class: "pulse-row mt" },
            h("div", null, h("div", { class: "eyebrow" }, "Claims outstanding"), h("div", { class: "big num mt8" }, exact(w.sup))),
            h("div", { class: "row", style: "gap:22px;flex-wrap:wrap" },
              // exact, never "<0.0001": a small cap is the size of the signers' bonds, and rounding it away read as a fault
              h("div", { class: "mini" }, h("span", { class: "l" }, "Mint cap"), h("span", { class: "v" }, exact(w.cap))),
              h("div", { class: "mini" }, h("span", { class: "l" }, "Room to mint"), h("span", { class: "v" }, exact(w.headroom))),
              h("div", { class: "mini" }, h("span", { class: "l" }, "Signers"), h("span", { class: "v" }, w.k + " of " + w.na))))),

        // ---- what this is, in the words that are actually true ----
        h("div", { class: "note mt8" },
          "Send " + name + " to your own deposit address. When " + w.k + " of the " + w.na +
          " active signers have both seen it, the vault mints you the same number of base units as a DERO asset. " +
          "Burn those units with a destination and the same quorum pays you back on " + chain + "."),
        // The deployment record's own words about this chain, whole and on their own line. Spliced into a
        // sentence and cut to fit, they read as broken software on the screen where irreversible money is sent.
        a.chain ? h("div", { class: "sub tiny mt8" }, U.clean(a.chain, 120)) : null,

        halted ? h("div", { class: "warnbox mt" },
          h("b", null, "This vault is halted."),
          h("div", { class: "sub" }, "Registering and minting are refused while the signers report a problem, and no new payment starts. " +
            "Burning still works and queues behind the usual delay; a withdrawal already paid still gets recorded. " +
            "A halt is a lease: it lapses at block " + w.halted + " (" + blocksAway(Number(w.halted - BigInt(D.height || 0))) + ") unless the signers renew it, " +
            "so a federation that stops answering cannot leave it halted.")) : null,

        // ---- deposit ----
        h("h3", { class: "mt" }, "Deposit"),
        // The minimum is a property of the vault, not of the visitor, so it is published before the wallet is
        // connected and before an address exists. A deposit below it can never be minted and no operator exists
        // to send it back: someone deciding whether to use this vault at all has to be able to see that first.
        // The floor and the ceiling in one box, because they fail the same way: a deposit outside either can
        // never be minted and nobody can send it back. The ceiling moves with everyone else's deposits, which
        // the floor does not, so it is the one to read again just before sending.
        // The limits and the fees as data, not as an alarm: the cap is the size of the federation's bonds, which is
        // what it is doing, so it is explained rather than flagged. The floor is still said plainly, because a
        // deposit below it is the one mistake nobody can undo.
        h("div", { class: "card mt8 swp-limits" },
          h("div", { class: "label" }, "Limits and fees"),
          kv("Smallest deposit", exact(w.mindep)), kv("Room to mint now", canMint ? exact(w.headroom) : "none: nothing can be minted until enough is burned"),
          kv("Largest deposit", "no limit per deposit"), kv("Registration", w.regfee > Z ? A.fmtDero(w.regfee, 5) + " DERO per index, to the treasury" : "none on this vault"),
          kv("Withdrawal fee", exact(w.wfee) + " per burn"), kv("Smallest burn", exact(w.minburn)),
          h("div", { class: "note mt8" }, "A deposit below the smallest can never be minted, and nobody can send it back. A deposit beyond the room to mint is not refused: it waits in the federation's wallet until enough is burned to make room. " +
            "The signers may send it back instead, which the contract records, but nothing makes them." + (w.winmax > Z ? " This vault also mints at most " + amt(w.winmax, 4) + " per window, which can bind before the cap does." : "")),
          h("div", { class: "note mt8" }, h("b", null, "Why there is a cap. "), "The vault mints at most " + exact(w.cap) + " in all, fixed when it was installed, and refuses every mint while the signers' bonds (" + A.fmtDero(w.bondtot, 2) +
            " DERO) cover less than the cap at the contract's ratio of " + w.capratio + ". It cannot promise more than the signers stand to lose, so a small bond means a small cap: the cap is how much this federation has put up, not a fault.")),

        h("div", { class: "mt" }, panel(c, c.foreign(), c.dero(), "the federation holds the reserve on " + chain, depStatus)),

        (() => {
          const connected = V.wallet.connected, registered = !!(my && my.indexes > 0), live = !!dep;
          const held = my && my.units ? my.units : Z;
          return steps([
            {
              state: !connected ? "now" : registered ? "done" : "now",
              title: "Register a deposit index",
              detail: !connected
                ? "A deposit address belongs to one index of one wallet. Connect a wallet to claim one."
                : registered
                  ? "Index " + (dep ? dep.j : (my && my.dep ? my.dep.j : "?")) + ". Registering again gives you a fresh index and a fresh deposit address, but not a fresh identity: the vault stores which DERO address owns every index and the foreign address attested to it, in the clear, so anyone reading the contract can link all of yours together."
                  : "Costs " + A.fmtDero(w.regfee, 5) + " DERO to the treasury. This is a ring-size-2 call, so your address is public in that transaction, and it is stored against the index where anyone can read it.",
              body: h("div", { class: "mt8" }, !connected
                ? button("Connect wallet", "primary", () => V.app.connectWallet())
                : button(registered ? "Register another index (" + A.fmtDero(w.regfee, 5) + " DERO)" : "Register a deposit index",
                    registered ? "small" : "primary", () => doRegister(w), canMint ? null : { disabled: true })),
            },
            {
              state: live ? "done" : registered ? "wait" : "todo",
              title: w.k + " signers publish your address on " + chain,
              detail: live
                ? "Live since block " + dep.since + ". Anything paid to it before that block is ignored by every honest signer."
                : registered
                  ? "Nothing for you to do. Each signer derives the same address and votes for it; the address exists once " + w.k + " of them agree."
                  : "Available once you hold an index.",
            },
            {
              state: live ? "now" : "todo",
              title: canMint ? "Send between " + exact(w.mindep) + " and " + exact(w.headroom) + " to that address" : "Nothing can be minted right now — do not send",
              detail: !canMint
                ? (halted ? "The vault is halted, so no deposit can be minted while it stays that way." : "The vault is at its mint cap. Anything sent now waits in the federation's wallet until enough is burned to make room, and no contract can return it.")
                : live
                  ? "Send from your own " + name + " wallet. Less than the minimum can never be minted; more than the headroom is not returned either, it waits — and the headroom moves with everyone else's deposits, so read it again immediately before you send."
                  : "The address appears here once the signers have published it.",
              body: live ? full("Your deposit address on " + chain, dep.addr, "Deposit address copied") : null,
            },
            {
              state: live ? "wait" : "todo",
              title: (a.conf ? a.conf + " confirmations" : "Confirmations") + " on " + chain,
              detail: "This app cannot see " + chain + ", so it cannot show your payment arriving. The signers watch for it and wait out the confirmation policy before they will vote.",
            },
            {
              state: held > Z ? "done" : live ? "wait" : "todo",
              title: w.k + " signers mint your wrapped units",
              detail: held > Z
                ? "You hold " + amt(held, 8) + " of wrapped " + (a.sym || "units") + "."
                : "One vote each, on the exact deposit they saw. The units arrive in the wallet that registered the index.",
            },
          ]);
        })(),

        // ---- withdraw ----
        h("h3", { class: "mt" }, "Withdraw"),
        h("div", { class: "stack mt8" }, burnForm(w)),

        // ---- your withdrawals, each read as a settlement ----
        mine.length ? h("h3", { class: "mt" }, "Your withdrawals") : null,
        mine.length ? mine.map((x) => wrow(w, x, c)) : null,

        // ---- the factual trust panel ----
        h("h3", { class: "mt" }, "What you are trusting"),
        h("div", { class: "card stack" },
          trustLine(false, w.k + " of the " + w.na + " signers can move the whole foreign reserve. They hold the keys to it; this contract cannot."),
          trustLine(true, "The DERO side is verifiable by anyone: " + exact(w.sup) + " of claims exist and the contract will not mint past " + exact(w.cap) + "."),
          trustLine(w.viewkey !== "", w.viewkey
            ? "A viewing key is published, so anyone can check the reserve on " + chain + " really covers the claims."
            : "No viewing key is published yet, so the reserve on " + chain + " cannot be checked by an outsider. Until it is, the backing is a claim, not a proof."),
          // Slash refuses a self-vote and needs k votes, so with n signers and a quorum of k the k who could take
          // the reserve are exactly the k needed to slash anybody: below n >= 2k nobody in a colluding quorum can
          // be slashed at all. This line carried a green tick in precisely that shape, two lines under the red
          // cross that says those same k can move the whole reserve.
          trustLine(w.bondtot > Z && w.na >= 2n * w.k, w.bondtot === Z
            ? "No signer bond is at stake."
            : w.na >= 2n * w.k
              ? "Signers have bonded " + A.fmtDero(w.bondtot, 2) + " DERO against a mint cap of " + exact(w.cap) + ". A quorum of the others can slash a signer's bond to the treasury — not to you."
              : "Signers have bonded " + A.fmtDero(w.bondtot, 2) + " DERO against a mint cap of " + exact(w.cap) + ", but with " + w.na + " signers and a quorum of " + w.k + " the bond deters nobody: a signer cannot vote to slash itself, so the " + w.k + " who could take the reserve are the " + w.k + " needed to slash anyone. A slashed bond goes to the treasury, never to you."),
          trustLine(w.sealed !== Z, w.sealed !== Z
            ? "The vault is sealed: the deployer has no power over it and nobody can add a signer without a quorum."
            : "The vault is NOT sealed. The deployer can still change the signer set. Do not deposit."),
          h("div", { class: "note" },
            "There is no operator and no override, and nothing here pays by itself: a burn is paid by " + w.k + " signers recording the foreign transaction, or returned by " +
            w.k + " signers voting to, or — " + RECLAIM_AFTER + " blocks after it became payable, if no signer has marked it as being paid — returned to you by the contract when you ask. " +
            "Only a burn a signer has marked as being paid waits on the quorum, because its coins may already have left.")),
        full("Vault contract", w.scid, "Vault contract id copied"));
    };

    const trustLine = (ok, txt) => h("div", { class: "row tight", style: "align-items:flex-start" },
      icon(ok ? "check" : "x", "svgi " + (ok ? "up" : "down")), h("span", { class: "sub" }, txt));

    // ---- burn form ----
    const burnForm = (w) => {
      const amount = h("input", { inputmode: "decimal", placeholder: "0.0", autocomplete: "off", spellcheck: "false" });
      const dest = h("input", { placeholder: "Your address on " + D.chainName(w.asset), autocomplete: "off", spellcheck: "false" });
      const out = h("div", { class: "note" });
      const btn = button("Burn and withdraw", "primary", () => doBurn(w, amount, dest));
      // what the wallet holds of the wrapped asset, read for this form: null while being read, undefined when the
      // wallet did not answer — an amount it would refuse is never offered, and unread counts as not enough
      let have = null;
      if (V.wallet.connected) V.wallet.getBalance(w.scid, true).then((b) => { have = V.toBig(b && b.balance) || Z; restate(); }, () => { have = undefined; restate(); });
      const restate = () => {
        const units = A.parseUnits(amount.value, dec());
        btn.disabled = false; btn.textContent = "Burn and withdraw";
        if (units === null || units <= Z) { out.textContent = ""; return; }
        if (units < w.minburn) { out.textContent = "Below the minimum burn of " + exact(w.minburn) + "."; return; }
        if (V.wallet.connected && have === null) { btn.disabled = true; btn.textContent = "Reading your balance…"; }
        else if (V.wallet.connected && (have === undefined || units > have)) { btn.disabled = true; btn.textContent = have === undefined ? "Balance unreadable" : "Not enough in your wallet: " + amt(units - have, 8) + " short"; }
        const net = units > w.wfee ? units - w.wfee : Z;
        const big = w.bigbps !== Z && units > w.bigAt;
        const waitBlocks = Number(w.wdelay) + (big ? Number(w.bigdelay) : 0);
        out.textContent = "You receive " + amt(net, 8) + " after a " + exact(w.wfee) + " fee" +
          (big ? ". This is larger than " + amt(w.bigAt, 4) + " (" + (Number(w.bigbps) / 100) + "% of claims), so it waits " + waitBlocks + " blocks — " + blocksAway(waitBlocks) + " — instead of " + w.wdelay + "."
               : ". It becomes payable " + w.wdelay + " blocks after the burn, " + blocksAway(Number(w.wdelay)) + ".");
      };
      amount.oninput = restate;
      return [
        h("label", { class: "field" }, h("span", null, "Amount to unwrap"), amount),
        h("label", { class: "field" }, h("span", null, "Destination on " + D.chainName(w.asset)), dest),
        out,
        // Burn never calls SIGNER(): it takes the return address as an argument, deliberately, so that a burn can
        // be made at any ring size. Publishing this wallet beside the destination is therefore this app's choice
        // - made so a vetoed or reclaimed withdrawal comes back to the wallet that made it - and not a rule of
        // the contract. Saying it was the contract's rule told the user the alternative did not exist.
        h("div", { class: "note" },
          "The destination you type is stored on DERO in the clear, and so is the address a vetoed or reclaimed withdrawal comes back to. " +
          "This app sends the burn at ring size 2 and names your connected wallet as that return address, so a vetoed or reclaimed withdrawal returns to you — which publishes this wallet beside the destination, permanently. " +
          "The contract does not require it: Burn never reads who signed. Use a destination you are willing to see linked to this wallet."),
        btn,
      ];
    };

    // ---- one withdrawal, read as a settlement ----
    // Not a card and not a badge: a hairline, then what is moving, by what mechanism, who is holding it, and the
    // height from which it may be paid. The destination and the foreign txid are printed in full because
    // they are the parts a person can check against their own wallet.
    const wrow = (w, x, c) => {
      const away = x.rel - tip;
      // Reclaim: the burner's own exit when no signer has acted. Open from RECLAIM_AFTER blocks past the release
      // height, and only for a withdrawal no signer has ever marked as being paid.
      const reclaimAt = x.rel + RECLAIM_AFTER, canReclaim = x.st === 0 && !x.marked && tip >= reclaimAt;
      const st = x.st === 1 ? ["up", "Settled"] : x.st === 2 ? ["warn", "Returned"] : canReclaim ? ["warn", "Reclaimable"] : away > 0 ? ["fg3", "Awaiting settlement"] : ["acc", "Payable now"];
      const status = x.st === 1
        ? "settled on " + c.chain
        : x.st === 2
          ? "the units and the fee were returned to you — " + w.k + " signers voted to return it (the contract records no reason), or you reclaimed it after the window"
          : canReclaim
            ? "no signer has marked this as being paid and the window has passed: you can take the units back yourself"
            : away > 0
              ? "awaiting settlement, payable at block " + x.rel + " — " + blocksAway(away) + " away"
              : x.marked
                ? "a signer marked it as being paid at block " + x.marked + "; the payment is in flight and only " + w.k + " signers can settle it now"
                : "payable now, since block " + x.rel + ". The signer daemons pay it unasked, but only " + w.k + " votes settle it; from block " + reclaimAt + " you can reclaim it yourself as long as no signer has marked it.";
      return h("div", { class: "mt", style: "padding-top:12px;border-top:1px solid var(--edge)" },
        h("div", { class: "between", style: "padding-bottom:6px" },
          h("span", { class: "row tight" }, h("span", { class: "mono dim2" }, "#" + x.j), h("b", { class: "num" }, amt(x.units, 8))),
          flag(st[0], st[1])),
        panel(c, c.dero(), c.foreign(), "the federation pays it from the reserve", status),
        // the same walkthrough as a deposit, read backwards: what has happened, what is waiting, what is left
        steps([
          { state: "done", title: "Burned on DERO", detail: "Block " + x.burned + ". The claim leaves the supply when the payment is recorded, not now." },
          {
            state: x.st !== 0 || away <= 0 ? "done" : "wait",
            title: "Release delay",
            detail: x.st !== 0 || away <= 0
              ? "Payable since block " + x.rel + "."
              : "Payable at block " + x.rel + ", " + blocksAway(away) + " away. A larger withdrawal waits longer; the wait always ends.",
          },
          {
            state: x.st === 1 ? "done" : x.st === 2 ? "todo" : away > 0 ? "todo" : "wait",
            title: "The federation pays you on " + c.chain,
            detail: x.st === 2
              ? "Not completed: the quorum returned it, or nobody acted and you reclaimed it."
              : x.marked
                ? "A signer marked it as being paid at block " + x.marked + ". It pays, checks its own payment on " + c.chain + ", and " + w.k + " signers vote."
                : "Each signer pays, checks its own payment on " + c.chain + ", and votes. From block " + reclaimAt + ", if no signer has marked it, the units are yours to reclaim.",
          },
          {
            state: x.st === 1 ? "done" : x.st === 2 ? "done" : canReclaim ? "now" : "todo",
            title: x.st === 2 ? "Units returned to you" : canReclaim ? "Reclaim your units" : "Settled",
            body: canReclaim ? button("Reclaim " + amt(x.units + x.fee, 8), "primary", () => doReclaim(w, x)) : null,
            detail: x.st === 1
              ? "Recorded on DERO against the foreign transaction below."
              : x.st === 2
                ? "Back with the address that burned them: returned by " + w.k + " signers' votes, which record no reason, or by the contract when you reclaimed."
                : "Recorded on DERO once " + w.k + " signers agree on the transaction that paid you.",
          },
        ]),
        kv("Burned at block", String(x.burned)),
        x.fee > Z ? kv("Withdrawal fee", amt(x.fee, 8)) : null,
        full("Destination on " + c.chain, x.dest, "Destination copied"),
        x.ftx ? full("Foreign settlement txid", x.ftx, "Transaction id copied") : null);
    };

    // ---- actions ----
    // every call that moves money is confirmed first (wrapkit.confirm), and a failure is kept (lastErr) and drawn at
    // the top of the screen in words, with what was reported behind a toggle
    const fail = (r) => { lastErr = r.err; draw(D.vault, lastMine); };
    const doRegister = (w) => WK().confirm("Register a deposit index?", [["Chain", WK().chain("dero", "vault " + V.shortAddr(w.scid))],
      ["It costs", (w.regfee > Z ? A.fmtDero(w.regfee, 5) + " DERO to the treasury, and " : "no registration fee on this vault, only ") + "the DERO network fee your wallet shows"], ["You get", "a fresh index; " + w.k + " signers then publish its deposit address on " + D.chainName(w.asset)],
      ["Public", "ring size 2: your DERO address is public in this call, and stored with the index"]], null, "Sign and register", async () => {
      lastErr = null;
      // un counts everyone's indexes; this wallet's own count (uc_<its raw key>, read by D.vaultIndexes) moves only when
      // its own Register executes. Unreadable beforehand, nothing is checked, and the toast says so rather than guess.
      const n0 = await D.vaultIndexes(w.scid, me()).catch(() => null);
      const r = await WK().tx({
        scid: w.scid, entry: "Register", args: [], dero: w.regfee,
        describe: "Register a deposit index", probe: n0 === null ? null : { scid: w.scid, keys: ["un"], check: async () => (await D.vaultIndexes(w.scid, me())) > n0 },
      });
      if (r.ok) { toast("Index registered. Signers must attest it before it can receive.", "ok"); reload(true); } else fail(r);
    });

    // The contract pays only w_<j>_by, so this is safe to send from any wallet; sending it from the burner's is
    // simply the natural place. Nothing is attached and SIGNER() is not read, so ring 16 is fine.
    const doReclaim = (w, x) => WK().confirm("Reclaim withdrawal #" + x.j + "?", [["Chain", WK().chain("dero", "vault " + V.shortAddr(w.scid))], ["It pays you", amt(x.units + x.fee, 8) + ", the units and the fee"],
      ["Why now", "no signer marked it as being paid, and " + RECLAIM_AFTER + " blocks have passed since it became payable"], ["Fee", "the DERO network fee your wallet shows"]], null, "Sign and reclaim", async () => {
      lastErr = null;
      const r = await WK().tx({
        scid: w.scid, entry: "Reclaim", args: [["j", "U", BigInt(x.j)]], ring: 16,
        describe: "Reclaim withdrawal #" + x.j + " — " + amt(x.units + x.fee, 8) + " back to you",
        // st becomes 2 only by a return; the key alone would also move if the signers settled it meanwhile
        probe: { scid: w.scid, keys: ["w_" + x.j + "_st"], check: (a, b) => b.vals["w_" + x.j + "_st"] === "2" },
      });
      if (r.ok) { toast("Reclaimed. The units and the fee are back in your wallet.", "ok"); reload(true); } else fail(r);
    });
    const doBurn = async (w, amountEl, destEl) => {
      const units = A.parseUnits(amountEl.value, dec());
      const dst = String(destEl.value || "").trim();
      if (units === null || units <= Z) return toast("Enter an amount", "err");
      if (units < w.minburn) return toast("Below the minimum burn of " + exact(w.minburn), "err");
      // the contract enforces these; refusing here saves the fee of a transaction that would revert
      if (dst.length < 16 || dst.length > 200) return toast("That destination is not a plausible address", "err");
      if (/[\s\u0000-\u001f\u007f]/.test(dst)) return toast("The destination contains whitespace or control characters", "err");
      const big = w.bigbps !== Z && units > w.bigAt, wait = Number(w.wdelay) + (big ? Number(w.bigdelay) : 0), chain = D.chainName(w.asset);
      WK().confirm("Burn and withdraw?", [["You burn", amt(units, 8) + " on DERO"], ["You receive", amt(units > w.wfee ? units - w.wfee : Z, 8) + " on " + chain + ", sent by the signers"],
        ["To", h("span", { class: "mono" }, dst)], ["Payable", wait ? blocksAway(wait) + " after the burn (" + wait + " blocks)" : "from the next block"], ["Fee", exact(w.wfee) + " withdrawal fee, and the DERO network fee"],
        ["Public", "the destination and this wallet are stored on DERO together, in the clear"]], null, "Sign and burn", async () => {
        lastErr = null;
        const r = await WK().tx({
          scid: w.scid, entry: "Burn", args: [["dest", "S", dst], ["refund", "S", me()]], asset: { scid: w.scid, amount: units },
          describe: "Burn " + amt(units, 8) + " and withdraw",
          // wn counts everyone's burns: this one executed only if a withdrawal opened since then names this wallet. w_<j>_by
          // is ADDRESS_RAW(refund), 66 hex characters from the node, compared with this wallet's raw key
          probe: { scid: w.scid, keys: ["wn"], check: async (a, b) => { const raw = V.addressRaw(me()), ks = []; for (let j = +a.vals.wn || 0; j < (+b.vals.wn || 0) && ks.length < 16; j++) ks.push("w_" + j + "_by"); const g = ks.length ? await D.fetchKeys(w.scid, ks) : {}; return !!raw && ks.some((k) => g[k] === raw); } },
        });
        if (r.ok) { amountEl.value = ""; toast("Burned. It becomes payable at the height shown below.", "ok"); reload(true); } else fail(r);
      });
    };

    // ---- load ----
    let lastMine = null, lastErr = null;
    const reload = async (force) => {
      try {
        await refreshTip();
        const want = picked || (D.vaults()[0] || {}).scid;
        const w = force || !D.vault || (D.vault.scid !== want && want) ? await D.loadVault(want) : D.vault;
        const my = w && V.wallet.connected ? await D.myVault(me()) : null;
        lastMine = my;
        draw(w, my);
      } catch (e) {
        console.error(e);
        draw(D.vault, null, WK().explain(e, "The vault could not be read"));
      }
    };

    render(el, h("h1", { class: "mt" }, "Wrapped assets"), h("div", { class: "list mt" }, U.skeleton(3)));
    reload(false);
    // redraw on each new block so the countdown to a release height actually counts down
    // Every block changes the data, and a redraw rebuilds the deposit and burn forms from scratch — which wiped an
    // amount or a destination address mid-typing (it took the render pass to notice). While any field on this
    // screen holds text, the screen is the user's: the redraw waits until the forms are empty again.
    const busy = () => [...el.querySelectorAll("input")].some((i) => i.value && i.value.trim());
    D.onChange(() => { if (router.current && router.current.name === "wrap" && D.vault && !busy()) { tip = (V.live && V.live.height) || tip; draw(D.vault, lastMine); } });
  });
})();
