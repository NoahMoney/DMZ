Function InitializePrivate(slots Uint64, epoch Uint64, minbid Uint64, incbps Uint64, treasury String, quote String) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM t, q as String
20 IF slots == 0 || slots > 10 || epoch < 20 || epoch > 1000000 THEN GOTO 900
21 IF minbid == 0 || minbid > 10000000000000 || incbps == 0 || incbps > 10000 THEN GOTO 900
22 LET t = ADDRESS_RAW(treasury)
23 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
24 LET q = DERO()
25 IF STRLEN(quote) == 0 THEN GOTO 28
26 IF STRLEN(quote) != 64 THEN GOTO 900
27 LET q = HEXDECODE(quote)
28 IF STRLEN(q) != 32 || q == SCID() || DEROVALUE() != 0 || ASSETVALUE(q) != 0 THEN GOTO 900
30 STORE("kind", "promo")
31 STORE("slots", slots)
32 STORE("epoch", epoch)
33 STORE("minbid", minbid)
34 STORE("incbps", incbps)
35 STORE("treasury", t)
36 STORE("quote", q)
37 STORE("sq", 0)
38 STORE("pr", 0)
39 STORE("fc", 0)
40 STORE("bi", 0)
41 STORE("rf", 0)
42 STORE("sx", 0)
50 STORE("initd", 1)
100 RETURN 0
900 RETURN 1
End Function
Function Bid(target String, ep Uint64, raise Uint64) Uint64
10 DIM s, x, q as String
11 DIM v, k, cur, b, j, i, fr, own, lo, lb, lq, need as Uint64
20 LET q = LOAD("quote")
21 LET v = ASSETVALUE(q)
22 LET s = SIGNER()
23 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 800
24 IF v == 0 || STRLEN(target) != 32 || raise > 1 || (q != DERO() && DEROVALUE() != 0) THEN GOTO 900
25 LET k = LOAD("slots")
26 LET cur = BLOCK_HEIGHT() / LOAD("epoch")
27 IF ep != cur THEN GOTO 900
28 LET b = (cur % 2) * k
29 LET fr = k
30 LET own = k
31 LET lo = k
32 LET j = 0
40 IF j >= k THEN GOTO 60
41 LET i = b + j
42 IF EXISTS("ep_" + i) == 0 THEN GOTO 53
43 IF LOAD("ep_" + i) != cur THEN GOTO 53
44 IF LOAD("by_" + i) != s || LOAD("tg_" + i) != target THEN GOTO 46
45 LET own = j
46 IF lo == k THEN GOTO 49
47 IF LOAD("bd_" + i) > lb THEN GOTO 56
48 IF LOAD("bd_" + i) == lb && LOAD("sq_" + i) < lq THEN GOTO 56
49 LET lo = j
50 LET lb = LOAD("bd_" + i)
51 LET lq = LOAD("sq_" + i)
52 GOTO 56
53 IF fr != k THEN GOTO 56
54 LET fr = j
56 LET j = j + 1
57 GOTO 40
60 IF raise == 0 THEN GOTO 70
61 IF own == k THEN GOTO 900
62 LET i = b + own
63 IF LOAD("bd_" + i) + v < v THEN GOTO 900
64 STORE("bd_" + i, LOAD("bd_" + i) + v)
65 STORE("bi", LOAD("bi") + v)
66 RETURN 0
70 IF own != k || v < LOAD("minbid") THEN GOTO 900
71 IF fr == k THEN GOTO 80
72 LET i = b + fr
73 IF EXISTS("cl_" + i) == 0 THEN GOTO 77
74 IF LOAD("cl_" + i) != 0 THEN GOTO 77
75 STORE("pr", LOAD("pr") + LOAD("bd_" + i))
77 put(i, target, v, s, cur)
78 RETURN 0
80 LET need = lb + bps(lb, LOAD("incbps"))
81 IF need > lb THEN GOTO 83
82 LET need = lb + 1
83 IF v < need || need < lb THEN GOTO 900
84 LET i = b + lo
85 LET x = LOAD("by_" + i)
86 STORE("rf", LOAD("rf") + lb)
87 put(i, target, v, s, cur)
88 SEND_ASSET_TO_ADDRESS(x, lb, q)
89 RETURN 0
800 IF v == 0 THEN GOTO 900
801 STORE("pr", LOAD("pr") + v)
802 STORE("sx", LOAD("sx") + v)
803 RETURN 0
900 RETURN 1
End Function
Function ClaimProceeds() Uint64
10 DIM cur, n, i, a, v as Uint64
11 DIM q as String
20 LET q = LOAD("quote")
21 LET v = ASSETVALUE(q)
22 IF IS_ADDRESS_VALID(SIGNER()) == 1 && (v != 0 || DEROVALUE() != 0) THEN GOTO 900
23 LET a = LOAD("pr") + v
24 IF v == 0 THEN GOTO 26
25 STORE("sx", LOAD("sx") + v)
26 LET cur = BLOCK_HEIGHT() / LOAD("epoch")
27 LET n = LOAD("slots") * 2
28 LET i = 0
30 IF i >= n THEN GOTO 50
31 IF EXISTS("ep_" + i) == 0 THEN GOTO 40
32 IF LOAD("ep_" + i) >= cur || LOAD("cl_" + i) != 0 THEN GOTO 40
33 LET a = a + LOAD("bd_" + i)
34 STORE("cl_" + i, 1)
40 LET i = i + 1
41 GOTO 30
50 IF a == 0 THEN GOTO 900
51 STORE("pr", 0)
52 STORE("fc", LOAD("fc") + a)
53 SEND_ASSET_TO_ADDRESS(LOAD("treasury"), a, q)
100 RETURN 0
900 RETURN 1
End Function
Function put(i Uint64, t String, v Uint64, s String, e Uint64) Uint64
10 DIM q as Uint64
20 LET q = LOAD("sq")
21 STORE("sq", q + 1)
22 STORE("tg_" + i, t)
23 STORE("bd_" + i, v)
24 STORE("by_" + i, s)
25 STORE("ep_" + i, e)
26 STORE("sq_" + i, q)
27 STORE("cl_" + i, 0)
28 STORE("bi", LOAD("bi") + v)
30 RETURN 0
End Function
Function bps(x Uint64, f Uint64) Uint64
10 RETURN (x / 10000) * f + ((x % 10000) * f) / 10000
End Function
