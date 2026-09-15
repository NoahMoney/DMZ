package tests

// contracts/promo.bas, permissionless paid visibility in one asset per auction (DERO, or a DERO-chain asset such as a
// wrapped XMR). promoModel below is the whole contract in Go; every test compares the chain with it, key by key, and
// checks conservation in the auction's asset after every call:
//   bi + sx = rf + fc + pr + held, and the contract's balance of that asset = pr + held (held = bd_ of slots whose cl_ is 0).

import (
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/dvm"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"

	ref "github.com/deroproject/derohe/veil/reference"
)

const promoFile = "promo.bas"

// The mainnet shape: 3 slots, an epoch of 4,800 blocks (a day at DERO's 18-second target), a 5 % increment; the runbook
// deploys a wXMR auction, whose minimum simdeploy sets at 0.01 XMR (10^10 of its 12-decimal units), and a DERO one would
// take 1 DERO. promoWXMR stands in for the vault's wrapped-XMR asset id.
const (
	promoK      = 3
	promoE      = 4800
	promoMinBid = 100000
	promoInc    = 500
	promoMinXMR = 10000000000
)

var promoWXMR = crypto.Hash{0xe1, 0x36, 0xe8}

type pslot struct {
	used   bool
	tg     crypto.Hash
	bd     uint64
	by     *rpc.Address
	ep, sq uint64
	cl     bool
}

type promoModel struct {
	quote                  crypto.Hash // zero: DERO
	k, e, minbid, inc      uint64
	s                      []pslot
	sq, pr, fc, bi, rf, sx uint64
	stray                  uint64 // DERO left by calls above ring 2 on an auction in another asset
}

func newPromoModel(q crypto.Hash, k, e, minbid, inc uint64) *promoModel {
	return &promoModel{quote: q, k: k, e: e, minbid: minbid, inc: inc, s: make([]pslot, 2*k)}
}

func pbps(x, f uint64) uint64 { return (x/10000)*f + ((x%10000)*f)/10000 }

func (m *promoModel) held() (h uint64) {
	for _, s := range m.s {
		if s.used && !s.cl {
			h += s.bd
		}
	}
	return
}

// bid is Bid(target, ep, raise) with v of the quote and, on an auction in another asset, dero DERO attached, by signer
// (nil: a ring above 2) at height h. It returns whether the call commits and the refund it pays (to, amount).
func (m *promoModel) bid(signer *rpc.Address, tg crypto.Hash, tlen int, ep, raise, v, dero, h uint64) (bool, *rpc.Address, uint64) {
	if signer == nil {
		if v == 0 {
			return false, nil, 0
		}
		m.pr += v
		m.sx += v
		m.stray += dero
		return true, nil, 0
	}
	if v == 0 || tlen != 32 || raise > 1 || dero != 0 {
		return false, nil, 0
	}
	cur := h / m.e
	if ep != cur {
		return false, nil, 0
	}
	b := (cur % 2) * m.k
	fr, own, lo := m.k, m.k, m.k
	var lb, lq uint64
	for j := uint64(0); j < m.k; j++ {
		s := m.s[b+j]
		if !s.used || s.ep != cur {
			if fr == m.k {
				fr = j
			}
			continue
		}
		if s.by.String() == signer.String() && s.tg == tg {
			own = j
		}
		if lo == m.k || s.bd < lb || (s.bd == lb && s.sq > lq) {
			lo, lb, lq = j, s.bd, s.sq
		}
	}
	if raise == 1 {
		if own == m.k {
			return false, nil, 0
		}
		s := &m.s[b+own]
		if s.bd+v < v {
			return false, nil, 0
		}
		s.bd += v
		m.bi += v
		return true, nil, 0
	}
	if own != m.k || v < m.minbid {
		return false, nil, 0
	}
	if fr != m.k {
		s := &m.s[b+fr]
		if s.used && !s.cl {
			m.pr += s.bd
		}
		m.put(b+fr, tg, v, signer, cur)
		return true, nil, 0
	}
	need := lb + pbps(lb, m.inc)
	if need <= lb {
		need = lb + 1
	}
	if v < need || need < lb {
		return false, nil, 0
	}
	old := m.s[b+lo].by
	m.rf += lb
	m.put(b+lo, tg, v, signer, cur)
	return true, old, lb
}

func (m *promoModel) put(i uint64, tg crypto.Hash, v uint64, by *rpc.Address, e uint64) {
	m.s[i] = pslot{used: true, tg: tg, bd: v, by: by, ep: e, sq: m.sq}
	m.sq++
	m.bi += v
}

// claim is ClaimProceeds with v of the quote and dero DERO attached (dero only on an auction in another asset), by
// signer (nil: a ring above 2) at height h: whether it commits, and what it pays.
func (m *promoModel) claim(signer *rpc.Address, v, dero, h uint64) (bool, uint64) {
	if signer != nil && (v != 0 || dero != 0) {
		return false, 0
	}
	a := m.pr + v
	cur := h / m.e
	var swept []int
	for i := range m.s {
		if m.s[i].used && m.s[i].ep < cur && !m.s[i].cl {
			a += m.s[i].bd
			swept = append(swept, i)
		}
	}
	if a == 0 {
		return false, 0
	}
	m.sx += v
	m.stray += dero
	for _, i := range swept {
		m.s[i].cl = true
	}
	m.pr = 0
	m.fc += a
	return true, a
}

// claimable is what ClaimProceeds would pay at height h with nothing attached: the number the app shows.
func (m *promoModel) claimable(h uint64) uint64 {
	a := m.pr
	for _, s := range m.s {
		if s.used && s.ep < h/m.e && !s.cl {
			a += s.bd
		}
	}
	return a
}

// ranked lists the bids placed in epoch ep as the app ranks them: bid down, then the earliest placed first.
func (m *promoModel) ranked(ep uint64) []pslot {
	var out []pslot
	for _, s := range m.s {
		if s.used && s.ep == ep {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].bd != out[b].bd {
			return out[a].bd > out[b].bd
		}
		return out[a].sq < out[b].sq
	})
	return out
}

// ---- the chain side -------------------------------------------------------------------------------------------

type promoEnv struct {
	c        *harness.Chain
	scid     crypto.Hash
	treasury *rpc.Address
	m        *promoModel
	t        *testing.T
}

func quoteArg(q crypto.Hash) string {
	if q == zero {
		return ""
	}
	return hex.EncodeToString(q[:])
}

func promoInstallArgs(k, e, minbid, inc uint64, treasury, quote string) rpc.Arguments {
	return rpc.Arguments{harness.U("slots", k), harness.U("epoch", e), harness.U("minbid", minbid), harness.U("incbps", inc), harness.S("treasury", treasury), harness.S("quote", quote)}
}

func newPromoIn(t *testing.T, q crypto.Hash, k, e, minbid, inc uint64) *promoEnv {
	t.Helper()
	c := harness.New()
	creator, treasury := c.NewAccount(), c.NewAccount()
	scid, res := c.Install(readContract(t, promoFile), nil, promoInstallArgs(k, e, minbid, inc, treasury.String(), quoteArg(q)), creator, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	c.Height = 10 * e // any epoch; tests move it explicitly
	return &promoEnv{c: c, scid: scid, treasury: treasury, m: newPromoModel(q, k, e, minbid, inc), t: t}
}

func newPromo(t *testing.T, k, e, minbid, inc uint64) *promoEnv {
	return newPromoIn(t, zero, k, e, minbid, inc)
}

func (p *promoEnv) cur() uint64 { return p.c.Height / p.m.e }

// in builds a call's attachment: v of the quote, and dero DERO (meaningful on an auction in another asset).
func (p *promoEnv) in(v, dero uint64) map[crypto.Hash]uint64 {
	in := map[crypto.Hash]uint64{}
	if dero > 0 {
		in[zero] = dero
	}
	if v > 0 {
		in[p.m.quote] += v
	}
	return in
}

func (p *promoEnv) bidX(who *rpc.Address, tg crypto.Hash, ep, raise, v, dero uint64) harness.Result {
	p.t.Helper()
	ok, to, amt := p.m.bid(who, tg, 32, ep, raise, v, dero, p.c.Height)
	r := p.c.Call(p.scid, "Bid", rpc.Arguments{harness.H("target", tg), harness.U("ep", ep), harness.U("raise", raise)}, p.in(v, dero), who)
	if r.OK() != ok {
		p.t.Fatalf("Bid(%x, ep %d, raise %d) with %d (+%d DERO) at height %d: chain ok=%v (%v), model ok=%v", tg[:2], ep, raise, v, dero, p.c.Height, r.OK(), r.Err, ok)
	}
	if ok {
		var total uint64
		for _, x := range r.Transfers {
			total += x.Amount
		}
		if to == nil && total != 0 {
			p.t.Fatalf("Bid paid %d with no refund due", total)
		}
		if to != nil && (len(r.Transfers) != 1 || r.Sent(p.m.quote, to) != amt) {
			p.t.Fatalf("Bid should refund %d of the quote to the displaced bidder as its only transfer; transfers %+v", amt, r.Transfers)
		}
	}
	p.check()
	return r
}

func (p *promoEnv) bid(who *rpc.Address, tg crypto.Hash, ep, raise, v uint64) harness.Result {
	p.t.Helper()
	return p.bidX(who, tg, ep, raise, v, 0)
}

func (p *promoEnv) claimX(who *rpc.Address, v, dero uint64) harness.Result {
	p.t.Helper()
	ok, amt := p.m.claim(who, v, dero, p.c.Height)
	r := p.c.Call(p.scid, "ClaimProceeds", nil, p.in(v, dero), who)
	if r.OK() != ok {
		p.t.Fatalf("ClaimProceeds with %d (+%d DERO) at height %d: chain ok=%v (%v), model ok=%v", v, dero, p.c.Height, r.OK(), r.Err, ok)
	}
	if ok && (len(r.Transfers) != 1 || r.Sent(p.m.quote, p.treasury) != amt) {
		p.t.Fatalf("ClaimProceeds should pay %d of the quote to the treasury as its only transfer; transfers %+v", amt, r.Transfers)
	}
	p.check()
	return r
}

func (p *promoEnv) claim(who *rpc.Address, v uint64) harness.Result {
	p.t.Helper()
	return p.claimX(who, v, 0)
}

// check compares every key with the model, and the balance and the counters with each other.
func (p *promoEnv) check() {
	p.t.Helper()
	c, s, m := p.c, p.scid, p.m
	for _, k := range []struct {
		key  string
		want uint64
	}{{"sq", m.sq}, {"pr", m.pr}, {"fc", m.fc}, {"bi", m.bi}, {"rf", m.rf}, {"sx", m.sx}} {
		if got := c.U(s, k.key); got != k.want {
			p.t.Fatalf("%s = %d, model %d", k.key, got, k.want)
		}
	}
	for i, x := range m.s {
		is := strconv.Itoa(i)
		if !x.used {
			if c.Exists(s, "ep_"+is) {
				p.t.Fatalf("slot %d exists on chain, not in the model", i)
			}
			continue
		}
		cl := uint64(0)
		if x.cl {
			cl = 1
		}
		if c.S(s, "tg_"+is) != string(x.tg[:]) || c.U(s, "bd_"+is) != x.bd || c.S(s, "by_"+is) != string(x.by.Compressed()) ||
			c.U(s, "ep_"+is) != x.ep || c.U(s, "sq_"+is) != x.sq || c.U(s, "cl_"+is) != cl {
			p.t.Fatalf("slot %d differs from the model %+v", i, x)
		}
	}
	held := m.held()
	if bal := c.SCBalance(s, m.quote); bal != m.pr+held {
		p.t.Fatalf("balance of the quote %d != pr %d + held %d", bal, m.pr, held)
	}
	if m.quote != zero && c.SCBalance(s, zero) != m.stray {
		p.t.Fatalf("DERO balance %d, model %d", c.SCBalance(s, zero), m.stray)
	}
	if m.bi+m.sx != m.rf+m.fc+m.pr+held {
		p.t.Fatalf("conservation: bi %d + sx %d != rf %d + fc %d + pr %d + held %d", m.bi, m.sx, m.rf, m.fc, m.pr, held)
	}
}

func tgt(b byte) crypto.Hash { return crypto.Hash{0x7a, b} }

// ---- tests ----------------------------------------------------------------------------------------------------

func TestPromoEntrypointsAndCanonHash(t *testing.T) {
	code := readContract(t, promoFile)
	sc, pos, err := dvm.ParseSmartContract(code)
	if err != nil {
		t.Fatalf("parse error at %s: %v", pos, err)
	}
	var got []string
	for fn := range sc.Functions {
		if fn[0] >= 'A' && fn[0] <= 'Z' {
			got = append(got, fn)
		}
	}
	sort.Strings(got)
	if want := []string{"Bid", "ClaimProceeds", "InitializePrivate"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("entrypoints %v want %v", got, want)
	}
	b, _ := os.ReadFile("../contracts/" + promoFile)
	h := sha256.Sum256([]byte(ref.Canon(string(b))))
	t.Logf("promo.bas: %d source bytes, %d installed; canonical sha256 %s", len(b), len(code), hex.EncodeToString(h[:]))
}

// Install at the mainnet shape, as a wXMR and as a DERO auction: the storage gas is under the 20,000 cap; every bound,
// a malformed quote, anything attached and a bad treasury are refused; a second InitializePrivate is refused from anyone.
func TestPromoInstallGasAndRefusals(t *testing.T) {
	c := harness.New()
	creator, treasury, stranger := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, promoFile)
	var scid crypto.Hash
	for _, q := range []struct {
		name   string
		quote  string
		minbid uint64
	}{{"wXMR", quoteArg(promoWXMR), promoMinXMR}, {"DERO", "", promoMinBid}} {
		id, res := c.Install(code, nil, promoInstallArgs(promoK, promoE, q.minbid, promoInc, treasury.String(), q.quote), creator, harness.MaxStorageGas)
		if !res.OK() {
			t.Fatal(res.Err)
		}
		t.Logf("install, a %s auction at the mainnet shape (K %d, E %d, minbid %d, incbps %d): storage gas %d, compute gas %d, %d installed bytes",
			q.name, promoK, promoE, q.minbid, promoInc, res.GasStorage, res.GasCompute, len(code))
		if res.GasStorage >= harness.MaxStorageGas {
			t.Fatalf("install storage gas %d is not under the cap %d", res.GasStorage, harness.MaxStorageGas)
		}
		want := string(zero[:])
		if q.quote != "" {
			want = string(promoWXMR[:])
		}
		if c.S(id, "quote") != want {
			t.Fatalf("%s: quote stored as %x", q.name, c.S(id, "quote"))
		}
		scid = id
	}
	for _, k := range []string{"slots", "epoch", "minbid", "incbps", "initd", "sq", "pr", "fc", "bi", "rf", "sx"} {
		if !c.Exists(scid, k) {
			t.Fatalf("install did not store %s", k)
		}
	}
	if c.S(scid, "treasury") != string(treasury.Compressed()) || c.S(scid, "kind") != "promo" {
		t.Fatal("treasury or kind not stored")
	}
	tr, wx := treasury.String(), quoteArg(promoWXMR)
	bad := []struct {
		why               string
		k, e, minbid, inc uint64
		treasury, quote   string
		in                map[crypto.Hash]uint64
	}{
		{"no slots", 0, promoE, promoMinBid, promoInc, tr, "", nil},
		{"11 slots", 11, promoE, promoMinBid, promoInc, tr, "", nil},
		{"a 19-block epoch", promoK, 19, promoMinBid, promoInc, tr, "", nil},
		{"an epoch over 1,000,000", promoK, 1000001, promoMinBid, promoInc, tr, "", nil},
		{"a zero minimum bid", promoK, promoE, 0, promoInc, tr, "", nil},
		{"a minimum bid over 10^13", promoK, promoE, 10000000000001, promoInc, tr, "", nil},
		{"a zero increment", promoK, promoE, promoMinBid, 0, tr, "", nil},
		{"an increment over 10,000 bps", promoK, promoE, promoMinBid, 10001, tr, "", nil},
		{"a treasury that is not an address", promoK, promoE, promoMinBid, promoInc, "nope", "", nil},
		{"a 63-character quote", promoK, promoE, promoMinBid, promoInc, tr, wx[:63], nil},
		{"a 65-character quote", promoK, promoE, promoMinBid, promoInc, tr, wx + "0", nil},
		{"a quote that is not hex", promoK, promoE, promoMinBid, promoInc, tr, "zz" + wx[2:], nil},
		{"DERO attached", promoK, promoE, promoMinBid, promoInc, tr, "", map[crypto.Hash]uint64{zero: 5}},
		{"DERO attached to a wXMR auction", promoK, promoE, promoMinBid, promoInc, tr, wx, map[crypto.Hash]uint64{zero: 5}},
		{"the quote attached", promoK, promoE, promoMinBid, promoInc, tr, wx, map[crypto.Hash]uint64{promoWXMR: 5}},
	}
	for _, b := range bad {
		if _, r := c.Install(code, b.in, promoInstallArgs(b.k, b.e, b.minbid, b.inc, b.treasury, b.quote), creator, harness.MaxStorageGas); r.OK() {
			t.Errorf("install with %s was accepted", b.why)
		}
	}
	for _, g := range []struct{ k, e, minbid, inc uint64 }{{1, 20, 1, 1}, {10, 1000000, 10000000000000, 10000}} {
		if _, r := c.Install(code, nil, promoInstallArgs(g.k, g.e, g.minbid, g.inc, tr, wx), creator, harness.MaxStorageGas); !r.OK() {
			t.Errorf("install at the bounds %+v refused: %v", g, r.Err)
		}
	}
	for _, who := range []*rpc.Address{stranger, creator} {
		if r := c.Call(scid, "InitializePrivate", promoInstallArgs(1, 20, 1, 1, stranger.String(), wx), nil, who); r.OK() {
			t.Errorf("InitializePrivate callable after install by %s", who.String()[:12])
		}
	}
	if c.U(scid, "slots") != promoK || c.S(scid, "treasury") != string(treasury.Compressed()) || c.S(scid, "quote") != string(zero[:]) {
		t.Fatal("a second InitializePrivate changed the terms")
	}
}

// Filling the slots, then outbidding: the lowest standing bid is displaced and refunded in the same call, in the quote;
// among equal lowest bids the latest placed goes first; a bid equal to the lowest, or short of the increment, is refused.
func TestPromoOutbidRefundsAndTies(t *testing.T) {
	for _, q := range []crypto.Hash{zero, promoWXMR} {
		p := newPromoIn(t, q, promoK, promoE, promoMinBid, promoInc)
		a, b, c3, d, e := p.c.NewAccount(), p.c.NewAccount(), p.c.NewAccount(), p.c.NewAccount(), p.c.NewAccount()
		ep := p.cur()
		p.bid(a, tgt(1), ep, 0, 100000)
		p.bid(b, tgt(2), ep, 0, 100000)
		p.bid(c3, tgt(3), ep, 0, 100000) // three equal bids: c3 is the latest placed
		for _, v := range []uint64{100000, 104999} {
			if r := p.bid(d, tgt(4), ep, 0, v); r.OK() {
				t.Fatalf("a bid of %d beat a lowest of 100,000 with a 5%% increment", v)
			}
		}
		r := p.bid(d, tgt(4), ep, 0, 105000) // exactly lowest + 5 %
		if r.Sent(q, c3) != 100000 {
			t.Fatalf("the latest of three equal lowest bids was not the one refunded: %+v", r.Transfers)
		}
		r = p.bid(e, tgt(5), ep, 0, 200000) // a and b (100,000, earlier) and d: this displaces b, the later of the two
		if r.Sent(q, b) != 100000 {
			t.Fatalf("b was not displaced: %+v", r.Transfers)
		}
		rank := p.m.ranked(ep)
		if len(rank) != 3 || rank[0].by != e || rank[1].by != d || rank[2].by != a {
			t.Fatalf("standings %+v", rank)
		}
	}
	q := newPromo(t, 1, 20, 1, 1) // the smallest bids: 1 bps rounds to nothing, and the increment is still one unit
	x, y := q.c.NewAccount(), q.c.NewAccount()
	q.bid(x, tgt(1), q.cur(), 0, 5)
	if r := q.bid(y, tgt(2), q.cur(), 0, 5); r.OK() {
		t.Fatal("an equal bid displaced a standing one")
	}
	if r := q.bid(y, tgt(2), q.cur(), 0, 6); r.Sent(zero, x) != 5 {
		t.Fatalf("a bid of lowest + 1 did not displace it: %+v", r.Transfers)
	}
}

// A bidder raises their own standing bid; a raise displaces nobody; nobody raises another's bid; a second new bid
// for the same target from the same bidder is refused (it must be a raise); a raise that would wrap is refused.
func TestPromoRaise(t *testing.T) {
	p := newPromo(t, promoK, promoE, promoMinBid, promoInc)
	a, b := p.c.NewAccount(), p.c.NewAccount()
	ep := p.cur()
	p.bid(a, tgt(1), ep, 0, 100000)
	if r := p.bid(a, tgt(1), ep, 0, 300000); r.OK() {
		t.Fatal("a second new bid for the same target by the same bidder was accepted")
	}
	if r := p.bid(b, tgt(1), ep, 1, 50000); r.OK() {
		t.Fatal("a raise of another bidder's bid was accepted")
	}
	if r := p.bid(a, tgt(2), ep, 1, 50000); r.OK() {
		t.Fatal("a raise with no standing bid for that target was accepted")
	}
	p.bid(a, tgt(1), ep, 1, 1) // any amount raises
	p.bid(a, tgt(1), ep, 1, 49999)
	if got := p.c.U(p.scid, "bd_"+strconv.FormatUint((ep%2)*promoK, 10)); got != 150000 {
		t.Fatalf("raised bid %d", got)
	}
	p.bid(b, tgt(1), ep, 0, 100000) // another bidder may bid for the same target: the unit is (bidder, target)
	p.c.Height += promoE
	if r := p.bid(a, tgt(1), p.cur(), 1, 1); r.OK() {
		t.Fatal("a raise reached a bid from the previous epoch")
	}
	w := newPromoIn(t, promoWXMR, 1, 20, 1, 1)
	z := w.c.NewAccount()
	w.bid(z, tgt(9), w.cur(), 0, 1<<63)
	if r := w.bid(z, tgt(9), w.cur(), 1, 1<<63); r.OK() {
		t.Fatal("a raise that wraps the bid was accepted")
	}
}

// Epochs roll over with no call. Bids placed in epoch N are epoch N + 1's winners; N + 1's bids go to the other bank
// and never touch them; in N + 2 a new bid reuses a finished slot and moves its bid into the proceeds.
func TestPromoEpochRolloverWithNoCalls(t *testing.T) {
	p := newPromoIn(t, promoWXMR, promoK, promoE, promoMinBid, promoInc)
	a, b, c3 := p.c.NewAccount(), p.c.NewAccount(), p.c.NewAccount()
	n := p.cur()
	p.c.Height = n*promoE + promoE - 1 // the last block of epoch n
	p.bid(a, tgt(1), n, 0, 120000)
	p.bid(b, tgt(2), n, 0, 110000)
	p.c.Height++ // epoch n+1 begins; nobody calls anything
	if r := p.bid(c3, tgt(3), n, 0, 500000); r.OK() {
		t.Fatal("a bid naming the epoch that just ended was accepted")
	}
	win := p.m.ranked(p.cur() - 1)
	if len(win) != 2 || win[0].by != a || win[1].by != b {
		t.Fatalf("winners of epoch n+1: %+v", win)
	}
	if p.m.claimable(p.c.Height) != 230000 { // on display, and final
		t.Fatalf("claimable %d", p.m.claimable(p.c.Height))
	}
	before := map[uint64]interface{}{}
	for i := uint64(0); i < 2*promoK; i++ {
		before[i] = p.c.Var(p.scid, "bd_"+strconv.FormatUint(i, 10))
	}
	for i := 0; i < 5; i++ {
		p.bid(p.c.NewAccount(), tgt(byte(10+i)), n+1, 0, uint64(100000+i*50000))
	}
	for i := uint64(0); i < promoK; i++ {
		j := (n%2)*promoK + i
		if p.c.Var(p.scid, "bd_"+strconv.FormatUint(j, 10)) != before[j] {
			t.Fatalf("a bid for epoch n+2 overwrote slot %d, which holds a bid on display", j)
		}
	}
	if w := p.m.ranked(n); len(w) != 2 {
		t.Fatalf("epoch n+1's winners changed: %+v", w)
	}
	p.c.Height += promoE // epoch n+2: a new bid reuses one of a's and b's slots, whose bids go into pr
	p.bid(c3, tgt(3), p.cur(), 0, 100000)
	if p.m.pr != 120000 {
		t.Fatalf("pr %d after reusing a finished slot", p.m.pr)
	}
	p.claim(p.c.NewAccount(), 0)
}

// ClaimProceeds is permissionless: anyone at ring 2, or nobody at a larger ring, pays exactly the final bids to the
// treasury, in the quote, never to the caller; a standing bid is not claimable; a second claim with nothing to pay is
// refused; anything attached is refused when there is a signer; with none the quote attached is paid on.
func TestPromoClaimIsPermissionless(t *testing.T) {
	for _, q := range []crypto.Hash{zero, promoWXMR} {
		p := newPromoIn(t, q, promoK, promoE, promoMinBid, promoInc)
		a, b, stranger := p.c.NewAccount(), p.c.NewAccount(), p.c.NewAccount()
		if r := p.claim(stranger, 0); r.OK() {
			t.Fatal("a claim with nothing to pay was accepted")
		}
		p.bid(a, tgt(1), p.cur(), 0, 150000)
		p.bid(b, tgt(2), p.cur(), 0, 100000)
		if r := p.claim(stranger, 0); r.OK() {
			t.Fatal("standing bids were claimable in their own epoch")
		}
		p.c.Height += promoE
		if r := p.claim(stranger, 7); r.OK() {
			t.Fatal("the quote attached at ring 2 was accepted")
		}
		if q != zero {
			if r := p.claimX(stranger, 0, 7); r.OK() {
				t.Fatal("DERO attached at ring 2 was accepted")
			}
		}
		r := p.claim(stranger, 0)
		if r.Sent(q, p.treasury) != 250000 || r.Sent(q, stranger) != 0 {
			t.Fatalf("the claim paid %+v", r.Transfers)
		}
		if r := p.claim(nil, 0); r.OK() {
			t.Fatal("a second claim with nothing left was accepted")
		}
		p.bid(a, tgt(1), p.cur(), 0, 100000)
		p.c.Height += promoE
		p.claim(nil, 3) // no signer: the attached 3 are paid on to the treasury with the winning bid
		if p.m.fc != 350003 || p.m.sx != 3 {
			t.Fatalf("fc %d sx %d", p.m.fc, p.m.sx)
		}
	}
}

// Every other refusal of Bid, each with the DVM's refund: a bad target, nothing attached, a raise flag above 1, the wrong
// epoch, below the minimum, DERO attached to an auction in another asset; and a call with no signer credits the quote
// to the proceeds, or is refused when it has none.
func TestPromoBidRefusals(t *testing.T) {
	for _, q := range []crypto.Hash{zero, promoWXMR} {
		p := newPromoIn(t, q, promoK, promoE, promoMinBid, promoInc)
		a := p.c.NewAccount()
		cur := p.cur()
		for _, x := range []struct {
			why     string
			args    rpc.Arguments
			v, dero uint64
		}{
			{"a 31-byte target", rpc.Arguments{harness.S("target", "0123456789012345678901234567890"), harness.U("ep", cur), harness.U("raise", 0)}, 100000, 0},
			{"a 33-byte target", rpc.Arguments{harness.S("target", "012345678901234567890123456789012"), harness.U("ep", cur), harness.U("raise", 0)}, 100000, 0},
			{"nothing attached", rpc.Arguments{harness.H("target", tgt(1)), harness.U("ep", cur), harness.U("raise", 0)}, 0, 0},
			{"raise 2", rpc.Arguments{harness.H("target", tgt(1)), harness.U("ep", cur), harness.U("raise", 2)}, 100000, 0},
			{"the next epoch", rpc.Arguments{harness.H("target", tgt(1)), harness.U("ep", cur+1), harness.U("raise", 0)}, 100000, 0},
			{"the previous epoch", rpc.Arguments{harness.H("target", tgt(1)), harness.U("ep", cur-1), harness.U("raise", 0)}, 100000, 0},
			{"below the minimum", rpc.Arguments{harness.H("target", tgt(1)), harness.U("ep", cur), harness.U("raise", 0)}, 99999, 0},
		} {
			if r := p.c.Call(p.scid, "Bid", x.args, p.in(x.v, x.dero), a); r.OK() {
				t.Errorf("Bid with %s was accepted", x.why)
			}
			p.check()
		}
		if q != zero {
			if r := p.bidX(a, tgt(1), cur, 0, 100000, 1); r.OK() {
				t.Fatal("DERO attached to a wXMR bid was accepted")
			}
			if r := p.bidX(a, tgt(1), cur, 0, 0, 100000); r.OK() {
				t.Fatal("a bid in DERO on a wXMR auction was accepted")
			}
		}
		if r := p.bid(nil, tgt(1), cur, 0, 0); r.OK() {
			t.Fatal("a call with no signer and nothing attached was accepted")
		}
		p.bid(nil, tgt(1), cur, 0, 100000) // no signer to refund: credited to the proceeds
		if q != zero {
			p.bidX(nil, tgt(1), cur, 0, 100, 9) // the DERO beside it stays in the contract: nothing here pays DERO
		}
		if p.c.Exists(p.scid, "ep_0") || p.c.Exists(p.scid, "ep_"+strconv.FormatUint(promoK, 10)) {
			t.Fatal("a call with no signer stored a bid")
		}
		p.claim(a, 0) // and it reaches the treasury
	}
}

// Random sequences of bids, raises, calls without a signer, claims and epoch changes, each compared with the model
// (outcome, refund, payout, every key) and checked for conservation; VEIL_FUZZ_N sets the steps per shape.
func TestPromoConservationFuzz(t *testing.T) {
	steps := 600
	if s := os.Getenv("VEIL_FUZZ_N"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			steps = n
		}
	}
	for _, shape := range []struct {
		q                 crypto.Hash
		k, e, minbid, inc uint64
	}{{promoWXMR, promoK, promoE, promoMinXMR, promoInc}, {zero, promoK, promoE, promoMinBid, promoInc}, {zero, 1, 20, 1, 1}, {promoWXMR, 2, 25, 1000, 10000}, {zero, 10, 40, 50, 250}} {
		p := newPromoIn(t, shape.q, shape.k, shape.e, shape.minbid, shape.inc)
		r := rand.New(rand.NewSource(int64(shape.k*1000 + shape.e + uint64(shape.q[0]))))
		who := []*rpc.Address{p.c.NewAccount(), p.c.NewAccount(), p.c.NewAccount(), p.c.NewAccount(), p.c.NewAccount()}
		amt := func() uint64 { return shape.minbid/2 + uint64(r.Int63n(int64(shape.minbid*3+2))) }
		dero := func() uint64 {
			if shape.q == zero || r.Intn(8) != 0 {
				return 0
			}
			return uint64(r.Intn(5))
		}
		var refunds, payouts uint64
		for i := 0; i < steps; i++ {
			switch op := r.Intn(20); {
			case op < 10:
				ep := p.cur()
				if r.Intn(12) == 0 {
					ep++
				}
				res := p.bidX(who[r.Intn(len(who))], tgt(byte(r.Intn(6))), ep, 0, amt(), dero())
				for _, x := range res.Transfers {
					refunds += x.Amount
				}
			case op < 13:
				p.bidX(who[r.Intn(len(who))], tgt(byte(r.Intn(6))), p.cur(), 1, 1+uint64(r.Int63n(int64(shape.minbid+1))), dero())
			case op < 14:
				p.bidX(nil, tgt(1), p.cur(), 0, uint64(r.Intn(3))*shape.minbid, dero())
			case op < 17:
				var caller *rpc.Address
				if r.Intn(2) == 0 {
					caller = who[r.Intn(len(who))]
				}
				v := uint64(0)
				if r.Intn(6) == 0 {
					v = uint64(r.Intn(4))
				}
				res := p.claimX(caller, v, dero())
				for _, x := range res.Transfers {
					payouts += x.Amount
				}
			default:
				p.c.Height += uint64(r.Int63n(int64(shape.e*3/2 + 1)))
			}
		}
		m := p.m
		if refunds != m.rf || payouts != m.fc {
			t.Fatalf("shape %+v: transfers out (refunds %d, payouts %d) differ from rf %d, fc %d", shape, refunds, payouts, m.rf, m.fc)
		}
		t.Logf("quote %x… K %d E %d minbid %d inc %d, %d steps: in %d, credited without signer %d, refunded %d, paid to the treasury %d, pr %d, held %d, stray DERO %d",
			shape.q[:3], shape.k, shape.e, shape.minbid, shape.inc, steps, m.bi, m.sx, m.rf, m.fc, m.pr, m.held(), m.stray)
	}
}

// Storage and compute gas of each call at the mainnet shape, in wXMR and in DERO, and the worst case at K = 10. Every
// call reads one bank (Bid) or both (ClaimProceeds), so compute grows with K and nothing else.
func TestPromoGasAtMainnetParameters(t *testing.T) {
	for _, g := range []struct {
		name   string
		q      crypto.Hash
		k      uint64
		minbid uint64
	}{{"wXMR", promoWXMR, promoK, promoMinXMR}, {"DERO", zero, promoK, promoMinBid}, {"wXMR", promoWXMR, 10, promoMinXMR}} {
		p := newPromoIn(t, g.q, g.k, promoE, g.minbid, promoInc)
		var maxFree, maxOut, maxRaise, maxClaim harness.Result
		keep := func(dst *harness.Result, r harness.Result) {
			if !r.OK() {
				t.Fatalf("%s K %d: %v", g.name, g.k, r.Err)
			}
			if r.GasCompute > dst.GasCompute {
				dst.GasCompute = r.GasCompute
			}
			if r.GasStorage > dst.GasStorage {
				dst.GasStorage = r.GasStorage
			}
		}
		for round := 0; round < 2; round++ { // the second round reuses finished slots (their bids move into pr)
			ep := p.cur()
			var last *rpc.Address
			for i := uint64(0); i < g.k; i++ {
				last = p.c.NewAccount()
				keep(&maxFree, p.bid(last, tgt(byte(i)), ep, 0, g.minbid+i))
			}
			keep(&maxRaise, p.bid(last, tgt(byte(g.k-1)), ep, 1, 1000))
			v := g.minbid * 2
			for i := uint64(0); i < g.k; i++ { // each displaces the lowest
				v = v + v/10
				keep(&maxOut, p.bid(p.c.NewAccount(), tgt(byte(100+i)), ep, 0, v))
			}
			p.c.Height += promoE
		}
		p.c.Height += promoE
		keep(&maxClaim, p.claim(p.c.NewAccount(), 0)) // 2K final slots swept in one call
		t.Logf("%s auction, K %d, E %d: Bid into a free slot %d storage / %d compute; Bid displacing %d / %d; raise %d / %d; ClaimProceeds over %d slots %d / %d",
			g.name, g.k, promoE, maxFree.GasStorage, maxFree.GasCompute, maxOut.GasStorage, maxOut.GasCompute, maxRaise.GasStorage, maxRaise.GasCompute, 2*g.k, maxClaim.GasStorage, maxClaim.GasCompute)
		for _, x := range []harness.Result{maxFree, maxOut, maxRaise, maxClaim} {
			if x.GasStorage >= harness.MaxStorageGas || x.GasCompute >= 10000000 {
				t.Fatalf("%s K %d: a call's gas is over a limit: %+v", g.name, g.k, x)
			}
		}
	}
}
