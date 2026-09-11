// DMZ board v3: the public order board for atomic swaps. Nothing is held here; it is the place where a maker says
// "I give X of this DERO asset for Y on chain C, pay me at this address, under this hash", and a taker answers
// with the two things the maker needs to compute the counter-chain script: the taker's refund key hash and the
// height after which the taker may refund. The funds move only through swap.bas (the DERO leg) and the
// counter-chain escrow. The maker holds the secret and locks first (docs/ZEC_SWAP.md §2), which is why the hash
// is the maker's, given at Post, and not the taker's as in v1.
// Keys: n kind v aw   per offer i: m_<i> maker (raw) c_<i> chain label ga_<i> give asset (64-hex or "" = DERO) gv_<i>
//       give amount wv_<i> want amount (counter-chain units, informational) wa_<i> maker's counter-chain address
//       h_<i> the maker's hash (SHA256 of the secret only the maker holds, 64 hex) e_<i> expiry height
//       st_<i> 0 open / 1 accepted / 2 cancelled   t_<i> taker (raw) rp_<i> taker's refund key hash (20 bytes as
//       40 hex) cl_<i> the counter-chain height after which the taker may refund   ad_<i> the acceptance deadline:
//       the accept height + aw, by which the maker must have locked the DERO leg (docs/ZEC_SWAP.md §5, P_lock)
// An offer dies by itself at e_<i>: Accept refuses it, and the maker may Cancel it whatever its state. v3: an
// accepted offer the maker has not locked for is dead to its taker at ad_<i>, and from then the maker may Cancel
// it too, so one Accept from anyone holds an offer for aw blocks at most, not until e_<i> (ADV-PRICE-07). No row
// needs a human to leave it: the maker may, and time alone ends it at e_<i>.
// The board holds nothing, so every call refuses DERO attached and, since v3, the offer's give asset attached
// (GAP-027): the one asset the DVM can name here. An asset the offer does not name cannot be seen — the DVM has no
// builtin that lists what a call carries — which is why nothing should ever be attached to a board call.
// Initialize is an ordinary exported function to the DVM, so a second call after install would reset n and
// overwrite every offer: the initd key refuses it (tests/reentry_test.go).
Function Initialize() Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 STORE("kind", "board")
11 STORE("v", 3)
12 STORE("n", 0)
13 STORE("aw", 200)
14 STORE("initd", 1)
20 RETURN 0
900 RETURN 1
End Function

// The maker's offer. hash is SHA256 of a 32-byte secret the maker keeps until the counter-chain claim, written as
// 64 lower-case hex characters. The taker builds the counter-chain script from these bytes and cannot see how they
// were written, but swap.bas Claim can only ever match the lower-case rendering, so an offer posted in any other
// case is one whose DERO leg the taker can never claim and whose counter-chain leg the maker still takes. Line 27
// refuses it here as well, so the board and the lock hold the same 64 characters. Line 29 refuses the give asset
// attached: the offer locks nothing, and the asset would stay here for ever.
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
27 IF HEX(HEXDECODE(hash)) != hash THEN GOTO 900
28 IF STRLEN(giveAsset) == 0 THEN GOTO 30
29 IF ASSETVALUE(HEXDECODE(giveAsset)) != 0 THEN GOTO 900
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
// One Accept per offer; never by the maker; never after expiry. cltv is a height: a transparent Zcash script reads
// any value from 500 000 000 (LOCKTIME_THRESHOLD, BIP 65) as a Unix time, every one of which is already past, so line
// 25 refuses it (ZEC-3). Whether cltv is inside the policy window of the Zcash tip only the companions can tell.
// Line 34 records the acceptance deadline: the taker's companion funds nothing against a lock mined at or after it.
Function Accept(id Uint64, refundPkh String, cltv Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() >= LOAD("e_" + id) THEN GOTO 900
25 IF STRLEN(refundPkh) != 40 || cltv == 0 || cltv >= 500000000 THEN GOTO 900
26 IF s == LOAD("m_" + id) THEN GOTO 900
27 IF STRLEN(LOAD("ga_" + id)) == 0 THEN GOTO 30
28 IF ASSETVALUE(HEXDECODE(LOAD("ga_" + id))) != 0 THEN GOTO 900
30 STORE("st_" + id, 1)
31 STORE("t_" + id, s)
32 STORE("rp_" + id, refundPkh)
33 STORE("cl_" + id, cltv)
34 STORE("ad_" + id, BLOCK_HEIGHT() + LOAD("aw"))
100 RETURN 0
900 RETURN 1
End Function

// The maker withdraws an open offer at any time, and an accepted one from its acceptance deadline or its expiry,
// whichever comes first — the taker who accepted and walked leaves nothing behind that needs anyone else. The board
// holds no value: a swap whose DERO leg was locked in time goes on in swap.bas whatever this row says. Lines 24-25
// refuse the give asset attached, on the path of both states.
Function Cancel(id Uint64) Uint64
10 DIM s as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF s != LOAD("m_" + id) THEN GOTO 900
24 IF STRLEN(LOAD("ga_" + id)) == 0 THEN GOTO 26
25 IF ASSETVALUE(HEXDECODE(LOAD("ga_" + id))) != 0 THEN GOTO 900
26 IF LOAD("st_" + id) == 2 THEN GOTO 900
27 IF LOAD("st_" + id) == 0 THEN GOTO 30
28 IF BLOCK_HEIGHT() < LOAD("e_" + id) && BLOCK_HEIGHT() < LOAD("ad_" + id) THEN GOTO 900
30 STORE("st_" + id, 2)
100 RETURN 0
900 RETURN 1
End Function
