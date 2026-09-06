// DMZ amm.js — BigInt mirror of reference/*.go: exact integer quotes so what the sheet shows is what the
// contract will do (the contract is the truth; these numbers only pick min_out and display impact).
(function () {
  const V = window.Veil;
  const B = BigInt, Z = 0n, TEN4 = 10000n, U64 = (1n << 64n) - 1n;

  const bps = (x, f) => (x / TEN4) * f + ((x % TEN4) * f) / TEN4;
  const bpsUp = (x, f) => { const r = bps(x, f); return ((x % TEN4) * f) % TEN4 === Z ? r : r + 1n; };
  // floor(a*b/c); null when the contract would panic (c == 0 or result ≥ 2^64)
  const mulDiv = (a, b, c) => { if (c === Z) return null; const q = (a * b) / c; return q > U64 ? null : q; };
  const mulDivUp = (a, b, c) => { if (c === Z) return null; const p = a * b; let q = p / c; if (p % c !== Z) q += 1n; return q > U64 ? null : q; };

  // ---- pool.bas ----
  function poolBuy(s, d) { // DERO in → asset out
    const fee = bpsUp(d, s.fee), deff = d - fee;
    if (d === Z || deff <= Z || s.rd === Z || s.ra === Z) return null;
    const out = mulDiv(deff, s.ra, s.rd + deff);
    return out && out > Z ? { out, fee, tax: Z, impact: impact(s.rd, s.ra, d, out) } : null;
  }
  function poolSell(s, a) { // asset in → DERO out
    const fee = bpsUp(a, s.fee), aeff = a - fee;
    if (a === Z || aeff <= Z || s.rd === Z || s.ra === Z) return null;
    const out = mulDiv(aeff, s.rd, s.ra + aeff);
    return out && out > Z ? { out, fee, tax: Z, impact: impactSell(s.rd, s.ra, a, out) } : null;
  }
  function poolAdd(s, d, a) {
    if (s.lp === Z) { if (d < 100000n) return null; return { lp: d - 1000n, d, a, refundD: Z, refundA: Z }; }
    const ld = mulDiv(d, s.lp, s.rd), la = mulDiv(a, s.lp, s.ra); if (ld === null || la === null) return null;
    if (ld <= la) { const need = mulDivUp(ld, s.ra, s.lp); return need === null ? null : { lp: ld, d, a: need, refundD: Z, refundA: a - need }; }
    const need = mulDivUp(la, s.rd, s.lp); return need === null ? null : { lp: la, d: need, a, refundD: d - need, refundA: Z };
  }
  function poolRemove(s, l) {
    if (l === Z || l > s.lp || s.lp - l < 1000n) return null;
    const d = mulDiv(l, s.rd, s.lp), a = mulDiv(l, s.ra, s.lp); return d === null || a === null ? null : { d, a };
  }
  // required asset for a given DERO deposit at the current ratio (for the add-liquidity form)
  const poolAssetFor = (s, d) => (s.lp === Z || s.rd === Z ? null : mulDivUp(d, s.ra, s.rd));

  // ---- launch.bas ----
  function launchBuy(s, d) {
    const fee = bpsUp(d, s.fee), tax = bps(d, s.tax);
    if (d === Z || fee + tax >= d) return null;
    let deff = d - fee - tax, refund = Z;
    let out = mulDiv(deff, s.ra, s.rd + deff); if (out === null) return null;
    if (s.phase === Z) {
      const rem = s.cs - s.sold; if (rem === Z) return null;
      if (out >= rem) { out = rem; const cost = mulDivUp(rem, s.rd, s.ra - rem); if (cost === null || cost > deff) return null; refund = deff - cost; deff = cost; }
    }
    if (out === Z) return null;
    return { out, fee, tax, refund, impact: impact(s.rd, s.ra, d, out), graduates: s.phase === Z && out === s.cs - s.sold };
  }
  function launchSell(s, a) {
    if (a === Z) return null;
    if (s.phase === Z) {
      if (a > s.sold) return null;
      const dout = mulDiv(a, s.rd, s.ra + a); if (dout === null || dout > s.real) return null;
      const fee = bpsUp(dout, s.fee), tax = bps(dout, s.tax); if (fee + tax >= dout) return null;
      return { out: dout - fee - tax, fee, tax, impact: impactSell(s.rd, s.ra, a, dout) };
    }
    const fee = bpsUp(a, s.fee), aeff = a - fee; if (aeff <= Z) return null;
    const dout = mulDiv(aeff, s.rd, s.ra + aeff); if (dout === null) return null;
    const tax = bps(dout, s.tax), out = dout - tax; if (out <= Z) return null;
    return { out, fee, tax, impact: impactSell(s.rd, s.ra, a, dout) };
  }
  // price impact in bps: execution price vs spot price (rd/ra)
  function impact(rd, ra, d, out) { if (out === Z || ra === Z) return 0; const spot = rd * 10n ** 18n / ra, exec = d * 10n ** 18n / out; return Number((exec - spot) * 10000n / spot); }
  function impactSell(rd, ra, a, dout) { if (a === Z || ra === Z) return 0; const spot = rd * 10n ** 18n / ra, exec = dout * 10n ** 18n / a; return Number((spot - exec) * 10000n / spot); }
  const minOut = (out, slipBps) => out - bpsUp(out, B(slipBps));

  // ---- formatting ----
  function fmtUnits(v, dec, maxFrac) {
    v = B(v); dec = Number(dec); const neg = v < Z; if (neg) v = -v;
    const s = v.toString().padStart(dec + 1, "0"); let int = s.slice(0, s.length - dec), frac = dec ? s.slice(s.length - dec) : "";
    int = int.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    if (maxFrac !== undefined) frac = frac.slice(0, maxFrac);
    frac = frac.replace(/0+$/, "");
    return (neg ? "-" : "") + int + (frac ? "." + frac : "");
  }
  // compact: 12.4K, 3.2M
  function fmtCompact(v, dec) {
    const n = Number(B(v)) / 10 ** Number(dec);
    if (!isFinite(n)) return fmtUnits(v, dec, 2);
    const a = Math.abs(n);
    if (a >= 1e9) return (n / 1e9).toFixed(2) + "B"; if (a >= 1e6) return (n / 1e6).toFixed(2) + "M"; if (a >= 1e4) return (n / 1e3).toFixed(1) + "K";
    if (a >= 1) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
    return n.toLocaleString(undefined, { maximumSignificantDigits: 3 });
  }
  const fmtDero = (v, f) => fmtUnits(v, 5, f === undefined ? 5 : f);
  function parseUnits(str, dec) {
    str = String(str || "").trim().replace(/,/g, ""); if (!/^\d*\.?\d*$/.test(str) || str === "" || str === ".") return null;
    let [i, f = ""] = str.split("."); dec = Number(dec); if (f.length > dec) f = f.slice(0, dec);
    return B((i || "0") + f.padEnd(dec, "0"));
  }
  // price of one whole token in DERO (float, display only)
  const price = (s, dec) => (s.ra === Z ? 0 : Number(s.rd) / 1e5 / (Number(s.ra) / 10 ** Number(dec)));
  const fmtPrice = (p) => (p === 0 ? "—" : p >= 1 ? p.toLocaleString(undefined, { maximumFractionDigits: 4 }) : p.toLocaleString(undefined, { maximumSignificantDigits: 4 }));
  const pct = (x) => (x === null || x === undefined || !isFinite(x) ? "—" : (x > 0 ? "+" : "") + x.toFixed(2) + "%");

  V.amm = { bps, bpsUp, mulDiv, mulDivUp, poolBuy, poolSell, poolAdd, poolRemove, poolAssetFor, launchBuy, launchSell, minOut, fmtUnits, fmtCompact, fmtDero, parseUnits, price, fmtPrice, pct, Z, B };
})();
