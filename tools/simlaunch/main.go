// simlaunch drives factory.bas, launch.bas (curve → graduation → AMM, staking rewards), social.bas and nft.bas
// on a running simulator over plain JSON-RPC, checking the launch state against the reference model after
// every step. Uses simulator wallets 0..8 (8 DERO each): a 10 DERO graduation needs several buyers.
//
//	go run ./simlaunch -contracts ../contracts
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	ref "veil/reference"
	rpc "veil/tools/simrpc"
)

var (
	daemon    = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	contracts = flag.String("contracts", "../contracts", "contracts directory")
	timeout   = flag.Duration("timeout", 180*time.Second, "per-tx confirmation timeout")
)

func wallet(i int) string { return fmt.Sprintf("127.0.0.1:%d", 30000+i) }

func read(name string) string {
	b, err := os.ReadFile(*contracts + "/" + name)
	rpc.Must(err)
	return ref.Canon(string(b))
}

func check(step string, st rpc.SCState, m *ref.Launch) {
	fields := map[string]uint64{"phase": m.Phase, "sold": m.Sold, "real": m.Real, "rd": m.RD, "ra": m.RA, "fd": m.FD, "fa": m.FA,
		"tacc": m.TAcc, "rpool": m.RPool, "locked": m.Locked, "stot": m.STot, "sacc": m.SAcc, "tc": m.TC, "pc": m.PC}
	for k, want := range fields {
		if got := st.U(k); got != want {
			log.Fatalf("%s: %s = %d, model %d", step, k, got, want)
		}
	}
	fmt.Printf("  %-16s phase=%d rd=%d ra=%d sold=%d real=%d fd=%d tacc=%d stot=%d tc=%d pc=%d (matches model)\n",
		step, m.Phase, m.RD, m.RA, m.Sold, m.Real, m.FD, m.TAcc, m.STot, m.TC, m.PC)
}

func main() {
	flag.Parse()
	log.SetFlags(0)
	c := &rpc.Client{Daemon: *daemon, Timeout: *timeout}
	addr := make([]string, 22)
	for i := range addr {
		addr[i] = rpc.Address(wallet(i))
	}
	treasury := addr[2]
	fmt.Printf("daemon %s height %d, treasury %s\n", *daemon, c.Height(), treasury)

	fmt.Println("1. install factory.bas (owner w0, regfee 0.1 DERO)")
	factory, gas, _ := c.Send(wallet(0), addr[0], read("factory.bas"), "", nil, []rpc.Arg{rpc.S("treasury", treasury), rpc.U("regfee", 10000)})
	fmt.Printf("  factory %s gas %d/%d tstr=%s\n", factory, gas.GasCompute, gas.GasStorage, c.GetSC(factory, false).Str("tstr"))

	// supply in atomic units: keep it wallet-friendly (balance decryption is a search, DECISIONS §5.2)
	fmt.Println("2. w1 launches VSC: supply 1e9 units, graduation 10 DERO, creator 3%, tax 5% to stakers (tmode 3)")
	p := struct{ supply, grad, cbps, tax, tmode, fee, pfee uint64 }{1000000000, 1000000, 300, 500, 3, 50, 5000}
	launch, gas, _ := c.Send(wallet(1), addr[1], read("launch.bas"), "", nil, []rpc.Arg{
		rpc.S("name", "Veil Sim Coin"), rpc.S("ticker", "VSC"), rpc.S("descr", "simulator run"), rpc.S("icon", ""), rpc.S("tags", "#sim"), rpc.H("quote", "0000000000000000000000000000000000000000000000000000000000000000"),
		rpc.U("dec", 3), rpc.U("supply", p.supply), rpc.U("grad", p.grad), rpc.U("cbps", p.cbps), rpc.U("tax", p.tax), rpc.U("tmode", p.tmode),
		rpc.S("treasury", treasury), rpc.U("fee", p.fee), rpc.U("pfee", p.pfee)})
	m, _ := ref.NewLaunch(p.supply, p.grad, p.cbps, p.tax, p.tmode, p.fee, p.pfee)
	fmt.Printf("  launch %s gas %d/%d; C=%d Lq=%d vd0=%d vt0=%d\n", launch, gas.GasCompute, gas.GasStorage, m.C, m.Lq, m.VD0, m.VT0)
	if b := rpc.Balance(wallet(1), launch); b != m.Alloc {
		log.Fatalf("creator allocation in wallet %d want %d", b, m.Alloc)
	}
	check("install", c.GetSC(launch, false), m)

	fmt.Println("3. register the launch on the factory (0.1 DERO to treasury)")
	c.Send(wallet(1), addr[1], "", factory, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 10000}}, []rpc.Arg{rpc.S("entrypoint", "RegisterLaunch"), rpc.H("scid", launch)}, wallet(2))
	if st := c.GetSC(factory, false); st.U("lc") != 1 || st.Str("launch_0") != launch {
		log.Fatal("registration not stored")
	}
	fmt.Println("  launch_0 stored")

	fmt.Println("4. buyers 12..15 buy 3 DERO each (published) until the curve graduates")
	buys := 0
	for i := 12; m.Phase == 0; i++ {
		if i > 15 {
			log.Fatal("ran out of buyers before graduation")
		}
		d := uint64(300000)
		q, _ := m.QuoteBuy(d)
		_, gas, h := c.Send(wallet(i), addr[i], "", launch, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", q), rpc.U("publish", 1)})
		sends, ok := m.Buy(ref.In{D: d}, q, 1, addr[i], uint64(h), 0)
		if !ok {
			log.Fatal("model rejected the buy")
		}
		buys++
		st := c.GetSC(launch, false)
		check(fmt.Sprintf("buy %d", buys), st, m)
		fmt.Printf("    gas %d/%d out=%d refund=%v t_%d=%q\n", gas.GasCompute, gas.GasStorage, sends[len(sends)-1].Amount, len(sends) > 1, m.TC-1, st.Str(fmt.Sprintf("t_%d", m.TC-1)))
	}
	if m.RD != m.Real || m.RA != m.Lq || m.Sold != m.C {
		log.Fatal("graduation state")
	}
	fmt.Printf("  graduated after %d buys at height %d\n", buys, m.GH)

	fmt.Println("5. AMM: w12 sells a fifth of its tokens, w13 buys 0.5 DERO")
	hold := rpc.Balance(wallet(12), launch)
	a := hold / 5
	_, _, h := c.Send(wallet(12), addr[12], "", launch, []rpc.Transfer{{SCID: launch, Burn: a}}, []rpc.Arg{rpc.S("entrypoint", "SwapAssetForDero"), rpc.U("min_out", 0), rpc.U("publish", 1)})
	m.Sell(ref.In{A: a}, 0, 1, addr[12], uint64(h), 0)
	check("sell", c.GetSC(launch, false), m)
	_, _, h = c.Send(wallet(13), addr[13], "", launch, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 50000}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", 0), rpc.U("publish", 0)})
	m.Buy(ref.In{D: 50000}, 0, 0, addr[13], uint64(h), 0)
	check("buy amm", c.GetSC(launch, false), m)

	fmt.Println("6. staking: w12 stakes half of its tokens, w15 buys 1 DERO (tax accrues), w12 claims, w12 unstakes")
	stake := rpc.Balance(wallet(12), launch) / 2
	c.Send(wallet(12), addr[12], "", launch, []rpc.Transfer{{SCID: launch, Burn: stake}}, []rpc.Arg{rpc.S("entrypoint", "Stake")})
	m.StakeOp(addr[12], ref.In{A: stake})
	check("stake", c.GetSC(launch, false), m)
	_, _, h = c.Send(wallet(15), addr[15], "", launch, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 100000}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", 0), rpc.U("publish", 0)})
	m.Buy(ref.In{D: 100000}, 0, 0, addr[15], uint64(h), 0)
	check("buy taxed", c.GetSC(launch, false), m)
	before := rpc.Balance(wallet(12), "")
	c.Send(wallet(12), addr[12], "", launch, nil, []rpc.Arg{rpc.S("entrypoint", "Claim")})
	sends, _ := m.Claim(addr[12])
	check("claim", c.GetSC(launch, false), m)
	var reward uint64
	if len(sends) > 0 {
		reward = sends[0].Amount
	}
	after := rpc.Balance(wallet(12), "")
	fmt.Printf("  reward %d units; wallet DERO %d -> %d (fee paid in between)\n", reward, before, after)
	if reward == 0 || after+5000 < before+reward { // reward received net of the claim fee
		log.Fatalf("staker did not receive the reward (before %d after %d reward %d)", before, after, reward)
	}
	c.Send(wallet(12), addr[12], "", launch, nil, []rpc.Arg{rpc.S("entrypoint", "Unstake"), rpc.U("amt", stake)})
	m.Unstake(addr[12], stake, ref.In{})
	check("unstake", c.GetSC(launch, false), m)

	fmt.Println("7. treasury claims protocol fees")
	fd := m.FD
	c.Send(wallet(2), addr[2], "", launch, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimFees")})
	m.ClaimFees()
	check("claimfees", c.GetSC(launch, false), m)
	fmt.Printf("  claimed %d DERO units\n", fd)

	fmt.Println("8. social: install, profile, follow")
	social, _, _ := c.Send(wallet(0), addr[0], read("social.bas"), "", nil, nil)
	c.Send(wallet(12), addr[12], "", social, nil, []rpc.Arg{rpc.S("entrypoint", "Profile"), rpc.S("name", "buyer three"), rpc.S("bio", "sim"), rpc.S("avatar", "")})
	c.Send(wallet(12), addr[12], "", social, nil, []rpc.Arg{rpc.S("entrypoint", "Follow"), rpc.S("target", addr[13])})
	st := c.GetSC(social, false)
	// keys use the DVM canonical (mainnet-prefix) address form; find them by scanning
	found := 0
	for k := range st.StringKeys {
		if len(k) > 3 && (k[:3] == "pn_" || k[:3] == "fl_") {
			found++
		}
	}
	if found != 2 {
		log.Fatalf("social keys: %v", st.StringKeys)
	}
	fmt.Println("  profile and follow stored")

	fmt.Println("9. nft: holder badge for VSC (hold 1000 units for 2 blocks, price 1 DERO)")
	nft, _, _ := c.Send(wallet(1), addr[1], read("nft.bas"), "", nil, []rpc.Arg{
		rpc.S("name", "VSC Holder Badge"), rpc.S("descr", "earned by holding VSC"), rpc.S("icon", ""), rpc.S("file", ""), rpc.S("tags", "#vsc"),
		rpc.S("collection", "VSC Badges"), rpc.H("token", launch), rpc.U("minhold", 1000), rpc.U("holdblocks", 2), rpc.U("maxsupply", 10),
		rpc.U("price", 100000), rpc.S("treasury", treasury), rpc.U("fee", 50)})
	c.Send(wallet(1), addr[1], "", factory, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 10000}}, []rpc.Arg{rpc.S("entrypoint", "RegisterNFT"), rpc.H("scid", nft)})
	c.Send(wallet(13), addr[13], "", nft, []rpc.Transfer{{SCID: launch, Burn: 5000}}, []rpc.Arg{rpc.S("entrypoint", "Lock")})
	c.WaitBlocks(2)
	c.SendWithFee(wallet(13), addr[13], nft, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 100000}}, []rpc.Arg{rpc.S("entrypoint", "Mint")}, 2000) // estimator runs at height 0
	if b := rpc.Balance(wallet(13), nft); b != 1 {
		log.Fatalf("nft balance %d", b)
	}
	st = c.GetSC(nft, false)
	if st.U("minted") != 1 || st.U("fd") != 500 || st.U("cacc") != 99500 {
		log.Fatal("nft state")
	}
	c.Send(wallet(13), addr[13], "", nft, nil, []rpc.Arg{rpc.S("entrypoint", "Unlock")})
	c.Send(wallet(1), addr[1], "", nft, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimCreator")})
	c.Send(wallet(2), addr[2], "", nft, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimFees")})
	st = c.GetSC(nft, false)
	if st.U("fd") != 0 || st.U("cacc") != 0 || st.Balance != 0 {
		log.Fatal("nft payouts")
	}
	fmt.Printf("  edition 0 minted by %s; creator and treasury paid\n", st.Str("ed_0"))
	fmt.Println("SIMULATOR LAUNCH E2E OK", hex.EncodeToString([]byte{}))
}
