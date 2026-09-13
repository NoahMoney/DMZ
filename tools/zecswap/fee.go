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
// (Sapling contributes max(spends, outputs), counted for a transaction read with a Sapling bundle; this package
// never builds one.) Every
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
// the output it spends; a transaction with an Orchard bundle is not measured here.
func (t *Tx) Fee() (uint64, error) {
	if t.Orchard != nil || t.Sapling != nil {
		return 0, errors.New("zecswap: the fee of a transaction with a shielded bundle is not computed here")
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

// ---- the swap fee ----
//
// The operator's ruling of 2026-09-13: every swap pays DMZ 0.5 %, in ZEC and never in DERO. The maker, who locks the
// DERO-side asset and receives the ZEC, pays it in the claim that spends the taker's P2SH output with the secret. The
// claim's second output pays SwapFee of the funding's value to the network's treasury address (deploy/networks.json
// zecTreasury), and the network fee still comes out of the maker's own output. A refund never pays it, and nothing
// the taker signs or pays changes. The fee is enforced by this software and by no contract: the script pays the
// maker's key whatever the claim's other outputs are, so a modified maker client can leave the fee out
// (docs/ZEC_SWAP.md §7, "The swap fee").

const (
	// SwapFeeBps is the fee in basis points of the funding: 50 is 0.5 %.
	SwapFeeBps = 50
	// BpsDenominator is 100 % in basis points.
	BpsDenominator = 10_000
	// OneThirdDustThresholdRate is zebrad 6.3.0's ONE_THIRD_DUST_THRESHOLD_RATE (zebra-chain/src/transparent.rs, at
	// tag v6.3.0), in zatoshi per 1,000 bytes. zebrad's mempool refuses a transaction with an output below the
	// threshold it gives (Output::is_dust, called from reject_if_non_standard_tx in
	// zebrad/src/components/mempool/storage.rs; added in Zebra 3.1.0, ZcashFoundation/zebra#10134).
	OneThirdDustThresholdRate = 100
	// TreasuryField is the deployment record's field for the treasury address, named in every refusal.
	TreasuryField = "zecTreasury"
)

// SwapFee is floor(value × SwapFeeBps / BpsDenominator), computed without the product, so no value overflows it.
func SwapFee(value uint64) uint64 {
	return value/BpsDenominator*SwapFeeBps + value%BpsDenominator*SwapFeeBps/BpsDenominator
}

// DustThreshold is zebrad's threshold for out: 3 × (100 × (serialized size + 148) / 1,000), with zebrad's integer
// division. That is 54 zatoshi for a pay-to-public-key-hash output (34 bytes) and for a pay-to-script-hash one (32).
func DustThreshold(out TxOut) uint64 {
	return 3 * (OneThirdDustThresholdRate * (txOutSize(out) + 148) / 1000)
}

// IsDust is zebrad's Output::is_dust: the output's value is below DustThreshold.
func IsDust(out TxOut) bool { return out.Value < DustThreshold(out) }

// TreasuryOutput is the fee output of a claim spending a funding of value, paid to the treasury script, and whether
// the claim carries it. It does not when the fee would be dust by zebrad's rule, because zebrad's mempool would then
// refuse the whole claim. With ZIP 317's 10,000-zatoshi floor and OutputFloor, a claim spends at least 15,000
// zatoshi, whose fee (75) is above the 54-zatoshi threshold, so the omission is reached only if those numbers change.
func TreasuryOutput(value uint64, treasury []byte) (TxOut, bool) {
	o := TxOut{Value: SwapFee(value), ScriptPubKey: treasury}
	return o, !IsDust(o)
}

// TreasuryScript checks a treasury address and returns the script its fee output pays. It must be a transparent
// address on net, P2PKH (t1…, tm…) or P2SH (t3…, t2…), because this package builds transparent transactions only.
func TreasuryScript(addr string, net Network) ([]byte, error) {
	if addr == "" {
		return nil, fmt.Errorf("zecswap: %s is empty", TreasuryField)
	}
	n, kind, h, err := ParseAddress(addr)
	if err != nil {
		return nil, fmt.Errorf("zecswap: %s %q is not a transparent Zcash address: %v", TreasuryField, addr, err)
	}
	if n != net {
		return nil, fmt.Errorf("zecswap: %s %s is a %s address, and this swap is on %s", TreasuryField, addr, n, net)
	}
	if kind == P2SH {
		return P2SHScript(h), nil
	}
	return P2PKHScript(h), nil
}

// CheckTreasury is the companion's rule for the record's zecTreasury before any swap starts. On mainnet it must be a
// valid transparent mainnet address, or no swap starts. Elsewhere it may be empty, and then no fee is taken (the
// caller says so), but it is never accepted invalid.
func CheckTreasury(addr string, mainnet bool) error {
	if addr == "" {
		if mainnet {
			return fmt.Errorf("zecswap: the deployment record's mainnet %s is empty; every mainnet swap pays its 0.5%% fee to that address, so none is started without it (deploy/networks.json)", TreasuryField)
		}
		return nil
	}
	net := Testnet
	if mainnet {
		net = Mainnet
	}
	_, err := TreasuryScript(addr, net)
	return err
}
