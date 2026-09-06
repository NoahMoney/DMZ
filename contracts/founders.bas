// Veil Founders Pass v1: a fixed-edition NFT (this contract's own asset, 0 decimals) whose registered holders receive
// a share of the exchange's fees, paid in DERO to their wallets. Anyone can Deposit; the protocol treasury deposits
// `share` bps of the fees it claims from every market (the promise is stored here, immutable). Rewards use a
// per-unit accumulator (acc, ×2^32) so a deposit is O(1) and any number of holders is supported; PayBatch pushes
// pending rewards to registered wallets in slices, Claim pulls them. Built-in marketplace: List / Buy / Unlist at a
// fixed price per unit, `mfee` bps of every sale goes to the holders. Immutable: no UpdateCode.
// Keys: nameHdr descrHdr iconURLHdr fileURL typeHdr kind v creator treasury price maxsupply start reserve share mfee
//       minted hst acc rpool und dep mdep paid hn   h_<i> hi_<addr> hu_<addr> hd_<addr>   ln la ls_<id> lp_<id> lu_<id>
// Invariant: contract DERO == rpool + und. rpool is owed to registered holders (already in acc); und is waiting for
// the first registration. Contract units == hst + sum(lu_).

Function InitializePrivate(name String, descr String, icon String, file String, price Uint64, maxsupply Uint64, start Uint64, reserve Uint64, share Uint64, mfee Uint64, treasury String) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM s, t as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF STRLEN(name) == 0 || STRLEN(name) > 64 || STRLEN(descr) > 240 || STRLEN(icon) > 200 || STRLEN(file) > 200 THEN GOTO 900
23 IF maxsupply == 0 || maxsupply > 100000 || price > 10000000000000 || reserve > maxsupply / 5 THEN GOTO 900
24 IF share > 10000 || mfee > 1000 THEN GOTO 900
25 LET t = ADDRESS_RAW(treasury)
26 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
30 STORE("nameHdr", name)
31 STORE("descrHdr", descr)
32 STORE("iconURLHdr", icon)
33 STORE("fileURL", file)
34 STORE("typeHdr", "Image")
35 STORE("kind", "founders")
36 STORE("v", 1)
37 STORE("creator", s)
38 STORE("treasury", t)
39 STORE("price", price)
40 STORE("maxsupply", maxsupply)
41 STORE("start", start)
42 STORE("reserve", reserve)
43 STORE("share", share)
44 STORE("mfee", mfee)
45 STORE("minted", reserve)
46 STORE("hst", 0)
47 STORE("acc", 0)
48 STORE("rpool", 0)
49 STORE("und", 0)
50 STORE("dep", 0)
51 STORE("mdep", 0)
52 STORE("paid", 0)
53 STORE("hn", 0)
54 STORE("ln", 0)
55 STORE("la", 0)
56 STORE("era", 0)
57 STORE("minhst", 1 + maxsupply / 50)
59 STORE("initd", 1)
60 IF reserve == 0 THEN GOTO 100
61 SEND_ASSET_TO_ADDRESS(s, reserve, SCID())
100 RETURN 0
900 RETURN 1
End Function

// Mint n editions for exactly n × price DERO (proceeds go to the treasury). Opens at block `start`.
Function Mint(n Uint64) Uint64
10 DIM s as String
11 DIM m, cost as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF BLOCK_HEIGHT() < LOAD("start") THEN GOTO 900
23 LET m = LOAD("minted")
24 IF n == 0 || n > LOAD("maxsupply") - m THEN GOTO 900
25 LET cost = n * LOAD("price")
26 IF DEROVALUE() != cost THEN GOTO 900
30 STORE("minted", m + n)
31 SEND_ASSET_TO_ADDRESS(s, n, SCID())
32 IF cost == 0 THEN GOTO 100
33 SEND_DERO_TO_ADDRESS(LOAD("treasury"), cost)
100 RETURN 0
900 RETURN 1
End Function

// Register the passes sent with this call so they earn. Pending rewards are settled first.
Function Register() Uint64
10 DIM s, a as String
11 DIM u, hn, hu as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 LET u = ASSETVALUE(SCID())
23 IF u == 0 || DEROVALUE() != 0 THEN GOTO 900
24 LET a = ADDRESS_STRING(s)
30 distribute(0)
31 settle(s, a)
32 LET hu = 0
33 IF EXISTS("hi_" + a) THEN GOTO 40
34 LET hn = LOAD("hn")
35 STORE("h_" + hn, a)
36 STORE("hi_" + a, hn + 1)
37 STORE("hn", hn + 1)
38 GOTO 41
40 LET hu = LOAD("hu_" + a)
41 STORE("hu_" + a, hu + u)
42 STORE("hd_" + a, LOAD("acc"))
43 STORE("he_" + a, LOAD("era"))
44 STORE("hst", LOAD("hst") + u)
45 distribute(0)
100 RETURN 0
900 RETURN 1
End Function

// Withdraw u registered passes back to the wallet (rewards settled first). Leaving the registry compacts it.
Function Withdraw(u Uint64) Uint64
10 DIM s, a as String
11 DIM hu as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 LET a = ADDRESS_STRING(s)
23 IF EXISTS("hi_" + a) == 0 THEN GOTO 900
24 LET hu = LOAD("hu_" + a)
25 IF u == 0 || u > hu THEN GOTO 900
30 distribute(0)
31 settle(s, a)
32 STORE("hst", LOAD("hst") - u)
33 SEND_ASSET_TO_ADDRESS(s, u, SCID())
34 IF u < hu THEN GOTO 40
35 remove(a)
36 GOTO 100
40 STORE("hu_" + a, hu - u)
100 RETURN 0
900 RETURN 1
End Function

Function Claim() Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
30 distribute(0)
31 settle(s, ADDRESS_STRING(s))
100 RETURN 0
900 RETURN 1
End Function

// Anyone can deposit DERO for the holders (the treasury does, with its promised share of the fees).
Function Deposit() Uint64
10 DIM d as Uint64
20 LET d = DEROVALUE()
21 IF d == 0 THEN GOTO 900
22 IF LOAD("dep") + d < d THEN GOTO 900
30 STORE("dep", LOAD("dep") + d)
31 distribute(d)
100 RETURN 0
900 RETURN 1
End Function

// Moves DERO that arrived while nobody was registered into the accumulator. Anyone.
Function Distribute() Uint64
10 IF DEROVALUE() != 0 THEN GOTO 900
20 distribute(0)
100 RETURN 0
900 RETURN 1
End Function

// Pushes pending rewards to registered holders from..from+n-1 (n ≤ 15: each settle costs ≈ 0.41M of the 10M
// compute gas a call has). Anyone.
Function PayBatch(from Uint64, n Uint64) Uint64
10 DIM i, end, hn as Uint64
11 DIM a as String
20 LET hn = LOAD("hn")
21 IF n == 0 || n > 15 || from >= hn || DEROVALUE() != 0 THEN GOTO 900
22 LET end = from + n
23 IF end <= hn THEN GOTO 30
24 LET end = hn
30 distribute(0)
31 LET i = from
40 IF i >= end THEN GOTO 100
41 LET a = LOAD("h_" + i)
42 settle(ADDRESS_RAW(a), a)
43 LET i = i + 1
44 GOTO 40
100 RETURN 0
900 RETURN 1
End Function

// Marketplace: deposit passes with an ask price per unit.
Function List(price Uint64) Uint64
10 DIM s as String
11 DIM u, id as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 LET u = ASSETVALUE(SCID())
23 IF u == 0 || price == 0 || price > 10000000000000 THEN GOTO 900
24 LET id = LOAD("ln")
30 STORE("ls_" + id, ADDRESS_STRING(s))
31 STORE("lp_" + id, price)
32 STORE("lu_" + id, u)
33 STORE("ln", id + 1)
34 STORE("la", LOAD("la") + 1)
100 RETURN 0
900 RETURN 1
End Function

Function Unlist(id Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("ls_" + id) == 0 THEN GOTO 900
23 IF LOAD("ls_" + id) != ADDRESS_STRING(s) THEN GOTO 900
30 SEND_ASSET_TO_ADDRESS(s, LOAD("lu_" + id), SCID())
31 DELETE("ls_" + id)
32 DELETE("lp_" + id)
33 DELETE("lu_" + id)
34 STORE("la", LOAD("la") - 1)
100 RETURN 0
900 RETURN 1
End Function

// Buy n units of listing id for exactly n × price. mfee bps of the price goes to the holders, the rest to the seller.
Function Buy(id Uint64, n Uint64) Uint64
10 DIM s as String
11 DIM lu, cost, fee as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF EXISTS("ls_" + id) == 0 THEN GOTO 900
23 LET lu = LOAD("lu_" + id)
24 IF n == 0 || n > lu THEN GOTO 900
25 LET cost = n * LOAD("lp_" + id)
26 IF DEROVALUE() != cost THEN GOTO 900
27 LET fee = bps(cost, LOAD("mfee"))
30 SEND_ASSET_TO_ADDRESS(s, n, SCID())
31 SEND_DERO_TO_ADDRESS(ADDRESS_RAW(LOAD("ls_" + id)), cost - fee)
32 STORE("mdep", LOAD("mdep") + fee)
33 distribute(fee)
34 IF n < lu THEN GOTO 40
35 DELETE("ls_" + id)
36 DELETE("lp_" + id)
37 DELETE("lu_" + id)
38 STORE("la", LOAD("la") - 1)
39 GOTO 100
40 STORE("lu_" + id, lu - n)
100 RETURN 0
900 RETURN 1
End Function

// ---- internals ----
// distribute(x): pool = und + x. With no registered units it waits in und; otherwise acc += floor(pool·2^32/hst) and
// ceil(inc·hst/2^32) ≤ pool moves to rpool, so the same residual is never credited twice (what stays in und is
// strictly less than one atomic unit per registered unit).
Function distribute(x Uint64) Uint64
10 DIM pool, hst, inc, acc, covered as Uint64
20 LET pool = LOAD("und") + x
21 LET hst = LOAD("hst")
22 IF pool == 0 THEN GOTO 100
23 IF hst < LOAD("minhst") THEN GOTO 90
24 IF mulhi(pool, 4294967296) < hst THEN GOTO 30
25 LET pool = hst * 4294967295
30 LET inc = muldiv(pool, 4294967296, hst)
31 LET acc = LOAD("acc")
32 IF acc + inc >= acc THEN GOTO 40
33 STORE("ea_" + LOAD("era"), acc)
34 STORE("era", LOAD("era") + 1)
35 LET acc = 0
40 LET covered = muldivup(inc, hst, 4294967296)
41 STORE("acc", acc + inc)
42 STORE("rpool", LOAD("rpool") + covered)
43 STORE("und", LOAD("und") + x - covered)
44 GOTO 100
90 STORE("und", LOAD("und") + x)
100 RETURN 0
End Function
Function settle(s String, a String) Uint64
10 DIM hu, hd, acc, pend, e, era as Uint64
20 IF EXISTS("hi_" + a) == 0 THEN GOTO 100
21 LET hu = LOAD("hu_" + a)
22 LET hd = LOAD("hd_" + a)
23 LET e = LOAD("he_" + a)
24 LET era = LOAD("era")
25 LET acc = LOAD("acc")
26 LET pend = 0
30 IF e >= era THEN GOTO 40
31 LET pend = pend + muldiv(hu, LOAD("ea_" + e) - hd, 4294967296)
32 LET hd = 0
33 LET e = e + 1
34 GOTO 30
40 LET pend = pend + muldiv(hu, acc - hd, 4294967296)
41 STORE("hd_" + a, acc)
42 STORE("he_" + a, era)
43 IF pend == 0 THEN GOTO 100
44 IF pend <= LOAD("rpool") THEN GOTO 50
45 LET pend = LOAD("rpool")
46 IF pend == 0 THEN GOTO 100
50 STORE("rpool", LOAD("rpool") - pend)
51 STORE("paid", LOAD("paid") + pend)
52 SEND_DERO_TO_ADDRESS(s, pend)
100 RETURN 0
End Function
Function remove(a String) Uint64
10 DIM idx, last as Uint64
11 DIM b as String
20 LET idx = LOAD("hi_" + a) - 1
21 LET last = LOAD("hn") - 1
22 IF idx == last THEN GOTO 30
23 LET b = LOAD("h_" + last)
24 STORE("h_" + idx, b)
25 STORE("hi_" + b, idx + 1)
30 DELETE("h_" + last)
31 DELETE("hi_" + a)
32 DELETE("hu_" + a)
33 DELETE("hd_" + a)
34 DELETE("he_" + a)
35 STORE("hn", last)
100 RETURN 0
End Function
Function bps(x Uint64, f Uint64) Uint64
10 RETURN (x / 10000) * f + ((x % 10000) * f) / 10000
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
Function shl(x Uint64, s Uint64) Uint64
10 RETURN x << s
End Function
Function shr(x Uint64, s Uint64) Uint64
10 RETURN x >> s
End Function
