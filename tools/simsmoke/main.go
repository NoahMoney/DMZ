// simsmoke drives a running derohe simulator end to end over plain JSON-RPC (no derohe imports):
// install the test asset and pool.bas, add liquidity, swap both ways, remove liquidity, and after every step
// compare the on-chain reserves with the reference model to the atomic unit.
//
//	go run ./simsmoke -contracts ../contracts
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
	w0        = flag.String("w0", "127.0.0.1:30000", "wallet rpc: liquidity provider / deployer")
	w1        = flag.String("w1", "127.0.0.1:30001", "wallet rpc: trader")
	w2        = flag.String("w2", "127.0.0.1:30002", "wallet rpc: treasury")
	contracts = flag.String("contracts", "../contracts", "contracts directory")
	timeout   = flag.Duration("timeout", 180*time.Second, "per-tx confirmation timeout")
)

func check(step string, st rpc.SCState, m *ref.Pool) {
	got := [3]uint64{st.U("rd"), st.U("ra"), st.U("lp")}
	want := [3]uint64{m.RD, m.RA, m.LP}
	fd, fa := st.U("fd"), st.U("fa")
	if got != want || fd != m.FD || fa != m.FA {
		log.Fatalf("%s: chain rd/ra/lp/fd/fa = %v/%d/%d, model %v/%d/%d", step, got, fd, fa, want, m.FD, m.FA)
	}
	fmt.Printf("  %-18s rd=%d ra=%d lp=%d fd=%d fa=%d tc=%d pc=%d  (matches model)\n", step, got[0], got[1], got[2], fd, fa, st.U("tc"), st.U("pc"))
}

func main() {
	flag.Parse()
	log.SetFlags(0)
	c := &rpc.Client{Daemon: *daemon, Timeout: *timeout}
	poolRaw, err := os.ReadFile(*contracts + "/pool.bas")
	rpc.Must(err)
	pool := ref.Canon(string(poolRaw))
	asset := "Function InitializePrivate() Uint64\n10 RETURN 0\nEnd Function\nFunction Mint(amount Uint64) Uint64\n10 SEND_ASSET_TO_ADDRESS(SIGNER(), amount, SCID())\n20 RETURN 0\nEnd Function\n"

	a0, a1, a2 := rpc.Address(*w0), rpc.Address(*w1), rpc.Address(*w2)
	fmt.Printf("daemon %s height %d\nw0 %s\nw1 %s\ntreasury %s\n", *daemon, c.Height(), a0, a1, a2)

	fmt.Println("1. install test asset")
	assetSCID, gas, _ := c.Send(*w0, a0, asset, "", nil, nil)
	fmt.Printf("  asset scid %s (gas %d/%d)\n", assetSCID, gas.GasCompute, gas.GasStorage)

	fmt.Println("2. mint 10,000,000 units to w0 and w1")
	for _, w := range []struct{ wallet, addr string }{{*w0, a0}, {*w1, a1}} {
		c.Send(w.wallet, w.addr, "", assetSCID, nil, []rpc.Arg{rpc.S("entrypoint", "Mint"), rpc.U("amount", 10000000)})
	}

	fmt.Println("3. install pool.bas (fee 50 bps, protocol share 50%)")
	poolSCID, gas, _ := c.Send(*w0, a0, pool, "", nil, []rpc.Arg{rpc.H("asset", assetSCID), rpc.S("treasury", a2), rpc.U("fee", 50), rpc.U("pfee", 5000)})
	fmt.Printf("  pool scid %s (gas %d/%d, code %d bytes)\n", poolSCID, gas.GasCompute, gas.GasStorage, len(pool))
	st := c.GetSC(poolSCID, true)
	if st.Str("kind") != "pool" || hex.EncodeToString([]byte(st.Str("asset"))) != assetSCID {
		log.Fatal("init state wrong")
	}
	m := ref.NewPool(50, 5000)
	check("install", st, m)

	fmt.Println("4. w0 adds 2 DERO + 1,000,000 units")
	d, a := uint64(200000), uint64(1000000)
	_, gas, h := c.Send(*w0, a0, "", poolSCID, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}, {SCID: assetSCID, Burn: a}}, []rpc.Arg{rpc.S("entrypoint", "AddLiquidity"), rpc.U("min_lp", 0)})
	m.AddLiquidity(ref.In{D: d, A: a}, 0, uint64(h), 0)
	check("AddLiquidity", c.GetSC(poolSCID, false), m)
	fmt.Printf("  gas %d/%d\n", gas.GasCompute, gas.GasStorage)

	fmt.Println("5. w1 buys with 0.5 DERO (published)")
	d = 50000
	q, _ := m.Quote(d)
	_, gas, h = c.Send(*w1, a1, "", poolSCID, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", q), rpc.U("publish", 1)})
	m.SwapDeroForAsset(ref.In{D: d}, q, 1, a1, uint64(h), 0)
	st = c.GetSC(poolSCID, false)
	check("SwapDeroForAsset", st, m)
	fmt.Printf("  quote %d units, t_0 = %q, gas %d/%d\n", q, st.Str("t_0"), gas.GasCompute, gas.GasStorage)

	fmt.Println("6. w1 sells 20,000 units (unpublished)")
	a = 20000
	_, gas, h = c.Send(*w1, a1, "", poolSCID, []rpc.Transfer{{SCID: assetSCID, Burn: a}}, []rpc.Arg{rpc.S("entrypoint", "SwapAssetForDero"), rpc.U("min_out", 0), rpc.U("publish", 0)})
	m.SwapAssetForDero(ref.In{A: a}, 0, 0, a1, uint64(h), 0)
	check("SwapAssetForDero", c.GetSC(poolSCID, false), m)
	fmt.Printf("  gas %d/%d\n", gas.GasCompute, gas.GasStorage)

	fmt.Println("7. slippage: buy with min_out above the quote must be rejected by the estimator")
	d = 50000
	q, _ = m.Quote(d)
	if _, err := c.Estimate(a1, "", poolSCID, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: d}}, []rpc.Arg{rpc.S("entrypoint", "SwapDeroForAsset"), rpc.U("min_out", q+1), rpc.U("publish", 0)}); err == nil {
		log.Fatal("gas estimate accepted a call that must revert")
	}
	fmt.Println("  rejected as expected")

	fmt.Println("8. w0 removes half of its LP")
	l := (m.LP - ref.MinLiq) / 2
	_, gas, h = c.Send(*w0, a0, "", poolSCID, []rpc.Transfer{{SCID: poolSCID, Burn: l}}, []rpc.Arg{rpc.S("entrypoint", "RemoveLiquidity"), rpc.U("min_dero", 0), rpc.U("min_asset", 0)})
	m.RemoveLiquidity(ref.In{L: l}, 0, 0, uint64(h), 0)
	check("RemoveLiquidity", c.GetSC(poolSCID, false), m)
	fmt.Printf("  gas %d/%d\n", gas.GasCompute, gas.GasStorage)

	fmt.Println("9. treasury claims protocol fees")
	fd, fa := m.FD, m.FA
	_, gas, _ = c.Send(*w2, a2, "", poolSCID, nil, []rpc.Arg{rpc.S("entrypoint", "ClaimFees")})
	m.ClaimFees()
	check("ClaimFees", c.GetSC(poolSCID, false), m)
	fmt.Printf("  claimed %d DERO units and %d asset units, gas %d/%d\n", fd, fa, gas.GasCompute, gas.GasStorage)
	fmt.Println("SIMULATOR SMOKE OK")
}
