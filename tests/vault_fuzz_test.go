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

// Conservation of the wrapped supply under random operation sequences. The model does not predict policy (cap,
// halt, quorum); it observes what landed and checks the v3 accounting after every step:
//
//	sup      == Σ minted − Σ released net amounts     (a burn holds its claim until the payment is recorded)
//	feepool  == Σ released fees                       (vetoed withdrawals return the fee with the units)
//	every withdrawal record equals its burn minus the fee, and the contract's DERO balance equals the bonds.
func TestVaultConservation(t *testing.T) {
	c := harness.New()
	owner, treasury := c.NewAccount(), c.NewAccount()
	users := []*rpc.Address{c.NewAccount(), c.NewAccount()}
	const wfee, minburn, bond = 10, 100, 1000000
	cfg := vaultCfg{k: 2, cap: 1000000, minbond: bond, minburn: minburn, wfee: wfee, mindep: 1, regfee: 0, signers: 3}
	scid, sg := installVault(t, c, owner, treasury, cfg)
	if r := c.Call(scid, "Seal", nil, nil, owner); !r.OK() {
		t.Fatalf("seal: %v", r.Err)
	}
	for j, u := range users {
		if r := c.Call(scid, "Register", nil, nil, u); !r.OK() {
			t.Fatalf("register: %v", r.Err)
		}
		for i := 0; i < 2; i++ {
			if r := c.Call(scid, "Attest", rpc.Arguments{harness.U("j", uint64(j)), harness.S("addr", fmt.Sprintf("4Axmr-user-%d", j))}, nil, sg[i]); !r.OK() {
				t.Fatalf("attest: %v", r.Err)
			}
		}
	}
	rng := rand.New(rand.NewSource(21))
	var minted, releasedNet, releasedFees uint64
	bal := map[int]uint64{}
	type wd struct {
		net, fee uint64
		st       uint64 // 0 pending, 1 released, 2 vetoed
		owner    int
	}
	var ws []*wd
	mints, burns, rels, vetos, refusedMints, refusedBurns, halts := 0, 0, 0, 0, 0, 0, 0
	halted := false
	check := func(step int) {
		t.Helper()
		if sup := c.U(scid, "sup"); sup != minted-releasedNet {
			t.Fatalf("step %d: sup %d != minted %d - released %d", step, sup, minted, releasedNet)
		}
		if fp := c.U(scid, "feepool"); fp != releasedFees {
			t.Fatalf("step %d: feepool %d != %d", step, fp, releasedFees)
		}
		if wn := c.U(scid, "wn"); wn != uint64(len(ws)) {
			t.Fatalf("step %d: wn %d != %d", step, wn, len(ws))
		}
		for j, w := range ws {
			if got := c.U(scid, fmt.Sprintf("w_%d_v", j)); got != w.net {
				t.Fatalf("step %d: withdrawal %d records %d want %d", step, j, got, w.net)
			}
			if got := c.U(scid, fmt.Sprintf("w_%d_st", j)); got != w.st {
				t.Fatalf("step %d: withdrawal %d state %d want %d", step, j, got, w.st)
			}
		}
		if b := c.SCBalance(scid, zero); b != 3*bond {
			t.Fatalf("step %d: vault DERO balance %d != bonds %d", step, b, 3*bond)
		}
		if sup := c.U(scid, "sup"); sup > c.U(scid, "cap") {
			t.Fatalf("step %d: sup %d above cap %d", step, sup, c.U(scid, "cap"))
		}
	}
	vote2 := func(entry string, args rpc.Arguments) (harness.Result, harness.Result) {
		a := c.Call(scid, entry, args, nil, sg[rng.Intn(3)])
		var b harness.Result
		for {
			b = c.Call(scid, entry, args, nil, sg[rng.Intn(3)])
			if b.OK() || !a.OK() {
				break
			}
			if r := c.Call(scid, entry, args, nil, sg[(rng.Intn(3)+1)%3]); r.OK() {
				b = r
				break
			}
		}
		return a, b
	}
	pending := func() int {
		for j, w := range ws {
			if w.st == 0 {
				return j
			}
		}
		return -1
	}
	for step := 0; step < 600; step++ {
		if rng.Intn(4) == 0 {
			c.NextBlock()
		}
		switch rng.Intn(11) {
		case 0, 1, 2, 3: // a deposit seen by the federation: two agreeing votes, sometimes a wrong one first
			j := rng.Intn(len(users))
			n := step*4 + rng.Intn(4)
			amount := uint64(1 + rng.Intn(5000))
			if rng.Intn(5) == 0 { // a lying signer votes another tuple first, then may correct it
				c.Call(scid, "Mint", mintArgs(uint64(j), n, 0, amount+1), nil, sg[rng.Intn(3)])
			}
			before := c.Exists(scid, "mint_"+depID(n, 0))
			a := c.Call(scid, "Mint", mintArgs(uint64(j), n, 0, amount), nil, sg[0])
			b := c.Call(scid, "Mint", mintArgs(uint64(j), n, 0, amount), nil, sg[1])
			if !before && c.Exists(scid, "mint_"+depID(n, 0)) {
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
			r := c.Call(scid, "Burn", rpc.Arguments{harness.S("dest", "4AxmrDest"+strings.Repeat("x", 10)), harness.S("refund", users[j].String())}, map[crypto.Hash]uint64{scid: v}, users[j])
			if r.OK() {
				bal[j] -= v
				ws = append(ws, &wd{net: v - wfee, fee: wfee, owner: j})
				burns++
			} else {
				refusedBurns++
				if v >= minburn {
					t.Fatalf("step %d: a valid burn of %d was refused: %v", step, v, r.Err)
				}
			}
		case 7: // signers release a pending withdrawal
			if j := pending(); j >= 0 {
				_, b := vote2("Released", rpc.Arguments{harness.U("j", uint64(j)), harness.S("ftx", fmt.Sprintf("ftx-%d-%d", step, j))})
				if b.OK() && c.U(scid, fmt.Sprintf("w_%d_st", j)) == 1 {
					ws[j].st = 1
					releasedNet += ws[j].net
					releasedFees += ws[j].fee
					rels++
				}
			}
		case 8: // signers veto a pending withdrawal: the units go back to whoever burned them
			if j := pending(); j >= 0 {
				_, b := vote2("Veto", rpc.Arguments{harness.U("j", uint64(j))})
				if b.OK() && c.U(scid, fmt.Sprintf("w_%d_st", j)) == 2 {
					ws[j].st = 2
					bal[ws[j].owner] += ws[j].net + ws[j].fee
					vetos++
				}
			}
		case 9: // halt or resume
			want := uint64(1)
			if halted {
				want = 0
			}
			_, b := vote2("Halt", rpc.Arguments{harness.U("want", want)})
			if b.OK() && (c.U(scid, "halted") > c.Height) == (want == 1) {
				halted = want == 1
				halts++
			}
		case 10: // the cap moves
			vote2("SetCap", rpc.Arguments{harness.U("cap", uint64(500000+rng.Intn(2000000)))})
		}
		check(step)
	}
	t.Logf("600 steps: %d mints (%d refused), %d burns (%d refused), %d releases, %d vetoes, %d halt flips; sup %d, fees %d, withdrawals %d, cap %d",
		mints, refusedMints, burns, refusedBurns, rels, vetos, halts, c.U(scid, "sup"), releasedFees, len(ws), c.U(scid, "cap"))
	if mints < 50 || burns < 20 || rels < 5 || vetos < 3 {
		t.Fatalf("the sequence did not exercise enough: %d mints %d burns %d releases %d vetoes", mints, burns, rels, vetos)
	}
}
