package xmrswap

import (
	"strconv"
	"time"

	"veil/tools/swapstatus"
)

// StatusV1 is this swap as the version 1 status answer of docs/SWAP_STATUS.md, which `-status` serves to the app on the
// same machine. It is built from the state file's own account, the configuration, the DERO height the companion last
// read (deroTip, 0 when it has read none) and the Monero confirmations its gate last counted. It reads nothing and
// invents nothing: a value the companion does not know is left out.
//
// It carries no secret. It is built from the public parts of the state only — heights, amounts, ids, txids — and never
// from Own.Secret, Gate, View, JointPassword, Revealed, the channel key or an Outcome sentence (which can quote an
// error); statusv1_test.go runs every phase of both roles and looks for each of those values in every answer.
//
// Call it from the goroutine that runs Step: it reads the state the machine is writing.
func (s *Swap) StatusV1(deroTip uint64, now time.Time) swapstatus.Payload {
	return statusV1(s.st, s.cfg, deroTip, s.xmrConfs, now)
}

// Words the answer uses for the two roles: the maker (bob) gives the DERO side, a withdrawal; the taker (alice) gives
// XMR, a deposit.
func appRole(role string) string {
	if role == "bob" {
		return "maker"
	}
	return "taker"
}

func statusV1(st *State, cfg Config, deroTip uint64, xmrConfs *uint64, now time.Time) swapstatus.Payload {
	cfg = cfg.withDefaults()
	role := appRole(st.Role)
	p := swapstatus.Payload{V: swapstatus.Version, Program: "xmrswap", Role: role, Direction: swapstatus.Direction(role),
		Network: cfg.DeroNet, Contract: st.HTLC, Phase: st.Phase, Updated: now.UTC().Format(time.RFC3339)}
	if p.Contract == "" {
		p.Contract = cfg.HTLC
	}
	// the terms as agreed once they are, and as this party was configured before
	t := st.Terms
	if t.AmountXmr == 0 {
		t = Terms{AmountDero: cfg.AmountDero, AmountXmr: cfg.AmountXmr, Asset: cfg.Asset, Decimals: cfg.AssetDecimals}
	}
	p.Asset = t.Asset
	if t.AmountDero > 0 {
		p.DeroAmount = strconv.FormatUint(t.AmountDero, 10)
	}
	if t.AmountXmr > 0 {
		p.ForeignAmount = strconv.FormatUint(t.AmountXmr, 10)
	}
	// DMZ's swap fee (fee.go): SwapFee of the XMR, paid by the maker's sweep after a Claim; none where no treasury is
	// recorded. A sweep that has gone out says what it paid.
	fee := uint64(0)
	if cfg.XmrTreasury != "" {
		fee = SwapFee(t.AmountXmr)
	}
	if st.Sweep.Fee > 0 {
		fee = st.Sweep.Fee
	}
	if t.AmountXmr > 0 {
		p.Fee = strconv.FormatUint(fee, 10)
	}
	v := statusView{st: st, cfg: cfg, tip: deroTip, now: now, rated: cfg.DeroNet != "simulator", fee: fee, xmrConfs: xmrConfs,
		dero: assetWords(t), xmr: swapstatus.Units(t.AmountXmr, 12) + " XMR"}
	if st.Role == "bob" {
		p.Steps, p.Next = v.bobSteps()
		if lockKnownBob[st.Phase] {
			p.Lock = swapstatus.U(st.Lock.ID)
		}
	} else {
		p.Steps, p.Next = v.aliceSteps()
		if st.Lock.SeenHeight > 0 {
			p.Lock = swapstatus.U(st.Lock.ID)
		}
	}
	if st.Done {
		p.Outcome = outcomeV1(st)
	}
	// a deadline belongs to a step still to happen: one that is done, skipped or failed counts nothing down
	for i := range p.Steps {
		if p.Steps[i].State != swapstatus.Active && p.Steps[i].State != swapstatus.Waiting {
			p.Steps[i].Deadline = nil
		}
	}
	return p
}

// outcomeV1 is an ended swap in the contract's words. The contract has no word for a swap that ended with nothing
// locked (refused, or the maker taking his DERO back before any XMR moved): each side has what it started with, so it
// is "refunded", and the answer's next sentence says that nothing moved.
func outcomeV1(st *State) string {
	switch st.Phase {
	case "swept", "sweep_unknown":
		if st.Role == "bob" {
			return "swapped" // the Claim is stable: the XMR is his, swept or not (a failed sweep is a failed step)
		}
		return "refunded" // alice sweeps only her own XMR back, after a Refund or a Reclaim
	case "claimed":
		return "swapped"
	case "punished":
		return "punished"
	case "reclaimed":
		return "reclaimed"
	}
	return "refunded" // refunded, aborted, refused
}

func assetWords(t Terms) string {
	if t.Asset == "" {
		return swapstatus.Units(t.AmountDero, 5) + " DERO"
	}
	return swapstatus.Units(t.AmountDero, t.Decimals) + " of token " + t.Asset[:8]
}

type statusView struct {
	st       *State
	cfg      Config
	tip      uint64
	now      time.Time
	rated    bool
	fee      uint64
	xmrConfs *uint64
	dero     string
	xmr      string
}

// at is a DERO deadline, or nil when the height is not known yet.
func (v statusView) at(h uint64) *swapstatus.Deadline {
	if h == 0 {
		return nil
	}
	return swapstatus.At(swapstatus.DERO, h, v.tip, v.rated, v.now)
}

// stable is the depth DERO needs before nothing can replace a block (STABLE_LIMIT), as each confirmer waits for it.
var stableNeed = swapstatus.U(DefaultStableBlocks)

func step(id, label, chain, state string) swapstatus.Step {
	return swapstatus.Step{ID: id, Label: label, Chain: chain, State: state}
}

// ladder is the state of each step of a happy path of n steps with the swap at stage k (0 before the first, n after
// the last): done below it, active at it, waiting above.
func ladder(i, k int) string {
	switch {
	case i < k:
		return swapstatus.Done
	case i == k:
		return swapstatus.Active
	}
	return swapstatus.Waiting
}

var lockKnownBob = map[string]bool{"dero_locked": true, "stipend_sending": true, "watching_xmr": true, "xmr_confirmed": true, "gate_sending": true,
	"gate_sent": true, "claimed": true, "sweep_ready": true, "sweeping": true, "swept": true, "sweep_unknown": true, "refund_window": true,
	"refunding": true, "refund_confirming": true, "refunded": true, "punish_window": true, "reclaiming": true, "reclaim_confirming": true,
	"reclaimed": true, "punished": true}

// bobStage is where the maker's happy path stands: 0 setting up, then the lock, the stipend, the XMR, the gate, the
// Claim, the sweep, 7 after it; -1 on an exit path or refused.
var bobStage = map[string]int{"": 0, "setup": 0, "verified_pi_a": 0, "dero_locking": 1, "dero_locked": 2, "stipend_sending": 2, "watching_xmr": 3,
	"xmr_confirmed": 4, "gate_sending": 4, "gate_sent": 5, "claimed": 6, "sweep_ready": 6, "sweeping": 6, "swept": 7, "sweep_unknown": 7}

func (v statusView) bobSteps() ([]swapstatus.Step, string) {
	st, c := v.st, v.cfg
	sweepLabel := "Your xmrswap sweeps the " + v.xmr + " to you"
	if v.fee > 0 {
		sweepLabel += ", less the 0.5% fee"
	}
	steps := []swapstatus.Step{
		step("dero_lock", "You lock "+v.dero+" for the taker", swapstatus.DERO, ""),
		step("stipend", "You send the taker the DERO for her fees", swapstatus.DERO, ""),
		step("xmr_lock", "The taker locks "+v.xmr+" at the joint address", swapstatus.Monero, ""),
		step("gate", "You hand the taker your gate secret", swapstatus.Monero, ""),
		step("claim", "The taker claims the "+v.dero+", before t1", swapstatus.DERO, ""),
		step("sweep", sweepLabel, swapstatus.Monero, ""),
	}
	lock, stip, xmr, gate, claim, sweep := &steps[0], &steps[1], &steps[2], &steps[3], &steps[4], &steps[5]
	lock.TxID = swapstatus.Hex64(st.Lock.Tx)
	if st.Lock.LockedHeight > 0 {
		lock.Conf, lock.Need = swapstatus.Depth(v.tip, st.Lock.LockedHeight), stableNeed
	}
	stip.TxID = swapstatus.Hex64(st.Stipend.Tx)
	if st.Stipend.Height > 0 {
		stip.Conf, stip.Need = swapstatus.Depth(v.tip, st.Stipend.Height), stableNeed
	}
	xmr.Need = swapstatus.U(c.Confirmations)
	claim.Deadline = v.at(st.Lock.T1)
	if st.Lock.ClaimSeenHeight > 0 {
		claim.Conf, claim.Need = swapstatus.Depth(v.tip, st.Lock.ClaimSeenHeight), stableNeed
	}
	if len(st.Sweep.Txs) > 0 {
		sweep.TxID = swapstatus.Hex64(st.Sweep.Txs[0])
	}
	if k, happy := bobStage[st.Phase]; happy {
		for i := range steps {
			steps[i].State = ladder(i, k-1)
		}
		switch k {
		case 1:
			if st.Contract.W1Min > 0 {
				lock.Deadline = v.at(lockCutoff(st.Lock.T1, st.Contract.W1Min))
			}
		case 2:
			stip.Deadline = v.at(st.Lock.T1)
		case 3:
			xmr.Conf = v.xmrConfs
			xmr.Deadline = v.at(deadlineOf(st.Lock.T1, c.GateMargin))
		case 4:
			gate.Deadline = v.at(deadlineOf(st.Lock.T1, c.GateMargin))
		}
		if k < 3 {
			gate.Deadline = v.at(deadlineOf(st.Lock.T1, c.GateMargin))
		}
		if st.Phase == "sweep_unknown" {
			sweep.State = swapstatus.Failed
		}
		return steps, v.bobNext()
	}
	for i := range steps {
		steps[i].State = swapstatus.Skipped
	}
	if st.Phase == "refused" {
		lock.State = swapstatus.Failed
		return steps, v.bobNext()
	}
	// an exit: what happened before it, then the exits taken
	lock.State = swapstatus.Done
	if st.Stipend.Tx != "" {
		stip.State = swapstatus.Done
	}
	if st.Xmr.ConfirmedAt > 0 {
		xmr.State, gate.State = swapstatus.Done, swapstatus.Failed // the gate did not lead to a Claim in time
	}
	g2 := st.Contract.G2
	refund := step("refund", "Your xmrswap refunds your DERO, from t1 until the refund cutoff", swapstatus.DERO, swapstatus.Failed)
	refund.TxID = swapstatus.Hex64(st.RefundTx)
	refund.Deadline = v.at(refundCutoff(st.Lock.T2))
	if st.Lock.RefundSeenHeight > 0 {
		refund.Conf, refund.Need = swapstatus.Depth(v.tip, st.Lock.RefundSeenHeight), stableNeed
	}
	switch st.Phase {
	case "refund_window":
		refund.State = swapstatus.Active
		if v.tip > 0 && v.tip < st.Lock.T1 {
			refund.State = swapstatus.Waiting
		}
	case "refunding", "refund_confirming":
		refund.State = swapstatus.Active
	case "refunded":
		refund.State = swapstatus.Done
	}
	steps = append(steps, refund)
	switch st.Phase {
	case "punish_window", "reclaiming", "reclaim_confirming", "reclaimed", "punished":
		punish := step("punish", "Anyone may Punish the lock, from t2 until t2 + g2, which pays the taker your DERO", swapstatus.DERO, swapstatus.Skipped)
		reclaim := step("reclaim", "Your xmrswap reclaims your DERO, from t2 + g2", swapstatus.DERO, swapstatus.Waiting)
		reclaim.TxID = swapstatus.Hex64(st.ReclaimTx)
		if st.Lock.ReclaimSeenHeight > 0 {
			reclaim.Conf, reclaim.Need = swapstatus.Depth(v.tip, st.Lock.ReclaimSeenHeight), stableNeed
		}
		if g2 > 0 {
			punish.Deadline, reclaim.Deadline = v.at(st.Lock.T2+g2), v.at(st.Lock.T2+g2)
		}
		switch st.Phase {
		case "punish_window":
			punish.State = swapstatus.Waiting
			if v.tip >= st.Lock.T2 {
				punish.State = swapstatus.Active
			}
		case "reclaiming", "reclaim_confirming":
			reclaim.State = swapstatus.Active
		case "reclaimed":
			reclaim.State = swapstatus.Done
		case "punished":
			punish.State, reclaim.State = swapstatus.Done, swapstatus.Skipped
		}
		steps = append(steps, punish, reclaim)
	}
	return steps, v.bobNext()
}

func (v statusView) bobNext() string {
	st, c := v.st, v.cfg
	h := func(x uint64) string { return strconv.FormatUint(x, 10) }
	switch st.Phase {
	case "", "setup", "verified_pi_a":
		return "Your xmrswap and the taker's are agreeing the terms and checking each other's proofs. Nothing is locked yet."
	case "dero_locking":
		return "Your xmrswap is locking your DERO for the taker. It waits for each attempt to settle, so it never locks twice."
	case "dero_locked", "stipend_sending":
		return "Your DERO is locked. Once the lock is stable your xmrswap sends the taker a little DERO for her fees, and then she locks her XMR."
	case "watching_xmr":
		return "Waiting for the taker's XMR at the joint address: at " + h(c.Confirmations) + " confirmations your xmrswap hands her the gate secret. If it has not come by t1 (DERO block " + h(st.Lock.T1) + "), it refunds your DERO."
	case "xmr_confirmed", "gate_sending":
		return "The XMR is at the joint address with enough confirmations: your xmrswap is handing the taker the gate secret."
	case "gate_sent":
		return "The taker has the gate secret and claims the DERO before t1 (DERO block " + h(st.Lock.T1) + "). Her Claim puts her key share on the chain, and your xmrswap then sweeps the XMR."
	case "claimed":
		return "The taker has claimed the DERO. Once her Claim is stable your xmrswap sweeps the XMR to your wallet."
	case "sweep_ready", "sweeping":
		if v.fee > 0 {
			return "Your xmrswap is sweeping the XMR from the joint address to your wallet, less the 0.5% fee."
		}
		return "Your xmrswap is sweeping the XMR from the joint address to your wallet."
	case "swept":
		return "Nothing: the XMR has been swept to your wallet."
	case "sweep_unknown":
		return "The XMR is yours, but the sweep did not finish: sweep it by hand with the key in your state file (docs/XMR_SWAP.md §7)."
	case "refund_window":
		return "The taker did not claim: your xmrswap refunds your DERO from t1 (DERO block " + h(st.Lock.T1) + ") and before the refund cutoff (DERO block " + h(refundCutoff(st.Lock.T2)) + ")."
	case "refunding":
		return "Your xmrswap is sending the Refund of your DERO."
	case "refund_confirming":
		return "Your xmrswap has refunded your DERO and is waiting for the Refund to be stable."
	case "refunded":
		return "Nothing: your DERO is back. The key share your Refund made public lets the taker recover any XMR she locked."
	case "punish_window":
		return "The refund cutoff passed with the lock open. Until t2 + g2 (DERO block " + h(st.Lock.T2+st.Contract.G2) + ") anyone may Punish it, which pays the taker your DERO; if nobody does, your xmrswap reclaims it then."
	case "reclaiming":
		return "Your xmrswap is sending the Reclaim of your DERO."
	case "reclaim_confirming":
		return "Your xmrswap has reclaimed your DERO and is waiting for the Reclaim to be stable."
	case "reclaimed":
		return "Nothing: your DERO is back by Reclaim. The key share it made public lets the taker recover any XMR she locked."
	case "punished":
		return "Nothing more: the lock was punished, and your DERO went to the taker."
	case "refused":
		return "Nothing was locked: the swap was refused before your DERO moved. Your xmrswap's own output says why."
	}
	return ""
}

// aliceStage is where the taker's happy path stands: 0 setting up, then the maker's lock, his stipend, her XMR, the
// gate, her Claim, 6 once it is stable; -1 on an exit path or refused.
var aliceStage = map[string]int{"": 0, "setup": 0, "offer_checked": 0, "verified_pi_b": 1, "lock_announced": 1, "saw_dero_lock": 2,
	"xmr_locking": 3, "xmr_locked": 4, "gate_received": 5, "claiming": 5, "claim_confirming": 5, "claimed": 6}

func (v statusView) aliceSteps() ([]swapstatus.Step, string) {
	st, c := v.st, v.cfg
	steps := []swapstatus.Step{
		step("dero_lock", "The maker locks "+v.dero+" for you", swapstatus.DERO, ""),
		step("stipend", "The maker sends you the DERO for your fees", swapstatus.DERO, ""),
		step("xmr_lock", "You lock "+v.xmr+" at the joint address", swapstatus.Monero, ""),
		step("gate", "The maker hands you his gate secret", swapstatus.Monero, ""),
		step("claim", "Your xmrswap claims the "+v.dero, swapstatus.DERO, ""),
	}
	lock, stip, xmr, gate, claim := &steps[0], &steps[1], &steps[2], &steps[3], &steps[4]
	if st.Lock.SeenHeight > 0 {
		lock.Conf, lock.Need = swapstatus.Depth(v.tip, st.Lock.SeenHeight), swapstatus.U(c.StableBlocks)
	}
	stip.TxID = swapstatus.Hex64(st.Stipend.Tx)
	if st.Stipend.Height > 0 {
		stip.Conf, stip.Need = swapstatus.Depth(v.tip, st.Stipend.Height), stableNeed
	}
	xmr.TxID = swapstatus.Hex64(st.Xmr.LockTx)
	claim.TxID = swapstatus.Hex64(st.ClaimTx)
	if st.Lock.ClaimSeenHeight > 0 {
		claim.Conf, claim.Need = swapstatus.Depth(v.tip, st.Lock.ClaimSeenHeight), stableNeed
	}
	xmrLocked := st.Xmr.LockTx != "" || st.Xmr.LockUnknown
	if k, happy := aliceStage[st.Phase]; happy {
		for i := range steps {
			steps[i].State = ladder(i, k-1)
		}
		if k == 2 {
			// one phase waits out the lock's stable depth, then the stipend, then the witnesses
			lock.State, stip.State = swapstatus.Active, swapstatus.Waiting
			if v.tip > 0 && v.tip >= st.Lock.SeenHeight+c.StableBlocks {
				lock.State, stip.State = swapstatus.Done, swapstatus.Active
			}
		}
		if k <= 3 {
			xmr.Deadline = v.at(st.Lock.T1)
		}
		if k <= 4 {
			gate.Deadline = v.at(deadlineOf(st.Lock.T1, c.GateDeadline))
		}
		if k <= 5 && st.Lock.T1 > 0 {
			claim.Deadline = v.at(claimCutoff(st.Lock.T1, c.ClaimMargin))
		}
		return steps, v.aliceNext()
	}
	for i := range steps {
		steps[i].State = swapstatus.Skipped
	}
	if st.Phase == "refused" {
		// refused at the lock (it was announced, and failed a check), or before any lock was announced
		if st.Consumed[KindDeroLocked] {
			lock.State = swapstatus.Failed
		}
		return steps, v.aliceNext()
	}
	// an exit: what happened before it, then the exits that remain or were taken
	if st.Lock.SeenHeight > 0 {
		lock.State = swapstatus.Done
	}
	if st.Stipend.Tx != "" {
		stip.State = swapstatus.Done
	}
	if xmrLocked {
		xmr.State, gate.State = swapstatus.Done, swapstatus.Failed
		if st.Gate != "" {
			gate.State, claim.State = swapstatus.Done, swapstatus.Failed
		}
	}
	refund := step("refund", "The maker takes his DERO back (Refund from t1, or Reclaim from t2 + g2)", swapstatus.DERO, swapstatus.Skipped)
	recover := step("recover", "Your xmrswap sweeps your "+v.xmr+" back from the joint address", swapstatus.Monero, swapstatus.Waiting)
	punish := step("punish", "Your xmrswap punishes, which pays you the maker's "+v.dero+", from t2", swapstatus.DERO, swapstatus.Skipped)
	punish.TxID = swapstatus.Hex64(st.PunishTx)
	if st.Lock.PunishSeenHeight > 0 {
		punish.Conf, punish.Need = swapstatus.Depth(v.tip, st.Lock.PunishSeenHeight), stableNeed
	}
	if len(st.Sweep.Txs) > 0 {
		recover.TxID = swapstatus.Hex64(st.Sweep.Txs[0])
	}
	g2 := st.Contract.G2
	switch st.Phase {
	case "await_refund":
		refund.State, punish.State = swapstatus.Active, swapstatus.Waiting
		refund.Deadline, punish.Deadline = v.at(refundCutoff(st.Lock.T2)), v.at(st.Lock.T2)
	case "refund_seen", "sweep_ready", "sweeping", "swept", "sweep_unknown", "aborted":
		refund.State, recover.State = swapstatus.Done, swapstatus.Active
		switch st.Phase {
		case "swept":
			recover.State = swapstatus.Done
		case "sweep_unknown":
			recover.State = swapstatus.Failed
		}
	case "punish_window", "punishing", "punish_confirming":
		punish.State = swapstatus.Active
		if st.Phase == "punish_window" && v.tip > 0 && v.tip < st.Lock.T2 {
			punish.State = swapstatus.Waiting
		}
		if g2 > 0 {
			punish.Deadline = v.at(punishCutoff(st.Lock.T2, g2, c.ClaimMargin))
		}
		recover.State = swapstatus.Skipped
	case "punished":
		punish.State, recover.State = swapstatus.Done, swapstatus.Skipped
	case "await_reclaim":
		refund.State, punish.State = swapstatus.Active, swapstatus.Failed
		refund.Label = "The maker reclaims his DERO, from t2 + g2: nothing makes him"
		if g2 > 0 {
			refund.Deadline = v.at(st.Lock.T2 + g2)
		}
	}
	steps = append(steps, refund)
	if xmrLocked {
		steps = append(steps, recover)
	}
	return append(steps, punish), v.aliceNext()
}

func (v statusView) aliceNext() string {
	st, c := v.st, v.cfg
	h := func(x uint64) string { return strconv.FormatUint(x, 10) }
	switch st.Phase {
	case "", "setup", "offer_checked", "verified_pi_b":
		return "Your xmrswap and the maker's are agreeing the terms and checking each other's proofs. Nothing is locked yet."
	case "lock_announced":
		return "The maker has announced his DERO lock: your xmrswap is checking it on the chain."
	case "saw_dero_lock":
		return "The maker's DERO lock checks out. Once it is stable and his DERO for your fees has arrived, your xmrswap locks your XMR at the joint address."
	case "xmr_locking":
		return "Your xmrswap is locking your XMR at the joint address."
	case "xmr_locked":
		return "Your XMR is at the joint address. At enough confirmations the maker hands you his gate secret; if it has not come by DERO block " + h(deadlineOf(st.Lock.T1, c.GateDeadline)) + " (t1 − G), your xmrswap waits for his refund."
	case "gate_received", "claiming":
		return "Your xmrswap has the gate secret and is claiming the DERO before the claim cutoff (DERO block " + h(claimCutoff(st.Lock.T1, c.ClaimMargin)) + ")."
	case "claim_confirming":
		return "Your Claim is in: your xmrswap is waiting for it to be stable."
	case "claimed":
		return "Nothing: the DERO is yours. The maker sweeps the XMR, and the 0.5% fee is paid from his share."
	case "await_refund":
		return "No Claim this time. From t1 (DERO block " + h(st.Lock.T1) + ") the maker can refund his DERO, which lets your xmrswap take your XMR back; if he has not by t2 (DERO block " + h(st.Lock.T2) + "), your xmrswap punishes."
	case "refund_seen", "sweep_ready", "sweeping":
		return "The maker took his DERO back, and his key share is on the chain: your xmrswap is sweeping your XMR back to your wallet."
	case "swept":
		return "Nothing: your XMR is back in your wallet."
	case "sweep_unknown":
		return "Your XMR is still at the joint address: the sweep did not finish. Sweep it by hand with the key in your state file (docs/XMR_SWAP.md §7)."
	case "aborted":
		return "Nothing: the maker took his DERO back before you locked any XMR."
	case "punish_window":
		return "The maker did not refund. From t2 (DERO block " + h(st.Lock.T2) + ") your xmrswap punishes, which pays you his DERO, before the punish cutoff (DERO block " + h(punishCutoff(st.Lock.T2, st.Contract.G2, c.ClaimMargin)) + ")."
	case "punishing":
		return "Your xmrswap is sending the Punish."
	case "punish_confirming":
		return "Your xmrswap has punished and is waiting for the Punish to be stable."
	case "punished":
		return "Nothing more: the maker's DERO is yours by Punish. Any XMR you locked stays at the joint address."
	case "await_reclaim":
		return "Past the punish cutoff: your XMR comes back only once the maker reclaims his DERO, which puts his key share on the chain. Nothing makes him."
	case "refused":
		return "Nothing of yours moved: the swap was refused before your XMR was locked. Your xmrswap's own output says why."
	}
	return ""
}
