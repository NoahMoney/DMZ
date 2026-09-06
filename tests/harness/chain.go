// Package harness runs Veil contracts through derohe's own DVM executor (dvm.Execute_sc_function,
// SanityCheckExternalTransfers, ErrorRevert, ProcessExternal) on an in-memory graviton store.
// It mirrors blockchain.process_transaction_sc and dvm.Simulator.common exactly, without a node.
package harness

import (
	"crypto/rand"
	"fmt"

	"github.com/deroproject/derohe/config"
	"github.com/deroproject/derohe/cryptography/bn256"
	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/dvm"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/graviton"
)

// MaxStorageGas is config.MAX_STORAGE_GAS_ATOMIC_UNITS: the most storage gas a real TX can carry.
const MaxStorageGas = config.MAX_STORAGE_GAS_ATOMIC_UNITS

type Chain struct {
	ss          *graviton.Snapshot
	balanceTree *graviton.Tree
	scTree      *graviton.Tree
	cache       map[crypto.Hash]*graviton.Tree
	Height      uint64
	Timestamp   uint64
}

// Result of one contract execution.
type Result struct {
	Err        error
	GasCompute uint64
	GasStorage uint64
	Transfers  []dvm.TransferExternal // external transfers emitted (only on success)
}

func (r Result) OK() bool { return r.Err == nil }

func New() *Chain {
	store, err := graviton.NewMemStore()
	if err != nil {
		panic(err)
	}
	ss, err := store.LoadSnapshot(0)
	if err != nil {
		panic(err)
	}
	bt, err := ss.GetTree(config.BALANCE_TREE)
	if err != nil {
		panic(err)
	}
	st, err := ss.GetTree(config.SC_META)
	if err != nil {
		panic(err)
	}
	return &Chain{ss: ss, balanceTree: bt, scTree: st, cache: map[crypto.Hash]*graviton.Tree{}, Height: 1, Timestamp: 1700000000}
}

// NextBlock advances height and timestamp.
func (c *Chain) NextBlock() {
	c.Height++
	c.Timestamp += 18
}

func zeroBalance(addr []byte) []byte {
	var p bn256.G1
	if err := p.DecodeCompressed(addr); err != nil {
		panic(err)
	}
	balance := crypto.ConstructElGamal(&p, crypto.ElGamal_BASE_G)
	nb := crypto.NonceBalance{NonceHeight: 0, Balance: balance}
	return nb.Serialize()
}

func (c *Chain) register(tree *graviton.Tree, addr []byte) {
	if _, err := tree.Get(addr); err != nil {
		if err := tree.Put(addr, zeroBalance(addr)); err != nil {
			panic(err)
		}
	}
}

// NewAccount creates a random registered account (as a wallet registration TX would).
func (c *Chain) NewAccount() *rpc.Address {
	k := crypto.RandomScalarBNRed()
	p := crypto.GPoint.ScalarMult(k)
	addr := rpc.NewAddressFromKeys(p)
	c.register(c.balanceTree, addr.Compressed())
	return addr
}

func (c *Chain) tree(id crypto.Hash) *graviton.Tree {
	var zero crypto.Hash
	if id == zero {
		return c.balanceTree
	}
	if t, ok := c.cache[id]; ok {
		return t
	}
	t, err := c.ss.GetTree(string(id[:]))
	if err != nil {
		panic(err)
	}
	c.cache[id] = t
	return t
}

func randHash() (h crypto.Hash) {
	if _, err := rand.Read(h[:]); err != nil {
		panic(err)
	}
	// graviton refuses a tree whose name starts with ':' (snapshot.go); a real SCID is a tx hash and can start with
	// any byte, but the harness uses the bytes as the tree name directly, so one install in 256 used to panic
	if h[0] == ':' {
		h[0] = 'v'
	}
	return
}

// Install installs code exactly as blockchain.process_transaction_sc (SC_INSTALL) does and runs Initialize /
// InitializePrivate with args. fees plays the role of the TX fee (storage gas budget, capped at MaxStorageGas).
func (c *Chain) Install(code string, incoming map[crypto.Hash]uint64, args rpc.Arguments, signer *rpc.Address, fees uint64) (crypto.Hash, Result) {
	scid := randHash()
	sc, _, err := dvm.ParseSmartContract(code)
	if err != nil {
		return scid, Result{Err: fmt.Errorf("parse: %w", err)}
	}
	var meta dvm.SC_META_DATA
	entry := "Initialize"
	if _, ok := sc.Functions["InitializePrivate"]; ok {
		meta.Type = 1
		entry = "InitializePrivate"
	}
	wData := dvm.Wrapped_tree(c.cache, c.ss, scid)
	wData.Put(dvm.SC_Code_Key(scid), dvm.Variable{Type: dvm.String, ValueString: code}.MarshalBinaryPanic())
	wSC := &dvm.Tree_Wrapper{Tree: c.scTree, Entries: map[string][]byte{}}
	wSC.Put(dvm.SC_Meta_Key(scid), meta.MarshalBinary())

	scdata := append(rpc.Arguments{}, args...)
	scdata = append(scdata,
		rpc.Argument{Name: rpc.SCACTION, DataType: rpc.DataUint64, Value: uint64(rpc.SC_INSTALL)},
		rpc.Argument{Name: rpc.SCCODE, DataType: rpc.DataString, Value: code})
	return scid, c.exec(wSC, wData, scid, randHash(), scid, sc, entry, 0, signer, incoming, scdata, fees)
}

// Call invokes an entrypoint exactly as blockchain.process_transaction_sc (SC_CALL) does.
// signer == nil emulates ringsize > 2 (SIGNER() = 33 zero bytes). The storage-gas budget is MaxStorageGas.
func (c *Chain) Call(scid crypto.Hash, entry string, args rpc.Arguments, incoming map[crypto.Hash]uint64, signer *rpc.Address) Result {
	wSC := &dvm.Tree_Wrapper{Tree: c.scTree, Entries: map[string][]byte{}}
	if _, err := wSC.Get(dvm.SC_Meta_Key(scid)); err != nil {
		return Result{Err: fmt.Errorf("scid %s not installed", scid)}
	}
	wData := dvm.Wrapped_tree(c.cache, c.ss, scid)
	balance, sc, found := dvm.ReadSC(wSC, wData, scid)
	if !found {
		return Result{Err: fmt.Errorf("SC not found %s", scid)}
	}
	scdata := append(rpc.Arguments{}, args...)
	scdata = append(scdata,
		rpc.Argument{Name: rpc.SCACTION, DataType: rpc.DataUint64, Value: uint64(rpc.SC_CALL)},
		rpc.Argument{Name: rpc.SCID, DataType: rpc.DataHash, Value: scid},
		rpc.Argument{Name: "entrypoint", DataType: rpc.DataString, Value: entry})
	return c.exec(wSC, wData, scid, randHash(), randHash(), sc, entry, balance, signer, incoming, scdata, MaxStorageGas)
}

func (c *Chain) exec(wSC, wData *dvm.Tree_Wrapper, scid, blid, txid crypto.Hash, sc dvm.SmartContract, entry string,
	balance uint64, signer *rpc.Address, incoming map[crypto.Hash]uint64, scdata rpc.Arguments, fees uint64) Result {
	if incoming == nil {
		incoming = map[crypto.Hash]uint64{}
	}
	var s [33]byte
	if signer != nil {
		copy(s[:], signer.Compressed())
		// A real signer necessarily holds an entry in every asset tree it burns from.
		for asset, v := range incoming {
			if v > 0 {
				c.register(c.tree(asset), s[:])
			}
		}
	}
	gc, gs, err := dvm.Execute_sc_function(wSC, wData, scid, c.Height, c.Height, c.Timestamp, blid, txid, sc, entry, 1, balance, s, incoming, scdata, fees, false)
	res := Result{Err: err, GasCompute: gc, GasStorage: gs}
	if err == nil {
		err = dvm.SanityCheckExternalTransfers(wData, c.balanceTree, scid)
		res.Err = err
	}
	if err != nil {
		var zero [33]byte
		if s != zero {
			dvm.ErrorRevert(c.ss, c.cache, c.balanceTree, s, scid, incoming)
		}
		return res
	}
	res.Transfers = append([]dvm.TransferExternal{}, wData.Transfere...)
	dvm.ProcessExternal(c.ss, c.cache, c.balanceTree, s, scid, wData, wSC)
	return res
}

// Var reads a stored variable (string or uint64 key); nil when absent.
func (c *Chain) Var(scid crypto.Hash, key interface{}) interface{} {
	w := dvm.Wrapped_tree(c.cache, c.ss, scid)
	return dvm.ReadSCValue(w, scid, key)
}

func (c *Chain) Exists(scid crypto.Hash, key string) bool { return c.Var(scid, key) != nil }

// U reads a uint64 variable; panics when absent or of another type.
func (c *Chain) U(scid crypto.Hash, key string) uint64 {
	v := c.Var(scid, key)
	u, ok := v.(uint64)
	if !ok {
		panic(fmt.Sprintf("key %q: want uint64, got %T (%v)", key, v, v))
	}
	return u
}

// S reads a string variable; panics when absent or of another type.
func (c *Chain) S(scid crypto.Hash, key string) string {
	v := c.Var(scid, key)
	s, ok := v.(string)
	if !ok {
		panic(fmt.Sprintf("key %q: want string, got %T (%v)", key, v, v))
	}
	return s
}

// SCBalance is the contract's own balance of asset (zero hash = DERO), as the node accounts for it.
func (c *Chain) SCBalance(scid, asset crypto.Hash) uint64 {
	w := dvm.Wrapped_tree(c.cache, c.ss, scid)
	v, _ := dvm.LoadSCAssetValue(w, scid, asset)
	return v
}

// Sum of transfers of asset to addr in a result.
func (r Result) Sent(asset crypto.Hash, addr *rpc.Address) uint64 {
	var total uint64
	a := string(addr.Compressed())
	for _, t := range r.Transfers {
		if t.Asset == asset && t.Address == a {
			total += t.Amount
		}
	}
	return total
}

// Arg helpers.
func U(name string, v uint64) rpc.Argument {
	return rpc.Argument{Name: name, DataType: rpc.DataUint64, Value: v}
}
func S(name string, v string) rpc.Argument {
	return rpc.Argument{Name: name, DataType: rpc.DataString, Value: v}
}
func H(name string, v crypto.Hash) rpc.Argument {
	return rpc.Argument{Name: name, DataType: rpc.DataHash, Value: v}
}
