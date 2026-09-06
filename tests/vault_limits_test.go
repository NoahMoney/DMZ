package tests

import (
	"fmt"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// S22 (part): limits a colluding quorum cannot lift at runtime. The owner sets them before Seal: a per-deposit
// maximum, a rolling per-2000-block mint window, and a withdrawal delay before any Released vote counts.
func TestVaultLimits(t *testing.T) {
	c := harness.New()
	owner, treasury, user, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	sg := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount()}
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()), harness.U("cap", 100000), harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	limits := func(who *rpc.Address, maxdep, winmax, wdelay uint64) harness.Result {
		return c.Call(scid, "SetLimits", rpc.Arguments{harness.U("maxdep", maxdep), harness.U("winmax", winmax), harness.U("wdelay", wdelay)}, nil, who)
	}
	if limits(stranger, 1, 1, 1).OK() {
		t.Fatal("a stranger set the limits")
	}
	if limits(owner, 1, 1, 200000).OK() {
		t.Fatal("an absurd withdrawal delay was accepted")
	}
	if r := limits(owner, 500, 1200, 5); !r.OK() {
		t.Fatalf("set limits: %v", r.Err)
	}
	for _, s := range sg {
		if r := c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", s.String())}, nil, owner); !r.OK() {
			t.Fatalf("add signer: %v", r.Err)
		}
		if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 40000}, s); !r.OK() {
			t.Fatalf("bond: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() {
		t.Fatalf("seal: %v", r.Err)
	}
	if limits(owner, 9, 9, 9).OK() {
		t.Fatal("limits changed after Seal")
	}
	if c.U(scid, "maxdep") != 500 || c.U(scid, "winmax") != 1200 || c.U(scid, "wdelay") != 5 {
		t.Fatal("limits not stored")
	}
	if r := c.Call(scid, "Register", nil, nil, user); !r.OK() {
		t.Fatalf("register: %v", r.Err)
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "zs1user")}, nil, sg[i])
	}
	mint := func(id string, amount uint64) bool {
		for i := 0; i < 2; i++ {
			c.Call(scid, "Mint", rpc.Arguments{harness.U("j", 0), harness.S("id", id), harness.U("amount", amount)}, nil, sg[i])
		}
		return c.Exists(scid, "md_"+id)
	}
	id := func(n int) string { return fmt.Sprintf("%064x", n) }
	if mint(id(1), 600) {
		t.Fatal("a deposit above the per-deposit maximum was minted")
	}
	if !mint(id(2), 500) || !mint(id(3), 500) {
		t.Fatal("deposits within the limits were refused")
	}
	if mint(id(4), 300) {
		t.Fatal("the window (1200 per 2000 blocks) was exceeded: 500 + 500 + 300")
	}
	if !mint(id(5), 200) {
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
	if mint(id(6), 300) {
		t.Fatal("the window did not slide: a bucket boundary alone opened a new window")
	}
	for i := 0; i < 2000; i++ {
		c.NextBlock()
	}
	if !mint(id(7), 300) {
		t.Fatal("the window refused a deposit two buckets later")
	}
	// withdrawal delay: the release vote does not count before wdelay blocks have passed since the burn
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestination")}, map[crypto.Hash]uint64{scid: 200}, user); !r.OK() {
		t.Fatalf("burn: %v", r.Err)
	}
	rel := func(who *rpc.Address) harness.Result {
		return c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", id(9))}, nil, who)
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
	t.Logf("limits held: maxdep 500, window 1200/2000 blocks, delay 5; sup %d", c.U(scid, "sup"))
}
