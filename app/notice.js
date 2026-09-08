// DMZ notice.js — the message from the dev. It is the plainest statement of what this thing is: unregulated,
// unsupported, and held by exactly the keys it names. Every other screen has to be careful not to contradict it,
// so it is worth reading before changing any copy anywhere else in the app.
//
// Every categorical sentence here names the mechanism that makes it true — a contract rule, a key, a person —
// or it was cut. The keys that survive deployment (the factory owner in contracts/factory.bas, the treasury every
// contract pays, the TELA INDEX that serves these files) and the federation behind the wrapped assets are listed
// rather than wished away: a plaque that says "nobody holds a switch" while three keys do is the one thing this
// page must never be. The pass share is read from founders.bas (`share`, bps, immutable), never typed here.
//
// This page is deliberately not a card, not a dialog and not dismissible. It is a plaque.
(function () {
  const V = window.Veil, U = V.ui, D = V.data;
  const { h, render, router } = U;

  const share = () => (D.founders && D.founders.share !== undefined ? Number(D.founders.share) / 100 + "%" : null);
  const wrapped = () => { const v = D.vaults(); return v.length ? v.map((x) => U.clean(x.sym || x.name || "wrapped", 12)).join(" and ") : "XMR and ZEC"; };
  // k and the active count come from the vault contract once the wrap screen has read it; before that the shape
  // is stated and the numbers are not invented
  const fed = () => (D.vault ? D.vault.k + "-of-" + D.vault.na : "k-of-n");

  // Kept close to the words that were written, corrected where the contracts do not deliver them. Each entry is
  // one paragraph; an array is a paragraph whose parts carry emphasis, because a few of these are load-bearing
  // and should not be skimmed.
  const body = () => [
    "Dear users,",
    "Please be respectful and kind to each other.",
    ["This platform is completely unregulated. ", ["b", "Its creators hold no key that can spend what you hold on DERO."],
      " The keys that do survive, and the federation that holds the coins behind " + wrapped() + ", are named below."],
    (share() ? share() + " of the DERO fees the treasury claims" : "A share of the DERO fees the treasury claims — the number is stored in the pass contract —") +
      " is deposited for the Founders Pass holders. The treasury's own bot makes that deposit; no contract forces it. Fees earned in a wrapped asset reach no holder today.",
    ["Code is law. ", ["b", "The contracts are final."], " None of them has an update path. The app you read them through can be republished under one key " +
      "until that key is thrown away; Settings shows the commit you are running. There is no server to take down."],
    "This platform was built to show the power that is already at our fingertips.",
    "I understand it is an extremely sharp double-edged sword. Freedom and privacy are a human right. No exceptions.",
    ["Do the right thing, and remember to trade — gamble — ", ["b", "responsibly"], "."],
    ["There is an IRC so you can talk amongst yourselves, but there is ", ["b", "NO SUPPORT"],
      ". Click links at your own discretion. Please be mindful: it is your responsibility to protect your funds, your security and your information."],
  ];

  const para = (p) => h("p", { class: "notice-p" },
    typeof p === "string" ? p : p.map((x) => (typeof x === "string" ? x : h(x[0], null, x[1]))));
  const key = (who, what) => h("div", null, h("b", null, who), h("div", { class: "sub" }, what));

  router.on("notice", (el) => {
    const draw = () => render(el,
      h("div", { class: "notice" },
        h("div", { class: "eyebrow mt" }, "A message from the dev"),
        h("h1", { class: "mt8 mb" }, "Read this once"),
        h("div", { class: "notice-body" }, body().map(para)),
        h("div", { class: "notice-sign mono" }, "Safe travels, space cowboy."),
        // the facts behind the message, so it is checkable rather than only a promise
        h("h3", { class: "mt" }, "What that means here"),
        h("div", { class: "notice-facts" },
          U.kv("Owner", "the factory key survives — what it can do is below"),
          U.kv("Custody", fed() + " federation holds the " + wrapped() + "; the vault contract cannot"),
          U.kv("Support", "none — nobody can act on your behalf"),
          U.kv("Moderation", "none — nothing posted can be removed"),
          U.kv("Fees", (share() || "the contract's share") + " of claimed DERO fees to Pass holders; wrapped-asset fees to none"),
          U.kv("Your keys", "never leave your wallet; this app cannot see them"),
          U.kv("Contracts", D.cfg.network + " · no update path in any of them"),
          U.kv("This app", "republishable under the TELA INDEX key until it is discarded"),
          U.kv("Your node", "every number on screen is what it reported")),
        h("h3", { class: "mt" }, "Who still holds a key"),
        h("div", { class: "stack mt8" },
          key("The factory owner", "Can change the registration fee, and the treasury address this app checks every market against — which would hide every " +
            "listed market from this app, though not from the chain. Cannot touch a listed contract or a balance. There is no renounce, only a transfer to another key."),
          key("The treasury", "Receives every protocol fee: the trade fees anyone may claim from a market (they pay only the treasury), registration and chat fees, " +
            "pass mints, slashed signer bonds and vault withdrawal fees. It runs the bot that deposits the pass share, and nothing in a contract makes it."),
          key("The app publisher", "The TELA INDEX that serves these files is installed at ring size 2, so it can be updated: whoever holds that key can publish a new " +
            "version of this app until the key is thrown away. Settings shows the commit you are running."),
          key("The federation", fed() + " bonded signers hold the " + wrapped() + " behind the wrapped assets. They can move it; the vault contract cannot. " +
            "Their count, quorum and bond are on the Deposit & withdraw screen."),
          key("Your node", "Every listing, price, balance and code check on screen is what the node you connected to reported. The app has no second source.")),
        h("div", { class: "note mt" },
          "None of this is a promise. Each line names the contract rule, the key or the person that makes it so. If any other " +
          "screen in this app appears to contradict it, this page is the one that is true.")));
    draw();
    // the pass share and the vault's numbers arrive with the data refreshes; fill them in rather than leave the fallback
    D.onChange(() => router.current && router.current.name === "notice" && draw());
  });
})();
