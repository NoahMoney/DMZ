package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"veil/tools/zecswap"
)

// The Zcash side: transparent only. The fees are the outputs paying zecTreasury, a P2PKH address whose key the
// treasury hands this bot in a file outside the repository. A payout is built and signed here with zecswap's own
// transaction type, ZIP 244 digest, ZIP 317 fee and secp256k1 signer, spending only outputs already counted as income
// or the change of this bot's own payouts; the change returns to zecTreasury. It expires zecExpiry blocks after it
// is built (ZIP 203), which is what lets a payout no node ever took be proven dead and paid again.

const (
	zecExpiry    = 40
	zecMaxInputs = 50
	zecMaxDests  = 50
)

type zecNet struct {
	nodes    []zecswap.ZcashSource // the first is primary; every node must agree before a fee counts or a payout closes
	chain    string                // mainnet, testnet or regtest
	net      zecswap.Network
	treasury string
	key      *zecswap.Key
	minPart  uint64
}

func (z *zecNet) Name() string            { return "ZEC" }
func (z *zecNet) MinPart() uint64         { return z.minPart }
func (z *zecNet) MaxDests() int           { return zecMaxDests }
func (z *zecNet) Check(addr string) error { _, err := zecScript(addr, z.net); return err }

// zecScript is the output script for a transparent address of net: P2PKH (t1…, tm…) or P2SH (t3…, t2…).
// zecswap.ParseAddress checks the Base58Check checksum and the two-byte prefix, and refuses a shielded address.
func zecScript(addr string, net zecswap.Network) ([]byte, error) {
	n, kind, h, err := zecswap.ParseAddress(addr)
	if err != nil {
		return nil, err
	}
	if n != net {
		return nil, fmt.Errorf("a Zcash %s address; this bot pays on %s", n, net)
	}
	if kind == zecswap.P2PKH {
		return zecswap.P2PKHScript(h), nil
	}
	return append(append([]byte{0xa9, 0x14}, h[:]...), 0x87), nil // OP_HASH160 <20 bytes> OP_EQUAL
}

func zecChainName(ci zecswap.ChainInfo) string {
	switch {
	case ci.Regtest:
		return "regtest"
	case ci.Chain == "main":
		return "mainnet"
	case ci.Chain == "test":
		return "testnet"
	}
	return ci.Chain
}

// info reads every node, refuses one on another chain or at another branch, and returns each one's view.
func (z *zecNet) info(ctx context.Context) ([]zecswap.ChainInfo, error) {
	var out []zecswap.ChainInfo
	for i, src := range z.nodes {
		ci, err := src.Node.ChainInfo(ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", src.Name, err)
		}
		if s, ok := src.Node.(*zecswap.ZcashRPC); ok {
			if err := s.Sane(ci); err != nil {
				return nil, fmt.Errorf("%s: %v", src.Name, err)
			}
		}
		if got := zecChainName(ci); got != z.chain {
			return nil, fmt.Errorf("%s is on Zcash %s; this bot pays on %s", src.Name, got, z.chain)
		}
		if i > 0 && ci.NextBranch != out[0].NextBranch {
			return nil, fmt.Errorf("%s is at branch %08x, %s at %08x: the nodes disagree", src.Name, ci.NextBranch, z.nodes[0].Name, out[0].NextBranch)
		}
		out = append(out, ci)
	}
	return out, nil
}

func outpoint(txid string, i uint32) string { return txid + ":" + strconv.FormatUint(uint64(i), 10) }

// Income counts an output only when every node shows it, with the same value, at least incomeConf deep.
func (z *zecNet) Income(ctx context.Context) ([]income, error) {
	cis, err := z.info(ctx)
	if err != nil {
		return nil, err
	}
	var agreed map[string]uint64
	for i, src := range z.nodes {
		us, err := src.Node.AddressUTXOs(ctx, z.treasury)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", src.Name, err)
		}
		got := map[string]uint64{}
		for _, u := range us {
			if u.Height == 0 || u.Height > cis[i].Height || cis[i].Height-u.Height+1 < incomeConf {
				continue
			}
			got[outpoint(u.TxID, u.Index)] = u.Value
		}
		if i == 0 {
			agreed = got
			continue
		}
		for id, v := range agreed {
			if w, ok := got[id]; !ok || w != v {
				delete(agreed, id)
			}
		}
	}
	var out []income
	for id, v := range agreed {
		out = append(out, income{ID: id, Amt: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func pushData(b []byte) []byte { return append([]byte{byte(len(b))}, b...) } // len(b) < 76: sig and compressed key

// Build pays each destination its part, returns the change to zecTreasury, and pays ZIP 317's conventional fee.
func (z *zecNet) Build(ctx context.Context, dests []dest, spendable func(string) bool) (built, error) {
	cis, err := z.info(ctx)
	if err != nil {
		return built{}, err
	}
	ci := cis[0]
	if ci.Regtest {
		err = zecswap.CheckBranchIDOnSchedule(ci.Upgrades, ci.Height+1, ci.NextBranch)
	} else {
		err = zecswap.CheckBranchID(ci.Net, ci.Height+1, ci.NextBranch)
	}
	if err != nil {
		return built{}, err
	}
	us, err := z.nodes[0].Node.AddressUTXOs(ctx, z.treasury)
	if err != nil {
		return built{}, err
	}
	pub, own := z.key.PubKey(), zecswap.P2PKHScript(z.key.PubKeyHash())
	type coin struct {
		c  zecswap.Coin
		id string
	}
	var coins []coin
	for _, u := range us {
		id := outpoint(u.TxID, u.Index)
		if !spendable(id) || !bytes.Equal(u.Script, own) || u.Height == 0 || u.Height > ci.Height {
			continue
		}
		c, err := zecswap.CoinOf(u)
		if err != nil {
			return built{}, err
		}
		coins = append(coins, coin{c, id})
	}
	sort.Slice(coins, func(i, j int) bool {
		if coins[i].c.Value != coins[j].c.Value {
			return coins[i].c.Value > coins[j].c.Value
		}
		return coins[i].id < coins[j].id
	})
	var outs []zecswap.TxOut
	var total uint64
	for _, d := range dests {
		spk, err := zecScript(d.Addr, z.net)
		if err != nil {
			return built{}, err
		}
		if d.Amt < zecswap.OutputFloor || d.Amt > zecswap.MaxMoney {
			return built{}, fmt.Errorf("a part of %d zatoshi", d.Amt)
		}
		outs = append(outs, zecswap.TxOut{Value: d.Amt, ScriptPubKey: spk})
		total += d.Amt
	}
	placeholder := append(pushData(make([]byte, 73)), pushData(make([]byte, len(pub)))...) // the largest signature
	tx := &zecswap.Tx{Version: 5, BranchID: ci.NextBranch, ExpiryHeight: ci.Height + zecExpiry}
	var sum uint64
	var inputs []string
	ok := false
	for _, c := range coins {
		if len(tx.In) == zecMaxInputs {
			break
		}
		sum += c.c.Value
		tx.In = append(tx.In, zecswap.TxIn{PrevOut: c.c.PrevOut, ScriptSig: placeholder, Sequence: zecswap.SequenceFinal, Value: c.c.Value, ScriptPubKey: c.c.Script})
		inputs = append(inputs, c.id)
		tx.Out = append(append([]zecswap.TxOut{}, outs...), zecswap.TxOut{ScriptPubKey: own})
		if fee := tx.ConventionalFee(); sum >= total+fee+zecswap.OutputFloor {
			tx.Out[len(outs)].Value = sum - total - fee
			ok = true
			break
		}
		tx.Out = tx.Out[:len(outs)] // a change below the floor is left to the fee
		if sum >= total+tx.ConventionalFee() {
			ok = true
			break
		}
	}
	if !ok {
		return built{}, fmt.Errorf("the treasury's spendable outputs hold %d zatoshi; the payout needs %d plus its fee", sum, total)
	}
	for i := range tx.In {
		d, err := tx.SigDigest(i, zecswap.SigHashAll)
		if err != nil {
			return built{}, err
		}
		sig := z.key.Sign(d)
		if !zecswap.VerifySig(sig, pub, d) {
			return built{}, errors.New("a signature did not verify against its own key")
		}
		tx.In[i].ScriptSig = append(pushData(sig), pushData(pub)...)
	}
	if err := tx.CheckFee(1); err != nil {
		return built{}, err
	}
	raw := tx.Serialize()
	back, err := zecswap.ParseTx(raw)
	if err != nil || !bytes.Equal(back.Serialize(), raw) {
		return built{}, errors.New("the payout does not parse back to itself")
	}
	id, idOK := zecswap.ComputeTxID(raw)
	if !idOK || id != zecswap.DisplayHex(tx.TxID()) {
		return built{}, errors.New("the payout's id cannot be computed from its bytes")
	}
	fee, err := tx.Fee()
	if err != nil {
		return built{}, err
	}
	return built{TxID: id, Blob: hex.EncodeToString(raw), Fee: fee, Expiry: tx.ExpiryHeight, Inputs: inputs}, nil
}

// Broadcast sends the recorded bytes to every node (idempotent: a node that holds them already is a success).
func (z *zecNet) Broadcast(ctx context.Context, p *payout) error {
	raw, err := hex.DecodeString(p.Blob)
	if err != nil {
		return err
	}
	id, err := zecswap.BroadcastAll(ctx, z.nodes, raw, func(f string, a ...interface{}) { fmt.Printf("ZEC payouts: "+f+"\n", a...) })
	if err != nil {
		return err
	}
	if id != p.TxID {
		return fmt.Errorf("the recorded bytes hash to %s, the payout recorded %s", id, p.TxID)
	}
	return nil
}

// State: mined incomeConf deep on every node; pending while any node knows it; dead once no node knows it and every
// node's tip is past its expiry height, since ZIP 203 lets no later block hold it; otherwise unknown.
func (z *zecNet) State(ctx context.Context, p *payout) (txState, error) {
	mined, err := zecswap.MinedEverywhere(ctx, z.nodes, p.TxID, incomeConf)
	if err != nil {
		return 0, err
	}
	if mined {
		return txMined, nil
	}
	unknown, err := zecswap.UnknownEverywhere(ctx, z.nodes, p.TxID)
	if err != nil {
		return 0, err
	}
	if !unknown {
		return txPending, nil
	}
	cis, err := z.info(ctx)
	if err != nil {
		return 0, err
	}
	for _, ci := range cis {
		if ci.Height+1 <= p.Expiry {
			return txUnknown, nil
		}
	}
	return txDead, nil
}
