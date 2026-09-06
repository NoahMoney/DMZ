package tests

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// vault.bas v3: 3-of-5 signers, one treasury. Every rule the adversarial reviews asked for has a case here.
func TestVaultFederation(t *testing.T) {
	c := harness.New()
	owner, treasury, user, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	cfg := vaultCfg{k: 3, cap: 1000, minbond: 1000, minburn: 100, wfee: 10, mindep: 5, regfee: 100, signers: 5}
	code := readContract(t, "vault.bas")

	// install rejects an unsafe configuration
	bad := func(args rpc.Arguments) {
		t.Helper()
		if _, r := c.Install(code, nil, args, owner, harness.MaxStorageGas); r.OK() {
			t.Fatalf("install accepted %v", args)
		}
	}
	base := func(k, minburn, wfee, mindep uint64) rpc.Arguments {
		return rpc.Arguments{harness.U("k", k), harness.S("treasury", treasury.String()), harness.U("cap", 1000),
			harness.U("minbond", 1000), harness.U("minburn", minburn), harness.U("wfee", wfee), harness.U("mindep", mindep), harness.U("regfee", 0), harness.U("capratio", 3), harness.U("bigbps", 2000), harness.U("bigdelay", 0)}
	}
	bad(base(1, 100, 10, 5))  // quorum below 2
	bad(base(3, 100, 100, 5)) // a fee that eats the whole minimum burn
	bad(base(3, 100, 10, 0))  // no minimum deposit

	scid, sg := installVault(t, c, owner, treasury, cfg)
	t.Logf("vault.bas: %d bytes", len(code))
	// a stranger may post a bond (that is how a future signer becomes a candidate) but it is not at stake, so it
	// cannot be used to inflate the bond total the cap is bounded by
	before := c.U(scid, "bondtot")
	if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 5000}, stranger); !r.OK() || c.U(scid, "bondtot") != before {
		t.Fatalf("a non-signer's bond counted at stake: %d", c.U(scid, "bondtot"))
	}
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() || c.U(scid, "na") != 5 || c.U(scid, "sealed") != 1 {
		t.Fatalf("seal: %v", r.Err)
	}

	// register: the fee is exact and goes to the treasury; every call is a NEW index, so two deposits of one user
	// are not linked on chain by a shared address
	if r := c.Call(scid, "Register", nil, nil, user); r.OK() {
		t.Fatal("registered without the fee")
	}
	r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{zero: 100}, user)
	if !r.OK() || r.Sent(zero, treasury) != 100 || c.U(scid, "un") != 1 || c.U(scid, "uc_"+string(user.Compressed())) != 1 {
		t.Fatalf("register: %v sent %d", r.Err, r.Sent(zero, treasury))
	}
	// an address may hold only one index the signers have not attested yet, so nobody can force unbounded
	// attestation work on the federation by registering in a loop
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{zero: 100}, user); r.OK() {
		t.Fatal("a second index was handed out while the first was still unattested")
	}

	// attest: votes are keyed by the address hash; a signer may correct their vote before the quorum
	att := func(who *rpc.Address, j uint64, addr string) harness.Result {
		return c.Call(scid, "Attest", rpc.Arguments{harness.U("j", j), harness.S("addr", addr)}, nil, who)
	}
	att(sg[0], 0, "4Axmr-user-zero")
	att(sg[1], 0, "4Axmr-WRONG")
	if r := att(sg[1], 0, "4Axmr-user-zero"); !r.OK() {
		t.Fatalf("a signer could not move their vote: %v", r.Err)
	}
	if c.Exists(scid, "da_0") {
		t.Fatal("attested below the quorum")
	}
	if r := att(sg[2], 0, "4Axmr-user-zero"); !r.OK() || c.S(scid, "da_0") != "4Axmr-user-zero" || c.U(scid, "dver_0") != 1 {
		t.Fatalf("attest: %v", r.Err)
	}
	// the executed scope is closed: a further vote opens the NEXT version's scope instead of re-running this one
	if r := att(sg[3], 0, "4Axmr-user-zero"); !r.OK() || c.U(scid, "dver_0") != 1 {
		t.Fatalf("a vote after the decision should start the next version, not repeat it: %v ver %d", r.Err, c.U(scid, "dver_0"))
	}
	for i := 0; i < 3; i++ {
		att(sg[i], 0, "4Axmr-corrected")
	}
	if c.S(scid, "da_0") != "4Axmr-corrected" || c.U(scid, "dver_0") != 2 {
		t.Fatal("re-attestation failed")
	}
	// with index 0 attested the user may take a second one, and it is a different index with its own address
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{zero: 100}, user); !r.OK() || c.U(scid, "un") != 2 || c.U(scid, "ul_"+string(user.Compressed())+"_1") != 1 {
		t.Fatalf("second index: %v", r.Err)
	}

	// mint: a rogue tuple cannot pin the deposit; the honest tuple reaches k; the id is canonical
	mint := func(who *rpc.Address, j uint64, n int, vout, amount uint64) harness.Result {
		return c.Call(scid, "Mint", mintArgs(j, n, vout, amount), nil, who)
	}
	if r := mint(sg[0], 0, 1, 0, 4); r.OK() {
		t.Fatal("a deposit below the minimum was voted")
	}
	if r := mint(stranger, 0, 1, 0, 100); r.OK() {
		t.Fatal("a stranger voted")
	}
	if r := mint(sg[0], 5, 1, 0, 100); r.OK() {
		t.Fatal("an unknown index was accepted")
	}
	mint(sg[4], 0, 1, 0, 1) // rogue amount
	mint(sg[0], 0, 1, 0, 100)
	if r := mint(sg[0], 0, 1, 0, 100); r.OK() {
		t.Fatal("the same signer voted for the same tuple twice")
	}
	mint(sg[1], 0, 1, 0, 100)
	if c.Exists(scid, "mint_"+depID(1, 0)) {
		t.Fatal("minted below the quorum")
	}
	r = mint(sg[2], 0, 1, 0, 100)
	if !r.OK() || r.Sent(scid, user) != 100 || c.U(scid, "mint_"+depID(1, 0)) != 1 || c.U(scid, "sup") != 100 {
		t.Fatalf("the k-th vote should mint: %v sent %d", r.Err, r.Sent(scid, user))
	}
	if r := mint(sg[3], 0, 1, 0, 100); r.OK() {
		t.Fatal("a vote after the mint was accepted")
	}
	// a second output of the same transaction is a different deposit
	for i := 0; i < 3; i++ {
		mint(sg[i], 1, 1, 1, 50)
	}
	if c.U(scid, "sup") != 150 || c.U(scid, "mj_"+depID(1, 1)) != 1 {
		t.Fatalf("vout 1 of the same txid should be its own deposit: sup %d", c.U(scid, "sup"))
	}

	// cap: a mint over the cap is refused at the k-th vote; SetCap needs k and respects the bond ratio
	for i := 0; i < 2; i++ {
		mint(sg[i], 0, 2, 0, 900)
	}
	if r := mint(sg[2], 0, 2, 0, 900); r.OK() {
		t.Fatal("a mint over the cap was accepted")
	}
	if r := c.Call(scid, "SetCap", rpc.Arguments{harness.U("cap", 20000)}, nil, sg[0]); r.OK() {
		t.Fatal("a cap above three times the bonds was accepted")
	}
	for i := 0; i < 3; i++ {
		c.Call(scid, "SetCap", rpc.Arguments{harness.U("cap", 2000)}, nil, sg[i])
	}
	if c.U(scid, "cap") != 2000 || c.U(scid, "ce") != 1 {
		t.Fatalf("cap %d ce %d", c.U(scid, "cap"), c.U(scid, "ce"))
	}
	// the refused mint can now complete: the tuple votes were kept and the third signer finishes it
	if r := mint(sg[2], 0, 2, 0, 900); !r.OK() || c.U(scid, "sup") != 1050 {
		t.Fatalf("mint after the cap rise: %v sup %d", r.Err, c.U(scid, "sup"))
	}

	// burn: the claim stays in sup until the release is recorded, so reserve >= sup holds throughout
	burn := func(v uint64, dest string) harness.Result {
		return c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", dest)}, map[crypto.Hash]uint64{scid: v}, user)
	}
	if r := burn(50, "4AxmrDestinationAddr"); r.OK() {
		t.Fatal("a burn below the minimum was accepted")
	}
	if r := burn(200, "short"); r.OK() {
		t.Fatal("a destination too short to be any foreign address was accepted")
	}
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDestinationAddr")}, map[crypto.Hash]uint64{scid: 200, zero: 1}, user); r.OK() {
		t.Fatal("a burn carrying DERO was accepted")
	}
	if r := burn(200, "4AxmrDestinationAddr"); !r.OK() || c.U(scid, "w_0_v") != 190 || c.U(scid, "w_0_f") != 10 || c.U(scid, "sup") != 1050 {
		t.Fatalf("burn: %v v %d sup %d", r.Err, c.U(scid, "w_0_v"), c.U(scid, "sup"))
	}

	// released: k votes on one foreign txid; the claim then leaves sup and the fee becomes the treasury's
	rel := func(who *rpc.Address, j uint64, ftx string) harness.Result {
		return c.Call(scid, "Released", rpc.Arguments{harness.U("j", j), harness.S("ftx", ftx)}, nil, who)
	}
	rel(sg[0], 0, "ftx-aaaa")
	rel(sg[1], 0, "ftx-bbbb")
	rel(sg[2], 0, "ftx-aaaa")
	if c.U(scid, "w_0_st") != 0 {
		t.Fatal("released without k votes on one txid")
	}
	if r := rel(sg[3], 0, "ftx-aaaa"); !r.OK() || c.U(scid, "w_0_st") != 1 || c.S(scid, "w_0_ftx") != "ftx-aaaa" {
		t.Fatalf("release: %v", r.Err)
	}
	if c.U(scid, "sup") != 860 || c.U(scid, "feepool") != 10 {
		t.Fatalf("release accounting: sup %d feepool %d", c.U(scid, "sup"), c.U(scid, "feepool"))
	}
	// one foreign payment settles one withdrawal only
	burn(200, "4AxmrDestinationAddr")
	if r := rel(sg[0], 1, "ftx-aaaa"); r.OK() {
		t.Fatal("a foreign txid was reused for a second withdrawal")
	}
	for i := 0; i < 3; i++ {
		rel(sg[i], 1, "ftx-cccc")
	}
	if c.U(scid, "w_1_st") != 1 || c.U(scid, "sup") != 670 || c.U(scid, "feepool") != 20 {
		t.Fatalf("second release: st %d sup %d fee %d", c.U(scid, "w_1_st"), c.U(scid, "sup"), c.U(scid, "feepool"))
	}

	// halt: votes name the target state; mint and burn stop; a resume needs k again
	if r := c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 0)}, nil, sg[0]); r.OK() {
		t.Fatal("a vote for the current state was accepted")
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 1)}, nil, sg[i])
	}
	if c.U(scid, "halted") != 0 {
		t.Fatal("halted below the quorum")
	}
	c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 1)}, nil, sg[2])
	if c.U(scid, "halted") != 1 || c.U(scid, "he") != 1 {
		t.Fatal("halt failed")
	}
	if r := mint(sg[0], 0, 3, 0, 10); r.OK() {
		t.Fatal("minted while halted")
	}
	if r := burn(100, "4AxmrDestinationAddr"); r.OK() {
		t.Fatal("burned while halted")
	}
	for i := 0; i < 3; i++ {
		c.Call(scid, "Halt", rpc.Arguments{harness.U("want", 0)}, nil, sg[i])
	}
	if c.U(scid, "halted") != 0 || c.U(scid, "he") != 2 {
		t.Fatal("resume failed")
	}

	// slash: self-slash refused; k votes remove a signer, void every pending vote and pay the bond to the treasury
	if r := c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[0].String())}, nil, sg[0]); r.OK() {
		t.Fatal("self slash accepted")
	}
	mint(sg[4], 0, 3, 0, 7)
	mint(sg[0], 0, 3, 0, 7)
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
	if r := mint(sg[4], 0, 3, 0, 7); r.OK() {
		t.Fatal("a slashed signer voted")
	}
	mint(sg[1], 0, 3, 0, 7)
	mint(sg[2], 0, 3, 0, 7)
	if c.Exists(scid, "mint_"+depID(3, 0)) {
		t.Fatal("votes from before the slash still counted")
	}
	if r := mint(sg[0], 0, 3, 0, 7); !r.OK() || c.U(scid, "mint_"+depID(3, 0)) != 1 {
		t.Fatalf("mint in the new epoch: %v", r.Err)
	}
	// the quorum floor: with na=4 and k=3 one more slash is allowed, a second is not
	for i := 0; i < 3; i++ {
		c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[3].String())}, nil, sg[i])
	}
	if c.U(scid, "na") != 3 {
		t.Fatal("the second slash should have passed")
	}
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[2].String())}, nil, sg[i]); r.OK() {
			t.Fatal("a slash vote below the quorum floor was accepted")
		}
	}
	// the contract holds the three remaining signers' bonds plus the stranger's candidate bond, which they can
	// take back at any time because they never became a signer
	if c.U(scid, "na") != 3 || c.U(scid, "bondtot") != 3000 || c.SCBalance(scid, zero) != 8000 {
		t.Fatalf("na %d bondtot %d balance %d", c.U(scid, "na"), c.U(scid, "bondtot"), c.SCBalance(scid, zero))
	}
	if r := c.Call(scid, "Unbond", nil, nil, stranger); !r.OK() || r.Sent(zero, stranger) != 5000 || c.SCBalance(scid, zero) != 3000 {
		t.Fatalf("candidate unbond: %v", r.Err)
	}
}
