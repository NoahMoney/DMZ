package zcashvault

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"veil/tools/zcashtx"
)

// fakeChain is an in-memory Zcash regtest chain for the unit tests. It VALIDATES what it is sent — inputs exist and
// are unspent in the chain and the mempool, the expiry has not passed, the fee is ZIP 317's, and every input's
// script is satisfied, with its own CHECKMULTISIG walk over zcashtx's vector-checked digests (not the backend's
// checker) — so a transaction the backend builds wrong is refused here as the node would refuse it. The real node is
// the regtest e2e (regtest_e2e_test.go).
type fakeChain struct {
	mu      sync.Mutex
	blocks  []fakeBlock
	mempool [][]byte
	epoch   int // bumped by a reorg, so replaced blocks get new hashes
}

type fakeBlock struct {
	hash string
	txs  [][]byte
}

func newFakeChain(height int) *fakeChain {
	c := &fakeChain{}
	c.blocks = append(c.blocks, fakeBlock{hash: c.hashOf(0)})
	for len(c.blocks) <= height {
		c.blocks = append(c.blocks, fakeBlock{hash: c.hashOf(len(c.blocks))})
	}
	return c
}

func (c *fakeChain) hashOf(h int) string {
	s := sha256.Sum256([]byte(fmt.Sprintf("block %d epoch %d", h, c.epoch)))
	return hex.EncodeToString(s[:])
}

func (c *fakeChain) tip() uint32 { c.mu.Lock(); defer c.mu.Unlock(); return uint32(len(c.blocks) - 1) }

func txidOf(raw []byte) string { id, _ := zcashtx.ComputeTxID(raw); return id }

type fakeOut struct {
	value  uint64
	script []byte
	height uint32
}

// utxos is the confirmed unspent set, and (with the mempool) what the mempool spends.
func (c *fakeChain) utxos() (map[string]fakeOut, map[string]bool) {
	outs := map[string]fakeOut{}
	for h, b := range c.blocks {
		for _, raw := range b.txs {
			tx, _ := zcashtx.ParseTx(raw)
			id := txidOf(raw)
			for _, in := range tx.In {
				delete(outs, outpointKey(zcashtx.DisplayHex(in.PrevOut.Hash), in.PrevOut.Index))
			}
			for i, o := range tx.Out {
				outs[outpointKey(id, uint32(i))] = fakeOut{o.Value, o.ScriptPubKey, uint32(h)}
			}
		}
	}
	memSpent := map[string]bool{}
	for _, raw := range c.mempool {
		tx, _ := zcashtx.ParseTx(raw)
		for _, in := range tx.In {
			memSpent[outpointKey(zcashtx.DisplayHex(in.PrevOut.Hash), in.PrevOut.Index)] = true
		}
	}
	return outs, memSpent
}

// validate is the node's acceptance check for a transaction entering the mempool (or a block, at height).
func (c *fakeChain) validate(raw []byte, height uint32, mempool bool) error {
	tx, err := zcashtx.ParseTx(raw)
	if err != nil {
		return err
	}
	outs, memSpent := c.utxos()
	if mempool {
		for _, m := range c.mempool {
			mt, _ := zcashtx.ParseTx(m)
			id := txidOf(m)
			for i, o := range mt.Out {
				outs[outpointKey(id, uint32(i))] = fakeOut{o.Value, o.ScriptPubKey, 0}
			}
		}
	}
	if tx.ExpiryHeight != 0 && tx.ExpiryHeight < height {
		return fmt.Errorf("expired at %d", tx.ExpiryHeight)
	}
	for i := range tx.In {
		k := outpointKey(zcashtx.DisplayHex(tx.In[i].PrevOut.Hash), tx.In[i].PrevOut.Index)
		o, ok := outs[k]
		if !ok || (mempool && memSpent[k]) {
			return fmt.Errorf("input %d (%s) is missing or spent", i, k)
		}
		tx.In[i].Value, tx.In[i].ScriptPubKey = o.value, o.script
	}
	fee, err := tx.Fee()
	if err != nil {
		return err
	}
	if fee < tx.ConventionalFee() {
		return fmt.Errorf("fee %d below %d", fee, tx.ConventionalFee())
	}
	for i := range tx.In {
		if err := fakeScriptCheck(tx, i); err != nil {
			return fmt.Errorf("input %d: %v", i, err)
		}
	}
	for _, o := range tx.Out {
		if zcashtx.IsDust(o) {
			return errors.New("dust output")
		}
	}
	return nil
}

// fakeScriptCheck satisfies a P2PKH or a P2SH CHECKMULTISIG spend the interpreter's way.
func fakeScriptCheck(tx *zcashtx.Tx, i int) error {
	in := tx.In[i]
	items, err := zcashtx.ScriptPushes(in.ScriptSig)
	if err != nil {
		return err
	}
	d, err := tx.SigDigest(i, zcashtx.SigHashAll)
	if err != nil {
		return err
	}
	if pkh, ok := zcashtx.P2PKHHash(in.ScriptPubKey); ok {
		if len(items) != 2 || zcashtx.Hash160(items[1]) != pkh || !zcashtx.VerifySig(items[0], items[1], d) {
			return errors.New("P2PKH spend does not verify")
		}
		return nil
	}
	sh, ok := zcashtx.P2SHHash(in.ScriptPubKey)
	if !ok {
		return errors.New("unknown script")
	}
	if len(items) < 2 || zcashtx.Hash160(items[len(items)-1]) != sh || len(items[0]) != 0 {
		return errors.New("P2SH redeem script does not match, or NULLDUMMY")
	}
	r := items[len(items)-1]
	if len(r) < 3 || r[len(r)-1] != zcashtx.OpCheckMultiSig {
		return errors.New("not a CHECKMULTISIG redeem script")
	}
	k, n := int(r[0]-0x50), int(r[len(r)-2]-0x50)
	if len(r) != 3+34*n {
		return errors.New("malformed redeem script")
	}
	sigs := items[1 : len(items)-1]
	if len(sigs) != k {
		return fmt.Errorf("%d signatures for a %d-of-%d script", len(sigs), k, n)
	}
	pos := 0
	for _, s := range sigs {
		for pos < n && !zcashtx.VerifySig(s, r[1+34*pos+1:1+34*pos+34], d) {
			pos++
		}
		if pos == n {
			return errors.New("CHECKMULTISIG fails")
		}
		pos++
	}
	return nil
}

// mine adds n blocks; the first takes every mempool transaction still valid.
func (c *fakeChain) mine(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := 0; i < n; i++ {
		h := uint32(len(c.blocks))
		var in [][]byte
		if i == 0 {
			pool := c.mempool
			c.mempool = nil
			for _, raw := range pool {
				// validate against the chain plus what this block already holds
				c.blocks = append(c.blocks, fakeBlock{hash: "pending", txs: in})
				err := c.validate(raw, h, false)
				c.blocks = c.blocks[:len(c.blocks)-1]
				if err == nil {
					in = append(in, raw)
				}
			}
		}
		c.blocks = append(c.blocks, fakeBlock{hash: c.hashOf(int(h)), txs: in})
	}
}

// include mines one block holding raw without validating it: the coinbase-like payments that fund the tests.
func (c *fakeChain) include(raws ...[]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h := len(c.blocks)
	c.blocks = append(c.blocks, fakeBlock{hash: c.hashOf(h), txs: raws})
}

// pay makes a funding transaction paying value to script and mines it; it returns the txid.
func (c *fakeChain) pay(script []byte, values ...uint64) string {
	var prev [32]byte
	rand.Read(prev[:])
	tx := &zcashtx.Tx{Version: 5, BranchID: zcashtx.BranchNU63, In: []zcashtx.TxIn{{PrevOut: zcashtx.OutPoint{Hash: prev}, ScriptSig: []byte{0x51}, Sequence: zcashtx.SequenceFinal}}}
	for _, v := range values {
		tx.Out = append(tx.Out, zcashtx.TxOut{Value: v, ScriptPubKey: script})
	}
	raw := tx.Serialize()
	c.include(raw)
	return txidOf(raw)
}

// reorg drops the last depth blocks and mines as many new, empty ones with other hashes; their transactions go
// back to the mempool when keep is set.
func (c *fakeChain) reorg(depth int, keep bool) {
	c.mu.Lock()
	c.epoch++
	dropped := c.blocks[len(c.blocks)-depth:]
	c.blocks = c.blocks[:len(c.blocks)-depth]
	if keep {
		for _, b := range dropped {
			c.mempool = append(c.mempool, b.txs...)
		}
	}
	c.mu.Unlock()
	for i := 0; i < depth; i++ {
		c.mu.Lock()
		h := len(c.blocks)
		c.blocks = append(c.blocks, fakeBlock{hash: c.hashOf(h)})
		c.mu.Unlock()
	}
}

func (c *fakeChain) dropMempool() { c.mu.Lock(); c.mempool = nil; c.mu.Unlock() }

// fakeNode is one node's RPC over a fakeChain.
type fakeNode struct {
	c          *fakeChain
	refuseSend bool
	down       bool
}

func (n *fakeNode) ChainInfo(ctx context.Context) (zcashtx.ChainInfo, error) {
	if n.down {
		return zcashtx.ChainInfo{}, errors.New("connection refused")
	}
	n.c.mu.Lock()
	defer n.c.mu.Unlock()
	h := uint32(len(n.c.blocks) - 1)
	return zcashtx.ChainInfo{Chain: "regtest", Net: zcashtx.Testnet, Regtest: true, Height: h, BestHash: n.c.blocks[h].hash,
		TipBranch: zcashtx.BranchNU63, NextBranch: zcashtx.BranchNU63,
		Upgrades: []zcashtx.NodeUpgrade{{Name: "NU6.3", BranchID: zcashtx.BranchNU63, Height: 1, Status: "active"}}}, nil
}

func (n *fakeNode) BlockHash(ctx context.Context, h uint32) (string, error) {
	n.c.mu.Lock()
	defer n.c.mu.Unlock()
	if int(h) >= len(n.c.blocks) {
		return "", errors.New("no such block")
	}
	return n.c.blocks[h].hash, nil
}

func (n *fakeNode) AddressUTXOs(ctx context.Context, addr string) ([]zcashtx.UTXO, error) {
	spk, _, err := zcashtx.AddressScript(addr, zcashtx.Testnet)
	if err != nil {
		return nil, err
	}
	n.c.mu.Lock()
	defer n.c.mu.Unlock()
	outs, _ := n.c.utxos()
	var r []zcashtx.UTXO
	for k, o := range outs {
		if bytes.Equal(o.script, spk) {
			var id string
			var vout uint64
			fmt.Sscanf(k[:64], "%s", &id)
			vout, _ = strconv.ParseUint(k[65:], 10, 32)
			r = append(r, zcashtx.UTXO{Address: addr, TxID: id, Index: uint32(vout), Script: o.script, Value: o.value, Height: o.height})
		}
	}
	return r, nil
}

func (n *fakeNode) AddressTxIDs(ctx context.Context, addr string, start, end uint32) ([]string, error) {
	spk, _, err := zcashtx.AddressScript(addr, zcashtx.Testnet)
	if err != nil {
		return nil, err
	}
	n.c.mu.Lock()
	defer n.c.mu.Unlock()
	pays := map[string]bool{}
	var r []string
	for h, b := range n.c.blocks {
		for _, raw := range b.txs {
			tx, _ := zcashtx.ParseTx(raw)
			id := txidOf(raw)
			hit := false
			for i, o := range tx.Out {
				if bytes.Equal(o.ScriptPubKey, spk) {
					pays[outpointKey(id, uint32(i))] = true
					hit = true
				}
			}
			for _, in := range tx.In {
				if pays[outpointKey(zcashtx.DisplayHex(in.PrevOut.Hash), in.PrevOut.Index)] {
					hit = true
				}
			}
			if hit && uint32(h) >= start && uint32(h) <= end {
				r = append(r, id)
			}
		}
	}
	return r, nil
}

func (n *fakeNode) RawTx(ctx context.Context, txid string) (zcashtx.RawTx, error) {
	n.c.mu.Lock()
	defer n.c.mu.Unlock()
	tip := uint32(len(n.c.blocks) - 1)
	for h, b := range n.c.blocks {
		for _, raw := range b.txs {
			if txidOf(raw) == txid {
				return zcashtx.RawTx{TxID: txid, Raw: raw, Mined: true, Height: uint32(h), Confirmations: tip - uint32(h) + 1, BlockHash: b.hash, Verified: true}, nil
			}
		}
	}
	for _, raw := range n.c.mempool {
		if txidOf(raw) == txid {
			return zcashtx.RawTx{TxID: txid, Raw: raw, Verified: true}, nil
		}
	}
	return zcashtx.RawTx{}, fmt.Errorf("%w: %s", zcashtx.ErrTxNotFound, txid)
}

func (n *fakeNode) SendRawTx(ctx context.Context, raw []byte) (string, error) {
	if n.refuseSend || n.down {
		return "", &zcashtx.RPCError{Method: "sendrawtransaction", Code: -1, Message: "refused"}
	}
	n.c.mu.Lock()
	defer n.c.mu.Unlock()
	id := txidOf(raw)
	for _, b := range n.c.blocks {
		for _, r := range b.txs {
			if txidOf(r) == id {
				return "", &zcashtx.RPCError{Method: "sendrawtransaction", Code: -27, Message: "already in chain"}
			}
		}
	}
	for _, r := range n.c.mempool {
		if txidOf(r) == id {
			return id, nil
		}
	}
	if err := n.c.validate(raw, uint32(len(n.c.blocks)), true); err != nil {
		return "", &zcashtx.RPCError{Method: "sendrawtransaction", Code: -26, Message: err.Error()}
	}
	n.c.mempool = append(n.c.mempool, raw)
	return id, nil
}

func (n *fakeNode) TxOut(ctx context.Context, txid string, index uint32) (*zcashtx.TxOutInfo, error) {
	return nil, nil
}

// fakeView is the vault contract's un and da_<j>.
type fakeView struct {
	mu sync.Mutex
	un uint64
	da map[uint64]string
}

func (v *fakeView) Indexes() (uint64, error) { v.mu.Lock(); defer v.mu.Unlock(); return v.un, nil }
func (v *fakeView) Attested(j uint64) (string, bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.da[j]
	return s, ok, nil
}

// testVault is a k-of-n vault on a fake chain: seeds in a temporary directory, one backend per signer, and a
// directory coordinator they share.
type testVault struct {
	t       *testing.T
	chain   *fakeChain
	view    *fakeView
	d       *Descriptor
	seeds   []string
	xpubs   []string
	signers []*Backend
	nodes   [][]*fakeNode
	dir     string
	coord   string
}

const testScid = "5a1e7e57deadbeef00112233445566778899aabbccddeeff0011223344556677"

func newTestVault(t *testing.T, k, n int) *testVault {
	t.Helper()
	tv := &testVault{t: t, chain: newFakeChain(200), view: &fakeView{da: map[uint64]string{}}, dir: t.TempDir()}
	tv.coord = t.TempDir()
	for i := 0; i < n; i++ {
		p := fmt.Sprintf("%s/seed%d", tv.dir, i)
		if err := GenerateSeed(p); err != nil {
			t.Fatal(err)
		}
		x, err := AccountXPub(p, "regtest", 0)
		if err != nil {
			t.Fatal(err)
		}
		tv.seeds, tv.xpubs = append(tv.seeds, p), append(tv.xpubs, x)
	}
	d, err := NewDescriptor("regtest", k, tv.xpubs, testScid)
	if err != nil {
		t.Fatal(err)
	}
	tv.d = d
	for i := range tv.seeds {
		tv.signers = append(tv.signers, tv.backend(i, fmt.Sprintf("%s/ledger%d.json", tv.dir, i), tv.coord))
	}
	return tv
}

// backend makes signer i's backend with a ledger and coordinator directory of the caller's choosing.
func (tv *testVault) backend(i int, ledgerPath, coordDir string) *Backend {
	acct, err := accountKey(tv.seeds[i], NetRegtest, 0)
	if err != nil {
		tv.t.Fatal(err)
	}
	fn := &fakeNode{c: tv.chain}
	if len(tv.nodes) <= i {
		tv.nodes = append(tv.nodes, []*fakeNode{fn})
	} else {
		tv.nodes[i] = []*fakeNode{fn}
	}
	b, err := newBackend(tv.d, acct, []node{{name: fmt.Sprintf("node-%d", i), zc: fn}}, tv.view, DirCoordinator{Dir: coordDir, Me: acct.XPub()}, ledgerPath, Config{}, tv.t.Logf)
	if err != nil {
		tv.t.Fatal(err)
	}
	return b
}

// register opens vault index j and attests the address every party derives for it.
func (tv *testVault) register(j uint64) string {
	a, err := tv.d.DepositAddress(j)
	if err != nil {
		tv.t.Fatal(err)
	}
	tv.view.mu.Lock()
	if tv.view.un <= j {
		tv.view.un = j + 1
	}
	tv.view.da[j] = a
	tv.view.mu.Unlock()
	return a
}

func (tv *testVault) depositScript(j uint64) []byte {
	vs, err := tv.d.Script(ChainDeposit, uint32(j))
	if err != nil {
		tv.t.Fatal(err)
	}
	return vs.ScriptPubKey
}

// finalize mines until every block so far is final and past an epoch boundary, so it can be selected.
func (tv *testVault) finalize() { tv.chain.mine(100 + 11) }

// userAddress is a fresh transparent destination.
func userAddress(t *testing.T) string {
	var h [20]byte
	rand.Read(h[:])
	return zcashtx.EncodeAddress(zcashtx.Testnet, zcashtx.P2PKH, h)
}
