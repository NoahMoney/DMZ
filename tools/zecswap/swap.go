package zecswap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
)

// The two state machines of docs/ZEC_SWAP.md §3, one per party, each run by that party on their own machine
// against their own nodes. Neither talks to the other: everything one needs is on the two chains — the offer and
// the accept on the board, the lock in the escrow, the funding and its spend on Zcash — so there is no channel, no
// relay and nothing to host.
//
// Every step reads what it acts on from the chain itself, checks it against what this party agreed to, and writes
// its intent before acting. The irreversible actions, and the durable write before each:
//
//	maker: Post (hash and secret on disk) · Lock (T_dero on disk) · the Zcash claim (the funding bound to this
//	       swap in the consumed-funding register, the claim's bytes on disk) · the taker's DERO Claim, sent for her
//	       once the Zcash claim is deep · Refund
//	taker: Accept (cltv on disk) · the funding (its bytes, and three pre-signed refunds, on disk and in the refund
//	       file) · the DERO Claim (the secret on disk) · the refund (on disk since the funding)
//
// Before either party's first call, the board's and the escrow's code must hash to the pins this build carries, on
// every DERO node (pin.go). The two steps each side's safety rests on are gated on every configured node agreeing
// (quorum.go): the maker's claim, which reveals the secret, and the taker's funding. Step advances a machine by at
// most one phase and may be called again after any error and after any restart.

// Config is one companion's parameters.
type Config struct {
	Policy Policy
	Board  string // board.bas scid
	Escrow string // swap.bas scid
	MyDero string // this party's DERO address, read from its wallet by the caller
	// DeroMainnet is the DERO network the wallet and daemon are on (the caller checked the two agree).
	DeroMainnet bool
	// Key loads the one Zcash key this swap spends with — the maker's claim key (its P2PKH address is the offer's
	// wantAddr), the taker's funding and refund key — for one use; the machine zeroes it straight after.
	Key func(net Network) (*Key, error)
	// Payout is the transparent P2PKH address the claim (maker) or the refund (taker) pays; "" is the key's own.
	Payout string
	// Maker, new offer: the asset and the two amounts.
	GiveAsset  string
	GiveAmount uint64
	WantAmount uint64
	// Maker: adopt an offer already on the board (HaveOffer), with the secret that was generated for it.
	// Taker: the offer to take (HaveOffer must be set).
	HaveOffer bool
	OfferID   uint64
	Secret    string
	// RefundFile is where the taker's pre-signed refund is written; "" is beside the state file.
	RefundFile string
	// ZcashName and DeroName name the primary nodes in logs; the witnesses are further nodes, each a trusted party
	// of its own, which must agree with the primary before the gated steps (quorum.go).
	ZcashName      string
	DeroName       string
	ZcashWitnesses []ZcashSource
	DeroWitnesses  []DeroSource
	// ZcashChain, when set, is the chain the Zcash node must report ("main", "test" or "regtest", as ChainInfo
	// names it): a node on another is refused before anything is read from it.
	ZcashChain string
	// TxVersion is the version of the Zcash transactions this party builds: 5 (also when 0) or 6 (ZIP 229).
	TxVersion uint32
	// Headers turns on the header gate (spv.go, -verify-headers); nil is off
	Headers *HeaderCheck
	Log     func(format string, a ...interface{})
}

// Swap is one party's machine.
type Swap struct {
	cfg  Config
	st   *State
	dero Dero
	zc   Zcash
	zsrc []ZcashSource // the primary first, then the witnesses
	dsrc []DeroSource
	used *UsedHashes
	// consumed binds every funding this maker credits to its swap (FundingKey); the taker has no use for it
	consumed *ConsumedFundings
	crash    func(phase string) bool // test hook: a process death right after a durable write
	rand     io.Reader               // test hook: the secret's source; nil is crypto/rand
	said     map[string]bool
	spvOK    string // the funding, block and tip the header gate last verified, so one pass does not read them twice
}

var errCrashed = errors.New("crashed by the test hook")

// NewMaker is the party with the DERO-side asset.
func NewMaker(cfg Config, st *State, dero Dero, zc Zcash) (*Swap, error) {
	return newSwap(RoleMaker, cfg, st, dero, zc)
}

// NewTaker is the party with ZEC.
func NewTaker(cfg Config, st *State, dero Dero, zc Zcash) (*Swap, error) {
	return newSwap(RoleTaker, cfg, st, dero, zc)
}

func newSwap(role string, cfg Config, st *State, dero Dero, zc Zcash) (*Swap, error) {
	if cfg.Policy == (Policy{}) {
		cfg.Policy = DefaultPolicy()
	}
	if err := cfg.Policy.Validate(); err != nil {
		return nil, err
	}
	if st.Role != role {
		return nil, fmt.Errorf("zecswap: the state is the %s's, this is the %s", st.Role, role)
	}
	if !isHex(cfg.Board, 32) || !isHex(cfg.Escrow, 32) {
		return nil, errors.New("zecswap: the board and escrow scids must be 64 hex characters")
	}
	if _, err := DeroRaw(cfg.MyDero); err != nil {
		return nil, err
	}
	if cfg.Key == nil {
		return nil, errors.New("zecswap: no Zcash key")
	}
	for _, c := range []struct{ name, have, want string }{{"DERO address", st.MyDero, cfg.MyDero}, {"board", st.Board, cfg.Board}, {"escrow contract", st.Escrow, cfg.Escrow}} {
		if c.have != "" && c.have != c.want {
			return nil, fmt.Errorf("zecswap: this state belongs to %s %s, not %s: its ids name that one's rows", c.name, c.have, c.want)
		}
	}
	if st.Phase != "" && st.DeroMainnet != cfg.DeroMainnet {
		return nil, errors.New("zecswap: this state belongs to the other DERO network")
	}
	if role == RoleTaker && !cfg.HaveOffer && st.Phase == "" {
		return nil, errors.New("zecswap: the taker needs the offer id")
	}
	if _, err := txVersion(cfg.TxVersion); err != nil {
		return nil, err
	}
	if cfg.Log == nil {
		cfg.Log = log.Printf
	}
	zname, dname := cfg.ZcashName, cfg.DeroName
	if zname == "" {
		zname = "zcash"
	}
	if dname == "" {
		dname = "dero"
	}
	s := &Swap{cfg: cfg, st: st, dero: dero, zc: zc, said: map[string]bool{}}
	s.zsrc = append([]ZcashSource{{Name: zname, Node: zc}}, cfg.ZcashWitnesses...)
	s.dsrc = append([]DeroSource{{Name: dname, Node: dero}}, cfg.DeroWitnesses...)
	seen := map[string]bool{}
	for _, n := range s.zsrc {
		if n.Name == "" || seen["z"+n.Name] || n.Node == nil {
			return nil, fmt.Errorf("zecswap: every Zcash node needs its own name (%q)", n.Name)
		}
		seen["z"+n.Name] = true
	}
	for _, n := range s.dsrc {
		if n.Name == "" || seen["d"+n.Name] || n.Node == nil {
			return nil, fmt.Errorf("zecswap: every DERO node needs its own name (%q)", n.Name)
		}
		seen["d"+n.Name] = true
	}
	if role == RoleMaker {
		u, err := LoadUsed(UsedPath(st.Path()))
		if err != nil {
			return nil, err
		}
		c, err := LoadConsumed(ConsumedPath(st.Path()))
		if err != nil {
			return nil, err
		}
		s.used, s.consumed = u, c
	}
	return s, nil
}

// Phase is the machine's current phase; State is its whole memory.
func (s *Swap) Phase() string { return s.st.Phase }
func (s *Swap) State() *State { return s.st }

// Step runs one phase. A waiting phase returns (false, nil) when nothing has changed.
func (s *Swap) Step(ctx context.Context) (bool, error) {
	if s.st.Done {
		return true, nil
	}
	var err error
	if s.st.Role == RoleMaker {
		err = s.stepMaker(ctx)
	} else {
		err = s.stepTaker(ctx)
	}
	return s.st.Done, err
}

// ---- durable moves ----

// save moves to phase — only along the table — and writes the whole state before anything acts on it.
func (s *Swap) save(phase string) error {
	from := s.st.Phase
	if !CanMove(s.st.Role, from, phase) {
		return fmt.Errorf("zecswap: the %s cannot move from %q to %q", s.st.Role, from, phase)
	}
	if Phases(s.st.Role)[phase].Terminal && !s.st.Done {
		return fmt.Errorf("zecswap: %q is an end and is entered only with its outcome", phase)
	}
	s.st.Phase = phase
	if err := s.st.Save(); err != nil {
		s.st.Phase = from
		return err
	}
	if phase != from {
		s.logf("%s -> %s", from, phase)
	}
	if s.crash != nil && s.crash(phase) {
		return errCrashed
	}
	return nil
}

// finish ends the swap in a terminal phase with an outcome a person can read.
func (s *Swap) finish(phase, outcome string) error {
	if !Phases(s.st.Role)[phase].Terminal {
		return fmt.Errorf("zecswap: %q is not an end", phase)
	}
	if !CanMove(s.st.Role, s.st.Phase, phase) {
		return fmt.Errorf("zecswap: the %s cannot end from %q in %q", s.st.Role, s.st.Phase, phase)
	}
	s.st.Done, s.st.Outcome = true, outcome
	if err := s.save(phase); err != nil {
		if !errors.Is(err, errCrashed) {
			s.st.Done, s.st.Outcome = false, ""
		}
		return err
	}
	s.logf("done: %s", outcome)
	return nil
}

func (s *Swap) logf(format string, a ...interface{}) {
	s.cfg.Log("%s: "+format, append([]interface{}{s.st.Role}, a...)...)
}

// sayOnce logs a waiting message once per process.
func (s *Swap) sayOnce(key, format string, a ...interface{}) {
	if !s.said[key] {
		s.said[key] = true
		s.logf(format, a...)
	}
}

// onceLogf is logf for a line that would otherwise come pass after pass: each distinct line is said once per process.
func (s *Swap) onceLogf(format string, a ...interface{}) {
	line := fmt.Sprintf(format, a...)
	s.sayOnce("line|"+line, "%s", line)
}

func (s *Swap) attempt(name string) uint64 { return s.st.Attempts[name] }

// failedCall records a DERO call whose outcome is unknown — it may be in the pool — so the next pass waits its
// settle window (settleWindow) before looking for its effect. A call refused at estimate sent nothing and is not
// recorded.
func (s *Swap) failedCall(name string, h uint64, err error) error {
	if errors.Is(err, ErrEstimate) {
		return err
	}
	if s.st.Attempts == nil {
		s.st.Attempts = map[string]uint64{}
	}
	s.st.Attempts[name] = h
	if serr := s.st.Save(); serr != nil {
		return serr
	}
	return err
}

func (s *Swap) settling(name string, h uint64) bool {
	a := s.attempt(name)
	return a > 0 && h < a+s.settleWindow(name)
}

// settleWindow is how many blocks an unseen call is given to land before its effect is looked for and it may go
// again. A second copy of most calls is refused by its contract and costs a fee. A second Lock is not: it is a second
// escrow under the same hash, for the same taker, which the one secret opens as well. So a Lock is given the whole
// span in which DERO could still mine it, DeroTxValidity blocks from the height it was sent at.
func (s *Swap) settleWindow(name string) uint64 {
	w := s.cfg.Policy.SettleBlocks
	if name == "lock" && w < DeroTxValidity {
		w = DeroTxValidity
	}
	return w
}

func (s *Swap) noteTx(name, txid string) {
	if s.st.Attempts != nil {
		delete(s.st.Attempts, name)
	}
	if txid == "" {
		return
	}
	if s.st.DeroTxs == nil {
		s.st.DeroTxs = map[string]string{}
	}
	s.st.DeroTxs[name] = txid
}

// ---- reading the Zcash chain ----

// chain reads the primary node, binds the swap to its chain on the first read, and refuses a node on another
// chain or one whose height fell further than a reorganisation can explain since this swap last read it. The
// second value is the cautious tip — the highest any node that answers reports — used only to schedule: the
// refund goes out, and the maker stops claiming, by the earliest clock among the nodes.
func (s *Swap) chain(ctx context.Context) (ChainInfo, uint32, error) {
	ci, err := s.zc.ChainInfo(ctx)
	if err != nil {
		return ci, 0, err
	}
	if s.cfg.ZcashChain != "" && ci.Chain != s.cfg.ZcashChain {
		return ci, 0, fmt.Errorf("zecswap: this companion is configured for Zcash %s and the node is on %s: refusing to read it", s.cfg.ZcashChain, ci.Chain)
	}
	switch {
	case s.st.ZcashChain == "":
		s.st.ZcashChain = ci.Chain
	case s.st.ZcashChain != ci.Chain:
		return ci, 0, fmt.Errorf("zecswap: this swap lives on Zcash %s and the node is on %s: refusing to read it", s.st.ZcashChain, ci.Chain)
	}
	if s.st.MaxZecTip > ReorgLimit && ci.Height < s.st.MaxZecTip-ReorgLimit {
		return ci, 0, fmt.Errorf("zecswap: the node is at height %d and this swap has seen %d: no reorganisation explains it", ci.Height, s.st.MaxZecTip)
	}
	if ci.Height > s.st.MaxZecTip {
		s.st.MaxZecTip = ci.Height
	}
	cautious := ci.Height
	for _, w := range s.zsrc[1:] {
		wi, err := w.Node.ChainInfo(ctx)
		if err != nil || wi.Chain != ci.Chain {
			s.sayOnce("wz"+w.Name, "Zcash witness %s cannot be read (%v, chain %q): it is not consulted for scheduling, and the gated steps wait for it", w.Name, err, wi.Chain)
			continue
		}
		if wi.Height > cautious {
			cautious = wi.Height
		}
	}
	return ci, cautious, nil
}

func (s *Swap) net() Network {
	if s.st.ZcashChain == "main" {
		return Mainnet
	}
	return Testnet
}

// signingBranch is the branch id a transaction sent now must be signed under: the node's consensus.nextblock,
// accepted only when this build knows it, it agrees with the schedule — this build's on mainnet and testnet, the
// node's own on regtest, which only a node with regtest's genesis block reaches — and every Zcash node reports the
// same one.
func (s *Swap) signingBranch(ctx context.Context, ci ChainInfo) (uint32, error) {
	if ci.Regtest {
		if err := CheckBranchIDOnSchedule(ci.Upgrades, ci.Height+1, ci.NextBranch); err != nil {
			return 0, err
		}
	} else if err := CheckBranchID(ci.Net, ci.Height+1, ci.NextBranch); err != nil {
		return 0, err
	}
	for _, w := range s.zsrc[1:] {
		wi, err := w.Node.ChainInfo(ctx)
		if err != nil {
			return 0, fmt.Errorf("zecswap: witness %s: %w", w.Name, err)
		}
		if wi.Chain != ci.Chain || wi.NextBranch != ci.NextBranch {
			return 0, fmt.Errorf("zecswap: witness %s says chain %s branch %08x, the primary chain %s branch %08x: not signing", w.Name, wi.Chain, wi.NextBranch, ci.Chain, ci.NextBranch)
		}
	}
	return ci.NextBranch, nil
}

func (s *Swap) htlc() (HTLC, error) {
	var h HTLC
	var err error
	if h.Hash, err = ParseSwapHash(s.st.Hash); err != nil {
		return h, err
	}
	if err := decodeHexInto(h.ClaimPkh[:], s.st.ClaimPkh); err != nil {
		return h, fmt.Errorf("claim key hash: %w", err)
	}
	if err := decodeHexInto(h.RefundPkh[:], s.st.RefundPkh); err != nil {
		return h, fmt.Errorf("refund key hash: %w", err)
	}
	h.CLTV = s.st.CLTV
	return h, CheckCLTVHeight(h.CLTV)
}

// withKey loads the key for one use and zeroes it after.
func (s *Swap) withKey(fn func(k *Key) error) error {
	k, err := s.cfg.Key(s.net())
	if err != nil {
		return fmt.Errorf("the Zcash key: %w", err)
	}
	defer k.Zero()
	return fn(k)
}

// payoutFor is the key hash a spend pays: the configured address, or the key's own.
func (s *Swap) payoutFor(k *Key) ([20]byte, error) {
	if s.cfg.Payout == "" {
		return k.PubKeyHash(), nil
	}
	net, kind, h, err := ParseAddress(s.cfg.Payout)
	if err != nil {
		return h, fmt.Errorf("payout address: %w", err)
	}
	if net != s.net() || kind != P2PKH {
		return h, fmt.Errorf("the payout address %s is not a %s P2PKH address", s.cfg.Payout, s.net())
	}
	return h, nil
}

// lockDeadline is the DERO height by which the maker must have locked: P_lock after he first saw the accept, or
// board.bas v3's recorded acceptance deadline ad_<i> if that is earlier — the board's is the one the taker holds
// him to, and he may have seen the accept late.
func lockDeadline(acceptHeight uint64, p Policy, o Offer) uint64 {
	d := acceptHeight + p.PLock
	if o.AcceptDeadline != 0 && o.AcceptDeadline < d {
		d = o.AcceptDeadline
	}
	return d
}

// lockOpen says whether the maker may still send his Lock at DERO height h. It executes by h + DeroTxValidity, and
// the taker funds nothing against a lock she first reads at or after the deadline, so it goes out only while it would
// execute with SettleBlocks to spare for her to read it.
func lockOpen(h, deadline uint64, p Policy) bool {
	return h+DeroTxValidity+p.SettleBlocks < deadline
}

// takerLockDeadline is the height from which the taker walks. board.bas v3 stamps ad_<i> from the height her Accept
// executes at, up to DeroTxValidity blocks after the height she sent it at (AcceptHeight), and the maker counts P_lock
// from when he first reads it, so she counts P_lock from the last height her Accept could have executed at. Counted
// from the height she sent it at, she would walk while the maker could still lock in time.
func takerLockDeadline(acceptHeight uint64, p Policy, o Offer) uint64 {
	return lockDeadline(acceptHeight+DeroTxValidity, p, o)
}

// ---- the maker ----

func (s *Swap) stepMaker(ctx context.Context) error {
	st, p := s.st, s.cfg.Policy
	switch st.Phase {
	case "":
		return s.makerSetup(ctx)

	case "posting":
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if s.settling("post", h) {
			return nil
		}
		// nothing is on the board as far as this companion has seen: an expiry that ran out while it was stopped
		// may still move (Post is found by its hash, not its expiry). board.bas refuses an expiry at or below the
		// height the Post executes at, which may be DeroTxValidity blocks from now.
		if st.OfferExpiry <= h+DeroTxValidity {
			st.OfferExpiry = h + p.OfferLife
		}
		txid, id, err := s.dero.Post(ctx, PostArgs{Chain: ZcashChainLabel, GiveAsset: st.GiveAsset, GiveAmount: st.GiveAmount, WantAmount: st.WantAmount, WantAddr: st.WantAddr, Hash: st.Hash, Expiry: st.OfferExpiry})
		if err != nil {
			return s.failedCall("post", h, err)
		}
		st.OfferID, st.HaveOffer = id, true
		s.noteTx("post", txid)
		if o, err := s.dero.ReadOffer(ctx, id); err == nil {
			st.OfferExpiry = o.Expiry
		}
		s.logf("offer %d posted under hash %s: %d of %q for %d zatoshi to %s", id, st.Hash, st.GiveAmount, st.GiveAsset, st.WantAmount, st.WantAddr)
		return s.save("posted")

	case "posted":
		o, err := s.dero.ReadOffer(ctx, st.OfferID)
		if err != nil {
			return err
		}
		if o.Maker != st.MakerRaw || o.Hash != st.Hash {
			return fmt.Errorf("zecswap: board row %d is not this swap's offer", st.OfferID)
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		switch o.State {
		case OfferCancelled:
			return s.finish("cancelled", "the offer was cancelled before anyone accepted it; nothing was locked")
		case OfferOpen:
			if h >= o.Expiry {
				return s.finish("expired", "the offer expired with no accept; nothing was locked")
			}
			return nil
		}
		if st.AcceptHeight == 0 {
			st.AcceptHeight, st.TakerRaw = h, o.Taker
			if err := st.Save(); err != nil {
				return err
			}
			s.logf("offer %d accepted by %s at DERO height %d", st.OfferID, o.Taker, h)
		}
		decline := func(why string) error {
			st.Note = why
			s.logf("not locking: %s; the offer will be cancelled at its deadline", why)
			return s.save("cancel_wait")
		}
		cltv, rp, err := checkAccept(o)
		if err != nil {
			return decline(err.Error())
		}
		_, cautious, err := s.chain(ctx)
		if err != nil {
			return err
		}
		if err := p.CheckCLTV(cltv, cautious); err != nil {
			return decline(err.Error())
		}
		deadline := lockDeadline(st.AcceptHeight, p, o)
		if !lockOpen(h, deadline, p) {
			return decline(fmt.Sprintf("DERO height %d is too close to the lock deadline %d: a Lock sent now may execute up to %d blocks later, and the taker funds nothing against a lock mined at or after the deadline", h, deadline, DeroTxValidity-1))
		}
		st.CLTV, st.RefundPkh, st.TakerRaw, st.TDero, st.LockDeadline = cltv, rp, o.Taker, h+p.TDeroWindow, deadline
		return s.save("locking")

	case "locking":
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if s.settling("lock", h) {
			return nil
		}
		if s.attempt("lock") > 0 {
			if e, ok, err := s.dero.FindEscrow(ctx, st.MakerRaw, st.TakerRaw, st.Hash); err != nil {
				return err
			} else if ok {
				return s.locked(e, "")
			}
		}
		deadline := st.LockDeadline
		if deadline == 0 {
			deadline = st.AcceptHeight + p.PLock
		}
		if !lockOpen(h, deadline, p) {
			st.Note = "the lock did not land before the lock deadline"
			return s.save("cancel_wait")
		}
		taker, err := DeroAddress(st.TakerRaw, st.DeroMainnet)
		if err != nil {
			return err
		}
		// swap.bas v2's Claim pays the named taker, and a transfer to an unregistered address is refused and the whole
		// Claim reverted: a lock for her would be one nobody can claim, which only T_dero ends
		if ok, err := s.dero.Registered(ctx, taker); err != nil {
			return err
		} else if !ok {
			st.Note = "the taker's DERO address " + taker + " is not registered on the chain, so no Claim could ever pay her"
			s.logf("not locking: %s", st.Note)
			return s.save("cancel_wait")
		}
		// T_dero: the escrow refuses an expiry below height + wmin at the height the Lock EXECUTES, which DERO allows up
		// to DeroTxValidity blocks after the height it is built at, now. So T_dero is the wider of this policy's window
		// and wmin + DeroTxValidity above now (TDeroWidth), written down before the call; a later attempt sets it again
		// (the Lock is found by maker, taker and hash, never expiry).
		wmin, err := s.dero.EscrowWMin(ctx)
		if err != nil {
			return err
		}
		if wmin > p.TDeroWindow {
			st.Note = fmt.Sprintf("the escrow's wmin %d is wider than this maker's T_dero window %d", wmin, p.TDeroWindow)
			return s.save("cancel_wait")
		}
		width, err := p.TDeroWidth(wmin)
		if err != nil {
			st.Note = err.Error()
			return s.save("cancel_wait")
		}
		st.TDero = h + width
		// The attempt is on disk before the Lock leaves. A companion that dies while its Lock waits in the pool finds it
		// on restart and gives that Lock its whole span to land (settleWindow) before it looks for it, instead of sending
		// a second, which swap.bas would take as a second escrow.
		if st.Attempts == nil {
			st.Attempts = map[string]uint64{}
		}
		st.Attempts["lock"] = h
		if err := st.Save(); err != nil {
			return err
		}
		txid, id, err := s.dero.Lock(ctx, st.Hash, taker, st.TDero, st.GiveAsset, st.GiveAmount)
		if errors.Is(err, ErrEstimate) {
			// refused at estimate: nothing was sent, so there is nothing to wait for
			delete(st.Attempts, "lock")
			if serr := st.Save(); serr != nil {
				return serr
			}
			return err
		}
		if err != nil {
			return s.failedCall("lock", h, err)
		}
		e, err := s.dero.ReadEscrow(ctx, id)
		if err != nil {
			return err
		}
		return s.locked(e, txid)

	case "locked":
		return s.makerWatchFunding(ctx)

	case "claiming":
		return s.sendClaim(ctx)

	case "claimed":
		return s.makerClaimed(ctx)

	case "refund_wait", "dero_refunding":
		e, err := EscrowAgreed(ctx, s.dsrc, st.EscrowID)
		if err != nil {
			return err
		}
		switch e.State {
		case EscrowClaimed:
			return s.finish("swapped", "the taker's DERO Claim landed")
		case EscrowRefunded:
			return s.finish(s.refundOutcome())
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		// no execution-delay margin: swap.bas refuses a Refund only before T_dero, so executing later than it was
		// sent can only help it
		if h < st.TDero {
			return nil
		}
		if st.Phase != "dero_refunding" {
			return s.save("dero_refunding")
		}
		if s.settling("refund", h) {
			return nil
		}
		txid, err := s.dero.Refund(ctx, st.EscrowID)
		if err != nil {
			return s.failedCall("refund", h, err)
		}
		s.noteTx("refund", txid)
		if err := st.Save(); err != nil {
			return err
		}
		// the end is declared on the next pass, once every DERO node shows the escrow refunded
		return nil

	case "cancel_wait":
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		// a Lock sent before the move here may have landed since, or may still: DERO mines it up to DeroTxValidity
		// blocks after it was sent. Its DERO then needs this companion's Refund, so nothing ends before that span has.
		if !st.HaveEscrow && s.attempt("lock") > 0 && st.TakerRaw != "" {
			if e, ok, err := s.dero.FindEscrow(ctx, st.MakerRaw, st.TakerRaw, st.Hash); err != nil {
				return err
			} else if ok {
				st.EscrowID, st.HaveEscrow, st.TDero = e.ID, true, e.Expiry
				s.logf("a lock sent earlier landed as escrow %d: it will be refunded after T_dero %d", e.ID, e.Expiry)
				return s.save("refund_wait")
			}
			if s.settling("lock", h) {
				return nil
			}
		}
		o, err := s.dero.ReadOffer(ctx, st.OfferID)
		if err != nil {
			return err
		}
		if o.State == OfferCancelled {
			return s.finish("cancelled", "the offer is cancelled: "+st.Note)
		}
		// board.bas v3 lets the maker cancel an accepted offer from its acceptance deadline or its expiry
		if o.State == OfferAccepted && h < o.Expiry && (o.AcceptDeadline == 0 || h < o.AcceptDeadline) {
			return nil
		}
		if s.settling("cancel", h) {
			return nil
		}
		txid, err := s.dero.Cancel(ctx, st.OfferID)
		if err != nil {
			return s.failedCall("cancel", h, err)
		}
		s.noteTx("cancel", txid)
		return s.finish("cancelled", "the offer is cancelled: "+st.Note)
	}
	return fmt.Errorf("zecswap: the maker has no phase %q", st.Phase)
}

// checkAccept reads the taker's two numbers off the board and refuses what the board itself does not: a cltv
// that is not a Zcash height (board.bas v2 took any Uint64 but zero; v3 refuses Unix times, and this holds either
// way), a refund key hash that is not 20 bytes of lower-case hex.
func checkAccept(o Offer) (uint32, string, error) {
	if o.CLTV >= LockTimeThreshold {
		return 0, "", fmt.Errorf("the accept's cltv %d is not a Zcash height", o.CLTV)
	}
	cltv := uint32(o.CLTV)
	if err := CheckCLTVHeight(cltv); err != nil {
		return 0, "", err
	}
	if len(o.RefundPkh) != 40 || strings.ToLower(o.RefundPkh) != o.RefundPkh || !isHex(o.RefundPkh, 20) {
		return 0, "", fmt.Errorf("the accept's refund key hash %q is not 40 lower-case hex characters", o.RefundPkh)
	}
	if !isHex(o.Taker, 33) {
		return 0, "", errors.New("the accept names no taker")
	}
	return cltv, o.RefundPkh, nil
}

func (s *Swap) makerSetup(ctx context.Context) error {
	st, p, cfg := s.st, s.cfg.Policy, s.cfg
	me, err := DeroRaw(cfg.MyDero)
	if err != nil {
		return err
	}
	// GAP-011: the board and the escrow must be the contracts this build was made for, on every DERO node
	if err := CheckPins(ctx, s.dsrc, cfg.Board, cfg.Escrow); err != nil {
		if errors.Is(err, ErrCodeNotPinned) {
			return s.finish("refused", "setup refused: "+err.Error()+"; nothing was posted or locked")
		}
		return err
	}
	if err := CheckEscrowWidth(ctx, s.dsrc, 1, p.TDeroWindow); err != nil {
		if errors.Is(err, ErrEscrowWidth) {
			return s.finish("refused", "setup refused: "+err.Error()+"; nothing was posted or locked")
		}
		return err
	}
	ci, _, err := s.chain(ctx)
	if err != nil {
		return err
	}
	var claimPkh, payout [20]byte
	if err := s.withKey(func(k *Key) error {
		claimPkh = k.PubKeyHash()
		payout, err = s.payoutFor(k)
		return err
	}); err != nil {
		return err
	}
	st.Board, st.Escrow, st.MyDero, st.MyRaw, st.MakerRaw, st.DeroMainnet = cfg.Board, cfg.Escrow, cfg.MyDero, me, me, cfg.DeroMainnet
	st.ClaimPkh, st.Payout = hex.EncodeToString(claimPkh[:]), hex.EncodeToString(payout[:])
	st.WantAddr = EncodeAddress(ci.Net, P2PKH, claimPkh)
	if st.SwapID == "" {
		var id [8]byte
		if _, err := io.ReadFull(s.random(), id[:]); err != nil {
			return err
		}
		st.SwapID = hex.EncodeToString(id[:])
	}
	refuse := func(why string) error { return s.finish("refused", "setup refused: "+why) }

	if cfg.HaveOffer {
		secret, err := ParseSecret(cfg.Secret)
		if err != nil {
			return refuse(err.Error())
		}
		sum := sha256.Sum256(secret[:])
		hash := hex.EncodeToString(sum[:])
		o, err := s.dero.ReadOffer(ctx, cfg.OfferID)
		if err != nil {
			return err
		}
		_, kind, pkh, aerr := ParseAddress(o.WantAddr)
		switch {
		case o.Maker != me:
			return refuse(fmt.Sprintf("offer %d was not posted by this wallet", o.ID))
		case o.Hash != hash:
			return refuse(fmt.Sprintf("offer %d's hash is not SHA256 of the secret given", o.ID))
		case o.Chain != ZcashChainLabel:
			return refuse(fmt.Sprintf("offer %d is for chain %q", o.ID, o.Chain))
		case aerr != nil || kind != P2PKH || pkh != claimPkh:
			return refuse(fmt.Sprintf("offer %d pays %q, which is not the key's address %s", o.ID, o.WantAddr, st.WantAddr))
		case o.State == OfferCancelled:
			return refuse(fmt.Sprintf("offer %d is cancelled", o.ID))
		case o.WantAmount < p.MinAmount || o.WantAmount > MaxMoney:
			return refuse(fmt.Sprintf("offer %d wants %d zatoshi, outside [%d, %d]", o.ID, o.WantAmount, p.MinAmount, uint64(MaxMoney)))
		}
		if err := s.used.Record(hash, st.SwapID); err != nil {
			if errors.Is(err, ErrHashReused) {
				return refuse(err.Error())
			}
			return err
		}
		st.HaveOffer, st.OfferID, st.Hash, st.Secret = true, o.ID, hash, cfg.Secret
		st.GiveAsset, st.GiveAmount, st.WantAmount, st.WantAddr, st.OfferExpiry = o.GiveAsset, o.GiveAmount, o.WantAmount, o.WantAddr, o.Expiry
		return s.save("posted")
	}

	switch {
	case cfg.GiveAmount == 0:
		return refuse("nothing to give")
	case cfg.WantAmount < p.MinAmount || cfg.WantAmount > MaxMoney:
		return refuse(fmt.Sprintf("%d zatoshi is outside [%d, %d]: below the minimum the claim fee is too large a part of the swap (§9 Z10)", cfg.WantAmount, p.MinAmount, uint64(MaxMoney)))
	case cfg.GiveAsset != "" && (strings.ToLower(cfg.GiveAsset) != cfg.GiveAsset || !isHex(cfg.GiveAsset, 32)):
		return refuse("the asset must be empty (DERO) or a 64 lower-case hex SCID")
	}
	// A fresh secret of exactly 32 bytes: swap.bas Claim takes nothing else, and the script says SIZE 32.
	var secret [SecretBytes]byte
	if _, err := io.ReadFull(s.random(), secret[:]); err != nil {
		return err
	}
	sum := sha256.Sum256(secret[:])
	hash := hex.EncodeToString(sum[:])
	if err := s.used.Record(hash, st.SwapID); err != nil {
		if errors.Is(err, ErrHashReused) {
			return refuse(err.Error() + "; the random source is repeating itself")
		}
		return err
	}
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	st.Secret, st.Hash = hex.EncodeToString(secret[:]), hash
	st.GiveAsset, st.GiveAmount, st.WantAmount, st.OfferExpiry = cfg.GiveAsset, cfg.GiveAmount, cfg.WantAmount, h+p.OfferLife
	return s.save("posting")
}

func (s *Swap) random() io.Reader {
	if s.rand != nil {
		return s.rand
	}
	return rand.Reader
}

func (s *Swap) locked(e Escrow, txid string) error {
	st := s.st
	if e.Maker != st.MakerRaw || e.Taker != st.TakerRaw || e.Hash != st.Hash || e.State != EscrowOpen {
		return fmt.Errorf("zecswap: escrow %d is not this swap's lock", e.ID)
	}
	st.EscrowID, st.HaveEscrow, st.TDero = e.ID, true, e.Expiry
	s.noteTx("lock", txid)
	s.logf("locked %d of %q in escrow %d for the taker, until DERO height %d", e.Amount, st.GiveAsset, e.ID, e.Expiry)
	return s.save("locked")
}

// makerWatchFunding is §3 step 5: the script's address is watched, and the funding is claimed only when it is at
// least wantAmount, bound to no other swap, at least C_zec deep on a block that is still the node's, confirmed by
// every Zcash node, and the cutoff has not passed.
func (s *Swap) makerWatchFunding(ctx context.Context) error {
	st, p := s.st, s.cfg.Policy
	ci, cautious, err := s.chain(ctx)
	if err != nil {
		return err
	}
	// A funding read here below C_zec that the chain has since dropped is said, with its block, before anything
	// else: it counts for nothing, and the depth of whatever funding is found next starts over on the chain as it is.
	if st.Sighted != nil {
		if _, err := Depth(ctx, s.zc, *st.Sighted, ci.Height); errors.Is(err, ErrReorged) {
			s.logf("REORGANISATION: the funding %s:%d read at height %d in block %s is off the node's best chain (%v); it counts for nothing, and the secret stays here until a funding is %d deep on the chain as it now is", st.Sighted.TxID, st.Sighted.Index, st.Sighted.Height, st.Sighted.BlockHash, err, p.ConfZec)
			st.Sighted = nil
			if err := st.Save(); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	if !p.ClaimOpen(cautious, st.CLTV) {
		st.Note = fmt.Sprintf("no funding of %d zatoshi was %d blocks deep before the claim cutoff (Zcash height %d, cltv %d)", st.WantAmount, p.ConfZec, cautious, st.CLTV)
		s.logf("%s: the secret is never revealed; the DERO is refunded after T_dero %d", st.Note, st.TDero)
		return s.save("refund_wait")
	}
	h, err := s.htlc()
	if err != nil {
		return err
	}
	us, err := ScriptOutputs(ctx, s.zc, h, s.net())
	if err != nil {
		return err
	}
	var best *UTXO
	for i := range us {
		u := &us[i]
		if u.Value < st.WantAmount {
			continue
		}
		// a funding already credited to another swap is never this one's
		owner, taken, err := s.consumed.Owner(FundingKey(st.ZcashChain, u.TxID, u.Index))
		if err != nil {
			return err
		}
		if taken && owner.Swap != st.SwapID {
			s.sayOnce("taken"+u.TxID, "the output %s:%d is already credited to swap %s (offer %d): not this swap's funding", u.TxID, u.Index, owner.Swap, owner.Offer)
			continue
		}
		if best == nil || u.Height < best.Height || (u.Height == best.Height && u.TxID < best.TxID) {
			best = u
		}
	}
	if best == nil {
		s.sayOnce("nofunding", "watching %s for %d zatoshi (claim cutoff at Zcash height %d)", h.Address(s.net()), st.WantAmount, st.CLTV-p.ClaimCutoff-1)
		return nil
	}
	f, err := SeeFunding(ctx, s.zc, h, *best)
	if errors.Is(err, ErrReorged) {
		s.logf("the funding moved while it was read: %v", err)
		return nil
	}
	if err != nil {
		return err
	}
	d, err := Depth(ctx, s.zc, f, ci.Height)
	if err != nil {
		return err
	}
	if d < p.ConfZec {
		s.sayOnce("shallow"+f.TxID+f.BlockHash, "funding %s:%d of %d zatoshi seen at depth %d in block %s; claiming at depth %d", f.TxID, f.Index, f.Value, d, f.BlockHash, p.ConfZec)
		if st.Sighted == nil || *st.Sighted != f {
			st.Sighted = &f
			return st.Save()
		}
		return nil
	}
	// the gate: every Zcash node must show this exact funding before the secret leaves this machine
	if err := ConfirmFunding(ctx, s.zsrc, st.ZcashChain, h, f, p.ConfZec, s.logf); err != nil {
		return err
	}
	// with -verify-headers the funding's block and every header above it must carry real work before the secret
	// leaves (spv.go); a failure is logged and tried again next pass, and the claim cutoff still ends the attempt
	if err := s.headerGateFunding(ctx, ci, f); err != nil {
		s.onceLogf("not claiming: %v", err)
		return nil
	}
	branch, err := s.signingBranch(ctx, ci)
	if err != nil {
		return err
	}
	// The funding's identity — its outpoint on this network — is bound to this swap before the claim exists, so
	// no other swap, and no second run of this one under another swap id, can ever credit it again.
	if err := s.consumed.Record(FundingKey(st.ZcashChain, f.TxID, f.Index), st.SwapID, st.Board, st.OfferID); err != nil {
		return err
	}
	claim, err := s.buildClaim(f, h, branch)
	if err != nil {
		return err
	}
	// the funding's own bytes are kept: a reorganisation that drops it is answered by sending them again
	frt, err := s.zc.RawTx(ctx, f.TxID)
	if err != nil {
		return err
	}
	st.Funding, st.Sighted, st.FundingHex = &f, nil, hex.EncodeToString(frt.Raw)
	st.ClaimHex, st.ClaimTxID, st.ClaimBranch = hex.EncodeToString(claim.Serialize()), DisplayHex(claim.TxID()), branch
	if err := s.save("claiming"); err != nil {
		return err
	}
	return s.sendClaim(ctx)
}

func (s *Swap) buildClaim(f Funding, h HTLC, branch uint32) (*Tx, error) {
	secret, err := ParseSecret(s.st.Secret)
	if err != nil {
		return nil, err
	}
	op, err := f.OutPoint()
	if err != nil {
		return nil, err
	}
	var claim *Tx
	err = s.withKey(func(k *Key) error {
		payout, err := s.payoutFor(k)
		if err != nil {
			return err
		}
		claim, err = BuildClaim(SpendRequest{Key: k, HTLC: h, Prev: op, Value: f.Value, Payout: payout, BranchID: branch, Version: s.cfg.TxVersion}, secret)
		return err
	})
	return claim, err
}

// sendClaim sends the stored claim — the same bytes every time — while the cutoff allows, never after it, and
// only once every Zcash node confirms the funding again: a restart in "claiming" is a fresh decision to reveal. A
// claim no node knows and whose branch id the chain has left is signed again under the new one: the old bytes can
// no longer be mined anywhere, so the two can never both be.
func (s *Swap) sendClaim(ctx context.Context) error {
	st, p := s.st, s.cfg.Policy
	ci, cautious, err := s.chain(ctx)
	if err != nil {
		return err
	}
	if _, known, err := TxKnown(ctx, s.zc, st.ClaimTxID); err != nil {
		return err
	} else if known {
		return s.save("claimed")
	}
	if !p.ClaimOpen(cautious, st.CLTV) {
		st.Note = fmt.Sprintf("the claim was built but the cutoff (cltv %d − %d) passed before any node took it; it is never sent", st.CLTV, p.ClaimCutoff)
		s.logf("%s", st.Note)
		return s.save("refund_wait")
	}
	h, err := s.htlc()
	if err != nil {
		return err
	}
	if err := ConfirmFunding(ctx, s.zsrc, st.ZcashChain, h, *st.Funding, p.ConfZec, s.logf); err != nil {
		return err
	}
	// a restart in "claiming" is a fresh decision to reveal: the header gate runs again (spv.go)
	if err := s.headerGateFunding(ctx, ci, *st.Funding); err != nil {
		s.onceLogf("not claiming: %v", err)
		return nil
	}
	if ci.NextBranch != st.ClaimBranch {
		branch, err := s.signingBranch(ctx, ci)
		if err != nil {
			return err
		}
		claim, err := s.buildClaim(*st.Funding, h, branch)
		if err != nil {
			return err
		}
		s.logf("the chain moved from branch id %08x to %08x before the claim was taken: signed again", st.ClaimBranch, branch)
		st.ClaimHex, st.ClaimTxID, st.ClaimBranch = hex.EncodeToString(claim.Serialize()), DisplayHex(claim.TxID()), branch
		if err := st.Save(); err != nil {
			return err
		}
	}
	raw, err := hex.DecodeString(st.ClaimHex)
	if err != nil {
		return err
	}
	// written before any node sees the claim: from this moment the secret may be public
	if !st.Revealed {
		st.Revealed = true
		if err := st.Save(); err != nil {
			return err
		}
	}
	if _, err := BroadcastAll(ctx, s.zsrc, raw, s.logf); err != nil {
		return err
	}
	s.logf("claim %s broadcast: the secret is now public on Zcash", st.ClaimTxID)
	return s.save("claimed")
}

func (s *Swap) makerClaimed(ctx context.Context) error {
	st, p := s.st, s.cfg.Policy
	ci, cautious, err := s.chain(ctx)
	if err != nil {
		return err
	}
	rt, known, err := TxKnown(ctx, s.zc, st.ClaimTxID)
	if err != nil {
		return err
	}
	if !known || !rt.Mined {
		return s.claimNotMined(ctx, ci, cautious, known)
	}
	if st.ClaimBlock != rt.BlockHash {
		if st.ClaimBlock != "" {
			s.logf("REORGANISATION: the claim %s was mined at %d in block %s and is now at %d in block %s", st.ClaimTxID, st.ClaimHeight, st.ClaimBlock, rt.Height, rt.BlockHash)
		}
		st.ClaimHeight, st.ClaimBlock = rt.Height, rt.BlockHash
		if err := st.Save(); err != nil {
			return err
		}
	}
	if rt.Confirmations < p.ConfZec {
		return nil
	}
	st.SpendTxID = st.ClaimTxID
	e, err := EscrowAgreed(ctx, s.dsrc, st.EscrowID)
	if err != nil {
		return err
	}
	switch e.State {
	case EscrowClaimed:
		return s.finish("swapped", fmt.Sprintf("swapped: %d zatoshi claimed in Zcash tx %s; the taker's DERO Claim landed", st.Funding.Value, st.ClaimTxID))
	case EscrowRefunded:
		return s.finish(s.refundOutcome())
	}
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	if h >= st.TDero {
		return s.save("dero_refunding")
	}
	// swap.bas v2 pays the named taker whoever sends Claim (docs/ATOMIC_SWAP_STUDY.md §2.2, Z-C). With this claim C_zec
	// deep the ZEC is the maker's, so he sends hers for her: a taker away when the secret appeared still gets the
	// DERO before T_dero. The contract pays her and nobody else; this costs the maker one fee and changes nothing
	// he holds. Like her own Claim it goes while h + 1 < T_dero, with no execution-delay margin: sent too late, it is
	// refused and costs him one fee.
	if h+1 < st.TDero && !s.settling("relay", h) {
		txid, err := s.dero.Claim(ctx, st.EscrowID, st.Secret)
		if err != nil {
			return s.failedCall("relay", h, err)
		}
		s.noteTx("relay", txid)
		s.logf("sent the taker's DERO Claim for her (swap.bas v2 pays her, whoever sends it)")
		return st.Save()
	}
	return nil
}

func (s *Swap) refundOutcome() (string, string) {
	if s.st.ClaimTxID != "" && s.st.SpendTxID == s.st.ClaimTxID {
		return "claimed_refunded", "the ZEC was claimed (Zcash tx " + s.st.ClaimTxID + ") and the escrow was still open at T_dero; it is refunded"
	}
	if s.st.Revealed {
		return "refunded", "the DERO is refunded; no ZEC was claimed: the secret was revealed in claim " + s.st.ClaimTxID + ", which is not on the chain"
	}
	return "refunded", "the DERO is refunded; no ZEC was claimed, and the secret was never revealed by this companion"
}

// claimNotMined is the maker's claim in no block of the node's best chain: in the mempool, dropped, or taken off the
// chain by a reorganisation, with or without the funding under it. The secret is public from the first broadcast, so
// from here the work is to get the claim mined — the funding's bytes and the claim's are both public, and anyone may
// send them again — and, when that has become impossible, to say so and settle the DERO: Refund after T_dero while
// the escrow is open, or the swap ends lost once the taker's Claim has landed (docs/ZEC_SWAP.md §9 Z5).
func (s *Swap) claimNotMined(ctx context.Context, ci ChainInfo, cautious uint32, known bool) error {
	st, p := s.st, s.cfg.Policy
	if st.Funding == nil {
		return errors.New("zecswap: a claim with no funding recorded")
	}
	if st.ClaimBlock != "" {
		where := "on no chain this node knows"
		if known {
			where = "back in the mempool"
		}
		s.sayOnce("claimoff"+st.ClaimBlock+where, "REORGANISATION: the claim %s, mined at %d in block %s, is %s", st.ClaimTxID, st.ClaimHeight, st.ClaimBlock, where)
	}
	if st.SpendTxID == st.ClaimTxID {
		st.SpendTxID = "" // it was deep once; it is not now
	}
	e, err := EscrowAgreed(ctx, s.dsrc, st.EscrowID)
	if err != nil {
		return err
	}
	if e.State == EscrowRefunded {
		return s.finish(s.refundOutcome())
	}
	// Once the taker's Claim has landed she holds the DERO, and nothing the maker sends can cost her: the claim
	// cutoff, which keeps a late secret from reaching her too close to T_dero, no longer binds him.
	open := p.ClaimOpen(cautious, st.CLTV) || e.State == EscrowClaimed
	frt, fknown, err := TxKnown(ctx, s.zc, st.Funding.TxID)
	if err != nil {
		return err
	}
	gone := false
	switch {
	case fknown && frt.Mined:
		// did something else spend the funding — the taker's refund, which can win once cltv has passed?
		h, err := s.htlc()
		if err != nil {
			return err
		}
		sp, err := s.scanFor(ctx, s.zsrc[0], h, ci.Height)
		if err != nil {
			return err
		}
		if sp != nil && sp.TxID != st.ClaimTxID && sp.Mined {
			st.SpendTxID = sp.TxID
			st.Note = fmt.Sprintf("the funding was spent by %s (%s), not by this companion's claim", sp.TxID, sp.Kind)
			s.logf("%s: the secret was public in the claim, so the taker may claim the DERO as well; the escrow is refunded after T_dero if she does not", st.Note)
			return s.save("refund_wait")
		}
		// the claim goes again only on a funding C_zec deep where the chain now has it
		if !known && open && frt.Confirmations >= p.ConfZec {
			raw, err := hex.DecodeString(st.ClaimHex)
			if err != nil {
				return err
			}
			if _, err := BroadcastAll(ctx, s.zsrc, raw, s.logf); err != nil {
				return err
			}
			s.logf("the claim %s is sent again (the funding is %d deep at height %d)", st.ClaimTxID, frt.Confirmations, frt.Height)
		}
	case !fknown:
		s.sayOnce("fundoff"+st.Funding.BlockHash, "REORGANISATION: the funding %s, read at height %d in block %s before the claim was sent, is on no chain this node knows: the claim %s cannot be mined without it", st.Funding.TxID, st.Funding.Height, st.Funding.BlockHash, st.ClaimTxID)
		// Its bytes are public. Once the claim cutoff has passed they are not sent again: that would lock the taker's
		// coin in the script once more, for a claim this companion may no longer send (Z17).
		if st.FundingHex == "" || !open {
			gone = true
			break
		}
		raw, err := hex.DecodeString(st.FundingHex)
		if err != nil {
			return err
		}
		if _, err := BroadcastAll(ctx, s.zsrc, raw, s.onceLogf); err != nil {
			// Only a node's answer is a verdict. A timeout or a dropped connection says nothing about the funding, and
			// a companion that took it for a refusal could end the swap lost while the funding can still be mined
			// (seen on the regtest zebrad, research/regtest/zecswap-reorg.log, run 2, scenario b).
			var verdict *RPCError
			if !errors.As(err, &verdict) {
				return fmt.Errorf("zecswap: the funding could not be sent again, and no node gave a verdict on it: %w", err)
			}
			s.sayOnce("fundback", "the funding cannot be put back from its own bytes: %v. Its coin has been spent a second time, or its expiry height has passed; unless the chain reorganises back, the claim can never be mined", err)
			gone = true
		} else {
			s.onceLogf("the funding %s is sent again from its own bytes — anyone holding them could — so that the claim can follow it", st.Funding.TxID)
		}
	}
	if e.State == EscrowClaimed {
		if !gone {
			return nil
		}
		// no node may know either, not just this one
		for _, id := range []string{st.Funding.TxID, st.ClaimTxID} {
			if unknown, err := UnknownEverywhere(ctx, s.zsrc, id); err != nil || !unknown {
				return err
			}
		}
		return s.finish("lost", fmt.Sprintf("LOST: this companion revealed the secret in claim %s, the taker's DERO Claim landed, and a reorganisation deeper than C_zec (%d) then took the funding %s off the chain in a form no node will mine again (docs/ZEC_SWAP.md §9 Z5). Neither leg is this maker's. The claim's bytes stay in the state file: if a chain ever carries the funding again, anyone can send them", st.ClaimTxID, p.ConfZec, st.Funding.TxID))
	}
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	if h >= st.TDero {
		return s.save("dero_refunding")
	}
	return nil
}

// ---- the taker ----

func (s *Swap) stepTaker(ctx context.Context) error {
	st, p := s.st, s.cfg.Policy
	switch st.Phase {
	case "":
		return s.takerSetup(ctx)

	case "accepting":
		o, err := s.dero.ReadOffer(ctx, st.OfferID)
		if err != nil {
			return err
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		switch {
		case o.State == OfferAccepted && o.Taker == st.MyRaw && o.RefundPkh == st.RefundPkh && o.CLTV == uint64(st.CLTV):
			st.AcceptHeight = h
			s.noteTx("accept", "")
			return s.save("accepted")
		case o.State == OfferAccepted:
			return s.finish("taken", "another taker accepted the offer first; nothing was sent but the Accept fee")
		case o.State == OfferCancelled:
			return s.finish("taken", "the maker cancelled the offer")
		case h >= o.Expiry:
			return s.finish("declined", "the offer expired before the accept landed")
		}
		// board.bas refuses an Accept that executes at or after the offer's expiry, which one sent now may do up to
		// DeroTxValidity blocks from now: none is sent that could, and an earlier one is left to land, or the offer to
		// expire
		if h+DeroTxValidity >= o.Expiry {
			s.sayOnce("acceptlate", "not sending an Accept at DERO height %d: it could execute at or after the offer's expiry %d", h, o.Expiry)
			return nil
		}
		if s.settling("accept", h) {
			return nil
		}
		if s.attempt("accept") == 0 {
			// nothing has been sent: a cltv decided in an earlier run may be stale, and may still move
			ci, _, err := s.chain(ctx)
			if err != nil {
				return err
			}
			if err := p.CheckCLTV(st.CLTV, ci.Height); err != nil {
				st.CLTV = ci.Height + p.CLTVWindow
				if err := st.Save(); err != nil {
					return err
				}
			}
		}
		txid, err := s.dero.Accept(ctx, st.OfferID, st.RefundPkh, uint64(st.CLTV))
		if err != nil {
			return s.failedCall("accept", h, err)
		}
		s.noteTx("accept", txid)
		st.AcceptHeight = h
		s.logf("accepted offer %d: refund key hash %s, cltv %d", st.OfferID, st.RefundPkh, st.CLTV)
		return s.save("accepted")

	case "accepted":
		o, err := s.dero.ReadOffer(ctx, st.OfferID)
		if err != nil {
			return err
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		deadline := takerLockDeadline(st.AcceptHeight, p, o)
		e, ok, err := s.dero.FindEscrow(ctx, st.MakerRaw, st.MyRaw, st.Hash)
		if err != nil {
			return err
		}
		if !ok {
			if o.State == OfferCancelled || h >= deadline {
				return s.finish("walked", fmt.Sprintf("the maker did not lock before DERO height %d; nothing was funded", deadline))
			}
			s.sayOnce("waitlock", "waiting for the maker's lock (until DERO height %d)", deadline)
			return nil
		}
		// board.bas v3: an offer the maker has not locked for is dead to its taker at its acceptance deadline, and a
		// lock first seen there may have been mined there — so it is refused, whatever else it says
		if o.AcceptDeadline != 0 && h >= o.AcceptDeadline {
			return s.finish("declined", fmt.Sprintf("the maker's lock was first seen at DERO height %d, at or after the acceptance deadline %d; nothing was funded", h, o.AcceptDeadline))
		}
		if err := s.checkEscrow(e, h); err != nil {
			return s.finish("declined", "the maker's lock was refused: "+err.Error()+"; nothing was funded")
		}
		st.EscrowID, st.HaveEscrow, st.LockSeenHeight, st.TDero = e.ID, true, h, e.Expiry
		s.logf("escrow %d checked at DERO height %d: %d of %q, T_dero %d", e.ID, h, e.Amount, e.Asset, e.Expiry)
		return s.save("lock_seen")

	case "lock_seen":
		return s.takerFund(ctx)

	case "funding":
		ci, _, err := s.chain(ctx)
		if err != nil {
			return err
		}
		if _, known, err := TxKnown(ctx, s.zc, st.FundingTxID); err != nil {
			return err
		} else if known {
			return s.save("funded")
		}
		if ci.Height+1 > st.FundingExpiry {
			if unknown, err := UnknownEverywhere(ctx, s.zsrc, st.FundingTxID); err != nil {
				return err
			} else if unknown {
				return s.finish("funding_expired", fmt.Sprintf("the funding %s was never taken by any node and expired at Zcash height %d: nothing was spent", st.FundingTxID, st.FundingExpiry))
			}
			return s.save("funded")
		}
		raw, err := hex.DecodeString(st.FundingHex)
		if err != nil {
			return err
		}
		if _, err := BroadcastAll(ctx, s.zsrc, raw, s.logf); err != nil {
			return err
		}
		s.logf("funded the script: tx %s, %d zatoshi to %s", st.FundingTxID, st.WantAmount, s.mustAddress())
		return s.save("funded")

	case "funded":
		return s.takerFunded(ctx)

	case "secret_seen":
		e, err := s.dero.ReadEscrow(ctx, st.EscrowID)
		if err != nil {
			return err
		}
		switch e.State {
		case EscrowClaimed:
			h, err := s.dero.Height(ctx)
			if err != nil {
				return err
			}
			if st.DeroClaimHeight == 0 {
				st.DeroClaimHeight = h
			}
			return s.save("dero_claimed")
		case EscrowRefunded:
			if agreed, err := EscrowAgreed(ctx, s.dsrc, st.EscrowID); err != nil {
				return err
			} else if agreed.State == EscrowRefunded {
				return s.finish("lost", "the maker claimed the ZEC and the DERO was refunded to him before any Claim landed")
			}
		}
		// give up only by the lowest height any DERO node reports: no single node can make her abandon a claim
		low, err := MinDeroHeight(ctx, s.dsrc)
		if err != nil {
			return err
		}
		if low+1 >= st.TDero {
			return s.finish("lost", fmt.Sprintf("the secret was read at DERO height %d, too late to claim the escrow before T_dero %d: the maker has both legs", low, st.TDero))
		}
		h, err := s.dero.Height(ctx)
		if err != nil {
			return err
		}
		if s.settling("claim", h) {
			return nil
		}
		// No execution-delay margin here, unlike the calls whose heights the companion sets: a Claim that executes at
		// or after T_dero is refused and costs one fee, and a Claim not sent costs her the whole leg. It goes while
		// low + 1 < T_dero (above), the last height from which it can still land in the next block.
		txid, err := s.dero.Claim(ctx, st.EscrowID, st.SecretSeen)
		if err != nil {
			return s.failedCall("claim", h, err)
		}
		s.noteTx("claim", txid)
		st.DeroClaimHeight = h
		return s.save("dero_claimed")

	case "dero_claimed":
		stable, err := s.dero.StableHeight(ctx)
		if err != nil {
			return err
		}
		// the Claim executes up to DeroTxValidity blocks after the height it was sent at: until that last block is
		// stable it may not be mined yet, or be mined in a block DERO can still replace
		if stable < st.DeroClaimHeight+DeroTxValidity {
			return nil
		}
		e, err := EscrowAgreed(ctx, s.dsrc, st.EscrowID)
		if err != nil {
			return err
		}
		switch e.State {
		case EscrowClaimed:
			return s.finish("swapped", fmt.Sprintf("swapped: the escrow's %d of %q is claimed with the secret the maker revealed in Zcash tx %s", e.Amount, e.Asset, st.SpendTxID))
		case EscrowOpen:
			s.logf("the Claim read at DERO height %d is gone from the chain: sending it again", st.DeroClaimHeight)
			st.DeroClaimHeight = 0
			return s.save("secret_seen")
		}
		return s.finish("lost", "the escrow was refunded to the maker")

	case "refunding":
		return s.takerRefunding(ctx)
	}
	return fmt.Errorf("zecswap: the taker has no phase %q", st.Phase)
}

func (s *Swap) mustAddress() string {
	h, err := s.htlc()
	if err != nil {
		return "?"
	}
	return h.Address(s.net())
}

// checkEscrow is §3 step 4's field-by-field check of the maker's lock against the offer: four equalities and a
// bound. A lock that fails any of them is one this taker never funds against.
func (s *Swap) checkEscrow(e Escrow, now uint64) error {
	st, p := s.st, s.cfg.Policy
	switch {
	case e.State != EscrowOpen:
		return fmt.Errorf("the lock is not open (st %d)", e.State)
	case e.Hash != st.Hash:
		return fmt.Errorf("the lock's hash %q is not the offer's %q", e.Hash, st.Hash)
	case e.Taker != st.MyRaw:
		return errors.New("the lock does not name this wallet as its taker")
	case e.Maker != st.MakerRaw:
		return errors.New("the lock was not made by the offer's maker")
	case !strings.EqualFold(e.Asset, st.GiveAsset):
		return fmt.Errorf("the lock holds asset %q, the offer gives %q", e.Asset, st.GiveAsset)
	case e.Amount < st.GiveAmount:
		return fmt.Errorf("the lock holds %d, the offer gives %d", e.Amount, st.GiveAmount)
	case e.Expiry <= now || e.Expiry-now < p.TakerMinDeroLeft:
		return fmt.Errorf("T_dero − now = %d DERO blocks, below the minimum %d", int64(e.Expiry)-int64(now), p.TakerMinDeroLeft)
	}
	return nil
}

func (s *Swap) takerSetup(ctx context.Context) error {
	st, p, cfg := s.st, s.cfg.Policy, s.cfg
	me, err := DeroRaw(cfg.MyDero)
	if err != nil {
		return err
	}
	st.Board, st.Escrow, st.MyDero, st.MyRaw, st.DeroMainnet, st.OfferID = cfg.Board, cfg.Escrow, cfg.MyDero, me, cfg.DeroMainnet, cfg.OfferID
	// GAP-011: the board and the escrow must be the contracts this build was made for, on every DERO node
	if err := CheckPins(ctx, s.dsrc, cfg.Board, cfg.Escrow); err != nil {
		if errors.Is(err, ErrCodeNotPinned) {
			return s.finish("declined", "offer refused: "+err.Error()+"; nothing was sent")
		}
		return err
	}
	if err := CheckEscrowWidth(ctx, s.dsrc, p.minWMin(), 1_000_000); err != nil {
		if errors.Is(err, ErrEscrowWidth) {
			return s.finish("declined", "offer refused: "+err.Error()+"; a narrower escrow can hold a lock too short for the Zcash leg; nothing was sent")
		}
		return err
	}
	o, err := s.dero.ReadOffer(ctx, cfg.OfferID)
	if errors.Is(err, ErrNoSuchOffer) {
		return s.finish("declined", fmt.Sprintf("there is no offer %d on the board", cfg.OfferID))
	}
	if err != nil {
		return err
	}
	ci, cautious, err := s.chain(ctx)
	if err != nil {
		return err
	}
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	decline := func(why string) error { return s.finish("declined", "offer refused: "+why+"; nothing was sent") }
	if o.State == OfferCancelled || (o.State == OfferAccepted && o.Taker != me) {
		return s.finish("taken", "the offer is no longer open")
	}
	if o.Chain != ZcashChainLabel {
		return decline(fmt.Sprintf("its chain is %q, not %q", o.Chain, ZcashChainLabel))
	}
	// ZEC-2: the hash must be exactly the lower-case hex of the bytes the script will hold
	hash, err := ParseSwapHash(o.Hash)
	if err != nil {
		return decline(err.Error())
	}
	anet, kind, claimPkh, err := ParseAddress(o.WantAddr)
	if err != nil {
		return decline("the maker's address: " + err.Error())
	}
	if anet != ci.Net || kind != P2PKH {
		return decline(fmt.Sprintf("the maker's address %s is not a %s P2PKH address", o.WantAddr, ci.Net))
	}
	switch {
	case o.WantAmount < p.MinAmount || o.WantAmount > MaxMoney:
		return decline(fmt.Sprintf("%d zatoshi is outside [%d, %d]", o.WantAmount, p.MinAmount, uint64(MaxMoney)))
	case o.GiveAmount == 0:
		return decline("it gives nothing")
	case o.GiveAsset != "" && !isHex(o.GiveAsset, 32):
		return decline(fmt.Sprintf("its asset %q is not a SCID", o.GiveAsset))
	case o.State == OfferOpen && o.Expiry <= h+DeroTxValidity:
		return decline("it expires before an Accept sent now could execute")
	}
	// Z8: a hash on two rows is a secret that opens two swaps
	rows, err := s.dero.HashRows(ctx, o.Hash)
	if err != nil {
		return err
	}
	if len(rows) != 1 || rows[0] != o.ID {
		return decline(fmt.Sprintf("its hash appears on board rows %v; one secret would open all of them (docs/ZEC_SWAP.md §9 Z8)", rows))
	}
	branch, err := s.signingBranch(ctx, ci)
	if err != nil {
		return decline(err.Error())
	}
	cltv := ci.Height + p.CLTVWindow
	var refundPkh, payout [20]byte
	// the taker must be able to fund before she accepts: an accept she cannot follow freezes the offer
	err = s.withKey(func(k *Key) error {
		refundPkh = k.PubKeyHash()
		if payout, err = s.payoutFor(k); err != nil {
			return err
		}
		coins, err := s.coins(ctx, k)
		if err != nil {
			return err
		}
		_, err = BuildFunding(FundingRequest{Key: k, HTLC: HTLC{Hash: hash, ClaimPkh: claimPkh, RefundPkh: refundPkh, CLTV: cltv}, Amount: o.WantAmount, Coins: coins, BranchID: branch, ExpiryHeight: ci.Height + p.FundingExpiry})
		return err
	})
	if err != nil {
		return decline(err.Error())
	}
	if o.State == OfferAccepted {
		// Accepted by this wallet already (the app, or an earlier run): its numbers are adopted only if they are this
		// key's and its cltv holds against this companion's own Zcash node. The app computes cltv from a height typed
		// by hand (+96) and board.bas stores it as given, so it is checked here, before the taker waits on a lock,
		// and again at funding (takerFund).
		if o.RefundPkh != hex.EncodeToString(refundPkh[:]) || o.CLTV >= LockTimeThreshold {
			return s.finish("taken", "this wallet accepted the offer with another refund key or cltv")
		}
		if err := p.CheckCLTV(uint32(o.CLTV), cautious); err != nil {
			return decline("the board's cltv, set when this wallet accepted, does not hold against this companion's own Zcash node: " + err.Error())
		}
		cltv = uint32(o.CLTV)
	}
	st.HaveOffer, st.Hash, st.GiveAsset, st.GiveAmount, st.WantAmount, st.WantAddr, st.OfferExpiry, st.MakerRaw = true, o.Hash, o.GiveAsset, o.GiveAmount, o.WantAmount, o.WantAddr, o.Expiry, o.Maker
	st.ClaimPkh, st.RefundPkh, st.Payout, st.CLTV = hex.EncodeToString(claimPkh[:]), hex.EncodeToString(refundPkh[:]), hex.EncodeToString(payout[:]), cltv
	return s.save("accepting")
}

// coins are the key's own confirmed outputs, from the node's address index.
func (s *Swap) coins(ctx context.Context, k *Key) ([]Coin, error) {
	us, err := s.zc.AddressUTXOs(ctx, k.Address(s.net()))
	if err != nil {
		return nil, err
	}
	out := make([]Coin, 0, len(us))
	for _, u := range us {
		c, err := CoinOf(u)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// takerFund is §3 step 4: the lock is past DERO's stable depth, still the lock she checked, confirmed by every DERO
// node, and every deadline still holds; then the funding and its three pre-signed refunds are built, the refunds
// written where she can hand them on, the whole intent saved — and only then is the funding sent.
func (s *Swap) takerFund(ctx context.Context) error {
	st, p := s.st, s.cfg.Policy
	stable, err := s.dero.StableHeight(ctx)
	if err != nil {
		return err
	}
	if stable < st.LockSeenHeight {
		return nil
	}
	h, err := s.dero.Height(ctx)
	if err != nil {
		return err
	}
	e, err := s.dero.ReadEscrow(ctx, st.EscrowID)
	if err != nil {
		return err
	}
	decline := func(why string) error {
		return s.finish("declined", "not funding: "+why+"; nothing was spent, and the maker refunds his lock after T_dero")
	}
	if e.Expiry != st.TDero {
		return decline("the lock changed between the two readings")
	}
	if err := s.checkEscrow(e, h); err != nil {
		return decline(err.Error())
	}
	// the gate: every DERO node must show this exact lock, past the stable depth, before any ZEC moves
	if err := ConfirmEscrow(ctx, s.dsrc, e, st.LockSeenHeight, s.checkEscrow, s.logf); err != nil {
		return err
	}
	ci, cautious, err := s.chain(ctx)
	if err != nil {
		return err
	}
	// With -verify-headers the tip these two checks read must be backed by work (spv.go): a node showing a higher tip
	// than the network's would make a cltv that is really later look in policy. A failure is a retry, not a
	// decline — until the lock is too short to fund at all.
	tip, err := s.headerGateTip(ctx, ci, cautious)
	if err != nil {
		if st.TDero-h < p.TakerMinDeroLeft {
			return decline("the Zcash headers up to the tip never verified before the lock became too short to fund: " + err.Error())
		}
		return err
	}
	if err := p.CheckCLTV(st.CLTV, tip); err != nil {
		return decline(err.Error())
	}
	if err := p.CheckTakerTimes(st.TDero-h, st.CLTV-tip); err != nil {
		return decline(err.Error())
	}
	branch, err := s.signingBranch(ctx, ci)
	if err != nil {
		return err
	}
	// The refunds are signed under this branch id and die at the next activation. One inside the swap's life
	// would leave the refund unbroadcastable exactly when it is needed. Two sources say when the next one is: the
	// node's pending upgrades, and this build's own schedule — an out-of-date node knows nothing of an upgrade this
	// build was released for (GAP-021).
	horizon := uint64(ci.Height) + p.ZecBlocksFor(st.TDero-h)
	nextActivation := uint32(0)
	upcoming := append([]NodeUpgrade(nil), ci.Pending...)
	if !ci.Regtest {
		for _, u := range Upgrades {
			if at := u.height(ci.Net); at > ci.Height+1 {
				upcoming = append(upcoming, NodeUpgrade{Name: u.Name + " (this build's schedule)", BranchID: u.BranchID, Height: at})
			}
		}
	}
	for _, u := range upcoming {
		if uint64(u.Height) <= horizon {
			return decline(fmt.Sprintf("network upgrade %s activates at Zcash height %d, before T_dero; a refund signed now would be invalid from then", u.Name, u.Height))
		}
		if nextActivation == 0 || u.Height < nextActivation {
			nextActivation = u.Height
		}
	}
	hc, err := s.htlc()
	if err != nil {
		return err
	}
	var funding *Tx
	var refunds []SignedTx
	err = s.withKey(func(k *Key) error {
		if k.PubKeyHash() != hc.RefundPkh {
			return errors.New("the key is not the one this swap's accept named")
		}
		coins, err := s.coins(ctx, k)
		if err != nil {
			return err
		}
		funding, err = BuildFunding(FundingRequest{Key: k, HTLC: hc, Amount: st.WantAmount, Coins: coins, BranchID: branch, ExpiryHeight: ci.Height + p.FundingExpiry, Version: s.cfg.TxVersion})
		if err != nil {
			return err
		}
		refunds, err = s.signRefunds(k, hc, OutPoint{Hash: funding.TxID(), Index: FundingOutput}, branch)
		return err
	})
	if err != nil {
		return decline(err.Error())
	}
	st.FundingHex, st.FundingTxID, st.FundingExpiry = hex.EncodeToString(funding.Serialize()), DisplayHex(funding.TxID()), ci.Height+p.FundingExpiry
	st.Refunds, st.RefundBranch, st.RefundLevel = refunds, branch, 0
	if st.RefundFile == "" {
		st.RefundFile = s.cfg.RefundFile
		if st.RefundFile == "" {
			st.RefundFile = st.Path() + ".refund.txt"
		}
	}
	if err := s.writeRefundFile(nextActivation); err != nil {
		return err
	}
	if err := s.save("funding"); err != nil {
		return err
	}
	s.logf("the pre-signed refund is in %s: hand it to a second device or a person you trust now", st.RefundFile)
	raw, _ := hex.DecodeString(st.FundingHex)
	if _, err := BroadcastAll(ctx, s.zsrc, raw, s.logf); err != nil {
		return err
	}
	s.logf("funded the script: tx %s, %d zatoshi to %s", st.FundingTxID, st.WantAmount, hc.Address(s.net()))
	return s.save("funded")
}

func (s *Swap) signRefunds(k *Key, hc HTLC, prev OutPoint, branch uint32) ([]SignedTx, error) {
	payout, err := s.payoutFor(k)
	if err != nil {
		return nil, err
	}
	var out []SignedTx
	for _, mult := range RefundFeeLevels {
		tx, err := BuildRefund(SpendRequest{Key: k, HTLC: hc, Prev: prev, Value: s.st.WantAmount, Payout: payout, BranchID: branch, FeeMult: mult, Version: s.cfg.TxVersion})
		if err != nil {
			return nil, err
		}
		fee, _ := tx.Fee()
		out = append(out, SignedTx{Hex: hex.EncodeToString(tx.Serialize()), TxID: DisplayHex(tx.TxID()), FeeMult: mult, Fee: fee})
	}
	return out, nil
}

// writeRefundFile is Z-B's hand-off: the refund, signed, with what it does, when it works, and what it cannot fix.
func (s *Swap) writeRefundFile(nextActivation uint32) error {
	st := s.st
	hc, err := s.htlc()
	if err != nil {
		return err
	}
	var payout [20]byte
	if err := decodeHexInto(payout[:], st.Payout); err != nil {
		return err
	}
	next := "none this build or the node knows of"
	if nextActivation != 0 {
		next = fmt.Sprintf("Zcash height %d", nextActivation)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# zecswap pre-signed refund (docs/ATOMIC_SWAP_STUDY.md §2.2, Z-B)\n")
	fmt.Fprintf(&b, "# swap: board %s offer %d, escrow %s id %d; Zcash %s\n", st.Board, st.OfferID, st.Escrow, st.EscrowID, st.ZcashChain)
	fmt.Fprintf(&b, "# script address %s; funding %s:%d of %d zatoshi\n", hc.Address(s.net()), st.FundingTxID, FundingOutput, st.WantAmount)
	fmt.Fprintf(&b, "# It pays the funding back to %s and nowhere else: that is signed, and nobody holding this file can change it.\n", EncodeAddress(s.net(), P2PKH, payout))
	fmt.Fprintf(&b, "# It can be mined from Zcash height %d on (nLockTime %d) and never expires by height (nExpiryHeight 0, ZIP 203).\n", st.CLTV+1, st.CLTV)
	fmt.Fprintf(&b, "# It is signed under consensus branch id %08x and stops being valid at the next network upgrade: %s.\n", st.RefundBranch, next)
	fmt.Fprintf(&b, "# After an upgrade only the refund key can sign it again.\n")
	fmt.Fprintf(&b, "# To use it: once the Zcash height is %d or more, send one line below with any node: zcash-cli sendrawtransaction <hex>\n", st.CLTV)
	fmt.Fprintf(&b, "# (or zebrad's RPC, or any service that relays raw transactions). Start with level 1; if it is not mined and no node\n")
	fmt.Fprintf(&b, "# still holds it, send the next level. They spend the same output, so at most one can ever be mined.\n")
	fmt.Fprintf(&b, "# WHAT IT CANNOT FIX: someone must broadcast it between height %d and the maker's spend. The claim branch of the\n", st.CLTV)
	fmt.Fprintf(&b, "# script never expires, so if nobody does, the maker can take the ZEC with the secret at any later moment\n")
	fmt.Fprintf(&b, "# (docs/ZEC_SWAP.md §9 Z17; operator decision 15). Whoever holds this file learns about the swap.\n")
	for i, r := range st.Refunds {
		fmt.Fprintf(&b, "level %d fee %d zatoshi (%dx) txid %s\n%s\n", i+1, r.Fee, r.FeeMult, r.TxID, r.Hex)
	}
	return writeAtomic(st.RefundFile, []byte(b.String()))
}

// takerFunded watches the funding and its spend. The maker's claim hands over the secret; cltv hands the taker her
// refund. Nothing else moves her.
func (s *Swap) takerFunded(ctx context.Context) error {
	st := s.st
	ci, cautious, err := s.chain(ctx)
	if err != nil {
		return err
	}
	rt, known, err := TxKnown(ctx, s.zc, st.FundingTxID)
	if err != nil {
		return err
	}
	switch {
	case !known && ci.Height+1 > st.FundingExpiry:
		if unknown, err := UnknownEverywhere(ctx, s.zsrc, st.FundingTxID); err != nil {
			return err
		} else if unknown {
			return s.finish("funding_expired", fmt.Sprintf("the funding %s left every node unmined and expired at Zcash height %d: nothing was spent", st.FundingTxID, st.FundingExpiry))
		}
	case !known:
		raw, _ := hex.DecodeString(st.FundingHex)
		if _, err := BroadcastAll(ctx, s.zsrc, raw, s.onceLogf); err != nil {
			s.onceLogf("the funding could not be re-sent: %v", err)
		}
	case rt.Mined:
		f := Funding{TxID: st.FundingTxID, Index: FundingOutput, Value: st.WantAmount, Height: rt.Height, BlockHash: rt.BlockHash}
		if st.Funding == nil || *st.Funding != f {
			st.Funding = &f
			if err := st.Save(); err != nil {
				return err
			}
		}
	}
	// cltv first, before any read a counterparty could make fail: from here the refund must go out whatever else
	// the nodes can or cannot be made to say, and the refunding phase still watches for a claim
	if cautious >= st.CLTV {
		s.logf("Zcash height %d has reached cltv %d: refunding", cautious, st.CLTV)
		return s.save("refunding")
	}
	if st.Funding == nil {
		return nil
	}
	_, err = s.spendCheck(ctx)
	return err
}

// scanMargin is how far below the last height at which the funding was seen unspent the search for its spend
// starts: a reorganisation of up to this depth cannot hide the spend from it.
const scanMargin = 10

// spendCheck looks at the funding output through every Zcash node: unspent, or spent by a claim — whose secret is
// checked against the hash, so any one honest node that shows it is enough — or by the refund. The first read is
// gettxout on the one outpoint; only once a node says it is spent does the budgeted search over the address begin
// on that node. A node that cannot be read is passed over, never waited for.
func (s *Swap) spendCheck(ctx context.Context) (bool, error) {
	st := s.st
	hc, err := s.htlc()
	if err != nil {
		return false, err
	}
	var firstErr error
	for _, src := range s.zsrc {
		sci, err := src.Node.ChainInfo(ctx)
		if err == nil && sci.Chain != st.ZcashChain {
			err = fmt.Errorf("it is on %s", sci.Chain)
		}
		if err != nil {
			s.sayOnce("spend"+src.Name, "%s cannot be read for the funding's spend: %v", src.Name, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sp, err := s.scanFor(ctx, src, hc, sci.Height)
		if err != nil {
			s.sayOnce("spend"+src.Name, "%s cannot be read for the funding's spend: %v", src.Name, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if sp == nil {
			continue
		}
		switch sp.Kind {
		case SpendClaim:
			st.SecretSeen, st.SpendTxID = hex.EncodeToString(sp.Secret[:]), sp.TxID
			s.logf("%s shows the maker's claim %s; the secret is read from it and checked against the hash", src.Name, sp.TxID)
			return true, s.save("secret_seen")
		case SpendRefund:
			if st.Phase != "refunding" {
				st.SpendTxID = sp.TxID
				return true, s.save("refunding")
			}
		default:
			return false, fmt.Errorf("zecswap: %s shows the funding spent by %s, which reads as neither the claim nor the refund: stopping, since a spend this companion cannot read may carry the secret", src.Name, sp.TxID)
		}
	}
	return false, firstErr
}

// scanFor is one node's look at the funding: gettxout, then — once spent — the budgeted search, with its cursor
// kept in the state under the node's name.
func (s *Swap) scanFor(ctx context.Context, src ZcashSource, hc HTLC, tip uint32) (*Spend, error) {
	st := s.st
	if st.Funding == nil {
		return nil, nil
	}
	if st.Scans == nil {
		st.Scans = map[string]ScanCursor{}
	}
	cur := st.Scans[src.Name]
	unspent, err := OutputUnspent(ctx, src.Node, *st.Funding, hc.ScriptPubKey())
	if err != nil {
		return nil, err
	}
	if unspent {
		base := st.Funding.Height
		if tip > base+scanMargin {
			base = tip - scanMargin
		}
		if base > cur.Base {
			st.Scans[src.Name] = ScanCursor{Base: base}
		}
		return nil, nil
	}
	sp, err := FindSpendStep(ctx, src.Node, hc, s.net(), *st.Funding, &cur, tip, SpendScanBudget)
	st.Scans[src.Name] = cur
	if serr := st.Save(); serr != nil {
		return nil, serr
	}
	return sp, err
}

// takerRefunding sends the pre-signed refund to every node from cltv on, climbing the fee ladder as blocks pass,
// until one is C_zec deep on every node — or until the maker's claim wins the race, which hands over the secret
// instead. No answer from any node stops the broadcast: the refund is idempotent, and a missing one is Z17.
func (s *Swap) takerRefunding(ctx context.Context) error {
	st, p := s.st, s.cfg.Policy
	ci, cautious, err := s.chain(ctx)
	if err != nil {
		return err
	}
	known := -1
	for i, r := range st.Refunds {
		if deep, err := MinedEverywhere(ctx, s.zsrc, r.TxID, p.ConfZec); err == nil && deep {
			st.SpendTxID = r.TxID
			return s.finish("refunded", fmt.Sprintf("the ZEC is refunded: Zcash tx %s, at least %d confirmations on every node", r.TxID, p.ConfZec))
		}
		if _, k, err := TxKnown(ctx, s.zc, r.TxID); err == nil && k && known < 0 {
			known = i
		}
	}
	if st.Funding == nil {
		rt, k, err := TxKnown(ctx, s.zc, st.FundingTxID)
		switch {
		case err == nil && k && rt.Mined:
			st.Funding = &Funding{TxID: st.FundingTxID, Index: FundingOutput, Value: st.WantAmount, Height: rt.Height, BlockHash: rt.BlockHash}
		case err == nil && !k && ci.Height+1 > st.FundingExpiry:
			if unknown, err := UnknownEverywhere(ctx, s.zsrc, st.FundingTxID); err == nil && unknown {
				return s.finish("funding_expired", fmt.Sprintf("the funding %s was never mined by any node and expired at Zcash height %d: nothing was spent, and there is nothing to refund", st.FundingTxID, st.FundingExpiry))
			}
		}
	}
	if st.Funding != nil {
		if spent, err := s.spendCheck(ctx); spent {
			return err
		}
	}
	if cautious < st.CLTV {
		return nil
	}
	if ci.NextBranch != st.RefundBranch && known < 0 {
		if err := s.resignRefunds(ctx, ci); err != nil {
			s.logf("the chain's branch id is %08x and the refunds are signed under %08x, and they cannot be signed again now: %v", ci.NextBranch, st.RefundBranch, err)
		}
	}
	level := int((cautious - st.CLTV) / p.RefundBump)
	if level >= len(st.Refunds) {
		level = len(st.Refunds) - 1
	}
	if level > st.RefundLevel {
		st.RefundLevel = level
		if err := st.Save(); err != nil {
			return err
		}
	}
	// a refund some node already holds is the one to spread: a higher level would be refused as a conflict
	send := st.RefundLevel
	if known >= 0 {
		send = known
	}
	r := st.Refunds[send]
	raw, err := hex.DecodeString(r.Hex)
	if err != nil {
		return err
	}
	if _, err := BroadcastAll(ctx, s.zsrc, raw, s.logf); err != nil {
		return fmt.Errorf("the refund (level %d) was not taken by any node: %w", send+1, err)
	}
	s.sayOnce("refund"+r.TxID, "refund %s (level %d, fee %d) broadcast", r.TxID, send+1, r.Fee)
	return nil
}

// resignRefunds signs the refunds again under the branch id the chain has moved to. The old ones can no longer
// be mined anywhere, so the new ones can never be mined beside them.
func (s *Swap) resignRefunds(ctx context.Context, ci ChainInfo) error {
	st := s.st
	branch, err := s.signingBranch(ctx, ci)
	if err != nil {
		return err
	}
	hc, err := s.htlc()
	if err != nil {
		return err
	}
	op := OutPoint{Index: FundingOutput}
	if op.Hash, err = ParseDisplayHex(st.FundingTxID); err != nil {
		return err
	}
	var refunds []SignedTx
	if err := s.withKey(func(k *Key) error {
		refunds, err = s.signRefunds(k, hc, op, branch)
		return err
	}); err != nil {
		return err
	}
	s.logf("the chain moved from branch id %08x to %08x: the refunds are signed again and the refund file rewritten", st.RefundBranch, branch)
	st.Refunds, st.RefundBranch = refunds, branch
	if err := s.writeRefundFile(0); err != nil {
		return err
	}
	return st.Save()
}

// ---- small helpers ----

func isHex(s string, n int) bool {
	if len(s) != 2*n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func decodeHexInto(dst []byte, s string) error {
	b, err := hex.DecodeString(s)
	if err != nil {
		return err
	}
	if len(b) != len(dst) {
		return fmt.Errorf("%d bytes, want %d", len(b), len(dst))
	}
	copy(dst, b)
	return nil
}
