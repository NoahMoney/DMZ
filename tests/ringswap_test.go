package tests

import (
	"testing"

	"github.com/deroproject/derohe/rpc"
	ref "github.com/deroproject/derohe/veil/reference"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// P-RING: SwapDeroForAssetTo / SwapAssetForDeroTo pay a named address and never read SIGNER(), so a wallet may
// send them at any ring size (measured on the simulator: tools/ringprobe). The harness emulates ring > 2 with a
// nil signer, which the DVM turns into 33 zero bytes; the classic entrypoints must fail closed under it.
func TestLaunchSwapToNamedAddress(t *testing.T) {
	e := newLaunchEnv(t, launchParams{supply: 1000000 * 100000, grad: 20 * 100000, cbps: 500, tax: 1000, tmode: 3, fee: 100, pfee: 5000})
	c := e.c
	d := uint64(300000)
	if r := c.Call(e.scid, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 0), harness.U("publish", 0)}, e.in(d, 0), nil); r.OK() {
		t.Fatal("SwapDeroForAsset must fail closed without a signer")
	}
	e.check(t, "refused without signer", harness.Result{}, nil, nil)
	// a buy paid to bob, sent without a signer
	sends, ok := e.m.Buy(ref.In{D: d}, 0, 0, e.bob.String(), c.Height, c.Timestamp)
	r := c.Call(e.scid, "SwapDeroForAssetTo", rpc.Arguments{harness.S("dest", e.bob.String()), harness.U("min_out", 0), harness.U("publish", 1)}, e.in(d, 0), nil)
	if !ok || !r.OK() {
		t.Fatalf("SwapDeroForAssetTo: model %v chain %v %v", ok, r.OK(), r.Err)
	}
	e.check(t, "buy to bob", r, e.bob, sends)
	got := r.Sent(e.scid, e.bob)
	if got == 0 {
		t.Fatal("bob received nothing")
	}
	// a private trade never publishes, whatever the caller asked: a record would name dest as the trader and let
	// one payer inflate "distinct publishers" under any address it likes
	if c.U(e.scid, "tc") != 0 {
		t.Fatalf("private buy wrote a public trade record (tc %d)", c.U(e.scid, "tc"))
	}
	// bob sells half of it, paid to alice, again without a signer; the record names alice
	a := got / 2
	sends, ok = e.m.Sell(ref.In{A: a}, 0, 0, e.alice.String(), c.Height, c.Timestamp)
	r = c.Call(e.scid, "SwapAssetForDeroTo", rpc.Arguments{harness.S("dest", e.alice.String()), harness.U("min_out", 0), harness.U("publish", 1)}, e.in(0, a), nil)
	if !ok || !r.OK() {
		t.Fatalf("SwapAssetForDeroTo: model %v chain %v %v", ok, r.OK(), r.Err)
	}
	e.check(t, "sell to alice", r, e.alice, sends)
	if r.Sent(zero, e.alice) == 0 {
		t.Fatal("alice received no DERO")
	}
	// a destination that is not an address is refused and nothing moves
	if r := c.Call(e.scid, "SwapDeroForAssetTo", rpc.Arguments{harness.S("dest", "not an address"), harness.U("min_out", 0), harness.U("publish", 0)}, e.in(d, 0), nil); r.OK() {
		t.Fatal("bad destination accepted")
	}
	e.check(t, "bad destination", harness.Result{}, nil, nil)
	// a refused trade must not revert: above ring 2 derohe cannot refund a reverted call (no signer to credit), so
	// the wrapper commits, sends everything back to dest and marks rf_<txid>; the state is untouched
	if _, ok := e.m.Buy(ref.In{D: d}, 1<<62, 0, e.bob.String(), c.Height, c.Timestamp); ok {
		t.Fatal("model accepted an impossible min_out")
	}
	r = c.Call(e.scid, "SwapDeroForAssetTo", rpc.Arguments{harness.S("dest", e.bob.String()), harness.U("min_out", 1<<62), harness.U("publish", 0)}, e.in(d, 0), nil)
	if !r.OK() || r.Sent(zero, e.bob) != d || r.Sent(e.scid, e.bob) != 0 {
		t.Fatalf("refused private buy: ok=%v refund %d want %d", r.OK(), r.Sent(zero, e.bob), d)
	}
	e.check(t, "refused private buy refunded", harness.Result{}, nil, nil)
	r = c.Call(e.scid, "SwapAssetForDeroTo", rpc.Arguments{harness.S("dest", e.alice.String()), harness.U("min_out", 1<<62), harness.U("publish", 0)}, e.in(0, 1000), nil)
	if !r.OK() || r.Sent(e.scid, e.alice) != 1000 || r.Sent(zero, e.alice) != 0 {
		t.Fatalf("refused private sell: ok=%v refund %d want 1000", r.OK(), r.Sent(e.scid, e.alice))
	}
	e.check(t, "refused private sell refunded", harness.Result{}, nil, nil)
	// the signed entrypoints still work and agree with the model
	e.buy(t, e.alice, d, 0, 0, 1)
	e.sell(t, e.alice, 1000, 0, 0, 0)
	t.Logf("max gas %v", e.gasMax)
}

func TestPoolSwapToNamedAddress(t *testing.T) {
	e := newPoolEnv(t, 30, 5000)
	e.add(t, e.alice, 100000000, 100000000, 0, 0)
	d := uint64(500000)
	if r := e.c.Call(e.pool, "SwapDeroForAsset", rpc.Arguments{harness.U("min_out", 0), harness.U("publish", 0)}, e.in(d, 0, 0), nil); r.OK() {
		t.Fatal("pool SwapDeroForAsset must fail closed without a signer")
	}
	e.checkState(t, "refused without signer", harness.Result{}, nil, nil)
	sends, ok := e.model.SwapDeroForAsset(ref.In{D: d}, 0, 0, e.bob.String(), e.c.Height, e.c.Timestamp)
	r := e.c.Call(e.pool, "SwapDeroForAssetTo", rpc.Arguments{harness.S("dest", e.bob.String()), harness.U("min_out", 0), harness.U("publish", 1)}, e.in(d, 0, 0), nil)
	if !ok || !r.OK() {
		t.Fatalf("pool SwapDeroForAssetTo: model %v chain %v %v", ok, r.OK(), r.Err)
	}
	e.checkState(t, "pool buy to bob", r, e.bob, sends)
	got := r.Sent(e.asset, e.bob)
	if got == 0 {
		t.Fatal("bob received no asset")
	}
	if e.c.U(e.pool, "tc") != 0 {
		t.Fatalf("private pool buy wrote a public trade record (tc %d)", e.c.U(e.pool, "tc"))
	}
	a := got / 2
	sends, ok = e.model.SwapAssetForDero(ref.In{A: a}, 0, 0, e.alice.String(), e.c.Height, e.c.Timestamp)
	r = e.c.Call(e.pool, "SwapAssetForDeroTo", rpc.Arguments{harness.S("dest", e.alice.String()), harness.U("min_out", 0), harness.U("publish", 1)}, e.in(0, a, 0), nil)
	if !ok || !r.OK() {
		t.Fatalf("pool SwapAssetForDeroTo: model %v chain %v %v", ok, r.OK(), r.Err)
	}
	e.checkState(t, "pool sell to alice", r, e.alice, sends)
	if r.Sent(zero, e.alice) == 0 {
		t.Fatal("alice received no DERO")
	}
	e.buy(t, e.bob, d, 0, 0, 0, 1)
	e.sell(t, e.bob, 0, 1000, 0, 0, 0)
	// a refused private pool trade commits and refunds to dest (see the launch test for why)
	r = e.c.Call(e.pool, "SwapDeroForAssetTo", rpc.Arguments{harness.S("dest", e.bob.String()), harness.U("min_out", 1<<62), harness.U("publish", 0)}, e.in(d, 0, 0), nil)
	if !r.OK() || r.Sent(zero, e.bob) != d || r.Sent(e.asset, e.bob) != 0 {
		t.Fatalf("refused private pool buy: ok=%v refund %d want %d", r.OK(), r.Sent(zero, e.bob), d)
	}
	e.checkState(t, "refused private pool buy refunded", harness.Result{}, nil, nil)
	r = e.c.Call(e.pool, "SwapAssetForDeroTo", rpc.Arguments{harness.S("dest", e.alice.String()), harness.U("min_out", 1<<62), harness.U("publish", 0)}, e.in(0, 1000, 0), nil)
	if !r.OK() || r.Sent(e.asset, e.alice) != 1000 || r.Sent(zero, e.alice) != 0 {
		t.Fatalf("refused private pool sell: ok=%v refund %d want 1000", r.OK(), r.Sent(e.asset, e.alice))
	}
	e.checkState(t, "refused private pool sell refunded", harness.Result{}, nil, nil)
}
