package xmrswap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// The two simulator wallets tools/simhtlc uses (30006 = Bob, 30007 = Alice): real addresses, so DeroRaw and
// the taker comparison run on the same bytes the contract would see.
const (
	bobAddr   = "deto1qyx6datstrsx8qlmctgpxdtze4t8mu2l4rue6ql29s7ec5j9hwnv5qgfywu80"
	aliceAddr = "deto1qy05unfm5l0aeqd5w7ht6ytgxuldzny668rdmaq5gujzmmn63qw2wqqzdnfce"
)

// fakePayment is one plain DERO transfer (the stipend) as the chain holds it.
type fakePayment struct {
	txid           string
	fromRaw, toRaw string
	amount, port   uint64
	height         uint64
}

// fakeChain is htlc.bas v2's rules in memory: the same checks, in the same order, at a height the test moves. It
// backs both the in-process Dero used by the state-machine tests and the HTTP daemon used by the client test.
type fakeChain struct {
	mu     sync.Mutex
	height uint64
	locks  []Lock
	txSeq  uint64
	// mined maps a txid to the height it was included at, for DERO.GetTransaction
	mined map[string]uint64
	// claimedAt is the height each lock's Claim was mined at, the fact Invariant 9's test compares the sweep against
	claimedAt map[uint64]uint64
	// flaky, when non-nil, decides whether a call fails after taking effect (a timeout after the send)
	flaky func() bool
	// stuckBelowFee, when set, makes the HTTP daemon hold any transfer with a lower fee in its pool for ever
	// (DERO.GetTransaction reports in_pool): the fee re-send's trigger
	stuckBelowFee uint64
	pool          map[string]bool
	// codeHash is what memDero answers for the contract's code hash: the pinned htlc.bas unless a test says not.
	// codeText is the code the HTTP daemon returns for DERO.GetSC with code:true.
	codeHash string
	codeText string
	// unregistered holds raw addresses that have never registered: a transfer to one is refused, and
	// DERO.GetEncryptedBalance answers "Account Unregistered"
	unregistered map[string]bool
	// balances is raw address + "|" + asset → spendable; an address the test never set holds plenty
	balances map[string]uint64
	payments []fakePayment
}

// testStableDepth is DERO's STABLE_LIMIT, which the fakes mirror: stable height = height − 8.
const testStableDepth = 8

func newFakeChain(height uint64) *fakeChain {
	return &fakeChain{height: height, mined: map[string]uint64{}, claimedAt: map[uint64]uint64{}, pool: map[string]bool{},
		codeHash: HTLCCodeSHA256, codeText: "Function Initialize() Uint64\n10 RETURN 0\nEnd Function\n",
		unregistered: map[string]bool{}, balances: map[string]uint64{}}
}

func (c *fakeChain) Height() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.height
}

func (c *fakeChain) StableHeight() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stable()
}

func (c *fakeChain) stable() uint64 {
	if c.height < testStableDepth {
		return 0
	}
	return c.height - testStableDepth
}

func (c *fakeChain) Advance(n uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.height += n
}

func (c *fakeChain) txid() string {
	c.txSeq++
	sum := sha256.Sum256([]byte("fake dero tx " + strconv.FormatUint(c.txSeq, 10)))
	id := hex.EncodeToString(sum[:])
	c.mined[id] = c.height
	c.height++ // a transaction is mined into the current block and one more block follows, as waitTx wants
	return id
}

// balanceOf is called under c.mu.
func (c *fakeChain) balanceOf(raw, asset string) uint64 {
	if b, ok := c.balances[raw+"|"+asset]; ok {
		return b
	}
	return 1 << 40
}

func (c *fakeChain) setBalance(addr, asset string, v uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.balances[mustRaw(addr)+"|"+asset] = v
}

func (c *fakeChain) setUnregistered(addr string, unregistered bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unregistered[mustRaw(addr)] = unregistered
}

func is64hex(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && len(s) == 64
}

func sha256hexOf(hexSecret string) (string, bool) {
	raw, err := hex.DecodeString(hexSecret)
	if err != nil || len(raw) != 32 || len(hexSecret) != 64 {
		return "", false
	}
	return SHA256Hex(raw), true
}

// lock is htlc.bas Lock at height h (the estimator passes h = 0): returns the new lock or an error. asset is "" for
// DERO or the 64-hex SCID, amount what was sent of it.
func (c *fakeChain) lock(h uint64, makerRaw, ha, hk, hb, taker string, t1, t2 uint64, asset string, amount uint64) (Lock, error) {
	if !is64hex(ha) || !is64hex(hk) || !is64hex(hb) {
		return Lock{}, errors.New("hash length")
	}
	if t1 <= h+1 || t2 <= t1 || t2 > h+1000000 {
		return Lock{}, errors.New("timelocks")
	}
	if taker == "" {
		return Lock{}, errors.New("taker required")
	}
	takerRaw, err := DeroRaw(taker)
	if err != nil {
		return Lock{}, err
	}
	if asset != "" && !is64hex(asset) {
		return Lock{}, errors.New("asset id")
	}
	if amount == 0 {
		return Lock{}, errors.New("nothing sent")
	}
	l := Lock{ID: uint64(len(c.locks)), Maker: makerRaw, Taker: takerRaw, HA: ha, HK: hk, HB: hb, Asset: asset, Amount: amount, T1: t1, T2: t2}
	c.locks = append(c.locks, l)
	return l, nil
}

func (c *fakeChain) claim(h uint64, signerRaw string, id uint64, sa, kb string) error {
	if id >= uint64(len(c.locks)) {
		return errors.New("no such lock")
	}
	l := &c.locks[id]
	if l.State != LockOpen || h >= l.T1 || signerRaw != l.Taker {
		return errors.New("claim refused")
	}
	if hsa, ok := sha256hexOf(sa); !ok || hsa != l.HA {
		return errors.New("sa")
	}
	if hkb, ok := sha256hexOf(kb); !ok || hkb != l.HK {
		return errors.New("kb")
	}
	l.State, l.XA = LockClaimed, sa
	c.claimedAt[id] = h
	return nil
}

func (c *fakeChain) refund(h uint64, signerRaw string, id uint64, sb string) error {
	if id >= uint64(len(c.locks)) {
		return errors.New("no such lock")
	}
	l := &c.locks[id]
	if l.State != LockOpen || h < l.T1 || h >= l.T2 || signerRaw != l.Maker {
		return errors.New("refund refused")
	}
	if hsb, ok := sha256hexOf(sb); !ok || hsb != l.HB {
		return errors.New("sb")
	}
	l.State, l.XB = LockRefunded, sb
	return nil
}

// punish is htlc.bas v2: from t2 anyone with a signer may send it, and it pays the stored taker (LIV-07).
func (c *fakeChain) punish(h uint64, signerRaw string, id uint64) error {
	if id >= uint64(len(c.locks)) {
		return errors.New("no such lock")
	}
	l := &c.locks[id]
	if l.State != LockOpen || h < l.T2 || signerRaw == "" {
		return errors.New("punish refused")
	}
	l.State = LockPunished
	return nil
}

// pay is a plain DERO transfer mined at the current height; it is called under c.mu.
func (c *fakeChain) pay(fromRaw, to string, amount, port uint64) (string, error) {
	toRaw, err := DeroRaw(to)
	if err != nil {
		return "", err
	}
	if c.unregistered[toRaw] {
		return "", errors.New("Account Unregistered")
	}
	h := c.height
	id := c.txid()
	c.payments = append(c.payments, fakePayment{txid: id, fromRaw: fromRaw, toRaw: toRaw, amount: amount, port: port, height: h})
	return id, nil
}

// findPayment is one wallet's history: incoming to raw, or outgoing from it, carrying port. Called under c.mu.
func (c *fakeChain) findPayment(raw string, incoming bool, port, minHeight uint64) (Payment, bool) {
	for _, p := range c.payments {
		mine := p.toRaw == raw
		if !incoming {
			mine = p.fromRaw == raw
		}
		if mine && p.port == port && p.height >= minHeight {
			return Payment{TxID: p.txid, Amount: p.amount, Height: p.height}, true
		}
	}
	return Payment{}, false
}

// paymentsTagged is every transfer from one address to another carrying port, in order.
func (c *fakeChain) paymentsTagged(from, to string, port uint64) []fakePayment {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []fakePayment
	for _, p := range c.payments {
		if p.fromRaw == mustRaw(from) && p.toRaw == mustRaw(to) && p.port == port {
			out = append(out, p)
		}
	}
	return out
}

// unclaim is a replaced block: the Claim it held is not in the chain now, and the lock is open again.
func (c *fakeChain) unclaim(id uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := &c.locks[id]
	l.State, l.XA = LockOpen, ""
	delete(c.claimedAt, id)
}

func (c *fakeChain) read(id uint64) (Lock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id >= uint64(len(c.locks)) {
		return Lock{}, ErrNoSuchLock
	}
	return c.locks[id], nil
}

// memDero is one party's in-process Dero over a fakeChain.
type memDero struct {
	chain *fakeChain
	meRaw string
	calls map[string]int
	// code, when set, is what this party's daemon answers for the contract's code hash instead of the chain's
	code string
}

func newMemDero(chain *fakeChain, addr string) *memDero {
	raw, err := DeroRaw(addr)
	if err != nil {
		panic(err)
	}
	return &memDero{chain: chain, meRaw: raw, calls: map[string]int{}}
}

func (d *memDero) Height(ctx context.Context) (uint64, error) { return d.chain.Height(), nil }
func (d *memDero) StableHeight(ctx context.Context) (uint64, error) {
	return d.chain.StableHeight(), nil
}

func (d *memDero) Lock(ctx context.Context, ha, hk, hb, taker string, t1, t2 uint64, asset string, amount uint64) (string, uint64, error) {
	c := d.chain
	c.mu.Lock()
	defer c.mu.Unlock()
	d.calls["Lock"]++
	for i := len(c.locks) - 1; i >= 0; i-- {
		l := c.locks[i]
		if l.Maker == d.meRaw && l.HA == ha && l.HK == hk && l.HB == hb {
			return "", l.ID, nil
		}
	}
	l, err := c.lock(c.height, d.meRaw, ha, hk, hb, taker, t1, t2, asset, amount)
	if err != nil {
		return "", 0, err
	}
	tx := c.txid()
	if c.flaky != nil && c.flaky() {
		return "", 0, errors.New("rpc timeout after the send")
	}
	return tx, l.ID, nil
}

func (d *memDero) exit(name string, f func(h uint64) error) (string, error) {
	c := d.chain
	c.mu.Lock()
	defer c.mu.Unlock()
	d.calls[name]++
	if err := f(c.height); err != nil {
		return "", err
	}
	tx := c.txid()
	if c.flaky != nil && c.flaky() {
		return "", errors.New("rpc timeout after the send")
	}
	return tx, nil
}

// Claim holds DeroRPC's rule: nothing is sent at a height inside the cutoff, and nothing sent is not a call.
func (d *memDero) Claim(ctx context.Context, id uint64, sa, kb string, cutoff uint64) (string, error) {
	if h := d.chain.Height(); h+1 >= cutoff {
		return "", fmt.Errorf("Claim not sent: at height %d it could be mined inside the last blocks before t1 (claim cutoff %d)", h, cutoff)
	}
	return d.exit("Claim", func(h uint64) error { return d.chain.claim(h, d.meRaw, id, sa, kb) })
}
func (d *memDero) Refund(ctx context.Context, id uint64, sb string) (string, error) {
	return d.exit("Refund", func(h uint64) error { return d.chain.refund(h, d.meRaw, id, sb) })
}
func (d *memDero) Punish(ctx context.Context, id uint64) (string, error) {
	return d.exit("Punish", func(h uint64) error { return d.chain.punish(h, d.meRaw, id) })
}
func (d *memDero) ReadLock(ctx context.Context, id uint64) (Lock, error) {
	d.calls["ReadLock"]++
	return d.chain.read(id)
}

func (d *memDero) CodeSHA256(ctx context.Context) (string, error) {
	if d.code != "" {
		return d.code, nil
	}
	d.chain.mu.Lock()
	defer d.chain.mu.Unlock()
	return d.chain.codeHash, nil
}

func (d *memDero) Registered(ctx context.Context, addr string) (bool, error) {
	raw, err := DeroRaw(addr)
	if err != nil {
		return false, err
	}
	d.chain.mu.Lock()
	defer d.chain.mu.Unlock()
	return !d.chain.unregistered[raw], nil
}

func (d *memDero) Balance(ctx context.Context, asset string) (uint64, error) {
	d.chain.mu.Lock()
	defer d.chain.mu.Unlock()
	return d.chain.balanceOf(d.meRaw, asset), nil
}

func (d *memDero) Pay(ctx context.Context, to string, amount, port uint64) (string, error) {
	c := d.chain
	c.mu.Lock()
	defer c.mu.Unlock()
	d.calls["Pay"]++
	tx, err := c.pay(d.meRaw, to, amount, port)
	if err != nil {
		return "", err
	}
	if c.flaky != nil && c.flaky() {
		return "", errors.New("rpc timeout after the send")
	}
	return tx, nil
}

func (d *memDero) FindPayment(ctx context.Context, incoming bool, port, minHeight uint64) (Payment, bool, error) {
	d.chain.mu.Lock()
	defer d.chain.mu.Unlock()
	p, ok := d.chain.findPayment(d.meRaw, incoming, port, minHeight)
	return p, ok, nil
}

// stubProver stands in for proof-xmr: keys are real (NewScalar, Public), the "proof" is a tag over (h, s) that
// only this prover can make, so a verifier learns nothing about the statement but a mismatched or forged proof
// is refused. bad makes every proof unverifiable, for the tests that a party must then refuse to lock.
type stubProver struct {
	bad   bool
	fixed *Scalar // when set, Keygen returns this secret every time: a prover that repeats itself (Invariant 13)
}

func stubTag(h string, s Point) []byte {
	sum := sha256.Sum256([]byte("xmrswap fake proof|" + h + "|" + s.Hex()))
	return sum[:]
}

func (p *stubProver) Keygen(ctx context.Context) (Scalar, string, Point, error) {
	if p.fixed != nil {
		s := *p.fixed
		return s, SHA256Hex(s[:]), s.Public(), nil
	}
	s, err := NewScalar(rand.Reader)
	if err != nil {
		return Scalar{}, "", Point{}, err
	}
	return s, SHA256Hex(s[:]), s.Public(), nil
}

func (p *stubProver) Prove(ctx context.Context, secret Scalar) (string, Point, []byte, error) {
	h, s := SHA256Hex(secret[:]), secret.Public()
	tag := stubTag(h, s)
	if p.bad {
		tag[0] ^= 1
	}
	return h, s, tag, nil
}

func (p *stubProver) Verify(ctx context.Context, h string, s Point, proof []byte) (bool, error) {
	return string(proof) == string(stubTag(h, s)), nil
}

// countingWallet wraps a Wallet to count the payments a machine makes and to let a test look at the world at
// the moment of each one.
type countingWallet struct {
	Wallet
	transfers  int
	sweeps     int
	closes     int
	onTransfer func(to string, amount uint64) error
	onSweep    func(to string) error
}

// CloseWallet is counted because the joint spend wallet holds s_a + s_b: a swap that swept and did not close
// it left that key in an open wallet in the rpc (SEC-K-09).
func (w *countingWallet) CloseWallet(ctx context.Context) error {
	w.closes++
	return w.Wallet.CloseWallet(ctx)
}

func (w *countingWallet) Transfer(ctx context.Context, to string, amount uint64) (string, error) {
	w.transfers++
	if w.onTransfer != nil {
		if err := w.onTransfer(to, amount); err != nil {
			return "", fmt.Errorf("TEST INVARIANT: %w", err)
		}
	}
	return w.Wallet.Transfer(ctx, to, amount)
}

func (w *countingWallet) SweepAll(ctx context.Context, to string) ([]string, error) {
	w.sweeps++
	if w.onSweep != nil {
		if err := w.onSweep(to); err != nil {
			return nil, fmt.Errorf("TEST INVARIANT: %w", err)
		}
	}
	return w.Wallet.SweepAll(ctx, to)
}

// spyChannel wraps a Channel to see every message a machine sends.
type spyChannel struct {
	Channel
	onSend func(to string, m Message) error
}

func (c *spyChannel) Send(ctx context.Context, to string, m Message) error {
	if c.onSend != nil {
		if err := c.onSend(to, m); err != nil {
			return fmt.Errorf("TEST INVARIANT: %w", err)
		}
	}
	return c.Channel.Send(ctx, to, m)
}
