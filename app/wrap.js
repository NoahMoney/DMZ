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
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, B = BigInt;
  const { h, render, button, kv, icon, pill, router, toast } = U;
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
    async loadVault() {
      const scid = this.cfg.vault;
      if (!scid || !/^[0-9a-f]{64}$/.test(scid)) return null;
      const r = await V.node.getSC(scid, { keys: [...NUM, "kind", "viewkey", "treasury"] });
      const kind = V.hexToStr(r.vals.kind || "");
      if (kind !== "vault") { console.warn("configured vault is not a vault contract"); return null; }
      const w = { scid, asset: this.cfg.vaultAsset || {}, viewkey: U.clean(V.hexToStr(r.vals.viewkey || ""), 200) };
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
      return { indexes: n, dep, withdrawals: await this.vaultWithdrawals(addr) };
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
      for (const j of mine) want.push("w_" + j + "_v", "w_" + j + "_f", "w_" + j + "_to", "w_" + j + "_h", "w_" + j + "_rel", "w_" + j + "_st", "w_" + j + "_ftx");
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
      }));
    },
  });

  // ---- formatting ----

  const dec = () => { const d = (D.vault && D.vault.asset && D.vault.asset.dec) || 0; return Number.isInteger(d) && d >= 0 && d <= 18 ? d : 0; };
  const sym = () => U.clean((D.vault && D.vault.asset && D.vault.asset.sym) || "units", 12);
  // base units are exact; the pretty form is a courtesy. When no decimals were recorded, show the exact integer.
  const amt = (u, places) => (dec() === 0 ? String(u) + " base units" : A.fmtUnits(u, dec(), places === undefined ? 6 : places) + " " + sym());
  // DERO blocks are about 18 seconds; say "about" and never round a wait down
  const blocksAway = (n) => {
    if (n <= 0) return "now";
    const s = n * 18;
    if (s < 90) return "about " + n + " block" + (n === 1 ? "" : "s") + " (~" + s + "s)";
    if (s < 5400) return "about " + Math.ceil(s / 60) + " min";
    if (s < 172800) return "about " + Math.ceil(s / 3600) + " h";
    return "about " + Math.ceil(s / 86400) + " days";
  };

  // ---- screen ----

  // the tip drives the "payable in N blocks" countdown; live.js already polls it, so read that rather than
  // asking the node again on every draw
  let tip = 0;
  const refreshTip = async () => { if (V.live && V.live.height) { tip = V.live.height; return; } try { tip = await V.node.height(); } catch (e) {} };

  router.on("wrap", async (el) => {
    const draw = (w, my, err) => {
      if (err) return render(el, h("h1", { class: "mt mb" }, "Wrapped assets"), h("div", { class: "errbox" }, err));
      if (!w) return render(el, h("h1", { class: "mt mb" }, "Wrapped assets"),
        U.emptyState("wallet", "No vault on this network",
          "This build of DMZ was published without a wrapped-asset vault, or the configured contract is not a v3 vault. Nothing to show, and nothing to sign."));

      const a = w.asset || {}, name = U.clean(a.name || "the foreign asset", 40), chain = U.clean(a.chain || "its own chain", 40);
      const halted = w.halted !== Z;
      const mine = my && my.withdrawals ? my.withdrawals : [];

      render(el,
        h("div", { class: "between mt" },
          h("h1", null, "Wrap " + (a.sym ? U.clean(a.sym, 12) : "assets")),
          halted ? pill("HALTED", "down") : pill("OPEN", "live")),

        // ---- what this is, in the words that are actually true ----
        h("div", { class: "card mt stack" },
          h("div", { class: "sub" },
            "Send " + name + " on " + chain + " to your own deposit address. When " + w.k + " of the " + w.na +
            " active signers have both seen it, the vault mints you the same number of base units as a DERO asset. " +
            "Burn those units with a destination and the same quorum pays you back on " + chain + "."),
          h("div", { class: "stats4" },
            h("div", { class: "stat" }, h("div", { class: "l" }, "Claims outstanding"), h("div", { class: "v num" }, amt(w.sup, 4))),
            h("div", { class: "stat" }, h("div", { class: "l" }, "Mint cap"), h("div", { class: "v num" }, amt(w.cap, 4))),
            h("div", { class: "stat" }, h("div", { class: "l" }, "Headroom"), h("div", { class: "v num" }, amt(w.headroom, 4))),
            h("div", { class: "stat" }, h("div", { class: "l" }, "Signers"), h("div", { class: "v" }, w.k + " of " + w.na)))),

        halted ? h("div", { class: "warnbox mt" },
          h("b", null, "This vault is halted."),
          h("div", { class: "sub" }, "Minting, registering and burning are refused while the signers report a problem. " +
            "A halt is a condition, not a decision: each signer votes it on and off from public state, so it clears by itself " +
            "when the condition does. Withdrawals already burned are unaffected and stay payable.")) : null,

        // ---- deposit ----
        h("h3", { class: "mt" }, "Deposit"),
        // The minimum is a property of the vault, not of the visitor, so it is published before the wallet is
        // connected and before an address exists. A deposit below it can never be minted and no operator exists
        // to send it back: someone deciding whether to use this vault at all has to be able to see that first.
        h("div", { class: "warnbox mt" },
          h("b", null, "Minimum deposit: " + amt(w.mindep, 8)),
          h("div", { class: "sub" }, "Anything smaller cannot be minted, and there is nobody who can return it. Check the amount before you send.")),
        !V.wallet.connected
          ? h("div", { class: "card stack", style: "align-items:center;text-align:center" },
              icon("wallet", "svgi dim2"), h("div", { class: "sub" }, "Connect your wallet to get a deposit address."),
              button("Connect wallet", "primary", () => V.app.connectWallet()))
          : h("div", { class: "card stack" },
              my && my.dep && my.dep.addr
                ? h("div", { class: "stack" },
                    h("div", { class: "eyebrow" }, "Your deposit address on " + chain),
                    h("button", {
                      class: "card tight mono", type: "button", style: "word-break:break-all;line-height:1.5",
                      onclick: () => { navigator.clipboard && navigator.clipboard.writeText(my.dep.addr).then(() => toast("Deposit address copied", "ok")); },
                    }, my.dep.addr),
                    kv("Live since block", String(my.dep.since)),
                    a.conf ? kv("Confirmations waited for", String(a.conf)) : null,
                    h("div", { class: "note" },
                      "This address is yours alone and was published by " + w.k + " signers. Deposits made to it before block " +
                      my.dep.since + " are ignored by every honest signer, so do not reuse an address you saw earlier. " +
                      "Register again for a fresh, unlinked index whenever you want one."),
                    button("Register another index (" + A.fmtDero(w.regfee, 5) + " DERO)", "small", () => doRegister(w)))
                : my && my.indexes > 0
                  ? h("div", { class: "stack" },
                      h("b", null, "Waiting for attestation"),
                      h("div", { class: "sub" }, "You hold index " + (my.dep ? my.dep.j : "?") + ". " + w.k + " signers must publish its foreign address before it exists. Until then there is nothing to send to."))
                  : h("div", { class: "stack" },
                      h("div", { class: "sub" }, "Registering allocates a fresh index, so two deposits of yours are not linked on chain. It costs " + A.fmtDero(w.regfee, 5) + " DERO, which goes to the treasury."),
                      h("div", { class: "note" }, "Register is a ring-size-2 call because the contract must know who you are. Your address is public in that transaction."),
                      button("Register a deposit index", "primary", () => doRegister(w), halted))),

        // ---- withdraw ----
        h("h3", { class: "mt" }, "Withdraw"),
        h("div", { class: "card stack" }, burnForm(w, halted)),

        // ---- your withdrawals ----
        mine.length ? h("h3", { class: "mt" }, "Your withdrawals") : null,
        mine.length ? h("div", { class: "list mt" }, mine.map((x) => wrow(w, x))) : null,

        // ---- the factual trust panel ----
        h("h3", { class: "mt" }, "What you are trusting"),
        h("div", { class: "card stack" },
          trustLine(false, w.k + " of the " + w.na + " signers can move the whole foreign reserve. They hold the keys to it; this contract cannot."),
          trustLine(true, "The DERO side is verifiable by anyone: " + amt(w.sup, 4) + " of claims exist and the contract will not mint past " + amt(w.cap, 4) + "."),
          trustLine(w.viewkey !== "", w.viewkey
            ? "A viewing key is published, so anyone can check the reserve on " + chain + " really covers the claims."
            : "No viewing key is published yet, so the reserve on " + chain + " cannot be checked by an outsider. Until it is, the backing is a claim, not a proof."),
          trustLine(w.bondtot > Z, w.bondtot > Z
            ? "Signers have bonded " + A.fmtDero(w.bondtot, 2) + " DERO, slashable by a quorum of the others."
            : "No signer bond is at stake."),
          trustLine(w.sealed !== Z, w.sealed !== Z
            ? "The vault is sealed: the deployer has no power over it and nobody can add a signer without a quorum."
            : "The vault is NOT sealed. The deployer can still change the signer set. Do not deposit."),
          h("div", { class: "note" },
            "There is no operator and no override. Every limit either empties by itself or expires into a payment: " +
            "doing nothing releases a withdrawal, it never cancels one. Stopping one takes " + w.k + " signers voting to veto or halt."),
          h("div", { class: "row" },
            h("button", { class: "sub mono ellipsis", type: "button", style: "display:block;max-width:100%", onclick: () => { navigator.clipboard && navigator.clipboard.writeText(w.scid).then(() => toast("Vault contract id copied", "ok")); } }, "vault " + w.scid.slice(0, 16) + "…"))));
    };

    const trustLine = (ok, txt) => h("div", { class: "row tight", style: "align-items:flex-start" },
      icon(ok ? "check" : "x", "svgi " + (ok ? "up" : "down")), h("span", { class: "sub" }, txt));

    // ---- burn form ----
    const burnForm = (w, halted) => {
      const amount = h("input", { inputmode: "decimal", placeholder: "0.0", autocomplete: "off", spellcheck: "false" });
      const dest = h("input", { placeholder: "Your address on " + U.clean((w.asset && w.asset.chain) || "the foreign chain", 40), autocomplete: "off", spellcheck: "false" });
      const out = h("div", { class: "note" });
      const restate = () => {
        const units = A.parseUnits(amount.value, dec());
        if (units === null || units <= Z) { out.textContent = ""; return; }
        if (units < w.minburn) { out.textContent = "Below the minimum burn of " + amt(w.minburn, 8) + "."; return; }
        const net = units > w.wfee ? units - w.wfee : Z;
        const big = w.bigbps !== Z && units > w.bigAt;
        const waitBlocks = Number(w.wdelay) + (big ? Number(w.bigdelay) : 0);
        out.textContent = "You receive " + amt(net, 8) + " after a " + amt(w.wfee, 8) + " fee" +
          (big ? ". This is larger than " + amt(w.bigAt, 4) + " (" + (Number(w.bigbps) / 100) + "% of claims), so it waits " + waitBlocks + " blocks — " + blocksAway(waitBlocks) + " — instead of " + w.wdelay + "."
               : ". It becomes payable " + w.wdelay + " blocks after the burn, " + blocksAway(Number(w.wdelay)) + ".");
      };
      amount.oninput = restate;
      return [
        h("label", { class: "field" }, h("span", null, "Amount to unwrap"), amount),
        h("label", { class: "field" }, h("span", null, "Destination on " + U.clean((w.asset && w.asset.chain) || "the foreign chain", 40)), dest),
        out,
        h("div", { class: "note" },
          "Burn is a ring-size-2 call: the contract must record who burned, so this address is public in that transaction. " +
          "The destination you type is stored on DERO in the clear — anyone can read it. Use an address you are willing to publish."),
        button("Burn and withdraw", "primary", () => doBurn(w, amount, dest), halted),
      ];
    };

    // ---- one withdrawal row ----
    const wrow = (w, x) => {
      const away = x.rel - tip;
      const state = x.st === 1 ? pill("PAID", "up") : x.st === 2 ? pill("RETURNED", "") : away > 0 ? pill("WAITING", "") : pill("PAYABLE", "live");
      return h("div", { class: "card tight stack" },
        h("div", { class: "between" }, h("b", null, "#" + x.j + " · " + amt(x.units, 8)), state),
        h("div", { class: "sub mono", style: "word-break:break-all" }, x.dest),
        x.st === 0
          ? h("div", { class: "sub" }, away > 0
              ? "Payable from block " + x.rel + " — " + blocksAway(away) + " away (burned at " + x.burned + ")."
              : "Payable now, since block " + x.rel + ". The signers pay it without anyone asking; nothing here needs a person.")
          : x.st === 1
            ? h("div", { class: "sub mono", style: "word-break:break-all" }, "paid in " + x.ftx)
            : h("div", { class: "sub" }, "The signers could not pay this destination, so the units were returned to you."));
    };

    // ---- actions ----
    const doRegister = async (w) => {
      const ok = await V.app.tx({
        scid: w.scid, entry: "Register", args: [], dero: w.regfee,
        describe: "Register a deposit index", probe: { scid: w.scid, keys: ["un"] },
      });
      if (ok) { toast("Index registered. Signers must attest it before it can receive.", "ok"); reload(true); }
    };

    const doBurn = async (w, amountEl, destEl) => {
      const units = A.parseUnits(amountEl.value, dec());
      const dst = String(destEl.value || "").trim();
      if (units === null || units <= Z) return toast("Enter an amount", "err");
      if (units < w.minburn) return toast("Below the minimum burn of " + amt(w.minburn, 8), "err");
      // the contract enforces these; refusing here saves the fee of a transaction that would revert
      if (dst.length < 16 || dst.length > 200) return toast("That destination is not a plausible address", "err");
      if (/[\s\u0000-\u001f\u007f]/.test(dst)) return toast("The destination contains whitespace or control characters", "err");
      const ok = await V.app.tx({
        scid: w.scid, entry: "Burn", args: [["dest", "S", dst], ["refund", "S", me()]], asset: { scid: w.scid, amount: units },
        describe: "Burn " + amt(units, 8) + " and withdraw", probe: { scid: w.scid, keys: ["wn"] },
      });
      if (ok) { amountEl.value = ""; toast("Burned. It becomes payable at the height shown below.", "ok"); reload(true); }
    };

    // ---- load ----
    let lastMine = null;
    const reload = async (force) => {
      try {
        await refreshTip();
        const w = force || !D.vault ? await D.loadVault() : D.vault;
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
    D.onChange(() => { if (router.current && router.current.name === "wrap" && D.vault) { tip = (V.live && V.live.height) || tip; draw(D.vault, lastMine); } });
  });
})();
