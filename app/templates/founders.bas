Function InitializePrivate(name String, descr String, icon String, file String, price Uint64, maxsupply Uint64, start Uint64, reserve Uint64, share Uint64, mfee Uint64, treasury String) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM s, t, b as String
11 DIM i as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
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
36 STORE("v", 2)
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
47 STORE("ev", 0)
48 STORE("pn", 0)
49 STORE("an", 0)
50 STORE("ln", 0)
51 STORE("la", 0)
52 STORE("mdep", 0)
53 newa(HEXDECODE("0000000000000000000000000000000000000000000000000000000000000000"))
54 LET b = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
55 LET i = 0
56 STORE("b" + SUBSTR(b, i, 1), 1)
57 LET i = i + 1
58 IF i < 58 THEN GOTO 56
59 STORE("initd", 1)
60 IF reserve == 0 THEN GOTO 100
61 SEND_ASSET_TO_ADDRESS(s, reserve, SCID())
100 RETURN 0
900 RETURN 1
End Function
Function Mint(n Uint64) Uint64
10 DIM s as String
11 DIM m, cost as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
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
Function Register() Uint64
10 DIM s, a as String
11 DIM u, e, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 LET u = ASSETVALUE(SCID())
23 IF u == 0 || DEROVALUE() != 0 THEN GOTO 900
24 LET a = ADDRESS_STRING(s)
30 LET e = LOAD("ev") + 1
31 STORE("ev", e)
32 open(s, u, e)
33 LET h = 0
34 IF EXISTS("hu_" + a) == 0 THEN GOTO 36
35 LET h = LOAD("hu_" + a)
36 STORE("hu_" + a, h + u)
37 STORE("hst", LOAD("hst") + u)
100 RETURN 0
900 RETURN 1
End Function
Function Withdraw(p Uint64, u Uint64) Uint64
10 DIM s, a as String
11 DIM pu, e, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
22 IF EXISTS("pu_" + p) == 0 THEN GOTO 900
23 IF LOAD("po_" + p) != s || LOAD("pc_" + p) != 0 THEN GOTO 900
24 LET pu = LOAD("pu_" + p)
25 IF u == 0 || u > pu THEN GOTO 900
26 LET a = ADDRESS_STRING(s)
30 LET e = LOAD("ev") + 1
31 STORE("ev", e)
32 STORE("pc_" + p, e)
33 IF u == pu THEN GOTO 40
34 open(s, pu - u, e)
40 STORE("hst", LOAD("hst") - u)
41 LET h = LOAD("hu_" + a) - u
42 IF h == 0 THEN GOTO 45
43 STORE("hu_" + a, h)
44 GOTO 50
45 DELETE("hu_" + a)
50 SEND_ASSET_TO_ADDRESS(s, u, SCID())
100 RETURN 0
900 RETURN 1
End Function
Function Claim(p Uint64, i Uint64, ka Uint64, kb Uint64) Uint64
10 DIM x as String
11 DIM ta, tb, c, u, amt as Uint64
20 IF DEROVALUE() != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
21 IF EXISTS("pu_" + p) == 0 || i >= LOAD("an") THEN GOTO 900
22 LET x = LOAD("ah_" + i)
23 IF ASSETVALUE(x) != 0 THEN GOTO 900
24 IF EXISTS("pd_" + i + "_" + p) == 0 THEN GOTO 27
25 LET ta = LOAD("pd_" + i + "_" + p)
26 GOTO 30
27 LET ta = at(i, LOAD("pt_" + p), ka)
30 LET tb = LOAD("ac_" + i)
31 LET c = LOAD("pc_" + p)
32 IF c == 0 THEN GOTO 40
33 LET tb = at(i, c, kb)
40 IF tb <= ta THEN GOTO 900
41 LET u = LOAD("pu_" + p)
42 LET amt = u * (tb - ta)
43 IF amt / u != tb - ta || amt > LOAD("ap_" + i) THEN GOTO 900
50 STORE("pd_" + i + "_" + p, tb)
51 STORE("ap_" + i, LOAD("ap_" + i) - amt)
52 STORE("aw_" + i, LOAD("aw_" + i) + amt)
53 SEND_ASSET_TO_ADDRESS(LOAD("po_" + p), amt, x)
100 RETURN 0
900 RETURN 1
End Function
Function Deposit(asset String) Uint64
10 DIM k as String
11 DIM d, i, h as Uint64
20 LET h = LOAD("hst")
21 IF h == 0 || STRLEN(asset) != 32 THEN GOTO 900
22 IF ASSETVALUE(SCID()) != 0 THEN GOTO 900
23 LET d = ASSETVALUE(asset)
24 IF d == 0 THEN GOTO 900
25 IF asset != LOAD("ah_0") && DEROVALUE() != 0 THEN GOTO 900
30 LET k = "ai_" + HEX(asset)
31 IF EXISTS(k) == 0 THEN GOTO 34
32 LET i = LOAD(k) - 1
33 GOTO 40
34 LET i = newa(asset)
40 IF credit(i, d, h) != 0 THEN GOTO 900
100 RETURN 0
900 RETURN 1
End Function
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
11 DIM u as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
22 IF EXISTS("ls_" + id) == 0 THEN GOTO 900
23 IF LOAD("ls_" + id) != ADDRESS_STRING(s) THEN GOTO 900
24 LET u = LOAD("lu_" + id)
30 DELETE("ls_" + id)
31 DELETE("lp_" + id)
32 DELETE("lu_" + id)
33 STORE("la", LOAD("la") - 1)
34 SEND_ASSET_TO_ADDRESS(s, u, SCID())
100 RETURN 0
900 RETURN 1
End Function
Function Buy(id Uint64, n Uint64) Uint64
10 DIM s, sl as String
11 DIM lu, cost, fee, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
22 IF EXISTS("ls_" + id) == 0 THEN GOTO 900
23 LET lu = LOAD("lu_" + id)
24 IF n == 0 || n > lu THEN GOTO 900
25 LET cost = n * LOAD("lp_" + id)
26 IF DEROVALUE() != cost THEN GOTO 900
27 LET sl = LOAD("ls_" + id)
28 LET fee = 0
29 LET h = LOAD("hst")
30 IF h == 0 THEN GOTO 40
31 LET fee = bps(cost, LOAD("mfee"))
32 IF fee == 0 THEN GOTO 40
33 IF credit(0, fee, h) != 0 THEN GOTO 900
34 STORE("mdep", LOAD("mdep") + fee)
40 IF n < lu THEN GOTO 50
41 DELETE("ls_" + id)
42 DELETE("lp_" + id)
43 DELETE("lu_" + id)
44 STORE("la", LOAD("la") - 1)
45 GOTO 60
50 STORE("lu_" + id, lu - n)
60 SEND_ASSET_TO_ADDRESS(s, n, SCID())
61 SEND_DERO_TO_ADDRESS(ADDRESS_RAW(sl), cost - fee)
100 RETURN 0
900 RETURN 1
End Function
Function SetXmr(addr String) Uint64
10 RETURN setp("xm_", addr, STRLEN(addr) == 95 || STRLEN(addr) == 106)
End Function
Function SetZec(addr String) Uint64
10 RETURN setp("zc_", addr, STRLEN(addr) == 35 && SUBSTR(addr, 0, 1) == "t")
End Function
Function setp(k String, x String, ok Uint64) Uint64
10 DIM s, a as String
11 DIM i, n as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 900
22 LET a = ADDRESS_STRING(s)
23 LET n = STRLEN(x)
24 IF n == 0 THEN GOTO 50
25 IF ok == 0 || EXISTS("hu_" + a) == 0 THEN GOTO 900
26 LET i = 0
30 IF EXISTS("b" + SUBSTR(x, i, 1)) == 0 THEN GOTO 900
31 LET i = i + 1
32 IF i < n THEN GOTO 30
40 STORE(k + a, x)
41 RETURN 0
50 IF EXISTS(k + a) == 0 THEN GOTO 900
51 DELETE(k + a)
52 RETURN 0
900 RETURN 1
End Function
Function credit(i Uint64, d Uint64, h Uint64) Uint64
10 DIM ad, pool, inc, n, e as Uint64
20 LET ad = LOAD("ad_" + i)
21 IF ad + d < ad THEN GOTO 900
22 LET pool = LOAD("au_" + i) + d
23 LET inc = pool / h
24 IF inc == 0 THEN GOTO 50
25 LET n = LOAD("ar_" + i)
26 LET e = LOAD("ev")
27 IF n == 0 THEN GOTO 30
28 IF LOAD("rt_" + i + "_" + (n - 1)) == e THEN GOTO 40
30 STORE("rt_" + i + "_" + n, e)
31 STORE("ra_" + i + "_" + n, LOAD("ac_" + i))
32 STORE("ar_" + i, n + 1)
40 STORE("ac_" + i, LOAD("ac_" + i) + inc)
41 STORE("ap_" + i, LOAD("ap_" + i) + inc * h)
50 STORE("au_" + i, pool - inc * h)
51 STORE("ad_" + i, ad + d)
100 RETURN 0
900 RETURN 1
End Function
Function at(i Uint64, t Uint64, k Uint64) Uint64
10 DIM n as Uint64
20 LET n = LOAD("ar_" + i)
21 IF k > n THEN GOTO 90
22 IF k == 0 THEN GOTO 30
23 IF LOAD("rt_" + i + "_" + (k - 1)) >= t THEN GOTO 90
30 IF k == n THEN GOTO 50
31 IF LOAD("rt_" + i + "_" + k) < t THEN GOTO 90
32 RETURN LOAD("ra_" + i + "_" + k)
50 RETURN LOAD("ac_" + i)
90 PANIC()
91 RETURN 0
End Function
Function newa(a String) Uint64
10 DIM i as Uint64
20 LET i = LOAD("an")
21 STORE("ah_" + i, a)
22 STORE("ai_" + HEX(a), i + 1)
23 STORE("ac_" + i, 0)
24 STORE("au_" + i, 0)
25 STORE("ap_" + i, 0)
26 STORE("ad_" + i, 0)
27 STORE("aw_" + i, 0)
28 STORE("ar_" + i, 0)
29 STORE("an", i + 1)
30 RETURN i
End Function
Function open(s String, u Uint64, e Uint64) Uint64
10 DIM p as Uint64
20 LET p = LOAD("pn")
21 STORE("po_" + p, s)
22 STORE("pu_" + p, u)
23 STORE("pt_" + p, e)
24 STORE("pc_" + p, 0)
25 STORE("pn", p + 1)
30 RETURN p
End Function
Function bps(x Uint64, f Uint64) Uint64
10 RETURN (x / 10000) * f + ((x % 10000) * f) / 10000
End Function
