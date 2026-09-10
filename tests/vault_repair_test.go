package tests

// LIV-04 (docs/audit/FINDINGS_2026-09-10.md), the contract half, examined and NOT repaired.
//
// The finding is real: vault.bas Burn 20-21 accepts `refund` on ADDRESS_RAW + IS_ADDRESS_VALID, which only
// decompress a point, and stores it in w_<j>_by; Veto 41 and Reclaim 21 both address their single transfer to
// that key, so a refund address that never registered on DERO makes every return path revert at every height
// for every caller. tests/zzadversarial_liveness_test.go TestBurnRefundToAnUnregisteredAddressCanNeverBeReturned
// reproduces it and still passes.
//
// Refusing such a Burn at the door would need the contract to test registration, and this test records why it
// cannot do that for free: the DVM drops a zero-amount send before it becomes a transfer record
// (vendor/derohe/dvm/dvm_functions.go dvm_send_dero_to_address and dvm_send_asset_to_address both return
// early on amount 0), and the registration check is a loop over transfer records
// (dvm/simulator.go SanityCheckExternalTransfers 227-238). So the only probe a contract has is a send of at
// least one atomic unit — the burner's own money, paid out at Burn time, which changes what a withdrawal is
// worth and needs a redeploy. The alternative, restricting `refund` to an address the vault has already seen
// sign a call (every ua_<j> from Register), refuses burns that work today and closes the exit while the vault
// is halted, since Register is halt-gated and Burn deliberately is not. Both are operator decisions, so the
// code is left alone; the guard belongs in app/wrap.js and tools/vaultsigner as the finding says.
//
// This test exists so that nobody later "fixes" LIV-04 with a zero-value probe and believes it works.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

const probeSource = `Function Initialize() Uint64
5 IF EXISTS("initd") != 0 THEN GOTO 900
10 STORE("initd", 1)
20 RETURN 0
900 RETURN 1
End Function

Function ProbeFree(who String) Uint64
10 DIM s as String
20 LET s = ADDRESS_RAW(who)
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
30 SEND_DERO_TO_ADDRESS(s, 0)
100 RETURN 0
900 RETURN 1
End Function

Function ProbeOne(who String) Uint64
10 DIM s as String
20 LET s = ADDRESS_RAW(who)
21 IF IS_ADDRESS_VALID(s) == 0 THEN GOTO 900
30 SEND_DERO_TO_ADDRESS(s, 1)
100 RETURN 0
900 RETURN 1
End Function
`

func TestAContractCannotTestRegistrationWithoutPayingForIt(t *testing.T) {
	c := harness.New()
	caller := c.NewAccount()
	scid, res := c.Install(probeSource, map[crypto.Hash]uint64{zero: 100000}, nil, caller, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	ghost := unregisteredAddress()

	// A zero-amount send to an address that has never registered is accepted, because it never becomes a
	// transfer at all: the contract learns nothing.
	r := c.Call(scid, "ProbeFree", rpc.Arguments{harness.S("who", ghost.String())}, nil, caller)
	if !r.OK() {
		t.Fatalf("a zero-value send to an unregistered address was refused: %v", r.Err)
	}
	if len(r.Transfers) != 0 {
		t.Fatalf("a zero-value send emitted %d transfer records", len(r.Transfers))
	}

	// One atomic unit does reach the registration check — and that is the whole cost of the only probe there is.
	if r := c.Call(scid, "ProbeOne", rpc.Arguments{harness.S("who", ghost.String())}, nil, caller); r.OK() {
		t.Fatal("a one-unit send to an unregistered address succeeded")
	} else {
		t.Logf("the only probe a contract has costs one atomic unit and reverts here: %v", r.Err)
	}
	if r := c.Call(scid, "ProbeOne", rpc.Arguments{harness.S("who", caller.String())}, nil, caller); !r.OK() || len(r.Transfers) != 1 {
		t.Fatalf("one unit to a registered address: %v transfers %d", r.Err, len(r.Transfers))
	}
}
