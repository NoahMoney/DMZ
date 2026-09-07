// simwrapmkt creates a market for a vault's wrapped asset on the simulator: install pool.bas against the vault's
// own SCID, register it on the factory so the app lists it, seed liquidity, then trade it so it has a chart.
//
// This is the market a wrapped asset needs to be tradable at all today. Note what it is and is not: pool.bas
// quotes everything in DERO, so this creates WRAPPED/DERO. A TOKEN/XMR market — XMR as the quote — is not
// possible until the quote asset becomes a contract parameter.
//
//	go run ./simwrapmkt -vault <scid> [-dero 200000 -units 20000 -trades 12]
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	rpc "veil/tools/simrpc"
)

var (
	daemon    = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	vault     = flag.String("vault", "", "vault.bas scid; its own SCID is the wrapped asset")
	factory   = flag.String("factory", "", "factory scid")
	contracts = flag.String("contracts", "../app/templates", "where to read pool.bas from — the app verifies against THIS copy")
	holder    = flag.String("holder", "127.0.0.1:30006", "wallet holding the wrapped units (it seeds the pool)")
	treasuryW = flag.String("treasury", "127.0.0.1:30002", "treasury wallet, must match the factory's treasury")
	dero      = flag.Uint64("dero", 200000, "DERO atomic units to seed")
	units     = flag.Uint64("units", 20000, "wrapped base units to seed")
	trades    = flag.Int("trades", 12, "buys/sells to run afterwards so the market has a chart")
	pool      = flag.String("pool", "", "reuse an existing pool scid instead of installing")
	base      = flag.String("base", "", "base asset scid; default is the vault itself (a wrapped/DERO market)")
	quoteF    = flag.String("quote", "", "quote asset scid; default DERO. Set it to a vault to make a TOKEN/WRAPPED pair.")
	acquire   = flag.String("acquire", "", "scid:deroUnits — buy the base asset from a launch first, so the holder can seed the pool")
)

func main() {
	flag.Parse()
	if len(*vault) != 64 || len(*factory) != 64 {
		log.Fatal("-vault and -factory required")
	}
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	me, tre := rpc.Address(*holder), rpc.Address(*treasuryW)

	zero := "0000000000000000000000000000000000000000000000000000000000000000"
	baseA, quoteA := *base, *quoteF
	if baseA == "" {
		baseA = *vault
	}
	if quoteA == "" {
		quoteA = zero
	}
	qname := "DERO"
	if quoteA != zero {
		qname = quoteA[:12]
	}
	fmt.Printf("market: base %s / quote %s\n", baseA[:12], qname)

	// To seed a TOKEN/WRAPPED market the holder needs the token as well as the wrapped units, so it can buy the
	// token first from whichever launch issued it.
	if *acquire != "" {
		p := strings.SplitN(*acquire, ":", 2)
		amt, _ := strconv.ParseUint(p[1], 10, 64)
		fmt.Printf("buying the base asset from launch %s with %d DERO units\n", p[0][:12], amt)
		c.Send(*holder, me, "", p[0], []rpc.Transfer{{Destination: c.RandomAddress(), Burn: amt}},
			[]rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", 0), rpc.U("publish", 0)})
	}

	held := rpc.Balance(*holder, baseA)
	fmt.Printf("holder has %d base units\n", held)
	if held < *units {
		log.Fatalf("holder holds %d, needs %d to seed: mint more first (tools/simvault -deposit)", held, *units)
	}

	if *pool == "" {
		code, err := os.ReadFile(filepath.Join(*contracts, "pool.bas"))
		rpc.Must(err)
		fmt.Println("installing pool.bas against the wrapped asset")
		scid, gas, _ := c.Send(*holder, me, string(code), "", nil, []rpc.Arg{
			rpc.H("asset", baseA), rpc.H("quote", quoteA), rpc.S("treasury", tre), rpc.U("fee", 50), rpc.U("pfee", 5000)})
		*pool = scid
		fmt.Printf("  pool %s (storage gas %d)\n", scid, gas.GasStorage)
		st := c.GetSC(scid, false)
		if got := hex.EncodeToString([]byte(st.Str("asset"))); got != baseA {
			log.Fatalf("pool asset is %s, wanted %s", got, baseA)
		}
	}

	// the factory listing is what the app reads; without it the market exists but is invisible
	regfee := c.GetSC(*factory, false).U("regfee")
	fmt.Printf("registering on the factory (fee %d)\n", regfee)
	var xf []rpc.Transfer
	if regfee > 0 {
		xf = []rpc.Transfer{{Destination: c.RandomAddress(), Burn: regfee}}
	}
	c.Send(*holder, me, "", *factory, xf, []rpc.Arg{rpc.S("entrypoint", "RegisterPool"), rpc.H("scid", *pool)})

	fmt.Printf("seeding %d DERO units and %d wrapped units\n", *dero, *units)
	q := rpc.Transfer{Destination: c.RandomAddress(), Burn: *dero}
	if quoteA != zero {
		q = rpc.Transfer{SCID: quoteA, Burn: *dero}
	}
	c.Send(*holder, me, "", *pool, []rpc.Transfer{q, {SCID: baseA, Burn: *units}},
		[]rpc.Arg{rpc.S("entrypoint", "AddLiquidity"), rpc.U("min_lp", 0)})
	st := c.GetSC(*pool, false)
	fmt.Printf("  reserves: %d DERO / %d wrapped, lp %d\n", st.U("rd"), st.U("ra"), st.U("lp"))

	// a market with no history has no chart, so give it one: buys from other wallets, sells from the holder
	rng := rand.New(rand.NewSource(7))
	buyers := []string{"127.0.0.1:30000", "127.0.0.1:30002", "127.0.0.1:30003", "127.0.0.1:30005"}
	for i := 0; i < *trades; i++ {
		if i%3 == 2 {
			back := rpc.Balance(*holder, baseA) / 40
			if back == 0 {
				continue
			}
			c.Send(*holder, me, "", *pool, []rpc.Transfer{{SCID: baseA, Burn: back}},
				[]rpc.Arg{rpc.S("entrypoint", "SwapAssetForDero"), rpc.U("min_out", 0), rpc.U("publish", 1)})
			fmt.Printf("  sell %d wrapped units\n", back)
			continue
		}
		w := buyers[rng.Intn(len(buyers))]
		amt := uint64(2000 + rng.Intn(9000))
		// a non-DERO quote must be paid by whoever holds it, so the holder does the buying too
		if quoteA != zero {
			w = *holder
			amt = rpc.Balance(*holder, quoteA) / 25
			if amt == 0 {
				continue
			}
		}
		qx := rpc.Transfer{Destination: c.RandomAddress(), Burn: amt}
		if quoteA != zero {
			qx = rpc.Transfer{SCID: quoteA, Burn: amt}
		}
		c.Send(w, rpc.Address(w), "", *pool, []rpc.Transfer{qx},
			[]rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", 0), rpc.U("publish", 1)})
		fmt.Printf("  buy with %d DERO units from %s\n", amt, w[len(w)-5:])
	}
	st = c.GetSC(*pool, false)
	fmt.Printf("done: pool %s, reserves %d DERO / %d wrapped, %d published trades\n",
		*pool, st.U("rd"), st.U("ra"), st.U("tc"))
}
