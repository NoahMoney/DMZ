// vaultsigner is one federation signer for contracts/vault.bas v3. It watches a foreign chain for deposits to the
// attested per-index addresses, votes Mint after the confirmation and finality policy, derives and attests deposit
// addresses for new indexes, pays pending withdrawals through the foreign backend and votes Released. Run one per
// signer wallet; k of them must agree before the contract does anything.
//
//	go run ./vaultsigner -network simulator -backend sim -wallet 127.0.0.1:30003 -vault <scid> -sim ../research/crosschain/poc/sim-foreign.json [-once]
//
// Backends: `sim` reads and appends to a JSON file. It simulates a foreign chain, it does not observe one — a
// deposit in it is a line somebody typed and a release in it is a line this signer typed — so it is refused on
// every DERO network but the simulator. The real backends (zcash, monero, solana) implement the same interface
// and none of them is built, so there is no configuration in which a testnet or mainnet vault can be signed for
// today. -backend has no default: which of those two a signer is running is the operator's statement to make,
// not this tool's to assume. Release MUST be idempotent for a given j (look the payment up before paying):
// the signer records an intent before it pays and may call Release again after a crash.
//
// Exactly-once release: every withdrawal goes through a durable intent (paying -> paid -> voted) written with
// fsync + rename before the next step; the state file is loaded strictly and is bound to one vault.
//
// Every vote is proved by reading back the daemon's OWN vote key (<f>v_<scope>_<address> holds the choice that
// signer made), so a co-signer voting in the same block can never be mistaken for our own vote landing.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	rpc "veil/tools/simrpc"
)

var (
	daemon    = flag.String("daemon", "127.0.0.1:20000", "daemon rpc")
	wallet    = flag.String("wallet", "127.0.0.1:30003", "this signer's wallet rpc")
	vault     = flag.String("vault", "", "vault.bas scid")
	backend   = flag.String("backend", "", "required, no default: the foreign backend. sim is a JSON file and is accepted only with -network simulator; zcash, monero and solana are planned and exit")
	network   = flag.String("network", "simulator", "DERO network the vault lives on: simulator, testnet, mainnet (selects the foreign-chain parameters)")
	simPath   = flag.String("sim", "../research/crosschain/poc/sim-foreign.json", "sim backend file")
	statePath = flag.String("state", "", "local vote memory and release intents (default: vaultsigner-<vault prefix>-<signer suffix>.json)")
	confs     = flag.Int64("confs", 0, "confirmations before a deposit is voted (0: the network table's value for the backend; sim: 2)")
	// A recorded payment that stays invisible must eventually be re-attempted rather than waited on forever:
	// there is no operator to notice a stuck withdrawal. Release is idempotent per j, so re-asking is cheap.
	verifyTries = flag.Int("verify-tries", 20, "passes a recorded foreign payment may stay unverifiable before the backend is asked to pay that withdrawal again")
	interval    = flag.Duration("interval", 20*time.Second, "poll interval")
	once        = flag.Bool("once", false, "one pass, then exit")
	timeout     = flag.Duration("timeout", 180*time.Second, "per-tx timeout")
	yes         = flag.Bool("yes", false, "required on mainnet: this signer pays real withdrawals on the foreign chain")
)

// Deposit is one foreign output paid to an attested deposit address. TxID is 64 lowercase hex characters (the raw
// 32-byte transaction id) and Vout is the output index within it: together they are the deposit's identity, and
// the contract builds the same id from the raw bytes so a spelling can never create a second one.
type Deposit struct {
	TxID   string `json:"txid"`
	Vout   uint64 `json:"vout"`
	Index  uint64 `json:"index"`  // the vault index (ua_ key) the address belongs to
	Amount uint64 `json:"amount"` // foreign base units
	Height int64  `json:"height"` // foreign height it was included at
}

func (d Deposit) id() string { return d.TxID + ":" + strconv.FormatUint(d.Vout, 10) }

type Release struct {
	J      uint64 `json:"j"`
	Dest   string `json:"dest"`
	Amount uint64 `json:"amount"`
	FTx    string `json:"ftx"`
}

// Backend is what a foreign chain must provide to the signer. Every method must fail rather than guess.
type Backend interface {
	Height() (int64, error)
	// Finalized is the highest foreign height this chain treats as irreversible under the network policy. A
	// deposit above it is never voted, however many confirmations it has.
	Finalized() (int64, error)
	Deposits() ([]Deposit, error)
	// Verify re-fetches one deposit at vote time and says whether it is still there with the same amount and
	// index. A deposit that vanished in a reorganisation must never be voted.
	Verify(d Deposit) (bool, error)
	Address(index uint64) (string, error)
	// ValidDest says whether a destination string is spendable on this chain. A withdrawal whose destination is
	// not is vetoed rather than paid into nowhere.
	ValidDest(dest string) bool
	Release(j uint64, dest string, amount uint64) (string, error) // idempotent per j; returns the foreign txid
	// VerifyRelease re-reads the foreign chain and says whether ftx really pays amount to dest for withdrawal j.
	// Release returns a string; this is what turns that string into a fact. A signer votes Released only for an
	// ftx it has verified itself, so a coordinator or a peer cannot make it certify a payment nobody made.
	VerifyRelease(j uint64, ftx string, dest string, amount uint64) (bool, error)
	Reserve() (uint64, error) // foreign units the federation holds
	// Health fails when the node or wallet is unreachable, unsynced or missing its signing material. The daemon
	// does nothing at all in that state rather than acting on a partial view.
	Health() error
}

// pickBackend chooses the foreign backend for a DERO network and settles the confirmation count. It is separate
// from main so the choice can be tested without a daemon: the rule it enforces is the whole safety of the
// wrapped assets, because a signer votes Mint on what its backend says it saw.
//
// Three refusals, in order of how badly the mistake ends:
//
//   - no -backend. The flag has no default. A default here is a tool deciding, on the operator's behalf and
//     silently, whether the coins behind every wrapped unit are real.
//   - -backend sim on any network but the simulator. simBackend reads a JSON file: Deposits returns the rows in
//     it, Verify re-reads the same rows, Release appends a row and invents a txid, and Address returns the
//     literal string "sim:<index>". Anyone who can write that file can mint wrapped units against no foreign
//     coin at all, and a withdrawal through it burns a holder's units and pays nobody. -yes does not buy this;
//     nothing does.
//   - -backend zcash, monero or solana anywhere. They are not built. The signer exits instead of degrading to
//     something that guesses.
//
// The sim branch also stops setting -confs on a real network, which it used to do before any of these checks.
func pickBackend(network, backend string, confs *int64, simPath string) (Backend, error) {
	params, ok := chainTable[network]
	if !ok {
		return nil, fmt.Errorf("unknown network %q (simulator, testnet, mainnet)", network)
	}
	switch backend {
	case "":
		return nil, errors.New(`-backend is required and has no default: "sim" (a JSON file, -network simulator only) or one of zcash, monero, solana (not implemented yet)`)
	case "sim":
		if network != "simulator" {
			return nil, fmt.Errorf(`-backend sim is a JSON file, not a chain: nothing in it is evidence that a foreign coin exists, so it is allowed only with -network simulator (this is -network %s). No foreign backend is implemented, so no %s vault can be signed for yet`, network, network)
		}
		if *confs == 0 {
			*confs = 2
		}
		return simBackend{simPath}, nil
	case "zcash", "monero", "solana":
		p := params[backend]
		if *confs == 0 || (network == "mainnet" && *confs < p.Confirmations) {
			*confs = p.Confirmations // the network table is the floor on mainnet, the default everywhere
		}
		return nil, fmt.Errorf("backend %q (%s, rpc %s, %d confirmations) is not implemented yet: docs/HANDOVER.md section 5 is the build plan", backend, p.Name, p.RPC, p.Confirmations)
	}
	return nil, fmt.Errorf("unknown backend %q (sim, zcash, monero, solana)", backend)
}

// --- sim backend: a JSON file edited by tools/simvault ---

type simFile struct {
	Height    int64     `json:"height"`
	Finalized int64     `json:"finalized"` // 0 means "height minus the confirmation policy"
	Deposits  []Deposit `json:"deposits"`
	Releases  []Release `json:"releases"`
}

type simBackend struct{ path string }

func (s simBackend) load() (simFile, error) {
	var f simFile
	b, err := os.ReadFile(s.path)
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal(b, &f)
}
func (s simBackend) save(f simFile) error {
	b, _ := json.MarshalIndent(f, "", "  ")
	return os.WriteFile(s.path, b, 0o644)
}
func (s simBackend) Height() (int64, error) { f, err := s.load(); return f.Height, err }
func (s simBackend) Finalized() (int64, error) {
	f, err := s.load()
	if err != nil {
		return 0, err
	}
	if f.Finalized > 0 {
		return f.Finalized, nil
	}
	return f.Height - *confs, nil
}
func (s simBackend) Deposits() ([]Deposit, error) {
	f, err := s.load()
	return f.Deposits, err
}
func (s simBackend) Verify(d Deposit) (bool, error) {
	f, err := s.load()
	if err != nil {
		return false, err
	}
	for _, x := range f.Deposits {
		if x.TxID == d.TxID && x.Vout == d.Vout {
			return x.Amount == d.Amount && x.Index == d.Index && x.Height == d.Height, nil
		}
	}
	return false, nil
}
func (s simBackend) Address(index uint64) (string, error) {
	// stands in for a subaddress (Monero) / diversified address (Orchard) derived from the shared viewing key:
	// deterministic in the index, so every signer derives the same string
	return "sim:" + strconv.FormatUint(index, 10), nil
}
func (s simBackend) ValidDest(dest string) bool {
	return len(dest) >= 16 && !strings.ContainsAny(dest, " \t\n")
}
func (s simBackend) Health() error {
	_, err := s.load()
	return err
}
func (s simBackend) Release(j uint64, dest string, amount uint64) (string, error) {
	f, err := s.load()
	if err != nil {
		return "", err
	}
	for _, r := range f.Releases {
		if r.J == j {
			return r.FTx, nil // already paid (by this signer before a crash, or by the coordinator)
		}
	}
	sum := sha256.Sum256([]byte("release|" + strconv.FormatUint(j, 10) + "|" + dest + "|" + strconv.FormatUint(amount, 10)))
	ftx := hex.EncodeToString(sum[:])
	f.Releases = append(f.Releases, Release{J: j, Dest: dest, Amount: amount, FTx: ftx})
	return ftx, s.save(f)
}
func (s simBackend) VerifyRelease(j uint64, ftx string, dest string, amount uint64) (bool, error) {
	f, err := s.load()
	if err != nil {
		return false, err
	}
	for _, r := range f.Releases {
		if r.J == j {
			// every field must match: a payment recorded for this j that went somewhere else, or paid a
			// different amount, is not the payment this withdrawal asked for
			return r.FTx == ftx && r.Dest == dest && r.Amount == amount, nil
		}
	}
	return false, nil // no payment on the foreign chain for this withdrawal at all
}
func (s simBackend) Reserve() (uint64, error) {
	f, err := s.load()
	if err != nil {
		return 0, err
	}
	var in, out uint64
	for _, d := range f.Deposits {
		in += d.Amount
	}
	for _, r := range f.Releases {
		out += r.Amount
	}
	if out > in {
		return 0, nil
	}
	return in - out, nil
}

// --- durable local state: what this signer has done, and what it knows about the foreign chain ---

// Intent is one withdrawal's progress. paying: recorded before any foreign payment; paid: the foreign txid is
// known; voted: the Released vote was counted. A signer that restarts finishes every intent from its state.
type Intent struct {
	J      uint64 `json:"j"`
	Dest   string `json:"dest"`
	Amount uint64 `json:"amount"`
	FTx    string `json:"ftx,omitempty"`
	State  string `json:"state"`
	// consecutive passes in which the recorded payment could not be verified on the foreign chain
	Tries int `json:"tries,omitempty"`
}

type memory struct {
	Vault   string             `json:"vault"` // the state file belongs to exactly one vault
	Intents map[string]*Intent `json:"intents"`
	Halted  map[string]bool    `json:"halted"` // halt votes cast, keyed by halt epoch and height bucket
	// LiveAt is the foreign height at which this signer first saw index j's deposit address published. A deposit
	// older than that is ignored: an address derived from a future index can be paid before anyone owns it, and
	// crediting such a payment would hand one user's coins to whoever registers the index next.
	LiveAt map[string]int64 `json:"liveAt"`
	path   string
}

func newMemory(path, vault string) *memory {
	return &memory{Vault: vault, Intents: map[string]*Intent{}, Halted: map[string]bool{}, LiveAt: map[string]int64{}, path: path}
}

// loadMemory is strict: a missing file is a fresh signer, an unreadable one or one belonging to another vault
// stops the signer rather than replaying its intents against the wrong withdrawal indexes.
func loadMemory(path, vault string) (*memory, error) {
	m := newMemory(path, vault)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, m); err != nil {
		return nil, fmt.Errorf("state %s is corrupt (%v): refusing to start with an empty memory", path, err)
	}
	if m.Vault != "" && m.Vault != vault {
		return nil, fmt.Errorf("state %s belongs to vault %s, not %s: its intents name that vault's withdrawal indexes", path, m.Vault, vault)
	}
	m.Vault = vault
	if m.Intents == nil {
		m.Intents = map[string]*Intent{}
	}
	if m.Halted == nil {
		m.Halted = map[string]bool{}
	}
	if m.LiveAt == nil {
		m.LiveAt = map[string]int64{}
	}
	return m, nil
}

func (m *memory) save() error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
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
	return os.Rename(tmp, m.path)
}

// withdrawalFinal says whether a burn recorded at DERO height h is final: at or below the daemon's stable
// height. A burn above it can still be reorganised out, and a foreign payment made for it would be a loss.
func withdrawalFinal(h, stable int64) bool { return h > 0 && h <= stable }

// inflightUnits totals the withdrawals the contract has recorded and not yet closed: burned, so their claim is
// still counted in `sup`, but possibly already paid abroad, so their coins may already have left the reserve.
// This is the exact width of the window in which an entirely correct federation looks short of its own supply.
// Every signer computes it from the same contract state at the same block, so every signer agrees.
// owedUnits is the reserve the federation must still be able to show: every claim except the ones whose coins
// have legitimately left, or are leaving, to pay a burn the contract has not yet closed. A reserve below this is
// a real shortfall and worth halting for; a reserve merely below `sup` is what an honest withdrawal looks like.
func owedUnits(sup, inflight uint64) uint64 {
	if sup <= inflight {
		return 0
	}
	return sup - inflight
}

func inflightUnits(st rpc.SCState) uint64 {
	var total uint64
	for j := uint64(0); j < st.U("wn"); j++ {
		js := strconv.FormatUint(j, 10)
		if st.U("w_"+js+"_st") == 0 {
			total += st.U("w_" + js + "_v")
		}
	}
	return total
}

// processWithdrawal drives one withdrawal j through its intent. It is the only place that pays on the foreign
// chain. crash, when non-nil, is a test hook: returning true at a stage abandons the call as a process death
// would, after the durable step that preceded it.
func processWithdrawal(mem *memory, be Backend, j uint64, dest string, amount uint64, markPaying func(j uint64) bool, voteReleased func(j uint64, ftx string) bool, crash func(stage string) bool) error {
	js := strconv.FormatUint(j, 10)
	it := mem.Intents[js]
	if it == nil {
		it = &Intent{J: j, Dest: dest, Amount: amount, State: "paying"}
		mem.Intents[js] = it
		if err := mem.save(); err != nil { // nothing is paid before the intent is on disk
			delete(mem.Intents, js)
			return err
		}
		if crash != nil && crash("after-intent") {
			return errors.New("crashed after recording the intent")
		}
	}
	if it.Dest != dest || it.Amount != amount {
		return fmt.Errorf("withdrawal %d: recorded intent (%s, %d) differs from the chain (%s, %d); operator must reconcile", j, it.Dest, it.Amount, dest, amount)
	}
	if it.State == "paying" {
		// Say so on chain before the coins move. The intent file above is this signer's own record and the
		// quorum cannot read it, so without the on-chain mark k signers could Veto a withdrawal whose coins had
		// already left and hand the burner their units back too. Fail closed: if the mark is not recorded, do
		// not pay. Nothing is lost by waiting a pass, and the mark expires on its own, so a vault that cannot
		// reach the chain stalls rather than freezes.
		if !markPaying(j) {
			return fmt.Errorf("withdrawal %d: could not record on chain that it is being paid; not paying this pass", j)
		}
		if crash != nil && crash("after-mark") {
			return errors.New("crashed after marking, before paying")
		}
		ftx, err := be.Release(j, it.Dest, it.Amount) // idempotent per j: a repeat after a crash finds the earlier payment
		if err != nil {
			return err
		}
		if ftx == "" || len(ftx) > 128 { // the contract refuses such an ftx; recording it would strand the intent in "paid"
			return fmt.Errorf("withdrawal %d: the backend returned an unusable foreign txid (%d bytes); operator must check the payment", j, len(ftx))
		}
		if crash != nil && crash("after-pay") {
			return errors.New("crashed after paying, before recording the txid")
		}
		it.FTx, it.State = ftx, "paid"
		if err := mem.save(); err != nil {
			return err
		}
		if crash != nil && crash("after-paid") {
			return errors.New("crashed after recording the payment")
		}
	}
	if it.State == "paid" {
		// Verify before every vote, not once after paying. A signer that crashed between paying and voting
		// re-checks on restart, and a signer whose backend coordinates with peers never certifies a txid on a
		// peer's word. A payment that is not visible yet fails closed: no vote this pass, retried on the next.
		ok, err := be.VerifyRelease(j, it.FTx, it.Dest, it.Amount)
		if err != nil || !ok {
			// Not voting is right, but never voting is a freeze, and there is no operator to unfreeze it. A
			// payment that stays invisible is eventually treated as one that did not happen: the intent goes
			// back to "paying" and the backend is asked again. That is safe exactly because Release must be
			// idempotent per j — a correct backend finds its earlier payment and returns the same txid, so this
			// costs one lookup; a backend that cannot look up its own payments was never fit to hold a reserve.
			it.Tries++
			if it.Tries < *verifyTries {
				if e := mem.save(); e != nil {
					return e
				}
				if err != nil {
					return fmt.Errorf("withdrawal %d: cannot verify the payment %s (attempt %d of %d): %w", j, it.FTx, it.Tries, *verifyTries, err)
				}
				return fmt.Errorf("withdrawal %d: the foreign chain does not show %s paying %d to %s (attempt %d of %d); not voting Released", j, it.FTx, it.Amount, it.Dest, it.Tries, *verifyTries)
			}
			// Re-asking the backend is only safe while the on-chain mark still blocks a Veto. If the mark has
			// lapsed, keep the intent as "paid" and keep verifying: the coins may well be there, and a second
			// payment is the one outcome worse than a slow one.
			if !markPaying(j) {
				return fmt.Errorf("withdrawal %d: %s is still not visible after %d attempts and the Paying mark has lapsed; keeping the record and verifying again next pass rather than paying twice", j, it.FTx, *verifyTries)
			}
			stale := it.FTx
			it.FTx, it.State, it.Tries = "", "paying", 0
			if e := mem.save(); e != nil {
				return e
			}
			return fmt.Errorf("withdrawal %d: %s was never visible on the foreign chain after %d attempts; re-asking the backend to pay it (it must find any earlier payment for %d and return the same txid)", j, stale, *verifyTries, j)
		}
		it.Tries = 0
		if !voteReleased(j, it.FTx) {
			return nil // the quorum may already be complete, or the vote is refused for now: keep the intent
		}
		if crash != nil && crash("after-vote") {
			return errors.New("crashed after voting")
		}
		it.State = "voted"
		return mem.save()
	}
	return nil
}

// lockStale is how long a lock may go untouched before another daemon takes it over. It must be comfortably
// longer than one pass, which waits for its own transactions to be included, so a slow but healthy signer is
// never displaced by an impatient one.
const lockStale = 30 * time.Minute

// takeLock claims the state file for this process. A lock file that exists but has not been touched within
// lockStale belonged to a process that died without cleaning up, and is taken over: refusing to start would
// leave the vault a signer short with nobody able to delete the file.
func takeLock(path string) error {
	lf, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		st, serr := os.Stat(path)
		if serr != nil {
			return fmt.Errorf("%s: %v", path, err)
		}
		if age := time.Since(st.ModTime()); age < lockStale {
			return fmt.Errorf("%s was touched %s ago: another signer is running on this state (wait %s, or stop it)",
				path, age.Round(time.Second), (lockStale - age).Round(time.Second))
		}
		// stale: whoever held it is gone. Truncate rather than remove-then-create, so two daemons racing here
		// both end up writing the same file instead of one deleting the other's fresh lock.
		lf, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("%s is stale but cannot be taken over: %v", path, err)
		}
		log.Printf("%s was last touched more than %s ago; taking it over", path, lockStale)
	}
	fmt.Fprintf(lf, "%d\n", os.Getpid())
	lf.Close()
	lockFile = path
	return nil
}

// heartbeat keeps this daemon's claim fresh. Closing the returned channel stops it.
func heartbeat() chan struct{} {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(lockStale / 6)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if lockFile != "" {
					now := time.Now()
					_ = os.Chtimes(lockFile, now, now)
				}
			}
		}
	}()
	return stop
}

// lockFile is removed on a clean exit and on a signal — but nothing can guarantee that, which is exactly why
// the claim above is held by a heartbeat rather than by this file's existence. The old comment here promised
// removal "on every exit path, including a fatal one", which was not true of log.Fatal or of SIGKILL.
var lockFile string

func release() {
	if lockFile != "" {
		os.Remove(lockFile)
		lockFile = ""
	}
}

func die(format string, a ...interface{}) {
	release()
	log.Fatalf(format, a...)
}

func main() {
	// `vaultsigner keygen | identity | relay` (coordwire.go) are read before the daemon's own flags
	if ok, code := runSubcommand(os.Args[1:], os.Stdout, os.Stderr); ok {
		os.Exit(code)
	}
	flag.Parse()
	if len(*vault) != 64 {
		log.Fatal("-vault must be the 64-hex scid of vault.bas")
	}
	params, ok := chainTable[*network]
	if !ok {
		log.Fatalf("unknown network %q (simulator, testnet, mainnet)", *network)
	}
	if *network == "mainnet" && !*yes {
		log.Fatal("mainnet: this signer pays real withdrawals; pass -yes to run it")
	}
	if *confs < 0 {
		log.Fatal("-confs cannot be negative")
	}
	be, err := pickBackend(*network, *backend, confs, *simPath)
	if err != nil {
		log.Fatalf("%v", err)
	}
	fmt.Printf("network %s; foreign parameters: monero %s (%d conf), zcash %s (%d conf), solana %s (%d conf); this backend %s at %d confirmations\n", *network,
		params["monero"].RPC, params["monero"].Confirmations, params["zcash"].RPC, params["zcash"].Confirmations, params["solana"].RPC, params["solana"].Confirmations, *backend, *confs)
	c := &rpc.Client{Daemon: *daemon, Timeout: *timeout}
	// the daemon must be the network the flag says: the flag picks the -yes gate and the confirmation table
	if testnet, name := c.Info(); (*network == "mainnet") == testnet {
		log.Fatalf("-network %s but the daemon at %s reports %q (testnet=%v)", *network, *daemon, name, testnet)
	}
	if v := c.GetSC(*vault, false).U("v"); v != 3 {
		log.Fatalf("vault %s is version %d; this signer speaks version 3", *vault, v)
	}
	me := rpc.Address(*wallet)
	// contract keys name a signer by ADDRESS_STRING, which is always the mainnet spelling even on testnet
	dvmMe := rpc.DVMAddress(me)
	if dvmMe == "" {
		log.Fatalf("wallet %s reported an address this tool cannot parse: %s", *wallet, me)
	}
	if *statePath == "" { // one file per (vault, signer): intents name withdrawal indexes of one vault only
		*statePath = "vaultsigner-" + (*vault)[:8] + "-" + me[len(me)-12:] + ".json"
	}
	// One daemon per state file: a second would overwrite intents and, with a backend that does not look payments
	// up by j, pay twice. The lock is held by a heartbeat rather than by its own existence, because its existence
	// cannot be trusted: simrpc exits through log.Fatal on any RPC trouble, which skips every deferred call, and
	// SIGKILL and a power cut skip the signal handler too. A lock whose presence alone locks therefore takes the
	// signer offline until a human deletes a file — and there is no human. A lock older than lockStale is taken
	// over instead, so the worst case is a duplicate vote, which the contract refuses, rather than a silent
	// signer, which is a vault drifting towards a quorum it can no longer reach.
	if err := takeLock(*statePath + ".lock"); err != nil {
		log.Fatalf("%v", err)
	}
	defer release()
	stop := heartbeat()
	defer close(stop)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; release(); os.Exit(130) }()

	mem, err := loadMemory(*statePath, *vault)
	if err != nil {
		die("%v", err)
	}
	fmt.Printf("signer %s\nvault  %s\nstate  %s (%d intents)\n", me, *vault, *statePath, len(mem.Intents))
	// signer coordination (coordwire.go): backends that sign with their peers hold coordHandle, which fails closed
	// until this has run
	if *coordPath != "" {
		node, err := startCoord(c, *vault, dvmMe)
		if err != nil {
			die("coordination: %v", err)
		}
		defer node.Close()
	}
	for {
		pass(c, be, me, dvmMe, mem)
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

// send submits one contract call: estimated when the daemon can judge it, with a fixed fee for entrypoints gated
// on BLOCK_HEIGHT() (the estimator runs contracts at height 0). Returns the txid and the inclusion height.
func send(c *rpc.Client, me, entry string, args []rpc.Arg, fixedFee uint64) (string, int64, bool) {
	all := append([]rpc.Arg{rpc.S("entrypoint", entry)}, args...)
	fee := fixedFee
	if fee == 0 {
		gas, err := c.Estimate(me, "", *vault, nil, all)
		if err != nil {
			fmt.Printf("%s %s refused at estimate: %v\n", time.Now().Format("15:04:05"), entry, err)
			return "", 0, false
		}
		fee = gas.GasStorage + 200
	}
	txid, height := c.SendWithFee(*wallet, me, *vault, nil, all, fee)
	if len(txid) < 8 {
		fmt.Printf("%s %s: the wallet returned no txid\n", time.Now().Format("15:04:05"), entry)
		return "", 0, false
	}
	fmt.Printf("%s %s %v tx %s… height %d\n", time.Now().Format("15:04:05"), entry, args, txid[:8], height)
	return txid, height, true
}

// vote submits a vote and reports whether the CONTRACT recorded it as ours. Inclusion is not execution, and a
// counter can also be moved by a co-signer in the same block, so the proof is this signer's own vote key
// (<f>v_<scope>_<address>), whose value is the choice it made. scope is built from the inclusion height because
// the contract scopes operational votes by the 2000-block bucket it executed in.
func vote(c *rpc.Client, me, entry string, args []rpc.Arg, fixedFee uint64, voteKey func(height int64) string, choice string) bool {
	_, height, ok := send(c, me, entry, args, fixedFee)
	if !ok {
		return false
	}
	key := voteKey(height)
	st := c.GetSC(*vault, false)
	if !st.Has(key) || st.Str(key) != choice {
		fmt.Printf("%s %s was included but the contract did not record our vote (%s): will vote again\n", time.Now().Format("15:04:05"), entry, key)
		return false
	}
	return true
}

func sha256hex(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// said keeps a per-process note of deposits already explained, so a permanent condition is reported once rather
// than on every poll.
var said = map[string]bool{}

func pass(c *rpc.Client, be Backend, me, dvmMe string, mem *memory) {
	if err := be.Health(); err != nil { // a partial view of the foreign chain is worse than no action at all
		log.Printf("foreign backend unhealthy, doing nothing this pass: %v", err)
		return
	}
	st := c.GetSC(*vault, false)
	if st.U("sealed") == 0 {
		fmt.Println("vault not sealed yet")
		return
	}
	se, he := strconv.FormatUint(st.U("se"), 10), strconv.FormatUint(st.U("he"), 10)
	// every operational vote lives in scope <se>_<he>_<bucket>; the bucket comes from the height the vote landed at
	scope := func(h int64) string { return se + "_" + he + "_" + strconv.FormatInt(h/2000, 10) }
	voteKey := func(f, suffix string) func(int64) string {
		return func(h int64) string { return f + "v_" + scope(h) + "_" + suffix + "_" + dvmMe }
	}
	// A halt is a CONDITION, not a decision somebody takes: this signer votes to halt while the reserve is
	// short of the supply and votes to resume once it is not. Every honest signer computes the same thing from
	// the same public state, so the quorum forms and dissolves on its own with nobody to ask. One vote per
	// direction per 2000-block bucket keeps a boundary case from oscillating at the cost of fees.
	haltVote := func(want uint64, reason string) {
		bucket := "he" + he + "_" + strconv.FormatUint(want, 10) + "_" + strconv.FormatInt(c.Height()/2000, 10)
		if mem.Halted[bucket] {
			return
		}
		fmt.Printf("%s; voting Halt(%d)\n", reason, want)
		if vote(c, me, "Halt", []rpc.Arg{rpc.U("want", want)}, 0, func(h int64) string { return "hv_" + scope(h) + "_" + dvmMe }, strconv.FormatUint(want, 10)) {
			// If that vote completed the quorum the contract has opened a new epoch, and every vote this pass
			// still casts must be keyed by it: scope closes over he, so refresh it here.
			he = strconv.FormatUint(c.GetSC(*vault, false).U("he"), 10)
			mem.Halted[bucket] = true
			if err := mem.save(); err != nil {
				die("%v", err)
			}
		}
	}
	// 0. the reserve invariant. A claim leaves the foreign reserve when the federation pays it, and leaves `sup`
	//    when the Released quorum records that payment. Those are necessarily different moments — the payment is
	//    the very thing the vote is about — so between them the reserve is legitimately short of `sup` by exactly
	//    the withdrawals in flight. The invariant an honest signer can check at one block is therefore
	//
	//        reserve >= sup - inflight,   inflight = every burned withdrawal the contract has not closed
	//
	//    Comparing the reserve to `sup` alone is not a stricter version of this: it halts on every ordinary
	//    withdrawal, and because vault.bas Released refuses while halted, `sup` can then never fall and the halt
	//    can never clear. That freezes the vault on its first withdrawal with no operator to lift it. It is not
	//    hypothetical: tools/simvault/e2e.sh saw halted=1 straight after the payment and recorded it as a size
	//    breaker firing, which is how it went unnoticed for two cycles.
	//
	//    inflight is a pure function of contract state at one block, deliberately. Narrowing it — by the release
	//    height, or by what this signer believes it has paid — would make two honest signers at slightly
	//    different tips compute different answers, and one of them would halt the other's honest payment.
	reserve, err := be.Reserve()
	if err != nil {
		log.Printf("reserve: %v", err)
		return
	}
	sup := st.U("sup")
	inflight := inflightUnits(st)
	owed := owedUnits(sup, inflight)
	tip, stable := c.Height(), c.StableHeight()
	// halted is the height the lease lapses at (0: not halted); a halt nobody renews expires on its own
	isHalted := st.U("halted") > uint64(tip)
	// A halt stops what ADDS exposure — attesting, minting, and this signer starting a new payment. It must not
	// stop the truthful recording of a payment already made: Released is votable while halted (it was not, and
	// that let a paid withdrawal be refunded on chain once its Paying mark lapsed), and a paid intent that is
	// never voted keeps sup from falling, so the shortfall could never clear.
	halted := false
	if reserve < owed {
		if !isHalted {
			haltVote(1, fmt.Sprintf("RESERVE SHORTFALL: foreign reserve %d < supply %d less %d in flight", reserve, sup, inflight))
		}
		halted = true
	} else if isHalted {
		// the shortfall that halted this vault has cleared: resume, so no human is needed to restart it
		haltVote(0, fmt.Sprintf("reserve %d now covers the %d that is owed", reserve, owed))
		halted = true
	}
	if halted {
		fmt.Println("vault halted: no attest, no mint, no new payment; withdrawals already paid still get their Released vote")
	}

	var fh, final int64
	if !halted {
		if fh, err = be.Height(); err != nil {
			log.Printf("foreign height: %v", err)
			return
		}
		if final, err = be.Finalized(); err != nil {
			log.Printf("foreign finality: %v", err)
			return
		}
	}

	// 1. attest deposit addresses for new indexes, and remember the foreign height each address went live at
	for j := uint64(0); !halted && j < st.U("un"); j++ {
		js := strconv.FormatUint(j, 10)
		if st.Has("da_" + js) {
			if _, seen := mem.LiveAt[js]; !seen { // first time we see this address published
				mem.LiveAt[js] = fh
				if err := mem.save(); err != nil {
					die("%v", err)
				}
			}
			continue
		}
		addr, err := be.Address(j)
		if err != nil {
			log.Printf("address(%d): %v", j, err)
			continue
		}
		ver := strconv.FormatUint(st.U0("dver_"+js), 10)
		ah := sha256hex(addr)
		if vote(c, me, "Attest", []rpc.Arg{rpc.U("j", j), rpc.S("addr", addr)}, 0, voteKey("d", js+"_"+ver), ah) {
			if _, seen := mem.LiveAt[js]; !seen {
				mem.LiveAt[js] = fh
				if err := mem.save(); err != nil {
					die("%v", err)
				}
			}
		}
	}

	// 2. mint deposits that are confirmed, final, and no older than the address they were paid to
	var deps []Deposit
	if !halted {
		if deps, err = be.Deposits(); err != nil {
			log.Printf("deposits: %v", err)
			return
		}
	}
	for _, d := range deps {
		if len(d.TxID) != 64 || d.Amount == 0 {
			continue
		}
		id, js := d.id(), strconv.FormatUint(d.Index, 10)
		if st.Has("mint_" + id) { // already minted or refunded, on chain: nothing to do, ever
			continue
		}
		if fh-d.Height < *confs || d.Height > final {
			continue // not enough confirmations, or above what this chain calls final
		}
		if !st.Has("ua_" + js) {
			fmt.Printf("deposit %s to unknown index %d\n", id[:12], d.Index)
			continue
		}
		if !st.Has("da_" + js) {
			continue // the address is not attested yet; the deposit waits
		}
		live, seen := mem.LiveAt[js]
		if !seen {
			mem.LiveAt[js] = fh // conservative: only what arrives from now on counts for an index we just met
			if err := mem.save(); err != nil {
				die("%v", err)
			}
			live = fh
		}
		if d.Height < live {
			if !said[id] {
				said[id] = true
				fmt.Printf("deposit %s predates index %d's address (foreign height %d < %d): not credited\n", id[:12], d.Index, d.Height, live)
			}
			continue
		}
		if amount := d.Amount; amount < st.U("mindep") {
			// Not "operator refund needed": there is no operator, and on a ring-signature or shielded chain the
			// federation cannot see who paid, so there is no address to return it to either. This is a real,
			// permanent loss with no mechanism behind it — which is why the minimum is published in the app
			// beside the deposit address, before the wallet is even connected. Say what is true here too.
			fmt.Printf("deposit %s (%d) is below the minimum %d: it can never be minted, and the chain carries no return address, so it cannot be sent back\n", id[:12], amount, st.U("mindep"))
			continue
		}
		// over the cap or over the window is a WAIT, not a refusal: the daemon retries every pass, so headroom
		// from a later burn or the window sliding credits the deposit with nobody having to intervene
		if sup, cap := st.U("sup"), st.U("cap"); sup+d.Amount < sup || sup+d.Amount > cap {
			if !said[id+"-cap"] {
				said[id+"-cap"] = true
				fmt.Printf("deposit %s (%d) waits for headroom: supply %d + %d exceeds the cap %d\n", id[:12], d.Amount, sup, d.Amount, cap)
			}
			continue
		}
		// re-read the deposit from the chain at vote time: one that vanished in a reorganisation is never voted
		if ok, err := be.Verify(d); err != nil || !ok {
			fmt.Printf("deposit %s did not verify at vote time (%v): not voting\n", id[:12], err)
			continue
		}
		th := sha256hex(js + "|" + strconv.FormatUint(d.Amount, 10))
		vote(c, me, "Mint", []rpc.Arg{rpc.U("j", d.Index), rpc.H("txid", d.TxID), rpc.U("vout", d.Vout), rpc.U("amount", d.Amount)}, 0, voteKey("m", id), th)
	}

	// 3. withdrawals
	vetoPass(c, be, me, dvmMe, mem, st, scope)
	// Paying is not a vote — one signer's mark protects the withdrawal for every signer — so it is confirmed by
	// reading the mark back rather than a vote key. A mark a co-signer already set is just as good as our own.
	markPaying := func(j uint64) bool {
		k := "w_" + strconv.FormatUint(j, 10) + "_p"
		if cur := c.GetSC(*vault, false); cur.Has(k) {
			// The mark is one-shot and lapses after 2000 blocks (vault.bas Veto line 25). A lapsed mark is not
			// protection: Veto can land on the withdrawal again, so paying under it is the double payment the
			// mark exists to prevent. Fail closed; the quorum settles it by Veto or by our Released vote.
			fresh := uint64(c.Height()) < cur.U(k)+2000
			if !fresh {
				fmt.Printf("withdrawal %d: its Paying mark at %d has lapsed; not paying under it\n", j, cur.U(k))
			}
			return fresh
		}
		if _, _, ok := send(c, me, "Paying", []rpc.Arg{rpc.U("j", j)}, 0); !ok {
			return false
		}
		return c.GetSC(*vault, false).Has(k)
	}
	voteReleased := func(j uint64, ftx string) bool {
		// Released is gated on BLOCK_HEIGHT(), which the daemon's estimator runs at height 0, so it goes out
		// with a fixed fee: a refused vote is mined and reverted, which is the price of the delay.
		return vote(c, me, "Released", []rpc.Arg{rpc.U("j", j), rpc.S("ftx", ftx)}, 3000,
			voteKey("r", strconv.FormatUint(j, 10)), ftx)
	}
	for j := uint64(0); j < st.U("wn"); j++ {
		js := strconv.FormatUint(j, 10)
		it := mem.Intents[js]
		if it != nil && it.State == "voted" && st.U("w_"+js+"_st") == 0 {
			// "voted" is not final until the chain closes the withdrawal. Every vote is scoped by the halt epoch
			// and the 2000-block bucket, so a Halt flip — or two signers whose passes straddle a bucket boundary
			// — leaves this signer's vote in a dead scope while the co-signers vote in the live one, and the
			// quorum never forms. Reproduced on the simulator: two Released votes per signer, none counted.
			// If our vote does not stand in the live scope, cast it again; the record and the txid are kept.
			if st.Has(voteKey("r", js)(tip)) {
				continue // our vote stands in the current scope: nothing to do until a co-signer completes it
			}
			fmt.Printf("withdrawal %d: our Released vote is in a superseded scope; voting again\n", j)
			it.State = "paid"
		} else if it != nil && it.State == "voted" {
			continue
		}
		// No NEW payment while halted. A payment is new when nobody has marked the withdrawal: if a signer has
		// (w_<j>_p exists), our Release is the idempotent lookup of that signer's payment — Release must find an
		// earlier payment for j and return its txid, which is already the contract every backend is held to —
		// and the Released vote that follows is exactly what a halt must not block. markPaying still refuses a
		// lapsed mark, so a withdrawal whose window has passed is left to the quorum.
		if halted && (it == nil || it.State != "paid") && !st.Has("w_"+js+"_p") {
			continue
		}
		if st.U("w_"+js+"_st") != 0 { // released or vetoed by the quorum: close our intent and never judge it again
			if it != nil {
				it.State = "voted"
				if err := mem.save(); err != nil {
					die("%v", err)
				}
			}
			continue
		}
		h := int64(st.U("w_" + js + "_h"))
		if !withdrawalFinal(h, stable) {
			fmt.Printf("withdrawal %d waits for DERO finality (burned at %d, stable height %d)\n", j, h, stable)
			continue
		}
		// The contract decides when a withdrawal may be paid, and it decided at burn time: w_<j>_rel already
		// carries the extra delay a large withdrawal earns. The daemon has no say and no override, because a
		// vault with no operator must never depend on one. Stopping a withdrawal takes a k-of-n Veto or Halt.
		if rel := int64(st.U("w_" + js + "_rel")); tip < rel {
			fmt.Printf("withdrawal %d is not releasable yet (tip %d, releasable at %d)\n", j, tip, rel)
			continue
		}
		dest, amount := st.Str("w_"+js+"_to"), st.U("w_"+js+"_v")
		// vetoPass has already voted to return this one. Opening an intent on it would be the worst of both: the
		// payment cannot succeed, and vetoPass skips any withdrawal that has an intent, so the veto quorum could
		// never form in a later bucket and the burner would end with neither units nor coins. An intent that is
		// already open is always finished, never abandoned, because the coins may already have been sent.
		if it == nil && !be.ValidDest(dest) {
			continue
		}
		// st is the snapshot from the top of the pass, and a pass spans many blocks because every vote waits for
		// inclusion. A co-signer's Veto or Released — or our own veto, cast moments ago — may have reached the
		// quorum since. Re-read this withdrawal's status immediately before paying anything abroad.
		if c.GetSC(*vault, false).U("w_"+js+"_st") != 0 {
			if it != nil { // settled by the quorum while this pass ran: close our intent, as the branch above does
				it.State = "voted"
				if err := mem.save(); err != nil {
					die("%v", err)
				}
			}
			continue
		}
		if err := processWithdrawal(mem, be, j, dest, amount, markPaying, voteReleased, nil); err != nil {
			log.Printf("withdrawal %d: %v", j, err)
		}
	}
	after := c.GetSC(*vault, false)
	// print the in-flight total next to the reserve: without it a correct vault looks short of its own supply
	// during every ordinary withdrawal, which is precisely the misreading that hid the halt deadlock
	fmt.Printf("%s vault: indexes %d, supply %d / cap %d, withdrawals %d, halted %v (lapses %d), reserve %d (in flight %d)\n", time.Now().Format("15:04:05"),
		after.U("un"), after.U("sup"), after.U("cap"), after.U("wn"), after.U("halted") > uint64(tip), after.U("halted"), reserve, inflightUnits(after))
}

// vetoPass votes to return withdrawals the foreign chain cannot accept. It NEVER vetoes one this signer has
// already started paying: the units would go back to the burner while the coins were already sent, and the
// reserve would be short by that amount for good.
func vetoPass(c *rpc.Client, be Backend, me, dvmMe string, mem *memory, st rpc.SCState, scope func(int64) string) {
	for j := uint64(0); j < st.U("wn"); j++ {
		js := strconv.FormatUint(j, 10)
		if st.U("w_"+js+"_st") != 0 {
			continue
		}
		if it := mem.Intents[js]; it != nil {
			continue // we have already paid, or are paying: vetoing now would double-spend the reserve
		}
		dest := st.Str("w_" + js + "_to")
		if be.ValidDest(dest) {
			continue
		}
		fmt.Printf("withdrawal %d names a destination this chain cannot pay (%q): voting Veto\n", j, dest)
		vote(c, me, "Veto", []rpc.Arg{rpc.U("j", j)}, 0,
			func(h int64) string { return "gv_" + scope(h) + "_" + js + "_" + dvmMe }, "1")
	}
}
