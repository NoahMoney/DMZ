// DMZ vault v3: k-of-n bonded federation wrapping a foreign asset (XMR, ZEC) one for one. Every state change
// needs k signer votes checked by SIGNER(), real only at ring size 2. Invariant: foreign reserve >= sup, always.
// Keys, rules, trust model and deployment rules: contracts/README.md (vault).

// One k-of-n vote. 1 = quorum reached (caller performs the effect once), 0 = counted, 2 = refuse. A signer may
// move a vote; the old choice loses one. <f>d_<kb> closes a scope so no decision executes twice. The vote key
// holds the choice and is named by the signer's ADDRESS_STRING, so a signer daemon can read back exactly what it
// voted rather than inferring it from a counter a co-signer may also have moved.
Function votek(f String, kb String, ch String, s String) Uint64
10 DIM vk, ck, dk, ok as String
11 DIM c as Uint64
20 LET dk = f + "d_" + kb
21 IF EXISTS(dk) != 0 THEN GOTO 900
22 LET vk = f + "v_" + kb + "_" + ADDRESS_STRING(s)
23 LET ck = f + "c_" + kb + "_" + ch
30 IF EXISTS(vk) == 0 THEN GOTO 40
31 IF LOAD(vk) == ch THEN GOTO 900
32 LET ok = f + "c_" + kb + "_" + LOAD(vk)
33 STORE(ok, LOAD(ok) - 1)
40 LET c = 0
41 IF EXISTS(ck) == 0 THEN GOTO 50
42 LET c = LOAD(ck)
50 LET c = c + 1
51 STORE(ck, c)
52 STORE(vk, ch)
60 IF c < LOAD("k") THEN GOTO 100
70 STORE(dk, 1)
71 RETURN 1
100 RETURN 0
900 RETURN 2
End Function

// 1 when s is an active signer. SIGNER() is 33 zero bytes above ring size 2, which is never a signer.
Function issigner(s String) Uint64
10 IF EXISTS("isg_" + s) == 0 THEN GOTO 900
20 IF LOAD("isg_" + s) == 0 THEN GOTO 900
30 RETURN 1
900 RETURN 0
End Function

Function InitializePrivate(k Uint64, treasury String, cap Uint64, minbond Uint64, minburn Uint64, wfee Uint64, mindep Uint64, regfee Uint64, capratio Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM s, t as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF k < 2 || k > 20 || cap == 0 || cap > 9223372036854775807 THEN GOTO 900
23 LET t = ADDRESS_RAW(treasury)
24 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
25 IF minbond == 0 || minburn == 0 || wfee >= minburn || mindep == 0 || capratio == 0 THEN GOTO 900
30 STORE("kind", "vault")
31 STORE("v", 3)
32 STORE("k", k)
33 STORE("n", 0)
34 STORE("na", 0)
35 STORE("se", 0)
36 STORE("ce", 0)
37 STORE("he", 0)
38 STORE("te", 0)
39 STORE("ve", 0)
40 STORE("sealed", 0)
41 STORE("halted", 0)
42 STORE("owner", s)
43 STORE("treasury", t)
44 STORE("cap", cap)
45 STORE("capratio", capratio)
46 STORE("sup", 0)
47 STORE("un", 0)
48 STORE("wn", 0)
49 STORE("bondtot", 0)
50 STORE("minbond", minbond)
51 STORE("minburn", minburn)
52 STORE("wfee", wfee)
53 STORE("mindep", mindep)
54 STORE("regfee", regfee)
55 STORE("feepool", 0)
56 STORE("maxdep", 0)
57 STORE("winmax", 0)
58 STORE("wdelay", 0)
59 STORE("viewkey", "")
99 STORE("initd", 1)
100 RETURN 0
900 RETURN 1
End Function

// Owner sets the outflow limits before sealing; 0 disables one. Afterwards only Tighten can change them.
Function SetLimits(maxdep Uint64, winmax Uint64, wdelay Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || s != LOAD("owner") || LOAD("sealed") != 0 THEN GOTO 900
22 IF wdelay > 20000 THEN GOTO 900
23 IF maxdep != 0 && maxdep < LOAD("mindep") THEN GOTO 900
24 IF winmax != 0 && winmax < maxdep THEN GOTO 900
30 STORE("maxdep", maxdep)
31 STORE("winmax", winmax)
32 STORE("wdelay", wdelay)
100 RETURN 0
900 RETURN 1
End Function

// k votes tighten limits after sealing, safe direction only. The floors stop a quorum bricking the vault.
Function Tighten(maxdep Uint64, winmax Uint64, wdelay Uint64) Uint64
10 DIM s, kb, ch as String
11 DIM r, cur as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("sealed") == 0 THEN GOTO 900
22 IF wdelay > 20000 || wdelay < LOAD("wdelay") THEN GOTO 900
23 IF maxdep == 0 || maxdep < LOAD("mindep") || winmax == 0 || winmax < maxdep THEN GOTO 900
24 LET cur = LOAD("maxdep")
25 IF cur != 0 && maxdep > cur THEN GOTO 900
26 LET cur = LOAD("winmax")
27 IF cur != 0 && winmax > cur THEN GOTO 900
30 LET kb = "" + LOAD("se") + "_" + LOAD("te")
31 LET ch = "" + maxdep + "|" + winmax + "|" + wdelay
32 LET r = votek("t", kb, ch, s)
33 IF r == 2 THEN GOTO 900
34 IF r == 0 THEN GOTO 100
40 STORE("maxdep", maxdep)
41 STORE("winmax", winmax)
42 STORE("wdelay", wdelay)
43 STORE("te", LOAD("te") + 1)
100 RETURN 0
900 RETURN 1
End Function

// k votes publish the foreign viewing key, which is what makes "backed one for one" checkable by anyone.
Function SetViewKey(key String) Uint64
10 DIM s, kb as String
11 DIM r as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("sealed") == 0 THEN GOTO 900
22 IF STRLEN(key) == 0 || STRLEN(key) > 400 THEN GOTO 900
30 LET kb = "" + LOAD("se") + "_" + LOAD("ve")
31 LET r = votek("k", kb, HEX(SHA256(key)), s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("viewkey", key)
41 STORE("ve", LOAD("ve") + 1)
100 RETURN 0
900 RETURN 1
End Function

// Owner adds signers before sealing; afterwards only AddSigner2 (k votes) can.
Function AddSigner(addr String) Uint64
10 DIM s, r as String
11 DIM n as Uint64
20 LET s = SIGNER()
21 IF s != LOAD("owner") || LOAD("sealed") != 0 THEN GOTO 900
22 LET r = ADDRESS_RAW(addr)
23 IF IS_ADDRESS_VALID(r) == 0 THEN GOTO 900
24 IF EXISTS("isg_" + r) != 0 THEN GOTO 900
25 LET n = LOAD("n")
26 IF n >= 20 THEN GOTO 900
30 STORE("sg_" + n, r)
31 STORE("isg_" + r, 1)
32 STORE("n", n + 1)
100 RETURN 0
900 RETURN 1
End Function

// k votes add a signer after sealing (replacing a slashed or lost one). The candidate must hold minbond; a key
// that was ever a signer never returns.
Function AddSigner2(addr String) Uint64
10 DIM s, t, kb as String
11 DIM r, n as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("sealed") == 0 THEN GOTO 900
22 LET t = ADDRESS_RAW(addr)
23 IF IS_ADDRESS_VALID(t) == 0 || EXISTS("isg_" + t) != 0 THEN GOTO 900
24 IF EXISTS("bond_" + t) == 0 THEN GOTO 900
25 IF LOAD("bond_" + t) < LOAD("minbond") THEN GOTO 900
26 LET n = LOAD("n")
27 IF n >= 20 THEN GOTO 900
30 LET kb = "" + LOAD("se")
31 LET r = votek("a", kb, t, s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("sg_" + n, t)
41 STORE("isg_" + t, 1)
42 STORE("n", n + 1)
43 STORE("na", LOAD("na") + 1)
44 STORE("bondtot", LOAD("bondtot") + LOAD("bond_" + t))
45 STORE("se", LOAD("se") + 1)
100 RETURN 0
900 RETURN 1
End Function

// Only an ACTIVE signer's bond counts in bondtot: the cap is bounded by what can actually be slashed.
Function Bond() Uint64
10 DIM s as String
11 DIM b, v as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 LET v = DEROVALUE()
23 IF v == 0 THEN GOTO 900
24 LET b = 0
25 IF EXISTS("bond_" + s) == 0 THEN GOTO 30
26 LET b = LOAD("bond_" + s)
30 IF b + v < b THEN GOTO 900
31 STORE("bond_" + s, b + v)
32 IF issigner(s) == 0 THEN GOTO 100
33 IF LOAD("bondtot") + v < LOAD("bondtot") THEN GOTO 900
34 STORE("bondtot", LOAD("bondtot") + v)
100 RETURN 0
900 RETURN 1
End Function

// A candidate who was never voted in takes their bond back. A signer, past or present, never can.
Function Unbond() Uint64
10 DIM s as String
11 DIM b as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || EXISTS("isg_" + s) != 0 THEN GOTO 900
22 IF EXISTS("bond_" + s) == 0 THEN GOTO 900
23 LET b = LOAD("bond_" + s)
24 IF b == 0 THEN GOTO 900
30 STORE("bond_" + s, 0)
31 SEND_DERO_TO_ADDRESS(s, b)
100 RETURN 0
900 RETURN 1
End Function

// n > k always: a k-of-k set can never slash anyone. n >= 2k (a quorum slashable by the rest) is a deployment
// rule in contracts/README.md, because requiring it here would forbid 2-of-3.
Function Seal() Uint64
10 DIM s as String
11 DIM i, n as Uint64
20 LET s = SIGNER()
21 IF s != LOAD("owner") || LOAD("sealed") != 0 THEN GOTO 900
22 LET n = LOAD("n")
23 IF n < LOAD("k") + 1 THEN GOTO 900
24 LET i = 0
30 IF i >= n THEN GOTO 40
31 IF EXISTS("bond_" + LOAD("sg_" + i)) == 0 THEN GOTO 900
32 IF LOAD("bond_" + LOAD("sg_" + i)) < LOAD("minbond") THEN GOTO 900
33 LET i = i + 1
34 GOTO 30
40 IF LOAD("cap") / LOAD("capratio") > LOAD("bondtot") THEN GOTO 900
41 STORE("na", n)
42 STORE("sealed", 1)
100 RETURN 0
900 RETURN 1
End Function

// A FRESH index per deposit (nothing on chain links two deposits of one user), paid for with regfee. Only one
// unattested index per address, which bounds the attestation work one account can force on the federation.
Function Register() Uint64
10 DIM s as String
11 DIM j, c as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF LOAD("sealed") == 0 || LOAD("halted") != 0 THEN GOTO 900
23 IF DEROVALUE() != LOAD("regfee") THEN GOTO 900
24 LET j = LOAD("un")
25 LET c = 0
26 IF EXISTS("uc_" + s) == 0 THEN GOTO 30
27 LET c = LOAD("uc_" + s)
28 IF EXISTS("da_" + LOAD("ul_" + s + "_" + (c - 1))) == 0 THEN GOTO 900
30 STORE("ua_" + j, s)
31 STORE("ul_" + s + "_" + c, j)
32 STORE("uc_" + s, c + 1)
33 STORE("un", j + 1)
34 IF LOAD("regfee") == 0 THEN GOTO 100
35 SEND_DERO_TO_ADDRESS(LOAD("treasury"), LOAD("regfee"))
100 RETURN 0
900 RETURN 1
End Function

// k signers attest index j's foreign address; re-attestation bumps dver_<j>. dh_<j> is when it became live: a
// signer ignores deposits older than that, since an address for a future index can be paid before it exists.
Function Attest(j Uint64, addr String) Uint64
10 DIM s, kb as String
11 DIM r, ver as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("ua_" + j) == 0 || LOAD("halted") != 0 THEN GOTO 900
23 IF STRLEN(addr) == 0 || STRLEN(addr) > 200 THEN GOTO 900
24 LET ver = 0
25 IF EXISTS("dver_" + j) == 0 THEN GOTO 30
26 LET ver = LOAD("dver_" + j)
30 LET kb = "" + LOAD("se") + "_" + LOAD("he") + "_" + (BLOCK_HEIGHT() / 2000) + "_" + j + "_" + ver
31 LET r = votek("d", kb, HEX(SHA256(addr)), s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("da_" + j, addr)
41 STORE("dver_" + j, ver + 1)
42 STORE("dh_" + j, BLOCK_HEIGHT())
100 RETURN 0
900 RETURN 1
End Function

// Mint against foreign output (txid, vout). txid is exactly 32 raw bytes and the id is built HERE, so one
// deposit has exactly one id. Votes count per (j, amount) tuple, so one wrong vote cannot block or redirect it.
Function Mint(j Uint64, txid String, vout Uint64, amount Uint64) Uint64
10 DIM s, id, kb, wb, pb as String
11 DIM r, sup, wm, pm as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF LOAD("sealed") == 0 || LOAD("halted") != 0 THEN GOTO 900
23 IF STRLEN(txid) != 32 THEN GOTO 900
24 IF amount < LOAD("mindep") THEN GOTO 900
25 IF EXISTS("ua_" + j) == 0 THEN GOTO 900
26 LET id = HEX(txid) + ":" + vout
27 IF EXISTS("mint_" + id) != 0 THEN GOTO 900
30 LET kb = "" + LOAD("se") + "_" + LOAD("he") + "_" + (BLOCK_HEIGHT() / 2000) + "_" + id
31 LET r = votek("m", kb, HEX(SHA256("" + j + "|" + amount)), s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 LET sup = LOAD("sup")
41 IF sup + amount < sup || sup + amount > LOAD("cap") THEN GOTO 900
42 IF LOAD("cap") / LOAD("capratio") > LOAD("bondtot") THEN GOTO 900
43 IF LOAD("maxdep") != 0 && amount > LOAD("maxdep") THEN GOTO 900
50 LET wb = "" + (BLOCK_HEIGHT() / 2000)
51 LET wm = 0
52 IF EXISTS("mw_" + wb) == 0 THEN GOTO 54
53 LET wm = LOAD("mw_" + wb)
54 LET pm = 0
55 IF BLOCK_HEIGHT() < 2000 THEN GOTO 59
56 LET pb = "" + (BLOCK_HEIGHT() / 2000 - 1)
57 IF EXISTS("mw_" + pb) == 0 THEN GOTO 59
58 LET pm = LOAD("mw_" + pb)
59 IF LOAD("winmax") != 0 && wm + pm + amount > LOAD("winmax") THEN GOTO 900
60 STORE("mw_" + wb, wm + amount)
61 STORE("sup", sup + amount)
62 STORE("mint_" + id, 1)
63 STORE("mh_" + id, BLOCK_HEIGHT())
64 STORE("mj_" + id, j)
65 STORE("ma_" + id, amount)
66 SEND_ASSET_TO_ADDRESS(LOAD("ua_" + j), amount, SCID())
100 RETURN 0
900 RETURN 1
End Function

// k votes record a deposit sent back abroad. The id is then closed: minted or refunded, never both.
Function Refunded(txid String, vout Uint64, ftx String) Uint64
10 DIM s, id, kb as String
11 DIM r as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("sealed") == 0 THEN GOTO 900
22 IF STRLEN(txid) != 32 THEN GOTO 900
23 IF STRLEN(ftx) == 0 || STRLEN(ftx) > 128 THEN GOTO 900
24 LET id = HEX(txid) + ":" + vout
25 IF EXISTS("mint_" + id) != 0 THEN GOTO 900
30 LET kb = "" + LOAD("se") + "_" + LOAD("he") + "_" + (BLOCK_HEIGHT() / 2000) + "_" + id
31 LET r = votek("x", kb, ftx, s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("mint_" + id, 2)
41 STORE("rf_" + id, ftx)
100 RETURN 0
900 RETURN 1
End Function

// Burn to withdraw. sup is NOT reduced here: the claim lives until the foreign payment is recorded, so
// reserve >= sup holds at every moment with no off-chain bookkeeping.
Function Burn(dest String) Uint64
10 DIM s as String
11 DIM v, j, f as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF LOAD("halted") != 0 || LOAD("sealed") == 0 THEN GOTO 900
23 LET v = ASSETVALUE(SCID())
24 IF v < LOAD("minburn") || DEROVALUE() != 0 THEN GOTO 900
25 IF STRLEN(dest) < 16 || STRLEN(dest) > 200 THEN GOTO 900
26 IF v > LOAD("sup") THEN GOTO 900
27 LET f = LOAD("wfee")
30 LET j = LOAD("wn")
31 STORE("w_" + j + "_to", dest)
32 STORE("w_" + j + "_v", v - f)
33 STORE("w_" + j + "_f", f)
34 STORE("w_" + j + "_by", s)
35 STORE("w_" + j + "_h", BLOCK_HEIGHT())
36 STORE("w_" + j + "_st", 0)
37 STORE("wn", j + 1)
100 RETURN 0
900 RETURN 1
End Function

// k votes on one foreign txid record that withdrawal j was paid: the claim leaves sup, the fee becomes the
// treasury's, and that txid can never settle another withdrawal. Nothing counts before wdelay blocks.
Function Released(j Uint64, ftx String) Uint64
10 DIM s, kb as String
11 DIM r as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("halted") != 0 THEN GOTO 900
22 IF EXISTS("w_" + j + "_st") == 0 THEN GOTO 900
23 IF LOAD("w_" + j + "_st") != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() < LOAD("w_" + j + "_h") + LOAD("wdelay") THEN GOTO 900
25 IF STRLEN(ftx) == 0 || STRLEN(ftx) > 128 THEN GOTO 900
26 IF EXISTS("fu_" + ftx) != 0 THEN GOTO 900
30 LET kb = "" + LOAD("se") + "_" + LOAD("he") + "_" + (BLOCK_HEIGHT() / 2000) + "_" + j
31 LET r = votek("r", kb, ftx, s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("w_" + j + "_st", 1)
41 STORE("w_" + j + "_ftx", ftx)
42 STORE("fu_" + ftx, j + 1)
43 STORE("sup", LOAD("sup") - LOAD("w_" + j + "_v"))
44 STORE("feepool", LOAD("feepool") + LOAD("w_" + j + "_f"))
100 RETURN 0
900 RETURN 1
End Function

// k votes return a withdrawal that cannot be paid, so a user is never left with neither units nor coins.
Function Veto(j Uint64) Uint64
10 DIM s, kb as String
11 DIM r as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("w_" + j + "_st") == 0 THEN GOTO 900
23 IF LOAD("w_" + j + "_st") != 0 THEN GOTO 900
30 LET kb = "" + LOAD("se") + "_" + LOAD("he") + "_" + (BLOCK_HEIGHT() / 2000) + "_" + j
31 LET r = votek("g", kb, "1", s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("w_" + j + "_st", 2)
41 SEND_ASSET_TO_ADDRESS(LOAD("w_" + j + "_by"), LOAD("w_" + j + "_v") + LOAD("w_" + j + "_f"), SCID())
100 RETURN 0
900 RETURN 1
End Function

// The treasury takes the fee units released withdrawals left behind; they are counted in sup throughout.
Function ClaimFees() Uint64
10 DIM s as String
11 DIM f as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || s != LOAD("treasury") || DEROVALUE() != 0 THEN GOTO 900
22 LET f = LOAD("feepool")
23 IF f == 0 THEN GOTO 900
30 STORE("feepool", 0)
31 SEND_ASSET_TO_ADDRESS(s, f, SCID())
100 RETURN 0
900 RETURN 1
End Function

// k votes for one target state inside one 2000-block bucket flip halted; each flip opens a new epoch.
Function Halt(want Uint64) Uint64
10 DIM s, kb as String
11 DIM r, e as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 || want > 1 || LOAD("sealed") == 0 THEN GOTO 900
22 IF want == LOAD("halted") THEN GOTO 900
23 LET e = LOAD("he")
30 LET kb = "" + LOAD("se") + "_" + e + "_" + (BLOCK_HEIGHT() / 2000)
31 LET r = votek("h", kb, "" + want, s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("halted", want)
41 STORE("he", e + 1)
100 RETURN 0
900 RETURN 1
End Function

// k votes change the cap, which can never exceed capratio x total bonds. Every change opens a cap epoch.
Function SetCap(cap Uint64) Uint64
10 DIM s, kb as String
11 DIM r as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("sealed") == 0 THEN GOTO 900
22 IF cap == 0 || cap == LOAD("cap") || cap > 9223372036854775807 THEN GOTO 900
23 IF cap / LOAD("capratio") > LOAD("bondtot") THEN GOTO 900
30 LET kb = "" + LOAD("se") + "_" + LOAD("ce")
31 LET r = votek("c", kb, "" + cap, s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("cap", cap)
41 STORE("ce", LOAD("ce") + 1)
100 RETURN 0
900 RETURN 1
End Function

// k votes slash a signer: bond to the treasury, removed for good, every pending vote voided (new slash epoch),
// active set never below the quorum floor. The cap follows the bond down so mints do not silently stop.
Function Slash(target String) Uint64
10 DIM s, t, kb as String
11 DIM r, b, na, bt as Uint64
20 LET s = SIGNER()
21 IF issigner(s) == 0 || DEROVALUE() != 0 || LOAD("sealed") == 0 THEN GOTO 900
22 LET t = ADDRESS_RAW(target)
23 IF t == s || issigner(t) == 0 THEN GOTO 900
24 LET na = LOAD("na")
25 IF na < LOAD("k") + 1 THEN GOTO 900
30 LET kb = "" + LOAD("se")
31 LET r = votek("s", kb, t, s)
32 IF r == 2 THEN GOTO 900
33 IF r == 0 THEN GOTO 100
40 STORE("isg_" + t, 0)
41 STORE("na", na - 1)
42 STORE("se", LOAD("se") + 1)
43 IF EXISTS("bond_" + t) == 0 THEN GOTO 100
44 LET b = LOAD("bond_" + t)
45 IF b == 0 THEN GOTO 100
46 STORE("bond_" + t, 0)
47 LET bt = LOAD("bondtot") - b
48 STORE("bondtot", bt)
49 SEND_DERO_TO_ADDRESS(LOAD("treasury"), b)
50 IF LOAD("cap") / LOAD("capratio") <= bt THEN GOTO 100
51 STORE("cap", bt * LOAD("capratio"))
52 STORE("ce", LOAD("ce") + 1)
100 RETURN 0
900 RETURN 1
End Function
