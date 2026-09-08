// DMZ amm.js — BigInt mirror of reference/*.go: exact integer quotes so what the sheet shows is what the
// contract will do (the contract is the truth; these numbers only pick min_out and display impact).
(function () {
  const V = window.Veil;
  const B = BigInt, Z = 0n, TEN4 = 10000n, U64 = (1n << 64n) - 1n;
  // pool.bas's first-deposit floor and the LP it locks forever, in atomic units of the QUOTE whatever the quote
  // is: the contract has one number for DERO (1 DERO) and for wXMR (1e-7 XMR) alike. Mirrored so the sheet
  // refuses what the contract refuses; giving them economic meaning on a twelve-decimal quote is a contract
  // change, sequenced separately.
  const FIRST_DEPOSIT_MIN = 100000n, MIN_LIQ = 1000n;

  const bps = (x, f) => (x / TEN4) * f + ((x % TEN4) * f) / TEN4;
  const bpsUp = (x, f) => { const r = bps(x, f); return ((x % TEN4) * f) % TEN4 === Z ? r : r + 1n; };
  // floor(a*b/c); null when the contract would panic (c == 0 or result ≥ 2^64)
  const mulDiv = (a, b, c) => { if (c === Z) return null; const q = (a * b) / c; return q > U64 ? null : q; };
  const mulDivUp = (a, b, c) => { if (c === Z) return null; const p = a * b; let q = p / c; if (p % c !== Z) q += 1n; return q > U64 ? null : q; };

  // ---- pool.bas ----
  function poolBuy(s, d) { // quote in → asset out
    const fee = bpsUp(d, s.fee), deff = d - fee;
    if (d === Z || deff <= Z || s.rd === Z || s.ra === Z) return null;
    const out = mulDiv(deff, s.ra, s.rd + deff);
    return out && out > Z ? { out, fee, tax: Z, impact: impact(s.rd, s.ra, d, out) } : null;
  }
  function poolSell(s, a) { // asset in → quote out
    const fee = bpsUp(a, s.fee), aeff = a - fee;
    if (a === Z || aeff <= Z || s.rd === Z || s.ra === Z) return null;
    const out = mulDiv(aeff, s.rd, s.ra + aeff);
    return out && out > Z ? { out, fee, tax: Z, impact: impactSell(s.rd, s.ra, a, out) } : null;
  }
  function poolAdd(s, d, a) {
    if (s.lp === Z) { if (d < FIRST_DEPOSIT_MIN) return null; return { lp: d - MIN_LIQ, d, a, refundD: Z, refundA: Z }; }
    const ld = mulDiv(d, s.lp, s.rd), la = mulDiv(a, s.lp, s.ra); if (ld === null || la === null) return null;
    if (ld <= la) { const need = mulDivUp(ld, s.ra, s.lp); return need === null ? null : { lp: ld, d, a: need, refundD: Z, refundA: a - need }; }
    const need = mulDivUp(la, s.rd, s.lp); return need === null ? null : { lp: la, d: need, a, refundD: d - need, refundA: Z };
  }
  function poolRemove(s, l) {
    if (l === Z || l > s.lp || s.lp - l < MIN_LIQ) return null;
    const d = mulDiv(l, s.rd, s.lp), a = mulDiv(l, s.ra, s.lp); return d === null || a === null ? null : { d, a };
  }
  // required asset for a given quote deposit at the current ratio (for the add-liquidity form)
  const poolAssetFor = (s, d) => (s.lp === Z || s.rd === Z ? null : mulDivUp(d, s.ra, s.rd));

  // ---- launch.bas ----
  // Mirrors launch.bas buyto exactly, including the overshoot branch (contract lines 46-59): when the buy would
  // pass the end of the curve, out is clamped to what is left, the DERO the curve actually takes is grossed
  // back up for fee and tax (used), both are recomputed on that smaller amount, and everything above it comes
  // back as the refund. The mirror used to refund deff - cost and keep the fee and tax on DERO the curve never
  // took, so the page promised the buyer less than the contract sends.
  function launchBuy(s, d) {
    let fee = bpsUp(d, s.fee), tax = bps(d, s.tax);
    if (d === Z || fee + tax >= d) return null;
    let deff = d - fee - tax, refund = Z;
    let out = mulDiv(deff, s.ra, s.rd + deff); if (out === null) return null;
    if (s.phase === Z) {
      const rem = s.cs - s.sold; if (rem === Z) return null;
      if (out >= rem) {
        out = rem;
        const cost = mulDivUp(rem, s.rd, s.ra - rem); if (cost === null || cost > deff) return null;
        if (cost !== deff) {
          let used = mulDivUp(cost, 10000n, 10000n - s.fee - s.tax); if (used === null) return null;
          used = used + 1n; if (used > d) used = d;
          fee = bpsUp(used, s.fee); tax = bps(used, s.tax); deff = used - fee - tax;
          if (deff < cost) return null;
          refund = d - used;
        }
      }
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
  // Price impact in bps: execution price against spot (rd/ra), as one exact ratio. The earlier form scaled spot
  // to 18 decimals and divided by it, and rd*1e18/ra is zero on a drained pool (rd=1 against 1.4e18 tokens in
  // the fuzz), so poolBuy threw a RangeError where the contract had simply paid. exec/spot = (d/out)/(rd/ra) =
  // d*ra/(out*rd): cross-multiplied there is nothing to floor. rd == 0 has no spot, and 0 is what "no impact
  // computable" has always meant here.
  function impact(rd, ra, d, out) { if (out === Z || ra === Z || rd === Z) return 0; return Number(d * ra * 10000n / (out * rd)) - 10000; }
  function impactSell(rd, ra, a, dout) { if (a === Z || ra === Z || rd === Z) return 0; return 10000 - Number(dout * ra * 10000n / (a * rd)); }
  const minOut = (out, slipBps) => out - bpsUp(out, B(slipBps));

  // ---- formatting ----
  function fmtUnits(v, dec, maxFrac) {
    v = B(v); dec = Number(dec); const neg = v < Z; if (neg) v = -v;
    const s = v.toString().padStart(dec + 1, "0"); let int = s.slice(0, s.length - dec), frac = dec ? s.slice(s.length - dec) : "";
    int = int.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
    if (maxFrac !== undefined) frac = frac.slice(0, maxFrac);
    frac = frac.replace(/0+$/, "");
    // A non-zero amount that maxFrac cuts to nothing must not print as "0": the wrap screen's minimum deposit is
    // exactly the number that exists to stop an unrecoverable transfer, and on a twelve-decimal vault it sits
    // below six places. Say which side of the smallest printable step it is on instead.
    if (v !== Z && int === "0" && !frac) return (neg ? ">-" : "<") + (maxFrac ? "0." + "0".repeat(maxFrac - 1) + "1" : "1");
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
  // Price of one whole token in the market's QUOTE (float, display only).
  //
  // priceOf takes the MARKET and reads both decimals off it, so a caller cannot forget the quote side. The
  // earlier shape, price(pt, dec, qdec), defaulted a missing qdec to DERO's 5 — which meant "I forgot" and
  // "I meant DERO" were the same call, and four of six call sites forgot. On a 12-decimal market that made
  // every change window read about -100% (one side scaled, the other not) and drew the chart 10^7 away from
  // the price printed above it. A default that makes forgetting indistinguishable from meaning it is not a
  // convenience; pt defaults to the market itself because a market carries its own current reserves.
  const qdecOf = (m) => { const q = Number(m && m.quoteDec); return Number.isFinite(q) ? q : 5; };
  const priceOf = (m, pt) => { const p = pt || m; return p.ra === Z ? 0 : Number(p.rd) / 10 ** qdecOf(m) / (Number(p.ra) / 10 ** Number(m.dec)); };
  const fmtPrice = (p) => (p === 0 ? "—" : p >= 1 ? p.toLocaleString(undefined, { maximumFractionDigits: 4 }) : p.toLocaleString(undefined, { maximumSignificantDigits: 4 }));
  const pct = (x) => (x === null || x === undefined || !isFinite(x) ? "—" : (x > 0 ? "+" : "") + x.toFixed(2) + "%");

  V.amm = { bps, bpsUp, mulDiv, mulDivUp, poolBuy, poolSell, poolAdd, poolRemove, poolAssetFor, launchBuy, launchSell, minOut, fmtUnits, fmtCompact, fmtDero, parseUnits, priceOf, fmtPrice, pct, Z, B };
})();
