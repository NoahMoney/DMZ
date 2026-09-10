package xmrswap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// State is everything one companion knows about one swap, on disk. It is the record that must exist before
// any irreversible step (Invariant 5): the lock, the gate, the claim, the refund, the sweep are each preceded
// by a save of the phase that intends them, so a restart finds the intent and finishes it instead of doing it
// again. It is written with fsync + rename and loaded strictly, as tools/vaultsigner's memory is: a missing
// file is a fresh swap, an unreadable one stops the companion, and one bound to another role or another
// contract is refused rather than replayed.
//
// The secrets live here and only here: this party's key share, Bob's gate secret, the joint view key. They
// are hex in a 0600 file. They never appear in a log line or in a message of another kind than the protocol's.
type State struct {
	Role    string `json:"role"` // "bob" or "alice"
	HTLC    string `json:"htlc"` // the htlc.bas scid this state belongs to
	Phase   string `json:"phase"`
	Outcome string `json:"outcome,omitempty"` // set when Done: what happened, for the screen
	Done    bool   `json:"done"`
	// SwapID names this swap in the used-secrets register (Invariant 13): random, chosen when the secrets are,
	// so every secret this file holds is on record as belonging to this swap and no other.
	SwapID string `json:"swapId,omitempty"`

	Terms Terms `json:"terms"`
	Own   Party `json:"own"`  // this party's share, hash, point, proof
	Peer  Party `json:"peer"` // the other party's hash, point, proof (never a secret)

	// Gate is k_b: Bob's own, generated at setup; Alice's, once received and checked against hk on the chain.
	Gate string `json:"gate,omitempty"`
	// HK is SHA256(k_b): Bob commits to it in bob_setup and in the lock; Alice records the committed value.
	HK string `json:"hk,omitempty"`
	// View is the joint address's private view key v (shared by design: both must see the lock, neither can
	// spend with it) and ViewPub is V.
	View    string `json:"view,omitempty"`
	ViewPub string `json:"viewPub,omitempty"`
	Joint   string `json:"joint,omitempty"` // the joint Monero address P = S_a + S_b, V

	MyDero   string `json:"myDero"`             // this party's DERO address
	MyXmr    string `json:"myXmr"`              // this party's own Monero address (Bob: where the sweep goes)
	PeerDero string `json:"peerDero,omitempty"` // Bob: Alice's DERO address, the taker he names

	// RestoreHeight is the Monero height at setup: the joint wallets scan from here.
	RestoreHeight uint64 `json:"restoreHeight"`

	Lock  LockRecord  `json:"lock"`
	Xmr   XmrRecord   `json:"xmr"`
	Sweep SweepRecord `json:"sweep"`

	ClaimTx  string `json:"claimTx,omitempty"`
	RefundTx string `json:"refundTx,omitempty"`
	PunishTx string `json:"punishTx,omitempty"`
	// Revealed is the peer's share as read from the chain: s_a for Bob after a Claim, s_b for Alice after a Refund.
	Revealed string `json:"revealed,omitempty"`

	// Consumed records message kinds already acted on, so a repeated delivery is dropped. LastSent is the
	// message the peer is owed: it is written before it is sent and repeated until the protocol moves on.
	Consumed   map[string]bool `json:"consumed"`
	LastSent   *Message        `json:"lastSent,omitempty"`
	LastSentAt time.Time       `json:"lastSentAt,omitempty"`

	path string
}

// Terms are what both sides agreed over the channel and what every chain read is checked against.
type Terms struct {
	AmountDero uint64 `json:"amountDero"` // atomic units
	AmountXmr  uint64 `json:"amountXmr"`  // piconero
	Asset      string `json:"asset"`      // "" is DERO; this build locks DERO only
	Net        string `json:"net"`        // Monero network name
	T1Delta    uint64 `json:"t1Delta"`    // blocks from Bob's lock to t1
	T2Delta    uint64 `json:"t2Delta"`    // blocks from t1 to t2
}

// Party is one side's key material. Secret is set only in Own.
type Party struct {
	Secret string `json:"secret,omitempty"` // hex of the reduced scalar
	H      string `json:"h"`                // SHA256 of the 32 secret bytes, 64 hex
	S      string `json:"s"`                // the point secret·G, hex
	Proof  string `json:"proof"`            // π, hex
}

// LockRecord is the DERO lock as this party knows it.
type LockRecord struct {
	ID           uint64 `json:"id"`
	Tx           string `json:"tx,omitempty"`
	IntentHeight uint64 `json:"intentHeight,omitempty"` // Bob: DERO height when the lock was decided
	// AttemptHeight is the DERO height at which this companion last submitted a transaction for this lock and
	// did not see it land — a Lock whose inclusion timed out, or a Refund sent without a readable answer. The
	// transaction may still be in the mempool, where DERO.GetSC cannot show it, so nothing is sent again until
	// the settle window measured from here has passed.
	AttemptHeight uint64 `json:"attemptHeight,omitempty"`
	SeenHeight    uint64 `json:"seenHeight,omitempty"` // Alice: DERO height when the lock first passed every check
	// ClaimSeenHeight is the DERO height at which Bob first read st == 1. The contract does not store the block
	// the Claim was mined in, and Bob never learns its txid from anyone he trusts; but the block is at or below
	// the height he read it at, so he sweeps only once this height is at or below the daemon's stable height
	// (Invariant 9): from then on DERO cannot replace the Claim's block.
	ClaimSeenHeight uint64 `json:"claimSeenHeight,omitempty"`
	T1              uint64 `json:"t1"`
	T2              uint64 `json:"t2"`
	Amount          uint64 `json:"amount"`
}

// settleFrom is the height a wait for a possibly in-flight transaction is measured from: the last attempt if
// there was one, otherwise the height the lock was decided at.
func (l LockRecord) settleFrom() uint64 {
	if l.AttemptHeight > l.IntentHeight {
		return l.AttemptHeight
	}
	return l.IntentHeight
}

// XmrRecord is the Monero lock as this party knows it.
type XmrRecord struct {
	LockTx      string `json:"lockTx,omitempty"`
	LockUnknown bool   `json:"lockUnknown,omitempty"` // Alice restarted between the transfer intent and its txid
	ConfirmedAt uint64 `json:"confirmedAt,omitempty"` // Bob: Monero height at which ≥ amount had ≥ confirmations
	Confirmed   uint64 `json:"confirmed,omitempty"`   // Bob: the confirmed amount he saw then
}

// SweepRecord is the spend of the joint output, attempted at most once.
type SweepRecord struct {
	Txs     []string `json:"txs,omitempty"`
	Unknown bool     `json:"unknown,omitempty"` // a restart found the sweep intent without its result
	// Waits counts the passes that found the joint wallet with nothing spendable in it. It is durable so a
	// restart does not begin the wait again, and bounded so a wallet that will never hold anything ends the
	// swap rather than holding it open for ever.
	Waits uint64 `json:"waits,omitempty"`
}

func NewState(path, role, htlc string) *State {
	return &State{Role: role, HTLC: htlc, Consumed: map[string]bool{}, path: path}
}

// LoadState is strict: a missing file is a fresh swap, an unreadable one or one belonging to another role or
// contract stops the companion rather than replaying its intents against the wrong lock.
func LoadState(path, role, htlc string) (*State, error) {
	s := NewState(path, role, htlc)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("state %s is corrupt (%v): refusing to start with an empty memory", path, err)
	}
	if s.Role != role {
		return nil, fmt.Errorf("state %s belongs to %s, not %s", path, s.Role, role)
	}
	if s.HTLC != "" && s.HTLC != htlc {
		return nil, fmt.Errorf("state %s belongs to contract %s, not %s: its lock id names that contract's lock", path, s.HTLC, htlc)
	}
	s.HTLC = htlc
	if s.Consumed == nil {
		s.Consumed = map[string]bool{}
	}
	s.path = path
	return s, nil
}

// Path is where the state lives.
func (s *State) Path() string { return s.path }

// StateFileWarning is what the operator must be told about this file's protection on this platform, or "" when
// the file's own mode is the protection. writeAtomic creates it 0600, which is the whole guard on a Unix-like
// system; Windows ignores those bits and gives the file the directory's inherited ACL instead, so on Windows
// nothing here restricts who may read the key share it holds. Saying so is not a fix — it is the difference
// between an operator who knows and one who believes the 0600 in the comment.
func StateFileWarning(path string) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	return "the state file " + path + " holds this party's key share in cleartext, and on Windows the 0600 it is created with is not enforced: it inherits the directory's ACL. Keep it in a directory only this account can read."
}

// Save writes the whole state atomically: temp file, fsync, rename. Nothing irreversible happens between a
// phase change and this call.
func (s *State) Save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.path, b)
}

// writeAtomic is the durable write both files use: temp file, fsync, rename, 0600.
func writeAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
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
	return os.Rename(tmp, path)
}

// --- the used-secrets register (Invariant 13) ---

// UsedSecrets is the record of every secret this companion has ever generated — as SHA-256, never the secret —
// with the swap each belongs to. It lives beside the state files, one per directory, so every swap run from
// that directory shares it, and it is written fsync+rename and loaded strictly as the state is. A freshly
// generated secret that is already in it is not a coincidence — one chance in 2^256 is not a thing that
// happens — so it means a broken prover or random source, and the swap is refused rather than started on a
// share whose first Claim would publish another swap's key (review R20) or a gate that opens another swap's
// lock (R23).
type UsedSecrets struct {
	Secrets map[string]UsedSecret `json:"secrets"` // SHA-256 hex of the secret → the swap it was made for
	path    string
}

// UsedSecret is one register entry: which swap, which of the four secrets, and when.
type UsedSecret struct {
	Swap string    `json:"swap"`
	Kind string    `json:"kind"` // share (s_a or s_b), gate (k_b), view (v)
	At   time.Time `json:"at"`
}

// ErrSecretReused is the refusal: wrapped in the error Record returns when another swap holds the secret.
var ErrSecretReused = errors.New("secret already used by another swap")

// UsedPath is where the register lives for a state file: beside it, under one name for every swap.
func UsedPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "xmrswap-used.json")
}

// LoadUsed is strict: a missing file is an empty register, an unreadable one stops the companion — a register
// it cannot read is one it cannot promise to have checked.
func LoadUsed(path string) (*UsedSecrets, error) {
	u := &UsedSecrets{Secrets: map[string]UsedSecret{}, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return u, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, u); err != nil {
		return nil, fmt.Errorf("used-secrets register %s is corrupt (%v): refusing to generate secrets it cannot be checked against", path, err)
	}
	if u.Secrets == nil {
		u.Secrets = map[string]UsedSecret{}
	}
	u.path = path
	return u, nil
}

// Record enters secret for swap, durably, before the caller uses it. Two companions in one directory (Bob and
// Alice on the simulator) share the file, so it is re-read before it is written and the entries are merged;
// the same swap recording the same secret again is a repeat, not a reuse.
func (u *UsedSecrets) Record(secret []byte, swap, kind string) error {
	if swap == "" {
		return errors.New("used-secrets register: a secret needs a swap id")
	}
	fresh, err := LoadUsed(u.path)
	if err != nil {
		return err
	}
	for k, v := range fresh.Secrets {
		u.Secrets[k] = v
	}
	h := SHA256Hex(secret)
	if prev, seen := u.Secrets[h]; seen && prev.Swap != swap {
		return fmt.Errorf("%w: this %s was generated for swap %s on %s", ErrSecretReused, kind, prev.Swap, prev.At.Format(time.RFC3339))
	}
	u.Secrets[h] = UsedSecret{Swap: swap, Kind: kind, At: time.Now().UTC()}
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(u.path, b)
}
