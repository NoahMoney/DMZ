package zecswap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// Version-5 transactions (ZIP 225) with transparent inputs and outputs only, and their ZIP 244 digests: the
// transaction id and the SIGHASH_ALL signature digest of a transparent input. That is all a swap needs — the
// funding of the script, the claim, the refund — and nothing shielded is ever built here (the design note says
// why: the market's Zcash leg is transparent by construction). The digest tree follows ZIP 244 exactly; the
// official test vectors (testdata/zip_0244.json) pin every byte of it, on the vectors that carry no shielded
// bundle.

const (
	// V5Version is nVersion 5 with the fOverwintered bit set, as serialized.
	V5Version = 0x80000005
	// V5VersionGroupID is nVersionGroupId for v5 transactions.
	V5VersionGroupID = 0x26A7270A
	// BranchNU5 and BranchNU6 are consensus branch ids. The one to sign under is the chain's current epoch,
	// read from the node (getblockchaininfo → consensus.chaintip); a wrong id makes the signature invalid,
	// never a loss.
	BranchNU5 = 0xC2D6D0B4
	BranchNU6 = 0xC8E71055
)

// OutPoint names a transparent output being spent.
type OutPoint struct {
	Hash  [32]byte // the previous transaction's id, as serialized (not display order)
	Index uint32
}

// TxIn is a transparent input. Value and ScriptPubKey describe the output it spends; ZIP 244 commits to both
// in the signature digest, which is what lets a signer prove what it thought it was spending.
type TxIn struct {
	PrevOut      OutPoint
	ScriptSig    []byte
	Sequence     uint32
	Value        uint64
	ScriptPubKey []byte
}

// TxOut is a transparent output.
type TxOut struct {
	Value        uint64
	ScriptPubKey []byte
}

// Tx is a v5 transaction with transparent parts and, when read from the chain, an Orchard bundle; never a
// Sapling one. The companion builds only transparent transactions (Orchard stays nil).
type Tx struct {
	BranchID     uint32
	LockTime     uint32
	ExpiryHeight uint32
	In           []TxIn
	Out          []TxOut
	Orchard      *OrchardBundle
}

func blake256(person string, data []byte) [32]byte {
	if len(person) != 16 {
		panic("zecswap: personalization must be 16 bytes: " + person)
	}
	var out [32]byte
	copy(out[:], blake2bSum(32, []byte(person), data))
	return out
}

func le32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func le64(v uint64) []byte { b := make([]byte, 8); binary.LittleEndian.PutUint64(b, v); return b }

// CompactSize is Bitcoin's variable-length count.
func CompactSize(n uint64) []byte {
	switch {
	case n < 0xfd:
		return []byte{byte(n)}
	case n <= 0xffff:
		return append([]byte{0xfd}, byte(n), byte(n>>8))
	case n <= 0xffffffff:
		return append([]byte{0xfe}, le32(uint32(n))...)
	}
	return append([]byte{0xff}, le64(n)...)
}

func withLen(b []byte) []byte { return append(CompactSize(uint64(len(b))), b...) }

// ---- ZIP 244 digests --------------------------------------------------------------------------------------

func (t *Tx) headerDigest() [32]byte {
	var b bytes.Buffer
	b.Write(le32(V5Version))
	b.Write(le32(V5VersionGroupID))
	b.Write(le32(t.BranchID))
	b.Write(le32(t.LockTime))
	b.Write(le32(t.ExpiryHeight))
	return blake256("ZTxIdHeadersHash", b.Bytes())
}

func (t *Tx) prevoutsDigest() [32]byte {
	var b bytes.Buffer
	for _, in := range t.In {
		b.Write(in.PrevOut.Hash[:])
		b.Write(le32(in.PrevOut.Index))
	}
	return blake256("ZTxIdPrevoutHash", b.Bytes())
}

func (t *Tx) sequenceDigest() [32]byte {
	var b bytes.Buffer
	for _, in := range t.In {
		b.Write(le32(in.Sequence))
	}
	return blake256("ZTxIdSequencHash", b.Bytes())
}

func (t *Tx) outputsDigest() [32]byte {
	var b bytes.Buffer
	for _, out := range t.Out {
		b.Write(le64(out.Value))
		b.Write(withLen(out.ScriptPubKey))
	}
	return blake256("ZTxIdOutputsHash", b.Bytes())
}

func (t *Tx) transparentDigest() [32]byte {
	if len(t.In) == 0 && len(t.Out) == 0 {
		return blake256("ZTxIdTranspaHash", nil)
	}
	var b bytes.Buffer
	p, s, o := t.prevoutsDigest(), t.sequenceDigest(), t.outputsDigest()
	b.Write(p[:])
	b.Write(s[:])
	b.Write(o[:])
	return blake256("ZTxIdTranspaHash", b.Bytes())
}

func saplingEmptyDigest() [32]byte { return blake256("ZTxIdSaplingHash", nil) }

func (t *Tx) txPerson() string { return "ZcashTxHash_" + string(le32(t.BranchID)) }

// TxID is the ZIP 244 transaction id: the four digests under the branch-personalized hash. Explorers show it
// byte-reversed; this is the serialized order, the one a prevout carries.
func (t *Tx) TxID() [32]byte {
	var b bytes.Buffer
	h, tr, sa, or := t.headerDigest(), t.transparentDigest(), saplingEmptyDigest(), t.Orchard.digest()
	b.Write(h[:])
	b.Write(tr[:])
	b.Write(sa[:])
	b.Write(or[:])
	return blake256(t.txPerson(), b.Bytes())
}

// SigDigest is the ZIP 244 signature digest for transparent input i under SIGHASH_ALL: what the input's key
// signs. Every input's amount and scriptPubKey must be filled in (the vectors and the node both supply them).
func (t *Tx) SigDigest(i int, hashType byte) ([32]byte, error) {
	var zero [32]byte
	if hashType != SigHashAll {
		return zero, errors.New("zecswap: only SIGHASH_ALL is signed")
	}
	if i < 0 || i >= len(t.In) {
		return zero, fmt.Errorf("zecswap: input %d of %d", i, len(t.In))
	}
	var amounts, scripts bytes.Buffer
	for _, in := range t.In {
		if in.ScriptPubKey == nil {
			return zero, errors.New("zecswap: every input needs the scriptPubKey it spends")
		}
		amounts.Write(le64(in.Value))
		scripts.Write(withLen(in.ScriptPubKey))
	}
	in := t.In[i]
	var txin bytes.Buffer
	txin.Write(in.PrevOut.Hash[:])
	txin.Write(le32(in.PrevOut.Index))
	txin.Write(le64(in.Value))
	txin.Write(withLen(in.ScriptPubKey))
	txin.Write(le32(in.Sequence))

	var tb bytes.Buffer
	tb.WriteByte(hashType)
	p, a, s, q, o, x := t.prevoutsDigest(), blake256("ZTxTrAmountsHash", amounts.Bytes()), blake256("ZTxTrScriptsHash", scripts.Bytes()), t.sequenceDigest(), t.outputsDigest(), blake256("Zcash___TxInHash", txin.Bytes())
	tb.Write(p[:])
	tb.Write(a[:])
	tb.Write(s[:])
	tb.Write(q[:])
	tb.Write(o[:])
	tb.Write(x[:])
	transparentSig := blake256("ZTxIdTranspaHash", tb.Bytes())

	var b bytes.Buffer
	h, sa, or := t.headerDigest(), saplingEmptyDigest(), t.Orchard.digest()
	b.Write(h[:])
	b.Write(transparentSig[:])
	b.Write(sa[:])
	b.Write(or[:])
	return blake256(t.txPerson(), b.Bytes()), nil
}

// ---- serialization (ZIP 225, no shielded bundle) ------------------------------------------------------------

// Serialize is the wire form: header, transparent inputs and outputs, then the empty Sapling bundle (zero
// spends, zero outputs, nothing else present) and the empty Orchard bundle (zero actions).
func (t *Tx) Serialize() []byte {
	var b bytes.Buffer
	b.Write(le32(V5Version))
	b.Write(le32(V5VersionGroupID))
	b.Write(le32(t.BranchID))
	b.Write(le32(t.LockTime))
	b.Write(le32(t.ExpiryHeight))
	b.Write(CompactSize(uint64(len(t.In))))
	for _, in := range t.In {
		b.Write(in.PrevOut.Hash[:])
		b.Write(le32(in.PrevOut.Index))
		b.Write(withLen(in.ScriptSig))
		b.Write(le32(in.Sequence))
	}
	b.Write(CompactSize(uint64(len(t.Out))))
	for _, out := range t.Out {
		b.Write(le64(out.Value))
		b.Write(withLen(out.ScriptPubKey))
	}
	b.Write(CompactSize(0)) // nSpendsSapling
	b.Write(CompactSize(0)) // nOutputsSapling
	t.Orchard.serialize(&b)
	return b.Bytes()
}

// ErrShielded is returned by ParseV5 for a transaction that carries a Sapling bundle: this package does not
// read those and says so rather than misreading them.
var ErrShielded = errors.New("zecswap: the transaction has a Sapling bundle")

// ParseV5 reads a serialized v5 transaction with transparent parts and at most an Orchard bundle. Inputs come
// back without Value and ScriptPubKey (the wire form does not carry them); fill them in from the outputs being
// spent before asking for a signature digest.
func ParseV5(raw []byte) (*Tx, error) {
	r := &reader{b: raw}
	if v := r.u32(); v != V5Version {
		return nil, fmt.Errorf("zecswap: version %08x is not v5", v)
	}
	if g := r.u32(); g != V5VersionGroupID {
		return nil, fmt.Errorf("zecswap: version group %08x is not v5", g)
	}
	t := &Tx{BranchID: r.u32(), LockTime: r.u32(), ExpiryHeight: r.u32()}
	nIn := r.compact()
	for i := uint64(0); i < nIn && r.err == nil; i++ {
		var in TxIn
		copy(in.PrevOut.Hash[:], r.bytes(32))
		in.PrevOut.Index = r.u32()
		in.ScriptSig = r.bytes(int(r.compact()))
		in.Sequence = r.u32()
		t.In = append(t.In, in)
	}
	nOut := r.compact()
	for i := uint64(0); i < nOut && r.err == nil; i++ {
		var out TxOut
		out.Value = r.u64()
		out.ScriptPubKey = r.bytes(int(r.compact()))
		t.Out = append(t.Out, out)
	}
	if r.err != nil {
		return nil, r.err
	}
	nSpends, nOutputs := r.compact(), r.compact() // nSpendsSapling, nOutputsSapling
	if r.err != nil {
		return nil, r.err
	}
	if nSpends != 0 || nOutputs != 0 {
		return nil, ErrShielded
	}
	orchard, err := parseOrchard(r)
	if err != nil {
		return nil, err
	}
	t.Orchard = orchard
	if r.p != len(raw) {
		return nil, fmt.Errorf("zecswap: %d trailing bytes", len(raw)-r.p)
	}
	return t, nil
}

type reader struct {
	b   []byte
	p   int
	err error
}

func (r *reader) bytes(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.p+n > len(r.b) {
		r.err = errors.New("zecswap: transaction truncated")
		return nil
	}
	out := r.b[r.p : r.p+n]
	r.p += n
	return out
}
func (r *reader) u32() uint32 {
	b := r.bytes(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}
func (r *reader) u64() uint64 {
	b := r.bytes(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
func (r *reader) compact() uint64 {
	b := r.bytes(1)
	if b == nil {
		return 0
	}
	switch b[0] {
	case 0xfd:
		x := r.bytes(2)
		if x == nil {
			return 0
		}
		return uint64(binary.LittleEndian.Uint16(x))
	case 0xfe:
		return uint64(r.u32())
	case 0xff:
		return r.u64()
	}
	return uint64(b[0])
}
