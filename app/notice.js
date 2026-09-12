// DMZ notice.js — the message from the dev. It is the plainest statement of what this thing is: unregulated,
// unsupported, and held by exactly the keys it names. Every other screen has to be careful not to contradict it,
// so it is worth reading before changing any copy anywhere else in the app.
//
// Every categorical sentence here names the mechanism that makes it true — a contract rule, a key, a person —
// or it was cut. The keys that survive deployment (the treasury every contract pays, on a development build the TELA
// INDEX that serves these files, and on an older factory its owner; on mainnet the INDEX has none) and the federation
// behind the wrapped assets are listed rather than wished away: a plaque that says "nobody holds a switch" while three
// keys do is the one thing this page must never be. The pass share is read from founders.bas (`share`, bps, immutable), never typed here.
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
  // A build that names no vault (mainnet's names none) offers no wrapped asset, so there is no federation to
  // name. Every screen that would mention a wrapped asset says so in these words, so none can say more than this.
  const noWrap = () => !D.allVaults().length;
  const NOWRAP = "Wrapped XMR and ZEC are not offered on this network.";
  const NOWRAP_WHY = "Wrapping means real Monero or Zcash held by a federation: named people, any k of whom could move every coin. " +
    "DMZ settles XMR and ZEC by atomic swap instead. Each side locks on its own chain under a hash and a time limit and keeps an exit of its own " +
    "for as long as it acts inside its window; nobody holds a coin on anyone's behalf.";
  V.notice = { noWrap, NOWRAP, NOWRAP_WHY };
  // On mainnet the TELA INDEX is installed at ring size 16 (MAINNET_DEPLOYMENT.md): its owner is "anon" and no key can
  // update it, which verifydeploy checks on chain. A development build may be served from an INDEX someone can update.
  const fixedApp = () => D.cfg.network === "mainnet";

  // Kept close to the words that were written, corrected where the contracts do not deliver them. Each entry is
  // one paragraph; an array is a paragraph whose parts carry emphasis, because a few of these are load-bearing
  // and should not be skimmed.
  const body = () => [
    "Dear users,",
    "Please be respectful and kind to each other.",
    ["This platform is completely unregulated. ", ["b", "Its creators hold no key that can spend what you hold on DERO."],
      noWrap() ? " The keys that do survive are named below. " + NOWRAP + " No federation holds a coin for anyone here."
        : " The keys that do survive, and the federation that holds the coins behind " + wrapped() + ", are named below."],
    (share() ? share() + " of the DERO fees the treasury claims" : "A share of the DERO fees the treasury claims — the number is stored in the pass contract —") +
      " is deposited for the Founders Pass holders. The treasury's own bot makes that deposit; no contract forces it." + (noWrap() ? "" : " Fees earned in a wrapped asset reach no holder today."),
    ["Code is law. ", ["b", "The contracts are final."], " None of them has an update path. " +
      (fixedApp() ? "Nor has the app you read them through: its TELA INDEX was installed with no owner, so a new version would be a new INDEX, found separately; "
        : "The app you read them through can be republished under one key until that key is thrown away; ") +
      "Settings shows the commit you are running. There is no server to take down."],
    "This platform was built to show the power that is already at our fingertips.",
    "I understand it is an extremely sharp double-edged sword. Freedom and privacy are a human right. No exceptions.",
    ["Do the right thing, and remember to trade — gamble — ", ["b", "responsibly"], "."],
    ["There is an IRC so you can talk amongst yourselves, but there is ", ["b", "NO SUPPORT"],
      ". Click links at your own discretion. Please be mindful: it is your responsibility to protect your funds, your security and your information."],
  ];

  const para = (p) => h("p", { class: "notice-p" },
    typeof p === "string" ? p : p.map((x) => (typeof x === "string" ? x : h(x[0], null, x[1]))));
  const key = (who, what) => h("div", null, h("b", null, who), h("div", { class: "sub" }, what));
  // The factory's powers are read from factory storage (feed.js registry_: v, owner, renounced), never assumed.
  // factory.bas v2 has no owner: Initialize fixes the treasury and the registration fee, and no function changes them.
  // An older v1 factory (the simulator's until it is redeployed) had an owner until Renounce, which deletes owner and
  // stores the height it ran at; neither version has a transfer of ownership.
  const R = () => D.registry || null;
  const renounced = () => (R() && R().renounced) || "";
  const noOwner = () => !!R() && !R().owner && !renounced() && R().v >= 2;
  const FIXED = "the treasury and the registration fee were fixed when it was installed, and no function in it can change them";
  const factoryKey = () => !R()
    ? key("The factory", "Not read yet: whether it has an owner is read from the chain.")
    : noOwner() ? key("The factory", "No owner: " + FIXED + ". A different treasury or fee would take a new factory. It cannot touch a listed contract or a balance.")
    : renounced()
    ? key("The factory owner", "Renounced at block " + renounced() + ": nobody can change the treasury or the registration fee.")
    : key("The factory owner", (R().owner ? "Held by " + V.shortAddr(R().owner) + ". " : "Its key could not be read. ") +
      "Can change the registration fee, and the treasury the factory forwards every registration fee to. " +
      ((D.cfg.treasuries || []).length
        ? "This app checks markets against the treasury its build ships, not that pointer, so moving it does not hide a listed market. "
        : "This build ships no treasury of its own, so it checks every market against that pointer: moving it would hide every listed market from this app, though not from the chain. ") +
      "Cannot touch a listed contract or a balance. Can renounce, which ends both powers for ever; there is no transfer to another key.");

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
          U.kv("Owner", !R() ? "not read yet" : noOwner() ? "none — the factory has no owner; " + FIXED
            : renounced() ? "none — the factory was renounced at block " + renounced() : "the factory key survives — what it can do is below"),
          U.kv("Custody", noWrap() ? "none — no wrapped asset here, so no federation" : fed() + " federation holds the " + wrapped() + "; the vault contract cannot"),
          U.kv("Support", "none — nobody can act on your behalf"),
          U.kv("Moderation", "none — nothing posted can be removed"),
          U.kv("Fees", (share() || "the contract's share") + (noWrap() ? " of claimed DERO fees to Pass holders" : " of claimed DERO fees to Pass holders; wrapped-asset fees to none")),
          U.kv("Your keys", "never leave your wallet; this app cannot see them"),
          U.kv("Contracts", D.cfg.network + " · no update path in any of them"),
          U.kv("This app", fixedApp() ? "fixed: its TELA INDEX has no owner; a new version would be a new INDEX" : "republishable under the TELA INDEX key until it is discarded"),
          U.kv("Your node", "every number on screen is what it reported")),
        h("h3", { class: "mt" }, "Who still holds a key"),
        h("div", { class: "stack mt8" },
          factoryKey(),
          key("The treasury", "Receives every protocol fee: the trade fees anyone may claim from a market (they pay only the treasury), registration and chat fees, " +
            "pass mints" + (noWrap() ? "" : ", slashed signer bonds and vault withdrawal fees") + ". It runs the bot that deposits the pass share, and nothing in a contract makes it."),
          fixedApp()
            ? key("The app publisher", "None. On mainnet the TELA INDEX that serves these files is installed at ring size 16, so its owner is recorded as \"anon\" " +
              "and no key can update it; tools/verifydeploy checks that on chain. A new version would be a new INDEX with its own id. Settings shows the commit you are running.")
            : key("The app publisher", "The TELA INDEX that serves this build can be updated by whoever holds its key, until that key is thrown away. On mainnet it is " +
              "installed with no owner instead. Settings shows the commit you are running."),
          noWrap() ? key("No federation", NOWRAP + " No signer holds a coin for anyone here. XMR and ZEC settle by atomic swap, which has no custodian: each side trusts " +
            "its own nodes, and its own swap program to be online at its window.")
            : key("The federation", fed() + " bonded signers hold the " + wrapped() + " behind the wrapped assets. They can move it; the vault contract cannot. " +
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
