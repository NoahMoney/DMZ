// DMZ htlc v3: the DERO leg of the DERO <-> Monero atomic swap (docs/XMR_SWAP_BUILD.md §1, §3.A). It is swap.bas with
// a second preimage on the claim, a preimage on the refund and two more exits. The maker (Bob, who has DERO) locks DERO
// or one asset for a named taker (Alice, who has XMR) under three sha256 hashes: ha of Alice's Monero key share s_a,
// hk of Bob's gate secret k_b, hb of Bob's key share s_b. An open lock (st_<id> = 0) has four exits, each at its own
// heights, so no two of them are open at one height:
//   Claim    the taker, before t1, with s_a and k_b. s_a stays on chain, so Bob can spend the Monero with s_a + s_b.
//            Bob hands k_b over off chain only once Alice's Monero lock has confirmed, so Alice cannot take the DERO
//            without locking XMR.
//   Refund   the maker, in [t1, t2), with s_b. s_b stays on chain, so Alice can recover any XMR she locked.
//   Punish   anyone, in [t2, t2 + g2), with no secret, paying only the taker: a maker who neither released k_b nor
//            refunded loses the DERO. The sender chooses when the lock closes and never who is paid (v2, LIV-07).
//   Reclaim  the maker, from t2 + g2 and for ever after, with s_b, stored as Refund stores it (v3: docs/
//            ATOMIC_SWAP_STUDY.md §5 decision 17 (a), U13; MT-J13). Punish has closed by then, so a lock nobody
//            punished goes back to its maker, and because he must reveal s_b to take it, Alice can still recover her
//            XMR at any later time. What v3 costs the taker: her Punish has a deadline, t2 + g2.
// Liveness by construction. Before t2 + g2 time alone carries an open lock to an exit someone may send, and in
// [t2, t2 + g2) anyone may send it. From t2 + g2 an open lock waits on its own maker, with no deadline: that is the
// maker's per-party window, the one exception the operator's ruling MT-J11(a) accepts, and the only party whose
// inaction keeps the lock open is the party it pays. No state here needs the operator. A maker who has lost s_b cannot
// leave that window, by his own loss; anyone could have closed the lock for the taker in [t2, t2 + g2).
// Fixed at install (MT-A03(a)): w1min, the least t1 - lock height; w2min, the least t2 - t1; g2, the length of the
// Punish window. No oracle, no admin, no fee, immutable.
// Keys: n kind v w1min w2min g2   per lock i: m_<i> maker (raw) t_<i> taker (raw, required) ha_<i> hk_<i> hb_<i> sha256
//       hex (64 each) a_<i> asset (raw 32 bytes, "" = DERO) v_<i> amount t1_<i> t2_<i> absolute heights st_<i> 0 open /
//       1 claimed / 2 refunded / 3 punished / 4 reclaimed xa_<i> revealed s_a (Claim) xb_<i> revealed s_b (Refund,
//       Reclaim)

// w1min, w2min, g2: widths in blocks, fixed for the life of the contract (the companions' mainnet profile supplies
// them). A missing argument makes the DVM refuse the call, and lines 6-7 refuse 0, anything past Lock's million-block
// horizon, and a w1min + w2min no lock could meet (the tightest lock has t2 = its height + max(2, w1min) + w2min, and
// t2 is at most 1 000 000 ahead), so an install that leaves one out or gets one wrong installs nothing. The separate
// ceilings keep w1min + w2min from wrapping. The same code with other values has the same code hash, so each
// companion reads all three and refuses a deployment outside its own policy.
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

// ha, hk, hb: 64 lower-case hex chars each of sha256(secret). Claim, Refund and Reclaim compare HEX(SHA256(raw)), which
// the DVM renders in lower case, with the stored string, so a hash in capitals or not hex at all is one no preimage
// can match: an H_k like that would let the taker lock XMR against a gate that can never open, and an H_b like that
// would close the maker's own Refund and Reclaim for good. Lock refuses them here, as swap.bas and board.bas do.
// taker: the address Claim and Punish pay, so unlike swap.bas it is required. t1 < t2 are absolute heights, t2 at most
// 1 000 000 ahead (line 25), and since v3 (MT-A03(a)) t1 is at least w1min ahead and t2 at least w2min after t1
// (line 26): a window narrower than the deployment's minimum is refused by the contract, not only by the companion's
// policy. asset: 64 hex chars of the asset SCID, or "" to lock the DERO sent with the call.
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
// that Punish is never a race: in [t2, t2 + g2) only Punish can move the funds, and it pays the taker.
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

// In [t2, t2 + g2) the funds go to the stored taker with no secret at all: the maker had [t1, t2) to refund and did
// not. Anyone may send it — the taker, the maker, a stranger, a relayer the taker asked — because it carries nothing
// that could steal and pays only t_<id>; the sender still needs a valid ring-2 signer and attaches nothing, as every
// other exit does. From t2 + g2 it is refused (line 25) and the lock is the maker's to Reclaim. No overflow: t2 is at
// most 1 000 000 past its lock's height and g2 at most 1 000 000.
//
// Every exit refuses value attached to it: DERO, and the lock's own asset. Anything accepted would stay in the
// contract for ever, since no exit pays out more than v_<id>. An asset of another SCID cannot be seen here — the
// DVM reads ASSETVALUE only for an asset it is named — so the companion never attaches one.
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

// From t2 + g2, and with no later limit, the maker alone takes the funds back by revealing his key share sb, exactly
// as Refund checks it, and sb is stored under xb_<id> as Refund stores it, so the taker's companion reads it there and
// recovers any XMR she locked. The state is 4, not Refund's 2, so the record says the taker's Punish window passed
// unused. Only the maker may send it (line 25): it pays the signer, and the signer must be m_<id>.
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
