package zecswap

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// The Orchard bundle, read and digested but never built. The companion never makes a shielded transaction; it
// must still read one, because a maker's wallet may add an Orchard change output to the claim that reveals
// the secret, and the txid and signature digests commit to the bundle (ZIP 244 T.4). The Sapling bundle is read
// and digested the same way in sapling.go.

const orchardActionSize = 32 + 32 + 32 + 32 + 32 + 580 + 80 // cv nullifier rk cmx ephemeralKey encCiphertext outCiphertext

// OrchardBundle is the wire form of ZIP 225's Orchard fields, kept as bytes.
type OrchardBundle struct {
	Actions       [][]byte // orchardActionSize bytes each
	Flags         byte
	ValueBalance  int64
	Anchor        [32]byte
	Proofs        []byte
	SpendAuthSigs [][]byte // 64 bytes each, one per action
	BindingSig    [64]byte
}

func (o *OrchardBundle) digest() [32]byte {
	if o == nil || len(o.Actions) == 0 {
		return blake256("ZTxIdOrchardHash", nil)
	}
	var compact, memos, noncompact bytes.Buffer
	for _, a := range o.Actions {
		cv, nullifier, rk, cmx, epk, enc, out := a[0:32], a[32:64], a[64:96], a[96:128], a[128:160], a[160:740], a[740:820]
		compact.Write(nullifier)
		compact.Write(cmx)
		compact.Write(epk)
		compact.Write(enc[:52])
		memos.Write(enc[52:564])
		noncompact.Write(cv)
		noncompact.Write(rk)
		noncompact.Write(enc[564:])
		noncompact.Write(out)
	}
	c, m, n := blake256("ZTxIdOrcActCHash", compact.Bytes()), blake256("ZTxIdOrcActMHash", memos.Bytes()), blake256("ZTxIdOrcActNHash", noncompact.Bytes())
	var b bytes.Buffer
	b.Write(c[:])
	b.Write(m[:])
	b.Write(n[:])
	b.WriteByte(o.Flags)
	vb := make([]byte, 8)
	binary.LittleEndian.PutUint64(vb, uint64(o.ValueBalance))
	b.Write(vb)
	b.Write(o.Anchor[:])
	return blake256("ZTxIdOrchardHash", b.Bytes())
}

func (o *OrchardBundle) serialize(b *bytes.Buffer) {
	if o == nil || len(o.Actions) == 0 {
		b.Write(CompactSize(0))
		return
	}
	b.Write(CompactSize(uint64(len(o.Actions))))
	for _, a := range o.Actions {
		b.Write(a)
	}
	b.WriteByte(o.Flags)
	vb := make([]byte, 8)
	binary.LittleEndian.PutUint64(vb, uint64(o.ValueBalance))
	b.Write(vb)
	b.Write(o.Anchor[:])
	b.Write(withLen(o.Proofs))
	for _, s := range o.SpendAuthSigs {
		b.Write(s)
	}
	b.Write(o.BindingSig[:])
}

func parseOrchard(r *reader) (*OrchardBundle, error) {
	n := r.compact()
	if r.err != nil {
		return nil, r.err
	}
	if n == 0 {
		return nil, nil
	}
	if n > 1<<16 {
		return nil, errors.New("zecswap: implausible Orchard action count")
	}
	o := &OrchardBundle{}
	for i := uint64(0); i < n; i++ {
		a := r.bytes(orchardActionSize)
		if a == nil {
			return nil, r.err
		}
		o.Actions = append(o.Actions, append([]byte{}, a...))
	}
	f := r.bytes(1)
	if f == nil {
		return nil, r.err
	}
	o.Flags = f[0]
	o.ValueBalance = int64(r.u64())
	copy(o.Anchor[:], r.bytes(32))
	o.Proofs = append([]byte{}, r.varBytes()...)
	for i := uint64(0); i < n; i++ {
		s := r.bytes(64)
		if s == nil {
			return nil, r.err
		}
		o.SpendAuthSigs = append(o.SpendAuthSigs, append([]byte{}, s...))
	}
	copy(o.BindingSig[:], r.bytes(64))
	return o, r.err
}
