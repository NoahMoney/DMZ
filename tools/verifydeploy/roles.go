package main

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// The privileged-role table: for every contract kind, every storage key that holds a role, what that role can do,
// and the source line that reads or writes it. One entry per kind; the verifier evaluates each entry against the
// variables DERO.GetSC returns for a deployed contract of that kind.
//
// Three tests keep this table honest against contracts/ as it changes (roles_test.go):
//   - TestRoleCitations: every cited line still contains the text it is cited for;
//   - TestRoleWriters: the functions that STORE or DELETE a role's keys are exactly the ones listed in Writers —
//     so a new setter (a function that could hand a role back, raise a fee, move the treasury) fails the test
//     until the table accounts for it;
//   - TestRoleKinds: every contracts/*.bas has an entry.
//
// Policy decides the exit status:
//   must end   a deployer power. Live after the documented revocation ceremony = FAIL, unless -allow names it.
//   guard      the one-time initialisation guard. Missing = FAIL: Initialize could run again.
//   by design  a power the design keeps: a market's own creator, or a k-of-n federation. Listed (a federation WARN).
//   payee      receives money; at most it can pull what is already its own. Listed.
//   parameter  a number fixed at install, printed with whatever can still change it. Listed.
//   inert      stored, but no function reads it for authority. Listed.

type policy string

const (
	pRevoke policy = "must end"
	pGuard  policy = "guard"
	pDesign policy = "by design"
	pPayee  policy = "payee"
	pFixed  policy = "parameter"
	pInert  policy = "inert"
)

type cite struct {
	File string // repository-relative
	Line int
	Has  string // text the line must contain
}

func (c cite) String() string { return fmt.Sprintf("%s:%d", path.Base(c.File), c.Line) }

// compactCites renders citations as "factory.bas:25,39-40,50-51", one entry per file in first-seen order.
func compactCites(cs []cite) []string {
	var files []string
	lines := map[string][]int{}
	for _, c := range cs {
		f := path.Base(c.File)
		if _, ok := lines[f]; !ok {
			files = append(files, f)
		}
		lines[f] = append(lines[f], c.Line)
	}
	var out []string
	for _, f := range files {
		ls := lines[f]
		sort.Ints(ls)
		var parts []string
		for i := 0; i < len(ls); {
			j := i
			for j+1 < len(ls) && ls[j+1] <= ls[j]+1 {
				j++
			}
			if j > i {
				parts = append(parts, fmt.Sprintf("%d-%d", ls[i], ls[j]))
			} else {
				parts = append(parts, fmt.Sprint(ls[i]))
			}
			i = j + 1
		}
		out = append(out, f+":"+strings.Join(parts, ","))
	}
	return out
}

type roleSpec struct {
	Role    string
	Keys    []string // "prefix*" for a family of keys
	Policy  policy
	Warn    bool     // a by-design role worth a WARN whenever it is live (custody)
	Writers []string // every function that STOREs or DELETEs one of Keys
	Cites   []cite
	Eval    func(v vars) (val string, live bool, state string)
}

type kindSpec struct {
	Kind  string // contracts/<Kind>.bas
	About string
	Roles []roleSpec
}

func c(file string, line int, has string) cite { return cite{"contracts/" + file, line, has} }

// initGuard is the re-entry guard every DMZ contract has: its Initialize / InitializePrivate is an exported
// function (the DVM refuses only lower-case entrypoints, vendor/derohe/dvm/dvm.go 361-371), so without the guard
// anyone could run it again and overwrite the contract's roles.
func initGuard(file string, guardLine int, guardHas string, storeLine int, storeHas string, writer string) roleSpec {
	return roleSpec{
		Role: "initd", Keys: []string{"initd"}, Policy: pGuard, Writers: []string{writer},
		Cites: []cite{c(file, guardLine, guardHas), c(file, storeLine, storeHas)},
		Eval: func(v vars) (string, bool, string) {
			if n, ok := v.u("initd"); ok && n != 0 {
				return "initd = " + v.num("initd"), false, writer + " refuses a second run (initd exists)"
			}
			return "(absent)", true, "initd is absent: " + writer + " can be called again and would overwrite every role"
		},
	}
}

// payeeRole is a treasury that only receives.
func payeeRole(role, key string, writers []string, cites []cite, what string) roleSpec {
	return roleSpec{
		Role: role, Keys: []string{key}, Policy: pPayee, Writers: writers, Cites: cites,
		Eval: func(v vars) (string, bool, string) {
			return v.keyVal(key), false, what
		},
	}
}

// fixedRole is a set of numbers written only at install.
func fixedRole(role string, keys []string, writers []string, cites []cite, what string) roleSpec {
	return roleSpec{
		Role: role, Keys: keys, Policy: pFixed, Writers: writers, Cites: cites,
		Eval: func(v vars) (string, bool, string) {
			parts := make([]string, 0, len(keys))
			for _, k := range keys {
				parts = append(parts, k+"="+v.num(k))
			}
			return strings.Join(parts, " "), false, what
		},
	}
}

const guardText = `IF EXISTS("initd") != 0 THEN GOTO 900`

var roleTable = []kindSpec{
	{
		Kind:  "factory",
		About: "one owner until Renounce; the registry itself is open to anyone who pays regfee",
		Roles: []roleSpec{
			{
				Role: "owner", Keys: []string{"owner", "renounced"}, Policy: pRevoke,
				Writers: []string{"Initialize", "Renounce"},
				Cites: []cite{
					c("factory.bas", 25, `STORE("owner", s)`),
					c("factory.bas", 39, `IF EXISTS("owner") == 0 THEN GOTO 900`), c("factory.bas", 40, `IF SIGNER() != LOAD("owner")`),
					c("factory.bas", 50, `IF EXISTS("owner") == 0 THEN GOTO 900`), c("factory.bas", 51, `IF SIGNER() != LOAD("owner")`),
					c("factory.bas", 61, `IF EXISTS("owner") == 0 THEN GOTO 900`), c("factory.bas", 62, `IF SIGNER() != LOAD("owner")`),
					c("factory.bas", 63, `DELETE("owner")`), c("factory.bas", 64, `STORE("renounced", BLOCK_HEIGHT())`),
				},
				Eval: func(v vars) (string, bool, string) {
					if v.has("owner") {
						return v.keyVal("owner"), true, "can call SetTreasury (moves every future registration fee), SetRegFee and Renounce; the ceremony ends it with Renounce"
					}
					if v.has("renounced") {
						return "(deleted by Renounce at height " + v.num("renounced") + ")", false, "owner is absent: SetTreasury, SetRegFee and Renounce refuse every signer at their EXISTS guard, and nothing can store an owner again"
					}
					return "(absent)", false, "owner is absent (no renounced height recorded): the setters refuse every signer at their EXISTS guard"
				},
			},
			{
				Role: "treasury", Keys: []string{"treasury", "tstr"}, Policy: pPayee,
				Writers: []string{"Initialize", "SetTreasury"},
				Cites:   []cite{c("factory.bas", 26, `STORE("treasury", t)`), c("factory.bas", 43, `STORE("treasury", t)`), c("factory.bas", 110, `SEND_DERO_TO_ADDRESS(LOAD("treasury"), DEROVALUE())`)},
				Eval: func(v vars) (string, bool, string) {
					s := "receives every registration fee (reg, line 35); cannot act"
					if v.has("owner") {
						s += "; the owner can still move it (SetTreasury)"
					} else {
						s += "; fixed for ever (SetTreasury needs an owner)"
					}
					return v.keyVal("treasury"), false, s
				},
			},
			{
				Role: "regfee", Keys: []string{"regfee"}, Policy: pFixed,
				Writers: []string{"Initialize", "SetRegFee"},
				Cites:   []cite{c("factory.bas", 28, `STORE("regfee", regfee)`), c("factory.bas", 52, `STORE("regfee", regfee)`), c("factory.bas", 108, `IF DEROVALUE() < LOAD("regfee")`)},
				Eval: func(v vars) (string, bool, string) {
					if v.has("owner") {
						return "regfee=" + v.num("regfee"), false, "the owner can still change it (SetRegFee)"
					}
					return "regfee=" + v.num("regfee"), false, "fixed for ever (SetRegFee needs an owner)"
				},
			},
			initGuard("factory.bas", 17, guardText, 32, `STORE("initd", 1)`, "Initialize"),
		},
	},
	{
		Kind:  "social",
		About: "no owner, no fee: a profile or a follow is written only by its own signer",
		Roles: []roleSpec{initGuard("social.bas", 9, guardText, 12, `STORE("initd", 1)`, "Initialize")},
	},
	{
		Kind:  "chat",
		About: "no owner, no moderator: nothing stored can be edited or removed by anyone",
		Roles: []roleSpec{
			payeeRole("treasury", "treasury", []string{"InitializePrivate"},
				[]cite{c("chat.bas", 22, `STORE("treasury", t)`), c("chat.bas", 49, `SEND_DERO_TO_ADDRESS(LOAD("treasury"), LOAD("fee"))`)},
				"receives each post's fee (Post, line 51); cannot act; fixed for ever"),
			fixedRole("fee", []string{"fee", "maxlen"}, []string{"InitializePrivate"},
				[]cite{c("chat.bas", 23, `STORE("fee", fee)`), c("chat.bas", 24, `STORE("maxlen", maxlen)`), c("chat.bas", 39, `IF DEROVALUE() != LOAD("fee")`)},
				"written only at install"),
			initGuard("chat.bas", 15, guardText, 26, `STORE("initd", 1)`, "InitializePrivate"),
		},
	},
	{
		Kind:  "founders",
		About: "no owner: the treasury receives mint proceeds; everything else belongs to pass holders",
		Roles: []roleSpec{
			{
				Role: "creator", Keys: []string{"creator"}, Policy: pInert, Writers: []string{"InitializePrivate"},
				Cites: []cite{c("founders.bas", 29, `STORE("creator", s)`)},
				Eval: func(v vars) (string, bool, string) {
					return v.keyVal("creator"), false, "the installer, recorded at install; no function reads it (TestRoleReaders)"
				},
			},
			payeeRole("treasury", "treasury", []string{"InitializePrivate"},
				[]cite{c("founders.bas", 30, `STORE("treasury", t)`), c("founders.bas", 71, `SEND_DERO_TO_ADDRESS(LOAD("treasury"), cost)`)},
				"receives mint proceeds (Mint, line 33); cannot act; fixed for ever"),
			fixedRole("terms", []string{"share", "mfee", "price", "maxsupply", "start", "reserve"}, []string{"InitializePrivate"},
				[]cite{c("founders.bas", 35, `STORE("share", share)`), c("founders.bas", 36, `STORE("mfee", mfee)`), c("founders.bas", 31, `STORE("price", price)`), c("founders.bas", 32, `STORE("maxsupply", maxsupply)`)},
				"written only at install"),
			initGuard("founders.bas", 13, guardText, 50, `STORE("initd", 1)`, "InitializePrivate"),
		},
	},
	{
		Kind:  "vault",
		About: "an owner until Seal, then a k-of-n signer federation holding custody by design",
		Roles: []roleSpec{
			{
				Role: "owner", Keys: []string{"owner", "sealed"}, Policy: pRevoke,
				Writers: []string{"InitializePrivate", "Seal"},
				Cites: []cite{
					c("vault.bas", 75, `STORE("owner", s)`), c("vault.bas", 73, `STORE("sealed", 0)`),
					c("vault.bas", 104, `s != LOAD("owner") || LOAD("sealed") != 0`),
					c("vault.bas", 157, `IF s != LOAD("owner") || LOAD("sealed") != 0 THEN GOTO 900`),
					c("vault.bas", 246, `IF s != LOAD("owner") || LOAD("sealed") != 0 THEN GOTO 900`),
					c("vault.bas", 257, `STORE("sealed", 1)`),
				},
				Eval: func(v vars) (string, bool, string) {
					if n, _ := v.u("sealed"); n != 0 {
						return v.keyVal("owner"), false, "sealed = 1: SetLimits, AddSigner and Seal refuse the owner; the key stays stored and has no power"
					}
					return v.keyVal("owner"), true, "not sealed: the owner can AddSigner (any address), SetLimits and Seal; the ceremony ends it with Seal"
				},
			},
			{
				Role: "signers", Keys: []string{"sg_*", "isg_*", "k", "n", "na"}, Policy: pDesign, Warn: true,
				Writers: []string{"InitializePrivate", "AddSigner", "AddSigner2", "Seal", "Slash"},
				Cites: []cite{
					c("vault.bas", 47, `IF EXISTS("isg_" + s) == 0 THEN GOTO 900`), c("vault.bas", 163, `STORE("sg_" + n, r)`),
					c("vault.bas", 189, `STORE("sg_" + n, t)`), c("vault.bas", 556, `STORE("isg_" + t, 0)`), c("vault.bas", 30, `IF c < LOAD("k") THEN GOTO 100`),
				},
				Eval: vaultSigners,
			},
			{
				Role: "treasury", Keys: []string{"treasury"}, Policy: pPayee, Writers: []string{"InitializePrivate"},
				Cites: []cite{c("vault.bas", 76, `STORE("treasury", t)`), c("vault.bas", 491, `s != LOAD("treasury")`), c("vault.bas", 281, `SEND_DERO_TO_ADDRESS(LOAD("treasury"), LOAD("regfee"))`), c("vault.bas", 565, `SEND_DERO_TO_ADDRESS(LOAD("treasury"), b)`)},
				Eval: func(v vars) (string, bool, string) {
					return v.keyVal("treasury"), true, "can call ClaimFees, which takes feepool (" + v.num("feepool") + ") and nothing else; receives regfee and slashed bonds; fixed for ever"
				},
			},
			{
				Role: "cap", Keys: []string{"cap"}, Policy: pFixed, Writers: []string{"InitializePrivate", "SetCap", "Slash"},
				Cites: []cite{c("vault.bas", 77, `STORE("cap", cap)`), c("vault.bas", 535, `STORE("cap", cap)`), c("vault.bas", 567, `STORE("cap", bt * LOAD("capratio"))`)},
				Eval: func(v vars) (string, bool, string) {
					return "cap=" + v.num("cap") + " capratio=" + v.num("capratio") + " bondtot=" + v.num("bondtot"), false, "changed only by k signer votes (SetCap, never above capratio x bonds) or lowered by Slash"
				},
			},
			{
				Role: "limits", Keys: []string{"winmax", "wdelay"}, Policy: pFixed, Writers: []string{"InitializePrivate", "SetLimits", "Tighten"},
				Cites: []cite{c("vault.bas", 107, `STORE("winmax", winmax)`), c("vault.bas", 128, `STORE("winmax", winmax)`), c("vault.bas", 122, `IF cur != 0 && winmax > cur THEN GOTO 900`)},
				Eval: func(v vars) (string, bool, string) {
					return "winmax=" + v.num("winmax") + " wdelay=" + v.num("wdelay"), false, "the owner's SetLimits before Seal; after Seal only k votes to Tighten, in the safe direction"
				},
			},
			{
				Role: "halt", Keys: []string{"halted"}, Policy: pFixed, Writers: []string{"InitializePrivate", "Halt"},
				Cites: []cite{c("vault.bas", 515, `STORE("halted", BLOCK_HEIGHT() + 20000)`), c("vault.bas", 41, `IF LOAD("halted") > BLOCK_HEIGHT() THEN GOTO 20`)},
				Eval: func(v vars) (string, bool, string) {
					return "halted=" + v.num("halted"), false, "a lease k signers set (Halt) that lapses by itself at that height; Burn and Released work while halted"
				},
			},
			{
				Role: "viewkey", Keys: []string{"viewkey"}, Policy: pFixed, Writers: []string{"InitializePrivate", "SetViewKey"},
				Cites: []cite{c("vault.bas", 146, `STORE("viewkey", key)`)},
				Eval: func(v vars) (string, bool, string) {
					s, _ := v.str("viewkey")
					if len(s) > 24 {
						s = s[:24] + "…"
					}
					if s == "" {
						s = "(empty)"
					}
					return s, false, "published by k signer votes (SetViewKey)"
				},
			},
			fixedRole("terms", []string{"minbond", "minburn", "wfee", "mindep", "regfee", "bigbps", "bigdelay", "capratio"}, []string{"InitializePrivate"},
				[]cite{c("vault.bas", 83, `STORE("minbond", minbond)`), c("vault.bas", 85, `STORE("wfee", wfee)`), c("vault.bas", 87, `STORE("regfee", regfee)`)},
				"written only at install"),
			initGuard("vault.bas", 54, guardText, 94, `STORE("initd", 1)`, "InitializePrivate"),
		},
	},
	{
		Kind:  "htlc",
		About: "no owner, admin or fee: a lock moves only by its maker's refund, its taker's claim or the clock (Punish)",
		Roles: []roleSpec{initGuard("htlc.bas", 17, guardText, 21, `STORE("initd", 1)`, "Initialize")},
	},
	{
		Kind:  "swap",
		About: "no owner, admin or fee: a lock moves only by the secret before expiry or its maker's refund after",
		Roles: []roleSpec{
			fixedRole("wmin", []string{"wmin"}, []string{"Initialize"},
				[]cite{c("swap.bas", 22, `STORE("wmin", wmin)`), c("swap.bas", 18, `IF wmin == 0 || wmin > 1000000 THEN GOTO 900`), c("swap.bas", 45, `expiry < h + LOAD("wmin")`)},
				"the minimum lock width, written only at install: the same code with another wmin has the same code hash, so a companion must read it"),
			initGuard("swap.bas", 17, guardText, 23, `STORE("initd", 1)`, "Initialize"),
		},
	},
	{
		Kind:  "board",
		About: "no owner and no value held: an offer is changed only by its maker or its taker",
		Roles: []roleSpec{
			fixedRole("aw", []string{"aw"}, []string{"Initialize"}, []cite{c("board.bas", 27, `STORE("aw", 200)`), c("board.bas", 88, `STORE("ad_" + id, BLOCK_HEIGHT() + LOAD("aw"))`)}, "written only at install"),
			initGuard("board.bas", 23, guardText, 28, `STORE("initd", 1)`, "Initialize"),
		},
	},
	{
		Kind:  "pool",
		About: "no owner: the treasury is a payee and ClaimFees pays it whoever calls",
		Roles: []roleSpec{
			payeeRole("treasury", "treasury", []string{"InitializePrivate"},
				[]cite{c("pool.bas", 27, `STORE("treasury", t)`), c("pool.bas", 250, `LET t = LOAD("treasury")`)},
				"receives protocol fees through ClaimFees, which anyone may call; cannot act; fixed for ever"),
			fixedRole("fees", []string{"fee", "pfee"}, []string{"InitializePrivate"}, []cite{c("pool.bas", 28, `STORE("fee", fee)`), c("pool.bas", 29, `STORE("pfee", pfee)`)}, "written only at install"),
			initGuard("pool.bas", 16, guardText, 41, `STORE("initd", 1)`, "InitializePrivate"),
		},
	},
	{
		Kind:  "launch",
		About: "the token's creator may claim the tax in tmode 1; the treasury is a payee",
		Roles: []roleSpec{
			{
				Role: "creator", Keys: []string{"creator"}, Policy: pDesign, Writers: []string{"InitializePrivate"},
				Cites: []cite{c("launch.bas", 40, `STORE("creator", s)`), c("launch.bas", 270, `IF LOAD("tmode") != 1 || s != LOAD("creator")`)},
				Eval: func(v vars) (string, bool, string) {
					if m, _ := v.u("tmode"); m == 1 {
						return v.keyVal("creator"), true, "tmode 1: can call ClaimTax, which takes the accrued tax (" + v.num("tacc") + ")"
					}
					return v.keyVal("creator"), false, "tmode " + v.num("tmode") + ": ClaimTax refuses everyone; the creator has no power"
				},
			},
			payeeRole("treasury", "treasury", []string{"InitializePrivate"},
				[]cite{c("launch.bas", 41, `STORE("treasury", t)`), c("launch.bas", 251, `LET t = LOAD("treasury")`)},
				"receives protocol fees through ClaimFees, which anyone may call; cannot act; fixed for ever"),
			fixedRole("fees", []string{"fee", "pfee", "tax", "tmode"}, []string{"InitializePrivate"},
				[]cite{c("launch.bas", 42, `STORE("fee", fee)`), c("launch.bas", 43, `STORE("pfee", pfee)`), c("launch.bas", 44, `STORE("tax", tax)`), c("launch.bas", 45, `STORE("tmode", tmode)`)}, "written only at install"),
			initGuard("launch.bas", 7, guardText, 68, `STORE("initd", 1)`, "InitializePrivate"),
		},
	},
	{
		Kind:  "nft",
		About: "the collection's creator claims its mint proceeds; the treasury is a payee",
		Roles: []roleSpec{
			{
				Role: "creator", Keys: []string{"creator"}, Policy: pDesign, Writers: []string{"InitializePrivate"},
				Cites: []cite{c("nft.bas", 30, `STORE("creator", s)`), c("nft.bas", 108, `IF s != LOAD("creator")`)},
				Eval: func(v vars) (string, bool, string) {
					return v.keyVal("creator"), true, "can call ClaimCreator, which takes the creator's share of mint proceeds (" + v.num("cacc") + ")"
				},
			},
			payeeRole("treasury", "treasury", []string{"InitializePrivate"},
				[]cite{c("nft.bas", 31, `STORE("treasury", t)`), c("nft.bas", 121, `LET t = LOAD("treasury")`)},
				"receives the fee share through ClaimFees, which anyone may call; cannot act; fixed for ever"),
			fixedRole("terms", []string{"price", "fee"}, []string{"InitializePrivate"}, []cite{c("nft.bas", 36, `STORE("price", price)`), c("nft.bas", 37, `STORE("fee", fee)`)}, "written only at install"),
			initGuard("nft.bas", 12, guardText, 41, `STORE("initd", 1)`, "InitializePrivate"),
		},
	},
	{
		Kind:  "nfa",
		About: "an ART-NFA-MS1 asset: its creator may rewrite links (Update); its holder lists it; the treasury is a payee",
		Roles: []roleSpec{
			{
				Role: "creatorAddr", Keys: []string{"creatorAddr"}, Policy: pDesign, Writers: []string{"init"},
				Cites: []cite{c("nfa.bas", 94, `STORE("creatorAddr", SIGNER())`), c("nfa.bas", 145, `IF LOAD("creatorAddr") == SIGNER() THEN GOTO 40 ELSE GOTO 20`)},
				Eval: func(v vars) (string, bool, string) {
					return v.keyVal("creatorAddr"), true, "can call Update for ever: rewrites iconURLHdr, coverURL, fileURL, fileSignURL and tagsHdr; receives the royalty"
				},
			},
			{
				Role: "owner", Keys: []string{"owner"}, Policy: pDesign, Writers: []string{"init", "transferOwnership"},
				Cites: []cite{c("nfa.bas", 93, `STORE("owner", SIGNER())`), c("nfa.bas", 310, `STORE("owner", newOwner)`), c("nfa.bas", 147, `IF LOAD("owner") == SIGNER() THEN GOTO 40 ELSE GOTO 999`), c("nfa.bas", 177, `IF LOAD("owner") == SIGNER() THEN GOTO 110 ELSE GOTO 400`)},
				Eval: func(v vars) (string, bool, string) {
					s := "the unit's holder: Start, CloseListing, CancelListing"
					if n, _ := v.u("ownerCanUpdate"); n == 1 {
						s += ", and Update (ownerCanUpdate = 1)"
					}
					return v.keyVal("owner"), true, s
				},
			},
			fixedRole("ownerCanUpdate", []string{"ownerCanUpdate"}, []string{"InitializePrivate"}, []cite{c("nfa.bas", 72, `STORE("ownerCanUpdate", ownerCanUpdate)`), c("nfa.bas", 146, `IF LOAD("ownerCanUpdate") == 1 THEN GOTO 30 ELSE GOTO 999`)}, "written only at install"),
			payeeRole("treasury", "treasury", []string{"init"},
				[]cite{c("nfa.bas", 120, `STORE("treasury", ADDRESS_RAW(treasury))`), c("nfa.bas", 399, `owe(LOAD("treasury"), protoPaymt)`)},
				"credited protoFee on each sale, paid by PayOwed which anyone may call; cannot act; fixed for ever"),
			fixedRole("fees", []string{"protoFee", "royalty", "artificerFee"}, []string{"InitializePrivate", "init"},
				[]cite{c("nfa.bas", 119, `STORE("protoFee", protoFee)`), c("nfa.bas", 71, `STORE("royalty", royalty)`), c("nfa.bas", 70, `STORE("artificerFee", artificerFee)`)}, "written only at install"),
			{
				Role: "initd", Keys: []string{"owner"}, Policy: pGuard, Writers: []string{"init", "transferOwnership"},
				Cites: []cite{c("nfa.bas", 69, `IF EXISTS("owner") == 0 THEN GOTO 300 ELSE GOTO 999`)},
				Eval: func(v vars) (string, bool, string) {
					if v.has("owner") {
						return "owner exists", false, "InitializePrivate refuses a second run (owner exists)"
					}
					return "(absent)", true, "owner is absent: InitializePrivate can run again"
				},
			},
		},
	},
}

func kindSpecOf(kind string) *kindSpec {
	for i := range roleTable {
		if roleTable[i].Kind == kind {
			return &roleTable[i]
		}
	}
	return nil
}

// vaultSigners lists the federation. sg_<i> holds each signer's raw key. Whether signer i is still active is
// isg_<raw key>, a key whose name contains raw bytes: DERO.GetSC returns it through JSON, which replaces invalid
// UTF-8, so it is looked up by applying the same replacement to the expected name. na is the exact active count.
func vaultSigners(v vars) (string, bool, string) {
	n, _ := v.u("n")
	var list []string
	for i := uint64(0); i < n; i++ {
		raw, ok := v.raw(fmt.Sprintf("sg_%d", i))
		if !ok {
			list = append(list, fmt.Sprintf("sg_%d (absent)", i))
			continue
		}
		a := showKey(raw)
		switch act, known := lossyU(v, "isg_"+string(raw)); {
		case !known:
			a += " (active: unreadable)"
		case act == 0:
			a += " (slashed)"
		}
		list = append(list, a)
	}
	sealed, _ := v.u("sealed")
	head := fmt.Sprintf("k=%s of na=%s active, n=%s ever added", v.num("k"), v.num("na"), v.num("n"))
	if len(list) > 0 {
		head += ": " + strings.Join(list, ", ")
	}
	if sealed == 0 {
		return head, false, "not sealed: signers can do nothing yet (every federation entrypoint needs sealed = 1)"
	}
	return head, true, "sealed: k signers vote Mint, Released, Veto, Attest, Halt, SetCap, Tighten, SetViewKey, AddSigner2 and Slash — custody of every wrapped unit"
}

// lossyU finds a uint64 variable whose name contains raw bytes, by the name JSON would have given it.
func lossyU(v vars, key string) (uint64, bool) {
	b, _ := json.Marshal(key)
	var mangled string
	_ = json.Unmarshal(b, &mangled)
	return v.u(mangled)
}

// statusOf maps a role's policy and liveness to a report status.
func statusOf(r roleSpec, live bool) status {
	switch r.Policy {
	case pRevoke, pGuard:
		if live {
			return sFail
		}
		return sPass
	case pDesign:
		if live && r.Warn {
			return sWarn
		}
	}
	return sInfo
}

// evalRoles reads every role of one contract. allow maps "<kind>.<role>" to the reason given on the command line.
func evalRoles(label, kind, scid string, v vars, allow map[string]string) []roleRow {
	ks := kindSpecOf(kind)
	if ks == nil {
		return nil
	}
	var rows []roleRow
	for _, r := range ks.Roles {
		val, live, state := r.Eval(v)
		row := roleRow{Contract: label, SCID: scid, Kind: kind, Role: r.Role, ID: kind + "." + r.Role, Keys: r.Keys, Policy: r.Policy,
			Value: val, Live: live, State: state, Status: statusOf(r, live), Cites: compactCites(r.Cites)}
		if row.Status == sFail {
			if why, ok := allow[row.ID]; ok {
				row.Status, row.Reason = sAllowed, why
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// printRoleTable writes the table this tool applies, with the functions that write each role's keys and the lines
// it cites, so a reader can check the table against the source without running anything else (-roles).
func printRoleTable(w io.Writer) {
	for _, k := range roleTable {
		fmt.Fprintf(w, "%s: %s\n", k.Kind, k.About)
		for _, r := range k.Roles {
			fmt.Fprintf(w, "  %-15s %-10s keys %s; written by %s\n", r.Role, r.Policy, strings.Join(r.Keys, ", "), strings.Join(r.Writers, ", "))
			fmt.Fprintf(w, "  %-15s cites %s\n", "", strings.Join(compactCites(r.Cites), "; "))
		}
	}
	fmt.Fprintf(w, "tela: a TELA-INDEX-1 (the app's entrypoint); a TELA-DOC-1 has no role that can act\n")
	fmt.Fprintf(w, "  %-15s %-10s keys owner; written by init (once, at install)\n", "owner", pRevoke)
	fmt.Fprintf(w, "  %-15s cites %s\n", "", strings.Join(compactCites(telaOwnerCites), "; "))
}

// roleIDs is every name -allow accepts.
func roleIDs() map[string]bool {
	ids := map[string]bool{"tela.owner": true}
	for _, k := range roleTable {
		for _, r := range k.Roles {
			if r.Policy == pRevoke || r.Policy == pGuard {
				ids[k.Kind+"."+r.Role] = true
			}
		}
	}
	return ids
}
