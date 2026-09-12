package zecswap

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// The Sapling bundle of a v5 transaction (ZIP 225), read and digested (ZIP 244 T.3) but never built.
//
// The companion never makes a shielded transaction. A counterparty's wallet may still put Sapling parts in the
// funding it sends, or in the claim that reveals the secret, and a v5 transaction's id commits to them. Reading the
// bundle and computing its digest lets this package compute the id of every v5 transaction from its bytes, so none is
// taken on the node's word (GAP-039), and the header gate (spv.go) can prove such a funding in its block. Every
// Sapling-bearing vector of the Zcash project's own zip_0244.json pins the digest, the transaction id and the
// SIGHASH_ALL digest to the byte (TestZIP244Vectors).
//
// A v6 transaction with shielded parts is still refused (ErrShielded): ZIP 229's digests for its shielded bundles
// have no test vectors here to pin an implementation to.

const (
	saplingSpendSize  = 32 + 32 + 32            // cv, nullifier, rk
	saplingOutputSize = 32 + 32 + 32 + 580 + 80 // cv, cmu, ephemeralKey, encCiphertext, outCiphertext
	saplingProofSize  = 192                     // Groth16
	saplingSigSize    = 64                      // spendAuthSig, bindingSig
	maxSaplingCount   = 1 << 16                 // far above what fits in a 2 MB block
)

// SaplingBundle is the wire form of ZIP 225's Sapling fields, kept as bytes.
type SaplingBundle struct {
	Spends        [][]byte // saplingSpendSize bytes each
	Outputs       [][]byte // saplingOutputSize bytes each
	ValueBalance  int64
	Anchor        [32]byte // present on the wire only when there is at least one spend
	SpendProofs   [][]byte
	SpendAuthSigs [][]byte
	OutputProofs  [][]byte
	BindingSig    [64]byte
}

// digest is ZIP 244's sapling_digest (T.3).
func (s *SaplingBundle) digest() [32]byte {
	if s == nil || len(s.Spends)+len(s.Outputs) == 0 {
		return blake256("ZTxIdSaplingHash", nil)
	}
	spends := blake256("ZTxIdSSpendsHash", nil)
	if len(s.Spends) > 0 {
		var compact, noncompact bytes.Buffer
		for _, sp := range s.Spends {
			cv, nullifier, rk := sp[0:32], sp[32:64], sp[64:96]
			compact.Write(nullifier)
			noncompact.Write(cv)
			noncompact.Write(s.Anchor[:])
			noncompact.Write(rk)
		}
		c, n := blake256("ZTxIdSSpendCHash", compact.Bytes()), blake256("ZTxIdSSpendNHash", noncompact.Bytes())
		spends = blake256("ZTxIdSSpendsHash", append(c[:], n[:]...))
	}
	outputs := blake256("ZTxIdSOutputHash", nil)
	if len(s.Outputs) > 0 {
		var compact, memos, noncompact bytes.Buffer
		for _, o := range s.Outputs {
			cv, cmu, epk, enc, out := o[0:32], o[32:64], o[64:96], o[96:676], o[676:756]
			compact.Write(cmu)
			compact.Write(epk)
			compact.Write(enc[:52])
			memos.Write(enc[52:564])
			noncompact.Write(cv)
			noncompact.Write(enc[564:])
			noncompact.Write(out)
		}
		c, m, n := blake256("ZTxIdSOutC__Hash", compact.Bytes()), blake256("ZTxIdSOutM__Hash", memos.Bytes()), blake256("ZTxIdSOutN__Hash", noncompact.Bytes())
		var b bytes.Buffer
		b.Write(c[:])
		b.Write(m[:])
		b.Write(n[:])
		outputs = blake256("ZTxIdSOutputHash", b.Bytes())
	}
	var b bytes.Buffer
	b.Write(spends[:])
	b.Write(outputs[:])
	vb := make([]byte, 8)
	binary.LittleEndian.PutUint64(vb, uint64(s.ValueBalance))
	b.Write(vb)
	return blake256("ZTxIdSaplingHash", b.Bytes())
}

// serialize writes the bundle in ZIP 225's order; a nil bundle is its two zero counts.
func (s *SaplingBundle) serialize(b *bytes.Buffer) {
	if s == nil {
		b.Write(CompactSize(0))
		b.Write(CompactSize(0))
		return
	}
	b.Write(CompactSize(uint64(len(s.Spends))))
	for _, sp := range s.Spends {
		b.Write(sp)
	}
	b.Write(CompactSize(uint64(len(s.Outputs))))
	for _, o := range s.Outputs {
		b.Write(o)
	}
	if len(s.Spends)+len(s.Outputs) == 0 {
		return
	}
	b.Write(le64(uint64(s.ValueBalance)))
	if len(s.Spends) > 0 {
		b.Write(s.Anchor[:])
	}
	for _, p := range s.SpendProofs {
		b.Write(p)
	}
	for _, sig := range s.SpendAuthSigs {
		b.Write(sig)
	}
	for _, p := range s.OutputProofs {
		b.Write(p)
	}
	b.Write(s.BindingSig[:])
}

// parseSapling reads the Sapling fields of a v5 or v6 transaction; nil when both counts are zero.
func parseSapling(r *reader) (*SaplingBundle, error) {
	fixed := func(n uint64, size int) ([][]byte, error) {
		var out [][]byte
		for i := uint64(0); i < n; i++ {
			a := r.bytes(size)
			if a == nil {
				return nil, r.err
			}
			out = append(out, append([]byte(nil), a...))
		}
		return out, nil
	}
	nSpends := r.compact()
	if r.err != nil {
		return nil, r.err
	}
	if nSpends > maxSaplingCount {
		return nil, errors.New("zecswap: implausible Sapling spend count")
	}
	s := &SaplingBundle{}
	var err error
	if s.Spends, err = fixed(nSpends, saplingSpendSize); err != nil {
		return nil, err
	}
	nOutputs := r.compact()
	if r.err != nil {
		return nil, r.err
	}
	if nOutputs > maxSaplingCount {
		return nil, errors.New("zecswap: implausible Sapling output count")
	}
	if s.Outputs, err = fixed(nOutputs, saplingOutputSize); err != nil {
		return nil, err
	}
	if nSpends+nOutputs == 0 {
		return nil, nil
	}
	s.ValueBalance = int64(r.u64())
	if nSpends > 0 {
		copy(s.Anchor[:], r.bytes(32))
	}
	if s.SpendProofs, err = fixed(nSpends, saplingProofSize); err != nil {
		return nil, err
	}
	if s.SpendAuthSigs, err = fixed(nSpends, saplingSigSize); err != nil {
		return nil, err
	}
	if s.OutputProofs, err = fixed(nOutputs, saplingProofSize); err != nil {
		return nil, err
	}
	copy(s.BindingSig[:], r.bytes(64))
	return s, r.err
}
