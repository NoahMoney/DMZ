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
  const { h, render, button, kv, icon, router, toast } = U;
  const me = () => V.wallet.canon;
  const Z = 0n;

  // ---- data ----

  const NUM = ["k", "n", "na", "sup", "cap", "capratio", "bondtot", "minbond", "minburn", "wfee", "mindep",
    "regfee", "wdelay", "winmax", "bigbps", "bigdelay", "halted", "sealed", "un", "wn", "feepool", "v"];
  const isNum = (x) => typeof x === "string" && /^\d+$/.test(x);
  const num = (vals, k) => (isNum(vals[k]) ? B(vals[k]) : Z);

  Object.assign(D, {
    vault: null,
    // The vault is not a factory listing: its id comes from the deployment record baked into config.js, and the
    // contract is asked to confirm what it is (kind, v). A vault whose kind or version does not match is not
    // shown at all, because every key name below would mean something else.
    async loadVault(pick) {
      const list = this.vaults();
      const scid = pick || (list[0] && list[0].scid) || this.cfg.vault;
      // the record is the deployment's own for THIS id, superseded vaults included: borrowing another vault's
      // record painted a retired 5-decimal vault, or any stranger's v3 contract, as the live XMR one (MISS-01)
      const rec = this.allVaults().find((v) => v.scid === scid);
      if (!rec) return null;
      const r = await V.node.getSC(scid, { keys: [...NUM, "kind", "viewkey", "treasury"] });
      const kind = V.hexToStr(r.vals.kind || "");
      if (kind !== "vault") { console.warn("configured vault is not a vault contract"); return null; }
      const w = { scid, asset: rec, viewkey: U.clean(V.hexToStr(r.vals.viewkey || ""), 200) };
      for (const k of NUM) w[k] = num(r.vals, k);
      if (w.v !== 3n) { console.warn("vault version " + w.v + " is not the version this app reads"); return null; }
      w.headroom = w.cap > w.sup ? w.cap - w.sup : Z;
      // the size above which a burn earns the extra delay: bigbps of the current supply, contract arithmetic
      w.bigAt = w.bigbps === Z ? Z : (w.sup / 10000n) * w.bigbps + ((w.sup % 10000n) * w.bigbps) / 10000n;
      this.vault = w;
      return w;
    },

    // The connected wallet's own deposit index and withdrawals. Read by exact key: the app never enumerates
    // other users, and an index that is registered but not yet attested has no foreign address to show.
    async myVault(addr) {
      const w = this.vault;
      if (!w || !addr) return null;
      const c = await this.fetchKeys(w.scid, ["uc_" + addr]);
      const n = isNum(c["uc_" + addr]) ? Number(c["uc_" + addr]) : 0;
      let dep = null;
      if (n > 0) {
        const idk = "ul_" + addr + "_" + (n - 1);
        const got = await this.fetchKeys(w.scid, [idk]);
        if (isNum(got[idk])) {
          const j = got[idk];
          const d = await this.fetchKeys(w.scid, ["da_" + j, "dh_" + j, "dver_" + j]);
          dep = {
            j,
            addr: U.clean(V.hexToStr(d["da_" + j] || ""), 200),
            since: isNum(d["dh_" + j]) ? B(d["dh_" + j]) : Z,
            ver: isNum(d["dver_" + j]) ? B(d["dver_" + j]) : Z,
          };
        }
      }
      // what this wallet actually holds of the wrapped asset, so the walkthrough can mark the last step done
      // rather than leaving the user to check another screen
      let units = 0n;
      try { const bal = await V.wallet.getBalance(w.scid, true); units = V.toBig(bal && bal.balance) || 0n; } catch (e) {}
      return Object.assign({ units }, { indexes: n, dep, withdrawals: await this.vaultWithdrawals(addr) });
    },

    // The last SCAN withdrawals, filtered to this address. w_<j>_by is the burner as ADDRESS_STRING, which is
    // the same canonical dero1 form the app uses everywhere.
    async vaultWithdrawals(addr) {
      const w = this.vault;
      if (!w || !addr) return [];
      const SCAN = 60;
      const wn = Number(w.wn), from = Math.max(0, wn - SCAN);
      if (wn === 0) return [];
      const keys = [];
      for (let j = from; j < wn; j++) keys.push("w_" + j + "_by");
      const by = await this.fetchKeys(w.scid, keys);
      const mine = [];
      for (let j = from; j < wn; j++) if (V.hexToStr(by["w_" + j + "_by"] || "") === addr) mine.push(j);
      if (!mine.length) return [];
      const want = [];
      for (const j of mine) want.push("w_" + j + "_v", "w_" + j + "_f", "w_" + j + "_to", "w_" + j + "_h", "w_" + j + "_rel", "w_" + j + "_st", "w_" + j + "_ftx", "w_" + j + "_p");
      const got = await this.fetchKeys(w.scid, want);
      const g = (j, s) => got["w_" + j + "_" + s];
      return mine.reverse().map((j) => ({
        j,
        units: isNum(g(j, "v")) ? B(g(j, "v")) : Z,
        fee: isNum(g(j, "f")) ? B(g(j, "f")) : Z,
        dest: U.clean(V.hexToStr(g(j, "to") || ""), 200),
        burned: isNum(g(j, "h")) ? Number(g(j, "h")) : 0,
        rel: isNum(g(j, "rel")) ? Number(g(j, "rel")) : 0,
        st: isNum(g(j, "st")) ? Number(g(j, "st")) : 0,
        ftx: U.clean(V.hexToStr(g(j, "ftx") || ""), 128),
        // a signer has marked this withdrawal as being paid: only k votes can settle it after that, because the
        // coins may already have left. Unmarked, the burner may Reclaim it once the window has passed.
        marked: isNum(g(j, "p")) ? Number(g(j, "p")) : 0,
      }));
    },
  });

  // ---- formatting ----

  const dec = () => { const d = (D.vault && D.vault.asset && D.vault.asset.dec) || 0; return Number.isInteger(d) && d >= 0 && d <= 18 ? d : 0; };
  const sym = () => U.clean((D.vault && D.vault.asset && D.vault.asset.sym) || "units", 12);
  // base units are exact; the pretty form is a courtesy. When no decimals were recorded, show the exact integer.
  const amt = (u, places) => (dec() === 0 ? String(u) + " base units" : A.fmtUnits(u, dec(), places === undefined ? 6 : places) + " " + sym());
  // A protocol constant (minimum deposit, minimum burn, the fee) is a rule, and a rule is printed to the last
  // digit: the "<0.00000001" fmtUnits gives a swallowed balance is honest for a balance and wrong for a threshold
  // someone has to meet exactly — on a 12-decimal vault the shipped minimum burn sits at the ninth place.
  const exact = (u) => amt(u, dec());
  // DERO blocks are about 18 seconds; say "about" and never round a wait down
  const blocksAway = (n) => {
    if (n <= 0) return "now";
    const s = n * 18;
    if (s < 90) return "about " + n + " block" + (n === 1 ? "" : "s") + " (~" + s + "s)";
    if (s < 5400) return "about " + Math.ceil(s / 60) + " min";
    if (s < 172800) return "about " + Math.ceil(s / 3600) + " h";
    return "about " + Math.ceil(s / 86400) + " days";
  };

  // ---- network grammar ----
  // A network is a two-letter mono label with a hairline leading edge in its tint (.net / .net.xmr / .net.zec),
  // so an amount, an address and a settlement each say plainly which chain they belong to. The tint is never a
  // fill, and the chain is read from the deployment record rather than guessed.
  const netOf = (a) => {
    const s = (String(a.sym || "") + " " + String(a.chain || "")).toUpperCase();
    if (/XMR|MONERO/.test(s)) return ["XM", "net xmr"];
    if (/ZEC|ZCASH/.test(s)) return ["ZC", "net zec"];
    return [(String(a.sym || "").toUpperCase().replace(/[^A-Z0-9]/g, "") + "??").slice(0, 2), "net"];
  };
  const mark = (code, cls, txt) => h("span", { class: "row tight" }, h("span", { class: cls }, code), h("span", { class: "label" }, txt));
  const arrow = () => h("span", { class: "dim2" }, "→");
  // state is stated, not glowed: a 6px square and precise words
  const flag = (tint, word) => h("span", { class: "row tight" }, h("i", { class: "dot", style: "background:var(--" + tint + ")" }), h("span", { class: "label" }, word));
  // a key/value row whose value is markup rather than a string
  const kvn = (k, ...v) => h("div", { class: "kv" }, h("span", { class: "dim" }, k),
    h("span", { class: "row tight", style: "flex-wrap:wrap;justify-content:flex-end" }, ...v));
  // The settlement panel, and the only shape a settlement is ever described in on this screen: which way it
  // moves, by what mechanism, who is holding the coins meanwhile, and what state it is in. Custody is a line of
  // its own because a federation holding the reserve is the fact a badge would hide.
  const panel = (c, from, to, custody, status) => [
    kvn("Settlement", from, arrow(), to),
    kv("Method", c.fed),
    kv("Custody", custody),
    kv("Status", status),
  ];
  // A string the user has to read in full — a deposit address, a destination, a settlement txid, the vault id —
  // is never truncated and never boxed: a label above, the mono value below, all of it selectable, and a click
  // copies it. Truncating any of these would hide the part that differs from a look-alike.
  // Wrapping is a sequence with waits in it, so it is shown as one: what is finished, what is happening now,
  // what is waiting on the federation, and what has not started. A user should never have to infer which.
  //   done — this has happened and cannot un-happen
  //   now  — your move; the action lives on this row
  //   wait — the chain or the federation is working; nothing for you to do
  //   todo — not reachable yet
  const steps = (items) => h("ol", { class: "steps" }, items.filter(Boolean).map((it, i) =>
    h("li", { class: "step " + it.state },
      h("span", { class: "n mono" }, it.state === "done" ? "✓" : String(i + 1)),
      h("div", { class: "grow" },
        h("div", { class: "between" },
          h("b", { class: "t" }, it.title),
          h("span", { class: "label sst" }, it.state === "done" ? "Done" : it.state === "now" ? "Your move" : it.state === "wait" ? "Waiting" : "")),
        it.detail ? h("div", { class: "d note" }, it.detail) : null,
        it.body || null))));

  const full = (label, value, copied) => h("div", { class: "stack", style: "gap:3px;margin-top:12px" },
    h("div", { class: "eyebrow" }, label),
    h("button", {
      class: "mono sub", type: "button", "aria-label": "Copy " + label,
      style: "display:block;width:100%;text-align:left;word-break:break-all;line-height:1.5",
      onclick: () => { navigator.clipboard && navigator.clipboard.writeText(value).then(() => toast(copied, "ok")); },
    }, value));

  // ---- screen ----

  // the tip drives the "payable in N blocks" countdown; live.js already polls it, so read that rather than
  // asking the node again on every draw
  let tip = 0;
  const refreshTip = async () => { if (V.live && V.live.height) { tip = V.live.height; return; } try { tip = await V.node.height(); } catch (e) {} };

  // ---- no vault: the mainnet shape ----
  // A build that names no vault offers no wrapped asset. The screen says so first, and why, then how XMR and ZEC
  // settle instead. It draws no deposit address, no burn form and no empty walkthrough, and nothing to sign.
  const noVault = () => [
    h("div", { class: "warnbox" }, h("b", null, V.notice.NOWRAP), h("div", { class: "sub mt8" }, V.notice.NOWRAP_WHY)),
    h("h3", { class: "mt" }, "How XMR and ZEC settle here instead"),
    kv("Monero", "atomic swap for a DERO-side asset"), kv("Zcash", "transparent ZEC only, by atomic swap"), kv("Custodian", "none: each chain's own lock"),
    h("div", { class: "note mt8" }, "This page runs no swap. Each side runs a swap program on its own machine, against its own Monero or Zcash node and its own DERO wallet, " +
      "and trusts those answers and its own presence at its window: miss the window and the other side's exit opens. Neither program has yet completed a swap on a real " +
      "Monero or Zcash network, and the Zcash one cannot yet broadcast a transaction. Shielded ZEC cannot be swapped here, and a transparent Zcash swap is public: the amount, both addresses and the hash."),
    h("div", { class: "note mt8" }, "Every market this app creates on this network is priced in DERO. Nothing on this screen asks you to sign."),
  ];

  // #wrap/<scid> opens that vault; #wrap opens the first one recorded for this build
  router.on("wrap", async (el, param) => {
    const title = () => h("h1", { class: "mt mb" }, "Wrapped assets");
    if (!D.allVaults().length) return render(el, title(), noVault());
    // only a vault this build ships opens by id: any other contract would borrow a shipped vault's words (MISS-01)
    const picked = D.allVaults().some((v) => v.scid === param) ? param : null;
    if (param && !picked) return render(el, title(), h("div", { class: "errbox" }, "This build ships no vault at " + U.clean(param, 64) + ", so nothing about it is shown and nothing can be signed."));
    const draw = (w, my, err) => {
      if (err) return render(el, title(), h("div", { class: "errbox" }, err));
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

        // ---- Level 1: what the vault holds, which way it settles, and whether it is open ----
        h("div", { class: "hero cut mt8" },
          h("div", { class: "between" },
            h("span", { class: "eyebrow" }, "Wrapped-asset vault"),
            flag(halted ? "down" : canMint ? "acc" : "warn", halted ? "Halted" : canMint ? "Open" : "Deposits paused")),
          h("div", { class: "row tight mt8", style: "flex-wrap:wrap;gap:6px 14px" }, c.foreign(), arrow(), c.dero()),
          h("div", { class: "pulse-row mt" },
            h("div", null, h("div", { class: "eyebrow" }, "Claims outstanding"), h("div", { class: "big num mt8" }, amt(w.sup, 4))),
            h("div", { class: "row", style: "gap:22px;flex-wrap:wrap" },
              h("div", { class: "mini" }, h("span", { class: "l" }, "Mint cap"), h("span", { class: "v" }, amt(w.cap, 4))),
              h("div", { class: "mini" }, h("span", { class: "l" }, "Headroom"), h("span", { class: "v" }, amt(w.headroom, 4))),
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
        h("div", { class: "warnbox mt8" },
          h("b", null, "Minimum deposit: " + exact(w.mindep)),
          h("div", { class: "sub" }, "Anything smaller cannot be minted, and there is nobody who can return it. Check the amount before you send."),
          h("div", { class: "mt8" }, h("b", null, canMint ? "Most that can be minted right now: " + exact(w.headroom) : "No headroom: nothing can be minted right now")),
          h("div", { class: "sub" }, "The vault will not mint past its cap of " + amt(w.cap, 4) + ". A deposit above the headroom is not refused — it waits in the federation's wallet until enough is burned to make room, and no contract can return it in the meantime." +
            (w.winmax > Z ? " This vault also mints at most " + amt(w.winmax, 4) + " per window, which can bind before the cap does." : ""))),

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
          trustLine(true, "The DERO side is verifiable by anyone: " + amt(w.sup, 4) + " of claims exist and the contract will not mint past " + amt(w.cap, 4) + "."),
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
              ? "Signers have bonded " + A.fmtDero(w.bondtot, 2) + " DERO against a mint cap of " + amt(w.cap, 4) + ". A quorum of the others can slash a signer's bond to the treasury — not to you."
              : "Signers have bonded " + A.fmtDero(w.bondtot, 2) + " DERO against a mint cap of " + amt(w.cap, 4) + ", but with " + w.na + " signers and a quorum of " + w.k + " the bond deters nobody: a signer cannot vote to slash itself, so the " + w.k + " who could take the reserve are the " + w.k + " needed to slash anyone. A slashed bond goes to the treasury, never to you."),
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
    const doRegister = async (w) => {
      const ok = await V.app.tx({
        scid: w.scid, entry: "Register", args: [], dero: w.regfee,
        // un counts everyone's indexes; uc_<me> moves only when this wallet's own Register executes
        describe: "Register a deposit index", probe: { scid: w.scid, keys: ["uc_" + me()] },
      });
      if (ok) { toast("Index registered. Signers must attest it before it can receive.", "ok"); reload(true); }
    };

    // The contract pays only w_<j>_by, so this is safe to send from any wallet; sending it from the burner's is
    // simply the natural place. Nothing is attached and SIGNER() is not read, so ring 16 is fine.
    const doReclaim = async (w, x) => {
      const ok = await V.app.tx({
        scid: w.scid, entry: "Reclaim", args: [["j", "U", BigInt(x.j)]], ring: 16,
        describe: "Reclaim withdrawal #" + x.j + " — " + amt(x.units + x.fee, 8) + " back to you",
        // st becomes 2 only by a return; the key alone would also move if the signers settled it meanwhile
        probe: { scid: w.scid, keys: ["w_" + x.j + "_st"], check: (a, b) => b.vals["w_" + x.j + "_st"] === "2" },
      });
      if (ok) { toast("Reclaimed. The units and the fee are back in your wallet.", "ok"); reload(true); }
    };
    const doBurn = async (w, amountEl, destEl) => {
      const units = A.parseUnits(amountEl.value, dec());
      const dst = String(destEl.value || "").trim();
      if (units === null || units <= Z) return toast("Enter an amount", "err");
      if (units < w.minburn) return toast("Below the minimum burn of " + exact(w.minburn), "err");
      // the contract enforces these; refusing here saves the fee of a transaction that would revert
      if (dst.length < 16 || dst.length > 200) return toast("That destination is not a plausible address", "err");
      if (/[\s\u0000-\u001f\u007f]/.test(dst)) return toast("The destination contains whitespace or control characters", "err");
      const ok = await V.app.tx({
        scid: w.scid, entry: "Burn", args: [["dest", "S", dst], ["refund", "S", me()]], asset: { scid: w.scid, amount: units },
        describe: "Burn " + amt(units, 8) + " and withdraw",
        // wn counts everyone's burns: this one executed only if a withdrawal opened since then names this wallet
        probe: { scid: w.scid, keys: ["wn"], check: async (a, b) => { const ks = []; for (let j = +a.vals.wn || 0; j < (+b.vals.wn || 0) && ks.length < 16; j++) ks.push("w_" + j + "_by"); const g = ks.length ? await D.fetchKeys(w.scid, ks) : {}; return ks.some((k) => V.hexToStr(g[k] || "") === me()); } },
      });
      if (ok) { amountEl.value = ""; toast("Burned. It becomes payable at the height shown below.", "ok"); reload(true); }
    };

    // ---- load ----
    let lastMine = null;
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
        draw(D.vault, null, "The vault could not be read from your node: " + U.clean(String(e && e.message || e), 160));
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
