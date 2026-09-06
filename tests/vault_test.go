package tests

import (
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// vault.bas v2: 3-of-5 signers, one user, one treasury. Every rule the adversarial review asked for has a case.
func TestVaultFederation(t *testing.T) {
	c := harness.New()
	owner, treasury, user, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	sg := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()}
	code := readContract(t, "vault.bas")
	// k=3, cap 1000 (within 3 x bonds once every signer bonds 1000), minbond 1000, minburn 100, wfee 10
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 3), harness.S("treasury", treasury.String()), harness.U("cap", 1000), harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("vault.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	mint := func(who *rpc.Address, j uint64, id string, amount uint64) harness.Result {
		return c.Call(scid, "Mint", rpc.Arguments{harness.U("j", j), harness.S("id", id), harness.U("amount", amount)}, nil, who)
	}
	dep := strings.Repeat("ab", 32)

	// k < 2 is refused at install
	if _, r := c.Install(code, nil, rpc.Arguments{harness.U("k", 1), harness.S("treasury", treasury.String()), harness.U("cap", 1000), harness.U("minbond", 1), harness.U("minburn", 100), harness.U("wfee", 10)}, owner, harness.MaxStorageGas); r.OK() {
		t.Fatal("k=1 vault installed")
	}
	for _, s := range sg {
		if r := c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", s.String())}, nil, owner); !r.OK() {
			t.Fatalf("add signer: %v", r.Err)
		}
	}
	// seal refused until every signer holds minbond; a stranger cannot bond
	if r := c.Call(scid, "Seal", nil, nil, owner); r.OK() {
		t.Fatal("sealed without bonds")
	}
	if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000}, stranger); r.OK() {
		t.Fatal("stranger bonded")
	}
	for i, s := range sg {
		if i == 4 {
			c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 999}, s) // one short
			if r := c.Call(scid, "Seal", nil, nil, owner); r.OK() {
				t.Fatal("sealed with a signer below minbond")
			}
			c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1}, s)
			continue
		}
		if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000}, s); !r.OK() {
			t.Fatalf("bond: %v", r.Err)
		}
	}
	if c.U(scid, "bondtot") != 5000 {
		t.Fatalf("bondtot %d", c.U(scid, "bondtot"))
	}
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() || c.U(scid, "na") != 5 || c.U(scid, "sealed") != 1 {
		t.Fatalf("seal: %v", r.Err)
	}
	if r := c.Call(scid, "Register", nil, nil, user); !r.OK() || c.U(scid, "ui_"+string(user.Compressed())) != 0 {
		t.Fatalf("register: %v", r.Err)
	}

	// attest: votes keyed by address hash; k of 5 agree; re-attestation bumps the version
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "zs1x")}, nil, sg[i])
	}
	c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "zs1WRONG")}, nil, sg[2])
	if c.Exists(scid, "da_0") {
		t.Fatal("attested without k agreeing votes")
	}
	if r := c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "zs1x")}, nil, sg[3]); !r.OK() || c.S(scid, "da_0") != "zs1x" || c.U(scid, "dver_0") != 1 {
		t.Fatalf("attest: %v", r.Err)
	}
	for i := 0; i < 3; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "zs1corrected")}, nil, sg[i])
	}
	if c.S(scid, "da_0") != "zs1corrected" || c.U(scid, "dver_0") != 2 {
		t.Fatal("re-attestation failed")
	}

	// mint: a rogue first vote cannot pin the deposit; the honest tuple reaches k and mints; replay refused
	if r := mint(sg[4], 0, dep, 1); !r.OK() { // rogue: wrong amount
		t.Fatalf("rogue vote: %v", r.Err)
	}
	if r := mint(sg[0], 0, dep, 100); !r.OK() {
		t.Fatalf("honest vote 1: %v", r.Err)
	}
	if r := mint(sg[0], 0, dep, 100); r.OK() {
		t.Fatal("double vote accepted")
	}
	if r := mint(stranger, 0, dep, 100); r.OK() {
		t.Fatal("stranger vote accepted")
	}
	if r := mint(sg[1], 5, dep, 100); r.OK() {
		t.Fatal("unknown index accepted")
	}
	mint(sg[1], 0, dep, 100)
	if c.Exists(scid, "md_"+dep) {
		t.Fatal("minted below k")
	}
	r := mint(sg[2], 0, dep, 100)
	if !r.OK() || r.Sent(scid, user) != 100 || c.U(scid, "md_"+dep) != 1 || c.U(scid, "sup") != 100 || c.U(scid, "mj_"+dep) != 0 {
		t.Fatalf("k-th honest vote should mint: %v sent %d", r.Err, r.Sent(scid, user))
	}
	if r := mint(sg[3], 0, dep, 100); r.OK() {
		t.Fatal("vote after mint accepted")
	}
	// id with a vout suffix is accepted; a short id is not
	dep2 := strings.Repeat("cd", 32) + ":1"
	for i := 0; i < 3; i++ {
		mint(sg[i], 0, dep2, 50)
	}
	if c.U(scid, "sup") != 150 {
		t.Fatalf("sup after second deposit: %d", c.U(scid, "sup"))
	}
	if r := mint(sg[0], 0, "abcd", 1); r.OK() {
		t.Fatal("short id accepted")
	}

	// cap: mint above cap refused at the k-th vote; SetCap needs k votes and the bond ratio; values can be revoted
	big := strings.Repeat("11", 32)
	mint(sg[0], 0, big, 900)
	mint(sg[1], 0, big, 900)
	if r := mint(sg[2], 0, big, 900); r.OK() {
		t.Fatal("mint over cap accepted")
	}
	if r := c.Call(scid, "SetCap", rpc.Arguments{harness.U("cap", 20000)}, nil, sg[0]); r.OK() {
		t.Fatal("cap above 3x bonds accepted")
	}
	for i := 0; i < 3; i++ {
		c.Call(scid, "SetCap", rpc.Arguments{harness.U("cap", 2000)}, nil, sg[i])
	}
	if c.U(scid, "cap") != 2000 || c.U(scid, "ce") != 1 {
		t.Fatalf("cap %d ce %d", c.U(scid, "cap"), c.U(scid, "ce"))
	}
	for i := 0; i < 3; i++ {
		c.Call(scid, "SetCap", rpc.Arguments{harness.U("cap", 1000)}, nil, sg[i])
	}
	if c.U(scid, "cap") != 1000 {
		t.Fatal("cap could not return to a previously used value")
	}
	for i := 0; i < 3; i++ {
		c.Call(scid, "SetCap", rpc.Arguments{harness.U("cap", 2000)}, nil, sg[i])
	}
	if r := mint(sg[2], 0, big, 900); !r.OK() || c.U(scid, "sup") != 1050 {
		t.Fatalf("mint after cap raise: %v sup=%d", r.Err, c.U(scid, "sup"))
	}

	// burn: minimum, fee, sup accounting, DERO refused
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4A...")}, map[crypto.Hash]uint64{scid: 50}, user); r.OK() {
		t.Fatal("burn below minimum accepted")
	}
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4A...")}, map[crypto.Hash]uint64{scid: 200, zero: 1}, user); r.OK() {
		t.Fatal("burn with DERO accepted")
	}
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4A...")}, map[crypto.Hash]uint64{scid: 200}, user); !r.OK() || c.U(scid, "w_0_v") != 190 || c.U(scid, "sup") != 850 || c.U(scid, "feepool") != 10 || c.U(scid, "w_0_st") != 0 {
		t.Fatalf("burn: %v w_0_v=%d sup=%d fee=%d", r.Err, c.U(scid, "w_0_v"), c.U(scid, "sup"), c.U(scid, "feepool"))
	}
	// released: the ftx is part of the agreement
	c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "aaaa")}, nil, sg[0])
	c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "bbbb")}, nil, sg[1])
	c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "aaaa")}, nil, sg[2])
	if c.U(scid, "w_0_st") != 0 {
		t.Fatal("released without k votes on one ftx")
	}
	if r := c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "aaaa")}, nil, sg[3]); !r.OK() || c.U(scid, "w_0_st") != 1 || c.S(scid, "w_0_ftx") != "aaaa" {
		t.Fatalf("release: %v", r.Err)
	}

	// halt: votes name the target; k needed; mint and burn refuse while halted; un-halt needs k
	if r := c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 0)}, nil, sg[0]); r.OK() {
		t.Fatal("vote for the current state accepted")
	}
	c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 1)}, nil, sg[0])
	c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 1)}, nil, sg[1])
	if c.U(scid, "halted") != 0 {
		t.Fatal("halted below k")
	}
	c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 1)}, nil, sg[2])
	if c.U(scid, "halted") != 1 || c.U(scid, "he") != 1 {
		t.Fatal("halt failed")
	}
	dep3 := strings.Repeat("ef", 32)
	if r := mint(sg[0], 0, dep3, 1); r.OK() {
		t.Fatal("mint while halted")
	}
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "x")}, map[crypto.Hash]uint64{scid: 100}, user); r.OK() {
		t.Fatal("burn while halted")
	}
	for i := 0; i < 3; i++ {
		c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 0)}, nil, sg[i])
	}
	if c.U(scid, "halted") != 0 || c.U(scid, "he") != 2 {
		t.Fatal("resume failed")
	}

	// slash: self-slash refused; k votes remove sg[4], void every pending vote (new epoch), send the bond
	if r := c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[0].String())}, nil, sg[0]); r.OK() {
		t.Fatal("self slash accepted")
	}
	mint(sg[4], 0, dep3, 7) // a pending vote from the signer about to be removed
	mint(sg[0], 0, dep3, 7)
	for i := 0; i < 2; i++ {
		c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[4].String())}, nil, sg[i])
	}
	if c.U(scid, "na") != 5 {
		t.Fatal("slashed below k votes")
	}
	r = c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[4].String())}, nil, sg[2])
	if !r.OK() || r.Sent(zero, treasury) != 1000 || c.U(scid, "na") != 4 || c.U(scid, "se") != 1 || c.U(scid, "bondtot") != 4000 {
		t.Fatalf("slash: %v sent %d na %d se %d", r.Err, r.Sent(zero, treasury), c.U(scid, "na"), c.U(scid, "se"))
	}
	if r := mint(sg[4], 0, dep3, 7); r.OK() {
		t.Fatal("slashed signer voted")
	}
	// the two pre-slash votes are void: sg[1] and sg[2] alone must not complete the mint
	mint(sg[1], 0, dep3, 7)
	mint(sg[2], 0, dep3, 7)
	if c.Exists(scid, "md_"+dep3) {
		t.Fatal("stale votes from before the slash counted")
	}
	if r := mint(sg[0], 0, dep3, 7); !r.OK() || c.U(scid, "md_"+dep3) != 1 {
		t.Fatalf("mint in the new epoch: %v", r.Err)
	}
	// quorum floor: with na=4 and k=3 one more slash is allowed, a second is not
	for i := 0; i < 3; i++ {
		c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[3].String())}, nil, sg[i])
	}
	if c.U(scid, "na") != 3 {
		t.Fatal("second slash should pass (na 4 to 3)")
	}
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[2].String())}, nil, sg[i]); r.OK() {
			t.Fatal("slash vote below the quorum floor accepted")
		}
	}
	if c.U(scid, "na") != 3 {
		t.Fatalf("na %d: the quorum floor was breached", c.U(scid, "na"))
	}
	// the cap bound follows bonds down: 3 x 3000 = 9000 covers cap 2000; a raise to 9001 is refused
	for i := 0; i < 3; i++ {
		c.Call(scid, "SetCap", rpc.Arguments{harness.U("cap", 9001)}, nil, sg[i])
	}
	if c.U(scid, "cap") != 2000 {
		t.Fatal("cap above the bond ratio accepted")
	}
	if c.SCBalance(scid, zero) != 3000 {
		t.Fatalf("remaining bonds: %d", c.SCBalance(scid, zero))
	}
}
