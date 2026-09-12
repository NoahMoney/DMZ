package xmrswap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	// JointPassword encrypts the joint wallets' files on the wallet-rpc (GAP-020): 32 random bytes made at setup, so
	// a copy of the wallet-dir alone opens nothing.
	JointPassword string `json:"jointPassword,omitempty"`

	MyDero   string `json:"myDero"`             // this party's DERO address
	MyXmr    string `json:"myXmr"`              // this party's own Monero address (Bob: where the sweep goes)
	PeerDero string `json:"peerDero,omitempty"` // Bob: Alice's DERO address, the taker he names

	// RestoreHeight is the Monero height at setup: the joint wallets scan from here.
	RestoreHeight uint64 `json:"restoreHeight"`
	// DeroHeight is the DERO height at setup: nothing of this swap's is on DERO below it, so the stipend is looked
	// for from here in either party's wallet history.
	DeroHeight uint64 `json:"deroHeight,omitempty"`

	// Contract is the htlc.bas install this swap runs on, as this companion read it before anything of its party's went
	// into it (checkDeployment): v3's install arguments, from which the Lock's cutoff, the Punish deadline and the
	// Reclaim height are counted.
	Contract ContractRecord `json:"contract"`
	Lock     LockRecord     `json:"lock"`
	Stipend  StipendRecord  `json:"stipend"`
	Xmr      XmrRecord      `json:"xmr"`
	Sweep    SweepRecord    `json:"sweep"`

	ClaimTx   string `json:"claimTx,omitempty"`
	RefundTx  string `json:"refundTx,omitempty"`
	PunishTx  string `json:"punishTx,omitempty"`
	ReclaimTx string `json:"reclaimTx,omitempty"` // Bob: his Reclaim, from t2 + g2 (htlc.bas v3)
	// Revealed is the peer's share as read from the chain: s_a for Bob after a Claim, s_b for Alice after a Refund or
	// a Reclaim.
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
	AmountDero uint64 `json:"amountDero"` // atomic units of Asset (DERO's when Asset is "")
	AmountXmr  uint64 `json:"amountXmr"`  // piconero
	// Asset is the DERO-side leg (U1): "" is DERO, otherwise the 64 lower-case hex SCID of the token or NFA unit
	// the maker locks instead. Decimals is that asset's scale as both sides agreed it; nothing on chain states it,
	// so the two companions compare it rather than trust one side's display.
	Asset    string `json:"asset"`
	Decimals uint64 `json:"decimals,omitempty"`
	// Stipend is the DERO, in atomic units, the maker sends the taker once his lock is stable and before she locks
	// any XMR, so that she can pay for the Claim — and for Punish — without having bought DERO anywhere (Path 0).
	Stipend uint64 `json:"stipend"`
	// DeroNet is the DERO network the maker's daemon is on — mainnet, testnet or simulator — and Net the Monero
	// network. Both are terms (GAP-007): a lock on one network paid for on another is the loss they rule out.
	DeroNet string `json:"deroNet"`
	Net     string `json:"net"`     // Monero network name
	T1Delta uint64 `json:"t1Delta"` // blocks from Bob's lock to t1
	T2Delta uint64 `json:"t2Delta"` // blocks from t1 to t2
}

// StipendRecord is the maker's DERO gift to the taker (Path 0) as this party knows it: for Bob the transfer he
// sent, for Alice the one her own wallet shows arriving. Tx is "" until one is known.
type StipendRecord struct {
	Tx     string `json:"tx,omitempty"`
	Amount uint64 `json:"amount,omitempty"`
	Height uint64 `json:"height,omitempty"` // the block it was mined in
	// AttemptHeight is Bob's DERO height when he last handed a stipend to his wallet without seeing it mined. A
	// DERO transaction is valid for TX_VALIDITY_HEIGHT = 11 blocks after the height it was built at
	// (blockchain/miner_block.go, enforced in blockchain.go), so once StipendSettleBlocks have passed it is mined
	// or it never will be, and his own wallet's history then says which — the rule that keeps the stipend single.
	AttemptHeight uint64 `json:"attemptHeight,omitempty"`
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
	// AttemptHeight is the DERO height at which this companion last handed a transaction for this lock to its wallet,
	// or was about to. For the Lock it is saved before the send (AttemptsRecorded), so a process that dies while its Lock
	// waits in a mempool leaves it on disk; a refund sent without a readable answer (refundBlind) sets it too. The
	// transaction may still be in a mempool, where DERO.GetSC cannot show it, so no second Lock goes out until every
	// block the first could be mined in is stable (Swap.lockWindowEnd).
	AttemptHeight uint64 `json:"attemptHeight,omitempty"`
	// AttemptsRecorded: this state records every Lock attempt in AttemptHeight before its transaction is sent. A state
	// saved by an earlier build, which recorded one only after a call returned an error, lacks it, and a restart into
	// dero_locking then takes the height it restarts at (or the cutoff, past which nothing was sent) as one its Lock may
	// have been built at.
	AttemptsRecorded bool   `json:"attemptsRecorded,omitempty"`
	SeenHeight       uint64 `json:"seenHeight,omitempty"` // Alice: DERO height when the lock first passed every check
	// LockedHeight is Bob's DERO height when his Lock came back executed: the lock's block is at or below it, so
	// once the stable height reaches it the lock can no longer be replaced, and only then does the stipend go out.
	LockedHeight uint64 `json:"lockedHeight,omitempty"`
	// ClaimSeenHeight is the DERO height at which a party first read st == 1. The contract does not store the block
	// the Claim was mined in; but the block is at or below the height it was read at, so Bob sweeps (Invariant 9)
	// and Alice stops watching (U3) only once this height is at or below the daemon's stable height: from then on
	// DERO cannot replace the Claim's block.
	ClaimSeenHeight uint64 `json:"claimSeenHeight,omitempty"`
	// PunishSeenHeight is ClaimSeenHeight's twin for the taker's Punish (htlc.bas v3): she counts it as done only once
	// this height is stable, because a Punish undone by a replaced block and re-mined at or after t2 + g2 is refused.
	PunishSeenHeight uint64 `json:"punishSeenHeight,omitempty"`
	// ReclaimSeenHeight is the same for the maker's Reclaim (htlc.bas v3): he counts it as done only once this height is
	// stable, because a Reclaim undone by a replaced block and not mined again leaves the lock open, and from t2 + g2
	// only he can move it.
	ReclaimSeenHeight uint64 `json:"reclaimSeenHeight,omitempty"`
	// RefundSeenHeight is the same for the maker's Refund (MT-B27): a Refund undone by a replaced block and mined again
	// at or after t2 is refused, and the lock is then open to Punish, so he counts it as done only once this is stable.
	RefundSeenHeight uint64 `json:"refundSeenHeight,omitempty"`
	T1               uint64 `json:"t1"`
	T2               uint64 `json:"t2"`
	Amount           uint64 `json:"amount"`
}

// ContractRecord is an htlc.bas v3 install's arguments as this party read them (MT-A03(a), MT-J13).
type ContractRecord struct {
	W1Min uint64 `json:"w1min,omitempty"`
	W2Min uint64 `json:"w2min,omitempty"`
	G2    uint64 `json:"g2,omitempty"`
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

// Save writes the whole state atomically: temp file, fsync, rename. Nothing irreversible happens between a
// phase change and this call.
func (s *State) Save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.path, b)
}

// writeAtomic is the durable write both files use, the state and the used-secrets register: a temporary file beside
// path, then fsync and rename. The temporary file is new, private and unguessable from the moment it exists:
//   - createPrivate makes it with O_EXCL (CREATE_NEW on Windows), so a file already there, planted by anyone who can
//     write the directory, is never opened, truncated or reused;
//   - it is owner-only in the create call itself: 0600 elsewhere, and on Windows a protected DACL granting this account
//     alone, in the SECURITY_ATTRIBUTES CreateFileW is given (acl_windows.go). It used to be path + ".tmp", created
//     with the directory's inherited ACL and restricted a moment later, and a process that opened it in that moment kept
//     its handle, and the key share written through it, after the ACL changed (GAP-020, the review of 2026-09-12);
//   - its name ends in 64 random bits (tempName), so nobody can prepare one in advance.
//
// The rename keeps the file's own security descriptor, so the state is owner-only under its real name too.
func writeAtomic(path string, b []byte) error {
	tmp, err := tempName(path)
	if err != nil {
		return err
	}
	f, err := createPrivate(tmp)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// tempName is a temporary name beside path that nobody can guess: path + ".tmp-" + 16 random hex digits.
func tempName(path string) (string, error) {
	var r [8]byte
	if _, err := io.ReadFull(rand.Reader, r[:]); err != nil {
		return "", err
	}
	return path + ".tmp-" + hex.EncodeToString(r[:]), nil
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

// ErrSecretReused is the refusal: wrapped in the error Record returns when another swap holds the entry — a
// secret, or one of the public identities a swap takes as its own (its joint address, the lock it accepted, a
// Monero payment its gate counted), which must never serve a second swap either.
var ErrSecretReused = errors.New("already used by another swap")

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
		return fmt.Errorf("%w: this %s belongs to swap %s, recorded %s", ErrSecretReused, kind, prev.Swap, prev.At.Format(time.RFC3339))
	}
	u.Secrets[h] = UsedSecret{Swap: swap, Kind: kind, At: time.Now().UTC()}
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(u.path, b)
}
