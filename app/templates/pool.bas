// Veil pool v1 - constant-product AMM for one DERO/asset pair. Immutable: no UpdateCode.
// All arithmetic is uint64. Every product of two possibly-large numbers goes through muldiv (128-bit).
// Every other arithmetic line carries its bound. Rounding: outputs floor, fees ceil, LP mint floor.
// Invariant: the contract holds exactly rd + fd DERO and ra + fa asset; every credit is guarded against wrapping
// that total (the node credits the FULL incoming value with plain wrapping arithmetic before the code runs, so the
// guard always uses the amounts received, never the amounts used).
// Every entrypoint requires ringsize 2: SIGNER() must resolve because output is sent to the signer.
// Storage keys: kind v asset treasury fee pfee rd ra lp fd fa tc pc lh lt   t_<n> (published trades)   p_<n> (price points)
// Trade record t_<n> = side|address|in|out|height|timestamp   (side 1 = bought asset with DERO, 2 = sold asset for DERO)
// Price point  p_<n> = height|timestamp|rd|ra  = closing reserves of the previous active block, written on the first reserve change of a new block

Function InitializePrivate(asset String, treasury String, fee Uint64, pfee Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM t as String
20 IF STRLEN(asset) != 32 THEN GOTO 900
30 IF asset == DERO() THEN GOTO 900
40 IF fee > 100 THEN GOTO 900
50 IF pfee > 10000 THEN GOTO 900
60 LET t = ADDRESS_RAW(treasury)
70 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
80 STORE("kind", "pool")
81 STORE("v", 1)
82 STORE("asset", asset)
83 STORE("treasury", t)
84 STORE("fee", fee)
85 STORE("pfee", pfee)
86 STORE("rd", 0)
87 STORE("ra", 0)
88 STORE("lp", 0)
89 STORE("fd", 0)
90 STORE("fa", 0)
91 STORE("tc", 0)
92 STORE("pc", 0)
93 STORE("lh", 0)
94 STORE("lt", 0)
95 STORE("fc", 0)
99 STORE("initd", 1)
100 RETURN 0
900 RETURN 1
End Function

// Deposit DERO and asset in the current ratio. Excess on one side is refunded. Mints LP (this SCID) to the signer.
// First deposit: LP minted = DERO deposited, 1000 units stay locked forever (MIN_LIQ), first deposit must be >= 1 DERO.
Function AddLiquidity(min_lp Uint64) Uint64
10 DIM d, a, rd, ra, lp, l, ld, la, need as Uint64
11 DIM s, asset as String
20 LET s = SIGNER()
30 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
40 LET asset = LOAD("asset")
50 LET d = DEROVALUE()
60 LET a = ASSETVALUE(asset)
70 IF d == 0 || a == 0 THEN GOTO 900
80 IF ASSETVALUE(SCID()) != 0 THEN GOTO 900
90 LET rd = LOAD("rd")
100 LET ra = LOAD("ra")
110 LET lp = LOAD("lp")
115 IF rd + LOAD("fd") + d < rd + LOAD("fd") || ra + LOAD("fa") + a < ra + LOAD("fa") THEN GOTO 900
120 tick(rd, ra)
130 IF lp > 0 THEN GOTO 300
140 IF d < 100000 THEN GOTO 900
150 LET l = d - 1000
160 IF l < min_lp THEN GOTO 900
170 STORE("rd", d)
180 STORE("ra", a)
190 STORE("lp", d)
200 SEND_ASSET_TO_ADDRESS(s, l, SCID())
210 RETURN 0
300 LET ld = muldiv(d, lp, rd)
310 LET la = muldiv(a, lp, ra)
320 IF ld <= la THEN GOTO 400
330 LET l = la
340 LET need = muldivup(l, rd, lp)
350 IF need > d THEN GOTO 900
360 IF d == need THEN GOTO 380
370 SEND_DERO_TO_ADDRESS(s, d - need)
380 LET d = need
390 GOTO 500
400 LET l = ld
410 LET need = muldivup(l, ra, lp)
420 IF need > a THEN GOTO 900
430 IF a == need THEN GOTO 450
440 SEND_ASSET_TO_ADDRESS(s, a - need, asset)
450 LET a = need
500 IF l == 0 || l < min_lp THEN GOTO 900
510 IF lp + l < lp THEN GOTO 900
520 STORE("rd", rd + d)
530 STORE("ra", ra + a)
540 STORE("lp", lp + l)
550 SEND_ASSET_TO_ADDRESS(s, l, SCID())
560 RETURN 0
900 RETURN 1
End Function

// Burn LP (send this SCID) and receive the proportional share of both reserves, rounded down.
Function RemoveLiquidity(min_dero Uint64, min_asset Uint64) Uint64
10 DIM l, rd, ra, lp, dout, aout as Uint64
11 DIM s, asset as String
20 LET s = SIGNER()
30 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
40 LET asset = LOAD("asset")
50 LET l = ASSETVALUE(SCID())
60 IF l == 0 THEN GOTO 900
70 IF DEROVALUE() != 0 || ASSETVALUE(asset) != 0 THEN GOTO 900
80 LET rd = LOAD("rd")
90 LET ra = LOAD("ra")
100 LET lp = LOAD("lp")
110 IF l > lp || lp - l < 1000 THEN GOTO 900
120 LET dout = muldiv(l, rd, lp)
130 LET aout = muldiv(l, ra, lp)
140 IF dout < min_dero || aout < min_asset THEN GOTO 900
150 tick(rd, ra)
160 STORE("rd", rd - dout)
170 STORE("ra", ra - aout)
180 STORE("lp", lp - l)
190 SEND_DERO_TO_ADDRESS(s, dout)
200 SEND_ASSET_TO_ADDRESS(s, aout, asset)
210 RETURN 0
900 RETURN 1
End Function

// Send DERO, receive asset. fee = ceil(d * fee / 10000); protocol share pf = floor(fee * pfee / 10000) accrues to fd; the rest stays in reserves.
Function SwapDeroForAsset(min_out Uint64, publish Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
30 RETURN buyto(s, min_out, publish)
900 RETURN 1
End Function

Function SwapDeroForAssetTo(dest String, min_out Uint64, publish Uint64) Uint64
10 DIM s as String
20 LET s = ADDRESS_RAW(dest)
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
30 IF buyto(s, min_out, 0) == 0 THEN GOTO 100
40 refundto(s)
100 RETURN 0
900 RETURN 1
End Function

Function buyto(s String, min_out Uint64, publish Uint64) Uint64
10 DIM d, fee, pf, deff, rd, ra, out as Uint64
11 DIM asset as String
40 LET asset = LOAD("asset")
50 LET d = DEROVALUE()
60 IF d == 0 THEN GOTO 900
70 IF ASSETVALUE(asset) != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
80 LET rd = LOAD("rd")
90 LET ra = LOAD("ra")
100 IF rd == 0 || ra == 0 THEN GOTO 900
110 LET fee = bpsup(d, LOAD("fee"))
120 LET pf = bps(fee, LOAD("pfee"))
130 LET deff = d - fee
140 IF rd + LOAD("fd") + d < rd + LOAD("fd") THEN GOTO 900
150 LET out = muldiv(deff, ra, rd + deff)
160 IF out == 0 || out < min_out THEN GOTO 900
170 tick(rd, ra)
180 STORE("rd", rd + d - pf)
190 STORE("ra", ra - out)
200 STORE("fd", LOAD("fd") + pf)
210 SEND_ASSET_TO_ADDRESS(s, out, asset)
220 post(1, d, out, publish, s)
230 RETURN 0
900 RETURN 1
End Function

// Send asset, receive DERO. Fee taken in asset units with the same rule.
Function SwapAssetForDero(min_out Uint64, publish Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
30 RETURN sellto(s, min_out, publish)
900 RETURN 1
End Function

Function SwapAssetForDeroTo(dest String, min_out Uint64, publish Uint64) Uint64
10 DIM s as String
20 LET s = ADDRESS_RAW(dest)
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
30 IF sellto(s, min_out, 0) == 0 THEN GOTO 100
40 refundto(s)
100 RETURN 0
900 RETURN 1
End Function

Function refundto(s String) Uint64
10 IF DEROVALUE() == 0 THEN GOTO 20
11 SEND_DERO_TO_ADDRESS(s, DEROVALUE())
20 IF ASSETVALUE(LOAD("asset")) == 0 THEN GOTO 30
21 SEND_ASSET_TO_ADDRESS(s, ASSETVALUE(LOAD("asset")), LOAD("asset"))
30 IF ASSETVALUE(SCID()) == 0 THEN GOTO 40
31 SEND_ASSET_TO_ADDRESS(s, ASSETVALUE(SCID()), SCID())
40 STORE("rf_" + HEX(TXID()), 1)
100 RETURN 0
End Function

Function sellto(s String, min_out Uint64, publish Uint64) Uint64
10 DIM a, fee, pf, aeff, rd, ra, out as Uint64
11 DIM asset as String
40 LET asset = LOAD("asset")
50 LET a = ASSETVALUE(asset)
60 IF a == 0 THEN GOTO 900
70 IF DEROVALUE() != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
80 LET rd = LOAD("rd")
90 LET ra = LOAD("ra")
100 IF rd == 0 || ra == 0 THEN GOTO 900
110 LET fee = bpsup(a, LOAD("fee"))
120 LET pf = bps(fee, LOAD("pfee"))
130 LET aeff = a - fee
140 IF ra + LOAD("fa") + a < ra + LOAD("fa") THEN GOTO 900
150 LET out = muldiv(aeff, rd, ra + aeff)
160 IF out == 0 || out < min_out THEN GOTO 900
170 tick(rd, ra)
180 STORE("ra", ra + a - pf)
190 STORE("rd", rd - out)
200 STORE("fa", LOAD("fa") + pf)
210 SEND_DERO_TO_ADDRESS(s, out)
220 post(2, a, out, publish, s)
230 RETURN 0
900 RETURN 1
End Function

// Treasury withdraws accrued protocol fees.
Function ClaimFees() Uint64
10 DIM fd, fa as Uint64
11 DIM s as String
20 LET s = SIGNER()
30 IF s != LOAD("treasury") THEN GOTO 900
40 LET fd = LOAD("fd")
50 LET fa = LOAD("fa")
60 STORE("fd", 0)
70 STORE("fa", 0)
75 STORE("fc", LOAD("fc") + fd)
80 SEND_DERO_TO_ADDRESS(s, fd)
90 SEND_ASSET_TO_ADDRESS(s, fa, LOAD("asset"))
100 RETURN 0
900 RETURN 1
End Function

// ---- internal helpers -------------------------------------------------------------------------

// Price point: on the first reserve change in a new block, persist the closing reserves of the previous active block.
Function tick(rd Uint64, ra Uint64) Uint64
10 DIM h, n as Uint64
20 LET h = BLOCK_HEIGHT()
30 IF LOAD("lh") == h THEN GOTO 100
40 IF LOAD("lh") == 0 THEN GOTO 80
50 LET n = LOAD("pc")
60 STORE("p_" + n, "" + LOAD("lh") + "|" + LOAD("lt") + "|" + rd + "|" + ra)
70 STORE("pc", n + 1)
80 STORE("lh", h)
90 STORE("lt", BLOCK_TIMESTAMP())
100 RETURN 0
End Function

// Published trade record. Only written when the trader passed publish = 1.
Function post(side Uint64, ain Uint64, aout Uint64, publish Uint64, s String) Uint64
10 DIM n as Uint64
20 IF publish == 0 THEN GOTO 100
30 LET n = LOAD("tc")
40 STORE("t_" + n, "" + side + "|" + ADDRESS_STRING(s) + "|" + ain + "|" + aout + "|" + BLOCK_HEIGHT() + "|" + BLOCK_TIMESTAMP())
50 STORE("tc", n + 1)
100 RETURN 0
End Function

// floor(x * f / 10000) exactly, without overflow, for f <= 10000:  x = 10000q + r  =>  q*f + floor(r*f/10000)
Function bps(x Uint64, f Uint64) Uint64
10 RETURN (x / 10000) * f + ((x % 10000) * f) / 10000
End Function

// ceil(x * f / 10000): the only fractional part comes from r*f/10000
Function bpsup(x Uint64, f Uint64) Uint64
10 DIM r as Uint64
20 LET r = bps(x, f)
30 IF ((x % 10000) * f) % 10000 == 0 THEN GOTO 50
40 RETURN r + 1
50 RETURN r
End Function

// DVM quirk: LET parses without the operator rejoiner, so only single-character operators (+ - * / % & | ^) work in LET.
// Shifts are therefore written as multiply/divide by 2^32 (identical under wrapping) or via shl/shr, which use RETURN.
Function shl(x Uint64, s Uint64) Uint64
10 RETURN x << s
End Function

Function shr(x Uint64, s Uint64) Uint64
10 RETURN x >> s
End Function

// High 64 bits of a*b via 32-bit limbs. p00, p01, p10, a1*b1 are each < 2^64; mid < 3 * 2^32.
Function mulhi(a Uint64, b Uint64) Uint64
10 DIM a0, a1, b0, b1, p00, p01, p10, mid as Uint64
20 LET a0 = a & 4294967295
30 LET a1 = a / 4294967296
40 LET b0 = b & 4294967295
50 LET b1 = b / 4294967296
60 LET p00 = a0 * b0
70 LET p01 = a0 * b1
80 LET p10 = a1 * b0
90 LET mid = p00 / 4294967296 + (p01 & 4294967295) + (p10 & 4294967295)
100 RETURN a1 * b1 + p01 / 4294967296 + p10 / 4294967296 + mid / 4294967296
End Function

// Number of leading zero bits of x, x != 0. Binary search, six steps. x * 2^k wraps exactly like x << k.
Function nlz(x Uint64) Uint64
10 DIM n as Uint64
20 LET n = 0
30 IF x >= 4294967296 THEN GOTO 60
40 LET n = n + 32
50 LET x = x * 4294967296
60 IF x >= 281474976710656 THEN GOTO 90
70 LET n = n + 16
80 LET x = x * 65536
90 IF x >= 72057594037927936 THEN GOTO 120
100 LET n = n + 8
110 LET x = x * 256
120 IF x >= 1152921504606846976 THEN GOTO 150
130 LET n = n + 4
140 LET x = x * 16
150 IF x >= 4611686018427387904 THEN GOTO 180
160 LET n = n + 2
170 LET x = x * 4
180 IF x >= 9223372036854775808 THEN GOTO 200
190 LET n = n + 1
200 RETURN n
End Function

// (u1 * 2^64 + u0) / v for u1 < v (quotient fits in 64 bits). Hacker's Delight divlu2 with base 2^32.
// Wrapping arithmetic on line 200 is exact because the true value of un21 is below 2^64.
// The && conditions are evaluated in full (no short-circuit): a wrapped q*vn0 is harmless because q < 2^32 fails first.
Function divlu(u1 Uint64, u0 Uint64, v Uint64) Uint64
10 DIM s, vn1, vn0, un32, un10, un1, un0, q1, q0, rhat, un21 as Uint64
20 LET s = nlz(v)
30 LET v = shl(v, s)
40 LET vn1 = v / 4294967296
50 LET vn0 = v & 4294967295
60 IF s == 0 THEN GOTO 90
70 LET un32 = shl(u1, s) | shr(u0, 64 - s)
80 GOTO 100
90 LET un32 = u1
100 LET un10 = shl(u0, s)
110 LET un1 = un10 / 4294967296
120 LET un0 = un10 & 4294967295
130 LET q1 = un32 / vn1
140 LET rhat = un32 - q1 * vn1
150 IF q1 < 4294967296 && q1 * vn0 <= rhat * 4294967296 + un1 THEN GOTO 200
160 LET q1 = q1 - 1
170 LET rhat = rhat + vn1
180 IF rhat < 4294967296 THEN GOTO 150
200 LET un21 = un32 * 4294967296 + un1 - q1 * v
210 LET q0 = un21 / vn1
220 LET rhat = un21 - q0 * vn1
230 IF q0 < 4294967296 && q0 * vn0 <= rhat * 4294967296 + un0 THEN GOTO 300
240 LET q0 = q0 - 1
250 LET rhat = rhat + vn1
260 IF rhat < 4294967296 THEN GOTO 230
300 RETURN q1 * 4294967296 + q0
End Function

// floor(a * b / c). Panics (reverting the whole call) when c == 0 or the result does not fit in 64 bits.
Function muldiv(a Uint64, b Uint64, c Uint64) Uint64
10 DIM hi, lo as Uint64
20 LET hi = mulhi(a, b)
30 LET lo = a * b
40 IF hi >= c THEN GOTO 900
50 IF hi == 0 THEN GOTO 80
60 RETURN divlu(hi, lo, c)
80 RETURN lo / c
900 PANIC()
910 RETURN 0
End Function

// ceil(a * b / c). Remainder = lo - q*c in wrapping arithmetic, exact because the true remainder is below c < 2^64.
// q = 2^64-1 with a non-zero remainder means the ceiling does not fit: panic instead of wrapping to 0.
Function muldivup(a Uint64, b Uint64, c Uint64) Uint64
10 DIM q as Uint64
20 LET q = muldiv(a, b, c)
30 IF a * b - q * c == 0 THEN GOTO 50
35 IF q == 18446744073709551615 THEN GOTO 900
40 RETURN q + 1
50 RETURN q
900 PANIC()
910 RETURN 0
End Function
