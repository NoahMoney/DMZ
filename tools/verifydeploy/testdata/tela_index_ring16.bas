Function InitializePrivate() Uint64
10 IF init() == 0 THEN GOTO 30
20 RETURN 1
30 STORE("var_header_name", "DMZ")
31 STORE("var_header_description", "DMZ: social DEX and launchpad for DERO assets")
32 STORE("var_header_icon", "")
33 STORE("dURL", "dmz.tela")
34 STORE("mods", "")
40 STORE("DOC1", "d33c76ab7250af76c12efbf6b79e7b0684c4c3a2ac76717c3ec64a681cb417e1")
41 STORE("DOC2", "b85ee690427d55f66636c4357ae86843c2ed42d3203f4c853d743861224a5de8")
42 STORE("DOC3", "b077c957501bc2805522b10df1b74d5870166fca04a4335b6719f98089bb85e1")
43 STORE("DOC4", "a23f78d4d23da0db567973ab9df7241d64b2761cfb7c2e34a78a5672007ed3d2")
44 STORE("DOC5", "bbeb72be04d2397f5ac9f3820a446b1ca9c9c9ca5f62c1368f0d1f334ed4bd8c")
45 STORE("DOC6", "f9d9afdc3e738ce8a6eeb3186bf72c2857d8ddd603d2e0c29f8cbb474be76d93")
46 STORE("DOC7", "c659bf71c4ba5c564faf725953478190c54ba6094cf862bbb286ba5a3a4486bb")
47 STORE("DOC8", "bec344befb05c4fa03c47f05fa48c38603c482c4c1f082a4c9b1401029909f84")
1000 RETURN 0
End Function

Function init() Uint64
10 IF EXISTS("owner") == 0 THEN GOTO 30
20 RETURN 1
30 STORE("owner", address())
50 STORE("telaVersion", "1.1.0")
60 STORE("commit", 0)
70 STORE(0, HEX(TXID()))
80 STORE("hash", HEX(TXID()))
85 STORE("likes", 0)
90 STORE("dislikes", 0)
100 RETURN 0
End Function

Function address() String
10 DIM s as String
20 LET s = SIGNER()
30 IF IS_ADDRESS_VALID(s) THEN GOTO 50
40 RETURN "anon"
50 RETURN ADDRESS_STRING(s)
End Function

Function Rate(r Uint64) Uint64
10 DIM addr as String
15 LET addr = address()
16 IF r < 100 && EXISTS(addr) == 0 && addr != "anon" THEN GOTO 30
20 RETURN 1
30 STORE(addr, ""+r+"_"+BLOCK_HEIGHT())
40 IF r < 50 THEN GOTO 70
50 STORE("likes", LOAD("likes")+1)
60 RETURN 0
70 STORE("dislikes", LOAD("dislikes")+1)
100 RETURN 0
End Function

Function UpdateCode(code String, mods String) Uint64
10 IF LOAD("owner") == "anon" THEN GOTO 20
15 IF code == "" THEN GOTO 20
16 IF LOAD("owner") == address() THEN GOTO 30
20 RETURN 1
30 UPDATE_SC_CODE(code)
40 STORE("commit", LOAD("commit")+1)
50 STORE(LOAD("commit"), HEX(TXID()))
60 STORE("hash", HEX(TXID()))
70 STORE("mods", mods)
100 RETURN 0
End Function