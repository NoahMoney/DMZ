package xmrswap

import (
	"context"
	"errors"
	"fmt"
)

// Report is what the status subcommand prints for one swap (BL-26): the state file's own account, what the DERO
// chain and the joint Monero wallet say now, and every place the two disagree. Lines are facts; Diverged are the
// disagreements, each one a reason for the operator to look before trusting either side.
type Report struct {
	Lines    []string
	Diverged []string
}

// Reconcile compares one swap's state file with the chain and, when w is not nil, with the joint Monero view
// wallet. It reads and never writes: no state is saved and nothing is sent. The one thing it changes is which
// wallet the wallet-rpc has open, so the caller must not point it at the wallet-rpc of a companion that is running.
func Reconcile(ctx context.Context, st *State, d Dero, w Wallet) Report {
	var r Report
	add := func(f string, a ...interface{}) { r.Lines = append(r.Lines, fmt.Sprintf(f, a...)) }
	bad := func(f string, a ...interface{}) { r.Diverged = append(r.Diverged, fmt.Sprintf(f, a...)) }

	add("%s, phase %q, done %v, swap %s", st.Role, st.Phase, st.Done, or0(st.SwapID))
	if st.Outcome != "" {
		add("outcome: %s", st.Outcome)
	}
	add("terms: %d atomic units of %s for %d piconero on %s; stipend %d; t1 %d, t2 %d", st.Terms.AmountDero, assetName(st.Terms.Asset), st.Terms.AmountXmr, st.Terms.Net, st.Terms.Stipend, st.Lock.T1, st.Lock.T2)
	if st.Stipend.Tx != "" {
		add("stipend: %d, tx %s (height %d)", st.Stipend.Amount, st.Stipend.Tx, st.Stipend.Height)
	}

	allowed, expected := lockStatesFor(st)
	var lock Lock
	haveLock := false
	switch {
	case !expected:
		add("chain: no lock is expected in this phase")
	default:
		l, err := d.ReadLock(ctx, st.Lock.ID)
		switch {
		case errors.Is(err, ErrNoSuchLock):
			if st.Phase == "lock_announced" {
				add("chain: lock %d is announced and not visible yet", st.Lock.ID)
			} else {
				bad("the state names lock %d and the contract has no such lock", st.Lock.ID)
			}
		case err != nil:
			add("chain: lock %d could not be read (%v): not compared", st.Lock.ID, err)
		default:
			lock, haveLock = l, true
			h, _ := d.Height(ctx)
			add("chain: lock %d is %s at height %d (t1 %d, t2 %d, %d of %s)", l.ID, lockStateName(l.State), h, l.T1, l.T2, l.Amount, assetName(l.Asset))
			if !allowed[l.State] {
				bad("the state file says %q, and the chain says lock %d is %s: run the companion to catch up, or look at why it stopped", st.Phase, l.ID, lockStateName(l.State))
			}
		}
	}
	if haveLock {
		for _, m := range lockMismatches(st, lock) {
			bad("lock %d is not the lock this swap agreed: %s", lock.ID, m)
		}
	}

	if w != nil && st.Joint != "" {
		reconcileMonero(ctx, st, w, add, bad)
	} else if st.Joint != "" {
		add("monero: not read (no -monero, or a companion is running on this state)")
	}
	return r
}

// reconcileMonero opens the joint view wallet by its name — making it from the shared view key when this party
// never made one (the taker never needs to), which can see and cannot spend — and reads what the joint address has
// received. A view-only wallet sees what arrives and not what is spent, so what it can check is the received total
// against the terms, at the phases where the swap acted on that total having arrived.
func reconcileMonero(ctx context.Context, st *State, w Wallet, add, bad func(string, ...interface{})) {
	name := jointWalletName(st.HTLC, st.SwapID, st.Lock.ID, "view")
	if err := w.OpenWallet(ctx, name, st.JointPassword); err != nil {
		if st.View == "" {
			add("monero: no joint view key in this state yet: not compared")
			return
		}
		if gerr := w.GenerateFromKeys(ctx, name, st.Joint, st.View, "", st.JointPassword, st.RestoreHeight); gerr != nil {
			add("monero: the joint view wallet %s cannot be opened or made (%v): not compared", name, gerr)
			return
		}
		if err := w.OpenWallet(ctx, name, st.JointPassword); err != nil {
			add("monero: the joint view wallet %s cannot be opened (%v): not compared", name, err)
			return
		}
	}
	a, err := w.Address(ctx)
	if err != nil {
		add("monero: %v", err)
		return
	}
	if a != st.Joint {
		bad("the wallet named %s holds %s, not this swap's joint address %s", name, a, st.Joint)
		return
	}
	if err := w.Refresh(ctx, st.RestoreHeight); err != nil {
		add("monero: refresh: %v: not compared", err)
		return
	}
	min := st.RestoreHeight
	if min > 0 {
		min--
	}
	in, err := w.Incoming(ctx, min)
	if err != nil {
		add("monero: %v", err)
		return
	}
	var received uint64
	for _, t := range in {
		received += t.Amount
	}
	add("monero: the joint address %s has received %d piconero in %d confirmed transfers (terms %d)", st.Joint, received, len(in), st.Terms.AmountXmr)
	if xmrActedOn(st) && received < st.Terms.AmountXmr {
		bad("the joint address has received %d piconero and this swap acted on %d having arrived (phase %q): a Monero reorganisation, or a wallet that is not scanning", received, st.Terms.AmountXmr, st.Phase)
	}
}

// lockStatesFor is which states of the lock on the chain the state file's phase is consistent with, and whether a
// lock is expected at all. A companion that is not running lags the chain; that is what a divergence reports.
func lockStatesFor(st *State) (map[uint64]bool, bool) {
	set := func(s ...uint64) map[uint64]bool {
		m := map[uint64]bool{}
		for _, x := range s {
			m[x] = true
		}
		return m
	}
	if st.Role == "bob" {
		switch st.Phase {
		case "dero_locked", "stipend_sending", "watching_xmr", "xmr_confirmed", "gate_sending":
			return set(LockOpen), true
		case "gate_sent", "refund_window":
			return set(LockOpen, LockClaimed), true
		case "claimed", "sweep_ready", "sweeping", "swept", "sweep_unknown":
			return set(LockClaimed), true
		case "refunding":
			return set(LockOpen, LockRefunded), true
		case "refunded":
			return set(LockRefunded), true
		case "punish_window", "closing":
			return set(LockOpen, LockPunished), true
		case "punished":
			return set(LockPunished), true
		}
		return nil, false
	}
	switch st.Phase {
	case "lock_announced", "saw_dero_lock", "xmr_locking", "xmr_locked", "gate_received", "await_refund":
		return set(LockOpen), true
	case "claiming":
		return set(LockOpen, LockClaimed), true
	case "claim_confirming", "claimed":
		return set(LockClaimed), true
	case "refund_seen", "sweep_ready", "sweeping", "swept", "sweep_unknown", "aborted":
		return set(LockRefunded), true
	case "punish_window", "punishing":
		return set(LockOpen, LockPunished), true
	case "punished":
		return set(LockPunished), true
	}
	return nil, false
}

// lockMismatches is every field of the lock on the chain that is not what this swap agreed or recorded.
func lockMismatches(st *State, l Lock) []string {
	var out []string
	if st.Lock.T1 != 0 && l.T1 != st.Lock.T1 {
		out = append(out, fmt.Sprintf("t1 %d, recorded %d", l.T1, st.Lock.T1))
	}
	if st.Lock.T2 != 0 && l.T2 != st.Lock.T2 {
		out = append(out, fmt.Sprintf("t2 %d, recorded %d", l.T2, st.Lock.T2))
	}
	if st.Lock.Amount != 0 && l.Amount != st.Lock.Amount {
		out = append(out, fmt.Sprintf("amount %d, recorded %d", l.Amount, st.Lock.Amount))
	}
	if l.Asset != st.Terms.Asset {
		out = append(out, fmt.Sprintf("asset %s, agreed %s", assetName(l.Asset), assetName(st.Terms.Asset)))
	}
	own, peer := st.Own.H, st.Peer.H
	ha, hb := l.HA, l.HB
	if st.Role == "bob" {
		ha, hb = l.HB, l.HA // his own share's hash is hb, hers is ha
	}
	if own != "" && ha != own {
		out = append(out, "the hash of this party's share is not in it")
	}
	if peer != "" && hb != peer {
		out = append(out, "the hash of the peer's share is not in it")
	}
	if st.HK != "" && l.HK != st.HK {
		out = append(out, "hk is not the gate hash committed to")
	}
	me, err := DeroRaw(st.MyDero)
	if err == nil {
		if st.Role == "bob" && l.Maker != me {
			out = append(out, "the maker is not this party's DERO address")
		}
		if st.Role == "alice" && l.Taker != me {
			out = append(out, "the taker is not this party's DERO address")
		}
	}
	return out
}

// xmrActedOn is whether this party has acted on the XMR having arrived at the joint address: Bob from the gate on,
// Alice once the gate reached her (which Bob sends only on his own count).
func xmrActedOn(st *State) bool {
	switch st.Phase {
	case "xmr_confirmed", "gate_sending", "gate_sent":
		return st.Role == "bob"
	case "gate_received", "claiming", "claim_confirming":
		return st.Role == "alice"
	case "claimed", "sweep_ready", "sweeping", "swept":
		return true
	}
	return false
}

func lockStateName(s uint64) string {
	switch s {
	case LockOpen:
		return "open"
	case LockClaimed:
		return "claimed"
	case LockRefunded:
		return "refunded"
	case LockPunished:
		return "punished"
	}
	return fmt.Sprintf("in state %d", s)
}

func or0(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
