package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// The vault's terminal states, and the rule the audit found broken three ways: no state may need a human to
// leave it. A halt was a boolean with no lease that blocked Burn (reproduced frozen at +2,000,000 blocks);
// Released was refused while halted but Veto was not, so a withdrawal already paid abroad could be refunded on
// chain once its Paying mark lapsed (reproduced); and a burn that never reached quorum had no way out at all.
//
// The halt is now a lease, the exit is never blocked, and an unmarked burn returns itself. Each of those is
// asserted below against the same sealed 2-of-3 vault, and the halt matrix — which entrypoints a halt stops —
// is written down as a table rather than left implicit in guard lines that can disagree.

const haltLease = 20000   // blocks a Halt(1) vote lasts before it lapses unless renewed
const reclaimAfter = 20000 // blocks after w_<j>_rel before an unmarked burn may Reclaim

func sealedVault(t *testing.T) (*harness.Chain, crypto.Hash, []*rpc.Address, *rpc.Address) {
	t.Helper()
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	cfg := vaultCfg{k: 2, cap: 9000, minbond: 1000, minburn: 100, wfee: 10, mindep: 5, regfee: 0, signers: 3}
	scid, sg := installVault(t, c, owner, treasury, cfg)
	mustOK(t, c.Call(scid, "Seal", nil, nil, owner), "seal")
	mustOK(t, c.Call(scid, "Register", nil, nil, user), "register")
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i])
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "Mint", mintArgs(0, 1, 0, 2000), nil, sg[i])
	}
	if c.U(scid, "sup") != 2000 {
		t.Fatalf("mint: sup %d", c.U(scid, "sup"))
	}
	return c, scid, sg, user
}

func halt(c *harness.Chain, scid crypto.Hash, sg []*rpc.Address, want uint64) {
	for i := 0; i < 2; i++ {
		c.Call(scid, "Halt", rpc.Arguments{harness.U("want", want)}, nil, sg[i])
	}
}

func isHalted(c *harness.Chain, scid crypto.Hash) bool { return c.U(scid, "halted") > c.Height }

func burn(c *harness.Chain, scid crypto.Hash, user *rpc.Address, v uint64) harness.Result {
	return c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrSomewhereFarAway"), harness.S("refund", user.String())},
		map[crypto.Hash]uint64{scid: v}, user)
}

// The matrix. A halt stops what adds exposure and nothing else.
func TestHaltMatrix(t *testing.T) {
	c, scid, sg, user := sealedVault(t)
	mustOK(t, burn(c, scid, user, 300), "burn before halt")
	halt(c, scid, sg, 1)
	if !isHalted(c, scid) {
		t.Fatal("halt did not take")
	}
	stranger := c.NewAccount()
	rows := []struct {
		name string
		call func() harness.Result
		want bool // may it succeed while halted?
	}{
		{"Register", func() harness.Result { return c.Call(scid, "Register", nil, nil, stranger) }, false},
		{"Attest", func() harness.Result {
			return c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-again")}, nil, sg[0])
		}, false},
		{"Mint", func() harness.Result { return c.Call(scid, "Mint", mintArgs(0, 2, 0, 100), nil, sg[0]) }, false},
		{"Burn", func() harness.Result { return burn(c, scid, user, 200) }, true},
		{"Paying", func() harness.Result { return c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 0)}, nil, sg[0]) }, true},
		{"Released (first vote)", func() harness.Result {
			c.Height = c.U(scid, "w_0_rel") + 1
			return c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "ftx-halted")}, nil, sg[0])
		}, true},
		{"Veto (first vote, unmarked j=1)", func() harness.Result {
			return c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 1)}, nil, sg[0])
		}, true},
	}
	for _, r := range rows {
		got := r.call().OK()
		if got != r.want {
			t.Errorf("while halted, %s: succeeded=%v, matrix says %v", r.name, got, r.want)
		}
	}
}

// BC-3 inverted: a halt that nobody lifts lapses, and burning was never blocked by it in the first place.
func TestAHaltIsALeaseAndNeverBlocksTheExit(t *testing.T) {
	c, scid, sg, user := sealedVault(t)
	halt(c, scid, sg, 1)
	set := c.U(scid, "halted")
	if set != c.Height+haltLease {
		t.Fatalf("halt stored %d, expected lapse at %d", set, c.Height+haltLease)
	}
	if r := burn(c, scid, user, 300); !r.OK() {
		t.Fatalf("burn refused while halted: %v", r.Err)
	}
	if r := c.Call(scid, "Mint", mintArgs(0, 2, 0, 100), nil, sg[0]); r.OK() {
		t.Fatal("mint accepted while halted")
	}
	// renewal by a live federation moves the lapse forward
	c.Height += 5000
	halt(c, scid, sg, 1)
	if got := c.U(scid, "halted"); got != c.Height+haltLease {
		t.Fatalf("renewal stored %d, expected %d", got, c.Height+haltLease)
	}
	// a dead federation's halt lapses on its own: nobody votes, the vault is open again
	c.Height = c.U(scid, "halted")
	if isHalted(c, scid) {
		t.Fatal("still halted at the lapse height")
	}
	if r := c.Call(scid, "Mint", mintArgs(0, 2, 0, 100), nil, sg[0]); !r.OK() {
		t.Fatalf("mint refused after the halt lapsed: %v", r.Err)
	}
	// and a resume vote on a lapsed halt is a no-op, not an error path anyone has to walk
	if r := c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 0)}, nil, sg[0]); r.OK() {
		t.Fatal("a resume vote was accepted on a vault that is not halted")
	}
}

// BC-2 inverted: a payment made before or during a halt is recorded, so the Paying mark is discharged by the
// truth rather than left to lapse into a refund.
func TestReleasedWorksWhileHaltedSoAPaidWithdrawalCannotBeRefunded(t *testing.T) {
	c, scid, sg, user := sealedVault(t)
	mustOK(t, burn(c, scid, user, 500), "burn")
	mustOK(t, c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 0)}, nil, sg[0]), "paying")
	halt(c, scid, sg, 1)
	c.Height = c.U(scid, "w_0_rel") + 1
	for i := 0; i < 2; i++ {
		c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "ftx-paid-during-halt")}, nil, sg[i])
	}
	if c.U(scid, "w_0_st") != 1 || c.U(scid, "sup") != 1510 {
		t.Fatalf("released while halted: st %d sup %d", c.U(scid, "w_0_st"), c.U(scid, "sup"))
	}
	// the mark's window has long passed; nothing can now return the units
	c.Height = c.U(scid, "w_0_p") + 2001
	if r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[0]); r.OK() {
		t.Fatal("a released withdrawal was vetoed")
	}
	if r := c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", 0)}, nil, user); r.OK() {
		t.Fatal("a released withdrawal was reclaimed")
	}
}

// RT-01 inverted: a burn nobody settles returns its units to the burner without anyone's help — unless a
// signer marked it, in which case the coins may already have left and only k votes may decide.
func TestAnUnsettledBurnReclaimsItselfUnlessMarked(t *testing.T) {
	c, scid, sg, user := sealedVault(t)
	mustOK(t, burn(c, scid, user, 500), "burn 0")
	mustOK(t, burn(c, scid, user, 300), "burn 1")
	reclaim := func(j uint64, who *rpc.Address) harness.Result {
		return c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", j)}, nil, who)
	}
	// too early
	if r := reclaim(0, user); r.OK() {
		t.Fatal("reclaimed before the window")
	}
	c.Height = c.U(scid, "w_0_rel") + reclaimAfter - 1
	if r := reclaim(0, user); r.OK() {
		t.Fatal("reclaimed one block early")
	}
	// a signer marks withdrawal 1 as being paid: that one can never self-reclaim
	mustOK(t, c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 1)}, nil, sg[0]), "paying 1")
	c.Height = c.U(scid, "w_1_rel") + reclaimAfter + 10
	if r := reclaim(1, user); r.OK() {
		t.Fatal("a marked withdrawal reclaimed itself: the coins may already have been sent")
	}
	// withdrawal 0 was never marked: anyone may trigger the reclaim, and it pays only the burner
	stranger := c.NewAccount()
	r := reclaim(0, stranger)
	if !r.OK() || r.Sent(scid, user) != 500 || r.Sent(scid, stranger) != 0 || c.U(scid, "w_0_st") != 2 || c.U(scid, "sup") != 2000 {
		t.Fatalf("reclaim: %v paid user %d stranger %d st %d sup %d", r.Err, r.Sent(scid, user), r.Sent(scid, stranger), c.U(scid, "w_0_st"), c.U(scid, "sup"))
	}
	if r := reclaim(0, user); r.OK() {
		t.Fatal("reclaimed twice")
	}
	// the marked one is still the quorum's to settle: Veto is refused while the mark is fresh and works once
	// it has lapsed (the mark was set after the reclaim window opened, so it outlives it here)
	if r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 1)}, nil, sg[0]); r.OK() {
		t.Fatal("veto accepted while the Paying mark was still fresh")
	}
	c.Height = c.U(scid, "w_1_p") + 2000
	for i := 0; i < 2; i++ {
		c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 1)}, nil, sg[i])
	}
	if c.U(scid, "w_1_st") != 2 {
		t.Fatalf("veto after the mark lapsed: st %d", c.U(scid, "w_1_st"))
	}
	// and nothing here sent value before a failure exit: Reclaim's send is its last statement
}

// No path in the new machine sends value and then fails: the ordering test covers Reclaim too.
func TestReclaimIsPermissionlessAndRingAgnostic(t *testing.T) {
	c, scid, _, user := sealedVault(t)
	mustOK(t, burn(c, scid, user, 500), "burn")
	c.Height = c.U(scid, "w_0_rel") + reclaimAfter
	// nil signer emulates ring size > 2, where SIGNER() is 33 zero bytes: Reclaim must not depend on it
	r := c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", 0)}, nil, nil)
	if !r.OK() || r.Sent(scid, user) != 500 {
		t.Fatalf("reclaim above ring 2: %v paid %d", r.Err, r.Sent(scid, user))
	}
}
