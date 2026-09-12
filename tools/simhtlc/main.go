// simhtlc drives whole DERO <-> Monero swaps on the local DERO simulator (docs/XMR_SWAP_BUILD.md §3.D, §5): it
// installs contracts/htlc.bas through reference.Canon, runs bob's and alice's companions in-process against the
// real daemon and wallets, and exercises all four exits — the happy path; bob refunds and alice recovers her XMR
// with s_b; bob vanishes and alice punishes; and (-paths reclaim, htlc.bas v3) nobody punishes, bob Reclaims from
// t2 + g2 and alice, restarted after it, recovers her XMR with the s_b his Reclaim reveals. It prints every
// transaction id, with the block each DERO one is in, and reads every contract key back.
//
// The Monero side is one of two, and the run says which:
//
//   - -fake-monero: the in-process fake monero-wallet-rpc. Evidence about DERO only.
//
//   - -regtest: two real monero-wallet-rpc, one per party, on a local monerod --regtest (tools/regtest: -monero-alice,
//     -monero-bob, -monerod). The driver refuses a monerod whose nettype is not fakechain, makes a fresh wallet on
//     each wallet-rpc, funds alice by mining to her, mines one block a round, and looks every Monero txid up on the
//     monerod. The companions name -monerod to their wallet-rpc (set_daemon) after every open and generate, as a
//     mainnet companion does, and each path prints how many of those calls the server accepted and refused.
//     Regtest addresses are mainnet-form; nothing here touches a public network.
//
// With -regtest two more paths make a real reorganisation of the Monero chain under a live swap (MT-B11): monerod's
// pop_blocks takes alice's lock and every block after it off the chain, flush_txpool keeps the lock out of the pool,
// and a longer branch is mined in their place.
//
//   - reorg-before: at 3 confirmations, below bob's depth, while he is still watching. He must never release k_b.
//   - reorg-after: once bob has released k_b, and alice then spends the lock's inputs again on the new branch
//     (docs/XMR_SWAP.md R3): she claims the DERO, and bob's s_a + s_b opens an empty address.
//
// A DERO-side reorganisation cannot be made here: the simulator is one node that mines its own chain.
//
//	cd tools && go run ./simhtlc -fake-monero -prover "<proof-xmr command>"
//	cd tools && go run ./simhtlc -regtest -prover "<proof-xmr command>"     # after tools/regtest/monero-up.sh
//
// Wallets: 30006 is bob (has DERO, locks first), 30007 is alice (has XMR, claims; also pays the install). The
// driver prints both DERO balances against what the three exits need and stops if either is short; -fund N
// has alice's wallet pay bob N atomic units first, as a plain transfer.
//
// Every swap carries the maker's stipend (Path 0): once bob's lock is stable he sends alice DERO for her Claim
// and a Punish, and alice locks no XMR until that stipend is itself at stable depth. It is sized as mainnet sizes
// it (xmrswap.DefaultStipend) unless -stipend says otherwise.
//
// htlc.bas v3 (MT-A03(a), MT-J13) takes three install arguments. Installed here, it gets -htlc-w1min, -htlc-w2min and
// -htlc-g2, small for the simulator (10, 6, 30); with -htlc, the install's own values are read and printed. Either way
// -t1 must be at least w1min + 11 (DERO mines a Lock at most 10 blocks after it is built; the companion counts 11) and
// -t2 at least w2min, and alice's
// floor on g2 (-g2-min) defaults to the install's. The punish exit ends with alice's Punish, sent at once from t2 and
// inside [t2, t2 + g2); bob sends none of his own. -t2 must also be more than 11: bob sends no Refund from his refund
// cutoff, t2 − 11 (MT-B27), and the default of 20 leaves him the heights t1 to t1 + 8 to send it in: the refund path
// brings him back just after t1, and a driver round can take several seconds when the machine is busy.
//
// The reclaim exit: bob locks, pays the stipend and goes away; alice locks her XMR and is stopped once she is waiting
// for his refund, before t2. Bob comes back at or past his refund cutoff, sends no Refund, sits out the Punish window
// [t2, t2 + g2) in which nobody punishes, Reclaims from t2 + g2 and watches it to stable depth. Alice's companion is
// restarted from its state file at t2 + g2, sends no Punish (past her punish cutoff), reads s_b from xb_<id> (st 4) and
// sweeps her XMR back from the joint wallet. The contract must end holding 0.
//
// The timelock policy is lowered for the run — the simulator mines a block every 18 s, or at once when a
// transaction is waiting — and the companions accept that only because the daemon reports itself as the
// simulator. -t1 is 48 blocks (mainnet policy: 400): the lock takes 2–7 blocks to mine and be announced, it must
// then be 8 deep before the stipend goes out, the stipend must be 8 deep before alice locks XMR, the Monero side
// mines a block per driver round, bob's gate must go out before t1 − -gate-deadline, and alice's Claim before
// t1 − 8 − -claim-margin (Invariant 9) and be mined before t1.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	ref "veil/reference"
	"veil/tools/xmrswap"
)

var (
	daemon        = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	bobWallet     = flag.String("bob", "127.0.0.1:30006", "bob's DERO wallet rpc (the maker: has DERO)")
	aliceW        = flag.String("alice", "127.0.0.1:30007", "alice's DERO wallet rpc (the taker: has XMR, claims)")
	contracts     = flag.String("contracts", "../contracts", "contracts dir")
	htlc          = flag.String("htlc", "", "an already installed htlc.bas scid (default: install one)")
	fakeMonero    = flag.Bool("fake-monero", false, "use the in-process fake monero-wallet-rpc")
	regtest       = flag.Bool("regtest", false, "use two real monero-wallet-rpc on a local monerod --regtest (tools/regtest)")
	moneroAlice   = flag.String("monero-alice", "http://127.0.0.1:28083", "-regtest: alice's monero-wallet-rpc")
	moneroBob     = flag.String("monero-bob", "http://127.0.0.1:28084", "-regtest: bob's monero-wallet-rpc")
	monerod       = flag.String("monerod", "http://127.0.0.1:28081", "-regtest: the monerod both wallet-rpc use, for mining and for looking every Monero txid up")
	stagenetMode  = flag.Bool("stagenet", false, "use two real monero-wallet-rpc on the synced STAGENET node (tools/regtest/stagenet-wallets-up.sh): named, funded wallets, real 2-minute blocks, nothing mined (MT-B19)")
	stagenetAlice = flag.String("stagenet-alice-wallet", "xmrswap-stagenet-alice", "-stagenet: alice's wallet file on her wallet-rpc (funded beforehand)")
	stagenetBob   = flag.String("stagenet-bob-wallet", "xmrswap-stagenet-bob", "-stagenet: bob's wallet file on his wallet-rpc")
	prover        = flag.String("prover", "", "the proof-xmr command, e.g. \"proof-xmr\", or \"wsl.exe -e <path to proof-xmr>\"")
	fakeProver    = flag.Bool("fake-prover", false, "stand-in prover whose proofs are tags, not zero-knowledge proofs: only for exercising the driver when proof-xmr is not built")
	amountDero    = flag.Uint64("amount-dero", 2000, "atomic units of the DERO-side asset bob locks per swap")
	asset         = flag.String("asset", "", "the DERO-side asset's SCID (U1); \"\" locks DERO. bob's wallet must hold it")
	assetDecimals = flag.Uint64("asset-decimals", 0, "the asset's decimals, as both companions agree them")
	amountXmr     = flag.Uint64("amount-xmr", 1_000_000_000_000, "piconero alice locks per swap (1 XMR)")
	t1            = flag.Uint64("t1", 48, "blocks from bob's lock to t1 (policy on mainnet: 400; see the package comment)")
	t2            = flag.Uint64("t2", 20, "blocks from t1 to t2 (policy on mainnet: 400); more than 11, since bob sends no Refund from t2 − 11 (MT-B27)")
	t1Min         = flag.Uint64("t1-min", 10, "alice's floor on t1 - now")
	t2Min         = flag.Uint64("t2-min", 6, "alice's floor on t2 - t1")
	stable        = flag.Uint64("stable", 2, "DERO blocks alice lets the lock age before locking XMR (policy: 8)")
	margin        = flag.Uint64("claim-margin", 2, "blocks of slack under t1 − 8 after which alice sends no Claim (policy: 8)")
	gateDeadline  = flag.Uint64("gate-deadline", 4, "G: alice ignores a gate reaching her at or after t1 − G (policy: 100)")
	stipend       = flag.Uint64("stipend", xmrswap.DefaultStipend, "the DERO bob sends alice for her gas, and the least she accepts")
	htlcW1min     = flag.Uint64("htlc-w1min", 10, "htlc.bas v3 install argument when the driver installs it: the least t1 − the Lock's execution height (bob's -t1 must be at least this + 11)")
	htlcW2min     = flag.Uint64("htlc-w2min", 6, "htlc.bas v3 install argument when the driver installs it: the least t2 − t1 (-t2 must be at least this)")
	htlcG2        = flag.Uint64("htlc-g2", 30, "htlc.bas v3 install argument when the driver installs it: the Punish window [t2, t2 + g2), after which bob may Reclaim (above 11 + 8 + -claim-margin)")
	g2Min         = flag.Uint64("g2-min", 0, "alice's floor on the install's g2 (default: -htlc-g2)")
	confs         = flag.Uint64("confs", 10, "Monero confirmations bob waits for before the gate (policy: 20; a block is mined per round)")
	fee           = flag.Uint64("fee", 2000, "fixed fee for Refund and Punish")
	paths         = flag.String("paths", "happy,refund,punish", "which exits to run, in order: happy, refund, punish, reclaim; with -regtest also reorg-before and reorg-after (MT-B11)")
	stateDir      = flag.String("state-dir", "", "where state files and the channel go (default: a temp dir)")
	round         = flag.Duration("round", time.Second, "pause between driver rounds")
	timeout       = flag.Duration("timeout", 60*time.Minute, "give up on one path after this long (reclaim, at the default windows, takes about 35 minutes)")
	fund          = flag.Uint64("fund", 0, "if bob is short, alice's wallet sends him this many atomic units first (a plain transfer)")
)

// moneroSide is the Monero half of a run: each party's wallet-rpc client and personal wallet, how to mine a block,
// and how to say where a Monero txid is.
type moneroSide struct {
	kind               string
	net                xmrswap.Network
	bob, alice         xmrswap.Wallet
	bobName, aliceName string
	bobAddr, aliceAddr string
	mine               func(n uint64)
	where              func(txid string) string
	monerod            string      // -regtest: the monerod the companions name to their wallet-rpc (host:port); "" names none
	sets               *daemonSets // -regtest: what the real wallet-rpc answered to those set_daemon calls
}

// daemonSets counts the companions' set_daemon calls (xmrswap pinDaemon, GAP-010) by what the real monero-wallet-rpc
// answered, so the run shows the pin exercised on the server rather than assumed.
type daemonSets struct{ ok, refused int64 }

// countingWallet is a real monero-wallet-rpc client that counts its set_daemon answers. It embeds the Wallet
// interface, so every other call goes straight through; SetDaemon is the one method added.
type countingWallet struct {
	xmrswap.Wallet
	sets *daemonSets
}

func (c countingWallet) SetDaemon(ctx context.Context, address string) error {
	ds, ok := c.Wallet.(xmrswap.DaemonSetter)
	if !ok {
		return fmt.Errorf("this monero-wallet-rpc client cannot name a monerod")
	}
	err := ds.SetDaemon(ctx, address)
	if err != nil {
		atomic.AddInt64(&c.sets.refused, 1)
	} else {
		atomic.AddInt64(&c.sets.ok, 1)
	}
	return err
}

func main() {
	flag.Parse()
	modes := 0
	for _, on := range []bool{*fakeMonero, *regtest, *stagenetMode} {
		if on {
			modes++
		}
	}
	if modes != 1 {
		log.Fatal("give exactly one of -fake-monero (the in-process fake), -regtest (real monero-wallet-rpc on tools/regtest) and -stagenet (the synced stagenet node)")
	}
	if *stagenetMode {
		stagenetDefaults()
		// before anything touches the DERO side: a node that is not stagenet, or still syncing, ends the run here
		stagenetNode()
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
	bobBal, err := bobD.Balance(ctx, "")
	if err != nil {
		log.Fatal(err)
	}
	aliceBal, err := aliceD.Balance(ctx, "")
	if err != nil {
		log.Fatal(err)
	}
	pathList := strings.Split(*paths, ",")
	for _, p := range pathList {
		switch p {
		case "happy", "refund", "punish", "reclaim":
		case "reorg-before", "reorg-after", "unlock":
			if !*regtest {
				log.Fatalf("-paths %s needs -regtest: the reorganisation is made on a real monerod", p)
			}
		default:
			log.Fatalf("-paths: unknown exit %q (happy, refund, punish, reclaim, reorg-before, reorg-after)", p)
		}
	}
	if *t2 <= xmrswap.DeroTxValidity {
		log.Fatalf("-t2 %d: bob sends no Refund from t2 − %d (MT-B27), so t2 − t1 must be more than %d or he can never refund", *t2, xmrswap.DeroTxValidity, xmrswap.DeroTxValidity)
	}
	// bob, per swap: what his companion checks before it locks (the lock when it is DERO, the stipend, the fee
	// reserve), plus the lock's own fee; alice: the install (~4,000) and nothing else of her own, since each swap's
	// stipend pays her Claim or Punish
	perSwap := *stipend + xmrswap.DefaultFeeReserve + 1000
	if *asset == "" {
		perSwap += *amountDero
	}
	needBob := uint64(len(pathList)) * perSwap
	needAlice := uint64(6000)
	say("simulator %s at height %d (18 s blocks)", network, height)
	say("bob   %s (wallet %s) has %d DERO atomic units; needs about %d", bobAddr, *bobWallet, bobBal, needBob)
	say("alice %s (wallet %s) has %d DERO atomic units; needs about %d", aliceAddr, *aliceW, aliceBal, needAlice)
	say("timelocks: t1 = lock + %d blocks, t2 = t1 + %d; alice ignores a gate from t1 − %d and sends no Claim from t1 − 8 − %d = lock + %d on", *t1, *t2, *gateDeadline, *margin, *t1-8-*margin)
	say("stipend (Path 0): %d DERO atomic units per swap, from bob to alice, before any XMR", *stipend)
	if bobBal < needBob && *fund > 0 && aliceBal >= needAlice+*fund+1000 {
		// alice's wallet tops bob up with a plain transfer (not through the contract: that is what the swap does)
		txid, err := aliceD.Pay(ctx, bobAddr, *fund, 0)
		if err != nil {
			log.Fatalf("fund bob: %v", err)
		}
		bobBal, _ = bobD.Balance(ctx, "")
		aliceBal, _ = aliceD.Balance(ctx, "")
		say("FUND bob with %d from alice: tx %s; bob now %d, alice %d", *fund, txid, bobBal, aliceBal)
	}
	if bobBal < needBob || aliceBal < needAlice {
		log.Fatalf("not enough DERO: bob %d (needs %d), alice %d (needs %d); pass -fund %d (alice pays bob) or lower -amount-dero", bobBal, needBob, aliceBal, needAlice, needBob-bobBal+1000)
	}
	if room := 2*xmrswap.DefaultStableBlocks + 6 + maxU(*gateDeadline+2, 8+*margin+2); *t1 < room {
		log.Fatalf("-t1 %d leaves no room: the lock and the stipend each take 8 blocks to become stable, and the gate and the Claim need %d more before t1", *t1, room)
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
		scid, gas, err = aliceD.Install(ctx, aliceAddr, code, xmrswap.HTLCInstallArgs(*htlcW1min, *htlcW2min, *htlcG2)...)
		if err != nil {
			log.Fatalf("install: %v", err)
		}
		sum := sha256.Sum256([]byte(code))
		say("INSTALL htlc.bas: scid %s, %d canonical bytes, storage gas %d (cap 20000), code sha256 %x (pinned %s), w1min %d w2min %d g2 %d", scid, len(code), gas, sum, xmrswap.HTLCCodeSHA256, *htlcW1min, *htlcW2min, *htlcG2)
	} else {
		say("using htlc.bas at %s", scid)
	}
	bobD.SCID, aliceD.SCID = scid, scid
	dep, err := bobD.Deployment(ctx)
	if err != nil || dep.CodeSHA256 != xmrswap.HTLCCodeSHA256 {
		log.Fatalf("the contract at %s hashes to %s (%v), not the pinned htlc.bas v3: the companions would refuse it", scid, dep.CodeSHA256, err)
	}
	if *g2Min == 0 {
		*g2Min = *htlcG2
		if *htlc != "" {
			*g2Min = dep.G2 // an install the driver did not make: alice takes its g2 as her floor, and says so
		}
	}
	say("htlc.bas v3 installed with w1min %d, w2min %d, g2 %d (Punish in [t2, t2 + %d), Reclaim from t2 + %d); alice's g2 floor %d", dep.W1Min, dep.W2Min, dep.G2, dep.G2, dep.G2, *g2Min)
	if *t1 < dep.W1Min+xmrswap.DeroTxValidity || *t2 < dep.W2Min {
		log.Fatalf("-t1 %d / -t2 %d: this install needs t1 ≥ w1min + %d = %d and t2 ≥ w2min = %d, or bob's own companion refuses to lock", *t1, *t2, xmrswap.DeroTxValidity, dep.W1Min+xmrswap.DeroTxValidity, dep.W2Min)
	}

	// 3. Monero
	var xm *moneroSide
	if *regtest {
		xm = regtestSide(ctx, say, len(pathList))
	} else if *stagenetMode {
		xm = stagenetSide(ctx, say, len(pathList))
	} else {
		var closeFn func()
		xm, closeFn = fakeSide(ctx, say, len(pathList))
		defer closeFn()
	}

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
	say("state and channel under the -state-dir")

	for _, path := range pathList {
		runPath(ctx, say, path, dir, scid, bobD, aliceD, bobAddr, aliceAddr, xm, prov)
	}
	bobAfter, _ := bobD.Balance(ctx, "")
	aliceAfter, _ := aliceD.Balance(ctx, "")
	cb, _ := bobD.ContractBalance(ctx)
	say("DERO balances: bob %d -> %d, alice %d -> %d; contract holds %d (expect 0)", bobBal, bobAfter, aliceBal, aliceAfter, cb)
	if cb != 0 {
		log.Fatalf("the contract still holds %d", cb)
	}
}

func maxU(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// fakeSide is one in-process fake node both companions talk to (in lockstep, each reopening its own wallet by name),
// with bob's and alice's personal wallets made from fresh keys and alice funded for every path.
func fakeSide(ctx context.Context, say func(string, ...interface{}), paths int) (*moneroSide, func()) {
	fw := xmrswap.NewFakeWallet(xmrswap.Stagenet)
	srv := httptest.NewServer(fw)
	wallet := xmrswap.NewWalletRPC(srv.URL, "", "")
	bob := newXmrWallet(ctx, wallet, "bob")
	alice := newXmrWallet(ctx, wallet, "alice")
	fw.Fund(alice, uint64(paths)*(*amountXmr+xmrswap.FakeFee)+1)
	fw.Mine(12)
	say("fake monero-wallet-rpc at %s: bob %s, alice %s (funded, height %d)", srv.URL, bob, alice, fw.Height())
	return &moneroSide{kind: "fake", net: xmrswap.Stagenet, bob: wallet, alice: wallet, bobName: "bob", aliceName: "alice", bobAddr: bob, aliceAddr: alice,
		mine: fw.Mine, where: func(string) string { return "on the fake node" }}, srv.Close
}

// regtestSide is two real monero-wallet-rpc, one per party, on a local monerod --regtest: a fresh wallet on each,
// alice funded by mining to her, one block mined a round, and every txid looked up on the monerod.
func regtestSide(ctx context.Context, say func(string, ...interface{}), paths int) *moneroSide {
	var info struct {
		Height  uint64 `json:"height"`
		Nettype string `json:"nettype"`
		Version string `json:"version"`
	}
	if err := jsonRPC(*monerod, "get_info", nil, &info); err != nil {
		log.Fatalf("monerod %s: %v", *monerod, err)
	}
	if info.Nettype != "fakechain" {
		log.Fatalf("monerod %s reports nettype %q: this driver runs on a regtest (fakechain) monerod only", *monerod, info.Nettype)
	}
	tag := fmt.Sprintf("xmrswap-e2e-%d", time.Now().Unix())
	mk := func(url, role string) (xmrswap.Wallet, string, string) {
		name := tag + "-" + role
		if err := jsonRPC(url, "create_wallet", map[string]interface{}{"filename": name, "password": "", "language": "English"}, nil); err != nil {
			log.Fatalf("%s's monero-wallet-rpc %s: create_wallet: %v", role, url, err)
		}
		w := xmrswap.NewWalletRPC(url, "", "")
		addr, err := w.Address(ctx)
		if err != nil {
			log.Fatalf("%s's monero-wallet-rpc %s: %v", role, url, err)
		}
		if pa, err := xmrswap.ParseAddress(addr); err != nil || pa.Net != xmrswap.Mainnet {
			log.Fatalf("%s's regtest wallet %s is not mainnet-form (%v)", role, addr, err)
		}
		return w, name, addr
	}
	bobW, bobName, bobAddr := mk(*moneroBob, "bob")
	aliceW, aliceName, aliceAddr := mk(*moneroAlice, "alice")
	mine := func(n uint64, to string) {
		if err := jsonRPC(*monerod, "generateblocks", map[string]interface{}{"amount_of_blocks": n, "wallet_address": to}, nil); err != nil {
			log.Fatalf("monerod generateblocks: %v", err)
		}
	}
	// one coinbase per path, and two spare, each its own output; then 70 blocks elsewhere, past the 60-block coinbase
	// unlock
	mine(uint64(paths)+2, aliceAddr)
	mine(70, bobAddr)
	_ = jsonRPC(*monerod, "get_info", nil, &info)
	say("regtest monerod %s (%s, nettype %s) at height %d: bob %s (%s on %s), alice %s (%s on %s); alice mined %d coinbases",
		*monerod, info.Version, info.Nettype, info.Height, bobAddr, bobName, *moneroBob, aliceAddr, aliceName, *moneroAlice, paths+2)
	// The companions name this monerod to their wallet-rpc after every open and generate, as a mainnet companion does
	// with -monerod. The wallet-rpc run in WSL2 beside the monerod, and WSL2 forwards the driver's loopback to them,
	// so the address the driver uses is the one the wallet-rpc must dial.
	host := *monerod
	if u, err := url.Parse(*monerod); err == nil && u.Host != "" {
		host = u.Host
	}
	sets := &daemonSets{}
	return &moneroSide{kind: "regtest", net: xmrswap.Mainnet, bob: countingWallet{bobW, sets}, alice: countingWallet{aliceW, sets},
		bobName: bobName, aliceName: aliceName, bobAddr: bobAddr, aliceAddr: aliceAddr,
		mine: func(n uint64) { mine(n, bobAddr) }, where: monerodWhere, monerod: host, sets: sets}
}

// stagenetFeeMargin is what the driver sets aside, per exit, for alice's Monero fee: 0.001 XMR, far above a stagenet
// transfer's fee.
const stagenetFeeMargin = 1_000_000_000

// stagenetDefaults gives -stagenet its own value for every flag not typed on the command line: the stagenet wallet-rpc
// and node, 0.05 XMR per exit, and the mainnet profile's own timing — 20 real confirmations, G 100, a stable depth and
// claim margin of 8, t1 420 and t2 400 DERO blocks — so the run rehearses the numbers a mainnet swap uses. The DERO
// side is still the simulator, whose 18 s blocks those widths are counted in.
func stagenetDefaults() {
	typed := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { typed[f.Name] = true })
	for name, v := range map[string]string{
		"monero-alice": "http://127.0.0.1:38088", "monero-bob": "http://127.0.0.1:38087", "monerod": "http://127.0.0.1:38081",
		"confs": "20", "stable": "8", "claim-margin": "8", "gate-deadline": "100", "t1": "420", "t2": "400",
		"htlc-w1min": "400", "htlc-w2min": "400", "htlc-g2": "4800",
		"amount-xmr": "50000000000", "round": "5s", "timeout": "6h",
	} {
		if !typed[name] {
			if err := flag.Set(name, v); err != nil {
				log.Fatalf("-stagenet default %s=%s: %v", name, v, err)
			}
		}
	}
}

// stagenetSide is two real monero-wallet-rpc on the harness's STAGENET node (MT-B19, tools/regtest/stagenet-wallets-up.sh):
// the public test network, where nothing is mined on demand and every block is a real one of about two minutes. The
// driver refuses a node that is not stagenet or not synchronized, opens the two parties' named wallets (made once, and
// alice's funded beforehand: tools/regtest/README.md, "The stagenet swap run"), and refuses to start unless alice can
// spend what every exit will lock.
// stagenetInfo is what -stagenet reads from the node's get_info.
type stagenetInfo struct {
	Height       uint64 `json:"height"`
	TargetHeight uint64 `json:"target_height"`
	Nettype      string `json:"nettype"`
	Synchronized bool   `json:"synchronized"`
	Version      string `json:"version"`
	Difficulty   uint64 `json:"difficulty"`
}

// stagenetNode refuses, with the reason, a node that is not stagenet or not at the tip: the run waits for the sync.
func stagenetNode() stagenetInfo {
	var info stagenetInfo
	if err := jsonRPC(*monerod, "get_info", nil, &info); err != nil {
		log.Fatalf("stagenet monerod %s: %v", *monerod, err)
	}
	if info.Nettype != "stagenet" {
		log.Fatalf("monerod %s reports nettype %q: -stagenet runs against a STAGENET node only", *monerod, info.Nettype)
	}
	if !info.Synchronized || info.Height+2 < info.TargetHeight {
		log.Fatalf("the stagenet node is not synchronized (height %d of %d): the run waits for the tip; tools/regtest/stagenet-up.sh reports the sync", info.Height, info.TargetHeight)
	}
	return info
}

func stagenetSide(ctx context.Context, say func(string, ...interface{}), paths int) *moneroSide {
	info := stagenetNode()
	open := func(url, name, role string) (xmrswap.Wallet, string) {
		w := xmrswap.NewWalletRPC(url, "", "")
		if err := w.OpenWallet(ctx, name, ""); err != nil {
			log.Fatalf("%s's stagenet wallet %s on %s: %v (tools/regtest/stagenet-wallets-up.sh --create makes it)", role, name, url, err)
		}
		if err := w.Refresh(ctx, 0); err != nil {
			log.Fatalf("refreshing %s's wallet %s: %v", role, name, err)
		}
		addr, err := w.Address(ctx)
		if err != nil {
			log.Fatalf("%s's wallet %s: %v", role, name, err)
		}
		if pa, err := xmrswap.ParseAddress(addr); err != nil || pa.Net != xmrswap.Stagenet {
			log.Fatalf("%s's wallet %s holds %s, not a stagenet address (%v)", role, name, addr, err)
		}
		return w, addr
	}
	bobW, bobAddr := open(*moneroBob, *stagenetBob, "bob")
	aliceW, aliceAddr := open(*moneroAlice, *stagenetAlice, "alice")
	var bal struct {
		Balance        uint64 `json:"balance"`
		Unlocked       uint64 `json:"unlocked_balance"`
		BlocksToUnlock uint64 `json:"blocks_to_unlock"`
	}
	if err := jsonRPC(*moneroAlice, "get_balance", map[string]interface{}{"account_index": 0}, &bal); err != nil {
		log.Fatalf("alice's balance: %v", err)
	}
	var outs struct {
		Transfers []struct {
			Amount      uint64 `json:"amount"`
			Spent       bool   `json:"spent"`
			BlockHeight uint64 `json:"block_height"`
		} `json:"transfers"`
	}
	_ = jsonRPC(*moneroAlice, "incoming_transfers", map[string]interface{}{"transfer_type": "available", "account_index": 0}, &outs)
	each := *amountXmr + stagenetFeeMargin
	large := 0
	for _, o := range outs.Transfers {
		if !o.Spent && o.Amount >= each {
			large++
		}
	}
	say("stagenet monerod %s (%s) at height %d, synchronized, difficulty %d: bob %s (%s on %s), alice %s (%s on %s)",
		*monerod, info.Version, info.Height, info.Difficulty, bobAddr, *stagenetBob, *moneroBob, aliceAddr, *stagenetAlice, *moneroAlice)
	say("alice can spend %d piconero now (%d in all; %d blocks until the rest unlocks), in %d outputs of at least %d each", bal.Unlocked, bal.Balance, bal.BlocksToUnlock, large, each)
	if need := uint64(paths) * each; bal.Unlocked < need {
		log.Fatalf("alice can spend %d piconero and %d exits of %d piconero need %d: fund her wallet first (tools/regtest/README.md)", bal.Unlocked, paths, *amountXmr, need)
	}
	if large < paths {
		say("WARNING: alice has %d outputs large enough for one exit, fewer than the %d exits: a later exit may wait up to 10 blocks (20 min) for the change of an earlier one to unlock; splitting her funds first avoids it (README)", large, paths)
	}
	host := *monerod
	if u, err := url.Parse(*monerod); err == nil && u.Host != "" {
		host = u.Host
	}
	sets := &daemonSets{}
	return &moneroSide{kind: "stagenet", net: xmrswap.Stagenet, bob: countingWallet{bobW, sets}, alice: countingWallet{aliceW, sets},
		bobName: *stagenetBob, aliceName: *stagenetAlice, bobAddr: bobAddr, aliceAddr: aliceAddr,
		mine: func(uint64) {}, where: monerodWhere, monerod: host, sets: sets}
}

// monerodWhere says where the regtest monerod has a transaction: in a block, in its pool, or nowhere.
func monerodWhere(txid string) string {
	b, _ := json.Marshal(map[string]interface{}{"txs_hashes": []string{txid}})
	resp, err := http.Post(strings.TrimSuffix(*monerod, "/")+"/get_transactions", "application/json", bytes.NewReader(b))
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	defer resp.Body.Close()
	var r struct {
		Txs []struct {
			InPool      bool   `json:"in_pool"`
			BlockHeight uint64 `json:"block_height"`
		} `json:"txs"`
		Missed []string `json:"missed_tx"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "unknown (" + err.Error() + ")"
	}
	switch {
	case len(r.Txs) == 1 && r.Txs[0].InPool:
		return "in monerod's pool"
	case len(r.Txs) == 1:
		return fmt.Sprintf("in monerod block %d", r.Txs[0].BlockHeight)
	}
	return "NOT on the regtest chain"
}

// coinbaseLock is MT-B14's taker on regtest: where her companion would transfer the XMR, the driver mines a block
// whose coinbase pays the joint address instead. A coinbase carries unlock_time = its height +
// CRYPTONOTE_MINED_MONEY_UNLOCK_WINDOW (60, monero src/cryptonote_config.h:44), so the output at the joint address
// confirms like any other and cannot be spent for 60 blocks. It is the one locked output that can reach an address on
// regtest: monero-wallet-rpc 0.18.5.1 refuses to build a transfer with a non-zero unlock_time (-50,
// wallet_rpc_server.cpp:1250). Every other call goes to her real wallet-rpc.
type coinbaseLock struct {
	xmrswap.Wallet
	say  func(string, ...interface{})
	path string
}

// SetDaemon passes set_daemon through, so the companion's -monerod pin works as on any wallet-rpc.
func (c *coinbaseLock) SetDaemon(ctx context.Context, address string) error {
	if ds, ok := c.Wallet.(xmrswap.DaemonSetter); ok {
		return ds.SetDaemon(ctx, address)
	}
	return fmt.Errorf("this monero-wallet-rpc client cannot name a monerod")
}

func (c *coinbaseLock) Transfer(ctx context.Context, to string, amount uint64) (string, error) {
	var gb struct {
		Blocks []string `json:"blocks"`
	}
	if err := jsonRPC(*monerod, "generateblocks", map[string]interface{}{"amount_of_blocks": 1, "wallet_address": to}, &gb); err != nil || len(gb.Blocks) != 1 {
		return "", fmt.Errorf("mining a coinbase to %s: %v", to, err)
	}
	var blk struct {
		MinerTxHash string `json:"miner_tx_hash"`
		BlockHeader struct {
			Height uint64 `json:"height"`
			Reward uint64 `json:"reward"`
		} `json:"block_header"`
	}
	if err := jsonRPC(*monerod, "get_block", map[string]interface{}{"hash": gb.Blocks[0]}, &blk); err != nil || blk.MinerTxHash == "" {
		return "", fmt.Errorf("the coinbase of block %s: %v", gb.Blocks[0], err)
	}
	if blk.BlockHeader.Reward < amount {
		return "", fmt.Errorf("the coinbase pays %d, less than the %d agreed: the test would say nothing about unlock_time", blk.BlockHeader.Reward, amount)
	}
	// read the coinbase's unlock_time back from the monerod rather than assume the rule
	var r struct {
		Txs []struct {
			AsJSON string `json:"as_json"`
		} `json:"txs"`
	}
	unlock := "unread"
	if err := monerodPost("/get_transactions", map[string]interface{}{"txs_hashes": []string{blk.MinerTxHash}, "decode_as_json": true}, &r); err == nil && len(r.Txs) == 1 {
		var tx struct {
			UnlockTime uint64 `json:"unlock_time"`
		}
		if json.Unmarshal([]byte(r.Txs[0].AsJSON), &tx) == nil {
			unlock = fmt.Sprintf("%d (block %d + %d)", tx.UnlockTime, blk.BlockHeader.Height, int64(tx.UnlockTime)-int64(blk.BlockHeader.Height))
		}
	}
	c.say("%s: MT-B14: instead of alice's transfer, block %d's coinbase pays the joint address %d piconero: tx %s, unlock_time %s", c.path, blk.BlockHeader.Height, blk.BlockHeader.Reward, blk.MinerTxHash, unlock)
	return blk.MinerTxHash, nil
}

// monerodPost is one call to a monerod path outside JSON-RPC (/get_transactions, /pop_blocks).
func monerodPost(path string, req, out interface{}) error {
	b, _ := json.Marshal(req)
	resp, err := http.Post(strings.TrimSuffix(*monerod, "/")+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// monerodHeight is the number of blocks in the monerod's chain; the tip is one less.
func monerodHeight() uint64 {
	var info struct {
		Height uint64 `json:"height"`
	}
	if err := jsonRPC(*monerod, "get_info", nil, &info); err != nil {
		log.Fatalf("monerod get_info: %v", err)
	}
	return info.Height
}

// monerodTx is where the monerod has txid: the block it is in, or its pool; ok is false when it has it nowhere.
func monerodTx(txid string) (height uint64, inPool, ok bool) {
	var r struct {
		Txs []struct {
			InPool      bool   `json:"in_pool"`
			BlockHeight uint64 `json:"block_height"`
		} `json:"txs"`
	}
	if err := monerodPost("/get_transactions", map[string]interface{}{"txs_hashes": []string{txid}}, &r); err != nil || len(r.Txs) != 1 {
		return 0, false, false
	}
	return r.Txs[0].BlockHeight, r.Txs[0].InPool, true
}

// keyImages are the key images txid spends, from the monerod's decoded copy of it.
func keyImages(txid string) ([]string, error) {
	var r struct {
		Txs []struct {
			AsJSON string `json:"as_json"`
		} `json:"txs"`
	}
	if err := monerodPost("/get_transactions", map[string]interface{}{"txs_hashes": []string{txid}, "decode_as_json": true}, &r); err != nil {
		return nil, err
	}
	if len(r.Txs) != 1 || r.Txs[0].AsJSON == "" {
		return nil, fmt.Errorf("the monerod has no copy of %s", txid)
	}
	var tx struct {
		Vin []struct {
			Key struct {
				KImage string `json:"k_image"`
			} `json:"key"`
		} `json:"vin"`
	}
	if err := json.Unmarshal([]byte(r.Txs[0].AsJSON), &tx); err != nil {
		return nil, err
	}
	var out []string
	for _, in := range tx.Vin {
		out = append(out, in.Key.KImage)
	}
	return out, nil
}

// blockHash is the hash of the monerod's block at height.
func blockHash(height uint64) string {
	var r struct {
		BlockHeader struct {
			Hash string `json:"hash"`
		} `json:"block_header"`
	}
	if err := jsonRPC(*monerod, "get_block_header_by_height", map[string]interface{}{"height": height}, &r); err != nil {
		log.Fatalf("monerod get_block_header_by_height %d: %v", height, err)
	}
	return r.BlockHeader.Hash
}

// walletBalance opens a party's own wallet on its wallet-rpc, refreshes it, and reads its total balance. The
// companion opens the wallet it needs by name before every use, so opening another here costs it nothing.
func walletBalance(url, name string) (uint64, error) {
	if err := jsonRPC(url, "open_wallet", map[string]interface{}{"filename": name, "password": ""}, nil); err != nil {
		return 0, err
	}
	if err := jsonRPC(url, "refresh", map[string]interface{}{}, nil); err != nil {
		return 0, err
	}
	var r struct {
		Balance uint64 `json:"balance"`
	}
	if err := jsonRPC(url, "get_balance", map[string]interface{}{"account_index": 0}, &r); err != nil {
		return 0, err
	}
	return r.Balance, nil
}

// reorg is MT-B11: a real reorganisation of the regtest chain under a live swap. pop_blocks takes the lock's block
// and every block after it off the chain (their transactions go back to the pool), flush_txpool takes alice's lock
// out of the pool so that no branch can include it again, and the blocks mined next are a different, longer branch
// at the same heights. With spendAgain, alice's own wallet then spends the lock's inputs a second time on the new
// branch, which is docs/XMR_SWAP.md R3's own case: the lock can never be mined again, by anyone.
func reorg(say func(string, ...interface{}), xm *moneroSide, path, lockTx string, spendAgain bool) {
	lh, inPool, ok := monerodTx(lockTx)
	if !ok || inPool {
		log.Fatalf("%s: alice's lock %s is not in a block: %s", path, lockTx, monerodWhere(lockTx))
	}
	kis, err := keyImages(lockTx)
	if err != nil {
		log.Fatalf("%s: the lock's key images: %v", path, err)
	}
	top := monerodHeight()
	n := top - lh
	before := blockHash(lh)
	say("%s: REORG: alice's lock %s is in block %d with %d confirmations (bob's depth: %d); it spends key images %s", path, lockTx, lh, n, *confs, strings.Join(kis, ", "))
	var pr struct {
		Height uint64 `json:"height"`
		Status string `json:"status"`
	}
	if err := monerodPost("/pop_blocks", map[string]interface{}{"nblocks": n}, &pr); err != nil || pr.Status != "OK" {
		log.Fatalf("%s: pop_blocks %d: %v (status %q)", path, n, err, pr.Status)
	}
	say("%s: REORG: pop_blocks took blocks %d..%d off the chain (height now %d); the lock is %s", path, lh, top-1, pr.Height, monerodWhere(lockTx))
	if err := jsonRPC(*monerod, "flush_txpool", map[string]interface{}{"txids": []string{lockTx}}, nil); err != nil {
		log.Fatalf("%s: flush_txpool: %v", path, err)
	}
	say("%s: REORG: flush_txpool: the lock is %s", path, monerodWhere(lockTx))
	xm.mine(n + 2)
	after := blockHash(lh)
	if after == before {
		log.Fatalf("%s: block %d is still %s: no new branch was mined", path, lh, before)
	}
	say("%s: REORG: mined %d blocks of a new branch: block %d was %s and is now %s; tip %d; the lock is %s", path, n+2, lh, before, after, monerodHeight()-1, monerodWhere(lockTx))
	if !spendAgain {
		return
	}
	if err := jsonRPC(*moneroAlice, "open_wallet", map[string]interface{}{"filename": xm.aliceName, "password": ""}, nil); err != nil {
		log.Fatalf("%s: alice's own wallet: %v", path, err)
	}
	if err := jsonRPC(*moneroAlice, "refresh", map[string]interface{}{}, nil); err != nil {
		log.Fatalf("%s: refreshing alice's own wallet: %v", path, err)
	}
	if err := jsonRPC(*moneroAlice, "rescan_spent", nil, nil); err != nil {
		say("%s: REORG: rescan_spent on alice's own wallet: %v", path, err)
	}
	var sw struct {
		TxHashList []string `json:"tx_hash_list"`
	}
	if err := jsonRPC(*moneroAlice, "sweep_all", map[string]interface{}{"address": xm.aliceAddr, "account_index": 0}, &sw); err != nil {
		log.Fatalf("%s: alice's second spend: %v", path, err)
	}
	xm.mine(1)
	lockSet := map[string]bool{}
	for _, k := range kis {
		lockSet[k] = true
	}
	var shared []string
	for _, tx := range sw.TxHashList {
		ks, err := keyImages(tx)
		if err != nil {
			say("%s: REORG: alice's second spend %s: %v", path, tx, err)
			continue
		}
		for _, k := range ks {
			if lockSet[k] {
				shared = append(shared, k)
			}
		}
		say("%s: REORG: alice's second spend %s, to her own address: %s", path, tx, monerodWhere(tx))
	}
	if len(shared) == 0 {
		say("%s: REORG: none of the second spend's key images is one the lock spent: only the flush keeps the lock off the chain", path)
		return
	}
	say("%s: REORG: the second spend uses %d of the lock's %d key images (%s): the lock can never be mined again", path, len(shared), len(kis), strings.Join(shared, ", "))
}

// jsonRPC is one JSON-RPC 2.0 call to a monerod or monero-wallet-rpc, for the driver's own setup.
func jsonRPC(base, method string, params, out interface{}) error {
	body := map[string]interface{}{"jsonrpc": "2.0", "id": "0", "method": method}
	if params != nil {
		body["params"] = params
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(strings.TrimSuffix(strings.TrimSuffix(base, "/"), "/json_rpc")+"/json_rpc", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	if env.Error != nil {
		return fmt.Errorf("%s: %d %s", method, env.Error.Code, env.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

func runPath(ctx context.Context, say func(string, ...interface{}), path, dir, scid string, bobD, aliceD *xmrswap.DeroRPC, bobAddr, aliceAddr string, xm *moneroSide, prov xmrswap.Prover) {
	say("===== %s =====", strings.ToUpper(path))
	pdir := filepath.Join(dir, path)
	if err := os.MkdirAll(pdir, 0o700); err != nil {
		log.Fatal(err)
	}
	logf := func(format string, a ...interface{}) {
		fmt.Printf("           %s "+format+"\n", append([]interface{}{time.Now().Format("15:04:05")}, a...)...)
	}
	cfg := func(role, addr, walletName string) xmrswap.Config {
		// GateMargin is left to its default — at least G + 2, and alice's cutoff plus two — so bob never releases a
		// gate she will not use
		return xmrswap.Config{HTLC: scid, AmountDero: *amountDero, AmountXmr: *amountXmr, Asset: *asset, AssetDecimals: *assetDecimals,
			Net: xm.net, DeroNet: "simulator", MyDero: addr, MoneroWalletName: walletName, Monerod: xm.monerod,
			T1Min: *t1Min, T2Min: *t2Min, T1Delta: *t1, T2Delta: *t2, Confirmations: *confs, StableBlocks: *stable, ClaimMargin: *margin,
			GateDeadline: *gateDeadline, Stipend: *stipend, StipendMin: *stipend, G2Min: *g2Min,
			Poll: 200 * time.Millisecond, ResendAfter: 30 * time.Second, Log: logf}
	}
	// GAP-019: the companions' channel is sealed under a key made for this swap alone, as the command line's is
	keyHex, err := xmrswap.NewChannelKey()
	if err != nil {
		log.Fatal(err)
	}
	chanKey, _ := xmrswap.ParseChannelKey(keyHex)
	say("%s: the channel is sealed (AES-256-GCM) under a fresh swap key (GAP-019)", path)
	aliceWallet := xm.alice
	if path == "unlock" {
		aliceWallet = &coinbaseLock{Wallet: xm.alice, say: say, path: path}
	}
	start := func(role string) *xmrswap.Swap {
		st, err := xmrswap.LoadState(filepath.Join(pdir, role+".json"), role, scid)
		if err != nil {
			log.Fatal(err)
		}
		dir, err := xmrswap.NewDirChannel(filepath.Join(pdir, "channel"), role)
		if err != nil {
			log.Fatal(err)
		}
		ch, err := xmrswap.NewSealedChannel(dir, chanKey, role, map[string]string{"bob": "alice", "alice": "bob"}[role])
		if err != nil {
			log.Fatal(err)
		}
		ch.OnDrop = func(reason string) { logf("%s: channel: %s", role, reason) }
		var sw *xmrswap.Swap
		if role == "bob" {
			sw, err = xmrswap.NewBob(cfg(role, bobAddr, xm.bobName), st, bobD, xm.bob, prov, ch)
		} else {
			sw, err = xmrswap.NewAlice(cfg(role, aliceAddr, xm.aliceName), st, aliceD, aliceWallet, prov, ch)
		}
		if err != nil {
			log.Fatal(err)
		}
		return sw
	}
	bob, alice := start("bob"), start("alice")
	deadline := time.Now().Add(*timeout)
	bobGone, bobBack := false, false
	aliceGone, aliceBack := false, false // reclaim: alice stopped through the Punish window, then restarted
	reorged := false
	var aliceXmrBefore uint64
	if strings.HasPrefix(path, "reorg") || path == "unlock" {
		b, err := walletBalance(*moneroAlice, xm.aliceName)
		if err != nil {
			log.Fatalf("%s: alice's own Monero wallet: %v", path, err)
		}
		aliceXmrBefore = b
		say("%s: alice's own Monero wallet holds %d piconero before the swap", path, b)
	}
	for {
		if time.Now().After(deadline) {
			log.Fatalf("%s: no end within %v (bob %s, alice %s)", path, *timeout, bob.Phase(), alice.Phase())
		}
		h, err := bobD.Height(ctx)
		if err != nil {
			log.Printf("height: %v", err)
		}
		// the scenario: bob goes offline once his DERO is locked and the stipend is paid — until t1 (refund), or for
		// good (punish). A maker who vanishes before paying the stipend leaves a taker who never locks XMR, and his
		// DERO waiting for his own refund: that is not the exit under test.
		bs := bob.State()
		paid := bs.Stipend.Tx != "" && bs.Phase != "stipend_sending"
		switch path {
		case "refund":
			if paid && !bobGone && !bobBack {
				bobGone = true
				say("bob goes offline after his lock and the stipend (he will be back at t1 = %d to refund)", bs.Lock.T1)
			}
			if bobGone && h > bs.Lock.T1 {
				bobGone, bobBack = false, true
				say("bob is back at height %d", h)
			}
		case "punish":
			if paid && !bobGone {
				bobGone = true
				say("bob vanishes after his lock and the stipend (t2 = %d); alice will punish", bs.Lock.T2)
			}
		case "reclaim":
			// htlc.bas v3's fourth exit. bob goes offline once his lock is in and the stipend paid, and comes back at or
			// past his refund cutoff (t2 − 11, MT-B27), too late to refund; alice is stopped once she waits for his refund,
			// before t2, so that nobody punishes in [t2, t2 + g2); bob Reclaims from t2 + g2, and alice's companion is
			// restarted from its state file at t2 + g2
			rcut, reclaimAt := bs.Lock.T2-xmrswap.DeroTxValidity, bs.Lock.T2+bs.Contract.G2
			if paid && !bobGone && !bobBack {
				bobGone = true
				say("reclaim: bob goes offline after his lock and the stipend; he will be back at his refund cutoff %d (t2 %d − %d), too late to refund, and Reclaims from t2 + g2 = %d", rcut, bs.Lock.T2, xmrswap.DeroTxValidity, reclaimAt)
			}
			if bobGone && h >= rcut {
				bobGone, bobBack = false, true
				say("reclaim: bob is back at height %d, at or past his refund cutoff %d and before t2 %d: his companion sends no Refund", h, rcut, bs.Lock.T2)
			}
			if as := alice.State(); !aliceGone && !aliceBack && as.Phase == "await_refund" {
				if as.Xmr.LockTx == "" || h >= bs.Lock.T2 {
					log.Fatalf("reclaim: alice reached await_refund at height %d with Monero lock %q (t2 %d): the path needs her XMR locked and her stopped before t2", h, as.Xmr.LockTx, bs.Lock.T2)
				}
				aliceGone = true
				say("reclaim: alice's companion is stopped at height %d in %s, her XMR locked (%s); nobody will punish in [t2 %d, t2 + g2 %d)", h, as.Phase, as.Xmr.LockTx, bs.Lock.T2, reclaimAt)
			}
			if aliceGone && h >= reclaimAt {
				aliceGone, aliceBack = false, true
				alice = start("alice")
				say("reclaim: alice's companion restarted from its state file at height %d, at or past t2 + g2 = %d (phase %q)", h, reclaimAt, alice.Phase())
			}
		case "reorg-before":
			// (a): alice's lock goes while bob is still counting it, below his depth
			if lock := alice.State().Xmr.LockTx; !reorged && bs.Phase == "watching_xmr" && lock != "" {
				if lh, inPool, ok := monerodTx(lock); ok && !inPool {
					if c := monerodHeight() - lh; c >= 3 && c < *confs {
						reorg(say, xm, path, lock, false)
						reorged = true
					}
				}
			}
		case "reorg-after":
			// (b): it goes after bob has released k_b, and alice spends its inputs again so it can never come back
			if lock := alice.State().Xmr.LockTx; !reorged && bs.Phase == "gate_sent" && lock != "" {
				reorg(say, xm, path, lock, true)
				reorged = true
			}
		}
		if !bobGone || (path == "punish" && alice.State().Done) {
			if _, err := bob.Step(ctx); err != nil {
				log.Printf("bob: %v", err)
			}
		}
		if !aliceGone {
			if _, err := alice.Step(ctx); err != nil {
				log.Printf("alice: %v", err)
			}
		}
		xm.mine(1)
		if bob.State().Done && alice.State().Done {
			break
		}
		time.Sleep(*round)
	}
	bs, as := bob.State(), alice.State()
	say("%s: bob ended %q: %s", path, bs.Phase, bs.Outcome)
	say("%s: alice ended %q: %s", path, as.Phase, as.Outcome)
	say("%s: DERO txids: lock %s | stipend %s (alice saw it at height %d) | claim %s | refund %s | punish %s | reclaim %s",
		path, or(bs.Lock.Tx), or(bs.Stipend.Tx), as.Stipend.Height, or(as.ClaimTx), or(bs.RefundTx), or(as.PunishTx+bs.PunishTx), or(bs.ReclaimTx))
	for _, tx := range []struct{ what, id string }{{"lock", bs.Lock.Tx}, {"stipend", bs.Stipend.Tx}, {"claim", as.ClaimTx}, {"refund", bs.RefundTx}, {"punish", as.PunishTx}, {"reclaim", bs.ReclaimTx}} {
		if w := deroWhere(tx.id); w != "" {
			say("%s: DERO %s %s: %s", path, tx.what, tx.id, w)
		}
	}
	if bs.ReclaimTx != "" {
		say("%s: bob's Reclaim was read executed at height %d (t2 %d + g2 %d = %d); his companion counted it once the stable height reached that", path, bs.Lock.ReclaimSeenHeight, bs.Lock.T2, bs.Contract.G2, bs.Lock.T2+bs.Contract.G2)
	}
	if as.Xmr.LockTx != "" {
		say("%s: Monero (%s) lock %s to the joint address %s: %s", path, xm.kind, as.Xmr.LockTx, as.Joint, xm.where(as.Xmr.LockTx))
	}
	for _, tx := range bs.Sweep.Txs {
		say("%s: Monero (%s) bob's sweep %s to %s: %s", path, xm.kind, tx, bs.MyXmr, xm.where(tx))
	}
	for _, tx := range as.Sweep.Txs {
		say("%s: Monero (%s) alice's recovery sweep %s to %s: %s", path, xm.kind, tx, as.MyXmr, xm.where(tx))
	}
	if xm.sets != nil {
		say("%s: set_daemon %s, sent to the real monero-wallet-rpc after every open and generate: %d accepted, %d refused so far",
			path, xm.monerod, atomic.LoadInt64(&xm.sets.ok), atomic.LoadInt64(&xm.sets.refused))
	}
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
	if cb, err := bobD.ContractBalance(ctx); err == nil {
		say("%s: the contract holds %d DERO atomic units after this exit", path, cb)
		if cb != 0 && *htlc == "" {
			log.Fatalf("%s: the contract this run installed still holds %d after the exit", path, cb)
		}
	}
	// fail closed: each of the four exits must have left its own state on the chain, and nothing else
	if want, ok := map[string]string{"happy": "1", "refund": "2", "punish": "3", "reclaim": "4"}[path]; ok {
		if got := fmt.Sprint(keys[fmt.Sprintf("st_%d", bs.Lock.ID)]); got != want {
			log.Fatalf("%s: lock %d ended in st %s, not %s: the exit did not happen (bob %s: %s; alice %s: %s)", path, bs.Lock.ID, got, want, bs.Phase, bs.Outcome, as.Phase, as.Outcome)
		}
	}
	if path == "reclaim" {
		xb := fmt.Sprint(keys[fmt.Sprintf("xb_%d", bs.Lock.ID)])
		raw, _ := hex.DecodeString(xb)
		hb := fmt.Sprint(keys[fmt.Sprintf("hb_%d", bs.Lock.ID)])
		if len(raw) != 32 || xmrswap.SHA256Hex(raw) != hb {
			log.Fatalf("reclaim: xb_%d = %q is not the preimage of hb_%d = %s", bs.Lock.ID, xb, bs.Lock.ID, hb)
		}
		if bs.Phase != "reclaimed" || bs.RefundTx != "" || as.PunishTx != "" || as.Phase != "swept" || as.Revealed == "" || len(as.Sweep.Txs) == 0 {
			log.Fatalf("reclaim: bob %s (%s), refund %q; alice %s (%s), punish %q, sweeps %v", bs.Phase, bs.Outcome, bs.RefundTx, as.Phase, as.Outcome, as.PunishTx, as.Sweep.Txs)
		}
		for _, tx := range as.Sweep.Txs {
			if w := xm.where(tx); xm.kind == "regtest" && !strings.HasPrefix(w, "in monerod block") {
				log.Fatalf("reclaim: alice's recovery sweep %s is %s", tx, w)
			}
		}
		say("reclaim: st_%d = 4 and sha256(xb_%d) = hb_%d: bob's Reclaim put s_b on the chain, and alice, restarted after t2 + g2, read it there and swept her XMR back; nobody sent a Refund or a Punish", bs.Lock.ID, bs.Lock.ID, bs.Lock.ID)
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
	if strings.HasPrefix(path, "reorg") || path == "unlock" {
		if strings.HasPrefix(path, "reorg") && !reorged {
			log.Fatalf("%s: the swap ended before the reorganisation was made (bob %s, alice %s)", path, bs.Phase, as.Phase)
		}
		say("%s: alice received k_b: %v; bob's outcome: %s; alice's lock %s is %s", path, as.Gate != "", bs.Outcome, as.Xmr.LockTx, monerodWhere(as.Xmr.LockTx))
		if b, err := walletBalance(*moneroAlice, xm.aliceName); err == nil {
			say("%s: alice's own Monero wallet: %d piconero before the swap, %d after (%+d)", path, aliceXmrBefore, b, int64(b)-int64(aliceXmrBefore))
		} else {
			say("%s: alice's own Monero wallet could not be read: %v", path, err)
		}
	}
}

func or(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// deroWhere is where the DERO simulator has a transaction: the topoheight of the block it is in (derod's
// DERO.GetTransaction block_height; on the simulator, one block a height, it is the height), its pool, or nowhere; ""
// for no txid, or one the companion could not record.
func deroWhere(txid string) string {
	if len(txid) != 64 {
		return ""
	}
	var r struct {
		Txs []struct {
			BlockHeight int64  `json:"block_height"`
			InPool      bool   `json:"in_pool"`
			ValidBlock  string `json:"valid_block"`
		} `json:"txs"`
	}
	if err := jsonRPC("http://"+strings.TrimPrefix(*daemon, "http://"), "DERO.GetTransaction", map[string]interface{}{"txs_hashes": []string{txid}}, &r); err != nil {
		return "unknown (" + err.Error() + ")"
	}
	switch {
	case len(r.Txs) != 1:
		return "NOT on the simulator's chain"
	case r.Txs[0].InPool:
		return "in the simulator's pool"
	case r.Txs[0].ValidBlock != "":
		return fmt.Sprintf("in the DERO block at topoheight %d", r.Txs[0].BlockHeight)
	}
	return "NOT in a block of the simulator's chain"
}

// deroLog is where one party's DERO client reports a Claim's estimate and a fee re-send (§4 policy, review R24).
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
	if err := w.GenerateFromKeys(ctx, name, addr, view.Hex(), spend.Hex(), "", 0); err != nil {
		log.Fatalf("fake wallet %s: %v", name, err)
	}
	return addr
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
