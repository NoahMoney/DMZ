package tests

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// C4: conservation of the wrapped supply under random operation sequences. The model does not predict policy
// (cap, halt, quorum); it observes what landed (md_<id> after the k-th vote, r.OK() on a burn) and checks that
// sup == Σ minted − Σ burned, feepool == Σ wfee, every withdrawal record equals its burn minus the fee, and the
// contract's DERO balance equals the bonds it holds, after every step.
func TestVaultConservation(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	sg := []*rpc.Address{c.NewAccount(), c.NewAccount(), c.NewAccount()}
	users := []*rpc.Address{c.NewAccount(), c.NewAccount()}
	code := readContract(t, "vault.bas")
	const wfee, minburn = 10, 100
	scid, res := c.Install(code, nil, rpc.Arguments{harness.U("k", 2), harness.S("treasury", treasury.String()), harness.U("cap", 1000000), harness.U("minbond", 1000), harness.U("minburn", minburn), harness.U("wfee", wfee)}, owner, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	for _, s := range sg {
		if r := c.Call(scid, "AddSigner", rpc.Arguments{harness.S("addr", s.String())}, nil, owner); !r.OK() {
			t.Fatalf("add signer: %v", r.Err)
		}
		if r := c.Call(scid, "Bond", nil, map[crypto.Hash]uint64{zero: 1000000}, s); !r.OK() {
			t.Fatalf("bond: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() {
		t.Fatalf("seal: %v", r.Err)
	}
	for j, u := range users {
		if r := c.Call(scid, "Register", nil, nil, u); !r.OK() {
			t.Fatalf("register: %v", r.Err)
		}
		for i := 0; i < 2; i++ {
			if r := c.Call(scid, "Attest", rpc.Arguments{harness.U("j", uint64(j)), harness.S("addr", fmt.Sprintf("zs1user%d", j))}, nil, sg[i]); !r.OK() {
				t.Fatalf("attest: %v", r.Err)
			}
		}
	}
	rng := rand.New(rand.NewSource(21))
	var minted, burned, fees uint64
	bal := map[int]uint64{}
	withdrawals := []uint64{} // net amount of every withdrawal record, by index
	mints, burns, refusedMints, refusedBurns, halts := 0, 0, 0, 0, 0
	halted := false
	check := func(step int) {
		t.Helper()
		if sup := c.U(scid, "sup"); sup != minted-burned {
			t.Fatalf("step %d: sup %d != minted %d - burned %d", step, sup, minted, burned)
		}
		if fp := c.U(scid, "feepool"); fp != fees {
			t.Fatalf("step %d: feepool %d != %d", step, fp, fees)
		}
		if wn := c.U(scid, "wn"); wn != uint64(len(withdrawals)) {
			t.Fatalf("step %d: wn %d != %d", step, wn, len(withdrawals))
		}
		for j, v := range withdrawals {
			if got := c.U(scid, fmt.Sprintf("w_%d_v", j)); got != v {
				t.Fatalf("step %d: withdrawal %d records %d want %d", step, j, got, v)
			}
		}
		if b := c.SCBalance(scid, zero); b != 3000000 {
			t.Fatalf("step %d: vault DERO balance %d != bonds 3000000", step, b)
		}
		if sup := c.U(scid, "sup"); sup > c.U(scid, "cap") {
			t.Fatalf("step %d: sup %d above cap %d", step, sup, c.U(scid, "cap"))
		}
	}
	vote2 := func(entry string, args rpc.Arguments) (harness.Result, harness.Result) {
		a := c.Call(scid, entry, args, nil, sg[rng.Intn(3)])
		var b harness.Result
		for {
			s := sg[rng.Intn(3)]
			b = c.Call(scid, entry, args, nil, s)
			if b.OK() || !a.OK() {
				break
			}
			// the same signer voting twice is refused: try another
			if r := c.Call(scid, entry, args, nil, sg[(rng.Intn(3)+1)%3]); r.OK() {
				b = r
				break
			}
		}
		return a, b
	}
	for step := 0; step < 600; step++ {
		if rng.Intn(4) == 0 {
			c.NextBlock()
		}
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // a deposit seen by the federation: two agreeing votes, sometimes a wrong one first
			j := rng.Intn(len(users))
			id := fmt.Sprintf("%064x", rng.Uint64())
			amount := uint64(1 + rng.Intn(5000))
			if rng.Intn(5) == 0 {
				c.Call(scid, "Mint", rpc.Arguments{harness.U("j", uint64(j)), harness.S("id", id), harness.U("amount", amount+1)}, nil, sg[rng.Intn(3)]) // a lying signer
			}
			before := c.Exists(scid, "md_"+id)
			a := c.Call(scid, "Mint", rpc.Arguments{harness.U("j", uint64(j)), harness.S("id", id), harness.U("amount", amount)}, nil, sg[0])
			b := c.Call(scid, "Mint", rpc.Arguments{harness.U("j", uint64(j)), harness.S("id", id), harness.U("amount", amount)}, nil, sg[1])
			if !before && c.Exists(scid, "md_"+id) {
				minted += amount
				bal[j] += amount
				mints++
				if got := a.Sent(scid, users[j]) + b.Sent(scid, users[j]); got != amount {
					t.Fatalf("step %d: minted %d reached the user as %d", step, amount, got)
				}
			} else {
				refusedMints++
			}
		case 4, 5, 6: // a user burns to withdraw
			j := rng.Intn(len(users))
			if bal[j] < minburn {
				continue
			}
			v := uint64(minburn) + uint64(rng.Intn(int(bal[j]-minburn+1)))
			if rng.Intn(6) == 0 {
				v = uint64(rng.Intn(minburn)) // below the minimum: must be refused and refunded
			}
			r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDest"+strings.Repeat("x", 10))}, map[crypto.Hash]uint64{scid: v}, users[j])
			if r.OK() {
				burned += v
				bal[j] -= v
				fees += wfee
				withdrawals = append(withdrawals, v-wfee)
				burns++
			} else {
				refusedBurns++
				if v >= minburn && !halted {
					t.Fatalf("step %d: a valid burn of %d was refused: %v", step, v, r.Err)
				}
			}
		case 7: // signers release a pending withdrawal
			for j := range withdrawals {
				if c.U(scid, fmt.Sprintf("w_%d_st", j)) == 0 {
					vote2("Released", rpc.Arguments{harness.U("j", uint64(j)), harness.S("ftx", fmt.Sprintf("%064x", rng.Uint64()))})
					break
				}
			}
		case 8: // halt or resume
			want := uint64(1)
			if halted {
				want = 0
			}
			_, b := vote2("Halt", rpc.Arguments{harness.U("want", want)})
			if b.OK() && c.U(scid, "halted") == want {
				halted = want == 1
				halts++
			}
		case 9: // cap moves up or down (never below the supply: the contract refuses otherwise or mints refuse later)
			cap := uint64(500000 + rng.Intn(2000000))
			vote2("SetCap", rpc.Arguments{harness.U("cap", cap)})
		}
		check(step)
	}
	t.Logf("600 steps: %d mints (%d refused), %d burns (%d refused), %d halt flips; supply %d, fees %d, withdrawals %d, cap %d", mints, refusedMints, burns, refusedBurns, halts, c.U(scid, "sup"), fees, len(withdrawals), c.U(scid, "cap"))
	if mints < 50 || burns < 20 {
		t.Fatalf("the sequence did not exercise enough: %d mints %d burns", mints, burns)
	}
}
