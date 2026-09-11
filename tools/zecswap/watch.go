package zecswap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// The watcher: what the node says about the script's address, checked before anything acts on it.
//
// The funding is found through getaddressutxos on the script's P2SH address and then read two more ways: the
// block the node names at that height (getblockhash) must be the block the transaction is in (getrawtransaction),
// and the transaction's own bytes must pay the script that amount. Its depth is tip − height + 1, and it counts
// only while the block at that height is still the one recorded: a reorganisation that removes the funding
// changes that hash, and the depth is then an error rather than a number. Nothing here decides — the state
// machines compare the depth with the policy — but nothing here reports a depth it did not re-check.
//
// A spend of the funding is found through getaddresstxids over the few blocks in which it can have happened, and
// read out of the spending transaction's own bytes: the secret is the 32-byte push that hashes to the script's
// hash, so a node that lied about the transaction could hide the secret but never forge one.

// ErrReorged means the block that held the funding is no longer the node's block at that height.
var ErrReorged = errors.New("zecswap: the block that held the funding is no longer on the node's best chain")

// Funding is the script's funding output as it was seen mined.
type Funding struct {
	TxID      string `json:"txid"` // display order
	Index     uint32 `json:"index"`
	Value     uint64 `json:"value"`
	Height    uint32 `json:"height"`
	BlockHash string `json:"blockHash"`
}

// OutPoint is the funding in the form a spend names it.
func (f Funding) OutPoint() (OutPoint, error) {
	h, err := ParseDisplayHex(f.TxID)
	if err != nil {
		return OutPoint{}, err
	}
	return OutPoint{Hash: h, Index: f.Index}, nil
}

// ScriptOutputs is getaddressutxos for the script's address. The address encodes exactly one script, so an output
// the node lists under it with another script is a node that cannot be read.
func ScriptOutputs(ctx context.Context, zc Zcash, h HTLC, net Network) ([]UTXO, error) {
	addr := h.Address(net)
	us, err := zc.AddressUTXOs(ctx, addr)
	if err != nil {
		return nil, err
	}
	spk := h.ScriptPubKey()
	for _, u := range us {
		if !bytes.Equal(u.Script, spk) {
			return nil, fmt.Errorf("zecswap: the node lists an output under %s whose script is not the swap script's", addr)
		}
	}
	return us, nil
}

// SeeFunding checks one output of the script's address three ways and returns it as a Funding.
func SeeFunding(ctx context.Context, zc Zcash, h HTLC, u UTXO) (Funding, error) {
	bh, err := zc.BlockHash(ctx, u.Height)
	if err != nil {
		return Funding{}, err
	}
	rt, err := zc.RawTx(ctx, u.TxID)
	if err != nil {
		return Funding{}, err
	}
	if !rt.Mined || rt.Height != u.Height || rt.BlockHash != bh {
		return Funding{}, fmt.Errorf("%w: getaddressutxos puts %s at height %d, getrawtransaction at %d in %s, getblockhash says %s", ErrReorged, u.TxID, u.Height, rt.Height, rt.BlockHash, bh)
	}
	_, outs, err := transparentParts(rt.Raw)
	if err != nil {
		return Funding{}, err
	}
	if int(u.Index) >= len(outs) || !bytes.Equal(outs[u.Index].ScriptPubKey, h.ScriptPubKey()) || outs[u.Index].Value != u.Value {
		return Funding{}, fmt.Errorf("zecswap: transaction %s's output %d is not %d zatoshi to the swap script, whatever the address index says", u.TxID, u.Index, u.Value)
	}
	return Funding{TxID: u.TxID, Index: u.Index, Value: u.Value, Height: u.Height, BlockHash: bh}, nil
}

// Depth is the funding's confirmations at tip, after checking the block at its height is still the one recorded.
func Depth(ctx context.Context, zc Zcash, f Funding, tip uint32) (uint32, error) {
	if tip < f.Height {
		return 0, fmt.Errorf("%w: the tip %d is below the funding's height %d", ErrReorged, tip, f.Height)
	}
	bh, err := zc.BlockHash(ctx, f.Height)
	if err != nil {
		return 0, err
	}
	if bh != f.BlockHash {
		return 0, fmt.Errorf("%w: height %d was %s and is now %s", ErrReorged, f.Height, f.BlockHash, bh)
	}
	return tip - f.Height + 1, nil
}

// Spend kinds.
const (
	SpendClaim  = "claim"
	SpendRefund = "refund"
	SpendOther  = "other"
)

// Spend is a transaction spending the funding output.
type Spend struct {
	TxID   string
	Kind   string
	Secret [32]byte // a claim's secret, checked against the script's hash
	Mined  bool
	Height uint32
}

// OutputUnspent asks the node (gettxout, best chain only) whether the funding output is still unspent. The answer
// is one output's, whatever anyone has paid the address, so padding the address cannot make this read expensive.
func OutputUnspent(ctx context.Context, zc Zcash, f Funding, spk []byte) (bool, error) {
	out, err := zc.TxOut(ctx, f.TxID, f.Index)
	if err != nil || out == nil {
		return false, err
	}
	if !bytes.Equal(out.Script, spk) {
		return false, fmt.Errorf("zecswap: gettxout says %s:%d pays another script than the swap's", f.TxID, f.Index)
	}
	return true, nil
}

// ScanCursor is how far a search for the funding's spend has read: block by block from Base, and within a block
// by the node's own order of its transactions. It lives in the state file, so the search survives a restart.
type ScanCursor struct {
	Base   uint32 `json:"base"`
	Height uint32 `json:"height"`
	Index  int    `json:"index"`
}

// SpendScanBudget is how many node reads one call of FindSpendStep may make.
const SpendScanBudget = 200

// FindSpendStep looks for the transaction spending f among the address's mined transactions, reading at most
// budget answers from the node — one per block asked about, one per transaction read — and leaving the cursor
// where it stopped. Anyone can pay the address, and a counterparty who wanted to hide his claim could bury it
// among thousands of payments; this makes every pass cost the same, and the whole search cost only what the
// padding cost its sender in fees. On reaching the tip without a find it starts again from Base on the next
// call, because a reorganisation may have moved the spend below where it had read. A transaction the node lists
// but cannot produce, or whose bytes do not parse, is passed over.
func FindSpendStep(ctx context.Context, zc Zcash, h HTLC, net Network, f Funding, cur *ScanCursor, tip uint32, budget int) (*Spend, error) {
	op, err := f.OutPoint()
	if err != nil {
		return nil, err
	}
	if cur.Base < f.Height {
		cur.Base = f.Height
	}
	if cur.Height < cur.Base || cur.Height > tip {
		cur.Height, cur.Index = cur.Base, 0
	}
	addr := h.Address(net)
	for reads := 0; cur.Height <= tip && reads < budget; {
		ids, err := zc.AddressTxIDs(ctx, addr, cur.Height, cur.Height)
		reads++
		if err != nil {
			return nil, err
		}
		for cur.Index < len(ids) && reads < budget {
			id := ids[cur.Index]
			cur.Index++
			if id == f.TxID {
				continue
			}
			reads++
			// Nothing at the address is passed over. A transaction the node lists and cannot produce, or whose
			// bytes this build cannot read — a version it has never heard of — might be the claim that carries the
			// secret, so the search stops there and says so, and the next pass tries again. The taker's refund does
			// not wait on this search.
			rt, err := zc.RawTx(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("zecswap: transaction %s pays or spends the swap address and cannot be read (%v): stopping rather than skipping a possible spend", id, err)
			}
			ins, _, err := transparentParts(rt.Raw)
			if err != nil {
				return nil, fmt.Errorf("zecswap: transaction %s pays or spends the swap address and cannot be read (%v): stopping rather than skipping a possible spend", id, err)
			}
			for _, in := range ins {
				if in.PrevOut != op {
					continue
				}
				kind, secret := ClassifySpend(in.ScriptSig, h)
				return &Spend{TxID: id, Kind: kind, Secret: secret, Mined: rt.Mined, Height: rt.Height}, nil
			}
		}
		if cur.Index >= len(ids) {
			cur.Height++
			cur.Index = 0
		}
	}
	return nil, nil
}

// ClassifySpend reads which branch of the script a scriptSig takes. A claim returns the secret: the 32-byte push
// whose SHA256 is the script's hash, wherever the spender put it — the script's own SIZE and SHA256 checks mean a
// valid claim carries exactly one.
func ClassifySpend(scriptSig []byte, h HTLC) (string, [32]byte) {
	var secret [32]byte
	pushes, err := scriptPushes(scriptSig)
	if err != nil || len(pushes) < 2 || !bytes.Equal(pushes[len(pushes)-1], h.RedeemScript()) {
		return SpendOther, secret
	}
	if !castToBool(pushes[len(pushes)-2]) {
		return SpendRefund, secret
	}
	for _, p := range pushes[:len(pushes)-2] {
		if len(p) == SecretBytes && sha256.Sum256(p) == h.Hash {
			copy(secret[:], p)
			return SpendClaim, secret
		}
	}
	return SpendOther, secret
}

// castToBool is the interpreter's truth of a stack item: any non-zero byte, except a lone sign bit at the end.
func castToBool(b []byte) bool {
	for i, c := range b {
		if c != 0 {
			return !(i == len(b)-1 && c == 0x80)
		}
	}
	return false
}

// scriptPushes reads a push-only script (every P2SH scriptSig is one: SIGPUSHONLY is consensus for P2SH) as its
// stack items. OP_0 is the empty item, OP_1NEGATE and OP_1..OP_16 the one-byte numbers they push.
func scriptPushes(s []byte) ([][]byte, error) {
	var out [][]byte
	for i := 0; i < len(s); {
		op := s[i]
		i++
		var n uint64
		switch {
		case op == 0x00:
			out = append(out, []byte{})
			continue
		case op <= 0x4b:
			n = uint64(op)
		case op == 0x4c:
			if i+1 > len(s) {
				return nil, errors.New("zecswap: truncated PUSHDATA1")
			}
			n = uint64(s[i])
			i++
		case op == 0x4d:
			if i+2 > len(s) {
				return nil, errors.New("zecswap: truncated PUSHDATA2")
			}
			n = uint64(binary.LittleEndian.Uint16(s[i:]))
			i += 2
		case op == 0x4e:
			if i+4 > len(s) {
				return nil, errors.New("zecswap: truncated PUSHDATA4")
			}
			n = uint64(binary.LittleEndian.Uint32(s[i:]))
			i += 4
		case op == 0x4f:
			out = append(out, []byte{0x81})
			continue
		case op >= 0x51 && op <= 0x60:
			out = append(out, []byte{op - 0x50})
			continue
		default:
			return nil, fmt.Errorf("zecswap: opcode %02x in a scriptSig that must be push-only", op)
		}
		if n > uint64(len(s)-i) {
			return nil, errors.New("zecswap: a push runs past the end of the script")
		}
		out = append(out, s[i:i+int(n)])
		i += int(n)
	}
	return out, nil
}

// transparentParts reads the transparent inputs and outputs of a serialized transaction of any version Zcash has
// had, 1 to 5, and nothing after them. A maker's claim is his own to build — his wallet may make it v4, or add
// Sapling or Orchard change — and the taker must read the secret out of it whatever it is; the transparent parts
// sit in the same place relative to the header in every version (ZIP 225 for v5; the protocol specification §7.1
// for the rest).
func transparentParts(raw []byte) ([]TxIn, []TxOut, error) {
	r := &reader{b: raw}
	header := r.u32()
	if r.err != nil {
		return nil, nil, r.err
	}
	overwintered := header&0x80000000 != 0
	version := header & 0x7fffffff
	switch {
	case version == 0 || version > 6:
		return nil, nil, fmt.Errorf("zecswap: transaction version %d, which this build cannot read", version)
	case overwintered != (version >= 3):
		return nil, nil, fmt.Errorf("zecswap: transaction version %d with fOverwintered %v", version, overwintered)
	}
	if overwintered {
		r.u32() // nVersionGroupId
	}
	if version >= 5 {
		r.bytes(12) // nConsensusBranchId, nLockTime, nExpiryHeight: v5 (ZIP 225) and v6 (ZIP 229) alike
	}
	nIn := r.compact()
	if r.err != nil {
		return nil, nil, r.err
	}
	if nIn > uint64(len(raw)-r.p)/41 {
		return nil, nil, errors.New("zecswap: more inputs than the transaction has bytes for")
	}
	ins := make([]TxIn, 0, nIn)
	for i := uint64(0); i < nIn && r.err == nil; i++ {
		var in TxIn
		copy(in.PrevOut.Hash[:], r.bytes(32))
		in.PrevOut.Index = r.u32()
		in.ScriptSig = append([]byte{}, r.varBytes()...)
		in.Sequence = r.u32()
		ins = append(ins, in)
	}
	nOut := r.compact()
	if r.err != nil {
		return nil, nil, r.err
	}
	if nOut > uint64(len(raw)-r.p)/9 {
		return nil, nil, errors.New("zecswap: more outputs than the transaction has bytes for")
	}
	outs := make([]TxOut, 0, nOut)
	for i := uint64(0); i < nOut && r.err == nil; i++ {
		var out TxOut
		out.Value = r.u64()
		out.ScriptPubKey = append([]byte{}, r.varBytes()...)
		outs = append(outs, out)
	}
	if r.err != nil {
		return nil, nil, r.err
	}
	return ins, outs, nil
}

// Broadcast sends a transaction this package built and answers with its id. It is idempotent: a transaction the
// node already holds — in its mempool or in a block — is a success, whatever words the node chose to say so, so a
// restart that sends the same bytes again is harmless. The node's txid must be the one computed here.
func Broadcast(ctx context.Context, zc Zcash, raw []byte) (string, error) {
	id, ok := ComputeTxID(raw)
	if !ok {
		return "", errors.New("zecswap: Broadcast sends only transactions whose id this package can compute")
	}
	got, err := zc.SendRawTx(ctx, raw)
	if err == nil {
		if got != id {
			return "", fmt.Errorf("zecswap: the node calls the transaction %s; its bytes hash to %s", got, id)
		}
		return id, nil
	}
	var re *RPCError
	if errors.As(err, &re) && re.Code == RPCTransactionAlreadyInChain {
		return id, nil
	}
	if _, kerr := zc.RawTx(ctx, id); kerr == nil {
		return id, nil
	}
	return id, err
}

// TxKnown asks the node about a transaction by id: known is false for one it has never heard of or has dropped.
func TxKnown(ctx context.Context, zc Zcash, txid string) (RawTx, bool, error) {
	rt, err := zc.RawTx(ctx, txid)
	if errors.Is(err, ErrTxNotFound) {
		return RawTx{}, false, nil
	}
	if err != nil {
		return RawTx{}, false, err
	}
	return rt, true, nil
}
