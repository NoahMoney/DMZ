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
	// long as he named. T1Max is per confirmation tier (MT-B32, the operator's answer to MT-J05): the tier's quote
	// plus the profile's slack, 480 blocks at the lowest tier, where every trade is while no tier's threshold is set.
	// It was three times the floor, 1,200, before. DefaultT1Max is the lowest tier's; a Config that sets none gets
	// its trade's tier's (withDefaults, TierFor). T2Max stays 1,200.
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
	// suppression that costs him. 100 is the study's value, not a measurement; the operator answered 30 and, asked to
	// re-confirm, confirmed 100 on 2026-09-12 (MT-J04, decision 5). An honest gate arrives near block 150 of the
	// default 420.
	DefaultGateDeadline uint64 = profileGateDeadline
	// DefaultFixedFee is what Refund, Punish and Reclaim are sent at (NewDeroRPC's FixedFee): the daemon's estimator
	// runs a contract at height 0, where all three are refused, so none can be estimated.
	DefaultFixedFee uint64 = 3000
	// DefaultClaimFee is what one htlc.bas Claim is sent at: its storage gas, 304 measured on the DVM
	// (tests/htlc_test.go TestHtlcClaim), plus the 200 exit() adds. v3's Claim is v2's byte for byte, so the figure
	// holds. The wallet pays a fee it is given exactly — walletapi/transaction_build.go computes one only when it is
	// zero.
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
	// DefaultG2Min is the taker's floor on the deployment's g2, the length of her Punish window [t2, t2 + g2)
	// (htlc.bas v3, MT-J13): she refuses an install whose g2 is shorter. It is the profile's g2, 4,800 blocks, as the
	// operator answered on 2026-09-12.
	DefaultG2Min uint64 = profileG2
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
	// Resume is how this companion is started again, as the recovery card prints it and the wake runs it (U9, MT-B15):
	// its executable, its arguments and its directory, which the wake registers as a program with its arguments, never
	// as a line for a shell. Wake, when set, registers that wake with the operating system; nil registers none.
	Resume Resume
	Wake   WakeFunc

	// T1Min, T2Min are policy floors: Alice refuses a lock below them; Bob refuses to choose below them, and below the
	// install's own w1min + DeroTxValidity and w2min, which htlc.bas v3 enforces at the Lock's execution height.
	T1Min, T2Min     uint64
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
	// LockSettleBlocks bounds how far the chain may move between the height Bob records a Lock attempt at, before its
	// transaction is sent, and the height the wallet builds it at: the client sends nothing once the height is this far
	// past the record. So an attempt recorded at a is mined by a + LockSettleBlocks + DeroTxValidity − 1 or never, and no
	// second Lock goes out until that height is stable with no lock of this swap's on the chain (lockWindowEnd).
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
	// G2Min is the taker's floor on the install's g2, her Punish window (MT-J13): she refuses a deployment with less.
	G2Min uint64

	Poll        time.Duration // how long one Step waits on the channel when a message is expected
	ResendAfter time.Duration // how often the last message is repeated while the protocol has not moved on
	Log         func(format string, a ...interface{})
}

// LockReader is what a witness node is asked: the reads the taker's lock check makes, and nothing that sends. A
// DeroRPC with no wallet is one.
type LockReader interface {
	Height(ctx context.Context) (uint64, error)
	ReadLock(ctx context.Context, id uint64) (Lock, error)
	Deployment(ctx context.Context) (Deployment, error)
}

func (c Config) withDefaults() Config {
	def := func(v *uint64, d uint64) {
		if *v == 0 {
			*v = d
		}
	}
	def(&c.T1Min, DefaultT1Min)
	def(&c.T2Min, DefaultT2Min)
	// MT-B32 and the review of 2026-09-12 (finding 6): the trade's confirmation tier sets, together, the Monero depth
	// the gate waits for, the t1 the maker quotes and the ceiling the taker accepts. Only the ceiling followed the tier
	// before, so a trade at a 40-confirmation tier would have been gated at 20 and quoted the lowest tier's t1.
	tier := TierFor(c.AmountXmr)
	def(&c.Confirmations, tier.Confirmations)
	def(&c.GateDeadline, DefaultGateDeadline)
	// the taker sees the lock a couple of blocks after the maker decided t1, and measures t1 − now against the same
	// floor, so the maker adds slack or his own lock is refused; off the simulator the width is also at least what the
	// t1 rule needs at this trade's confirmations and G (T1Quote). The simulator's blocks are neither DERO's 18 s nor
	// Monero's 120 s, so the rule, which counts in them, says nothing there.
	quote := c.T1Min + (profileT1Default - profileT1Min)
	if c.DeroNet != "simulator" {
		if q := T1Quote(c.Confirmations, c.GateDeadline); q > quote {
			quote = q
		}
	}
	def(&c.T1Delta, quote)
	def(&c.T2Delta, c.T2Min+(profileT2MinusT1-profileT2Min))
	def(&c.T1Max, tier.T1Max) // MT-B32: the ceiling of the trade's own confirmation tier
	def(&c.T2Max, DefaultT2Max)
	def(&c.StableBlocks, DefaultStableBlocks)
	def(&c.ClaimMargin, DefaultClaimMargin)
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
	def(&c.G2Min, DefaultG2Min)
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
	// inconsistentWarned: the operator has been told once that the view wallet listed a payment its own chain cannot
	// hold (MT-G05).
	inconsistentWarned bool
	// witnessWaiting is the last reason a witness node gave the taker to wait before her XMR lock, said once (MT-B09).
	witnessWaiting string
	// blindWarned: the operator has been told once that the contract cannot be read and the refund goes out blind.
	blindWarned bool
	// stipendNote is the last reason the operator was given for the XMR lock waiting on the stipend; a reason is
	// said once, and again only when it changes.
	stipendNote string
	// codeChecked: this process has read the contract's code and found it is the htlc.bas it was built for (U4).
	codeChecked bool
	// lockWaitNoted is the Lock attempt the operator was last told is being waited out, said once per attempt.
	lockWaitNoted uint64
	// crash is a test hook: after each durable step it may abandon the call as a process death would.
	crash func(stage string) bool
	// rand is a test hook for the gate secret and the view key: nil is crypto/rand.
	rand io.Reader
}

var errCrashed = errors.New("crashed by the test hook")

// v2Phases are the phases only an htlc.bas v2 companion saved, with what each was. A swap begun on v2 cannot be resumed
// by this companion: its contract's code is v2's, which the command line refuses at start (U4), and the phase has no
// counterpart here, so it is refused by name rather than as an unknown phase. v2 was installed only on the simulator.
var v2Phases = map[string]string{
	"closing": "the maker closing a lock nobody punished with a Punish of his own, LIV-07 under v2; v3 gives such a lock back to him by Reclaim instead",
}

// v2PhaseError is the refusal of a state file in one of v2Phases.
func v2PhaseError(role, phase string) error {
	return fmt.Errorf("%s: this state file is in phase %q, which only an htlc.bas v2 companion saves (%s): this companion runs htlc.bas v3 swaps only, so it cannot resume it; finish that swap with the v2 build that started it (docs/XMR_SWAP_BUILD.md §3.D)", role, phase, v2Phases[phase])
}

func newSwap(role string, cfg Config, st *State, dero Dero, xmr Wallet, prover Prover, ch Channel) (*Swap, error) {
	cfg = cfg.withDefaults()
	if st.Role != role {
		return nil, fmt.Errorf("state is for %s, this is %s", st.Role, role)
	}
	if _, old := v2Phases[st.Phase]; old && !st.Done {
		return nil, v2PhaseError(role, st.Phase)
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
	// The review of 2026-09-12 (finding 6): off the simulator the maker's t1 width must carry the study's t1 rule
	// (docs/ATOMIC_SWAP_STUDY.md §2.1 U7) at his own confirmations and G, or the taker's XMR can reach its depth too late
	// for her to claim. Nothing checked it at runtime before; the profile test checked only the defaults. The taker holds
	// his offer to the same rule at her own view of the tier (stepAlice, setup).
	if role == "bob" && cfg.DeroNet != "simulator" {
		if need := RequiredT1(cfg.Confirmations, cfg.GateDeadline, profileK); float64(cfg.T1Delta) < need {
			return nil, fmt.Errorf("a t1 width of %d is below the %.1f blocks the t1 rule needs at %d Monero confirmations and a gate deadline of %d (docs/ATOMIC_SWAP_STUDY.md §2.1 U7): the taker's XMR could reach its confirmations too late for her to claim; give a t1 width of at least %d, or leave it to the default", cfg.T1Delta, need, cfg.Confirmations, cfg.GateDeadline, T1Quote(cfg.Confirmations, cfg.GateDeadline))
		}
	}
	// MT-B27: his Refund goes out only below refundCutoff(t2) = t2 − DeroTxValidity, so a t2 − t1 of DeroTxValidity or
	// less leaves no height in [t1, t2) at which one can be sent and still execute before t2
	if role == "bob" && cfg.T2Delta <= DeroTxValidity {
		return nil, fmt.Errorf("a t2 − t1 of %d leaves no height at which a Refund can go out and execute before t2: one built at h executes by h + %d, so t2 − t1 must be more than %d", cfg.T2Delta, DeroTxValidity, DeroTxValidity)
	}
	// X-23: the taker holds the maker to the same rule. A lock whose t2 − t1 leaves him no height to refund at leaves
	// her XMR no way back but a Punish or his Reclaim, so her floor on t2 − t1, at the offer and at each sight of the
	// lock, is never below DeroTxValidity + 1, whatever T2Min says
	if role == "alice" && cfg.T2Min <= DeroTxValidity {
		cfg.T2Min = DeroTxValidity + 1
	}
	if role == "alice" && punishCutoff(0, cfg.G2Min, cfg.ClaimMargin) == 0 {
		return nil, fmt.Errorf("a g2 floor of %d leaves no block in which a Punish can go out and be stable before t2 + g2 (it needs more than %d + %d + the claim margin %d)", cfg.G2Min, DeroTxValidity, DefaultStableBlocks, cfg.ClaimMargin)
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

// stopRepeatingGate ends the repeat of k_b when the maker leaves gate_sent for his refund. From t1 htlc.bas refuses her
// Claim, so the gate is of no use to her any more, and repeating it would only put k_b on the channel again while he
// refunds. Without this the repeat went on for as long as ResendAfter, a wall-clock interval, allowed: a swap that
// ended within it (a fast machine) sent the gate once, and one that did not (a loaded machine) sent it again from
// refund_window (X-19). claimSeen ends it the same way on the other exit.
func (s *Swap) stopRepeatingGate() {
	if s.st.LastSent != nil && s.st.LastSent.Kind == KindGate {
		s.st.LastSent, s.st.LastSentAt = nil, time.Time{}
	}
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

// checkDeployment is U4 with MT-A03(a) and MT-J13: before the first thing either party puts into the contract, its
// code must be the htlc.bas this companion was built for and its install arguments inside this party's policy. v3's
// code hashes the same whatever it was installed with, so the three values are read with it, in one DERO.GetSC, and
// kept in the state: the Lock's cutoff, the Punish deadline and the Reclaim height are all counted from them. Read
// once per process; the SCID does not change underneath a swap.
func (s *Swap) checkDeployment(ctx context.Context) (bool, error) {
	if s.codeChecked {
		return true, nil
	}
	d, err := s.dero.Deployment(ctx)
	if err != nil {
		return false, err
	}
	if d.CodeSHA256 != HTLCCodeSHA256 {
		return false, s.finish("refused", "the contract at "+s.cfg.HTLC+" is not htlc.bas v3: its code hashes to "+d.CodeSHA256+", and this companion was built for "+HTLCCodeSHA256+"; nothing of this party's was locked in it")
	}
	if why := s.deploymentRefusal(d); why != "" {
		return false, s.finish("refused", fmt.Sprintf("the htlc.bas v3 at %s was installed with w1min %d, w2min %d and g2 %d: %s; nothing of this party's was locked in it", s.cfg.HTLC, d.W1Min, d.W2Min, d.G2, why))
	}
	s.st.Contract = ContractRecord{W1Min: d.W1Min, W2Min: d.W2Min, G2: d.G2}
	s.codeChecked = true
	return true, nil
}

// deploymentRefusal is this party's policy on an install's arguments: "" inside it, otherwise why not.
//
//   - Both: an install stores all three; one that stores none is not v3's, whatever its code.
//   - Bob: htlc.bas v3 refuses a Lock executing at a height h with t1 < h + w1min or t2 < t1 + w2min (line 26), and a
//     Lock built at the height t1 was decided at executes at most DeroTxValidity − 1 blocks later; the companion allows
//     DeroTxValidity, one block conservative. His agreed widths must
//     therefore be at least max(T1Min, w1min + DeroTxValidity) and w2min, or his Lock is refused on chain and its fee
//     lost; the daemon's estimate, run at height 0, would not say so.
//   - Alice: g2 is her Punish window, and she refuses one shorter than G2Min. w1min and w2min only add refusals to the
//     contract that her own checks of the lock already make; a lock that exists met them.
func (s *Swap) deploymentRefusal(d Deployment) string {
	if d.W1Min == 0 || d.W2Min == 0 || d.G2 == 0 {
		return "it stores no w1min, w2min or g2, as every htlc.bas v3 install does"
	}
	if s.st.Role == "bob" {
		floor := s.cfg.T1Min
		if f := d.W1Min + DeroTxValidity; f > floor {
			floor = f
		}
		if s.st.Terms.T1Delta < floor {
			return fmt.Sprintf("the agreed t1 width %d is below %d, the least that lets a Lock built now execute %d blocks later and still meet w1min (max(T1Min %d, w1min + %d))", s.st.Terms.T1Delta, floor, DeroTxValidity, s.cfg.T1Min, DeroTxValidity)
		}
		if s.st.Terms.T2Delta < d.W2Min {
			return fmt.Sprintf("the agreed t2 − t1 of %d is below w2min, and the contract would refuse the Lock", s.st.Terms.T2Delta)
		}
		return ""
	}
	if d.G2 < s.cfg.G2Min {
		return fmt.Sprintf("g2 %d is below the %d this taker's policy requires: her window to Punish a maker who missed his refund would be shorter than that", d.G2, s.cfg.G2Min)
	}
	return ""
}

// contract is the install this swap runs on, from the state. A state saved before htlc.bas v3 holds none; then it is
// read now, and only a v3 install of the pinned code is taken, since every window after t2 is counted from its g2.
func (s *Swap) contract(ctx context.Context) (ContractRecord, error) {
	if s.st.Contract.G2 != 0 {
		return s.st.Contract, nil
	}
	d, err := s.dero.Deployment(ctx)
	if err != nil {
		return ContractRecord{}, err
	}
	if d.CodeSHA256 != HTLCCodeSHA256 || d.W1Min == 0 || d.W2Min == 0 || d.G2 == 0 {
		return ContractRecord{}, fmt.Errorf("this state records no htlc.bas v3 install, and the contract at %s is not one (code %s, w1min %d, w2min %d, g2 %d): the windows after t2 cannot be counted", s.cfg.HTLC, d.CodeSHA256, d.W1Min, d.W2Min, d.G2)
	}
	s.st.Contract = ContractRecord{W1Min: d.W1Min, W2Min: d.W2Min, G2: d.G2}
	return s.st.Contract, s.st.Save()
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
	seen := map[string]bool{}
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
		// MT-G05: one wallet's answers must hold together. This swap's joint address is fresh, so a payment to it at
		// height 0, or at or below min (the scan this swap asked for starts above min), is not on the chain the wallet
		// reports. Neither is a second entry for a txid already counted: get_transfers lists a transaction once per
		// address. Each is a node, or a wallet, saying something its own chain does not; it is left out, and said once.
		if t.Height == 0 || t.Height <= min || seen[t.TxID] {
			if !s.inconsistentWarned {
				s.inconsistentWarned = true
				s.cfg.Log("bob: the view wallet lists tx %s at the joint address at height %d, which its own chain cannot hold (at height 0, at or below %d where this swap's scan starts, or listed twice): it does not count towards the gate", t.TxID, t.Height, min)
			}
			continue
		}
		seen[t.TxID] = true
		if t.Height > h {
			continue // scanned after the wallet gave its height: it counts on a later pass
		}
		// Confirmations are counted no deeper than the wallet's own heights give. h − t.Height is what
		// monero-wallet-rpc itself reports (its height less the block's), and a node that says more is saying
		// something its own chain does not.
		confs := t.Confirmations
		if d := h - t.Height; d < confs {
			confs = d
		}
		if confs >= s.cfg.Confirmations && t.Unlocked {
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
		// U4, MT-A03(a): the contract must be the htlc.bas this companion was built for, installed with windows his
		// agreed widths can meet, before anything goes into it
		if ok, err := s.checkDeployment(ctx); !ok {
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
		st.Lock = LockRecord{IntentHeight: h, T1: h + st.Terms.T1Delta, T2: h + st.Terms.T1Delta + st.Terms.T2Delta, Amount: st.Terms.AmountDero, AttemptsRecorded: true}
		// U9 for the maker (MT-B28): what to keep secret, what to run, and every window of his as a clock time, said before
		// the one payment that needs them — and the OS asked to wake this machine before t1, when his Refund window opens,
		// and before t2 + g2, when nobody having punished, only his Reclaim returns his DERO and the taker's XMR
		s.printMakerCard(st.Lock.T1, st.Lock.T2, h, time.Now())
		return s.save("dero_locking")

	case "dero_locking":
		// htlc.bas takes a second Lock under the same hashes as a second lock, for the same taker, and the one s_a and
		// the one k_b open both: she claims both, or anyone Punishes the one the maker does not know of to her, and he
		// loses a whole AmountDero. A Lock already sent may be in a mempool — this node's or another's — where DERO.GetSC,
		// and so every lookup, cannot see it; derod can drop it from its own pool while another node still mines it; and
		// the process that sent it can die while it waits. So every attempt is on disk before its transaction leaves,
		// and no second one goes out while DERO could still mine the first: until every block it could land in is stable
		// with no lock of this swap's on the chain (lockWindowEnd).
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		c, err := s.contract(ctx)
		if err != nil {
			return err
		}
		cutoff := lockCutoff(st.Lock.T1, c.W1Min)
		if !st.Lock.AttemptsRecorded {
			// A state an earlier build saved in this phase. That build recorded an attempt only after a Lock call returned
			// an error, so one may have gone out at any height from the intent until now, or until the cutoff, past which
			// none was sent: that height is taken as the attempt's, and its window is waited out like any other.
			a := h
			if cutoff > 0 && a >= cutoff {
				a = cutoff - 1
			}
			if a < st.Lock.IntentHeight {
				a = st.Lock.IntentHeight
			}
			if a > st.Lock.AttemptHeight {
				st.Lock.AttemptHeight = a
			}
			st.Lock.AttemptsRecorded = true
			if err := st.Save(); err != nil {
				return err
			}
		}
		if a := st.Lock.AttemptHeight; a > 0 {
			// an earlier attempt: the chain is only looked at until DERO can no longer mine it. A cutoff of 0 is the
			// lookup alone, which sends nothing whatever the height (Dero.Lock)
			_, id, err := s.dero.Lock(ctx, st.Peer.H, st.HK, st.Own.H, st.PeerDero, st.Lock.T1, st.Lock.T2, st.Terms.Asset, st.Lock.Amount, 0)
			if err == nil {
				return s.lockLanded(id, st.Lock.Tx)
			}
			if !errors.Is(err, ErrLockTooLate) {
				return err
			}
			stable, err := s.dero.StableHeight(ctx)
			if err != nil {
				return err
			}
			end := s.lockWindowEnd(a)
			if stable < end {
				if s.lockWaitNoted != a {
					s.lockWaitNoted = a
					s.cfg.Log("bob: the Lock attempted at height %d is not on the chain, and DERO could still mine it until height %d: no other Lock goes out until that height is stable (stable height %d)", a, end, stable)
				}
				return nil
			}
			s.cfg.Log("bob: the Lock attempted at height %d never landed: every block it could have been mined in, up to %d, is stable and holds no lock of this swap's", a, end)
		}
		if h >= cutoff {
			// MT-A03(a): no lock of this swap's is on the chain, none can still land, and one built now could execute less
			// than w1min before t1, where htlc.bas v3 refuses it and keeps the fee
			return s.finish("refused", fmt.Sprintf("the Lock could no longer be built in time: from height %d a Lock with t1 %d could execute less than the contract's w1min %d before t1, and htlc.bas v3 would refuse it on chain; nothing was locked", cutoff, st.Lock.T1, c.W1Min))
		}
		// The attempt is on disk before the Lock leaves, and the client sends it only while the height is within
		// LockSettleBlocks of the record, so the window counted from the record covers the height it is built at.
		prev := st.Lock.AttemptHeight
		st.Lock.AttemptHeight, st.Lock.Tx = h, ""
		if err := st.Save(); err != nil {
			st.Lock.AttemptHeight = prev
			return err
		}
		if s.crash != nil && s.crash("lock_attempt") {
			return errCrashed // the test hook: killed with the attempt recorded and nothing sent
		}
		sendBy := cutoff
		if lim := h + s.cfg.LockSettleBlocks; lim < sendBy {
			sendBy = lim
		}
		txid, id, err := s.dero.Lock(ctx, st.Peer.H, st.HK, st.Own.H, st.PeerDero, st.Lock.T1, st.Lock.T2, st.Terms.Asset, st.Lock.Amount, sendBy)
		if lockNotSent(err) {
			// nothing went out: the attempt recorded for this call is withdrawn, so no pass waits on a transaction that
			// does not exist (the earlier one it replaced, if any, had already been waited out)
			st.Lock.AttemptHeight = prev
			if serr := st.Save(); serr != nil {
				return serr
			}
			if errors.Is(err, ErrLockTooLate) {
				return nil // the next pass holds the height against the cutoff
			}
			return err
		}
		if s.crash != nil && s.crash("lock_sent") {
			return errCrashed // the test hook: killed after the Lock went out and before its result was recorded
		}
		if err != nil {
			// the transaction may be in a mempool: its attempt is on disk already, and the next pass waits it out
			if txid != "" {
				st.Lock.Tx = txid
				if serr := st.Save(); serr != nil {
					return serr
				}
			}
			return err
		}
		return s.lockLanded(id, txid)

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
				s.stopRepeatingGate()
				return s.save("refund_window")
			}
			return nil
		}
		s.stopRepeatingGate()
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
			// only this party can Refund: his own, from a pass that did not live to record it, or whose call returned an
			// error after the transaction went out — watched to stable depth like the one recorded (MT-B27)
			return s.refundSeen(ctx)
		case LockPunished:
			return s.finish("punished", "punished: alice took the DERO after t2; the refund window was missed")
		case LockReclaimed:
			// only this party can Reclaim: his own, sent by another process or by hand, watched to stable depth
			return s.reclaimSeen(ctx)
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h < st.Lock.T1 {
			return nil
		}
		// MT-B27: a Refund built at h executes by h + DeroTxValidity, and one executing at or after t2 is refused, which
		// leaves the lock open to Punish; from the cutoff none is sent, first time or again
		cutoff := refundCutoff(st.Lock.T2)
		if h >= cutoff {
			return s.pastRefundCutoff(ctx, h, cutoff)
		}
		// Invariant 5: the intent is durable before every send, a second one after a replaced block included
		if st.Phase != "refunding" {
			if err := s.save("refunding"); err != nil {
				return err
			}
		}
		txid, err := s.dero.Refund(ctx, st.Lock.ID, st.Own.Secret, cutoff)
		if err != nil {
			return err
		}
		return s.refundSent(ctx, txid, cutoff)

	case "punish_window", "reclaiming":
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		switch l.State {
		case LockPunished:
			return s.finish("punished", "punished: the DERO went to alice in [t2, t2 + g2); the refund window was missed")
		case LockRefunded:
			// a Refund of his sent before the cutoff, landing (or mined again after a replaced block) before t2: watched
			// to stable depth like any other (MT-B27)
			return s.refundSeen(ctx)
		case LockReclaimed:
			// his own Reclaim, from a pass that did not live to record it (or whose call returned an error after the
			// transaction went out): watched to stable depth like any other
			return s.reclaimSeen(ctx)
		case LockClaimed:
			return s.claimSeen(ctx, l)
		}
		// MT-J13 (htlc.bas v3): past t2 an open lock has two exits left, one after the other. In [t2, t2 + g2) only
		// Punish, which pays alice: the maker loses the lock only to a timely one, and his companion sends none — under
		// v2 it closed the lock this way itself (LIV-07), because the DERO was lost to him at t2 either way; under v3 it
		// is not, and a Punish of his own would pay it away for nothing. From t2 + g2 the lock is his to Reclaim with
		// s_b, which the contract stores as Refund does, so alice recovers any XMR she locked once he sends it; from then
		// the lock, and her XMR with it, waits only on him, which is why this companion sends it unattended and watches it
		// to stable depth. Invariant 9 holds for Reclaim as for Refund: the lock is open, so s_a has not been read.
		c, err := s.contract(ctx)
		if err != nil {
			return err
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if h < st.Lock.T2+c.G2 {
			return nil
		}
		// A process that dies after the Reclaim went out and before the next save, or whose call returns an error after
		// the transaction was sent, finds the lock still open here while that transaction is in the mempool, and sends a
		// second: one executes, the other is mined and refused, and its fee (FixedFee) is the whole cost
		// (docs/XMR_SWAP_BUILD.md §3.D).
		if st.Phase != "reclaiming" {
			if err := s.save("reclaiming"); err != nil {
				return err
			}
		}
		txid, err := s.dero.Reclaim(ctx, st.Lock.ID, st.Own.Secret)
		if err != nil {
			return err
		}
		st.ReclaimTx = txid
		// U3 for the maker's Reclaim: it is his once DERO can no longer replace its block, and he watches until then
		if hr, herr := s.dero.Height(ctx); herr == nil {
			st.Lock.ReclaimSeenHeight = hr
		}
		s.cfg.Log("bob: reclaimed the lock nobody punished: tx %s at height %d (t2 + g2 = %d); watching it until it is stable", txid, h, st.Lock.T2+c.G2)
		return s.save("reclaim_confirming")

	case "reclaim_confirming":
		return s.confirmReclaim(ctx)

	case "refund_confirming":
		return s.confirmRefund(ctx)
	}
	if _, old := v2Phases[st.Phase]; old {
		return v2PhaseError("bob", st.Phase)
	}
	return fmt.Errorf("bob: unknown phase %q", st.Phase)
}

// lockNotSent says whether an error from Dero.Lock means no transaction left: the cutoff refused it, or it failed before
// the wallet was handed one.
func lockNotSent(err error) bool {
	return errors.Is(err, ErrLockTooLate) || errors.Is(err, ErrLockNotSent)
}

// lockWindowEnd is the last height, counted one block conservative as every cutoff here is, at which a Lock attempt
// recorded at a could be mined. The client sends it only while the height is below a + LockSettleBlocks, so it is built
// at or below that, and DERO mines a transaction within DeroTxValidity − 1 blocks of the height it was built at or never
// (blockchain/miner_block.go). Once this height is stable with no lock of this swap's on the chain, the attempt never
// landed and never will.
func (s *Swap) lockWindowEnd(a uint64) uint64 { return a + s.cfg.LockSettleBlocks + DeroTxValidity }

// lockLanded records this swap's Lock, sent now or found on the chain, and tells the taker.
func (s *Swap) lockLanded(id uint64, txid string) error {
	st := s.st
	st.Lock.ID, st.Lock.Tx = id, txid
	s.cfg.Log("bob: DERO locked: id %d tx %s t1 %d t2 %d amount %d", id, txid, st.Lock.T1, st.Lock.T2, st.Lock.Amount)
	if err := s.queue(KindDeroLocked, deroLockedBody{ID: id, Tx: txid}); err != nil {
		return err
	}
	return s.save("dero_locked")
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
// pass. None goes out from the refund cutoff (MT-B27), where it could execute only at or after t2.
func (s *Swap) refundBlind(ctx context.Context, readErr error) error {
	st := s.st
	h, herr := s.dero.Height(ctx)
	if herr != nil {
		return readErr
	}
	cutoff := refundCutoff(st.Lock.T2)
	if h < st.Lock.T1 || h >= cutoff {
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
	txid, err := s.dero.Refund(ctx, st.Lock.ID, st.Own.Secret, cutoff)
	if err != nil {
		return err
	}
	return s.refundSent(ctx, txid, cutoff)
}

// refundSent records a Refund the client saw execute and moves to watching it (MT-B27): it is his once DERO can no
// longer replace its block. The height it is read at bounds that block from above.
func (s *Swap) refundSent(ctx context.Context, txid string, cutoff uint64) error {
	st := s.st
	st.RefundTx = txid
	if hr, herr := s.dero.Height(ctx); herr == nil {
		st.Lock.RefundSeenHeight = hr
	}
	s.cfg.Log("bob: refunded: tx %s, read at height %d (refund cutoff %d, t2 %d); watching it until it is stable", txid, st.Lock.RefundSeenHeight, cutoff, st.Lock.T2)
	return s.save("refund_confirming")
}

// pastRefundCutoff is the maker at or past his refund cutoff with the lock still open (MT-B27). No Refund of his can
// execute before t2 any more, so none is sent, and the operator is told plainly what that leaves him: from t2 until
// t2 + g2 anyone may Punish the lock, which pays the taker his DERO and returns him nothing; if nobody does, his Reclaim
// from t2 + g2 returns it, and puts s_b on the chain for her. punish_window waits for whichever the chain shows, and
// sends that Reclaim.
func (s *Swap) pastRefundCutoff(ctx context.Context, h, cutoff uint64) error {
	st := s.st
	c, err := s.contract(ctx)
	if err != nil {
		return err
	}
	s.cfg.Log("bob: height %d is at or past the refund cutoff %d (t2 %d − %d) with the lock open: no Refund is sent, since one could execute only at or after t2, where htlc.bas refuses it and keeps the fee. From t2 until t2 + g2 = %d anyone may Punish the lock, which pays alice the DERO and returns this party nothing; if nobody does, this party Reclaims it from t2 + g2, which returns the DERO and puts s_b on the chain for alice", h, cutoff, st.Lock.T2, DeroTxValidity, st.Lock.T2+c.G2)
	return s.save("punish_window")
}

// refundSeen is Bob's move on reading st == 2 from a phase that did not record the Refund as sent: only the maker may
// Refund, so it is his own — from a pass that died after it went out, a call that returned an error after the
// transaction was sent, or one a replaced block undid and the chain then mined again before t2. It is watched to stable
// depth from the height it was first read at (confirmRefund).
func (s *Swap) refundSeen(ctx context.Context) error {
	if s.st.Lock.RefundSeenHeight == 0 {
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		s.st.Lock.RefundSeenHeight = h // the Refund's block is at or below the height it is first known at
	}
	if s.st.RefundTx == "" {
		s.st.RefundTx = "(refunded; txid not recorded)"
	}
	return s.save("refund_confirming")
}

// confirmRefund is U3's rule for the maker's Refund (MT-B27), as confirmReclaim is for his Reclaim: he counts it as done
// only once DERO can no longer replace the block it is in. A Refund undone by a replaced block and mined again at or
// after t2 is refused, and the lock is then open to Punish, which pays the taker. Unlike the other confirmers it reads
// the lock on every pass, not only once the height it was read at is stable: the Refund's own window closes at its
// cutoff, t2 − DeroTxValidity, so one undone is sent again at once while the cutoff allows, rather than found up to 8
// blocks later, when it may no longer be.
func (s *Swap) confirmRefund(ctx context.Context) error {
	st := s.st
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	if st.Lock.RefundSeenHeight == 0 {
		st.Lock.RefundSeenHeight = h
		return st.Save()
	}
	l, err := s.dero.ReadLock(ctx, st.Lock.ID)
	if err != nil {
		return err
	}
	switch l.State {
	case LockRefunded:
		stable, err := s.dero.StableHeight(ctx)
		if err != nil {
			return err
		}
		if stable < st.Lock.RefundSeenHeight {
			return nil
		}
		_ = s.queue(KindDone, doneBody{Outcome: "refunded"})
		return s.finish("refunded", fmt.Sprintf("refunded: tx %s, stable (stable height %d); s_b is on the chain so alice can recover any XMR she locked", st.RefundTx, stable))
	case LockOpen:
		if cutoff := refundCutoff(st.Lock.T2); h < cutoff {
			s.cfg.Log("bob: the refund read at height %d is not on the chain any more: its block was replaced and the lock is open again; at height %d, before the refund cutoff %d, the Refund goes out again", st.Lock.RefundSeenHeight, h, cutoff)
		} else {
			s.cfg.Log("bob: the refund read at height %d is not on the chain any more: its block was replaced and the lock is open again, and at height %d, at or past the refund cutoff %d, no Refund can execute before t2", st.Lock.RefundSeenHeight, h, cutoff)
		}
	}
	// open again, or — a replaced block having let another exit in first — moved some other way: refund_window sends the
	// Refund again while the cutoff allows, routes the state the chain now shows, or, past the cutoff with the lock open,
	// says what that leaves this party and waits for Punish or t2 + g2
	st.Lock.RefundSeenHeight, st.RefundTx = 0, ""
	return s.save("refund_window")
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
		d, err := w.Deployment(ctx)
		if err != nil {
			return "", s.witnessWait(name + " cannot be read (" + err.Error() + ")")
		}
		if d.CodeSHA256 != HTLCCodeSHA256 {
			return fmt.Sprintf("%s shows contract code %s, not the pinned htlc.bas v3", name, d.CodeSHA256), false
		}
		// the install arguments her own node showed, and her policy took, must be every node's (htlc.bas v3's code is
		// the same for every install, so the code alone does not say which one this is)
		if got := (ContractRecord{W1Min: d.W1Min, W2Min: d.W2Min, G2: d.G2}); got != s.st.Contract {
			return fmt.Sprintf("%s shows the contract installed with w1min %d, w2min %d, g2 %d, not the %d, %d, %d her own node shows", name, got.W1Min, got.W2Min, got.G2, s.st.Contract.W1Min, s.st.Contract.W2Min, s.st.Contract.G2), false
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
		case got.T2Delta <= DeroTxValidity:
			// X-23, MT-B27: the maker's Refund goes out only below t2 − DeroTxValidity, so this window leaves him no height
			// to send one at. His own companion refuses to offer it; hers refuses it from a maker whose companion does not
			return s.finish("refused", fmt.Sprintf("offer refused: a t2 − t1 of %d leaves the maker no height at which a Refund can go out and execute before t2 (one built at h executes by h + %d, MT-B27), so the XMR she locks could come back only through a Punish or his Reclaim", got.T2Delta, DeroTxValidity))
		case got.T1Delta < s.cfg.T1Min || got.T2Delta < s.cfg.T2Min:
			return s.finish("refused", fmt.Sprintf("offer refused: timelock widths %d/%d below the minimums %d/%d", got.T1Delta, got.T2Delta, s.cfg.T1Min, s.cfg.T2Min))
		case got.T1Delta > s.cfg.T1Max || got.T2Delta > s.cfg.T2Max:
			return s.finish("refused", fmt.Sprintf("offer refused: timelock widths %d/%d above the maximums %d/%d; until t1 the XMR she locks has no exit of its own and only the maker decides whether the trade happens", got.T1Delta, got.T2Delta, s.cfg.T1Max, s.cfg.T2Max))
		case s.cfg.DeroNet != "simulator" && float64(got.T1Delta) < RequiredT1(s.cfg.Confirmations, s.cfg.GateDeadline, profileK):
			// the review of 2026-09-12 (finding 6): a t1 the rule does not cover at this trade's tier is one in which her
			// XMR may reach the maker's depth, and the gate her, too late to claim
			return s.finish("refused", fmt.Sprintf("offer refused: a t1 width of %d is below the %.1f blocks the t1 rule needs at this trade's %d Monero confirmations and a gate deadline of %d (docs/ATOMIC_SWAP_STUDY.md §2.1 U7): the XMR she locks could reach the maker's depth too late for her to claim", got.T1Delta, RequiredT1(s.cfg.Confirmations, s.cfg.GateDeadline, profileK), s.cfg.Confirmations, s.cfg.GateDeadline))
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
		// U4, MT-J13: the contract must be the htlc.bas this companion was built for, or nothing it stores means
		// anything, and its g2 at least her policy's, or her Punish window is shorter than she agreed to
		if ok, err := s.checkDeployment(ctx); !ok {
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
		// MT-J13 (htlc.bas v3): Punish has a deadline, t2 + g2. She sends it at once from t2, and none from the cutoff,
		// after which one could execute, or be stable, only at or past t2 + g2, where the contract refuses it and keeps
		// the fee. A restart that finds the lock open past the cutoff sends nothing: the lock is bob's to Reclaim from
		// t2 + g2, and a Reclaim of his puts s_b on the chain, from which she recovers any XMR she locked. Nothing makes
		// him send one: without it her XMR and his DERO stay locked for ever, which is v3's cost to her, and the reason
		// her recovery card asks for a Punish in time, from her or from anyone she trusts to send it.
		c, err := s.contract(ctx)
		if err != nil {
			return err
		}
		cutoff := punishCutoff(st.Lock.T2, c.G2, s.cfg.ClaimMargin)
		if h >= cutoff {
			s.cfg.Log("alice: height %d is past the punish cutoff %d (t2 %d + g2 %d − %d validity − %d stable − %d margin): not punishing; waiting for bob's Reclaim, which puts s_b on the chain (if he never sends one, the XMR locked here and his DERO stay locked for ever)", h, cutoff, st.Lock.T2, c.G2, DeroTxValidity, DefaultStableBlocks, s.cfg.ClaimMargin)
			return s.save("await_reclaim")
		}
		// A process that dies after the Punish went out and before the next save, or whose call returns an error after
		// the transaction was sent, finds the lock still open here while that transaction is in the mempool, and sends a
		// second: one executes, the other is mined and refused, and its fee (FixedFee) is the whole cost. Waiting for the
		// first to settle instead, as the Lock does after a restart, would spend blocks of a window with a deadline
		// (docs/XMR_SWAP_BUILD.md §3.D).
		if st.Phase != "punishing" {
			if err := s.save("punishing"); err != nil {
				return err
			}
		}
		txid, err := s.dero.Punish(ctx, st.Lock.ID, cutoff)
		if err != nil {
			return err
		}
		st.PunishTx = txid
		if hp, herr := s.dero.Height(ctx); herr == nil {
			st.Lock.PunishSeenHeight = hp
		}
		s.cfg.Log("alice: punished: tx %s; watching it until it is stable", txid)
		return s.save("punish_confirming")

	case "punish_confirming":
		return s.confirmPunish(ctx)

	case "await_reclaim":
		// past her Punish cutoff with the lock open: the lock is bob's to Reclaim from t2 + g2, with no deadline, and a
		// Punish someone else sent in time still pays her. Either way the chain says which, and afterLockMoved acts on it
		l, err := s.dero.ReadLock(ctx, st.Lock.ID)
		if err != nil {
			return err
		}
		if l.State != LockOpen {
			return s.afterLockMoved(ctx, l)
		}
		return nil
	}
	return fmt.Errorf("alice: unknown phase %q", st.Phase)
}

// confirmPunish is U3's rule for the taker's Punish (htlc.bas v3): it counts as done only once DERO can no longer
// replace the block it is in. A Punish undone by a replaced block and re-mined at or after t2 + g2 is refused, and the
// lock is then bob's to Reclaim; a Punish whose block was replaced before the cutoff is sent again.
func (s *Swap) confirmPunish(ctx context.Context) error {
	st := s.st
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	if st.Lock.PunishSeenHeight == 0 {
		st.Lock.PunishSeenHeight = h // the Punish's block is at or below the height it is first known at
		return st.Save()
	}
	stable, err := s.dero.StableHeight(ctx)
	if err != nil {
		return err
	}
	if stable < st.Lock.PunishSeenHeight {
		return nil
	}
	l, err := s.dero.ReadLock(ctx, st.Lock.ID)
	if err != nil {
		return err
	}
	switch l.State {
	case LockPunished:
		_ = s.queue(KindDone, doneBody{Outcome: "punished"})
		how := "the DERO was paid to this party by a Punish in [t2, t2 + g2)"
		if st.PunishTx != "" {
			how = "took the DERO, tx " + st.PunishTx
		}
		return s.finish("punished", fmt.Sprintf("punished: %s, stable (stable height %d); any XMR locked stays under the joint key", how, stable))
	case LockOpen:
		s.cfg.Log("alice: the punish read at height %d is not on the chain any more: its block was replaced and the lock is open again", st.Lock.PunishSeenHeight)
		st.Lock.PunishSeenHeight, st.PunishTx = 0, ""
		return s.save("punish_window")
	}
	return s.afterLockMoved(ctx, l)
}

// reclaimSeen is Bob's move on reading st == 4 from a phase that did not record sending the Reclaim: only the maker may
// Reclaim, so it is his own, from a pass that died after it went out, or whose call returned an error after the
// transaction was sent. It is watched to stable depth from the height it was first read at (confirmReclaim).
func (s *Swap) reclaimSeen(ctx context.Context) error {
	if s.st.Lock.ReclaimSeenHeight == 0 {
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		s.st.Lock.ReclaimSeenHeight = h // the Reclaim's block is at or below the height it is first known at
	}
	if s.st.ReclaimTx == "" {
		s.st.ReclaimTx = "(reclaimed; txid not recorded)"
	}
	return s.save("reclaim_confirming")
}

// confirmReclaim is U3's rule for the maker's Reclaim, as confirmPunish is for the taker's Punish: he counts it as done
// only once DERO can no longer replace the block it is in. A Reclaim undone by a replaced block and not mined again
// leaves the lock open, and from t2 + g2 nobody but him can move it — Punish has closed, and Reclaim is the maker's — so
// a companion that had already said "reclaimed" would leave his DERO in the contract, and a taker who had not yet read
// s_b waiting on him, until someone ran it again. It sends the Reclaim again instead.
func (s *Swap) confirmReclaim(ctx context.Context) error {
	st := s.st
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	if st.Lock.ReclaimSeenHeight == 0 {
		st.Lock.ReclaimSeenHeight = h
		return st.Save()
	}
	stable, err := s.dero.StableHeight(ctx)
	if err != nil {
		return err
	}
	if stable < st.Lock.ReclaimSeenHeight {
		return nil
	}
	l, err := s.dero.ReadLock(ctx, st.Lock.ID)
	if err != nil {
		return err
	}
	switch l.State {
	case LockReclaimed:
		_ = s.queue(KindDone, doneBody{Outcome: "reclaimed"})
		return s.finish("reclaimed", fmt.Sprintf("reclaimed: tx %s after t2 + g2, stable (stable height %d); s_b is on the chain so alice can recover any XMR she locked", st.ReclaimTx, stable))
	case LockOpen:
		s.cfg.Log("bob: the reclaim read at height %d is not on the chain any more: its block was replaced and the lock is open again, and only this party can move it now; sending the Reclaim again", st.Lock.ReclaimSeenHeight)
	}
	// open again, or — a replaced block having let another exit in first — moved some other way: the window's own
	// phase sends the Reclaim again, or routes the state the chain now shows
	st.Lock.ReclaimSeenHeight, st.ReclaimTx = 0, ""
	return s.save("punish_window")
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
	case LockRefunded, LockReclaimed:
		// htlc.bas v3's Reclaim stores s_b in xb_<id> exactly as Refund does, with the same check (sha256(xb) == hb):
		// for her, st 4 is st 2, and her XMR comes back with no deadline
		how := "refunded"
		if l.State == LockReclaimed {
			how = "reclaimed the lock after t2 + g2"
		}
		if err := s.checkRevealed(l.XB); err != nil {
			return fmt.Errorf("%s lock: %w", lockStateName(l.State), err)
		}
		s.cfg.Log("alice: bob %s; s_b read from the chain", how)
		if s.st.Xmr.LockTx == "" && !s.st.Xmr.LockUnknown {
			return s.finish("aborted", "bob "+how+" before any XMR was locked: nothing to recover")
		}
		return s.save("refund_seen")
	case LockPunished:
		// whoever sent it, it paid her; it is watched to stable depth as her own Punish is (confirmPunish)
		if s.st.Lock.PunishSeenHeight == 0 {
			h, err := s.dero.Height(ctx)
			if err != nil {
				return err
			}
			s.st.Lock.PunishSeenHeight = h
		}
		return s.save("punish_confirming")
	}
	return nil
}
