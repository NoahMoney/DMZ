// Veil factory v1: registry of pools, launches, NFT collections and NFAs plus the protocol treasury pointer.
// The DVM has no cross-contract reads, so registration is a paid hint: the app verifies every registered SCID
// (code equal to the shipped template, treasury equal to "treasury" here) before listing it.
// Keys: owner treasury tstr regfee pc pool_<n> lc launch_<n> nc nft_<n> nfa_<n>   (SCIDs stored as hex strings)

Function Initialize(treasury String, regfee Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM s, t as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 LET t = ADDRESS_RAW(treasury)
23 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
30 STORE("kind", "factory")
31 STORE("v", 1)
32 STORE("owner", s)
33 STORE("treasury", t)
34 STORE("tstr", ADDRESS_STRING(t))
35 STORE("regfee", regfee)
36 STORE("pc", 0)
37 STORE("lc", 0)
38 STORE("nc", 0)
39 STORE("initd", 1)
40 RETURN 0
900 RETURN 1
End Function

Function SetTreasury(treasury String) Uint64
10 DIM t as String
20 IF SIGNER() != LOAD("owner") THEN GOTO 900
21 LET t = ADDRESS_RAW(treasury)
22 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
30 STORE("treasury", t)
31 STORE("tstr", ADDRESS_STRING(t))
40 RETURN 0
900 RETURN 1
End Function

Function SetRegFee(regfee Uint64) Uint64
10 IF SIGNER() != LOAD("owner") THEN GOTO 900
20 STORE("regfee", regfee)
30 RETURN 0
900 RETURN 1
End Function

Function TransferOwnership(newowner String) Uint64
10 DIM t as String
20 IF SIGNER() != LOAD("owner") THEN GOTO 900
21 LET t = ADDRESS_RAW(newowner)
22 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
30 STORE("tmpowner", t)
40 RETURN 0
900 RETURN 1
End Function

Function ClaimOwnership() Uint64
10 IF EXISTS("tmpowner") == 0 THEN GOTO 900
20 IF SIGNER() != LOAD("tmpowner") THEN GOTO 900
30 STORE("owner", SIGNER())
31 DELETE("tmpowner")
40 RETURN 0
900 RETURN 1
End Function

// Registration: pay regfee (forwarded to the treasury), append the SCID hex to the list.
Function RegisterPool(scid String) Uint64
10 IF reg(scid) == 0 THEN GOTO 900
20 STORE("pool_" + LOAD("pc"), HEX(scid))
21 STORE("pc", LOAD("pc") + 1)
30 RETURN 0
900 RETURN 1
End Function

Function RegisterLaunch(scid String) Uint64
10 IF reg(scid) == 0 THEN GOTO 900
20 STORE("launch_" + LOAD("lc"), HEX(scid))
21 STORE("lc", LOAD("lc") + 1)
30 RETURN 0
900 RETURN 1
End Function

Function RegisterNFT(scid String) Uint64
10 IF reg(scid) == 0 THEN GOTO 900
20 STORE("nft_" + LOAD("nc"), HEX(scid))
21 STORE("nc", LOAD("nc") + 1)
30 RETURN 0
900 RETURN 1
End Function

// An NFA (contracts/nfa.bas, one ART-NFA-MS1 asset per contract) takes the next slot of the badge counter:
// slot i holds exactly one of nft_<i> / nfa_<i>, so the app walks one range for both kinds and reads each
// slot by prefix, skipping the spelling that is absent. Initialize gains no key.
Function RegisterNFA(scid String) Uint64
10 IF reg(scid) == 0 THEN GOTO 900
20 STORE("nfa_" + LOAD("nc"), HEX(scid))
21 STORE("nc", LOAD("nc") + 1)
30 RETURN 0
900 RETURN 1
End Function

// Common checks for a registration; returns 1 when acceptable and forwards the fee.
Function reg(scid String) Uint64
10 IF STRLEN(scid) != 32 THEN GOTO 900
20 IF DEROVALUE() < LOAD("regfee") THEN GOTO 900
30 IF DEROVALUE() == 0 THEN GOTO 40
35 SEND_DERO_TO_ADDRESS(LOAD("treasury"), DEROVALUE())
40 RETURN 1
900 RETURN 0
End Function
