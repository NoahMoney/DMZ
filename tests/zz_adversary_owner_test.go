package tests

// Adversarial probes of the factory owner key, inverted for factory.bas v2 (MT-J15(b)): the key they attacked no
// longer exists. v1 had an owner with SetTreasury and SetRegFee until Renounce; v2 fixes the treasury and the
// registration fee at Initialize for the life of the factory, so there is nothing to transfer, renounce or abuse.

import (
	"testing"

	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// ADV-5 / SEC-K-02 (was TestFactoryOwnershipCannotBeRevoked, then TestFactoryOwnershipEndsOnlyByRenounce). v1's
// TransferOwnership armed tmpowner only, so a transfer to a burn address could never complete; the transfer was
// removed and Renounce ended the owner's powers. v2 has no owner to transfer or renounce, and no deployment window in
// which the installer could still move the treasury or the fee: the installer's first calls after install are refused.
func TestFactoryHasNoOwnerToTransferOrRenounce(t *testing.T) {
	c := harness.New()
	installer, treasury := c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, factoryArgs(treasury.String(), 10000), installer, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	burn, other := c.NewAccount(), c.NewAccount()
	if r := c.Call(scid, "TransferOwnership", rpc.Arguments{harness.S("newowner", burn.String())}, nil, installer); r.OK() || c.Exists(scid, "tmpowner") {
		t.Fatal("a transfer of ownership is possible")
	}
	for _, who := range []*rpc.Address{installer, burn, nil} {
		if r := c.Call(scid, "SetTreasury", rpc.Arguments{harness.S("treasury", other.String())}, nil, who); r.OK() {
			t.Fatal("SetTreasury accepted")
		}
		if r := c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 1)}, nil, who); r.OK() {
			t.Fatal("SetRegFee accepted")
		}
		if r := c.Call(scid, "Renounce", nil, nil, who); r.OK() {
			t.Fatal("Renounce accepted")
		}
	}
	if c.S(scid, "tstr") != treasury.String() || c.U(scid, "regfee") != 10000 || c.Exists(scid, "owner") || c.Exists(scid, "renounced") {
		t.Fatal("the factory's state moved, or an owner key appeared")
	}
	t.Log("ADV-5 CLOSED BY DESIGN: factory.bas v2 has no owner; no transfer, setter or Renounce exists, and the installer's calls are refused from the first block")
}

// ADV-6, inverted. In v1 SetRegFee was unbounded, so one owner call could set a fee nobody can pay and freeze the
// registry until the same key lifted it. v2 has no SetRegFee: the fee is the one Initialize stored, and no call by
// anyone can raise it. The only way to an unpayable fee is to install one, once and in public: tools/verifydeploy
// prints regfee, and a deployment that got it wrong installs a new factory (tools/simdeploy -migrate-from).
func TestNoCallCanFreezeTheRegistry(t *testing.T) {
	c := harness.New()
	installer, treasury, creator := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "factory.bas")
	scid, res := c.Install(code, nil, factoryArgs(treasury.String(), 0), installer, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	mustOK(t, c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", txhash(1))}, nil, creator), "register while open")
	for _, who := range []*rpc.Address{installer, creator, nil} {
		if r := c.Call(scid, "SetRegFee", rpc.Arguments{harness.U("regfee", 18446744073709551615)}, nil, who); r.OK() {
			t.Fatal("an unpayable fee was set")
		}
	}
	mustOK(t, c.Call(scid, "RegisterPool", rpc.Arguments{harness.H("scid", txhash(2))}, nil, creator), "register after the attempt")
	mustOK(t, c.Call(scid, "RegisterNFA", rpc.Arguments{harness.H("scid", txhash(3))}, nil, creator), "register an NFA after the attempt")
	if c.U(scid, "pc") != 2 || c.U(scid, "nc") != 1 || c.U(scid, "regfee") != 0 {
		t.Fatalf("pc %d nc %d regfee %d", c.U(scid, "pc"), c.U(scid, "nc"), c.U(scid, "regfee"))
	}
	t.Log("ADV-6 CLOSED BY DESIGN: no call can change regfee; the registry stays open at the fee installed")
}
