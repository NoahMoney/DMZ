package zecswap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// More than one node, for the reads a swap's safety rests on.
//
// A companion trusts its nodes for what the chains contain (node.go, dero.go). A node that lies in collusion with
// the counterparty costs more than docs/ZEC_SWAP.md §9 Z14 and Z15 say:
//
//   - The maker's Zcash node shows a funding that does not exist, at full depth. The maker's companion claims it
//     through that node and so hands the node the secret; with swap.bas v2's Claim, which pays the stored taker
//     whoever sends it, the node's owner completes the taker's DERO leg, and the taker has the maker's asset
//     without ever funding. "The claim fails, nothing is revealed" holds only when the liar is not the
//     counterparty's accomplice.
//   - The taker's DERO node shows a lock that does not exist. She funds; the maker claims the ZEC with the secret;
//     her DERO Claim finds no lock, and she has lost the whole ZEC leg, not two hours.
//
// So a companion may be given further nodes — witnesses — for either chain, and the step each side's safety rests
// on waits until every node agrees:
//
//   - the maker reveals the secret only when every Zcash node shows the exact funding outpoint, value and script,
//     unspent, at C_zec depth, in the same block, in a transaction whose bytes hash to its id (ConfirmFunding);
//   - the taker funds only when every DERO node shows exactly the lock she checked — maker, taker, hash, asset,
//     amount, expiry and state — past the stable depth (ConfirmEscrow);
//   - either party ends a swap, and so stops watching it, only when every node agrees on how it ended
//     (EscrowAgreed, MinedEverywhere, UnknownEverywhere).
//
// A node that disagrees or does not answer stops that step, and every node's answer is logged. Reads that only
// schedule — the Zcash height at which the refund may go out, the height at which the maker stops claiming — take
// the most cautious answer among the nodes that respond, and a witness that is silent never delays a refund.
//
// This turns "one lying node" into "every node lying together". It does not remove the trust: every node named
// here is a trusted party, and a configuration with a single node — above all one on someone else's machine — is
// one where a single liar can cost the whole swap. The default remains a node of one's own.

// ZcashSource is one Zcash node with the name its answers are logged under.
type ZcashSource struct {
	Name string
	Node Zcash
}

// DeroReader is the read side of a DERO daemon; DeroRPC is one, and a witness needs no wallet.
type DeroReader interface {
	Height(ctx context.Context) (uint64, error)
	StableHeight(ctx context.Context) (uint64, error)
	ReadOffer(ctx context.Context, id uint64) (Offer, error)
	ReadEscrow(ctx context.Context, id uint64) (Escrow, error)
	// CodeSHA256 is the SHA-256 of a contract's code as the daemon stores it (pin.go).
	CodeSHA256(ctx context.Context, scid string) (string, error)
	// EscrowWMin is the escrow's install-time minimum width (swap.bas v2's wmin): the same code with a smaller
	// wmin has the same code hash, so it is read, not assumed.
	EscrowWMin(ctx context.Context) (uint64, error)
	// Registered says whether a DERO address has an account on this chain.
	Registered(ctx context.Context, addr string) (bool, error)
}

// DeroSource is one DERO daemon with the name its answers are logged under.
type DeroSource struct {
	Name string
	Node DeroReader
}

// ConfirmFunding is the maker's gate before he reveals the secret.
func ConfirmFunding(ctx context.Context, srcs []ZcashSource, chain string, h HTLC, f Funding, conf uint32, logf func(string, ...interface{})) error {
	if len(srcs) == 0 {
		return errors.New("zecswap: no Zcash node to confirm the funding")
	}
	spk := h.ScriptPubKey()
	for _, src := range srcs {
		if err := confirmFundingAt(ctx, src.Node, chain, spk, f, conf); err != nil {
			logf("%s does NOT confirm the funding %s:%d (%d zatoshi in block %s at %d): %v", src.Name, f.TxID, f.Index, f.Value, f.BlockHash, f.Height, err)
			return fmt.Errorf("zecswap: %s does not confirm the funding %s:%d, so the secret is not revealed: %w", src.Name, f.TxID, f.Index, err)
		}
		logf("%s confirms the funding %s:%d, %d zatoshi to the script, in block %s at %d, at least %d deep", src.Name, f.TxID, f.Index, f.Value, f.BlockHash, f.Height, conf)
	}
	return nil
}

func confirmFundingAt(ctx context.Context, zc Zcash, chain string, spk []byte, f Funding, conf uint32) error {
	ci, err := zc.ChainInfo(ctx)
	if err != nil {
		return err
	}
	if ci.Chain != chain {
		return fmt.Errorf("it is on Zcash %s, the swap on %s", ci.Chain, chain)
	}
	out, err := zc.TxOut(ctx, f.TxID, f.Index)
	if err != nil {
		return err
	}
	if out == nil {
		return errors.New("gettxout: no such unspent output")
	}
	if !bytes.Equal(out.Script, spk) {
		return errors.New("gettxout: the output pays another script")
	}
	if out.Confirmations < conf {
		return fmt.Errorf("gettxout: %d confirmations", out.Confirmations)
	}
	rt, err := zc.RawTx(ctx, f.TxID)
	if err != nil {
		return err
	}
	if !rt.Verified {
		return errors.New("getrawtransaction: the transaction's bytes cannot be checked against its id here")
	}
	if !rt.Mined || rt.Height != f.Height || rt.BlockHash != f.BlockHash {
		return fmt.Errorf("getrawtransaction: in block %s at %d (mined %v)", rt.BlockHash, rt.Height, rt.Mined)
	}
	if rt.Confirmations < conf {
		return fmt.Errorf("getrawtransaction: %d confirmations", rt.Confirmations)
	}
	_, outs, err := transparentParts(rt.Raw)
	if err != nil {
		return err
	}
	if int(f.Index) >= len(outs) || outs[f.Index].Value != f.Value || !bytes.Equal(outs[f.Index].ScriptPubKey, spk) {
		return errors.New("the transaction's bytes do not pay the script that amount at that index")
	}
	bh, err := zc.BlockHash(ctx, f.Height)
	if err != nil {
		return err
	}
	if bh != f.BlockHash {
		return fmt.Errorf("getblockhash: height %d is %s", f.Height, bh)
	}
	return nil
}

// ConfirmEscrow is the taker's gate before she funds: every DERO node shows exactly want, past the stable depth
// measured from the height it was first seen at, and check passes against that node's own height.
func ConfirmEscrow(ctx context.Context, srcs []DeroSource, want Escrow, seenHeight uint64, check func(Escrow, uint64) error, logf func(string, ...interface{})) error {
	if len(srcs) == 0 {
		return errors.New("zecswap: no DERO node to confirm the lock")
	}
	for _, src := range srcs {
		err := func() error {
			e, err := src.Node.ReadEscrow(ctx, want.ID)
			if err != nil {
				return err
			}
			if e != want {
				return fmt.Errorf("it shows another lock: %+v", e)
			}
			stable, err := src.Node.StableHeight(ctx)
			if err != nil {
				return err
			}
			if stable < seenHeight {
				return fmt.Errorf("its stable height %d is below %d, the height the lock was first seen at", stable, seenHeight)
			}
			h, err := src.Node.Height(ctx)
			if err != nil {
				return err
			}
			return check(e, h)
		}()
		if err != nil {
			logf("%s does NOT confirm escrow %d: %v", src.Name, want.ID, err)
			return fmt.Errorf("zecswap: %s does not confirm escrow %d, so nothing is funded: %w", src.Name, want.ID, err)
		}
		logf("%s confirms escrow %d: %d of %q for this taker under %s until %d, past the stable depth", src.Name, want.ID, want.Amount, want.Asset, want.Hash, want.Expiry)
	}
	return nil
}

// EscrowAgreed reads an escrow from every DERO node and returns it only if they all say the same.
func EscrowAgreed(ctx context.Context, srcs []DeroSource, id uint64) (Escrow, error) {
	var first Escrow
	for i, src := range srcs {
		e, err := src.Node.ReadEscrow(ctx, id)
		if err != nil {
			return Escrow{}, fmt.Errorf("zecswap: %s: %w", src.Name, err)
		}
		if i == 0 {
			first = e
		} else if e != first {
			return Escrow{}, fmt.Errorf("zecswap: the DERO nodes disagree about escrow %d: %s says state %d, %s says %d", id, srcs[0].Name, first.State, src.Name, e.State)
		}
	}
	return first, nil
}

// MinDeroHeight is the lowest height any DERO node reports: the one to give up by, so that no single node can
// make a party abandon a claim it could still send.
func MinDeroHeight(ctx context.Context, srcs []DeroSource) (uint64, error) {
	var min uint64
	for i, src := range srcs {
		h, err := src.Node.Height(ctx)
		if err != nil {
			return 0, fmt.Errorf("zecswap: %s: %w", src.Name, err)
		}
		if i == 0 || h < min {
			min = h
		}
	}
	return min, nil
}

// BroadcastAll sends a transaction to every Zcash node; it has gone out if any took it.
func BroadcastAll(ctx context.Context, srcs []ZcashSource, raw []byte, logf func(string, ...interface{})) (string, error) {
	var id string
	var firstErr error
	ok := false
	for _, src := range srcs {
		got, err := Broadcast(ctx, src.Node, raw)
		id = got
		if err != nil {
			logf("%s did not take %s: %v", src.Name, got, err)
			if firstErr == nil {
				firstErr = err
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

// MinedEverywhere says whether every Zcash node shows txid mined at least conf deep.
func MinedEverywhere(ctx context.Context, srcs []ZcashSource, txid string, conf uint32) (bool, error) {
	for _, src := range srcs {
		rt, known, err := TxKnown(ctx, src.Node, txid)
		if err != nil {
			return false, fmt.Errorf("zecswap: %s: %w", src.Name, err)
		}
		if !known || !rt.Mined || rt.Confirmations < conf {
			return false, nil
		}
	}
	return true, nil
}

// UnknownEverywhere says whether no Zcash node knows txid at all.
func UnknownEverywhere(ctx context.Context, srcs []ZcashSource, txid string) (bool, error) {
	for _, src := range srcs {
		_, known, err := TxKnown(ctx, src.Node, txid)
		if err != nil {
			return false, fmt.Errorf("zecswap: %s: %w", src.Name, err)
		}
		if known {
			return false, nil
		}
	}
	return true, nil
}

// TrustWarnings is what a person must read before a swap runs on these nodes: each is a trusted party, and a chain
// read through one node alone is a chain on which one liar can cost the whole swap.
func TrustWarnings(zcash, dero []string) []string {
	var out []string
	for _, set := range []struct {
		chain string
		flag  string
		eps   []string
	}{{"Zcash", "-zcash", zcash}, {"DERO", "-daemon", dero}} {
		for _, e := range set.eps {
			where := "on this machine"
			if !IsLoopback(e) {
				where = "ON ANOTHER MACHINE"
			}
			out = append(out, fmt.Sprintf("%s node %s (%s) is a trusted party: the companion believes it about what the %s chain contains.", set.chain, e, where, set.chain))
		}
		switch {
		case len(set.eps) == 1 && !IsLoopback(set.eps[0]):
			out = append(out, fmt.Sprintf("WARNING: the only %s node, %s, is on another machine. A single remote node that colludes with your counterparty can cost you the whole swap (docs/ZEC_SWAP.md §9 Z14, Z15, and quorum.go). Run your own node, and name a second one with another %s.", set.chain, set.eps[0], set.flag))
		case len(set.eps) == 1:
			out = append(out, fmt.Sprintf("WARNING: %s is the only %s node. If it lies — compromised, or run by your counterparty — a single node can cost you the whole swap. Naming a second node you trust with another %s makes that take two.", set.eps[0], set.chain, set.flag))
		}
	}
	return out
}

// IsLoopback says whether an endpoint ("host:port", a URL, or a bare host) names this machine.
func IsLoopback(endpoint string) bool {
	host := endpoint
	if strings.Contains(endpoint, "://") {
		u, err := url.Parse(endpoint)
		if err != nil {
			return false
		}
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
