package tests

// contracts/rate.bas: community sentiment. One standing vote per (signer, target), counted in O(1), changeable and
// clearable by its own voter only, each vote paying a fixed fee to the treasury. The display rules the app follows on
// top of these counts are held by tests/js/sentiment.test.mjs.

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/dvm"
	"github.com/deroproject/derohe/rpc"
	ref "github.com/deroproject/derohe/veil/reference"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// rateCanonSha256 is sha256(reference.Canon(contracts/rate.bas)): what simdeploy installs, what app/templates/rate.bas
// holds and what the app compares the node's copy with before it reads a count or sends a vote.
const rateCanonSha256 = "7dceb0c2e300d74a3fc7fb7955b18b89069892c8d3b9a3b52971480b27128c3c"

// rateFee is the fee simdeploy installs on the simulator and on mainnet (-rate-fee), 0.1 DERO.
const rateFee = 10000

func installRate(t *testing.T, c *harness.Chain, owner, treasury *rpc.Address, fee uint64) (crypto.Hash, harness.Result) {
	t.Helper()
	scid, res := c.Install(readContract(t, "rate.bas"), nil, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("fee", fee)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	if res.GasStorage >= harness.MaxStorageGas {
		t.Fatalf("install storage gas %d of %d", res.GasStorage, harness.MaxStorageGas)
	}
	return scid, res
}

func rateTarget(label string) crypto.Hash { return crypto.Hash(sha256.Sum256([]byte(label))) }

// rateVote sends Vote(target, v) with pay DERO attached; who == nil is a ring > 2 call.
func rateVote(c *harness.Chain, scid crypto.Hash, who *rpc.Address, target crypto.Hash, v, pay uint64) harness.Result {
	var in map[crypto.Hash]uint64
	if pay > 0 {
		in = map[crypto.Hash]uint64{zero: pay}
	}
	return c.Call(scid, "Vote", rpc.Arguments{harness.H("target", target), harness.U("v", v)}, in, who)
}

// rateU reads a uint64 key, 0 when it is absent.
func rateU(c *harness.Chain, scid crypto.Hash, k string) uint64 {
	v := c.Var(scid, k)
	if v == nil {
		return 0
	}
	return v.(uint64)
}

func rateKeys(target crypto.Hash, who *rpc.Address) (b, s, x string) {
	t := hex.EncodeToString(target[:])
	return "b_" + t, "s_" + t, "x_" + t + "_" + who.String()
}

// The state a refused call must leave alone: every counter and every standing vote of every target, and n.
func rateSnapshot(c *harness.Chain, scid crypto.Hash, targets []crypto.Hash, voters []*rpc.Address) string {
	var out []string
	for _, tg := range targets {
		for _, w := range voters {
			b, s, x := rateKeys(tg, w)
			out = append(out, b, strings.Repeat("|", int(rateU(c, scid, b))), s, strings.Repeat("|", int(rateU(c, scid, s))), x)
			if v := c.Var(scid, x); v != nil {
				out = append(out, strings.Repeat("|", int(v.(uint64))))
			}
		}
	}
	return strings.Join(out, ",") + ",n" + strings.Repeat("|", int(rateU(c, scid, "n")))
}

// Under a seeded random sequence of votes, changes, withdrawals, wrong payments, repeats and ring > 2 calls, each
// target's b_ and s_ equal the number of standing votes of each kind in a Go model, after every call; every x_ key is
// the model's vote or absent; n counts the accepted calls; the treasury receives exactly fee per accepted vote; and the
// contract never keeps DERO.
func TestRateCountersAreTheStandingVotes(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	scid, _ := installRate(t, c, owner, treasury, rateFee)
	voters := make([]*rpc.Address, 7)
	for i := range voters {
		voters[i] = c.NewAccount()
	}
	targets := []crypto.Hash{rateTarget("a"), rateTarget("b"), rateTarget("c")}
	model := map[[2]int]uint64{}
	rng := rand.New(rand.NewSource(20260915))
	var accepted, votes, paid, refused uint64
	for i := 0; i < 1500; i++ {
		vi, ti, v := rng.Intn(len(voters)), rng.Intn(len(targets)), uint64(rng.Intn(3))
		pay := uint64(0)
		if v > 0 {
			pay = rateFee
		}
		switch rng.Intn(10) {
		case 0:
			pay++
		case 1:
			if pay > 0 {
				pay--
			}
		}
		who := voters[vi]
		cur := model[[2]int{vi, ti}]
		want := v != cur && pay == map[bool]uint64{true: rateFee, false: 0}[v > 0]
		if rng.Intn(25) == 0 {
			who, want = nil, false // SIGNER() is 33 zero bytes above ring 2
		}
		r := rateVote(c, scid, who, targets[ti], v, pay)
		if r.OK() != want {
			t.Fatalf("step %d: voter %d target %d vote %d (standing %d) paying %d: accepted %v, want %v (%v)", i, vi, ti, v, cur, pay, r.OK(), want, r.Err)
		}
		if r.OK() {
			accepted++
			model[[2]int{vi, ti}] = v
			if v > 0 {
				votes++
			}
			paid += r.Sent(zero, treasury)
		} else {
			refused++
		}
		for tj, tg := range targets {
			var nb, ns uint64
			for vj, w := range voters {
				m := model[[2]int{vj, tj}]
				switch m {
				case 1:
					nb++
				case 2:
					ns++
				}
				_, _, x := rateKeys(tg, w)
				got := c.Var(scid, x)
				if (m == 0 && got != nil) || (m != 0 && (got == nil || got.(uint64) != m)) {
					t.Fatalf("step %d: voter %d target %d: stored vote %v, the model says %d", i, vj, tj, got, m)
				}
			}
			b, s, _ := rateKeys(tg, owner)
			if rateU(c, scid, b) != nb || rateU(c, scid, s) != ns {
				t.Fatalf("step %d: target %d counts bullish %d suspicious %d, the standing votes are %d and %d", i, tj, rateU(c, scid, b), rateU(c, scid, s), nb, ns)
			}
		}
		if rateU(c, scid, "n") != accepted {
			t.Fatalf("step %d: n = %d, %d calls were accepted", i, rateU(c, scid, "n"), accepted)
		}
		if c.SCBalance(scid, zero) != 0 {
			t.Fatalf("step %d: the contract holds %d DERO", i, c.SCBalance(scid, zero))
		}
		if i%7 == 0 {
			c.NextBlock()
		}
	}
	if paid != votes*rateFee {
		t.Fatalf("the treasury received %d for %d votes at %d", paid, votes, rateFee)
	}
	if refused < 300 || votes < 300 {
		t.Fatalf("the sequence exercised too little: %d refused, %d votes", refused, votes)
	}
	t.Logf("%d calls: %d accepted (%d votes paying %d to the treasury), %d refused", accepted+refused, accepted, votes, paid, refused)
}

// Change and withdraw, worked by hand: a vote moves between the two counters, a withdrawal takes it out of both, and
// another target is untouched.
func TestRateChangeAndWithdraw(t *testing.T) {
	c := harness.New()
	owner, treasury, a, b := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid, _ := installRate(t, c, owner, treasury, rateFee)
	tg, other := rateTarget("market"), rateTarget("another market")
	bk, sk, _ := rateKeys(tg, a)
	step := func(who *rpc.Address, v uint64, wantB, wantS uint64) {
		t.Helper()
		pay := uint64(0)
		if v > 0 {
			pay = rateFee
		}
		r := rateVote(c, scid, who, tg, v, pay)
		if !r.OK() {
			t.Fatalf("vote %d: %v", v, r.Err)
		}
		if got := r.Sent(zero, treasury); got != pay {
			t.Fatalf("vote %d sent the treasury %d, want %d", v, got, pay)
		}
		if rateU(c, scid, bk) != wantB || rateU(c, scid, sk) != wantS {
			t.Fatalf("after vote %d: bullish %d suspicious %d, want %d and %d", v, rateU(c, scid, bk), rateU(c, scid, sk), wantB, wantS)
		}
		_, _, x := rateKeys(tg, who)
		if got := c.Var(scid, x); (v == 0 && got != nil) || (v != 0 && got != v) {
			t.Fatalf("after vote %d the voter's key holds %v", v, got)
		}
	}
	step(a, 1, 1, 0) // bullish
	step(b, 2, 1, 1) // suspicious
	step(a, 2, 0, 2) // a changes its mind, and pays again
	step(a, 0, 0, 1) // a withdraws, for nothing
	step(a, 1, 1, 1) // and votes again
	step(b, 0, 1, 0) // b withdraws
	if rateU(c, scid, "n") != 6 {
		t.Fatalf("n = %d after six accepted calls", rateU(c, scid, "n"))
	}
	ob, osk, _ := rateKeys(other, a)
	if c.Exists(scid, ob) || c.Exists(scid, osk) {
		t.Fatal("a vote on one target wrote another target's counters")
	}
}

// Every refusal, each leaving every counter, every standing vote and n exactly as they were.
func TestRateRefusals(t *testing.T) {
	c := harness.New()
	owner, treasury, a, b := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid, _ := installRate(t, c, owner, treasury, rateFee)
	tg := rateTarget("market")
	if r := rateVote(c, scid, a, tg, 1, rateFee); !r.OK() {
		t.Fatalf("setup vote: %v", r.Err)
	}
	targets, voters := []crypto.Hash{tg}, []*rpc.Address{a, b, owner}
	before := rateSnapshot(c, scid, targets, voters)
	raw := func(who *rpc.Address, target string, v, pay uint64) harness.Result {
		var in map[crypto.Hash]uint64
		if pay > 0 {
			in = map[crypto.Hash]uint64{zero: pay}
		}
		return c.Call(scid, "Vote", rpc.Arguments{harness.S("target", target), harness.U("v", v)}, in, who)
	}
	hexText := hex.EncodeToString(tg[:])
	cases := []struct {
		why string
		r   func() harness.Result
	}{
		{"a ring > 2 call, whose SIGNER() is blank", func() harness.Result { return rateVote(c, scid, nil, tg, 2, rateFee) }},
		{"a target of 31 bytes", func() harness.Result { return raw(b, strings.Repeat("t", 31), 1, rateFee) }},
		{"a target of 33 bytes", func() harness.Result { return raw(b, strings.Repeat("t", 33), 1, rateFee) }},
		{"the target's 64-character hex text instead of its 32 bytes", func() harness.Result { return raw(b, hexText, 1, rateFee) }},
		{"an empty target", func() harness.Result { return raw(b, "", 1, rateFee) }},
		{"v = 3", func() harness.Result { return rateVote(c, scid, b, tg, 3, rateFee) }},
		{"v = 2^64 - 1", func() harness.Result { return rateVote(c, scid, b, tg, ^uint64(0), rateFee) }},
		{"a vote paying nothing", func() harness.Result { return rateVote(c, scid, b, tg, 1, 0) }},
		{"a vote paying one less than the fee", func() harness.Result { return rateVote(c, scid, b, tg, 2, rateFee-1) }},
		{"a vote paying one more than the fee", func() harness.Result { return rateVote(c, scid, b, tg, 2, rateFee+1) }},
		{"the same vote again", func() harness.Result { return rateVote(c, scid, a, tg, 1, rateFee) }},
		{"a withdrawal with no vote standing", func() harness.Result { return rateVote(c, scid, b, tg, 0, 0) }},
		{"a withdrawal carrying DERO", func() harness.Result { return rateVote(c, scid, a, tg, 0, 1) }},
		{"a withdrawal carrying the fee", func() harness.Result { return rateVote(c, scid, a, tg, 0, rateFee) }},
		{"InitializePrivate again, by the installer", func() harness.Result {
			return c.Call(scid, "InitializePrivate", rpc.Arguments{harness.S("treasury", b.String()), harness.U("fee", 0)}, nil, owner)
		}},
		{"InitializePrivate again, by a stranger", func() harness.Result {
			return c.Call(scid, "InitializePrivate", rpc.Arguments{harness.S("treasury", b.String()), harness.U("fee", 0)}, nil, b)
		}},
		{"an internal function called as an entrypoint", func() harness.Result {
			return c.Call(scid, "ck", rpc.Arguments{harness.S("t", hexText), harness.U("c", 1)}, nil, b)
		}},
	}
	for _, tc := range cases {
		if r := tc.r(); r.OK() {
			t.Errorf("%s was accepted", tc.why)
		}
		if after := rateSnapshot(c, scid, targets, voters); after != before {
			t.Fatalf("%s changed the state:\n%s\n%s", tc.why, before, after)
		}
	}
	// Nobody can change or remove another's vote: a caller's key is built from its own SIGNER(), so b's calls, every
	// one of them accepted, write b's vote and never a's.
	_, _, ax := rateKeys(tg, a)
	for _, v := range []uint64{2, 1, 0} {
		pay := uint64(0)
		if v > 0 {
			pay = rateFee
		}
		if r := rateVote(c, scid, b, tg, v, pay); !r.OK() {
			t.Fatalf("b's vote %d: %v", v, r.Err)
		}
		if c.U(scid, ax) != 1 {
			t.Fatalf("b's vote %d changed a's standing vote to %v", v, c.Var(scid, ax))
		}
	}
	if c.S(scid, "treasury") != string(treasury.Compressed()) || c.U(scid, "fee") != rateFee {
		t.Fatal("the treasury or the fee moved")
	}
	// install-time refusals
	if _, res := c.Install(readContract(t, "rate.bas"), nil, rpc.Arguments{harness.S("treasury", "not an address"), harness.U("fee", rateFee)}, owner, harness.MaxStorageGas); res.OK() {
		t.Error("an install naming no valid treasury was accepted")
	}
	if _, res := c.Install(readContract(t, "rate.bas"), map[crypto.Hash]uint64{zero: 5}, rpc.Arguments{harness.S("treasury", treasury.String()), harness.U("fee", rateFee)}, owner, harness.MaxStorageGas); res.OK() {
		t.Error("an install carrying DERO was accepted: nothing could ever take it out")
	}
}

// The fee is exact, reaches the treasury in the same call and never stays in the contract; a withdrawal pays none;
// and a contract installed with fee 0 takes nothing at all.
func TestRateFeeIsExactAndPaid(t *testing.T) {
	c := harness.New()
	owner, treasury, a := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid, _ := installRate(t, c, owner, treasury, rateFee)
	tg := rateTarget("market")
	for _, v := range []uint64{1, 2} {
		r := rateVote(c, scid, a, tg, v, rateFee)
		if !r.OK() || r.Sent(zero, treasury) != rateFee || len(r.Transfers) != 1 {
			t.Fatalf("vote %d: ok %v, the treasury received %d in %d transfers", v, r.OK(), r.Sent(zero, treasury), len(r.Transfers))
		}
		if c.SCBalance(scid, zero) != 0 {
			t.Fatalf("the contract kept %d", c.SCBalance(scid, zero))
		}
	}
	if r := rateVote(c, scid, a, tg, 0, 0); !r.OK() || len(r.Transfers) != 0 {
		t.Fatalf("withdraw: ok %v, %d transfers", r.OK(), len(r.Transfers))
	}
	free, _ := installRate(t, c, owner, treasury, 0)
	if r := rateVote(c, free, a, tg, 1, 1); r.OK() {
		t.Fatal("a contract whose fee is 0 accepted DERO with a vote")
	}
	if r := rateVote(c, free, a, tg, 1, 0); !r.OK() || len(r.Transfers) != 0 {
		t.Fatalf("a free vote: ok %v, %d transfers (%v)", r.OK(), len(r.Transfers), r.Err)
	}
}

// Install and per-call gas. Every call touches a fixed set of keys, so its cost cannot grow with the number of voters
// or targets: the 2nd and the 200th voter on one target pay the same compute gas.
func TestRateGas(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	scid, res := installRate(t, c, owner, treasury, rateFee)
	t.Logf("rate.bas install at fee %d: storage gas %d of %d, compute gas %d", rateFee, res.GasStorage, harness.MaxStorageGas, res.GasCompute)
	_, res0 := installRate(t, c, owner, treasury, 0)
	t.Logf("rate.bas install at fee 0: storage gas %d, compute gas %d", res0.GasStorage, res0.GasCompute)
	tg := rateTarget("market")
	measure := func(what string, who *rpc.Address, v uint64) harness.Result {
		t.Helper()
		pay := uint64(0)
		if v > 0 {
			pay = rateFee
		}
		r := rateVote(c, scid, who, tg, v, pay)
		if !r.OK() {
			t.Fatalf("%s: %v", what, r.Err)
		}
		t.Logf("%-38s compute %8d storage %4d", what, r.GasCompute, r.GasStorage)
		return r
	}
	first := c.NewAccount()
	measure("first vote on a target", first, 1)
	second := measure("a vote on a target that has one", c.NewAccount(), 1)
	for i := 0; i < 197; i++ {
		if r := rateVote(c, scid, c.NewAccount(), tg, uint64(1+i%2), rateFee); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	late := measure("the 200th voter's vote", c.NewAccount(), 1)
	if late.GasCompute != second.GasCompute || late.GasStorage > second.GasStorage+5 {
		t.Fatalf("the 200th vote costs compute %d storage %d, the 2nd %d and %d: the cost grew with the voters", late.GasCompute, late.GasStorage, second.GasCompute, second.GasStorage)
	}
	measure("a change of mind", first, 2)
	measure("a withdrawal", first, 0)
}

// The contract exposes exactly InitializePrivate and Vote; nothing else a caller can reach writes anything.
func TestRateEntrypoints(t *testing.T) {
	sc, _, err := dvm.ParseSmartContract(readContract(t, "rate.bas"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for fn := range sc.Functions {
		if fn[0] >= 'A' && fn[0] <= 'Z' {
			got = append(got, fn)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "InitializePrivate,Vote" {
		t.Fatalf("entrypoints %v", got)
	}
	// no update path, and the treasury and the fee are each written once, by InitializePrivate
	src := readContract(t, "rate.bas")
	for s, want := range map[string]int{"UPDATE_SC_CODE": 0, "UpdateCode": 0, `STORE("treasury"`: 1, `STORE("fee"`: 1, "SIGNER()": 1} {
		if n := strings.Count(src, s); n != want {
			t.Fatalf("%q appears %d times, want %d", s, n, want)
		}
	}
}

// The template the app ships is the tested contract, and its hash is the pinned one.
func TestRateCanonicalHash(t *testing.T) {
	src := readContract(t, "rate.bas")
	h := sha256.Sum256([]byte(src))
	got := hex.EncodeToString(h[:])
	t.Logf("rate.bas canonical sha256 %s (%d installed bytes)", got, len(src))
	if got != rateCanonSha256 {
		t.Errorf("rate.bas canonical sha256 is %s, pinned %s: a changed contract is a new contract, re-pin it deliberately", got, rateCanonSha256)
	}
	tpl, err := os.ReadFile(filepath.Join(repoRoot(t), "app", "templates", "rate.bas"))
	if err != nil {
		t.Fatal(err)
	}
	if string(tpl) != ref.Canon(src) {
		t.Error("app/templates/rate.bas is not Canon(contracts/rate.bas): run tools/simdeploy")
	}
}
