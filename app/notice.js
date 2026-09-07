// DMZ notice.js — the message from the dev. It is the plainest statement of what this thing is: unregulated,
// unowned, unchangeable and unsupported. Every other screen has to be careful not to contradict it, so it is
// worth reading before changing any copy anywhere else in the app.
//
// This page is deliberately not a card, not a dialog and not dismissible. It is a plaque.
(function () {
  const V = window.Veil, U = V.ui, D = V.data;
  const { h, render, router } = U;

  // Kept close to the words that were written. Each entry is one paragraph; an array is a paragraph whose parts
  // carry emphasis, because a few of these are load-bearing and should not be skimmed.
  const BODY = [
    "Dear users,",
    "Please be respectful and kind to each other.",
    ["This platform is completely unregulated. ", ["b", "All rights and permissions have been revoked from its creators."]],
    "All platform fees are distributed amongst the NFT holders.",
    ["Code is law. ", ["b", "This code is final."], " There can be no further changes. There is no server to take down."],
    "This platform was built to show the power that is already at our fingertips.",
    "I understand it is an extremely sharp double-edged sword. Freedom and privacy are a human right. No exceptions.",
    ["Do the right thing, and remember to trade — gamble — ", ["b", "responsibly"], "."],
    ["There is an IRC so you can talk amongst yourselves, but there is ", ["b", "NO SUPPORT"],
      ". Click links at your own discretion. Please be mindful: it is your responsibility to protect your funds, your security and your information."],
  ];

  const para = (p) => h("p", { class: "notice-p" },
    typeof p === "string" ? p : p.map((x) => (typeof x === "string" ? x : h(x[0], null, x[1]))));

  router.on("notice", (el) => {
    render(el,
      h("div", { class: "notice" },
        h("div", { class: "eyebrow mt" }, "A message from the dev"),
        h("h1", { class: "mt8 mb" }, "Read this once"),
        h("div", { class: "notice-body" }, BODY.map(para)),
        h("div", { class: "notice-sign mono" }, "Safe travels, space cowboy."),
        // the facts behind the message, so it is checkable rather than only a promise
        h("h3", { class: "mt" }, "What that means here"),
        h("div", { class: "notice-facts" },
          U.kv("Owner", "none after deployment"),
          U.kv("Support", "none — nobody can act on your behalf"),
          U.kv("Moderation", "none — nothing posted can be removed"),
          U.kv("Fees", "distributed to Founders Pass holders"),
          U.kv("Your keys", "never leave your wallet; this app cannot see them"),
          U.kv("Contracts", D.cfg.network + " · immutable once installed")),
        h("div", { class: "note mt" },
          "Nothing on this page can be switched off, because there is nobody left holding a switch. If any other " +
          "screen in this app appears to contradict it, this page is the one that is true.")));
  });
})();
