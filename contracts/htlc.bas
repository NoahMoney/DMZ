// Veil htlc v2: the DERO leg of the DERO <-> Monero atomic swap (docs/XMR_SWAP_BUILD.md §1, §3.A). It is swap.bas with
// a second preimage on the claim, a preimage on the refund and a third exit. The maker (Bob, who has DERO) locks DERO
// or one asset for a named taker (Alice, who has XMR) under three sha256 hashes: ha of Alice's Monero key share s_a,
// hk of Bob's gate secret k_b, hb of Bob's key share s_b. Claim (taker, before t1) needs s_a and k_b and leaves s_a on
// chain, so Bob can spend the Monero with s_a + s_b; Bob hands k_b over off chain only once Alice's Monero lock has
// confirmed, so Alice cannot take the DERO without locking XMR. Refund (maker, t1 <= height < t2) needs s_b and leaves
// it on chain, so Alice can recover any XMR she locked. Punish (height >= t2) needs nothing and pays the taker: a maker
// who neither released k_b nor refunded loses the DERO, so doing nothing is never his best move. v2 (LIV-07): anyone
// may send Punish. It carries no secret and can pay nobody but t_<id>, so the sender chooses when an abandoned lock
// closes and never who is paid; in v1 only the taker could, and a lock she never punished was frozen for ever. No
// oracle, no admin, no fee, immutable.
// Keys: n kind v   per lock i: m_<i> maker (raw) t_<i> taker (raw, required) ha_<i> hk_<i> hb_<i> sha256 hex (64 each)
//       a_<i> asset (raw 32 bytes, "" = DERO) v_<i> amount t1_<i> t2_<i> absolute heights st_<i> 0 open / 1 claimed /
//       2 refunded / 3 punished xa_<i> revealed s_a (Claim) xb_<i> revealed s_b (Refund)

Function Initialize() Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 STORE("kind", "htlc")
11 STORE("v", 2)
12 STORE("n", 0)
19 STORE("initd", 1)
20 RETURN 0
900 RETURN 1
End Function

// ha, hk, hb: 64 lower-case hex chars each of sha256(secret). Claim and Refund compare HEX(SHA256(raw)), which the
// DVM renders in lower case, with the stored string, so a hash in capitals or not hex at all is one no preimage can
// match: an H_k like that would let the taker lock XMR against a gate that can never open, and an H_b like that
// would close the maker's own Refund for good. v2 refuses them here, as swap.bas and board.bas do. taker: the
// address Claim and Punish pay, so unlike swap.bas it is required. t1 < t2 are absolute heights, t2 at most
// 1 000 000 ahead; the minimum widths (400 blocks each) are the companion's policy, not the contract's. asset: 64
// hex chars of the asset SCID, or "" to lock the DERO sent with the call.
Function Lock(ha String, hk String, hb String, taker String, t1 Uint64, t2 Uint64, asset String) Uint64
10 DIM s, t, a as String
11 DIM v, id, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF STRLEN(ha) != 64 || STRLEN(hk) != 64 || STRLEN(hb) != 64 THEN GOTO 900
23 IF HEX(HEXDECODE(ha)) != ha || HEX(HEXDECODE(hk)) != hk || HEX(HEXDECODE(hb)) != hb THEN GOTO 900
24 LET h = BLOCK_HEIGHT()
25 IF t1 <= h + 1 || t2 <= t1 || t2 > h + 1000000 THEN GOTO 900
26 IF STRLEN(taker) == 0 THEN GOTO 900
27 LET t = ADDRESS_RAW(taker)
28 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
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

// The taker, before t1, reveals both her key share sa and the maker's gate secret kb: each is 32 raw bytes given as
// 64 hex characters and hashed as raw bytes. Only sa is kept — it is what the maker needs to spend the Monero; kb has
// done its work the moment the call commits.
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

// The maker, from t1 and before t2, takes the funds back by revealing his key share sb. The window closes at t2 so
// that Punish is never a race: past t2 only Punish can move the funds, and it pays the taker.
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

// From t2 the funds go to the stored taker with no secret at all: the maker had [t1, t2) to refund and did not.
// Anyone may send it — the taker, the maker, a stranger — because it carries nothing that could steal and pays
// only t_<id>; the sender still needs a valid ring-2 signer and attaches nothing, as every other exit does.
//
// Every exit refuses value attached to it: DERO, and the lock's own asset. Anything accepted would stay in the
// contract for ever, since no exit pays out more than v_<id>. An asset of another SCID cannot be seen here — the
// DVM reads ASSETVALUE only for an asset it is named — so the companion never attaches one.
Function Punish(id Uint64) Uint64
10 DIM s, t, a as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() < LOAD("t2_" + id) THEN GOTO 900
25 LET a = LOAD("a_" + id)
26 IF STRLEN(a) == 0 THEN GOTO 30
27 IF ASSETVALUE(a) != 0 THEN GOTO 900
30 STORE("st_" + id, 3)
31 LET t = LOAD("t_" + id)
32 IF STRLEN(a) != 0 THEN GOTO 35
33 SEND_DERO_TO_ADDRESS(t, LOAD("v_" + id))
34 GOTO 100
35 SEND_ASSET_TO_ADDRESS(t, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function
