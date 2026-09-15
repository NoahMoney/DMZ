// DMZ search.js — one search over everything the app has read: markets (ticker, pair, name, quote symbol, tags,
// description, contract id), NFAs and holder NFTs. Results are typed, so a token and an NFA with one name are never
// one line. Nothing here asks the node: it filters what feed.js already holds,
// with the per-market text cached until the contract changes, and the input is debounced.
(function () {
  const V = window.Veil, U = V.ui, D = V.data, { h, render, avatar, router } = U;
  const low = (x) => String(x || "").toLowerCase();
  const Q = (V.search = {});
  const hay = new WeakMap();
  const mk = (s) => {
    const k = s.sig + "|" + s.name + "|" + s.ticker + "|" + (s.quoteSym || "");
    let c = hay.get(s);
    if (!c || c.k !== k) { c = { k, t: low(s.ticker), n: low(s.name), id: low(s.scid), all: " " + low([s.ticker, U.pairText(s), s.name, s.quoteSym || "DERO", s.tags, s.descr].join(" ")) }; hay.set(s, c); }
    return c;
  };
  // 0 is no match. The ticker exactly, then its start, a contract id's start (four characters at least, so a
  // single hex letter does not match every id), the name's start, the start of any word, then anywhere.
  const score = (c, q) => (c.t === q ? 100 : c.t.startsWith(q) ? 80 : q.length >= 4 && c.id.startsWith(q) ? 70 : c.n.startsWith(q) ? 60 : c.all.includes(" " + q) ? 40 : c.all.includes(q) ? 20 : 0);
  Q.norm = (v) => low(String(v || "").trim());
  Q.market = (s, q) => !q || score(mk(s), q) > 0;
  Q.markets = (q, n) => D.list().filter((s) => s.m && s.kind !== "nft").map((s) => [score(mk(s), q), s]).filter((x) => x[0] > 0)
    .sort((a, b) => b[0] - a[0] || b[1].m.hot - a[1].m.hot).slice(0, n).map((x) => x[1]);
  // everything that is not a market, typed: NFA, NFT
  Q.others = (q, n = 40) => {
    const out = [];
    if (!q) return out;
    const hit = (id, ...f) => (q.length >= 4 && low(id).startsWith(q)) || low(f.join(" ")).includes(q);
    for (const x of D.nfas.values()) if (hit(x.scid, x.name, x.descr, x.tags, x.collection, x.type)) out.push({ type: "NFA", label: x.name, sub: x.collection || x.type || "", av: { icon: x.icon, ticker: "NFA", scid: x.scid }, go: () => router.go("nfa", x.scid) });
    for (const x of D.nfts.values()) if (hit(x.scid, x.name, x.descr, x.tags, x.tokenTicker)) out.push({ type: "NFT", label: x.name, sub: "holder NFT · " + (x.tokenTicker || ""), av: { icon: x.icon, ticker: "NFT", scid: x.scid }, go: () => router.go("nft", x.scid) });
    return out.slice(0, n);
  };
  // one typed result line, for the header's list and the Markets screen's
  Q.line = (it, attrs) => h(attrs && attrs.role ? "div" : "button", Object.assign({ class: "gsr-i", type: attrs && attrs.role ? null : "button", onclick: it.go }, attrs || {}),
    it.av ? avatar(it.av, "sm") : null, it.type ? h("span", { class: "tlabel" }, it.type) : null,
    h("span", { class: "grow ellipsis" }, h("b", null, it.label), it.sub ? h("span", { class: "sub tiny" }, "  " + it.sub) : null));

  // The header's search: a combobox over a typed list. Arrow keys move, Enter opens (the first result when none is
  // chosen), Escape closes; the last line opens every match in Markets.
  Q.bind = (input) => {
    const box = h("div", { class: "gsr", id: "gsr", role: "listbox", "aria-label": "Search results", hidden: true });
    input.parentNode.append(box);
    // the input's words are index.html's; what it covers is this file's, so this file says it
    input.placeholder = "Search markets, NFAs, NFTs, tags, ids"; input.setAttribute("aria-label", "Search markets, NFAs and holder NFTs");
    for (const [k, v] of [["role", "combobox"], ["aria-controls", "gsr"], ["aria-expanded", "false"], ["aria-autocomplete", "list"]]) input.setAttribute(k, v);
    let t = 0, items = [], at = -1;
    const close = () => { box.hidden = true; input.setAttribute("aria-expanded", "false"); input.removeAttribute("aria-activedescendant"); };
    const pick = (i) => { const it = items[i]; if (it) { close(); it.go(); } };
    const draw = () => {
      const raw = input.value.trim(), q = Q.norm(raw);
      if (!q) return close();
      items = Q.markets(q, 6).map((s) => ({ type: "Market", label: U.pairText(s), sub: s.name, av: s, go: () => router.go("token", s.scid) })).concat(Q.others(q, 6));
      items.push({ type: "", label: "Every match in Markets", sub: "“" + U.clean(raw, 40) + "”", go: () => { router.go("markets"); setTimeout(() => V.markets && V.markets.search(raw), 0); } });
      at = -1;
      // onclick null: the option opens on mousedown, before the input's blur closes the list, and only once
      render(box, items.map((it, i) => Q.line(it, { role: "option", id: "gsr-" + i, "aria-selected": "false", onclick: null, onmousedown: (e) => { e.preventDefault(); pick(i); } })));
      box.hidden = false; input.setAttribute("aria-expanded", "true");
    };
    const move = (d) => { if (box.hidden || !items.length) return; at = (at + d + items.length) % items.length; U.$$(".gsr-i", box).forEach((e, i) => e.setAttribute("aria-selected", String(i === at))); input.setAttribute("aria-activedescendant", "gsr-" + at); };
    input.addEventListener("input", () => { clearTimeout(t); t = setTimeout(draw, 120); });
    input.addEventListener("keydown", (e) => {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); move(e.key === "ArrowDown" ? 1 : -1); }
      else if (e.key === "Enter") { e.preventDefault(); clearTimeout(t); draw(); pick(at >= 0 ? at : 0); }
      else if (e.key === "Escape") close();
    });
    input.addEventListener("blur", () => setTimeout(close, 150));
    input.addEventListener("focus", () => { if (input.value.trim()) draw(); });
  };
})();
