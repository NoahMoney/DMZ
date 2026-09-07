// DMZ chat v1: rooms of messages, append-only, on chain. There is no server: a client reads rooms straight from
// the contract with DERO.GetSC and pays a fee to write one. Keys, room naming and the display rules the client
// must follow: contracts/README.md (chat).
//
// PERMANENT AND UNMODERATED, on purpose. A deployed DMZ has no operator, so nothing here can ever be edited or
// removed by anyone, including whoever installed it. The fee is the whole of the spam control. Everything else —
// muting, hiding unknown senders, stripping control characters — happens in the client, on the reader's own
// device, and changes nothing about what is stored.
//
// The author is public. SIGNER() is real only at ring size 2, so posting attributably reveals the poster's
// address, exactly as publishing a trade does. That is the honest trade for an attributable message and the
// client says so before the first post.

Function InitializePrivate(treasury String, fee Uint64, maxlen Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM t as String
20 LET t = ADDRESS_RAW(treasury)
21 IF IS_ADDRESS_VALID(t) == 0 THEN GOTO 900
22 IF maxlen == 0 || maxlen > 400 THEN GOTO 900
30 STORE("kind", "chat")
31 STORE("v", 1)
32 STORE("treasury", t)
33 STORE("fee", fee)
34 STORE("maxlen", maxlen)
35 STORE("n", 0)
40 STORE("initd", 1)
50 RETURN 0
900 RETURN 1
End Function

// One message into one room. Stored under the room's own running index, so a client reads a room by asking for
// rn_<room> and then the last few rm_<room>_<j> — two round trips, no scan of anything it does not want.
// The record is author|height|text: split on the FIRST TWO separators only, because text may contain them.
Function Post(room String, text String) Uint64
10 DIM s as String
11 DIM j as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF DEROVALUE() != LOAD("fee") THEN GOTO 900
23 IF STRLEN(room) == 0 || STRLEN(room) > 32 THEN GOTO 900
24 IF STRLEN(text) == 0 || STRLEN(text) > LOAD("maxlen") THEN GOTO 900
30 LET j = 0
31 IF EXISTS("rn_" + room) == 0 THEN GOTO 40
32 LET j = LOAD("rn_" + room)
40 STORE("rm_" + room + "_" + j, ADDRESS_STRING(s) + "|" + BLOCK_HEIGHT() + "|" + text)
41 STORE("rn_" + room, j + 1)
42 STORE("n", LOAD("n") + 1)
50 IF LOAD("fee") == 0 THEN GOTO 100
51 SEND_DERO_TO_ADDRESS(LOAD("treasury"), LOAD("fee"))
100 RETURN 0
900 RETURN 1
End Function
