package zecswap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"veil/tools/zecspv"
)

// the harness's evidence directory, beside the testnet header plumbing (research/testnet/ is the private DERO
// testnet's exercise, MT-F05)
const testnetSwapLog = "../../research/regtest/zecswap-testnet.log"

// TestTestnetSwap is MT-C15 (BL-42): the zecswap command line, as separate maker, taker and status processes, runs one
// whole swap and one refund on the ZCASH TESTNET through the harness's rpcgate, with the DERO leg on the simulator (or,
// with ZECSWAP_TESTNET_DERO_*, the private DERO testnet) and a board and escrow of its own. Skipped unless
// ZECSWAP_TESTNET_SWAP=1; the procedure is docs/ZEC_SWAP.md §11.1.
//
// It runs the policy the command line ships for -network testnet (DefaultPolicy, docs/ZEC_SWAP.md §5), so it waits
// the real depths: C_zec = 10 Zcash blocks before the claim and before either side calls its leg done, a cltv 96
// blocks out, and T_dero = 1,600 DERO blocks after the lock. Both swaps run with -verify-headers, which on testnet
// checks the plumbing only: Testnet's minimum-difficulty rule makes forged headers nearly free. Nothing is mined here:
// the taker funds from a key file the operator has filled with testnet ZEC (ZECSWAP_TESTNET_TAKER_KEY), and the
// maker's claim pays another the operator keeps (ZECSWAP_TESTNET_MAKER_KEY), both outside the repository. Every
// txid is logged in research/regtest/zecswap-testnet.log.
func TestTestnetSwap(t *testing.T) {
	if os.Getenv("ZECSWAP_TESTNET_SWAP") == "" {
		t.Skip("set ZECSWAP_TESTNET_SWAP=1, ZECSWAP_TESTNET_MAKER_KEY and ZECSWAP_TESTNET_TAKER_KEY to run the Zcash testnet swap (docs/ZEC_SWAP.md §11.1)")
	}
	makerKey, takerKey := os.Getenv("ZECSWAP_TESTNET_MAKER_KEY"), os.Getenv("ZECSWAP_TESTNET_TAKER_KEY")
	if makerKey == "" || takerKey == "" || makerKey == takerKey {
		t.Fatal("ZECSWAP_TESTNET_MAKER_KEY and ZECSWAP_TESTNET_TAKER_KEY must name two different key files (64 hex or WIF), kept outside the repository")
	}
	user, pass, err := ReadCookie(testnetCookie)
	if err != nil {
		t.Fatalf("the testnet gate's cookie: %v", err)
	}
	f, err := os.OpenFile(testnetSwapLog, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := filepath.Abs(filepath.Join("..", ".."))
	e := &e2e{t: t, ctx: context.Background(), zc: NewZcashRPC(testnetGate, user, pass), log: f, dir: t.TempDir(), repo: repo}
	defer e.close()
	cookie, err := filepath.Abs(testnetCookie)
	if err != nil {
		t.Fatal(err)
	}
	e.cookie = cookie
	p := DefaultPolicy()
	e.cn = &cliNet{network: "testnet", zcash: testnetGate, wmin: p.TakerMinDeroLeft, wantZat: p.MinAmount, interval: "10s",
		wait: 10 * time.Hour, verifyAll: true, makerKey: makerKey, takerKey: takerKey}
	// the DERO side: the simulator unless all four of the private DERO testnet's endpoints are given
	if d := os.Getenv("ZECSWAP_TESTNET_DERO_DAEMON"); d != "" {
		mw, tw, iw := os.Getenv("ZECSWAP_TESTNET_DERO_MAKER"), os.Getenv("ZECSWAP_TESTNET_DERO_TAKER"), os.Getenv("ZECSWAP_TESTNET_DERO_INSTALLER")
		if mw == "" || tw == "" || iw == "" || mw == tw || mw == iw || tw == iw {
			t.Fatal("with ZECSWAP_TESTNET_DERO_DAEMON, give three different wallets of the driver's own: ZECSWAP_TESTNET_DERO_MAKER, _TAKER and _INSTALLER")
		}
		e.cn.dero, e.cn.makerWallet, e.cn.takerWallet, e.cn.installWallet = d, mw, tw, iw
	}
	w := e.where()

	e.logf("== MT-C15: the zecswap command line on the ZCASH TESTNET through rpcgate %s; the DERO leg on %s (maker %s, taker %s, installer %s)", testnetGate, w.dero, w.makerWallet, w.takerWallet, w.installWallet)
	e.logf("== policy: DefaultPolicy (docs/ZEC_SWAP.md §5): C_zec %d, cltv %d ± %d Zcash blocks, T_dero %d DERO blocks after the lock, the taker funds with at least %d left; escrow wmin %d",
		p.ConfZec, p.CLTVWindow, p.CLTVTolerance, p.TDeroWindow, p.TakerMinDeroLeft, w.wmin)
	e.logf("== -verify-headers on both swaps. TESTNET CAVEAT: it checks the plumbing only; the minimum-difficulty rule makes testnet headers nearly free to forge, so a lie would cost almost nothing")

	// preflight, DERO: a testnet daemon (the simulator reports testnet too)
	testnet, deroNet, err := NewDeroRPC(w.dero, "", "", "").Info(e.ctx)
	if err != nil {
		t.Fatalf("the DERO daemon %s: %v", w.dero, err)
	}
	if !testnet {
		t.Fatalf("the DERO daemon %s is not on a testnet (%s)", w.dero, deroNet)
	}
	e.logf("dero: %s answers as %s", w.dero, deroNet)

	// preflight, Zcash: the testnet, synced, its tip passing the shipped header check
	ci, err := e.zc.ChainInfo(e.ctx)
	if err != nil {
		t.Fatalf("the testnet node is not usable yet (still syncing, or unreachable): %v", err)
	}
	g, err := e.zc.BlockHash(e.ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if ci.Chain != "test" || ci.Regtest || g != zecspv.Testnet.Genesis.String() {
		t.Fatalf("not the Zcash testnet: chain %q, regtest %v, genesis %s", ci.Chain, ci.Regtest, g)
	}
	hc := DefaultHeaderCheck()
	params, floor, _, err := hc.setup(Testnet)
	if err != nil {
		t.Fatal(err)
	}
	first := ci.Height - uint32(hc.Below)
	raw, _, err := GatherHeaders(e.ctx, e.zc, first, ci.Height)
	if err != nil {
		t.Fatal(err)
	}
	tip, err := zecspv.VerifyTip(params, first, raw, hc.policy(0, floor))
	if errors.Is(err, zecspv.ErrStale) {
		t.Fatalf("the testnet node is not at the tip yet (height %d): %v", ci.Height, err)
	}
	if err != nil {
		t.Fatalf("the testnet tip's headers: %v", err)
	}
	e.logf("zcash testnet: height %d, tip %s at %s, genesis %s; the shipped header check passes on the tip (plumbing only)", tip.Height, tip.Hash, tip.Time.Format(time.RFC3339), g)

	// the taker's coins: two swaps, each the price and two conventional fees (funding and a spend), with room
	tk, err := LoadKey(takerKey, Testnet)
	if err != nil {
		t.Fatalf("the taker's key: %v", err)
	}
	mk, err := LoadKey(makerKey, Testnet)
	if err != nil {
		t.Fatalf("the maker's key: %v", err)
	}
	need := 2 * (p.MinAmount + 4*MinimumFee)
	if have := e.balance(tk); have < need {
		t.Fatalf("the taker's address %s holds %d zatoshi confirmed; the run needs at least %d. Send transparent testnet ZEC to it (docs/ZEC_SWAP.md §11.1)", tk.Address(Testnet), have, need)
	}
	e.logf("zcash testnet: taker %s, maker %s", tk.Address(Testnet), mk.Address(Testnet))

	e.bin = buildCLI(t)
	e.install()
	e.cliSwap("S", false) // a whole swap: both end swapped
	e.cliSwap("R", true)  // a refund: the maker stopped after locking; the taker refunds at cltv, the maker at T_dero
	e.logf("== done: MT-C15 — one swap and one refund on the Zcash testnet, every id above")
}
