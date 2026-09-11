package xmrswap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// startWith is run.start with the config the caller wants: a second process on the same state file, or one
// started with different flags.
func startWith(r *run, role string, mod func(*Config)) *Swap {
	st := r.loadState(role)
	ch, err := NewDirChannel(filepath.Join(r.dir, "channel"), role)
	if err != nil {
		r.t.Fatal(err)
	}
	ch.Poll = time.Millisecond
	spy := &spyChannel{Channel: ch, onSend: func(to string, m Message) error { return r.checkSend(role, to, m) }}
	cfg := r.config(role)
	if mod != nil {
		mod(&cfg)
	}
	var sw *Swap
	if role == "bob" {
		sw, err = NewBob(cfg, st, newMemDero(r.chain, bobAddr), r.bobW, r.prover, spy)
	} else {
		sw, err = NewAlice(cfg, st, newMemDero(r.chain, aliceAddr), r.aliceW, r.prover, spy)
	}
	if err != nil {
		r.t.Fatal(err)
	}
	return sw
}

// driveUntil steps both sides until alice reaches want, or the round budget runs out.
func driveUntil(r *run, want string, rounds int) {
	for i := 0; i < rounds; i++ {
		if p, _ := r.phase("alice"); p == want {
			return
		}
		r.step("bob")
		r.step("alice")
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	bp, _ := r.phase("bob")
	ap, _ := r.phase("alice")
	r.t.Fatalf("alice never reached %s: bob %s, alice %s\n%s", want, bp, ap, strings.Join(r.logs, "\n"))
}

// ADVERSARY 1, inverted. The maker's timelocks now have a ceiling as well as a floor: Config.T1Max/T2Max,
// tested at the offer and at both sightings of the lock (CheckLock). A maker who names a t1 two hundred days
// out is refused before the taker's XMR goes anywhere, because until t1 that XMR has no exit of its own —
// she cannot claim without k_b, she cannot refund, and Punish is only at t2.
func TestTakerRefusesAnUnboundedT1(t *testing.T) {
	const huge = uint64(900000) // ~187 days at 18 s blocks; t2 = t1 + 10 is still inside htlc.bas's 1 000 000
	r := newRun(t, plan{scenario: "happy", seed: 1})
	r.bob = startWith(r, "bob", func(c *Config) { c.T1Delta = huge })
	r.alice = startWith(r, "alice", func(c *Config) { c.T1Max, c.T2Max = 200, 200 })
	driveUntil(r, "refused", 400)

	alice := r.loadState("alice")
	if r.aliceW.transfers != 0 {
		t.Fatalf("alice made %d Monero transfers against a t1 %d blocks wide", r.aliceW.transfers, huge)
	}
	if !alice.Done {
		t.Fatalf("alice is in phase %s and not done", alice.Phase)
	}
	t.Logf("refused before any XMR moved: %s", alice.Outcome)
}

// ADVERSARY 1b. CheckLock over t1 at the floor, inside the band, and above the ceiling.
func TestCheckLockBandsTheWindows(t *testing.T) {
	const now = uint64(1000)
	base := LockExpect{HA: "a", HK: "k", HB: "b", TakerRaw: "me", Amount: 5, T1Min: 100, T2Min: 50, T1Max: 400, T2Max: 200}
	lock := func(t1, t2 uint64) Lock {
		return Lock{HA: "a", HK: "k", HB: "b", Taker: "me", Amount: 5, T1: now + t1, T2: now + t1 + t2}
	}
	for _, c := range []struct {
		name   string
		t1, t2 uint64
		ok     bool
	}{
		{"below the t1 floor", 99, 100, false},
		{"at the t1 floor", 100, 50, true},
		{"inside the band", 250, 100, true},
		{"at the t1 ceiling", 400, 200, true},
		{"above the t1 ceiling", 401, 100, false},
		{"far above the t1 ceiling", 900000, 100, false},
		{"above the t2 ceiling", 200, 201, false},
	} {
		err := CheckLock(lock(c.t1, c.t2), now, base)
		if (err == nil) != c.ok {
			t.Fatalf("%s (t1 +%d, t2 +%d): err %v, want ok=%v", c.name, c.t1, c.t2, err, c.ok)
		}
		t.Logf("%-26s t1 +%-7d t2 +%-4d -> %v", c.name, c.t1, c.t2, err)
	}
}

// ADVERSARY 2, inverted. Two companions on one state file pay once between them. The Monero lock now re-reads
// the state file in the last moment before the transfer: the durable intent is the one on disk, and a phase
// that has moved on is another process's work. (The other half of the fix is in cmd/xmrswap: the lock file is
// stamped on every pass, so a live companion never looks like a dead one.)
func TestTwoCompanionsOnOneStateFileLockXmrOnce(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 2})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "saw_dero_lock", 200)
	r.fw.Fund(r.aliceXmr, testXmr+FakeFee+1) // she must be able to pay twice for the test to see two payments
	r.fw.Mine(12)
	// the run's own transfer hook asserts the on-disk phase at each payment; here the point is the payments
	// themselves, so it stands down and the count speaks
	r.aliceW.onTransfer = nil

	a1 := startWith(r, "alice", nil)
	a2 := startWith(r, "alice", nil) // the second process: same state file, same channel
	// long enough for the maker's stipend (Path 0) to be paid and to reach stable depth, which is what the XMR
	// lock waits for, and for both processes to reach the payment
	for i := 0; i < 40; i++ {
		r.step("bob")
		if _, err := a1.Step(r.ctx); err != nil {
			r.logf("a1: %v", err)
		}
		// the second process is expected to refuse the payment once the first has moved the phase on
		if _, err := a2.Step(r.ctx); err != nil {
			r.logf("a2: %v", err)
		}
		r.chain.Advance(1)
		r.fw.Mine(1)
	}
	if r.aliceW.transfers != 1 {
		t.Fatalf("%d Monero transfers from two companions on one state file, want exactly one", r.aliceW.transfers)
	}
	alice := r.loadState("alice")
	n, amt := r.paidTo(r.aliceXmr, alice.Joint)
	if n != 1 || amt != testXmr {
		t.Fatalf("the joint address was paid %d times, %d piconero in total; want one payment of %d", n, amt, testXmr)
	}
	t.Logf("two companions on one state file paid the joint address once, %d piconero", amt)
}

// poolDaemon is fakeDaemon with a mempool that is not the contract: the first transfer is accepted, given a
// txid and reported in_pool, and executes only when release is called -- which is what a DERO node does with a
// transaction it has not mined yet, and what DERO.GetSC therefore cannot show.
type poolDaemon struct {
	d      *fakeDaemon
	mu     sync.Mutex
	parked *rpcReq
	held   string
}

func (p *poolDaemon) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req rpcReq
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	env := map[string]interface{}{"jsonrpc": "2.0", "id": "1"}
	p.mu.Lock()
	if req.Method == "transfer" && p.parked == nil && p.held == "" {
		c := p.d.chain
		c.mu.Lock()
		c.txSeq++
		sum := sha256.Sum256([]byte("parked " + strconv.FormatUint(c.txSeq, 10)))
		id := hex.EncodeToString(sum[:])
		c.pool[id] = true
		c.mu.Unlock()
		cp := req
		p.parked, p.held = &cp, id
		p.mu.Unlock()
		env["result"] = map[string]interface{}{"txid": id}
		json.NewEncoder(w).Encode(env)
		return
	}
	p.mu.Unlock()
	res, rpcErr := p.d.handle(req)
	if rpcErr != "" {
		env["error"] = map[string]interface{}{"code": -32000, "message": rpcErr}
	} else {
		env["result"] = res
	}
	json.NewEncoder(w).Encode(env)
}

// release mines the parked transaction, as a node eventually does.
func (p *poolDaemon) release() {
	p.mu.Lock()
	req := p.parked
	p.parked = nil
	held := p.held
	p.mu.Unlock()
	if req == nil {
		return
	}
	c := p.d.chain
	c.mu.Lock()
	delete(c.pool, held)
	c.mined[held] = c.height
	c.mu.Unlock()
	p.d.run(c.Height(), req.Params)
	c.Advance(1)
}

// ADVERSARY 3, inverted. FindLock reads executed contract state and cannot see a transaction still in the
// mempool, so the client now remembers the txid it submitted and asks the daemon after it by hash before it
// would send another. A Lock parked in the pool therefore produces one lock, not two, and the id the client
// finally returns is that transaction's own.
func TestLockIsNotDoubledWhenTheFirstTransactionIsStillInThePool(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	pd := &poolDaemon{d: &fakeDaemon{chain: chain, addr: bobAddr}}
	srv := httptest.NewServer(pd)
	defer srv.Close()
	c := NewDeroRPC(srv.URL, srv.URL, strings.Repeat("ab", 32))
	c.Poll, c.Timeout = time.Millisecond, 150*time.Millisecond

	_, ha := secretPair(t)
	hk := SHA256Hex(mustHex(strings.Repeat("11", 32)))
	hb := SHA256Hex(mustHex(strings.Repeat("22", 32)))

	h, err := c.Height(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// first attempt: accepted into the pool, never mined inside Timeout
	if _, _, err := c.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, "", 5000); err == nil {
		t.Fatal("the first Lock should have reported that its transaction did not mine")
	} else {
		t.Logf("first Lock: %v", err)
	}
	// the companion's next Step calls Lock again with the same arguments: it must not send a second one
	if _, _, err := c.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, "", 5000); err == nil {
		t.Fatal("the second Lock returned without an error while the first was still in the pool")
	} else {
		t.Logf("second Lock: %v", err)
	}
	chain.mu.Lock()
	n := len(chain.locks)
	chain.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d locks are on the chain while the only transaction is still in the pool", n)
	}
	// the first transaction lands after all, and the next attempt finds it instead of sending another
	pd.release()
	_, id, err := c.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, "", 5000)
	if err != nil {
		t.Fatalf("third Lock: %v", err)
	}
	chain.mu.Lock()
	n = len(chain.locks)
	chain.mu.Unlock()
	if n != 1 || id != 0 {
		t.Fatalf("%d locks on the chain and Lock returned id %d; want one lock, id 0", n, id)
	}
	l, err := c.ReadLock(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("one lock: id %d ha %s taker %s amount %d t1 %d st %d", id, l.HA[:8], l.Taker[:8], l.Amount, l.T1, l.State)
}

// junkLocks fills the contract with n locks, as anyone may: htlc.bas Lock is permissionless, costs one atomic
// unit of DERO plus storage gas, and nothing ever deletes a lock's ten keys.
func junkLocks(c *fakeChain, n int, taker string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	takerRaw, _ := DeroRaw(taker)
	makerRaw, _ := DeroRaw(bobAddr)
	for i := 0; i < n; i++ {
		h := SHA256Hex([]byte("junk " + strconv.Itoa(i)))
		c.locks = append(c.locks, Lock{ID: uint64(len(c.locks)), Maker: makerRaw, Taker: takerRaw,
			HA: h, HK: h, HB: h, Amount: 1, T1: c.height + 10, T2: c.height + 20})
	}
}

// ADVERSARY 4a, inverted. htlc.bas is a permissionless, append-only bulletin: a Lock costs one atomic unit and
// storage gas, adds ten keys, and is never deleted, so its whole state grows without bound and reading it whole
// -- DERO.GetSC with variables:true, capped at 8 MB by call() -- eventually fails for every companion on that
// scid, for ever. The client now reads keys by name (keysstring, vendor/derohe/rpc/daemon_rpc.go:238), so the
// size of a read is set by what was asked for. Padded past any cap, ReadLock, FindLock and Lock still work.
func TestKeyedReadsSurviveAPaddedContract(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	d := &fakeDaemon{chain: chain, addr: bobAddr}
	srv := httptest.NewServer(d)
	defer srv.Close()
	c := NewDeroRPC(srv.URL, srv.URL, strings.Repeat("ab", 32))
	c.Poll, c.Timeout = time.Millisecond, time.Second

	junkLocks(chain, 1, aliceAddr)
	// measure the wire size of the whole contract with one lock in it, then pad past the 8 MB cap
	res, _ := d.handle(rpcReq{Method: "DERO.GetSC", Params: map[string]interface{}{"variables": true}})
	b, _ := json.Marshal(res)
	per := len(b)
	need := 2 * ((8 << 20) / per)
	t.Logf("one lock is %d bytes of the whole contract; %d locks pass the 8 MB cap", per, need)
	junkLocks(chain, need, aliceAddr)

	l, err := c.ReadLock(ctx, 0)
	if err != nil {
		t.Fatalf("ReadLock(0) with %d locks: %v", need+1, err)
	}
	t.Logf("ReadLock(0) with %d locks: t1 %d amount %d st %d", need+1, l.T1, l.Amount, l.State)
	if _, err := c.ContractBalance(ctx); err != nil {
		t.Fatalf("ContractBalance: %v", err)
	}
	// and a whole-contract read is still what a padded contract kills, which is why nothing does one
	var whole scState
	if err := c.call(ctx, c.Daemon, "DERO.GetSC", map[string]interface{}{"scid": c.SCID, "variables": true}, &whole); err == nil {
		t.Fatal("the padded contract still came back whole: the test did not build the case")
	} else {
		t.Logf("reading the whole contract still fails, as it must: %v", err)
	}

	ha, hk, hb := strings.Repeat("cd", 32), strings.Repeat("ce", 32), strings.Repeat("cf", 32)
	txid, id, err := c.Lock(ctx, ha, hk, hb, aliceAddr, 1050, 1100, "", 5000)
	if err != nil {
		t.Fatalf("Lock on a padded contract: %v", err)
	}
	t.Logf("Lock on a contract of %d locks: id %d tx %s", need+2, id, txid[:8])
	makerRaw, _ := DeroRaw(bobAddr)
	if got, found, err := c.FindLock(ctx, makerRaw, ha, hk, hb); err != nil || !found || got.ID != id {
		t.Fatalf("FindLock: id %d found %v err %v", got.ID, found, err)
	}
}

// blindDero is a Dero whose reads fail, which is what every companion on a padded contract has.
type blindDero struct {
	Dero
	blind bool
}

func (b *blindDero) ReadLock(ctx context.Context, id uint64) (Lock, error) {
	if b.blind {
		return Lock{}, errContractTooBig
	}
	return b.Dero.ReadLock(ctx, id)
}

var errContractTooBig = errors.New("DERO.GetSC: decode: unexpected end of JSON input")

// ADVERSARY 4b, inverted. A read that fails must not cost the refund window. Bob's refund_window used to read
// the lock first and return the read error, so a maker who could not read never sent Refund and at t2 the
// taker -- who needs no companion, no secret and no XMR, only the lock id -- took the whole locked amount with
// Punish. Now the Refund goes out without a readable answer: the contract turns it down harmlessly if the lock
// has already moved, and a maker who cannot read still gets his DERO back before t2.
func TestABlindMakerStillRefundsBeforeT2(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 4})
	blind := &blindDero{}
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	// drive far enough that the DERO is locked and announced
	for i := 0; i < 200; i++ {
		if p, _ := r.phase("alice"); p == "saw_dero_lock" {
			break
		}
		r.step("bob")
		r.step("alice")
		r.chain.Advance(1)
	}
	st := r.loadState("bob")
	if st.Lock.T1 == 0 {
		t.Fatal("no lock")
	}
	t.Logf("bob locked %d atomic units, t1 %d t2 %d, height %d", st.Lock.Amount, st.Lock.T1, st.Lock.T2, r.chain.Height())

	// the attacker pads the contract; alice locks no XMR and simply waits
	blind.Dero = newMemDero(r.chain, bobAddr)
	blind.blind = true
	bob, err := NewBob(r.config("bob"), r.loadState("bob"), blind, r.bobW, r.prover, r.bob.ch)
	if err != nil {
		t.Fatal(err)
	}
	for r.chain.Height() < st.Lock.T2+1 {
		if _, err := bob.Step(r.ctx); err != nil {
			r.logf("bob step: %v", err)
		}
		r.chain.Advance(1)
	}
	l, err := r.chain.read(st.Lock.ID)
	if err != nil {
		t.Fatal(err)
	}
	if l.State != LockRefunded {
		t.Fatalf("the lock is in state %d at height %d, past t2 %d: a maker who cannot read never refunded (bob is in phase %s)",
			l.State, r.chain.Height(), st.Lock.T2, bob.Phase())
	}
	t.Logf("the maker refunded %d atomic units without ever reading the contract; bob is in phase %s", l.Amount, bob.Phase())
	// and the taker, who locked nothing, can no longer punish
	aliceRaw, _ := DeroRaw(aliceAddr)
	r.chain.mu.Lock()
	err = r.chain.punish(r.chain.height, aliceRaw, st.Lock.ID)
	r.chain.mu.Unlock()
	if err == nil {
		t.Fatal("Punish worked on a refunded lock")
	}
	if r.aliceW.transfers != 0 {
		t.Fatalf("alice made %d Monero transfers; the point is that she made none", r.aliceW.transfers)
	}
}

// ADVERSARY 5, inverted. A resume already checked the role and the contract, and on the Monero side that the
// open wallet is the one the swap belongs to. The DERO wallet is now checked the same way: the lock names one
// maker and one taker, only those wallets can Refund or Claim it, so a companion pointed at another -dero
// wallet is refused at construction rather than left with a lock it can never move.
func TestAResumeAgainstADifferentDeroWalletIsRefused(t *testing.T) {
	r := newRun(t, plan{scenario: "happy", seed: 5})
	r.bob = startWith(r, "bob", nil)
	r.alice = startWith(r, "alice", nil)
	driveUntil(r, "saw_dero_lock", 200)
	st := r.loadState("bob")
	if st.Lock.Amount == 0 || st.MyDero != bobAddr {
		t.Fatalf("no lock yet: %+v", st.Lock)
	}
	cfg := r.config("bob")
	cfg.MyDero = aliceAddr // the operator points -dero at another wallet
	_, err := NewBob(cfg, r.loadState("bob"), newMemDero(r.chain, aliceAddr), r.bobW, r.prover, r.bob.ch)
	if err == nil {
		t.Fatal("a resume against another DERO wallet was accepted")
	}
	if !strings.Contains(err.Error(), st.MyDero) || !strings.Contains(err.Error(), cfg.MyDero) {
		t.Fatalf("the refusal names neither wallet: %v", err)
	}
	t.Logf("refused: %v", err)
	// the companion the state belongs to is still accepted and still refunds
	if _, err := NewBob(r.config("bob"), r.loadState("bob"), newMemDero(r.chain, bobAddr), r.bobW, r.prover, r.bob.ch); err != nil {
		t.Fatalf("the right wallet was refused too: %v", err)
	}
}

// ADVERSARY 3b, inverted. One s_a and one k_b open every lock carrying their hashes, so a doubled lock is
// claimed twice for one Monero payment. There is no second lock to claim now: the pooled attempt produces one
// lock and the taker takes the agreed amount, once.
func TestTheLockIsClaimedOnceWithOneSecret(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1000)
	pd := &poolDaemon{d: &fakeDaemon{chain: chain, addr: bobAddr}}
	srv := httptest.NewServer(pd)
	defer srv.Close()
	bobC := NewDeroRPC(srv.URL, srv.URL, strings.Repeat("ab", 32))
	bobC.Poll, bobC.Timeout = time.Millisecond, 150*time.Millisecond
	asrv := httptest.NewServer(&fakeDaemon{chain: chain, addr: aliceAddr})
	defer asrv.Close()
	aliceC := NewDeroRPC(asrv.URL, asrv.URL, strings.Repeat("ab", 32))
	aliceC.Poll, aliceC.Timeout = time.Millisecond, time.Second

	sa, ha := secretPair(t)
	kb := strings.Repeat("11", 32)
	hk := SHA256Hex(mustHex(kb))
	hb := SHA256Hex(mustHex(strings.Repeat("22", 32)))
	h, err := bobC.Height(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := bobC.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, "", 5000); err == nil {
		t.Fatal("expected the first Lock to report it did not mine")
	}
	if _, _, err := bobC.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, "", 5000); err == nil {
		t.Fatal("the second Lock went out while the first was still in the pool")
	}
	pd.release()
	if _, _, err := bobC.Lock(ctx, ha, hk, hb, aliceAddr, h+50, h+100, "", 5000); err != nil {
		t.Fatalf("the attempt after the pool cleared: %v", err)
	}

	chain.mu.Lock()
	n := len(chain.locks)
	chain.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d locks carry these hashes, want one", n)
	}
	if _, err := aliceC.Claim(ctx, 0, sa, kb, claimCutoff(h+50, DefaultClaimMargin)); err != nil {
		t.Fatalf("claim: %v", err)
	}
	l, err := aliceC.ReadLock(ctx, 0)
	if err != nil || l.State != LockClaimed {
		t.Fatalf("lock 0 state %d %v", l.State, err)
	}
	if _, err := aliceC.ReadLock(ctx, 1); !errors.Is(err, ErrNoSuchLock) {
		t.Fatalf("a second lock exists: %v", err)
	}
	if l.Amount != 5000 {
		t.Fatalf("the taker took %d, want the one lock's 5000", l.Amount)
	}
	t.Logf("one s_a and one k_b claimed the one lock: %d atomic units for one Monero payment of the agreed size", l.Amount)
}
