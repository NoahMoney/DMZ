package tests

// REFUTER evidence for the founders-nfa dimension. Two things the finder's FOUND-1 does not show, both at the
// maxsupply the repository actually deploys and documents (500: tools/simdeploy/main.go:48, deploy/README.md:73):
//
//  1. minhst is 11 there, and `und` has exactly one exit — distribute() when hst >= minhst (founders.bas:246).
//     No Withdraw of a deposit exists, no timeout, no operator path. So while fewer than 11 units are registered
//     every DERO the treasury deposits is frozen, and Distribute / Claim / PayBatch all return 0 and move nothing.
//     That is the constraint any change to minhst has to respect: raising the floor to (maxsupply+4)/5 = 100 of
//     500, as FOUND-1 proposes, would freeze the pass's whole income until a fifth of the supply is registered.
//  2. app/nfts.js:93 offers "Release waiting DERO" whenever hst > 0 and labels the row "Waiting for the first
//     registration". The first registration is not what releases it, and the app never reads minhst.

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

func TestRefuteFoundersUndIsFrozenBelowMinhstAtTheDeployedSupply(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	alice := c.NewAccount()
	const max, price = 500, 1000
	scid := installFounders(t, c, creator, treasury, price, max, 0, 0, 5000, 250) // reserve 0

	if mh := c.U(scid, "minhst"); mh != 11 {
		t.Fatalf("minhst %d at the deployed maxsupply 500, want 1 + 500/50 = 11", mh)
	}
	const dep = 900_000
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: dep}, treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	// alice mints and registers 10 — one short of the floor. hst > 0, so the app's Release button is offered.
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 10)}, map[crypto.Hash]uint64{zero: 10 * price}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 10}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.U(scid, "hst") != 10 || c.U(scid, "und") != dep || c.U(scid, "rpool") != 0 {
		t.Fatalf("hst %d und %d rpool %d", c.U(scid, "hst"), c.U(scid, "und"), c.U(scid, "rpool"))
	}
	// every call the app or the bot can make, by anyone, returns 0 and moves nothing
	for _, call := range []struct {
		entry string
		args  rpc.Arguments
	}{{"Distribute", nil}, {"Claim", nil}, {"PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}}} {
		r := c.Call(scid, call.entry, call.args, nil, alice)
		if !r.OK() {
			t.Fatalf("%s: %v", call.entry, r.Err)
		}
		if got := r.Sent(zero, alice); got != 0 {
			t.Fatalf("%s paid %d while hst(10) < minhst(11)", call.entry, got)
		}
		if c.U(scid, "und") != dep || c.U(scid, "rpool") != 0 {
			t.Fatalf("%s moved und to %d / rpool %d", call.entry, c.U(scid, "und"), c.U(scid, "rpool"))
		}
	}
	t.Logf("FROZEN: %d DERO sits in und at hst 10 of minhst 11. Distribute, Claim and PayBatch all succeed and move nothing; there is no other exit from und in founders.bas (SEND_DERO_TO_ADDRESS appears only in Mint:71, Buy:223 and settle:286, and settle pays from rpool)", dep)
	t.Logf("app/nfts.js:93 offers 'Release waiting DERO' on hst > 0 and labels this 'Waiting for the first registration' — the first registration happened; the eleventh registered unit is what releases it, and app/founders.js:13 never reads minhst")

	// the 11th unit releases it, which is what the label should say
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 1)}, map[crypto.Hash]uint64{zero: price}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.U(scid, "und") >= dep || c.U(scid, "rpool") == 0 {
		t.Fatalf("the 11th registration did not release und: und %d rpool %d", c.U(scid, "und"), c.U(scid, "rpool"))
	}
	t.Logf("the 11th registered unit released it: und %d, rpool %d", c.U(scid, "und"), c.U(scid, "rpool"))
	checkFounders(t, c, scid)
}
