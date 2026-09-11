//    Copyright 2022. Civilware. All rights reserved.
//    Artificer NFA Market Standard (ART-NFA-MS1)
//
// DMZ NFA: ART-NFA-MS1 v1.1.1 with a quote asset and a protocol fee (docs/NFA_BUILD.md §1). Immutable: no UpdateCode.
// Upstream: https://raw.githubusercontent.com/civilware/artificer-nfa-standard/main/ART-NFA-MS1/ART-NFA-MS1.bas
// at d286b19, sha256 6c42bb7eae4c44d7b39c0aeb5f184338449b4225ea2a925e0eb539783d8115b1 (13,184 bytes, LF; git blob
// 5aca6b91). Everything below is that file with these changes and no others:
//  1. The creator's metadata are InitializePrivate arguments rather than literals edited into the source, because
//     every DMZ NFA installs the one template the app verifies by hash (§3.B). After them come quote, protoFee and
//     treasury, which init() validates and stores: quote is "" (DERO) or the 64-hex SCID a listing is priced in;
//     protoFee is bps of every sale, paid to treasury; treasury must be a valid address. royalty and artificerFee
//     must each be <= 100 (they are multiplied by 100 below, and the standard's own sum check can wrap without
//     that) and ownerCanUpdate 0 or 1, the domain Update() reads.
//  2. Every DEROVALUE() that meant "the money offered" is ASSETVALUE(quote) and every SEND_DERO_TO_ADDRESS that
//     paid or refunded is SEND_ASSET_TO_ADDRESS(.., quote). In the DVM ASSETVALUE(zero hash) IS DEROVALUE() and
//     SEND_ASSET_TO_ADDRESS(a, n, zero hash) IS SEND_DERO_TO_ADDRESS(a, n) (dvm_functions.go), so a DERO-quoted NFA
//     does exactly what the standard does. On any other quote, DERO burned on a market call (Start, BuyItNow, Bid,
//     CloseListing, CancelListing) would sit in the contract forever, so it is refused with RETURN 1, which
//     refunds the whole call, as pool.bas does.
//  3. processDEROFinalPayment also takes protoFee for treasury, in the quote, in the same settlement as royalty,
//     artificerFee and charity. protoFee + 100 * (royalty + artificerFee + charityDonatePerc) <= 10000: init
//     checks it, and Start clamps the charity share to the room left exactly as the standard clamps against 100.
//  4. BuyItNow and Bid refund any amount above 1844674407370955 = floor((2^64 - 1) / 10000). Every product in
//     this contract is an amount times a number <= 10000, so under that bound none can wrap; above it the
//     standard's own royalty arithmetic already wraps. That is 18.4 billion DERO, unreachable, and 1,844 units of
//     a 12-decimal quote.
//  5. A settlement sends only to addresses that have signed a transaction: the buyer or winning bidder (the unit),
//     creatorAddr (royalty, the installer) and owner (the remainder). The artificerFee, charity and protoFee shares
//     are credited to ow_<address> by owe() and leave through PayOwed(payee), which anyone may call and which makes
//     exactly one transfer. Why: the DVM checks a call's transfer destinations against the account registry but
//     keeps only the LAST lookup's error (dvm/simulator.go SanityCheckExternalTransfers, 227-238), then commits
//     the state and panics at the first unregistered destination (ProcessExternal 356-359), and the node recovers
//     and keeps the block. The standard paid artificerAddr (hard-coded, unregistered on any chain but mainnet) and
//     charityDonateAddr (typed by the seller) before the owner, so one unregistered address completed the sale and
//     destroyed every share after it, the seller's included. A PayOwed to an unregistered address is refused whole
//     (its one transfer is the last) and the share waits until that account exists. The arithmetic of every share
//     is unchanged.
//  6. BuyItNow on an ENDED auction settles it to the highest bidder through processHighestBidder, as an empty Bid
//     does. The standard sent the unit back to the owner and left the winning bid in the contract under
//     currBidAddr/currBidAmt, which resetVars never clears, and a later empty Bid settled another listing to that
//     stale bid. Now every way an auction ends settles its bid (processHighestBidder, from CloseListing, Bid or
//     BuyItNow) or refunds it (CancelListing 460), and a sale never takes one, so no bid outlives its listing.
//     resetVars stays as it is: it runs after those DELETEs, and a key DELETEd earlier in the same call makes
//     EXISTS or LOAD of it panic in the DVM (dvm_store.go 81-98), so it cannot look for a bid itself.
//  7. Start stores a clamped charity share only after the charity address has been checked. The standard stored it
//     first, and its refusal path (400) returns 0, so a refused Start left a share with no address and every later
//     settlement reverted.
//  8. Every entrypoint refuses, with RETURN 1, any asset it does not take, where the standard kept it for ever: DERO
//     or the quote on ClaimOwnership and Start (they take the unit), anything on Update, CloseListing, CancelListing
//     and PayOwed, the unit on BuyItNow and Bid (their DERO guard, item 2, stays for a wrapped quote). A refused call
//     at ring 2 hands every attached asset back to the signer. Above ring 2 there is no signer to hand it to, so
//     ClaimOwnership and Start check after their ring>2 branch, which keeps the standard's path of sending the unit
//     to the recorded owner; only what else was attached to such a call stays in the contract.
// Line numbers are the standard's. The new lines sit in gaps the standard left (init 190-295, ClaimOwnership 25,
// Update 5, Start 35, BuyItNow 12-17, 65 and 512-516, Bid 12-21 and 85, CloseListing 5, CancelListing 20,
// processDEROFinalPayment 15-16 and 112-118);
// Start 150-165 are rewritten for the bps bound and 170 now jumps to the address check (175-185 are gone); BuyItNow
// 510 and processDEROFinalPayment 90 and 110 keep their numbers with new statements. PayOwed and owe() are new. No
// function is renumbered. The standard's four-space indentation is dropped: the chain is charged for every source
// byte, and tests/sendorder_test.go reads statements at column 0.
// Keys: the standard's, plus quote (raw 32 bytes) protoFee treasury (raw address) owed (total credited and not yet
// paid) ow_<address> (owed to that address) op_<address> (paid to it, only grows). The contract's quote balance is
// owed plus currBidAmt while an auction holds a bid.
// The one party this template adds is the treasury, and it holds no power here: it receives protoFee through
// PayOwed, which anyone may call. Every other gated call is the standard's (Update for creatorAddr, and for the
// owner if ownerCanUpdate is 1; Start, CloseListing and CancelListing for the asset's owner).

Function InitializePrivate(artificerFee Uint64, royalty Uint64, ownerCanUpdate Uint64, nameHdr String, descrHdr String, typeHdr String, iconURLHdr String, tagsHdr String, fileCheckC String, fileCheckS String, fileURL String, fileSignURL String, coverURL String, collection String, quote String, protoFee Uint64, treasury String) Uint64
10  IF EXISTS("owner") == 0 THEN GOTO 300 ELSE GOTO 999
300 STORE("artificerFee", artificerFee)
310 STORE("royalty", royalty)
320 STORE("ownerCanUpdate", ownerCanUpdate)
330 STORE("nameHdr", nameHdr)
340 STORE("descrHdr", descrHdr)
350 STORE("typeHdr", typeHdr)
360 STORE("iconURLHdr", iconURLHdr)
370 STORE("tagsHdr", tagsHdr)
400 STORE("fileCheckC", fileCheckC)
410 STORE("fileCheckS", fileCheckS)
420 STORE("fileURL", fileURL)
430 STORE("fileSignURL", fileSignURL)
440 STORE("coverURL", coverURL)
450 STORE("collection", collection)
500 IF init(quote, protoFee, treasury) == 0 THEN GOTO 600 ELSE GOTO 999
600 RETURN 0
999 RETURN 1
End Function

// quote is decoded and stored before it is checked, the way the standard stores artificerAddr at 40 and checks it
// at 50: a failed init returns 1 and the install never happens, so nothing half-validated can persist.
Function init(quote String, protoFee Uint64, treasury String) Uint64
10  IF EXISTS("owner") == 0 THEN GOTO 20 ELSE GOTO 999
20  STORE("owner", SIGNER())
30  STORE("creatorAddr", SIGNER())
40  STORE("artificerAddr", ADDRESS_RAW("dero1qy0khp9s9yw2h0eu20xmy9lth3zp5cacmx3rwt6k45l568d2mmcf6qgcsevzx"))
50  IF IS_ADDRESS_VALID(LOAD("artificerAddr")) == 1 THEN GOTO 60 ELSE GOTO 999
60  STORE("active", 0)
70  STORE("scBalance", 0)
80  STORE("cancelBuffer", 300)
90  STORE("startBlockTime", 0)
100 STORE("endBlockTime", 0)
110 STORE("bidCount", 0)
120 STORE("staticBidIncr", 10000)
130 STORE("percentBidIncr", 1000)
140 STORE("listType", "")
150 STORE("charityDonatePerc", 0)
160 STORE("startPrice", 0)
170 STORE("currBidPrice", 0)
180 STORE("version", "1.1.1")
190 STORE("owed", 0)
200 IF STRLEN(quote) == 0 THEN GOTO 210 ELSE GOTO 220
210 STORE("quote", DERO())
215 GOTO 240
220 IF STRLEN(quote) == 64 THEN GOTO 230 ELSE GOTO 999
230 STORE("quote", HEXDECODE(quote))
240 IF STRLEN(LOAD("quote")) == 32 THEN GOTO 250 ELSE GOTO 999
250 IF LOAD("quote") == SCID() THEN GOTO 999
260 IF protoFee > 10000 THEN GOTO 999
270 STORE("protoFee", protoFee)
280 STORE("treasury", ADDRESS_RAW(treasury))
290 IF IS_ADDRESS_VALID(LOAD("treasury")) == 1 THEN GOTO 295 ELSE GOTO 999
295 IF LOAD("royalty") > 100 || LOAD("artificerFee") > 100 || LOAD("ownerCanUpdate") > 1 THEN GOTO 999
500 IF LOAD("charityDonatePerc") * 100 + LOAD("artificerFee") * 100 + LOAD("royalty") * 100 + LOAD("protoFee") > 10000 THEN GOTO 999
600 SEND_ASSET_TO_ADDRESS(SIGNER(), 1, SCID())
610 RETURN 0
999 RETURN 1
End Function

// 25: anything attached but the unit is refused, after the ring>2 branch (20 -> 500): above ring 2 a refusal refunds
// nobody and would destroy the only unit, so that call keeps the standard's path to the recorded owner.
Function ClaimOwnership() Uint64
10  IF ASSETVALUE(SCID()) == 1 THEN GOTO 20 ELSE GOTO 999
20  IF ADDRESS_STRING(SIGNER()) == "" THEN GOTO 500
25  IF DEROVALUE() != 0 || ASSETVALUE(LOAD("quote")) != 0 THEN GOTO 999
30  transferOwnership(SIGNER())
40  SEND_ASSET_TO_ADDRESS(SIGNER(), 1, SCID())
50  RETURN 0
500 SEND_ASSET_TO_ADDRESS(LOAD("owner"), 1, SCID())
510 RETURN 0
999 RETURN 1
End Function

Function Update(iconURL String, coverURL String, fileURL String, fileSignURL String, tags String) Uint64
5   IF DEROVALUE() != 0 || ASSETVALUE(LOAD("quote")) != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 999
10  IF LOAD("creatorAddr") == SIGNER() THEN GOTO 40 ELSE GOTO 20
20  IF LOAD("ownerCanUpdate") == 1 THEN GOTO 30 ELSE GOTO 999
30  IF LOAD("owner") == SIGNER() THEN GOTO 40 ELSE GOTO 999
40  IF iconURL != "" THEN GOTO 50 ELSE GOTO 60
50  STORE("iconURLHdr", iconURL)
60  IF coverURL != "" THEN GOTO 70 ELSE GOTO 80
70  STORE("coverURL", coverURL)
80  IF fileURL != "" THEN GOTO 90 ELSE GOTO 100
90  STORE("fileURL", fileURL)
100 IF fileSignURL != "" THEN GOTO 110 ELSE GOTO 120
110 STORE("fileSignURL", fileSignURL)
120 IF tags != "" THEN GOTO 130 ELSE GOTO 140
130 STORE("tagsHdr", tags)
140 RETURN 0
999 RETURN 1
End Function

// 150-165: the standard clamps charityDonatePerc so that charity + artificerFee + royalty <= 100. With protoFee
// in bps the room left is (10000 - protoFee - 100 * artificerFee - 100 * royalty) / 100 percent, which is the
// standard's 100 - artificerFee - royalty when protoFee is 0. Comparing the argument instead of adding to it
// also means a caller's absurd charityDonatePerc can no longer wrap the sum past the check. 170: the clamped share
// goes through the same address check as an unclamped one (195) and is stored only after it (200-205); the
// standard stored it at 170, before the check, and the refusal at 400 committed it. 35: DERO or the quote attached
// is refused, after the ring>2 branch (30 -> 600) for the same reason as ClaimOwnership 25.
Function Start(listType String, duration Uint64, startPrice Uint64, charityDonateAddr String, charityDonatePerc Uint64) Uint64
10  dim tempPercCount as Uint64
20  dim err as String
30  IF ADDRESS_STRING(SIGNER()) == "" THEN GOTO 600
35  IF DEROVALUE() != 0 || ASSETVALUE(LOAD("quote")) != 0 THEN GOTO 999
40  IF ASSETVALUE(SCID()) == 1 THEN GOTO 70 ELSE GOTO 400
70  IF listType == "auction" THEN GOTO 100 ELSE GOTO 80
80  IF listType == "sale" THEN GOTO 100 ELSE GOTO 400
100 IF LOAD("owner") == SIGNER() THEN GOTO 110 ELSE GOTO 400
110 IF checkActive(LOAD("listType")) == 999 THEN GOTO 150 ELSE GOTO 400
150 LET tempPercCount = (10000 - LOAD("protoFee") - LOAD("artificerFee") * 100 - LOAD("royalty") * 100) / 100
155 IF charityDonatePerc > tempPercCount THEN GOTO 165 ELSE GOTO 190
165 LET charityDonatePerc = tempPercCount
170 GOTO 195
190 IF charityDonatePerc > 0 THEN GOTO 195 ELSE GOTO 210
195 IF IS_ADDRESS_VALID(ADDRESS_RAW(charityDonateAddr)) == 1 THEN GOTO 200 ELSE GOTO 400
200 STORE("charityDonatePerc", charityDonatePerc)
205 STORE("charityDonateAddr", ADDRESS_RAW(charityDonateAddr))
210 STORE("listType", listType)
220 STORE("scBalance", 1)
230 STORE("startBlockTime", BLOCK_TIMESTAMP())
240 STORE("endBlockTime", generateEndBlock(duration, BLOCK_TIMESTAMP()))
250 STORE("startPrice", startPrice)
270 STORE("active", 1)
300 RETURN 0
400 IF ASSETVALUE(SCID()) > 0 THEN GOTO 410 ELSE GOTO 999
410 SEND_ASSET_TO_ADDRESS(SIGNER(), ASSETVALUE(SCID()), SCID())
420 RETURN 0
600 IF ASSETVALUE(SCID()) > 0 THEN GOTO 610 ELSE GOTO 999
610 SEND_ASSET_TO_ADDRESS(LOAD("owner"), ASSETVALUE(SCID()), SCID())
620 RETURN 0
999 RETURN 1
End Function

// 512-516: checkActive answers 111 for any ended listing before it compares the type, so an ended auction reaches
// this branch too. An auction is settled to its highest bidder (or back to the owner if nobody bid), exactly as an
// empty Bid after the end settles it; only an ended sale sends the unit back at 520. The caller's own attachment is
// refunded at 920 either way.
Function BuyItNow() Uint64
10  dim activeFlag as Uint64
12  dim q as String
15  IF ADDRESS_STRING(SIGNER()) == "" THEN GOTO 999
16  LET q = LOAD("quote")
17  IF (q != DERO() && DEROVALUE() != 0) || ASSETVALUE(SCID()) != 0 THEN GOTO 999
20  IF LOAD("owner") == SIGNER() THEN GOTO 920 ELSE GOTO 30
30  LET activeFlag = checkActive("sale")
40  IF activeFlag == 0 THEN GOTO 50 ELSE GOTO 500
50  IF LOAD("scBalance") == 1 THEN GOTO 60 ELSE GOTO 920
60  IF ASSETVALUE(q) >= LOAD("startPrice") THEN GOTO 65 ELSE GOTO 920
65  IF ASSETVALUE(q) > 1844674407370955 THEN GOTO 920
70  SEND_ASSET_TO_ADDRESS(SIGNER(), LOAD("scBalance"), SCID())
80  STORE("scBalance", 0)
90  processDEROFinalPayment(ASSETVALUE(q))
95  transferOwnership(SIGNER())
96  resetVars(1)
97  STORE("previousSalePrice", ASSETVALUE(q))
100 RETURN 0
500 IF activeFlag == 999 THEN GOTO 920 ELSE GOTO 510
510 IF activeFlag == 111 THEN GOTO 512 ELSE GOTO 920
512 IF LOAD("listType") != "auction" THEN GOTO 520
514 processHighestBidder()
516 GOTO 920
520 SEND_ASSET_TO_ADDRESS(LOAD("owner"), LOAD("scBalance"), SCID())
530 STORE("scBalance", 0)
540 resetVars(1)
920 IF ASSETVALUE(q) > 0 THEN GOTO 925 ELSE GOTO 930
925 SEND_ASSET_TO_ADDRESS(SIGNER(), ASSETVALUE(q), q)
930 RETURN 0
999 RETURN 1
End Function

Function Bid() Uint64
10  dim activeFlag, bidAmt as Uint64
12  dim q as String
15  IF ADDRESS_STRING(SIGNER()) == "" THEN GOTO 999
20  LET q = LOAD("quote")
21  IF (q != DERO() && DEROVALUE() != 0) || ASSETVALUE(SCID()) != 0 THEN GOTO 999
25  LET bidAmt = ASSETVALUE(q)
30  IF LOAD("owner") == SIGNER() THEN GOTO 920 ELSE GOTO 35
35  LET activeFlag = checkActive("auction")
40  IF activeFlag == 0 THEN GOTO 50 ELSE GOTO 500
50  IF LOAD("scBalance") == 1 THEN GOTO 51 ELSE GOTO 920
51  IF EXISTS(SIGNER() + "-bidDate") == 1 THEN GOTO 60 ELSE GOTO 70
60  IF LOAD(SIGNER() + "-bidDate") < BLOCK_TIMESTAMP() THEN GOTO 70 ELSE GOTO 920
70  IF bidAmt >= LOAD("startPrice") THEN GOTO 80 ELSE GOTO 920
80  IF bidAmt >= LOAD("currBidPrice") THEN GOTO 85 ELSE GOTO 920
85  IF bidAmt > 1844674407370955 THEN GOTO 920
90  STORE("currBidPrice", findLesserIncrease(bidAmt))
100 outbidReturns()
120 STORE("currBidAddr", SIGNER())
130 STORE("currBidAmt", bidAmt)
140 STORE(SIGNER() + "-bidDate", BLOCK_TIMESTAMP())
150 STORE("bidCount", LOAD("bidCount") + 1)
170 IF LOAD("endBlockTime") - 900 <= BLOCK_TIMESTAMP() THEN GOTO 180 ELSE GOTO 190
180 STORE("endBlockTime", BLOCK_TIMESTAMP() + 900)
190 RETURN 0
500 IF activeFlag == 999 THEN GOTO 920 ELSE GOTO 510
510 IF activeFlag == 111 THEN GOTO 520 ELSE GOTO 920
520 IF bidAmt > 0 THEN GOTO 530 ELSE GOTO 540
530 SEND_ASSET_TO_ADDRESS(SIGNER(), bidAmt, q)
540 processHighestBidder()
550 RETURN 0
920 IF bidAmt > 0 THEN GOTO 925 ELSE GOTO 930
925 SEND_ASSET_TO_ADDRESS(SIGNER(), bidAmt, q)
930 RETURN 0
999 RETURN 1
End Function

Function CloseListing() Uint64
5   IF DEROVALUE() != 0 || ASSETVALUE(LOAD("quote")) != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 999
10  IF LOAD("owner") == SIGNER() THEN GOTO 20 ELSE GOTO 999
20  IF checkActive(LOAD("listType")) == 111 THEN GOTO 30 ELSE GOTO 999
30  IF LOAD("listType") == "auction" THEN GOTO 40 ELSE GOTO 200
40  IF LOAD("bidCount") > 0 THEN GOTO 50 ELSE GOTO 210
50  processHighestBidder()
60  RETURN 0
200 IF LOAD("listType") == "sale" THEN GOTO 210 ELSE GOTO 999
210 SEND_ASSET_TO_ADDRESS(LOAD("owner"), LOAD("scBalance"), SCID())
220 STORE("scBalance", 0)
230 resetVars(1)
240 RETURN 0
999 RETURN 1
End Function

Function CancelListing() Uint64
10  dim tempCounter as Uint64
20  IF DEROVALUE() != 0 || ASSETVALUE(LOAD("quote")) != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 999
30  IF LOAD("owner") == SIGNER() THEN GOTO 50 ELSE GOTO 999
50  IF checkActive(LOAD("listType")) == 0 THEN GOTO 60 ELSE GOTO 999
60  IF (LOAD("startBlockTime") + LOAD("cancelBuffer")) >= BLOCK_TIMESTAMP() THEN GOTO 460 ELSE GOTO 999
460 outbidReturns()
600 SEND_ASSET_TO_ADDRESS(LOAD("owner"), LOAD("scBalance"), SCID())
610 STORE("scBalance", 0)
620 resetVars(1)
630 RETURN 0
999 RETURN 1
End Function

Function transferOwnership(newOwner String) Uint64
10  IF LOAD("owner") == newOwner THEN GOTO 40 ELSE GOTO 20
20  STORE("previousOwner", LOAD("owner"))
30  STORE("owner", newOwner)
40  RETURN 0
End Function

Function generateEndBlock(duration Uint64, startBlockTime Uint64) Uint64
10  dim timeinseconds, endBlockTime as Uint64
20  LET timeinseconds = 3600 * duration
30  IF timeinseconds == 0 THEN GOTO 40 ELSE GOTO 50
40  LET timeinseconds = 3600
50  IF timeinseconds > 604800 THEN GOTO 60 ELSE GOTO 70
60  LET timeinseconds = 604800
70  LET endBlockTime = startBlockTime + timeinseconds
80  RETURN endBlockTime
End Function

Function checkActive(listType String) Uint64
10  IF LOAD("startBlockTime") <= BLOCK_TIMESTAMP() THEN GOTO 30 ELSE GOTO 900
30  IF LOAD("scBalance") == 1 THEN GOTO 40 ELSE GOTO 900
40  IF LOAD("endBlockTime") > BLOCK_TIMESTAMP() THEN GOTO 50 ELSE GOTO 500
50  IF LOAD("listType") == listType THEN GOTO 200 ELSE GOTO 910
200 STORE("active", 1)
210 RETURN 0
500 STORE("active", 0)
520 RETURN 111
900 STORE("active", 0)
910 RETURN 999
End Function

Function processHighestBidder() Uint64
10  dim bidAmt as Uint64
20  dim bidAddr as String
30  LET bidAddr = LOAD("owner")
100 IF EXISTS("currBidAmt") == 1 THEN GOTO 110 ELSE GOTO 310
110 IF EXISTS("currBidAddr") == 1 THEN GOTO 120 ELSE GOTO 310
120 IF LOAD("currBidAddr") != "" THEN GOTO 130 ELSE GOTO 310
130 IF LOAD("currBidAmt") > 0 THEN GOTO 140 ELSE GOTO 310
140 LET bidAddr = LOAD("currBidAddr")
150 LET bidAmt = LOAD("currBidAmt")
310 SEND_ASSET_TO_ADDRESS(bidAddr, LOAD("scBalance"), SCID())
320 processDEROFinalPayment(bidAmt)
350 transferOwnership(bidAddr)
360 STORE("scBalance", 0)
370 IF EXISTS(bidAddr + "-bidDate") == 1 THEN GOTO 380 ELSE GOTO 390
380 DELETE(bidAddr + "-bidDate")
390 DELETE("currBidAddr")
400 DELETE("currBidAmt")
410 IF bidAmt > 0 THEN GOTO 420 ELSE GOTO 600
420 STORE("previousAuctionPrice", bidAmt)
600 resetVars(1)
610 RETURN 0
End Function

Function outbidReturns() Uint64
20  IF EXISTS("currBidAddr") == 1 THEN GOTO 30 ELSE GOTO 900
30  IF EXISTS("currBidAmt") == 1 THEN GOTO 40 ELSE GOTO 900
40  IF LOAD("currBidAmt") > 0 THEN GOTO 50 ELSE GOTO 900
50  IF LOAD("currBidAddr") != "" THEN GOTO 60 ELSE GOTO 900
60  SEND_ASSET_TO_ADDRESS(LOAD("currBidAddr"), LOAD("currBidAmt"), LOAD("quote"))
800 DELETE(LOAD("currBidAddr") + "-bidDate")
810 DELETE("currBidAddr")
820 DELETE("currBidAmt")
900 RETURN 0
End Function

// The settlement, in the quote. Each share floors, so the four together never exceed saleAmt and the owner's
// remainder cannot wrap; the bound on saleAmt (BuyItNow 65, Bid 85) keeps every product under 2^64. The royalty
// and the remainder are sent now: creatorAddr and owner are addresses that signed a transaction here (init 30;
// ClaimOwnership 30, BuyItNow 95 and processHighestBidder 350 record only a signer), so the account exists. The
// other three are owed (90, 110, 118) and paid by PayOwed.
Function processDEROFinalPayment(saleAmt Uint64) Uint64
10  dim payoutAmt, royaltyPaymt, artificerPaymt, charityPaymt, protoPaymt as Uint64
15  dim q as String
16  LET q = LOAD("quote")
20  IF saleAmt == 0 THEN GOTO 200 ELSE GOTO 60
60  IF LOAD("royalty") > 0 THEN GOTO 65 ELSE GOTO 80
65  LET royaltyPaymt = LOAD("royalty") * saleAmt / 100
66  IF royaltyPaymt > 0 THEN GOTO 70 ELSE GOTO 80
70  SEND_ASSET_TO_ADDRESS(LOAD("creatorAddr"), royaltyPaymt, q)
80  IF LOAD("artificerFee") > 0 THEN GOTO 85 ELSE GOTO 100
85  LET artificerPaymt = LOAD("artificerFee") * saleAmt / 100
86  IF artificerPaymt > 0 THEN GOTO 90 ELSE GOTO 100
90  owe(LOAD("artificerAddr"), artificerPaymt)
100 IF LOAD("charityDonatePerc") > 0 THEN GOTO 105 ELSE GOTO 112
105 LET charityPaymt = LOAD("charityDonatePerc") * saleAmt / 100
106 IF charityPaymt > 0 THEN GOTO 110 ELSE GOTO 112
110 owe(LOAD("charityDonateAddr"), charityPaymt)
112 IF LOAD("protoFee") > 0 THEN GOTO 114 ELSE GOTO 120
114 LET protoPaymt = LOAD("protoFee") * saleAmt / 10000
116 IF protoPaymt > 0 THEN GOTO 118 ELSE GOTO 120
118 owe(LOAD("treasury"), protoPaymt)
120 LET payoutAmt = saleAmt - royaltyPaymt - artificerPaymt - charityPaymt - protoPaymt
125 IF payoutAmt > 0 THEN GOTO 130 ELSE GOTO 200
130 SEND_ASSET_TO_ADDRESS(LOAD("owner"), payoutAmt, q)
200 RETURN 0
End Function

// Credits amt of the quote to payee (a raw address that init or Start validated). Internal: the DVM refuses an
// entrypoint whose name does not start with a capital letter.
Function owe(payee String, amt Uint64) Uint64
10  dim k as String
20  LET k = ADDRESS_STRING(payee)
30  IF EXISTS("ow_" + k) == 0 THEN GOTO 60
40  STORE("ow_" + k, LOAD("ow_" + k) + amt)
50  GOTO 70
60  STORE("ow_" + k, amt)
70  STORE("owed", LOAD("owed") + amt)
80  RETURN 0
End Function

// Pays payee (an address string) everything owed to it, in the quote. Anyone may call it, at any ring size: the
// transfer goes to the payee, never to the caller. It is the call's only transfer, so if the payee's account does
// not exist yet the DVM refuses the whole call and the share stays owed until it does. Nothing may be attached.
Function PayOwed(payee String) Uint64
10  dim a, k as String
20  dim amt as Uint64
30  IF DEROVALUE() != 0 || ASSETVALUE(LOAD("quote")) != 0 || ASSETVALUE(SCID()) != 0 THEN GOTO 999
40  LET a = ADDRESS_RAW(payee)
50  IF IS_ADDRESS_VALID(a) == 0 THEN GOTO 999
60  LET k = ADDRESS_STRING(a)
70  IF EXISTS("ow_" + k) == 0 THEN GOTO 999
80  LET amt = LOAD("ow_" + k)
90  DELETE("ow_" + k)
100 STORE("owed", LOAD("owed") - amt)
110 IF EXISTS("op_" + k) == 0 THEN GOTO 140
120 STORE("op_" + k, LOAD("op_" + k) + amt)
130 GOTO 150
140 STORE("op_" + k, amt)
150 SEND_ASSET_TO_ADDRESS(a, amt, LOAD("quote"))
160 RETURN 0
999 RETURN 1
End Function

Function resetVars(forceReset Uint64) Uint64
10  IF forceReset == 0 THEN GOTO 20 ELSE GOTO 30
20  IF checkActive(LOAD("listType")) == 999 THEN GOTO 20 ELSE GOTO 900
30  STORE("startBlockTime", 0)
40  STORE("endBlockTime", 0)
50  STORE("bidCount", 0)
60  STORE("active", 0)
80  STORE("startPrice", 0)
90  STORE("currBidPrice", 0)
100 STORE("listType", "")
110 STORE("charityDonateAddr", "")
120 STORE("charityDonatePerc", 0)
200 RETURN 0
900 RETURN 999
End Function

Function findLesserIncrease(bidAmt Uint64) Uint64
10  dim percentCalc, staticCalc as Uint64
20  LET percentCalc = bidAmt + (bidAmt * LOAD("percentBidIncr") / 10000)
30  LET staticCalc = bidAmt + LOAD("staticBidIncr")
50  IF percentCalc < staticCalc THEN GOTO 100 ELSE GOTO 200
100 RETURN percentCalc
200 RETURN staticCalc
End Function
