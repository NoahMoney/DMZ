package zcashvault

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"veil/tools/zcashtx"
)

// The Zcash nodes a signer reads, and what they must agree on.
//
// Every node named in a signer's configuration is a trusted party for what the chain contains; more than one turns
// "one lying node" into "every node lying together", and nothing here removes the trust. The rules, in the direction
// each failure costs least:
//
//   - the tip is the LOWEST any node reports, and the finalized height is that tip less the finality depth (Zebra's
//     100-block reorganisation limit); every node must name the same block at the finalized height, or nothing is
//     read at all;
//   - a deposit is reported only when every node shows the same transaction, mined in the same block, at or below the
//     finalized height, paying the same amount to the same vault address — so a node inventing a deposit is outvoted
//     by one honest node, and a node hiding one only delays it;
//   - the reserve is the LOWEST total any node shows, so a lying node can make the vault look short (a halt vote,
//     which clears itself) but never hide a shortfall;
//   - a payment is certified only when every node shows it mined in the same block at or below the finalized height.

type node struct {
	name string
	zc   zcashtx.Zcash
}

type nodes struct {
	list  []node
	net   Net
	depth uint32
}

// chainView is one agreed reading of the chain.
type chainView struct {
	Tip       uint32 // the lowest tip any node reports
	Final     uint32 // Tip - depth: final by Zcash's consensus reorganisation limit
	FinalHash string // the block every node names at Final
	Branch    uint32 // consensus.nextblock, the same on every node
}

func (ns *nodes) view(ctx context.Context) (chainView, error) {
	if len(ns.list) == 0 {
		return chainView{}, errors.New("zcashvault: no Zcash node is configured")
	}
	var v chainView
	for i, n := range ns.list {
		ci, err := n.zc.ChainInfo(ctx)
		if err != nil {
			return chainView{}, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		if ci.Chain != ns.net.chainName() {
			return chainView{}, fmt.Errorf("zcashvault: node %s is on Zcash %q; this vault is on %s", n.name, ci.Chain, ns.net)
		}
		if ci.Regtest {
			err = zcashtx.CheckBranchIDOnSchedule(ci.Upgrades, ci.Height+1, ci.NextBranch)
		} else {
			err = zcashtx.CheckBranchID(ns.net.addrNet(), ci.Height+1, ci.NextBranch)
		}
		if err != nil {
			return chainView{}, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		if i == 0 || ci.Height < v.Tip {
			v.Tip = ci.Height
		}
		if i == 0 {
			v.Branch = ci.NextBranch
		} else if ci.NextBranch != v.Branch {
			return chainView{}, fmt.Errorf("zcashvault: the nodes disagree on the next block's consensus branch (%08x, %08x): not signing across an upgrade boundary", v.Branch, ci.NextBranch)
		}
	}
	if v.Tip <= ns.depth {
		return chainView{}, fmt.Errorf("zcashvault: the chain (%d blocks) is not yet deeper than the finality depth %d", v.Tip, ns.depth)
	}
	v.Final = v.Tip - ns.depth
	for i, n := range ns.list {
		h, err := n.zc.BlockHash(ctx, v.Final)
		if err != nil {
			return chainView{}, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		if i == 0 {
			v.FinalHash = h
		} else if h != v.FinalHash {
			return chainView{}, fmt.Errorf("zcashvault: nodes %s and %s name different blocks at the finalized height %d: they are not on one chain", ns.list[0].name, n.name, v.Final)
		}
	}
	return v, nil
}

// minedTx is a transaction every node shows mined in the same best-chain block, its bytes checked against its id.
type minedTx struct {
	TxID      string // display order
	Raw       []byte
	Height    uint32
	BlockHash string
	Tx        *zcashtx.Tx
}

// agreedMined reads txid from every node. It fails unless every node knows it, mined, in the same block, with bytes
// that hash to txid; unknown is true when no node knows it at all.
func (ns *nodes) agreedMined(ctx context.Context, txid string) (m *minedTx, unknown bool, err error) {
	unknownAll := true
	for i, n := range ns.list {
		rt, known, err := zcashtx.TxKnown(ctx, n.zc, txid)
		if err != nil {
			return nil, false, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		if !known {
			if !unknownAll || i > 0 && m != nil {
				return nil, false, fmt.Errorf("zcashvault: node %s does not know %s, which another node does", n.name, txid)
			}
			continue
		}
		if i > 0 && unknownAll {
			return nil, false, fmt.Errorf("zcashvault: node %s knows %s, which %s does not", n.name, txid, ns.list[0].name)
		}
		unknownAll = false
		if !rt.Verified {
			return nil, false, fmt.Errorf("zcashvault: node %s: the bytes of %s cannot be checked against its id", n.name, txid)
		}
		if !rt.Mined {
			return nil, false, fmt.Errorf("zcashvault: node %s shows %s unmined", n.name, txid)
		}
		if m == nil {
			tx, err := zcashtx.ParseTx(rt.Raw)
			if err != nil {
				return nil, false, fmt.Errorf("zcashvault: %s: %w", txid, err)
			}
			m = &minedTx{TxID: txid, Raw: rt.Raw, Height: rt.Height, BlockHash: rt.BlockHash, Tx: tx}
		} else if rt.Height != m.Height || rt.BlockHash != m.BlockHash || !bytes.Equal(rt.Raw, m.Raw) {
			return nil, false, fmt.Errorf("zcashvault: nodes disagree about %s (block %s at %d, and %s at %d)", txid, m.BlockHash, m.Height, rt.BlockHash, rt.Height)
		}
	}
	if unknownAll {
		return nil, true, nil
	}
	return m, false, nil
}

// txSeen is what the nodes know of one transaction, without requiring agreement: for deciding whether a payment is
// already out there.
type txSeen struct {
	KnownAny  bool   // some node holds it, in a block or its mempool
	MinedAny  bool   // some node has it in its best chain
	MinedAll  bool   // every node has it in its best chain
	MinHeight uint32 // the lowest height a node reports it mined at
}

func (ns *nodes) seen(ctx context.Context, txid string) (txSeen, error) {
	var s txSeen
	s.MinedAll = true
	for _, n := range ns.list {
		rt, known, err := zcashtx.TxKnown(ctx, n.zc, txid)
		if err != nil {
			return txSeen{}, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		if known {
			s.KnownAny = true
		}
		if known && rt.Mined {
			if !s.MinedAny || rt.Height < s.MinHeight {
				s.MinHeight = rt.Height
			}
			s.MinedAny = true
		} else {
			s.MinedAll = false
		}
	}
	return s, nil
}

// agreedUTXOs is the set of confirmed unspent outputs paying addr that EVERY node reports, each checked to pay the
// script given; an output only some nodes report is left out, which costs at most a delay.
func (ns *nodes) agreedUTXOs(ctx context.Context, addr string, script []byte) ([]zcashtx.UTXO, error) {
	var agreed map[string]zcashtx.UTXO
	for i, n := range ns.list {
		us, err := n.zc.AddressUTXOs(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		here := map[string]zcashtx.UTXO{}
		for _, u := range us {
			if !bytes.Equal(u.Script, script) {
				return nil, fmt.Errorf("zcashvault: node %s lists an output of %s that pays another script", n.name, addr)
			}
			here[u.TxID+":"+fmt.Sprint(u.Index)] = u
		}
		if i == 0 {
			agreed = here
			continue
		}
		for k, u := range agreed {
			if v, ok := here[k]; !ok || v.Value != u.Value || v.Height != u.Height {
				delete(agreed, k)
			}
		}
	}
	out := make([]zcashtx.UTXO, 0, len(agreed))
	for _, u := range agreed {
		out = append(out, u)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].TxID != out[b].TxID {
			return out[a].TxID < out[b].TxID
		}
		return out[a].Index < out[b].Index
	})
	return out, nil
}

// lowestBalance is the lowest total of addr's confirmed unspent outputs any node reports.
func (ns *nodes) balances(ctx context.Context, addr string, script []byte) ([]uint64, error) {
	out := make([]uint64, len(ns.list))
	for i, n := range ns.list {
		us, err := n.zc.AddressUTXOs(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		for _, u := range us {
			if !bytes.Equal(u.Script, script) {
				return nil, fmt.Errorf("zcashvault: node %s lists an output of %s that pays another script", n.name, addr)
			}
			if out[i]+u.Value < out[i] || out[i]+u.Value > zcashtx.MaxMoney {
				return nil, errors.New("zcashvault: a balance above the Zcash supply")
			}
			out[i] += u.Value
		}
	}
	return out, nil
}

// agreedHistory is the set of transactions paying or spending addr in blocks [1, upTo], which every node must report
// identically; upTo is at or below the finalized height, where honest nodes cannot differ.
func (ns *nodes) agreedHistory(ctx context.Context, addr string, upTo uint32) ([]string, error) {
	if upTo < 1 {
		return nil, nil
	}
	var first []string
	for i, n := range ns.list {
		ids, err := n.zc.AddressTxIDs(ctx, addr, 1, upTo)
		if err != nil {
			return nil, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		sort.Strings(ids)
		ids = dedup(ids)
		if i == 0 {
			first = ids
			continue
		}
		if len(ids) != len(first) {
			return nil, fmt.Errorf("zcashvault: nodes %s and %s list %d and %d transactions for %s up to %d", ns.list[0].name, n.name, len(first), len(ids), addr, upTo)
		}
		for j := range ids {
			if ids[j] != first[j] {
				return nil, fmt.Errorf("zcashvault: nodes %s and %s list different transactions for %s up to %d", ns.list[0].name, n.name, addr, upTo)
			}
		}
	}
	return first, nil
}

// history is one node's view of addr's transactions up to upTo, without agreement (for looking a payment up).
func (ns *nodes) historyAny(ctx context.Context, addr string, upTo uint32) ([]string, error) {
	set := map[string]bool{}
	for _, n := range ns.list {
		ids, err := n.zc.AddressTxIDs(ctx, addr, 1, upTo)
		if err != nil {
			return nil, fmt.Errorf("zcashvault: node %s: %w", n.name, err)
		}
		for _, id := range ids {
			set[id] = true
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// rawAny is the transaction's bytes from the first node that knows it, checked against its id.
func (ns *nodes) rawAny(ctx context.Context, txid string) (*zcashtx.Tx, []byte, error) {
	var lastErr error
	for _, n := range ns.list {
		rt, known, err := zcashtx.TxKnown(ctx, n.zc, txid)
		if err != nil {
			lastErr = err
			continue
		}
		if !known {
			continue
		}
		if !rt.Verified {
			return nil, nil, fmt.Errorf("zcashvault: node %s: the bytes of %s cannot be checked against its id", n.name, txid)
		}
		tx, err := zcashtx.ParseTx(rt.Raw)
		if err != nil {
			return nil, nil, err
		}
		return tx, rt.Raw, nil
	}
	if lastErr != nil {
		return nil, nil, lastErr
	}
	return nil, nil, fmt.Errorf("%w: %s", zcashtx.ErrTxNotFound, txid)
}

// broadcast sends raw to every node; it has gone out if any took it (or already held it).
func (ns *nodes) broadcast(ctx context.Context, raw []byte) (string, error) {
	var id string
	var firstErr error
	ok := false
	for _, n := range ns.list {
		got, err := zcashtx.Broadcast(ctx, n.zc, raw)
		id = got
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("zcashvault: node %s: %w", n.name, err)
			}
			continue
		}
		ok = true
	}
	if ok {
		return id, nil
	}
	return id, firstErr
}

func dedup(s []string) []string {
	out := s[:0]
	for i, x := range s {
		if i == 0 || x != s[i-1] {
			out = append(out, x)
		}
	}
	return out
}
