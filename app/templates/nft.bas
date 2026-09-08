Function InitializePrivate(name String, descr String, icon String, file String, tags String, collection String, token String, minhold Uint64, holdblocks Uint64, maxsupply Uint64, price Uint64, treasury String, fee Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM s, t as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF STRLEN(name) == 0 || STRLEN(name) > 64 || STRLEN(descr) > 240 || STRLEN(icon) > 200 || STRLEN(file) > 200 THEN GOTO 900
23 IF STRLEN(tags) > 100 || STRLEN(collection) > 64 || STRLEN(token) != 32 THEN GOTO 900
24 IF minhold == 0 || maxsupply == 0 || holdblocks == 0 || holdblocks > 10000000 || fee > 100 THEN GOTO 900
25 LET t = ADDRESS_RAW(treasury)
26 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
30 STORE("nameHdr", name)
31 STORE("descrHdr", descr)
32 STORE("typeHdr", "Image")
33 STORE("iconURLHdr", icon)
34 STORE("fileURL", file)
35 STORE("tagsHdr", tags)
36 STORE("collection", collection)
37 STORE("kind", "nft")
38 STORE("v", 1)
39 STORE("creator", s)
40 STORE("treasury", t)
41 STORE("token", token)
42 STORE("minhold", minhold)
43 STORE("holdblocks", holdblocks)
44 STORE("maxsupply", maxsupply)
45 STORE("price", price)
46 STORE("fee", fee)
47 STORE("minted", 0)
48 STORE("fd", 0)
49 STORE("cacc", 0)
55 STORE("initd", 1)
60 RETURN 0
900 RETURN 1
End Function
Function Lock() Uint64
10 DIM s, a as String
11 DIM v as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 LET v = ASSETVALUE(LOAD("token"))
23 IF v < LOAD("minhold") || DEROVALUE() != 0 THEN GOTO 900
24 LET a = ADDRESS_STRING(s)
25 IF EXISTS("lk_" + a) THEN GOTO 900
30 STORE("lk_" + a, v)
31 STORE("lh_" + a, BLOCK_HEIGHT())
32 STORE("lm_" + a, 0)
40 RETURN 0
900 RETURN 1
End Function
Function Mint() Uint64
10 DIM s, a as String
11 DIM d, price, fee, n as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 LET a = ADDRESS_STRING(s)
23 IF EXISTS("lk_" + a) == 0 THEN GOTO 900
24 IF LOAD("lm_" + a) != 0 || EXISTS("mt_" + a) THEN GOTO 900
25 IF BLOCK_HEIGHT() < LOAD("lh_" + a) + LOAD("holdblocks") THEN GOTO 900
26 LET n = LOAD("minted")
27 IF n >= LOAD("maxsupply") THEN GOTO 900
28 LET d = DEROVALUE()
29 LET price = LOAD("price")
30 IF d < price THEN GOTO 900
31 IF d == price THEN GOTO 33
32 SEND_DERO_TO_ADDRESS(s, d - price)
33 LET fee = bps(price, LOAD("fee"))
34 STORE("fd", LOAD("fd") + fee)
35 STORE("cacc", LOAD("cacc") + price - fee)
36 STORE("lm_" + a, 1)
37 STORE("mt_" + a, 1)
38 STORE("minted", n + 1)
39 STORE("ed_" + n, a)
40 SEND_ASSET_TO_ADDRESS(s, 1, SCID())
41 RETURN 0
900 RETURN 1
End Function
Function Unlock() Uint64
10 DIM s, a as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 LET a = ADDRESS_STRING(s)
23 IF EXISTS("lk_" + a) == 0 THEN GOTO 900
30 SEND_ASSET_TO_ADDRESS(s, LOAD("lk_" + a), LOAD("token"))
31 DELETE("lk_" + a)
32 DELETE("lh_" + a)
33 DELETE("lm_" + a)
40 RETURN 0
900 RETURN 1
End Function
Function ClaimCreator() Uint64
10 DIM s as String
11 DIM x as Uint64
20 LET s = SIGNER()
21 IF s != LOAD("creator") THEN GOTO 900
30 LET x = LOAD("cacc")
31 STORE("cacc", 0)
32 SEND_DERO_TO_ADDRESS(s, x)
40 RETURN 0
900 RETURN 1
End Function
Function ClaimFees() Uint64
10 DIM t as String
11 DIM x as Uint64
20 LET t = LOAD("treasury")
30 LET x = LOAD("fd")
31 IF x == 0 THEN GOTO 900
32 STORE("fd", 0)
33 SEND_DERO_TO_ADDRESS(t, x)
40 RETURN 0
900 RETURN 1
End Function
Function bps(x Uint64, f Uint64) Uint64
10 RETURN (x / 10000) * f + ((x % 10000) * f) / 10000
End Function
