package tests

// contracts/v2/founders.bas, the Founders Pass v2 (operator rulings 2026-09-13; docs/FOUNDERS_V2_NOTES.md). Per-asset
// reward pools with O(1) calls: registrations are positions whose units never change, each asset has an unscaled
// accumulator, and Claim takes two record indices the contract checks in O(1). v2Model below is the whole contract in
// Go; every test compares the chain with it.

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"

	ref "github.com/deroproject/derohe/veil/reference"
)

const foundersV2File = "v2/founders.bas"

// foundersV2CanonSha256 is sha256(reference.Canon(contracts/v2/founders.bas)): the code the chain will hold, and the
// hash tools/verifydeploy, tools/release and app/config.js must carry once v2 is adopted.
const foundersV2CanonSha256 = "348f872ed49ec218f361c4a70a5f6cd96ada38c3d3d8a0b960fba473c02746d3"

// The description the operator approved for v2 (2026-09-13). 227 bytes, inside founders.bas's 240.
const foundersV2Descr = "Register a pass for a fixed share of the fees DMZ takes, paid in the asset each fee was taken in: " +
	"DERO and DERO tokens through this contract, XMR and ZEC by the treasury's bot. The bot makes each payment; no " +
	"contract forces it."

var (
	v2TokA = crypto.Hash{0xa1, 0x01} // stand-ins for DERO tokens: a wrapped XMR, a launch token
	v2TokB = crypto.Hash{0xb2, 0x02}
)

func installV2Args(treasury string, name, descr, icon, file string, price, max, start, reserve, share, mfee uint64) rpc.Arguments {
	return rpc.Arguments{harness.S("name", name), harness.S("descr", descr), harness.S("icon", icon), harness.S("file", file),
		harness.U("price", price), harness.U("maxsupply", max), harness.U("start", start), harness.U("reserve", reserve),
		harness.U("share", share), harness.U("mfee", mfee), harness.S("treasury", treasury)}
}

func installV2(t *testing.T, c *harness.Chain, creator, treasury *rpc.Address, price, max, start, reserve uint64) crypto.Hash {
	t.Helper()
	scid, res := c.Install(readContract(t, foundersV2File), nil,
		installV2Args(treasury.String(), "DMZ Founders Pass", foundersV2Descr, "", "", price, max, start, reserve, 5000, 250), creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install v2: %v", res.Err)
	}
	return scid
}

// ---- the model ------------------------------------------------------------------------------------------------

type v2Asset struct {
	id                 crypto.Hash
	ac, au, ap, ad, aw uint64
	rt, ra             []uint64
}

type v2Pos struct {
	owner     *rpc.Address
	u, pt, pc uint64
}

type v2Model struct {
	ev, hst uint64
	assets  []*v2Asset
	idx     map[crypto.Hash]int
	pos     []*v2Pos
	pd      map[[2]int]uint64 // (asset, position) -> the accumulator claimed through
	hu      map[string]uint64
}

func newV2Model() *v2Model {
	m := &v2Model{idx: map[crypto.Hash]int{}, pd: map[[2]int]uint64{}, hu: map[string]uint64{}}
	m.addAsset(zero)
	return m
}

func (m *v2Model) addAsset(id crypto.Hash) int {
	m.assets = append(m.assets, &v2Asset{id: id})
	m.idx[id] = len(m.assets) - 1
	return len(m.assets) - 1
}

func (m *v2Model) register(who *rpc.Address, u uint64) {
	m.ev++
	m.pos = append(m.pos, &v2Pos{owner: who, u: u, pt: m.ev})
	m.hst += u
	m.hu[who.String()] += u
}

func (m *v2Model) withdraw(p int, u uint64) {
	m.ev++
	q := m.pos[p]
	q.pc = m.ev
	if u < q.u {
		m.pos = append(m.pos, &v2Pos{owner: q.owner, u: q.u - u, pt: m.ev})
	}
	m.hst -= u
	a := q.owner.String()
	if m.hu[a] -= u; m.hu[a] == 0 {
		delete(m.hu, a)
	}
}

// credit is founders.bas credit(): false, changing nothing, when ad would wrap.
func (m *v2Model) credit(i int, d uint64) bool {
	a := m.assets[i]
	if a.ad+d < a.ad {
		return false
	}
	pool := a.au + d
	inc := pool / m.hst
	if inc > 0 {
		if n := len(a.rt); n == 0 || a.rt[n-1] != m.ev {
			a.rt = append(a.rt, m.ev)
			a.ra = append(a.ra, a.ac)
		}
		a.ac += inc
		a.ap += inc * m.hst
	}
	a.au = pool - inc*m.hst
	a.ad += d
	return true
}

// deposit is Deposit(): refused with nobody registered; a new asset gets the next index.
func (m *v2Model) deposit(id crypto.Hash, d uint64) bool {
	if m.hst == 0 || d == 0 {
		return false
	}
	i, ok := m.idx[id]
	if ok {
		return m.credit(i, d)
	}
	i = m.addAsset(id)
	return m.credit(i, d)
}

// accAt is the accumulator of asset i when tick t began, and the record index Claim must be handed for it.
func (m *v2Model) accAt(i int, t uint64) (uint64, uint64) {
	a := m.assets[i]
	for k, rt := range a.rt {
		if rt >= t {
			return a.ra[k], uint64(k)
		}
	}
	return a.ac, uint64(len(a.rt))
}

// window returns the accumulator a claim starts from and the one it runs to.
func (m *v2Model) window(p, i int) (uint64, uint64) {
	q := m.pos[p]
	ta, ok := m.pd[[2]int{i, p}]
	if !ok {
		ta, _ = m.accAt(i, q.pt)
	}
	tb := m.assets[i].ac
	if q.pc != 0 {
		tb, _ = m.accAt(i, q.pc)
	}
	return ta, tb
}

// owed is what position p may claim of asset i now, in exact integers (big, so an overflow would show).
func (m *v2Model) owed(t *testing.T, p, i int) uint64 {
	t.Helper()
	ta, tb := m.window(p, i)
	if tb <= ta {
		return 0
	}
	x := new(big.Int).Mul(new(big.Int).SetUint64(m.pos[p].u), new(big.Int).SetUint64(tb-ta))
	if !x.IsUint64() {
		t.Fatalf("position %d asset %d: owed %s does not fit 64 bits", p, i, x)
	}
	return x.Uint64()
}

func (m *v2Model) claim(t *testing.T, p, i int) uint64 {
	t.Helper()
	amt := m.owed(t, p, i)
	if amt == 0 {
		return 0
	}
	_, tb := m.window(p, i)
	m.pd[[2]int{i, p}] = tb
	m.assets[i].ap -= amt
	m.assets[i].aw += amt
	return amt
}

// ---- reading the chain the way the app and the bot will ----------------------------------------------------

func s64(n uint64) string { return itoa(n) }

// v2Hint is the binary search a client runs over the records of asset i: the first k with rt_<i>_<k> >= t.
func v2Hint(c *harness.Chain, scid crypto.Hash, i, t uint64) uint64 {
	lo, hi := uint64(0), c.U(scid, "ar_"+s64(i))
	for lo < hi {
		mid := (lo + hi) / 2
		if c.U(scid, "rt_"+s64(i)+"_"+s64(mid)) >= t {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

func v2ClaimArgs(c *harness.Chain, scid crypto.Hash, p, i uint64) rpc.Arguments {
	var ka, kb uint64
	if c.Exists(scid, "pt_"+s64(p)) && c.Exists(scid, "ar_"+s64(i)) {
		ka = v2Hint(c, scid, i, c.U(scid, "pt_"+s64(p)))
		if pc := c.U(scid, "pc_"+s64(p)); pc != 0 {
			kb = v2Hint(c, scid, i, pc)
		}
	}
	return rpc.Arguments{harness.U("p", p), harness.U("i", i), harness.U("ka", ka), harness.U("kb", kb)}
}

func v2Deposit(asset crypto.Hash) rpc.Arguments { return rpc.Arguments{harness.H("asset", asset)} }

func v2In(asset crypto.Hash, v uint64) map[crypto.Hash]uint64 {
	return map[crypto.Hash]uint64{asset: v}
}

// check compares every key the model knows with the chain, and the per-asset invariants: the balance is what the
// passes are owed plus the dust, everything credited is paid, owed or dust, and what is owed is exactly the sum of
// what every position can claim.
func (m *v2Model) check(t *testing.T, c *harness.Chain, scid crypto.Hash, what string, full bool) {
	t.Helper()
	if c.U(scid, "hst") != m.hst || c.U(scid, "ev") != m.ev || c.U(scid, "pn") != uint64(len(m.pos)) || c.U(scid, "an") != uint64(len(m.assets)) {
		t.Fatalf("%s: hst %d/%d ev %d/%d pn %d/%d an %d/%d", what, c.U(scid, "hst"), m.hst, c.U(scid, "ev"), m.ev,
			c.U(scid, "pn"), len(m.pos), c.U(scid, "an"), len(m.assets))
	}
	for i, a := range m.assets {
		is := s64(uint64(i))
		for _, kv := range []struct {
			k    string
			want uint64
		}{{"ac_", a.ac}, {"au_", a.au}, {"ap_", a.ap}, {"ad_", a.ad}, {"aw_", a.aw}, {"ar_", uint64(len(a.rt))}} {
			if got := c.U(scid, kv.k+is); got != kv.want {
				t.Fatalf("%s: asset %d %s%d = %d, model %d", what, i, kv.k, i, got, kv.want)
			}
		}
		if bal := c.SCBalance(scid, a.id); bal != a.ap+a.au {
			t.Fatalf("%s: asset %d balance %d != owed %d + dust %d", what, i, bal, a.ap, a.au)
		}
		if a.ad != a.aw+a.ap+a.au {
			t.Fatalf("%s: asset %d credited %d != paid %d + owed %d + dust %d", what, i, a.ad, a.aw, a.ap, a.au)
		}
		if !full {
			continue
		}
		if c.S(scid, "ah_"+is) != string(a.id[:]) || c.U(scid, "ai_"+hex.EncodeToString(a.id[:])) != uint64(i)+1 {
			t.Fatalf("%s: asset %d id keys", what, i)
		}
		for k := range a.rt {
			if c.U(scid, "rt_"+is+"_"+s64(uint64(k))) != a.rt[k] || c.U(scid, "ra_"+is+"_"+s64(uint64(k))) != a.ra[k] {
				t.Fatalf("%s: asset %d record %d", what, i, k)
			}
		}
		var owed uint64
		for p := range m.pos {
			owed += m.owed(t, p, i)
		}
		if owed != a.ap {
			t.Fatalf("%s: asset %d: the positions can claim %d in all, the contract holds %d for them", what, i, owed, a.ap)
		}
	}
	if !full {
		return
	}
	for p, q := range m.pos {
		ps := s64(uint64(p))
		if c.S(scid, "po_"+ps) != raw(q.owner) || c.U(scid, "pu_"+ps) != q.u || c.U(scid, "pt_"+ps) != q.pt || c.U(scid, "pc_"+ps) != q.pc {
			t.Fatalf("%s: position %d keys", what, p)
		}
		for i := range m.assets {
			v, ok := m.pd[[2]int{i, p}]
			if got, has := c.Var(scid, "pd_"+s64(uint64(i))+"_"+ps).(uint64); has != ok || got != v {
				t.Fatalf("%s: pd_%d_%d chain %v/%d model %v/%d", what, i, p, has, got, ok, v)
			}
		}
	}
	for a, u := range m.hu {
		if c.U(scid, "hu_"+a) != u {
			t.Fatalf("%s: hu_%s…", what, a[:12])
		}
	}
}

// ---- deposits in DERO and two tokens, registrations moving between them ---------------------------------------

func TestFoundersV2PerAssetShares(t *testing.T) {
	c := harness.New()
	creator, treasury, alice, bob, carol := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installV2(t, c, creator, treasury, 100000, 20, 0, 4) // the operator's mainnet shape: 20 editions, 4 reserved
	m := newV2Model()
	for _, x := range []struct {
		a *rpc.Address
		n uint64
	}{{alice, 3}, {bob, 5}, {carol, 2}} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", x.n)}, v2In(zero, x.n*100000), x.a); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
	}
	// nothing can be deposited for nobody: DERO and a token are both refused while no pass is registered (FEE-01)
	if r := c.Call(scid, "Deposit", v2Deposit(zero), v2In(zero, 500000), treasury); r.OK() {
		t.Fatal("a DERO deposit with no pass registered was accepted")
	}
	if r := c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 500000), treasury); r.OK() {
		t.Fatal("a token deposit with no pass registered was accepted")
	}
	reg := func(who *rpc.Address, u uint64) {
		t.Helper()
		if r := c.Call(scid, "Register", nil, v2In(scid, u), who); !r.OK() {
			t.Fatalf("register: %v", r.Err)
		}
		m.register(who, u)
	}
	dep := func(asset crypto.Hash, d uint64) {
		t.Helper()
		if r := c.Call(scid, "Deposit", v2Deposit(asset), v2In(asset, d), treasury); !r.OK() {
			t.Fatalf("deposit %x… %d: %v", asset[:2], d, r.Err)
		}
		if !m.deposit(asset, d) {
			t.Fatal("model refused a deposit the chain took")
		}
		m.check(t, c, scid, "deposit", true)
	}
	withdraw := func(who *rpc.Address, p, u uint64) {
		t.Helper()
		r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("p", p), harness.U("u", u)}, nil, who)
		if !r.OK() || r.Sent(scid, who) != u {
			t.Fatalf("withdraw: %v", r.Err)
		}
		m.withdraw(int(p), u)
	}
	reg(alice, 3)         // p0
	reg(bob, 5)           // p1, hst 8
	dep(zero, 800003)     // inc 100000, 3 units of dust
	dep(v2TokA, 1600000)  // asset 1: inc 200000
	reg(carol, 2)         // p2, hst 10
	dep(v2TokB, 1000007)  // asset 2: inc 100000, 7 of dust
	withdraw(bob, 1, 2)   // p1 closes; p3 holds bob's other 3; hst 8
	dep(zero, 797)        // 797 + the 3 waiting = 800: inc 100, no dust
	dep(v2TokA, 80)       // inc 10
	reg(creator, 4)       // p4, the reserve, hst 12
	withdraw(alice, 0, 3) // p0 closes; hst 9
	dep(v2TokB, 900)      // 900 + 7 = 907: inc 100, 7 of dust
	if c.U(scid, "an") != 3 || c.U(scid, "hst") != 9 {
		t.Fatalf("an %d hst %d", c.U(scid, "an"), c.U(scid, "hst"))
	}

	// what each position earned, worked by hand
	want := []struct {
		who  *rpc.Address
		p    uint64
		dero uint64
		a, b uint64
	}{
		{alice, 0, 300000 + 300, 600000 + 30, 300000}, // 3 units, closed before the last tokB deposit
		{bob, 1, 500000, 1000000, 500000},             // 5 units, closed before the second DERO deposit
		{carol, 2, 200, 20, 200000 + 200},             // 2 units from after the first two deposits
		{bob, 3, 300, 30, 300},                        // bob's 3 left registered, from the withdrawal on
		{creator, 4, 0, 0, 400},                       // 4 units, only the last deposit
	}
	stranger := c.NewAccount()
	for _, w := range want {
		for i, amt := range []uint64{w.dero, w.a, w.b} {
			who := stranger // anyone may push a claim; the owner is paid
			if i == 1 {
				who = nil // at any ring size
			}
			r := c.Call(scid, "Claim", v2ClaimArgs(c, scid, w.p, uint64(i)), nil, who)
			asset := m.assets[i].id
			if amt == 0 {
				if r.OK() {
					t.Fatalf("p%d asset %d: a claim of nothing was accepted", w.p, i)
				}
				continue
			}
			if !r.OK() || r.Sent(asset, w.who) != amt || len(r.Transfers) != 1 {
				t.Fatalf("p%d asset %d: %v paid %d, want %d", w.p, i, r.Err, r.Sent(asset, w.who), amt)
			}
			if got := m.claim(t, int(w.p), i); got != amt {
				t.Fatalf("model p%d asset %d: %d, hand-worked %d", w.p, i, got, amt)
			}
			if r2 := c.Call(scid, "Claim", v2ClaimArgs(c, scid, w.p, uint64(i)), nil, w.who); r2.OK() {
				t.Fatalf("p%d asset %d: a second claim was accepted", w.p, i)
			}
		}
	}
	m.check(t, c, scid, "after every claim", true)
	// every unit deposited is paid, except the dust, which stays in the contract and is accounted for
	for i, x := range []struct{ dep, dust uint64 }{{800800, 0}, {1600080, 0}, {1000907, 7}} {
		a := m.assets[i]
		if a.ad != x.dep || a.aw != x.dep-x.dust || a.au != x.dust || c.SCBalance(scid, a.id) != x.dust || a.ap != 0 {
			t.Fatalf("asset %d: credited %d paid %d dust %d balance %d owed %d", i, a.ad, a.aw, a.au, c.SCBalance(scid, a.id), a.ap)
		}
	}
	t.Logf("three assets, five positions: every unit paid exactly as worked by hand; dust left: DERO 0, tokA 0, tokB 7")
}

// ---- conservation per asset, after every accepted and refused call --------------------------------------------

func TestFoundersV2ConservationFuzz(t *testing.T) {
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	users := []*rpc.Address{creator, c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()}
	const price, max = 100000, 40
	scid, res := c.Install(readContract(t, foundersV2File), nil,
		installV2Args(treasury.String(), "DMZ Founders Pass", foundersV2Descr, "", "", price, max, 0, 8, 5000, 250), creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	m := newV2Model()
	units := newConsLedger(scid)
	units.apply(t, res, nil, creator)
	rng := rand.New(rand.NewSource(20260913))
	var y consTally
	depIn := map[crypto.Hash]uint64{}   // every unit of each asset the contract accepted for the passes
	paidOut := map[crypto.Hash]uint64{} // every unit of each asset Claim sent
	stored := map[string]bool{}         // xm_/zc_ keys set
	assets := []crypto.Hash{zero, v2TokA, v2TokB}
	junk := byte(0)
	b58 := "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	addr := func(first string, n int) string {
		var s strings.Builder
		s.WriteString(first)
		for s.Len() < n {
			s.WriteByte(b58[rng.Intn(len(b58))])
		}
		return s.String()
	}
	steps := consSteps() + 200
	full := 0
	for step := 0; step < steps; step++ {
		who := consPick(rng, users)
		var r harness.Result
		var in map[crypto.Hash]uint64
		ok := false // what the model says the chain must do
		after := func() {}
		switch rng.Intn(15) {
		case 0: // mint
			n := consUpTo(rng, 3)
			pay := n * price
			if rng.Intn(5) == 0 {
				pay--
			}
			in = consDero(pay)
			minted := c.U(scid, "minted")
			ok = n > 0 && n <= max-minted && pay == n*price
			r = c.Call(scid, "Mint", rpc.Arguments{harness.U("n", n)}, in, who)
		case 1, 2: // register
			u := consUpTo(rng, units.of(who))
			in = map[crypto.Hash]uint64{}
			if u > 0 {
				in[scid] = u
			}
			stray := rng.Intn(8) == 0
			if stray {
				in[zero] = 1000
			}
			ok = u > 0 && !stray
			r = c.Call(scid, "Register", nil, in, who)
			after = func() { m.register(who, u) }
		case 3: // withdraw: usually one of the caller's open positions, sometimes anything
			p, u := uint64(0), uint64(1)
			var mine []int
			for i, q := range m.pos {
				if q.owner == who && q.pc == 0 {
					mine = append(mine, i)
				}
			}
			if len(mine) > 0 && rng.Intn(5) != 0 {
				p = uint64(mine[rng.Intn(len(mine))])
				u = consUpTo(rng, m.pos[p].u)
				if rng.Intn(6) == 0 {
					u = m.pos[p].u + 1
				}
			} else if len(m.pos) > 0 {
				p = uint64(rng.Intn(len(m.pos) + 1))
			}
			ok = int(p) < len(m.pos) && m.pos[p].owner == who && m.pos[p].pc == 0 && u >= 1 && u <= m.pos[p].u
			r = c.Call(scid, "Withdraw", rpc.Arguments{harness.U("p", p), harness.U("u", u)}, nil, who)
			after = func() { m.withdraw(int(p), u) }
		case 4, 5, 6: // claim, by anyone, sometimes with a wrong hint or something attached
			if len(m.pos) == 0 {
				continue
			}
			p := uint64(rng.Intn(len(m.pos)))
			i := uint64(rng.Intn(len(m.assets) + 1)) // one past the end, sometimes
			args := v2ClaimArgs(c, scid, p, i)
			bad := false
			if int(i) < len(m.assets) && rng.Intn(6) == 0 {
				_, hasPd := m.pd[[2]int{int(i), int(p)}]
				if !hasPd {
					args[2] = harness.U("ka", args[2].Value.(uint64)+1) // ka is read: a wrong one is refused
					bad = true
				} else if m.pos[p].pc != 0 {
					args[3] = harness.U("kb", args[3].Value.(uint64)+1)
					bad = true
				}
			}
			signer := who
			if rng.Intn(3) == 0 {
				signer = nil
			}
			attach := signer != nil && units.of(signer) > 0 && rng.Intn(8) == 0
			if attach {
				in = map[crypto.Hash]uint64{scid: 1}
			} else if rng.Intn(10) == 0 && int(i) < len(m.assets) && signer != nil {
				in = map[crypto.Hash]uint64{m.assets[i].id: 5} // the claimed asset attached
				attach = true
			}
			ok = int(i) < len(m.assets) && !bad && !attach && m.owed(t, int(p), int(i)) > 0
			r = c.Call(scid, "Claim", args, in, signer)
			after = func() {
				amt := m.claim(t, int(p), int(i))
				id := m.assets[i].id
				if got := r.Sent(id, m.pos[p].owner); got != amt || len(r.Transfers) != 1 {
					t.Fatalf("step %d claim p%d asset %d: chain paid %d, model %d", step, p, i, got, amt)
				}
				paidOut[id] += amt
			}
		case 7, 8: // deposit: DERO, a token, or a new one; sometimes nothing, sometimes DERO beside a token
			var asset crypto.Hash
			switch k := rng.Intn(8); {
			case k < 3:
				asset = zero
			case k < 5:
				asset = v2TokA
			case k < 7:
				asset = v2TokB
			default:
				junk++
				asset = crypto.Hash{0xc3, junk}
			}
			d := consUpTo(rng, 5_000_000)
			if rng.Intn(8) == 0 {
				d = 0
			}
			in = map[crypto.Hash]uint64{}
			if d > 0 {
				in[asset] = d
			}
			stray := asset != zero && rng.Intn(6) == 0
			if stray {
				in[zero] = 100
			}
			ok = m.hst > 0 && d > 0 && !stray
			signer := consPick(rng, []*rpc.Address{treasury, who, nil})
			r = c.Call(scid, "Deposit", v2Deposit(asset), in, signer)
			after = func() {
				if !m.deposit(asset, d) {
					t.Fatalf("step %d: the model refused a deposit", step)
				}
				depIn[asset] += d
				if asset[0] == 0xc3 {
					assets = append(assets, asset)
				}
			}
		case 9: // list
			u := consUpTo(rng, units.of(who))
			pr := consUpTo(rng, 1_000_000)
			in = map[crypto.Hash]uint64{}
			if u > 0 {
				in[scid] = u
			}
			ok = u > 0 && pr > 0
			r = c.Call(scid, "List", rpc.Arguments{harness.U("price", pr)}, in, who)
		case 10: // unlist
			id := uint64(rng.Int63n(int64(c.U(scid, "ln")) + 1))
			seller, has := c.Var(scid, "ls_"+s64(id)).(string)
			ok = has && seller == who.String()
			r = c.Call(scid, "Unlist", rpc.Arguments{harness.U("id", id)}, nil, who)
		case 11: // buy: mfee to the passes when any are registered
			id := uint64(rng.Int63n(int64(c.U(scid, "ln")) + 1))
			lu, has := c.Var(scid, "lu_"+s64(id)).(uint64)
			lp, _ := c.Var(scid, "lp_"+s64(id)).(uint64)
			n := consUpTo(rng, lu)
			pay := n * lp
			if rng.Intn(5) == 0 {
				pay++
			}
			in = consDero(pay)
			ok = has && n > 0 && pay == n*lp
			fee := uint64(0)
			if m.hst > 0 {
				fee = (pay/10000)*250 + ((pay%10000)*250)/10000
			}
			r = c.Call(scid, "Buy", rpc.Arguments{harness.U("id", id), harness.U("n", n)}, in, who)
			after = func() {
				if fee > 0 {
					if !m.credit(0, fee) {
						t.Fatal("model refused a market fee")
					}
					depIn[zero] += fee
				}
			}
		case 12: // payout addresses
			entry, key := "SetXmr", "xm_"+who.String()
			a := addr("8", 95)
			valid := true
			switch rng.Intn(6) {
			case 0:
				entry, key, a = "SetZec", "zc_"+who.String(), addr("t1", 35)
			case 1:
				a = addr("4", 106)
			case 2:
				a = a[:50] + "0" + a[51:] // not base58
				valid = false
			case 3:
				a = ""
			}
			_, reg := m.hu[who.String()]
			if a == "" {
				ok = stored[key]
			} else {
				ok = valid && reg
			}
			r = c.Call(scid, entry, rpc.Arguments{harness.S("addr", a)}, nil, who)
			after = func() { stored[key] = a != "" }
		case 13: // DERO attached where nothing is taken
			ok = false
			in = consDero(1000)
			if len(m.pos) > 0 {
				r = c.Call(scid, "Claim", v2ClaimArgs(c, scid, 0, 0), in, who)
			} else {
				r = c.Call(scid, "Withdraw", rpc.Arguments{harness.U("p", 0), harness.U("u", 1)}, in, who)
			}
		default:
			c.NextBlock()
			continue
		}
		if r.OK() != ok {
			t.Fatalf("step %d: the chain returned %v, the model expected ok=%v", step, r.Err, ok)
		}
		units.apply(t, r, in, who)
		y.note(r)
		if r.OK() {
			after()
		}
		// per asset: everything accepted for the passes = everything claimed + the contract's balance
		for _, a := range assets {
			if depIn[a] != paidOut[a]+c.SCBalance(scid, a) {
				t.Fatalf("step %d asset %x…: accepted %d != claimed %d + held %d", step, a[:2], depIn[a], paidOut[a], c.SCBalance(scid, a))
			}
		}
		// pass units: wallets + registered + listed = minted
		var listed uint64
		for i := uint64(0); i < c.U(scid, "ln"); i++ {
			if v, has := c.Var(scid, "lu_"+s64(i)).(uint64); has {
				listed += v
			}
		}
		if units.total()+c.U(scid, "hst")+listed != c.U(scid, "minted") {
			t.Fatalf("step %d: pass units %d + %d + %d != minted %d", step, units.total(), c.U(scid, "hst"), listed, c.U(scid, "minted"))
		}
		isFull := step%20 == 0
		if isFull {
			full++
		}
		m.check(t, c, scid, "fuzz step", isFull)
	}
	m.check(t, c, scid, "fuzz end", true)
	y.require(t, "founders v2")
	for i, a := range m.assets {
		t.Logf("asset %d (%x…): credited %d, claimed %d, owed %d, dust %d, %d records", i, a.id[:2], a.ad, a.aw, a.ap, a.au, len(a.rt))
	}
	t.Logf("%d positions, %d full state comparisons", len(m.pos), full+1)
}

// ---- no call's cost grows with the number of assets or holders ---------------------------------------------------

// v2CostRun builds a pass with `holders` registered holders, `assets` extra tokens and `churn` register/deposit
// rounds (which grow the positions and the records of tokA), then makes each measured call in the same code shape.
func v2CostRun(t *testing.T, holders, assets, churn int) map[string]harness.Result {
	t.Helper()
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	scid := installV2(t, c, creator, treasury, 0, 1000, 0, 0)
	out := map[string]harness.Result{}
	must := func(what string, r harness.Result) harness.Result {
		t.Helper()
		if !r.OK() {
			t.Fatalf("%s: %v", what, r.Err)
		}
		return r
	}
	users := make([]*rpc.Address, holders)
	for i := range users {
		users[i] = c.NewAccount()
		must("mint", c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 3)}, nil, users[i]))
		must("register", c.Call(scid, "Register", nil, v2In(scid, 1), users[i]))
	}
	must("deposit dero", c.Call(scid, "Deposit", v2Deposit(zero), v2In(zero, 1_000_000), treasury))
	for j := 0; j < assets; j++ {
		must("deposit junk", c.Call(scid, "Deposit", v2Deposit(crypto.Hash{0xd4, byte(j), byte(j >> 8)}), v2In(crypto.Hash{0xd4, byte(j), byte(j >> 8)}, 1_000_000), treasury))
	}
	must("deposit tokA", c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 1_000_000), treasury))
	for k := 0; k < churn; k++ {
		u := users[k%holders]
		must("churn register", c.Call(scid, "Register", nil, v2In(scid, 1), u))
		must("churn deposit", c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 1_000_000), treasury))
		if k%2 == 1 {
			p := c.U(scid, "pn") - 1
			must("churn withdraw", c.Call(scid, "Withdraw", rpc.Arguments{harness.U("p", p), harness.U("u", 1)}, nil, u))
		}
	}
	x := c.NewAccount()
	must("x mint", c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 3)}, nil, x))
	must("x register", c.Call(scid, "Register", nil, v2In(scid, 2), x))
	px := c.U(scid, "pn") - 1
	must("deposit in x's tick", c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 1_000_000), treasury))
	out["Withdraw part"] = must("x withdraw", c.Call(scid, "Withdraw", rpc.Arguments{harness.U("p", px), harness.U("u", 1)}, nil, x))
	out["Deposit, new tick"] = must("deposit", c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 1_000_000), treasury))
	out["Deposit, same tick"] = must("deposit", c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 1_000_000), treasury))
	must("later register", c.Call(scid, "Register", nil, v2In(scid, 1), users[0]))
	must("later deposit", c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 1_000_000), treasury))
	ai := c.U(scid, "ai_"+hex.EncodeToString(v2TokA[:])) - 1
	args := v2ClaimArgs(c, scid, px, ai)
	n := c.U(scid, "ar_"+s64(ai))
	if ka, kb := args[2].Value.(uint64), args[3].Value.(uint64); ka == 0 || kb == 0 || ka >= n || kb >= n {
		t.Fatalf("the measured claim must read two records from the middle: ka %d kb %d of %d", ka, kb, n)
	}
	out["Claim, closed"] = must("claim closed", c.Call(scid, "Claim", args, nil, nil))
	out["Claim, open"] = must("claim open", c.Call(scid, "Claim", v2ClaimArgs(c, scid, px+1, ai), nil, nil))
	out["Register again"] = must("x register again", c.Call(scid, "Register", nil, v2In(scid, 1), x))
	out["Deposit, new asset"] = must("deposit new", c.Call(scid, "Deposit", v2Deposit(crypto.Hash{0xe5, 0x05}), v2In(crypto.Hash{0xe5, 0x05}, 1_000_000), treasury))
	out["SetXmr 106"] = must("setxmr", c.Call(scid, "SetXmr", rpc.Arguments{harness.S("addr", "4"+strings.Repeat("A", 105))}, nil, x))
	seller, buyer := c.NewAccount(), c.NewAccount()
	must("seller mint", c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 2)}, nil, seller))
	must("list", c.Call(scid, "List", rpc.Arguments{harness.U("price", 10_000_000)}, v2In(scid, 2), seller))
	out["Buy"] = must("buy", c.Call(scid, "Buy", rpc.Arguments{harness.U("id", c.U(scid, "ln")-1), harness.U("n", 1)}, v2In(zero, 10_000_000), buyer))
	t.Logf("state: %d holders, %d assets, %d positions, %d records of tokA", c.U(scid, "hst"), c.U(scid, "an"), c.U(scid, "pn"), n)
	return out
}

func TestFoundersV2CostDoesNotGrowWithAssetsOrHolders(t *testing.T) {
	small := v2CostRun(t, 2, 0, 3)
	large := v2CostRun(t, 60, 40, 80)
	for _, k := range []string{"Deposit, new tick", "Deposit, same tick", "Deposit, new asset", "Register again", "Withdraw part", "Claim, closed", "Claim, open", "SetXmr 106", "Buy"} {
		a, b := small[k], large[k]
		t.Logf("%-20s compute %9d / %9d   storage %5d / %5d", k, a.GasCompute, b.GasCompute, a.GasStorage, b.GasStorage)
		if a.GasCompute != b.GasCompute {
			t.Errorf("%s: compute gas %d with 2 holders and 1 token, %d with 60 holders and 41 tokens", k, a.GasCompute, b.GasCompute)
		}
		if d := int64(b.GasStorage) - int64(a.GasStorage); d > 12 || d < -12 {
			t.Errorf("%s: storage gas %d against %d; only the width of a stored number may differ", k, a.GasStorage, b.GasStorage)
		}
		if b.GasCompute > 7_500_000 {
			t.Errorf("%s: %d compute gas is too close to the 10,000,000 a call has", k, b.GasCompute)
		}
	}
}

// ---- payout addresses ------------------------------------------------------------------------------------------

func TestFoundersV2PayoutAddresses(t *testing.T) {
	c := harness.New()
	creator, treasury, alice, bob := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installV2(t, c, creator, treasury, 0, 20, 0, 4)
	for _, who := range []*rpc.Address{alice, bob} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 2)}, nil, who); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
	}
	if r := c.Call(scid, "Register", nil, v2In(scid, 2), alice); !r.OK() {
		t.Fatalf("register: %v", r.Err)
	}
	xmr95 := "8" + strings.Repeat("9AbCdEfGhJ", 9) + "kmno"    // a subaddress's shape: 95 base58 characters
	xmr106 := "4" + strings.Repeat("zyxwvutsrq", 10) + "PQRST" // an integrated address's: 106
	zec := "t1" + strings.Repeat("Mn7", 11)                    // 35, starting with t
	if len(xmr95) != 95 || len(xmr106) != 106 || len(zec) != 35 {
		t.Fatalf("fixture lengths %d %d %d", len(xmr95), len(xmr106), len(zec))
	}
	set := func(entry, a string, who *rpc.Address, in map[crypto.Hash]uint64) harness.Result {
		return c.Call(scid, entry, rpc.Arguments{harness.S("addr", a)}, in, who)
	}
	xk, zk := "xm_"+alice.String(), "zc_"+alice.String()
	// only a registered holder: bob holds passes but has not registered them; a ring>2 caller has no address at all
	if r := set("SetXmr", xmr95, bob, nil); r.OK() {
		t.Fatal("an unregistered holder stored a Monero address")
	}
	if r := set("SetZec", zec, bob, nil); r.OK() {
		t.Fatal("an unregistered holder stored a Zcash address")
	}
	if r := set("SetXmr", xmr95, nil, nil); r.OK() {
		t.Fatal("a ring>2 caller stored an address")
	}
	if r := set("SetXmr", xmr95, alice, consDero(1)); r.OK() {
		t.Fatal("DERO attached to SetXmr was accepted")
	}
	if r := set("SetXmr", xmr95, alice, v2In(scid, 1)); r.OK() {
		t.Fatal("a pass attached to SetXmr was accepted")
	}
	// set, replace
	r := set("SetXmr", xmr95, alice, nil)
	if !r.OK() || c.S(scid, xk) != xmr95 {
		t.Fatalf("set xmr: %v", r.Err)
	}
	t.Logf("SetXmr(95 characters): compute %d, storage %d", r.GasCompute, r.GasStorage)
	r = set("SetXmr", xmr106, alice, nil)
	if !r.OK() || c.S(scid, xk) != xmr106 {
		t.Fatalf("replace xmr: %v", r.Err)
	}
	t.Logf("SetXmr(106 characters): compute %d, storage %d", r.GasCompute, r.GasStorage)
	if r.GasCompute > 7_500_000 {
		t.Fatalf("SetXmr(106) takes %d of the 10,000,000 compute gas a call has", r.GasCompute)
	}
	if r := set("SetZec", zec, alice, nil); !r.OK() || c.S(scid, zk) != zec {
		t.Fatalf("set zec: %v", r.Err)
	}
	// format bounds: length, and every character base58
	for _, bad := range []string{
		xmr95[:94], xmr95 + "a", xmr106[:105], xmr106 + "a", strings.Repeat("8", 200),
		xmr95[:40] + "0" + xmr95[41:], xmr95[:40] + "O" + xmr95[41:], xmr95[:40] + "I" + xmr95[41:], xmr95[:40] + "l" + xmr95[41:],
		xmr95[:40] + "+" + xmr95[41:], xmr95[:40] + " " + xmr95[41:], xmr95[:94] + "\xff", "\x00" + xmr95[1:],
	} {
		if r := set("SetXmr", bad, alice, nil); r.OK() {
			t.Fatalf("Monero address %q (%d bytes) accepted", bad, len(bad))
		}
	}
	for _, bad := range []string{zec[:34], zec + "a", "z" + zec[1:], "T" + zec[1:], zec[:20] + "0" + zec[21:], zec[:34] + "_", xmr95} {
		if r := set("SetZec", bad, alice, nil); r.OK() {
			t.Fatalf("Zcash address %q accepted", bad)
		}
	}
	if c.S(scid, xk) != xmr106 || c.S(scid, zk) != zec {
		t.Fatal("a refused call changed a stored address")
	}
	// clear, and clearing what is not there is refused
	if r := set("SetZec", "", alice, nil); !r.OK() || c.Exists(scid, zk) {
		t.Fatalf("clear zec: %v", r.Err)
	}
	if r := set("SetZec", "", alice, nil); r.OK() {
		t.Fatal("clearing an address that is not stored was accepted")
	}
	if r := set("SetXmr", "", bob, nil); r.OK() {
		t.Fatal("bob cleared an address he never stored")
	}
	// a holder who withdraws everything can no longer store one, and can still clear the one stored
	if r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("p", 0), harness.U("u", 2)}, nil, alice); !r.OK() {
		t.Fatalf("withdraw: %v", r.Err)
	}
	if c.Exists(scid, "hu_"+alice.String()) || c.S(scid, xk) != xmr106 {
		t.Fatal("after a full withdrawal hu_ should be gone and the stored address should stay until its owner clears it")
	}
	if r := set("SetXmr", xmr95, alice, nil); r.OK() {
		t.Fatal("a holder with nothing registered replaced an address")
	}
	if r := set("SetXmr", "", alice, nil); !r.OK() || c.Exists(scid, xk) {
		t.Fatalf("clear after withdrawal: %v", r.Err)
	}
}

// ---- v1 behaviours v2 keeps ------------------------------------------------------------------------------------

func TestFoundersV2KeptBehaviours(t *testing.T) {
	c := harness.New()
	creator, treasury, alice, bob, carol := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, foundersV2File)
	inst := func(signer *rpc.Address, tr string, price, max, reserve, share, mfee uint64, name, descr string) (crypto.Hash, harness.Result) {
		return c.Install(code, nil, installV2Args(tr, name, descr, "", "", price, max, 5, reserve, share, mfee), signer, harness.MaxStorageGas)
	}
	ts := treasury.String()
	// install bounds, v1's: reserve at most a fifth, supply 1..100000, price, share, fee, texts, a real treasury, ring 2
	for _, bad := range []struct {
		why                              string
		signer                           *rpc.Address
		tr                               string
		price, max, reserve, share, mfee uint64
		name, descr                      string
	}{
		{"a reserve of 5 of 20", creator, ts, 100000, 20, 5, 5000, 250, "P", "d"},
		{"no supply", creator, ts, 100000, 0, 0, 5000, 250, "P", "d"},
		{"supply 100001", creator, ts, 100000, 100001, 0, 5000, 250, "P", "d"},
		{"price above 10^13", creator, ts, 10000000000001, 20, 4, 5000, 250, "P", "d"},
		{"share above 10000", creator, ts, 100000, 20, 4, 10001, 250, "P", "d"},
		{"market fee above 1000", creator, ts, 100000, 20, 4, 5000, 1001, "P", "d"},
		{"no name", creator, ts, 100000, 20, 4, 5000, 250, "", "d"},
		{"a 241-byte description", creator, ts, 100000, 20, 4, 5000, 250, "P", strings.Repeat("x", 241)},
		{"no treasury", creator, "", 100000, 20, 4, 5000, 250, "P", "d"},
		{"a ring>2 installer", nil, ts, 100000, 20, 4, 5000, 250, "P", "d"},
	} {
		if _, r := inst(bad.signer, bad.tr, bad.price, bad.max, bad.reserve, bad.share, bad.mfee, bad.name, bad.descr); r.OK() {
			t.Fatalf("install with %s accepted", bad.why)
		}
	}
	scid, res := inst(creator, ts, 500000, 20, 4, 5000, 250, "DMZ Founders Pass", foundersV2Descr)
	if !res.OK() || res.Sent(scid, creator) != 4 || c.U(scid, "minted") != 4 || c.U(scid, "v") != 2 || c.S(scid, "kind") != "founders" {
		t.Fatalf("install: %v", res.Err)
	}
	if r := c.Call(scid, "InitializePrivate", installV2Args(alice.String(), "X", "y", "", "", 0, 100, 0, 20, 10000, 1000), nil, alice); r.OK() || c.U(scid, "price") != 500000 {
		t.Fatal("a second InitializePrivate was accepted")
	}
	units := newConsLedger(scid)
	units.apply(t, res, nil, creator)
	call := func(entry string, args rpc.Arguments, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
		r := c.Call(scid, entry, args, in, who)
		units.apply(t, r, in, who)
		return r
	}
	// mint: from `start`, exact price, within the supply, ring 2, nothing else attached
	if r := call("Mint", rpc.Arguments{harness.U("n", 2)}, consDero(1000000), alice); r.OK() {
		t.Fatal("mint before start accepted")
	}
	for c.Height < 5 {
		c.NextBlock()
	}
	for _, bad := range []struct {
		n, pay uint64
		who    *rpc.Address
		extra  bool
	}{{2, 1000001, alice, false}, {2, 999999, alice, false}, {17, 17 * 500000, alice, false}, {0, 0, alice, false}, {1, 500000, nil, false}, {1, 500000, creator, true}} {
		in := consDero(bad.pay)
		if bad.extra {
			in[scid] = 1
		}
		if r := call("Mint", rpc.Arguments{harness.U("n", bad.n)}, in, bad.who); r.OK() {
			t.Fatalf("mint %+v accepted", bad)
		}
	}
	r := call("Mint", rpc.Arguments{harness.U("n", 16)}, consDero(16*500000), alice)
	if !r.OK() || r.Sent(scid, alice) != 16 || r.Sent(zero, treasury) != 16*500000 || c.U(scid, "minted") != 20 {
		t.Fatalf("mint: %v", r.Err)
	}
	if r := call("Mint", rpc.Arguments{harness.U("n", 1)}, consDero(500000), bob); r.OK() {
		t.Fatal("mint past the supply accepted")
	}
	// register: the units sent, ring 2, no DERO
	for _, bad := range []struct {
		in  map[crypto.Hash]uint64
		who *rpc.Address
	}{{nil, alice}, {map[crypto.Hash]uint64{scid: 2, zero: 1}, alice}} {
		if r := call("Register", nil, bad.in, bad.who); r.OK() {
			t.Fatalf("register %v accepted", bad.in)
		}
	}
	// marketplace with nobody registered: the whole price goes to the seller, since no pass could take the fee
	if r := call("List", rpc.Arguments{harness.U("price", 200000)}, v2In(scid, 6), alice); !r.OK() || c.U(scid, "la") != 1 || c.U(scid, "lu_0") != 6 {
		t.Fatalf("list: %v", r.Err)
	}
	if r := call("List", rpc.Arguments{harness.U("price", 0)}, v2In(scid, 1), alice); r.OK() {
		t.Fatal("list at price 0 accepted")
	}
	r = call("Buy", rpc.Arguments{harness.U("id", 0), harness.U("n", 1)}, consDero(200000), bob)
	if !r.OK() || r.Sent(zero, alice) != 200000 || r.Sent(scid, bob) != 1 || c.U(scid, "mdep") != 0 || c.U(scid, "ad_0") != 0 {
		t.Fatalf("buy with nobody registered: %v, seller got %d", r.Err, r.Sent(zero, alice))
	}
	// registered passes take mfee bps of each sale, as DERO
	if r := call("Register", nil, v2In(scid, 4), creator); !r.OK() || c.U(scid, "hst") != 4 || c.U(scid, "hu_"+creator.String()) != 4 || c.U(scid, "pn") != 1 {
		t.Fatalf("register: %v", r.Err)
	}
	if r := call("Buy", rpc.Arguments{harness.U("id", 0), harness.U("n", 2)}, consDero(399999), carol); r.OK() {
		t.Fatal("underpaid buy accepted")
	}
	if r := call("Buy", rpc.Arguments{harness.U("id", 0), harness.U("n", 6)}, consDero(1200000), carol); r.OK() {
		t.Fatal("a buy of more than is listed accepted")
	}
	r = call("Buy", rpc.Arguments{harness.U("id", 0), harness.U("n", 2)}, consDero(400000), carol)
	if !r.OK() || r.Sent(scid, carol) != 2 || r.Sent(zero, alice) != 390000 || c.U(scid, "lu_0") != 3 || c.U(scid, "mdep") != 10000 {
		t.Fatalf("buy: %v", r.Err)
	}
	if c.U(scid, "ac_0") != 2500 || c.U(scid, "ap_0") != 10000 || c.U(scid, "au_0") != 0 || c.SCBalance(scid, zero) != 10000 {
		t.Fatalf("market fee not credited to the 4 registered passes: ac %d ap %d", c.U(scid, "ac_0"), c.U(scid, "ap_0"))
	}
	if r := call("Unlist", rpc.Arguments{harness.U("id", 0)}, nil, carol); r.OK() {
		t.Fatal("unlist by a stranger accepted")
	}
	if r := call("Unlist", rpc.Arguments{harness.U("id", 0)}, v2In(scid, 1), alice); r.OK() {
		t.Fatal("unlist with a pass attached accepted")
	}
	if r := call("Unlist", rpc.Arguments{harness.U("id", 0)}, nil, alice); !r.OK() || r.Sent(scid, alice) != 3 || c.U(scid, "la") != 0 || c.Exists(scid, "ls_0") {
		t.Fatalf("unlist: %v", r.Err)
	}
	// the whole of a listing: its keys go
	if r := call("List", rpc.Arguments{harness.U("price", 100000)}, v2In(scid, 2), alice); !r.OK() {
		t.Fatalf("list 2: %v", r.Err)
	}
	if r := call("Buy", rpc.Arguments{harness.U("id", 1), harness.U("n", 2)}, consDero(200000), bob); !r.OK() || c.Exists(scid, "lu_1") || c.U(scid, "la") != 0 {
		t.Fatalf("buy all: %v", r.Err)
	}
	if r := call("Claim", v2ClaimArgs(c, scid, 0, 0), nil, alice); !r.OK() || r.Sent(zero, creator) != 15000 {
		t.Fatalf("the reserve's claim of both fees: %v paid %d", r.Err, r.Sent(zero, creator))
	}
	// supply: every unit in a wallet, registered or listed, and never more than maxsupply
	if units.total()+c.U(scid, "hst") != c.U(scid, "minted") || c.U(scid, "minted") != 20 {
		t.Fatalf("units %d + %d != %d", units.total(), c.U(scid, "hst"), c.U(scid, "minted"))
	}
}

// ---- FEE-01 and FEE-02 are closed by construction; FEE-07 is v1's ---------------------------------------------

func TestFoundersV2NothingWaitsUnowned(t *testing.T) {
	c := harness.New()
	creator, treasury, alice, bob, whale := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installV2(t, c, creator, treasury, 0, 20, 0, 4)
	for _, x := range []struct {
		a *rpc.Address
		n uint64
	}{{alice, 1}, {bob, 8}, {whale, 7}} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", x.n)}, nil, x.a); !r.OK() {
			t.Fatalf("mint: %v", r.Err)
		}
	}
	// FEE-01: before any registration a deposit is refused, so there is nothing for a first registrant to sweep
	for _, a := range []crypto.Hash{zero, v2TokA} {
		if r := c.Call(scid, "Deposit", v2Deposit(a), v2In(a, 10_000_000), treasury); r.OK() {
			t.Fatalf("deposit of %x… with nobody registered accepted", a[:2])
		}
		if c.SCBalance(scid, a) != 0 {
			t.Fatal("a refused deposit left value behind")
		}
	}
	// "any number": one registered pass takes all of a deposit made while it is alone
	if r := c.Call(scid, "Register", nil, v2In(scid, 1), alice); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Deposit", v2Deposit(zero), v2In(zero, 1_000_000), treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	// a pass registered after a deposit earns nothing of it
	if r := c.Call(scid, "Register", nil, v2In(scid, 8), bob); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Claim", v2ClaimArgs(c, scid, 1, 0), nil, bob); r.OK() {
		t.Fatalf("a pass registered after the deposit was paid %d of it", r.Sent(zero, bob))
	}
	if r := c.Call(scid, "Deposit", v2Deposit(zero), v2In(zero, 900_000), treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Claim", v2ClaimArgs(c, scid, 0, 0), nil, nil); !r.OK() || r.Sent(zero, alice) != 1_100_000 {
		t.Fatalf("alice: %v %d", r.Err, r.Sent(zero, alice))
	}
	if r := c.Call(scid, "Claim", v2ClaimArgs(c, scid, 1, 0), nil, nil); !r.OK() || r.Sent(zero, bob) != 800_000 {
		t.Fatalf("bob: %v %d", r.Err, r.Sent(zero, bob))
	}
	// FEE-02: with everyone gone deposits are refused again, so no backlog forms for whoever registers next
	for p, who := range []*rpc.Address{alice, bob} {
		u := []uint64{1, 8}[p]
		if r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("p", uint64(p)), harness.U("u", u)}, nil, who); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	for i := 0; i < 90; i++ {
		if r := c.Call(scid, "Deposit", v2Deposit(zero), v2In(zero, 100000), treasury); r.OK() {
			t.Fatal("a deposit with every pass withdrawn was accepted")
		}
		c.NextBlock()
	}
	if r := c.Call(scid, "Register", nil, v2In(scid, 7), whale); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Claim", v2ClaimArgs(c, scid, 2, 0), nil, whale); r.OK() {
		t.Fatalf("the whale took %d that accrued before it registered", r.Sent(zero, whale))
	}
	if c.SCBalance(scid, zero) != 0 {
		t.Fatalf("the contract holds %d DERO nobody is owed", c.SCBalance(scid, zero))
	}
	// FEE-07, unchanged from v1 and still open: a pass registered just before a deposit takes a full share of it
	if r := c.Call(scid, "Register", nil, v2In(scid, 8), bob); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Deposit", v2Deposit(zero), v2In(zero, 1_500_000), treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Claim", v2ClaimArgs(c, scid, 3, 0), nil, nil); !r.OK() || r.Sent(zero, bob) != 800_000 {
		t.Fatalf("FEE-07 pin: bob %v %d", r.Err, r.Sent(zero, bob))
	}
}

// ---- Claim's two record indices are checked -------------------------------------------------------------------

func TestFoundersV2HintsAreChecked(t *testing.T) {
	c := harness.New()
	creator, treasury, a, b := c.NewAccount(), c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installV2(t, c, creator, treasury, 0, 20, 0, 4)
	m := newV2Model()
	for _, who := range []*rpc.Address{a, b} {
		if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 4)}, nil, who); !r.OK() {
			t.Fatal(r.Err)
		}
	}
	reg := func(who *rpc.Address, u uint64) {
		if r := c.Call(scid, "Register", nil, v2In(scid, u), who); !r.OK() {
			t.Fatal(r.Err)
		}
		m.register(who, u)
	}
	dep := func() {
		if r := c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 1_000_000), treasury); !r.OK() {
			t.Fatal(r.Err)
		}
		m.deposit(v2TokA, 1_000_000)
	}
	reg(b, 1)
	dep()
	dep() // same tick: no new record
	reg(a, 2)
	dep()
	reg(b, 1)
	dep()
	if r := c.Call(scid, "Withdraw", rpc.Arguments{harness.U("p", 1), harness.U("u", 2)}, nil, a); !r.OK() {
		t.Fatal(r.Err)
	}
	m.withdraw(1, 2)
	dep()
	reg(b, 1)
	dep()
	i := uint64(m.idx[v2TokA])
	n := c.U(scid, "ar_"+s64(i))
	_, wantKa := m.accAt(int(i), m.pos[1].pt)
	_, wantKb := m.accAt(int(i), m.pos[1].pc)
	if n != 5 || v2Hint(c, scid, i, m.pos[1].pt) != wantKa || v2Hint(c, scid, i, m.pos[1].pc) != wantKb {
		t.Fatalf("records %d, hints %d %d", n, wantKa, wantKb)
	}
	args := func(ka, kb uint64) rpc.Arguments {
		return rpc.Arguments{harness.U("p", 1), harness.U("i", i), harness.U("ka", ka), harness.U("kb", kb)}
	}
	for _, k := range []uint64{0, 1, 2, 3, 4, 5, 6, ^uint64(0)} {
		if k != wantKa {
			if r := c.Call(scid, "Claim", args(k, wantKb), nil, nil); r.OK() {
				t.Fatalf("ka %d (right: %d) accepted", k, wantKa)
			}
		}
		if k != wantKb {
			if r := c.Call(scid, "Claim", args(wantKa, k), nil, nil); r.OK() {
				t.Fatalf("kb %d (right: %d) accepted", k, wantKb)
			}
		}
	}
	amt := m.owed(t, 1, int(i))
	if r := c.Call(scid, "Claim", args(wantKa, wantKb), nil, nil); !r.OK() || r.Sent(v2TokA, a) != amt || amt == 0 {
		t.Fatalf("the right hints: %v paid %d want %d", r.Err, r.Sent(v2TokA, a), amt)
	}
	m.claim(t, 1, int(i))
	m.check(t, c, scid, "hints", true)
}

// ---- stray value -------------------------------------------------------------------------------------------------

// Every entrypoint refuses DERO and pass units it does not take, and Claim the asset it pays. The DVM has no builtin
// that lists what a call carries (GAP-027), so an asset a call does not name is kept: the last case pins that.
func TestFoundersV2StrayValue(t *testing.T) {
	c := harness.New()
	creator, treasury, a := c.NewAccount(), c.NewAccount(), c.NewAccount()
	scid := installV2(t, c, creator, treasury, 0, 20, 0, 4)
	if r := c.Call(scid, "Mint", rpc.Arguments{harness.U("n", 4)}, nil, a); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Register", nil, v2In(scid, 2), a); !r.OK() {
		t.Fatal(r.Err)
	}
	if r := c.Call(scid, "Deposit", v2Deposit(v2TokA), v2In(v2TokA, 1000), treasury); !r.OK() {
		t.Fatal(r.Err)
	}
	for _, x := range []struct {
		entry string
		args  rpc.Arguments
		in    map[crypto.Hash]uint64
	}{
		{"Deposit", v2Deposit(v2TokA), map[crypto.Hash]uint64{v2TokA: 1000, zero: 1}},
		{"Deposit", v2Deposit(v2TokA), map[crypto.Hash]uint64{v2TokA: 1000, scid: 1}},
		{"Deposit", v2Deposit(scid), v2In(scid, 1)},
		{"Deposit", rpc.Arguments{harness.S("asset", "short")}, consDero(1000)},
		{"Claim", v2ClaimArgs(c, scid, 0, 1), consDero(1)},
		{"Claim", v2ClaimArgs(c, scid, 0, 1), v2In(scid, 1)},
		{"Claim", v2ClaimArgs(c, scid, 0, 1), v2In(v2TokA, 1)},
		{"Withdraw", rpc.Arguments{harness.U("p", 0), harness.U("u", 1)}, v2In(scid, 1)},
		{"Withdraw", rpc.Arguments{harness.U("p", 0), harness.U("u", 1)}, consDero(1)},
		{"Register", nil, map[crypto.Hash]uint64{scid: 1, zero: 1}},
		{"List", rpc.Arguments{harness.U("price", 5)}, map[crypto.Hash]uint64{scid: 1, zero: 1}},
		{"SetZec", rpc.Arguments{harness.S("addr", "t1"+strings.Repeat("a", 33))}, v2In(scid, 1)},
	} {
		if r := c.Call(scid, x.entry, x.args, x.in, a); r.OK() {
			t.Fatalf("%s with %v attached accepted", x.entry, x.in)
		}
	}
	if c.SCBalance(scid, zero) != 0 || c.SCBalance(scid, v2TokA) != 1000 {
		t.Fatal("a refused call left value behind")
	}
	// the residual: tokB attached to Register, which names no asset but the pass and DERO, is kept
	if r := c.Call(scid, "Register", nil, map[crypto.Hash]uint64{scid: 1, v2TokB: 777}, a); !r.OK() {
		t.Fatal(r.Err)
	}
	if c.SCBalance(scid, v2TokB) != 777 || c.Exists(scid, "ai_"+hex.EncodeToString(v2TokB[:])) {
		t.Fatal("GAP-027 residual: the unnamed asset should be kept, outside every pool")
	}
}

// ---- install gas, the description, the code's hash, no owner ----------------------------------------------------

func TestFoundersV2InstallGas(t *testing.T) {
	if len(foundersV2Descr) != 227 {
		t.Fatalf("the approved description is %d bytes, not 227", len(foundersV2Descr))
	}
	code := readContract(t, foundersV2File)
	for _, x := range []struct {
		why                     string
		name, descr, icon, file string
	}{
		{"the mainnet shape", "DMZ Founders Pass", foundersV2Descr, "", ""},
		{"every header at its maximum", strings.Repeat("n", 64), strings.Repeat("d", 240), strings.Repeat("i", 200), strings.Repeat("f", 200)},
	} {
		c := harness.New()
		creator, treasury := c.NewAccount(), c.NewAccount()
		scid, r := c.Install(code, nil, installV2Args(treasury.String(), x.name, x.descr, x.icon, x.file, 100000, 20, 0, 4, 5000, 250), creator, harness.MaxStorageGas)
		if !r.OK() {
			t.Fatalf("%s: %v", x.why, r.Err)
		}
		t.Logf("founders v2, %s: %d bytes installed, install storage gas %d, compute %d", x.why, len(code), r.GasStorage, r.GasCompute)
		if r.GasStorage >= harness.MaxStorageGas {
			t.Fatalf("%s: install storage gas %d is not under the %d cap", x.why, r.GasStorage, harness.MaxStorageGas)
		}
		if c.S(scid, "descrHdr") != x.descr {
			t.Fatal("description not stored")
		}
	}
}

func TestFoundersV2CanonicalHash(t *testing.T) {
	b, err := os.ReadFile("../contracts/" + foundersV2File)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(ref.Canon(string(b))))
	if got := hex.EncodeToString(h[:]); got != foundersV2CanonSha256 {
		t.Fatalf("sha256(Canon(contracts/%s)) = %s, pinned %s: a changed contract needs its pin, contracts/README.md and docs/FOUNDERS_V2_NOTES.md changed with it", foundersV2File, got, foundersV2CanonSha256)
	}
}

// No owner, no setter, no upgrade: the numbers and texts are written by InitializePrivate and by nothing else, no
// function reads the creator, and nothing can replace the code.
func TestFoundersV2HasNoOwnerAndNoSetter(t *testing.T) {
	b, err := os.ReadFile("../contracts/" + foundersV2File)
	if err != nil {
		t.Fatal(err)
	}
	src := ref.Canon(string(b)) // the code the chain holds: the header comment may name what the code lacks
	fixed := map[string]bool{"nameHdr": true, "descrHdr": true, "iconURLHdr": true, "fileURL": true, "typeHdr": true, "kind": true, "v": true,
		"creator": true, "treasury": true, "price": true, "maxsupply": true, "start": true, "reserve": true, "share": true, "mfee": true, "initd": true}
	seen := map[string]bool{}
	for name, f := range parseBas(src) {
		for _, n := range f.nums {
			for _, cl := range callsIn(f.body[n]) {
				if cl.name == "UPDATE_SC_CODE" {
					t.Fatalf("%s calls UPDATE_SC_CODE", name)
				}
				if (cl.name == "STORE" || cl.name == "DELETE") && len(cl.args) > 0 {
					if k := strLit.FindStringSubmatch(cl.args[0]); k != nil && fixed[k[1]] {
						if name != "InitializePrivate" || cl.name == "DELETE" {
							t.Fatalf("%s writes %q, which only InitializePrivate may", name, k[1])
						}
						seen[k[1]] = true
					}
				}
				if cl.name == "LOAD" && len(cl.args) == 1 && cl.args[0] == `"creator"` {
					t.Fatalf("%s reads the creator: v2 gives it no power", name)
				}
			}
		}
	}
	if len(seen) != len(fixed) {
		t.Fatalf("InitializePrivate writes %d of the %d fixed keys", len(seen), len(fixed))
	}
	if strings.Contains(src, `"owner"`) || strings.Contains(src, "UpdateCode") {
		t.Fatal("v2 mentions an owner or UpdateCode")
	}
}
