// Veil swap v2: hash-time-locked escrow for atomic swaps. A maker locks DERO or one asset under sha256(secret) for a
// named taker until `expiry` (block height); the secret claims it for her before expiry and then sits on chain for
// the other chain's leg; after expiry only the maker can refund. No oracle, no admin, no fee, immutable.
// v2 (docs/ATOMIC_SWAP_STUDY.md §2.2; docs/MAINNET_GAP_ANALYSIS.md): Claim pays the named taker whoever sends it,
// so a relayer, the maker's companion or the taker's second device can complete her leg once the secret is public
// (Z-C); every lock names its taker (GAP-024); a lock is at least wmin blocks wide, wmin fixed at install (Z-D,
// GAP-025); an exit refuses the lock's own asset attached, as it refuses DERO (GAP-027). The DVM reads ASSETVALUE
// only for an asset it is named, so an unrelated asset attached to any call cannot be seen.
// Keys: n kind v wmin   per lock i: m_<i> maker (raw) t_<i> taker (raw) h_<i> sha256 hex (64) a_<i> asset (raw 32
//       bytes, "" = DERO) v_<i> amount e_<i> expiry height st_<i> 0 open / 1 claimed / 2 refunded x_<i> revealed secret

// wmin: the minimum escrow width in blocks, 1,600 for the ZEC market (docs/ZEC_SWAP.md §5). A missing argument makes
// the DVM refuse the call, and line 6 refuses 0 and any width past Lock's million-block ceiling, so an install that
// leaves it out installs nothing. The same code with a smaller wmin has the same code hash, so the taker's companion
// reads wmin and refuses a deployment below its own policy.
Function Initialize(wmin Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
6 IF wmin == 0 || wmin > 1000000 THEN GOTO 900
10 STORE("kind", "swap")
11 STORE("v", 2)
12 STORE("n", 0)
13 STORE("wmin", wmin)
19 STORE("initd", 1)
20 RETURN 0
900 RETURN 1
End Function

// hash: 64 hex chars of sha256(secret), lower case. taker: the taker's address, required (line 26). expiry: absolute
// height, at least wmin and at most 1 000 000 ahead (line 24). asset: 64 hex chars of the asset SCID, or "" to lock
// the DERO sent with the call.
// Claim matches the stored hash against HEX(SHA256(raw)), and the DVM renders hex in lower case, so a hash written
// any other way names a lock nobody can ever claim — while a counter-chain script, which holds the same 32 bytes and
// cannot see how they were written, still pays. A maker who posts H in upper case therefore takes the taker's leg
// and refunds this one at expiry. Line 29 refuses that lock at the door: the hash must be exactly the lower-case hex
// of the bytes it names.
// The taker must be a registered address: a Claim's one transfer to an unregistered one is refused and reverted. The
// DVM cannot test registration without paying for it, so the maker's companion checks it before locking.
Function Lock(hash String, taker String, expiry Uint64, asset String) Uint64
10 DIM s, t, a as String
11 DIM v, id, h as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF STRLEN(hash) != 64 THEN GOTO 900
23 LET h = BLOCK_HEIGHT()
24 IF expiry <= h + 1 || expiry < h + LOAD("wmin") || expiry > h + 1000000 THEN GOTO 900
26 IF STRLEN(taker) == 0 THEN GOTO 900
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

// Reveal the secret before expiry. The funds go to the taker Lock named, whoever sends the call (line 29): the secret
// is public the moment this call reaches a mempool, so the sender decides nothing but who pays the fee. The sender
// must still be a ring-2 signer (line 21), and the call carries no value — no DERO (line 21) and not the lock's own
// asset (line 32), either of which would otherwise stay here for ever. The secret is 32 raw bytes given as 64 hex
// characters (a curve scalar fits), hashed as raw bytes: hash == sha256(bytes).
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
30 LET a = LOAD("a_" + id)
31 IF STRLEN(a) == 0 THEN GOTO 40
32 IF ASSETVALUE(a) != 0 THEN GOTO 900
40 STORE("st_" + id, 1)
41 STORE("x_" + id, secret)
42 IF STRLEN(a) != 0 THEN GOTO 46
44 SEND_DERO_TO_ADDRESS(t, LOAD("v_" + id))
45 GOTO 100
46 SEND_ASSET_TO_ADDRESS(t, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function

// After expiry the maker takes the funds back, with nothing attached: no DERO (line 21), not the lock's asset (28).
Function Refund(id Uint64) Uint64
10 DIM s, a as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF EXISTS("st_" + id) == 0 THEN GOTO 900
23 IF LOAD("st_" + id) != 0 THEN GOTO 900
24 IF BLOCK_HEIGHT() < LOAD("e_" + id) THEN GOTO 900
25 IF s != LOAD("m_" + id) THEN GOTO 900
26 LET a = LOAD("a_" + id)
27 IF STRLEN(a) == 0 THEN GOTO 30
28 IF ASSETVALUE(a) != 0 THEN GOTO 900
30 STORE("st_" + id, 2)
32 IF STRLEN(a) != 0 THEN GOTO 35
33 SEND_DERO_TO_ADDRESS(s, LOAD("v_" + id))
34 GOTO 100
35 SEND_ASSET_TO_ADDRESS(s, LOAD("v_" + id), a)
100 RETURN 0
900 RETURN 1
End Function
