package zcashtx

import (
	"errors"
	"fmt"
)

// ZIP 317 (https://zips.z.cash/zip-0317): the conventional fee, computed from what the transaction contains and
// never chosen by the caller. The constants and the formula are the ZIP's, and the sizes are summed the way its
// reference implementation sums them (librustzcash zcash_primitives/src/transaction/fees/zip317.rs,
// fee_required): each input's and each output's own serialized size, without the CompactSize count of the vector.
//
//	logical_actions  = max(⌈tx_in_total_size / 150⌉, ⌈tx_out_total_size / 34⌉) + nOrchardActions
//	conventional_fee = 5,000 · max(2, logical_actions)
const (
	MarginalFee             = 5_000
	GraceActions            = 2
	P2PKHStandardInputSize  = 150
	P2PKHStandardOutputSize = 34
	// MinimumFee is marginal_fee · grace_actions: no transaction pays less.
	MinimumFee = MarginalFee * GraceActions
	// OneThirdDustThresholdRate is zebrad 6.3.0's ONE_THIRD_DUST_THRESHOLD_RATE (zebra-chain/src/transparent.rs), in
	// zatoshi per 1,000 bytes: zebrad's mempool refuses a transaction with an output below the threshold it gives.
	OneThirdDustThresholdRate = 100
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
	if t.Sapling != nil {
		n += uint64(max(len(t.Sapling.Spends), len(t.Sapling.Outputs)))
	}
	return n
}

// ConventionalFee is ZIP 317's conventional fee for this transaction, in zatoshi.
func (t *Tx) ConventionalFee() uint64 {
	return MarginalFee * max(uint64(GraceActions), t.LogicalActions())
}

// Fee is what the transaction pays: the inputs' values less the outputs'. Every input must carry the value of
// the output it spends; a transaction with a shielded bundle is not measured here.
func (t *Tx) Fee() (uint64, error) {
	if t.Orchard != nil || t.Sapling != nil {
		return 0, errors.New("zcashtx: the fee of a transaction with a shielded bundle is not computed here")
	}
	var in, out uint64
	for _, i := range t.In {
		if i.Value > MaxMoney || in+i.Value > MaxMoney {
			return 0, errors.New("zcashtx: the inputs add up to more than the supply")
		}
		in += i.Value
	}
	for _, o := range t.Out {
		if o.Value > MaxMoney || out+o.Value > MaxMoney {
			return 0, errors.New("zcashtx: the outputs add up to more than the supply")
		}
		out += o.Value
	}
	if out > in {
		return 0, fmt.Errorf("zcashtx: the outputs (%d) are more than the inputs (%d)", out, in)
	}
	return in - out, nil
}

// DustThreshold is zebrad's threshold for out: 3 × (100 × (serialized size + 148) / 1,000), with zebrad's integer
// division. That is 54 zatoshi for a pay-to-public-key-hash output (34 bytes) and for a pay-to-script-hash one (32).
func DustThreshold(out TxOut) uint64 {
	return 3 * (OneThirdDustThresholdRate * (txOutSize(out) + 148) / 1000)
}

// IsDust is zebrad's Output::is_dust: the output's value is below DustThreshold.
func IsDust(out TxOut) bool { return out.Value < DustThreshold(out) }
