package tests

// htlc.bas v3: the operator's MT-J13(b), which is docs/ATOMIC_SWAP_STUDY.md §5 decision 17 (a) (U13), and MT-A03(a).
// - Punish is still open to anyone and still pays only the taker, but only in [t2, t2 + g2).
// - From t2 + g2 the maker alone may Reclaim, by revealing s_b, which is stored as Refund stores it.
// - Lock refuses a t1 - height below w1min and a t2 - t1 below w2min.
// - w1min, w2min and g2 are Initialize arguments, each refused at 0 and past a stated ceiling.
// TestHtlcV3WindowMatrix is the liveness pin: at every height an open lock has an exit somebody may send, and the only
// heights at which that somebody is one named party with no deadline are those from t2 + g2, the maker's own window,
// which the operator's ruling MT-J11(a) accepts.

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/veil/tests/harness"
)

// Initialize takes all three widths, refuses 0 and anything past its ceilings (lines 6-7), and never accepts a pair
// of minimum widths no lock could meet. Every accepted edge is then used: the tightest lock its widths allow is
// accepted, and one block tighter is not.
func TestHtlcV3InstallArguments(t *testing.T) {
	c := harness.New()
	deployer, maker, taker := c.NewAccount(), c.NewAccount(), c.NewAccount()
	code := readContract(t, "htlc.bas")
	U := harness.U
	top := ^uint64(0)
	for _, tc := range []struct {
		what string
		args rpc.Arguments
	}{
		{"no arguments (the DVM refuses a missing parameter)", nil},
		{"no w1min", rpc.Arguments{U("w2min", 400), U("g2", 100)}},
		{"no w2min", rpc.Arguments{U("w1min", 400), U("g2", 100)}},
		{"no g2", rpc.Arguments{U("w1min", 400), U("w2min", 400)}},
		{"w1min 0", htlcInitArgs(0, 400, 100)},
		{"w2min 0", htlcInitArgs(400, 0, 100)},
		{"g2 0", htlcInitArgs(400, 400, 0)},
		{"w1min past the ceiling", htlcInitArgs(1000001, 1, 100)},
		{"w2min past the ceiling", htlcInitArgs(1, 1000001, 100)},
		{"g2 past the ceiling", htlcInitArgs(400, 400, 1000001)},
		{"w1min + w2min that no lock could meet", htlcInitArgs(500000, 500000, 100)},
		{"w1min + w2min one past the edge", htlcInitArgs(1, 999999, 100)},
		{"a w1min whose sum with w2min wraps to 1", htlcInitArgs(top, 2, 100)},
		{"a w2min whose sum with w1min wraps to 0", htlcInitArgs(2, top-1, 100)},
		{"g2 of 2^64 - 1", htlcInitArgs(400, 400, top)},
	} {
		if _, r := c.Install(code, nil, tc.args, deployer, harness.MaxStorageGas); r.OK() {
			t.Fatalf("an install with %s was accepted", tc.what)
		}
	}
	_, ha := preimage(3)
	_, hk := preimage(5)
	_, hb := preimage(7)
	lock := func(scid crypto.Hash, t1, t2 uint64) harness.Result {
		return c.Call(scid, "Lock", rpc.Arguments{harness.S("ha", ha), harness.S("hk", hk), harness.S("hb", hb),
			harness.S("taker", taker.String()), U("t1", t1), U("t2", t2), harness.S("asset", "")}, map[crypto.Hash]uint64{zero: 1000}, maker)
	}
	for _, w := range []struct{ w1, w2, g2 uint64 }{{1, 1, 1}, {1, 999998, 1000000}, {499999, 500000, 1}, {400, 400, 2400}} {
		scid, r := c.Install(code, nil, htlcInitArgs(w.w1, w.w2, w.g2), deployer, harness.MaxStorageGas)
		if !r.OK() {
			t.Fatalf("install with w1min %d, w2min %d, g2 %d: %v", w.w1, w.w2, w.g2, r.Err)
		}
		if r.GasStorage >= 20000 {
			t.Fatalf("install storage gas %d does not fit the 20,000 cap", r.GasStorage)
		}
		h := c.Height
		t1 := h + w.w1
		if w.w1 < 2 {
			t1 = h + 2 // Lock's own rule, unchanged from v2: t1 > height + 1
		}
		t2 := t1 + w.w2
		if t2 > h+1000000 {
			t.Fatalf("w1min %d, w2min %d: the tightest lock ends at h+%d, past Lock's horizon", w.w1, w.w2, t2-h)
		}
		if r := lock(scid, t1, t2-1); r.OK() {
			t.Fatalf("w1min %d, w2min %d: a t2 - t1 one block short was accepted", w.w1, w.w2)
		}
		if w.w1 > 2 {
			if r := lock(scid, t1-1, t2); r.OK() {
				t.Fatalf("w1min %d, w2min %d: a t1 one block short was accepted", w.w1, w.w2)
			}
		}
		if r := lock(scid, t1, t2); !r.OK() {
			t.Fatalf("w1min %d, w2min %d: the tightest lock the widths allow was refused: %v", w.w1, w.w2, r.Err)
		}
		t.Logf("w1min %d, w2min %d, g2 %d: install storage gas %d; the tightest lock (t1 = h+%d, t2 = h+%d) accepted, one block tighter refused",
			w.w1, w.w2, w.g2, r.GasStorage, t1-h, t2-h)
	}
}

// MT-A03(a) at the companions' values (the Monero profile's t1 minimum of 400 and T2 - T1 of 400): the contract itself
// refuses a narrower lock. A maker's companion fixes t1 from the height it builds the Lock at, and a DERO transaction
// may be mined up to 10 blocks later (derohe admits it only while block height − its build height < 11; the companion
// allows 11, one block conservative, and so does this test), so a Lock built at exactly t1 = +w1min is refused once it
// is mined a block late, while one built at the default +420 still fits 11 blocks later
// (docs/HTLC_V3_COMPANION_NOTES.md §3).
func TestHtlcV3LockWidths(t *testing.T) {
	e := newHtlcWith(t, 400, 400, 1200)
	c := e.c
	dero := map[crypto.Hash]uint64{zero: 100000}
	h := c.Height
	for _, tc := range []struct {
		what   string
		t1, t2 uint64
	}{
		{"a t1 one block short of w1min", h + 399, h + 799},
		{"a t2 - t1 one block short of w2min", h + 400, h + 799},
		{"both short", h + 399, h + 798},
		{"v2's tightest lock", h + 2, h + 3},
	} {
		if r := e.lock(tc.t1, tc.t2, "", dero, e.bob); r.OK() {
			t.Fatalf("%s was accepted", tc.what)
		}
		if c.U(e.scid, "n") != 0 {
			t.Fatalf("%s wrote a record", tc.what)
		}
		e.empty(t, zero)
	}
	mustOK(t, e.lock(h+400, h+800, "", dero, e.bob), "the lock at exactly both widths")
	mustOK(t, e.lock(h+420, h+820, "", dero, e.bob), "the companion's default lock (t1 = +420, t2 - t1 = 400)")
	c.Height = h + 1
	if r := e.lock(h+400, h+800, "", dero, e.bob); r.OK() {
		t.Fatal("a Lock built with t1 = +400 and mined one block later was accepted")
	}
	c.Height = h + 11
	mustOK(t, e.lock(h+420, h+820, "", dero, e.bob), "a default Lock mined 11 blocks after it was built")
	c.Height = h + 820
	for id := uint64(0); id < 3; id++ {
		mustOK(t, e.punish(id, nil, e.carol), "punish "+strconv.FormatUint(id, 10))
	}
	e.empty(t, zero)
}

// The liveness pin. Fourteen identical locks; at each height below, every exit is sent by the taker, the maker, a
// stranger and a ring>2 caller. An exit the windows allow is sent on a fresh lock and must pay exactly its payee; one
// they do not allow is sent on lock 0, the probe, and must be refused and leave it open. At every height at least one
// exit is allowed, and from t2 + g2 the only one allowed is the maker's Reclaim: an open lock waits on nobody but its
// own maker, and on him only from t2 + g2 (MT-J11(a)). The edges t2 + g2 - 1, t2 + g2 and t2 + g2 + 1 are all here.
func TestHtlcV3WindowMatrix(t *testing.T) {
	const G = 30
	e := newHtlcWith(t, htlcW1min, htlcW2min, G)
	c := e.c
	h0 := c.Height
	t1, t2 := h0+50, h0+100
	dero := map[crypto.Hash]uint64{zero: 100000}
	const locks = 14
	for i := 0; i < locks; i++ {
		mustOK(t, e.lock(t1, t2, "", dero, e.bob), "lock "+strconv.Itoa(i))
	}
	type party struct {
		name string
		a    *rpc.Address
	}
	parties := []party{{"the taker", e.alice}, {"the maker", e.bob}, {"a stranger", e.carol}, {"a ring>2 caller", nil}}
	allowed := func(h uint64, entry string, who *rpc.Address) bool {
		switch entry {
		case "Claim":
			return h < t1 && who == e.alice
		case "Refund":
			return h >= t1 && h < t2 && who == e.bob
		case "Punish":
			return h >= t2 && h < t2+G && who != nil
		}
		return h >= t2+G && who == e.bob
	}
	send := func(entry string, id uint64, who *rpc.Address) harness.Result {
		switch entry {
		case "Claim":
			return e.claim(id, e.sa, e.kb, nil, who)
		case "Refund":
			return e.refund(id, e.sb, nil, who)
		case "Punish":
			return e.punish(id, nil, who)
		}
		return e.reclaim(id, e.sb, nil, who)
	}
	payee := map[string]*rpc.Address{"Claim": e.alice, "Refund": e.bob, "Punish": e.alice, "Reclaim": e.bob}
	state := map[string]uint64{"Claim": 1, "Refund": 2, "Punish": 3, "Reclaim": 4}
	heights := []struct {
		name string
		h    uint64
	}{
		{"the lock's next block", h0 + 1}, {"t1 - 1", t1 - 1}, {"t1", t1}, {"t2 - 1", t2 - 1}, {"t2", t2},
		{"t2 + g2 - 1", t2 + G - 1}, {"t2 + g2", t2 + G}, {"t2 + g2 + 1", t2 + G + 1}, {"t2 + g2 + 10,000,000", t2 + G + 10000000},
	}
	next := uint64(1) // lock 0 is the probe; 1.. are spent by the exits the windows allow
	for _, at := range heights {
		c.Height = at.h
		var open []string
		for _, entry := range []string{"Claim", "Refund", "Punish", "Reclaim"} {
			for _, p := range parties {
				if !allowed(at.h, entry, p.a) {
					if r := send(entry, 0, p.a); r.OK() {
						t.Fatalf("at %s, %s by %s was accepted", at.name, entry, p.name)
					}
					e.open(t, 0, zero, 100000)
					continue
				}
				id := next
				next++
				k := strconv.FormatUint(id, 10)
				r := send(entry, id, p.a)
				if !r.OK() || r.Sent(zero, payee[entry]) != 100000 || c.U(e.scid, "st_"+k) != state[entry] {
					t.Fatalf("at %s, %s by %s: %v; paid %d, st %d", at.name, entry, p.name, r.Err, r.Sent(zero, payee[entry]), c.U(e.scid, "st_"+k))
				}
				for _, q := range parties {
					if q.a != nil && q.a != payee[entry] && r.Sent(zero, q.a) != 0 {
						t.Fatalf("at %s, %s by %s also paid %s", at.name, entry, p.name, q.name)
					}
				}
				switch entry {
				case "Claim":
					if c.S(e.scid, "xa_"+k) != e.sa || c.Exists(e.scid, "xb_"+k) {
						t.Fatalf("at %s the claim did not leave exactly s_a on chain", at.name)
					}
				case "Refund", "Reclaim":
					if c.S(e.scid, "xb_"+k) != e.sb || c.Exists(e.scid, "xa_"+k) {
						t.Fatalf("at %s the %s did not leave exactly s_b on chain", at.name, entry)
					}
				default:
					if c.Exists(e.scid, "xa_"+k) || c.Exists(e.scid, "xb_"+k) {
						t.Fatalf("at %s the punish revealed a secret", at.name)
					}
				}
				open = append(open, entry+" by "+p.name)
			}
		}
		if len(open) == 0 {
			t.Fatalf("at %s an open lock had no exit anybody may send", at.name)
		}
		if at.h >= t2+G && (len(open) != 1 || open[0] != "Reclaim by the maker") {
			t.Fatalf("at %s the exits open were %v; from t2 + g2 only the maker's Reclaim may be", at.name, open)
		}
		t.Logf("%-24s %s", at.name+":", strings.Join(open, ", "))
	}
	if next != locks {
		t.Fatalf("the matrix spent %d locks; %d were made for it", next-1, locks-1)
	}
	// the probe, refused everything above, is still its maker's to take back ten million blocks on
	mustOK(t, e.reclaim(0, e.sb, nil, e.bob), "the maker's reclaim of the probe")
	e.empty(t, zero)
}

// Reclaim, on its own: refused before t2 + g2; at t2 + g2 refused to anyone but the maker, to every wrong or malformed
// s_b, and to any value attached; then it pays the maker the lock, stores s_b exactly where Refund does, and closes
// the lock to every exit. The taker's companion reads s_b from xb_<id> and checks it against hb_<id>, as here.
func TestHtlcV3Reclaim(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	var asset crypto.Hash
	asset[0] = 9
	assetHex := hex.EncodeToString(asset[:])
	dero := map[crypto.Hash]uint64{zero: 100000}
	t1, t2 := c.Height+50, c.Height+100
	mustOK(t, e.lock(t1, t2, "", dero, e.bob), "DERO lock")
	mustOK(t, e.lock(t1, t2, assetHex, map[crypto.Hash]uint64{asset: 5000}, e.bob), "asset lock")
	for _, h := range []uint64{t1, t2 - 1, t2, t2 + htlcG2 - 1} {
		c.Height = h
		if r := e.reclaim(0, e.sb, nil, e.bob); r.OK() {
			t.Fatalf("a reclaim at height %d (t2 + g2 = %d) was accepted", h, t2+htlcG2)
		}
		e.open(t, 0, zero, 100000)
	}
	c.Height = t2 + htlcG2
	for _, tc := range []refusal{
		{"a reclaim by the taker, with s_b", func() harness.Result { return e.reclaim(0, e.sb, nil, e.alice) }},
		{"a reclaim by a stranger, with s_b", func() harness.Result { return e.reclaim(0, e.sb, nil, e.carol) }},
		{"a reclaim at ring size > 2", func() harness.Result { return e.reclaim(0, e.sb, nil, nil) }},
		{"a reclaim with a wrong sb", func() harness.Result { return e.reclaim(0, wrongSecret, nil, e.bob) }},
		{"a reclaim without sb", func() harness.Result { return e.reclaim(0, "", nil, e.bob) }},
		{"a reclaim with Alice's share as sb", func() harness.Result { return e.reclaim(0, e.sa, nil, e.bob) }},
		{"a reclaim with the gate secret as sb", func() harness.Result { return e.reclaim(0, e.kb, nil, e.bob) }},
		{"a reclaim with hb itself as sb", func() harness.Result { return e.reclaim(0, e.hb, nil, e.bob) }},
		{"a reclaim with a short sb", func() harness.Result { return e.reclaim(0, e.sb[:62], nil, e.bob) }},
		{"a reclaim with a non-hex sb", func() harness.Result { return e.reclaim(0, strings.Repeat("zz", 32), nil, e.bob) }},
		{"a reclaim with DERO attached", func() harness.Result { return e.reclaim(0, e.sb, dero, e.bob) }},
		{"a reclaim of a lock that does not exist", func() harness.Result { return e.reclaim(2, e.sb, nil, e.bob) }},
		{"a punish at t2 + g2", func() harness.Result { return e.punish(0, nil, e.alice) }},
		{"a refund at t2 + g2", func() harness.Result { return e.refund(0, e.sb, nil, e.bob) }},
		{"a claim at t2 + g2", func() harness.Result { return e.claim(0, e.sa, e.kb, nil, e.alice) }},
	} {
		if r := tc.call(); r.OK() {
			t.Fatalf("%s was accepted", tc.what)
		}
		e.open(t, 0, zero, 100000)
	}
	r := e.reclaim(0, e.sb, nil, e.bob)
	if !r.OK() || r.Sent(zero, e.bob) != 100000 || r.Sent(zero, e.alice) != 0 || c.U(e.scid, "st_0") != 4 ||
		c.S(e.scid, "xb_0") != e.sb || c.Exists(e.scid, "xa_0") {
		t.Fatalf("reclaim: %v paid %d st %d", r.Err, r.Sent(zero, e.bob), c.U(e.scid, "st_0"))
	}
	t.Logf("reclaim storage gas %d, compute gas %d", r.GasStorage, r.GasCompute)
	raw, err := hex.DecodeString(c.S(e.scid, "xb_0"))
	sum := sha256.Sum256(raw)
	if err != nil || hex.EncodeToString(sum[:]) != c.S(e.scid, "hb_0") {
		t.Fatal("the s_b the reclaim left on chain does not open hb: the taker could not recover her XMR from it")
	}
	for _, tc := range []refusal{
		{"a second reclaim", func() harness.Result { return e.reclaim(0, e.sb, nil, e.bob) }},
		{"a punish of a reclaimed lock", func() harness.Result { return e.punish(0, nil, e.carol) }},
		{"a refund of a reclaimed lock", func() harness.Result { return e.refund(0, e.sb, nil, e.bob) }},
		{"a claim of a reclaimed lock", func() harness.Result { return e.claim(0, e.sa, e.kb, nil, e.alice) }},
	} {
		if r := tc.call(); r.OK() {
			t.Fatalf("%s was accepted", tc.what)
		}
	}
	if c.U(e.scid, "st_0") != 4 || c.S(e.scid, "xb_0") != e.sb {
		t.Fatal("a closed exit changed the record")
	}
	// the asset leg: its own asset attached is refused (GAP-027), then the maker takes the asset back and no DERO moves
	if r := e.reclaim(1, e.sb, map[crypto.Hash]uint64{asset: 7}, e.bob); r.OK() {
		t.Fatal("a reclaim with the lock's asset attached was accepted")
	}
	e.open(t, 1, asset, 5000)
	if r := e.reclaim(1, e.sb, nil, e.bob); !r.OK() || r.Sent(asset, e.bob) != 5000 || r.Sent(zero, e.bob) != 0 || c.U(e.scid, "st_1") != 4 {
		t.Fatalf("asset reclaim: %v paid %d", r.Err, r.Sent(asset, e.bob))
	}
	e.empty(t, asset)
}

// The other side of the liveness pin. A v3 lock stays open for good in one way only: its maker cannot Reclaim because
// he has lost s_b, simulated here by an hb nobody holds a preimage for. Its twin shows that in [t2, t2 + g2) anyone
// could have closed it for the taker; from t2 + g2 no caller moves it by any exit, because the one exit left is the
// maker's and needs s_b. That is MT-J11(a)'s per-party window, waiting on the party it pays, not on the operator.
func TestHtlcV3OnlyTheMakersOwnWindowStaysOpen(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	lost := strings.Repeat("ab", 32) // a well-formed lower-case hash whose preimage nobody here knows
	dero := map[crypto.Hash]uint64{zero: 100000}
	t1, t2 := c.Height+50, c.Height+100
	for i := 0; i < 2; i++ {
		mustOK(t, c.Call(e.scid, "Lock", rpc.Arguments{harness.S("ha", e.ha), harness.S("hk", e.hk), harness.S("hb", lost),
			harness.S("taker", e.alice.String()), harness.U("t1", t1), harness.U("t2", t2), harness.S("asset", "")}, dero, e.bob), "lock")
	}
	c.Height = t2 + htlcG2 - 1
	if r := e.punish(0, nil, e.carol); !r.OK() || r.Sent(zero, e.alice) != 100000 {
		t.Fatalf("a stranger's punish of the twin in the taker's window: %v", r.Err)
	}
	for _, h := range []uint64{t2 + htlcG2, t2 + htlcG2 + 10000000} {
		c.Height = h
		for _, who := range []*rpc.Address{e.alice, e.bob, e.carol, nil} {
			for _, r := range []harness.Result{e.claim(1, e.sa, e.kb, nil, who), e.refund(1, e.sb, nil, who), e.punish(1, nil, who),
				e.reclaim(1, e.sb, nil, who), e.reclaim(1, wrongSecret, nil, who)} {
				if r.OK() {
					t.Fatalf("at height %d a lock whose s_b is lost was moved", h)
				}
			}
			e.open(t, 1, zero, 100000)
		}
	}
	if c.SCBalance(e.scid, zero) != 100000 {
		t.Fatalf("the contract holds %d; the lost-key lock is 100000", c.SCBalance(e.scid, zero))
	}
	t.Log("the one lock left open is the maker's own, from t2 + g2, and it waits only on his s_b; its twin was closed for the taker by a stranger inside [t2, t2 + g2)")
}
