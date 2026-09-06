package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// Review scenario (2026-09-06): k=2, n=3; halted; C votes Halt(0); A,B slash C; A alone votes Halt(0).
func TestHaltVoteDoesNotSurviveSlash(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	A, B, C := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()), harness.U("cap", 1000),
		harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10), harness.U("mindep", 1), harness.U("regfee", 0), harness.U("capratio", 3), harness.U("bigbps", 2000), harness.U("bigdelay", 0)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	for _, s := range []*rpc.Address{A, B, C} {
		if r := c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", s.String())}, nil, owner); !r.OK() {
			t.Fatalf("add: %v", r.Err)
		}
		if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000}, s); !r.OK() {
			t.Fatalf("bond: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() {
		t.Fatalf("seal: %v", r.Err)
	}
	halt := func(who *rpc.Address, want uint64) harness.Result {
		return c.Call(scid, "Halt", rpc.Arguments{harness.U("want", want)}, nil, who)
	}
	// halt with A and B
	halt(A, 1)
	if r := halt(B, 1); !r.OK() || c.U(scid, "halted") != 1 || c.U(scid, "he") != 1 {
		t.Fatalf("halt: %v halted=%d he=%d", r.Err, c.U(scid, "halted"), c.U(scid, "he"))
	}
	// rogue C votes to resume
	if r := halt(C, 0); !r.OK() {
		t.Fatalf("C vote: %v", r.Err)
	}
	t.Logf("after C's vote: halted=%d he=%d se=%d", c.U(scid, "halted"), c.U(scid, "he"), c.U(scid, "se"))
	// A and B slash C
	c.Call(scid, "Slash", rpc.Arguments{harness.S("target", C.String())}, nil, A)
	if r := c.Call(scid, "Slash", rpc.Arguments{harness.S("target", C.String())}, nil, B); !r.OK() || c.U(scid, "se") != 1 || c.U(scid, "na") != 2 {
		t.Fatalf("slash: %v se=%d na=%d", r.Err, c.U(scid, "se"), c.U(scid, "na"))
	}
	t.Logf("after slash: halted=%d he=%d se=%d na=%d", c.U(scid, "halted"), c.U(scid, "he"), c.U(scid, "se"), c.U(scid, "na"))
	// A alone votes to resume
	r := halt(A, 0)
	t.Logf("after A's single vote: ok=%v halted=%d he=%d", r.OK(), c.U(scid, "halted"), c.U(scid, "he"))
	if c.U(scid, "halted") == 0 {
		t.Fatalf("DEFECT CONFIRMED: vault resumed on one live signer's vote (k=2, na=2); slashed C's stale Halt(0) vote counted")
	}
}
