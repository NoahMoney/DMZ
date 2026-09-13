package main

// XMR and ZEC payouts to the Founders Pass holders (docs/FOUNDERS_V2_NOTES.md §2).
//
// TRUSTED PARTY. No contract forces any of this. founders.bas pays no XMR and no ZEC; it only stores where a holder
// wants to be paid (SetXmr, SetZec). The swap fees reach the treasury's own Monero and Zcash addresses, and this bot,
// run by whoever holds the treasury's keys, decides to pay the passes their share. It can stop, pay late, or not pay,
// and nothing on any chain can make it. What it offers instead is a record: every period's income, snapshot and
// split, and every payment's txid, is in an append-only journal per asset, and every payment is public on its chain.
//
// The rule is founders.bas's for DERO deposits: a deposit is shared by the passes registered when it is made. Here a
// "deposit" is one period's income. At the end of each period (a DERO height, every -period-blocks blocks, read once it
// is stable) the bot:
//   - counts the fees that reached the treasury's address and are at least incomeConf deep (Monero: unlocked incoming
//     transfers to xmrTreasury; Zcash: outputs paying zecTreasury), each once, by its id;
//   - takes `share` bps of them (floored, as founders.bas bps()); the rest is the treasury's and stays in its wallet;
//   - adds the passes' part to what is carried from earlier periods and divides it equally among the pass units
//     registered at that height (hu_<addr> over hst, read from the contract's storage at that height): each unit gets
//     floor(pool / hst), and the remainder, less than hst atomic units, is carried to the next period, exactly as
//     founders.bas keeps au_<i>;
//   - with no pass registered, carries the whole of the passes' part, which the first period with a registered pass
//     then divides (app/notice.js: "the bot holds those fees and pays them in the first deposit after a pass
//     registers").
//
// A holder's part is paid to the address it stored on the pass (xm_<addr> or zc_<addr>) as read at the latest
// snapshot, after this bot checks it fully (checksum, network prefix, and for Zcash that it is transparent). A holder
// with no address, or one that fails, keeps its part owed here and is paid in a later period once it stores one. A
// part below -xmr-min-part / -zec-min-part (set about the network fee) is carried, not paid. The network fee of a
// payout is the treasury's: holders receive their parts in full.
//
// The journal is append-only JSON lines, fsynced per line. A payout is written as its intent (period, snapshot, the
// holders and amounts) before anything is built; its signed transaction (Zcash: the raw bytes; Monero: the wallet's
// tx_metadata of a transfer built with do_not_relay) before it is broadcast; then "sent", then "paid" once it is
// incomeConf deep. On restart the open payout is settled before anything new is paid: an intent with no signed
// record was never broadcast and is voided; a signed one is looked up (Monero: the wallet's history; Zcash: every
// node) and, if unknown, the same bytes are broadcast again. Only a payout proven never minable (a Zcash transaction
// past its expiry height that no node knows) is voided and its parts become due again. One payout is open per asset
// at a time, so a crash at any step can never produce a second transaction for the same parts.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"sort"
	"strings"
)

// incomeConf is how deep an XMR or ZEC fee must be before it is counted, and a payout before it is closed as paid.
const incomeConf = 10

// income is one fee payment to the treasury's address: a Monero txid, or a Zcash outpoint "txid:index".
type income struct {
	ID  string `json:"id"`
	Amt uint64 `json:"amt"`
}

// dest is one holder's part in a payout.
type dest struct {
	Holder string `json:"holder"` // the pass holder's DERO address, as hu_<addr> spells it
	Addr   string `json:"addr"`   // the payout address it stored, at the snapshot
	Amt    uint64 `json:"amt"`    // atomic units: piconero or zatoshi
}

// built is a signed payout, not yet broadcast.
type built struct {
	TxID   string
	Blob   string // what rebroadcasts it: Zcash's raw transaction, Monero's tx_metadata (hex)
	Fee    uint64
	Expiry uint32   // Zcash: nExpiryHeight; 0 for Monero
	Inputs []string // Zcash: the outpoints it spends
}

type txState int

const (
	txUnknown txState = iota // no node or wallet knows it: broadcast the same bytes again
	txPending                // known, not yet incomeConf deep
	txMined                  // incomeConf deep
	txDead                   // proven never minable: its parts may be paid again
)

// payNet is one coin's side, narrowed so a test can fake it.
type payNet interface {
	Name() string
	Income(ctx context.Context) ([]income, error)                                        // confirmed fees at the treasury address
	Check(addr string) error                                                             // nil when addr is payable on this network
	MinPart() uint64                                                                     // a part below this is carried
	MaxDests() int                                                                       // destinations per transaction
	Build(ctx context.Context, dests []dest, spendable func(string) bool) (built, error) // sign, do not broadcast
	Broadcast(ctx context.Context, p *payout) error                                      // idempotent
	State(ctx context.Context, p *payout) (txState, error)
}

// passReader reads founders.bas at a DERO height.
type passReader interface {
	Stable() (uint64, error)
	// Snapshot reads hst, every hu_<addr>, and key+<addr> (xm_ or zc_) for every holder and for each of extra.
	Snapshot(height uint64, key string, extra []string) (snapshot, error)
}

type snapshot struct {
	Height uint64
	Topo   int64
	Hst    uint64
	Units  map[string]uint64
	Addrs  map[string]string
}

// event is one journal line.
type event struct {
	T      string            `json:"t"` // period, intent, signed, sent, paid, void
	N      uint64            `json:"n,omitempty"`
	Period uint64            `json:"period,omitempty"` // the snapshot's DERO height
	Topo   int64             `json:"topo,omitempty"`
	Income []income          `json:"income,omitempty"`
	Total  uint64            `json:"total,omitempty"`
	Share  uint64            `json:"share,omitempty"`
	Hst    uint64            `json:"hst,omitempty"`
	Units  map[string]uint64 `json:"units,omitempty"`
	Addrs  map[string]string `json:"addrs,omitempty"`
	Inc    uint64            `json:"inc,omitempty"`   // per pass unit this period
	Carry  uint64            `json:"carry,omitempty"` // carried after this period
	Dests  []dest            `json:"dests,omitempty"`
	TxID   string            `json:"txid,omitempty"`
	Blob   string            `json:"blob,omitempty"`
	Fee    uint64            `json:"fee,omitempty"`
	Expiry uint32            `json:"expiry,omitempty"`
	Inputs []string          `json:"inputs,omitempty"`
	Why    string            `json:"why,omitempty"`
}

// payout is the one open payout of an asset.
type payout struct {
	N      uint64
	Period uint64
	Dests  []dest
	TxID   string
	Blob   string
	Expiry uint32
	Inputs []string
	Sent   bool
}

func (p *payout) total() uint64 {
	var s uint64
	for _, d := range p.Dests {
		s += d.Amt
	}
	return s
}

// ledger is one asset's journal, replayed.
type ledger struct {
	path     string
	f        *os.File
	Income   uint64            // every fee counted
	Treasury uint64            // the treasury's part of it
	Paid     uint64            // parts in payouts closed as paid
	Carry    uint64            // the passes' part not yet divided: remainders, and all of it while no pass is registered
	Due      map[string]uint64 // per holder: divided to it, not yet in a payout
	Counted  map[string]bool   // income ids counted
	own      map[string]bool   // txids of every payout this bot signed (their change is never income)
	last     *event            // the latest period
	open     *payout
	nextN    uint64
}

// crashHook lets a test stop the process at a named step; nil otherwise.
var crashHook func(step string) error

func hook(step string) error {
	if crashHook != nil {
		return crashHook(step)
	}
	return nil
}

// openLedger replays the journal at path, creating it if absent. A last line cut short by a crash is dropped (it
// was never acted on: every step follows its line's fsync); any other unreadable line stops the asset.
func openLedger(path string) (*ledger, error) {
	l := &ledger{path: path, Due: map[string]uint64{}, Counted: map[string]bool{}, own: map[string]bool{}, nextN: 1}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	good := len(b)
	if i := bytes.LastIndexByte(b, '\n'); i+1 != len(b) {
		good = i + 1 // a torn tail
	}
	sc := bufio.NewScanner(bytes.NewReader(b[:good]))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 1; sc.Scan(); n++ {
		var e event
		dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("journal %s line %d is unreadable (%v): refusing to pay this asset", path, n, err)
		}
		if err := l.apply(e); err != nil {
			return nil, fmt.Errorf("journal %s line %d: %v: refusing to pay this asset", path, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if good != len(b) {
		if err := f.Truncate(int64(good)); err != nil {
			f.Close()
			return nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	l.f = f
	return l, nil
}

func (l *ledger) close() {
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}

// record checks e against the ledger, appends it, fsyncs, and only then applies it.
func (l *ledger) record(e event) error {
	if err := l.check(e); err != nil {
		return err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := l.f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := l.f.Sync(); err != nil {
		return err
	}
	l.mutate(e)
	return hook("after-" + e.T)
}

func (l *ledger) apply(e event) error {
	if err := l.check(e); err != nil {
		return err
	}
	l.mutate(e)
	return nil
}

// split is the period rule: pass units share pool equally, the remainder is carried.
func split(pool, hst uint64) (inc, carry uint64) {
	if hst == 0 {
		return 0, pool
	}
	return pool / hst, pool % hst
}

func add(a, b uint64) (uint64, error) {
	s, c := bits.Add64(a, b, 0)
	if c != 0 {
		return 0, errors.New("an amount overflows 64 bits")
	}
	return s, nil
}

// check validates e without changing anything.
func (l *ledger) check(e event) error {
	switch e.T {
	case "period":
		if l.last != nil && e.Period <= l.last.Period {
			return fmt.Errorf("period %d does not follow period %d", e.Period, l.last.Period)
		}
		var sum uint64
		seen := map[string]bool{}
		for _, in := range e.Income {
			if in.ID == "" || l.Counted[in.ID] || seen[in.ID] {
				return fmt.Errorf("income %q is counted twice", in.ID)
			}
			seen[in.ID] = true
			var err error
			if sum, err = add(sum, in.Amt); err != nil {
				return err
			}
		}
		if sum != e.Total {
			return fmt.Errorf("period %d lists %d of income and totals %d", e.Period, sum, e.Total)
		}
		if e.Share > 10000 {
			return fmt.Errorf("share %d bps", e.Share)
		}
		var units uint64
		for _, u := range e.Units {
			units += u
		}
		if units != e.Hst {
			return fmt.Errorf("period %d: the holders' units sum to %d, hst is %d", e.Period, units, e.Hst)
		}
		pool, err := add(l.Carry, shareOf(e.Total, e.Share))
		if err != nil {
			return err
		}
		if inc, carry := split(pool, e.Hst); inc != e.Inc || carry != e.Carry {
			return fmt.Errorf("period %d records %d per unit and %d carried; its numbers give %d and %d", e.Period, e.Inc, e.Carry, inc, carry)
		}
		if _, err := add(l.Income, e.Total); err != nil {
			return err
		}
		for a, u := range e.Units {
			if _, err := add(l.Due[a], e.Inc*u); err != nil {
				return err
			}
		}
	case "intent":
		if l.open != nil {
			return fmt.Errorf("payout %d opens while payout %d is open", e.N, l.open.N)
		}
		if e.N != l.nextN || len(e.Dests) == 0 {
			return fmt.Errorf("payout %d is not the next one (%d) or pays no one", e.N, l.nextN)
		}
		seen := map[string]bool{}
		for _, d := range e.Dests {
			if seen[d.Holder] || d.Amt == 0 || d.Amt > l.Due[d.Holder] {
				return fmt.Errorf("payout %d pays %s %d, more than it is owed (%d) or twice", e.N, d.Holder, d.Amt, l.Due[d.Holder])
			}
			seen[d.Holder] = true
		}
	case "signed", "sent", "paid", "void":
		if l.open == nil || l.open.N != e.N {
			return fmt.Errorf("%s names payout %d, which is not open", e.T, e.N)
		}
		switch {
		case e.T == "signed" && (l.open.TxID != "" || e.TxID == "" || e.Blob == ""):
			return fmt.Errorf("payout %d is signed twice, or without its transaction", e.N)
		case (e.T == "sent" || e.T == "paid") && (l.open.TxID == "" || e.TxID != l.open.TxID):
			return fmt.Errorf("%s of payout %d names tx %s, it signed %q", e.T, e.N, e.TxID, l.open.TxID)
		}
	default:
		return fmt.Errorf("unknown record %q", e.T)
	}
	return nil
}

func (l *ledger) mutate(e event) {
	switch e.T {
	case "period":
		for _, in := range e.Income {
			l.Counted[in.ID] = true
		}
		pass := shareOf(e.Total, e.Share)
		l.Income += e.Total
		l.Treasury += e.Total - pass
		for a, u := range e.Units {
			if e.Inc*u > 0 {
				l.Due[a] += e.Inc * u
			}
		}
		l.Carry = e.Carry
		ev := e
		l.last = &ev
	case "intent":
		for _, d := range e.Dests {
			l.Due[d.Holder] -= d.Amt
			if l.Due[d.Holder] == 0 {
				delete(l.Due, d.Holder)
			}
		}
		l.open = &payout{N: e.N, Period: e.Period, Dests: e.Dests}
		l.nextN = e.N + 1
	case "signed":
		l.open.TxID, l.open.Blob, l.open.Expiry, l.open.Inputs = e.TxID, e.Blob, e.Expiry, e.Inputs
		l.own[e.TxID] = true
	case "sent":
		l.open.Sent = true
	case "paid":
		l.Paid += l.open.total()
		l.open = nil
	case "void":
		for _, d := range l.open.Dests {
			l.Due[d.Holder] += d.Amt
		}
		l.open = nil
	}
}

// spendable says whether a Zcash outpoint may fund a payout: income already counted, or change of this bot's own
// payouts. An output not yet counted is never spent, so no fee can leave the address before it is counted.
func (l *ledger) spendable(id string) bool {
	return l.Counted[id] || l.own[txidOf(id)]
}

func txidOf(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return id[:i]
	}
	return id
}

// inFlight is what the open payout carries.
func (l *ledger) inFlight() uint64 {
	if l.open == nil {
		return 0
	}
	return l.open.total()
}

// balances splits what the passes are still owed into owed (no payable address at the latest snapshot) and carried
// (the undivided pool, and parts below the minimum). paid + inFlight + owed + carried + treasury = income.
func (l *ledger) balances(n payNet) (owed, carried uint64) {
	carried = l.Carry
	for h, v := range l.Due {
		a := ""
		if l.last != nil {
			a = l.last.Addrs[h]
		}
		if a == "" || n.Check(a) != nil {
			owed += v
		} else {
			carried += v
		}
	}
	return owed, carried
}

// assetPayer runs one asset's periods and payouts.
type assetPayer struct {
	net   payNet
	pr    passReader
	l     *ledger
	key   string // xm_ or zc_
	share uint64
	every uint64 // period length, DERO blocks
	dry   bool
	out   io.Writer
}

func (p *assetPayer) say(format string, a ...interface{}) {
	fmt.Fprintf(p.out, p.net.Name()+" payouts: "+format+"\n", a...)
}

// round settles the open payout, closes a period if one ended, and pays what is due when nothing is open.
func (p *assetPayer) round(ctx context.Context) error {
	if p.l.open != nil {
		if _, err := p.settle(ctx); err != nil {
			return err
		}
	}
	if err := p.closePeriod(ctx); err != nil {
		return err
	}
	if p.l.open == nil {
		if err := p.pay(ctx); err != nil {
			return err
		}
	}
	owed, carried := p.l.balances(p.net)
	p.say("income %d, treasury %d, paid %d, in flight %d, owed (no payable address) %d, carried %d", p.l.Income, p.l.Treasury, p.l.Paid, p.l.inFlight(), owed, carried)
	return nil
}

// settle finishes the open payout if it can; done says it is closed.
func (p *assetPayer) settle(ctx context.Context) (bool, error) {
	o := p.l.open
	if o.TxID == "" {
		if p.dry {
			p.say("dry run: payout %d has no signed transaction and would be voided", o.N)
			return false, nil
		}
		p.say("payout %d was never signed and recorded, so never broadcast: voided, its parts are due again", o.N)
		return true, p.l.record(event{T: "void", N: o.N, Why: "no signed transaction was recorded, so none was broadcast"})
	}
	st, err := p.net.State(ctx, o)
	if err != nil {
		return false, fmt.Errorf("payout %d (tx %s): %v", o.N, o.TxID, err)
	}
	if p.dry {
		p.say("dry run: payout %d (tx %s) is in state %d; nothing is recorded or broadcast", o.N, o.TxID, st)
		return false, nil
	}
	switch st {
	case txMined:
		p.say("payout %d paid: tx %s, %d to %d holders", o.N, o.TxID, o.total(), len(o.Dests))
		return true, p.l.record(event{T: "paid", N: o.N, TxID: o.TxID})
	case txDead:
		p.say("payout %d (tx %s) can never be mined: voided, its parts are due again", o.N, o.TxID)
		return true, p.l.record(event{T: "void", N: o.N, TxID: o.TxID, Why: "no node knows it and it is past its expiry height"})
	case txUnknown:
		p.say("payout %d (tx %s) is unknown: broadcasting the same transaction again", o.N, o.TxID)
		if err := p.net.Broadcast(ctx, o); err != nil {
			return false, fmt.Errorf("payout %d (tx %s): rebroadcast: %v", o.N, o.TxID, err)
		}
		if err := hook("after-broadcast"); err != nil {
			return false, err
		}
	}
	if !o.Sent {
		return false, p.l.record(event{T: "sent", N: o.N, TxID: o.TxID})
	}
	return false, nil
}

// closePeriod closes the period ending at the latest stable multiple of every, if it is new.
func (p *assetPayer) closePeriod(ctx context.Context) error {
	stable, err := p.pr.Stable()
	if err != nil {
		return err
	}
	b := stable / p.every * p.every
	if b == 0 || (p.l.last != nil && b <= p.l.last.Period) {
		return nil
	}
	items, err := p.net.Income(ctx)
	if err != nil {
		return fmt.Errorf("reading income: %v", err)
	}
	var fresh []income
	var total uint64
	for _, in := range items {
		if in.ID == "" || p.l.Counted[in.ID] || p.l.own[txidOf(in.ID)] {
			continue
		}
		if total, err = add(total, in.Amt); err != nil {
			return err
		}
		fresh = append(fresh, in)
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].ID < fresh[j].ID })
	var owed []string
	for h := range p.l.Due {
		owed = append(owed, h)
	}
	sort.Strings(owed)
	s, err := p.pr.Snapshot(b, p.key, owed)
	if err != nil {
		return fmt.Errorf("snapshot at DERO height %d: %v", b, err)
	}
	pool, err := add(p.l.Carry, shareOf(total, p.share))
	if err != nil {
		return err
	}
	inc, carry := split(pool, s.Hst)
	e := event{T: "period", Period: b, Topo: s.Topo, Income: fresh, Total: total, Share: p.share, Hst: s.Hst, Units: s.Units, Addrs: s.Addrs, Inc: inc, Carry: carry}
	if p.dry {
		p.say("dry run: period %d would count %d in %d payments, %d per pass unit over %d, carrying %d; nothing recorded", b, total, len(fresh), inc, s.Hst, carry)
		return nil
	}
	if err := p.l.record(e); err != nil {
		return err
	}
	if s.Hst == 0 {
		p.say("period %d (topo %d): %d counted in %d payments; no pass registered, so the passes' part is held (%d carried) for the first period with one", b, s.Topo, total, len(fresh), carry)
	} else {
		p.say("period %d (topo %d): %d counted in %d payments; %d per pass unit to %d units, %d carried", b, s.Topo, total, len(fresh), inc, s.Hst, carry)
	}
	return nil
}

// pay opens one payout for every holder with a payable address and a part at least MinPart, up to MaxDests.
func (p *assetPayer) pay(ctx context.Context) error {
	if p.l.last == nil {
		return nil
	}
	var holders []string
	for h := range p.l.Due {
		holders = append(holders, h)
	}
	sort.Strings(holders)
	var dests []dest
	for _, h := range holders {
		amt, addr := p.l.Due[h], p.l.last.Addrs[h]
		switch {
		case addr == "":
			continue // owed until it stores an address
		case p.net.Check(addr) != nil:
			p.say("%s… stored %q, which is not payable here (%v): its %d stays owed", short(h), addr, p.net.Check(addr), amt)
			continue
		case amt < p.net.MinPart():
			continue // carried until it reaches the minimum
		}
		dests = append(dests, dest{Holder: h, Addr: addr, Amt: amt})
		if len(dests) == p.net.MaxDests() {
			break
		}
	}
	if len(dests) == 0 {
		return nil
	}
	if p.dry {
		for _, d := range dests {
			p.say("dry run: would pay %d to %s (holder %s…)", d.Amt, d.Addr, short(d.Holder))
		}
		return nil
	}
	n := p.l.nextN
	if err := p.l.record(event{T: "intent", N: n, Period: p.l.last.Period, Dests: dests}); err != nil {
		return err
	}
	b, err := p.net.Build(ctx, dests, p.l.spendable)
	if err != nil {
		if verr := p.l.record(event{T: "void", N: n, Why: "the transaction could not be built"}); verr != nil {
			return verr
		}
		return fmt.Errorf("payout %d not built, its parts stay due: %v", n, err)
	}
	if err := hook("after-build"); err != nil {
		return err
	}
	if err := p.l.record(event{T: "signed", N: n, TxID: b.TxID, Blob: b.Blob, Fee: b.Fee, Expiry: b.Expiry, Inputs: b.Inputs}); err != nil {
		return err
	}
	if err := p.net.Broadcast(ctx, p.l.open); err != nil {
		return fmt.Errorf("payout %d (tx %s) signed and recorded, broadcast failed; it is sent again, never rebuilt: %v", n, b.TxID, err)
	}
	if err := hook("after-broadcast"); err != nil {
		return err
	}
	var total uint64
	for _, d := range dests {
		total += d.Amt
	}
	p.say("payout %d sent: tx %s, %d to %d holders, fee %d (the treasury's)", n, b.TxID, total, len(dests), b.Fee)
	return p.l.record(event{T: "sent", N: n, TxID: b.TxID})
}
