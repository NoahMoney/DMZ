// vaultsigner is one federation signer for contracts/vault.bas. It watches a foreign chain for deposits to the
// attested per-user addresses, votes Mint after the confirmation policy, derives and attests deposit addresses
// for new user indexes, pays pending withdrawals through the foreign backend and votes Released. Run one per
// signer wallet; k of them must agree before the contract does anything.
//
//	go run ./vaultsigner -wallet 127.0.0.1:30003 -vault <scid> -backend sim -sim ../research/crosschain/poc/sim-foreign.json [-confs 2] [-once]
//
// Backends: `sim` reads a JSON file that stands in for the foreign chain (height, deposits, releases). Real
// backends (zcash, monero, solana) plug into the same calls: Deposits(), Address(index), Release(j, dest, amount),
// Reserve(). Release MUST be idempotent for a given j (look the payment up before paying): the signer records an
// intent before it pays and may call Release again after a crash.
//
// Exactly-once release: every withdrawal goes through a durable intent (paying → paid → voted) written with
// fsync + rename before the next step; the state file is loaded strictly (a corrupt file stops the signer rather
// than starting it with an empty memory).
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
	"strconv"
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
	statePath = flag.String("state", "", "local vote memory and release intents (default: vaultsigner-<signer address prefix>.json)")
	confs     = flag.Int64("confs", 0, "confirmations before a deposit is voted (0: the network table's value for the backend; sim: 2)")
	interval  = flag.Duration("interval", 20*time.Second, "poll interval")
	once      = flag.Bool("once", false, "one pass, then exit")
	timeout   = flag.Duration("timeout", 180*time.Second, "per-tx timeout")
	yes       = flag.Bool("yes", false, "required on mainnet: this signer pays real withdrawals on the foreign chain")
	maxOutBps = flag.Uint64("max-withdrawal-bps", 2000, "a single withdrawal above this share of the foreign reserve (basis points) is not paid: the signer votes Halt(1) instead; 0 disables")
)

// suspiciousWithdrawal says whether one withdrawal is too large a share of the reserve to pay without a human:
// amount > reserve × bps / 10000. Exact integers; a zero reserve makes every withdrawal suspicious.
func suspiciousWithdrawal(amount, reserve, bps uint64) bool {
	if bps == 0 {
		return false
	}
	return amount > (reserve/10000)*bps+((reserve%10000)*bps)/10000
}

// Deposit is a confirmed-or-not transfer to a user's deposit address on the foreign chain.
type Deposit struct {
	TxID   string `json:"txid"`   // 64 hex
	Index  uint64 `json:"index"`  // user deposit index (vault ua_ key)
	Amount uint64 `json:"amount"` // foreign base units
	Height int64  `json:"height"` // foreign height it was included at
}

type Release struct {
	J      uint64 `json:"j"`
	Dest   string `json:"dest"`
	Amount uint64 `json:"amount"`
	FTx    string `json:"ftx"`
}

// Backend is what a foreign chain must provide to the signer.
type Backend interface {
	Height() (int64, error)
	Deposits() ([]Deposit, error)
	Address(index uint64) (string, error)
	Release(j uint64, dest string, amount uint64) (string, error) // idempotent per j; returns the foreign txid
	Reserve() (uint64, error)                                     // foreign units the federation holds
}

// --- sim backend: a JSON file edited by tools/simvault ---

type simFile struct {
	Height   int64     `json:"height"`
	Deposits []Deposit `json:"deposits"`
	Releases []Release `json:"releases"`
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
func (s simBackend) Deposits() ([]Deposit, error) {
	f, err := s.load()
	return f.Deposits, err
}
func (s simBackend) Address(index uint64) (string, error) {
	// stands in for a subaddress (Monero) / diversified address (Orchard) derived from the shared viewing key:
	// deterministic in the index, so every signer derives the same string
	return "sim:" + strconv.FormatUint(index, 10), nil
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

// --- durable local state: votes already cast and release intents ---

// Intent is one withdrawal's progress. paying: recorded before any foreign payment; paid: the foreign txid is
// known; voted: the Released vote was submitted. A signer that restarts finishes every intent from its state.
type Intent struct {
	J      uint64 `json:"j"`
	Dest   string `json:"dest"`
	Amount uint64 `json:"amount"`
	FTx    string `json:"ftx,omitempty"`
	State  string `json:"state"`
}

type memory struct {
	Minted   map[string]bool    `json:"minted"`
	Attested map[string]bool    `json:"attested"`
	Intents  map[string]*Intent `json:"intents"`
	Halted   map[string]bool    `json:"halted"` // halt votes cast, keyed by height bucket
	path     string
}

func newMemory(path string) *memory {
	return &memory{Minted: map[string]bool{}, Attested: map[string]bool{}, Intents: map[string]*Intent{}, Halted: map[string]bool{}, path: path}
}

// loadMemory is strict: a missing file is a fresh signer, an unreadable one stops the signer.
func loadMemory(path string) (*memory, error) {
	m := newMemory(path)
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
	if m.Minted == nil {
		m.Minted = map[string]bool{}
	}
	if m.Attested == nil {
		m.Attested = map[string]bool{}
	}
	if m.Intents == nil {
		m.Intents = map[string]*Intent{}
	}
	if m.Halted == nil {
		m.Halted = map[string]bool{}
	}
	return m, nil
}

// save writes the whole state to a temp file, fsyncs it and renames it over the old one.
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
		log.Fatalf("backend %q (%s, rpc %s, %d confirmations) is not implemented yet: see docs/history/HISTORY.md (day log) wrapper backlog", *backend, p.Name, p.RPC, p.Confirmations)
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
	me := rpc.Address(*wallet)
	if *statePath == "" {
		*statePath = "vaultsigner-" + me[len(me)-12:] + ".json" // one file per signer identity, not per port
	}
	// one daemon per state file: a second one would overwrite intents and, with a backend that does not look
	// payments up by j, pay twice. The lock names the pid; remove it by hand only when no signer is running.
	lock := *statePath + ".lock"
	lf, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		log.Fatalf("%s exists: another signer runs on this state (or crashed without removing it): %v", lock, err)
	}
	fmt.Fprintf(lf, "%d\n", os.Getpid())
	lf.Close()
	defer os.Remove(lock)
	mem, err := loadMemory(*statePath)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("signer %s\nvault  %s\nstate  %s (%d intents)\n", me, *vault, *statePath, len(mem.Intents))
	for {
		pass(c, be, me, mem)
		if *once {
			return
		}
		time.Sleep(*interval)
	}
}

// send submits one contract call: estimated when the daemon can judge it, with a fixed fee for entrypoints gated on
// BLOCK_HEIGHT() (the estimator runs contracts at height 0). Returns the txid and the height it was included at.
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

// vote submits a vote and reports whether the contract counted it: inclusion is not execution, so the vote's own
// counter key (mc_/dc_/rc_/hc_) must have grown. counter builds the key from the height the vote landed at.
// A vote that was included but not counted is not remembered, and the next pass casts it again; the contract
// refuses a signer's duplicate, so at worst a fee is spent.
func vote(c *rpc.Client, me, entry string, args []rpc.Arg, fixedFee uint64, counter func(height int64) string) bool {
	before := c.GetSC(*vault, false)
	_, height, ok := send(c, me, entry, args, fixedFee)
	if !ok {
		return false
	}
	key := counter(height)
	after := c.GetSC(*vault, false)
	if after.U0(key) <= before.U0(key) {
		fmt.Printf("%s %s was included but not counted (%s unchanged): will vote again\n", time.Now().Format("15:04:05"), entry, key)
		return false
	}
	return true
}

func sha256hex(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func pass(c *rpc.Client, be Backend, me string, mem *memory) {
	st := c.GetSC(*vault, false)
	if st.U("sealed") == 0 {
		fmt.Println("vault not sealed yet")
		return
	}
	se := strconv.FormatUint(st.U("se"), 10)
	he := strconv.FormatUint(st.U("he"), 10)
	// a halt vote is keyed by slash epoch, halt epoch, target state and 2000-block bucket (vault.bas Halt)
	haltKey := func(want uint64) func(int64) string {
		return func(h int64) string {
			return "hc_" + se + "_" + he + "_" + strconv.FormatUint(want, 10) + "_" + strconv.FormatInt(h/2000, 10)
		}
	}
	// halt memory follows the halt epoch: after a resume (new he) a fresh shortfall must be voted again
	haltOnce := func(reason string) {
		bucket := "he" + he + "_" + strconv.FormatInt(c.Height()/2000, 10)
		if mem.Halted[bucket] {
			return
		}
		fmt.Printf("%s; voting Halt(1)\n", reason)
		if vote(c, me, "Halt", []rpc.Arg{rpc.U("want", 1)}, 0, haltKey(1)) {
			mem.Halted[bucket] = true
			if err := mem.save(); err != nil {
				log.Fatal(err)
			}
		}
	}
	// 0. reserve check: the foreign chain must hold the wrapped supply plus every burn not yet paid (sup was
	//    reduced at the burn, the reserve only at the payment); our own paid intents are already out of both
	reserve, err := be.Reserve()
	if err != nil {
		log.Printf("reserve: %v", err)
		return
	}
	pending := uint64(0)
	for j := uint64(0); j < st.U("wn"); j++ {
		js := strconv.FormatUint(j, 10)
		if st.U("w_"+js+"_st") != 0 {
			continue
		}
		if it := mem.Intents[js]; it != nil && it.State != "paying" {
			continue
		}
		pending += st.U("w_" + js + "_v")
	}
	if sup := st.U("sup"); reserve < sup+pending {
		haltOnce(fmt.Sprintf("RESERVE SHORTFALL: foreign reserve %d < wrapped supply %d + unpaid withdrawals %d", reserve, sup, pending))
		return
	}
	if st.U("halted") != 0 {
		fmt.Println("vault halted: no mints, no releases")
		return
	}
	// 1. attest deposit addresses for new user indexes
	for j := uint64(0); j < st.U("un"); j++ {
		js := strconv.FormatUint(j, 10)
		if st.Has("da_"+js) || mem.Attested[js] {
			continue
		}
		addr, err := be.Address(j)
		if err != nil {
			log.Printf("address(%d): %v", j, err)
			continue
		}
		ver := strconv.FormatUint(st.U0("dver_"+js), 10)
		key := "dc_" + se + "_" + js + "_" + ver + "_" + sha256hex(addr)
		if vote(c, me, "Attest", []rpc.Arg{rpc.U("j", j), rpc.S("addr", addr)}, 0, func(int64) string { return key }) {
			mem.Attested[js] = true
			if err := mem.save(); err != nil {
				log.Fatal(err)
			}
		}
	}
	// 2. mint confirmed deposits
	height, err := be.Height()
	if err != nil {
		log.Printf("foreign height: %v", err)
		return
	}
	deps, err := be.Deposits()
	if err != nil {
		log.Printf("deposits: %v", err)
		return
	}
	for _, d := range deps {
		if len(d.TxID) < 64 || len(d.TxID) > 80 || d.Amount == 0 || height-d.Height < *confs {
			continue
		}
		if st.Has("md_"+d.TxID) || mem.Minted[d.TxID] {
			continue
		}
		js := strconv.FormatUint(d.Index, 10)
		if !st.Has("ua_" + js) {
			fmt.Printf("deposit %s… to unknown index %d\n", d.TxID[:8], d.Index)
			continue
		}
		// what the contract will refuse at quorum it refuses for every signer: say why instead of voting into it.
		// The funds stay at the deposit address until the cap is raised (TRUST_STATEMENT: Cap); a deposit above
		// maxdep is never minted and needs the operator runbook (refund from the federation's address).
		if sup, cap := st.U("sup"), st.U("cap"); sup+d.Amount < sup || sup+d.Amount > cap {
			fmt.Printf("deposit %s… (%d) waits: supply %d + %d exceeds the cap %d; k signers must SetCap first\n", d.TxID[:8], d.Amount, sup, d.Amount, cap)
			continue
		}
		if maxdep := st.U0("maxdep"); maxdep != 0 && d.Amount > maxdep {
			fmt.Printf("deposit %s… (%d) is above the per-deposit limit %d: never minted, operator refund needed\n", d.TxID[:8], d.Amount, maxdep)
			continue
		}
		// v2: the contract pays the address registered under the index; the vote names (j, id, amount) and is
		// counted under mc_<se>_<id>_<sha256(j|amount)>
		key := "mc_" + se + "_" + d.TxID + "_" + sha256hex(js+"|"+strconv.FormatUint(d.Amount, 10))
		if vote(c, me, "Mint", []rpc.Arg{rpc.U("j", d.Index), rpc.S("id", d.TxID), rpc.U("amount", d.Amount)}, 0, func(int64) string { return key }) {
			mem.Minted[d.TxID] = true
			if err := mem.save(); err != nil {
				log.Fatal(err)
			}
		}
	}
	// 3. withdrawals: pay on the foreign chain only once the burn is final on DERO (C6) and the vault's withdrawal
	//    delay has passed (the delay is the honest signers' time to notice a bad burn and halt), then vote Released.
	//    Released is gated on BLOCK_HEIGHT() and the estimator runs at height 0, so it goes out with a fixed fee.
	wdelay := int64(st.U0("wdelay"))
	tip := c.Height()
	voteReleased := func(j uint64, ftx string) bool {
		key := "rc_" + se + "_" + strconv.FormatUint(j, 10) + "_" + ftx
		return vote(c, me, "Released", []rpc.Arg{rpc.U("j", j), rpc.S("ftx", ftx)}, 3000, func(int64) string { return key })
	}
	stable := c.StableHeight()
	for j := uint64(0); j < st.U("wn"); j++ {
		js := strconv.FormatUint(j, 10)
		it := mem.Intents[js]
		if it != nil && it.State == "voted" {
			continue
		}
		if st.U("w_"+js+"_st") != 0 { // released by the quorum: close our intent if we had one, and never judge it again
			if it != nil {
				it.State = "voted"
				if err := mem.save(); err != nil {
					log.Fatal(err)
				}
			}
			continue
		}
		h := int64(st.U("w_" + js + "_h"))
		if !withdrawalFinal(h, stable) {
			fmt.Printf("withdrawal %d waits for DERO finality (burned at %d, stable height %d)\n", j, h, stable)
			continue
		}
		if tip < h+wdelay {
			fmt.Printf("withdrawal %d waits for the withdrawal delay (burned at %d, delay %d, tip %d)\n", j, h, wdelay, tip)
			continue
		}
		dest, amount := st.Str("w_"+js+"_to"), st.U("w_"+js+"_v")
		// the size test is taken once, before this daemon has paid anything for it; a withdrawal already in an
		// intent was judged when the reserve still held it
		if it == nil && suspiciousWithdrawal(amount, reserve, *maxOutBps) {
			haltOnce(fmt.Sprintf("SUSPICIOUS WITHDRAWAL %d: %d is more than %d bps of the reserve %d; not paying", j, amount, *maxOutBps, reserve))
			continue
		}
		if err := processWithdrawal(mem, be, j, dest, amount, voteReleased, nil); err != nil {
			log.Printf("withdrawal %d: %v", j, err)
		}
	}
	after := c.GetSC(*vault, false)
	fmt.Printf("%s vault: users %d, supply %d / cap %d, withdrawals %d, halted %d\n", time.Now().Format("15:04:05"), after.U("un"), after.U("sup"), after.U("cap"), after.U("wn"), after.U("halted"))
}
