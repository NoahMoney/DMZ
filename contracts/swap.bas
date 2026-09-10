// Veil swap v1: hash-time-locked escrow for atomic swaps. A maker locks DERO or one asset under sha256(secret) for a
// taker (or for anyone) until `expiry` (block height); the taker claims by revealing the secret, which then sits on
// chain for the other chain's leg; after expiry only the maker can refund. No oracle, no admin, no fee, immutable.
// Keys: n kind v   per lock i: m_<i> maker (raw) t_<i> taker (raw, "" = anyone) h_<i> sha256 hex (64) a_<i> asset (raw 32
//       bytes, "" = DERO) v_<i> amount e_<i> expiry height st_<i> 0 open / 1 claimed / 2 refunded x_<i> revealed secret

Function Initialize() Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 STORE("kind", "swap")
11 STORE("v", 1)
12 STORE("n", 0)
19 STORE("initd", 1)
20 RETURN 0
900 RETURN 1
End Function

// hash: 64 hex chars of sha256(secret), lower case. taker: address string or "". expiry: absolute height, at most
// 1 000 000 ahead. asset: 64 hex chars of the asset SCID, or "" to lock the DERO sent with the call.
// Claim matches the stored hash against HEX(SHA256(raw)), and the DVM renders hex in lower case, so a hash written
// any other way names a lock nobody can ever claim — while a counter-chain script, which holds the same 32 bytes and
// cannot see how they were written, still pays. A maker who posts H in upper case therefore takes the taker's leg
// and refunds this one at expiry. Line 29 refuses that lock at the door: the hash must be exactly the lower-case hex
// of the bytes it names. It is the last check before the stores, and it is on both paths through line 26.
Function Lock(hash String, taker String, expiry Uint64, asset String) Uint64
10 DIM s, t, a as String
11 DIM v, id, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF STRLEN(hash) != 64 THEN GOTO 900
23 LET h = BLOCK_HEIGHT()
24 IF expiry <= h + 1 || expiry > h + 1000000 THEN GOTO 900
25 LET t = ""
26 IF STRLEN(taker) == 0 THEN GOTO 29
27 LET t = ADDRESS_RAW(taker)
28 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
29 IF HEX(HEXDECODE(hash)) != hash THEN GOTO 900
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
53 STORE("h_" + id, hash)
54 STORE("a_" + id, a)
55 STORE("v_" + id, v)
56 STORE("e_" + id, expiry)
57 STORE("st_" + id, 0)
58 STORE("n", id + 1)
100 RETURN 0
900 RETURN 1
End Function

// Reveal the secret before expiry: the funds go to the caller (must be the taker when one was named). The secret is
// 32 raw bytes given as 64 hex characters (a curve scalar fits), hashed as raw bytes: hash == sha256(bytes).
Function Claim(id Uint64, secret String) Uint64
10 DIM s, t, a, raw as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() >= LOAD("e_" + id) THEN GOTO 900
25 IF STRLEN(secret) != 64 THEN GOTO 900
26 LET raw = HEXDECODE(secret)
27 IF STRLEN(raw) != 32 THEN GOTO 900
28 IF HEX(SHA256(raw)) != LOAD("h_" + id) THEN GOTO 900
29 LET t = LOAD("t_" + id)
30 IF STRLEN(t) != 0 && t != s THEN GOTO 900
40 STORE("st_" + id, 1)
41 STORE("x_" + id, secret)
42 LET a = LOAD("a_" + id)
43 IF STRLEN(a) != 0 THEN GOTO 46
44 SEND_DERO_TO_ADDRESS(s, LOAD("v_" + id))
45 GOTO 100
46 SEND_ASSET_TO_ADDRESS(s, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function

// After expiry the maker takes the funds back.
Function Refund(id Uint64) Uint64
10 DIM s, a as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() < LOAD("e_" + id) THEN GOTO 900
25 IF s != LOAD("m_" + id) THEN GOTO 900
30 STORE("st_" + id, 2)
31 LET a = LOAD("a_" + id)
32 IF STRLEN(a) != 0 THEN GOTO 35
33 SEND_DERO_TO_ADDRESS(s, LOAD("v_" + id))
34 GOTO 100
35 SEND_ASSET_TO_ADDRESS(s, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function
