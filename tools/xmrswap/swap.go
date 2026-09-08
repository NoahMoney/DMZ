// The state machines (docs/XMR_SWAP_BUILD.md §3.D): the companion a user runs for the length of one DERO ↔ Monero
// atomic swap. It holds that user's key share and, for the maker, the gate secret; it talks to the
// user's own DERO wallet, Monero wallet and daemon; and it exchanges the protocol's seven messages with the
// counterparty over a Channel. Nobody hosts anything and nothing here can take or freeze either side's coins:
// every irreversible step is gated on something this companion read itself — a proof it verified, a lock it
// read from the chain, a confirmation count from its own view-only wallet — and never on what the peer says.
package xmrswap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"time"
)

// Policy defaults (§1, timelocks): DERO blocks are 18 s.
const (
	DefaultT1Min uint64 = 400 // Alice must lock, see the Monero confirmations, receive k_b and claim in this
	DefaultT2Min uint64 = 400 // Bob's refund window
	// DefaultConfirmations is the Monero depth before the gate (Invariant 10, review R3). Ten is
	// CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE — below it nobody can spend the output, so ten buys Bob nothing but
	// time — and Monero has no consensus bound on a reorganisation, with an 18-block one reported; twenty is the
	// policy, and the one number in the protocol that is an assumption rather than a proof.
	DefaultConfirmations uint64 = 20
	// DefaultStableBlocks is DERO's STABLE_LIMIT: a block this deep is never replaced. Alice locks XMR only against
	// a lock this old, and a Claim is only acted on once it is this deep.
	DefaultStableBlocks uint64 = 8
	// DefaultClaimMargin is Alice's slack on top of the stable depth (Invariant 9, review R2): she sends no Claim
	// once height + 1 ≥ t1 − 8 − margin, because a Claim mined in the last eight blocks before t1 can be re-mined
	// at or after t1 and refused, with s_a already read by Bob.
	DefaultClaimMargin uint64 = 8
)

// Config is one companion's parameters. The minimums are policy the contract leaves to the client (§1); a
// simulator run lowers them, a mainnet run must not.
type Config struct {
	HTLC       string
	AmountDero uint64 // atomic units
	AmountXmr  uint64 // piconero
	Net        Network
	MyDero     string // this party's DERO address, read from its wallet by the caller
	// MoneroWalletName is the personal wallet's file name on the wallet-rpc, so it can be reopened after the
	// joint wallets were generated on the same rpc. "" means it is the wallet that is open and stays open.
	MoneroWalletName string

	T1Min, T2Min     uint64 // policy floors (Alice refuses a lock below them; Bob refuses to choose below them)
	T1Delta, T2Delta uint64 // Bob's widths: t1 = lock height + T1Delta, t2 = t1 + T2Delta
	Confirmations    uint64 // Monero confirmations before Bob releases k_b
	StableBlocks     uint64 // DERO blocks Alice waits after first seeing the lock before she locks XMR
	// ClaimMargin: Alice sends a Claim only while height + 1 < t1 − StableBlocks − ClaimMargin, so the Claim is
	// stable before t1 and Bob may act on it (Invariant 9); past that she waits for Bob's refund, or t2.
	ClaimMargin uint64
	// GateMargin: Bob releases k_b only while height + GateMargin < t1, so Alice has blocks left to claim in —
	// by default her own cutoff plus two blocks for the channel, since a gate she cannot use is a wasted one.
	GateMargin uint64
	// LockSettleBlocks: after a restart during a lock, Bob waits this many blocks for a possibly in-flight
	// transaction to land before the idempotent Lock looks for it, so a mempool transaction is never doubled.
	LockSettleBlocks uint64

	Poll        time.Duration // how long one Step waits on the channel when a message is expected
	ResendAfter time.Duration // how often the last message is repeated while the protocol has not moved on
	Log         func(format string, a ...interface{})
}

func (c Config) withDefaults() Config {
	def := func(v *uint64, d uint64) {
		if *v == 0 {
			*v = d
		}
	}
	def(&c.T1Min, DefaultT1Min)
	def(&c.T2Min, DefaultT2Min)
	// the taker sees the lock a couple of blocks after the maker decided t1, and measures t1 − now against the
	// same floor, so the maker adds slack or his own lock is refused
	def(&c.T1Delta, c.T1Min+20)
	def(&c.T2Delta, c.T2Min)
	def(&c.Confirmations, DefaultConfirmations)
	def(&c.StableBlocks, DefaultStableBlocks)
	def(&c.ClaimMargin, DefaultClaimMargin)
	def(&c.GateMargin, DefaultStableBlocks+c.ClaimMargin+2)
	def(&c.LockSettleBlocks, 3)
	if c.Poll <= 0 {
		c.Poll = 2 * time.Second
	}
	if c.ResendAfter <= 0 {
		c.ResendAfter = time.Minute
	}
	if c.Log == nil {
		c.Log = log.Printf
	}
	return c
}

// Swap is one side's state machine. Step advances it by at most one phase and returns whether it is finished;
// it is safe to call again after any error, and after a restart from the state file.
type Swap struct {
	cfg    Config
	st     *State
	dero   Dero
	xmr    Wallet
	prover Prover
	ch     Channel
	peer   string
	// resumedPhase is the phase the state file was in when this process started. A phase that intends a
	// payment (dero_locking, xmr_locking, sweeping) means something different when a process wakes up in it:
	// the payment may already have gone out.
	resumedPhase string
	// transferTried: this process has called Transfer once. Whatever the call returned, it is not called again.
	transferTried bool
	// used is the register of every secret this companion has generated, beside the state file (Invariant 13).
	used *UsedSecrets
	// frozenWarned: the operator has been told once that the joint address holds a transfer the gate does not count.
	frozenWarned bool
	// crash is a test hook: after each durable step it may abandon the call as a process death would.
	crash func(stage string) bool
	// rand is a test hook for the gate secret and the view key: nil is crypto/rand.
	rand io.Reader
}

var errCrashed = errors.New("crashed by the test hook")

func newSwap(role string, cfg Config, st *State, dero Dero, xmr Wallet, prover Prover, ch Channel) (*Swap, error) {
	cfg = cfg.withDefaults()
	if st.Role != role {
		return nil, fmt.Errorf("state is for %s, this is %s", st.Role, role)
	}
	if cfg.HTLC == "" || cfg.MyDero == "" || cfg.AmountDero == 0 || cfg.AmountXmr == 0 {
		return nil, errors.New("config needs HTLC, MyDero, AmountDero and AmountXmr")
	}
	if _, err := DeroRaw(cfg.MyDero); err != nil {
		return nil, err
	}
	if cfg.T1Delta < cfg.T1Min || cfg.T2Delta < cfg.T2Min {
		return nil, fmt.Errorf("timelock widths %d/%d are below the policy minimums %d/%d", cfg.T1Delta, cfg.T2Delta, cfg.T1Min, cfg.T2Min)
	}
	used, err := LoadUsed(UsedPath(st.Path()))
	if err != nil {
		return nil, err
	}
	peer := "alice"
	if role == "alice" {
		peer = "bob"
	}
	return &Swap{cfg: cfg, st: st, dero: dero, xmr: xmr, prover: prover, ch: ch, peer: peer, resumedPhase: st.Phase, used: used}, nil
}

// NewBob is the maker: he has DERO, locks first, and ends with XMR.
func NewBob(cfg Config, st *State, dero Dero, xmr Wallet, prover Prover, ch Channel) (*Swap, error) {
	return newSwap("bob", cfg, st, dero, xmr, prover, ch)
}

// NewAlice is the taker: she has XMR, locks second, and ends with DERO.
func NewAlice(cfg Config, st *State, dero Dero, xmr Wallet, prover Prover, ch Channel) (*Swap, error) {
	return newSwap("alice", cfg, st, dero, xmr, prover, ch)
}

func (s *Swap) Phase() string { return s.st.Phase }
func (s *Swap) State() *State { return s.st }

// Step runs one phase. Waiting phases return (false, nil) when nothing has changed yet.
func (s *Swap) Step(ctx context.Context) (bool, error) {
	if s.st.Done {
		return true, nil
	}
	if err := s.flush(ctx); err != nil {
		return false, err
	}
	var err error
	if s.st.Role == "bob" {
		err = s.stepBob(ctx)
	} else {
		err = s.stepAlice(ctx)
	}
	if err != nil {
		return false, err
	}
	// a message queued by this step goes out now rather than at the next call: the peer is waiting
	if err := s.flush(ctx); err != nil {
		return false, err
	}
	return s.st.Done, nil
}

// --- durable transitions and messaging ---

// save is the durable step: the phase (and whatever the caller changed) is on disk before anything acts on it.
func (s *Swap) save(phase string) error {
	from := s.st.Phase
	s.st.Phase = phase
	if err := s.st.Save(); err != nil {
		s.st.Phase = from // the file still says `from`, and so must the memory that will retry
		return err
	}
	if phase != from {
		s.cfg.Log("%s: %s -> %s", s.st.Role, from, phase)
	}
	if s.crash != nil && s.crash(phase) {
		return errCrashed
	}
	return nil
}

// finish ends the swap with an outcome the screen can show.
func (s *Swap) finish(phase, outcome string) error {
	s.st.Done, s.st.Outcome = true, outcome
	s.cfg.Log("%s: done: %s", s.st.Role, outcome)
	return s.save(phase)
}

// queue records the message the peer is owed. It is saved by the caller's save and sent by flush, so a crash
// between the two repeats the send rather than losing it.
func (s *Swap) queue(kind string, body interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	s.st.LastSent = &Message{Kind: kind, Body: b}
	s.st.LastSentAt = time.Time{}
	return nil
}

func (s *Swap) flush(ctx context.Context) error {
	m := s.st.LastSent
	if m == nil {
		return nil
	}
	if !s.st.LastSentAt.IsZero() && time.Since(s.st.LastSentAt) < s.cfg.ResendAfter {
		return nil
	}
	if err := s.ch.Send(ctx, s.peer, *m); err != nil {
		return err
	}
	s.cfg.Log("%s: sent %s", s.st.Role, m.Kind)
	s.st.LastSentAt = time.Now()
	return nil
}

// recv waits up to `wait` for the message the phase expects. Repeats of consumed kinds are dropped; anything
// else is dropped too, with a note — the chain, not the channel, decides what happens next.
func (s *Swap) recv(ctx context.Context, want string, into interface{}, wait time.Duration) (bool, error) {
	dctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		m, err := s.ch.Recv(dctx)
		if err != nil {
			if ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				return false, nil
			}
			return false, err
		}
		if s.st.Consumed[m.Kind] {
			continue
		}
		if m.Kind != want {
			s.cfg.Log("%s: dropping %s in phase %s", s.st.Role, m.Kind, s.st.Phase)
			continue
		}
		if err := json.Unmarshal(m.Body, into); err != nil {
			s.cfg.Log("%s: dropping malformed %s: %v", s.st.Role, m.Kind, err)
			continue
		}
		s.st.Consumed[m.Kind] = true
		// the peer answered in protocol order, so whatever this side was repeating has arrived: stop repeating it
		s.st.LastSent, s.st.LastSentAt = nil, time.Time{}
		return true, nil
	}
}

// --- message bodies: only what §1's table lists ---

type offerBody struct {
	AmountDero uint64 `json:"amountDero"`
	AmountXmr  uint64 `json:"amountXmr"`
	Asset      string `json:"asset"`
	Net        string `json:"net"`
	HTLC       string `json:"htlc"`
	T1Delta    uint64 `json:"t1Delta"`
	T2Delta    uint64 `json:"t2Delta"`
}

type aliceSetupBody struct {
	S     string `json:"s"`     // S_a
	H     string `json:"h"`     // H_a
	Proof string `json:"proof"` // π_a
	V     string `json:"v"`     // the joint view public key
	View  string `json:"view"`  // the joint private view key: shared by design, spends nothing
	Dero  string `json:"dero"`  // her DERO address, the taker
}

type bobSetupBody struct {
	S     string `json:"s"`
	H     string `json:"h"`
	Proof string `json:"proof"`
	HK    string `json:"hk"` // SHA256(k_b), never k_b
}

type deroLockedBody struct {
	ID uint64 `json:"id"`
	Tx string `json:"tx"`
}

type xmrLockedBody struct {
	Tx string `json:"tx"`
}

type gateBody struct {
	KB string `json:"kb"`
}

type doneBody struct {
	Outcome string `json:"outcome"`
}

// --- shared pieces ---

func isHash(h string) bool {
	b, err := hex.DecodeString(h)
	return err == nil && len(b) == 32 && len(h) == 64
}

func (s *Swap) random() io.Reader {
	if s.rand != nil {
		return s.rand
	}
	return rand.Reader
}

// beginSwap names the swap before its first secret exists, so the register can bind every secret to it.
func (s *Swap) beginSwap() error {
	if s.st.SwapID != "" {
		return nil
	}
	var id [8]byte
	if _, err := io.ReadFull(s.random(), id[:]); err != nil {
		return err
	}
	s.st.SwapID = hex.EncodeToString(id[:])
	return nil
}

// recordSecret enters a freshly generated secret in the register, durably, before it is used (Invariant 13).
func (s *Swap) recordSecret(kind string, secret []byte) error {
	if err := s.used.Record(secret, s.st.SwapID, kind); err != nil {
		return fmt.Errorf("%s: %w", kind, err)
	}
	return nil
}

// refuseIfReused turns a register collision into the swap's end: a secret seen before means the prover or the
// random source is repeating itself, and no swap should start on it. Any other error is retried by the caller.
func (s *Swap) refuseIfReused(err error) error {
	if errors.Is(err, ErrSecretReused) {
		return s.finish("refused", "setup refused: "+err.Error()+"; the prover or the random source is not producing fresh secrets")
	}
	return err
}

// keygen makes this party's share and its proof, checking the prover's own consistency before trusting it.
func (s *Swap) keygen(ctx context.Context) error {
	secret, h, pub, err := s.prover.Keygen(ctx)
	if err != nil {
		return fmt.Errorf("keygen: %w", err)
	}
	if _, err := ParseScalar(secret.Hex()); err != nil || h != SHA256Hex(secret[:]) || pub != secret.Public() {
		return errors.New("keygen: the prover's output does not match its own statement")
	}
	if err := s.recordSecret("share", secret[:]); err != nil {
		return err
	}
	ph, ps, proof, err := s.prover.Prove(ctx, secret)
	if err != nil {
		return fmt.Errorf("prove: %w", err)
	}
	if ph != h || ps != pub {
		return errors.New("prove: the proof is for a different (h, s) than keygen gave")
	}
	s.st.Own = Party{Secret: secret.Hex(), H: h, S: pub.Hex(), Proof: hex.EncodeToString(proof)}
	return nil
}

// verifyPeer checks the counterparty's proof against the hash and point they sent. Without it the DERO
// preimage and the Monero key share could be two different numbers, and one side's coins would be unspendable.
func (s *Swap) verifyPeer(ctx context.Context, h, sHex, proofHex string) error {
	if !isHash(h) {
		return errors.New("peer hash is not 64 hex")
	}
	pt, err := ParsePoint(sHex)
	if err != nil {
		return fmt.Errorf("peer point: %w", err)
	}
	proof, err := hex.DecodeString(proofHex)
	if err != nil || len(proof) == 0 {
		return errors.New("peer proof is not hex")
	}
	ok, err := s.prover.Verify(ctx, h, pt, proof)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if !ok {
		return errors.New("the peer's proof does not verify: their DERO preimage is not their Monero share")
	}
	s.st.Peer = Party{H: h, S: sHex, Proof: proofHex}
	return nil
}

// jointAddress is P = S_a + S_b with the shared view key V.
func (s *Swap) jointAddress() (string, error) {
	own, err := ParsePoint(s.st.Own.S)
	if err != nil {
		return "", err
	}
	peer, err := ParsePoint(s.st.Peer.S)
	if err != nil {
		return "", err
	}
	p, err := AddPoints(own, peer)
	if err != nil {
		return "", fmt.Errorf("joint spend key: %w", err)
	}
	v, err := ParsePoint(s.st.ViewPub)
	if err != nil {
		return "", err
	}
	return Address(p, v, s.cfg.Net)
}

// spendKey is s_a + s_b, formed only once the other share is on the chain.
func (s *Swap) spendKey() (string, error) {
	own, err := ParseScalar(s.st.Own.Secret)
	if err != nil {
		return "", err
	}
	peer, err := ParseScalar(s.st.Revealed)
	if err != nil {
		return "", fmt.Errorf("revealed share: %w", err)
	}
	return AddScalars(own, peer).Hex(), nil
}

// checkRevealed verifies a share read from the chain is the peer's: it hashes to their H and is the discrete
// log of their S. The contract checked the hash; the proof at setup tied that hash to S, and this is the last
// look before a key is formed from it.
func (s *Swap) checkRevealed(x string) error {
	sc, err := ParseScalar(x)
	if err != nil {
		return fmt.Errorf("revealed share: %w", err)
	}
	if SHA256Hex(sc[:]) != s.st.Peer.H {
		return errors.New("revealed share does not hash to the peer's H")
	}
	if sc.Public().Hex() != s.st.Peer.S {
		return errors.New("revealed share is not the discrete log of the peer's S")
	}
	s.st.Revealed = x
	return nil
}

func (s *Swap) walletName(kind string) string {
	scid := s.cfg.HTLC
	if len(scid) > 8 {
		scid = scid[:8]
	}
	return "xmrswap-" + scid + "-" + strconv.FormatUint(s.st.Lock.ID, 10) + "-" + kind
}

// openOrGenerate opens a joint wallet by name, creating it from keys when it does not exist yet. Both calls
// are safe to repeat, so a restart anywhere in here is harmless.
func (s *Swap) openOrGenerate(ctx context.Context, name, spendHex string) error {
	if err := s.xmr.OpenWallet(ctx, name); err == nil {
		return nil
	}
	if err := s.xmr.GenerateFromKeys(ctx, name, s.st.Joint, s.st.View, spendHex, s.st.RestoreHeight); err != nil {
		return fmt.Errorf("generate %s: %w", name, err)
	}
	return s.xmr.OpenWallet(ctx, name)
}

// ownWallet makes sure the personal wallet is the open one before anything is read from or paid out of it.
func (s *Swap) ownWallet(ctx context.Context) error {
	if s.cfg.MoneroWalletName != "" {
		if err := s.xmr.OpenWallet(ctx, s.cfg.MoneroWalletName); err != nil {
			return fmt.Errorf("open own wallet: %w", err)
		}
	}
	if s.st.MyXmr != "" {
		a, err := s.xmr.Address(ctx)
		if err != nil {
			return err
		}
		if a != s.st.MyXmr {
			return fmt.Errorf("the open Monero wallet is %s, not this party's %s", a, s.st.MyXmr)
		}
	}
	return nil
}

// confirmedAtJoint is what Bob's own view-only wallet shows: the piconero at the joint address that he could
// spend — at least the required confirmations, unlocked, with no unlock_time of the sender's choosing — and
// never more than the wallet's own unlocked balance, which leaves out an output the scanner found to be a
// duplicate one-time key (Invariant 8, reviews R5d and R6: a transfer that confirms but cannot be spent would
// otherwise release the gate and leave Bob's XMR frozen at no cost to Alice). Nothing Alice says enters this
// number.
func (s *Swap) confirmedAtJoint(ctx context.Context) (uint64, uint64, error) {
	if err := s.xmr.OpenWallet(ctx, s.walletName("view")); err != nil {
		return 0, 0, err
	}
	h, err := s.xmr.Height(ctx)
	if err != nil {
		return 0, 0, err
	}
	// wallet2::get_payments treats min_height as exclusive; one block early loses nothing and misses nothing
	min := s.st.RestoreHeight
	if min > 0 {
		min--
	}
	in, err := s.xmr.Incoming(ctx, min)
	if err != nil {
		return 0, 0, err
	}
	var sum uint64
	for _, t := range in {
		if t.UnlockTime != 0 {
			if !s.frozenWarned {
				s.frozenWarned = true
				s.cfg.Log("bob: a transfer at the joint address carries unlock_time %d: it does not count towards the gate, whatever its confirmations", t.UnlockTime)
			}
			continue
		}
		if t.Confirmations >= s.cfg.Confirmations && t.Unlocked {
			sum += t.Amount
		}
	}
	unlocked, err := s.xmr.UnlockedBalance(ctx)
	if err != nil {
		return 0, 0, err
	}
	if unlocked < sum {
		sum = unlocked
	}
	return sum, h, nil
}

// sweep spends the joint output to this party's own address, at most once per swap: the intent is durable
// before the call, and a restart that finds the intent without a result does not try again — the spend key
// stays in the state file for a hand-driven sweep, and a second attempt is the one thing worse than a slow one.
func (s *Swap) sweep(ctx context.Context) error {
	switch s.st.Phase {
	case "sweep_ready":
		if err := s.save("sweeping"); err != nil {
			return err
		}
		if err := s.xmr.OpenWallet(ctx, s.walletName("spend")); err != nil {
			return err
		}
		txs, err := s.xmr.SweepAll(ctx, s.st.MyXmr)
		if err != nil {
			// the intent stands: nothing is retried automatically, and the operator sees why
			s.st.Sweep.Unknown = true
			_ = s.finish("sweep_unknown", "the sweep of the joint wallet failed ("+err.Error()+"); the spend key is in the state file")
			return err
		}
		s.st.Sweep.Txs = txs
		s.cfg.Log("%s: swept the joint wallet to %s: %v", s.st.Role, s.st.MyXmr, txs)
		return s.finish("swept", "swept the XMR to "+s.st.MyXmr)
	case "sweeping":
		s.st.Sweep.Unknown = true
		return s.finish("sweep_unknown", "restarted during the sweep: it was attempted once and is not attempted again; the spend key is in the state file under "+s.walletName("spend"))
	}
	return nil
}

// --- Bob ---

func (s *Swap) stepBob(ctx context.Context) error {
	st := s.st
	switch st.Phase {
	case "":
		// setup: share, proof, gate secret, addresses. Nothing has been said yet.
		if err := s.beginSwap(); err != nil {
			return err
		}
		if err := s.keygen(ctx); err != nil {
			return s.refuseIfReused(err)
		}
		var kb [32]byte
		if _, err := io.ReadFull(s.random(), kb[:]); err != nil {
			return err
		}
		if err := s.recordSecret("gate", kb[:]); err != nil {
			return s.refuseIfReused(err)
		}
		st.Gate, st.HK = hex.EncodeToString(kb[:]), SHA256Hex(kb[:])
		if err := s.ownWallet(ctx); err != nil {
			return err
		}
		a, err := s.xmr.Address(ctx)
		if err != nil {
			return err
		}
		h, err := s.xmr.Height(ctx)
		if err != nil {
			return err
		}
		st.MyXmr, st.MyDero, st.RestoreHeight = a, s.cfg.MyDero, h
		st.Terms = Terms{AmountDero: s.cfg.AmountDero, AmountXmr: s.cfg.AmountXmr, Net: s.cfg.Net.String(), T1Delta: s.cfg.T1Delta, T2Delta: s.cfg.T2Delta}
		if err := s.queue(KindOffer, offerBody{AmountDero: st.Terms.AmountDero, AmountXmr: st.Terms.AmountXmr, Asset: st.Terms.Asset, Net: st.Terms.Net, HTLC: s.cfg.HTLC, T1Delta: st.Terms.T1Delta, T2Delta: st.Terms.T2Delta}); err != nil {
			return err
		}
		return s.save("setup")

	case "setup":
		var m aliceSetupBody
		ok, err := s.recv(ctx, KindAliceSetup, &m, s.cfg.Poll)
		if err != nil || !ok {
			return err
		}
		// Invariant 4: no DERO lock until π_a verifies against (H_a, S_a)
		if err := s.verifyPeer(ctx, m.H, m.S, m.Proof); err != nil {
			return s.finish("refused", "alice_setup refused: "+err.Error())
		}
		v, err := ParseScalar(m.View)
		if err != nil {
			return s.finish("refused", "alice_setup refused: view key: "+err.Error())
		}
		if v.Public().Hex() != m.V {
			return s.finish("refused", "alice_setup refused: V is not the public key of the view key sent")
		}
		if _, err := DeroRaw(m.Dero); err != nil {
			return s.finish("refused", "alice_setup refused: "+err.Error())
		}
		st.View, st.ViewPub, st.PeerDero = m.View, m.V, m.Dero
		joint, err := s.jointAddress()
		if err != nil {
			return s.finish("refused", "alice_setup refused: "+err.Error())
		}
		st.Joint = joint
		s.cfg.Log("bob: π_a verified; joint address %s; taker %s", joint, m.Dero)
		if err := s.queue(KindBobSetup, bobSetupBody{S: st.Own.S, H: st.Own.H, Proof: st.Own.Proof, HK: st.HK}); err != nil {
			return err
		}
		return s.save("verified_pi_a")

	case "verified_pi_a":
		// decide the windows from the height now, and make the decision durable before the lock goes out
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		st.Lock = LockRecord{IntentHeight: h, T1: h + st.Terms.T1Delta, T2: h + st.Terms.T1Delta + st.Terms.T2Delta, Amount: st.Terms.AmountDero}
		return s.save("dero_locking")

	case "dero_locking":
		if s.resumedPhase == "dero_locking" {
			// a restart: a lock sent just before it may still be in the mempool; let it land before Lock looks
			// for it, or the lookup misses it and the DERO is locked twice
			h, err := s.dero.Height(ctx)
			if err != nil {
				return err
			}
			if h < st.Lock.IntentHeight+s.cfg.LockSettleBlocks {
				return nil
			}
		}
		txid, id, err := s.dero.Lock(ctx, st.Peer.H, st.HK, st.Own.H, st.PeerDero, st.Lock.T1, st.Lock.T2, st.Lock.Amount)
		if err != nil {
			return err
		}
		st.Lock.ID, st.Lock.Tx = id, txid
		s.cfg.Log("bob: DERO locked: id %d tx %s t1 %d t2 %d amount %d", id, txid, st.Lock.T1, st.Lock.T2, st.Lock.Amount)
		if err := s.queue(KindDeroLocked, deroLockedBody{ID: id, Tx: txid}); err != nil {
			return err
		}
		return s.save("dero_locked")

	case "dero_locked":
		if err := s.openOrGenerate(ctx, s.walletName("view"), ""); err != nil {
			return err
		}
		return s.save("watching_xmr")

	case "watching_xmr":
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h >= st.Lock.T1 {
			return s.save("refund_window")
		}
		// Alice's txid is informational: consume it so a repeat is not noise, and never act on it
		var x xmrLockedBody
		if ok, err := s.recv(ctx, KindXmrLocked, &x, time.Millisecond); err != nil {
			return err
		} else if ok {
			st.Xmr.LockTx = x.Tx
			s.cfg.Log("bob: alice reports Monero tx %s; watching the view wallet, not the message", x.Tx)
		}
		// Invariant 2: the gate waits for this party's own view-only wallet
		sum, mh, err := s.confirmedAtJoint(ctx)
		if err != nil {
			return err
		}
		if sum < st.Terms.AmountXmr {
			return nil
		}
		st.Xmr.ConfirmedAt, st.Xmr.Confirmed = mh, sum
		s.cfg.Log("bob: %d piconero at the joint address with ≥ %d confirmations (Monero height %d)", sum, s.cfg.Confirmations, mh)
		return s.save("xmr_confirmed")

	case "xmr_confirmed":
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h+s.cfg.GateMargin >= st.Lock.T1 {
			s.cfg.Log("bob: too close to t1 (%d, height %d) to release the gate usefully; refunding after t1", st.Lock.T1, h)
			return s.save("refund_window")
		}
		// re-read right before the release: the record above is a memory, this is the fact
		sum, _, err := s.confirmedAtJoint(ctx)
		if err != nil {
			return err
		}
		if sum < st.Terms.AmountXmr {
			return fmt.Errorf("the joint address shows %d confirmed, below %d: not releasing the gate", sum, st.Terms.AmountXmr)
		}
		if err := s.queue(KindGate, gateBody{KB: st.Gate}); err != nil {
			return err
		}
		if err := s.save("gate_sending"); err != nil {
			return err
		}
		if err := s.flush(ctx); err != nil {
			return err
		}
		return s.save("gate_sent")

	case "gate_sending":
		// durable intent found after a restart: send (again) and move on
		if err := s.flush(ctx); err != nil {
			return err
		}
		return s.save("gate_sent")

	case "gate_sent":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		switch {
		case l.State == LockClaimed:
			return s.claimSeen(ctx, l)
		case l.State == LockOpen:
			h, err := s.dero.Height(ctx)
			if err != nil {
				return err
			}
			if h >= st.Lock.T1 {
				return s.save("refund_window")
			}
			return nil
		}
		return s.save("refund_window")

	case "claimed":
		// Invariant 9: a Claim Bob acts on must be one DERO can no longer undo. Until the height he read it at
		// is at or below the stable height, the Claim's block can still be replaced — and a Claim re-mined at or
		// after t1 is refused, reopening the lock with s_a already in Bob's hands; a sweep made on that reading
		// would be a race for the XMR that Alice could lose (review R2).
		stable, err := s.dero.StableHeight(ctx)
		if err != nil {
			return err
		}
		if stable < st.Lock.ClaimSeenHeight {
			return nil
		}
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		if l.State != LockClaimed {
			s.cfg.Log("bob: the claim read at height %d is gone from the chain (st %d): back to watching the lock", st.Lock.ClaimSeenHeight, l.State)
			st.Lock.ClaimSeenHeight = 0
			return s.save("gate_sent")
		}
		spend, err := s.spendKey()
		if err != nil {
			return err
		}
		if err := s.openOrGenerate(ctx, s.walletName("spend"), spend); err != nil {
			return err
		}
		return s.save("sweep_ready")

	case "sweep_ready", "sweeping":
		if err := s.sweep(ctx); err != nil {
			return err
		}
		if st.Phase == "swept" {
			_ = s.queue(KindDone, doneBody{Outcome: st.Outcome})
			return s.flush(ctx)
		}
		return nil

	case "refund_window", "refunding":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		switch l.State {
		case LockClaimed:
			// her claim landed on the boundary: the swap happened after all
			return s.claimSeen(ctx, l)
		case LockRefunded:
			return s.finish("refunded", "refunded: the DERO is back; s_b is on the chain so alice can recover any XMR she locked")
		case LockPunished:
			return s.finish("punished", "punished: alice took the DERO after t2; the refund window was missed")
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h >= st.Lock.T2 {
			s.cfg.Log("bob: past t2 (%d) with the lock still open: only alice can move it now (Punish)", st.Lock.T2)
			return s.save("punish_window")
		}
		if h < st.Lock.T1 {
			return nil
		}
		if st.Phase != "refunding" {
			if err := s.save("refunding"); err != nil {
				return err
			}
		}
		txid, err := s.dero.Refund(ctx, st.Lock.ID, st.Own.Secret)
		if err != nil {
			return err
		}
		st.RefundTx = txid
		s.cfg.Log("bob: refunded: tx %s", txid)
		_ = s.queue(KindDone, doneBody{Outcome: "refunded"})
		return s.finish("refunded", "refunded: tx "+txid+"; s_b is on the chain so alice can recover any XMR she locked")

	case "punish_window":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		switch l.State {
		case LockPunished:
			return s.finish("punished", "punished: alice took the DERO after t2")
		case LockRefunded:
			return s.finish("refunded", "refunded")
		case LockClaimed:
			return s.claimSeen(ctx, l)
		}
		return nil
	}
	return fmt.Errorf("bob: unknown phase %q", st.Phase)
}

// claimSeen is Bob's move on reading st == 1 from whichever phase noticed: s_a is checked against the proof
// it was tied to, the height it was read at is recorded as the bound on the Claim's block, and whatever he was
// repeating stops — the gate, usually — because the chain has answered it, and a gate re-sent from a later
// phase is one nothing decided to send.
func (s *Swap) claimSeen(ctx context.Context, l Lock) error {
	if err := s.checkRevealed(l.XA); err != nil {
		return fmt.Errorf("claimed lock: %w", err)
	}
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	s.st.Lock.ClaimSeenHeight = h
	s.st.LastSent, s.st.LastSentAt = nil, time.Time{}
	s.cfg.Log("bob: alice claimed; s_a read from the chain at height %d", h)
	return s.save("claimed")
}

// --- Alice ---

// LockExpect is what Alice requires of the lock she reads from the chain (Invariant 3).
type LockExpect struct {
	HA, HK, HB string // her own H_a; Bob's committed H_k and H_b
	TakerRaw   string // her own address, raw hex
	Amount     uint64
	Asset      string
	T1Min      uint64
	T2Min      uint64
}

// CheckLock is every §4.3 check, and names the first that fails.
func CheckLock(l Lock, now uint64, e LockExpect) error {
	switch {
	case l.State != LockOpen:
		return fmt.Errorf("lock is not open (st %d)", l.State)
	case l.HA != e.HA:
		return errors.New("ha is not SHA256 of my share")
	case l.HK != e.HK:
		return errors.New("hk is not the H_k bob committed to")
	case l.HB != e.HB:
		return errors.New("hb is not the H_b bob's proof was verified against")
	case l.Taker != e.TakerRaw:
		return errors.New("taker is not my address")
	case l.T1 <= now || l.T1-now < e.T1Min:
		return fmt.Errorf("t1 − now = %d is below the minimum %d", int64(l.T1)-int64(now), e.T1Min)
	case l.T2 <= l.T1 || l.T2-l.T1 < e.T2Min:
		return fmt.Errorf("t2 − t1 = %d is below the minimum %d", int64(l.T2)-int64(l.T1), e.T2Min)
	case l.Amount < e.Amount:
		return fmt.Errorf("amount %d is below the agreed %d", l.Amount, e.Amount)
	case l.Asset != e.Asset:
		return fmt.Errorf("asset %q is not the agreed %q", l.Asset, e.Asset)
	}
	return nil
}

func (s *Swap) expect() (LockExpect, error) {
	raw, err := DeroRaw(s.st.MyDero)
	if err != nil {
		return LockExpect{}, err
	}
	own, err := ParseScalar(s.st.Own.Secret)
	if err != nil {
		return LockExpect{}, err
	}
	return LockExpect{HA: SHA256Hex(own[:]), HK: s.st.HK, HB: s.st.Peer.H, TakerRaw: raw, Amount: s.st.Terms.AmountDero, Asset: s.st.Terms.Asset, T1Min: s.cfg.T1Min, T2Min: s.cfg.T2Min}, nil
}

func (s *Swap) stepAlice(ctx context.Context) error {
	st := s.st
	switch st.Phase {
	case "":
		if err := s.beginSwap(); err != nil {
			return err
		}
		if err := s.keygen(ctx); err != nil {
			return s.refuseIfReused(err)
		}
		v, err := NewScalar(s.random())
		if err != nil {
			return err
		}
		if err := s.recordSecret("view", v[:]); err != nil {
			return s.refuseIfReused(err)
		}
		st.View, st.ViewPub = v.Hex(), v.Public().Hex()
		if err := s.ownWallet(ctx); err != nil {
			return err
		}
		a, err := s.xmr.Address(ctx)
		if err != nil {
			return err
		}
		h, err := s.xmr.Height(ctx)
		if err != nil {
			return err
		}
		st.MyXmr, st.MyDero, st.RestoreHeight = a, s.cfg.MyDero, h
		return s.save("setup")

	case "setup":
		var m offerBody
		ok, err := s.recv(ctx, KindOffer, &m, s.cfg.Poll)
		if err != nil || !ok {
			return err
		}
		want := Terms{AmountDero: s.cfg.AmountDero, AmountXmr: s.cfg.AmountXmr, Net: s.cfg.Net.String()}
		got := Terms{AmountDero: m.AmountDero, AmountXmr: m.AmountXmr, Asset: m.Asset, Net: m.Net, T1Delta: m.T1Delta, T2Delta: m.T2Delta}
		switch {
		case m.HTLC != s.cfg.HTLC:
			return s.finish("refused", "offer refused: it names contract "+m.HTLC+", mine is "+s.cfg.HTLC)
		case got.AmountDero != want.AmountDero || got.AmountXmr != want.AmountXmr || got.Asset != want.Asset || got.Net != want.Net:
			return s.finish("refused", fmt.Sprintf("offer refused: %d DERO-units for %d piconero of %q on %s, mine is %d for %d on %s", got.AmountDero, got.AmountXmr, got.Asset, got.Net, want.AmountDero, want.AmountXmr, want.Net))
		case got.T1Delta < s.cfg.T1Min || got.T2Delta < s.cfg.T2Min:
			return s.finish("refused", fmt.Sprintf("offer refused: timelock widths %d/%d below the minimums %d/%d", got.T1Delta, got.T2Delta, s.cfg.T1Min, s.cfg.T2Min))
		}
		st.Terms = got
		if err := s.queue(KindAliceSetup, aliceSetupBody{S: st.Own.S, H: st.Own.H, Proof: st.Own.Proof, V: st.ViewPub, View: st.View, Dero: st.MyDero}); err != nil {
			return err
		}
		return s.save("offer_checked")

	case "offer_checked":
		var m bobSetupBody
		ok, err := s.recv(ctx, KindBobSetup, &m, s.cfg.Poll)
		if err != nil || !ok {
			return err
		}
		if err := s.verifyPeer(ctx, m.H, m.S, m.Proof); err != nil {
			return s.finish("refused", "bob_setup refused: "+err.Error())
		}
		if !isHash(m.HK) {
			return s.finish("refused", "bob_setup refused: hk is not 64 hex")
		}
		st.HK = m.HK
		joint, err := s.jointAddress()
		if err != nil {
			return s.finish("refused", "bob_setup refused: "+err.Error())
		}
		st.Joint = joint
		s.cfg.Log("alice: π_b verified; joint address %s", joint)
		return s.save("verified_pi_b")

	case "verified_pi_b":
		var m deroLockedBody
		ok, err := s.recv(ctx, KindDeroLocked, &m, s.cfg.Poll)
		if err != nil || !ok {
			return err
		}
		st.Lock.ID = m.ID
		return s.save("lock_announced")

	case "lock_announced":
		// Invariant 3, first sight: every field of the lock as the chain has it
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if errors.Is(err, ErrNoSuchLock) {
			return nil // the announcement can outrun the wallet's view of the block
		}
		if err != nil {
			return err
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		e, err := s.expect()
		if err != nil {
			return err
		}
		if err := CheckLock(l, h, e); err != nil {
			return s.finish("refused", "lock refused: "+err.Error())
		}
		st.Lock.SeenHeight, st.Lock.T1, st.Lock.T2, st.Lock.Amount = h, l.T1, l.T2, l.Amount
		s.cfg.Log("alice: lock %d checked at height %d: t1 %d t2 %d amount %d", l.ID, h, l.T1, l.T2, l.Amount)
		return s.save("saw_dero_lock")

	case "saw_dero_lock":
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h < st.Lock.SeenHeight+s.cfg.StableBlocks {
			return nil
		}
		// second sight, past DERO's stability depth: the same lock, still open, with the budget that remains
		// after the wait (the minimum less the blocks spent waiting)
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		e, err := s.expect()
		if err != nil {
			return err
		}
		// the policy floor was met at first sight; the wait and the poll granularity have spent a few blocks
		// since, so the second sight asks that at least half the floor remains — the stable depth is far below
		// half, and a lock that has lost half its budget is one to walk away from
		e.T1Min = (e.T1Min + 1) / 2
		if err := CheckLock(l, h, e); err != nil {
			return s.finish("refused", "lock refused at second sight: "+err.Error())
		}
		if l.T1 != st.Lock.T1 || l.T2 != st.Lock.T2 || l.Amount != st.Lock.Amount {
			return s.finish("refused", "lock changed between sightings")
		}
		// Invariant 5: the intent is durable before the transfer
		return s.save("xmr_locking")

	case "xmr_locking":
		if s.resumedPhase == "xmr_locking" || s.transferTried {
			// the transfer may or may not have gone out: a restart landed here, or the one call this process
			// made returned an error. Paying again could pay twice; not paying costs nothing — if it never went
			// out, Bob refunds at t1 and this wallet still holds the coins. So carry on as if locked and let
			// the chain say the rest.
			st.Xmr.LockUnknown = true
			s.cfg.Log("alice: the Monero transfer's fate is unknown; not sending it again")
			return s.save("xmr_locked")
		}
		if err := s.ownWallet(ctx); err != nil {
			return err // nothing was sent: safe to come back here
		}
		s.transferTried = true
		txid, err := s.xmr.Transfer(ctx, st.Joint, st.Terms.AmountXmr)
		if err != nil {
			return err
		}
		st.Xmr.LockTx = txid
		s.cfg.Log("alice: XMR locked: tx %s to %s", txid, st.Joint)
		if err := s.queue(KindXmrLocked, xmrLockedBody{Tx: txid}); err != nil {
			return err
		}
		return s.save("xmr_locked")

	case "xmr_locked":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		if l.State != LockOpen {
			return s.afterLockMoved(l)
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h >= st.Lock.T1 {
			s.cfg.Log("alice: t1 (%d) passed without the gate: waiting for bob's refund, or t2", st.Lock.T1)
			return s.save("await_refund")
		}
		var m gateBody
		ok, err := s.recv(ctx, KindGate, &m, s.cfg.Poll)
		if err != nil || !ok {
			return err
		}
		kb, err := hex.DecodeString(m.KB)
		if err != nil || len(kb) != 32 || SHA256Hex(kb) != l.HK {
			s.cfg.Log("alice: the gate received does not hash to hk on the chain; ignoring it")
			delete(st.Consumed, KindGate)
			return nil
		}
		st.Gate = m.KB
		return s.save("gate_received")

	case "gate_received", "claiming":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		if l.State != LockOpen {
			return s.afterLockMoved(l)
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		// Invariants 9 and 11: a Claim mined from here on could still be inside DERO's replaceable depth at t1,
		// and one sent later than that is a fee on a call the contract refuses; either way the exit that remains
		// is Bob's refund (her XMR back with s_b), or Punish at t2
		if cutoff := claimCutoff(st.Lock.T1, s.cfg.ClaimMargin); h+1 >= cutoff {
			s.cfg.Log("alice: height %d is past the claim cutoff %d (t1 %d − %d stable − %d margin): not claiming; waiting for bob's refund, or t2", h, cutoff, st.Lock.T1, DefaultStableBlocks, s.cfg.ClaimMargin)
			return s.save("await_refund")
		}
		if st.Phase != "claiming" {
			if err := s.save("claiming"); err != nil {
				return err
			}
		}
		txid, err := s.dero.Claim(ctx, st.Lock.ID, st.Own.Secret, st.Gate)
		if err != nil {
			return err
		}
		st.ClaimTx = txid
		s.cfg.Log("alice: claimed: tx %s", txid)
		_ = s.queue(KindDone, doneBody{Outcome: "claimed"})
		return s.finish("claimed", "claimed the DERO: tx "+txid)

	case "await_refund":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		if l.State != LockOpen {
			return s.afterLockMoved(l)
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h >= st.Lock.T2 {
			return s.save("punish_window")
		}
		return nil

	case "refund_seen":
		spend, err := s.spendKey()
		if err != nil {
			return err
		}
		if err := s.openOrGenerate(ctx, s.walletName("spend"), spend); err != nil {
			return err
		}
		return s.save("sweep_ready")

	case "sweep_ready", "sweeping":
		return s.sweep(ctx)

	case "punish_window", "punishing":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		if l.State != LockOpen {
			return s.afterLockMoved(l)
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h < st.Lock.T2 {
			return nil
		}
		if st.Phase != "punishing" {
			if err := s.save("punishing"); err != nil {
				return err
			}
		}
		txid, err := s.dero.Punish(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		st.PunishTx = txid
		s.cfg.Log("alice: punished: tx %s", txid)
		_ = s.queue(KindDone, doneBody{Outcome: "punished"})
		return s.finish("punished", "punished: took the DERO, tx "+txid+"; any XMR locked stays under the joint key")
	}
	return fmt.Errorf("alice: unknown phase %q", st.Phase)
}

// claimCutoff is the first height at which Alice sends no Claim: one mined at h is stable at h + 8, and it must
// be stable before t1 or a replaced block can re-mine it after t1, refused, with s_a already read by Bob
// (review R2). margin is the operator's slack on top of DERO's depth; a t1 inside the two leaves no height.
func claimCutoff(t1, margin uint64) uint64 {
	if t1 <= DefaultStableBlocks+margin {
		return 0
	}
	return t1 - DefaultStableBlocks - margin
}

// afterLockMoved routes Alice once the lock is no longer open, from whichever waiting phase noticed. Whatever
// she was repeating (her Monero txid, usually) has been overtaken by the chain and stops.
func (s *Swap) afterLockMoved(l Lock) error {
	s.st.LastSent, s.st.LastSentAt = nil, time.Time{}
	switch l.State {
	case LockClaimed:
		if s.st.ClaimTx == "" {
			s.st.ClaimTx = "(claimed; txid not recorded)"
		}
		return s.finish("claimed", "claimed the DERO")
	case LockRefunded:
		if err := s.checkRevealed(l.XB); err != nil {
			return fmt.Errorf("refunded lock: %w", err)
		}
		s.cfg.Log("alice: bob refunded; s_b read from the chain")
		if s.st.Xmr.LockTx == "" && !s.st.Xmr.LockUnknown {
			return s.finish("aborted", "bob refunded before any XMR was locked: nothing to recover")
		}
		return s.save("refund_seen")
	case LockPunished:
		return s.finish("punished", "punished: took the DERO; any XMR locked stays under the joint key")
	}
	return nil
}
