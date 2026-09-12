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
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Policy defaults (§1, timelocks): DERO blocks are 18 s. The policy values come from the mainnet profile
// (mainnet_profile.json, through the generated profile_gen.go, MT-B16); profile_test.go checks every one of them.
const (
	DefaultT1Min uint64 = profileT1Min // Alice must lock, see the Monero confirmations, receive k_b and claim in this
	DefaultT2Min uint64 = profileT2Min // Bob's refund window
	// DefaultConfirmations is the Monero depth before the gate (Invariant 10, review R3). Ten is
	// CRYPTONOTE_DEFAULT_TX_SPENDABLE_AGE — below it nobody can spend the output, so ten buys Bob nothing but
	// time — and Monero has no consensus bound on a reorganisation, with an 18-block one reported; twenty is the
	// policy, and the one number in the protocol that is an assumption rather than a proof.
	DefaultConfirmations uint64 = profileConfirmationsFloor
	// DefaultStableBlocks is DERO's STABLE_LIMIT: a block this deep is never replaced. Alice locks XMR only against
	// a lock this old, and a Claim is only acted on once it is this deep.
	DefaultStableBlocks uint64 = 8
	// DefaultClaimMargin is Alice's slack on top of the stable depth (Invariant 9, review R2): she sends no Claim
	// once height + 1 ≥ t1 − 8 − margin, because a Claim mined in the last eight blocks before t1 can be re-mined
	// at or after t1 and refused, with s_a already read by Bob.
	DefaultClaimMargin uint64 = profileClaimMargin
	// DefaultT1Max and DefaultT2Max are the ceilings on the windows the maker chooses. htlc.bas admits a t2 up
	// to a million blocks out and the floors above say nothing about the top, so without a ceiling the taker's
	// XMR can be locked against a t1 months away with no exit of her own before it: she cannot claim without
	// k_b, she cannot refund, and Punish is only at t2, so the maker holds a free option on her coins for as
	// long as he named. Three times the floor is wide enough for every honest delay at 18 s blocks (about six
	// hours) and short enough that the option is worth little.
	DefaultT1Max uint64 = profileT1Max
	DefaultT2Max uint64 = profileT2Max
	// DefaultSweepWaits is how many passes the sweep gives a joint wallet that shows nothing spendable before
	// it takes that answer at face value. A wallet made from keys has scanned nothing until it is refreshed,
	// and the output it is waiting for needs ten blocks to become spendable, so the first answer is routinely a
	// zero that means "not yet" — while a transfer that was never mined, one frozen by the sender's unlock_time
	// and one the scanner ignored as a duplicate one-time key all look exactly the same and never resolve. This
	// is the line between them: at the default -interval of five seconds, twenty minutes.
	DefaultSweepWaits uint64 = 240
	// DefaultGateDeadline is G (U2, docs/ATOMIC_SWAP_STUDY.md §2.1): Alice treats a k_b that reaches her at or
	// after t1 − G as never received, and waits for Bob's refund or t2 instead of claiming. Her Claim carries s_a
	// in the clear into a mempool ordered by fee per byte, so a Bob who releases the gate late and keeps her
	// Claim out of the chain until t1 can read s_a, sweep the XMR, and refund; G is how many blocks of
	// suppression that costs him. 100 is the study's assumed value (operator decision 5), not a measurement; an
	// honest gate arrives near block 150 of the default 420.
	DefaultGateDeadline uint64 = profileGateDeadline
	// DefaultFixedFee is what Refund and Punish are sent at (NewDeroRPC's FixedFee): the daemon's estimator runs
	// a contract at height 0, where both are refused, so neither can be estimated.
	DefaultFixedFee uint64 = 3000
	// DefaultClaimFee is what one htlc.bas v2 Claim is sent at: its storage gas, 304 measured on the DVM
	// (tests/htlc_test.go TestHtlcClaim), plus the 200 exit() adds. The wallet pays a fee it is given exactly —
	// walletapi/transaction_build.go computes one only when it is zero.
	DefaultClaimFee uint64 = 504
	// DefaultStipend is the DERO the maker gives the taker (Path 0, §2.0.1): her worst case, a Claim mined and
	// refused at its last re-send followed by Punish at its last re-send, each doubled twice (Resends = 2):
	// (3,000 + 504) × 4 = 14,016 atomic units, 0.14016 DERO. A pool trade after the Claim needs its own fee on
	// top, which this does not cover.
	DefaultStipend uint64 = (DefaultFixedFee + DefaultClaimFee) << 2
	// DefaultStipendSettleBlocks: a DERO transaction may only be mined within TX_VALIDITY_HEIGHT = 11 blocks of
	// the height it was built at (blockchain/miner_block.go; blockchain.go refuses it later), so twelve blocks
	// after the maker handed a stipend to his wallet it has either been mined or never will be. He sends another
	// only after that, and only if his own wallet's history shows none: the stipend goes out once.
	DefaultStipendSettleBlocks uint64 = 12
	// DefaultFeeReserve is the DERO the maker must hold beyond the lock and the stipend before he locks: his
	// Refund at its last re-send (3,000 × 4) and about a thousand for the Lock itself (859 measured,
	// docs/audit/FINDINGS_2026-09-10.md XMR-01).
	DefaultFeeReserve uint64 = DefaultFixedFee<<2 + 1000
	// DefaultCloseAfter is how many blocks past t2 the maker's companion leaves an open lock to the taker's own
	// Punish before sending it itself. htlc.bas v2 pays the taker whoever sends it (LIV-07), so the maker gains
	// nothing but a closed lock and a finished companion for his fee; waiting first spares him the fee whenever
	// the taker is there to do it.
	DefaultCloseAfter uint64 = 20
)

// Config is one companion's parameters. The minimums are policy the contract leaves to the client (§1); a
// simulator run lowers them, a mainnet run must not.
type Config struct {
	HTLC       string
	AmountDero uint64 // atomic units
	AmountXmr  uint64 // piconero
	Net        Network
	// DeroNet is the DERO network this party's daemon is on: "mainnet", "testnet" or "simulator", as the daemon
	// itself reports it (the command line reads DERO.GetInfo). It is required and is a term of the offer (GAP-007).
	DeroNet string
	MyDero  string // this party's DERO address, read from its wallet by the caller
	// MoneroWalletName is the personal wallet's file name on the wallet-rpc, so it can be reopened after the
	// joint wallets were generated on the same rpc. "" means it is the wallet that is open and stays open.
	// MoneroWalletPassword is that wallet's own password.
	MoneroWalletName     string
	MoneroWalletPassword string
	// MoneroWalletDir is the wallet-rpc's --wallet-dir, when it is on this machine: the companion then deletes the
	// joint wallets' files once the sweep has gone out (GAP-020). monero-wallet-rpc has no call that deletes a
	// wallet, so without it the files stay, encrypted under the swap's own password.
	MoneroWalletDir string
	// Monerod, when set, is the monerod the wallet-rpc must use (GAP-010): it is named to the wallet-rpc with
	// set_daemon after every open and every generate, because the rpc keeps a set_daemon only for the wallet that
	// was open when it was sent.
	Monerod string
	// Witnesses are further DERO daemons that must show the taker the very lock her own node shows before she locks
	// any XMR (MT-B09). Her own node alone could show her a lock that is not on the chain: she would lock XMR against
	// nothing, and her Claim would hand s_a to whoever runs that node. Each is only read.
	Witnesses []LockReader
	// ResumeCommand is the command that resumes this companion, as the recovery card prints it and the wake runs it
	// (U9, MT-B15). Wake, when set, registers that wake with the operating system; nil registers none.
	ResumeCommand string
	Wake          WakeFunc

	T1Min, T2Min     uint64 // policy floors (Alice refuses a lock below them; Bob refuses to choose below them)
	T1Max, T2Max     uint64 // policy ceilings: Alice refuses an offer, and a lock, whose windows are wider
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
	// SweepWaits is how many passes the sweep waits on a joint wallet with nothing spendable in it.
	SweepWaits uint64
	// Asset is the DERO-side leg (U1): "" is DERO, otherwise the 64-hex SCID of the token or NFA unit the maker
	// locks. AssetDecimals is its scale. Both sides configure both; the taker refuses an offer that differs, and
	// checks the lock on the chain against the asset agreed.
	Asset         string
	AssetDecimals uint64
	// Stipend is what the maker sends the taker for gas (Path 0). StipendMin is the least the taker accepts, and
	// the DERO she must be able to spend before she locks any XMR.
	Stipend    uint64
	StipendMin uint64
	// StipendSettleBlocks is how long the maker waits on a stipend he handed to his wallet without seeing it
	// mined before he looks again and, finding none, sends one.
	StipendSettleBlocks uint64
	// GateDeadline is G (U2): Alice treats a gate reaching her at or after t1 − G as never received. Bob's
	// GateMargin is raised to at least G + 2, so an honest maker never sends a gate she would ignore.
	GateDeadline uint64
	// CloseAfter: blocks past t2 after which the maker's companion sends Punish itself on a lock still open.
	CloseAfter uint64

	Poll        time.Duration // how long one Step waits on the channel when a message is expected
	ResendAfter time.Duration // how often the last message is repeated while the protocol has not moved on
	Log         func(format string, a ...interface{})
}

// LockReader is what a witness node is asked: the reads the taker's lock check makes, and nothing that sends. A
// DeroRPC with no wallet is one.
type LockReader interface {
	Height(ctx context.Context) (uint64, error)
	ReadLock(ctx context.Context, id uint64) (Lock, error)
	CodeSHA256(ctx context.Context) (string, error)
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
	def(&c.T1Delta, c.T1Min+(profileT1Default-profileT1Min))
	def(&c.T2Delta, c.T2Min+(profileT2MinusT1-profileT2Min))
	def(&c.T1Max, DefaultT1Max)
	def(&c.T2Max, DefaultT2Max)
	def(&c.Confirmations, DefaultConfirmations)
	def(&c.StableBlocks, DefaultStableBlocks)
	def(&c.ClaimMargin, DefaultClaimMargin)
	def(&c.GateDeadline, DefaultGateDeadline)
	def(&c.GateMargin, DefaultStableBlocks+c.ClaimMargin+2)
	// U2: a gate Bob releases inside t1 − G is one Alice ignores, so his margin is at least G plus two blocks
	// for the channel, whatever it was set to
	if c.GateMargin < c.GateDeadline+2 {
		c.GateMargin = c.GateDeadline + 2
	}
	def(&c.LockSettleBlocks, 3)
	def(&c.SweepWaits, DefaultSweepWaits)
	def(&c.Stipend, DefaultStipend)
	def(&c.StipendMin, DefaultStipend)
	def(&c.StipendSettleBlocks, DefaultStipendSettleBlocks)
	def(&c.CloseAfter, DefaultCloseAfter)
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
	// scanWarned: the operator has been told once that the joint wallet holds nothing spendable yet.
	scanWarned bool
	// witnessWaiting is the last reason a witness node gave the taker to wait before her XMR lock, said once (MT-B09).
	witnessWaiting string
	// blindWarned: the operator has been told once that the contract cannot be read and the refund goes out blind.
	blindWarned bool
	// stipendNote is the last reason the operator was given for the XMR lock waiting on the stipend; a reason is
	// said once, and again only when it changes.
	stipendNote string
	// codeChecked: this process has read the contract's code and found it is the htlc.bas it was built for (U4).
	codeChecked bool
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
	switch cfg.DeroNet {
	case "mainnet", "testnet", "simulator":
	default:
		return nil, fmt.Errorf("config needs the DERO network the daemon reports (mainnet, testnet or simulator), not %q", cfg.DeroNet)
	}
	if st.Terms.DeroNet != "" && st.Terms.DeroNet != cfg.DeroNet {
		return nil, fmt.Errorf("this state agreed a swap on DERO %s, and the daemon is on %s", st.Terms.DeroNet, cfg.DeroNet)
	}
	if _, err := DeroRaw(cfg.MyDero); err != nil {
		return nil, err
	}
	// The lock names one maker and one taker, and only those wallets can Refund or Claim it. A resume pointed
	// at a different -dero wallet is therefore a swap that can never be left: the same check ownWallet makes on
	// the Monero side, made here.
	if st.MyDero != "" && st.MyDero != cfg.MyDero {
		return nil, fmt.Errorf("this state belongs to DERO address %s, the wallet at -dero is %s: only the first can refund the lock it made, or claim as the taker it was named", st.MyDero, cfg.MyDero)
	}
	if cfg.T1Delta < cfg.T1Min || cfg.T2Delta < cfg.T2Min {
		return nil, fmt.Errorf("timelock widths %d/%d are below the policy minimums %d/%d", cfg.T1Delta, cfg.T2Delta, cfg.T1Min, cfg.T2Min)
	}
	if role == "bob" && cfg.T1Delta <= cfg.GateMargin {
		return nil, fmt.Errorf("a t1 width of %d leaves no block in which the gate may go out (gate margin %d, from the gate deadline %d)", cfg.T1Delta, cfg.GateMargin, cfg.GateDeadline)
	}
	// U1: the asset is compared byte for byte with what the contract stores, which DERO.GetSC hands back as lower
	// case hex; a SCID typed in capitals is the same asset and is written the one way
	if cfg.Asset != "" {
		cfg.Asset = strings.ToLower(cfg.Asset)
		if !isHash(cfg.Asset) {
			return nil, fmt.Errorf("asset %q is not a 64-hex SCID", cfg.Asset)
		}
	}
	if st.Terms.Asset != "" && st.Terms.Asset != cfg.Asset {
		return nil, fmt.Errorf("this state agreed asset %q, the flags say %q", st.Terms.Asset, cfg.Asset)
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
	Decimals   uint64 `json:"decimals"`
	Stipend    uint64 `json:"stipend"`
	DeroNet    string `json:"deroNet"`
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

// isHash is 64 lower-case hex characters. htlc.bas compares HEX(SHA256(preimage)), which is lower case, with the
// stored string, so an upper-case hash is one no preimage can ever match: an H_k in capitals would pass a
// case-blind check, the taker would lock XMR, and every k_b would then be refused on chain (attack D, study
// §2.1). swap.bas and board.bas refuse such a hash themselves; htlc.bas checks only the length, so the companion
// refuses it for both parties before anything is locked.
func isHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for _, c := range h {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// stipendPort is the destination port the stipend carries (Path 0): a tag both parties compute from H_k, which is
// fresh for one swap (Invariant 13), so each finds the stipend in their own wallet's history by the tag alone —
// the maker to know he has already paid it, the taker to know it has arrived — with no txid passed between them.
//
// It is kept to 53 bits: the wallet decodes the payload's uint64 exactly, but anything on the way that reads JSON
// numbers as floating point would round a larger tag to another one, and a stipend nobody can find is one the
// taker waits on until her budget runs out.
func stipendPort(hk string) uint64 {
	sum := sha256.Sum256([]byte("xmrswap stipend|" + hk))
	if p := binary.BigEndian.Uint64(sum[:8]) >> 11; p != 0 {
		return p
	}
	return 1
}

// deroNet is the network a DERO address is written for: "dero" mainnet, "deto" testnet and the simulator (an
// integrated address's trailing "i" dropped). DeroRaw accepts both spellings of one key, so this is what tells
// two addresses of the same key on different networks apart.
func deroNet(addr string) string {
	s := strings.ToLower(addr)
	pos := strings.LastIndex(s, "1")
	if pos < 1 {
		return ""
	}
	return strings.TrimSuffix(s[:pos], "i")
}

// DeroNetwork is deroNet for the command line's network check.
func DeroNetwork(addr string) string { return deroNet(addr) }

// checkCode is U4: before the first thing either party puts into the contract, its code must be the htlc.bas this
// companion was built for. Read once per process; the SCID does not change underneath a swap.
func (s *Swap) checkCode(ctx context.Context) (bool, error) {
	if s.codeChecked {
		return true, nil
	}
	code, err := s.dero.CodeSHA256(ctx)
	if err != nil {
		return false, err
	}
	if code != HTLCCodeSHA256 {
		return false, s.finish("refused", "the contract at "+s.cfg.HTLC+" is not htlc.bas v2: its code hashes to "+code+", and this companion was built for "+HTLCCodeSHA256+"; nothing of this party's was locked in it")
	}
	s.codeChecked = true
	return true, nil
}

func (s *Swap) random() io.Reader {
	if s.rand != nil {
		return s.rand
	}
	return rand.Reader
}

// beginSwap names the swap before its first secret exists, so the register can bind every secret to it, and makes
// the password its joint wallets will be encrypted under (GAP-020). The password comes from crypto/rand directly:
// it is never compared with anything, so nothing is gained by letting a test choose it.
func (s *Swap) beginSwap() error {
	if s.st.JointPassword == "" {
		var pw [32]byte
		if _, err := io.ReadFull(rand.Reader, pw[:]); err != nil {
			return err
		}
		s.st.JointPassword = hex.EncodeToString(pw[:])
	}
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

// claimIdentity enters a public identity this swap takes as its own — its joint address, the lock it accepted, a
// Monero payment its gate counted — in the register its secrets are in. Another swap run from this directory that
// is handed the same one is refused instead of being served by it twice: one Monero payment opening two gates,
// one lock paying for two setups (the operator's replay rule). The register covers the swaps run from one
// directory; that is where a companion keeps its state, and a swap elsewhere has its own register. An identity is
// taken per role: the maker and the taker of one swap share its joint address by design, and on the simulator
// they share a directory, so the entry is "this maker's" or "this taker's", never the swap's as a whole.
func (s *Swap) claimIdentity(kind, id string) error {
	return s.used.Record([]byte(kind+"|"+s.st.Role+"|"+id), s.st.SwapID, s.st.Role+" "+kind)
}

// refuseIdentity ends the swap on an identity another swap has already taken; any other error is retried.
func (s *Swap) refuseIdentity(what string, err error) error {
	if errors.Is(err, ErrSecretReused) {
		return s.finish("refused", what+" refused: "+err.Error()+"; nothing one swap has taken is served to another")
	}
	return err
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
		return errors.New("peer hash is not 64 lower-case hex")
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

// checkRevealed verifies a share read from the chain is the peer's and keeps it in the form a key is made from:
// the 32 bytes as revealed hash to their H, and the value reduced mod ℓ is the discrete log of their S
// (RecoverShare, MT-B25). The contract checked the hash; the proof at setup tied that hash to S; this is the last look
// before a key is formed from it. A value the peer revealed unreduced — x = s + kℓ, which the circuit and the contract
// both accept — is the same share, and the swap goes on with s rather than stalling on it.
func (s *Swap) checkRevealed(x string) error {
	sc, err := RecoverShare(x, s.st.Peer.H, s.st.Peer.S)
	if err != nil {
		return fmt.Errorf("revealed share: %w", err)
	}
	s.st.Revealed = sc.Hex()
	return nil
}

// walletName is the joint wallet's file name on the wallet-rpc. The contract's scid and the lock id are both
// public, so on their own they name a file anyone could have made first; SwapID is this swap's own random name
// (state.go), which is what keeps the name from ever being inherited from an unrelated run.
func (s *Swap) walletName(kind string) string {
	return jointWalletName(s.cfg.HTLC, s.st.SwapID, s.st.Lock.ID, kind)
}

// jointWalletName is walletName for anyone holding only the state file (the status subcommand).
func jointWalletName(htlc, swapID string, lockID uint64, kind string) string {
	scid := htlc
	if len(scid) > 8 {
		scid = scid[:8]
	}
	if swapID == "" {
		swapID = "0"
	}
	return "xmrswap-" + scid + "-" + swapID + "-" + strconv.FormatUint(lockID, 10) + "-" + kind
}

// openJoint opens a joint wallet by name, creating it from keys when it does not exist yet, and then requires
// that the wallet the rpc opened really holds st.Joint. The name is not evidence: a file of that name already
// in the wallet-rpc's wallet-dir is accepted by open_wallet whatever keys it holds, and generate_from_keys —
// the only call that checks the address — is then never reached, so the gate would count coins at some other
// address and the sweep would spend some other wallet. It is the check ownWallet makes for the party's own
// wallet, made for the two the swap creates.
//
// The refresh at the end is what makes the balance mean anything: generate_from_keys sets the restore height
// and returns (wallet_rpc_server.cpp on_generate_from_keys never calls refresh), so a wallet made from keys
// has scanned nothing at all until the `refresh` RPC or the server's background idle loop reaches it. Both
// calls are safe to repeat, so a restart anywhere in here is harmless.
func (s *Swap) openJoint(ctx context.Context, name, spendHex string) error {
	if err := s.xmr.OpenWallet(ctx, name, s.st.JointPassword); err != nil {
		if err := s.xmr.GenerateFromKeys(ctx, name, s.st.Joint, s.st.View, spendHex, s.st.JointPassword, s.st.RestoreHeight); err != nil {
			return fmt.Errorf("generate %s: %w", name, err)
		}
		if err := s.xmr.OpenWallet(ctx, name, s.st.JointPassword); err != nil {
			return err
		}
	}
	if err := s.pinDaemon(ctx); err != nil {
		return err
	}
	a, err := s.xmr.Address(ctx)
	if err != nil {
		return err
	}
	if a != s.st.Joint {
		return fmt.Errorf("the Monero wallet %s holds %s, not this swap's joint address %s", name, a, s.st.Joint)
	}
	if err := s.xmr.Refresh(ctx, s.st.RestoreHeight); err != nil {
		return fmt.Errorf("refresh %s: %w", name, err)
	}
	return nil
}

// ownWallet makes sure the personal wallet is the open one before anything is read from or paid out of it.
func (s *Swap) ownWallet(ctx context.Context) error {
	if s.cfg.MoneroWalletName != "" {
		if err := s.xmr.OpenWallet(ctx, s.cfg.MoneroWalletName, s.cfg.MoneroWalletPassword); err != nil {
			return fmt.Errorf("open own wallet: %w", err)
		}
	}
	if err := s.pinDaemon(ctx); err != nil {
		return err
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
//
// counted is the txids that made up the sum. get_transfers reports one entry per transaction and address and no
// output index, and the address is this swap's own, so a txid names the payment to this joint address: the gate
// records each one as this swap's before it opens (claimIdentity), and a payment already counted by another swap
// keeps it shut.
func (s *Swap) confirmedAtJoint(ctx context.Context) (sum, h uint64, counted []string, err error) {
	if err := s.openJoint(ctx, s.walletName("view"), ""); err != nil {
		return 0, 0, nil, err
	}
	h, err = s.xmr.Height(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	// wallet2::get_payments treats min_height as exclusive; one block early loses nothing and misses nothing
	min := s.st.RestoreHeight
	if min > 0 {
		min--
	}
	in, err := s.xmr.Incoming(ctx, min)
	if err != nil {
		return 0, 0, nil, err
	}
	for _, t := range in {
		// MT-B14, review R6: an output whose transaction carries an unlock_time never counts, whatever its value.
		// monero 0.18.5.1 unlocks an output only past both its unlock_time and 10 blocks of depth (wallet2.cpp:7308-
		// 7344), and a 0.18.5.1 wallet builds no transfer with one (wallet2.cpp:7826), so one here is another
		// program's or a coinbase's (height + 60, cryptonote_config.h:44). The wallet's own "locked" flag and the
		// unlocked-balance cap below keep a lock that has not expired out already; this line also keeps out one that
		// expires before t1 − G, which no honest taker's companion makes.
		if t.UnlockTime != 0 {
			if !s.frozenWarned {
				s.frozenWarned = true
				s.cfg.Log("bob: a transfer at the joint address carries unlock_time %d: it does not count towards the gate, whatever its confirmations", t.UnlockTime)
			}
			continue
		}
		if t.Confirmations >= s.cfg.Confirmations && t.Unlocked {
			sum += t.Amount
			counted = append(counted, t.TxID)
		}
	}
	unlocked, err := s.xmr.UnlockedBalance(ctx)
	if err != nil {
		return 0, 0, nil, err
	}
	if unlocked < sum {
		sum = unlocked
	}
	return sum, h, counted, nil
}

// sweep spends the joint output to this party's own address, at most once per swap: the intent is durable
// before the call, and a restart that finds the intent without a result does not try again — the spend key
// stays in the state file for a hand-driven sweep, and a second attempt is the one thing worse than a slow one.
func (s *Swap) sweep(ctx context.Context) error {
	switch s.st.Phase {
	case "sweep_ready":
		spend, err := s.spendKey()
		if err != nil {
			return err
		}
		name := s.walletName("spend")
		if err := s.openJoint(ctx, name, spend); err != nil {
			return err
		}
		// A wallet with nothing spendable has spent nothing, so a refusal on it is not the unknown the terminal
		// state is for: the scan may still be behind the tip, or the output not yet ten blocks deep. Stay in
		// sweep_ready and try again on the next pass, for as many passes as SweepWaits allows. The budget is
		// what keeps it from waiting for ever on the three cases that never resolve — a transfer that was never
		// mined, one the sender froze with an unlock_time, one the scanner ignored as a duplicate one-time key —
		// which from here look exactly like a scan that is behind.
		unlocked, err := s.xmr.UnlockedBalance(ctx)
		if err != nil {
			return err
		}
		if unlocked == 0 && s.st.Sweep.Waits < s.cfg.SweepWaits {
			s.st.Sweep.Waits++
			if !s.scanWarned {
				s.scanWarned = true
				s.cfg.Log("%s: the joint wallet %s shows nothing spendable; waiting for the scan and for the ten-block spendable age, and trying again for up to %d passes", s.st.Role, name, s.cfg.SweepWaits)
			}
			return s.st.Save()
		}
		if err := s.save("sweeping"); err != nil {
			return err
		}
		txs, err := s.xmr.SweepAll(ctx, s.st.MyXmr)
		s.closeJoint(ctx, name)
		if err == nil && s.crash != nil && s.crash("sweep_sent") {
			return errCrashed // the test hook: killed after the sweep went out and before it was recorded (MT-B12)
		}
		if err != nil {
			// the intent stands: nothing is retried automatically, and the operator sees why — and whether anything
			// can have gone out, which a refusal the wallet made before sending rules out
			var rerr *RPCError
			if errors.As(err, &rerr) && refusedBeforeSending(rerr.Code) {
				_ = s.finish("sweep_unknown", "the wallet refused the sweep before sending anything ("+err.Error()+"): the XMR is still at the joint address, and the spend key is in the state file")
				return err
			}
			s.st.Sweep.Unknown = true
			_ = s.finish("sweep_unknown", "the sweep of the joint wallet failed ("+err.Error()+"); the spend key is in the state file")
			return err
		}
		s.st.Sweep.Txs = txs
		s.cfg.Log("%s: swept the joint wallet to %s: %v", s.st.Role, s.st.MyXmr, txs)
		s.removeJointWallets()
		return s.finish("swept", "swept the XMR to "+s.st.MyXmr)
	case "sweeping":
		// MT-B12 (XMR-R1): the process stopped between the durable intent and the record of the sweep. Whether the
		// sweep went out is a question the joint spend wallet answers: it lists every transfer out of it, mined or
		// in the pool, and nobody but this party can have made one (after a Claim only Bob holds s_a + s_b, after
		// a Refund only Alice). A transfer out is the sweep, and it is recorded. None, and the wallet can spend:
		// the sweep never went out, and it is made now. That repeat cannot pay anyone else — sweep_all pays only
		// this party's own address — and if a sweep had gone out unseen, the daemon would refuse the second as a
		// double spend of the same key images. None and nothing to spend: the scan may be behind, and it waits as
		// sweep_ready does, for as many passes as SweepWaits allows.
		spend, err := s.spendKey()
		if err != nil {
			return err
		}
		name := s.walletName("spend")
		if err := s.openJoint(ctx, name, spend); err != nil {
			return err
		}
		out, err := s.xmr.Outgoing(ctx)
		if err != nil {
			return err
		}
		if len(out) > 0 {
			s.closeJoint(ctx, name)
			s.st.Sweep.Txs = out
			s.cfg.Log("%s: restarted during the sweep: the joint wallet lists it (%v); recording it", s.st.Role, out)
			s.removeJointWallets()
			return s.finish("swept", "swept the XMR to "+s.st.MyXmr+" (the sweep was found in the joint wallet after a restart)")
		}
		unlocked, err := s.xmr.UnlockedBalance(ctx)
		if err != nil {
			return err
		}
		if unlocked > 0 {
			s.cfg.Log("%s: restarted during the sweep: the joint wallet has sent nothing and can spend %d; sweeping again", s.st.Role, unlocked)
			return s.save("sweep_ready")
		}
		if s.st.Sweep.Waits < s.cfg.SweepWaits {
			s.st.Sweep.Waits++
			return s.st.Save()
		}
		s.closeJoint(ctx, name)
		s.st.Sweep.Unknown = true
		return s.finish("sweep_unknown", "restarted during the sweep, and the joint wallet shows neither a transfer out nor anything to spend; the spend key is in the state file under "+name)
	}
	return nil
}

// removeJointWallets deletes both joint wallets' files from the wallet-rpc's --wallet-dir once the sweep has gone out
// (GAP-020): the spend wallet holds s_a + s_b and the view wallet the shared view key, and neither has a use after
// the sweep. monero-wallet-rpc has no call that deletes a wallet, so this needs the directory on this machine
// (MoneroWalletDir; on mainnet the wallet-rpc is loopback, so it is). The sweep's transaction is already with the
// wallet-rpc, and if it had to be rebuilt both shares are still in the state file.
func (s *Swap) removeJointWallets() {
	if s.cfg.MoneroWalletDir == "" {
		s.cfg.Log("%s: the joint wallets %s and %s stay in the wallet-rpc's wallet-dir, encrypted under this swap's password; delete them once the sweep is confirmed", s.st.Role, s.walletName("spend"), s.walletName("view"))
		return
	}
	for _, kind := range []string{"spend", "view"} {
		for _, suffix := range []string{"", ".keys", ".address.txt"} {
			p := filepath.Join(s.cfg.MoneroWalletDir, s.walletName(kind)+suffix)
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.cfg.Log("%s: could not delete %s (%v): delete it by hand once the sweep is confirmed", s.st.Role, p, err)
			}
		}
	}
}

// pinDaemon names Config.Monerod to the wallet-rpc for the wallet just opened (GAP-010). monero-wallet-rpc 0.18.5.1
// keeps a set_daemon only for the wallet open when it is sent: after close and open, and for a wallet made later
// from keys, it is back on its own --daemon-address (research/regtest/monero-smoke.log). So it is sent again after
// every open and every generate, and a wallet-rpc that refuses it stops the companion.
func (s *Swap) pinDaemon(ctx context.Context) error {
	if s.cfg.Monerod == "" {
		return nil
	}
	ds, ok := s.xmr.(DaemonSetter)
	if !ok {
		return errors.New("-monerod was given and this monero-wallet-rpc client cannot name a monerod")
	}
	if err := ds.SetDaemon(ctx, s.cfg.Monerod); err != nil {
		return fmt.Errorf("set_daemon %s: %w", s.cfg.Monerod, err)
	}
	return nil
}

// ownHeight is the Monero height at setup: the personal wallet, refreshed, then its height. get_height is the open
// wallet's scanned height, not the chain's (research/regtest/monero-smoke.log), so it is read after a refresh, when it
// is the height of the chain the wallet's daemon reports. It becomes the joint wallets' restore height, where a low
// reading only lengthens their scan.
func (s *Swap) ownHeight(ctx context.Context) (uint64, error) {
	if err := s.xmr.Refresh(ctx, 0); err != nil {
		return 0, fmt.Errorf("refresh own wallet: %w", err)
	}
	return s.xmr.Height(ctx)
}

// closeJoint closes the joint spend wallet as soon as the sweep is over. That wallet holds s_a + s_b, in a file
// encrypted under this swap's joint password (GAP-020): leaving it open in the wallet-rpc for the rest of the
// process's life adds a second way to reach the key for no gain. The file itself stays on the wallet-rpc's host
// until removeJointWallets deletes it, which it can do only when -monero-wallet-dir names that directory; a close
// it cannot make is said out loud rather than assumed.
func (s *Swap) closeJoint(ctx context.Context, name string) {
	if err := s.xmr.CloseWallet(ctx); err != nil {
		s.cfg.Log("%s: the joint wallet %s could not be closed (%v): it is still open in the wallet-rpc and its file holds the joint spend key", s.st.Role, name, err)
	}
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
		h, err := s.ownHeight(ctx)
		if err != nil {
			return err
		}
		dh, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		st.MyXmr, st.MyDero, st.RestoreHeight, st.DeroHeight = a, s.cfg.MyDero, h, dh
		st.Terms = Terms{AmountDero: s.cfg.AmountDero, AmountXmr: s.cfg.AmountXmr, Asset: s.cfg.Asset, Decimals: s.cfg.AssetDecimals, Stipend: s.cfg.Stipend,
			DeroNet: s.cfg.DeroNet, Net: s.cfg.Net.String(), T1Delta: s.cfg.T1Delta, T2Delta: s.cfg.T2Delta}
		if err := s.queue(KindOffer, offerBody{AmountDero: st.Terms.AmountDero, AmountXmr: st.Terms.AmountXmr, Asset: st.Terms.Asset, Decimals: st.Terms.Decimals,
			Stipend: st.Terms.Stipend, DeroNet: st.Terms.DeroNet, Net: st.Terms.Net, HTLC: s.cfg.HTLC, T1Delta: st.Terms.T1Delta, T2Delta: st.Terms.T2Delta}); err != nil {
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
		// one key has an address on every DERO network, and DeroRaw reads them all the same; a taker who gives her
		// address for another network is on another chain from this wallet, and nothing she does there reaches here
		if deroNet(m.Dero) != deroNet(s.cfg.MyDero) {
			return s.finish("refused", "alice_setup refused: her DERO address "+m.Dero+" is written for another network than this wallet's "+s.cfg.MyDero)
		}
		st.View, st.ViewPub, st.PeerDero = m.View, m.V, m.Dero
		joint, err := s.jointAddress()
		if err != nil {
			return s.finish("refused", "alice_setup refused: "+err.Error())
		}
		if err := s.claimIdentity("joint", joint); err != nil {
			return s.refuseIdentity("alice_setup", err)
		}
		st.Joint = joint
		s.cfg.Log("bob: π_a verified; joint address %s; taker %s", joint, m.Dero)
		if err := s.queue(KindBobSetup, bobSetupBody{S: st.Own.S, H: st.Own.H, Proof: st.Own.Proof, HK: st.HK}); err != nil {
			return err
		}
		return s.save("verified_pi_a")

	case "verified_pi_a":
		// U4: the contract must be the htlc.bas this companion was built for before anything goes into it
		if ok, err := s.checkCode(ctx); !ok {
			return err
		}
		// Path 0: the stipend is paid to the taker's address, and a transfer to one that never registered is refused
		reg, err := s.dero.Registered(ctx, st.PeerDero)
		if err != nil {
			return err
		}
		if !reg {
			return s.finish("refused", "the taker's DERO address "+st.PeerDero+" is not registered on this chain: the stipend could not reach it, so nothing was locked")
		}
		// what the lock, the stipend and this side's own fees will draw on, read from this party's own wallet
		if err := s.bobFunds(ctx); err != nil {
			return err
		}
		// decide the windows from the height now, and make the decision durable before the lock goes out
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		st.Lock = LockRecord{IntentHeight: h, T1: h + st.Terms.T1Delta, T2: h + st.Terms.T1Delta + st.Terms.T2Delta, Amount: st.Terms.AmountDero}
		return s.save("dero_locking")

	case "dero_locking":
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		// A lock already sent may still be in the mempool, where DERO.GetSC — and so the idempotent lookup Lock
		// makes — cannot see it. That is true of a restart and equally true of this process's own retry after
		// the client gave up waiting for inclusion, so the wait covers both; without it a second Lock goes out
		// carrying the same hashes, the same taker and the same windows, both mine as separate ids, and the
		// taker claims both with one s_a and one k_b.
		if s.resumedPhase == "dero_locking" || st.Lock.AttemptHeight > 0 {
			if h < st.Lock.settleFrom()+s.cfg.LockSettleBlocks {
				return nil
			}
		}
		txid, id, err := s.dero.Lock(ctx, st.Peer.H, st.HK, st.Own.H, st.PeerDero, st.Lock.T1, st.Lock.T2, st.Terms.Asset, st.Lock.Amount)
		if err != nil {
			// the transaction may be in the pool: record it, and the height it went out at, so the next pass
			// waits for it to land instead of sending another
			st.Lock.AttemptHeight = h
			if txid != "" {
				st.Lock.Tx = txid
			}
			if serr := st.Save(); serr != nil {
				return serr
			}
			return err
		}
		st.Lock.ID, st.Lock.Tx = id, txid
		s.cfg.Log("bob: DERO locked: id %d tx %s t1 %d t2 %d amount %d", id, txid, st.Lock.T1, st.Lock.T2, st.Lock.Amount)
		if err := s.queue(KindDeroLocked, deroLockedBody{ID: id, Tx: txid}); err != nil {
			return err
		}
		return s.save("dero_locked")

	case "dero_locked":
		if st.Lock.LockedHeight == 0 {
			// the Lock came back executed, so its block is at or below the height now
			h, err := s.dero.Height(ctx)
			if err != nil {
				return err
			}
			st.Lock.LockedHeight = h
		}
		if err := s.openJoint(ctx, s.walletName("view"), ""); err != nil {
			return err
		}
		return s.save("stipend_sending")

	case "stipend_sending":
		return s.sendStipend(ctx)

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
		sum, mh, _, err := s.confirmedAtJoint(ctx)
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
		sum, _, counted, err := s.confirmedAtJoint(ctx)
		if err != nil {
			return err
		}
		if sum < st.Terms.AmountXmr {
			return fmt.Errorf("the joint address shows %d confirmed, below %d: not releasing the gate", sum, st.Terms.AmountXmr)
		}
		// replay rule: every payment behind this gate becomes this swap's before the gate opens, and one another
		// swap has already counted keeps it shut — the maker refunds at t1 and the payer recovers with s_b
		for _, tx := range counted {
			if err := s.claimIdentity("xmr-in", tx+"|"+st.Joint); err != nil {
				if !errors.Is(err, ErrSecretReused) {
					return err
				}
				s.cfg.Log("bob: not releasing the gate: %v; refunding after t1", err)
				return s.save("refund_window")
			}
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
		// U6: from here the XMR is his. The Claim can no longer be undone, s_a + s_b opens the joint address, and the
		// output is already past Monero's spend age; the sweep is his first spend of it, not a condition of owning it
		s.cfg.Log("bob: the claim is stable (read at height %d): the XMR at the joint address is this party's; sweeping it", st.Lock.ClaimSeenHeight)
		spend, err := s.spendKey()
		if err != nil {
			return err
		}
		if err := s.openJoint(ctx, s.walletName("spend"), spend); err != nil {
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
			return s.refundBlind(ctx, err)
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
			s.cfg.Log("bob: past t2 (%d) with the lock still open: only Punish can move it now, and it pays alice whoever sends it", st.Lock.T2)
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

	case "punish_window", "closing":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		switch l.State {
		case LockPunished:
			if st.PunishTx != "" {
				return s.finish("punished", "punished: this companion closed the abandoned lock (tx "+st.PunishTx+"), and htlc.bas v2 paid the DERO to alice, the taker")
			}
			return s.finish("punished", "punished: the DERO went to alice after t2; the refund window was missed")
		case LockRefunded:
			return s.finish("refunded", "refunded")
		case LockClaimed:
			return s.claimSeen(ctx, l)
		}
		// LIV-07: in v1 a lock the taker never punished stayed open for ever, and this companion waited on it for
		// ever. v2 pays the taker whoever sends Punish, so after CloseAfter blocks of her silence the maker closes
		// it himself: it costs him the fee, the DERO was lost to him at t2 either way, and no state is left
		// waiting on a person.
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h < st.Lock.T2+s.cfg.CloseAfter {
			return nil
		}
		if st.Phase != "closing" {
			if err := s.save("closing"); err != nil {
				return err
			}
		}
		txid, err := s.dero.Punish(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		st.PunishTx = txid
		s.cfg.Log("bob: closed the abandoned lock with Punish: tx %s; it paid alice, the taker", txid)
		return s.finish("punished", "punished: this companion closed the abandoned lock (tx "+txid+") at t2 + "+strconv.FormatUint(s.cfg.CloseAfter, 10)+", and htlc.bas v2 paid the DERO to alice, the taker")
	}
	return fmt.Errorf("bob: unknown phase %q", st.Phase)
}

// bobFunds is what the maker checks in his own wallet before the lock: the asset (or DERO) the lock carries, the
// stipend, and the fees his Lock and a Refund at its last re-send may cost. A shortfall found after the lock is a
// lock the taker never answers — no stipend, so no XMR — and a refund at t1; found here, it costs nothing.
func (s *Swap) bobFunds(ctx context.Context) error {
	st := s.st
	needDero := st.Terms.Stipend + DefaultFeeReserve
	if st.Terms.Asset == "" {
		needDero += st.Terms.AmountDero
	} else {
		have, err := s.dero.Balance(ctx, st.Terms.Asset)
		if err != nil {
			return err
		}
		if have < st.Terms.AmountDero {
			return fmt.Errorf("this wallet can spend %d of asset %s and the lock needs %d: nothing was locked", have, st.Terms.Asset, st.Terms.AmountDero)
		}
	}
	have, err := s.dero.Balance(ctx, "")
	if err != nil {
		return err
	}
	if have < needDero {
		return fmt.Errorf("this wallet can spend %d DERO atomic units; the lock, the stipend (%d) and the fees this side may pay (%d) need %d: nothing was locked", have, st.Terms.Stipend, DefaultFeeReserve, needDero)
	}
	return nil
}

// sendStipend is Path 0 (docs/ATOMIC_SWAP_STUDY.md §2.0.1): the maker gives the taker the DERO her Claim — and a
// Punish, if it comes to that — will cost, once his own lock is stable and before she has locked anything. It is
// his cost on every swap, a walker's included: a taker who takes it and never locks XMR has been paid the stipend
// for a proof's worth of CPU, and he cannot tell her from a newcomer. It is sent at most once: the intent is
// durable before the wallet sees it, and a stipend that may be in flight is waited out (StipendSettleBlocks, past
// DERO's transaction validity) and then looked for in his own wallet before another goes out.
func (s *Swap) sendStipend(ctx context.Context) error {
	st := s.st
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	if h >= st.Lock.T1 {
		s.cfg.Log("bob: t1 (%d) came before the stipend went out: no stipend; refunding", st.Lock.T1)
		return s.save("refund_window")
	}
	stable, err := s.dero.StableHeight(ctx)
	if err != nil {
		return err
	}
	if stable < st.Lock.LockedHeight {
		return nil // the lock is not stable yet: a gift against a lock DERO could still replace is one given for nothing
	}
	port := stipendPort(st.HK)
	if p, found, err := s.dero.FindPayment(ctx, false, port, st.DeroHeight); err != nil {
		return err
	} else if found {
		st.Stipend.Tx, st.Stipend.Amount, st.Stipend.Height = p.TxID, p.Amount, p.Height
		s.cfg.Log("bob: the stipend to the taker is in this wallet's history: %d DERO atomic units, tx %s, height %d", p.Amount, p.TxID, p.Height)
		return s.save("watching_xmr")
	}
	if st.Stipend.AttemptHeight > 0 && h < st.Stipend.AttemptHeight+s.cfg.StipendSettleBlocks {
		return nil // an earlier attempt may still be in the pool; after the settle window it is mined or it never will be
	}
	st.Stipend.AttemptHeight = h
	if err := s.save("stipend_sending"); err != nil {
		return err
	}
	txid, err := s.dero.Pay(ctx, st.PeerDero, st.Terms.Stipend, port)
	if txid != "" {
		st.Stipend.Tx = txid
	}
	if err != nil {
		if serr := st.Save(); serr != nil {
			return serr
		}
		return fmt.Errorf("the stipend: %w", err)
	}
	st.Stipend.Amount = st.Terms.Stipend
	s.cfg.Log("bob: sent the taker a stipend of %d DERO atomic units for her gas (Path 0): tx %s", st.Terms.Stipend, txid)
	return s.save("watching_xmr")
}

// stipendArrived is the taker's side of Path 0: no XMR leaves until her own wallet shows the maker's stipend, tagged
// for this swap, at least the agreed amount, in a block DERO can no longer replace — and until she can spend at
// least StipendMin, which is what a Claim and a Punish at their last re-sends may cost her.
func (s *Swap) stipendArrived(ctx context.Context) (bool, error) {
	st := s.st
	wait := func(format string, a ...interface{}) (bool, error) {
		if note := fmt.Sprintf(format, a...); note != s.stipendNote {
			s.stipendNote = note
			s.cfg.Log("alice: %s; no XMR is locked until it is", note)
		}
		return false, nil
	}
	p, found, err := s.dero.FindPayment(ctx, true, stipendPort(st.HK), st.DeroHeight)
	if err != nil {
		return false, err
	}
	if !found {
		return wait("waiting for the maker's stipend of %d DERO atomic units (Path 0)", st.Terms.Stipend)
	}
	if p.Amount < st.Terms.Stipend {
		return wait("the stipend that arrived (tx %s) is %d DERO atomic units, below the %d agreed", p.TxID, p.Amount, st.Terms.Stipend)
	}
	stable, err := s.dero.StableHeight(ctx)
	if err != nil {
		return false, err
	}
	if p.Height > stable {
		return wait("the stipend (tx %s, height %d) is not at stable depth yet (stable height %d)", p.TxID, p.Height, stable)
	}
	have, err := s.dero.Balance(ctx, "")
	if err != nil {
		return false, err
	}
	if have < s.cfg.StipendMin {
		return wait("this wallet can spend %d DERO atomic units, below the %d a Claim and a Punish may cost", have, s.cfg.StipendMin)
	}
	st.Stipend = StipendRecord{Tx: p.TxID, Amount: p.Amount, Height: p.Height}
	s.cfg.Log("alice: the maker's stipend of %d DERO atomic units is at stable depth (tx %s, height %d); this wallet can spend %d", p.Amount, p.TxID, p.Height, have)
	return true, nil
}

// confirmClaim is U3: the taker keeps watching her own Claim until DERO can no longer replace the block it is in.
// A Claim undone by a replaced block and re-mined at or after t1 is refused, the lock opens again with s_a already
// public, and the exits that remain — Bob's refund revealing s_b, or Punish at t2 — need this companion running to
// take them. So "claimed" is said only of a Claim at stable depth.
func (s *Swap) confirmClaim(ctx context.Context) error {
	st := s.st
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	if st.Lock.ClaimSeenHeight == 0 {
		st.Lock.ClaimSeenHeight = h // the Claim's block is at or below the height it is first known at
		return s.save("claim_confirming")
	}
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
	switch l.State {
	case LockClaimed:
		_ = s.queue(KindDone, doneBody{Outcome: "claimed"})
		return s.finish("claimed", fmt.Sprintf("claimed the DERO: tx %s, stable (stable height %d)", st.ClaimTx, stable))
	case LockOpen:
		s.cfg.Log("alice: the claim read at height %d is not on the chain any more: its block was replaced and the lock is open again", st.Lock.ClaimSeenHeight)
		st.Lock.ClaimSeenHeight, st.ClaimTx = 0, ""
		if h+1 < claimCutoff(st.Lock.T1, s.cfg.ClaimMargin) {
			return s.save("claiming")
		}
		s.cfg.Log("alice: past the claim cutoff: not claiming again; waiting for bob's refund, or t2")
		return s.save("await_refund")
	}
	return s.afterLockMoved(ctx, l)
}

// assetName is how an asset is named in what the operator reads.
func assetName(a string) string {
	if a == "" {
		return "DERO"
	}
	return "asset " + a
}

// deadlineOf is t1 − g, or 0 for a t1 inside g.
func deadlineOf(t1, g uint64) uint64 {
	if t1 <= g {
		return 0
	}
	return t1 - g
}

// refundBlind is the Refund a maker who cannot read the contract must still send. The companion reads htlc.bas
// by named keys and walks its ids in windows (getKeys and FindLock in dero.go), so padding the contract with junk
// locks no longer blinds it; but a read can still fail — a node that stops answering, a network that drops — and
// a maker who waits for a readable answer inside [t1, t2) never refunds, after which the taker takes the whole
// locked amount with Punish at t2 whether or not she ever locked any XMR. So a read that fails does not stop the
// refund: it goes out without one, and the contract refuses it harmlessly if the lock has already moved. One
// attempt per eighth of the window, so a read that keeps failing spends a handful of fees rather than one every
// pass.
func (s *Swap) refundBlind(ctx context.Context, readErr error) error {
	st := s.st
	h, herr := s.dero.Height(ctx)
	if herr != nil {
		return readErr
	}
	if h < st.Lock.T1 || h >= st.Lock.T2 {
		return readErr // outside the window the contract refuses it, and the fee would buy nothing
	}
	every := (st.Lock.T2 - st.Lock.T1) / 8
	if every < s.cfg.LockSettleBlocks {
		every = s.cfg.LockSettleBlocks
	}
	if st.Lock.AttemptHeight > 0 && h < st.Lock.AttemptHeight+every {
		return readErr
	}
	st.Lock.AttemptHeight = h
	if !s.blindWarned {
		s.blindWarned = true
		s.cfg.Log("bob: the contract cannot be read (%v); sending Refund without reading it — inside [t1 %d, t2 %d) a refund the contract turns down costs a fee, and a refund never sent costs the whole lock", readErr, st.Lock.T1, st.Lock.T2)
	}
	if st.Phase != "refunding" {
		if err := s.save("refunding"); err != nil {
			return err
		}
	} else if err := st.Save(); err != nil {
		return err
	}
	txid, err := s.dero.Refund(ctx, st.Lock.ID, st.Own.Secret)
	if err != nil {
		return err
	}
	st.RefundTx = txid
	s.cfg.Log("bob: refunded: tx %s", txid)
	_ = s.queue(KindDone, doneBody{Outcome: "refunded"})
	return s.finish("refunded", "refunded: tx "+txid+"; s_b is on the chain so alice can recover any XMR she locked")
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
	// T1Max and T2Max are the ceilings on the same two widths; 0 is no ceiling, for a caller that means to
	// check only the floors.
	T1Max uint64
	T2Max uint64
}

// CheckLock is every §4.3 check, and names the first that fails.
// witnessCheck is MT-B09: before the taker locks XMR, every witness node must show the pinned contract and the lock
// her own node shows, field for field, still open and inside the policy by that node's own height. A witness that
// shows other terms, another contract, or a lock outside the policy is lying or on another chain, and she refuses
// with nothing of hers locked (refusal). One that cannot be reached, or does not show the lock yet, may only be
// behind: she waits (wait), saying so once, and the budget the second sight shrinks on every pass turns a wait that
// never ends into the same refusal.
func (s *Swap) witnessCheck(ctx context.Context, l Lock, e LockExpect) (refusal string, wait bool) {
	for i, w := range s.cfg.Witnesses {
		name := fmt.Sprintf("DERO node %d of %d", i+2, len(s.cfg.Witnesses)+1)
		code, err := w.CodeSHA256(ctx)
		if err != nil {
			return "", s.witnessWait(name + " cannot be read (" + err.Error() + ")")
		}
		if code != HTLCCodeSHA256 {
			return fmt.Sprintf("%s shows contract code %s, not the pinned htlc.bas v2", name, code), false
		}
		wl, err := w.ReadLock(ctx, l.ID)
		if errors.Is(err, ErrNoSuchLock) {
			return "", s.witnessWait(fmt.Sprintf("%s does not show lock %d", name, l.ID))
		}
		if err != nil {
			return "", s.witnessWait(name + " cannot be read (" + err.Error() + ")")
		}
		if d := lockDifference(l, wl); d != "" {
			return fmt.Sprintf("%s shows lock %d with %s", name, l.ID, d), false
		}
		h, err := w.Height(ctx)
		if err != nil {
			return "", s.witnessWait(name + " cannot be read (" + err.Error() + ")")
		}
		if err := CheckLock(wl, h, e); err != nil {
			return fmt.Sprintf("%s, at its height %d: %v", name, h, err), false
		}
	}
	if n := len(s.cfg.Witnesses); n > 0 {
		s.cfg.Log("alice: %d other DERO node(s) show lock %d on the pinned contract, with the same terms, open", n, l.ID)
	}
	return "", false
}

// witnessWait says once why the XMR lock waits on a witness, and reports that it does.
func (s *Swap) witnessWait(why string) bool {
	if why != s.witnessWaiting {
		s.witnessWaiting = why
		s.cfg.Log("alice: not locking XMR yet: %s", why)
	}
	return true
}

// lockDifference names the first field in which two readings of one lock differ, or "" when none does.
func lockDifference(a, b Lock) string {
	for _, f := range []struct {
		differ bool
		name   string
	}{
		{a.Maker != b.Maker, "another maker"},
		{a.Taker != b.Taker, "another taker"},
		{a.HA != b.HA, "another ha"},
		{a.HK != b.HK, "another hk"},
		{a.HB != b.HB, "another hb"},
		{a.Asset != b.Asset, "another asset"},
		{a.Amount != b.Amount, fmt.Sprintf("amount %d, not %d", b.Amount, a.Amount)},
		{a.T1 != b.T1, fmt.Sprintf("t1 %d, not %d", b.T1, a.T1)},
		{a.T2 != b.T2, fmt.Sprintf("t2 %d, not %d", b.T2, a.T2)},
		{a.State != b.State, fmt.Sprintf("state %d, not %d", b.State, a.State)},
	} {
		if f.differ {
			return f.name
		}
	}
	return ""
}

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
	case e.T1Max != 0 && l.T1-now > e.T1Max:
		return fmt.Errorf("t1 − now = %d is above the maximum %d: until t1 the locked XMR has no exit of its own and only the maker decides whether the trade happens", l.T1-now, e.T1Max)
	case e.T2Max != 0 && l.T2-l.T1 > e.T2Max:
		return fmt.Errorf("t2 − t1 = %d is above the maximum %d", l.T2-l.T1, e.T2Max)
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
	return LockExpect{HA: SHA256Hex(own[:]), HK: s.st.HK, HB: s.st.Peer.H, TakerRaw: raw, Amount: s.st.Terms.AmountDero, Asset: s.st.Terms.Asset,
		T1Min: s.cfg.T1Min, T2Min: s.cfg.T2Min, T1Max: s.cfg.T1Max, T2Max: s.cfg.T2Max}, nil
}

func (s *Swap) stepAlice(ctx context.Context) error {
	st := s.st
	switch st.Phase {
	case "":
		// Path 0: the maker's stipend can reach only a registered address, and registration is the one thing on
		// DERO that needs no DERO — the wallet's own proof of work — so it is asked for before anything is said
		reg, err := s.dero.Registered(ctx, s.cfg.MyDero)
		if err != nil {
			return err
		}
		if !reg {
			return fmt.Errorf("the DERO address %s is not registered on this chain: register it (the wallet's own registration costs no DERO) and run again; the maker's stipend cannot reach an unregistered address", s.cfg.MyDero)
		}
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
		h, err := s.ownHeight(ctx)
		if err != nil {
			return err
		}
		dh, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		st.MyXmr, st.MyDero, st.RestoreHeight, st.DeroHeight = a, s.cfg.MyDero, h, dh
		return s.save("setup")

	case "setup":
		var m offerBody
		ok, err := s.recv(ctx, KindOffer, &m, s.cfg.Poll)
		if err != nil || !ok {
			return err
		}
		want := Terms{AmountDero: s.cfg.AmountDero, AmountXmr: s.cfg.AmountXmr, Asset: s.cfg.Asset, Decimals: s.cfg.AssetDecimals, DeroNet: s.cfg.DeroNet, Net: s.cfg.Net.String()}
		got := Terms{AmountDero: m.AmountDero, AmountXmr: m.AmountXmr, Asset: m.Asset, Decimals: m.Decimals, Stipend: m.Stipend, DeroNet: m.DeroNet, Net: m.Net, T1Delta: m.T1Delta, T2Delta: m.T2Delta}
		switch {
		case m.HTLC != s.cfg.HTLC:
			return s.finish("refused", "offer refused: it names contract "+m.HTLC+", mine is "+s.cfg.HTLC)
		case got.DeroNet != want.DeroNet:
			return s.finish("refused", "offer refused: the maker's lock would be on DERO "+got.DeroNet+", and this companion's daemon is on DERO "+want.DeroNet+" (GAP-007)")
		case got.AmountDero != want.AmountDero || got.AmountXmr != want.AmountXmr || got.Asset != want.Asset || got.Net != want.Net:
			return s.finish("refused", fmt.Sprintf("offer refused: %d units of %s for %d piconero on %s, mine is %d of %s for %d on %s", got.AmountDero, assetName(got.Asset), got.AmountXmr, got.Net, want.AmountDero, assetName(want.Asset), want.AmountXmr, want.Net))
		case got.Decimals != want.Decimals:
			return s.finish("refused", fmt.Sprintf("offer refused: the maker scales %s with %d decimals and this companion with %d, so one of the two amounts is not what its holder thinks", assetName(got.Asset), got.Decimals, want.Decimals))
		case got.Stipend < s.cfg.StipendMin:
			return s.finish("refused", fmt.Sprintf("offer refused: a stipend of %d DERO atomic units is below the %d this companion needs to pay for a Claim, and a Punish, without buying DERO from anyone (Path 0)", got.Stipend, s.cfg.StipendMin))
		case got.T1Delta < s.cfg.T1Min || got.T2Delta < s.cfg.T2Min:
			return s.finish("refused", fmt.Sprintf("offer refused: timelock widths %d/%d below the minimums %d/%d", got.T1Delta, got.T2Delta, s.cfg.T1Min, s.cfg.T2Min))
		case got.T1Delta > s.cfg.T1Max || got.T2Delta > s.cfg.T2Max:
			return s.finish("refused", fmt.Sprintf("offer refused: timelock widths %d/%d above the maximums %d/%d; until t1 the XMR she locks has no exit of its own and only the maker decides whether the trade happens", got.T1Delta, got.T2Delta, s.cfg.T1Max, s.cfg.T2Max))
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
			return s.finish("refused", "bob_setup refused: hk is not 64 lower-case hex, so no gate secret could ever match it on chain")
		}
		st.HK = m.HK
		joint, err := s.jointAddress()
		if err != nil {
			return s.finish("refused", "bob_setup refused: "+err.Error())
		}
		if err := s.claimIdentity("joint", joint); err != nil {
			return s.refuseIdentity("bob_setup", err)
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
		// U4: the contract must be the htlc.bas this companion was built for, or nothing it stores means anything
		if ok, err := s.checkCode(ctx); !ok {
			return err
		}
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
		// replay rule: the lock this swap accepts is this swap's; one another swap already accepted is refused
		// here, before any XMR, even though its fresh hashes would already have failed the check above
		if err := s.claimIdentity("lock", s.cfg.HTLC+"#"+strconv.FormatUint(l.ID, 10)); err != nil {
			return s.refuseIdentity("lock", err)
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
		// Path 0: the maker's stipend, stable, and DERO this wallet can spend on a Claim and a Punish — before any
		// XMR moves. Each pass repeats the second sight above, so a stipend that never comes ends in the refusal
		// the shrinking budget gives, with nothing of hers locked.
		if ok, err := s.stipendArrived(ctx); err != nil || !ok {
			return err
		}
		// MT-B09: every other DERO node configured shows the same lock, or no XMR moves
		if refusal, wait := s.witnessCheck(ctx, l, e); refusal != "" {
			return s.finish("refused", "lock refused: "+refusal)
		} else if wait {
			return nil
		}
		s.cfg.Log("alice: locking %d piconero against t1 %d and t2 %d — about %d and %d hours from height %d, and until t1 the XMR has no exit of its own",
			st.Terms.AmountXmr, l.T1, l.T2, (l.T1-h)*18/3600, (l.T2-h)*18/3600, h)
		// U9 (MT-B15), the recovery card: what to keep secret, what to run, and when every window opens and closes, said
		// before the one payment that needs it — and the OS asked to wake this machine before the first window closes
		s.printRecoveryCard(l, h, time.Now())
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
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h >= st.Lock.T1 {
			// past t1 the maker can refund and no Claim of hers would be accepted, so a transfer that has not
			// gone out must not go out now: whatever held it up — a wallet on the wrong network, a balance that
			// does not cover it, a wallet-rpc that was down — it is too late for it. Nothing of hers has moved,
			// so bob's refund ends this as an abort.
			s.cfg.Log("alice: t1 (%d) passed with the Monero lock still unsent: not sending it; waiting for bob's refund", st.Lock.T1)
			return s.save("await_refund")
		}
		if err := s.ownWallet(ctx); err != nil {
			return err // nothing was sent: safe to come back here
		}
		// Invariant 5 across processes, not only across restarts: the durable intent is the one on disk, and a
		// second companion started on this state file may already have moved it on. resumedPhase and
		// transferTried are this process's own memory and say nothing about another's, so the file is read
		// again here, in the last moment before the payment.
		if path := st.Path(); path != "" {
			fresh, err := LoadState(path, st.Role, st.HTLC)
			if err != nil {
				return err
			}
			if fresh.Phase != "xmr_locking" {
				return fmt.Errorf("%s says phase %q, not xmr_locking: another companion is running on this swap, and this one will not pay the joint address a second time", path, fresh.Phase)
			}
		}
		s.transferTried = true
		txid, err := s.xmr.Transfer(ctx, st.Joint, st.Terms.AmountXmr)
		if err != nil {
			// A refusal the wallet makes before it builds anything is not a fate that is unknown: nothing was
			// sent, and recording it as a lock would report XMR that does not exist. Stay here, say why, and
			// leave the transfer still to make.
			var rerr *RPCError
			if errors.As(err, &rerr) && refusedBeforeSending(rerr.Code) {
				s.transferTried = false
				return fmt.Errorf("the Monero lock was refused before any transaction was built (%w): nothing was sent", err)
			}
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
			return s.afterLockMoved(ctx, l)
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		// U2: a gate that reaches her at or after t1 − G is one she does not use. Her Claim carries s_a in the clear
		// into a mempool ordered by fee per byte; sent that late, a maker who can keep it out of the chain until t1
		// reads s_a, sweeps the XMR and refunds. G is how many blocks of suppression that must now cost him.
		if h+s.cfg.GateDeadline >= st.Lock.T1 {
			s.cfg.Log("alice: t1 − G (%d) passed without the gate: waiting for bob's refund, or t2", deadlineOf(st.Lock.T1, s.cfg.GateDeadline))
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
		// measured when it arrived, not when this pass began waiting for it
		if h, err = s.dero.Height(ctx); err != nil {
			delete(st.Consumed, KindGate) // the maker repeats it; the repeat is taken
			return err
		}
		if h+s.cfg.GateDeadline >= st.Lock.T1 {
			s.cfg.Log("alice: the gate reached her at height %d, at or after t1 − G = %d: treated as not received (U2); waiting for bob's refund, or t2", h, deadlineOf(st.Lock.T1, s.cfg.GateDeadline))
			return s.save("await_refund")
		}
		st.Gate = m.KB
		return s.save("gate_received")

	case "gate_received", "claiming":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		if l.State != LockOpen {
			return s.afterLockMoved(ctx, l)
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
		txid, err := s.dero.Claim(ctx, st.Lock.ID, st.Own.Secret, st.Gate, claimCutoff(st.Lock.T1, s.cfg.ClaimMargin))
		if err != nil {
			return err
		}
		st.ClaimTx = txid
		// U3: the Claim executed; it is hers once DERO can no longer replace its block, and she watches until then
		if hc, herr := s.dero.Height(ctx); herr == nil {
			st.Lock.ClaimSeenHeight = hc
		}
		s.cfg.Log("alice: claimed: tx %s; watching it until it is stable", txid)
		return s.save("claim_confirming")

	case "claim_confirming":
		return s.confirmClaim(ctx)

	case "await_refund":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		if l.State != LockOpen {
			return s.afterLockMoved(ctx, l)
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
		if err := s.openJoint(ctx, s.walletName("spend"), spend); err != nil {
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
			return s.afterLockMoved(ctx, l)
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

// refusedBeforeSending are the monero-wallet-rpc codes that mean the wallet turned the request down before it
// built a transaction (src/wallet/wallet_rpc_server_error_codes.h): a destination it cannot parse, no wallet
// open, a watch-only wallet, an amount of zero, or a balance that does not cover it. In every one of them no
// coins moved.
//
// CodeNotSigned is the client's: a wallet that answered with a transaction to sign elsewhere broadcast nothing.
func refusedBeforeSending(code int) bool {
	switch code {
	case -2, -13, -17, -29, -37, -46, -50, CodeNotSigned:
		return true
	}
	return false
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
func (s *Swap) afterLockMoved(ctx context.Context, l Lock) error {
	s.st.LastSent, s.st.LastSentAt = nil, time.Time{}
	switch l.State {
	case LockClaimed:
		// only she can claim, so this is her own Claim from a pass that did not live to record it: it is watched to
		// stable depth like any other (U3)
		if s.st.ClaimTx == "" {
			s.st.ClaimTx = "(claimed; txid not recorded)"
		}
		if s.st.Lock.ClaimSeenHeight == 0 {
			h, err := s.dero.Height(ctx)
			if err != nil {
				return err
			}
			s.st.Lock.ClaimSeenHeight = h
		}
		return s.save("claim_confirming")
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
