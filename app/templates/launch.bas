Function InitializePrivate(name String, ticker String, descr String, icon String, tags String, quote String, dec Uint64, supply Uint64, grad Uint64, cbps Uint64, tax Uint64, tmode Uint64, treasury String, fee Uint64, pfee Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM t, s as String
11 DIM alloc, rest, lq, c, vd0, vt0 as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF STRLEN(name) == 0 || STRLEN(name) > 32 || STRLEN(ticker) == 0 || STRLEN(ticker) > 10 THEN GOTO 900
23 IF STRLEN(descr) > 240 || STRLEN(icon) > 200 || STRLEN(tags) > 100 THEN GOTO 900
24 IF dec > 12 || supply < 1000000 || supply >= 9223372036854775808 THEN GOTO 900
25 IF grad < 1000000 || grad > 1000000000000 THEN GOTO 900
26 IF cbps > 500 || tax > 1000 || tmode > 3 THEN GOTO 900
27 IF tmode == 0 && tax != 0 THEN GOTO 900
28 IF tmode != 0 && tax == 0 THEN GOTO 900
29 IF fee > 100 || pfee > 10000 THEN GOTO 900
30 IF STRLEN(quote) != 32 THEN GOTO 900
31 LET t = ADDRESS_RAW(treasury)
32 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
33 STORE("qt", quote)
40 LET alloc = bps(supply, cbps)
41 LET rest = supply - alloc
42 LET lq = rest / 5
43 LET c = rest - lq
44 LET vd0 = grad / 3
45 LET vt0 = muldiv(grad + vd0, c, grad)
50 STORE("nameHdr", name)
51 STORE("descrHdr", descr)
52 STORE("iconURLHdr", icon)
53 STORE("tagsHdr", tags)
54 STORE("typeHdr", "Token")
55 STORE("kind", "launch")
56 STORE("v", 2)
57 STORE("ticker", ticker)
58 STORE("dec", dec)
59 STORE("S", supply)
60 STORE("creator", s)
61 STORE("treasury", t)
62 STORE("fee", fee)
63 STORE("pfee", pfee)
64 STORE("tax", tax)
65 STORE("tmode", tmode)
66 STORE("cs", c)
67 STORE("Lq", lq)
68 STORE("G", grad)
69 STORE("vd0", vd0)
70 STORE("phase", 0)
71 STORE("sold", 0)
72 STORE("real", 0)
73 STORE("rd", vd0)
74 STORE("ra", vt0)
75 STORE("fd", 0)
76 STORE("fa", 0)
77 STORE("tacc", 0)
78 STORE("rpool", 0)
79 STORE("locked", 0)
80 STORE("stot", 0)
81 STORE("sacc", 0)
82 STORE("tc", 0)
83 STORE("pc", 0)
84 STORE("lh", 0)
85 STORE("lt", 0)
86 STORE("sera", 0)
87 STORE("fc", 0)
89 STORE("initd", 1)
90 IF alloc == 0 THEN GOTO 100
91 SEND_ASSET_TO_ADDRESS(s, alloc, SCID())
100 RETURN 0
900 RETURN 1
End Function
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
10 DIM d, fee, pf, tx, deff, rd, ra, out, rem, cost, real, used as Uint64
11 DIM q as String
20 LET q = LOAD("qt")
21 IF q != DERO() && DEROVALUE() != 0 THEN GOTO 900
22 LET d = ASSETVALUE(q)
23 IF d == 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
24 LET rd = LOAD("rd")
25 LET ra = LOAD("ra")
26 LET fee = bpsup(d, LOAD("fee"))
27 LET tx = bps(d, LOAD("tax"))
28 IF fee + tx >= d THEN GOTO 900
29 LET deff = d - fee - tx
30 IF LOAD("phase") == 1 THEN GOTO 200
31 LET used = d
40 LET real = LOAD("real")
41 IF real + LOAD("fd") + LOAD("tacc") + LOAD("rpool") + d < real THEN GOTO 900
42 LET rem = LOAD("cs") - LOAD("sold")
43 IF rem == 0 THEN GOTO 900
44 LET out = muldiv(deff, ra, rd + deff)
45 IF out < rem THEN GOTO 60
46 LET out = rem
47 LET cost = muldivup(rem, rd, ra - rem)
48 IF cost > deff THEN GOTO 900
49 IF cost == deff THEN GOTO 60
50 LET used = muldivup(cost, 10000, 10000 - LOAD("fee") - LOAD("tax")) + 1
51 IF used < d THEN GOTO 53
52 LET used = d
53 LET fee = bpsup(used, LOAD("fee"))
54 LET tx = bps(used, LOAD("tax"))
55 LET deff = used - fee - tx
56 IF deff >= cost THEN GOTO 58
57 GOTO 900
58 IF used == d THEN GOTO 60
59 SEND_ASSET_TO_ADDRESS(s, d - used, q)
60 IF out == 0 || out < min_out THEN GOTO 900
61 tick(rd, ra)
62 STORE("real", real + deff)
63 STORE("rd", rd + deff)
64 STORE("ra", ra - out)
65 STORE("sold", LOAD("sold") + out)
66 STORE("fd", LOAD("fd") + fee)
67 STORE("tacc", LOAD("tacc") + tx)
68 SEND_ASSET_TO_ADDRESS(s, out, SCID())
69 post(1, used, out, publish, s)
70 IF out < rem THEN GOTO 100
71 STORE("phase", 1)
72 STORE("rd", real + deff)
73 STORE("ra", LOAD("Lq"))
74 STORE("gh", BLOCK_HEIGHT())
100 RETURN 0
200 IF rd + LOAD("fd") + LOAD("tacc") + LOAD("rpool") + d < rd THEN GOTO 900
201 LET pf = bps(fee, LOAD("pfee"))
202 LET out = muldiv(deff, ra, rd + deff)
203 IF out == 0 || out < min_out THEN GOTO 900
204 tick(rd, ra)
205 STORE("rd", rd + deff + fee - pf)
206 STORE("ra", ra - out)
207 STORE("fd", LOAD("fd") + pf)
208 STORE("tacc", LOAD("tacc") + tx)
209 SEND_ASSET_TO_ADDRESS(s, out, SCID())
210 post(1, d, out, publish, s)
211 RETURN 0
900 RETURN 1
End Function
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
5 DIM q as String
6 LET q = LOAD("qt")
7 IF q == DERO() THEN GOTO 10
8 IF DEROVALUE() == 0 THEN GOTO 10
9 SEND_DERO_TO_ADDRESS(s, DEROVALUE())
10 IF ASSETVALUE(q) == 0 THEN GOTO 20
11 SEND_ASSET_TO_ADDRESS(s, ASSETVALUE(q), q)
20 IF ASSETVALUE(SCID()) == 0 THEN GOTO 30
21 SEND_ASSET_TO_ADDRESS(s, ASSETVALUE(SCID()), SCID())
30 STORE("rf_" + HEX(TXID()), 1)
100 RETURN 0
End Function
Function sellto(s String, min_out Uint64, publish Uint64) Uint64
10 DIM a, fee, pf, tx, aeff, rd, ra, dout, out, real as Uint64
11 DIM q as String
20 LET q = LOAD("qt")
21 IF q != DERO() && DEROVALUE() != 0 THEN GOTO 900
22 LET a = ASSETVALUE(SCID())
23 IF a == 0 || ASSETVALUE(q) != 0 THEN GOTO 900
24 LET rd = LOAD("rd")
25 LET ra = LOAD("ra")
26 IF LOAD("phase") == 1 THEN GOTO 200
30 IF a > LOAD("sold") THEN GOTO 900
31 LET real = LOAD("real")
32 LET dout = muldiv(a, rd, ra + a)
33 IF dout > real THEN GOTO 900
34 LET fee = bpsup(dout, LOAD("fee"))
35 LET tx = bps(dout, LOAD("tax"))
36 IF fee + tx >= dout THEN GOTO 900
37 LET out = dout - fee - tx
38 IF out < min_out THEN GOTO 900
39 tick(rd, ra)
40 STORE("real", real - dout)
41 STORE("rd", rd - dout)
42 STORE("ra", ra + a)
43 STORE("sold", LOAD("sold") - a)
44 STORE("fd", LOAD("fd") + fee)
45 STORE("tacc", LOAD("tacc") + tx)
46 SEND_ASSET_TO_ADDRESS(s, out, q)
47 post(2, a, out, publish, s)
48 RETURN 0
200 IF ra + LOAD("fa") + a < ra + LOAD("fa") THEN GOTO 900
201 LET fee = bpsup(a, LOAD("fee"))
202 LET pf = bps(fee, LOAD("pfee"))
203 LET aeff = a - fee
204 LET dout = muldiv(aeff, rd, ra + aeff)
205 LET tx = bps(dout, LOAD("tax"))
206 LET out = dout - tx
207 IF out == 0 || out < min_out THEN GOTO 900
208 tick(rd, ra)
209 STORE("ra", ra + a - pf)
210 STORE("rd", rd - dout)
211 STORE("fa", LOAD("fa") + pf)
212 STORE("tacc", LOAD("tacc") + tx)
213 SEND_ASSET_TO_ADDRESS(s, out, q)
214 post(2, a, out, publish, s)
215 RETURN 0
900 RETURN 1
End Function
Function ClaimFees() Uint64
10 DIM fd, fa as Uint64
11 DIM t, q as String
20 LET t = LOAD("treasury")
21 LET q = LOAD("qt")
22 LET fd = LOAD("fd")
23 LET fa = LOAD("fa")
24 IF fd == 0 && fa == 0 THEN GOTO 900
32 STORE("fd", 0)
33 STORE("fa", 0)
34 STORE("fc", LOAD("fc") + fd)
35 SEND_ASSET_TO_ADDRESS(t, fd, q)
36 SEND_ASSET_TO_ADDRESS(t, fa, SCID())
40 RETURN 0
900 RETURN 1
End Function
Function ClaimTax() Uint64
10 DIM s, q as String
11 DIM x as Uint64
20 LET s = SIGNER()
21 IF LOAD("tmode") != 1 || s != LOAD("creator") THEN GOTO 900
30 LET x = LOAD("tacc")
31 STORE("tacc", 0)
32 LET q = LOAD("qt")
33 SEND_ASSET_TO_ADDRESS(s, x, q)
40 RETURN 0
900 RETURN 1
End Function
Function Buyback() Uint64
10 DIM d, rd, ra, out as Uint64
20 IF LOAD("tmode") != 2 || LOAD("phase") != 1 THEN GOTO 900
21 LET d = LOAD("tacc")
22 IF d == 0 THEN GOTO 900
23 LET rd = LOAD("rd")
24 LET ra = LOAD("ra")
25 IF d <= rd / 100 THEN GOTO 27
26 LET d = rd / 100
27 IF d == 0 THEN GOTO 900
28 IF EXISTS("bbh") == 0 THEN GOTO 30
29 IF BLOCK_HEIGHT() < LOAD("bbh") + 20 THEN GOTO 900
30 LET out = muldiv(d, ra, rd + d)
31 IF out == 0 THEN GOTO 900
32 tick(rd, ra)
33 STORE("rd", rd + d)
34 STORE("ra", ra - out)
35 STORE("locked", LOAD("locked") + out)
36 STORE("tacc", LOAD("tacc") - d)
37 STORE("bbh", BLOCK_HEIGHT())
40 RETURN 0
900 RETURN 1
End Function
Function Distribute() Uint64
10 IF LOAD("tmode") != 3 THEN GOTO 900
20 dist()
30 RETURN 0
900 RETURN 1
End Function
Function Stake() Uint64
10 DIM s, a as String
11 DIM t, st as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || LOAD("tmode") != 3 THEN GOTO 900
22 LET t = ASSETVALUE(SCID())
23 IF t == 0 || DEROVALUE() != 0 || ASSETVALUE(LOAD("qt")) != 0 THEN GOTO 900
24 IF LOAD("stot") + t < t THEN GOTO 900
25 LET a = ADDRESS_STRING(s)
30 dist()
31 settle(s, a)
32 LET st = 0
33 IF EXISTS("st_" + a) == 0 THEN GOTO 35
34 LET st = LOAD("st_" + a)
35 STORE("st_" + a, st + t)
36 STORE("sd_" + a, LOAD("sacc"))
37 STORE("se_" + a, LOAD("sera"))
38 STORE("stot", LOAD("stot") + t)
40 RETURN 0
900 RETURN 1
End Function
Function Unstake(amt Uint64) Uint64
10 DIM s, a as String
11 DIM st as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || LOAD("tmode") != 3 THEN GOTO 900
22 IF DEROVALUE() != 0 || ASSETVALUE(SCID()) != 0 || ASSETVALUE(LOAD("qt")) != 0 THEN GOTO 900
23 LET a = ADDRESS_STRING(s)
24 IF amt == 0 || EXISTS("st_" + a) == 0 THEN GOTO 900
25 LET st = LOAD("st_" + a)
26 IF amt > st THEN GOTO 900
30 dist()
31 settle(s, a)
32 STORE("stot", LOAD("stot") - amt)
33 SEND_ASSET_TO_ADDRESS(s, amt, SCID())
34 IF amt < st THEN GOTO 40
35 DELETE("st_" + a)
36 DELETE("sd_" + a)
37 DELETE("se_" + a)
38 RETURN 0
40 STORE("st_" + a, st - amt)
41 RETURN 0
900 RETURN 1
End Function
Function Claim() Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || LOAD("tmode") != 3 THEN GOTO 900
30 dist()
31 settle(s, ADDRESS_STRING(s))
40 RETURN 0
900 RETURN 1
End Function
Function dist() Uint64
10 DIM tacc, stot, inc, acc, paid as Uint64
20 LET tacc = LOAD("tacc")
21 LET stot = LOAD("stot")
22 IF tacc == 0 || stot == 0 THEN GOTO 100
23 IF mulhi(tacc, 4294967296) < stot THEN GOTO 25
24 LET tacc = stot * 4294967295
25 LET inc = muldiv(tacc, 4294967296, stot)
26 LET acc = LOAD("sacc")
27 IF acc + inc >= acc THEN GOTO 40
28 STORE("sea_" + LOAD("sera"), acc)
29 STORE("sera", LOAD("sera") + 1)
30 LET acc = 0
40 LET paid = muldivup(inc, stot, 4294967296)
41 STORE("sacc", acc + inc)
42 STORE("tacc", LOAD("tacc") - paid)
43 STORE("rpool", LOAD("rpool") + paid)
100 RETURN 0
End Function
Function settle(s String, a String) Uint64
10 DIM st, sd, acc, pend, e, era as Uint64
11 DIM q as String
20 IF EXISTS("st_" + a) == 0 THEN GOTO 100
21 LET st = LOAD("st_" + a)
22 LET sd = LOAD("sd_" + a)
23 LET e = LOAD("se_" + a)
24 LET era = LOAD("sera")
25 LET acc = LOAD("sacc")
26 LET pend = 0
30 IF e >= era THEN GOTO 40
31 LET pend = pend + muldiv(st, LOAD("sea_" + e) - sd, 4294967296)
32 LET sd = 0
33 LET e = e + 1
34 GOTO 30
40 LET pend = pend + muldiv(st, acc - sd, 4294967296)
41 STORE("sd_" + a, acc)
42 STORE("se_" + a, era)
43 IF pend == 0 THEN GOTO 100
44 IF pend <= LOAD("rpool") THEN GOTO 50
45 LET pend = LOAD("rpool")
46 IF pend == 0 THEN GOTO 100
50 STORE("rpool", LOAD("rpool") - pend)
51 LET q = LOAD("qt")
52 SEND_ASSET_TO_ADDRESS(s, pend, q)
100 RETURN 0
End Function
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
Function post(side Uint64, ain Uint64, aout Uint64, publish Uint64, s String) Uint64
10 DIM n as Uint64
20 IF publish == 0 THEN GOTO 100
30 LET n = LOAD("tc")
40 STORE("t_" + n, "" + side + "|" + ADDRESS_STRING(s) + "|" + ain + "|" + aout + "|" + BLOCK_HEIGHT() + "|" + BLOCK_TIMESTAMP())
50 STORE("tc", n + 1)
100 RETURN 0
End Function
Function bps(x Uint64, f Uint64) Uint64
10 RETURN (x / 10000) * f + ((x % 10000) * f) / 10000
End Function
Function bpsup(x Uint64, f Uint64) Uint64
10 DIM r as Uint64
20 LET r = bps(x, f)
30 IF ((x % 10000) * f) % 10000 == 0 THEN GOTO 50
40 RETURN r + 1
50 RETURN r
End Function
Function shl(x Uint64, s Uint64) Uint64
10 RETURN x << s
End Function
Function shr(x Uint64, s Uint64) Uint64
10 RETURN x >> s
End Function
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
