// DMZ nft.js — holder-NFT collection page (lock → wait → mint → unlock).
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, { h, render, avatar, kv, button, toast } = U;
  const me = () => V.wallet.canon;
  const stat = (l, v, cls) => h("div", { class: "stat" }, h("div", { class: "l" }, l), h("div", { class: "v num " + (cls || "") }, v));
  V.nft = {
    page: async (el, scid) => {
      const n = D.nfts.get(scid);
      // nft_<i> and nfa_<i> share one counter on the factory, so a link that guessed #nft for an NFA is a
      // plausible guess: send it to the NFA page rather than to "Unknown collection".
      if (!n && D.nfas.has(scid)) return V.nfas.page(el, scid);
      if (!n) return render(el, h("div", { class: "errbox mt" }, "Unknown collection"));
      const tok = D.assets.get(n.token);
      // the caller's own lock is read by key; other holders' locks are never enumerated
      const k = me() ? await D.myKeys(scid, me(), ["lk_", "lh_", "lm_", "mt_"]).catch(() => ({})) : {};
      const mintedEver = k.mt === 1n; // one edition per address for the life of the contract (mt_ survives Unlock)
      const lock = k.lk ? { amount: k.lk, height: k.lh || 0n, minted: k.lm === 1n || mintedEver } : null;
      const height = V.live.height || (await V.node.height().catch(() => 0));
      const ready = lock && height >= Number(lock.height + n.holdblocks);
      const soldOut = n.minted >= n.maxsupply; // nft.bas Mint 27: the contract refuses the mint after this
      const left = lock ? Math.max(0, Number(lock.height + n.holdblocks) - height) : 0;
      const img = U.safeUrl(n.file);
      render(el,
        // Level 1 — the edition count is what this contract is about
        h("div", { class: "hero mt" },
          h("div", { class: "between" }, h("span", { class: "eyebrow" }, "Holder edition"), U.verified()),
          h("div", { class: "row mt8" }, avatar({ icon: n.icon, ticker: "NFT", scid }, "big"),
            h("div", { class: "grow" }, h("h1", null, n.name), h("div", { class: "sub ellipsis" }, n.collection))),
          h("div", { class: "label mt" }, "Editions minted"),
          h("div", { class: "big num mt8" }, n.minted + " / " + n.maxsupply)),
        img && V.settings.images ? h("img", { src: img, alt: "", referrerpolicy: "no-referrer", loading: "lazy", style: "width:100%;display:block;margin-top:14px;border:1px solid var(--edge)" }) : null,
        h("div", { class: "stats4 mt" },
          stat("Mint price", A.fmtDero(n.price, 2) + " DERO"),
          stat("Hold", A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker),
          stat("Holding period", n.holdblocks + " blocks"),
          stat("Editions left", String(n.maxsupply - n.minted))),
        n.descr ? h("div", { class: "note mt8" }, n.descr) : null,
        h("div", { class: "nft-grid mt" },
          h("div", null,
            V.wallet.connected ? h("div", { class: "card" }, h("div", { class: "label" }, "Your lock"),
              lock ? [h("div", { class: "row mt8", style: "gap:0" },
                  stat("Locked", A.fmtCompact(lock.amount, n.tokenDec) + " " + n.tokenTicker),
                  stat("Since block", String(lock.height)),
                  stat(lock.minted ? "Edition" : ready ? "Status" : "Ready in", lock.minted ? "minted" : ready ? "ready" : left + " blk", ready || lock.minted ? "up" : "")),
                !lock.minted && !ready ? h("div", { class: "progress mt8" }, h("i", { style: "width:" + Math.min(100, Math.round(((height - Number(lock.height)) / Math.max(1, Number(n.holdblocks))) * 100)) + "%" })) : null]
                : h("div", { class: "sub mt8" }, "No lock from this wallet yet. Locking is one transaction and reversible at any time.")) : null,
            h("div", { class: "stack mt" },
              soldOut ? h("div", { class: "note" }, "All " + n.maxsupply + " editions are minted. The contract refuses another, so locking now earns nothing; an existing lock can still be unlocked.") : null,
              !lock && !soldOut && mintedEver ? h("div", { class: "note" }, "This address already minted its edition: one per address, locking again earns nothing more.") : null,
              !lock && !soldOut && !mintedEver ? button("Lock " + A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker, "primary wide", () => V.app.tx({ scid, entry: "Lock", args: [], asset: { scid: n.token, amount: n.minhold }, describe: "Lock tokens" })) : null,
              lock && !lock.minted && !soldOut ? button(ready ? "Mint edition (" + A.fmtDero(n.price, 2) + " DERO)" : "Minting in " + left + " blocks", "primary wide", () => V.app.tx({ scid, entry: "Mint", args: [], dero: n.price, fee: 2000, describe: "Mint NFT" }), { disabled: !ready }) : null,
              lock ? button("Unlock tokens", "wide", () => V.app.tx({ scid, entry: "Unlock", args: [], describe: "Unlock" })) : null,
              me() === n.creator ? button("Claim creator revenue", "wide", () => V.app.tx({ scid, entry: "ClaimCreator", args: [], describe: "Claim" })) : null,
              tok ? button("Open " + tok.ticker, "wide", () => U.router.go("token", tok.scid)) : null),
            h("div", { class: "note mt" }, "Lock the token in the contract to prove you hold it; after " + n.holdblocks + " blocks (~" + Math.round(Number(n.holdblocks) * 18 / 60) + " min) mint one edition, one per lock. Unlock returns your tokens at any time.")),
          // the ledger: no shield and no badge, the numbers are the assurance
          h("div", null, h("div", { class: "card" }, h("div", { class: "between" }, h("b", null, "Public accounting"), h("span", { class: "label" }, "On chain")),
            h("div", { class: "mt8" },
              kv("Editions minted", n.minted + " of " + n.maxsupply),
              kv("Creator revenue waiting", A.fmtDero(n.cacc || 0n, 2) + " DERO"),
              kv("Protocol fee on mints", Number(n.fee || 0n) / 100 + "% (" + A.fmtDero(n.fd || 0n, 2) + " DERO unclaimed)"),
              kv("Creator", h("button", { class: "mono linkbtn", type: "button", onclick: () => U.router.go("profile", n.creator) }, V.shortAddr(n.creator))),
              kv("Contract", h("button", { class: "mono", type: "button", title: "Copy the contract id", onclick: () => { navigator.clipboard && navigator.clipboard.writeText(scid).then(() => toast("Contract id copied", "ok")); } }, V.shortAddr(scid))))))));
    },
  };
})();
