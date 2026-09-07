// DMZ chat.js — rooms, read straight off the chain. There is no server, so this is an IRC client whose server is
// a contract: the room list is local, the messages are contract keys, and the block height is the clock.
//
// Three things the interface must keep saying, because they are true and irreversible:
//   - posting is a ring-size-2 call, so the poster's address is public in that transaction;
//   - a message is permanent — nobody, including whoever deployed DMZ, can edit or remove it;
//   - the fee is the only spam control, because there is no moderator and there is no operator.
// Muting is local to this device and changes nothing about what is stored.
(function () {
  const V = window.Veil, U = V.ui, A = V.amm, D = V.data, B = BigInt;
  const { h, render, button, icon, router, toast } = U;
  const me = () => V.wallet.canon;
  const isNum = (x) => typeof x === "string" && /^\d+$/.test(x);

  const GLOBAL = [
    ["#dmz", "everything"],
    ["#dero", "the home chain"],
    ["#xmr", "monero"],
    ["#zec", "zcash"],
    ["#markets", "trading"],
  ];
  // a market's room is its contract id, shortened: tickers collide, contract ids do not
  const roomOf = (scid) => "#" + String(scid).slice(0, 12);
  const MUTE = "dmz.chat.mute";
  const muted = () => { try { return new Set(JSON.parse(localStorage.getItem(MUTE) || "[]")); } catch (e) { return new Set(); } };
  const mute = (addr) => { const s = muted(); s.add(addr); try { localStorage.setItem(MUTE, JSON.stringify([...s])); } catch (e) {} };
  const unmuteAll = () => { try { localStorage.removeItem(MUTE); } catch (e) {} };

  // ---- data ----

  Object.assign(D, {
    chat: null,
    async loadChat() {
      const scid = this.cfg.chat;
      if (!scid || !/^[0-9a-f]{64}$/.test(scid)) return null;
      const r = await V.node.getSC(scid, { keys: ["kind", "v", "fee", "maxlen", "n"] });
      if (V.hexToStr(r.vals.kind || "") !== "chat") { console.warn("configured chat contract is not a chat"); return null; }
      const c = { scid, v: isNum(r.vals.v) ? B(r.vals.v) : 0n };
      if (c.v !== 1n) { console.warn("chat version " + c.v + " is not the version this app reads"); return null; }
      c.fee = isNum(r.vals.fee) ? B(r.vals.fee) : 0n;
      c.maxlen = isNum(r.vals.maxlen) ? Number(r.vals.maxlen) : 280;
      c.total = isNum(r.vals.n) ? Number(r.vals.n) : 0;
      this.chat = c;
      return c;
    },
    // The last `want` messages of one room: the count, then that many records by exact key. Nothing scans, and a
    // busy room never makes a quiet one slower to open.
    async roomMessages(room, want) {
      const c = this.chat;
      if (!c) return { n: 0, msgs: [] };
      const got = await this.fetchKeys(c.scid, ["rn_" + room]);
      const n = isNum(got["rn_" + room]) ? Number(got["rn_" + room]) : 0;
      if (!n) return { n: 0, msgs: [] };
      const from = Math.max(0, n - want), keys = [];
      for (let j = from; j < n; j++) keys.push("rm_" + room + "_" + j);
      const recs = await this.fetchKeys(c.scid, keys);
      const msgs = [];
      for (let j = from; j < n; j++) {
        const raw = V.hexToStr(recs["rm_" + room + "_" + j] || "");
        if (!raw) continue;
        // author|height|text — split on the first two separators only: the text may contain them
        const i1 = raw.indexOf("|"), i2 = i1 < 0 ? -1 : raw.indexOf("|", i1 + 1);
        if (i2 < 0) continue;
        const addr = raw.slice(0, i1), height = raw.slice(i1 + 1, i2), text = raw.slice(i2 + 1);
        if (!V.canonAddress(addr)) continue;
        msgs.push({ j, addr: V.canonAddress(addr), height: isNum(height) ? Number(height) : 0, text: U.clean(text, 400) });
      }
      return { n, msgs };
    },
  });

  // ---- screen ----

  const SHOW = 120;
  let room = "#dmz", sending = [], lastKey = "";

  router.on("chat", async (el, param) => {
    if (param) room = "#" + String(param).replace(/^#/, "").slice(0, 32);

    const nick = (addr) => {
      const p = D.profile(addr);
      return U.clean((p && p.name) || V.shortAddr(addr), 20);
    };

    const rooms = () => {
      const list = GLOBAL.slice();
      const seen = new Set(list.map((r) => r[0]));
      for (const s of D.trending ? D.trending(8) : []) {
        const r = roomOf(s.scid);
        if (!seen.has(r)) { seen.add(r); list.push([r, U.clean(s.ticker, 12)]); }
      }
      if (!seen.has(room)) list.push([room, ""]);
      return list;
    };

    const roomList = () =>
      h("div", { class: "rooms" },
        h("div", { class: "eyebrow rooms-h" }, "Rooms"),
        rooms().map(([r, topic]) =>
          h("button", {
            class: "roomrow" + (r === room ? " on" : ""), type: "button",
            onclick: () => { room = r; sending = []; lastKey = ""; router.go("chat", r.slice(1)); },
          }, h("span", { class: "mono grow ellipsis" }, r), topic ? h("span", { class: "tiny dim2 ellipsis" }, topic) : null)));

    const line = (m) => {
      const mine = m.addr === me();
      const act = m.text.startsWith("/me ");
      return h("div", { class: "msg" + (mine ? " mine" : "") },
        h("span", { class: "mono when" }, String(m.height)),
        h("button", {
          class: "mono who ellipsis", type: "button", title: m.addr,
          onclick: () => router.go("profile", m.addr),
        }, nick(m.addr)),
        h("span", { class: "what" + (act ? " act" : "") }, act ? "* " + nick(m.addr) + " " + m.text.slice(4) : m.text));
    };

    const pending = (t) => h("div", { class: "msg sending" },
      h("span", { class: "mono when" }, "····"),
      h("span", { class: "mono who ellipsis" }, nick(me() || "")),
      h("span", { class: "what" }, t));

    const draw = (c, data, err) => {
      if (err) return render(el, h("h1", { class: "mt mb" }, "Chat"), h("div", { class: "errbox" }, err));
      if (!c) return render(el, h("h1", { class: "mt mb" }, "Chat"),
        U.emptyState("feed", "No chat on this network",
          "This build of DMZ was published without a chat contract, so there is no room to open. Nothing to read and nothing to sign."));

      const mutes = muted();
      const shown = (data.msgs || []).filter((m) => !mutes.has(m.addr));
      const hidden = (data.msgs || []).length - shown.length;

      const input = h("input", {
        class: "compose", maxlength: String(c.maxlen), autocomplete: "off", spellcheck: "true",
        placeholder: V.wallet.connected ? "Message " + room : "Connect a wallet to post",
        disabled: V.wallet.connected ? null : true,
      });
      const send = async () => {
        const text = U.clean(String(input.value || "").trim(), c.maxlen);
        if (!text) return;
        input.value = "";
        sending.push(text);
        drawNow();
        const ok = await V.app.tx({
          scid: c.scid, entry: "Post",
          args: [["room", "S", room], ["text", "S", text]],
          dero: c.fee, describe: "Post to " + room,
          probe: { scid: c.scid, keys: ["rn_" + room] },
        });
        sending = sending.filter((t) => t !== text);
        if (ok) { lastKey = ""; reload(); } else { input.value = text; drawNow(); }
      };
      input.onkeydown = (e) => { if (e.key === "Enter") { e.preventDefault(); send(); } };

      render(el,
        h("div", { class: "chat-grid" },
          roomList(),
          h("div", { class: "chan" },
            // the status line stands in for an IRC server banner, and says what is true of this one
            h("div", { class: "chan-head" },
              h("b", { class: "mono" }, room),
              h("span", { class: "note grow ellipsis" },
                data.n + " message" + (data.n === 1 ? "" : "s") + " · " +
                (c.fee > 0n ? A.fmtDero(c.fee, 5) + " DERO to post" : "free to post") +
                " · no presence: there is no server to know who is here"),
              hidden ? button(hidden + " muted", "small ghost", () => { unmuteAll(); reload(); }) : null),
            h("div", { class: "log" },
              shown.length ? shown.map(line) : h("div", { class: "empty" },
                h("b", null, room),
                "Nothing has been said here yet. A message costs " +
                (c.fee > 0n ? A.fmtDero(c.fee, 5) + " DERO" : "only the network fee") +
                " and is written to the chain, where it stays."),
              sending.map(pending)),
            h("div", { class: "composer" },
              h("span", { class: "mono dim2 prompt" }, ">"),
              input,
              button("Send", "small primary", send, !V.wallet.connected)),
            h("div", { class: "note chan-foot" },
              "Posting is a ring-size-2 call, so this address is public in that transaction. A message is " +
              "permanent: nobody, including whoever deployed DMZ, can edit or remove it. Muting hides a sender " +
              "on this device only."))));
    };

    let cur = { n: 0, msgs: [] }, curChat = null;
    const drawNow = () => draw(curChat, cur);
    const reload = async () => {
      try {
        const c = curChat || (await D.loadChat());
        curChat = c;
        if (!c) return draw(null, cur);
        const data = await D.roomMessages(room, SHOW);
        const key = room + ":" + data.n;
        cur = data;
        if (key !== lastKey) { lastKey = key; draw(c, data); }
      } catch (e) {
        console.error(e);
        draw(curChat, cur, "The chat contract could not be read from your node: " + U.clean(String((e && e.message) || e), 160));
      }
    };

    render(el, h("h1", { class: "mt" }, "Chat"), h("div", { class: "list mt" }, U.skeleton(4)));
    reload();
    // a new block is the only thing that can add a message, so that is when to look
    D.onChange(() => { if (router.current && router.current.name === "chat") reload(); });
  });
})();
