package zcashvault

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"veil/tools/zcashtx"
)

// The release transaction, built the same way by every signer from what the nodes agree on, and checked by every
// signer against the contract's withdrawal before any of its inputs is signed.
//
//	version    5 (ZIP 225), the next block's consensus branch id
//	nLockTime  j, the withdrawal's index on DERO. Every input's nSequence is final, so the lock time is ignored by
//	           consensus and serves only as the on-chain marker that names the withdrawal this transaction pays;
//	           signers find a payment for j by it after losing their ledger.
//	nExpiry    epoch + finality depth + epoch length + expiry window: a height every signer in the same epoch
//	           computes identically, some 40 to 50 blocks past the tip, after which an unmined transaction is dead
//	inputs     input 0 is the ANCHOR: the vault's change output (the head of its change chain) when there is one,
//	           otherwise the oldest reserve output. Then the reserve's other outputs, largest first.
//	outputs    0: the destination, amount − fee.   1: the vault's change address, the rest.
//	fee        ZIP 317's conventional fee of the transaction with k maximum-size signatures per input, exactly
//
// Why the anchor. Every release spends the head of the change chain and creates the next head. Any two releases a
// signer could ever sign therefore spend a common output, or one spends an output that exists only if the other was
// mined — so at most one of them can ever be in the chain. That holds however the signers' views of the chain differ
// and whatever a co-signer or the coordinator proposes: it is the Zcash consensus, not this code, that refuses the
// second payment. The ledger adds the rule that a signer signs a different transaction for the same withdrawal only
// when this exclusion is shown (mayReplace).
//
// Why the network fee comes out of the withdrawal. vault.bas keeps its own fee (wfee) as the treasury's, counted in
// sup. What leaves the reserve for withdrawal j must be exactly w_<j>_v, the amount Released removes from sup, or the
// reserve would fall behind sup by the network fees and the daemons would halt the vault; so the destination
// receives w_<j>_v less ZIP 317's fee for this transaction.

const (
	// MinChange is the least the change output carries, so the vault's change chain never ends: a withdrawal that
	// would leave less pays up to this much less to its destination (the vault is then down to its last coins).
	MinChange = zcashtx.MinimumFee
	// maxSigLen bounds a DER signature with its SIGHASH byte; fees are computed with it, so the fee a transaction
	// pays never depends on the signatures it happens to carry.
	maxSigLen = 73
)

// buildRules are the parameters every signer of a vault must share for their transactions to be the same.
type buildRules struct {
	K            int
	Depth        uint32 // finality depth
	Epoch        uint32 // blocks per input-selection epoch
	ExpiryWindow uint32
	MaxInputs    int
}

// coin is one vault output that could be spent.
type coin struct {
	Out    zcashtx.OutPoint
	TxID   string // display order
	Vout   uint32
	Value  uint64
	Height uint32
	VS     *VaultScript
}

func (c coin) key() string { return outpointKey(c.TxID, c.Vout) }

func outpointKey(txid string, vout uint32) string {
	return txid + ":" + strconv.FormatUint(uint64(vout), 10)
}

func coinOf(u zcashtx.UTXO, vs *VaultScript) (coin, error) {
	h, err := zcashtx.ParseDisplayHex(u.TxID)
	if err != nil {
		return coin{}, err
	}
	return coin{Out: zcashtx.OutPoint{Hash: h, Index: u.Index}, TxID: u.TxID, Vout: u.Index, Value: u.Value, Height: u.Height, VS: vs}, nil
}

// plan is one release transaction, unsigned, and what it was built from.
type plan struct {
	Tx     *zcashtx.Tx
	Inputs []coin
	Fee    uint64
	Payout uint64
	Change uint64
	Expiry uint32
}

// placeholderScriptSig is the largest scriptSig a vault input spending vs can carry.
func placeholderScriptSig(k int, redeem []byte) []byte {
	sigs := make([][]byte, k)
	for i := range sigs {
		sigs[i] = make([]byte, maxSigLen)
	}
	ss, err := multisigScriptSig(sigs, redeem)
	if err != nil {
		panic(err) // a redeem script NewDescriptor accepted always fits
	}
	return ss
}

// minInputValue is what one vault input adds to ZIP 317's fee, rounded up: an output worth less costs more to spend
// than it brings, so it is never selected (and a flood of such outputs cannot raise anyone's fee).
func minInputValue(k int, vs *VaultScript) uint64 {
	ss := placeholderScriptSig(k, vs.Redeem)
	size := uint64(32 + 4 + len(zcashtx.CompactSize(uint64(len(ss)))) + len(ss) + 4)
	return (size + zcashtx.P2PKHStandardInputSize - 1) / zcashtx.P2PKHStandardInputSize * zcashtx.MarginalFee
}

// epochAnchor is the height input selection is taken at: the finalized height rounded down to the epoch, so signers
// whose tips differ by a few blocks select from the same outputs.
func (r buildRules) epochAnchor(final uint32) uint32 { return final / r.Epoch * r.Epoch }

func (r buildRules) expiry(final uint32) uint32 {
	return r.epochAnchor(final) + r.Depth + r.Epoch + r.ExpiryWindow
}

// buildRelease is the deterministic construction. candidates are the reserve outputs every node agrees on; head is
// the change chain's head when there is one (it is always input 0, however young).
func buildRelease(r buildRules, j uint64, destScript []byte, amount uint64, final, branch uint32, candidates []coin, head *coin, change *VaultScript) (*plan, error) {
	if j >= zcashtx.LockTimeThreshold {
		return nil, fmt.Errorf("zcashvault: withdrawal index %d cannot be written as a lock-time marker", j)
	}
	if amount == 0 || amount > zcashtx.MaxMoney {
		return nil, fmt.Errorf("zcashvault: a withdrawal of %d zatoshi", amount)
	}
	anchorAt := r.epochAnchor(final)
	var eligible []coin
	for _, c := range candidates {
		if head != nil && c.key() == head.key() {
			continue
		}
		if c.Height == 0 || c.Height > anchorAt || c.Value < minInputValue(r.K, c.VS) {
			continue
		}
		eligible = append(eligible, c)
	}
	var anchor coin
	if head != nil {
		anchor = *head
	} else {
		if len(eligible) == 0 {
			return nil, errors.New("zcashvault: no reserve output is final yet")
		}
		sort.Slice(eligible, func(a, b int) bool { return olderThan(eligible[a], eligible[b]) })
		anchor, eligible = eligible[0], eligible[1:]
	}
	sort.Slice(eligible, func(a, b int) bool {
		if eligible[a].Value != eligible[b].Value {
			return eligible[a].Value > eligible[b].Value
		}
		return olderThan(eligible[a], eligible[b])
	})
	inputs := []coin{anchor}
	total := anchor.Value
	for _, c := range eligible {
		if total >= amount+MinChange || len(inputs) >= r.MaxInputs {
			break
		}
		inputs = append(inputs, c)
		total += c.Value
	}
	if total <= amount {
		if len(inputs) >= r.MaxInputs {
			return nil, fmt.Errorf("zcashvault: withdrawal %d needs more than %d inputs; the reserve's largest outputs hold %d of the %d it pays", j, r.MaxInputs, total, amount)
		}
		return nil, fmt.Errorf("zcashvault: the final reserve outputs hold %d zatoshi, not enough for withdrawal %d of %d (younger outputs become spendable as they reach finality)", total, j, amount)
	}
	tx := &zcashtx.Tx{Version: 5, BranchID: branch, LockTime: uint32(j), ExpiryHeight: r.expiry(final)}
	if tx.ExpiryHeight > zcashtx.MaxExpiryHeight {
		return nil, errors.New("zcashvault: the expiry height is past ZIP 203's maximum")
	}
	for _, c := range inputs {
		tx.In = append(tx.In, zcashtx.TxIn{PrevOut: c.Out, ScriptSig: placeholderScriptSig(r.K, c.VS.Redeem), Sequence: zcashtx.SequenceFinal, Value: c.Value, ScriptPubKey: c.VS.ScriptPubKey})
	}
	tx.Out = []zcashtx.TxOut{{ScriptPubKey: destScript}, {ScriptPubKey: change.ScriptPubKey}}
	fee := tx.ConventionalFee()
	p := &plan{Tx: tx, Inputs: inputs, Fee: fee, Expiry: tx.ExpiryHeight}
	if total >= amount+MinChange {
		p.Change = total - amount
		if fee >= amount {
			return nil, fmt.Errorf("zcashvault: the network fee %d is not below the withdrawal %d", fee, amount)
		}
		p.Payout = amount - fee
	} else {
		// the vault's last coins: keep MinChange so the change chain continues; the destination is short by less
		// than MinChange, and the reserve by exactly as much less than w_<j>_v
		p.Change = MinChange
		if total < MinChange+fee+1 {
			return nil, fmt.Errorf("zcashvault: %d zatoshi cannot pay a fee of %d and keep %d", total, fee, MinChange)
		}
		p.Payout = total - MinChange - fee
	}
	tx.Out[0].Value, tx.Out[1].Value = p.Payout, p.Change
	if zcashtx.IsDust(tx.Out[0]) {
		return nil, fmt.Errorf("zcashvault: withdrawal %d would pay %d zatoshi, below the dust threshold", j, p.Payout)
	}
	for i := range tx.In {
		tx.In[i].ScriptSig = nil // unsigned; ZIP 244's txid does not commit to scriptSigs
	}
	if err := checkRelease(tx, r.K, j, destScript, amount, change, func(spk []byte) *VaultScript {
		for _, c := range inputs {
			if bytes.Equal(c.VS.ScriptPubKey, spk) {
				return c.VS
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("zcashvault: the transaction built for withdrawal %d fails its own check: %w", j, err)
	}
	return p, nil
}

func olderThan(a, b coin) bool {
	if a.Height != b.Height {
		return a.Height < b.Height
	}
	if a.TxID != b.TxID {
		return a.TxID < b.TxID
	}
	return a.Vout < b.Vout
}

// checkRelease is the rule a signer applies to any transaction before it signs it, adopts it as a peer's payment,
// or certifies it on DERO: it pays withdrawal j — the lock-time marker is j, output 0 pays the contract's destination
// exactly amount less the fee (or, only when the vault is down to its last coins, at most MinChange less), output 1
// returns the rest to the vault's change address, every input spends a vault address with a final sequence, and the
// fee is exactly ZIP 317's for the transaction with maximum-size signatures. Every input must carry its value and
// scriptPubKey. vault maps a scriptPubKey to the vault address it is, or nil.
func checkRelease(tx *zcashtx.Tx, k int, j uint64, destScript []byte, amount uint64, change *VaultScript, vault func([]byte) *VaultScript) error {
	switch {
	case tx.Version != 5 && tx.Version != 0:
		return fmt.Errorf("version %d; a release is a v5 transaction", tx.Version)
	case tx.Sapling != nil || tx.Orchard != nil:
		return errors.New("a release has no shielded part")
	case uint64(tx.LockTime) != j:
		return fmt.Errorf("its lock-time marker is %d, not withdrawal %d", tx.LockTime, j)
	case tx.ExpiryHeight == 0:
		return errors.New("a release always expires")
	case len(tx.In) == 0:
		return errors.New("no inputs")
	case len(tx.Out) != 2:
		return fmt.Errorf("%d outputs; a release has the destination and the change", len(tx.Out))
	case !bytes.Equal(tx.Out[0].ScriptPubKey, destScript):
		return errors.New("output 0 does not pay the withdrawal's destination")
	case !bytes.Equal(tx.Out[1].ScriptPubKey, change.ScriptPubKey):
		return errors.New("output 1 does not return the change to the vault")
	}
	probe := *tx
	probe.In = append([]zcashtx.TxIn{}, tx.In...)
	for i, in := range tx.In {
		if in.Sequence != zcashtx.SequenceFinal {
			return fmt.Errorf("input %d has a non-final sequence, which would make the lock time bind", i)
		}
		vs := vault(in.ScriptPubKey)
		if vs == nil || in.ScriptPubKey == nil {
			return fmt.Errorf("input %d does not spend a vault address", i)
		}
		probe.In[i].ScriptSig = placeholderScriptSig(k, vs.Redeem)
	}
	fee, err := tx.Fee()
	if err != nil {
		return err
	}
	if want := probe.ConventionalFee(); fee != want {
		return fmt.Errorf("it pays a fee of %d; ZIP 317's for it is %d", fee, want)
	}
	payout, ch := tx.Out[0].Value, tx.Out[1].Value
	switch {
	case payout+fee == amount && ch >= MinChange:
	case ch == MinChange && payout+fee < amount && amount-(payout+fee) < MinChange:
	default:
		return fmt.Errorf("it pays %d with a fee of %d and change %d; withdrawal %d is %d", payout, fee, ch, j, amount)
	}
	if zcashtx.IsDust(tx.Out[0]) {
		return errors.New("output 0 is dust")
	}
	return nil
}
