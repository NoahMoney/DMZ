// DMZ rate v1: community sentiment on a market. Each address holds at most one standing vote per target, Bullish or
// Suspicious / Scam, and may change or clear it at any time. Keys, and the display rules a client must follow:
// contracts/README.md (rate).
//
// IT IS A COUNT, NOT A VERDICT. The contract adds up what addresses said; it cannot tell one person with twenty
// addresses from twenty people, and it does not try. Nobody can change or remove another's vote, including whoever
// installed it: there is no owner, no moderator and no setter. The fee, fixed at install, is the only spam control.
//
// The voter is public. SIGNER() is real only at ring size 2, so each vote reveals its address, as a chat message does.
//
// Keys: x_<t>_<addr> = the standing vote (1 bullish, 2 suspicious; absent: none), b_<t> and s_<t> = how many standing
// votes of each kind target t has. t is the target's 32 bytes as 64 lower-case hex, written by HEX() from the H
// argument, so one market has exactly one spelling. n counts every accepted Vote.

Function InitializePrivate(treasury String, fee Uint64) Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 DIM t as String
20 LET t = ADDRESS_RAW(treasury)
21 IF IS_ADDRESS_VALID(t) == 0 || DEROVALUE() != 0 THEN GOTO 900
30 STORE("kind", "rate")
31 STORE("v", 1)
32 STORE("treasury", t)
33 STORE("fee", fee)
34 STORE("n", 0)
40 STORE("initd", 1)
50 RETURN 0
900 RETURN 1
End Function

// The counter a vote of kind c counts in.
Function ck(t String, c Uint64) String
10 IF c == 1 THEN GOTO 20
11 RETURN "s_" + t
20 RETURN "b_" + t
End Function

// v = 1 bullish, 2 suspicious, 0 withdraw. A vote pays exactly `fee`, forwarded to the treasury in the same call; a
// withdrawal carries nothing, because it only takes back the caller's own weight. A call that would change nothing
// (the same vote again, or a withdrawal with no vote standing) is refused, so nobody pays for nothing. The old vote's
// counter is taken down before the new one is counted, so b_<t> + s_<t> is always the number of standing votes.
Function Vote(target String, v Uint64) Uint64
10 DIM s, t, k, c as String
11 DIM o as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
22 IF STRLEN(target) != 32 || v > 2 THEN GOTO 900
23 LET t = HEX(target)
24 LET k = "x_" + t + "_" + ADDRESS_STRING(s)
25 LET o = 0
26 IF EXISTS(k) == 0 THEN GOTO 30
27 LET o = LOAD(k)
30 IF v == o THEN GOTO 900
31 IF o == 0 THEN GOTO 40
32 LET c = ck(t, o)
33 IF LOAD(c) == 0 THEN GOTO 900
34 STORE(c, LOAD(c) - 1)
40 STORE("n", LOAD("n") + 1)
41 IF v == 0 THEN GOTO 60
42 IF DEROVALUE() != LOAD("fee") THEN GOTO 900
43 STORE(k, v)
44 LET c = ck(t, v)
45 IF EXISTS(c) == 0 THEN GOTO 48
46 STORE(c, LOAD(c) + 1)
47 GOTO 50
48 STORE(c, 1)
50 IF LOAD("fee") == 0 THEN GOTO 100
51 SEND_DERO_TO_ADDRESS(LOAD("treasury"), LOAD("fee"))
52 RETURN 0
60 IF DEROVALUE() != 0 THEN GOTO 900
61 DELETE(k)
100 RETURN 0
900 RETURN 1
End Function
