// vaultsigner is one federation signer for contracts/vault.bas v3. It watches a foreign chain for deposits to the
// attested per-index addresses, votes Mint after the confirmation and finality policy, derives and attests deposit
// addresses for new indexes, pays pending withdrawals through the foreign backend and votes Released. Run one per
// signer wallet; k of them must agree before the contract does anything.
//
//	go run ./vaultsigner -wallet 127.0.0.1:30003 -vault <scid> -backend sim -sim ../research/crosschain/poc/sim-foreign.json [-once]
//
// Backends: `sim` reads a JSON file that stands in for the foreign chain. Real backends (zcash, monero, solana)
// implement the same interface. Release MUST be idempotent for a given j (look the payment up before paying):
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
	backend   = flag.String("backend", "sim", "foreign backend: sim (zcash, monero, solana: planned)")
	network   = flag.String("network", "simulator", "DERO network the vault lives on: simulator, testnet, mainnet (selects the foreign-chain parameters)")
	simPath   = flag.String("sim", "../research/crosschain/poc/sim-foreign.json", "sim backend file")
	statePath = flag.String("state", "", "local vote memory and release intents (default: vaultsigner-<vault prefix>-<signer suffix>.json)")
	confs     = flag.Int64("confs", 0, "confirmations before a deposit is voted (0: the network table's value for the backend; sim: 2)")
	interval  = flag.Duration("interval", 20*time.Second, "poll interval")
	once      = flag.Bool("once", false, "one pass, then exit")
	timeout   = flag.Duration("timeout", 180*time.Second, "per-tx timeout")
	yes       = flag.Bool("yes", false, "required on mainnet: this signer pays real withdrawals on the foreign chain")
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
	Reserve() (uint64, error)                                     // foreign units the federation holds
	// Health fails when the node or wallet is unreachable, unsynced or missing its signing material. The daemon
	// does nothing at all in that state rather than acting on a partial view.
	Health() error
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

// processWithdrawal drives one withdrawal j through its intent. It is the only place that pays on the foreign
// chain. crash, when non-nil, is a test hook: returning true at a stage abandons the call as a process death
// would, after the durable step that preceded it.
func processWithdrawal(mem *memory, be Backend, j uint64, dest string, amount uint64, voteReleased func(j uint64, ftx string) bool, crash func(stage string) bool) error {
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

// lockFile is removed on every exit path, including a fatal one and a signal, so an ordinary Ctrl-C or one bad
// RPC does not take a signer offline until a human deletes a file.
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
	var be Backend
	switch *backend {
	case "sim":
		be = simBackend{*simPath}
		if *confs == 0 {
			*confs = 2
		}
	case "zcash", "monero", "solana":
		p := params[*backend]
		if *confs == 0 || (*network == "mainnet" && *confs < p.Confirmations) {
			*confs = p.Confirmations // the network table is the floor on mainnet, the default everywhere
		}
		log.Fatalf("backend %q (%s, rpc %s, %d confirmations) is not implemented yet: docs/HANDOVER.md section 5 is the build plan", *backend, p.Name, p.RPC, p.Confirmations)
	default:
		log.Fatalf("unknown backend %q", *backend)
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
	// one daemon per state file: a second would overwrite intents and, with a backend that does not look
	// payments up by j, pay twice. The lock names the pid and is removed on every exit path.
	lf, err := os.OpenFile(*statePath+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		log.Fatalf("%s.lock exists: another signer runs on this state (or was killed with SIGKILL): %v", *statePath, err)
	}
	lockFile = *statePath + ".lock"
	fmt.Fprintf(lf, "%d\n", os.Getpid())
	lf.Close()
	defer release()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; release(); os.Exit(130) }()

	mem, err := loadMemory(*statePath, *vault)
	if err != nil {
		die("%v", err)
	}
	fmt.Printf("signer %s\nvault  %s\nstate  %s (%d intents)\n", me, *vault, *statePath, len(mem.Intents))
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
			mem.Halted[bucket] = true
			if err := mem.save(); err != nil {
				die("%v", err)
			}
		}
	}
	// 0. the reserve invariant, which v3 makes exact: a burn keeps its claim in sup until the payment is
	//    recorded, so the foreign reserve must simply cover sup at every moment.
	reserve, err := be.Reserve()
	if err != nil {
		log.Printf("reserve: %v", err)
		return
	}
	sup := st.U("sup")
	if reserve < sup {
		if st.U("halted") == 0 {
			haltVote(1, fmt.Sprintf("RESERVE SHORTFALL: foreign reserve %d < wrapped supply %d", reserve, sup))
		}
		vetoPass(c, be, me, dvmMe, mem, st, scope) // returning funds is the one thing a halted vault must still do
		return
	}
	if st.U("halted") != 0 {
		// the shortfall that halted this vault has cleared: resume, so no human is needed to restart it
		haltVote(0, fmt.Sprintf("reserve %d now covers the supply %d again", reserve, sup))
		vetoPass(c, be, me, dvmMe, mem, st, scope)
		fmt.Println("vault halted: no mints, no releases until the resume votes reach the quorum")
		return
	}

	fh, err := be.Height()
	if err != nil {
		log.Printf("foreign height: %v", err)
		return
	}
	final, err := be.Finalized()
	if err != nil {
		log.Printf("foreign finality: %v", err)
		return
	}

	// 1. attest deposit addresses for new indexes, and remember the foreign height each address went live at
	for j := uint64(0); j < st.U("un"); j++ {
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
	deps, err := be.Deposits()
	if err != nil {
		log.Printf("deposits: %v", err)
		return
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
			fmt.Printf("deposit %s (%d) is below the minimum %d: never minted, operator refund needed\n", id[:12], amount, st.U("mindep"))
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
	tip, stable := c.Height(), c.StableHeight()
	voteReleased := func(j uint64, ftx string) bool {
		// Released is gated on BLOCK_HEIGHT(), which the daemon's estimator runs at height 0, so it goes out
		// with a fixed fee: a refused vote is mined and reverted, which is the price of the delay.
		return vote(c, me, "Released", []rpc.Arg{rpc.U("j", j), rpc.S("ftx", ftx)}, 3000,
			voteKey("r", strconv.FormatUint(j, 10)), ftx)
	}
	for j := uint64(0); j < st.U("wn"); j++ {
		js := strconv.FormatUint(j, 10)
		it := mem.Intents[js]
		if it != nil && it.State == "voted" {
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
		if err := processWithdrawal(mem, be, j, dest, amount, voteReleased, nil); err != nil {
			log.Printf("withdrawal %d: %v", j, err)
		}
	}
	after := c.GetSC(*vault, false)
	fmt.Printf("%s vault: indexes %d, supply %d / cap %d, withdrawals %d, halted %d, reserve %d\n", time.Now().Format("15:04:05"),
		after.U("un"), after.U("sup"), after.U("cap"), after.U("wn"), after.U("halted"), reserve)
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
