// DMZ notice.js — the message from the dev. It is the plainest statement of what this thing is: unregulated,
// unsupported, and held by exactly the keys it names. Every other screen has to be careful not to contradict it,
// so it is worth reading before changing any copy anywhere else in the app.
//
// Every categorical sentence here names the mechanism that makes it true — a contract rule, a key, a person —
// or it was cut. The keys that survive deployment (the treasury every contract pays, on a development build the TELA
// INDEX that serves these files, and on an older factory its owner; on mainnet the INDEX has none) and, where the build
// names a vault, the federation that holds the XMR and ZEC are listed rather than wished away: a plaque that says
// "nobody holds a switch" while keys do is the one thing this page must never be. There are no atomic swaps: XMR and
// ZEC move only through a vault (contracts/vault.bas), which is custody. The pass share is read from founders.bas
// (`share`, bps, immutable), never typed here, and the fee words follow the pass's own description.
//
// This page is deliberately not a card, not a dialog and not dismissible. It is a plaque.
(function () {
  const V = window.Veil, U = V.ui, D = V.data;
  const { h, render, router } = U;

  const share = () => (D.founders && D.founders.share !== undefined ? Number(D.founders.share) / 100 + "%" : null);
  const wrapped = () => { const v = D.vaults(); return v.length ? v.map((x) => U.clean(x.sym || x.name || "wrapped", 12)).join(" and ") : "XMR and ZEC"; };
  // what a pass is paid in: DERO, the wrapped unit of each vault this build names (itself a DERO token), and DERO tokens
  const assets = () => ["DERO", ...D.vaults().map((x) => "w" + U.clean(x.sym || "", 8))].join(", ") + " and DERO tokens";
  // k and the active count come from the vault contract once the wrap screen has read it, and only where the build names
  // one vault: two vaults have two federations, and one's numbers are not the other's. Otherwise the shape is stated.
  const one = () => !!D.vault && D.vaults().length === 1;
  const fed = () => (one() ? D.vault.k + "-of-" + D.vault.na : "k-of-n");
  const kn = () => (one() ? D.vault.k + " of the " + D.vault.na : "k of the n");
  // A build that names no vault (the mainnet test's names none) has no wrapped asset and no way for XMR or ZEC in or
  // out. Every screen that would mention either says so in these words, so none can say more than this.
  const noWrap = () => !D.allVaults().length;
  const NOWRAP = "Wrapped XMR and ZEC are not offered on this network.";
  const NODEP = "Deposits and withdrawals of XMR and ZEC are not available on this network.";
  const NOWRAP_WHY = "Wrapping means real Monero or Zcash held by a federation: named people, any k of whom could move every coin. " +
    "This build names no vault, so nothing brings XMR or ZEC to DERO here, and nothing takes them out.";
  V.notice = { noWrap, NOWRAP, NODEP, NOWRAP_WHY };
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
      noWrap() ? " The keys that do survive are named below. No federation holds a coin for anyone here."
        : " The keys that do survive, and the federation that holds the " + wrapped() + " behind the wrapped assets, are named below."],
    noWrap()
      ? ["There are no atomic swaps on DMZ. XMR and ZEC could come in and go out only through a vault, and this build names none. ", ["b", NODEP], " " + NOWRAP]
      : ["There are no atomic swaps on DMZ. XMR and ZEC come in and go out only through a vault: you send them to a deposit address of your own and receive the same amount of the wrapped asset on DERO, 1:1, and you burn it to take them out. ",
        ["b", "A vault is custody:"], " " + kn() + " named signers of " + (one() ? "the vault" : "each vault") + " can move its whole reserve, and the vault contract cannot. You trust them with the coins for as long as you hold the wrapped asset."],
    "The registered Founders Passes receive " + (share() ? share() + " of the trading fees the treasury claims from DMZ markets," : "a share of the trading fees the treasury claims from DMZ markets, as stored in the pass contract,") +
      " each in the asset the fee was taken in, never converted to DERO: " + assets() + ". The treasury's bot deposits them into the pass contract, where each registered pass claims its part. " +
      "The bot makes every deposit, and no contract forces it. A deposit is shared by the passes registered when it is made. " +
      "While no pass is registered nothing is deposited: the bot holds those fees and pays them in the first deposit after a pass registers.",
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
          U.kv("Custody", noWrap() ? "none — no vault on this network, so no federation holds a coin" : fed() + " federation of named signers holds the " + wrapped() + " and can move it; the vault contract cannot"),
          U.kv("XMR and ZEC", noWrap() ? "not available — deposits and withdrawals need a vault, and this network has none" : "in and out only through the vault, 1:1 against the wrapped asset; there are no atomic swaps"),
          U.kv("Support", "none — nobody can act on your behalf"),
          U.kv("Moderation", "none — nothing posted can be removed"),
          U.kv("Fees", (share() || "the contract's share") + " of the trading fees the treasury claims, to registered passes, each in its own asset; the treasury's bot deposits it, and no contract forces it"),
          U.kv("Your keys", "never leave your wallet; this app cannot see them"),
          U.kv("Contracts", D.cfg.network + " · no update path in any of them"),
          U.kv("This app", fixedApp() ? "fixed: its TELA INDEX has no owner; a new version would be a new INDEX" : "republishable under the TELA INDEX key until it is discarded"),
          U.kv("Your node", "every number on screen is what it reported")),
        h("h3", { class: "mt" }, "Who still holds a key"),
        h("div", { class: "stack mt8" },
          factoryKey(),
          key("The treasury", "Receives every protocol fee: the trade fees anyone may claim from a market (they pay only the treasury), registration and chat fees, the rate contract's vote fees, " +
            "the protocol fee on NFA sales" + (noWrap() ? " and pass mints" : ", pass mints, and from each vault its index registration and withdrawal fees and slashed signer bonds") + ". " +
            "It runs the bot that deposits the pass share, and nothing in a contract makes it."),
          fixedApp()
            ? key("The app publisher", "None. On mainnet the TELA INDEX that serves these files is installed at ring size 16, so its owner is recorded as \"anon\" " +
              "and no key can update it; tools/verifydeploy checks that on chain. A new version would be a new INDEX with its own id. Settings shows the commit you are running.")
            : key("The app publisher", "The TELA INDEX that serves this build can be updated by whoever holds its key, until that key is thrown away. On mainnet it is " +
              "installed with no owner instead. Settings shows the commit you are running."),
          noWrap() ? key("No vault", NOWRAP + " " + NODEP + " No signer holds a coin for anyone here.")
            : key("The federation", "A " + fed() + " federation of bonded signers holds the " + wrapped() + " behind the wrapped assets. They can move all of it; the vault contract cannot. This is custody. " +
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
