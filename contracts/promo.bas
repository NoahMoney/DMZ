// DMZ promotion: permissionless paid visibility, fixed at install (docs: contracts/README.md "promo.bas").
//
// One auction takes one asset, the quote: DERO, or one DERO-chain asset named by its SCID (a wrapped XMR or ZEC, as
// nfa.bas takes a quote). Bids in different assets cannot be ranked against each other without a price oracle, and there
// is none, so another asset is another install. In the DVM ASSETVALUE(zero id) IS DEROVALUE() and
// SEND_ASSET_TO_ADDRESS(a, n, zero id) IS SEND_DERO_TO_ADDRESS(a, n), so a DERO auction runs exactly this code.
//
// K slots and an epoch of E blocks, a minimum bid and a minimum increment in bps, the treasury and the quote, all written
// once by InitializePrivate; no owner, no setter, no UpdateCode. Epoch N is BLOCK_HEIGHT() / E. A bid placed in epoch N
// competes for display in epoch N + 1. When epoch N ends its K standing bids are epoch N + 1's winners with no call: the
// app reads them from storage (the slots whose ep_ is N). Nothing here needs a person to move it on.
//
// Two banks of K slots, bank = epoch % 2 (slot i = bank * K + j). Bids in epoch N write only bank N % 2, so the bank
// holding epoch N - 1's bids, which are on display during N, is never touched while they are displayed. A slot in the
// current bank whose ep_ is not N is free: it is empty, or it holds a bid from N - 2 or earlier, whose display is over.
//
// Bid(target, ep, raise), the quote attached. target is any 32-byte id (an H argument): the contract judges no target,
// and an app shows a winner only if it can verify what the id is. ep is the epoch the bidder means to bid in; the call is
// refused in any other, because a transaction executes up to about ten blocks after it is built and a bid built at an
// epoch's end would otherwise land in the next competition at a price nobody chose for it.
//   raise 0, a new bid: at least minbid. It takes a free slot of the bank if there is one; otherwise it must beat the
//     lowest standing bid by the increment (lowest + bps(lowest, incbps), and at least lowest + 1), and displaces it.
//     The displaced bid goes back to its bidder in the same call, in the quote, as the call's only transfer. Ties: among
//     equal lowest bids the latest placed (highest sq_) is displaced, so an earlier bid keeps its place. A bidder who
//     already stands for target in this epoch must raise instead.
//   raise 1: adds what is attached to the caller's own standing bid for target in this epoch. It displaces nobody.
// DERO attached to an auction in another asset is refused (the DVM hands a ring-2 caller everything back).
// A bid is recorded with its bidder's SIGNER(), so it needs ring size 2. At a larger ring there is no signer to record
// or refund: the quote attached to such a call cannot be returned to anyone, so it is credited to the proceeds (sx
// counts it), where it reaches the treasury like a winning bid, rather than staying in the contract unaccounted. Only
// DERO attached above ring 2 to an auction in another asset stays in the contract: nothing here can pay it anywhere.
//
// Proceeds. A bid whose ep_ is below the current epoch can never be displaced or refunded: it is final. ClaimProceeds,
// which anyone may send at any ring size, sweeps every final bid not yet counted (cl_ 0 -> 1) into what it pays, adds
// pr (final bids swept earlier, when a new bid reused their slot), and pays it all to the treasury, in the quote, as its
// only transfer. fc counts every unit paid; only ClaimProceeds moves it, only upward (the counter the treasury's bot
// proves a claim by, README rule 4b). Every call is O(K): Bid reads the K slots of one bank, ClaimProceeds all 2K.
//
// Conservation, in the quote: bi (bids and raises in) + sx (credited from calls with no signer) = rf (refunded) + fc
// (paid to the treasury) + pr + held, where held is the sum of bd_ over slots whose cl_ is 0; the quote balance = pr + held.
//
// Keys: kind slots epoch minbid incbps treasury (raw) quote (raw 32 bytes; zero = DERO) initd sq (next sequence)
//       pr fc bi rf sx
//       slot i: tg_<i> target (32 raw bytes) bd_<i> bid by_<i> bidder (raw, a signer) ep_<i> epoch placed sq_<i> order
//               cl_<i> 1 once swept into the proceeds

Function InitializePrivate(slots Uint64, epoch Uint64, minbid Uint64, incbps Uint64, treasury String, quote String) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM t, q as String
20 IF slots == 0 || slots > 10 || epoch < 20 || epoch > 1000000 THEN GOTO 900
21 IF minbid == 0 || minbid > 10000000000000 || incbps == 0 || incbps > 10000 THEN GOTO 900
22 LET t = ADDRESS_RAW(treasury)
23 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
24 LET q = DERO()
25 IF STRLEN(quote) == 0 THEN GOTO 28
26 IF STRLEN(quote) != 64 THEN GOTO 900
27 LET q = HEXDECODE(quote)
28 IF STRLEN(q) != 32 || q == SCID() || DEROVALUE() != 0 || ASSETVALUE(q) != 0 THEN GOTO 900
30 STORE("kind", "promo")
31 STORE("slots", slots)
32 STORE("epoch", epoch)
33 STORE("minbid", minbid)
34 STORE("incbps", incbps)
35 STORE("treasury", t)
36 STORE("quote", q)
37 STORE("sq", 0)
38 STORE("pr", 0)
39 STORE("fc", 0)
40 STORE("bi", 0)
41 STORE("rf", 0)
42 STORE("sx", 0)
50 STORE("initd", 1)
100 RETURN 0
900 RETURN 1
End Function

// 40-57 read the current bank once: own = the caller's standing bid for target, fr = the first free slot, lo = the
// lowest standing bid (lb its amount, lq its sequence). k means none. Each is a position j in the bank.
Function Bid(target String, ep Uint64, raise Uint64) Uint64
10 DIM s, x, q as String
11 DIM v, k, cur, b, j, i, fr, own, lo, lb, lq, need as Uint64
20 LET q = LOAD("quote")
21 LET v = ASSETVALUE(q)
22 LET s = SIGNER()
23 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 800
24 IF v == 0 || STRLEN(target) != 32 || raise > 1 || (q != DERO() && DEROVALUE() != 0) THEN GOTO 900
25 LET k = LOAD("slots")
26 LET cur = BLOCK_HEIGHT() / LOAD("epoch")
27 IF ep != cur THEN GOTO 900
28 LET b = (cur % 2) * k
29 LET fr = k
30 LET own = k
31 LET lo = k
32 LET j = 0
40 IF j >= k THEN GOTO 60
41 LET i = b + j
42 IF EXISTS("ep_" + i) == 0 THEN GOTO 53
43 IF LOAD("ep_" + i) != cur THEN GOTO 53
44 IF LOAD("by_" + i) != s || LOAD("tg_" + i) != target THEN GOTO 46
45 LET own = j
46 IF lo == k THEN GOTO 49
47 IF LOAD("bd_" + i) > lb THEN GOTO 56
48 IF LOAD("bd_" + i) == lb && LOAD("sq_" + i) < lq THEN GOTO 56
49 LET lo = j
50 LET lb = LOAD("bd_" + i)
51 LET lq = LOAD("sq_" + i)
52 GOTO 56
53 IF fr != k THEN GOTO 56
54 LET fr = j
56 LET j = j + 1
57 GOTO 40
60 IF raise == 0 THEN GOTO 70
61 IF own == k THEN GOTO 900
62 LET i = b + own
63 IF LOAD("bd_" + i) + v < v THEN GOTO 900
64 STORE("bd_" + i, LOAD("bd_" + i) + v)
65 STORE("bi", LOAD("bi") + v)
66 RETURN 0
70 IF own != k || v < LOAD("minbid") THEN GOTO 900
71 IF fr == k THEN GOTO 80
72 LET i = b + fr
73 IF EXISTS("cl_" + i) == 0 THEN GOTO 77
74 IF LOAD("cl_" + i) != 0 THEN GOTO 77
75 STORE("pr", LOAD("pr") + LOAD("bd_" + i))
77 put(i, target, v, s, cur)
78 RETURN 0
80 LET need = lb + bps(lb, LOAD("incbps"))
81 IF need > lb THEN GOTO 83
82 LET need = lb + 1
83 IF v < need || need < lb THEN GOTO 900
84 LET i = b + lo
85 LET x = LOAD("by_" + i)
86 STORE("rf", LOAD("rf") + lb)
87 put(i, target, v, s, cur)
88 SEND_ASSET_TO_ADDRESS(x, lb, q)
89 RETURN 0
800 IF v == 0 THEN GOTO 900
801 STORE("pr", LOAD("pr") + v)
802 STORE("sx", LOAD("sx") + v)
803 RETURN 0
900 RETURN 1
End Function

// Pays the treasury every final bid not yet swept, plus pr, in the quote. Anyone, any ring size; the treasury is the only
// payee. Anything attached is refused when there is a signer to refund it to; with none (ring > 2) the quote attached is
// paid on with the rest.
Function ClaimProceeds() Uint64
10 DIM cur, n, i, a, v as Uint64
11 DIM q as String
20 LET q = LOAD("quote")
21 LET v = ASSETVALUE(q)
22 IF IS_ADDRESS_VALID(SIGNER()) == 1 && (v != 0 || DEROVALUE() != 0) THEN GOTO 900
23 LET a = LOAD("pr") + v
24 IF v == 0 THEN GOTO 26
25 STORE("sx", LOAD("sx") + v)
26 LET cur = BLOCK_HEIGHT() / LOAD("epoch")
27 LET n = LOAD("slots") * 2
28 LET i = 0
30 IF i >= n THEN GOTO 50
31 IF EXISTS("ep_" + i) == 0 THEN GOTO 40
32 IF LOAD("ep_" + i) >= cur || LOAD("cl_" + i) != 0 THEN GOTO 40
33 LET a = a + LOAD("bd_" + i)
34 STORE("cl_" + i, 1)
40 LET i = i + 1
41 GOTO 30
50 IF a == 0 THEN GOTO 900
51 STORE("pr", 0)
52 STORE("fc", LOAD("fc") + a)
53 SEND_ASSET_TO_ADDRESS(LOAD("treasury"), a, q)
100 RETURN 0
900 RETURN 1
End Function

// ---- internals ----
// put writes bid v of signer s for target t, placed in epoch e, into slot i, and counts it in.
Function put(i Uint64, t String, v Uint64, s String, e Uint64) Uint64
10 DIM q as Uint64
20 LET q = LOAD("sq")
21 STORE("sq", q + 1)
22 STORE("tg_" + i, t)
23 STORE("bd_" + i, v)
24 STORE("by_" + i, s)
25 STORE("ep_" + i, e)
26 STORE("sq_" + i, q)
27 STORE("cl_" + i, 0)
28 STORE("bi", LOAD("bi") + v)
30 RETURN 0
End Function
// bps: x * f / 10000, floored, without overflow for f <= 10000 (founders.bas bps()).
Function bps(x Uint64, f Uint64) Uint64
10 RETURN (x / 10000) * f + ((x % 10000) * f) / 10000
End Function
