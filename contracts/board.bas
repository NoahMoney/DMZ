// DMZ board v2: the public order board for atomic swaps. Nothing is held here; it is the place where a maker says
// "I give X of this DERO asset for Y on chain C, pay me at this address, under this hash", and a taker answers
// with the two things the maker needs to compute the counter-chain script: the taker's refund key hash and the
// height after which the taker may refund. The funds move only through swap.bas (the DERO leg) and the
// counter-chain escrow. The maker holds the secret and locks first (docs/ZEC_SWAP.md §2), which is why the hash
// is the maker's, given at Post, and not the taker's as in v1.
// Keys: n kind v   per offer i: m_<i> maker (raw) c_<i> chain label ga_<i> give asset (64-hex or "" = DERO) gv_<i> give
//       amount wv_<i> want amount (counter-chain units, informational) wa_<i> maker's counter-chain address
//       h_<i> the maker's hash (SHA256 of the secret only the maker holds, 64 hex) e_<i> expiry height
//       st_<i> 0 open / 1 accepted / 2 cancelled   t_<i> taker (raw) rp_<i> taker's refund key hash (20 bytes as
//       40 hex) cl_<i> the counter-chain height after which the taker may refund
// An offer dies by itself at e_<i>: Accept refuses it, and the maker may Cancel it whatever its state — an
// accepted offer that went nowhere is not a row that needs a human to leave it.
// Initialize is an ordinary exported function to the DVM, so a second call after install would reset n and
// overwrite every offer: the initd key refuses it (tests/reentry_test.go).
Function Initialize() Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 STORE("kind", "board")
11 STORE("v", 2)
12 STORE("n", 0)
13 STORE("initd", 1)
20 RETURN 0
900 RETURN 1
End Function

// The maker's offer. hash is SHA256 of a 32-byte secret the maker keeps until the counter-chain claim.
Function Post(chain String, giveAsset String, giveAmount Uint64, wantAmount Uint64, wantAddr String, hash String, expiry Uint64) Uint64
10 DIM s as String
11 DIM id, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF STRLEN(chain) == 0 || STRLEN(chain) > 16 || STRLEN(wantAddr) == 0 || STRLEN(wantAddr) > 128 THEN GOTO 900
23 IF STRLEN(giveAsset) != 0 && STRLEN(giveAsset) != 64 THEN GOTO 900
24 IF giveAmount == 0 || wantAmount == 0 || STRLEN(hash) != 64 THEN GOTO 900
25 LET h = BLOCK_HEIGHT()
26 IF expiry <= h || expiry > h + 1000000 THEN GOTO 900
30 LET id = LOAD("n")
31 STORE("m_" + id, s)
32 STORE("c_" + id, chain)
33 STORE("ga_" + id, giveAsset)
34 STORE("gv_" + id, giveAmount)
35 STORE("wv_" + id, wantAmount)
36 STORE("wa_" + id, wantAddr)
37 STORE("h_" + id, hash)
38 STORE("e_" + id, expiry)
39 STORE("st_" + id, 0)
40 STORE("n", id + 1)
100 RETURN 0
900 RETURN 1
End Function

// The taker binds the offer to their DERO address (the escrow's beneficiary) and names the refund key hash and
// the refund height of the counter-chain script they will fund once the maker's DERO lock is deep enough.
// One Accept per offer; never by the maker; never after expiry.
Function Accept(id Uint64, refundPkh String, cltv Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() >= LOAD("e_" + id) THEN GOTO 900
25 IF STRLEN(refundPkh) != 40 || cltv == 0 THEN GOTO 900
26 IF s == LOAD("m_" + id) THEN GOTO 900
30 STORE("st_" + id, 1)
31 STORE("t_" + id, s)
32 STORE("rp_" + id, refundPkh)
33 STORE("cl_" + id, cltv)
100 RETURN 0
900 RETURN 1
End Function

// The maker withdraws an open offer at any time, and an accepted one once it has expired — the taker who accepted
// and walked leaves nothing behind that needs anyone else.
Function Cancel(id Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF s != LOAD("m_" + id) THEN GOTO 900
24 IF LOAD("st_" + id) == 2 THEN GOTO 900
25 IF LOAD("st_" + id) == 0 THEN GOTO 30
26 IF BLOCK_HEIGHT() < LOAD("e_" + id) THEN GOTO 900
30 STORE("st_" + id, 2)
100 RETURN 0
900 RETURN 1
End Function
