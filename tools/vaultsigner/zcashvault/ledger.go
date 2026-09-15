package zcashvault

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
)

// The signer's release ledger: for each withdrawal j, every transaction this signer has signed for it or adopted as
// its payment, written to disk BEFORE the signature leaves the process. It is the backend's own file, beside the
// daemon's state, and it is what makes Release idempotent per j across crashes:
//
//   - a signer never signs a second transaction for j while an earlier one it signed could still be mined, unless
//     the two spend a common output (directly, or through the vault's chain of change outputs), so that at most one
//     of them can ever be in the chain;
//   - a crash between signing and recording cannot happen, because the record comes first; a crash after recording
//     re-signs the same transaction, and RFC 6979 makes that the same signature;
//   - a crash after assembling finds the whole signed transaction here and broadcasts it again.
//
// Losing this file is survivable (Release also finds a payment for j on the chain by its nLockTime marker), but it
// is not the design: back it up with the daemon's state.

type ledgerTx struct {
	TxID    string   `json:"txid"`
	Hex     string   `json:"hex"`             // the transaction as built (placeholder-free, scriptSigs empty)
	Final   string   `json:"final,omitempty"` // the fully signed transaction, once assembled or adopted
	Expiry  uint32   `json:"expiry"`
	Inputs  []string `json:"inputs"`  // outpoints spent, "<display txid>:<vout>", input 0 the anchor
	Values  []uint64 `json:"values"`  // the value each input spends
	Scripts []string `json:"scripts"` // the scriptPubKey each input spends, hex
	State   string   `json:"state"`   // signing, assembled, adopted, abandoned
}

type ledgerEntry struct {
	J      uint64      `json:"j"`
	Dest   string      `json:"dest"`
	Amount uint64      `json:"amount"`
	Txs    []*ledgerTx `json:"txs"`
}

type ledger struct {
	Vault   string                  `json:"vault"`
	Signer  string                  `json:"signer"` // this signer's account xpub
	Entries map[string]*ledgerEntry `json:"entries"`
	path    string
}

// loadLedger is strict like the daemon's state: a missing file is a fresh signer; a corrupt one, or one written for
// another vault or another signer, stops the backend rather than forgetting what was signed.
func loadLedger(path, vault, signer string) (*ledger, error) {
	l := &ledger{Vault: vault, Signer: signer, Entries: map[string]*ledgerEntry{}, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return l, l.save()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("zcashvault: the release ledger %s is corrupt (%v): refusing to start without the record of what was signed", path, err)
	}
	if l.Vault != vault || l.Signer != signer {
		return nil, fmt.Errorf("zcashvault: the release ledger %s belongs to vault %.8s… signer %.12s…, not this one", path, l.Vault, l.Signer)
	}
	if l.Entries == nil {
		l.Entries = map[string]*ledgerEntry{}
	}
	l.path = path
	return l, nil
}

func (l *ledger) entry(j uint64, dest string, amount uint64) (*ledgerEntry, error) {
	k := strconv.FormatUint(j, 10)
	e := l.Entries[k]
	if e == nil {
		e = &ledgerEntry{J: j, Dest: dest, Amount: amount}
		l.Entries[k] = e
		return e, nil
	}
	if e.Dest != dest || e.Amount != amount {
		return nil, fmt.Errorf("zcashvault: withdrawal %d is recorded as %d to %s and the contract now says %d to %s; not signing anything for it", j, e.Amount, e.Dest, amount, dest)
	}
	return e, nil
}

func (e *ledgerEntry) find(txid string) *ledgerTx {
	for _, t := range e.Txs {
		if t.TxID == txid {
			return t
		}
	}
	return nil
}

// save writes the ledger atomically: a temporary file, fsync, rename, then fsync of the directory where the
// platform allows it.
func (l *ledger) save() error {
	b, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
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
	return os.Rename(tmp, l.path)
}
