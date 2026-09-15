// DMZ discover.js — the discovery sections on Home: ranked lists built only from figures the app already reads from
// the chain (stats.js), each section naming the figure it ranks by. A section exists only for a number the app has:
// there is no view count anywhere in DMZ, so there is no "Most viewed". XMR and ZEC reach DMZ as a vault's wrapped
// units, so their sections are the markets priced in those units.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, S = V.stats, H = V.home, { h, avatar, changePill, sparkline, router, icon } = U;
  const X = (V.disc = {});
  const N = 5;

  // ---- the rankings: pure over V.data, so tests/js/discovery.test.mjs runs them without a DOM ----
  // blocks in which the reserves moved (every trade, posted or not, netted per block), then trades their owners posted
  X.blocks24 = (s) => (s.m.upBlocks24 || 0) + (s.m.downBlocks24 || 0);
  X.posted24 = (s) => (s.m.buys24 || 0) + (s.m.sells24 || 0);
  const mkt = (s) => s && s.m && (s.kind === "launch" || s.kind === "pool");
  // D.ranked(): markets holding reserves, without the thin half of any split pair — that half's numbers are the
  // cheapest on the exchange to manufacture, so no section may advertise it (feed.js, and the UI pass checks it)
  X.trending = (n) => D.trending(n);
  // Volume is compared inside one quote only: there is no rate between DERO and anything else. It ranks the DERO
  // markets, and the section says so; markets priced in XMR or ZEC have their own sections.
  X.volume = (n) => D.ranked().filter((s) => !s.quote && s.m.vol24 > 0).sort((a, b) => b.m.vol24 - a.m.vol24).slice(0, n);
  X.active = (n) => D.ranked().filter((s) => X.blocks24(s) > 0).sort((a, b) => X.blocks24(b) - X.blocks24(a) || X.posted24(b) - X.posted24(a)).slice(0, n);
  // youngest first, by the block it was installed at; a market whose age could not be read is not guessed at
  X.fresh = (n) => D.list().filter((s) => mkt(s) && s.m.age !== null && (!s.splitWith || s.splitDeepest)).sort((a, b) => a.m.age - b.m.age).slice(0, n);
  // The largest 24h move over markets deep enough for a move to mean something: the floor is stats.js's thin line
  // (twenty DERO, or the quote's equivalent), the one the risk box uses. Without it a dust pool moved by one small
  // order owns this list. A quote of unknown scale has no depth to judge, so it is left out.
  X.movers = (n) => D.ranked().filter((s) => s.m.c24h !== null && s.points.length > 1 && !s.m.thin && !s.quoteDecUnknown).sort((a, b) => Math.abs(b.m.c24h) - Math.abs(a.m.c24h)).slice(0, n);
  // The promotion contract's current winners (V.promo, another file's). null when this build has no promotion
  // interface, so a missing module is never shown as "nothing is promoted".
  X.promos = () => { const P = V.promo; if (!P || typeof P.slots !== "function") return null; try { return (P.slots() || []).map((x) => D.assets.get(typeof x === "string" ? x : x && x.scid)).filter(Boolean); } catch (e) { return null; } };
  // The markets priced in a vault's unit. Only a vault record this build ships counts, so a token that merely calls
  // itself XMR is never among them. Ranked by the hot score, which is scaled per quote (stats.js).
  X.vault = (code) => D.vaults().find((v) => U.netOf(v)[0] === code) || null;
  X.wrapped = (v, n) => D.ranked().filter((s) => s.quote === v.scid).sort((a, b) => b.m.hot - a.m.hot || b.points.length - a.points.length).slice(0, n);

  // ---- presentation ----
  // one sparkline per section and market, rebuilt only when the contract state or the 10-minute window moves
  const sparks = new Map();
  const spark = (key, s) => {
    const k = key + s.scid, sig = s.sig + "|" + Math.floor(S.now() / 600); let c = sparks.get(k);
    if (!c || c.sig !== sig) { c = { sig, el: sparkline(s.points.filter((p) => p.ts >= S.now() - 86400), s, (s.m.c24h || 0) >= 0) }; sparks.set(k, c); if (sparks.size > 400) sparks.delete(sparks.keys().next().value); }
    return c.el;
  };
  // A card is one button: the whole row opens the market, one tab stop, 44px or more tall. The pair names it, the
  // name gives way beside it, and the right-hand figure is the one the section ranks by.
  X.card = (key, s, i, val, sub) => h("button", { class: "dcard", type: "button", "data-scid": s.scid, onclick: () => router.go("token", s.scid), "aria-label": U.pairAria(s) + ", " + s.name },
    h("span", { class: "rank" }, i + 1), avatar(s, "sm"),
    // line two: the wrapped mark, a real warning, and for a ticker another market also uses, the contract's short id
    // in the machine voice — identity without an accusation — then the name, which is what gives way
    h("div", { class: "grow ellipsis" }, h("div", { class: "row tight" }, U.pair(s, true), H.vmark(s), h("span", { class: "badges" }, H.rowBadges(s).slice(0, 1))),
      h("div", { class: "sub tiny ellipsis" }, U.wmark(s), H.mark(s, " · "), s.dupTicker ? h("span", { class: "mono" }, V.shortAddr(s.scid) + " · ") : null, s.name)),
    h("div", { class: "sp" }, spark(key, s)),
    h("div", { class: "right" }, h("span", { class: "num price" }, val), sub));
  const priceCell = (s) => [H.priceEl(s, "num"), changePill(s.m.c24h)];
  const empty = (t) => h("div", { class: "note dz-empty" }, t);
  const list = (key, rows, cell) => (rows.length ? h("div", { class: "dz-list" }, rows.map((s, i) => X.card(key, s, i, ...cell(s)))) : null);
  const sec = (id, ic, title, what, body, all) => h("section", { class: "dz-sec", "aria-labelledby": "dz-" + id },
    h("div", { class: "sh" }, h("span", { class: "ico" }, icon(ic)), h("div", { class: "grow" }, h("h2", { id: "dz-" + id }, title), h("div", { class: "sub tiny" }, what)),
      all ? h("button", { class: "linkbtn", type: "button", "aria-label": "See all: " + title, onclick: all }, "See all") : null),
    body);
  const see = (v) => () => router.go("markets", v);
  const q = (s) => " " + D.qsym(s);
  // XMR and ZEC: the markets priced in the vault's unit. A build with no vault says, in notice.js's words, that the
  // wrapped asset is not offered here, rather than showing an empty list that reads as "nobody trades it yet".
  X.wrapSec = (code, sym, key) => {
    const v = X.vault(code);
    if (!v) return sec(key, "gem", sym, "Markets priced in wrapped " + sym, empty("Wrapped " + sym + " is not offered on this network."));
    return sec(key, "gem", sym, "Priced in wrapped " + sym + ": 1:1 claims on the " + sym + " the vault's federation holds",
      list(key, X.wrapped(v, N), priceCell) || empty("No market is priced in wrapped " + sym + " yet."), see("net:" + v.scid));
  };

  // Home's discovery: every section from a real figure, five rows each, and "See all" opens the Markets view that
  // ranks the same way over every market.
  X.home = () => {
    if (!D.lastRefresh) return D.lastError ? U.errorState("The market registry could not be read from your node: " + U.clean(D.lastError, 160), () => V.app.refresh(true)) : h("div", { class: "list" }, U.skeleton(4));
    const pr = X.promos();
    return h("div", { class: "dz-grid" },
      pr ? sec("promo", "sparkle", "Highlighted", "Promoted: placement bought on the promotion contract. It says nothing about the market.", list("p", pr.slice(0, N), priceCell) || empty("Nothing is promoted right now.")) : null,
      sec("trend", "flame", "Trending", "Hot score: sized trades, distinct posters and volume, last hour", list("t", X.trending(N), priceCell) || empty("Nothing has traded in the last hour."), see("trending")),
      sec("vol", "bolt", "Highest volume", "Reserve flow over 24 hours · DERO markets", list("v", X.volume(N), (s) => [S.fmtDeroShort(s.m.vol24) + " DERO", changePill(s.m.c24h)]) || empty("No DERO market has traded in 24 hours."), see("volume")),
      sec("act", "feed", "Most active", "Blocks with a trade in 24 hours, then posted trades", list("a", X.active(N), (s) => [X.blocks24(s) + " block" + (X.blocks24(s) === 1 ? "" : "s"), h("span", { class: "sub tiny" }, X.posted24(s) + " posted")]) || empty("No market has traded in 24 hours."), see("active")),
      sec("new", "sparkle", "New", "Youngest first, by the block each was installed at", list("n", X.fresh(N), (s) => [S.ageText(s.m.age) + " old", h("span", { class: "sub tiny num" }, "L " + S.fmtDeroShort(s.m.liq) + q(s))]) || empty("No market is listed yet."), see("new")),
      sec("mov", "trend", "Biggest movers", "Largest 24h change, over markets with at least the thin-market floor of liquidity", list("m", X.movers(N), (s) => [h("span", { class: "chg " + (s.m.c24h >= 0 ? "up" : "down") }, A.pct(s.m.c24h)), h("span", { class: "sub tiny num" }, "L " + S.fmtDeroShort(s.m.liq) + q(s))]) || empty("No market above the liquidity floor moved in 24 hours."), see("movers")),
      X.wrapSec("XM", "XMR", "xmr"), X.wrapSec("ZC", "ZEC", "zec"));
  };
})();
