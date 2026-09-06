package tests

import (
	"strings"

	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// The v3 safety valves: a withdrawal that cannot be paid goes back to its owner, a deposit that cannot be
// credited is closed for good, the fee pool is claimable, limits only tighten, a slashed signer can be replaced,
// bonds that were never a signer's come back, and the viewing key that makes the reserve auditable lives on chain.
func TestVaultSafetyValves(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	cfg := vaultCfg{k: 2, cap: 9000, minbond: 1000, minburn: 100, wfee: 10, mindep: 5, regfee: 0, signers: 3}
	scid, sg := installVault(t, c, owner, treasury, cfg)
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() {
		t.Fatalf("seal: %v", r.Err)
	}
	if r := c.Call(scid, "Register", nil, nil, user); !r.OK() {
		t.Fatalf("register: %v", r.Err)
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i])
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "Mint", mintArgs(0, 1, 0, 2000), nil, sg[i])
	}
	if c.U(scid, "sup") != 2000 {
		t.Fatalf("mint: sup %d", c.U(scid, "sup"))
	}

	// Veto: a withdrawal the federation cannot pay goes back to whoever burned it, units and fee together
	if r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "an-address-the-chain-rejects")}, map[crypto.Hash]uint64{scid: 500}, user); !r.OK() {
		t.Fatalf("burn: %v", r.Err)
	}
	if r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, user); r.OK() {
		t.Fatal("a user vetoed their own withdrawal")
	}
	c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[0])
	if c.U(scid, "w_0_st") != 0 {
		t.Fatal("vetoed below the quorum")
	}
	r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[1])
	if !r.OK() || r.Sent(scid, user) != 500 || c.U(scid, "w_0_st") != 2 || c.U(scid, "sup") != 2000 {
		t.Fatalf("veto: %v returned %d st %d sup %d", r.Err, r.Sent(scid, user), c.U(scid, "w_0_st"), c.U(scid, "sup"))
	}
	// a vetoed withdrawal can never then be released, and cannot be vetoed twice
	if r := c.Call(scid, "Released", rpc.Arguments{harness.U("j", 0), harness.S("ftx", "ftx-1")}, nil, sg[0]); r.OK() {
		t.Fatal("a vetoed withdrawal was released")
	}
	if r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 0)}, nil, sg[2]); r.OK() {
		t.Fatal("a vetoed withdrawal was vetoed again")
	}
	// and a released withdrawal can never be vetoed
	c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrGoodDestination")}, map[crypto.Hash]uint64{scid: 300}, user)
	for i := 0; i < 2; i++ {
		c.Call(scid, "Released", rpc.Arguments{harness.U("j", 1), harness.S("ftx", "ftx-paid")}, nil, sg[i])
	}
	if c.U(scid, "w_1_st") != 1 || c.U(scid, "sup") != 1710 || c.U(scid, "feepool") != 10 {
		t.Fatalf("release: st %d sup %d fee %d", c.U(scid, "w_1_st"), c.U(scid, "sup"), c.U(scid, "feepool"))
	}
	if r := c.Call(scid, "Veto", rpc.Arguments{harness.U("j", 1)}, nil, sg[0]); r.OK() {
		t.Fatal("a released withdrawal was vetoed")
	}

	// ClaimFees: the treasury takes the fee units; nobody else can
	if r := c.Call(scid, "ClaimFees", nil, nil, user); r.OK() {
		t.Fatal("a stranger claimed the fees")
	}
	r = c.Call(scid, "ClaimFees", nil, nil, treasury)
	if !r.OK() || r.Sent(scid, treasury) != 10 || c.U(scid, "feepool") != 0 || c.U(scid, "sup") != 1710 {
		t.Fatalf("claim fees: %v sent %d sup %d", r.Err, r.Sent(scid, treasury), c.U(scid, "sup"))
	}
	if r := c.Call(scid, "ClaimFees", nil, nil, treasury); r.OK() {
		t.Fatal("an empty fee pool was claimed")
	}

	// Refunded: a deposit that cannot be credited is closed for good and can never be minted afterwards
	ref := func(who *rpc.Address, n int, vout uint64, ftx string) harness.Result {
		return c.Call(scid, "Refunded", rpc.Arguments{harness.H("txid", txhash(n)), harness.U("vout", vout), harness.S("ftx", ftx)}, nil, who)
	}
	for i := 0; i < 2; i++ {
		ref(sg[i], 7, 0, "ftx-refund")
	}
	if c.U(scid, "mint_"+depID(7, 0)) != 2 || c.S(scid, "rf_"+depID(7, 0)) != "ftx-refund" {
		t.Fatalf("refund not recorded: %d", c.U(scid, "mint_"+depID(7, 0)))
	}
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Mint", mintArgs(0, 7, 0, 100), nil, sg[i]); r.OK() {
			t.Fatal("a refunded deposit was minted")
		}
	}
	// and a minted deposit can never be marked refunded
	if r := ref(sg[0], 1, 0, "ftx-x"); r.OK() {
		t.Fatal("a minted deposit was marked refunded")
	}

	// Tighten: only the safe direction, and only with k votes
	tighten := func(who *rpc.Address, winmax, wdelay uint64) harness.Result {
		return c.Call(scid, "Tighten", rpc.Arguments{harness.U("winmax", winmax), harness.U("wdelay", wdelay)}, nil, who)
	}
	if r := tighten(user, 100, 10); r.OK() {
		t.Fatal("a stranger tightened the limits")
	}
	for i := 0; i < 2; i++ {
		tighten(sg[i], 4000, 6)
	}
	if c.U(scid, "winmax") != 4000 || c.U(scid, "wdelay") != 6 || c.U(scid, "te") != 1 {
		t.Fatalf("tighten: winmax %d wdelay %d", c.U(scid, "winmax"), c.U(scid, "wdelay"))
	}
	if r := tighten(sg[0], 5000, 6); r.OK() {
		t.Fatal("a wider window was accepted")
	}
	if r := tighten(sg[0], 4000, 5); r.OK() {
		t.Fatal("a shorter withdrawal delay was accepted")
	}
	if r := tighten(sg[0], 0, 6); r.OK() {
		t.Fatal("disabling a set limit was accepted")
	}

	// SetViewKey: k votes publish the key that makes the reserve auditable
	for i := 0; i < 2; i++ {
		c.Call(scid, "SetViewKey", rpc.Arguments{harness.S("key", "monero-private-view-key-hex")}, nil, sg[i])
	}
	if c.S(scid, "viewkey") != "monero-private-view-key-hex" || c.U(scid, "ve") != 1 {
		t.Fatalf("view key not published: %q", c.S(scid, "viewkey"))
	}

	// AddSigner2: a candidate bonds first, k signers vote them in, and the bond then counts at stake
	cand := c.NewAccount()
	before := c.U(scid, "bondtot")
	if r := c.Call(scid, "AddSigner2", rpc.Arguments{harness.S("addr", cand.String())}, nil, sg[0]); r.OK() {
		t.Fatal("a candidate without a bond was voted in")
	}
	if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000}, cand); !r.OK() {
		t.Fatalf("candidate bond: %v", r.Err)
	}
	if c.U(scid, "bondtot") != before {
		t.Fatalf("a candidate's bond counted at stake before they were a signer: %d", c.U(scid, "bondtot"))
	}
	for i := 0; i < 2; i++ {
		c.Call(scid, "AddSigner2", rpc.Arguments{harness.S("addr", cand.String())}, nil, sg[i])
	}
	if c.U(scid, "na") != 4 || c.U(scid, "bondtot") != before+1000 || c.U(scid, "se") != 1 {
		t.Fatalf("add signer: na %d bondtot %d se %d", c.U(scid, "na"), c.U(scid, "bondtot"), c.U(scid, "se"))
	}
	if r := c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, cand); !r.OK() {
		t.Fatalf("the new signer cannot vote: %v", r.Err)
	}
	// a signer, new or old, can never take their bond back
	if r := c.Call(scid, "Unbond", nil, nil, cand); r.OK() {
		t.Fatal("a signer unbonded")
	}
	// a candidate who is never voted in can
	other := c.NewAccount()
	c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 700}, other)
	r = c.Call(scid, "Unbond", nil, nil, other)
	if !r.OK() || r.Sent(zero, other) != 700 || c.U(scid, "bondtot") != before+1000 {
		t.Fatalf("unbond: %v returned %d bondtot %d", r.Err, r.Sent(zero, other), c.U(scid, "bondtot"))
	}
	if r := c.Call(scid, "Unbond", nil, nil, other); r.OK() {
		t.Fatal("unbonded twice")
	}
}

// A k-of-k federation can never remove a signer: Slash needs k votes from signers other than the target, so with
// n == k one compromised key stays in the set for good and one absent key freezes the vault. Sealing such a set
// is refused (external audit finding F3, 2026-09-06).
func TestVaultRefusesQuorumOfEveryone(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	code := readContract(t, "vault.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 3), harness.S("treasury", treasury.String()), harness.U("cap", 1000),
		harness.U("minbond", 1000), harness.U("minburn", 100), harness.U("wfee", 10), harness.U("mindep", 1), harness.U("regfee", 0), harness.U("capratio", 3), harness.U("bigbps", 2000), harness.U("bigdelay", 0)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	sg := make([]*rpc.Address, 4)
	for i := range sg {
		sg[i] = c.NewAccount()
	}
	add := func(i int) {
		t.Helper()
		if r := c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", sg[i].String())}, nil, owner); !r.OK() {
			t.Fatalf("add %d: %v", i, r.Err)
		}
		if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000}, sg[i]); !r.OK() {
			t.Fatalf("bond %d: %v", i, r.Err)
		}
	}
	for i := 0; i < 3; i++ { // exactly k signers
		add(i)
	}
	if r := c.Call(scid, "Seal", nil, nil, owner); r.OK() {
		t.Fatal("a 3-of-3 federation was sealed: nobody in it could ever be slashed")
	}
	add(3)
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() || c.U(scid, "na") != 4 {
		t.Fatalf("3-of-4 refused: %v", r.Err)
	}
	// and with n = k+1 exactly one slash is possible, which is what keeps a compromised key removable
	for i := 1; i < 4; i++ {
		c.Call(scid, "Slash", rpc.Arguments{harness.S("target", sg[0].String())}, nil, sg[i])
	}
	if c.U(scid, "na") != 3 || c.U(scid, "isg_"+string(sg[0].Compressed())) != 0 {
		t.Fatalf("the one slash a k+1 set allows did not happen: na %d", c.U(scid, "na"))
	}
}

// One foreign output must have exactly one id. v3 takes the txid as 32 raw bytes and builds the id itself; an
// earlier draft also accepted the 64-character hex spelling and hashed whichever it was given, so the same
// deposit had two ids and could be minted twice (adversarial audit, 2026-09-06).
func TestVaultDepositIdIsCanonical(t *testing.T) {
	c := harness.New()
	owner, treasury, user := c.NewAccount(), c.NewAccount(), c.NewAccount()
	cfg := vaultCfg{k: 2, cap: 9000, minbond: 1000, minburn: 100, wfee: 10, mindep: 1, regfee: 0, signers: 3}
	scid, sg := installVault(t, c, owner, treasury, cfg)
	c.Call(scid, "Seal", nil, nil, owner)
	c.Call(scid, "Register", nil, nil, user)
	for i := 0; i < 2; i++ {
		c.Call(scid, "Attest", rpc.Arguments{harness.U("j", 0), harness.S("addr", "4Axmr-user")}, nil, sg[i])
	}
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Mint", mintArgs(0, 1, 0, 500), nil, sg[i]); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
	}
	if c.U(scid, "sup") != 500 {
		t.Fatalf("sup %d", c.U(scid, "sup"))
	}
	// the same deposit spelled as 64 hex characters instead of 32 raw bytes must not be a second deposit
	hexSpelling := depID(1, 0)[:64]
	for i := 0; i < 2; i++ {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("j", 0), harness.S("txid", hexSpelling), harness.U("vout", 0), harness.U("amount", 500)}, nil, sg[i]); r.OK() {
			t.Fatal("the hex spelling of a txid was accepted: one deposit would have two ids")
		}
	}
	if c.U(scid, "sup") != 500 {
		t.Fatalf("one deposit was minted twice: sup %d", c.U(scid, "sup"))
	}
	// and a txid of any other length is refused outright
	for _, n := range []int{16, 31, 33, 64} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("j", 0), harness.S("txid", strings.Repeat("a", n)), harness.U("vout", 0), harness.U("amount", 500)}, nil, sg[0]); r.OK() {
			t.Fatalf("a %d-byte txid was accepted", n)
		}
	}
}
