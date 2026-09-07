package tests

// TEMPORARY measurement + smoke test for the quote-asset-parameter study. Delete after reading.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// install gas for the current pool and the quote-parameterised pool, side by side
func TestZZQuoteInstallGas(t *testing.T) {
	c := harness.New()
	alice, treasury := c.NewAccount(), c.NewAccount()
	var asset, zero crypto.Hash
	asset[0] = 1
	code := readContract(t, "pool.bas")
	_, r := c.Install(code, nil, rpc.Arguments{
		harness.H("asset", asset), harness.H("quote", zero), harness.S("treasury", treasury.String()), harness.U("fee", 30), harness.U("pfee", 2000),
	}, alice, harness.MaxStorageGas)
	if !r.OK() {
		t.Fatal(r.Err)
	}
	// The quote parameter cost +860 bytes and +934 gas over the DERO-only v1. This asserts the budget rather
	// than the delta, because the contract it was measured against is gone: a pool must always install.
	t.Logf("pool.bas %5d bytes  install storage gas %d of %d  (headroom %d)",
		len(code), r.GasStorage, harness.MaxStorageGas, harness.MaxStorageGas-r.GasStorage)
	if r.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("pool.bas no longer installs: %d of %d storage gas", r.GasStorage, harness.MaxStorageGas)
	}
}

// a wrapped-asset quote: full lifecycle against a non-DERO quote SCID
func TestZZQuoteNonDeroLifecycle(t *testing.T) {
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
	// no DERO anywhere in these calls
	in := func(m map[crypto.Hash]uint64) map[crypto.Hash]uint64 { return m }
	r := c.Call(pool, "AddLiquidity", rpc.Arguments{harness.U("min_lp", 0)}, in(map[crypto.Hash]uint64{wxmr: 10_000_000, base: 50_000_000}), alice)
	if !r.OK() {
		t.Fatalf("AddLiquidity: %v", r.Err)
	}
	t.Logf("AddLiquidity  compute %d  storage %d  transfers %d", r.GasCompute, r.GasStorage, len(r.Transfers))
	rd := c.U(pool, "rd")
	ra := c.U(pool, "ra")
	t.Logf("reserves rd=%d ra=%d", rd, ra)

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
	// a DERO burn must now be rejected as stray value
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

// the same contract with the zero hash as quote must behave exactly like the DERO pool
func TestZZQuoteZeroHashIsDero(t *testing.T) {
	c := harness.New()
	alice := c.NewAccount()
	treasury := c.NewAccount()
	base, res := c.Install(assetCode, nil, nil, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	if r := c.Call(base, "Mint", rpc.Arguments{harness.U("amount", 1<<62)}, nil, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	var z crypto.Hash
	pool, res := c.Install(readContract(t, "pool.bas"), nil, rpc.Arguments{
		harness.H("asset", base), harness.H("quote", z), harness.S("treasury", treasury.String()), harness.U("fee", 30), harness.U("pfee", 2000),
	}, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	r := c.Call(pool, "AddLiquidity", rpc.Arguments{harness.U("min_lp", 0)}, map[crypto.Hash]uint64{z: 10_000_000, base: 50_000_000}, alice)
	if !r.OK() {
		t.Fatalf("AddLiquidity: %v", r.Err)
	}
	r = c.Call(pool, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 1)}, map[crypto.Hash]uint64{z: 1_000_000}, alice)
	if !r.OK() {
		t.Fatalf("buy with DERO: %v", r.Err)
	}
	r = c.Call(pool, "SwapAssetForDero", rpc.Arguments{harness.U("min_out", 1), harness.U("publish", 1)}, map[crypto.Hash]uint64{base: 1_000_000}, alice)
	if !r.OK() {
		t.Fatalf("sell for DERO: %v", r.Err)
	}
	for _, tr := range r.Transfers {
		t.Logf("sell pays asset %s amount %d", tr.Asset.String(), tr.Amount)
	}
	r = c.Call(pool, "ClaimFees", nil, nil, alice)
	if !r.OK() {
		t.Fatalf("ClaimFees: %v", r.Err)
	}
	for _, tr := range r.Transfers {
		t.Logf("ClaimFees pays asset %s amount %d", tr.Asset.String(), tr.Amount)
	}
}

// stray DERO sent into a wrapper-quoted pool must be rejected, not trapped
func TestZZQuoteStrayDero(t *testing.T) {
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
