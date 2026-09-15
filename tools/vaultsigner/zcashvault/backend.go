package zcashvault

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"veil/tools/zcashtx"
)

// Backend is the vault signer's Zcash side: everything tools/vaultsigner's Backend interface asks of a foreign
// chain (docs/HANDOVER.md §5.2), for a vault whose reserve is held in transparent P2SH k-of-n multisig addresses.
// Every method fails rather than guesses. tools/vaultsigner adapts it (zcash_backend.go).

// Deposit mirrors tools/vaultsigner's Deposit. TxID is the transaction id in display order (as explorers and every
// RPC write it), which is the spelling the contract's deposit id is built from.
type Deposit struct {
	TxID   string
	Vout   uint64
	Index  uint64
	Amount uint64
	Height int64
}

// VaultView is what the backend reads from the vault contract: how many indexes are registered (un) and the address
// the quorum attested for each (da_<j>). The adapter reads them from the DERO daemon.
type VaultView interface {
	Indexes() (uint64, error)
	Attested(j uint64) (string, bool, error)
}

// NodeConfig is one Zcash node: a zebrad (or zcashd) JSON-RPC endpoint and its cookie file.
type NodeConfig struct {
	Name   string `json:"name,omitempty"`
	URL    string `json:"url"`
	Cookie string `json:"cookie,omitempty"` // path to the node's cookie file (user:password); never logged
}

// Config is one signer's backend configuration. Every signer of a vault must use the same Network, K, XPubs, and the
// same FinalityDepth, Epoch, ExpiryWindow and MaxInputs, or their transactions will not be the same.
type Config struct {
	Network        string       `json:"network"` // mainnet, testnet or regtest (the Zcash network)
	K              int          `json:"k"`
	XPubs          []string     `json:"xpubs"`   // the n signers' account keys (any order)
	Seed           string       `json:"seed"`    // this signer's seed file, outside every repository
	Account        uint32       `json:"account"` // BIP 44 account index (hardened), usually 0
	Nodes          []NodeConfig `json:"nodes"`
	Ledger         string       `json:"ledger"`                   // this signer's release ledger file
	CoordinatorDir string       `json:"coordinatorDir,omitempty"` // the pilot's shared-directory relay
	FinalityDepth  uint32       `json:"finalityDepth,omitempty"`  // default 100 (Zebra's reorganisation limit)
	Epoch          uint32       `json:"epoch,omitempty"`          // default 10 blocks
	ExpiryWindow   uint32       `json:"expiryWindow,omitempty"`   // default 40 blocks
	MaxInputs      int          `json:"maxInputs,omitempty"`      // default 200
}

// LoadConfig reads a JSON configuration file, strictly.
func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("zcashvault: %s: %w", path, err)
	}
	return c, nil
}

// Backend implements the signer's Zcash side.
type Backend struct {
	d       *Descriptor
	acct    *ExtKey // this signer's private account key
	me      int     // this signer's position in d.XPubs
	ns      *nodes
	vv      VaultView
	coord   Coordinator
	led     *ledger
	rules   buildRules
	timeout time.Duration
	logf    func(string, ...interface{})

	mu     sync.Mutex
	txs    map[string]*zcashtx.Tx // parsed transactions by id: their bytes never change
	mined  map[string]*minedTx    // transactions agreed mined at or below the finalized height
	warned map[string]bool
}

// New builds the backend for the vault with DERO scid vault. coord may be nil when cfg names a CoordinatorDir.
func New(cfg Config, vault string, vv VaultView, coord Coordinator, logf func(string, ...interface{})) (*Backend, error) {
	d, err := NewDescriptor(cfg.Network, cfg.K, cfg.XPubs, vault)
	if err != nil {
		return nil, err
	}
	if cfg.Seed == "" || cfg.Ledger == "" {
		return nil, errors.New("zcashvault: the configuration must name this signer's seed file and its release ledger")
	}
	acct, err := accountKey(cfg.Seed, d.Net, cfg.Account)
	if err != nil {
		return nil, err
	}
	if len(cfg.Nodes) == 0 {
		acct.Zero()
		return nil, errors.New("zcashvault: the configuration names no Zcash node")
	}
	var list []node
	for _, nc := range cfg.Nodes {
		var user, pass string
		if nc.Cookie != "" {
			if user, pass, err = zcashtx.ReadCookie(nc.Cookie); err != nil {
				acct.Zero()
				return nil, fmt.Errorf("zcashvault: node %s: %w", nc.URL, err)
			}
		}
		name := nc.Name
		if name == "" {
			name = nc.URL
		}
		list = append(list, node{name: name, zc: zcashtx.NewZcashRPC(nc.URL, user, pass)})
	}
	if coord == nil && cfg.CoordinatorDir != "" {
		coord = DirCoordinator{Dir: cfg.CoordinatorDir, Me: acct.XPub()}
	}
	b, err := newBackend(d, acct, list, vv, coord, cfg.Ledger, cfg, logf)
	if err != nil {
		acct.Zero()
		return nil, err
	}
	for _, nc := range cfg.Nodes {
		if !zcashtx.IsLoopback(nc.URL) {
			b.logf("zcash node %s is on another machine: it is a trusted party for what the Zcash chain contains", nc.URL)
		}
	}
	if len(cfg.Nodes) == 1 {
		b.logf("one Zcash node only (%s): if it lies, nothing here can tell; name a second node you run", list[0].name)
	}
	return b, nil
}

func newBackend(d *Descriptor, acct *ExtKey, list []node, vv VaultView, coord Coordinator, ledgerPath string, cfg Config, logf func(string, ...interface{})) (*Backend, error) {
	me := -1
	for i, x := range d.XPubs {
		if x == acct.XPub() {
			me = i
		}
	}
	if me < 0 {
		return nil, errors.New("zcashvault: this signer's account key (from its seed) is not one of the descriptor's keys")
	}
	if vv == nil || coord == nil {
		return nil, errors.New("zcashvault: the backend needs the vault's contract view and a coordinator")
	}
	r := buildRules{K: d.K, Depth: cfg.FinalityDepth, Epoch: cfg.Epoch, ExpiryWindow: cfg.ExpiryWindow, MaxInputs: cfg.MaxInputs}
	if r.Depth == 0 {
		r.Depth = 100
	}
	if r.Depth < 100 && d.Net != NetRegtest {
		return nil, fmt.Errorf("zcashvault: a finality depth of %d on %s; Zcash's consensus limit is 100 blocks and nothing shallower is final", r.Depth, d.Net)
	}
	if r.Epoch == 0 {
		r.Epoch = 10
	}
	if r.ExpiryWindow == 0 {
		r.ExpiryWindow = 40
	}
	if r.ExpiryWindow < 10 {
		return nil, errors.New("zcashvault: an expiry window under 10 blocks leaves no time to mine a release")
	}
	if r.MaxInputs == 0 {
		r.MaxInputs = 200
	}
	if r.MaxInputs < 1 || r.MaxInputs > 250 {
		return nil, errors.New("zcashvault: maxInputs must be 1 to 250 (a release must stay a standard transaction)")
	}
	led, err := loadLedger(ledgerPath, d.Vault, acct.XPub())
	if err != nil {
		return nil, err
	}
	if logf == nil {
		logf = log.Printf
	}
	return &Backend{d: d, acct: acct, me: me, ns: &nodes{list: list, net: d.Net, depth: r.Depth}, vv: vv, coord: coord, led: led,
		rules: r, timeout: 90 * time.Second, logf: logf, txs: map[string]*zcashtx.Tx{}, mined: map[string]*minedTx{}, warned: map[string]bool{}}, nil
}

// Descriptor is the vault's public custody definition, and ViewKey what the signers publish with SetViewKey.
func (b *Backend) Descriptor() *Descriptor { return b.d }

func (b *Backend) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), b.timeout)
}

func (b *Backend) warnOnce(key, format string, a ...interface{}) {
	if !b.warned[key] {
		b.warned[key] = true
		b.logf(format, a...)
	}
}

// ---- the chain ----

// Height is the lowest tip the configured nodes report.
func (b *Backend) Height() (int64, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	v, err := b.ns.view(ctx)
	return int64(v.Tip), err
}

// Finalized is the lowest tip less the finality depth, at which every node names the same block.
func (b *Backend) Finalized() (int64, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	v, err := b.ns.view(ctx)
	return int64(v.Final), err
}

// Health fails unless every node answers, is synced, is on the vault's network, agrees with the others on the
// finalized block and on the next block's branch id, and this signer's key and ledger are in place.
func (b *Backend) Health() error {
	if b.acct == nil || !b.acct.IsPrivate() {
		return errors.New("zcashvault: this signer's key is not loaded")
	}
	if _, err := os.Stat(b.led.path); err != nil {
		return fmt.Errorf("zcashvault: the release ledger: %w", err)
	}
	ctx, cancel := b.ctx()
	defer cancel()
	_, err := b.ns.view(ctx)
	return err
}

// Address is vault index j's deposit address: the P2SH k-of-n address every party derives from the descriptor.
func (b *Backend) Address(index uint64) (string, error) { return b.d.DepositAddress(index) }

// ValidDest says whether dest can be paid: a transparent address (P2PKH or P2SH) on the vault's network, and not the
// vault's own change address. A shielded or unified address is not payable by this custody and is refused, so the
// daemon vetoes the withdrawal and the burner gets the units back. It reads no chain: the answer for a string never
// changes, so a withdrawal is never vetoed because a node was unreachable.
func (b *Backend) ValidDest(dest string) bool {
	if _, _, err := zcashtx.AddressScript(dest, b.d.Net.addrNet()); err != nil {
		return false
	}
	ch, err := b.d.Change()
	return err == nil && dest != ch.Address
}

// vaultScripts maps every vault scriptPubKey — each registered index's deposit address and the change address —
// to its script. n is the contract's un.
func (b *Backend) vaultScripts() (map[string]*VaultScript, uint64, error) {
	n, err := b.vv.Indexes()
	if err != nil {
		return nil, 0, err
	}
	if n >= HardenedOffset {
		return nil, 0, fmt.Errorf("zcashvault: %d registered indexes is beyond the non-hardened range", n)
	}
	m := map[string]*VaultScript{}
	for j := uint32(0); uint64(j) < n; j++ {
		vs, err := b.d.Script(ChainDeposit, j)
		if err != nil {
			return nil, 0, err
		}
		m[string(vs.ScriptPubKey)] = vs
	}
	ch, err := b.d.Change()
	if err != nil {
		return nil, 0, err
	}
	m[string(ch.ScriptPubKey)] = ch
	return m, n, nil
}

// Reserve is the vault's balance: the confirmed unspent outputs at every registered deposit address and at the
// change address, the lowest total any node shows. Anyone holding the published descriptor computes the same sum.
func (b *Backend) Reserve() (uint64, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	if _, err := b.ns.view(ctx); err != nil {
		return 0, err
	}
	scripts, _, err := b.vaultScripts()
	if err != nil {
		return 0, err
	}
	totals := make([]uint64, len(b.ns.list))
	for _, vs := range scripts {
		bal, err := b.ns.balances(ctx, vs.Address, vs.ScriptPubKey)
		if err != nil {
			return 0, err
		}
		for i := range totals {
			totals[i] += bal[i]
			if totals[i] > zcashtx.MaxMoney {
				return 0, errors.New("zcashvault: a reserve above the Zcash supply")
			}
		}
	}
	min := totals[0]
	for i, t := range totals {
		if t < min {
			min = t
		}
		if t != totals[0] {
			b.warnOnce(fmt.Sprintf("reserve-%d-%d", t, totals[0]), "the nodes disagree on the reserve (%s: %d, %s: %d); taking the lower", b.ns.list[0].name, totals[0], b.ns.list[i].name, t)
		}
	}
	return min, nil
}

// minedFinal is agreedMined, cached for transactions at or below the finalized height.
func (b *Backend) minedFinal(ctx context.Context, txid string, final uint32) (*minedTx, error) {
	if m, ok := b.mined[txid]; ok && m.Height <= final {
		return m, nil
	}
	m, unknown, err := b.ns.agreedMined(ctx, txid)
	if err != nil {
		return nil, err
	}
	if unknown {
		return nil, fmt.Errorf("zcashvault: no node knows %s", txid)
	}
	if m.Height <= final {
		b.mined[txid] = m
	}
	return m, nil
}

// Deposits is every output ever paid to an attested deposit address, in a block at or below the finalized height,
// that every node reports identically — spent or not: the vault may already have spent a deposit it has not yet
// minted (it waits for headroom, or the vault is halted), and a deposit must never disappear from the list because
// the reserve moved. An index whose attested address is not the one the descriptor derives is left out and named.
func (b *Backend) Deposits() ([]Deposit, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	v, err := b.ns.view(ctx)
	if err != nil {
		return nil, err
	}
	n, err := b.vv.Indexes()
	if err != nil {
		return nil, err
	}
	var out []Deposit
	for j := uint64(0); j < n; j++ {
		da, ok, err := b.vv.Attested(j)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		vs, err := b.d.Script(ChainDeposit, uint32(j))
		if err != nil {
			return nil, err
		}
		if da != vs.Address {
			b.warnOnce(fmt.Sprintf("attest-%d-%s", j, da), "index %d is attested as %q, but the descriptor derives %s: its deposits are not reported", j, da, vs.Address)
			continue
		}
		ids, err := b.ns.agreedHistory(ctx, vs.Address, v.Final)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			m, err := b.minedFinal(ctx, id, v.Final)
			if err != nil {
				return nil, err
			}
			if m.Height > v.Final {
				continue
			}
			for vout, o := range m.Tx.Out {
				if o.Value > 0 && bytes.Equal(o.ScriptPubKey, vs.ScriptPubKey) {
					out = append(out, Deposit{TxID: id, Vout: uint64(vout), Index: j, Amount: o.Value, Height: int64(m.Height)})
				}
			}
		}
	}
	return out, nil
}

// Verify re-reads a deposit from every node at vote time: the same transaction, mined in the same best-chain block
// at the recorded height, at or below the finalized height, paying the recorded amount to index d.Index's address.
func (b *Backend) Verify(d Deposit) (bool, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	v, err := b.ns.view(ctx)
	if err != nil {
		return false, err
	}
	if d.Height <= 0 || d.Height > int64(v.Final) || d.Index >= HardenedOffset || d.Vout > 1<<20 {
		return false, nil
	}
	vs, err := b.d.Script(ChainDeposit, uint32(d.Index))
	if err != nil {
		return false, err
	}
	m, unknown, err := b.ns.agreedMined(ctx, d.TxID) // never the cache: this is the re-read
	if err != nil || unknown {
		return false, err
	}
	if int64(m.Height) != d.Height {
		return false, nil
	}
	for _, n := range b.ns.list {
		h, err := n.zc.BlockHash(ctx, m.Height)
		if err != nil {
			return false, err
		}
		if h != m.BlockHash {
			return false, nil
		}
	}
	if d.Vout >= uint64(len(m.Tx.Out)) {
		return false, nil
	}
	o := m.Tx.Out[d.Vout]
	return o.Value == d.Amount && bytes.Equal(o.ScriptPubKey, vs.ScriptPubKey), nil
}

// ---- releases ----

// fillPrevouts sets every input's Value and ScriptPubKey from the transaction it spends, read from a node that
// knows it and checked against its id.
func (b *Backend) fillPrevouts(ctx context.Context, tx *zcashtx.Tx) error {
	for i := range tx.In {
		id := zcashtx.DisplayHex(tx.In[i].PrevOut.Hash)
		prev, err := b.parsed(ctx, id)
		if err != nil {
			return fmt.Errorf("input %d spends %s: %w", i, id, err)
		}
		idx := tx.In[i].PrevOut.Index
		if int(idx) >= len(prev.Out) {
			return fmt.Errorf("input %d spends output %d of %s, which has %d", i, idx, id, len(prev.Out))
		}
		tx.In[i].Value, tx.In[i].ScriptPubKey = prev.Out[idx].Value, prev.Out[idx].ScriptPubKey
	}
	return nil
}

// parsed is a transaction's parsed form from any node, cached by id (a transaction's bytes never change).
func (b *Backend) parsed(ctx context.Context, txid string) (*zcashtx.Tx, error) {
	if t, ok := b.txs[txid]; ok {
		return t, nil
	}
	t, _, err := b.ns.rawAny(ctx, txid)
	if err != nil {
		return nil, err
	}
	b.txs[txid] = t
	return t, nil
}

// verifyPayment is everything a signer checks on a signed transaction before it treats it as withdrawal j's
// payment: every input spends a vault address with k valid signatures, and checkRelease holds.
func (b *Backend) verifyPayment(ctx context.Context, tx *zcashtx.Tx, j uint64, destScript []byte, amount uint64, scripts map[string]*VaultScript) error {
	if err := b.fillPrevouts(ctx, tx); err != nil {
		return err
	}
	ch, err := b.d.Change()
	if err != nil {
		return err
	}
	lookup := func(spk []byte) *VaultScript { return scripts[string(spk)] }
	for i, in := range tx.In {
		vs := lookup(in.ScriptPubKey)
		if vs == nil {
			return fmt.Errorf("input %d does not spend a vault address", i)
		}
		if err := checkMultisigInput(tx, i, vs, b.d.K); err != nil {
			return err
		}
	}
	return checkRelease(tx, b.d.K, j, destScript, amount, ch, lookup)
}

// VerifyRelease says whether ftx is withdrawal j's payment and is final: every node shows it mined in the same
// best-chain block at or below the finalized height, every input spends a vault address with k valid signatures,
// and it pays dest amount less its ZIP 317 fee under the lock-time marker j. A payment that is mined but not yet
// final is an error that says when it will be.
func (b *Backend) VerifyRelease(j uint64, ftx string, dest string, amount uint64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := b.ctx()
	defer cancel()
	destScript, _, err := zcashtx.AddressScript(dest, b.d.Net.addrNet())
	if err != nil {
		return false, nil
	}
	v, err := b.ns.view(ctx)
	if err != nil {
		return false, err
	}
	m, unknown, err := b.ns.agreedMined(ctx, ftx)
	if err != nil {
		return false, err
	}
	if unknown {
		return false, nil
	}
	if m.Height > v.Final {
		return false, fmt.Errorf("zcashvault: %s is mined at %d and final at %d (finalized height now %d)", ftx, m.Height, m.Height+b.rules.Depth, v.Final)
	}
	for _, n := range b.ns.list {
		if h, err := n.zc.BlockHash(ctx, m.Height); err != nil || h != m.BlockHash {
			return false, fmt.Errorf("zcashvault: node %s does not name block %s at %d: %v", n.name, m.BlockHash, m.Height, err)
		}
	}
	scripts, _, err := b.vaultScripts()
	if err != nil {
		return false, err
	}
	tx, err := zcashtx.ParseTx(m.Raw)
	if err != nil {
		return false, err
	}
	if err := b.verifyPayment(ctx, tx, j, destScript, amount, scripts); err != nil {
		return false, fmt.Errorf("zcashvault: %s is not withdrawal %d's payment: %w", ftx, j, err)
	}
	return true, nil
}

// Release pays withdrawal j, idempotently: it returns the id of the one transaction that pays j, finding it in this
// signer's ledger, among the co-signers' finished transactions, or on the chain by its lock-time marker, before it
// ever builds one. Otherwise it builds the release deterministically, records it, signs its inputs, publishes the
// signatures through the coordinator, and assembles and broadcasts once k signers' valid signatures are there. Until
// then it returns an error naming how many it has; the daemon calls it again on the next pass.
func (b *Backend) Release(j uint64, dest string, amount uint64) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, cancel := b.ctx()
	defer cancel()
	if j >= zcashtx.LockTimeThreshold {
		return "", fmt.Errorf("zcashvault: withdrawal index %d", j)
	}
	if !b.ValidDest(dest) {
		return "", fmt.Errorf("zcashvault: %q is not a transparent address this vault can pay", dest)
	}
	destScript, _, err := zcashtx.AddressScript(dest, b.d.Net.addrNet())
	if err != nil {
		return "", err
	}
	if amount == 0 || amount > zcashtx.MaxMoney {
		return "", fmt.Errorf("zcashvault: a withdrawal of %d zatoshi", amount)
	}
	e, err := b.led.entry(j, dest, amount)
	if err != nil {
		return "", err
	}
	v, err := b.ns.view(ctx)
	if err != nil {
		return "", err
	}
	scripts, _, err := b.vaultScripts()
	if err != nil {
		return "", err
	}
	// 1. what this signer already has for j
	if id, done, err := b.resume(ctx, v, e, destScript, amount, scripts); done {
		return id, err
	}
	// 2. a payment the co-signers finished
	if id, ok := b.adoptFinal(ctx, v, e, destScript, amount, scripts); ok {
		return id, nil
	}
	// 3. a payment on the chain this signer has no record of (a lost ledger)
	if len(e.Txs) == 0 {
		if id, ok, err := b.findOnChain(ctx, v, e, destScript, amount, scripts); err != nil || ok {
			return id, err
		}
	}
	// 4. build ours
	p, err := b.plan(ctx, v, j, destScript, amount, scripts)
	if err != nil {
		return "", err
	}
	txid := zcashtx.DisplayHex(p.Tx.TxID())
	lt := e.find(txid)
	if lt == nil {
		for _, earlier := range e.Txs {
			if earlier.State == "abandoned" {
				continue
			}
			ok, why, err := b.mayReplace(ctx, v, earlier, p, scripts)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("zcashvault: withdrawal %d: %s, signed earlier, could still be mined (%s); not signing another", j, earlier.TxID, why)
			}
		}
		lt = &ledgerTx{TxID: txid, Hex: hex.EncodeToString(p.Tx.Serialize()), Expiry: p.Expiry, State: "signing"}
		for _, c := range p.Inputs {
			lt.Inputs = append(lt.Inputs, c.key())
			lt.Values = append(lt.Values, c.Value)
			lt.Scripts = append(lt.Scripts, hex.EncodeToString(c.VS.ScriptPubKey))
		}
		e.Txs = append(e.Txs, lt)
		if err := b.led.save(); err != nil { // the record precedes the signature
			e.Txs = e.Txs[:len(e.Txs)-1]
			return "", err
		}
	}
	return b.collect(ctx, e, lt, destScript, amount, scripts, true)
}

// resume handles every transaction this signer already has for j. done is true when the answer is final for this
// call: a payment exists (its id), or one that could still be mined must be waited for (an error).
func (b *Backend) resume(ctx context.Context, v chainView, e *ledgerEntry, destScript []byte, amount uint64, scripts map[string]*VaultScript) (string, bool, error) {
	for i := len(e.Txs) - 1; i >= 0; i-- {
		lt := e.Txs[i]
		if lt.State == "abandoned" || lt.Final == "" {
			continue
		}
		s, err := b.ns.seen(ctx, lt.TxID)
		if err != nil {
			return "", true, err
		}
		raw, err := hex.DecodeString(lt.Final)
		if err != nil {
			return "", true, fmt.Errorf("zcashvault: the ledger's copy of %s is not hex", lt.TxID)
		}
		if s.MinedAny || s.KnownAny {
			if !s.MinedAll {
				b.ns.broadcast(ctx, raw) // a partial broadcast: give it to every node; the answer is not needed
			}
			return lt.TxID, true, nil
		}
		invalid, why, err := b.provablyInvalid(ctx, v, lt)
		if err != nil {
			return "", true, err
		}
		if invalid {
			b.logf("withdrawal %d: %s can never be mined (%s); a replacement may be built", e.J, lt.TxID, why)
			lt.State = "abandoned"
			if err := b.led.save(); err != nil {
				return "", true, err
			}
			continue
		}
		if _, err := b.ns.broadcast(ctx, raw); err != nil {
			// Not provably invalid, and refused now: most often because a release still in flight spends one of its
			// inputs. Waiting here would stall the withdrawal until that spend is final; instead the search goes on,
			// and a replacement is signed only if mayReplace shows that at most one of the two can ever be mined.
			b.logf("withdrawal %d: %s is signed and may still be mined, and no node takes it now (%v)", e.J, lt.TxID, err)
			continue
		}
		return lt.TxID, true, nil
	}
	// a transaction signed but not assembled may have reached k signatures since: finish it without signing again
	for i := len(e.Txs) - 1; i >= 0; i-- {
		lt := e.Txs[i]
		if lt.State != "signing" || v.Tip+3 >= lt.Expiry {
			continue
		}
		if id, err := b.collect(ctx, e, lt, destScript, amount, scripts, false); err == nil {
			return id, true, nil
		}
	}
	return "", false, nil
}

// provablyInvalid says whether a signed transaction can never be mined: it is not in any node's chain, and either
// every block up to its expiry height is final, or one of its inputs is spent by another transaction at or below
// the finalized height.
func (b *Backend) provablyInvalid(ctx context.Context, v chainView, lt *ledgerTx) (bool, string, error) {
	s, err := b.ns.seen(ctx, lt.TxID)
	if err != nil {
		return false, "", err
	}
	if s.MinedAny {
		return false, "", nil
	}
	if v.Final >= lt.Expiry {
		return true, fmt.Sprintf("it expired at %d and the chain is final through %d", lt.Expiry, v.Final), nil
	}
	for i, key := range lt.Inputs {
		spk, err := hex.DecodeString(lt.Scripts[i])
		if err != nil {
			return false, "", err
		}
		vs := scriptsByKey(spk, b.d)
		if vs == nil {
			continue
		}
		ids, err := b.ns.agreedHistory(ctx, vs.Address, v.Final)
		if err != nil {
			return false, "", err
		}
		for _, id := range ids {
			if id == lt.TxID {
				continue
			}
			t, err := b.parsed(ctx, id)
			if err != nil {
				return false, "", err
			}
			for _, in := range t.In {
				if outpointKey(zcashtx.DisplayHex(in.PrevOut.Hash), in.PrevOut.Index) == key {
					return true, fmt.Sprintf("its input %s is spent by %s, final", key, id), nil
				}
			}
		}
	}
	return false, "", nil
}

// scriptsByKey finds the vault script with this scriptPubKey among the change address and the deposit addresses
// already derived.
func scriptsByKey(spk []byte, d *Descriptor) *VaultScript {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, vs := range d.cache {
		if bytes.Equal(vs.ScriptPubKey, spk) {
			return vs
		}
	}
	return nil
}

// mayReplace says whether this signer may sign p for a withdrawal it already signed earlier for: only when at most
// one of the two can ever be mined — they share an input, or p's anchor descends through the vault's change chain
// from a release that spent one of earlier's inputs, or earlier is provably invalid.
func (b *Backend) mayReplace(ctx context.Context, v chainView, earlier *ledgerTx, p *plan, scripts map[string]*VaultScript) (bool, string, error) {
	spent := map[string]bool{}
	for _, k := range earlier.Inputs {
		spent[k] = true
	}
	for _, c := range p.Inputs {
		if spent[c.key()] {
			return true, "", nil
		}
	}
	// walk back from p's anchor through the change chain: each link is a vault release, whose input 0 is the
	// previous anchor
	cur := p.Inputs[0].TxID
	for hop := 0; hop < 64; hop++ {
		t, err := b.parsed(ctx, cur)
		if err != nil {
			break
		}
		if !isVaultSpend(t, scripts) {
			break
		}
		for _, in := range t.In {
			if spent[outpointKey(zcashtx.DisplayHex(in.PrevOut.Hash), in.PrevOut.Index)] {
				return true, "", nil
			}
		}
		cur = zcashtx.DisplayHex(t.In[0].PrevOut.Hash)
	}
	invalid, why, err := b.provablyInvalid(ctx, v, earlier)
	if err != nil {
		return false, "", err
	}
	if invalid {
		b.logf("withdrawal %d: %s can never be mined (%s)", p.Tx.LockTime, earlier.TxID, why)
		earlier.State = "abandoned"
		return true, "", b.led.save()
	}
	return false, "it shares no input with the new one and is neither expired nor spent", nil
}

// isVaultSpend says whether every input of t spends a vault address: its scriptSig ends with the redeem script of one
// (consensus checked the k signatures of a mined one).
func isVaultSpend(t *zcashtx.Tx, scripts map[string]*VaultScript) bool {
	if len(t.In) == 0 {
		return false
	}
	for _, in := range t.In {
		items, err := zcashtx.ScriptPushes(in.ScriptSig)
		if err != nil || len(items) == 0 {
			return false
		}
		redeem := items[len(items)-1]
		if _, ok := scripts[string(zcashtx.P2SHScript(zcashtx.Hash160(redeem)))]; !ok {
			return false
		}
	}
	return true
}

// plan gathers the reserve outputs every node agrees on and builds the release.
func (b *Backend) plan(ctx context.Context, v chainView, j uint64, destScript []byte, amount uint64, scripts map[string]*VaultScript) (*plan, error) {
	ch, err := b.d.Change()
	if err != nil {
		return nil, err
	}
	var cands []coin
	var heads []coin
	keys := make([]string, 0, len(scripts))
	for k := range scripts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vs := scripts[k]
		us, err := b.ns.agreedUTXOs(ctx, vs.Address, vs.ScriptPubKey)
		if err != nil {
			return nil, err
		}
		for _, u := range us {
			c, err := coinOf(u, vs)
			if err != nil {
				return nil, err
			}
			if vs == ch && u.Index == 1 {
				t, err := b.parsed(ctx, u.TxID)
				if err != nil {
					return nil, err
				}
				if isVaultSpend(t, scripts) {
					heads = append(heads, c)
					continue
				}
			}
			cands = append(cands, c)
		}
	}
	var head *coin
	switch len(heads) {
	case 0:
	case 1:
		head = &heads[0]
	default:
		return nil, fmt.Errorf("zcashvault: the vault has %d unspent change outputs from its own releases; its change chain has forked, which only k signers acting together can do — refusing to sign anything", len(heads))
	}
	return buildRelease(b.rules, j, destScript, amount, v.Final, v.Branch, cands, head, ch)
}

// collect signs lt (when sign is true), publishes the signatures, gathers the co-signers', and assembles and
// broadcasts once k signers' are valid.
func (b *Backend) collect(ctx context.Context, e *ledgerEntry, lt *ledgerTx, destScript []byte, amount uint64, scripts map[string]*VaultScript, sign bool) (string, error) {
	raw, err := hex.DecodeString(lt.Hex)
	if err != nil {
		return "", err
	}
	tx, err := zcashtx.ParseTx(raw)
	if err != nil {
		return "", err
	}
	if len(lt.Values) != len(tx.In) || len(lt.Scripts) != len(tx.In) {
		return "", errors.New("zcashvault: the ledger's record of the inputs does not match the transaction")
	}
	vss := make([]*VaultScript, len(tx.In))
	digests := make([][32]byte, len(tx.In))
	for i := range tx.In {
		spk, err := hex.DecodeString(lt.Scripts[i])
		if err != nil {
			return "", err
		}
		tx.In[i].Value, tx.In[i].ScriptPubKey = lt.Values[i], spk
		if vss[i] = scripts[string(spk)]; vss[i] == nil {
			return "", fmt.Errorf("zcashvault: input %d of %s does not spend a vault address", i, lt.TxID)
		}
	}
	ch, err := b.d.Change()
	if err != nil {
		return "", err
	}
	lookup := func(spk []byte) *VaultScript { return scripts[string(spk)] }
	// the check before any signature: this is withdrawal j's payment as the contract recorded it
	if err := checkRelease(tx, b.d.K, e.J, destScript, amount, ch, lookup); err != nil {
		return "", fmt.Errorf("zcashvault: refusing to sign %s for withdrawal %d: %w", lt.TxID, e.J, err)
	}
	for i := range tx.In {
		if digests[i], err = tx.SigDigest(i, zcashtx.SigHashAll); err != nil {
			return "", err
		}
	}
	round := sigRound(b.d.Vault, e.J, lt.TxID)
	mine := make([][]byte, len(tx.In))
	if sign {
		for i, vs := range vss {
			child, err := b.acct.Path(b.d.branch, vs.Chain, vs.Index)
			if err != nil {
				return "", err
			}
			key, err := child.signingKey()
			child.Zero()
			if err != nil {
				return "", err
			}
			mine[i], err = signInput(tx, i, key)
			key.Zero()
			if err != nil {
				return "", err
			}
		}
		p := sigPayload{V: 1, Vault: b.d.Vault, J: e.J, TxID: lt.TxID, Signer: b.d.XPubs[b.me]}
		for _, s := range mine {
			p.Sigs = append(p.Sigs, hex.EncodeToString(s))
		}
		pb, _ := json.Marshal(p)
		if err := b.coord.Put(ctx, round, pb); err != nil {
			return "", fmt.Errorf("zcashvault: publishing the signatures for %s: %w", lt.TxID, err)
		}
	}
	got, err := b.coord.Get(ctx, round)
	if err != nil {
		return "", fmt.Errorf("zcashvault: reading the signatures for %s: %w", lt.TxID, err)
	}
	valid := map[int][][]byte{}
	if sign {
		valid[b.me] = mine
	}
	for _, pb := range got {
		var p sigPayload
		if decodePayload(pb, &p) != nil || p.V != 1 || p.Vault != b.d.Vault || p.J != e.J || p.TxID != lt.TxID || len(p.Sigs) != len(tx.In) {
			continue
		}
		who := -1
		for i, x := range b.d.XPubs {
			if x == p.Signer {
				who = i
			}
		}
		if who < 0 || valid[who] != nil {
			continue
		}
		sigs := make([][]byte, len(tx.In))
		ok := true
		for i, vs := range vss {
			s, err := hex.DecodeString(p.Sigs[i])
			if err != nil || !checkSig(s, vs.Pubs[vs.signer[who]], digests[i]) {
				ok = false
				break
			}
			sigs[i] = s
		}
		if ok {
			valid[who] = sigs
		}
	}
	if len(valid) < b.d.K {
		return "", fmt.Errorf("zcashvault: withdrawal %d: %d of %d signatures for %s; waiting for the co-signers", e.J, len(valid), b.d.K, lt.TxID)
	}
	for i, vs := range vss {
		type ps struct {
			pos int
			sig []byte
		}
		var have []ps
		for who, sigs := range valid {
			have = append(have, ps{vs.signer[who], sigs[i]})
		}
		sort.Slice(have, func(a, c int) bool { return have[a].pos < have[c].pos })
		var ordered [][]byte
		for _, h := range have[:b.d.K] {
			ordered = append(ordered, h.sig)
		}
		if tx.In[i].ScriptSig, err = multisigScriptSig(ordered, vs.Redeem); err != nil {
			return "", err
		}
		if err := checkMultisigInput(tx, i, vs, b.d.K); err != nil {
			return "", fmt.Errorf("zcashvault: the assembled input %d: %w", i, err)
		}
	}
	if err := checkRelease(tx, b.d.K, e.J, destScript, amount, ch, lookup); err != nil {
		return "", err
	}
	full := tx.Serialize()
	if id, ok := zcashtx.ComputeTxID(full); !ok || id != lt.TxID {
		return "", errors.New("zcashvault: the assembled transaction's id is not the one signed")
	}
	lt.Final, lt.State = hex.EncodeToString(full), "assembled"
	if err := b.led.save(); err != nil { // recorded before it is broadcast
		return "", err
	}
	fp, _ := json.Marshal(finalPayload{V: 1, Vault: b.d.Vault, J: e.J, TxID: lt.TxID, Hex: lt.Final})
	if err := b.coord.Put(ctx, finalRound(b.d.Vault, e.J), fp); err != nil {
		b.logf("withdrawal %d: could not publish the assembled %s to the co-signers: %v", e.J, lt.TxID, err)
	}
	if _, err := b.ns.broadcast(ctx, full); err != nil {
		return "", fmt.Errorf("zcashvault: withdrawal %d: %s is assembled and recorded, and no node took it yet: %v", e.J, lt.TxID, err)
	}
	b.logf("withdrawal %d: %s assembled from %d signatures and broadcast", e.J, lt.TxID, b.d.K)
	return lt.TxID, nil
}

// adoptFinal takes a payment for j that the co-signers finished, when it verifies completely and could still be
// mined: never one this signer has already found dead, and never one whose expiry is final.
func (b *Backend) adoptFinal(ctx context.Context, v chainView, e *ledgerEntry, destScript []byte, amount uint64, scripts map[string]*VaultScript) (string, bool) {
	got, err := b.coord.Get(ctx, finalRound(b.d.Vault, e.J))
	if err != nil {
		return "", false
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var p finalPayload
		if decodePayload(got[k], &p) != nil || p.V != 1 || p.Vault != b.d.Vault || p.J != e.J {
			continue
		}
		raw, err := hex.DecodeString(p.Hex)
		if err != nil {
			continue
		}
		if id, ok := zcashtx.ComputeTxID(raw); !ok || id != p.TxID {
			continue
		}
		if lt := e.find(p.TxID); lt != nil && lt.State == "abandoned" {
			continue
		}
		tx, err := zcashtx.ParseTx(raw)
		if err != nil || (tx.ExpiryHeight != 0 && v.Final >= tx.ExpiryHeight) {
			continue
		}
		if err := b.verifyPayment(ctx, tx, e.J, destScript, amount, scripts); err != nil {
			b.warnOnce("final-"+p.TxID, "withdrawal %d: a co-signer's transaction %s does not verify: %v", e.J, p.TxID, err)
			continue
		}
		if id, ok := b.adopt(ctx, e, p.TxID, raw, tx); ok {
			return id, true
		}
	}
	return "", false
}

// findOnChain looks for withdrawal j's payment in the change address's history: every release pays the change
// address, and carries j as its lock time.
func (b *Backend) findOnChain(ctx context.Context, v chainView, e *ledgerEntry, destScript []byte, amount uint64, scripts map[string]*VaultScript) (string, bool, error) {
	ch, err := b.d.Change()
	if err != nil {
		return "", false, err
	}
	ids, err := b.ns.historyAny(ctx, ch.Address, v.Tip)
	if err != nil {
		return "", false, err
	}
	for _, id := range ids {
		t, err := b.parsed(ctx, id)
		if err != nil {
			return "", false, err
		}
		if uint64(t.LockTime) != e.J || len(t.Out) != 2 || !bytes.Equal(t.Out[0].ScriptPubKey, destScript) || !isVaultSpend(t, scripts) {
			continue
		}
		_, raw, err := b.ns.rawAny(ctx, id)
		if err != nil {
			return "", false, err
		}
		tx, err := zcashtx.ParseTx(raw)
		if err != nil {
			return "", false, err
		}
		if err := b.verifyPayment(ctx, tx, e.J, destScript, amount, scripts); err != nil {
			b.warnOnce("chain-"+id, "withdrawal %d: %s carries its marker and does not verify as its payment: %v", e.J, id, err)
			continue
		}
		b.logf("withdrawal %d: its payment %s is already on the chain; adopting it", e.J, id)
		id, ok := b.adopt(ctx, e, id, raw, tx)
		return id, ok, nil
	}
	return "", false, nil
}

func (b *Backend) adopt(ctx context.Context, e *ledgerEntry, txid string, raw []byte, tx *zcashtx.Tx) (string, bool) {
	lt := e.find(txid)
	if lt == nil {
		lt = &ledgerTx{TxID: txid, Hex: hex.EncodeToString(raw), Expiry: tx.ExpiryHeight}
		for _, in := range tx.In {
			lt.Inputs = append(lt.Inputs, outpointKey(zcashtx.DisplayHex(in.PrevOut.Hash), in.PrevOut.Index))
			lt.Values = append(lt.Values, in.Value)
			lt.Scripts = append(lt.Scripts, hex.EncodeToString(in.ScriptPubKey))
		}
		e.Txs = append(e.Txs, lt)
	}
	lt.Final = hex.EncodeToString(raw)
	if lt.State != "assembled" {
		lt.State = "adopted"
	}
	if err := b.led.save(); err != nil {
		b.logf("withdrawal %d: recording the adopted %s: %v", e.J, txid, err)
		return "", false
	}
	// it is the payment only if it is out there: taken now, or already held by a node
	if _, err := b.ns.broadcast(ctx, raw); err != nil {
		if s, serr := b.ns.seen(ctx, txid); serr != nil || !s.KnownAny {
			b.logf("withdrawal %d: the co-signers' %s is not taken by any node (%v)", e.J, txid, err)
			return "", false
		}
	}
	return txid, true
}

// Summary is a one-line description of the custody for the daemon's start-up banner.
func (b *Backend) Summary() string {
	ch, _ := b.d.Change()
	return fmt.Sprintf("zcash %s, transparent P2SH %d-of-%d, this signer is key %d, change address %s, finality depth %d, nodes %s",
		b.d.Net, b.d.K, b.d.N(), b.me+1, ch.Address, b.rules.Depth, strings.Join(nodeNames(b.ns.list), ", "))
}

func nodeNames(l []node) []string {
	var out []string
	for _, n := range l {
		out = append(out, n.name)
	}
	return out
}
