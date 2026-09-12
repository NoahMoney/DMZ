package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

// The journal closes the crash window between ClaimFees and Deposit, per asset. founders.bas v2 keeps one reward pool
// per asset, and a fee is paid to the passes in the asset it was taken in (the operator's ruling of 2026-09-13). A
// market's ClaimFees sends the treasury its fees in two legs, fd in the market's quote and fa in its asset, and each
// leg is owed to the pass at `share` bps in that same asset. Every claim is recorded before it is sent and every
// deposit before it is sent; on start the bot reconciles each open record against the chain and finishes what it
// started. Written with fsync + rename; a corrupt journal stops the bot.
//
// What counts as done is what the contracts counted, not what the wallet returned:
//   - A claim is proven by the market's cumulative fc counter, which only ClaimFees moves, by the quote leg it sends.
//     The quote units owed are that growth. So a claim is sent only when the quote leg holds something (fd > 0): with
//     nothing in it, nothing on chain could prove the claim ran.
//   - The asset leg has no counter. It is counted at the fa read just before the send, which the claim moved at least:
//     fa only grows until a ClaimFees sends all of it to the treasury, whoever calls it. Asset-leg fees that arrive
//     between that read and the claim's execution reach the treasury uncounted. That is at most the fees of the blocks
//     the claim waits for, and it errs toward the treasury, never toward a deposit the claims did not earn.
//   - A deposit is closed only once the pass's per-asset counter ad_<i> grew by its units. ad_0 also counts market
//     fees, and anyone may deposit, so the test is "grew by at least".
//
// A transaction that was included but not executed moves none of these counters.
//
// While no pass is registered, v2 refuses every Deposit. The bot then holds what it owes in Owed and deposits it once a
// pass registers, in the first deposit of each asset (the operator's ruling of 2026-09-13).

// deroID is DERO's asset id as founders.bas v2 names it: 32 zero bytes, in hex.
const deroID = "0000000000000000000000000000000000000000000000000000000000000000"

// journalV is the layout this bot writes. The v1 bot's journal (claims with a state, one DERO deposit) has none.
const journalV = 2

type claim struct {
	SCID   string `json:"scid"`
	Quote  string `json:"quote"`          // the quote leg's asset id, 64 hex (deroID for DERO)
	Asset  string `json:"asset"`          // the asset leg's asset id, 64 hex
	FD     uint64 `json:"fd"`             // quote units: the fd read before the send
	FA     uint64 `json:"fa"`             // asset units: the fa read before the send, and what is counted once proven
	Before uint64 `json:"before"`         // the market's cumulative fc counter before the claim was sent
	TxID   string `json:"txid,omitempty"` // set once the claim tx is confirmed
}

type deposit struct {
	Units  uint64 `json:"units"`  // what is sent: share bps of Base
	Base   uint64 `json:"base"`   // the owed units this deposit covers
	Before uint64 `json:"before"` // the pass's ad_<i> for this asset before the send (0 while it has no index for it)
	TxID   string `json:"txid,omitempty"`
}

type journal struct {
	V        int                 `json:"v"`
	Claims   []claim             `json:"claims"`   // sent, not yet proven
	Owed     map[string]uint64   `json:"owed"`     // per asset id: claimed, not yet covered by a confirmed deposit
	Deposits map[string]*deposit `json:"deposits"` // per asset id: a deposit in flight
	path     string
}

func newJournal(path string) *journal {
	return &journal{V: journalV, Owed: map[string]uint64{}, Deposits: map[string]*deposit{}, path: path}
}

func isID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func loadJournal(path string) (*journal, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newJournal(path), nil
	}
	j := &journal{path: path} // V stays 0 unless the file says 2: a v1 journal carries no v at all
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("journal %s is corrupt (%v): refusing to run", path, err)
	}
	if err := json.Unmarshal(b, j); err != nil {
		return nil, fmt.Errorf("journal %s is corrupt (%v): refusing to run", path, err)
	}
	if j.V != journalV {
		_, v1Deposit := raw["deposit"]
		if len(j.Claims) > 0 || v1Deposit || j.V != 0 {
			return nil, fmt.Errorf("journal %s was written by the Founders Pass v1 bot (or another layout, v %d): its DERO-only records cannot be reconciled against v2's per-asset counters. Settle it with the v1 bot against the v1 pass, or move it aside once it is settled: refusing to run", path, j.V)
		}
		j.V = journalV
	}
	if j.Owed == nil {
		j.Owed = map[string]uint64{}
	}
	if j.Deposits == nil {
		j.Deposits = map[string]*deposit{}
	}
	for _, c := range j.Claims {
		if c.SCID == "" || !isID(c.Quote) || !isID(c.Asset) {
			return nil, fmt.Errorf("journal %s has an unusable claim record %+v: refusing to run", path, c)
		}
	}
	for a, d := range j.Deposits {
		if !isID(a) || d == nil || d.Units == 0 {
			return nil, fmt.Errorf("journal %s has an unusable deposit record for %q: refusing to run", path, a)
		}
	}
	for a := range j.Owed {
		if !isID(a) {
			return nil, fmt.Errorf("journal %s owes an unusable asset id %q: refusing to run", path, a)
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

// chain is what the journal needs from the DERO side, narrowed so a test can fake it.
type chain interface {
	Fees(scid string) (fd, fa uint64)                    // a market's unclaimed fees: fd in its quote, fa in its asset
	FC(scid string) uint64                               // its cumulative claimed quote-leg counter (only ClaimFees moves it, upward)
	Legs(scid string, launch bool) (quote, asset string) // the two legs' asset ids, 64 hex; "" when unreadable
	PassDep(asset string) uint64                         // the pass's ad_<i> for this asset; 0 while it has no index for it
	Registered() uint64                                  // the pass's hst: registered pass units
	SendClaim(scid string) (string, error)
	SendDeposit(asset string, units uint64) (string, error)
}

// shareOf is share bps of x, floored, without overflow: founders.bas bps().
func shareOf(x, share uint64) uint64 { return (x/10000)*share + ((x%10000)*share)/10000 }

func (j *journal) add(asset string, units uint64) {
	if units > 0 {
		j.Owed[asset] += units
	}
}

// assets lists every asset the journal owes or has a deposit in flight for, in a fixed order.
func (j *journal) assets() []string {
	seen := map[string]bool{}
	var out []string
	for a := range j.Owed {
		seen[a] = true
		out = append(out, a)
	}
	for a := range j.Deposits {
		if !seen[a] {
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// claimedUnits is what one claim record's quote leg actually moved: the fc growth since its Before, bounded by the
// next record for the same market (its Before), so two sequential claims never count the same growth twice. A record
// whose growth is below the fd it was sent for never landed (the counter only moves by ClaimFees, and one claim takes
// every unit of fd there is).
func (j *journal) claimedUnits(ch chain, i int) (uint64, bool) {
	c := j.Claims[i]
	end := ch.FC(c.SCID)
	for _, later := range j.Claims[i+1:] {
		if later.SCID == c.SCID && later.Before < end {
			end = later.Before
		}
	}
	if c.FD == 0 || end < c.Before+c.FD {
		return 0, false
	}
	return end - c.Before, true
}

// reconcile finishes what a previous run left open. A claim record is judged by the market's cumulative fc counter:
// if it grew by at least the recorded fd, the claim landed and both legs are owed (the quote leg for that growth,
// the asset leg at its recorded read); otherwise it never landed and is dropped. (The unclaimed fd balance is no
// evidence: new fees can refill it between a crash and the restart.) Then every deposit in flight is settled.
func (j *journal) reconcile(ch chain) error {
	for i, c := range j.Claims {
		if units, landed := j.claimedUnits(ch, i); landed {
			j.add(c.Quote, units)
			j.add(c.Asset, c.FA)
		}
	}
	j.Claims = nil
	if err := j.save(); err != nil {
		return err
	}
	return j.settleDeposits(ch)
}

// settleDeposits closes each deposit in flight whose units reached the pass, and sends the others again, unless no
// pass is registered: v2 refuses a deposit then, so the record is held until one registers.
func (j *journal) settleDeposits(ch chain) error {
	for _, a := range j.assets() {
		d := j.Deposits[a]
		if d == nil {
			continue
		}
		if ch.PassDep(a) >= d.Before+d.Units {
			j.finishDeposit(a)
			if err := j.save(); err != nil {
				return err
			}
			continue
		}
		if ch.Registered() == 0 {
			continue
		}
		if err := j.sendDeposit(ch, a); err != nil {
			return err
		}
	}
	return nil
}

// claimMarket records the claim, sends it, and counts it only once the market's fc proves it. It reads fc before the
// fees, so a claim someone else makes in between is inside the growth. It returns the units owed on each leg.
func (j *journal) claimMarket(ch chain, scid, quote, asset string) (uint64, uint64, error) {
	before := ch.FC(scid)
	fd, fa := ch.Fees(scid)
	if fd == 0 {
		return 0, 0, nil // no quote leg, so nothing could prove the claim: it waits
	}
	j.Claims = append(j.Claims, claim{SCID: scid, Quote: quote, Asset: asset, FD: fd, FA: fa, Before: before})
	if err := j.save(); err != nil {
		j.Claims = j.Claims[:len(j.Claims)-1]
		return 0, 0, err
	}
	txid, err := ch.SendClaim(scid)
	if err != nil {
		return 0, 0, err // the record stays; reconcile decides on the next start
	}
	i := len(j.Claims) - 1
	units, landed := j.claimedUnits(ch, i)
	j.Claims = j.Claims[:i]
	if !landed { // included but not executed: nothing moved, so there is nothing to owe
		if err := j.save(); err != nil {
			return 0, 0, err
		}
		return 0, 0, fmt.Errorf("claim tx %s was included but the market's fc did not grow by %d: not counted", short(txid), fd)
	}
	j.add(quote, units)
	j.add(asset, fa)
	return units, fa, j.save()
}

// depositShare settles the deposits in flight, then deposits share bps of what is owed in each asset, one Deposit per
// asset. With no pass registered it deposits nothing and holds everything owed. An asset whose share rounds to zero
// units stays owed and joins the next deposit of that asset.
func (j *journal) depositShare(ch chain, share uint64) error {
	if err := j.settleDeposits(ch); err != nil {
		return err
	}
	if ch.Registered() == 0 {
		return nil
	}
	for _, a := range j.assets() {
		if j.Deposits[a] != nil {
			continue
		}
		base := j.Owed[a]
		units := shareOf(base, share)
		if units == 0 {
			continue
		}
		j.Deposits[a] = &deposit{Units: units, Base: base, Before: ch.PassDep(a)}
		if err := j.save(); err != nil {
			delete(j.Deposits, a)
			return err
		}
		if err := j.sendDeposit(ch, a); err != nil {
			return err
		}
	}
	return nil
}

// sendDeposit sends asset a's deposit in flight and closes it only once the pass's ad_<i> proves it landed.
func (j *journal) sendDeposit(ch chain, a string) error {
	d := j.Deposits[a]
	txid, err := ch.SendDeposit(a, d.Units)
	if err != nil {
		return err // the record stays; the next sweep or start resends or closes it
	}
	if ch.PassDep(a) < d.Before+d.Units {
		return fmt.Errorf("deposit tx %s of %d units of %s… was included but the pass's counter for it did not grow: the record stays open for the next start", short(txid), d.Units, a[:8])
	}
	d.TxID = txid
	j.finishDeposit(a)
	return j.save()
}

func short(txid string) string {
	if len(txid) > 8 {
		return txid[:8] + "…"
	}
	return txid
}

// finishDeposit: the deposit covered what was owed in its asset when it was created; claims counted since stay owed.
func (j *journal) finishDeposit(a string) {
	d := j.Deposits[a]
	if o := j.Owed[a]; o > d.Base {
		j.Owed[a] = o - d.Base
	} else {
		delete(j.Owed, a)
	}
	delete(j.Deposits, a)
}
