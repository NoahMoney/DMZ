// Package swapstatus is the companions' half of docs/SWAP_STATUS.md: the version 1 status answer an xmrswap or zecswap
// gives the app on the same machine, and the loopback server that gives it. It uses the standard library only.
//
// The answer carries no secret: the struct below has no field that could hold one, Validate refuses a value outside
// the contract before anything is served, and each companion's tests run every phase of its swap and look for every
// secret it holds in every answer.
package swapstatus

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Version is the contract's version, the answer's "v".
const Version = 1

// The values a step's state and chain, and an answer's outcome, may take (docs/SWAP_STATUS.md §4).
const (
	Done    = "done"
	Active  = "active"
	Waiting = "waiting"
	Skipped = "skipped"
	Failed  = "failed"

	DERO   = "DERO"
	Monero = "Monero"
	Zcash  = "Zcash"
)

// Outcomes are the answer's possible outcomes: "" while the swap runs, one of the others once it has ended.
var Outcomes = []string{"", "swapped", "refunded", "punished", "reclaimed", "lost"}

// MaxSteps is the most steps an answer may carry.
const MaxSteps = 32

// Payload is one status answer, version 1. Amounts are decimal strings of atomic units; a value the companion does
// not know is left out (an empty amount) or null (a nil pointer), never made up.
type Payload struct {
	V             int     `json:"v"`
	Program       string  `json:"program"`
	Role          string  `json:"role"`
	Direction     string  `json:"direction"`
	Network       string  `json:"network"`
	Contract      string  `json:"contract"`
	Lock          *uint64 `json:"lock"`
	Asset         string  `json:"asset"`
	DeroAmount    string  `json:"deroAmount,omitempty"`
	ForeignAmount string  `json:"foreignAmount,omitempty"`
	Fee           string  `json:"fee,omitempty"`
	Phase         string  `json:"phase"`
	Outcome       string  `json:"outcome"`
	Next          string  `json:"next"`
	Updated       string  `json:"updated"`
	Steps         []Step  `json:"steps"`
}

// Step is one stage of the swap, from the user's side.
type Step struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Chain    string    `json:"chain"`
	State    string    `json:"state"`
	TxID     string    `json:"txid"`
	Conf     *uint64   `json:"conf"`
	Need     *uint64   `json:"need"`
	Deadline *Deadline `json:"deadline"`
}

// Deadline is the height a step must happen by (or from) on a chain, and the companion's estimate of when that is.
type Deadline struct {
	Chain  string  `json:"chain"`
	Height uint64  `json:"height"`
	ETA    *string `json:"eta"`
}

// U is a pointer to a count, for Conf, Need and Lock.
func U(v uint64) *uint64 { return &v }

// Direction is the user's direction for a role: the maker gives the DERO side, a withdrawal; the taker a deposit.
func Direction(role string) string {
	switch role {
	case "maker":
		return "withdrawal"
	case "taker":
		return "deposit"
	}
	return ""
}

// BlockSeconds is each chain's target block time: what an estimate of a deadline's time is counted in.
var BlockSeconds = map[string]uint64{DERO: 18, Monero: 120, Zcash: 75}

// At is a deadline on chain at height, with an estimate of its time when the chain's blocks come at its target rate
// (rated) and the companion has read the tip: now plus the blocks left at the chain's block time. A deadline at or
// below the tip has passed, and its estimate is now. With no tip, or on a chain mined on demand (the DERO simulator,
// a Zcash regtest), there is no honest estimate and ETA is null.
func At(chain string, height, tip uint64, rated bool, now time.Time) *Deadline {
	d := &Deadline{Chain: chain, Height: height}
	if rated && tip > 0 {
		var left uint64
		if height > tip {
			left = height - tip
		}
		eta := now.Add(time.Duration(left*BlockSeconds[chain]) * time.Second).UTC().Format(time.RFC3339)
		d.ETA = &eta
	}
	return d
}

// Depth is how many blocks a transaction seen at height has on top of it at tip, or nil when either is unknown.
func Depth(tip, height uint64) *uint64 {
	if tip == 0 || height == 0 {
		return nil
	}
	if tip < height {
		return U(0)
	}
	return U(tip - height)
}

// Confirmations is the Zcash and Monero count for a transaction mined at height: the tip's block counts as one.
func Confirmations(tip, height uint64) *uint64 {
	if tip == 0 || height == 0 {
		return nil
	}
	if tip < height {
		return U(0)
	}
	return U(tip - height + 1)
}

// Hex64 is s when it is a 64-character lower-case hex id, else "": a step names only a transaction id it has.
func Hex64(s string) string {
	if hex64.MatchString(s) {
		return s
	}
	return ""
}

var (
	hex64   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	decimal = regexp.MustCompile(`^[0-9]{1,30}$`)
	// SecretName is the app's own test for a field that names a secret (app/track.js SECRET): an answer with one is
	// refused whole, so the companion refuses to serve it first.
	SecretName = regexp.MustCompile(`(?i)secret|preimage|priv|seed|mnemonic|spend.?key|view.?key|channel.?key|pairing`)
)

// Validate is every rule of the contract that the app checks, applied before an answer is served: an answer that
// breaks one is never sent (fail closed).
func (p Payload) Validate() error {
	in := func(v string, set ...string) bool {
		for _, s := range set {
			if v == s {
				return true
			}
		}
		return false
	}
	switch {
	case p.V != Version:
		return fmt.Errorf("swapstatus: version %d, not %d", p.V, Version)
	case !in(p.Program, "xmrswap", "zecswap"):
		return fmt.Errorf("swapstatus: program %q", p.Program)
	case Direction(p.Role) == "" || p.Direction != Direction(p.Role):
		return fmt.Errorf("swapstatus: role %q with direction %q", p.Role, p.Direction)
	case !in(p.Network, "simulator", "testnet", "mainnet"):
		return fmt.Errorf("swapstatus: network %q", p.Network)
	case !hex64.MatchString(p.Contract):
		return errors.New("swapstatus: the contract is not 64 lower-case hex")
	case p.Asset != "" && !hex64.MatchString(p.Asset):
		return errors.New("swapstatus: the asset is neither \"\" nor 64 lower-case hex")
	case !in(p.Outcome, Outcomes...):
		return fmt.Errorf("swapstatus: outcome %q", p.Outcome)
	case len(p.Phase) > 40 || len(p.Next) > 240:
		return errors.New("swapstatus: the phase or the next sentence is too long")
	case len(p.Steps) > MaxSteps:
		return fmt.Errorf("swapstatus: %d steps, at most %d", len(p.Steps), MaxSteps)
	}
	for _, a := range []struct{ name, v string }{{"deroAmount", p.DeroAmount}, {"foreignAmount", p.ForeignAmount}, {"fee", p.Fee}} {
		if a.v != "" && !decimal.MatchString(a.v) {
			return fmt.Errorf("swapstatus: %s %q is not a decimal string", a.name, a.v)
		}
	}
	if _, err := time.Parse(time.RFC3339, p.Updated); err != nil {
		return fmt.Errorf("swapstatus: updated %q is not RFC 3339", p.Updated)
	}
	for i, s := range p.Steps {
		switch {
		case s.ID == "" || len(s.ID) > 40 || len(s.Label) > 120:
			return fmt.Errorf("swapstatus: step %d's id or label", i)
		case !in(s.Chain, DERO, Monero, Zcash):
			return fmt.Errorf("swapstatus: step %s on chain %q", s.ID, s.Chain)
		case !in(s.State, Done, Active, Waiting, Skipped, Failed):
			return fmt.Errorf("swapstatus: step %s in state %q", s.ID, s.State)
		case s.TxID != "" && !hex64.MatchString(s.TxID):
			return fmt.Errorf("swapstatus: step %s's txid is not 64 lower-case hex", s.ID)
		case s.Deadline != nil && !in(s.Deadline.Chain, DERO, Monero, Zcash):
			return fmt.Errorf("swapstatus: step %s's deadline on chain %q", s.ID, s.Deadline.Chain)
		}
		if s.Deadline != nil && s.Deadline.ETA != nil {
			if _, err := time.Parse(time.RFC3339, *s.Deadline.ETA); err != nil {
				return fmt.Errorf("swapstatus: step %s's deadline estimate is not RFC 3339", s.ID)
			}
		}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if k := SecretField(b); k != "" {
		return fmt.Errorf("swapstatus: a field named %q: an answer never carries a secret", k)
	}
	return nil
}

// SecretField is the first key in a JSON document, at any depth, whose name the app reads as a secret's, or "".
func SecretField(b []byte) string {
	var v interface{}
	if json.Unmarshal(b, &v) != nil {
		return ""
	}
	var walk func(v interface{}) string
	walk = func(v interface{}) string {
		switch x := v.(type) {
		case map[string]interface{}:
			for k, e := range x {
				if SecretName.MatchString(k) {
					return k
				}
				if f := walk(e); f != "" {
					return f
				}
			}
		case []interface{}:
			for _, e := range x {
				if f := walk(e); f != "" {
					return f
				}
			}
		}
		return ""
	}
	return walk(v)
}

// Units writes an amount of atomic units at a scale of decimals, trimmed: 2000000 at 5 is "20", 150000 at 5 "1.5".
func Units(v, decimals uint64) string {
	s := fmt.Sprintf("%d", v)
	if decimals == 0 {
		return s
	}
	d := int(decimals)
	if len(s) <= d {
		s = strings.Repeat("0", d-len(s)+1) + s
	}
	whole, frac := s[:len(s)-d], strings.TrimRight(s[len(s)-d:], "0")
	if frac == "" {
		return whole
	}
	return whole + "." + frac
}
