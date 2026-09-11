package tests

// Adversarial probes of the factory owner key: the one privilege the directive says the
// operator must be able to revoke after deployment.

import (
	"testing"

	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// ADV-5 / SEC-K-02 (was TestFactoryOwnershipCannotBeRevoked). TransferOwnership used to arm tmpowner only, and the
// transfer completed when the NEW owner signed ClaimOwnership, so the planned transfer to a burn address could never
// complete and left the old key in full control behind a pending transfer a reader could take for a revocation.
// The transfer is gone (a key feared exposed is answered by Renounce, not by moving it). Renounce is the owner's own
// act and deletes owner; after it SetTreasury and SetRegFee refuse every caller for ever (TestFactoryRenounce has
// the full matrix).
func TestFactoryOwnershipEndsOnlyByRenounce(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("regfee", 10000)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	// "burn": a well-formed address whose key nobody will ever use to sign. Nothing can hand it the factory.
	burn := c.NewAccount()
	if r := c.Call(scid, "TransferOwnership", rpc.Arguments{harness.S("newowner", burn.String())}, nil, owner); r.OK() || c.Exists(scid, "tmpowner") {
		t.Fatal("a transfer of ownership is still possible")
	}
	// Until the owner renounces, the setters are the owner's: the deployment window.
	newTreasury := c.NewAccount()
	mustOK(t, c.Call(scid, "SetTreasury", rpc.Arguments{harness.S("treasury", newTreasury.String())}, nil, owner), "SetTreasury before Renounce")
	mustOK(t, c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 1)}, nil, owner), "SetRegFee before Renounce")

	// The owner renounces, and every power goes with it.
	mustOK(t, c.Call(scid, "Renounce", nil, nil, owner), "renounce")
	for _, who := range []*rpc.Address{owner, burn, nil} {
		if r := c.Call(scid, "SetTreasury", rpc.Arguments{harness.S("treasury", treasury.String())}, nil, who); r.OK() {
			t.Fatal("SetTreasury accepted after Renounce")
		}
		if r := c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 5)}, nil, who); r.OK() {
			t.Fatal("SetRegFee accepted after Renounce")
		}
	}
	if c.S(scid, "tstr") != newTreasury.String() || c.U(scid, "regfee") != 1 || c.Exists(scid, "owner") {
		t.Fatal("state moved after Renounce")
	}
	t.Log("ADV-5 REPAIRED: no transfer of the factory exists, and Renounce leaves no owner: SetTreasury and SetRegFee refuse the old key, a stranger and an anonymous caller, for ever")
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
