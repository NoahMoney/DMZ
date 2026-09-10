package tests

// Adversarial probes of the factory owner key: the one privilege the directive says the
// operator must be able to revoke after deployment.

import (
	"testing"

	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// ADV-5. There is no state in which factory.bas has no owner. TransferOwnership only
// arms tmpowner; the transfer completes when the NEW owner signs ClaimOwnership. A
// transfer to a key nobody holds therefore never completes, and the old key keeps
// SetTreasury and SetRegFee for ever — while the chain shows a pending transfer that a
// reader may mistake for a completed revocation. The old owner can also re-arm tmpowner
// at will, so even the pending state is not a commitment.
func TestFactoryOwnershipCannotBeRevoked(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("regfee", 10000)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	// "burn": a well-formed address whose key nobody will ever use to sign.
	burn := c.NewAccount()
	mustOK(t, c.Call(scid, "TransferOwnership", rpc.Arguments{harness.S("newowner", burn.String())}, nil, owner), "transfer to burn")

	// The chain now shows a pending transfer, and the old key still holds every power.
	newTreasury := c.NewAccount()
	mustOK(t, c.Call(scid, "SetTreasury", rpc.Arguments{harness.S("treasury", newTreasury.String())}, nil, owner), "SetTreasury after 'revocation'")
	mustOK(t, c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 1)}, nil, owner), "SetRegFee after 'revocation'")
	if c.S(scid, "tstr") != newTreasury.String() {
		t.Fatalf("treasury not moved: %s", c.S(scid, "tstr"))
	}
	// And the old owner can re-arm the pending transfer to itself or anyone else.
	someone := c.NewAccount()
	mustOK(t, c.Call(scid, "TransferOwnership", rpc.Arguments{harness.S("newowner", someone.String())}, nil, owner), "re-arm tmpowner")
	mustOK(t, c.Call(scid, "ClaimOwnership", nil, nil, someone), "claim by the new key")
	if r := c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 5)}, nil, owner); r.OK() {
		t.Fatal("the old owner still had power after a COMPLETED transfer")
	}
	t.Log("ADV-5 CONFIRMED: a transfer to an unclaimable address leaves the old key in full control; only a transfer to a LIVE key ever removes it, so no no-owner state exists (BL-241 / BL-147 / D-10 plan a ceremony the code cannot perform)")
}

// ADV-6. SetRegFee is unbounded. One owner call freezes the registry: no pool, launch,
// NFT or NFA can ever be listed again, and nothing in the contract can undo it but the
// same key. A kill switch with no ceiling and no timelock.
func TestOwnerCanFreezeTheRegistryWithOneCall(t *testing.T) {
	c := harness.New()
	owner, treasury, creator := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("regfee", 0)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	mustOK(t, c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", txhash(1))}, nil, creator), "register while open")

	mustOK(t, c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 18446744073709551615)}, nil, owner), "set an unpayable fee")
	if r := c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", txhash(2))}, nil, creator); r.OK() {
		t.Fatal("a pool registered at an unpayable fee")
	}
	if r := c.Call(scid, "RegisterNFA", rpc.Arguments{harness.H("scid", txhash(3))}, nil, creator); r.OK() {
		t.Fatal("an NFA registered at an unpayable fee")
	}
	if c.U(scid, "pc") != 1 {
		t.Fatalf("pc %d", c.U(scid, "pc"))
	}
	t.Logf("ADV-6 CONFIRMED: regfee is now %d; every Register* entrypoint is dead and only the owner key can lift it", c.U(scid, "regfee"))
}
