package zecswap

import (
	"strconv"
	"time"

	"veil/tools/swapstatus"
)

// StatusV1 is this swap as the version 1 status answer of docs/SWAP_STATUS.md, which `-status` serves to the app on the
// same machine. network is the DERO network as the app names it (simulator, testnet or mainnet), which the command line
// reads from the daemon; deroTip is the DERO height the companion last read, 0 when it has read none. The Zcash side is
// the state's own: the highest tip this swap has read, the funding and the claim as last seen mined. It reads nothing
// and invents nothing: a value the companion does not know is left out.
//
// It carries no secret: never the maker's secret (public only once his claim is mined, and not taken from here even
// then), never the taker's copy of it, never a key, never an Outcome or Note sentence (which can quote an error);
// statusv1_test.go runs every phase of both machines and looks for them in every answer.
//
// Call it from the goroutine that runs Step: it reads the state the machine is writing.
func (s *Swap) StatusV1(network string, deroTip uint64, now time.Time) swapstatus.Payload {
	return statusV1(s.st, s.cfg, network, deroTip, now)
}

func statusV1(st *State, cfg Config, network string, deroTip uint64, now time.Time) swapstatus.Payload {
	pol := cfg.Policy
	if pol == (Policy{}) {
		pol = DefaultPolicy()
	}
	p := swapstatus.Payload{V: swapstatus.Version, Program: "zecswap", Role: st.Role, Direction: swapstatus.Direction(st.Role),
		Network: network, Contract: st.Escrow, Phase: st.Phase, Updated: now.UTC().Format(time.RFC3339)}
	if p.Contract == "" {
		p.Contract = cfg.Escrow
	}
	asset, give, want := st.GiveAsset, st.GiveAmount, st.WantAmount
	if st.Role == RoleMaker && give == 0 && want == 0 {
		asset, give, want = cfg.GiveAsset, cfg.GiveAmount, cfg.WantAmount
	}
	p.Asset = asset
	if give > 0 {
		p.DeroAmount = strconv.FormatUint(give, 10)
	}
	chain := st.ZcashChain
	if chain == "" {
		chain = cfg.ZcashChain
	}
	// DMZ's swap fee (fee.go): 0.5 % of the funding, the second output of the maker's claim, unless it would be dust;
	// none where no treasury is recorded. Taken from the treasury fixed at setup, as the claim takes it.
	fee := uint64(0)
	if want > 0 {
		p.ForeignAmount = strconv.FormatUint(want, 10)
		treasury, value := st.Treasury, want
		if treasury == "" {
			treasury = cfg.Treasury
		}
		if st.Funding != nil && st.Funding.Value > 0 {
			value = st.Funding.Value
		}
		p.Fee = "0"
		if treasury != "" {
			p.Fee = "" // unknown until the address is read on this swap's network
			if spk, err := TreasuryScript(treasury, netOf(chain)); err == nil {
				if o, ok := TreasuryOutput(value, spk); ok {
					fee = o.Value
				}
				p.Fee = strconv.FormatUint(fee, 10)
			}
		}
	}
	if st.HaveEscrow {
		p.Lock = swapstatus.U(st.EscrowID)
	}
	v := zview{st: st, pol: pol, tip: deroTip, ztip: uint64(st.MaxZecTip), now: now, rated: network != "simulator", zrated: chain != "regtest" && chain != "",
		fee: fee, dero: giveWords(asset, give), zec: swapstatus.Units(want, 8) + " ZEC"}
	if st.Role == RoleMaker {
		p.Steps, p.Next = v.makerSteps()
	} else {
		p.Steps, p.Next = v.takerSteps()
	}
	if st.Done {
		p.Outcome = outcomeV1(st.Phase)
	}
	for i := range p.Steps {
		if p.Steps[i].State != swapstatus.Active && p.Steps[i].State != swapstatus.Waiting {
			p.Steps[i].Deadline = nil
		}
	}
	return p
}

// outcomeV1 is an ended swap in the contract's words. The contract has no word for a swap that ended with nothing
// locked or funded (cancelled, expired, refused, walked, declined, taken, a funding that expired unmined): each side
// has what it started with, so it is "refunded", and the answer's next sentence says that nothing moved. A maker who
// claimed the ZEC and had his DERO refunded as well is "refunded" too, and his next sentence says he has both.
func outcomeV1(phase string) string {
	switch phase {
	case "swapped":
		return "swapped"
	case "lost":
		return "lost"
	}
	return "refunded"
}

func giveWords(asset string, amount uint64) string {
	if asset == "" {
		return swapstatus.Units(amount, 5) + " DERO"
	}
	short := asset
	if len(short) > 8 {
		short = short[:8]
	}
	return strconv.FormatUint(amount, 10) + " units of token " + short
}

type zview struct {
	st            *State
	pol           Policy
	tip, ztip     uint64
	now           time.Time
	rated, zrated bool
	fee           uint64
	dero, zec     string
}

func (v zview) at(h uint64) *swapstatus.Deadline {
	if h == 0 {
		return nil
	}
	return swapstatus.At(swapstatus.DERO, h, v.tip, v.rated, v.now)
}

func (v zview) zat(h uint64) *swapstatus.Deadline {
	if h == 0 {
		return nil
	}
	return swapstatus.At(swapstatus.Zcash, h, v.ztip, v.zrated, v.now)
}

func zstep(id, label, chain string) swapstatus.Step {
	return swapstatus.Step{ID: id, Label: label, Chain: chain, State: swapstatus.Skipped}
}

// zladder is each step's state with the step at index k running: done below, active at, waiting above.
func zladder(i, k int) string {
	switch {
	case i < k:
		return swapstatus.Done
	case i == k:
		return swapstatus.Active
	}
	return swapstatus.Waiting
}

// claimCutoffHeight is the first Zcash height at which the maker sends no claim (Policy.ClaimOpen).
func (v zview) claimCutoffHeight() uint64 {
	if v.st.CLTV == 0 || uint64(v.st.CLTV) <= uint64(v.pol.ClaimCutoff)+1 {
		return 0
	}
	return uint64(v.st.CLTV) - uint64(v.pol.ClaimCutoff) - 1
}

var makerStage = map[string]int{"": 0, "posting": 0, "posted": 1, "locking": 2, "locked": 3, "claiming": 4, "claimed": 4, "swapped": 6}

func (v zview) makerSteps() ([]swapstatus.Step, string) {
	st := v.st
	claimLabel := "You claim the " + v.zec + " with your secret"
	if v.fee > 0 {
		claimLabel = "You claim the " + v.zec + ", less the 0.5% fee, with your secret"
	}
	steps := []swapstatus.Step{
		zstep("offer", "You post the offer: "+v.dero+" for "+v.zec, swapstatus.DERO),
		zstep("accept", "A taker accepts it", swapstatus.DERO),
		zstep("dero_lock", "You lock "+v.dero+" in the escrow", swapstatus.DERO),
		zstep("zec_fund", "The taker funds the Zcash script with "+v.zec, swapstatus.Zcash),
		zstep("zec_claim", claimLabel, swapstatus.Zcash),
		zstep("dero_claim", "The taker claims the "+v.dero+" with the secret your claim made public", swapstatus.DERO),
	}
	offer, accept, lock, fund, claim, dclaim := &steps[0], &steps[1], &steps[2], &steps[3], &steps[4], &steps[5]
	offer.TxID = swapstatus.Hex64(st.DeroTxs["post"])
	lock.TxID = swapstatus.Hex64(st.DeroTxs["lock"])
	dclaim.TxID = swapstatus.Hex64(st.DeroTxs["relay"])
	need := swapstatus.U(uint64(v.pol.ConfZec))
	if f := st.Funding; f != nil {
		fund.TxID, fund.Conf, fund.Need = swapstatus.Hex64(f.TxID), swapstatus.Confirmations(v.ztip, uint64(f.Height)), need
	} else if f := st.Sighted; f != nil {
		fund.TxID, fund.Conf, fund.Need = swapstatus.Hex64(f.TxID), swapstatus.Confirmations(v.ztip, uint64(f.Height)), need
	} else {
		fund.Need = need
	}
	claim.TxID = swapstatus.Hex64(st.ClaimTxID)
	if st.ClaimHeight > 0 {
		claim.Conf, claim.Need = swapstatus.Confirmations(v.ztip, uint64(st.ClaimHeight)), need
	}
	accept.Deadline, lock.Deadline = v.at(st.OfferExpiry), v.at(st.LockDeadline)
	fund.Deadline, claim.Deadline = v.zat(v.claimCutoffHeight()), v.zat(v.claimCutoffHeight())
	dclaim.Deadline = v.at(st.TDero)
	deep := st.ClaimTxID != "" && st.SpendTxID == st.ClaimTxID
	if k, happy := makerStage[st.Phase]; happy {
		if st.Phase == "claimed" && deep {
			k = 5
		}
		for i := range steps {
			steps[i].State = zladder(i, k)
		}
		return steps, v.makerNext()
	}
	if st.HaveOffer {
		offer.State = swapstatus.Done
	}
	if st.AcceptHeight > 0 {
		accept.State = swapstatus.Done
	}
	if st.HaveEscrow {
		lock.State = swapstatus.Done
	} else if st.AcceptHeight > 0 {
		lock.State = swapstatus.Failed
	}
	if st.Funding != nil {
		fund.State = swapstatus.Done
	}
	if st.ClaimTxID != "" {
		claim.State = swapstatus.Failed
		if deep {
			claim.State = swapstatus.Done
		}
	}
	switch st.Phase {
	case "refused":
		offer.State = swapstatus.Failed
	case "expired":
		accept.State = swapstatus.Failed
	case "lost":
		dclaim.State = swapstatus.Done
	}
	switch st.Phase {
	case "refund_wait", "dero_refunding", "refunded", "claimed_refunded":
		r := zstep("dero_refund", "Your zecswap refunds your DERO, from T_dero", swapstatus.DERO)
		r.TxID, r.Deadline = swapstatus.Hex64(st.DeroTxs["refund"]), v.at(st.TDero)
		switch st.Phase {
		case "refund_wait":
			r.State = swapstatus.Active
			if v.tip > 0 && v.tip < st.TDero {
				r.State = swapstatus.Waiting
			}
		case "dero_refunding":
			r.State = swapstatus.Active
		default:
			r.State = swapstatus.Done
		}
		steps = append(steps, r)
	case "cancel_wait", "cancelled":
		c := zstep("cancel", "Your zecswap cancels the offer", swapstatus.DERO)
		c.TxID, c.State = swapstatus.Hex64(st.DeroTxs["cancel"]), swapstatus.Active
		if st.Phase == "cancelled" {
			c.State = swapstatus.Done
		}
		steps = append(steps, c)
	}
	return steps, v.makerNext()
}

func (v zview) makerNext() string {
	st := v.st
	h := func(x uint64) string { return strconv.FormatUint(x, 10) }
	switch st.Phase {
	case "":
		return "Your zecswap is setting up the offer. Nothing is posted yet."
	case "posting":
		return "Your zecswap is posting the offer on the board."
	case "posted":
		return "Your offer is on the board, waiting for a taker until DERO block " + h(st.OfferExpiry) + ". Nothing is locked yet."
	case "locking":
		return "A taker accepted: your zecswap is locking your DERO in the escrow, before DERO block " + h(st.LockDeadline) + "."
	case "locked":
		return "Your DERO is in the escrow. Waiting for the taker to fund the Zcash script: at " + h(uint64(v.pol.ConfZec)) + " confirmations your zecswap claims the ZEC, before Zcash block " + h(v.claimCutoffHeight()) + "."
	case "claiming":
		return "The funding is deep enough: your zecswap is claiming the ZEC, which makes your secret public."
	case "claimed":
		if st.ClaimTxID != "" && st.SpendTxID == st.ClaimTxID {
			return "The ZEC is yours. The taker's DERO Claim is due before T_dero (DERO block " + h(st.TDero) + "); your zecswap sends it for her if she is away."
		}
		return "Your claim of the ZEC is confirming. Once it is in, the taker's zecswap claims the DERO with the secret it made public."
	case "swapped":
		return "Nothing: the ZEC is yours, and the taker has the DERO."
	case "refund_wait":
		return "No ZEC was claimed in time: your DERO comes back by Refund from T_dero (DERO block " + h(st.TDero) + ")."
	case "dero_refunding":
		return "Your zecswap is refunding your DERO from the escrow."
	case "refunded":
		return "Nothing: your DERO is back."
	case "claimed_refunded":
		return "Nothing: you claimed the ZEC, and the DERO came back to you as well, because the taker never claimed it."
	case "cancel_wait":
		return "Nothing is locked: your zecswap cancels the offer as soon as the board allows it."
	case "cancelled":
		return "Nothing: the offer is cancelled, and nothing was locked."
	case "expired":
		return "Nothing: the offer expired with no taker, and nothing was locked."
	case "refused":
		return "Nothing was posted or locked: the swap was refused at the start. Your zecswap's own output says why."
	case "lost":
		return "The swap is lost: a Zcash reorganisation took the funding your claim spent off the chain, after the taker had claimed the DERO."
	}
	return ""
}

var takerStage = map[string]int{"": 0, "accepting": 0, "accepted": 1, "lock_seen": 2, "funding": 2, "funded": 3, "secret_seen": 4, "dero_claimed": 4, "swapped": 5}

func (v zview) takerSteps() ([]swapstatus.Step, string) {
	st := v.st
	steps := []swapstatus.Step{
		zstep("accept", "You accept the offer", swapstatus.DERO),
		zstep("dero_lock", "The maker locks "+v.dero+" in the escrow", swapstatus.DERO),
		zstep("zec_fund", "You fund the Zcash script with "+v.zec, swapstatus.Zcash),
		zstep("zec_claim", "The maker claims the ZEC with his secret, before cltv", swapstatus.Zcash),
		zstep("dero_claim", "Your zecswap claims the "+v.dero+" with the secret", swapstatus.DERO),
	}
	accept, lock, fund, claim, dclaim := &steps[0], &steps[1], &steps[2], &steps[3], &steps[4]
	accept.TxID = swapstatus.Hex64(st.DeroTxs["accept"])
	fund.TxID, fund.Need = swapstatus.Hex64(st.FundingTxID), swapstatus.U(uint64(v.pol.ConfZec))
	if st.Funding != nil {
		fund.Conf = swapstatus.Confirmations(v.ztip, uint64(st.Funding.Height))
	}
	dclaim.TxID = swapstatus.Hex64(st.DeroTxs["claim"])
	accept.Deadline, dclaim.Deadline = v.at(st.OfferExpiry), v.at(st.TDero)
	if st.AcceptHeight > 0 {
		lock.Deadline = v.at(st.AcceptHeight + DeroTxValidity + v.pol.PLock)
	}
	fund.Deadline, claim.Deadline = v.zat(uint64(st.FundingExpiry)), v.zat(uint64(st.CLTV))
	if k, happy := takerStage[st.Phase]; happy {
		for i := range steps {
			steps[i].State = zladder(i, k)
		}
		if k >= 4 {
			claim.TxID = swapstatus.Hex64(st.SpendTxID) // the maker's claim, from which the secret was read
		}
		if st.Phase == "lock_seen" {
			fund.Deadline = nil // the funding's expiry is set when it is built
		}
		return steps, v.takerNext()
	}
	if st.AcceptHeight > 0 {
		accept.State = swapstatus.Done
	}
	if st.HaveEscrow {
		lock.State = swapstatus.Done
	}
	switch st.Phase {
	case "refunding", "refunded", "lost":
		fund.State = swapstatus.Done
	}
	switch st.Phase {
	case "taken":
		accept.State = swapstatus.Failed
	case "walked":
		lock.State = swapstatus.Failed
	case "declined":
		switch {
		case st.AcceptHeight == 0:
			accept.State = swapstatus.Failed
		case !st.HaveEscrow:
			lock.State = swapstatus.Failed
		}
	case "funding_expired":
		fund.State = swapstatus.Failed
	case "lost":
		claim.State, claim.TxID, dclaim.State = swapstatus.Done, swapstatus.Hex64(st.SpendTxID), swapstatus.Failed
	case "refunding", "refunded":
		r := zstep("zec_refund", "Your zecswap takes your ZEC back, from cltv", swapstatus.Zcash)
		r.Deadline = v.zat(uint64(st.CLTV))
		switch {
		case st.Phase == "refunded":
			r.State, r.TxID = swapstatus.Done, swapstatus.Hex64(st.SpendTxID)
		case v.ztip > 0 && v.ztip < uint64(st.CLTV):
			r.State = swapstatus.Waiting
		default:
			r.State = swapstatus.Active
		}
		d := zstep("dero_refund", "The maker takes his DERO back, from T_dero", swapstatus.DERO)
		d.State, d.Deadline = swapstatus.Waiting, v.at(st.TDero)
		steps = append(steps, r, d)
	}
	return steps, v.takerNext()
}

func (v zview) takerNext() string {
	st := v.st
	h := func(x uint64) string { return strconv.FormatUint(x, 10) }
	switch st.Phase {
	case "", "accepting":
		return "Your zecswap is accepting the offer on the board."
	case "accepted":
		return "You accepted: waiting for the maker to lock his DERO, until DERO block " + h(st.AcceptHeight+DeroTxValidity+v.pol.PLock) + ". Nothing of yours is funded."
	case "lock_seen":
		return "The maker's DERO is in the escrow and checks out: your zecswap funds the Zcash script."
	case "funding":
		return "Your zecswap has sent your funding of the Zcash script; it is mined before Zcash block " + h(uint64(st.FundingExpiry)) + ", or it expires unspent."
	case "funded":
		return "Your ZEC is in the script. The maker claims it with his secret before cltv (Zcash block " + h(uint64(st.CLTV)) + "); from cltv your zecswap takes it back."
	case "secret_seen":
		return "The maker claimed the ZEC, and his secret is public: your zecswap is claiming the DERO before T_dero (DERO block " + h(st.TDero) + ")."
	case "dero_claimed":
		return "Your DERO Claim is in: your zecswap is waiting for it to be stable."
	case "swapped":
		return "Nothing: the DERO is yours."
	case "refunding":
		return "No claim came: your zecswap takes your ZEC back from cltv (Zcash block " + h(uint64(st.CLTV)) + "), until the refund is deep."
	case "refunded":
		return "Nothing: your ZEC is back at your refund address. The maker's DERO goes back to him from T_dero."
	case "walked":
		return "Nothing of yours moved: the maker did not lock his DERO in time."
	case "declined":
		return "Nothing of yours was funded: the offer or the maker's lock did not pass your zecswap's checks. Its own output says why."
	case "taken":
		return "Nothing of yours moved: another taker accepted the offer first."
	case "funding_expired":
		return "Nothing was spent: your funding was never mined, and it has expired."
	case "lost":
		return "The swap is lost: the maker claimed the ZEC, and the DERO went back to him before your Claim landed."
	}
	return ""
}
