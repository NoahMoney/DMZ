// simhtlc drives whole DERO <-> Monero swaps on the local DERO simulator (docs/XMR_SWAP_BUILD.md §3.D, §5): it
// installs contracts/htlc.bas through reference.Canon, runs bob's and alice's companions in-process against the
// real daemon and wallets, and exercises all three exits — the happy path; bob refunds and alice recovers her XMR
// with s_b; bob vanishes and alice punishes. It prints every transaction id and reads every contract key back.
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
//     cd tools && go run ./simhtlc -fake-monero -prover "<proof-xmr command>"
//     cd tools && go run ./simhtlc -regtest -prover "<proof-xmr command>"     # after tools/regtest/monero-up.sh
//
// Wallets: 30006 is bob (has DERO, locks first), 30007 is alice (has XMR, claims; also pays the install). The
// driver prints both DERO balances against what the three exits need and stops if either is short; -fund N
// has alice's wallet pay bob N atomic units first, as a plain transfer.
//
// Every swap carries the maker's stipend (Path 0): once bob's lock is stable he sends alice DERO for her Claim
// and a Punish, and alice locks no XMR until that stipend is itself at stable depth. It is sized as mainnet sizes
// it (xmrswap.DefaultStipend) unless -stipend says otherwise.
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
	prover        = flag.String("prover", "", "the proof-xmr command, e.g. \"proof-xmr\", or \"wsl.exe -e <path to proof-xmr>\"")
	fakeProver    = flag.Bool("fake-prover", false, "stand-in prover whose proofs are tags, not zero-knowledge proofs: only for exercising the driver when proof-xmr is not built")
	amountDero    = flag.Uint64("amount-dero", 2000, "atomic units of the DERO-side asset bob locks per swap")
	asset         = flag.String("asset", "", "the DERO-side asset's SCID (U1); \"\" locks DERO. bob's wallet must hold it")
	assetDecimals = flag.Uint64("asset-decimals", 0, "the asset's decimals, as both companions agree them")
	amountXmr     = flag.Uint64("amount-xmr", 1_000_000_000_000, "piconero alice locks per swap (1 XMR)")
	t1            = flag.Uint64("t1", 48, "blocks from bob's lock to t1 (policy on mainnet: 400; see the package comment)")
	t2            = flag.Uint64("t2", 8, "blocks from t1 to t2 (policy on mainnet: 400)")
	t1Min         = flag.Uint64("t1-min", 10, "alice's floor on t1 - now")
	t2Min         = flag.Uint64("t2-min", 6, "alice's floor on t2 - t1")
	stable        = flag.Uint64("stable", 2, "DERO blocks alice lets the lock age before locking XMR (policy: 8)")
	margin        = flag.Uint64("claim-margin", 2, "blocks of slack under t1 − 8 after which alice sends no Claim (policy: 8)")
	gateDeadline  = flag.Uint64("gate-deadline", 4, "G: alice ignores a gate reaching her at or after t1 − G (policy: 100)")
	stipend       = flag.Uint64("stipend", xmrswap.DefaultStipend, "the DERO bob sends alice for her gas, and the least she accepts")
	closeAfter    = flag.Uint64("close-after", 3, "blocks past t2 after which bob closes an abandoned lock himself")
	confs         = flag.Uint64("confs", 10, "Monero confirmations bob waits for before the gate (policy: 20; a block is mined per round)")
	fee           = flag.Uint64("fee", 2000, "fixed fee for Refund and Punish")
	paths         = flag.String("paths", "happy,refund,punish", "which exits to run, in order")
	stateDir      = flag.String("state-dir", "", "where state files and the channel go (default: a temp dir)")
	round         = flag.Duration("round", time.Second, "pause between driver rounds")
	timeout       = flag.Duration("timeout", 40*time.Minute, "give up on one path after this long")
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
	if *fakeMonero == *regtest {
		log.Fatal("give exactly one of -fake-monero (the in-process fake) and -regtest (real monero-wallet-rpc on tools/regtest)")
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
		scid, gas, err = aliceD.Install(ctx, aliceAddr, code)
		if err != nil {
			log.Fatalf("install: %v", err)
		}
		sum := sha256.Sum256([]byte(code))
		say("INSTALL htlc.bas: scid %s, %d canonical bytes, storage gas %d (cap 20000), code sha256 %x (pinned %s)", scid, len(code), gas, sum, xmrswap.HTLCCodeSHA256)
	} else {
		say("using htlc.bas at %s", scid)
	}
	bobD.SCID, aliceD.SCID = scid, scid
	if h, err := bobD.CodeSHA256(ctx); err != nil || h != xmrswap.HTLCCodeSHA256 {
		log.Fatalf("the contract at %s hashes to %s (%v), not the pinned htlc.bas v2: the companions would refuse it", scid, h, err)
	}

	// 3. Monero
	var xm *moneroSide
	if *regtest {
		xm = regtestSide(ctx, say, len(pathList))
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
			GateDeadline: *gateDeadline, Stipend: *stipend, StipendMin: *stipend, CloseAfter: *closeAfter,
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
			sw, err = xmrswap.NewBob(cfg(role, bobAddr, xm.bobName), st, bobD, xm.bob, prov, ch)
		} else {
			sw, err = xmrswap.NewAlice(cfg(role, aliceAddr, xm.aliceName), st, aliceD, xm.alice, prov, ch)
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
		}
		if !bobGone || (path == "punish" && alice.State().Done) {
			if _, err := bob.Step(ctx); err != nil {
				log.Printf("bob: %v", err)
			}
		}
		if _, err := alice.Step(ctx); err != nil {
			log.Printf("alice: %v", err)
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
	say("%s: DERO txids: lock %s | stipend %s (alice saw it at height %d) | claim %s | refund %s | punish %s",
		path, or(bs.Lock.Tx), or(bs.Stipend.Tx), as.Stipend.Height, or(as.ClaimTx), or(bs.RefundTx), or(as.PunishTx+bs.PunishTx))
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
