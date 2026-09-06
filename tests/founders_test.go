package tests

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

const two32 = uint64(1) << 32

func installFounders(t *testing.T, c *harness.Chain, creator, treasury *rpc.Address, price, max, start, reserve, share, mfee uint64) crypto.Hash {
	t.Helper()
	code := readContract(t, "founders.bas")
	scid, res := c.Install(code, nil, rpc.Arguments{harness.S("name", "Veil Founders Pass"), harness.S("descr", "a share of every fee"), harness.S("icon", ""), harness.S("file", ""),
		harness.U("price", price), harness.U("maxsupply", max), harness.U("start", start), harness.U("reserve", reserve), harness.U("share", share), harness.U("mfee", mfee), harness.S("treasury", treasury.String())}, creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	t.Logf("founders.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	return scid
}

// contract DERO == rpool + und, hst == sum of registered units, registry indices consistent
func checkFounders(t *testing.T, c *harness.Chain, scid crypto.Hash) {
	t.Helper()
	if c.SCBalance(scid, zero) != c.U(scid, "rpool")+c.U(scid, "und") {
		t.Fatalf("DERO invariant: balance %d rpool %d und %d", c.SCBalance(scid, zero), c.U(scid, "rpool"), c.U(scid, "und"))
	}
	listed := uint64(0)
	for i := uint64(0); i < c.U(scid, "ln"); i++ {
		if c.Exists(scid, "lu_"+itoa(i)) {
			listed += c.U(scid, "lu_"+itoa(i))
		}
	}
	_ = listed // derohe keeps no balance for a contract's own asset (it can mint it), so units are checked through the registry
	sum := uint64(0)
	for i := uint64(0); i < c.U(scid, "hn"); i++ {
		a := c.S(scid, "h_"+itoa(i))
		if c.U(scid, "hi_"+a) != i+1 {
			t.Fatalf("registry index broken at %d", i)
		}
		sum += c.U(scid, "hu_"+a)
	}
	if sum != c.U(scid, "hst") {
		t.Fatalf("hst %d != sum of hu %d", c.U(scid, "hst"), sum)
	}
}

func itoa(i uint64) string { return big.NewInt(int64(i)).String() }

func TestFoundersMintRegisterRewards(t *testing.T) {
	c := harness.New()
	creator, treasury, alice, bob, carol := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 500000, 100, 5, 10, 5000, 250)
	if c.U(scid, "minted") != 10 || c.SCBalance(scid, scid) != 0 {
		t.Fatal("reserve not minted to creator")
	}
	// mint before start → refused; wrong payment → refused; exact → passes to buyer, DERO to treasury
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 2)}, map[crypto.Hash]uint64{zero: 1000000}, alice); r.OK() {
		t.Fatal("mint before start accepted")
	}
	for c.Height < 5 {
		c.NextBlock()
	}
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 2)}, map[crypto.Hash]uint64{zero: 1000001}, alice); r.OK() {
		t.Fatal("overpaid mint accepted")
	}
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 91)}, map[crypto.Hash]uint64{zero: 91 * 500000}, alice); r.OK() {
		t.Fatal("mint above maxsupply accepted")
	}
	r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 2)}, map[crypto.Hash]uint64{zero: 1000000}, alice)
	if !r.OK() || r.Sent(scid, alice) != 2 || r.Sent(zero, treasury) != 1000000 || c.U(scid, "minted") != 12 {
		t.Fatalf("mint: %v", r.Err)
	}
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 3)}, map[crypto.Hash]uint64{zero: 1500000}, bob); !r.OK() {
		t.Fatalf("bob mint: %v", r.Err)
	}
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 1)}, map[crypto.Hash]uint64{zero: 500000}, nil); r.OK() {
		t.Fatal("anonymous mint accepted")
	}
	// a deposit before anyone registers waits in und
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 700000}, nil); !r.OK() || c.U(scid, "und") != 700000 || c.U(scid, "dep") != 700000 {
		t.Fatalf("deposit: %v und=%d", r.Err, c.U(scid, "und"))
	}
	checkFounders(t, c, scid)
	// alice registers 2: below minhst (1 + 100/50 = 3) the waiting DERO stays undistributed, so the first
	// registrant of a single unit can never sweep what accrued before anyone held the pass (C9)
	if c.U(scid, "minhst") != 3 {
		t.Fatalf("minhst %d, want 3", c.U(scid, "minhst"))
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 2}, alice); !r.OK() || c.U(scid, "hst") != 2 || c.U(scid, "hn") != 1 {
		t.Fatalf("register alice: %v", r.Err)
	}
	if c.U(scid, "und") != 700000 || c.U(scid, "rpool") != 0 {
		t.Fatalf("und released below minhst: und %d rpool %d", c.U(scid, "und"), c.U(scid, "rpool"))
	}
	if r := c.Call(scid, "Distribute", nil, nil, nil); c.U(scid, "und") != 700000 {
		t.Fatalf("Distribute released below minhst: %v und %d", r.Err, c.U(scid, "und"))
	}
	// bob registers 3 → hst 5 ≥ minhst: the 700000 is released to both, 2/5 and 3/5
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 3}, bob); !r.OK() || c.U(scid, "hst") != 5 || c.U(scid, "hn") != 2 {
		t.Fatalf("register bob: %v", r.Err)
	}
	if c.U(scid, "und") != 0 || c.U(scid, "rpool") != 700000 {
		t.Fatalf("und not released at minhst: und %d rpool %d", c.U(scid, "und"), c.U(scid, "rpool"))
	}
	checkFounders(t, c, scid)
	// alice claims her 2/5 of the 700000
	r = c.Call(scid, "Claim", nil, nil, alice)
	if !r.OK() || r.Sent(zero, alice) != 280000 || c.U(scid, "rpool") != 420000 || c.U(scid, "paid") != 280000 {
		t.Fatalf("claim: %v got %d", r.Err, r.Sent(zero, alice))
	}
	// deposit 1 DERO → alice 2/5, bob 3/5; PayBatch pushes both (bob also gets his 420000 from before)
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 100000}, treasury); !r.OK() {
		t.Fatalf("deposit2: %v", r.Err)
	}
	r = c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, nil)
	if !r.OK() || r.Sent(zero, alice) != 40000 || r.Sent(zero, bob) != 480000 {
		t.Fatalf("paybatch: %v alice %d bob %d", r.Err, r.Sent(zero, alice), r.Sent(zero, bob))
	}
	t.Logf("PayBatch of 2 holders: compute gas %d", r.GasCompute)
	if c.U(scid, "rpool") != 0 || c.U(scid, "paid") != 800000 {
		t.Fatalf("after paybatch rpool %d paid %d", c.U(scid, "rpool"), c.U(scid, "paid"))
	}
	// withdraw: bob takes 3 back (registry compacts), then a deposit is alice's alone
	r = c.Call(scid, "Withdraw", rpc.Arguments{harness.U("u", 3)}, nil, bob)
	if !r.OK() || r.Sent(scid, bob) != 3 || c.U(scid, "hn") != 1 || c.U(scid, "hst") != 2 || c.Exists(scid, "hi_"+bob.String()) {
		t.Fatalf("withdraw: %v", r.Err)
	}
	if r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("u", 1)}, nil, bob); r.OK() {
		t.Fatal("withdraw by non-holder accepted")
	}
	// bob withdrew: hst is 2 again, below minhst, so a deposit now waits in und instead of being alice's alone
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 100001}, carol); !r.OK() {
		t.Fatalf("deposit3: %v", r.Err)
	}
	r = c.Call(scid, "Claim", nil, nil, alice)
	if !r.OK() || r.Sent(zero, alice) != 0 || c.U(scid, "und") != 100001 {
		t.Fatalf("claim3: %v got %d und %d (below minhst nothing is released)", r.Err, r.Sent(zero, alice), c.U(scid, "und"))
	}
	checkFounders(t, c, scid)
	// marketplace: bob lists 3 at 2 DERO; carol buys 2 (fee 2.5% → holders), bob unlists the rest; strangers cannot unlist
	if r := c.Call(scid, "List", rpc.Arguments{harness.U("price", 200000)}, map[crypto.Hash]uint64{scid: 3}, bob); !r.OK() || c.U(scid, "la") != 1 || c.U(scid, "lu_0") != 3 {
		t.Fatalf("list: %v", r.Err)
	}
	if r := c.Call(scid, "Buy", rpc.Arguments{harness.U("id", 0), harness.U("n", 2)}, map[crypto.Hash]uint64{zero: 399999}, carol); r.OK() {
		t.Fatal("underpaid buy accepted")
	}
	r = c.Call(scid, "Buy", rpc.Arguments{harness.U("id", 0), harness.U("n", 2)}, map[crypto.Hash]uint64{zero: 400000}, carol)
	if !r.OK() || r.Sent(scid, carol) != 2 || r.Sent(zero, bob) != 390000 || c.U(scid, "lu_0") != 1 || c.U(scid, "mdep") != 10000 {
		t.Fatalf("buy: %v units %d seller %d", r.Err, r.Sent(scid, carol), r.Sent(zero, bob))
	}
	if r := c.Call(scid, "Unlist", rpc.Arguments{harness.U("id", 0)}, nil, carol); r.OK() {
		t.Fatal("unlist by stranger accepted")
	}
	if r := c.Call(scid, "Unlist", rpc.Arguments{harness.U("id", 0)}, nil, bob); !r.OK() || r.Sent(scid, bob) != 1 || c.U(scid, "la") != 0 || c.Exists(scid, "ls_0") {
		t.Fatalf("unlist: %v", r.Err)
	}
	// the 10000 marketplace fee also waited (hst 2 < minhst 3): und = 110001. Carol registers the 2 passes she bought →
	// hst 4 releases everything, 2/4 to alice and 2/4 to carol (one unit of dust stays in rpool)
	if c.U(scid, "und") != 110001 {
		t.Fatalf("und %d, want 110001 waiting below minhst", c.U(scid, "und"))
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 2}, carol); !r.OK() || c.U(scid, "hst") != 4 || c.U(scid, "und") != 0 {
		t.Fatalf("register carol: %v hst %d und %d", r.Err, c.U(scid, "hst"), c.U(scid, "und"))
	}
	r = c.Call(scid, "Claim", nil, nil, alice)
	if !r.OK() || r.Sent(zero, alice) != 55000 {
		t.Fatalf("fee claim: %v got %d, want 55000", r.Err, r.Sent(zero, alice))
	}
	checkFounders(t, c, scid)
}

// 15 registered holders paid in one PayBatch call must fit the compute-gas limit with margin.
func TestFoundersPayBatchGas(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 1000, 0, 0, 5000, 250)
	for i := 0; i < 60; i++ {
		a := c.NewAccount()
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", uint64(1+i%3))}, nil, a); !r.OK() {
			t.Fatalf("mint %d: %v", i, r.Err)
		}
		if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: uint64(1 + i%3)}, a); !r.OK() {
			t.Fatalf("register %d: %v", i, r.Err)
		}
	}
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: 123456789}, nil); !r.OK() {
		t.Fatalf("deposit: %v", r.Err)
	}
	if r := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 16)}, nil, nil); r.OK() {
		t.Fatal("n > 15 accepted")
	}
	r := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, nil)
	if !r.OK() {
		t.Fatalf("paybatch 15: %v", r.Err)
	}
	t.Logf("PayBatch of 15 holders: compute gas %d of 10000000, %d transfers", r.GasCompute, len(r.Transfers))
	if r.GasCompute > 7500000 {
		t.Fatalf("PayBatch(15) too close to the compute limit: %d", r.GasCompute)
	}
	for from := uint64(15); from < 60; from += 15 {
		if r := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", from), harness.U("n", 15)}, nil, nil); !r.OK() {
			t.Fatalf("paybatch %d: %v", from, r.Err)
		}
	}
	r2 := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 55), harness.U("n", 15)}, nil, nil)
	if !r2.OK() || len(r2.Transfers) != 0 {
		t.Fatalf("paybatch tail: %v %d", r2.Err, len(r2.Transfers))
	}
	if c.U(scid, "rpool") > 120 { // dust below one unit per holder
		t.Fatalf("rpool left %d", c.U(scid, "rpool"))
	}
	checkFounders(t, c, scid)
}

// Random register / withdraw / deposit / claim / paybatch / list / buy against a big.Int model of the accumulator.
func TestFoundersFuzz(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 0, 500, 0, 0, 5000, 250) // minhst = 1 + 500/50 = 11
	rng := rand.New(rand.NewSource(7))
	type acct struct {
		a         *rpc.Address
		wallet    uint64 // passes in the wallet
		reg       uint64 // registered units
		hd, he    uint64 // accumulator snapshot and the era it belongs to
		paid      uint64
		modelPaid uint64
	}
	accts := make([]*acct, 6)
	for i := range accts {
		accts[i] = &acct{a: c.NewAccount()}
		n := uint64(1 + rng.Intn(20))
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", n)}, nil, accts[i].a); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
		accts[i].wallet = n
	}
	acc, era, hst, und, rpool := uint64(0), uint64(0), uint64(0), uint64(0), uint64(0)
	ea := map[uint64]uint64{} // final accumulator of each closed era
	b32 := new(big.Int).Lsh(big.NewInt(1), 32)
	distribute := func(x uint64) {
		total := und + x
		if total == 0 {
			return
		}
		if hst < 11 { // below minhst nothing is released (C9): it waits in und
			und = total
			return
		}
		pool := total
		if pool>>32 >= hst { // the most one increment can carry; the rest stays undistributed for now
			pool = hst * (1<<32 - 1)
		}
		inc := new(big.Int).Div(new(big.Int).Mul(new(big.Int).SetUint64(pool), b32), new(big.Int).SetUint64(hst)).Uint64()
		cov := new(big.Int).Mul(new(big.Int).SetUint64(inc), new(big.Int).SetUint64(hst))
		covered := new(big.Int).Div(cov, b32).Uint64()
		if new(big.Int).Mod(cov, b32).Sign() != 0 {
			covered++
		}
		if acc+inc < acc { // the accumulator would wrap: close the era
			ea[era] = acc
			era++
			acc = 0
		}
		acc += inc
		rpool += covered
		und = total - covered
	}
	share := func(units, delta uint64) uint64 {
		return new(big.Int).Div(new(big.Int).Mul(new(big.Int).SetUint64(units), new(big.Int).SetUint64(delta)), b32).Uint64()
	}
	settle := func(a *acct) uint64 {
		if a.reg == 0 && !c.Exists(scid, "hi_"+a.a.String()) {
			return 0
		}
		pend, hd := uint64(0), a.hd
		for e := a.he; e < era; e++ {
			pend += share(a.reg, ea[e]-hd)
			hd = 0
		}
		pend += share(a.reg, acc-hd)
		a.hd, a.he = acc, era
		if pend > rpool {
			pend = rpool
		}
		rpool -= pend
		a.modelPaid += pend
		return pend
	}
	for step := 0; step < 3000; step++ {
		a := accts[rng.Intn(len(accts))]
		switch rng.Intn(6) {
		case 0: // register some
			if a.wallet == 0 {
				continue
			}
			u := uint64(1 + rng.Intn(int(a.wallet)))
			r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: u}, a.a)
			if !r.OK() {
				t.Fatalf("step %d register: %v", step, r.Err)
			}
			distribute(0)
			got := settle(a)
			if r.Sent(zero, a.a) != got {
				t.Fatalf("step %d register settle: chain %d model %d", step, r.Sent(zero, a.a), got)
			}
			a.paid += r.Sent(zero, a.a)
			a.hd, a.he = acc, era
			a.wallet -= u
			a.reg += u
			hst += u
			distribute(0)
		case 1: // withdraw some
			if a.reg == 0 {
				continue
			}
			u := uint64(1 + rng.Intn(int(a.reg)))
			r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("u", u)}, nil, a.a)
			if !r.OK() {
				t.Fatalf("step %d withdraw: %v", step, r.Err)
			}
			distribute(0)
			got := settle(a)
			if r.Sent(zero, a.a) != got || r.Sent(scid, a.a) != u {
				t.Fatalf("step %d withdraw settle: chain %d model %d", step, r.Sent(zero, a.a), got)
			}
			a.paid += r.Sent(zero, a.a)
			a.wallet += u
			a.reg -= u
			hst -= u
		case 2: // deposit
			d := uint64(1 + rng.Intn(5000000))
			if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: d}, nil); !r.OK() {
				t.Fatalf("step %d deposit: %v", step, r.Err)
			}
			distribute(d)
		case 3: // claim
			r := c.Call(scid, "Claim", nil, nil, a.a)
			if !r.OK() {
				t.Fatalf("step %d claim: %v", step, r.Err)
			}
			distribute(0)
			got := settle(a)
			if r.Sent(zero, a.a) != got {
				t.Fatalf("step %d claim: chain %d model %d", step, r.Sent(zero, a.a), got)
			}
			a.paid += r.Sent(zero, a.a)
		case 4: // paybatch over everyone
			hn := c.U(scid, "hn")
			if hn == 0 {
				continue
			}
			r := c.Call(scid, "PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, nil)
			if !r.OK() {
				t.Fatalf("step %d paybatch: %v", step, r.Err)
			}
			distribute(0)
			for _, x := range accts {
				got := settle(x)
				if r.Sent(zero, x.a) != got {
					t.Fatalf("step %d paybatch %s: chain %d model %d", step, x.a.String()[:12], r.Sent(zero, x.a), got)
				}
				x.paid += got
			}
		case 5: // list one and let another buy it (fee to holders)
			if a.wallet == 0 {
				continue
			}
			buyer := accts[rng.Intn(len(accts))]
			if buyer == a {
				continue
			}
			price := uint64(1000 + rng.Intn(1000000))
			if r := c.Call(scid, "List", rpc.Arguments{harness.U("price", price)}, map[crypto.Hash]uint64{scid: 1}, a.a); !r.OK() {
				t.Fatalf("step %d list: %v", step, r.Err)
			}
			a.wallet--
			id := c.U(scid, "ln") - 1
			r := c.Call(scid, "Buy", rpc.Arguments{harness.U("id", id), harness.U("n", 1)}, map[crypto.Hash]uint64{zero: price}, buyer.a)
			if !r.OK() {
				t.Fatalf("step %d buy: %v", step, r.Err)
			}
			fee := (price/10000)*250 + ((price%10000)*250)/10000
			if r.Sent(zero, a.a) != price-fee || r.Sent(scid, buyer.a) != 1 {
				t.Fatalf("step %d buy amounts", step)
			}
			buyer.wallet++
			distribute(fee)
		}
		if c.U(scid, "hst") != hst || c.U(scid, "rpool") != rpool || c.U(scid, "und") != und || c.U(scid, "acc") != acc || c.U(scid, "era") != era {
			t.Fatalf("step %d state: hst %d/%d rpool %d/%d und %d/%d acc %d/%d era %d/%d", step, c.U(scid, "hst"), hst, c.U(scid, "rpool"), rpool, c.U(scid, "und"), und, c.U(scid, "acc"), acc, c.U(scid, "era"), era)
		}
		if step%250 == 0 {
			checkFounders(t, c, scid)
		}
	}
	checkFounders(t, c, scid)
	total := uint64(0)
	for _, a := range accts {
		if a.paid != a.modelPaid {
			t.Fatalf("paid mismatch %d vs %d", a.paid, a.modelPaid)
		}
		total += a.paid
	}
	if total != c.U(scid, "paid") {
		t.Fatalf("paid total %d vs contract %d", total, c.U(scid, "paid"))
	}
	t.Logf("fuzz: 3000 steps, %d DERO-units paid out across %d holders, dust %d", total, len(accts), c.U(scid, "rpool")+c.U(scid, "und"))
}
