// simnfa: one ART-NFA-MS1 asset (contracts/nfa.bas) installed on a running simulator, registered on the factory
// and, with -start, listed for sale so the NFA screen has a listed one to show. Verbose per call, like simnft.
//
//	go run ./simnfa -factory <scid> [-quote <scid>] [-creator 10] [-buyer 13] [-treasury 2] [-fee-bps 50] [-start] [-buy]
//	go run ./simnfa -factory <scid> -nfa <scid> [-register=false] -start   # an NFA already installed
//
// The code installed is ref.Canon(contracts/nfa.bas), exactly what simdeploy ships to app/templates and hashes
// into config.js templates.nfa: the app lists an NFA only when its on-chain code hashes to that, its treasury is
// the factory's and its protoFee is the deployment's feeBps (app/feed.js loadNFA), so all three are checked or
// taken from the chain here rather than trusted to flags. Wallet indexes map to 127.0.0.1:3NNNN.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	ref "veil/reference"
	rpc "veil/tools/simrpc"
)

// Storage gas an install must stay under (docs/NFA_BUILD.md §5.A; tests/nfa_test.go asserts the same bound).
const gasCap = 20000

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// quoteBalance is a wallet's balance of the quote as text: "" is DERO, and a wallet that has never scanned the
// asset may refuse the read, which is a fact worth printing rather than a reason to stop.
func quoteBalance(wallet, scid string) string {
	var r struct {
		Balance uint64 `json:"balance"`
	}
	params := map[string]interface{}{}
	if scid != "" {
		params["scid"] = scid
	}
	if err := rpc.Call(wallet, "GetBalance", params, &r); err != nil {
		return "unreadable (" + err.Error() + ")"
	}
	return fmt.Sprint(r.Balance)
}

func main() {
	daemon := flag.String("daemon", "127.0.0.1:20000", "daemon")
	contracts := flag.String("contracts", "../contracts", "contracts dir")
	factory := flag.String("factory", "", "factory scid")
	quote := flag.String("quote", "", "quote scid (64 hex; empty prices the NFA in DERO)")
	nfaFlag := flag.String("nfa", "", "an NFA already installed from this template: skip the install, register and/or start it")
	register := flag.Bool("register", true, "register on the factory; false only for a factory deployed before RegisterNFA existed")
	creator := flag.Int("creator", 10, "creator wallet index: installs, registers and lists")
	buyer := flag.Int("buyer", 13, "buyer wallet index: reported, and buys with -buy")
	treasuryIdx := flag.Int("treasury", 2, "treasury wallet index (must be the factory's treasury)")
	feeBps := flag.Uint64("fee-bps", 50, "protoFee in bps: must equal the deployment's feeBps (deploy/networks.json), or the app refuses the NFA")
	start := flag.Bool("start", false, "after registering, Start a sale at -price for -hours")
	price := flag.Uint64("price", 100000, "sale price in atomic units of the quote (with -start)")
	hours := flag.Uint64("hours", 24, "sale duration in hours (with -start)")
	buy := flag.Bool("buy", false, "after -start, the buyer takes the sale at -price (the listing is then gone)")
	name := flag.String("name", "DMZ simulator NFA", "nameHdr")
	collection := flag.String("collection", "DMZ simulator", "collection (install-only)")
	royalty := flag.Uint64("royalty", 5, "royalty percent to the creator (≤ 100)")
	flag.Parse()
	log.SetFlags(0)
	if !hex64.MatchString(*factory) {
		log.Fatal("-factory must be a 64-hex scid")
	}
	if *quote != "" && !hex64.MatchString(*quote) {
		log.Fatal("-quote must be empty (DERO) or a 64-hex scid")
	}
	if *nfaFlag != "" && !hex64.MatchString(*nfaFlag) {
		log.Fatal("-nfa must be a 64-hex scid")
	}
	if *royalty*100+*feeBps > 10000 {
		log.Fatalf("royalty %d%% and protoFee %d bps do not fit in a sale (init line 500)", *royalty, *feeBps)
	}
	// every entrypoint used here reads SIGNER() (init stores the creator, Start and BuyItNow check the caller), so
	// the client's default ring of 2 is the only one that works; simrpc never sends above it unless asked
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	w := func(i int) string { return fmt.Sprintf("127.0.0.1:%d", 30000+i) }
	cr, by, tr := rpc.Address(w(*creator)), rpc.Address(w(*buyer)), rpc.Address(w(*treasuryIdx))
	qname := "DERO"
	if *quote != "" {
		qname = "quote " + (*quote)[:8]
	}

	fmt.Println("0. factory")
	fs := c.GetSC(*factory, true)
	regfee, nc, tstr := fs.U("regfee"), fs.U("nc"), fs.Str("tstr")
	fmt.Println("   regfee", regfee, "nc", nc, "treasury", tstr)
	if rpc.DVMAddress(tstr) == "" || rpc.DVMAddress(tstr) != rpc.DVMAddress(tr) {
		log.Fatalf("wallet %d is %s, not the factory's treasury %s: the app lists only NFAs whose treasury is the factory's", *treasuryIdx, tr, tstr)
	}
	// checked before any gas is spent: a factory deployed before contracts/factory.bas gained RegisterNFA cannot
	// list an NFA at all, and an install that can never be registered is not what this tool is for
	if *register && !strings.Contains(fs.Code, "Function RegisterNFA(") {
		log.Fatalf("the factory at %s has no RegisterNFA entrypoint (deployed before factory.bas gained it): redeploy it through simdeploy, or pass -register=false to install and list without a registry entry", *factory)
	}
	codeRaw, err := os.ReadFile(*contracts + "/nfa.bas")
	rpc.Must(err)
	code := ref.Canon(string(codeRaw))
	sum := sha256.Sum256([]byte(code))
	fmt.Println("   nfa.bas canon", len(code), "bytes, sha256", hex.EncodeToString(sum[:]), "(must be config.js templates.nfa)")

	// InitializePrivate's argument list in the standard's order, then the three DMZ additions (tests/nfa_test.go
	// nfaArgs). fileCheckC/S are the C: and S: lines of a DERO filesign; the simulator has no signed file, so both
	// are empty and the file URLs say so.
	args := []rpc.Arg{rpc.U("artificerFee", 0), rpc.U("royalty", *royalty), rpc.U("ownerCanUpdate", 0),
		rpc.S("nameHdr", *name), rpc.S("descrHdr", "One unit, one owner; priced in "+qname+" on the DMZ simulator"), rpc.S("typeHdr", "Image"),
		rpc.S("iconURLHdr", ""), rpc.S("tagsHdr", "#dmz #simulator"), rpc.S("fileCheckC", ""), rpc.S("fileCheckS", ""),
		rpc.S("fileURL", ""), rpc.S("fileSignURL", ""), rpc.S("coverURL", ""), rpc.S("collection", *collection),
		rpc.S("quote", *quote), rpc.U("protoFee", *feeBps), rpc.S("treasury", tr)}

	fmt.Println("1. balances")
	crDero, byDero := rpc.Balance(w(*creator), ""), rpc.Balance(w(*buyer), "")
	fmt.Println("   creator", cr, "DERO", crDero)
	fmt.Println("   buyer  ", by, "DERO", byDero, qname, quoteBalance(w(*buyer), *quote))

	nfa, gas := *nfaFlag, rpc.Gas{}
	if nfa != "" {
		fmt.Println("2. reuse", nfa)
		if st := c.GetSC(nfa, true); st.Code != code {
			log.Fatalf("the contract at %s is not this template: its code does not equal canon nfa.bas", nfa)
		}
	} else {
		fmt.Println("2. estimate and install")
		g, err := c.Estimate(cr, code, "", nil, args)
		fmt.Println("   -> storage gas", g.GasStorage, "compute", g.GasCompute, err)
		if err != nil {
			os.Exit(1)
		}
		if g.GasStorage >= gasCap {
			log.Fatalf("install storage gas %d is not under the %d cap (docs/NFA_BUILD.md §5.A)", g.GasStorage, gasCap)
		}
		// install fee, the registration burn and its fee, and a Start: refused up front rather than half way through
		if need := g.GasStorage + 200 + regfee + 3000; crDero < need {
			log.Fatalf("creator wallet %d holds %d atomic DERO, needs at least %d for install + register + start", *creator, crDero, need)
		}
		nfa, gas, _ = c.Send(w(*creator), cr, code, "", nil, args)
		fmt.Println("   nfa", nfa, "storage gas", gas.GasStorage)
	}
	st := c.GetSC(nfa, false)
	fmt.Println("   quote", st.StringKeys["quote"], "protoFee", st.U("protoFee"), "royalty", st.U("royalty"), "version", st.Str("version"), "listType", st.Str("listType"))
	// the unit must be in the creator's wallet to have been minted there, and to be deposited by Start; a reused
	// NFA that is already listed holds it in the contract, which is no reason to refuse its registration
	if unit := rpc.Balance(w(*creator), nfa); unit == 1 {
		fmt.Println("   creator holds the one unit")
	} else if *nfaFlag == "" || *start {
		log.Fatalf("creator holds %d units of the NFA, want exactly 1", unit)
	} else {
		fmt.Println("   creator does not hold the unit: scBalance", st.U("scBalance"), "(listed, or held elsewhere)")
	}

	regTx := ""
	if *register {
		fmt.Println("3. register on factory")
		reg := []rpc.Arg{rpc.S("entrypoint", "RegisterNFA"), rpc.H("scid", nfa)}
		burn := []rpc.Transfer{{Destination: c.RandomAddress(), Burn: regfee}}
		if _, err := c.Estimate(cr, "", *factory, burn, reg); err != nil {
			fmt.Println("   register estimate:", err)
			os.Exit(1)
		}
		regTx, _, _ = c.Send(w(*creator), cr, "", *factory, burn, reg)
		fs = c.GetSC(*factory, false)
		if fs.U("nc") != nc+1 || fs.Str(fmt.Sprintf("nfa_%d", nc)) != nfa {
			log.Fatalf("registration did not land: nc %d, nfa_%d = %q", fs.U("nc"), nc, fs.Str(fmt.Sprintf("nfa_%d", nc)))
		}
		fmt.Println("   registered as nfa_"+fmt.Sprint(nc), "tx", regTx)
	} else {
		fmt.Println("3. not registered (-register=false): the app's registry will not list it")
	}

	startTx, buyTx := "", ""
	if *start {
		fmt.Println("4. start a sale:", *price, "atomic units of", qname, "for", *hours, "hours")
		sargs := []rpc.Arg{rpc.S("entrypoint", "Start"), rpc.S("listType", "sale"), rpc.U("duration", *hours), rpc.U("startPrice", *price), rpc.S("charityDonateAddr", ""), rpc.U("charityDonatePerc", 0)}
		unit := []rpc.Transfer{{SCID: nfa, Burn: 1}}
		if _, err := c.Estimate(cr, "", nfa, unit, sargs); err != nil {
			fmt.Println("   start estimate:", err)
			os.Exit(1)
		}
		startTx, _, _ = c.Send(w(*creator), cr, "", nfa, unit, sargs)
		st = c.GetSC(nfa, false)
		fmt.Println("   listType", st.Str("listType"), "startPrice", st.U("startPrice"), "scBalance", st.U("scBalance"), "ends", st.U("endBlockTime"), "tx", startTx)
		if st.Str("listType") != "sale" || st.U("scBalance") != 1 {
			log.Fatal("the sale did not start")
		}
	}
	if *buy && *start {
		fmt.Println("5. buyer takes the sale")
		var pay []rpc.Transfer
		if *quote == "" {
			pay = []rpc.Transfer{{Destination: c.RandomAddress(), Burn: *price}}
		} else {
			pay = []rpc.Transfer{{SCID: *quote, Burn: *price}}
		}
		bargs := []rpc.Arg{rpc.S("entrypoint", "BuyItNow")}
		if _, err := c.Estimate(by, "", nfa, pay, bargs); err != nil {
			fmt.Println("   buy estimate:", err)
			os.Exit(1)
		}
		buyTx, _, _ = c.Send(w(*buyer), by, "", nfa, pay, bargs)
		st = c.GetSC(nfa, false)
		// the unit is the truth (docs/NFA.md §1.1): the buyer holds it after a settled sale
		fmt.Println("   buyer holds the unit:", rpc.Balance(w(*buyer), nfa) == 1, "previousSalePrice", st.U("previousSalePrice"), "listType", st.Str("listType"), "tx", buyTx)
	}
	fmt.Println("SIMULATOR NFA OK")
	fmt.Printf("scid %s\nstorage_gas %d\nregister_tx %s\nstart_tx %s\nbuy_tx %s\n", nfa, gas.GasStorage, regTx, startTx, buyTx)
}
