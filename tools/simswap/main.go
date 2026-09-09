// simswap runs the atomic-swap escrow (contracts/swap.bas) on the live simulator: install, lock 1 DERO from wallet A
// for wallet B under sha256 of a random 32-byte secret, B claims with the hex secret, then a second lock with a
// 3-block expiry that A refunds. Prints every txid, fee and gas so the run can be recorded (research experiment 2).
//
//	cd tools && go run ./simswap -contracts ../contracts [-a 127.0.0.1:30003 -b 127.0.0.1:30004]
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	ref "veil/reference"

	rpc "veil/tools/simrpc"
)

func main() {
	daemon := flag.String("daemon", "127.0.0.1:20000", "daemon")
	contracts := flag.String("contracts", "../contracts", "contracts dir")
	wa := flag.String("a", "127.0.0.1:30003", "maker wallet rpc (locks)")
	wb := flag.String("b", "127.0.0.1:30004", "taker wallet rpc (claims)")
	board := flag.Bool("board", true, "also run the board.bas v2 scenario: post, accept, early cancel refused, cancel after expiry")
	flag.Parse()
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	codeRaw, err := os.ReadFile(filepath.Join(*contracts, "swap.bas"))
	rpc.Must(err)
	code := ref.Canon(string(codeRaw))
	a, b := rpc.Address(*wa), rpc.Address(*wb)
	t0 := time.Now()
	fmt.Printf("maker %s\ntaker %s\n", a, b)
	scid, gas, h := c.Send(*wa, a, code, "", nil, nil)
	fmt.Printf("[%5.1fs] INSTALL swap.bas scid %s storage-gas %d height %d\n", time.Since(t0).Seconds(), scid, gas.GasStorage, h)

	var secret [32]byte
	rpc.Must(readRand(secret[:]))
	sum := sha256.Sum256(secret[:])
	hash := hex.EncodeToString(sum[:])
	height := c.Height()
	exp := uint64(height) + 40
	txid, gas, h := c.Send(*wa, a, "", scid, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 100000}}, []rpc.Arg{rpc.S("entrypoint", "Lock"), rpc.S("hash", hash), rpc.S("taker", b), rpc.U("expiry", exp), rpc.S("asset", "")})
	fmt.Printf("[%5.1fs] LOCK 1 DERO for taker, hash %s… expiry %d: tx %s storage-gas %d height %d\n", time.Since(t0).Seconds(), hash[:16], exp, txid, gas.GasStorage, h)

	before := rpc.Balance(*wb, "")
	// a wrong secret must be refused by the estimator (the app would never send it)
	if _, err := c.Estimate(b, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Claim"), rpc.U("id", 0), rpc.S("secret", hex.EncodeToString(make([]byte, 32)))}); err == nil {
		fmt.Println("BUG: wrong secret passed the estimator")
	} else {
		fmt.Printf("[%5.1fs] wrong secret refused at estimate: %.60s\n", time.Since(t0).Seconds(), err.Error())
	}
	txid, gas, h = c.Send(*wb, b, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Claim"), rpc.U("id", 0), rpc.S("secret", hex.EncodeToString(secret[:]))}, *wb)
	after := rpc.Balance(*wb, "")
	st := c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] CLAIM by taker: tx %s storage-gas %d height %d; taker balance %d → %d (+%d incl. fee); st_0=%d secret on chain=%s…\n", time.Since(t0).Seconds(), txid, gas.GasStorage, h, before, after, int64(after)-int64(before), st.U("st_0"), st.Str("x_0")[:16])

	// refund path: lock with a short expiry, wait it out, refund
	height = c.Height()
	exp2 := uint64(height) + 3
	txid, gas, h = c.Send(*wa, a, "", scid, []rpc.Transfer{{Destination: c.RandomAddress(), Burn: 50000}}, []rpc.Arg{rpc.S("entrypoint", "Lock"), rpc.S("hash", hash), rpc.S("taker", ""), rpc.U("expiry", exp2), rpc.S("asset", "")})
	fmt.Printf("[%5.1fs] LOCK 0.5 DERO for anyone, expiry %d: tx %s height %d\n", time.Since(t0).Seconds(), exp2, txid, h)
	for c.Height() < int64(exp2) {
		time.Sleep(2 * time.Second)
	}
	if _, err := c.Estimate(b, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Claim"), rpc.U("id", 1), rpc.S("secret", hex.EncodeToString(secret[:]))}); err == nil {
		fmt.Println("note: estimator ran at height 0, so it accepts an expired claim; the chain refuses it (fee-fixed send below)")
	}
	if id, hh := c.SendWithFee(*wb, b, scid, nil, []rpc.Arg{rpc.S("entrypoint", "Claim"), rpc.U("id", 1), rpc.S("secret", hex.EncodeToString(secret[:]))}, 1500, *wb); id != "" {
		st = c.GetSC(scid, false)
		fmt.Printf("[%5.1fs] late CLAIM sent tx %s height %d → st_1=%d (0 = refused by the contract)\n", time.Since(t0).Seconds(), id, hh, st.U("st_1"))
	}
	beforeA := rpc.Balance(*wa, "")
	txid, hh := c.SendWithFee(*wa, a, scid, nil, []rpc.Arg{rpc.S("entrypoint", "Refund"), rpc.U("id", 1)}, 1500, *wa)
	afterA := rpc.Balance(*wa, "")
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] REFUND by maker: tx %s height %d; maker balance %d → %d; st_1=%d\n", time.Since(t0).Seconds(), txid, hh, beforeA, afterA, st.U("st_1"))
	fmt.Printf("contract DERO balance now %d (expect 0)\n", st.Balance)
	if *board {
		runBoard(c, t0, *contracts, *wa, a, *wb, b)
	}
}

func readRand(b []byte) error { _, err := rand.Read(b); return err }

// runBoard exercises contracts/board.bas v2 (docs/ZEC_SWAP.md §4) on the live simulator: the maker posts an offer
// under a hash of a secret only the maker holds, the taker accepts with a refund key hash and a refund height, a
// live accepted offer cannot be cancelled, and once the offer has expired the maker cancels it — the exit v1
// lacked, so that a taker who accepted and walked leaves nothing that needs anyone else.
func runBoard(c *rpc.Client, t0 time.Time, contracts, wa, a, wb, b string) {
	raw, err := os.ReadFile(filepath.Join(contracts, "board.bas"))
	rpc.Must(err)
	code := ref.Canon(string(raw))
	scid, gas, h := c.Send(wa, a, code, "", nil, nil)
	fmt.Printf("[%5.1fs] INSTALL board.bas scid %s storage-gas %d height %d\n", time.Since(t0).Seconds(), scid, gas.GasStorage, h)
	var secret [32]byte
	rand.Read(secret[:])
	sum := sha256.Sum256(secret[:])
	hash := hex.EncodeToString(sum[:])
	exp := uint64(c.Height()) + 6
	txid, _, h := c.Send(wa, a, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Post"), rpc.S("chain", "zcash-t"), rpc.S("giveAsset", ""), rpc.U("giveAmount", 100000), rpc.U("wantAmount", 25000000), rpc.S("wantAddr", "t1SimulatorMakerTransparentAddr"), rpc.S("hash", hash), rpc.U("expiry", exp)})
	fmt.Printf("[%5.1fs] POST offer 0: 1 DERO for 0.25 ZEC under %s… expiry %d: tx %s height %d\n", time.Since(t0).Seconds(), hash[:16], exp, txid, h)
	pkh := strings.Repeat("cd", 20)
	txid, _, h = c.Send(wb, b, "", scid, nil, []rpc.Arg{rpc.S("entrypoint", "Accept"), rpc.U("id", 0), rpc.S("refundPkh", pkh), rpc.U("cltv", 2500000)})
	st := c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] ACCEPT by taker: tx %s height %d; st_0=%d cl_0=%d\n", time.Since(t0).Seconds(), txid, h, st.U("st_0"), st.U("cl_0"))
	// Both cancels are gated on the expiry height, and the estimator runs at height 0, so its verdict says
	// nothing about the chain's: send each fee-fixed (as the escrow's Refund is) and read the state back.
	cancelArgs := []rpc.Arg{rpc.S("entrypoint", "Cancel"), rpc.U("id", 0)}
	if id, hh := c.SendWithFee(wa, a, scid, nil, cancelArgs, 1500, wa); id != "" {
		st = c.GetSC(scid, false)
		fmt.Printf("[%5.1fs] early CANCEL of the live accepted offer sent tx %s height %d → st_0=%d (1 = refused by the contract)\n", time.Since(t0).Seconds(), id, hh, st.U("st_0"))
		if st.U("st_0") != 1 {
			fmt.Println("the live accepted offer was cancelled before its expiry: wrong")
			os.Exit(1)
		}
	}
	for uint64(c.Height()) < exp {
		c.WaitBlocks(1)
	}
	txid, h = c.SendWithFee(wa, a, scid, nil, cancelArgs, 1500, wa)
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] CANCEL by maker after expiry: tx %s height %d; st_0=%d (2 = cancelled)\n", time.Since(t0).Seconds(), txid, h, st.U("st_0"))
	if st.U("st_0") != 2 {
		fmt.Println("the expired accepted offer did not cancel")
		os.Exit(1)
	}
	fmt.Println("board: post, accept, early cancel refused, cancel after expiry — OK")
}
