// DMZ allow.js — the contracts V.app.tx will send to (Y1-10): one allowlist, so no screen keeps a send path of its own.
// Admitted: a listed market, holder NFT or NFA (each verified when it was listed: code hash and treasury, feed.js); the
// build's factory, social, Founders Pass, vaults and chat; and one generic entry for the build's own promotion and
// sentiment contracts (config.js promo, rate): only the entrypoints this app calls on each, and only once the code the
// node holds hashes to the template this build ships (app/templates/<kind>.bas). Everything else is refused, in words.
(function () {
  const V = window.Veil, D = V.data;
  // the contracts a build names by config key, with the entrypoints this app sends to each
  const SOLO = { promo: ["Bid", "ClaimProceeds"], rate: ["Vote"] };
  const isScid = (x) => typeof x === "string" && /^[0-9a-f]{64}$/.test(x);
  const good = new Map(); // scid -> kind once its code matched (contracts are immutable); a failed check is asked again
  async function codeIs(scid, kind) {
    if (good.get(scid) === kind) return true;
    try {
      const [r, t] = await Promise.all([V.node.getSC(scid, { code: true }), fetch("templates/" + kind + ".bas").then((x) => (x.ok ? x.text() : ""), () => "")]);
      if (!t || !r || !r.code || (await V.sha256Hex(r.code)) !== (await V.sha256Hex(t))) return false;
      good.set(scid, kind); return true;
    } catch (e) { return false; }
  }
  // null when this call may be sent; otherwise why not. A node that does not answer is a refusal (fail closed).
  async function check(opts) {
    const s = opts && opts.scid, cfg = D.cfg || {};
    if (!isScid(s)) return "Refusing: bad contract id";
    if (D.assets.has(s) || D.nfts.has(s) || D.nfas.has(s) || s === cfg.factory || s === cfg.social || (D.founders && s === D.founders.scid) ||
      D.allVaults().some((v) => v.scid === s) || (cfg.chat && s === cfg.chat)) return null;
    const kind = Object.keys(SOLO).find((k) => isScid(cfg[k]) && cfg[k] === s);
    if (!kind) return "Refusing: contract is not a verified listing";
    if (!SOLO[kind].includes(opts.entry)) return "Refusing: this app sends only " + SOLO[kind].join(" and ") + " to the " + kind + " contract";
    if (!(await codeIs(s, kind))) return "Refusing: the " + kind + " contract's code on your node does not match the template this build ships";
    return null;
  }
  V.allow = { SOLO, check };
})();
