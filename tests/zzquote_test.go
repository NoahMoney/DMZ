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
	alice := c.NewAccount()
	treasury := c.NewAccount()
	asset, res := c.Install(assetCode, nil, nil, alice, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatal(res.Err)
	}
	base := readContract(t, "pool.bas")
	_, r1 := c.Install(base, nil, rpc.Arguments{
		harness.H("asset", asset), harness.S("treasury", treasury.String()), harness.U("fee", 30), harness.U("pfee", 2000),
	}, alice, harness.MaxStorageGas)
	if !r1.OK() {
		t.Fatal(r1.Err)
	}
	q := readContract(t, "poolq.bas")

	_, r2 := c.Install(q, nil, rpc.Arguments{
		harness.H("asset", asset), harness.H("quote", zero), harness.S("treasury", treasury.String()), harness.U("fee", 30), harness.U("pfee", 2000),
	}, alice, harness.MaxStorageGas)
	if !r2.OK() {
		t.Fatal(r2.Err)
	}
	t.Logf("pool.bas  %5d bytes  install storage gas %d of %d", len(base), r1.GasStorage, harness.MaxStorageGas)
	t.Logf("poolq.bas %5d bytes  install storage gas %d of %d  (delta %+d bytes, %+d gas)",
		len(q), r2.GasStorage, harness.MaxStorageGas, len(q)-len(base), int64(r2.GasStorage)-int64(r1.GasStorage))
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
	pool, res := c.Install(readContract(t, "poolq.bas"), nil, rpc.Arguments{
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
	pool, res := c.Install(readContract(t, "poolq.bas"), nil, rpc.Arguments{
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
