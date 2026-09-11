// simswap runs the atomic-swap escrow (contracts/swap.bas v2) and the order board (contracts/board.bas v3) on the
// live simulator, and exits non-zero on any outcome the contracts should not produce.
//
// Swap, installed with a minimum escrow width wmin (-wmin, 20 blocks here; a mainnet deployment names 1,600): lock
// 1 DERO from wallet A for wallet B under sha256 of a random 32-byte secret; a third wallet C — neither maker nor
// taker — sends the Claim with the secret, and the DERO goes to B, who sends nothing (Z-C); a lock naming no taker is
// refused (GAP-024), and so is one narrower than wmin, whose DERO comes back to the maker (GAP-025); a second lock is
// refused a Claim after its expiry and refunded by A. The escrow's DERO balance must end at zero.
//
// Board: post; a Unix-time cltv refused; accept, with the acceptance deadline ad_<id> read back; an early cancel
// refused; an accept near expiry cancelled at its expiry (the v2 exit, kept); and the first offer cancelled at its
// acceptance deadline, before its expiry (v3, Z-E). The deadline is board.bas aw = 200 blocks, about an hour at the
// simulator's block rate; -deadline=false skips that last wait.
//
//	cd tools && go run ./simswap -contracts ../contracts [-a 127.0.0.1:30003 -b 127.0.0.1:30004 -c 127.0.0.1:30005 -wmin 20]
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

func fail(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	daemon := flag.String("daemon", "127.0.0.1:20000", "daemon")
	contracts := flag.String("contracts", "../contracts", "contracts dir")
	wa := flag.String("a", "127.0.0.1:30003", "maker wallet rpc (locks, refunds, posts)")
	wb := flag.String("b", "127.0.0.1:30004", "taker wallet rpc (named in the lock, sends nothing to be paid; accepts)")
	wc := flag.String("c", "127.0.0.1:30005", "third-party wallet rpc (sends the taker's Claim, and a late one)")
	wmin := flag.Uint64("wmin", 20, "swap.bas install argument: the minimum escrow width in blocks (a mainnet deployment names 1600)")
	board := flag.Bool("board", true, "also run the board.bas v3 scenario")
	deadline := flag.Bool("deadline", true, "in the board scenario, wait out the acceptance deadline (aw blocks, about an hour) and cancel at it")
	flag.Parse()
	c := &rpc.Client{Daemon: *daemon, Timeout: 180 * time.Second}
	codeRaw, err := os.ReadFile(filepath.Join(*contracts, "swap.bas"))
	rpc.Must(err)
	code := ref.Canon(string(codeRaw))
	a, b, x := rpc.Address(*wa), rpc.Address(*wb), rpc.Address(*wc)
	if a == b || a == x || b == x {
		fail("the maker, taker and third wallets must be three different addresses")
	}
	t0 := time.Now()
	fmt.Printf("maker %s\ntaker %s\nthird %s\n", a, b, x)
	scid, gas, h := c.Send(*wa, a, code, "", nil, []rpc.Arg{rpc.U("wmin", *wmin)})
	st := c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] INSTALL swap.bas v%d wmin %d scid %s storage-gas %d height %d\n", time.Since(t0).Seconds(), st.U("v"), st.U("wmin"), scid, gas.GasStorage, h)
	if st.U("v") != 2 || st.U("wmin") != *wmin {
		fail("swap.bas v%d wmin %d installed, want v2 with wmin %d", st.U("v"), st.U("wmin"), *wmin)
	}
	claimArgs := func(id uint64, secret string) []rpc.Arg {
		return []rpc.Arg{rpc.S("entrypoint", "Claim"), rpc.U("id", id), rpc.S("secret", secret)}
	}
	lockArgs := func(hash, taker string, expiry uint64) []rpc.Arg {
		return []rpc.Arg{rpc.S("entrypoint", "Lock"), rpc.S("hash", hash), rpc.S("taker", taker), rpc.U("expiry", expiry), rpc.S("asset", "")}
	}
	burn := func(v uint64) []rpc.Transfer { return []rpc.Transfer{{Destination: c.RandomAddress(), Burn: v}} }

	secret, hash := newSecret()
	exp := uint64(c.Height()) + *wmin + 20
	txid, gas, h := c.Send(*wa, a, "", scid, burn(100000), lockArgs(hash, b, exp))
	fmt.Printf("[%5.1fs] LOCK 1 DERO for the taker, hash %s… expiry %d: tx %s storage-gas %d height %d\n", time.Since(t0).Seconds(), hash[:16], exp, txid, gas.GasStorage, h)

	// a wrong secret must be refused by the estimator (the app would never send it)
	if _, err := c.Estimate(x, "", scid, nil, claimArgs(0, hex.EncodeToString(make([]byte, 32)))); err == nil {
		fail("a wrong secret passed the estimator")
	} else {
		fmt.Printf("[%5.1fs] wrong secret from the third wallet refused at estimate: %.60s\n", time.Since(t0).Seconds(), err.Error())
	}
	// The third wallet sends the Claim. The taker sends nothing, so her balance moves by exactly the lock.
	beforeB, beforeC := rpc.Balance(*wb, ""), rpc.Balance(*wc, "")
	txid, gas, h = c.Send(*wc, x, "", scid, nil, claimArgs(0, secret), *wb)
	afterB, afterC := rpc.Balance(*wb, ""), rpc.Balance(*wc, "")
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] CLAIM for the taker, sent by the third wallet: tx %s storage-gas %d height %d; taker %d → %d (%+d, sent nothing); third %d → %d (%+d, its fee only); st_0=%d secret on chain=%s…\n",
		time.Since(t0).Seconds(), txid, gas.GasStorage, h, beforeB, afterB, int64(afterB)-int64(beforeB), beforeC, afterC, int64(afterC)-int64(beforeC), st.U("st_0"), st.Str("x_0")[:16])
	if st.U("st_0") != 1 || st.Str("x_0") != secret {
		fail("the third wallet's Claim did not settle lock 0")
	}
	if int64(afterB)-int64(beforeB) != 100000 {
		fail("the named taker was paid %d, want 100000", int64(afterB)-int64(beforeB))
	}
	if afterC > beforeC {
		fail("the third wallet that sent the Claim gained %d: only the taker may be paid", afterC-beforeC)
	}

	// GAP-024: a lock naming no taker is refused. Lock reads BLOCK_HEIGHT() only against the expiry, which the
	// estimator (height 0) always passes here, so its verdict is the taker rule's.
	if _, err := c.Estimate(a, "", scid, burn(10000), lockArgs(hash, "", uint64(c.Height())+*wmin+20)); err == nil {
		fail("a Lock naming no taker passed the estimator")
	} else {
		fmt.Printf("[%5.1fs] LOCK naming no taker refused at estimate: %.60s\n", time.Since(t0).Seconds(), err.Error())
	}
	// GAP-025: a lock narrower than wmin is refused on chain (the estimator's height 0 cannot judge it), and its DERO
	// goes back to the maker, a ring-2 signer: only the fixed fee is spent.
	narrow := uint64(c.Height()) + *wmin - 1
	beforeA := rpc.Balance(*wa, "")
	txid, hh := c.SendWithFee(*wa, a, scid, burn(10000), lockArgs(hash, b, narrow), 1500, *wa)
	afterA := rpc.Balance(*wa, "")
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] LOCK 0.1 DERO narrower than wmin (expiry %d) sent tx %s height %d → n=%d escrow %d; maker %d → %d (%+d: the fee only, the 10000 came back)\n",
		time.Since(t0).Seconds(), narrow, txid, hh, st.U("n"), st.Balance, beforeA, afterA, int64(afterA)-int64(beforeA))
	if st.U("n") != 1 || st.Balance != 0 {
		fail("a lock narrower than wmin was accepted")
	}
	if int64(afterA)-int64(beforeA) != -1500 {
		fail("the refused lock cost the maker %d, not the 1500 fee: its DERO did not come back", int64(beforeA)-int64(afterA))
	}

	// refund path: lock a few blocks past the minimum width, wait it out, a late claim is refused, the maker refunds
	exp2 := uint64(c.Height()) + *wmin + 3
	txid, _, h = c.Send(*wa, a, "", scid, burn(50000), lockArgs(hash, b, exp2))
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] LOCK 0.5 DERO for the taker, expiry %d: tx %s height %d; n=%d\n", time.Since(t0).Seconds(), exp2, txid, h, st.U("n"))
	if st.U("n") != 2 {
		fail("the lock wmin + 3 blocks wide was refused")
	}
	for uint64(c.Height()) < exp2 {
		c.WaitBlocks(1)
	}
	if _, err := c.Estimate(x, "", scid, nil, claimArgs(1, secret)); err == nil {
		fmt.Println("note: estimator ran at height 0, so it accepts an expired claim; the chain refuses it (fee-fixed send below)")
	}
	if id, hh := c.SendWithFee(*wc, x, scid, nil, claimArgs(1, secret), 1500, *wc); id != "" {
		st = c.GetSC(scid, false)
		fmt.Printf("[%5.1fs] late CLAIM by the third wallet sent tx %s height %d → st_1=%d (0 = refused by the contract)\n", time.Since(t0).Seconds(), id, hh, st.U("st_1"))
		if st.U("st_1") != 0 {
			fail("a Claim after expiry settled lock 1")
		}
	}
	beforeA = rpc.Balance(*wa, "")
	txid, hh = c.SendWithFee(*wa, a, scid, nil, []rpc.Arg{rpc.S("entrypoint", "Refund"), rpc.U("id", 1)}, 1500, *wa)
	afterA = rpc.Balance(*wa, "")
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] REFUND by maker: tx %s height %d; maker balance %d → %d; st_1=%d\n", time.Since(t0).Seconds(), txid, hh, beforeA, afterA, st.U("st_1"))
	if st.U("st_1") != 2 {
		fail("the maker's refund of lock 1 did not settle")
	}
	fmt.Printf("contract DERO balance now %d (expect 0)\n", st.Balance)
	if st.Balance != 0 {
		fail("the escrow kept %d", st.Balance)
	}
	fmt.Println("swap: third-party claim paid the taker, no-taker and too-narrow locks refused, late claim refused, refund, escrow empty — OK")
	if *board {
		runBoard(c, t0, *contracts, *wa, a, *wb, b, *deadline)
	}
}

func newSecret() (secret, hash string) {
	var s [32]byte
	if _, err := rand.Read(s[:]); err != nil {
		fail("random: %v", err)
	}
	sum := sha256.Sum256(s[:])
	return hex.EncodeToString(s[:]), hex.EncodeToString(sum[:])
}

// runBoard exercises contracts/board.bas v3 (docs/ZEC_SWAP.md §4) on the live simulator: the maker posts an offer
// under a hash of a secret only the maker holds, the taker accepts with a refund key hash and a refund height, and
// the board records the acceptance deadline ad_<id>. A live accepted offer cannot be cancelled; one accepted near its
// expiry is cancelled at the expiry; and one whose expiry lies past its deadline is cancelled at the deadline — so a
// taker who accepted and walked holds an offer for aw blocks, not until its expiry.
func runBoard(c *rpc.Client, t0 time.Time, contracts, wa, a, wb, b string, deadline bool) {
	raw, err := os.ReadFile(filepath.Join(contracts, "board.bas"))
	rpc.Must(err)
	code := ref.Canon(string(raw))
	scid, gas, h := c.Send(wa, a, code, "", nil, nil)
	st := c.GetSC(scid, false)
	aw := st.U("aw")
	fmt.Printf("[%5.1fs] INSTALL board.bas v%d scid %s storage-gas %d height %d; acceptance window aw=%d blocks\n", time.Since(t0).Seconds(), st.U("v"), scid, gas.GasStorage, h, aw)
	if st.U("v") != 3 || aw == 0 {
		fail("board.bas v%d aw %d installed, want v3 with an acceptance window", st.U("v"), aw)
	}
	_, hash := newSecret()
	postArgs := func(expiry uint64) []rpc.Arg {
		return []rpc.Arg{rpc.S("entrypoint", "Post"), rpc.S("chain", "zcash-t"), rpc.S("giveAsset", ""), rpc.U("giveAmount", 100000), rpc.U("wantAmount", 25000000), rpc.S("wantAddr", "t1SimulatorMakerTransparentAddr"), rpc.S("hash", hash), rpc.U("expiry", expiry)}
	}
	pkh := strings.Repeat("cd", 20)
	acceptArgs := func(id, cltv uint64) []rpc.Arg {
		return []rpc.Arg{rpc.S("entrypoint", "Accept"), rpc.U("id", id), rpc.S("refundPkh", pkh), rpc.U("cltv", cltv)}
	}
	cancelArgs := func(id uint64) []rpc.Arg { return []rpc.Arg{rpc.S("entrypoint", "Cancel"), rpc.U("id", id)} }

	// Offer 0 expires well past its deadline, so a cancel at the deadline is the deadline's doing, not the expiry's.
	e0 := uint64(c.Height()) + aw + 60
	txid, _, h := c.Send(wa, a, "", scid, nil, postArgs(e0))
	fmt.Printf("[%5.1fs] POST offer 0: 1 DERO for 0.25 ZEC under %s… expiry %d: tx %s height %d\n", time.Since(t0).Seconds(), hash[:16], e0, txid, h)
	// A Unix-time refund height is refused. Accept is gated on the offer's expiry only, which the estimator (height 0)
	// always passes, so its verdict here is the cltv rule's.
	if _, err := c.Estimate(b, "", scid, nil, acceptArgs(0, 500000000)); err == nil {
		fail("Accept with cltv 500000000 (a Unix time to a Zcash script) passed the estimator")
	} else {
		fmt.Printf("[%5.1fs] ACCEPT with cltv 500000000 refused at estimate: %.60s\n", time.Since(t0).Seconds(), err.Error())
	}
	txid, _, ha := c.Send(wb, b, "", scid, nil, acceptArgs(0, 2500000))
	st = c.GetSC(scid, false)
	ad0 := st.U("ad_0")
	fmt.Printf("[%5.1fs] ACCEPT offer 0 by taker: tx %s height %d; st_0=%d cl_0=%d ad_0=%d (accept height + aw = %d)\n", time.Since(t0).Seconds(), txid, ha, st.U("st_0"), st.U("cl_0"), ad0, uint64(ha)+aw)
	if st.U("st_0") != 1 || ad0 != uint64(ha)+aw {
		fail("accept of offer 0: st_0 %d ad_0 %d", st.U("st_0"), ad0)
	}
	// Both cancels are gated on heights, and the estimator runs at height 0, so its verdict says nothing about the
	// chain's: send each fee-fixed (as the escrow's Refund is) and read the state back.
	if id, hh := c.SendWithFee(wa, a, scid, nil, cancelArgs(0), 1500, wa); id != "" {
		st = c.GetSC(scid, false)
		fmt.Printf("[%5.1fs] early CANCEL of the live accepted offer 0 sent tx %s height %d → st_0=%d (1 = refused by the contract)\n", time.Since(t0).Seconds(), id, hh, st.U("st_0"))
		if st.U("st_0") != 1 {
			fail("the live accepted offer was cancelled before its deadline and its expiry")
		}
	}

	// Offer 1 expires before its deadline: the maker cancels it at the expiry, as in v2.
	e1 := uint64(c.Height()) + 10
	txid, _, h = c.Send(wa, a, "", scid, nil, postArgs(e1))
	fmt.Printf("[%5.1fs] POST offer 1, expiry %d: tx %s height %d\n", time.Since(t0).Seconds(), e1, txid, h)
	txid, _, h = c.Send(wb, b, "", scid, nil, acceptArgs(1, 2500000))
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] ACCEPT offer 1 by taker: tx %s height %d; st_1=%d ad_1=%d (past its expiry %d)\n", time.Since(t0).Seconds(), txid, h, st.U("st_1"), st.U("ad_1"), e1)
	if st.U("st_1") != 1 {
		fail("accept of offer 1 did not bind it")
	}
	for uint64(c.Height()) < e1 {
		c.WaitBlocks(1)
	}
	txid, hh := c.SendWithFee(wa, a, scid, nil, cancelArgs(1), 1500, wa)
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] CANCEL of offer 1 by maker after its expiry: tx %s height %d; st_1=%d (2 = cancelled)\n", time.Since(t0).Seconds(), txid, hh, st.U("st_1"))
	if st.U("st_1") != 2 {
		fail("the expired accepted offer 1 did not cancel")
	}

	if !deadline {
		fmt.Println("board: post, Unix-time cltv refused, accept with deadline, early cancel refused, cancel after expiry — OK (acceptance-deadline wait skipped: -deadline=false)")
		return
	}
	fmt.Printf("[%5.1fs] waiting for offer 0's acceptance deadline, height %d (now %d)\n", time.Since(t0).Seconds(), ad0, c.Height())
	for uint64(c.Height()) < ad0 {
		c.WaitBlocks(1)
		if n := c.Height(); n%25 == 0 {
			fmt.Printf("[%5.1fs] height %d of %d\n", time.Since(t0).Seconds(), n, ad0)
		}
	}
	txid, hh = c.SendWithFee(wa, a, scid, nil, cancelArgs(0), 1500, wa)
	st = c.GetSC(scid, false)
	fmt.Printf("[%5.1fs] CANCEL of offer 0 by maker at its acceptance deadline: tx %s height %d (ad_0 %d, expiry %d); st_0=%d (2 = cancelled)\n", time.Since(t0).Seconds(), txid, hh, ad0, e0, st.U("st_0"))
	if st.U("st_0") != 2 {
		fail("the maker could not cancel offer 0 at its acceptance deadline")
	}
	if uint64(hh) >= e0 {
		fail("the cancel landed at %d, at or after the offer's expiry %d: it proves nothing about the deadline", hh, e0)
	}
	fmt.Println("board: post, Unix-time cltv refused, accept with deadline, early cancel refused, cancel after expiry, cancel at the acceptance deadline before expiry — OK")
}
