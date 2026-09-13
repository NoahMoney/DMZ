Function Initialize(w1min Uint64, w2min Uint64, g2 Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
6 IF w1min == 0 || w2min == 0 || g2 == 0 THEN GOTO 900
7 IF w1min > 1000000 || w2min > 1000000 || g2 > 1000000 || w1min + w2min > 999999 THEN GOTO 900
10 STORE("kind", "htlc")
11 STORE("v", 3)
12 STORE("n", 0)
13 STORE("w1min", w1min)
14 STORE("w2min", w2min)
15 STORE("g2", g2)
19 STORE("initd", 1)
20 RETURN 0
900 RETURN 1
End Function
Function Lock(ha String, hk String, hb String, taker String, t1 Uint64, t2 Uint64, asset String) Uint64
10 DIM s, t, a as String
11 DIM v, id, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF STRLEN(ha) != 64 || STRLEN(hk) != 64 || STRLEN(hb) != 64 THEN GOTO 900
23 IF HEX(HEXDECODE(ha)) != ha || HEX(HEXDECODE(hk)) != hk || HEX(HEXDECODE(hb)) != hb THEN GOTO 900
24 LET h = BLOCK_HEIGHT()
25 IF t1 <= h + 1 || t2 <= t1 || t2 > h + 1000000 THEN GOTO 900
26 IF t1 < h + LOAD("w1min") || t2 < t1 + LOAD("w2min") THEN GOTO 900
27 IF STRLEN(taker) == 0 THEN GOTO 900
28 LET t = ADDRESS_RAW(taker)
29 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
30 LET a = ""
31 IF STRLEN(asset) == 0 THEN GOTO 40
32 IF STRLEN(asset) != 64 THEN GOTO 900
33 LET a = HEXDECODE(asset)
34 IF STRLEN(a) != 32 THEN GOTO 900
35 LET v = ASSETVALUE(a)
36 IF v == 0 || DEROVALUE() != 0 THEN GOTO 900
37 GOTO 50
40 LET v = DEROVALUE()
41 IF v == 0 THEN GOTO 900
50 LET id = LOAD("n")
51 STORE("m_" + id, s)
52 STORE("t_" + id, t)
53 STORE("ha_" + id, ha)
54 STORE("hk_" + id, hk)
55 STORE("hb_" + id, hb)
56 STORE("a_" + id, a)
57 STORE("v_" + id, v)
58 STORE("t1_" + id, t1)
59 STORE("t2_" + id, t2)
60 STORE("st_" + id, 0)
61 STORE("n", id + 1)
100 RETURN 0
900 RETURN 1
End Function
Function Claim(id Uint64, sa String, kb String) Uint64
10 DIM s, a, raw as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() >= LOAD("t1_" + id) THEN GOTO 900
25 IF s != LOAD("t_" + id) THEN GOTO 900
26 IF STRLEN(sa) != 64 || STRLEN(kb) != 64 THEN GOTO 900
27 LET raw = HEXDECODE(sa)
28 IF STRLEN(raw) != 32 THEN GOTO 900
29 IF HEX(SHA256(raw)) != LOAD("ha_" + id) THEN GOTO 900
30 LET raw = HEXDECODE(kb)
31 IF STRLEN(raw) != 32 THEN GOTO 900
32 IF HEX(SHA256(raw)) != LOAD("hk_" + id) THEN GOTO 900
33 LET a = LOAD("a_" + id)
34 IF STRLEN(a) == 0 THEN GOTO 40
35 IF ASSETVALUE(a) != 0 THEN GOTO 900
40 STORE("st_" + id, 1)
41 STORE("xa_" + id, sa)
42 IF STRLEN(a) != 0 THEN GOTO 45
43 SEND_DERO_TO_ADDRESS(s, LOAD("v_" + id))
44 GOTO 100
45 SEND_ASSET_TO_ADDRESS(s, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function
Function Refund(id Uint64, sb String) Uint64
10 DIM s, a, raw as String
11 DIM h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 LET h = BLOCK_HEIGHT()
25 IF h < LOAD("t1_" + id) || h >= LOAD("t2_" + id) THEN GOTO 900
26 IF s != LOAD("m_" + id) THEN GOTO 900
27 IF STRLEN(sb) != 64 THEN GOTO 900
28 LET raw = HEXDECODE(sb)
29 IF STRLEN(raw) != 32 THEN GOTO 900
30 IF HEX(SHA256(raw)) != LOAD("hb_" + id) THEN GOTO 900
31 LET a = LOAD("a_" + id)
32 IF STRLEN(a) == 0 THEN GOTO 40
33 IF ASSETVALUE(a) != 0 THEN GOTO 900
40 STORE("st_" + id, 2)
41 STORE("xb_" + id, sb)
42 IF STRLEN(a) != 0 THEN GOTO 45
43 SEND_DERO_TO_ADDRESS(s, LOAD("v_" + id))
44 GOTO 100
45 SEND_ASSET_TO_ADDRESS(s, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function
Function Punish(id Uint64) Uint64
10 DIM s, t, a as String
11 DIM h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 LET h = BLOCK_HEIGHT()
25 IF h < LOAD("t2_" + id) || h >= LOAD("t2_" + id) + LOAD("g2") THEN GOTO 900
26 LET a = LOAD("a_" + id)
27 IF STRLEN(a) == 0 THEN GOTO 30
28 IF ASSETVALUE(a) != 0 THEN GOTO 900
30 STORE("st_" + id, 3)
31 LET t = LOAD("t_" + id)
32 IF STRLEN(a) != 0 THEN GOTO 35
33 SEND_DERO_TO_ADDRESS(t, LOAD("v_" + id))
34 GOTO 100
35 SEND_ASSET_TO_ADDRESS(t, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function
Function Reclaim(id Uint64, sb String) Uint64
10 DIM s, a, raw as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() < LOAD("t2_" + id) + LOAD("g2") THEN GOTO 900
25 IF s != LOAD("m_" + id) THEN GOTO 900
26 IF STRLEN(sb) != 64 THEN GOTO 900
27 LET raw = HEXDECODE(sb)
28 IF STRLEN(raw) != 32 THEN GOTO 900
29 IF HEX(SHA256(raw)) != LOAD("hb_" + id) THEN GOTO 900
30 LET a = LOAD("a_" + id)
31 IF STRLEN(a) == 0 THEN GOTO 40
32 IF ASSETVALUE(a) != 0 THEN GOTO 900
40 STORE("st_" + id, 4)
41 STORE("xb_" + id, sb)
42 IF STRLEN(a) != 0 THEN GOTO 45
43 SEND_DERO_TO_ADDRESS(s, LOAD("v_" + id))
44 GOTO 100
45 SEND_ASSET_TO_ADDRESS(s, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function
