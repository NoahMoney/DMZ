// DMZ names.js — what a market is called and what it may not call itself, split out of feed.js (DOC size, MT-E10).
// Adds to V.data the vault list and chain nouns, the names a wrapped asset reserves and the impersonation that wears
// them, split pairs, the naming of a market's quote and base, and decorate(), which feed.js runs over every record
// on each refresh. Nothing here reads the chain: it names and marks records feed.js has already read and verified.
(function () {
  const V = window.Veil, B = BigInt, S = V.stats;
  Object.assign(V.data, {
    // Every wrapped asset this build knows about: one vault per foreign chain, because a vault holds exactly one
    // asset. `vault`/`vaultAsset` are the older single-vault spelling and are folded in for compatibility.
    allVaults() {
      const c = this.cfg, out = [];
      const add = (v) => { if (v && /^[0-9a-f]{64}$/.test(v.scid || "") && !out.some((x) => x.scid === v.scid)) out.push(v); };
      for (const v of c.vaults || []) add(v);
      if (c.vault) add(Object.assign({ scid: c.vault }, c.vaultAsset || {}));
      return out;
    },
    // The vaults a user may deposit into or quote a launch in. A superseded vault (legacy: true) is left out
    // here and only here: its markets are still named and scaled by allVaults(), and its units can still be
    // burned, because a holder of them must always have the exit.
    vaults() { return this.allVaults().filter((v) => !v.legacy); },
    // The short noun for a vault's chain. `chain` in the deployment record is a descriptive clause — "Monero —
    // simulated on this network (12 decimals, as Monero)" — and it was spliced into running prose and cut at 40
    // characters, which severed a word and left the parenthesis open on seven lines of the wrap screen, the
    // destination field's own label among them. The clause still has one right place: shown whole, on its own.
    // Wherever a sentence needs a noun it gets this instead, and nothing in it depends on the clause's length.
    chainName(v) { return V.ui.clean(String((v && v.name) || "").split(/[(—–-]/)[0].trim() || (v && v.sym) || "its own chain", 24); },
    // The name of a wrapped asset belongs to its vault, and a vault is named by the shipped config, so its
    // symbol cannot be forged. Anything else wearing that name is claiming to be it. This is the name most
    // worth stealing on DMZ: the product points a Monero holder at a ticker called XMR, so a token that simply
    // calls itself WXMR is the cheapest attack there is. "w" + symbol is reserved too, since that is how a
    // wrapped asset is usually written.
    // With no vault (mainnet's build) XMR and ZEC stay reserved: nothing here can be the real one.
    named() { const v = this.allVaults(); return v.length ? v : [{ scid: "-", sym: "XMR", name: "Monero" }, { scid: "-", sym: "ZEC", name: "Zcash" }]; },
    reservedBy(ticker) {
      const k = V.ui.tickerKey(ticker || "");
      if (!k) return null;
      for (const v of this.named()) {
        for (const nm of [v.sym, v.name, "w" + (v.sym || "")]) {
          if (nm && V.ui.tickerKey(nm) === k) return v;
        }
      }
      return null;
    },
    // Whether this market wears a wrapped asset's name without being that asset. The genuine article is the
    // market whose traded asset IS the vault; nothing a creator can type is evidence either way.
    // A display name carrying a vault's symbol or name as a whole word — "Wrapped Monero" beside a ticker that
    // copies nothing — was the gap left when only the ticker was guarded. Whole words, not substrings: a token
    // called Zecret is not claiming to be Zcash, and a red "Not real ZEC" on it would teach users to ignore the
    // pill that matters.
    reservedIn(text) {
      const words = new Set(String(text || "").split(/[^\p{L}\p{N}]+/u).map((w) => V.ui.tickerKey(w)).filter(Boolean));
      if (!words.size) return null;
      for (const v of this.named()) {
        const first = String(v.name || "").split(/[^\p{L}\p{N}]+/u)[0];
        for (const nm of [v.sym, "w" + (v.sym || ""), first]) { const k = nm && V.ui.tickerKey(nm); if (k && words.has(k)) return v; }
      }
      return null;
    },
    impersonation(s) {
      if (!s) return null;
      const v = this.reservedBy(s.ticker) || this.reservedIn(s.name);
      if (!v || s.asset === v.scid || s.scid === v.scid) return null;
      return V.ui.clean(v.sym || v.name || "a wrapped asset", 10);
    },
    // A split pair: two or more contracts trading the same base against the same quote, each with its own
    // reserves. The thinner one gives a worse price for the same size, and a large order can empty it while the
    // deeper one is untouched — the owner's own requirement is that nobody be handed the thin half unwarned.
    // Computed here once per refresh so every surface reads one answer: it lived inside markets.js, which meant
    // Home, the tape, trending and the trade sheet could not see it, and the trade sheet is where the drain
    // happens. A launch's own token is its scid; a pool names its asset.
    pairKey(s) { const id = (x) => (typeof x === "string" && /^[0-9a-f]{64}$/.test(x) ? x : ""); const b = id(s.asset) || id(s.scid); return b ? b + "/" + (id(s.quote) || "dero") : ""; },
    // What the contract actually holds of its quote: a curve holds what it raised, an AMM its quote reserve.
    // Only ever compared inside one pair, so both sides are the same asset with the same decimals — this never
    // becomes a cross-quote comparison, which there is no rate for and must never be one.
    depth(s) { return s.kind === "launch" && s.phase === 0n ? s.real || 0n : s.rd || 0n; },
    // The vault whose wrapped asset this market trades, or null when the market is a native DERO one.
    // The symbol a market's prices, reserves and fees are denominated in. Never assume DERO: a market quoted in
    // wrapped XMR shows XMR amounts, and labelling them DERO would misstate the price by whatever the rate is.
    // Give a market its quote's display name. Safe to call more than once and on a market with no quote.
    nameQuote(s) {
      if (!s) return s;
      const v = s.quote ? this.allVaults().find((x) => x.scid === s.quote) : null, known = !!(v && Number.isInteger(v.dec));
      s.quoteLegacy = !!(v && v.legacy);
      s.quoteSym = !s.quote ? "DERO" : v ? V.ui.clean(v.sym || v.name || "wrapped", 10) : s.quote.slice(0, 6);
      s.quoteDec = !s.quote ? 5 : known ? v.dec : 5;
      // A quote nobody names has no known scale. The 5 above is a placeholder that keeps the maths finite, not a
      // reading, and a screen that scales an amount by it must say so rather than print a figure that is off by
      // whatever the real scale is.
      s.quoteDecUnknown = !!s.quote && !known;
      s.pair = (s.ticker || "?") + " / " + s.quoteSym;
      return s;
    },
    qsym(s) { return (s && s.quoteSym) || "DERO"; },
    qdec(s) { return BigInt(Number.isInteger(s && s.quoteDec) ? s.quoteDec : 5); },
    vaultOf(s) {
      if (!s) return null;
      for (const v of this.allVaults()) if (s.asset === v.scid || s.scid === v.scid || s.quote === v.scid) return v;
      return null;
    },

    // Name a pool's base asset. A launch names it; a wrapped asset is not a launch, so its vault's deployment
    // record names it, or its market would show a contract id where a symbol belongs. Anything else keeps the
    // placeholder 5 decimals from loadContract and is marked decUnknown: that 5 is not a reading, and a screen
    // that scales an amount by it must say so. Safe to call more than once and on an unlisted pool.
    nameBase(s) {
      if (!s || s.kind !== "pool") return s;
      const l = this.assets.get(s.asset), bv = this.allVaults().find((x) => x.scid === s.asset);
      s.assetVerified = !!(l && l.kind === "launch"); s.icon = "";
      if (s.assetVerified) { s.name = l.name + " pool"; s.ticker = l.ticker; s.dec = l.dec; s.icon = l.icon; }
      else if (bv) { s.ticker = V.ui.clean(bv.sym || bv.name || "wrapped", 10); s.name = V.ui.clean(bv.name || "wrapped asset", 40); s.dec = B(Number.isInteger(bv.dec) ? bv.dec : 5); s.wrappedBase = true; s.vaultRec = bv; s.baseLegacy = !!bv.legacy; }
      s.decUnknown = !s.assetVerified && !bv;
      return s;
    },
    decorate() {
      for (const s of this.assets.values()) this.nameBase(s);
      // Name the quote so a market can be read as a pair. A wrapped quote is named by its vault record; anything
      // else is shown by its contract id rather than guessed at.
      for (const s of this.assets.values()) this.nameQuote(s);
      // impersonation: markets whose tickers read alike (look-alikes folded) are all marked, so the copy never looks safer than the original
      const groups = new Map();
      for (const s of this.assets.values()) { if (s.kind === "nft" || (s.kind === "pool" && s.assetVerified)) continue; const k = V.ui.tickerKey(s.ticker); s.lookalike = V.ui.hasHomoglyph(s.ticker); s.impersonates = this.impersonation(s); (groups.get(k) || groups.set(k, []).get(k)).push(s); }
      for (const g of groups.values()) for (const s of g) s.dupTicker = g.length > 1;
      // Two genuine pools of one wrapped asset share its ticker because they trade the same thing. That is a
      // split pair, which markets.js already reports far more usefully, and calling it a shared ticker put a
      // warning on the real asset while a copy of it carried none — the guard pointing the wrong way.
      for (const g of groups.values()) if (g.length > 1 && g.every((s) => s.wrappedBase)) for (const s of g) s.dupTicker = false;
      // An NFA's name is the whole of what it sells, and the fold above runs over this.assets, which never holds
      // one. So an NFA calling itself wXMR — the cheapest name to steal here — wore the plain Verified shield
      // while a pool with that name was caught by this same code. Same three marks, same helpers, over nfas.
      const ng = new Map();
      for (const s of this.nfas.values()) { s.lookalike = V.ui.hasHomoglyph(s.name); s.impersonates = this.impersonation({ ticker: s.name, name: s.name, scid: s.scid }); const k = V.ui.tickerKey(s.name); (ng.get(k) || ng.set(k, []).get(k)).push(s); }
      for (const g of ng.values()) for (const s of g) s.dupTicker = g.length > 1;
      for (const s of this.assets.values()) if (s.kind === "pool" && s.assetVerified) { const l = this.assets.get(s.asset); if (!l) continue; s.dupTicker = !!l.dupTicker; s.lookalike = !!l.lookalike; s.impersonates = l.impersonates || null; } // a launch and its own pool share a ticker by design
      // split pairs: ids, deepest first, on every member (ids rather than objects, so a cached record has no cycle)
      const byPair = new Map();
      for (const s of this.assets.values()) { s.splitWith = null; s.splitDeepest = false; if (s.kind === "nft") continue; const k = this.pairKey(s); if (k) (byPair.get(k) || byPair.set(k, []).get(k)).push(s); }
      for (const g of byPair.values()) {
        if (g.length < 2) continue;
        g.sort((x, y) => (this.depth(y) > this.depth(x) ? 1 : this.depth(y) < this.depth(x) ? -1 : 0));
        const ids = g.map((x) => x.scid);
        for (const s of g) { s.splitWith = ids; s.splitDeepest = g[0] === s; }
      }
      for (const s of this.assets.values()) S.enrich(s, this.height);
      for (const n of this.nfts.values()) { const t = this.assets.get(n.token); n.tokenTicker = t ? t.ticker : V.shortAddr(n.token); n.tokenDec = t ? t.dec : 5n; n.tokenDecUnknown = !t; }
    },
  });
})();
