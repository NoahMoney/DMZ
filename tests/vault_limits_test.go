package tests

import (
	"strconv"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// The outflow limits, and the property that matters more than any of them: every limit either empties by itself
// or expires into a payment. Nothing here needs a human to unblock it, because a deployed vault has no operator.
func TestVaultLimits(t *testing.T) {
	c := harness.New()
	owner, treasury, user, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	limits := func(scid crypto.Hash, who *rpc.Address, winmax, wdelay uint64) harness.Result {
		return c.Call(scid, "SetLimits", rpc.Arguments{harness.U("winmax", winmax), harness.U("wdelay", wdelay)}, nil, who)
	}
	// the limits must be set before the signers are added, so install by hand rather than through the helper
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()),
		harness.U("cap", 100000), harness.U("minbond", 40000), harness.U("minburn", 100), harness.U("wfee", 10),
		harness.U("mindep", 1), harness.U("regfee", 0), harness.U("capratio", 3),
		harness.U("bigbps", 2000), harness.U("bigdelay", 500)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	if limits(scid, stranger, 1, 1).OK() {
		t.Fatal("a stranger set the limits")
	}
	if limits(scid, owner, 1, 200000).OK() {
		t.Fatal("an absurd withdrawal delay was accepted")
	}
	if r := limits(scid, owner, 1200, 5); !r.OK() {
		t.Fatalf("set limits: %v", r.Err)
	}
	sg := make([]*rpc.Address, 3)
	for i := range sg {
		sg[i] = c.NewAccount()
		if r := c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", sg[i].String())}, nil, owner); !r.OK() {
			t.Fatalf("add signer: %v", r.Err)
		}
		if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 40000}, sg[i]); !r.OK() {
			t.Fatalf("bond: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() {
		t.Fatalf("seal: %v", r.Err)
	}
	if limits(scid, owner, 9, 9).OK() {
		t.Fatal("limits changed after Seal")
	}
	if c.U(scid, "winmax") != 1200 || c.U(scid, "wdelay") != 5 {
		t.Fatal("limits not stored")
	}
	if r := c.Call(scid, "Register", nil, nil, user); !r.OK() {
		t.Fatalf("register: %v", r.Err)
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i])
	}
	mint := func(n int, amount uint64) bool {
		for i := 0; i < 2; i++ {
			c.Call(scid, "Mint", mintArgs(0, n, 0, amount), nil, sg[i])
		}
		return c.Exists(scid, "mint_"+depID(n, 0))
	}
	// The rolling window is the only mint limit: 1200 units over this 2000-block bucket and the previous one. It
	// refuses to START a mint once the window is full; it does not require the deposit to fit in what is left.
	// The difference is the whole point: a deposit larger than winmax could otherwise never be minted in any
	// bucket, ever, which is a permanent loss with no operator to send the coins back.
	if !mint(2, 500) || !mint(3, 500) {
		t.Fatal("deposits within the window were refused")
	}
	if !mint(4, 300) {
		t.Fatal("a deposit was refused although the window was not yet full: the window may overshoot by one")
	}
	if c.U(scid, "sup") != 1300 {
		t.Fatalf("sup %d, want 1300: 500 + 500 + 300, the last one overshooting the 1200 window", c.U(scid, "sup"))
	}
	if mint(5, 200) {
		t.Fatal("the window was full at 1300 of 1200 and still admitted another mint")
	}
	// A deposit the window turned away is not lost: the window empties by itself and the same deposit mints
	// later, with nobody intervening. 2000 blocks on, the previous bucket still holds 1300; 4000 on it is clear.
	for i := 0; i < 2000; i++ {
		c.NextBlock()
	}
	if mint(5, 200) {
		t.Fatal("the window did not slide: a bucket boundary alone opened a new window")
	}
	for i := 0; i < 2000; i++ {
		c.NextBlock()
	}
	if !mint(5, 200) {
		t.Fatal("the deposit the window turned away never became mintable: that would be a permanent loss")
	}
	// And the case that motivated the rule: a single deposit larger than the whole window still mints. Under the
	// old check (wm + pm + amount > winmax) this was refused in every bucket for ever.
	for i := 0; i < 4000; i++ {
		c.NextBlock()
	}
	if !mint(6, 1400) {
		t.Fatal("a deposit larger than winmax was refused: that is the per-deposit maximum returning by another door")
	}
	if c.U(scid, "sup") != 2900 {
		t.Fatalf("sup after the window cleared: %d, want 2900", c.U(scid, "sup"))
	}

	// withdrawal delay: an ordinary withdrawal waits wdelay blocks, and a LARGE one (over bigbps of the supply)
	// waits wdelay + bigdelay. Both then pay: a size limit that refused instead of delaying would need somebody
	// with the authority to release it, and after Seal nobody has any authority at all.
	burn := func(v uint64) uint64 {
		j := c.U(scid, "wn")
		if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationAddr"), harness.S("refund", user.String())}, map[crypto.Hash]uint64{scid: v}, user); !r.OK() {
			t.Fatalf("burn %d: %v", v, r.Err)
		}
		return j
	}
	small := burn(200) // 200 of the 2900 supply is well under 20 %
	if got, want := c.U(scid, "w_"+strconv.FormatUint(small, 10)+"_rel"), uint64(c.Height)+5; got != want {
		t.Fatalf("small withdrawal releasable at %d, want %d (wdelay only)", got, want)
	}
	big := burn(700) // 700 is over 20 % of the 2900 supply (580)
	if got, want := c.U(scid, "w_"+strconv.FormatUint(big, 10)+"_rel"), uint64(c.Height)+505; got != want {
		t.Fatalf("large withdrawal releasable at %d, want %d (wdelay + bigdelay)", got, want)
	}
	rel := func(j uint64, who *rpc.Address, ftx string) harness.Result {
		return c.Call(scid, "Released", rpc.Arguments{harness.U("j", j), harness.S("ftx", ftx)}, nil, who)
	}
	if rel(small, sg[0], "ftx-small").OK() {
		t.Fatal("a release vote counted before the withdrawal delay")
	}
	for i := 0; i < 5; i++ {
		c.NextBlock()
	}
	for i := 0; i < 2; i++ {
		if r := rel(small, sg[i], "ftx-small"); !r.OK() {
			t.Fatalf("release after the delay refused: %v", r.Err)
		}
	}
	if c.U(scid, "w_"+strconv.FormatUint(small, 10)+"_st") != 1 {
		t.Fatal("the small withdrawal did not release")
	}
	// the large one is still waiting, and nothing but time is needed
	if rel(big, sg[0], "ftx-big").OK() {
		t.Fatal("the large withdrawal released before its longer delay")
	}
	for i := 0; i < 505; i++ {
		c.NextBlock()
	}
	for i := 0; i < 2; i++ {
		if r := rel(big, sg[i], "ftx-big"); !r.OK() {
			t.Fatalf("large release after its delay refused: %v", r.Err)
		}
	}
	if c.U(scid, "w_"+strconv.FormatUint(big, 10)+"_st") != 1 {
		t.Fatal("the large withdrawal never released: a delay that never expires is a permanent freeze")
	}
	t.Logf("window 1200/2000 blocks (self-clearing), delay 5 and 505 blocks; sup %d", c.U(scid, "sup"))
}
