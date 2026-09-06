// DMZ live.js — what makes the app feel alive: a block clock driving refreshes, rolling numbers with up/down
// flashes, live "ago" labels, the price tape and the mood glow. Respects prefers-reduced-motion.
(function () {
  const V = window.Veil;
  const reduced = () => window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  const live = {
    height: 0, blockAt: 0, listeners: [], polling: false, interval: 2500,
    onBlock(fn) { this.listeners.push(fn); },
    start() { if (this.polling) return; this.polling = true; this.poll(); setInterval(() => this.tickAges(), 1000); },
    async poll() {
      try { const h = await V.node.height(); if (h && h !== this.height) { const first = !this.height; this.height = h; this.blockAt = Date.now(); if (!first) this.listeners.forEach((f) => { try { f(h); } catch (e) { console.error(e); } }); else this.listeners.forEach((f) => { try { f(h); } catch (e) {} }); } } catch (e) {}
      setTimeout(() => this.poll(), this.interval);
    },
    // <span data-ts="1700000000"> → "12s ago"; <span data-block-age> → seconds since the last block
    tickAges() {
      const now = Date.now();
      for (const el of document.querySelectorAll("[data-ts]")) { const t = +el.dataset.ts; if (t) el.textContent = V.ui.timeAgo(t) + " ago"; }
      for (const el of document.querySelectorAll("[data-block-age]")) el.textContent = this.blockAt ? Math.floor((now - this.blockAt) / 1000) + "s" : "—";
    },

    // rolling number: animates from the previous value shown in this element and flashes on change
    num(el, value, fmt, opt = {}) {
      const prev = el._v; el._v = value;
      if (prev === undefined || !el.textContent || reduced() || typeof value !== "number" || typeof prev !== "number" || !isFinite(prev) || !isFinite(value)) { el.textContent = fmt(value); return; }
      if (prev === value) return;
      const t0 = performance.now(), dur = opt.ms || 500;
      const step = (t) => { const k = Math.min(1, (t - t0) / dur), e = 1 - Math.pow(1 - k, 3); el.textContent = fmt(prev + (value - prev) * e); if (k < 1) requestAnimationFrame(step); else el.textContent = fmt(value); };
      requestAnimationFrame(step);
      if (!opt.noFlash) this.flash(el, value > prev ? "up" : "down");
    },
    flash(el, dir) { el.classList.remove("flash-up", "flash-down"); void el.offsetWidth; el.classList.add(dir === "up" ? "flash-up" : "flash-down"); setTimeout(() => el.classList.remove("flash-up", "flash-down"), 900); },

    // price tape: <div id="tape"> gets two copies of the items for a seamless marquee; existing items update in place
    tape(container, assets) {
      const S = V.stats, h = V.ui.h;
      const items = assets.filter((s) => s.rd > 0n).slice(0, 14);
      if (!items.length) { container.hidden = true; return; }
      container.hidden = false;
      let track = container.querySelector(".tape-track");
      if (!track || track.dataset.n !== String(items.length)) {
        track = h("div", { class: "tape-track", "data-n": items.length });
        for (let copy = 0; copy < 2; copy++) for (const s of items) track.append(h("button", { class: "tape-item", type: "button", "data-scid": s.scid, "data-copy": copy, onclick: () => V.ui.router.go("token", s.scid) }, h("b", null, s.ticker), h("span", { class: "tp num" }), h("span", { class: "tc" })));
        V.ui.render(container, track);
        if (reduced()) track.style.animation = "none";
      }
      for (const el of track.querySelectorAll(".tape-item")) {
        const s = assets.find((x) => x.scid === el.dataset.scid); if (!s) continue;
        this.num(el.querySelector(".tp"), s.price, S.fmtPriceSub, { ms: 350 });
        const c = s.m ? s.m.c24h : null; const tc = el.querySelector(".tc"); tc.textContent = c === null ? "" : V.amm.pct(c); tc.className = "tc " + (c === null ? "" : c >= 0 ? "up" : "down");
      }
      container.style.setProperty("--tape-dur", Math.max(20, items.length * 4) + "s");
    },

    // ambient glow follows market mood (volume-weighted 24h change of the listed assets)
    mood(assets) {
      let w = 0, acc = 0;
      for (const s of assets) if (s.m && s.m.c24h !== null) { const k = 1 + Math.log1p(s.m.vol24); acc += s.m.c24h * k; w += k; }
      const m = w ? acc / w : 0; const t = Math.max(-1, Math.min(1, m / 25));
      const hue = t >= 0 ? 150 + (1 - t) * 90 : 350 + t * 15; // green ← violet → coral
      document.documentElement.style.setProperty("--mood", `hsl(${hue} 80% 60%)`);
      document.documentElement.style.setProperty("--mood-a", (0.12 + Math.abs(t) * 0.18).toFixed(2));
      document.documentElement.dataset.mood = t > 0.15 ? "up" : t < -0.15 ? "down" : "flat";
    },
  };
  V.live = live;
})();
