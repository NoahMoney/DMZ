package tests

// Pools quoted in a wrapped asset. pool_test.go installs every pool with the zero hash (DERO) as its quote, so
// the wXMR-quoted path — every buy, sell, fee and refund in an asset that is not DERO, and stray DERO refused —
// is covered here and nowhere else. These belong in pool_test.go beside poolEnv, and the install-gas bound at
// the end belongs in launch_test.go; they are kept in this file only because it is the one this pass may edit.

import (
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// a wrapped-asset quote: full lifecycle against a non-DERO quote SCID, with no DERO in any call
func TestPoolNonDeroQuoteLifecycle(t *testing.T) {
	c := harness.New()
	alice := c.NewAccount()
	treasury := c.NewAccount()
	base, res := c.Install(assetCode, nil, nil, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	wxmr, res := c.Install(assetCode, nil, nil, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	for _, s := range []crypto.Hash{base, wxmr} {
		if r := c.Call(s, "Mint", rpc.Arguments{harness.U("amount", 1<<62)}, nil, alice); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	pool, res := c.Install(readContract(t, "pool.bas"), nil, rpc.Arguments{
		harness.H("asset", base), harness.H("quote", wxmr), harness.S("treasury", treasury.String()), harness.U("fee", 30), harness.U("pfee", 2000),
	}, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	r := c.Call(pool, "AddLiquidity", rpc.Arguments{harness.U("min_lp", 0)}, map[crypto.Hash]uint64{wxmr: 10_000_000, base: 50_000_000}, alice)
	if !r.OK() {
		t.Fatalf("AddLiquidity: %v", r.Err)
	}
	t.Logf("AddLiquidity  compute %d  storage %d  transfers %d", r.GasCompute, r.GasStorage, len(r.Transfers))
	t.Logf("reserves rd=%d ra=%d", c.U(pool, "rd"), c.U(pool, "ra"))

	r = c.Call(pool, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 1)}, map[crypto.Hash]uint64{wxmr: 1_000_000}, alice)
	if !r.OK() {
		t.Fatalf("buy: %v", r.Err)
	}
	for _, tr := range r.Transfers {
		t.Logf("buy pays asset %s amount %d", tr.Asset.String(), tr.Amount)
	}
	r = c.Call(pool, "SwapAssetForDero", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 1)}, map[crypto.Hash]uint64{base: 1_000_000}, alice)
	if !r.OK() {
		t.Fatalf("sell: %v", r.Err)
	}
	for _, tr := range r.Transfers {
		t.Logf("sell pays asset %s amount %d", tr.Asset.String(), tr.Amount)
	}
	// a DERO burn must be rejected as stray value
	if r = c.Call(pool, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 0)}, map[crypto.Hash]uint64{zero: 1_000_000}, alice); r.OK() {
		t.Fatalf("a DERO burn was accepted by a wXMR-quoted pool")
	}
	r = c.Call(pool, "ClaimFees", nil, nil, alice)
	if !r.OK() {
		t.Fatalf("ClaimFees: %v", r.Err)
	}
	for _, tr := range r.Transfers {
		t.Logf("ClaimFees pays asset %s amount %d", tr.Asset.String(), tr.Amount)
	}
	r = c.Call(pool, "RemoveLiquidity", rpc.Arguments{harness.U("min_dero", 0), harness.U("min_asset", 0)}, map[crypto.Hash]uint64{pool: 1_000_000}, alice)
	if !r.OK() {
		t.Fatalf("RemoveLiquidity: %v", r.Err)
	}
	for _, tr := range r.Transfers {
		t.Logf("RemoveLiquidity pays asset %s amount %d", tr.Asset.String(), tr.Amount)
	}
}

// stray DERO sent into a wrapper-quoted pool must be rejected, not trapped
func TestPoolNonDeroQuoteRefusesStrayDero(t *testing.T) {
	c := harness.New()
	alice := c.NewAccount()
	treasury := c.NewAccount()
	base, _ := c.Install(assetCode, nil, nil, alice, harness.MaxStorageGas)
	wxmr, _ := c.Install(assetCode, nil, nil, alice, harness.MaxStorageGas)
	for _, s := range []crypto.Hash{base, wxmr} {
		if r := c.Call(s, "Mint", rpc.Arguments{harness.U("amount", 1<<62)}, nil, alice); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	pool, res := c.Install(readContract(t, "pool.bas"), nil, rpc.Arguments{
		harness.H("asset", base), harness.H("quote", wxmr), harness.S("treasury", treasury.String()), harness.U("fee", 30), harness.U("pfee", 2000),
	}, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	if r := c.Call(pool, "AddLiquidity", rpc.Arguments{harness.U("min_lp", 0)}, map[crypto.Hash]uint64{wxmr: 10000000, base: 50000000}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	// buy with the right quote plus a stray DERO burn
	if r := c.Call(pool, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 0)}, map[crypto.Hash]uint64{wxmr: 1000000, zero: 777}, alice); r.OK() {
		t.Fatalf("stray DERO was accepted and would be trapped in the pool")
	}
	if b := c.SCBalance(pool, zero); b != 0 {
		t.Fatalf("pool holds %d trapped DERO", b)
	}
	// the To-variant must hand the stray DERO back rather than swallow it
	bob := c.NewAccount()
	r := c.Call(pool, "SwapDeroForAssetTo", rpc.Arguments{harness.S("dest", bob.String()), harness.U("min_out", 1), harness.U("publish", 0)}, map[crypto.Hash]uint64{wxmr: 1000000, zero: 777}, alice)
	if !r.OK() {
		t.Fatalf("To-variant: %v", r.Err)
	}
	if got := r.Sent(zero, bob); got != 777 {
		t.Fatalf("stray DERO refunded %d, want 777", got)
	}
	if got := r.Sent(wxmr, bob); got != 1000000 {
		t.Fatalf("quote refunded %d, want 1000000", got)
	}
	t.Logf("stray DERO refunded %d, quote refunded %d", r.Sent(zero, bob), r.Sent(wxmr, bob))
}

// Worst-case install gas for launch.bas: every metadata field at the length the contract allows. launch_test.go
// installs with short strings, and the cap is per transaction, so it is the longest install that must fit.
func TestLaunchMaxMetadataInstallGas(t *testing.T) {
	code := readContract(t, "launch.bas")
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	args := rpc.Arguments{
		harness.S("name", strings.Repeat("N", 32)),
		harness.S("ticker", strings.Repeat("T", 10)),
		harness.S("descr", strings.Repeat("D", 240)),
		harness.S("icon", strings.Repeat("I", 200)),
		harness.S("tags", strings.Repeat("G", 100)),
		harness.H("quote", zeroHash),
		harness.U("dec", 5), harness.U("supply", 3689348814741910324), harness.U("grad", 1000000000000),
		harness.U("cbps", 500), harness.U("tax", 1000), harness.U("tmode", 3),
		harness.S("treasury", treasury.String()), harness.U("fee", 100), harness.U("pfee", 10000),
	}
	_, res := c.Install(code, nil, args, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("launch.bas with maximum metadata no longer installs: %d of %d storage gas", res.GasStorage, harness.MaxStorageGas)
	}
	t.Logf("launch.bas MAX-METADATA install: code %d bytes, storage gas %d of %d, headroom %d, compute %d",
		len(code), res.GasStorage, harness.MaxStorageGas, harness.MaxStorageGas-res.GasStorage, res.GasCompute)
}
