// Veil social v1: opt-in profiles and follow graph. Trade posts live in the pools (t_<n>), never here.
// Addresses in keys are the DVM canonical string form (ADDRESS_STRING, always the dero1 prefix, see DECISIONS).
// Keys: pn_<addr> pb_<addr> pa_<addr> (name, bio, avatar)   f_<me>_<target> = height   fc_<me> = count   fl_<me>_<i> = target
//       fr_<target> = follower count
// No entrypoint takes value: each refuses DERO attached with RETURN 1, which the DVM discards and, at ring size 2,
// refunds (GAP-027). The DVM has no builtin that lists what else a call carries, so another asset attached is kept.

Function Initialize() Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 STORE("kind", "social")
20 STORE("v", 1)
29 STORE("initd", 1)
30 RETURN 0
900 RETURN 1
End Function

Function Profile(name String, bio String, avatar String) Uint64
10 DIM s, a as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 IF STRLEN(name) > 32 || STRLEN(bio) > 160 || STRLEN(avatar) > 200 THEN GOTO 900
23 LET a = ADDRESS_STRING(s)
30 STORE("pn_" + a, name)
31 STORE("pb_" + a, bio)
32 STORE("pa_" + a, avatar)
40 RETURN 0
900 RETURN 1
End Function

Function Follow(target String) Uint64
10 DIM s, me, t, tr as String
11 DIM n as Uint64
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 LET tr = ADDRESS_RAW(target)
23 IF IS_ADDRESS_VALID(tr) == 0 || tr == s THEN GOTO 900
24 LET me = ADDRESS_STRING(s)
25 LET t = ADDRESS_STRING(tr)
26 IF EXISTS("f_" + me + "_" + t) THEN GOTO 900
30 STORE("f_" + me + "_" + t, BLOCK_HEIGHT())
31 LET n = 0
32 IF EXISTS("fc_" + me) == 0 THEN GOTO 34
33 LET n = LOAD("fc_" + me)
34 STORE("fl_" + me + "_" + n, t)
35 STORE("fc_" + me, n + 1)
36 LET n = 0
37 IF EXISTS("fr_" + t) == 0 THEN GOTO 39
38 LET n = LOAD("fr_" + t)
39 STORE("fr_" + t, n + 1)
40 RETURN 0
900 RETURN 1
End Function

Function Unfollow(target String) Uint64
10 DIM s, me, t, tr as String
20 LET s = SIGNER()
21 IF IS_ADDRESS_VALID(s) == 0 || DEROVALUE() != 0 THEN GOTO 900
22 LET tr = ADDRESS_RAW(target)
23 IF IS_ADDRESS_VALID(tr) == 0 THEN GOTO 900
24 LET me = ADDRESS_STRING(s)
25 LET t = ADDRESS_STRING(tr)
26 IF EXISTS("f_" + me + "_" + t) == 0 THEN GOTO 900
30 DELETE("f_" + me + "_" + t)
31 STORE("fr_" + t, LOAD("fr_" + t) - 1)
40 RETURN 0
900 RETURN 1
End Function
