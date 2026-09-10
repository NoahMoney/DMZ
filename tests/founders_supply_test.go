package tests

// The Founders Pass at the mainnet shape the directive names: maxsupply 20. Two questions the existing tests do
// not ask. (1) Can the supply exceed maxsupply by any sequence — mint to the cap, then drive every entrypoint
// that sends a unit and count what ever reached a wallet? (2) What does the anti-sweep floor minhst do at 20
// editions?

import (
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// unitsOut counts every unit of the pass the contract has ever sent to a wallet minus every unit sent back in.
type unitLedger struct {
	c    *harness.Chain
	scid crypto.Hash
	held uint64
}

func (l *unitLedger) call(entry string, args rpc.Arguments, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
	r := l.c.Call(l.scid, entry, args, in, who)
	if r.OK() {
		l.held -= in[l.scid]
		for _, tr := range r.Transfers {
			if tr.Asset == l.scid {
				l.held += tr.Amount
			}
		}
	}
	return r
}

// inContract is what the contract's own bookkeeping says it is holding: registered units plus listed units.
func (l *unitLedger) inContract() uint64 {
	n := l.c.U(l.scid, "hst")
	for i := uint64(0); i < l.c.U(l.scid, "ln"); i++ {
		if l.c.Exists(l.scid, "lu_"+itoa(i)) {
			n += l.c.U(l.scid, "lu_"+itoa(i))
		}
	}
	return n
}

func TestFoundersSupplyCannotExceedMaxsupplyAt20(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	alice, bob, carol := c.NewAccount(), c.NewAccount(), c.NewAccount()
	const max, reserve, price = 20, 4, 1000
	scid := installFounders(t, c, creator, treasury, price, max, 0, reserve, 5000, 250)
	l := &unitLedger{c: c, scid: scid, held: reserve} // the install minted the reserve to the creator

	if c.U(scid, "minted") != reserve || c.U(scid, "maxsupply") != max {
		t.Fatalf("minted %d maxsupply %d", c.U(scid, "minted"), c.U(scid, "maxsupply"))
	}
	if mh := c.U(scid, "minhst"); mh != 1 {
		t.Fatalf("minhst %d at maxsupply 20 — the note in founders.bas 49 says 1 + maxsupply/50", mh)
	}
	t.Logf("maxsupply 20 -> minhst = %d: the C9 anti-sweep floor is one single pass", c.U(scid, "minhst"))

	// mint the remaining 16 across three wallets
	for _, m := range []struct {
		a *rpc.Address
		n uint64
	}{{alice, 8}, {bob, 5}, {carol, 3}} {
		if r := l.call("Mint", rpc.Arguments{harness.U("n", m.n)}, map[crypto.Hash]uint64{zero: m.n * price}, m.a); !r.OK() {
			t.Fatalf("mint %d: %v", m.n, r.Err)
		}
	}
	if c.U(scid, "minted") != max {
		t.Fatalf("minted %d, want the cap", c.U(scid, "minted"))
	}

	// every further mint must be refused, at any n, any price, any caller, and after the registry has moved
	for _, n := range []uint64{1, 2, max, ^uint64(0)} {
		for _, v := range []uint64{0, n * price, 1} {
			if r := l.call("Mint", rpc.Arguments{harness.U("n", n)}, map[crypto.Hash]uint64{zero: v}, alice); r.OK() {
				t.Fatalf("Mint(%d) with %d DERO succeeded past the cap", n, v)
			}
		}
	}

	// now drive every entrypoint that moves a unit, and keep counting
	steps := []func(){
		func() { l.call("Register", nil, map[crypto.Hash]uint64{scid: 8}, alice) },
		func() { l.call("Register", nil, map[crypto.Hash]uint64{scid: 5}, bob) },
		func() { l.call("Deposit", nil, map[crypto.Hash]uint64{zero: 700000}, carol) },
		func() { l.call("Distribute", nil, nil, carol) },
		func() { l.call("PayBatch", rpc.Arguments{harness.U("from", 0), harness.U("n", 15)}, nil, carol) },
		func() { l.call("Withdraw", rpc.Arguments{harness.U("u", 3)}, nil, alice) },
		func() { l.call("List", rpc.Arguments{harness.U("price", 5000)}, map[crypto.Hash]uint64{scid: 3}, alice) },
		func() { l.call("Buy", rpc.Arguments{harness.U("id", 0), harness.U("n", 2)}, map[crypto.Hash]uint64{zero: 10000}, carol) },
		func() { l.call("Unlist", rpc.Arguments{harness.U("id", 0)}, nil, alice) },
		func() { l.call("Withdraw", rpc.Arguments{harness.U("u", 5)}, nil, bob) },
		func() { l.call("Register", nil, map[crypto.Hash]uint64{scid: 4}, creator) },
		func() { l.call("Claim", nil, nil, creator) },
		func() { l.call("Withdraw", rpc.Arguments{harness.U("u", 4)}, nil, creator) },
		// and the refused shapes: unlisting someone else's listing, buying a listing that is gone, withdrawing
		// more than is registered
		func() { l.call("Unlist", rpc.Arguments{harness.U("id", 0)}, nil, bob) },
		func() { l.call("Buy", rpc.Arguments{harness.U("id", 0), harness.U("n", 99)}, map[crypto.Hash]uint64{zero: 495000}, carol) },
		func() { l.call("Withdraw", rpc.Arguments{harness.U("u", 99)}, nil, alice) },
	}
	for i, s := range steps {
		s()
		total := l.held + l.inContract()
		if total != max {
			t.Fatalf("after step %d: %d units in wallets + %d the contract accounts for = %d, want exactly %d",
				i, l.held, l.inContract(), total, max)
		}
		if c.U(scid, "minted") > max {
			t.Fatalf("after step %d: minted %d > maxsupply", i, c.U(scid, "minted"))
		}
	}
	checkFounders(t, c, scid)
	t.Logf("supply held at %d through %d steps; maxsupply is written once (founders.bas 32) and there is no setter and no UpdateCode", max, len(steps))
}

// TestFoundersMinhstAt20LetsOneUnitSweepEverything: with maxsupply 20 the C9 floor degenerates to 1, so the
// first wallet to register a single pass takes every DERO deposited before anyone was registered — including the
// installer, who holds the reserve for free.
func TestFoundersMinhstAt20LetsOneUnitSweepEverything(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	alice := c.NewAccount()
	scid := installFounders(t, c, creator, treasury, 1000, 20, 0, 4, 5000, 250)

	// the treasury deposits before anyone has registered — the sequence the bot produces on a fresh deployment
	const dep = 900_000
	if r := c.Call(scid, "Deposit", nil, map[crypto.Hash]uint64{zero: dep}, treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.U(scid, "und") != dep || c.U(scid, "rpool") != 0 {
		t.Fatalf("und %d rpool %d", c.U(scid, "und"), c.U(scid, "rpool"))
	}
	// alice mints ONE pass and registers it
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 1)}, map[crypto.Hash]uint64{zero: 1000}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1}, alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.U(scid, "hst") != 1 {
		t.Fatalf("hst %d", c.U(scid, "hst"))
	}
	r := c.Call(scid, "Claim", nil, nil, alice)
	if !r.OK() {
		t.Fatal(r.Err)
	}
	got := r.Sent(zero, alice)
	if got != dep {
		t.Fatalf("one registered pass of 20 claimed %d of the %d deposited before anyone registered", got, dep)
	}
	t.Logf("one pass of 20 (5%%) claimed 100%% of the %d deposited before any holder existed; the same holder can Withdraw immediately after", got)
	checkFounders(t, c, scid)
}
