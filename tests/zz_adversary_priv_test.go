package tests

// Adversarial probes of the vault privileged and emergency paths (audit dimension:
// secrets, keys, privileges, emergency controls). Each test documents today's behaviour
// exactly, so it fails loudly if any of these properties is later changed.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func advVault(t *testing.T, c *harness.Chain, owner, treasury *rpc.Address) (crypto.Hash, []*rpc.Address) {
	t.Helper()
	cfg := vaultCfg{k: 2, cap: 9000, minbond: 1000, minburn: 100, wfee: 10, mindep: 5, regfee: 0, signers: 3}
	scid, sg := installVault(t, c, owner, treasury, cfg)
	mustOK(t, c.Call(scid, "Seal", nil, nil, owner), "seal")
	return scid, sg
}

// ADV-1. Paying(j) is a single-signer, unvoted, one-shot, never-expiring mark with no
// height gate. One key marks every open withdrawal in the block it appears; from then on
// the permissionless Reclaim exit is gone FOREVER and the only way out is k signer votes.
// The contract header claims "no state needs a human to leave it": one key falsifies it.
func TestOneSignerKeyPermanentlyRemovesTheReclaimExit(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid, sg := advVault(t, c, owner, treasury)
	mustOK(t, c.Call(scid, "Register", nil, nil, user), "register")
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i])
		c.Call(scid, "Mint", mintArgs(0, 1, 0, 4000), nil, sg[i])
	}
	if c.U(scid, "sup") != 4000 {
		t.Fatalf("mint: sup %d", c.U(scid, "sup"))
	}
	mustOK(t, c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationOne"), harness.S("refund", user.String())},
		map[crypto.Hash]uint64{scid: 500}, user), "burn 0")
	burnHeight := c.Height
	mustOK(t, c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationTwo"), harness.S("refund", user.String())},
		map[crypto.Hash]uint64{scid: 500}, user), "burn 1")

	// ONE signer, no vote, no fee, no delay: mark every open withdrawal.
	for j := uint64(0); j < c.U(scid, "wn"); j++ {
		mustOK(t, c.Call(scid, "Paying", rpc.Arguments{harness.U("j", j)}, nil, sg[0]), "single-signer mark")
	}
	if c.U(scid, "w_0_p") != burnHeight || c.U(scid, "w_1_p") == 0 {
		t.Fatalf("marks not set at the burn height: %d %d (burn %d)", c.U(scid, "w_0_p"), c.U(scid, "w_1_p"), burnHeight)
	}

	// Four days later (the Reclaim window) and then some: the exit is gone.
	c.Height += 20000 + 5000
	if r := c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", 0)}, nil, user); r.OK() {
		t.Fatal("Reclaim worked on a marked withdrawal")
	}
	// Five million blocks later, still gone. There is no expiry on the mark.
	c.Height += 5000000
	if r := c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", 0)}, nil, user); r.OK() {
		t.Fatal("Reclaim came back after a very long wait")
	}
	if r := c.Call(scid, "Reclaim", rpc.Arguments{harness.U("j", 1)}, nil, user); r.OK() {
		t.Fatal("Reclaim came back on the second withdrawal")
	}
	// With fewer than k signers answering, Veto cannot finish either: one vote is counted
	// and nothing settles.
	if r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[1]); !r.OK() {
		t.Fatalf("veto vote refused: %v", r.Err)
	}
	if c.U(scid, "w_0_st") != 0 || c.U(scid, "w_1_st") != 0 {
		t.Fatalf("state moved: %d %d", c.U(scid, "w_0_st"), c.U(scid, "w_1_st"))
	}
	t.Logf("ADV-1 CONFIRMED: one signer marked every open withdrawal at height %d; at height %d Reclaim is still refused for both and only k votes can settle them",
		burnHeight, c.Height)
}

// ADV-2. Register bars an address for ever while its newest index is unattested. There is
// no expiry, no abandon, no per-index timeout: only k signer votes can clear it. A halt
// (a k-of-n emergency control) blocks Attest, so the federation emergency control creates
// a state that only the federation can leave.
func TestAnUnattestedIndexBarsThatAddressForEver(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid, sg := advVault(t, c, owner, treasury)
	mustOK(t, c.Call(scid, "Register", nil, nil, user), "register 1")
	if c.U(scid, "un") != 1 {
		t.Fatalf("un %d", c.U(scid, "un"))
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 1)}, nil, sg[i])
	}
	if c.U(scid, "halted") == 0 {
		t.Fatal("halt did not take")
	}
	if r := c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[0]); r.OK() {
		t.Fatal("Attest worked while halted")
	}
	// Let the halt lease lapse: the vault reopens, but this address is still barred.
	c.Height += 20001
	if r := c.Call(scid, "Register", nil, nil, user); r.OK() {
		t.Fatal("Register worked with an unattested index")
	}
	c.Height += 1000000
	if r := c.Call(scid, "Register", nil, nil, user); r.OK() {
		t.Fatal("the bar expired")
	}
	// Only the federation can clear it.
	for i := 0; i < 2; i++ {
		mustOK(t, c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i]), "attest")
	}
	mustOK(t, c.Call(scid, "Register", nil, nil, user), "register 2")
	t.Log("ADV-2 CONFIRMED: an address whose newest index is unattested can never Register again until k signers attest it; no timeout exists")
}

// ADV-3. ClaimFees is the treasury key only power, and sup is NOT reduced by it. The
// directive requires the operator to revoke their own access after deployment; destroying
// the treasury key locks every wfee unit ever collected AND leaves them counted in sup for
// ever, so the federation must keep foreign coins backing units nobody can redeem.
func TestFeeUnitsStayInSupAndNeedTheTreasuryKeyForEver(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	stranger := c.NewAccount()
	scid, sg := advVault(t, c, owner, treasury)
	mustOK(t, c.Call(scid, "Register", nil, nil, user), "register")
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i])
		c.Call(scid, "Mint", mintArgs(0, 1, 0, 4000), nil, sg[i])
	}
	mustOK(t, c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationOne"), harness.S("refund", user.String())},
		map[crypto.Hash]uint64{scid: 500}, user), "burn")
	for i := 0; i < 2; i++ {
		c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "ftx-one")}, nil, sg[i])
	}
	supAfter, fee := c.U(scid, "sup"), c.U(scid, "feepool")
	if fee != 10 || supAfter != 4000-490 {
		t.Fatalf("release accounting: sup %d feepool %d", supAfter, fee)
	}
	// Nobody but the treasury can move them: not a signer, not the owner, not a stranger.
	for _, who := range []*rpc.Address{sg[0], sg[1], owner, user, stranger} {
		if r := c.Call(scid, "ClaimFees", nil, nil, who); r.OK() {
			t.Fatal("a non-treasury address claimed the fee pool")
		}
	}
	mustOK(t, c.Call(scid, "ClaimFees", nil, nil, treasury), "claim")
	if c.U(scid, "feepool") != 0 {
		t.Fatal("feepool not drained")
	}
	if c.U(scid, "sup") != supAfter {
		t.Fatalf("ClaimFees changed sup: %d -> %d", supAfter, c.U(scid, "sup"))
	}
	// The claimed units are below minburn (100), so even the treasury cannot redeem them:
	// a permanent claim on the foreign reserve that no key can settle.
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrTreasuryCashOut"), harness.S("refund", treasury.String())},
		map[crypto.Hash]uint64{scid: fee}, treasury); r.OK() {
		t.Fatal("a sub-minburn burn was accepted")
	}
	t.Logf("ADV-3 CONFIRMED: sup stayed %d across ClaimFees; %d fee units sit with the treasury below minburn, so they can never leave sup", c.U(scid, "sup"), fee)
}

// ADV-4. The viewing key is what the app turns into "anyone can check the reserve", and
// SetViewKey validates nothing about the string: k signers can publish any 1..400 byte
// value and earn the green line.
func TestSetViewKeyAcceptsAnyString(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	scid, sg := advVault(t, c, owner, treasury)
	junk := "not a viewing key at all -- just words"
	for i := 0; i < 2; i++ {
		mustOK(t, c.Call(scid, "SetViewKey", rpc.Arguments{harness.S("key", junk)}, nil, sg[i]), "setviewkey")
	}
	if got := c.S(scid, "viewkey"); got != junk {
		t.Fatalf("viewkey %q", got)
	}
	t.Logf("ADV-4 CONFIRMED: viewkey is now %q — no hex, length or curve check exists", junk)
}
