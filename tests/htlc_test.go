package tests

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

// The three secrets of one swap as the companion hands them to the contract: 32 raw bytes each (an ed25519 scalar or
// 32 random bytes), passed as 64 hex characters and hashed raw. sa is Alice's Monero key share, kb Bob's gate secret,
// sb Bob's key share; ha/hk/hb are the hex sha256 the lock is made under.
type htlcSecrets struct{ sa, ha, kb, hk, sb, hb string }

// preimage is deterministic so a failure prints the same bytes every run; seed keeps the three secrets apart.
func preimage(seed byte) (secret, hash string) {
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(i)*seed + seed
	}
	sum := sha256.Sum256(raw[:])
	return hex.EncodeToString(raw[:]), hex.EncodeToString(sum[:])
}

// A well-formed secret that hashes to none of the three: the shape the contract must refuse on content, not length.
var wrongSecret = strings.Repeat("00", 32)

// htlcOneCapital is h with its first letter in capitals and everything else as it was: the smallest change that
// makes a hash no lower-case preimage hash can equal (a leading digit has no capital, so the first letter is used).
func htlcOneCapital(h string) string {
	for i, c := range h {
		if c >= 'a' && c <= 'f' {
			return h[:i] + strings.ToUpper(string(c)) + h[i+1:]
		}
	}
	return h
}

// htlcEnv is one installed htlc.bas and the parties of one swap: bob makes (he has the DERO), alice takes (she has
// the XMR), carol is nobody. Every test installs its own, so a closing balance of zero means "this path left nothing".
type htlcEnv struct {
	c                 *harness.Chain
	scid              crypto.Hash
	bob, alice, carol *rpc.Address
	htlcSecrets
}

func newHtlc(t *testing.T) *htlcEnv {
	t.Helper()
	c := harness.New()
	e := &htlcEnv{c: c, bob: c.NewAccount(), alice: c.NewAccount(), carol: c.NewAccount()}
	code := readContract(t, "htlc.bas")
	var res harness.Result
	e.scid, res = c.Install(code, nil, nil, e.bob, harness.MaxStorageGas)
	if !res.OK() {
		t.Fatalf("install: %v", res.Err)
	}
	// Install gas is charged on the canonical bytes against a 20,000 cap. The number is logged so the README's table
	// can be kept honest, and the test fails on the same edit that would fail on the chain.
	t.Logf("htlc.bas: %d bytes, install storage gas %d", len(code), res.GasStorage)
	if res.GasStorage >= 20000 {
		t.Fatalf("install storage gas %d does not fit the 20,000 cap", res.GasStorage)
	}
	if c.S(e.scid, "kind") != "htlc" || c.U(e.scid, "v") != 2 || c.U(e.scid, "n") != 0 {
		t.Fatal("Initialize did not write kind/v/n")
	}
	e.sa, e.ha = preimage(3)
	e.kb, e.hk = preimage(5)
	e.sb, e.hb = preimage(7)
	return e
}

// lock is Bob's Lock under this swap's three hashes with alice as taker; who and in vary for the refusal cases.
func (e *htlcEnv) lock(t1, t2 uint64, asset string, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
	return e.c.Call(e.scid, "Lock", rpc.Arguments{harness.S("ha", e.ha), harness.S("hk", e.hk), harness.S("hb", e.hb),
		harness.S("taker", e.alice.String()), harness.U("t1", t1), harness.U("t2", t2), harness.S("asset", asset)}, in, who)
}

func (e *htlcEnv) claim(id uint64, sa, kb string, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
	return e.c.Call(e.scid, "Claim", rpc.Arguments{harness.U("id", id), harness.S("sa", sa), harness.S("kb", kb)}, in, who)
}

func (e *htlcEnv) refund(id uint64, sb string, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
	return e.c.Call(e.scid, "Refund", rpc.Arguments{harness.U("id", id), harness.S("sb", sb)}, in, who)
}

func (e *htlcEnv) punish(id uint64, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
	return e.c.Call(e.scid, "Punish", rpc.Arguments{harness.U("id", id)}, in, who)
}

// open asserts a refused call changed nothing about lock id: still state 0, nothing revealed, the value still held.
func (e *htlcEnv) open(t *testing.T, id uint64, asset crypto.Hash, v uint64) {
	t.Helper()
	k := strconv.FormatUint(id, 10)
	if e.c.U(e.scid, "st_"+k) != 0 || e.c.Exists(e.scid, "xa_"+k) || e.c.Exists(e.scid, "xb_"+k) || e.c.SCBalance(e.scid, asset) < v {
		t.Fatalf("lock %d is no longer open: st %d, balance %d", id, e.c.U(e.scid, "st_"+k), e.c.SCBalance(e.scid, asset))
	}
}

// empty asserts no exit left value behind, in DERO or in the asset.
func (e *htlcEnv) empty(t *testing.T, asset crypto.Hash) {
	t.Helper()
	if d, a := e.c.SCBalance(e.scid, zero), e.c.SCBalance(e.scid, asset); d != 0 || a != 0 {
		t.Fatalf("value left in the contract: %d DERO, %d asset", d, a)
	}
}

type refusal struct {
	what string
	call func() harness.Result
}

// The swap itself: Bob locks, Alice claims with both preimages before t1, and her share is on chain for Bob to read.
// Every way a claim can be wrong is tried first on the same open lock and must leave it open — the gate secret is
// what stops Alice taking the DERO before she has locked XMR, so a claim without it is the case that matters most.
func TestHtlcClaim(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	t1, t2 := c.Height+50, c.Height+100
	dero := map[crypto.Hash]uint64{zero: 100000}
	if r := e.lock(t1, t2, "", dero, e.bob); !r.OK() {
		t.Fatalf("lock: %v", r.Err)
	}
	if c.U(e.scid, "n") != 1 || c.S(e.scid, "m_0") != string(e.bob.Compressed()) || c.S(e.scid, "t_0") != string(e.alice.Compressed()) ||
		c.S(e.scid, "ha_0") != e.ha || c.S(e.scid, "hk_0") != e.hk || c.S(e.scid, "hb_0") != e.hb || c.S(e.scid, "a_0") != "" ||
		c.U(e.scid, "v_0") != 100000 || c.U(e.scid, "t1_0") != t1 || c.U(e.scid, "t2_0") != t2 || c.U(e.scid, "st_0") != 0 ||
		c.SCBalance(e.scid, zero) != 100000 {
		t.Fatal("lock stored the wrong record")
	}
	for _, tc := range []refusal{
		{"claim without kb", func() harness.Result { return e.claim(0, e.sa, "", nil, e.alice) }},
		{"claim with a wrong kb", func() harness.Result { return e.claim(0, e.sa, wrongSecret, nil, e.alice) }},
		{"claim with a wrong sa", func() harness.Result { return e.claim(0, wrongSecret, e.kb, nil, e.alice) }},
		{"claim with sa and kb swapped", func() harness.Result { return e.claim(0, e.kb, e.sa, nil, e.alice) }},
		{"claim with Bob's share as sa", func() harness.Result { return e.claim(0, e.sb, e.kb, nil, e.alice) }},
		{"claim with a short sa", func() harness.Result { return e.claim(0, e.sa[:62], e.kb, nil, e.alice) }},
		{"claim with a non-hex sa", func() harness.Result { return e.claim(0, strings.Repeat("zz", 32), e.kb, nil, e.alice) }},
		{"claim by the maker", func() harness.Result { return e.claim(0, e.sa, e.kb, nil, e.bob) }},
		{"claim by a stranger", func() harness.Result { return e.claim(0, e.sa, e.kb, nil, e.carol) }},
		{"claim at ring size > 2", func() harness.Result { return e.claim(0, e.sa, e.kb, nil, nil) }},
		{"claim with DERO attached", func() harness.Result { return e.claim(0, e.sa, e.kb, dero, e.alice) }},
		{"claim of a lock that does not exist", func() harness.Result { return e.claim(1, e.sa, e.kb, nil, e.alice) }},
	} {
		if r := tc.call(); r.OK() {
			t.Fatalf("%s accepted", tc.what)
		}
		e.open(t, 0, zero, 100000)
	}
	// The last block before t1 is still Alice's.
	c.Height = t1 - 1
	r := e.claim(0, e.sa, e.kb, nil, e.alice)
	if !r.OK() || r.Sent(zero, e.alice) != 100000 || c.U(e.scid, "st_0") != 1 || c.S(e.scid, "xa_0") != e.sa || c.Exists(e.scid, "xb_0") {
		t.Fatalf("claim: %v paid %d st %d", r.Err, r.Sent(zero, e.alice), c.U(e.scid, "st_0"))
	}
	e.empty(t, zero)
	// the Claim's storage gas is what the companion's estimate (gas + 200) and so the maker's stipend are sized on
	t.Logf("claim storage gas %d, compute gas %d", r.GasStorage, r.GasCompute)
	// Nothing twice, and the other two exits are closed to a claimed lock even inside their own windows.
	if r := e.claim(0, e.sa, e.kb, nil, e.alice); r.OK() {
		t.Fatal("double claim accepted")
	}
	c.Height = t1
	if r := e.refund(0, e.sb, nil, e.bob); r.OK() {
		t.Fatal("refund of a claimed lock accepted")
	}
	c.Height = t2
	if r := e.punish(0, nil, e.alice); r.OK() {
		t.Fatal("punish after a claim accepted")
	}
	if c.U(e.scid, "st_0") != 1 || c.Exists(e.scid, "xb_0") {
		t.Fatal("a closed exit changed the record")
	}
	e.empty(t, zero)
	// At t1 the claim is over, even with both preimages in hand; the maker's window has opened instead.
	t1b, t2b := c.Height+10, c.Height+20
	mustOK(t, e.lock(t1b, t2b, "", dero, e.bob), "lock 1")
	c.Height = t1b
	if r := e.claim(1, e.sa, e.kb, nil, e.alice); r.OK() {
		t.Fatal("claim at t1 accepted")
	}
	e.open(t, 1, zero, 100000)
	if r := e.refund(1, e.sb, nil, e.bob); !r.OK() || r.Sent(zero, e.bob) != 100000 {
		t.Fatalf("refund at t1: %v paid %d", r.Err, r.Sent(zero, e.bob))
	}
	e.empty(t, zero)
}

// Bob takes the DERO back inside [t1, t2) by revealing his share, which is what lets Alice recover XMR she locked.
// Before t1 the funds are Alice's to claim; from t2 they are hers to take; only the maker, only with sb.
func TestHtlcRefund(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	t1, t2 := c.Height+50, c.Height+100
	dero := map[crypto.Hash]uint64{zero: 100000}
	mustOK(t, e.lock(t1, t2, "", dero, e.bob), "lock")
	c.Height = t1 - 1
	if r := e.refund(0, e.sb, nil, e.bob); r.OK() {
		t.Fatal("refund before t1 accepted")
	}
	e.open(t, 0, zero, 100000)
	c.Height = t1
	for _, tc := range []refusal{
		{"refund by the taker", func() harness.Result { return e.refund(0, e.sb, nil, e.alice) }},
		{"refund by a stranger", func() harness.Result { return e.refund(0, e.sb, nil, e.carol) }},
		{"refund at ring size > 2", func() harness.Result { return e.refund(0, e.sb, nil, nil) }},
		{"refund with a wrong sb", func() harness.Result { return e.refund(0, wrongSecret, nil, e.bob) }},
		{"refund without sb", func() harness.Result { return e.refund(0, "", nil, e.bob) }},
		{"refund with Alice's share as sb", func() harness.Result { return e.refund(0, e.sa, nil, e.bob) }},
		{"refund with the gate secret as sb", func() harness.Result { return e.refund(0, e.kb, nil, e.bob) }},
		{"refund with a non-hex sb", func() harness.Result { return e.refund(0, strings.Repeat("zz", 32), nil, e.bob) }},
		{"refund with DERO attached", func() harness.Result { return e.refund(0, e.sb, dero, e.bob) }},
		{"refund of a lock that does not exist", func() harness.Result { return e.refund(1, e.sb, nil, e.bob) }},
	} {
		if r := tc.call(); r.OK() {
			t.Fatalf("%s accepted", tc.what)
		}
		e.open(t, 0, zero, 100000)
	}
	r := e.refund(0, e.sb, nil, e.bob)
	if !r.OK() || r.Sent(zero, e.bob) != 100000 || c.U(e.scid, "st_0") != 2 || c.S(e.scid, "xb_0") != e.sb || c.Exists(e.scid, "xa_0") {
		t.Fatalf("refund: %v paid %d st %d", r.Err, r.Sent(zero, e.bob), c.U(e.scid, "st_0"))
	}
	e.empty(t, zero)
	if r := e.refund(0, e.sb, nil, e.bob); r.OK() {
		t.Fatal("double refund accepted")
	}
	if r := e.claim(0, e.sa, e.kb, nil, e.alice); r.OK() {
		t.Fatal("claim of a refunded lock accepted")
	}
	c.Height = t2
	if r := e.punish(0, nil, e.alice); r.OK() {
		t.Fatal("punish after a refund accepted")
	}
	if c.U(e.scid, "st_0") != 2 || c.Exists(e.scid, "xa_0") {
		t.Fatal("a closed exit changed the record")
	}
	e.empty(t, zero)
	// The last block before t2 is still Bob's; at t2 the window has closed and only Alice can move the funds, so a
	// maker who waits too long has handed the DERO over — the incentive the punish path exists to create.
	t1b, t2b := c.Height+10, c.Height+20
	mustOK(t, e.lock(t1b, t2b, "", dero, e.bob), "lock 1")
	mustOK(t, e.lock(t1b, t2b, "", dero, e.bob), "lock 2")
	c.Height = t2b - 1
	if r := e.refund(1, e.sb, nil, e.bob); !r.OK() || r.Sent(zero, e.bob) != 100000 {
		t.Fatalf("refund at t2-1: %v paid %d", r.Err, r.Sent(zero, e.bob))
	}
	c.Height = t2b
	if r := e.refund(2, e.sb, nil, e.bob); r.OK() {
		t.Fatal("refund at t2 accepted")
	}
	e.open(t, 2, zero, 100000)
	if r := e.punish(2, nil, e.alice); !r.OK() || r.Sent(zero, e.alice) != 100000 || c.U(e.scid, "st_2") != 3 {
		t.Fatalf("punish after the missed refund window: %v paid %d", r.Err, r.Sent(zero, e.alice))
	}
	e.empty(t, zero)
}

// From t2 Alice takes the DERO with no secret: Bob had [t1, t2) to refund and did not. Not a block earlier, and
// never once the lock has already been claimed or refunded (those are asserted where they happen). Who may send
// it is v2's change and has its own test (htlc_v2_test.go): anyone, and it still pays only the taker.
func TestHtlcPunish(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	t1, t2 := c.Height+50, c.Height+100
	dero := map[crypto.Hash]uint64{zero: 100000}
	mustOK(t, e.lock(t1, t2, "", dero, e.bob), "lock")
	for _, h := range []uint64{c.Height, t1, t2 - 1} {
		c.Height = h
		if r := e.punish(0, nil, e.alice); r.OK() {
			t.Fatalf("punish at height %d (t2 %d) accepted", h, t2)
		}
		e.open(t, 0, zero, 100000)
	}
	c.Height = t2
	for _, tc := range []refusal{
		{"punish at ring size > 2", func() harness.Result { return e.punish(0, nil, nil) }},
		{"punish with DERO attached", func() harness.Result { return e.punish(0, dero, e.alice) }},
		{"punish of a lock that does not exist", func() harness.Result { return e.punish(1, nil, e.alice) }},
	} {
		if r := tc.call(); r.OK() {
			t.Fatalf("%s accepted", tc.what)
		}
		e.open(t, 0, zero, 100000)
	}
	r := e.punish(0, nil, e.alice)
	if !r.OK() || r.Sent(zero, e.alice) != 100000 || c.U(e.scid, "st_0") != 3 || c.Exists(e.scid, "xa_0") || c.Exists(e.scid, "xb_0") {
		t.Fatalf("punish: %v paid %d st %d", r.Err, r.Sent(zero, e.alice), c.U(e.scid, "st_0"))
	}
	e.empty(t, zero)
	if r := e.punish(0, nil, e.alice); r.OK() {
		t.Fatal("double punish accepted")
	}
	if r := e.refund(0, e.sb, nil, e.bob); r.OK() {
		t.Fatal("refund of a punished lock accepted")
	}
	if r := e.claim(0, e.sa, e.kb, nil, e.alice); r.OK() {
		t.Fatal("claim of a punished lock accepted")
	}
	if c.U(e.scid, "st_0") != 3 {
		t.Fatal("a closed exit changed the record")
	}
	e.empty(t, zero)
}

// A lock the protocol cannot complete must not be made: Punish pays the taker, so an anonymous lock has no exit;
// the windows must be ordered and ahead; exactly one of DERO or one asset must be sent. Every refusal refunds.
func TestHtlcLockRefusals(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	h := c.Height
	var asset crypto.Hash
	asset[0] = 9
	assetHex := hex.EncodeToString(asset[:])
	dero := map[crypto.Hash]uint64{zero: 100000}
	units := map[crypto.Hash]uint64{asset: 5000}
	lock := func(ha, hk, hb, taker string, t1, t2 uint64, a string, in map[crypto.Hash]uint64, who *rpc.Address) harness.Result {
		return c.Call(e.scid, "Lock", rpc.Arguments{harness.S("ha", ha), harness.S("hk", hk), harness.S("hb", hb),
			harness.S("taker", taker), harness.U("t1", t1), harness.U("t2", t2), harness.S("asset", a)}, in, who)
	}
	al := e.alice.String()
	for _, tc := range []refusal{
		{"lock with no taker", func() harness.Result { return lock(e.ha, e.hk, e.hb, "", h+50, h+100, "", dero, e.bob) }},
		{"lock with a taker that is not an address", func() harness.Result {
			return lock(e.ha, e.hk, e.hb, "dero1notanaddress", h+50, h+100, "", dero, e.bob)
		}},
		{"lock with t1 == now+1", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+1, h+100, "", dero, e.bob) }},
		{"lock with t1 == now", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h, h+100, "", dero, e.bob) }},
		{"lock with t1 == 0", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, 0, h+100, "", dero, e.bob) }},
		{"lock with t2 == t1", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+50, "", dero, e.bob) }},
		{"lock with t2 < t1", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+49, "", dero, e.bob) }},
		{"lock with t2 == 0", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, 0, "", dero, e.bob) }},
		{"lock with t2 beyond now+1000000", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+1000001, "", dero, e.bob) }},
		{"lock with a short ha", func() harness.Result { return lock(e.ha[:63], e.hk, e.hb, al, h+50, h+100, "", dero, e.bob) }},
		{"lock with no hk", func() harness.Result { return lock(e.ha, "", e.hb, al, h+50, h+100, "", dero, e.bob) }},
		{"lock with a long hb", func() harness.Result { return lock(e.ha, e.hk, e.hb+"0", al, h+50, h+100, "", dero, e.bob) }},
		// v2: a hash no preimage can match — capitals, or not hex — is refused, since HEX(SHA256(raw)) is lower case
		{"lock with an upper-case ha", func() harness.Result { return lock(strings.ToUpper(e.ha), e.hk, e.hb, al, h+50, h+100, "", dero, e.bob) }},
		{"lock with a mixed-case hk", func() harness.Result { return lock(e.ha, htlcOneCapital(e.hk), e.hb, al, h+50, h+100, "", dero, e.bob) }},
		{"lock with a non-hex hb", func() harness.Result { return lock(e.ha, e.hk, strings.Repeat("zz", 32), al, h+50, h+100, "", dero, e.bob) }},
		{"lock with both DERO and an asset", func() harness.Result {
			return lock(e.ha, e.hk, e.hb, al, h+50, h+100, assetHex, map[crypto.Hash]uint64{asset: 5000, zero: 1}, e.bob)
		}},
		{"lock with neither", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+100, "", nil, e.bob) }},
		{"asset lock with nothing sent", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+100, assetHex, nil, e.bob) }},
		{"asset lock with only DERO sent", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+100, assetHex, dero, e.bob) }},
		{"DERO lock with only an asset sent", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+100, "", units, e.bob) }},
		{"lock with a short asset id", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+100, assetHex[:63], units, e.bob) }},
		{"lock at ring size > 2", func() harness.Result { return lock(e.ha, e.hk, e.hb, al, h+50, h+100, "", dero, nil) }},
	} {
		if r := tc.call(); r.OK() {
			t.Fatalf("%s accepted", tc.what)
		}
		if c.U(e.scid, "n") != 0 {
			t.Fatalf("%s wrote a record", tc.what)
		}
		e.empty(t, asset)
	}
	// The edges that are inside: t1 = now+2 with t2 right behind it, and t2 exactly 1,000,000 ahead. Both are then
	// emptied through the exits their heights allow, so the check that nothing stays behind covers them too.
	mustOK(t, lock(e.ha, e.hk, e.hb, al, h+2, h+3, "", dero, e.bob), "lock at the near edge")
	mustOK(t, lock(e.ha, e.hk, e.hb, al, h+2, h+1000000, "", dero, e.bob), "lock at the far edge")
	if c.U(e.scid, "n") != 2 {
		t.Fatalf("n %d after two locks", c.U(e.scid, "n"))
	}
	c.Height = h + 3
	if r := e.punish(0, nil, e.alice); !r.OK() || r.Sent(zero, e.alice) != 100000 {
		t.Fatalf("punish at the near edge: %v", r.Err)
	}
	if r := e.refund(1, e.sb, nil, e.bob); !r.OK() || r.Sent(zero, e.bob) != 100000 {
		t.Fatalf("refund at the far edge: %v", r.Err)
	}
	e.empty(t, asset)
}

// Initialize is an ordinary exported function to the DVM, and a second call would reset n so the next lock
// overwrites an open one. tests/reentry_test.go asserts the guard for the contracts it names, and this is not one
// of them, so it is asserted here: nobody, the deployer included, can call it again.
func TestHtlcInitializeNotCallable(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	t2 := c.Height + 100
	mustOK(t, e.lock(c.Height+50, t2, "", map[crypto.Hash]uint64{zero: 100000}, e.bob), "lock")
	for _, who := range []*rpc.Address{e.carol, e.bob, nil} {
		if r := c.Call(e.scid, "Initialize", nil, nil, who); r.OK() {
			t.Fatal("Initialize callable after install")
		}
	}
	if c.U(e.scid, "n") != 1 || c.U(e.scid, "st_0") != 0 || c.U(e.scid, "v_0") != 100000 {
		t.Fatal("a second Initialize changed the state")
	}
	c.Height = t2
	mustOK(t, e.punish(0, nil, e.alice), "punish")
	e.empty(t, zero)
}

// Any DMZ asset can be the DERO leg: the same three exits, paid in the asset, with no DERO involved.
func TestHtlcAssetRoundTrip(t *testing.T) {
	e := newHtlc(t)
	c := e.c
	var asset crypto.Hash
	asset[0] = 9
	assetHex := hex.EncodeToString(asset[:])
	units := map[crypto.Hash]uint64{asset: 5000}
	t1, t2 := c.Height+50, c.Height+100
	for i := uint64(0); i < 3; i++ {
		mustOK(t, e.lock(t1, t2, assetHex, units, e.bob), "asset lock")
		k := strconv.FormatUint(i, 10)
		if c.S(e.scid, "a_"+k) != string(asset[:]) || c.U(e.scid, "v_"+k) != 5000 {
			t.Fatalf("asset lock %d stored the wrong record", i)
		}
	}
	if c.U(e.scid, "n") != 3 || c.SCBalance(e.scid, asset) != 15000 || c.SCBalance(e.scid, zero) != 0 {
		t.Fatalf("after three asset locks: n %d, asset %d, DERO %d", c.U(e.scid, "n"), c.SCBalance(e.scid, asset), c.SCBalance(e.scid, zero))
	}
	c.Height = t1 - 1
	if r := e.claim(0, e.sa, e.kb, nil, e.alice); !r.OK() || r.Sent(asset, e.alice) != 5000 || r.Sent(zero, e.alice) != 0 || c.S(e.scid, "xa_0") != e.sa {
		t.Fatalf("asset claim: %v paid %d", r.Err, r.Sent(asset, e.alice))
	}
	c.Height = t1
	if r := e.refund(1, e.sb, nil, e.bob); !r.OK() || r.Sent(asset, e.bob) != 5000 || r.Sent(zero, e.bob) != 0 || c.S(e.scid, "xb_1") != e.sb {
		t.Fatalf("asset refund: %v paid %d", r.Err, r.Sent(asset, e.bob))
	}
	c.Height = t2
	if r := e.punish(2, nil, e.alice); !r.OK() || r.Sent(asset, e.alice) != 5000 || r.Sent(zero, e.alice) != 0 {
		t.Fatalf("asset punish: %v paid %d", r.Err, r.Sent(asset, e.alice))
	}
	if c.U(e.scid, "st_0") != 1 || c.U(e.scid, "st_1") != 2 || c.U(e.scid, "st_2") != 3 {
		t.Fatal("the three exits did not record their states")
	}
	e.empty(t, asset)
}
