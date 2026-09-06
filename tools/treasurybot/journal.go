package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// The journal closes the crash window between ClaimFees and Deposit: fees claimed from a market are owed to
// the Founders Pass at `share` bps, and a bot that died after claiming and before depositing used to forget
// them forever. Every claim is recorded before it is sent and every deposit before it is sent; on start the
// bot reconciles each open record against the chain (a market's cumulative fc, the pass's cumulative dep) and
// finishes what it started. Written with fsync + rename; a corrupt journal stops the bot.
//
// What counts as done is what the contracts counted, not what the wallet returned: a claim is "claimed" only
// once the market's fc grew, and the units it owes are that growth; a deposit is closed only once the pass's dep
// grew by its units. A transaction that was included but not executed leaves both counters where they were.

type claim struct {
	SCID   string `json:"scid"`
	FD     uint64 `json:"fd"`             // units claimed: the fd read before the send, replaced by the fc growth once proven
	Before uint64 `json:"before"`         // the market's cumulative fc counter before the claim was sent
	TxID   string `json:"txid,omitempty"` // set once the claim tx is confirmed
	State  string `json:"state"`          // claiming | claimed
}

type deposit struct {
	Units  uint64 `json:"units"`  // share owed for the claims below
	Base   uint64 `json:"base"`   // sum of the claims' fd this deposit covers
	Before uint64 `json:"before"` // the pass's dep counter before the deposit was sent
	TxID   string `json:"txid,omitempty"`
}

type journal struct {
	Claims  []claim  `json:"claims"`            // claimed, not yet covered by a confirmed deposit
	Deposit *deposit `json:"deposit,omitempty"` // a deposit in flight
	path    string
}

func loadJournal(path string) (*journal, error) {
	j := &journal{path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return j, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, j); err != nil {
		return nil, fmt.Errorf("journal %s is corrupt (%v): refusing to run", path, err)
	}
	for _, c := range j.Claims {
		if c.SCID == "" || (c.State != "claiming" && c.State != "claimed") {
			return nil, fmt.Errorf("journal %s has an unusable claim record %+v: refusing to run", path, c)
		}
	}
	return j, nil
}

func (j *journal) save() error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := j.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, j.path)
}

// chain is what reconcile needs from the DERO side, narrowed so a test can fake it.
type chain interface {
	FD(scid string) uint64 // a market's unclaimed fee balance
	FC(scid string) uint64 // a market's cumulative claimed-fee counter (only ClaimFees moves it, only upward)
	PassDep() uint64       // the pass's cumulative dep counter
	SendClaim(scid string) (string, error)
	SendDeposit(units uint64) (string, error)
}

// owed returns the fd total of confirmed claims not yet covered by a deposit.
func (j *journal) owed() uint64 {
	var s uint64
	for _, c := range j.Claims {
		if c.State == "claimed" {
			s += c.FD
		}
	}
	return s
}

// claimedUnits is what one claim record actually moved: the fc growth since its Before, bounded by the next
// record for the same market (its Before), so two sequential claims never count the same growth twice. A record
// whose growth is below the fd it was sent for never landed (the counter only moves by ClaimFees, and one claim
// takes every unit of fd there is).
func (j *journal) claimedUnits(ch chain, i int) (uint64, bool) {
	c := j.Claims[i]
	end := ch.FC(c.SCID)
	for _, later := range j.Claims[i+1:] {
		if later.SCID == c.SCID && later.Before < end {
			end = later.Before
		}
	}
	if end < c.Before+c.FD {
		return 0, false
	}
	return end - c.Before, true
}

// reconcile finishes what a previous run left open. A "claiming" record is judged by the market's cumulative
// fc counter: if it grew by at least the recorded fd the claim landed and is kept as claimed for that growth,
// otherwise it never landed and is dropped. (The unclaimed fd balance is no evidence: new fees can refill it
// between a crash and the restart.) A deposit in flight whose units already reached the pass (dep counter grew
// by at least Units) is closed; otherwise it is sent again.
func (j *journal) reconcile(ch chain) error {
	kept := j.Claims[:0]
	for i, c := range j.Claims {
		if c.State == "claiming" {
			units, landed := j.claimedUnits(ch, i)
			if !landed {
				continue
			}
			c.FD, c.State = units, "claimed"
		}
		kept = append(kept, c)
	}
	j.Claims = kept
	if err := j.save(); err != nil {
		return err
	}
	if j.Deposit != nil {
		if ch.PassDep() >= j.Deposit.Before+j.Deposit.Units {
			j.finishDeposit()
			return j.save()
		}
		return j.sendDeposit(ch)
	}
	return nil
}

// claimMarket records the claim, sends it, and records it as claimed only once the market's fc proves it.
func (j *journal) claimMarket(ch chain, scid string, fd uint64) error {
	j.Claims = append(j.Claims, claim{SCID: scid, FD: fd, Before: ch.FC(scid), State: "claiming"})
	if err := j.save(); err != nil {
		j.Claims = j.Claims[:len(j.Claims)-1]
		return err
	}
	txid, err := ch.SendClaim(scid)
	if err != nil {
		return err // the record stays "claiming"; reconcile decides on the next start
	}
	i := len(j.Claims) - 1
	units, landed := j.claimedUnits(ch, i)
	if !landed { // included but not executed: nothing moved, so there is nothing to owe
		j.Claims = j.Claims[:i]
		if err := j.save(); err != nil {
			return err
		}
		return fmt.Errorf("claim tx %s was included but the market's fc did not move: not counted", short(txid))
	}
	c := &j.Claims[i]
	c.FD, c.TxID, c.State = units, txid, "claimed"
	return j.save()
}

// depositShare converts every confirmed claim into one deposit of share bps. Claims whose share rounds to zero
// units stay in the journal and join the next deposit.
func (j *journal) depositShare(ch chain, share uint64) error {
	if j.Deposit != nil {
		return errors.New("a deposit is already in flight: reconcile first")
	}
	base := j.owed()
	if base == 0 {
		return nil
	}
	units := (base/10000)*share + ((base%10000)*share)/10000
	if units == 0 {
		return nil
	}
	j.Deposit = &deposit{Units: units, Base: base, Before: ch.PassDep()}
	if err := j.save(); err != nil {
		j.Deposit = nil
		return err
	}
	return j.sendDeposit(ch)
}

// sendDeposit sends the deposit in flight and closes it only once the pass's dep counter proves it landed.
func (j *journal) sendDeposit(ch chain) error {
	txid, err := ch.SendDeposit(j.Deposit.Units)
	if err != nil {
		return err // the deposit record stays; reconcile resends or closes it on the next start
	}
	if ch.PassDep() < j.Deposit.Before+j.Deposit.Units {
		return fmt.Errorf("deposit tx %s was included but the pass's dep counter did not grow by %d: the record stays open for the next start", short(txid), j.Deposit.Units)
	}
	j.Deposit.TxID = txid
	j.finishDeposit()
	return j.save()
}

func short(txid string) string {
	if len(txid) > 8 {
		return txid[:8] + "…"
	}
	return txid
}

func (j *journal) finishDeposit() {
	// the deposit covered every claim that was confirmed when it was created; claims confirmed since stay
	var rest []claim
	covered := j.Deposit.Base
	for _, c := range j.Claims {
		if c.State == "claimed" && covered >= c.FD {
			covered -= c.FD
			continue
		}
		rest = append(rest, c)
	}
	j.Claims = rest
	j.Deposit = nil
}
