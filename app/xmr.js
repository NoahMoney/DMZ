// DMZ xmr.js — the DERO ↔ XMR swap screen (contracts/htlc.bas v3, docs/XMR_SWAP.md). It reads the lock contract this
// build names only once its code matches the shipped template (app/templates/htlc.bas, the bytes tools/xmrswap pins),
// lists the locks where the connected wallet is the maker or the taker, finds any lock by its id, and shows each
// lock's state, its three heights and the exit a transaction sent now would meet. Its one action is Punish, which
// needs no secret and pays only the taker. Claim, Refund and Reclaim need the swap's secrets, which live only in the
// user's own xmrswap: this page never asks for, accepts or stores a secret or a key.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, T = V.track, N = Number;
  const { h, render, button, kv, router, openSheet, closeSheet, toast } = U;
  const BLOCK = 18, SCAN = 200, FEE = 2000, me = () => V.wallet.canon;
  const hex64 = (x) => /^[0-9a-f]{64}$/.test(x || "");
  const int = (x) => (typeof x === "string" && /^\d{1,15}$/.test(x) ? N(x) : null);
  const who = (x) => (x ? D.rawToAddr(x) : null);
  const tip = () => (V.live && V.live.height) || D.height || 0;
  const ST = ["open", "claimed", "refunded", "punished", "reclaimed"], ORDER = ["claim", "refund", "punish", "reclaim"];
  const RING2 = "Ring size 2: the contract reads who signed, so your DERO address is public in this call.";

  // Which exit a transaction meets at height h, by the contract's own comparisons (htlc.bas Claim line 24, Refund 25,
  // Punish 25, Reclaim 24): Claim below t1, Refund in [t1, t2), Punish in [t2, t2 + g2), Reclaim from t2 + g2. A
  // transaction sent now runs in the next block at the earliest, so the screen asks at tip + 1. Anything unread is
  // "unreadable", never a window.
  function exit(st, t1, t2, g2, h) {
    if (st === null || st === undefined) return { k: "unreadable" };
    if (st !== 0) return { k: "closed" };
    if (![t1, t2, g2, h].every((x) => Number.isSafeInteger(x) && x > 0) || t2 <= t1) return { k: "unreadable" };
    if (h < t1) return { k: "claim", until: t1 };
    if (h < t2) return { k: "refund", from: t1, until: t2 };
    if (h < t2 + g2) return { k: "punish", from: t2, until: t2 + g2 };
    return { k: "reclaim", from: t2 + g2 };
  }
  const EXIT = { claim: ["Claim", "the taker alone, with her key share s_a and the maker's gate secret; it pays her"],
    refund: ["Refund", "the maker alone, with his key share s_b; it pays him back"],
    punish: ["Punish", "anyone, with no secret; it pays only the taker"],
    reclaim: ["Reclaim", "the maker alone, with s_b; it pays him back"] };
  const canPunish = (l, g2, H) => l.st === 0 && H > 0 && exit(l.st, l.t1, l.t2, g2, H + 1).k === "punish";
  // The DERO-side stages of one lock, for the tracker (app/track.js). The Monero side is marked unseen: only the
  // user's own xmrswap can see it. dep is the lock's depth, or null when it could not be read.
  function stages(l, g2, H, dep) {
    const s = l.st, e = exit(s, l.t1, l.t2, g2, H + 1), open = s === 0;
    const w = (k, n) => (s === n ? "done" : s === null || (open && e.k === "unreadable") ? "unreadable" : !open ? "skipped" : e.k === k ? "active" : ORDER.indexOf(e.k) < ORDER.indexOf(k) ? "waiting" : "skipped");
    const dl = (x) => (Number.isSafeInteger(x) ? { chain: "DERO", height: x, eta: null } : null);
    const deep = open && dep !== null && dep !== undefined;
    return [
      { label: "The maker locks " + (l.asset === "" ? "DERO" : "the asset") + " for the taker" + (open && !deep ? " (its depth could not be read)" : ""), state: deep && dep < T.STABLE ? "active" : "done", conf: deep ? dep : null, need: deep ? T.STABLE : null },
      { label: "The taker locks XMR at the joint address; once it has confirmed, the maker hands her his gate secret", chain: "Monero", state: "unseen" },
      { label: "Claim: the taker takes it, before t1", state: w("claim", 1), deadline: dl(l.t1) },
      { label: "Refund: the maker takes it back, from t1 until t2", state: w("refund", 2), deadline: dl(l.t2) },
      { label: "Punish: anyone pays it to the taker, from t2 until t2 + g2", state: w("punish", 3), deadline: dl(l.t2 + g2) },
      { label: "Reclaim: the maker takes it back, from t2 + g2", state: w("reclaim", 4) },
      { label: s === 1 ? "The maker sweeps the XMR with the s_a the Claim made public" : s === 2 || s === 4 ? "The taker recovers her XMR with the s_b the maker made public" : s === 3 ? "The taker's XMR stays at the joint address unless the maker ever makes s_b public" : "The XMR settles with the secret the DERO exit makes public", chain: "Monero", state: "unseen" },
    ].map((x) => Object.assign({ chain: "DERO", conf: null, need: null, deadline: null, txid: "" }, x));
  }

  // ---- data ----
  const X = (V.xmr = { state: "", at: 0, h: 0, fail: "", verified: "", codeHash: "", w1min: null, w2min: null, g2: null, n: null, mine: [], scanned: 0, one: null, exit, stages, canPunish });
  // the code check: the node's copy of the contract against the template this bundle ships, byte for byte by hash
  async function check(scid) {
    if (X.verified === scid) return true;
    const [r, t] = await Promise.all([V.node.getSC(scid, { code: true }), fetch("templates/htlc.bas").then((x) => (x.ok ? x.text() : ""), () => "")]);
    if (!t) throw new Error("the shipped template, templates/htlc.bas, could not be read, so the contract cannot be checked");
    const [a, b] = await Promise.all([V.sha256Hex(r.code || ""), V.sha256Hex(t)]);
    X.codeHash = r.code ? a : "";
    if (!r.code || a !== b) return false;
    X.verified = scid; return true;
  }
  const LK = ["m_", "t_", "a_", "v_", "t1_", "t2_", "st_"];
  const lockOf = (id, g) => { const a = g["a_" + id], st = int(g["st_" + id]);
    return { id, maker: who(g["m_" + id]), taker: who(g["t_" + id]), asset: a === "" || hex64(a) ? a : null, v: /^\d{1,20}$/.test(g["v_" + id] || "") ? BigInt(g["v_" + id]) : null,
      t1: int(g["t1_" + id]), t2: int(g["t2_" + id]), st: st !== null && st <= 4 ? st : null }; };
  X.load = async (id) => {
    const scid = D.cfg.htlc;
    if (!hex64(scid)) return (X.state = "none");
    if (!(await check(scid))) return (X.state = "wrong");
    const r = await V.node.getSC(scid, { keys: ["kind", "v", "n", "w1min", "w2min", "g2"] });
    if (V.hexToStr(r.vals.kind || "") !== "htlc" || r.vals.v !== "3") return (X.state = "wrong");
    X.w1min = int(r.vals.w1min); X.w2min = int(r.vals.w2min); X.g2 = int(r.vals.g2); X.n = int(r.vals.n); X.h = tip();
    const mine = [], w = me();
    // the wallet's own locks among the newest SCAN: two keys a lock to find them, then the rest of theirs
    if (X.n !== null && w) {
      const from = Math.max(0, X.n - SCAN), ks = []; for (let i = from; i < X.n; i++) ks.push("m_" + i, "t_" + i);
      const g = ks.length ? await D.fetchKeys(scid, ks) : {}, ids = [];
      for (let i = X.n - 1; i >= from; i--) if (who(g["m_" + i]) === w || who(g["t_" + i]) === w) ids.push(i);
      const f = ids.length ? await D.fetchKeys(scid, ids.flatMap((i) => LK.map((k) => k + i))) : {};
      for (const i of ids) mine.push(lockOf(i, f));
      X.scanned = X.n - from;
    }
    X.mine = mine; X.one = null;
    if (id !== undefined) {
      const f = await D.fetchKeys(scid, LK.map((k) => k + id));
      if (f["st_" + id] !== undefined) { X.one = lockOf(id, f); if (X.one.st === 0) X.one.depth = await T.depth(scid, "st_" + id, X.h); }
    }
    X.at = Date.now(); X.state = "ok"; X.fail = "";
  };

  // ---- words ----
  const assetOf = (a) => { if (a === null) return null; if (!a) return { sym: "DERO", dec: 5n }; const m = D.list().find((x) => x.asset === a); return m ? { sym: m.ticker, dec: BigInt(m.dec) } : { sym: "asset " + a.slice(0, 8), dec: null }; };
  const amt = (a, v) => (a === null || v === null ? "an unreadable amount" : a.dec === null ? v + " units of " + a.sym + " (scale unknown)" : A.fmtUnits(v, a.dec, 6) + " " + a.sym);
  const xmrAmt = (v) => A.fmtUnits(v, 12n, 6) + " XMR";
  const when = (b) => { if (!Number.isSafeInteger(b)) return "unreadable"; const t = tip(), d = b - t; return "block " + b + (!t ? "" : d <= 0 ? " · passed" : " · about " + new Date(Date.now() + d * BLOCK * 1000).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })); };
  const blocks = (n) => (n === null ? "unreadable" : n.toLocaleString() + (n === 1 ? " block" : " blocks") + " (about " + (n * BLOCK < 5400 ? Math.round((n * BLOCK) / 60) + " min" : Math.round((n * BLOCK) / 360) / 10 + " h") + ")");
  const dir = (l) => (!me() ? null : l.maker === me() ? "withdrawal" : l.taker === me() ? "deposit" : null);
  const dirLine = (l) => { const d = dir(l); return d ? T.dirText(d, "XMR", amt(assetOf(l.asset), l.v)) : amt(assetOf(l.asset), l.v) + " locked for XMR · the maker's withdrawal, the taker's deposit"; };
  const stWord = (l) => (l.st === null ? "unreadable" : ST[l.st]);
  const flag = (l) => h("span", { class: "pill " + (l.st === 0 ? "up" : l.st === null ? "bad" : "") }, stWord(l));
  const para = (l, t) => h("div", { class: "mt8" }, h("b", null, l + ": "), h("span", { class: "sub" }, t));
  const cmd = (s) => h("pre", { class: "mono", style: "white-space:pre-wrap;word-break:break-all;font-size:12px;margin:6px 0 0;padding:10px;border:1px solid var(--edge)" }, s);
  const STATUS = "xmrswap status -dir <the directory with your swap's state file> -daemon <your DERO node>";
  const secrets = () => h("div", { class: "note mt8" }, "Claim, Refund and Reclaim need the swap's secrets. They live only in your own xmrswap, on your machine, which sends each exit at its height: " +
    "this page never asks for a secret or a key, and would not take one. To see where your swap stands, ask your xmrswap:", cmd(STATUS));
  function commands() {
    const id = hex64(D.cfg.htlc) ? D.cfg.htlc : "<none: this build names no htlc.bas>";
    const common = (D.cfg.network === "mainnet" ? "-mainnet -net mainnet" : "-net stagenet") + " -daemon <your DERO node> -dero <your DERO wallet rpc> -monero <your monero-wallet-rpc> -htlc " + id +
      " -prover <absolute path to proof-xmr> -channel <a directory both sides can reach> -amount-dero <atomic units> -amount-xmr <piconero>";
    return [h("h3", { class: "mt" }, "Your xmrswap, with this network's contract filled in"),
      h("div", { class: "sub mt8" }, "The maker, who has DERO and wants XMR, makes the swap's channel key and hands it to the taker out of band, with the trade's terms:"),
      cmd("xmrswap channel-key\nXMRSWAP_CHANNEL_KEY=<key> xmrswap bob " + common),
      h("div", { class: "sub mt8" }, "The taker, who has XMR and wants DERO:"), cmd("XMRSWAP_CHANNEL_KEY=<key> xmrswap alice " + common),
      h("div", { class: "sub mt8" }, "Either side, at any time; it reads and never writes:"), cmd(STATUS),
      h("div", { class: "note mt8" }, "Both wallets are started with a login, given through VEIL_RPC_LOGIN and XMRSWAP_MONERO_LOGIN." + (D.cfg.network === "mainnet" ? " On mainnet it also needs -monerod and -monero-wallet-dir." : "") + " xmrswap bob -h lists every flag.")];
  }
  function how() {
    const g2 = X.g2;
    return [h("h3", { class: "mt" }, "How it works"),
      h("div", { class: "sub mt8" }, "The maker locks DERO, or one DERO token, in htlc.bas for a named taker under three hashes. The taker locks XMR at a joint Monero address whose two key shares a proof ties to those hashes. " +
        "The lock has four exits, each at its own heights, so no two are open at once:"),
      para("Claim", "the taker, before t1, with her key share s_a and the maker's gate secret, which he hands her only once her XMR has confirmed. She takes the DERO; s_a is then public, and the maker sweeps the XMR with it."),
      para("Refund", "the maker, from t1 until t2, with his key share s_b. He takes his DERO back; s_b is then public, and the taker recovers her XMR with it."),
      para("Punish", "anyone, from t2 until t2 + g2, with no secret. It pays the maker's DERO to the taker, never to the sender. Her XMR stays at the joint address unless he ever makes s_b public."),
      para("Reclaim", "the maker, from t2 + g2 on, with s_b. A lock nobody punished goes back to him, and s_b lets the taker recover her XMR at any later time."),
      h("div", { class: "note mt8" }, "The windows, read from this contract: t1 is at least w1min = " + blocks(X.w1min) + " after the lock, t2 at least w2min = " + blocks(X.w2min) + " after t1, and the Punish window lasts g2 = " + blocks(g2) +
        ". They were fixed when the contract was installed, and no function changes them. Before t2 + g2, time alone opens an exit someone may send; after it, only the maker's own Reclaim is left."),
      para("The fee", "0.5% of the XMR. The maker pays it in XMR, out of the XMR he receives, and only on a completed swap: a refunded, punished or reclaimed swap pays none. It is an output of his own Monero transaction; no contract here takes it."),
      para("What each side trusts", "its own DERO and Monero nodes, its own xmrswap, and being online, or having a machine online, at its window. Miss your window and the other side's exit opens. A node that lies to you and works with your counterparty can cost you your side of the swap, so run your own. No one holds a coin for anyone: there is no custodian."),
      para("Privacy", "every call to the lock is ring size 2, because the contract reads who signed. The maker's and the taker's DERO addresses are public, and so are the amount and the heights."),
      para("The proof", "the cross-curve proof that ties each Monero key share to its hash on DERO is unaudited. It is built on a Bulletproofs R1CS library its own authors mark unaudited, and no outside review has checked it."),
      para("Finding a counterparty", "this app cannot. The two sides agree the trade elsewhere, and the maker hands the taker the swap's channel key out of band; the two xmrswap programs then talk only through a directory both can reach, sealed under that key."),
      para("Tested", "every exit has run against real Monero node software on a local test chain (regtest). The Monero swap has not yet run on a public network."),
      commands()];
  }
  const want = () => ({ program: "xmrswap", network: D.cfg.network, contracts: [D.cfg.htlc] });
  const fmt = { foreign: xmrAmt, dero: (a, v) => amt(assetOf(a), v) };
  const readLine = () => h("div", { class: "note mt" }, (X.fail ? "The last read failed (" + X.fail + "). " : "") + "Read from your node at block " + X.h + ", ",
    h("span", { "data-ts": Math.floor(X.at / 1000) }, U.timeAgo(Math.floor(X.at / 1000)) + " ago"),
    ". Lock contract " + V.shortAddr(D.cfg.htlc) + ": its code matches the template this app ships (sha256 " + X.codeHash.slice(0, 12) + "…); it holds " + (X.n === null ? "an unreadable number of" : X.n) +
    " lock" + (X.n === 1 ? "" : "s") + ". t1, t2 and t2 + g2 are DERO heights; a time beside one is an estimate at 18 s a block.");

  // ---- Punish: the one write, confirmed first ----
  const send = async (l) => { const scid = D.cfg.htlc;
    const ok = await V.app.tx({ scid, entry: "Punish", args: [["id", "U", BigInt(l.id)]], describe: "Punish lock #" + l.id, fee: FEE, probe: { scid, keys: ["st_" + l.id], check: (p0, p1) => p1.vals["st_" + l.id] === "3" } });
    if (ok) { await X.load(l.id).catch(() => {}); D.changed(); } };
  function punishSheet(l) {
    const end = l.t2 + X.g2;
    openSheet(h("h2", null, "Punish lock #" + l.id),
      h("div", { class: "stack mt" }, kv("Pays", amt(assetOf(l.asset), l.v)), kv("To the taker, only", h("span", { class: "mono" }, V.shortAddr(l.taker))), kv("You pay", "the gas: a DERO network fee of " + A.fmtUnits(BigInt(FEE), 5n, 5) + " DERO"), kv("Window ends", when(end))),
      h("div", { class: "note mt" }, "Punish pays the whole lock to the taker named in it, and to nobody else: not to you, whoever you are. You, the sender, pay its gas. It needs no secret. " +
        "The contract accepts it from t2 until t2 + g2; mined at block " + end + " or later it is refused, and only the fee is spent. " + RING2),
      button("Send Punish", "primary wide mt", () => { closeSheet(); send(l); }));
  }

  // ---- screens ----
  const row = (l) => { const e = exit(l.st, l.t1, l.t2, X.g2, tip() + 1);
    return h("button", { class: "feed-item", type: "button", onclick: () => router.go("xmr", String(l.id)) },
      h("div", { class: "body" }, h("div", { class: "between", style: "gap:10px" }, h("b", null, dirLine(l)), flag(l)),
        h("div", { class: "meta" }, "#" + l.id + (EXIT[e.k] ? " · open now: " + EXIT[e.k][0] + " · " + T.left({ chain: "DERO", height: e.until || e.from, eta: null }, tip(), Date.now()) : "")))); };
  function list(el, redraw) {
    const look = h("input", { inputmode: "numeric", placeholder: "lock id", autocomplete: "off", "aria-label": "Lock id", style: "flex:1;min-width:0" });
    const go = () => { const v = look.value.trim(); if (!/^\d{1,9}$/.test(v)) return toast("A lock id is a whole number" + (X.n ? ", from 0 to " + (X.n - 1) : ""), "err"); router.go("xmr", v); };
    render(el, h("h1", { class: "mt" }, "XMR swap"),
      h("div", { class: "sub mt8" }, "Swap XMR for DERO or a DERO token, and back. The DERO leg is a lock in htlc.bas, read here; the Monero leg is a joint address your own xmrswap builds, funds and watches. " +
        "No one holds a coin for anyone: each side keeps an exit of its own for as long as it acts inside its window."),
      T.panel("xmrswap", want(), open, redraw, fmt, tip()),
      U.sectionHead("bolt", "Your locks", "Where your wallet is the maker (a withdrawal) or the taker (a deposit)"),
      !me() ? U.emptyState("wallet", null, "Connect a wallet to list the locks where it is the maker or the taker, or find any lock by its id below.")
        : X.mine.length ? h("div", { class: "list mt8" }, X.mine.map(row)) : U.emptyState("bolt", null, "None of the newest " + X.scanned + " locks names your wallet."),
      h("div", { class: "row mt" }, look, button("Find lock", "small", go)),
      how(), readLine());
  }
  function page(el, id, redraw) {
    const l = X.one;
    if (!l) return render(el, h("h1", { class: "mt mb" }, "XMR lock #" + id), h("div", { class: "errbox" }, "This contract has no lock with that number" + (X.n !== null ? ": it holds " + X.n + "." : ".")), readLine());
    const H = tip(), e = exit(l.st, l.t1, l.t2, X.g2, H + 1), a = assetOf(l.asset), end = Number.isSafeInteger(l.t2) && X.g2 !== null ? l.t2 + X.g2 : null;
    const p = T.paired("xmrswap"), mine = !!p && p.lock === l.id;
    const you = (x) => (x && x === me() ? " · you" : "");
    render(el,
      h("div", { class: "between mt" }, h("h1", null, "XMR lock #" + l.id), flag(l)), h("div", { class: "sub mt8" }, dirLine(l)),
      h("h3", { class: "mt" }, "The lock · htlc.bas"),
      kv("Amount", amt(a, l.v)), kv("State", stWord(l)),
      kv("Maker · has DERO", h("span", { class: "mono" }, (l.maker ? V.shortAddr(l.maker) : "unreadable") + you(l.maker))), kv("Taker · paid by Claim and Punish", h("span", { class: "mono" }, (l.taker ? V.shortAddr(l.taker) : "unreadable") + you(l.taker))),
      kv("t1 · Claim ends, Refund opens", when(l.t1)), kv("t2 · Refund ends, Punish opens", when(l.t2)), kv("t2 + g2 · Punish ends, Reclaim opens", end === null ? "unreadable" : when(end)),
      h("h3", { class: "mt" }, "Open now"),
      kv("A transaction sent now meets", EXIT[e.k] ? EXIT[e.k][0] + " (next block, " + (H + 1) + ")" : e.k === "closed" ? "no exit: the lock is " + stWord(l) : "unreadable: a height or the state could not be read"),
      kv("Who may send it", EXIT[e.k] ? EXIT[e.k][1] : "nobody"),
      canPunish(l, X.g2, H) && X.verified === D.cfg.htlc ? h("div", { class: "stack mt" }, button("Punish: pay the taker", "primary wide", () => punishSheet(l)),
        h("div", { class: "sub tiny" }, "Anyone may send it now, until block " + (end - 1) + ". It pays only the taker; you pay the gas.")) : null,
      l.st === 0 ? secrets() : null,
      h("h3", { class: "mt" }, "Stages"),
      mine ? T.panel("xmrswap", want(), open, redraw, fmt, H)
        : [T.render(stages(l, X.g2, H, l.depth), H), h("div", { class: "note mt8" }, "The Monero side is visible only to your swap program. Pair with it above the list of your locks to see every stage here.")],
      readLine());
  }
  const open = () => !!router.current && router.current.name === "xmr";

  router.on("xmr", async (el, param) => {
    const id = /^\d{1,9}$/.test(param || "") ? N(param) : undefined;
    const here = () => open() && router.current.param === param;
    const title = () => h("h1", { class: "mt mb" }, id === undefined ? "XMR swap" : "XMR lock #" + id);
    const busy = () => [...el.querySelectorAll("input")].some((i) => i.value && i.value.trim());
    const draw = () => {
      if (X.state === "none") return render(el, title(), U.emptyState("trend", "No Monero swap contract on this network", "This build names no htlc.bas, so there is no lock to read and nothing to sign."), how());
      if (X.state === "wrong") return render(el, title(), h("div", { class: "errbox" }, "The contract this build names as the Monero swap lock, " + V.shortAddr(D.cfg.htlc) + ", is not the htlc.bas v3 this app ships" +
        (X.codeHash ? ": its code hashes to " + X.codeHash.slice(0, 16) + "…" : ": your node returned no code for it") + ". Nothing from it is shown and nothing can be signed."));
      if (X.state === "err") return render(el, title(), U.errorState("The Monero swap contract could not be read from your node: " + X.fail, () => reload()));
      return id === undefined ? list(el, redraw) : page(el, id, redraw);
    };
    const redraw = () => { if (here() && !busy()) draw(); };
    const reload = async () => {
      try { await X.load(id); } catch (e) { X.fail = U.clean(String((e && e.message) || e), 160); if (X.state !== "ok") X.state = "err"; }
      if (here()) draw();
    };
    render(el, title(), h("div", { class: "list" }, U.skeleton(3)));
    await reload();
    T.watch("xmrswap", want(), here, redraw);
    // one read per block while this screen is open, and none while a field on it holds text
    D.onChange(() => { if (here() && !busy() && Date.now() - X.at > 4000) reload(); });
  });
  // a build that names no lock contract has no Monero swap tab; the route still explains the swap
  if (!hex64(D.cfg.htlc)) { const t = document.querySelector('.tab[data-tab="xmr"]'); if (t) t.remove(); }
})();
