// Veil vault v2: k-of-n federation wrap contract. A named signer set watches a foreign chain (XMR, ZEC, SOL) and
// mints this contract's own asset one-for-one against deposits it has seen; users burn to withdraw and the signers
// release on the foreign chain. Every state change that matters needs k distinct signer votes checked by SIGNER().
// The signers can steal or freeze what they hold on the foreign chain; this contract makes the DERO side honest.
// v2 (2026-09-06, after the adversarial review): votes are keyed by the full tuple so one signer cannot pin a
// deposit; every vote family carries the slash epoch se so a removed signer's votes stop counting; the active
// signer count na never drops below k; Halt votes name their target state and live in 2000-block buckets; cap
// votes carry a cap epoch; the cap is bounded by 3 x total bonds; bonds are posted before sealing with a minimum;
// the recipient of a mint is the registered index's address (no address round-trip); withdrawals carry a fee and
// a minimum; sup cannot underflow. Not yet in v2 (tracked): encrypted destinations, rotation/unbonding, rate
// limits and withdrawal delay, published viewing keys.
// Keys: kind v k n na se ce he sealed halted owner treasury cap sup un wn bondtot minbond minburn wfee feepool
//       sg_<i> signer raw   isg_<raw> 1 active   bond_<raw>
//       users: ui_<raw> index  ua_<j> raw   da_<j> attested address  dver_<j>  dc_<se>_<j>_<ver>_<ah> dv_<se>_<j>_<ver>_<ah>_<raw>  (ah = sha256 hex of addr)
//       mint id (foreign txid[:vout], 64..80 chars): mc_<se>_<id>_<th> votes for tuple th = sha256 hex of "<j>|<amount>";
//             mv_<se>_<id>_<raw> voted; md_<id> minted (once, ever); mh_<id> height; mj_<id> index; ma_<id> amount
//       withdrawal j: w_<j>_to dest  w_<j>_v net amount  w_<j>_by raw  w_<j>_h height  w_<j>_st 0 pending / 1 released  w_<j>_ftx
//       release votes: rc_<se>_<j>_<ftx>  rv_<se>_<j>_<raw>   halt votes: hc_<he>_<want>_<hb>  hv_<he>_<want>_<hb>_<raw> (hb = height/2000)
//       cap votes: cc_<se>_<ce>_<cap>  cv_<se>_<ce>_<cap>_<raw>   slash votes: sc_<se>_<t>  sv_<se>_<t>_<raw>

Function InitializePrivate(k Uint64, treasury String, cap Uint64, minbond Uint64, minburn Uint64, wfee Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM s, t as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF k < 2 || k > 20 || cap == 0 || cap > 9223372036854775807 THEN GOTO 900
23 LET t = ADDRESS_RAW(treasury)
24 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
25 IF minbond == 0 || minburn == 0 || wfee >= minburn THEN GOTO 900
30 STORE("kind", "vault")
31 STORE("v", 2)
32 STORE("k", k)
33 STORE("n", 0)
34 STORE("na", 0)
35 STORE("se", 0)
36 STORE("ce", 0)
37 STORE("he", 0)
38 STORE("sealed", 0)
39 STORE("halted", 0)
40 STORE("owner", s)
41 STORE("treasury", t)
42 STORE("cap", cap)
43 STORE("sup", 0)
44 STORE("un", 0)
45 STORE("wn", 0)
46 STORE("bondtot", 0)
47 STORE("minbond", minbond)
48 STORE("minburn", minburn)
49 STORE("wfee", wfee)
50 STORE("feepool", 0)
51 STORE("maxdep", 0)
52 STORE("winmax", 0)
53 STORE("wdelay", 0)
99 STORE("initd", 1)
100 RETURN 0
900 RETURN 1
End Function

// Owner adds signers one by one before sealing. After Seal() the set is fixed (rotation is a v3 item).
Function SetLimits(maxdep Uint64, winmax Uint64, wdelay Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || s != LOAD("owner") || LOAD("sealed") != 0 THEN GOTO 900
22 IF wdelay > 100000 THEN GOTO 900
30 STORE("maxdep", maxdep)
31 STORE("winmax", winmax)
32 STORE("wdelay", wdelay)
100 RETURN 0
900 RETURN 1
End Function

Function AddSigner(addr String) Uint64
10 DIM s, r as String
11 DIM n as Uint64
20 LET s = SIGNER()
21 IF s != LOAD("owner") THEN GOTO 900
22 IF LOAD("sealed") != 0 THEN GOTO 900
23 LET r = ADDRESS_RAW(addr)
24 IF IS_ADDRESS_VALID(r) == 0 THEN GOTO 900
25 IF EXISTS("isg_" + r) != 0 THEN GOTO 900
26 LET n = LOAD("n")
27 IF n >= 20 THEN GOTO 900
30 STORE("sg_" + n, r)
31 STORE("isg_" + r, 1)
32 STORE("n", n + 1)
100 RETURN 0
900 RETURN 1
End Function

// Every signer must hold at least minbond before the set can be sealed; the cap must respect the bond ratio.
Function Seal() Uint64
10 DIM s as String
11 DIM i, n as Uint64
20 LET s = SIGNER()
21 IF s != LOAD("owner") THEN GOTO 900
22 IF LOAD("sealed") != 0 THEN GOTO 900
23 LET n = LOAD("n")
24 IF n < LOAD("k") THEN GOTO 900
25 LET i = 0
30 IF i >= n THEN GOTO 40
31 IF EXISTS("bond_" + LOAD("sg_" + i)) == 0 THEN GOTO 900
32 IF LOAD("bond_" + LOAD("sg_" + i)) < LOAD("minbond") THEN GOTO 900
33 LET i = i + 1
34 GOTO 30
40 IF LOAD("cap") > 3 * LOAD("bondtot") THEN GOTO 900
41 STORE("na", n)
42 STORE("sealed", 1)
100 RETURN 0
900 RETURN 1
End Function

// A user claims a deposit index; the signers derive that user's foreign deposit address from it and attest it.
Function Register() Uint64
10 DIM s as String
11 DIM j as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF LOAD("sealed") == 0 THEN GOTO 900
23 IF EXISTS("ui_" + s) != 0 THEN GOTO 900
24 LET j = LOAD("un")
30 STORE("ui_" + s, j)
31 STORE("ua_" + j, s)
32 STORE("un", j + 1)
100 RETURN 0
900 RETURN 1
End Function

// k signers attest the foreign deposit address for user index j; re-attestation bumps dver_<j> and needs k votes
// again, so a wrong address can be corrected and the history stays on chain.
Function Attest(j Uint64, addr String) Uint64
10 DIM s, ah, kb as String
11 DIM c, ver, se as Uint64
20 LET s = SIGNER()
21 IF EXISTS("isg_" + s) == 0 THEN GOTO 900
22 IF LOAD("isg_" + s) == 0 || DEROVALUE() != 0 THEN GOTO 900
23 IF EXISTS("ua_" + j) == 0 || LOAD("halted") != 0 THEN GOTO 900
24 IF STRLEN(addr) == 0 || STRLEN(addr) > 200 THEN GOTO 900
25 LET ver = 0
26 IF EXISTS("dver_" + j) == 0 THEN GOTO 30
27 LET ver = LOAD("dver_" + j)
30 LET se = LOAD("se")
31 LET ah = HEX(SHA256(addr))
32 LET kb = "" + se + "_" + j + "_" + ver + "_" + ah
33 IF EXISTS("dv_" + kb + "_" + s) != 0 THEN GOTO 900
34 LET c = 0
35 IF EXISTS("dc_" + kb) == 0 THEN GOTO 40
36 LET c = LOAD("dc_" + kb)
40 LET c = c + 1
41 STORE("dc_" + kb, c)
42 STORE("dv_" + kb + "_" + s, 1)
43 IF c < LOAD("k") THEN GOTO 100
50 STORE("da_" + j, addr)
51 STORE("dver_" + j, ver + 1)
100 RETURN 0
900 RETURN 1
End Function

// A signer posts DERO as a bond before the set is sealed (and may top up later). No unbonding in v2 on purpose.
Function Bond() Uint64
10 DIM s as String
11 DIM b, v as Uint64
20 LET s = SIGNER()
21 IF EXISTS("isg_" + s) == 0 THEN GOTO 900
22 IF LOAD("isg_" + s) == 0 THEN GOTO 900
23 LET v = DEROVALUE()
24 IF v == 0 THEN GOTO 900
25 LET b = 0
26 IF EXISTS("bond_" + s) == 0 THEN GOTO 30
27 LET b = LOAD("bond_" + s)
30 IF b + v < b THEN GOTO 900
31 STORE("bond_" + s, b + v)
32 STORE("bondtot", LOAD("bondtot") + v)
100 RETURN 0
900 RETURN 1
End Function

// Vote to mint amount wrapped units to the address registered under index j for foreign deposit id
// (txid, optionally ":vout"; 64..80 characters). Votes are counted per tuple (j, amount): the first tuple to reach
// k mints, so a single wrong vote can neither block nor redirect a deposit. One vote per signer per id per slash
// epoch. An id is minted at most once, ever, and only within cap and the bond ratio.
Function Mint(j Uint64, id String, amount Uint64) Uint64
10 DIM s, r, th, se, wb, pb as String
11 DIM c, sup, wm, pm as Uint64
20 LET s = SIGNER()
21 IF EXISTS("isg_" + s) == 0 THEN GOTO 900
22 IF LOAD("isg_" + s) == 0 || LOAD("sealed") == 0 || LOAD("halted") != 0 THEN GOTO 900
23 IF DEROVALUE() != 0 THEN GOTO 900
24 IF STRLEN(id) < 64 || STRLEN(id) > 80 || amount == 0 THEN GOTO 900
25 IF EXISTS("md_" + id) != 0 THEN GOTO 900
26 IF EXISTS("ua_" + j) == 0 THEN GOTO 900
27 LET se = "" + LOAD("se")
28 IF EXISTS("mv_" + se + "_" + id + "_" + s) != 0 THEN GOTO 900
30 LET th = HEX(SHA256("" + j + "|" + amount))
31 LET c = 0
32 IF EXISTS("mc_" + se + "_" + id + "_" + th) == 0 THEN GOTO 40
33 LET c = LOAD("mc_" + se + "_" + id + "_" + th)
40 LET c = c + 1
41 STORE("mc_" + se + "_" + id + "_" + th, c)
42 STORE("mv_" + se + "_" + id + "_" + s, 1)
43 IF c < LOAD("k") THEN GOTO 100
50 LET sup = LOAD("sup")
51 IF sup + amount < sup || sup + amount > LOAD("cap") THEN GOTO 900
52 IF LOAD("cap") > 3 * LOAD("bondtot") THEN GOTO 900
53 IF LOAD("maxdep") != 0 && amount > LOAD("maxdep") THEN GOTO 900
54 LET wb = "" + (BLOCK_HEIGHT() / 2000)
55 LET wm = 0
56 IF EXISTS("mw_" + wb) == 0 THEN GOTO 58
57 LET wm = LOAD("mw_" + wb)
58 LET pm = 0
59 IF BLOCK_HEIGHT() < 2000 THEN GOTO 63
60 LET pb = "" + (BLOCK_HEIGHT() / 2000 - 1)
61 IF EXISTS("mw_" + pb) == 0 THEN GOTO 63
62 LET pm = LOAD("mw_" + pb)
63 IF LOAD("winmax") != 0 && wm + pm + amount > LOAD("winmax") THEN GOTO 900
64 STORE("mw_" + wb, wm + amount)
65 LET r = LOAD("ua_" + j)
66 STORE("sup", sup + amount)
67 STORE("md_" + id, 1)
68 STORE("mh_" + id, BLOCK_HEIGHT())
69 STORE("mj_" + id, j)
70 STORE("ma_" + id, amount)
71 SEND_ASSET_TO_ADDRESS(r, amount, SCID())
100 RETURN 0
900 RETURN 1
End Function

// Burn wrapped units to withdraw: the units sent with the call stay in the contract for good; wfee is kept as the
// federation fee and the net amount is what the signers release to dest.
Function Burn(dest String) Uint64
10 DIM s as String
11 DIM v, j, f as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF LOAD("halted") != 0 THEN GOTO 900
23 LET v = ASSETVALUE(SCID())
24 IF v < LOAD("minburn") || DEROVALUE() != 0 THEN GOTO 900
25 IF STRLEN(dest) == 0 || STRLEN(dest) > 200 THEN GOTO 900
26 IF v > LOAD("sup") THEN GOTO 900
27 LET f = LOAD("wfee")
30 LET j = LOAD("wn")
31 STORE("w_" + j + "_to", dest)
32 STORE("w_" + j + "_v", v - f)
33 STORE("w_" + j + "_by", s)
34 STORE("w_" + j + "_h", BLOCK_HEIGHT())
35 STORE("w_" + j + "_st", 0)
36 STORE("wn", j + 1)
37 STORE("sup", LOAD("sup") - v)
38 STORE("feepool", LOAD("feepool") + f)
100 RETURN 0
900 RETURN 1
End Function

// Signers record (k votes on the same foreign txid) that withdrawal j was paid.
Function Released(j Uint64, ftx String) Uint64
10 DIM s, se as String
11 DIM c as Uint64
20 LET s = SIGNER()
21 IF EXISTS("isg_" + s) == 0 THEN GOTO 900
22 IF LOAD("isg_" + s) == 0 || DEROVALUE() != 0 || LOAD("halted") != 0 THEN GOTO 900
23 IF EXISTS("w_" + j + "_st") == 0 THEN GOTO 900
24 IF LOAD("w_" + j + "_st") != 0 || BLOCK_HEIGHT() < LOAD("w_" + j + "_h") + LOAD("wdelay") THEN GOTO 900
25 IF STRLEN(ftx) == 0 || STRLEN(ftx) > 128 THEN GOTO 900
26 LET se = "" + LOAD("se")
27 IF EXISTS("rv_" + se + "_" + j + "_" + s) != 0 THEN GOTO 900
30 LET c = 0
31 IF EXISTS("rc_" + se + "_" + j + "_" + ftx) == 0 THEN GOTO 40
32 LET c = LOAD("rc_" + se + "_" + j + "_" + ftx)
40 LET c = c + 1
41 STORE("rc_" + se + "_" + j + "_" + ftx, c)
42 STORE("rv_" + se + "_" + j + "_" + s, 1)
43 IF c < LOAD("k") THEN GOTO 100
50 STORE("w_" + j + "_st", 1)
51 STORE("w_" + j + "_ftx", ftx)
100 RETURN 0
900 RETURN 1
End Function

// k votes for the same target state within the same 2000-block bucket flip halted; each flip starts a new epoch.
Function Halt(want Uint64) Uint64
10 DIM s, kb as String
11 DIM e, c, hb as Uint64
20 LET s = SIGNER()
21 IF EXISTS("isg_" + s) == 0 THEN GOTO 900
22 IF LOAD("isg_" + s) == 0 || DEROVALUE() != 0 || want > 1 || LOAD("sealed") == 0 THEN GOTO 900
23 IF want == LOAD("halted") THEN GOTO 900
24 LET e = LOAD("he")
25 LET hb = BLOCK_HEIGHT() / 2000
26 LET kb = "" + LOAD("se") + "_" + e + "_" + want + "_" + hb
27 IF EXISTS("hv_" + kb + "_" + s) != 0 THEN GOTO 900
30 LET c = 0
31 IF EXISTS("hc_" + kb) == 0 THEN GOTO 40
32 LET c = LOAD("hc_" + kb)
40 LET c = c + 1
41 STORE("hc_" + kb, c)
42 STORE("hv_" + kb + "_" + s, 1)
43 IF c < LOAD("k") THEN GOTO 100
50 STORE("halted", want)
51 STORE("he", e + 1)
100 RETURN 0
900 RETURN 1
End Function

// k votes for the same new cap change it; the cap can never exceed 3 x total bonds; every change opens a new cap
// epoch so old votes never combine with new ones and a value can be voted again later.
Function SetCap(cap Uint64) Uint64
10 DIM s, kb as String
11 DIM c as Uint64
20 LET s = SIGNER()
21 IF EXISTS("isg_" + s) == 0 THEN GOTO 900
22 IF LOAD("isg_" + s) == 0 || DEROVALUE() != 0 || LOAD("sealed") == 0 THEN GOTO 900
23 IF cap == 0 || cap == LOAD("cap") || cap > 9223372036854775807 THEN GOTO 900
24 IF cap > 3 * LOAD("bondtot") THEN GOTO 900
25 LET kb = "" + LOAD("se") + "_" + LOAD("ce") + "_" + cap
26 IF EXISTS("cv_" + kb + "_" + s) != 0 THEN GOTO 900
30 LET c = 0
31 IF EXISTS("cc_" + kb) == 0 THEN GOTO 40
32 LET c = LOAD("cc_" + kb)
40 LET c = c + 1
41 STORE("cc_" + kb, c)
42 STORE("cv_" + kb + "_" + s, 1)
43 IF c < LOAD("k") THEN GOTO 100
50 STORE("cap", cap)
51 STORE("ce", LOAD("ce") + 1)
100 RETURN 0
900 RETURN 1
End Function

// k votes slash a signer: the bond goes to the treasury, the signer is removed, every pending vote of every family
// is voided (new slash epoch), and the active set never drops below k. A signer cannot vote to slash itself.
Function Slash(target String) Uint64
10 DIM s, t, se as String
11 DIM c, b, na as Uint64
20 LET s = SIGNER()
21 IF EXISTS("isg_" + s) == 0 THEN GOTO 900
22 IF LOAD("isg_" + s) == 0 || DEROVALUE() != 0 || LOAD("sealed") == 0 THEN GOTO 900
23 LET t = ADDRESS_RAW(target)
24 IF t == s THEN GOTO 900
25 IF EXISTS("isg_" + t) == 0 THEN GOTO 900
26 IF LOAD("isg_" + t) == 0 THEN GOTO 900
27 LET na = LOAD("na")
28 IF na < LOAD("k") + 1 THEN GOTO 900
29 LET se = "" + LOAD("se")
30 IF EXISTS("sv_" + se + "_" + t + "_" + s) != 0 THEN GOTO 900
31 LET c = 0
32 IF EXISTS("sc_" + se + "_" + t) == 0 THEN GOTO 40
33 LET c = LOAD("sc_" + se + "_" + t)
40 LET c = c + 1
41 STORE("sc_" + se + "_" + t, c)
42 STORE("sv_" + se + "_" + t + "_" + s, 1)
43 IF c < LOAD("k") THEN GOTO 100
50 STORE("isg_" + t, 0)
51 STORE("na", na - 1)
52 STORE("se", LOAD("se") + 1)
53 LET b = 0
54 IF EXISTS("bond_" + t) == 0 THEN GOTO 100
55 LET b = LOAD("bond_" + t)
56 IF b == 0 THEN GOTO 100
57 STORE("bond_" + t, 0)
58 STORE("bondtot", LOAD("bondtot") - b)
59 SEND_DERO_TO_ADDRESS(LOAD("treasury"), b)
100 RETURN 0
900 RETURN 1
End Function
