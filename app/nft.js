// DMZ nft.js — holder-NFT collection page (lock → wait → mint → unlock).
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, { h, render, avatar, kv, button, pill, icon } = U;
  const me = () => V.wallet.canon;
  V.nft = {
    page: async (el, scid) => {
      const n = D.nfts.get(scid); if (!n) return render(el, h("div", { class: "errbox mt" }, "Unknown collection"));
      const tok = D.assets.get(n.token);
      // the caller's own lock is read by key; other holders' locks are never enumerated
      const k = me() ? await D.myKeys(scid, me(), ["lk_", "lh_", "lm_", "mt_"]).catch(() => ({})) : {};
      const mintedEver = k.mt === 1n; // one edition per address for the life of the contract (mt_ survives Unlock)
      const lock = k.lk ? { amount: k.lk, height: k.lh || 0n, minted: k.lm === 1n || mintedEver } : null;
      const height = V.live.height || (await V.node.height().catch(() => 0));
      const ready = lock && height >= Number(lock.height + n.holdblocks);
      const img = U.safeUrl(n.file);
      render(el,
        h("div", { class: "row mt" }, avatar({ icon: n.icon, ticker: "NFT", scid }, "big"), h("div", { class: "grow" }, h("h1", null, n.name), h("div", { class: "sub" }, n.collection), h("div", { class: "mt8" }, U.verified()))),
        img && V.settings.images ? h("img", { src: img, alt: "", referrerpolicy: "no-referrer", loading: "lazy", style: "width:100%;border-radius:20px;margin-top:14px" }) : null,
        h("div", { class: "card mt" }, n.descr ? h("div", { class: "sub mb" }, n.descr) : null,
          kv("Minted", n.minted + " / " + n.maxsupply), kv("Requires holding", A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker), kv("For", n.holdblocks + " blocks (~" + Math.round(Number(n.holdblocks) * 18 / 60) + " min)"), kv("Mint price", A.fmtDero(n.price, 2) + " DERO"), kv("Creator", h("span", { class: "mono" }, V.shortAddr(n.creator)))),
        h("div", { class: "note mt" }, "Lock the token in the contract to prove you hold it; after the holding period mint one edition (one per lock). Unlock returns your tokens at any time."),
        V.wallet.connected ? h("div", { class: "card mt" }, h("div", { class: "eyebrow" }, "Your lock"),
          lock ? [h("div", { class: "row mt8 stats3" }, h("div", { class: "stat" }, h("div", { class: "l" }, "Locked"), h("div", { class: "v num" }, A.fmtCompact(lock.amount, n.tokenDec))), h("div", { class: "stat" }, h("div", { class: "l" }, "Since block"), h("div", { class: "v num" }, String(lock.height))), h("div", { class: "stat" }, h("div", { class: "l" }, lock.minted ? "Edition" : ready ? "Status" : "Ready in"), h("div", { class: "v num " + (ready || lock.minted ? "up" : "") }, lock.minted ? "minted" : ready ? "ready" : Math.max(0, Number(lock.height + n.holdblocks) - height) + " blk"))),
            !lock.minted && !ready ? h("div", { class: "progress mt8" }, h("i", { style: "width:" + Math.min(100, Math.round(((height - Number(lock.height)) / Math.max(1, Number(n.holdblocks))) * 100)) + "%" })) : null]
          : h("div", { class: "sub mt8" }, "No lock from this wallet yet. Locking is one transaction and reversible at any time.")) : null,
        h("div", { class: "card mt" }, h("div", { class: "row tight" }, icon("shield", "svgi up"), h("b", null, "Public accounting")),
          kv("Editions minted", n.minted + " of " + n.maxsupply), kv("Creator revenue waiting", A.fmtDero(n.cacc || 0n, 2) + " DERO"), kv("Protocol fee on mints", Number(n.fee || 0n) / 100 + "% (" + A.fmtDero(n.fd || 0n, 2) + " DERO unclaimed)"), kv("Creator", h("button", { class: "mono linkbtn", type: "button", onclick: () => U.router.go("profile", n.creator) }, V.shortAddr(n.creator))), kv("Contract", h("span", { class: "mono" }, V.shortAddr(scid)))),
        h("div", { class: "stack mt" },
          !lock && mintedEver ? h("div", { class: "note" }, "This address already minted its edition: one per address, locking again earns nothing more.") : null,
          !lock && !mintedEver ? button("Lock " + A.fmtCompact(n.minhold, n.tokenDec) + " " + n.tokenTicker, "primary wide", () => V.app.tx({ scid, entry: "Lock", args: [], asset: { scid: n.token, amount: n.minhold }, describe: "Lock tokens" })) : null,
          lock && !lock.minted ? button(ready ? "Mint edition (" + A.fmtDero(n.price, 2) + " DERO)" : "Minting in " + Math.max(0, Number(lock.height + n.holdblocks) - height) + " blocks", "primary wide", () => V.app.tx({ scid, entry: "Mint", args: [], dero: n.price, fee: 2000, describe: "Mint NFT" }), { disabled: !ready }) : null,
          lock ? button("Unlock tokens", "wide", () => V.app.tx({ scid, entry: "Unlock", args: [], describe: "Unlock" })) : null,
          me() === n.creator ? button("Claim creator revenue", "wide", () => V.app.tx({ scid, entry: "ClaimCreator", args: [], describe: "Claim" })) : null,
          tok ? button("Open " + tok.ticker, "wide", () => U.router.go("token", tok.scid)) : null));
    },
  };
})();
