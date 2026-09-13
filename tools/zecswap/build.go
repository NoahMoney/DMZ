package zecswap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The three transactions of the Zcash leg, built from the layers below them (script.go, sighash.go, sign.go,
// fee.go): the taker's funding of the script, the maker's claim with the secret, and the taker's refund after
// cltv. Each is signed with a key the person hands the companion for that purpose only, checked after signing —
// the signature verifies, the fee is at least ZIP 317's for the bytes actually built, the bytes parse back to the
// same transaction — and returned for the caller to store before it is ever broadcast. Each is a v5 transaction
// unless the request asks for v6 (ZIP 229); the node on regtest accepts both (research/regtest/zecswap-e2e.log).

const (
	// SequenceFinal is the nSequence of an input with nothing to wait for.
	SequenceFinal = 0xffffffff
	// SequenceRefund is the refund input's nSequence: anything but final. BIP 65's CHECKLOCKTIMEVERIFY fails when
	// "the nSequence field of the txin is 0xffffffff", because a final input disables nLockTime altogether.
	SequenceRefund = 0xfffffffe
	// maxSigLen bounds a DER signature with its SIGHASH byte; fees are computed with this size and are therefore
	// never below the fee of the smaller signature actually made.
	maxSigLen = 73
	// OutputFloor is the smallest output this package makes. A change output below it is dropped and its value
	// left to the fee; a claim or refund that would pay less than it is refused.
	OutputFloor = MarginalFee
	// MaxFundingInputs bounds the funding's inputs, and with them the work of building it: a key whose balance
	// is spread over more small outputs than this should be consolidated in the person's own wallet first.
	MaxFundingInputs = 50
)

// RefundFeeLevels are the fee multiples the taker's refund is pre-signed at (docs/ATOMIC_SWAP_STUDY.md §2.2 Z-B):
// ZIP 317's block template takes each transaction on its own fee, with no child-pays-for-parent, so a refund that
// is not mined at one fee can only be replaced by a copy signed at a higher one.
var RefundFeeLevels = []uint64{1, 2, 4}

// txVersion is a request's version: 0 is 5.
func txVersion(v uint32) (uint32, error) {
	switch v {
	case 0, 5:
		return 5, nil
	case 6:
		return 6, nil
	}
	return 0, fmt.Errorf("zecswap: transaction version %d; this package builds v5 and v6", v)
}

// P2PKHScript is OP_DUP OP_HASH160 <pkh> OP_EQUALVERIFY OP_CHECKSIG.
func P2PKHScript(pkh [20]byte) []byte {
	out := []byte{opDup, opHash160}
	out = append(out, push(pkh[:])...)
	return append(out, opEqualVerify, opCheckSig)
}

// P2PKHHash reads the key hash out of a pay-to-public-key-hash script.
func P2PKHHash(spk []byte) ([20]byte, bool) {
	var h [20]byte
	if len(spk) != 25 || spk[0] != opDup || spk[1] != opHash160 || spk[2] != 20 || spk[23] != opEqualVerify || spk[24] != opCheckSig {
		return h, false
	}
	copy(h[:], spk[3:23])
	return h, true
}

// P2SHScript is OP_HASH160 <script hash> OP_EQUAL.
func P2SHScript(sh [20]byte) []byte {
	out := []byte{opHash160}
	out = append(out, push(sh[:])...)
	return append(out, opEqual)
}

// isPayee says whether spk is a P2PKH or a P2SH output script, the only two a treasury output may pay.
func isPayee(spk []byte) bool {
	if _, ok := P2PKHHash(spk); ok {
		return true
	}
	return len(spk) == 23 && spk[0] == opHash160 && spk[1] == 20 && spk[22] == opEqual
}

// ---- keys ----

// WIF version bytes (Zcash's chainparams, the same as Bitcoin's): 0x80 on mainnet, 0xEF on testnet and regtest.
const (
	wifMainnet = 0x80
	wifTestnet = 0xEF
)

// ParseKeyText reads a key as a person exports it: 64 hex characters, or WIF (what zcashd's dumpprivkey writes).
// An uncompressed WIF key is refused: the swap script names HASH160 of the compressed public key, which is the
// only form this package signs with.
func ParseKeyText(text string, net Network) (*Key, error) {
	s := strings.TrimSpace(text)
	var raw [32]byte
	if len(s) == 64 {
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil, errors.New("zecswap: the key is 64 characters but not hex")
		}
		copy(raw[:], b)
		for i := range b {
			b[i] = 0
		}
	} else {
		payload, err := DecodeBase58Check(s)
		if err != nil {
			return nil, fmt.Errorf("zecswap: the key is neither 64 hex characters nor WIF: %v", err)
		}
		defer func() {
			for i := range payload {
				payload[i] = 0
			}
		}()
		switch {
		case len(payload) == 33:
			return nil, errors.New("zecswap: an uncompressed WIF key; export the compressed form, whose public key hash is what the swap script names")
		case len(payload) != 34 || payload[33] != 0x01:
			return nil, errors.New("zecswap: not a WIF private key")
		}
		want := byte(wifTestnet)
		if net == Mainnet {
			want = wifMainnet
		}
		if payload[0] != want {
			return nil, fmt.Errorf("zecswap: the WIF key is for another network than %s", net)
		}
		copy(raw[:], payload[1:33])
	}
	k, err := ParseKey(raw)
	for i := range raw {
		raw[i] = 0
	}
	return k, err
}

// LoadKey reads a key file. The file is the person's own, handed to the companion for this swap's spends only.
func LoadKey(path string, net Network) (*Key, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := range b {
			b[i] = 0
		}
	}()
	return ParseKeyText(string(b), net)
}

// Address is the key's transparent P2PKH address.
func (k *Key) Address(net Network) string { return EncodeAddress(net, P2PKH, k.PubKeyHash()) }

// ---- the funding ----

// Coin is an output the funding can spend.
type Coin struct {
	PrevOut OutPoint
	Value   uint64
	Script  []byte
}

// CoinOf turns a getaddressutxos entry into a Coin.
func CoinOf(u UTXO) (Coin, error) {
	h, err := ParseDisplayHex(u.TxID)
	if err != nil {
		return Coin{}, err
	}
	return Coin{PrevOut: OutPoint{Hash: h, Index: u.Index}, Value: u.Value, Script: u.Script}, nil
}

// FundingRequest is everything the funding is made of.
type FundingRequest struct {
	Key          *Key
	HTLC         HTLC
	Amount       uint64 // exactly the offer's wantAmount
	Coins        []Coin // the key's own confirmed outputs; any other is ignored
	BranchID     uint32 // consensus.nextblock, checked by the caller
	ExpiryHeight uint32 // tip + Policy.FundingExpiry
	Version      uint32 // 5 (or 0) or 6
}

// FundingOutput is the index of the script's output in a funding BuildFunding made.
const FundingOutput = 0

// BuildFunding pays exactly Amount to the script's P2SH output (index 0), returns the change to the key's own
// address, and pays ZIP 317's conventional fee. Coins are taken largest first; the fee is recomputed for every
// input added. A leftover too small to be its own output (below OutputFloor) goes to the fee, which is then above
// the conventional one by less than that floor; nothing else is ever added to it.
func BuildFunding(r FundingRequest) (*Tx, error) {
	if r.Key == nil {
		return nil, errors.New("zecswap: no key")
	}
	ver, err := txVersion(r.Version)
	if err != nil {
		return nil, err
	}
	if r.Amount == 0 || r.Amount > MaxMoney {
		return nil, fmt.Errorf("zecswap: a funding of %d zatoshi", r.Amount)
	}
	if r.ExpiryHeight == 0 || r.ExpiryHeight > MaxExpiryHeight {
		return nil, fmt.Errorf("zecswap: the funding's expiry height %d must be above 0 and at most %d", r.ExpiryHeight, MaxExpiryHeight)
	}
	pkh := r.Key.PubKeyHash()
	own := P2PKHScript(pkh)
	var coins []Coin
	for _, c := range r.Coins {
		if bytes.Equal(c.Script, own) && c.Value > 0 && c.Value <= MaxMoney {
			coins = append(coins, c)
		}
	}
	sort.Slice(coins, func(i, j int) bool {
		if coins[i].Value != coins[j].Value {
			return coins[i].Value > coins[j].Value
		}
		if c := bytes.Compare(coins[i].PrevOut.Hash[:], coins[j].PrevOut.Hash[:]); c != 0 {
			return c < 0
		}
		return coins[i].PrevOut.Index < coins[j].PrevOut.Index
	})
	pub := r.Key.PubKey()
	placeholder := append(push(make([]byte, maxSigLen)), push(make([]byte, len(pub)))...)
	spk := r.HTLC.ScriptPubKey()
	var sum uint64
	for n := 1; n <= len(coins) && n <= MaxFundingInputs; n++ {
		sum += coins[n-1].Value
		if sum > MaxMoney {
			return nil, errors.New("zecswap: the coins add up to more than the supply")
		}
		if sum < r.Amount {
			continue
		}
		tx := &Tx{Version: ver, BranchID: r.BranchID, ExpiryHeight: r.ExpiryHeight}
		for _, c := range coins[:n] {
			tx.In = append(tx.In, TxIn{PrevOut: c.PrevOut, ScriptSig: placeholder, Sequence: SequenceFinal, Value: c.Value, ScriptPubKey: c.Script})
		}
		tx.Out = []TxOut{{Value: r.Amount, ScriptPubKey: spk}, {ScriptPubKey: own}}
		if fee := tx.ConventionalFee(); sum >= r.Amount+fee+OutputFloor {
			tx.Out[1].Value = sum - r.Amount - fee
		} else {
			tx.Out = tx.Out[:1]
			if sum < r.Amount+tx.ConventionalFee() {
				continue
			}
		}
		if err := signP2PKH(tx, r.Key); err != nil {
			return nil, err
		}
		if err := tx.CheckFee(1); err != nil {
			return nil, err
		}
		return tx, roundTrip(tx)
	}
	return nil, fmt.Errorf("zecswap: the key with hash %x holds %d zatoshi in %d confirmed outputs; the funding needs %d plus its fee", pkh[:], sum, len(coins), r.Amount)
}

// signP2PKH signs every input of tx as a P2PKH spend by key. ZIP 244 digests do not commit to scriptSigs, so
// every digest is the same whichever input is signed first.
func signP2PKH(tx *Tx, key *Key) error {
	pub := key.PubKey()
	for i := range tx.In {
		d, err := tx.SigDigest(i, SigHashAll)
		if err != nil {
			return err
		}
		sig := key.Sign(d)
		if !VerifySig(sig, pub, d) {
			return errors.New("zecswap: a signature did not verify against its own key")
		}
		tx.In[i].ScriptSig = append(push(sig), push(pub)...)
	}
	return nil
}

// roundTrip is the last check on anything built here: it parses back to the transaction it was.
func roundTrip(tx *Tx) error {
	raw := tx.Serialize()
	back, err := ParseTx(raw)
	if err != nil {
		return fmt.Errorf("zecswap: a transaction this package built does not parse: %v", err)
	}
	if !bytes.Equal(back.Serialize(), raw) || back.TxID() != tx.TxID() {
		return errors.New("zecswap: a transaction this package built does not round-trip")
	}
	return nil
}

// ---- the two spends of the script ----

// SpendRequest is what both spends of the script are made of.
type SpendRequest struct {
	Key      *Key
	HTLC     HTLC
	Prev     OutPoint // the funding output
	Value    uint64   // its value
	Payout   [20]byte // the P2PKH key hash the spend pays
	BranchID uint32   // consensus.nextblock, checked by the caller
	FeeMult  uint64   // 1 is the conventional fee; the refund ladder uses RefundFeeLevels
	Version  uint32   // 5 (or 0) or 6
	// Treasury is the P2PKH or P2SH script the maker's claim pays the swap fee to (SwapFee of Value, fee.go), as its
	// second output; nil pays none. A refund never pays it: BuildRefund refuses a request that names one.
	Treasury []byte
}

// BuildClaim is the maker's claim: the claim branch, with the secret. nSequence final, nLockTime 0 and
// nExpiryHeight 0 — the claim has no reason to wait and none to expire; the maker's companion alone decides
// when it may still be sent (Policy.ClaimOpen). Output 0 pays the maker, less the network fee; output 1, when
// r.Treasury is set and the fee is not dust (TreasuryOutput), pays the swap fee to the treasury.
func BuildClaim(r SpendRequest, secret [32]byte) (*Tx, error) {
	if r.Key == nil {
		return nil, errors.New("zecswap: no key")
	}
	if r.Treasury != nil && !isPayee(r.Treasury) {
		return nil, errors.New("zecswap: the treasury script is neither P2PKH nor P2SH")
	}
	ver, err := txVersion(r.Version)
	if err != nil {
		return nil, err
	}
	if r.Key.PubKeyHash() != r.HTLC.ClaimPkh {
		return nil, errors.New("zecswap: the key is not the script's claim key")
	}
	if sha256.Sum256(secret[:]) != r.HTLC.Hash {
		return nil, errors.New("zecswap: the secret does not hash to the script's hash")
	}
	tx := &Tx{Version: ver, BranchID: r.BranchID, In: []TxIn{{PrevOut: r.Prev, Sequence: SequenceFinal, Value: r.Value, ScriptPubKey: r.HTLC.ScriptPubKey()}}}
	pub := r.Key.PubKey()
	if err := finishSpend(tx, r, func(sig []byte) []byte { return r.HTLC.ClaimScriptSig(sig, pub, secret) }); err != nil {
		return nil, err
	}
	return tx, CheckClaimTx(tx)
}

// BuildRefund is the taker's refund: the refund branch, valid from height cltv + 1 and never expiring.
// nLockTime = cltv, so CHECKLOCKTIMEVERIFY's "the pushed height is at most nLockTime" holds; nSequence
// SequenceRefund, so the input is not final and nLockTime counts (BIP 65); nExpiryHeight 0, which ZIP 203 defines
// as no expiry, so the transaction can be handed to someone else and broadcast whenever it is needed.
func BuildRefund(r SpendRequest) (*Tx, error) {
	if r.Key == nil {
		return nil, errors.New("zecswap: no key")
	}
	if r.Treasury != nil {
		return nil, errors.New("zecswap: a refund never pays the swap fee: a failed swap is not charged")
	}
	ver, err := txVersion(r.Version)
	if err != nil {
		return nil, err
	}
	if r.Key.PubKeyHash() != r.HTLC.RefundPkh {
		return nil, errors.New("zecswap: the key is not the script's refund key")
	}
	if err := CheckCLTVHeight(r.HTLC.CLTV); err != nil {
		return nil, err
	}
	tx := &Tx{Version: ver, BranchID: r.BranchID, LockTime: r.HTLC.CLTV, In: []TxIn{{PrevOut: r.Prev, Sequence: SequenceRefund, Value: r.Value, ScriptPubKey: r.HTLC.ScriptPubKey()}}}
	pub := r.Key.PubKey()
	if err := finishSpend(tx, r, func(sig []byte) []byte { return r.HTLC.RefundScriptSig(sig, pub) }); err != nil {
		return nil, err
	}
	return tx, CheckRefundTx(tx, r.HTLC)
}

func finishSpend(tx *Tx, r SpendRequest, scriptSig func(sig []byte) []byte) error {
	mult := r.FeeMult
	if mult == 0 {
		mult = 1
	}
	if mult > 16 {
		return fmt.Errorf("zecswap: a fee of %d times the conventional one", mult)
	}
	if r.Value > MaxMoney {
		return errors.New("zecswap: the funding is worth more than the supply")
	}
	tx.Out = []TxOut{{ScriptPubKey: P2PKHScript(r.Payout)}}
	var swapFee uint64
	if r.Treasury != nil {
		if o, ok := TreasuryOutput(r.Value, r.Treasury); ok {
			tx.Out = append(tx.Out, o)
			swapFee = o.Value
		}
	}
	tx.In[0].ScriptSig = scriptSig(make([]byte, maxSigLen))
	// ZIP 317 counts every output, the treasury's included; the network fee comes out of output 0 alone
	fee := tx.ConventionalFee() * mult
	if r.Value < fee+swapFee+OutputFloor {
		return fmt.Errorf("zecswap: the funding of %d zatoshi does not cover a fee of %d, a swap fee of %d and an output of at least %d", r.Value, fee, swapFee, OutputFloor)
	}
	tx.Out[0].Value = r.Value - fee - swapFee
	d, err := tx.SigDigest(0, SigHashAll)
	if err != nil {
		return err
	}
	sig := r.Key.Sign(d)
	if !VerifySig(sig, r.Key.PubKey(), d) {
		return errors.New("zecswap: the spend's signature did not verify against its own key")
	}
	tx.In[0].ScriptSig = scriptSig(sig)
	if err := tx.CheckFee(mult); err != nil {
		return err
	}
	return roundTrip(tx)
}

// CheckRefundTx is every rule a refund must obey to be minable once cltv has passed, and the reason for each.
// It is the table ZEC-7 of the 2026-09-10 audit asked for: a refund that breaks any of them can never be mined,
// and a refund that cannot be mined at cltv is the entry to the loss of Z17.
func CheckRefundTx(tx *Tx, h HTLC) error {
	switch {
	case len(tx.In) != 1:
		return fmt.Errorf("zecswap: a refund spends the one funding output, not %d inputs", len(tx.In))
	case tx.In[0].Sequence == SequenceFinal:
		return errors.New("zecswap: the refund's input is final (nSequence 0xffffffff), so CHECKLOCKTIMEVERIFY fails (BIP 65)")
	case tx.LockTime < h.CLTV:
		return fmt.Errorf("zecswap: the refund's nLockTime %d is below cltv %d, so CHECKLOCKTIMEVERIFY fails", tx.LockTime, h.CLTV)
	case tx.LockTime >= LockTimeThreshold:
		return fmt.Errorf("zecswap: the refund's nLockTime %d is a Unix time, and cltv is a height (BIP 65 refuses to compare the two)", tx.LockTime)
	case tx.ExpiryHeight != 0 && tx.ExpiryHeight <= tx.LockTime:
		return fmt.Errorf("zecswap: the refund's nExpiryHeight %d is at or below its nLockTime %d: ZIP 203 makes it invalid above %d and nLockTime keeps it out of every block up to %d, so no block can hold it", tx.ExpiryHeight, tx.LockTime, tx.ExpiryHeight, tx.LockTime)
	case tx.ExpiryHeight != 0:
		return errors.New("zecswap: the pre-signed refund must not expire (nExpiryHeight 0, ZIP 203): it is handed to someone else to broadcast whenever it is needed")
	}
	return nil
}

// CheckClaimTx is the claim's shape: one input, final, no lock time, no expiry, and one or two outputs (the maker's,
// and the treasury's when the swap fee is paid).
func CheckClaimTx(tx *Tx) error {
	switch {
	case len(tx.In) != 1:
		return fmt.Errorf("zecswap: a claim spends the one funding output, not %d inputs", len(tx.In))
	case len(tx.Out) < 1 || len(tx.Out) > 2:
		return fmt.Errorf("zecswap: a claim pays the maker and at most the treasury, not %d outputs", len(tx.Out))
	case tx.LockTime != 0:
		return errors.New("zecswap: the claim has a lock time")
	case tx.ExpiryHeight != 0:
		return errors.New("zecswap: the claim has an expiry height")
	}
	return nil
}
