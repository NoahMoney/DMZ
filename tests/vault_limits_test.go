package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// Limits a colluding quorum cannot lift at runtime. The owner sets them before Seal: a per-deposit maximum, a
// rolling per-2000-block mint window, and a withdrawal delay before any Released vote counts. After Seal only
// Tighten (k votes, safe direction only) can move them.
func TestVaultLimits(t *testing.T) {
	c := harness.New()
	owner, treasury, user, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	cfg := vaultCfg{k: 2, cap: 100000, minbond: 40000, minburn: 100, wfee: 10, mindep: 1, regfee: 0, signers: 3}
	limitsBefore := func(scid crypto.Hash, who *rpc.Address, maxdep, winmax, wdelay uint64) harness.Result {
		return c.Call(scid, "SetLimits", rpc.Arguments{harness.U("maxdep", maxdep), harness.U("winmax", winmax), harness.U("wdelay", wdelay)}, nil, who)
	}
	// the limits must be set before the signers are added, so install by hand rather than through the helper
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", cfg.k), harness.S("treasury", treasury.String()),
		harness.U("cap", cfg.cap), harness.U("minbond", cfg.minbond), harness.U("minburn", cfg.minburn),
		harness.U("wfee", cfg.wfee), harness.U("mindep", cfg.mindep), harness.U("regfee", cfg.regfee), harness.U("capratio", 3)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	if limitsBefore(scid, stranger, 1, 1, 1).OK() {
		t.Fatal("a stranger set the limits")
	}
	if limitsBefore(scid, owner, 1, 1, 200000).OK() {
		t.Fatal("an absurd withdrawal delay was accepted")
	}
	if r := limitsBefore(scid, owner, 500, 1200, 5); !r.OK() {
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
	if limitsBefore(scid, owner, 9, 9, 9).OK() {
		t.Fatal("limits changed after Seal")
	}
	if c.U(scid, "maxdep") != 500 || c.U(scid, "winmax") != 1200 || c.U(scid, "wdelay") != 5 {
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
	if mint(1, 600) {
		t.Fatal("a deposit above the per-deposit maximum was minted")
	}
	if !mint(2, 500) || !mint(3, 500) {
		t.Fatal("deposits within the limits were refused")
	}
	if mint(4, 300) {
		t.Fatal("the window (1200 per 2000 blocks) was exceeded: 500 + 500 + 300")
	}
	if !mint(5, 200) {
		t.Fatal("a deposit that fits the window remainder was refused")
	}
	if c.U(scid, "sup") != 1200 {
		t.Fatalf("sup %d", c.U(scid, "sup"))
	}
	// the window slides: this bucket plus the previous one. 2000 blocks later the previous bucket still holds
	// 1200, so nothing fits; 4000 blocks later it is clear
	for i := 0; i < 2000; i++ {
		c.NextBlock()
	}
	if mint(6, 300) {
		t.Fatal("the window did not slide: a bucket boundary alone opened a new window")
	}
	for i := 0; i < 2000; i++ {
		c.NextBlock()
	}
	if !mint(7, 300) {
		t.Fatal("the window refused a deposit two buckets later")
	}
	// withdrawal delay: the release vote does not count before wdelay blocks have passed since the burn
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationAddr")}, map[crypto.Hash]uint64{scid: 200}, user); !r.OK() {
		t.Fatalf("burn: %v", r.Err)
	}
	rel := func(who *rpc.Address) harness.Result {
		return c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "ftx-paid-abroad")}, nil, who)
	}
	if rel(sg[0]).OK() {
		t.Fatal("a release vote counted before the withdrawal delay")
	}
	for i := 0; i < 5; i++ {
		c.NextBlock()
	}
	if r := rel(sg[0]); !r.OK() {
		t.Fatalf("release vote after the delay refused: %v", r.Err)
	}
	if r := rel(sg[1]); !r.OK() || c.U(scid, "w_0_st") != 1 {
		t.Fatalf("release not completed: %v st %d", r.Err, c.U(scid, "w_0_st"))
	}
	if c.U(scid, "sup") != 1310 || c.U(scid, "feepool") != 10 {
		t.Fatalf("release accounting: sup %d feepool %d", c.U(scid, "sup"), c.U(scid, "feepool"))
	}
	// a veto is not blocked by the withdrawal delay: funds a federation cannot pay go back at once
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationAddr")}, map[crypto.Hash]uint64{scid: 200}, user); !r.OK() {
		t.Fatalf("second burn: %v", r.Err)
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 1)}, nil, sg[i])
	}
	if c.U(scid, "w_1_st") != 2 {
		t.Fatal("a veto was blocked by the withdrawal delay")
	}
	t.Logf("limits held: maxdep 500, window 1200/2000 blocks, delay 5; sup %d", c.U(scid, "sup"))
}
