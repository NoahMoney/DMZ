// Veil board v1: the public order board for atomic swaps. Nothing is held here; it is the place where a maker says
// "I give X of this DERO asset for Y on chain C, pay me at this address", and a taker answers with the hash their
// counter-chain lock uses. The funds move only through swap.bas (DERO leg) and the counter-chain escrow.
// Keys: n kind v   per offer i: m_<i> maker (raw) c_<i> chain label ga_<i> give asset (64-hex or "" = DERO) gv_<i> give
//       amount wv_<i> want amount (counter-chain units, informational) wa_<i> maker's counter-chain address
//       e_<i> expiry height st_<i> 0 open / 1 accepted / 2 cancelled   t_<i> taker (raw) h_<i> hash the taker locked under
//       ta_<i> taker's counter-chain lock reference (free text, e.g. a tx id)

Function Initialize() Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 STORE("kind", "board")
11 STORE("v", 1)
12 STORE("n", 0)
19 STORE("initd", 1)
20 RETURN 0
900 RETURN 1
End Function

Function Post(chain String, giveAsset String, giveAmount Uint64, wantAmount Uint64, wantAddr String, expiry Uint64) Uint64
10 DIM s as String
11 DIM id, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF STRLEN(chain) == 0 || STRLEN(chain) > 16 || STRLEN(wantAddr) == 0 || STRLEN(wantAddr) > 128 THEN GOTO 900
23 IF STRLEN(giveAsset) != 0 && STRLEN(giveAsset) != 64 THEN GOTO 900
24 IF giveAmount == 0 || wantAmount == 0 THEN GOTO 900
25 LET h = BLOCK_HEIGHT()
26 IF expiry <= h || expiry > h + 1000000 THEN GOTO 900
30 LET id = LOAD("n")
31 STORE("m_" + id, s)
32 STORE("c_" + id, chain)
33 STORE("ga_" + id, giveAsset)
34 STORE("gv_" + id, giveAmount)
35 STORE("wv_" + id, wantAmount)
36 STORE("wa_" + id, wantAddr)
37 STORE("e_" + id, expiry)
38 STORE("st_" + id, 0)
39 STORE("n", id + 1)
100 RETURN 0
900 RETURN 1
End Function

// The taker has locked the counter-chain side under `hash`; the maker now locks the DERO leg in swap.bas for the taker.
Function Accept(id Uint64, hash String, lockRef String) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() >= LOAD("e_" + id) THEN GOTO 900
25 IF STRLEN(hash) != 64 || STRLEN(lockRef) > 128 THEN GOTO 900
26 IF s == LOAD("m_" + id) THEN GOTO 900
30 STORE("st_" + id, 1)
31 STORE("t_" + id, s)
32 STORE("h_" + id, hash)
33 STORE("ta_" + id, lockRef)
100 RETURN 0
900 RETURN 1
End Function

Function Cancel(id Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF s != LOAD("m_" + id) THEN GOTO 900
30 STORE("st_" + id, 2)
100 RETURN 0
900 RETURN 1
End Function
