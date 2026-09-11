package zecswap

import (
	"errors"
	"fmt"
)

// ZIP 317 (https://zips.z.cash/zip-0317): the conventional fee, computed from what the transaction contains and
// never chosen by the caller. The constants and the formula are the ZIP's, and the sizes are summed the way its
// reference implementation sums them (librustzcash zcash_primitives/src/transaction/fees/zip317.rs,
// fee_required): each input's and each output's own serialized size, without the CompactSize count of the vector
// — which is why a pay-to-public-key-hash output is 34 bytes (8 value, 1 length, 25 script).
//
//	logical_actions  = max(⌈tx_in_total_size / 150⌉, ⌈tx_out_total_size / 34⌉) + nOrchardActions
//	conventional_fee = 5,000 · max(2, logical_actions)
//
// (Sapling contributes max(spends, outputs); this package neither builds nor reads a Sapling bundle.) Every
// transaction here — the funding, the claim, the refund — is built, its fee computed from its own bytes, and
// checked again after signing: the fee is at least the conventional fee of the transaction actually sent.
const (
	MarginalFee             = 5_000
	GraceActions            = 2
	P2PKHStandardInputSize  = 150
	P2PKHStandardOutputSize = 34
	// MinimumFee is marginal_fee · grace_actions: no transaction pays less.
	MinimumFee = MarginalFee * GraceActions
)

func txInSize(in TxIn) uint64 {
	return 32 + 4 + uint64(len(CompactSize(uint64(len(in.ScriptSig))))) + uint64(len(in.ScriptSig)) + 4
}

func txOutSize(out TxOut) uint64 {
	return 8 + uint64(len(CompactSize(uint64(len(out.ScriptPubKey))))) + uint64(len(out.ScriptPubKey))
}

// LogicalActions is ZIP 317's count for this transaction.
func (t *Tx) LogicalActions() uint64 {
	var in, out uint64
	for _, i := range t.In {
		in += txInSize(i)
	}
	for _, o := range t.Out {
		out += txOutSize(o)
	}
	ceil := func(a, b uint64) uint64 { return (a + b - 1) / b }
	n := max(ceil(in, P2PKHStandardInputSize), ceil(out, P2PKHStandardOutputSize))
	if t.Orchard != nil {
		n += uint64(len(t.Orchard.Actions))
	}
	return n
}

// ConventionalFee is ZIP 317's conventional fee for this transaction, in zatoshi.
func (t *Tx) ConventionalFee() uint64 {
	return MarginalFee * max(uint64(GraceActions), t.LogicalActions())
}

// Fee is what the transaction pays: the inputs' values less the outputs'. Every input must carry the value of
// the output it spends; a transaction with an Orchard bundle is not measured here.
func (t *Tx) Fee() (uint64, error) {
	if t.Orchard != nil {
		return 0, errors.New("zecswap: the fee of a transaction with an Orchard bundle is not computed here")
	}
	var in, out uint64
	for _, i := range t.In {
		if i.Value > MaxMoney || in+i.Value > MaxMoney {
			return 0, errors.New("zecswap: the inputs add up to more than the supply")
		}
		in += i.Value
	}
	for _, o := range t.Out {
		if o.Value > MaxMoney || out+o.Value > MaxMoney {
			return 0, errors.New("zecswap: the outputs add up to more than the supply")
		}
		out += o.Value
	}
	if out > in {
		return 0, fmt.Errorf("zecswap: the outputs (%d) are more than the inputs (%d)", out, in)
	}
	return in - out, nil
}

// CheckFee refuses a transaction that pays less than its conventional fee times mult.
func (t *Tx) CheckFee(mult uint64) error {
	fee, err := t.Fee()
	if err != nil {
		return err
	}
	if want := t.ConventionalFee() * mult; fee < want {
		return fmt.Errorf("zecswap: the transaction pays %d zatoshi, below ZIP 317's %d for its %d logical actions (×%d)", fee, want, t.LogicalActions(), mult)
	}
	return nil
}
