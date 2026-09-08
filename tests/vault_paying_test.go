package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// Nobody may take out more than was put in. The hole this closes: between a signer sending Monero abroad and
// k signers recording Released, the withdrawal is still st == 0, so a quorum could Veto it and hand the burner
// their units back as well. The coins are gone and the units are back — the vault has paid twice, and the
// reserve no longer covers the supply. The only thing that ever stood in the way was each signer's own local
// file, which the quorum does not share, so signers with no knowledge of the payment could vote in good faith.
//
// Paying(j) puts that knowledge on chain before the money moves.
func TestVetoCannotPayAWithdrawalTwice(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	cfg := vaultCfg{k: 2, cap: 9000, minbond: 1000, minburn: 100, wfee: 10, mindep: 5, regfee: 0, signers: 3}
	scid, sg := installVault(t, c, owner, treasury, cfg)
	mustOK(t, c.Call(scid, "Seal", nil, nil, owner), "seal")
	mustOK(t, c.Call(scid, "Register", nil, nil, user), "register")
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i])
		c.Call(scid, "Mint", mintArgs(0, 1, 0, 2000), nil, sg[i])
	}
	if c.U(scid, "sup") != 2000 {
		t.Fatalf("mint: sup %d", c.U(scid, "sup"))
	}
	mustOK(t, c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrPayMeAbroadNow"), harness.S("refund", user.String())},
		map[crypto.Hash]uint64{scid: 500}, user), "burn")

	// Only a signer may say a payment is under way, and only for a withdrawal that is still open.
	if r := c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 0)}, nil, user); r.OK() {
		t.Fatal("a stranger marked a withdrawal as being paid")
	}
	if r := c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 7)}, nil, sg[0]); r.OK() {
		t.Fatal("a withdrawal that does not exist was marked")
	}
	if r := c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 0)}, nil, sg[0]); !r.OK() {
		t.Fatalf("paying: %v", r.Err)
	}
	at := c.U(scid, "w_0_p")
	if at != c.Height {
		t.Fatalf("mark recorded height %d at %d", at, c.Height)
	}

	// The whole point: with the payment in flight the quorum cannot return the units.
	for _, s := range sg {
		if r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, s); r.OK() {
			t.Fatal("a withdrawal already being paid abroad was vetoed — the burner is paid twice")
		}
	}
	if c.U(scid, "w_0_st") != 0 || c.U(scid, "sup") != 2000 {
		t.Fatalf("veto took effect anyway: st %d sup %d", c.U(scid, "w_0_st"), c.U(scid, "sup"))
	}

	// The mark cannot be refreshed, or one signer could hold a withdrawal open for ever by re-marking it.
	c.Height += 500
	c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 0)}, nil, sg[1])
	if c.U(scid, "w_0_p") != at {
		t.Fatalf("the mark was refreshed: %d then %d", at, c.U(scid, "w_0_p"))
	}

	// And the payment lands: Released is the truthful record and still works with the mark set.
	for i := 0; i < 2; i++ {
		c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "ftx-paid")}, nil, sg[i])
	}
	if c.U(scid, "w_0_st") != 1 || c.U(scid, "sup") != 1510 {
		t.Fatalf("release: st %d sup %d", c.U(scid, "w_0_st"), c.U(scid, "sup"))
	}
}

// The other half, and the one that decides whether this is a safety feature or a freeze: a payment that is
// marked and then never made must not strand the burner. The window expires and the veto works again, so no
// state needs a human to leave it.
func TestAMarkedWithdrawalThatIsNeverPaidUnfreezesItself(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	cfg := vaultCfg{k: 2, cap: 9000, minbond: 1000, minburn: 100, wfee: 10, mindep: 5, regfee: 0, signers: 3}
	scid, sg := installVault(t, c, owner, treasury, cfg)
	mustOK(t, c.Call(scid, "Seal", nil, nil, owner), "seal")
	mustOK(t, c.Call(scid, "Register", nil, nil, user), "register")
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i])
		c.Call(scid, "Mint", mintArgs(0, 1, 0, 2000), nil, sg[i])
	}
	mustOK(t, c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrNeverPaidAtAll"), harness.S("refund", user.String())},
		map[crypto.Hash]uint64{scid: 500}, user), "burn")
	mustOK(t, c.Call(scid, "Paying", rpc.Arguments{harness.U("j", 0)}, nil, sg[0]), "paying")

	// One block short of the window, still protected.
	c.Height = c.U(scid, "w_0_p") + 1999
	if r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[0]); r.OK() {
		t.Fatal("the window ended a block early")
	}
	// At the window, the federation can admit it never paid and give the units back.
	c.Height = c.U(scid, "w_0_p") + 2000
	c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[0])
	r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[1])
	if !r.OK() || r.Sent(scid, user) != 500 || c.U(scid, "w_0_st") != 2 {
		t.Fatalf("stranded after the window: %v returned %d st %d", r.Err, r.Sent(scid, user), c.U(scid, "w_0_st"))
	}
}

func mustOK(t *testing.T, r harness.Result, what string) {
	t.Helper()
	if !r.OK() {
		t.Fatalf("%s: %v", what, r.Err)
	}
}
