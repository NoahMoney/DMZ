// simscam installs markets that impersonate something a user trusts, so the app's defences can be tested
// against real chain state instead of assumed. It exists because the impersonation guard had only ever been
// reasoned about: nothing had put a market called WXMR in front of the app to see what it did.
//
//	go run ./simscam -ticker WXMR -name "Wrapped Monero"        # a token whose ticker claims to be the wrapped asset
//	go run ./simscam -ticker XMR  -name "Monero"
//
// It installs and registers a perfectly ordinary launch. The point is not that the contract is malicious — it
// is that it is not, and the only thing separating it from the real wrapped asset is what the app chooses to
// say about the name.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	ref "veil/reference"
	rpc "veil/tools/simrpc"
)

var (
	daemon    = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	factory   = flag.String("factory", "", "factory scid (default: read from ../app/config.js)")
	contracts = flag.String("contracts", "../app/templates", "where to read launch.bas from — the app verifies against THIS copy")
	creatorW  = flag.String("creator", "127.0.0.1:30006", "wallet that creates the market")
	treasuryW = flag.String("treasury", "127.0.0.1:30002", "treasury wallet, must match the factory's treasury")
	ticker    = flag.String("ticker", "WXMR", "the ticker to impersonate")
	name      = flag.String("name", "Wrapped Monero", "the display name to impersonate")
	descr     = flag.String("descr", "", "description")
	quote     = flag.String("quote", "0000000000000000000000000000000000000000000000000000000000000000", "quote asset scid")
)

func main() {
	flag.Parse()
	c := &rpc.Client{Daemon: *daemon, Timeout: 180e9}
	if *factory == "" {
		b, err := os.ReadFile("../app/config.js")
		rpc.Must(err)
		*factory = between(string(b), `factory: "`, `"`)
	}
	if len(*factory) != 64 {
		log.Fatal("-factory required (or ../app/config.js must carry one)")
	}
	code, err := os.ReadFile(filepath.Join(*contracts, "launch.bas"))
	rpc.Must(err)
	src := ref.Canon(string(code))
	creator, treasury := rpc.Address(*creatorW), rpc.Address(*treasuryW)
	p := struct{ supply, grad, cbps, tax, tmode, fee, pfee uint64 }{1000000000, 1000000, 300, 0, 0, 50, 5000}
	scid, gas, h := c.Send(*creatorW, creator, src, "", nil, []rpc.Arg{
		rpc.S("name", *name), rpc.S("ticker", *ticker), rpc.S("descr", *descr), rpc.S("icon", ""), rpc.S("tags", ""),
		rpc.H("quote", *quote), rpc.U("dec", 5), rpc.U("supply", p.supply), rpc.U("grad", p.grad), rpc.U("cbps", p.cbps),
		rpc.U("tax", p.tax), rpc.U("tmode", p.tmode), rpc.S("treasury", treasury), rpc.U("fee", p.fee), rpc.U("pfee", p.pfee)})
	if len(scid) != 64 {
		log.Fatalf("install failed (gas %d/%d)", gas.GasCompute, gas.GasStorage)
	}
	fmt.Printf("installed %q (%s) as %s at height %d, storage gas %d\n", *name, *ticker, scid, h, gas.GasStorage)
	fee := c.GetSC(*factory, false).U("regfee")
	var xf []rpc.Transfer
	if fee > 0 {
		xf = []rpc.Transfer{{Destination: c.RandomAddress(), Burn: fee}}
	}
	c.Send(*creatorW, creator, "", *factory, xf, []rpc.Arg{rpc.S("entrypoint", "RegisterLaunch"), rpc.H("scid", scid)}, *treasuryW)
	st := c.GetSC(*factory, false)
	fmt.Printf("registered on the factory: %d launches listed\n", st.U("lc"))
	fmt.Printf("SCAM=%s\n", scid)
}

func between(s, a, b string) string {
	i := indexOf(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	j := indexOf(s, b)
	if j < 0 {
		return ""
	}
	return s[:j]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
