// DMZ xswd.js — wallet connection over XSWD (ws://localhost:44326/xswd). Facts from DECISIONS §5:
// first message is ApplicationData, reply {accepted}, one wallet request in flight at a time (the wallet prompts the
// user), 10 req/s burst 20 or the wallet drops us, DERO.* is proxied without prompts, events via Subscribe.
(function () {
  const V = window.Veil;
  const PORTS = [44326];
  const KEY = "veil.xswd.id";

  function appId() {
    // one id per browser session: the wallet's permissions are per connection anyway, and a persistent id would
    // let any later page on this origin present itself to the wallet as this app
    let id = null; try { id = sessionStorage.getItem(KEY); } catch (e) {}
    if (!id || !/^[0-9a-f]{64}$/.test(id)) { const b = new Uint8Array(32); crypto.getRandomValues(b); id = V.bytesToHex(b); try { sessionStorage.setItem(KEY, id); localStorage.removeItem(KEY); } catch (e) {} }
    return id;
  }

  const wallet = {
    ws: null, connected: false, address: null, canon: null, daemon: null, id: 0, pending: new Map(), queue: [], busy: false, unanswered: new Set(),
    listeners: {}, lastSend: 0, status: "disconnected", statusCb: null, connectPromise: null,
    on(ev, fn) { (this.listeners[ev] = this.listeners[ev] || []).push(fn); },
    emit(ev, data) { (this.listeners[ev] || []).forEach((f) => { try { f(data); } catch (e) { console.error(e); } }); },
    setStatus(s) { this.status = s; this.emit("status", s); },

    connect() {
      if (this.connected) return Promise.resolve(this);
      if (this.connectPromise) return this.connectPromise;
      this.connectPromise = this._connect().finally(() => { this.connectPromise = null; });
      return this.connectPromise;
    },
    async _connect() {
      this.setStatus("connecting");
      let lastErr = null;
      for (const port of PORTS) {
        try { await this._open("ws://localhost:" + port + "/xswd"); return this; } catch (e) { lastErr = e; }
      }
      this.setStatus("disconnected");
      throw lastErr || new Error("no wallet");
    },
    _open(url) {
      return new Promise((resolve, reject) => {
        let ws; try { ws = new WebSocket(url); } catch (e) { return reject(e); }
        let phase = "open";
        const fail = (msg) => { try { ws.close(); } catch (e) {} reject(new Error(msg)); };
        const t = setTimeout(() => fail("wallet did not answer (is XSWD enabled?)"), 120000); // the user may be reading the prompt
        ws.onerror = () => { if (phase === "open") { clearTimeout(t); reject(new Error("no wallet listening on " + url)); } };
        ws.onopen = () => {
          phase = "handshake"; this.setStatus("approve");
          ws.send(JSON.stringify({ id: appId(), name: "DMZ", description: "DMZ: social DEX and launchpad for DERO assets (swap, launch, follow)", url: location.protocol + "//" + location.host }));
        };
        ws.onclose = () => {
          clearTimeout(t);
          const was = this.connected; this.connected = false; this.ws = null; this.address = null; this.canon = null;
          for (const p of this.pending.values()) p.reject(new Error("wallet disconnected")); this.pending.clear(); this.unanswered.clear();
          this.queue.splice(0).concat(this.lowQueue.splice(0)).forEach((q) => q.reject(new Error("wallet disconnected"))); this.busy = false;
          this.setStatus("disconnected"); if (was) this.emit("disconnect");
          if (phase !== "ready") reject(new Error("wallet closed the connection"));
        };
        ws.onmessage = async (ev) => {
          let m; try { m = JSON.parse(ev.data); } catch (e) { return; }
          if (phase === "handshake") {
            clearTimeout(t);
            if (!m.accepted) return fail(m.message || "connection rejected");
            phase = "ready"; this.ws = ws; this.connected = true; this.setStatus("connected");
            try {
              const a = await this.call("GetAddress"); this.canon = V.canonAddress(a && a.address); if (!this.canon) { this.connected = false; this.ws = null; return fail("wallet returned an invalid address"); } this.address = a.address;
              try { const d = await this.call("GetDaemon"); this.daemon = d.endpoint; } catch (e) { this.daemon = null; }
            } catch (e) { return fail("address permission denied"); }
            this.emit("connect", this); resolve(this); return;
          }
          // the wallet echoes ids as the raw JSON text (a string id comes back with its quotes): normalise
          const rid = m.id === undefined || m.id === null ? undefined : String(m.id).replace(/^"+|"+$/g, "");
          if (rid !== undefined && this.pending.has(rid)) {
            const p = this.pending.get(rid); this.pending.delete(rid);
            if (p.late) { this.unanswered.delete(rid); this.emit("late", m.error ? { error: m.error.message || "wallet error" } : m.result || {}); return; } // a transfer answered after its caller gave up
            m.error ? p.reject(Object.assign(new Error(m.error.message || "wallet error"), { code: m.error.code })) : p.resolve(m.result);
            return;
          }
          if (m.event || (m.result && m.result.event)) this.emit("event", m.result || m);
        };
      });
    },
    disconnect() { if (this.ws) this.ws.close(); },

    // Serialised, rate-limited request queue: the wallet shows one permission prompt at a time.
    // Background reads (balances) go to the low queue so a user action never waits behind them.
    lowQueue: [],
    call(method, params, opts) {
      return new Promise((resolve, reject) => { (opts && opts.low ? this.lowQueue : this.queue).push({ method, params, resolve, reject }); this._drain(); });
    },
    async _drain() {
      if (this.busy || (!this.queue.length && !this.lowQueue.length)) return;
      if (!this.connected) { this.queue.splice(0).concat(this.lowQueue.splice(0)).forEach((q) => q.reject(new Error("wallet not connected"))); return; }
      this.busy = true;
      const q = this.queue.shift() || this.lowQueue.shift();
      const wait = 150 - (Date.now() - this.lastSend); if (wait > 0) await new Promise((r) => setTimeout(r, wait));
      const id = String(++this.id);
      const done = (fn) => (v) => { this.pending.delete(id); this.busy = false; fn(v); this._drain(); };
      this.pending.set(id, { resolve: done(q.resolve), reject: done(q.reject) });
      const body = { jsonrpc: "2.0", id: Number(id), method: q.method }; if (q.params !== undefined) body.params = q.params;
      this.lastSend = Date.now();
      try { this.ws.send(V.jsonBig(body)); } catch (e) { this.pending.get(id).reject(e); }
      const isDaemon = q.method.startsWith("DERO."), isTransfer = q.method === "transfer";
      // a transfer the wallet has not answered may still be approved and sent later: keep listening for its answer,
      // free the queue, and let app.tx refuse new transactions while one is unanswered
      setTimeout(() => {
        if (!this.pending.has(id)) return;
        if (isTransfer) { this.pending.get(id).late = true; this.unanswered.add(id); this.busy = false; q.reject(new Error("the wallet has not answered in 10 minutes; if you approve it later the transaction is still sent: check the wallet history before sending again")); this._drain(); return; }
        this.pending.get(id).reject(new Error(q.method + (isDaemon ? " timed out" : ": no answer from the wallet (permission prompt?)")));
      }, isTransfer ? 600000 : isDaemon ? 30000 : 180000);
    },

    // Wallet API used by the app
    getBalance(scid, low) { return this.call("GetBalance", scid ? { scid } : {}, low ? { low: true } : undefined); },
    getHeight() { return this.call("GetHeight"); },
    transfer(params) { return this.call("transfer", Object.assign({ ringsize: 2 }, params)); },
    subscribe(event) { return this.call("Subscribe", { event }); },
    proxy(method, params) { return this.call(method, params); },
    isDenied(e) { return e && (e.code === -32043 || e.code === -32044); },
  };

  V.wallet = wallet;
})();
