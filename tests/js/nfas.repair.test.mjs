// The NFA page's account of a held bid (FINDINGS_2026-09-10 NFA-2). contracts/nfa.bas resetVars (356-370) clears
// listType, bidCount and startPrice but not currBidAddr/currBidAmt, so a bid can outlive the listing that took it.
// processHighestBidder (291) reads those two keys and nothing else: an empty Bid() on ANY listing of that asset,
// once checkActive returns 111, hands the unit to that address at that amount. The page has to say so, in every
// state the strand can be seen in, and must not offer a Close as if the unit were coming back.
//
// app/nfas.js is an IIFE over `window`, like app/ui.js in sanitize.test.mjs. The shim below supplies the pieces it
// destructures at load and reads at draw; `h` builds plain objects so the assertions read the rendered words.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ROOT, A } from "./harness.mjs";

const SCID = "a".repeat(64);
const OWNER = "dero1qyowner";
const BIDDER = "dero1qybidder";

function textOf(n) {
  if (n === null || n === undefined || n === false || n === true) return "";
  if (Array.isArray(n)) return n.map(textOf).join(" ");
  if (typeof n === "object") return (n.label || "") + " " + n.kids.map(textOf).join(" ");
  return String(n);
}

// A record with every key app/nfas.js reads, at the values a fresh NFA has; each case overrides what it is about.
function rec(over) {
  return Object.assign({
    scid: SCID, sig: "sig", name: "Unit", collection: "", type: "Image", version: "v1.1.1", descr: "",
    owner: OWNER, creator: OWNER, prevOwner: "", quote: "", quoteDecUnknown: false,
    listType: "", scBalance: 0n, startBlockTime: 0n, endBlockTime: 0n, cancelBuffer: 300n,
    startPrice: 0n, bidCount: 0n, currBidAmt: 0n, currBidPrice: 0n, bidder: "",
    royalty: 5n, artificerFee: 0n, protoFee: 100n, charityDonatePerc: 0n, charityAddr: "",
    staticBidIncr: 100n, percentBidIncr: 500n, previousSalePrice: 0n, previousAuctionPrice: 0n,
    ownerCanUpdate: 0n, file: "", fileSign: "", cover: "", icon: "", tags: "", checkC: "", checkS: "",
  }, over);
}

// Loads a fresh copy of app/nfas.js: the module memoises its last draw key, so each case gets its own instance.
function loadNFAs(s, viewer) {
  const buttons = [];
  const h = (tag, attrs, ...kids) => ({ tag, attrs: attrs || {}, kids });
  const win = { Veil: {} };
  const V = win.Veil;
  V.ui = {
    h, render: (el, ...kids) => { el.kids = kids; },
    avatar: () => h("i"), kv: (l, v) => h("div", null, l, v),
    button: (label, cls, onclick, opts) => { const b = { tag: "button", attrs: {}, kids: [], label, disabled: !!(opts && opts.disabled) }; buttons.push(b); return b; },
    router: { go: () => {}, current: { name: "nfa", param: SCID } },
    openSheet: () => {}, closeSheet: () => {}, toast: () => {},
    safeUrl: () => "", verified: () => null, clean: (x) => x, segmented: () => h("div"),
  };
  V.amm = A;
  V.data = {
    nfas: new Map([[SCID, s]]), lastRefresh: 1, vault: null, height: 0,
    qsym: (r) => (r.quote ? "XMR" : "DERO"), qdec: (r) => (r.quote ? 12n : 5n),
    displayName: (a) => (a === OWNER ? "the owner" : a === BIDDER ? "the bidder" : a),
    vaultOf: () => null, onChange: () => {}, refresh: async () => {}, changed: () => {},
  };
  V.wallet = { canon: viewer, connected: !!viewer, getBalance: () => new Promise(() => {}) };
  V.settings = { images: false };
  V.app = { tx: async () => false, connectWallet: () => {} };
  V.shortAddr = (a) => String(a).slice(0, 8);
  V.canonAddress = (a) => a;
  new Function("window", readFileSync(join(ROOT, "app", "nfas.js"), "utf8"))(win);
  const el = { kids: [] };
  V.nfas.page(el, SCID);
  return { text: textOf(el.kids).replace(/\s+/g, " "), buttons };
}

const nowSec = () => BigInt(Math.floor(Date.now() / 1000));

// The three states the same strand can be read in. In each, the page must name the claim and must not describe it
// as a refund that only the next listing can release.
test("a held bid is disclosed as a standing claim when the asset is not listed", () => {
  const r = loadNFAs(rec({ currBidAmt: 200000n, bidder: BIDDER }), OWNER);
  assert.match(r.text, /Held bid/);
  assert.match(r.text, /any wallet can settle/i);
  assert.equal(/nothing else releases it/.test(r.text), false, "the false clause is still on the page");
});

test("a held bid is still on the page once the owner starts a new listing", () => {
  const r = loadNFAs(rec({ listType: "auction", scBalance: 1n, startBlockTime: nowSec() - 60n, endBlockTime: nowSec() + 3600n, startPrice: 500000n, currBidAmt: 200000n, bidder: BIDDER }), OWNER);
  assert.match(r.text, /Held bid/, "the strand vanishes from a live listing");
  assert.match(r.text, /any wallet can settle/i);
  assert.match(r.text, /Held bid 2 DERO/, "the held amount is shown without its symbol");
});

test("the bidder of a strand is not called the top bidder of a listing they did not bid on", () => {
  const r = loadNFAs(rec({ quote: "b".repeat(64), listType: "auction", scBalance: 1n, startBlockTime: nowSec() - 60n, endBlockTime: nowSec() + 3600n, startPrice: 500000n, currBidAmt: 200000n, bidder: BIDDER }), BIDDER);
  assert.equal(/You are the top bidder/.test(r.text), false);
  assert.match(r.text, /Held bid [0-9.,]+ XMR/, "a wrapped-quote held bid is shown without its symbol");
});

test("an ended listing with no bid but a held bid does not promise the unit back", () => {
  const r = loadNFAs(rec({ listType: "sale", scBalance: 1n, startBlockTime: nowSec() - 7200n, endBlockTime: nowSec() - 60n, startPrice: 500000n, currBidAmt: 200000n, bidder: BIDDER }), OWNER);
  assert.equal(/Nothing sold; closing returns the unit to your wallet/.test(r.text), false, "the Close is still described as a return");
  assert.match(r.text, /before your Close is mined/);
  assert.match(r.text, /Held bid/);
});

// The ordinary case must stay quiet: no listing, no bid, no warning.
test("an asset with no held bid says nothing about one", () => {
  const r = loadNFAs(rec({}), OWNER);
  assert.equal(/Held bid/.test(r.text), false);
  assert.equal(/any wallet can settle/i.test(r.text), false);
});

test("an ended auction that took a real bid still reads as a settlement, not a strand", () => {
  const r = loadNFAs(rec({ listType: "auction", scBalance: 1n, startBlockTime: nowSec() - 7200n, endBlockTime: nowSec() - 60n, startPrice: 100000n, bidCount: 2n, currBidAmt: 300000n, bidder: BIDDER }), OWNER);
  assert.match(r.text, /Closing pays 3 DERO/);
  assert.equal(/Held bid/.test(r.text), false);
});
