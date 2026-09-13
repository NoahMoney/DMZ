package zecswap

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"time"
)

// StateFormat names the layout of the state file; a file of another layout is refused, not guessed at.
const StateFormat = 1

// The two roles.
const (
	RoleMaker = "maker" // holds the DERO-side asset and the secret; locks first; ends with ZEC
	RoleTaker = "taker" // holds ZEC; funds the script second; ends with the DERO-side asset
)

// State is everything one companion knows about one swap, on disk. It is written before every irreversible
// action — every broadcast, every DERO call — so a restart finds the intent and finishes it rather than doing it
// differently: the funding, the claim and the refunds are built once, stored, and only ever re-sent as the same
// bytes. It is written with fsync and rename, created 0600 (and owner-only on Windows, HardenFile), and loaded
// strictly: a missing file is a fresh swap, an unreadable one stops the companion.
//
// The maker's secret lives here and only here until his claim makes it public. The Zcash keys never do: they are
// read from the key file for each signature and zeroed after.
type State struct {
	Format  int    `json:"zecswap"`
	Role    string `json:"role"`
	Phase   string `json:"phase"`
	Done    bool   `json:"done"`
	Outcome string `json:"outcome,omitempty"`
	Note    string `json:"note,omitempty"` // why the machine took the exit it is on
	SwapID  string `json:"swapId,omitempty"`

	ZcashChain  string `json:"zcashChain"` // main, test or regtest, as the node reported it at the first step
	DeroMainnet bool   `json:"deroMainnet"`
	Board       string `json:"board"`
	Escrow      string `json:"escrow"`
	MyDero      string `json:"myDero"`
	MyRaw       string `json:"myRaw"`

	HaveOffer   bool   `json:"haveOffer"`
	OfferID     uint64 `json:"offerId"`
	Hash        string `json:"hash,omitempty"`
	Secret      string `json:"secret,omitempty"` // the maker's, from setup; never the taker's
	GiveAsset   string `json:"giveAsset"`
	GiveAmount  uint64 `json:"giveAmount,omitempty"`
	WantAmount  uint64 `json:"wantAmount,omitempty"`
	WantAddr    string `json:"wantAddr,omitempty"`
	OfferExpiry uint64 `json:"offerExpiry,omitempty"`
	MakerRaw    string `json:"makerRaw,omitempty"`
	TakerRaw    string `json:"takerRaw,omitempty"`
	ClaimPkh    string `json:"claimPkh,omitempty"`
	RefundPkh   string `json:"refundPkh,omitempty"`
	Payout      string `json:"payout,omitempty"` // 40-hex key hash the claim or refund pays
	// Treasury is the maker's: the zecTreasury address his claim pays the swap fee to, fixed at setup
	Treasury string `json:"treasury,omitempty"`
	CLTV     uint32 `json:"cltv,omitempty"`

	AcceptHeight   uint64            `json:"acceptHeight,omitempty"` // DERO height the accept was first read at
	LockDeadline   uint64            `json:"lockDeadline,omitempty"` // the maker locks only below this: P_lock, or board v3's ad_<i> if earlier
	HaveEscrow     bool              `json:"haveEscrow"`
	EscrowID       uint64            `json:"escrowId"`
	TDero          uint64            `json:"tDero,omitempty"`
	LockSeenHeight uint64            `json:"lockSeenHeight,omitempty"`
	Attempts       map[string]uint64 `json:"attempts,omitempty"` // DERO call → height of an attempt whose result was not seen
	DeroTxs        map[string]string `json:"deroTxs,omitempty"`  // DERO call → txid

	FundingHex    string                `json:"fundingHex,omitempty"`
	FundingTxID   string                `json:"fundingTxid,omitempty"`
	FundingExpiry uint32                `json:"fundingExpiry,omitempty"`
	Funding       *Funding              `json:"funding,omitempty"` // as last seen mined
	Scans         map[string]ScanCursor `json:"scans,omitempty"`   // per Zcash node: how far the search for the funding's spend has read
	Refunds       []SignedTx            `json:"refunds,omitempty"`
	RefundBranch  uint32                `json:"refundBranch,omitempty"`
	RefundLevel   int                   `json:"refundLevel,omitempty"`
	RefundFile    string                `json:"refundFile,omitempty"`
	ClaimHex      string                `json:"claimHex,omitempty"`
	ClaimTxID     string                `json:"claimTxid,omitempty"`
	ClaimBranch   uint32                `json:"claimBranch,omitempty"`
	// where the maker last saw his claim mined: if the chain stops carrying it there, that is a reorganisation
	ClaimHeight uint32 `json:"claimHeight,omitempty"`
	ClaimBlock  string `json:"claimBlock,omitempty"`
	// Revealed is written before the claim first goes to any node: from then on the secret may be public
	Revealed bool `json:"revealed,omitempty"`
	// Sighted is the maker's last read of a funding below C_zec, so that its leaving the chain is noticed and said
	Sighted    *Funding `json:"sighted,omitempty"`
	SecretSeen string   `json:"secretSeen,omitempty"` // the taker's copy of s, read from the maker's claim
	SpendTxID  string   `json:"spendTxid,omitempty"`  // the Zcash transaction that spent the script

	DeroClaimHeight uint64 `json:"deroClaimHeight,omitempty"`
	MaxZecTip       uint32 `json:"maxZecTip,omitempty"`

	path string
}

// SignedTx is one stored transaction: its bytes, its id, and the fee multiple it was signed at.
type SignedTx struct {
	Hex     string `json:"hex"`
	TxID    string `json:"txid"`
	FeeMult uint64 `json:"feeMult"`
	Fee     uint64 `json:"fee"`
}

// PhaseInfo is one state of a machine: the states it may move to, whether it is an end, the deadline it runs
// against, and what happens if the companion stops in it — the line `status` prints.
type PhaseInfo struct {
	Next      []string
	Terminal  bool
	Deadline  string
	IfStopped string
}

// MakerPhases is the maker's machine. Every move not listed is refused (CanMove), and the tests walk every pair.
var MakerPhases = map[string]PhaseInfo{
	"": {Next: []string{"posting", "posted", "refused"}, Deadline: "none",
		IfStopped: "nothing has happened"},
	"posting": {Next: []string{"posted"}, Deadline: "none: the offer is not yet known to be on the board",
		IfStopped: "an offer may be on the board under the stored hash; a restart looks for it before posting again"},
	"posted": {Next: []string{"locking", "cancel_wait", "expired", "cancelled"}, Deadline: "the offer's expiry; once accepted, P_lock DERO blocks to lock",
		IfStopped: "an accept goes unanswered: the taker walks after P_lock and the row frees at the offer's expiry. Nothing is locked"},
	"locking": {Next: []string{"locked", "cancel_wait"}, Deadline: "the accept height + P_lock",
		IfStopped: "a Lock may be in flight; a restart waits for it to settle and looks for it before sending another"},
	"locked": {Next: []string{"claiming", "refund_wait"}, Deadline: "the claim cutoff, cltv − R Zcash blocks",
		IfStopped: "a funding goes unclaimed: the taker refunds her ZEC after cltv, and the DERO waits in the escrow until this companion sends Refund after T_dero"},
	"claiming": {Next: []string{"claimed", "refund_wait"}, Deadline: "the claim cutoff",
		IfStopped: "the claim is built and stored; a restart sends those same bytes if the cutoff has not passed, and never after it"},
	"claimed": {Next: []string{"refund_wait", "dero_refunding", "swapped", "claimed_refunded", "lost"}, Deadline: "T_dero",
		IfStopped: "the ZEC is claimed; if the taker never claims the DERO it stays in the escrow until this companion sends Refund after T_dero. A reorganisation that takes the funding off the chain is only noticed, and the funding put back, while this companion runs"},
	"refund_wait": {Next: []string{"dero_refunding", "swapped", "refunded", "claimed_refunded"}, Deadline: "T_dero, then none",
		IfStopped: "the DERO stays in the escrow; only this wallet can Refund it, at any time after T_dero"},
	"dero_refunding": {Next: []string{"swapped", "refunded", "claimed_refunded"}, Deadline: "none: Refund has no end",
		IfStopped: "a Refund may be in flight; a restart reads the escrow and sends it again only if it is still open"},
	"cancel_wait": {Next: []string{"cancelled", "refund_wait"}, Deadline: "the offer's expiry, after which Cancel is allowed",
		IfStopped: "the board row stays accepted until someone cancels it; nothing is locked"},
	"swapped":          {Terminal: true},
	"claimed_refunded": {Terminal: true},
	"refunded":         {Terminal: true},
	"cancelled":        {Terminal: true},
	"expired":          {Terminal: true},
	"refused":          {Terminal: true},
	// lost: the secret was revealed, the taker's DERO Claim landed, and a reorganisation deeper than C_zec took the
	// funding off the chain in a form nobody can mine again (docs/ZEC_SWAP.md §9 Z5)
	"lost": {Terminal: true},
}

// TakerPhases is the taker's machine.
var TakerPhases = map[string]PhaseInfo{
	"": {Next: []string{"accepting", "declined", "taken"}, Deadline: "none",
		IfStopped: "nothing has happened"},
	"accepting": {Next: []string{"accepted", "declined", "taken"}, Deadline: "the offer's expiry",
		IfStopped: "an Accept may be in flight; a restart reads the board before sending another. Nothing is funded"},
	"accepted": {Next: []string{"lock_seen", "walked", "declined"}, Deadline: "the accept height + P_lock",
		IfStopped: "nothing is funded; the maker may lock and will refund his lock after T_dero"},
	"lock_seen": {Next: []string{"funding", "declined"}, Deadline: "funding while T_dero − now ≥ the policy minimum",
		IfStopped: "nothing is funded; the maker refunds his lock after T_dero"},
	"funding": {Next: []string{"funded", "funding_expired"}, Deadline: "the funding's expiry height",
		IfStopped: "the funding and the pre-signed refund are stored; a restart sends the same funding until it expires. Hand the refund file on now"},
	"funded": {Next: []string{"secret_seen", "refunding", "funding_expired"}, Deadline: "cltv",
		IfStopped: "THE ZEC IS AT RISK: from cltv someone must broadcast the pre-signed refund, or the maker can take the ZEC with the secret at any time after T_dero (docs/ZEC_SWAP.md §9 Z17)"},
	"secret_seen": {Next: []string{"dero_claimed", "lost"}, Deadline: "T_dero",
		IfStopped: "THE DERO IS AT RISK: the secret is known; the escrow must be claimed before T_dero, after which the maker refunds it"},
	"dero_claimed": {Next: []string{"swapped", "secret_seen", "lost"}, Deadline: "the Claim's block becoming stable",
		IfStopped: "the Claim is mined; a restart checks it is still there once DERO cannot replace its block"},
	"refunding": {Next: []string{"secret_seen", "refunded", "funding_expired"}, Deadline: "none: the refund must be sent until it is deep",
		IfStopped: "THE ZEC IS AT RISK: the refund must be broadcast and mined before the maker spends the output with the secret (Z17)"},
	"swapped":         {Terminal: true},
	"refunded":        {Terminal: true},
	"walked":          {Terminal: true},
	"declined":        {Terminal: true},
	"taken":           {Terminal: true},
	"funding_expired": {Terminal: true},
	"lost":            {Terminal: true},
}

// Phases is the table for a role.
func Phases(role string) map[string]PhaseInfo {
	if role == RoleMaker {
		return MakerPhases
	}
	return TakerPhases
}

// CanMove says whether a machine of this role may go from one phase to another. A terminal phase goes nowhere —
// not even to itself, so an ended swap's outcome can never be written over; a live phase may stay; everything
// else is in the table or refused.
func CanMove(role, from, to string) bool {
	info, ok := Phases(role)[from]
	if !ok || info.Terminal {
		return false
	}
	if from == to {
		return true
	}
	for _, n := range info.Next {
		if n == to {
			return true
		}
	}
	return false
}

// NewState is a fresh swap for a role, to be written at path.
func NewState(path, role string) *State {
	return &State{Format: StateFormat, Role: role, path: path}
}

// LoadState is strict: a missing file is a fresh swap; a file that cannot be read, is of another layout or role,
// or names a phase its role's machine does not have, stops the companion rather than being replayed.
func LoadState(path, role string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return NewState(path, role), nil
	}
	if err != nil {
		return nil, err
	}
	s, err := ParseState(b)
	if err != nil {
		return nil, fmt.Errorf("state %s: %w", path, err)
	}
	if s.Role != role {
		return nil, fmt.Errorf("state %s belongs to the %s, not the %s", path, s.Role, role)
	}
	s.path = path
	return s, nil
}

// ParseState reads a state file's bytes, strictly.
func ParseState(b []byte) (*State, error) {
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("corrupt (%v): refusing to start with an empty memory", err)
	}
	if s.Format != StateFormat {
		return nil, fmt.Errorf("layout %d, this build reads %d", s.Format, StateFormat)
	}
	if s.Role != RoleMaker && s.Role != RoleTaker {
		return nil, fmt.Errorf("role %q", s.Role)
	}
	if _, ok := Phases(s.Role)[s.Phase]; !ok {
		return nil, fmt.Errorf("the %s has no phase %q", s.Role, s.Phase)
	}
	return &s, nil
}

// Path is where the state lives.
func (s *State) Path() string { return s.path }

// Save writes the whole state atomically.
func (s *State) Save() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.path, b)
}

// HardenFile, when set, is how writeAtomic creates the temporary every file here passes through — the state, the
// taker's pre-signed refund and the two registers: it creates the path it is given, which must not exist, already
// restricted to this account, and returns it open for writing; a name that exists is an error errors.Is reads as
// os.ErrExist. The command line sets it on Windows to CreateOwnerOnly, because there the 0600 a file is created with
// means nothing and the file would inherit its directory's ACL (tools/xmrswap's GAP-020, the same guard). The
// restriction must be the creation itself: Windows checks access only when a file is opened, so a file created with
// the directory's ACL and restricted afterwards could be opened in between, and that handle reads what is written next.
var HardenFile func(path string) (*os.File, error)

// tempSuffix names a temporary: random, so that nobody can know the name before the file exists and create it first.
// A test replaces it.
var tempSuffix = func() (string, error) {
	var r [12]byte
	if _, err := rand.Read(r[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(r[:]), nil
}

// tempTries is how many fresh names writeAtomic draws before it gives up: with 96 random bits, a second draw is needed
// only when someone is creating names beside the file on purpose.
const tempTries = 8

// writeAtomic is the durable write every file here uses: a temporary beside the file, <file>.<random>.tmp, created new
// (O_EXCL and 0600, or HardenFile when set) and never an existing file whoever made it; then write, fsync, close and
// rename. It fails closed: a failure at any step removes the temporary and returns the error, so the file is the old
// one or the new one, and nothing half-written is left beside it.
func writeAtomic(path string, b []byte) error {
	if path == "" {
		return errors.New("zecswap: no path to write")
	}
	f, tmp, err := createTemp(path)
	if err != nil {
		return err
	}
	if err := writeClose(f, b); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("zecswap: writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// createTemp creates a fresh temporary for path. A name that already exists — by chance, or because another account
// that can create files in the directory made it to be written into — is left alone, never opened, and another name
// is drawn.
func createTemp(path string) (*os.File, string, error) {
	create := func(p string) (*os.File, error) { return os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) }
	if HardenFile != nil {
		create = HardenFile
	}
	for i := 0; i < tempTries; i++ {
		s, err := tempSuffix()
		if err != nil {
			return nil, "", fmt.Errorf("zecswap: naming a temporary for %s: %w", path, err)
		}
		tmp := path + "." + s + ".tmp"
		f, err := create(tmp)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", fmt.Errorf("zecswap: creating %s: %w", tmp, err)
		}
		return f, tmp, nil
	}
	return nil, "", fmt.Errorf("zecswap: %d fresh temporary names beside %s were all taken already: refusing to write into one", tempTries, path)
}

// writeClose writes b, flushes it to the disk and closes f, returning the first error.
func writeClose(f *os.File, b []byte) error {
	_, err := f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// StateFileWarning is what the operator must be told about the file's protection on this platform, or "" when the
// file is protected: by its own 0600 elsewhere, and on Windows, where the 0600 is not enforced and the file would
// inherit its directory's ACL, only by HardenFile.
func StateFileWarning(path string) string {
	if runtime.GOOS != "windows" || HardenFile != nil {
		return ""
	}
	return "the state file " + path + " (and, for a maker, the secret in it) is created 0600, which Windows does not enforce: it inherits the directory's ACL. Keep it in a directory only this account can read."
}

// ---- the used-hash register (docs/ZEC_SWAP.md §9 Z8) ----

// UsedHashes is every swap hash this companion has made or adopted, beside the state files, so a secret is never
// used for two swaps: the second taker to fund would be claimable with the first's secret.
type UsedHashes struct {
	Hashes map[string]UsedHash `json:"hashes"`
	path   string
}

// UsedHash is one entry.
type UsedHash struct {
	Swap string    `json:"swap"`
	At   time.Time `json:"at"`
}

// ErrHashReused is the refusal.
var ErrHashReused = errors.New("zecswap: the swap hash is already used by another swap")

// UsedPath is where the register lives for a state file.
func UsedPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "zecswap-used.json")
}

// LoadUsed is strict: a register it cannot read is one it cannot promise to have checked.
func LoadUsed(path string) (*UsedHashes, error) {
	u := &UsedHashes{Hashes: map[string]UsedHash{}, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return u, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, u); err != nil {
		return nil, fmt.Errorf("used-hash register %s is corrupt (%v)", path, err)
	}
	if u.Hashes == nil {
		u.Hashes = map[string]UsedHash{}
	}
	u.path = path
	return u, nil
}

// Record enters hash for swap before the caller uses it; the same swap recording it again is a repeat.
func (u *UsedHashes) Record(hash, swap string) error {
	fresh, err := LoadUsed(u.path)
	if err != nil {
		return err
	}
	for k, v := range fresh.Hashes {
		u.Hashes[k] = v
	}
	if prev, ok := u.Hashes[hash]; ok && prev.Swap != swap {
		return fmt.Errorf("%w: swap %s since %s", ErrHashReused, prev.Swap, prev.At.Format(time.RFC3339))
	}
	u.Hashes[hash] = UsedHash{Swap: swap, At: time.Now().UTC()}
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(u.path, b)
}

// ---- the consumed-funding register ----

// ConsumedFundings is every Zcash funding this companion has credited — claimed — as a swap's, beside the state
// files. The identity of a funding is its outpoint on its network, "<chain>:<txid>:<vout>", bound to the swap it
// was credited to. A maker claims only an outpoint that is not bound to another swap, and binds it durably before
// the claim is built, so one funding is never credited twice nor to two swaps — whatever a lost or copied state
// file, or two swaps with the same script, would otherwise let happen.
type ConsumedFundings struct {
	Fundings map[string]Consumed `json:"fundings"`
	path     string
}

// Consumed is one entry.
type Consumed struct {
	Swap  string    `json:"swap"`
	Board string    `json:"board"`
	Offer uint64    `json:"offer"`
	At    time.Time `json:"at"`
}

// ErrFundingConsumed is the refusal.
var ErrFundingConsumed = errors.New("zecswap: the funding is already credited to another swap")

// ConsumedPath is where the register lives for a state file.
func ConsumedPath(statePath string) string {
	return filepath.Join(filepath.Dir(statePath), "zecswap-consumed.json")
}

// FundingKey is a funding's identity: its network and its outpoint.
func FundingKey(chain, txid string, index uint32) string {
	return chain + ":" + txid + ":" + strconv.FormatUint(uint64(index), 10)
}

// LoadConsumed is strict, as the other register is.
func LoadConsumed(path string) (*ConsumedFundings, error) {
	c := &ConsumedFundings{Fundings: map[string]Consumed{}, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("consumed-funding register %s is corrupt (%v)", path, err)
	}
	if c.Fundings == nil {
		c.Fundings = map[string]Consumed{}
	}
	c.path = path
	return c, nil
}

// Owner is the swap a funding is bound to, read fresh from disk.
func (c *ConsumedFundings) Owner(key string) (Consumed, bool, error) {
	fresh, err := LoadConsumed(c.path)
	if err != nil {
		return Consumed{}, false, err
	}
	v, ok := fresh.Fundings[key]
	return v, ok, nil
}

// Record binds key to swap, durably. The same swap binding it again is a repeat.
func (c *ConsumedFundings) Record(key, swap, board string, offer uint64) error {
	fresh, err := LoadConsumed(c.path)
	if err != nil {
		return err
	}
	c.Fundings = fresh.Fundings
	if prev, ok := c.Fundings[key]; ok && prev.Swap != swap {
		return fmt.Errorf("%w: %s belongs to swap %s (offer %d) since %s", ErrFundingConsumed, key, prev.Swap, prev.Offer, prev.At.Format(time.RFC3339))
	}
	c.Fundings[key] = Consumed{Swap: swap, Board: board, Offer: offer, At: time.Now().UTC()}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(c.path, b)
}

// DefaultDir is where the command line keeps a swap's files when told nothing else: the user's configuration
// directory, outside any repository, so a maker's secret is never swept into a commit by `git add -A` (GAP-028).
func DefaultDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "zecswap"), nil
}

// InsideGitRepo says whether path lies inside a git work tree: a state file there is one a commit can carry off.
func InsideGitRepo(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for dir := filepath.Dir(abs); ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// WrittenFiles is every file the companion writes for a state file (GAP-028): the state itself, which holds a
// maker's secret; its lock; the taker's refund file, unless -refund-file names another; the two registers beside
// it; and the temporary each atomic write passes through, <file>.<random>.tmp, written here as the pattern
// <file>.*.tmp. One is left behind only by a process that died between its creation and its rename.
func WrittenFiles(statePath string) []string {
	var out []string
	for _, p := range []string{statePath, statePath + ".lock", statePath + ".refund.txt", UsedPath(statePath), ConsumedPath(statePath)} {
		out = append(out, p)
		if !(len(p) > 5 && p[len(p)-5:] == ".lock") {
			out = append(out, p+".*.tmp")
		}
	}
	return out
}

// StateFiles lists the swap state files in a directory, sorted.
func StateFiles(dir string) ([]string, error) {
	m, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range m {
		if b := filepath.Base(p); b == "zecswap-used.json" || b == "zecswap-consumed.json" {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
