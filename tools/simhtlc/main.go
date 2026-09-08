// simhtlc drives whole DERO <-> Monero swaps on the local DERO simulator (docs/XMR_SWAP_BUILD.md §3.D, §5): it
// installs contracts/htlc.bas through reference.Canon, runs bob's and alice's companions in-process against the
// real daemon and wallets, with the fake monero-wallet-rpc standing in for Monero, and exercises all three exits
// — the happy path; bob refunds and alice recovers her XMR with s_b; bob vanishes and alice punishes. It prints
// every transaction id and reads every contract key back. This is the evidence the build is gated on.
//
//	cd tools && go run ./simhtlc -fake-monero -prover "wsl.exe -e /home/wilma/target/proof-xmr/release/proof-xmr"
//	cd tools && go run ./simhtlc -fake-monero -prover "..." -fund 8000    # after a few runs, when bob has run low
//
// Wallets: 30006 is bob (has DERO, locks first), 30007 is alice (has XMR, claims; also pays the install). The
// driver prints both DERO balances against what the three exits need and stops if either is short; -fund N
// has alice's wallet (the richer one on the simulator) pay bob N atomic units first, as a plain transfer.
//
// The timelock policy is lowered for the run — the simulator mines a block every 18 s, so the mainnet minimums
// would take hours — and the companions accept that only because the daemon reports itself as the simulator.
// -t1 is 28 blocks (mainnet policy: 400) because that is what the happy path needs: the lock takes 2–7 blocks
// to mine and be announced, alice ages it -stable blocks, the fake Monero mines a block per driver round, and
// her Claim must go out before t1 − 8 − -claim-margin (Invariant 9) and be mined before t1. At 16 blocks the
// refund path degraded to "alice refuses" whenever the lock was slow, which is protocol-correct and shows
// nothing; 28 leaves her ten blocks of budget after the slowest lock seen.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"flag"
	"fmt"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ref "veil/reference"
	"veil/tools/xmrswap"
)

var (
	daemon     = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	bobWallet  = flag.String("bob", "127.0.0.1:30006", "bob's DERO wallet rpc (the maker: has DERO)")
	aliceW     = flag.String("alice", "127.0.0.1:30007", "alice's DERO wallet rpc (the taker: has XMR, claims)")
	contracts  = flag.String("contracts", "../contracts", "contracts dir")
	htlc       = flag.String("htlc", "", "an already installed htlc.bas scid (default: install one)")
	fakeMonero = flag.Bool("fake-monero", false, "use the in-process fake monero-wallet-rpc (the only Monero backend this driver has)")
	prover     = flag.String("prover", "", "the proof-xmr command, e.g. \"wsl.exe -e /home/wilma/target/proof-xmr/release/proof-xmr\"")
	fakeProver = flag.Bool("fake-prover", false, "stand-in prover whose proofs are tags, not zero-knowledge proofs: only for exercising the driver when proof-xmr is not built")
	amountDero = flag.Uint64("amount-dero", 2000, "DERO atomic units bob locks per swap")
	amountXmr  = flag.Uint64("amount-xmr", 1_000_000_000_000, "piconero alice locks per swap (1 XMR)")
	t1         = flag.Uint64("t1", 28, "blocks from bob's lock to t1 (policy on mainnet: 400; see the package comment)")
	t2         = flag.Uint64("t2", 8, "blocks from t1 to t2 (policy on mainnet: 400)")
	t1Min      = flag.Uint64("t1-min", 10, "alice's floor on t1 - now")
	t2Min      = flag.Uint64("t2-min", 6, "alice's floor on t2 - t1")
	stable     = flag.Uint64("stable", 2, "DERO blocks alice lets the lock age before locking XMR (policy: 8)")
	margin     = flag.Uint64("claim-margin", 2, "blocks of slack under t1 − 8 after which alice sends no Claim (policy: 8)")
	confs      = flag.Uint64("confs", 10, "Monero confirmations bob waits for before the gate (policy: 20; the fake Monero mines a block per round)")
	fee        = flag.Uint64("fee", 2000, "fixed fee for Refund and Punish")
	paths      = flag.String("paths", "happy,refund,punish", "which exits to run, in order")
	stateDir   = flag.String("state-dir", "", "where state files and the channel go (default: a temp dir)")
	round      = flag.Duration("round", time.Second, "pause between driver rounds")
	timeout    = flag.Duration("timeout", 25*time.Minute, "give up on one path after this long")
	fund       = flag.Uint64("fund", 0, "if bob is short, alice's wallet sends him this many atomic units first (a plain transfer)")
)

func main() {
	flag.Parse()
	if !*fakeMonero {
		log.Fatal("-fake-monero is required: this driver has no other Monero backend (a stagenet run is a separate, one-off step)")
	}
	if *prover == "" && !*fakeProver {
		log.Fatal("-prover \"<command>\" is required (or -fake-prover, which proves nothing)")
	}
	ctx := context.Background()
	t0 := time.Now()
	say := func(format string, a ...interface{}) {
		fmt.Printf("[%6.1fs] "+format+"\n", append([]interface{}{time.Since(t0).Seconds()}, a...)...)
	}

	// 1. the two DERO wallets: who they are and what they hold
	bobD := xmrswap.NewDeroRPC(*daemon, *bobWallet, "")
	aliceD := xmrswap.NewDeroRPC(*daemon, *aliceW, "")
	bobD.FixedFee, aliceD.FixedFee = *fee, *fee
	bobD.Log, aliceD.Log = deroLog("bob"), deroLog("alice")
	testnet, network, err := bobD.Info(ctx)
	if err != nil {
		log.Fatalf("daemon %s: %v", *daemon, err)
	}
	if !testnet || !strings.EqualFold(network, "simulator") {
		log.Fatalf("daemon %s reports %q (testnet=%v): this driver runs on the simulator only", *daemon, network, testnet)
	}
	height, _ := bobD.Height(ctx)
	bobAddr, err := bobD.Address(ctx)
	if err != nil {
		log.Fatalf("bob's wallet %s: %v", *bobWallet, err)
	}
	aliceAddr, err := aliceD.Address(ctx)
	if err != nil {
		log.Fatalf("alice's wallet %s: %v", *aliceW, err)
	}
	bobBal, err := bobD.Balance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	aliceBal, err := aliceD.Balance(ctx)
	if err != nil {
		log.Fatal(err)
	}
	pathList := strings.Split(*paths, ",")
	// bob: one lock per path plus its fee (~600), one refund at the fixed fee; alice: the install (~4,000),
	// one claim (~600), one punish at the fixed fee
	needBob := uint64(len(pathList))*(*amountDero+1000) + *fee + 1000
	needAlice := uint64(6000) + *fee + 1000
	say("simulator %s at height %d (18 s blocks)", network, height)
	say("bob   %s (wallet %s) has %d DERO atomic units; needs about %d", bobAddr, *bobWallet, bobBal, needBob)
	say("alice %s (wallet %s) has %d DERO atomic units; needs about %d", aliceAddr, *aliceW, aliceBal, needAlice)
	say("timelocks: t1 = lock + %d blocks, t2 = t1 + %d; alice sends no Claim from t1 − 8 − %d = lock + %d on (Invariant 9)", *t1, *t2, *margin, *t1-8-*margin)
	if bobBal < needBob && *fund > 0 && aliceBal >= needAlice+*fund+1000 {
		// the simulator's wallets hold little; alice's wallet is the richer one here, so it tops bob up with a
		// plain transfer (not through the contract: that is what the swap itself does)
		txid, err := aliceD.Pay(ctx, bobAddr, *fund)
		if err != nil {
			log.Fatalf("fund bob: %v", err)
		}
		bobBal, _ = bobD.Balance(ctx)
		aliceBal, _ = aliceD.Balance(ctx)
		say("FUND bob with %d from alice: tx %s; bob now %d, alice %d", *fund, txid, bobBal, aliceBal)
	}
	if bobBal < needBob || aliceBal < needAlice {
		log.Fatalf("not enough DERO: bob %d (needs %d), alice %d (needs %d); pass -fund %d (alice pays bob) or lower -amount-dero", bobBal, needBob, aliceBal, needAlice, needBob-bobBal+1000)
	}
	if *t1 <= 8+*margin+*stable+4 {
		log.Fatalf("-t1 %d leaves alice no room: the lock takes blocks to mine, she ages it -stable %d, and she sends no Claim from t1 − 8 − -claim-margin %d on", *t1, *stable, *margin)
	}

	// 2. the contract, installed as the app would install it: comments stripped, gas measured on what is sent
	scid := *htlc
	if scid == "" {
		path := filepath.Join(*contracts, "htlc.bas")
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("htlc.bas is not in %s yet: %v", *contracts, err)
		}
		code := ref.Canon(string(raw))
		var gas uint64
		scid, gas, err = aliceD.Install(ctx, aliceAddr, code)
		if err != nil {
			log.Fatalf("install: %v", err)
		}
		say("INSTALL htlc.bas: scid %s, %d canonical bytes, storage gas %d (cap 20000)", scid, len(code), gas)
	} else {
		say("using htlc.bas at %s", scid)
	}
	bobD.SCID, aliceD.SCID = scid, scid

	// 3. Monero: one fake node both companions talk to (in lockstep, each reopening its own wallet by name), with
	//    bob's and alice's personal wallets made from fresh keys and alice funded for every path
	fw := xmrswap.NewFakeWallet(xmrswap.Stagenet)
	srv := httptest.NewServer(fw)
	defer srv.Close()
	wallet := xmrswap.NewWalletRPC(srv.URL, "", "")
	bobXmr := newXmrWallet(ctx, wallet, "bob")
	aliceXmr := newXmrWallet(ctx, wallet, "alice")
	fw.Fund(aliceXmr, uint64(len(pathList))*(*amountXmr+xmrswap.FakeFee)+1)
	fw.Mine(12)
	say("fake monero-wallet-rpc at %s: bob %s, alice %s (funded %d piconero, height %d)", srv.URL, bobXmr, aliceXmr, uint64(len(pathList))*(*amountXmr+xmrswap.FakeFee)+1, fw.Height())

	// 4. the prover, checked once end to end so a failure is reported here and not mid-swap
	var prov xmrswap.Prover
	if *fakeProver {
		prov = &tagProver{}
		say("PROVER: -fake-prover: proofs are tags, not zero-knowledge proofs; this run shows the driver, not the security")
	} else {
		prov = xmrswap.NewProverCLI(strings.Fields(*prover))
	}
	{
		tp := time.Now()
		secret, h, s, err := prov.Keygen(ctx)
		if err != nil {
			log.Fatalf("prover keygen: %v", err)
		}
		ph, ps, proof, err := prov.Prove(ctx, secret)
		if err != nil {
			log.Fatalf("prover prove: %v", err)
		}
		tProve := time.Since(tp)
		tp = time.Now()
		ok, err := prov.Verify(ctx, ph, ps, proof)
		if err != nil || !ok || ph != h || ps != s {
			log.Fatalf("prover self-test failed: ok %v err %v", ok, err)
		}
		say("PROVER self-test: keygen+prove %.1fs, verify %.2fs, proof %d bytes", tProve.Seconds(), time.Since(tp).Seconds(), len(proof))
	}

	dir := *stateDir
	if dir == "" {
		dir, err = os.MkdirTemp("", "simhtlc-")
		if err != nil {
			log.Fatal(err)
		}
	}
	say("state and channel under %s", dir)

	for _, path := range pathList {
		runPath(ctx, say, path, dir, scid, bobD, aliceD, bobAddr, aliceAddr, wallet, fw, prov)
	}
	bobAfter, _ := bobD.Balance(ctx)
	aliceAfter, _ := aliceD.Balance(ctx)
	cb, _ := bobD.ContractBalance(ctx)
	say("DERO balances: bob %d -> %d, alice %d -> %d; contract holds %d (expect 0)", bobBal, bobAfter, aliceBal, aliceAfter, cb)
	say("XMR (fake node): bob's wallet received %d piconero, alice's wallet received %d", received(ctx, wallet, "bob"), received(ctx, wallet, "alice"))
	if cb != 0 {
		log.Fatalf("the contract still holds %d", cb)
	}
}

func runPath(ctx context.Context, say func(string, ...interface{}), path, dir, scid string, bobD, aliceD *xmrswap.DeroRPC, bobAddr, aliceAddr string, wallet xmrswap.Wallet, fw *xmrswap.FakeWallet, prov xmrswap.Prover) {
	say("===== %s =====", strings.ToUpper(path))
	pdir := filepath.Join(dir, path)
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		log.Fatal(err)
	}
	logf := func(format string, a ...interface{}) {
		fmt.Printf("           %s "+format+"\n", append([]interface{}{time.Now().Format("15:04:05")}, a...)...)
	}
	cfg := func(role, addr string) xmrswap.Config {
		// GateMargin is left to its default — alice's cutoff plus two — so bob never releases a gate she will not use
		return xmrswap.Config{HTLC: scid, AmountDero: *amountDero, AmountXmr: *amountXmr, Net: xmrswap.Stagenet, MyDero: addr, MoneroWalletName: role,
			T1Min: *t1Min, T2Min: *t2Min, T1Delta: *t1, T2Delta: *t2, Confirmations: *confs, StableBlocks: *stable, ClaimMargin: *margin,
			Poll: 200 * time.Millisecond, ResendAfter: 30 * time.Second, Log: logf}
	}
	start := func(role string) *xmrswap.Swap {
		st, err := xmrswap.LoadState(filepath.Join(pdir, role+".json"), role, scid)
		if err != nil {
			log.Fatal(err)
		}
		ch, err := xmrswap.NewDirChannel(filepath.Join(pdir, "channel"), role)
		if err != nil {
			log.Fatal(err)
		}
		var sw *xmrswap.Swap
		if role == "bob" {
			sw, err = xmrswap.NewBob(cfg(role, bobAddr), st, bobD, wallet, prov, ch)
		} else {
			sw, err = xmrswap.NewAlice(cfg(role, aliceAddr), st, aliceD, wallet, prov, ch)
		}
		if err != nil {
			log.Fatal(err)
		}
		return sw
	}
	bob, alice := start("bob"), start("alice")
	deadline := time.Now().Add(*timeout)
	bobGone, bobBack := false, false
	for {
		if time.Now().After(deadline) {
			log.Fatalf("%s: no end within %v (bob %s, alice %s)", path, *timeout, bob.Phase(), alice.Phase())
		}
		h, err := bobD.Height(ctx)
		if err != nil {
			log.Printf("height: %v", err)
		}
		// the scenario: bob goes offline once his DERO is locked — until t1 (refund), or for good (punish)
		// "locked" means locked AND announced: a maker who vanishes before telling the taker the lock id harms only
		// himself, and that is not the exit under test
		ls := bob.State().LastSent
		locked := ls != nil && ls.Kind == xmrswap.KindDeroLocked && !bob.State().LastSentAt.IsZero()
		switch path {
		case "refund":
			if locked && !bobGone && !bobBack {
				bobGone = true
				say("bob goes offline after his lock (he will be back at t1 = %d to refund)", bob.State().Lock.T1)
			}
			if bobGone && h > bob.State().Lock.T1 {
				bobGone, bobBack = false, true
				say("bob is back at height %d", h)
			}
		case "punish":
			if locked && !bobGone {
				bobGone = true
				say("bob vanishes after his lock (t2 = %d); alice will punish", bob.State().Lock.T2)
			}
		}
		if !bobGone || (path == "punish" && alice.State().Done) {
			if _, err := bob.Step(ctx); err != nil {
				log.Printf("bob: %v", err)
			}
		}
		if _, err := alice.Step(ctx); err != nil {
			log.Printf("alice: %v", err)
		}
		fw.Mine(1)
		if bob.State().Done && alice.State().Done {
			break
		}
		time.Sleep(*round)
	}
	bs, as := bob.State(), alice.State()
	say("%s: bob ended %q: %s", path, bs.Phase, bs.Outcome)
	say("%s: alice ended %q: %s", path, as.Phase, as.Outcome)
	say("txids: lock %s | xmr lock %s | claim %s | refund %s | punish %s | sweep %v", or(bs.Lock.Tx), or(as.Xmr.LockTx), or(as.ClaimTx), or(bs.RefundTx), or(as.PunishTx), append(bs.Sweep.Txs, as.Sweep.Txs...))
	keys, err := bobD.ReadKeys(ctx, bs.Lock.ID)
	if err != nil {
		log.Fatalf("read back lock %d: %v", bs.Lock.ID, err)
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Printf("           %-8s = %s\n", k, keys[k])
	}
	if bs.Revealed != "" {
		say("bob read s_a from the chain and swept with s_a + s_b; joint address %s", bs.Joint)
	}
	if as.Revealed != "" {
		say("alice read s_b from the chain and recovered with s_a + s_b; joint address %s", as.Joint)
	}
	if path == "punish" {
		say("the XMR alice locked stays under the joint key %s: nobody holds s_a + s_b", as.Joint)
	}
}

func or(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// deroLog is where one party's DERO client reports a fee re-send (§4 policy, review R24).
func deroLog(role string) func(string, ...interface{}) {
	return func(format string, a ...interface{}) {
		fmt.Printf("           %s %s's dero client: "+format+"\n", append([]interface{}{time.Now().Format("15:04:05"), role}, a...)...)
	}
}

func newXmrWallet(ctx context.Context, w xmrswap.Wallet, name string) string {
	spend, err := xmrswap.NewScalar(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	view, err := xmrswap.NewScalar(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	addr, err := xmrswap.Address(spend.Public(), view.Public(), xmrswap.Stagenet)
	if err != nil {
		log.Fatal(err)
	}
	if err := w.GenerateFromKeys(ctx, name, addr, view.Hex(), spend.Hex(), 0); err != nil {
		log.Fatalf("fake wallet %s: %v", name, err)
	}
	return addr
}

func received(ctx context.Context, w xmrswap.Wallet, name string) uint64 {
	if err := w.OpenWallet(ctx, name); err != nil {
		return 0
	}
	in, err := w.Incoming(ctx, 0)
	if err != nil {
		return 0
	}
	var sum uint64
	for _, t := range in {
		sum += t.Amount
	}
	return sum
}

// tagProver is the -fake-prover: real keys, and a proof that is a tag over (h, s) rather than a proof of the
// statement. It exercises every other part of the driver; it proves nothing about the shares.
type tagProver struct{}

func tag(h string, s xmrswap.Point) []byte {
	sum := sha256.Sum256([]byte("simhtlc tag|" + h + "|" + s.Hex()))
	return sum[:]
}

func (tagProver) Keygen(ctx context.Context) (xmrswap.Scalar, string, xmrswap.Point, error) {
	s, err := xmrswap.NewScalar(rand.Reader)
	if err != nil {
		return xmrswap.Scalar{}, "", xmrswap.Point{}, err
	}
	return s, xmrswap.SHA256Hex(s[:]), s.Public(), nil
}

func (tagProver) Prove(ctx context.Context, secret xmrswap.Scalar) (string, xmrswap.Point, []byte, error) {
	h, s := xmrswap.SHA256Hex(secret[:]), secret.Public()
	return h, s, tag(h, s), nil
}

func (tagProver) Verify(ctx context.Context, h string, s xmrswap.Point, proof []byte) (bool, error) {
	return string(proof) == string(tag(h, s)), nil
}
