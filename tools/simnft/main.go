// simnft: the NFT part of the launch scenario in isolation, verbose per call.
//
//	go run ./simnft -launch <scid> -factory <scid> [-creator 1] [-holder 13] [-treasury 2]
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	rpc "veil/tools/simrpc"
)

func main() {
	daemon := flag.String("daemon", "127.0.0.1:20000", "daemon")
	contracts := flag.String("contracts", "../contracts", "contracts dir")
	launch := flag.String("launch", "", "launch scid (the token to hold)")
	factory := flag.String("factory", "", "factory scid")
	creator := flag.Int("creator", 1, "creator wallet index")
	holder := flag.Int("holder", 13, "holder wallet index (must hold the token)")
	treasuryIdx := flag.Int("treasury", 2, "treasury wallet index")
	flag.Parse()
	log.SetFlags(0)
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	w := func(i int) string { return fmt.Sprintf("127.0.0.1:%d", 30000+i) }
	cr, hd, tr := rpc.Address(w(*creator)), rpc.Address(w(*holder)), rpc.Address(w(*treasuryIdx))
	code, err := os.ReadFile(*contracts + "/nft.bas")
	rpc.Must(err)
	args := []rpc.Arg{rpc.S("name", "VSC Holder Badge"), rpc.S("descr", "earned by holding VSC"), rpc.S("icon", ""), rpc.S("file", ""), rpc.S("tags", "#vsc"),
		rpc.S("collection", "VSC Badges"), rpc.H("token", *launch), rpc.U("minhold", 1000), rpc.U("holdblocks", 2), rpc.U("maxsupply", 10),
		rpc.U("price", 100000), rpc.S("treasury", tr), rpc.U("fee", 50)}
	fmt.Println("holder token balance:", rpc.Balance(w(*holder), *launch), "DERO:", rpc.Balance(w(*holder), ""))
	fmt.Println("1. estimate nft install")
	g, err := c.Estimate(cr, string(code), "", nil, args)
	fmt.Println("   ->", g, err)
	if err != nil {
		os.Exit(1)
	}
	fmt.Println("2. install")
	nft, gas, _ := c.Send(w(*creator), cr, string(code), "", nil, args)
	fmt.Println("   nft", nft, gas)
	fmt.Println("3. register on factory")
	if _, err := c.Estimate(cr, "", *factory, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 10000}}, []rpc.Arg{rpc.S("entrypoint", "RegisterNFT"), rpc.H("scid", nft)}); err != nil {
		fmt.Println("   register estimate:", err)
		os.Exit(1)
	}
	c.Send(w(*creator), cr, "", *factory, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 10000}}, []rpc.Arg{rpc.S("entrypoint", "RegisterNFT"), rpc.H("scid", nft)})
	fmt.Println("4. lock 5000 units")
	if _, err := c.Estimate(hd, "", nft, []rpc.Transfer{{SCID: *launch, Burn: 5000}}, []rpc.Arg{rpc.S("entrypoint", "Lock")}); err != nil {
		fmt.Println("   lock estimate:", err)
		os.Exit(1)
	}
	c.Send(w(*holder), hd, "", nft, []rpc.Transfer{{SCID: *launch, Burn: 5000}}, []rpc.Arg{rpc.S("entrypoint", "Lock")})
	fmt.Println("   locked; waiting 2 blocks")
	c.WaitBlocks(2)
	fmt.Println("5. mint for 1 DERO (fixed fee: the estimator runs at height 0 and would reject the hold check)")
	if _, err := c.Estimate(hd, "", nft, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 100000}}, []rpc.Arg{rpc.S("entrypoint", "Mint")}); err != nil {
		fmt.Println("   mint estimate (expected to fail):", err)
	}
	c.SendWithFee(w(*holder), hd, nft, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 100000}}, []rpc.Arg{rpc.S("entrypoint", "Mint")}, 2000)
	st := c.GetSC(nft, false)
	fmt.Println("   minted:", st.U("minted"), "fd:", st.U("fd"), "cacc:", st.U("cacc"), "ed_0:", st.Str("ed_0"), "holder nft balance:", rpc.Balance(w(*holder), nft))
	fmt.Println("6. unlock, claim creator, claim fees")
	c.Send(w(*holder), hd, "", nft, nil, []rpc.Arg{rpc.S("entrypoint", "Unlock")})
	c.Send(w(*creator), cr, "", nft, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimCreator")})
	c.Send(w(*treasuryIdx), tr, "", nft, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimFees")})
	st = c.GetSC(nft, false)
	fmt.Println("   fd:", st.U("fd"), "cacc:", st.U("cacc"), "contract DERO:", st.Balance, "balances:", st.Balances)
	fmt.Println("SIMULATOR NFT OK")
}
